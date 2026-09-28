/**
 * AI 助手服务的调用适配层（**临时**，与 `objectViewApi.ts` 同一套方案）。
 *
 * 本机无法构建 wails3 CLI（bindings 生成器），因此按 wails v3.0.0-beta.12 的
 * 方法 ID 算法（FNV-1a-32 over `pvfine/services.<Type>.<Method>`）手写绑定 ID。
 * 算法已用仓库内既有 bindings 的真实 ID 回归验证（ObjectViewService 两项精确吻合）。
 *
 * 何时删除本文件：官方生成器可用后改用 `bindings/pvfine/services` 的生成结果。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `services.AIMessage`。 */
export interface AIMessage {
  role: string; // user | assistant | tool
  content: string;
  /** Role=tool 时必填：对应上一轮模型发起的工具调用 ID。 */
  toolCallId?: string;
}

/** 对应 Go `services.AIToolRun`。 */
export interface AIToolRun {
  name: string;
  summary: string;
}

/**
 * 对应 Go `services.AIRef`：AI 回复中的一处可定位引用。
 * kind = file（文件，可带行号）/ dir（目录）/ object（对象，如「装备 10018」）。
 */
export interface AIRef {
  path?: string;
  line?: number;
  kind: "file" | "dir" | "object";
  label?: string;
}

/** 对应 Go `services.AIChatResponse`。 */
export interface AIChatResponse {
  reply: string;
  runs: AIToolRun[];
  refs?: AIRef[];
}

/** 对应 Go `services.EditorContext`：前端随每次提问传来的编辑器当前状态。 */
export interface EditorContext {
  activePath?: string;
  activeText?: string;
  selection?: string;
  cursorLine?: number;
  cursorCol?: number;
  recentPaths?: string[];
}

/**
 * 处理一轮对话（后端内部执行「模型 → 工具 → 回填 → 再问」循环）。
 * fqn = pvfine/services.AIService.Chat
 */
export function Chat(
  messages: AIMessage[],
  context?: EditorContext
): $CancellablePromise<AIChatResponse> {
  return $Call.ByID(432861725, { messages, context: context ?? {} });
}

/**
 * 用当前配置发一次最小对话，验证接入点与 key 可用。
 * fqn = pvfine/services.AIService.TestConnection
 */
export function TestConnection(): $CancellablePromise<void> {
  return $Call.ByID(3812815951);
}
