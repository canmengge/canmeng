package services

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"pvfine/internal/formview"
	"pvfine/internal/pvf"
)

// 「添加掉落」：往 `etc/independent_drop.etc` 的 `[independent drop]` 段**末尾**
// （即最后一个 `[/independent drop]` 之前）追加一条新配置。
//
// 为什么走文本层而不是结构化引擎：
//   - 引擎的 `insert` 按**段名 + 锚点段**插入，而这里要插的是"段末尾、闭合标签之前"，
//     并且内联列表模式下还要同时插入紧跟其后的 `[list] … [/list]`；
//   - 该文件里 `[list]` 有 862 次，按段名操作会波及全部。
//   - 文本层只动一个位置，改完照样**重新投影逐格校验**，不通过就整体放弃。
//
// 与单元格编辑同一套语义：**只改归档内存，不落盘**（落盘仍走主工具条「保存 PVF」）。

// FormViewDropItem 是内联列表里的一条候选（物品ID + 权重）。
type FormViewDropItem struct {
	ItemID string `json:"itemId"`
	Weight string `json:"weight"`
}

// FormViewDropEntry 是「添加掉落」表单提交的一条新配置（字段与 17 列一一对应）。
type FormViewDropEntry struct {
	// IsAPC：false = 怪物（类型 0）/ true = APC（类型 1）。
	IsAPC bool `json:"isApc"`
	// MonsterID 是怪物 / APC 编号（必填）。
	MonsterID string `json:"monsterId"`
	// UseList：true = 内联列表（掉落方式 1）/ false = 单一物品（掉落方式 0）。
	UseList bool `json:"useList"`
	// ItemID 是「单一物品」模式下的物品编号。
	ItemID string `json:"itemId"`
	// List 是「内联列表」模式下的候选（物品编号 + 权重）。
	List []FormViewDropItem `json:"list"`
	// Rates 是五个难度的掉落率，**按百分比**（20 = 20%）；缺省 100。
	Rates []float64 `json:"rates"`
	// Counts 是五个难度的个数；缺省 1。
	Counts []int `json:"counts"`
	// LevelMin / LevelMax 是等级下限 / 上限，缺省 0。
	LevelMin int `json:"levelMin"`
	LevelMax int `json:"levelMax"`
	// JobLimit 是职业限制，缺省 -1（不限）。
	JobLimit string `json:"jobLimit"`
}

// 各段在文本里的缩进（与真实归档一致：数据行一个制表符，[list] 同层，候选两个）。
const (
	dropRowIndent  = "\t"
	dropListIndent = "\t"
	dropItemIndent = "\t\t"
	dropRateScale  = 10000 // 百分比 → 归档刻度（100% = 1000000）
)

