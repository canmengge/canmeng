<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { Clipboard } from "@wailsio/runtime";
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
  NTabPane,
  NTabs,
  NTooltip,
  useDialog,
  useMessage,
} from "naive-ui";
import {
  BookmarkAdd24Regular,
  Bot24Regular,
  Dismiss16Regular,
  DocumentAdd24Regular,
  DocumentSearch24Regular,
  Eye24Regular,
  EyeOff24Regular,
  Save24Regular,
  Search24Regular,
  TextAddT24Regular,
  TextT24Regular,
} from "@vicons/fluent";
import {
  useEditorStore,
  type DraggedEditorTab,
  type EditorPaneId,
  type EditorTab,
} from "../stores/editor";
import { useArchiveStore } from "../stores/archive";
import { useExplorerStore } from "../stores/explorer";
import { useBookmarkStore } from "../stores/bookmarks";
import { MAX_CONTEXT_FILES, useAIStore } from "../stores/ai";
import { useSidebarStore } from "../stores/sidebar";
import { useAnnotationEditStore } from "../stores/annotationEdit";
import CodeEditor, { type PlaceholderEditRequest } from "./CodeEditor.vue";
import LargeScrollView from "./LargeScrollView.vue";
import ListDuplicatePanel from "./ListDuplicatePanel.vue";
import {
  findListDuplicates,
  parseListEntries,
  type ListDuplicateIssue,
} from "../listDuplicate";
import { useSettingsStore } from "../stores/settings";
import ImageThumbnail from "./ImageThumbnail.vue";
import PreviewHost from "./previews/PreviewHost.vue";
import { getPreviewProvider } from "../previews/registry";
import type { PreviewFile } from "../previews/types";
import type { ResolvedThemeId } from "../theme";
import type { ListRegistrationTarget } from "../../bindings/pvfine/services/models";
import { markTrace, traceAsync } from "../diagTrace";
import {
  AUTO_TARGET,
  defaultTargetTableIndex,
  isProtectedStringTable,
  loadSafeTableTargets,
  loadStringTableGuard,
  type SafeTableTarget,
} from "../services/stringGuardApi";

const props = defineProps<{
  paneId: EditorPaneId;
  themeId: ResolvedThemeId;
}>();

const paneId = props.paneId;
const editor = useEditorStore();
const archive = useArchiveStore();
const explorer = useExplorerStore();
const bookmarks = useBookmarkStore();
const settings = useSettingsStore();
const ai = useAIStore();
const sidebar = useSidebarStore();
const annotationEdit = useAnnotationEditStore();
const message = useMessage();
const dialog = useDialog();
const host = ref<HTMLDivElement | null>(null);
const draggingIndex = ref<number | null>(null);
const revealingFile = ref(false);
const bookmarking = ref(false);
const dragOver = ref(false);
const dragOverEdge = ref<DropEdge | null>(null);
const previewVisibility = reactive(new Map<number, boolean>());
const tabContextMenu = ref({
  show: false,
  x: 0,
  y: 0,
  paneId: null as EditorPaneId | null,
  index: null as number | null,
});

/** 编辑区右键菜单：刷新当前文件内容 + 编辑路径注释。 */
const fileContextMenu = ref({ show: false, x: 0, y: 0 });
const fileContextMenuOptions = [
  // 从归档重读当前文件并把编辑器内容替换掉；有未保存修改时会先确认（见 onRefreshActiveFile）。
  { label: "刷新", key: "refresh-file" },
  { label: "编辑注释…", key: "edit-annotation" },
];

function onEditorContextMenu(event: MouseEvent): void {
  if (!editor.activeTab) return;
  fileContextMenu.value = { show: true, x: event.clientX, y: event.clientY };
}

async function onRefreshActiveFile(): Promise<void> {
  const tab = editor.activeTab;
  if (!tab) return;
  // 无未保存修改：直接从归档重读并替换，无副作用。
  if (tab.text === tab.original) {
    await refreshActiveFileNow(tab.index);
    return;
  }
  // 有未保存修改：刷新会丢弃本地草稿，必须先确认，避免误点丢内容。
  dialog.warning({
    title: "刷新文件内容",
    content: "该文件有未保存的修改，刷新会丢弃这些修改并从归档重新读取。继续吗？",
    positiveText: "刷新并丢弃修改",
    negativeText: "取消",
    onPositiveClick: () => refreshActiveFileNow(tab.index),
  });
}

/** 执行「刷新」：从归档重读该标签并替换编辑器内容（与旧行为不同：不再保留本地草稿）。 */
async function refreshActiveFileNow(index: number): Promise<void> {
  try {
    const discarded = await editor.reloadFileFromArchive(index);
    message.success(discarded ? "已刷新文件内容（本地修改已丢弃）" : "已刷新文件内容");
  } catch (e: any) {
    message.error(`刷新失败: ${e?.message ?? e}`);
  }
}

function onFileContextMenuSelect(key: string | number): void {
  fileContextMenu.value.show = false;
  if (key === "refresh-file") {
    void onRefreshActiveFile();
    return;
  }
  if (key !== "edit-annotation") return;
  const tab = editor.activeTab;
  if (!tab) return;
  void annotationEdit.openFor({ path: tab.path, isDir: false });
}

type DropEdge = "left" | "right" | "top" | "bottom";
const dragMime = "application/x-pvfine-editor-tab";

/** 「修改/创建占位符译文」对话框状态。 */
const placeholderEdit = reactive({
  show: false,
  index: -1,
  tableIndex: 0,
  key: "",
  value: "",
  fallback: false,
  missing: false,
  saving: false,
  /** 源表属于客户端汉化禁动表：确定后走「改写到安全表」。 */
  protectedTable: false,
  /** 改写目标（安全表号）。 */
  targetTableIndex: 1,
});

/** 「改写目标」清单（打开对话框时刷新，含每张表在当前归档里的可写性）。 */
const safeTableList = ref<SafeTableTarget[]>([]);
const safeTableOptions = computed(() => {
  const options: {
    label: string;
    value: number;
    disabled?: boolean;
  }[] = [
    {
      label: "自动匹配（推荐：挑一张当前可写的安全表）",
      value: AUTO_TARGET,
    },
  ];
  for (const entry of safeTableList.value) {
    options.push({
      label: entry.writable
        ? `表 ${entry.index} · ${entry.label}`
        : `表 ${entry.index} · ${entry.label}（当前归档不可写）`,
      value: entry.index,
      disabled: !entry.writable,
    });
  }
  return options;
});

