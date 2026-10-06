<script setup lang="ts">
import { computed, h, ref, watch, type Component } from "vue";
import { useMessage } from "naive-ui";
import {
  NButton,
  NDropdown,
  NIcon,
  NTooltip,
  NProgress,
  NText,
  useDialog,
  type DropdownOption,
} from "naive-ui";
import { Dismiss16Regular } from "@vicons/fluent";
import { useArchiveStore } from "../stores/archive";
import { useEditorStore } from "../stores/editor";
import { useAdvancedSearchStore } from "../stores/advancedSearch";
import { useSettingsStore } from "../stores/settings";
import { useImportStore } from "../stores/import";
import { useVersionStore } from "../stores/version";
import { useScriptStore } from "../stores/script";
import { useSidebarStore } from "../stores/sidebar";
import { useBookmarkStore, type BookmarkGroup } from "../stores/bookmarks";
import IndexHashRegistrationModal from "./IndexHashRegistrationModal.vue";
import BookmarkPopup from "./BookmarkPopup.vue";
import DevPanel from "./DevPanel.vue";
import { useDevStore } from "../stores/dev";
import { useFormViewStore } from "../stores/formView";
import { DEV_TOOLS } from "../buildInfo";
import { ArchiveService } from "../../bindings/pvfine/services";
import { Events } from "@wailsio/runtime";
import { CancelSave } from "../services/saveApi";
import { ExportFilesTo } from "../services/exportApi";
import ExportDialog from "./ExportDialog.vue";
import { useUnsavedChanges } from "../composables/unsavedChanges";
import { useEquipTemplateStore } from "../stores/equipTemplate";
import { PickKeyFileDialog } from "../services/keyApi";

const archive = useArchiveStore();
const editor = useEditorStore();
const advancedSearch = useAdvancedSearchStore();
const settings = useSettingsStore();
const importer = useImportStore();
const version = useVersionStore();
const script = useScriptStore();
const sidebar = useSidebarStore();
const bookmarks = useBookmarkStore();
const dev = useDevStore();
const formView = useFormViewStore();
const equipTemplate = useEquipTemplateStore();
const message = useMessage();
const dialog = useDialog();
const hashRegistrationVisible = ref(false);
const bookmarkPopupVisible = ref(false);
const bookmarkBtnRef = ref<HTMLElement | null>(null);
const exportingModified = ref(false);
const exportPickVisible = ref(false);
const exportPickPaths = ref<string[]>([]);

// ---------------------------------------------------------------------------
// 封包进度（事件 `save:progress`）
//
// 整包写回实测可达十几秒（543MB / 11.4s），期间必须给得出进度、也允许取消；
// 取消只在 rename 之前生效，源文件一定保持原样（后端语义见 services/save_job.go）。
// ---------------------------------------------------------------------------
const saveProgress = ref<{ phase: string; done: number; total: number } | null>(null);
const saveCancelRequested = ref(false);

const SAVE_PHASE_TEXT: Record<string, string> = {
  prepare: "准备写入",
  backup: "备份源文件",
  rebuild: "重建归档数据",
  write: "写入磁盘",
  sync: "刷盘",
  rename: "替换源文件",
};

function savePhaseText(phase: string): string {
  return SAVE_PHASE_TEXT[phase] ?? phase;
}

function savePercent(): number {
  const current = saveProgress.value;
  if (!current || current.total <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round((current.done / current.total) * 100)));
}

function eventData(event: any): any {
  return event?.data ?? event;
}

Events.On("save:progress", (event: any) => {
  const data = eventData(event);
  const phase = String(data?.phase ?? "");
  if (!phase || phase === "done") {
    saveProgress.value = null;
    return;
  }
  saveProgress.value = {
    phase,
    done: Number(data?.done ?? 0),
    total: Number(data?.total ?? 0),
  };
});

/** 请求取消封包：只发一个开关，真正的中止点由后端在下个安全点执行。 */
async function cancelSave(): Promise<void> {
  if (saveCancelRequested.value) return;
  saveCancelRequested.value = true;
  try {
    await CancelSave();
  } catch {
    // 后端可能刚好结束（没有在跑的任务），忽略即可。
  }
}

