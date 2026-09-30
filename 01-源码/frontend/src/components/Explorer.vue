<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  NButton,
  NDropdown,
  NEmpty,
  NIcon,
  NInput,
  NModal,
  NSelect,
  NSpin,
  NTag,
  NText,
  NTooltip,
  useDialog,
  useMessage,
} from "naive-ui";
import { ArrowCollapseAll20Regular, Target20Regular } from "@vicons/fluent";
import { ArchiveService } from "../../bindings/pvfine/services";
import { ExportFilesTo } from "../services/exportApi";
import ExportDialog from "./ExportDialog.vue";
import ExternalEditPanel from "./ExternalEditPanel.vue";
import type { FileRegistration, TreeTag, TreeNode } from "../../bindings/pvfine/services/models";
import { useArchiveStore } from "../stores/archive";
import { useExternalEditStore } from "../stores/externalEdit";
import { useExplorerStore, type SearchItem, type TreeItem } from "../stores/explorer";
import { useEditorStore } from "../stores/editor";
import { useFileSetStore, type FileSetEntry } from "../stores/fileSets";
import { useBookmarkStore, type BookmarkInput } from "../stores/bookmarks";
import { useBatchStore } from "../stores/batch";
import { useImportStore } from "../stores/import";
import { useSettingsStore } from "../stores/settings";
import { useSidebarStore } from "../stores/sidebar";
import { MAX_CONTEXT_FILES, useAIStore } from "../stores/ai";
import { resolvePathAnnotations, useSearchWindowStore } from "../stores/searchWindow";
import { useAnnotationEditStore } from "../stores/annotationEdit";
import { buildSearchTree } from "../searchTree";
import FileTree from "./FileTree.vue";

const archive = useArchiveStore();
const explorer = useExplorerStore();
const searchWindow = useSearchWindowStore();
const annotationEdit = useAnnotationEditStore();
const externalEdit = useExternalEditStore();
const sidebar = useSidebarStore();
const ai = useAIStore();
const editor = useEditorStore();
const fileSets = useFileSetStore();
const bookmarks = useBookmarkStore();
const batch = useBatchStore();
const importer = useImportStore();
const settings = useSettingsStore();
const message = useMessage();
const dialog = useDialog();

