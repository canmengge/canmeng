<script setup lang="ts">
/**
 * 结构化视图（只读）。
 *
 * 按外部规则（config/formats.json）把**一个文件**投影成「段 → 行 → 列」表格，
 * 例如 etc/independent_drop.etc 的 17 列掉落配置行。
 *
 * 两处运行位置共用本组件：
 *   - 侧栏面板（主窗口内，`detached` 为 false）
 *   - **独立窗口**（`?view=formview`，`detached` 为 true）—— 17 列在侧栏里太挤，
 *     独立窗口可以左右拉宽。
 *
 * 布局要点（修掉"不能下滑"）：头/工具栏/页脚固定，**中间只有一个滚动区**
 * （`.fv-body`），且 flex 链上每层都写 `min-height: 0` —— flex 子项默认
 * `min-height: auto`，不写就撑不出滚动条。
 *
 * 本组件**只读**：不写回任何字节，也不改文档。
 */
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { NButton, NEmpty, NInput, NSelect, NSpin, NTag } from "naive-ui";
import { useFormViewStore } from "../stores/formView";
import type { FormViewRow, FormViewSection } from "../services/formViewApi";

const props = withDefaults(defineProps<{ detached?: boolean }>(), {
  detached: false,
});

const formView = useFormViewStore();

/** 主表分页大小（一次渲染上万行会拖慢界面）。 */
const pageSize = 200;
const page = ref(1);
/** 折叠段里最多先渲染多少行。 */
const inlineRowLimit = 50;

/** 列宽：段名 → 各列像素宽（0 表示"自动"）。 */
const columnWidths = ref<Record<string, number[]>>({});

onMounted(() => {
  void formView.loadFormats();
  if (!props.detached) void formView.refreshWindowOpen();
});

onUnmounted(() => {
  stopResize();
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

const otherSections = computed<FormViewSection[]>(() =>
  (formView.projection?.sections ?? []).filter((section) => section !== mainSection.value)
);

const pageCount = computed(() =>
  Math.max(1, Math.ceil((mainSection.value?.rows.length ?? 0) / pageSize))
);

const pagedRows = computed<FormViewRow[]>(() => {
  const rows = mainSection.value?.rows ?? [];
  const start = (page.value - 1) * pageSize;
  return rows.slice(start, start + pageSize);
});

const openInWindowLabel = computed(() =>
  formView.windowOpen ? "切到独立窗口" : "在独立窗口打开"
);

watch(
  () => formView.projection,
  () => {
    page.value = 1;
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

// ---- 列宽左右拉伸 ----

type DragState = { key: string; index: number; startX: number; startWidth: number };
let dragState: DragState | null = null;

function columnStyle(section: FormViewSection, index: number) {
  const width = columnWidths.value[section.section]?.[index] ?? 0;
  return width > 0 ? { width: `${width}px`, minWidth: `${width}px` } : undefined;
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
  <div class="fv-root" :class="{ 'fv-root--detached': props.detached }">
    <!-- ① 标题栏（固定） -->
    <header class="fv-head">
      <div class="fv-head-title">
        <span class="fv-title">结构化视图</span>
        <NTag size="small" :bordered="false" type="info">只读</NTag>
      </div>
      <div class="fv-head-actions">
        <NButton size="tiny" quaternary @click="resetColumnWidths">重置列宽</NButton>
        <NButton
          v-if="!props.detached"
          size="tiny"
          type="primary"
          secondary
          @click="formView.openInWindow()"
        >
          {{ openInWindowLabel }}
        </NButton>
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
      <div v-if="formView.currentFormat?.notes" class="fv-notes">
        {{ formView.currentFormat.notes }}
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
                拖表头右边缘可调列宽
              </span>
            </div>
            <ul v-if="mainSection.warnings?.length" class="fv-warnings">
              <li v-for="(warning, index) in mainSection.warnings" :key="index">
                {{ warning }}
              </li>
            </ul>

            <table class="fv-table">
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
                    :class="{ 'fv-num': isNumeric(cellText(row, index).raw) }"
                    :title="'原值: ' + cellText(row, index).raw"
                  >
                    {{ cellText(row, index).text }}
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
              <table class="fv-table fv-table--compact">
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
      </span>
      <div class="fv-pager" v-if="pageCount > 1">
        <NButton size="tiny" :disabled="page <= 1" @click="page -= 1">上一页</NButton>
        <span class="fv-pager-text">{{ page }} / {{ pageCount }}</span>
        <NButton size="tiny" :disabled="page >= pageCount" @click="page += 1">下一页</NButton>
      </div>
      <span v-if="props.detached" class="fv-foot-hint">窗口可左右拉伸</span>
    </footer>
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

.fv-root--detached {
  padding: 0 2px;
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

/* 表格：sticky 表头要求 border-collapse: separate（collapse 下 sticky 会掉边框） */
.fv-table {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 11px;
  width: max-content;
  min-width: 100%;
}

.fv-table th,
.fv-table td {
  border-bottom: 1px solid var(--pvf-border-faint);
  border-right: 1px solid var(--pvf-border-faint);
  padding: 3px 8px;
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
  /* 表头右侧留出拖拽手柄的位置 */
  padding-right: 10px;
}

.fv-th-text {
  display: inline-block;
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: bottom;
}

/* 列宽拖拽手柄 */
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

/* 手柄定位需要 th 作为定位父级 */
.fv-table thead th {
  position: sticky;
  top: 0;
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
</style>
