package services

import (
	"fmt"
	"strconv"
	"strings"

	"pvfine/internal/formview"
	"pvfine/internal/pvf"
)

// 「添加候选」：往**某一处** `[list]`（内联掉落列表）末尾追加一条候选（物品编号 + 权重）。
//
// 与「添加掉落」同一套做法（文本层定位 + 重新投影校验 + 只改归档内存）：
//   - 该文件里 `[list]` 有 862 次，按段名操作会波及全部 ⇒ 必须按**出现序号**定位；
//   - 插入点是这一处 `[list]` 的 `[/list]` **之前**（追加到该块末尾）；
//   - 缩进用两个制表符，与现有候选行完全一致（用户 2026-10-03 明确要求格式一致）。

// AddDropCandidate 往指定 `[list]` 的末尾追加一条候选，返回重新投影后的这一段。
func (s *FormViewService) AddDropCandidate(
	filePath string,
	section string,
	occurrence int,
	item FormViewDropItem,
) (*formview.ProjectedSection, error) {
	filePath = normalizeFormViewPath(filePath)
	section = strings.TrimSpace(section)
	switch {
	case filePath == "":
		return nil, fmt.Errorf("文件路径不能为空")
	case section == "":
		return nil, fmt.Errorf("段名不能为空")
	case occurrence < 1:
		return nil, fmt.Errorf("出现序号必须 ≥ 1")
	}
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	format, ok := catalog.LookupFile(filePath)
	if !ok {
		return nil, fmt.Errorf("该文件没有结构化视图规则: %s", filePath)
	}
	if _, ok := format.LookupSection(section); !ok {
		return nil, fmt.Errorf("规则里没有段 [%s]", section)
	}

	id, err := requireUnsignedInt("候选物品编号", item.ItemID)
	if err != nil {
		return nil, err
	}
	weight := strings.TrimSpace(item.Weight)
	if weight == "" {
		weight = "1000"
	}
	if _, err := strconv.ParseInt(weight, 10, 64); err != nil {
		return nil, fmt.Errorf("候选权重必须是整数: %q", item.Weight)
	}

	s.c.mu.RLock()
	a := s.c.archive
	if a == nil {
		s.c.mu.RUnlock()
		return nil, ErrNoArchive
	}
	index, found := a.Find(filePath)
	if !found {
		s.c.mu.RUnlock()
		return nil, fmt.Errorf("归档内找不到文件: %s", filePath)
	}
	text, err := a.Text(index)
	if err != nil {
		s.c.mu.RUnlock()
		return nil, fmt.Errorf("读取文件失败（%s）: %w", filePath, err)
	}
	s.c.mu.RUnlock()

	plan, err := planSectionAppend(text, section, occurrence)
	if err != nil {
		return nil, err
	}
	// 缩进照抄该段最后一条数据行（对 [list] 就是最后一条候选）。
	indent := plan.rowIndent
	if indent == "" {
		indent = dropItemIndent
	}
	line := indent + id + "\t" + weight + "\r\n"
	updatedText := text[:plan.offset] + line + text[plan.offset:]

	// 校验：这一处 `[list]` 重新投影后，最后一行必须正好是刚追加的那条。
	view := pvf.ParseScriptView(updatedText)
	projected := sectionOccurrence(format, view, section, occurrence)
	if projected == nil {
		return nil, fmt.Errorf("追加后重新投影失败：找不到段 [%s] 第 %d 次出现", section, occurrence)
	}
	if len(projected.Rows) == 0 {
		return nil, fmt.Errorf("追加后重新投影失败：段 [%s] 第 %d 次出现没有任何行", section, occurrence)
	}
	last := projected.Rows[len(projected.Rows)-1].Cells
	want := []string{id, weight}
	for i := 0; i < len(want); i++ {
		if i >= len(last) {
			return nil, fmt.Errorf("追加后校验失败：最后一行第 %d 列缺失（期望 %q）", i+1, want[i])
		}
		if strings.TrimSpace(last[i].Value) != want[i] {
			return nil, fmt.Errorf(
				"追加后校验失败：最后一行第 %d 列应为 %q，实际为 %q。已取消本次添加（没有写入任何内容）",
				i+1, want[i], last[i].Value)
		}
	}

	if _, _, err := s.c.setText(index, updatedText); err != nil {
		return nil, err
	}
	// 同 ApplyCellEdits：广播出去，让主窗口的编辑区刷新这个文件的标签。
	emitFormViewFileChanged(index)

	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	fresh := s.c.archive
	if fresh == nil {
		return nil, ErrNoArchive
	}
	freshIndex, found := fresh.Find(filePath)
	if !found {
		return nil, fmt.Errorf("写回后找不到文件: %s", filePath)
	}
	freshText, err := fresh.Text(freshIndex)
	if err != nil {
		return nil, fmt.Errorf("写回后读取失败: %w", err)
	}
	result := formview.ProjectSection(format, pvf.ParseScriptView(freshText), section, occurrence)
	if result == nil {
		return nil, fmt.Errorf("写回后重新投影失败：找不到段 [%s] 第 %d 次出现", section, occurrence)
	}
	holder := []formview.ProjectedSection{*result}
	s.fillRefNames(holder, format, fresh, &holder[0].Warnings)
	return &holder[0], nil
}

