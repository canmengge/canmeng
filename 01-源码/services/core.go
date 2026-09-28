// Package services hosts the wails3-exposed application services. All
// services share one core that owns the loaded pvf archive and its derived
// indexes (directory tree, sorted path list for search).
package services

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	annotationrules "pvfine/internal/annotations"
	"pvfine/internal/pvf"
	renderingrules "pvfine/internal/rendering"
	pvfversion "pvfine/internal/version"
)

// emitEvent 安全地发事件:脱离 wails 运行时(如单元测试)时为 no-op。
func emitEvent(name string, data ...any) {
	if app := application.Get(); app != nil {
		app.Event.Emit(name, data...)
	}
}

var (
	ErrNoArchive      = errors.New("尚未打开归档文件")
	ErrSearchIndexing = errors.New("搜索索引正在构建")
	ErrBatchPlanStale = errors.New("批处理预览已过期,请重新预览")
)

const (
	ChangeKindAdded    = "added"
	ChangeKindModified = "modified"
)

// TreeNode is one entry in the explorer tree: either a directory or a file.
type TreeNode struct {
	Name        string           `json:"name"`
	Path        string           `json:"path"`
	IsDir       bool             `json:"isDir"`
	Size        int32            `json:"size"`
	DataType    int32            `json:"dataType"`
	ChildCount  int32            `json:"childCount"`
	FileIndex   int32            `json:"fileIndex"` // -1 for directories
	ChangeKind  string           `json:"changeKind,omitempty"`
	Tags        []TreeTag        `json:"tags,omitempty"`
	Annotations []TreeAnnotation `json:"annotations,omitempty"`
	Icon        *ImageReference  `json:"icon,omitempty"`
	FieldImage  *ImageReference  `json:"fieldImage,omitempty"`
}

// pathEntry feeds the search scanner.
type pathEntry struct {
	path       string
	lower      string
	idx        int32
	size       int32
	typ        int32
	changeKind string
}

