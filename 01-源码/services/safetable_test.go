package services

import (
	"errors"
	"strings"
	"testing"

	"pvfine/internal/pvf"
)

// 核心用例：装备名落在禁动表 3，改写到安全表 1 —— 名字能改，禁动表一个字节不动。
func TestRewritePlaceholderToSafeTable(t *testing.T) {
	fixture := newStringGuardFixture(t)
	protectedIndex, ok := fixture.archive.Find("String/equipment.uv.str")
	if !ok {
		t.Fatal("禁动表缺失")
	}
	protectedBefore, err := fixture.archive.RawBytes(protectedIndex)
	if err != nil {
		t.Fatal(err)
	}
	scriptRawBefore, err := fixture.archive.RawBytes(fixture.protected)
	if err != nil {
		t.Fatal(err)
	}

	result, err := NewEditorService(fixture.core).RewritePlaceholderToSafeTable(
		fixture.protected, 3, "name_10019", "改过的装备名", 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.RewrittenOccurrences != 1 {
		t.Errorf("改写占位符个数 = %d，期望 1", result.RewrittenOccurrences)
	}
	if !strings.EqualFold(result.TargetTablePath, "String/Common.uv.str") {
		t.Errorf("目标表路径 = %q", result.TargetTablePath)
	}

	// ① 脚本里的表号被改写，且不再引用禁动表。
	if !strings.Contains(result.ScriptText, "<1::name_10019>") {
		t.Errorf("脚本未改写为新表号: %q", result.ScriptText)
	}
	if strings.Contains(result.ScriptText, "<3::name_10019>") {
		t.Errorf("脚本仍引用禁动表: %q", result.ScriptText)
	}
	stored, err := fixture.archive.Text(fixture.protected)
	if err != nil {
		t.Fatal(err)
	}
	if stored != result.ScriptText {
		t.Errorf("归档里的脚本文本与返回值不一致")
	}

	// ② 新文本落在安全表。
	if got, ok := fixture.archive.LookupStringTable(1, "name_10019"); !ok || got != "改过的装备名" {
		t.Errorf("安全表未写入: %q %v", got, ok)
	}

	// ③ 禁动表字节级未变（这是本功能的底线）。
	protectedAfter, err := fixture.archive.RawBytes(protectedIndex)
	if err != nil {
		t.Fatal(err)
	}
	if string(protectedBefore) != string(protectedAfter) {
		t.Error("禁动表字节被改动")
	}

	// ④ 脚本 token 数不变（PVF 硬约束：只改字符串内容，不动 token 结构）。
	// token 流每个 token 5 字节，因此比较原始载荷长度即可。
	scriptRawAfter, err := fixture.archive.RawBytes(fixture.protected)
	if err != nil {
		t.Fatal(err)
	}
	if len(scriptRawAfter) != len(scriptRawBefore) {
		t.Errorf("脚本载荷长度变化 %d -> %d", len(scriptRawBefore), len(scriptRawAfter))
	}
	if len(scriptRawAfter)%5 != 0 || len(scriptRawAfter)/5 != len(scriptRawBefore)/5 {
		t.Errorf("脚本 token 数变化 %d -> %d", len(scriptRawBefore)/5, len(scriptRawAfter)/5)
	}
}

// 一次改写应覆盖脚本里同一键的多次引用。
func TestRewritePlaceholderToSafeTableReplacesAllOccurrences(t *testing.T) {
	a := pvf.New()
	mustAddText(t, a, "list/n_string.lst",
		"1 `String/Common.uv.str` 3 `String/equipment.uv.str`", pvf.TypeScript)
	mustAddText(t, a, "String/Common.uv.str", "name_10018>安全表装备名\r\n", pvf.TypeUnicode)
	mustAddText(t, a, "String/equipment.uv.str", "name_10018>禁动表装备名\r\n", pvf.TypeUnicode)
	itemIndex, err := a.AddFileText("equipment/a.equ",
		"[name]\n{8=`<3::name_10018>`}\n[name2]\n{8=`<3::name_10018>`}", pvf.TypeScript)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCore()
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	result, err := NewEditorService(c).RewritePlaceholderToSafeTable(itemIndex, 3, "name_10018", "新名字", 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.RewrittenOccurrences != 2 {
		t.Errorf("改写个数 = %d，期望 2", result.RewrittenOccurrences)
	}
	if strings.Count(result.ScriptText, "<1::name_10018>") != 2 {
		t.Errorf("脚本改写不完整: %q", result.ScriptText)
	}
}

func TestRewritePlaceholderRejectsInvalidTargets(t *testing.T) {
	fixture := newStringGuardFixture(t)
	service := NewEditorService(fixture.core)

	// 目标表是禁动表：本接口不得成为写禁动表的后门。
	_, err := service.RewritePlaceholderToSafeTable(fixture.safe, 1, "name_10018", "x", 3)
	if err == nil {
		t.Fatal("以禁动表为目标未被拒绝")
	}
	var blocked *ProtectedStringTableError
	if !errors.As(err, &blocked) {
		t.Errorf("错误类型 = %T: %v", err, err)
	}

	// 源表本身已是安全表：应走普通「修改译文」。
	if _, err := service.RewritePlaceholderToSafeTable(fixture.safe, 1, "name_10018", "x", 5); err == nil {
		t.Error("源表为安全表时未被拒绝")
	}

	// 源表 == 目标表。
	if _, err := service.RewritePlaceholderToSafeTable(fixture.protected, 3, "name_10019", "x", 3); err == nil {
		t.Error("源表与目标表相同时未被拒绝")
	}

	// 空键名 / 空译文。
	if _, err := service.RewritePlaceholderToSafeTable(fixture.protected, 3, "  ", "x", 1); err == nil {
		t.Error("空键名未被拒绝")
	}
	if _, err := service.RewritePlaceholderToSafeTable(fixture.protected, 3, "name_10019", "  ", 1); err == nil {
		t.Error("空译文未被拒绝")
	}
}

// 脚本里没有该占位符时必须明确报错，而不是静默成功。
func TestRewritePlaceholderRejectsMissingPlaceholder(t *testing.T) {
	fixture := newStringGuardFixture(t)
	_, err := NewEditorService(fixture.core).RewritePlaceholderToSafeTable(
		fixture.protected, 3, "name_not_in_script", "x", 1,
	)
	if err == nil {
		t.Fatal("占位符不存在时未被拒绝")
	}
	if !strings.Contains(err.Error(), "name_not_in_script") {
		t.Errorf("错误信息未提到键名: %v", err)
	}
}

// 复现用户遇到的报错「表 1 没有可写入的 .str」：
// 110US 的 String/Common.uv.str 只有一行注释（50 字节、零条目），内核判为不可写。
// 自动匹配必须跳过它、落到可写的表；显式指定它则要给出**能看懂的原因**。
func TestRewritePlaceholderAutoSkipsEmptyTable(t *testing.T) {
	a := pvf.New()
	mustAddText(t, a, "list/n_string.lst",
		"1 `String/Common.uv.str` 3 `String/equipment.uv.str` 5 `String/ItemShop.uv.str`", pvf.TypeScript)
	// 表 1：只有注释 + NUL 填充 ⇒ 零条目 ⇒ 不可写。
	mustAddText(t, a, "String/Common.uv.str", "// empty\r\n\x00\x00", pvf.TypeUnicode)
	mustAddText(t, a, "String/ItemShop.uv.str", "name_10018>商店表旧名\r\n", pvf.TypeUnicode)
	mustAddText(t, a, "String/equipment.uv.str", "name_10018>禁动表装备名\r\n", pvf.TypeUnicode)
	itemIndex, err := a.AddFileText("equipment/a.equ", "[name]\n{8=`<3::name_10018>`}", pvf.TypeScript)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCore()
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)
	service := NewEditorService(c)

	// 可写性判定。
	targets, err := service.SafeTableTargets()
	if err != nil {
		t.Fatal(err)
	}
	status := map[int]*SafeTableTarget{}
	for _, entry := range targets.AllowedTables {
		status[entry.Index] = entry
	}
	if status[1] == nil || status[1].Writable {
		t.Errorf("表 1 应判为不可写: %#v", status[1])
	}
	if status[1] != nil && status[1].UnwritableReason == "" {
		t.Error("不可写的表缺少原因说明")
	}
	if status[5] == nil || !status[5].Writable || status[5].WriteTarget == "" {
		t.Errorf("表 5 应判为可写且给出目标路径: %#v", status[5])
	}
	if !targets.AutoAvailable {
		t.Error("自动匹配应可用（表 5 可写）")
	}

	// 先验"显式指定不可写的表"：必须明确报错并点出"空表"这个原因。
	// 该步在任何写入之前就失败，因此不动归档，可以安全地放在自动匹配之前。
	_, err = service.RewritePlaceholderToSafeTable(itemIndex, 3, "name_10018", "x", 1)
	if err == nil {
		t.Fatal("显式指定空表未被拒绝")
	}
	if !strings.Contains(err.Error(), "空表") {
		t.Errorf("错误信息未解释原因: %v", err)
	}

	// 再验自动匹配：跳过表 1、落到表 5（此步会真的改写脚本，故放在最后）。
	result, err := service.RewritePlaceholderToSafeTable(itemIndex, 3, "name_10018", "新名字", TargetTableAuto)
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetTableIndex != 5 || !result.AutoPicked {
		t.Fatalf("自动匹配结果 = %#v", result)
	}
	if len(result.SkippedTableIndexes) != 1 || result.SkippedTableIndexes[0] != 1 {
		t.Errorf("跳过表 = %#v", result.SkippedTableIndexes)
	}
	if !strings.Contains(result.ScriptText, "<5::name_10018>") {
		t.Errorf("脚本未改写为新表号: %q", result.ScriptText)
	}
	if got, ok := a.LookupStringTable(5, "name_10018"); !ok || got != "新名字" {
		t.Errorf("表 5 未写入: %q %v", got, ok)
	}
}

// 表 1 可写时，自动匹配应按数据文件偏好顺序优先用表 1。
func TestRewritePlaceholderAutoPrefersFirstWritable(t *testing.T) {
	fixture := newStringGuardFixture(t)
	result, err := NewEditorService(fixture.core).RewritePlaceholderToSafeTable(
		fixture.protected, 3, "name_10019", "自动选表", TargetTableAuto)
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetTableIndex != 1 || !result.AutoPicked || len(result.SkippedTableIndexes) != 0 {
		t.Errorf("自动匹配结果 = %#v", result)
	}
}

func TestStringTableGuardInfo(t *testing.T) {
	fixture := newStringGuardFixture(t)
	info, err := NewEditorService(fixture.core).StringTableGuardInfo()
	if err != nil {
		t.Fatal(err)
	}
	if len(info.AllowedTables) != 4 {
		t.Fatalf("安全表数 = %d，期望 4", len(info.AllowedTables))
	}
	seen := map[int]string{}
	for _, entry := range info.AllowedTables {
		seen[entry.Index] = entry.Label
	}
	if seen[1] != "Common" || seen[27] != "GameServerMsg" {
		t.Errorf("安全表标签 = %#v", seen)
	}
	if info.ProtectedCount != 34 {
		t.Errorf("禁动路径数 = %d，期望 34", info.ProtectedCount)
	}
	if !info.Enabled {
		t.Error("夹具已开启写保护，StringTableGuardInfo.Enabled 应为 true")
	}
	if info.Hint == "" {
		t.Error("缺少修复建议")
	}
}
