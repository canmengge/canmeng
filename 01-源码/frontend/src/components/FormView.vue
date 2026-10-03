<script setup lang="ts">
/**
 * 可视化编辑区 —— 结构化视图（只读）。
 *
 * 按外部规则（config/formats.json）把**一个文件**投影成「段 → 行 → 列」表格，
 * 例如 etc/independent_drop.etc 的 17 列掉落配置行。
 *
 * **只跑在独立窗口里**（`?view=formview` + FormViewWindow.vue）。侧栏那份已经在
 * 2026-10-03 按用户要求整体删掉（UI 与功能都不保留），入口改为工具条
 * 「可视化编辑区」下拉。
 *
 * 布局要点（修掉"不能下滑"）：头/工具栏/页脚固定，**中间只有一个滚动区**
 * （`.fv-body`），且 flex 链上每层都写 `min-height: 0` —— flex 子项默认
 * `min-height: auto`，不写就撑不出滚动条。
 *
 * 关联：规则里配了 links 的列（如独立掉落的「掉落方式」= 内联列表 / 外部文件），
 * 该格**双击**会打开被引用段（紧跟其后的 [list]）—— 那里的候选（物品 / 权重）**也能改**
 * （用户 2026-10-03 要求：内联列表不能只读，否则可视化没意义）。
 *
 * 编辑模型（对齐装备文本编辑）：**先改前端草稿、立刻显示；点「保存改动」→ 写进归档内存
 * 并立即写入 PVF 文件（落盘）**。草稿自带段信息，所以主表与各处内联列表可以混在一次提交里。
 *
 * 2026-10-03 用户要求：可视化这边的「保存改动」就该等于"保存这个掉落文件"，
 * 不该只改内存、再让用户回主窗口点一次「保存 PVF」（那样主窗口标签还会挂个绿点）。
 */
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
// 草稿里的 ref 值也要能显示中文名：直接复用「对象视图」的解析（同一个 Go 进程、同一套规则），
// 不新增后端接口（用户 2026-10-03 要求：把 3015 改成 3037 时名字要跟着变）。
import {
  AddDropCandidate,
  AddIndependentDrop,
  DeleteIndependentDrop,
  ResolveRefNames,
  type FormViewDropItem,
} from "../services/formViewApi";
import {
  NButton,
  NEmpty,
  NInput,
  NModal,
  NCheckbox,
  NSelect,
  NSpin,
  NTag,
  useMessage,
} from "naive-ui";
import { useEditorStore } from "../stores/editor";
import { useFormViewStore } from "../stores/formView";
import type { FormViewRow, FormViewSection } from "../services/formViewApi";

const formView = useFormViewStore();
const editor = useEditorStore();
const message = useMessage();

/** 主表分页大小（一次渲染上万行会拖慢界面）。 */
// 每页行数（用户 2026-10-03：200 → 500）。
const pageSize = 500;
const page = ref(1);
/** 折叠段里最多先渲染多少行。 */
const inlineRowLimit = 50;

/** 列宽：段名 → 各列像素宽（0 表示"自动"）。 */
const columnWidths = ref<Record<string, number[]>>({});



onMounted(() => {
  void formView.loadFormats();
});

onUnmounted(() => {
  stopResize();
});

/**
 * 规则说明的**折叠文本**。
 *
 * 刻意用 JS 截断而不是 CSS `line-clamp`：2026-10-03 事故里发现，前端 JS 已是新版
 * （状态栏版号对得上）但**样式表的行为没生效**（说明没折叠、表格空白还在）。
 * 凡是"必须生效"的布局，一律走 JS + 行内样式 —— 那样任何 CSS 缓存都挡不住。
 */
const notesBrief = computed(() => {
  const full = (formView.currentFormat?.notes ?? "").trim();
  if (full.length <= 110) return full;
  return `${full.slice(0, 110)}…（悬停看全文）`;
});

/** 界面标题：文件族名 + 「编辑」（如 独立掉落 → 独立掉落编辑）。 */
const viewTitle = computed(() => {
  const label = formView.currentFormat?.label ?? formView.projection?.formatLabel ?? "";
  return label === "" ? "可视化编辑" : `${label}编辑`;
});

/** 主表 = 行数最多的那一段（通常是主配置表，如「掉落配置行」）。 */
const mainSection = computed<FormViewSection | null>(() => {
  const sections = formView.projection?.sections ?? [];
  let best: FormViewSection | null = null;
  for (const section of sections) {
    if (!best || section.rows.length > best.rows.length) best = section;
  }
  return best;
});

/**
 * 被「行 → 关联」认领过的段：已在主表里可双击查看，不再重复堆到「其它段」。
 * 键为 `段名小写#出现序号`。
 */
const linkedSectionKeys = computed(() => {
  const keys = new Set<string>();
  for (const section of formView.projection?.sections ?? []) {
    for (const row of section.rows) {
      if (!row.link) continue;
      keys.add(`${row.link.targetSection.toLowerCase()}#${row.link.occurrence}`);
    }
  }
  return keys;
});

/**
 * 「其它段」列表。
 *
 * 用户 2026-10-03 要求**去掉**这块 UI：主投影里剩下的都是没被任何行关联的零散段
 * （例如没配对的几处 `[list]`），铺在下面只会干扰主表。这里直接返回空数组 ⇒
 * 模板里那段 `v-if="otherSections.length"` 永远不渲染（标记留着，将来若想恢复，
 * 把下面这段 filter 放回去即可 —— 它过滤的正是"非主段 且 未被行关联认领"的段）。
 */
const otherSections = computed<FormViewSection[]>(() => []);

// ---- 搜索（怪物 / 掉落物品：ID 与中文名视为同一个目标）----
//
// 用户要求（2026-10-03）：加两个搜索 —— ① 怪物 ID / 名称 ② 掉落物品名 / ID；
// 「搜名称和搜 ID 要显示是一个」。做法：按列的 ref（规则里声明的对象类型）圈定列，
// 再把该格的**原值（编号）与解析出的中文名一起比**，任一命中即算命中。
// 分组入口由 ref 自动生成（怪物 = monster，掉落物品 = equipment|stackable…），
// 所以规则改了、文件族换了都不用改这段代码。

/** 可选搜索范围：一列（或一组同 ref 的列）= 一个范围。 */
type SearchScope = { ref: string; label: string };

const searchScopes = computed<SearchScope[]>(() => {
  const section = mainSection.value;
  if (!section) return [];
  const refs = section.columnRefs ?? [];
  const scopes: SearchScope[] = [];
  const seen = new Set<string>();
  (section.columns ?? []).forEach((label, index) => {
    const ref = (refs[index] ?? "").trim();
    if (ref === "" || seen.has(ref)) return;
    seen.add(ref);
    scopes.push({ ref, label: label.trim() === "" ? ref : label });
  });
  return scopes;
});

const searchRef = ref("");
const searchQuery = ref("");
/**
 * 搜哪个字段：ID / 名称。
 *
 * 用户 2026-10-03：搜 `28` 本意是找"怪物ID = 28"，可**名字里带 28 的行也被搜出来**了 ⇒
 * 必须能把"按 ID 搜"和"按名称搜"分开。
 */
const searchField = ref<"id" | "name">("id");
/** 精确匹配（照抄主工具条那颗「启用精确匹配」）：开启后必须整串相等，而不是包含。 */
const searchExact = ref(false);

/** 命中搜索范围那些列的下标（规则里可能有多列共用一个 ref）。 */
const searchColumns = computed<number[]>(() => {
  const section = mainSection.value;
  const ref = searchRef.value.trim();
  if (!section || ref === "") return [];
  const refs = section.columnRefs ?? [];
  const indexes: number[] = [];
  refs.forEach((value, index) => {
    if ((value ?? "").trim() === ref) indexes.push(index);
  });
  return indexes;
});

/** 一行的目标列里，编号或中文名任一含关键字即命中（都按小写子串比）。 */
/**
 * 一行是否命中：**先按字段（ID / 名称）分开**，再按"包含 / 精确"比。
 *
 * - 字段 = ID：只比编号（草稿里的新编号也算 ✓）
 * - 字段 = 名称：只比中文名
 * - 精确匹配开启：必须**整串相等**（搜怪物ID 28 ⇒ 只有编号正好是 28 的那行）
 */
function rowMatchesSearch(row: FormViewRow, query: string): boolean {
  for (const index of searchColumns.value) {
    const cell = row.cells[index];
    if (!cell) continue;
    const target =
      searchField.value === "name"
        ? rowName(mainSection.value, row, index).trim().toLowerCase()
        : cellCurrent(row, index).trim().toLowerCase();
    if (target === "") continue;
    if (searchExact.value ? target === query : target.includes(query)) return true;
  }
  return false;
}

/** 命中的**行号**集合（行号 = 投影里的 index，改值就用它定位）。 */
const searchHits = computed<Set<number>>(() => {
  const hits = new Set<number>();
  const query = searchQuery.value.trim().toLowerCase();
  if (searchRef.value.trim() === "" || query === "") return hits;
  for (const row of mainSection.value?.rows ?? []) {
    if (rowMatchesSearch(row, query)) hits.add(row.index);
  }
  return hits;
});

const searchActive = computed(
  () => searchRef.value.trim() !== "" && searchQuery.value.trim() !== ""
);

/** 表格实际展示的行：搜索激活时只留命中行。 */
const visibleRows = computed<FormViewRow[]>(() => {
  const rows = mainSection.value?.rows ?? [];
  if (!searchActive.value) return rows;
  return rows.filter((row) => searchHits.value.has(row.index));
});

function clearSearch(): void {
  searchRef.value = "";
  searchQuery.value = "";
}

const pageCount = computed(() =>
  Math.max(1, Math.ceil(visibleRows.value.length / pageSize))
);

const pagedRows = computed<FormViewRow[]>(() => {
  const start = (page.value - 1) * pageSize;
  return visibleRows.value.slice(start, start + pageSize);
});

watch([searchRef, searchQuery], () => {
  page.value = 1;
});

// ---- 批量改（整列 / 命中行做数值运算）----
//
// 不新增后端接口：直接复用已有的「按（段, 出现序号, 行, 列）改一格」通道
// （ApplyCellEdits：唯一段守卫 → 克隆改写 → 逐格校验 → core.setText，**不落盘**）。
// 一条 op 只改一个 token，所以"整列 ×N"就是**每行一条** —— 1834 行 = 1834 条，
// 一次提交、一次校验、一次回写。

const batchVisible = ref(false);
const batchColumn = ref(-1);
const batchOperator = ref("×");
const batchOperand = ref("1");
/** 只改「当前搜索命中的行」（没搜索时该开关无效，等于全表）。 */
const batchOnlyHits = ref(true);
const batchRunning = ref(false);

const batchColumns = computed(() =>
  (mainSection.value?.columns ?? []).map((label, index) => ({
    label: label.trim() === "" ? `第 ${index + 1} 列` : label,
    value: index,
  }))
);

