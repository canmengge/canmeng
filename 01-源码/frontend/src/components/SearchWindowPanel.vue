<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Search24Regular } from "@vicons/fluent";
import {
  NButton,
  NDropdown,
  NEmpty,
  NIcon,
  NTag,
  NText,
  useMessage,
  type DropdownOption,
} from "naive-ui";
import { buildSearchTree } from "../searchTree";
import { ArchiveService } from "../../bindings/pvfine/services";
import type { TreeTag } from "../../bindings/pvfine/services/models";
import { useAIStore, MAX_CONTEXT_FILES } from "../stores/ai";
import { useAnnotationEditStore } from "../stores/annotationEdit";
import { useArchiveStore } from "../stores/archive";
import { useEditorStore } from "../stores/editor";
import { useExplorerStore, type SearchItem, type TreeItem } from "../stores/explorer";
import { useFileSetStore } from "../stores/fileSets";
import { useBookmarkStore } from "../stores/bookmarks";
import { useSearchWindowStore } from "../stores/searchWindow";
import { useSidebarStore } from "../stores/sidebar";
import FileTree from "./FileTree.vue";

/**
 * 搜索视窗：与左侧资源管理器**同格式**的文件列表，但只装"搜索命中的文件"与
 * "手动收进来的文件"（左侧文件上右键 → 收进搜索视窗）。
 * 只读呈现与跳转，不改变归档内容。右键菜单与左侧文件树一致（子集：均为文件操作）。
 */
const searchWindow = useSearchWindowStore();
const explorer = useExplorerStore();
const editor = useEditorStore();
const archive = useArchiveStore();
const fileSets = useFileSetStore();
const bookmarks = useBookmarkStore();
const ai = useAIStore();
const sidebar = useSidebarStore();
const annotationEdit = useAnnotationEditStore();
const message = useMessage();

// ---- 标签与文件树对齐（用户 2026-09-26：ID + [中文名] 要与左树一致）----
// 手动收进/导入后收进的条目 SearchItem 不带登记信息；这里对缺标签的条目批量
// ResolveFiles 补齐（数据源与左树相同：后端 treeTagsByFile）。
const resolvedTags = ref<Map<string, TreeTag[]>>(new Map());
const tagsRequestToken = ref(0);

watch(
  () => searchWindow.entries.map((entry) => entry.path).join("\n"),
  async () => {
    const token = ++tagsRequestToken.value;
    const missing = searchWindow.entries
      .filter((entry) => entry.category === "file" && !entry.name)
      .slice(0, 200)
      .map((entry) => entry.path);
    if (missing.length === 0) return;
    try {
      const nodes = (await ArchiveService.ResolveFiles(missing)) ?? [];
      if (token !== tagsRequestToken.value) return;
      const next = new Map(resolvedTags.value);
      for (const node of nodes) {
        if (!node) continue;
        next.set(node.path, (node.tags ?? []).filter(
          (tag): tag is TreeTag => !!tag
        ));
      }
      resolvedTags.value = next;
    } catch {
      // 归档未打开/切换：保留现状即可。
    }
  },
  { immediate: true }
);

/** 用左树登记信息补齐缺标签的条目，保证与文件树显示一致。 */
const enrichedEntries = computed<SearchItem[]>(() =>
  searchWindow.entries.map((item) => {
    if (item.category !== "file" || item.name) return item;
    const registered = explorer.getItem(item.path);
    const tags =
      (registered?.tags ?? []).filter((tag) => !!tag).length > 0
        ? registered!.tags
        : resolvedTags.value.get(item.path) ?? [];
    const tag = tags.find((candidate) => candidate && (candidate.id || candidate.name));
    if (!tag) return item;
    return {
      ...item,
      id: tag.id ?? "",
      name: tag.name ?? "",
      category: tag.category || item.category,
      icon: item.icon ?? registered?.icon ?? null,
      fieldImage: item.fieldImage ?? registered?.fieldImage ?? null,
    };
  })
);

// 搜索视窗显示 ID + [中文名]，与左侧文件树完全一致（原 showIdTag:false 已按
// 用户 2026-09-26 要求撤销）。
const treeItems = computed(() => buildSearchTree(enrichedEntries.value));
const hitCount = computed(() => explorer.hits.length);
const searching = computed(() => explorer.mode === "search" && explorer.query.trim() !== "");

// 点击 = 定位左树 + 打开文件；双击 = 只定位左树。两者完成后都关闭搜索视窗
// （用户 2026-09-27：点开/定位后不再需要停在搜索视窗上）。
async function openItem(item: TreeItem): Promise<void> {
  if (item.isDir) return;
  // 定位左树：若左侧正处于搜索模式，先切回目录树，再展开并滚动定位到该文件
  // （store 的 clearSearch 只清状态、不触发「取消搜索快照」，不会回灌搜索视窗）。
  if (explorer.mode === "search") {
    explorer.clearSearch();
  }
  void explorer.revealPath(item.key);
  await editor.openFile(item.fileIndex);
  sidebar.close();
}

