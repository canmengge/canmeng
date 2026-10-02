package services

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"

	appconfig "pvfine/config"
	"pvfine/internal/pvf"
)

// 外部登记表链接（external link annotations）。
//
// 背景：PVF 里有一类「编号外键」字段，它本身不含路径，真正的目标文件登记在另一份表里。例如
// 装备脚本的
//
//	[part set index]
//		4
//
// 只写一个数字，映射表在
//
//	etc/equipmentpartset.etc
//		[equipment part set]
//			4	`character/partset/uniqueset.equ`	`神器装扮  套装`	...
//
// （路径相对 `equipment/`，所以完整路径是 equipment/character/partset/uniqueset.equ）。
// 于是只按文本解析出来的注解引擎只能看到数字「4」，无法跳转。
//
// 做法：为命中的值 token 下发一条 link 注解：
//   - TargetFileIndex = 解析出的目标文件索引（Ctrl+单击跳转）
//   - Content         = 目标归档路径（前端悬停提示里显示「目标」）
//   - Title           = 登记表给出的名称（如套装名；前端悬停提示里显示「套装」，**不**渲染名称标签）
//
// 注意两个约定（前端 CodeEditor.vue 与这里成对出现，改一处必须改另一处）：
//  1. 这类注解的 Title 虽然非空，但前端**不**给它渲染绿色名称标签（否则每处多一个标签）；
//     判定方式：type === "link" 且 Content 非空。
//  2. 前端只对这类注解挂「↗ CTRL+左键可跳转…」装饰提示。
//
// 规则来自内嵌 config/external_links.json（业务规则外部数据，不硬编码）。
// 登记表解析走「段名 + 段内 token 序号」，不要求记录等宽 —— etc 里同一个段的记录宽度并不一致
// （有的记录名称与编号同行，有的缩进到下一行），按固定步长切分会整体错位。
//
// 性能：登记表在首次打开带该字段的文件时惰性解析一次并缓存（不参与打开热路径，符合 F3），
// 内容改动时随 resetAnnotationCachesLocked 一起作废。

// externalLinkMatch 限定规则作用的文件范围（必须显式声明扩展名）。
type externalLinkMatch struct {
	Extensions []string `json:"extensions,omitempty"`
}

// externalLinkSource 指向「承载编号的那个字段」。
type externalLinkSource struct {
	// Section 是字段所在段名（不含方括号），如 "part set index"。
	Section string `json:"section"`
	// Index 是该段内第几个直接 token（0 起），通常是 0。
	Index int `json:"index"`
}

// externalLinkTable 指向登记表及其列约定。
type externalLinkTable struct {
	// Format 决定登记表怎么切分记录：
	//
	//	"section"（默认）—— 每个 `[section]` 段是一条记录，IDToken/PathToken 是「段内序号」，
	//	                    如 etc/equipmentpartset.etc 的 [equipment part set]；
	//	"flat"          —— 整份文本按 RecordTokens 等宽切分，IDToken/PathToken 是「记录内偏移」，
	//	                    如 list/appendage.lst（每行 `ID \`路径\``）。
	Format string `json:"format,omitempty"`
	// Path 是登记表的归档路径。
	Path string `json:"path"`
	// Section 是记录段名（仅 format=section），如 "equipment part set"。
	Section string `json:"section,omitempty"`
	// RecordTokens 是每条记录的 token 数（仅 format=flat），如 list/*.lst 的 2。
	RecordTokens int `json:"recordTokens,omitempty"`
	// IDToken / PathToken 是编号列与路径列（section 用段内序号；flat 用记录内偏移）。
	IDToken   int `json:"idToken"`
	PathToken int `json:"pathToken"`
	// NameToken 是登记表内「名称」列的序号（可选）。取到的值可能是字符串表
	// 占位符（如 `<3::rareset_name_cap>`），取用时再解析成实际文本。
	NameToken *int `json:"nameToken,omitempty"`
	// NameSection 是登记表**没有**名称列时，去目标文件里读名称的段名（可选，如 "name"）。
	NameSection string `json:"nameSection,omitempty"`
	// PathPrefix 是登记表里路径需要补的前缀（如该表存的是相对 equipment/ 的路径）。
	PathPrefix string `json:"pathPrefix"`
}

