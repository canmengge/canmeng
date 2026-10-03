package services

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"pvfine/internal/formview"
	"pvfine/internal/pvf"
)

// FormViewFormatInfo 是一个可用文件族的对外描述。
type FormViewFormatInfo struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Files    []string `json:"files"`
	Notes    string   `json:"notes,omitempty"`
	Sections []string `json:"sections"`
}

// FormViewFormatListResult 是结构化视图规则目录及其来源。
type FormViewFormatListResult struct {
	RulePath    string                `json:"rulePath"`
	FormatCount int                   `json:"formatCount"`
	Formats     []*FormViewFormatInfo `json:"formats"`
}

// FormViewService 提供「结构化视图」：按外部规则把脚本文件投影成只读表格
// （段 → 行 → 列），供界面做表格化阅读。
//
// 规则来自外部数据文件（config/formats.json），本服务不硬编码任何段名、
// 列名、枚举取值或刻度常量。投影复用内核既有的词法投影
// （internal/pvf 的 ParseScriptView），不另写一套解析器。
//
// 整个投影过程**只读**：不产生任何归档写入。
type FormViewService struct {
	c *core

	mu     sync.RWMutex
	rules  formview.Catalog
	path   string
	err    error
	loaded bool

	// refResolver 是 ref 列的名称解析器（含跨调用复用的登记表索引与名称缓存）。
	refResolver *refNameResolver
}

func NewFormViewService(c *core) *FormViewService {
	service := &FormViewService{c: c}
	if path, ok := formview.FindSourcePath(); ok {
		service.path = path
	} else if runtimePath, err := formview.RuntimePath(); err == nil {
		service.path = runtimePath
	}
	return service
}

// ListFormats 返回规则文件里定义的全部文件族。
func (s *FormViewService) ListFormats() (*FormViewFormatListResult, error) {
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	formats := make([]*FormViewFormatInfo, 0, len(catalog.Formats))
	for _, format := range catalog.Sorted() {
		info := &FormViewFormatInfo{
			ID:       format.ID,
			Label:    format.DisplayLabel(),
			Files:    append([]string(nil), format.Files...),
			Notes:    format.Notes,
			Sections: make([]string, 0, len(format.Sections)),
		}
		for _, section := range format.Sections {
			label := strings.TrimSpace(section.Label)
			if label == "" {
				label = strings.TrimSpace(section.Section)
			}
			info.Sections = append(info.Sections, label)
		}
		formats = append(formats, info)
	}
	return &FormViewFormatListResult{
		RulePath:    s.rulePath(),
		FormatCount: len(formats),
		Formats:     formats,
	}, nil
}

// ReloadRules 重新读取规则文件；校验失败时保留原有规则不变。
func (s *FormViewService) ReloadRules() (*FormViewFormatListResult, error) {
	catalog, err := s.loadRules()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.rules, s.err, s.loaded = catalog, nil, true
	s.mu.Unlock()
	result, err := s.ListFormats()
	if err != nil {
		return nil, err
	}
	emitEvent("formview:reloaded", result)
	return result, nil
}

// ProjectFile 把一个归档文件按规则投影成「段 → 行 → 列」表格（只读）。
func (s *FormViewService) ProjectFile(filePath string) (*formview.Projection, error) {
	filePath = normalizeFormViewPath(filePath)
	if filePath == "" {
		return nil, fmt.Errorf("文件路径不能为空")
	}
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	format, ok := catalog.LookupFile(filePath)
	if !ok {
		return nil, fmt.Errorf("该文件没有结构化视图规则: %s", filePath)
	}

	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	a := s.c.archive
	if a == nil {
		return nil, ErrNoArchive
	}
	index, found := a.Find(filePath)
	if !found {
		return nil, fmt.Errorf("归档内找不到文件: %s", filePath)
	}
	text, err := a.Text(index)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败（%s）: %w", filePath, err)
	}
	projection := formview.Project(a.Path(index), format, pvf.ParseScriptView(text))
	s.fillRefNames(projection.Sections, format, a, &projection.Warnings)
	return projection, nil
}

