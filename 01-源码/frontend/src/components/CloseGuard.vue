<script setup lang="ts">
import { onUnmounted, ref } from "vue";
import { Events } from "@wailsio/runtime";
import { useDialog } from "naive-ui";
import { useUnsavedChanges } from "../composables/unsavedChanges";
import { useEditorStore } from "../stores/editor";
import { markTrace, traceLines } from "../diagTrace";

type CloseAction = "close" | "quit";

const dialog = useDialog();
const hasUnsavedChanges = useUnsavedChanges();
const editor = useEditorStore();
const pendingAction = ref<CloseAction | null>(null);
const closing = ref(false);

function eventData(event: any): any {
  return event?.data ?? event;
}

function requestClose(action: CloseAction): void {
  if (pendingAction.value || closing.value) return;
  pendingAction.value = action;

  // ⚠ 这里**不做任何写回归档**（用户 2026-09-30 明确要求：只有用户点保存才允许写入）。
  // "有没有未保存修改"的判定来自 useUnsavedChanges，其中已包含大文件的待写段。
  markTrace(`收到关闭请求(${action})`, {
    未保存判定: hasUnsavedChanges.value,
    大文件待写段: editor.pendingLargeEditCount,
  });
  // 把这次判定直接写进日志文件（等价于自动敲一次 SCRZ）：
  // 关窗这条链出问题时，不必再让用户手动敲控制台命令，日志里直接有现场。
  void Events.Emit("dev:diag", { cmd: "SCRZ", uiTrace: traceLines() }).catch(() => {});

  if (!hasUnsavedChanges.value) {
    void confirmClose(action);
    return;
  }

  dialog.warning({
    title: "未保存的修改",
    content: "当前工作区有未保存的修改，退出后这些修改将丢失。确定继续吗？",
    positiveText: "退出",
    negativeText: "取消",
    onPositiveClick: () => confirmClose(action),
    onNegativeClick: cancelClose,
    onClose: cancelClose,
  });
}

async function confirmClose(action: CloseAction): Promise<void> {
  if (pendingAction.value !== action || closing.value) return;
  closing.value = true;
  const eventName = action === "quit" ? "app:quit-confirmed" : "app:close-confirmed";
  markTrace(`关闭已确认(${action})`);
  try {
    await Events.Emit(eventName);
  } catch (error) {
    console.error("confirm close failed", error);
    closing.value = false;
    pendingAction.value = null;
  }
}

function cancelClose(): void {
  if (closing.value) return;
  pendingAction.value = null;
  // 必须回一个"取消"：内核给关窗请求挂了 3 秒兜底放行，若不告诉它用户取消了，
  // 3 秒后窗口会被兜底关掉（用户明明点了"取消"）。
  void Events.Emit("app:close-cancelled");
}

const offQuitRequested = Events.On("app:quit-requested", () => requestClose("quit"));
const offCloseRequested = Events.On("app:close-requested", (event) => {
  const action = eventData(event)?.action === "quit" ? "quit" : "close";
  requestClose(action);
});

onUnmounted(() => {
  offQuitRequested();
  offCloseRequested();
});
</script>

<template />
