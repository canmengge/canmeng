<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import {
  ArrowSync20Regular,
  ChevronDown20Regular,
  Copy20Regular,
  Delete20Regular,
  DocumentText20Regular,
} from "@vicons/fluent";
import { NButton, NIcon, NInput, NText, NTooltip, useMessage } from "naive-ui";
import { Events } from "@wailsio/runtime";
import { useLogStore, type LogLevelFilter } from "../stores/log";
import { clearTrace, markTrace, traceLines } from "../diagTrace";

/**
 * 输出日志面板：停靠在编辑区底部，与 HC\logs\pvfine-*.log 完全同源。
 *
 * - 顶部 4px 是**拖拽手柄**，可自由上下调节高度（高度写进 localStorage）；
 * - 底部是**日志搜索/过滤条**，紧贴面板下沿，随面板高度一起移动；
 * - 面板收在编辑列里，因此它的下方永远是状态栏，与截图里的位置一致。
 */
const log = useLogStore();
const message = useMessage();

const bodyRef = ref<HTMLElement | null>(null);
const resizing = ref(false);
let startY = 0;
let startHeight = 0;

const levelOptions: Array<{ label: string; value: LogLevelFilter }> = [
  { label: "全部", value: "all" },
  { label: "信息+", value: "info" },
  { label: "警告+", value: "warn" },
  { label: "仅错误", value: "error" },
];

async function scrollToBottom(): Promise<void> {
  await nextTick();
  const element = bodyRef.value;
  if (element) element.scrollTop = element.scrollHeight;
}

function onResizeStart(event: MouseEvent): void {
  resizing.value = true;
  startY = event.clientY;
  startHeight = log.height;
  document.body.style.cursor = "row-resize";
  event.preventDefault();
}

function onResizeMove(event: MouseEvent): void {
  if (!resizing.value) return;
  // 往上拖 = 变高：日志面板和它下面的搜索条一起往上长。
  log.setHeight(startHeight + (startY - event.clientY));
}

function onResizeEnd(): void {
  if (!resizing.value) return;
  resizing.value = false;
  document.body.style.cursor = "";
}

async function copyAll(): Promise<void> {
  const lines = log.filtered.map(
    (entry) => entry.text ?? `${entry.time} ${entry.level} [${entry.module}] ${entry.message} ${entry.fields ?? ""}`,
  );
  if (lines.length === 0) {
    message.info("当前没有可复制的日志");
    return;
  }
  try {
    await navigator.clipboard.writeText(lines.join("\n"));
    message.success(`已复制 ${lines.length} 行日志`);
  } catch (error: any) {
    message.error(`复制失败：${error?.message ?? error}`);
  }
}

// 面板自带诊断按钮：点击即把现场写进日志。
// 注意：若卡死发生在界面渲染线程，这里的按钮同样点不动——届时请改用
// 程序黑色控制台窗口输入 SCRZ 回车（不依赖界面线程，最可靠）。
async function runDiag(cmd: string): Promise<void> {
  const key = cmd.toUpperCase();
  switch (key) {
    case "SCRZ": {
      markTrace("手动 SCRZ", { 面板日志数: log.entries.length });
      try {
        await Events.Emit("dev:diag", { cmd: "SCRZ", uiTrace: traceLines() });
        message.success("已输出诊断现场（进程状态 / 卡住的打开动作 / 慢打开记录 / goroutine 栈）");
      } catch (error: any) {
        message.error(`诊断失败：${error?.message ?? error}`);
      }
      break;
    }
    case "QK": {
      clearTrace();
      message.success("已清空操作时间线");
      break;
    }
    case "HELP": {
      message.info(
        "诊断 = 输出卡死现场到日志 ｜ 清时间线 = 清空前端操作时间线 ｜ 界面完全卡死时请用程序控制台窗口输入 SCRZ",
      );
      break;
    }
  }
}

