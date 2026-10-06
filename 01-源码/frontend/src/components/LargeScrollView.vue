<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { NButton, NTag, useMessage } from "naive-ui";
import {
  Annotation,
  Compartment,
  EditorState,
  type Extension,
  StateEffect,
  StateField,
  type Range,
} from "@codemirror/state";
import {
  Decoration,
  type DecorationSet,
  drawSelection,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
  WidgetType,
} from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { highlightSelectionMatches, search, searchKeymap } from "@codemirror/search";
import { pvfHighlighting, pvfLanguage } from "../pvfLanguage";
import { listLinkAt, resolveListLinkIndex } from "../listNames";
import { GetFileLines, GetWindowAnnotations, type WindowAnnotation } from "../services/largeTextApi";
import { useArchiveStore } from "../stores/archive";
import { useEditorStore } from "../stores/editor";

/**
 * 大文件的「连续全文 TXT 视图」（记事本那种一路滚下去的观感）。
 *
 * 做法：用 spacer 把整个文件的高度撑开（行数 × 固定行高），滚动到哪就只向后端要
 * 「当前视口附近的行」——DOM 里永远只有几百行。
 * 这样既有"整页全部显示"的连续观感，又没有任何一次是整篇文本进窗口
 * （那正是停摆 43 秒的根因，见 services/large_text.go 顶部注释）。
 *
 * 定位靠固定行高（ROW_HEIGHT），因此 textarea 的 line-height 必须严格等于它、
 * 且不折行（wrap=off，长行横向滚动）——这是精确虚拟定位的前提。
 */
const ROW_HEIGHT = 20;
/** 上下各多取的行数：滚动时不露白。 */
const OVERSCAN = 300;
/** 一次向后端要的行数。 */
const CHUNK = 2000;

const props = defineProps<{
  index: number;
  path: string;
  size: number;
  /** 搜索结果定位请求：跳到目标行所在位置。 */
  reveal?: { seq: number; needles: string[]; line?: number } | null;
  /** 本标签当前是否可见（见 CodeEditor 的同名 prop：隐藏时滚动位置会被 display:none 归零）。 */
  active?: boolean;
}>();

/** Ctrl/Cmd+单击清单里的路径：与普通编辑器一致——打开目标文件并在左树定位。 */
const emit = defineEmits<{
  (event: "activate-reference", fileIndex: number): void;
}>();

const message = useMessage();
const archive = useArchiveStore();
const editor = useEditorStore();

/** 清单文件（.lst）：才启用行内路径链接的 Ctrl+单击跳转。 */
const isListFile = computed(() => props.path.toLowerCase().endsWith(".lst"));

const viewport = ref<HTMLDivElement | null>(null);
const totalLines = ref(0);
const editable = ref(true);
const loading = ref(false);
const saving = ref(false);

/** 已加载窗口：起始行（1 基）、行数、文本。 */
const winStart = ref(1);
const winCount = ref(0);
const winText = ref("");

// ---------------------------------------------------------------------------
// 视口窗口的编辑器：CodeMirror（P3，2026-10-01 实测通过后落地）
//
// 开发者面板探针实测（真实 WebView2 + 开着大归档）：空文档挂载 2–6ms、
// 20KB 挂载 3–5ms、130KB 挂载 1–3ms、首帧 8–13ms —— 挂载不是墙
// （当年"空文档也 30 秒"的前提是 27MB 大文本已在同一页面，如今大文本不进窗口）。
//
// 因此把窗口从裸 textarea 换成**每视口一个 CodeMirror**（同一时刻只有一个实例、
// 只装当前视口那约 130KB），拿回 PVF 语法着色；不变的是虚拟定位铁律——
// 每行必须严格 20px、不折行，否则 spacer 定位会漂移。
// ---------------------------------------------------------------------------
const cmHost = ref<HTMLDivElement | null>(null);
let cmView: EditorView | null = null;
/** 只读开关走 compartment：归档只读（editable=false）时热切换，不重建视图。 */
const editableCompartment = new Compartment();
/**
 * 「这段内容是程序换进来的（滚动换窗 / 首屏加载），不是用户打的字」标记。
 * updateListener 靠它把换窗排除掉——否则**每次打开文件都会把归档自己的内容
 * 记成一段"未保存改动"**（2026-10-01 用户实测发现的 bug：关了再开还提示）。
 */
const remoteWindowChange = Annotation.define<boolean>();