async function openPlaceholderEdit(tabIndex: number, request: PlaceholderEditRequest): Promise<void> {
  placeholderEdit.show = true;
  placeholderEdit.index = tabIndex;
  placeholderEdit.tableIndex = request.tableIndex;
  placeholderEdit.key = request.key;
  placeholderEdit.value = request.missing ? "" : request.value;
  placeholderEdit.fallback = request.fallback;
  placeholderEdit.missing = request.missing;
  placeholderEdit.saving = false;
  placeholderEdit.protectedTable = false;
  placeholderEdit.targetTableIndex = defaultTargetTableIndex();
  // 先取白名单（判断是否为禁动表），再取带可写性的目标清单。
  await loadStringTableGuard();
  const targets = await loadSafeTableTargets(true);
  safeTableList.value = targets?.allowedTables ?? [];
  placeholderEdit.targetTableIndex = defaultTargetTableIndex();
  placeholderEdit.protectedTable = isProtectedStringTable(request.tableIndex);
}

async function confirmPlaceholderEdit(): Promise<void> {
  if (placeholderEdit.saving) return;
  const value = placeholderEdit.value.trim();
  if (!value) {
    message.warning("译文不能为空");
    return;
  }
  placeholderEdit.saving = true;
  try {
    if (placeholderEdit.protectedTable) {
      // 禁动表不能写：改走「写安全表 + 改写脚本里的表号」两步，一次完成。
      const result = await editor.rewritePlaceholderToSafeTable(
        placeholderEdit.index,
        placeholderEdit.tableIndex,
        placeholderEdit.key,
        value,
        placeholderEdit.targetTableIndex
      );
      placeholderEdit.show = false;
      const target = result?.targetTableIndex ?? placeholderEdit.targetTableIndex;
      let detail = "";
      if (result?.autoPicked) {
        const skipped = result.skippedTableIndexes ?? [];
        detail =
          skipped.length > 0
            ? `（自动匹配：跳过不可写的表 ${skipped.join("/")}）`
            : "（自动匹配）";
      }
      message.success(
        `已写入安全表 ${target}${detail}；` +
          `脚本里的 <${placeholderEdit.tableIndex}::${placeholderEdit.key}> 已改写为 ` +
          `<${target}::${placeholderEdit.key}>` +
          `（共 ${result?.rewrittenOccurrences ?? 0} 处，保存归档后生效）`
      );
      return;
    }
    await editor.setPlaceholderText(
      placeholderEdit.index,
      placeholderEdit.tableIndex,
      placeholderEdit.key,
      value
    );
    if (placeholderEdit.missing) {
      // 新建表项时顺手把引用插到光标处，省得手写 {8=`<表号::键名>`}。
      const inserted = insertPlaceholderReference(
        placeholderEdit.index,
        placeholderEdit.tableIndex,
        placeholderEdit.key
      );
      placeholderEdit.show = false;
      message.success(
        inserted
          ? "已创建字符串表条目并插入引用（保存后生效）"
          : "已创建字符串表条目（未找到可插入的光标位置）"
      );
      return;
    }
    placeholderEdit.show = false;
    message.success("已写入字符串表（保存后生效）");
  } catch (error) {
    message.error(String(error));
  } finally {
    placeholderEdit.saving = false;
  }
}

/** 编辑器实例(按标签索引),用于在光标处插入文本。 */
const editorRefs = new Map<number, { insertText: (text: string) => boolean }>();

function setEditorRef(index: number, instance: unknown): void {
  if (instance) {
    editorRefs.set(index, instance as { insertText: (text: string) => boolean });
  } else {
    editorRefs.delete(index);
  }
}

function insertPlaceholderReference(index: number, tableIndex: number, key: string): boolean {
  // 标记里的 8 是 token 类型(字符串池引用)，表号在 <表号::键名> 里。
  return editorRefs.get(index)?.insertText("{8=`<" + tableIndex + "::" + key + ">`}") ?? false;
}

/** 该标签是否有待执行的搜索定位请求（有则交给编辑器滚动到命中处）。 */
function revealFor(index: number): { seq: number; needles: string[]; line?: number } | null {
  const pending = editor.pendingReveal;
  return pending && pending.index === index ? pending : null;
}

/** .lst 清单文件：启用行内名称标签（仅可见行惰性解析，见 listNames.ts）。 */
function isListFile(path: string): boolean {
  return path.toLowerCase().endsWith(".lst");
}

/** 单击可跳转注释（如 .lst 路径）：打开目标文件并在左侧文件树中定位。 */
async function onActivateReference(fileIndex: number, paneId: EditorPaneId): Promise<void> {
  // 已打开过的标签可直接算出路径 ⇒ 先发起定位，再打开文件，两者不再串行等待
  // （对齐「搜索视窗」的体验：定位与打开并行，点完立刻有反馈）。
  // 整条链路进操作时间线：卡死时面板敲 SCRZ 能看到停在哪一步。
  markTrace("点击路径链接", {
    来源列表: activeTab.value?.path ?? "-",
    目标索引: fileIndex,
  });
  const knownPath = editor.tabs.find((tab) => tab.index === fileIndex)?.path;
  // 先打开文件（主诉求），完成后再定位左树——避免树重建与文件渲染抢主线程，
  // 否则"点路径打开文件"会卡死。
  await traceAsync(
    `打开目标文件 ${knownPath ?? `#${fileIndex}`}`,
    () => editor.openFile(fileIndex, paneId),
    { 目标索引: fileIndex },
  );
  const path = knownPath ?? editor.tabs.find((tab) => tab.index === fileIndex)?.path;
  if (path) void traceAsync(`定位左树 ${path}`, () => explorer.revealPath(path));
}

/** 「插入字符串引用」对话框:新建/更新表项，并把引用插到光标处。 */
const referenceInsert = reactive({
  show: false,
  tableIndex: "3",
  key: "",
  value: "",
  saving: false,
});

function openReferenceInsert(): void {
  referenceInsert.show = true;
  referenceInsert.key = "";
  referenceInsert.value = "";
  referenceInsert.saving = false;
}

async function confirmReferenceInsert(): Promise<void> {
  const active = activeTab.value;
  if (!active) {
    message.warning("请先打开一个文件");
    return;
  }
  const key = referenceInsert.key.trim();
  const tableIndex = Number.parseInt(referenceInsert.tableIndex.trim(), 10);
  if (!Number.isFinite(tableIndex)) {
    message.warning("表号必须是整数");
    return;
  }
  if (!key) {
    message.warning("键名不能为空");
    return;
  }
  const value = referenceInsert.value.trim();
  if (!value) {
    message.warning("译文不能为空");
    return;
  }
  referenceInsert.saving = true;
  try {
    await editor.setPlaceholderText(active.index, tableIndex, key, value);
    const inserted = insertPlaceholderReference(active.index, tableIndex, key);
    referenceInsert.show = false;
    message.success(
      inserted ? "已插入引用并写入字符串表（保存后生效）" : "已写入字符串表（未找到可插入的光标位置）"
    );
  } catch (error) {
    message.error(String(error));
  } finally {
    referenceInsert.saving = false;
  }
}

