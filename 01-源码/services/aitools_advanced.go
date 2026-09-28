package services

import (
	"fmt"
	"sort"
	"strings"
)

// buildAdvancedAITools 组装 P1~P3 的进阶 AI 工具。
//
// 设计约定（与 buildAITools 一致）：
//   - 全部复用既有服务（BatchService / DoctorService），不另起炉灶；
//   - 只读工具（diagnose / preview_replace / diff_file / describe_batch_ops）
//     不受写保护拦截；写工具（apply_replace）受「AI 写保护」门禁约束；
//   - 应用类工具只写内存覆盖层，绝不落盘（归档保存仍由人类手动执行）。
func buildAdvancedAITools(c *core) []AITool {
	batch := NewBatchService(c)
	doctor := NewDoctorService(c)

	return []AITool{
		diagnoseTool(doctor),
		previewReplaceTool(batch),
		applyReplaceTool(batch),
		diffFileTool(c),
		describeBatchOpsTool(),
	}
}

// diagnoseTool：归档只读体检（复用 DoctorService）。
func diagnoseTool(doctor *DoctorService) AITool {
	return AITool{
		Name: "diagnose",
		Description: "对当前打开的归档做一次只读体检：文件/目录总数、扩展名分布、字符串表指纹与 lst 登记覆盖。" +
			"属于全量扫描，文件很多时可能耗时较久；结果用于判断归档整体健康状况。",
		Schema:   aiObjectSchema(map[string]any{}, nil),
		ReadOnly: true,
		Run: func(args string) (string, error) {
			report, err := doctor.Run()
			if err != nil {
				return "", err
			}
			extensions := report.Extensions
			if len(extensions) > 20 {
				extensions = extensions[:20]
			}
			return aiTruncate(aiJSON(map[string]any{
				"path":          report.Path,
				"fileCount":     report.FileCount,
				"dirCount":      report.DirCount,
				"groupCount":    report.GroupCount,
				"paged110":      report.Paged110,
				"durationMs":    report.DurationMs,
				"extensionsTop": extensions,
				"tableSummary":  report.TableSummary,
				"listCoverage":  report.ListCoverage,
				"unregistered":  report.UnregisteredTop,
				"warnings":      report.Warnings,
			}), aiMaxOutputRunes), nil
		},
	}
}

// previewReplaceTool：跨文件文本替换预览（只读，复用 BatchService.Preview）。
func previewReplaceTool(batch *BatchService) AITool {
	return AITool{
		Name: "preview_replace",
		Description: "预览一次跨文件文本替换（不修改归档）：先给出待处理的文件路径列表（可用 search_files 定位），" +
			"再传查找/替换内容。返回将改动哪些文件、匹配处数、diff 摘要与计划 ID；确认后用 apply_replace 应用。",
		Schema: aiObjectSchema(map[string]any{
			"find":        map[string]any{"type": "string", "description": "查找内容（字面量或正则）"},
			"replacement": map[string]any{"type": "string", "description": "替换内容；正则模式下支持 $1 / ${name} 捕获组"},
			"paths":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "要处理的文件路径列表（必填）"},
			"regex":       map[string]any{"type": "boolean", "description": "find 是否按正则解释，默认 false"},
		}, []string{"find", "replacement", "paths"}),
		ReadOnly: true,
		Run: func(args string) (string, error) {
			parsed, err := aiParseArgs(args)
			if err != nil {
				return "", err
			}
			find := aiStringArg(parsed, "find")
			if strings.TrimSpace(find) == "" {
				return "", fmt.Errorf("find 不能为空")
			}
			paths := aiStringSliceArg(parsed, "paths")
			if len(paths) == 0 {
				return "", fmt.Errorf("paths 不能为空：请先用 search_files 确定要处理的文件")
			}
			page, err := batch.Preview(BatchRequest{
				Mode:  BatchModeText,
				Paths: paths,
				Text: &TextReplaceSpec{
					Find:        find,
					Replacement: aiStringArg(parsed, "replacement"),
					Regex:       aiBoolArg(parsed, "regex", false),
				},
			})
			if err != nil {
				return "", err
			}
			rows := make([]map[string]any, 0, len(page.Rows))
			for _, row := range page.Rows {
				if row == nil {
					continue
				}
				entry := map[string]any{
					"fileIndex":  row.FileIndex,
					"path":       row.Path,
					"status":     row.Status,
					"matchCount": row.MatchCount,
				}
				if row.Reason != "" {
					entry["reason"] = row.Reason
				}
				if len(row.Diff) > 0 {
					lines := make([]string, 0, 12)
					for index, line := range row.Diff {
						if index >= 12 {
							lines = append(lines, "…（更多差异省略）")
							break
						}
						lines = append(lines, line.Kind+" "+line.Text)
					}
					entry["diffSample"] = lines
				}
				rows = append(rows, entry)
			}
			return aiTruncate(aiJSON(map[string]any{
				"planId":             page.PlanID,
				"requestedFiles":     page.RequestedFiles,
				"matchedFiles":       page.MatchedFiles,
				"matchedOccurrences": page.MatchedOccurrences,
				"changedFiles":       page.ChangedFiles,
				"rows":               rows,
				"note":               "planId 用于 apply_replace；本页仅展示前若干条",
			}), aiMaxOutputRunes), nil
		},
	}
}

