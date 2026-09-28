package services

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"pvfine/internal/pvf"
)

type stringGuardFixture struct {
	core      *core
	archive   *pvf.Archive
	safe      int32 // 用安全表 1 的装备脚本
	protected int32 // 用禁动表 3 的装备脚本
}

// newStringGuardFixture 造一个同时含安全表与禁动表的归档：
// 表 1（String/Common.uv.str）是安全表，表 3（String/equipment.uv.str）在禁动名单内。
func newStringGuardFixture(t *testing.T) stringGuardFixture {
	t.Helper()
	// 本夹具验证的是"开启写保护之后的行为"；而总开关默认是关闭的
	// （2026-09-24 用户要求默认不拦截），所以这里显式打开，用例结束后恢复默认。
	setStringTableGuardEnabled(true)
	t.Cleanup(func() { setStringTableGuardEnabled(false) })
	a := pvf.New()
	encode := func(s string) []byte {
		out := make([]byte, 0, len(s)*2)
		for _, r := range s {
			out = append(out, byte(r), byte(r>>8))
		}
		return out
	}
	mustAddText(t, a, "list/n_string.lst",
		"1 `String/Common.uv.str` 3 `String/equipment.uv.str`", pvf.TypeScript)
	// .str 在真实归档里是 TypeUnicode（UTF-16LE 文本），按 TypeScript 加会被
	// 当成 token 流解码——这里保持与实际一致，否则批处理的文本匹配会失真。
	a.AddFile("String/Common.uv.str", encode("name_10018>安全表装备名\r\n"), pvf.TypeUnicode)
	a.AddFile("String/equipment.uv.str", encode("name_10019>禁动表装备名\r\n"), pvf.TypeUnicode)
	mustAddText(t, a, "equipment/equipment.lst",
		"10018 `character/vest.equ` 10019 `character/vest2.equ`", pvf.TypeScript)
	safe, err := a.AddFileText("equipment/character/vest.equ",
		"[name]\n{8=`<1::name_10018>`}", pvf.TypeScript)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := a.AddFileText("equipment/character/vest2.equ",
		"[name]\n{8=`<3::name_10019>`}", pvf.TypeScript)
	if err != nil {
		t.Fatal(err)
	}

	c := NewCore()
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)
	return stringGuardFixture{core: c, archive: a, safe: safe, protected: protected}
}

func assertRawUnchanged(t *testing.T, before, after [][]byte, context string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("%s：文件数变化 %d -> %d", context, len(before), len(after))
	}
	for i := range before {
		if !bytes.Equal(before[i], after[i]) {
			t.Fatalf("%s：文件 %d 的字节被改动", context, i)
		}
	}
}

// 2026-09-24：写保护总开关默认关闭 —— 关闭时禁动表写入必须放行（"默认去掉限制"）。
func TestStringGuardDisabledAllowsProtectedTableWrite(t *testing.T) {
	fixture := newStringGuardFixture(t)
	setStringTableGuardEnabled(false)
	if err := NewEditorService(fixture.core).SetPlaceholderText(fixture.protected, 3, "name_10019", "关闭保护后写入"); err != nil {
		t.Fatalf("写保护关闭时仍被拦截: %v", err)
	}
	if got, ok := fixture.archive.LookupStringTable(3, "name_10019"); !ok || got != "关闭保护后写入" {
		t.Errorf("关闭保护后写入未生效: %q %v", got, ok)
	}
}

// 总开关开启时恢复拦截（用户在设置里把开关打开后的行为）。
func TestStringGuardEnabledBlocksProtectedTableWrite(t *testing.T) {
	fixture := newStringGuardFixture(t)
	setStringTableGuardEnabled(true)
	err := NewEditorService(fixture.core).SetPlaceholderText(fixture.protected, 3, "name_10019", "开启保护后写入")
	var blocked *ProtectedStringTableError
	if !errors.As(err, &blocked) {
		t.Fatalf("写保护开启时未拦截: %v", err)
	}
}

// 复现 0.38 乱码事故：把译文写进禁动表 3 必须被拦截，且归档一个字节都不能变。
func TestStringGuardBlocksProtectedTableWrite(t *testing.T) {
	fixture := newStringGuardFixture(t)
	before := rawSnapshot(t, fixture.archive)

	err := NewEditorService(fixture.core).SetPlaceholderText(fixture.protected, 3, "name_10019", "改过的名字")
	if err == nil {
		t.Fatal("写入禁动表 3 未被拦截")
	}
	var blocked *ProtectedStringTableError
	if !errors.As(err, &blocked) {
		t.Fatalf("错误类型 = %T: %v", err, err)
	}
	if blocked.TableIndex != 3 {
		t.Errorf("拦截的表号 = %d", blocked.TableIndex)
	}
	if !strings.Contains(err.Error(), "禁动") || blocked.Hint == "" {
		t.Errorf("拦截信息缺少原因或建议: %v / %+v", err, blocked)
	}
	if got, ok := fixture.archive.LookupStringTable(3, "name_10019"); !ok || got != "禁动表装备名" {
		t.Errorf("禁动表内容被改动: %q %v", got, ok)
	}
	assertRawUnchanged(t, before, rawSnapshot(t, fixture.archive), "禁动表写入")
}