const pane = computed(() => editor.panes.find((item) => item.id === paneId));
const paneTabs = computed(() => {
  const indexes = pane.value?.tabIndexes ?? [];
  return indexes
    .map((index) => editor.tabs.find((tab) => tab.index === index))
    .filter((tab): tab is EditorTab => !!tab);
});
const activeKeyStr = computed(() => {
  const key = pane.value?.activeKey;
  return key === null || key === undefined ? undefined : String(key);
});
const activeTab = computed(() => {
  const activeKey = pane.value?.activeKey;
  return paneTabs.value.find((tab) => tab.index === activeKey) ?? null;
});

const fileRegistration = reactive({
  show: false,
  loading: false,
  fileIndex: -1,
  filePath: "",
  targets: [] as ListRegistrationTarget[],
  listPath: "",
  id: "",
  generatedID: "",
});
const fileRegistrationOptions = computed(() =>
  fileRegistration.targets.map((target) => ({
    label: `${target.listPath}  ·  ${target.entryPath}`,
    value: target.listPath,
  })),
);
const activeHasID = computed(() =>
  (activeTab.value?.tags ?? []).some((tag) => tag.id.trim() !== ""),
);
const canRegisterActiveFile = computed(
  () =>
    archive.open &&
    archive.indexReady &&
    !!activeTab.value &&
    !activeHasID.value &&
    !fileRegistration.loading,
);

/** list 查重（只对当前打开的 .lst 生效），结果显示在独立的查重窗口里。 */
const listDuplicate = reactive({
  show: false,
  fileName: "",
  issues: [] as ListDuplicateIssue[],
  /** 本次检测时刻（面板上显示，便于确认结果对应的是当前内容）。 */
  checkedAt: 0,
});

const canCheckListDuplicate = computed(
  () => archive.open && !!activeTab.value && isListFile(activeTab.value.path),
);

function runListDuplicateCheck(): void {
  const tab = activeTab.value;
  if (!tab) {
    message.warning("请先打开一个 lst 文件");
    return;
  }
  // 大文件的文本没有载入窗口（走 TXT 页模式，见 services/large_text.go）：
  // 这里必须明确拒绝，否则会拿空串去查重、报「未发现重复条目」——错得看不出错。
  if (tab.largeFile) {
    message.warning("大文件走 TXT 页模式（文本未载入窗口），list 查重暂不支持；可改用「用外部编辑器打开」后在本机查重");
    return;
  }
  // 每次都按编辑器里的「当前内容」全量重算（未保存的改动同样生效），
  // 结果整体替换，不会留下上一次检测的条目。
  const issues = findListDuplicates(parseListEntries(tab.text));
  listDuplicate.fileName = tab.path;
  listDuplicate.issues = issues;
  listDuplicate.checkedAt = Date.now();
  listDuplicate.show = true;
  if (issues.length === 0) {
    message.success("list 查重完成：未发现重复条目");
  } else {
    message.warning(`list 查重完成：发现 ${issues.length} 处问题`);
  }
}

function onListDuplicateLocate(line: number): void {
  const tab = activeTab.value;
  if (!tab) return;
  void editor.revealFileLine(tab.index, line);
}
type FileTagKind = "id" | "name" | "path";
interface FileTag {
  kind: FileTagKind;
  value: string;
}
const fileTags = computed(() => {
  const ids = new Set<string>();
  const names = new Set<string>();
  const result: FileTag[] = [];
  for (const tag of activeTab.value?.tags ?? []) {
    const id = tag.id.trim();
    if (id && !ids.has(id)) {
      ids.add(id);
      result.push({ kind: "id", value: id });
    }
    const name = tag.name.trim();
    if (name && !names.has(name)) {
      names.add(name);
      result.push({ kind: "name", value: name });
    }
  }
  // 路径放在最后：它通常最长，靠后的位置被截断时不影响前面的 id/名称。
  const path = activeTab.value?.path.trim();
  if (path) result.push({ kind: "path", value: path });
  return result;
});
const canRevealActiveFile = computed(
  () => archive.open && !!activeTab.value && !revealingFile.value
);
const activeBookmarked = computed(
  () => !!activeTab.value && bookmarks.isBookmarkedInGroup(activeTab.value.path)
);
const activeTabDirty = computed(
  () => !!activeTab.value && activeTab.value.editable && activeTab.value.text !== activeTab.value.original
);
const canBookmarkActiveFile = computed(
  () => archive.open && bookmarks.loaded && !!activeTab.value && !bookmarking.value
);
/** 分屏下拉：一次选择即执行，控件本身不保存状态（value 恒为 null）。 */
const splitOptions = [
  { label: "左右分屏", value: "columns" },
  { label: "上下分屏", value: "rows" },
];

function onSplitSelect(value: unknown): void {
  if (value === "columns" || value === "rows") editor.split(value, paneId);
}

function previewFile(tab: EditorTab): PreviewFile {
  return { index: tab.index, path: tab.path, text: tab.text, editable: tab.editable };
}

function previewProviderFor(tab: EditorTab) {
  // 大文件的文本没进窗口（TXT 页模式）：预览拿不到内容，直接不给入口，
  // 免得点开是空白让人以为文件是空的。
  if (tab.largeFile) return undefined;
  return getPreviewProvider(previewFile(tab));
}

function isPreviewOpen(index: number): boolean {
  return previewVisibility.get(index) ?? true;
}

function togglePreview(index: number): void {
  previewVisibility.set(index, !isPreviewOpen(index));
}

function closePreview(index: number): void {
  previewVisibility.set(index, false);
}

watch(
  activeTab,
  (tab) => {
    if (tab && previewProviderFor(tab) && !previewVisibility.has(tab.index)) {
      previewVisibility.set(tab.index, true);
    }
  },
  { immediate: true },
);

function activatePane(): void {
  editor.activatePane(paneId);
}

function onActive(key: string | number): void {
  editor.activateTab(paneId, Number(key));
}

function onClose(index: number): void {
  editor.requestCloseTab(index, paneId);
}

function onTabMouseDown(event: MouseEvent, index: number): void {
  if (event.button !== 1) return;
  event.preventDefault();
  event.stopPropagation();
  onClose(index);
}

const tabContextMenuOptions = computed(() => [
  {
    label: "关闭当前",
    key: "close",
    disabled: tabContextMenu.value.index === null,
  },
  {
    label: "关闭所有",
    key: "close-all",
    disabled: editor.tabs.length === 0,
  },
  {
    label: "关闭其它",
    key: "close-others",
    disabled: editor.tabs.length <= 1 || tabContextMenu.value.index === null,
  },
]);

