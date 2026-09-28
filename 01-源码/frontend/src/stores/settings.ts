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

/** 默认文本编辑上限（MB），与后端 DefaultAppSettings 一致。 */
export const DEFAULT_TEXT_EDIT_LIMIT_MB = 8;

/** 文本编辑上限的可选项；0 表示不限（超大文件会占用大量内存并可能卡顿）。 */
export const TEXT_EDIT_LIMIT_CHOICES: { label: string; value: number }[] = [
  { label: "8 MB（默认）", value: 8 },
  { label: "16 MB", value: 16 },
  { label: "32 MB", value: 32 },
  { label: "64 MB", value: 64 },
  { label: "不限（大文件可能卡顿、占用大量内存）", value: 0 },
];

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
  textEditLimitMB: DEFAULT_TEXT_EDIT_LIMIT_MB,
  ai: { ...defaultAIAssistantSettings },
  updateChannel: "stable",
  mcpEnabled: false,
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
  const textEditLimitMB = ref<number>(DEFAULT_TEXT_EDIT_LIMIT_MB);
  const ai = ref<AIAssistantSettings>({ ...defaultAIAssistantSettings });
  const updateChannel = ref<UpdateChannel>("stable");
  const mcpEnabled = ref(false);

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
      textEditLimitMB.value = normalizeTextEditLimitMB(settings.textEditLimitMB);
      ai.value = normalizeAIAssistantSettings(settings.ai);
      updateChannel.value = normalizeUpdateChannel(settings.updateChannel);
      mcpEnabled.value = normalizeMcpEnabled(settings.mcpEnabled);
    } catch (error) {
      console.error("load settings failed", error);
      annotationTagPlacement.value = "after-target";
      explorerOpenMode.value = "single-click";
      vimMode.value = false;
      backupSourceOnSave.value = true;
      npkDirectory.value = "";
      themeMode.value = "dark";
      protectedStringTableGuard.value = false;
      textEditLimitMB.value = DEFAULT_TEXT_EDIT_LIMIT_MB;
      ai.value = { ...defaultAIAssistantSettings };
      updateChannel.value = "stable";
      mcpEnabled.value = false;
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
      textEditLimitMB: textEditLimitMB.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
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
      textEditLimitMB: textEditLimitMB.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
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
      textEditLimitMB: textEditLimitMB.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
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
      textEditLimitMB: textEditLimitMB.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
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
      textEditLimitMB: textEditLimitMB.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
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
      textEditLimitMB: textEditLimitMB.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
    });
  }

  async function saveTextEditLimitMB(value: number) {
    await saveSettings({
      annotationTagPlacement: annotationTagPlacement.value,
      explorerOpenMode: explorerOpenMode.value,
      vimMode: vimMode.value,
      backupSourceOnSave: backupSourceOnSave.value,
      npkDirectory: npkDirectory.value,
      theme: themeMode.value,
      protectedStringTableGuard: protectedStringTableGuard.value,
      textEditLimitMB: value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
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
      textEditLimitMB: textEditLimitMB.value,
      ai: value,
      updateChannel: updateChannel.value,
      mcpEnabled: mcpEnabled.value,
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
      textEditLimitMB: textEditLimitMB.value,
      ai: ai.value,
      updateChannel: value,
      mcpEnabled: mcpEnabled.value,
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
      textEditLimitMB: textEditLimitMB.value,
      ai: ai.value,
      updateChannel: updateChannel.value,
      mcpEnabled: value,
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
    const previousTextEditLimitMB = textEditLimitMB.value;
    const previousAI = ai.value;
    const previousUpdateChannel = updateChannel.value;
    const previousMcpEnabled = mcpEnabled.value;
    annotationTagPlacement.value = normalizePlacement(next.annotationTagPlacement);
    explorerOpenMode.value = normalizeExplorerOpenMode(next.explorerOpenMode);
    vimMode.value = normalizeVimMode(next.vimMode);
    backupSourceOnSave.value = normalizeBackupSourceOnSave(next.backupSourceOnSave);
    npkDirectory.value = normalizeNPKDirectory(next.npkDirectory);
    themeMode.value = normalizeThemeMode(next.theme);
    protectedStringTableGuard.value = normalizeProtectedStringTableGuard(
      next.protectedStringTableGuard
    );
    textEditLimitMB.value = normalizeTextEditLimitMB(next.textEditLimitMB);
    ai.value = normalizeAIAssistantSettings(next.ai);
    updateChannel.value = normalizeUpdateChannel(next.updateChannel);
    mcpEnabled.value = normalizeMcpEnabled(next.mcpEnabled);
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
      textEditLimitMB.value = previousTextEditLimitMB;
      ai.value = previousAI;
      updateChannel.value = previousUpdateChannel;
      mcpEnabled.value = previousMcpEnabled;
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
    textEditLimitMB,
    ai,
    updateChannel,
    mcpEnabled,
    load,
    savePlacement,
    saveExplorerOpenMode,
    saveVimMode,
    saveBackupSourceOnSave,
    saveThemeMode,
    saveProtectedStringTableGuard,
    saveTextEditLimitMB,
    saveAIAssistant,
    saveUpdateChannel,
    saveMcpEnabled,
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

/** 文本编辑上限：0 表示不限；非法值回退到默认 8MB。 */
function normalizeTextEditLimitMB(value: number | null | undefined): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    return DEFAULT_TEXT_EDIT_LIMIT_MB;
  }
  return Math.floor(value);
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