const canSave = computed(() => archive.open && !editor.saving);
const canSaveToSource = computed(() => archive.open && !!archive.info?.path && !editor.saving);
/** 关闭 PVF：与关闭窗口共用同一份「未保存」判定。 */
const unsaved = useUnsavedChanges();
const closingArchive = ref(false);
const canCloseArchive = computed(() => archive.open && !closingArchive.value);
const versionChangeCount = computed(() => {
  if (!archive.open) return 0;
  if (version.enabled) {
    return version.status.changedFiles;
  }
  return archive.modifiedCount;
});
const versionTooltip = computed(() => {
  if (!archive.open) return "管理工作区版本、提交和历史";
  if (version.enabled) {
    if (version.status.changedFiles > 0) {
      return `版本控制 (${version.status.branch})：${version.status.changedFiles} 个变更待提交`;
    }
    return `版本控制 (${version.status.branch})：工作区无变更`;
  }
  if (archive.modifiedCount > 0) {
    return `版本控制未启用（当前有 ${archive.modifiedCount} 个未保存修改）`;
  }
  return "管理工作区版本、提交和历史";
});

/** 书签总数（所有书签簿 + 全部分组内的条目），用于工具栏「书签」角标。 */
const bookmarkCount = computed(() => {
  let total = 0;
  const walkGroups = (groups: BookmarkGroup[]) => {
    for (const group of groups) {
      total += group.entries.length;
      walkGroups(group.groups);
    }
  };
  for (const book of bookmarks.books) {
    total += book.entries.length;
    walkGroups(book.groups);
  }
  return total;
});

/** 下拉菜单里的 emoji 图标（与预览图一致）。 */
function renderEmoji(emoji: string) {
  return () => h("span", { style: "font-size:14px;line-height:1" }, emoji);
}

/** 下拉菜单里的矢量图标（轮廓比 emoji 更清晰，用于「关闭 PVF」等条目）。 */
function renderIcon(icon: Component) {
  return () => h(NIcon, { size: 14 }, { default: () => h(icon) });
}

/** 「打开」下拉：打开文件 / 保存 / 另存为。 */
const openMenuOptions = computed<DropdownOption[]>(() => [
  {
    label: "打开文件…",
    key: "open",
    icon: renderEmoji("📂"),
    disabled: archive.loading,
  },
  { type: "divider", key: "open-divider" },
  {
    label: "保存",
    key: "save",
    icon: renderEmoji("💾"),
    disabled: !canSaveToSource.value,
  },
  {
    label: "另存为…",
    key: "save-as",
    icon: renderEmoji("📤"),
    disabled: !canSave.value,
  },
  {
    label: "导入密钥",
    key: "import-key",
    icon: renderEmoji("🔑"),
  },
  { type: "divider", key: "close-divider" },
  {
    label: "关闭 PVF",
    key: "close",
    icon: renderIcon(Dismiss16Regular),
    disabled: !canCloseArchive.value,
  },
]);

/**
 * 「可视化编辑区」下拉：把文件内容做成可视化界面的编辑器入口都收在这里。
 * 新增可视化编辑器时**往下面排**即可（一项 = 一个编辑区）。
 */
const visualMenuOptions = computed<DropdownOption[]>(() => [
  {
    label: "独立掉落编辑",
    key: "independent-drop",
    icon: renderEmoji("🎯"),
    disabled: !archive.open,
  },
  {
    // B1 第 1 批（2026-10-06）：新增文件族的可视化编辑入口。
    // 「装备升级系统编辑」已按用户 2026-10-06 要求撤下（没用的模块；规则数据暂留 formats.json，不影响任何界面）。
    label: "商店物品编辑",
    key: "itemshop",
    icon: renderEmoji("🏪"),
    disabled: !archive.open,
  },
]);

/** 「更多UI」下拉：收纳次级入口（原「脚本工作区」按钮挪到了这里）。 */
const moreMenuOptions = computed<DropdownOption[]>(() => {
  const options: DropdownOption[] = [
    {
      label: archive.unpacking ? "取消解包" : "解包归档…",
      key: archive.unpacking ? "cancel-unpack" : "unpack",
      icon: renderEmoji(archive.unpacking ? "⏹️" : "📦"),
      disabled: !archive.open,
    },
    {
      label: "对象视图",
      key: "objectview",
      icon: renderEmoji("🧱"),
      disabled: !archive.open,
    },
    {
      label: script.workspaceDetached ? "脚本工作区（已在独立窗口）" : "脚本工作区",
      key: "script-workspace",
      icon: renderEmoji("⌨️"),
      disabled: !archive.open,
    },
  ];
  if (archive.info?.paged110) {
    options.push({
      label: "注册 indexhash",
      key: "register-hash",
      icon: renderEmoji("🔑"),
      disabled: !archive.open,
    });
  }
  options.push({
    label: "装备属性模板",
    key: "equip-template",
    icon: renderEmoji("⚔️"),
  });
  options.push({ type: "divider", key: "more-divider" });
  return options;
});

