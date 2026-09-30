package services

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"pvfine/internal/logging"
	"pvfine/internal/pvf"
)

// bigTextBytes / bigTextLines 是「反编译后文本」规模的大文件阈值，与 bigFileBytes
//（归档内原始体积）并用：归档内小 ≠ 展开后小，前端靠 LargeFile 决定是否改走
//「连续全文 TXT」通道（见 services/large_text.go），漏判会让前端带着几百万字符
// 去挂编辑器而卡死。
const (
	bigTextBytes = 4 << 20 // 展开后 4MB
	bigTextLines = 100000  // 或 10 万行
)

// bigFileBytes:超过该大小的文本文件按「大文件降级」处理——前端关闭折行/空白高亮，
// 避免几十万行的文件把编辑器拖死；**不再有任何读写限制**（2026-09-28 用户要求）。
const bigFileBytes = 8 << 20

// EditorService: 文件内容读取、内存编辑、保存/另存为、导出与整包解包。
type EditorService struct {
	c        *core
	settings *SettingsService
}

func NewEditorService(c *core, settings ...*SettingsService) *EditorService {
	service := &EditorService{c: c}
	if len(settings) > 0 {
		service.settings = settings[0]
	}
	return service
}

// FileMeta 返回给前端的单个文件视图。
type FileMeta struct {
	Index       int32              `json:"index"`
	Path        string             `json:"path"`
	DataType    int32              `json:"dataType"`
	Size        int32              `json:"size"`
	Tags        []TreeTag          `json:"tags,omitempty"`
	Editable    bool               `json:"editable"`
	// LargeFile 标记「超过 bigFileBytes 的大文件」：前端据此二次确认并降级渲染。
	LargeFile bool `json:"largeFile"`
	// TextOmitted 表示该文件的文本**有意没有下发**（见 GetFile 里的大文件分支）：
	// 前端据 largeFile 显示「大文件卡片」，不要把它当成空文件。
	TextOmitted bool   `json:"textOmitted"`
	Text        string `json:"text"`
	Modified    bool               `json:"modified"`
	Annotations []EditorAnnotation `json:"annotations,omitempty"`
	Icon        *ImageReference    `json:"icon,omitempty"`
	FieldImage  *ImageReference    `json:"fieldImage,omitempty"`
}