// sectionAppendPlan 是「追加到某段末尾」的落点与缩进。
type sectionAppendPlan struct {
	// offset = **最后一个条目**所在行的行尾之后（新内容插在这里）。
	offset int
	// rowIndent = 该段最后一条**数据行**的行首缩进（新行照抄它，格式才一致）。
	rowIndent string
}

// planSectionAppend 找到第 occurrence 次出现的 `[section]` 段里「最后一个条目之后」的位置。
//
// 「最后一个条目」= 闭合标签之前**最后一行非空行**（对 `[independent drop]` 来说，
// 最后一条数据行若带 `[list]`，那就是它那处 `[/list]`；对 `[list]` 来说就是最后一条候选）。
//
// 为什么不插在"闭合标签之前"（2026-10-03 用户实测踩到）：文件里闭合标签前
// **本来就可能有空行**，插在它之前会把新条目塞到空行之上，看起来像凭空多出一段空白。
//
// 缩进另取：栈深度为 0 的**数据行**的缩进 —— 不能拿"上一行"的缩进（上一行可能是
// `[/list]`，缩进少一层，照抄就错了）。
func planSectionAppend(text, section string, occurrence int) (sectionAppendPlan, error) {
	name := strings.TrimSpace(section)
	open := "[" + name + "]"
	seen := 0
	inTarget := false
	var stack []string
	plan := sectionAppendPlan{}
	foundRow := false

	for lineStart := 0; lineStart <= len(text); {
		lineRest := text[lineStart:]
		lineEnd := strings.IndexAny(lineRest, "\r\n")
		line := lineRest
		next := len(text)
		if lineEnd >= 0 {
			line = lineRest[:lineEnd]
			next = lineStart + lineEnd
			for next < len(text) && (text[next] == '\r' || text[next] == '\n') {
				next++
			}
		}
		trimmed := strings.TrimSpace(line)

		switch {
		case isSectionOpenTag(trimmed):
			if inTarget {
				stack = append(stack, strings.ToLower(trimmed))
			} else if strings.EqualFold(trimmed, open) {
				seen++
				if seen == occurrence {
					inTarget = true
					stack = stack[:0]
				}
			}
		case isSectionCloseTag(trimmed):
			if !inTarget {
				break
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
				break
			}
			if !foundRow {
				return sectionAppendPlan{}, fmt.Errorf(
					"段 [%s] 的第 %d 次出现里没有任何条目（无可插入位置）", name, occurrence)
			}
			return plan, nil
		default:
			if inTarget && trimmed != "" {
				// 任何非空行都把插入点往后推（含 [list] 的 [/list]，它属于最后一个条目）。
				plan.offset = next
				if len(stack) == 0 {
					plan.rowIndent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
					foundRow = true
				}
			}
		}

		if next <= lineStart {
			break
		}
		lineStart = next
	}
	return sectionAppendPlan{}, fmt.Errorf("文件里找不到段 [%s] 的第 %d 次出现", name, occurrence)
}
