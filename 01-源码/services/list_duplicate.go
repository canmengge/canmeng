package services

import (
	"errors"
	"sort"
	"strings"
	"time"

	"pvfine/internal/logging"
)

// 本文件是「大文件 list 查重」的后端实现。
//
// 为什么不在前端查：大文件的整份文本不进窗口（秒开的前提），前端拿不到内容，
// 所以 .lst 查重这个按钮对大文件一直是置灰的。这里把同一套规则搬到 Go 侧，
// 文本留在后端扫完只回传「问题条目」，窗口不装整份文本 —— 秒开性能不受影响。
//
// 规则与前端 `frontend/src/listDuplicate.ts` 严格一致（提示语也照抄）：
//   ① ID 相同、路径不同 → "ID重复，请检查后修改！"
//   ② ID 不同、路径相同 → "路径重复，请检查后修改！"
//   ③ ID 相同、路径相同 → "兄弟，你列表加重复了，给我检查好了啊！"

// 回传上限：超大清单可能整篇都是重复项，全量回传既慢又没意义。
const (
	listDuplicateIssueCap   = 300
	listDuplicateEntryCap   = 100
	listDuplicateScanCap    = 1 << 30 // 单次扫描的文本上限（1GB），防御异常文件
)

// ListDuplicateEntry 是一个清单条目（与前端 ListEntry 同构）。
type ListDuplicateEntry struct {
	Line int32  `json:"line"` // 1 基行号
	ID   string `json:"id"`
	Path string `json:"path"`
	Raw  string `json:"raw"`
}

// ListDuplicateIssue 是一处重复问题；kind: id / path / both。
type ListDuplicateIssue struct {
	Kind    string               `json:"kind"`
	Message string               `json:"message"`
	Entries []ListDuplicateEntry `json:"entries"`
}

// ListDuplicateReport 是查重结果（Total 是真实条数，可能大于 len(Issues)）。
type ListDuplicateReport struct {
	Issues    []ListDuplicateIssue `json:"issues"`
	Total     int                  `json:"total"`
	Truncated bool                 `json:"truncated"`
	Entries   int                  `json:"entries"`
}

var listDuplicateMessages = map[string]string{
	"id":   "ID重复，请检查后修改！",
	"path": "路径重复，请检查后修改！",
	"both": "兄弟，你列表加重复了，给我检查好了啊！",
}

// CheckListDuplicates 对指定 .lst 做整份查重（文本留在后端）。
func (s *EditorService) CheckListDuplicates(index int32) (ListDuplicateReport, error) {
	startedAt := time.Now()
	s.c.mu.Lock()
	a := s.c.archive
	if a == nil {
		s.c.mu.Unlock()
		return ListDuplicateReport{}, ErrNoArchive
	}
	if err := validateAnnotationIndex(a, index); err != nil {
		s.c.mu.Unlock()
		return ListDuplicateReport{}, err
	}
	// 字符串是不可变的：取到之后就可以放锁扫描，不占用前台 RPC（踩坑 #20）。
	text, err := s.currentLargeTextLocked(index)
	s.c.mu.Unlock()
	if err != nil {
		return ListDuplicateReport{}, err
	}
	if len(text) > listDuplicateScanCap {
		return ListDuplicateReport{}, errors.New("清单过大，已放弃查重（超过 1GB）")
	}

	report := scanListDuplicates(index, text)
	logging.For("listdup").Info("list 查重完成",
		"文件", a.Path(index), "条目", report.Entries, "问题", report.Total,
		"截断", report.Truncated, "耗时", logging.FormatDuration(time.Since(startedAt)))
	return report, nil
}

// hashLine 是「哈希 + 行号」，用来代替「键 → 条目」的大 map：
// 438 万条目的 map 会再造出约 1GB 常驻内存，而这里只需要 16 字节/行。
type hashLine struct {
	hash uint64
	line int32
}

func fnv64a(a string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(a); i++ {
		h ^= uint64(a[i])
		h *= 1099511628211
	}
	return h
}

func fnv64aSep(a, b string) uint64 {
	h := fnv64a(a)
	h ^= 0
	h *= 1099511628211
	for i := 0; i < len(b); i++ {
		h ^= uint64(b[i])
		h *= 1099511628211
	}
	return h
}