function hideTabContextMenu(): void {
  tabContextMenu.value.show = false;
  tabContextMenu.value.paneId = null;
  tabContextMenu.value.index = null;
}

function onTabContextMenu(event: MouseEvent, index: number): void {
  event.preventDefault();
  editor.activateTab(paneId, index);
  tabContextMenu.value = {
    show: true,
    x: event.clientX,
    y: event.clientY,
    paneId,
    index,
  };
}

function onTabContextMenuSelect(key: string | number): void {
  const index = tabContextMenu.value.index;
  const targetPaneId = tabContextMenu.value.paneId;
  hideTabContextMenu();
  if (index === null) return;

  if (key === "close") {
    editor.requestCloseTab(index, targetPaneId ?? paneId);
  } else if (key === "close-all") {
    editor.requestCloseAll();
  } else if (key === "close-others") {
    editor.requestCloseOthers(index);
  }
}

async function onRevealActiveFile(): Promise<void> {
  if (revealingFile.value) return;
  const tab = activeTab.value;
  if (!archive.open || !tab) return;

  revealingFile.value = true;
  try {
    if (explorer.mode === "search") explorer.clearSearch();
    const found = await explorer.revealPath(tab.path);
    if (!found) message.info("当前文件未在资源管理器中找到");
  } catch (error: any) {
    message.error(`定位文件失败: ${error?.message ?? error}`);
  } finally {
    revealingFile.value = false;
  }
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
      content: `将复制“${active.name}”为新的可编辑书签簿，并把当前文件加入副本。继续吗？`,
      positiveText: "复制并加入",
      negativeText: "取消",
      onPositiveClick: () => finish(!!bookmarks.copyBook(active.id)),
      onNegativeClick: () => finish(false),
      onClose: () => finish(false),
    });
  });
}

/** 「AI 引入」：把当前文件交给右侧 AI 助手定位，并自动切到 AI 面板。 */
function onIntroduceToAI(): void {
  const tab = activeTab.value;
  if (!tab) return;
  const { added, truncated } = ai.introduceFiles([{ path: tab.path, title: tab.title }]);
  sidebar.show("ai");
  if (added === 0) {
    message.info("该文件已在 AI 引入中");
    return;
  }
  const truncatedText = truncated ? `，已达上限 ${MAX_CONTEXT_FILES} 个` : "";
  message.success(`已引入给 AI：${tab.path}${truncatedText}`);
}

async function onBookmarkActive(): Promise<void> {
  const tab = activeTab.value;
  if (!tab || !canBookmarkActiveFile.value || activeBookmarked.value) return;
  bookmarking.value = true;
  try {
    if (!(await confirmBuiltinBookmarkCopy())) return;
    const result = bookmarks.addEntries([
      { path: tab.path, name: tab.title, fileIndex: tab.index },
    ]);
    if (result.added > 0) message.success(`已加入书签：${tab.path}`);
    else message.info("当前文件已在当前书签分组中");
  } catch (error: any) {
    message.error(`加入书签失败: ${error?.message ?? error}`);
  } finally {
    bookmarking.value = false;
  }
}

async function openFileRegistration(): Promise<void> {
  const tab = activeTab.value;
  if (!tab || !canRegisterActiveFile.value) return;
  fileRegistration.loading = true;
  try {
    const options = await archive.listRegistrationOptions(tab.index);
    const targets = (options?.targets ?? []).filter(
      (target): target is ListRegistrationTarget => !!target,
    );
    if (!options || targets.length === 0) {
      throw new Error("没有找到可用的 lst");
    }
    fileRegistration.fileIndex = tab.index;
    fileRegistration.filePath = tab.path;
    fileRegistration.targets = targets;
    fileRegistration.listPath = targets[0].listPath;
    fileRegistration.id = targets[0].suggestedId;
    fileRegistration.generatedID = targets[0].suggestedId;
    fileRegistration.show = true;
  } catch (error: any) {
    message.error(`读取 lst 选项失败：${error?.message ?? error}`);
  } finally {
    fileRegistration.loading = false;
  }
}

function onFileRegistrationListChange(listPath: string): void {
  fileRegistration.listPath = listPath;
  const target = fileRegistration.targets.find((item) => item.listPath === listPath);
  if (!target) return;
  fileRegistration.id = target.suggestedId;
  fileRegistration.generatedID = target.suggestedId;
}

async function confirmFileRegistration(): Promise<void> {
  const id = fileRegistration.id.trim();
  if (!fileRegistration.listPath) {
    message.warning("请选择 lst");
    return;
  }
  if (!id) {
    message.warning("id 不能为空");
    return;
  }
  fileRegistration.loading = true;
  try {
    const result = await archive.registerFileToList(
      fileRegistration.fileIndex,
      fileRegistration.listPath,
      id,
    );
    if (!result) throw new Error("后端没有返回注册结果");
    fileRegistration.show = false;
    message.success(
      archive.info?.paged110
        ? `已注册到 ${result.listPath}，并写入 indexhash`
        : `已注册到 ${result.listPath}`,
    );
  } catch (error: any) {
    message.error(`注册到 lst 失败：${error?.message ?? error}`);
  } finally {
    fileRegistration.loading = false;
  }
}

// 仅作为异常长路径的兜底上限，实际宽度由 CSS 按窗口宽度决定。
const pathHeadLength = 24;
const pathTailLength = 56;

function tagLabel(kind: FileTagKind): string {
  if (kind === "id") return "id";
  if (kind === "name") return "name";
  return "路径";
}

/**
 * 保留路径的末段（目录/文件名本身）和路径起点，中间省略。这里只是防止极端
 * 长路径撑爆标签的兜底；正常长度交给 CSS 按可用宽度收缩并显示省略号。
 */
function shortenPath(path: string): string {
  if (path.length <= pathHeadLength + pathTailLength + 1) return path;
  return `${path.slice(0, pathHeadLength)}…${path.slice(-pathTailLength)}`;
}

/** 路径通常很长，通知里只说明复制了什么，避免整条路径铺满提示。 */
function copyFeedback(tag: FileTag): string {
  return tag.kind === "path" ? "已复制文件路径" : `已复制 ${tag.value}`;
}

