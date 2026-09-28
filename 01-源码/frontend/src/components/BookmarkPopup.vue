<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { useMessage } from "naive-ui";
import { useArchiveStore } from "../stores/archive";
import { useBookmarkStore, type BookmarkEntry, type BookmarkGroup } from "../stores/bookmarks";
import { useEditorStore } from "../stores/editor";
import { useExplorerStore } from "../stores/explorer";

const props = defineProps<{
  show: boolean;
  anchorEl: HTMLElement | null;
}>();
const emit = defineEmits<{ (e: "update:show", value: boolean): void }>();

const archive = useArchiveStore();
const bookmarks = useBookmarkStore();
const editor = useEditorStore();
const explorer = useExplorerStore();
const message = useMessage();

const popupStyle = ref<{ top: string; left: string }>({ top: "0px", left: "0px" });

/** 级联栏：每一栏是「上一栏所选分组」的下一级内容（子分组 + 文件）。 */
type ColumnItem =
  | { kind: "group"; group: BookmarkGroup }
  | { kind: "entry"; entry: BookmarkEntry };

const columns = ref<ColumnItem[][]>([]);
/** 每一栏被点开的分组（用于面包屑与选中高亮）。 */
const selectedPath = ref<BookmarkGroup[]>([]);

const activeBook = computed(() => bookmarks.activeBook);
const rootGroups = computed(() => activeBook.value?.groups ?? []);

const COLUMN_WIDTH = 172;
const MIN_WIDTH = 360;
const MAX_WIDTH = 1100;
const MIN_HEIGHT = 180;
const MAX_HEIGHT = 720;

/** 用户拖动后的自定义尺寸：默认 368×369（用户 2026-09-26 指定）。 */
const customWidth = ref(368);
const customHeight = ref(369);
const viewportWidth = ref(window.innerWidth);
const colsRef = ref<HTMLElement | null>(null);

/**
 * 栏数变化时自动向右拓展：宽度必须至少容纳全部栏（否则新栏会被裁剪），
 * 再受屏幕宽度限制；用户拖过宽度时以「用户宽度」与「所需宽度」的较大者为准。
 */
const autoWidth = computed(() => 24 + columns.value.length * COLUMN_WIDTH);
const popupWidth = computed(() => {
  const base =
    customWidth.value > 0 ? Math.max(customWidth.value, autoWidth.value) : autoWidth.value;
  return Math.min(base, viewportMaxWidth());
});

const totalEntries = computed(() => {
  let total = 0;
  const walk = (groups: BookmarkGroup[]) => {
    for (const group of groups) {
      total += group.entries.length;
      walk(group.groups);
    }
  };
  for (const book of bookmarks.books) {
    total += book.entries.length;
    walk(book.groups);
  }
  return total;
});

/** 分组的下一级内容：文件在前、子分组在后（更快看到文件）。 */
function itemsOf(group: BookmarkGroup): ColumnItem[] {
  return [
    ...group.entries.map((entry) => ({ kind: "entry" as const, entry })),
    ...group.groups.map((child) => ({ kind: "group" as const, group: child })),
  ];
}

function resetColumns(): void {
  columns.value = [rootGroups.value.map((group) => ({ kind: "group" as const, group }))];
  selectedPath.value = [];
}

/** 点击某栏分组：截断其后所有栏，并在右侧新开一栏展示它的下一级内容。 */
function openGroup(group: BookmarkGroup, columnIndex: number): void {
  selectedPath.value = [...selectedPath.value.slice(0, columnIndex), group];
  columns.value = [...columns.value.slice(0, columnIndex + 1), itemsOf(group)];
}

function jumpCrumb(index: number): void {
  selectedPath.value = selectedPath.value.slice(0, index + 1);
  columns.value = columns.value.slice(0, index + 2);
}

/** 切换书签簿标签页。 */
function switchBook(id: string): void {
  if (id === bookmarks.activeBookId) return;
  bookmarks.setActiveBook(id);
  selectedPath.value = [];
  columns.value = [rootGroups.value.map((group) => ({ kind: "group" as const, group }))];
}

function close(): void {
  endResize();
  emit("update:show", false);
}

