<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { NButton, NEmpty, NIcon, NTag, NTooltip } from "naive-ui";
import { Bot24Regular, Delete24Regular, Dismiss24Regular, Send24Regular, Stop24Regular } from "@vicons/fluent";
import { useAIStore } from "../stores/ai";
import { useArchiveStore } from "../stores/archive";
import { useSettingsStore } from "../stores/settings";
import { useEditorStore } from "../stores/editor";
import type { AIRef } from "../services/aiApi";
import { BUILTIN_QUICK_ACTIONS, type QuickAction } from "../services/quickActions";

const ai = useAIStore();
const archive = useArchiveStore();
const settings = useSettingsStore();
const editor = useEditorStore();

const draft = ref("");
const listRef = ref<HTMLElement | null>(null);
const inputRef = ref<HTMLTextAreaElement | null>(null);

async function onSend(): Promise<void> {
  const text = draft.value;
  draft.value = "";
  await ai.send(text);
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    void onSend();
  }
}

function onClear(): void {
  ai.reset();
  inputRef.value?.focus();
}

function openSettings(): void {
  settings.open("ai");
}

function applyQuickAction(action: QuickAction): void {
  draft.value = action.prompt;
  inputRef.value?.focus();
}

/** AI 引用标签的显示文本。 */
function refLabel(refItem: AIRef): string {
  if (refItem.label && refItem.label.trim()) return refItem.label;
  if (!refItem.path) return "";
  if (refItem.kind === "file" && refItem.line && refItem.line > 0) {
    return `${refItem.path}:${refItem.line}`;
  }
  return refItem.path;
}

/** 点击 AI 引用：文件类打开并定位到行号；目录类定位到资源树。 */
async function openRef(refItem: AIRef): Promise<void> {
  if (!refItem.path) return;
  if (refItem.kind === "file") {
    await editor.openFileByPath(refItem.path, refItem.line);
  }
}

/** 快捷操作 = 内置（提示词可被设置覆盖）+ 用户自定义（P3 自定义规则引擎的落地形式）。 */
const quickActions = computed<QuickAction[]>(() => {
  const overrides = settings.ai.quickActionPrompts ?? {};
  const builtin = BUILTIN_QUICK_ACTIONS.map((action) => ({
    ...action,
    prompt: overrides[action.id] ?? action.prompt,
  }));
  const custom = (settings.ai.customActions ?? []).map((item, index) => ({
    id: `custom-${index}`,
    label: item.label,
    prompt: item.prompt,
  }));
  return [...builtin, ...custom];
});

watch(
  () => ai.messages.length,
  async () => {
    await nextTick();
    listRef.value?.scrollTo({ top: listRef.value.scrollHeight });
  }
);

// 流式输出期间跟着往下滚：不滚会是"屏幕外偷偷生成"的观感。
watch(
  () => ai.streamText.length,
  async () => {
    await nextTick();
    listRef.value?.scrollTo({ top: listRef.value.scrollHeight });
  }
);
</script>

