import { defineStore } from "pinia";
import { computed, ref, watch } from "vue";
import {
  ListFormats,
  ProjectFile,
  ReloadRules,
  type FormViewFormatInfo,
  type FormViewProjection,
} from "../services/formViewApi";
import { useArchiveStore } from "./archive";

/**
 * 结构化视图面板状态：按规则把**文件**投影成「段 → 行 → 列」表格（只读）。
 *
 * 数据全部来自 Go 侧 `FormViewService`（只读投影），这里只负责取数与界面状态。
 * 与对象视图的区别：对象视图是「一个 ID → 关联文件」，这里是「一个文件 → 结构化表格」。
 */
export const useFormViewStore = defineStore("formView", () => {
  const archive = useArchiveStore();

  const formats = ref<FormViewFormatInfo[]>([]);
  const rulePath = ref("");
  const formatsLoading = ref(false);
  const formatsError = ref("");

  const formatId = ref("");
  const filePath = ref("");

  const projection = ref<FormViewProjection | null>(null);
  const projecting = ref(false);
  const error = ref("");

  /** 归档切换时自增，用于丢弃迟到的响应。 */
  const sessionId = ref(0);

  const ready = computed(() => archive.open);
  const canProject = computed(
    () => ready.value && !projecting.value && filePath.value.trim() !== ""
  );
  const formatOptions = computed(() =>
    formats.value.map((entry) => ({ label: entry.label, value: entry.id }))
  );
  const currentFormat = computed(
    () => formats.value.find((entry) => entry.id === formatId.value) ?? null
  );

  watch(
    () => archive.info?.path ?? "",
    () => reset()
  );

  watch(formatId, (next) => {
    // 切换文件族时把路径预填成规则里的第一个路径，省得手敲。
    const format = formats.value.find((entry) => entry.id === next);
    if (format && format.files.length > 0) {
      filePath.value = format.files[0];
    }
  });

  /** 读取文件族目录（不依赖已打开的归档）。 */
  async function loadFormats(force = false): Promise<void> {
    if (formatsLoading.value) return;
    if (!force && formats.value.length > 0) return;
    formatsLoading.value = true;
    formatsError.value = "";
    try {
      const result = await ListFormats();
      formats.value = result?.formats ?? [];
      rulePath.value = result?.rulePath ?? "";
      if (formatId.value === "" && formats.value.length > 0) {
        formatId.value = formats.value[0].id;
      }
    } catch (issue: any) {
      formatsError.value = String(issue?.message ?? issue);
    } finally {
      formatsLoading.value = false;
    }
  }

  /** 重新读取规则文件（config/formats.json）。 */
  async function reloadRules(): Promise<void> {
    if (formatsLoading.value) return;
    formatsLoading.value = true;
    formatsError.value = "";
    try {
      const result = await ReloadRules();
      formats.value = result?.formats ?? [];
      rulePath.value = result?.rulePath ?? "";
    } catch (issue: any) {
      formatsError.value = String(issue?.message ?? issue);
    } finally {
      formatsLoading.value = false;
    }
  }

  async function project(): Promise<void> {
    if (!canProject.value) return;
    const archivePath = archive.info?.path ?? "";
    const session = sessionId.value;
    projecting.value = true;
    error.value = "";
    projection.value = null;
    try {
      const result = await ProjectFile(filePath.value.trim());
      if (session !== sessionId.value || (archive.info?.path ?? "") !== archivePath) return;
      projection.value = result;
    } catch (issue: any) {
      if (session !== sessionId.value) return;
      error.value = String(issue?.message ?? issue);
    } finally {
      if (session === sessionId.value) projecting.value = false;
    }
  }

  /** 清空当前结果（归档切换、或用户手动清空）。 */
  function reset(): void {
    sessionId.value += 1;
    projection.value = null;
    error.value = "";
    projecting.value = false;
  }

  return {
    formats,
    rulePath,
    formatsLoading,
    formatsError,
    formatId,
    filePath,
    projection,
    projecting,
    error,
    ready,
    canProject,
    formatOptions,
    currentFormat,
    loadFormats,
    reloadRules,
    project,
    reset,
  };
});