watch(
  () => archive.unpackMessage,
  (msg) => {
    if (msg) message.info(msg);
  }
);

async function onOpen() {
  try {
    await archive.openDialog();
    if (archive.open) message.success(`已打开 ${archive.info?.fileCount.toLocaleString()} 个文件`);
  } catch (e: any) {
    // 打开失败的提示统一由顶层 ArchiveErrorDialog 负责（含「选择 sk.dat 文件…」按钮），
    // 这里不再另发 message，避免「弹窗 + 右下角提示」同时出现。
    if (isCancel(e)) return;
  }
}

function onImport(): void {
  importer.open("");
}

/**
 * 封包成功提示：弹窗 + 2 秒倒计时自动关闭，也可点「关闭」立刻关。
 * 2026-09-29 用户要求——原来只发一条轻提示（message），不够醒目。
 */
function showSaveSuccessDialog(): void {
  const remain = ref(2);
  let timer: ReturnType<typeof setInterval> | undefined;
  const stop = () => {
    if (timer !== undefined) {
      clearInterval(timer);
      timer = undefined;
    }
  };
  const dlg = dialog.success({
    title: "封包成功",
    content: () =>
      h("div", { style: "line-height:1.8" }, [
        h("div", "已保存到源文件。"),
        h(
          "div",
          { style: "margin-top:6px; color:#9aa4b2; font-size:12px" },
          `${remain.value} 秒后自动关闭（也可点「关闭」立即关闭）`
        ),
      ]),
    positiveText: "关闭",
    closable: true,
    maskClosable: true,
    onAfterLeave: stop,
  });
  timer = setInterval(() => {
    remain.value -= 1;
    if (remain.value <= 0) {
      stop();
      dlg.destroy();
    }
  }, 1000);
}

/**
 * 封包失败提示：弹窗（「确认」+「关闭」两个按钮，**必须用户手动关闭**，不自动消失）。
 * 2026-09-29 用户要求——保存被拒（例如页密钥不足）这类错误此前只落在日志里，不够醒目。
 */
function showSaveFailedDialog(err: unknown): void {
  const text = (err as { message?: string } | null)?.message ?? String(err);
  dialog.error({
    title: "封包失败 · 源文件未被修改",
    content: () =>
      h("div", { style: "line-height:1.8" }, [
        h("div", { style: "white-space:pre-wrap; word-break:break-all" }, text),
        h(
          "div",
          { style: "margin-top:8px; color:#9aa4b2; font-size:12px" },
          "源文件保持原样，没有任何改动被写入；请按提示处理后重新封包。"
        ),
      ]),
    positiveText: "确认",
    negativeText: "关闭",
    closable: false,
    maskClosable: false,
    closeOnEsc: false,
  });
}

async function saveToSource() {
  saveCancelRequested.value = false;
  try {
    await editor.save();
    showSaveSuccessDialog();
  } catch (e: any) {
    if (!isCancel(e)) showSaveFailedDialog(e);
  } finally {
    saveProgress.value = null;
  }
}

function onSave() {
  if (!archive.info?.path) {
    void onSaveAs();
    return;
  }
  const backupHint = settings.backupSourceOnSave
    ? "保存前会将当前源文件备份为同目录下的 .bak 文件。"
    : "当前未启用源文件备份。";
  const dialogRef = dialog.warning({
    title: "确认保存到源文件",
    // 写成渲染函数（而不是字符串）：封包期间直接把进度条与「取消封包」画在同一个弹窗里。
    content: () =>
      h("div", { style: "line-height:1.8" }, [
        h("div", `保存会覆盖源文件中的当前内容。${backupHint}`),
        h("div", { style: "margin-top:4px" }, "确定继续吗？"),
        saveProgress.value
          ? h("div", { style: "margin-top:12px" }, [
              h(
                "div",
                { style: "font-size:12px; color:#9aa4b2" },
                saveCancelRequested.value
                  ? "正在取消…（等当前步骤走到安全点即中止，源文件不会被改动）"
                  : `${savePhaseText(saveProgress.value.phase)}…`
              ),
              h("div", { style: "margin-top:6px" }, [
                h(NProgress, {
                  percentage: savePercent(),
                  processing: !saveCancelRequested.value,
                  status: saveCancelRequested.value ? "warning" : "default",
                  height: 8,
                  showIndicator: false,
                }),
              ]),
              h(
                "div",
                { style: "margin-top:4px; font-size:12px; color:#9aa4b2" },
                `${savePercent()}%`
              ),
              h("div", { style: "margin-top:6px" }, [
                h(
                  NButton,
                  {
                    size: "tiny",
                    quaternary: true,
                    disabled: saveCancelRequested.value,
                    onClick: () => void cancelSave(),
                  },
                  { default: () => "取消封包" }
                ),
              ]),
            ])
          : null,
      ]),
    positiveText: "确认保存",
    negativeText: "取消",
    // naive-ui 会等待 onPositiveClick 返回的 Promise 结束再关闭弹窗,
    // 期间通过 dialogRef 回写加载态,否则保存过程没有任何反馈。
    onPositiveClick: async () => {
      dialogRef.loading = true;
      dialogRef.negativeButtonProps = { disabled: true };
      try {
        await saveToSource();
      } finally {
        dialogRef.loading = false;
        dialogRef.negativeButtonProps = { disabled: false };
      }
    },
  });
}

