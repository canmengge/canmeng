package services

import (
	"fmt"
	"strings"
)

// =============================================================================
// 大文件「页式 TXT」通道
//
// 背景（2026-09-30 实测）：`list/equipment.lst` 解码后 2740 万字符，只要把这**整份**
// 文本送进窗口，前端就停摆 43 秒、之后每 11 秒一轮 —— 且与是否挂 CodeMirror、渲染
// 多少行都无关（空文档挂载 CodeMirror 也要 30 秒）。而外部编辑器打开同一个文件是秒开，
// 区别只有一个：它读本地文件、不经窗口 IPC。
//
// 因此这里把「外部 TXT 秒开」的能力做进程序：**文本留在后端，窗口只拿当前一页**
// （默认每页 2000 行 ≈ 130KB）。编辑在当前页的 textarea 里做，保存时后端把该页拼回
// 全文并写进归档内存（与编辑器保存完全同一条路径：`core.setText`），
// 之后照常由用户点「保存 PVF」落盘。
// =============================================================================

// largeTextLinesPerPage 是每页行数：一页约 130KB，打开/切换都在毫秒级。
const largeTextLinesPerPage = 2000

// LargeTextPage 是一页大文本（窗口里只会出现这一页）。
type LargeTextPage struct {
	Index     int32  `json:"index"`
	Path      string `json:"path"`
	Page      int32  `json:"page"`      // 0 基页号
	PageCount int32  `json:"pageCount"` // 总页数
	Lines     int32  `json:"lines"`     // 全文行数
	PageLines int32  `json:"pageLines"` // 本页行数
	Editable  bool   `json:"editable"`
	Dirty     bool   `json:"dirty"` // 归档内存里该文件是否已有未保存修改
	Text      string `json:"text"`  // **仅本页**文本
}

// largeTextOffsets 扫一遍全文，返回每页起始字符偏移与总行数。
// 41 万行也只有约 200 个偏移，开销可以忽略（Go 侧扫 27MB 约 20ms）。
func largeTextOffsets(text string) ([]int, int32) {
	offsets := make([]int, 1, len(text)/131072+2)
	offsets[0] = 0
	line := int32(1)
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		// 第 line 行到此结束：若它正好是每页的最后一行，下一页从这里开始。
		if line%largeTextLinesPerPage == 0 {
			offsets = append(offsets, i+1)
		}
		line++
	}
	return offsets, line
}

// pageSlice 把 (全文, 页号) 换算成字符区间，页号越界时钳到合法范围。
func pageSlice(text string, offsets []int, page int32) (int, int, int) {
	p := int(page)
	if p < 0 {
		p = 0
	}
	if p >= len(offsets) {
		p = len(offsets) - 1
	}
	from := offsets[p]
	to := len(text)
	if p+1 < len(offsets) {
		to = offsets[p+1]
	}
	return p, from, to
}

// countLines 统计一段文本的行数（末行无换行符也算一行）。
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

// GetFilePage 取一页大文本。窗口里永远只有这一页，因此大文件也能"秒开"。
func (s *EditorService) GetFilePage(index int32, page int32) (*LargeTextPage, error) {
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
	offsets, lines := largeTextOffsets(text)
	p, from, to := pageSlice(text, offsets, page)
	pageText := text[from:to]
	return &LargeTextPage{
		Index:     index,
		Path:      a.Path(index),
		Page:      int32(p),
		PageCount: int32(len(offsets)),
		Lines:     lines,
		PageLines: countLines(pageText),
		Editable:  true,
		Dirty:     a.IsModified(index),
		Text:      pageText,
	}, nil
}

// SetFilePage 把某一页的文本拼回全文并写进归档内存。
//
// 只传一页（约 130KB），几十兆文本不动 IPC；拼接在 Go 侧完成，随后走与编辑器保存
// 完全相同的 `core.setText`（字符串表保护、搜索名同步、未保存计数都在这一条路上）。
func (s *EditorService) SetFilePage(index int32, page int32, text string) (*LargeTextPage, error) {
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

	offsets, _ := largeTextOffsets(cur)
	p := int(page)
	if p < 0 || p >= len(offsets) {
		return nil, fmt.Errorf("页号越界: %d（共 %d 页）", page, len(offsets))
	}
	_, from, to := pageSlice(cur, offsets, page)
	// setText 自己会加锁，这里必须先放锁再调用。
	if _, _, err := s.c.setText(index, cur[:from]+text+cur[to:]); err != nil {
		return nil, err
	}
	return s.GetFilePage(index, int32(p))
}
