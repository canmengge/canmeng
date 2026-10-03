// Package formview 定义「结构化视图」的数据模型与规则解析。
//
// 规则全部来自外部数据文件（config/formats.json）：本包不硬编码任何游戏
// 段名、列名、枚举取值或刻度常量，只做解析、校验与投影。
//
// 投影（Project）复用内核既有的 tolerant 语义投影 internal/pvf 的
// ParseScriptView —— 不重复实现一套 PVF 词法。
package formview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// 列类型。空字符串等价于 text。
const (
	ColumnTypeText = "text"
	ColumnTypeInt  = "int"
	ColumnTypeRate = "rate"
	ColumnTypeEnum = "enum"
	ColumnTypeRef  = "ref"
)

// 段呈现方式。空字符串等价于 table。
const (
	SectionKindTable = "table"
)

// Column 是一列的定义。
type Column struct {
	// Label 是列名（界面表头）。
	Label string `json:"label"`
	// Type 取值：text（默认）/ int / rate / enum / ref。
	Type string `json:"type,omitempty"`
	// Values 是 enum 的取值 → 显示文本。
	Values map[string]string `json:"values,omitempty"`
	// Ref 是 ref 的提示（对象类型提示串，用 `|` 分隔，仅用于界面提示，不做解析）。
	Ref string `json:"ref,omitempty"`
	// Scale 是 rate 的满值刻度（如 1000000 表示 100%）。
	Scale int64 `json:"scale,omitempty"`
	// NoneValue 是 ref 的「无限制」取值（如 -1）。
	NoneValue string `json:"noneValue,omitempty"`
}

// Section 是一段的定义。
type Section struct {
	// Section 是段名，写法与 ScriptElement.Section 一致（不带方括号）。
	Section string `json:"section"`
	// Label 是段的显示名。
	Label string `json:"label"`
	// Kind 取值：table（默认）。
	Kind string `json:"kind,omitempty"`
	// RowTokens 是「一行占几个 token」——切行的唯一依据。
	RowTokens int `json:"rowTokens"`
	// Columns 必须与 RowTokens 等长：第 i 个元素描述每行第 i 个 token。
	Columns []Column `json:"columns"`
	// Optional 为 true 时，本段在本文件里未出现也不告警（用于只在部分客户端存在的段）。
	Optional bool `json:"optional,omitempty"`
}

// Format 是一个文件族的结构化视图规则。
type Format struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Files 是适用的归档内路径，支持 path.Match 通配（大小写不敏感）。
	Files    []string  `json:"files"`
	Notes    string    `json:"notes,omitempty"`
	Sections []Section `json:"sections"`
}

// Catalog 是加载后的规则目录。
type Catalog struct {
	Version     int               `json:"version"`
	Description string            `json:"description,omitempty"`
	Source      map[string]string `json:"source,omitempty"`
	Formats     []Format          `json:"formats"`
}

// DisplayLabel 返回显示名，缺失时回退到 ID。
func (f Format) DisplayLabel() string {
	if label := strings.TrimSpace(f.Label); label != "" {
		return label
	}
	return strings.TrimSpace(f.ID)
}

// ColumnLabels 返回列名列表。
func (s Section) ColumnLabels() []string {
	out := make([]string, 0, len(s.Columns))
	for _, column := range s.Columns {
		label := strings.TrimSpace(column.Label)
		if label == "" {
			label = "?"
		}
		out = append(out, label)
	}
	return out
}

// SectionKind 返回归一化后的段呈现方式。
func (s Section) SectionKind() string {
	if kind := strings.TrimSpace(s.Kind); kind != "" {
		return kind
	}
	return SectionKindTable
}

// normalizeArchivePath 统一归档内路径写法，便于比较。
func normalizeArchivePath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	return strings.Trim(strings.ToLower(value), "/")
}

