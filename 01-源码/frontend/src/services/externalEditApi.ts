/**
 * 「大文件外部编辑」调用的适配层（与 `exportApi.ts` / `importApi.ts` 同源做法）。
 *
 * `frontend/bindings/` 由 `wails3 generate bindings` 生成、本机无法重建，因此新增的
 * Go 方法统一走 `Call.ByName`，不碰 `bindings/`。
 * fqn = `pvfine/services.EditorService.<方法名>`
 */
// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 一次「外部编辑」会话。 */
export interface ExternalEditSession {
  path: string;
  localPath: string;
  dir: string;
  startedAt: string;
  baseline: string;
  hash: string;
  changed: boolean;
  opened: boolean;
  size: number;
  modTime: string;
  note?: string;
}

/** 回填结果。 */
export interface ExternalEditResult {
  path: string;
  localPath: string;
  applied: boolean;
  reason: string;
  bytes: number;
  modified: boolean;
  savedHint?: string;
}

/** 导出到工作目录 + 调起系统默认程序，返回会话。 */
export function ExternalEditStart(path: string): $CancellablePromise<ExternalEditSession> {
  return $Call.ByName("pvfine/services.EditorService.ExternalEditStart", path);
}

/** 列出全部会话。 */
export function ExternalEditList(): $CancellablePromise<ExternalEditSession[]> {
  return $Call.ByName("pvfine/services.EditorService.ExternalEditList");
}

/** 重新比对本地文件与基线（是否已改）。 */
export function ExternalEditCheck(path: string): $CancellablePromise<ExternalEditSession> {
  return $Call.ByName("pvfine/services.EditorService.ExternalEditCheck", path);
}

/** 把外部文件回填进归档（内存），需再保存 PVF 才落盘。 */
export function ExternalEditApply(path: string): $CancellablePromise<ExternalEditResult> {
  return $Call.ByName("pvfine/services.EditorService.ExternalEditApply", path);
}

/** 在资源管理器里选中工作副本。 */
export function ExternalEditReveal(path: string): $CancellablePromise<void> {
  return $Call.ByName("pvfine/services.EditorService.ExternalEditReveal", path);
}

/** 结束会话；discardLocal 为真时连工作副本目录一起删除。 */
export function ExternalEditFinish(
  path: string,
  discardLocal: boolean
): $CancellablePromise<void> {
  return $Call.ByName("pvfine/services.EditorService.ExternalEditFinish", path, discardLocal);
}
