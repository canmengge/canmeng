/**
 * 文件节点右键动作（新建 / 导入 / 删除 / 批量处理 / 导出）。
 *
 * 左侧资源管理器（`Explorer.vue`）与搜索视窗（`SearchWindowPanel.vue`）的右键菜单
 * 语义一致，两侧节点同构（都是 `TreeItem`；搜索视窗里的目录是合成节点、`fileIndex = -1`，
 * 因此目录一律走后端 `ListDescendantFiles` 补全）。这里抽出与视图无关的部分复用，
 * 避免把 Explorer 里上百行逻辑再抄一遍。
 */
import { ref } from "vue";
import { useDialog, useMessage } from "naive-ui";
import { ArchiveService } from "../../bindings/pvfine/services";
import type { FileRegistration, TreeNode } from "../../bindings/pvfine/services/models";
import { useArchiveStore } from "../stores/archive";
import { useBatchStore } from "../stores/batch";
import { useEditorStore } from "../stores/editor";
import { useExplorerStore, type TreeItem } from "../stores/explorer";
import { useImportStore } from "../stores/import";
import { ExportFilesTo } from "../services/exportApi";

export function useFileNodeActions() {
  const archive = useArchiveStore();
  const editor = useEditorStore();
  const explorer = useExplorerStore();
  const importer = useImportStore();
  const batch = useBatchStore();
  const message = useMessage();
  const dialog = useDialog();

  // ---- 路径 / 节点收集 ----

  function parentDirectory(item: TreeItem | null): string {
    if (!item) return "";
    if (item.isDir) return item.key;
    const slash = item.key.lastIndexOf("/");
    return slash >= 0 ? item.key.slice(0, slash) : "";
  }

  async function collectPaths(items: TreeItem[]): Promise<string[]> {
    const paths: string[] = [];
    const seen = new Set<string>();
    const push = (path: string) => {
      if (path && !seen.has(path)) {
        seen.add(path);
        paths.push(path);
      }
    };
    for (const item of items) {
      if (!item.isDir) {
        if (item.fileIndex >= 0) push(item.key);
        continue;
      }
      const nodes = (await ArchiveService.ListDescendantFiles(item.key)) ?? [];
      for (const node of nodes) {
        if (node && !node.isDir && node.fileIndex >= 0) push(node.path);
      }
    }
    return paths;
  }

  async function collectNodes(items: TreeItem[]): Promise<TreeNode[]> {
    const paths = await collectPaths(items);
    if (paths.length === 0) return [];
    const nodes = (await ArchiveService.ResolveFiles(paths)) ?? [];
    return nodes.filter(
      (node): node is TreeNode => !!node && !node.isDir && node.fileIndex >= 0
    );
  }

  // ---- 新建文件 ----

  const newFileVisible = ref(false);
  const newFileName = ref("");
  const newFileType = ref(1);
  const newFileError = ref("");
  const newFileParent = ref("");
  const creating = ref(false);
  const newFileTypeOptions = [
    { label: "脚本（DataType 1）", value: 1 },
    { label: "文本（DataType 3）", value: 3 },
  ];

  function openNewFileDialog(item: TreeItem | null): void {
    newFileParent.value = parentDirectory(item);
    newFileName.value = "";
    newFileType.value = 1;
    newFileError.value = "";
    newFileVisible.value = true;
  }

  function closeNewFileDialog(): void {
    if (creating.value) return;
    newFileVisible.value = false;
    newFileError.value = "";
  }

  /** 归一化新建文件路径：与后端 normalizeNewFilePath 保持一致（目录无需预先存在）。 */
  function normalizeNewFilePath(raw: string): string | null {
    const path = raw.trim().replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
    if (!path) return null;
    const parts = path.split("/");
    if (parts.some((part) => !part || part === "." || part === "..")) return null;
    return parts.join("/");
  }

  async function submitNewFile(): Promise<void> {
    if (creating.value) return;
    const name = normalizeNewFilePath(newFileName.value);
    if (!name) {
      newFileError.value = "路径无效：不能为空，且不能包含空段、. 或 ..";
      return;
    }
    const path = newFileParent.value ? `${newFileParent.value}/${name}` : name;
    creating.value = true;
    newFileError.value = "";
    try {
      const node = await ArchiveService.CreateFile(path, newFileType.value);
      if (!node) throw new Error("后端未返回新文件信息");
      newFileVisible.value = false;
      await archive.refreshInfo();
      await explorer.reload();
      if (explorer.mode === "tree") await explorer.revealPath(node.path);
      else explorer.selectPath(node.path);
      await editor.openFile(node.fileIndex);
      message.success(`已新建文件 ${node.path}`);
    } catch (error: any) {
      newFileError.value = String(error?.message ?? error);
    } finally {
      creating.value = false;
    }
  }

  // ---- 导入文件 ----

  function onImport(item: TreeItem | null): void {
    importer.open(parentDirectory(item));
  }

  // ---- 删除文件 ----

  type DeleteDecision = "sync" | "files-only" | "cancel";
  const deleting = ref(false);

  function confirmDelete(
    fileCount: number,
    nodes: TreeNode[],
    registrations: FileRegistration[]
  ): Promise<DeleteDecision> {
    const preview = nodes
      .slice(0, 3)
      .map((node) => node.path)
      .join("、");
    const fileSuffix = fileCount > 3 ? ` 等 ${fileCount} 个文件` : "";
    const registrationPreview = registrations
      .slice(0, 3)
      .map((registration) => `${registration.listPath}（ID ${registration.id}）`)
      .join("、");
    const registrationSuffix =
      registrations.length > 3 ? ` 等 ${registrations.length} 条` : "";

    return new Promise((resolve) => {
      let settled = false;
      const finish = (value: DeleteDecision) => {
        if (settled) return;
        settled = true;
        resolve(value);
      };
      const hasRegistrations = registrations.length > 0;
      dialog.warning({
        title: "删除文件",
        content: hasRegistrations
          ? `确定删除 ${preview}${fileSuffix}吗？发现 ${registrations.length} 条注册项（${registrationPreview}${registrationSuffix}）。是否同步从 lst 中删除？`
          : `确定删除 ${preview}${fileSuffix}吗？删除只会修改当前归档内存，保存后才写入磁盘。`,
        positiveText: hasRegistrations ? "同步删除注册项" : "删除",
        negativeText: hasRegistrations ? "仅删除文件" : "取消",
        onPositiveClick: () => finish(hasRegistrations ? "sync" : "files-only"),
        onNegativeClick: () => finish(hasRegistrations ? "files-only" : "cancel"),
        onClose: () => finish("cancel"),
      });
    });
  }

  async function onDeleteSelected(items: TreeItem[]): Promise<void> {
    if (deleting.value) return;
    const snapshot = [...items];
    deleting.value = true;
    try {
      const nodes = await collectNodes(snapshot);
      const indexes = [
        ...new Set(nodes.map((node) => node.fileIndex).filter((index) => index >= 0)),
      ];
      if (indexes.length === 0) {
        message.info("选中的节点里没有文件");
        return;
      }
      const registrations = ((await ArchiveService.FindFileRegistrations(indexes)) ?? []).filter(
        (registration): registration is FileRegistration => !!registration
      );
      const decision = await confirmDelete(indexes.length, nodes, registrations);
      if (decision === "cancel") return;

      const removed = await ArchiveService.DeleteFilesWithRegistrations(
        indexes,
        decision === "sync"
      );
      await editor.refreshAfterArchiveChange(
        decision === "sync" ? registrations.map((registration) => registration.listPath) : []
      );
      await archive.refreshInfo();
      await explorer.reload();
      const registrationMessage =
        decision === "sync" ? `，同步删除 ${registrations.length} 条注册项` : "";
      message.success(
        `已删除 ${removed?.length ?? indexes.length} 个文件${registrationMessage}`
      );
    } catch (error: any) {
      message.error(`删除文件失败: ${error?.message ?? error}`);
    } finally {
      deleting.value = false;
    }
  }

  // ---- 批量处理 ----

  const batching = ref(false);

  async function onBatchSelected(items: TreeItem[], label = "搜索视窗选择"): Promise<void> {
    if (batching.value) return;
    const snapshot = [...items];
    batching.value = true;
    try {
      const paths = await collectPaths(snapshot);
      if (paths.length === 0) {
        message.info("选中的节点里没有文件");
        return;
      }
      batch.open(paths, `${label}（${paths.length} 个文件）`);
    } catch (error: any) {
      message.error(`打开批处理失败: ${error?.message ?? error}`);
    } finally {
      batching.value = false;
    }
  }

  // ---- 导出文件 ----

  const exporting = ref(false);
  const exportPickVisible = ref(false);
  const exportPickScopes = ref<string[]>([]);
  const exportKind = ref("");
  const exportLabel = ref("选中文件");

  /** 收集待导出条目并弹出目录选择器；kind 非空时用于命名顶层时间戳文件夹。 */
  async function onExportSelected(
    items: TreeItem[],
    kind = "",
    label = "选中文件"
  ): Promise<void> {
    if (exporting.value) return;
    const snapshot = [...items];
    exporting.value = true;
    try {
      const scopes = await collectPaths(snapshot);
      if (scopes.length === 0) {
        message.info("选中的节点里没有文件");
        return;
      }
      exportPickScopes.value = scopes;
      exportKind.value = kind;
      exportLabel.value = label;
      exportPickVisible.value = true;
    } catch (error: any) {
      message.error(`导出失败: ${error?.message ?? error}`);
    } finally {
      exporting.value = false;
    }
  }

  /** 目录选定后写盘：落在 <所选目录>\<时间戳><kind>文件导出\ 内。 */
  async function onExportPicked(payload: { dir: string; paths: string[] }): Promise<void> {
    const { dir, paths } = payload;
    if (!dir || paths.length === 0) return;
    exporting.value = true;
    try {
      const path = await ExportFilesTo(dir, paths, exportKind.value);
      if (path) {
        message.success(`已导出 ${paths.length} 个${exportLabel.value}到 ${path}`);
      }
    } catch (error: any) {
      message.error(`导出失败: ${error?.message ?? error}`);
    } finally {
      exporting.value = false;
      exportPickScopes.value = [];
    }
  }

  return {
    newFileVisible,
    newFileName,
    newFileType,
    newFileError,
    newFileParent,
    newFileTypeOptions,
    creating,
    deleting,
    batching,
    exporting,
    exportPickVisible,
    exportPickScopes,
    openNewFileDialog,
    closeNewFileDialog,
    submitNewFile,
    onImport,
    onDeleteSelected,
    onBatchSelected,
    onExportSelected,
    onExportPicked,
    collectPaths,
  };
}
