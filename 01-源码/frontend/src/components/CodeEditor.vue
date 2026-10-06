<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import {
  EditorView,
  keymap,
  lineNumbers,
  highlightActiveLine,
  highlightActiveLineGutter,
  drawSelection,
  highlightWhitespace,
  rectangularSelection,
  crosshairCursor,
  Decoration,
  GutterMarker,
  gutter,
  hoverTooltip,
  tooltips,
  WidgetType,
  type DecorationSet,
} from "@codemirror/view";
import {
  EditorState,
  Compartment,
  StateEffect,
  StateField,
  type Range,
} from "@codemirror/state";
import {
  HighlightStyle,
  codeFolding,
  foldGutter,
  foldKeymap,
  foldService,
  indentUnit,
  syntaxHighlighting,
} from "@codemirror/language";
import {
  defaultKeymap,
  history,
  historyKeymap,
  indentLess,
  insertTab,
} from "@codemirror/commands";
import { highlightSelectionMatches, search, searchKeymap } from "@codemirror/search";
import { autocompletion, type Completion, type CompletionContext, type CompletionResult } from "@codemirror/autocomplete";
import { javascript } from "@codemirror/lang-javascript";
import { tags } from "@lezer/highlight";
import { vim } from "@replit/codemirror-vim";
import { NTooltip } from "naive-ui";
import { pvfHighlighting, pvfLanguage } from "../pvfLanguage";
import { luaHighlighting, luaLanguage } from "../luaLanguage";
import type { EditorAnnotation } from "../../bindings/pvfine/services/models";
import type { AnnotationTagPlacement } from "../stores/settings";
import { useImageStore } from "../stores/images";
import { scriptCompletionSource as declarationCompletionSource } from "../scriptLanguageService";
import type { ResolvedThemeId } from "../theme";
import { listLinkAt, listNamePlugin, resolveListLinkIndex } from "../listNames";
import { searchPanelPhrases, searchPanelTheme } from "../searchPanel";
import {
  CompletionCatalog,
  type FormViewCompletionColumn,
  type FormViewCompletionSection,
} from "../services/formViewApi";

const props = defineProps<{
  doc: string;
  /**
   * 编辑器语言模式：默认 `pvf`（PVF token 格式，所有普通文件）；
   * `javascript` 供脚本工作台；`lua` **仅** `.lua` 文件（由 EditorPane 严格按扩展名判定）。
   */
  language?: "pvf" | "javascript" | "lua";
  readOnly?: boolean;
  /** 大文件（>8MB）：不装空白高亮，避免几十万行把渲染拖死（折行保留）。 */
  largeFile?: boolean;
  annotations?: EditorAnnotation[];
  tagPlacement?: AnnotationTagPlacement;
  /** 「绿色关联框」（ID 关联标签）是否显示：Alt+Q 切换，默认显示。 */
  showReferenceTags?: boolean;
  vimMode?: boolean;
  themeId: ResolvedThemeId;
  /** 搜索结果定位请求(seq 变化即触发一次定位)，needles 依次尝试直至命中。 */
  reveal?: { seq: number; needles: string[]; line?: number } | null;
  /** .lst 清单文件：在可见行路径后显示目标文件名称（惰性，见 listNames.ts）。 */
  listNames?: boolean;
  /**
   * A7 行内错误标记：`{ line, message }`（**1 基**行号）。数据来自「清单查重」结果；
   * 只作用于普通文件通道，且**文档一改就自动清空**（行号会失效，重新查重再出现）。
   */
  problems?: { line: number; message: string }[] | null;
  /**
   * 本标签当前是否可见。标签切换走 v-show（NTabPane 的 `show:lazy`），隐藏时
   * display:none 会把编辑器滚动位置归零 —— 靠这个信号在重新可见时把位置写回去。
   */
  active?: boolean;
}>();

const emit = defineEmits<{
  (e: "change", text: string): void;
  (e: "open-reference", fileIndex: number): void;
  /** 单击可跳转注释：打开文件并在左侧文件树中定位。 */
  (e: "activate-reference", fileIndex: number): void;
  (e: "edit-placeholder", request: PlaceholderEditRequest): void;
}>();

/** 一次「修改/创建占位符译文」请求:点击标签后由父组件弹框处理。 */
export interface PlaceholderEditRequest {
  tableIndex: number;
  key: string;
  value: string;
  fallback: boolean;
  /** 该键在字符串表里还不存在，需要新建。 */
  missing: boolean;
}

const host = ref<HTMLDivElement | null>(null);
let view: EditorView | null = null;
const readOnlyComp = new Compartment();
const vimComp = new Compartment();
const editorThemeComp = new Compartment();
const images = useImageStore();

interface AnnotationDisplay {
  annotations: EditorAnnotation[];
  placement: AnnotationTagPlacement;
  showReferenceTags: boolean;
}

const setAnnotations = StateEffect.define<AnnotationDisplay>();
const setDiagnosticLine = StateEffect.define<number | null>();

const javascriptHighlighting = syntaxHighlighting(
  HighlightStyle.define([
    { tag: tags.comment, color: "var(--pvf-text-faint)", fontStyle: "italic" },
    { tag: [tags.string, tags.regexp], color: "var(--pvf-editor-syntax-string)" },
    { tag: [tags.number, tags.bool, tags.atom], color: "var(--pvf-editor-syntax-number)" },
    {
      tag: [tags.keyword, tags.controlKeyword],
      color: "var(--pvf-editor-syntax-heading)",
      fontWeight: "600",
    },
    { tag: tags.operator, color: "var(--pvf-text-secondary)" },
    { tag: tags.variableName, color: "var(--pvf-text-code)" },
    { tag: tags.definition(tags.variableName), color: "var(--pvf-editor-syntax-heading)" },
    { tag: tags.function(tags.variableName), color: "var(--pvf-editor-syntax-heading)" },
    { tag: tags.propertyName, color: "var(--pvf-editor-syntax-string)" },
    { tag: [tags.typeName, tags.className], color: "var(--pvf-editor-syntax-heading)" },
    { tag: [tags.punctuation, tags.bracket], color: "var(--pvf-text-muted)" },
    { tag: tags.invalid, color: "var(--pvf-error)" },
  ]),
);

class AnnotationWidget extends WidgetType {
  constructor(
    readonly annotation: EditorAnnotation,
    readonly openReference: (fileIndex: number) => void,
    readonly showTooltip: (annotation: EditorAnnotation, element: HTMLElement) => void,
    readonly hideTooltip: () => void,
    readonly editPlaceholder: (request: PlaceholderEditRequest) => void
  ) {
    super();
  }

  eq(other: AnnotationWidget): boolean {
    return (
      other.annotation.title === this.annotation.title &&
      other.annotation.content === this.annotation.content &&
      other.annotation.type === this.annotation.type &&
      other.annotation.targetFileIndex === this.annotation.targetFileIndex &&
      other.annotation.image?.path === this.annotation.image?.path &&
      other.annotation.image?.index === this.annotation.image?.index &&
      other.annotation.inlineImage === this.annotation.inlineImage
    );
  }