// core owns the loaded archive plus derived indexes. Guarded by mu; all
// services take it per call.
type core struct {
	mu                  sync.RWMutex
	archive             *pvf.Archive
	annotationEngine    *annotationrules.Engine
	annotationErr       error
	// annotationExternal 记录最近一次外置注释加载的统计（供界面显示来源与规模）。
	annotationExternal annotationrules.ExternalSummary
	renderingEngine     *renderingrules.Engine
	renderingErr        error
	annotationRelations map[string]map[string]*relationTarget
	editorText          map[int32]string
	editorAnnotation    editorAnnotationCache
	// pathAnnotations 是「路径 → 目录标注」的**按需**缓存，由 pathAnnotationsMu 保护
	// （与 mu 分开：访问点多在持有 mu.RLock 时发生，无法就地写入；加锁顺序恒为
	// mu → pathAnnotationsMu，不得反向）。
	//
	// 2026-09-24 性能修复（P-2006）：此前在打开归档时对**全部**节点预计算
	// （installArchiveIndexesLockedWithSearch 调 buildPathAnnotations）。在 1.0 的
	// 标注库下（57 规则 / 5,651 字段）engine.AnnotatePath 线性遍历规则集合，单节点
	// 实测约 23.9 µs（原版 37 字段约 0.2 µs），对全部 467.8 万节点预计算需约 112 秒
	// —— GUI 日志实测「构建目录索引」阶段 162.9 秒。改为首次访问时计算并缓存
	// （F3：打开后按需），打开归档恢复为秒级。
	//
	// 注意：treeAnnotationsFor 返回的是缓存内部切片，调用方必须经 cloneTreeAnnotations。
	pathAnnotations   map[string][]TreeAnnotation
	pathAnnotationsMu sync.RWMutex

	// fileNameCache 是「fileIndex → 显示名」的惰性缓存（.lst 编辑器行内名称注释用）。
	// equipment.lst 有 36 万行，打开时**不能**全量解析 ScriptMetadata；这里按调用方
	// （前端可见行）按需查询并复用结果。独立锁：解析是重操作，不能占用主 mu。
	fileNameCache   map[int32]string
	fileNameCacheMu sync.RWMutex

	// 用户自定义路径注释（右键「编辑注释」写入，存 %AppConfig%\pvfine\path-annotations.json）。
	// 命中时**整体替换**内置规则结果（用户优先）；见 pathannotations.go。
	annotationOverrides   map[string]PathAnnotationOverride
	annotationOverridesMu sync.RWMutex

	dirChildren map[string][]*TreeNode // dirPath -> ordered children ("" = root)
	directories         []string
	sortedPaths         []pathEntry
	searchRecords       []searchRecord
	searchByFile        map[int32][]int
	// searchMetadata is the canonical semantic snapshot used to rebuild the
	// derived search records after a path/list delta. It intentionally keeps
	// only list-backed records; ordinary file records are derived from
	// sortedPaths and are therefore not duplicated here.
	searchMetadata          []indexedMetadata
	searchSpecFingerprint   string
	treeTagsByFile          map[int32][]TreeTag
	visualsByFile           map[int32]fileVisuals
	indexStatus             IndexStatus
	indexStartedAt          time.Time
	indexCancel             context.CancelFunc
	indexDirty              map[int32]struct{}
	indexGen                uint64
	searchIndexDeltaPending bool
	searchIndexListPending  map[int32]struct{}
	// searchIndexCachePath is only set by tests. Production cache files are
	// resolved from os.UserCacheDir by search_index_cache.go.
	searchIndexCachePath string
	batchRevision        uint64
	batchPlan            *batchPlan
	scriptPlan           *scriptPlan
	scriptCancel         context.CancelFunc
	versionRepo          *pvfversion.Repository
	versionHead          pvfversion.Commit
	versionHeadSnapshot  pvfversion.Snapshot
	versionWorking       pvfversion.Snapshot
	versionChanges       []pvfversion.FileChange
	versionChangeMap     map[string]pvfversion.FileChange
	versionUndo          []versionUndoRecord
	versionSavedSnapshot pvfversion.Snapshot
	versionArtifactDirty map[string]struct{}
	versionSavedTree     string
	versionSavedPVF      string
	versionSavedCommit   string
	versionViewCommit    string
	versionBaseArchive   *pvf.Archive
	versionLoadID        uint64
	versionLoading       bool
	versionLoadError     string
	advancedIndex        *pvf.StringPoolIndex
	advancedStatus       AdvancedSearchIndexStatus
	advancedCancel       context.CancelFunc
	advancedDirty        map[int32]struct{}
	advancedGen          uint64
	advancedQueryCache   map[advancedQueryKey]*advancedHitView
	advancedQueryOrder   []advancedQueryKey
	binaryCache          map[string]map[int32]advancedFileMatch
	unpackCancel         atomic.Bool
	unpackRunning        atomic.Bool
}

type editorAnnotationCache struct {
	valid       bool
	fileIndex   int32
	text        string
	annotations []EditorAnnotation
}

func newCore() *core { return makeCore() }

// NewCore creates the shared service state (one per application).
func NewCore() *core { return makeCore() }

func makeCore() *core {
	annotationEngine, annotationExternal, annotationErr := annotationrules.LoadPreferredWithSummary()
	renderingEngine, renderingErr := renderingrules.LoadDefault()
	return &core{
		annotationEngine:   annotationEngine,
		annotationExternal: annotationExternal,
		annotationErr:      annotationErr,
		renderingEngine:  renderingEngine,
		renderingErr:     renderingErr,
		visualsByFile:    make(map[int32]fileVisuals),
	}
}

func archiveChangeKind(a *pvf.Archive, index int32) string {
	if a == nil || index < 0 || index >= a.FileCount() || !a.IsModified(index) {
		return ""
	}
	if a.File(index).ChunkIndex < 0 {
		return ChangeKindAdded
	}
	return ChangeKindModified
}

// setArchive loads an archive and builds derived indexes. Index building
// walks every path once (~1M entries, well under a second in Go).
func (c *core) setArchive(a *pvf.Archive) error {
	if c.annotationErr != nil {
		return c.annotationErr
	}
	if c.renderingErr != nil {
		return c.renderingErr
	}
	children, paths, err := buildIndex(a)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.detachVersionLocked()
	c.installArchiveIndexesLocked(a, children, paths)
	c.mu.Unlock()
	return nil
}

