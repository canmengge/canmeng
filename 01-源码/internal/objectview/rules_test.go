package objectview

import "testing"

func TestLoadDefaultCatalogIsValid(t *testing.T) {
	catalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.ObjectTypes) < 16 {
		t.Fatalf("内置对象类型只有 %d 个", len(catalog.ObjectTypes))
	}
	// 按中文显示名同样可查。
	rule, ok := catalog.Lookup("装备")
	if !ok || rule.ID != "equipment" {
		t.Fatalf("按显示名查找失败: %#v, %v", rule, ok)
	}
	if paths := rule.AllListPaths(); len(paths) != 1 || paths[0] != "equipment/equipment.lst" {
		t.Errorf("装备登记表 = %#v", paths)
	}
	if rule.StringTable == nil || *rule.StringTable != 3 {
		t.Errorf("装备字符串表 = %v", rule.StringTable)
	}
	// 技能是按职业分表的类型，候选路径应去重且多于一条。
	skill, ok := catalog.Lookup("skill")
	if !ok {
		t.Fatal("缺少 skill 类型")
	}
	if paths := skill.AllListPaths(); len(paths) < 10 {
		t.Errorf("技能登记表数量 = %d", len(paths))
	}
}

func TestParseRejectsInvalidCatalogs(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"version 非 1", `{"version":2,"objectTypes":[{"id":"a","label":"A","listPath":"a/a.lst"}]}`},
		{"无对象类型", `{"version":1,"objectTypes":[]}`},
		{"id 重复（大小写无关）", `{"version":1,"objectTypes":[{"id":"a","label":"A","listPath":"a/a.lst"},{"id":"A","label":"A2","listPath":"a/a.lst"}]}`},
		{"id 为空", `{"version":1,"objectTypes":[{"id":" ","label":"A","listPath":"a/a.lst"}]}`},
		{"label 为空", `{"version":1,"objectTypes":[{"id":"a","label":" ","listPath":"a/a.lst"}]}`},
		{"缺少登记表", `{"version":1,"objectTypes":[{"id":"a","label":"A"}]}`},
		{"登记表不是 .lst", `{"version":1,"objectTypes":[{"id":"a","label":"A","listPath":"a/a.equ"}]}`},
		{"扩展名缺少点", `{"version":1,"objectTypes":[{"id":"a","label":"A","listPath":"a/a.lst","extensions":["equ"]}]}`},
		{"键名模式缺表格号", `{"version":1,"objectTypes":[{"id":"a","label":"A","listPath":"a/a.lst","keyPatterns":["name_{id}"]}]}`},
		{"键名模式缺 id 占位符", `{"version":1,"objectTypes":[{"id":"a","label":"A","listPath":"a/a.lst","stringTable":3,"keyPatterns":["name_1"]}]}`},
		{"未知字段", `{"version":1,"objectTypes":[{"id":"a","label":"A","listPath":"a/a.lst","bogus":1}]}`},
		{"多个 JSON 文档", `{"version":1,"objectTypes":[{"id":"a","label":"A","listPath":"a/a.lst"}]}{}`},
	}
	for _, testCase := range cases {
		if _, err := Parse([]byte(testCase.data)); err == nil {
			t.Errorf("%s：非法规则被接受", testCase.name)
		}
	}
}

func TestKeyPatternsForReplacesID(t *testing.T) {
	rule := ObjectType{ID: "equipment", Label: "装备", StringTable: new(int), KeyPatterns: []string{"name_{id}", "explain_{id}"}}
	got := rule.KeyPatternsFor("10018")
	if len(got) != 2 || got[0] != "name_10018" || got[1] != "explain_10018" {
		t.Fatalf("键名模式 = %#v", got)
	}
}
