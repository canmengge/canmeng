<script setup lang="ts">
import { computed, ref } from "vue";
import { NInput } from "naive-ui";
import type { PreviewFile } from "../../previews/types";

const props = defineProps<{
  file: PreviewFile;
  active: boolean;
}>();

interface ListEntry {
  id: string;
  path: string;
}

/** 解析 .lst 文本：每行一条 `ID<TAB>路径`（路径可能被反引号包裹）。 */
function parseEntries(text: string): ListEntry[] {
  const entries: ListEntry[] = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line) continue;
    const idx = line.indexOf("\t");
    let id = "";
    let path = "";
    if (idx > 0) {
      id = line.slice(0, idx).trim();
      path = line.slice(idx + 1).trim();
    } else {
      const sp = line.indexOf(" ");
      if (sp <= 0) continue;
      id = line.slice(0, sp).trim();
      path = line.slice(sp + 1).trim();
    }
    if (!id || !path) continue;
    entries.push({ id, path: path.replace(/^`|`$/g, "").trim() });
  }
  return entries;
}

const allEntries = computed<ListEntry[]>(() => parseEntries(props.file.text ?? ""));
const filter = ref("");
const filtered = computed<ListEntry[]>(() => {
  const keyword = filter.value.trim().toLowerCase();
  if (!keyword) return allEntries.value;
  return allEntries.value.filter(
    (entry) => entry.id.toLowerCase().includes(keyword) || entry.path.toLowerCase().includes(keyword),
  );
});
</script>

<template>
  <div class="list-preview">
    <div class="list-preview-toolbar">
      <span class="list-preview-count">{{ allEntries.length }} 条</span>
      <NInput
        v-model:value="filter"
        size="small"
        clearable
        class="list-preview-filter"
        placeholder="筛选 ID 或路径"
      />
    </div>
    <div v-if="allEntries.length === 0" class="list-preview-empty">等待有效的清单内容…</div>
    <div v-else class="list-preview-table">
      <div class="list-preview-head">
        <span class="list-preview-col-id">ID</span>
        <span class="list-preview-col-path">路径</span>
      </div>
      <div class="list-preview-body">
        <div
          v-for="(entry, index) in filtered"
          :key="`${entry.id}:${index}`"
          class="list-preview-row"
        >
          <span class="list-preview-col-id">{{ entry.id }}</span>
          <span class="list-preview-col-path">{{ entry.path }}</span>
        </div>
        <div v-if="filtered.length === 0" class="list-preview-empty">无匹配条目</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.list-preview {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  padding: 7px 8px;
  gap: 6px;
  font-size: 11px;
}
.list-preview-toolbar {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
}
.list-preview-count {
  flex: 0 0 auto;
  color: var(--pvf-text-secondary);
}
.list-preview-filter {
  flex: 1 1 auto;
  min-width: 0;
}
.list-preview-empty {
  display: flex;
  flex: 1 1 auto;
  min-height: 80px;
  align-items: center;
  justify-content: center;
  color: var(--pvf-text-muted);
  background: var(--pvf-surface-inset);
  border: 1px dashed var(--pvf-border-subtle);
  border-radius: 5px;
}
.list-preview-table {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 5px;
}
.list-preview-head,
.list-preview-row {
  display: flex;
  align-items: center;
  min-width: 0;
  gap: 8px;
  padding: 3px 8px;
}
.list-preview-head {
  flex: 0 0 auto;
  color: var(--pvf-text-secondary);
  font-weight: 600;
  background: var(--pvf-surface-subtle);
  border-bottom: 1px solid var(--pvf-border-subtle);
}
.list-preview-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}
.list-preview-row:nth-child(even) {
  background: var(--pvf-surface-inset);
}
.list-preview-col-id {
  flex: 0 0 84px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--pvf-warning);
}
.list-preview-col-path {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--pvf-text-primary);
}
</style>
