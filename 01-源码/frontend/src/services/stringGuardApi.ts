/**
 * 字符串表写保护 + 「改写到安全表」的调用适配层（**临时**）。
 *
 * 为什么不在 `frontend/bindings/`：官方 `wails3 generate bindings` 在本机不可用
 * （模块缓存缺 `internal/commands/build_assets/windows/{msix,nsis}`，`//go:embed` 编译失败），
 * 而 `AGENTS.md` 规定 `bindings/` 禁手工修改。详见
 * 旧工作台补丁说明 `P-0004-object-view/README.md` §7.2（已随工作台迁移归档到
 * `07-归档资料\项目旧文档\`，原路径 `pvf-dev-project/...` 不再使用）。
 *
 * 方法 ID 由 wails v3.0.0-beta.12 的算法复刻：
 *   methodID = FNV-1a-32("<PkgPath>.<TypeName>.<MethodName>")
 * 取自 `pkg/application/bindings.go:245/251` + `internal/hash/fnv.go:5`；
 * 该算法已用仓库内已有 bindings 的真实 ID 回归验证 7/7 吻合
 * （复算探针：`pvf-dev-project/work/fnv-probe/`）。
 *
 * 官方生成器可用后：删除本文件，改用生成的 bindings。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `services.StringTableGuardEntry`。 */
export interface StringTableGuardEntry {
  index: number;
  path: string;
  label: string;
}

/** 对应 Go `services.StringTableGuardInfo`。 */
export interface StringTableGuardInfo {
  allowedTables: StringTableGuardEntry[];
  protectedCount: number;
  hint: string;
  /** 对应 Go `services.StringTableGuardInfo.Enabled`：写保护总开关是否开启。 */
  enabled: boolean;
}

/** 对应 Go `services.SafeTableTarget`：带**当前归档里的可写性**。 */
export interface SafeTableTarget {
  index: number;
  path: string;
  label: string;
  writable: boolean;
  writeTarget?: string;
  unwritableReason?: string;
}

/** 对应 Go `services.SafeTableTargetsResult`。 */
export interface SafeTableTargetsResult {
  allowedTables: SafeTableTarget[];
  autoPreference: number[];
  autoAvailable: boolean;
  protectedCount: number;
  hint: string;
}

/** 对应 Go `services.SafeTableRewriteResult`。 */
export interface SafeTableRewriteResult {
  scriptPath: string;
  scriptText: string;
  sourceTableIndex: number;
  targetTableIndex: number;
  targetTablePath: string;
  key: string;
  rewrittenOccurrences: number;
  autoPicked: boolean;
  skippedTableIndexes?: number[];
}

/** 「自动匹配」哨兵值（对应 Go `services.TargetTableAuto`）。 */
export const AUTO_TARGET = 0;

/**
 * 安全表白名单与提示文案（与归档无关）。
 * fqn = pvfine/services.EditorService.StringTableGuardInfo
 */
export function StringTableGuardInfo(): $CancellablePromise<StringTableGuardInfo> {
  return $Call.ByID(2743377872);
}

/**
 * 四张安全表在**当前归档**里的可写性 + 自动匹配偏好。
 * fqn = pvfine/services.EditorService.SafeTableTargets
 */
export function SafeTableTargets(): $CancellablePromise<SafeTableTargetsResult> {
  return $Call.ByID(2187346599);
}

/**
 * 把显示文本从禁动表改写到安全表（写安全表 + 改写脚本里的表号）。
 * targetTableIndex 传 AUTO_TARGET(0) 表示自动挑一张可写的安全表。
 * fqn = pvfine/services.EditorService.RewritePlaceholderToSafeTable
 */
export function RewritePlaceholderToSafeTable(
  index: number,
  sourceTableIndex: number,
  key: string,
  text: string,
  targetTableIndex: number
): $CancellablePromise<SafeTableRewriteResult> {
  return $Call.ByID(1477501103, index, sourceTableIndex, key, text, targetTableIndex);
}

let cachedGuard: StringTableGuardInfo | null = null;
let cachedTargets: SafeTableTargetsResult | null = null;

/** 清空缓存：设置里切换写保护开关后必须调用，否则界面会继续用旧状态。 */
export function invalidateStringTableGuardCache(): void {
  cachedGuard = null;
  cachedTargets = null;
}

/** 读取并缓存安全表白名单（进程内只取一次）。 */
export async function loadStringTableGuard(force = false): Promise<StringTableGuardInfo | null> {
  if (!force && cachedGuard) return cachedGuard;
  try {
    cachedGuard = await StringTableGuardInfo();
  } catch {
    cachedGuard = null;
  }
  return cachedGuard;
}

/** 读取「改写目标」清单（含可写性）；每次打开对话框可强制刷新。 */
export async function loadSafeTableTargets(
  force = false
): Promise<SafeTableTargetsResult | null> {
  if (!force && cachedTargets) return cachedTargets;
  try {
    cachedTargets = await SafeTableTargets();
  } catch {
    cachedTargets = null;
  }
  return cachedTargets;
}

/** 安全表清单；未加载成功时返回空数组。 */
export function allowedStringTables(): StringTableGuardEntry[] {
  return cachedGuard?.allowedTables ?? [];
}

/**
 * 判断某个字符串表号在**当前是否应当被界面拦截**。
 *
 * 两个条件都要满足：① 写保护总开关开启（设置 → 交互与文件）；② 该表号不在
 * 安全表白名单内。开关关闭时一律返回 false —— 默认不限制，与后端的放行行为
 * 保持一致，避免"界面拦下、后端其实能写"的割裂。
 * 白名单尚未加载时返回 false —— 界面不主动拦，由后端拦截兜底。
 */
export function isProtectedStringTable(tableIndex: number): boolean {
  if (!cachedGuard || !cachedGuard.enabled) return false;
  return !cachedGuard.allowedTables.some((entry) => entry.index === tableIndex);
}

/** 默认目标：**自动匹配**（挑当前归档里确实可写的安全表）。 */
export function defaultTargetTableIndex(): number {
  return AUTO_TARGET;
}
