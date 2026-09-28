<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  NAlert,
  NAutoComplete,
  NButton,
  NCheckbox,
  NIcon,
  NInput,
  NModal,
  NPopover,
  NProgress,
  NRadioButton,
  NRadioGroup,
  NTag,
  NText,
  NTree,
  type TreeOption,
  useMessage,
} from "naive-ui";
import { ArchiveService } from "../../bindings/pvfine/services";
import { Dismiss16Regular, FolderOpen20Regular } from "@vicons/fluent";
import { ImportFilesEx } from "../services/importApi";
import FolderPickerModal from "./FolderPickerModal.vue";
import { useArchiveStore } from "../stores/archive";
import { useEditorStore } from "../stores/editor";
import { useExplorerStore, type SearchItem, type TreeItem } from "../stores/explorer";
import { useImportStore } from "../stores/import";
import { useSearchWindowStore, resolvePathAnnotations } from "../stores/searchWindow";
import { useSidebarStore } from "../stores/sidebar";

const importer = useImportStore();
const archive = useArchiveStore();
const editor = useEditorStore();
const explorer = useExplorerStore();
const searchWindow = useSearchWindowStore();
const sidebar = useSidebarStore();
const message = useMessage();

const fileFilter = ref("");

/** 待导入列表（按名称过滤后的视图）。 */
const visiblePaths = computed(() => {
  const keyword = fileFilter.value.trim().toLowerCase();
  if (!keyword) return importer.sourcePaths;
  return importer.sourcePaths.filter((path) => path.toLowerCase().includes(keyword));
});

const canStart = computed(
  () => archive.open && !importer.running && importer.sourcePaths.length > 0
);

const readProgress = computed(() => {
  const current = importer.progress;
  if (!importer.running || current.total <= 0) return null;
  return Math.round((current.scanned / current.total) * 100);
});

// ---- 导入目录自动补全（来自归档目录树） ----
const targetOptions = ref<string[]>([]);
let targetQueryToken = 0;

watch(
  () => importer.visible,
  async (visible) => {
    if (visible) {
      importer.error = "";
      await refreshTargetOptions(importer.targetDir);
    }
  }
);

async function refreshTargetOptions(prefix: string): Promise<void> {
  const token = ++targetQueryToken;
  try {
    const suggestions = (await ArchiveService.SuggestDirectories(prefix, 50)) ?? [];
    if (token === targetQueryToken) targetOptions.value = suggestions;
  } catch {
    if (token === targetQueryToken) targetOptions.value = [];
  }
}

// 自绘本地「多选」选择器：文件与文件夹统一入口，勾选文件夹 = 导入其全部内容。
// （Windows 原生对话框在 wails beta.12 里文件夹模式与多选互斥，故自绘。）
const pickerShow = ref(false);

function openPicker(): void {
  if (importer.running || !archive.open) return;
  pickerShow.value = true;
}

function onPickerConfirm(paths: string[]): void {
  const added = importer.addFiles(paths);
  message.success(added > 0 ? `已添加 ${added} 项` : "所选内容已在列表中");
}

// ---- 从归档目录树选择导入目录 ----
const dirPickerShow = ref(false);
const dirPickerSelected = ref<string[]>([]);
/** TreeItem → TreeOption：children 为 null（未加载）时转 undefined，交给 on-load 懒加载。 */
const dirPickerTree = computed<TreeOption[]>(() => explorer.roots.map(toPickerOption));
function toPickerOption(item: TreeItem): TreeOption {
  return {
    key: item.key,
    label: item.label,
    isLeaf: !item.isDir,
    children: item.children ? item.children.map(toPickerOption) : undefined,
  };
}
const dirPickerExpanded = ref<Array<string | number>>([""]);

async function onDirPickerLoad(node: TreeOption): Promise<void> {
  const item = explorer.getItem(String(node.key ?? ""));
  if (item && item.isDir) await explorer.loadChildren(item);
}

function confirmDirPicker(): Promise<void> {
  dirPickerShow.value = false;
  const key = dirPickerSelected.value[0];
  if (!key) return Promise.resolve();
  const item = explorer.getItem(key);
  if (!item) return Promise.resolve();
  // 选中的是文件 → 导入到它所在的目录；目录 → 直接作为导入目录。
  if (item.isDir) {
    importer.targetDir = item.key;
  } else {
    const slash = item.key.lastIndexOf("/");
    importer.targetDir = slash > 0 ? item.key.slice(0, slash) : "";
  }
  message.success(`导入目录：${importer.targetDir || "归档根目录"}`);
  return Promise.resolve();
}

