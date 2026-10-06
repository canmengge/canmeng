/**
 * 大文件「全文查找」调用的适配层（与 `largeTextApi.ts` 同源做法）。
 *
 * `frontend/bindings/` 由 `wails3 generate bindings` 生成、本机无法重建，因此新增的
 * Go 方法统一走 `Call.ByName`，不碰 `bindings/`。
 * fqn = `pvfine/services.EditorService.SearchInFile`
 */
// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";
import type { OverlaySegment } from "./largeTextApi";

/** 单文件内的一处命中；行号 1 基，列与长度按字符计。 */
export interface FileSearchMatch {
  line: number;
  column: number;
  length: number;
  /** 命中所在行的原文（截断后，仅供结果预览）。 */
  text: string;
}

export interface FileSearchResult {
  matches: FileSearchMatch[];
  /** 真实命中数（可能大于 matches.length）。 */
  total: number;
  truncated: boolean;
}

/**
 * 在整个文件里查找（后端扫全文，并把「未写回归档」的段叠加后再搜）。
 *
 * `segments` 传 `editor.pendingSegmentsOf(index)`，这样用户还没保存的改动也搜得到，
 * 但归档一个字节都不会被改动。
 */
/**
 * 把用户输入折成「真正拿去搜的查询」。
 *
 * 【2026-10-06 用户实测反馈：搜 `14 10000` 搜不到】PVF 里 token 之间是 **TAB**
 * （脚本里显示成 `14⇥10000`），而人习惯打**空格**；字面量匹配空格必然落空。
 * 于是：**多关键词**时折成正则，词之间允许任意空白（`14\s+10000`）——TAB、空格、多个空格都能命中；
 * **单个关键词**维持原字面行为（不进正则，行为与之前完全一致）。
 */
export function queryToSearchPattern(term: string): { query: string; regex: boolean } {
  const words = term
    .split(/\s+/)
    .map((word) => word.trim())
    .filter((word) => word.length > 0);
  if (words.length <= 1) return { query: term, regex: false };
  const escaped = words.map((word) => word.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  return { query: escaped.join("\\s+"), regex: true };
}

export function SearchInFile(
  index: number,
  query: string,
  caseSensitive: boolean,
  regex: boolean,
  wholeWord: boolean,
  limit: number,
  segments: OverlaySegment[]
): $CancellablePromise<FileSearchResult | null> {
  return $Call.ByName(
    "pvfine/services.EditorService.SearchInFile",
    index,
    query,
    caseSensitive,
    regex,
    wholeWord,
    limit,
    segments
  );
}
