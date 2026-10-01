/**
 * Lua 语法着色 —— **仅用于 `.lua` 文件**。
 *
 * 背景（2026-10-01 实测）：110 版 PVF 的 AI 脚本是标准 Lua 明文
 * （`.../monster/.../ai/action.lua`、`aicharacter/pvp/.../ai/movecommand.lua` 等，
 * 共 2054 个）；老版 DNF 用的 `.nut`（Squirrel）在 110 归档里已是 0 个。
 *
 * 边界（用户 2026-10-01 明确要求）：**只影响 `.lua`，不影响任何别的文件**。
 * 挂载与否由 `EditorPane.vue` 的 `editorLanguage()` 严格按扩展名判定 ——
 * 只有路径以 `.lua` 结尾才传 `language="lua"`；其余文件一律走 `pvfLanguage`，
 * 着色与行为与以前完全一致。本文件不导任何副作用、不改全局状态。
 *
 * 实现沿用 `pvfLanguage.ts` 的同一手法（StreamLanguage），零新增依赖。
 */
import {
  HighlightStyle,
  StreamLanguage,
  StringStream,
  syntaxHighlighting,
} from "@codemirror/language";
import { tags } from "@lezer/highlight";

const KEYWORDS = new Set([
  "and",
  "break",
  "do",
  "else",
  "elseif",
  "end",
  "for",
  "function",
  "goto",
  "if",
  "in",
  "local",
  "not",
  "or",
  "repeat",
  "return",
  "then",
  "until",
  "while",
]);

/** nil / true / false 在 Lua 里也是关键字，但配色上与流程关键字分开（借 atom）。 */
const ATOMS = new Set(["nil", "true", "false"]);

/** Lua 标准库 + DNF 110 AI 脚本里高频出现的全局对象（只影响配色，不参与语义）。 */
const BUILTINS = new Set([
  "print",
  "type",
  "tostring",
  "tonumber",
  "pairs",
  "ipairs",
  "next",
  "select",
  "pcall",
  "xpcall",
  "error",
  "assert",
  "setmetatable",
  "getmetatable",
  "rawget",
  "rawset",
  "rawequal",
  "rawlen",
  "require",
  "collectgarbage",
  "unpack",
  "load",
  "loadstring",
  "math",
  "string",
  "table",
  "io",
  "os",
  "coroutine",
  "debug",
  "utf8",
  "self",
  "_G",
  "_VERSION",
  // 实测自 escapecommand.lua / action.lua 等
  "AIBridge",
  "Idle",
  "Battle",
  "EnemyInfo",
  "MyInfo",
  "State",
  "ChangeCommand",
  // ---- DNF 110 AI 全局 API ----------------------------------------------
  // 2026-10-01 对全部 **2054 个 .lua** 实测选出：括号里是「出现在多少个文件里」，
  // 只收跨文件共享的 API 对象/函数，不收各文件自建的东西。
  "Common", // 1640 个文件
  "AIBaseScripts", // 1388
  "Patrol", // 1326
  "MoveMethod", // 295
  "SkillDifficulty", // 176
  "AIBaseScriptsNew", // 159
  "DestinationSelect", // 113
  "Buff", // 58
  "Pvp", // 13
  "AIEvent", // 4
  "UtilBridge", // 2
  "setDefault", // 1555（全局函数）
  "GetCommand", // 262
  "BindCommand", // 85
  "_A", // 72（按等级换算数值的辅助函数，如 _A(180)）
  "_ALERT", // 3
  // 刻意排除（实测验证过，收了反而会把局部/属性错当成 API）：
  //   L            —— 各文件自建：`local L = {}`
  //   ObjectInfo   —— 是属性：`myObjInfo = MyInfo.ObjectInfo`
]);

type LuaMode = "normal" | "longstring" | "longcomment";

interface LuaState {
  mode: LuaMode;
  /** 长括号的等号层数：`[[`=0、`[=[`=1 ……，收尾必须是同层 `]=*]`。 */
  level: number;
}

/**
 * 尝试吃掉一个 Lua 长括号开头（`[[` / `[=[` / `[==[` …）。
 * 命中返回等号层数；未命中回退到原位置并返回 -1（普通下标 `t[1]` 会走这条）。
 */
function eatLongOpen(stream: StringStream): number {
  if (stream.peek() !== "[") return -1;
  const start = stream.pos;
  stream.next();
  let level = 0;
  while (stream.eat("=")) level++;
  if (stream.eat("[")) return level;
  stream.pos = start;
  return -1;
}

/** 读到同层的 `]=*]` 为止；跨行时状态留在 state 上，下一行接着读。 */
function consumeLong(stream: StringStream, state: LuaState, style: string): string {
  const closer = "]" + "=".repeat(state.level) + "]";
  while (!stream.eol()) {
    if (stream.match(closer)) {
      state.mode = "normal";
      break;
    }
    stream.next();
  }
  return style;
}