// ---- 导入 ----
/** 取消的判定：原生对话框返回 cancel，或后端 ErrImportCancelled。 */
function isCancelled(text: string): boolean {
  const lower = text.toLowerCase();
  return lower.includes("cancel") || text.includes("已取消");
}

/** 把本次导入的变更文件收进搜索视窗（可选功能）。 */
async function addImportedToSearchWindow(paths: string[]): Promise<void> {
  if (paths.length === 0) return;
  try {
    const nodes = (await ArchiveService.ResolveFiles(paths)) ?? [];
    const items: SearchItem[] = [];
    for (const node of nodes) {
      if (!node || node.isDir) continue;
      const label = node.path.slice(node.path.lastIndexOf("/") + 1) || node.path;
      items.push({
        key: node.path,
        label,
        path: node.path,
        id: "",
        name: "",
        category: "file",
        fileIndex: node.fileIndex,
        size: node.size ?? 0,
        dataType: node.dataType ?? 0,
        changeKind: "added",
        annotations: (node.annotations ?? []).filter(
          (annotation): annotation is NonNullable<typeof annotation> => !!annotation
        ),
        pathAnnotations: await resolvePathAnnotations(node.path),
        icon: node.icon ?? null,
        fieldImage: node.fieldImage ?? null,
      });
    }
    const result = searchWindow.addEntries(items);
    if (result.added > 0) sidebar.show("search");
  } catch {
    // 收进搜索视窗失败不影响导入结果本身。
  }
}

async function startImport(): Promise<void> {
  if (!canStart.value) return;
  importer.running = true;
  importer.error = "";
  importer.resetProgress();
  try {
    const result = await ImportFilesEx(
      importer.sourcePaths,
      importer.targetDir,
      importer.mode,
      importer.conflict
    );
    if (!result) throw new Error("后端未返回导入结果");

    const changedPaths = result.changedPaths ?? [];
    await editor.refreshAfterArchiveChange(changedPaths);
    await Promise.all([archive.refreshInfo(), explorer.reload()]);
    if (importer.addToSearchWindow) {
      await addImportedToSearchWindow(changedPaths);
    }

    const parts = [`导入 ${result.importedCount.toLocaleString()} 个新文件`];
    if (result.overwrittenCount > 0) parts.push(`覆盖 ${result.overwrittenCount.toLocaleString()} 个`);
    if (result.skippedCount > 0) parts.push(`跳过 ${result.skippedCount.toLocaleString()} 个`);
    // 文本导入里编码无法识别的二进制文件（.equ/.lst 等脚本）已自动按原始字节写入。
    const autoRaw = result.autoRawCount ?? 0;
    if (autoRaw > 0) {
      parts.push(`${autoRaw.toLocaleString()} 个自动按原始字节导入`);
    }
    importer.close();
    message.success(parts.join("，"));
  } catch (error: any) {
    const text = String(error?.message ?? error);
    if (isCancelled(text)) {
      importer.close();
    } else {
      importer.error = text;
      message.error(`导入失败: ${text}`);
    }
  } finally {
    importer.running = false;
  }
}

/**
 * 关闭弹窗。运行期间也允许关闭：先请求取消（后端在活动归档被改动之前才会响应），
 * 再收起界面 —— 这样"卡住"时用户永远有退路。
 */
async function close(): Promise<void> {
  if (importer.running) {
    await importer.requestCancel();
  }
  importer.close();
}

function pathLabel(path: string): string {
  return path.replaceAll("\\", "/").replace(/\/+$/, "");
}
</script>