const searchInput = ref("");
const fileTreeRef = ref<{ collapseAll: () => void } | null>(null);
const adding = ref(false);
const bookmarking = ref(false);
const exporting = ref(false);
const exportPickVisible = ref(false);
const exportPickScopes = ref<string[]>([]);
const copying = ref(false);
const batching = ref(false);
const creating = ref(false);
const deleting = ref(false);
const newFileVisible = ref(false);
const newFileParent = ref("");
const newFileName = ref("");
const newFileType = ref(1);
const newFileError = ref("");
const contextMenu = ref({
  show: false,
  x: 0,
  y: 0,
  items: [] as TreeItem[],
  anchor: null as TreeItem | null,
});
const searchTreeItems = computed(() => buildSearchTree(explorer.hits));
const visibleTreeItems = computed(() =>
  explorer.mode === "search" ? searchTreeItems.value : explorer.roots
);
const treeKey = computed(() => `${explorer.mode}:${explorer.query}:${explorer.revision}`);
const newFileTypeOptions = [
  { label: "脚本（DataType 1）", value: 1 },
  { label: "文本（DataType 3）", value: 3 },
];
const copyBusy = computed(
  () =>
    creating.value ||
    deleting.value ||
    adding.value ||
    exporting.value ||
    copying.value ||
    batching.value ||
    importer.running ||
    !archive.open
);
const copyMenuDisabled = computed(
  () => copyBusy.value || contextMenu.value.items.length === 0
);
// ID 与名称都来自语义搜索索引，索引未就绪时没有可复制的值。
const copyIndexDisabled = computed(() => copyMenuDisabled.value || !archive.indexReady);
const contextMenuOptions = computed(() => [
  {
    label: "新建文件",
    key: "new-file",
    disabled:
      creating.value ||
      deleting.value ||
      adding.value ||
      exporting.value ||
      copying.value ||
      batching.value ||
      importer.running ||
      !archive.open,
  },
  {
    label: "导入文件…",
    key: "import",
    disabled:
      creating.value ||
      deleting.value ||
      adding.value ||
      exporting.value ||
      copying.value ||
      batching.value ||
      importer.running ||
      !archive.open,
  },
  {
    label: "删除文件",
    key: "delete",
    disabled: copyMenuDisabled.value,
  },
  {
    type: "divider",
    key: "divider",
  },
  {
    label: "导出文件",
    key: "export",
    disabled: copyMenuDisabled.value,
  },
  {
    // 大文件（如 list/equipment.lst）在编辑器里打开很慢：导出成文本用系统默认程序改，再一键回填。
    label: "用外部编辑器编辑",
    key: "external-edit",
    disabled:
      copyBusy.value ||
      !archive.open ||
      contextMenu.value.items.length !== 1 ||
      contextMenu.value.anchor?.isDir === true,
  },
  {
    label: "外部编辑会话…",
    key: "external-edit-panel",
    disabled: !archive.open,
  },
  {
    label: "复制",
    key: "copy",
    disabled: copyMenuDisabled.value,
    children: [
      {
        label: "文件路径",
        key: "copy-paths",
      },
      {
        label: "ID",
        key: "copy-ids",
        disabled: copyIndexDisabled.value,
      },
      {
        label: "名称",
        key: "copy-names",
        disabled: copyIndexDisabled.value,
      },
    ],
  },
  {
    label: `加入“${fileSets.activeSet?.name ?? "当前文件集"}”`,
    key: "add",
    disabled: copyMenuDisabled.value,
  },
  {
    label: "加入当前书签簿",
    key: "bookmark-add",
    disabled:
      creating.value ||
      deleting.value ||
      adding.value ||
      exporting.value ||
      copying.value ||
      batching.value ||
      bookmarking.value ||
      importer.running ||
      !archive.open ||
      contextMenu.value.items.length === 0,
  },
  {
    label: "批量处理…",
    key: "batch",
    disabled: copyMenuDisabled.value,
  },
  {
    label: "收进搜索视窗",
    key: "add-to-search-window",
    disabled:
      copyMenuDisabled.value ||
      contextMenu.value.items.filter((item) => !item.isDir).length === 0,
  },
  {
    label: "AI 引入",
    key: "ai-introduce",
    disabled: contextMenu.value.items.length === 0,
  },
  {
    label: "编辑注释…",
    key: "edit-annotation",
    // 仅对单个条目开放：注释按路径存储，批量编辑语义含糊（后续可做批量套用）。
    disabled:
      creating.value ||
      deleting.value ||
      adding.value ||
      exporting.value ||
      copying.value ||
      batching.value ||
      importer.running ||
      !archive.open ||
      contextMenu.value.items.length !== 1,
  },
  {
    // 与工具栏「折叠所有目录」同一动作：把树恢复到刚打开归档时的全折叠状态。
    label: "全部折叠",
    key: "collapse-all",
    disabled: !archive.open,
  },
]);

watch(
  () => archive.info?.path ?? "",
  async (archivePath) => {
    if (archivePath) {
      explorer.reset();
      searchInput.value = "";
      await explorer.loadRoots();
    } else {
      explorer.reset();
    }
  }
);

function submitSearch(event: KeyboardEvent): void {
  if (event.isComposing) return;
  const term = searchInput.value.trim();
  if (!term) return;
  // A-01：索引按需构建——未构建时先开始构建，构建完成后再按回车即搜索。
  if (!archive.indexReady) {
    void archive.ensureSearchIndex();
    return;
  }
  void explorer.search(term);
}

function clearSearch(): void {
  // 取消搜索时，把本次命中的文件快照收进搜索视窗（结果不因清空而丢失）。
  // 命中条目自带后端随搜索返回的 pathAnnotations，直接复用 addEntries 的按路径去重。
  if (explorer.mode === "search" && explorer.hits.length > 0) {
    const result = searchWindow.addEntries(explorer.hits);
    if (result.added > 0) sidebar.show("search");
  }
  searchInput.value = "";
  explorer.clearSearch();
}

function toggleExactMatch(): void {
  void explorer.setExactMatch(!explorer.exactMatch);
}

function collapseAllDirectories(): void {
  fileTreeRef.value?.collapseAll();
}

async function onTreeLoad(item: TreeItem): Promise<void> {
  await explorer.loadChildren(item);
}

