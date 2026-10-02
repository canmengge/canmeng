package services

import (
	"strings"
	"testing"

	annotationrules "pvfine/internal/annotations"
	"pvfine/internal/pvf"
)

func externalPartSetTable() externalLinkTable {
	nameToken := 2
	return externalLinkTable{
		Format:  "section",
		Path:    "etc/equipmentpartset.etc",
		Section: "equipment part set",
		IDToken: 0, PathToken: 1, NameToken: &nameToken,
		PathPrefix: "equipment/",
	}
}

func externalAppendageTable() externalLinkTable {
	return externalLinkTable{
		Format: "flat", Path: "list/appendage.lst",
		RecordTokens: 2, IDToken: 0, PathToken: 1,
		NameSection: "name",
	}
}

func newEmptyAnnotationEngine(t *testing.T) *annotationrules.Engine {
	t.Helper()
	engine, err := annotationrules.Compile(annotationrules.Document{
		Version: 1,
		Rules:   []annotationrules.Rule{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

// 段式登记表解析：同一段内记录宽度不一致（名称与编号同行 / 名称缩进到下一行）都要能取对，
// 名称列取到字符串表占位符时保留原文（由取用方解析），且不能把 [hide equipment part set]
// 这类别的段算进来。
func TestParseSectionExternalLinkTable(t *testing.T) {
	table := externalPartSetTable()
	text := strings.Join([]string{
		"[hide equipment part set]",
		"\t16072",
		"[/hide equipment part set]",
		"",
		"[equipment part set]",
		"\t3\t`character/partset/rareset.equ`",
		"\t\t{8=`<3::equipmentpartset_1>`}",
		"\t\t{8=`<3::rareset_name_cap>`}\t`[hat avatar]`\t3\t2",
		"[/equipment part set]",
		"",
		"[equipment part set]",
		"\t4\t`character/partset/uniqueset.equ`\t`神器装扮  套装`\t`[hat avatar]`\t3\t2",
		"[/equipment part set]",
		"",
		"[equipment part set]",
		"\t20000\t`cnrare/character/partset/rareset.equ`\t`稀有装扮  套装`",
		"[/equipment part set]",
	}, "\n")

	result := parseExternalLinkTableText(text, table)
	if len(result) != 3 {
		t.Fatalf("table = %#v", result)
	}
	if got := result["4"]; got.Path != "equipment/character/partset/uniqueset.equ" || got.Name != "神器装扮  套装" {
		t.Fatalf(`result["4"] = %#v`, got)
	}
	// 名称缩进到下一行、且是字符串表占位符：原文保留，路径不受影响。
	if got := result["3"]; got.Path != "equipment/character/partset/rareset.equ" || got.Name != "<3::equipmentpartset_1>" {
		t.Fatalf(`result["3"] = %#v`, got)
	}
	// 表中路径已带子树前缀（cnrare/…）时同样相对 equipment/，不得重复补前缀。
	if got := result["20000"]; got.Path != "equipment/cnrare/character/partset/rareset.equ" {
		t.Fatalf(`result["20000"] = %#v`, got)
	}
	if _, exists := result["16072"]; exists {
		t.Fatalf("hide 段不该参与解析: %#v", result)
	}
}

// 扁平登记表解析：list/appendage.lst 这类「ID + 反引号路径」每行一条记录。
// 注释行（#）不参与，重复 ID 以先出现为准。
func TestParseFlatExternalLinkTable(t *testing.T) {
	text := strings.Join([]string{
		"# comment line",
		"1\t`Appendage/changeAttackInfo/basic_attack_type_to_magical.apd`",
		"168\t`Appendage/equipment/flaming_romance_nec.apd`",
		"169\t`Appendage/equipment/other.apd`",
		"169\t`Appendage/equipment/duplicate.apd`",
	}, "\n")

	result := parseExternalLinkTableText(text, externalAppendageTable())
	if len(result) != 3 {
		t.Fatalf("table = %#v", result)
	}
	if got := result["168"].Path; got != "Appendage/equipment/flaming_romance_nec.apd" {
		t.Fatalf(`result["168"] = %q`, got)
	}
	if got := result["169"].Path; got != "Appendage/equipment/other.apd" {
		t.Fatalf("重复 ID 应以先出现为准: %#v", result["169"])
	}
	if _, exists := result["1"]; !exists {
		t.Fatalf("第一行记录没解析出来: %#v", result)
	}
}

// 端到端（套装）：装备的 [part set index] 应解析成登记表登记的套装文件；Title 承载套装名
// （供悬停提示，前端不渲染成名称标签）、Content 承载目标路径。
func TestPartSetIndexAnnotationsResolveSetFile(t *testing.T) {
	engine := newEmptyAnnotationEngine(t)

	a := pvf.New()
	mustAddText(t, a, "etc/equipmentpartset.etc", strings.Join([]string{
		"[equipment part set]",
		"\t3\t`character/partset/rareset.equ`",
		"[/equipment part set]",
		"",
		"[equipment part set]",
		"\t4\t`character/partset/uniqueset.equ`\t`神器装扮  套装`",
		"[/equipment part set]",
	}, "\n"), pvf.TypeScript)
	setIndex := mustAddText(t, a, "equipment/character/partset/uniqueset.equ",
		"[piece set ability]\n\t3\n[/piece set ability]", pvf.TypeScript)
	equIndex := mustAddText(t, a, "equipment/character/archer/avatar/belt/769900005.equ",
		"[name]\n\t`测试腰带`\n\n[part set index]\n\t4\n", pvf.TypeScript)

	c := &core{annotationEngine: engine}
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	meta, err := NewEditorService(c).GetFile(equIndex)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Annotations) != 1 {
		t.Fatalf("annotations = %#v", meta.Annotations)
	}
	annotation := meta.Annotations[0]
	if annotation.Type != "link" || annotation.TargetFileIndex != setIndex {
		t.Fatalf("annotation = %#v", annotation)
	}
	if annotation.Title != "神器装扮  套装" {
		t.Fatalf("套装名 = %q", annotation.Title)
	}
	if annotation.Content != "equipment/character/partset/uniqueset.equ" {
		t.Fatalf("annotation content = %q", annotation.Content)
	}
	// 注解位置是编辑器侧偏移（UTF-16 码元），按 rune 校验；BMP 内两者一致。
	runes := []rune(meta.Text)
	if annotation.Start < 0 || annotation.End > int32(len(runes)) ||
		string(runes[annotation.Start:annotation.End]) != "4" {
		t.Fatalf("annotation range = [%d,%d) over %q", annotation.Start, annotation.End, meta.Text)
	}

	// 表里查不到的编号：静默不下发注解（不能给出点了没反应的链接）。
	missingIndex := mustAddText(t, a, "equipment/character/archer/avatar/belt/769900006.equ",
		"[name]\n\t`未登记套装`\n\n[part set index]\n\t99999\n", pvf.TypeScript)
	missing, err := NewEditorService(c).GetFile(missingIndex)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.Annotations) != 0 {
		t.Fatalf("未登记编号不该产生注解: %#v", missing.Annotations)
	}
}

// 端到端（附加状态）：只有 [appendage] 段里的数字才跳转；
// 同一文件里其它段落的数字（概率、持续时间、价格…）一律不得产生链接。
func TestAppendageAnnotationsResolveApd(t *testing.T) {
	engine := newEmptyAnnotationEngine(t)

	a := pvf.New()
	mustAddText(t, a, "list/appendage.lst", strings.Join([]string{
		"# Appendage",
		"1\t`Appendage/changeAttackInfo/basic_attack_type_to_magical.apd`",
		"168\t`Appendage/equipment/flaming_romance_nec.apd`",
	}, "\n"), pvf.TypeScript)
	apdIndex := mustAddText(t, a, "Appendage/equipment/flaming_romance_nec.apd",
		"[type]\n\t`change status`\n\n[buff]\n\t1\n", pvf.TypeScript)
	equIndex := mustAddText(t, a, "equipment/character/common/amulet/100300008.equ", strings.Join([]string{
		"[name]",
		"\t`浪漫的火焰项链`",
		"",
		"[minimum level]",
		"\t80",
		"",
		"[if]",
		"\t[distance]",
		"\t\t`near`",
		"\t[event attack success]",
		"\t\t1",
		"[/if]",
		"",
		"[then]",
		"\t[target]",
		"\t\t`myself`\t0",
		"\t[probability]",
		"\t\t2",
		"\t[equipment duration]",
		"\t\t20000",
		"\t[appendage]",
		"\t\t168",
		"[/then]",
	}, "\n"), pvf.TypeScript)

	c := &core{annotationEngine: engine}
	if err := c.setArchive(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	meta, err := NewEditorService(c).GetFile(equIndex)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Annotations) != 1 {
		t.Fatalf("只有 [appendage] 的值该有注解，实际 = %#v", meta.Annotations)
	}
	annotation := meta.Annotations[0]
	if annotation.Type != "link" || annotation.TargetFileIndex != apdIndex {
		t.Fatalf("annotation = %#v", annotation)
	}
	// 登记表里路径写作 `Appendage/...`，归档内实际路径是小写；比较时忽略大小写。
	if !strings.EqualFold(annotation.Content, "Appendage/equipment/flaming_romance_nec.apd") {
		t.Fatalf("annotation content = %q", annotation.Content)
	}
	// .apd 没有 [name] 段 ⇒ 名称留空（悬停只显示目标路径），不得把占位符或路径塞进 Title。
	if annotation.Title != "" {
		t.Fatalf("annotation title = %q", annotation.Title)
	}
	runes := []rune(meta.Text)
	if string(runes[annotation.Start:annotation.End]) != "168" {
		t.Fatalf("annotation range = [%d,%d) over %q", annotation.Start, annotation.End, meta.Text)
	}
}