/* ---------------- 书签簿操作：重命名 / 导入 / 导出 ---------------- */

const renaming = ref(false);
const renameValue = ref("");
const renameInputEl = ref<HTMLInputElement | null>(null);

function startRename(): void {
  const book = bookmarks.activeBook;
  if (!book) return;
  if (!book.editable) {
    message.warning("内置书签簿不可重命名，请先复制副本");
    return;
  }
  renameValue.value = book.name;
  renaming.value = true;
  void nextTick(() => renameInputEl.value?.focus());
}

function commitRename(): void {
  if (!renaming.value) return;
  renaming.value = false;
  const book = bookmarks.activeBook;
  if (!book) return;
  const name = renameValue.value.trim();
  if (!name || name === book.name) return;
  try {
    bookmarks.renameBook(book.id, name);
    message.success("书签簿已重命名");
  } catch (e: any) {
    message.error(String(e?.message ?? e));
  }
}

function isCancelled(text: string): boolean {
  const lower = text.toLowerCase();
  return lower.includes("cancel") || text.includes("已取消");
}

async function onImport(): Promise<void> {
  try {
    const book = await bookmarks.importBook();
    if (!book) {
      message.info("已取消导入");
      return;
    }
    message.success(`已导入书签簿「${book.name}」`);
    resetColumns();
  } catch (e: any) {
    const text = String(e?.message ?? e);
    if (!isCancelled(text)) message.error(`导入书签簿失败: ${text}`);
  }
}

async function onExport(): Promise<void> {
  try {
    const path = await bookmarks.exportActiveBook();
    if (!path) {
      message.info("已取消导出");
      return;
    }
    message.success(`已导出到: ${path}`);
  } catch (e: any) {
    const text = String(e?.message ?? e);
    if (!isCancelled(text)) message.error(`导出书签簿失败: ${text}`);
  }
}

function openEntry(entry: BookmarkEntry): void {
  if (!archive.open) {
    message.info("请先打开一个 PVF 归档");
    return;
  }
  if (bookmarks.resolving) {
    message.info("正在匹配当前归档中的书签");
    return;
  }
  if (entry.isDir) {
    void explorer.revealPath(entry.path).then((found) => {
      if (!found) message.warning(`当前归档中不存在文件夹: ${entry.path}`);
    });
    return;
  }
  if (entry.fileIndex < 0) {
    message.warning(`当前归档中不存在文件: ${entry.path}`);
    return;
  }
  void editor.openFile(entry.fileIndex);
  // 文件书签点开后即收起浮层：视线本来就跟着文件走，留着浮层只会挡编辑区。
  emit("update:show", false);
}

/**
 * 双击文件书签：打开文件，并把它在左侧文件树里展开定位（与高级搜索的
 * 「打开并定位」同一套动作）。单击仍只做"打开文件"。
 */
async function openEntryAndReveal(entry: BookmarkEntry): Promise<void> {
  openEntry(entry);
  if (!archive.open || entry.isDir || entry.fileIndex < 0 || bookmarks.resolving) return;
  try {
    // 左侧处于搜索结果态时先退回文件树态，否则定位不可见。
    if (explorer.mode === "search") explorer.clearSearch();
    const found = await explorer.revealPath(entry.path);
    if (!found) message.warning(`未在资源管理器中找到: ${entry.path}`);
  } catch (error: any) {
    message.error(`定位文件失败: ${error?.message ?? error}`);
  } finally {
    // 定位完同样收起浮层，让视线直接落到左侧已展开的文件上。
    emit("update:show", false);
  }
}

/* ---------------- 拖动调整尺寸（右/下/右下角，上下左右实时响应） ---------------- */

const resizing = ref(false);
let resizeMode: "x" | "y" | "xy" = "xy";
let resizeStart = { x: 0, y: 0, width: 0, height: 0 };

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

function viewportMaxWidth(): number {
  return Math.min(MAX_WIDTH, viewportWidth.value - 24);
}

function viewportMaxHeight(): number {
  // 顶部偏移 + 头部/标签/面包屑/底部约 130px，再留 16px 余量
  return Math.max(MIN_HEIGHT, Math.min(MAX_HEIGHT, window.innerHeight - 200));
}

