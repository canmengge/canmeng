<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  ArrowSync24Regular,
  CheckmarkCircle24Regular,
  Code24Regular,
  Copy24Regular,
  CursorHover24Regular,
  Desktop24Regular,
  DismissCircle24Regular,
  DocumentSync24Regular,
  FolderOpen24Regular,
  Image24Regular,
  Info24Regular,
  PaintBrush24Regular,
  Settings24Regular,
  ShieldCheckmark24Regular,
  ShieldLock24Regular,
  Bot24Regular,
  Tag24Regular,
  WeatherMoon24Regular,
  WeatherSunny24Regular,
  Wrench24Regular,
} from "@vicons/fluent";
import {
  NAlert,
  NButton,
  NIcon,
  NInput,
  NModal,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NSpin,
  NSwitch,
  NTag,
  NTooltip,
  useDialog,
  useMessage,
} from "naive-ui";
import { AnnotationService, RenderingService, UpdateService } from "../../bindings/pvfine/services";
import type { AIAssistantSettings, AICustomAction } from "../../bindings/pvfine/services/models";
import { AnnotationSources, type AnnotationReloadResult } from "../services/annotationApi";
import { TestConnection } from "../services/aiApi";
import { BUILD_LABEL } from "../buildInfo";
import { BUILTIN_QUICK_ACTIONS } from "../services/quickActions";
import {
  useSettingsStore,
  AI_PROVIDER_CHOICES,
  TEXT_EDIT_LIMIT_CHOICES,
  type AnnotationTagPlacement,
  type ExplorerOpenMode,
  type ThemeMode,
} from "../stores/settings";
import { useEditorStore } from "../stores/editor";
import { useExplorerStore } from "../stores/explorer";
import { useImageStore } from "../stores/images";
import { invalidateStringTableGuardCache } from "../services/stringGuardApi";

type TabKey = "general" | "editor" | "npk" | "system" | "ai";

const settings = useSettingsStore();
const editor = useEditorStore();
const explorer = useExplorerStore();
const images = useImageStore();
const message = useMessage();
const dialog = useDialog();

const activeTab = computed<TabKey>({
  get: () => settings.activeTab,
  set: (val) => {
    settings.activeTab = val;
  },
});
const reloadingAnnotations = ref(false);
const reloadingRendering = ref(false);
const selectingNPK = ref(false);
const rebuildingNPK = ref(false);

const tabs = [
  { id: "general" as const, label: "常规与外观", icon: PaintBrush24Regular },
  { id: "editor" as const, label: "代码编辑器", icon: Code24Regular },
  { id: "npk" as const, label: "NPK 资源库", icon: Image24Regular },
  { id: "system" as const, label: "系统维护", icon: Wrench24Regular },
  { id: "ai" as const, label: "AI 助手", icon: Bot24Regular },
];

const npkProgressPercent = computed(() => {
  const { total, done } = images.status;
  if (!total || total <= 0) return 0;
  return Math.min(100, Math.round((done / total) * 100));
});