// ---------------------------------------------------------------------------
// 段内注解装饰：中文名标签（widget）+ 关联标记（mark）
//
// 注解由后端**按视口**算出（GetWindowAnnotations），位置是相对段首的字符偏移，
// 正好等于 CodeMirror 文档内的位置，直接装饰即可。
// ---------------------------------------------------------------------------
class AnnotationTagWidget extends WidgetType {
  constructor(readonly text: string) {
    super();
  }
  eq(other: AnnotationTagWidget): boolean {
    return other.text === this.text;
  }
  toDOM(): HTMLElement {
    const span = document.createElement("span");
    span.className = "lsc-ann-tag";
    span.textContent = this.text;
    return span;
  }
  ignoreEvent(): boolean {
    return true;
  }
}

const setWindowAnnotations = StateEffect.define<WindowAnnotation[]>();

function buildAnnotationDecorations(list: WindowAnnotation[], docLength: number): DecorationSet {
  const ranges: Range<Decoration>[] = [];
  for (const item of list) {
    if (item.start < 0 || item.end > docLength || item.end <= item.start) continue;
    const label = item.title || item.content || "";
    ranges.push(
      Decoration.mark({
        class: "lsc-ann",
        attributes: label ? { title: label } : undefined,
      }).range(item.start, item.end)
    );
    if (label) {
      ranges.push(
        Decoration.widget({ widget: new AnnotationTagWidget(label), side: 1 }).range(item.end)
      );
    }
  }
  return Decoration.set(ranges, true);
}

const windowAnnotationField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(deco, tr) {
    deco = deco.map(tr.changes);
    for (const effect of tr.effects) {
      if (effect.is(setWindowAnnotations)) {
        deco = buildAnnotationDecorations(effect.value, tr.state.doc.length);
      }
    }
    return deco;
  },
  provide: (field) => EditorView.decorations.from(field),
});

/**
 * Ctrl+F 查找面板中文文案（2026-10-06：面板默认是英文）。
 *
 * 键名 = `@codemirror/search` 源码里 `phrase(view, "...")` 的原始字符串，**不能改**；
 * 它走官方 EditorState.phrases 机制，所以不动 DOM、不接管行为，升级 CodeMirror 也不会坏。
 * 标记串（供 check-frontend-live.ps1 校验）：pvfSearchPanelCN_20261006
 */
const searchPanelPhrases = EditorState.phrases.of({
  Find: "查找",
  Replace: "替换",
  next: "下一个",
  previous: "上一个",
  all: "全部选中",
  "match case": "区分大小写",
  regexp: "正则",
  "by word": "全词匹配",
  replace: "替换",
  "replace all": "全部替换",
  close: "关闭",
});

