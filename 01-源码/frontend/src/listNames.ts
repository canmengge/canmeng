/**
 * .lst 清单编辑器的「行内名称」装饰：在每行路径后面显示目标文件的中文名。
 *
 * 性能约束（关键）：equipment.lst 有 36 万行，**不能**在打开时全量解析名称。
 * 因此这里只处理**可见区**的行（CodeMirror visibleRanges，通常几十行），
 * 名称按路径缓存并批量向后端请求，滚动到新区域时才补请求；换归档时整体作废。
 */
import {
  Decoration,
  DecorationSet,
  EditorView,
  ViewPlugin,
  WidgetType,
  type ViewUpdate,
} from "@codemirror/view";
import { StateEffect, type Range } from "@codemirror/state";
import { Events } from "@wailsio/runtime";
import { ArchiveService } from "../bindings/pvfine/services";
import { markTrace } from "./diagTrace";

/** 路径 → 显示名（空串表示"已查询但无名称"，区分于"未查询"的 undefined）。 */
const nameCache = new Map<string, string>();
/** 路径 → 目标 fileIndex（-1 表示"已查询但归档中不存在"，区分于"未查询"的 undefined）。 */
const indexCache = new Map<string, number>();
/** 正在请求中的路径，避免重复发起。 */
const pendingPaths = new Set<string>();
const pendingIndexPaths = new Set<string>();
/** 当前挂载的编辑器视图：请求返回后据此触发一次重绘。 */
const liveViews = new Set<EditorView>();
/** 名称缓存更新后，用它触发一次装饰重建。 */
const refreshEffect = StateEffect.define<null>();

// 归档切换后「路径 → 名称 / fileIndex」可能变化，整体作废（模块级只注册一次）。
Events.On("archive:opened", () => {
  nameCache.clear();
  indexCache.clear();
});
Events.On("archive:closed", () => {
  nameCache.clear();
  indexCache.clear();
});

