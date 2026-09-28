package services

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	appconfig "pvfine/config"
	"pvfine/internal/pvf"
	"pvfine/internal/stringguard"
)

// DoctorService 提供"一次只读遍历出全貌"的归档体检能力：
// 文件/目录总量、扩展名分布、字符串表指纹、以及 `.equ/.stk/.obj` 的 lst 登记覆盖。
// 它不写入任何内容，也不修改索引；用于"改前先体检"。
type DoctorService struct {
	c       *core
	running atomic.Bool
}

func NewDoctorService(c *core) *DoctorService {
	return &DoctorService{c: c}
}

// DoctorExtension 是按扩展名聚合的统计项。
type DoctorExtension struct {
	Ext   string `json:"ext"`
	Count int    `json:"count"`
	Bytes int64  `json:"bytes"`
}

// DoctorTableCheck 是单张字符串表的只读检查结果。
type DoctorTableCheck struct {
	TableIndex int    `json:"tableIndex"`
	Path       string `json:"path"`
	Protected  bool   `json:"protected"`
	Exists     bool   `json:"exists"`
	Size       int32  `json:"size"`
	SHA256     string `json:"sha256"`
}

// DoctorTableSummary 汇总字符串表检查结果。
type DoctorTableSummary struct {
	Total     int `json:"total"`
	Present   int `json:"present"`
	Missing   int `json:"missing"`
	Protected int `json:"protected"`
}

// DoctorCoverage 是按扩展名分组的未登记文件样本。
type DoctorCoverage struct {
	Ext     string   `json:"ext"`
	Count   int      `json:"count"`
	Samples []string `json:"samples"`
}

// DoctorListCoverage 汇总 lst 登记覆盖结果。
type DoctorListCoverage struct {
	ListPaths    []string `json:"listPaths"`
	Registered   int      `json:"registered"`
	Checked      int      `json:"checked"`
	Unregistered int      `json:"unregistered"`
}

// DoctorReport 是一次体检的完整结果。
type DoctorReport struct {
	Path            string             `json:"path"`
	GeneratedAt     string             `json:"generatedAt"`
	DurationMs      int64              `json:"durationMs"`
	FileCount       int32              `json:"fileCount"`
	DirCount        int                `json:"dirCount"`
	GroupCount      int32              `json:"groupCount"`
	BodySize        int32              `json:"bodySize"`
	Paged110        bool               `json:"paged110"`
	Extensions      []DoctorExtension  `json:"extensions"`
	Tables          []DoctorTableCheck `json:"tables"`
	TableSummary    DoctorTableSummary `json:"tableSummary"`
	ListCoverage    DoctorListCoverage `json:"listCoverage"`
	UnregisteredTop []DoctorCoverage   `json:"unregisteredTop"`
	Warnings        []string           `json:"warnings"`
}

// coverageExtensions 是需要 lst 登记的脚本类扩展名（上级规则 §6.11）。
var coverageExtensions = []string{".equ", ".stk", ".obj"}

// coverageListPaths 是登记覆盖检查所依据的清单文件。
var coverageListPaths = []string{
	"list/equipment.lst",
	"list/stackable.lst",
	"list/passiveobject.lst",
}

const coverageSampleLimit = 50

// Run 执行一次只读体检。同一时刻只允许一个体检在跑。
func (s *DoctorService) Run() (*DoctorReport, error) {
	if !s.running.CompareAndSwap(false, true) {
		return nil, errors.New("体检已在进行中")
	}
	defer s.running.Store(false)

	started := time.Now()
	s.c.mu.RLock()
	a := s.c.archive
	s.c.mu.RUnlock()
	if a == nil {
		return nil, ErrNoArchive
	}

	info := a.Info()
	report := &DoctorReport{
		Path:        info.Path,
		GeneratedAt: started.Format(time.RFC3339),
		FileCount:   info.FileCount,
		GroupCount:  info.GroupCount,
		BodySize:    info.BodySize,
		Paged110:    info.Paged110,
	}

	registered, listPaths, warnings := collectRegisteredPaths(a)
	report.ListCoverage.ListPaths = listPaths
	report.Warnings = append(report.Warnings, warnings...)

	extensions, dirCount, checked, unregistered, groups := scanArchivePaths(a, registered)
	report.Extensions = extensions
	report.DirCount = dirCount
	report.ListCoverage.Registered = len(registered)
	report.ListCoverage.Checked = checked
	report.ListCoverage.Unregistered = unregistered
	report.UnregisteredTop = groups

	report.Tables, report.TableSummary = checkStringTables(a, &report.Warnings)
	report.DurationMs = time.Since(started).Milliseconds()
	return report, nil
}

