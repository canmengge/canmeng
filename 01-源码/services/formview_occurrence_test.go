package services

import (
	"strings"
	"testing"
)

// twoLists 是**取自真实归档**的形状：外层段里嵌两处 `[list]`，数据行连排（token 流）。
// 锁住"外层段只数本层 token"这件事 —— 早先按段名栈判层级时这里只数出 17 个（少算了）。
const twoLists = "[independent drop]\n" +
	"\t0\t20\t0\t1\t1\t1\t1\t1\t1\t1\t1\t1\t1\t0\t0\t-1\t1\n" +
	"\t[list]\n" +
	"\t\t14400\t1000\n" +
	"\t[/list]\n" +
	"\t0\t21\t3015\t1\t1\t1\t1\t1\t1\t1\t1\t1\t1\t0\t0\t-1\t0\n" +
	"\t0\t22\t0\t1\t1\t1\t1\t1\t1\t1\t1\t1\t1\t0\t0\t-1\t1\n" +
	"\t[list]\n" +
	"\t\t25500\t500\n" +
	"\t[/list]\n" +
	"[/independent drop]\n"

// 外层段：3 行 × 17 列 = 51 个本层 token；两个 [list] 的内容**不算**。
func TestSectionTokenSpansCountsOnlyOwnLevel(t *testing.T) {
	spans, err := sectionTokenSpans(twoLists, "independent drop", 1)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(spans) != 51 {
		t.Fatalf("外层段本层 token = %d，期望 51（嵌套 [list] 的 token 不该算进来）", len(spans))
	}
	if got := twoLists[spans[0].start:spans[0].end]; got != "0" {
		t.Errorf("第一个 token = %q，期望 0", got)
	}
	if got := twoLists[spans[50].start:spans[50].end]; got != "1" {
		t.Errorf("最后一个 token = %q，期望 1", got)
	}
	// 第 2 行的第 3 列（掉落物品）应能精确定位到 3015
	row1Item := spans[17+2]
	if got := twoLists[row1Item.start:row1Item.end]; got != "3015" {
		t.Errorf("第 2 行第 3 列 = %q，期望 3015", got)
	}
}

// 内联列表本身：两处各 2 个 token，各自定位正确。
func TestSectionTokenSpansNestedList(t *testing.T) {
	first, err := sectionTokenSpans(twoLists, "list", 1)
	if err != nil {
		t.Fatalf("取第 1 处 [list] 失败: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("第 1 处 [list] token = %d，期望 2", len(first))
	}
	if got := twoLists[first[0].start:first[0].end]; got != "14400" {
		t.Errorf("第 1 处首个 token = %q，期望 14400", got)
	}

	second, err := sectionTokenSpans(twoLists, "list", 2)
	if err != nil {
		t.Fatalf("取第 2 处 [list] 失败: %v", err)
	}
	if got := twoLists[second[0].start:second[0].end]; got != "25500" {
		t.Errorf("第 2 处首个 token = %q，期望 25500", got)
	}
}

// 越界与不存在的段必须报错，不能返回半截结果。
func TestSectionTokenSpansErrors(t *testing.T) {
	if _, err := sectionTokenSpans(twoLists, "list", 3); err == nil {
		t.Error("第 3 处 [list] 不存在，应当报错")
	}
	if _, err := sectionTokenSpans(twoLists, "不存在", 1); err == nil {
		t.Error("段不存在应当报错")
	}
	if _, err := sectionTokenSpans("[list]\n\t1\t2\n", "list", 1); err == nil {
		t.Error("没有闭合标签应当报错")
	}
	// 缩进 / 空行 / 多空格都不能打乱计数
	messy := "[list]\n\n\t\t14400   1000\n   \t25500\t500   \n[/list]\n"
	spans, err := sectionTokenSpans(messy, "list", 1)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	want := []string{"14400", "1000", "25500", "500"}
	if len(spans) != len(want) {
		t.Fatalf("token 数 = %d，期望 %d", len(spans), len(want))
	}
	for i, expect := range want {
		if got := messy[spans[i].start:spans[i].end]; got != expect {
			t.Errorf("第 %d 个 token = %q，期望 %q", i+1, got, expect)
		}
	}
	_ = strings.TrimSpace
}
