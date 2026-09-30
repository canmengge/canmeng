<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { NButton, NTag, useMessage } from "naive-ui";
import { GetFileLines } from "../services/largeTextApi";
import { useArchiveStore } from "../stores/archive";
import { useEditorStore } from "../stores/editor";

/**
 * 大文件的「连续全文 TXT 视图」（记事本那种一路滚下去的观感）。
 *
 * 做法：用 spacer 把整个文件的高度撑开（行数 × 固定行高），滚动到哪就只向后端要
 * 「当前视口附近的行」——DOM 里永远只有几百行。
 * 这样既有"整页全部显示"的连续观感，又没有任何一次是整篇文本进窗口
 * （那正是停摆 43 秒的根因，见 services/large_text.go 顶部注释）。
 *
 * 定位靠固定行高（ROW_HEIGHT），因此 textarea 的 line-height 必须严格等于它、
 * 且不折行（wrap=off，长行横向滚动）——这是精确虚拟定位的前提。
 */
const ROW_HEIGHT = 20;
/** 上下各多取的行数：滚动时不露白。 */
const OVERSCAN = 300;
/** 一次向后端要的行数。 */
const CHUNK = 2000;

const props = defineProps<{
  index: number;
  path: string;
  size: number;
  /** 搜索结果定位请求：跳到目标行所在位置。 */
  reveal?: { seq: number; needles: string[]; line?: number } | null;
}>();

const message = useMessage();
const archive = useArchiveStore();
const editor = useEditorStore();

const viewport = ref<HTMLDivElement | null>(null);
const totalLines = ref(0);
const editable = ref(true);
const loading = ref(false);
const saving = ref(false);

/** 已加载窗口：起始行（1 基）、行数、文本。 */
const winStart = ref(1);
const winCount = ref(0);
const winText = ref("");

/**
 * 本标签「改了但还没写回归档」的段数。
 *
 * ⚠ 本组件的铁律（用户 2026-09-30 明确要求）：**绝不自动保存**。
 * 打字、失焦、滚动、切标签，都只把改动记在前端（`editor.pendingLargeSegments`）；
 * 只有用户点「保存本段」或按 Ctrl+S，才写回归档内存（再「保存 PVF」才落盘）。
 * 打一半的字被自动写进去是不允许的 —— 之前那版会在失焦/滚动时自动写回，已被移除。
 */
const pendingCount = computed(() => editor.pendingSegmentsOf(props.index).length);

const spacerHeight = computed(() => `${Math.max(totalLines.value, 1) * ROW_HEIGHT}px`);
const winTop = computed(() => `${(winStart.value - 1) * ROW_HEIGHT}px`);
const winHeight = computed(() => `${Math.max(winCount.value, 1) * ROW_HEIGHT}px`);

/** 与后端 countLines 同口径：空串 0 行；末行无换行符也算一行。 */
function countLines(text: string): number {
  if (text === "") return 0;
  const breaks = (text.match(/\n/g) ?? []).length;
  return text.endsWith("\n") ? breaks : breaks + 1;
}

let fetchSeq = 0;

async function loadWindow(startLine: number): Promise<void> {
  const seq = ++fetchSeq;
  loading.value = true;
  try {
    const chunk = await GetFileLines(props.index, startLine, CHUNK);
    if (!chunk || seq !== fetchSeq) return;
    totalLines.value = chunk.lines;
    editable.value = chunk.editable;
    winStart.value = chunk.start;
    // 先把归档内容显示出来：即使下面"叠加未写回段"这一步出问题，也不至于整块空白。
    winCount.value = chunk.count;
    winText.value = chunk.text;
    // 这一段若用户改过、还没写回归档，就显示用户自己的内容（滚走再滚回来也还在）。
    try {
      const pending = editor
        .pendingSegmentsOf(props.index)
        .find((segment) => segment.start === chunk.start);
      if (pending) {
        winCount.value = pending.count;
        winText.value = pending.text;
      }
    } catch {
      // store 版本落后（热更新只换了组件）等情况：忽略叠加，保持可用。
    }
  } catch (error: any) {
    message.error(`读取第 ${startLine} 行起的内容失败：${error?.message ?? error}`);
  } finally {
    if (seq === fetchSeq) loading.value = false;
  }
}

/**
 * Ctrl+S：与普通文件完全一致 —— 把本标签的改动写进归档内存（再由工具栏「保存 PVF」落盘）。
 *
 * 实现上直接调 `editor.saveTab`：它内部会先把 TXT 视图的"待写回段"落到归档内存，
 * 因此 TXT 视图不再需要单独一个「保存本段」按钮（用户 2026-09-30 要求合并掉那一步）。
 */
async function saveNow(): Promise<void> {
  if (pendingCount.value === 0) return;
  saving.value = true;
  try {
    const saved = await editor.saveTab(props.index);
    if (!saved) return;
    await archive.refreshInfo();
    // 写回后把窗口重新对齐到同一位置：用户看到的就是刚写进去的内容。
    await loadWindow(winStart.value);
    message.success("已写回归档内存，请点工具栏「保存 PVF」落盘");
  } catch (error: any) {
    message.error(`写回归档失败：${error?.message ?? error}`);
  } finally {
    saving.value = false;
  }
}

