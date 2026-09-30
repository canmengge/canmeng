<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from "vue";
import { NButton, NInputNumber, NTag, NTooltip } from "naive-ui";

/**
 * 大文件的「内置 TXT 视图」：不挂 CodeMirror，一切关联（注解 / 着色 / 名称标签 / 补全 /
 * 语法树 / 语法解析）全部不装。
 *
 * 起因（2026-09-30 实测，见操作时间线）：
 *   list/equipment.lst（2740 万字符 / 41 万行）在真实运行环境里 `new EditorView` 要
 *   **29.9 秒**，且**空文档挂载也一样**（即与文档无关），之后还反复出现 ~11 秒停摆。
 *   至此唯一可行的通道就是：大文件不进 CodeMirror。
 *
 * 做法：按行分页（默认每页 2000 行），只把**当前页**交给原生 textarea（一页约 130KB，
 * 打开与编辑都是毫秒级）。保存时按页序拼回全文交给归档（仍然是「保存 PVF」才落盘）。
 * 分页只在原文本上算一次（几十万行也只有几百个页），因此页边界与编辑互不干扰。
 */
const LINES_PER_PAGE = 2000;

const props = defineProps<{
  doc: string;
  readOnly?: boolean;
  /** 搜索结果定位请求：跳到目标行所在页。 */
  reveal?: { seq: number; needles: string[]; line?: number } | null;
}>();

const emit = defineEmits<{ (e: "change", text: string): void }>();

/** 当前用于切页的全文（保存后为我们自己拼出来的那一份，避免中途读到父组件的旧值）。 */
const master = ref(props.doc);
/** 每页首个字符的偏移；长度 = 页数。 */
const pageStarts = ref<number[]>([0]);
const pageCount = ref(1);
const lineCount = ref(1);
const pageIndex = ref(0);
const current = ref("");
const busy = ref(false);
/** 页号 → 编辑后的文本（只存动过的页）。 */
const edits = new Map<number, string>();
const dirtyPages = ref(0);
/** 最近一次我们自己发出去的内容：父组件回灌同一份时不必重解析。 */
let lastEmitted: string | null = null;

function parseDoc(text: string): void {
  busy.value = true;
  const starts: number[] = [0];
  let lines = 1;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10) {
      lines += 1;
      // 每满一页记一次起点：41 万行也只需约 200 个数字。
      if ((lines - 1) % LINES_PER_PAGE === 0) starts.push(i + 1);
    }
  }
  master.value = text;
  pageStarts.value = starts;
  lineCount.value = lines;
  pageCount.value = starts.length;
  edits.clear();
  dirtyPages.value = 0;
  pageIndex.value = 0;
  current.value = "";
  loadPage(0);
  busy.value = false;
}

function originalPage(p: number): string {
  const from = pageStarts.value[p] ?? 0;
  const to = pageStarts.value[p + 1] ?? master.value.length;
  return master.value.slice(from, to);
}

function pageText(p: number): string {
  return edits.get(p) ?? originalPage(p);
}

function loadPage(p: number, focus = false): void {
  const next = Math.min(Math.max(0, p), pageCount.value - 1);
  if (next === pageIndex.value && current.value === pageText(next)) return;
  pageIndex.value = next;
  current.value = pageText(next);
  void focus;
}

/** 把各页按顺序拼回全文（只有动过的页用新内容）。 */
function joined(): string {
  if (edits.size === 0) return master.value;
  let out = "";
  for (let p = 0; p < pageCount.value; p += 1) out += pageText(p);
  return out;
}

function onEdit(value: string): void {
  if (props.readOnly) return;
  current.value = value;
  edits.set(pageIndex.value, value);
  dirtyPages.value = edits.size;
}

/** 交给归档（内存），之后照常「保存 PVF」落盘。 */
function save(): void {
  const text = joined();
  const keep = pageIndex.value;
  lastEmitted = text;
  emit("change", text);
  parseDoc(text);
  loadPage(keep);
}

function goPage(p: number): void {
  loadPage(p);
}

/** 跳到指定行（搜索结果定位用）。 */
function goLine(line: number): void {
  const page = Math.floor(Math.max(0, line - 1) / LINES_PER_PAGE);
  loadPage(page);
}

