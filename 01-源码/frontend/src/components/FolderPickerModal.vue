<script setup lang="ts">
import { ref, watch } from "vue";
import { NButton, NIcon, NInput, NModal, NText, NTree, type TreeOption } from "naive-ui";
import { ArrowUp20Regular } from "@vicons/fluent";
import { ListLocalFiles } from "../services/importApi";

/**
 * 自绘本地「多选」选择器：目录 + 文件混排，支持多选。
 * 级联语义：勾选文件夹 = 导入其全部内容（后端递归展开，含未展开部分）；
 * UI 上勾选目录时同步勾选其**已加载**的子孙，展开后子项自动跟进勾选。
 * （Windows 原生对话框在 wails beta.12 里文件夹模式与多选互斥，故自绘。）
 */
const props = defineProps<{ show: boolean }>();
const emit = defineEmits<{ "update:show": [show: boolean]; confirm: [paths: string[]] }>();

interface DirNode extends TreeOption {
  key: string;
  label: string;
  isLeaf: boolean;
  loaded?: boolean;
}

const treeData = ref<DirNode[]>([]);
const checkedKeys = ref<string[]>([]);
const expandedKeys = ref<Array<string | number>>([]);
const pathInput = ref("");
const error = ref("");
/** key → 节点 的索引，供级联勾选递归已加载子孙。 */
const nodesByKey = new Map<string, DirNode>();

function toNodes(entries: Awaited<ReturnType<typeof ListLocalFiles>>): DirNode[] {
  return (entries ?? []).map((entry) => ({
    key: entry.path,
    label: entry.name,
    isLeaf: !entry.isDir,
  }));
}

function registerNodes(nodes: DirNode[]): void {
  for (const node of nodes) {
    nodesByKey.set(node.key, node);
    if (node.children) registerNodes(node.children as DirNode[]);
  }
}

/** 收集某节点下**已加载**的全部子孙 key（未加载部分不在树里，交给后端递归）。 */
function loadedDescendants(key: string): string[] {
  const node = nodesByKey.get(key);
  if (!node?.children) return [];
  const out: string[] = [];
  for (const child of node.children as DirNode[]) {
    out.push(child.key);
    out.push(...loadedDescendants(child.key));
  }
  return out;
}

function onCheckedUpdate(keys: Array<string | number>): void {
  const next = new Set(keys.map(String));
  const prev = new Set(checkedKeys.value);
  for (const key of next) {
    if (!prev.has(key)) {
      // 新勾选目录：级联勾选其已加载子孙。
      for (const descendant of loadedDescendants(key)) next.add(descendant);
    }
  }
  for (const key of prev) {
    if (!next.has(key)) {
      // 取消目录：级联取消其已加载子孙。
      for (const descendant of loadedDescendants(key)) next.delete(descendant);
    }
  }
  checkedKeys.value = [...next];
}

async function loadChildren(node: DirNode): Promise<void> {
  if (node.loaded) return;
  node.loaded = true;
  try {
    const entries = (await ListLocalFiles(node.key)) ?? [];
    node.children = toNodes(entries);
    registerNodes(node.children as DirNode[]);
    // 父已勾选 → 新加载的子项同步勾选（孙目录展开时经同一逻辑继续传递）。
    if (checkedKeys.value.includes(node.key) && node.children.length > 0) {
      const set = new Set(checkedKeys.value);
      for (const child of node.children as DirNode[]) set.add(child.key);
      checkedKeys.value = [...set];
    }
  } catch {
    node.children = [];
    node.isLeaf = true; // 无权限/空目录：不再显示展开箭头
  }
}

async function jumpTo(path: string): Promise<void> {
  const target = path.trim();
  if (!target) return;
  error.value = "";
  try {
    const entries = (await ListLocalFiles(target)) ?? [];
    treeData.value = toNodes(entries);
    registerNodes(treeData.value);
    expandedKeys.value = [];
    checkedKeys.value = [];
    pathInput.value = target.replaceAll("/", "\\").replace(/\\+$/, "");
  } catch (e: any) {
    error.value = String(e?.message ?? e);
  }
}

async function jumpUp(): Promise<void> {
  const current = pathInput.value.trim();
  if (!current) return;
  const normalized = current.replaceAll("/", "\\").replace(/\\+$/, "");
  const parent = normalized.slice(0, Math.max(normalized.lastIndexOf("\\"), 0));
  await jumpTo(parent);
}

watch(
  () => props.show,
  async (show) => {
    if (!show) return;
    checkedKeys.value = [];
    error.value = "";
    pathInput.value = "";
    nodesByKey.clear();
    treeData.value = [];
    const drives = (await ListLocalFiles("")) ?? [];
    treeData.value = toNodes(drives);
    registerNodes(treeData.value);
    expandedKeys.value = [];
  }
);

async function onLoad(node: TreeOption): Promise<void> {
  await loadChildren(node as DirNode);
}

function onConfirm(): void {
  const paths = [...checkedKeys.value].sort();
  if (paths.length === 0) return;
  emit("confirm", paths);
  emit("update:show", false);
}
</script>

<template>
  <NModal
    :show="props.show"
    preset="card"
    title="选择要导入的文件 / 文件夹（可多选）"
    :style="{ width: 'min(560px, calc(100vw - 40px))' }"
    :mask-closable="true"
    @update:show="emit('update:show', $event)"
  >
    <div class="fp-body">
      <div class="fp-pathbar">
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
      </div>

      <NText v-if="error" depth="3" class="fp-error">{{ error }}</NText>

      <div class="fp-tree">
        <NTree
          block-line
          checkable
          :cascade="false"
          :data="treeData"
          :checked-keys="checkedKeys"
          :expanded-keys="expandedKeys"
          :on-load="onLoad"
          :on-update:checked-keys="onCheckedUpdate"
          :on-update:expanded-keys="(keys: Array<string | number>) => (expandedKeys = keys)"
          :animated="false"
          virtual-scroll
          :style="{ height: '340px' }"
        />
      </div>

      <div class="fp-footer-info">
        <NText depth="3">已勾选 {{ checkedKeys.length }} 项</NText>
        <NText depth="3" class="fp-hint">勾选文件夹 = 导入其全部内容（含未展开部分）</NText>
      </div>
    </div>

    <template #footer>
      <div class="fp-actions">
        <NButton quaternary @click="emit('update:show', false)">取消</NButton>
        <NButton
          type="primary"
          :disabled="checkedKeys.length === 0"
          @click="onConfirm"
        >
          确定（{{ checkedKeys.length }}）
        </NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.fp-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.fp-pathbar {
  display: flex;
  gap: 6px;
}
.fp-pathbar :deep(.n-input) {
  flex: 1;
}
.fp-error {
  font-size: 12px;
  color: #e88080;
}
.fp-tree {
  border: 1px solid var(--pvf-border-normal, rgba(255, 255, 255, 0.09));
  border-radius: 6px;
  padding: 4px;
  overflow: hidden;
}
.fp-footer-info {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.fp-hint {
  font-size: 11px;
}
.fp-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