/** 本次批量要作用到的行号（升序）。 */
const batchTargets = computed<number[]>(() => {
  const rows = mainSection.value?.rows ?? [];
  if (batchOnlyHits.value && searchActive.value) {
    return [...searchHits.value].sort((left, right) => left - right);
  }
  return rows.map((row) => row.index);
});

/** 与改写引擎（applyBatchNumericOperator）同名同义，保证"算出来 = 引擎会算的"。 */
function applyNumericOperator(current: number, operator: string, operand: number): number {
  switch (operator) {
    case "=":
      return operand;
    case "+":
      return current + operand;
    case "-":
      return current - operand;
    case "×":
      return current * operand;
    case "÷":
      return operand === 0 ? Number.NaN : current / operand;
    default:
      return Number.NaN;
  }
}

/** 归档里是整数或 float32：整数写成整数，其余最多留 6 位小数。 */
function formatNumeric(value: number): string {
  if (!Number.isFinite(value)) return "";
  if (Number.isInteger(value)) return String(value);
  return String(Math.round(value * 1e6) / 1e6);
}

async function runBatch(): Promise<void> {
  const section = mainSection.value;
  if (!section || batchColumn.value < 0) {
    message.warning("先选一列");
    return;
  }
  if (fileOpenInEditor()) {
    message.warning(
      "该文件正在编辑区打开：请先关掉那个标签页，再回来改（避免两处同时改同一份文本）"
    );
    return;
  }
  const operand = Number(batchOperand.value);
  if (!Number.isFinite(operand)) {
    message.warning("运算数必须是数字");
    return;
  }
  const rows = batchTargets.value;
  if (rows.length === 0) {
    message.warning("没有要改的行");
    return;
  }

  const column = batchColumn.value;
  const edits: {
    section: string;
    occurrence: number;
    row: number;
    column: number;
    value: string;
  }[] = [];
  let skipped = 0;
  for (const rowIndex of rows) {
    const row = section.rows[rowIndex];
    const cell = row?.cells[column];
    if (!row || !cell) {
      skipped += 1;
      continue;
    }
    // 取**当前值**（草稿优先）：连续两次批量改要能叠加，而不是都从归档原值重算。
    const current = Number(cellCurrent(row, column));
    if (!Number.isFinite(current)) {
      // 非数值格（空值 / 文本）不参与数值运算，跳过并如实报数。
      skipped += 1;
      continue;
    }
    const text = formatNumeric(applyNumericOperator(current, batchOperator.value, operand));
    if (text === "" || text === cell.value) continue;
    edits.push({
      section: section.section,
      occurrence: section.occurrence,
      row: rowIndex,
      column,
      value: text,
    });
  }
  if (edits.length === 0) {
    message.warning(skipped > 0 ? `没有可改的数值格（跳过 ${skipped} 格）` : "数值没有变化");
    return;
  }

  batchRunning.value = true;
  try {
    // 与单格编辑同一个模型：**只落草稿**，点「保存改动」才写进归档内存。
    const staged = stageEdits(edits);
    batchVisible.value = false;
    if (staged === 0) {
      message.warning("改动算下来与原值一致，没有可暂存的内容");
    } else {
      message.success(
        `批量改：已暂存 ${staged} 格改动（点「保存改动」写进归档）` +
          (skipped > 0 ? `；跳过 ${skipped} 格（非数值）` : "")
      );
    }
  } finally {
    batchRunning.value = false;
  }
}

watch(
  () => formView.projection,
  () => {
    page.value = 1;
    // 每次重新解析都回到"按内容自适应"：避免上一次拖出来的宽度把撑开的空白冻住
    // （2026-10-03 事故：中间那一片空白一直消不掉）。
    columnWidths.value = {};
    // 投影换了（重新解析、或草稿保存成功后的回传）⇒ 旧草稿的行列坐标不再可信，清掉。
    pendingEdits.value = new Map();
    // 搜索**默认选中第一个可搜的列**（按规则列顺序；独立掉落就是「怪物/APC」，ref = monster）。
    // 用户 2026-10-03：默认空白要手点一次太麻烦，默认就要能直接打字搜。
    if (searchRef.value === "" && searchScopes.value.length > 0) {
      searchRef.value = searchScopes.value[0].ref;
    }
  }
);

/**
 * 该格「当前应该显示的值」：**本地草稿优先**，没有草稿才用归档里的值。
 *
 * 这是「改了立刻看见」的关键 —— 编辑 / 批量改只写草稿，界面马上按草稿渲染，
 * 不再走"提交后端 → 等重新投影 → 才显示新值"那条慢路径。
 */
function cellCurrent(row: FormViewRow, index: number): string {
  const draft = draftValue(mainSection.value, row.index, index);
  if (draft !== null) return draft;
  return row.cells[index]?.value ?? "";
}

/** 该格是否有未保存的草稿（界面上要标出来）。 */
function isDirtyCell(row: FormViewRow, index: number): boolean {
  const section = mainSection.value;
  if (!section) return false;
  return pendingEdits.value.has(
    draftKey(section.section, section.occurrence, row.index, index)
  );
}

/**
 * 按列规则把原始值渲染成可读文本（后端算 `display` 用的是同一套规则）。
 *
 * 用途：**草稿 / 编辑态**没有后端给的 `display`，必须前端自己按 Type+Scale 换算 ——
 * 否则改完还没保存的那一格会露出原始大数字（用户 2026-10-03 指出：
 * 掉落率 1000000 应显示 100%，改成 10000 应立刻显示 1%）。
 */
function formatCellValue(index: number, raw: string): string {
  return formatByRule(mainSection.value, index, raw);
}

/** 按某个段的列规则渲染原始值（草稿态与主表/查看器共用同一套换算）。 */
function formatByRule(
  section: FormViewSection | null,
  index: number,
  raw: string
): string {
  const type = (section?.columnTypes?.[index] ?? "").trim().toLowerCase();
  const scale = section?.columnScales?.[index] ?? 0;
  if (type === "rate" && scale > 0) {
    const value = Number(raw);
    if (Number.isFinite(value)) {
      const percent = (value / scale) * 100;
      const text = Number.isInteger(percent)
        ? String(percent)
        : String(Math.round(percent * 100) / 100);
      return `${text}%`;
    }
  }
  return raw;
}

function cellText(row: FormViewRow, index: number): { text: string; raw: string } {
  const cell = row.cells[index];
  if (!cell) return { text: "", raw: "" };
  if (isDirtyCell(row, index)) {
    const current = cellCurrent(row, index);
    return { text: formatCellValue(index, current), raw: current };
  }
  return {
    text: cell.display && cell.display !== "" ? cell.display : cell.value,
    raw: cell.value,
  };
}

function isNumeric(value: string): boolean {
  return /^-?\d+$/.test(value);
}

function sectionTitle(section: FormViewSection): string {
  const suffix = section.occurrence > 1 ? ` #${section.occurrence}` : "";
  return `${section.label || section.section}${suffix}`;
}

/**
 * 该格是否是「显示关联内容并可双击打开」的格（规则里的 displayColumn）。
 *
 * 交互落在**显示列**（如「掉落物品」）上；**触发列**（如「掉落方式」）只当普通标签 ——
 * 2026-10-03 用户要求：内联列表时，掉落物品列不该显示那个无意义的「金币 0」。
 */
function isLinkCell(row: FormViewRow, index: number): boolean {
  return !!row.link && row.link.displayColumn === index;
}

/**
 * 关联行在显示列上要写的文字：取**触发列**的可读文本（「内联列表」/「外部文件」）。
 */
function linkCellText(row: FormViewRow, index: number): string {
  const link = row.link;
  if (!link || link.displayColumn !== index) return "";
  const trigger = row.cells[link.column];
  if (!trigger) return "";
  return trigger.display && trigger.display !== "" ? trigger.display : trigger.value;
}

/** 名称解析结果（ref 列才有）。 */
function cellName(row: FormViewRow, index: number): string {
  return row.cells[index]?.name ?? "";
}

function cellTitle(row: FormViewRow, index: number): string {
  const raw = row.cells[index]?.value ?? "";
  const name = cellName(row, index);
  if (isLinkCell(row, index) && row.link) {
    const target = row.link.title || row.link.targetSection;
    return `这一行是「${linkCellText(row, index)}」：双击查看关联的「${target}」（第 ${row.link.occurrence} 处）`;
  }
  if (name !== "") {
    return `${name}\n编号: ${raw}\n双击可改（先存本地草稿，点「保存改动」才写进归档内存）`;
  }
  return `原值: ${raw}\n双击可改（先存本地草稿，点「保存改动」才写进归档内存）`;
}

// ---- 行 → 关联段（双击查看） ----

const viewerVisible = ref(false);
const viewerLoading = ref(false);
const viewerTitle = ref("");
const viewerSection = ref<FormViewSection | null>(null);
const viewerError = ref("");

/**
 * 打开关联段。
 *
 * 被行关联认领的段**不在主投影里**（一个文件有 862 次 [list]，全带上会让 payload 膨胀），
 * 所以这里按「段名 + 出现序号」向服务端单独取那一次出现。
 */
async function openLink(row: FormViewRow): Promise<void> {
  const link = row.link;
  if (!link) return;
  const name = link.title || link.targetSection;
  viewerTitle.value = `${name} · [${link.targetSection}] 第 ${link.occurrence} 处`;
  viewerSection.value = null;
  viewerError.value = "";
  viewerVisible.value = true;
  viewerLoading.value = true;
  try {
    viewerSection.value = await formView.loadLinkedSection(
      link.targetSection,
      link.occurrence
    );
  } catch (issue: any) {
    viewerError.value = String(issue?.message ?? issue);
  } finally {
    viewerLoading.value = false;
  }
}

// ---- 查看器（内联列表）里的编辑：同一套草稿 ----
//
// 用户 2026-10-03 要求：内联列表（掉落候选 `[list]`）必须能改，不能只是"可看"。
// 后端已经支持按（段名, 出现序号, 行, 列）定位到某一处 `[list]`（同名段有 862 次），
// 所以这里只需把查看器的格子接上跟主表一样的编辑 + 草稿。

type ViewerEditState = { row: number; column: number };

const viewerEditing = ref<ViewerEditState | null>(null);
const viewerEditText = ref("");

/** 查看器某格当前应显示的值（草稿优先）。 */
function viewerCellCurrent(row: FormViewRow, index: number): string {
  const draft = draftValue(viewerSection.value, row.index, index);
  if (draft !== null) return draft;
  return row.cells[index]?.value ?? "";
}

/** 查看器某格是否有未保存草稿。 */
function viewerCellDirty(row: FormViewRow, index: number): boolean {
  const section = viewerSection.value;
  if (!section) return false;
  return pendingEdits.value.has(
    draftKey(section.section, section.occurrence, row.index, index)
  );
}

function viewerCellText(row: FormViewRow, index: number): { text: string; raw: string } {
  const cell = row.cells[index];
  if (!cell) return { text: "", raw: "" };
  if (viewerCellDirty(row, index)) {
    const current = viewerCellCurrent(row, index);
    return { text: formatByRule(viewerSection.value, index, current), raw: current };
  }
  return {
    text: cell.display && cell.display !== "" ? cell.display : cell.value,
    raw: cell.value,
  };
}