/** 后端交互串行化：滚动容易连发请求。 */
let queue: Promise<unknown> = Promise.resolve();
function enqueue(task: () => Promise<unknown>): void {
  queue = queue.then(task).catch(() => undefined);
}

function visibleRange(): { start: number; end: number } {
  const el = viewport.value;
  if (!el) return { start: 1, end: CHUNK };
  const first = Math.floor(el.scrollTop / ROW_HEIGHT) + 1;
  const rows = Math.ceil(el.clientHeight / ROW_HEIGHT);
  return { start: Math.max(1, first - OVERSCAN), end: first + rows + OVERSCAN };
}

let raf = 0;
function onScroll(): void {
  if (raf) return;
  raf = requestAnimationFrame(() => {
    raf = 0;
    const need = visibleRange();
    const haveStart = winStart.value;
    const haveEnd = winStart.value + winCount.value;
    if (need.start >= haveStart && need.end <= haveEnd) return;
    // 滚动只换窗口、不写归档：改过的段留在前端（loadWindow 会把它显示回来）。
    enqueue(async () => {
      await loadWindow(need.start);
    });
  });
}

function onInput(event: Event): void {
  winText.value = (event.target as HTMLTextAreaElement).value;
  winCount.value = countLines(winText.value);
  // 只记在前端；用户没点保存之前绝不写回归档。
  editor.setPendingLargeSegment(props.index, winStart.value, winCount.value, winText.value);
}

/** 段内 Ctrl/Cmd+S = 与普通文件一致：写回归档内存（不再是单独的"保存本段"）。 */
function onKeydown(event: KeyboardEvent): void {
  if ((event.ctrlKey || event.metaKey) && event.code === "KeyS") {
    event.preventDefault();
    void saveNow();
  }
}



/** 供搜索定位使用：跳到指定行（只换窗口，不写归档）。 */
async function gotoLine(line: number): Promise<void> {
  enqueue(async () => {
    await loadWindow(Math.max(1, line - 50));
  });
  const el = viewport.value;
  if (el) el.scrollTop = Math.max(0, (Math.max(1, line) - 1) * ROW_HEIGHT);
}

defineExpose({ revealLine: gotoLine });

// 搜索/AI 定位：跳到大文件的目标行（连续视图里就是滚动 + 加载那一段）。
watch(
  () => props.reveal?.seq,
  () => {
    const line = props.reveal?.line;
    if (line && line > 0) void gotoLine(line);
  }
);

onMounted(() => {
  void loadWindow(1);
});

onBeforeUnmount(() => {
  // 刻意什么都不做：未写回的段留在 store 里（切标签不丢），
  // 关标签 / 关窗口会由确认框提示（见 editor.hasPendingLargeEdits / useUnsavedChanges）。
});
</script>

<template>
  <div class="lsc-root">
    <div class="lsc-bar">
      <NTag size="tiny" :bordered="false" type="warning">TXT 模式</NTag>
      <span class="lsc-hint">
        连续全文 {{ totalLines.toLocaleString() }} 行 · 文本留在后端，滚到哪取到哪
      </span>
      <span class="lsc-gap" />
      <span v-if="saving" class="lsc-state">写回归档中…</span>
      <span v-else-if="pendingCount > 0" class="lsc-state lsc-state--dirty">
        有 {{ pendingCount }} 段改动未保存（按 Ctrl+S 或点上方的「保存」按钮）
      </span>
      <span v-else-if="loading" class="lsc-state">加载中…</span>
    </div>

    <div ref="viewport" class="lsc-viewport" @scroll="onScroll">
      <div class="lsc-spacer" :style="{ height: spacerHeight }">
        <textarea
          class="lsc-window"
          :style="{ top: winTop, height: winHeight }"
          wrap="off"
          spellcheck="false"
          :readonly="!editable"
          :value="winText"
          @input="onInput"
          @keydown="onKeydown"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.lsc-root {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
}
.lsc-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  padding: 4px 8px;
  border-bottom: 1px solid var(--pvf-border-subtle, rgba(255, 255, 255, 0.08));
}
.lsc-hint,
.lsc-state {
  color: var(--pvf-text-secondary);
  font-size: 12px;
}
.lsc-state--dirty {
  color: var(--pvf-warning);
}
.lsc-gap {
  flex: 1;
}
/* 滚动容器：整个文件的高度由里面的 spacer 撑开 */
.lsc-viewport {
  flex: 1;
  min-height: 0;
  overflow: auto;
  position: relative;
}
.lsc-spacer {
  position: relative;
  width: 100%;
}

/* 视口窗口：只有这几百行真的在 DOM 里 */
.lsc-window {
  position: absolute;
  left: 0;
  right: 0;
  display: block;
  width: 100%;
  margin: 0;
  padding: 0 10px;
  border: 0;
  outline: none;
  resize: none;
  overflow: hidden;
  white-space: pre;
  tab-size: 4;
  color: var(--pvf-text-primary);
  background: transparent;
  font-family: "SF Mono", Menlo, Consolas, "Courier New", monospace;
  font-size: 13px;
  /* 必须等于脚本里的 ROW_HEIGHT，否则虚拟定位会漂移 */
  line-height: 20px;
}
</style>