// GetFile 返回文件的反编译文本(内存编辑视图)。
func (s *EditorService) GetFile(index int32) (*FileMeta, error) {
	// 标记为前台请求：后台索引构建会让路，点击打开不再被 70 秒的构建拖住。
	s.c.beginFront()
	defer s.c.endFront()
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	a := s.c.archive
	if a == nil {
		return nil, ErrNoArchive
	}
	if err := validateAnnotationIndex(a, index); err != nil {
		return nil, err
	}
	f := a.File(index)
	meta := &FileMeta{
		Index:    index,
		Path:     a.Path(index),
		DataType: f.DataType,
		Size:     f.DataSize,
		Tags:     cloneTreeTags(s.c.treeTagsByFile[index]),
		Editable: false,
	}
	visuals := s.c.fileVisualsLocked(index)
	meta.Icon = cloneImageReference(visuals.icon)
	meta.FieldImage = cloneImageReference(visuals.fieldImage)
	switch f.DataType {
	case pvf.TypeScript, pvf.TypeUnicode:
		// 只对"文本类"文件打大文件标记：图片/二进制等大文件不需要降级渲染，
		// 避免前端误以为它是"超过上限的文本"而给出错误提示。
		if f.DataSize > bigFileBytes {
			meta.LargeFile = true
		}
		// 追踪必须从解码之前开始：反编译（4MB → 27MB）是最贵的一步，若计时起点在
		// 它之后，日志里的「读取文本」会永远显示 0s（2026-09-28 修复的计时陷阱）。
		stage := traceBegin(index, meta.Path)
		steps := map[string]string{}
		readStart := time.Now()
		text, ok := s.c.editorText[index]
		if !ok {
			var err error
			// 未修改文件的解码结果可复用：大清单解码要几百毫秒，反复打开/预览
			// 不必每次重做（改过的内容走 editorText，不进这个缓存）。
			text, err = s.c.cachedDecodedText(index, a)
			if err != nil {
				traceStep(stage, "failed")
				return nil, err
			}
		}
		steps["读取文本"] = logging.FormatDuration(time.Since(readStart))
		meta.Editable = true
		meta.Text = text
		// 反编译文本可能远大于归档内原始体积（stackable.lst 原始 1.4MB → 展开后
		// 719 万字符、14 万行）：LargeFile 只按 DataSize 判会漏掉这类文件，前端就会
		// 带着几百万字符去跑语法解析 → 打开即卡死。按展开后的体积/行数补判一次。
		rows := strings.Count(text, "\n") + 1
		if !meta.LargeFile && (int64(len(text)) > bigTextBytes || rows > bigTextLines) {
			meta.LargeFile = true
		}
		if meta.LargeFile {
			// 超大文本**不下发**（2026-09-30 实测）：list/equipment.lst 解码后 2740 万字符，
			// 光是把它送进窗口就让前端停摆 43 秒、之后每 11 秒一轮（与编辑器、扩展、
			// 渲染行数都无关；空文档挂载 CM 也要 30 秒）。这类文件改走「连续全文 TXT」
			// 通道（GetFileLines/SetFileLines 按视口取行），因此这里只保留判定：
			// 文本与注解都不给前端，前端据 largeFile 走 TXT 视图。
			// 解码结果仍进缓存，供按行取数与保存链路复用。
			meta.Text = ""
			meta.TextOmitted = true
			meta.Annotations = nil
			meta.Editable = true
			traceStep(stage, "omitted")
			traceDone(stage, OpenTrace{
				Index: index, Path: meta.Path, Bytes: int64(f.DataSize), Lines: rows,
				StepMs: steps, Skipped: "文本不下发(大文件)",
			})
			meta.Modified = a.IsModified(index)
			return meta, nil
		}
		if f.DataType == pvf.TypeScript {
			traceStep(stage, "annotations")
			annotStart := time.Now()
			annotations, err := s.c.editorAnnotationsLocked(index, text)
			if err != nil {
				traceStep(stage, "failed")
				return nil, err
			}
			steps["标注解析"] = logging.FormatDuration(time.Since(annotStart))
			skipped := ""
			if rows > annotationRowLimit {
				skipped = fmt.Sprintf("行数>%d", annotationRowLimit)
			}
			meta.Annotations = annotations
			traceStep(stage, "done")
			traceDone(stage, OpenTrace{
				Index: index, Path: meta.Path, Bytes: int64(f.DataSize), Lines: rows,
				StepMs: steps, Annotations: len(annotations), Skipped: skipped,
			})
		} else {
			traceStep(stage, "done")
			traceDone(stage, OpenTrace{
				Index: index, Path: meta.Path, Bytes: int64(f.DataSize), Lines: rows,
				StepMs: steps,
			})
		}
	default:
		meta.Text = fmt.Sprintf("; 不支持的类型 %d(v1 仅支持脚本/文本编辑)", f.DataType)
	}
	meta.Modified = a.IsModified(index)
	return meta, nil
}

// GetAnnotations recomputes annotations against the exact current editor text.
func (s *EditorService) GetAnnotations(index int32) ([]EditorAnnotation, error) {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if s.c.archive == nil {
		return nil, ErrNoArchive
	}
	if err := validateAnnotationIndex(s.c.archive, index); err != nil {
		return nil, err
	}
	if s.c.archive.File(index).DataType != pvf.TypeScript {
		return []EditorAnnotation{}, nil
	}
	text, ok := s.c.editorText[index]
	if !ok {
		var err error
		text, err = s.c.archive.Text(index)
		if err != nil {
			return nil, err
		}
	}
	return s.c.editorAnnotationsLocked(index, text)
}

