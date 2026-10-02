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
//			4	`character/partset/uniqueset.equ`	...
//
// （路径相对 `equipment/`，所以完整路径是 equipment/character/partset/uniqueset.equ）。
// 于是只按文本解析出来的注解引擎只能看到数字「4」，无法跳转。
//
// 做法：为命中的值 token 下发一条 link 注解（TargetFileIndex = 解析出的目标文件索引），并把
// 解析到的目标归档路径放进 Content —— 前端据此在悬停提示里显示目标路径。
// 注意：这类注解的 **Title 必须留空**，否则前端会按「有标题就渲染绿色名称标签」的既有规则
// 多画一个标签（见 frontend\src\components\CodeEditor.vue 的 annotationDecorations 与
// AnnotationWidget）；前端也只对「Title 为空的 link 注解」启用 Content 提示。
//
// 规则来自内嵌 config/external_links.json（业务规则外部数据，不硬编码）。
// 登记表解析走「段名 + 段内 token 序号」，不要求记录等宽 —— etc 里同一个段的记录宽度并不一致
// （有的记录名称在同一行，有的缩进到下一行），按固定步长切分会整体错位。
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
	// Path 是登记表的归档路径。
	Path string `json:"path"`
	// Section 是登记表里的记录段名，如 "equipment part set"。
	Section string `json:"section"`
	// IDToken / PathToken 是记录段内 token 序号：编号列与路径列。
	IDToken   int `json:"idToken"`
	PathToken int `json:"pathToken"`
	// PathPrefix 是登记表里路径需要补的前缀（该表存的是相对 equipment/ 的路径）。
	PathPrefix string `json:"pathPrefix"`
}

type externalLinkRule struct {
	ID     string             `json:"id"`
	Desc   string             `json:"description,omitempty"`
	Match  externalLinkMatch  `json:"match"`
	Source externalLinkSource `json:"source"`
	Table  externalLinkTable  `json:"table"`
}

type externalLinkDocument struct {
	Version int                `json:"version"`
	Links   []externalLinkRule `json:"links"`
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
			if len(rule.Match.Extensions) == 0 ||
				strings.TrimSpace(rule.Source.Section) == "" || rule.Source.Index < 0 ||
				strings.TrimSpace(rule.Table.Path) == "" || strings.TrimSpace(rule.Table.Section) == "" ||
				rule.Table.IDToken < 0 || rule.Table.PathToken < 0 ||
				rule.Table.IDToken == rule.Table.PathToken {
				continue
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
		target, ok := c.externalLinkTableLocked(rule.Table)[strings.ToLower(strings.TrimSpace(token.Value))]
		if !ok || target == "" {
			continue
		}
		// 解析不到目标文件时静默跳过：宁可没有下划线，也不给出点了没反应的链接。
		resolved, fileIndex, ok := findListTargetInArchive(c.archive, rule.Table.Path, target)
		if !ok {
			continue
		}
		annotations = append(annotations, EditorAnnotation{
			Start: int32(token.Start), End: int32(token.End),
			Type: "link", TargetFileIndex: fileIndex,
			// Content 只承载「目标路径」，供前端悬停提示；Title 留空以避开名称标签。
			Content: resolved,
		})
		linked[key] = struct{}{}
	}
	return annotations
}

func externalLinkTableCacheKey(table externalLinkTable) string {
	return fmt.Sprintf("%s|%s|%d|%d|%s",
		normalizeAnnotationPath(table.Path),
		strings.ToLower(strings.TrimSpace(table.Section)),
		table.IDToken, table.PathToken,
		strings.ToLower(strings.TrimSpace(table.PathPrefix)),
	)
}

// externalLinkTableLocked 取「编号 → 归档路径」表；惰性解析一次后缓存。
// 解析失败（表不存在 / 解码失败）缓存空表，避免每次打开文件都重试。
func (c *core) externalLinkTableLocked(table externalLinkTable) map[string]string {
	key := externalLinkTableCacheKey(table)
	if cached, ok := c.externalLinkTables[key]; ok {
		return cached
	}
	parsed := c.parseExternalLinkTableLocked(table)
	if c.externalLinkTables == nil {
		c.externalLinkTables = make(map[string]map[string]string)
	}
	c.externalLinkTables[key] = parsed
	return parsed
}

func (c *core) parseExternalLinkTableLocked(table externalLinkTable) map[string]string {
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

// parseExternalLinkTableText 从登记表文本里抽出「编号 → 归档路径」。
// 按「段名 + 段内 token 序号」取值：不要求记录等宽，也不会把 [hide equipment part set]
// 这类别的段算进来。同一编号重复登记时以先出现的为准（与清单关系的口径一致）。
func parseExternalLinkTableText(text string, table externalLinkTable) map[string]string {
	view := pvf.ParseScriptView(text)
	section := strings.TrimSpace(table.Section)

	type record struct {
		id     string
		target string
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
		switch element.Index {
		case table.IDToken:
			current.id = strings.ToLower(strings.TrimSpace(element.Value))
		case table.PathToken:
			current.target = element.Value
		}
	}

	result := make(map[string]string, len(order))
	for _, sectionID := range order {
		current := pending[sectionID]
		if current.id == "" || strings.TrimSpace(current.target) == "" {
			continue
		}
		if _, duplicate := result[current.id]; duplicate {
			continue
		}
		result[current.id] = joinExternalLinkPath(table.PathPrefix, current.target)
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
