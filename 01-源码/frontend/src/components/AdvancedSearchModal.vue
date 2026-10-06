<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import {
  NAlert,
  NButton,
  NCheckbox,
  NEmpty,
  NIcon,
  NInput,
  NModal,
  NPopover,
  NSpin,
  NTree,
  NTag,
  NTooltip,
  useMessage,
} from "naive-ui";
import { Add20Regular, Search16Regular, Search24Regular, Target20Regular } from "@vicons/fluent";
import { ArchiveService } from "../../bindings/pvfine/services";
import type { SearchHit } from "../../bindings/pvfine/services/models";
import { useArchiveStore } from "../stores/archive";
import { useAdvancedSearchStore } from "../stores/advancedSearch";
import { useEditorStore } from "../stores/editor";
import { useExplorerStore, type SearchItem } from "../stores/explorer";
import { useSearchWindowStore } from "../stores/searchWindow";
import { useSidebarStore } from "../stores/sidebar";
import { clearSearchHitLines, publishSearchHitLines } from "../searchMarks";
import { SearchInFile } from "../services/fileSearchApi";

/**
 * 「高级搜索」对话框：左侧文件树搜索引擎的图形化版本（原内容/字符串池搜索已停用）。
 * 独立于左树状态：在这里搜索不会把左树切到搜索模式；双击命中直接打开文件。
 */
const search = useAdvancedSearchStore();
const archive = useArchiveStore();
const editor = useEditorStore();
const explorer = useExplorerStore();
const searchWindow = useSearchWindowStore();
const sidebar = useSidebarStore();
const message = useMessage();

/** 界面模式：精确定位（现界面）/ 添加视图（勾选批量收进搜索视窗）。 */
const viewMode = ref<"locate" | "add">("locate");
/** 添加视图模式的勾选集（key = 结果行 key）。 */
const checkedKeys = ref<Set<string>>(new Set());
const checkedCount = computed(() => checkedKeys.value.size);
const allChecked = computed(
  () => hits.value.length > 0 && hits.value.every((item) => checkedKeys.value.has(item.key))
);
const someChecked = computed(() => hits.value.some((item) => checkedKeys.value.has(item.key)));

function toggleAll(checked: boolean): void {
  checkedKeys.value = checked ? new Set(hits.value.map((item) => item.key)) : new Set();
}

function toggleItem(item: SearchItem, checked: boolean): void {
  const next = new Set(checkedKeys.value);
  if (checked) next.add(item.key);
  else next.delete(item.key);
  checkedKeys.value = next;
}

/** 把勾选的搜索结果批量收进右侧搜索视窗（复用搜索视窗 store 的去重收纳）。 */
function addCheckedToWindow(): void {
  const items = hits.value.filter((item) => checkedKeys.value.has(item.key));
  if (items.length === 0) {
    message.info("请先勾选要添加的文件");
    return;
  }
  const result = searchWindow.addEntries(items);
  sidebar.show("search");
  if (result.added === 0) {
    message.info("所选文件已在搜索视窗中");
  } else {
    const skipped = result.skipped > 0 ? `，跳过 ${result.skipped} 个重复` : "";
    message.success(`已添加 ${result.added} 个文件到搜索视窗${skipped}`);
  }
  checkedKeys.value = new Set();
  // 执行成功后关闭搜索框，方便用户直接查看右侧搜索视窗。
  search.close();
}

const query = ref("");
const exact = ref(false);
/**
 * A9（2026-10-06）：**内容搜索**模式。
 *
 * 面板原本只搜「文件记录」（路径/名称/ID —— `ArchiveService.Search`），文件记录没有"行"，
 * 所以永远显示不了行号。勾上它改走 `ArchiveService.AdvancedSearch("string", …)`（内容/字符串池
 * 搜索），命中带 **Line**（后端在分页出口用现成的行偏移表算出来）⇒ 可以显示行号并按行精确跳转。
 * 标记：pvfContentSearchLineA9_20261006
 */
