<script setup lang="ts">
/**
 * 结构化视图（只读）。
 *
 * 按外部规则（config/formats.json）把**一个文件**投影成「段 → 行 → 列」表格，
 * 例如 etc/independent_drop.etc 的 17 列掉落配置行。
 *
 * 本组件**只读**：不写回任何字节，也不改文档。写回是后续步骤。
 */
import { computed, onMounted, ref, watch } from "vue";
import { NButton, NEmpty, NInput, NSelect, NSpin, NTag } from "naive-ui";
import { useFormViewStore } from "../stores/formView";
import type { FormViewRow, FormViewSection } from "../services/formViewApi";

const formView = useFormViewStore();

/** 每页行数（主表分页，避免一次渲染上万行）。 */
const pageSize = 200;
const page = ref(1);

/** 折叠段里最多先显示多少行。 */
const inlineRowLimit = 50;

onMounted(() => {
  void formView.loadFormats();
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

watch(
  () => formView.projection,
  () => {
    page.value = 1;
  }
);

function cellText(row: FormViewRow, index: number): { text: string; raw: string } {
  const cell = row.cells[index];
  if (!cell) return { text: "", raw: "" };
  return { text: cell.display && cell.display !== "" ? cell.display : cell.value, raw: cell.value };
}

function sectionTitle(section: FormViewSection): string {
  const suffix = section.occurrence > 1 ? ` #${section.occurrence}` : "";
  return `${section.label || section.section}${suffix}`;
}
</script>

<template>
  <div class="fv-panel">
    <div class="fv-header">
      <div class="fv-title">结构化视图</div>
      <NTag size="small" :bordered="false" type="info">只读</NTag>
    </div>

    <div v-if="!formView.ready" class="fv-hint">
      <NEmpty size="small" description="先打开一个 PVF 归档，再解析文件" />
    </div>

    <template v-else>
      <div class="fv-controls">
        <NSelect
          v-model:value="formView.formatId"
          :options="formView.formatOptions"
          :loading="formView.formatsLoading"
          size="small"
          placeholder="选择文件族"
        />
        <div class="fv-path-row">
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
        </div>
        <div class="fv-actions">
          <NButton size="tiny" quaternary :loading="formView.formatsLoading" @click="formView.reloadRules()">
            重新读规则
          </NButton>
          <span class="fv-rule-path" :title="formView.rulePath">规则：{{ formView.rulePath }}</span>
        </div>
        <div v-if="formView.currentFormat?.notes" class="fv-notes">
          {{ formView.currentFormat.notes }}
        </div>
      </div>

      <div v-if="formView.formatsError" class="fv-error">{{ formView.formatsError }}</div>
      <div v-if="formView.error" class="fv-error">{{ formView.error }}</div>

      <NSpin :show="formView.projecting">
        <template v-if="formView.projection">
          <div class="fv-stats">
            <span>{{ formView.projection.formatLabel }}</span>
            <span class="fv-sep">·</span>
            <span>{{ formView.projection.file }}</span>
            <span class="fv-sep">·</span>
            <span>{{ formView.projection.tokenCount }} 个 token</span>
            <span class="fv-sep">·</span>
            <span>{{ formView.projection.sections.length }} 个段</span>
          </div>

          <ul v-if="formView.projection.warnings.length" class="fv-warnings">
            <li v-for="(warning, index) in formView.projection.warnings" :key="index">{{ warning }}</li>
          </ul>

          <template v-if="mainSection">
            <div class="fv-section-head">
              <span class="fv-section-title">{{ sectionTitle(mainSection) }}</span>
              <span class="fv-section-meta">
                {{ mainSection.rows.length }} 行 × {{ mainSection.columns.length }} 列
              </span>
            </div>
            <ul v-if="mainSection.warnings?.length" class="fv-warnings">
              <li v-for="(warning, index) in mainSection.warnings" :key="index">{{ warning }}</li>
            </ul>

            <div class="fv-table-wrap">
              <table class="fv-table">
                <thead>
                  <tr>
                    <th class="fv-th-index">#</th>
                    <th v-for="(column, index) in mainSection.columns" :key="index">{{ column }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="row in pagedRows" :key="row.index" :class="{ 'fv-row-incomplete': !row.complete }">
                    <td class="fv-td-index">{{ row.index + 1 }}</td>
                    <td v-for="(_, index) in mainSection.columns" :key="index" :title="'原值: ' + cellText(row, index).raw">
                      {{ cellText(row, index).text }}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div v-if="pageCount > 1" class="fv-pager">
              <NButton size="tiny" :disabled="page <= 1" @click="page -= 1">上一页</NButton>
              <span class="fv-pager-text">第 {{ page }} / {{ pageCount }} 页</span>
              <NButton size="tiny" :disabled="page >= pageCount" @click="page += 1">下一页</NButton>
            </div>
          </template>

          <div v-if="otherSections.length" class="fv-others">
            <div class="fv-section-head">
              <span class="fv-section-title">其它段</span>
              <span class="fv-section-meta">{{ otherSections.length }} 块</span>
            </div>
            <details v-for="(section, index) in otherSections" :key="index" class="fv-block">
              <summary>
                {{ sectionTitle(section) }}
                <span class="fv-section-meta">{{ section.rows.length }} 行</span>
              </summary>
              <table class="fv-table fv-table--compact">
                <thead>
                  <tr>
                    <th v-for="(column, columnIndex) in section.columns" :key="columnIndex">{{ column }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="row in section.rows.slice(0, inlineRowLimit)" :key="row.index">
                    <td v-for="(_, columnIndex) in section.columns" :key="columnIndex" :title="'原值: ' + cellText(row, columnIndex).raw">
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
    </template>
  </div>
</template>

<style scoped>
.fv-panel {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
  overflow: hidden;
  padding: 10px 10px 0;
  color: var(--pvf-text-primary);
}

.fv-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.fv-title {
  font-size: 13px;
  font-weight: 600;
}

.fv-controls {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.fv-path-row {
  display: flex;
  gap: 6px;
}

.fv-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.fv-rule-path {
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
  padding: 6px 8px;
}

.fv-error {
  font-size: 12px;
  color: var(--pvf-text-primary);
  background: var(--pvf-surface-error);
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 4px;
  padding: 6px 8px;
  word-break: break-all;
}

.fv-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 11px;
  color: var(--pvf-text-secondary);
}

.fv-sep {
  color: var(--pvf-text-faint);
}

.fv-warnings {
  margin: 0;
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
  margin-top: 4px;
}

.fv-section-title {
  font-size: 12px;
  font-weight: 600;
}

.fv-section-meta {
  font-size: 11px;
  color: var(--pvf-text-muted);
}

.fv-table-wrap {
  flex: 1 1 auto;
  min-height: 120px;
  overflow: auto;
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 4px;
  background: var(--pvf-surface-panel);
}

.fv-table {
  border-collapse: collapse;
  font-size: 11px;
  width: max-content;
  min-width: 100%;
}

.fv-table th,
.fv-table td {
  border-bottom: 1px solid var(--pvf-border-faint);
  border-right: 1px solid var(--pvf-border-faint);
  padding: 2px 6px;
  text-align: left;
  white-space: nowrap;
}

.fv-table thead th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--pvf-surface-elevated);
  color: var(--pvf-text-secondary);
  font-weight: 600;
}

.fv-td-index,
.fv-th-index {
  color: var(--pvf-text-faint);
  text-align: right;
}

.fv-row-incomplete {
  background: var(--pvf-surface-warning);
}

.fv-table--compact th,
.fv-table--compact td {
  padding: 1px 5px;
}

.fv-pager {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 0;
}

.fv-pager-text {
  font-size: 11px;
  color: var(--pvf-text-secondary);
}

.fv-others {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-bottom: 10px;
}

.fv-block {
  border: 1px solid var(--pvf-border-faint);
  border-radius: 4px;
  padding: 4px 6px;
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

.fv-hint {
  padding: 12px 0;
}
</style>
