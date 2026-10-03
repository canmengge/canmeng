package services

import (
	"strings"
	"testing"
)

// joinAfterDelete 决定"删掉一条之后原位置补什么" —— 用户 2026-10-03 两次实测都栽在这里：
// 普通行要让下一条**往前靠**（一个制表符）；被删那条自带 `[list]` 块时，
// 下一条必须**另起一行**（不能在 `[/list]` 后面接着写）。
func TestJoinAfterDelete(t *testing.T) {
	const text = "[independent drop]\n\t0\t20\t1\n\t0\t21\t1\n[/independent drop]\n"
	at := strings.Index(text, "0\t21")
	if at < 0 {
		t.Fatal("测试夹具有问题：找不到第二个数据行")
	}

	if got := joinAfterDelete(text, at, false); got != "\t" {
		t.Errorf("普通行衔接 = %q，期望 %q（往前靠）", got, "\t")
	}
	if got := joinAfterDelete(text, at, true); got != "\n\t" {
		t.Errorf("自带 [list] 的衔接 = %q，期望 %q（另起一行）", got, "\n\t")
	}

	// CRLF 文件要用它自己的换行
	const crlf = "[independent drop]\r\n\t0\t20\t1\r\n\t0\t21\t1\r\n[/independent drop]\r\n"
	atCrlf := strings.Index(crlf, "0\t21")
	if got := joinAfterDelete(crlf, atCrlf, true); got != "\r\n\t" {
		t.Errorf("CRLF 文件的衔接 = %q，期望 %q", got, "\r\n\t")
	}
}

// findBlockEnd 必须找到**配对的** `[/list]` 就收工（早先会继续往后找第二个而报错）。
func TestFindBlockEnd(t *testing.T) {
	const text = "[list]\n\t\t14400\t1000\n\t[/list]\n\t0\t21\t1\n\t[list]\n\t\t25500\t500\n\t[/list]\n"
	end, err := findBlockEnd(text, 0, "list")
	if err != nil {
		t.Fatalf("找块尾失败: %v", err)
	}
	if got := text[:end]; !strings.HasSuffix(got, "[/list]") {
		t.Errorf("块尾应停在第一个 [/list] 之后，实际前文 = %q", got)
	}
	if strings.Contains(text[:end], "25500") {
		t.Error("不该把第二块也包含进来（必须找到配对的就收工）")
	}
	if _, err := findBlockEnd("[list]\n\t\t1\t1\n", 0, "list"); err == nil {
		t.Error("没有闭合标签时应当报错")
	}
}