const contentMode = ref(false);
/** key → 行号（1 基）。只有内容搜索模式下才会有值。 */
const hitLines = ref(new Map<string, number>());
/** 归档文件索引 → 命中行号（给编辑器的概览条打点用，见 ../searchMarks）。 */
const contentByFile = ref(new Map<number, number[]>());
/**
 * C（2026-10-06）：「范围扫描」——**目标文件夹**（必填，如 `etc/`）。
 * 用户要求"取消建大索引"后，正文搜索改走：① 用元数据搜索按范围圈文件（轻）
 * ② 对圈出来的文件**逐个**调 `SearchInFile` 扫正文（每次一个小请求，带进度、随时可停）。
 * 全程**不建索引、不常驻内存**。标记：pvfScopedScanC_20261006
 */
const contentScope = ref("");
/** 扫描进度（active = 正在扫，点「停止」即中断）。 */
const scanProgress = ref({ done: 0, total: 0, active: false });
const scanStop = ref(false);

/**
 * C（2026-10-06 用户要求）：**从左树点选目标文件夹**，不再手打路径。
 *
 * 树数据直接来自归档自身的懒加载接口 `ArchiveService.ListChildren(path)`（与左侧文件树
 * 同一套数据、同一个约定：`""` = 根），展开哪层才拉哪层；只列**目录**（范围就是"这个目录
 * 及其下属子目录与文件"）。选中即把 `目录路径 + "/"` 填进范围。标记：pvfScopePickerC_20261006
 */
interface ScopeNode {
  key: string;
  label: string;
  isLeaf: boolean;
  /** 懒加载：`undefined` = 还没展开过（NTree 会据此显示展开箭头）；`[]` = 已展开且为空。 */
  children?: ScopeNode[];
}

const scopePickerOpen = ref(false);
const scopeTree = ref<ScopeNode[]>([]);
const scopeTreeLoading = ref(false);

async function fetchScopeChildren(path: string): Promise<ScopeNode[]> {
  const nodes = (await ArchiveService.ListChildren(path)) ?? [];
  return nodes
    .filter((node) => !!node)
    .map((node) => ({
      key: node.path,
      // 目录名取路径末段（TreeNode 的 name 是可选字段，这里不依赖它）
      label: node.path.split("/").filter(Boolean).pop() ?? node.path,
      isLeaf: false,
    }));
}

/** n-tree onLoad：展开才拉子目录（空目录置为叶子，免得多一个没用的展开箭头）。 */
async function loadScopeNode(node: any): Promise<void> {
  // 参数类型放宽到 any：naive-ui 的 on-load 是 TreeOption 泛型，结构上兼容但显式标注容易挑刺
  try {
    const children = await fetchScopeChildren(node.key);
    node.children = children;
    if (children.length === 0) node.isLeaf = true;
  } catch {
    node.children = [];
    node.isLeaf = true;
  }
}

/** 打开选择器：首次只拉顶层目录。 */
async function openScopePicker(): Promise<void> {
  scopePickerOpen.value = true;
  if (scopeTree.value.length > 0) return;
  scopeTreeLoading.value = true;
  try {
    scopeTree.value = await fetchScopeChildren("");
  } catch {
    scopeTree.value = [];
  } finally {
    scopeTreeLoading.value = false;
  }
}

function pickScope(keys: Array<string | number>): void {
  const key = keys.length > 0 ? String(keys[0]) : "";
  if (!key) return;
  contentScope.value = key.endsWith("/") ? key : `${key}/`;
  scopePickerOpen.value = false;
}
/** 扫描代次：换关键词/重扫时旧的那轮会自行退出（避免两轮一起写结果）。 */
let scanToken = 0;
const hits = ref<SearchItem[]>([]);
const nextCursor = ref(-1);
const searching = ref(false);
const searched = ref(false);
const error = ref("");
const inputEl = ref<{ focus: () => void } | null>(null);
const listEl = ref<HTMLElement | null>(null);

const loadedCount = computed(() => hits.value.length);

watch(
  () => search.visible,
  (visible) => {
    if (visible) {
      error.value = "";
      // A9 自查修复：F3 挂在**窗口**上而不是结果区 —— 面板打开时焦点在搜索框（默认行为），
      // 只挂结果区会导致"打开后直接按 F3 没反应"。
      window.addEventListener("keydown", onResultsKeydown);
      window.setTimeout(() => inputEl.value?.focus(), 60);
    } else {
      window.removeEventListener("keydown", onResultsKeydown);
      // A6 v2：面板关闭后命中点不再可信（用户可能已改文件），一起清掉
      clearSearchHitLines();
    }
  }
);