async function onSaveAs() {
  try {
    const path = await editor.saveAs();
    if (path) message.success(`已另存为 ${path}`);
  } catch (e: any) {
    if (!isCancel(e)) message.error(`另存为失败: ${e?.message ?? e}`);
  }
}

/**
 * 「导入密钥」：选一个 sk.dat 复制进密钥库（选一次即永久生效，以后打开该 PVF 无需再放密钥）。
 * 与「打开失败」弹窗里的按钮走同一套逻辑，区别只是入口在「打开」下拉菜单里。
 */
async function onImportKey(): Promise<void> {
  try {
    const tip = await PickKeyFileDialog();
    if (tip) message.success(tip);
  } catch (e: any) {
    if (!isCancel(e)) message.error(`导入密钥失败: ${e?.message ?? e}`);
  }
}

function onOpenMenuSelect(key: string | number): void {
  if (key === "open") void onOpen();
  else if (key === "save") onSave();
  else if (key === "save-as") void onSaveAs();
  else if (key === "import-key") void onImportKey();
  else if (key === "close") onCloseArchive();
}

function onMoreMenuSelect(key: string | number): void {
  switch (key) {
    case "unpack":
      onUnpack();
      break;
    case "cancel-unpack":
      onCancelUnpack();
      break;
    case "objectview":
      sidebar.show("objectview");
      break;
    case "script-workspace":
      // 原工具条上的「脚本工作区」分段按钮：改到「更多UI」下面（用户 2026-10-03 要求）。
      script.showWorkspace();
      break;
    case "register-hash":
      hashRegistrationVisible.value = true;
      break;
    case "equip-template":
      equipTemplate.open();
      break;
  }
}

/** 「可视化编辑区」下拉的选择处理。 */
function onVisualMenuSelect(key: string | number): void {
  if (key === "independent-drop") void formView.openInWindow("independent_drop");
  // B1 第 1 批（2026-10-06）：文件族入口（「装备升级系统」已按用户要求撤下）。
  if (key === "itemshop") void formView.openInWindow("itemshop");
}

function onUnpack() {
  dialog.warning({
    title: "解包归档",
    content: `将 ${archive.info?.fileCount.toLocaleString()} 个文件解包到所选目录(约 ${(archive.info! ? (archive.info!.bodySize * 10.79) / 1e6 : 0).toFixed(0)}MB)。继续?`,
    positiveText: "选择目录…",
    negativeText: "取消",
    onPositiveClick: async () => {
      try {
        const started = await archive.unpackDialog();
        if (!started) return;
        message.info("解包已开始");
      } catch (e: any) {
        if (!isCancel(e)) message.error(`解包失败: ${e?.message ?? e}`);
      }
    },
  });
}

function onCancelUnpack() {
  archive.cancelUnpack();
}

function toggleBookmarkPopup(): void {
  bookmarkPopupVisible.value = !bookmarkPopupVisible.value;
}

/**
 * 导出归档中所有「已修改未封包」的条目（文件树里显示为黄色的条目）。
 * 复用资源管理器「导出文件」的同一套流程：整包扫描取路径 → 选择目标目录 → 按相对路径落盘。
 */