type externalLinkRule struct {
	ID    string            `json:"id"`
	Desc  string            `json:"description,omitempty"`
	Match externalLinkMatch `json:"match"`
	// Source 是承载编号的那个字段。
	Source externalLinkSource `json:"source"`
	Table  externalLinkTable  `json:"table"`
}

type externalLinkDocument struct {
	Version int                `json:"version"`
	Links   []externalLinkRule `json:"links"`
}

// externalLinkTarget 是登记表一行的解析结果。
type externalLinkTarget struct {
	// Path 是目标文件的归档路径（已补前缀）。
	Path string
	// Name 是登记表给出的名称（可能是字符串表占位符原文）。
	Name string
}

var (
	externalLinkRulesOnce sync.Once
	externalLinkRules     []externalLinkRule
)

// externalLinkRulesFromConfig 解析内嵌规则。解析失败或规则不完整时返回 nil：功能静默关闭，
// 不影响其它注解（与「外置注释数据损坏即退回内置」的既有口径一致）。
func externalLinkRulesFromConfig() []externalLinkRule {
	externalLinkRulesOnce.Do(func() {
		var document externalLinkDocument
		if err := json.Unmarshal(appconfig.ExternalLinksJSON, &document); err != nil {
			return
		}
		rules := make([]externalLinkRule, 0, len(document.Links))
		for _, rule := range document.Links {
			format := strings.ToLower(strings.TrimSpace(rule.Table.Format))
			if format == "" {
				format = "section"
			}
			rule.Table.Format = format
			if len(rule.Match.Extensions) == 0 ||
				strings.TrimSpace(rule.Source.Section) == "" || rule.Source.Index < 0 ||
				strings.TrimSpace(rule.Table.Path) == "" ||
				rule.Table.IDToken < 0 || rule.Table.PathToken < 0 ||
				rule.Table.IDToken == rule.Table.PathToken {
				continue
			}
			if format == "section" {
				if strings.TrimSpace(rule.Table.Section) == "" {
					continue
				}
			} else if format == "flat" {
				// 扁平表必须能按等宽切分，否则整份表会错位（宁可不生效）。
				if rule.Table.RecordTokens <= 0 ||
					rule.Table.RecordTokens <= rule.Table.IDToken ||
					rule.Table.RecordTokens <= rule.Table.PathToken {
					continue
				}
			} else {
				continue
			}
			if rule.Table.NameToken != nil && *rule.Table.NameToken < 0 {
				rule.Table.NameToken = nil
			}
			rules = append(rules, rule)
		}
		externalLinkRules = rules
	})
	return externalLinkRules
}

func externalLinkMatchFile(match externalLinkMatch, filePath string) bool {
	if len(match.Extensions) == 0 {
		return false
	}
	ext := strings.ToLower(path.Ext(filePath))
	for _, candidate := range match.Extensions {
		if strings.EqualFold(strings.TrimSpace(candidate), ext) {
			return true
		}
	}
	return false
}

// externalLinkSourceToken 在已解析的脚本视图里取「源字段」的那个值 token。
func externalLinkSourceToken(view pvf.ScriptView, source externalLinkSource) (pvf.ScriptElement, bool) {
	section := strings.TrimSpace(source.Section)
	for _, element := range view.Elements {
		if element.Kind != pvf.ScriptElementToken || element.Index != source.Index {
			continue
		}
		if strings.EqualFold(element.Section, section) {
			return element, true
		}
	}
	return pvf.ScriptElement{}, false
}

