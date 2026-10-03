package services

import (
	"testing"

	"pvfine/internal/formview"
	"pvfine/internal/pvf"
)

// TestValidateCellValue 盯住"什么输入会破坏文件结构"这条红线。
//
// 结构化视图写回的是**脚本 token 流**：一个 token 里出现空格 / 制表符 / 换行 /
// 方括号都会改变切分，轻则该格错位，重则整段崩掉。所以这些一律拒绝。
func TestValidateCellValue(t *testing.T) {
	cases := []struct {
		name   string
		column formview.Column
		value  string
		wantOK bool
	}{
		{"整数列接受数字", formview.Column{Label: "个数", Type: formview.ColumnTypeInt}, "5", true},
		{"整数列接受负数", formview.Column{Label: "职业限制", Type: formview.ColumnTypeInt}, "-1", true},
		{"整数列拒绝非数字", formview.Column{Label: "个数", Type: formview.ColumnTypeInt}, "abc", false},
		{"刻度列接受数字", formview.Column{Label: "掉率", Type: formview.ColumnTypeRate, Scale: 1000000}, "200000", true},
		{"刻度列拒绝小数", formview.Column{Label: "掉率", Type: formview.ColumnTypeRate, Scale: 1000000}, "1.5", false},
		{"文本列接受中文", formview.Column{Label: "备注"}, "中级", true},
		{"拒绝空值", formview.Column{Label: "备注"}, "", false},
		{"拒绝空格", formview.Column{Label: "备注"}, "a b", false},
		{"拒绝制表符", formview.Column{Label: "备注"}, "a\tb", false},
		{"拒绝换行", formview.Column{Label: "备注"}, "a\nb", false},
		{"拒绝方括号", formview.Column{Label: "备注"}, "[list]", false},
		{"拒绝反引号", formview.Column{Label: "备注"}, "`x`", false},
		// 刻意**不**按枚举卡取值：真实数据里存在规则枚举没登记的取值
		// （实测 掉落方式 = 5 有 9 行），卡死会让用户改不动这些真实存在的值。
		{"枚举列不强卡取值", formview.Column{Label: "掉落方式", Type: formview.ColumnTypeEnum,
			Values: map[string]string{"1": "内联列表"}}, "5", true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			err := validateCellValue(item.column, item.value)
			if item.wantOK && err != nil {
				t.Errorf("期望通过，实际报错: %v", err)
			}
			if !item.wantOK && err == nil {
				t.Error("期望被拒绝，实际通过")
			}
		})
	}
}

// TestCountSectionOccurrences 是"能不能改"的判据：改写引擎按段名匹配，
// 同名出现多次会一起被改，所以只允许唯一出现的段。
func TestCountSectionOccurrences(t *testing.T) {
	text := "[independent drop]\r\n" +
		"\t0\t20\t0\t1000000\t1000000\t1000000\t1000000\t1000000\t1\t1\t1\t1\t1\t0\t0\t-1\t1\r\n" +
		"\t[list]\r\n" +
		"\t\t14400\t1000\r\n" +
		"\t[/list]\r\n" +
		"\t0\t21\t0\t1000000\t1000000\t1000000\t1000000\t1000000\t1\t1\t1\t1\t1\t0\t0\t-1\t1\r\n" +
		"\t[list]\r\n" +
		"\t\t25500\t500\r\n" +
		"\t[/list]\r\n" +
		"[/independent drop]\r\n"
	view := pvf.ParseScriptView(text)

	if got := countSectionOccurrences(view, "independent drop"); got != 1 {
		t.Errorf("independent drop 出现次数 = %d，期望 1（可编辑）", got)
	}
	if got := countSectionOccurrences(view, "list"); got != 2 {
		t.Errorf("list 出现次数 = %d，期望 2（应被拒绝编辑）", got)
	}
	if got := countSectionOccurrences(view, "nope"); got != 0 {
		t.Errorf("不存在的段出现次数 = %d，期望 0", got)
	}
	// 大小写与空白不敏感。
	if got := countSectionOccurrences(view, "  Independent Drop  "); got != 1 {
		t.Errorf("段名比较应当忽略大小写与首尾空白，实际 %d", got)
	}
}

// TestCellValueIn 保证"校验改后取值"用的是同一套定位。
func TestCellValueIn(t *testing.T) {
	projection := &formview.Projection{
		Sections: []formview.ProjectedSection{
			{
				Section:    "independent drop",
				Occurrence: 1,
				Rows: []formview.Row{
					{Index: 0, Cells: []formview.Cell{{Value: "0"}, {Value: "20"}}},
					{Index: 1, Cells: []formview.Cell{{Value: "1"}, {Value: "10422"}}},
				},
			},
		},
	}
	if got, ok := cellValueIn(projection, "independent drop", 1, 1, 1); !ok || got != "10422" {
		t.Errorf("取第 2 行第 2 列 = %q / %v，期望 10422 / true", got, ok)
	}
	if _, ok := cellValueIn(projection, "independent drop", 1, 9, 0); ok {
		t.Error("行号越界应当返回 false")
	}
	if _, ok := cellValueIn(projection, "independent drop", 2, 0, 0); ok {
		t.Error("出现序号不符应当返回 false")
	}
	if got := rowCountOf(projection, "independent drop", 1); got != 2 {
		t.Errorf("rowCountOf = %d，期望 2", got)
	}
	if got := rowCountOf(projection, "list", 1); got != 0 {
		t.Errorf("不存在的段 rowCountOf = %d，期望 0", got)
	}
}


