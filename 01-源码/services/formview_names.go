package services

import (
	"strings"
	"sync"

	"pvfine/internal/objectview"
	"pvfine/internal/pvf"
)

// refNameResolver 把结构化视图里 ref 列的对象 ID 翻成中文名（只读）。
//
// **完全复用对象视图那一套**，不另写解析：
//   objectview 规则给出「对象类型 → 候选登记表」→ 登记表把 ID 变成脚本路径
//   → resolveEntryIndex 变成归档文件索引 → core.readRelationTargetNameLocked 读该文件的
//   [name] 段并解析成中文（自带缓存）。
//
// 性能要点（2026-10-03 实测踩到的坑）：登记表很大（怪物 / 装备 / 道具各上万条），
// 而独立掉落一个文件就有 1834 行 × 2 个 ref 列。**必须"一次建 ID→路径 表、多次查"**，
// 否则每个 ID 都线性扫一遍登记表就是 O(行数 × 登记表条数)。
// 因此实例挂在服务上长期持有（缓存跨调用复用），而不是每次投影新建。
type refNameResolver struct {
	c       *core
	catalog objectview.Catalog
	// archive 是本实例缓存对应的归档；归档换了就整体重建。
	archive *pvf.Archive

	mu         sync.Mutex
	cache      map[string]string
	entryIndex map[string]map[string]listEntry
}

// listEntry 是登记表里的一条：ID → 脚本路径。
type listEntry struct {
	path     string
	listPath string
}

const (
	// refNameCacheLimit 与注解侧同量级：超额整体清空，避免无界增长。
	refNameCacheLimit = 20000
	// refEntryIndexLimit 是"每个登记表一份 ID→路径 映射"的份数上限。
	refEntryIndexLimit = 64
	// nameSectionName 是名称所在的段名（与 config/lists.json 的 nameSection 一致）。
	nameSectionName = "name"
	// maxRefNameResolutions 是**单次投影**里名称解析的次数上限。
	// 超了就停止解析并告警（宁可只显示编号，也不让界面卡住）。
	maxRefNameResolutions = 5000
)

func newRefNameResolver(c *core, a *pvf.Archive) *refNameResolver {
	catalog, err := objectview.LoadDefault()
	if err != nil {
		// 规则坏了不该拖垮投影：退化成"没有名称"。
		catalog = objectview.Catalog{}
	}
	return &refNameResolver{
		c:          c,
		catalog:    catalog,
		archive:    a,
		cache:      make(map[string]string),
		entryIndex: make(map[string]map[string]listEntry),
	}
}

// name 解析「列声明的对象类型 + ID」对应的名称；ref 支持 `a|b` 多候选。
// 必须在持有 core 读锁时调用（内部只读归档）。
func (r *refNameResolver) name(ref, objectID string) string {
	if r == nil {
		return ""
	}
	objectID = strings.TrimSpace(objectID)
	if objectID == "" {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(ref)) + "|" + objectID

	r.mu.Lock()
	cached, ok := r.cache[key]
	r.mu.Unlock()
	if ok {
		return cached
	}

	resolved := r.resolve(ref, objectID)

	r.mu.Lock()
	if len(r.cache) >= refNameCacheLimit {
		r.cache = make(map[string]string)
	}
	r.cache[key] = resolved
	r.mu.Unlock()
	return resolved
}

// resolve 逐个候选类型尝试解析，取第一个成功的。
func (r *refNameResolver) resolve(ref, objectID string) string {
	for _, candidate := range strings.Split(ref, "|") {
		rule, ok := r.catalog.Lookup(strings.TrimSpace(candidate))
		if !ok {
			continue
		}
		if name := r.resolveType(rule, objectID); name != "" {
			return name
		}
	}
	return ""
}

func (r *refNameResolver) resolveType(rule objectview.ObjectType, objectID string) string {
	if r.c == nil || r.archive == nil {
		return ""
	}
	entry, ok := r.entriesFor(rule)[objectID]
	if !ok {
		return ""
	}
	fileIndex, ok := resolveEntryIndex(r.archive, entry.listPath, entry.path)
	if !ok {
		return ""
	}
	return r.c.readRelationTargetNameLocked(fileIndex, entry.listPath, nameSectionName)
}

// entriesFor 建/取某类型的「ID → (脚本路径, 登记表)」映射。
// 一个类型的全部候选登记表只扫一次，结果跨调用复用。
func (r *refNameResolver) entriesFor(rule objectview.ObjectType) map[string]listEntry {
	key := strings.Join(rule.AllListPaths(), "|")
	r.mu.Lock()
	if cached, ok := r.entryIndex[key]; ok {
		r.mu.Unlock()
		return cached
	}
	r.mu.Unlock()

	index := make(map[string]listEntry)
	if a := r.archive; a != nil {
		for _, candidate := range rule.AllListPaths() {
			listIndex, ok := a.FindList(candidate)
			if !ok {
				continue
			}
			pairs, err := a.ListPairs(listIndex)
			if err != nil {
				continue
			}
			listPath := a.Path(listIndex)
			for _, pair := range pairs {
				id := strings.TrimSpace(pair.ID)
				if id == "" {
					continue
				}
				if _, exists := index[id]; exists {
					continue
				}
				index[id] = listEntry{path: strings.TrimSpace(pair.Path), listPath: listPath}
			}
		}
	}

	r.mu.Lock()
	if len(r.entryIndex) >= refEntryIndexLimit {
		r.entryIndex = make(map[string]map[string]listEntry)
	}
	r.entryIndex[key] = index
	r.mu.Unlock()
	return index
}
