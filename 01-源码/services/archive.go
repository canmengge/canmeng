package services

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"pvfine/internal/logging"
	"pvfine/internal/pvf"
	pvfversion "pvfine/internal/version"
)

// ArchiveService: 打开/关闭归档、状态查询、资源树懒加载与搜索。
type ArchiveService struct {
	c *core

	// 导入任务：同一时刻只允许一个，承载取消开关与进度快照。
	importMu  sync.Mutex
	importJob *importJob
}

func NewArchiveService(c *core) *ArchiveService { return &ArchiveService{c: c} }

// findSaveRemnant 返回目标归档同目录残留的保存临时文件（<path>.pvftmp）。
// 内核 save.go 的 SaveAs 采用「写临时文件 + rename」的原子写，正常完成或出错都会
// 删除临时文件；仅当进程在写入途中被强杀（崩溃/断电）时，才会留下 .pvftmp 残留。
// 第二个返回值表示是否存在残留。
func findSaveRemnant(pvfPath string) (string, bool) {
	if pvfPath == "" {
		return "", false
	}
	remnant := pvfPath + ".pvftmp"
	if _, err := os.Stat(remnant); err == nil {
		return remnant, true
	}
	return "", false
}

// ArchiveInfo 是前端可观察的归档状态快照。
type ArchiveInfo = pvf.ArchiveInfoView

// FileRegistration describes one indexed id/path entry in an archive list.
type FileRegistration struct {
	ID            string `json:"id"`
	Category      string `json:"category"`
	FileIndex     int32  `json:"fileIndex"`
	FilePath      string `json:"filePath"`
	ListFileIndex int32  `json:"listFileIndex"`
	ListPath      string `json:"listPath"`
	EntryPath     string `json:"entryPath"`
}