async function onExportModified(): Promise<void> {
  if (exportingModified.value || !archive.open) return;
  const archivePath = archive.info?.path ?? "";
  message.info("正在收集已修改的条目…");
  try {
    const paths = (await ArchiveService.ListModifiedPaths()) ?? [];
    if (archive.info?.path !== archivePath) return;
    if (paths.length === 0) {
      message.info("当前没有已修改未封包的条目");
      return;
    }
    exportPickPaths.value = paths;
    exportPickVisible.value = true;
  } catch (e: any) {
    if (!isCancel(e)) message.error(`收集已修改条目失败: ${e?.message ?? e}`);
  }
}

/** 目录选定后导出：落在 <所选目录>\<时间戳>改动文件导出\ 内。 */
async function onExportModifiedPicked(payload: { dir: string; paths: string[] }): Promise<void> {
  const { dir, paths } = payload;
  if (!dir || paths.length === 0) return;
  exportingModified.value = true;
  try {
    const out = await ExportFilesTo(dir, paths, "改动文件导出");
    if (out) message.success(`已导出 ${paths.length} 个已修改条目到 ${out}`);
  } catch (e: any) {
    if (!isCancel(e)) message.error(`导出已修改条目失败: ${e?.message ?? e}`);
  } finally {
    exportingModified.value = false;
    exportPickPaths.value = [];
  }
}

function isCancel(e: any): boolean {
  const text = String(e?.message ?? e).toLowerCase();
  return text.includes("cancel") || text.includes("已取消");
}

/**
 * 关闭当前 PVF：有未保存改动先确认。
 * 关闭动作本身带兜底：先清空界面（编辑器标签），再请求后端关闭；
 * 后端即使卡住/报错也只会提示，界面一定回到「未打开」状态，不会出现关不掉。
 */
function onCloseArchive(): void {
  if (!canCloseArchive.value) return;
  // 没有未保存改动也确认一次（用户 2026-09-28 要求）：是 = 关闭，否 = 取消。
  if (!unsaved.value) {
    dialog.warning({
      title: "关闭 PVF",
      content: "是否关闭 PVF？",
      positiveText: "是",
      negativeText: "否",
      onPositiveClick: () => void doCloseArchive(),
    });
    return;
  }
  dialog.warning({
    title: "PVF 未保存",
    content:
      "当前 PVF 还有未保存的改动（未封包 / 未提交的修改），关闭后这些改动将丢失且无法恢复。确定要关闭吗？",
    positiveText: "仍要关闭",
    negativeText: "取消",
    onPositiveClick: () => void doCloseArchive(),
  });
}

async function doCloseArchive(): Promise<void> {
  if (closingArchive.value) return;
  closingArchive.value = true;
  const closedPath = archive.info?.path ?? "";
  try {
    // 先清本地界面状态：保证「关不掉」不会发生（后端异常也不影响这里）。
    editor.closeAllTabs();
  } catch (e: any) {
    console.error("清空编辑器标签失败", e);
  }
  try {
    await archive.close();
    message.success(closedPath ? `已关闭 PVF：${closedPath}` : "已关闭当前 PVF");
  } catch (e: any) {
    message.error(`关闭 PVF 失败: ${e?.message ?? e}`);
  } finally {
    closingArchive.value = false;
  }
}
</script>