  toDOM(): HTMLElement {
    const tag = document.createElement("span");
    const imageReference = this.annotation.image;
    const inlineImage = !!(imageReference && this.annotation.inlineImage);
    const placeholder = this.annotation.placeholder;
    tag.className = inlineImage
      ? "cm-annotation-inline-image"
      : `cm-annotation-tag cm-annotation-tag--${this.annotation.type || "text"}`;
    if (!inlineImage) {
      // 译文/名称末尾常带空白（字符串表里常见普通空格、以及不换行空格 U+00A0 / 全角空格
      // U+3000 —— 后两者不会被 nowrap 折叠，会把胶囊框右侧撑出一段空洞）。显示时去掉
      // 首尾空白，让框紧贴文字；提示文本仍用原始 title/不动的 content。
      tag.textContent = this.annotation.title.replace(/^[\s\u00a0\u3000]+|[\s\u00a0\u3000]+$/gu, "");
      if (tag.textContent === "") tag.textContent = this.annotation.title;
    }
    const hints = [
      this.annotation.targetFileIndex >= 0 ? "Cmd/Ctrl+单击打开来源字符串表" : "",
      placeholder ? "单击修改译文" : "",
    ].filter(Boolean);
    const tooltip = hintText(this.annotation, hints);
    tag.setAttribute("aria-label", tooltip || this.annotation.title);
    tag.contentEditable = "false";
    if (imageReference && inlineImage) {
      const imageSlot = document.createElement("span");
      imageSlot.className = "cm-annotation-inline-image-slot";
      imageSlot.setAttribute("aria-hidden", "true");
      tag.prepend(imageSlot);
      const entry: InlineImageSlot = {
        reference: imageReference,
        slot: imageSlot,
        request: 0,
        pending: false,
        loaded: false,
        loadedGeneration: -1,
      };
      inlineImageSlots.set(imageSlot, entry);
      queueMicrotask(() => loadInlineImage(entry));
    }
    if (tooltip || this.annotation.image) {
      tag.addEventListener("mouseenter", () => this.showTooltip(this.annotation, tag));
      tag.addEventListener("mouseleave", this.hideTooltip);
    }
    if (placeholder) {
      tag.classList.add("cm-annotation-tag--editable");
      tag.setAttribute("role", "button");
      tag.setAttribute("tabindex", "0");
      tag.addEventListener("click", (event) => {
        if (event.metaKey || event.ctrlKey) return;
        event.preventDefault();
        event.stopPropagation();
        this.editPlaceholder({
          tableIndex: placeholder.tableIndex,
          key: placeholder.key,
          value: this.annotation.title,
          fallback: !!placeholder.fallback,
          missing: !!placeholder.missing,
        });
      });
    }
    if (this.annotation.targetFileIndex >= 0) {
      tag.classList.add("cm-annotation-tag--link");
      tag.setAttribute("role", "button");
      tag.addEventListener("click", (event) => {
        if (!event.metaKey && !event.ctrlKey) return;
        event.preventDefault();
        event.stopPropagation();
        this.openReference(this.annotation.targetFileIndex);
      });
    }
    return tag;
  }

  destroy(dom: HTMLElement): void {
    const imageSlot = dom.querySelector<HTMLElement>(".cm-annotation-inline-image-slot");
    if (imageSlot) inlineImageSlots.delete(imageSlot);
  }

  ignoreEvent(): boolean {
    return true;
  }
}

interface InlineImageSlot {
  reference: NonNullable<EditorAnnotation["image"]>;
  slot: HTMLElement;
  request: number;
  pending: boolean;
  loaded: boolean;
  loadedGeneration: number;
}

/** 标注标签的 tooltip:标注内容 + 可用操作提示。 */
function hintText(annotation: EditorAnnotation, hints: string[]): string {
  return [annotation.content || annotation.title, ...hints].filter(Boolean).join("\n\n");
}

const inlineImageSlots = new Map<HTMLElement, InlineImageSlot>();

function loadInlineImage(entry: InlineImageSlot): void {
  if (!entry.slot.isConnected || entry.pending) return;
  const generation = images.status.generation;
  if (entry.loaded && entry.loadedGeneration === generation) return;

  const request = ++entry.request;
  const revision = images.revision;
  entry.pending = true;
  entry.loaded = false;
  entry.slot.replaceChildren();
  void images.loadImage(entry.reference).then((data) => {
    if (request !== entry.request || !entry.slot.isConnected || images.status.generation !== generation) return;
    if (!data?.dataUrl) return;
    const image = document.createElement("img");
    image.src = data.dataUrl;
    image.alt = "";
    image.width = 16;
    image.height = 16;
    entry.slot.replaceChildren(image);
    entry.loaded = true;
    entry.loadedGeneration = generation;
  }).finally(() => {
    if (request !== entry.request) return;
    entry.pending = false;
    // 如果请求在索引切换/完成前返回空结果，补一次请求，避免正文图片
    // 因为首次加载早于索引完成而永久缺失。
    if (!entry.loaded && entry.slot.isConnected && images.revision !== revision) {
      loadInlineImage(entry);
    }
  });
}

type AnnotationTooltipPlacement = "bottom-start" | "top-start";

const annotationTooltip = ref<{
  visible: boolean;
  left: number;
  top: number;
  placement: AnnotationTooltipPlacement;
  content: string;
  dataUrl: string;
  loading: boolean;
}>({
  visible: false,
  left: 0,
  top: 0,
  placement: "bottom-start",
  content: "",
  dataUrl: "",
  loading: false,
});
let annotationTooltipRequest = 0;
let activeTooltipImageReference: EditorAnnotation["image"] = null;
let tooltipHideTimer: number | undefined;

function clearTooltipHideTimer(): void {
  if (tooltipHideTimer === undefined) return;
  window.clearTimeout(tooltipHideTimer);
  tooltipHideTimer = undefined;
}

function showAnnotationTooltip(annotation: EditorAnnotation, element: HTMLElement): void {
  clearTooltipHideTimer();
  const rect = element.getBoundingClientRect();
  const width = 320;
  const left = Math.min(Math.max(8, rect.left), Math.max(8, window.innerWidth - width - 8));
  const placement: AnnotationTooltipPlacement = rect.bottom + 260 < window.innerHeight
    ? "bottom-start"
    : "top-start";
  const top = placement === "bottom-start" ? rect.bottom : rect.top;
  const request = ++annotationTooltipRequest;
  activeTooltipImageReference = annotation.image;
  annotationTooltip.value = {
    visible: true,
    left,
    top,
    placement,
    content: [
      annotation.content || (annotation.image
        ? `${annotation.image.path}[${annotation.image.index}]`
        : annotation.title),
      annotation.targetFileIndex >= 0 ? "Cmd/Ctrl+单击可以跳转" : "",
      annotation.type === "reference" ? "Alt+Q：隐藏 / 显示绿色关联框" : "",
    ].filter(Boolean).join("\n\n"),
    dataUrl: "",
    loading: !!annotation.image,
  };

  if (!annotation.image) return;
  void images.loadImage(annotation.image)
    .then((data) => {
      if (request !== annotationTooltipRequest) return;
      annotationTooltip.value.dataUrl = data?.dataUrl ?? "";
      annotationTooltip.value.loading = false;
    })
    .catch(() => {
      if (request !== annotationTooltipRequest) return;
      annotationTooltip.value.loading = false;
    });
}

function hideAnnotationTooltip(): void {
  clearTooltipHideTimer();
  annotationTooltipRequest++;
  activeTooltipImageReference = null;
  annotationTooltip.value.visible = false;
}

function scheduleHideTooltip(): void {
  clearTooltipHideTimer();
  tooltipHideTimer = window.setTimeout(() => {
    tooltipHideTimer = undefined;
    hideAnnotationTooltip();
  }, 180);
}

function cancelTooltipHide(): void {
  clearTooltipHideTimer();
}

watch(
  () => images.revision,
  () => {
    for (const [slot, entry] of inlineImageSlots) {
      if (!slot.isConnected) {
        inlineImageSlots.delete(slot);
        continue;
      }
      loadInlineImage(entry);
    }

    const reference = activeTooltipImageReference;
    if (!reference || !annotationTooltip.value.visible) return;
    const request = ++annotationTooltipRequest;
    annotationTooltip.value.dataUrl = "";
    annotationTooltip.value.loading = true;
    void images.loadImage(reference).then((data) => {
      if (request !== annotationTooltipRequest) return;
      annotationTooltip.value.dataUrl = data?.dataUrl ?? "";
      annotationTooltip.value.loading = false;
    }).catch(() => {
      if (request !== annotationTooltipRequest) return;
      annotationTooltip.value.loading = false;
    });
  }
);

/**
 * 外部登记表链接值后面的说明标签（如 [part set index] 的编号 → 「↗ CTRL+左键可跳转到对应文件」）。
 * 纯展示：WidgetType 默认 ignoreEvent 为 true —— 不接收事件、不参与文档内容，保存时不会被写进 PVF。
 */
