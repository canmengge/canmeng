package services

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"pvfine/internal/pvf"
	pvfversion "pvfine/internal/version"
)

const (
	ImportModeText          = "text"
	ImportModeRaw           = "raw"
	maxImportPreviewEntries = 500
)

// 导入冲突处理策略（ImportFilesEx 的 conflict 参数）。
const (
	ImportConflictOverwrite = "overwrite" // 覆盖（默认；与历史 ImportFiles 行为一致）
	ImportConflictRename    = "rename"    // 重命名：目标已存在时另存为 name_1.ext、name_2.ext…
	ImportConflictSkip      = "skip"      // 跳过：保留归档内现有文件
	ImportConflictAbort     = "abort"     // 终止：存在任一同名冲突即整体取消（不碰归档）
)

// ImportResult describes one atomically applied import operation.
type ImportResult struct {
	TargetDir        string   `json:"targetDir"`
	Mode             string   `json:"mode"`
	ImportedCount    int      `json:"importedCount"`
	OverwrittenCount int      `json:"overwrittenCount"`
	SkippedCount     int      `json:"skippedCount"`
	ChangedPaths     []string `json:"changedPaths,omitempty"`
	// ChangedIndexes 与 ChangedPaths 一一对应，供调用方做目录索引的增量补丁
	// （前端不需要，但服务端装索引时要靠它避免全量重建）。
	ChangedIndexes []int32 `json:"changedIndexes,omitempty"`
}

// ImportPreview describes the validated changes without modifying the live
// archive. Entries is capped for large directory imports; the counters remain
// complete.
type ImportPreview struct {
	TargetDir        string                `json:"targetDir"`
	Mode             string                `json:"mode"`
	TotalFiles       int                   `json:"totalFiles"`
	ImportedCount    int                   `json:"importedCount"`
	OverwrittenCount int                   `json:"overwrittenCount"`
	Entries          []*ImportPreviewEntry `json:"entries"`
	EntriesTruncated bool                  `json:"entriesTruncated"`
}

// ImportPreviewEntry is one validated source-to-archive mapping.
type ImportPreviewEntry struct {
	SourcePath string `json:"sourcePath"`
	TargetPath string `json:"targetPath"`
	DataType   int32  `json:"dataType"`
	Size       int64  `json:"size"`
	Overwrite  bool   `json:"overwrite"`
}

type importFile struct {
	sourcePath string
	targetPath string
	data       []byte
	text       string
	dataType   int32
}

type importSelection struct {
	path  string
	isDir bool
}

// ImportFilesDialog opens a native multi-selection picker and imports the
// selected files or directories into targetDir.
func (s *ArchiveService) ImportFilesDialog(targetDir, mode string) (*ImportResult, error) {
	paths, err := s.SelectImportFilesDialog()
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return s.ImportFiles(paths, targetDir, mode)
}

// SelectImportFilesDialog opens the native multi-selection picker without
// changing the current archive. The returned paths can be previewed first.
func (s *ArchiveService) SelectImportFilesDialog() ([]string, error) {
	return application.Get().Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(true).
		AddFilter("所有文件", "*").
		SetTitle("导入文件").
		PromptForMultipleSelection()
}

// SelectImportFilesDialogFiles 打开原生**多选文件**对话框（不含文件夹）。
// Windows 原生对话框选择文件夹时只能单选（系统限制），要一次导入多个文件夹
// 请用拖拽或应用内的「文件夹选择器」（ListLocalEntries）。
func (s *ArchiveService) SelectImportFilesDialogFiles() ([]string, error) {
	return application.Get().Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("所有文件", "*").
		SetTitle("选择要导入的文件（可多选）").
		PromptForMultipleSelection()
}

// LocalEntry 是本地磁盘的一个条目（应用内文件夹选择器用）。
type LocalEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

// maxLocalFilesPerDir 单目录最多返回的文件条目数：文件选择器面对超大目录时
// 避免一次性把几万个文件塞给前端（目录数量少，不设上限）。
const maxLocalFilesPerDir = 3000

// ListLocalFiles 列出本地目录下的**子目录 + 文件**（目录在前、文件在后），path 为空
// 时返回盘符列表。供前端自绘的「多选文件 / 文件夹」对话框懒加载使用：Windows 原生
// 对话框在 wails beta.12 的实现里文件夹模式与多选互斥，无法一次选择多个文件夹。
func (s *ArchiveService) ListLocalFiles(path string) ([]LocalEntry, error) {
	return listLocalDirEntries(path, true)
}

