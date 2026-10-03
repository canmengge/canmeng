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

	plan, err := planSectionAppend(text, section, occurrence, rowTokensOf(format, section))
	if err != nil {
		return nil, err
	}
	// 候选行本身**不带缩进**：缩进由 separator 照抄上一条候选决定
	//（文件里候选是"换行 + 2 个制表符"，照抄就还是那样；连排的也一样跟着连排）。
	separator := plan.separator
	if plan.tailIsTag {
		// 收官行是标签（理论上 `[list]` 里不会发生，留着以防格式异常的段）
		separator = detectEOL(text) + dropItemIndent
	}
	if separator == "" {
		separator = detectEOL(text) + dropItemIndent
	}
	line := id + "\t" + weight
	updatedText := text[:plan.offset] + separator + line + text[plan.offset:]

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

// sectionAppendPlan 是「追加到某段末尾」的落点、分隔符与缩进。
type sectionAppendPlan struct {
	// offset = 最后一个条目**最后一个 token 之后**（新内容插在这里，不含行尾换行）。
	offset int
	// separator = 收官行**自己**前面那段空白（可能含换行）。
	// 收官行是"连排的一行"⇒ 它就是制表符（跟着连排 ✓）；
	// 收官行是 `[/list]` 这类标签 ⇒ 不用它，改用 tailIsTag 那条规则。
	separator string
	// rowIndent = 该段**第一条数据行**的行首缩进（新行照抄它）。
	rowIndent string
	// tailIsTag：最后一个条目的收官行是**标签**（如 `[/list]`）。
	//
	// 用户 2026-10-03 实测：加了"列表掉落"之后，后续添加必须**另起一行** ——
	// 这是对的（文件里 `[/list]` 后面本来就换行），错的是我照抄了那个列表行前面的
	// 制表符，把新行塞到了 `[/list]` 同一行上。收官行是标签时，分隔符应当是
	// 「换行 + 数据行缩进」。
	tailIsTag bool
}

// detectEOL 取文件实际使用的换行（这份归档是 LF，别处有的是 CRLF，别写死）。
func detectEOL(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		if index > 0 && text[index-1] == '\r' {
			return "\r\n"
		}
		return "\n"
	}
	return "\r\n"
}

// nextLineBreak 找从 from 开始的下一处换行，返回「本行结束位置」与「下一行开始位置」。
//
// **三种换行都要认**（`\r\n` / `\n` / `\r`）：这份归档是**混用**的 —— 文件里既有 CRLF
// 也有单独的 LF（2026-10-03 按字节核对发现）。早先只认 `\r\n`，于是整个文件被当成
// **一行**，标签行永远匹配不上，报出"文件里找不到段 [independent drop] 的第 1 次出现"。
func nextLineBreak(text string, from int) (int, int) {
	if from >= len(text) {
		return len(text), len(text)
	}
	index := from
	for index < len(text) && text[index] != '\r' && text[index] != '\n' {
		index++
	}
	next := index
	for next < len(text) && (text[next] == '\r' || text[next] == '\n') {
		next++
	}
	return index, next
}

