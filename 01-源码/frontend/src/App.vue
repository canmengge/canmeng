<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  dateZhCN,
  NConfigProvider,
  NDialogProvider,
  NMessageProvider,
  zhCN,
} from "naive-ui";
import ToolBar from "./components/ToolBar.vue";
import Explorer from "./components/Explorer.vue";
import EditorTabs from "./components/EditorTabs.vue";
import FileSetSidebar from "./components/FileSetSidebar.vue";
import StatusBar from "./components/StatusBar.vue";
import AdvancedSearchModal from "./components/AdvancedSearchModal.vue";
import AnnotationEditModal from "./components/AnnotationEditModal.vue";
import BatchProcessModal from "./components/BatchProcessModal.vue";
import ImportModal from "./components/ImportModal.vue";
import VersionPanel from "./components/VersionPanel.vue";
import SettingsModal from "./components/SettingsModal.vue";
import UpdatePrompt from "./components/UpdatePrompt.vue";
import CommandPalette from "./components/CommandPalette.vue";
import CloseGuard from "./components/CloseGuard.vue";
import EditorCloseGuard from "./components/EditorCloseGuard.vue";
import ArchiveErrorDialog from "./components/ArchiveErrorDialog.vue";
import LogPanel from "./components/LogPanel.vue";
import { useArchiveStore } from "./stores/archive";
import { useEditorStore } from "./stores/editor";
import { useFileSetStore } from "./stores/fileSets";
import { useBookmarkStore } from "./stores/bookmarks";
import { useSettingsStore } from "./stores/settings";
import { useVersionStore } from "./stores/version";
import { useImageStore } from "./stores/images";
import { useScriptStore } from "./stores/script";
import { useAdvancedSearchStore } from "./stores/advancedSearch";
import { useSidebarStore } from "./stores/sidebar";
import { useLogStore } from "./stores/log";
import { useDevStore } from "./stores/dev";
import { DEV_TOOLS } from "./buildInfo";
import {
  applyTheme,
  getTheme,
  makeThemeOverrides,
  resolveThemeMode,
} from "./theme";

const archive = useArchiveStore();
const editor = useEditorStore();
const fileSets = useFileSetStore();
const bookmarks = useBookmarkStore();
const settings = useSettingsStore();
const version = useVersionStore();
const images = useImageStore();
const script = useScriptStore();
const search = useAdvancedSearchStore();
const sidebar = useSidebarStore();
const palette = ref<{ open: () => void; close: () => void; toggle: () => void } | null>(null);
const isMac = /Macintosh|Mac OS X|MacIntel/i.test(
  `${navigator.platform} ${navigator.userAgent}`
);

const systemThemeQuery = window.matchMedia("(prefers-color-scheme: dark)");
const systemPrefersDark = ref(systemThemeQuery.matches);
const activeTheme = computed(() =>
  getTheme(resolveThemeMode(settings.themeMode, systemPrefersDark.value))
);
const themeOverrides = computed(() => makeThemeOverrides(activeTheme.value));

watch(
  activeTheme,
  (theme) => {
    applyTheme(theme);
  },
  { immediate: true }
);

const explorerWidth = ref(300);
const sidebarWidth = ref(332);
const resizing = ref(false);
const resizingSidebar = ref(false);

function onResizeStart() {
  resizing.value = true;
  document.body.style.cursor = "col-resize";
}
function onSidebarResizeStart() {
  resizingSidebar.value = true;
  document.body.style.cursor = "col-resize";
}
function onResizeMove(e: MouseEvent) {
  if (resizing.value) {
    explorerWidth.value = Math.min(560, Math.max(200, e.clientX));
  } else if (resizingSidebar.value) {
    sidebarWidth.value = Math.min(560, Math.max(240, window.innerWidth - e.clientX));
  }
}
function onResizeEnd() {
  if (!resizing.value && !resizingSidebar.value) return;
  resizing.value = false;
  resizingSidebar.value = false;
  document.body.style.cursor = "";
}

onMounted(() => {
  void (async () => {
    await settings.load();
    await images.initialize();
  })();
  void fileSets.load();
  void bookmarks.load();
  systemThemeQuery.addEventListener("change", onSystemThemeChange);
  window.addEventListener("mousemove", onResizeMove);
  window.addEventListener("mouseup", onResizeEnd);
  window.addEventListener("keydown", onKeydown);
});
onUnmounted(() => {
  window.removeEventListener("mousemove", onResizeMove);
  window.removeEventListener("mouseup", onResizeEnd);
  window.removeEventListener("keydown", onKeydown);
  systemThemeQuery.removeEventListener("change", onSystemThemeChange);
});

function onSystemThemeChange(event: MediaQueryListEvent): void {
  systemPrefersDark.value = event.matches;
}

