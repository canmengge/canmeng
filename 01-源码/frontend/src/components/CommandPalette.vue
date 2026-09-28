<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { NEmpty, NInput, NModal, NSpin, type InputInst } from "naive-ui";
import { ArchiveService } from "../../bindings/pvfine/services";
import type { SearchHit } from "../../bindings/pvfine/services/models";
import { useAdvancedSearchStore } from "../stores/advancedSearch";
import { useArchiveStore } from "../stores/archive";
import { useAIStore } from "../stores/ai";
import { useEditorStore } from "../stores/editor";
import { useSidebarStore } from "../stores/sidebar";

/**
 * 命令面板（Ctrl+P / Ctrl+K）：
 * - 默认模式按文件名 / ID / 路径快速打开文件（走已有搜索索引）；
 * - 输入 `>`（或按 Tab）切换到命令模式，执行保存、分屏、面板切换等常用动作。
 */
const archive = useArchiveStore();
const editor = useEditorStore();
const sidebar = useSidebarStore();
const search = useAdvancedSearchStore();
const ai = useAIStore();

const visible = ref(false);
const query = ref("");
const hits = ref<SearchHit[]>([]);
const searching = ref(false);
const activeIndex = ref(0);
const inputRef = ref<InputInst | null>(null);
let requestId = 0;
let debounceTimer: ReturnType<typeof setTimeout> | null = null;

interface PaletteCommand {
  id: string;
  title: string;
  hint: string;
  run: () => void | Promise<void>;
}

const COMMANDS: PaletteCommand[] = [
  {
    id: "save",
    title: "保存当前标签",
    hint: "Ctrl+S",
    run: () => editor.saveActiveTab(),
  },
  {
    id: "save-as",
    title: "另存为新 PVF",
    hint: "Ctrl+Shift+S",
    run: () => editor.saveAs(),
  },
  {
    id: "close-tab",
    title: "关闭当前标签",
    hint: "Ctrl+W",
    run: () => {
      if (editor.activeKey !== null) editor.requestCloseTab(editor.activeKey, editor.activePaneId);
    },
  },
  {
    id: "split-columns",
    title: "左右分屏",
    hint: "Ctrl+\\",
    run: () => editor.split("columns"),
  },
  {
    id: "split-rows",
    title: "上下分屏",
    hint: "Ctrl+Shift+\\",
    run: () => editor.split("rows"),
  },
  {
    id: "advanced-search",
    title: "高级搜索（文件 / 名称 / ID）",
    hint: "Ctrl+Shift+F",
    run: () => search.open(),
  },
  {
    id: "build-search-index",
    title: "构建搜索索引（按需）",
    hint: "打开归档后默认不构建",
    run: () => archive.ensureSearchIndex(),
  },
  {
    id: "panel-filesets",
    title: "右侧面板：文件集",
    hint: "",
    run: () => sidebar.show("filesets"),
  },
  {
    id: "panel-objectview",
    title: "右侧面板：对象视图",
    hint: "",
    run: () => sidebar.show("objectview"),
  },
  {
    id: "ai-panel",
    title: "AI 助手面板",
    hint: "",
    run: () => sidebar.show("ai"),
  },
  {
    id: "ai-explain-file",
    title: "AI：解释当前文件",
    hint: "",
    run: () => {
      sidebar.show("ai");
      void ai.send("解释当前打开的文件的作用和结构，重点说明关键字段。");
    },
  },
  {
    id: "ai-check-file",
    title: "AI：校验当前文件",
    hint: "",
    run: () => {
      sidebar.show("ai");
      void ai.send("帮我检查当前文件有没有格式、引用、编码方面的问题。");
    },
  },
  {
    id: "toggle-sidebar",
    title: "显示 / 隐藏右侧面板",
    hint: "Ctrl+B",
    run: () => sidebar.toggle(),
  },
  {
    id: "open-archive",
    title: "打开 PVF 归档",
    hint: "Ctrl+O",
    run: () => archive.openDialog(),
  },
];

interface PaletteItem {
  key: string;
  label: string;
  detail: string;
  run: () => void | Promise<void>;
}

const commandMode = computed(() => query.value.startsWith(">"));

const filteredCommands = computed(() => {
  const term = query.value.slice(1).trim().toLowerCase();
  if (!term) return COMMANDS;
  return COMMANDS.filter(
    (command) => command.title.toLowerCase().includes(term) || command.id.includes(term)
  );
});

const items = computed<PaletteItem[]>(() => {
  if (commandMode.value) {
    return filteredCommands.value.map((command) => ({
      key: `cmd:${command.id}`,
      label: command.title,
      detail: command.hint,
      run: command.run,
    }));
  }
  return hits.value.map((hit) => ({
    key: `file:${hit.fileIndex}:${hit.path}`,
    label: hit.name || hit.path.slice(hit.path.lastIndexOf("/") + 1),
    detail: hit.path,
    run: () => editor.openFile(hit.fileIndex),
  }));
});

