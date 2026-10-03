package formview

import (
	"fmt"
	"strconv"
	"strings"

	"pvfine/internal/pvf"
)

// 投影上限：避免把上千次段出现 / 上万行一次性塞给界面（这是界面预算，不是游戏事实，
// 因此在代码里定，并在超出时明确告警；要全量时改这里即可）。
const (
	maxSectionOccurrences = 50
	maxSectionRows        = 2000
)

// Cell 是表格里的一格。
type Cell struct {
	// Value 是 token 的原样文本（写回时以它为准）。
	Value string `json:"value"`
	// Display 是给人看的文本（枚举翻译 / 百分比换算 / 「不限」）；与 Value 相同则留空。
	Display string `json:"display,omitempty"`
	// Start / End 是该 token 在编辑器文本里的偏移（UTF-16 单位，与 CodeMirror 一致）。
	Start int `json:"start"`
	End   int `json:"end"`
}

// RowLink 是一行关联到的另一段（如独立掉落的 [list]）：界面据此提供「双击查看」。
type RowLink struct {
	// Column 是触发链接的列下标（0 基），即规则里 Link.Column。
	Column int `json:"column"`
	// TargetSection 是被引用段的段名。
	TargetSection string `json:"targetSection"`
	// Occurrence 是被引用段在本文件里的第几次出现（1 基），与
	// ProjectedSection.Occurrence 对应——界面靠这两个值在同名段里定位。
	Occurrence int `json:"occurrence"`
	// Title 是查看器标题（来自规则）。
	Title string `json:"title,omitempty"`
}

// Row 是一行。
type Row struct {
	// Index 是行号（0 基）。
	Index int `json:"index"`
	// Complete 表示该行 token 数是否达到 rowTokens（false = 末行不完整 / 文件异常）。
	Complete bool   `json:"complete"`
	Cells    []Cell `json:"cells"`
	// Link 是本行关联到的另一段（规则配了 links 且本行命中时才有）。
	Link *RowLink `json:"link,omitempty"`
}

// ProjectedSection 是一段（一次出现）的投影结果。
type ProjectedSection struct {
	// Section 是段名（不带方括号）。
	Section string `json:"section"`
	// Label 是段显示名。
	Label string `json:"label"`
	// Kind 是呈现方式（当前只有 table）。
	Kind string `json:"kind"`
	// Occurrence 是同一段名在本文件里的第几次出现（1 基）。
	Occurrence int `json:"occurrence"`
	// Columns 是表头（来自规则，与 RowTokens 等长）。
	Columns []string `json:"columns"`
	// Rows 是按 RowTokens 切出来的行。
	Rows []Row `json:"rows"`
	// TokenCount 是本段的 token 总数。
	TokenCount int `json:"tokenCount"`
	// Warnings 是本段特有的告警。
	Warnings []string `json:"warnings,omitempty"`

	// start 是本段第一个 token 在编辑器文本里的偏移。不导出：只用于投影内部按
	// 偏移顺序建立「行 → 被引用段」的关联。
	start int
}

// Projection 是一个文件的投影结果（只读）。
type Projection struct {
	FormatID    string             `json:"formatId"`
	FormatLabel string             `json:"formatLabel"`
	File        string             `json:"file"`
	TokenCount  int                `json:"tokenCount"`
	Sections    []ProjectedSection `json:"sections"`
	Warnings    []string           `json:"warnings"`
}

// tokenGroup 是同一段一次出现里的 token 序列。
type tokenGroup struct {
	section string
	id      int
	tokens  []pvf.ScriptElement
}

