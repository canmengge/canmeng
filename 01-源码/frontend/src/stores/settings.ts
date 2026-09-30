import { defineStore } from "pinia";
import { ref } from "vue";
import { SettingsService } from "../../bindings/pvfine/services";
import type { AIAssistantSettings, AICustomAction, AppSettings } from "../../bindings/pvfine/services/models";
import type { ThemeMode } from "../theme";

export type AnnotationTagPlacement = "after-target" | "line-end" | "hidden";
export type ExplorerOpenMode = "single-click" | "double-click";
export type SettingsTab = "general" | "editor" | "npk" | "system" | "ai";
/** 更新通道：stable = 正式更新源；dev = 开发人员专用测试源。 */
export type UpdateChannel = "stable" | "dev";
export type { ThemeMode } from "../theme";

/** AI 服务商预设（与后端 AI镶嵌.md §四 一致）；custom = 用户自填 OpenAI 兼容地址。 */
export const AI_PROVIDER_CHOICES: { label: string; value: string; baseURL: string; model: string }[] = [
  { label: "自定义（OpenAI 兼容地址）", value: "custom", baseURL: "", model: "" },
  { label: "DeepSeek", value: "deepseek", baseURL: "https://api.deepseek.com/v1", model: "deepseek-chat" },
  { label: "通义千问", value: "qwen", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", model: "qwen-plus" },
  { label: "Kimi", value: "kimi", baseURL: "https://api.moonshot.cn/v1", model: "moonshot-v1-8k" },
  { label: "OpenAI", value: "openai", baseURL: "https://api.openai.com/v1", model: "gpt-4o-mini" },
  { label: "Ollama（本机）", value: "ollama", baseURL: "http://localhost:11434/v1", model: "" },
];

const defaultAIAssistantSettings: AIAssistantSettings = {
  enabled: false,
  provider: "custom",
  baseURL: "",
  model: "",
  apiKey: "",
  writeProtection: true,
  customActions: [],
  quickActionPrompts: {},
};

const defaultSettings: AppSettings = {
  annotationTagPlacement: "after-target",
  explorerOpenMode: "single-click",
  vimMode: false,
  backupSourceOnSave: true,
  npkDirectory: "",
  theme: "dark",
  protectedStringTableGuard: false,
  ai: { ...defaultAIAssistantSettings },
  updateChannel: "stable",
  mcpEnabled: false,
  mcpWriteEnabled: false,
};

export const useSettingsStore = defineStore("settings", () => {
  const visible = ref(false);
  const activeTab = ref<SettingsTab>("general");
  const loaded = ref(false);
  const saving = ref(false);
  const annotationTagPlacement = ref<AnnotationTagPlacement>("after-target");
  const explorerOpenMode = ref<ExplorerOpenMode>("single-click");
  const vimMode = ref(false);
  const backupSourceOnSave = ref(true);
  const npkDirectory = ref("");
  const themeMode = ref<ThemeMode>("dark");
  const protectedStringTableGuard = ref(false);
  const ai = ref<AIAssistantSettings>({ ...defaultAIAssistantSettings });
  const updateChannel = ref<UpdateChannel>("stable");
  const mcpEnabled = ref(false);
  const mcpWriteEnabled = ref(false);
  /** 编辑器内「绿色关联框」（ID 关联标签）是否显示：Alt+Q 切换，仅本次会话有效。 */
  const showReferenceTags = ref(true);

  function toggleReferenceTags(): void {
    showReferenceTags.value = !showReferenceTags.value;
  }

  /**
   * 「纯文本模式」：把所有编辑器降级成记事本——关掉注解标签、语法着色、清单名称标签、
   * 补全与空白高亮，只留文本 + 行号 + 折行 + 查找。大文件（如几十万行的 list/*.lst）
   * 用它换取打开速度。工具条「纯文本」按钮或 Alt+T 切换，仅本次会话有效。
   */
  const plainTextMode = ref(false);

  function togglePlainTextMode(): void {
    plainTextMode.value = !plainTextMode.value;
  }

  async function load() {
    if (loaded.value) return;
    try {
      const settings = await SettingsService.GetSettings();
      annotationTagPlacement.value = normalizePlacement(settings.annotationTagPlacement);
      explorerOpenMode.value = normalizeExplorerOpenMode(settings.explorerOpenMode);
      vimMode.value = normalizeVimMode(settings.vimMode);
      backupSourceOnSave.value = normalizeBackupSourceOnSave(settings.backupSourceOnSave);
      npkDirectory.value = normalizeNPKDirectory(settings.npkDirectory);
      themeMode.value = normalizeThemeMode(settings.theme);
      protectedStringTableGuard.value = normalizeProtectedStringTableGuard(
        settings.protectedStringTableGuard
      );
      ai.value = normalizeAIAssistantSettings(settings.ai);
      updateChannel.value = normalizeUpdateChannel(settings.updateChannel);
      mcpEnabled.value = normalizeMcpEnabled(settings.mcpEnabled);
      mcpWriteEnabled.value = normalizeBool(settings.mcpWriteEnabled);
    } catch (error) {
      console.error("load settings failed", error);
      annotationTagPlacement.value = "after-target";
      explorerOpenMode.value = "single-click";
      vimMode.value = false;
      backupSourceOnSave.value = true;
      npkDirectory.value = "";
      themeMode.value = "dark";
      protectedStringTableGuard.value = false;
      ai.value = { ...defaultAIAssistantSettings };
      updateChannel.value = "stable";
      mcpEnabled.value = false;
      mcpWriteEnabled.value = false;
    } finally {
      loaded.value = true;
    }
  }

  async function savePlacement(value: AnnotationTagPlacement) {
    await saveSettings({
      annotationTagPlacement: value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: vimMode.value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
      mcpWriteEnabled: mcpWriteEnabled.value,
    });
  }

  async function saveExplorerOpenMode(value: ExplorerOpenMode) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: value,
      vimMode: vimMode.value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
      mcpWriteEnabled: mcpWriteEnabled.value,
    });
  }

  async function saveVimMode(value: boolean) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
      mcpWriteEnabled: mcpWriteEnabled.value,
    });
  }

  async function saveBackupSourceOnSave(value: boolean) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: vimMode.value,
      backupSourceOnSave: value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
      mcpWriteEnabled: mcpWriteEnabled.value,
    });
  }

  async function saveThemeMode(value: ThemeMode) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: vimMode.value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
      mcpWriteEnabled: mcpWriteEnabled.value,
    });
  }

  async function saveProtectedStringTableGuard(value: boolean) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: vimMode.value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
      mcpWriteEnabled: mcpWriteEnabled.value,
    });
  }

  async function saveAIAssistant(value: AIAssistantSettings) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: vimMode.value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      ai: value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
      mcpWriteEnabled: mcpWriteEnabled.value,
    });
  }

  async function saveUpdateChannel(value: UpdateChannel) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: vimMode.value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      ai: ai.value,
      updateChannel: value,
      mcpEnabled: mcpEnabled.value,
      mcpWriteEnabled: mcpWriteEnabled.value,
    });
  }

  async function saveMcpEnabled(value: boolean) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: vimMode.value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: value,
      mcpWriteEnabled: mcpWriteEnabled.value,
    });
  }

  async function saveMcpWriteEnabled(value: boolean) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: vimMode.value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
      mcpWriteEnabled: value,
    });
  }

  async function saveSettings(next: AppSettings) {
    const previousPlacement = annotationTagPlacement.value;
    const previousExplorerOpenMode = explorerOpenMode.value;
    const previousVimMode = vimMode.value;
    const previousBackupSourceOnSave = backupSourceOnSave.value;
    const previousNPKDirectory = npkDirectory.value;
    const previousThemeMode = themeMode.value;
    const previousProtectedStringTableGuard = protectedStringTableGuard.value;
    const previousAI = ai.value;
    const previousUpdateChannel = updateChannel.value;
    const previousMcpEnabled = mcpEnabled.value;
    const previousMcpWriteEnabled = mcpWriteEnabled.value;
    annotationTagPlacement.value = normalizePlacement(next.annotationTagPlacement);
    explorerOpenMode.value = normalizeExplorerOpenMode(next.explorerOpenMode);
    vimMode.value = normalizeVimMode(next.vimMode);
    backupSourceOnSave.value = normalizeBackupSourceOnSave(next.backupSourceOnSave);
    npkDirectory.value = normalizeNPKDirectory(next.npkDirectory);
    themeMode.value = normalizeThemeMode(next.theme);
    protectedStringTableGuard.value = normalizeProtectedStringTableGuard(
      next.protectedStringTableGuard
    );
    ai.value = normalizeAIAssistantSettings(next.ai);
    updateChannel.value = normalizeUpdateChannel(next.updateChannel);
    mcpEnabled.value = normalizeMcpEnabled(next.mcpEnabled);
    mcpWriteEnabled.value = normalizeBool(next.mcpWriteEnabled);
    saving.value = true;
    try {
      await SettingsService.SaveSettings({
        ...defaultSettings,
        ...next,
      });
    } catch (error) {
      annotationTagPlacement.value = previousPlacement;
      explorerOpenMode.value = previousExplorerOpenMode;
      vimMode.value = previousVimMode;
      backupSourceOnSave.value = previousBackupSourceOnSave;
      npkDirectory.value = previousNPKDirectory;
      themeMode.value = previousThemeMode;
      protectedStringTableGuard.value = previousProtectedStringTableGuard;
      ai.value = previousAI;
      updateChannel.value = previousUpdateChannel;
      mcpEnabled.value = previousMcpEnabled;
      mcpWriteEnabled.value = previousMcpWriteEnabled;
      throw error;
    } finally {
      saving.value = false;
    }
  }

  function open(tab?: SettingsTab) {
    if (tab && typeof tab === "string") {
      activeTab.value = tab;
    } else {
      activeTab.value = "general";
    }
    visible.value = true;
    void load();
  }

  return {
    visible,
    activeTab,
    loaded,
    saving,
    annotationTagPlacement,
    explorerOpenMode,
    vimMode,
    backupSourceOnSave,
    npkDirectory,
    themeMode,
    protectedStringTableGuard,
    ai,
    updateChannel,
    mcpEnabled,
    mcpWriteEnabled,
    showReferenceTags,
    toggleReferenceTags,
    plainTextMode,
    togglePlainTextMode,
    load,
    savePlacement,
    saveExplorerOpenMode,
    saveVimMode,
    saveBackupSourceOnSave,
    saveThemeMode,
    saveProtectedStringTableGuard,
    saveAIAssistant,
    saveUpdateChannel,
    saveMcpEnabled,
    saveMcpWriteEnabled,
    open,
  };
});

