package services

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AITool 是 AI 助手可调用的一个工具（方案见 AI镶嵌.md §四.3）。
// 这是唯一真源：内置对话与将来的 MCP 服务共用同一张表，避免两套逻辑漂移。
// ReadOnly=false 的写工具受「AI 写保护」门禁约束（见 services/ai.go）。
type AITool struct {
	Name        string
	Description string
	Schema      map[string]any
	ReadOnly    bool
	Run         func(args string) (string, error)
}

const (
	// aiMaxOutputRunes 限制单个工具给模型的最大回显，防止大文件撑爆上下文。
	aiMaxOutputRunes = 20000
	// aiMaxListItems 是目录/搜索类工具的单次返回条数上限。
	aiMaxListItems = 50
	// aiMaxFieldMatches 是字段语义查询的返回上限。
	aiMaxFieldMatches = 20
)

// NewAIToolSource 返回一个「工具注册表来源」，供 MCP 等外部暴露方与内置 AI 助手
// 共用同一份工具（AI镶嵌.md §八：单一真源，避免两套逻辑漂移）。
func NewAIToolSource(c *core, settings *SettingsService) func() []AITool {
	return func() []AITool { return buildAITools(c, settings) }
}

// guardWrite 包装写工具：统一 recover 兜底 + 统一错误前缀。
// 无论由内置 AI 助手还是外部 MCP 客户端调用，写工具都只改内存覆盖层，
// 归档保存（落盘）永远由人类完成。
func guardWrite(name string, run func(args string) (string, error)) func(args string) (string, error) {
	return func(args string) (out string, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%s 执行异常（已拦截）: %v", name, r)
			}
		}()
		out, err = run(args)
		if err != nil {
			return "", fmt.Errorf("%s 失败: %w", name, err)
		}
		return out, nil
	}
}

