package annotations

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appconfig "pvfine/config"
)

// EnvAnnotationDir 可用环境变量显式指定外置注释目录。
const EnvAnnotationDir = "PVFINE_ANNOTATION_DIR"

// ExternalDirName 是程序目录下的默认外置注释目录名。
const ExternalDirName = "注释数据"

// ExternalSummary 描述一次外置注释加载的结果（供界面显示与排查，不参与规则解析）。
type ExternalSummary struct {
	Dir        string   `json:"dir"`
	Found      bool     `json:"found"`
	DurationMs int64    `json:"durationMs"`
	FieldFiles int      `json:"fieldFiles"`
	Fields     int      `json:"fields"`
	Rules      int      `json:"rules"`
	PathRules  int      `json:"pathRules"`
	HoverRules int      `json:"hoverRules"`
	Relations  int      `json:"relations"`
	Warnings   []string `json:"warnings,omitempty"`
}

// FindExternalDir 解析外置注释目录：
//  1. 环境变量 PVFINE_ANNOTATION_DIR（显式指定）
//  2. <exe 同目录>\注释数据
//
// 返回值第二项表示该目录是否可用（存在且含 fields/ 或 paths/）。
func FindExternalDir() (string, bool) {
	if env := strings.TrimSpace(os.Getenv(EnvAnnotationDir)); env != "" {
		return env, isExternalDir(env)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir := filepath.Join(filepath.Dir(exe), ExternalDirName)
	return dir, isExternalDir(dir)
}

func isExternalDir(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	for _, name := range []string{"fields", "paths", "hover"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// LoadWithExternal 加载「内置基础注解 + 外置注释目录」，返回合并后的文档与加载统计。
// 外置目录缺失或损坏时**不报错**，退回内置（注释变少但程序照常工作）。
func LoadWithExternal() (Document, ExternalSummary, error) {
	base, err := parseDocument(appconfig.AnnotationsJSON)
	if err != nil {
		return Document{}, ExternalSummary{}, err
	}
	if lists, listErr := ParseLists(appconfig.ListsJSON); listErr == nil {
		base.Relations = lists.Relations
	}

	dir, found := FindExternalDir()
	summary := ExternalSummary{Dir: dir, Found: found}
	if !found {
		summary.Warnings = append(summary.Warnings, "未找到外置注释目录（仅使用内置基础注解）: "+dir)
		return base, summary, nil
	}

	external, extSummary, err := loadExternalDocuments(dir)
	if err != nil {
		summary.Warnings = append(summary.Warnings, "外置注释加载失败，已退回内置基础注解: "+err.Error())
		return base, summary, nil
	}
	extSummary.Dir = dir
	extSummary.Found = true
	// 内置基础注解里的预览规格需要保留：合并后仍能预览装备等结构化文件。
	return mergeDocuments(base, external), extSummary, nil
}

// LoadPreferredWithSummary 与 LoadDefault 等价，但额外返回外置加载统计。
func LoadPreferredWithSummary() (*Engine, ExternalSummary, error) {
	document, summary, err := LoadWithExternal()
	if err != nil {
		return nil, summary, err
	}
	engine, err := Compile(document)
	if err != nil {
		return nil, summary, err
	}
	return engine, summary, nil
}

// loadExternalDocuments 读取 注释数据\ 下的全部分片（fields/、paths/、hover/、relations/）。
func loadExternalDocuments(dir string) (Document, ExternalSummary, error) {
	started := time.Now()
	summary := ExternalSummary{}
	merged := Document{Version: 1, Relations: map[string]RelationSpec{}}

	readDocuments := func(sub string) ([]Document, []string) {
		folder := filepath.Join(dir, sub)
		entries, err := os.ReadDir(folder)
		if err != nil {
			return nil, nil
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
				continue
			}
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		documents := make([]Document, 0, len(names))
		warnings := make([]string, 0)
		for _, name := range names {
			data, readErr := os.ReadFile(filepath.Join(folder, name))
			if readErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s/%s 读取失败: %v", sub, name, readErr))
				continue
			}
			document, parseErr := parseDocument(data)
			if parseErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s/%s 解析失败: %v", sub, name, parseErr))
				continue
			}
			if sub == "paths" && isDirectoryAnnotationShard(name) {
				markPathRulesDirOnly(&document)
			}
			documents = append(documents, document)
		}
		return documents, warnings
	}

	fieldDocs, warnings := readDocuments("fields")
	summary.FieldFiles = len(fieldDocs)
	summary.Warnings = append(summary.Warnings, warnings...)
	for _, document := range fieldDocs {
		counts := countDocument(document)
		summary.Fields += counts.fields
		summary.Rules += counts.rules
		merged = mergeDocuments(merged, document)
	}

	pathDocs, pathWarnings := readDocuments("paths")
	summary.Warnings = append(summary.Warnings, pathWarnings...)
	for _, document := range pathDocs {
		summary.PathRules += len(document.Rules)
		summary.Rules += len(document.Rules)
		merged = mergeDocuments(merged, document)
	}

	hoverDocs, hoverWarnings := readDocuments("hover")
	summary.Warnings = append(summary.Warnings, hoverWarnings...)
	for _, document := range hoverDocs {
		// hover 分片以 fields 形式提供 token 规则（历史写法），统计时两者都要算。
		count := len(document.Rules) + len(document.Fields)
		summary.HoverRules += count
		summary.Rules += count
		merged = mergeDocuments(merged, document)
	}

	relationsFile := filepath.Join(dir, "relations", "lists.json")
	if data, err := os.ReadFile(relationsFile); err == nil {
		if lists, parseErr := ParseLists(data); parseErr == nil {
			for name, relation := range lists.Relations {
				merged.Relations[name] = relation
			}
			summary.Relations = len(lists.Relations)
		} else {
			summary.Warnings = append(summary.Warnings, "relations/lists.json 解析失败: "+parseErr.Error())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		summary.Warnings = append(summary.Warnings, "relations/lists.json 读取失败: "+err.Error())
	}

	if summary.Fields+summary.Rules+len(merged.Relations) == 0 {
		return Document{}, summary, errors.New("外置注释目录里没有可用内容")
	}
	summary.DurationMs = time.Since(started).Milliseconds()
	return merged, summary, nil
}

// isDirectoryAnnotationShard 判断 paths/ 下的分片是否为「目录注释」分片
// （历史约定：directories.json）。以下划线开头的辅助文件（如
// _diff-vs-builtin.json）一律跳过。
func isDirectoryAnnotationShard(name string) bool {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, "_") {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "director") || strings.Contains(trimmed, "目录")
}

// markPathRulesDirOnly 把分片里的路径规则标记为"仅目录"。
func markPathRulesDirOnly(document *Document) {
	for i := range document.Rules {
		if document.Rules[i].Target.Kind == "path" {
			document.Rules[i].DirOnly = true
		}
	}
}

func countDocument(document Document) (counts struct{ fields, rules int }) {
	counts.fields = len(document.Fields)
	counts.rules = len(document.Rules)
	return counts
}

// mergeDocuments 把 overlay 合并进 base：
//   - Relations / Rules 按键覆盖；
//   - Fields 按「扩展名+段名」覆盖标题与正文，但**保留 base 的预览规格**。
func mergeDocuments(base, overlay Document) Document {
	if base.Version == 0 {
		base.Version = 1
	}
	if base.Relations == nil {
		base.Relations = make(map[string]RelationSpec)
	}
	for name, relation := range overlay.Relations {
		base.Relations[name] = relation
	}

	fields := make([]FieldDefinition, 0, len(base.Fields)+len(overlay.Fields))
	indexByKey := make(map[string]int, len(base.Fields))
	for _, field := range base.Fields {
		indexByKey[annotationFieldKey(field)] = len(fields)
		fields = append(fields, field)
	}
	for _, field := range overlay.Fields {
		key := annotationFieldKey(field)
		if index, ok := indexByKey[key]; ok {
			if fields[index].Preview != nil && field.Preview == nil {
				field.Preview = fields[index].Preview
			}
			fields[index] = field
			continue
		}
		indexByKey[key] = len(fields)
		fields = append(fields, field)
	}
	base.Fields = fields

	rules := make([]Rule, 0, len(base.Rules)+len(overlay.Rules))
	indexByID := make(map[string]int, len(base.Rules))
	for _, rule := range base.Rules {
		indexByID[rule.ID] = len(rules)
		rules = append(rules, rule)
	}
	for _, rule := range overlay.Rules {
		if index, ok := indexByID[rule.ID]; ok {
			rules[index] = rule
			continue
		}
		indexByID[rule.ID] = len(rules)
		rules = append(rules, rule)
	}
	base.Rules = rules
	return base
}

func annotationFieldKey(field FieldDefinition) string {
	extension := "*"
	if len(field.Match.Extensions) > 0 {
		extension = strings.ToLower(strings.TrimSpace(field.Match.Extensions[0]))
	}
	if glob := strings.ToLower(strings.TrimSpace(field.Match.Glob)); glob != "" {
		return extension + "|glob:" + glob
	}
	return extension + "|" + strings.ToLower(strings.TrimSpace(field.Target.Section))
}

// MarshalDocumentSummary 输出一行可读的统计文本（日志用）。
func (s ExternalSummary) String() string {
	status := "未启用"
	if s.Found {
		status = "已启用"
	}
	return fmt.Sprintf("外置注释%s 目录=%s 字段=%d 规则=%d（路径 %d / 引用 %d）关系=%d 耗时=%dms",
		status, s.Dir, s.Fields, s.Rules, s.PathRules, s.HoverRules, s.Relations, s.DurationMs)
}

// 供测试与调用方复用：把 Document 编码为 JSON（不导出字段顺序细节）。
func marshalDocument(document Document) ([]byte, error) {
	return json.Marshal(document)
}