class ExternalLinkHintWidget extends WidgetType {
  toDOM(): HTMLElement {
    const hint = document.createElement("span");
    hint.className = "cm-external-hint";
    // 文案保持通用：同一套机制既服务 [part set index]（套装），也服务 [appendage]（扩展状态）。
    hint.textContent = "↗ CTRL+左键可跳转到对应文件";
    return hint;
  }
}

/** 无状态，复用一个实例（引用相等即视为同一个 widget，避免每次重排都重建）。 */
const externalLinkHintWidget = new ExternalLinkHintWidget();

/** 单个文件参与装饰的标注上限；超出不再渲染（与后端 annotationCountLimit 同向兜底）。 */
const annotationRenderLimit = 20000;

function annotationDecorations(
  state: EditorState,
  display: AnnotationDisplay
): DecorationSet {
  // 渲染上限：每条标注都要一次 sliceString / lineAt 并 new 一个 Widget，
  // 几十万条会冻结界面。超出部分直接不装饰（后端已有同向的数量上限，此处兜底）。
  const source =
    display.annotations.length > annotationRenderLimit
      ? display.annotations.slice(0, annotationRenderLimit)
      : display.annotations;
  const ranges = source.flatMap((annotation) => {
    const targetStart = Math.max(0, Math.min(state.doc.length, annotation.start));
    const targetEnd = Math.max(targetStart, Math.min(state.doc.length, annotation.end));
    const result: Range<Decoration>[] = [];

    // 外部登记表链接（后端 type=link 且 Content 是目标归档路径，如 [part set index] 的编号）：
    // 悬停显示套装名 + 目标路径，值后面再挂一个说明标签（见 ExternalLinkHintWidget）。
    // 普通 .lst 路径链接不受影响（仍是虚线下划线 + 原提示）。
    const externalTarget =
      annotation.type === "link" ? (annotation.content ?? "").trim() : "";
    const isExternalLink = externalTarget !== "";

    if (annotation.targetFileIndex >= 0 && targetStart < targetEnd && annotation.type !== "reference") {
      // .lst 路径链接 / 外部登记表链接：悬停用原生 title 给出操作提示
      // （仅 Ctrl+单击才跳转，见 click 处理）。
      // ID 关联（type=reference）不在此列：关联目标只由后面的绿色标签承载，
      // 原文 token 保持普通可编辑文本（2026-09-27 用户要求）。
      const linkText = state.doc.sliceString(targetStart, targetEnd);
      const setName = (annotation.title ?? "").trim();
      const hint = isExternalLink
        ? [
            setName ? `套装：${setName}` : "",
            `目标：${externalTarget}`,
            "Ctrl+单击：打开文件并在左侧文件树中定位",
          ]
            .filter(Boolean)
            .join("\n")
        : `Ctrl+单击：打开文件并在左侧文件树中定位\n${linkText}`;
      // 外部登记表链接额外加醒目样式，让"这个值可以点"一眼可见
      // （普通 .lst 路径链接保持只有虚线下划线）。
      result.push(
        Decoration.mark({
          class: isExternalLink
            ? "cm-annotation-link cm-external-link"
            : "cm-annotation-link",
          attributes: { title: hint },
        }).range(targetStart, targetEnd)
      );
      if (isExternalLink) {
        // 值后面的说明标签：只给人看，不进文档、不可点（见 ExternalLinkHintWidget）。
        result.push(
          Decoration.widget({ widget: externalLinkHintWidget, side: 1 }).range(targetEnd)
        );
      }
    }

    if (annotation.type === "reference" && targetStart < targetEnd) {
      // 关联目标（物品 ID）本身：加一个强调样式。黄色 ID 是主、后面的绿色关联框是次，
      // 所以这里让 ID 更重更亮（2026-09-27 用户要求）。
      result.push(
        Decoration.mark({ class: "cm-annotation-target" }).range(targetStart, targetEnd)
      );
    }

    if (
      display.placement !== "hidden" &&
      annotation.title.trim() !== "" &&
      // 外部登记表链接的 Title 是套装名（只供悬停提示用），不渲染成名称标签。
      !isExternalLink &&
      // 绿色关联框（ID 关联标签）：Alt+Q 可整体隐藏，原文保持可编辑（2026-09-28 用户要求）。
      (display.showReferenceTags || annotation.type !== "reference")
    ) {
      const position =
        display.placement === "line-end" ? state.doc.lineAt(targetEnd).to : targetEnd;
      result.push(
        Decoration.widget({
          widget: new AnnotationWidget(
            annotation,
            (fileIndex) => emit("open-reference", fileIndex),
            showAnnotationTooltip,
            scheduleHideTooltip,
            (request) => emit("edit-placeholder", request)
          ),
          side: 1,
        }).range(position)
      );
    }
    return result;
  }).sort((a, b) => a.from - b.from);
  return Decoration.set(ranges, true);
}

const annotationDisplayField = StateField.define<AnnotationDisplay>({
  create() {
    return {
      annotations: props.annotations ?? [],
      placement: props.tagPlacement ?? "after-target",
      showReferenceTags: props.showReferenceTags ?? true,
    };
  },
  update(display, transaction) {
    let next = display;
    if (transaction.docChanged) {
      next = {
        ...next,
        annotations: next.annotations.map((annotation) => ({
          ...annotation,
          start: transaction.changes.mapPos(annotation.start, 1),
          end: transaction.changes.mapPos(annotation.end, -1),
        })),
      };
    }
    for (const effect of transaction.effects) {
      if (effect.is(setAnnotations)) next = effect.value;
    }
    return next;
  },
});

const annotationField = StateField.define<DecorationSet>({
  create(state) {
    return annotationDecorations(state, state.field(annotationDisplayField));
  },
  update(decorations, transaction) {
    let next = decorations.map(transaction.changes);
    for (const effect of transaction.effects) {
      if (effect.is(setAnnotations)) {
        next = annotationDecorations(transaction.state, effect.value);
      }
    }
    return next;
  },
  provide: (field) => EditorView.decorations.from(field),
});

/**
 * A3 正文悬浮提示：把已有注解（**段注释 / 物品编号 / 文件路径**）挂到正文 token 上。
 *
 * 此前只有「注解胶囊」自己能弹提示（自绘 NTooltip），鼠标停在被注解的**原文**上没有任何
 * 反馈；这里用 CodeMirror 官方 `hoverTooltip` 统一补上，数据仍走同一份
 * `annotationDisplayField` —— 不新增后端、不新增数据源。
 * 标记：pvfAnnotationHoverA3_20261006
 */
const annotationHover = hoverTooltip(
  (view, pos) => {
    const display = view.state.field(annotationDisplayField, false);
    if (!display) return null;
    const hit = display.annotations.find(
      (item) => pos >= item.start && pos <= item.end && !!(item.title || item.content)
    );
    if (!hit) return null;
    const text = [hit.title, hit.content].filter(Boolean).join("\n\n");
    if (!text) return null;
    return {
      pos: hit.start,
      end: hit.end,
      above: true,
      create: () => {
        const dom = document.createElement("div");
        dom.className = "cm-annotation-hover";
        dom.textContent = text;
        return { dom };
      },
    };
  },
  { hoverTime: 220 }
);

const diagnosticLineField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(decorations, transaction) {
    let next = decorations.map(transaction.changes);
    for (const effect of transaction.effects) {
      if (!effect.is(setDiagnosticLine)) continue;
      if (effect.value === null) {
        next = Decoration.none;
        continue;
      }
      const line = transaction.state.doc.line(effect.value);
      next = Decoration.set([
        Decoration.line({ class: "cm-diagnostic-line" }).range(line.from),
      ]);
    }
    return next;
  },
  provide: (field) => EditorView.decorations.from(field),
});

/**
 * A7 行内错误标记：把校验问题（当前接「清单查重」）画到行上 —— 行底红色 + 行号旁红点，
 * 鼠标停在行号红点或该行上直接显示原因（原生 `title`，不再引入浮层机制）。
 *
 * 语义约定：**文档一改就清空**（存的是"查重那一刻"的行号，正文改过就不再对应），
 * 用户重新查重即再次出现。只装普通文件通道。
 * 标记：pvfInlineProblemsA7_20261006
 */