// AddIndependentDrop 追加一条掉落配置，返回**重新投影后**的结果（只改归档内存，不落盘）。
func (s *FormViewService) AddIndependentDrop(
	filePath string,
	entry FormViewDropEntry,
) (*formview.Projection, error) {
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
	section, ok := format.LookupSection(dropSectionName)
	if !ok {
		return nil, fmt.Errorf("规则里没有段 [%s]", dropSectionName)
	}
	rowText, err := buildDropRowText(section, entry)
	if err != nil {
		return nil, err
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

	insertAt, err := lastSectionCloseOffset(text, dropSectionName)
	if err != nil {
		return nil, err
	}
	updatedText := text[:insertAt] + rowText + text[insertAt:]

	// 校验：重新投影，确认**新追加的那一行**每一格都等于期望值。
	view := pvf.ParseScriptView(updatedText)
	projected := sectionOccurrence(format, view, dropSectionName, 1)
	if projected == nil {
		return nil, fmt.Errorf("追加后重新投影失败：找不到段 [%s]", dropSectionName)
	}
	if len(projected.Rows) == 0 {
		return nil, fmt.Errorf("追加后重新投影失败：段 [%s] 没有任何行", dropSectionName)
	}
	want := strings.Split(strings.TrimSpace(strings.Trim(rowText, "\r\n")), "\t")
	got := projected.Rows[len(projected.Rows)-1].Cells
	for i := 0; i < len(want); i++ {
		if i >= len(got) {
			return nil, fmt.Errorf("追加后校验失败：第 %d 列缺失（期望 %q）", i+1, want[i])
		}
		if strings.TrimSpace(got[i].Value) != strings.TrimSpace(want[i]) {
			return nil, fmt.Errorf(
				"追加后校验失败：最后一行第 %d 列应为 %q，实际为 %q。已取消本次添加（没有写入任何内容）",
				i+1, want[i], got[i].Value)
		}
	}

	if _, _, err := s.c.setText(index, updatedText); err != nil {
		return nil, err
	}

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

// dropSectionName 是独立掉落的数据段名（与规则里一致）。
const dropSectionName = "independent drop"

// buildDropRowText 按规则把一条配置拼成文本（数据行 + 内联列表时紧跟的 [list] 块）。
func buildDropRowText(section formview.Section, entry FormViewDropEntry) (string, error) {
	monsterID, err := requireUnsignedInt("怪物/APC 编号", entry.MonsterID)
	if err != nil {
		return "", err
	}
	kind := "0"
	if entry.IsAPC {
		kind = "1"
	}

	// 掉落物品列：单一物品 = 物品编号；内联列表 = 0（与现有数据写法一致）。
	itemCell := "0"
	way := "1"
	if !entry.UseList {
		way = "0"
		itemCell, err = requireUnsignedInt("掉落物品编号", entry.ItemID)
		if err != nil {
			return "", err
		}
	}

	rates := make([]string, dropRateCount)
	for i := 0; i < dropRateCount; i++ {
		percent := 100.0
		if i < len(entry.Rates) {
			percent = entry.Rates[i]
		}
		if percent < 0 || percent > 100 {
			return "", fmt.Errorf("难度%d 掉落率必须在 0 ~ 100 之间（当前 %v）", i+1, percent)
		}
		rates[i] = strconv.FormatInt(int64(math.Round(percent*dropRateScale)), 10)
	}

	counts := make([]string, dropRateCount)
	for i := 0; i < dropRateCount; i++ {
		count := 1
		if i < len(entry.Counts) {
			count = entry.Counts[i]
		}
		if count < 0 {
			return "", fmt.Errorf("难度%d 个数不能为负数", i+1)
		}
		counts[i] = strconv.Itoa(count)
	}

	job := strings.TrimSpace(entry.JobLimit)
	if job == "" {
		job = "-1"
	} else if _, err := strconv.ParseInt(job, 10, 64); err != nil {
		return "", fmt.Errorf("职业限制必须是整数（-1 表示不限）: %q", job)
	}

	tokens := []string{
		kind, monsterID, itemCell,
		rates[0], rates[1], rates[2], rates[3], rates[4],
		counts[0], counts[1], counts[2], counts[3], counts[4],
		strconv.Itoa(entry.LevelMin), strconv.Itoa(entry.LevelMax),
		job, way,
	}
	if len(tokens) != section.RowTokens {
		// 规则与代码不一致时必须报错，绝不写出列数不对的行。
		return "", fmt.Errorf(
			"规则说这段每行 %d 个 token，但本功能拼出 %d 个；请先核对规则",
			section.RowTokens, len(tokens))
	}

	// 注意：**不要**再补一个换行 —— 插入点前面那一行自带行尾（\r\n），
	// 多写一个就会在归档里留下空行（用户 2026-10-03 实测截图发现）。
	var builder strings.Builder
	builder.WriteString(dropRowIndent)
	builder.WriteString(strings.Join(tokens, "\t"))
	builder.WriteString("\r\n")

	if entry.UseList {
		if len(entry.List) == 0 {
			return "", fmt.Errorf("选择「掉落物列表」时至少要有一条候选")
		}
		builder.WriteString(dropListIndent)
		builder.WriteString("[list]\r\n")
		for i, candidate := range entry.List {
			id, err := requireUnsignedInt(fmt.Sprintf("候选第 %d 条的物品编号", i+1), candidate.ItemID)
			if err != nil {
				return "", err
			}
			weight := strings.TrimSpace(candidate.Weight)
			if weight == "" {
				weight = "1000"
			}
			if _, err := strconv.ParseInt(weight, 10, 64); err != nil {
				return "", fmt.Errorf("候选第 %d 条的权重必须是整数: %q", i+1, candidate.Weight)
			}
			builder.WriteString(dropItemIndent)
			builder.WriteString(id)
			builder.WriteString("\t")
			builder.WriteString(weight)
			builder.WriteString("\r\n")
		}
		builder.WriteString(dropListIndent)
		builder.WriteString("[/list]\r\n")
	}
	return builder.String(), nil
}

// dropRateCount 是难度档数（掉落率 / 个数各有这么多档）。
const dropRateCount = 5

// requireUnsignedInt 校验"必须是非负整数"的字段并返回规范化文本。
func requireUnsignedInt(label, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s 不能为空", label)
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || parsed < 0 {
		return "", fmt.Errorf("%s 必须是非负整数: %q", label, value)
	}
	return strconv.FormatInt(parsed, 10), nil
}

// lastSectionCloseOffset 返回**最后一个** `[/section]` 所在行的起始偏移
// （新内容插在它之前 = 追加到该段末尾）。
func lastSectionCloseOffset(text, section string) (int, error) {
	tag := "[/" + strings.TrimSpace(section) + "]"
	found := -1
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
		if strings.EqualFold(strings.TrimSpace(line), tag) {
			found = lineStart
		}
		if next <= lineStart {
			break
		}
		lineStart = next
	}
	if found < 0 {
		return 0, fmt.Errorf("文件里找不到段 [%s] 的结束标签 %s", section, tag)
	}
	return found, nil
}
