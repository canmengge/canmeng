package services

import (
	"fmt"
	"path"
	"strings"

	annotationrules "pvfine/internal/annotations"
	"pvfine/internal/pvf"
)

type EditorAnnotation struct {
	Start           int32           `json:"start"`
	End             int32           `json:"end"`
	Title           string          `json:"title"`
	Content         string          `json:"content"`
	Type            string          `json:"type"`
	TargetFileIndex int32           `json:"targetFileIndex"`
	RuleIDs         []string        `json:"ruleIds,omitempty"`
	Image           *ImageReference `json:"image,omitempty"`
	InlineImage     bool            `json:"inlineImage,omitempty"`
	Placeholder     *PlaceholderRef `json:"placeholder,omitempty"`
}

// PlaceholderRef identifies the string-table entry a placeholder annotation
// resolves through, so the editor can offer to rewrite — or create — that text.
type PlaceholderRef struct {
	TableIndex int32  `json:"tableIndex"`
	Key        string `json:"key"`
	Fallback   bool   `json:"fallback,omitempty"`
	// Missing reports a placeholder no table answers yet: the editor offers to
	// create the entry, which is how a new file gets its display text.
	Missing bool `json:"missing,omitempty"`
}

// missingPlaceholderLabel is the tag shown for a `<table::key>` placeholder no
// string table answers yet; clicking it creates the entry.
const missingPlaceholderLabel = "未定义"

// TreeAnnotation is one annotation attached to a path in the explorer.
type TreeAnnotation struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Type    string   `json:"type"`
	RuleIDs []string `json:"ruleIds,omitempty"`
}

type relationTarget struct {
	reference   annotationrules.Reference
	nameSection string
	listPath    string
	nameLoaded  bool
}

// pathAnnotationsForNode 计算**单个**节点的目录标注。
//
// 2026-09-24 性能修复（P-2006）：原实现 buildPathAnnotations 在打开归档时对全部节点
// 预计算（约 467.8 万次 engine.AnnotatePath），在 1.0 的标注库下需约 112 秒。改成
// 「只算调用方真正要看的那个节点」，全量预计算由 core.treeAnnotationsFor 的按需缓存
// 取代。返回 nil 表示该路径没有任何 path 类标注（绝大多数路径如此）。
func pathAnnotationsForNode(engine *annotationrules.Engine, path string, isDir bool) []TreeAnnotation {
	if engine == nil {
		return nil
	}
	matches := engine.AnnotatePath(path, isDir)
	if len(matches) == 0 {
		return nil
	}
	annotations := make([]TreeAnnotation, 0, len(matches))
	for _, match := range matches {
		annotations = append(annotations, TreeAnnotation{
			Title: match.Title, Content: match.Content, Type: match.Type,
			RuleIDs: append([]string(nil), match.RuleIDs...),
		})
	}
	return annotations
}

// annotationRowLimit 是允许做逐行标注解析的行数上限。
// 标注解析要跑 ParseScriptView（整文 []rune + 逐 token 建表）并对每行解析一次目标文件
// （解码 + 取字段），复杂度 ≈ O(行数 × 目标文件大小)；几十万行的 list/*.lst 会在
// GetFile 的全局锁内跑几十秒，界面完全冻结。超过上限直接不生成标注：
// 文件照常打开、可编辑、可搜索定位。
const annotationRowLimit = 60000

// annotationCountLimit 是单文件标注条数硬上限。几十万条标注既会让 JSON 跨 IPC
// 变得巨大，也会让前端为每条构造 Widget，同样导致卡顿；超出即截断。
const annotationCountLimit = 20000

// listNameCacheLimit 是「清单行 → 目标名」缓存的条目上限；超出整表清空（名称可重算）。
const listNameCacheLimit = 50000