<template>
  <NModal
    :show="importer.visible"
    preset="card"
    title="导入文件"
    :mask-closable="!importer.running"
    :close-on-esc="!importer.running"
    :style="{ width: 'min(1020px, calc(100vw - 40px))' }"
    @update:show="(show) => !show && close()"
  >
    <!-- data-file-drop-target 是 wails 文件拖放的必要标记：
         没有它，WebView2 的 drop 会被运行时直接忽略。 -->
    <div class="import-layout" data-file-drop-target="true">
      <!-- 左列：待导入的文件（支持拖拽） -->
      <div class="import-files">
        <div class="pane-header">
          <span class="pane-title">待导入的文件</span>
          <span class="pane-hint">支持拖拽文件 / 文件夹到此处</span>
        </div>
        <div class="files-toolbar">
          <NButton size="tiny" secondary :disabled="importer.running || !archive.open" @click="openPicker">
            <template #icon><NIcon :size="14"><FolderOpen20Regular /></NIcon></template>
            添加文件 / 文件夹…
          </NButton>
          <NButton
            size="tiny"
            quaternary
            :disabled="importer.running || importer.sourcePaths.length === 0"
            @click="importer.clearFiles()"
          >
            清空
          </NButton>
        </div>
        <div class="files-list">
          <div v-if="importer.sourcePaths.length === 0" class="files-empty">
            拖拽文件或文件夹到这里，<br />或点击「浏览添加…」
          </div>
          <div
            v-for="path in visiblePaths"
            :key="path"
            class="file-row"
            :title="path"
          >
            <span class="file-row-path">{{ pathLabel(path) }}</span>
            <button
              class="file-row-remove"
              :disabled="importer.running"
              aria-label="移除"
              @click="importer.removeFile(path)"
            >
              <NIcon :size="12"><Dismiss16Regular /></NIcon>
            </button>
          </div>
        </div>
        <div class="files-footer">
          <NInput
            v-model:value="fileFilter"
            size="tiny"
            placeholder="筛选待导入文件…"
            clearable
            :disabled="importer.running"
          />
          <NTag size="small" :bordered="false">
            文件数：{{ importer.sourcePaths.length.toLocaleString() }}
          </NTag>
        </div>
      </div>

      <!-- 右列：导入属性 -->
      <div class="import-props">
        <div class="pane-header">
          <span class="pane-title">导入属性</span>
        </div>

        <div class="prop-section">
          <div class="prop-label">导入到</div>
          <div class="target-row">
            <NAutoComplete
              v-model:value="importer.targetDir"
              size="small"
              :options="targetOptions"
              placeholder="请选择导入目录（留空 = 归档根目录）"
              :disabled="importer.running || !archive.open"
              clearable
              @update:value="refreshTargetOptions"
            />
            <NPopover
              v-model:show="dirPickerShow"
              trigger="click"
              placement="bottom-end"
              :width="340"
            >
              <template #trigger>
                <NButton
                  size="small"
                  secondary
                  :disabled="importer.running || !archive.open"
                  @click="dirPickerSelected = []"
                >
                  从目录树选择…
                </NButton>
              </template>
              <div class="dir-picker">
                <NTree
                  block-line
                  :data="dirPickerTree"
                  :expanded-keys="dirPickerExpanded"
                  :selected-keys="dirPickerSelected"
                  :on-load="onDirPickerLoad"
                  :on-update:expanded-keys="(keys: Array<string | number>) => (dirPickerExpanded = keys)"
                  :on-update:selected-keys="(keys: string[]) => (dirPickerSelected = keys)"
                  virtual-scroll
                  :animated="false"
                  class="dir-picker-tree"
                />
                <NButton size="small" type="primary" block :disabled="dirPickerSelected.length === 0" @click="confirmDirPicker">
                  设为导入目录
                </NButton>
                <div class="prop-hint">选目录 = 导入到该目录；选文件 = 导入到其所在目录。</div>
              </div>
            </NPopover>
          </div>
          <div class="prop-hint">可直接输入，或点「从目录树选择…」浏览已打开 PVF 的目录（留空 = 根目录）。</div>
        </div>

        <div class="prop-section">
          <div class="prop-label">读取方式</div>
          <NRadioGroup v-model:value="importer.mode" size="small" :disabled="importer.running">
            <NRadioButton value="text">文本导入（默认）</NRadioButton>
            <NRadioButton value="raw">原始字节</NRadioButton>
          </NRadioGroup>
        </div>

        <div class="prop-section">
          <div class="prop-label">文件冲突处理</div>
          <div class="conflict-options">
            <NCheckbox
              :checked="importer.conflict === 'overwrite'"
              :disabled="importer.running"
              @update:checked="importer.conflict = 'overwrite'"
            >
              <span class="conflict-name">覆盖</span>
              <span class="conflict-desc">同名文件直接替换归档内现有内容</span>
            </NCheckbox>
            <NCheckbox
              :checked="importer.conflict === 'rename'"
              :disabled="importer.running"
              @update:checked="importer.conflict = 'rename'"
            >
              <span class="conflict-name">重命名</span>
              <span class="conflict-desc">保留原文件，新文件另存为 name_1.ext</span>
            </NCheckbox>
            <NCheckbox
              :checked="importer.conflict === 'skip'"
              :disabled="importer.running"
              @update:checked="importer.conflict = 'skip'"
            >
              <span class="conflict-name">跳过</span>
              <span class="conflict-desc">保留原文件，跳过同名的新文件</span>
            </NCheckbox>
            <NCheckbox
              :checked="importer.conflict === 'abort'"
              :disabled="importer.running"
              @update:checked="importer.conflict = 'abort'"
            >
              <span class="conflict-name">终止</span>
              <span class="conflict-desc">发现任一同名冲突即整体取消导入</span>
            </NCheckbox>
          </div>
        </div>

        <div class="prop-section">
          <NCheckbox v-model:checked="importer.addToSearchWindow" size="small" :disabled="importer.running">
            导入后添加到搜索视窗
          </NCheckbox>
        </div>

        <div v-if="importer.running" class="prop-progress">
          <NText depth="3">{{ importer.progressText || "正在处理…" }}</NText>
          <NProgress
            v-if="readProgress !== null"
            type="line"
            :percentage="readProgress"
            :show-indicator="false"
          />
        </div>

        <NAlert v-if="importer.error" type="error" :show-icon="false">
          {{ importer.error }}
        </NAlert>

        <div class="prop-spacer" />
        <NButton
          type="primary"
          block
          :disabled="!canStart"
          :loading="importer.running"
          @click="startImport"
        >
          {{ importer.running ? "导入中…" : "开始导入" }}
        </NButton>
      </div>
    </div>
  </NModal>
  <FolderPickerModal
    v-model:show="pickerShow"
    @confirm="onPickerConfirm"
  />
