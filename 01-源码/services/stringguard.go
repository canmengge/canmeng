package services

import (
	"strings"
	"sync"
	"sync/atomic"

	"pvfine/internal/logging"
	"pvfine/internal/pvf"
	"pvfine/internal/stringguard"
)

// 字符串表写保护（上级工作台规则 §6.12）。
//
// 所有会改写字符串表载荷的调用都必须先过这里：
//   - core.setText            按归档路径判定（用户直接在编辑器里打开 .str 编辑）
//   - core.setPlaceholderText 按表号判定（就地改译文）
//   - archive.CreateFile / DeleteFiles、import、batch 按路径判定
//
// 清单来自 external 数据文件（config/protected-string-tables.json）或内置副本。
//
// 2026-09-24 起本保护受**总开关**控制（设置 → 交互与文件 → 字符串表写保护），
// 且默认**关闭**：开关关闭时上面所有拦截一律放行。

var (
	stringGuardMu     sync.RWMutex
	stringGuardValue  *stringguard.Guard
	stringGuardErr    error
	stringGuardLoaded bool
)

// stringTableGuardEnabled 是「字符串表写保护」总开关。
//
// 由 SettingsService 在**构造**（启动时读盘同步）与**保存设置**（立即生效）时写入；
// 默认 false = 不拦截，与 DefaultAppSettings() 的默认值一致 —— 这样才不会出现
// "设置还没被读到，保护却按旧值拦了" 的窗口期。
var stringTableGuardEnabled atomic.Bool

// setStringTableGuardEnabled 更新写保护总开关（由 SettingsService 调用）。
func setStringTableGuardEnabled(enabled bool) { stringTableGuardEnabled.Store(enabled) }

// StringTableGuardEnabled 报告写保护总开关当前是否开启。
// 前端「编辑器占位符编辑」据此决定是否做界面拦截，与后端共用同一事实源。
func StringTableGuardEnabled() bool { return stringTableGuardEnabled.Load() }

// protectedStringTablePaths 返回禁动名单里的归档路径（归一化形式）。
func protectedStringTablePaths() ([]string, error) {
	guard, err := stringGuard()
	if err != nil {
		return nil, err
	}
	return guard.ProtectedPaths(), nil
}

// assertProtectedTablesUnmodifiedLocked 是保存前的**双保险指纹校验**。
//
// 正常的写入路径已经由写保护逐个拦截；这里在落盘前再把禁动表过一遍，
// 抓「从别的路绕过去」的改动（批量处理 / 导入 / 脚本等），发现即拒绝保存 ——
// 宁可报错让用户确认，也不产出客户端读不了的包（对照 4.4 客户乱码事故）。
//
// 总开关关闭时一律放行（那是用户显式选择，界面里能看清）。
// 调用方必须已持有 c.mu。
func (c *core) assertProtectedTablesUnmodifiedLocked(a *pvf.Archive) error {
	if a == nil || !StringTableGuardEnabled() {
		return nil
	}
	paths, err := protectedStringTablePaths()
	if err != nil || len(paths) == 0 {
		// 清单读不到就不阻止保存：保护缺失应是"少一层保险"，不该让用户存不了文件。
		return nil
	}
	var changed []string
	for _, path := range paths {
		index, ok := a.Find(path)
		if !ok {
			continue
		}
		if a.IsModified(index) {
			changed = append(changed, a.Path(index))
		}
	}
	if len(changed) == 0 {
		return nil
	}
	sample := changed
	if len(sample) > 5 {
		sample = sample[:5]
	}
	return &ProtectedStringTableError{
		Path:   sample[0],
		Reason: "本次保存包含对客户端汉化禁动表的修改：" + strings.Join(sample, "、"),
		Hint:   "这些表被客户端直接读取，改坏会导致界面与文本成片报废；请撤销这些修改（或在「设置 → 交互与文件」里关闭字符串表写保护，后果自负）后重新保存",
	}
}

// isSafeStringTable 查询表号是否在安全表白名单内（**事实查询，与总开关无关**）。
//
// 「改写到安全表」这个功能本身就以"源表在白名单外、目标表在白名单内"为语义前提，
// 所以它不能复用 guardStringTableWriteByIndex —— 后者在总开关关闭时一律放行。
func isSafeStringTable(tableIndex int) (bool, error) {
	guard, err := stringGuard()
	if err != nil {
		return false, err
	}
	return guard.CheckTableIndex(tableIndex).Verdict == stringguard.VerdictAllow, nil
}