// 安全表 1 不能被误拦。
func TestStringGuardAllowsSafeTableWrite(t *testing.T) {
	fixture := newStringGuardFixture(t)
	if err := NewEditorService(fixture.core).SetPlaceholderText(fixture.safe, 1, "name_10018", "改过的安全名"); err != nil {
		t.Fatalf("安全表 1 被误拦: %v", err)
	}
	if got, ok := fixture.archive.LookupStringTable(1, "name_10018"); !ok || got != "改过的安全名" {
		t.Errorf("安全表写入未生效: %q %v", got, ok)
	}
}

// 直接在编辑器里打开禁动 .str 改文本，同样必须被拦截。
func TestStringGuardBlocksDirectStringTableEdit(t *testing.T) {
	fixture := newStringGuardFixture(t)
	index, ok := fixture.archive.Find("String/equipment.uv.str")
	if !ok {
		t.Fatal("禁动表缺失")
	}
	before := rawSnapshot(t, fixture.archive)

	err := NewEditorService(fixture.core).SetText(index, "name_10019>手改的\r\n")
	if err == nil {
		t.Fatal("直接编辑禁动 .str 未被拦截")
	}
	var blocked *ProtectedStringTableError
	if !errors.As(err, &blocked) {
		t.Fatalf("错误类型 = %T: %v", err, err)
	}
	if !strings.EqualFold(blocked.Path, "String/equipment.uv.str") {
		t.Errorf("拦截路径 = %q", blocked.Path)
	}
	assertRawUnchanged(t, before, rawSnapshot(t, fixture.archive), "禁动 .str 直接编辑")
}

// 反向用例：普通脚本（非字符串表）的编辑不能被误拦。
func TestStringGuardAllowsOrdinaryFileEdit(t *testing.T) {
	fixture := newStringGuardFixture(t)
	if err := NewEditorService(fixture.core).SetText(fixture.safe, "[name]\n`内联名`"); err != nil {
		t.Fatalf("普通脚本编辑被误拦: %v", err)
	}
	if err := NewEditorService(fixture.core).SetText(fixture.protected, "[name]\n`内联名2`"); err != nil {
		t.Fatalf("普通脚本编辑被误拦: %v", err)
	}
}

// 未登记表号（无法确认安全）同样拒绝。
func TestStringGuardBlocksUnregisteredTable(t *testing.T) {
	fixture := newStringGuardFixture(t)
	err := NewEditorService(fixture.core).SetPlaceholderText(fixture.safe, 99, "name_10018", "x")
	if err == nil {
		t.Fatal("未登记表号 99 未被拦截")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("错误信息未提到表号: %v", err)
	}
}

// 删除禁动表同样被拦截（删掉客户端直接读不到显示文本）。
func TestStringGuardBlocksProtectedDelete(t *testing.T) {
	fixture := newStringGuardFixture(t)
	index, ok := fixture.archive.Find("String/equipment.uv.str")
	if !ok {
		t.Fatal("禁动表缺失")
	}
	if _, err := NewArchiveService(fixture.core).DeleteFiles([]int32{index}); err == nil {
		t.Fatal("删除禁动 .str 未被拦截")
	}
	if _, ok := fixture.archive.Find("String/equipment.uv.str"); !ok {
		t.Error("禁动 .str 被删除")
	}
}

// 批处理不能成为绕过写保护的旁路：被拦文件在预览里以"跳过 + 原因"呈现。
func TestStringGuardBlocksBatchTarget(t *testing.T) {
	fixture := newStringGuardFixture(t)
	service := NewBatchService(fixture.core)

	page, err := service.Preview(BatchRequest{
		Mode:  BatchModeText,
		Paths: []string{"String/equipment.uv.str", "String/Common.uv.str"},
		Text:  &TextReplaceSpec{Find: "装备名", Replacement: "改过的"},
	})
	if err != nil {
		t.Fatal(err)
	}
	blockedRows, safeRows := 0, 0
	for _, row := range page.Rows {
		switch {
		case strings.EqualFold(row.Path, "String/equipment.uv.str"):
			blockedRows++
			if row.Status != BatchFileSkipped || !strings.Contains(row.Reason, "已拦截") {
				t.Errorf("禁动表行未被拦截: status=%s reason=%s", row.Status, row.Reason)
			}
		case strings.EqualFold(row.Path, "String/Common.uv.str"):
			safeRows++
		}
	}
	if blockedRows != 1 || safeRows != 1 {
		for _, row := range page.Rows {
			t.Logf("预览行: path=%s status=%s reason=%s", row.Path, row.Status, row.Reason)
		}
		t.Fatalf("预览行数：禁动命中 %d、安全命中 %d，共 %d 行", blockedRows, safeRows, len(page.Rows))
	}

	safeIndex, ok := fixture.archive.Find("String/Common.uv.str")
	if !ok {
		t.Fatal("安全表缺失")
	}
	if _, err := service.Apply(page.PlanID, []int32{safeIndex}); err != nil {
		t.Fatal(err)
	}
	if got, ok := fixture.archive.LookupStringTable(3, "name_10019"); !ok || got != "禁动表装备名" {
		t.Errorf("禁动表被批处理改写: %q %v", got, ok)
	}
	if got, ok := fixture.archive.LookupStringTable(1, "name_10018"); !ok || !strings.Contains(got, "改过的") {
		t.Errorf("安全表未被批处理改写: %q %v", got, ok)
	}
}
