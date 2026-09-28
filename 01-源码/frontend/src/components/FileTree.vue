<script setup lang="ts">
import { computed, h, nextTick, ref, watch, type VNodeChild } from "vue";
import { NIcon, NTag, NTree, type TreeInst, type TreeOption } from "naive-ui";
import { Document24Regular, FolderOpen24Regular } from "@vicons/fluent";
import type { RevealRequest, TreeItem } from "../stores/explorer";
import type { ExplorerOpenMode } from "../stores/settings";
import ImageThumbnail from "./ImageThumbnail.vue";

const props = withDefaults(
  defineProps<{
    items: TreeItem[];
    height?: string;
    expandAll?: boolean;
    openMode?: ExplorerOpenMode;
    selectedKey?: string | null;
    /** 多选：完整选择集（优先于 selectedKey）。 */
    selectedKeys?: string[];
    revealRequest?: RevealRequest | null;
    loadChildren?: (item: TreeItem) => Promise<void>;
  }>(),
  {
    height: "100%",
    expandAll: false,
    openMode: "single-click",
    selectedKey: null,
    selectedKeys: () => [],
    revealRequest: null,
  }
);

const emit = defineEmits<{
  open: [item: TreeItem];
  select: [item: TreeItem];
  /** Shift 连选 / Ctrl 点选：父级直接采用这一整份选择集。 */
  "select-many": [keys: string[]];
  /** 双击定位（openMode 非 double-click 时由双击触发）：搜索视窗里定位到左树。 */
  locate: [item: TreeItem];
  deselect: [];
  "reveal-consumed": [];
  contextmenu: [event: MouseEvent, item: TreeItem | null, items: TreeItem[]];
}>();

/*
 * 行高（nodeHeight 20px / 行内边距 0）由 `src/theme.ts` 的全局主题统一给出：
 * naive 的 Tree 用**主题** nodeHeight 计算虚拟滚动的每行高度，而组件级
 * `:theme-overrides` 在 Tree 上不生效（实测仍按默认 30px 排列，每行空出 6px），
 * 所以这条通道只能走 NConfigProvider。
 */

type FileTreeNode = TreeOption & {
  treeItem: TreeItem;
  children?: FileTreeNode[];
};

function toOption(item: TreeItem): FileTreeNode {
  return {
    key: item.key,
    label: item.label,
    treeItem: item,
    isLeaf: item.isLeaf,
    children: item.children ? item.children.map(toOption) : undefined,
  };
}

const treeData = computed(() => props.items.map(toOption));
const itemsByKey = computed(() => {
  const result = new Map<string, TreeItem>();

  function register(items: TreeItem[]) {
    for (const item of items) {
      result.set(item.key, item);
      if (item.children) register(item.children);
    }
  }

  register(props.items);
  return result;
});

const expandedKeys = ref<Array<string | number>>([]);
const searchExpansionInitialized = ref(false);
const treeRef = ref<TreeInst | null>(null);
const directoryToggleKeys = new Set<string>();
const handledClickEvents = new WeakSet<MouseEvent>();
/** 当前选中集：多选入参优先，兼容旧的单选入参。 */
const activeKeys = computed<string[]>(() =>
  props.selectedKeys && props.selectedKeys.length > 0
    ? props.selectedKeys
    : props.selectedKey
      ? [props.selectedKey]
      : []
);
/** Shift 连选的锚点：最后一次普通点击的节点。 */
let anchorKey = "";
/** 当前可见（已展开）节点的顺序，用于计算 Shift 区间。 */
const visibleRows = computed<TreeItem[]>(() => {
  const expanded = new Set(expandedKeys.value.map(String));
  const result: TreeItem[] = [];
  const walk = (items: TreeItem[]) => {
    for (const item of items) {
      result.push(item);
      if (item.isDir && item.children && expanded.has(item.key)) walk(item.children);
    }
  };
  walk(props.items);
  return result;
});

function isTreeControl(event: MouseEvent): boolean {
  const target = event.target;
  return target instanceof Element && !!target.closest("[data-switcher]");
}

function collectExpandedKeys(items: TreeItem[], result: Array<string | number>): void {
  for (const item of items) {
    if (!item.isDir) continue;
    result.push(item.key);
    if (item.children) collectExpandedKeys(item.children, result);
  }
}

watch(
  () => props.expandAll,
  (expandAll) => {
    if (!expandAll) {
      expandedKeys.value = [];
      searchExpansionInitialized.value = false;
      return;
    }
    const next: Array<string | number> = [];
    collectExpandedKeys(props.items, next);
    expandedKeys.value = next;
    searchExpansionInitialized.value = props.items.length > 0;
  },
  { immediate: true }
);

