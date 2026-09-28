import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { Events } from "@wailsio/runtime";
import { Chat, type AIMessage, type AIRef, type AIToolRun, type EditorContext } from "../services/aiApi";
import { useEditorStore } from "./editor";

/** 后端 SSE 事件的名字：见 services/ai.go 的 emitEvent。 */
const AI_DELTA_EVENT = "ai:delta";
const AI_TOOL_EVENT = "ai:tool";

function eventData(event: any): any {
  return event?.data ?? event;
}

export interface AIChatEntry {
  role: "user" | "assistant" | "error";
  content: string;
  /** 实际发给模型的文本（可含引入的文件上下文）；缺省时用 content。 */
  sent?: string;
  /** 本次回复里 AI 实际执行过的工具（供回显）。 */
  runs?: AIToolRun[];
  /** 本次回复里的可定位引用（路径/行号），渲染为可点击跳转链接。 */
  refs?: AIRef[];
}

/** 从编辑器「AI 引入」进来的上下文条目（文件，或整个文件夹）。 */
export interface AIFileContext {
  path: string;
  title: string;
  /** 是否为文件夹：文件夹整体引用，AI 用 list_directory 自行展开。 */
  isDir?: boolean;
}

/** 引入上下文数量上限：选多少识别多少，但一次/累计最多带入 20 个文件。 */
export const MAX_CONTEXT_FILES = 20;

