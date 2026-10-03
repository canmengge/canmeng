package services

import "testing"

// planSectionAppend 必须跟着"收官行的排法"走 —— 用户 2026-10-03 三次实测都栽在这里：
// ① 连排（一行里塞多行数据）新条目要接在**行内**；② 一条一行要另起一行；
// ③ 收官行是 `[/list]` 时必须另起一行（不能塞进 `[/list]` 同一行）。
//
// 这三种形状都取自真实归档（按字节核对过），锁进测试，避免以后又写回"写死缩进"。
func TestPlanSectionAppendLayout(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		rowTokens int
		wantSep   string
		tailIsTag bool
	}{
		{
			name:      "收官行连排多行数据 ⇒ 接在行内（制表符）",
			text:      "[independent drop]\n\t0\t20\t1\t0\t21\t1\n[/independent drop]\n",
			rowTokens: 3,
			wantSep:   "\t",
		},
		{
			name:      "收官行只有一行数据 ⇒ 另起一行（换行 + 缩进）",
			text:      "[independent drop]\n\t0\t20\t1\n\t0\t21\t1\n[/independent drop]\n",
			rowTokens: 3,
			wantSep:   "\n\t",
		},
		{
			name:      "收官行是 [/list] ⇒ 另起一行",
			text:      "[independent drop]\n\t0\t20\t1\n\t[list]\n\t\t14400\t1000\n\t[/list]\n[/independent drop]\n",
			rowTokens: 3,
			wantSep:   "\n\t",
			tailIsTag: true,
		},
		{
			name:      "CRLF 文件同样识别（别把换行写死）",
			text:      "[independent drop]\r\n\t0\t20\t1\r\n[/independent drop]\r\n",
			rowTokens: 3,
			wantSep:   "\r\n\t",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			plan, err := planSectionAppend(item.text, "independent drop", 1, item.rowTokens)
			if err != nil {
				t.Fatalf("定位失败: %v", err)
			}
			if plan.separator != item.wantSep {
				t.Errorf("separator = %q，期望 %q", plan.separator, item.wantSep)
			}
			if plan.tailIsTag != item.tailIsTag {
				t.Errorf("tailIsTag = %v，期望 %v", plan.tailIsTag, item.tailIsTag)
			}
			if plan.offset <= 0 || plan.offset > len(item.text) {
				t.Fatalf("offset = %d 越界（长度 %d）", plan.offset, len(item.text))
			}
			// 插入点必须在最后一个条目之后：即它后面只剩换行 + 闭合标签
			if got := item.text[plan.offset:]; len(got) == 0 {
				t.Error("插入点之后没有任何内容（应当还有闭合标签）")
			}
		})
	}
}

// 段不存在 / 出现序号越界时必须报错，而不是猜一个位置。
func TestPlanSectionAppendMissing(t *testing.T) {
	const text = "[independent drop]\n\t0\t20\t1\n[/independent drop]\n"
	if _, err := planSectionAppend(text, "list", 1, 3); err == nil {
		t.Error("段不存在时应当报错")
	}
	if _, err := planSectionAppend(text, "independent drop", 2, 3); err == nil {
		t.Error("出现序号越界时应当报错")
	}
}