watch(
  () => props.items,
  () => {
    if (props.expandAll && props.items.length === 0) {
      expandedKeys.value = [];
      searchExpansionInitialized.value = false;
    } else if (props.expandAll && !searchExpansionInitialized.value) {
      const next: Array<string | number> = [];
      collectExpandedKeys(props.items, next);
      expandedKeys.value = next;
      searchExpansionInitialized.value = true;
    }
  },
  { deep: true }
);

// 只有显式的“定位”请求才会展开目录并滚动到目标节点；选中态本身不触发滚动。
watch(
  () => props.revealRequest,
  (request) => {
    if (!request) return;
    void revealPath(request.path).then(() => {
      if (props.revealRequest === request) emit("reveal-consumed");
    });
  },
  { immediate: true }
);

function findAncestorKeys(key: string): string[] {
  const parts = key.split("/").filter(Boolean);
  if (parts.length < 2) return [];

  const ancestors: string[] = [];
  let items = props.items;
  for (let index = 0; index < parts.length - 1; index++) {
    const ancestorKey = parts.slice(0, index + 1).join("/");
    const item = items.find((candidate) => candidate.key === ancestorKey);
    if (!item || !item.isDir) return [];
    ancestors.push(item.key);
    items = item.children ?? [];
  }
  return ancestors;
}

async function revealPath(key: string): Promise<void> {
  const ancestors = findAncestorKeys(key);
  if (ancestors.length > 0) {
    expandedKeys.value = [...new Set([...expandedKeys.value, ...ancestors])];
  }
  await nextTick();
  treeRef.value?.scrollTo({ key, behavior: "smooth" });
}

async function onLoad(node: TreeOption): Promise<void> {
  const item = (node as FileTreeNode).treeItem;
  if (item?.isDir && item.children === null) {
    await props.loadChildren?.(item);
  }
}

async function toggleDirectory(item: TreeItem): Promise<void> {
  if (!item.isDir || directoryToggleKeys.has(item.key)) return;
  directoryToggleKeys.add(item.key);
  try {
    if (item.children === null) {
      await props.loadChildren?.(item);
      if (item.children === null) return;
    }
    const next = new Set(expandedKeys.value);
    if (next.has(item.key)) next.delete(item.key);
    else next.add(item.key);
    expandedKeys.value = [...next];
  } finally {
    directoryToggleKeys.delete(item.key);
  }
}

function openNode(item: TreeItem): void {
  if (item.isDir) {
    void toggleDirectory(item).catch((error) => {
      console.error("load explorer directory failed", error);
    });
    return;
  }
  emit("open", item);
}

/** Shift 连选（区间）/ Ctrl 点选（切换）：都不打开文件、不展开目录。 */
function applyMultiSelect(item: TreeItem, additive: boolean, range: boolean): void {
  const current = [...activeKeys.value];
  if (range && anchorKey) {
    const rows = visibleRows.value;
    const from = rows.findIndex((row) => row.key === anchorKey);
    const to = rows.findIndex((row) => row.key === item.key);
    if (from >= 0 && to >= 0) {
      const [start, end] = from <= to ? [from, to] : [to, from];
      const rangeKeys = rows.slice(start, end + 1).map((row) => row.key);
      emit("select-many", additive ? [...new Set([...current, ...rangeKeys])] : rangeKeys);
      return;
    }
  }
  if (additive) {
    emit(
      "select-many",
      current.includes(item.key)
        ? current.filter((key) => key !== item.key)
        : [...current, item.key]
    );
    return;
  }
  emit("select-many", [item.key]);
}

function onNodeClick(event: MouseEvent, item: TreeItem): void {
  // NTree calls nodeProps.onClick once from its own handler and once from the
  // merged DOM handler when block-line is enabled. Handle the same event only once.
  if (handledClickEvents.has(event)) return;
  handledClickEvents.add(event);
  if (isTreeControl(event)) return;

  const additive = event.ctrlKey || event.metaKey;
  const range = event.shiftKey;
  if (additive || range) {
    event.stopPropagation();
    applyMultiSelect(item, additive, range);
    return;
  }

  // 单击只更新选中高亮，不展开、不滚动；滚动定位只由显式的定位请求触发。
  if (!item.isDir) emit("select", item);
  anchorKey = item.key;
  if (props.openMode !== "single-click") return;
  event.stopPropagation();
  openNode(item);
}

function onNodeDblclick(event: MouseEvent, item: TreeItem): void {
  if (isTreeControl(event)) return;
  event.stopPropagation();
  // double-click 打开模式下，双击即打开（沿用旧行为）。
  if (props.openMode === "double-click") {
    openNode(item);
    return;
  }
  // 其余模式：双击 = 定位（不打开文件）。目录双击仍展开/收起，
  // 文件双击交给父级处理（搜索视窗据此定位到左树并关闭自身）。
  if (item.isDir) {
    void toggleDirectory(item).catch((error) => {
      console.error("load explorer directory failed", error);
    });
    return;
  }
  emit("locate", item);
}