const setProblems = StateEffect.define<{ line: number; message: string }[]>();

const problemsField = StateField.define<readonly { line: number; message: string }[]>({
  create: () => [],
  update(list, transaction) {
    if (transaction.docChanged) return [];
    for (const effect of transaction.effects) {
      if (effect.is(setProblems)) return effect.value;
    }
    return list;
  },
});

/** 行底高亮 + `title`（鼠标停在该行即显示原因）。 */
const problemsDeco = EditorView.decorations.compute([problemsField], (state) => {
  const list = state.field(problemsField);
  if (list.length === 0) return Decoration.none;
  const ranges: Range<Decoration>[] = [];
  for (const item of list) {
    if (!Number.isFinite(item.line) || item.line < 1 || item.line > state.doc.lines) continue;
    ranges.push(
      Decoration.line({
        class: "cm-problem-line",
        attributes: { title: item.message },
      }).range(state.doc.line(item.line).from)
    );
  }
  return Decoration.set(ranges, true);
});

class ProblemMarker extends GutterMarker {
  constructor(readonly message: string) {
    super();
  }
  eq(other: ProblemMarker): boolean {
    return other.message === this.message;
  }
  toDOM(): HTMLElement {
    const dom = document.createElement("div");
    dom.className = "cm-problem-marker";
    dom.textContent = "●";
    dom.title = this.message;
    return dom;
  }
}

/** 行号旁的红点：只在有问题的行上出现，`title` 直接说明原因。 */
const problemsGutter = gutter({
  class: "cm-problem-gutter",
  lineMarker(view, line) {
    const list = view.state.field(problemsField, false);
    if (!list || list.length === 0) return null;
    const number = view.state.doc.lineAt(line.from).number;
    const hit = list.find((item) => item.line === number);
    return hit ? new ProblemMarker(hit.message) : null;
  },
});

/**
 * tab 箭头的形状：**与 CodeMirror 内置完全相同的几何**（同一张 200×20 的 SVG、
 * 同样 `auto 100%` + `right 90%`，仍会随 tab 宽度被裁切 —— 观感与原版一致）。
 * 这里只把它当"形状遮罩"，颜色交给 `background-color: currentColor`：
 * 于是能跟随主题、并且可以提亮（用户 2026-09-27：只改颜色，不动样式）。
 */
const TAB_ARROW_MASK =
  `url('data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg" width="200" height="20"><path stroke="%23000" stroke-width="1" fill="none" d="M1 10H196L190 5M190 15L196 10M197 4L197 16"/></svg>')`;

function createEditorTheme(themeId: ResolvedThemeId) {
  return EditorView.theme(
    {
      "&": {
        height: "100%",
        fontSize: "13px",
        color: "var(--pvf-text-primary)",
        backgroundColor: "transparent",
      },
    ".cm-scroller": {
      fontFamily: "'SF Mono', Menlo, Consolas, 'Courier New', monospace",
      lineHeight: "1.55",
      userSelect: "text",
    },
    ".cm-gutters": {
      backgroundColor: "transparent",
      border: "none",
      color: "var(--pvf-editor-gutter-text)",
    },
    ".cm-content": { padding: "8px 0" },
    // tab 箭头：几何/尺寸/位置**完全沿用 CodeMirror 内置样式**（还原成最初始观感），
    // 只把颜色换成跟随主题的正文色并提亮 ⇒ 一眼能看出哪里是 tab（只改颜色，不动样式）。
    ".cm-highlightTab": {
      backgroundImage: "none",
      backgroundColor: "currentColor",
      opacity: "0.7",
      maskImage: TAB_ARROW_MASK,
      maskSize: "auto 100%",
      maskPosition: "right 90%",
      maskRepeat: "no-repeat",
      "-webkit-mask-image": TAB_ARROW_MASK,
      "-webkit-mask-size": "auto 100%",
      "-webkit-mask-position": "right 90%",
      "-webkit-mask-repeat": "no-repeat",
    },
    ".cm-activeLine": { backgroundColor: "var(--pvf-editor-active-line)" },
    ".cm-activeLineGutter": { backgroundColor: "var(--pvf-editor-active-line)" },
    ".cm-selectionMatch": { backgroundColor: "var(--pvf-editor-selection-match)" },
  },
  { dark: themeId === "dark" }
  );
}

/** 大文件的 change 回传合并：整篇序列化是 O(行数)，每次击键都做会卡住输入。 */
let changeTimer: number | undefined;
/** 最近一次回报给父组件的文本：watch(props.doc) 用它做 O(1) 短路。 */
let lastEmitted: string | null = null;

function reportChange(text: string): void {
  lastEmitted = text;
  emit("change", text);
}

function scheduleChange(currentView: EditorView): void {
  if (changeTimer !== undefined) window.clearTimeout(changeTimer);
  changeTimer = window.setTimeout(() => {
    changeTimer = undefined;
    reportChange(currentView.state.doc.toString());
  }, 250);
}

/** 卸载或整篇替换前把未回传的改动补发出去，避免丢修改。 */
function flushPendingChange(): void {
  if (changeTimer === undefined) return;
  window.clearTimeout(changeTimer);
  changeTimer = undefined;
  if (view) reportChange(view.state.doc.toString());
}

/* ── 本轮新增：A2 段折叠 / A1 段名补全 / A8 跳行 ───────────────────────────────
   标记串（供 check-frontend-live.ps1 校验 dev server 是否已吐新代码）：
   pvfFoldGotoA2A8_20261006
   三条都只作用于普通文件通道；大文件通道（largeFile）一律不装
   （用户 2026-10-06 明确：大文件先不要动）。全部只读渲染层，不改文本。
   ──────────────────────────────────────────────────────────────────────── */

/** PVF 段头：独占一行、形如 `[名称]`（闭合写法 `[/名称]` 也匹配）。 */
const PVF_SECTION_LINE = /^\s*\[\/?[^\]\n]+\]\s*$/;
/** PVF 闭合标签行（如 `[/if]`）：并入上一段的折叠范围。 */
const PVF_CLOSE_LINE = /^\s*\[\/[^\]\n]+\]\s*$/;

/**
 * A2 段折叠：从段头行末折叠到「下一个段头行」之前；若该段以闭合标签收尾，
 * 闭合标签一并折进去。纯读计算，不触碰文本。
 */
const pvfFoldService = foldService.of((state, lineStart) => {
  const startLine = state.doc.lineAt(lineStart);
  if (!PVF_SECTION_LINE.test(startLine.text)) return null;
  let last = startLine.number;
  for (let n = startLine.number + 1; n <= state.doc.lines; n += 1) {
    const text = state.doc.line(n).text;
    if (PVF_SECTION_LINE.test(text)) {
      if (PVF_CLOSE_LINE.test(text)) last = n;
      break;
    }
    last = n;
  }
  if (last === startLine.number) return null;
  return { from: startLine.to, to: state.doc.line(last).to };
});

/** 段名补全缓存：Text 不可变，按引用比较即可，只在文档变动后重扫一次。 */
let sectionNamesCache: { doc: unknown; names: string[] } | null = null;

function sectionNames(state: EditorState): string[] {
  if (sectionNamesCache && sectionNamesCache.doc === state.doc) return sectionNamesCache.names;
  const names = new Set<string>();
  for (let n = 1; n <= state.doc.lines; n += 1) {
    const m = /^\[(\/?)([^\]\n]+)\]$/.exec(state.doc.line(n).text.trim());
    if (!m) continue;
    const name = m[2].trim();
    if (name) names.add(`${m[1]}${name}`);
  }
  const list = [...names].sort();
  sectionNamesCache = { doc: state.doc, names: list };
  return list;
}

/* ── A1 段目录（全量段名 + 段内字段/枚举取值）────────────────────────────────
   数据来自后端 `FormViewService.CompletionCatalog`：它把 `config/formats.json`
   （结构化视图规则）与「注释数据」合并去重后下发 —— 前端不各自解析，避免口径漂移。
   只读一次、进程内缓存；取不到就静默降级为「本文件已有段名」，绝不影响打字。
   标记：pvfSectionCatalogA1_20261006
   ──────────────────────────────────────────────────────────────────────────── */
