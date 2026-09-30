package services

import "strings"

// =============================================================================
// 大文件「连续全文 TXT」通道（大文件秒开的落地实现）
//
// 背景（2026-09-30 实测）：`list/equipment.lst` 解码后 2740 万字符 / 41.4 万行，
// 只要把这**整份**文本送进窗口，前端就停摆 43 秒、之后每 11 秒一轮 —— 且与是否挂
// CodeMirror、渲染多少行都无关（空文档挂载 CodeMirror 也要 30 秒）。
// 而外部编辑器打开同一个文件是秒开，区别只有一个：它读本地文件、不经窗口 IPC。
//
// 做法：**文本留在后端，窗口按视口取行**。前端用 spacer 把整个文件的高度撑开
// （行数 × 固定行高），滚动到哪就只取「当前视口附近的行」（GetFileLines），
// DOM 里永远只有几百行；编辑当前段后由后端拼回全文写回归档内存（SetFileLines，
// 与编辑器保存同一条 `core.setText`），之后照常由用户点「保存 PVF」落盘。
// =============================================================================

// largeTextScrollChunk 是一次请求的默认行数（前端还会上下多取一些做缓冲）。
const largeTextScrollChunk = 2000

// LargeTextChunk 是行区间切片（行号 1 基）。
type LargeTextChunk struct {
	Index    int32  `json:"index"`
	Path     string `json:"path"`
	Start    int32  `json:"start"` // 本块起始行（1 基）
	Count    int32  `json:"count"` // 本块行数
	Lines    int32  `json:"lines"` // 全文行数
	Editable bool   `json:"editable"`
	Dirty    bool   `json:"dirty"`
	Text     string `json:"text"` // **仅本块**文本
}

// countLines 统计一段文本的行数（空串 0 行；末行无换行符也算一行）。
func countLines(text string) int32 {
	if text == "" {
		return 0
	}
	lines := int32(strings.Count(text, "\n"))
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return lines
}

// lineStartOffset 返回第 line 行（1 基）的起始字符偏移。
func lineStartOffset(text string, line int32) int {
	if line <= 1 {
		return 0
	}
	offset := 0
	for l := int32(1); l < line; l++ {
		idx := strings.IndexByte(text[offset:], '\n')
		if idx < 0 {
			return len(text)
		}
		offset += idx + 1
	}
	return offset
}

// currentLargeTextLocked 返回该文件当前的全文：编辑器 overlay 优先，否则用解码缓存。
// 调用方必须已持有 c.mu。
func (s *EditorService) currentLargeTextLocked(index int32) (string, error) {
	a := s.c.archive
	if a == nil {
		return "", ErrNoArchive
	}
	if text, ok := s.c.editorText[index]; ok {
		return text, nil
	}
	return s.c.cachedDecodedText(index, a)
}

// GetFileLines 取 [startLine, startLine+count) 行（1 基，含首不含尾）。
// 大文件的文本不进窗口，视口滚到哪就只取哪一段，因此打开与滚动都是毫秒级。
func (s *EditorService) GetFileLines(index int32, startLine int32, count int32) (*LargeTextChunk, error) {
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
	text, err := s.currentLargeTextLocked(index)
	if err != nil {
		return nil, err
	}
	total := countLines(text)
	if startLine < 1 {
		startLine = 1
	}
	if total > 0 && startLine > total {
		startLine = total
	}
	if count <= 0 {
		count = largeTextScrollChunk
	}
	from := lineStartOffset(text, startLine)
	to := len(text)
	if end := startLine + count; total > 0 && end <= total {
		to = lineStartOffset(text, end)
	}
	chunk := text[from:to]
	return &LargeTextChunk{
		Index:    index,
		Path:     a.Path(index),
		Start:    startLine,
		Count:    countLines(chunk),
		Lines:    total,
		Editable: true,
		Dirty:    a.IsModified(index),
		Text:     chunk,
	}, nil
}

// SetFileLines 用 text 替换 [startLine, startLine+lineCount) 行并写回归档内存。
//
// 前端只传当前视口那几百行，几十兆文本不过 IPC；拼回全文在 Go 侧做，
// 之后仍走 `core.setText`（与编辑器保存同一条路：字符串表保护、未保存计数、
// 搜索名同步都在这一条路上）。
func (s *EditorService) SetFileLines(index int32, startLine int32, lineCount int32, text string) (*LargeTextChunk, error) {
	s.c.mu.Lock()
	a := s.c.archive
	if a == nil {
		s.c.mu.Unlock()
		return nil, ErrNoArchive
	}
	if err := validateAnnotationIndex(a, index); err != nil {
		s.c.mu.Unlock()
		return nil, err
	}
	cur, err := s.currentLargeTextLocked(index)
	s.c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if startLine < 1 {
		startLine = 1
	}
	if lineCount < 0 {
		lineCount = 0
	}
	total := countLines(cur)
	from := lineStartOffset(cur, startLine)
	to := len(cur)
	if end := startLine + lineCount; total > 0 && end <= total {
		to = lineStartOffset(cur, end)
	}
	// setText 自己会加锁：必须已放锁再调用。
	if _, _, err := s.c.setText(index, cur[:from]+text+cur[to:]); err != nil {
		return nil, err
	}
	return s.GetFileLines(index, startLine, largeTextScrollChunk)
}
