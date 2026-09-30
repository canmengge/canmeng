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

// TestApplyOverlaySegments 覆盖「未写回段」在内存里的替换：
// 行数变化、多段从后往前替换、基线文本不被改动。
func TestApplyOverlaySegments(t *testing.T) {
	base := joinLines(
		"10001\t`a.equ`", // 行 1
		"10002\t`b.equ`", // 行 2
		"10003\t`c.equ`", // 行 3
		"10004\t`d.equ`", // 行 4
	)

	// 段①（行 2-3）替换成一行；段②（行 4）替换成两行。
	segments := []OverlaySegment{
		{Start: 2, Count: 2, Text: "10001\t`z.equ`\n"},
		{Start: 4, Count: 1, Text: "10005\t`e.equ`\n10006\t`f.equ`\n"},
	}
	out := applyOverlaySegments(0, base, segments)
	want := joinLines(
		"10001\t`a.equ`",
		"10001\t`z.equ`",
		"10005\t`e.equ`",
		"10006\t`f.equ`",
	)
	if out != want {
		t.Fatalf("替换结果 = %q, 期望 %q", out, want)
	}
	if base != joinLines("10001\t`a.equ`", "10002\t`b.equ`", "10003\t`c.equ`", "10004\t`d.equ`") {
		t.Fatal("基线文本被改动了（必须一个字节都不动）")
	}

	// 替换后的内容应当能被查重看见：行 1 与行 2 ID 相同、路径不同 → id 问题。
	report := scanListDuplicates(0, out)
	kinds := map[string]int{}
	for _, issue := range report.Issues {
		kinds[issue.Kind]++
	}
	if kinds["id"] != 1 {
		t.Fatalf("替换后 id 问题 = %d, 期望 1（未保存的改动必须参与查重）", kinds["id"])
	}
}

func joinLines(lines ...string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}