function startViewerEdit(row: FormViewRow, index: number): void {
  if (!row.cells[index]) return;
  if (fileOpenInEditor()) {
    message.warning(
      "该文件正在编辑区打开：请先关掉那个标签页，再回来改（避免两处同时改同一份文本）"
    );
    return;
  }
  viewerEditText.value = viewerCellCurrent(row, index);
  viewerEditing.value = { row: row.index, column: index };
}

function cancelViewerEdit(): void {
  viewerEditing.value = null;
}

/** 回车 / 失焦都落到草稿（与主表同一规则）。 */
function commitViewerEdit(): void {
  const state = viewerEditing.value;
  const section = viewerSection.value;
  if (!state || !section) return;
  stageEdits([
    { section, row: state.row, column: state.column, value: viewerEditText.value },
  ]);
  viewerEditing.value = null;
}

// ---- 查看器里「添加候选」（往这一处 [list] 末尾追加一条） ----
//
// 用户 2026-10-03 要求：内联列表里也要能加 —— 只加"物品ID + 权重"，
// 格式与现有候选行完全一致（两个制表符缩进）。

const viewerAddVisible = ref(false);
const viewerAddItemId = ref("");
const viewerAddWeight = ref("1000");
const viewerAdding = ref(false);

/**
 * 排队中的「新增」。
 *
 * 用户 2026-10-03 明确要求：**新增也不能立刻生效** —— 与改一格同一套模型，
 * 先排队（草稿），点「保存改动」时才真正写进归档内存。
 * 插入本来没法当"单元格草稿"存（它是新增行），所以单独排一队。
 */
type PendingInsert =
  | { kind: "candidate"; section: string; occurrence: number; item: FormViewDropItem }
  | { kind: "drop"; entry: FormViewDropEntry };

const pendingInserts = ref<PendingInsert[]>([]);
const pendingInsertCount = computed(() => pendingInserts.value.length);

/**
 * 排队中的「删除选中」：存的是**段里的行号**（0 基）。
 *
 * 提交时按**行号从大到小**执行 —— 删一行会让后面所有行号前移，倒着删才不会错位。
 */
const pendingDeletes = ref<number[]>([]);
const pendingDeleteCount = computed(() => pendingDeletes.value.length);

/** 当前选中的行（点行选中；点已选中的行取消选中）。 */
const selectedRow = ref<number | null>(null);

function toggleSelectRow(row: FormViewRow): void {
  selectedRow.value = selectedRow.value === row.index ? null : row.index;
}

function queueDeleteSelected(): void {
  const index = selectedRow.value;
  if (index === null) {
    message.warning("先点一行选中它");
    return;
  }
  if (pendingDeletes.value.includes(index)) {
    message.info("这一行已经在待删除列表里了");
    return;
  }
  pendingDeletes.value = [...pendingDeletes.value, index].sort((left, right) => left - right);
  selectedRow.value = null;
  message.success("已加入待删除（点上方「保存改动」才真正删除）");
}

/** 该处 `[list]` 排队中的候选（查看器里显示成"还没保存"的行）。 */
function pendingCandidates(section: FormViewSection | null): FormViewDropItem[] {
  if (!section) return [];
  return pendingInserts.value
    .filter(
      (item): item is Extract<PendingInsert, { kind: "candidate" }> =>
        item.kind === "candidate" &&
        item.section.toLowerCase() === section.section.toLowerCase() &&
        item.occurrence === section.occurrence
    )
    .map((item) => item.item);
}

function startViewerAdd(): void {
  viewerAddItemId.value = "";
  viewerAddWeight.value = "1000";
  viewerAddVisible.value = true;
}

/** **只排队**，不碰归档（点「保存改动」才写）。 */
function submitViewerAdd(): void {
  const section = viewerSection.value;
  if (!section) return;
  if (fileOpenInEditor()) {
    message.warning(
      "该文件正在编辑区打开：请先关掉那个标签页，再加（避免两处同时改同一份文本）"
    );
    return;
  }
  const itemId = viewerAddItemId.value.trim();
  if (itemId === "") {
    message.warning("物品ID 不能为空");
    return;
  }
  pendingInserts.value = [
    ...pendingInserts.value,
    {
      kind: "candidate",
      section: section.section,
      occurrence: section.occurrence,
      item: { itemId, weight: viewerAddWeight.value.trim() || "1000" },
    },
  ];
  viewerAddVisible.value = false;
  message.success("已加入待提交（点上方「保存改动」才写进归档内存）");
}

// ---- 草稿里的 ref 值 → 中文名（实时解析） ----
//
// 归档里的名称是后端投影时解析好、跟着投影一起下发的；草稿是"还没写进归档的新 ID"，
// 所以没有名字。这里用「对象视图」的解析补上：ref 支持 `a|b` 多候选（与后端规则一致），
// 逐个试到有名字为止。结果按 `ref|id` 缓存 + 去重，同一个 ID 只问一次。

const draftNames = ref<Map<string, string>>(new Map());
const pendingNameLookups = new Set<string>();

/**
 * 该名字是否**正在查**。
 *
 * 用来区分两件事：① 请求还没回来（等一下就会出现）② 确实查不到（登记表里没有这个编号）。
 * 用户 2026-10-03 反复问"为什么怪物名不出"，界面必须能自己说清楚是哪种。
 */
function isNamePending(section: FormViewSection | null, index: number, value: string): boolean {
  const ref = (section?.columnRefs?.[index] ?? "").trim();
  const id = value.trim();
  if (ref === "" || id === "") return false;
  return pendingNameLookups.has(`${ref}|${id}`);
}

/**
 * 该格在本次搜索里要**标出来**的那段文字（没有命中就返回空串）。
 *
 * 只有"当前搜索范围里的列 + 当前搜索字段"才参与标记 —— 搜 ID 就只标编号里的命中，
 * 搜名称就只标名字里的命中（用户 2026-10-03：搜 28 时希望把 `281` 里的 `28` 标出来）。
 */
function cellHitQuery(row: FormViewRow, index: number): string {
  if (!searchActive.value || !searchColumns.value.includes(index)) return "";
  const query = searchQuery.value.trim().toLowerCase();
  if (query === "") return "";
  const target = (
    searchField.value === "name"
      ? rowName(mainSection.value, row, index)
      : cellCurrent(row, index)
  )
    .trim()
    .toLowerCase();
  if (searchExact.value) return target === query ? query : "";
  return target.includes(query) ? query : "";
}

/** 把文本按命中片段切成「前 / 命中 / 后」三段（大小写不敏感，取第一处）。 */
function splitHit(
  text: string,
  query: string
): { before: string; hit: string; after: string } {
  if (query === "" || text === "") return { before: text, hit: "", after: "" };
  const at = text.toLowerCase().indexOf(query);
  if (at < 0) return { before: text, hit: "", after: "" };
  return {
    before: text.slice(0, at),
    hit: text.slice(at, at + query.length),
    after: text.slice(at + query.length),
  };
}

/** 取草稿值的中文名；没有就问一次后端（问过就缓存，含"查不到"的空结果）。 */
function draftName(section: FormViewSection | null, index: number, value: string): string {
  const id = value.trim();
  const ref = (section?.columnRefs?.[index] ?? "").trim();
  if (id === "" || ref === "") return "";
  const key = `${ref}|${id}`;
  const cached = draftNames.value.get(key);
  if (cached !== undefined) return cached;
  void lookupDraftName(ref, id, key);
  return "";
}

/**
 * 向后端要名字。
 *
 * 用的是 `ResolveRefNames`（**表格同款解析器**）—— 早先这里借用「对象视图」的
 * ResolveObject，结果物品能出名字、怪物出不来（用户 2026-10-03 实测）。
 * `ref` 支持 `a|b` 多候选，由后端逐个试，前端不必自己循环。
 */
async function lookupDraftName(ref: string, id: string, key: string): Promise<void> {
  if (pendingNameLookups.has(key)) return;
  pendingNameLookups.add(key);
  try {
    const names = await ResolveRefNames(ref, [id]);
    const next = new Map(draftNames.value);
    next.set(key, (names?.[id] ?? "").trim());
    draftNames.value = next;
  } catch {
    // 解析失败就保持空（下次渲染会再试一次），不弹错打断编辑。
  } finally {
    pendingNameLookups.delete(key);
  }
}

/**
 * 一格要显示的名字：**草稿优先** —— 有草稿就解析草稿里的新 ID，
 * 没草稿才用投影里那份（后端已解析好的）名字。
 */
function rowName(section: FormViewSection | null, row: FormViewRow, index: number): string {
  const draft = draftValue(section, row.index, index);
  if (draft !== null) return draftName(section, index, draft);
  return row.cells[index]?.name ?? "";
}

// ---- 添加掉落（往 [independent drop] 段末尾追加一条） ----
//
// 用户 2026-10-03 要求：搜索后面加一颗「添加掉落」，点开是一张表单 —— 怪物ID 直接填；
// 掉落物品**二选一**（单一物品 / 掉落物列表 = 内联列表形式，物品+权重可增删）；
// 掉落率按百分比填（与现有格式一致）；「掉落方式」随选择自动定，不让人填。
// 写入位置：最后一个 `[/independent drop]` 之前（= 追加到该段末尾）。

const dropFormVisible = ref(false);
const dropAdding = ref(false);
const dropIsAPC = ref(false);
const dropMonsterId = ref("");
const dropUseList = ref(false);
const dropItemId = ref("");
const dropItems = ref<FormViewDropItem[]>([{ itemId: "", weight: "1000" }]);
const dropRates = ref<string[]>(["100", "100", "100", "100", "100"]);
const dropCounts = ref<string[]>(["1", "1", "1", "1", "1"]);
const dropLevelMin = ref("0");
const dropLevelMax = ref("0");
const dropJobLimit = ref("-1");

/** 取「列声明里含某个对象类型」的列下标（拿不到返回 -1）。 */
function columnIndexOfRef(section: FormViewSection | null, want: string): number {
  const refs = section?.columnRefs ?? [];
  for (let index = 0; index < refs.length; index += 1) {
    const candidates = (refs[index] ?? "")
      .split("|")
      .map((item) => item.trim());
    if (candidates.includes(want)) return index;
  }
  return -1;
}

/**
 * 表单里填的怪物ID / 物品ID 也实时解析中文名（复用同一个解析 + 缓存）。
 *
 * 都用**普通函数**而不是 computed：名字是异步取回的，computed 在首次求值时会
 * 把空结果缓存住（用户 2026-10-03 实测：物品ID 能出名字、怪物ID 不出）。
 */
function dropMonsterName(id: string): string {
  return draftName(mainSection.value, columnIndexOfRef(mainSection.value, "monster"), id);
}