// ListLocalDirectories 只列出本地目录（不含文件），path 为空时返回盘符列表。
// 供「选择导出目标目录」这类只允许选文件夹的应用内选择器使用。
func (s *ArchiveService) ListLocalDirectories(path string) ([]LocalEntry, error) {
	return listLocalDirEntries(path, false)
}

// CreateLocalDirectory 在本地创建目录（含父级），返回创建后的路径。
// 供应用内的目录选择器在导出前新建目标文件夹使用。
func (s *ArchiveService) CreateLocalDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("目录路径不能为空")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("创建目录 %q 失败: %w", path, err)
	}
	return path, nil
}

func listLocalDirEntries(path string, includeFiles bool) ([]LocalEntry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		entries := make([]LocalEntry, 0, 8)
		for c := 'C'; c <= 'Z'; c++ {
			root := fmt.Sprintf("%c:\\", c)
			if info, err := os.Stat(root); err == nil && info.IsDir() {
				entries = append(entries, LocalEntry{
					Name:  fmt.Sprintf("本地磁盘 (%c:)", c),
					Path:  root,
					IsDir: true,
				})
			}
		}
		return entries, nil
	}
	dirEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("读取目录 %q 失败: %w", path, err)
	}
	skipNoise := func(name string) bool {
		return strings.HasPrefix(name, "$") || strings.HasPrefix(name, ".") ||
			strings.EqualFold(name, "System Volume Information")
	}
	dirs := make([]LocalEntry, 0, len(dirEntries))
	files := make([]LocalEntry, 0)
	for _, entry := range dirEntries {
		name := entry.Name()
		if entry.IsDir() {
			if skipNoise(name) {
				continue
			}
			dirs = append(dirs, LocalEntry{Name: name, Path: filepath.Join(path, name), IsDir: true})
			continue
		}
		if !includeFiles {
			continue
		}
		if skipNoise(name) {
			continue
		}
		files = append(files, LocalEntry{Name: name, Path: filepath.Join(path, name), IsDir: false})
		if len(files) >= maxLocalFilesPerDir {
			break
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})
	return append(dirs, files...), nil
}

// PreviewImport 只做「路径映射 + 冲突统计 + 可写性校验」，不修改活动归档。
//
// 2026-09-25 性能修正（原本会克隆整份归档、写入暂存归档并**全量重建目录索引**，
// 实测 15.4 s）：预览阶段这些操作对正确性没有贡献 —— 真正的应用发生在
// ImportFiles，且它是原子的（先写 stage，成功才安装）。因此这里只保留：
//  1. 扫描目录 + 读取源文件（带进度、可取消）；
//  2. 按归档路径判重（a.Find，O(1) 查表，千级文件只需毫秒）；
//  3. 字符串表写保护的路径判定（规则 §6.12，默认关闭时是空操作）。
func (s *ArchiveService) PreviewImport(sourcePaths []string, targetDir, mode string) (*ImportPreview, error) {
	job, err := s.beginImport()
	if err != nil {
		return nil, err
	}
	defer s.endImport(job)

	files, targetDir, mode, err := prepareImportFiles(sourcePaths, targetDir, mode, job)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errorsNoImportFiles()
	}

	preview := &ImportPreview{
		TargetDir:  targetDir,
		Mode:       mode,
		TotalFiles: len(files),
		Entries:    []*ImportPreviewEntry{},
	}
	for _, file := range files {
		// 写保护按路径判定：命中禁动表时在预览阶段就报错，避免用户白等一次应用。
		if err := guardArchiveWriteByPath(file.targetPath); err != nil {
			return nil, fmt.Errorf("导入 %q 被拦截: %w", file.targetPath, err)
		}
	}

	// 判重只需读归档路径索引：短读锁，不再跨十几秒的重活。
	s.c.mu.RLock()
	a := s.c.archive
	if a == nil {
		s.c.mu.RUnlock()
		return nil, ErrNoArchive
	}
	if err := s.c.ensureVersionReadyLocked(); err != nil {
		s.c.mu.RUnlock()
		return nil, err
	}
	for _, file := range files {
		_, overwrite := a.Find(file.targetPath)
		if overwrite {
			preview.OverwrittenCount++
		} else {
			preview.ImportedCount++
		}
		if len(preview.Entries) < maxImportPreviewEntries {
			preview.Entries = append(preview.Entries, &ImportPreviewEntry{
				SourcePath: file.sourcePath,
				TargetPath: file.targetPath,
				DataType:   file.dataType,
				Size:       int64(len(file.data)),
				Overwrite:  overwrite,
			})
		} else {
			preview.EntriesTruncated = true
		}
	}
	s.c.mu.RUnlock()
	return preview, nil
}