// Project 把内核的宽容语义投影（pvf.ParseScriptView）按规则切成「段 → 行 → 列」。
//
// 只做投影，不修改任何文本；行的切分唯一依据是规则里的 RowTokens。
func Project(filePath string, format Format, view pvf.ScriptView) *Projection {
	projection := &Projection{
		FormatID:    format.ID,
		FormatLabel: format.DisplayLabel(),
		File:        filePath,
		Sections:    []ProjectedSection{},
		Warnings:    []string{},
	}
	warn := func(text string) {
		projection.Warnings = append(projection.Warnings, text)
	}

	// 1) 按「段的一次出现」（SectionID）收集 token，保持首次出现顺序。
	groups := make([]*tokenGroup, 0)
	byID := make(map[int]*tokenGroup)
	occurrence := make(map[string]int)
	for _, element := range view.Elements {
		if element.Kind != pvf.ScriptElementToken {
			continue
		}
		projection.TokenCount++
		group, ok := byID[element.SectionID]
		if !ok {
			group = &tokenGroup{section: element.Section, id: element.SectionID}
			byID[element.SectionID] = group
			groups = append(groups, group)
		}
		group.tokens = append(group.tokens, element)
	}

	// 2) 逐个规则段切表。一段在本文件里可能出现多次（如每个配置行后面跟一个 [list]），
	//    每次出现单独成表，用 Occurrence 区分。
	matchedGroups := make(map[int]bool, len(groups))
	for _, rule := range format.Sections {
		found := false
		skipped := 0
		for _, group := range groups {
			if !strings.EqualFold(strings.TrimSpace(group.section), strings.TrimSpace(rule.Section)) {
				continue
			}
			found = true
			matchedGroups[group.id] = true
			occurrence[rule.Section]++
			if occurrence[rule.Section] > maxSectionOccurrences {
				skipped++
				continue
			}
			projection.Sections = append(projection.Sections, projectSection(rule, group, occurrence[rule.Section]))
		}
		if !found && !rule.Optional {
			warn(fmt.Sprintf("段 [%s]（%s）未在本文件中出现", rule.Section, rule.Label))
		}
		if skipped > 0 {
			warn(fmt.Sprintf("段 [%s]（%s）共出现 %d 次，只投影前 %d 次（其余已略过）",
				rule.Section, rule.Label, occurrence[rule.Section], maxSectionOccurrences))
		}
	}

	// 3) 规则没覆盖到的段：只报数量，避免把界面刷满。
	unknown := make(map[string]int)
	unknownOrder := make([]string, 0)
	for _, group := range groups {
		if matchedGroups[group.id] {
			continue
		}
		name := strings.TrimSpace(group.section)
		if name == "" {
			name = "(无段)"
		}
		if _, seen := unknown[name]; !seen {
			unknownOrder = append(unknownOrder, name)
		}
		unknown[name] += len(group.tokens)
	}
	for _, name := range unknownOrder {
		warn(fmt.Sprintf("段 [%s] 未在规则中定义（%d 个 token 未投影）", name, unknown[name]))
	}

	// 4) 行 → 被引用段 的关联（只有规则里配了 links 的段才需要做）。
	resolveLinks(projection, format)
	return projection
}

// linkTarget 是被引用段的一次出现，供 resolveLinks 认领。
type linkTarget struct {
	sectionIndex int
	start        int
	claimed      bool
}

// resolveLinks 按规则的 links 定义，把每一行关联到**它后面的**目标段出现。
//
// 配对依据是文本偏移顺序，不是计数：取「起点在本行之后、尚未被认领、且最靠前」
// 的那一次出现。这样某一行没有列表、或中间插了别的段，都不会整体错位。
func resolveLinks(projection *Projection, format Format) {
	targets := make(map[string][]*linkTarget)
	for index := range projection.Sections {
		section := &projection.Sections[index]
		key := sectionKey(section.Section)
		targets[key] = append(targets[key], &linkTarget{sectionIndex: index, start: section.start})
	}

	for _, rule := range format.Sections {
		if len(rule.Links) == 0 {
			continue
		}
		key := sectionKey(rule.Section)
		for index := range projection.Sections {
			section := &projection.Sections[index]
			if sectionKey(section.Section) != key {
				continue
			}
			for rowIndex := range section.Rows {
				row := &section.Rows[rowIndex]
				rowEnd := rowEndOffset(row)
				for _, link := range rule.Links {
					if !linkMatches(link, row) {
						continue
					}
					claimed := claimTarget(targets[sectionKey(link.TargetSection)], rowEnd)
					if claimed == nil {
						continue
					}
					target := &projection.Sections[claimed.sectionIndex]
					row.Link = &RowLink{
						Column:        link.Column,
						TargetSection: target.Section,
						Occurrence:    target.Occurrence,
						Title:         strings.TrimSpace(link.Title),
					}
				}
			}
		}
	}
}

// claimTarget 取「出现在 after 之后、最靠前且尚未被认领」的一次出现。
func claimTarget(list []*linkTarget, after int) *linkTarget {
	var best *linkTarget
	for _, item := range list {
		if item.claimed || item.start < after {
			continue
		}
		if best == nil || item.start < best.start {
			best = item
		}
	}
	if best != nil {
		best.claimed = true
	}
	return best
}

