import { defineStore } from "pinia";
import { ref, computed } from "vue";
import { Events } from "@wailsio/runtime";
import { ArchiveService, EditorService } from "../../bindings/pvfine/services";
import type {
  ArchiveInfo,
  FileRegistration,
  FileRegistrationOptions,
  IndexHashRegistrationResult,
  IndexHashTarget,
  IndexStatus,
} from "../../bindings/pvfine/services/models";

const recentArchivesKey = "pvfine.recentArchives";
const maxRecentArchives = 8;

/** 归档全局状态:打开/关闭/统计/解包进度 */
export const useArchiveStore = defineStore("archive", () => {
  const recentArchives = ref<string[]>(readRecentArchives());
  const info = ref<ArchiveInfo | null>(null);
  const loading = ref(false);
  const loadError = ref("");
  /** 打开失败的一次性提示：kind=skdat 时前端弹「缺 sk.dat」可关闭弹窗。 */
  const openError = ref<{ kind: "skdat" | "other"; message: string } | null>(null);
  /** 保存残留临时文件（.pvftmp）路径：打开归档时检测到上次保存被中断则置非空，供界面提示。 */
  const saveRemnant = ref<string | null>(null);
  const indexStatus = ref<IndexStatus>({
    state: "idle",
    stage: "",
    done: 0,
    total: 0,
    skipped: 0,
    error: "",
    refreshing: false,
    refreshError: "",
    cacheHit: false,
    openDurationMs: 0,
    buildDurationMs: 0,
  });

  // 解包状态
  const unpacking = ref(false);
  const unpackProgress = ref({ done: 0, total: 0 });
  const unpackMessage = ref("");

  const open = computed(() => !!info.value && info.value.path !== "");
  const modifiedCount = computed(() => info.value?.modifiedCount ?? 0);
  const indexing = computed(() => open.value && indexStatus.value.state === "building");
  const refreshingIndex = computed(() => open.value && indexStatus.value.refreshing);
  const indexReady = computed(() => open.value && indexStatus.value.state === "ready");
  let indexPollTimer: number | undefined;
  let indexPollBusy = false;

  function readRecentArchives(): string[] {
    if (typeof window === "undefined") return [];
    try {
      const raw = window.localStorage.getItem(recentArchivesKey);
      if (!raw) return [];
      const parsed: unknown = JSON.parse(raw);
      if (!Array.isArray(parsed)) return [];
      const unique: string[] = [];
      for (const path of parsed) {
        if (typeof path !== "string" || path.trim() === "" || unique.includes(path)) continue;
        unique.push(path);
        if (unique.length >= maxRecentArchives) break;
      }
      return unique;
    } catch {
      return [];
    }
  }

  function persistRecentArchives(): void {
    try {
      window.localStorage.setItem(recentArchivesKey, JSON.stringify(recentArchives.value));
    } catch {
      // 本地存储不可用时仍保留本次运行内的记录。
    }
  }

  function rememberArchive(path: string): void {
    if (!path.trim()) return;
    recentArchives.value = [
      path,
      ...recentArchives.value.filter((item) => item !== path),
    ].slice(0, maxRecentArchives);
    persistRecentArchives();
  }

  function clearRecentArchives(): void {
    recentArchives.value = [];
    persistRecentArchives();
  }

  function removeRecentArchive(path: string): void {
    recentArchives.value = recentArchives.value.filter((item) => item !== path);
    persistRecentArchives();
  }

  function readIndexStatus(data: any): IndexStatus {
    return {
      state: String(data?.state ?? "idle"),
      stage: String(data?.stage ?? ""),
      done: Number(data?.done ?? 0),
      total: Number(data?.total ?? 0),
      skipped: Number(data?.skipped ?? 0),
      error: String(data?.error ?? ""),
      refreshing: Boolean(data?.refreshing ?? false),
      refreshError: String(data?.refreshError ?? ""),
      cacheHit: Boolean(data?.cacheHit ?? false),
      openDurationMs: Number(data?.openDurationMs ?? 0),
      buildDurationMs: Number(data?.buildDurationMs ?? 0),
    };
  }

  function eventData(event: any): any {
    return event?.data ?? event;
  }

  /** 用户主动取消（原生对话框返回 cancel）不算打开失败。 */
  function isOpenCancelled(message: string): boolean {
    const lower = message.toLowerCase();
    return lower.includes("cancel") || message.includes("已取消");
  }

  /**
   * 路径不存在（"记住的上次归档"被搬走 / 改名）—— 这不是"打开失败"，
   * 不该弹红框吓人。用户 2026-10-03 截图：启动时自动打开 `D:\115us\PVF Ai Agent\Script.pvf`
   * 而那个文件夹已经不在了，结果弹了一个「打开 PVF 失败」的错误框。
   */
  function isMissingPath(message: string): boolean {
    const lower = message.toLowerCase();
    return (
      lower.includes("cannot find the path") ||
      lower.includes("no such file") ||
      lower.includes("系统找不到指定的路径") ||
      lower.includes("找不到指定的路径") ||
      lower.includes("系统找不到指定的文件") ||
      lower.includes("the system cannot find the file")
    );
  }

  /**
   * 记录一次打开失败，供顶层 ArchiveErrorDialog 统一弹窗（含「选择 sk.dat 文件…」按钮）。
   * 2026-09-29：用户取消不算失败；真正的失败也不再由各入口各发一条右下角 message。
   * 2026-10-03：**路径不存在**同样不算失败 —— 清掉这条失效记录，交给状态栏给一句人话提示。
   */
  function recordOpenError(e: any): void {
    const message = String(e?.message ?? e);
    if (isOpenCancelled(message)) return;
    if (isMissingPath(message)) {
      // 把失效路径从"最近归档"里剔除，免得每次启动都拿它去撞一次墙。
      const stale = message.match(/[A-Za-z]:\\[^\r\n]*?\.pvf/i)?.[0]?.trim();
      if (stale) {
        recentArchives.value = recentArchives.value.filter((item) => item !== stale);
        persistRecentArchives();
      }
      loadError.value = message;
      return;
    }
    openError.value = { kind: message.includes("sk.dat") ? "skdat" : "other", message };
  }

  function clearOpenError(): void {
    openError.value = null;
  }

  function clearSaveRemnant(): void {
    saveRemnant.value = null;
  }

  function stopIndexPolling() {
    window.clearInterval(indexPollTimer);
    indexPollTimer = undefined;
  }

  async function refreshIndexStatus() {
    if (!open.value || indexPollBusy) return;
    indexPollBusy = true;
    try {
      const status = readIndexStatus(await ArchiveService.IndexStatus());
      indexStatus.value = status;
      // idle = 尚未构建（A-01 按需构建），无需继续轮询。
      if (status.state === "ready" || status.state === "error" || status.state === "idle") {
        stopIndexPolling();
      }
    } catch {
      // Event delivery remains the primary path; a transient poll failure is harmless.
    } finally {
      indexPollBusy = false;
    }
  }

  function startIndexPolling() {
    stopIndexPolling();
    void refreshIndexStatus();
    indexPollTimer = window.setInterval(() => void refreshIndexStatus(), 250);
  }

  function applyOpenedInfo(res: ArchiveInfo, poll = true): ArchiveInfo {
    info.value = res;
    rememberArchive(res.path);
    // A-01：语义索引按需构建，打开时不自动开始，所以初始状态是"未构建"。
    indexStatus.value = readIndexStatus({ state: "idle", stage: "deferred" });
    if (poll) startIndexPolling();
    return res;
  }

  async function openPath(path: string): Promise<ArchiveInfo | null> {
    if (!path.trim()) return null;
    loading.value = true;
    loadError.value = "";
    try {
      const res = await ArchiveService.Open(path);
      if (!res) return null;
      return applyOpenedInfo(res);
    } catch (e: any) {
      loadError.value = String(e?.message ?? e);
      recordOpenError(e);
      // 不向上抛：打开失败的提示统一由顶层 ArchiveErrorDialog 弹窗，各入口无需处理。
      return null;
    } finally {
      loading.value = false;
    }
  }

  async function openDialog() {
    loading.value = true;
    loadError.value = "";
    try {
      const res = await ArchiveService.OpenDialog();
      if (res) {
        applyOpenedInfo(res, false);
        indexStatus.value = readIndexStatus(await ArchiveService.IndexStatus());
        startIndexPolling();
      }
    } catch (e: any) {
      loadError.value = String(e?.message ?? e);
      recordOpenError(e);
      // 同上：不向上抛，避免各入口出现未捕获的 Promise 拒绝。
    } finally {
      loading.value = false;
    }
  }

  async function close() {
    await ArchiveService.Close();
    stopIndexPolling();
    info.value = null;
  }

  async function refreshInfo() {
    info.value = await ArchiveService.Info();
  }

  async function rebuildSearchIndex(): Promise<IndexStatus> {
    const status = readIndexStatus(await ArchiveService.RebuildSearchIndex());
    indexStatus.value = status;
    startIndexPolling();
    return status;
  }

  /** A-01：语义索引按需构建——已就绪或正在构建时什么都不做，否则开始构建。 */
  async function ensureSearchIndex(): Promise<void> {
    if (!open.value) return;
    if (indexStatus.value.state === "ready" || indexStatus.value.state === "building") return;
    await rebuildSearchIndex();
  }

  async function listRegistrationOptions(fileIndex: number): Promise<FileRegistrationOptions | null> {
    return (await ArchiveService.ListRegistrationOptions(fileIndex)) ?? null;
  }

  async function registerFileToList(
    fileIndex: number,
    listPath: string,
    id: string,
  ): Promise<FileRegistration | null> {
    return (await ArchiveService.RegisterFileToList(fileIndex, listPath, id)) ?? null;
  }

  async function indexHashTargets(): Promise<IndexHashTarget[]> {
    return ((await ArchiveService.IndexHashTargets()) ?? []).filter(
      (target): target is IndexHashTarget => !!target,
    );
  }

  async function registerMissingIndexHashes(
    listPath: string,
    ids: string[],
  ): Promise<IndexHashRegistrationResult | null> {
    return (await ArchiveService.RegisterMissingIndexHashes(listPath, ids)) ?? null;
  }

  async function unpackDialog(): Promise<boolean> {
    unpackMessage.value = "";
    const started = await EditorService.UnpackDialog();
    unpacking.value = started;
    return started;
  }

  function cancelUnpack() {
    EditorService.CancelUnpack();
  }

  // 后端事件
  Events.On("archive:opened", (event: any) => {
    const data = eventData(event);
    applyOpenedInfo(data);
    loading.value = false;
  });
  Events.On("archive:closed", () => {
    stopIndexPolling();
    info.value = null;
    indexStatus.value = readIndexStatus(null);
  });
  Events.On("archive:index-progress", (event: any) => {
    indexStatus.value = readIndexStatus(eventData(event));
  });
  Events.On("archive:index-ready", (event: any) => {
    stopIndexPolling();
    indexStatus.value = readIndexStatus(eventData(event));
  });
  Events.On("archive:index-error", (event: any) => {
    stopIndexPolling();
    const data = eventData(event);
    indexStatus.value = readIndexStatus(data);
  });
  Events.On("archive:saved", (event: any) => {
    info.value = eventData(event);
  });
  Events.On("archive:changed", (event: any) => {
    const data = eventData(event);
    if (data?.path) info.value = data;
  });
  Events.On("archive:batch-applied", () => {
    void refreshInfo();
  });
  Events.On("archive:reloaded", (event: any) => {
    const data = eventData(event);
    if (data?.path) info.value = data;
    else void refreshInfo();
  });
  Events.On("unpack:progress", (event: any) => {
    const data = eventData(event);
    unpackProgress.value = { done: data.done ?? 0, total: data.total ?? 0 };
  });
  Events.On("unpack:done", (event: any) => {
    const data = eventData(event);
    unpacking.value = false;
    unpackMessage.value = data?.message ?? "";
  });
  Events.On("archive:open-path", (event: any) => {
    const path = typeof event === "string" ? event : event?.data;
    if (typeof path === "string" && path.trim()) {
      void openPath(path.trim());
    }
  });
  Events.On("archive:save-remnant", (event: any) => {
    const data = eventData(event);
    const path = typeof data === "string" ? data : data?.path;
    if (typeof path === "string" && path.trim()) {
      saveRemnant.value = path.trim();
    }
  });

  return {
    recentArchives,
    info,
    loading,
    loadError,
    openError,
    clearOpenError,
    saveRemnant,
    clearSaveRemnant,
    open,
    modifiedCount,
    indexStatus,
    indexing,
    refreshingIndex,
    indexReady,
    unpacking,
    unpackProgress,
    unpackMessage,
    openDialog,
    openPath,
    clearRecentArchives,
    removeRecentArchive,
    close,
    refreshInfo,
    refreshIndexStatus,
    rebuildSearchIndex,
    ensureSearchIndex,
    listRegistrationOptions,
    registerFileToList,
    indexHashTargets,
    registerMissingIndexHashes,
    unpackDialog,
    cancelUnpack,
  };
});