async function onTreeOpen(item: TreeItem): Promise<void> {
  if (item && !item.isDir) {
    explorer.selectPath(item.key);
    void editor.openFile(item.fileIndex);
  }
}

function onTreeSelect(item: TreeItem): void {
  explorer.selectPath(item.key);
}

function hideContextMenu(): void {
  contextMenu.value.show = false;
  contextMenu.value.items = [];
  contextMenu.value.anchor = null;
}

function onTreeContextMenu(
  event: MouseEvent,
  item: TreeItem | null,
  items: TreeItem[]
): void {
  if (!archive.open) {
    hideContextMenu();
    return;
  }
  contextMenu.value = {
    show: true,
    x: event.clientX,
    y: event.clientY,
    items: item ? items : [],
    anchor: item,
  };
}

function parentDirectory(item: TreeItem | null): string {
  if (!item) return "";
  if (item.isDir) return item.key;
  const slash = item.key.lastIndexOf("/");
  return slash >= 0 ? item.key.slice(0, slash) : "";
}

function openNewFileDialog(item: TreeItem | null): void {
  newFileParent.value = parentDirectory(item);
  newFileName.value = "";
  newFileType.value = 1;
  newFileError.value = "";
  newFileVisible.value = true;
  hideContextMenu();
}

function closeNewFileDialog(): void {
  if (creating.value) return;
  newFileVisible.value = false;
  newFileError.value = "";
}

/**
 * 归一化新建文件路径：与后端 normalizeNewFilePath 保持一致。
 * 输入中出现的目录不需要预先存在，后端会按路径补建目录。
 */
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

function serviceEntry(node: TreeNode): FileSetEntry {
  const names = [
    ...new Set((node.tags ?? []).map((tag) => tag.name.trim()).filter(Boolean)),
  ];
  return {
    fileIndex: node.fileIndex,
    path: node.path,
    name: names.join(" / ") || node.name || node.path.slice(node.path.lastIndexOf("/") + 1),
    ids: [...new Set((node.tags ?? []).map((tag) => tag.id).filter(Boolean))],
    size: node.size,
    dataType: node.dataType,
    icon: node.icon ?? null,
    fieldImage: node.fieldImage ?? null,
  };
}

function appendSearchPaths(item: TreeItem, paths: string[], seen: Set<string>): void {
  if (!item.isDir) {
    if (item.fileIndex >= 0 && !seen.has(item.key)) {
      seen.add(item.key);
      paths.push(item.key);
    }
    return;
  }
  for (const child of item.children ?? []) appendSearchPaths(child, paths, seen);
}

async function collectFilePaths(items: TreeItem[]): Promise<string[]> {
  const paths: string[] = [];
  const seen = new Set<string>();
  const appendPath = (path: string) => {
    if (!seen.has(path)) {
      seen.add(path);
      paths.push(path);
    }
  };
  const directoryRequests = new Map<string, Promise<TreeNode[]>>();

  for (const item of items) {
    if (!item.isDir) {
      if (item.fileIndex >= 0) appendPath(item.key);
      continue;
    }
    if (explorer.mode === "search") {
      appendSearchPaths(item, paths, seen);
      continue;
    }
    let request = directoryRequests.get(item.key);
    if (!request) {
      request = ArchiveService.ListDescendantFiles(item.key).then(
        (nodes) =>
          (nodes ?? []).filter(
            (node): node is TreeNode => !!node && !node.isDir && node.fileIndex >= 0
          )
      );
      directoryRequests.set(item.key, request);
    }
    for (const node of await request) appendPath(node.path);
  }
  return paths;
}

async function collectExportScopes(items: TreeItem[]): Promise<string[]> {
  if (explorer.mode === "search") return collectFilePaths(items);
  return [...new Set(items.map((item) => item.key).filter(Boolean))];
}

async function collectNodes(items: TreeItem[]): Promise<TreeNode[]> {
  const paths = await collectFilePaths(items);
  const nodes = await ArchiveService.ResolveFiles(paths);
  return (nodes ?? []).filter(
    (node): node is TreeNode => !!node && !node.isDir && node.fileIndex >= 0
  );
}

async function collectFiles(items: TreeItem[]): Promise<FileSetEntry[]> {
  return (await collectNodes(items)).map(serviceEntry);
}