let sectionCatalog: FormViewCompletionSection[] = [];
let sectionCatalogLoading: Promise<void> | null = null;

function loadSectionCatalog(): Promise<void> {
  if (sectionCatalog.length > 0) return Promise.resolve();
  if (!sectionCatalogLoading) {
    sectionCatalogLoading = CompletionCatalog()
      .then((result) => {
        sectionCatalog = result?.sections ?? [];
      })
      .catch(() => {
        // 后端没起来 / 服务未注册：退化为只提示本文件段名，不弹错、不影响打字。
        sectionCatalog = [];
      })
      .then(() => {
        sectionCatalogLoading = null;
      });
  }
  return sectionCatalogLoading;
}

/** 按段名（大小写不敏感）取段目录条目。 */
function catalogSection(name: string): FormViewCompletionSection | null {
  const key = name.trim().toLowerCase();
  if (!key) return null;
  for (const item of sectionCatalog) {
    if (item.section.trim().toLowerCase() === key) return item;
  }
  return null;
}

/** 段内第 index 个 token 对应的列定义（与后端表格投影同一套 `index % rowTokens`）。 */
function columnAt(item: FormViewCompletionSection, index: number): FormViewCompletionColumn | null {
  const columns = item.columns ?? [];
  if (columns.length === 0) return null;
  const rowTokens = item.rowTokens && item.rowTokens > 0 ? item.rowTokens : columns.length;
  return columns[index % rowTokens] ?? null;
}

/** 段内 token 位置：index = 光标前已输入完的 token 数，word = 正在输入的这一段。 */
function tokenPosition(text: string): { index: number; word: string } {
  const leading = text.replace(/^\s+/, "");
  if (leading === "") return { index: 0, word: "" };
  const parts = leading.split(/\s+/).filter(Boolean);
  if (/\s$/.test(leading)) return { index: parts.length, word: "" };
  return { index: Math.max(0, parts.length - 1), word: parts[parts.length - 1] };
}

/** 当前光标所在段（向上找最近的段头行）与段内位置；光标在段头行上、或闭合标签行内返回 null。 */
function sectionPositionAt(
  state: EditorState,
  pos: number
): { name: string; index: number; word: string } | null {
  const line = state.doc.lineAt(pos);
  for (let n = line.number; n >= 1; n -= 1) {
    const row = state.doc.line(n);
    const header = /^\[(\/?)([^\]\n]+)\]$/.exec(row.text.trim());
    if (!header) continue;
    if (n === line.number || header[1] === "/") return null;
    const body = state.doc.sliceString(state.doc.line(n + 1).from, pos);
    return { name: header[2].trim(), ...tokenPosition(body) };
  }
  return null;
}

/**
 * A1 脚本补全（只作用于普通文件通道）：
 *  ① 行首 `[` 内 → **全量段名**（后端段目录）+ 本文件已有段名 + `[/xxx]` 闭合写法；
 *  ② 段内任意位置 → 该位置的**字段名**（`Columns[N % rowTokens]`，与表格投影同源）
 *     与**合法取值**（枚举）；位置提示写在候选的 detail / info 里，边打边看。
 * 后端目录拿不到时退化为 ①（只提示本文件段名），不报错。
 */