async function onKeydown(e: KeyboardEvent) {
  // Alt+Q：显示 / 隐藏编辑器里的「绿色关联框」（ID 关联标签）。
  if (e.altKey && !e.ctrlKey && !e.metaKey && e.code === "KeyQ") {
    e.preventDefault();
    settings.toggleReferenceTags();
    return;
  }
  // Alt+T：切换「纯文本模式」（关掉注解 / 着色 / 名称标签，像记事本一样打开）。
  if (e.altKey && !e.ctrlKey && !e.metaKey && e.code === "KeyT") {
    e.preventDefault();
    settings.togglePlainTextMode();
    return;
  }
  const mod = e.metaKey || e.ctrlKey;
  if (!mod) return;
  if (e.code === "Backslash") {
    e.preventDefault();
    if (e.shiftKey) {
      editor.split("rows");
    } else {
      editor.split("columns");
    }
    return;
  }
  const key = e.key.toLowerCase();
  if (key === "f" && e.shiftKey) {
    // 全局高级搜索：焦点在编辑器里也能直接唤起。
    e.preventDefault();
    if (archive.open) search.open();
    return;
  }
  if (key === "b" && !e.shiftKey) {
    e.preventDefault();
    sidebar.toggle();
    return;
  }
  if (key === "j" && !e.shiftKey) {
    // 输出日志面板开关（与常见编辑器一致）。
    e.preventDefault();
    useLogStore().toggle();
    return;
  }
  if (key === "d" && e.shiftKey && DEV_TOOLS) {
    // 开发者面板开关（仅开发人员专用构建）。
    e.preventDefault();
    useDevStore().toggle();
    return;
  }
  if ((key === "p" || key === "k") && !e.shiftKey) {
    // 命令面板：Ctrl+P 打开文件，Ctrl+K 同入口（与常见编辑器一致）。
    e.preventDefault();
    palette.value?.toggle();
    return;
  }
  if (key === "enter") {
    if (script.workspaceVisible) {
      if (script.canRun) {
        e.preventDefault();
        void script.run();
      }
    } else if (version.canCommit) {
      e.preventDefault();
      void version.commit();
    }
  } else if (key === "o") {
    e.preventDefault();
    if (!archive.loading) await archive.openDialog();
  } else if (key === "s" && !e.shiftKey && script.workspaceVisible) {
    e.preventDefault();
    try {
      await script.saveScript();
    } catch {
      // 工作区已经展示了具体错误。
    }
  } else if (key === "s" && !e.shiftKey) {
    e.preventDefault();
    if (archive.open) await editor.saveActiveTab();
  } else if (key === "s" && e.shiftKey) {
    e.preventDefault();
    if (archive.open) await editor.saveAs();
  } else if (key === "w") {
    e.preventDefault();
    if (editor.activeKey !== null) editor.requestCloseTab(editor.activeKey, editor.activePaneId);
  }
}
</script>

<template>
  <NConfigProvider :theme="activeTheme.naiveTheme" :theme-overrides="themeOverrides" :locale="zhCN" :date-locale="dateZhCN">
    <NMessageProvider placement="bottom-right">
      <NDialogProvider>
        <CloseGuard />
        <EditorCloseGuard />
        <ArchiveErrorDialog />
        <div class="app-root" data-file-drop-target :class="{ 'app-root--mac': isMac }">
          <ToolBar />
          <AdvancedSearchModal />
          <AnnotationEditModal />
          <BatchProcessModal />
          <ImportModal />
          <VersionPanel />
          <SettingsModal />
          <UpdatePrompt />
          <CommandPalette ref="palette" />
          <div class="app-body">
            <div class="explorer-pane" :style="{ width: explorerWidth + 'px' }">
              <Explorer />
            </div>
            <div class="resize-handle" @mousedown.prevent="onResizeStart" />
            <div class="editor-column">
              <div class="editor-pane">
                <EditorTabs :theme-id="activeTheme.id" />
              </div>
              <!-- 输出日志停靠在编辑区底部（状态栏之上），高度可拖拽调节 -->
              <LogPanel />
            </div>
            <div class="resize-handle" @mousedown.prevent="onSidebarResizeStart" />
            <FileSetSidebar :style="{ '--pvf-sidebar-width': sidebarWidth + 'px' }" />
          </div>
          <StatusBar />
        </div>
      </NDialogProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>

<style scoped>
.app-root {
  display: flex;
  flex-direction: column;
  height: 100vh;
  min-height: 0;
  background: var(--pvf-window-background-solid);
}
.app-root--mac {
  background: var(--pvf-window-background-glass);
}
.app-body {
  flex: 1;
  display: flex;
  min-height: 0;
  overflow: hidden;
}
.explorer-pane {
  flex-shrink: 0;
  min-width: 200px;
  max-width: 560px;
  min-height: 0;
}
.resize-handle {
  width: 4px;
  margin: 0 -2px;
  cursor: col-resize;
  z-index: 10;
  flex-shrink: 0;
}
.resize-handle:hover {
  background: var(--pvf-effect-split-hover);
}
/* 编辑列：上方是编辑区（自适应），下方固定的日志面板（LogPanel 自己管高度） */
.editor-column {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
.editor-pane {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
}
.app-root--mac :deep(.toolbar) {
  padding-left: 82px;
}
</style>