/** 取出选中文件在索引中登记的 ID 或名称，按值去重并保持归档顺序。 */
function collectIndexValues(nodes: TreeNode[], pick: (tag: TreeTag) => string): string[] {
  const values: string[] = [];
  const seen = new Set<string>();
  for (const node of nodes) {
    for (const tag of node.tags ?? []) {
      const value = pick(tag).trim();
      if (!value || seen.has(value)) continue;
      seen.add(value);
      values.push(value);
    }
  }
  return values;
}

type DeleteDecision = "sync" | "files-only" | "cancel";

function confirmDelete(
  fileCount: number,
  files: FileSetEntry[],
  registrations: FileRegistration[]
): Promise<DeleteDecision> {
  const preview = files
    .slice(0, 3)
    .map((file) => file.path)
    .join("、");
  const fileSuffix = fileCount > 3 ? ` 等 ${fileCount} 个文件` : "";
  const registrationPreview = registrations
    .slice(0, 3)
    .map((registration) => `${registration.listPath}（ID ${registration.id}）`)
    .join("、");
  const registrationSuffix = registrations.length > 3 ? ` 等 ${registrations.length} 条` : "";

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
  const selectedItems = [...items];
  hideContextMenu();
  deleting.value = true;
  try {
    const files = await collectFiles(selectedItems);
    const indexes = [...new Set(files.map((file) => file.fileIndex).filter((index) => index >= 0))];
    if (indexes.length === 0) {
      message.info("选中的目录中没有文件");
      return;
    }
    const registrations = ((await ArchiveService.FindFileRegistrations(indexes)) ?? []).filter(
      (registration): registration is FileRegistration => !!registration
    );
    const decision = await confirmDelete(indexes.length, files, registrations);
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

async function writeClipboardText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // 某些桌面 WebView 不允许直接访问 Clipboard API，继续使用兼容方案。
    }
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  if (!copied) throw new Error("系统剪贴板不可用");
}

type CopyKind = "paths" | "ids" | "names";

const copyKindLabels: Record<CopyKind, string> = {
  paths: "文件路径",
  ids: "ID",
  names: "名称",
};

async function onCopy(kind: CopyKind, items: TreeItem[]): Promise<void> {
  if (copying.value) return;
  const archivePath = archive.info?.path ?? "";
  const session = fileSets.sessionId;
  hideContextMenu();
  copying.value = true;
  try {
    const nodes = await collectNodes(items);
    if (session !== fileSets.sessionId || archive.info?.path !== archivePath) return;
    const values =
      kind === "paths"
        ? nodes.map((node) => node.path)
        : collectIndexValues(nodes, (tag) => (kind === "ids" ? tag.id : tag.name));
    if (values.length === 0) {
      message.info(
        kind === "paths" ? "选中的目录中没有文件" : `选中的文件在索引中没有登记${copyKindLabels[kind]}`
      );
      return;
    }
    await writeClipboardText(values.join("\n"));
    message.success(`已复制 ${values.length} 个${copyKindLabels[kind]}`);
  } catch (error: any) {
    if (session === fileSets.sessionId && archive.info?.path === archivePath) {
      message.error(`复制${copyKindLabels[kind]}失败: ${error?.message ?? error}`);
    }
  } finally {
    copying.value = false;
  }
}

function bookmarkInputs(items: TreeItem[]): BookmarkInput[] {
  const result: BookmarkInput[] = [];
  const seen = new Set<string>();
  for (const item of items) {
    // 文件夹也可加入书签（isDir=true）；文件需能解析到索引。
    if (!item.isDir && item.fileIndex < 0) continue;
    const path = bookmarks.normalizePath(item.key);
    if (!path || seen.has(path)) continue;
    seen.add(path);
    result.push({
      path,
      name: item.label || path.slice(path.lastIndexOf("/") + 1),
      fileIndex: item.fileIndex,
      isDir: item.isDir,
    });
  }
  return result;
}

function confirmBuiltinBookmarkCopy(): Promise<boolean> {
  const active = bookmarks.activeBook;
  if (!active || active.editable) return Promise.resolve(true);
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value: boolean) => {
      if (settled) return;
      settled = true;
      resolve(value);
    };
    dialog.warning({
      title: "内置书签簿不可编辑",
      content: `将复制“${active.name}”为新的可编辑书签簿，并把本次选择加入副本。继续吗？`,
      positiveText: "复制并加入",
      negativeText: "取消",
      onPositiveClick: () => finish(!!bookmarks.copyBook(active.id)),
      onNegativeClick: () => finish(false),
      onClose: () => finish(false),
    });
  });
}

