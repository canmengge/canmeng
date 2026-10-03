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
	hadList := false
	next := skipSpaces(text, end)
	if strings.HasPrefix(text[next:], "[list]") {
		blockEnd, blockErr := findBlockEnd(text, next, "list")
		if blockErr != nil {
			return nil, blockErr
		}
		end = blockEnd
		hadList = true
	}

	// 删除范围：含该条前面的空白（否则留空行）；第一条保留它自己的前导空白
	//（否则数据行会接到 `[independent drop]` 标签那一行上）。
	cutFrom := start
	if rowIndex == 0 {
		cutFrom = first.start
	}

	updatedText := ""
	switch {
	case rowIndex == 0 && rows > 1:
		// 第一条且后面还有：它自己的前导空白（换行 + 缩进）**保留**，
		// 于是下一条正好"补位"到第一行的位置 —— 不能再补衔接符，否则会多一个缩进。
		after := skipSpaces(text, end)
		updatedText = text[:first.start] + text[after:]
	case rowIndex < rows-1:
		// 中间那条：补上衔接（普通行往前靠；自带 [list] 的让下一条另起一行）
		after := skipSpaces(text, end)
		updatedText = text[:cutFrom] + joinAfterDelete(text, after, hadList) + text[after:]
	default:
		// 最后一条：只删自己，闭合标签前的空白原样保留
		updatedText = text[:cutFrom] + text[end:]
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

// joinAfterDelete 决定"删掉一条之后，它原来的位置要补什么"：
//
//   - 被删的是普通行 ⇒ 补一个制表符，下一条**往前靠**（接着上一行写）；
//   - 被删的这条**自带 `[list]` 块** ⇒ 补「换行 + 缩进」，下一条必须**另起一行**
//     （用户 2026-10-03：删完看到下一条挤在 `[/list]` 后面是不对的）。
//
// 缩进优先沿用下一条自己的（它本来就另起一行时），否则用一个制表符。
func joinAfterDelete(text string, after int, hadList bool) string {
	if !hadList {
		return "\t"
	}
	indent := dropRowIndent
	if sep := whitespaceBefore(text, after); strings.ContainsAny(sep, "\r\n") {
		if cut := strings.LastIndexAny(sep, "\r\n"); cut >= 0 {
			indent = sep[cut+1:]
		}
	}
	return detectEOL(text) + indent
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
			// 闭合标签把层级减回 0 ⇒ 这一块结束，**立刻收工**。
			// （早先只在"进入前 depth==0"时返回，于是配对的 `[/list]` 被当成"还没结束"，
			//   继续往后找第二个 ⇒ 报"找不到配对结束标签"，用户 2026-10-03 实测踩到。）
			depth--
			if depth <= 0 {
				return at + len(closeTag), nil
			}
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