function pvfCompletionSource(context: CompletionContext): CompletionResult | null {
  const state = context.state;
  const line = state.doc.lineAt(context.pos);
  const before = line.text.slice(0, context.pos - line.from);

  // ① 段头：行首方括号内
  const headerMatch = /^(\s*)\[(\/?)([A-Za-z0-9_ -]*)$/.exec(before);
  if (headerMatch) {
    void loadSectionCatalog(); // 首次触发时异步取目录；本次先用已有数据
    const typed = `${headerMatch[2]}${headerMatch[3]}`.toLowerCase();
    const options: Completion[] = [];
    const seen = new Set<string>();
    const push = (name: string, detail: string) => {
      const key = name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      options.push({
        label: name,
        type: "keyword",
        detail,
        boost: detail === "本文件已有段名" ? 1 : 0,
      });
    };
    // 本文件已出现过的段名排前面（补起来最稳）
    for (const name of sectionNames(state)) {
      if (name.toLowerCase().startsWith(typed)) push(name, "本文件已有段名");
    }
    for (const item of sectionCatalog) {
      const name = item.section.trim();
      if (!name || !name.toLowerCase().startsWith(typed)) continue;
      push(name, item.label ? `${item.label}（段目录）` : "段目录");
      if (!typed.startsWith("/")) push(`/${name}`, "闭合写法");
    }
    if (options.length === 0) return null;
    return {
      from: context.pos - typed.length,
      options: options.slice(0, 300),
      validFor: /^\/?[A-Za-z0-9_ -]*$/,
    };
  }

  // ② 段内位置：字段名 + 合法取值
  const spot = sectionPositionAt(state, context.pos);
  if (!spot) return null;
  const item = catalogSection(spot.name);
  if (!item) return null;
  const column = columnAt(item, spot.index);
  const token = (item.tokens ?? []).find((entry) => entry.index === spot.index) ?? null;
  const values = column?.values ?? token?.values ?? null;
  const label = (column?.label ?? "").trim() || (token?.label ?? "").trim();
  const where = `段 [${item.section}] 第 ${spot.index + 1} 个 token${label ? `：${label}` : ""}`;
  const options: Completion[] = [];
  if (values) {
    for (const [value, text] of Object.entries(values)) {
      if (spot.word && !value.startsWith(spot.word) && !text.includes(spot.word)) continue;
      options.push({
        label: text ? `${value}　${text}` : value,
        apply: value,
        type: column?.type || token?.type || "text",
        detail: label || where,
        info: `${where}${text ? `\n\n取值：${value} = ${text}` : ""}`,
      });
    }
  } else if (label) {
    // 没有枚举取值时，用一条「不改动文本」的候选把字段名显示出来（= 参数提示）。
    options.push({
      label,
      apply: spot.word,
      type: "info",
      detail: where,
      info: `${where}\n\n（该位置暂无合法取值表，仅提示字段名）`,
    });
  }
  if (options.length === 0) return null;
  return { from: context.pos - spot.word.length, options: options.slice(0, 200) };
}

/* A8 跳行（Ctrl/Cmd+G）：复用既有的 revealPosition，只补一个输入入口。 */
const gotoOpen = ref(false);
const gotoText = ref("");
const gotoInput = ref<HTMLInputElement | null>(null);
const gotoPos = ref({ left: 0, top: 0 });

function openGotoLine(): void {
  if (!view) return;
  const rect = view.dom.getBoundingClientRect();
  gotoPos.value = { left: rect.left + 12, top: rect.top + 12 };
  gotoText.value = "";
  gotoOpen.value = true;
  void nextTick(() => gotoInput.value?.focus());
}

function closeGotoLine(): void {
  gotoOpen.value = false;
  view?.focus();
}

function submitGotoLine(): void {
  const n = Number.parseInt(gotoText.value.trim(), 10);
  closeGotoLine();
  if (!Number.isFinite(n) || n <= 0) return;
  revealPosition(n);
}

function makeExtensions(themeId: ResolvedThemeId) {
  const isJavaScript = props.language === "javascript";
  // Lua 只给 .lua 用（判定见 EditorPane.vue 的 editorLanguage）：110 版 PVF 的 AI
  // 脚本是 Lua，老版的 .nut 是 Squirrel，两者语法与高亮规则完全不同，不能混用。
  const isLua = props.language === "lua";
  // 大文件降级：空白高亮要给每个空白字符加装饰、折行要逐字符测量，几十万行时都是
  // 卡顿主因；这里只剔除空白高亮（折行保留：长行不换行会看不见内容）。
  const large = props.largeFile === true;
  return [
    lineNumbers(),
    highlightActiveLineGutter(),
    highlightActiveLine(),
    history(),
    drawSelection(),
    // 空白字符显示：大文件要关（几十万行逐字符加装饰会卡）；`.lua` 也关 ——
    // Lua 靠 tab 缩进，满屏 `→`/`•` 比代码本身还显眼（用户 2026-10-01 反馈）。
    // 纯显示开关，不碰文本内容。
    ...(large || isLua ? [] : [highlightWhitespace()]),
    rectangularSelection(),
    crosshairCursor(),
    highlightSelectionMatches(),
    // Ctrl+F 文本内查找（本轮新增：普通编辑器此前没有这个面板）。
    // 官方 search 面板 + 共用「中文文案/胶囊外观」（见 ../searchPanel.ts）。
    // 大文件（props.largeFile）不装，保持原样。标记：pvfSearchPanelNormalFile_20261006
    ...(large ? [] : [search(), searchPanelPhrases, searchPanelTheme]),
    // A2 段折叠：折叠边栏 + 折起占位 + 自定义「按 [段] 折叠」规则。
    // 大文件通道不装（用户 2026-10-06 明确）。
    ...(large ? [] : [codeFolding(), foldGutter(), pvfFoldService]),
    vimComp.of(props.vimMode ? vim() : []),
    keymap.of([
      // A8 跳行：Ctrl/Cmd+G。放在最前 ⇒ 覆盖 searchKeymap 的「查找下一个」，
      // 与主流编辑器一致；查找下一个仍可用 F3 / 搜索面板按钮。
      {
        key: "Mod-g",
        run: () => {
          openGotoLine();
          return true;
        },
      },
      ...defaultKeymap,
      ...historyKeymap,
      ...searchKeymap,
      ...foldKeymap,
      { key: "Tab", run: insertTab, shift: indentLess },
    ]),
    EditorView.domEventHandlers({
      click(event, currentView) {
        const mouseEvent = event as MouseEvent;
        // 路径链接只在 Ctrl（macOS：Cmd）+ 左键时跳转；普通单击保持纯文本编辑行为
        // （2026-09-28 用户要求）。
        if (!mouseEvent.ctrlKey && !mouseEvent.metaKey) return false;
        const position = currentView.posAtCoords({
          x: mouseEvent.clientX,
          y: mouseEvent.clientY,
        });
        if (position === null) return false;
        const display = currentView.state.field(annotationDisplayField, false);
        const annotation = display?.annotations.find(
          (item) =>
            item.targetFileIndex >= 0 &&
            // ID 关联（reference）不拦截正文点击：原文照旧可编辑，关联只走绿色标签。
            // 例外：.lst 清单里 reference 就是这一行的目标文件，正文点击应当跳转
            // （2026-09-29 修复：skill/*.lst 的条目是「相对清单目录 + 混合大小写」写法，
            //  如 skill/swordmanskill.lst 里的 `Swordman/X.skl`；前端兜底 ResolveFiles
            //  只认归档根精确路径，解析不了，所以这类清单只能靠 reference 跳转）。
            (props.listNames || item.type !== "reference") &&
            item.start <= position &&
            position < item.end
        );
        if (!annotation) {
          // 超大清单（标注被后端跳过）的行内路径链接兜底：点击落在 `路径` token 上时，
          // 惰性解析目标 fileIndex（可见区已批量预取，通常命中缓存，无额外往返）。
          if (!props.listNames) return false;
          const linkPath = listLinkAt(currentView, position);
          if (!linkPath) return false;
          mouseEvent.preventDefault();
          mouseEvent.stopPropagation();
          void resolveListLinkIndex(linkPath).then((fileIndex) => {
            if (fileIndex < 0) return;
            // Ctrl/Cmd+单击：打开文件并在左侧文件树中定位。
            emit("activate-reference", fileIndex);
          });
          return true;
        }
        mouseEvent.preventDefault();
        mouseEvent.stopPropagation();
        // Ctrl/Cmd+单击：打开文件并在左侧文件树中定位。
        emit("activate-reference", annotation.targetFileIndex);
        return true;
      },
    }),
    readOnlyComp.of(EditorState.readOnly.of(!!props.readOnly)),
    annotationDisplayField,
    annotationField,
    // A3：正文 token 悬浮提示（段注释 / 物品编号 / 文件路径）
    annotationHover,
    diagnosticLineField,
    // A7：行内错误标记（行底 + 行号旁红点 + 原生 title 说明原因）
    problemsField,
    problemsDeco,
    problemsGutter,
    indentUnit.of("\t"),
    // .lst 清单：可见行路径后显示目标文件名称（惰性，不影响打开速度）。
    ...(props.listNames ? [listNamePlugin] : []),
    // 语法解析（lezer）要扫描全文：41 万行的清单解析一次就是几十秒，是"打开第二个大文件
    // 直接卡死"的主因。超大文本不做语法着色，其余能力（链接跳转、定位、搜索）保留。
    ...(large
      ? []
      : [isJavaScript ? javascript() : isLua ? luaLanguage.extension : pvfLanguage.extension]),
    ...(large
      ? []
      : isJavaScript
        ? [
            tooltips({ parent: document.body, position: "fixed" }),
            javascriptHighlighting,
            autocompletion({ override: [scriptCompletionSource] }),
          ]
        : isLua
          ? [luaHighlighting]
          : [
              pvfHighlighting,
              // A1 段名补全：只在行首 `[` 内触发，提示本文件已有段名。
              autocompletion({ override: [pvfCompletionSource] }),
            ]),
    editorThemeComp.of(createEditorTheme(themeId)),
    // 折行：长行（[item list] 一长串 ID）必须换行显示，否则要横向滚动、看不全。
    // 大文件也保留折行（2026-09-27 用户明确要求）。
    EditorView.lineWrapping,
    EditorView.updateListener.of((u) => {
      if (!u.docChanged) return;
      // 整篇序列化是 O(行数)：大文件每敲一个键都做一遍会卡住输入，
      // 这里合并到 250ms 后回传一次（内容不丢，卸载/切档前会强制 flush）。
      if (large) {
        scheduleChange(u.view);
        return;
      }
      reportChange(u.state.doc.toString());
    }),
  ];
}

function scriptCompletionSource(
  context: CompletionContext,
): CompletionResult | Promise<CompletionResult | null> | null {
  const fallback = (): CompletionResult | null => {
    const word = context.matchBefore(/[\w$.-]*/);
    if (!word || (word.from === word.to && !context.explicit)) return null;
    return {
      from: word.from,
      options: [
        { label: "pvf", type: "variable", detail: "PVF 脚本 API" },
        { label: "pvf.files", type: "function", detail: "PVFFile[]" },
        { label: "pvf.find", type: "function", detail: "(path) => PVFFile | null" },
        { label: "pvf.glob", type: "function", detail: "(pattern) => PVFFile[]" },
        { label: "pvf.fileset", type: "function", detail: "(name) => PVFFileSet | null" },
        { label: "pvf.createFileset", type: "function", detail: "(name, paths?) => PVFFileSet" },
        { label: "fileset.getAll", type: "function", detail: "() => string[]" },
        { label: "fileset.setAll", type: "function", detail: "(paths) => number" },
        { label: "pvf.createFile", type: "function", detail: "(path, dataType?, text?) => PVFFile" },
        { label: "pvf.copyFile", type: "function", detail: "(from, to, overwrite?) => PVFFile" },
        { label: "pvf.deleteFile", type: "function", detail: "(path) => boolean" },
        { label: "pvf.lst", type: "function", detail: "(path) => PVFList" },
        { label: "list.get", type: "function", detail: "() => Record<string, string>" },
        { label: "list.forEach", type: "function", detail: "(id, path) => void，按文件顺序" },
        { label: "list.set", type: "function", detail: "(id, path) => void" },
        { label: "list.mset", type: "function", detail: "(entries) => void" },
        { label: "list.unset", type: "function", detail: "(id) => boolean" },
        { label: "list.getId", type: "function", detail: "(path) => string | null" },
        { label: "pvf.log", type: "function", detail: "记录脚本日志" },
        { label: "pvf.progress", type: "function", detail: "更新执行进度" },
        { label: "pvf.modifiedCount", type: "property", detail: "number" },
        { label: "pvf.scannedCount", type: "property", detail: "number" },
        { label: "file.parse", type: "function", detail: "() => PVFDocument" },
        { label: "file.write", type: "function", detail: "(document) => void" },
        { label: "document.section", type: "function", detail: "(path) => PVFSection | null" },
        { label: "document.sections", type: "function", detail: "(path) => PVFSection[]" },
        { label: "document.warnings", type: "function", detail: "() => PVFParseWarning[]" },
        { label: "section.get", type: "function", detail: "(index?) => PVFScalar" },
        { label: "section.set", type: "function", detail: "(value, index?) => void" },
        { label: "section.append", type: "function", detail: "(value) => void" },
      ],
      validFor: /[\w$.-]*/,
    };
  };
  const result = declarationCompletionSource(context);
  if (result && typeof (result as Promise<CompletionResult | null>).then === "function") {
    return (result as Promise<CompletionResult | null>).then((value) => value ?? fallback());
  }
  return result ?? fallback();
}

function revealPosition(lineNumber: number, columnNumber = 1): void {
  if (!view) return;
  const line = Math.max(1, Math.min(view.state.doc.lines, Math.trunc(lineNumber)));
  const column = Math.max(1, Math.trunc(columnNumber));
  const lineInfo = view.state.doc.line(line);
  const position = Math.min(lineInfo.to, lineInfo.from + column - 1);
  view.dispatch({
    selection: { anchor: position },
    effects: [
      setDiagnosticLine.of(line),
      EditorView.scrollIntoView(position, { y: "center" }),
    ],
  });
  view.focus();
}

/** 在光标处插入文本(如字符串表引用),并把光标放到插入内容之后。 */
function insertText(text: string): boolean {
  if (!view) return false;
  const range = view.state.selection.main;
  view.dispatch({
    changes: { from: range.from, to: range.to, insert: text },
    selection: { anchor: range.from + text.length },
  });
  view.focus();
  return true;
}

defineExpose({ revealPosition, insertText });

onMounted(() => {
  view = new EditorView({
    state: EditorState.create({ doc: props.doc, extensions: makeExtensions(props.themeId) }),
    parent: host.value!,
  });
  // 记下滚动位置：标签被隐藏（display:none）时它会被浏览器归零，切回来要写回去。
  view.scrollDOM.addEventListener("scroll", rememberScrollTop, { passive: true });
});

onBeforeUnmount(() => {
  hideAnnotationTooltip();
  flushPendingChange(); // 大文件的改动可能还在防抖窗口里，先补发再销毁
  view?.scrollDOM.removeEventListener("scroll", rememberScrollTop);
  view?.destroy();
  view = null;
});

// 外部文档切换(标签切换):内容不同才整体替换
watch(
  () => props.doc,
  (doc) => {
    if (!view) return;
    // O(1) 短路：这个 doc 就是本组件刚刚回报出去的那份文本 ⇒ 编辑器里已经是它了。
    // 少了这一步，每次切档/每次击键回传都要做一次 O(文档) 的 toString() 比较
    // （切大文件的标签时尤其明显）。
    if (lastEmitted !== null && lastEmitted === doc) return;
    const current = view.state.doc.toString();
    if (doc !== current) {
      view.dispatch({
        changes: { from: 0, to: current.length, insert: doc },
        effects: setDiagnosticLine.of(null),
      });
    }
  }
);

/** 选中并滚动到指定区间（搜索结果定位用）。 */
function revealRange(from: number, to: number): void {
  if (!view) return;
  view.dispatch({
    selection: { anchor: from, head: to },
    effects: [
      setDiagnosticLine.of(view.state.doc.lineAt(from).number),
      EditorView.scrollIntoView(from, { y: "center" }),
    ],
  });
  view.focus();
}

/** 依次尝试 needles，命中第一处即选中并高亮整行，返回是否命中。 */
function revealNeedle(needles: string[]): boolean {
  if (!view) return false;
  const doc = view.state.doc;
  const text = doc.toString();
  for (const needle of needles) {
    const position = text.indexOf(needle);
    if (position < 0) continue;
    revealRange(position, position + needle.length);
    return true;
  }
  return false;
}

// ---------------------------------------------------------------------------
// 标签滚动位置记忆
//
// 背景：标签切换用 NTabPane 的 `display-directive="show:lazy"`，隐藏的标签是
// display:none（不是卸载）。元素一旦 display:none 就失去布局，浏览器会把
// `.cm-scroller` 的 scrollTop 归零 —— 于是切回来时停在文档第一行（用户实测）。
//
// 做法：可见期间持续记下 scrollTop（隐藏期间不记，避免把归零当成真实位置），
// 重新可见时在下一帧写回（写前先 requestMeasure，让 CodeMirror 重新测量布局）。
// 定位请求（搜索命中 / 文件树跳转）驱动的显示要跳过恢复，否则会盖掉定位。
// ---------------------------------------------------------------------------
let savedScrollTop = 0;
let lastRevealAt = 0;

function rememberScrollTop(): void {
  if (!view || props.active === false) return;
  savedScrollTop = view.scrollDOM.scrollTop;
}

function restoreScrollTop(): void {
  const top = savedScrollTop;
  if (!view || top <= 0) return;
  requestAnimationFrame(() => {
    if (!view || props.active === false) return;
    view.requestMeasure();
    view.scrollDOM.scrollTop = top;
  });
}

watch(
  () => props.active,
  (active) => {
    if (!active) return;
    // 本次显示是为了「定位到某行」（下面的 reveal watcher 自己会滚动）：别抢。
    if (performance.now() - lastRevealAt < 300) return;
    restoreScrollTop();
  },
  { flush: "post" }
);

// 必须在 doc 的 watch 之后注册：文件内容整体替换先发生，再做定位。
watch(
  () => props.reveal?.seq,
  () => {
    if (!props.reveal) return;
    lastRevealAt = performance.now();
    // AI 引用跳转：带行号时直接按行定位；否则按搜索命中文本定位。
    if (props.reveal.line && props.reveal.line > 0) {
      revealPosition(props.reveal.line);
    } else if (props.reveal.needles.length > 0) {
      revealNeedle(props.reveal.needles);
    }
  }
);

watch(
  () => [props.annotations, props.tagPlacement, props.showReferenceTags] as const,
  ([annotations, placement, showReferenceTags]) => {
    view?.dispatch({
      effects: setAnnotations.of({
        annotations: annotations ?? [],
        placement: placement ?? "after-target",
        showReferenceTags: showReferenceTags ?? true,
      }),
    });
  },
  // 刻意不用 deep：标注每次都由后端整体重算后替换（数组引用必变），deep 会逐字段
  // 遍历几十万条标注做依赖收集，是打开大清单时卡顿的次要来源。
);

// A7：行内错误标记。同样是整份替换语义（引用变才 dispatch），不做 deep 比较。
watch(
  () => props.problems,
  (problems) => {
    view?.dispatch({ effects: setProblems.of(problems ?? []) });
  }
);

watch(
  () => props.readOnly,
  (ro) => {
    view?.dispatch({
      effects: readOnlyComp.reconfigure(EditorState.readOnly.of(!!ro)),
    });
  }
);

watch(
  () => props.vimMode,
  (enabled) => {
    view?.dispatch({
      effects: vimComp.reconfigure(enabled ? vim() : []),
    });
  }
);

watch(
  () => props.themeId,
  (themeId) => {
    view?.dispatch({
      effects: editorThemeComp.reconfigure(createEditorTheme(themeId)),
    });
  }
);
</script>

<template>
  <div ref="host" class="code-editor" />
  <!-- A8 跳行浮层：Ctrl/Cmd+G 唤起；回车跳转、Esc 关闭 -->
  <div
    v-if="gotoOpen"
    class="goto-line"
    :style="{ left: `${gotoPos.left}px`, top: `${gotoPos.top}px` }"
  >
    <input
      ref="gotoInput"
      v-model="gotoText"
      class="goto-line-input"
      placeholder="行号，回车跳转"
      @keydown.stop
      @keydown.enter.prevent="submitGotoLine"
      @keydown.esc.prevent="closeGotoLine"
    />
  </div>
  <NTooltip
    :show="annotationTooltip.visible"
    trigger="manual"
    :x="annotationTooltip.left"
    :y="annotationTooltip.top"
    :placement="annotationTooltip.placement"
    to="body"
    :raw="true"
    :show-arrow="false"
    :delay="0"
    :duration="0"
    :keep-alive-on-hover="true"
    :z-index="2000"
    content-class="annotation-tooltip-content"
  >
    <div
      class="annotation-tooltip-inner"
      role="tooltip"
      @mouseenter="cancelTooltipHide"
      @mouseleave="scheduleHideTooltip"
    >
      <div v-if="annotationTooltip.loading" class="annotation-tooltip-loading">正在加载图片…</div>
      <img v-if="annotationTooltip.dataUrl" :src="annotationTooltip.dataUrl" alt="标注图片" />
      <div class="annotation-tooltip-text">{{ annotationTooltip.content }}</div>
    </div>
  </NTooltip>
</template>

<style scoped>
/* A8 跳行浮层：用主题无关的半透明底（不依赖具体 --pvf-* 变量，明暗主题都能用） */
.goto-line {
  position: fixed;
  z-index: 40;
  padding: 5px 8px;
  border: 1px solid rgb(127 127 127 / 35%);
  border-radius: 6px;
  background: rgb(127 127 127 / 14%);
  backdrop-filter: blur(2px);
  box-shadow: 0 6px 18px rgb(0 0 0 / 18%);
}

.goto-line-input {
  width: 150px;
  border: none;
  outline: none;
  background: transparent;
  color: inherit;
  font: inherit;
}

.code-editor {
  flex: 1;
  min-width: 0;
  min-height: 0;
  height: auto;
  overflow: hidden;
}
.code-editor :deep(.cm-editor),
.code-editor :deep(.cm-scroller) {
  min-height: 0;
  height: 100%;
}
.code-editor :deep(.cm-annotation-tag) {
  display: inline-flex;
  align-items: center;
  /* 兜底：宽度按文字实际宽度收缩，避免被父级拉伸导致右侧留出空白。 */
  width: max-content;
  max-width: 220px;
  height: 18px;
  margin-left: 7px;
  padding: 0 6px;
  overflow: hidden;
  color: var(--pvf-editor-annotation-text);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 11px;
  line-height: 16px;
  text-overflow: ellipsis;
  vertical-align: 1px;
  white-space: nowrap;
  user-select: none;
  background: var(--pvf-editor-annotation-surface);
  border: 1px solid var(--pvf-editor-annotation-border);
  border-radius: 4px;
}
.code-editor :deep(.cm-annotation-tag--enum) {
  color: var(--pvf-editor-annotation-enum-text);
  background: var(--pvf-editor-annotation-enum-surface);
  border-color: var(--pvf-editor-annotation-enum-border);
}
.code-editor :deep(.cm-annotation-tag--reference) {
  color: var(--pvf-editor-annotation-reference-text);
  background: var(--pvf-editor-annotation-reference-surface);
  border-color: var(--pvf-editor-annotation-reference-border);
}
/* 关联目标（物品 ID）：ID 是主、关联框是次，这里把 ID 加重，视线先落在 ID 上。 */
.code-editor :deep(.cm-annotation-target) {
  font-weight: 600;
}
/* 字符串表占位符的译文：文档里仍是占位符，这里只做展示。 */
.code-editor :deep(.cm-annotation-tag--placeholder) {
  font-style: italic;
  border-style: dashed;
}
.code-editor :deep(.cm-annotation-tag--editable) {
  cursor: pointer;
}
/* 表里还没有这个键：提示需要填写，点击即可创建。 */
.code-editor :deep(.cm-annotation-tag--placeholder-missing) {
  color: var(--pvf-error);
  border-style: dashed;
  border-color: var(--pvf-error);
}
.code-editor :deep(.cm-annotation-tag--editable:hover) {
  filter: brightness(1.15);
  text-decoration: underline dotted var(--pvf-editor-annotation-link);
  text-underline-offset: 2px;
}
.code-editor :deep(.cm-annotation-inline-image) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  margin-left: 7px;
  vertical-align: middle;
  line-height: 1;
  user-select: none;
}
.code-editor :deep(.cm-annotation-inline-image-slot) {
  display: inline-flex;
  flex: 0 0 16px;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
}
.code-editor :deep(.cm-annotation-inline-image-slot img) {
  display: block;
  width: 16px;
  height: 16px;
  object-fit: contain;
}
.code-editor :deep(.cm-annotation-tag--link) {
  cursor: pointer;
}
.code-editor :deep(.cm-annotation-tag--link:hover) {
  filter: brightness(1.15);
}
.code-editor :deep(.cm-annotation-link) {
  cursor: pointer;
  text-decoration: underline dotted var(--pvf-editor-annotation-link);
  text-underline-offset: 2px;
}
/* 外部登记表链接（如 [part set index] 的编号）：淡色底 + 实线细边 + 加粗，
   与只有虚线下划线的普通路径链接区分开 —— "这里能点"一眼可见。 */
