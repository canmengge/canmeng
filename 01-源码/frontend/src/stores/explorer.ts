import { defineStore } from "pinia";
import { computed, nextTick, ref } from "vue";
import { Events } from "@wailsio/runtime";
import { ArchiveService } from "../../bindings/pvfine/services";
import type {
  SearchHit,
  TreeAnnotation,
  TreeNode,
  TreeTag,
  ImageReference,
} from "../../bindings/pvfine/services/models";
import { useArchiveStore } from "./archive";
import { useSidebarStore } from "./sidebar";

export interface TreeItem {
  key: string; // 归档内路径
  label: string; // 显示名(目录/文件最后一段)
  isDir: boolean;
  isLeaf: boolean;
  children: TreeItem[] | null; // null = 未加载
  fileIndex: number; // 目录为 -1
  size: number;
  dataType: number;
  childCount: number;
  changeKind: string;
  tags: TreeTag[];
  annotations: TreeAnnotation[];
  icon: ImageReference | null;
  fieldImage: ImageReference | null;
}

export interface SearchItem {
  key: string;
  label: string;
  path: string;
  id: string;
  /** 清单/脚本里登记的中文名（文件行 `[名称]` 标签用它；无登记时为空串）。 */
  name: string;
  category: string;
  fileIndex: number;
  size: number;
  dataType: number;
  changeKind: string;
  annotations: TreeAnnotation[];
  pathAnnotations: Record<string, TreeAnnotation[]>;
  icon: ImageReference | null;
  fieldImage: ImageReference | null;
}

export interface RevealRequest {
  path: string;
  nonce: number;
}