async function onBookmarkSelected(items: TreeItem[]): Promise<void> {
  if (bookmarking.value) return;
  const selectedItems = [...items];
  const session = bookmarks.sessionId;
  const archivePath = archive.info?.path ?? "";
  hideContextMenu();
  bookmarking.value = true;
  try {
    await bookmarks.load();
    if (session !== bookmarks.sessionId || archive.info?.path !== archivePath) return;
    const entries = bookmarkInputs(selectedItems);
    if (entries.length === 0) {
      message.info("请选择文件或文件夹节点加入书签");
      return;
    }
    if (!(await confirmBuiltinBookmarkCopy())) return;
    if (session !== bookmarks.sessionId || archive.info?.path !== archivePath) return;
    const result = bookmarks.addEntries(entries);
    if (result.added === 0) {
      message.info("选中的文件已在当前书签分组中");
      return;
    }
    const duplicateText = result.skipped > 0 ? `，跳过 ${result.skipped} 个重复项` : "";
    message.success(`已加入 ${result.added} 个书签${duplicateText}`);
  } catch (error: any) {
    if (session === bookmarks.sessionId && archive.info?.path === archivePath) {
      message.error(`加入书签失败: ${error?.message ?? error}`);
    }
  } finally {
    bookmarking.value = false;
  }
}

/**
 * 「AI 引入」：把右键选中的条目（文件或整个文件夹）交给 AI 助手，选多少识别多少。
 * 文件夹**整体引用**，不再递归展开成文件列表（AI 可自行用 list_directory 展开）。
 */
async function onAIIntroduce(): Promise<void> {
  const items = [...contextMenu.value.items];
  hideContextMenu();
  if (items.length === 0) return;
  const entries = items
    .filter((item) => !!item.key)
    .map((item) => ({
      path: item.key,
      title: item.key.slice(item.key.lastIndexOf("/") + 1),
      isDir: item.isDir,
    }));
  if (entries.length === 0) return;
  const { added, truncated } = ai.introduceFiles(entries);
  sidebar.show("ai");
  if (added === 0) {
    message.info("选中的条目已全部在 AI 引入中");
    return;
  }
  const truncatedText = truncated ? `，已达上限 ${MAX_CONTEXT_FILES} 个` : "";
  message.success(`已引入给 AI ${added} 个条目${truncatedText}`);
}