onBeforeUnmount(() => {
  window.removeEventListener("keydown", onResultsKeydown);
});

function resetResults(): void {
  hits.value = [];
  nextCursor.value = -1;
  searched.value = false;
  checkedKeys.value = new Set();
  // A6 v2：结果清了，编辑器上的命中点也要一起清（否则留下"幽灵点"）
  contentByFile.value = new Map();
  clearSearchHitLines();
}

/**
 * A9：把内容搜索的命中摊平成面板用的行（一个文件可能有多条命中）。
 * `line` 是 Go 侧新加的字段 —— 本机无法重生成 bindings，所以这里按 `unknown` 窄化读取，
 * 不碰 `bindings/`（与 `services/formViewApi.ts` 同一种做法）。
 */
function buildContentRows(result: unknown): {
  rows: SearchItem[];
  lines: Map<string, number>;
  byFile: Map<number, number[]>;
} {
  const rows: SearchItem[] = [];
  const lines = new Map<string, number>();
  const byFile = new Map<number, number[]>();
  const source = (result ?? {}) as { hits?: unknown[] };
  let seq = 0;
  for (const hitRaw of source.hits ?? []) {
    const hit = hitRaw as {
      name?: string;
      path?: string;
      size?: number;
      dataType?: number;
      fileIndex?: number;
      details?: unknown[];
    } | null;
    if (!hit) continue;
    for (const detailRaw of hit.details ?? []) {
      const detail = detailRaw as { value?: string; poolOffset?: number; line?: number } | null;
      if (!detail) continue;
      const value = (detail.value ?? "").trim();
      seq += 1;
      const key = `${hit.fileIndex ?? -1}#${detail.poolOffset ?? 0}#${seq}`;
      const line = Number(detail.line ?? 0);
      if (line > 0) {
        lines.set(key, line);
        // A6 v2：同一文件的行号归集到一处（给概览条打点用；后面统一去重排序）
        const bucket = byFile.get(hit.fileIndex ?? -1);
        if (bucket) bucket.push(line);
        else byFile.set(hit.fileIndex ?? -1, [line]);
      }
      rows.push({
        key,
        label: value || hit.path || "",
        path: hit.path ?? "",
        id: "",
        name: hit.name ?? "",
        category: "内容",
        fileIndex: hit.fileIndex ?? -1,
        size: hit.size ?? 0,
        dataType: hit.dataType ?? 0,
      } as unknown as SearchItem);
    }
  }
  for (const [index, list] of byFile) {
    byFile.set(index, [...new Set(list)].sort((a, b) => a - b));
  }
  return { rows, lines, byFile };
}

/** A9：命中行号（没有就返回 0 ⇒ 调用方退化为 needle 模糊定位）。 */
function lineOf(item: SearchItem): number {
  return hitLines.value.get(item.key) ?? 0;
}

/**
 * C：**范围扫描**（正文搜索，不建索引）。
 *
 * ① 用元数据搜索按「目标文件夹」把文件圈出来（路径子串/通配符，很轻，4 秒级小索引）；
 * ② 对圈出来的文件**逐个**调 `SearchInFile`（每次一个小请求）—— 边扫边出结果、显示进度、
 *    点「停止」立刻中断；换关键词/重扫时旧的一轮靠 `scanToken` 自行退出。
 *
 * 与已停用的"跨归档正文检索"的区别：**不建索引、不常驻内存、不会长时间占全局锁**，
 * 代价是范围越大越慢（但随时可停，不像建索引那样一旦开始就得等）。
 * 标记：pvfScopedScanC_20261006
 */