function dropItemName(id: string): string {
  const section = mainSection.value;
  const refs = section?.columnRefs ?? [];
  for (let index = 0; index < refs.length; index += 1) {
    const candidates = (refs[index] ?? "").split("|").map((item) => item.trim());
    if (candidates.includes("equipment") || candidates.includes("stackable")) {
      return draftName(section, index, id);
    }
  }
  return "";
}

function addDropItemRow(): void {
  dropItems.value = [...dropItems.value, { itemId: "", weight: "1000" }];
}

function removeDropItemRow(index: number): void {
  if (dropItems.value.length <= 1) return;
  dropItems.value = dropItems.value.filter((_, position) => position !== index);
}

function resetDropForm(): void {
  dropIsAPC.value = false;
  dropMonsterId.value = "";
  dropUseList.value = false;
  dropItemId.value = "";
  dropItems.value = [{ itemId: "", weight: "1000" }];
  dropRates.value = ["100", "100", "100", "100", "100"];
  dropCounts.value = ["1", "1", "1", "1", "1"];
  dropLevelMin.value = "0";
  dropLevelMax.value = "0";
  dropJobLimit.value = "-1";
}

async function submitDrop(): Promise<void> {
  if (fileOpenInEditor()) {
    message.warning(
      "该文件正在编辑区打开：请先关掉那个标签页，再加（避免两处同时改同一份文本）"
    );
    return;
  }
  const rates = dropRates.value.map((value) => Number(value));
  if (rates.some((value) => !Number.isFinite(value) || value < 0 || value > 100)) {
    message.warning("掉落率请填 0 ~ 100 的百分比（如 20 表示 20%）");
    return;
  }
  const counts = dropCounts.value.map((value) => Number(value));
  if (counts.some((value) => !Number.isFinite(value) || value < 0)) {
    message.warning("个数必须是非负整数");
    return;
  }
  // **只排队**（点「保存改动」才真正写进归档内存）：与改一格同一套模型。
  pendingInserts.value = [
    ...pendingInserts.value,
    {
      kind: "drop",
      entry: {
        isApc: dropIsAPC.value,
        monsterId: dropMonsterId.value.trim(),
        useList: dropUseList.value,
        itemId: dropItemId.value.trim(),
        list: dropItems.value.map((item) => ({
          itemId: item.itemId.trim(),
          weight: item.weight.trim(),
        })),
        rates,
        counts,
        levelMin: Number(dropLevelMin.value) || 0,
        levelMax: Number(dropLevelMax.value) || 0,
        jobLimit: dropJobLimit.value.trim() || "-1",
      },
    },
  ];
  dropFormVisible.value = false;
  resetDropForm();
  message.success("已加入待提交（点上方「保存改动」才写进归档内存）");
}

// ---- 单元格编辑（双击改值 → 本地草稿 → 点「保存改动」才进归档） ----
//
// 模型（2026-10-03 用户指定，对齐装备文本编辑）：**先改前端、立刻显示；点「保存改动」
// 才写进归档内存**。所以这里不再"每改一格都去后端转一圈"—— 改的是草稿，显示走 cellText
// 里的草稿优先，提交只在 saveDrafts() 时发生一次（批量改也是先落草稿）。

type EditState = {
  /** 正在改的是哪个段（主表 = independent drop；查看器 = 某一处 [list]）。 */
  section: FormViewSection;
  row: number;
  column: number;
  label: string;
};

const editing = ref<EditState | null>(null);
/** 正在编辑的文本（单独一个 ref，模板里就不必对 editing 判空）。 */
const editText = ref("");
/** 已经写进**归档内存**（还没落盘）的格数。 */
const editedCount = ref(0);
/** 正在把草稿提交给后端（写归档内存）。 */
const saving = ref(false);
/** 正在把归档写进 PVF 文件（落盘，可能十几秒）。 */
const savingToPvf = ref(false);

/**
 * 一条草稿：**自带所在段**（段名 + 出现序号）。
 *
 * 之所以带上段：内联列表（`[list]`，本文件 862 次）也在同一张草稿簿里 ——
 * 主表改的是 `[independent drop]`，查看器改的是某一处 `[list]`，
 * 两者必须能区分开，保存时才能各自定位到正确的那一次出现。
 */
type DraftEdit = {
  section: string;
  occurrence: number;
  row: number;
  column: number;
  value: string;
};

/** 草稿键：段名#出现序号:行:列（同一格只留最后一次改动）。 */
function draftKey(
  section: string,
  occurrence: number,
  row: number,
  column: number
): string {
  return `${section.toLowerCase()}#${occurrence}:${row}:${column}`;
}

/** 本地草稿（未提交）：改了立刻显示，点「保存改动」才写进归档内存。 */
const pendingEdits = ref<Map<string, DraftEdit>>(new Map());
const pendingCount = computed(() => pendingEdits.value.size);

/** 按（段名, 出现序号）找投影里的段；主表找不到就看查看器里那份（内联列表）。 */
function sectionOf(name: string, occurrence: number): FormViewSection | null {
  const main = mainSection.value;
  if (main && main.section.toLowerCase() === name.toLowerCase() && main.occurrence === occurrence) {
    return main;
  }
  const viewer = viewerSection.value;
  if (
    viewer &&
    viewer.section.toLowerCase() === name.toLowerCase() &&
    viewer.occurrence === occurrence
  ) {
    return viewer;
  }
  return null;
}

function draftValue(
  section: FormViewSection | null,
  row: number,
  index: number
): string | null {
  if (!section) return null;
  const draft = pendingEdits.value.get(
    draftKey(section.section, section.occurrence, row, index)
  );
  return draft ? draft.value : null;
}

/**
 * 把若干格改动放进草稿（**不碰归档**），返回真正发生变化的格数。
 *
 * 值改回归档原样就从草稿里删掉 —— 不制造"改了又改回来"的假未保存标记。
 */
/** stageEdits 的入参：段可以直接给对象（主表/查看器各持有自己那份），也可以给「段名+出现序号」。 */
type StageEditInput = {
  section: FormViewSection | string;
  occurrence?: number;
  row: number;
  column: number;
  value: string;
};

function stageEdits(items: StageEditInput[]): number {
  if (items.length === 0) return 0;
  const next = new Map(pendingEdits.value);
  let staged = 0;
  for (const item of items) {
    const section =
      typeof item.section === "string"
        ? sectionOf(item.section, item.occurrence ?? 0)
        : item.section;
    if (!section) continue;
    const key = draftKey(section.section, section.occurrence, item.row, item.column);
    const original = (section.rows[item.row]?.cells[item.column]?.value ?? "").trim();
    const value = item.value.trim();
    if (value === original) {
      next.delete(key);
      continue;
    }
    next.set(key, {
      section: section.section,
      occurrence: section.occurrence,
      row: item.row,
      column: item.column,
      value,
    });
    staged += 1;
  }
  pendingEdits.value = next;
  return staged;
}

/** 放弃全部草稿（不动归档），含排队中的新增与删除。 */
function discardDrafts(): void {
  if (
    pendingEdits.value.size === 0 &&
    pendingInserts.value.length === 0 &&
    pendingDeletes.value.length === 0
  ) {
    return;
  }
  pendingEdits.value = new Map();
  pendingInserts.value = [];
  pendingDeletes.value = [];
  selectedRow.value = null;
  message.info("已放弃未保存的改动");
}

/** 把草稿 + 排队中的新增/删除一起提交给后端（写进归档内存，**不落盘**）。 */
async function saveDrafts(): Promise<void> {
  // 草稿**自带段信息**：主表的改动与各处内联列表（[list]）的改动可以混在一起一次提交。
  const drafts = [...pendingEdits.value.values()];
  const queued = [...pendingInserts.value];
  const deletes = [...pendingDeletes.value];
  if (drafts.length === 0 && queued.length === 0 && deletes.length === 0) return;
  if (fileOpenInEditor()) {
    message.warning(
      "该文件正在编辑区打开：请先关掉那个标签页，再保存（避免两处同时改同一份文本）"
    );
    return;
  }
  // 按行、列排序提交：服务端逐格定位 + 逐格校验，顺序稳定、行为可预期。
  drafts.sort((left, right) => left.row - right.row || left.column - right.column);
  saving.value = true;
  try {
    // ① 先执行排队中的**删除**：行号**从大到小**（删一行会让后面所有行号前移，倒着删才不错位）。
    for (const rowIndex of [...deletes].sort((left, right) => right - left)) {
      await DeleteIndependentDrop(formView.filePath.trim(), rowIndex);
    }
    pendingDeletes.value = [];
    // ② 再执行排队中的新增（插入不是"改一格"，各走自己的通道）。
    for (const item of queued) {
      if (item.kind === "drop") {
        await AddIndependentDrop(formView.filePath.trim(), item.entry);
      } else {
        await AddDropCandidate(
          formView.filePath.trim(),
          item.section,
          item.occurrence,
          item.item
        );
      }
    }
    pendingInserts.value = [];
    await formView.applyEdits(
      drafts.map((draft) => ({
        section: draft.section,
        occurrence: draft.occurrence,
        row: draft.row,
        column: draft.column,
        value: draft.value,
      }))
    );
    // 只有新增、没有改格时，上面的 applyEdits 会空转（不会回传新投影），这里补一次重新解析。
    if (drafts.length === 0) await formView.project();
    pendingEdits.value = new Map();
    editedCount.value += drafts.length;
    const parts: string[] = [];
    if (drafts.length > 0) parts.push(`${drafts.length} 格`);
    if (queued.length > 0) parts.push(`新增 ${queued.length} 条`);
    if (deletes.length > 0) parts.push(`删除 ${deletes.length} 条`);
    // 用户 2026-10-03 要求：**可视化的「保存改动」= 直接保存这个掉落文件**。
    // 只写归档内存的话，主窗口那个标签会一直挂着"还有改动没保存"的绿点，
    // 用户还得再点一次「保存 PVF」—— 那正是他要消掉的"多一步"。
    // 所以这里接着走主工具条同一条落盘通道（封包进度 / 取消都沿用）。
    const persisted = await persistToSource();

    // 查看器里那份（某处 [list]）不在主投影里，得单独重取一次，否则它还停在被改之前的旧值。
    if (viewerVisible.value && viewerSection.value) {
      const target = viewerSection.value;
      try {
        const fresh = await formView.loadLinkedSection(target.section, target.occurrence);
        if (fresh) viewerSection.value = fresh;
      } catch {
        // 取不到就保持原样（不覆盖、不报错：主表那边已经刷新成功）。
      }
    }
    const summary = parts.join(" + ");
    if (persisted) {
      message.success(`已保存 ${summary} 并写入 PVF 文件`);
    } else {
      message.warning(
        `已保存 ${summary} 到归档内存，但写入 PVF 文件没成功：可回主窗口点「保存 PVF」重试`
      );
    }
  } catch (issue: any) {
    message.error(String(issue?.message ?? issue));
  } finally {
    saving.value = false;
  }
}