// resetPathAnnotationsLocked 清空「路径 → 目录标注」按需缓存。
// 换归档 / 关归档 / 重载标注规则时调用；调用方须已持有 c.mu 的写锁。
func (c *core) resetPathAnnotationsLocked() {
	c.pathAnnotationsMu.Lock()
	c.pathAnnotations = make(map[string][]TreeAnnotation)
	c.pathAnnotationsMu.Unlock()
}

// resetFileNameCacheLocked 清空「fileIndex → 显示名」缓存（换归档 / 关归档时）。
// 调用方须已持有 c.mu 的写锁。
func (c *core) resetFileNameCacheLocked() {
	c.fileNameCacheMu.Lock()
	c.fileNameCache = make(map[int32]string)
	c.fileNameCacheMu.Unlock()
}

// fileNameFor 返回 fileIndex 对应文件的显示名（ScriptMetadata.Name，已解析占位符）。
// 结果写入 fileNameCache 复用；解析失败返回空串（调用方显示为空即可）。
// 不持有 c.mu：调用方负责保证 archive 有效且未被替换。
func (c *core) fileNameFor(a *pvf.Archive, fileIndex int32) string {
	if a == nil || fileIndex < 0 {
		return ""
	}
	c.fileNameCacheMu.RLock()
	if c.fileNameCache != nil {
		if name, ok := c.fileNameCache[fileIndex]; ok {
			c.fileNameCacheMu.RUnlock()
			return name
		}
	}
	c.fileNameCacheMu.RUnlock()

	metadata, err := a.ScriptMetadata(fileIndex)
	name := ""
	if err == nil {
		name = metadata.Name
	}
	c.fileNameCacheMu.Lock()
	if c.fileNameCache == nil {
		c.fileNameCache = make(map[int32]string)
	}
	c.fileNameCache[fileIndex] = name
	c.fileNameCacheMu.Unlock()
	return name
}

// treeAnnotationsFor 返回单个路径的目录标注：首次访问时计算并写入缓存。
//
// 调用方须已持有 c.mu 的读锁或写锁（本方法只读 c.annotationEngine；自身只用
// pathAnnotationsMu —— 加锁顺序恒为 mu → pathAnnotationsMu，不构成环）。
// 返回值是缓存内部切片，**调用方必须经 cloneTreeAnnotations** 再交给外部。
func (c *core) treeAnnotationsFor(path string, isDir bool) []TreeAnnotation {
	c.pathAnnotationsMu.RLock()
	cached, ok := c.pathAnnotations[path]
	c.pathAnnotationsMu.RUnlock()
	if ok {
		return cached
	}
	annotations := pathAnnotationsForNode(c.annotationEngine, path, isDir)
	// 用户注释优先：命中覆盖层时整体替换内置规则结果（软件自带的注释也可以改）。
	if override, ok := c.annotationOverrideFor(path); ok {
		annotations = []TreeAnnotation{{
			Title:   override.Title,
			Content: override.Content,
			Type:    "user",
		}}
	}
	c.pathAnnotationsMu.Lock()
	if c.pathAnnotations == nil {
		c.pathAnnotations = make(map[string][]TreeAnnotation)
	}
	c.pathAnnotations[path] = annotations
	c.pathAnnotationsMu.Unlock()
	return annotations
}

// setAnnotationOverride 写入一条用户注释并清空路径标注缓存（文件树/搜索/面包屑立即生效）。
// 注意锁序：先放 overrides 锁再拿 c.mu 写锁，避免与 treeAnnotationsFor（mu → overridesMu）
// 形成持锁等待环。
func (c *core) setAnnotationOverride(path string, override PathAnnotationOverride) {
	c.annotationOverridesMu.Lock()
	if c.annotationOverrides == nil {
		c.annotationOverrides = make(map[string]PathAnnotationOverride)
	}
	c.annotationOverrides[path] = override
	c.annotationOverridesMu.Unlock()
	c.mu.Lock()
	c.resetPathAnnotationsLocked()
	c.mu.Unlock()
}

