package services

import "testing"

// TestScanListDuplicatesKinds 覆盖用户指定的三类问题：
//   ① ID 相同、路径不同 → id
//   ② ID 不同、路径相同 → path
//   ③ ID 相同、路径相同 → both
// 同时确认注释行/空行被跳过、行号是真实行号。
func TestScanListDuplicatesKinds(t *testing.T) {
	text := joinLines(
		"; 注释行不算条目",
		"",
		"10001\t`equipment/a.equ`", // 第 3 行
		"10002\t`equipment/b.equ`", // 第 4 行
		"10001\t`equipment/c.equ`", // 第 5 行：与第 3 行 ID 相同、路径不同 → id
		"10003\t`equipment/a.equ`", // 第 6 行：与第 3 行路径相同、ID 不同 → path
		"10002\t`equipment/b.equ`", // 第 7 行：与第 4 行完全相同 → both
		"# 井号注释",
	)

	report := scanListDuplicates(0, text)
	if report.Entries != 5 {
		t.Fatalf("条目数 = %d, 期望 5（注释与空行应被跳过）", report.Entries)
	}

	counts := map[string]int{}
	firstLines := map[string]int32{}
	for _, issue := range report.Issues {
		counts[issue.Kind]++
		if _, ok := firstLines[issue.Kind]; !ok {
			firstLines[issue.Kind] = issue.Entries[0].Line
		}
		if issue.Message == "" {
			t.Errorf("kind=%s 缺少提示语", issue.Kind)
		}
	}

	if counts["both"] != 1 {
		t.Errorf("整行重复(both) = %d, 期望 1", counts["both"])
	}
	if counts["id"] != 1 {
		t.Errorf("ID 重复(id) = %d, 期望 1", counts["id"])
	}
	if counts["path"] != 1 {
		t.Errorf("路径重复(path) = %d, 期望 1", counts["path"])
	}
	if got := firstLines["both"]; got != 4 && got != 7 {
		t.Errorf("整行重复的起始行 = %d, 期望 4 或 7（真实行号）", got)
	}
}

// TestParseListLineQuotes 覆盖路径的三种写法：反引号 / 引号 / 裸 token。
func TestParseListLineQuotes(t *testing.T) {
	cases := []struct {
		raw    string
		id     string
		path   string
		parsed bool
	}{
		{"10018\t`equipment/a.equ`", "10018", "equipment/a.equ", true},
		{"10018\t\"equipment/a.equ\"", "10018", "equipment/a.equ", true},
		{"10018\t'equipment/a.equ'", "10018", "equipment/a.equ", true},
		{"10018 equipment/a.equ", "10018", "equipment/a.equ", true},
		{"10018", "", "", false},
		{"", "", "", false},
	}
	for _, item := range cases {
		id, path, ok := parseListLine(item.raw)
		if ok != item.parsed || id != item.id || path != item.path {
			t.Errorf("parseListLine(%q) = (%q,%q,%v), 期望 (%q,%q,%v)",
				item.raw, id, path, ok, item.id, item.path, item.parsed)
		}
	}
}

func joinLines(lines ...string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}
