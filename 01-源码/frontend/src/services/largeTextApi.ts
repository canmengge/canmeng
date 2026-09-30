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

/** 清单条目（与前端 listDuplicate.ts 的 ListEntry 同构）。 */
export interface ListDuplicateEntry {
  line: number;
  id: string;
  path: string;
  raw: string;
}

/** 一处重复问题；kind: id / path / both。 */
export interface ListDuplicateIssue {
  kind: "id" | "path" | "both";
  message: string;
  entries: ListDuplicateEntry[];
}

export interface ListDuplicateReport {
  issues: ListDuplicateIssue[];
  /** 真实问题条数（可能大于 issues.length，超出部分被截断）。 */
  total: number;
  truncated: boolean;
  /** 解析出的条目数。 */
  entries: number;
}

/**
 * 大文件 list 查重：整份文本留在后端扫，只回传问题条目。
 *
 * 前端拿不到大文件的整份文本（秒开前提），所以这一路必须走后端；
 * 规则与提示语与前端 `listDuplicate.ts` 完全一致。
 */
export function CheckListDuplicates(
  index: number
): $CancellablePromise<ListDuplicateReport | null> {
  return $Call.ByName("pvfine/services.EditorService.CheckListDuplicates", index);
}

/** TXT 视图里「改了但还没写回归档」的一段（1 基起始行 + 行数 + 该段内容）。 */
export interface OverlaySegment {
  start: number;
  count: number;
  text: string;
}

/**
 * 带「未写回段」的查重：后端在**内存里**按段替换后扫描，归档一个字节都不动。
 *
 * 为什么要这样：只有用户显式保存才允许写回归档（用户红线），所以查重不能顺手把
 * 改动写回去；但直接把归档文本扫一遍又会把用户未保存的修改当成不存在。
 * 把段传上去、在 Go 侧内存里替换，既看得到最新内容，又不改归档。
 */
export function CheckListDuplicatesWithOverlay(
  index: number,
  segments: OverlaySegment[]
): $CancellablePromise<ListDuplicateReport | null> {
  return $Call.ByName(
    "pvfine/services.EditorService.CheckListDuplicatesWithOverlay",
    index,
    segments
  );
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
