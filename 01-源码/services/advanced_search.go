package services

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"pvfine/internal/pvf"
)

const (
	AdvancedSearchModeBinary = "binary"
	AdvancedSearchModeString = "string"

	AdvancedIndexStateIdle     = "idle"
	AdvancedIndexStateBuilding = "building"
	AdvancedIndexStateReady    = "ready"
	AdvancedIndexStateError    = "error"
)

var ErrAdvancedSearchIndexing = errors.New("高级搜索字符串索引正在构建")

// ErrAdvancedSearchDisabled 表示"跨归档正文检索"已被停用。
//
// 【2026-10-06 用户要求：取消建大索引】实测那份字符串池索引要 438 万条目、常驻约 4.9GB
// （本机总内存 7.1GB），一旦构建就把整机拖垮（文件树、打开文件全部转圈）。
// 用户明确要求取消 ⇒ 这里直接拒绝，任何调用者（面板 / 以后新增的入口）都无法再触发构建。
// 正文搜索改走两条**不建索引**的路：① Ctrl+Shift+F「当前文件搜索」
// ② 高级搜索面板里的「范围扫描」（按目录圈文件后逐个文件分页扫，随时可停）。
var ErrAdvancedSearchDisabled = errors.New("跨归档正文检索已停用（原索引约 4.9GB，已按你的要求取消建索引）；请改用「当前文件搜索」或面板里的「范围扫描」")

