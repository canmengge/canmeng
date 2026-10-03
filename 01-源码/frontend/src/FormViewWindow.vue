<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  dateZhCN,
  NConfigProvider,
  NDialogProvider,
  NMessageProvider,
  zhCN,
} from "naive-ui";
import FormView from "./components/FormView.vue";
import { LoadFormViewSession } from "./services/formViewWindowApi";
import { useFormViewStore } from "./stores/formView";
import { useSettingsStore } from "./stores/settings";
import {
  applyTheme,
  getTheme,
  makeThemeOverrides,
  resolveThemeMode,
} from "./theme";

/**
 * 结构化视图「独立窗口」的根组件。
 *
 * 与 ScriptWindow.vue 同一套做法：**刻意不挂** CloseGuard / ToolBar / Explorer
 * （那些属于主窗口，而且 app:close-requested 是广播事件，两个窗口都挂会让退出
 * 确认弹两次）。
 *
 * 与脚本窗口的区别：本窗口**只读**、没有未保存内容，因此关窗走原生关闭即可
 * （Go 侧关窗钩子直接放行），不需要二次确认，也不需要把状态交回主窗口。
 */

const formView = useFormViewStore();
const settings = useSettingsStore();

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

function onSystemThemeChange(event: MediaQueryListEvent): void {
  systemPrefersDark.value = event.matches;
}

/** 启动错误（例如主进程里没有打开归档之外的问题）。 */
const bootError = ref("");

onMounted(() => {
  void (async () => {
    try {
      // 主题来自后端设置，独立窗口必须自己加载。
      await settings.load();
      // 看哪个文件族的参数由主窗口暂存在 Go 侧（每个 webview 各一份 Pinia）。
      await formView.initFromSession(await LoadFormViewSession());
    } catch (issue: any) {
      bootError.value = String(issue?.message ?? issue);
    }
  })();
  systemThemeQuery.addEventListener("change", onSystemThemeChange);
});

onUnmounted(() => {
  systemThemeQuery.removeEventListener("change", onSystemThemeChange);
});
</script>

<template>
  <NConfigProvider
    :theme="activeTheme.naiveTheme"
    :theme-overrides="themeOverrides"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >
    <NMessageProvider placement="bottom-right">
      <NDialogProvider>
        <div class="fv-window-root">
          <div v-if="bootError" class="fv-window-error">{{ bootError }}</div>
          <FormView v-else />
        </div>
      </NDialogProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>

<style scoped>
.fv-window-root {
  display: flex;
  flex-direction: column;
  width: 100vw;
  height: 100vh;
  /* min-height:0 是让内部滚动区真正能滚的关键（flex 子项默认 min-height:auto）。 */
  min-height: 0;
  overflow: hidden;
  background: var(--pvf-window-background-solid, #181a20);
  color: var(--pvf-text-primary, #e6e6e6);
}

.fv-window-error {
  padding: 16px;
  font-size: 12px;
}
</style>
