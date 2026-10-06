import { defineStore } from "pinia";
import { computed, ref, watch } from "vue";
import { Events } from "@wailsio/runtime";
import {
  ApplyCellEdits,
  ListFormats,
  ProjectFile,
  ProjectSectionOccurrence,
  ReloadRules,
  type FormViewCellEdit,
  type FormViewFormatInfo,
  type FormViewProjection,
  type FormViewSection,
} from "../services/formViewApi";
import {
  IsFormViewWindowOpen,
  OpenFormViewWindow,
  type FormViewSession,
} from "../services/formViewWindowApi";
import { useArchiveStore } from "./archive";

/**
 * 可视化编辑区状态：按规则把**文件**投影成「段 → 行 → 列」表格。
 *
 * 主表可改（双击改一格 / 批量改整列），改的是**归档内存**，落盘仍走主工具条「保存 PVF」；
 * 被行关联的内联列表（如独立掉落的 `[list]`）在查看器里**只读**。
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
  /** 正在应用单元格改动。 */
  const applying = ref(false);
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
    // 用户 2026-10-06 实测：在下拉里换了文件族，表格却还是上一个文件族的
    //（原来要手动在路径框里按回车才重新解析）⇒ 切族后**自动重新解析**，做到"丝滑切换"。
    // 归档没打开 / 正在解析时 project() 自己会跳过，不会出错。
    void project();
  });

  // 独立窗口关掉后复位标记（工具条「可视化编辑区」按钮的已打开状态）。
  Events.On("form-view:closed", () => {
    windowOpen.value = false;
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

  /**
   * 打开独立窗口。
   *
   * `preferredFormatId` 由入口指定（工具条「可视化编辑区」菜单给的是对应文件族）；
   * 缺省沿用当前选择。入口不一定挂过面板，所以这里先确保规则已加载，并给文件族
   * 兜底一个文件，避免开出一个空窗口。
   */
  async function openInWindow(preferredFormatId = ""): Promise<void> {
    await loadFormats();
    const target = preferredFormatId || formatId.value || formats.value[0]?.id || "";
    if (target !== "") formatId.value = target;
    const format = formats.value.find((entry) => entry.id === formatId.value);
    if (format && format.files.length > 0 && filePath.value.trim() === "") {
      filePath.value = format.files[0];
    }
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

  /**
   * 按需取「某段第 occurrence 次出现」。
   *
   * 主投影会把被行关联认领的目标段从输出里移除（实测该文件有 862 次 [list]），
   * 界面双击关联格时用本方法单独取那一次，避免为看一眼列表传整包。
   */
  async function loadLinkedSection(
    section: string,
    occurrence: number
  ): Promise<FormViewSection | null> {
    const path = filePath.value.trim();
    if (path === "") return null;
    return ProjectSectionOccurrence(path, section, occurrence);
  }

  /**
   * 应用单元格改动：服务端定位 / 校验 / 写回后回传新投影，本地直接替换。
   * 只写归档内存 —— 落盘仍走主工具条的「保存 PVF」。
   */
  async function applyEdits(edits: FormViewCellEdit[]): Promise<void> {
    if (edits.length === 0) return;
    applying.value = true;
    try {
      projection.value = await ApplyCellEdits(filePath.value.trim(), edits);
      error.value = "";
    } finally {
      applying.value = false;
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
    applying,
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
    loadLinkedSection,
    applyEdits,
    initFromSession,
    openInWindow,
    refreshWindowOpen,
    reset,
  };
});