func (c *core) editorAnnotationsLocked(index int32, text string) ([]EditorAnnotation, error) {
	if c.annotationErr != nil {
		return nil, c.annotationErr
	}
	if c.annotationEngine == nil || c.archive == nil {
		return nil, nil
	}
	if c.editorAnnotation.valid && c.editorAnnotation.fileIndex == index && c.editorAnnotation.text == text {
		return cloneEditorAnnotations(c.editorAnnotation.annotations), nil
	}
	if strings.Count(text, "\n")+1 > annotationRowLimit {
		c.editorAnnotation = editorAnnotationCache{valid: true, fileIndex: index, text: text}
		return nil, nil
	}
	filePath := c.archive.Path(index)
	view := pvf.ParseScriptView(text)
	results := c.annotationEngine.AnnotateWithContextAndListResolver(
		filePath, view, c.resolveAnnotationReferenceContextLocked, c.resolveListAnnotationReferenceLocked,
	)
	annotations := make([]EditorAnnotation, 0, len(results))
	for _, result := range results {
		annotations = append(annotations, EditorAnnotation{
			Start: int32(result.Start), End: int32(result.End),
			Title: result.Title, Content: result.Content, Type: result.Type,
			TargetFileIndex: result.TargetFileIndex,
			RuleIDs:         append([]string(nil), result.RuleIDs...),
			Image:           imageReferenceFromAnnotation(result.Image),
			InlineImage:     result.InlineImage,
		})
	}
	annotations = c.appendUnindexedListLinksLocked(filePath, view, annotations)
	annotations = c.appendPlaceholderAnnotationsLocked(view, annotations)
	if len(annotations) > annotationCountLimit {
		annotations = annotations[:annotationCountLimit]
	}
	c.editorAnnotation = editorAnnotationCache{
		valid:       true,
		fileIndex:   index,
		text:        text,
		annotations: cloneEditorAnnotations(annotations),
	}
	return annotations, nil
}

// appendPlaceholderAnnotationsLocked surfaces the text behind the newer
// clients' `<table::key>` placeholders. The editor keeps showing (and writing)
// the placeholder itself — rewriting it would change the stored data — and the
// resolved text is attached as a display-only tag next to it. The tag carries
// the table index and key so it can be edited in place (SetPlaceholderText).
//
// A placeholder no table answers yet is annotated too, with Missing set: that
// is how a brand-new file gets its text, because the editor can then create the
// entry instead of the user having to open the (possibly 49 MB) table.
func (c *core) appendPlaceholderAnnotationsLocked(view pvf.ScriptView, annotations []EditorAnnotation) []EditorAnnotation {
	if c.archive == nil {
		return annotations
	}
	for _, element := range view.Elements {
		if element.Kind != pvf.ScriptElementToken {
			continue
		}
		index, key, ok := pvf.ParsePlaceholder(element.Value)
		if !ok {
			continue
		}
		resolution, found := c.archive.ResolveStringTable(index, key)
		if !found {
			annotations = append(annotations, EditorAnnotation{
				Start:           int32(element.Start),
				End:             int32(element.End),
				Title:           missingPlaceholderLabel,
				Content:         element.Value + "\n该字符串表里还没有这个键，单击可创建并填写译文",
				Type:            "placeholder-missing",
				TargetFileIndex: -1,
				Placeholder: &PlaceholderRef{
					TableIndex: int32(index),
					Key:        key,
					Missing:    true,
				},
			})
			continue
		}
		text := resolution.Text
		if resolution.Fallback {
			text += untranslatedMark
		}
		annotation := EditorAnnotation{
			Start:   int32(element.Start),
			End:     int32(element.End),
			Title:   text,
			Content: element.Value + "\n" + resolution.Source,
			Type:    "placeholder",
			Placeholder: &PlaceholderRef{
				TableIndex: int32(index),
				Key:        key,
				Fallback:   resolution.Fallback,
			},
		}
		// Link to the string table itself（不再按体积设限：任何大小的表都可打开）。
		if sourceIndex, ok := c.archive.Find(resolution.Source); ok {
			annotation.TargetFileIndex = sourceIndex
			annotation.Content += "\n\nCmd/Ctrl+单击打开字符串表；单击标签可修改译文"
		} else {
			annotation.TargetFileIndex = -1
			annotation.Content += "\n\n单击标签可修改译文"
		}
		annotations = append(annotations, annotation)
	}
	return annotations
}