/** AI 助手会话状态：只保存当前会话，不落盘（方案见 AI镶嵌.md §二 功能边界）。 */
export const useAIStore = defineStore("ai", () => {
  const messages = ref<AIChatEntry[]>([]);
  const sending = ref(false);
  const lastError = ref("");
  /** 流式尚未落定的增量正文：模型边生成边追加，收到完整回复后清空（以服务端的为准）。 */
  const streamText = ref("");
  /** 正在执行的工具名（AI 调用工具期间实时回显）。 */
  const activeTool = ref("");
  /** 本轮已完成的工具调用，实时累积；收到完整回复后用服务端的 runs 覆盖（避免重复渲染）。 */
  const pendingRuns = ref<AIToolRun[]>([]);
  /** 当前从编辑器引入的文件（AI 可直接定位/修改这些窗口内容），最多 MAX_CONTEXT_FILES 个。 */
  const contextFiles = ref<AIFileContext[]>([]);

  const canSend = computed(() => !sending.value);
  const isEmpty = computed(() => messages.value.length === 0);

  // ---- 后端流式事件订阅（见 services/ai.go：emitEvent("ai:delta"/"ai:tool")）----
  Events.On(AI_DELTA_EVENT, (event: any) => {
    const text = String(eventData(event)?.text ?? "");
    if (text) streamText.value += text;
  });

  Events.On(AI_TOOL_EVENT, (event: any) => {
    const data = eventData(event);
    const name = String(data?.name ?? "");
    if (!name) return;
    if (data?.phase === "start") {
      activeTool.value = name;
      return;
    }
    // done：落一条可回显记录，工具名重复时保留最新一条摘要。
    if (activeTool.value === name) activeTool.value = "";
    const summary = String(data?.summary ?? "");
    const next = pendingRuns.value.filter((run) => run.name !== name);
    next.push({ name, summary });
    pendingRuns.value = next;
  });

  function reset(): void {
    messages.value = [];
    lastError.value = "";
    contextFiles.value = [];
    streamText.value = "";
    activeTool.value = "";
    pendingRuns.value = [];
  }

  /**
   * 把编辑器选中的文件/文件夹引入给 AI：作为上下文随下一条消息发送。
   * 选多少识别多少（去重），总数上限 MAX_CONTEXT_FILES；返回实际新增数与是否被截断。
   */
  function introduceFiles(
    files: AIFileContext[]
  ): { added: number; truncated: boolean } {
    const next = [...contextFiles.value];
    const seen = new Set(next.map((item) => item.path));
    let added = 0;
    let truncated = false;
    for (const file of files) {
      if (!file.path || seen.has(file.path)) continue;
      if (next.length >= MAX_CONTEXT_FILES) {
        truncated = true;
        break;
      }
      seen.add(file.path);
      next.push({ path: file.path, title: file.title, isDir: file.isDir });
      added += 1;
    }
    contextFiles.value = next;
    return { added, truncated };
  }

  function removeContextFile(path: string): void {
    contextFiles.value = contextFiles.value.filter((item) => item.path !== path);
  }

  function clearContextFiles(): void {
    contextFiles.value = [];
  }

  /** 当前进行中的对话请求（供「停止思考」取消）。 */
  let activeRequest: ReturnType<typeof Chat> | null = null;
  /** 用户是否主动停止了本次生成（用于区分「已停止」与真实错误）。 */
  let stopRequested = false;

  /**
   * 发送一条用户消息。工具调用由后端在单次 Chat 内循环执行，
   * 前端只传 user/assistant 历史（工具结果不回传历史，由后端每轮现算）。
   */
  async function send(text: string): Promise<void> {
    const trimmed = text.trim();
    if (!trimmed || sending.value) return;
    let sentText = trimmed;
    const ctxList = contextFiles.value;
    if (ctxList.length > 0) {
      const fileLines = ctxList
        .map(
          (item, index) =>
            `${index + 1}. ${item.isDir ? "文件夹" : "文件"}：${item.path}` +
            `${!item.isDir && item.title ? `，标题：${item.title}` : ""}`
        )
        .join("\n");
      sentText =
        `（用户从编辑器引入了 ${ctxList.length} 个条目：\n${fileLines}\n` +
        `文件夹请先用 list_directory 展开，再用 read_file/edit_file 处理其中的文件）\n` + trimmed;
    }
    messages.value.push({ role: "user", content: trimmed, sent: sentText });
    sending.value = true;
    lastError.value = "";
    stopRequested = false;
    streamText.value = "";
    activeTool.value = "";
    pendingRuns.value = [];
    try {
      const history: AIMessage[] = [];
      for (const entry of messages.value) {
        if (entry.role === "user" || entry.role === "assistant") {
          history.push({ role: entry.role, content: entry.sent ?? entry.content });
        }
      }
      const request = Chat(history, collectEditorContext());
      activeRequest = request;
      const res = await request;
      messages.value.push({
        role: "assistant",
        content: res?.reply ?? "（模型没有返回内容）",
        runs: res?.runs ?? [],
        refs: res?.refs ?? [],
      });
    } catch (error: any) {
      // 流式增量只是"预览"，最终内容一律以服务端返回的 result 为准。
      if (stopRequested) {
        messages.value.push({ role: "assistant", content: "（已停止生成）" });
      } else {
        const message = String(error?.message ?? error);
        lastError.value = message;
        messages.value.push({ role: "error", content: message });
      }
    } finally {
      activeRequest = null;
      stopRequested = false;
      sending.value = false;
      streamText.value = "";
      activeTool.value = "";
      pendingRuns.value = [];
    }
  }

  /** 停止当前生成：取消后端请求（ctx 取消），立即恢复可发送状态。 */
  function stop(): void {
    if (!activeRequest) return;
    stopRequested = true;
    streamText.value = "";
    activeTool.value = "";
    try {
      void activeRequest.cancel();
    } catch {
      // 请求已结束：忽略
    }
    activeRequest = null;
    sending.value = false;
  }

  /**
   * 采集编辑器当前上下文（活动文件 + 最近文件），随每次提问传给后端，
   * 让 AI 能「看见」用户正在编辑的内容（方向1：上下文感知）。
   * 选区/光标暂未采集（需 CodeEditor 上报，留作后续增强）。
   */
  function collectEditorContext(): EditorContext {
    const editor = useEditorStore();
    const activeTab = editor.activeTab;
    return {
      activePath: activeTab?.path ?? "",
      activeText: activeTab ? activeTab.text.slice(0, 4000) : "",
      recentPaths: editor.tabs.slice(0, 10).map((tab) => tab.path),
    };
  }

  return {
    messages,
    sending,
    lastError,
    contextFiles,
    streamText,
    activeTool,
    pendingRuns,
    canSend,
    isEmpty,
    send,
    stop,
    reset,
    introduceFiles,
    removeContextFile,
    clearContextFiles,
  };
});
