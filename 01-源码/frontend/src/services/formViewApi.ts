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
  /** ref 列解析出的目标名称（如怪物 ID → 中文名）；解析不到时为空。 */
  name?: string;
  /**
   * 偏移。**注意单位**：Go 侧 `ScriptView` 说明为"归一化换行后的 UTF-16 单元"，
   * 不能拿去切原始文本（非 ASCII / CRLF 会切错），所以回写一律走
   * `ApplyCellEdits`（结构化改写引擎），不用这两个值。
   */
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
  /** 因被行关联认领、已从 sections 里移除的目标段次数。 */
  linkedTargets?: number;
  warnings: string[];
}

/** 对应 Go `services.FormViewCellEdit`：把某段某行某列改成什么。 */
export interface FormViewCellEdit {
  section: string;
  occurrence: number;
  row: number;
  column: number;
  value: string;
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

/**
 * 只取「某段第 N 次出现」的投影（1 基）。被行关联认领的段不在主投影里，界面双击时按需取。
 * fqn = pvfine/services.FormViewService.ProjectSectionOccurrence
 */
export function ProjectSectionOccurrence(
  filePath: string,
  section: string,
  occurrence: number
): $CancellablePromise<FormViewSection> {
  return $Call.ByID(3685630439, filePath, section, occurrence);
}

/**
 * 应用一批单元格改动，返回**重新投影后**的结果（服务端做完定位、校验、写回）。
 * 只写归档内存，落盘仍走主工具条的「保存 PVF」。
 * fqn = pvfine/services.FormViewService.ApplyCellEdits
 */
export function ApplyCellEdits(
  filePath: string,
  edits: FormViewCellEdit[]
): $CancellablePromise<FormViewProjection> {
  return $Call.ByID(394327007, filePath, edits);
}
