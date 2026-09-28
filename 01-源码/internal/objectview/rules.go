// Package objectview 定义"对象视图"的数据模型与规则解析。
//
// 规则全部来自外部数据文件（config/objectview.json）：本包不硬编码任何
// 游戏对象类型、lst 路径或字符串表号，只做解析、校验与查询。
package objectview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ObjectType 描述一类游戏对象如何从归档里解析出它的脚本、登记与显示文本。
type ObjectType struct {
	// ID 是稳定标识（前端/对话用它选类型）。
	ID string `json:"id"`
	// Label 是显示名。
	Label string `json:"label"`
	// ListPath 是单个 .lst；写 90US 路径即可，Archive.FindList 会兼容
	// 110US 的 `list/` 布局。
	ListPath string `json:"listPath,omitempty"`
	// ListPaths 用于按职业分表的类型（如技能）。
	ListPaths []string `json:"listPaths,omitempty"`
	// Extensions 是期望的脚本扩展名，用于一致性提示。
	Extensions []string `json:"extensions,omitempty"`
	// StringTable 是显示文本所在字符串表号；缺省表示不做表查询，
	// 只靠脚本内占位符扫描。
	StringTable *int `json:"stringTable,omitempty"`
	// KeyPatterns 是兜底键名模式，`{id}` 会被替换为对象 ID。
	KeyPatterns []string `json:"keyPatterns,omitempty"`
	// Notes 记录校准依据与限制。
	Notes string `json:"notes,omitempty"`
}

// Catalog 是加载后的对象类型目录。
type Catalog struct {
	Version     int               `json:"version"`
	Description string            `json:"description,omitempty"`
	Source      map[string]string `json:"source,omitempty"`
	ObjectTypes []ObjectType      `json:"objectTypes"`
}

// DisplayLabel 返回显示名，缺失时回退到 ID。
func (t ObjectType) DisplayLabel() string {
	if label := strings.TrimSpace(t.Label); label != "" {
		return label
	}
	return strings.TrimSpace(t.ID)
}

// AllListPaths 返回按优先级排列的候选 .lst 路径（去重、去空白）。
func (t ObjectType) AllListPaths() []string {
	out := make([]string, 0, len(t.ListPaths)+1)
	seen := make(map[string]struct{}, len(t.ListPaths)+1)
	appendPath := func(candidate string) {
		candidate = strings.Trim(strings.ReplaceAll(strings.TrimSpace(candidate), "\\", "/"), "/")
		if candidate == "" {
			return
		}
		key := strings.ToLower(candidate)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	appendPath(t.ListPath)
	for _, listPath := range t.ListPaths {
		appendPath(listPath)
	}
	return out
}

// KeyPatternsFor 把 KeyPatterns 里的 `{id}` 替换为对象 ID。
func (t ObjectType) KeyPatternsFor(objectID string) []string {
	out := make([]string, 0, len(t.KeyPatterns))
	for _, pattern := range t.KeyPatterns {
		out = append(out, strings.ReplaceAll(pattern, "{id}", objectID))
	}
	return out
}

// Parse 解析并校验对象视图规则。
func Parse(data []byte) (Catalog, error) {
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("解析对象视图规则失败: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Catalog{}, fmt.Errorf("对象视图规则只能包含一个 JSON 文档")
		}
		return Catalog{}, fmt.Errorf("解析对象视图规则失败: %w", err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// Validate 校验目录结构与每条对象类型。
func Validate(catalog Catalog) error {
	problems := make([]string, 0)
	if catalog.Version != 1 {
		problems = append(problems, fmt.Sprintf("version 必须为 1，当前为 %d", catalog.Version))
	}
	if len(catalog.ObjectTypes) == 0 {
		problems = append(problems, "objectTypes 不能为空")
	}
	seenIDs := make(map[string]bool, len(catalog.ObjectTypes))
	for i, objectType := range catalog.ObjectTypes {
		prefix := fmt.Sprintf("objectTypes[%d]", i)
		id := strings.TrimSpace(objectType.ID)
		switch {
		case id == "":
			problems = append(problems, prefix+".id 不能为空")
		case seenIDs[strings.ToLower(id)]:
			problems = append(problems, prefix+".id 重复: "+id)
		}
		seenIDs[strings.ToLower(id)] = true

		if strings.TrimSpace(objectType.Label) == "" {
			problems = append(problems, prefix+".label 不能为空")
		}
		listPaths := objectType.AllListPaths()
		if len(listPaths) == 0 {
			problems = append(problems, prefix+" 必须配置 listPath 或 listPaths")
		}
		for _, listPath := range listPaths {
			if !strings.HasSuffix(strings.ToLower(listPath), ".lst") {
				problems = append(problems, prefix+" 的 lst 路径必须以 .lst 结尾: "+listPath)
			}
		}
		for _, extension := range objectType.Extensions {
			if extension == "" || !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, "/\\") {
				problems = append(problems, prefix+".extensions 必须是以点开头的文件后缀")
				break
			}
		}
		if objectType.StringTable != nil && *objectType.StringTable < 0 {
			problems = append(problems, prefix+".stringTable 不能为负数")
		}
		for _, pattern := range objectType.KeyPatterns {
			if !strings.Contains(pattern, "{id}") {
				problems = append(problems, prefix+".keyPatterns 必须包含 {id} 占位符: "+pattern)
				break
			}
		}
		if objectType.StringTable == nil && len(objectType.KeyPatterns) > 0 {
			problems = append(problems, prefix+" 配置了 keyPatterns 但缺少 stringTable")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("对象视图规则校验失败:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

// Lookup 按 id 或 label 查找对象类型（大小写无关）。
func (c Catalog) Lookup(id string) (ObjectType, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	if key == "" {
		return ObjectType{}, false
	}
	for _, objectType := range c.ObjectTypes {
		if strings.ToLower(strings.TrimSpace(objectType.ID)) == key {
			return objectType, true
		}
	}
	for _, objectType := range c.ObjectTypes {
		if strings.ToLower(strings.TrimSpace(objectType.Label)) == key {
			return objectType, true
		}
	}
	return ObjectType{}, false
}

// Sorted 返回按显示名排序的类型列表，供界面稳定展示。
func (c Catalog) Sorted() []ObjectType {
	out := append([]ObjectType(nil), c.ObjectTypes...)
	sort.SliceStable(out, func(i, j int) bool {
		left, right := out[i].DisplayLabel(), out[j].DisplayLabel()
		if left != right {
			return left < right
		}
		return out[i].ID < out[j].ID
	})
	return out
}
