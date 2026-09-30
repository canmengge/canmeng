/**
 * 大文件「页式 TXT」调用的适配层（与 `externalEditApi.ts` 同源做法）。
 *
 * `frontend/bindings/` 由 `wails3 generate bindings` 生成、本机无法重建，因此新增的
 * Go 方法统一走 `Call.ByName`，不碰 `bindings/`。
 * fqn = `pvfine/services.EditorService.<方法名>`
 */
// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 一页大文本：窗口里只会出现这一页。 */
export interface LargeTextPage {
  index: number;
  path: string;
  page: number;
  pageCount: number;
  lines: number;
  pageLines: number;
  editable: boolean;
  dirty: boolean;
  text: string;
}

/** 取一页大文本（大文件不进窗口，因此也能秒开）。 */
export function GetFilePage(
  index: number,
  page: number
): $CancellablePromise<LargeTextPage | null> {
  return $Call.ByName("pvfine/services.EditorService.GetFilePage", index, page);
}

/** 把一页文本写回归档内存（之后仍需「保存 PVF」落盘）。 */
export function SetFilePage(
  index: number,
  page: number,
  text: string
): $CancellablePromise<LargeTextPage | null> {
  return $Call.ByName("pvfine/services.EditorService.SetFilePage", index, page, text);
}

/** 行区间切片（1 基行号）：连续滚动视图每次只取视口附近的行。 */
export interface LargeTextChunk {
  index: number;
  path: string;
  start: number;
  count: number;
  lines: number;
  editable: boolean;
  dirty: boolean;
  text: string;
}

/** 取 [startLine, startLine+count) 行。 */
export function GetFileLines(
  index: number,
  startLine: number,
  count: number
): $CancellablePromise<LargeTextChunk | null> {
  return $Call.ByName("pvfine/services.EditorService.GetFileLines", index, startLine, count);
}

/** 用 text 替换 [startLine, startLine+lineCount) 行并写回归档内存。 */
export function SetFileLines(
  index: number,
  startLine: number,
  lineCount: number,
  text: string
): $CancellablePromise<LargeTextChunk | null> {
  return $Call.ByName(
    "pvfine/services.EditorService.SetFileLines",
    index,
    startLine,
    lineCount,
    text
  );
}
