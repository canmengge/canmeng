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
 * 该格**双击**会打开被引用段（紧跟其后的 [list]）的只读查看器。
 *
 * 本组件**只读**：不写回任何字节，也不改文档。
 */
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import {
  NButton,
  NEmpty,
  NInput,
  NModal,
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
const pageSize = 200;
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

const otherSections = computed<FormViewSection[]>(() =>
  (formView.projection?.sections ?? []).filter(
    (section) =>
      section !== mainSection.value &&
      !linkedSectionKeys.value.has(`${section.section.toLowerCase()}#${section.occurrence}`)
  )
);

const pageCount = computed(() =>
  Math.max(1, Math.ceil((mainSection.value?.rows.length ?? 0) / pageSize))
);

const pagedRows = computed<FormViewRow[]>(() => {
  const rows = mainSection.value?.rows ?? [];
  const start = (page.value - 1) * pageSize;
  return rows.slice(start, start + pageSize);
});

watch(
  () => formView.projection,
  () => {
    page.value = 1;
    // 每次重新解析都回到"按内容自适应"：避免上一次拖出来的宽度把撑开的空白冻住
    // （2026-10-03 事故：中间那一片空白一直消不掉）。
    columnWidths.value = {};
  }
);

function cellText(row: FormViewRow, index: number): { text: string; raw: string } {
  const cell = row.cells[index];
  if (!cell) return { text: "", raw: "" };
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

/** 该格是否是「触发关联」的列（规则里的 link.column）。 */
function isLinkCell(row: FormViewRow, index: number): boolean {
  return !!row.link && row.link.column === index;
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
    return `双击查看关联的「${target}」（第 ${row.link.occurrence} 处）\n原值: ${raw}`;
  }
  if (name !== "") {
    return `${name}\n编号: ${raw}\n双击可改（改的是归档内存，点主工具条「保存 PVF」落盘）`;
  }
  return `原值: ${raw}\n双击可改（改的是归档内存，点主工具条「保存 PVF」落盘）`;
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

// ---- 单元格编辑（双击改值） ----

type EditState = { row: number; column: number; label: string };

const editing = ref<EditState | null>(null);
/** 正在编辑的文本（单独一个 ref，模板里就不必对 editing 判空）。 */
const editText = ref("");
/** 本次会话改过多少格（未落盘）。 */
const editedCount = ref(0);

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
  editText.value = cell.value;
  editing.value = {
    row: row.index,
    column: index,
    label: section.columns[index] ?? "",
  };
  void nextTick();
}

function cancelEdit(): void {
  editing.value = null;
}

async function commitEdit(): Promise<void> {
  const state = editing.value;
  const section = mainSection.value;
  if (!state || !section) return;
  const value = editText.value.trim();
  const original = section.rows[state.row]?.cells[state.column]?.value ?? "";
  if (value === original) {
    editing.value = null;
    return;
  }
  try {
    await formView.applyEdits([
      {
        section: section.section,
        occurrence: section.occurrence,
        row: state.row,
        column: state.column,
        value,
      },
    ]);
    editing.value = null;
    editedCount.value += 1;
    message.success(
      `已改「${state.label}」为 ${value}（归档内存已更新，点主工具条「保存 PVF」落盘）`
    );
  } catch (issue: any) {
    message.error(String(issue?.message ?? issue));
  }
}

function onCellDblClick(row: FormViewRow, index: number, section: FormViewSection): void {
  if (isLinkCell(row, index)) {
    void openLink(row);
    return;
  }
  startEdit(row, index, section);
}

// ---- 列宽左右拉伸 ----

type DragState = { key: string; index: number; startX: number; startWidth: number };
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
  };
  columnWidths.value = { ...columnWidths.value, [section.section]: widths };
  window.addEventListener("pointermove", onResizeMove);
  window.addEventListener("pointerup", onResizeEnd);
}

function onResizeMove(event: PointerEvent): void {
  const state = dragState;
  if (!state) return;
  const next = Math.max(48, Math.round(state.startWidth + (event.clientX - state.startX)));
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
        <NTag size="small" :bordered="false" type="info">只读</NTag>
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
                拖表头右边缘调列宽 · 带 🔗 的格可双击查看关联列表
              </span>
            </div>
            <ul v-if="mainSection.warnings?.length" class="fv-warnings">
              <li v-for="(warning, index) in mainSection.warnings" :key="index">
                {{ warning }}
              </li>
            </ul>

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
                  :class="{ 'fv-row-incomplete': !row.complete }"
                >
                  <td class="fv-td-index">{{ row.index + 1 }}</td>
                  <td
                    v-for="(_, index) in mainSection.columns"
                    :key="index"
                    :class="{
                      'fv-num': isNumeric(cellText(row, index).raw),
                      'fv-link-cell': isLinkCell(row, index),
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
                      @blur="cancelEdit()"
                    />
                    <span
                      v-else
                      class="fv-cell"
                      :style="cellStyle(mainSection, index)"
                      :title="cellText(row, index).text"
                    >
                      <span v-if="cellName(row, index)" class="fv-name">
                        {{ cellName(row, index) }}
                      </span>
                      <span v-else>{{ cellText(row, index).text }}</span>
                      <span v-if="cellName(row, index)" class="fv-id">
                        {{ cellText(row, index).text }}
                      </span>
                      <span v-if="isLinkCell(row, index)" class="fv-link-badge">🔗</span>
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
          已改 {{ editedCount }} 格 · 未落盘（点主工具条「保存 PVF」）
        </template>
        <template v-else>窗口可左右拉伸 · 双击格子可改值</template>
      </span>
    </footer>

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
                    :class="{ 'fv-num': isNumeric(cellText(row, index).raw) }"
                    :title="cellTitle(row, index)"
                  >
                    <span v-if="cellName(row, index)" class="fv-name">
                      {{ cellName(row, index) }}
                    </span>
                    <span v-else>{{ cellText(row, index).text }}</span>
                    <span v-if="cellName(row, index)" class="fv-id">
                      {{ cellText(row, index).text }}
                    </span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="fv-viewer-hint">
            只读查看。候选列表（物品 / 权重）的修改请到「归档编辑」里改这个文件的原文。
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
  max-width: 100%;
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