// appendUnindexedListLinksLocked makes the path token in an otherwise
// unconfigured .lst file navigable. These are link-only editor annotations:
// they carry no title/content, so the frontend renders no name tag.
func (c *core) appendUnindexedListLinksLocked(filePath string, view pvf.ScriptView, annotations []EditorAnnotation) []EditorAnnotation {
	if c.archive == nil || c.annotationEngine == nil || !strings.EqualFold(path.Ext(filePath), ".lst") {
		return annotations
	}
	if c.hasListRelationPathLocked(filePath) {
		return annotations
	}

	linked := make(map[string]struct{}, len(annotations))
	for _, annotation := range annotations {
		if annotation.TargetFileIndex >= 0 {
			linked[editorAnnotationRangeKey(annotation.Start, annotation.End)] = struct{}{}
		}
	}
	tokens := make([]pvf.ScriptElement, 0, len(view.Elements))
	for _, element := range view.Elements {
		if element.Kind == pvf.ScriptElementToken {
			tokens = append(tokens, element)
		}
	}
	for offset := 0; offset+1 < len(tokens); offset += 2 {
		pathToken := tokens[offset+1]
		_, targetIndex, ok := findListTargetInArchive(c.archive, filePath, pathToken.Value)
		if !ok {
			continue
		}
		key := editorAnnotationRangeKey(int32(pathToken.Start), int32(pathToken.End))
		if _, exists := linked[key]; exists {
			continue
		}
		annotations = append(annotations, EditorAnnotation{
			Start: int32(pathToken.Start), End: int32(pathToken.End),
			Type: "link", TargetFileIndex: targetIndex,
		})
		linked[key] = struct{}{}
	}
	return annotations
}

func (c *core) hasListRelationPathLocked(filePath string) bool {
	current := normalizeAnnotationPath(filePath)
	for _, relation := range c.annotationEngine.Document().Relations {
		kind := relation.Kind
		if kind == "" {
			kind = "list"
		}
		switch kind {
		case "list":
			if normalizeAnnotationPath(relation.ListPath) == current {
				return true
			}
		case "contextual":
			for _, listPath := range relation.ContextPaths {
				if normalizeAnnotationPath(listPath) == current {
					return true
				}
			}
		}
	}
	return false
}