/** 双击 = 定位文件到左侧资源管理器（不打开文件），随后关闭搜索视窗。 */
async function locateItem(item: TreeItem): Promise<void> {
  if (item.isDir) return;
  if (explorer.mode === "search") {
    explorer.clearSearch();
  }
  const found = await explorer.revealPath(item.key);
  if (found) sidebar.close();
}

function selectItem(item: TreeItem): void {
  explorer.selectedKey = item.key;
}

// ---- 右键菜单（与左侧文件树一致的文件操作子集）----
const contextMenu = ref<{
  show: boolean;
  x: number;
  y: number;
  anchor: TreeItem | null;
  items: TreeItem[];
}>({ show: false, x: 0, y: 0, anchor: null, items: [] });

function onTreeContextMenu(event: MouseEvent, item: TreeItem | null, items: TreeItem[]): void {
  contextMenu.value = {
    show: true,
    x: event.clientX,
    y: event.clientY,
    anchor: item,
    items: items.length > 0 ? items : item ? [item] : [],
  };
}

function hideContextMenu(): void {
  contextMenu.value.show = false;
}

const contextMenuOptions = computed<DropdownOption[]>(() => [
  {
    label: "打开（定位到左树）",
    key: "open",
    disabled: contextMenu.value.items.length !== 1 || contextMenu.value.items[0].isDir,
  },
  { type: "divider", key: "divider-open" },
  {
    label: "复制",
    key: "copy",
    disabled: contextMenu.value.items.length === 0,
    children: [
      { label: "文件路径", key: "copy-paths" },
      { label: "ID", key: "copy-ids" },
      { label: "名称", key: "copy-names" },
    ],
  },
  {
    label: "加入当前书签簿",
    key: "bookmark-add",
    disabled: contextMenu.value.items.length === 0,
  },
  {
    label: `加入“${fileSets.activeSet?.name ?? "当前文件集"}”`,
    key: "add-to-file-set",
    disabled: contextMenu.value.items.length === 0,
  },
  {
    label: "编辑注释…",
    key: "edit-annotation",
    disabled: contextMenu.value.items.length !== 1,
  },
  {
    label: "AI 引入",
    key: "ai-introduce",
    disabled: contextMenu.value.items.length === 0,
  },
]);

// ---- 菜单动作实现（与 Explorer.vue 同语义）----
async function writeClipboardText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // WebView 拒绝时走隐藏 textarea 兜底。
    }
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  document.body.removeChild(textarea);
  if (!copied) throw new Error("系统剪贴板不可用");
}

function itemTags(item: TreeItem): TreeTag[] {
  return (item.tags ?? []).filter((tag): tag is TreeTag => !!tag);
}

async function onCopy(kind: "paths" | "ids" | "names", items: TreeItem[]): Promise<void> {
  hideContextMenu();
  const files = items.filter((item) => !item.isDir);
  if (files.length === 0) {
    message.info("请选择文件");
    return;
  }
  try {
    let values: string[];
    if (kind === "paths") {
      values = files.map((item) => item.key);
    } else {
      values = [];
      for (const item of files) {
        for (const tag of itemTags(item)) {
          const value = kind === "ids" ? tag.id : tag.name;
          if (value && !values.includes(value)) values.push(value);
        }
      }
    }
    if (values.length === 0) {
      message.info(kind === "ids" ? "所选文件没有登记 ID" : "所选文件没有登记名称");
      return;
    }
    await writeClipboardText(values.join("\n"));
    message.success(`已复制 ${values.length} 项`);
  } catch (e: any) {
    message.error(`复制失败: ${e?.message ?? e}`);
  }
}

async function onBookmarkAdd(items: TreeItem[]): Promise<void> {
  const entries = items
    .filter((item) => !item.isDir && item.fileIndex >= 0)
    .map((item) => ({
      path: item.key,
      name: item.label,
      fileIndex: item.fileIndex,
      isDir: false,
    }));
  if (entries.length === 0) {
    message.info("请选择文件节点加入书签");
    return;
  }
  try {
    await bookmarks.load();
    const result = bookmarks.addEntries(entries);
    if (result.added === 0) {
      message.info("所选文件已在当前书签分组中");
      return;
    }
    message.success(`已加入 ${result.added} 个书签`);
  } catch (e: any) {
    message.error(`加入书签失败: ${e?.message ?? e}`);
  } finally {
    hideContextMenu();
  }
}

