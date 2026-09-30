/**
 * 大文件「连续全文 TXT」调用的适配层（与 `exportApi.ts` 同源做法）。
 *
 * `frontend/bindings/` 由 `wails3 generate bindings` 生成、本机无法重建，因此新增的
 * Go 方法统一走 `Call.ByName`，不碰 `bindings/`。
 * fqn = `pvfine/services.EditorService.<方法名>`
 */
// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

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

/**
 * 大文件当前视口那一段的注解（中文名 / 绿色关联框回归用）。
 * start / end 是**相对段首**的字符偏移 —— 窗口里的文本就是这一段的全部内容。
 */
export interface WindowAnnotation {
  start: number;
  end: number;
  type: string;
  title: string;
  content: string;
  targetFileIndex: number;
}

/**
 * 只解析 [startLine, startLine+lineCount) 这段的注解。
 *
 * 大文件整份文本不进窗口（秒开前提），注解必须按需取：成本只与视口大小有关，
 * 与文件多大无关。滚动换窗后调一次即可，段内打字不重算。
 */
export function GetWindowAnnotations(
  index: number,
  startLine: number,
  lineCount: number
): $CancellablePromise<WindowAnnotation[] | null> {
  return $Call.ByName(
    "pvfine/services.EditorService.GetWindowAnnotations",
    index,
    startLine,
    lineCount
  );
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