function normalizePlacement(value: string): AnnotationTagPlacement {
  if (value === "line-end" || value === "hidden") return value;
  return "after-target";
}

function normalizeExplorerOpenMode(value: string): ExplorerOpenMode {
  return value === "double-click" ? value : "single-click";
}

function normalizeUpdateChannel(value: string): UpdateChannel {
  return value === "dev" ? "dev" : "stable";
}

/** MCP 只读服务开关：只有显式 true 才算开启（默认关闭，与后端一致）。 */
function normalizeMcpEnabled(value: boolean): boolean {
  return value === true;
}

/** 通用布尔开关：只有显式 true 才算开启（用于 MCP 写能力等默认关闭项）。 */
function normalizeBool(value: boolean): boolean {
  return value === true;
}

function normalizeVimMode(value: boolean): boolean {
  return value === true;
}

function normalizeBackupSourceOnSave(value: boolean): boolean {
  return value !== false;
}

function normalizeNPKDirectory(value: string | null | undefined): string {
  return typeof value === "string" ? value.trim() : "";
}

function normalizeThemeMode(value: string | null | undefined): ThemeMode {
  if (value === "light" || value === "system") return value;
  return "dark";
}

/** 写保护开关：只有显式 true 才算开启（默认关闭，与后端 DefaultAppSettings 一致）。 */
function normalizeProtectedStringTableGuard(value: boolean): boolean {
  return value === true;
}