async function onAddToFileSet(items: TreeItem[]): Promise<void> {
  hideContextMenu();
  const entries = items
    .filter((item) => !item.isDir && item.fileIndex >= 0)
    .map((item) => ({
      fileIndex: item.fileIndex,
      path: item.key,
      name: item.label,
      ids: [...new Set(itemTags(item).map((tag) => tag.id).filter(Boolean))],
      size: item.size,
      dataType: item.dataType,
      icon: item.icon ?? null,
      fieldImage: item.fieldImage ?? null,
    }));
  if (entries.length === 0) {
    message.info("请选择文件");
    return;
  }
  const result = fileSets.addEntries(entries);
  if (result.added === 0 && result.skipped === 0) {
    message.info("没有可加入的文件");
    return;
  }
  const skipped = result.skipped > 0 ? `，跳过 ${result.skipped} 个重复项` : "";
  message.success(`已加入 ${result.added} 个文件${skipped}`);
}

function onEditAnnotation(item: TreeItem | null): void {
  hideContextMenu();
  if (!item || item.isDir) return;
  void annotationEdit.openFor({
    path: item.key,
    isDir: false,
    builtinTitle: itemTags(item)
      .map((tag) => tag.name)
      .filter(Boolean)
      .join("、"),
  });
}

function onAIIntroduce(items: TreeItem[]): void {
  hideContextMenu();
  if (items.length === 0) return;
  const entries = items
    .filter((item) => !!item.key)
    .map((item) => ({
      path: item.key,
      title: item.key.slice(item.key.lastIndexOf("/") + 1),
      isDir: item.isDir,
    }));
  if (entries.length === 0) return;
  const { added, truncated } = ai.introduceFiles(entries);
  sidebar.show("ai");
  if (added === 0) {
    message.info("选中的条目已全部在 AI 引入中");
    return;
  }
  message.success(
    `已引入给 AI ${added} 个条目${truncated ? `，已达上限 ${MAX_CONTEXT_FILES} 个` : ""}`
  );
}

function onContextMenuSelect(key: string | number): void {
  const items = [...contextMenu.value.items];
  const anchor = contextMenu.value.anchor;
  if (key === "open") {
    hideContextMenu();
    if (anchor && !anchor.isDir) void openItem(anchor);
    return;
  }
  if (key === "copy-paths") {
    void onCopy("paths", items);
    return;
  }
  if (key === "copy-ids") {
    void onCopy("ids", items);
    return;
  }
  if (key === "copy-names") {
    void onCopy("names", items);
    return;
  }
  if (key === "bookmark-add") {
    void onBookmarkAdd(items);
    return;
  }
  if (key === "add-to-file-set") {
    void onAddToFileSet(items);
    return;
  }
  if (key === "edit-annotation") {
    onEditAnnotation(anchor);
    return;
  }
  if (key === "ai-introduce") {
    onAIIntroduce(items);
    return;
  }
}
</script>

<template>
  <div class="search-window-panel">
    <div class="sw-head">
      <NIcon :size="16"><Search24Regular /></NIcon>
      <NText strong>搜索视窗</NText>
      <NTag v-if="searching" size="tiny" :bordered="false">
        「{{ explorer.query }}」{{ hitCount.toLocaleString() }} 条
      </NTag>
      <NButton
        v-if="searchWindow.manualCount > 0"
        quaternary
        size="tiny"
        class="sw-clear"
        @click="searchWindow.clearManual()"
      >
        清空收藏（{{ searchWindow.manualCount }}）
      </NButton>
    </div>
    <NText depth="3" class="sw-note">
      搜索结果默认显示在这里（格式与左侧文件列表一致）；在左侧文件上右键可选择「收进搜索视窗」常驻保留。
    </NText>
    <NEmpty
      v-if="!searchWindow.hasContent"
      size="small"
      class="sw-empty"
      description="还没有内容：在左侧搜索，或右键「收进搜索视窗」"
    />
    <div v-else class="sw-body">
      <FileTree
        :items="treeItems"
        :selected-keys="explorer.selectedKeys"
        @open="openItem"
        @locate="locateItem"
        @select="selectItem"
        @select-many="explorer.setSelection($event)"
        @contextmenu="onTreeContextMenu"
      />
    </div>
    <NDropdown
      trigger="manual"
      placement="bottom-start"
      :show="contextMenu.show"
      :x="contextMenu.x"
      :y="contextMenu.y"
      :options="contextMenuOptions"
      @select="onContextMenuSelect"
      @clickoutside="hideContextMenu"
    />
  </div>
</template>

<style scoped>
.search-window-panel {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
  min-height: 0;
  padding: 10px 12px 0;
}
.sw-head {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.sw-clear {
  margin-left: auto;
}
.sw-note {
  font-size: 12px;
  line-height: 1.5;
}
.sw-empty {
  margin-top: 24px;
}
.sw-body {
  flex: 1 1 auto;
  min-height: 0;
  margin: 0 -12px;
  overflow: hidden;
}
</style>