// appendExternalLinkAnnotationsLocked 为「编号外键」字段补上可跳转注解。
func (c *core) appendExternalLinkAnnotationsLocked(
	filePath string,
	view pvf.ScriptView,
	annotations []EditorAnnotation,
) []EditorAnnotation {
	if c.archive == nil {
		return annotations
	}
	rules := externalLinkRulesFromConfig()
	if len(rules) == 0 {
		return annotations
	}

	linked := make(map[string]struct{}, len(annotations))
	for _, annotation := range annotations {
		if annotation.TargetFileIndex >= 0 {
			linked[editorAnnotationRangeKey(annotation.Start, annotation.End)] = struct{}{}
		}
	}

	for _, rule := range rules {
		if !externalLinkMatchFile(rule.Match, filePath) {
			continue
		}
		token, ok := externalLinkSourceToken(view, rule.Source)
		if !ok {
			continue
		}
		key := editorAnnotationRangeKey(int32(token.Start), int32(token.End))
		if _, exists := linked[key]; exists {
			continue
		}
		entry, ok := c.externalLinkTableLocked(rule.Table)[strings.ToLower(strings.TrimSpace(token.Value))]
		if !ok || entry.Path == "" {
			continue
		}
		// 解析不到目标文件时静默跳过：宁可没有下划线，也不给出点了没反应的链接。
		resolved, fileIndex, ok := findListTargetInArchive(c.archive, rule.Table.Path, entry.Path)
		if !ok {
			continue
		}
		// Title = 名称（如套装名 / 状态名）。这类注解不渲染名称标签，
		// 只由前端拼进悬停提示（见本文件顶部约定）。
		// 登记表没有名称列时退回目标文件里的 NameSection（如 .apd 的 [name]）；
		// 两者都取不到就留空（悬停只显示目标路径）。
		name := resolveExternalLinkName(c, entry.Name)
		if name == "" && strings.TrimSpace(rule.Table.NameSection) != "" {
			name = resolveExternalLinkName(c, c.readRelationTargetNameLocked(
				fileIndex, rule.Table.Path, rule.Table.NameSection,
			))
		}
		annotations = append(annotations, EditorAnnotation{
			Start: int32(token.Start), End: int32(token.End),
			Title:   name,
			Content: resolved,
			Type:    "link", TargetFileIndex: fileIndex,
		})
		linked[key] = struct{}{}
	}
	return annotations
}

// resolveExternalLinkName 把登记表里的名称解析成可显示的文本：名称大多写成字符串表占位符
// （如 `<3::equipmentpartset_1>`），交给既有的占位符解析取文。
// 解析不出来（拿回来仍是占位符原文）时返回空 —— 悬停里显示 `<3::xxx>` 比不显示名称更难读。
func resolveExternalLinkName(c *core, value string) string {
	name := strings.TrimSpace(value)
	if name == "" {
		return ""
	}
	resolved := strings.TrimSpace(c.resolveNameTextLocked(name))
	if strings.Contains(resolved, "<") && strings.Contains(resolved, "::") {
		return ""
	}
	return resolved
}

func externalLinkTableCacheKey(table externalLinkTable) string {
	nameToken := -1
	if table.NameToken != nil {
		nameToken = *table.NameToken
	}
	return fmt.Sprintf("%s|%s|%s|%d|%d|%d|%d|%s",
		strings.ToLower(strings.TrimSpace(table.Format)),
		normalizeAnnotationPath(table.Path),
		strings.ToLower(strings.TrimSpace(table.Section)),
		table.RecordTokens, table.IDToken, table.PathToken, nameToken,
		strings.ToLower(strings.TrimSpace(table.PathPrefix)),
	)
}

// externalLinkTableLocked 取「编号 → 登记信息」表；惰性解析一次后缓存。
// 解析失败（表不存在 / 解码失败）缓存空表，避免每次打开文件都重试。
func (c *core) externalLinkTableLocked(table externalLinkTable) map[string]externalLinkTarget {
	key := externalLinkTableCacheKey(table)
	if cached, ok := c.externalLinkTables[key]; ok {
		return cached
	}
	parsed := c.parseExternalLinkTableLocked(table)
	if c.externalLinkTables == nil {
		c.externalLinkTables = make(map[string]map[string]externalLinkTarget)
	}
	c.externalLinkTables[key] = parsed
	return parsed
}

func (c *core) parseExternalLinkTableLocked(table externalLinkTable) map[string]externalLinkTarget {
	index, ok := c.archive.FindList(table.Path)
	if !ok {
		return nil
	}
	text, err := c.cachedDecodedText(index, c.archive)
	if err != nil {
		return nil
	}
	return parseExternalLinkTableText(text, table)
}

// parseExternalLinkTableText 从登记表文本里抽出「编号 → 目标路径 + 名称」。
// 同一编号重复登记时以先出现的为准（与清单关系的口径一致）。
func parseExternalLinkTableText(text string, table externalLinkTable) map[string]externalLinkTarget {
	view := pvf.ParseScriptView(text)
	if strings.EqualFold(strings.TrimSpace(table.Format), "flat") {
		return parseFlatExternalLinkTable(view, table)
	}
	return parseSectionExternalLinkTable(view, table)
}

