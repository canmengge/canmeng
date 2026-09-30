package stringguard

import (
	"sort"
	"strconv"
	"strings"
)

// Verdict 是一次写判定的结论。
type Verdict int

const (
	// VerdictAllow 放行。
	VerdictAllow Verdict = iota
	// VerdictCaution 不是字符串表禁动项，但汉化包改动过：放行并提示。
	VerdictCaution
	// VerdictDeny 命中禁动项：拒绝写入。
	VerdictDeny
)

func (v Verdict) String() string {
	switch v {
	case VerdictAllow:
		return "allow"
	case VerdictCaution:
		return "caution"
	case VerdictDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// Decision 是一次写判定的结果。
type Decision struct {
	Verdict Verdict
	// TableIndex 是命中判定时涉及的字符串表号；未知为 -1。
	TableIndex int
	// TablePath 是该表在归档内的路径（来自清单的实测映射）；未知为空串。
	TablePath string
	// Protected 表示该表/路径属于禁动名单。
	Protected bool
	// Reason 是人类可读的原因。
	Reason string
	// Hint 是修复建议（来自清单的 blockedHint，必要时补充具体表号）。
	Hint string
}

// Blocked 报告该判定是否拒绝写入。
func (d Decision) Blocked() bool { return d.Verdict == VerdictDeny }

// Guard 是加载后的写保护守卫，只读、并发安全。
type Guard struct {
	allowedTables    map[int]bool
	tablePaths       map[int]string
	protected        map[string]bool
	protectedByTable map[int]bool
	caution          map[string]bool
	hint             string
	allowedOrder     []int
	// autoOrder 是「自动匹配」的候选顺序（已过滤为白名单内、去重、保序）。
	autoOrder []int
}

// AutoTargetPreference 返回「自动匹配」的候选安全表顺序。
func (g *Guard) AutoTargetPreference() []int {
	return append([]int(nil), g.autoOrder...)
}

// New 由一个已校验的清单构建守卫。
func New(catalog Catalog) *Guard {
	guard := &Guard{
		allowedTables: make(map[int]bool, len(catalog.WritePolicy.AllowedTableNumbers)),
		tablePaths:    make(map[int]string, len(catalog.TablePaths)),
		protected:     make(map[string]bool, len(catalog.ProtectedPaths)),
		protectedByTable: make(map[int]bool),
		caution:       make(map[string]bool, len(catalog.CautionPaths)),
		hint:          strings.TrimSpace(catalog.WritePolicy.BlockedHint),
		allowedOrder:  catalog.SortedAllowedTables(),
	}
	for _, index := range catalog.WritePolicy.AllowedTableNumbers {
		guard.allowedTables[index] = true
	}
	// 自动匹配顺序：过滤出白名单内的表号、去重并保序；配置为空则回落到白名单升序。
	seenAuto := make(map[int]bool, len(catalog.AutoTargets.Preference))
	for _, index := range catalog.AutoTargets.Preference {
		if !guard.allowedTables[index] || seenAuto[index] {
			continue
		}
		seenAuto[index] = true
		guard.autoOrder = append(guard.autoOrder, index)
	}
	if len(guard.autoOrder) == 0 {
		guard.autoOrder = append([]int(nil), guard.allowedOrder...)
	}
	for key, path := range catalog.TablePaths {
		index, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		guard.tablePaths[index] = path
	}
	for _, path := range catalog.ProtectedPaths {
		key := normalizePath(path)
		guard.protected[key] = true
		// 反查该路径所属表号，便于"按表号"拦截时判断是否属于禁动名单。
		for index, tablePath := range guard.tablePaths {
			if normalizePath(tablePath) == key {
				guard.protectedByTable[index] = true
			}
		}
	}
	for _, path := range catalog.CautionPaths {
		guard.caution[normalizePath(path)] = true
	}
	return guard
}

// AllowedTableNumbers 返回升序白名单表号（供界面展示）。
func (g *Guard) AllowedTableNumbers() []int {
	return append([]int(nil), g.allowedOrder...)
}

// ProtectedPathCount 返回禁动路径条数（供界面展示）。
func (g *Guard) ProtectedPathCount() int { return len(g.protected) }

// ProtectedPaths 返回禁动名单里的归档路径（已归一化：`/` 分隔、小写、去首尾斜杠），
// 按字典序排列。供「保存前指纹校验」这类批量化检查使用。
func (g *Guard) ProtectedPaths() []string {
	out := make([]string, 0, len(g.protected))
	for key := range g.protected {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Hint 返回清单里的修复建议。
func (g *Guard) Hint() string { return g.hint }

// CheckTableIndex 按字符串表号判定是否允许写入。
//
// 白名单之外的表号一律拒绝：n_string.lst 的 37 张表里只有 4 张安全，
// 其余（含未登记的未知表号）都视为禁动，避免"漏在名单外"绕过保护。
func (g *Guard) CheckTableIndex(index int) Decision {
	path := g.tablePaths[index]
	if g.allowedTables[index] {
		return Decision{
			Verdict:    VerdictAllow,
			TableIndex: index,
			TablePath:  path,
			Reason:     "该表在安全表白名单内",
		}
	}
	decision := Decision{
		TableIndex: index,
		TablePath:  path,
		Protected:  g.protectedByTable[index],
		Verdict:    VerdictDeny,
		Hint:       g.hint,
	}
	switch {
	case path == "":
		decision.Reason = "表 " + strconv.Itoa(index) + " 不在 list/n_string.lst 的登记范围内（无法确认安全），已按保护策略拒绝"
	case decision.Protected:
		decision.Reason = "表 " + strconv.Itoa(index) + "（" + path + "）属于客户端汉化禁动名单"
	default:
		decision.Reason = "表 " + strconv.Itoa(index) + "（" + path + "）不在安全表白名单（1/5/8/27）内"
	}
	return decision
}

// CheckPath 按归档内文件路径判定是否允许写入。
func (g *Guard) CheckPath(path string) Decision {
	key := normalizePath(path)
	switch {
	case key == "":
		return Decision{Verdict: VerdictAllow, TableIndex: -1}
	case g.protected[key]:
		index := -1
		for tableIndex, tablePath := range g.tablePaths {
			if normalizePath(tablePath) == key {
				index = tableIndex
				break
			}
		}
		return Decision{
			Verdict:    VerdictDeny,
			TableIndex: index,
			TablePath:  path,
			Protected:  true,
			Reason:     path + " 属于客户端汉化禁动名单",
			Hint:       g.hint,
		}
	case g.caution[key]:
		return Decision{
			Verdict:   VerdictCaution,
			TableIndex: -1,
			TablePath: path,
			Reason:    path + " 被汉化包改动过，但属于非字符串表文件",
			Hint:      "不是表铁律拦截项；改动前请确认不会破坏客户端资源。",
		}
	default:
		for index, tablePath := range g.tablePaths {
			if normalizePath(tablePath) == key && !g.allowedTables[index] {
				return Decision{
					Verdict:    VerdictDeny,
					TableIndex: index,
					TablePath:  path,
					Protected:  true,
					Reason:     path + " 是表 " + strconv.Itoa(index) + " 的实际载荷，表号不在安全表白名单内",
					Hint:       g.hint,
				}
			}
		}
		return Decision{Verdict: VerdictAllow, TableIndex: -1, TablePath: path}
	}
}