func normalizeAnnotationPath(value string) string {
	return strings.ToLower(strings.Trim(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"), "/"))
}

func editorAnnotationRangeKey(start, end int32) string {
	return fmt.Sprintf("%d:%d", start, end)
}

// resolveListAnnotationReferenceLocked resolves a concrete row by its path.
// The regular relation cache is intentionally ID-based for field references;
// list rows need path-based resolution so duplicate IDs remain independent.
func (c *core) resolveListAnnotationReferenceLocked(relationName, id, context, listPath, relativePath string) (annotationrules.Reference, bool) {
	if c.archive == nil || c.annotationEngine == nil {
		return annotationrules.Reference{}, false
	}
	_, fileIndex, ok := findListTargetInArchive(c.archive, listPath, relativePath)
	if !ok {
		return annotationrules.Reference{}, false
	}
	relation, ok := c.annotationEngine.Relation(relationName)
	if !ok {
		return annotationrules.Reference{}, false
	}
	reference := annotationrules.Reference{
		ID: id, Path: c.archive.Path(fileIndex), FileIndex: fileIndex,
	}
	reference.Name = c.readRelationTargetNameLocked(fileIndex, listPath, relation.NameSection)
	return reference, true
}

// resetAnnotationCachesLocked 作废「注解派生缓存」：关系映射 + 「清单行 → 目标名」缓存。
//
// 任何内容改动都必须调用它。此前只重置了 annotationRelations，漏掉了 listNameCache：
// 编辑目标文件（改 [name]）后，清单里仍显示旧名称 —— 自检时由
// TestListFileAnnotationsResolveNamesAndTargets 等三个用例暴露出来。
func (c *core) resetAnnotationCachesLocked() {
	c.annotationRelations = make(map[string]map[string]*relationTarget)
	c.listNameCache = nil
}

// listNameCacheKey 组合影响名称结果的三要素；listPath 参与是因为部分清单
// （如 itemshop）在缺 [name] 时会回退到 npc 名取值路径。
func listNameCacheKey(fileIndex int32, listPath, nameSection string) string {
	return fmt.Sprintf("%d|%s|%s", fileIndex, listPath, nameSection)
}

// storeListNameLocked 写入「清单行 → 目标名」缓存；容量到顶时整表清空（名称可重算）。
func (c *core) storeListNameLocked(key, value string) {
	if c.listNameCache == nil {
		c.listNameCache = make(map[string]string)
	}
	if len(c.listNameCache) >= listNameCacheLimit {
		c.listNameCache = make(map[string]string)
	}
	c.listNameCache[key] = value
}

func (c *core) readRelationTargetNameLocked(fileIndex int32, listPath, nameSection string) string {
	key := listNameCacheKey(fileIndex, listPath, nameSection)
	if cached, ok := c.listNameCache[key]; ok {
		return cached
	}
	text, err := c.archive.Text(fileIndex)
	if err != nil {
		return ""
	}
	name := c.readRelationTargetNameFromTextLocked(listPath, nameSection, text)
	c.storeListNameLocked(key, name)
	return name
}

func (c *core) readRelationTargetNameFromTextLocked(listPath, nameSection, text string) string {
	name := c.resolveNameTextLocked(firstSectionValue(text, nameSection))
	if name != "" || !sameSearchPath(listPath, itemShopListPath) {
		return name
	}
	npcID := firstSectionValue(text, "npc")
	if npcID == "" {
		return ""
	}
	for relationName, relation := range c.annotationEngine.Document().Relations {
		kind := relation.Kind
		if kind == "" {
			kind = "list"
		}
		if kind != "list" || !sameSearchPath(relation.ListPath, npcListPath) {
			continue
		}
		reference, ok := c.resolveAnnotationReferenceContextLocked(relationName, npcID, "")
		if ok {
			return reference.Name
		}
	}
	return ""
}

// resolveIndexedReferenceLocked 用搜索索引把 ID 解析成归档文件，作为注解关联的兜底。
// 索引与关系清单同源（同样来自 equipment.lst / stackable.lst 等），但路径解析更宽松，
// 因此能救回"清单切分与真实版式对不上、或 ID 登记在同类别的另一份清单里"的引用。
// 只接受与关系同 category 的记录，避免不同清单之间 ID 撞号。
func (c *core) resolveIndexedReferenceLocked(relationName, id string) (annotationrules.Reference, bool) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" || c.archive == nil {
		return annotationrules.Reference{}, false
	}
	category := strings.ToLower(relationSearchCategory(relationName))
	c.ensureSearchIDIndexLocked()
	recordIndex, ok := c.searchIDIndex[category+"\x00"+strings.ToLower(trimmed)]
	if !ok {
		return annotationrules.Reference{}, false
	}
	hit := c.searchRecords[recordIndex].hit
	if hit.FileIndex < 0 || hit.Path == "" {
		return annotationrules.Reference{}, false
	}
	// 名称优先用索引里已解析好的元数据名（占位符已还原成中文），
	// 缺失时再回落到直接读目标文件的 [name]。
	name := c.resolveNameTextLocked(hit.Name)
	if name == "" {
		name = c.readRelationTargetNameLocked(hit.FileIndex, "", "name")
	}
	return annotationrules.Reference{ID: trimmed, Path: hit.Path, FileIndex: hit.FileIndex, Name: name}, true
}