/**
 * 把归档写进 PVF 文件（与主工具条「保存 PVF」同一条通道：`editor.save()`）。
 *
 * 独立窗口里调用同样有效 —— 归档在后端是同一份，本窗口的 `editor` store 即使
 * 一个标签都没有，`EditorService.Save()` 照样把当前归档整体落盘。
 * 返回是否成功（失败时提示里带上原因，不吞错）。
 */
async function persistToSource(): Promise<boolean> {
  savingToPvf.value = true;
  try {
    await editor.save();
    return true;
  } catch (issue: any) {
    const text = String(issue?.message ?? issue);
    if (!/cancel|已取消/i.test(text)) {
      message.error(`写入 PVF 文件失败：${text}`);
    }
    return false;
  } finally {
    savingToPvf.value = false;
  }
}

function normalizePath(value: string): string {
  return (value ?? "").replace(/\\/g, "/").trim().toLowerCase();
}

/**
 * 该文件是否正在编辑区打开。
 *
 * 开着就不让在这里改：编辑区标签页自己持有一份文本副本，两处同时改同一份文件
 * 会出现"这边改完、那边保存时把它覆盖回去"。宁可拦住，也不制造这种事故。
 */
function fileOpenInEditor(): boolean {
  const want = normalizePath(formView.filePath);
  if (want === "") return false;
  return editor.tabs.some((tab) => normalizePath(tab.path ?? "") === want);
}

function startEdit(row: FormViewRow, index: number, section: FormViewSection): void {
  const cell = row.cells[index];
  if (!cell) return;
  if (fileOpenInEditor()) {
    message.warning(
      "该文件正在编辑区打开：请先关掉那个标签页，再回来改（避免两处同时改同一份文本）"
    );
    return;
  }
  // 续改时取草稿里的值：别把用户刚打的字又顶回归档里的旧值。
  editText.value = cellCurrent(row, index);
  editing.value = {
    section,
    row: row.index,
    column: index,
    label: section.columns[index] ?? "",
  };
  void nextTick();
}

function cancelEdit(): void {
  editing.value = null;
}

/**
 * 回车 / 点到别处：落到**草稿**并**立刻显示**（不碰归档、不重新投影）。
 * 所以不会再出现"先显示旧值、几秒后才变"。
 */
function commitEdit(): void {
  const state = editing.value;
  if (!state) return;
  stageEdits([
    {
      section: state.section,
      row: state.row,
      column: state.column,
      value: editText.value,
    },
  ]);
  editing.value = null;
}

function onCellDblClick(row: FormViewRow, index: number, section: FormViewSection): void {
  if (isLinkCell(row, index)) {
    void openLink(row);
    return;
  }
  startEdit(row, index, section);
}

// ---- 列宽左右拉伸 ----

type DragState = {
  key: string;
  index: number;
  startX: number;
  startWidth: number;
  /** 按下瞬间量到的各列宽度：**首次真正移动时才落盘**。 */
  widths: number[];
  seeded: boolean;
};
let dragState: DragState | null = null;

function columnStyle(section: FormViewSection, index: number) {
  const width = columnWidths.value[section.section]?.[index] ?? 0;
  return width > 0 ? { width: `${width}px` } : undefined;
}

/**
 * 单元格**内容**的行内宽度上限（拖动列宽后才给）。
 *
 * 表格保持 `table-layout: auto`（没拖过就是"内容多宽就多宽"，中间不可能留空白）；
 * 一旦给了这个上限，内容被约束住，列也就跟着变窄 —— **向左拖才真的动得动**，
 * 而且不会像"切固定布局"那样把已撑开的宽度冻住。
 */
function cellStyle(section: FormViewSection, index: number): Record<string, string> | undefined {
  const width = columnWidths.value[section.section]?.[index] ?? 0;
  return width > 0 ? { maxWidth: `${Math.max(24, width - 14)}px` } : undefined;
}

function onResizeStart(
  event: PointerEvent,
  section: FormViewSection,
  index: number
): void {
  const handle = event.currentTarget as HTMLElement | null;
  const table = handle?.closest("table") as HTMLTableElement | null;
  if (!table) return;
  // 先量出当前所有列的真实宽度再接管，避免"一拖就跳"。
  const headers = Array.from(table.querySelectorAll("thead th"));
  const widths = section.columns.map((_, columnIndex) => {
    const element = headers[columnIndex + 1] as HTMLElement | undefined;
    return element ? Math.round(element.getBoundingClientRect().width) : 0;
  });
  const current = headers[index + 1] as HTMLElement | undefined;
  dragState = {
    key: section.section,
    index,
    startX: event.clientX,
    startWidth: current ? Math.round(current.getBoundingClientRect().width) : 80,
    widths,
    seeded: false,
  };
  // 刻意**不**在这里写入 widths：单纯点一下手柄（没拖动）不该改动任何列宽，
  // 否则手一抖就把当时的布局冻住（2026-10-03 用户反复遇到"空白消不掉"）。真正
  // 写入发生在 onResizeMove 的第一次移动。
  window.addEventListener("pointermove", onResizeMove);
  window.addEventListener("pointerup", onResizeEnd);
}

function onResizeMove(event: PointerEvent): void {
  const state = dragState;
  if (!state) return;
  if (!state.seeded) {
    columnWidths.value = { ...columnWidths.value, [state.key]: [...state.widths] };
    state.seeded = true;
  }
  // ★ 只能**缩小**，不能放大（上限 = 按下瞬间量到的"内容自然宽度"）。
  //
  // 2026-10-03 定案：那个"列后面一大片空白"反复消不掉的根因，就是**拖动把列拖宽过**
  // 留下的固定像素宽度（宽度是会话内记忆的，只有「解析」或「重置列宽」才清）。
  // 用户的真实需求是"狭窄一点、能向左拉"，从来不需要把列拖得比内容还宽 ——
  // 而从结构上禁止放大，就**不可能再产生空白**。
  const ceiling = Math.max(48, state.widths[state.index] ?? state.startWidth);
  const next = Math.min(
    ceiling,
    Math.max(48, Math.round(state.startWidth + (event.clientX - state.startX)))
  );
  const widths = [...(columnWidths.value[state.key] ?? [])];
  widths[state.index] = next;
  columnWidths.value = { ...columnWidths.value, [state.key]: widths };
}

function onResizeEnd(): void {
  stopResize();
}

function stopResize(): void {
  dragState = null;
  window.removeEventListener("pointermove", onResizeMove);
  window.removeEventListener("pointerup", onResizeEnd);
}

function resetColumnWidths(): void {
  columnWidths.value = {};
}
</script>

