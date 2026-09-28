package services

import (
	"fmt"
	"strconv"
	"strings"

	"pvfine/internal/logging"
	"pvfine/internal/pvf"
	"pvfine/internal/stringguard"
	pvfversion "pvfine/internal/version"
)

// 「改写到安全表」：当显示文本落在客户端汉化禁动表（如装备的表 3、道具的表 13）时，
// 上级规则 §6.12 给的合法路径是两步——
//
//	① 把新文本写进安全表（1/5/8/27）；
//	② 把脚本里的 <禁动表号::键名> 改成 <安全表号::键名>（即改 StringLink 表号）。
//
// 只拦不做②的话，用户就改不了名字和介绍——所以这里把两步做成一次原子操作。
// 关键不变量：**脚本的 token 数不变**（只改字符串内容，不动 token 结构）。
//
// 目标表既可由调用方指定，也可传 0 表示「自动匹配」：按数据文件的偏好顺序，
// 挑第一张**当前归档里确实可写**的安全表（有些表在某个客户端里是空表，
// 内核无法向其中追加条目，例如 110US 的 String/Common.uv.str 只有 50 字节）。

// safeTableProbeKey 是探测"这张安全表能不能写入"用的假键。
// StringTableEntryIndex 对未知键会返回该表的基准 .str，因此可用来判断可写性（只读）。
const safeTableProbeKey = "pvfine_probe_missing_key"

// TargetTableAuto 是「自动匹配」的哨兵值：由后端按偏好挑一张可写的安全表。
const TargetTableAuto = 0

// StringTableGuardEntry 是白名单里的一张安全表（不含可写性，不依赖已打开的归档）。
type StringTableGuardEntry struct {
	Index int    `json:"index"`
	Path  string `json:"path"`
	Label string `json:"label"`
}

// SafeTableTarget 是「改写目标」下拉的一项：带**当前归档里的可写性**。
type SafeTableTarget struct {
	Index int    `json:"index"`
	Path  string `json:"path"`
	Label string `json:"label"`
	// Writable 表示这张表当前确实可写。
	Writable bool `json:"writable"`
	// WriteTarget 是可写时新条目会落到哪个 .str。
	WriteTarget string `json:"writeTarget,omitempty"`
	// UnwritableReason 说明不可写的原因。
	UnwritableReason string `json:"unwritableReason,omitempty"`
}

// StringTableGuardInfo 描述写保护策略（与归档无关，用于判断某表是否禁动）。
type StringTableGuardInfo struct {
	AllowedTables  []*StringTableGuardEntry `json:"allowedTables"`
	ProtectedCount int                      `json:"protectedCount"`
	Hint           string                   `json:"hint"`
	// Enabled 表示写保护总开关当前是否开启。前端据此决定要不要做界面拦截：
	// 关闭时编辑器不再把占位符编辑改道到「改写到安全表」，直接走普通「修改译文」。
	Enabled bool `json:"enabled"`
}

// SafeTableTargetsResult 是「改写目标」下拉所需的数据：白名单 + 可写性 + 自动匹配偏好。
type SafeTableTargetsResult struct {
	AllowedTables  []*SafeTableTarget `json:"allowedTables"`
	AutoPreference []int              `json:"autoPreference"`
	AutoAvailable  bool               `json:"autoAvailable"`
	ProtectedCount int                `json:"protectedCount"`
	Hint           string             `json:"hint"`
}

// SafeTableRewriteResult 是一次「禁动表 → 安全表」改写的执行结果。
type SafeTableRewriteResult struct {
	ScriptPath           string `json:"scriptPath"`
	ScriptText           string `json:"scriptText"`
	SourceTableIndex     int32  `json:"sourceTableIndex"`
	TargetTableIndex     int32  `json:"targetTableIndex"`
	TargetTablePath      string `json:"targetTablePath"`
	Key                  string `json:"key"`
	RewrittenOccurrences int    `json:"rewrittenOccurrences"`
	// AutoPicked 表示目标表是后端按「自动匹配」挑出来的。
	AutoPicked bool `json:"autoPicked"`
	// SkippedTableIndexes 是自动匹配过程中跳过的不可写表（界面据此解释为什么不是表 1）。
	SkippedTableIndexes []int `json:"skippedTableIndexes,omitempty"`
}

