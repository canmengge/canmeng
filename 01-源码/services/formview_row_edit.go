package services

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"pvfine/internal/formview"
	"pvfine/internal/pvf"
)

// 通用「段内行」增删 —— **完全由规则驱动，不写死任何段名**。
//
// 为什么单开一套（用户 2026-10-06 明确要求：每个可视化 UI 的 UI 与功能都要独立）：
//   - 独立掉落那族有自己的语义：一行掉落配置 + **紧跟其后、属于它的** `[list]` 块
//     （删除要连带整块、新增要写死 17 列），实现在 `formview_add.go` / `formview_delete.go`；
//   - 别的文件族（如 NPC 商店的 `[item list]`＝7 个页签、装备升级系统的 `[need item]`）只需要
//     "按规则列数，往某段某一次出现的末尾加一行 / 删掉某几行"。
//
// 两套**互不调用、互不影响**：这里改动不会动到独立掉落的既有行为；反过来也一样。
//
// 与其它写入一致：只写**归档内存**，落盘只由用户点主工具条「保存 PVF」触发；
// 每次改完都**重新投影校验**，不符就整体放弃（不写入任何内容）。
// 标记：pvfRowEditModule_20261006

// InsertSectionRow 往「段 section 的第 occurrence 次出现」的末尾追加一行。
// values 必须与规则里该段的 rowTokens 等长（一列一个值）。
func (s *FormViewService) InsertSectionRow(
	filePath string,
	section string,
	occurrence int,
	values []string,
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
	rule, ok := format.LookupSection(section)
	if !ok {
		return nil, fmt.Errorf("规则里没有段 [%s]", section)
	}
	if rule.RowTokens < 1 {
		return nil, fmt.Errorf("规则里段 [%s] 的 rowTokens 无效", section)
	}
	if len(values) != rule.RowTokens {
		return nil, fmt.Errorf(
			"段 [%s] 每行 %d 列，收到 %d 个值", section, rule.RowTokens, len(values))
	}
	clean := make([]string, len(values))
	for i, raw := range values {
		value, valueErr := validateRowValue(rule, i, raw)
		if valueErr != nil {
			return nil, valueErr
		}
		clean[i] = value
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

	plan, err := planSectionAppend(text, section, occurrence, rule.RowTokens)
	if err != nil {
		return nil, err
	}
	// 分隔符：沿用"收官行自己的空白"（连排就还是制表符；收官行是标签则换行 + 数据行缩进）。
	separator := plan.separator
	if plan.tailIsTag || separator == "" {
		indent := plan.rowIndent
		if strings.TrimSpace(indent) == "" {
			indent = dropRowIndent
		}
		separator = detectEOL(text) + indent
	}
	line := strings.Join(clean, "\t")
	updatedText := text[:plan.offset] + separator + line + text[plan.offset:]

	// 校验：重新投影，最后一行必须正好是刚追加的那一行。
	view := pvf.ParseScriptView(updatedText)
	projected := sectionOccurrence(format, view, section, occurrence)
	if projected == nil {
		return nil, fmt.Errorf("追加后重新投影失败：找不到段 [%s] 第 %d 次出现", section, occurrence)
	}
	if len(projected.Rows) == 0 {
		return nil, fmt.Errorf("追加后重新投影失败：段 [%s] 第 %d 次出现没有任何行", section, occurrence)
	}
	last := projected.Rows[len(projected.Rows)-1].Cells
	for i := range clean {
		if i >= len(last) {
			return nil, fmt.Errorf("追加后校验失败：最后一行第 %d 列缺失（期望 %q）", i+1, clean[i])
		}
		if strings.TrimSpace(last[i].Value) != clean[i] {
			return nil, fmt.Errorf(
				"追加后校验失败：最后一行第 %d 列应为 %q，实际为 %q。已取消本次添加（没有写入任何内容）",
				i+1, clean[i], last[i].Value)
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
	return s.projectSectionLocked(fresh, format, freshText, filePath, section, occurrence)
}

// DeleteSectionRows 删除「段 section 的第 occurrence 次出现」里的若干行（rowIndexes 为 0 基行号）。
//
// 与独立掉落的 `DeleteIndependentDrop` 的关键差别：**不会**连带删除"紧跟其后的 `[list]` 块" ——
// 那是独立掉落的专属语义；这里只按行删 token（含该行前面的空白，避免留空行）。
func (s *FormViewService) DeleteSectionRows(
	filePath string,
	section string,
	occurrence int,
	rowIndexes []int,
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
	case len(rowIndexes) == 0:
		return nil, fmt.Errorf("没有要删除的行")
	}
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	format, ok := catalog.LookupFile(filePath)
	if !ok {
		return nil, fmt.Errorf("该文件没有结构化视图规则: %s", filePath)
	}
	rule, ok := format.LookupSection(section)
	if !ok {
		return nil, fmt.Errorf("规则里没有段 [%s]", section)
	}
	if rule.RowTokens < 1 {
		return nil, fmt.Errorf("规则里段 [%s] 的 rowTokens 无效", section)
	}
	rowTokens := rule.RowTokens

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

	// 从大到小删：先删靠后的行，前面的行号才不会漂移。
	targets := append([]int(nil), rowIndexes...)
	sort.Sort(sort.Reverse(sort.IntSlice(targets)))
	seen := make(map[int]bool, len(targets))
	updated := text
	for _, rowIndex := range targets {
		if rowIndex < 0 {
			return nil, fmt.Errorf("行号必须 ≥ 0")
		}
		if seen[rowIndex] {
			continue
		}
		seen[rowIndex] = true

		spans, spanErr := sectionTokenSpans(updated, section, occurrence)
		if spanErr != nil {
			return nil, spanErr
		}
		if len(spans) == 0 || len(spans)%rowTokens != 0 {
			return nil, fmt.Errorf(
				"段 [%s] 的 token 数 %d 不是每行 %d 的整数倍，为安全起见不做删除",
				section, len(spans), rowTokens)
		}
		rows := len(spans) / rowTokens
		if rowIndex >= rows {
			return nil, fmt.Errorf("行号 %d 超出范围（本段共 %d 行）", rowIndex+1, rows)
		}
		first := spans[rowIndex*rowTokens]
		last := spans[rowIndex*rowTokens+rowTokens-1]
		cutFrom := whitespaceStart(updated, first.start)
		if rowIndex == 0 {
			// 第一条保留自己的前导空白，否则数据行会接到段标签那一行上。
			cutFrom = first.start
		}
		switch {
		case rowIndex == 0 && rows > 1:
			after := skipSpaces(updated, last.end)
			updated = updated[:first.start] + updated[after:]
		case rowIndex < rows-1:
			after := skipSpaces(updated, last.end)
			updated = updated[:cutFrom] + joinAfterDelete(updated, after, false) + updated[after:]
		default:
			updated = updated[:cutFrom] + updated[last.end:]
		}

		projected := sectionOccurrence(format, pvf.ParseScriptView(updated), section, occurrence)
		if projected == nil {
			return nil, fmt.Errorf("删除后重新投影失败：找不到段 [%s] 第 %d 次出现", section, occurrence)
		}
		if len(projected.Rows) != rows-1 {
			return nil, fmt.Errorf(
				"删除后校验失败：行数应为 %d，实际 %d。已取消本次删除（没有写入任何内容）",
				rows-1, len(projected.Rows))
		}
	}

	if _, _, err := s.c.setText(index, updated); err != nil {
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
	return s.projectSectionLocked(fresh, format, freshText, filePath, section, occurrence)
}

// projectSectionLocked 重新投影某一段并解析引用名（调用方需已持有 c.mu 读锁）。
func (s *FormViewService) projectSectionLocked(
	fresh *pvf.Archive,
	format formview.Format,
	freshText string,
	filePath string,
	section string,
	occurrence int,
) (*formview.ProjectedSection, error) {
	result := formview.ProjectSection(format, pvf.ParseScriptView(freshText), section, occurrence)
	if result == nil {
		return nil, fmt.Errorf("写回后重新投影失败：找不到段 [%s] 第 %d 次出现（%s）", section, occurrence, filePath)
	}
	holder := []formview.ProjectedSection{*result}
	s.fillRefNames(holder, format, fresh, &holder[0].Warnings)
	return &holder[0], nil
}

// validateRowValue 校验并规整一个列值（按规则里该列的类型）。
//
// 只做"写坏了会被后面校验拦下"的前置检查：空值 / 含制表符换行（会撕裂这一行）/
// 类型不符（int、rate 要数字；enum 要在枚举里；ref 是编号，允许规则里声明的 noneValue）。
func validateRowValue(rule formview.Section, columnIndex int, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	label := ""
	var column formview.Column
	if columnIndex < len(rule.Columns) {
		column = rule.Columns[columnIndex]
		label = strings.TrimSpace(column.Label)
	}
	if label == "" {
		label = fmt.Sprintf("第 %d 列", columnIndex+1)
	}
	if value == "" {
		return "", fmt.Errorf("列 [%s] 的值不能为空", label)
	}
	if strings.ContainsAny(value, "\t\r\n") {
		return "", fmt.Errorf("列 [%s] 的值里不能含制表符或换行", label)
	}
	switch strings.TrimSpace(column.Type) {
	case formview.ColumnTypeInt:
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return "", fmt.Errorf("列 [%s] 需要整数，收到 %q", label, raw)
		}
	case formview.ColumnTypeRate:
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return "", fmt.Errorf("列 [%s] 需要数字，收到 %q", label, raw)
		}
	case formview.ColumnTypeEnum:
		if len(column.Values) > 0 {
			if _, ok := column.Values[value]; !ok {
				return "", fmt.Errorf("列 [%s] 的取值 %q 不在枚举里", label, value)
			}
		}
	case formview.ColumnTypeRef:
		if column.NoneValue != "" && value == column.NoneValue {
			break
		}
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return "", fmt.Errorf("列 [%s] 需要编号，收到 %q", label, raw)
		}
	}
	return value, nil
}