<template>
  <div class="fv-root">
    <!-- ① 标题栏（固定） -->
    <header class="fv-head">
      <div class="fv-head-title">
        <span class="fv-crumbs">可视化编辑区</span>
        <span class="fv-crumb-sep">›</span>
        <span class="fv-title">{{ viewTitle }}</span>
        <NTag
          size="small"
          :bordered="false"
          type="warning"
          title="主表可双击改值；改完点「保存改动」就写进归档内存并保存到 PVF 文件。关联的内联列表在主表里只读（在它自己的查看器里可改）。"
        >
          可编辑
        </NTag>
      </div>
      <div class="fv-head-actions">
        <NButton size="tiny" quaternary @click="resetColumnWidths">重置列宽</NButton>
      </div>
    </header>

    <!-- ② 参数区（固定） -->
    <section class="fv-form">
      <div class="fv-form-row">
        <span class="fv-label">文件族</span>
        <NSelect
          v-model:value="formView.formatId"
          :options="formView.formatOptions"
          :loading="formView.formatsLoading"
          size="small"
          placeholder="选择文件族"
          class="fv-select"
        />
        <span class="fv-label">文件</span>
        <NInput
          v-model:value="formView.filePath"
          size="small"
          placeholder="归档内路径，如 etc/independent_drop.etc"
          @keyup.enter="formView.project()"
        />
        <NButton
          size="small"
          type="primary"
          :disabled="!formView.canProject"
          :loading="formView.projecting"
          @click="formView.project()"
        >
          解析
        </NButton>
        <NButton
          size="small"
          quaternary
          :loading="formView.formatsLoading"
          @click="formView.reloadRules()"
        >
          重新读规则
        </NButton>
      </div>
      <div class="fv-form-sub">
        <span class="fv-rule" :title="formView.rulePath">规则：{{ formView.rulePath }}</span>
      </div>
      <div
        v-if="formView.currentFormat?.notes"
        class="fv-notes"
        :title="formView.currentFormat?.notes"
      >
        {{ notesBrief }}
      </div>
    </section>

    <!-- ③ 错误（固定） -->
    <div v-if="formView.formatsError" class="fv-error">{{ formView.formatsError }}</div>
    <div v-if="formView.error" class="fv-error">{{ formView.error }}</div>

    <!-- ④ 唯一滚动区 -->
    <div class="fv-body">
      <div v-if="!formView.ready" class="fv-hint">
        <NEmpty size="small" description="先打开一个 PVF 归档，再解析文件" />
      </div>

      <NSpin v-else :show="formView.projecting">
        <template v-if="formView.projection">
          <div class="fv-stats">
            <span class="fv-stats-strong">{{ formView.projection.formatLabel }}</span>
            <span class="fv-sep">·</span>
            <span>{{ formView.projection.file }}</span>
            <span class="fv-sep">·</span>
            <span>{{ formView.projection.tokenCount }} 个 token</span>
            <span class="fv-sep">·</span>
            <span>{{ formView.projection.sections.length }} 个段</span>
          </div>

          <ul v-if="formView.projection.warnings.length" class="fv-warnings">
            <li v-for="(warning, index) in formView.projection.warnings" :key="index">
              {{ warning }}
            </li>
          </ul>

          <template v-if="mainSection">
            <div class="fv-section-head">
              <span class="fv-section-title">{{ sectionTitle(mainSection) }}</span>
              <span class="fv-section-meta">
                {{ mainSection.rows.length }} 行 × {{ mainSection.columns.length }} 列 ·
                拖表头右边缘调列宽 · 双击格改值 · 带 🔗 的格双击查看关联列表（关联列表只读）
              </span>
            </div>
            <ul v-if="mainSection.warnings?.length" class="fv-warnings">
              <li v-for="(warning, index) in mainSection.warnings" :key="index">
                {{ warning }}
              </li>
            </ul>

            <!-- 搜索 + 批量改（都作用在下方这张主表上） -->
            <div class="fv-tools">
              <span class="fv-tools-label">搜索</span>
              <NSelect
                v-model:value="searchRef"
                :options="searchScopes.map((scope) => ({ label: scope.label, value: scope.ref }))"
                size="small"
                clearable
                placeholder="选列：怪物 / 掉落物品…"
                class="fv-tools-scope"
              />
              <NSelect
                v-model:value="searchField"
                :options="[
                  { label: 'ID', value: 'id' },
                  { label: '名称', value: 'name' },
                ]"
                size="small"
                class="fv-tools-field"
              />
              <NInput
                v-model:value="searchQuery"
                size="small"
                clearable
                :placeholder="
                  searchField === 'name'
                    ? '输入中文名（按名称搜）'
                    : '输入编号（按 ID 搜）'
                "
                class="fv-tools-query"
              />
              <NButton
                size="tiny"
                :type="searchExact ? 'primary' : 'default'"
                :ghost="!searchExact"
                :title="
                  searchExact
                    ? '精确匹配已开启：必须整串相等（如 ID 搜 28 只出编号为 28 的行）'
                    : '启用精确匹配（与主工具条那颗按钮同一套用法）'
                "
                @click="searchExact = !searchExact"
              >
                ◎ 精确
              </NButton>
              <span v-if="searchActive" class="fv-tools-hit">
                命中 {{ searchHits.size }} / {{ mainSection.rows.length }} 行
              </span>
              <NButton v-if="searchActive" size="tiny" quaternary @click="clearSearch">
                清除
              </NButton>
              <NButton size="tiny" type="primary" @click="dropFormVisible = true">
                添加掉落
              </NButton>
              <NButton
                size="tiny"
                type="error"
                ghost
                :disabled="selectedRow === null"
                title="先在表里点一行选中，再点这里（会把这整条掉落删掉，含它自带的候选列表）"
                @click="queueDeleteSelected"
              >
                删除选中
              </NButton>
              <span class="fv-tools-gap" />
              <span
                v-if="pendingCount + pendingInsertCount + pendingDeleteCount > 0"
                class="fv-tools-dirty"
              >
                未保存 {{ pendingCount }} 格{{
                  pendingInsertCount > 0 ? ` + 新增 ${pendingInsertCount} 条` : ""
                }}{{ pendingDeleteCount > 0 ? ` + 删除 ${pendingDeleteCount} 条` : "" }}
              </span>
              <NButton
                size="tiny"
                quaternary
                :disabled="pendingCount + pendingInsertCount + pendingDeleteCount === 0"
                @click="discardDrafts"
              >
                放弃改动
              </NButton>
              <NButton
                size="tiny"
                type="primary"
                :disabled="
                  pendingCount + pendingInsertCount + pendingDeleteCount === 0 || savingToPvf
                "
                :loading="saving || savingToPvf"
                title="写进归档内存，并立即保存到 PVF 文件（不用再回主窗口点「保存 PVF」）"
                @click="saveDrafts"
              >
                {{ savingToPvf ? "正在写入 PVF…" : "保存改动" }}
              </NButton>
              <!-- 「批量改」按钮已按用户 2026-10-03 要求撤下（"现在那个有问题不好用，后续我再改"）：
                   面板与脚本都留着（batchVisible 控制，不会显示），下次接回来只加回这一颗按钮即可。 -->
            </div>

            <div v-if="batchVisible" class="fv-batch">
              <div class="fv-batch-row">
                <span class="fv-tools-label">列</span>
                <NSelect
                  v-model:value="batchColumn"
                  :options="batchColumns"
                  size="small"
                  placeholder="要改哪一列"
                  class="fv-batch-col"
                />
                <span class="fv-tools-label">运算</span>
                <NSelect
                  v-model:value="batchOperator"
                  :options="[
                    { label: '= 设为', value: '=' },
                    { label: '+ 加', value: '+' },
                    { label: '- 减', value: '-' },
                    { label: '× 乘', value: '×' },
                    { label: '÷ 除', value: '÷' },
                  ]"
                  size="small"
                  class="fv-batch-op"
                />
                <NInput
                  v-model:value="batchOperand"
                  size="small"
                  placeholder="数值"
                  class="fv-batch-num"
                />
              </div>
              <div class="fv-batch-row">
                <NCheckbox v-model:checked="batchOnlyHits" :disabled="!searchActive">
                  只改搜索命中的行
                </NCheckbox>
                <span class="fv-batch-count">
                  将作用于 <b>{{ batchTargets.length }}</b> 行{{
                    searchActive ? `（命中 ${searchHits.size} 行）` : "（全表）"
                  }}
                </span>
                <span class="fv-tools-gap" />
                <NButton
                  size="small"
                  type="primary"
                  :loading="batchRunning"
                  :disabled="batchColumn < 0"
                  @click="runBatch"
                >
                  执行
                </NButton>
              </div>
              <div class="fv-batch-hint">
                先存本地草稿、立刻显示；点「保存改动」才写进归档内存，非数值格自动跳过。
              </div>
            </div>

            <table class="fv-table" :style="{ minWidth: 0 }">
              <colgroup>
                <col class="fv-col-index" />
                <col
                  v-for="(_, index) in mainSection.columns"
                  :key="index"
                  :style="columnStyle(mainSection, index)"
                />
              </colgroup>
              <thead>
                <tr>
                  <th class="fv-th-index">#</th>
                  <th
                    v-for="(column, index) in mainSection.columns"
                    :key="index"
                    :style="columnStyle(mainSection, index)"
                    :title="column"
                  >
                    <span class="fv-th-text">{{ column }}</span>
                    <span
                      class="fv-th-grip"
                      title="拖动调整列宽"
                      @pointerdown.stop.prevent="onResizeStart($event, mainSection, index)"
                    />
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="row in pagedRows"
                  :key="row.index"
                  :class="{
                    'fv-row-incomplete': !row.complete,
                    'fv-row-selected': selectedRow === row.index,
                    'fv-row-deleting': pendingDeletes.includes(row.index),
                  }"
                  @click="toggleSelectRow(row)"
                >
                  <td class="fv-td-index">{{ row.index + 1 }}</td>
                  <td
                    v-for="(_, index) in mainSection.columns"
                    :key="index"
                    :class="{
                      'fv-num': isNumeric(cellText(row, index).raw),
                      'fv-link-cell': isLinkCell(row, index),
                      'fv-cell-dirty': isDirtyCell(row, index),
                      'fv-cell-editing':
                        editing !== null &&
                        editing.row === row.index &&
                        editing.column === index,
                    }"
                    :title="cellTitle(row, index)"
                    @dblclick="onCellDblClick(row, index, mainSection)"
                  >
                    <NInput
                      v-if="
                        editing !== null &&
                        editing.row === row.index &&
                        editing.column === index
                      "
                      v-model:value="editText"
                      size="tiny"
                      autofocus
                      class="fv-edit-input"
                      @keyup.enter="commitEdit()"
                      @keyup.esc="cancelEdit()"
                      @blur="commitEdit()"
                    />
                    <span
                      v-else
                      class="fv-cell"
                      :style="cellStyle(mainSection, index)"
                    >
                      <template v-if="isLinkCell(row, index)">
                        <span class="fv-link-pill">
                          <span class="fv-link-text">{{ linkCellText(row, index) }}</span>
                          <span class="fv-link-badge">🔗 查看</span>
                        </span>
                      </template>
                      <template v-else>
                        <!-- 搜索命中的那一段标出来（搜 ID 标编号里那段、搜名称标名字里那段） -->
                        <span v-if="rowName(mainSection, row, index)" class="fv-name">
                          {{ splitHit(rowName(mainSection, row, index), cellHitQuery(row, index)).before
                          }}<span v-if="cellHitQuery(row, index)" class="fv-hit">{{
                            splitHit(rowName(mainSection, row, index), cellHitQuery(row, index)).hit
                          }}</span>{{
                            splitHit(rowName(mainSection, row, index), cellHitQuery(row, index)).after
                          }}
                        </span>
                        <span v-else>
                          {{ splitHit(cellText(row, index).text, cellHitQuery(row, index)).before
                          }}<span v-if="cellHitQuery(row, index)" class="fv-hit">{{
                            splitHit(cellText(row, index).text, cellHitQuery(row, index)).hit
                          }}</span>{{
                            splitHit(cellText(row, index).text, cellHitQuery(row, index)).after
                          }}
                        </span>
                        <span v-if="rowName(mainSection, row, index)" class="fv-id">
                          {{ splitHit(cellText(row, index).text, cellHitQuery(row, index)).before
                          }}<span v-if="cellHitQuery(row, index)" class="fv-hit">{{
                            splitHit(cellText(row, index).text, cellHitQuery(row, index)).hit
                          }}</span>{{
                            splitHit(cellText(row, index).text, cellHitQuery(row, index)).after
                          }}
                        </span>
                      </template>
                    </span>
                  </td>
                </tr>
              </tbody>
            </table>
          </template>

          <div v-if="otherSections.length" class="fv-others">
            <div class="fv-section-head">
              <span class="fv-section-title">其它段</span>
              <span class="fv-section-meta">{{ otherSections.length }} 块</span>
            </div>
            <details v-for="(section, index) in otherSections" :key="index" class="fv-block">
              <summary>
                <span>{{ sectionTitle(section) }}</span>
                <span class="fv-section-meta">{{ section.rows.length }} 行</span>
              </summary>
              <table class="fv-table fv-table--compact" :style="{ minWidth: 0 }">
                <colgroup>
                  <col class="fv-col-index" />
                  <col
                    v-for="(_, columnIndex) in section.columns"
                    :key="columnIndex"
                    :style="columnStyle(section, columnIndex)"
                  />
                </colgroup>
                <thead>
                  <tr>
                    <th class="fv-th-index">#</th>
                    <th
                      v-for="(column, columnIndex) in section.columns"
                      :key="columnIndex"
                      :style="columnStyle(section, columnIndex)"
                      :title="column"
                    >
                      <span class="fv-th-text">{{ column }}</span>
                      <span
                        class="fv-th-grip"
                        title="拖动调整列宽"
                        @pointerdown.stop.prevent="
                          onResizeStart($event, section, columnIndex)
                        "
                      />
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="row in section.rows.slice(0, inlineRowLimit)" :key="row.index">
                    <td class="fv-td-index">{{ row.index + 1 }}</td>
                    <td
                      v-for="(_, columnIndex) in section.columns"
                      :key="columnIndex"
                      :class="{ 'fv-num': isNumeric(cellText(row, columnIndex).raw) }"
                      :title="'原值: ' + cellText(row, columnIndex).raw"
                    >
                      {{ cellText(row, columnIndex).text }}
                    </td>
                  </tr>
                </tbody>
              </table>
              <div v-if="section.rows.length > inlineRowLimit" class="fv-section-meta">
                只显示前 {{ inlineRowLimit }} 行（共 {{ section.rows.length }} 行）
              </div>
            </details>
          </div>
        </template>

        <div v-else-if="!formView.projecting" class="fv-hint">
          <NEmpty size="small" description="选好文件族与路径后点「解析」" />
        </div>
      </NSpin>
    </div>

    <!-- ⑤ 页脚（固定） -->
    <footer v-if="formView.projection && mainSection" class="fv-foot">
      <span class="fv-foot-info">
        共 {{ mainSection.rows.length }} 行 · 每页 {{ pageSize }} 行
        <template v-if="formView.projection?.linkedTargets">
          · 已关联 {{ formView.projection?.linkedTargets }} 段（双击带 🔗 的格查看）
        </template>
      </span>
      <div class="fv-pager" v-if="pageCount > 1">
        <NButton size="tiny" :disabled="page <= 1" @click="page -= 1">上一页</NButton>
        <span class="fv-pager-text">{{ page }} / {{ pageCount }}</span>
        <NButton size="tiny" :disabled="page >= pageCount" @click="page += 1">下一页</NButton>
      </div>
      <span class="fv-foot-hint">
        <template v-if="editedCount > 0">
          本次已保存 {{ editedCount }} 格（含写入 PVF 文件）
        </template>
        <template v-else>
          窗口可左右拉伸 · 双击格子改值（先存草稿，点「保存改动」写进归档并保存到 PVF 文件）
        </template>
      </span>
    </footer>

    <!-- ⑦ 添加掉落（追加到 [independent drop] 段末尾） -->
    <NModal
      v-model:show="dropFormVisible"
      preset="card"
      title="添加掉落（追加到 [independent drop] 段末尾）"
      class="fv-drop-modal"
      :bordered="false"
      size="small"
    >
      <div class="fv-drop">
        <div class="fv-drop-row">
          <span class="fv-drop-label">类型</span>
          <NSelect
            v-model:value="dropIsAPC"
            :options="[
              { label: '怪物', value: false },
              { label: 'APC', value: true },
            ]"
            size="small"
            class="fv-drop-small"
          />
          <span class="fv-drop-label">怪物/APC ID</span>
          <NInput
            v-model:value="dropMonsterId"
            size="small"
            placeholder="必填，如 20"
            class="fv-drop-id"
          />
          <span v-if="dropMonsterName(dropMonsterId)" class="fv-drop-name">
            {{ dropMonsterName(dropMonsterId) }}
          </span>
          <span
            v-else-if="
              dropMonsterId.trim() !== '' &&
              !isNamePending(
                mainSection,
                columnIndexOfRef(mainSection, 'monster'),
                dropMonsterId
              )
            "
            class="fv-drop-hint"
          >
            （查不到该编号）
          </span>
        </div>

        <div class="fv-drop-row">
          <span class="fv-drop-label">掉落物品</span>
          <NSelect
            v-model:value="dropUseList"
            :options="[
              { label: '单一物品', value: false },
              { label: '掉落物列表（内联）', value: true },
            ]"
            size="small"
            class="fv-drop-way"
          />
          <template v-if="!dropUseList">
            <NInput
              v-model:value="dropItemId"
              size="small"
              placeholder="物品ID，如 3015"
              class="fv-drop-id"
            />
            <span v-if="dropItemName(dropItemId)" class="fv-drop-name">
              {{ dropItemName(dropItemId) }}
            </span>
          </template>
          <span v-else class="fv-drop-hint">
            掉落方式将写为「内联列表(1)」；候选在下方维护
          </span>
        </div>

        <div v-if="dropUseList" class="fv-drop-candidates">
          <div v-for="(item, index) in dropItems" :key="index" class="fv-drop-row">
            <span class="fv-drop-label">候选 {{ index + 1 }}</span>
            <NInput
              v-model:value="item.itemId"
              size="small"
              placeholder="物品ID"
              class="fv-drop-id"
            />
            <span v-if="dropItemName(item.itemId)" class="fv-drop-name">
              {{ dropItemName(item.itemId) }}
            </span>
            <NInput
              v-model:value="item.weight"
              size="small"
              placeholder="权重"
              class="fv-drop-weight"
            />
            <NButton
              size="tiny"
              quaternary
              :disabled="dropItems.length <= 1"
              @click="removeDropItemRow(index)"
            >
              删除
            </NButton>
          </div>
          <NButton size="tiny" @click="addDropItemRow">+ 加一条候选</NButton>
        </div>

        <div class="fv-drop-row">
          <span class="fv-drop-label">掉落率(%)</span>
          <NInput
            v-for="(_, index) in dropRates"
            :key="`rate-${index}`"
            v-model:value="dropRates[index]"
            size="small"
            class="fv-drop-num"
          />
          <span class="fv-drop-hint">依次为难度 1~5（如 20 = 20%）</span>
        </div>
        <div class="fv-drop-row">
          <span class="fv-drop-label">个数</span>
          <NInput
            v-for="(_, index) in dropCounts"
            :key="`count-${index}`"
            v-model:value="dropCounts[index]"
            size="small"
            class="fv-drop-num"
          />
          <span class="fv-drop-hint">依次为难度 1~5</span>
        </div>
        <div class="fv-drop-row">
          <span class="fv-drop-label">等级下限</span>
          <NInput v-model:value="dropLevelMin" size="small" class="fv-drop-num" />
          <span class="fv-drop-label">等级上限</span>
          <NInput v-model:value="dropLevelMax" size="small" class="fv-drop-num" />
          <span class="fv-drop-label">职业限制</span>
          <NInput v-model:value="dropJobLimit" size="small" class="fv-drop-num" />
          <span class="fv-drop-hint">-1 = 不限</span>
        </div>

        <div class="fv-drop-hint">
          写入位置：最后一个 [/independent drop] 之前（追加到该段末尾）。点「保存改动」
          即写进归档内存并保存到 PVF 文件。
        </div>
        <div class="fv-drop-actions">
          <NButton size="small" @click="dropFormVisible = false">取消</NButton>
          <NButton size="small" type="primary" :loading="dropAdding" @click="submitDrop">
            添加
          </NButton>
        </div>
      </div>
    </NModal>

    <!-- ⑥ 关联段查看器（只读） -->
    <NModal
      v-model:show="viewerVisible"
      preset="card"
      :title="viewerTitle"
      class="fv-viewer-modal"
      :bordered="false"
      size="small"
    >
      <NSpin :show="viewerLoading">
        <div v-if="viewerError" class="fv-error">{{ viewerError }}</div>
        <div v-else-if="viewerSection" class="fv-viewer">
          <div class="fv-viewer-meta">
            段 [{{ viewerSection.section }}] · 第 {{ viewerSection.occurrence }} 处 ·
            {{ viewerSection.rows.length }} 行 × {{ viewerSection.columns.length }} 列
          </div>
          <div class="fv-viewer-table">
            <table class="fv-table">
              <thead>
                <tr>
                  <th class="fv-th-index">#</th>
                  <th v-for="(column, index) in viewerSection.columns" :key="index">
                    {{ column }}
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in viewerSection.rows" :key="row.index">
                  <td class="fv-td-index">{{ row.index + 1 }}</td>
                  <td
                    v-for="(_, index) in viewerSection.columns"
                    :key="index"
                    :class="{
                      'fv-num': isNumeric(viewerCellText(row, index).raw),
                      'fv-cell-dirty': viewerCellDirty(row, index),
                      'fv-cell-editing':
                        viewerEditing !== null &&
                        viewerEditing.row === row.index &&
                        viewerEditing.column === index,
                    }"
                    title="双击改值（物品编号 / 权重）—— 改的是本地草稿，点上方「保存改动」写进归档并保存到 PVF 文件"
                    @dblclick="startViewerEdit(row, index)"
                  >
                    <NInput
                      v-if="
                        viewerEditing !== null &&
                        viewerEditing.row === row.index &&
                        viewerEditing.column === index
                      "
                      v-model:value="viewerEditText"
                      size="tiny"
                      autofocus
                      class="fv-edit-input"
                      @keyup.enter="commitViewerEdit()"
                      @keyup.esc="cancelViewerEdit()"
                      @blur="commitViewerEdit()"
                    />
                    <span v-else class="fv-cell">
                      <span v-if="rowName(viewerSection, row, index)" class="fv-name">
                        {{ rowName(viewerSection, row, index) }}
                      </span>
                      <span v-else>{{ viewerCellText(row, index).text }}</span>
                      <span v-if="rowName(viewerSection, row, index)" class="fv-id">
                        {{ viewerCellText(row, index).text }}
                      </span>
                    </span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="fv-viewer-hint">
            双击格子可改（物品编号 / 权重）—— 改的是本地草稿，点上方「保存改动」才写进归档内存。
          </div>
          <div v-if="pendingCandidates(viewerSection).length" class="fv-viewer-hint">
            待提交候选（还没写进归档）：
            <span
              v-for="(item, index) in pendingCandidates(viewerSection)"
              :key="index"
              class="fv-drop-name"
            >
              {{ item.itemId }} / {{ item.weight }}&nbsp;&nbsp;
            </span>
          </div>
          <div class="fv-drop-row">
            <NButton size="tiny" type="primary" ghost @click="startViewerAdd">
              添加候选
            </NButton>
            <template v-if="viewerAddVisible">
              <span class="fv-drop-label">物品ID</span>
              <NInput
                v-model:value="viewerAddItemId"
                size="small"
                placeholder="如 14400"
                class="fv-drop-id"
              />
              <span v-if="dropItemName(viewerAddItemId)" class="fv-drop-name">
                {{ dropItemName(viewerAddItemId) }}
              </span>
              <span class="fv-drop-label">权重</span>
              <NInput
                v-model:value="viewerAddWeight"
                size="small"
                placeholder="如 1000"
                class="fv-drop-weight"
              />
              <NButton size="tiny" type="primary" :loading="viewerAdding" @click="submitViewerAdd">
                追加
              </NButton>
              <NButton size="tiny" quaternary @click="viewerAddVisible = false">取消</NButton>
            </template>
          </div>
        </div>
        <NEmpty v-else description="正在取这一段的投影…" />
      </NSpin>
    </NModal>
  </div>
