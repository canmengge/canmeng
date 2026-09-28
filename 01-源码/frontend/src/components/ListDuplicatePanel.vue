<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Clipboard } from "@wailsio/runtime";
import { NButton, NIcon, useMessage } from "naive-ui";
import {
  ArrowSync24Regular,
  Copy24Regular,
  Dismiss16Regular,
  DocumentSearch24Regular,
  Search24Regular,
} from "@vicons/fluent";
import type { ListDuplicateIssue, ListEntry } from "../listDuplicate";

const props = defineProps<{
  show: boolean;
  /** 当前查重的 .lst 路径（展示用）。 */
  fileName: string;
  issues: ListDuplicateIssue[];
  /** 本次检测时刻（毫秒时间戳）；0 表示未检测。 */
  checkedAt: number;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "locate", line: number): void;
  /** 重新检测当前打开的文件（重新解析 + 查重并刷新下方结果）。 */
  (e: "recheck"): void;
}>();

const message = useMessage();
const hasIssues = computed(() => props.issues.length > 0);

/**
 * 参与了「多处」重复关系的行号：同一行同时出现在两条以上问题里
 * （例如既和某行 ID 重复、又和另一行路径重复）——这类行在列表里加背景高亮。
 */
const multiHitLines = computed(() => {
  const counts = new Map<number, number>();
  for (const issue of props.issues) {
    for (const entry of issue.entries) {
      counts.set(entry.line, (counts.get(entry.line) ?? 0) + 1);
    }
  }
  const result = new Set<number>();
  for (const [line, count] of counts) {
    if (count > 1) result.add(line);
  }
  return result;
});