// buildAITools 组装当前可用的全部 AI 工具。所有工具都复用既有服务与既有的
// 写保护/备份链路，不另起炉灶。
func buildAITools(c *core, settings *SettingsService) []AITool {
	archive := NewArchiveService(c)
	editor := NewEditorService(c, settings)
	objectView := NewObjectViewService(c)

	tools := []AITool{
		{
			Name:        "search_files",
			Description: "在打开的 PVF 归档中按名称、ID 或路径搜索文件（不区分大小写，支持 * ? 通配符）。结果按相关性排序（名称全等 > 名称前缀 > 名称包含 > ID > 路径包含），一次调用即返回最相关命中，无需换关键词重搜。",
			Schema: aiObjectSchema(map[string]any{
				"query": map[string]any{"type": "string", "description": "关键词，如 117530002、belt、装备名或中文名（如 赫西亚瑞）"},
				"limit": map[string]any{"type": "integer", "description": "返回上限，默认 20，最大 100"},
				"scope": map[string]any{"type": "string", "description": "可选；只在该目录前缀内搜索，如 stackable/shop、equipment/character/mage"},
			}, []string{"query"}),
			ReadOnly: true,
			Run: func(args string) (string, error) {
				parsed, err := aiParseArgs(args)
				if err != nil {
					return "", err
				}
				query := aiStringArg(parsed, "query")
				if strings.TrimSpace(query) == "" {
					return "", fmt.Errorf("query 不能为空")
				}
				limit := clampInt(aiIntArg(parsed, "limit", 20), 1, 100)
				result, err := archive.SearchRanked(query, aiStringArg(parsed, "scope"), limit)
				if err != nil {
					return "", err
				}
				hits := make([]map[string]any, 0, len(result.Hits))
				for _, hit := range result.Hits {
					hits = append(hits, map[string]any{
						"path":      hit.Path,
						"id":        hit.ID,
						"name":      hit.Name,
						"category":  hit.Category,
						"fileIndex": hit.FileIndex,
					})
				}
				return aiJSON(map[string]any{"count": len(hits), "hits": hits}), nil
			},
		},
		{
			Name:        "read_file",
			Description: "读取归档内一个文本文件的反编译内容（每行带「行号:」前缀，行号与编辑器一致）；引用位置时请使用「归档路径:行号」，如 equipment/character/common/jacket/cloth/vest_owool.equ:7。超过编辑上限的大文件只会返回占位提示。",
			Schema: aiObjectSchema(map[string]any{
				"path": map[string]any{"type": "string", "description": "归档内路径，如 equipment/character/common/jacket/cloth/vest_owool.equ"},
			}, []string{"path"}),
			ReadOnly: true,
			Run: func(args string) (string, error) {
				parsed, err := aiParseArgs(args)
				if err != nil {
					return "", err
				}
				path := aiStringArg(parsed, "path")
				index, err := aiFindObjectIndex(c, path)
				if err != nil {
					return "", err
				}
				meta, err := editor.GetFile(index)
				if err != nil {
					return "", err
				}
				if !meta.Editable {
					// 超上限/不支持的类型：占位文本本身就是给用户看的解释，原样给模型。
					return meta.Text, nil
				}
				// 带行号返回：模型据此才能引用「路径:行号」的精确位置（与编辑器行号一致）。
				return aiNumberLines(aiTruncate(meta.Text, aiMaxOutputRunes)), nil
			},
		},
		{
			Name:        "list_directory",
			Description: "列出归档内某个目录的直接子节点；path 传空字符串表示根目录。单次最多 50 条，超出部分用 offset 翻页。",
			Schema: aiObjectSchema(map[string]any{
				"path":   map[string]any{"type": "string", "description": "目录路径，空字符串表示根目录"},
				"offset": map[string]any{"type": "integer", "description": "可选；从第几条开始返回，用于翻页"},
			}, nil),
			ReadOnly: true,
			Run: func(args string) (string, error) {
				parsed, err := aiParseArgs(args)
				if err != nil {
					return "", err
				}
				nodes, err := archive.ListChildren(aiStringArg(parsed, "path"))
				if err != nil {
					return "", err
				}
				offset := clampInt(aiIntArg(parsed, "offset", 0), 0, len(nodes))
				end := offset + aiMaxListItems
				if end > len(nodes) {
					end = len(nodes)
				}
				entries := make([]map[string]any, 0, end-offset)
				for _, node := range nodes[offset:end] {
					entries = append(entries, map[string]any{
						"path":       node.Path,
						"isDir":      node.IsDir,
						"childCount": node.ChildCount,
						"size":       node.Size,
					})
				}
				result := map[string]any{"count": len(nodes), "offset": offset, "entries": entries}
				if end < len(nodes) {
					result["nextOffset"] = end
				}
				return aiJSON(result), nil
			},
		},
		{
			Name:        "object_view",
			Description: "按对象类型 + ID 聚合对象视图（脚本/显示文本/登记表/索引/引用方）；objectType 留空时返回全部对象类型目录。",
			Schema: aiObjectSchema(map[string]any{
				"objectType": map[string]any{"type": "string", "description": "对象类型 ID，如 equipment；留空则列出全部类型"},
				"objectID":   map[string]any{"type": "string", "description": "对象 ID，如 10018"},
			}, nil),
			ReadOnly: true,
			Run: func(args string) (string, error) {
				parsed, err := aiParseArgs(args)
				if err != nil {
					return "", err
				}
				objectType := strings.TrimSpace(aiStringArg(parsed, "objectType"))
				if objectType == "" {
					types, err := objectView.ListObjectTypes()
					if err != nil {
						return "", err
					}
					return aiJSON(map[string]any{"objectTypeCount": types.ObjectTypeCount, "types": types.Types}), nil
				}
				view, err := objectView.ResolveObject(objectType, aiStringArg(parsed, "objectID"))
				if err != nil {
					return "", err
				}
				return aiTruncate(aiJSON(view), aiMaxOutputRunes), nil
			},
		},
		{
			Name:        "field_semantics",
			Description: "查询注释数据里的字段语义（含义/取值说明）；可按关键词、扩展名过滤。",
			Schema: aiObjectSchema(map[string]any{
				"keyword":   map[string]any{"type": "string", "description": "关键词，如 name、explain、grade"},
				"extension": map[string]any{"type": "string", "description": "扩展名过滤，如 .equ、.str"},
				"limit":     map[string]any{"type": "integer", "description": "返回上限，默认 20，最大 50"},
			}, nil),
			ReadOnly: true,
			Run: func(args string) (string, error) {
				parsed, err := aiParseArgs(args)
				if err != nil {
					return "", err
				}
				keyword := strings.ToLower(strings.TrimSpace(aiStringArg(parsed, "keyword")))
				extension := strings.ToLower(strings.TrimSpace(aiStringArg(parsed, "extension")))
				limit := clampInt(aiIntArg(parsed, "limit", aiMaxFieldMatches), 1, 50)

				c.mu.RLock()
				engineAvailable := c.annotationEngine != nil
				fields := make([]aiFieldInfo, 0, limit)
				if engineAvailable {
					document := c.annotationEngine.Document()
					for _, field := range document.Fields {
						if extension != "" && !aiExtensionMatch(field.Match.Extensions, extension) {
							continue
						}
						if keyword != "" {
							haystack := strings.ToLower(field.ID + " " + field.Annotation.Title + " " +
								field.Annotation.Content + " " + field.Target.Section)
							if !strings.Contains(haystack, keyword) {
								continue
							}
						}
						fields = append(fields, aiFieldInfo{
							ID:         field.ID,
							Title:      field.Annotation.Title,
							Section:    field.Target.Section,
							Extensions: field.Match.Extensions,
							Content:    aiTruncate(field.Annotation.Content, 300),
						})
						if len(fields) >= limit {
							break
						}
					}
				}
				c.mu.RUnlock()

				if !engineAvailable {
					return "", fmt.Errorf("注释引擎未加载")
				}
				return aiJSON(map[string]any{"matched": len(fields), "fields": fields}), nil
			},
		},
		{
			Name:        "resolve_string",
			Description: "查询字符串表译文（表号 + 键），如表 3 的 name_10018。",
			Schema: aiObjectSchema(map[string]any{
				"tableIndex": map[string]any{"type": "integer", "description": "字符串表号，如 3"},
				"key":        map[string]any{"type": "string", "description": "键名，如 name_10018"},
			}, []string{"tableIndex", "key"}),
			ReadOnly: true,
			Run: func(args string) (string, error) {
				parsed, err := aiParseArgs(args)
				if err != nil {
					return "", err
				}
				tableIndex := aiIntArg(parsed, "tableIndex", 0)
				key := aiStringArg(parsed, "key")
				if strings.TrimSpace(key) == "" {
					return "", fmt.Errorf("key 不能为空")
				}
				c.mu.RLock()
				defer c.mu.RUnlock()
				if c.archive == nil {
					return "", ErrNoArchive
				}
				resolution, found := c.archive.ResolveStringTable(tableIndex, key)
				if !found {
					return aiJSON(map[string]any{"found": false}), nil
				}
				return aiJSON(map[string]any{
					"found":    true,
					"text":     resolution.Text,
					"source":   resolution.Source,
					"fallback": resolution.Fallback,
				}), nil
			},
		},
		{
			Name:        "edit_file",
			Description: "把一个文本文件的完整新内容写入内存覆盖层（不会立即落盘；归档保存属于人类操作，AI 禁止自动归档）。外部 MCP 客户端调用时同样受「AI 写保护」门禁约束。",
			Schema: aiObjectSchema(map[string]any{
				"path": map[string]any{"type": "string", "description": "归档内路径"},
				"text": map[string]any{"type": "string", "description": "完整的新文件内容"},
			}, []string{"path", "text"}),
			ReadOnly: false,
			Run: guardWrite("edit_file", func(args string) (string, error) {
				parsed, err := aiParseArgs(args)
				if err != nil {
					return "", err
				}
				path := aiStringArg(parsed, "path")
				text := aiStringArg(parsed, "text")
				index, err := aiFindObjectIndex(c, path)
				if err != nil {
					return "", err
				}
				if err := editor.SetText(index, text); err != nil {
					return "", err
				}
				return "已写入内存覆盖层（尚未保存到磁盘；归档保存属于人类操作，请提示用户手动保存）", nil
			}),
		},
		{
			Name:        "save_archive",
			Description: "【已禁用】归档保存属于人类操作，AI 禁止自动归档；调用会一律被拒绝，改为提示用户手动保存。",
			Schema:      aiObjectSchema(map[string]any{}, nil),
			ReadOnly:    false,
			Run: func(args string) (string, error) {
				return "", fmt.Errorf("归档（保存）属于人类操作，AI 禁止自动归档；请提示用户在应用内手动保存")
			},
		},
		{
			Name:        "search_knowledge",
			Description: "在内置知识库（DNF/PVF 社区教程、字段注释、脚本范例）中按关键词检索，返回相关文件与命中行摘录。回答 PVF 语法、字段含义、脚本写法、修改流程等问题时优先用此工具查资料。",
			Schema: aiObjectSchema(map[string]any{
				"query": map[string]any{"type": "string", "description": "关键词，可多个（空格分隔），如 装备 掉落、script .equ 写法"},
				"limit": map[string]any{"type": "integer", "description": "返回文件数上限，默认 10，最大 30"},
			}, []string{"query"}),
			ReadOnly: true,
			Run: func(args string) (string, error) {
				parsed, err := aiParseArgs(args)
				if err != nil {
					return "", err
				}
				query := aiStringArg(parsed, "query")
				if strings.TrimSpace(query) == "" {
					return "", fmt.Errorf("query 不能为空")
				}
				return aiKnowledge.searchKnowledge(query, aiIntArg(parsed, "limit", 10))
			},
		},
		{
			Name:        "read_knowledge",
			Description: "读取知识库中一个文件的完整内容（每行带「行号:」前缀）；path 用 search_knowledge 返回的 file 字段。",
			Schema: aiObjectSchema(map[string]any{
				"path":   map[string]any{"type": "string", "description": "知识库内相对路径，来自 search_knowledge 的 file 字段"},
				"offset": map[string]any{"type": "integer", "description": "可选；从第几行开始读取，用于翻页"},
			}, []string{"path"}),
			ReadOnly: true,
			Run: func(args string) (string, error) {
				parsed, err := aiParseArgs(args)
				if err != nil {
					return "", err
				}
				path := aiStringArg(parsed, "path")
				if strings.TrimSpace(path) == "" {
					return "", fmt.Errorf("path 不能为空")
				}
				return aiKnowledge.readKnowledge(path, aiIntArg(parsed, "offset", 0))
			},
		},
	}
	// P1~P3 的进阶工具（诊断 / 批量替换预览与应用 / 文件对比 / 批量能力说明）。
	return append(tools, buildAdvancedAITools(c)...)
}