// removeAnnotationOverride 删除一条用户注释（该路径回退到内置注释规则）并清缓存。
func (c *core) removeAnnotationOverride(path string) {
	c.annotationOverridesMu.Lock()
	delete(c.annotationOverrides, path)
	c.annotationOverridesMu.Unlock()
	c.mu.Lock()
	c.resetPathAnnotationsLocked()
	c.mu.Unlock()
}

// annotationOverrideFor 查询用户注释；调用方可已持有 c.mu（独立锁，锁序 mu → overridesMu 同向）。
func (c *core) annotationOverrideFor(path string) (PathAnnotationOverride, bool) {
	c.annotationOverridesMu.RLock()
	defer c.annotationOverridesMu.RUnlock()
	if len(c.annotationOverrides) == 0 {
		return PathAnnotationOverride{}, false
	}
	override, ok := c.annotationOverrides[path]
	return override, ok
}

// replaceAnnotationOverrides 供服务启动时整体装载用户注释（不触发缓存清理）。
func (c *core) replaceAnnotationOverrides(overrides map[string]PathAnnotationOverride) {
	c.annotationOverridesMu.Lock()
	c.annotationOverrides = overrides
	c.annotationOverridesMu.Unlock()
}

func (c *core) recordOpenDuration(duration time.Duration) {
	c.mu.Lock()
	if c.archive != nil {
		c.indexStatus.OpenDurationMs = durationMilliseconds(duration)
	}
	c.mu.Unlock()
}

// replaceArchiveLocked installs a newly materialized archive while retaining
// the current version repository session. The caller must hold c.mu.
func (c *core) replaceArchiveLocked(a *pvf.Archive) error {
	children, paths, err := buildIndex(a)
	if err != nil {
		return err
	}
	c.installArchiveIndexesPreservingSearchLocked(a, children, paths)
	return nil
}

// replaceArchivePayloadLocked installs an archive whose path and data-type
// structure is unchanged. Reusing the existing tree/path indexes avoids a
// second full directory walk when checkout or discard only changes payloads.
// The caller must hold c.mu.
func (c *core) replaceArchivePayloadLocked(a *pvf.Archive, changedIndexes map[int32]struct{}) error {
	if a == nil || c.archive == nil {
		return ErrNoArchive
	}
	if c.indexCancel != nil {
		c.indexCancel()
		c.indexCancel = nil
	}
	if c.advancedCancel != nil {
		c.advancedCancel()
		c.advancedCancel = nil
	}
	preserveSearch := c.indexStatus.State == IndexStateReady && c.searchRecords != nil
	c.indexGen++
	c.batchRevision++
	c.batchPlan = nil
	c.invalidateScriptLocked()
	c.bindRenderingEngineLocked(a)
	c.archive = a
	refreshArchiveIndexMetadataLocked(c, changedIndexes)
	if preserveSearch {
		c.searchIndexDeltaPending = true
		c.searchIndexListPending = nil
	} else {
		c.searchRecords = nil
		c.searchByFile = make(map[int32][]int)
		c.searchMetadata = nil
		c.searchSpecFingerprint = ""
		c.searchIndexDeltaPending = false
		c.searchIndexListPending = nil
		c.treeTagsByFile = make(map[int32][]TreeTag)
		c.visualsByFile = make(map[int32]fileVisuals)
		c.indexStatus = IndexStatus{State: IndexStateIdle}
	}
	c.indexStartedAt = time.Time{}
	c.indexDirty = make(map[int32]struct{}, len(changedIndexes))
	for index := range changedIndexes {
		c.indexDirty[index] = struct{}{}
	}
	c.editorText = make(map[int32]string)
	c.editorAnnotation = editorAnnotationCache{}
	c.annotationRelations = make(map[string]map[string]*relationTarget)
	c.advancedIndex = nil
	c.advancedDirty = nil
	c.advancedStatus = AdvancedSearchIndexStatus{State: AdvancedIndexStateIdle}
	c.binaryCache = make(map[string]map[int32]advancedFileMatch)
	c.advancedQueryCache = make(map[advancedQueryKey]*advancedHitView)
	c.advancedQueryOrder = nil
	c.advancedGen++
	c.unpackCancel.Store(false)
	c.unpackRunning.Store(false)
	return nil
}