async function copyTag(value: string, feedback: string): Promise<void> {
  try {
    // Wails 桌面端使用原生剪贴板,不受 WebView 的 Clipboard 权限限制。
    await Clipboard.SetText(value);
    message.success(feedback);
  } catch {
    try {
      // 浏览器开发模式没有 Wails runtime 时使用 Web Clipboard API。
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
      } else {
        const textarea = document.createElement("textarea");
        textarea.value = value;
        textarea.setAttribute("readonly", "");
        textarea.style.position = "fixed";
        textarea.style.opacity = "0";
        document.body.appendChild(textarea);
        textarea.focus();
        textarea.select();
        const copied = document.execCommand("copy");
        textarea.remove();
        if (!copied) throw new Error("clipboard unavailable");
      }
      message.success(feedback);
    } catch {
      message.error("复制失败,请重试");
    }
  }
}

function sizeText(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function isOpeningTab(index: number): boolean {
  return editor.openingPaneId === paneId && pane.value?.activeKey === index;
}

function onDragStart(event: DragEvent, index: number): void {
  draggingIndex.value = index;
  editor.beginTabDrag(paneId, index);
  if (!event.dataTransfer) return;
  event.dataTransfer.effectAllowed = "move";
  event.dataTransfer.setData(dragMime, JSON.stringify({ paneId, index }));
}

function onDragEnd(): void {
  draggingIndex.value = null;
  editor.endTabDrag();
  dragOver.value = false;
  dragOverEdge.value = null;
}

function readDraggedTab(event: DragEvent): DraggedEditorTab | null {
  if (editor.draggingTab) return editor.draggingTab;
  const raw = event.dataTransfer?.getData(dragMime);
  if (!raw) return null;
  try {
    const value = JSON.parse(raw) as { paneId?: unknown; index?: unknown };
    if (typeof value.paneId !== "string" || typeof value.index !== "number") return null;
    return { paneId: value.paneId, index: value.index };
  } catch {
    return null;
  }
}

function getDropEdge(event: DragEvent): DropEdge | null {
  if (editor.isSplit) return null;
  if (!host.value) return null;
  const rect = host.value.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return null;
  const edgeSize = Math.min(80, Math.max(36, Math.round(Math.min(rect.width, rect.height) * 0.14)));
  const x = event.clientX - rect.left;
  const y = event.clientY - rect.top;
  if (x <= edgeSize) return "left";
  if (x >= rect.width - edgeSize) return "right";
  if (y <= edgeSize) return "top";
  if (y >= rect.height - edgeSize) return "bottom";
  return null;
}

function onDragOver(event: DragEvent): void {
  const dragged = readDraggedTab(event);
  if (!dragged) return;
  dragOver.value = true;
  dragOverEdge.value = getDropEdge(event);
  if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
  editor.activatePane(paneId);
}

function onDragLeave(event: DragEvent): void {
  const related = event.relatedTarget as Node | null;
  if (related && host.value?.contains(related)) return;
  dragOver.value = false;
  dragOverEdge.value = null;
}

function onDrop(event: DragEvent): void {
  const dragged = readDraggedTab(event);
  const edge = dragOverEdge.value ?? getDropEdge(event);
  dragOver.value = false;
  dragOverEdge.value = null;
  editor.endTabDrag();
  if (!dragged) return;

  if (edge) {
    const orientation = edge === "left" || edge === "right" ? "columns" : "rows";
    const insertBefore = edge === "left" || edge === "top";
    editor.splitAndMoveTab(dragged.index, dragged.paneId, paneId, orientation, insertBefore);
    return;
  }
  editor.moveTab(dragged.index, dragged.paneId, paneId);
}
</script>

<template>
  <div
    ref="host"
    class="editor-pane-view"
    :class="{
      'editor-pane-view--active': editor.activePaneId === paneId,
      'editor-pane-view--drop-target': dragOver,
    }"
    @pointerdown="activatePane"
    @focusin="activatePane"
    @dragover.capture.prevent="onDragOver"
    @dragleave="onDragLeave"
    @drop.capture.prevent="onDrop"
  >
    <div
      v-if="dragOverEdge"
      class="drop-preview"
      :class="`drop-preview--${dragOverEdge}`"
      aria-hidden="true"
    >
      <div class="drop-preview-pane" />
      <div class="drop-preview-divider" />
    </div>
    <div v-if="paneTabs.length === 0" class="pane-empty">
      <NSpin v-if="editor.openingPaneId === paneId" />
      <NEmpty v-else size="small" description="从左侧选择一个文件" />
    </div>

    <NTabs
      v-else
      type="card"
      size="small"
      :value="activeKeyStr"
      class="editor-tabs"
      style="height: 100%"
      @update:value="onActive"
    >
      <NTabPane
        v-for="tab in paneTabs"
        :key="`${paneId}:${tab.index}`"
        :name="String(tab.index)"
        display-directive="show:lazy"
        style="height: 100%"
      >
        <template #tab>
          <span
            class="tab-label"
            :class="{ 'tab-label--dragging': draggingIndex === tab.index }"
            :title="tab.path"
            draggable="true"
            @dragstart="onDragStart($event, tab.index)"
            @dragend="onDragEnd"
            @mousedown="onTabMouseDown($event, tab.index)"
            @contextmenu.stop="onTabContextMenu($event, tab.index)"
          >
            <ImageThumbnail v-if="tab.icon" :reference="tab.icon" :size="16" />
            <span :class="['tab-dot', { dirty: tab.text !== tab.original }]" />
            <span class="tab-title">{{ tab.title }}</span>
            <NTooltip>
              <template #trigger>
                <NButton
                  quaternary
                  size="tiny"
                  class="tab-close"
                  @click.stop="onClose(tab.index)"
                >
                  <template #icon><NIcon :size="12"><Dismiss16Regular /></NIcon></template>
                </NButton>
              </template>
              关闭 (Cmd+W)
            </NTooltip>
          </span>
        </template>

        <div class="editor-info-bar" role="toolbar" aria-label="当前文件操作">
          <NTooltip trigger="hover">
            <template #trigger>
              <NButton
                quaternary
                size="tiny"
                class="editor-save-button"
                :type="activeTabDirty ? 'primary' : 'default'"
                :loading="editor.saving"
                aria-label="保存"
                @click="editor.saveActiveTab(paneId)"
              >
                <template #icon><NIcon><Save24Regular /></NIcon></template>
              </NButton>
            </template>
            {{ activeTabDirty ? "保存当前文件 (Cmd+S)" : "当前文件没有待保存的修改" }}
          </NTooltip>

          <div class="editor-file-tags" aria-label="当前文件关联信息">
            <NTag
              v-for="tag in fileTags"
              :key="`${tag.kind}:${tag.value}`"
              size="tiny"
              :bordered="false"
              :type="tag.kind === 'id' ? 'info' : tag.kind === 'name' ? 'success' : 'default'"
              :class="['editor-file-tag', `editor-file-tag--${tag.kind}`]"
              role="button"
              tabindex="0"
              :title="`点击复制${tagLabel(tag.kind)}: ${tag.value}`"
              @click="copyTag(tag.value, copyFeedback(tag))"
              @keydown.enter.prevent="copyTag(tag.value, copyFeedback(tag))"
              @keydown.space.prevent="copyTag(tag.value, copyFeedback(tag))"
            >
              {{ tag.kind === "path" ? shortenPath(tag.value) : tag.value }}
            </NTag>
          </div>

          <div class="editor-pane-actions" role="group" aria-label="编辑器操作">
            <NTooltip trigger="hover">
              <template #trigger>
                <NButton
                  quaternary
                  size="tiny"
                  :type="settings.plainTextMode ? 'primary' : 'default'"
                  aria-label="纯文本模式"
                  @click="settings.togglePlainTextMode()"
                >
                  <template #icon><NIcon><TextT24Regular /></NIcon></template>
                  纯文本
                </NButton>
              </template>
              {{ settings.plainTextMode
                ? "纯文本模式已开启（Alt+T 关闭）：关闭注解标签 / 语法着色 / 清单名称标签 / 补全，像记事本一样打开"
                : "纯文本模式（Alt+T）：像记事本一样打开，关闭注解标签 / 着色 / 名称标签，大文件更快" }}
            </NTooltip>
            <NTooltip v-if="activeTab?.index === tab.index && isListFile(tab.path)" trigger="hover">
              <template #trigger>
                <NButton
                  quaternary
                  size="tiny"
                  :disabled="!canCheckListDuplicate || tab.largeFile"
                  aria-label="list 查重"
                  @click="runListDuplicateCheck"
                >
                  <template #icon><NIcon><Search24Regular /></NIcon></template>
                  list查重
                </NButton>
              </template>
              {{ tab.largeFile
                ? "大文件走 TXT 页模式（文本未载入窗口），暂不支持查重"
                : "检查当前 lst 里重复的条目（ID 重复 / 路径重复 / 整行重复）" }}
            </NTooltip>
            <NTooltip v-if="activeTab?.index === tab.index && !activeHasID" trigger="hover">
              <template #trigger>
                <NButton
                  quaternary
                  size="tiny"
                  :loading="fileRegistration.loading"
                  :disabled="!canRegisterActiveFile"
                  aria-label="注册到 lst"
                  @click="openFileRegistration"
                >
                  <template #icon><NIcon><DocumentAdd24Regular /></NIcon></template>
                  注册到lst
                </NButton>
              </template>
              自动生成一个可用 id，确认后写入当前文件对应的 lst
            </NTooltip>
            <NTooltip trigger="hover">
              <template #trigger>
                <NButton
                  quaternary
                  size="tiny"
                  :type="ai.contextFiles.some((f) => f.path === activeTab?.path) ? 'primary' : 'default'"
                  :disabled="!activeTab"
                  aria-label="AI 引入"
                  @click="onIntroduceToAI"
                >
                  <template #icon><NIcon><Bot24Regular /></NIcon></template>
                  AI 引入
                </NButton>
              </template>
              把当前文件引入给右侧 AI 助手：AI 可直接定位此窗口并按要求修改内容
            </NTooltip>
            <NTooltip trigger="hover">
              <template #trigger>
                <NButton
                  quaternary
                  size="tiny"
                  :type="activeBookmarked ? 'primary' : 'default'"
                  :loading="bookmarking"
                  :disabled="!canBookmarkActiveFile || activeBookmarked"
                  aria-label="加入书签"
                  @click="onBookmarkActive"
                >
                  <template #icon><NIcon><BookmarkAdd24Regular /></NIcon></template>
                  {{ activeBookmarked ? "已在书签" : "加入书签" }}
                </NButton>
              </template>
              {{ activeBookmarked ? "当前文件已在当前书签簿" : "加入当前书签簿" }}
            </NTooltip>

            <NTooltip trigger="hover">
              <template #trigger>
                <NButton
                  quaternary
                  size="tiny"
                  :loading="revealingFile"
                  :disabled="!canRevealActiveFile"
                  @click="onRevealActiveFile"
                >
                  <template #icon><NIcon><DocumentSearch24Regular /></NIcon></template>
                  在资源管理器中选中
                </NButton>
              </template>
              定位当前文件
            </NTooltip>


            <NTooltip v-if="previewProviderFor(tab)" trigger="hover">
              <template #trigger>
                <NButton
                  quaternary
                  size="tiny"
                  :type="isPreviewOpen(tab.index) ? 'primary' : 'default'"
                  aria-label="切换文件预览"
                  @click="togglePreview(tab.index)"
                >
                  <template #icon>
                    <NIcon><EyeOff24Regular v-if="isPreviewOpen(tab.index)" /><Eye24Regular v-else /></NIcon>
                  </template>
                  预览
                </NButton>
              </template>
              {{ isPreviewOpen(tab.index) ? "收起文件预览" : "打开文件预览" }}
            </NTooltip>

            <NTooltip v-if="archive.info?.paged110" trigger="hover">
              <template #trigger>
                <NButton
                  quaternary
                  size="tiny"
                  :disabled="!tab.editable"
                  aria-label="插入字符串引用"
                  @click="openReferenceInsert"
                >
                  <template #icon><NIcon><TextAddT24Regular /></NIcon></template>
                  字符串引用
                </NButton>
              </template>
              在光标处插入 {8=`&lt;表号::键名&gt;`}，并创建/更新字符串表条目
            </NTooltip>

            <!-- 分屏：左右/上下合并为一个下拉控件，固定在工具条最右侧。 -->
            <NSelect
              class="editor-split-select"
              size="tiny"
              :value="null"
              :options="splitOptions"
              :disabled="!activeTab"
              placeholder="分屏"
              @update:value="onSplitSelect"
            />
          </div>
        </div>

        <div class="pane-body">
          <div v-if="!tab.editable" class="readonly-hint">
            该文件类型(text {{ tab.dataType }},{{ sizeText(tab.size) }})暂不支持编辑
          </div>
          <NSpin v-if="isOpeningTab(tab.index)" style="margin-top: 120px" />
          <!--
            大文件（tab.largeFile：归档内 >8MB / 展开后 >4MB / >10 万行）走「连续全文 TXT」：
            后端不下发整份文本（services/editor.go 大文件分支），窗口按视口取行
            （services/large_text.go 的 GetFileLines/SetFileLines）。
            实测整份 2740 万字符进窗口会让界面停摆 43 秒。
          -->
          <LargeScrollView
            v-else-if="tab.largeFile"
            :index="tab.index"
            :path="tab.path"
            :size="tab.size"
            :reveal="revealFor(tab.index)"
          />
          <CodeEditor
            v-else
            :ref="(instance: unknown) => setEditorRef(tab.index, instance)"
            :doc="tab.text"
            :read-only="!tab.editable"
            :large-file="tab.largeFile"
            :plain-text="settings.plainTextMode"
            :annotations="settings.plainTextMode ? [] : tab.annotations"
            :tag-placement="settings.annotationTagPlacement"
            :show-reference-tags="settings.showReferenceTags"
            :vim-mode="settings.vimMode"
            :theme-id="props.themeId"
            :reveal="revealFor(tab.index)"
            :list-names="isListFile(tab.path)"
            @change="(text: string) => editor.updateContent(tab.index, text)"
            @open-reference="(fileIndex: number) => editor.openFile(fileIndex, paneId)"
            @activate-reference="(fileIndex: number) => onActivateReference(fileIndex, paneId)"
            @edit-placeholder="(request: PlaceholderEditRequest) => openPlaceholderEdit(tab.index, request)"
            @contextmenu.prevent="onEditorContextMenu"
          />
          <PreviewHost
            v-if="previewProviderFor(tab)"
            :file="previewFile(tab)"
            :active="editor.activePaneId === paneId && activeTab?.index === tab.index"
            :open="isPreviewOpen(tab.index)"
            @close="closePreview(tab.index)"
          />
        </div>
      </NTabPane>
    </NTabs>
    <NDropdown
      trigger="manual"
      placement="bottom-start"
      :show="tabContextMenu.show"
      :x="tabContextMenu.x"
      :y="tabContextMenu.y"
      :options="tabContextMenuOptions"
      @select="onTabContextMenuSelect"
      @clickoutside="hideTabContextMenu"
    />
    <NDropdown
      trigger="manual"
      placement="bottom-start"
      :show="fileContextMenu.show"
      :x="fileContextMenu.x"
      :y="fileContextMenu.y"
      :options="fileContextMenuOptions"
      @select="onFileContextMenuSelect"
      @clickoutside="fileContextMenu.show = false"
    />
    <ListDuplicatePanel
      :show="listDuplicate.show && paneId === editor.activePaneId"
      :file-name="listDuplicate.fileName"
      :issues="listDuplicate.issues"
      :checked-at="listDuplicate.checkedAt"
      @close="listDuplicate.show = false"
      @locate="onListDuplicateLocate"
      @recheck="runListDuplicateCheck"
    />
    <NModal
      v-model:show="fileRegistration.show"
      preset="card"
      title="注册到 lst"
      style="width: min(560px, calc(100vw - 32px))"
      :mask-closable="!fileRegistration.loading"
    >
      <div class="placeholder-edit">
        <div class="placeholder-edit-hint">
          当前文件：{{ fileRegistration.filePath }}。确认后会写入归档内存，保存 PVF 后落盘。
        </div>
        <NSelect
          :value="fileRegistration.listPath"
          :options="fileRegistrationOptions"
          placeholder="选择 lst"
          @update:value="onFileRegistrationListChange"
        />
        <NInput
          :value="fileRegistration.id"
          placeholder="输入数字 id"
          @update:value="fileRegistration.id = $event"
        >
          <template #prefix>id</template>
        </NInput>
        <div class="placeholder-edit-hint">
          默认 id：{{ fileRegistration.generatedID }}；可以直接修改。{{ archive.info?.paged110 ? "110page 会同步写入 indexhash。" : "当前归档只写入 lst。" }}
        </div>
      </div>
      <template #footer>
        <div class="placeholder-edit-footer">
          <NButton size="small" :disabled="fileRegistration.loading" @click="fileRegistration.show = false">
            取消
          </NButton>
          <NButton
            size="small"
            type="primary"
            :loading="fileRegistration.loading"
            @click="confirmFileRegistration"
          >
            确定
          </NButton>
        </div>
      </template>
    </NModal>
    <NModal
      v-model:show="placeholderEdit.show"
      preset="card"
      :title="(placeholderEdit.protectedTable ? '改写到安全表 ' : '修改译文 ') + `<${placeholderEdit.tableIndex}::${placeholderEdit.key}>`"
      style="width: 480px"
      :mask-closable="!placeholderEdit.saving"
    >
      <div class="placeholder-edit">
        <div v-if="placeholderEdit.protectedTable" class="placeholder-edit-warn">
          表 {{ placeholderEdit.tableIndex }} 属于客户端汉化禁动表（直接改写它会让界面成片乱码）。
          确定后一次完成两步：① 把译文写进安全表；
          ② 把脚本里的 &lt;{{ placeholderEdit.tableIndex }}::{{ placeholderEdit.key }}&gt; 改写为
          &lt;{{ placeholderEdit.targetTableIndex }}::{{ placeholderEdit.key }}&gt;（脚本 token 数不变）。
        </div>
        <div v-else class="placeholder-edit-hint">
          只改字符串表里的这一条，脚本里的占位符不动；保存归档后生效。
        </div>
        <NSelect
          v-if="placeholderEdit.protectedTable"
          v-model:value="placeholderEdit.targetTableIndex"
          size="small"
          style="margin-bottom: 8px"
          :options="safeTableOptions"
          placeholder="选择安全表"
        />
        <NInput
          v-model:value="placeholderEdit.value"
          type="textarea"
          :autosize="{ minRows: 1, maxRows: 4 }"
          placeholder="输入显示文本"
          @keydown.enter.exact.prevent="confirmPlaceholderEdit"
        />
        <div v-if="placeholderEdit.fallback && !placeholderEdit.protectedTable" class="placeholder-edit-warn">
          该译文目前来自语言覆盖层（标记为「未翻译」），修改后会写入覆盖层那一份。
        </div>
        <div v-if="placeholderEdit.missing && !placeholderEdit.protectedTable" class="placeholder-edit-warn">
          字符串表里还没有这个键，确定后会创建该条目，并把
          {8=`&lt;表号::键名&gt;`} 插入到光标处。
        </div>
      </div>
      <template #footer>
        <div class="placeholder-edit-footer">
          <NButton size="small" :disabled="placeholderEdit.saving" @click="placeholderEdit.show = false">
            取消
          </NButton>
          <NButton
            size="small"
            type="primary"
            :loading="placeholderEdit.saving"
            @click="confirmPlaceholderEdit"
          >
            确定
          </NButton>
        </div>
      </template>
    </NModal>

    <NModal
      v-model:show="referenceInsert.show"
      preset="card"
      title="插入字符串引用"
      style="width: 480px"
      :mask-closable="!referenceInsert.saving"
    >
      <div class="placeholder-edit">
        <div class="placeholder-edit-hint">
          在当前光标处插入 {8=`&lt;表号::键名&gt;`}（8 是 token 类型，不是表号），并把译文写入
          该表号对应的字符串表；用 3=装备、13=道具 最常见。
        </div>
        <NInput v-model:value="referenceInsert.tableIndex" placeholder="表号，如 3（装备）/ 13（道具）">
          <template #prefix>表号</template>
        </NInput>
        <NInput v-model:value="referenceInsert.key" placeholder="键名，如 name_900000001">
          <template #prefix>键名</template>
        </NInput>
        <NInput
          v-model:value="referenceInsert.value"
          type="textarea"
          :autosize="{ minRows: 1, maxRows: 4 }"
          placeholder="显示文本"
        >
          <template #prefix>译文</template>
        </NInput>
      </div>
      <template #footer>
        <div class="placeholder-edit-footer">
          <NButton size="small" :disabled="referenceInsert.saving" @click="referenceInsert.show = false">
            取消
          </NButton>
          <NButton
            size="small"
            type="primary"
            :loading="referenceInsert.saving"
            @click="confirmReferenceInsert"
          >
            插入并写入
          </NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.placeholder-edit {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.placeholder-edit-hint {
  color: var(--pvf-text-muted);
  font-size: 12px;
}
.placeholder-edit-warn {
  color: var(--pvf-warning, var(--pvf-text-secondary));
  font-size: 12px;
}
.placeholder-edit-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.editor-pane-view {
  position: relative;
  height: 100%;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.editor-pane-view--active {
  box-shadow: inset 0 0 0 1px var(--pvf-effect-active-pane-ring);
}
.editor-pane-view--drop-target {
  box-shadow: inset 0 0 0 1px var(--pvf-effect-success-ring);
}
.drop-preview {
  position: absolute;
  z-index: 20;
  pointer-events: none;
  inset: 0;
  border: 1px solid var(--pvf-effect-success-edge);
}
.drop-preview-pane {
  position: absolute;
  background: var(--pvf-surface-drag-preview);
}
.drop-preview-divider {
  position: absolute;
  background: var(--pvf-success);
  box-shadow: 0 0 8px var(--pvf-effect-success-glow);
}
.drop-preview--left .drop-preview-pane {
  top: 0;
  bottom: 0;
  left: 0;
  width: 50%;
}
.drop-preview--left .drop-preview-divider {
  top: 0;
  bottom: 0;
  left: 50%;
  width: 2px;
}
.drop-preview--right .drop-preview-pane {
  top: 0;
  right: 0;
  bottom: 0;
  width: 50%;
}
.drop-preview--right .drop-preview-divider {
  top: 0;
  right: 50%;
  bottom: 0;
  width: 2px;
}
.drop-preview--top .drop-preview-pane {
  top: 0;
  right: 0;
  left: 0;
  height: 50%;
}
.drop-preview--top .drop-preview-divider {
  top: 50%;
  right: 0;
  left: 0;
  height: 2px;
}
.drop-preview--bottom .drop-preview-pane {
  right: 0;
  bottom: 0;
  left: 0;
  height: 50%;
}
.drop-preview--bottom .drop-preview-divider {
  right: 0;
  bottom: 50%;
  left: 0;
  height: 2px;
}
.pane-empty {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.editor-tabs {
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}
.editor-tabs :deep(.n-tabs-nav) {
  padding: 0 6px;
}
.editor-tabs :deep(.n-tabs-pane-wrapper),
.editor-tabs :deep(.n-tab-pane) {
  height: 100%;
  min-height: 0;
  padding: 0 !important;
}
.editor-tabs :deep(.n-tabs-pane-wrapper) {
  flex: 1;
  min-width: 0;
  overflow: hidden;
}
.editor-tabs :deep(.n-tab-pane) {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 260px;
  min-width: 0;
  overflow: hidden;
}
.tab-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tab-label--dragging {
  opacity: 0.45;
}
.tab-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: transparent;
  border: 1px solid var(--pvf-border-strong);
  flex-shrink: 0;
}
.tab-dot.dirty {
  background: var(--pvf-success);
  border-color: var(--pvf-success);
}
.tab-close {
  padding: 0 2px;
  height: auto;
  flex-shrink: 0;
}
/*
 * 两行布局（用户 2026-09-24 要求）：第一行是保存 + 文件操作按钮，
 * 第二行是文件标签（ID / 名称 / 路径），从行首左对齐、从左到右排列。
 * 用 flex-wrap + order + basis:100% 换行，避免改动模板结构。
 */
.editor-info-bar {
  flex: 0 0 auto;
  min-width: 0;
  min-height: 30px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  row-gap: 4px;
  padding: 2px 8px;
  border-bottom: 1px solid var(--pvf-border-subtle);
  background: var(--pvf-surface-subtle);
}
.editor-save-button {
  flex: 0 0 auto;
  order: 0;
}
.editor-file-tags {
  order: 2;
  flex: 1 1 100%;
  min-width: 0;
  min-height: 20px;
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 4px;
  overflow-x: auto;
  scrollbar-width: none;
}
.editor-file-tags::-webkit-scrollbar {
  display: none;
}
.editor-file-tag {
  flex: 0 0 auto;
  max-width: 240px;
  cursor: pointer;
  user-select: none;
}
/*
 * 路径标签吃掉工具栏的剩余空间：窗口宽就展示得更完整，窗口窄则由 flex 收缩
 * 并显示省略号。上限用 vw 跟窗口联动，避免在大窗口下仍被固定像素卡住。
 */
.editor-file-tag--path {
  flex: 0 1 auto;
  min-width: 96px;
  max-width: min(60vw, 720px);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.editor-file-tag :deep(.n-tag__content) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.editor-pane-actions {
  order: 1;
  flex-shrink: 0;
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 2px;
}
.editor-split-select {
  flex: 0 0 auto;
  width: 104px;
  margin-left: 4px;
}

/*
 * 用户 2026-09-28：把「保存」按钮与工具条上的全部操作按钮 / 分屏下拉放大 1/4
 * （tiny 22px/12px → 28px/15px，图标 16px → 20px）。
 * 「list查重」与旁边的按钮保持同等字号/尺寸。
 */
.editor-info-bar :deep(.editor-save-button),
.editor-pane-actions :deep(.n-button),
.editor-pane-actions :deep(.editor-split-select) {
  height: 28px !important;
  font-size: 15px !important;
}
.editor-info-bar :deep(.editor-save-button .n-icon),
.editor-pane-actions :deep(.n-button .n-icon) {
  font-size: 20px !important;
}
.editor-pane-actions :deep(.editor-split-select) {
  width: 128px !important;
}

.pane-body {
  position: relative;
  flex: 1;
  min-width: 0;
  min-height: 0;
  height: auto;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.readonly-hint {
  padding: 6px 12px;
  color: var(--pvf-warning);
  background: var(--pvf-surface-warning);
  font-size: 12px;
  flex-shrink: 0;
}
</style>