// SetText 把编辑后的文本写入内存 overlay(不落盘)。
func (s *EditorService) SetText(index int32, text string) error {
	_, _, err := s.c.setText(index, text)
	return err
}

// SetPlaceholderText rewrites the display text behind one `<table::key>`
// placeholder of a script. index is the script being edited; tableIndex and key
// come from the placeholder annotation. Only the `.str` payload changes — the
// script keeps its placeholder, so no stored script data is altered.
func (s *EditorService) SetPlaceholderText(index int32, tableIndex int32, key string, text string) error {
	return s.c.setPlaceholderText(index, tableIndex, key, text)
}

// Save 把全部内存修改写回源文件(原子写:临时文件 + rename)。
func (s *EditorService) Save() (ArchiveInfo, error) {
	s.c.mu.Lock()
	a := s.c.archive
	if a == nil {
		s.c.mu.Unlock()
		return ArchiveInfo{}, ErrNoArchive
	}
	if err := s.c.ensureVersionReadyLocked(); err != nil {
		s.c.mu.Unlock()
		return ArchiveInfo{}, err
	}
	if a.SourcePath() == "" {
		s.c.mu.Unlock()
		return ArchiveInfo{}, fmt.Errorf("归档没有源文件,请使用另存为")
	}
	if s.shouldBackupSource() {
		backupStart := time.Now()
		if err := backupSourceFile(a.SourcePath()); err != nil {
			logging.For("save").Error("源文件备份失败", "错误", err.Error())
			s.c.mu.Unlock()
			return ArchiveInfo{}, err
		}
		logging.For("save").Info("源文件备份完成",
			"耗时", logging.FormatDuration(time.Since(backupStart)))
	}
	saveStart := time.Now()
	if err := a.Save(); err != nil {
		logging.For("save").Error("保存归档失败", "错误", err.Error())
		s.c.mu.Unlock()
		return ArchiveInfo{}, err
	}
	logging.For("save").Info("归档已保存",
		"文件", a.SourcePath(), "耗时", logging.FormatDuration(time.Since(saveStart)))
	if err := s.c.markVersionArtifactSavedLocked(a.SourcePath()); err != nil {
		s.c.mu.Unlock()
		return ArchiveInfo{}, err
	}
	info := a.Info()
	s.c.mu.Unlock()
	s.c.persistCurrentSearchIndexCacheAsync()
	emitEvent("archive:saved", info)
	return info, nil
}

func (s *EditorService) shouldBackupSource() bool {
	if s.settings == nil {
		return true
	}
	settings, err := s.settings.GetSettings()
	if err != nil {
		// 读取设置失败时使用安全默认值,不要因为配置文件问题阻止保存。
		return true
	}
	return settings.BackupSourceOnSave
}

func backupSourceFile(sourcePath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("打开源文件以创建备份失败: %w", err)
	}
	defer source.Close()

	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("读取源文件信息失败: %w", err)
	}

	backupPath := sourcePath + ".bak"
	temp, err := os.CreateTemp(filepath.Dir(backupPath), "."+filepath.Base(backupPath)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("创建源文件备份临时文件失败: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("设置源文件备份权限失败: %w", err)
	}
	if _, err := io.CopyBuffer(temp, source, make([]byte, 1<<20)); err != nil {
		_ = temp.Close()
		return fmt.Errorf("写入源文件备份失败: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("同步源文件备份失败: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭源文件备份失败: %w", err)
	}
	if err := os.Rename(tempPath, backupPath); err != nil {
		return fmt.Errorf("替换源文件备份失败: %w", err)
	}
	return nil
}

// SaveAsDialog 弹出保存对话框并另存为新 PVF。返回保存路径。
func (s *EditorService) SaveAsDialog() (string, error) {
	s.c.mu.RLock()
	a := s.c.archive
	if a == nil {
		s.c.mu.RUnlock()
		return "", ErrNoArchive
	}
	s.c.mu.RUnlock()
	defName := "Script_new.pvf"
	if src := a.SourcePath(); src != "" {
		defName = filepath.Base(src)
	}
	path, err := application.Get().Dialog.SaveFile().
		SetFilename(defName).
		AddFilter("PVF 归档", "*.pvf").
		SetMessage("另存为 PVF").
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil // 用户取消
	}
	s.c.mu.Lock()
	if s.c.archive != a {
		s.c.mu.Unlock()
		return "", ErrNoArchive
	}
	if err := s.c.ensureVersionReadyLocked(); err != nil {
		s.c.mu.Unlock()
		return "", err
	}
	if err := a.SaveAs(path); err != nil {
		s.c.mu.Unlock()
		return "", err
	}
	info := a.Info()
	s.c.mu.Unlock()
	s.c.persistCurrentSearchIndexCacheAsync()
	emitEvent("archive:saved", info)
	return path, nil
}

