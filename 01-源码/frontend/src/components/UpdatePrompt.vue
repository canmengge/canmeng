<script setup lang="ts">
import { computed } from "vue";
import { NButton, NModal, NText, useMessage } from "naive-ui";
import { useUpdateStore } from "../stores/update";

/**
 * 「提示更新」窗口：检测到新版本时告知用户，并把官网下载地址给出去
 * —— 地址可点击跳转系统浏览器，也可一键复制；右下角可关闭。
 *
 * 不再走框架的自动下载安装：实测该流程会卡在更新窗口的事件握手，
 * 用户侧表现为"点了没反应"，故改为引导到官网下载（2026-09-27 用户裁定）。
 */
const update = useUpdateStore();
const message = useMessage();

const versionText = computed(() => {
  const from = update.currentVersion;
  const to = update.latestVersion;
  if (from && to) return `当前 v${from} → 最新 v${to}`;
  if (to) return `最新版本 v${to}`;
  return "";
});

async function onOpenPage(): Promise<void> {
  const ok = await update.openPage();
  if (!ok) message.error("无法打开系统浏览器，请手动访问下面的地址");
}

async function onCopy(): Promise<void> {
  const url = update.downloadUrl;
  if (await update.copyUrl()) {
    message.success("下载地址已复制");
    return;
  }
  try {
    await navigator.clipboard.writeText(url);
    message.success("下载地址已复制");
  } catch {
    message.error("复制失败，请手动选中地址后复制");
  }
}
</script>

<template>
  <NModal
    :show="update.visible"
    preset="card"
    title="发现新版本"
    :style="{ width: 'min(460px, calc(100vw - 48px))' }"
    :mask-closable="false"
    @update:show="(show: boolean) => !show && update.close()"
  >
    <div class="up">
      <NText v-if="versionText" depth="3">{{ versionText }}</NText>
      <NText depth="3">
        新版本请前往官网下载：下载完整包解压后覆盖原程序即可（配置、注释数据与知识库不受影响）。
      </NText>
      <div class="up-url">
        <span class="up-url__text" :title="update.downloadUrl">{{ update.downloadUrl }}</span>
        <NButton size="small" quaternary @click="onCopy">复制</NButton>
      </div>
    </div>
    <template #footer>
      <div class="up-actions">
        <NButton quaternary @click="update.close">关闭</NButton>
        <NButton type="primary" @click="onOpenPage">打开下载页面</NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.up {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.up-url {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px 6px 12px;
  border: 1px solid rgba(128, 128, 128, 0.35);
  border-radius: 8px;
  background: rgba(128, 128, 128, 0.12);
  user-select: text;
}
.up-url__text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, Consolas, monospace;
  font-size: 12.5px;
  cursor: text;
}
.up-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
