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
  /**
   * 界面上**显示关联内容并可双击打开**的列（规则未指定时同 column）。
   * 例：独立掉落的触发列是「掉落方式」，但「内联列表」显示在「掉落物品」列上。
   */
  displayColumn: number;
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
  /**
   * 每列声明的对象类型（来自规则 `Column.Ref`，与 columns 等长；没写 ref 的列为空串）。
   * 界面靠它认「哪一列是怪物 / 哪一列是掉落物品」，从而把编号与中文名当同一个搜索目标。
   */
  columnRefs?: string[];
  /** 每列的规则类型（text/int/rate/enum/ref），与 columns 等长。 */
  columnTypes?: string[];
  /** rate 列的满值刻度（如 1000000 表示 100%）；非 rate 列为 0。 */
  columnScales?: number[];
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

/** 对应 Go `services.FormViewDropItem`：内联列表里的一条候选（物品编号 + 权重）。 */
export interface FormViewDropItem {
  itemId: string;
  weight: string;
}

/** 对应 Go `services.FormViewDropEntry`：新增一条掉落配置（字段与 17 列一一对应）。 */
export interface FormViewDropEntry {
  /** false = 怪物（类型 0）/ true = APC（类型 1）。 */
  isApc: boolean;
  monsterId: string;
  /** true = 内联列表（掉落方式 1）/ false = 单一物品（掉落方式 0）。 */
  useList: boolean;
  itemId: string;
  list: FormViewDropItem[];
  /** 五个难度的掉落率，**按百分比**（20 = 20%）。 */
  rates: number[];
  counts: number[];
  levelMin: number;
  levelMax: number;
  jobLimit: string;
}

/**
 * 往 `[independent drop]` 段末尾追加一条配置，返回重新投影后的结果（只改归档内存，不落盘）。
 * fqn = pvfine/services.FormViewService.AddIndependentDrop
 *
 * 方法 ID 是**复算并反验过的**：用 `pvfine/services.ObjectViewService` 的三个已知 ID
 * （ListObjectTypes / ResolveObject / ReloadRules）验证 FNV-1a-32 算法 3/3 吻合后，
 * 才算出的这个值 —— 不是猜的。
 */
export function AddIndependentDrop(
  filePath: string,
  entry: FormViewDropEntry
): $CancellablePromise<FormViewProjection> {
  return $Call.ByID(3153989520, filePath, entry);
}

/**
 * 往**某一处** `[list]`（内联掉落列表）末尾追加一条候选（物品编号 + 权重），
 * 返回重新投影后的这一段。
 * fqn = pvfine/services.FormViewService.AddDropCandidate
 *
 * 方法 ID 同样复算 + 反验过（连同上面 AddIndependentDrop，4 个已知 ID 全部命中
 * FNV-1a-32 后才取用）。
 */
export function AddDropCandidate(
  filePath: string,
  section: string,
  occurrence: number,
  item: FormViewDropItem
): $CancellablePromise<FormViewSection> {
  return $Call.ByID(2721441661, filePath, section, occurrence, item);
}

/**
 * 删除**某一处** `[list]` 里第 `rowIndex` 行（0 基）的候选，返回重新投影后的这一段。
 *
 * 与 `DeleteIndependentDrop` 同一套格式规则（删掉前面的空白、让下一条往前靠、
 * 删完重新投影校验，不符就整体放弃）；同样只写归档内存、不落盘。
 * fqn = pvfine/services.FormViewService.DeleteDropCandidate
 *
 * 方法 ID 同样复算 + 反验过（连同本文件里已有的 AddDropCandidate /
 * DeleteIndependentDrop / ResolveRefNames / ApplyCellEdits / RuleText，
 * 5 个已知 ID 全部命中 FNV-1a-32 后才取用）。
 */
export function DeleteDropCandidate(
  filePath: string,
  section: string,
  occurrence: number,
  rowIndex: number
): $CancellablePromise<FormViewSection> {
  return $Call.ByID(1599531457, filePath, section, occurrence, rowIndex);
}

/**
 * 把一批 ref 值（怪物 / 物品编号）翻成中文名，返回 `{ 编号: 名字 }`（查不到的键不出现）。
 *
 * **与表格里的名字同源**：走的是后端那个 `refNameResolver`（表格投影用的同一个）。
 * 早先前端借用「对象视图」的 ResolveObject 显示草稿名，两条路对怪物并不等价，
 * 于是出现"物品能出名字、怪物出不来"（用户 2026-10-03 实测）。
 *
 * fqn = pvfine/services.FormViewService.ResolveRefNames（ID 同样反验过）
 */
export function ResolveRefNames(
  ref: string,
  ids: string[]
): $CancellablePromise<Record<string, string>> {
  return $Call.ByID(1504687577, ref, ids);
}