async function runScopedContentScan(term: string): Promise<void> {
  const scope = contentScope.value.trim();
  if (scope === "") {
    error.value = "请先填「目标文件夹」（例如 etc/ 或 equipment/character/），再扫描";
    return;
  }
  if (scanProgress.value.active) return;
  resetResults();
  const token = ++scanToken;
  scanStop.value = false;
  scanProgress.value = { done: 0, total: 0, active: true };
  error.value = "";
  try {
    // ① 圈文件：范围命中上限 300 个（够了；再多请缩小范围）
    const listed = await ArchiveService.Search(scope, 0, 300);
    if (token !== scanToken) return;
    const files = (listed?.hits ?? [])
      .map((hit: SearchHit | null) => explorer.toSearchItem(hit as SearchHit))
      .filter((item) => item.fileIndex >= 0);
    if (files.length === 0) {
      error.value = `范围「${scope}」没有匹配到文件`;
      return;
    }
    scanProgress.value = { done: 0, total: files.length, active: true };

    const rows: SearchItem[] = [];
    const lines = new Map<string, number>();
    const byFile = new Map<number, number[]>();
    let seq = 0;
    for (const file of files) {
      if (token !== scanToken || scanStop.value) break;
      try {
        const found = await SearchInFile(file.fileIndex, term, false, false, false, 50, []);
        for (const match of found?.matches ?? []) {
          seq += 1;
          const key = `${file.fileIndex}#${match.line}#${seq}`;
          lines.set(key, match.line);
          const bucket = byFile.get(file.fileIndex);
          if (bucket) bucket.push(match.line);
          else byFile.set(file.fileIndex, [match.line]);
          rows.push({
            key,
            label: (match.text ?? "").trim() || term,
            path: file.path,
            id: "",
            name: file.name,
            category: "正文",
            fileIndex: file.fileIndex,
            size: file.size,
            dataType: file.dataType,
          } as unknown as SearchItem);
        }
      } catch {
        // 单个文件失败（解不开/超大）不打断整轮：继续扫下一个
      }
      const done = scanProgress.value.done + 1;
      scanProgress.value = { done, total: files.length, active: true };
      // 每 5 个文件刷一次列表：够"边扫边看"，又不会把大数组每文件重渲染一遍
      if (done % 5 === 0) hits.value = [...rows];
    }

    hits.value = rows;
    hitLines.value = lines;
    contentByFile.value = byFile;
    // 命中行发布给编辑器：概览条打黄点（与"当前文件搜索"共用同一通道）
    publishSearchHitLines(byFile);
    searched.value = true;
    if (scanStop.value) {
      message.info(`已停止：扫了 ${scanProgress.value.done} / ${files.length} 个文件，命中 ${rows.length} 处`);
    } else if (rows.length === 0) {
      message.info(`扫完 ${files.length} 个文件：没有命中`);
    } else {
      message.success(`扫完 ${files.length} 个文件：命中 ${rows.length} 处`);
    }
  } catch (e: any) {
    if (token === scanToken) error.value = String(e?.message ?? e);
  } finally {
    if (token === scanToken) scanProgress.value = { ...scanProgress.value, active: false };
  }
}

async function runSearch(): Promise<void> {
  const term = query.value.trim();
  if (!term || searching.value) return;
  if (!archive.indexReady) {
    void archive.ensureSearchIndex();
    return;
  }
  searching.value = true;
  error.value = "";
  try {
    if (contentMode.value) {
      // C：正文搜索走「范围扫描」——不建索引（那份 4.9GB 的索引已按用户要求取消）
      await runScopedContentScan(term);
      return;
    }
    const result = exact.value
      ? await ArchiveService.SearchExact(term, 0, 200)
      : await ArchiveService.Search(term, 0, 200);
    hits.value = (result?.hits ?? []).map((hit: SearchHit | null) => explorer.toSearchItem(hit as SearchHit));
    hitLines.value = new Map();
    nextCursor.value = result?.nextCursor ?? -1;
    searched.value = true;
  } catch (e: any) {
    error.value = String(e?.message ?? e);
    searched.value = true;
  } finally {
    searching.value = false;
  }
}

async function loadMore(): Promise<void> {
  const term = query.value.trim();
  if (!term || searching.value || nextCursor.value < 0) return;
  searching.value = true;
  error.value = "";
  try {
    if (contentMode.value) {
      // C：范围扫描是"逐个文件扫"的自驱动分页，没有"加载更多"这一步
      message.info("范围扫描不需要加载更多：改范围或关键词后重新扫描即可");
      return;
    }
    const result = exact.value
      ? await ArchiveService.SearchExact(term, nextCursor.value, 200)
      : await ArchiveService.Search(term, nextCursor.value, 200);
    const more = (result?.hits ?? []).map((hit: SearchHit | null) =>
      explorer.toSearchItem(hit as SearchHit)
    );
    hits.value = [...hits.value, ...more];
    nextCursor.value = result?.nextCursor ?? -1;
  } catch (e: any) {
    error.value = String(e?.message ?? e);
  } finally {
    searching.value = false;
  }
}