watch(
  () => log.filtered.length,
  () => {
    if (!log.paused) void scrollToBottom();
  },
);

watch(
  () => log.paused,
  (paused) => {
    if (!paused) void scrollToBottom();
  },
);

onMounted(() => {
  window.addEventListener("mousemove", onResizeMove);
  window.addEventListener("mouseup", onResizeEnd);
  void log.loadHistory().then(() => scrollToBottom());
  // 原生菜单「诊断 → 清空前端操作时间线」触发。
  Events.On("dev:diag-qk", () => {
    clearTrace();
    markTrace("菜单清空时间线");
  });
});

onBeforeUnmount(() => {
  window.removeEventListener("mousemove", onResizeMove);
  window.removeEventListener("mouseup", onResizeEnd);
});
</script>

<template>
  <div v-if="log.visible" class="log-panel" :style="{ height: log.height + 'px' }">
    <div class="log-resizer" title="拖动调节日志窗口高度" @mousedown="onResizeStart" />

    <div class="log-head">
      <NIcon :size="14" class="log-head-icon"><DocumentText20Regular /></NIcon>
      <NText strong class="log-title">输出日志</NText>
      <NTooltip trigger="hover">
        <template #trigger>
          <span class="log-count">
            {{ log.filtered.length.toLocaleString() }} / {{ log.entries.length.toLocaleString() }} 行
          </span>
        </template>
        显示行数 / 已缓存行数（最多 2000 行，且与 HC\logs 下的日志文件同源）
      </NTooltip>
      <span v-if="log.errorCount > 0" class="log-error-count">
        {{ log.errorCount }} 条错误
      </span>

      <div class="log-head-actions">
        <div class="log-levels">
          <NButton
            v-for="option in levelOptions"
            :key="option.value"
            size="tiny"
            quaternary
            :type="log.levelFilter === option.value ? 'primary' : 'default'"
            @click="log.levelFilter = option.value"
          >
            {{ option.label }}
          </NButton>
        </div>
        <NTooltip trigger="hover">
          <template #trigger>
            <NButton size="tiny" quaternary @click="log.paused ? log.resume() : (log.paused = true)">
              <template #icon>
                <NIcon><ArrowSync20Regular /></NIcon>
              </template>
              {{ log.paused ? "继续跟进" : "暂停跟进" }}
            </NButton>
          </template>
          暂停后新日志不再滚动进来（已显示的内容保留），方便翻看历史
        </NTooltip>
        <NButton size="tiny" quaternary @click="copyAll">
          <template #icon>
            <NIcon><Copy20Regular /></NIcon>
          </template>
          复制
        </NButton>
        <NButton size="tiny" quaternary @click="log.clear()">
          <template #icon>
            <NIcon><Delete20Regular /></NIcon>
          </template>
          清空
        </NButton>
        <NButton size="tiny" quaternary title="收起日志窗口" @click="log.toggle()">
          <template #icon>
            <NIcon><ChevronDown20Regular /></NIcon>
          </template>
        </NButton>
      </div>
    </div>

    <div ref="bodyRef" class="log-body">
      <div v-if="log.filtered.length === 0" class="log-empty">
        <NText depth="3">
          {{ log.entries.length === 0 ? "还没有日志" : "没有匹配的日志（试试清空过滤条件）" }}
        </NText>
      </div>
      <div
        v-for="(entry, index) in log.filtered"
        :key="`${entry.time}:${index}`"
        class="log-line"
        :class="`log-line--${entry.level.toLowerCase()}`"
      >
        <span class="log-time">{{ entry.time }}</span>
        <span class="log-level">{{ entry.level }}</span>
        <span class="log-module">[{{ entry.module }}]</span>
        <span class="log-message">{{ entry.message }}</span>
        <span v-if="entry.fields" class="log-fields">{{ entry.fields }}</span>
      </div>
      <div v-if="log.paused && log.unread > 0" class="log-unread" @click="log.resume()">
        有 {{ log.unread }} 条新日志（暂停中）· 点击继续跟进
      </div>
    </div>

    <!-- 紧贴日志窗口下沿的搜索条：面板高度变化时它跟着一起移动。 -->
    <div class="log-search">
      <NInput
        v-model:value="log.query"
        size="tiny"
        clearable
        placeholder="过滤日志（模块 / 消息 / 文件名 / 关键字）"
      >
        <template #prefix>
          <span class="log-search-hint">搜索</span>
        </template>
      </NInput>
      <NButton
        size="tiny"
        quaternary
        type="warning"
        title="界面卡顿/卡死时点击：把现场（卡住的打开动作、操作时间线、goroutine 栈）写进日志"
        @click="runDiag('SCRZ')"
      >
        诊断
      </NButton>
      <NButton size="tiny" quaternary title="清空前端操作时间线（SCRZ 的记录来源）" @click="runDiag('QK')">
        清时间线
      </NButton>
      <NText depth="3" class="log-search-count">
        命中 {{ log.filtered.length.toLocaleString() }} 行
      </NText>
    </div>
  </div>
