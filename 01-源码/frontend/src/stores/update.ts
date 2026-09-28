import { computed, ref } from "vue";
import { defineStore } from "pinia";
import { Events } from "@wailsio/runtime";
import { UpdateService } from "../../bindings/pvfine/services";
import type { UpdateInfo } from "../../bindings/pvfine/services/updateservice";

/** 官网下载页（后端同值，兜底用于界面显示）。 */
export const UPDATE_DOWNLOAD_PAGE = "https://www.mengfly.fun/";

function normalize(raw: any): UpdateInfo | null {
  if (!raw || typeof raw !== "object") return null;
  return {
    currentVersion: String(raw.currentVersion ?? ""),
    latestVersion: String(raw.latestVersion ?? ""),
    hasUpdate: Boolean(raw.hasUpdate),
    downloadUrl: String(raw.downloadUrl || UPDATE_DOWNLOAD_PAGE),
  };
}

/**
 * 「提示更新」状态：检测到新版本时弹窗，把官网下载地址交给用户
 * （可点击用系统浏览器打开，也可复制）。
 *
 * 触发来源有两个：① 设置里的「检查更新」按钮；② 后端后台检查发现新版本后
 * 发的 `app:update-available` 事件（应用启动约 3 秒后自动来）。
 */
export const useUpdateStore = defineStore("update", () => {
  const visible = ref(false);
  const checking = ref(false);
  const info = ref<UpdateInfo | null>(null);

  const downloadUrl = computed(() => info.value?.downloadUrl || UPDATE_DOWNLOAD_PAGE);
  const currentVersion = computed(() => info.value?.currentVersion ?? "");
  const latestVersion = computed(() => info.value?.latestVersion ?? "");

  function close(): void {
    visible.value = false;
  }

  /** 主动检查一次；返回结果供调用方提示（发现新版本时会自动弹窗）。 */
  async function check(): Promise<UpdateInfo | null> {
    if (checking.value) return null;
    checking.value = true;
    try {
      const result = normalize(await UpdateService.CheckForUpdates());
      info.value = result;
      if (result?.hasUpdate) visible.value = true;
      return result;
    } finally {
      checking.value = false;
    }
  }

  /** 用系统默认浏览器打开官网下载页。 */
  async function openPage(): Promise<boolean> {
    try {
      await UpdateService.OpenDownloadPage();
      return true;
    } catch {
      return false;
    }
  }

  /** 复制官网地址到剪贴板。 */
  async function copyUrl(): Promise<boolean> {
    try {
      return Boolean(await UpdateService.CopyDownloadURL());
    } catch {
      return false;
    }
  }

  Events.On("app:update-available", (event: any) => {
    const result = normalize(event?.data ?? event);
    if (!result) return;
    info.value = { ...result, hasUpdate: true };
    visible.value = true;
  });

  return {
    visible,
    checking,
    info,
    downloadUrl,
    currentVersion,
    latestVersion,
    close,
    check,
    openPage,
    copyUrl,
  };
});
