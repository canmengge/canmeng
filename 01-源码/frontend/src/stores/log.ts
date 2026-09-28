import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { Events } from "@wailsio/runtime";
import { Clear, History, type LogEntry } from "../services/logApi";

/** 面板里最多保留的行数（超出丢最旧，避免长时间运行吃内存）。 */
const MAX_ENTRIES = 2000;
const MIN_HEIGHT = 80;
const MAX_HEIGHT = 620;
const HEIGHT_KEY = "pvfine.logPanel.height";
const VISIBLE_KEY = "pvfine.logPanel.visible";

const levelRank: Record<string, number> = {
  DEBUG: 0,
  INFO: 1,
  WARN: 2,
  ERROR: 3,
};

/** 过滤级别："all" 表示不过滤，否则只显示 >= 该级别的行。 */
export type LogLevelFilter = "all" | "info" | "warn" | "error";

const filterRank: Record<LogLevelFilter, number> = {
  all: -1,
  info: 1,
  warn: 2,
  error: 3,
};

function readNumber(key: string, fallback: number): number {
  const raw = window.localStorage.getItem(key);
  const value = raw === null ? NaN : Number(raw);
  return Number.isFinite(value) ? value : fallback;
}

function readBoolean(key: string, fallback: boolean): boolean {
  const raw = window.localStorage.getItem(key);
  if (raw === null) return fallback;
  return raw === "1";
}

/**
 * 「输出日志」面板状态。
 *
 * 数据来源是后端日志系统本身（`app:log` 事件 + `LogService.History` 补历史），
 * 因此面板里看到的与 HC\logs\pvfine-*.log 完全同源。
 */
export const useLogStore = defineStore("log", () => {
  const entries = ref<LogEntry[]>([]);
  const visible = ref(readBoolean(VISIBLE_KEY, true));
  const height = ref(
    Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, readNumber(HEIGHT_KEY, 190))),
  );
  const levelFilter = ref<LogLevelFilter>("all");
  const query = ref("");
  /** 暂停 = 停止跟进新日志（已显示的内容保留），便于翻看历史。 */
  const paused = ref(false);
  const unread = ref(0);

  const filtered = computed(() => {
    const min = filterRank[levelFilter.value];
    const needle = query.value.trim().toLowerCase();
    return entries.value.filter((entry) => {
      if (min >= 0 && (levelRank[entry.level] ?? 1) < min) return false;
      if (!needle) return true;
      const haystack = `${entry.module} ${entry.message} ${entry.fields ?? ""}`.toLowerCase();
      return haystack.includes(needle);
    });
  });

  const errorCount = computed(
    () => entries.value.filter((entry) => entry.level === "ERROR").length,
  );

  function append(entry: LogEntry): void {
    if (paused.value) {
      unread.value += 1;
      return;
    }
    const next = entries.value.length >= MAX_ENTRIES
      ? [...entries.value.slice(entries.value.length - MAX_ENTRIES + 1), entry]
      : [...entries.value, entry];
    entries.value = next;
  }

  /** 拉取启动以来（订阅之前）的日志，与实时事件合并去重。 */
  async function loadHistory(): Promise<void> {
    try {
      const history = (await History()) ?? [];
      if (history.length === 0) return;
      const seen = new Set(entries.value.map((entry) => entry.text ?? `${entry.time}${entry.message}`));
      const merged = [...history.filter((entry) => !seen.has(entry.text ?? `${entry.time}${entry.message}`)), ...entries.value];
      merged.sort((left, right) => left.time.localeCompare(right.time));
      entries.value = merged.slice(-MAX_ENTRIES);
    } catch {
      // 历史补拉失败不影响实时事件。
    }
  }

  async function clear(): Promise<void> {
    entries.value = [];
    unread.value = 0;
    try {
      await Clear();
    } catch {
      // 忽略：本地已清空。
    }
  }

  function setHeight(value: number): void {
    const clamped = Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, Math.round(value)));
    height.value = clamped;
    window.localStorage.setItem(HEIGHT_KEY, String(clamped));
  }

  function toggle(): void {
    visible.value = !visible.value;
    window.localStorage.setItem(VISIBLE_KEY, visible.value ? "1" : "0");
    if (visible.value) {
      unread.value = 0;
      void loadHistory();
    }
  }

  function resume(): void {
    paused.value = false;
    unread.value = 0;
  }

  function eventData(event: any): any {
    return event?.data ?? event;
  }

  Events.On("app:log", (event: any) => {
    const data = eventData(event);
    if (!data) return;
    append({
      time: String(data.time ?? ""),
      level: String(data.level ?? "INFO").toUpperCase(),
      module: String(data.module ?? "-"),
      message: String(data.message ?? ""),
      fields: data.fields ? String(data.fields) : undefined,
      text: data.text ? String(data.text) : undefined,
    });
  });

  return {
    entries,
    filtered,
    visible,
    height,
    levelFilter,
    query,
    paused,
    unread,
    errorCount,
    append,
    loadHistory,
    clear,
    setHeight,
    toggle,
    resume,
  };
});
