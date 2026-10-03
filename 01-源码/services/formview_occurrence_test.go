package services

import (
	"strings"
	"testing"
)

// twoLists 是三段式片段：外层 [independent drop] 里嵌两个 [list]（真实归档就是这个形状，
// 只不过实际文件里有 862 个）。用来验证"只改第 N 个同名段"这件事真的成立。
const twoLists = "[independent drop]\r\n" +
	"\t0\t20\t0\t1\t1\t1\t1\t1\t1\t1\t1\t1\t1\t0\t0\t-1\t1\r\n" +
	"\t[list]\r\n" +
	"\t\t14400\t1000\r\n" +
	"\t[/list]\r\n" +
	"\t0\t21\t3015\t1\t1\t1\t1\t1\t1\t1\t1\t1\t1\t0\t0\t-1\t0\r\n" +
	"\t0\t22\t0\t1\t1\t1\t1\t1\t1\t1\t1\t1\t1\t0\t0\t-1\t1\r\n" +
	"\t[list]\r\n" +
	"\t\t25500\t500\r\n" +
	"\t[/list]\r\n" +
	"[/independent drop]\r\n"

// TestSectionTokenSpans 验证按出现序号取 token：嵌套子段的 token 不算进外层。
func TestSectionTokenSpans(t *testing.T) {
	second, err := sectionTokenSpans(twoLists, "list", 2)
	if err != nil {
		t.Fatalf("取第 2 个 [list] 失败: %v", err)
	}
	if len(second) != 2 {
		t.Fatalf("第 2 个 [list] 应有 2 个 token，实际 %d", len(second))
	}
	if got := twoLists[second[0].start:second[0].end]; got != "25500" {
		t.Errorf("第 2 个 [list] 第 1 个 token = %q，期望 25500", got)
	}
	if got := twoLists[second[1].start:second[1].end]; got != "500" {
		t.Errorf("第 2 个 [list] 第 2 个 token = %q，期望 500", got)
	}

	first, err := sectionTokenSpans(twoLists, "list", 1)
	if err != nil {
		t.Fatalf("取第 1 个 [list] 失败: %v", err)
	}
	if got := twoLists[first[0].start:first[0].end]; got != "14400" {
		t.Errorf("第 1 个 [list] 第 1 个 token = %q，期望 14400", got)
	}

	// 外层段：3 行 × 17 列 = 51 个直接 token，两个 [list] 的内容**不算**。
	outer, err := sectionTokenSpans(twoLists, "independent drop", 1)
	if err != nil {
		t.Fatalf("取 [independent drop] 失败: %v", err)
	}
	if len(outer) != 51 {
		t.Errorf("[independent drop] 直接 token 应为 51，实际 %d（嵌套子段被算进去了？）", len(outer))
	}

	if _, err := sectionTokenSpans(twoLists, "list", 3); err == nil {
		t.Error("第 3 个 [list] 不存在，应当报错")
	}
	if _, err := sectionTokenSpans(twoLists, "没有这个段", 1); err == nil {
		t.Error("不存在的段应当报错")
	}
}

// TestApplyOccurrenceEdits 验证"只改第 N 次出现"，且从后往前拼接不会把下标推偏。
func TestApplyOccurrenceEdits(t *testing.T) {
	got, changed, err := applyOccurrenceEdits(twoLists, []occurrenceEdit{
		{section: "list", occurrence: 2, tokenIndex: 1, value: "888"},
	})
	if err != nil {
		t.Fatalf("改写失败: %v", err)
	}
	if !changed {
		t.Fatal("应当有改动")
	}
	if !strings.Contains(got, "\t\t25500\t888\r\n") {
		t.Errorf("第 2 个 [list] 的权重没改成 888:\n%s", got)
	}
	if !strings.Contains(got, "\t\t14400\t1000\r\n") {
		t.Errorf("第 1 个 [list] 被误改（必须原样）:\n%s", got)
	}

	// 一条改成更长的值（1000 → 123456），另一条在同一段里更靠前 —— 靠"从后往前替换"保证两者都对。
	got2, _, err := applyOccurrenceEdits(twoLists, []occurrenceEdit{
		{section: "list", occurrence: 1, tokenIndex: 0, value: "7"},
		{section: "list", occurrence: 1, tokenIndex: 1, value: "123456"},
		{section: "independent drop", occurrence: 1, tokenIndex: 1, value: "99"},
	})
	if err != nil {
		t.Fatalf("多格改写失败: %v", err)
	}
	if !strings.Contains(got2, "\t\t7\t123456\r\n") {
		t.Errorf("同一行的两个 token 改后应为 7 / 123456:\n%s", got2)
	}
	if !strings.Contains(got2, "\t0\t99\t0\t1\t") {
		t.Errorf("外层段的第 2 个 token 应改成 99:\n%s", got2)
	}
	if !strings.Contains(got2, "\t\t25500\t500\r\n") {
		t.Errorf("第 2 个 [list] 不该被动:\n%s", got2)
	}

	// 越界必须报错，且不返回半截结果。
	if _, _, err := applyOccurrenceEdits(twoLists, []occurrenceEdit{
		{section: "list", occurrence: 1, tokenIndex: 9, value: "1"},
	}); err == nil {
		t.Error("token 下标越界应当报错")
	}
}

// TestSectionTokenSpansIgnoresIndent 确认缩进/空行/注释行不会打乱 token 计数。
func TestSectionTokenSpansIgnoresIndent(t *testing.T) {
	text := "[list]\r\n" +
		"\r\n" +
		"\t\t14400   1000\r\n" +
		"   \t25500\t500   \r\n" +
		"[/list]\r\n"
	spans, err := sectionTokenSpans(text, "list", 1)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	want := []string{"14400", "1000", "25500", "500"}
	if len(spans) != len(want) {
		t.Fatalf("token 数 = %d，期望 %d", len(spans), len(want))
	}
	for i, expect := range want {
		if got := text[spans[i].start:spans[i].end]; got != expect {
			t.Errorf("第 %d 个 token = %q，期望 %q", i+1, got, expect)
		}
	}
}