defineExpose({
  /** 该通道不提供占位符插入（大文件不走注解链路），返回 false 让调用方走兜底提示。 */
  insertText: (): boolean => false,
  revealLine: goLine,
});

watch(
  () => props.doc,
  (doc) => {
    // 自己刚保存出去的内容不再重解析，否则会打断用户当前位置。
    if (lastEmitted !== null && doc === lastEmitted) return;
    parseDoc(doc);
  }
);

watch(
  () => props.reveal?.seq,
  () => {
    const line = props.reveal?.line;
    if (line && line > 0) goLine(line);
  }
);

onBeforeUnmount(() => {
  // 切标签/关标签时把改动交出去，避免丢内容（拼接只在此时做一次）。
  if (edits.size > 0) emit("change", joined());
});

parseDoc(props.doc);
</script>

<template>
  <div class="plain-text-view">
    <div class="ptv-bar">
      <NTag size="tiny" :bordered="false" type="warning">TXT 模式</NTag>
      <span class="ptv-hint">
        大文件不进编辑器：{{ lineCount.toLocaleString() }} 行 / 每页
        {{ LINES_PER_PAGE.toLocaleString() }} 行，只加载当前页
      </span>
      <span class="ptv-spacer" />
      <NButton size="tiny" quaternary :disabled="pageIndex === 0" @click="goPage(0)">
        首页
      </NButton>
      <NButton size="tiny" quaternary :disabled="pageIndex === 0" @click="goPage(pageIndex - 1)">
        上一页
      </NButton>
      <NInputNumber
        class="ptv-page-input"
        size="tiny"
        :min="1"
        :max="pageCount"
        :value="pageIndex + 1"
        :show-button="false"
        @update:value="(value: number | null) => goPage((value ?? 1) - 1)"
      />
      <span class="ptv-page-total">/ {{ pageCount.toLocaleString() }} 页</span>
      <NButton
        size="tiny"
        quaternary
        :disabled="pageIndex >= pageCount - 1"
        @click="goPage(pageIndex + 1)"
      >
        下一页
      </NButton>
      <NButton
        size="tiny"
        quaternary
        :disabled="pageIndex >= pageCount - 1"
        @click="goPage(pageCount - 1)"
      >
        末页
      </NButton>

      <NTooltip trigger="hover">
        <template #trigger>
          <NButton
            size="tiny"
            type="primary"
            :disabled="readOnly || dirtyPages === 0"
            @click="save"
          >
            保存到归档{{ dirtyPages > 0 ? ` (${dirtyPages})` : "" }}
          </NButton>
        </template>
        把改动写回归档内存（之后仍需「保存 PVF」才会落盘）
      </NTooltip>
    </div>

    <div v-if="readOnly" class="ptv-readonly">该文件类型不支持编辑，仅展示文本。</div>

    <textarea
      class="ptv-area"
      wrap="off"
      spellcheck="false"
      :readonly="readOnly"
      :value="current"
      @input="onEdit(($event.target as HTMLTextAreaElement).value)"
    />
  </div>
</template>

<style scoped>
.plain-text-view {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
}
.ptv-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  padding: 4px 8px;
  border-bottom: 1px solid var(--pvf-border-subtle, rgba(255, 255, 255, 0.08));
}
.ptv-hint {
  color: var(--pvf-text-secondary);
  font-size: 12px;
}
.ptv-spacer {
  flex: 1;
}
.ptv-page-input {
  width: 78px;
}
.ptv-page-total {
  color: var(--pvf-text-secondary);
  font-size: 12px;
}
.ptv-readonly {
  padding: 4px 8px;
  color: var(--pvf-warning);
  background: var(--pvf-surface-warning);
  font-size: 12px;
}
.ptv-area {
  flex: 1;
  min-height: 0;
  width: 100%;
  margin: 0;
  padding: 6px 10px;
  border: 0;
  outline: none;
  resize: none;
  overflow: auto;
  white-space: pre;
  tab-size: 4;
  color: var(--pvf-text-primary);
  background: transparent;
  font-family: "SF Mono", Menlo, Consolas, "Courier New", monospace;
  font-size: 13px;
  line-height: 1.55;
}
</style>