// parseSectionExternalLinkTable 处理 `[section]` 结构的登记表（如 etc/equipmentpartset.etc）：
// 按「段名 + 段内 token 序号」取值 —— 不要求记录等宽（同一段里名称可能与编号同行、也可能缩进
// 到下一行），也不会把 [hide equipment part set] 这类别的段算进来。
func parseSectionExternalLinkTable(view pvf.ScriptView, table externalLinkTable) map[string]externalLinkTarget {
	section := strings.TrimSpace(table.Section)
	nameToken := -1
	if table.NameToken != nil {
		nameToken = *table.NameToken
	}

	type record struct {
		id     string
		target string
		name   string
	}
	// 按 SectionID 分组：登记表里每个 [segment] 开段就是一条记录。
	pending := make(map[int]*record)
	order := make([]int, 0, 64)
	for _, element := range view.Elements {
		if element.Kind != pvf.ScriptElementToken || !strings.EqualFold(element.Section, section) {
			continue
		}
		current := pending[element.SectionID]
		if current == nil {
			current = &record{}
			pending[element.SectionID] = current
			order = append(order, element.SectionID)
		}
		switch {
		case element.Index == table.IDToken:
			current.id = strings.ToLower(strings.TrimSpace(element.Value))
		case element.Index == table.PathToken:
			current.target = element.Value
		case nameToken >= 0 && element.Index == nameToken:
			current.name = element.Value
		}
	}

	result := make(map[string]externalLinkTarget, len(order))
	for _, sectionID := range order {
		current := pending[sectionID]
		if current.id == "" || strings.TrimSpace(current.target) == "" {
			continue
		}
		if _, duplicate := result[current.id]; duplicate {
			continue
		}
		result[current.id] = externalLinkTarget{
			Path: joinExternalLinkPath(table.PathPrefix, current.target),
			Name: strings.TrimSpace(current.name),
		}
	}
	return result
}

// parseFlatExternalLinkTable 处理等宽扁平的登记表（如 list/appendage.lst：每行 `ID `路径“）：
// 把全部 token 拉平后按 RecordTokens 切分，IDToken/PathToken 就是记录内偏移。
func parseFlatExternalLinkTable(view pvf.ScriptView, table externalLinkTable) map[string]externalLinkTarget {
	step := table.RecordTokens
	if step <= 0 || step <= table.IDToken || step <= table.PathToken {
		return nil
	}
	tokens := make([]pvf.ScriptElement, 0, len(view.Elements))
	for _, element := range view.Elements {
		if element.Kind == pvf.ScriptElementToken {
			tokens = append(tokens, element)
		}
	}
	nameToken := -1
	if table.NameToken != nil {
		nameToken = *table.NameToken
	}

	result := make(map[string]externalLinkTarget, len(tokens)/step)
	for offset := 0; offset+step <= len(tokens); offset += step {
		id := strings.ToLower(strings.TrimSpace(tokens[offset+table.IDToken].Value))
		if id == "" {
			continue
		}
		if _, duplicate := result[id]; duplicate {
			continue
		}
		path := joinExternalLinkPath(table.PathPrefix, tokens[offset+table.PathToken].Value)
		if path == "" {
			continue
		}
		target := externalLinkTarget{Path: path}
		if nameToken >= 0 && nameToken < step {
			target.Name = strings.TrimSpace(tokens[offset+nameToken].Value)
		}
		result[id] = target
	}
	return result
}

// joinExternalLinkPath 给登记表里的相对路径补前缀（表里已经带前缀的不重复补）。
func joinExternalLinkPath(prefix, target string) string {
	target = strings.Trim(strings.ReplaceAll(strings.TrimSpace(target), "\\", "/"), "/")
	if target == "" {
		return ""
	}
	prefix = strings.Trim(strings.ReplaceAll(strings.TrimSpace(prefix), "\\", "/"), "/")
	if prefix == "" || strings.HasPrefix(strings.ToLower(target), strings.ToLower(prefix)+"/") {
		return target
	}
	return prefix + "/" + target
}