// ImportFiles imports sourcePaths into targetDir. The source paths may be
// files or directories. All source data is read and staged before the live
// archive is replaced, so an error leaves the current archive unchanged.
func (s *ArchiveService) ImportFiles(sourcePaths []string, targetDir, mode string) (*ImportResult, error) {
	return s.ImportFilesEx(sourcePaths, targetDir, mode, ImportConflictOverwrite)
}

// ImportFilesEx 同 ImportFiles，但支持文件冲突处理策略（覆盖/重命名/跳过/终止）。
// abort 在扫描完成后立刻检查冲突并整体取消，不碰活动归档；rename 在写入 stage
// 时改用不重名的目标路径。
func (s *ArchiveService) ImportFilesEx(sourcePaths []string, targetDir, mode, conflict string) (*ImportResult, error) {
	job, err := s.beginImport()
	if err != nil {
		return nil, err
	}
	defer s.endImport(job)

	// ① 扫描 + 读取：完全不持锁，可取消、有进度。
	files, targetDir, mode, err := prepareImportFiles(sourcePaths, targetDir, mode, job)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errorsNoImportFiles()
	}
	conflict, err = normalizeImportConflict(conflict)
	if err != nil {
		return nil, err
	}
	// 「终止」策略：扫描完立刻判冲突，尽早失败（此时还没克隆 stage，零成本取消）。
	if conflict == ImportConflictAbort {
		if conflicts := s.findImportConflicts(files); len(conflicts) > 0 {
			return nil, fmt.Errorf("发现 %d 个同名冲突（冲突处理=终止），导入已取消，例如：%s",
				len(conflicts), strings.Join(conflicts, "、"))
		}
	}

	targetPaths := make([]string, 0, len(files))
	for _, file := range files {
		targetPaths = append(targetPaths, file.targetPath)
	}

	// ② 短读锁：取活动归档、记录代际、克隆出独立 stage。
	//    stage 是我们的私有副本，之后的重活全部在锁外做（P0-3）。
	s.c.mu.RLock()
	a := s.c.archive
	if a == nil {
		s.c.mu.RUnlock()
		return nil, ErrNoArchive
	}
	if err := s.c.ensureVersionReadyLocked(); err != nil {
		s.c.mu.RUnlock()
		return nil, err
	}
	revision := s.c.batchRevision
	// 连"就地改文本"也要防：若在暂存期间有人改了活动归档，安装 stage 会静默丢掉那次改动。
	baseModified := a.ModifiedCount()
	var before pvfversion.ContentSnapshot
	if s.c.versionRepo != nil {
		before, err = pvfversion.ContentSnapshotFromArchive(a, targetPaths)
		if err != nil {
			s.c.mu.RUnlock()
			return nil, err
		}
	}
	stage := a.CloneForBatch()
	oldChildren, oldPaths := s.c.dirChildren, s.c.sortedPaths
	s.c.mu.RUnlock()

	// ③ 锁外：把变更写进 stage（失败或取消都不会碰活动归档）。
	job.set(ImportProgress{Phase: "stage", Total: len(files)})
	result, err := applyPreparedImport(stage, files, mode, conflict, job)
	if err != nil {
		return nil, err
	}
	result.TargetDir = targetDir
	result.Mode = mode

	// ④ 锁外：目录索引**增量**补丁（P1）——只处理被改动的路径，
	//    不再对 431 万条做 buildIndex（实测 8.7 s）。无法增量时退回全量。
	job.set(ImportProgress{Phase: "index", Total: len(files)})
	children, paths, patched := patchArchiveIndex(oldChildren, oldPaths, stage, result.ChangedIndexes)
	if !patched {
		if children, paths, err = buildIndex(stage); err != nil {
			return nil, err
		}
	}

	// 版本快照（after）也在锁外算好，装索引时才短暂持写锁。
	var after pvfversion.ContentSnapshot
	if s.c.versionRepo != nil {
		after, err = pvfversion.ContentSnapshotFromArchive(stage, targetPaths)
		if err != nil {
			return nil, err
		}
	}

	// ⑤ 短写锁：校验代际后安装。期间若有其它操作改动过归档，本次放弃（不安装）。
	s.c.mu.Lock()
	if s.c.archive != a || s.c.batchRevision != revision || a.ModifiedCount() != baseModified {
		s.c.mu.Unlock()
		return nil, ErrImportStale
	}
	if s.c.versionRepo != nil {
		if err := s.c.recordVersionMutationLocked("导入文件", before, after); err != nil {
			s.c.mu.Unlock()
			return nil, err
		}
	}

	s.c.installArchiveIndexesPreservingSearchLocked(stage, children, paths)
	// 结构变化（新增/覆盖文件改变了路径表与 fileIndex）：打上增量刷新标记。
	// 这样导入完成后的 startSearchIndex() 会走 buildSearchIndexDelta（秒级）——
	// 补上新文件的搜索记录与树标签（[中文名]/注释），而不是几十秒的全量重建。
	// 索引刷新在导入返回之后异步进行，不影响导入速度。
	s.c.searchIndexDeltaPending = true
	if s.c.indexDirty == nil {
		s.c.indexDirty = make(map[int32]struct{})
	}
	changedPathSet := make(map[string]struct{}, len(files))
	forceSearchRefresh := false
	for _, file := range files {
		normalizedPath := normalizeSearchPath(file.targetPath)
		changedPathSet[normalizedPath] = struct{}{}
		if strings.HasSuffix(normalizedPath, ".str") || isNPCEntryPath(normalizedPath) || normalizedPath == npcListPath {
			forceSearchRefresh = true
		}
		if index, exists := stage.Find(file.targetPath); exists {
			s.c.indexDirty[index] = struct{}{}
		}
	}
	if s.c.searchIndexListPending == nil {
		s.c.searchIndexListPending = make(map[int32]struct{})
	}
	for _, spec := range s.c.searchableListSpecsLocked() {
		listIndex, exists := stage.FindList(spec.listPath)
		if !exists {
			continue
		}
		if _, changed := changedPathSet[normalizeSearchPath(stage.Path(listIndex))]; changed {
			s.c.searchIndexListPending[listIndex] = struct{}{}
		}
	}
	info := stage.Info()
	versioned := s.c.versionRepo != nil
	s.c.mu.Unlock()

	if forceSearchRefresh {
		s.c.startSearchIndexForced()
	} else {
		s.c.startSearchIndex()
	}
	emitEvent("archive:changed", info)
	emitEvent("archive:advanced-search-stale", map[string]any{"import": true})
	if versioned {
		emitVersionState(s.c, "files-imported")
	}
	return result, nil
}

