<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";
import { NButton, NInputNumber, NTag, useMessage } from "naive-ui";
import { GetFilePage, SetFilePage, type LargeTextPage } from "../services/largeTextApi";
import { useArchiveStore } from "../stores/archive";
import { useExternalEditStore } from "../stores/externalEdit";

/**
 * 大文件的「内置 TXT」（页式）：
 *
 * - 文本**留在后端**，窗口每次只取一页（Go 侧 GetFilePage，默认每页 2000 行 ≈ 130KB）
 *   —— 几十兆文本永不进窗口，所以打开与切换都是毫秒级；
 * - 编辑就在当前页的 textarea 里做（原生控件，记事本手感）；
 * - 「保存本页」由后端把该页拼回全文写进归档内存（与编辑器保存同一条路径），
 *   顶部「未保存修改」会 +1，之后照常点「保存 PVF」落盘；
 * - 切页/关标签前会自动提交本页，避免丢改动。
 *
 * 这一档文件不再挂 CodeMirror、不再挂任何注解/着色/跳转关联。
 */
const props = defineProps<{
  index: number;
  path: string;
  size: number;
}>();

const message = useMessage();
const archive = useArchiveStore();
const externalEdit = useExternalEditStore();

const info = ref<LargeTextPage | null>(null);
const text = ref("");
const loading = ref(false);
const saving = ref(false);
/** 当前页有本地未提交改动。 */
const dirty = ref(false);

async function load(page: number): Promise<void> {
  loading.value = true;
  try {
    const pageInfo = await GetFilePage(props.index, page);
    if (!pageInfo) return;
    info.value = pageInfo;
    text.value = pageInfo.text;
    dirty.value = false;
  } catch (error: any) {
    message.error(`读取第 ${page + 1} 页失败：${error?.message ?? error}`);
  } finally {
    loading.value = false;
  }
}

async function savePage(): Promise<boolean> {
  const pageInfo = info.value;
  if (!pageInfo || !dirty.value) return true;
  saving.value = true;
  try {
    const updated = await SetFilePage(props.index, pageInfo.page, text.value);
    if (!updated) return false;
    info.value = updated;
    text.value = updated.text;
    dirty.value = false;
    await archive.refreshInfo();
    return true;
  } catch (error: any) {
    message.error(`保存本页失败：${error?.message ?? error}`);
    return false;
  } finally {
    saving.value = false;
  }
}

async function goto(page: number): Promise<void> {
  if (!(await savePage())) return;
  await load(page);
}

async function onSaveClick(): Promise<void> {
  if (await savePage()) message.success("本页已写回归档内存，请点工具栏「保存 PVF」落盘");
}

function onInput(event: Event): void {
  text.value = (event.target as HTMLTextAreaElement).value;
  dirty.value = true;
}

async function onOpenExternal(): Promise<void> {
  try {
    if (!(await savePage())) return;
    const session = await externalEdit.start(props.path);
    message.success(
      session?.opened ? "已导出副本并用系统默认程序打开" : "已导出副本，请到工作目录里手动打开"
    );
  } catch (error: any) {
    message.error(`导出失败：${error?.message ?? error}`);
  }
}

onMounted(() => {
  void load(0);
});

onBeforeUnmount(() => {
  // 关标签/切标签时把本页交出去，避免丢改动。
  void savePage();
});
</script>

<template>
  <div class="large-text-view">
    <div class="ltv-bar">
      <NTag size="tiny" :bordered="false" type="warning">TXT 模式</NTag>
      <span class="ltv-hint">
        <template v-if="info">
          {{ info.lines.toLocaleString() }} 行 / 共 {{ info.pageCount.toLocaleString() }} 页
          · 本页 {{ info.pageLines.toLocaleString() }} 行（文本留在后端，只加载当前页）
        </template>
        <template v-else>加载中…</template>
      </span>
      <span class="ltv-spacer" />
      <NButton size="tiny" quaternary :disabled="!info || info.page <= 0" @click="goto(0)">
        首页
      </NButton>
      <NButton
        size="tiny"
        quaternary
        :disabled="!info || info.page <= 0"
        @click="goto((info?.page ?? 1) - 1)"
      >
        上一页
      </NButton>
      <NInputNumber
        class="ltv-page-input"
        size="tiny"
        :min="1"
        :max="info?.pageCount ?? 1"
        :value="(info?.page ?? 0) + 1"
        :show-button="false"
        @update:value="(value: number | null) => goto((value ?? 1) - 1)"
      />
      <NButton
        size="tiny"
        quaternary
        :disabled="!info || info.page >= info.pageCount - 1"
        @click="goto((info?.page ?? -1) + 1)"
      >
        下一页
      </NButton>
      <NButton size="tiny" quaternary :disabled="!info" @click="onOpenExternal">
        用外部编辑器打开
      </NButton>
      <NButton
        size="tiny"
        type="primary"
        :loading="saving"
        :disabled="!dirty"
        @click="onSaveClick"
      >
        保存本页{{ dirty ? " ●" : "" }}
      </NButton>
    </div>

    <textarea
      class="ltv-area"
      wrap="off"
      spellcheck="false"
      :readonly="loading"
      :value="text"
      @input="onInput"
    />
  </div>
</template>

<style scoped>
.large-text-view {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
}
.ltv-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  padding: 4px 8px;
  border-bottom: 1px solid var(--pvf-border-subtle, rgba(255, 255, 255, 0.08));
}
.ltv-hint {
  color: var(--pvf-text-secondary);
  font-size: 12px;
}
.ltv-spacer {
  flex: 1;
}
.ltv-page-input {
  width: 78px;
}
.ltv-area {
  flex: 1;
  min-height: 0;
  width: 100%;
  margin: 0;
  padding: 6px 10px;
  border: 0;
  outline: none;
  resize: none;
  overflow: auto;
  white-space: pre;
  tab-size: 4;
  color: var(--pvf-text-primary);
  background: transparent;
  font-family: "SF Mono", Menlo, Consolas, "Courier New", monospace;
  font-size: 13px;
  line-height: 1.55;
}
</style>