// rebuildArchiveIndexesLocked refreshes every derived view after the archive's
// file table changes. The caller must hold c.mu and must pass c.archive.
func (c *core) rebuildArchiveIndexesLocked(a *pvf.Archive) error {
	children, paths, err := buildIndex(a)
	if err != nil {
		return err
	}
	c.installArchiveIndexesPreservingSearchLocked(a, children, paths)
	return nil
}

// installArchiveIndexesLocked installs a complete set of derived indexes.
// The caller must hold c.mu.
func (c *core) installArchiveIndexesLocked(a *pvf.Archive, children map[string][]*TreeNode, paths []pathEntry) {
	c.installArchiveIndexesLockedWithSearch(a, children, paths, false)
}

func (c *core) installArchiveIndexesPreservingSearchLocked(a *pvf.Archive, children map[string][]*TreeNode, paths []pathEntry) {
	c.installArchiveIndexesLockedWithSearch(a, children, paths, true)
}

func (c *core) installArchiveIndexesLockedWithSearch(a *pvf.Archive, children map[string][]*TreeNode, paths []pathEntry, preserveSearch bool) {
	preserveSearch = preserveSearch && c.indexStatus.State == IndexStateReady && c.searchRecords != nil
	if c.indexCancel != nil {
		c.indexCancel()
		c.indexCancel = nil
	}
	if c.advancedCancel != nil {
		c.advancedCancel()
		c.advancedCancel = nil
	}
	c.indexGen++
	c.batchRevision++
	c.batchPlan = nil
	c.invalidateScriptLocked()
	directories := make([]string, 0, len(children))
	for path := range children {
		if path != "" {
			directories = append(directories, path)
		}
	}
	sort.Strings(directories)
	c.bindRenderingEngineLocked(a)
	c.archive = a
	c.annotationRelations = make(map[string]map[string]*relationTarget)
	c.editorText = make(map[int32]string)
	c.editorAnnotation = editorAnnotationCache{}
	c.resetPathAnnotationsLocked()
	c.resetFileNameCacheLocked()
	c.dirChildren = children
	c.directories = directories
	c.sortedPaths = paths
	if preserveSearch {
		// The old semantic snapshot remains readable until the caller starts a
		// path-delta refresh. This keeps structural edits asynchronous.
		c.searchIndexDeltaPending = true
		c.searchIndexListPending = nil
	} else {
		c.searchRecords = nil
		c.searchByFile = make(map[int32][]int)
		c.searchMetadata = nil
		c.searchSpecFingerprint = ""
		c.searchIndexDeltaPending = false
		c.searchIndexListPending = nil
		c.treeTagsByFile = make(map[int32][]TreeTag)
		c.visualsByFile = make(map[int32]fileVisuals)
		c.indexStatus = IndexStatus{State: IndexStateIdle}
	}
	c.indexStartedAt = time.Time{}
	c.indexDirty = make(map[int32]struct{})
	c.advancedIndex = nil
	c.advancedDirty = nil
	c.advancedStatus = AdvancedSearchIndexStatus{State: AdvancedIndexStateIdle}
	c.binaryCache = make(map[string]map[int32]advancedFileMatch)
	c.advancedQueryCache = make(map[advancedQueryKey]*advancedHitView)
	c.advancedQueryOrder = nil
	c.advancedGen++
	c.unpackCancel.Store(false)
	c.unpackRunning.Store(false)
}

// bindRenderingEngineLocked applies the current user-facing renderer to an
// archive that is about to become active. The caller must hold c.mu.
func (c *core) bindRenderingEngineLocked(a *pvf.Archive) {
	if a == nil {
		return
	}
	a.SetScriptRenderer(c.renderingEngine)
}

