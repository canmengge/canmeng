package services

import (
	"bytes"
	"strings"
	"testing"

	"pvfine/internal/pvf"
)

// newObjectViewTestCore 构造一个 90US 布局的合成归档：
// 登记表条目是「列表目录相对」路径（character/vest.equ 位于 equipment/ 下），
// 脚本 [name] 用 <3::name_10018> 占位符，表 3 指向一个 .str。
func newObjectViewTestCore(t *testing.T) (*core, *pvf.Archive) {
	t.Helper()
	a := pvf.New()
	mustAddText(t, a, "equipment/character/vest.equ",
		"[name]\n{8=`<3::name_10018>`}\n[grade]\n20", pvf.TypeScript)
	mustAddText(t, a, "equipment/equipment.lst", "10018 `character/vest.equ`", pvf.TypeScript)
	mustAddText(t, a, "list/n_string.lst", "3 `String/equipment.uv.str`", pvf.TypeScript)
	mustAddText(t, a, "String/equipment.uv.str", "name_10018>测试装备\r\n", pvf.TypeUnicode)

	c := NewCore()
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)
	return c, a
}

func findObjectViewFile(view *ObjectView, role string) *ObjectViewFile {
	for _, file := range view.Files {
		if file.Role == role {
			return file
		}
	}
	return nil
}

func TestObjectViewServiceResolveEquipment(t *testing.T) {
	c, _ := newObjectViewTestCore(t)
	view, err := NewObjectViewService(c).ResolveObject("equipment", "10018")
	if err != nil {
		t.Fatal(err)
	}
	if view.ObjectType != "equipment" || view.ObjectLabel != "装备" || view.ObjectID != "10018" {
		t.Fatalf("对象标识 = %#v", view)
	}

	script := findObjectViewFile(view, "script")
	if script == nil || !script.Exists {
		t.Fatalf("缺少脚本文件: %#v", view.Files)
	}
	if !strings.EqualFold(script.Path, "equipment/character/vest.equ") {
		t.Errorf("脚本路径 = %q", script.Path)
	}
	if list := findObjectViewFile(view, "list"); list == nil || !strings.EqualFold(list.Path, "equipment/equipment.lst") {
		t.Errorf("登记表未解析: %#v", view.Files)
	}

	if len(view.Registrations) != 1 {
		t.Fatalf("登记项 = %#v", view.Registrations)
	}
	if registration := view.Registrations[0]; registration.ID != "10018" ||
		!strings.EqualFold(registration.ListPath, "equipment/equipment.lst") {
		t.Errorf("登记项内容 = %#v", registration)
	}

	// 脚本占位符与规则键名模式指向同一个键，应去重且保留脚本来源。
	if len(view.Texts) != 1 {
		t.Fatalf("显示文本 = %#v", view.Texts)
	}
	text := view.Texts[0]
	if text.TableIndex != 3 || text.Key != "name_10018" || text.Origin != "script" {
		t.Errorf("显示文本条目 = %#v", text)
	}
	if !text.Found || text.Value != "测试装备" {
		t.Errorf("显示文本值 = %#v", text)
	}
	if view.Name != "测试装备" {
		t.Errorf("对象名称 = %q", view.Name)
	}
}

func TestObjectViewServiceUnknownObjectID(t *testing.T) {
	c, _ := newObjectViewTestCore(t)
	view, err := NewObjectViewService(c).ResolveObject("equipment", "999999")
	if err != nil {
		t.Fatalf("未登记的对象不应报错: %v", err)
	}
	if len(view.Registrations) != 0 {
		t.Errorf("未登记对象出现了登记项: %#v", view.Registrations)
	}
	if findObjectViewFile(view, "script") != nil {
		t.Errorf("未登记对象解析出了脚本: %#v", view.Files)
	}
	if !hasWarning(view, "未在任何候选登记表中登记") {
		t.Errorf("缺少未登记告警: %#v", view.Warnings)
	}
}

func TestObjectViewServiceRejectsBadInput(t *testing.T) {
	c, _ := newObjectViewTestCore(t)
	service := NewObjectViewService(c)
	if _, err := service.ResolveObject("equipment", "  "); err == nil {
		t.Error("空 ID 被接受")
	}
	if _, err := service.ResolveObject("not-a-type", "10018"); err == nil {
		t.Error("未知对象类型被接受")
	}
}

func TestObjectViewServiceIsReadOnly(t *testing.T) {
	c, a := newObjectViewTestCore(t)
	before := rawSnapshot(t, a)
	fileCount := a.FileCount()

	if _, err := NewObjectViewService(c).ResolveObject("equipment", "10018"); err != nil {
		t.Fatal(err)
	}

	if a.FileCount() != fileCount {
		t.Fatalf("文件数变化: %d -> %d", fileCount, a.FileCount())
	}
	after := rawSnapshot(t, a)
	for i := range before {
		if !bytes.Equal(before[i], after[i]) {
			t.Fatalf("文件 %d 的字节被改动", i)
		}
	}
}