function clearAll(): void {
  query.value = "";
  resetResults();
  error.value = "";
  inputEl.value?.focus();
}

const activeKey = ref<string | null>(null);

/** 结果列宽（px）：四列对齐，列间竖线可左右拉伸（路径列占剩余宽度）。 */
const colWidths = ref({ type: 68, name: 210, id: 96 });
type ResultCol = "type" | "name" | "id";
let resizing: { col: ResultCol; startX: number; startW: number } | null = null;

function startResize(event: MouseEvent, col: ResultCol): void {
  event.preventDefault();
  event.stopPropagation();
  resizing = { col, startX: event.clientX, startW: colWidths.value[col] };
  const onMove = (e: MouseEvent) => {
    if (!resizing) return;
    const width = Math.max(24, Math.min(720, resizing.startW + (e.clientX - resizing.startX)));
    colWidths.value = { ...colWidths.value, [resizing.col]: width };
  };
  const onUp = () => {
    resizing = null;
    window.removeEventListener("mousemove", onMove);
    window.removeEventListener("mouseup", onUp);
  };
  window.addEventListener("mousemove", onMove);
  window.addEventListener("mouseup", onUp);
}

/** 打开文件 + 定位左树 + 关闭搜索框（单按钮/双击/回车统一走这里）。 */
async function openItem(item: SearchItem): Promise<void> {
  if (item.fileIndex < 0) return;
  try {
    if (explorer.mode === "search") {
      explorer.clearSearch();
    }
    // A9：不再只是"打开文件"——直接把编辑器**滚到并高亮命中内容**。
    // 内容搜索模式下有**准确行号** ⇒ 按行跳（最准）；否则退回按 needle 模糊定位。
    // 标记：pvfAdvancedSearchStepA9_20261006 / pvfContentSearchLineA9_20261006
    const line = lineOf(item);
    if (line > 0) {
      await editor.revealFileLine(item.fileIndex, line);
    } else {
      await editor.revealInFile(item.fileIndex, [query.value, item.label, item.name, item.id]);
    }
    await explorer.revealPath(item.path);
    search.close();
  } catch (e: any) {
    error.value = String(e?.message ?? e);
  }
}

/**
 * A9「跨文档跳下一处 / 上一处」。
 *
 * 与 `openItem` 刻意分开：`openItem` 会**关闭搜索框**（定位完就该看文件），
 * 而连续查看时必须**留着面板**，否则每跳一次就要重新搜一遍。
 * 行光标复用既有的 `activeKey`（单击行即设），到末尾**环绕**。
 * 快捷键：F3 = 下一处，Shift+F3 = 上一处（与主流编辑器一致）。
 */
async function jumpTo(index: number): Promise<void> {
  const item = hits.value[index];
  if (!item || item.fileIndex < 0) return;
  activeKey.value = item.key;
  try {
    // A9 自查修复：与 openItem 对齐 —— 左树还在搜索模式时，revealPath 设了也没人渲染。
    if (explorer.mode === "search") {
      explorer.clearSearch();
    }
    const line = lineOf(item);
    if (line > 0) {
      await editor.revealFileLine(item.fileIndex, line);
    } else {
      await editor.revealInFile(item.fileIndex, [query.value, item.label, item.name, item.id]);
    }
    await explorer.revealPath(item.path);
  } catch (e: any) {
    error.value = String(e?.message ?? e);
  }
}

async function stepHit(delta: number): Promise<void> {
  const total = hits.value.length;
  if (total === 0) return;
  const current = hits.value.findIndex((item) => item.key === activeKey.value);
  const next = current < 0 ? (delta > 0 ? 0 : total - 1) : (current + delta + total) % total;
  await jumpTo(next);
}

function onResultsKeydown(event: KeyboardEvent): void {
  if (event.key !== "F3") return;
  event.preventDefault();
  void stepHit(event.shiftKey ? -1 : 1);
}