async function onContextMenuSelect(key: string | number): Promise<void> {
  if (key === "collapse-all") {
    hideContextMenu();
    collapseAllDirectories();
    return;
  }
  if (key === "edit-annotation") {
    const item = contextMenu.value.anchor;
    hideContextMenu();
    if (!item) return;
    await annotationEdit.openFor({
      path: item.key,
      isDir: item.isDir,
      // 内置注释可能有多条：标题全部带出来供用户在原值上改。
      builtinTitle: item.annotations.map((annotation) => annotation.title).filter(Boolean).join("、"),
    });
    return;
  }
  if (key === "ai-introduce") {
    await onAIIntroduce();
    return;
  }
  if (key === "external-edit-panel") {
    hideContextMenu();
    externalEdit.openPanel();
    return;
  }
  if (key === "external-edit") {
    const item = contextMenu.value.anchor;
    hideContextMenu();
    if (!item || item.isDir) {
      message.info("外部编辑只针对单个文件：请右键一个文件");
      return;
    }
    try {
      const session = await externalEdit.start(item.key);
      message.success(
        session.opened
          ? `已导出并用系统默认程序打开：${session.localPath}`
          : `已导出到 ${session.localPath}（未能自动打开，请手动打开）`
      );
    } catch (error: any) {
      message.error(`外部编辑失败：${error?.message ?? error}`);
    }
    return;
  }
  if (key === "add-to-search-window") {
    // 先复制待处理项：下面要 await 补目录标注，菜单状态随时可能被清掉。
    const items = [...contextMenu.value.items];
    hideContextMenu();
    await addToSearchWindow(items);
    return;
  }
  if (key === "new-file") {
    openNewFileDialog(contextMenu.value.anchor);
    return;
  }
  if (key === "import") {
    const targetDir = parentDirectory(contextMenu.value.anchor);
    hideContextMenu();
    importer.open(targetDir);
    return;
  }
  if (key === "delete") {
    void onDeleteSelected(contextMenu.value.items);
    return;
  }
  if (key === "export") {
    await onExportSelected(contextMenu.value.items);
    return;
  }
  if (key === "copy-paths") {
    await onCopy("paths", contextMenu.value.items);
    return;
  }
  if (key === "copy-ids") {
    await onCopy("ids", contextMenu.value.items);
    return;
  }
  if (key === "copy-names") {
    await onCopy("names", contextMenu.value.items);
    return;
  }
  if (key === "bookmark-add") {
    await onBookmarkSelected(contextMenu.value.items);
    return;
  }
  if (key === "batch") {
    await onBatchSelected(contextMenu.value.items);
    return;
  }
  if (key !== "add" || adding.value) return;
  const selectedItems = contextMenu.value.items;
  const archivePath = archive.info?.path ?? "";
  const session = fileSets.sessionId;
  hideContextMenu();
  adding.value = true;
  try {
    const entries = await collectFiles(selectedItems);
    if (session !== fileSets.sessionId || archive.info?.path !== archivePath) return;
    const result = fileSets.addEntries(entries);
    if (result.added === 0 && result.skipped === 0) {
      message.info("选中的目录中没有文件");
      return;
    }
    const duplicateText = result.skipped > 0 ? `，跳过 ${result.skipped} 个重复项` : "";
    message.success(`已加入 ${result.added} 个文件${duplicateText}`);
  } catch (error: any) {
    if (session === fileSets.sessionId && archive.info?.path === archivePath) {
      message.error(`加入文件集失败: ${error?.message ?? error}`);
    }
  } finally {
    adding.value = false;
  }
}

async function onBatchSelected(items: TreeItem[]): Promise<void> {
  if (batching.value) return;
  const selectedItems = [...items];
  const archivePath = archive.info?.path ?? "";
  const session = fileSets.sessionId;
  hideContextMenu();
  batching.value = true;
  try {
    const paths = await collectFilePaths(selectedItems);
    if (session !== fileSets.sessionId || archive.info?.path !== archivePath) return;
    if (paths.length === 0) {
      message.info("选中的目录中没有文件");
      return;
    }
    batch.open(paths, `资源管理器选择（${paths.length} 个文件）`);
  } catch (error: any) {
    if (session === fileSets.sessionId && archive.info?.path === archivePath) {
      message.error(`打开批处理失败: ${error?.message ?? error}`);
    }
  } finally {
    batching.value = false;
  }
}

async function onExportSelected(items: TreeItem[]): Promise<void> {
  if (exporting.value) return;
  const archivePath = archive.info?.path ?? "";
  const session = fileSets.sessionId;
  hideContextMenu();
  exporting.value = true;
  try {
    const scopes = await collectExportScopes(items);
    if (session !== fileSets.sessionId || archive.info?.path !== archivePath) return;
    if (scopes.length === 0) {
      message.info("选中的目录中没有文件");
      return;
    }
    exportPickScopes.value = scopes;
    exportPickVisible.value = true;
  } catch (error: any) {
    if (session === fileSets.sessionId && archive.info?.path === archivePath) {
      if (!isCancel(error)) message.error(`导出失败: ${error?.message ?? error}`);
    }
  } finally {
    exporting.value = false;
  }
}

/** 目录选定后导出：落在 <所选目录>\<时间戳>文件导出\ 内。 */
async function onExportPicked(payload: { dir: string; paths: string[] }): Promise<void> {
  const { dir } = payload;
  if (!dir || payload.paths.length === 0) return;
  exporting.value = true;
  try {
    const path = await ExportFilesTo(dir, payload.paths, "");
    if (path) message.success(`已导出选中文件到 ${path}`);
  } catch (error: any) {
    if (!isCancel(error)) message.error(`导出失败: ${error?.message ?? error}`);
  } finally {
    exporting.value = false;
    exportPickScopes.value = [];
  }
}

/**
 * 把选中的文件收进「搜索视窗」：搜索结果会随查询变化，手动收进来的文件不受影响。
 */