</template>

<style scoped>
.import-layout {
  display: flex;
  gap: 14px;
  min-height: 480px;
  max-height: min(72vh, 640px);
}
.import-files,
.import-props {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
}
.import-files {
  flex: 1 1 46%;
}
.import-props {
  flex: 1 1 54%;
}
.pane-header {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.pane-title {
  font-weight: 600;
  font-size: 13px;
}
.pane-hint {
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 11px;
}
.files-toolbar {
  display: flex;
  align-items: center;
  gap: 6px;
}
.files-list {
  flex: 1;
  min-height: 0;
  overflow: auto;
  border: 1px dashed var(--pvf-border-normal);
  border-radius: 6px;
  padding: 4px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.files-empty {
  margin: auto;
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 12px;
  text-align: center;
  line-height: 1.8;
}
.file-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 6px;
  border-radius: 4px;
  min-height: 24px;
}
.file-row:hover {
  background: rgba(127, 127, 127, 0.14);
}
.file-row-path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
  direction: rtl; /* 长路径保留末段（文件名）可见 */
  text-align: left;
}
.file-row-remove {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--pvf-text-muted, #9aa4b2);
  cursor: pointer;
}
.file-row-remove:hover {
  color: #e88080;
  background: rgba(232, 128, 128, 0.14);
}
.files-footer {
  display: flex;
  align-items: center;
  gap: 8px;
}
.prop-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.target-row {
  display: flex;
  gap: 6px;
  align-items: center;
}
.target-row :deep(.n-auto-complete) {
  flex: 1;
}
.dir-picker {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.dir-picker-tree {
  height: 300px;
  overflow: auto;
}
.prop-label {
  font-size: 12px;
  font-weight: 600;
}
.prop-hint {
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 11px;
}
.conflict-options {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
/* NRadio 的 label 槽里放「名称 + 描述」：让单选圆点与文字在同一行对齐。 */
.conflict-option :deep(.n-radio__label) {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.conflict-name {
  font-size: 12px;
  font-weight: 500;
}
.conflict-desc {
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 11px;
}
.prop-progress {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
}
.prop-spacer {
  flex: 1;
}
</style>