/**
 * 删除 `[independent drop]` 段里第 `rowIndex` 行（0 基）的**整条**掉落配置
 * （该行 + 它自带的 `[list]` 块），并把后面那一条"往前靠"，返回重新投影后的结果。
 * fqn = pvfine/services.FormViewService.DeleteIndependentDrop（ID 同样反验过）
 */
export function DeleteIndependentDrop(
  filePath: string,
  rowIndex: number
): $CancellablePromise<FormViewProjection> {
  return $Call.ByID(1159749652, filePath, rowIndex);
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

/** 对应 Go `services.FormViewCompletionColumn`。 */
export interface FormViewCompletionColumn {
  label: string;
  type?: string;
  values?: Record<string, string> | null;
  ref?: string;
  scale?: number;
  noneValue?: string;
}

/** 对应 Go `services.FormViewCompletionToken`（注释数据里的「段内第 N 个 token」）。 */
export interface FormViewCompletionToken {
  index: number;
  label: string;
  type?: string;
  values?: Record<string, string> | null;
}

/** 对应 Go `services.FormViewCompletionSection`。 */
export interface FormViewCompletionSection {
  section: string;
  label?: string;
  rowTokens?: number;
  columns?: FormViewCompletionColumn[] | null;
  tokens?: FormViewCompletionToken[] | null;
  formats?: string[] | null;
  /** `format` = 结构化视图规则；`annotation` = 注释数据。 */
  source: string;
}

/** 对应 Go `services.FormViewCompletionCatalog`。 */
export interface FormViewCompletionCatalog {
  rulePath: string;
  sections: FormViewCompletionSection[];
}

/**
 * 编辑器脚本补全用的段目录：**全量段名 + 段内字段/枚举取值**（后端把结构化视图规则
 * 与「注释数据」合并去重后下发；前端不各自解析，避免两处口径漂移）。
 * fqn = pvfine/services.FormViewService.CompletionCatalog
 */
export function CompletionCatalog(): $CancellablePromise<FormViewCompletionCatalog> {
  return $Call.ByID(3329411431);
}

/**
 * 往「段 + 第几次出现」的末尾追加一行（`values` 个数必须等于规则里该段的 rowTokens）。
 *
 * 这是**通用的段行编辑**，与独立掉落那套（`AddIndependentDrop` / `AddDropCandidate`）**互不影响**：
 * 独立掉落有自己的语义（一行 + 紧跟的 `[list]` 块、17 列写死），见 `formview_add.go` / `formview_delete.go`。
 * fqn = pvfine/services.FormViewService.InsertSectionRow
 * 方法 ID 用已知的 `ListFormats`(3541309786) 反验 FNV-1a-32 算法后复算。
 */
export function InsertSectionRow(
  filePath: string,
  section: string,
  occurrence: number,
  values: string[]
): $CancellablePromise<FormViewSection> {
  return $Call.ByID(3375162946, filePath, section, occurrence, values);
}

/**
 * 删除「段 + 第几次出现」里的若干行（0 基行号；后端从后往前删，删完重新投影校验）。
 * 同样**不连带删** `[list]` 块 —— 那是独立掉落的专属语义。
 * fqn = pvfine/services.FormViewService.DeleteSectionRows
 */
export function DeleteSectionRows(
  filePath: string,
  section: string,
  occurrence: number,
  rowIndexes: number[]
): $CancellablePromise<FormViewSection> {
  return $Call.ByID(4041113065, filePath, section, occurrence, rowIndexes);
}

/**
 * 新建一个「商店条目」：往商店文件里追加 `[tab]` + `` `条目名` `` + 内置首个物品的 `[item list]`。
 *
 * 商店模块专属（与独立掉落、通用段行增删**互不影响**）。按商店文件现有格式写：
 * 条目名用**反引号对**包住（与文件里 `` `[weapon shop]` `` 同一个符号）。
 * fqn = pvfine/services.FormViewService.AppendShopTab
 * 方法 ID 用已知的 `ListFormats`(3541309786) 反验 FNV-1a-32 算法后复算。
 */
export function AppendShopTab(
  filePath: string,
  name: string,
  firstItem: string
): $CancellablePromise<FormViewProjection> {
  return $Call.ByID(3470123801, filePath, name, firstItem);
}

/**
 * 删除第 `occurrence` 个**整个商店条目**（一个 `[tab]` 块，含它自己的 `[item list]`）。
 *
 * 与「删除段内一行」（`DeleteSectionRows`）分开：那是行级，这是块级、整条目。
 * 商店模块专属；后端删完会重新投影校验（条目数必须正好 -1），不符就整体放弃。
 * fqn = pvfine/services.FormViewService.DeleteShopTab
 * 方法 ID 用已知的 `ListFormats`(3541309786) 反验 FNV-1a-32 算法后复算。
 */
export function DeleteShopTab(
  filePath: string,
  occurrence: number
): $CancellablePromise<FormViewProjection> {
  return $Call.ByID(139918680, filePath, occurrence);
}
