package services

import (
	"fmt"
	"strings"

	"pvfine/internal/formview"
)

// 为什么需要这个文件（2026-10-03 用户要求：内联列表也必须能改）
//
// 「独立掉落」的掉落候选是一个**内联列表** —— `[list]` 段。在 `etc/independent_drop.etc`
// 里 `[list]` 出现 **862 次**。而结构化改写引擎（`internal/pvf/batch.go` 的
// `TransformStructuredBatch`，操作按 `Section` **段名**匹配）一改就会把 862 个同名段
// 一起改掉，所以原来只允许改"在本文件里唯一出现"的段，内联列表被直接禁掉了。
//
// 投影里的 `Occurrence` 是"这个段名在本文件里第几次出现"，它只在**文本位置**上有意义。
// 因此这里用**文本层**精确定位：
//
//  1. 按行扫描，找到第 `occurrence` 次出现的 `[section]` … `[/section]` 块；
//  2. 只数该块**直接层级**的 token（嵌套子段的 token 不算，与投影的 `directTokens` 一致）；
//  3. 把第 index 个 token 的文本整段换成新值。
//
// 替换的是数字（ASCII）⇒ 只动 ASCII 字节，不切多字节字符，安全；
// 结构性内容（换行 / 段标签 / 缩进）一律不碰。
//
// 注意：`internal/pvf/` 是内核冻结层（F1：只许补功能、禁改实现），所以本文件**不动内核**，
// 全部逻辑放在服务层。

// isSectionOpenTag 判断一行是否是段起始标签（`[xxx]`，不含 `[/xxx]`）。
func isSectionOpenTag(line string) bool {
	return strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "[/") && strings.HasSuffix(line, "]")
}

// isSectionCloseTag 判断一行是否是段结束标签（`[/xxx]`）。
func isSectionCloseTag(line string) bool {
	return strings.HasPrefix(line, "[/") && strings.HasSuffix(line, "]")
}

// tokenSpan 是文本里某个 token 的字节区间（半开区间 [start, end)）。
type tokenSpan struct {
	start int
	end   int
}

// sectionTokenSpans 返回文本里**第 occurrence 次**出现的 `[section]` 段的直接层级 token 区间。
//
// occurrence 从 1 开始，与 `formview.ProjectedSection.Occurrence` 同义。
// 找不到该次出现时返回错误（宁可报错，也不猜位置）。
func sectionTokenSpans(text, section string, occurrence int) ([]tokenSpan, error) {
	name := strings.TrimSpace(section)
	if name == "" {
		return nil, fmt.Errorf("段名不能为空")
	}
	if occurrence < 1 {
		return nil, fmt.Errorf("出现序号必须 ≥ 1")
	}
	open := "[" + name + "]"

	closeTag := "[/" + name + "]"
	// 出现次数按**子串**数（不按行）：这份归档把标签和数据压在同一行里，
	// 按"整行等于标签"判层级会漏判（用户 2026-10-03 实测踩到"没有正常闭合"）。
	// 标签本身是配对的（实测 [list]=863 / [/list]=863），所以按子串找一定靠得住。
	cursor := 0
	regionStart := -1
	for round := 1; round <= occurrence; round++ {
		at := strings.Index(text[cursor:], open)
		if at < 0 {
			return nil, fmt.Errorf("文件里没有段 [%s] 的第 %d 次出现", name, occurrence)
		}
		regionStart = cursor + at + len(open)
		cursor = regionStart
	}
	closeAt := strings.Index(text[regionStart:], closeTag)
	if closeAt < 0 {
		return nil, fmt.Errorf("段 [%s] 的第 %d 次出现没有正常闭合", name, occurrence)
	}
	region := text[regionStart : regionStart+closeAt]

	// 本层 token = 区域里所有"既不是标签、也不在子段块内"的空白分隔片段。
	var spans []tokenSpan
	pos := 0
	for pos < len(region) {
		ch := region[pos]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n':
			pos++
		case ch == '[':
			tagEnd := strings.IndexByte(region[pos:], ']')
			if tagEnd < 0 {
				return nil, fmt.Errorf("段 [%s] 里有没写完的标签", name)
			}
			tag := region[pos : pos+tagEnd+1]
			if strings.HasPrefix(tag, "[/") {
				// 多余/不配对的闭合标签：跳过它自己，不影响本层计数
				pos += tagEnd + 1
				continue
			}
			// 子段（如 [list]）⇒ 整块跳过：块里的 token 不算本层的
			inner := strings.Index(region[pos+len(tag):], "[/"+strings.Trim(tag, "[]")+"]")
			if inner < 0 {
				pos += tagEnd + 1
				continue
			}
			pos = pos + len(tag) + inner + len(tag) + 1
		default:
			from := pos
			for pos < len(region) {
				c := region[pos]
				if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '[' || c == ']' {
					break
				}
				pos++
			}
			if pos > from {
				spans = append(spans, tokenSpan{start: regionStart + from, end: regionStart + pos})
			}
		}
	}
	return spans, nil
}