// MatchesFile 判断归档内路径是否命中本规则的 files 列表。
func (f Format) MatchesFile(filePath string) bool {
	normalized := normalizeArchivePath(filePath)
	if normalized == "" {
		return false
	}
	for _, pattern := range f.Files {
		candidate := normalizeArchivePath(pattern)
		if candidate == "" {
			continue
		}
		if candidate == normalized {
			return true
		}
		if strings.ContainsAny(candidate, "*?[") {
			if ok, err := path.Match(candidate, normalized); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// LookupFile 按归档内路径找规则；多条命中时取第一条（按 formats 顺序）。
func (c Catalog) LookupFile(filePath string) (Format, bool) {
	for _, format := range c.Formats {
		if format.MatchesFile(filePath) {
			return format, true
		}
	}
	return Format{}, false
}

// Lookup 按 id 或 label 查找规则（大小写无关）。
func (c Catalog) Lookup(id string) (Format, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	if key == "" {
		return Format{}, false
	}
	for _, format := range c.Formats {
		if strings.ToLower(strings.TrimSpace(format.ID)) == key {
			return format, true
		}
	}
	for _, format := range c.Formats {
		if strings.ToLower(format.DisplayLabel()) == key {
			return format, true
		}
	}
	return Format{}, false
}

// Sorted 返回按显示名排序的规则列表，供界面稳定展示。
func (c Catalog) Sorted() []Format {
	out := append([]Format(nil), c.Formats...)
	sort.SliceStable(out, func(i, j int) bool {
		left, right := out[i].DisplayLabel(), out[j].DisplayLabel()
		if left != right {
			return left < right
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Parse 解析并校验结构化视图规则。
func Parse(data []byte) (Catalog, error) {
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("解析结构化视图规则失败: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Catalog{}, fmt.Errorf("结构化视图规则只能包含一个 JSON 文档")
		}
		return Catalog{}, fmt.Errorf("解析结构化视图规则失败: %w", err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// Validate 校验目录结构与每条规则。
func Validate(catalog Catalog) error {
	problems := make([]string, 0)
	if catalog.Version != 1 {
		problems = append(problems, fmt.Sprintf("version 必须为 1，当前为 %d", catalog.Version))
	}
	if len(catalog.Formats) == 0 {
		problems = append(problems, "formats 不能为空")
	}
	seenIDs := make(map[string]bool, len(catalog.Formats))
	for i, format := range catalog.Formats {
		prefix := fmt.Sprintf("formats[%d]", i)
		id := strings.TrimSpace(format.ID)
		switch {
		case id == "":
			problems = append(problems, prefix+".id 不能为空")
		case seenIDs[strings.ToLower(id)]:
			problems = append(problems, prefix+".id 重复: "+id)
		}
		seenIDs[strings.ToLower(id)] = true

		if strings.TrimSpace(format.Label) == "" {
			problems = append(problems, prefix+".label 不能为空")
		}
		if len(format.Files) == 0 {
			problems = append(problems, prefix+".files 不能为空")
		}
		for _, file := range format.Files {
			if normalizeArchivePath(file) == "" {
				problems = append(problems, prefix+".files 含空白路径")
				break
			}
		}
		if len(format.Sections) == 0 {
			problems = append(problems, prefix+".sections 不能为空")
		}
		seenSections := make(map[string]bool, len(format.Sections))
		for j, section := range format.Sections {
			sectionPrefix := fmt.Sprintf("%s.sections[%d]", prefix, j)
			name := strings.TrimSpace(section.Section)
			switch {
			case name == "":
				problems = append(problems, sectionPrefix+".section 不能为空")
			case seenSections[strings.ToLower(name)]:
				problems = append(problems, sectionPrefix+".section 重复: "+name)
			}
			seenSections[strings.ToLower(name)] = true

			if strings.TrimSpace(section.Label) == "" {
				problems = append(problems, sectionPrefix+".label 不能为空")
			}
			switch section.SectionKind() {
			case SectionKindTable:
			default:
				problems = append(problems, sectionPrefix+".kind 只支持 table，当前为 "+section.Kind)
			}
			if section.RowTokens < 1 {
				problems = append(problems, sectionPrefix+".rowTokens 必须 ≥ 1")
			}
			// 列定义必须与 rowTokens 一一对应 —— 这是「表格分列」的命名契约。
			if len(section.Columns) != section.RowTokens {
				problems = append(problems, fmt.Sprintf(
					"%s.columns 长度(%d) 必须等于 rowTokens(%d)",
					sectionPrefix, len(section.Columns), section.RowTokens))
			}
			for k, column := range section.Columns {
				columnPrefix := fmt.Sprintf("%s.columns[%d]", sectionPrefix, k)
				if strings.TrimSpace(column.Label) == "" {
					problems = append(problems, columnPrefix+".label 不能为空")
				}
				switch columnType(column) {
				case ColumnTypeText, ColumnTypeInt:
				case ColumnTypeRate:
					if column.Scale <= 0 {
						problems = append(problems, columnPrefix+".scale 在 rate 列上必须为正")
					}
				case ColumnTypeEnum:
					if len(column.Values) == 0 {
						problems = append(problems, columnPrefix+".values 在 enum 列上不能为空")
					}
				case ColumnTypeRef:
					if strings.TrimSpace(column.Ref) == "" {
						problems = append(problems, columnPrefix+".ref 在 ref 列上不能为空")
					}
				default:
					problems = append(problems, columnPrefix+".type 不支持: "+column.Type)
				}
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("结构化视图规则校验失败:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

// columnType 返回归一化后的列类型。
func columnType(column Column) string {
	if value := strings.TrimSpace(column.Type); value != "" {
		return value
	}
	return ColumnTypeText
}