// StringTableGuardInfo 返回安全表白名单与提示文案（不依赖归档）。
func (s *EditorService) StringTableGuardInfo() (*StringTableGuardInfo, error) {
	guard, err := stringGuard()
	if err != nil {
		return nil, err
	}
	allowed := guard.AllowedTableNumbers()
	entries := make([]*StringTableGuardEntry, 0, len(allowed))
	for _, index := range allowed {
		path := guard.CheckTableIndex(index).TablePath
		entries = append(entries, &StringTableGuardEntry{
			Index: index,
			Path:  path,
			Label: stringTableShortLabel(path),
		})
	}
	return &StringTableGuardInfo{
		AllowedTables:  entries,
		ProtectedCount: guard.ProtectedPathCount(),
		Hint:           guard.Hint(),
		Enabled:        StringTableGuardEnabled(),
	}, nil
}

// SafeTableTargets 返回四张安全表在**当前归档**里的可写性，以及自动匹配偏好。
func (s *EditorService) SafeTableTargets() (*SafeTableTargetsResult, error) {
	guard, err := stringGuard()
	if err != nil {
		return nil, err
	}
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	archive := s.c.archive
	if archive == nil {
		return nil, ErrNoArchive
	}

	allowed := guard.AllowedTableNumbers()
	entries := make([]*SafeTableTarget, 0, len(allowed))
	writable := make(map[int]bool, len(allowed))
	for _, index := range allowed {
		entry := buildSafeTableTarget(archive, guard, index)
		entries = append(entries, entry)
		writable[index] = entry.Writable
	}
	preference := guard.AutoTargetPreference()
	autoAvailable := false
	for _, candidate := range preference {
		if writable[candidate] {
			autoAvailable = true
			break
		}
	}
	return &SafeTableTargetsResult{
		AllowedTables:  entries,
		AutoPreference: preference,
		AutoAvailable:  autoAvailable,
		ProtectedCount: guard.ProtectedPathCount(),
		Hint:           guard.Hint(),
	}, nil
}

// RewritePlaceholderToSafeTable 把一条占位符显示文本从禁动表改写到安全表。
//
// targetTableIndex 传 TargetTableAuto(0) 表示按偏好自动挑一张可写的安全表。
func (s *EditorService) RewritePlaceholderToSafeTable(
	index int32, sourceTableIndex int32, key string, text string, targetTableIndex int32,
) (*SafeTableRewriteResult, error) {
	return s.c.rewritePlaceholderToSafeTable(index, sourceTableIndex, key, text, targetTableIndex)
}

// buildSafeTableTarget 判定一张安全表在当前归档里是否可写。
func buildSafeTableTarget(archive *pvf.Archive, guard *stringguard.Guard, index int) *SafeTableTarget {
	path := guard.CheckTableIndex(index).TablePath
	target := &SafeTableTarget{Index: index, Path: path, Label: stringTableShortLabel(path)}
	fileIndex, ok := archive.StringTableEntryIndex(index, safeTableProbeKey)
	if !ok {
		target.UnwritableReason = "该表在当前归档里解析不到任何条目（常见于空表），内核无法向其中追加新条目"
		return target
	}
	target.Writable = true
	target.WriteTarget = archive.Path(fileIndex)
	return target
}

// stringTableShortLabel 从 `String/Common.uv.str` 取出可读短名 `Common`。
func stringTableShortLabel(path string) string {
	trimmed := strings.Trim(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "/")
	if slash := strings.LastIndexByte(trimmed, '/'); slash >= 0 {
		trimmed = trimmed[slash+1:]
	}
	lower := strings.ToLower(trimmed)
	for _, suffix := range []string{".uv.str", ".kor.str", ".translate.str", ".str"} {
		if strings.HasSuffix(lower, suffix) {
			return trimmed[:len(trimmed)-len(suffix)]
		}
	}
	return trimmed
}

