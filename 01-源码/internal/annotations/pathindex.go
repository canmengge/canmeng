package annotations

import (
	"sort"
	"strings"
)

// pathIndex 为 `target.kind = "path"` 的规则建索引：
//   - 精确路径（无通配符）→ 哈希查表；
//   - 目录前缀（`<dir>/**`）→ 哈希查表，查询时用"自身 + 逐级祖先"命中；
//   - 其它复杂 glob → 回退线性扫描。
//
// 引入原因：注释外置化后路径规则上万条（文件夹 + 文件注释），原先每次
// AnnotatePath 线性遍历全部规则，实测约 1.4 ms/节点，展开一个目录会明显卡顿。
// 索引让查询变成 O(路径层数)。
type pathIndex struct {
	exact  map[string][]int
	prefix map[string][]int
	other  []int
}

func buildPathIndex(rules []compiledRule) *pathIndex {
	index := &pathIndex{exact: make(map[string][]int), prefix: make(map[string][]int)}
	for position := range rules {
		compiled := &rules[position]
		if compiled.rule.Target.Kind != "path" {
			continue
		}
		glob := strings.TrimSpace(compiled.rule.Match.Glob)
		switch {
		case glob == "":
			// 空 glob 匹配一切，只能线性处理。
			index.other = append(index.other, position)
		case strings.HasSuffix(glob, "/**"):
			base := strings.TrimSuffix(glob, "/**")
			if hasGlobMeta(base) {
				index.other = append(index.other, position)
				continue
			}
			key := normalizePath(base)
			index.prefix[key] = append(index.prefix[key], position)
		case !hasGlobMeta(glob):
			key := normalizePath(glob)
			index.exact[key] = append(index.exact[key], position)
		default:
			index.other = append(index.other, position)
		}
	}
	return index
}

func hasGlobMeta(value string) bool {
	return strings.ContainsAny(value, "*?[")
}

// lookup 返回可能命中该路径的规则下标，按 rules 顺序排序去重（保持既有 RuleIDs 顺序）。
func (p *pathIndex) lookup(normalized string) []int {
	if p == nil {
		return nil
	}
	candidates := make([]int, 0, 8)
	candidates = append(candidates, p.exact[normalized]...)
	segments := strings.Split(normalized, "/")
	// `a/**` 同时覆盖 a 自身与 a/b/c，所以从自身一路查到根。
	for length := len(segments); length >= 1; length-- {
		key := strings.Join(segments[:length], "/")
		candidates = append(candidates, p.prefix[key]...)
	}
	candidates = append(candidates, p.other...)
	if len(candidates) == 0 {
		return nil
	}
	sort.Ints(candidates)
	unique := candidates[:1]
	for _, value := range candidates[1:] {
		if value != unique[len(unique)-1] {
			unique = append(unique, value)
		}
	}
	return unique
}