// linkMatches 判断某一行是否命中链接的触发条件。
func linkMatches(link Link, row *Row) bool {
	if link.Column < 0 || link.Column >= len(row.Cells) {
		return false
	}
	value := strings.TrimSpace(row.Cells[link.Column].Value)
	for _, want := range link.When {
		if value == strings.TrimSpace(want) {
			return true
		}
	}
	return false
}

// rowEndOffset 返回一行最后一个 token 的结束偏移。
func rowEndOffset(row *Row) int {
	end := 0
	for _, cell := range row.Cells {
		if cell.End > end {
			end = cell.End
		}
	}
	return end
}

// sectionKey 是段名比较用的归一化键。
func sectionKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// projectSection 把一组 token 按 RowTokens 切成行。
func projectSection(rule Section, group *tokenGroup, occurrence int) ProjectedSection {
	rowTokens := rule.RowTokens
	if rowTokens < 1 {
		rowTokens = 1
	}
	projected := ProjectedSection{
		Section:    rule.Section,
		Label:      strings.TrimSpace(rule.Label),
		Kind:       rule.SectionKind(),
		Occurrence: occurrence,
		Columns:    rule.ColumnLabels(),
		Rows:       make([]Row, 0, len(group.tokens)/rowTokens+1),
		TokenCount: len(group.tokens),
		start:      groupStart(group),
	}

	for start := 0; start < len(group.tokens) && len(projected.Rows) < maxSectionRows; start += rowTokens {
		end := start + rowTokens
		if end > len(group.tokens) {
			end = len(group.tokens)
		}
		row := Row{
			Index:    len(projected.Rows),
			Complete: end-start == rowTokens,
			Cells:    make([]Cell, 0, rowTokens),
		}
		for i := start; i < end; i++ {
			columnIndex := i - start
			column := Column{}
			if columnIndex < len(rule.Columns) {
				column = rule.Columns[columnIndex]
			}
			row.Cells = append(row.Cells, makeCell(column, group.tokens[i]))
		}
		projected.Rows = append(projected.Rows, row)
	}

	if total := len(group.tokens) / rowTokens; total > maxSectionRows {
		projected.Warnings = append(projected.Warnings, fmt.Sprintf(
			"本段共 %d 行，只展示前 %d 行（其余已略过）", total, maxSectionRows))
	}
	if remainder := len(group.tokens) % rowTokens; remainder != 0 {
		projected.Warnings = append(projected.Warnings, fmt.Sprintf(
			"本段共 %d 个 token，不是 rowTokens(%d) 的整数倍，末行缺 %d 个（文件可能异常）",
			len(group.tokens), rowTokens, rowTokens-remainder))
	}
	if len(projected.Rows) == 0 {
		projected.Warnings = append(projected.Warnings, "本段没有任何 token（空段）")
	}
	return projected
}

// groupStart 返回一组 token 在文本里的起始偏移（空组返回 0）。
func groupStart(group *tokenGroup) int {
	if group == nil || len(group.tokens) == 0 {
		return 0
	}
	return group.tokens[0].Start
}

// makeCell 生成一格，并按列类型补上可读文本。
func makeCell(column Column, element pvf.ScriptElement) Cell {
	cell := Cell{
		Value: element.Value,
		Start: element.Start,
		End:   element.End,
	}
	switch columnType(column) {
	case ColumnTypeEnum:
		if text, ok := column.Values[strings.TrimSpace(element.Value)]; ok && strings.TrimSpace(text) != "" {
			cell.Display = text
		}
	case ColumnTypeRate:
		if scale := column.Scale; scale > 0 {
			if value, err := strconv.ParseInt(strings.TrimSpace(element.Value), 10, 64); err == nil {
				cell.Display = formatPercent(value, scale)
			}
		}
	case ColumnTypeRef:
		if none := strings.TrimSpace(column.NoneValue); none != "" && strings.TrimSpace(element.Value) == none {
			cell.Display = "（不限）"
		}
	}
	return cell
}

// formatPercent 把刻度值换算成百分比文本（最多 4 位小数，去掉多余的 0）。
func formatPercent(value, scale int64) string {
	percent := float64(value) * 100 / float64(scale)
	text := strconv.FormatFloat(percent, 'f', 4, 64)
	if strings.ContainsRune(text, '.') {
		text = strings.TrimRight(text, "0")
		text = strings.TrimRight(text, ".")
	}
	if text == "" || text == "-0" {
		text = "0"
	}
	return text + "%"
}
