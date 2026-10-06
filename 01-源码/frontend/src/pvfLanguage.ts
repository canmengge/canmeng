import {
  HighlightStyle,
  StreamLanguage,
  StringStream,
  syntaxHighlighting,
} from "@codemirror/language";
import { tags } from "@lezer/highlight";

type PvfMode = "normal" | "string" | "section";

interface PvfState {
  mode: PvfMode;
}

const numberPattern = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/;

function consumeString(stream: StringStream, state: PvfState): string {
  while (!stream.eol()) {
    const ch = stream.next();
    if (ch !== "`") continue;

    // PVF escapes a literal backtick by doubling it.
    if (stream.peek() === "`") {
      stream.next();
      continue;
    }
    state.mode = "normal";
    break;
  }
  return "string";
}

function consumeSection(stream: StringStream, state: PvfState): string {
  while (!stream.eol()) {
    if (stream.next() === "]") {
      state.mode = "normal";
      break;
    }
  }
  return "heading";
}

function isTokenBoundary(ch: string): boolean {
  return /\s/.test(ch) || "`[]{}=,;".includes(ch);
}

export const pvfLanguage = StreamLanguage.define<PvfState>({
  name: "pvf",
  // A11：显式映射，不赌 StreamLanguage 的默认 token 名表
  tokenTable: { lineComment: tags.lineComment },
  startState: () => ({ mode: "normal" }),
  token(stream, state) {
    if (state.mode === "string") return consumeString(stream, state);
    if (state.mode === "section") return consumeSection(stream, state);

    if (stream.eatSpace()) return null;

    if (stream.eat("#")) {
      // Comments are intentionally unstyled, but their contents must not be
      // mistaken for numbers or other tokens.
      stream.skipToEnd();
      return null;
    }

    // A11（2026-10-06，对照 UT `Script.xshd` / `Lst.xshd` / `Kor.xshd`）：
    // PVF 家族的注释是 `//`（`#` 只是行头分隔，历史上有意不上色、这里保持不动）。
    // 整行吃掉 ⇒ 注释里的内容不会被误判成数字/字符串；样式走 `tags.lineComment`
    // （极轻斜体，见下方 highlightStyle 与 tokenTable 的显式映射）。
    // 标记：pvfSyntaxA11_20261006
    if (stream.match("//")) {
      stream.skipToEnd();
      return "lineComment";
    }

    // A11：KOR 尖括号串 `<...>`（UT `Script.xshd:29-49` / `Kor.xshd:21-23` 的专用着色）。
    // 只认**同一行内闭合**的 `<>`；孤立的 `<` 不吞整行（PVF 文本里 `<` 罕见，但别赌）。
    if (stream.match(/^<[^>\n]*>/)) {
      return "string";
    }

    if (stream.eat("`")) {
      state.mode = "string";
      return consumeString(stream, state);
    }

    if (stream.eat("[")) {
      state.mode = "section";
      return consumeSection(stream, state);
    }

    const start = stream.pos;
    stream.eatWhile((ch) => !isTokenBoundary(ch));
    if (stream.pos === start) {
      stream.next();
      return null;
    }

    return numberPattern.test(stream.current()) ? "number" : null;
  },
});

const pvfHighlightStyle = HighlightStyle.define([
  { tag: tags.number, color: "var(--pvf-editor-syntax-number)" },
  { tag: tags.string, color: "var(--pvf-editor-syntax-string)" },
  {
    tag: tags.heading,
    color: "var(--pvf-editor-syntax-heading)",
    fontWeight: "600",
  },
  // A11：`//` 注释按"不上色"处理（与 `#` 的既有约定一致）—— 这里只给一个极轻的
  // 斜体，让注释在满屏数据里可辨但不抢眼；不引入新的主题变量，避免主题漂移。
  { tag: tags.lineComment, fontStyle: "italic", opacity: "0.75" },
]);

export const pvfHighlighting = syntaxHighlighting(pvfHighlightStyle);
