<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  NAlert,
  NButton,
  NCheckbox,
  NEmpty,
  NIcon,
  NInput,
  NModal,
  NSpin,
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
      window.setTimeout(() => inputEl.value?.focus(), 60);
    }
  }
);

function resetResults(): void {
  hits.value = [];
  nextCursor.value = -1;
  searched.value = false;
  checkedKeys.value = new Set();
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
    const result = exact.value
      ? await ArchiveService.SearchExact(term, 0, 200)
      : await ArchiveService.Search(term, 0, 200);
    hits.value = (result?.hits ?? []).map((hit: SearchHit | null) => explorer.toSearchItem(hit as SearchHit));
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
    await editor.openFile(item.fileIndex);
    await explorer.revealPath(item.path);
    search.close();
  } catch (e: any) {
    error.value = String(e?.message ?? e);
  }
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
          <NCheckbox v-model:checked="exact" size="small" :disabled="searching">
            精确匹配（整词相等，不再做子串匹配）
          </NCheckbox>
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
          {{ viewMode === "locate" ? "双击命中项打开文件并定位到左树；通配符 * 匹配任意字符、? 匹配单个字符" : "勾选文件后点「添加到视图」，添加结果在右侧搜索视窗查看" }}
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