// scanListDuplicates 做三趟扫描：整行（ID+路径）/ ID / 路径。
// 每趟只留 16 字节/行的哈希数组，扫完即弃，避免为超大清单堆出 GB 级 map。
func scanListDuplicates(index int32, text string) ListDuplicateReport {
	offsets := lineOffsetsFor(index, text)
	lineText := func(line int32) string {
		if line < 1 || int(line) >= len(offsets) {
			return ""
		}
		return text[offsets[line-1]:offsets[line]]
	}
	entryAt := func(line int32) (ListDuplicateEntry, bool) {
		raw := strings.TrimRight(lineText(line), "\r")
		id, path, ok := parseListLine(raw)
		if !ok {
			return ListDuplicateEntry{}, false
		}
		return ListDuplicateEntry{Line: line, ID: id, Path: path, Raw: raw}, true
	}

	collect := func(hash func(id, path string) uint64) [][]int32 {
		var rows []hashLine
		totalLines := int32(len(offsets)) - 1
		for line := int32(1); line <= totalLines; line++ {
			raw := lineText(line)
			id, path, ok := parseListLine(strings.TrimRight(raw, "\r"))
			if !ok {
				continue
			}
			rows = append(rows, hashLine{hash: hash(id, path), line: line})
		}
		sort.Slice(rows, func(x, y int) bool { return rows[x].hash < rows[y].hash })
		var groups [][]int32
		for i := 0; i < len(rows); {
			j := i + 1
			for j < len(rows) && rows[j].hash == rows[i].hash {
				j++
			}
			if j-i > 1 {
				lines := make([]int32, 0, j-i)
				for k := i; k < j; k++ {
					lines = append(lines, rows[k].line)
				}
				sort.Slice(lines, func(x, y int) bool { return lines[x] < lines[y] })
				groups = append(groups, lines)
			}
			i = j
		}
		return groups
	}

	entryCount := 0
	{
		for line := int32(1); line < int32(len(offsets)); line++ {
			if _, _, ok := parseListLine(strings.TrimRight(lineText(line), "\r")); ok {
				entryCount++
			}
		}
	}

	var issues []ListDuplicateIssue
	add := func(kind string, lines []int32, unique func(entries []ListDuplicateEntry) bool) {
		if len(lines) < 2 {
			return
		}
		entries := make([]ListDuplicateEntry, 0, len(lines))
		for _, line := range lines {
			if len(entries) >= listDuplicateEntryCap {
				break
			}
			if entry, ok := entryAt(line); ok {
				entries = append(entries, entry)
			}
		}
		if len(entries) < 2 {
			return
		}
		if unique != nil && !unique(entries) {
			return
		}
		issues = append(issues, ListDuplicateIssue{
			Kind:    kind,
			Message: listDuplicateMessages[kind],
			Entries: entries,
		})
	}

	// ③ ID 与路径都相同
	for _, group := range collect(func(id, path string) uint64 { return fnv64aSep(id, path) }) {
		add("both", group, nil)
	}
	// ① 同一 ID 指向不同路径
	for _, group := range collect(func(id, _ string) uint64 { return fnv64a(id) }) {
		add("id", group, func(entries []ListDuplicateEntry) bool {
			seen := make(map[string]bool, len(entries))
			for _, entry := range entries {
				seen[entry.Path] = true
			}
			return len(seen) > 1
		})
	}
	// ② 同一路径被不同 ID 指向
	for _, group := range collect(func(_, path string) uint64 { return fnv64a(path) }) {
		add("path", group, func(entries []ListDuplicateEntry) bool {
			seen := make(map[string]bool, len(entries))
			for _, entry := range entries {
				seen[entry.ID] = true
			}
			return len(seen) > 1
		})
	}

	sort.SliceStable(issues, func(x, y int) bool {
		return issues[x].Entries[0].Line < issues[y].Entries[0].Line
	})
	total := len(issues)
	truncated := false
	if total > listDuplicateIssueCap {
		issues = issues[:listDuplicateIssueCap]
		truncated = true
	}
	return ListDuplicateReport{
		Issues:    issues,
		Total:     total,
		Truncated: truncated,
		Entries:   entryCount,
	}
}

// parseListLine 解析一行清单条目，规则与前端 parseListEntries 一致：
// 跳过空行与 `;` / `//` / `#` 注释行；ID 是首个 token；路径优先取反引号内，
// 其次单/双引号，最后退回首个空白分隔 token。
func parseListLine(raw string) (id string, path string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", false
	}
	if trimmed[0] == ';' || trimmed[0] == '#' || strings.HasPrefix(trimmed, "//") {
		return "", "", false
	}
	space := strings.IndexAny(trimmed, " \t")
	if space <= 0 {
		return "", "", false
	}
	id = trimmed[:space]
	rest := strings.TrimSpace(trimmed[space+1:])
	if rest == "" {
		return "", "", false
	}
	switch rest[0] {
	case '`':
		if end := strings.IndexByte(rest[1:], '`'); end >= 0 {
			path = rest[1 : 1+end]
		} else {
			path = rest[1:]
		}
	case '"', '\'':
		quote := rest[0]
		if end := strings.IndexByte(rest[1:], quote); end >= 0 {
			path = rest[1 : 1+end]
		} else {
			path = rest[1:]
		}
	default:
		if end := strings.IndexAny(rest, " \t"); end >= 0 {
			path = rest[:end]
		} else {
			path = rest
		}
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", false
	}
	return id, path, true
}