function onViewportResize(): void {
  viewportWidth.value = window.innerWidth;
}

function startResize(event: MouseEvent, mode: "x" | "y" | "xy"): void {
  event.preventDefault();
  event.stopPropagation();
  resizing.value = true;
  resizeMode = mode;
  resizeStart = {
    x: event.clientX,
    y: event.clientY,
    width: popupWidth.value,
    height: customHeight.value,
  };
  window.addEventListener("mousemove", onResizeMove);
  window.addEventListener("mouseup", endResize);
}

function onResizeMove(event: MouseEvent): void {
  if (!resizing.value) return;
  const dx = event.clientX - resizeStart.x;
  const dy = event.clientY - resizeStart.y;
  if (resizeMode !== "y") {
    customWidth.value = clamp(resizeStart.width + dx, MIN_WIDTH, viewportMaxWidth());
  }
  if (resizeMode !== "x") {
    customHeight.value = clamp(resizeStart.height + dy, MIN_HEIGHT, viewportMaxHeight());
  }
}

function endResize(): void {
  resizing.value = false;
  window.removeEventListener("mousemove", onResizeMove);
  window.removeEventListener("mouseup", endResize);
}

watch(
  () => props.show,
  (show) => {
    if (!show) {
      endResize();
      return;
    }
    if (!bookmarks.loaded || rootGroups.value.length === 0) {
      void bookmarks.load(rootGroups.value.length === 0);
    }
    resetColumns();
    // 尺寸随屏幕收敛，避免小屏溢出
    customWidth.value = clamp(customWidth.value || 0, 0, viewportMaxWidth());
    customHeight.value = clamp(customHeight.value, MIN_HEIGHT, viewportMaxHeight());
    if (props.anchorEl) {
      const rect = props.anchorEl.getBoundingClientRect();
      const width = popupWidth.value;
      let left = rect.left;
      if (left + width > window.innerWidth - 12) {
        left = Math.max(12, window.innerWidth - width - 12);
      }
      popupStyle.value = { top: `${rect.bottom + 8}px`, left: `${left}px` };
    }
  }
);

// 书签数据异步到达后重建第一栏
watch(rootGroups, () => {
  if (props.show) resetColumns();
});

// 每新增一栏后自动滚到最右，保证新栏立即可见
watch(
  () => columns.value.length,
  async () => {
    await nextTick();
    const el = colsRef.value;
    if (el) el.scrollLeft = el.scrollWidth;
  }
);

function onKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape" && props.show) close();
}
onMounted(() => {
  window.addEventListener("keydown", onKeydown);
  window.addEventListener("resize", onViewportResize);
});
onUnmounted(() => {
  window.removeEventListener("keydown", onKeydown);
  window.removeEventListener("resize", onViewportResize);
  endResize();
});
</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="bm-mask" @click="close" />
    <div
      v-if="show"
      class="bm-popup"
      :class="{ 'bm-popup--resizing': resizing }"
      :style="{ ...popupStyle, width: popupWidth + 'px' }"
      @click.stop
    >
      <div class="bm-head">
        <span class="bm-title">🔖</span>
        <template v-if="renaming">
          <input
            ref="renameInputEl"
            v-model="renameValue"
            class="bm-rename-input"
            maxlength="40"
            @keydown.enter.prevent="commitRename"
            @keydown.esc="renaming = false"
            @blur="commitRename"
          />
        </template>
        <template v-else>
          <span class="bm-title-text" :title="activeBook?.name ?? '书签'">
            {{ activeBook?.name ?? "书签" }}
          </span>
          <span class="bm-act" title="重命名当前书签簿" @click="startRename">✏️</span>
        </template>
        <span class="bm-act" title="导入书签簿" @click="onImport">📥</span>
        <span class="bm-act" title="导出当前书签簿" @click="onExport">📤</span>
        <span class="bm-close" title="关闭" @click="close">✕</span>
      </div>

      <!-- 书签簿标签页 -->
      <div class="bm-tabs" role="tablist">
        <button
          v-for="book in bookmarks.books"
          :key="book.id"
          type="button"
          role="tab"
          class="bm-tab"
          :class="{ on: book.id === bookmarks.activeBookId }"
          :aria-selected="book.id === bookmarks.activeBookId"
          @click="switchBook(book.id)"
        >
          {{ book.name }}
        </button>
      </div>

      <div v-if="selectedPath.length" class="bm-crumbbar">
        <template v-for="(crumb, index) in selectedPath" :key="crumb.id">
          <b @click="jumpCrumb(index)">{{ crumb.name }}</b>
          <span v-if="index < selectedPath.length - 1"> › </span>
        </template>
      </div>

      <div ref="colsRef" class="bm-cols" :style="{ height: customHeight + 'px' }">
        <div v-for="(column, columnIndex) in columns" :key="columnIndex" class="bm-col">
          <div
            v-for="(item, itemIndex) in column"
            :key="itemIndex"
            class="bm-row"
            :class="{
              on: item.kind === 'group' && selectedPath[columnIndex]?.id === item.group.id,
            }"
            @click="
              item.kind === 'group' ? openGroup(item.group, columnIndex) : openEntry(item.entry)
            "
            @dblclick="
              item.kind === 'group'
                ? openGroup(item.group, columnIndex)
                : openEntryAndReveal(item.entry)
            "
          >
            <span class="fi">{{ item.kind === "group" ? "📁" : "📄" }}</span>
            <span class="n">{{ item.kind === "group" ? item.group.name : item.entry.name }}</span>
            <span v-if="item.kind === 'group'" class="arw">›</span>
          </div>
          <div v-if="column.length === 0" class="bm-empty">（空分组）</div>
        </div>
      </div>

      <div class="bm-foot">
        <span>共 {{ totalEntries }} 个书签 · 点 📁 逐级往右 · 点 📄 打开文件 · 双击 📄 定位到左侧文件树</span>
        <span class="bm-foot-size">{{ Math.round(popupWidth) }} × {{ customHeight }}</span>
      </div>

      <!-- 拖动手柄：右 / 下 / 右下角 -->
      <div class="bm-resize bm-resize-e" title="拖动调整宽度" @mousedown="startResize($event, 'x')" />
      <div class="bm-resize bm-resize-s" title="拖动调整高度" @mousedown="startResize($event, 'y')" />
      <div class="bm-resize bm-resize-se" title="拖动调整宽高" @mousedown="startResize($event, 'xy')" />
    </div>
  </Teleport>
</template>

<style scoped>
.bm-mask {
  position: fixed;
  inset: 0;
  z-index: 3000;
}
.bm-popup {
  position: fixed;
  display: flex;
  flex-direction: column;
  background: #242832;
  border: 1px solid rgba(128, 128, 128, 0.22);
  border-radius: 10px;
  box-shadow: 0 18px 46px rgba(0, 0, 0, 0.6);
  z-index: 3001;
  overflow: hidden;
  color: rgba(255, 255, 255, 0.92);
}
/* 拖动中禁用文本选中与过渡，保证实时响应不抖动 */
.bm-popup--resizing {
  user-select: none;
}
.bm-head {
  height: 32px;
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 11px;
  font-size: 12.5px;
  font-weight: 700;
}

/* 书签簿标签页 */
.bm-tabs {
  flex: none;
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 0 8px;
  border-bottom: 1px solid rgba(128, 128, 128, 0.16);
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: thin;
}
.bm-tab {
  height: 27px;
  flex: none;
  padding: 0 12px;
  margin-bottom: -1px;
  border: 1px solid transparent;
  border-bottom: none;
  border-radius: 7px 7px 0 0;
  background: transparent;
  color: rgba(255, 255, 255, 0.6);
  font-family: inherit;
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
  transition: background 120ms ease, color 120ms ease;
}
.bm-tab:hover {
  background: rgba(79, 140, 255, 0.09);
  color: rgba(255, 255, 255, 0.9);
}
.bm-tab.on {
  background: rgba(79, 140, 255, 0.15);
  border-color: rgba(79, 140, 255, 0.35);
  color: #cfe0ff;
}

