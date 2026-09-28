/**
 * 界面侧操作时间线。
 *
 * 用途：记录「打开列表 → 点击路径 → 打开目标文件 → 定位左树」每一步的时刻与耗时，
 * 界面卡死时在「输出日志」面板敲 SCRZ，把它和后端现场一起 dump 到日志文件
 * （日志面板本身独立线程渲染，卡死时仍可输入）。
 *
 * 约定：所有写操作都极廉价（一次 push + 两次 Date.now），不得影响正常操作性能。
 */
import { Events } from "@wailsio/runtime";

export interface UiTraceEntry {
  /** HH:mm:ss.mmm */
  at: string;
  step: string;
  /** 该步耗时（毫秒，未完成的步骤为空）。 */
  ms?: number;
  /** 未返回 true ⇒ 卡死时就是停在这一步。 */
  pending?: boolean;
  detail?: Record<string, unknown>;
}

const entries: UiTraceEntry[] = [];
const MAX_ENTRIES = 60;

// 换归档后旧时间线失去意义，整体作废（与 listNames 的名称/索引缓存同一策略）。
Events.On("archive:opened", () => {
  entries.length = 0;
});
Events.On("archive:closed", () => {
  entries.length = 0;
});

// 心跳：每秒把最新时间线推给 Go 一次。JS 主线程被卡住 ⇒ 心跳停止 ⇒
// Go 看门狗自动把「停摆开始时刻 + 最后操作时间线」写进日志（无需人工敲命令）。
setInterval(() => {
  void Events.Emit("dev:trace-sync", { lines: traceLines(30), heartbeat: Date.now() }).catch(() => {});
}, 1000);

function stamp(date = new Date()): string {
  const pad = (value: number, width = 2): string => String(value).padStart(width, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
}

function push(entry: UiTraceEntry): void {
  entries.push(entry);
  if (entries.length > MAX_ENTRIES) entries.splice(0, entries.length - MAX_ENTRIES);
  // 实时同步给 Go（fire-and-forget）：界面卡死时控制台 SCRZ 也能拿到时间线。
  void Events.Emit("dev:trace-sync", { lines: traceLines(30) }).catch(() => {});
}

/** 记录一个瞬时事件（如「点击路径」）。 */
export function markTrace(step: string, detail?: Record<string, unknown>): void {
  push({ at: stamp(), step, detail });
}

/** 开始一步异步操作，返回句柄；必须配 endTrace / traceAsync 结束。 */
export function beginTrace(step: string, detail?: Record<string, unknown>): UiTraceEntry {
  const entry: UiTraceEntry = { at: stamp(), step, pending: true, detail };
  push(entry);
  return entry;
}

/** 结束 beginTrace 这一步并记录耗时。 */
export function endTrace(entry: UiTraceEntry, detail?: Record<string, unknown>): void {
  const at = stamp(new Date());
  const ms = Date.parse(`1970-01-01T${at}Z`) - Date.parse(`1970-01-01T${entry.at}Z`);
  entry.ms = Math.max(0, ms);
  entry.pending = false;
  if (detail) entry.detail = { ...(entry.detail ?? {}), ...detail };
}

/**
 * 包住一段异步操作：自动记录耗时；抛错时把错误信息写进时间线（便于事后定位）。
 */
export async function traceAsync<T>(
  step: string,
  run: () => Promise<T>,
  detail?: Record<string, unknown>,
): Promise<T> {
  const entry = beginTrace(step, detail);
  try {
    const value = await run();
    endTrace(entry);
    return value;
  } catch (error) {
    entry.pending = false;
    entry.detail = {
      ...(entry.detail ?? {}),
      错误: String((error as Error)?.message ?? error ?? "未知错误"),
    };
    throw error;
  }
}

/** 导出最近的记录（文本形式，给日志用）。 */
export function traceLines(limit = 30): string[] {
  const slice = entries.slice(-limit);
  return slice.map((entry) => {
    const cost = entry.ms === undefined ? (entry.pending ? "未完成" : "-") : `${entry.ms}ms`;
    const extra = entry.detail ? ` ${JSON.stringify(entry.detail)}` : "";
    return `${entry.at} ${entry.step} [${cost}]${extra}`;
  });
}

/** 清空时间线（换归档/新开一轮排查时用）。 */
export function clearTrace(): void {
  entries.length = 0;
}
