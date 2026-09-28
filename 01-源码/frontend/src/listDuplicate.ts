/**
 * .lst 清单查重（只针对 list\ 目录下的 .lst 条目行）。
 *
 * 条目行形如：`10018	`equipment/character/common/jacket/cloth/vest_owool.equ``
 * （ID + 空白 + 反引号包裹的路径）。三类问题与提示语由用户 2026-09-28 指定：
 *   ① ID 相同、路径不同   → ID重复，请检查后修改！
 *   ② ID 不同、路径相同   → 路径重复，请检查后修改！
 *   ③ ID 相同、路径相同   → 兄弟，你列表加重复了，给我检查好了啊！
 */

export interface ListEntry {
  /** 1 基行号（用于展示与定位）。 */
  line: number;
  id: string;
  path: string;
  /** 原始行文本（复制用）。 */
  raw: string;
}

export type ListDuplicateKind = "id" | "path" | "both";

export interface ListDuplicateIssue {
  kind: ListDuplicateKind;
  message: string;
  /** 涉及的行（按行号升序）。 */
  entries: ListEntry[];
}

export const LIST_DUPLICATE_MESSAGES: Record<ListDuplicateKind, string> = {
  id: "ID重复，请检查后修改！",
  path: "路径重复，请检查后修改！",
  both: "兄弟，你列表加重复了，给我检查好了啊！",
};

function pushTo<K>(map: Map<K, ListEntry[]>, key: K, entry: ListEntry): void {
  const list = map.get(key);
  if (list) list.push(entry);
  else map.set(key, [entry]);
}

/**
 * 解析 .lst 文本：跳过空行与注释行（`;` / `//` / `#`）。
 * 路径优先取反引号内内容，其次单/双引号，最后退回第一个空白分隔 token。
 */
export function parseListEntries(text: string): ListEntry[] {
  const entries: ListEntry[] = [];
  const lines = text.split(/\r?\n/);
  for (let i = 0; i < lines.length; i += 1) {
    const raw = lines[i];
    const trimmed = raw.trim();
    if (!trimmed || trimmed.startsWith(";") || trimmed.startsWith("//") || trimmed.startsWith("#")) {
      continue;
    }
    const head = /^(\S+)\s+(.+)$/.exec(trimmed);
    if (!head) continue;
    const id = head[1];
    const rest = head[2].trim();
    let path = "";
    const tick = /^`([^`]*)`/.exec(rest);
    if (tick) {
      path = tick[1];
    } else {
      const quote = /^"([^"]*)"/.exec(rest) ?? /^'([^']*)'/.exec(rest);
      path = quote ? quote[1] : (rest.split(/\s+/)[0] ?? "");
    }
    path = path.trim();
    if (!path) continue;
    entries.push({ line: i + 1, id, path, raw });
  }
  return entries;
}

/** 查重：完全重复 / ID 重复 / 路径重复三类，按出现行号排序。 */
export function findListDuplicates(entries: ListEntry[]): ListDuplicateIssue[] {
  const issues: ListDuplicateIssue[] = [];
  const byPair = new Map<string, ListEntry[]>();
  const byId = new Map<string, ListEntry[]>();
  const byPath = new Map<string, ListEntry[]>();

  for (const entry of entries) {
    pushTo(byPair, `${entry.id}\u0000${entry.path}`, entry);
    pushTo(byId, entry.id, entry);
    pushTo(byPath, entry.path, entry);
  }

  // ③ ID 与路径都相同：整行重复。
  for (const group of byPair.values()) {
    if (group.length > 1) {
      issues.push({ kind: "both", message: LIST_DUPLICATE_MESSAGES.both, entries: group });
    }
  }

  // ① 同一个 ID 指向了多个不同路径（路径全同的情况已由 ③ 覆盖）。
  for (const group of byId.values()) {
    if (group.length < 2) continue;
    if (new Set(group.map((entry) => entry.path)).size > 1) {
      issues.push({ kind: "id", message: LIST_DUPLICATE_MESSAGES.id, entries: group });
    }
  }

  // ② 同一个路径被多个不同 ID 指向（ID 全同的情况已由 ③ 覆盖）。
  for (const group of byPath.values()) {
    if (group.length < 2) continue;
    if (new Set(group.map((entry) => entry.id)).size > 1) {
      issues.push({ kind: "path", message: LIST_DUPLICATE_MESSAGES.path, entries: group });
    }
  }

  for (const issue of issues) {
    issue.entries.sort((a, b) => a.line - b.line);
  }
  issues.sort((a, b) => a.entries[0].line - b.entries[0].line);
  return issues;
}