// ProjectSectionOccurrence 只取「某段第 occurrence 次出现」的投影。
//
// 主投影会把被行关联认领的目标段从输出里移除（实测 etc/independent_drop.etc 有 862 次
// [list]，全带上会让 payload 膨胀），界面双击关联格时用本方法单独取那一次。
func (s *FormViewService) ProjectSectionOccurrence(filePath, section string, occurrence int) (*formview.ProjectedSection, error) {
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

	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	a := s.c.archive
	if a == nil {
		return nil, ErrNoArchive
	}
	index, found := a.Find(filePath)
	if !found {
		return nil, fmt.Errorf("归档内找不到文件: %s", filePath)
	}
	text, err := a.Text(index)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败（%s）: %w", filePath, err)
	}
	projected := formview.ProjectSection(format, pvf.ParseScriptView(text), section, occurrence)
	if projected == nil {
		return nil, fmt.Errorf("文件 %s 里没有段 [%s] 的第 %d 次出现", filePath, section, occurrence)
	}
	holder := []formview.ProjectedSection{*projected}
	s.fillRefNames(holder, format, a, &holder[0].Warnings)
	return &holder[0], nil
}

// fillRefNames 给 ref 列的格子补上中文名（解析不到就留空，界面照旧显示编号）。
//
// 有**单次上限**：宁可某些格子只显示编号，也不让一次投影卡住界面（超额会写进 warnings）。
func (s *FormViewService) fillRefNames(sections []formview.ProjectedSection, format formview.Format, a *pvf.Archive, warnings *[]string) {
	resolver := s.resolverFor(a)
	if resolver == nil {
		return
	}
	attempts := 0
	capped := false

	for sectionIndex := range sections {
		section := &sections[sectionIndex]
		rule, ok := format.LookupSection(section.Section)
		if !ok {
			continue
		}
		for rowIndex := range section.Rows {
			row := &section.Rows[rowIndex]
			for cellIndex := range row.Cells {
				if cellIndex >= len(rule.Columns) {
					continue
				}
				column := rule.Columns[cellIndex]
				if strings.TrimSpace(column.Type) != formview.ColumnTypeRef {
					continue
				}
				value := strings.TrimSpace(row.Cells[cellIndex].Value)
				if none := strings.TrimSpace(column.NoneValue); none != "" && value == none {
					continue
				}
				if attempts >= maxRefNameResolutions {
					capped = true
					break
				}
				attempts++
				if name := resolver.name(column.Ref, value); name != "" {
					row.Cells[cellIndex].Name = name
				}
			}
			if capped {
				break
			}
		}
		if capped {
			break
		}
	}
	if capped && warnings != nil {
		*warnings = append(*warnings, fmt.Sprintf(
			"名称解析已达单次上限 %d 次，其余格子只显示编号（再点一次解析会命中缓存继续补）",
			maxRefNameResolutions))
	}
}

// resolverFor 取当前归档对应的名称解析器（归档换了就整体重建）。
func (s *FormViewService) resolverFor(a *pvf.Archive) *refNameResolver {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refResolver == nil || s.refResolver.archive != a {
		s.refResolver = newRefNameResolver(s.c, a)
	}
	return s.refResolver
}

// FormViewCellEdit 描述「把某段某行某列改成什么」。
//
// 刻意**不用文本偏移定位**：投影里的 Start/End 是"归一化换行后的 UTF-16 单元"
// （见 internal/pvf/script_view.go 的说明），拿它去切 Go 字符串会切错位置 ——
// 尤其非 ASCII 内容。改为按（段名, 出现序号, 行号, 列号）定位，交给项目的
// **结构化改写引擎**（TransformStructuredBatch，批量处理用的那套）去改。
type FormViewCellEdit struct {
	Section    string `json:"section"`
	Occurrence int    `json:"occurrence"`
	Row        int    `json:"row"`
	Column     int    `json:"column"`
	Value      string `json:"value"`
}

