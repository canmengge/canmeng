/**
 * Ctrl+F 文本查找面板：统一中文文案 + 外观（2026-10-06）。
 *
 * ## 为什么这么写
 *
 * 面板由 CodeMirror 官方 `SearchPanel` 渲染，默认是**英文 + 裸样式**。这里只用官方两个入口，
 * 不接管 DOM、不改交互逻辑（升级 CodeMirror 也不会坏）：
 *
 * 1. `EditorState.phrases` —— 官方文案翻译表。**键名 = `@codemirror/search` 源码里
 *    `phrase(view, "...")` 的原始字符串，不能改**（改了就不生效，退回英文）。
 * 2. `EditorView.theme` —— 主题样式覆盖。
 *
 * ## 面板 DOM（取自 `@codemirror/search` 源码，用于写选择器）
 *
 * ```
 * div.cm-panel.cm-search
 *   input.cm-textfield[name=search]
 *   button.cm-button[name=next|prev|select]
 *   label > input[type=checkbox][name=case|re|word]
 *   <br>                                 ← flex 容器里不换行，靠样式撑满一行
 *   input.cm-textfield[name=replace]
 *   button.cm-button[name=replace|replaceAll]
 *   button[name=close]                   ← 面板最后一个子元素，推到最右
 * ```
 *
 * ## 谁在用
 *
 * - `CodeEditor.vue`（普通文件，本轮新增 Ctrl+F 查找）
 * - `LargeScrollView.vue`（大文件视口，此前已有面板，本次改为共用本文件）
 *
 * 同一口径只写一处：两边都 import 这里，不要再各写一份。
 */
import { EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";

/** Ctrl+F 面板中文文案（键名是官方原始串，不可改）。 */
export const searchPanelPhrases = EditorState.phrases.of({
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

/** Ctrl+F 面板外观：圆角胶囊 + 主题无关半透明底（明暗主题都协调）。 */
export const searchPanelTheme = EditorView.theme({
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