/** 资源管理器状态:懒加载树 + 搜索 */
export const useExplorerStore = defineStore("explorer", () => {
  const archive = useArchiveStore();
  const roots = ref<TreeItem[]>([]);
  const expanded = ref<Set<string>>(new Set([""]));
  // 多选：selectedKeys 是完整选择集；selectedKey 是"主选中项"（最后被普通点击的项），
  // 保留它让既有调用点（打开/关闭标签、定位、清理）继续按单选语义工作。
  const selectedKeys = ref<string[]>([]);
  const selectedKey = computed<string | null>({
    get: () =>
      selectedKeys.value.length > 0 ? selectedKeys.value[selectedKeys.value.length - 1] : null,
    set: (value: string | null) => {
      selectedKeys.value = value ? [value] : [];
    },
  });
  // 仅由显式的“定位”操作写入；文件树的滚动定位只响应这个请求，选中态本身不触发滚动。
  const revealRequest = ref<RevealRequest | null>(null);
  let revealNonce = 0;
  const itemsByKey = new Map<string, TreeItem>();

  // 搜索状态
  const query = ref("");
  const hits = ref<SearchItem[]>([]);
  const nextCursor = ref(-1);
  const searching = ref(false);
  const mode = ref<"tree" | "search">("tree");
  const exactMatch = ref(false);
  const revision = ref(0);
  let searchRequest = 0;
  let refreshTimer: number | undefined;
  let treeRefreshRequest = 0;

  function toTreeItem(n: TreeNode): TreeItem {
    return {
      key: n.path,
      label: n.name,
      isDir: n.isDir,
      isLeaf: !n.isDir,
      children: n.isDir ? null : undefined!,
      fileIndex: n.fileIndex,
      size: n.size,
      dataType: n.dataType,
      childCount: n.childCount,
      changeKind: n.changeKind ?? "",
      tags: (n.tags ?? []).filter((tag): tag is TreeTag => !!tag),
      annotations: cleanAnnotations(n.annotations),
      icon: n.icon ?? null,
      fieldImage: n.fieldImage ?? null,
    };
  }

  function registerItems(items: TreeItem[]) {
    for (const item of items) itemsByKey.set(item.key, item);
  }

  function toSearchItem(n: SearchHit): SearchItem {
    const fallback = n.path.slice(n.path.lastIndexOf("/") + 1);
    return {
      key: `${n.fileIndex}:${n.category}:${n.id}:${n.path}`,
      label: n.name || fallback,
      path: n.path,
      id: n.id,
      name: n.category === "file" ? "" : n.name,
      category: n.category,
      fileIndex: n.fileIndex,
      size: n.size,
      dataType: n.dataType,
      changeKind: n.changeKind ?? "",
      annotations: cleanAnnotations(n.annotations),
      pathAnnotations: Object.fromEntries(
        Object.entries(n.pathAnnotations ?? {}).map(([path, annotations]) => [
          path,
          cleanAnnotations(annotations),
        ])
      ),
      icon: n.icon ?? null,
      fieldImage: n.fieldImage ?? null,
    };
  }

  /** 加载根节点(归档打开后调用) */
  async function loadRoots() {
    const nodes = (await ArchiveService.ListChildren("")) ?? [];
    const nextRoots = nodes.filter((n): n is TreeNode => !!n).map(toTreeItem);
    roots.value = nextRoots;
    itemsByKey.clear();
    registerItems(roots.value);
    revision.value++;
  }

  /** 重新加载资源树；搜索模式会等待新的语义索引完成后自动恢复。 */
  async function reload(): Promise<void> {
    const restoreSearch = mode.value === "search" && query.value.trim() !== "";
    const currentQuery = query.value;
    selectedKey.value = null;
    revealRequest.value = null;
    await loadRoots();
    if (!restoreSearch) {
      clearSearch();
      return;
    }

    const request = ++searchRequest;
    hits.value = [];
    nextCursor.value = -1;
    mode.value = "search";
    searching.value = true;
    try {
      const status = await ArchiveService.IndexStatus();
      if (request !== searchRequest || !archive.open) return;
      if (status?.state === "ready") {
        await search(currentQuery);
      }
    } finally {
      if (request === searchRequest && mode.value === "search" && hits.value.length === 0) {
        const status = await ArchiveService.IndexStatus().catch(() => null);
        if (status?.state !== "building") searching.value = false;
      }
    }
  }

  /** n-tree onLoad:展开目录时加载其子节点 */
  async function loadChildren(node: TreeItem): Promise<void> {
    if (!node.isDir || node.children) return;
    const nodes = (await ArchiveService.ListChildren(node.key)) ?? [];
    const children = nodes.filter((n): n is TreeNode => !!n).map(toTreeItem);
    node.children = children;
    registerItems(node.children);
  }

  /**
   * 加载目标条目的父目录，并将其设为资源树当前选中项并滚动定位（文件与文件夹都支持）。
   *
   * 性能（2026-09-27 修复）：路径已知 ⇒ 各层祖先**一次性并行**加载（过去是逐级
   * `await loadChildren`，深度 d 就要 d+1 次串行往返），查找走 `itemsByKey` 映射
   * （过去是每层线性 `items.find`）。这正是「高级搜索定位快、清单里 Ctrl+左键定位慢」
   * 的根因：搜索结果的目标多半已落在展开过的目录里被短路，而清单路径要一层层现拉。
   */
  async function revealPath(path: string): Promise<boolean> {
    if (!archive.open) return false;
    const parts = normalizePath(path).split("/").filter(Boolean);
    if (parts.length === 0) return false;

    if (roots.value.length === 0) await loadRoots();
    const keys = parts.map((_part, index) => parts.slice(0, index + 1).join("/"));
    // 只请求尚未加载的祖先层；已展开过的目录直接命中，不再发 IPC。
    const pending = keys.slice(0, -1).filter((key) => {
      const node = itemsByKey.get(key);
      if (!node) return true;
      return node.isDir && !node.children;
    });
    await Promise.all(
      pending.map(async (key) => {
        const nodes = (await ArchiveService.ListChildren(key)) ?? [];
        const children = nodes.filter((n): n is TreeNode => !!n).map(toTreeItem);
        const parent = itemsByKey.get(key);
        if (parent?.isDir) parent.children = children;
        registerItems(children);
      })
    );

    const node = itemsByKey.get(keys[keys.length - 1]);
    if (!node) return false;
    // 祖先目录是"原地改 children"，roots 引用没变 ⇒ FileTree 的 treeData / itemsByKey
    // 两个 computed 不会重算，展开键算不出来、naive 树也拿不到新节点 → 定位失效。
    // 这里浅拷贝一次 roots 强制刷新（只在 reveal 时发生一次，代价 O(已加载节点)）。
    roots.value = [...roots.value];
    await nextTick();
    selectedKey.value = node.key;
    revealRequest.value = { path: node.key, nonce: ++revealNonce };
    return true;
  }

  /** 仅选中节点，不展开、不滚动。 */
  function selectPath(path: string | null): void {
    selectedKey.value = path;
  }

  /** 取消当前选中。 */
  function clearSelection(): void {
    selectedKey.value = null;
  }

  /** 多选：直接设置整个选择集（Shift 连选 / Ctrl 点选由文件树算好后回调）。 */
  function setSelection(keys: string[]): void {
    const unique: string[] = [];
    const seen = new Set<string>();
    for (const key of keys) {
      if (!key || seen.has(key)) continue;
      seen.add(key);
      unique.push(key);
    }
    selectedKeys.value = unique;
  }

  /** 文件树消费完一次定位请求后回调，避免重复滚动。 */
  function consumeRevealRequest(): void {
    revealRequest.value = null;
  }

  function getItem(path: string): TreeItem | undefined {
    return itemsByKey.get(path);
  }

  function reset() {
    searchRequest++;
    treeRefreshRequest++;
    window.clearTimeout(refreshTimer);
    roots.value = [];
    itemsByKey.clear();
    selectedKey.value = null;
    revealRequest.value = null;
    expanded.value = new Set([""]);
    clearSearch();
    mode.value = "tree";
  }

  /** 刷新已加载节点的路径标注和可用的索引标签。 */
  async function refreshTreeTags() {
    if (!archive.open) return;
    const request = ++treeRefreshRequest;
    const loadedDirectories = Array.from(itemsByKey.values())
      .filter((item) => item.isDir && item.children !== null)
      .map((item) => item.key);
    const paths = ["", ...loadedDirectories];
    const nodeLists = await Promise.all(
      paths.map(async (path) => (await ArchiveService.ListChildren(path)) ?? [])
    );
    if (request !== treeRefreshRequest || !archive.open) return;

    const tagsByFile = new Map<number, TreeTag[]>();
    const annotationsByPath = new Map<string, TreeAnnotation[]>();
    const changeKindsByPath = new Map<string, string>();
    const nodesByPath = new Map<string, TreeNode>();
    for (const nodes of nodeLists) {
      for (const node of nodes) {
        if (!node) continue;
        nodesByPath.set(node.path, node);
        annotationsByPath.set(node.path, cleanAnnotations(node.annotations));
        if (!node.isDir) changeKindsByPath.set(node.path, node.changeKind ?? "");
        if (archive.indexReady && !node.isDir && node.fileIndex >= 0) {
          tagsByFile.set(
            node.fileIndex,
            (node.tags ?? []).filter((tag): tag is TreeTag => !!tag)
          );
        }
      }
    }
    for (const item of itemsByKey.values()) {
      item.annotations = annotationsByPath.get(item.key) ?? item.annotations;
      if (!item.isDir) {
        item.changeKind = changeKindsByPath.get(item.key) ?? item.changeKind;
        const tags = tagsByFile.get(item.fileIndex);
        if (tags) item.tags = tags;
        const node = nodesByPath.get(item.key);
        if (node) {
          item.icon = node.icon ?? null;
          item.fieldImage = node.fieldImage ?? null;
        }
      }
    }
  }

  async function refreshAnnotations() {
    await refreshTreeTags();
    if (mode.value === "search" && query.value.trim() && archive.indexReady) {
      await search(query.value);
    }
  }

  /** 执行新搜索(从第一页开始) */
  async function search(q: string) {
    const request = ++searchRequest;
    query.value = q;
    hits.value = [];
    nextCursor.value = -1;
    if (!q.trim()) {
      searching.value = false;
      mode.value = "tree";
      return;
    }
    mode.value = "search";
    if (!archive.indexReady) return;
    await loadMore(request);
    // 搜索结果默认显示在右侧「搜索视窗」（用户 2026-09-24 要求）。
    if (request === searchRequest && query.value.trim()) {
      useSidebarStore().show("search");
    }
  }

  /** 切换路径、名称和 id 的精确匹配模式。 */
  async function setExactMatch(value: boolean): Promise<void> {
    if (exactMatch.value === value) return;
    exactMatch.value = value;
    if (query.value.trim()) await search(query.value);
  }

  /** 加载下一页搜索结果 */
  async function loadMore(request = searchRequest) {
    if (!archive.indexReady || request !== searchRequest) return;
    if (nextCursor.value === -1 && hits.value.length > 0) return;
    searching.value = true;
    try {
      const res = exactMatch.value
        ? await ArchiveService.SearchExact(query.value, Math.max(nextCursor.value, 0), 200)
        : await ArchiveService.Search(query.value, Math.max(nextCursor.value, 0), 200);
      if (request !== searchRequest) return;
      const page = (res?.hits ?? [])
        .filter((n): n is SearchHit => !!n)
        .map(toSearchItem);
      hits.value.push(...page);
      nextCursor.value = res?.nextCursor ?? -1;
    } finally {
      if (request === searchRequest) searching.value = false;
    }
  }

  function clearSearch() {
    searchRequest++;
    window.clearTimeout(refreshTimer);
    query.value = "";
    hits.value = [];
    nextCursor.value = -1;
    mode.value = "tree";
  }

  Events.On("archive:index-updated", () => {
    void refreshTreeTags();
    if (mode.value !== "search" || !query.value.trim() || !archive.indexReady) return;
    window.clearTimeout(refreshTimer);
    refreshTimer = window.setTimeout(() => void search(query.value), 0);
  });
  // 结构变更（脚本窗口或批处理在别处增删条目）后目录树必须重载；事件是
  // 广播的，因此独立脚本窗口的变更也能反映到这个窗口。
  Events.On("archive:batch-applied", (event: any) => {
    const data = event?.data ?? event;
    if (data?.structural) void reload();
  });
  Events.On("archive:index-ready", () => {
    void refreshTreeTags();
    if (mode.value === "search" && query.value.trim()) {
      void search(query.value);
    }
  });
  Events.On("archive:reloaded", () => {
    reset();
    void loadRoots();
  });

  return {
    roots,
    expanded,
    selectedKey,
    selectedKeys,
    setSelection,
    revealRequest,
    query,
    hits,
    nextCursor,
    searching,
    exactMatch,
    mode,
    revision,
    toTreeItem,
    toSearchItem,
    getItem,
    loadRoots,
    reload,
    loadChildren,
    revealPath,
    selectPath,
    clearSelection,
    consumeRevealRequest,
    refreshTreeTags,
    refreshAnnotations,
    reset,
    search,
    setExactMatch,
    loadMore,
    clearSearch,
  };
});

function cleanAnnotations(
  annotations: (TreeAnnotation | null)[] | null | undefined
): TreeAnnotation[] {
  return (annotations ?? []).filter(
    (annotation): annotation is TreeAnnotation => !!annotation
  );
}

function normalizePath(path: string): string {
  return path.replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
}