<template>
  <div class="toolbar" role="toolbar" aria-label="主工具栏">
    <!-- ① 文件：打开（含下拉）+ 封包 -->
    <div class="tb-group" role="group" aria-label="文件">
      <NDropdown
        trigger="click"
        placement="bottom-start"
        :options="openMenuOptions"
        @select="onOpenMenuSelect"
      >
        <div class="open-split">
          <NTooltip trigger="hover">
            <template #trigger>
              <button
                type="button"
                class="tb-btn tb-btn-primary open-main"
                :disabled="archive.loading"
                @click.stop="onOpen"
              >
                <span class="ic">📂</span>
                <span>打开</span>
              </button>
            </template>
            打开 PVF 归档 (Cmd+O)
          </NTooltip>
          <button type="button" class="open-caret" aria-label="打开菜单">
            <span class="caret">▾</span>
          </button>
        </div>
      </NDropdown>

      <NTooltip trigger="hover">
        <template #trigger>
          <button
            type="button"
            class="tb-btn tb-btn-primary"
            :disabled="!canSaveToSource"
            @click="onSave"
          >
            <span class="ic">📦</span>
            <span>封包</span>
          </button>
        </template>
        封包 = 保存到源文件（写回 PVF，需确认）
      </NTooltip>

      <NTooltip trigger="hover">
        <template #trigger>
          <button
            type="button"
            class="tb-btn tb-btn-close"
            :disabled="!canCloseArchive"
            @click="onCloseArchive"
          >
            <span class="ic-x">
              <NIcon :size="12"><Dismiss16Regular /></NIcon>
            </span>
            <span>{{ closingArchive ? "关闭中…" : "关闭" }}</span>
          </button>
        </template>
        关闭当前 PVF 文件（有未保存改动时会先确认）
      </NTooltip>
    </div>

    <div class="tb-sep" />

    <!-- ② 归档视图：书签 → 导入 → 工作区切换 → 高级搜索 -->
    <div class="tb-group" role="group" aria-label="归档视图">
      <NTooltip trigger="hover">
        <template #trigger>
          <button
            ref="bookmarkBtnRef"
            type="button"
            class="tb-btn tb-btn-book"
            :class="{ 'tb-btn-active': bookmarkPopupVisible }"
            @click="toggleBookmarkPopup"
          >
            <span class="ic">🔖</span>
            <span>书签</span>
            <span v-if="bookmarkCount > 0" class="tb-cnt">{{ bookmarkCount }}</span>
          </button>
        </template>
        打开 / 关闭书签面板
      </NTooltip>

      <NTooltip trigger="hover">
        <template #trigger>
          <button
            type="button"
            class="tb-btn tb-btn-plain"
            :disabled="!archive.open || importer.running"
            @click="onImport"
          >
            <span class="ic">📥</span>
            <span>导入</span>
          </button>
        </template>
        批量导入文件到归档根目录
      </NTooltip>

      <div class="seg" role="tablist" aria-label="工作区模式">
        <button
          type="button"
          role="tab"
          :aria-selected="!script.workspaceVisible"
          class="seg-i"
          :class="{ on: !script.workspaceVisible }"
          title="切回归档编辑"
          @click="script.hideWorkspace"
        >
          <span class="ic">📄</span>
          <span>归档编辑</span>
        </button>
        <NDropdown
          trigger="click"
          placement="bottom-start"
          :options="visualMenuOptions"
          @select="onVisualMenuSelect"
        >
          <button
            type="button"
            role="tab"
            class="seg-i"
            :class="{ on: formView.windowOpen }"
            :title="
              formView.windowOpen
                ? '可视化编辑区（已有独立窗口在打开）'
                : '可视化编辑区：把文件内容做成可视化界面来编辑'
            "
          >
            <span class="ic">🧩</span>
            <span>可视化编辑区</span>
            <span class="caret">▾</span>
          </button>
        </NDropdown>
      </div>

      <NTooltip trigger="hover">
        <template #trigger>
          <button
            type="button"
            class="tb-btn tb-btn-plain"
            :disabled="!archive.open"
            @click="advancedSearch.open"
          >
            <span class="ic">🔍</span>
            <span>高级搜索</span>
          </button>
        </template>
        在归档中搜索路径、名称或 ID（双击命中直接打开）
      </NTooltip>

      <NTooltip trigger="hover">
        <template #trigger>
          <button
            type="button"
            class="tb-btn tb-btn-plain"
            :disabled="!archive.open || exportingModified"
            @click="onExportModified"
          >
            <span class="ic">📤</span>
            <span>导出改动</span>
            <span v-if="archive.modifiedCount > 0" class="tb-cnt">{{ archive.modifiedCount }}</span>
          </button>
        </template>
        导出所有已修改未封包的条目（文件树中显示为黄色的条目）
      </NTooltip>
    </div>

    <div class="tb-spacer" />

    <div v-if="archive.unpacking" class="unpack-progress">
      <NText depth="3">
        解包中 {{ archive.unpackProgress.done.toLocaleString() }} /
        {{ archive.unpackProgress.total.toLocaleString() }}
      </NText>
      <NProgress
        type="line"
        :show-indicator="false"
        :percentage="
          archive.unpackProgress.total
            ? Math.round((archive.unpackProgress.done / archive.unpackProgress.total) * 100)
            : 0
        "
        style="width: 140px"
      />
    </div>

    <!-- ③ 快捷入口：设置 / 版本管理 / AI 助手 / 更多 -->
    <div class="tb-group" role="group" aria-label="快捷入口">
      <NTooltip trigger="hover">
        <template #trigger>
          <button type="button" class="tb-btn tb-btn-plain" @click="settings.open()">
            <span class="ic">⚙️</span>
            <span>设置</span>
          </button>
        </template>
        打开设置
      </NTooltip>

      <NTooltip trigger="hover">
        <template #trigger>
          <button
            type="button"
            class="tb-btn tb-btn-plain"
            :disabled="!archive.open"
            @click="version.open"
          >
            <span class="ic">🗂</span>
            <span>版本管理</span>
            <span v-if="versionChangeCount > 0" class="tb-cnt-w">{{ versionChangeCount }}</span>
          </button>
        </template>
        {{ versionTooltip }}
      </NTooltip>

      <NTooltip trigger="hover">
        <template #trigger>
          <button type="button" class="tb-btn tb-btn-ai" @click="sidebar.show('ai')">
            <span class="ic">✨</span>
            <span>AI 助手</span>
          </button>
        </template>
        在右侧栏打开 AI 助手面板
      </NTooltip>

      <!-- 开发者面板入口：只在「开发人员专用」构建里出现 -->
      <NTooltip v-if="DEV_TOOLS" trigger="hover">
        <template #trigger>
          <button
            type="button"
            class="tb-btn tb-btn-dev"
            :class="{ 'tb-btn-dev--on': dev.visible }"
            @click="dev.toggle()"
          >
            <span class="ic">🛠</span>
            <span>开发者</span>
          </button>
        </template>
        开发者面板（Ctrl+Shift+D）：运行环境、打开耗时、索引、日志、体检与内部开关
      </NTooltip>

      <NDropdown
        trigger="click"
        placement="bottom-end"
        :options="moreMenuOptions"
        @select="onMoreMenuSelect"
      >
        <button type="button" class="tb-btn tb-btn-more">
          <span class="ic">⋯</span>
          <span>更多</span>
          <span class="caret">▾</span>
        </button>
      </NDropdown>
    </div>

    <IndexHashRegistrationModal
      :show="hashRegistrationVisible"
      @update:show="hashRegistrationVisible = $event"
    />
    <BookmarkPopup v-model:show="bookmarkPopupVisible" :anchor-el="bookmarkBtnRef" />
    <ExportDialog
      v-model:show="exportPickVisible"
      title="导出改动"
      :paths="exportPickPaths"
      @confirm="onExportModifiedPicked"
    />
  </div>

  <DevPanel v-if="DEV_TOOLS" />