const largeWindowTheme = EditorView.theme({
  // 高度交给内容自己撑开：搜索面板出现时不会把正文顶掉（窗口是顶部锚定的，
  // 向下长出去不会让行号与 spacer 错位）。
  "&": { fontSize: "13px", backgroundColor: "transparent" },
  ".cm-scroller": {
    overflow: "hidden",
    fontFamily: "'SF Mono', Menlo, Consolas, 'Courier New', monospace",
    lineHeight: "20px",
  },
  ".cm-gutters": {
    background: "transparent",
    color: "var(--pvf-text-faint)",
    border: "none",
  },
  ".cm-activeLine": { background: "rgba(127, 127, 127, 0.10)" },
  ".cm-activeLineGutter": {
    background: "rgba(127, 127, 127, 0.10)",
    color: "var(--pvf-text-primary)",
  },
  ".cm-content": { padding: "0", lineHeight: "20px", caretColor: "var(--pvf-text-primary)" },
  ".cm-line": { padding: "0", lineHeight: "20px" },
  ".cm-cursor": { borderLeftWidth: "1px" },
  "&.cm-focused": { outline: "none" },
  // ── Ctrl+F 查找面板外观（DOM 仍是官方 SearchPanel，这里只覆盖样式）──────────
  ".cm-panel.cm-search": {
    display: "flex",
    flexWrap: "wrap",
    alignItems: "center",
    gap: "6px 8px",
    padding: "7px 10px",
    borderTop: "1px solid rgb(127 127 127 / 25%)",
    background: "rgb(127 127 127 / 12%)",
    backdropFilter: "blur(3px)",
    fontFamily: "inherit",
    fontSize: "12px",
  },
  // `<br>` 在 flex 容器里不会换行，给它占满一行来把「替换」那排分开
  ".cm-panel.cm-search br": { flexBasis: "100%", height: "0" },
  ".cm-panel.cm-search .cm-textfield": {
    minWidth: "150px",
    padding: "3px 8px",
    border: "1px solid rgb(127 127 127 / 35%)",
    borderRadius: "5px",
    background: "transparent",
    color: "inherit",
    fontFamily: "inherit",
    fontSize: "12px",
    outline: "none",
  },
  ".cm-panel.cm-search .cm-textfield:focus": {
    borderColor: "var(--pvf-text-primary)",
  },
  ".cm-panel.cm-search .cm-button": {
    padding: "3px 10px",
    border: "1px solid rgb(127 127 127 / 35%)",
    borderRadius: "5px",
    // 官方按钮自带渐变底/内阴影，清掉换成平面胶囊
    background: "transparent",
    backgroundImage: "none",
    boxShadow: "none",
    color: "inherit",
    fontFamily: "inherit",
    fontSize: "12px",
    cursor: "pointer",
  },
  ".cm-panel.cm-search .cm-button:hover": {
    background: "rgb(127 127 127 / 18%)",
  },
  ".cm-panel.cm-search label": {
    display: "inline-flex",
    alignItems: "center",
    gap: "4px",
    whiteSpace: "nowrap",
    color: "var(--pvf-text-faint)",
    cursor: "pointer",
  },
  ".cm-panel.cm-search input[type=checkbox]": {
    margin: "0",
    accentColor: "var(--pvf-accent, #4a8cff)",
    cursor: "pointer",
  },
  // 关闭键是面板最后一个子元素：推到最右，做成图标位
  ".cm-panel.cm-search button[name=close]": {
    marginLeft: "auto",
    padding: "2px 7px",
    border: "none",
    background: "transparent",
    backgroundImage: "none",
    boxShadow: "none",
    borderRadius: "5px",
    color: "var(--pvf-text-faint)",
    fontSize: "15px",
    lineHeight: "1",
    cursor: "pointer",
  },
  ".cm-panel.cm-search button[name=close]:hover": {
    background: "rgb(127 127 127 / 18%)",
    color: "inherit",
  },
});

function makeExtensions(): Extension[] {
  return [
    largeWindowTheme,
    // 行号显示**文件真实行号**（窗口只装了其中一段，所以用 winStart 换算）
    lineNumbers({ formatNumber: (lineNo) => String(winStart.value + lineNo - 1) }),
    highlightActiveLineGutter(),
    highlightActiveLine(),
    drawSelection(),
    highlightSelectionMatches(),
    // 段内搜索（Ctrl+F）：只在当前视口这几千行里找，不碰后端、不影响秒开
    search(),
    // 面板文案中文化（官方 phrases 机制；只影响文字，不改行为）
    searchPanelPhrases,
    windowAnnotationField,
    // PVF 语法着色（与普通编辑器同一套语言与高亮规则）
    pvfLanguage.extension,
    pvfHighlighting,
    history(),
    keymap.of([
      { key: "Mod-s", run: () => (void saveNow(), true) },
      ...defaultKeymap,
      ...historyKeymap,
      ...searchKeymap,
    ]),
    // Ctrl/Cmd+单击清单里的路径 → 打开目标文件（大文件多半就是清单，这条最实用）
    EditorView.domEventHandlers({
      click(event, currentView) {
        const mouseEvent = event as MouseEvent;
        if (!mouseEvent.ctrlKey && !mouseEvent.metaKey) return false;
        if (!isListFile.value) return false;
        const position = currentView.posAtCoords({
          x: mouseEvent.clientX,
          y: mouseEvent.clientY,
        });
        if (position === null) return false;
        const linkPath = listLinkAt(currentView, position);
        if (!linkPath) return false;
        mouseEvent.preventDefault();
        mouseEvent.stopPropagation();
        void resolveListLinkIndex(linkPath).then((fileIndex) => {
          if (fileIndex < 0) return;
          emit("activate-reference", fileIndex);
        });
        return true;
      },
    }),
    // 绝不折行：折行会破坏「行号 × 20px」的虚拟定位
    EditorView.updateListener.of((update) => {
      if (!update.docChanged) return;
      // 程序换窗（带 remoteWindowChange 标记）不是用户编辑：跳过，不记待写回段。
      if (update.transactions.some((tr) => tr.annotation(remoteWindowChange) === true)) return;
      const text = update.state.doc.toString();
      winText.value = text;
      winCount.value = countLines(text);
      // 只记在前端；用户没点保存之前绝不写回归档（本组件铁律）。
      editor.setPendingLargeSegment(props.index, winStart.value, winCount.value, text);
      // 改动后标注要跟着重算（否则插一行会让绿色标签/虚线整体错开一行）。
      scheduleAnnotationRefresh();
    }),
    editableCompartment.of(EditorView.editable.of(editable.value)),
  ];
}

