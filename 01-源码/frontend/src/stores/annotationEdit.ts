import { defineStore } from "pinia";
import { ref } from "vue";
import {
  GetPathAnnotation,
  RemovePathAnnotation,
  SetPathAnnotation,
} from "../services/pathAnnotationsApi";
import { useExplorerStore } from "./explorer";

/**
 * 「编辑注释」弹窗状态：文件树与代码编辑器的右键入口共用这一个弹窗。
 * 保存/删除由后端 PathAnnotationService 持久化（%AppConfig%\pvfine\path-annotations.json），
 * 后端会立即清注释缓存，这里只需在成功后刷新文件树标签。
 */
export const useAnnotationEditStore = defineStore("annotationEdit", () => {
  const visible = ref(false);
  const path = ref("");
  const isDir = ref(false);
  /** 当前生效的内置注释标题（无用户注释时预填进输入框，方便在原值上改）。 */
  const builtinTitle = ref("");
  const title = ref("");
  const content = ref("");
  /** 该路径是否已存在用户注释（决定「恢复默认」按钮是否可用）。 */
  const hasOverride = ref(false);
  const saving = ref(false);
  const error = ref("");

  async function openFor(entry: {
    path: string;
    isDir: boolean;
    builtinTitle?: string;
  }): Promise<void> {
    path.value = entry.path;
    isDir.value = entry.isDir;
    builtinTitle.value = entry.builtinTitle ?? "";
    title.value = entry.builtinTitle ?? "";
    content.value = "";
    hasOverride.value = false;
    error.value = "";
    visible.value = true;
    try {
      const result = await GetPathAnnotation(entry.path);
      if (result?.found && result.override) {
        hasOverride.value = true;
        title.value = result.override.title;
        content.value = result.override.content ?? "";
      }
    } catch (e: any) {
      error.value = String(e?.message ?? e);
    }
  }

  async function save(): Promise<void> {
    if (!path.value || saving.value) return;
    saving.value = true;
    error.value = "";
    try {
      await SetPathAnnotation(path.value, title.value.trim(), content.value.trim());
      visible.value = false;
      void useExplorerStore().refreshTreeTags();
    } catch (e: any) {
      error.value = String(e?.message ?? e);
    } finally {
      saving.value = false;
    }
  }

  /** 删除用户注释：该路径回退到软件内置注释。 */
  async function remove(): Promise<void> {
    if (!path.value || saving.value) return;
    saving.value = true;
    error.value = "";
    try {
      await RemovePathAnnotation(path.value);
      visible.value = false;
      void useExplorerStore().refreshTreeTags();
    } catch (e: any) {
      error.value = String(e?.message ?? e);
    } finally {
      saving.value = false;
    }
  }

  function close(): void {
    visible.value = false;
  }

  return {
    visible,
    path,
    isDir,
    builtinTitle,
    title,
    content,
    hasOverride,
    saving,
    error,
    openFor,
    save,
    remove,
    close,
  };
});