</template>

<style scoped>
/* 工具栏：深色卡片（与设计稿一致） */
.toolbar {
  height: 50px;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 14px;
  margin: 8px 10px;
  background: var(--pvf-surface-card);
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 9px;
  flex-shrink: 0;
}
.tb-group {
  display: flex;
  align-items: center;
  gap: 8px;
}
.tb-sep {
  width: 1px;
  height: 24px;
  background: var(--pvf-border-strong);
  flex: none;
}
.tb-spacer {
  flex: 1;
}

/* 按钮（与设计稿 .btn 系列逐项一致） */
.tb-btn {
  height: 34px;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 0 15px;
  border-radius: 8px;
  font-family: inherit;
  font-size: 13.5px;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid transparent;
  white-space: nowrap;
  transition: filter 120ms ease, background 120ms ease, color 120ms ease;
}
.tb-btn:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}
.tb-btn .ic {
  font-size: 15px;
  line-height: 1;
}
.tb-btn-primary {
  background: var(--pvf-primary);
  color: #fff;
  box-shadow: 0 2px 12px rgba(79, 140, 255, 0.4);
}
.tb-btn-primary:hover:not(:disabled) {
  filter: brightness(1.06);
}
.tb-btn-plain {
  background: transparent;
  color: var(--pvf-text-secondary);
  font-weight: 500;
  padding: 0 11px;
}
.tb-btn-plain:hover:not(:disabled) {
  background: rgba(79, 140, 255, 0.09);
  color: var(--pvf-text-primary);
}
/* 关闭 PVF：沿用 tb-btn 的圆角药丸形状，用淡红圆盘图标表达「关闭」语义 */
.tb-btn-close {
  background: rgba(128, 128, 128, 0.05);
  color: var(--pvf-text-secondary);
  border-color: var(--pvf-border-subtle);
  font-weight: 500;
  padding: 0 12px;
}
.tb-btn-close:hover:not(:disabled) {
  background: var(--pvf-error-surface);
  border-color: var(--pvf-error);
  color: var(--pvf-error-hover);
}
.tb-btn-close .ic-x {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: var(--pvf-error-surface);
  color: var(--pvf-error);
  flex: none;
  transition: background 120ms ease, color 120ms ease;
}
.tb-btn-close:hover:not(:disabled) .ic-x {
  background: var(--pvf-error);
  color: #fff;
}
.tb-btn-book {
  background: var(--pvf-primary-soft);
  color: #bcd4ff;
  border-color: rgba(79, 140, 255, 0.3);
}
.tb-btn-book:hover:not(:disabled) {
  filter: brightness(1.08);
}
.tb-btn-ai {
  background: linear-gradient(135deg, #4f8cff, #7d5cff);
  color: #fff;
  box-shadow: 0 2px 12px rgba(110, 110, 255, 0.42);
}
.tb-btn-ai:hover {
  filter: brightness(1.08);
}
.tb-btn-more {
  background: transparent;
  color: var(--pvf-text-secondary);
  border-color: var(--pvf-border-subtle);
  padding: 0 12px;
  font-size: 13px;
  font-weight: 500;
}
.tb-btn-more:hover {
  background: rgba(79, 140, 255, 0.09);
}
/* 开发者面板入口：橙金色，和普通功能按钮区分开 */
.tb-btn-dev {
  background: rgba(240, 160, 32, 0.16);
  color: #f2c97d;
  border-color: rgba(240, 160, 32, 0.4);
}
.tb-btn-dev:hover {
  filter: brightness(1.12);
}
.tb-btn-dev--on {
  background: rgba(240, 160, 32, 0.3);
  color: #ffd89b;
}
.caret {
  font-size: 10px;
  opacity: 0.85;
  margin-left: 1px;
}

/* 角标（内联胶囊，与设计稿 .cnt / .cnt-w 一致） */
.tb-cnt {
  font-size: 10.5px;
  font-weight: 700;
  padding: 1px 6px;
  border-radius: 999px;
  background: rgba(79, 140, 255, 0.22);
  color: #cfe0ff;
}
.tb-cnt-w {
  font-size: 10px;
  font-weight: 800;
  padding: 1px 5px;
  border-radius: 999px;
  background: rgba(242, 201, 125, 0.22);
  color: var(--pvf-warning);
}

/* 「打开」+ 下拉箭头：视觉连体 */
.open-split {
  display: inline-flex;
  align-items: center;
  gap: 0;
}
.open-main {
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
}
.open-caret {
  height: 34px;
  width: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: none;
  cursor: pointer;
  color: #fff;
  background: var(--pvf-primary);
  border-left: 1px solid rgba(255, 255, 255, 0.3);
  border-top-right-radius: 8px;
  border-bottom-right-radius: 8px;
  box-shadow: 0 2px 12px rgba(79, 140, 255, 0.4);
}
.open-caret:hover {
  filter: brightness(1.08);
}

/* 分段控件（与设计稿 .seg / .seg-i 一致） */
.seg {
  display: inline-flex;
  align-items: center;
  border: 1px solid var(--pvf-border-strong);
  border-radius: 9px;
  background: rgba(128, 128, 128, 0.07);
  padding: 2px;
  gap: 2px;
  user-select: none;
}
.seg-i {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 29px;
  padding: 0 12px;
  border-radius: 7px;
  font-family: inherit;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--pvf-text-secondary);
  background: transparent;
  border: 1px solid transparent;
  cursor: pointer;
  white-space: nowrap;
  transition: all 120ms ease;
  line-height: 1;
}
.seg-i .ic {
  font-size: 13px;
  line-height: 1;
  opacity: 0.95;
}
.seg-i.on {
  background: rgba(79, 140, 255, 0.15);
  color: #8fb8ff;
  border-color: rgba(79, 140, 255, 0.6);
  box-shadow: 0 0 0 1px rgba(79, 140, 255, 0.18);
}
.seg-i:hover:not(.on):not(:disabled) {
  color: var(--pvf-text-primary);
  background: rgba(79, 140, 255, 0.06);
}
.seg-i:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}
.seg-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}
.seg-dot--running {
  background: var(--pvf-primary);
  animation: pulse-badge 1.2s infinite ease-in-out;
}
.seg-dot--success {
  background: var(--pvf-success, #18a058);
}
.seg-dot--warning {
  background: var(--pvf-warning, #f0a020);
}
@keyframes pulse-badge {
  0%, 100% {
    transform: scale(0.9);
    opacity: 0.6;
  }
  50% {
    transform: scale(1.3);
    opacity: 1;
  }
}

.unpack-progress {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-right: 10px;
}
</style>