// OpenDialog 弹出文件选择框并加载归档。
func (s *ArchiveService) OpenDialog() (*ArchiveInfo, error) {
	path, err := application.Get().Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("PVF 归档", "*.pvf").
		AddFilter("所有文件", "*").
		SetTitle("打开 PVF 归档").
		PromptForSingleSelection()
	if err != nil {
		return nil, err // 用户取消等
	}
	if path == "" {
		return nil, nil
	}
	info, err := s.Open(path)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// Open 加载指定路径的归档并构建目录索引。
func (s *ArchiveService) Open(path string) (ArchiveInfo, error) {
	log := logging.For("archive")
	stage := logging.StartStage("archive", "打开归档", "文件", path)
	startedAt := time.Now()

	stat, statErr := os.Stat(path)
	if statErr != nil {
		stage.Fail(statErr, "文件", path)
		return ArchiveInfo{}, statErr
	}
	log.Info("开始加载归档", "文件", path, "大小MB", float64(stat.Size())/(1<<20))

	openStage := logging.StartStage("archive", "解析归档(pvf.Open)")
	a, err := pvf.Open(path)
	if err != nil {
		openStage.Fail(err)
		stage.Fail(err, "文件", path)
		return ArchiveInfo{}, err
	}
	openStage.Done("文件数", a.FileCount())

	indexStage := logging.StartStage("archive", "构建目录索引")
	if err := s.c.setArchive(a); err != nil {
		indexStage.Fail(err)
		stage.Fail(err, "文件", path)
		return ArchiveInfo{}, err
	}
	indexStage.Done()
	s.c.recordOpenDuration(time.Since(startedAt))
	info := a.Info()
	stage.Done("文件数", info.FileCount, "数据块数", info.GroupCount, "Paged110", info.Paged110)
	// B-02：检测上次保存是否被中断（残留 .pvftmp）。save.go 用「临时文件 + rename」
	// 原子写，正常/错误返回都会清理临时文件；只有进程在写入途中被强杀才残留。
	// 这里只提示、不自动删除（残留可能含有价值的写入数据，交由用户决定）。
	if remnant, ok := findSaveRemnant(path); ok {
		logging.For("save").Warn("检测到上次保存的残留临时文件，可能因保存被中断导致",
			"归档", path, "残留", remnant)
		emitEvent("archive:save-remnant", map[string]any{"path": remnant})
	}
	// Version repository discovery/recovery is deliberately detached from the
	// normal open path. The raw PVF and its tree are usable immediately; the
	// background task will replace the in-memory archive only when recovery is
	// actually needed.
	s.c.startVersionLoad(path, a)
	emitEvent("archive:opened", info)
	// A-01：语义索引改为**按需构建**。打开归档只做解析 + 目录索引，不再在后台
	// 抢占 CPU；用户首次需要搜索时由前端触发 RebuildSearchIndex（等价"开始构建"）。
	s.c.deferSearchIndex()
	// 空闲片刻后自动构建（不抢打开瞬间的 CPU），这样文件树里已登记的
	// [中文名] 标签与路径搜索稍后会自动出现，不必手动触发。
	s.c.scheduleIdleSearchIndex(2500 * time.Millisecond)
	return info, nil
}

// Close 关闭当前归档,丢弃未保存的内存修改。
func (s *ArchiveService) Close() {
	logging.For("archive").Info("关闭归档")
	s.c.closeArchive()
	emitEvent("archive:closed")
}

// Info 返回当前归档状态;未打开时 Path 为空。
func (s *ArchiveService) Info() ArchiveInfo {
	info, _ := s.c.archiveInfo()
	return info
}

// IndexStatus 返回当前归档的语义搜索索引状态。
func (s *ArchiveService) IndexStatus() IndexStatus {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	return s.c.indexStatus
}

// RebuildSearchIndex starts an asynchronous forced rebuild. An existing ready
// snapshot remains available to Search while the replacement is prepared.
func (s *ArchiveService) RebuildSearchIndex() (IndexStatus, error) {
	s.c.mu.RLock()
	if s.c.archive == nil {
		s.c.mu.RUnlock()
		return IndexStatus{}, ErrNoArchive
	}
	s.c.mu.RUnlock()
	s.c.startSearchIndexForced()
	return s.IndexStatus(), nil
}

// ListChildren 懒加载某目录的直接子节点;path 为空表示根。
func (s *ArchiveService) ListChildren(path string) ([]*TreeNode, error) {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	if s.c.archive == nil {
		return nil, ErrNoArchive
	}
	list := s.c.dirChildren[path]
	if list == nil {
		return []*TreeNode{}, nil
	}
	result := make([]*TreeNode, len(list))
	for i, node := range list {
		copyNode := *node
		copyNode.Annotations = cloneTreeAnnotations(s.c.treeAnnotationsFor(copyNode.Path, copyNode.IsDir))
		if !copyNode.IsDir {
			copyNode.ChangeKind = archiveChangeKind(s.c.archive, copyNode.FileIndex)
			copyNode.Tags = cloneTreeTags(s.c.treeTagsByFile[copyNode.FileIndex])
			visuals := s.c.visualsByFile[copyNode.FileIndex]
			copyNode.Icon = cloneImageReference(visuals.icon)
			copyNode.FieldImage = cloneImageReference(visuals.fieldImage)
		}
		result[i] = &copyNode
	}
	return result, nil
}

// ListDescendantFiles returns all files below a directory path. An empty path
// returns every file in the archive. Results preserve the archive path order.
func (s *ArchiveService) ListDescendantFiles(scopePath string) ([]*TreeNode, error) {
	scopePath = strings.Trim(strings.ReplaceAll(scopePath, "\\", "/"), "/")

	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	if s.c.archive == nil {
		return nil, ErrNoArchive
	}

	prefix := scopePath
	start := 0
	if scopePath != "" {
		prefix += "/"
		start = sort.Search(len(s.c.sortedPaths), func(i int) bool {
			return s.c.sortedPaths[i].path >= prefix
		})
	}

	result := make([]*TreeNode, 0)
	for _, entry := range s.c.sortedPaths[start:] {
		if scopePath != "" && !strings.HasPrefix(entry.path, prefix) {
			break
		}
		visuals := s.c.visualsByFile[entry.idx]
		result = append(result, &TreeNode{
			Name:        pathBase(entry.path),
			Path:        entry.path,
			Size:        entry.size,
			DataType:    entry.typ,
			FileIndex:   entry.idx,
			ChangeKind:  archiveChangeKind(s.c.archive, entry.idx),
			Tags:        cloneTreeTags(s.c.treeTagsByFile[entry.idx]),
			Annotations: cloneTreeAnnotations(s.c.treeAnnotationsFor(entry.path, false)),
			Icon:        cloneImageReference(visuals.icon),
			FieldImage:  cloneImageReference(visuals.fieldImage),
		})
	}
	return result, nil
}

// ResolveFiles resolves archive files by their normalized paths. Missing
// paths are omitted and duplicate input paths are returned only once.
func (s *ArchiveService) ResolveFiles(paths []string) ([]*TreeNode, error) {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	if s.c.archive == nil {
		return nil, ErrNoArchive
	}

	result := make([]*TreeNode, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		filePath := strings.Trim(strings.ReplaceAll(rawPath, "\\", "/"), "/")
		if filePath == "" {
			continue
		}
		if _, ok := seen[filePath]; ok {
			continue
		}
		seen[filePath] = struct{}{}

		index := sort.Search(len(s.c.sortedPaths), func(i int) bool {
			return s.c.sortedPaths[i].path >= filePath
		})
		if index >= len(s.c.sortedPaths) || s.c.sortedPaths[index].path != filePath {
			continue
		}
		entry := s.c.sortedPaths[index]
		visuals := s.c.visualsByFile[entry.idx]
		result = append(result, &TreeNode{
			Name:        pathBase(entry.path),
			Path:        entry.path,
			Size:        entry.size,
			DataType:    entry.typ,
			FileIndex:   entry.idx,
			ChangeKind:  archiveChangeKind(s.c.archive, entry.idx),
			Tags:        cloneTreeTags(s.c.treeTagsByFile[entry.idx]),
			Annotations: cloneTreeAnnotations(s.c.treeAnnotationsFor(entry.path, false)),
			Icon:        cloneImageReference(visuals.icon),
			FieldImage:  cloneImageReference(visuals.fieldImage),
		})
	}
	return result, nil
}

// ResolveFileNames 返回与输入路径一一对应的显示名（未找到或解析失败时为空串）。
// 供 .lst 编辑器在行内按**可见行**显示目标文件的名称：名称为惰性解析 + 缓存
// （见 core.fileNameFor），避免打开大清单（equipment.lst 有 36 万行）时全量解析。
func (s *ArchiveService) ResolveFileNames(paths []string) ([]string, error) {
	s.c.mu.RLock()
	a := s.c.archive
	if a == nil {
		s.c.mu.RUnlock()
		return nil, ErrNoArchive
	}
	indexes := make([]int32, len(paths))
	for i, rawPath := range paths {
		indexes[i] = -1
		filePath := strings.Trim(strings.ReplaceAll(rawPath, "\\", "/"), "/")
		if filePath == "" {
			continue
		}
		pos := sort.Search(len(s.c.sortedPaths), func(j int) bool {
			return s.c.sortedPaths[j].path >= filePath
		})
		if pos >= len(s.c.sortedPaths) || s.c.sortedPaths[pos].path != filePath {
			continue
		}
		indexes[i] = s.c.sortedPaths[pos].idx
	}
	s.c.mu.RUnlock()

	names := make([]string, len(paths))
	for i, fileIndex := range indexes {
		if fileIndex < 0 {
			continue
		}
		names[i] = s.c.fileNameFor(a, fileIndex)
	}
	return names, nil
}

// FindFileRegistrations returns configured .lst entries that point to the
// supplied file indexes. It uses the same relation definitions as the search
// index, including contextual skill lists.
func (s *ArchiveService) FindFileRegistrations(fileIndexes []int32) ([]*FileRegistration, error) {
	specs := s.c.searchableListSpecs()
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	if s.c.archive == nil {
		return nil, ErrNoArchive
	}
	if err := validateFileIndexes(s.c.archive, fileIndexes); err != nil {
		return nil, err
	}
	return findFileRegistrationsLocked(s.c.archive, specs, fileIndexes), nil
}

// CreateFile adds an empty editable file to the current archive. dataType is
// pvf.TypeScript or pvf.TypeUnicode; the caller can fill its content through
// EditorService.SetText afterwards.
func (s *ArchiveService) CreateFile(path string, dataType int32) (*TreeNode, error) {
	path, err := normalizeNewFilePath(path)
	if err != nil {
		return nil, err
	}
	if dataType != pvf.TypeScript && dataType != pvf.TypeUnicode {
		return nil, fmt.Errorf("不支持的新文件类型: %d", dataType)
	}

	s.c.mu.Lock()
	a := s.c.archive
	if a == nil {
		s.c.mu.Unlock()
		return nil, ErrNoArchive
	}
	if err := s.c.ensureVersionReadyLocked(); err != nil {
		s.c.mu.Unlock()
		return nil, err
	}
	if _, exists := a.Find(path); exists {
		s.c.mu.Unlock()
		return nil, fmt.Errorf("文件已存在: %s", path)
	}
	// 字符串表写保护（上级规则 §6.12）：不允许新建/覆盖禁动表路径。
	if err := guardArchiveWriteByPath(path); err != nil {
		s.c.mu.Unlock()
		return nil, err
	}
	var before pvfversion.ContentSnapshot
	if s.c.versionRepo != nil {
		before = make(pvfversion.ContentSnapshot)
	}
	index := a.AddFile(path, []byte{}, dataType)
	if err := s.c.rebuildArchiveIndexesLocked(a); err != nil {
		s.c.mu.Unlock()
		return nil, err
	}
	if s.c.versionRepo != nil {
		after, snapshotErr := pvfversion.ContentSnapshotFromArchive(a, []string{path})
		if snapshotErr != nil {
			s.c.mu.Unlock()
			return nil, snapshotErr
		}
		if recordErr := s.c.recordVersionMutationLocked("新建文件", before, after); recordErr != nil {
			s.c.mu.Unlock()
			return nil, recordErr
		}
	}
	node := &TreeNode{
		Name:        a.File(index).Name,
		Path:        a.Path(index),
		Size:        0,
		DataType:    dataType,
		FileIndex:   index,
		ChangeKind:  ChangeKindAdded,
		Annotations: cloneTreeAnnotations(s.c.treeAnnotationsFor(a.Path(index), false)),
		Icon:        cloneImageReference(s.c.visualsByFile[index].icon),
		FieldImage:  cloneImageReference(s.c.visualsByFile[index].fieldImage),
	}
	info := a.Info()
	versioned := s.c.versionRepo != nil
	s.c.mu.Unlock()

	s.c.startSearchIndex()
	emitEvent("archive:changed", info)
	if versioned {
		emitVersionState(s.c, "file-created")
	}
	return node, nil
}

// DeleteFiles removes one or more file entries from the current archive.
func (s *ArchiveService) DeleteFiles(fileIndexes []int32) ([]string, error) {
	return s.deleteFiles(fileIndexes, false)
}

// DeleteFilesWithRegistrations removes files and, when requested, the
// configured .lst entries that register those files.
func (s *ArchiveService) DeleteFilesWithRegistrations(fileIndexes []int32, syncRegistrations bool) ([]string, error) {
	return s.deleteFiles(fileIndexes, syncRegistrations)
}

func (s *ArchiveService) deleteFiles(fileIndexes []int32, syncRegistrations bool) ([]string, error) {
	var specs []searchableListSpec
	if syncRegistrations {
		specs = s.c.searchableListSpecs()
	}

	s.c.mu.Lock()
	a := s.c.archive
	if a == nil {
		s.c.mu.Unlock()
		return nil, ErrNoArchive
	}
	if err := s.c.ensureVersionReadyLocked(); err != nil {
		s.c.mu.Unlock()
		return nil, err
	}
	if err := validateFileIndexes(a, fileIndexes); err != nil {
		s.c.mu.Unlock()
		return nil, err
	}
	mutationPaths := make([]string, 0, len(fileIndexes))
	for _, index := range fileIndexes {
		mutationPaths = append(mutationPaths, a.Path(index))
	}
	// 字符串表写保护（上级规则 §6.12）：删除禁动 .str 同样会让客户端成片乱码。
	for _, mutationPath := range mutationPaths {
		if err := guardArchiveWriteByPath(mutationPath); err != nil {
			s.c.mu.Unlock()
			return nil, err
		}
	}
	var registrations []*FileRegistration
	if syncRegistrations {
		registrations = findFileRegistrationsLocked(a, specs, fileIndexes)
		for _, registration := range registrations {
			mutationPaths = append(mutationPaths, registration.ListPath)
		}
	}
	var before pvfversion.ContentSnapshot
	if s.c.versionRepo != nil {
		var snapshotErr error
		before, snapshotErr = pvfversion.ContentSnapshotFromArchive(a, mutationPaths)
		if snapshotErr != nil {
			s.c.mu.Unlock()
			return nil, snapshotErr
		}
	}
	if syncRegistrations {
		byList := make(map[int32][]pvf.ListPair)
		for _, registration := range registrations {
			byList[registration.ListFileIndex] = append(
				byList[registration.ListFileIndex],
				pvf.ListPair{ID: registration.ID, Path: registration.EntryPath},
			)
		}
		for listIndex, entries := range byList {
			if _, err := a.RemoveListPairs(listIndex, entries); err != nil {
				s.c.mu.Unlock()
				return nil, err
			}
		}
	}
	paths, err := a.RemoveFiles(fileIndexes)
	if err != nil {
		s.c.mu.Unlock()
		return nil, err
	}
	if len(paths) == 0 {
		s.c.mu.Unlock()
		return []string{}, nil
	}
	if err := s.c.rebuildArchiveIndexesLocked(a); err != nil {
		s.c.mu.Unlock()
		return nil, err
	}
	if s.c.versionRepo != nil {
		after, snapshotErr := pvfversion.ContentSnapshotFromArchive(a, mutationPaths)
		if snapshotErr != nil {
			s.c.mu.Unlock()
			return nil, snapshotErr
		}
		if recordErr := s.c.recordVersionMutationLocked("删除文件", before, after); recordErr != nil {
			s.c.mu.Unlock()
			return nil, recordErr
		}
	}
	info := a.Info()
	versioned := s.c.versionRepo != nil
	forceSearchRefresh := false
	for _, mutationPath := range mutationPaths {
		normalizedPath := normalizeSearchPath(mutationPath)
		if strings.HasSuffix(normalizedPath, ".str") || isNPCEntryPath(normalizedPath) || normalizedPath == npcListPath {
			forceSearchRefresh = true
			break
		}
	}
	s.c.mu.Unlock()

	if forceSearchRefresh {
		s.c.startSearchIndexForced()
	} else {
		s.c.startSearchIndex()
	}
	emitEvent("archive:changed", info)
	if versioned {
		emitVersionState(s.c, "files-deleted")
	}
	return paths, nil
}

func validateFileIndexes(a *pvf.Archive, indexes []int32) error {
	for _, index := range indexes {
		if index < 0 || index >= a.FileCount() {
			return fmt.Errorf("文件索引越界: %d", index)
		}
	}
	return nil
}

func findFileRegistrationsLocked(a *pvf.Archive, specs []searchableListSpec, fileIndexes []int32) []*FileRegistration {
	targets := make(map[int32]string, len(fileIndexes))
	for _, index := range fileIndexes {
		if index < 0 || index >= a.FileCount() {
			continue
		}
		targets[index] = a.Path(index)
	}
	if len(targets) == 0 {
		return []*FileRegistration{}
	}

	registrations := make([]*FileRegistration, 0)
	for _, spec := range specs {
		listIndex, ok := a.FindList(spec.listPath)
		if !ok {
			continue
		}
		listPath := a.Path(listIndex)
		pairs, err := a.ScriptListPairs(listIndex)
		if err != nil {
			continue
		}
		for _, pair := range pairs {
			_, targetIndex, ok := findListTargetInArchive(a, listPath, pair.Path)
			if !ok {
				continue
			}
			filePath, selected := targets[targetIndex]
			if !selected {
				continue
			}
			registrations = append(registrations, &FileRegistration{
				ID:            pair.ID,
				Category:      spec.category,
				FileIndex:     targetIndex,
				FilePath:      filePath,
				ListFileIndex: listIndex,
				ListPath:      listPath,
				EntryPath:     pair.Path,
			})
		}
	}
	sort.SliceStable(registrations, func(i, j int) bool {
		left, right := registrations[i], registrations[j]
		if left.FilePath != right.FilePath {
			return left.FilePath < right.FilePath
		}
		if left.ListPath != right.ListPath {
			return left.ListPath < right.ListPath
		}
		return left.ID < right.ID
	})
	return registrations
}

// SuggestDirectories returns directory paths with a case-insensitive prefix
// match. An empty prefix returns no suggestions to avoid flooding the UI.
func (s *ArchiveService) SuggestDirectories(prefix string, limit int) ([]string, error) {
	prefix = normalizeAdvancedScope(prefix)
	if prefix == "" {
		return []string{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	if s.c.archive == nil {
		return nil, ErrNoArchive
	}
	result := make([]string, 0, limit)
	for _, path := range s.c.directories {
		if !strings.HasPrefix(strings.ToLower(path), prefix) {
			continue
		}
		result = append(result, path)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func normalizeNewFilePath(raw string) (string, error) {
	path := strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "/")
	if path == "" {
		return "", errors.New("文件名不能为空")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("文件路径无效: %q", raw)
		}
	}
	return path, nil
}

// SearchResult 是一页搜索命中;NextCursor < 0 表示已扫完。
type SearchResult struct {
	Hits       []*SearchHit `json:"hits"`
	NextCursor int          `json:"nextCursor"`
	Scanned    int          `json:"scanned"`
}

// Search 在路径、语义名称和 id 中做不区分大小写的子串匹配。
// 查询包含 * 或 ? 时，改用通配符匹配：* 匹配任意长度字符，? 匹配一个字符。
// cursor 传上次返回的 NextCursor(首次传 0),limit 为本页上限(1..1000)。
func (s *ArchiveService) Search(query string, cursor int, limit int) (*SearchResult, error) {
	return s.search(query, cursor, limit, false)
}

// SearchExact 在路径、语义名称和 id 中做不区分大小写的全量匹配。
// 查询包含 * 或 ? 时，通配符仍按完整字段匹配。
// cursor 传上次返回的 NextCursor(首次传 0),limit 为本页上限(1..1000)。
func (s *ArchiveService) SearchExact(query string, cursor int, limit int) (*SearchResult, error) {
	return s.search(query, cursor, limit, true)
}

func (s *ArchiveService) search(query string, cursor int, limit int, exact bool) (*SearchResult, error) {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	res := &SearchResult{Hits: []*SearchHit{}, NextCursor: -1}
	if s.c.archive == nil {
		return nil, ErrNoArchive
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return res, nil
	}
	if s.c.indexStatus.State == IndexStateBuilding || s.c.indexStatus.State == IndexStateIdle {
		return nil, ErrSearchIndexing
	}
	if s.c.indexStatus.State == IndexStateError {
		return nil, errors.New(s.c.indexStatus.Error)
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if cursor < 0 {
		cursor = 0
	}
	matcher := newSearchMatcher(q, exact)
	records := s.c.searchRecords
	i := cursor
	for ; i < len(records) && len(res.Hits) < limit; i++ {
		record := &records[i]
		matched := matcher.match(record.lowerPath) ||
			matcher.match(record.lowerName) ||
			matcher.match(record.lowerID)
		if matched {
			hit := record.hit
			hit.ChangeKind = archiveChangeKind(s.c.archive, hit.FileIndex)
			hit.Annotations = cloneTreeAnnotations(s.c.treeAnnotationsFor(hit.Path, false))
			hit.PathAnnotations = clonePathAnnotationChain(s.c, hit.Path)
			res.Hits = append(res.Hits, &hit)
		}
	}
	if i < len(records) {
		res.NextCursor = i
	}
	res.Scanned = i
	return res, nil
}

// SearchRanked 是面向 AI 工具的排序搜索：对全量 searchRecords 做一次完整扫描，
// 按命中质量分桶（名称全等 > 名称前缀 > 名称包含 > ID > 路径包含），合并取前 limit 条。
// 相比 Search（凑满 limit 即早退、只能命中排序靠前的记录），一次调用即可拿到最相关的
// 命中，避免 AI 反复换关键词盲搜。scope 非空时只保留路径以 scope（不区分大小写）开头
// 的记录。查询含 * ? 通配符时仍按通配符匹配（此时不再做前缀/全等细分）。
func (s *ArchiveService) SearchRanked(query string, scope string, limit int) (*SearchResult, error) {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	res := &SearchResult{Hits: []*SearchHit{}, NextCursor: -1}
	if s.c.archive == nil {
		return nil, ErrNoArchive
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return res, nil
	}
	if s.c.indexStatus.State == IndexStateBuilding || s.c.indexStatus.State == IndexStateIdle {
		return nil, ErrSearchIndexing
	}
	if s.c.indexStatus.State == IndexStateError {
		return nil, errors.New(s.c.indexStatus.Error)
	}
	if limit <= 0 || limit > 1000 {
		limit = 20
	}
	scopeLower := strings.ToLower(strings.TrimSpace(scope))
	hasWildcard := strings.ContainsAny(q, "*?")
	matcher := newSearchMatcher(q, false)
	records := s.c.searchRecords
	capEach := limit * 2
	if capEach < 40 {
		capEach = 40
	}
	var exactHits, prefixHits, nameHits, idHits, pathHits []*SearchHit
	add := func(dst *[]*SearchHit, record *searchRecord) {
		if len(*dst) >= capEach {
			return
		}
		hit := record.hit
		hit.ChangeKind = archiveChangeKind(s.c.archive, hit.FileIndex)
		hit.Annotations = cloneTreeAnnotations(s.c.treeAnnotationsFor(hit.Path, false))
		hit.PathAnnotations = clonePathAnnotationChain(s.c, hit.Path)
		*dst = append(*dst, &hit)
	}
	for i := range records {
		record := &records[i]
		if scopeLower != "" && !strings.HasPrefix(record.lowerPath, scopeLower) {
			continue
		}
		if matcher.match(record.lowerName) {
			switch {
			case record.lowerName == q:
				add(&exactHits, record)
			case !hasWildcard && strings.HasPrefix(record.lowerName, q):
				add(&prefixHits, record)
			default:
				add(&nameHits, record)
			}
			continue
		}
		if matcher.match(record.lowerID) {
			add(&idHits, record)
			continue
		}
		if matcher.match(record.lowerPath) {
			add(&pathHits, record)
		}
	}
	merged := make([]*SearchHit, 0, limit)
buckets:
	for _, bucket := range [][]*SearchHit{exactHits, prefixHits, nameHits, idHits, pathHits} {
		for _, hit := range bucket {
			if len(merged) >= limit {
				break buckets
			}
			merged = append(merged, hit)
		}
	}
	res.Hits = merged
	return res, nil
}

// clonePathAnnotationChain 返回 filePath 自身及其各级祖先目录的目录标注。
//
// P-2006：不再从全量预计算 map 取值，改为经 core.treeAnnotationsFor 按需计算。
// filePath 自身是文件（调用方来自搜索结果），其祖先必为目录，故首个用 false、
// 其后用 true。调用方须已持有 c.mu（访问器只读 c.annotationEngine）。
func clonePathAnnotationChain(c *core, filePath string) map[string][]TreeAnnotation {
	result := make(map[string][]TreeAnnotation)
	current := filePath
	isDir := false
	for current != "" {
		if annotations := cloneTreeAnnotations(c.treeAnnotationsFor(current, isDir)); len(annotations) > 0 {
			result[current] = annotations
		}
		current, _ = splitParent(current)
		isDir = true
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