// whitespaceBefore 取 offset 之前那一段空白：先吃掉行首缩进（空格 / 制表符），再吃掉换行。
// 结果就是"上一条目与本条目之间"的分隔符。
func whitespaceBefore(text string, offset int) string {
	if offset > len(text) {
		offset = len(text)
	}
	start := offset
	for start > 0 && (text[start-1] == ' ' || text[start-1] == '\t') {
		start--
	}
	for start > 0 && (text[start-1] == '\r' || text[start-1] == '\n') {
		start--
	}
	return text[start:offset]
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
func planSectionAppend(text, section string, occurrence, rowTokens int) (sectionAppendPlan, error) {
	name := strings.TrimSpace(section)
	open := "[" + name + "]"
	closeTag := "[/" + name + "]"
	if name == "" || occurrence < 1 {
		return sectionAppendPlan{}, fmt.Errorf("段名与出现序号无效")
	}

	// 一次前向扫描把每行记录下来（换行三种都认），后面全部按**行号**推理 ——
	// 不再用"段名栈"：栈那套容易被嵌套子段带偏，而这里其实只关心
	// "最后一个条目在哪、它前面那段空白是什么"。
	type lineRef struct {
		start   int
		end     int
		indent  string
		trimmed string
		// tokens 是该行的 token 数（按空白切）；lastTokenStart 是最后一个 token 的起点。
		// 用它们判断"这一行里连排了几行数据" —— 连排时新条目要接在**行内**（用制表符）。
		tokens         int
		lastTokenStart int
	}
	var lines []lineRef
	for lineStart := 0; lineStart <= len(text); {
		lineEnd, next := nextLineBreak(text, lineStart)
		line := text[lineStart:lineEnd]
		item := lineRef{
			start:          lineStart,
			end:            lineEnd,
			indent:         line[:len(line)-len(strings.TrimLeft(line, " \t"))],
			trimmed:        strings.TrimSpace(line),
			lastTokenStart: -1,
		}
		for pos := 0; pos < len(line); {
			if line[pos] == ' ' || line[pos] == '\t' {
				pos++
				continue
			}
			start := pos
			for pos < len(line) && line[pos] != ' ' && line[pos] != '\t' {
				pos++
			}
			item.tokens++
			item.lastTokenStart = lineStart + start
		}
		lines = append(lines, item)
		if next <= lineStart {
			break
		}
		lineStart = next
	}

	// 第 occurrence 个闭合标签
	closeIndex := -1
	seenClose := 0
	for index := range lines {
		if strings.EqualFold(lines[index].trimmed, closeTag) {
			seenClose++
			if seenClose == occurrence {
				closeIndex = index
				break
			}
		}
	}
	if closeIndex < 0 {
		return sectionAppendPlan{}, fmt.Errorf("文件里找不到段 [%s] 的第 %d 次出现", name, occurrence)
	}

	// 该次出现的开始标签之后 = 本条目的内容起点（找不到就用文件开头）
	regionStart := 0
	seenOpen := 0
	for index := 0; index < closeIndex; index++ {
		if strings.EqualFold(lines[index].trimmed, open) {
			seenOpen++
			if seenOpen == occurrence {
				regionStart = index + 1
				break
			}
		}
	}

	// 段内第一条非空行的缩进 = 该段数据行的缩进（照抄它）
	rowIndent := ""
	for index := regionStart; index < closeIndex; index++ {
		if lines[index].trimmed != "" {
			rowIndent = lines[index].indent
			break
		}
	}
	if rowIndent == "" {
		rowIndent = dropRowIndent
	}

	// 从闭合标签往前找最后一个非空行 = 最后一个条目的收官行
	tailIndex := -1
	for index := closeIndex - 1; index >= regionStart; index-- {
		if lines[index].trimmed != "" {
			tailIndex = index
			break
		}
	}
	if tailIndex < 0 {
		return sectionAppendPlan{}, fmt.Errorf(
			"段 [%s] 的第 %d 次出现里没有任何条目（无可插入位置）", name, occurrence)
	}

	tail := lines[tailIndex]
	tailIsTag := isSectionOpenTag(tail.trimmed) || isSectionCloseTag(tail.trimmed)
	plan := sectionAppendPlan{
		offset:    tail.end,
		rowIndent: rowIndent,
		tailIsTag: tailIsTag,
	}
	// 分隔符完全跟着"收官行的排法"走（用户 2026-10-03：文件里两种排法都有）：
	//   ① 收官行是标签（`[/list]`）⇒ 另起一行；
	//   ② 收官行里**连排了多行数据** ⇒ 新条目接在这一行**后面**（用行内的制表符）；
	//   ③ 收官行只有一行数据 ⇒ 另起一行（换行 + 缩进）。
	switch {
	case tailIsTag:
		plan.separator = detectEOL(text) + rowIndent
	case rowTokens > 0 && tail.tokens > rowTokens && tail.lastTokenStart >= 0:
		plan.separator = whitespaceBefore(text, tail.lastTokenStart)
	default:
		plan.separator = whitespaceBefore(text, tail.start+len(tail.indent))
	}
	return plan, nil
}

// 说明：早先这里按"段名栈"判层级，遇到嵌套子段（[independent drop] 里的 862 个 [list]）
// 会算错 —— 实测表现为"扫描在第一个 [/list] 处提前结束"（外层只剩 17 个 token）。
// 现在改为"按行号前后查找"，不再依赖层级推断，所以这里没有栈。