/** 从一行 .lst 文本提取反引号包裹的路径；没有则返回空串。 */
export function extractListPath(text: string): string {
  const match = /`([^`]+)`/.exec(text);
  return match ? match[1].trim() : "";
}

export interface ListPathRange {
  path: string;
  /** 行内偏移（不含反引号），加 line.from 得文档位置。 */
  start: number;
  end: number;
}

/** 行内路径 token 的范围；与 extractListPath 同一提取规则（反引号包裹）。 */
export function extractListPathRange(text: string): ListPathRange | null {
  const match = /`([^`]+)`/.exec(text);
  if (!match) return null;
  const path = match[1].trim();
  if (!path) return null;
  const start = match.index + 1;
  return { path, start, end: start + match[1].length };
}

class ListNameWidget extends WidgetType {
  constructor(readonly name: string) {
    super();
  }
  eq(other: ListNameWidget): boolean {
    return other.name === this.name;
  }
  toDOM(): HTMLElement {
    const span = document.createElement("span");
    span.className = "cm-list-name-tag";
    span.textContent = `[${this.name}]`;
    span.title = this.name;
    return span;
  }
  /** 名称标签纯展示：不拦截鼠标事件，光标/选择不受影响。 */
  ignoreEvent(): boolean {
    return true;
  }
}

/** 批量请求名称（去重 + 跳过已缓存/请求中），返回后刷新所有在用视图。 */
async function requestNames(paths: string[]): Promise<void> {
  const need: string[] = [];
  for (const path of paths) {
    if (!path || nameCache.has(path) || pendingPaths.has(path) || need.includes(path)) continue;
    need.push(path);
  }
  if (need.length === 0) return;
  for (const path of need) pendingPaths.add(path);
  let names: (string | null)[] | null = null;
  try {
    names = await ArchiveService.ResolveFileNames(need);
  } catch {
    names = null; // 归档未打开/已切换：本次留空，缓存"无名称"避免反复请求。
  }
  need.forEach((path, index) => {
    nameCache.set(path, names?.[index] ?? "");
    pendingPaths.delete(path);
  });
  for (const view of liveViews) {
    view.dispatch({ effects: refreshEffect.of(null) });
  }
}

/** 批量解析「路径 → fileIndex」（-1 = 归档中不存在），返回后刷新所有在用视图。 */
async function requestIndexes(paths: string[]): Promise<void> {
  const need: string[] = [];
  for (const path of paths) {
    if (!path || indexCache.has(path) || pendingIndexPaths.has(path) || need.includes(path)) continue;
    need.push(path);
  }
  if (need.length === 0) return;
  for (const path of need) pendingIndexPaths.add(path);
  const resolved = new Map<string, number>();
  try {
    const nodes = (await ArchiveService.ResolveFiles(need)) ?? [];
    for (const node of nodes) {
      if (node) resolved.set(node.path, node.fileIndex);
    }
  } catch {
    // 归档未打开/已切换：不写缓存，下次滚动到该区域再试。
    for (const path of need) pendingIndexPaths.delete(path);
    return;
  }
  need.forEach((path) => {
    indexCache.set(path, resolved.get(path) ?? -1);
    pendingIndexPaths.delete(path);
  });
  for (const view of liveViews) {
    view.dispatch({ effects: refreshEffect.of(null) });
  }
}

/**
 * 查询路径的目标 fileIndex：先查缓存，未命中再单次请求。
 * 供编辑器 click 兜底使用（超大清单的行内链接不走标注链路，见 listNamePlugin）。
 */
export async function resolveListLinkIndex(path: string): Promise<number> {
  const cached = indexCache.get(path);
  if (cached !== undefined) {
    markTrace("路径→索引（命中缓存）", { 路径: path, 索引: cached });
    return cached;
  }
  try {
    const nodes = (await ArchiveService.ResolveFiles([path])) ?? [];
    const index = nodes.find((node) => !!node)?.fileIndex ?? -1;
    indexCache.set(path, index);
    return index;
  } catch {
    return -1;
  }
}

/** 命中行内路径链接时返回该路径（供编辑器 click 判断点击是否落在路径 token 上）。 */
export function listLinkAt(view: EditorView, position: number): string | null {
  const line = view.state.doc.lineAt(position);
  const range = extractListPathRange(line.text);
  if (!range) return null;
  const start = line.from + range.start;
  const end = line.from + range.end;
  if (position < start || position > end) return null;
  return range.path;
}

/**
 * .lst 名称装饰插件：只给可见行的路径行尾挂 widget。
 * 未缓存的路径先收集起来异步请求，返回后经 refreshEffect 重建装饰。
 */
export const listNamePlugin = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet;
    private readonly editorView: EditorView;

    constructor(view: EditorView) {
      this.editorView = view;
      liveViews.add(view);
      this.decorations = Decoration.none;
      this.refresh(view);
    }

    update(update: ViewUpdate): void {
      const refreshRequested = update.transactions.some((tr) =>
        tr.effects.some((effect) => effect.is(refreshEffect)),
      );
      if (update.viewportChanged || update.docChanged || refreshRequested) {
        this.refresh(update.view);
      }
    }

    destroy(): void {
      liveViews.delete(this.editorView);
    }

    private refresh(view: EditorView): void {
      const ranges: Range<Decoration>[] = [];
      const missingNames: string[] = [];
      const missingIndexes: string[] = [];
      for (const visible of view.visibleRanges) {
        let position = visible.from;
        while (position <= visible.to) {
          const line = view.state.doc.lineAt(position);
          const range = extractListPathRange(line.text);
          if (range) {
            // 行内路径链接（惰性）：超大清单的标注会被后端跳过（打开速度优先），
            // 这里对可见行的路径 token 补回「Ctrl+单击跳转并定位 / 悬停提示」。
            const linkStart = line.from + range.start;
            const linkEnd = line.from + range.end;
            if (linkEnd > linkStart) {
              ranges.push(
                Decoration.mark({
                  class: "cm-annotation-link",
                  attributes: {
                    title: `Ctrl+单击：打开文件并在左侧文件树中定位\n${range.path}`,
                  },
                }).range(linkStart, linkEnd),
              );
            }
            if (!indexCache.has(range.path)) missingIndexes.push(range.path);
            const cached = nameCache.get(range.path);
            if (cached === undefined) {
              missingNames.push(range.path);
            } else if (cached) {
              ranges.push(
                Decoration.widget({ widget: new ListNameWidget(cached), side: 1 }).range(line.to),
              );
            }
          }
          if (line.to >= visible.to || line.to >= view.state.doc.length) break;
          position = line.to + 1;
        }
      }
      this.decorations = Decoration.set(ranges, true);
      if (missingNames.length > 0) void requestNames(missingNames);
      if (missingIndexes.length > 0) void requestIndexes(missingIndexes);
    }
  },
  { decorations: (value) => value.decorations },
);
