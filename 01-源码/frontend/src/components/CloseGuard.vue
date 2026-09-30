<script setup lang="ts">
import { onUnmounted, ref } from "vue";
import { Events } from "@wailsio/runtime";
import { useDialog } from "naive-ui";
import { useUnsavedChanges } from "../composables/unsavedChanges";
import { useEditorStore } from "../stores/editor";
import { markTrace } from "../diagTrace";

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
  // 关窗确认是数据安全路径：收到请求与最终判定都进操作时间线（控制台 SCRZ 可查），
  // 否则"内核拦下了但对话框没起"这种故障只能靠猜。
  markTrace(`收到关闭请求(${action})`, {
    未保存判定: hasUnsavedChanges.value,
    大文件段未提交: editor.pendingLargeEditCount,
  });
  if (pendingAction.value || closing.value) return;
  pendingAction.value = action;

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