</template>

<style scoped>
/* 布局主干：flex 竖排，只有 .fv-body 滚动。
   每层都要 min-height:0 —— flex 子项默认 min-height:auto 会顶开高度、吃掉滚动条。 */
.fv-root {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
  color: var(--pvf-text-primary);
  background: var(--pvf-surface-panel);
}

/* ① 标题栏 */
.fv-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex: 0 0 auto;
  padding: 8px 10px 6px;
}

.fv-head-title {
  display: flex;
  align-items: center;
  gap: 6px;
}

.fv-crumbs {
  font-size: 11px;
  color: var(--pvf-text-muted);
}

.fv-crumb-sep {
  font-size: 11px;
  color: var(--pvf-text-faint);
}

.fv-title {
  font-size: 13px;
  font-weight: 600;
}

.fv-head-actions {
  display: flex;
  align-items: center;
  gap: 6px;
}

/* ② 参数区 */
.fv-form {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: 0 0 auto;
  padding: 0 10px 8px;
  border-bottom: 1px solid var(--pvf-border-faint);
}

.fv-form-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: nowrap;
}

.fv-label {
  flex: 0 0 auto;
  font-size: 11px;
  color: var(--pvf-text-muted);
}

.fv-select {
  flex: 0 0 150px;
  width: 150px;
}

.fv-form-sub {
  display: flex;
  align-items: center;
  gap: 8px;
}

