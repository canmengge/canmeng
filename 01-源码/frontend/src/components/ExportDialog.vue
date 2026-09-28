<script setup lang="ts">
import { computed, h, onBeforeUnmount, ref, watch, type VNodeChild } from "vue";
import {
  NButton,
  NEmpty,
  NIcon,
  NInput,
  NModal,
  NTag,
  NText,
  NTree,
  type TreeOption,
} from "naive-ui";
import { ArrowUp20Regular } from "@vicons/fluent";
import { ArchiveService } from "../../bindings/pvfine/services";
import type { TreeTag } from "../../bindings/pvfine/services/models";
import { CreateLocalDirectory, ListLocalFiles } from "../services/importApi";

/**
 * 导出窗口（两栏）：
 * - 左：**要导出的文件**树（按目录分组，可勾选文件 / 文件夹，取消勾选即不导出）；
 * - 右：**导出到**目标文件夹（只列目录、单选，带「新建文件夹」）。
 * 确认后由调用方落盘到 <目标目录>\<时间戳><后缀>\（后端 ExportFilesTo 负责建目录）。
 */
const props = withDefaults(
  defineProps<{
    show: boolean;
    /** 窗口标题，如「导出改动」/「导出文件」。 */
    title?: string;
    /** 待导出条目（归档内相对路径）。 */
    paths?: string[];
  }>(),
  { title: "导出", paths: () => [] }
);
const emit = defineEmits<{
  "update:show": [show: boolean];
  confirm: [payload: { dir: string; paths: string[] }];
}>();

// ---------------- 左栏：要导出的文件树 ----------------

interface FileNode extends TreeOption {
  key: string;
  label: string;
  isLeaf: boolean;
  children?: FileNode[];
}

/** 文件条目 → 显示用的 ID / 中文名（与左侧文件树同源：索引里的登记信息）。 */
const resolvedTags = ref<Map<string, TreeTag[]>>(new Map());

async function loadResolvedTags(): Promise<void> {
  const files = props.paths.filter((path) => !!path);
  if (files.length === 0) {
    resolvedTags.value = new Map();
    return;
  }
  try {
    const nodes = (await ArchiveService.ResolveFiles(files)) ?? [];
    const map = new Map<string, TreeTag[]>();
    for (const node of nodes) {
      if (node && node.path) map.set(node.path, node.tags ?? []);
    }
    resolvedTags.value = map;
  } catch {
    resolvedTags.value = new Map();
  }
}

/** 与左侧文件树一致的标签渲染：文件名 + ID 胶囊 + [中文名]。 */
function renderFileLabel({ option }: { option: TreeOption }): VNodeChild {
  const node = option as FileNode;
  const children: VNodeChild[] = [
    h(
      "span",
      {
        class: node.isLeaf ? "ex-item-name" : "ex-item-name ex-item-name--dir",
        title: node.key,
      },
      node.label
    ),
  ];
  for (const tag of resolvedTags.value.get(node.key) ?? []) {
    if (tag.id) {
      children.push(
        h(
          NTag,
          {
            size: "tiny",
            bordered: false,
            type: "info",
            class: "ex-tag ex-tag-id",
            title: `id: ${tag.id}`,
          },
          { default: () => tag.id }
        )
      );
    }
    if (tag.name) {
      children.push(
        h(
          "span",
          {
            class: "ex-name-tag",
            title: `${tag.name}${tag.id ? `（id: ${tag.id}）` : ""}`,
          },
          `[${tag.name}]`
        )
      );
    }
  }
  return h("div", { class: "ex-label", title: node.key }, children);
}

/** 把扁平路径表按 "/" 组装成树（同目录节点自动合并）。 */
const fileTree = computed<FileNode[]>(() => {
  const root: FileNode[] = [];
  const dirNodes = new Map<string, FileNode>();
  for (const path of [...props.paths].sort()) {
    const parts = path.split("/").filter(Boolean);
    if (parts.length === 0) continue;
    let siblings = root;
    let prefix = "";
    for (let i = 0; i < parts.length; i++) {
      prefix = prefix ? `${prefix}/${parts[i]}` : parts[i];
      if (i === parts.length - 1) {
        siblings.push({ key: prefix, label: parts[i], isLeaf: true });
        continue;
      }
      let node = dirNodes.get(prefix);
      if (!node) {
        node = { key: prefix, label: parts[i], isLeaf: false, children: [] };
        dirNodes.set(prefix, node);
        siblings.push(node);
      }
      siblings = node.children as FileNode[];
    }
  }
  return root;
});