<template>
  <div class="ai-panel">
    <div class="ai-heading">
      <div class="ai-title">
        <NIcon :size="16"><Bot24Regular /></NIcon>
        <span>AI 助手</span>
        <NTag size="tiny" :bordered="false" :type="settings.ai.writeProtection ? 'success' : 'warning'">
          {{ settings.ai.writeProtection ? "写保护开" : "写保护关" }}
        </NTag>
      </div>
      <NTooltip trigger="hover" :disabled="ai.isEmpty">
        <template #trigger>
          <NButton quaternary size="tiny" :disabled="ai.isEmpty" aria-label="清空会话" @click="onClear">
            <template #icon><NIcon :size="14"><Delete24Regular /></NIcon></template>
          </NButton>
        </template>
        清空当前会话
      </NTooltip>
    </div>

    <div v-if="!settings.ai.enabled" class="ai-disabled">
      <div class="ai-tutorial">
        <div class="ai-tutorial-title">首次使用 AI 助手</div>
        <div class="ai-tutorial-step">
          <span class="tut-no">1</span>
          <div class="tut-body">
            <div class="tut-title">打开设置</div>
            <div class="tut-desc">点击下方「前往配置」，进入「设置 → AI 助手」。</div>
          </div>
        </div>
        <div class="ai-tutorial-step">
          <span class="tut-no">2</span>
          <div class="tut-body">
            <div class="tut-title">填写接入信息</div>
            <div class="tut-desc">选择服务商（DeepSeek / Kimi / OpenAI 兼容等），填入 Base URL、模型名与 API Key（本地服务可留空）。</div>
          </div>
        </div>
        <div class="ai-tutorial-step">
          <span class="tut-no">3</span>
          <div class="tut-body">
            <div class="tut-title">测试连接</div>
            <div class="tut-desc">点「测试连接」确认能联通后，再回到本页面。</div>
          </div>
        </div>
        <div class="ai-tutorial-step">
          <span class="tut-no">4</span>
          <div class="tut-body">
            <div class="tut-title">启用 AI 助手</div>
            <div class="tut-desc">打开「启用 AI 助手」开关并保存，配置即生效，本教程自动消失。</div>
          </div>
        </div>
      </div>
      <NButton type="primary" size="small" class="ai-tutorial-cta" @click="openSettings">前往配置</NButton>
    </div>

    <template v-else>
      <div ref="listRef" class="ai-messages">
        <NEmpty
          v-if="ai.isEmpty"
          description="问点什么，比如：装备的 [name] 字段是什么意思？"
          size="small"
          class="ai-empty"
        />
        <div
          v-for="(entry, index) in ai.messages"
          :key="index"
          class="ai-entry"
          :class="`ai-entry--${entry.role}`"
        >
          <div class="ai-bubble">{{ entry.content }}</div>
          <div v-if="entry.runs && entry.runs.length > 0" class="ai-runs">
            <NTag
              v-for="(run, runIndex) in entry.runs"
              :key="runIndex"
              size="tiny"
              :bordered="false"
              :title="run.summary"
            >
              {{ run.name }}
            </NTag>
          </div>
          <div v-if="entry.refs && entry.refs.length > 0" class="ai-refs">
            <NTag
              v-for="(ref, refIndex) in entry.refs"
              :key="refIndex"
              size="tiny"
              :bordered="false"
              type="info"
              class="ai-ref-tag"
              :class="{ 'ai-ref-tag--clickable': ref.kind === 'file' && ref.path }"
              @click="ref.kind === 'file' && ref.path ? openRef(ref) : undefined"
            >
              {{ refLabel(ref) }}
            </NTag>
          </div>
        </div>
        <div v-if="ai.sending" class="ai-entry ai-entry--pending">
          <div class="ai-bubble" :class="{ 'ai-bubble--streaming': ai.streamText }">
            <template v-if="ai.streamText">{{ ai.streamText }}</template>
            <template v-else-if="ai.activeTool">正在执行工具：{{ ai.activeTool }}…</template>
            <template v-else>思考中…</template>
          </div>
          <div v-if="ai.pendingRuns.length > 0" class="ai-runs">
            <NTag
              v-for="(run, runIndex) in ai.pendingRuns"
              :key="runIndex"
              size="tiny"
              :bordered="false"
              :title="run.summary"
            >
              {{ run.name }}
            </NTag>
          </div>
        </div>
      </div>

      <div v-if="ai.contextFiles.length" class="ai-context">
        <NTag
          v-for="ctx in ai.contextFiles"
          :key="ctx.path"
          size="small"
          :bordered="false"
          type="info"
          class="ai-context-tag"
        >
          <template #icon>
            <NIcon
              :size="12"
              class="ai-context-close"
              aria-label="移除引入"
              @click="ai.removeContextFile(ctx.path)"
            ><Dismiss24Regular /></NIcon>
          </template>
          {{ ctx.isDir ? "已引入文件夹：" : "已引入：" }}{{ ctx.path }}
          <NIcon :size="12" class="ai-context-bot"><Bot24Regular /></NIcon>
        </NTag>
      </div>

      <div v-if="!ai.sending" class="ai-quick">
        <NTag
          v-for="action in quickActions"
          :key="action.id"
          size="small"
          :bordered="false"
          class="ai-quick-tag"
          @click="applyQuickAction(action)"
        >
          {{ action.label }}
        </NTag>
      </div>

      <div class="ai-input-row">
        <textarea
          ref="inputRef"
          v-model="draft"
          class="ai-input"
          rows="2"
          placeholder="向 AI 助手提问（Enter 发送，Shift+Enter 换行）"
          :disabled="ai.sending"
          @keydown="onKeydown"
        />
        <NButton
          :type="ai.sending ? 'warning' : 'primary'"
          size="small"
          :disabled="!ai.sending && !draft.trim()"
          :aria-label="ai.sending ? '停止思考' : '发送'"
          @click="ai.sending ? ai.stop() : onSend()"
        >
          <template #icon>
            <NIcon><component :is="ai.sending ? Stop24Regular : Send24Regular" /></NIcon>
          </template>
        </NButton>
      </div>
      <div v-if="!archive.open" class="ai-hint">当前未打开归档：可正常对话，但归档类工具会提示先打开 PVF。</div>
    </template>
  </div>