// scanArchivePaths 一次遍历完成扩展名统计、目录计数与登记覆盖检查。
func scanArchivePaths(a *pvf.Archive, registered map[string]struct{}) ([]DoctorExtension, int, int, int, []DoctorCoverage) {
	byExt := make(map[string]*DoctorExtension)
	dirs := make(map[string]struct{})
	needsRegistration := make(map[string]struct{}, len(coverageExtensions))
	for _, ext := range coverageExtensions {
		needsRegistration[ext] = struct{}{}
	}
	unregisteredByExt := make(map[string]int)
	samplesByExt := make(map[string][]string)
	checked := 0
	unregistered := 0

	total := a.FileCount()
	for i := int32(0); i < total; i++ {
		path := a.Path(i)
		ext := extensionOf(path)
		entry := byExt[ext]
		if entry == nil {
			entry = &DoctorExtension{Ext: ext}
			byExt[ext] = entry
		}
		entry.Count++
		entry.Bytes += int64(a.File(i).DataSize)

		for idx := strings.LastIndexByte(path, '/'); idx > 0; idx = strings.LastIndexByte(path[:idx], '/') {
			dirs[path[:idx]] = struct{}{}
		}

		if _, need := needsRegistration[ext]; !need {
			continue
		}
		checked++
		if _, ok := registered[normalizeArchivePath(path)]; ok {
			continue
		}
		unregistered++
		unregisteredByExt[ext]++
		if len(samplesByExt[ext]) < coverageSampleLimit {
			samplesByExt[ext] = append(samplesByExt[ext], path)
		}
	}

	extensions := make([]DoctorExtension, 0, len(byExt))
	for _, entry := range byExt {
		extensions = append(extensions, *entry)
	}
	sort.Slice(extensions, func(i, j int) bool {
		if extensions[i].Count != extensions[j].Count {
			return extensions[i].Count > extensions[j].Count
		}
		return extensions[i].Ext < extensions[j].Ext
	})

	groups := make([]DoctorCoverage, 0, len(unregisteredByExt))
	for ext, count := range unregisteredByExt {
		groups = append(groups, DoctorCoverage{Ext: ext, Count: count, Samples: samplesByExt[ext]})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Ext < groups[j].Ext
	})

	return extensions, len(dirs), checked, unregistered, groups
}

// extensionOf 返回小写扩展名（含点）；没有扩展名时返回空串。
func extensionOf(path string) string {
	name := path
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		name = path[idx+1:]
	}
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		return strings.ToLower(name[dot:])
	}
	return ""
}

// collectRegisteredPaths 读出各登记清单的已登记路径集合。
func collectRegisteredPaths(a *pvf.Archive) (map[string]struct{}, []string, []string) {
	registered := make(map[string]struct{})
	found := make([]string, 0, len(coverageListPaths))
	warnings := make([]string, 0)
	for _, listPath := range coverageListPaths {
		index, ok := a.FindList(listPath)
		if !ok {
			warnings = append(warnings, "登记清单不存在: "+listPath)
			continue
		}
		pairs, err := a.ListPairs(index)
		if err != nil {
			warnings = append(warnings, "读取登记清单失败: "+listPath)
			continue
		}
		found = append(found, listPath)
		for _, pair := range pairs {
			value := normalizeArchivePath(pair.Path)
			if value == "" {
				continue
			}
			registered[value] = struct{}{}
		}
	}
	return registered, found, warnings
}

func normalizeArchivePath(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
}

// checkStringTables 统计字符串表是否存在，并给出每张表的字节 SHA-256（供与基线比对）。
func checkStringTables(a *pvf.Archive, warnings *[]string) ([]DoctorTableCheck, DoctorTableSummary) {
	checks := make([]DoctorTableCheck, 0)
	summary := DoctorTableSummary{}
	catalog, err := stringguard.Parse(appconfig.ProtectedStringTablesJSON)
	if err != nil {
		*warnings = append(*warnings, "禁动表清单解析失败: "+err.Error())
		return checks, summary
	}
	protected := make(map[string]struct{}, len(catalog.ProtectedPaths))
	for _, path := range catalog.ProtectedPaths {
		protected[normalizeArchivePath(path)] = struct{}{}
	}

	indexes := make([]int, 0, len(catalog.TablePaths))
	for key := range catalog.TablePaths {
		value, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		indexes = append(indexes, value)
	}
	sort.Ints(indexes)

	for _, tableIndex := range indexes {
		path := catalog.TablePath(tableIndex)
		check := DoctorTableCheck{TableIndex: tableIndex, Path: path}
		summary.Total++
		if _, ok := protected[normalizeArchivePath(path)]; ok {
			check.Protected = true
			summary.Protected++
		}
		index, ok := a.Find(path)
		if !ok {
			summary.Missing++
			checks = append(checks, check)
			continue
		}
		check.Exists = true
		check.Size = a.File(index).DataSize
		if raw, err := a.RawBytes(index); err == nil && len(raw) > 0 {
			sum := sha256.Sum256(raw)
			check.SHA256 = hex.EncodeToString(sum[:])
		}
		summary.Present++
		checks = append(checks, check)
	}
	return checks, summary
}