function onNodeContextMenu(event: MouseEvent, item: TreeItem): void {
  event.preventDefault();
  event.stopPropagation();
  const selected = activeKeys.value
    .map((key) => itemsByKey.value.get(key))
    .filter((selectedItem): selectedItem is TreeItem => !!selectedItem);
  // 右键点在某项上：它已在选择集内 → 对整个集合操作；否则只对当前项操作。
  const targets = selected.some((selectedItem) => selectedItem.key === item?.key)
    ? selected
    : item
      ? [item]
      : [];
  emit("contextmenu", event, item, targets);
}

function nodeProps({ option }: { option: TreeOption }) {
  const item = (option as FileTreeNode).treeItem;
  return {
    // 选中高亮自己画：selectable=false 时不能依赖 NTree 渲染 selected-keys 高亮。
    class: item && activeKeys.value.includes(item.key) ? "tree-node-selected" : undefined,
    onContextmenu: (event: MouseEvent) => onNodeContextMenu(event, item),
    onClick: (event: MouseEvent) => onNodeClick(event, item),
    onDblclick: (event: MouseEvent) => onNodeDblclick(event, item),
  };
}

function onShellContextMenu(event: MouseEvent): void {
  event.preventDefault();
  emit("contextmenu", event, null, []);
}

function onShellClick(event: MouseEvent): void {
  const target = event.target;
  if (!(target instanceof Element)) return;
  // 节点区域由节点自身处理；滚动条点击不应清空选中。
  if (target.closest(".n-tree-node, .n-scrollbar-rail")) return;
  emit("deselect");
}

function onExpandedKeys(keys: Array<string | number>): void {
  expandedKeys.value = keys;
}

function collapseAll(): void {
  expandedKeys.value = [];
}

function renderLabel({ option }: { option: TreeOption }): VNodeChild {
  const item = (option as FileTreeNode).treeItem;
  if (!item) return option.label ?? "";

  const changeColor =
    item.changeKind === "added"
      ? "var(--pvf-success)"
      : item.changeKind === "modified"
        ? "var(--pvf-warning)"
        : undefined;
  const children: VNodeChild[] = [];
  // 行首图标与书签面板同款（16px + 主题强调色）：目录=打开的文件夹，
  // 文件=有缩略图用缩略图，否则用通用文档图标兜底，保证每行都有图标可对齐。
  if (item.isDir) {
    children.push(
      h(NIcon, { size: 16, class: "tree-entry-icon" }, { default: () => h(FolderOpen24Regular) })
    );
  } else if (item.icon) {
    children.push(h(ImageThumbnail, { reference: item.icon, size: 16 }));
  } else {
    children.push(
      h(NIcon, { size: 16, class: "tree-entry-icon" }, { default: () => h(Document24Regular) })
    );
  }
  children.push(
    h(
      "span",
      {
        class: item.isDir ? "tree-item-name tree-item-name--dir" : "tree-item-name",
        style: { color: changeColor },
        title: item.label,
      },
      item.label
    ),
  );
  for (const annotation of item.annotations) {
    children.push(
      h(
        NTag,
        {
          size: "tiny",
          bordered: false,
          type: annotation.type === "reference" ? "success" : annotation.type === "enum" ? "warning" : "info",
          class: "tree-tag tree-tag-annotation",
          title: annotation.content,
        },
        { default: () => annotation.title }
      )
    );
  }
  if (!item.isDir) {
    for (const tag of item.tags) {
      if (tag.id) {
        children.push(
          h(
            NTag,
            {
              size: "tiny",
              bordered: false,
              type: "info",
              class: "tree-tag tree-tag-id",
              title: `id: ${tag.id}`,
            },
            { default: () => tag.id }
          )
        );
      }
      if (tag.name) {
        // 清单里已解析出的中文名以 [名称] 形式**紧跟**文件名（旧版观感）：
        // 用纯文本而不是标签，才能真正与文件名粘连。
        children.push(
          h(
            "span",
            {
              class: "tree-name-tag",
              title: `${tag.name}${tag.id ? `（id: ${tag.id}）` : ""}`,
            },
            `[${tag.name}]`
          )
        );
      }
    }
  }
  return h("div", { class: "tree-label", title: item.key }, children);
}

defineExpose({ collapseAll });
</script>