.fv-rule {
  font-size: 11px;
  color: var(--pvf-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fv-notes {
  font-size: 11px;
  line-height: 1.5;
  color: var(--pvf-text-secondary);
  background: var(--pvf-surface-subtle);
  border: 1px solid var(--pvf-border-faint);
  border-radius: 4px;
  padding: 5px 8px;
  /* 规则说明可能很长（含实测结论）：这里最多两行，完整内容悬停看 title */
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

/* ③ 错误 */
.fv-error {
  flex: 0 0 auto;
  margin: 6px 10px 0;
  font-size: 12px;
  color: var(--pvf-text-primary);
  background: var(--pvf-surface-error);
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 4px;
  padding: 6px 8px;
  word-break: break-all;
}

/* ④ 滚动区（唯一的滚动容器） */
.fv-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  padding: 8px 10px 10px;
}

.fv-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 11px;
  color: var(--pvf-text-secondary);
}

.fv-stats-strong {
  font-weight: 600;
  color: var(--pvf-text-primary);
}

.fv-sep {
  color: var(--pvf-text-faint);
}

.fv-warnings {
  margin: 4px 0 0;
  padding-left: 16px;
  font-size: 11px;
  line-height: 1.6;
  color: var(--pvf-text-secondary);
}

.fv-section-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin: 10px 0 4px;
}

.fv-section-title {
  font-size: 12px;
  font-weight: 600;
}

.fv-section-meta {
  font-size: 11px;
  color: var(--pvf-text-muted);
}

/* 表格：sticky 表头要求 border-collapse: separate（collapse 下 sticky 会掉边框）。
   只占**内容宽度**，绝不加 min-width:100% —— 否则列宽总和不足窗口时，浏览器会把
   多余宽度摊给内容最长的列，在中间摊出一大片空白，而且表格被钉住、列宽拉不窄
   （2026-10-03 用户报的"中间一片空白 / 向左拉没反应"）。 */
.fv-table {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 11px;
  width: max-content;
}

.fv-col-index {
  width: 56px;
}

/* 单元格内容的包装元素。
   拖过列宽后由**行内** max-width 约束（刻意不靠样式表：2026-10-03 事故里
   前端 JS 是新版、但样式表行为没生效）。没给宽度时它就是个普通行内元素，
   列宽完全由内容决定 —— 中间不可能出现空白。 */
.fv-cell {
  display: inline-block;
  /* ★ 这里必须是**绝对像素**上限，绝不能写百分比！
     2026-10-03 定了两件事（都是实测）：
     ① 百分比尺寸（max-width:100% / min-width:100%）在 auto 布局的表格里会产生
        "循环依赖"，浏览器把宽度结算到某一列上 ⇒ 后面拖出一大片空白；
     ② 即便没有百分比，**auto 布局下每列宽度 = 该列所有单元格里最宽的那个**。
        表格一次渲染 200 行，屏幕外的行里只要有**一个超长物品名**，这一列就被撑宽，
        文字左对齐、右边留出空白 —— 用户看到的"空白"就是这么来的。
     所以给内容一个绝对上限，超长就省略号，完整内容看 title。 */
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

.fv-table th,
.fv-table td {
  border-bottom: 1px solid var(--pvf-border-faint);
  border-right: 1px solid var(--pvf-border-faint);
  /* 紧凑：行高压到最小，靠斑马纹与分隔线辨行 */
  padding: 2px 6px;
  text-align: left;
  white-space: nowrap;
  background: var(--pvf-surface-panel);
}

.fv-table thead th {
  position: sticky;
  top: 0;
  z-index: 2;
  background: var(--pvf-surface-elevated);
  color: var(--pvf-text-secondary);
  font-weight: 600;
  padding-right: 10px;
}

.fv-th-text {
  display: inline-block;
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: bottom;
}

/* 列宽拖拽手柄（th 是 sticky，本身就是定位父级） */
.fv-th-grip {
  position: absolute;
  top: 0;
  right: 0;
  width: 7px;
  height: 100%;
  cursor: col-resize;
  user-select: none;
  touch-action: none;
  background: linear-gradient(
    to right,
    transparent 0,
    transparent 3px,
    var(--pvf-border-normal, #4a4a4a) 3px,
    var(--pvf-border-normal, #4a4a4a) 4px,
    transparent 4px
  );
  opacity: 0.55;
}

.fv-th-grip:hover {
  opacity: 1;
}

.fv-table tbody tr:nth-child(even) td {
  background: var(--pvf-surface-subtle);
}

.fv-table tbody tr:hover td {
  background: var(--pvf-surface-hover);
}

.fv-num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.fv-td-index,
.fv-th-index {
  color: var(--pvf-text-faint);
  text-align: right;
  width: 56px;
}

.fv-row-incomplete td {
  background: var(--pvf-surface-warning);
}

/* 可双击查看关联的格 */
/* 关联列表标记（如「内联列表 🔗 查看」）：原来只挂一个淡 🔗，用户看不清 —— 改成药丸底 + 醒目徽章。 */
.fv-link-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--pvf-surface-selected);
  border: 1px solid var(--pvf-border-normal);
  color: var(--pvf-text-primary);
  font-weight: 600;
}

.fv-link-text {
  white-space: nowrap;
}

/* 提高一级选择器，压过下面既有的 .fv-link-badge */
.fv-link-pill .fv-link-badge {
  display: inline-flex;
  align-items: center;
  padding: 0 4px;
  border-radius: 3px;
  background: var(--pvf-warning-surface);
  color: var(--pvf-warning);
  font-size: 11px;
  font-weight: 600;
}

/* 有未保存草稿的格：底色 + 左侧一道标记（让人一眼看出"这格改了还没保存"） */
.fv-table tbody td.fv-cell-dirty {
  background: var(--pvf-surface-warning);
}

.fv-table tbody td.fv-cell-dirty .fv-cell::before {
  content: "";
  display: inline-block;
  width: 3px;
  height: 1em;
  margin-right: 4px;
  vertical-align: -2px;
  background: var(--pvf-warning);
}

/* 选中行 / 待删除行（点行选中，再点「删除选中」） */
.fv-table tbody tr.fv-row-selected > td {
  background: var(--pvf-surface-selected);
  box-shadow: inset 3px 0 0 0 var(--pvf-text-primary);
}

.fv-table tbody tr.fv-row-deleting > td {
  background: var(--pvf-surface-error);
  text-decoration: line-through;
}

/* 搜索命中的那一段（用户 2026-10-03：搜 28 时把 281 里的 28 标出来） */
.fv-hit {
  background: var(--pvf-warning-surface);
  color: var(--pvf-warning);
  border-radius: 2px;
  padding: 0 1px;
  font-weight: 600;
}

.fv-tools-dirty {
  color: var(--pvf-warning);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}

.fv-link-cell {
  cursor: pointer;
  text-decoration: underline dotted;
  text-underline-offset: 2px;
}

.fv-link-badge {
  font-size: 9px;
  margin-left: 3px;
  opacity: 0.75;
}

/* ref 列解析出的名称：名称在前、编号在后（编号淡一点，便于对照） */
.fv-name {
  color: var(--pvf-text-primary);
}

.fv-id {
  margin-left: 5px;
  font-size: 10px;
  color: var(--pvf-text-faint);
  font-variant-numeric: tabular-nums;
}

/* 正在编辑的格 */
.fv-cell-editing {
  padding: 1px 3px;
}

.fv-edit-input {
  width: 100%;
  min-width: 72px;
}

.fv-edit-input :deep(.n-input__input-el) {
  font-size: 11px;
  padding: 0 4px;
}

.fv-table--compact th,
.fv-table--compact td {
  padding: 2px 6px;
}

.fv-others {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 4px;
}

.fv-block {
  border: 1px solid var(--pvf-border-faint);
  border-radius: 4px;
  padding: 4px 6px 6px;
  background: var(--pvf-surface-subtle);
}

.fv-block > summary {
  cursor: pointer;
  font-size: 11px;
  color: var(--pvf-text-secondary);
  display: flex;
  justify-content: space-between;
  gap: 8px;
}

.fv-block > table {
  margin-top: 4px;
}

.fv-hint {
  padding: 16px 0;
}

/* ⑤ 页脚 */
.fv-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex: 0 0 auto;
  padding: 6px 10px;
  border-top: 1px solid var(--pvf-border-faint);
  background: var(--pvf-surface-panel);
}

.fv-foot-info,
.fv-foot-hint {
  font-size: 11px;
  color: var(--pvf-text-muted);
}

.fv-pager {
  display: flex;
  align-items: center;
  gap: 8px;
}

.fv-pager-text {
  font-size: 11px;
  color: var(--pvf-text-secondary);
  font-variant-numeric: tabular-nums;
}

/* ⑥ 查看器 */
.fv-tools {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  margin: 6px 0;
  border: 1px solid var(--pvf-border-faint);
  border-radius: 6px;
  background: var(--pvf-surface-subtle);
  flex-wrap: wrap;
}

.fv-tools-label {
  color: var(--pvf-text-muted);
  font-size: 12px;
  white-space: nowrap;
}

.fv-tools-scope {
  width: 190px;
}

.fv-tools-field {
  width: 88px;
}

.fv-tools-query {
  width: 240px;
}

.fv-tools-hit {
  color: var(--pvf-text-primary);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}

.fv-tools-gap {
  flex: 1;
}

.fv-batch {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
  margin-bottom: 8px;
  border: 1px dashed var(--pvf-border-normal);
  border-radius: 6px;
  background: var(--pvf-surface-subtle);
}

.fv-batch-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.fv-batch-col {
  width: 170px;
}

.fv-batch-op {
  width: 110px;
}

.fv-batch-num {
  width: 120px;
}

.fv-batch-count {
  font-size: 12px;
  color: var(--pvf-text-secondary);
}

.fv-batch-hint {
  font-size: 12px;
  color: var(--pvf-text-muted);
}

/* 「添加掉落」表单 */
.fv-drop {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.fv-drop-row {
  display: flex;
  align-items: center;
  gap: 10px 8px;
  flex-wrap: wrap;
}

/* 标签固定成一列宽度并右对齐 ⇒ 各行字段竖直对齐（用户 2026-10-03：表单太乱，对齐一下） */
.fv-drop-label {
  flex: 0 0 84px;
  width: 84px;
  text-align: right;
  color: var(--pvf-text-muted);
  font-size: 12px;
  white-space: nowrap;
}

.fv-drop-small {
  width: 110px;
}

.fv-drop-way {
  width: 180px;
}

.fv-drop-id {
  width: 140px;
}

.fv-drop-weight {
  width: 100px;
}

.fv-drop-num {
  width: 76px;
}

.fv-drop-name {
  color: var(--pvf-text-secondary);
  font-size: 12px;
  white-space: nowrap;
}

.fv-drop-hint {
  color: var(--pvf-text-muted);
  font-size: 12px;
}

.fv-drop-candidates {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
  border: 1px dashed var(--pvf-border-normal);
  border-radius: 6px;
  background: var(--pvf-surface-subtle);
}

.fv-drop-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.fv-viewer {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.fv-viewer-meta,
.fv-viewer-hint {
  font-size: 11px;
  color: var(--pvf-text-muted);
}

.fv-viewer-table {
  max-height: 52vh;
  overflow: auto;
  border: 1px solid var(--pvf-border-faint);
  border-radius: 4px;
}
</style>