func errorsNoImportFiles() error {
	return fmt.Errorf("没有可导入的文件")
}

func prepareImportFiles(sourcePaths []string, targetDir, mode string, job *importJob) ([]importFile, string, string, error) {
	var err error
	targetDir, err = normalizeImportTargetDir(targetDir)
	if err != nil {
		return nil, "", "", err
	}
	mode, err = normalizeImportMode(mode)
	if err != nil {
		return nil, "", "", err
	}
	files, err := collectImportFiles(sourcePaths, targetDir, job)
	if err != nil {
		return nil, "", "", err
	}
	if len(files) == 0 {
		return files, targetDir, mode, nil
	}
	if err := readImportFiles(files, mode, job); err != nil {
		return nil, "", "", err
	}
	return files, targetDir, mode, nil
}

func normalizeImportMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ImportModeText
	}
	if mode != ImportModeText && mode != ImportModeRaw {
		return "", fmt.Errorf("不支持的导入模式: %q", mode)
	}
	return mode, nil
}

func normalizeImportTargetDir(raw string) (string, error) {
	dir := strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "/")
	if dir == "" {
		return "", nil
	}
	return normalizeNewFilePath(dir)
}

func collectImportFiles(sourcePaths []string, targetDir string, job *importJob) ([]importFile, error) {
	selections := make([]importSelection, 0, len(sourcePaths))
	seenSelections := make(map[string]struct{}, len(sourcePaths))
	scanned := 0
	for _, rawPath := range sourcePaths {
		if err := job.tick("scan", scanned, 0, 0, false); err != nil {
			return nil, err
		}
		rawPath = strings.TrimSpace(rawPath)
		if rawPath == "" {
			continue
		}
		path, err := filepath.Abs(filepath.Clean(rawPath))
		if err != nil {
			return nil, fmt.Errorf("解析来源路径 %q 失败: %w", rawPath, err)
		}
		if _, exists := seenSelections[path]; exists {
			continue
		}
		seenSelections[path] = struct{}{}

		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("读取来源路径 %q 失败: %w", rawPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("不支持符号链接来源: %q", rawPath)
		}
		if info.IsDir() {
			selections = append(selections, importSelection{path: path, isDir: true})
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("来源不是普通文件或目录: %q", rawPath)
		}
		selections = append(selections, importSelection{path: path})
	}

	sort.Slice(selections, func(i, j int) bool {
		return selections[i].path < selections[j].path
	})
	selectedDirs := make([]string, 0)
	filtered := selections[:0]
	for _, selection := range selections {
		redundant := false
		for _, dir := range selectedDirs {
			if selection.path != dir && isPathWithin(dir, selection.path) {
				redundant = true
				break
			}
		}
		if redundant {
			continue
		}
		filtered = append(filtered, selection)
		if selection.isDir {
			selectedDirs = append(selectedDirs, selection.path)
		}
	}

	files := make([]importFile, 0)
	seenSources := make(map[string]struct{})
	appendFile := func(sourcePath, relativePath string) error {
		if _, exists := seenSources[sourcePath]; exists {
			return nil
		}
		seenSources[sourcePath] = struct{}{}
		scanned++
		// 每个来源文件都能成为取消点：选到大目录时才不会"转圈到天荒地老"。
		if err := job.tick("scan", scanned, 0, 0, false); err != nil {
			return err
		}
		targetPath := targetDir
		if targetPath != "" {
			targetPath += "/"
		}
		targetPath += filepath.ToSlash(relativePath)
		targetPath, err := normalizeNewFilePath(targetPath)
		if err != nil {
			return fmt.Errorf("来源 %q 映射到归档路径失败: %w", sourcePath, err)
		}
		files = append(files, importFile{sourcePath: sourcePath, targetPath: targetPath})
		return nil
	}

	for _, selection := range filtered {
		if !selection.isDir {
			if err := appendFile(selection.path, filepath.Base(selection.path)); err != nil {
				return nil, err
			}
			continue
		}

		directoryName := filepath.Base(selection.path)
		err := filepath.WalkDir(selection.path, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			relative, relErr := filepath.Rel(selection.path, path)
			if relErr != nil {
				return relErr
			}
			return appendFile(path, filepath.Join(directoryName, relative))
		})
		if err != nil {
			return nil, fmt.Errorf("扫描目录 %q 失败: %w", selection.path, err)
		}
	}

	if len(files) == 0 {
		return []importFile{}, nil
	}
	sort.Slice(files, func(i, j int) bool {
		left := strings.ToLower(strings.ReplaceAll(files[i].targetPath, "\\", "/"))
		right := strings.ToLower(strings.ReplaceAll(files[j].targetPath, "\\", "/"))
		if left != right {
			return left < right
		}
		return files[i].sourcePath < files[j].sourcePath
	})
	seenTargets := make(map[string]string, len(files))
	for _, file := range files {
		key := strings.ToLower(file.targetPath)
		if previous, exists := seenTargets[key]; exists {
			return nil, fmt.Errorf("批次内归档路径冲突: %q 与 %q 都映射到 %q", previous, file.sourcePath, file.targetPath)
		}
		seenTargets[key] = file.sourcePath
	}
	return files, nil
}

func isPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) {
		return false
	}
	prefix := ".." + string(filepath.Separator)
	return !strings.HasPrefix(relative, prefix)
}

func readImportFiles(files []importFile, mode string, job *importJob) error {
	var read int64
	for index := range files {
		file := &files[index]
		data, err := os.ReadFile(file.sourcePath)
		if err != nil {
			return fmt.Errorf("读取 %q 失败: %w", file.sourcePath, err)
		}
		read += int64(len(data))
		if err := job.tick("read", index+1, len(files), read, false); err != nil {
			return err
		}
		file.data = data
		file.dataType = importDataType(file.targetPath)
		if mode == ImportModeText {
			text, err := decodeImportText(file.sourcePath, data)
			if err != nil {
				return err
			}
			file.text = text
		}
	}
	return nil
}

func importDataType(path string) int32 {
	if strings.EqualFold(filepath.Ext(path), ".str") {
		return pvf.TypeUnicode
	}
	return pvf.TypeScript
}

// decodeImportText 把导入文件内容归一化为 UTF-8 文本。PVF 的 .str（TypeUnicode）
// 在归档内外都是 UTF-16LE（通常无 BOM），因此文本导入除 UTF-8 外必须同时接受
// UTF-16，否则默认方式无法导入任何标准字符串表文件。写回时 SetText 会按
// TypeUnicode 自动重新编码为 UTF-16LE，往返无损。
func decodeImportText(sourcePath string, data []byte) (string, error) {
	switch {
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		return string(data[3:]), nil
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return trimImportBOM(string(utf16.Decode(importU16LE(data[2:])))), nil
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return trimImportBOM(string(utf16.Decode(importU16BE(data[2:])))), nil
	}
	// 无 BOM：纯 ASCII 的 UTF-16LE 同时是"合法"的 UTF-8（NUL 间隔字节），因此
	// 先按 NUL 密度识别 UTF-16 特征（PVF .str 的常见形态），UTF-8 兜底。
	if looksLikeUTF16Bytes(data) && len(data)%2 == 0 {
		if order := detectUTF16ByteOrder(data); order != 0 {
			units := importU16BE(data)
			if order == 'l' {
				units = importU16LE(data)
			}
			text := string(utf16.Decode(units))
			if importTextPlausible(text) {
				return trimImportBOM(text), nil
			}
		}
	}
	if utf8.Valid(data) {
		return string(data), nil
	}
	return "", fmt.Errorf("无法识别 %q 的文本编码（UTF-8/UTF-16 均不匹配），二进制内容请改用「原始字节」方式导入", sourcePath)
}