.bm-close {
  margin-left: auto;
  color: rgba(255, 255, 255, 0.42);
  font-size: 13px;
  cursor: pointer;
  padding: 0 4px;
}
.bm-close:hover {
  color: rgba(255, 255, 255, 0.9);
}
/* 头部小操作按钮（重命名 / 导入 / 导出） */
.bm-act {
  flex: none;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  opacity: 0.55;
  padding: 3px 4px;
  border-radius: 4px;
  transition: opacity 120ms ease, background 120ms ease;
}
.bm-act:hover {
  opacity: 1;
  background: rgba(79, 140, 255, 0.16);
}
.bm-title-text {
  font-weight: 700;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 180px;
}
.bm-rename-input {
  flex: 0 1 170px;
  min-width: 0;
  height: 22px;
  padding: 0 6px;
  font-family: inherit;
  font-size: 12px;
  color: rgba(255, 255, 255, 0.92);
  background: rgba(0, 0, 0, 0.28);
  border: 1px solid rgba(79, 140, 255, 0.5);
  border-radius: 4px;
  outline: none;
}
.bm-crumbbar {
  flex: none;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 11px;
  font-size: 11px;
  color: rgba(255, 255, 255, 0.42);
  border-bottom: 1px solid rgba(128, 128, 128, 0.16);
  white-space: nowrap;
  overflow: hidden;
}
.bm-crumbbar b {
  color: #bcd4ff;
  font-weight: 600;
  cursor: pointer;
}
.bm-crumbbar b:hover {
  text-decoration: underline;
}
.bm-cols {
  display: flex;
  min-height: 0;
  flex: none;
  max-height: calc(100vh - 200px);
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: thin;
}
.bm-col {
  width: 172px;
  flex: none;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 3px 4px;
  border-right: 1px solid rgba(128, 128, 128, 0.16);
}
.bm-col:last-child {
  border-right: none;
}
.bm-row {
  display: flex;
  align-items: center;
  gap: 7px;
  height: 23px;
  padding: 0 9px;
  border-radius: 5px;
  font-size: 12.5px;
  color: rgba(255, 255, 255, 0.78);
  white-space: nowrap;
  cursor: pointer;
}
.bm-row:hover {
  background: rgba(79, 140, 255, 0.09);
}
.bm-row.on {
  background: rgba(79, 140, 255, 0.15);
  color: #cfe0ff;
}
.bm-row .fi {
  font-size: 13px;
  line-height: 1;
  flex: none;
}
.bm-row .n {
  overflow: hidden;
  text-overflow: ellipsis;
}
.bm-row .arw {
  margin-left: auto;
  font-size: 10px;
  color: rgba(255, 255, 255, 0.42);
  flex: none;
}
.bm-empty {
  padding: 8px 10px;
  font-size: 11.5px;
  color: rgba(255, 255, 255, 0.42);
}
.bm-foot {
  height: 26px;
  flex: none;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 11px;
  border-top: 1px solid rgba(128, 128, 128, 0.16);
  font-size: 11px;
  color: rgba(255, 255, 255, 0.42);
}
.bm-foot-size {
  margin-left: auto;
  font-family: ui-monospace, Consolas, monospace;
  font-size: 10.5px;
  opacity: 0.75;
}

/* 拖动手柄 */
.bm-resize {
  position: absolute;
  z-index: 6;
}
.bm-resize-e {
  top: 0;
  right: 0;
  width: 5px;
  height: 100%;
  cursor: ew-resize;
}
.bm-resize-e:hover {
  background: rgba(79, 140, 255, 0.35);
}
.bm-resize-s {
  left: 0;
  bottom: 0;
  height: 5px;
  width: 100%;
  cursor: ns-resize;
}
.bm-resize-s:hover {
  background: rgba(79, 140, 255, 0.35);
}
.bm-resize-se {
  right: 0;
  bottom: 0;
  width: 14px;
  height: 14px;
  cursor: nwse-resize;
  background: linear-gradient(
    135deg,
    transparent 45%,
    rgba(255, 255, 255, 0.28) 45%,
    rgba(255, 255, 255, 0.28) 55%,
    transparent 55%
  );
}
.bm-resize-se:hover {
  background: rgba(79, 140, 255, 0.5);
}
</style>