/** 树里出现的全部 key（文件 + 目录）。 */
const allFileKeys = computed(() => {
  const keys: string[] = [];
  const walk = (nodes: FileNode[]) => {
    for (const node of nodes) {
      keys.push(node.key);
      if (node.children) walk(node.children as FileNode[]);
    }
  };
  walk(fileTree.value);
  return keys;
});

const checkedFiles = ref<string[]>([]);
const checkedFileSet = computed(() => new Set(checkedFiles.value));

/** 父 → 直接子 key，供勾选联动（naive 的 cascade 在本树结构下不生效，自己实现）。 */
const childrenByKey = computed(() => {
  const map = new Map<string, string[]>();
  const walk = (nodes: FileNode[]) => {
    for (const node of nodes) {
      const kids = (node.children ?? []) as FileNode[];
      if (kids.length > 0) map.set(node.key, kids.map((child) => child.key));
      walk(kids);
    }
  };
  walk(fileTree.value);
  return map;
});

/** 某节点下的全部子孙 key（深度递归）。 */
function descendantsOf(key: string): string[] {
  const out: string[] = [];
  const stack = [...(childrenByKey.value.get(key) ?? [])];
  while (stack.length > 0) {
    const current = stack.pop() as string;
    out.push(current);
    stack.push(...(childrenByKey.value.get(current) ?? []));
  }
  return out;
}

/** 勾选联动：勾上目录 = 其下全部文件一并勾上；取消目录 = 其下全部取消。 */
function onFileCheckedUpdate(keys: Array<string | number>): void {
  const next = new Set(keys.map(String));
  const prev = new Set(checkedFiles.value);
  for (const key of next) {
    if (!prev.has(key)) {
      for (const descendant of descendantsOf(key)) next.add(descendant);
    }
  }
  for (const key of prev) {
    if (!next.has(key)) {
      for (const descendant of descendantsOf(key)) next.delete(descendant);
    }
  }
  checkedFiles.value = [...next];
}

/** 判某条路径是否会被导出（自身被勾选，或其任一上级目录被勾选）。 */
function isPathChecked(path: string): boolean {
  if (checkedFileSet.value.has(path)) return true;
  const parts = path.split("/");
  let prefix = "";
  for (let i = 0; i < parts.length - 1; i++) {
    prefix = prefix ? `${prefix}/${parts[i]}` : parts[i];
    if (checkedFileSet.value.has(prefix)) return true;
  }
  return false;
}

const selectedPaths = computed(() => props.paths.filter((path) => isPathChecked(path)));

const fileExpanded = ref<Array<string | number>>([]);

// ---------------- 右栏：目标文件夹（只列目录、单选） ----------------

interface DirNode extends TreeOption {
  key: string;
  label: string;
  isLeaf: boolean;
  loaded?: boolean;
  checkboxDisabled?: boolean;
}

const dirTree = ref<DirNode[]>([]);
const dirChecked = ref<string[]>([]);
const dirExpanded = ref<Array<string | number>>([]);
const pathInput = ref("");
const dirError = ref("");
const loadingDirs = ref(false);
const dirNodesByKey = new Map<string, DirNode>();

const createVisible = ref(false);
const createName = ref("");
const createError = ref("");

function toDirNodes(entries: Awaited<ReturnType<typeof ListLocalFiles>>): DirNode[] {
  return (entries ?? []).map((entry) => ({
    key: entry.path,
    label: entry.name,
    isLeaf: !entry.isDir,
    // 目标只能是文件夹：文件照旧列出来（与「导入文件」同一套树），但不可勾选。
    checkboxDisabled: !entry.isDir,
  }));
}

function registerDirNodes(nodes: DirNode[]): void {
  for (const node of nodes) {
    dirNodesByKey.set(node.key, node);
    if (node.children) registerDirNodes(node.children as DirNode[]);
  }
}

/** 当前跳转路径（规范化成 Windows 形式，去掉尾部分隔符）。 */
function currentDir(): string {
  return pathInput.value.trim().replaceAll("/", "\\").replace(/\\+$/, "");
}

const targetDir = computed(() => dirChecked.value[0] ?? "");

