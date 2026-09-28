package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 用户自定义路径注释：文件树/编辑器右键「编辑注释」的读写层。
//
// 存储：%AppConfig%\pvfine\path-annotations.json（与 settings/file-sets 同目录，
// 不放 HC 缓存目录 —— HC 是可删缓存，用户数据必须持久）。
// 合并语义：某路径存在用户注释时，**整体替换**内置注释规则结果（用户优先），
// 写入/删除后立即清空 core 的路径标注缓存，文件树/搜索/面包屑当场刷新。

// PathAnnotationOverride 是一条用户注释（标题=树上的标签文本，内容=悬停说明）。
type PathAnnotationOverride struct {
	Title     string `json:"title"`
	Content   string `json:"content,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// PathAnnotationEntry 是 List 返回的一条（带路径，按路径排序）。
type PathAnnotationEntry struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	Content   string `json:"content,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// PathAnnotationResult 是 Get 的返回：found=false 表示该路径没有用户注释
//（树上看到的是内置规则给出的）。
type PathAnnotationResult struct {
	Found    bool                    `json:"found"`
	Override *PathAnnotationOverride `json:"override,omitempty"`
}

// PathAnnotationService 管理用户路径注释的持久化与生效。
type PathAnnotationService struct {
	c         *core
	mu        sync.Mutex
	path      string
	initErr   error
	overrides map[string]PathAnnotationOverride
}

// NewPathAnnotationService 创建服务并装载用户注释文件；文件缺失/损坏不报错
//（与 filesets 同策略：用户数据读不出来时退回内置注释，应用照常工作）。
func NewPathAnnotationService(c *core) *PathAnnotationService {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return &PathAnnotationService{c: c, initErr: fmt.Errorf("获取用户配置目录失败: %w", err)}
	}
	return newPathAnnotationService(c, filepath.Join(configDir, "pvfine", "path-annotations.json"))
}

func newPathAnnotationService(c *core, path string) *PathAnnotationService {
	service := &PathAnnotationService{c: c, path: path}
	service.load()
	c.replaceAnnotationOverrides(service.overrides)
	return service
}

// load 读取注释文件；损坏时保留空表并记录 initErr（写入时会向用户报错）。
// 路径键归一化复用 annotations.go 的 normalizeAnnotationPath（正斜杠/去首尾/小写）。
func (s *PathAnnotationService) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrides = make(map[string]PathAnnotationOverride)
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.initErr = fmt.Errorf("读取用户注释失败: %w", err)
		}
		return
	}
	var document struct {
		Overrides map[string]PathAnnotationOverride `json:"overrides"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		s.initErr = fmt.Errorf("解析用户注释失败: %w", err)
		return
	}
	if document.Overrides != nil {
		s.overrides = document.Overrides
	}
}

// saveLocked 把当前表写盘（调用方须已持有 s.mu）；空表时删除文件。
func (s *PathAnnotationService) saveLocked() error {
	if len(s.overrides) == 0 {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除空用户注释文件失败: %w", err)
		}
		return nil
	}
	document := struct {
		Overrides map[string]PathAnnotationOverride `json:"overrides"`
	}{Overrides: s.overrides}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("编码用户注释失败: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("创建用户注释目录失败: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("写入用户注释失败: %w", err)
	}
	return nil
}

// Get 查询单个路径的用户注释（供前端打开编辑对话框时回填当前值）。
func (s *PathAnnotationService) Get(path string) (PathAnnotationResult, error) {
	key := normalizeAnnotationPath(path)
	if key == "" {
		return PathAnnotationResult{}, fmt.Errorf("路径不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	override, ok := s.overrides[key]
	if !ok {
		return PathAnnotationResult{Found: false}, nil
	}
	value := override
	return PathAnnotationResult{Found: true, Override: &value}, nil
}

// List 返回全部用户注释（按路径排序，供设置/排查用）。
func (s *PathAnnotationService) List() ([]PathAnnotationEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := make([]PathAnnotationEntry, 0, len(s.overrides))
	for path, override := range s.overrides {
		entries = append(entries, PathAnnotationEntry{
			Path:      path,
			Title:     override.Title,
			Content:   override.Content,
			UpdatedAt: override.UpdatedAt,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// Set 写入（或覆盖）一条用户注释：立即生效（清 core 标注缓存）并持久化。
// title 为空表示删除 —— 与 Remove 等价，方便前端「清空标题即清除」的交互。
func (s *PathAnnotationService) Set(path string, title string, content string) error {
	key := normalizeAnnotationPath(path)
	if key == "" {
		return fmt.Errorf("路径不能为空")
	}
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" {
		return s.Remove(path)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return s.initErr
	}
	s.overrides[key] = PathAnnotationOverride{
		Title:     title,
		Content:   content,
		UpdatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.c.setAnnotationOverride(key, s.overrides[key])
	return nil
}

// Remove 删除一条用户注释，该路径回退到内置注释规则。
func (s *PathAnnotationService) Remove(path string) error {
	key := normalizeAnnotationPath(path)
	if key == "" {
		return fmt.Errorf("路径不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.overrides[key]; !exists {
		return nil
	}
	if s.initErr != nil {
		return s.initErr
	}
	delete(s.overrides, key)
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.c.removeAnnotationOverride(key)
	return nil
}