/** 检测时刻文案：让用户一眼确认结果对应的是「刚才那份内容」。 */
const checkedText = computed(() => {
  if (!props.checkedAt) return "";
  const date = new Date(props.checkedAt);
  const pad = (value: number) => String(value).padStart(2, "0");
  return `检测于 ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
});

// ---- 窗口几何：位置与尺寸都由用户拖动 / 拉伸决定（不再自动缩放）----
const MIN_WIDTH = 320;
const MIN_HEIGHT = 160;

const left = ref(360);
const top = ref(90);
const width = ref(620);
const height = ref(440);
const placed = ref(false);

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

/** 编辑器最大化时的默认尺寸（用户 2026-09-28 指定：658 × 710）。 */
const BASE_WIDTH = 658;
const BASE_HEIGHT = 710;

/**
 * 非最大化时同比例缩小：以屏幕可用尺寸（≈ 最大化后的视口）为基准算缩放因子。
 * 最大化时因子 = 1 ⇒ 尺寸就是 658 × 710；窗口越小，查重窗口按同比例变小。
 */
function viewportScale(): number {
  const maxWidth = window.screen?.availWidth || window.innerWidth;
  const maxHeight = window.screen?.availHeight || window.innerHeight;
  return Math.min(1, window.innerWidth / maxWidth, window.innerHeight / maxHeight);
}

/**
 * 首次打开时的默认位置与尺寸：右边缘贴着右侧「清单预览」侧栏的左边缘，
 * 顶部与编辑器内容区对齐；尺寸为基准的 5/3 倍（超出视口时自动收进可视区）。
 * 之后位置与大小完全由用户拖动 / 拉伸决定。
 */
function placeInitial(): void {
  if (placed.value) return;
  placed.value = true;

  const paneHost =
    document.querySelector<HTMLElement>(".pane-body") ??
    document.querySelector<HTMLElement>(".editor-pane");
  const paneRect = paneHost?.getBoundingClientRect();
  // 右侧「清单预览」所在侧栏（收起时宽度为 0，退回窗口右边缘）。
  const sidebarHost = document.querySelector<HTMLElement>(".file-set-sidebar");
  const sidebarRect = sidebarHost?.getBoundingClientRect();

  const viewportWidth = window.innerWidth;
  const viewportHeight = window.innerHeight;
  const rightLimit =
    sidebarRect && sidebarRect.width > 0 && sidebarRect.left > 0
      ? sidebarRect.left - 4
      : viewportWidth - 24;

  // 顶部与「清单预览」面板顶部对齐（侧栏取不到时退回编辑器内容区顶部）。
  const nextTop = clamp(
    sidebarRect && sidebarRect.height > 0 ? sidebarRect.top : (paneRect?.top ?? 74) + 12,
    0,
    Math.max(0, viewportHeight - 80)
  );
  const scale = viewportScale();
  const nextWidth = clamp(
    Math.round(BASE_WIDTH * scale),
    MIN_WIDTH,
    Math.max(MIN_WIDTH, rightLimit - 8)
  );
  const nextHeight = clamp(
    Math.round(BASE_HEIGHT * scale),
    MIN_HEIGHT,
    Math.max(MIN_HEIGHT, viewportHeight - nextTop - 40)
  );

  top.value = nextTop;
  width.value = nextWidth;
  height.value = nextHeight;
  left.value = clamp(rightLimit - nextWidth, 0, Math.max(0, viewportWidth - 80));
}

watch(
  () => props.show,
  (visible) => {
    if (visible) placeInitial();
  }
);

/** 指针拖拽的统一收尾（拖动 / 拉伸共用）。 */
function trackPointer(onMove: (event: PointerEvent) => void): void {
  const move = (event: PointerEvent) => onMove(event);
  const up = () => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    document.body.style.userSelect = "";
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
  document.body.style.userSelect = "none";
}

/** 拖动标题栏移动窗口（可在整个界面范围内自由移动）。 */
function startDrag(event: PointerEvent): void {
  if (event.button !== 0) return;
  const startX = event.clientX;
  const startY = event.clientY;
  const startLeft = left.value;
  const startTop = top.value;
  trackPointer((moveEvent) => {
    left.value = clamp(
      startLeft + moveEvent.clientX - startX,
      0,
      Math.max(0, window.innerWidth - 80)
    );
    top.value = clamp(
      startTop + moveEvent.clientY - startY,
      0,
      Math.max(0, window.innerHeight - 36)
    );
  });
}

type ResizeDir = "l" | "r" | "t" | "b" | "br";

/** 拉伸：左/右改宽、上/下改高，右下角同时改宽高。 */
function startResize(event: PointerEvent, dir: ResizeDir): void {
  if (event.button !== 0) return;
  event.preventDefault();
  event.stopPropagation();
  const startX = event.clientX;
  const startY = event.clientY;
  const start = { left: left.value, top: top.value, width: width.value, height: height.value };
  trackPointer((moveEvent) => {
    const dx = moveEvent.clientX - startX;
    const dy = moveEvent.clientY - startY;
    let nextLeft = start.left;
    let nextTop = start.top;
    let nextWidth = start.width;
    let nextHeight = start.height;

    if (dir === "r" || dir === "br") {
      nextWidth = Math.max(MIN_WIDTH, Math.min(start.width + dx, window.innerWidth - start.left - 8));
    }
    if (dir === "l") {
      nextWidth = Math.max(MIN_WIDTH, start.width - dx);
      nextLeft = start.left + (start.width - nextWidth);
      if (nextLeft < 0) {
        nextWidth += nextLeft;
        nextLeft = 0;
      }
    }
    if (dir === "b" || dir === "br") {
      nextHeight = Math.max(MIN_HEIGHT, Math.min(start.height + dy, window.innerHeight - start.top - 8));
    }
    if (dir === "t") {
      nextHeight = Math.max(MIN_HEIGHT, start.height - dy);
      nextTop = start.top + (start.height - nextHeight);
      if (nextTop < 0) {
        nextHeight += nextTop;
        nextTop = 0;
      }
    }

    left.value = nextLeft;
    top.value = nextTop;
    width.value = nextWidth;
    height.value = nextHeight;
  });
}

/** 单条复制内容：ID + 路径（不含行号与提示语）。 */
function entryText(entry: ListEntry): string {
  return `${entry.id}\t${entry.path}`;
}

/** 复制全部：文件名 + 每行 `第 N 行 / ID / 路径`（不含提示语，行号去重）。 */
function allText(): string {
  const seen = new Set<number>();
  const lines: string[] = [];
  for (const issue of props.issues) {
    for (const entry of issue.entries) {
      if (seen.has(entry.line)) continue;
      seen.add(entry.line);
      lines.push(`第 ${entry.line} 行\t${entry.id}\t${entry.path}`);
    }
  }
  if (lines.length === 0) return props.fileName;
  return [props.fileName, ...lines].join("\n");
}

async function copy(text: string, feedback: string): Promise<void> {
  try {
    // Wails 桌面端使用原生剪贴板，不受 WebView 的 Clipboard 权限限制。
    await Clipboard.SetText(text);
    message.success(feedback);
  } catch {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
        message.success(feedback);
      } else {
        message.error("复制失败：当前环境不支持剪贴板");
      }
    } catch {
      message.error("复制失败");
    }
  }
}
</script>

<template>
  <div
    v-if="show"
    class="ldp"
    :style="{
      left: `${left}px`,
      top: `${top}px`,
      width: `${width}px`,
      height: `${height}px`,
    }"
  >
    <div class="ldp-head" title="按住可拖动窗口" @pointerdown="startDrag">
      <NIcon :size="15" class="ldp-head-icon"><Search24Regular /></NIcon>
      <span class="ldp-title">list 查重</span>
      <span class="ldp-file" :title="fileName">{{ fileName }}</span>
      <span v-if="checkedText" class="ldp-time">{{ checkedText }}</span>
      <NButton
        size="tiny"
        quaternary
        title="重新检测当前打开的文件"
        @pointerdown.stop
        @click="emit('recheck')"
      >
        <template #icon><NIcon><ArrowSync24Regular /></NIcon></template>
        重新检测
      </NButton>
      <span class="ldp-count" :class="{ 'ldp-count--bad': hasIssues }">
        {{ hasIssues ? `${issues.length} 处问题` : "无重复" }}
      </span>
      <div class="ldp-head-actions" @pointerdown.stop>
        <NButton size="tiny" quaternary @click="copy(allText(), '已复制全部查重结果')">
          <template #icon><NIcon><Copy24Regular /></NIcon></template>
          复制全部
        </NButton>
        <NButton size="tiny" quaternary title="关闭" @click="emit('close')">
          <template #icon><NIcon><Dismiss16Regular /></NIcon></template>
        </NButton>
      </div>
    </div>

    <div class="ldp-body">
      <div v-if="!hasIssues" class="ldp-empty">未发现重复条目（ID 与路径均唯一）</div>
      <div v-for="(issue, index) in issues" :key="`${issue.kind}-${index}`" class="ldp-issue">
        <div class="ldp-issue-head">
          <span class="ldp-issue-msg">{{ issue.message }}</span>
        </div>
        <div
          v-for="entry in issue.entries"
          :key="entry.line"
          class="ldp-line"
          :class="{ 'ldp-line--multi': multiHitLines.has(entry.line) }"
          :title="multiHitLines.has(entry.line) ? '该行参与了多处重复（与其他行多组冲突）' : undefined"
        >
          <span class="ldp-line-no">第 {{ entry.line }} 行</span>
          <span class="ldp-line-id">{{ entry.id }}</span>
          <span class="ldp-line-path" :title="entry.path">{{ entry.path }}</span>
          <NButton
            size="tiny"
            quaternary
            class="ldp-line-copy"
            title="复制这一条（ID + 路径）"
            @click="copy(entryText(entry), '已复制该条')"
          >
            <template #icon><NIcon><Copy24Regular /></NIcon></template>
            复制
          </NButton>
          <NButton
            size="tiny"
            quaternary
            class="ldp-line-locate"
            title="定位到这一行"
            @click="emit('locate', entry.line)"
          >
            <template #icon><NIcon><DocumentSearch24Regular /></NIcon></template>
            定位
          </NButton>
        </div>
      </div>
    </div>

    <div class="ldp-foot">提示：此窗口可移动、可拉伸（拖动标题栏移动，拖动边缘或右下角调整大小）</div>

    <span class="ldp-resize ldp-resize--l" @pointerdown="(e) => startResize(e, 'l')" />
    <span class="ldp-resize ldp-resize--r" @pointerdown="(e) => startResize(e, 'r')" />
    <span class="ldp-resize ldp-resize--t" @pointerdown="(e) => startResize(e, 't')" />
    <span class="ldp-resize ldp-resize--b" @pointerdown="(e) => startResize(e, 'b')" />
    <span
      class="ldp-resize ldp-resize--br"
      title="拖动可调整大小"
      @pointerdown="(e) => startResize(e, 'br')"
    />
  </div>
</template>

<style scoped>
.ldp {
  position: fixed;
  display: flex;
  flex-direction: column;
  background: var(--pvf-surface-card);
  border: 1px solid var(--pvf-border-strong);
  border-radius: 10px;
  box-shadow: 0 14px 38px rgba(0, 0, 0, 0.46);
  /* 低于 naive-ui 弹窗层级，避免盖住确认框。 */
  z-index: 1500;
  overflow: hidden;
}
.ldp-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 7px 8px 7px 10px;
  background: rgba(128, 128, 128, 0.08);
  border-bottom: 1px solid var(--pvf-border-subtle);
  flex-shrink: 0;
  cursor: move;
  user-select: none;
}
.ldp-head-icon {
  color: var(--pvf-primary);
  flex-shrink: 0;
}
.ldp-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--pvf-text-primary);
  flex-shrink: 0;
}
.ldp-file {
  font-size: 11.5px;
  color: var(--pvf-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
}
.ldp-time {
  font-size: 11px;
  color: var(--pvf-text-muted);
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}
.ldp-count {
  font-size: 11px;
  font-weight: 700;
  padding: 1px 7px;
  border-radius: 999px;
  background: rgba(125, 211, 160, 0.18);
  color: #7dd3a0;
  flex-shrink: 0;
}
.ldp-count--bad {
  background: rgba(255, 77, 109, 0.2);
  color: #ff4d6d;
}
.ldp-head-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}
.ldp-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
/* 底部提示：告知用户窗口可移动 / 可拉伸。 */
.ldp-foot {
  flex-shrink: 0;
  padding: 4px 10px;
  font-size: 11px;
  color: var(--pvf-text-muted);
  background: rgba(128, 128, 128, 0.06);
  border-top: 1px solid var(--pvf-border-subtle);
  user-select: none;
}
.ldp-empty {
  padding: 14px 10px;
  font-size: 12.5px;
  color: #7dd3a0;
  text-align: center;
}
.ldp-issue {
  border: 1px solid rgba(255, 77, 109, 0.35);
  border-radius: 8px;
  overflow: hidden;
}
.ldp-issue-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 5px 8px;
  background: rgba(255, 77, 109, 0.12);
}
.ldp-issue-msg {
  font-size: 12.5px;
  font-weight: 700;
  color: #ff4d6d;
}
.ldp-line {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 8px;
  border-top: 1px solid var(--pvf-border-subtle);
  font-size: 12px;
}
/* 参与了多处重复的行：背景高亮，一眼看出它是"多组冲突"的关键行。 */
.ldp-line--multi {
  background: rgba(242, 201, 125, 0.18);
  box-shadow: inset 3px 0 0 #f2c97d;
}
.ldp-line-no {
  color: var(--pvf-text-muted);
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}
.ldp-line-id {
  color: #f2c97d;
  font-weight: 600;
  flex-shrink: 0;
  user-select: text;
}
.ldp-line-path {
  color: var(--pvf-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
  user-select: text;
}
.ldp-line-locate {
  flex-shrink: 0;
}

/* ---- 拉伸热区（左右上下 + 右下角）---- */
.ldp-resize {
  position: absolute;
  z-index: 2;
}
.ldp-resize--l,
.ldp-resize--r {
  top: 10px;
  bottom: 10px;
  width: 6px;
  cursor: ew-resize;
}
.ldp-resize--l {
  left: 0;
}
.ldp-resize--r {
  right: 0;
}
.ldp-resize--t,
.ldp-resize--b {
  left: 10px;
  right: 10px;
  height: 6px;
  cursor: ns-resize;
}
.ldp-resize--t {
  top: 0;
}
.ldp-resize--b {
  bottom: 0;
}
.ldp-resize--br {
  right: 0;
  bottom: 0;
  width: 16px;
  height: 16px;
  cursor: nwse-resize;
}
.ldp-resize--br::after {
  content: "";
  position: absolute;
  right: 3px;
  bottom: 3px;
  width: 9px;
  height: 9px;
  border-right: 2px solid var(--pvf-text-muted);
  border-bottom: 2px solid var(--pvf-text-muted);
  border-bottom-right-radius: 2px;
  opacity: 0.75;
}
</style>
