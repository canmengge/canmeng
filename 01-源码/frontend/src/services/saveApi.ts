/**
 * 近内核新增调用的适配层：**封包（保存 PVF）** 与 **结构化视图规则查看**
 * （与 `importApi.ts` 同源做法）。
 *
 * ## 为什么不在 frontend/bindings/ 里
 *
 * `frontend/bindings/` 由 `wails3 generate bindings` 生成、禁止手工修改；本机
 * **无法构建 wails3 CLI**（模块缓存里 `build_assets/windows/msix` 与 `nsis` 是空目录，
 * `//go:embed` 直接编译失败）。新增的 Go 方法只能这样接进来，不碰 `bindings/`。
 *
 * ## 方法 ID 不是猜的
 *
 * 算法取自 wails v3.0.0-beta.12：
 *   - `pkg/application/bindings.go:245`  fqn := "<PkgPath>.<TypeName>.<MethodName>"
 *   - `pkg/application/bindings.go:251`  methodID := hash.Fnv(fqn)   （FNV-1a-32）
 *
 * 复算后与**已有 bindings 回归校验**（EditorService.Save = 1087792513、
 * SaveAsDialog = 3571654109，两项均与生成结果一致）。
 * 2026-10-03 新增 `FormViewService.RuleText` 时同样先复算这两项（`Math.imul` 版实现，
 * 两个已知值都吻合）才落盘 ID = **3900388819**。
 *
 * ## 何时删除本文件
 *
 * 官方生成器可用后改用 `bindings/pvfine/services` 的生成结果并删除本文件。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/**
 * 对应 Go `services.SaveProgress`。
 * phase: prepare / backup / rebuild / write / sync / rename / done。
 */
export interface SaveProgress {
  phase: string;
  done: number;
  total: number;
  running: boolean;
}

/**
 * CancelSave 请求取消正在进行的封包。
 *
 * 取消点只在「临时文件 rename 之前」，因此源文件一定保持原样
 * （与导入取消同一套语义，见 services/save_job.go）。
 */
export function CancelSave(): $CancellablePromise<void> {
  return $Call.ByID(1573453603);
}

/** SaveStatus 返回当前（或最近一次）封包的进度。 */
export function SaveStatus(): $CancellablePromise<SaveProgress> {
  return $Call.ByID(2499540195);
}

/** 对应 Go `services.FormViewRuleSource`：规则文件全文及其来源。 */
export interface FormViewRuleSource {
  /** 规则文件全文（JSON 文本，界面只读展示）。 */
  text: string;
  /** 来源：「(内置)」或仓库里的文件路径 —— 让用户知道这是正在生效的那一份。 */
  source: string;
}

/**
 * 对应 Go `services.FormViewService.RuleText`：返回**当前真正生效**的规则全文（只读）。
 *
 * 用户 2026-10-03 要求规则随程序内置，并在可视化编辑区里能"看规则"（可看可搜、不能改）。
 */
export function FormViewRuleText(): $CancellablePromise<FormViewRuleSource> {
  return $Call.ByID(3900388819);
}