async function jumpTo(path: string): Promise<void> {
  const target = path.trim();
  if (!target) return;
  dirError.value = "";
  loadingDirs.value = true;
  try {
    const entries = (await ListLocalFiles(target)) ?? [];
    dirTree.value = toDirNodes(entries);
    registerDirNodes(dirTree.value);
    dirExpanded.value = [];
    dirChecked.value = [];
    pathInput.value = target.replaceAll("/", "\\").replace(/\\+$/, "");
  } catch (e: any) {
    dirError.value = String(e?.message ?? e);
  } finally {
    loadingDirs.value = false;
  }
}

async function jumpUp(): Promise<void> {
  const current = currentDir();
  if (!current) return;
  const parent = current.slice(0, Math.max(current.lastIndexOf("\\"), 0));
  await jumpTo(parent);
}

async function loadDirChildren(node: DirNode): Promise<void> {
  if (node.loaded) return;
  node.loaded = true;
  try {
    const entries = (await ListLocalFiles(node.key)) ?? [];
    node.children = toDirNodes(entries);
    registerDirNodes(node.children as DirNode[]);
  } catch (e: any) {
    node.children = [];
    node.isLeaf = true;
    dirError.value = String(e?.message ?? e);
  }
}

async function onDirLoad(node: TreeOption): Promise<void> {
  await loadDirChildren(node as DirNode);
}

/** 目标文件夹只允许选一个：保留最后勾选的那个。 */
function onDirCheckedUpdate(keys: Array<string | number>): void {
  const prev = dirChecked.value;
  const added = keys.map(String).filter((key) => !prev.includes(key));
  dirChecked.value = added.length > 0 ? [added[added.length - 1]] : keys.map(String).slice(-1);
}

function openCreateDir(): void {
  if (!currentDir()) {
    dirError.value = "请先用「跳转」进入某个文件夹，再新建";
    return;
  }
  createName.value = "";
  createError.value = "";
  createVisible.value = true;
}