async function onPlacementChange(value: string | number | boolean) {
  try {
    await settings.savePlacement(value as AnnotationTagPlacement);
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

async function onExplorerOpenModeChange(value: string | number | boolean) {
  try {
    await settings.saveExplorerOpenMode(value as ExplorerOpenMode);
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

async function onVimModeChange(value: boolean) {
  try {
    await settings.saveVimMode(value);
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

async function onBackupSourceOnSaveChange(value: boolean) {
  try {
    await settings.saveBackupSourceOnSave(value);
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

const textEditLimitOptions = TEXT_EDIT_LIMIT_CHOICES.map((item) => ({
  label: item.label,
  value: item.value,
}));

async function onTextEditLimitChange(value: string | number | boolean) {
  const next = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(next)) return;
  try {
    await settings.saveTextEditLimitMB(next);
    message.success(
      next === 0 ? "文本编辑上限已设为不限（大文件可能卡顿、占用大量内存）" : `文本编辑上限已设为 ${next} MB`
    );
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

// ---- AI 助手 ----
const aiProviderOptions = AI_PROVIDER_CHOICES.map((item) => ({ label: item.label, value: item.value }));
const checkingAI = ref(false);
// 文本类字段本地草稿：失焦才落盘，避免每敲一个键写一次 settings.json。
const aiBaseURLDraft = ref("");
const aiModelDraft = ref("");
const aiAPIKeyDraft = ref("");
/** 自定义快捷命令的编辑草稿：每行一条「标签|提示词」。 */
const aiCustomActionsDraft = ref("");

function formatCustomActions(actions: AICustomAction[] | null | undefined): string {
  return (actions ?? []).map((item) => `${item.label}|${item.prompt}`).join("\n");
}

function parseCustomActions(text: string): AICustomAction[] {
  const result: AICustomAction[] = [];
  for (const rawLine of text.split("\n")) {
    const line = rawLine.trim();
    if (!line) continue;
    const separator = line.indexOf("|");
    const label = (separator >= 0 ? line.slice(0, separator) : line).trim();
    const prompt = (separator >= 0 ? line.slice(separator + 1) : "").trim();
    if (!label || !prompt) continue;
    result.push({ label, prompt });
  }
  return result;
}

/** 提交自定义快捷命令草稿（失焦静默、按钮显式反馈；无变化不写盘）。 */
async function commitCustomActions(showFeedback: boolean): Promise<void> {
  const normalized = formatCustomActions(parseCustomActions(aiCustomActionsDraft.value));
  aiCustomActionsDraft.value = normalized;
  if (normalized === formatCustomActions(settings.ai.customActions)) {
    if (showFeedback) message.success("自定义快捷命令已是最新（已保存）");
    return;
  }
  try {
    await settings.saveAIAssistant({ ...settings.ai, customActions: parseCustomActions(normalized) });
    if (showFeedback) message.success("自定义快捷命令已保存");
  } catch (error: any) {
    message.error(`保存失败: ${error?.message ?? error}`);
  }
}

/** 内置快捷命令提示词的编辑草稿（id → 提示词；空串表示用默认值）。 */
const aiQuickActionDrafts = ref<Record<string, string>>({});

function setQuickActionDraft(id: string, value: string): void {
  aiQuickActionDrafts.value = { ...aiQuickActionDrafts.value, [id]: value };
}

/** 提交内置快捷命令提示词（失焦静默、按钮显式反馈；无变化不写盘）。 */
async function commitQuickActionPrompts(showFeedback: boolean): Promise<void> {
  const next: Record<string, string> = {};
  for (const action of BUILTIN_QUICK_ACTIONS) {
    const value = (aiQuickActionDrafts.value[action.id] ?? "").trim();
    if (value) next[action.id] = value;
  }
  if (JSON.stringify(next) === JSON.stringify(settings.ai.quickActionPrompts ?? {})) {
    if (showFeedback) message.success("提示词已是最新（已保存）");
    return;
  }
  try {
    await settings.saveAIAssistant({ ...settings.ai, quickActionPrompts: next });
    if (showFeedback) message.success("快捷命令提示词已保存");
  } catch (error: any) {
    message.error(`保存失败: ${error?.message ?? error}`);
  }
}

watch(
  () => settings.ai,
  (ai) => {
    aiBaseURLDraft.value = ai.baseURL;
    aiModelDraft.value = ai.model;
    aiAPIKeyDraft.value = ai.apiKey;
    aiCustomActionsDraft.value = formatCustomActions(ai.customActions);
    // 内置快捷命令提示词草稿：未覆盖的留空（输入框显示默认值占位）。
    const overrides = ai.quickActionPrompts ?? {};
    const drafts: Record<string, string> = {};
    for (const action of BUILTIN_QUICK_ACTIONS) {
      drafts[action.id] = overrides[action.id] ?? "";
    }
    aiQuickActionDrafts.value = drafts;
  },
  { immediate: true, deep: true }
);

async function saveAIField(patch: Partial<AIAssistantSettings>): Promise<void> {
  try {
    await settings.saveAIAssistant({ ...settings.ai, ...patch });
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

function onAIEnabledChange(value: boolean): void {
  void saveAIField({ enabled: value });
}

function onAIWriteProtectionChange(value: boolean): void {
  void saveAIField({ writeProtection: value });
}

function onAIProviderChange(value: string | number | boolean): void {
  const provider = String(value);
  const preset = AI_PROVIDER_CHOICES.find((item) => item.value === provider);
  // 预设带出该服务商的接入地址与默认模型；custom 保留用户当前草稿不被覆盖。
  const patch: Partial<AIAssistantSettings> = { provider };
  if (preset && preset.value !== "custom") {
    patch.baseURL = preset.baseURL;
    patch.model = preset.model;
  } else {
    patch.baseURL = aiBaseURLDraft.value.trim();
    patch.model = aiModelDraft.value.trim();
  }
  patch.apiKey = aiAPIKeyDraft.value;
  void saveAIField(patch);
}

/** 归一化接入地址：去掉末尾斜杠与误粘贴的 /chat/completions 后缀（请求时会自动拼接）。 */
function normalizeAIBaseURL(input: string): string {
  let url = input.trim();
  while (url.endsWith("/")) url = url.slice(0, -1);
  if (url.toLowerCase().endsWith("/chat/completions")) {
    url = url.slice(0, -"/chat/completions".length);
  }
  return url;
}

/** 提交三个文本草稿；带反馈由调用方决定（失焦静默、按钮显式提示）。 */
async function commitAIDrafts(showFeedback: boolean): Promise<void> {
  const baseURL = normalizeAIBaseURL(aiBaseURLDraft.value);
  const model = aiModelDraft.value.trim();
  const apiKey = aiAPIKeyDraft.value;
  aiBaseURLDraft.value = baseURL;
  if (baseURL === settings.ai.baseURL && model === settings.ai.model && apiKey === settings.ai.apiKey) {
    if (showFeedback) message.success("AI 配置已是最新（已保存）");
    return;
  }
  try {
    await settings.saveAIAssistant({ ...settings.ai, baseURL, model, apiKey });
    if (showFeedback) message.success("AI 配置已保存");
  } catch (error: any) {
    message.error(`保存 AI 配置失败: ${error?.message ?? error}`);
  }
}

async function onTestAIConnection(): Promise<void> {
  if (checkingAI.value) return;
  await commitAIDrafts(false);
  checkingAI.value = true;
  try {
    await TestConnection();
    message.success("AI 连接正常");
  } catch (error: any) {
    message.error(`连接失败: ${error?.message ?? error}`);
  } finally {
    checkingAI.value = false;
  }
}

/** 删除本地保存的 API Key：清空草稿与 settings.json 里的密钥并落盘。 */
function onDeleteAPIKey(): void {
  dialog.warning({
    title: "删除本地 API Key",
    content: "确定要删除已保存在本机设置文件里的 API Key 吗？删除后 AI 助手需重新填入才能使用。",
    positiveText: "删除",
    negativeText: "取消",
    onPositiveClick: async () => {
      aiAPIKeyDraft.value = "";
      try {
        await settings.saveAIAssistant({ ...settings.ai, apiKey: "" });
        message.success("已删除本地保存的 API Key");
      } catch (error: any) {
        message.error(`删除 API Key 失败: ${error?.message ?? error}`);
      }
    },
  });
}

async function onProtectedStringTableGuardChange(value: boolean) {
  try {
    await settings.saveProtectedStringTableGuard(value);
    // 后端拦截判定已经跟着变了，前端缓存必须一起失效，
    // 否则编辑器仍会按旧状态把占位符编辑改道到「改写到安全表」。
    invalidateStringTableGuardCache();
    message.success(
      value
        ? "已开启字符串表写保护：写入禁动字符串表会被拦截"
        : "已关闭字符串表写保护：不再拦截禁动字符串表写入"
    );
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

// ---- 注释数据（外置） ----
const annotationSources = ref<AnnotationReloadResult | null>(null);

async function refreshAnnotationSources(): Promise<void> {
  try {
    annotationSources.value = (await AnnotationSources()) ?? null;
  } catch {
    annotationSources.value = null;
  }
}

// 打开设置面板时刷新一次注释来源（不重新加载，只读统计）。
watch(
  () => settings.visible,
  (visible) => {
    if (visible) void refreshAnnotationSources();
  }
);

async function onThemeModeChange(value: ThemeMode) {
  try {
    await settings.saveThemeMode(value);
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

async function onReloadAnnotations() {
  if (reloadingAnnotations.value) return;
  reloadingAnnotations.value = true;
  try {
    const result = await AnnotationService.ReloadRules();
    await Promise.all([editor.refreshAnnotations(), explorer.refreshAnnotations()]);
    message.success(
      `已重载 ${result.ruleCount} 条标注规则、${result.relationCount} 个关联类型`
    );
    void refreshAnnotationSources();
  } catch (error: any) {
    message.error(`重载标注规则失败: ${error?.message ?? error}`);
  } finally {
    reloadingAnnotations.value = false;
  }
}

async function onReloadRendering() {
  if (reloadingRendering.value) return;
  reloadingRendering.value = true;
  try {
    const result = await RenderingService.ReloadRules();
    await editor.refreshRenderedText();
    message.success(`已重载 ${result.ruleCount} 条渲染规则`);
  } catch (error: any) {
    message.error(`重载渲染规则失败: ${error?.message ?? error}`);
  } finally {
    reloadingRendering.value = false;
  }
}

const checkingUpdates = ref(false);

async function onCheckUpdates(): Promise<void> {
  if (checkingUpdates.value) return;
  checkingUpdates.value = true;
  try {
    await UpdateService.CheckForUpdates();
  } catch (error: any) {
    message.error(`检查更新失败: ${error?.message ?? error}`);
  } finally {
    checkingUpdates.value = false;
  }
}

async function onUpdateChannelChange(value: string | number | boolean): Promise<void> {
  const next = value === "dev" ? "dev" : "stable";
  try {
    await settings.saveUpdateChannel(next);
    message.success(
      next === "dev"
        ? "已切换到「开发人员专用」通道，重启应用后生效"
        : "已切换回「正式版」通道，重启应用后生效"
    );
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

async function onMcpEnabledChange(value: boolean): Promise<void> {
  try {
    await settings.saveMcpEnabled(value);
    message.success(
      value
        ? "已开启「MCP 只读服务」，重启应用后在本机回环地址提供只读归档工具"
        : "已关闭「MCP 只读服务」，重启应用后生效"
    );
  } catch (error: any) {
    message.error(`保存设置失败: ${error?.message ?? error}`);
  }
}

async function onSelectNPKDirectory(): Promise<void> {
  if (selectingNPK.value) return;
  selectingNPK.value = true;
  try {
    const next = await images.selectDirectory();
    settings.npkDirectory = next.directory ?? "";
    if (next.directory) message.success("已选择 NPK 目录，开始建立图标索引");
  } catch (error: any) {
    message.error(`选择 NPK 目录失败: ${error?.message ?? error}`);
  } finally {
    selectingNPK.value = false;
  }
}

async function onRebuildNPKIndex(): Promise<void> {
  if (rebuildingNPK.value || !images.status.directory) return;
  rebuildingNPK.value = true;
  try {
    await images.rebuild();
  } catch (error: any) {
    message.error(`重建图标索引失败: ${error?.message ?? error}`);
  } finally {
    rebuildingNPK.value = false;
  }
}

async function copyNPKDirectory(): Promise<void> {
  if (!settings.npkDirectory) return;
  try {
    await navigator.clipboard.writeText(settings.npkDirectory);
    message.success("已复制 NPK 目录路径");
  } catch {
    message.warning("复制路径失败，请手动选择复制");
  }
}
</script>

<template>
  <NModal
    v-model:show="settings.visible"
    preset="card"
    :bordered="false"
    :mask-closable="!settings.saving"
    :close-on-esc="!settings.saving"
    class="settings-modal"
    :style="{ width: 'min(680px, calc(100vw - 32px))' }"
  >
    <template #header>
      <div class="settings-modal-header">
        <NIcon :size="20" class="settings-modal-header-icon">
          <Settings24Regular />
        </NIcon>
        <span class="settings-modal-header-title">偏好设置</span>
      </div>
    </template>

    <NSpin :show="!settings.loaded || settings.saving">
      <div class="settings-container">
        <!-- 顶部导航分段栏 -->
        <nav class="settings-nav" aria-label="设置分类">
          <button
            v-for="tab in tabs"
            :key="tab.id"
            type="button"
            class="settings-nav-item"
            :class="{ 'settings-nav-item--active': activeTab === tab.id }"
            @click="activeTab = tab.id"
          >
            <NIcon :size="16" class="settings-nav-icon">
              <component :is="tab.icon" />
            </NIcon>
            <span class="settings-nav-label">{{ tab.label }}</span>
            <span
              v-if="tab.id === 'npk' && images.status.directory"
              class="settings-nav-dot"
              :class="`settings-nav-dot--${images.status.state}`"
            />
          </button>
        </nav>

        <!-- Tab 1: 常规与外观 -->
        <div v-show="activeTab === 'general'" class="settings-tab-panel">
          <!-- 主题选择 -->
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">外观主题</div>
              <div class="group-subtitle">选择界面的色彩风格，支持深色、浅色或跟随操作系统自动切换</div>
            </div>

            <div class="theme-grid">
              <!-- 深色 -->
              <button
                type="button"
                class="theme-card"
                :class="{ 'theme-card--active': settings.themeMode === 'dark' }"
                @click="onThemeModeChange('dark')"
              >
                <div class="theme-preview theme-preview--dark">
                  <div class="preview-titlebar">
                    <span class="preview-dot dot-red" />
                    <span class="preview-dot dot-yellow" />
                    <span class="preview-dot dot-green" />
                  </div>
                  <div class="preview-body">
                    <div class="preview-line line-short accent-blue" />
                    <div class="preview-line line-long accent-muted" />
                    <div class="preview-line line-med accent-green" />
                  </div>
                </div>
                <div class="theme-card-footer">
                  <div class="theme-card-title">
                    <NIcon :size="16"><WeatherMoon24Regular /></NIcon>
                    <span>深色模式</span>
                  </div>
                  <NIcon
                    v-if="settings.themeMode === 'dark'"
                    :size="18"
                    class="theme-check-icon"
                  >
                    <CheckmarkCircle24Regular />
                  </NIcon>
                </div>
              </button>

              <!-- 浅色 -->
              <button
                type="button"
                class="theme-card"
                :class="{ 'theme-card--active': settings.themeMode === 'light' }"
                @click="onThemeModeChange('light')"
              >
                <div class="theme-preview theme-preview--light">
                  <div class="preview-titlebar">
                    <span class="preview-dot dot-red" />
                    <span class="preview-dot dot-yellow" />
                    <span class="preview-dot dot-green" />
                  </div>
                  <div class="preview-body">
                    <div class="preview-line line-short accent-blue" />
                    <div class="preview-line line-long accent-muted" />
                    <div class="preview-line line-med accent-green" />
                  </div>
                </div>
                <div class="theme-card-footer">
                  <div class="theme-card-title">
                    <NIcon :size="16"><WeatherSunny24Regular /></NIcon>
                    <span>浅色模式</span>
                  </div>
                  <NIcon
                    v-if="settings.themeMode === 'light'"
                    :size="18"
                    class="theme-check-icon"
                  >
                    <CheckmarkCircle24Regular />
                  </NIcon>
                </div>
              </button>

              <!-- 跟随系统 -->
              <button
                type="button"
                class="theme-card"
                :class="{ 'theme-card--active': settings.themeMode === 'system' }"
                @click="onThemeModeChange('system')"
              >
                <div class="theme-preview theme-preview--system">
                  <div class="preview-titlebar">
                    <span class="preview-dot dot-red" />
                    <span class="preview-dot dot-yellow" />
                    <span class="preview-dot dot-green" />
                  </div>
                  <div class="preview-body preview-body--split">
                    <div class="split-half split-half--light">
                      <div class="preview-line line-short accent-blue" />
                      <div class="preview-line line-long accent-muted" />
                    </div>
                    <div class="split-half split-half--dark">
                      <div class="preview-line line-short accent-blue" />
                      <div class="preview-line line-long accent-muted" />
                    </div>
                  </div>
                </div>
                <div class="theme-card-footer">
                  <div class="theme-card-title">
                    <NIcon :size="16"><Desktop24Regular /></NIcon>
                    <span>跟随系统</span>
                  </div>
                  <NIcon
                    v-if="settings.themeMode === 'system'"
                    :size="18"
                    class="theme-check-icon"
                  >
                    <CheckmarkCircle24Regular />
                  </NIcon>
                </div>
              </button>
            </div>
          </section>

          <!-- 交互与文件 -->
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">交互与文件</div>
            </div>

            <div class="settings-card">
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><CursorHover24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">节点打开触发方式</div>
                  <div class="setting-item-desc">设置资源管理器目录树中的文件使用单击还是双击进行打开</div>
                </div>
                <div class="setting-item-control">
                  <NRadioGroup
                    :value="settings.explorerOpenMode"
                    size="small"
                    @update:value="onExplorerOpenModeChange"
                  >
                    <NRadioButton value="single-click">单击打开</NRadioButton>
                    <NRadioButton value="double-click">双击打开</NRadioButton>
                  </NRadioGroup>
                </div>
              </div>

              <div class="setting-card-divider" />

              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><ShieldCheckmark24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">覆盖保存前自动备份</div>
                  <div class="setting-item-desc">保存并覆盖原 PVF 归档前，自动在同目录下生成同名的 <code>.bak</code> 备份文件</div>
                </div>
                <div class="setting-item-control">
                  <NSwitch
                    :value="settings.backupSourceOnSave"
                    @update:value="onBackupSourceOnSaveChange"
                  />
                </div>
              </div>

              <div class="setting-card-divider" />

              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><ShieldLock24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">字符串表写保护</div>
                  <div class="setting-item-desc">
                    关闭（默认）时不限制写入；开启后，对客户端汉化禁动字符串表（安全表
                    1/5/8/27 之外）的写入会被拦截并给出原因，可避免界面乱码
                  </div>
                </div>
                <div class="setting-item-control">
                  <NSwitch
                    :value="settings.protectedStringTableGuard"
                    @update:value="onProtectedStringTableGuardChange"
                  />
                </div>
              </div>

              <div class="setting-card-divider" />

              <!-- 注释数据（外置）：注释内容放在程序目录的「注释数据」文件夹，可随时改、即时生效 -->
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><DocumentSync24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">注释数据（外置）</div>
                  <div class="setting-item-desc">
                    <template v-if="annotationSources?.external?.found">
                      目录：<code>{{ annotationSources.external.dir }}</code><br />
                      字段 {{ annotationSources.external.fields }} · 规则
                      {{ annotationSources.external.rules }}（路径 {{ annotationSources.external.pathRules }} / 引用
                      {{ annotationSources.external.hoverRules }}）· 关系
                      {{ annotationSources.external.relations }} · 加载
                      {{ annotationSources.external.durationMs }}ms
                    </template>
                    <template v-else>
                      未找到外置注释目录（当前只用内置基础注解）：
                      <code>{{ annotationSources?.external?.dir || "—" }}</code>
                    </template>
                  </div>
                </div>
                <div class="setting-item-control">
                  <NButton size="small" :loading="reloadingAnnotations" @click="onReloadAnnotations">
                    重新加载
                  </NButton>
                </div>
              </div>
            </div>
          </section>
        </div>

        <!-- Tab 2: 代码编辑器 -->
        <div v-show="activeTab === 'editor'" class="settings-tab-panel">
          <!-- 标注规则渲染 -->
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">规则标注排版</div>
              <div class="group-subtitle">调整 PVF 规则标注在代码编辑器中的显示位置与渲染行为</div>
            </div>

            <div class="settings-card">
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><Tag24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">Tag 标注显示位置</div>
                  <div class="setting-item-desc">选择规则名称、关联说明与代码 Tag 的停靠排版方式</div>
                </div>
                <div class="setting-item-control">
                  <NRadioGroup
                    :value="settings.annotationTagPlacement"
                    size="small"
                    @update:value="onPlacementChange"
                  >
                    <NRadioButton value="after-target">紧随内容</NRadioButton>
                    <NRadioButton value="line-end">行末对齐</NRadioButton>
                    <NRadioButton value="hidden">隐藏标注</NRadioButton>
                  </NRadioGroup>
                </div>
              </div>

              <!-- 标注效果实时预览 -->
              <div class="preview-annotation-box">
                <div class="preview-annotation-title">实时排版预览：</div>
                <div class="preview-code-block">
                  <div
                    v-if="settings.annotationTagPlacement === 'after-target'"
                    class="preview-code-row"
                  >
                    <span class="code-token code-key">[name]</span>
                    <span class="code-token code-string">`魔剑-阿波菲斯`</span>
                    <span class="preview-tag preview-tag--blue">巨剑</span>
                  </div>
                  <div
                    v-else-if="settings.annotationTagPlacement === 'line-end'"
                    class="preview-code-row preview-code-row--line-end"
                  >
                    <span class="code-token code-key">[name]</span>
                    <span class="code-token code-string">`魔剑-阿波菲斯`</span>
                    <span class="preview-tag preview-tag--blue preview-tag--end">巨剑</span>
                  </div>
                  <div v-else class="preview-code-row">
                    <span class="code-token code-key">[name]</span>
                    <span class="code-token code-string">`魔剑-阿波菲斯`</span>
                    <span class="preview-code-muted">（Tag 标注已隐藏）</span>
                  </div>
                </div>
              </div>
            </div>
          </section>

          <!-- 编辑模式 -->
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">编辑模式</div>
            </div>

            <div class="settings-card">
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><Code24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">Vim 键位模式</span>
                    <NTag size="small" round :bordered="false" type="info">Modal</NTag>
                  </div>
                  <div class="setting-item-desc">启用后支持 Vim 普通、插入与可视模态，提供熟悉高效的代码导航与操作指令</div>
                </div>
                <div class="setting-item-control">
                  <NSwitch :value="settings.vimMode" @update:value="onVimModeChange" />
                </div>
              </div>
            </div>
          </section>

          <!-- 大文件 -->
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">大文件</div>
            </div>

            <div class="settings-card">
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><Code24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">文本编辑上限</span>
                  </div>
                  <div class="setting-item-desc">
                    超过上限的文件只显示占位提示，不载入编辑器；在上限之内但超过 8MB 的文件会先二次确认，然后以只读方式打开并关闭折行与空白高亮，需要编辑时点编辑器里的「允许编辑」
                  </div>
                </div>
                <div class="setting-item-control">
                  <NSelect
                    size="small"
                    style="min-width: 240px"
                    :value="settings.textEditLimitMB"
                    :options="textEditLimitOptions"
                    @update:value="onTextEditLimitChange"
                  />
                </div>
              </div>
            </div>
          </section>
        </div>

        <!-- Tab 3: NPK 资源库 -->
        <div v-show="activeTab === 'npk'" class="settings-tab-panel">
          <!-- NPK 概览说明 -->
          <div class="hero-card">
            <div class="hero-card-icon">
              <NIcon :size="28"><Image24Regular /></NIcon>
            </div>
            <div class="hero-card-body">
              <div class="hero-card-header">
                <span class="hero-card-title">NPK 游戏图像资源库</span>
                <NTag
                  v-if="images.status.state === 'ready'"
                  type="success"
                  size="small"
                  round
                  :bordered="false"
                >
                  <template #icon><NIcon><CheckmarkCircle24Regular /></NIcon></template>
                  索引就绪
                </NTag>
                <NTag
                  v-else-if="images.status.state === 'building'"
                  type="info"
                  size="small"
                  round
                  :bordered="false"
                >
                  <template #icon><NIcon class="spinning"><ArrowSync24Regular /></NIcon></template>
                  扫描中
                </NTag>
                <NTag
                  v-else-if="images.status.state === 'error'"
                  type="error"
                  size="small"
                  round
                  :bordered="false"
                >
                  <template #icon><NIcon><DismissCircle24Regular /></NIcon></template>
                  索引异常
                </NTag>
                <NTag v-else size="small" round :bordered="false">未配置目录</NTag>
              </div>
              <div class="hero-card-desc">
                配置 DNF 客户端的 ImagePacks2 目录后，pvfine 可实时解析武器、装备、消耗品和技能的原版图标，并在脚本代码中提供直观的悬浮图像预览。
              </div>
            </div>
          </div>

          <!-- 目录选择卡片 -->
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">NPK 目录路径</div>
            </div>

            <div class="settings-card">
              <div class="npk-path-row">
                <div class="npk-path-icon">
                  <NIcon :size="20"><FolderOpen24Regular /></NIcon>
                </div>
                <div class="npk-path-text-wrapper" :title="settings.npkDirectory || ''">
                  <span v-if="settings.npkDirectory" class="npk-path-text">
                    {{ settings.npkDirectory }}
                  </span>
                  <span v-else class="npk-path-placeholder">
                    尚未选择目录，请点击右侧按钮选择客户端 ImagePacks2 文件夹
                  </span>
                </div>
                <div class="npk-path-actions">
                  <NTooltip v-if="settings.npkDirectory" trigger="hover">
                    <template #trigger>
                      <NButton
                        quaternary
                        size="small"
                        aria-label="复制路径"
                        @click="copyNPKDirectory"
                      >
                        <template #icon><NIcon><Copy24Regular /></NIcon></template>
                      </NButton>
                    </template>
                    复制目录路径
                  </NTooltip>
                  <NButton
                    secondary
                    size="small"
                    :loading="selectingNPK"
                    @click="onSelectNPKDirectory"
                  >
                    <template #icon><NIcon><FolderOpen24Regular /></NIcon></template>
                    {{ settings.npkDirectory ? "更改目录" : "选择目录" }}
                  </NButton>
                </div>
              </div>

              <!-- 正在扫描时：进度条 -->
              <div v-if="images.status.state === 'building'" class="npk-progress-card">
                <div class="npk-progress-header">
                  <span class="npk-progress-title">
                    {{ images.status.total > 0 ? `正在扫描 NPK 资源 (${images.status.done}/${images.status.total})` : "正在准备扫描资源包..." }}
                  </span>
                  <span class="npk-progress-percent">{{ npkProgressPercent }}%</span>
                </div>
                <div class="npk-progress-track">
                  <div
                    class="npk-progress-bar"
                    :style="{ width: `${npkProgressPercent}%` }"
                  />
                </div>
              </div>

              <!-- 就绪时：数据指标网格 -->
              <div v-else-if="images.status.state === 'ready'" class="npk-metrics-grid">
                <div class="metric-item">
                  <div class="metric-value">{{ images.status.npkFiles.toLocaleString() }}</div>
                  <div class="metric-label">NPK 资源包</div>
                </div>
                <div class="metric-item">
                  <div class="metric-value">{{ images.status.imgFiles.toLocaleString() }}</div>
                  <div class="metric-label">IMG 镜像文件</div>
                </div>
                <div class="metric-item">
                  <div class="metric-value">{{ images.status.imageCount.toLocaleString() }}</div>
                  <div class="metric-label">有效图标资产</div>
                </div>
              </div>

              <div
                v-if="images.status.state === 'ready' && (images.status.skipped > 0 || images.status.duplicates > 0)"
                class="npk-notes-row"
              >
                <NIcon :size="14" class="notes-icon"><Info24Regular /></NIcon>
                <span>
                  {{
                    [
                      images.status.skipped > 0 ? `跳过 ${images.status.skipped} 项非标准文件` : "",
                      images.status.duplicates > 0 ? `去重 ${images.status.duplicates} 项重复资源` : "",
                    ].filter(Boolean).join("，")
                  }}
                </span>
              </div>

              <!-- 异常告警 -->
              <div v-else-if="images.status.state === 'error'" class="npk-error-box">
                <NAlert type="error" :bordered="false" title="图标索引建立失败">
                  {{ images.status.error || "扫描过程中发生未知错误，请检查目录权限或文件是否被占用" }}
                </NAlert>
              </div>

              <div class="setting-card-divider" />

              <!-- 重建按钮 -->
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><ArrowSync24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">重建全量图标索引</div>
                  <div class="setting-item-desc">当客户端更新补丁、新增 NPK 或图标出现缺失时，可点击重建全量映射</div>
                </div>
                <div class="setting-item-control">
                  <NButton
                    size="small"
                    secondary
                    :loading="rebuildingNPK"
                    :disabled="!images.status.directory || images.status.state === 'building'"
                    @click="onRebuildNPKIndex"
                  >
                    <template #icon><NIcon><ArrowSync24Regular /></NIcon></template>
                    重建索引
                  </NButton>
                </div>
              </div>
            </div>
          </section>
        </div>

        <!-- Tab 4: 系统维护 -->
        <div v-show="activeTab === 'system'" class="settings-tab-panel">
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">维护与热重载</div>
              <div class="group-subtitle">快速维护本地标注与脚本渲染规则</div>
            </div>

            <div class="settings-card">
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><DocumentSync24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">标注规则热重载</div>
                  <div class="setting-item-desc">从磁盘重新加载标注规则定义与关联数据类型。编辑了本地规则文件后无需重启应用，点击即可即时同步刷新。</div>
                </div>
                <div class="setting-item-control">
                  <NButton
                    size="small"
                    secondary
                    :loading="reloadingAnnotations"
                    aria-label="重载标注规则"
                    @click="onReloadAnnotations"
                  >
                    <template #icon><NIcon><DocumentSync24Regular /></NIcon></template>
                    立即重载
                  </NButton>
                </div>
              </div>

              <div class="setting-card-divider" />

              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><DocumentSync24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">渲染规则热重载</div>
                  <div class="setting-item-desc">从磁盘重新加载脚本展示格式，并刷新当前编辑器中的未修改文件。</div>
                </div>
                <div class="setting-item-control">
                  <NButton
                    size="small"
                    secondary
                    :loading="reloadingRendering"
                    aria-label="重载渲染规则"
                    @click="onReloadRendering"
                  >
                    <template #icon><NIcon><DocumentSync24Regular /></NIcon></template>
                    立即重载
                  </NButton>
                </div>
              </div>

            </div>
          </section>

          <!-- 软件更新 -->
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">软件更新</div>
              <div class="group-subtitle">
                正式版走官方更新源；「开发人员专用」指向测试通道，用于测试更新流程，两者互不影响
              </div>
            </div>

            <div class="settings-card">
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><ArrowSync24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">更新通道</div>
                  <div class="setting-item-desc">切换后需重启应用生效</div>
                </div>
                <div class="setting-item-control">
                  <NRadioGroup
                    :value="settings.updateChannel"
                    size="small"
                    @update:value="onUpdateChannelChange"
                  >
                    <NRadioButton value="stable">正式版</NRadioButton>
                    <NRadioButton value="dev">开发人员专用</NRadioButton>
                  </NRadioGroup>
                </div>
              </div>

              <div class="setting-card-divider" />

              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><ArrowSync24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">检查更新</div>
                  <div class="setting-item-desc">立即向当前通道的更新源查询新版本</div>
                </div>
                <div class="setting-item-control">
                  <NButton
                    size="small"
                    secondary
                    :loading="checkingUpdates"
                    aria-label="检查更新"
                    @click="onCheckUpdates"
                  >
                    <template #icon><NIcon><ArrowSync24Regular /></NIcon></template>
                    检查更新
                  </NButton>
                </div>
              </div>
            </div>
          </section>

          <!-- MCP 只读服务 -->
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">MCP 只读服务</div>
              <div class="group-subtitle">
                以 Model Context Protocol 向外部 AI 客户端开放本机只读归档工具（不开放任何写能力）
              </div>
            </div>

            <div class="settings-card">
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><Bot24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label">开启 MCP 只读服务</div>
                  <div class="setting-item-desc">开启后应用启动时监听本机回环地址（默认 127.0.0.1:17650），仅提供读取类工具；切换后需重启应用生效</div>
                </div>
                <div class="setting-item-control">
                  <NSwitch :value="settings.mcpEnabled" @update:value="onMcpEnabledChange" />
                </div>
              </div>
            </div>
          </section>

          <!-- 关于应用信息 -->
          <div class="about-card">
            <div class="about-logo-row">
              <div class="about-brand">pvfine</div>
              <NTag size="small" round :bordered="false" type="primary">{{ BUILD_LABEL }}</NTag>
            </div>
            <div class="about-desc">
              基于 Wails 3、Go 与 Vue 3 构建的高性能、现代化的 DNF PVF 脚本交互式编辑工具。
            </div>
            <div class="about-badges">
              <span class="about-pill">Go 1.24</span>
              <span class="about-pill">Vue 3</span>
              <span class="about-pill">CodeMirror 6</span>
              <span class="about-pill">Naive UI</span>
            </div>
          </div>
        </div>

        <!-- Tab 5: AI 助手 -->
        <div v-show="activeTab === 'ai'" class="settings-tab-panel">
          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">模型接入</div>
            </div>
            <div class="settings-card">
              <div class="setting-item">
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">启用 AI 助手</span>
                  </div>
                  <div class="setting-item-desc">不内置、不锁死任何模型：填一个 OpenAI 兼容接入点即可（DeepSeek / 通义 / Kimi / Ollama / 自定义网关）</div>
                </div>
                <div class="setting-item-control">
                  <NSwitch :value="settings.ai.enabled" @update:value="onAIEnabledChange" />
                </div>
              </div>
              <div class="setting-item">
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">服务商</span>
                  </div>
                  <div class="setting-item-desc">选择预设会自动带出接入地址与默认模型，可再手改</div>
                </div>
                <div class="setting-item-control">
                  <NSelect
                    size="small"
                    style="min-width: 220px"
                    :value="settings.ai.provider"
                    :options="aiProviderOptions"
                    @update:value="onAIProviderChange"
                  />
                </div>
              </div>
              <div class="setting-item">
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">接入地址（Base URL）</span>
                  </div>
                  <div class="setting-item-desc">OpenAI 兼容 endpoint，例如 https://api.deepseek.com/v1</div>
                </div>
                <div class="setting-item-control" style="min-width: 280px">
                  <NInput
                    size="small"
                    :value="aiBaseURLDraft"
                    placeholder="https://api.deepseek.com/v1"
                    @update:value="aiBaseURLDraft = $event"
                    @blur="commitAIDrafts(false)"
                    @keydown.enter.prevent="commitAIDrafts(true)"
                  />
                </div>
              </div>
              <div class="setting-item">
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">模型</span>
                  </div>
                  <div class="setting-item-desc">如 deepseek-chat、qwen-plus、moonshot-v1-8k；Ollama 填本机模型名</div>
                </div>
                <div class="setting-item-control" style="min-width: 280px">
                  <NInput
                    size="small"
                    :value="aiModelDraft"
                    placeholder="deepseek-chat"
                    @update:value="aiModelDraft = $event"
                    @blur="commitAIDrafts(false)"
                  />
                </div>
              </div>
              <div class="setting-item">
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">API Key</span>
                  </div>
                  <div class="setting-item-desc">只保存在本机设置文件里，不会随日志或对话上传；本地服务（如 Ollama）可留空</div>
                </div>
                <div class="setting-item-control" style="min-width: 280px">
                  <div style="display: flex; gap: 8px; align-items: center">
                    <NInput
                      size="small"
                      type="password"
                      show-password-on="click"
                      :value="aiAPIKeyDraft"
                      placeholder="sk-..."
                      @update:value="aiAPIKeyDraft = $event"
                      @blur="commitAIDrafts(false)"
                      @keydown.enter.prevent="commitAIDrafts(true)"
                    />
                    <NButton size="small" secondary @click="onDeleteAPIKey">删除密钥</NButton>
                  </div>
                </div>
              </div>
              <div class="setting-item">
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">保存配置</span>
                  </div>
                  <div class="setting-item-desc">提交以上接入地址、模型与 API Key；在输入框内按回车、或点击其它位置（失焦）时也会自动保存</div>
                </div>
                <div class="setting-item-control">
                  <NButton size="small" type="primary" :loading="settings.saving" @click="commitAIDrafts(true)">
                    保存 AI 配置
                  </NButton>
                </div>
              </div>

              <div class="setting-item">
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">测试连接</span>
                  </div>
                  <div class="setting-item-desc">用当前配置发一次最小对话，验证接入点与 Key 可用</div>
                </div>
                <div class="setting-item-control">
                  <NButton size="small" secondary :loading="checkingAI" @click="onTestAIConnection">
                    测试连接
                  </NButton>
                </div>
              </div>
            </div>
          </section>

          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">安全</div>
            </div>
            <div class="settings-card">
              <div class="setting-item">
                <div class="setting-item-icon">
                  <NIcon :size="18"><ShieldLock24Regular /></NIcon>
                </div>
                <div class="setting-item-content">
                  <div class="setting-item-label-row">
                    <span class="setting-item-label">AI 写保护</span>
                    <NTag size="small" round :bordered="false" type="warning">默认开启</NTag>
                  </div>
                  <div class="setting-item-desc">开启时 AI 只能读取和检索，任何修改都会被拒绝；关闭后 AI 可修改文件，但仍受字符串表写保护与保存前备份约束</div>
                </div>
                <div class="setting-item-control">
                  <NSwitch :value="settings.ai.writeProtection" @update:value="onAIWriteProtectionChange" />
                </div>
              </div>
            </div>
          </section>

          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">内置快捷命令提示词</div>
            </div>
            <div class="settings-card" style="padding: 12px">
              <div class="setting-item-desc">
                修改快捷命令点击后填入的提示词（留空表示用默认值）
              </div>
              <div v-for="action in BUILTIN_QUICK_ACTIONS" :key="action.id" class="quick-prompt-row">
                <div class="quick-prompt-label">{{ action.label }}</div>
                <NInput
                  size="small"
                  :value="aiQuickActionDrafts[action.id] ?? ''"
                  :placeholder="action.prompt"
                  @update:value="(value: string) => setQuickActionDraft(action.id, value)"
                  @blur="commitQuickActionPrompts(false)"
                />
              </div>
              <div style="margin-top: 8px">
                <NButton size="small" type="primary" :loading="settings.saving" @click="commitQuickActionPrompts(true)">
                  保存提示词
                </NButton>
              </div>
            </div>
          </section>

          <section class="settings-group">
            <div class="group-header">
              <div class="group-title">自定义快捷命令</div>
            </div>
            <div class="settings-card" style="padding: 12px">
              <div class="setting-item-label-row">
                <span class="setting-item-label">快捷命令</span>
              </div>
              <div class="setting-item-desc">
                每行一条，格式「标签|提示词」；会显示在 AI 面板输入框上方，点击即填入提问模板（空行忽略）
              </div>
              <NInput
                v-model:value="aiCustomActionsDraft"
                type="textarea"
                :rows="4"
                placeholder="例如：检查装备字段|当前文件里的 [name]、[grade] 字段分别是什么意思？"
                @blur="commitCustomActions(false)"
              />
              <div style="margin-top: 8px">
                <NButton size="small" type="primary" :loading="settings.saving" @click="commitCustomActions(true)">
                  保存命令
                </NButton>
              </div>
            </div>
          </section>
        </div>
      </div>
    </NSpin>
  </NModal>
</template>

<style scoped>
/* 内置快捷命令提示词编辑行 */
.quick-prompt-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 8px;
}
.quick-prompt-label {
  flex: none;
  width: 96px;
  font-size: 13px;
  opacity: 0.85;
}
/* 弹窗头部 */
.settings-modal-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
  color: var(--pvf-text-primary);
}
.settings-modal-header-icon {
  color: var(--pvf-primary);
}

.settings-container {
  min-height: 440px;
}

/* 顶部导航分段栏 */
.settings-nav {
  display: flex;
  gap: 4px;
  padding: 3px;
  margin-bottom: 20px;
  background: var(--pvf-surface-subtle);
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 9px;
}
.settings-nav-item {
  position: relative;
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  padding: 8px 12px;
  background: transparent;
  border: none;
  border-radius: 7px;
  color: var(--pvf-text-secondary);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.16s ease;
  user-select: none;
}
.settings-nav-item:hover {
  color: var(--pvf-text-primary);
  background: var(--pvf-surface-hover);
}
.settings-nav-item:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px var(--pvf-effect-focus-ring);
}
.settings-nav-item--active {
  color: var(--pvf-text-primary);
  background: var(--pvf-surface-card);
  font-weight: 600;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1), 0 0 0 1px var(--pvf-border-faint);
}
.settings-nav-icon {
  display: flex;
  align-items: center;
}
.settings-nav-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  transition: background-color 0.2s ease;
}
.settings-nav-dot--ready {
  background-color: var(--pvf-success);
}
.settings-nav-dot--building {
  background-color: var(--pvf-primary);
  animation: pulse-dot 1.2s infinite ease-in-out;
}
.settings-nav-dot--error {
  background-color: var(--pvf-error);
}

@keyframes pulse-dot {
  0%, 100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.4;
    transform: scale(1.3);
  }
}

/* 分组通用样式 */
.settings-tab-panel {
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.settings-group {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.group-header {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.group-title {
  color: var(--pvf-text-primary);
  font-size: 13px;
  font-weight: 600;
}
.group-subtitle {
  color: var(--pvf-text-faint);
  font-size: 12px;
}

/* 卡片容器 */
.settings-card {
  background: var(--pvf-surface-card);
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 10px;
  overflow: hidden;
}
.setting-card-divider {
  height: 1px;
  margin: 0 16px;
  background: var(--pvf-border-faint);
}

/* 设置项行 */
.setting-item {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px 16px;
}
.setting-item-icon {
  flex: 0 0 34px;
  height: 34px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--pvf-surface-subtle);
  border: 1px solid var(--pvf-border-faint);
  border-radius: 8px;
  color: var(--pvf-primary);
}
.setting-item-content {
  flex: 1;
  min-width: 0;
}
.setting-item-label-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.setting-item-label {
  color: var(--pvf-text-primary);
  font-size: 13px;
  font-weight: 500;
}
.setting-item-desc {
  margin-top: 3px;
  color: var(--pvf-text-faint);
  font-size: 12px;
  line-height: 1.45;
}
.setting-item-desc code {
  padding: 1px 4px;
  background: var(--pvf-surface-code);
  border-radius: 3px;
  font-family: ui-monospace, monospace;
}
.setting-item-control {
  flex: 0 0 auto;
}

/* 主题选择网格 */
.theme-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 12px;
}
.theme-card {
  display: flex;
  flex-direction: column;
  padding: 10px;
  background: var(--pvf-surface-card);
  border: 1.5px solid var(--pvf-border-subtle);
  border-radius: 10px;
  cursor: pointer;
  transition: all 0.18s ease;
  user-select: none;
  text-align: left;
}
.theme-card:hover {
  border-color: var(--pvf-primary-hover);
  background: var(--pvf-surface-hover);
  transform: translateY(-1px);
}
.theme-card:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px var(--pvf-effect-focus-ring);
}
.theme-card--active {
  border-color: var(--pvf-primary);
  background: var(--pvf-primary-soft);
  box-shadow: 0 0 0 1px var(--pvf-primary);
}
.theme-preview {
  height: 60px;
  border-radius: 6px;
  border: 1px solid rgba(128, 128, 128, 0.2);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  margin-bottom: 10px;
}
.theme-preview--dark {
  background: #181a20;
}
.theme-preview--light {
  background: #f3f5f8;
}
.preview-titlebar {
  height: 16px;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 6px;
  background: rgba(128, 128, 128, 0.08);
  border-bottom: 1px solid rgba(128, 128, 128, 0.15);
}
.preview-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
}
.dot-red { background: #ff5f56; }
.dot-yellow { background: #ffbd2e; }
.dot-green { background: #27c93f; }

.preview-body {
  flex: 1;
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.preview-body--split {
  padding: 0;
  flex-direction: row;
}
.split-half {
  flex: 1;
  padding: 8px 6px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.split-half--light {
  background: #ffffff;
}
.split-half--dark {
  background: #1e2129;
}
.preview-line {
  height: 4px;
  border-radius: 2px;
}
.line-short { width: 40%; }
.line-long { width: 85%; }
.line-med { width: 60%; }
.accent-blue { background: #4f8cff; }
.accent-green { background: #63e2b7; }
.accent-muted { background: rgba(128, 128, 128, 0.3); }

.theme-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 4px;
}
.theme-card-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--pvf-text-primary);
}
.theme-check-icon {
  color: var(--pvf-primary);
}

/* 标注排版预览框 */
.preview-annotation-box {
  padding: 12px 16px 14px;
  background: var(--pvf-surface-subtle);
  border-top: 1px solid var(--pvf-border-faint);
}
.preview-annotation-title {
  font-size: 11px;
  font-weight: 500;
  color: var(--pvf-text-faint);
  margin-bottom: 8px;
}
.preview-code-block {
  padding: 8px 12px;
  background: var(--pvf-surface-code);
  border: 1px solid var(--pvf-border-faint);
  border-radius: 6px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
.preview-code-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.preview-code-row--line-end {
  justify-content: space-between;
}
.code-key {
  color: var(--pvf-primary);
  font-weight: 600;
}
.code-string {
  color: var(--pvf-editor-syntax-string);
}
.preview-code-muted {
  color: var(--pvf-text-faint);
  font-size: 11px;
  font-family: -apple-system, BlinkMacSystemFont, sans-serif;
}
.preview-tag {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  font-size: 11px;
  border-radius: 3px;
  font-family: -apple-system, BlinkMacSystemFont, sans-serif;
}
.preview-tag--blue {
  background: var(--pvf-editor-annotation-surface);
  color: var(--pvf-editor-annotation-text);
  border: 1px solid var(--pvf-editor-annotation-border);
}
.preview-tag--end {
  margin-left: auto;
}

/* NPK Hero 说明卡片 */
.hero-card {
  display: flex;
  gap: 16px;
  padding: 16px;
  background: var(--pvf-surface-elevated);
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 10px;
}
.hero-card-icon {
  flex: 0 0 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--pvf-primary-soft);
  color: var(--pvf-primary);
  border-radius: 10px;
}
.hero-card-body {
  flex: 1;
  min-width: 0;
}
.hero-card-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 4px;
}
.hero-card-title {
  color: var(--pvf-text-primary);
  font-size: 14px;
  font-weight: 600;
}
.hero-card-desc {
  color: var(--pvf-text-secondary);
  font-size: 12px;
  line-height: 1.5;
}

/* NPK 路径选择行 */
.npk-path-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
}
.npk-path-icon {
  color: var(--pvf-text-faint);
  display: flex;
  align-items: center;
}
.npk-path-text-wrapper {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.npk-path-text {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--pvf-text-primary);
  user-select: text;
  -webkit-user-select: text;
}
.npk-path-placeholder {
  font-size: 12px;
  color: var(--pvf-text-faint);
  font-style: italic;
}
.npk-path-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
}

/* NPK 扫描进度条 */
.npk-progress-card {
  padding: 12px 16px 16px;
  background: var(--pvf-surface-subtle);
  border-top: 1px solid var(--pvf-border-faint);
}
.npk-progress-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  font-size: 12px;
}
.npk-progress-title {
  color: var(--pvf-primary);
  font-weight: 500;
}
.npk-progress-percent {
  color: var(--pvf-text-muted);
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}
.npk-progress-track {
  height: 6px;
  background: var(--pvf-surface-inset);
  border-radius: 3px;
  overflow: hidden;
}
.npk-progress-bar {
  height: 100%;
  background: var(--pvf-primary);
  border-radius: 3px;
  transition: width 0.25s ease-out;
}

/* NPK 统计网格 */
.npk-metrics-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1px;
  background: var(--pvf-border-faint);
  border-top: 1px solid var(--pvf-border-faint);
}
.metric-item {
  padding: 12px 16px;
  background: var(--pvf-surface-card);
  text-align: center;
}
.metric-value {
  font-size: 18px;
  font-weight: 600;
  color: var(--pvf-text-primary);
  font-variant-numeric: tabular-nums;
}
.metric-label {
  margin-top: 2px;
  font-size: 11px;
  color: var(--pvf-text-faint);
}
.npk-notes-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  background: var(--pvf-surface-subtle);
  color: var(--pvf-text-faint);
  font-size: 11px;
  border-top: 1px solid var(--pvf-border-faint);
}
.notes-icon {
  color: var(--pvf-info);
}

.npk-error-box {
  padding: 12px 16px;
  border-top: 1px solid var(--pvf-border-faint);
}

/* 关于卡片 */
.about-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px;
  background: var(--pvf-surface-subtle);
  border: 1px solid var(--pvf-border-faint);
  border-radius: 10px;
}
.about-logo-row {
  display: flex;
  align-items: center;
  gap: 10px;
}
.about-brand {
  font-size: 16px;
  font-weight: 700;
  color: var(--pvf-text-primary);
  letter-spacing: -0.2px;
}
.about-desc {
  color: var(--pvf-text-secondary);
  font-size: 12px;
  line-height: 1.45;
}
.about-badges {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 4px;
}
.about-pill {
  display: inline-flex;
  padding: 2px 8px;
  background: var(--pvf-surface-card);
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 4px;
  font-size: 11px;
  color: var(--pvf-text-muted);
}
.about-update-row {
  display: flex;
  margin-top: 4px;
}

/* 动画 */
.spinning {
  animation: spin 1s linear infinite;
}
@keyframes spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 600px) {
  .theme-grid {
    grid-template-columns: 1fr;
  }
  .settings-nav {
    flex-wrap: wrap;
  }
  .settings-nav-item {
    flex: 1 1 45%;
  }
  .npk-metrics-grid {
    grid-template-columns: 1fr;
  }
}
</style>
