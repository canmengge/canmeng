package annotations

import "testing"

// 外置数据只有说明文本（type=text、无 values）时，必须继承内嵌定义的枚举表：
// 否则它会顶掉内嵌定义、连翻译表一起丢，预览就显示 [amulet] 这类原文。
// 2026-10-01 用户实况：注释数据\fields\equ.json 覆盖内嵌配置后枚举翻译消失。
func TestMergeDocumentsInheritsEnumValuesFromBase(t *testing.T) {
	base := Document{
		Version: 1,
		Fields: []FieldDefinition{{
			ID:         "equ.equipment-type",
			Match:      MatchSpec{Extensions: []string{".equ"}},
			Target:     TargetSpec{Kind: "token", Section: "equipment type", Index: intPtr(0)},
			Annotation: AnnotationSpec{Title: "装备类型", Type: "enum", Values: map[string]string{"[amulet]": "项链", "[waist]": "腰带"}},
			Preview:    &PreviewSpec{Provider: "equ", Role: "equipment-type", Group: "summary", Order: 20, Format: "enum"},
		}},
	}
	// 外置：同一个字段键，只有说明文本、没有 values
	overlay := Document{
		Version: 1,
		Fields: []FieldDefinition{{
			ID:         "equ.section.equipment-type",
			Match:      MatchSpec{Extensions: []string{".equ"}},
			Target:     TargetSpec{Kind: "token", Section: "equipment type", Index: intPtr(0)},
			Annotation: AnnotationSpec{Title: "装备类型", Type: "text", Content: "|字段|意义|"},
			Preview:    &PreviewSpec{Provider: "equ", Role: "equipment-type", Group: "summary", Order: 20, Format: "enum"},
		}},
	}

	merged := mergeDocuments(base, overlay)
	if len(merged.Fields) != 1 {
		t.Fatalf("合并后字段数 = %d, 期望 1（同键应替换而非追加）", len(merged.Fields))
	}
	field := merged.Fields[0]
	if field.Annotation.Content != "|字段|意义|" {
		t.Errorf("外置的说明文本应保留，实际 %q", field.Annotation.Content)
	}
	if field.Annotation.Values["[amulet]"] != "项链" || field.Annotation.Values["[waist]"] != "腰带" {
		t.Errorf("枚举表未继承：%v", field.Annotation.Values)
	}
}

// 外置自己给了 values 时以外置为准（不能被内嵌覆盖）。
func TestMergeDocumentsPrefersOverlayValues(t *testing.T) {
	base := Document{
		Version: 1,
		Fields: []FieldDefinition{{
			ID:         "equ.attach-type",
			Target:     TargetSpec{Kind: "token", Section: "attach type", Index: intPtr(0)},
			Annotation: AnnotationSpec{Title: "交易类型", Type: "enum", Values: map[string]string{"[trade]": "内嵌值"}},
		}},
	}
	overlay := Document{
		Version: 1,
		Fields: []FieldDefinition{{
			ID:         "equ.section.attach-type",
			Target:     TargetSpec{Kind: "token", Section: "attach type", Index: intPtr(0)},
			Annotation: AnnotationSpec{Title: "交易类型", Type: "enum", Values: map[string]string{"[trade]": "外置值"}},
		}},
	}
	merged := mergeDocuments(base, overlay)
	if got := merged.Fields[0].Annotation.Values["[trade]"]; got != "外置值" {
		t.Fatalf("外置 values 应优先，实际 %q", got)
	}
}
