package services

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"pvfine/internal/objectview"
	"pvfine/internal/pvf"
)

// ObjectTypeInfo 是一个可用对象类型的对外描述。
type ObjectTypeInfo struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	ListPaths   []string `json:"listPaths"`
	Extensions  []string `json:"extensions,omitempty"`
	StringTable *int     `json:"stringTable,omitempty"`
	Notes       string   `json:"notes,omitempty"`
}

// ObjectTypeListResult 是对象类型目录及其来源。
type ObjectTypeListResult struct {
	RulePath        string            `json:"rulePath"`
	ObjectTypeCount int               `json:"objectTypeCount"`
	Types           []*ObjectTypeInfo `json:"types"`
}

// ObjectViewFile 是对象关联的一个归档文件。
//
// Role 取值：
//   - script          对象脚本（登记表条目指向的文件）
//   - list            登记表
//   - indexHash       登记表的伴随索引（含 `_v5` 等变体）
//   - stringTable     显示文本所在的 .str
//   - scriptCandidate 按 ID 在归档内扫到的同名脚本（登记表里没有这个 ID 时给出）
type ObjectViewFile struct {
	// Role 取值：script（对象脚本）/ list（登记表）/ indexHash（登记表的伴随索引）。
	Role      string `json:"role"`
	Path      string `json:"path"`
	FileIndex int32  `json:"fileIndex"` // -1 表示归档内不存在
	Exists    bool   `json:"exists"`
}

