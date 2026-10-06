package services

// 大文件「全文查找」：在**整个文件**里搜，而不是只搜已加载的视口窗口。
//
// 为什么必须放后端：大文件的文本不进窗口（秒开前提，见 large_text.go 顶部注释），
// 前端 CodeMirror 文档里只有当前视口那几千行 ⇒ 官方 search 面板只能搜到窗口内内容
// （用户反馈：大文件按 Ctrl+F"像没有搜索功能"）。
//
// 做法照 services/list_duplicate.go 的既定模板：
//  1. **整份文本留在后端扫**，只回传「命中位置 + 目标行预览」；
//  2. **不写回归档** —— 用户未保存的改动由前端把 overlay 段传上来，在内存里叠加后再扫
//     （用户红线：只有显式保存才写回）；
//  3. **扫描放锁外** —— Go 字符串不可变，取到文本即可放锁，不占用前台 RPC（踩坑 #20）。

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// fileSearchMatchCap 单次最多回传多少处命中（超出只计数、不回传）。
	fileSearchMatchCap = 20000
	// fileSearchCountCap 统计总数时的上限，防止病态表达式把总数统计变成无底洞。
	fileSearchCountCap = 200000
	// fileSearchDefaultLimit 前端不传 limit 时的默认回传条数。
	fileSearchDefaultLimit = 2000
	// fileSearchPreviewRunes 每处命中回传的「该行原文」字符上限（仅用于结果预览）。
	fileSearchPreviewRunes = 300
)

// FileSearchMatch 是单文件内的一处命中；行号 1 基，列与长度按**字符**计。
type FileSearchMatch struct {
	Line   int32  `json:"line"`
	Column int32  `json:"column"`
	Length int32  `json:"length"`
	Text   string `json:"text"`
}

// FileSearchResult 是查找结果；Total 是真实命中数（可能大于 len(Matches)）。
type FileSearchResult struct {
	Matches   []FileSearchMatch `json:"matches"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
}

// SearchInFile 在指定文件的**全文**里查找。
//
// caseSensitive / regex / wholeWord 与官方查找面板的三个勾选一一对应；
// segments 是「TXT 视图里改了但还没写回归档」的段（可为空）。
func (s *EditorService) SearchInFile(
	index int32,
	query string,
	caseSensitive bool,
	regex bool,
	wholeWord bool,
	limit int32,
	segments []OverlaySegment,
) (FileSearchResult, error) {
	empty := FileSearchResult{Matches: []FileSearchMatch{}}
	if strings.TrimSpace(query) == "" {
		return empty, nil
	}
	if limit <= 0 {
		limit = fileSearchDefaultLimit
	}
	if limit > fileSearchMatchCap {
		limit = fileSearchMatchCap
	}

	re, err := compileFileSearchPattern(query, caseSensitive, regex, wholeWord)
	if err != nil {
		return empty, err
	}

	s.c.beginFront()
	defer s.c.endFront()

	s.c.mu.Lock()
	a := s.c.archive
	if a == nil {
		s.c.mu.Unlock()
		return empty, ErrNoArchive
	}
	if err := validateAnnotationIndex(a, index); err != nil {
		s.c.mu.Unlock()
		return empty, err
	}
	text, err := s.currentLargeTextLocked(index)
	s.c.mu.Unlock()
	if err != nil {
		return empty, err
	}
	// 未写回的改动在这里叠加（只在内存里，归档一个字节都不动）。
	if len(segments) > 0 {
		text = applyOverlaySegments(index, text, segments)
	}

	offsets := lineOffsetsFor(index, text)
	// 先按上限把所有命中位置取出来：既拿到真实总数，也够填满回传条数。
	locs := re.FindAllStringIndex(text, fileSearchCountCap)
	result := FileSearchResult{
		Matches: make([]FileSearchMatch, 0, min(len(locs), int(limit))),
		Total:   len(locs),
	}
	for _, loc := range locs {
		if len(result.Matches) >= int(limit) {
			result.Truncated = true
			break
		}
		line := lineOfOffset(offsets, loc[0])
		if line < 1 || line > len(offsets)-1 {
			continue
		}
		lineStart := int(offsets[line-1])
		lineEnd := int(offsets[line]) - 1 // 去掉行尾换行符
		if lineEnd < lineStart {
			lineEnd = lineStart
		}
		result.Matches = append(result.Matches, FileSearchMatch{
			Line:   int32(line),
			Column: int32(utf8.RuneCountInString(text[lineStart:loc[0]])),
			Length: int32(utf8.RuneCountInString(text[loc[0]:loc[1]])),
			Text:   truncateRunes(text[lineStart:lineEnd], fileSearchPreviewRunes),
		})
	}
	return result, nil
}

// compileFileSearchPattern 把三个勾选翻译成正则（普通查找走 QuoteMeta，绝不自己解释元字符）。
func compileFileSearchPattern(query string, caseSensitive, regex, wholeWord bool) (*regexp.Regexp, error) {
	pattern := query
	if !regex {
		pattern = regexp.QuoteMeta(query)
		if wholeWord {
			pattern = `\b` + pattern + `\b`
		}
	}
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("查找表达式无效：%v", err)
	}
	return re, nil
}

// lineOfOffset 用行偏移表把字节偏移映射成 1 基行号。
// offsets[L] = 第 L+1 行的起始下标，末尾有哨兵 ⇒ 命中 [offsets[i-1], offsets[i]) 落在第 i 行。
func lineOfOffset(offsets []int32, off int) int {
	if len(offsets) == 0 {
		return 1
	}
	i := sort.Search(len(offsets), func(i int) bool { return int(offsets[i]) > off })
	if i <= 0 {
		return 1
	}
	return i
}

// truncateRunes 按字符截断（PVF 脚本里中文很常见，按字节截会切出半个字）。
func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