type exportSelection struct {
	index int32
	path  string
}

// exportTimestampDir 生成导出用的顶层文件夹名：<时间戳><kind>，例如
// 「2026年9月27日10.15.20文件导出」/「…改动文件导出」。时分秒用点号分隔，
// 天然规避 Windows 文件名非法字符。
func exportTimestampDir(kind string) string {
	return time.Now().Format("2006年1月2日15.04.05") + kind
}

// ExportFilesDialog 将选中的文件或目录导出到目标目录,文件内容使用渲染后的 UTF-8 文本。
// 目录会递归展开,并保留归档内的相对路径。目标目录用系统原生对话框选择,
// 实际写入 <所选目录>\<时间戳>文件导出\。
func (s *EditorService) ExportFilesDialog(scopes []string) (string, error) {
	a, selections, err := s.prepareExport(scopes)
	if err != nil {
		return "", err
	}
	dir, err := application.Get().Dialog.OpenFile().
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true).
		SetTitle("选择导出目标目录").
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", nil
	}
	return s.exportTo(filepath.Join(dir, exportTimestampDir("文件导出")), a, selections)
}

// ExportFilesTo 把 scopes 导出到 dir 下新建的「<时间戳><kind>」文件夹,返回实际写入的目录。
// 目标目录由前端自绘的目录选择器给出；kind 为空时按「文件导出」命名。
func (s *EditorService) ExportFilesTo(dir string, scopes []string, kind string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", fmt.Errorf("未选择导出目录")
	}
	if strings.TrimSpace(kind) == "" {
		kind = "文件导出"
	}
	a, selections, err := s.prepareExport(scopes)
	if err != nil {
		return "", err
	}
	return s.exportTo(filepath.Join(dir, exportTimestampDir(kind)), a, selections)
}

// prepareExport 把 scopes 解析为待写出的条目清单,同时返回解析时刻的活动归档
// （供写盘前的会话守卫用,避免归档被换掉后写到别的文件上）。
func (s *EditorService) prepareExport(scopes []string) (*pvf.Archive, []exportSelection, error) {
	s.c.mu.RLock()
	a := s.c.archive
	if a == nil {
		s.c.mu.RUnlock()
		return nil, nil, ErrNoArchive
	}
	selections := collectExportSelections(a, s.c.sortedPaths, scopes)
	s.c.mu.RUnlock()
	if len(selections) == 0 {
		return nil, nil, fmt.Errorf("没有可导出的文件")
	}
	return a, selections, nil
}

// exportTo 建好 base 目录并把条目按归档内相对路径写入,返回 base。
func (s *EditorService) exportTo(base string, a *pvf.Archive, selections []exportSelection) (string, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	if s.c.archive != a {
		return "", ErrNoArchive
	}
	for _, selection := range selections {
		text, err := a.Text(selection.index)
		if err != nil {
			return "", fmt.Errorf("渲染 %q 失败: %w", selection.path, err)
		}
		dst, err := safeExportPath(base, selection.path)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			return "", err
		}
	}
	return base, nil
}

