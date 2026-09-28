<script setup lang="ts">
import { watch } from "vue";
import { useDialog } from "naive-ui";
import { useArchiveStore } from "../stores/archive";

// 统一处理"打开归档失败"的顶层弹窗。放在 NDialogProvider 之内，任何入口
// （工具栏 / 状态栏 / 快捷键 / 命令面板 / 拖拽）打开失败都会走到这里。
const archive = useArchiveStore();
const dialog = useDialog();

watch(
  () => archive.openError,
  (error) => {
    if (!error) return;
    if (error.kind === "skdat") {
      dialog.warning({
        title: "提示",
        content: "[注意] ⚠️⚠️⚠️ 打开归档时，请确认归档旁边有配套的 sk.dat 文件（一般在客户端里）。",
        positiveText: "知道了",
      });
    }
    archive.clearOpenError();
  }
);
</script>

<template>
  <span class="archive-error-dialog" aria-hidden="true" />
</template>