async function addToSearchWindow(items: TreeItem[]): Promise<void> {
  const files = items.filter((item) => !item.isDir);
  if (files.length === 0) {
    message.info("目录不能收进搜索视窗");
    return;
  }
  const entries = await Promise.all(files.map((item) => toSearchWindowEntry(item)));
  const result = searchWindow.addEntries(entries);
  if (result.added === 0) {
    message.info("选中的文件已在搜索视窗中");
    return;
  }
  sidebar.show("search");
  const duplicateText = result.skipped > 0 ? `，跳过 ${result.skipped} 个重复项` : "";
  message.success(`已收进搜索视窗 ${result.added} 个文件${duplicateText}`);
}

/** 文件树节点 → 搜索视窗条目（与搜索结果共用同一数据结构）。 */
async function toSearchWindowEntry(item: TreeItem): Promise<SearchItem> {
  const tag = item.tags[0];
  return {
    key: item.key,
    label: item.label,
    path: item.key,
    id: tag?.id ?? "",
    name: tag?.name ?? "",
    category: tag?.category ?? "file",
    fileIndex: item.fileIndex,
    size: item.size,
    dataType: item.dataType,
    changeKind: item.changeKind ?? "",
    annotations: item.annotations ?? [],
    pathAnnotations: await resolvePathAnnotations(item.key),
    icon: item.icon ?? null,
    fieldImage: item.fieldImage ?? null,
  };
}

function isCancel(error: any): boolean {
  return String(error?.message ?? error).toLowerCase().includes("cancel");
}


</script>

<template>
  <div class="explorer">
    <div class="exp-search">
      <NInput
        v-model:value="searchInput"
        :placeholder="
          archive.indexReady
            ? '搜索路径、名称或 id（支持 *、?）…'
            : '索引未构建：回车开始构建，完成后再次回车搜索…'
        "
        clearable
        size="small"
        :disabled="!archive.open"
        @clear="clearSearch"
        @keydown.enter.prevent="submitSearch"
      >
        <template #suffix>
          <NTooltip trigger="hover">
            <template #trigger>
              <NButton
                text
                circle
                size="tiny"
                :type="explorer.exactMatch ? 'primary' : 'default'"
                class="exact-toggle"
                :class="{ 'exact-toggle--active': explorer.exactMatch }"
                :disabled="!archive.open"
                aria-label="切换精确匹配"
                :aria-pressed="explorer.exactMatch"
                @click.stop="toggleExactMatch"
              >
                <template #icon><NIcon><Target20Regular /></NIcon></template>
              </NButton>
            </template>
            {{ explorer.exactMatch ? "关闭精确匹配" : "启用精确匹配" }}
          </NTooltip>
        </template>
      </NInput>
      <NTooltip trigger="hover">
        <template #trigger>
          <NButton
            quaternary
            circle
            size="small"
            class="collapse-all"
            :disabled="!archive.open"
            aria-label="收起所有目录"
            @click="collapseAllDirectories"
          >
            <template #icon><NIcon><ArrowCollapseAll20Regular /></NIcon></template>
          </NButton>
        </template>
        收起所有目录
      </NTooltip>
    </div>

    <NSpin
      class="exp-spin"
      :show="archive.loading || explorer.searching || adding || exporting || copying || batching || bookmarking || importer.running || creating || deleting"
    >
      <div class="exp-body">
        <!-- 空态 -->
        <NEmpty
          v-if="!archive.open"
          description="未打开归档"
          class="exp-empty"
          size="small"
        />

        <template v-else>
          <div v-if="explorer.mode === 'search'" class="search-meta">
            <NTag size="tiny" :bordered="false">命中 {{ explorer.hits.length.toLocaleString() }} 条</NTag>
            <NButton quaternary size="tiny" @click="clearSearch">返回目录</NButton>
          </div>
          <NEmpty
            v-if="explorer.mode === 'search' && !explorer.searching && !searchTreeItems.length"
            description="无匹配结果"
            size="small"
            class="exp-empty"
          />
          <div v-else class="tree-viewport">
            <FileTree
              ref="fileTreeRef"
              :key="treeKey"
              :items="visibleTreeItems"
              :expand-all="explorer.mode === 'search'"
              :open-mode="settings.explorerOpenMode"
              :selected-keys="explorer.selectedKeys"
              :reveal-request="explorer.revealRequest"
              :load-children="onTreeLoad"
              @open="onTreeOpen"
              @select="onTreeSelect"
              @select-many="explorer.setSelection($event)"
              @deselect="explorer.clearSelection()"
              @reveal-consumed="explorer.consumeRevealRequest()"
              @contextmenu="onTreeContextMenu"
            />
          </div>
          <div v-if="explorer.mode === 'search' && explorer.nextCursor >= 0" class="load-more">
            <NButton size="tiny" quaternary :loading="explorer.searching" @click="explorer.loadMore()">
              加载更多
            </NButton>
          </div>
        </template>
      </div>
    </NSpin>
    <NDropdown
      trigger="manual"
      placement="bottom-start"
      :show="contextMenu.show"
      :x="contextMenu.x"
      :y="contextMenu.y"
      :options="contextMenuOptions"
      @select="onContextMenuSelect"
      @clickoutside="hideContextMenu"
    />
    <NModal
      :show="newFileVisible"
      preset="card"
      title="新建文件"
      :style="{ width: 'min(420px, calc(100vw - 48px))' }"
      :mask-closable="false"
      @update:show="(show) => !show && closeNewFileDialog()"
    >
      <div class="new-file-form">
        <NText depth="3">
          创建位置：{{ newFileParent ? `${newFileParent}/` : "归档根目录/" }}
        </NText>
        <NInput
          v-model:value="newFileName"
          autofocus
          placeholder="输入文件名或相对路径，例如 dir/new.equ"
          :disabled="creating"
          :status="newFileError ? 'error' : undefined"
          @keydown.enter.prevent="submitNewFile"
        />
        <NSelect
          v-model:value="newFileType"
          :options="newFileTypeOptions"
          :disabled="creating"
        />
        <NText v-if="newFileError" type="error">{{ newFileError }}</NText>
      </div>
      <template #footer>
        <div class="new-file-modal-footer">
          <NButton quaternary :disabled="creating" @click="closeNewFileDialog">取消</NButton>
          <NButton type="primary" :loading="creating" @click="submitNewFile">创建</NButton>
        </div>
      </template>
    </NModal>
    <ExportDialog
      v-model:show="exportPickVisible"
      title="导出文件"
      :paths="exportPickScopes"
      @confirm="onExportPicked"
    />
    <ExternalEditPanel v-model:show="externalEdit.panelVisible" />
  </div>