// ensureSearchIDIndexLocked 惰性构建「类别 + ID → 搜索记录下标」索引。
// 首次构建 O(记录数)，之后复用；索引重建或换归档（indexGen 变化）后自动重建。
func (c *core) ensureSearchIDIndexLocked() {
	if c.searchRecords == nil {
		c.searchIDIndex = nil
		c.searchIDIndexGen = c.indexGen
		return
	}
	if c.searchIDIndex != nil && c.searchIDIndexGen == c.indexGen {
		return
	}
	index := make(map[string]int32, len(c.searchRecords))
	for i := range c.searchRecords {
		lowerID := c.searchRecords[i].lowerID
		if lowerID == "" {
			continue
		}
		key := strings.ToLower(c.searchRecords[i].hit.Category) + "\x00" + lowerID
		if _, duplicate := index[key]; duplicate {
			continue
		}
		index[key] = int32(i)
	}
	c.searchIDIndex = index
	c.searchIDIndexGen = c.indexGen
}

func (c *core) resolveAnnotationReferenceLocked(relationName, id string) (annotationrules.Reference, bool) {
	return c.resolveAnnotationReferenceContextLocked(relationName, id, "")
}

func (c *core) resolveAnnotationReferenceContextLocked(relationName, id, context string) (annotationrules.Reference, bool) {
	if c.annotationRelations == nil {
		c.annotationRelations = make(map[string]map[string]*relationTarget)
	}
	relation, relationOK := c.annotationEngine.Relation(relationName)
	if relationOK && relation.Kind == "union" {
		for _, member := range relation.Relations {
			if reference, ok := c.resolveAnnotationReferenceContextLocked(member, id, context); ok {
				return reference, true
			}
		}
		return annotationrules.Reference{}, false
	}
	cacheKey := relationName
	if relationOK && relation.Kind == "contextual" {
		cacheKey += "\x00" + normalizeAnnotationContext(context)
	}
	targets, ok := c.annotationRelations[cacheKey]
	if !ok {
		if relationOK && relation.Kind == "contextual" {
			targets = c.buildContextualAnnotationRelationLocked(relation, context)
		} else {
			targets = c.buildAnnotationRelationLocked(relationName)
		}
		c.annotationRelations[cacheKey] = targets
	}
	target, ok := targets[id]
	if !ok {
		// 清单切分与真实版式不符、或该 ID 由同一类别的其它清单登记时，退回搜索
		// 索引兜底解析（索引与关系同源，但按 FindList + 候选路径解析，容错更好）。
		return c.resolveIndexedReferenceLocked(relationName, id)
	}
	if !target.nameLoaded {
		target.nameLoaded = true
		target.reference.Name = c.readRelationTargetNameLocked(
			target.reference.FileIndex, target.listPath, target.nameSection,
		)
	}
	return target.reference, true
}

func (c *core) buildAnnotationRelationLocked(name string) map[string]*relationTarget {
	result := make(map[string]*relationTarget)
	if c.annotationEngine == nil || c.archive == nil {
		return result
	}
	relation, ok := c.annotationEngine.Relation(name)
	if !ok {
		return result
	}
	return c.buildAnnotationRelationFromListLocked(relation, relation.ListPath)
}

func (c *core) buildContextualAnnotationRelationLocked(relation annotationrules.RelationSpec, context string) map[string]*relationTarget {
	listPath, ok := annotationContextPath(relation.ContextPaths, context)
	if !ok {
		return map[string]*relationTarget{}
	}
	return c.buildAnnotationRelationFromListLocked(relation, listPath)
}

