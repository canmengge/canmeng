package services

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"pvfine/internal/formview"
	"pvfine/internal/pvf"
)

// FormViewFormatInfo 是一个可用文件族的对外描述。
type FormViewFormatInfo struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Files    []string `json:"files"`
	Notes    string   `json:"notes,omitempty"`
	Sections []string `json:"sections"`
}

// FormViewFormatListResult 是结构化视图规则目录及其来源。
type FormViewFormatListResult struct {
	RulePath    string                `json:"rulePath"`
	FormatCount int                   `json:"formatCount"`
	Formats     []*FormViewFormatInfo `json:"formats"`
}

// FormViewService 提供「结构化视图」：按外部规则把脚本文件投影成只读表格
// （段 → 行 → 列），供界面做表格化阅读。
//
// 规则来自外部数据文件（config/formats.json），本服务不硬编码任何段名、
// 列名、枚举取值或刻度常量。投影复用内核既有的词法投影
// （internal/pvf 的 ParseScriptView），不另写一套解析器。
//
// 整个投影过程**只读**：不产生任何归档写入。
type FormViewService struct {
	c *core

	mu     sync.RWMutex
	rules  formview.Catalog
	path   string
	err    error
	loaded bool
}

func NewFormViewService(c *core) *FormViewService {
	service := &FormViewService{c: c}
	if path, ok := formview.FindSourcePath(); ok {
		service.path = path
	} else if runtimePath, err := formview.RuntimePath(); err == nil {
		service.path = runtimePath
	}
	return service
}

// ListFormats 返回规则文件里定义的全部文件族。
func (s *FormViewService) ListFormats() (*FormViewFormatListResult, error) {
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	formats := make([]*FormViewFormatInfo, 0, len(catalog.Formats))
	for _, format := range catalog.Sorted() {
		info := &FormViewFormatInfo{
			ID:       format.ID,
			Label:    format.DisplayLabel(),
			Files:    append([]string(nil), format.Files...),
			Notes:    format.Notes,
			Sections: make([]string, 0, len(format.Sections)),
		}
		for _, section := range format.Sections {
			label := strings.TrimSpace(section.Label)
			if label == "" {
				label = strings.TrimSpace(section.Section)
			}
			info.Sections = append(info.Sections, label)
		}
		formats = append(formats, info)
	}
	return &FormViewFormatListResult{
		RulePath:    s.rulePath(),
		FormatCount: len(formats),
		Formats:     formats,
	}, nil
}

// ReloadRules 重新读取规则文件；校验失败时保留原有规则不变。
func (s *FormViewService) ReloadRules() (*FormViewFormatListResult, error) {
	catalog, err := s.loadRules()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.rules, s.err, s.loaded = catalog, nil, true
	s.mu.Unlock()
	result, err := s.ListFormats()
	if err != nil {
		return nil, err
	}
	emitEvent("formview:reloaded", result)
	return result, nil
}

// ProjectFile 把一个归档文件按规则投影成「段 → 行 → 列」表格（只读）。
func (s *FormViewService) ProjectFile(filePath string) (*formview.Projection, error) {
	filePath = normalizeFormViewPath(filePath)
	if filePath == "" {
		return nil, fmt.Errorf("文件路径不能为空")
	}
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	format, ok := catalog.LookupFile(filePath)
	if !ok {
		return nil, fmt.Errorf("该文件没有结构化视图规则: %s", filePath)
	}

	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	a := s.c.archive
	if a == nil {
		return nil, ErrNoArchive
	}
	index, found := a.Find(filePath)
	if !found {
		return nil, fmt.Errorf("归档内找不到文件: %s", filePath)
	}
	text, err := a.Text(index)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败（%s）: %w", filePath, err)
	}
	return formview.Project(a.Path(index), format, pvf.ParseScriptView(text)), nil
}

func (s *FormViewService) rulePath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.path == "" {
		return "(内置)"
	}
	return s.path
}

func (s *FormViewService) catalog() (formview.Catalog, error) {
	s.mu.RLock()
	if s.loaded {
		catalog, err := s.rules, s.err
		s.mu.RUnlock()
		return catalog, err
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return s.rules, s.err
	}
	rules, err := s.loadRules()
	s.rules, s.err, s.loaded = rules, err, true
	return rules, err
}

// loadRules 优先读仓库内数据文件（开发态改数据即生效），否则用内置副本。
func (s *FormViewService) loadRules() (formview.Catalog, error) {
	if s.path != "" {
		if _, statErr := os.Stat(s.path); statErr == nil {
			return formview.LoadFile(s.path)
		}
	}
	return formview.LoadDefault()
}

func normalizeFormViewPath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	return strings.Trim(value, "/")
}
