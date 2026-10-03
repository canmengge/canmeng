package services

import (
	"fmt"
	"strings"

	"pvfine/internal/formview"
	"pvfine/internal/pvf"
)

// DeleteIndependentDrop 删除 `[independent drop]` 段里第 rowIndex 行（0 基）的**整条**掉落配置。
//
// 「一条」= 该行的 rowTokens 个 token + **紧跟其后、属于它的** `[list] … [/list]` 块。
//
// 格式（用户 2026-10-03 反复强调的"红线"）：
//   - 删除范围**含该条前面的那段空白**（换行 / 缩进）—— 否则会留下空行；
//   - 若后面还有下一条（且不是第一行的情况），把**下一条的前导空白收成一个制表符**，
//     于是下一条会"往前靠"、接到上一条的行尾（与"添加"的排版规则同源）；
//   - 第一行不收起（否则会把数据行接到 `[independent drop]` 标签那一行上）。
//
// 与其它写入一致：只改归档内存、不落盘；改完**重新投影校验**，不符就整体放弃。
func (s *FormViewService) DeleteIndependentDrop(
	filePath string,
	rowIndex int,
) (*formview.Projection, error) {
	filePath = normalizeFormViewPath(filePath)
	if filePath == "" {
		return nil, fmt.Errorf("文件路径不能为空")
	}
	if rowIndex < 0 {
		return nil, fmt.Errorf("行号必须 ≥ 0")
	}
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	format, ok := catalog.LookupFile(filePath)
	if !ok {
		return nil, fmt.Errorf("该文件没有结构化视图规则: %s", filePath)
	}
	section, ok := format.LookupSection(dropSectionName)
	if !ok || section.RowTokens <= 0 {
		return nil, fmt.Errorf("规则里段 [%s] 的 rowTokens 无效", dropSectionName)
	}
	rowTokens := section.RowTokens

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

	spans, err := sectionTokenSpans(text, dropSectionName, 1)
	if err != nil {
		return nil, err
	}
	if len(spans) == 0 || len(spans)%rowTokens != 0 {
		return nil, fmt.Errorf(
			"段 [%s] 的 token 数 %d 不是每行 %d 的整数倍，为安全起见不做删除",
			dropSectionName, len(spans), rowTokens)
	}
	rows := len(spans) / rowTokens
	if rowIndex >= rows {
		return nil, fmt.Errorf("行号 %d 超出范围（本段共 %d 行）", rowIndex+1, rows)
	}

	first := spans[rowIndex*rowTokens]
	last := spans[rowIndex*rowTokens+rowTokens-1]
	start := whitespaceStart(text, first.start)
	end := last.end

	// 紧跟其后的 [list] 块属于这一条 ⇒ 整块一起删。
	next := skipSpaces(text, end)
	if strings.HasPrefix(text[next:], "[list]") || strings.HasPrefix(text[next:], "[list]\r") {
		blockEnd, blockErr := findBlockEnd(text, next, "list")
		if blockErr != nil {
			return nil, blockErr
		}
		end = blockEnd
	}

	updatedText := ""
	switch {
	case rowIndex > 0 && rowIndex < rows-1:
		// 后面还有下一条 ⇒ 把它前面的空白收成一个制表符（往前靠）
		after := skipSpaces(text, end)
		updatedText = text[:start] + "\t" + text[after:]
	case rowIndex == 0:
		// 第一条：保留它前面那段空白（否则数据行会接到标签那一行上）
		updatedText = text[:first.start] + text[end:]
	default:
		// 最后一条：只删自己，闭合标签前的空白原样保留
		updatedText = text[:start] + text[end:]
	}

	// 校验：重新投影，行数必须正好少一行，且原本的下一条应当落到被删的位置上。
	view := pvf.ParseScriptView(updatedText)
	projected := sectionOccurrence(format, view, dropSectionName, 1)
	if projected == nil {
		return nil, fmt.Errorf("删除后重新投影失败：找不到段 [%s]", dropSectionName)
	}
	if len(projected.Rows) != rows-1 {
		return nil, fmt.Errorf(
			"删除后校验失败：行数应为 %d，实际 %d。已取消本次删除（没有写入任何内容）",
			rows-1, len(projected.Rows))
	}
	if rowIndex < rows-1 {
		before := spans[(rowIndex+1)*rowTokens]
		want := text[before.start:before.end]
		got := ""
		if cells := projected.Rows[rowIndex].Cells; len(cells) > 0 {
			got = cells[0].Value
		}
		if strings.TrimSpace(got) != strings.TrimSpace(want) {
			return nil, fmt.Errorf(
				"删除后校验失败：原第 %d 行应落到第 %d 行（首列 %q），实际首列 %q。已取消本次删除",
				rowIndex+2, rowIndex+1, want, got)
		}
	}

	if _, _, err := s.c.setText(index, updatedText); err != nil {
		return nil, err
	}
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
	result := formview.Project(filePath, format, pvf.ParseScriptView(freshText))
	s.fillRefNames(result.Sections, format, fresh, &result.Warnings)
	return result, nil
}

// whitespaceStart 返回 pos 之前那段空白（空格 / 制表符 / 换行）的起点。
func whitespaceStart(text string, pos int) int {
	if pos > len(text) {
		pos = len(text)
	}
	start := pos
	for start > 0 {
		switch text[start-1] {
		case ' ', '\t', '\r', '\n':
			start--
		default:
			return start
		}
	}
	return start
}

// skipSpaces 跳过 pos 起的空白（空格 / 制表符 / 换行），返回第一个非空白字符的位置。
func skipSpaces(text string, pos int) int {
	for pos < len(text) {
		switch text[pos] {
		case ' ', '\t', '\r', '\n':
			pos++
		default:
			return pos
		}
	}
	return len(text)
}

// findBlockEnd 从 `[name]` 标签开始，找到与它配对的 `[/name]` **结束位置**（含标签本身）。
// 嵌套同名标签按层级计数，不会提前收工。
func findBlockEnd(text string, start int, name string) (int, error) {
	open := "[" + name + "]"
	closeTag := "[/" + name + "]"
	depth := 0
	cursor := start
	for cursor < len(text) {
		index := strings.IndexByte(text[cursor:], '[')
		if index < 0 {
			break
		}
		at := cursor + index
		switch {
		case strings.HasPrefix(text[at:], closeTag):
			if depth == 0 {
				return at + len(closeTag), nil
			}
			depth--
			cursor = at + len(closeTag)
		case strings.HasPrefix(text[at:], open):
			depth++
			cursor = at + len(open)
		default:
			cursor = at + 1
		}
	}
	return 0, fmt.Errorf("找不到 %s 的配对结束标签，为安全起见不做删除", closeTag)
}