</template>

<style scoped>
.exp-spin,
.exp-spin :deep(.n-spin-container),
.exp-spin :deep(.n-spin-content) {
  flex: 1;
  height: 100%;
  min-width: 0;
  min-height: 0;
}
.exp-spin {
  display: flex;
}
.exp-spin :deep(.n-spin-container) {
  display: flex;
  flex-direction: column;
}
.exp-spin :deep(.n-spin-content) {
  display: flex;
  flex-direction: column;
}
.explorer {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  border-right: 1px solid var(--pvf-border-normal);
}
.exp-search {
  display: flex;
  align-items: center;
  gap: 4px;
  /* 左内边距 = 工具栏外边距 10 + 边框 1 + 内边距 14，使搜索框左边缘与「打开」按钮对齐。 */
  padding: 8px 8px 8px 25px;
  flex-shrink: 0;
}
.exp-search :deep(.n-input) {
  flex: 1;
  min-width: 0;
}
.collapse-all {
  flex-shrink: 0;
  color: var(--pvf-text-muted);
}
.exact-toggle {
  color: var(--pvf-text-muted);
}
.exact-toggle--active {
  color: var(--pvf-primary-hover);
  background: var(--pvf-primary-selected);
}
.index-status {
  display: flex;
  align-items: center;
  gap: 6px;
  min-height: 18px;
  margin-top: 4px;
  color: var(--pvf-text-muted);
  font-size: 11px;
  line-height: 18px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.index-status--error {
  color: var(--pvf-error);
}
.exp-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.tree-viewport {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  overflow: auto;
}
.exp-empty {
  margin-top: 80px;
}
.new-file-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.new-file-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.search-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 3px 8px;
  font-size: 12px;
  color: var(--pvf-text-muted);
}
.load-more {
  display: flex;
  justify-content: center;
  padding: 6px 0 10px;
}
</style>