// ---- 工具实现的小工具函数 ----

// aiFieldInfo 是 field_semantics 工具返回的一条字段语义摘要。
type aiFieldInfo struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Section    string   `json:"section"`
	Extensions []string `json:"extensions,omitempty"`
	Content    string   `json:"content,omitempty"`
}

func aiExtensionMatch(extensions []string, wanted string) bool {
	for _, extension := range extensions {
		if strings.ToLower(strings.TrimSpace(extension)) == wanted {
			return true
		}
	}
	return false
}

func aiObjectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func aiJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func aiParseArgs(args string) (map[string]any, error) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return map[string]any{}, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, fmt.Errorf("工具参数不是合法 JSON: %w", err)
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	return parsed, nil
}

func aiStringArg(parsed map[string]any, key string) string {
	value, ok := parsed[key].(string)
	if !ok {
		return ""
	}
	return value
}

func aiIntArg(parsed map[string]any, key string, fallback int) int {
	value, ok := parsed[key].(float64)
	if !ok {
		return fallback
	}
	return int(value)
}

func aiBoolArg(parsed map[string]any, key string, fallback bool) bool {
	value, ok := parsed[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func aiStringSliceArg(parsed map[string]any, key string) []string {
	raw, ok := parsed[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, value)
	}
	return result
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

// aiNumberLines 给每行加「行号: 」前缀（行号与编辑器一致，从 1 开始）。
// 模型据此才能精确引用位置，使 AI 回答与文本内容建立可跳转的关联。
func aiNumberLines(value string) string {
	lines := strings.Split(value, "\n")
	var builder strings.Builder
	for index, line := range lines {
		fmt.Fprintf(&builder, "%d: %s", index+1, line)
		if index < len(lines)-1 {
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func aiTruncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + fmt.Sprintf("\n…（内容过长，已截断，共 %d 字符）", len(runes))
}

// aiFindObjectIndex 按归档内路径定位文件索引。
func aiFindObjectIndex(c *core, path string) (int32, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, fmt.Errorf("路径不能为空")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.archive == nil {
		return 0, ErrNoArchive
	}
	index, ok := c.archive.Find(path)
	if !ok {
		return 0, fmt.Errorf("归档里没有这个文件: %s", path)
	}
	return index, nil
}