// looksLikeUTF16Bytes 判断字节序列是否更像 UTF-16：正常文本中 NUL 字节几乎
// 不出现，而 UTF-16 的 ASCII 内容约有一半字节是 NUL 高字节。
func looksLikeUTF16Bytes(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	zeros := 0
	for _, b := range data {
		if b == 0 {
			zeros++
		}
	}
	return zeros*100 > len(data)*10
}

// detectUTF16ByteOrder 通过零字节分布区分无 BOM 的 UTF-16LE/BE：拉丁/中文/韩文
// 文本的高字节大多为 0，LE 时落在奇数索引，BE 时落在偶数索引。
func detectUTF16ByteOrder(data []byte) byte {
	if len(data) < 2 {
		return 0
	}
	lowZeros, highZeros := 0, 0
	for i := 0; i+1 < len(data); i += 2 {
		if data[i] == 0 {
			lowZeros++
		}
		if data[i+1] == 0 {
			highZeros++
		}
	}
	switch {
	case highZeros > lowZeros:
		return 'l'
	case lowZeros > highZeros:
		return 'b'
	}
	return 0
}

func importU16LE(data []byte) []uint16 {
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
	}
	return units
}

func importU16BE(data []byte) []uint16 {
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
	}
	return units
}

func trimImportBOM(text string) string {
	return strings.TrimPrefix(text, "\uFEFF")
}

// importTextPlausible 抽样检查解码结果：U+FFFD（非法代理项的产物）占比过高，
// 说明内容并非真正的 UTF-16 文本，避免把随机二进制误判成乱码文本。
func importTextPlausible(text string) bool {
	const sample = 65536
	total, bad := 0, 0
	for _, r := range text {
		if r == '\uFFFD' {
			bad++
		}
		total++
		if total >= sample {
			break
		}
	}
	if total == 0 {
		return true
	}
	return bad*100 < total
}

func applyImportFile(a *pvf.Archive, file importFile, mode string, index int32) error {
	if mode == ImportModeText {
		return a.SetText(index, file.text)
	}
	return a.SetRawBytes(index, file.data)
}

// addImportFile 追加一个新条目，返回它在暂存归档里的索引（增量索引补丁需要）。
func addImportFile(a *pvf.Archive, file importFile, mode string) (int32, error) {
	if mode == ImportModeText {
		return a.AddFileText(file.targetPath, file.text, file.dataType)
	}
	return a.AddFile(file.targetPath, file.data, file.dataType), nil
}

