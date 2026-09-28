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

/** 路径 → 显示名（空串表示"已查询但无名称"，区分于"未查询"的 undefined）。 */
const nameCache = new Map<string, string>();
/** 正在请求中的路径，避免重复发起。 */
const pendingPaths = new Set<string>();
/** 当前挂载的编辑器视图：请求返回后据此触发一次重绘。 */
const liveViews = new Set<EditorView>();
/** 名称缓存更新后，用它触发一次装饰重建。 */
const refreshEffect = StateEffect.define<null>();

// 归档切换后「路径 → 名称」可能变化，整体作废（模块级只注册一次）。
Events.On("archive:opened", () => nameCache.clear());
Events.On("archive:closed", () => nameCache.clear());

/** 从一行 .lst 文本提取反引号包裹的路径；没有则返回空串。 */
export function extractListPath(text: string): string {
  const match = /`([^`]+)`/.exec(text);
  return match ? match[1].trim() : "";
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
      const missing: string[] = [];
      for (const visible of view.visibleRanges) {
        let position = visible.from;
        while (position <= visible.to) {
          const line = view.state.doc.lineAt(position);
          const path = extractListPath(line.text);
          if (path) {
            const cached = nameCache.get(path);
            if (cached === undefined) {
              missing.push(path);
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
      if (missing.length > 0) void requestNames(missing);
    }
  },
  { decorations: (value) => value.decorations },
);
