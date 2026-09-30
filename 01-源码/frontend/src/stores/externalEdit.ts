/**
 * 「大文件外部编辑」状态：会话列表 + 导出/检查/回填/收尾动作。
 *
 * 用途：像 `list/equipment.lst`（27MB / 41 万行）这种大清单在编辑器里打开很慢，
 * 走「导出到工作目录 → 系统默认程序改 → 一键回填」这条路；本 store 只做前端编排，
 * 真正的读写都在 `services/EditorService.ExternalEdit*`（Go）。
 */
import { ref } from "vue";
import { defineStore } from "pinia";
import { ArchiveService } from "../../bindings/pvfine/services";
import { useEditorStore } from "./editor";
import {
  ExternalEditApply,
  ExternalEditCheck,
  ExternalEditFinish,
  ExternalEditList,
  ExternalEditReveal,
  ExternalEditStart,
  type ExternalEditResult,
  type ExternalEditSession,
} from "../services/externalEditApi";

export const useExternalEditStore = defineStore("externalEdit", () => {
  const editor = useEditorStore();

  const sessions = ref<ExternalEditSession[]>([]);
  const panelVisible = ref(false);
  /** 正在处理的归档内路径（按钮 loading 用），空串表示空闲。 */
  const busy = ref("");
  /** 面板里手工输入待编辑文件的归档内路径，例如 list/equipment.lst。 */
  const manualPath = ref("");

  async function refresh(): Promise<void> {
    try {
      sessions.value = (await ExternalEditList()) ?? [];
    } catch {
      // 归档未打开 / 服务不可用时保持空列表，不打扰用户。
      sessions.value = [];
    }
  }

  function openPanel(): void {
    panelVisible.value = true;
    void refresh();
  }

  function closePanel(): void {
    panelVisible.value = false;
  }

  /** 导出该文件到工作目录并调起系统默认程序。 */
  async function start(path: string): Promise<ExternalEditSession> {
    const session = await ExternalEditStart(path);
    await refresh();
    panelVisible.value = true;
    return session;
  }

  async function check(path: string): Promise<ExternalEditSession> {
    busy.value = path;
    try {
      const session = await ExternalEditCheck(path);
      await refresh();
      return session;
    } finally {
      busy.value = "";
    }
  }

  /** 回填归档（内存）；成功后若该文件已在编辑器里打开，顺带刷新编辑区。 */
  async function apply(path: string): Promise<ExternalEditResult> {
    busy.value = path;
    try {
      const result = await ExternalEditApply(path);
      await refresh();
      if (result?.applied) await reloadTabIfOpen(path);
      return result;
    } finally {
      busy.value = "";
    }
  }

  async function reveal(path: string): Promise<void> {
    await ExternalEditReveal(path);
  }

  async function finish(path: string, discardLocal: boolean): Promise<void> {
    await ExternalEditFinish(path, discardLocal);
    await refresh();
  }

  /** 文件若已打开就让它从归档重读；未打开时 reloadFileFromArchive 会安全地什么都不做。 */
  async function reloadTabIfOpen(path: string): Promise<void> {
    try {
      const nodes = (await ArchiveService.ResolveFiles([path])) ?? [];
      const index = nodes.find((node) => node && !node.isDir)?.fileIndex ?? -1;
      if (index >= 0) await editor.reloadFileFromArchive(index);
    } catch {
      // 刷新失败不影响回填结果本身。
    }
  }

  return {
    sessions,
    panelVisible,
    busy,
    manualPath,
    refresh,
    openPanel,
    closePanel,
    start,
    check,
    apply,
    reveal,
    finish,
  };
});
