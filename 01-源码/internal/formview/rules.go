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

// Link 描述「某一列取到某些值时，本行关联同一文件里的另一段」。
//
// 真实例子：etc/independent_drop.etc 的「掉落方式」= 内联列表(1) / 外部文件(2) 时，
// 第 3 列（掉落物品）没有意义，真正的候选列表是**紧跟在该行之后**的 [list] 段。
//
// 关联关系由**文本偏移顺序**确定（目标段出现在本行之后），不靠"第 N 个配第 N 个"
// 的计数——这样即使某一行没有列表、或中间插了别的段，也不会错位。
type Link struct {
	// Column 是触发列的下标（0 基，对应 Columns 的下标）。
	Column int `json:"column"`
	// When 是触发取值：与该列 token 的原样文本比较（去首尾空白）。
	When []string `json:"when"`
	// TargetSection 是被引用段的段名（如 list），必须在本文件族里定义。
	TargetSection string `json:"targetSection"`
	// DisplayColumn 是界面上**显示关联内容并可双击打开**的列下标（0 基）。
	//
	// 与 Column 分开的原因（2026-10-03 用户要求）：独立掉落的**触发列**是最后那列
	// 「掉落方式」，但用户要看的是「掉落物品」列 —— 内联列表时那一列原本显示的
	// 是无意义的 0（会被解析成「金币 0」）。所以让规则单独指定"显示列"：
	// 交互落在显示列上，触发列只当普通标签。
	// 不写（nil）时与 Column 相同。
	DisplayColumn *int `json:"displayColumn,omitempty"`
	// Title 是界面上查看器用的标题（如「掉落候选」）。
	Title string `json:"title,omitempty"`
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
	// Links 是「本段的行 → 另一段」的关联定义（可选）。
	Links []Link `json:"links,omitempty"`
	// MaxOccurrences 是本段最多投影多少次出现（0 = 用默认上限）。
	//
	// 有些段每行数据后面跟一次（实测 etc/independent_drop.etc 的 [list] 出现 862 次），
	// 默认上限会把绝大多数挡掉 —— 那正是"内联列表打不开"的根因，所以需要在规则里放宽。
	MaxOccurrences int `json:"maxOccurrences,omitempty"`
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

// OccurrenceLimit 返回本段允许投影的最大出现次数（0 或负值用默认上限）。
func (s Section) OccurrenceLimit() int {
	if s.MaxOccurrences > 0 {
		return s.MaxOccurrences
	}
	return defaultMaxSectionOccurrences
}

// LookupSection 按段名（大小写与首尾空白不敏感）取段定义。
// sectionColumnRefs 返回每列声明的对象类型（Column.Ref），与 ColumnLabels 等长；
// 没写 ref 的列给空串。界面靠它认「哪一列是怪物 / 哪一列是掉落物品」——搜索时把
// ID 与中文名当作同一个目标（用户 2026-10-03 要求：搜名称和搜 ID 结果一致）。
func sectionColumnRefs(s Section) []string {
	refs := make([]string, 0, len(s.Columns))
	for _, column := range s.Columns {
		refs = append(refs, strings.TrimSpace(column.Ref))
	}
	return refs
}

// sectionColumnTypes 返回每列声明的类型（Column.Type），与 ColumnLabels 等长。
func sectionColumnTypes(s Section) []string {
	types := make([]string, 0, len(s.Columns))
	for _, column := range s.Columns {
		types = append(types, strings.TrimSpace(column.Type))
	}
	return types
}

// sectionColumnScales 返回每列的 rate 刻度（非 rate 列为 0），与 ColumnLabels 等长。
func sectionColumnScales(s Section) []int64 {
	scales := make([]int64, 0, len(s.Columns))
	for _, column := range s.Columns {
		scales = append(scales, column.Scale)
	}
	return scales
}

func (f Format) LookupSection(name string) (Section, bool) {
	key := strings.TrimSpace(name)
	for _, section := range f.Sections {
		if strings.EqualFold(strings.TrimSpace(section.Section), key) {
			return section, true
		}
	}
	return Section{}, false
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
			if section.MaxOccurrences < 0 {
				problems = append(problems, sectionPrefix+".maxOccurrences 不能为负")
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
			for k, link := range section.Links {
				linkPrefix := fmt.Sprintf("%s.links[%d]", sectionPrefix, k)
				if link.Column < 0 || link.Column >= len(section.Columns) {
					problems = append(problems, fmt.Sprintf(
						"%s.column(%d) 超出列范围 0..%d",
						linkPrefix, link.Column, len(section.Columns)-1))
				}
				if len(link.When) == 0 {
					problems = append(problems, linkPrefix+".when 不能为空")
				}
				if link.DisplayColumn != nil &&
					(*link.DisplayColumn < 0 || *link.DisplayColumn >= len(section.Columns)) {
					problems = append(problems, fmt.Sprintf("%s.displayColumn(%d) 超出列范围 0..%d",
						linkPrefix, *link.DisplayColumn, len(section.Columns)-1))
				}
				if strings.TrimSpace(link.TargetSection) == "" {
					problems = append(problems, linkPrefix+".targetSection 不能为空")
				} else if strings.EqualFold(strings.TrimSpace(link.TargetSection), name) {
					problems = append(problems, linkPrefix+".targetSection 不能指向本段自身")
				}
			}
		}
		// links 的目标段必须在本文件族里有定义（此时 seenSections 才收齐）。
		for j, section := range format.Sections {
			for k, link := range section.Links {
				target := strings.ToLower(strings.TrimSpace(link.TargetSection))
				if target == "" {
					continue
				}
				if !seenSections[target] {
					problems = append(problems, fmt.Sprintf(
						"%s.sections[%d].links[%d].targetSection 指向未定义的段: %s",
						prefix, j, k, link.TargetSection))
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