func (c *core) buildAnnotationRelationFromListLocked(relation annotationrules.RelationSpec, listPath string) map[string]*relationTarget {
	result := make(map[string]*relationTarget)
	if c.archive == nil {
		return result
	}
	// FindList 同时兼容两种客户端版式：90US 的 `equipment/equipment.lst`
	// 与 110US 把清单集中到 `list/` 下的 `list/equipment.lst`。
	listIndex, ok := c.archive.FindList(listPath)
	if !ok {
		return result
	}
	// 走解码缓存：同一份清单可能被多个关系引用，不用每次都重新反编译 27MB。
	text, err := c.cachedDecodedText(listIndex, c.archive)
	if err != nil {
		return result
	}
	view := pvf.ParseScriptView(text)
	tokens := make([]pvf.ScriptElement, 0, len(view.Elements))
	for _, element := range view.Elements {
		if element.Kind == pvf.ScriptElementToken {
			tokens = append(tokens, element)
		}
	}
	for offset := 0; offset+relation.RecordTokens <= len(tokens); offset += relation.RecordTokens {
		id := tokens[offset+relation.IDToken].Value
		if id == "" {
			continue
		}
		_, fileIndex, ok := findListTargetInArchive(c.archive, listPath, tokens[offset+relation.PathToken].Value)
		if !ok {
			continue
		}
		if _, duplicate := result[id]; duplicate {
			continue
		}
		result[id] = &relationTarget{
			reference: annotationrules.Reference{
				ID: id, Path: c.archive.Path(fileIndex), FileIndex: fileIndex,
			},
			nameSection: relation.NameSection,
			listPath:    listPath,
		}
	}
	return result
}

func annotationContextPath(paths map[string]string, context string) (string, bool) {
	context = normalizeAnnotationContext(context)
	for key, value := range paths {
		if normalizeAnnotationContext(key) == context {
			return value, true
		}
	}
	return "", false
}

func normalizeAnnotationContext(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// resolveNameTextLocked 把"名字"里可能存在的字符串表占位符（如 `<13::name_590712499>`）
// 解析成实际文本；不是占位符、或表里查不到时原样返回。
// 关联目标文件的 [name] 常常写成 `{8= `<13::name_<id>>`}`，不解析就会把未翻译的
// 标签原文当成物品名显示出来。
func (c *core) resolveNameTextLocked(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || c.archive == nil {
		return value
	}
	index, key, ok := pvf.ParsePlaceholder(trimmed)
	if !ok {
		// `{8= `<13::name_x>`}` 这类"整块占位符"写法：取最外层的一对尖括号再解析。
		start := strings.Index(trimmed, "<")
		end := strings.LastIndex(trimmed, ">")
		if start < 0 || end <= start {
			return value
		}
		index, key, ok = pvf.ParsePlaceholder(trimmed[start : end+1])
		if !ok {
			return value
		}
	}
	resolution, found := c.archive.ResolveStringTable(index, key)
	if !found || resolution.Text == "" {
		return value
	}
	text := resolution.Text
	if resolution.Fallback {
		text += untranslatedMark
	}
	return text
}

// firstSectionValue returns the first direct value of a top-level section.
// Nested sections are skipped: they describe sub-records, so their values must
// not be mistaken for the file's own name or reference id.
func firstSectionValue(text, section string) string {
	for _, element := range pvf.ParseScriptView(text).Elements {
		if element.Kind != pvf.ScriptElementToken || element.Index != 0 || len(element.SectionPath) != 1 {
			continue
		}
		if strings.EqualFold(element.Section, section) {
			return element.Value
		}
	}
	return ""
}

func cloneTreeAnnotations(values []TreeAnnotation) []TreeAnnotation {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]TreeAnnotation, len(values))
	for i, value := range values {
		cloned[i] = value
		cloned[i].RuleIDs = append([]string(nil), value.RuleIDs...)
	}
	return cloned
}

func cloneEditorAnnotations(values []EditorAnnotation) []EditorAnnotation {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]EditorAnnotation, len(values))
	for i, value := range values {
		cloned[i] = value
		cloned[i].RuleIDs = append([]string(nil), value.RuleIDs...)
		cloned[i].Image = cloneImageReference(value.Image)
	}
	return cloned
}

func imageReferenceFromAnnotation(reference *annotationrules.ImageReference) *ImageReference {
	if reference == nil || strings.TrimSpace(reference.Path) == "" || reference.Index < 0 {
		return nil
	}
	return &ImageReference{Path: reference.Path, Index: reference.Index}
}

func validateAnnotationIndex(a *pvf.Archive, index int32) error {
	if index < 0 || index >= a.FileCount() {
		return fmt.Errorf("文件索引越界: %d", index)
	}
	return nil
}