// applyReplaceTool：应用替换计划（写工具，受写保护门禁；只写内存覆盖层）。
func applyReplaceTool(batch *BatchService) AITool {
	return AITool{
		Name: "apply_replace",
		Description: "应用 preview_replace 生成的替换计划（只写内存覆盖层，不落盘；归档保存仍由人类手动执行）。" +
			"未指定 fileIndexes 时默认应用计划中全部可变动的文件。",
		Schema: aiObjectSchema(map[string]any{
			"planId":      map[string]any{"type": "string", "description": "preview_replace 返回的计划 ID"},
			"fileIndexes": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "仅应用这些文件索引；留空表示全部可变动文件"},
		}, []string{"planId"}),
		ReadOnly: false,
		Run: func(args string) (string, error) {
			parsed, err := aiParseArgs(args)
			if err != nil {
				return "", err
			}
			planID := strings.TrimSpace(aiStringArg(parsed, "planId"))
			if planID == "" {
				return "", fmt.Errorf("planId 不能为空")
			}
			indexes := aiInt32SliceArg(parsed, "fileIndexes")
			if len(indexes) == 0 {
				collected, collectErr := collectChangedIndexes(batch, planID)
				if collectErr != nil {
					return "", collectErr
				}
				indexes = collected
			}
			if len(indexes) == 0 {
				return "", fmt.Errorf("该计划没有可应用的文件")
			}
			result, err := batch.Apply(planID, indexes)
			if err != nil {
				return "", err
			}
			return aiJSON(map[string]any{
				"appliedFiles":  result.AppliedFiles,
				"modifiedCount": result.ModifiedCount,
				"note":          "已写入内存覆盖层，请在应用内手动保存归档",
			}), nil
		},
	}
}