// ApplyCellEdits 应用一批单元格改动，返回**重新投影后**的结果。
//
// 流程（每一步都为"改错地方"设了闸）：
//  1. 重新投影，按（段, 出现序号, 行, 列）定位目标格 —— 偏移与内容都以"现在"为准；
//  2. 段在本文件里**唯一出现**时走结构化改写引擎（按段名匹配，安全）；
//     出现多次时（如 `[list]` 有 862 次）改走**文本层按出现序号定位**
//     （见 formview_occurrence.go）—— 否则同名段会被一起改掉；
//  3. 在**克隆**上做结构化改写（与批量预览同一手法），不碰真归档；
//  4. 校验：把改后的文本重新投影，逐个确认目标格真的等于期望值，不符则**整体放弃**；
//  5. 通过后经 core.setText 提交（自带写保护 / 版本快照 / 搜索索引同步）—— **不落盘**，
//     落盘仍走主工具条的「保存 PVF」。
func (s *FormViewService) ApplyCellEdits(filePath string, edits []FormViewCellEdit) (*formview.Projection, error) {
	filePath = normalizeFormViewPath(filePath)
	if filePath == "" {
		return nil, fmt.Errorf("文件路径不能为空")
	}
	if len(edits) == 0 {
		return nil, fmt.Errorf("没有要应用的改动")
	}
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	format, ok := catalog.LookupFile(filePath)
	if !ok {
		return nil, fmt.Errorf("该文件没有结构化视图规则: %s", filePath)
	}

	// ① 读当前文本并重新投影（定位以"现在"为准）。
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
	view := pvf.ParseScriptView(text)
	current := formview.Project(filePath, format, view)

	// ② 定位 + 校验 + 生成结构化操作。
	type expectation struct {
		edit  FormViewCellEdit
		value string
	}
	operations := make([]pvf.StructuredBatchOperation, 0, len(edits))
	occurrenceEdits := make([]occurrenceEdit, 0, len(edits))
	expected := make([]expectation, 0, len(edits))
	for _, edit := range edits {
		rule, ok := format.LookupSection(edit.Section)
		if !ok {
			s.c.mu.RUnlock()
			return nil, fmt.Errorf("规则里没有段 [%s]", edit.Section)
		}
		target := sectionOccurrence(format, view, edit.Section, edit.Occurrence)
		if target == nil {
			s.c.mu.RUnlock()
			return nil, fmt.Errorf(
				"文件里没有段 [%s] 的第 %d 次出现", edit.Section, edit.Occurrence)
		}
		if edit.Row < 0 || edit.Row >= len(target.Rows) {
			s.c.mu.RUnlock()
			return nil, fmt.Errorf(
				"行号 %d 超出范围（段 [%s] 第 %d 次出现只有 %d 行）",
				edit.Row+1, edit.Section, edit.Occurrence, len(target.Rows))
		}
		if edit.Column < 0 || edit.Column >= len(rule.Columns) {
			s.c.mu.RUnlock()
			return nil, fmt.Errorf("列号 %d 超出范围", edit.Column+1)
		}
		value := strings.TrimSpace(edit.Value)
		if err := validateCellValue(rule.Columns[edit.Column], value); err != nil {
			s.c.mu.RUnlock()
			return nil, fmt.Errorf("第 %d 行「%s」列: %w", edit.Row+1, rule.Columns[edit.Column].Label, err)
		}
		if edit.Row < len(target.Rows) && edit.Column < len(target.Rows[edit.Row].Cells) &&
			target.Rows[edit.Row].Cells[edit.Column].Value == value {
			continue // 值没变，不必写
		}
		// **按段分别选路**（2026-10-03 修正）：单次出现的段走结构化引擎；
		// 只有出现多次的段（内联列表 `[list]` 有 862 次）才走"按出现序号定位"的文本层。
		// 早先写成"一批里只要有一个多次段就整批走文本层"，结果把主表（1834 行）也塞进
		// 只数一次出现的扫描，报出"取不到第 20 个"—— 现在各走各的路。
		if countSectionOccurrences(view, edit.Section) > 1 {
			occurrenceEdits = append(occurrenceEdits, occurrenceEdit{
				section:    edit.Section,
				occurrence: edit.Occurrence,
				tokenIndex: edit.Row*rowTokensOf(format, edit.Section) + edit.Column,
				value:      value,
			})
		} else {
			operations = append(operations, pvf.StructuredBatchOperation{
				Kind:       "set",
				Section:    edit.Section,
				TokenIndex: edit.Row*rule.RowTokens + edit.Column,
				Value:      value,
			})
		}
		expected = append(expected, expectation{edit: edit, value: value})
	}
	if len(operations) == 0 && len(occurrenceEdits) == 0 {
		s.c.mu.RUnlock()
		return current, nil
	}

	// ③-a 单次出现的段：结构化引擎（在克隆上改，不碰内核；UTF-16 / 换行都交给它）。
	//     没有这类改动时直接用当前文本，交给下面的文本层处理。
	var updatedText string
	if len(operations) == 0 {
		s.c.mu.RUnlock()
		updatedText = text
	} else {
		raw, err := a.RawBytes(index)
		if err != nil {
			s.c.mu.RUnlock()
			return nil, fmt.Errorf("读取原始内容失败（%s）: %w", filePath, err)
		}
		stage := a.CloneForBatch()
		s.c.mu.RUnlock()

		// ③-b 在克隆上改写（与「批量处理」同一引擎：UTF-16 / 换行都交给它，不自己切字符串）。
		transformed, err := stage.TransformStructuredBatch(raw, operations)
		if err != nil {
			return nil, fmt.Errorf("改写失败: %w", err)
		}
		if !transformed.Changed() {
			return nil, fmt.Errorf("改写引擎没有产生任何改动：%s", strings.Join(transformed.Warnings(), "；"))
		}
		if err := stage.SetRawBytes(index, transformed.Raw()); err != nil {
			return nil, fmt.Errorf("暂存改写结果失败: %w", err)
		}
		innerText, err := stage.Text(index)
		if err != nil {
			return nil, fmt.Errorf("读回改写结果失败: %w", err)
		}
		updatedText = innerText
	}

	// ③-b 多次出现的段（内联列表）：在**上面结果的基础上**按出现序号逐个替换 ——
	//      完全不动内核（`internal/pvf/` 是冻结层），也不怕同名段有几百个。
	if len(occurrenceEdits) > 0 {
		next, changed, applyErr := applyOccurrenceEdits(updatedText, occurrenceEdits)
		if applyErr != nil {
			return nil, applyErr
		}
		if changed {
			updatedText = next
		}
	}

	// ④ 校验：重新投影，逐个确认目标格真的变成了期望值；不符就整体放弃（不写入）。
	//    同样要用 sectionOccurrence 单独取那一处 —— 主投影里内联列表（被关联认领的 [list]）
	//    是看不到的，用主投影校验会一律"查不到"而误报校验失败。
	checkView := pvf.ParseScriptView(updatedText)
	for _, item := range expected {
		got := ""
		ok := false
		if projected := sectionOccurrence(format, checkView, item.edit.Section, item.edit.Occurrence); projected != nil &&
			item.edit.Row < len(projected.Rows) &&
			item.edit.Column < len(projected.Rows[item.edit.Row].Cells) {
			got = projected.Rows[item.edit.Row].Cells[item.edit.Column].Value
			ok = true
		}
		if !ok || got != item.value {
			return nil, fmt.Errorf(
				"校验未通过：第 %d 行第 %d 列改后应为 %q，实际为 %q。已取消本次修改（没有写入任何内容）",
				item.edit.Row+1, item.edit.Column+1, item.value, got)
		}
	}

	// ⑤ 提交到归档内存（不落盘）。
	if _, _, err := s.c.setText(index, updatedText); err != nil {
		return nil, err
	}
	// ⑤-b 广播「这个文件被改过」。
	//
	// 主窗口可能把这个文件开在「归档编辑」里，那份标签持有的是**打开时的旧文本副本**；
	// 不同步的话，用户一保存就把这里的改动覆盖掉（2026-10-03 实测丢过改动）。
	// 复用现有通道：编辑区 store 监听 archive:batch-applied 并按 fileIndexes 重拉标签。
	emitFormViewFileChanged(index)

	// ⑥ 回读并重新投影，界面直接换新结果，不必再请求一次。
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

// emitFormViewFileChanged 广播「结构化视图改了某个文件」。
//
// 复用批量 / 脚本那条现成通道（`archive:batch-applied`）：主窗口的编辑区 store 监听它，
// 会按 `fileIndexes` 重拉对应标签的内容。**不广播就会丢改动** —— 编辑区里那份标签持有
// 打开时的旧文本副本，用户一保存就把可视化这边的改动覆盖掉（2026-10-03 用户实测丢过条目）。
//
// structural=false：这里只改文本、不动条目表，所以文件索引不会变，不需要重建树。
func emitFormViewFileChanged(index int32) {
	emitEvent("archive:batch-applied", map[string]any{
		"structural":  false,
		"fileIndexes": []int32{index},
		"source":      "formview",
	})
}

// sectionOccurrence 单独投影「某段第 N 次出现」。
//
// 为什么不能只在主投影里找：主投影会把**被行关联认领**的目标段（如独立掉落的 862 处 [list]）
// 从输出里移除（否则界面会被 862 个两列表格淹掉），于是内联列表的改动在
// `current.Sections` 里**根本找不到**，行数会被算成 0、报出"行号 1 超出范围"
// （2026-10-03 用户实测踩到）。所以定位与校验都要走这里，与查看器用同一个 API。
func sectionOccurrence(
	format formview.Format,
	view pvf.ScriptView,
	section string,
	occurrence int,
) *formview.ProjectedSection {
	return formview.ProjectSection(format, view, section, occurrence)
}

// countSectionOccurrences 数某段名在本文件里出现了几次（按 SectionID 去重）。
func countSectionOccurrences(view pvf.ScriptView, section string) int {
	key := strings.TrimSpace(section)
	seen := make(map[int]bool)
	for _, element := range view.Elements {
		if element.Kind != pvf.ScriptElementToken {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(element.Section), key) {
			continue
		}
		seen[element.SectionID] = true
	}
	return len(seen)
}

// rowCountOf 取投影里某段的行数（找不到返回 0）。
func rowCountOf(projection *formview.Projection, section string, occurrence int) int {
	for index := range projection.Sections {
		item := &projection.Sections[index]
		if strings.EqualFold(strings.TrimSpace(item.Section), strings.TrimSpace(section)) &&
			item.Occurrence == occurrence {
			return len(item.Rows)
		}
	}
	return 0
}

// cellValueIn 取投影里某格的原始取值。
func cellValueIn(projection *formview.Projection, section string, occurrence, row, column int) (string, bool) {
	for index := range projection.Sections {
		item := &projection.Sections[index]
		if !strings.EqualFold(strings.TrimSpace(item.Section), strings.TrimSpace(section)) ||
			item.Occurrence != occurrence {
			continue
		}
		if row < 0 || row >= len(item.Rows) {
			return "", false
		}
		cells := item.Rows[row].Cells
		if column < 0 || column >= len(cells) {
			return "", false
		}
		return strings.TrimSpace(cells[column].Value), true
	}
	return "", false
}

// validateCellValue 只挡会破坏文件结构的输入，不做业务取值判断
// （真实数据里存在规则枚举没登记的值，例如掉落方式 = 5，所以不按枚举卡）。
func validateCellValue(column formview.Column, value string) error {
	if value == "" {
		return fmt.Errorf("不能改成空值")
	}
	if strings.ContainsAny(value, " \t\r\n[]`") {
		return fmt.Errorf("不能包含空格、制表符、换行、反引号或方括号（会破坏文件结构）")
	}
	switch strings.TrimSpace(column.Type) {
	case formview.ColumnTypeInt, formview.ColumnTypeRate:
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return fmt.Errorf("这一列必须填整数")
		}
	}
	return nil
}

func (s *FormViewService) rulePath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.path == "" {
		return "(内置)"
	}
	return s.path
}

func (s *FormViewService) catalog() (formview.Catalog, error) {
	s.mu.RLock()
	if s.loaded {
		catalog, err := s.rules, s.err
		s.mu.RUnlock()
		return catalog, err
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return s.rules, s.err
	}
	rules, err := s.loadRules()
	s.rules, s.err, s.loaded = rules, err, true
	return rules, err
}

// loadRules 优先读仓库内数据文件（开发态改数据即生效），否则用内置副本。
func (s *FormViewService) loadRules() (formview.Catalog, error) {
	if s.path != "" {
		if _, statErr := os.Stat(s.path); statErr == nil {
			return formview.LoadFile(s.path)
		}
	}
	return formview.LoadDefault()
}

func normalizeFormViewPath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	return strings.Trim(value, "/")
}