// ProtectedStringTableError 表示一次写入被字符串表写保护拦截。
type ProtectedStringTableError struct {
	TableIndex int
	Path       string
	Reason     string
	Hint       string
}

func (e *ProtectedStringTableError) Error() string {
	var builder strings.Builder
	builder.WriteString("已拦截：")
	builder.WriteString(e.Reason)
	if e.Hint != "" {
		builder.WriteString("；建议：")
		builder.WriteString(e.Hint)
	}
	return builder.String()
}

// stringGuard 返回进程内共享的写保护守卫（惰性加载一次）。
func stringGuard() (*stringguard.Guard, error) {
	stringGuardMu.RLock()
	if stringGuardLoaded {
		guard, err := stringGuardValue, stringGuardErr
		stringGuardMu.RUnlock()
		return guard, err
	}
	stringGuardMu.RUnlock()

	stringGuardMu.Lock()
	defer stringGuardMu.Unlock()
	if stringGuardLoaded {
		return stringGuardValue, stringGuardErr
	}
	guard, err := stringguard.LoadPreferred()
	stringGuardValue, stringGuardErr, stringGuardLoaded = guard, err, true
	if err != nil {
		logging.For("stringguard").Error("字符串表写保护清单加载失败", "错误", err.Error())
	}
	return guard, err
}

// ReloadStringGuard 重新读取写保护清单并原子替换当前守卫。
func ReloadStringGuard() (*stringguard.Guard, error) {
	guard, err := stringguard.LoadPreferred()
	if err != nil {
		return nil, err
	}
	stringGuardMu.Lock()
	stringGuardValue, stringGuardErr, stringGuardLoaded = guard, nil, true
	stringGuardMu.Unlock()
	return guard, nil
}

func newProtectedStringTableError(decision stringguard.Decision) *ProtectedStringTableError {
	return &ProtectedStringTableError{
		TableIndex: decision.TableIndex,
		Path:       decision.TablePath,
		Reason:     decision.Reason,
		Hint:       decision.Hint,
	}
}

// guardStringTableWriteByIndex 按字符串表号判定；命中保护策略则返回错误。
// 总开关关闭时（默认）一律放行。
//
// 清单不可用时**拒绝写入**（fail-closed）：这条路径只用于改写字符串表载荷，
// 而字符串表正是最贵的一类事故来源，宁可挡住也不冒"整片界面乱码"的险。
func guardStringTableWriteByIndex(tableIndex int) error {
	if !StringTableGuardEnabled() {
		return nil
	}
	guard, err := stringGuard()
	if err != nil {
		return &ProtectedStringTableError{
			TableIndex: tableIndex,
			Reason:     "字符串表写保护清单不可用，已按保护策略拒绝写入：" + err.Error(),
			Hint:       "检查 config/protected-string-tables.json 是否存在且合法。",
		}
	}
	if decision := guard.CheckTableIndex(tableIndex); decision.Blocked() {
		return newProtectedStringTableError(decision)
	}
	return nil
}

// guardArchiveWriteByPath 按归档内路径判定；命中禁动路径则返回错误。
// 总开关关闭时（默认）一律放行。
//
// 清单不可用时不直接放行：改为"只放行非 .str 文件"，.str 仍一律拒绝，
// 避免因清单损坏而让最危险的写入静默通过。
func guardArchiveWriteByPath(path string) error {
	if !StringTableGuardEnabled() {
		return nil
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	guard, err := stringGuard()
	if err != nil {
		if strings.HasSuffix(strings.ToLower(strings.ReplaceAll(trimmed, "\\", "/")), ".str") {
			return &ProtectedStringTableError{
				Path:   path,
				Reason: "字符串表写保护清单不可用，已按保护策略拒绝写入字符串表：" + err.Error(),
				Hint:   "检查 config/protected-string-tables.json 是否存在且合法。",
			}
		}
		return nil
	}
	if decision := guard.CheckPath(path); decision.Blocked() {
		return newProtectedStringTableError(decision)
	}
	return nil
}