// AdvancedSearchIndexStatus describes the lazy string reverse-index state.
type AdvancedSearchIndexStatus struct {
	State string `json:"state"`
	Stage string `json:"stage"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Error string `json:"error"`
}

// AdvancedSearchHit is one file-level advanced-search result.
type AdvancedSearchHit struct {
	Name      string                  `json:"name,omitempty"`
	Path      string                  `json:"path"`
	Size      int32                   `json:"size"`
	DataType  int32                   `json:"dataType"`
	FileIndex int32                   `json:"fileIndex"`
	Details   []*AdvancedSearchDetail `json:"details,omitempty"`
}

// AdvancedSearchDetail explains why a file matched the query.
type AdvancedSearchDetail struct {
	Kind             string   `json:"kind"`
	Value            string   `json:"value,omitempty"`
	Pool             string   `json:"pool,omitempty"`
	PoolOffset       int32    `json:"poolOffset,omitempty"`
	Occurrences      int      `json:"occurrences"`
	TokenTypes       []int32  `json:"tokenTypes,omitempty"`
	FileFields       []string `json:"fileFields,omitempty"`
	ByteOffsets      []int    `json:"byteOffsets,omitempty"`
	TokenOffsets     []int    `json:"tokenOffsets,omitempty"`
	Hex              string   `json:"hex,omitempty"`
	OffsetsTruncated bool     `json:"offsetsTruncated,omitempty"`
	// Line 是命中内容在**反编译文本**里的 1 基行号（0 = 没算出来/算不到）。
	//
	// 为什么在分页出口才算：字符串索引是"池值维度"的（只记哪些文件引用了该池值），
	// **不存文件内偏移**，所以行号只能拿文本正查一次 Value 得到；放索引阶段会拖慢索引
	// 且要改持久化格式。只对用户要看的那一页算，代价 ≈ 页内涉及文件各一次解码（走缓存）。
	Line int32 `json:"line,omitempty"`
}

// AdvancedSearchResult is a paged file-level advanced-search response.
type AdvancedSearchResult struct {
	Hits       []*AdvancedSearchHit `json:"hits"`
	NextCursor int                  `json:"nextCursor"`
	Scanned    int                  `json:"scanned"`
}

type advancedFileMatch struct {
	fileIndex int32
	details   []*AdvancedSearchDetail
}

// advancedHitView is a scope-filtered, path-sorted view of file matches.
type advancedHitView struct {
	matches []advancedFileMatch
}

type advancedQueryKey struct {
	mode  string
	query string
	regex bool
	scope string
}

// AdvancedIndexStatus returns the lazy string reverse-index status.
func (s *ArchiveService) AdvancedIndexStatus() AdvancedSearchIndexStatus {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	return s.c.advancedStatus
}

// AdvancedSearch searches raw token bytes or string-pool references.
// cursor is the result offset from the previous response; limit is 1..1000.
func (s *ArchiveService) AdvancedSearch(mode, query, scopePath string, regex bool, cursor, limit int) (*AdvancedSearchResult, error) {
	result := &AdvancedSearchResult{Hits: []*AdvancedSearchHit{}, NextCursor: -1}
	query = strings.TrimSpace(query)
	if query == "" {
		return result, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if cursor < 0 {
		cursor = 0
	}
	scope := normalizeAdvancedScope(scopePath)

	switch strings.ToLower(strings.TrimSpace(mode)) {
	case AdvancedSearchModeBinary:
		return s.searchAdvancedBinary(query, scope, cursor, limit)
	case AdvancedSearchModeString:
		// 【2026-10-06 用户要求：取消建大索引】字符串池索引（438 万条目 / 常驻 4.9GB）不再构建，
		// 直接拒绝 ⇒ 不会再有"搜一次卡死整机"的情况。正文搜索请走不建索引的两条路
		// （Ctrl+Shift+F 当前文件搜索 / 面板的「范围扫描」），见 ErrAdvancedSearchDisabled 注释。
		return nil, ErrAdvancedSearchDisabled
	default:
		return nil, fmt.Errorf("未知高级搜索模式: %s", mode)
	}
}

func (s *ArchiveService) searchAdvancedString(query, scope string, regex bool, cursor, limit int) (*AdvancedSearchResult, error) {
	if regex {
		if _, err := regexp.Compile(query); err != nil {
			return nil, fmt.Errorf("正则表达式无效: %w", err)
		}
	}
	index, err := s.c.ensureAdvancedStringIndex()
	if err != nil {
		return nil, err
	}
	key := advancedQueryKey{mode: AdvancedSearchModeString, query: query, regex: regex, scope: scope}
	view := s.c.lookupAdvancedView(key)
	if view == nil {
		poolMatches, err := index.MatchDetails(query, regex)
		if err != nil {
			return nil, fmt.Errorf("匹配字符串池失败: %w", err)
		}
		byFile := make(map[int32][]*AdvancedSearchDetail)
		for _, match := range poolMatches {
			byFile[match.FileIndex] = append(byFile[match.FileIndex], &AdvancedSearchDetail{
				Kind:        "string",
				Value:       match.Value,
				Pool:        match.Pool,
				PoolOffset:  match.Offset,
				Occurrences: match.Occurrences,
				TokenTypes:  append([]int32(nil), match.TokenTypes...),
				FileFields:  append([]string(nil), match.FileFields...),
			})
		}
		matches := make([]advancedFileMatch, 0, len(byFile))
		for fileIndex, details := range byFile {
			matches = append(matches, advancedFileMatch{fileIndex: fileIndex, details: details})
		}
		view = s.c.buildAndCacheAdvancedView(key, matches, scope)
	}
	return s.paginateAdvancedView(view.matches, cursor, limit)
}

func (s *ArchiveService) searchAdvancedBinary(query, scope string, cursor, limit int) (*AdvancedSearchResult, error) {
	key := advancedQueryKey{mode: AdvancedSearchModeBinary, query: query, regex: false, scope: scope}
	if view := s.c.lookupAdvancedView(key); view != nil {
		return s.paginateAdvancedView(view.matches, cursor, limit)
	}

	s.c.mu.RLock()
	if s.c.archive == nil {
		s.c.mu.RUnlock()
		return nil, ErrNoArchive
	}
	a := s.c.archive
	pattern, err := a.EncodeScriptQuery(query)
	s.c.mu.RUnlock()
	if errors.Is(err, pvf.ErrQueryStringNotInPool) {
		return &AdvancedSearchResult{Hits: []*AdvancedSearchHit{}, NextCursor: -1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("编译二进制查询失败: %w", err)
	}
	if len(pattern) == 0 {
		return &AdvancedSearchResult{Hits: []*AdvancedSearchHit{}, NextCursor: -1}, nil
	}

	matched, err := s.c.binaryMatches(string(pattern), a)
	if err != nil {
		return nil, err
	}
	matches := make([]advancedFileMatch, 0, len(matched))
	for _, match := range matched {
		matches = append(matches, match)
	}
	view := s.c.buildAndCacheAdvancedView(key, matches, scope)
	return s.paginateAdvancedView(view.matches, cursor, limit)
}

func (c *core) lookupAdvancedView(key advancedQueryKey) *advancedHitView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.advancedQueryCache == nil {
		return nil
	}
	return c.advancedQueryCache[key]
}

// buildAndCacheAdvancedView filters matches by scope, sorts them by path, and
// caches the result. The generation guard discards the cache entry if an edit
// raced with the build.
func (c *core) buildAndCacheAdvancedView(key advancedQueryKey, matches []advancedFileMatch, scope string) *advancedHitView {
	c.mu.RLock()
	a := c.archive
	if a == nil {
		c.mu.RUnlock()
		return &advancedHitView{matches: []advancedFileMatch{}}
	}
	filtered := make([]advancedFileMatch, 0, len(matches))
	for _, match := range matches {
		index := match.fileIndex
		if index < 0 || index >= a.FileCount() {
			continue
		}
		if scope != "" && !advancedPathInScope(a.Path(index), scope) {
			continue
		}
		filtered = append(filtered, match)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return a.Path(filtered[i].fileIndex) < a.Path(filtered[j].fileIndex)
	})
	gen := c.advancedGen
	c.mu.RUnlock()

	view := &advancedHitView{matches: filtered}
	c.mu.Lock()
	if c.archive == a && c.advancedGen == gen {
		c.storeAdvancedViewLocked(key, view)
	}
	c.mu.Unlock()
	return view
}

func (c *core) storeAdvancedViewLocked(key advancedQueryKey, view *advancedHitView) {
	if c.advancedQueryCache == nil {
		c.advancedQueryCache = make(map[advancedQueryKey]*advancedHitView)
	}
	if _, exists := c.advancedQueryCache[key]; !exists {
		c.advancedQueryOrder = append(c.advancedQueryOrder, key)
	}
	c.advancedQueryCache[key] = view
	if len(c.advancedQueryOrder) > 8 {
		evict := c.advancedQueryOrder[0]
		c.advancedQueryOrder = c.advancedQueryOrder[1:]
		delete(c.advancedQueryCache, evict)
	}
}

// binaryMatches returns the scope-free, per-file binary matches for a compiled
// pattern, reusing the per-pattern cache or a single coalesced scan.
func (c *core) binaryMatches(patternKey string, a *pvf.Archive) (map[int32]advancedFileMatch, error) {
	if err := c.syncAdvancedDirty(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	if c.archive != a {
		c.mu.RUnlock()
		return nil, ErrNoArchive
	}
	if c.binaryCache != nil {
		if cached := c.binaryCache[patternKey]; cached != nil {
			c.mu.RUnlock()
			return cached, nil
		}
	}
	state := c.advancedStatus.State
	c.mu.RUnlock()

	matched := make(map[int32]advancedFileMatch)
	pattern := []byte(patternKey)

	if state == AdvancedIndexStateIdle {
		index, coalesced, err := c.tryCoalescedBuild(a, pattern, matched)
		if err != nil {
			return nil, err
		}
		if coalesced {
			c.finishCoalescedBuild(a, index, matched, patternKey)
			return matched, nil
		}
	}

	// Plain binary-only scan (index building elsewhere or already built).
	c.mu.RLock()
	if c.archive != a {
		c.mu.RUnlock()
		return nil, ErrNoArchive
	}
	err := a.ForEachRawFile(context.Background(), func(index int32, path string, _ pvf.File, raw []byte) bool {
		occurrences, byteOffsets, tokenOffsets := binaryMatchOffsets(raw, pattern)
		if occurrences > 0 {
			matched[index] = advancedFileMatch{
				fileIndex: index,
				details: []*AdvancedSearchDetail{{
					Kind:             "binary",
					Occurrences:      occurrences,
					ByteOffsets:      byteOffsets,
					TokenOffsets:     tokenOffsets,
					Hex:              formatHex(pattern),
					OffsetsTruncated: occurrences > len(byteOffsets),
				}},
			}
		}
		return true
	})
	c.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.archive == a {
		c.storeBinaryMatchesLocked(patternKey, matched)
	}
	c.mu.Unlock()
	return matched, nil
}

func (c *core) storeBinaryMatchesLocked(patternKey string, matched map[int32]advancedFileMatch) {
	if c.binaryCache == nil {
		c.binaryCache = make(map[string]map[int32]advancedFileMatch)
	}
	c.binaryCache[patternKey] = matched
}

// tryCoalescedBuild starts a string-index build while also matching one binary
// pattern in the same scan. It returns coalesced=false when another goroutine
// already claimed the idle index, so the caller falls back to a binary-only
// scan.
func (c *core) tryCoalescedBuild(a *pvf.Archive, pattern []byte, matched map[int32]advancedFileMatch) (*pvf.StringPoolIndex, bool, error) {
	c.mu.Lock()
	if c.archive != a || c.advancedStatus.State != AdvancedIndexStateIdle {
		c.mu.Unlock()
		return nil, false, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.advancedCancel = cancel
	c.advancedStatus = AdvancedSearchIndexStatus{
		State: AdvancedIndexStateBuilding,
		Stage: "references",
		Total: int(a.FileCount()),
	}
	status := c.advancedStatus
	c.mu.Unlock()
	emitEvent("archive:advanced-index-progress", status)

	c.mu.RLock()
	index, err := a.BuildStringPoolIndex(ctx,
		func(index int32, path string, raw []byte) {
			occurrences, byteOffsets, tokenOffsets := binaryMatchOffsets(raw, pattern)
			if occurrences > 0 {
				matched[index] = advancedFileMatch{
					fileIndex: index,
					details: []*AdvancedSearchDetail{{
						Kind:             "binary",
						Occurrences:      occurrences,
						ByteOffsets:      byteOffsets,
						TokenOffsets:     tokenOffsets,
						Hex:              formatHex(pattern),
						OffsetsTruncated: occurrences > len(byteOffsets),
					}},
				}
			}
		},
		c.advancedProgressHook(int64(a.FileCount())),
	)
	c.mu.RUnlock()
	cancel()
	if err != nil {
		c.mu.Lock()
		if c.archive == a && c.advancedStatus.State == AdvancedIndexStateBuilding {
			c.advancedCancel = nil
			c.advancedStatus = AdvancedSearchIndexStatus{State: AdvancedIndexStateError, Stage: "error", Error: err.Error()}
		}
		status := c.advancedStatus
		c.mu.Unlock()
		emitEvent("archive:advanced-index-error", status)
		return nil, false, err
	}
	return index, true, nil
}

func (c *core) finishCoalescedBuild(a *pvf.Archive, index *pvf.StringPoolIndex, matched map[int32]advancedFileMatch, patternKey string) {
	c.mu.Lock()
	if c.archive != a || c.advancedStatus.State != AdvancedIndexStateBuilding {
		c.mu.Unlock()
		return
	}
	c.advancedCancel = nil
	c.advancedIndex = index
	c.advancedDirty = nil
	c.advancedStatus = AdvancedSearchIndexStatus{
		State: AdvancedIndexStateReady,
		Stage: "ready",
		Done:  int(a.FileCount()),
		Total: int(a.FileCount()),
	}
	c.storeBinaryMatchesLocked(patternKey, matched)
	status := c.advancedStatus
	c.mu.Unlock()
	emitEvent("archive:advanced-index-ready", status)
	persistAdvancedIndexAsync(c, a, index)
}

// advancedProgressHook returns a build hook that emits periodic index progress
// events so the frontend progress bar advances during a long scan.
func (c *core) advancedProgressHook(total int64) func(int32, string, []byte) {
	var done int64
	last := time.Now()
	return func(_ int32, _ string, _ []byte) {
		done++
		now := time.Now()
		if done%4096 != 0 && now.Sub(last) < 200*time.Millisecond {
			return
		}
		last = now
		emitEvent("archive:advanced-index-progress", AdvancedSearchIndexStatus{
			State: AdvancedIndexStateBuilding,
			Stage: "references",
			Done:  int(done),
			Total: int(total),
		})
	}
}

// ensureAdvancedStringIndex returns the built string index, starting an
// asynchronous build when none exists yet.
func (c *core) ensureAdvancedStringIndex() (*pvf.StringPoolIndex, error) {
	c.mu.RLock()
	if c.archive == nil {
		c.mu.RUnlock()
		return nil, ErrNoArchive
	}
	a := c.archive
	state := c.advancedStatus.State
	index := c.advancedIndex
	dirty := len(c.advancedDirty) > 0
	errText := c.advancedStatus.Error
	c.mu.RUnlock()

	switch state {
	case AdvancedIndexStateReady:
		if dirty {
			return c.refreshAdvancedIndex(index)
		}
		return index, nil
	case AdvancedIndexStateBuilding:
		return nil, ErrAdvancedSearchIndexing
	case AdvancedIndexStateError:
		if errText != "" {
			return nil, errors.New(errText)
		}
	}
	return c.startAdvancedStringBuild(a)
}

func (c *core) startAdvancedStringBuild(a *pvf.Archive) (*pvf.StringPoolIndex, error) {
	c.mu.Lock()
	if c.archive != a {
		c.mu.Unlock()
		return nil, ErrNoArchive
	}
	switch c.advancedStatus.State {
	case AdvancedIndexStateBuilding:
		c.mu.Unlock()
		return nil, ErrAdvancedSearchIndexing
	case AdvancedIndexStateReady:
		index := c.advancedIndex
		c.mu.Unlock()
		return index, nil
	case AdvancedIndexStateError:
		if c.advancedStatus.Error != "" {
			errText := c.advancedStatus.Error
			c.mu.Unlock()
			return nil, errors.New(errText)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.advancedCancel = cancel
	c.advancedStatus = AdvancedSearchIndexStatus{
		State: AdvancedIndexStateBuilding,
		Stage: "references",
		Total: int(a.FileCount()),
	}
	status := c.advancedStatus
	c.mu.Unlock()
	emitEvent("archive:advanced-index-progress", status)
	go c.runAdvancedStringBuild(a, ctx, cancel)
	return nil, ErrAdvancedSearchIndexing
}

func (c *core) runAdvancedStringBuild(a *pvf.Archive, ctx context.Context, cancel context.CancelFunc) {
	// Fast path: reuse the on-disk cache when the source is unchanged. The read
	// lock keeps SetText from mutating the overlay while we inspect it.
	c.mu.RLock()
	current := c.archive == a
	idx, ok := c.loadAdvancedIndexCache(a)
	c.mu.RUnlock()
	if !current {
		cancel()
		return
	}
	if ok {
		c.finishAdvancedBuild(a, idx, cancel)
		return
	}

	c.mu.RLock()
	index, err := a.BuildStringPoolIndex(ctx, c.advancedProgressHook(int64(a.FileCount())))
	c.mu.RUnlock()
	cancel()
	if err != nil {
		c.finishAdvancedBuildError(a, err)
		return
	}
	c.finishAdvancedBuild(a, index, cancel)
}

func (c *core) finishAdvancedBuild(a *pvf.Archive, index *pvf.StringPoolIndex, cancel context.CancelFunc) {
	cancel()
	c.mu.Lock()
	if c.archive != a || c.advancedStatus.State != AdvancedIndexStateBuilding {
		c.mu.Unlock()
		return
	}
	c.advancedCancel = nil
	c.advancedIndex = index
	c.advancedDirty = nil
	c.advancedStatus = AdvancedSearchIndexStatus{
		State: AdvancedIndexStateReady,
		Stage: "ready",
		Done:  int(a.FileCount()),
		Total: int(a.FileCount()),
	}
	status := c.advancedStatus
	c.mu.Unlock()
	emitEvent("archive:advanced-index-ready", status)
	persistAdvancedIndexAsync(c, a, index)
}

func (c *core) finishAdvancedBuildError(a *pvf.Archive, err error) {
	c.mu.Lock()
	if c.archive != a || c.advancedStatus.State != AdvancedIndexStateBuilding {
		c.mu.Unlock()
		return
	}
	c.advancedCancel = nil
	c.advancedStatus = AdvancedSearchIndexStatus{State: AdvancedIndexStateError, Stage: "error", Error: err.Error()}
	status := c.advancedStatus
	c.mu.Unlock()
	emitEvent("archive:advanced-index-error", status)
}

// refreshAdvancedIndex applies pending single-file edits to the published
// index by building a fresh copy, then refreshes cached binary matches.
func (c *core) refreshAdvancedIndex(index *pvf.StringPoolIndex) (*pvf.StringPoolIndex, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.advancedIndex != index || len(c.advancedDirty) == 0 {
		if c.advancedIndex != nil {
			return c.advancedIndex, nil
		}
		return nil, ErrAdvancedSearchIndexing
	}
	if c.archive == nil {
		return nil, ErrNoArchive
	}
	dirty := make([]int32, 0, len(c.advancedDirty))
	for fileIndex := range c.advancedDirty {
		dirty = append(dirty, fileIndex)
	}
	sort.Slice(dirty, func(i, j int) bool { return dirty[i] < dirty[j] })

	fresh, err := index.RefreshFiles(c.archive, dirty)
	if err != nil {
		c.invalidateAdvancedSearchLocked()
		return nil, err
	}
	c.advancedIndex = fresh
	c.advancedDirty = nil
	c.refreshAdvancedBinaryLocked(dirty)
	c.advancedGen++
	c.advancedQueryCache = make(map[advancedQueryKey]*advancedHitView)
	c.advancedQueryOrder = nil
	return fresh, nil
}

// syncAdvancedDirty applies any pending single-file edits before a binary
// search so binary results reflect the latest overlay.
func (c *core) syncAdvancedDirty() error {
	c.mu.RLock()
	index := c.advancedIndex
	hasDirty := len(c.advancedDirty) > 0
	c.mu.RUnlock()
	if !hasDirty {
		return nil
	}
	_, err := c.refreshAdvancedIndex(index)
	return err
}

// refreshAdvancedBinaryLocked re-scans dirty files against every cached binary
// pattern. Callers must hold c.mu.
func (c *core) refreshAdvancedBinaryLocked(dirty []int32) {
	if len(c.binaryCache) == 0 || c.archive == nil {
		return
	}
	raws := make(map[int32][]byte, len(dirty))
	for _, index := range dirty {
		raw, err := c.archive.RawBytes(index)
		if err != nil {
			continue
		}
		raws[index] = raw
	}
	for patternKey, byFile := range c.binaryCache {
		pattern := []byte(patternKey)
		next := make(map[int32]advancedFileMatch, len(byFile))
		for fileIndex, match := range byFile {
			next[fileIndex] = match
		}
		for index, raw := range raws {
			occurrences, byteOffsets, tokenOffsets := binaryMatchOffsets(raw, pattern)
			if occurrences > 0 {
				next[index] = advancedFileMatch{
					fileIndex: index,
					details: []*AdvancedSearchDetail{{
						Kind:             "binary",
						Occurrences:      occurrences,
						ByteOffsets:      byteOffsets,
						TokenOffsets:     tokenOffsets,
						Hex:              formatHex(pattern),
						OffsetsTruncated: occurrences > len(byteOffsets),
					}},
				}
			} else {
				delete(next, index)
			}
		}
		c.binaryCache[patternKey] = next
	}
}

func (s *ArchiveService) paginateAdvancedView(matches []advancedFileMatch, cursor, limit int) (*AdvancedSearchResult, error) {
	result, pending, err := s.paginateAdvancedViewLocked(matches, cursor, limit)
	if err != nil {
		return nil, err
	}
	// 行号换算必须在锁**外**做：它可能要解码文件（写文本缓存，而 cachedDecodedText 要求持写锁），
	// 在分页的 RLock 段里做会互斥卡死。只对**当前这一页**的命中算。
	s.fillAdvancedLines(pending)
	return result, nil
}

// pendingAdvancedLine 记下"哪个文件的哪条命中还要算行号"，等锁外统一处理。
type pendingAdvancedLine struct {
	fileIndex int32
	detail    *AdvancedSearchDetail
}

func (s *ArchiveService) paginateAdvancedViewLocked(matches []advancedFileMatch, cursor, limit int) (*AdvancedSearchResult, []pendingAdvancedLine, error) {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	if s.c.archive == nil {
		return nil, nil, ErrNoArchive
	}
	result := &AdvancedSearchResult{Hits: []*AdvancedSearchHit{}, NextCursor: -1}
	if cursor > len(matches) {
		cursor = len(matches)
	}
	pending := make([]pendingAdvancedLine, 0, limit)
	i := cursor
	for ; i < len(matches) && len(result.Hits) < limit; i++ {
		match := matches[i]
		index := match.fileIndex
		if index < 0 || index >= s.c.archive.FileCount() {
			continue
		}
		file := s.c.archive.File(index)
		result.Hits = append(result.Hits, &AdvancedSearchHit{
			Name:      s.c.indexedFileNameLocked(index),
			Path:      s.c.archive.Path(index),
			Size:      file.DataSize,
			DataType:  file.DataType,
			FileIndex: index,
			Details:   match.details,
		})
		for _, detail := range match.details {
			if detail == nil || strings.TrimSpace(detail.Value) == "" {
				continue
			}
			pending = append(pending, pendingAdvancedLine{fileIndex: index, detail: detail})
		}
	}
	if i < len(matches) {
		result.NextCursor = i
	}
	result.Scanned = i
	return result, pending, nil
}

// fillAdvancedLines 给命中回填 1 基行号（字符串/内容搜索的"按行跳转"靠它）。
//
// 只用**反编译文本里的首个匹配**定位：字符串模式命中的是池值文本，索引里没有文件内偏移
// （索引是"池值维度"的），所以拿 Value 在文本里正查一次最直接，行为与前端原先的
// needle 定位等价、但**能给出准确行号**。解不开或找不到就留 0，前端退化为"只打开文件"。
func (s *ArchiveService) fillAdvancedLines(pending []pendingAdvancedLine) {
	// 单页最多算这些行号：即便命中的都是"已缓存的大文件"，也不让一次搜索做无上限的扫描
	// （27MB 文本上一次 strings.Index 是十几毫秒，200 个文件叠起来就是秒级卡顿）。
	budget := 80
	for _, item := range pending {
		if budget <= 0 {
			break
		}
		if item.detail == nil || item.detail.Line > 0 {
			continue
		}
		budget--
		text, ok := s.decodedTextForLine(item.fileIndex)
		if !ok || text == "" {
			continue
		}
		at := strings.Index(text, item.detail.Value)
		if at < 0 {
			continue
		}
		item.detail.Line = int32(lineOfOffset(lineOffsetsFor(item.fileIndex, text), at))
	}
}

// decodedTextForLine 取某文件的当前文本（未保存草稿优先，其次解码缓存）。
//
// `cachedDecodedText` 要求调用方持 `c.mu`，所以这里自己加写锁 —— **调用方不得已持锁**，
// 否则就是死锁（这正是行号计算被推出 RLock 段的原因）。
func (s *ArchiveService) decodedTextForLine(index int32) (string, bool) {
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	if s.c.archive == nil || index < 0 || index >= s.c.archive.FileCount() {
		return "", false
	}
	if text, ok := s.c.editorText[index]; ok && text != "" {
		return text, true
	}
	// 【2026-10-06 卡死事故修复】这里**只能吃已经解过的缓存，绝不触发解码**。
	//
	// 上一版调的是 `cachedDecodedText`（会现场解码）：一页最多 200 个文件 ⇒ 200 次解码、
	// 每次几百毫秒且进程内文本暴涨，而且全程占着全局写锁 —— 结果不只是搜索转圈，
	// **整个程序的 c.mu 都被占住**，连文件树/打开文件都点了没反应（实测事故）。
	// 现在改成：不在缓存里就留 `line = 0`，前端退化为原有的 needle 定位（功能不丢）。
	// 用户先打开过（或用过预览）的文件本来就在缓存里，照样能显示行号。
	if text, ok := s.c.textCache[index]; ok && text != "" {
		return text, true
	}
	return "", false
}

// ContentScanHit 是「范围正文扫描」的一处命中。
type ContentScanHit struct {
	FileIndex int32  `json:"fileIndex"`
	Path      string `json:"path"`
	Line      int32  `json:"line"`
	Text      string `json:"text"`
}

// ContentScanResult 是**一批**扫描结果（分页，不是全量）。
type ContentScanResult struct {
	Hits       []*ContentScanHit `json:"hits"`
	NextCursor int               `json:"nextCursor"`
	Scanned    int               `json:"scanned"`
	Skipped    int               `json:"skipped"`
	Done       bool              `json:"done"`
}

// contentScanSkipExt 与前端保持一致：这些是二进制/多媒体，解码贵且搜不到正文。
var contentScanSkipExt = map[string]struct{}{
	"img": {}, "npk": {}, "ani": {}, "als": {}, "atk": {}, "dds": {},
	"png": {}, "jpg": {}, "jpeg": {}, "bmp": {}, "tga": {}, "gif": {},
	"ogg": {}, "mp3": {}, "wav": {}, "wma": {}, "avi": {}, "wmv": {},
}

const (
	contentScanMaxFileBytes = 2 << 20
	contentScanHitsPerFile  = 20
	// 单次请求里最多走多少个归档下标：范围很"稀"（目标目录文件少）时也不会一次跑几十万次
	// 路径比较，保证每次请求都是毫秒级、随时可停。
	contentScanIterBudget = 20000
)

// ScanContentInScope 在**指定目录范围内**分批扫正文（分页、不建索引、不常驻内存）。
//
// 【2026-10-06 用户选择"方案 2"】前端逐文件发请求时，几千个文件就是几千次 IPC + 几千次
// 解码等待（实测"太慢、目标在后面要等很久"）。这里把 **枚举 + 跳过 + 扫描** 全放到服务端：
// 一次请求处理一批，前端只要循环拿下一批 ⇒ 请求数降两个数量级。
//
// `cursor` 是**归档文件下标**（不是结果下标）：服务端从该下标继续往后走，因此**不需要**
// 把上百万条路径搬到前端（那正是上一版卡死的原因）。`NextCursor < 0` / `Done=true` 表示
// 该范围已经走完；`Skipped` 是二进制/超大/解不开而跳过的文件数。
func (s *ArchiveService) ScanContentInScope(
	scopePath, query string,
	caseSensitive, regex, wholeWord bool,
	cursor, limit int,
) (*ContentScanResult, error) {
	result := &ContentScanResult{Hits: []*ContentScanHit{}, NextCursor: -1}
	query = strings.TrimSpace(query)
	if query == "" {
		result.Done = true
		return result, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	if cursor < 0 {
		cursor = 0
	}
	scope := strings.Trim(strings.ReplaceAll(strings.TrimSpace(scopePath), "\\", "/"), "/")
	prefix := ""
	if scope != "" {
		prefix = strings.ToLower(scope) + "/"
	}

	s.c.mu.RLock()
	archive := s.c.archive
	s.c.mu.RUnlock()
	if archive == nil {
		return nil, ErrNoArchive
	}

	total := int(archive.FileCount())
	editor := &EditorService{c: s.c}
	i := cursor
	iterations := 0
	for ; i < total && result.Scanned < limit && iterations < contentScanIterBudget; i, iterations = i+1, iterations+1 {
		index := int32(i)
		s.c.mu.RLock()
		path := archive.Path(index)
		size := archive.File(index).DataSize
		s.c.mu.RUnlock()
		lower := strings.ToLower(path)
		if prefix != "" && !strings.HasPrefix(lower, prefix) {
			continue // 不在范围内：既不计数也不算"跳过"（只是枚举路过）
		}
		ext := lower
		if dot := strings.LastIndex(lower, "."); dot >= 0 {
			ext = lower[dot+1:]
		}
		if _, skip := contentScanSkipExt[ext]; skip || size > contentScanMaxFileBytes {
			result.Skipped++
			continue
		}
		found, err := editor.SearchInFile(
			index, query, caseSensitive, regex, wholeWord, contentScanHitsPerFile, nil,
		)
		if err != nil {
			result.Skipped++ // 解不开（其实是二进制/异常）也算跳过，不让整轮失败
			continue
		}
		result.Scanned++
		for _, match := range found.Matches {
			result.Hits = append(result.Hits, &ContentScanHit{
				FileIndex: index,
				Path:      path,
				Line:      match.Line,
				Text:      match.Text,
			})
		}
	}
	if i < total {
		result.NextCursor = i
	} else {
		result.Done = true
	}
	return result, nil
}

func (c *core) indexedFileNameLocked(index int32) string {
	seen := make(map[string]struct{})
	names := make([]string, 0, 1)
	for _, recordIndex := range c.searchByFile[index] {
		if recordIndex < 0 || recordIndex >= len(c.searchRecords) {
			continue
		}
		record := c.searchRecords[recordIndex].hit
		if record.Category == SearchCategoryFile || record.Name == "" {
			continue
		}
		if _, ok := seen[record.Name]; ok {
			continue
		}
		seen[record.Name] = struct{}{}
		names = append(names, record.Name)
	}
	return strings.Join(names, " / ")
}

const maxAdvancedOffsets = 32

func binaryMatchOffsets(raw, pattern []byte) (int, []int, []int) {
	if len(pattern) == 0 {
		return 0, nil, nil
	}
	occurrences := 0
	byteOffsets := make([]int, 0, maxAdvancedOffsets)
	tokenOffsets := make([]int, 0, maxAdvancedOffsets)
	for start := 0; start <= len(raw)-len(pattern); {
		relative := bytes.Index(raw[start:], pattern)
		if relative < 0 {
			break
		}
		offset := start + relative
		occurrences++
		if len(byteOffsets) < maxAdvancedOffsets {
			byteOffsets = append(byteOffsets, offset)
			if offset%5 == 0 {
				tokenOffsets = append(tokenOffsets, offset/5)
			}
		}
		start = offset + 1
	}
	return occurrences, byteOffsets, tokenOffsets
}

func formatHex(raw []byte) string {
	encoded := strings.ToUpper(hex.EncodeToString(raw))
	parts := make([]string, 0, len(encoded)/2)
	for i := 0; i+1 < len(encoded); i += 2 {
		parts = append(parts, encoded[i:i+2])
	}
	return strings.Join(parts, " ")
}

func (c *core) invalidateAdvancedSearchLocked() {
	if c.advancedCancel != nil {
		c.advancedCancel()
		c.advancedCancel = nil
	}
	c.advancedIndex = nil
	c.advancedDirty = nil
	c.advancedStatus = AdvancedSearchIndexStatus{State: AdvancedIndexStateIdle}
	c.binaryCache = make(map[string]map[int32]advancedFileMatch)
	c.advancedQueryCache = make(map[advancedQueryKey]*advancedHitView)
	c.advancedQueryOrder = nil
	c.advancedGen++
}

// markAdvancedSearchDirtyLocked records a single-file edit for incremental
// refresh when an index is already published; otherwise it falls back to a
// full invalidation.
func (c *core) markAdvancedSearchDirtyLocked(index int32) {
	if c.advancedIndex != nil && c.advancedStatus.State == AdvancedIndexStateReady {
		if c.advancedDirty == nil {
			c.advancedDirty = make(map[int32]struct{})
		}
		c.advancedDirty[index] = struct{}{}
		c.advancedGen++
		c.advancedQueryCache = make(map[advancedQueryKey]*advancedHitView)
		c.advancedQueryOrder = nil
		return
	}
	c.invalidateAdvancedSearchLocked()
}

func normalizeAdvancedScope(scope string) string {
	scope = strings.TrimSpace(strings.ReplaceAll(scope, "\\", "/"))
	for strings.HasPrefix(scope, "./") || strings.HasPrefix(scope, "/") {
		if strings.HasPrefix(scope, "./") {
			scope = scope[2:]
		} else {
			scope = scope[1:]
		}
	}
	return strings.TrimRight(strings.ToLower(scope), "/")
}

func advancedPathInScope(path, scope string) bool {
	if scope == "" {
		return true
	}
	path = strings.ToLower(strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/"))
	return path == scope || strings.HasPrefix(path, scope+"/")
}