/** 把窗口内容整体换成语义（滚动换窗 / 叠加未写回段后调用）。带「程序换窗」注解。 */
function applyCmDoc(text: string): void {
  if (!cmView) return;
  if (cmView.state.doc.toString() === text) return;
  cmView.dispatch({
    changes: { from: 0, to: cmView.state.doc.length, insert: text },
    annotations: remoteWindowChange.of(true),
  });
}

/**
 * 取一次段内注解（中文名 / 关联标记）。
 *
 * **必须传窗口当前显示的文本**：按归档文本算的话，用户插一行就会让标注位置整体
 * 错开一行（绿色标签与虚线下划线错位）。只算这一段（约 130KB），与文件多大无关。
 */
let annotationSeq = 0;
async function loadAnnotations(): Promise<void> {
  if (!cmView) return;
  const seq = ++annotationSeq;
  const text = cmView.state.doc.toString();
  try {
    const list = await GetWindowAnnotations(props.index, text);
    if (seq !== annotationSeq) return;
    // 期间用户又改了：等下一次刷新，别把过期位置画上去。
    if (cmView.state.doc.toString() !== text) return;
    cmView.dispatch({ effects: setWindowAnnotations.of(list ?? []) });
  } catch {
    // 注解取不到就当作没有：正文照常可用，不弹错误打扰用户。
  }
}

/** 打字停顿后重算注解：新加的行也要有自己的标注（400ms 合并连续输入）。 */
let annotationTimer: ReturnType<typeof setTimeout> | undefined;
function scheduleAnnotationRefresh(): void {
  if (annotationTimer !== undefined) clearTimeout(annotationTimer);
  annotationTimer = setTimeout(() => {
    annotationTimer = undefined;
    void loadAnnotations();
  }, 400);
}

// 窗口内容 / 可编辑态变化 → 同步进 CodeMirror（用户打字走 updateListener，不会绕回来）
watch([winText, editable], ([text]) => {
  applyCmDoc(text);
  void loadAnnotations();
});
watch(editable, (value) => {
  cmView?.dispatch({ effects: editableCompartment.reconfigure(EditorView.editable.of(value)) });
});

/**
 * 本标签「改了但还没写回归档」的段数。
 *
 * ⚠ 本组件的铁律（用户 2026-09-30 明确要求）：**绝不自动保存**。
 * 打字、失焦、滚动、切标签，都只把改动记在前端（`editor.pendingLargeSegments`）；
 * 只有用户点「保存本段」或按 Ctrl+S，才写回归档内存（再「保存 PVF」才落盘）。
 * 打一半的字被自动写进去是不允许的 —— 之前那版会在失焦/滚动时自动写回，已被移除。
 */
const pendingCount = computed(() => editor.pendingSegmentsOf(props.index).length);

const spacerHeight = computed(() => `${Math.max(totalLines.value, 1) * ROW_HEIGHT}px`);
const winTop = computed(() => `${(winStart.value - 1) * ROW_HEIGHT}px`);
const winHeight = computed(() => `${Math.max(winCount.value, 1) * ROW_HEIGHT}px`);

/** 与后端 countLines 同口径：空串 0 行；末行无换行符也算一行。 */
function countLines(text: string): number {
  if (text === "") return 0;
  const breaks = (text.match(/\n/g) ?? []).length;
  return text.endsWith("\n") ? breaks : breaks + 1;
}

let fetchSeq = 0;

async function loadWindow(startLine: number): Promise<void> {
  const seq = ++fetchSeq;
  loading.value = true;
  try {
    const chunk = await GetFileLines(props.index, startLine, CHUNK);
    if (!chunk || seq !== fetchSeq) return;
    totalLines.value = chunk.lines;
    editable.value = chunk.editable;
    winStart.value = chunk.start;
    // 先把归档内容显示出来：即使下面"叠加未写回段"这一步出问题，也不至于整块空白。
    winCount.value = chunk.count;
    winText.value = chunk.text;
    // 这一段若用户改过、还没写回归档，就显示用户自己的内容（滚走再滚回来也还在）。
    try {
      const pending = editor
        .pendingSegmentsOf(props.index)
        .find((segment) => segment.start === chunk.start);
      if (pending) {
        winCount.value = pending.count;
        winText.value = pending.text;
      }
    } catch {
      // store 版本落后（热更新只换了组件）等情况：忽略叠加，保持可用。
    }
  } catch (error: any) {
    message.error(`读取第 ${startLine} 行起的内容失败：${error?.message ?? error}`);
  } finally {
    if (seq === fetchSeq) loading.value = false;
  }
}