</template>

<style scoped>
.ai-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  padding: 8px;
  gap: 8px;
}
.ai-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.ai-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
}
.ai-disabled {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  padding: 16px;
}
.ai-tutorial {
  width: 100%;
  max-width: 360px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.ai-tutorial-title {
  font-size: 14px;
  font-weight: 600;
}
.ai-tutorial-step {
  display: flex;
  gap: 10px;
  align-items: flex-start;
}
.tut-no {
  flex: 0 0 auto;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 600;
  color: #fff;
  background: var(--pvf-primary, #4a9eff);
}
.tut-body {
  flex: 1;
  min-width: 0;
}
.tut-title {
  font-size: 12.5px;
  font-weight: 600;
}
.tut-desc {
  margin-top: 2px;
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 11.5px;
  line-height: 1.6;
}
.ai-tutorial-cta {
  align-self: center;
}
.ai-messages {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 4px 2px;
}
.ai-empty {
  margin: auto;
}
.ai-entry {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 96%;
}
.ai-entry--user {
  align-self: flex-end;
  align-items: flex-end;
}
.ai-entry--error {
  align-self: stretch;
  align-items: stretch;
}
.ai-bubble {
  padding: 6px 10px;
  border-radius: 10px;
  background: rgba(128, 128, 128, 0.16);
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 12px;
  line-height: 1.5;
}
.ai-entry--error .ai-bubble {
  color: #e88080;
}
/* 流式气泡：正文齐全色由 :class 控制，只靠光标提示"还在写"。
   正在生成时不做半透明，否则流式文字比静态消息更难读。 */
.ai-entry--pending .ai-bubble {
  opacity: 0.72;
}
.ai-bubble--streaming {
  opacity: 1;
}
.ai-bubble--streaming::after {
  content: "";
  display: inline-block;
  width: 2px;
  height: 1em;
  margin-left: 3px;
  vertical-align: -2px;
  background: currentColor;
  animation: ai-caret 1s steps(2, start) infinite;
}
@keyframes ai-caret {
  50% {
    opacity: 0;
  }
}
.ai-runs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.ai-refs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.ai-ref-tag {
  max-width: 100%;
}
.ai-ref-tag--clickable {
  cursor: pointer;
  user-select: none;
}
.ai-ref-tag--clickable:hover {
  opacity: 0.82;
}
.ai-context {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
}
.ai-context-tag {
  max-width: 100%;
}
.ai-context-close {
  cursor: pointer;
  opacity: 0.75;
}
.ai-context-close:hover {
  opacity: 1;
  color: #e88080;
}
.ai-context-bot {
  margin-left: 4px;
  opacity: 0.85;
}
.ai-input-row {
  display: flex;
  align-items: flex-end;
  gap: 6px;
}
.ai-input {
  flex: 1;
  resize: none;
  font-size: 12px;
  line-height: 1.5;
  padding: 6px 8px;
  border-radius: 8px;
  border: 1px solid rgba(128, 128, 128, 0.35);
  background: transparent;
  color: inherit;
  font-family: inherit;
}
.ai-input:focus {
  outline: none;
  border-color: rgba(96, 140, 255, 0.7);
}
.ai-quick {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.ai-quick-tag {
  cursor: pointer;
  user-select: none;
}
.ai-quick-tag:hover {
  opacity: 0.85;
}
.ai-hint {
  font-size: 11px;
  opacity: 0.65;
}
</style>
