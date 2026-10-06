package services

import (
	"fmt"
	"strconv"
	"strings"

	"pvfine/internal/formview"
	"pvfine/internal/pvf"
)

// NPC 商店模块专属：新建一个「商店条目」。
//
// 「商店条目」在文件里就是**一个 `[tab]` 块**（实测结构，用户 2026-10-06 指认）：
//
//	[tab]
//		`条目名`            ← 也可以用字符串表引用 `{8=`<5::key>`}`；这里按用户要求写 `` `名字` ``
//		[item list]
//			756000007      ← 该条目售卖的物品
//		[/item list]
//	[/tab]
//
// 与其它模块**互不影响**（用户 2026-10-06 要求）：独立掉落、通用段行增删各有各的实现，
// 这里只服务 NPC 商店（D2）。段名走 `config/formats.json` 里商店文件族已有的规则，
// 块内格式与缩进**照抄文件里已有的条目**（取自最后那个 `[/tab]` 行自身的缩进）。
//
// 为什么必须带"首个物品"：内核的 `planSectionAppend` 对**空段没有落点**（会报"没有任何条目"），
// 所以新建出来的 `[item list]` 不能是空的 —— 之后继续加物品走通用的 `InsertSectionRow`。
//
// 只写**归档内存**、不落盘（落盘仍由用户点主工具条「保存 PVF」触发）；改完重新投影校验。
// 标记：pvfShopTabModule_20261006

const (
	shopTabSection      = "tab"
	shopItemListSection = "item list"
)