func (c *core) closeArchive() {
	c.mu.Lock()
	c.detachVersionLocked()
	if c.indexCancel != nil {
		c.indexCancel()
		c.indexCancel = nil
	}
	if c.advancedCancel != nil {
		c.advancedCancel()
		c.advancedCancel = nil
	}
	c.indexGen++
	c.batchRevision++
	c.batchPlan = nil
	c.invalidateScriptLocked()
	c.archive = nil
	c.annotationRelations = nil
	c.editorText = nil
	c.editorAnnotation = editorAnnotationCache{}
	c.resetPathAnnotationsLocked()
	c.dirChildren = nil
	c.directories = nil
	c.sortedPaths = nil
	c.searchRecords = nil
	c.searchByFile = nil
	c.searchMetadata = nil
	c.searchSpecFingerprint = ""
	c.searchIndexDeltaPending = false
	c.searchIndexListPending = nil
	c.treeTagsByFile = nil
	c.visualsByFile = nil
	c.indexStatus = IndexStatus{State: IndexStateIdle}
	c.indexStartedAt = time.Time{}
	c.indexDirty = nil
	c.advancedIndex = nil
	c.advancedDirty = nil
	c.advancedStatus = AdvancedSearchIndexStatus{State: AdvancedIndexStateIdle}
	c.binaryCache = nil
	c.advancedQueryCache = nil
	c.advancedQueryOrder = nil
	c.advancedGen++
	c.unpackCancel.Store(false)
	c.unpackRunning.Store(false)
	c.mu.Unlock()
}

// withArchive runs fn with the loaded archive under read lock.
func (c *core) withArchive(fn func(a *pvf.Archive) error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.archive == nil {
		return ErrNoArchive
	}
	return fn(c.archive)
}

// withArchiveWrite runs fn with the loaded archive under the write lock.
// Archive edits and saves must exclude background index reads because the
// archive overlay and rebuilt tables are mutable.
func (c *core) withArchiveWrite(fn func(a *pvf.Archive) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.archive == nil {
		return ErrNoArchive
	}
	return fn(c.archive)
}

func (c *core) archiveInfo() (pvf.ArchiveInfoView, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.archive == nil {
		return pvf.ArchiveInfoView{}, false
	}
	return c.archive.Info(), true
}

// buildIndex creates the lazy-tree directory index and the search path list.
// Duplicate paths are preserved (each maps to its own file index).
func buildIndex(a *pvf.Archive) (map[string][]*TreeNode, []pathEntry, error) {
	n := a.FileCount()
	dirChildren := make(map[string][]*TreeNode)
	dirSet := make(map[string]bool)
	paths := make([]pathEntry, 0, n)

	// registerDir adds p and all missing ancestors as directory nodes.
	// Invariant: registering p also registers every ancestor, so hitting an
	// already-registered dir means the whole ancestor chain is present.
	registerDir := func(p string) {
		for p != "" {
			if dirSet[p] {
				return
			}
			dirSet[p] = true
			parent, name := splitParent(p)
			dirChildren[parent] = append(dirChildren[parent], &TreeNode{
				Name: name, Path: p, IsDir: true, FileIndex: -1,
			})
			p = parent
		}
	}

	for i := int32(0); i < n; i++ {
		p := a.Path(i)
		if p == "" {
			continue
		}
		f := a.File(i)
		changeKind := archiveChangeKind(a, i)
		paths = append(paths, pathEntry{
			path:       p,
			lower:      strings.ToLower(p),
			idx:        i,
			size:       f.DataSize,
			typ:        f.DataType,
			changeKind: changeKind,
		})

		parent, name := splitParent(p)
		dirChildren[parent] = append(dirChildren[parent], &TreeNode{
			Name: name, Path: p, IsDir: false,
			Size: f.DataSize, DataType: f.DataType, FileIndex: i,
			ChangeKind: changeKind,
		})
		registerDir(parent)
	}

	for _, list := range dirChildren {
		sort.Slice(list, func(x, y int) bool {
			if list[x].IsDir != list[y].IsDir {
				return list[x].IsDir
			}
			return list[x].Name < list[y].Name
		})
		for _, t := range list {
			if t.IsDir {
				t.ChildCount = int32(len(dirChildren[t.Path]))
			}
		}
	}

	sort.Slice(paths, func(x, y int) bool { return paths[x].path < paths[y].path })
	return dirChildren, paths, nil
}

func splitParent(p string) (parent, name string) {
	if slash := strings.LastIndexByte(p, '/'); slash >= 0 {
		return p[:slash], p[slash+1:]
	}
	return "", p
}
