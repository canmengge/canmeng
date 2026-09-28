import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { Events } from "@wailsio/runtime";
import { CancelImport, type ImportProgress } from "../services/importApi";
import type { ImportPreview } from "../../bindings/pvfine/services/models";

export type ImportMode = "text" | "raw";
/** 文件冲突处理策略（与 Go services.ImportConflict* 对应）。 */
export type ImportConflict = "overwrite" | "rename" | "skip" | "abort";

const emptyProgress = (): ImportProgress => ({
  phase: "",
  scanned: 0,
  total: 0,
  bytes: 0,
  running: false,
});

const phaseLabels: Record<string, string> = {
  scan: "扫描目录",
  read: "读取文件",
  stage: "写入暂存归档",
  index: "整理索引",
  install: "安装变更",
  done: "完成",
};

/** 文件导入弹窗状态，供工具栏和资源管理器共享。 */
export const useImportStore = defineStore("import", () => {
  const visible = ref(false);
  const targetDir = ref("");
  const mode = ref<ImportMode>("text");
  const sourcePaths = ref<string[]>([]);
  /** 文件冲突处理：覆盖 / 重命名 / 跳过 / 终止（默认覆盖）。 */
  const conflict = ref<ImportConflict>("overwrite");
  /** 导入成功后是否把变更文件收进右侧搜索视窗（默认勾选）。 */
  const addToSearchWindow = ref(true);
  const preview = ref<ImportPreview | null>(null);
  const running = ref(false);
  const error = ref("");
  /** 后端上报的进度（`import:progress` 事件）。 */
  const progress = ref<ImportProgress>(emptyProgress());
  const cancelRequested = ref(false);

  /** 进度文案：扫描/读取阶段给出数与量，其余给出阶段名。 */
  const progressText = computed(() => {
    const current = progress.value;
    const label = phaseLabels[current.phase] ?? "";
    if (!current.running || !label) return "";
    if (current.phase === "scan") {
      return `${label}… 已找到 ${current.scanned.toLocaleString()} 个文件`;
    }
    if (current.phase === "read") {
      const mb = (current.bytes / (1024 * 1024)).toFixed(1);
      return `${label}… ${current.scanned.toLocaleString()}/${current.total.toLocaleString()}（${mb} MB）`;
    }
    return `${label}…`;
  });

  function eventData(event: any): any {
    return event?.data ?? event;
  }

  function resetProgress(): void {
    progress.value = emptyProgress();
    cancelRequested.value = false;
  }

  function open(target = ""): void {
    if (running.value) return;
    targetDir.value = target.replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
    mode.value = "text";
    sourcePaths.value = [];
    preview.value = null;
    error.value = "";
    resetProgress();
    visible.value = true;
  }

  /** 往待导入列表追加本地文件/文件夹（去重；文件夹由后端递归展开）。 */
  function addFiles(paths: string[]): number {
    const seen = new Set(sourcePaths.value);
    let added = 0;
    for (const raw of paths) {
      const path = String(raw ?? "").trim();
      if (!path || seen.has(path)) continue;
      seen.add(path);
      sourcePaths.value.push(path);
      added += 1;
    }
    return added;
  }

  function removeFile(path: string): void {
    sourcePaths.value = sourcePaths.value.filter((item) => item !== path);
  }

  function clearFiles(): void {
    sourcePaths.value = [];
    preview.value = null;
  }

  function close(): void {
    visible.value = false;
    sourcePaths.value = [];
    preview.value = null;
    error.value = "";
    resetProgress();
  }

  /**
   * 请求取消正在进行的导入。
   *
   * 后端只在「扫描 / 读取 / 写入暂存归档」三个阶段响应取消，这三步都在活动归档
   * 被改动之前，所以取消后归档保持原样；已进入安装阶段时取消不会生效（很快结束）。
   */
  async function requestCancel(): Promise<void> {
    if (!running.value || cancelRequested.value) return;
    cancelRequested.value = true;
    try {
      await CancelImport();
    } catch {
      // 后端可能刚好结束（没有在跑的任务），忽略即可：调用方随后仍会拿到结果。
    }
  }

  Events.On("import:progress", (event: any) => {
    const data = eventData(event);
    progress.value = {
      phase: String(data?.phase ?? ""),
      scanned: Number(data?.scanned ?? 0),
      total: Number(data?.total ?? 0),
      bytes: Number(data?.bytes ?? 0),
      running: Boolean(data?.running ?? true),
    };
  });

  // 拖拽导入：Wails 原生文件拖放（绝对路径数组）。导入窗口打开时，拖进窗口的
  // 文件/文件夹全部收进待导入列表（文件夹由后端递归展开）。
  Events.On("common:WindowFilesDropped", (event: any) => {
    if (!visible.value || running.value) return;
    const data = eventData(event);
    const paths = Array.isArray(data) ? data : Array.isArray(data?.files) ? data.files : [];
    const dropped = paths.map((item: any) => String(item?.path ?? item ?? "")).filter(Boolean);
    if (dropped.length > 0) addFiles(dropped);
  });

  return {
    visible,
    targetDir,
    mode,
    sourcePaths,
    conflict,
    addToSearchWindow,
    preview,
    running,
    error,
    progress,
    progressText,
    cancelRequested,
    open,
    close,
    addFiles,
    removeFile,
    clearFiles,
    resetProgress,
    requestCancel,
  };
});