// ObjectViewText 是对象的一条显示文本。
type ObjectViewText struct {
	TableIndex int    `json:"tableIndex"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Source     string `json:"source,omitempty"`
	Found      bool   `json:"found"`
	Fallback   bool   `json:"fallback"`
	// Origin 取值：script（脚本内的 `<表号::键名>` 占位符）/ pattern（规则里的键名模式）。
	Origin string `json:"origin"`
}

// ObjectViewRegistration 是对象在某个 .lst 中的登记项。
type ObjectViewRegistration struct {
	ListPath      string `json:"listPath"`
	ListFileIndex int32  `json:"listFileIndex"`
	Category      string `json:"category"`
	ID            string `json:"id"`
	EntryPath     string `json:"entryPath"`
}

// ObjectView 是一个对象的聚合视图（只读）。
type ObjectView struct {
	ObjectType    string                    `json:"objectType"`
	ObjectLabel   string                    `json:"objectLabel"`
	ObjectID      string                    `json:"objectId"`
	Name          string                    `json:"name,omitempty"`
	Files         []*ObjectViewFile         `json:"files"`
	Texts         []*ObjectViewText         `json:"texts"`
	Registrations []*ObjectViewRegistration `json:"registrations"`
	Warnings      []string                  `json:"warnings"`
}

// ObjectViewService 以"游戏对象"为单位聚合归档内容：脚本、登记表、伴随索引、
// 显示文本与告警。
//
// 规则来自外部数据文件（config/objectview.json），本服务不硬编码任何对象类型、
// 文件路径或字符串表号。整个聚合过程**只读**：不产生任何归档写入。
type ObjectViewService struct {
	c *core

	mu     sync.RWMutex
	rules  objectview.Catalog
	path   string
	err    error
	loaded bool
}

func NewObjectViewService(c *core) *ObjectViewService {
	service := &ObjectViewService{c: c}
	if path, ok := objectview.FindSourcePath(); ok {
		service.path = path
	} else if runtimePath, err := objectview.RuntimePath(); err == nil {
		service.path = runtimePath
	}
	return service
}

// ListObjectTypes 返回数据文件里定义的全部对象类型。
func (s *ObjectViewService) ListObjectTypes() (*ObjectTypeListResult, error) {
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	types := make([]*ObjectTypeInfo, 0, len(catalog.ObjectTypes))
	for _, objectType := range catalog.Sorted() {
		info := &ObjectTypeInfo{
			ID:         objectType.ID,
			Label:      objectType.DisplayLabel(),
			ListPaths:  objectType.AllListPaths(),
			Extensions: append([]string(nil), objectType.Extensions...),
			Notes:      objectType.Notes,
		}
		if objectType.StringTable != nil {
			tableIndex := *objectType.StringTable
			info.StringTable = &tableIndex
		}
		types = append(types, info)
	}
	return &ObjectTypeListResult{
		RulePath:        s.rulePath(),
		ObjectTypeCount: len(types),
		Types:           types,
	}, nil
}

// ReloadRules 重新读取数据文件；校验失败时保留原有规则不变。
//
// 与首次加载走同一条解析路径（loadRules）：数据文件缺失时回退内置副本，
// 而不是直接抛"文件不存在"。
func (s *ObjectViewService) ReloadRules() (*ObjectTypeListResult, error) {
	catalog, err := s.loadRules()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.rules, s.err, s.loaded = catalog, nil, true
	s.mu.Unlock()
	result, err := s.ListObjectTypes()
	if err != nil {
		return nil, err
	}
	emitEvent("objectview:reloaded", result)
	return result, nil
}

// ResolveObject 把一个对象 ID 聚合为可审阅的视图。
func (s *ObjectViewService) ResolveObject(objectType, objectID string) (*ObjectView, error) {
	objectType = strings.TrimSpace(objectType)
	objectID = strings.TrimSpace(objectID)
	if objectID == "" {
		return nil, fmt.Errorf("对象 ID 不能为空")
	}
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	rule, ok := catalog.Lookup(objectType)
	if !ok {
		return nil, fmt.Errorf("未知对象类型: %s", objectType)
	}
	// 先取可搜索清单规格：该方法内部会加读锁，不能与下面的读锁嵌套。
	specs := s.c.searchableListSpecs()

	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	a := s.c.archive
	if a == nil {
		return nil, ErrNoArchive
	}

	view := &ObjectView{
		ObjectType:    rule.ID,
		ObjectLabel:   rule.DisplayLabel(),
		ObjectID:      objectID,
		Files:         []*ObjectViewFile{},
		Texts:         []*ObjectViewText{},
		Registrations: []*ObjectViewRegistration{},
		Warnings:      []string{},
	}
	warn := func(format string, args ...any) {
		view.Warnings = append(view.Warnings, fmt.Sprintf(format, args...))
	}

	// 1) 从候选登记表里解析对象脚本。解码失败的登记表会变成告警而不是被
	//    静默跳过——否则用户只看到"未登记"，无法分辨"真的没登记"与"表损坏"。
	entryPath, listPath, listIndex := lookupListEntry(a, rule, objectID, warn)
	scriptIndex := int32(-1)
	if entryPath != "" {
		if index, found := resolveEntryIndex(a, listPath, entryPath); found {
			scriptIndex = index
		} else {
			warn("登记表条目指向的文件在归档内不存在: %s", entryPath)
		}
	}

	// 2) 关联文件：脚本 / 登记表 / 登记表的伴随索引。
	if entryPath != "" {
		path := entryPath
		if scriptIndex >= 0 {
			path = a.Path(scriptIndex)
		}
		view.Files = append(view.Files, &ObjectViewFile{
			Role: "script", Path: path, FileIndex: scriptIndex, Exists: scriptIndex >= 0,
		})
		if !matchesAnyExtension(path, rule.Extensions) {
			warn("脚本扩展名与对象类型声明不符（期望 %s）: %s", strings.Join(rule.Extensions, "/"), path)
		}
	} else {
		warn("对象未在任何候选登记表中登记（类型 %s / ID %s）", rule.DisplayLabel(), objectID)
		// 未命中时仍给出第一张实际存在的候选登记表：界面才能回答"该登记到哪"。
		if candidatePath, candidateIndex, found := firstExistingList(a, rule); found {
			listPath, listIndex = candidatePath, candidateIndex
			warn("可登记的目标登记表: %s", candidatePath)
		}
	}
	if listPath != "" {
		view.Files = append(view.Files, &ObjectViewFile{
			Role: "list", Path: listPath, FileIndex: listIndex, Exists: true,
		})
		appendIndexHashFiles(a, listPath, view, warn)
	}

	// 3) 显示文本：优先扫描脚本文本里的 `<表号::键名>` 占位符，
	//    再用规则里的键名模式兜底。
	view.Texts = collectObjectTexts(a, rule, objectID, scriptIndex, warn)

	// 3b) 显示文本所在的字符串表文件：解析用的是哪张 .str 直接列出来，
	//     省得为了改一句话再去翻表号映射。
	appendStringTableFiles(a, rule, view)

	// 3c) 登记表里没有这个 ID 时，按 ID 直接在归档里找同名脚本：
	//     否则界面只能回答"未登记"，无法回答"这个 ID 的文件到底在不在归档里"。
	if scriptIndex < 0 {
		appendSameIDScripts(a, rule, objectID, view, warn)
	}

	// 4) 该文件还被哪些登记表引用（复用搜索索引的关系定义）。
	if scriptIndex >= 0 {
		for _, registration := range findFileRegistrationsLocked(a, specs, []int32{scriptIndex}) {
			view.Registrations = append(view.Registrations, &ObjectViewRegistration{
				ListPath:      registration.ListPath,
				ListFileIndex: registration.ListFileIndex,
				Category:      registration.Category,
				ID:            registration.ID,
				EntryPath:     registration.EntryPath,
			})
		}
		if len(view.Registrations) == 0 {
			warn("脚本文件未在任何登记表中被引用: %s", a.Path(scriptIndex))
		}
	}

	// 5) 名称取第一条以 name 开头的已解析文本。
	for _, text := range view.Texts {
		if text.Found && strings.HasPrefix(strings.ToLower(text.Key), "name") {
			view.Name = text.Value
			break
		}
	}
	return view, nil
}

func (s *ObjectViewService) rulePath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.path == "" {
		return "(内置)"
	}
	return s.path
}

func (s *ObjectViewService) catalog() (objectview.Catalog, error) {
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
func (s *ObjectViewService) loadRules() (objectview.Catalog, error) {
	if s.path != "" {
		if _, statErr := os.Stat(s.path); statErr == nil {
			return objectview.LoadFile(s.path)
		}
	}
	return objectview.LoadDefault()
}

// lookupListEntry 在类型的候选登记表里找 ID 对应的条目路径。
// 登记表解码失败会以告警形式上报，而不是静默跳过（否则调用方无法分辨
// "确实没登记"与"表解析失败"）。
func lookupListEntry(a *pvf.Archive, rule objectview.ObjectType, objectID string, warn func(string, ...any)) (entryPath, listPath string, listIndex int32) {
	for _, candidate := range rule.AllListPaths() {
		index, ok := a.FindList(candidate)
		if !ok {
			continue
		}
		pairs, err := a.ListPairs(index)
		if err != nil {
			warn("登记表解析失败，已跳过: %s（%s）", a.Path(index), err.Error())
			continue
		}
		for _, pair := range pairs {
			if strings.TrimSpace(pair.ID) == objectID {
				return strings.TrimSpace(pair.Path), a.Path(index), index
			}
		}
	}
	return "", "", -1
}

// firstExistingList 返回该类型第一张实际存在的候选登记表，用于"未登记"时
// 告诉用户"该登记到哪个文件"。
func firstExistingList(a *pvf.Archive, rule objectview.ObjectType) (listPath string, listIndex int32, found bool) {
	for _, candidate := range rule.AllListPaths() {
		if index, ok := a.FindList(candidate); ok {
			return a.Path(index), index, true
		}
	}
	return "", -1, false
}

// resolveEntryIndex 把登记表条目解释为归档文件索引。
//
// 90US 的条目是列表目录相对（`character/a.equ` 在 `equipment/equipment.lst` 里），
// 110US 集中到 `list/` 下且条目改为归档根相对（`equipment/character/a.equ`），
// 两种都试（见 docs/FORMAT.md §9.2）。
func resolveEntryIndex(a *pvf.Archive, listPath, entryPath string) (int32, bool) {
	entryPath = strings.Trim(strings.ReplaceAll(strings.TrimSpace(entryPath), "\\", "/"), "/")
	if entryPath == "" {
		return 0, false
	}
	candidates := []string{entryPath}
	if dir := listDirPrefix(listPath); dir != "" {
		candidates = append(candidates, dir+"/"+entryPath)
	}
	for _, candidate := range candidates {
		if index, ok := a.Find(candidate); ok {
			return index, true
		}
	}
	return 0, false
}

// listDirPrefix 返回 90US 布局下条目需要补的目录前缀。
// 110US 把所有登记表集中到 `list/` 下且条目是归档根相对，因此不加前缀。
func listDirPrefix(listPath string) string {
	trimmed := strings.Trim(strings.ReplaceAll(strings.TrimSpace(listPath), "\\", "/"), "/")
	slash := strings.LastIndexByte(trimmed, '/')
	if slash <= 0 {
		return ""
	}
	dir := trimmed[:slash]
	if strings.EqualFold(dir, "list") {
		return ""
	}
	return dir
}

func matchesAnyExtension(path string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	lower := strings.ToLower(path)
	for _, extension := range extensions {
		if strings.HasSuffix(lower, strings.ToLower(extension)) {
			return true
		}
	}
	return false
}

// placeholderRef 是脚本里出现的一条 `<表号::键名>` 引用。
type placeholderRef struct {
	tableIndex int
	key        string
}

// scanPlaceholders 收集文本里出现的全部 `<表号::键名>` 占位符。
func scanPlaceholders(text string) []placeholderRef {
	refs := make([]placeholderRef, 0, 8)
	for i := 0; i < len(text); i++ {
		if text[i] != '<' {
			continue
		}
		end := strings.IndexByte(text[i:], '>')
		if end < 0 {
			break
		}
		if tableIndex, key, ok := pvf.ParsePlaceholder(text[i : i+end+1]); ok {
			refs = append(refs, placeholderRef{tableIndex: tableIndex, key: key})
		}
		i += end
	}
	return refs
}

// sameIDScriptLimit 是「同名脚本候选」的展示上限：命中很多同名文件时只列前若干个，
// 其余用告警计数提示，避免把面板刷满。
const sameIDScriptLimit = 12

// appendIndexHashFiles 把登记表的伴随索引加入关联文件。
// 部分系列除 `_indexhash.etc` 外还有 `_v5.etc` 变体，一并列出。
func appendIndexHashFiles(a *pvf.Archive, listPath string, view *ObjectView, warn func(string, ...any)) {
	companion, ok := pvf.IndexHashCompanionPath(listPath)
	if !ok {
		return
	}
	// 归档里一份都没有时也要列出主索引（exists=false），界面才能回答"缺哪个文件"。
	siblings := a.IndexHashSiblingPaths(listPath)
	if len(siblings) == 0 {
		siblings = []string{companion}
	}
	for _, siblingPath := range siblings {
		siblingIndex := int32(-1)
		if index, found := a.Find(siblingPath); found {
			siblingIndex = index
		}
		view.Files = append(view.Files, &ObjectViewFile{
			Role: "indexHash", Path: siblingPath, FileIndex: siblingIndex, Exists: siblingIndex >= 0,
		})
	}
	if _, found := a.Find(companion); !found {
		warn("登记表的伴随索引不存在: %s（110US 归档才需要）", companion)
	}
}

// appendStringTableFiles 把显示文本所在的 .str 加入关联文件（同一张表只列一次）。
//
// 两个来源：① 文本已解析出来时用它回报的真实路径；② 该类型声明了 stringTable
// 但条目还没命中时，用 `n_string.lst` 把表号翻成路径 —— 这样"文字缺条目"的场景
// 也能直接跳到该改的那张表，而不用再自己查表号映射。
func appendStringTableFiles(a *pvf.Archive, rule objectview.ObjectType, view *ObjectView) {
	seen := make(map[string]struct{}, len(view.Texts)+1)
	add := func(source string) {
		source = strings.TrimSpace(source)
		if source == "" {
			return
		}
		if _, dup := seen[source]; dup {
			return
		}
		seen[source] = struct{}{}
		fileIndex := int32(-1)
		if index, found := a.Find(source); found {
			fileIndex = index
		}
		view.Files = append(view.Files, &ObjectViewFile{
			Role: "stringTable", Path: source, FileIndex: fileIndex, Exists: fileIndex >= 0,
		})
	}
	for _, text := range view.Texts {
		add(text.Source)
	}
	if rule.StringTable != nil {
		add(stringTableFileForIndex(a, *rule.StringTable))
	}
}

// stringTableFileForIndex 用公开接口把字符串表号翻成 .str 路径。
//
// 表号→路径映射在内核里是私有实现且内核属冻结层，这里只做一次只读查询：
// 读 `list/n_string.lst`（90US 回退 `n_string.lst`）的 `表号 → 路径` 条目。
func stringTableFileForIndex(a *pvf.Archive, tableIndex int) string {
	target := strconv.Itoa(tableIndex)
	for _, name := range []string{"list/n_string.lst", "n_string.lst"} {
		index, ok := a.FindList(name)
		if !ok {
			index, ok = a.Find(name)
		}
		if !ok {
			continue
		}
		pairs, err := a.ListPairs(index)
		if err != nil {
			continue
		}
		for _, pair := range pairs {
			if strings.TrimSpace(pair.ID) != target {
				continue
			}
			path := strings.Trim(strings.ReplaceAll(strings.TrimSpace(pair.Path), "\\", "/"), "/")
			if path != "" {
				return path
			}
		}
	}
	return ""
}

// appendSameIDScripts 扫描归档，把文件主名等于对象 ID 的脚本列为候选关联文件。
//
// 只在"登记表里查不到这个 ID"时调用：此时界面原本只能回答"未登记"，
// 无法回答"这个 ID 的文件到底在不在归档里"。
// 扫描只作用于条目的文件名，先用长度做一次零分配的前置过滤，再逐条精确比对。
func appendSameIDScripts(a *pvf.Archive, rule objectview.ObjectType, objectID string, view *ObjectView, warn func(string, ...any)) {
	objectID = strings.TrimSpace(objectID)
	if objectID == "" {
		return
	}
	extensions := rule.Extensions
	matched, overflow := 0, 0
	total := a.FileCount()
	for i := int32(0); i < total; i++ {
		file := a.File(i)
		if len(extensions) > 0 && !hasExpectedLength(file.Name, objectID, extensions) {
			continue
		}
		if !matchesAnyExtension(file.Name, extensions) {
			continue
		}
		if !strings.EqualFold(fileBaseWithoutExtension(file.Name), objectID) {
			continue
		}
		matched++
		if matched > sameIDScriptLimit {
			overflow++
			continue
		}
		path := file.Name
		if file.Path != "" {
			path = file.Path + "/" + file.Name
		}
		view.Files = append(view.Files, &ObjectViewFile{
			Role: "scriptCandidate", Path: path, FileIndex: i, Exists: true,
		})
	}
	switch {
	case matched == 0:
		warn("归档内没有以 %s 命名的脚本文件（扩展名 %s）", objectID, strings.Join(extensions, "/"))
	case overflow > 0:
		warn("按 ID 找到 %d 个同名脚本文件，只列出前 %d 个", matched, sameIDScriptLimit)
	default:
		warn("登记表里没有该 ID，但归档内找到 %d 个同名脚本文件（见关联文件）", matched)
	}
}

// hasExpectedLength 判断文件名长度是否可能是「对象 ID + 某个扩展名」，
// 用于在扫描时用一次整数比较挡掉绝大多数条目（不产生任何分配）。
func hasExpectedLength(name, objectID string, extensions []string) bool {
	for _, extension := range extensions {
		if len(name) == len(objectID)+len(extension) {
			return true
		}
	}
	return false
}

// fileBaseWithoutExtension 返回路径或文件名的主名（去目录、去最后一个扩展名）。
func fileBaseWithoutExtension(path string) string {
	base := path
	if slash := strings.LastIndexAny(base, "/\\"); slash >= 0 {
		base = base[slash+1:]
	}
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		base = base[:dot]
	}
	return base
}

// collectObjectTexts 汇总对象的显示文本：脚本占位符优先，规则键名模式兜底。
func collectObjectTexts(a *pvf.Archive, rule objectview.ObjectType, objectID string, scriptIndex int32, warn func(string, ...any)) []*ObjectViewText {
	texts := make([]*ObjectViewText, 0, 4)
	seen := make(map[string]struct{})
	add := func(tableIndex int, key, origin string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		tag := fmt.Sprintf("%d\x00%s", tableIndex, key)
		if _, exists := seen[tag]; exists {
			return
		}
		seen[tag] = struct{}{}
		entry := &ObjectViewText{TableIndex: tableIndex, Key: key, Origin: origin}
		if resolution, ok := a.ResolveStringTable(tableIndex, key); ok {
			entry.Value = resolution.Text
			entry.Source = resolution.Source
			entry.Fallback = resolution.Fallback
			entry.Found = true
		}
		texts = append(texts, entry)
	}

	if scriptIndex >= 0 {
		text, err := a.Text(scriptIndex)
		if err != nil {
			warn("读取脚本失败: %s", err.Error())
		} else {
			refs := scanPlaceholders(text)
			if len(refs) == 0 {
				warn("脚本内未发现 <表号::键名> 占位符（可能是 90US 内联文本，或该对象尚未引用显示文本）")
			}
			for _, ref := range refs {
				add(ref.tableIndex, ref.key, "script")
			}
		}
	}
	if rule.StringTable != nil {
		for _, key := range rule.KeyPatternsFor(objectID) {
			add(*rule.StringTable, key, "pattern")
		}
	}
	return texts
}