</template>

<style scoped>
.log-panel {
  position: relative;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  min-height: 80px;
  border-top: 1px solid var(--pvf-border-normal);
  background: var(--pvf-surface-bar);
  overflow: hidden;
}
.log-resizer {
  position: absolute;
  top: -2px;
  left: 0;
  right: 0;
  height: 5px;
  cursor: row-resize;
  z-index: 5;
}
.log-resizer:hover {
  background: var(--pvf-effect-split-hover, rgba(127, 127, 127, 0.35));
}
.log-head {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 24px;
  padding: 0 8px;
  font-size: 11px;
  color: var(--pvf-text-muted);
  border-bottom: 1px solid var(--pvf-border-normal);
  flex-shrink: 0;
}
.log-head-icon {
  color: var(--pvf-text-faint);
}
.log-title {
  font-size: 11px;
}
.log-count,
.log-error-count {
  color: var(--pvf-text-faint);
}
.log-error-count {
  color: var(--pvf-error);
}
.log-head-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 2px;
}
.log-levels {
  display: inline-flex;
  align-items: center;
  gap: 0;
}
.log-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  padding: 2px 0 4px;
  font-family: var(--vscode-editor-font-family, Consolas, "Courier New", monospace);
  font-size: 11.5px;
  line-height: 1.5;
}
.log-line {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding: 0 8px;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--pvf-text-secondary);
}
.log-line:hover {
  background: var(--pvf-surface-hover);
}
.log-time {
  color: var(--pvf-text-faint);
  flex: 0 0 auto;
}
.log-level {
  flex: 0 0 auto;
  width: 42px;
  color: var(--pvf-text-muted);
}
.log-module {
  flex: 0 0 auto;
  color: var(--pvf-text-faint);
}
.log-message {
  color: var(--pvf-text-primary);
}
.log-fields {
  color: var(--pvf-text-muted);
}
.log-line--debug .log-message {
  color: var(--pvf-text-muted);
}
.log-line--warn .log-level,
.log-line--warn .log-message {
  color: var(--pvf-warning);
}
.log-line--error .log-level,
.log-line--error .log-message {
  color: var(--pvf-error);
}
.log-empty {
  padding: 10px 10px;
}
.log-unread {
  position: sticky;
  bottom: 0;
  margin: 4px 8px 0;
  padding: 3px 8px;
  border-radius: 4px;
  background: var(--pvf-primary-soft, rgba(64, 128, 255, 0.18));
  color: var(--pvf-primary);
  cursor: pointer;
  font-size: 11px;
}
.log-search {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
  border-top: 1px solid var(--pvf-border-normal);
  flex-shrink: 0;
}
.log-search-hint {
  color: var(--pvf-text-faint);
  font-size: 11px;
}
.log-search-count {
  flex-shrink: 0;
  font-size: 11px;
}
</style>