<template>
  <div
    class="file-tree-shell"
    @click="onShellClick"
    @contextmenu="onShellContextMenu"
  >
    <NTree
      ref="treeRef"
      block-line
      :selectable="false"
      :animated="false"
      virtual-scroll
      class="file-tree"
      :data="treeData"
      :indent="11"
      :expanded-keys="expandedKeys"
      :selected-keys="activeKeys"
      :on-load="onLoad"
      :on-update:expanded-keys="onExpandedKeys"
      :node-props="nodeProps"
      :render-label="renderLabel"
      :style="{ height }"
      :expand-on-click="false"
    />
  </div>
</template>

<style scoped>
.file-tree-shell {
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
}
.file-tree {
  width: 100%;
  min-width: 0;
  min-height: 0;
  /*
   * 行高由上面的 treeTheme（nodeHeight=22px）统一给出，这里只做 CSS 兜底：
   * 虚拟滚动的行高、内容区最小高度、行内边距三者必须一致，否则每行会留下空隙。
   * 字号层级：文件名 13px 正文色，注释/ID 标签 11px 弱化，中文名 12px 高亮 ——
   * 三档字号 + 目录加粗，让"目录 > 文件名 > 附属信息"的主次一眼可辨。
   */
  font-size: 13px;
  --n-node-content-height: 22px;
  --n-line-height: 22px;
  --n-node-wrapper-padding: 0;
}

/*
 * 行内容铺满可用宽度：文件名占剩余空间、放不下用省略号（悬停由 title 显示全名）；
 * 缩进每级固定 12px（NTree 的 indent），不随层级变深或内容变长而溢出。
 */
:deep(.n-tree-node) {
  width: 100%;
  min-width: 0;
}

:deep(.n-tree-node-content) {
  width: 100%;
  min-width: 0;
  overflow: hidden;
}

:deep(.n-tree-node-content__text) {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
}

/*
 * 缩进封顶（用户 2026-09-24：目录层级过深时文件持续右移、最终看不见）。
 * naive 为每一级渲染一个 indent 元素，宽度取 indent prop；这里改为逐级递减、
 * 第 7 级起归零 —— 缩进总量最多 3×11 + 3×7 = 54px，其后无论多深都不再右移。
 */
:deep(.n-tree-node-indent > div) {
  width: 11px !important;
}

:deep(.n-tree-node-indent:nth-child(n + 4) > div) {
  width: 7px !important;
}

:deep(.n-tree-node-indent:nth-child(n + 7) > div) {
  width: 0 !important;
}

:deep(.tree-label) {
  display: flex;
  align-items: center;
  /* 与书签面板行同款：图标与名称之间 6px；后面的注释/名称也走同一 gap。 */
  gap: 6px;
  min-width: 0;
  width: 100%;
  overflow: hidden;
  white-space: nowrap;
  vertical-align: middle;
}

/* 行首图标：颜色与书签面板的 .bookmark-entry-icon 完全一致（主题强调色）。 */
:deep(.tree-entry-icon) {
  flex: 0 0 auto;
  color: var(--pvf-primary-hover);
}

:deep(.tree-item-name) {
  /* 不撑满剩余宽度：注释/名称紧跟在文件名右侧，而不是被推到行尾；
     文件名保留一段最小可见宽度，空间不足时优先压缩后面的标签。 */
  flex: 0 1 auto;
  min-width: 36px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 目录名加粗：与文件形成第一层视觉主次，长列表里层级走向更直观。 */
:deep(.tree-item-name--dir) {
  font-weight: 600;
  letter-spacing: 0.2px;
}

/* 图标固定尺寸，不参与压缩。 */
:deep(.tree-label > .image-thumbnail) {
  flex: 0 0 auto;
}

/* 注释/ID 标签：小一号弱化（11px），圆角胶囊感，间距交给 .tree-label 的 gap。 */
:deep(.tree-tag) {
  flex: 0 4 auto;
  min-width: 0;
  overflow: hidden;
  font-size: 11px;
  height: 17px;
  padding: 0 5px;
  border-radius: 4px;
}

:deep(.tree-tag .n-tag__content) {
  overflow: hidden;
  font-size: 11px;
  line-height: 17px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 清单里已解析出的中文名：[名称] 紧跟文件名，中字号 + 中等字重做第二层主次。 */
:deep(.tree-name-tag) {
  flex: 0 3 auto;
  min-width: 0;
  overflow: hidden;
  font-size: 12px;
  font-weight: 500;
  color: var(--pvf-success);
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 悬停行不再自定义底色：与书签面板共用主题默认 hover，观感完全一致。 */

/* 选中行高亮（可多行同时高亮）：底色 + 左侧主题色细条，定位一目了然。 */
:deep(.n-tree-node.tree-node-selected) {
  background: var(--pvf-editor-active-line, rgba(127, 127, 127, 0.22));
  box-shadow: inset 2px 0 0 var(--pvf-primary, #4a9eff);
}
</style>
