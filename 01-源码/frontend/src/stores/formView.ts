import { defineStore } from "pinia";
import { computed, ref, watch } from "vue";
import {
  ListFormats,
  ProjectFile,
  ReloadRules,
  type FormViewFormatInfo,
  type FormViewProjection,
} from "../services/formViewApi";
import {
  IsFormViewWindowOpen,
  OpenFormViewWindow,
  type FormViewSession,
} from "../services/formViewWindowApi";
import { useArchiveStore } from "./archive";

/**
 * 结构化视图状态：按规则把**文件**投影成「段 → 行 → 列」表格（只读）。
 *
 * 两种运行位置共用这一个 store：
 *   - 侧栏面板（主窗口内）
 *   - **独立窗口**（`?view=formview`，另一个 webview，**有自己的 store 实例**）
 *
 * 独立窗口拿不到主窗口的归档 store（每个 webview 各一份 Pinia），所以那里
 * 初始参数来自 Go 侧暂存的 `FormViewSession`；而**归档本身不用传** —— 投影是
 * Go 侧 core 在读已打开的归档，两个窗口共用同一个进程。
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

  /** 本实例是否跑在独立窗口里。 */
  const detached = ref(false);
  const windowOpen = ref(false);

  /** 归档切换时自增，用于丢弃迟到的响应。 */
  const sessionId = ref(0);

  const ready = computed(() => archive.open || detached.value);
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
    () => {
      // 独立窗口没有归档 store，路径恒为空，别让它把自己重置掉。
      if (!detached.value) reset();
    }
  );

  watch(formatId, (next) => {
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
      if (session !== sessionId.value) return;
      if (!detached.value && (archive.info?.path ?? "") !== archivePath) return;
      projection.value = result;
    } catch (issue: any) {
      if (session !== sessionId.value) return;
      error.value = String(issue?.message ?? issue);
    } finally {
      if (session === sessionId.value) projecting.value = false;
    }
  }

  /** 独立窗口启动时：用主窗口暂存的参数初始化，并立即投影一次。 */
  async function initFromSession(session: FormViewSession | null): Promise<void> {
    detached.value = true;
    await loadFormats();
    if (session) {
      if (session.formatId) formatId.value = session.formatId;
      if (session.filePath) filePath.value = session.filePath;
    }
    if (filePath.value.trim() !== "") await project();
  }

  /** 在独立窗口里打开当前这个「文件族 + 路径」。 */
  async function openInWindow(): Promise<void> {
    await OpenFormViewWindow({
      formatId: formatId.value,
      filePath: filePath.value.trim(),
    });
    windowOpen.value = true;
  }

  /** 查询独立窗口是否开着（面板显示状态用）。 */
  async function refreshWindowOpen(): Promise<void> {
    try {
      windowOpen.value = await IsFormViewWindowOpen();
    } catch {
      windowOpen.value = false;
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
    detached,
    windowOpen,
    canProject,
    formatOptions,
    currentFormat,
    loadFormats,
    reloadRules,
    project,
    initFromSession,
    openInWindow,
    refreshWindowOpen,
    reset,
  };
});