// collectChangedIndexes 汇总某个预览计划中所有「可变更」文件的索引（跨分页）。
func collectChangedIndexes(batch *BatchService, planID string) ([]int32, error) {
	cursor := 0
	seen := make(map[int32]struct{})
	result := make([]int32, 0)
	// 最多翻 1000 页（每页 500 条 ⇒ 50 万文件），防御异常游标导致的死循环。
	for pageIndex := 0; pageIndex < 1000; pageIndex++ {
		page, err := batch.PreviewPage(planID, cursor, 500)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Rows {
			if row == nil || row.Status != BatchFileChanged || row.FileIndex < 0 {
				continue
			}
			if _, exists := seen[row.FileIndex]; exists {
				continue
			}
			seen[row.FileIndex] = struct{}{}
			result = append(result, row.FileIndex)
		}
		next := page.NextCursor
		if next < 0 || next <= cursor {
			break
		}
		cursor = next
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

// diffFileTool：对比「内存编辑内容」与「归档原始内容」（只读）。
func diffFileTool(c *core) AITool {
	return AITool{
		Name: "diff_file",
		Description: "查看某个文件「当前内存编辑内容」与「归档原始内容」的差异（只读）。" +
			"用于确认改了什么；没有未保存改动时返回 changed=false。",
		Schema: aiObjectSchema(map[string]any{
			"path": map[string]any{"type": "string", "description": "归档内文件路径"},
		}, []string{"path"}),
		ReadOnly: true,
		Run: func(args string) (string, error) {
			parsed, err := aiParseArgs(args)
			if err != nil {
				return "", err
			}
			path := strings.TrimSpace(aiStringArg(parsed, "path"))
			if path == "" {
				return "", fmt.Errorf("path 不能为空")
			}
			c.mu.RLock()
			defer c.mu.RUnlock()
			if c.archive == nil {
				return "", ErrNoArchive
			}
			index, ok := c.archive.Find(path)
			if !ok {
				return "", fmt.Errorf("归档里没有这个文件: %s", path)
			}
			current, hasOverlay := c.editorText[index]
			if !hasOverlay {
				return aiJSON(map[string]any{"changed": false, "note": "该文件没有未保存的内存改动"}), nil
			}
			original, err := c.archive.Text(index)
			if err != nil {
				return "", err
			}
			if original == current {
				return aiJSON(map[string]any{"changed": false, "note": "内存内容与原始内容一致"}), nil
			}
			diff, added, removed := aiLineDiff(original, current)
			return aiTruncate(aiJSON(map[string]any{
				"changed":      true,
				"addedLines":   added,
				"removedLines": removed,
				"diff":         diff,
			}), aiMaxOutputRunes), nil
		},
	}
}

// describeBatchOpsTool：说明支持的批量修改能力与参数格式（只读），辅助「脚本生成」。
func describeBatchOpsTool() AITool {
	return AITool{
		Name:        "describe_batch_ops",
		Description: "返回编辑器支持的批量修改能力与参数格式（只读）。当用户想「批量改」时，先看这里再构造计划。",
		Schema:      aiObjectSchema(map[string]any{}, nil),
		ReadOnly:    true,
		Run: func(args string) (string, error) {
			return aiJSON(map[string]any{
				"textReplace": map[string]any{
					"tool":   "preview_replace",
					"fields": []string{"find", "replacement", "regex", "paths"},
					"notes": []string{
						"paths 必填：先用 search_files 定位要处理的文件",
						"regex=true 时 find 为正则，replacement 支持 $1 / ${name} 捕获组",
						"预览只读、不改归档；确认后用 apply_replace 应用",
					},
				},
				"apply": map[string]any{
					"tool":   "apply_replace",
					"fields": []string{"planId", "fileIndexes"},
					"notes": []string{
						"只写内存覆盖层，不落盘；归档保存由用户手动执行",
						"fileIndexes 留空表示应用计划中全部可变动文件",
					},
				},
			}), nil
		},
	}
}

// aiDiffMaxCells 限制 LCS 动态规划的规模（行数乘积），避免大文件内存爆炸。
const aiDiffMaxCells = 4000000

// aiLineDiff 用 LCS 生成行级差异（" " 相同 / "-" 仅原始 / "+" 仅当前）。
// 规模过大时退化为行数统计。
func aiLineDiff(original, current string) ([]string, int, int) {
	oldLines := strings.Split(original, "\n")
	newLines := strings.Split(current, "\n")
	n, m := len(oldLines), len(newLines)
	if n == 0 || m == 0 || n*m > aiDiffMaxCells {
		return []string{fmt.Sprintf("（文件较大，仅统计：原始 %d 行 → 当前 %d 行）", n, m)}, max(0, m-n), max(0, n-m)
	}
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	result := make([]string, 0, n+m)
	added, removed := 0, 0
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			result = append(result, " "+oldLines[i])
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			result = append(result, "-"+oldLines[i])
			removed++
			i++
		default:
			result = append(result, "+"+newLines[j])
			added++
			j++
		}
	}
	for ; i < n; i++ {
		result = append(result, "-"+oldLines[i])
		removed++
	}
	for ; j < m; j++ {
		result = append(result, "+"+newLines[j])
		added++
	}
	return result, added, removed
}

// aiInt32SliceArg 解析 JSON 数组参数为 []int32。
func aiInt32SliceArg(parsed map[string]any, key string) []int32 {
	raw, ok := parsed[key].([]any)
	if !ok {
		return nil
	}
	result := make([]int32, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(float64)
		if !ok {
			continue
		}
		result = append(result, int32(value))
	}
	return result
}