/**
 * Ctrl+S：与普通文件完全一致 —— 把本标签的改动写进归档内存（再由工具栏「保存 PVF」落盘）。
 *
 * 实现上直接调 `editor.saveTab`：它内部会先把 TXT 视图的"待写回段"落到归档内存，
 * 因此 TXT 视图不再需要单独一个「保存本段」按钮（用户 2026-09-30 要求合并掉那一步）。
 */
async function saveNow(): Promise<void> {
  if (pendingCount.value === 0) return;
  saving.value = true;
  try {
    const saved = await editor.saveTab(props.index);
    if (!saved) return;
    await archive.refreshInfo();
    // 写回后把窗口重新对齐到同一位置：用户看到的就是刚写进去的内容。
    await loadWindow(winStart.value);
    message.success("已写回归档内存，请点工具栏「保存 PVF」落盘");
  } catch (error: any) {
    message.error(`写回归档失败：${error?.message ?? error}`);
  } finally {
    saving.value = false;
  }
}

/** 后端交互串行化：滚动容易连发请求。 */
let queue: Promise<unknown> = Promise.resolve();
function enqueue(task: () => Promise<unknown>): void {
  queue = queue.then(task).catch(() => undefined);
}

function visibleRange(): { start: number; end: number } {
  const el = viewport.value;
  if (!el) return { start: 1, end: CHUNK };
  const first = Math.floor(el.scrollTop / ROW_HEIGHT) + 1;
  const rows = Math.ceil(el.clientHeight / ROW_HEIGHT);
  return { start: Math.max(1, first - OVERSCAN), end: first + rows + OVERSCAN };
}

// ---------------------------------------------------------------------------
// 标签滚动位置记忆（与 CodeEditor 同一套做法，背景见那边的说明）：
// 标签切换用 v-show，隐藏时 display:none 会把 viewport.scrollTop 归零；
// 这里不涉及窗口数据（winStart/winText 都还在），只是把滚动位置写回去。
// ---------------------------------------------------------------------------
/** 上次可见时的滚动位置。 */
let savedScrollTop = 0;
/** 最近一次「定位跳转」的时间戳：紧跟着的标签显示不要抢滚动。 */
let lastRevealAt = 0;

let raf = 0;
function onScroll(): void {
  // 先记下位置（标签隐藏期间不记：那时 scrollTop 已被 display:none 归零）。
  const el = viewport.value;
  if (el && props.active !== false) savedScrollTop = el.scrollTop;
  if (raf) return;
  raf = requestAnimationFrame(() => {
    raf = 0;
    const need = visibleRange();
    const haveStart = winStart.value;
    const haveEnd = winStart.value + winCount.value;
    if (need.start >= haveStart && need.end <= haveEnd) return;
    // 滚动只换窗口、不写归档：改过的段留在前端（loadWindow 会把它显示回来）。
    enqueue(async () => {
      await loadWindow(need.start);
    });
  });
}

/** 供搜索定位使用：跳到指定行（只换窗口，不写归档）。 */
async function gotoLine(line: number): Promise<void> {
  lastRevealAt = performance.now();
  enqueue(async () => {
    await loadWindow(Math.max(1, line - 50));
  });
  const el = viewport.value;
  if (el) el.scrollTop = Math.max(0, (Math.max(1, line) - 1) * ROW_HEIGHT);
}

watch(
  () => props.active,
  (active) => {
    if (!active) return;
    // 定位请求（搜索 / AI 跳转）驱动的显示：别抢它的滚动。
    if (performance.now() - lastRevealAt < 300) return;
    const top = savedScrollTop;
    if (top <= 0) return;
    requestAnimationFrame(() => {
      const el = viewport.value;
      if (!el || props.active === false) return;
      el.scrollTop = top;
    });
  },
  { flush: "post" }
);

defineExpose({ revealLine: gotoLine });