// autoSafeTableLocked 按偏好挑第一张可写的安全表，并回报跳过的表号。
// 调用方必须持有 c.mu。
func (c *core) autoSafeTableLocked() (int32, []int, error) {
	guard, err := stringGuard()
	if err != nil {
		return 0, nil, err
	}
	preference := guard.AutoTargetPreference()
	skipped := make([]int, 0, len(preference))
	for _, candidate := range preference {
		if _, ok := c.archive.StringTableEntryIndex(candidate, safeTableProbeKey); ok {
			return int32(candidate), skipped, nil
		}
		skipped = append(skipped, candidate)
	}
	return 0, skipped, fmt.Errorf(
		"当前归档里没有可写入的安全表（已尝试表 %s）；可能是这些表都是空表，请改用手动选择其它表。",
		joinInts(preference))
}

func joinInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, "/")
}

func (c *core) rewritePlaceholderToSafeTable(
	index int32, sourceTableIndex int32, key, text string, targetTableIndex int32,
) (*SafeTableRewriteResult, error) {
	key = strings.TrimSpace(key)
	text = strings.TrimSpace(text)
	guard, err := stringGuard()
	if err != nil {
		return nil, err
	}
	switch {
	case key == "":
		return nil, fmt.Errorf("键名不能为空")
	case text == "":
		return nil, fmt.Errorf("译文不能为空")
	}

	c.mu.Lock()
	if c.archive == nil {
		c.mu.Unlock()
		return nil, ErrNoArchive
	}
	// 目标可为「自动匹配」：挑一张当前确实可写的安全表。
	autoPicked := false
	var skipped []int
	if targetTableIndex == TargetTableAuto {
		picked, skippedTables, pickErr := c.autoSafeTableLocked()
		if pickErr != nil {
			c.mu.Unlock()
			return nil, pickErr
		}
		targetTableIndex = picked
		skipped = skippedTables
		autoPicked = true
	}
	if targetTableIndex == sourceTableIndex {
		c.mu.Unlock()
		return nil, fmt.Errorf("源表与目标表相同（表 %d），请直接使用「修改译文」", sourceTableIndex)
	}
	// 目标表必须在白名单内——本接口同样受写保护约束，不能拿它写禁动表。
	if err := guardStringTableWriteByIndex(int(targetTableIndex)); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	// 源表必须是确实被拦下的禁动表，否则调用方应当走普通「修改译文」。
	//
	// 这里走**事实查询** isSafeStringTable，而不是 guardStringTableWriteByIndex：
	// 后者在总开关关闭时会一律放行，会把"源表本就在安全表里"的调用误判成可改写。
	sourceIsSafe, sourceErr := isSafeStringTable(int(sourceTableIndex))
	if sourceErr != nil {
		// 清单不可用时保持既有行为：无法确认"源表在白名单内"就按非安全表继续，
		// 由目标表白名单与脚本路径判定兜底。
		logging.For("stringguard").Warn("安全表清单不可用，跳过源表白名单校验", "错误", sourceErr.Error())
	}
	if sourceIsSafe {
		c.mu.Unlock()
		return nil, fmt.Errorf("表 %d 已在安全表白名单内，直接使用「修改译文」即可", sourceTableIndex)
	}
	if err := c.ensureVersionReadyLocked(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if index < 0 || index >= c.archive.FileCount() {
		c.mu.Unlock()
		return nil, fmt.Errorf("文件索引越界: %d", index)
	}
	scriptPath := c.archive.Path(index)
	if err := guardArchiveWriteByPath(scriptPath); err != nil {
		c.mu.Unlock()
		return nil, err
	}

	// 用编辑器当前文本（含未保存草稿）；没有草稿才回落到归档文本。
	scriptText, ok := c.editorText[index]
	if !ok {
		read, readErr := c.archive.Text(index)
		if readErr != nil {
			c.mu.Unlock()
			return nil, readErr
		}
		scriptText = read
	}
	sourceToken := "<" + strconv.Itoa(int(sourceTableIndex)) + "::" + key + ">"
	targetToken := "<" + strconv.Itoa(int(targetTableIndex)) + "::" + key + ">"
	occurrences := strings.Count(scriptText, sourceToken)
	if occurrences == 0 {
		c.mu.Unlock()
		return nil, fmt.Errorf("脚本里没有找到占位符 %s，无法改写", sourceToken)
	}
	rewritten := strings.ReplaceAll(scriptText, sourceToken, targetToken)

	// 目标条目落在哪个 .str：键已存在则原地改，不存在则由内核追加到基准表。
	targetFileIndex, ok := c.archive.StringTableEntryIndex(int(targetTableIndex), key)
	if !ok {
		c.mu.Unlock()
		return nil, fmt.Errorf(
			"表 %d（%s）在当前归档里不可写入：该 .str 解析不到任何条目（常见于空表）。"+
				"请改选其它安全表，或把目标设为「自动匹配」。",
			targetTableIndex, guard.CheckTableIndex(int(targetTableIndex)).TablePath)
	}
	targetPath := c.archive.Path(targetFileIndex)

	// 版本簿记：脚本与目标 .str 一起快照，保证改动清单与回滚都能看到这两处变化。
	var before pvfversion.ContentSnapshot
	if c.versionRepo != nil {
		snapshot, snapErr := pvfversion.ContentSnapshotFromArchive(c.archive, []string{scriptPath, targetPath})
		if snapErr != nil {
			c.mu.Unlock()
			return nil, snapErr
		}
		before = snapshot
	}

	// 顺序很关键：先写安全表，再改脚本。
	// 若先改脚本而表写失败，客户端会读到一个没有文本的占位符（显示空白）；
	// 反过来只完成一半，留下的是一条没人引用的表项（无害）。
	if _, err := c.archive.SetStringTableEntry(int(targetTableIndex), key, text); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if err := c.archive.SetText(index, rewritten); err != nil {
		c.mu.Unlock()
		return nil, err
	}

	if c.versionRepo != nil {
		after, snapErr := pvfversion.ContentSnapshotFromArchive(c.archive, []string{scriptPath, targetPath})
		if snapErr != nil {
			c.mu.Unlock()
			return nil, snapErr
		}
		if err := c.recordVersionMutationLocked("改写为安全表", before, after); err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}

	c.batchRevision++
	c.batchPlan = nil
	c.invalidateScriptLocked()
	versioned := c.versionRepo != nil
	if c.editorText == nil {
		c.editorText = make(map[int32]string)
	}
	c.editorText[index] = rewritten
	c.resetAnnotationCachesLocked()
	c.editorAnnotation = editorAnnotationCache{}
	c.markAdvancedSearchDirtyLocked(index)
	delete(c.visualsByFile, index)
	if c.indexDirty == nil {
		c.indexDirty = make(map[int32]struct{})
	}
	c.indexDirty[index] = struct{}{}
	c.indexDirty[targetFileIndex] = struct{}{}
	ready := c.indexStatus.State == IndexStateReady && c.searchRecords != nil
	c.mu.Unlock()

	// 字符串表变了，名称与搜索索引需要整表刷新（与 setPlaceholderText 一致）。
	if ready {
		c.startSearchIndexForced()
	}
	emitEvent("archive:advanced-search-stale", map[string]any{"fileIndex": index})
	if versioned {
		emitVersionState(c, "edited")
	}
	return &SafeTableRewriteResult{
		ScriptPath:           scriptPath,
		ScriptText:           rewritten,
		SourceTableIndex:     sourceTableIndex,
		TargetTableIndex:     targetTableIndex,
		TargetTablePath:      targetPath,
		Key:                  key,
		RewrittenOccurrences: occurrences,
		AutoPicked:           autoPicked,
		SkippedTableIndexes:  skipped,
	}, nil
}
