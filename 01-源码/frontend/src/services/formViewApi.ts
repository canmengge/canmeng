/**
 * 结构化视图服务的调用适配层（**临时**）。
 *
 * ## 为什么不放在 frontend/bindings/ 里
 *
 * 与 `objectViewApi.ts` 同因：`frontend/bindings/` 由 `wails3 generate bindings` 生成、
 * 禁手工修改，而本机**无法构建 wails3 CLI**（模块缓存里 `build_assets/windows/msix`
 * 与 `.../nsis` 是空目录）。因此这里放独立适配层，**不碰** `bindings/`。
 *
 * ## 方法 ID 是复算出来的，不是猜的
 *
 * 算法（wails v3.0.0-beta.12）：methodID = FNV-1a-32("pvfine/services.<Type>.<Method>")。
 * 本次用临时探针复算 `ObjectViewService` 的 3 个已知 ID，**3/3 与 objectViewApi.ts 吻合**，
 * 才用它算出 FormViewService 的 3 个 ID。
 *
 * ## 何时删除本文件
 *
 * 官方生成器可用后，改用 `bindings/pvfine/services` 的生成结果并删除本文件；
 * 届时此处的接口应与 Go 侧 JSON tag 逐字段一致。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `services.FormViewFormatInfo`。 */
export interface FormViewFormatInfo {
  id: string;
  label: string;
  files: string[];
  notes?: string;
  sections: string[];
}

/** 对应 Go `services.FormViewFormatListResult`。 */
export interface FormViewFormatListResult {
  rulePath: string;
  formatCount: number;
  formats: FormViewFormatInfo[];
}

/** 对应 Go `formview.Cell`。display 为有翻译/换算时的可读文本。 */
export interface FormViewCell {
  value: string;
  display?: string;
  /** UTF-16 偏移，与 CodeMirror 一致。 */
  start: number;
  end: number;
}

/**
 * 对应 Go `formview.RowLink`：本行关联到的**另一段**（如独立掉落的 [list]）。
 * 规则里配了 links 且本行命中时才有。
 */
export interface FormViewRowLink {
  /** 触发链接的列下标（0 基）。 */
  column: number;
  /** 被引用段的段名。 */
  targetSection: string;
  /** 被引用段在本文件里的第几次出现（1 基），与 FormViewSection.occurrence 对应。 */
  occurrence: number;
  /** 查看器标题（来自规则）。 */
  title?: string;
}

/** 对应 Go `formview.Row`。 */
export interface FormViewRow {
  index: number;
  complete: boolean;
  cells: FormViewCell[];
  /** 本行关联到的另一段（规则配了 links 且命中时才有）。 */
  link?: FormViewRowLink;
}

/** 对应 Go `formview.ProjectedSection`。 */
export interface FormViewSection {
  section: string;
  label: string;
  kind: string;
  occurrence: number;
  columns: string[];
  rows: FormViewRow[];
  tokenCount: number;
  warnings?: string[];
}

/** 对应 Go `formview.Projection`。 */
export interface FormViewProjection {
  formatId: string;
  formatLabel: string;
  file: string;
  tokenCount: number;
  sections: FormViewSection[];
  warnings: string[];
}

/**
 * 返回规则文件里定义的全部文件族。
 * fqn = pvfine/services.FormViewService.ListFormats
 */
export function ListFormats(): $CancellablePromise<FormViewFormatListResult> {
  return $Call.ByID(3541309786);
}

/**
 * 把一个归档文件按规则投影成「段 → 行 → 列」表格（只读）。
 * fqn = pvfine/services.FormViewService.ProjectFile
 */
export function ProjectFile(filePath: string): $CancellablePromise<FormViewProjection> {
  return $Call.ByID(1684362141, filePath);
}

/**
 * 重新读取规则文件（config/formats.json）。
 * fqn = pvfine/services.FormViewService.ReloadRules
 */
export function ReloadRules(): $CancellablePromise<FormViewFormatListResult> {
  return $Call.ByID(38255038);
}