// 搜索/AI 定位：跳到大文件的目标行（连续视图里就是滚动 + 加载那一段）。
watch(
  () => props.reveal?.seq,
  () => {
    const line = props.reveal?.line;
    if (line && line > 0) void gotoLine(line);
  }
);

onMounted(() => {
  // 挂载视口编辑器（实测毫秒级，见文件顶部说明），再加载首屏。
  if (cmHost.value && !cmView) {
    cmView = new EditorView({
      state: EditorState.create({ doc: "", extensions: makeExtensions() }),
      parent: cmHost.value,
    });
    applyCmDoc(winText.value);
  }
  void loadWindow(1);
});

onBeforeUnmount(() => {
  // 销毁视图即可；未写回的段留在 store 里（切标签不丢），
  // 关标签 / 关窗口会由确认框提示（见 editor.hasPendingLargeEdits / useUnsavedChanges）。
  if (annotationTimer !== undefined) {
    clearTimeout(annotationTimer);
    annotationTimer = undefined;
  }
  cmView?.destroy();
  cmView = null;
});
</script>

<template>
  <div class="lsc-root">
    <div class="lsc-bar">
      <NTag size="tiny" :bordered="false" type="warning">TXT 模式</NTag>
      <span class="lsc-hint">
        连续全文 {{ totalLines.toLocaleString() }} 行 · 文本留在后端，滚到哪取到哪
      </span>
      <span class="lsc-gap" />
      <span v-if="saving" class="lsc-state">写回归档中…</span>
      <span v-else-if="pendingCount > 0" class="lsc-state lsc-state--dirty">
        有 {{ pendingCount }} 段改动未保存（按 Ctrl+S 或点上方的「保存」按钮）
      </span>
      <span v-else-if="loading" class="lsc-state">加载中…</span>
    </div>

    <div ref="viewport" class="lsc-viewport" @scroll="onScroll">
      <div class="lsc-spacer" :style="{ height: spacerHeight }">
        <div
          ref="cmHost"
          class="lsc-window"
          :style="{ top: winTop, height: winHeight }"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.lsc-root {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
}
.lsc-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  padding: 4px 8px;
  border-bottom: 1px solid var(--pvf-border-subtle, rgba(255, 255, 255, 0.08));
}
.lsc-hint,
.lsc-state {
  color: var(--pvf-text-secondary);
  font-size: 12px;
}
.lsc-state--dirty {
  color: var(--pvf-warning);
}
.lsc-gap {
  flex: 1;
}
/* 滚动容器：整个文件的高度由里面的 spacer 撑开 */
.lsc-viewport {
  flex: 1;
  min-height: 0;
  overflow: auto;
  position: relative;
}
.lsc-spacer {
  position: relative;
  width: 100%;
}

/* 视口窗口：只有这几百行真的在 DOM 里（现在是每视口一个 CodeMirror） */
.lsc-window {
  position: absolute;
  left: 0;
  right: 0;
  overflow: hidden;
  background: transparent;
}
/* CodeMirror 的行必须严格等于 ROW_HEIGHT（20px）、不折行，否则虚拟定位会漂移 */
.lsc-window :deep(.cm-editor) {
  height: auto;
  background: transparent;
  color: var(--pvf-text-primary);
  font-family: "SF Mono", Menlo, Consolas, "Courier New", monospace;
  font-size: 13px;
}
.lsc-window :deep(.cm-scroller) {
  overflow: hidden;
  line-height: 20px;
}
.lsc-window :deep(.cm-content) {
  padding: 0;
  white-space: pre;
  tab-size: 4;
}
.lsc-window :deep(.cm-line) {
  padding: 0 10px;
  line-height: 20px;
}
.lsc-window :deep(.cm-cursor) {
  border-left-color: var(--pvf-text-primary);
}
.lsc-window :deep(.cm-selectionBackground) {
  background: rgba(90, 140, 220, 0.35) !important;
}
/* 段内注解：关联标记 + 中文名标签（与普通编辑器的绿色标签同一观感） */
.lsc-window :deep(.lsc-ann) {
  border-bottom: 1px dashed rgba(122, 200, 140, 0.75);
  background: rgba(122, 200, 140, 0.08);
}
.lsc-window :deep(.lsc-ann-tag) {
  margin-left: 6px;
  padding: 0 4px;
  border-radius: 3px;
  font-size: 11px;
  color: var(--pvf-text-secondary);
  background: rgba(122, 200, 140, 0.14);
}
</style>