// AppendShopTab 往商店文件里追加一个商店条目（`[tab]` + 反引号名字 + 内置首个物品的 `[item list]`）。
func (s *FormViewService) AppendShopTab(
	filePath string,
	name string,
	firstItem string,
) (*formview.Projection, error) {
	filePath = normalizeFormViewPath(filePath)
	name = strings.TrimSpace(name)
	firstItem = strings.TrimSpace(firstItem)
	switch {
	case filePath == "":
		return nil, fmt.Errorf("文件路径不能为空")
	case name == "":
		return nil, fmt.Errorf("条目名不能为空")
	case strings.ContainsAny(name, "`\t\r\n"):
		return nil, fmt.Errorf("条目名里不能有反引号、制表符或换行")
	case firstItem == "":
		return nil, fmt.Errorf("首个物品编号不能为空")
	}
	if _, err := strconv.ParseInt(firstItem, 10, 64); err != nil {
		return nil, fmt.Errorf("首个物品编号需要是数字，收到 %q", firstItem)
	}

	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	format, ok := catalog.LookupFile(filePath)
	if !ok {
		return nil, fmt.Errorf("该文件没有结构化视图规则: %s", filePath)
	}
	if _, ok := format.LookupSection(shopTabSection); !ok {
		return nil, fmt.Errorf("规则里这个文件族没有段 [%s]，不是商店文件（%s）", shopTabSection, filePath)
	}
	if _, ok := format.LookupSection(shopItemListSection); !ok {
		return nil, fmt.Errorf("规则里这个文件族没有段 [%s]（%s）", shopItemListSection, filePath)
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

	beforeTabs := countSectionOccurrences(pvf.ParseScriptView(text), shopTabSection)
	beforeItems := countSectionOccurrences(pvf.ParseScriptView(text), shopItemListSection)

	// 落点：**最后一个条目的 `[/tab]` 之后**（不是"最后一个 [/tab] 之前"）。
	//
	// 为什么（2026-10-06 实测 itemshop/(r)equipmentshop7.shp）：该文件 `[tab]` 出现 8 次、`[/tab]` 只有 7 次
	// —— 最外层那个"分组 tab"没有闭合（客户端自己的写法）。此时"最后一个 `[/tab]`"属于**最后一个条目**，
	// 插在它之前就会把新条目嵌套进上一个条目。改成插在它**之后**，新条目才与其它条目同级。
	offset, indent, err := shopTabInsertPoint(text)
	if err != nil {
		return nil, err
	}
	eol := detectEOL(text)
	inner := indent + "\t"
	block := indent + "[" + shopTabSection + "]" + eol +
		inner + "`" + name + "`" + eol +
		inner + "[" + shopItemListSection + "]" + eol +
		inner + "\t" + firstItem + eol +
		inner + "[/" + shopItemListSection + "]" + eol +
		indent + "[/" + shopTabSection + "]" + eol
	updatedText := text[:offset] + block + text[offset:]

	// 校验：条目数 +1、物品列表数 +1，且新条目的名字 token 正好是条目名。
	//
	// 注意：内核会把反引号串**去掉反引号**后作为 token 值（实测 `` `[etc shop]` `` → `[etc shop]`），
	// 所以这里比对的是 `name` 本身，**不带**反引号。
	view := pvf.ParseScriptView(updatedText)
	afterTabs := countSectionOccurrences(view, shopTabSection)
	afterItems := countSectionOccurrences(view, shopItemListSection)
	if afterTabs != beforeTabs+1 {
		return nil, fmt.Errorf(
			"新建后校验失败：条目数应为 %d，实际 %d。已取消本次新建（没有写入任何内容）",
			beforeTabs+1, afterTabs)
	}
	if afterItems != beforeItems+1 {
		return nil, fmt.Errorf(
			"新建后校验失败：物品列表数应为 %d，实际 %d。已取消本次新建（没有写入任何内容）",
			beforeItems+1, afterItems)
	}
	projection := formview.Project(filePath, format, view)
	fresh := latestSection(projection, shopTabSection)
	if fresh == nil || len(fresh.Rows) == 0 {
		return nil, fmt.Errorf("新建后校验失败：找不到新条目。已取消本次新建（没有写入任何内容）")
	}
	if got := strings.TrimSpace(fresh.Rows[0].Cells[0].Value); got != name {
		return nil, fmt.Errorf(
			"新建后校验失败：新条目名应为 %q，实际 %q。已取消本次新建（没有写入任何内容）",
			name, got)
	}

	if _, _, err := s.c.setText(index, updatedText); err != nil {
		return nil, err
	}
	emitFormViewFileChanged(index)

	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	after := s.c.archive
	if after == nil {
		return nil, ErrNoArchive
	}
	afterIndex, found := after.Find(filePath)
	if !found {
		return nil, fmt.Errorf("写回后找不到文件: %s", filePath)
	}
	afterText, err := after.Text(afterIndex)
	if err != nil {
		return nil, fmt.Errorf("写回后读取失败: %w", err)
	}
	result := formview.Project(filePath, format, pvf.ParseScriptView(afterText))
	s.fillRefNames(result.Sections, format, after, &result.Warnings)
	return result, nil
}

// shopTabInsertPoint 返回「新建商店条目」的落点（下一行行首）与该层级的缩进。
//
// 做法：找**最后一个** `[tab]` 开标签，再往后找**与它同缩进**的 `[/tab]` ——
// 那才是这个条目自己的收口，新条目插在它之后 ⇒ 与其它条目同级。
//
// 为什么要认"同缩进"（2026-10-06 实测 itemshop/(r)equipmentshop7.shp）：该文件 `[tab]` 8 次、
// `[/tab]` 只有 7 次 —— 最外层那个"分组 tab"客户端自己就没闭合，文件里最后那个 `[/tab]`
// 属于最后一个条目；若按"最后一个 `[/tab]` 之前"插入，新条目会被**嵌套进上一个条目**。
func shopTabInsertPoint(text string) (int, string, error) {
	type lineRef struct {
		start   int
		end     int // 含该行行尾的换行
		indent  string
		trimmed string
	}
	lines := make([]lineRef, 0, 256)
	for start := 0; start <= len(text); {
		rest := text[start:]
		cut := strings.IndexAny(rest, "\r\n")
		lineEnd := len(text)
		next := len(text)
		if cut >= 0 {
			lineEnd = start + cut
			next = lineEnd
			for next < len(text) && (text[next] == '\r' || text[next] == '\n') {
				next++
			}
		}
		line := text[start:lineEnd]
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines = append(lines, lineRef{
			start:   start,
			end:     next,
			indent:  indent,
			trimmed: strings.TrimSpace(line),
		})
		if next <= start {
			break
		}
		start = next
	}

	openTag := "[" + shopTabSection + "]"
	closeTag := "[/" + shopTabSection + "]"
	openIndex := -1
	for i := range lines {
		if strings.EqualFold(lines[i].trimmed, openTag) {
			openIndex = i
		}
	}
	if openIndex < 0 {
		return 0, "", fmt.Errorf("文件里找不到 %s，无法确定新建条目的位置（为安全起见不新建）", openTag)
	}
	indent := lines[openIndex].indent
	for i := openIndex + 1; i < len(lines); i++ {
		if !strings.EqualFold(lines[i].trimmed, closeTag) || lines[i].indent != indent {
			continue
		}
		return lines[i].end, indent, nil
	}
	return 0, "", fmt.Errorf("找不到 %s 的配对 %s（为安全起见不新建）", openTag, closeTag)
}

// latestSection 取某段**出现序号最大**的那一块（= 刚新建出来的那一条）。
func latestSection(projection *formview.Projection, section string) *formview.ProjectedSection {
	if projection == nil {
		return nil
	}
	var best *formview.ProjectedSection
	for i := range projection.Sections {
		item := &projection.Sections[i]
		if !strings.EqualFold(strings.TrimSpace(item.Section), strings.TrimSpace(section)) {
			continue
		}
		if best == nil || item.Occurrence > best.Occurrence {
			best = item
		}
	}
	return best
}