async function confirmCreateDir(): Promise<void> {
  const name = createName.value.trim();
  if (!name) {
    createError.value = "请输入文件夹名";
    return;
  }
  if (/[\\/:*?"<>|]/.test(name)) {
    createError.value = '文件夹名不能包含 \\ / : * ? " < > |';
    return;
  }
  const base = currentDir();
  const target = `${base}\\${name}`;
  try {
    const created = await CreateLocalDirectory(target);
    createVisible.value = false;
    await jumpTo(base);
    dirChecked.value = [created || target];
  } catch (e: any) {
    createError.value = String(e?.message ?? e);
  }
}

// ---------------- 左右分栏：拖动分隔线调整宽度 ----------------

const splitHost = ref<HTMLElement | null>(null);
/** 左栏宽度（px）；0 = 均分（50%）。 */
const leftWidth = ref(0);
/** 单栏最小宽度，避免把某一侧拖到看不见。 */
const MIN_COL = 220;

let resizeMove: ((event: PointerEvent) => void) | null = null;
let resizeEnd: (() => void) | null = null;

function onResizeStart(event: PointerEvent): void {
  const host = splitHost.value;
  if (!host) return;
  const rect = host.getBoundingClientRect();
  if (rect.width <= 0) return;
  const startX = event.clientX;
  const startWidth = leftWidth.value || rect.width / 2;
  const maxWidth = Math.max(MIN_COL, rect.width - MIN_COL);
  document.body.style.cursor = "col-resize";
  document.body.style.userSelect = "none";
  resizeMove = (moveEvent: PointerEvent) => {
    leftWidth.value = Math.min(maxWidth, Math.max(MIN_COL, startWidth + (moveEvent.clientX - startX)));
  };
  resizeEnd = () => {
    resizeMove = null;
    resizeEnd = null;
    document.body.style.cursor = "";
    document.body.style.userSelect = "";
    window.removeEventListener("pointermove", onResizeMove);
    window.removeEventListener("pointerup", onResizeEnd);
  };
  window.addEventListener("pointermove", onResizeMove);
  window.addEventListener("pointerup", onResizeEnd);
}

function onResizeMove(event: PointerEvent): void {
  resizeMove?.(event);
}

function onResizeEnd(): void {
  resizeEnd?.();
}

onBeforeUnmount(() => onResizeEnd());

// ---------------- 打开 / 确认 ----------------

async function reloadAll(): Promise<void> {
  checkedFiles.value = [...allFileKeys.value];
  fileExpanded.value = [...allFileKeys.value];
  dirChecked.value = [];
  pathInput.value = "";
  dirExpanded.value = [];
  dirError.value = "";
  createVisible.value = false;
  dirNodesByKey.clear();
  dirTree.value = [];
  loadingDirs.value = true;
  void loadResolvedTags();
  try {
    const drives = (await ListLocalFiles("")) ?? [];
    dirTree.value = toDirNodes(drives);
    registerDirNodes(dirTree.value);
  } catch (e: any) {
    dirError.value = `读取本地磁盘失败：${e?.message ?? e}`;
  } finally {
    loadingDirs.value = false;
  }
}

watch(
  () => props.show,
  (show) => {
    if (show) void reloadAll();
  }
);

function onConfirm(): void {
  const dir = targetDir.value;
  const paths = selectedPaths.value;
  if (!dir || paths.length === 0) return;
  emit("confirm", { dir, paths });
  emit("update:show", false);
}
</script>

<template>
  <NModal
    :show="props.show"
    preset="card"
    :title="props.title"
    :style="{ width: 'min(940px, calc(100vw - 40px))' }"
    :mask-closable="true"
    @update:show="emit('update:show', $event)"
  >
    <div ref="splitHost" class="ex-box">
      <!-- 左：要导出的文件 -->
      <section class="ex-col" :style="{ flexBasis: leftWidth ? `${leftWidth}px` : '50%' }">
        <div class="ex-col-head">
          <NText depth="3" class="ex-col-title">
            要导出的文件（{{ selectedPaths.length }} / {{ props.paths.length }}）
          </NText>
          <NButton size="tiny" quaternary @click="checkedFiles = [...allFileKeys]">全选</NButton>
        </div>
        <div class="ex-col-body">
          <NTree
            v-if="props.paths.length > 0"
            block-line
            checkable
            :cascade="false"
            :data="fileTree"
            :checked-keys="checkedFiles"
            :expanded-keys="fileExpanded"
            :render-label="renderFileLabel"
            :animated="false"
            virtual-scroll
            :style="{ height: '360px' }"
            :on-update:checked-keys="onFileCheckedUpdate"
            :on-update:expanded-keys="(keys: Array<string | number>) => (fileExpanded = keys)"
          />
          <NEmpty v-else size="small" description="没有待导出的文件" />
        </div>
      </section>

      <div
        class="ex-resizer"
        role="separator"
        aria-orientation="vertical"
        aria-label="调整左右宽度"
        tabindex="0"
        title="拖动调整左右宽度（双击恢复均分）"
        @pointerdown.prevent="onResizeStart"
        @dblclick="leftWidth = 0"
      />

      <!-- 右：导出到哪个文件夹 -->
      <section class="ex-col">
        <div class="ex-col-head">
          <NText depth="3" class="ex-col-title">导出到</NText>
          <NButton
            size="small"
            quaternary
            :disabled="!pathInput"
            aria-label="上一级"
            @click="jumpUp"
          >
            <template #icon><NIcon :size="16"><ArrowUp20Regular /></NIcon></template>
          </NButton>
          <NInput
            v-model:value="pathInput"
            size="small"
            placeholder="输入本地路径后回车跳转，如 D:\\Mod\\"
            @keydown.enter.prevent="jumpTo(pathInput)"
          />
          <NButton size="small" secondary @click="jumpTo(pathInput)">跳转</NButton>
          <NButton size="small" secondary @click="openCreateDir">新建文件夹</NButton>
        </div>
        <NText v-if="dirError" depth="3" class="ex-error">{{ dirError }}</NText>
        <div class="ex-col-body">
          <NTree
            v-if="dirTree.length > 0"
            block-line
            checkable
            :cascade="false"
            :data="dirTree"
            :checked-keys="dirChecked"
            :expanded-keys="dirExpanded"
            :on-load="onDirLoad"
            :animated="false"
            virtual-scroll
            :style="{ height: '360px' }"
            :on-update:checked-keys="onDirCheckedUpdate"
            :on-update:expanded-keys="(keys: Array<string | number>) => (dirExpanded = keys)"
          />
          <NEmpty
            v-else
            size="small"
            :description="loadingDirs ? '正在读取本地磁盘…' : '请用上方「跳转」进入目标文件夹'"
          />
        </div>
      </section>
    </div>

    <div class="ex-summary">
      <NText depth="3">将导出 {{ selectedPaths.length }} 个文件</NText>
      <NText depth="3" class="ex-target">
        目标：{{ targetDir || "（未选择文件夹）" }}
      </NText>
    </div>

    <template #footer>
      <div class="ex-actions">
        <NButton quaternary @click="emit('update:show', false)">取消</NButton>
        <NButton
          type="primary"
          :disabled="!targetDir || selectedPaths.length === 0"
          @click="onConfirm"
        >
          导出（{{ selectedPaths.length }}）
        </NButton>
      </div>
    </template>
  </NModal>

  <NModal
    v-model:show="createVisible"
    preset="card"
    title="新建文件夹"
    :style="{ width: 'min(380px, calc(100vw - 40px))' }"
  >
    <div class="ex-create">
      <NText depth="3">位置：{{ currentDir() || "（先用「跳转」进入一个文件夹）" }}</NText>
      <NInput
        v-model:value="createName"
        size="small"
        placeholder="文件夹名"
        @keydown.enter.prevent="confirmCreateDir"
      />
      <NText v-if="createError" depth="3" class="ex-error">{{ createError }}</NText>
    </div>
    <template #footer>
      <div class="ex-actions">
        <NButton quaternary @click="createVisible = false">取消</NButton>
        <NButton type="primary" @click="confirmCreateDir">创建</NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
/* 左右两栏合并为同一个框；两列表头等高 ⇒ 下面两棵树严格对齐。 */
.ex-box {
  display: flex;
  align-items: stretch;
  border: 1px solid var(--pvf-border-normal, rgba(255, 255, 255, 0.09));
  border-radius: 6px;
  overflow: hidden;
}
.ex-col {
  display: flex;
  flex: 1 1 0;
  flex-direction: column;
  min-width: 0;
}
.ex-col-head {
  display: flex;
  align-items: center;
  gap: 6px;
  /* 固定表头高度：这是"两棵树对齐"的关键。 */
  height: 32px;
  padding: 0 6px;
  border-bottom: 1px solid var(--pvf-border-normal, rgba(255, 255, 255, 0.09));
  background: var(--pvf-surface-subtle);
  overflow: hidden;
}
.ex-col-title {
  flex: 0 0 auto;
  font-size: 12px;
  white-space: nowrap;
}
.ex-col-head :deep(.n-input) {
  flex: 1 1 auto;
  min-width: 60px;
}
.ex-col-head > :deep(.n-button):last-child {
  margin-left: auto;
}
.ex-col-body {
  flex: 1 1 auto;
  min-height: 0;
  padding: 4px;
  overflow: hidden;
}
/* 中间分隔线：与编辑器分屏同款观感，可左右拖动（双击恢复均分）。 */
.ex-resizer {
  flex: 0 0 5px;
  z-index: 5;
  cursor: col-resize;
  background: var(--pvf-surface-inset);
}
.ex-resizer:hover,
.ex-resizer:focus-visible {
  background: var(--pvf-effect-split-hover);
  outline: none;
}
.ex-error {
  font-size: 12px;
  color: #e88080;
}

/* 行标签：与左侧文件树（FileTree.vue）同款取值，观感完全一致。 */
:deep(.ex-label) {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  width: 100%;
  overflow: hidden;
  white-space: nowrap;
}
:deep(.ex-item-name) {
  flex: 0 1 auto;
  min-width: 36px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
:deep(.ex-item-name--dir) {
  font-weight: 600;
  letter-spacing: 0.2px;
}
:deep(.ex-tag) {
  flex: 0 4 auto;
  min-width: 0;
  overflow: hidden;
  font-size: 11px;
  height: 17px;
  padding: 0 5px;
  border-radius: 4px;
}
:deep(.ex-tag .n-tag__content) {
  overflow: hidden;
  font-size: 11px;
  line-height: 17px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
:deep(.ex-name-tag) {
  flex: 0 3 auto;
  min-width: 0;
  overflow: hidden;
  font-size: 12px;
  font-weight: 500;
  color: var(--pvf-success);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ex-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 10px;
}
.ex-target {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ex-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.ex-create {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
</style>