const emptyText = computed(() => {
  if (commandMode.value) return "没有匹配的命令";
  if (!archive.indexReady) return "搜索索引构建中，稍后再试";
  return "没有匹配的文件";
});

function focusInput(): void {
  inputRef.value?.focus();
}

function open(): void {
  visible.value = true;
  query.value = "";
  hits.value = [];
  activeIndex.value = 0;
  void nextTick(focusInput);
}

function close(): void {
  visible.value = false;
}

function toggle(): void {
  if (visible.value) close();
  else open();
}

defineExpose({ open, close, toggle });

watch(query, () => {
  activeIndex.value = 0;
  if (debounceTimer) clearTimeout(debounceTimer);
  const value = query.value.trim();
  if (commandMode.value || value.length === 0) {
    hits.value = [];
    searching.value = false;
    return;
  }
  debounceTimer = setTimeout(() => void runSearch(value), 120);
});

async function runSearch(term: string): Promise<void> {
  const request = ++requestId;
  searching.value = true;
  try {
    if (!archive.indexReady) {
      hits.value = [];
      return;
    }
    const result = await ArchiveService.Search(term, 0, 40);
    if (request !== requestId) return;
    hits.value = (result?.hits ?? []).filter((hit): hit is SearchHit => !!hit);
  } catch {
    if (request === requestId) hits.value = [];
  } finally {
    if (request === requestId) searching.value = false;
  }
}

function move(step: number): void {
  const total = items.value.length;
  if (total === 0) return;
  activeIndex.value = (activeIndex.value + step + total) % total;
}

async function runItem(index: number): Promise<void> {
  const item = items.value[index];
  if (!item) return;
  close();
  await item.run();
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === "ArrowDown") {
    event.preventDefault();
    move(1);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    move(-1);
  } else if (event.key === "Enter") {
    event.preventDefault();
    void runItem(activeIndex.value);
  } else if (event.key === "Escape") {
    event.preventDefault();
    close();
  } else if (event.key === "Tab") {
    event.preventDefault();
    query.value = commandMode.value ? "" : ">";
  }
}
</script>

<template>
  <NModal
    v-model:show="visible"
    preset="card"
    title="命令面板"
    :bordered="false"
    style="width: min(720px, calc(100vw - 32px))"
    @after-enter="focusInput"
  >
    <NInput
      ref="inputRef"
      v-model:value="query"
      :placeholder="
        commandMode
          ? '输入命令关键字…'
          : '输入文件名 / ID / 路径打开；输入 > 或按 Tab 切换命令模式'
      "
      clearable
      @keydown="onKeydown"
    />
    <div class="palette-body">
      <div v-if="searching" class="palette-status">
        <NSpin size="small" />
      </div>
      <NEmpty v-else-if="items.length === 0" :description="emptyText" size="small" />
      <template v-else>
        <button
          v-for="(item, index) in items"
          :key="item.key"
          type="button"
          class="palette-item"
          :class="{ 'palette-item--active': index === activeIndex }"
          @click="void runItem(index)"
          @mousemove="activeIndex = index"
        >
          <span class="palette-item-label">{{ item.label }}</span>
          <span class="palette-item-detail">{{ item.detail }}</span>
        </button>
      </template>
    </div>
    <div class="palette-footer">
      <span>↑↓ 选择</span>
      <span>Enter 执行</span>
      <span>Tab 切换 文件 / 命令</span>
      <span>Esc 关闭</span>
      <span v-if="!archive.indexReady" class="palette-footer-warn">索引构建中</span>
    </div>
  </NModal>
</template>

<style scoped>
.palette-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 340px;
  margin-top: 10px;
  overflow-y: auto;
}
.palette-status {
  display: flex;
  justify-content: center;
  padding: 16px 0;
}
.palette-item {
  display: flex;
  gap: 12px;
  align-items: baseline;
  justify-content: space-between;
  width: 100%;
  padding: 6px 10px;
  font-size: 13px;
  color: var(--pvf-text-primary, inherit);
  text-align: left;
  cursor: pointer;
  background: transparent;
  border: none;
  border-radius: 6px;
}
.palette-item--active {
  background: var(--pvf-editor-active-line, rgba(127, 127, 127, 0.18));
}
.palette-item-label {
  flex: none;
  max-width: 42%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.palette-item-detail {
  flex: 1;
  overflow: hidden;
  color: var(--pvf-text-faint, #8b949e);
  font-size: 12px;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.palette-footer {
  display: flex;
  gap: 14px;
  margin-top: 10px;
  color: var(--pvf-text-faint, #8b949e);
  font-size: 12px;
}
.palette-footer-warn {
  margin-left: auto;
  color: var(--pvf-warning, #d29922);
}
</style>