.code-editor :deep(.cm-external-link) {
  background: var(--pvf-editor-annotation-external-surface);
  /* 用 inset 阴影画下边线：不占布局空间，不扰动行高与列对齐。 */
  box-shadow: inset 0 -1px 0 var(--pvf-editor-annotation-external-border);
  border-radius: 3px;
  padding: 0 3px;
  font-weight: 600;
  text-decoration: none;
}
.code-editor :deep(.cm-external-link:hover) {
  filter: brightness(1.3);
}
/* 外部登记表链接值后面的说明标签（纯展示，不进文档、不可点）。 */
.code-editor :deep(.cm-external-hint) {
  margin-left: 6px;
  font-size: 11px;
  line-height: 16px;
  color: var(--pvf-editor-annotation-link);
  opacity: 0.75;
  user-select: none;
  white-space: nowrap;
}
/* .lst 清单行内名称标签：风格与文件树的 [中文名] 保持一致。 */
.code-editor :deep(.cm-list-name-tag) {
  margin-left: 6px;
  color: var(--pvf-success);
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
  user-select: none;
}
.code-editor :deep(.cm-diagnostic-line) {
  background: var(--pvf-error-surface);
  box-shadow: inset 3px 0 0 var(--pvf-error);
}
/* A7：行内错误标记（行底同款红底 + 行号旁红点，原因走原生 title） */
.code-editor :deep(.cm-problem-line) {
  background: var(--pvf-error-surface);
  box-shadow: inset 3px 0 0 var(--pvf-error);
}
.code-editor :deep(.cm-problem-marker) {
  height: 100%;
  padding: 0 2px;
  color: var(--pvf-error);
  font-size: 10px;
  line-height: 1;
  cursor: help;
}
:global(.cm-problem-gutter) {
  width: 14px;
}
/* A3：正文悬浮提示的外观（与注解胶囊的提示同款配色） */
:global(.cm-annotation-hover) {
  max-width: 360px;
  padding: 8px 10px;
  color: var(--pvf-text-primary);
  font-size: 12px;
  line-height: 1.55;
  white-space: pre-wrap;
  background: var(--pvf-surface-elevated);
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 6px;
  box-shadow: 0 8px 24px var(--pvf-effect-tooltip-shadow);
}
.code-editor :deep(.cm-scroller) {
  flex: 1 1 auto;
  max-height: 100%;
  overflow: auto;
}
:global(.cm-tooltip-autocomplete) {
  z-index: 1000;
}
:global(.annotation-tooltip-content) {
  max-width: 320px;
  padding: 9px;
  color: var(--pvf-text-primary);
  pointer-events: auto;
  user-select: text;
  background: var(--pvf-surface-elevated);
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 6px;
  box-shadow: 0 8px 24px var(--pvf-effect-tooltip-shadow);
}
.annotation-tooltip-inner {
  max-width: 300px;
  cursor: text;
  user-select: text;
}
.annotation-tooltip-inner img {
  display: block;
  max-width: 100%;
  max-height: 220px;
  margin: 0 auto 7px;
  object-fit: contain;
}
.annotation-tooltip-loading,
.annotation-tooltip-text {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.annotation-tooltip-loading {
  margin-bottom: 7px;
  color: var(--pvf-text-muted);
}
</style>