// normalizeImportConflict 校验并归一化冲突处理策略；空串视为覆盖（历史默认）。
func normalizeImportConflict(conflict string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(conflict)) {
	case "", ImportConflictOverwrite:
		return ImportConflictOverwrite, nil
	case ImportConflictRename:
		return ImportConflictRename, nil
	case ImportConflictSkip:
		return ImportConflictSkip, nil
	case ImportConflictAbort:
		return ImportConflictAbort, nil
	default:
		return "", fmt.Errorf("未知的文件冲突处理策略: %q", conflict)
	}
}

// findImportConflicts 返回归档内已存在的目标路径样本（最多 5 条），供 abort 策略报错。
// 调用方须不持有 c.mu（内部自取短读锁）。
func (s *ArchiveService) findImportConflicts(files []importFile) []string {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	a := s.c.archive
	if a == nil {
		return nil
	}
	conflicts := make([]string, 0, 5)
	for _, file := range files {
		if _, exists := a.Find(file.targetPath); exists {
			conflicts = append(conflicts, file.targetPath)
			if len(conflicts) >= 5 {
				break
			}
		}
	}
	return conflicts
}

// renameImportTarget 为冲突文件生成不重名的目标路径：name.ext → name_1.ext、name_2.ext…
func renameImportTarget(a *pvf.Archive, targetPath string) string {
	dir := ""
	name := targetPath
	if slash := strings.LastIndex(targetPath, "/"); slash >= 0 {
		dir, name = targetPath[:slash], targetPath[slash+1:]
	}
	base, ext := name, ""
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		base, ext = name[:dot], name[dot:]
	}
	for i := 1; i < 100000; i++ {
		candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
		if dir != "" {
			candidate = dir + "/" + candidate
		}
		if _, exists := a.Find(candidate); !exists {
			return candidate
		}
	}
	return targetPath
}

func applyPreparedImport(a *pvf.Archive, files []importFile, mode, conflict string, job *importJob) (*ImportResult, error) {
	result := &ImportResult{ChangedPaths: []string{}, ChangedIndexes: []int32{}}
	for _, file := range files {
		// 每个文件都是取消点：大批量导入时用户能随时停手（stage 直接丢弃）。
		if err := job.tick("stage", len(result.ChangedIndexes)+result.SkippedCount, len(files), 0, false); err != nil {
			return nil, err
		}
		index, exists := a.Find(file.targetPath)
		if exists && conflict == ImportConflictSkip {
			result.SkippedCount++
			continue
		}
		if exists && conflict == ImportConflictRename {
			// 另存为不重名的新条目；写保护按最终路径判定。
			file.targetPath = renameImportTarget(a, file.targetPath)
			index, exists = a.Find(file.targetPath)
		}
		if exists && conflict == ImportConflictAbort {
			// 正常流程在扫描后已拦截；这里兜底（防止 stage 期间出现并发变化）。
			return nil, fmt.Errorf("发现同名冲突（冲突处理=终止）: %s", file.targetPath)
		}
		// 字符串表写保护（上级规则 §6.12）：整份替换禁动 .str 同样会让界面成片乱码。
		if err := guardArchiveWriteByPath(file.targetPath); err != nil {
			return nil, fmt.Errorf("导入 %q 被拦截: %w", file.targetPath, err)
		}
		if exists {
			if err := a.SetDataType(index, file.dataType); err != nil {
				return nil, fmt.Errorf("设置 %q 类型失败: %w", file.targetPath, err)
			}
			if err := applyImportFile(a, file, mode, index); err != nil {
				return nil, fmt.Errorf("导入 %q 到 %q 失败: %w", file.sourcePath, file.targetPath, err)
			}
			result.OverwrittenCount++
			result.ChangedPaths = append(result.ChangedPaths, a.Path(index))
			result.ChangedIndexes = append(result.ChangedIndexes, index)
			continue
		}

		index, err := addImportFile(a, file, mode)
		if err != nil {
			return nil, fmt.Errorf("导入 %q 到 %q 失败: %w", file.sourcePath, file.targetPath, err)
		}
		result.ImportedCount++
		result.ChangedPaths = append(result.ChangedPaths, a.Path(index))
		result.ChangedIndexes = append(result.ChangedIndexes, index)
	}
	return result, nil
}
