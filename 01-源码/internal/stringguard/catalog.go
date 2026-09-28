// Package stringguard 实现客户端字符串表的写保护策略（上级规则 §6.12「字符串表铁律」）。
//
// 背景：客户端「110US 基础汉化 v1.1」改过的 34 张 `String/*.uv.str` 一旦被改写，
// 游戏界面会成片乱码；经实测交叉比对，`list/n_string.lst` 里的 37 张表中
// **只有 1 / 5 / 8 / 27 四张是安全的**（即白名单 = 禁动名单的补集）。
//
// 本包只做策略判定：表号、路径、白名单与提示文案全部来自外部数据文件
// （config/protected-string-tables.json），Go 侧不硬编码任何游戏数据。
package stringguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// AutoTargets 是「自动匹配安全表」的偏好：数据驱动，界面上的"自动匹配"选项用它。
type AutoTargets struct {
	// Preference 是按优先级排列的安全表号。实际挑选时**只取当前归档里确实可写的表**
	// （有些表在某个客户端里是空的，内核无法往里追加条目）。留空则回落到白名单升序。
	Preference []int `json:"preference,omitempty"`
}

// Catalog 是字符串表写保护清单。
type Catalog struct {
	Version     int               `json:"version"`
	Description string            `json:"description,omitempty"`
	Source      map[string]string `json:"source,omitempty"`
	WritePolicy WritePolicy       `json:"writePolicy"`
	// AutoTargets 见 AutoTargets；缺省时按白名单升序尝试。
	AutoTargets AutoTargets `json:"autoTargets,omitempty"`
	// TablePaths 是「表号 → 归档内 .str 路径」（实测 list/n_string.lst）。
	TablePaths map[string]string `json:"tablePaths"`
	// ProtectedPaths 是禁动路径（大小写无关比较）。
	ProtectedPaths []string `json:"protectedPaths"`
	// CautionPaths 是汉化包改动但不属于字符串表的文件：提示级、不硬拦截。
	CautionPaths []string `json:"cautionPaths,omitempty"`
	Notes        []string `json:"notes,omitempty"`
}

// WritePolicy 描述写保护模式与白名单。
type WritePolicy struct {
	// Mode 目前只支持 "whitelist"：白名单之外的表号一律拒绝。
	Mode                string `json:"mode"`
	AllowedTableNumbers []int  `json:"allowedTableNumbers"`
	// BlockedHint 是拦截时展示给用户的修复建议。
	BlockedHint string `json:"blockedHint,omitempty"`
}

// Parse 解析并校验清单。
func Parse(data []byte) (Catalog, error) {
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("解析字符串表写保护清单失败: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Catalog{}, fmt.Errorf("字符串表写保护清单只能包含一个 JSON 文档")
		}
		return Catalog{}, fmt.Errorf("解析字符串表写保护清单失败: %w", err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// Validate 校验清单结构与取值。
func Validate(catalog Catalog) error {
	problems := make([]string, 0)
	if catalog.Version != 1 {
		problems = append(problems, fmt.Sprintf("version 必须为 1，当前为 %d", catalog.Version))
	}
	switch catalog.WritePolicy.Mode {
	case "whitelist":
	case "":
		problems = append(problems, "writePolicy.mode 不能为空（当前只支持 whitelist）")
	default:
		problems = append(problems, "writePolicy.mode 只支持 whitelist，当前为 "+catalog.WritePolicy.Mode)
	}
	if len(catalog.WritePolicy.AllowedTableNumbers) == 0 {
		problems = append(problems, "writePolicy.allowedTableNumbers 不能为空（否则任何写入都会被拒）")
	}
	seenTables := make(map[int]bool, len(catalog.WritePolicy.AllowedTableNumbers))
	for _, index := range catalog.WritePolicy.AllowedTableNumbers {
		if index < 0 {
			problems = append(problems, fmt.Sprintf("allowedTableNumbers 不能为负数: %d", index))
		}
		if seenTables[index] {
			problems = append(problems, fmt.Sprintf("allowedTableNumbers 重复: %d", index))
		}
		seenTables[index] = true
	}
	if len(catalog.ProtectedPaths) == 0 {
		problems = append(problems, "protectedPaths 不能为空")
	}
	seenPaths := make(map[string]bool, len(catalog.ProtectedPaths))
	for _, path := range catalog.ProtectedPaths {
		key := normalizePath(path)
		// 必须在 normalizePath **之前**判断原始值：normalizePath 会把反斜杠
		// 换成正斜杠，用它判断等于这条校验永远不触发（曾因此漏掉一个用例）。
		rawHasBackslash := strings.ContainsRune(strings.TrimSpace(path), '\\')
		switch {
		case key == "":
			problems = append(problems, "protectedPaths 含空路径")
		case rawHasBackslash:
			problems = append(problems, "protectedPaths 必须使用 / 分隔: "+path)
		case seenPaths[key]:
			problems = append(problems, "protectedPaths 重复: "+path)
		default:
			seenPaths[key] = true
		}
		// 白名单表不能被同时列为禁动，否则策略自相矛盾。
		if figure := tableNumberForPath(catalog.TablePaths, key); figure >= 0 && seenTables[figure] {
			problems = append(problems, fmt.Sprintf("路径 %s 属于白名单表 %d，但又被列为禁动", path, figure))
		}
	}
	for key := range catalog.TablePaths {
		if _, err := strconv.Atoi(key); err != nil {
			problems = append(problems, "tablePaths 的键必须是表号（十进制）: "+key)
		}
	}
	for _, index := range catalog.WritePolicy.AllowedTableNumbers {
		if _, ok := catalog.TablePaths[strconv.Itoa(index)]; !ok {
			problems = append(problems, fmt.Sprintf("白名单表 %d 缺少 tablePaths 映射，无法给出准确提示", index))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("字符串表写保护清单校验失败:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

// AllowedTableNumbers 返回升序的白名单表号。
func (c Catalog) SortedAllowedTables() []int {
	out := append([]int(nil), c.WritePolicy.AllowedTableNumbers...)
	sort.Ints(out)
	return out
}

// TablePath 返回表号对应的归档路径（未知表号返回空串）。
func (c Catalog) TablePath(index int) string {
	return c.TablePaths[strconv.Itoa(index)]
}

// tableNumberForPath 反查路径所属表号；找不到返回 -1。
func tableNumberForPath(tablePaths map[string]string, normalized string) int {
	for key, path := range tablePaths {
		if normalizePath(path) != normalized {
			continue
		}
		if index, err := strconv.Atoi(key); err == nil {
			return index
		}
	}
	return -1
}

// normalizePath 统一为小写、正斜杠、无首尾斜杠，用于大小写无关比较。
func normalizePath(path string) string {
	trimmed := strings.Trim(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "/")
	return strings.ToLower(trimmed)
}