export const luaLanguage = StreamLanguage.define<LuaState>({
  name: "lua",
  startState: () => ({ mode: "normal", level: 0 }),
  // 自定义样式名（StreamParser.tokenTable）：后面跟 `(` 的调用名。
  tokenTable: {
    luaFunction: tags.function(tags.variableName),
  },
  token(stream, state) {
    // 跨行的长字符串 / 长注释：接着上一行读。
    if (state.mode === "longstring") return consumeLong(stream, state, "string");
    if (state.mode === "longcomment") return consumeLong(stream, state, "comment");

    if (stream.eatSpace()) return null;
    if (stream.eol()) return null;

    // 注释：`--` 行注释；`--[[` / `--[=[` 长注释（可跨行）。
    if (stream.match("--")) {
      const level = eatLongOpen(stream);
      if (level >= 0) {
        state.mode = "longcomment";
        state.level = level;
        return consumeLong(stream, state, "comment");
      }
      stream.skipToEnd();
      return "comment";
    }

    // 长字符串：`[[ ... ]]` / `[=[ ... ]=]`
    if (stream.peek() === "[") {
      const level = eatLongOpen(stream);
      if (level >= 0) {
        state.mode = "longstring";
        state.level = level;
        return consumeLong(stream, state, "string");
      }
    }

    // 引号字符串（`\` 转义；Lua 的短字符串不跨行）
    const quote = stream.peek();
    if (quote === '"' || quote === "'") {
      stream.next();
      let escaped = false;
      while (!stream.eol()) {
        const ch = stream.next() as string;
        if (escaped) escaped = false;
        else if (ch === "\\") escaped = true;
        else if (ch === quote) break;
      }
      return "string";
    }

    // 数字：0x1F / 3.14 / .5 / 1e10
    if (
      stream.match(/^0[xX][0-9a-fA-F]+/) ||
      stream.match(/^(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?/)
    ) {
      return "number";
    }

    // 标识符：原子 / 关键字 / 内建 / 函数调用名 / 普通
    // （普通标识符刻意不上色，保持正文可读。**这里只决定配色，不改变任何字符**）
    if (stream.match(/^[A-Za-z_]\w*/)) {
      const word = stream.current();
      if (ATOMS.has(word)) return "atom";
      if (KEYWORDS.has(word)) return "keyword";
      if (BUILTINS.has(word)) return "builtin";
      // 往前窥探一个非空白字符是不是 `(`：是则按「函数调用名」配色。
      // 注意：只是窥探，必须把位置还原到词尾，否则会吃掉后面的字符
      //（那将直接破坏正文 —— 与本功能的「只上色、不动内容」原则相悖）。
      const wordEnd = stream.pos;
      stream.eatWhile(/\s/);
      const isCall = stream.peek() === "(";
      stream.pos = wordEnd;
      return isCall ? "luaFunction" : null;
    }

    // 运算符：多字符必须优先于单字符（`..`/`...` 先于 `.`）
    if (stream.match(/^(?:\.\.\.|\.\.|==|~=|<=|>=|<<|>>|\/\/)/)) return "operator";
    if (stream.match(/^[+\-*/%^#&~|<>=]/)) return "operator";
    if (stream.match(/^[(){}\[\];:,.]/)) return "punctuation";

    stream.next();
    return null;
  },
});

/**
 * 配色沿用普通编辑器的 CSS 变量（自动跟随主题，不写死颜色）。
 * `builtin` 是 StreamLanguage 的旧式样式名，映射到 `variableName.standard`。
 */
const luaHighlightStyle = HighlightStyle.define([
  { tag: tags.comment, color: "var(--pvf-text-faint)", fontStyle: "italic" },
  { tag: tags.string, color: "var(--pvf-editor-syntax-string)" },
  { tag: [tags.number, tags.atom], color: "var(--pvf-editor-syntax-number)" },
  {
    tag: tags.keyword,
    color: "var(--pvf-editor-syntax-heading)",
    fontWeight: "600",
  },
  {
    tag: tags.standard(tags.variableName),
    color: "var(--pvf-editor-syntax-heading)",
  },
  // 函数调用名：`math.abs(...)` / `AIBridge:getObjectInfo(...)` / `setDefault(...)`
  { tag: tags.function(tags.variableName), color: "var(--pvf-editor-syntax-heading)" },
  { tag: tags.operator, color: "var(--pvf-text-secondary)" },
  { tag: tags.punctuation, color: "var(--pvf-text-muted)" },
]);

export const luaHighlighting = syntaxHighlighting(luaHighlightStyle);
