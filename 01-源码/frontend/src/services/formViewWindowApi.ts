/**
 * 结构化视图「独立窗口」服务的调用适配层（**临时**）。
 *
 * 与 `objectViewApi.ts` / `formViewApi.ts` 同因：`frontend/bindings/` 由
 * `wails3 generate bindings` 生成、禁手工修改，而本机无法构建 wails3 CLI。
 *
 * 方法 ID = FNV-1a-32("pvfine/services.<Type>.<Method>")，本次用临时探针
 * **反验 3 个已知 ID 全部吻合**后才算出下面这些。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `services.FormViewSession`。 */
export interface FormViewSession {
  formatId: string;
  filePath: string;
}

/**
 * 打开（或聚焦）结构化视图独立窗口。
 * fqn = pvfine/services.FormViewWindowService.OpenFormViewWindow
 */
export function OpenFormViewWindow(session: FormViewSession): $CancellablePromise<void> {
  return $Call.ByID(2254078221, session);
}

/**
 * 取出主窗口暂存的初始参数（新窗口挂载时调用）。
 * fqn = pvfine/services.FormViewWindowService.LoadFormViewSession
 */
export function LoadFormViewSession(): $CancellablePromise<FormViewSession | null> {
  return $Call.ByID(4281858199);
}

/**
 * 独立窗口当前是否开着。
 * fqn = pvfine/services.FormViewWindowService.IsFormViewWindowOpen
 */
export function IsFormViewWindowOpen(): $CancellablePromise<boolean> {
  return $Call.ByID(1610840221);
}

/**
 * 把独立窗口提到前台；返回是否真的存在。
 * fqn = pvfine/services.FormViewWindowService.FocusFormViewWindow
 */
export function FocusFormViewWindow(): $CancellablePromise<boolean> {
  return $Call.ByID(1166323745);
}

/**
 * 关闭独立窗口（本窗口只读，无需二次确认）。
 * fqn = pvfine/services.FormViewWindowService.CloseFormViewWindow
 */
export function CloseFormViewWindow(): $CancellablePromise<void> {
  return $Call.ByID(1064768105);
}