function onRowKeydown(event: KeyboardEvent, item: SearchItem): void {
  if (viewMode.value !== "locate") return;
  if (event.key === "Enter") void openItem(item);
}

function buildSearchIndex(): void {
  void archive.ensureSearchIndex();
}
</script>

<template>
  <NModal
    :show="search.visible"
    preset="card"
    :bordered="false"
    :style="{ width: 'min(880px, calc(100vw - 48px))' }"
    :mask-closable="true"
    :close-on-esc="true"
    @update:show="search.visible = $event"
  >
    <template #header>
      <div class="as-header">
        <NIcon :size="18" class="as-header-icon"><Search24Regular /></NIcon>
        <span>高级搜索</span>
        <NTag size="tiny" :bordered="false" type="info">路径 / 名称 / ID</NTag>
        <div class="as-mode-switch">
          <button
            :class="['as-mode-btn', { 'as-mode-btn--active': viewMode === 'locate' }]"
            @click="viewMode = 'locate'"
          >
            <NIcon :size="14"><Target20Regular /></NIcon>
            <span>精确定位</span>
          </button>
          <button
            :class="['as-mode-btn', { 'as-mode-btn--active': viewMode === 'add' }]"
            @click="viewMode = 'add'"
          >
            <NIcon :size="14"><Add20Regular /></NIcon>
            <span>添加视图</span>
          </button>
        </div>
      </div>
    </template>

    <div class="as-body">
      <div class="as-search-row">
        <NInput
          ref="inputEl"
          v-model:value="query"
          size="large"
          round
          clearable
          :placeholder="'搜索路径、名称或 ID（支持 * 、 ? 通配符），回车搜索'"
          :disabled="!archive.open"
          @keyup.enter="runSearch"
          @clear="resetResults"
        >
          <template #prefix>
            <NIcon :size="16"><Search16Regular /></NIcon>
          </template>
        </NInput>
        <NButton size="large" type="primary" :loading="searching" :disabled="!archive.open || !query.trim()" @click="runSearch">
          搜索
        </NButton>
      </div>

      <div class="as-options-row">
        <template v-if="viewMode === 'locate'">
          <NCheckbox v-model:checked="exact" size="small" :disabled="searching || contentMode">
            精确匹配（整词相等，不再做子串匹配）
          </NCheckbox>
          <NCheckbox
            v-model:checked="contentMode"
            size="small"
            :disabled="searching || scanProgress.active"
            @update:checked="resetResults"
          >
            正文搜索（范围扫描，不建大索引）
          </NCheckbox>
          <template v-if="contentMode">
            <NInput
              v-model:value="contentScope"
              size="tiny"
              style="width: 190px"
              placeholder="目标文件夹（可点右侧选择）"
              :disabled="searching || scanProgress.active"
            />
            <!-- C：点这里从左树选目录（展开哪层拉哪层），选中即填范围 -->
            <NPopover v-model:show="scopePickerOpen" trigger="click" placement="bottom-start" :width="300">
              <template #trigger>
                <NButton size="tiny" secondary :disabled="searching || scanProgress.active" @click="openScopePicker">
                  选择文件夹
                </NButton>
              </template>
              <div class="as-scope-picker">
                <div class="as-scope-picker-head">
                  <span>点选要搜索的目录</span>
                  <NButton size="tiny" quaternary @click="contentScope = ''">清空</NButton>
                </div>
                <NSpin :show="scopeTreeLoading" size="small">
                  <NTree
                    v-if="scopeTree.length > 0"
                    block-line
                    selectable
                    :cancelable="false"
                    :data="scopeTree"
                    :on-load="loadScopeNode"
                    @update:selected-keys="pickScope"
                  />
                  <div v-else class="as-scope-picker-empty">（没有可选的目录）</div>
                </NSpin>
              </div>
            </NPopover>
            <span v-if="scanProgress.active" class="as-meta">
              已扫 {{ scanProgress.done }} / {{ scanProgress.total }}…
            </span>
            <NButton v-if="scanProgress.active" size="tiny" secondary @click="scanStop = true">停止</NButton>
          </template>
        </template>
        <template v-else>
          <NCheckbox
            :checked="allChecked"
            :indeterminate="someChecked && !allChecked"
            :disabled="hits.length === 0"
            size="small"
            @update:checked="toggleAll"
          >
            全选
          </NCheckbox>
          <NButton size="tiny" type="primary" :disabled="checkedCount === 0" @click="addCheckedToWindow">
            添加到视图（{{ checkedCount }}）
          </NButton>
        </template>
        <NTooltip v-if="!archive.indexReady" trigger="hover">
          <template #trigger>
            <NButton size="tiny" type="warning" secondary @click="buildSearchIndex">
              搜索索引未就绪：点击构建
            </NButton>
          </template>
          打开归档后索引会自动构建；也可手动触发
        </NTooltip>
        <span v-if="searched && !searching" class="as-meta">
          命中 {{ loadedCount.toLocaleString() }} 条<template v-if="nextCursor >= 0">（还有更多，可加载）</template>
        </span>
      </div>

      <NAlert v-if="error" type="error" :show-icon="false" class="as-error">
        {{ error }}
      </NAlert>

      <div ref="listEl" class="as-results">
        <NSpin :show="searching && hits.length === 0">
          <NEmpty
            v-if="hits.length === 0 && !searching"
            :description="searched ? '没有匹配的文件' : '输入关键词后回车搜索'"
            size="small"
            class="as-empty"
          />
          <div
            v-for="item in hits"
            :key="item.key"
            class="as-row"
            :class="{ 'as-row--active': viewMode === 'locate' && activeKey === item.key }"
            tabindex="0"
            @click="viewMode === 'add' ? toggleItem(item, !checkedKeys.has(item.key)) : (activeKey = item.key)"
            @dblclick="viewMode === 'locate' && openItem(item)"
            @keydown="onRowKeydown($event, item)"
          >
            <NCheckbox
              v-if="viewMode === 'add'"
              class="as-row-check"
              size="small"
              :checked="checkedKeys.has(item.key)"
              @click.stop
              @update:checked="(checked: boolean) => toggleItem(item, checked)"
            />
            <span class="as-col" :style="{ width: colWidths.type + 'px' }">
              <NTag size="tiny" :bordered="false" class="as-row-type">{{ item.category || "file" }}</NTag>
            </span>
            <span class="col-resizer" title="拖动调整列宽" @mousedown.stop="startResize($event, 'type')" @dblclick.stop />
            <span class="as-col as-col-name" :style="{ width: colWidths.name + 'px' }" :title="item.label">{{ item.label }}</span>
            <span class="col-resizer" title="拖动调整列宽" @mousedown.stop="startResize($event, 'name')" @dblclick.stop />
            <span class="as-col as-col-id" :style="{ width: colWidths.id + 'px' }">
              <NTag v-if="item.id" size="tiny" :bordered="false" type="info">{{ item.id }}</NTag>
            </span>
            <span class="col-resizer" title="拖动调整列宽" @mousedown.stop="startResize($event, 'id')" @dblclick.stop />
            <span class="as-row-path" :title="item.path">{{ item.path }}</span>
            <!-- A9：内容搜索的命中行号（点行/按钮即按行精确跳转） -->
            <span v-if="lineOf(item) > 0" class="as-row-line">第 {{ lineOf(item) }} 行</span>
            <span v-if="viewMode === 'locate'" class="as-row-actions">
              <NTooltip trigger="hover">
                <template #trigger>
                  <button class="as-row-btn" aria-label="打开并定位" @click.stop="openItem(item)">
                    <NIcon :size="13"><Target20Regular /></NIcon>
                  </button>
                </template>
                打开文件并定位到左树
              </NTooltip>
            </span>
          </div>
          <div v-if="nextCursor >= 0 && hits.length > 0" class="as-more">
            <NButton size="tiny" quaternary :loading="searching" @click="loadMore">
              加载更多
            </NButton>
          </div>
        </NSpin>
      </div>
    </div>

    <template #footer>
      <div class="as-footer">
        <span class="as-footer-hint">
          {{ viewMode === "locate" ? "双击命中项：打开文件并定位到命中内容；F3 / Shift+F3：跳到下一处 / 上一处（不关面板）；通配符 * 匹配任意字符、? 匹配单个字符" : "勾选文件后点「添加到视图」，添加结果在右侧搜索视窗查看" }}
        </span>
        <div class="as-footer-actions">
          <NButton size="small" quaternary :disabled="searching || (!searched && hits.length === 0)" @click="clearAll">
            清空
          </NButton>
          <NButton size="small" quaternary @click="search.close()">关闭</NButton>
        </div>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.as-header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.as-header-icon {
  color: var(--pvf-primary-hover, #7aa2f7);
}
/* 「精确定位 / 添加视图」联结切换：居中、浅灰圆角容器条、按钮独立圆角
   （风格与工具栏归档编辑/脚本工作区一致）。 */
.as-mode-switch {
  margin: 0 auto;
  display: inline-flex;
  gap: 4px;
  padding: 3px;
  background: rgba(127, 127, 127, 0.1);
  border-radius: 10px;
}
.as-mode-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 32px;
  padding: 0 16px;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 8px;
  color: var(--pvf-text-muted, #9aa4b2);
  cursor: pointer;
  font-size: 13px;
  transition: color 0.12s ease, border-color 0.12s ease, background 0.12s ease;
}
.as-mode-btn--active {
  border-color: var(--pvf-primary, #4a9eff);
  color: var(--pvf-primary-hover, #7aa2f7);
  background: var(--pvf-primary-selected, rgba(74, 158, 255, 0.15));
}
.as-row-check {
  flex: 0 0 auto;
}
.as-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.as-search-row {
  display: flex;
  gap: 10px;
}
.as-options-row {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 22px;
}
.as-meta {
  margin-left: auto;
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 12px;
}
.as-error {
  flex: 0 0 auto;
}
.as-results {
  min-height: 240px;
  max-height: min(52vh, 520px);
  overflow: auto;
  border: 1px solid var(--pvf-border-normal, rgba(255, 255, 255, 0.09));
  border-radius: 8px;
  padding: 4px;
}
.as-empty {
  margin-top: 72px;
}
.as-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 8px;
  border-radius: 6px;
  min-height: 26px;
  cursor: default;
}
.as-row:hover {
  background: rgba(127, 127, 127, 0.14);
}
/* 点击行固定选中高亮：与 hover 色区分，便于锁定目标。 */
.as-row--active {
  background: var(--pvf-primary-selected, rgba(74, 158, 255, 0.18));
  box-shadow: inset 2px 0 0 var(--pvf-primary, #4a9eff);
}
/* 四列对齐：前三列固定宽度（状态驱动，全行共享），路径列占剩余宽度。
   列间竖线可左右拉伸（对象视图分隔线同款视觉）。 */
.as-col {
  flex: 0 0 auto;
  overflow: hidden;
  white-space: nowrap;
}
.as-row-type {
  overflow: hidden;
  text-overflow: ellipsis;
}
.as-col-name {
  text-overflow: ellipsis;
  font-size: 12.5px;
  font-weight: 500;
}
.as-col-id {
  display: inline-flex;
  align-items: center;
}
.as-row-path {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 11.5px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.col-resizer {
  flex: 0 0 6px;
  align-self: stretch;
  position: relative;
  cursor: col-resize;
}
.col-resizer::after {
  content: "";
  position: absolute;
  left: 2px;
  top: 3px;
  bottom: 3px;
  width: 2px;
  border-radius: 1px;
  background: var(--pvf-primary, #4a9eff);
  opacity: 0.5;
  transition: opacity 0.12s ease, width 0.12s ease;
}
.col-resizer:hover::after {
  opacity: 1;
  width: 3px;
}
/* 行内操作按钮（打开 + 定位合一）：悬停该行或该行被选中（固定高亮）时显示。
   主色实底 + 白色靶心图标，显眼且与"定位"语义一致。 */
.as-row-actions {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.as-row-btn {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 20px;
  border: none;
  border-radius: 4px;
  background: var(--pvf-primary, #4a9eff);
  color: #fff;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.12s ease, filter 0.12s ease;
}
.as-row:hover .as-row-btn,
.as-row--active .as-row-btn {
  opacity: 1;
}
.as-row-btn:hover {
  filter: brightness(1.18);
}
.as-more {
  display: flex;
  justify-content: center;
  padding: 4px 0 6px;
}
.as-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.as-footer-hint {
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 11px;
}
.as-footer-actions {
  display: flex;
  gap: 8px;
}
</style>
