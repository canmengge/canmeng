package services

import (
	"fmt"
	"strings"

	"pvfine/internal/formview"
	"pvfine/internal/pvf"
)

// 「删除候选」：删掉**某一处** `[list]`（内联掉落列表）里的第 rowIndex 行（0 基）。
//
// 与「删除选中」（DeleteIndependentDrop）**同一套格式规则**（用户 2026-10-03：
// "注意删除后的格式也要一样，前边的删除功能已经有了，这个照抄就好了"）：
//
//   - 删除范围含该行**前面的那段空白**（换行 / 缩进）—— 否则会留下空行；
//   - 若后面还有下一行，把下一行的前导空白收成**一个制表符** ⇒ 下一条"往前靠"；
//   - 第一行不收起（否则数据行会接到 `[list]` 标签那一行上）；
//   - 最后一行只删自己，`[/list]` 之前的空白原样保留。
//
// 与其它写入一致：只改**归档内存**、不落盘；改完**重新投影校验**，不符就整体放弃。
func (s *FormViewService) DeleteDropCandidate(
	filePath string,
	section string,
	occurrence int,
	rowIndex int,
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
	case rowIndex < 0:
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
	if _, ok := format.LookupSection(section); !ok {
		return nil, fmt.Errorf("规则里没有段 [%s]", section)
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

	rowTokens := rowTokensOf(format, section)
	spans, err := sectionTokenSpans(text, section, occurrence)
	if err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		return nil, fmt.Errorf("段 [%s] 第 %d 次出现里没有可删的行", section, occurrence)
	}
	if len(spans)%rowTokens != 0 {
		return nil, fmt.Errorf(
			"段 [%s] 第 %d 次出现的 token 数 %d 不是每行 %d 的整数倍，为安全起见不做删除",
			section, occurrence, len(spans), rowTokens)
	}
	rows := len(spans) / rowTokens
	if rowIndex >= rows {
		return nil, fmt.Errorf("行号 %d 超出范围（这一处共 %d 行）", rowIndex+1, rows)
	}

	first := spans[rowIndex*rowTokens]
	last := spans[rowIndex*rowTokens+rowTokens-1]
	// 删除范围：含该行前面的空白（否则留空行）；第一行保留它自己的前导空白。
	cutFrom := whitespaceStart(text, first.start)
	if rowIndex == 0 {
		cutFrom = first.start
	}

	updatedText := ""
	switch {
	case rowIndex == 0 && rows > 1:
		// 第一行且后面还有：保留它自己的前导空白（换行 + 缩进），下一行正好补位。
		after := skipSpaces(text, last.end)
		updatedText = text[:first.start] + text[after:]
	case rowIndex < rows-1:
		// 中间那条：补一个制表符 ⇒ 下一条往前靠（与「添加候选」的分隔符同源）。
		after := skipSpaces(text, last.end)
		updatedText = text[:cutFrom] + "\t" + text[after:]
	default:
		// 最后一条：只删自己，`[/list]` 前的空白原样保留。
		updatedText = text[:cutFrom] + text[last.end:]
	}

	// 校验：重新投影**这一处**，行数必须正好少一行，且原本的下一行应落到被删的位置上。
	view := pvf.ParseScriptView(updatedText)
	projected := sectionOccurrence(format, view, section, occurrence)
	if projected == nil {
		return nil, fmt.Errorf("删除后重新投影失败：找不到段 [%s] 第 %d 次出现", section, occurrence)
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
	result := formview.ProjectSection(format, pvf.ParseScriptView(freshText), section, occurrence)
	if result == nil {
		return nil, fmt.Errorf("写回后重新投影失败：找不到段 [%s] 第 %d 次出现", section, occurrence)
	}
	holder := []formview.ProjectedSection{*result}
	s.fillRefNames(holder, format, fresh, &holder[0].Warnings)
	return &holder[0], nil
}