// occurrenceEdit 是"改某次出现里某一格"的请求（已归一化）。
type occurrenceEdit struct {
	section    string
	occurrence int
	tokenIndex int
	value      string
}

// applyOccurrenceEdits 在文本上应用一批"按出现序号定位"的改动，返回新文本。
//
// 做法：按（段名, 出现序号）分组，每组**只扫一遍**拿到全部 token 区间，然后**从后往前**
// 替换（避免前面的改动把后面的下标推偏）。任何一个下标越界都会整体放弃（返回错误），
// 不做半截修改。
func applyOccurrenceEdits(text string, edits []occurrenceEdit) (string, bool, error) {
	if len(edits) == 0 {
		return text, false, nil
	}

	type groupKey struct {
		section    string
		occurrence int
	}
	groups := make(map[groupKey][]occurrenceEdit)
	order := make([]groupKey, 0, len(edits))
	for _, edit := range edits {
		key := groupKey{section: strings.TrimSpace(edit.section), occurrence: edit.occurrence}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], edit)
	}

	// 收集全部替换区间（绝对偏移），最后统一从后往前拼接。
	type splice struct {
		start int
		end   int
		value string
	}
	splices := make([]splice, 0, len(edits))
	for _, key := range order {
		spans, err := sectionTokenSpans(text, key.section, key.occurrence)
		if err != nil {
			return text, false, err
		}
		for _, edit := range groups[key] {
			if edit.tokenIndex < 0 || edit.tokenIndex >= len(spans) {
				return text, false, fmt.Errorf(
					"段 [%s] 第 %d 次出现只有 %d 个直接值，取不到第 %d 个（行/列算出来越界）",
					key.section, key.occurrence, len(spans), edit.tokenIndex+1)
			}
			span := spans[edit.tokenIndex]
			splices = append(splices, splice{start: span.start, end: span.end, value: edit.value})
		}
	}

	// 从后往前替换：这样前面的区间不受后面替换的长度变化影响。
	for i := 1; i < len(splices); i++ {
		for j := i; j > 0 && splices[j-1].start < splices[j].start; j-- {
			splices[j-1], splices[j] = splices[j], splices[j-1]
		}
	}
	changed := false
	result := text
	for _, item := range splices {
		if result[item.start:item.end] == item.value {
			continue
		}
		result = result[:item.start] + item.value + result[item.end:]
		changed = true
	}
	return result, changed, nil
}

// rowTokensOf 取规则里某段"一行占几个 token"（没定义时返回 1）。
func rowTokensOf(format formview.Format, section string) int {
	rule, ok := format.LookupSection(section)
	if !ok || rule.RowTokens <= 0 {
		return 1
	}
	return rule.RowTokens
}
