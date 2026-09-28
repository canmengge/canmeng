import type { SearchItem, TreeItem } from "./stores/explorer";

export interface SearchTreeOptions {
  /**
   * 是否在文件行显示清单 ID 标签。搜索视窗只要 `[名称]`（用户 2026-09-24 要求），
   * 左侧搜索结果列表保持默认（ID + 名称）。
   */
  showIdTag?: boolean;
}

/**
 * 把扁平的搜索命中整理成与左侧资源管理器**同构**的树：目录按路径自动补齐，
 * 文件挂在对应目录节点下。
 *
 * 从 `Explorer.vue` 提取出来，供「搜索视窗」与左侧文件列表共用，
 * 以保证两种视图的显示格式完全一致。
 */
export function buildSearchTree(items: SearchItem[], options: SearchTreeOptions = {}): TreeItem[] {
  const showIdTag = options.showIdTag !== false;
  const roots: TreeItem[] = [];
  const nodesByPath = new Map<string, TreeItem>();

  for (const item of items) {
    const parts = item.path
      .replaceAll("\\", "/")
      .split("/")
      .filter(Boolean);
    if (parts.length === 0) continue;

    let children = roots;
    let parentPath = "";
    for (const part of parts.slice(0, -1)) {
      const path = parentPath ? `${parentPath}/${part}` : part;
      let directory = nodesByPath.get(path);
      if (!directory) {
        directory = {
          key: path,
          label: part,
          isDir: true,
          isLeaf: false,
          children: [],
          fileIndex: -1,
          size: 0,
          dataType: 0,
          childCount: 0,
          changeKind: "",
          tags: [],
          annotations: item.pathAnnotations[path] ?? [],
          icon: null,
          fieldImage: null,
        };
        nodesByPath.set(path, directory);
        children.push(directory);
      } else if (directory.annotations.length === 0) {
        // 同一目录可能由多个条目创建：谁先创建谁定标注，先到者没有标注时用后来者补齐
        // （搜索命中带完整祖先标注链；手动收进来的条目单独解析）。
        const inherited = item.pathAnnotations[path];
        if (inherited && inherited.length > 0) directory.annotations = inherited;
      }
      children = directory.children ?? [];
      parentPath = path;
    }

    const filePath = parts.join("/");
    let file = nodesByPath.get(filePath);
    if (!file) {
      file = {
        key: filePath,
        label: parts[parts.length - 1],
        isDir: false,
        isLeaf: true,
        children: [],
        fileIndex: item.fileIndex,
        size: item.size,
        dataType: item.dataType,
        childCount: 0,
        changeKind: item.changeKind,
        tags: [],
        annotations: item.annotations,
        icon: item.icon,
        fieldImage: item.fieldImage,
      };
      nodesByPath.set(filePath, file);
      children.push(file);
    }

    if (file.annotations.length === 0 && item.annotations.length > 0) {
      file.annotations = item.annotations;
    }
    if (!file.icon && item.icon) file.icon = item.icon;
    if (!file.fieldImage && item.fieldImage) file.fieldImage = item.fieldImage;

    if (item.category !== "file") {
      file.tags.push({
        // 名称标签取清单登记名（`item.name`），不是文件名——否则文件行会显示成
        // `117530002.equ [117530002.equ]` 这种重复内容。
        id: showIdTag ? item.id : "",
        name: item.name,
        category: item.category,
      });
    }
  }

  sortTree(roots);
  return roots;
}

/** 目录在前、同类按名称排序（与资源管理器一致）。 */
function sortTree(items: TreeItem[]): void {
  items.sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    return a.label < b.label ? -1 : a.label > b.label ? 1 : 0;
  });
  for (const item of items) {
    if (item.children) sortTree(item.children);
  }
}
