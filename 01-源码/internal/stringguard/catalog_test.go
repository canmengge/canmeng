package stringguard

import "testing"

// 内置清单必须能拦住 0.38 乱码事故涉及的表，并放行四张安全表。
func TestLoadDefaultGuardPolicy(t *testing.T) {
	guard, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{1, 5, 8, 27} {
		decision := guard.CheckTableIndex(index)
		if decision.Verdict != VerdictAllow {
			t.Errorf("安全表 %d 未被放行: %+v", index, decision)
		}
	}
	// 表 0 Character / 3 equipment / 13 Stackable：规范里点名的最易误用项。
	for _, index := range []int{0, 3, 13} {
		decision := guard.CheckTableIndex(index)
		if !decision.Blocked() {
			t.Fatalf("禁动表 %d 未被拦截: %+v", index, decision)
		}
		if decision.Hint == "" {
			t.Errorf("表 %d 缺少修复建议", index)
		}
	}
	if got := guard.CheckTableIndex(3).TablePath; got != "String/equipment.uv.str" {
		t.Errorf("表 3 的实测路径 = %q", got)
	}
	if decision := guard.CheckTableIndex(99); !decision.Blocked() {
		t.Errorf("未登记表号未被拦截: %+v", decision)
	}
	if decision := guard.CheckTableIndex(-1); !decision.Blocked() {
		t.Errorf("负数表号未被拦截: %+v", decision)
	}

	// 路径判定：大小写与分隔符无关。
	if decision := guard.CheckPath("string/EQUIPMENT.uv.str"); !decision.Blocked() {
		t.Errorf("禁动路径未被拦截: %+v", decision)
	}
	if decision := guard.CheckPath(`String\equipment.uv.str`); !decision.Blocked() {
		t.Errorf("反斜杠路径未被拦截: %+v", decision)
	}
	if decision := guard.CheckPath("String/Common.uv.str"); decision.Verdict != VerdictAllow {
		t.Errorf("安全表路径未放行: %+v", decision)
	}
	if decision := guard.CheckPath("equipment/character/x.equ"); decision.Verdict != VerdictAllow {
		t.Errorf("普通脚本文件未放行: %+v", decision)
	}
	if decision := guard.CheckPath(""); decision.Verdict != VerdictAllow {
		t.Errorf("空路径未放行: %+v", decision)
	}
	// 汉化包改动过但不是字符串表的文件：提示级。
	if decision := guard.CheckPath("skill/common/combomastery.skl"); decision.Verdict != VerdictCaution {
		t.Errorf(".skl 应为提示级: %+v", decision)
	}

	if got := guard.ProtectedPathCount(); got != 34 {
		t.Errorf("禁动路径数 = %d，期望 34", got)
	}
	if got := len(guard.AllowedTableNumbers()); got != 4 {
		t.Errorf("白名单表数 = %d，期望 4", got)
	}
}

func TestParseRejectsInvalidCatalogs(t *testing.T) {
	const valid = `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1],"blockedHint":"h"},
	  "tablePaths":{"1":"String/Common.uv.str","3":"String/equipment.uv.str"},
	  "protectedPaths":["string/equipment.uv.str"]}`
	if _, err := Parse([]byte(valid)); err != nil {
		t.Fatalf("合法清单被拒: %v", err)
	}

	cases := []struct {
		name string
		data string
	}{
		{"version 非 1", `{"version":2,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str"]}`},
		{"mode 为空", `{"version":1,"writePolicy":{"mode":"","allowedTableNumbers":[1]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str"]}`},
		{"mode 非白名单", `{"version":1,"writePolicy":{"mode":"blacklist","allowedTableNumbers":[1]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str"]}`},
		{"白名单为空", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str"]}`},
		{"白名单重复", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1,1]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str"]}`},
		{"白名单负数", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[-1]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str"]}`},
		{"白名单缺映射", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[7]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str"]}`},
		{"禁动路径为空", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1]},"tablePaths":{"1":"a.str"},"protectedPaths":[]}`},
		{"禁动路径重复", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str","String/X.uv.str"]}`},
		{"禁动路径含反斜杠", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1]},"tablePaths":{"1":"a.str"},"protectedPaths":["string\\x.uv.str"]}`},
		{"禁动与白名单自相矛盾", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1]},"tablePaths":{"1":"String/Common.uv.str"},"protectedPaths":["string/common.uv.str"]}`},
		{"tablePaths 键非十进制", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1]},"tablePaths":{"一":"a.str","1":"a.str"},"protectedPaths":["string/x.uv.str"]}`},
		{"未知字段", `{"version":1,"bogus":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str"]}`},
		{"多个 JSON 文档", `{"version":1,"writePolicy":{"mode":"whitelist","allowedTableNumbers":[1]},"tablePaths":{"1":"a.str"},"protectedPaths":["string/x.uv.str"]}{}`},
	}
	for _, testCase := range cases {
		if _, err := Parse([]byte(testCase.data)); err == nil {
			t.Errorf("%s：非法清单被接受", testCase.name)
		}
	}
}