func collectExportSelections(a *pvf.Archive, sortedPaths []pathEntry, scopes []string) []exportSelection {
	selections := make([]exportSelection, 0, len(scopes))
	seenPaths := make(map[string]struct{}, len(scopes))
	add := func(index int32, path string) {
		path = normalizeExportPath(path)
		if path == "" {
			return
		}
		if _, exists := seenPaths[path]; exists {
			return
		}
		seenPaths[path] = struct{}{}
		selections = append(selections, exportSelection{index: index, path: path})
	}

	for _, rawScope := range scopes {
		scope := normalizeExportPath(rawScope)
		if scope == "" {
			continue
		}
		if index, ok := a.Find(scope); ok {
			add(index, a.Path(index))
			continue
		}

		prefix := scope + "/"
		start := sort.Search(len(sortedPaths), func(i int) bool {
			return sortedPaths[i].path >= prefix
		})
		for _, entry := range sortedPaths[start:] {
			if !strings.HasPrefix(entry.path, prefix) {
				break
			}
			add(entry.idx, entry.path)
		}
	}
	return selections
}

func normalizeExportPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	return strings.Trim(path, "/")
}

func safeExportPath(base, internal string) (string, error) {
	parts := strings.Split(strings.ReplaceAll(internal, "\\", "/"), "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("导出路径越界: %q", internal)
		}
		cleaned = append(cleaned, sanitizeExportName(part))
	}
	if len(cleaned) == 0 {
		return "", fmt.Errorf("导出路径为空: %q", internal)
	}
	return filepath.Join(append([]string{base}, cleaned...)...), nil
}

func sanitizeExportName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return '_'
		case strings.ContainsRune(`<>:"|?*`, r):
			return '_'
		default:
			return r
		}
	}, name)
}

// UnpackDialog 选择目录后,在后台协程把整包解包到该目录。
// 进度通过事件 "unpack:progress" {done,total} 推送,结束发 "unpack:done"。
func (s *EditorService) UnpackDialog() (bool, error) {
	if !s.c.unpackRunning.CompareAndSwap(false, true) {
		return false, fmt.Errorf("解包正在进行中")
	}
	s.c.mu.RLock()
	a := s.c.archive
	s.c.mu.RUnlock()
	if a == nil {
		s.c.unpackRunning.Store(false)
		return false, ErrNoArchive
	}
	dir, err := application.Get().Dialog.OpenFile().
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true).
		SetTitle("选择解包目标目录").
		PromptForSingleSelection()
	if err != nil {
		s.c.unpackRunning.Store(false)
		return false, err
	}
	if dir == "" {
		s.c.unpackRunning.Store(false)
		return false, nil // 用户取消
	}

	s.c.unpackCancel.Store(false)
	go func() {
		defer s.c.unpackRunning.Store(false)
		emit := application.Get().Event.Emit
		total := int(a.FileCount())
		progress := func(done, tot int) { emit("unpack:progress", map[string]int{"done": done, "total": tot}) }
		cancel := func() bool { return s.c.unpackCancel.Load() }

		err := a.ExtractTo(dir, progress, cancel)
		if err == pvf.ErrCancelled {
			emit("unpack:done", map[string]any{"ok": false, "message": "解包已取消", "dir": dir})
			return
		}
		if err != nil {
			emit("unpack:done", map[string]any{"ok": false, "message": "解包失败: " + err.Error(), "dir": dir})
			return
		}
		emit("unpack:done", map[string]any{"ok": true, "message": fmt.Sprintf("已解包 %d 个文件到 %s", total, dir), "dir": dir})
	}()
	return true, nil
}

// CancelUnpack 请求中止进行中的解包。
func (s *EditorService) CancelUnpack() {
	s.c.unpackCancel.Store(true)
}

// IsUnpacking 报告解包是否进行中。
func (s *EditorService) IsUnpacking() bool { return s.c.unpackRunning.Load() }