func TestObjectViewServiceListObjectTypes(t *testing.T) {
	c, _ := newObjectViewTestCore(t)
	result, err := NewObjectViewService(c).ListObjectTypes()
	if err != nil {
		t.Fatal(err)
	}
	if result.ObjectTypeCount != len(result.Types) || result.ObjectTypeCount < 16 {
		t.Fatalf("对象类型数量 = %d", result.ObjectTypeCount)
	}
	var equipment *ObjectTypeInfo
	for _, info := range result.Types {
		if info.ID == "equipment" {
			equipment = info
			break
		}
	}
	if equipment == nil {
		t.Fatal("缺少 equipment 类型")
	}
	if equipment.Label != "装备" || equipment.StringTable == nil || *equipment.StringTable != 3 {
		t.Errorf("equipment 类型 = %#v", equipment)
	}
	if len(equipment.ListPaths) != 1 || equipment.ListPaths[0] != "equipment/equipment.lst" {
		t.Errorf("equipment 登记表 = %#v", equipment.ListPaths)
	}
	// 中文显示名同样可查（Lookup 兼容 label）。
	if _, err := NewObjectViewService(c).ResolveObject("装备", "10018"); err != nil {
		t.Errorf("按显示名解析失败: %v", err)
	}
}

func TestScanPlaceholders(t *testing.T) {
	refs := scanPlaceholders("[name]\n{8=`<3::name_10018>`}\n[explain]\n{10=`<3::explain_10018>`}\n[icon]\n`a.img`\n1")
	if len(refs) != 2 {
		t.Fatalf("占位符数量 = %d: %#v", len(refs), refs)
	}
	if refs[0].tableIndex != 3 || refs[0].key != "name_10018" {
		t.Errorf("第 1 个占位符 = %#v", refs[0])
	}
	if refs[1].tableIndex != 3 || refs[1].key != "explain_10018" {
		t.Errorf("第 2 个占位符 = %#v", refs[1])
	}
	if refs := scanPlaceholders("没有占位符 <not-a-placeholder> 结束"); len(refs) != 0 {
		t.Errorf("非法占位符被接受: %#v", refs)
	}
}

func hasWarning(view *ObjectView, fragment string) bool {
	for _, warning := range view.Warnings {
		if strings.Contains(warning, fragment) {
			return true
		}
	}
	return false
}

// 未登记对象仍要能回答"该登记到哪"（审查项 R2）。
func TestObjectViewServiceUnknownObjectIDSuggestsTargetList(t *testing.T) {
	c, _ := newObjectViewTestCore(t)
	view, err := NewObjectViewService(c).ResolveObject("equipment", "999999")
	if err != nil {
		t.Fatal(err)
	}
	list := findObjectViewFile(view, "list")
	if list == nil || !strings.EqualFold(list.Path, "equipment/equipment.lst") {
		t.Fatalf("未给出目标登记表: %#v", view.Files)
	}
	if !hasWarning(view, "可登记的目标登记表") {
		t.Errorf("缺少目标登记表告警: %#v", view.Warnings)
	}
}

// 损坏的登记表必须变成告警，不能被静默跳过（审查项 R3）。
func TestObjectViewServiceSurfacesBrokenList(t *testing.T) {
	a := pvf.New()
	// 4 字节载荷不是 5 的倍数 ⇒ .lst 解码必然失败。
	a.AddFile("equipment/equipment.lst", []byte{0x00, 0x01, 0x02, 0x03}, pvf.TypeScript)
	c := NewCore()
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	view, err := NewObjectViewService(c).ResolveObject("equipment", "10018")
	if err != nil {
		t.Fatalf("登记表损坏不应让整次解析失败: %v", err)
	}
	if !hasWarning(view, "登记表解析失败") {
		t.Errorf("损坏的登记表被静默跳过: %#v", view.Warnings)
	}
}

// 数据文件缺失时重载应回退内置副本，而不是抛"文件不存在"（审查项 R1）。
func TestObjectViewServiceReloadRulesFallsBackToBuiltIn(t *testing.T) {
	c, _ := newObjectViewTestCore(t)
	service := NewObjectViewService(c)
	service.mu.Lock()
	service.path = t.TempDir() + "/missing-objectview.json"
	service.loaded = false
	service.mu.Unlock()

	result, err := service.ReloadRules()
	if err != nil {
		t.Fatalf("数据文件缺失时应回退内置副本: %v", err)
	}
	if result.ObjectTypeCount < 16 {
		t.Errorf("回退后对象类型数量 = %d", result.ObjectTypeCount)
	}
}

func rawSnapshot(t *testing.T, a *pvf.Archive) [][]byte {
	t.Helper()
	out := make([][]byte, 0, a.FileCount())
	for i := int32(0); i < int32(a.FileCount()); i++ {
		raw, err := a.RawBytes(i)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, raw)
	}
	return out
}