/** AI 配置：非法/缺失字段逐个回退默认（写保护只有显式 false 才算关）。 */
function normalizeAIAssistantSettings(value: AIAssistantSettings | null | undefined): AIAssistantSettings {
  if (!value || typeof value !== "object") return { ...defaultAIAssistantSettings };
  const provider =
    typeof value.provider === "string" &&
    AI_PROVIDER_CHOICES.some((choice) => choice.value === value.provider)
      ? value.provider
      : "custom";
  const text = (input: unknown): string => (typeof input === "string" ? input : "");
  return {
    enabled: value.enabled === true,
    provider,
    baseURL: text(value.baseURL).trim(),
    model: text(value.model).trim(),
    apiKey: text(value.apiKey),
    // AI 写保护默认开启（需求 1）：只有显式 false 才算关闭。
    writeProtection: value.writeProtection !== false,
    // 自定义快捷命令：只保留 label / prompt 都非空的条目（P3）。
    customActions: Array.isArray(value.customActions)
      ? value.customActions
          .map((item) => ({
            label: typeof item?.label === "string" ? item.label.trim() : "",
            prompt: typeof item?.prompt === "string" ? item.prompt : "",
          }))
          .filter((item) => item.label !== "" && item.prompt.trim() !== "")
      : [],
    quickActionPrompts: normalizeQuickActionPrompts(value.quickActionPrompts),
  };
}

/** 内置快捷命令提示词的覆盖表：只保留非空字符串值。 */
function normalizeQuickActionPrompts(value: unknown): Record<string, string> {
  if (!value || typeof value !== "object") return {};
  const result: Record<string, string> = {};
  for (const [key, prompt] of Object.entries(value as Record<string, unknown>)) {
    if (typeof prompt === "string" && prompt.trim() !== "") {
      result[key] = prompt;
    }
  }
  return result;
}
