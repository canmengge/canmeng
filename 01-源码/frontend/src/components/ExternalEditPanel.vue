<script setup lang="ts">
/**
 * 「外部编辑」面板：把大文件（如 list/equipment.lst）导出到工作目录、用系统默认程序改，
 * 再一键回填归档。左侧资源管理器右键「用外部编辑器编辑」也会进这个面板。
 */
import { ref } from "vue";
import { NButton, NEmpty, NInput, NModal, NTag, NText, useMessage } from "naive-ui";
import { useExternalEditStore } from "../stores/externalEdit";
import type { ExternalEditSession } from "../services/externalEditApi";

defineProps<{ show: boolean }>();
const emit = defineEmits<{ (e: "update:show", value: boolean): void }>();

const externalEdit = useExternalEditStore();
const message = useMessage();
/** 正在处理的路径（按钮 loading）。 */
const acting = ref("");

function close(): void {
  emit("update:show", false);
}

function formatSize(bytes: number): string {
  if (!bytes) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`;
}

function normalizePath(raw: string): string {
  return raw.trim().replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
}

async function startPath(path: string): Promise<void> {
  acting.value = path;
  try {
    const session = await externalEdit.start(path);
    externalEdit.manualPath = "";
    message.success(
      session.opened
        ? `已导出并用系统默认程序打开：${session.localPath}`
        : `已导出到 ${session.localPath}（未能自动打开，请手动打开）`
    );
  } catch (error: any) {
    message.error(`外部编辑失败：${error?.message ?? error}`);
  } finally {
    acting.value = "";
  }
}

async function startManual(): Promise<void> {
  const path = normalizePath(externalEdit.manualPath);
  if (!path) {
    message.info("请输入归档内路径，例如 list/equipment.lst");
    return;
  }
  await startPath(path);
}

async function onCheck(session: ExternalEditSession): Promise<void> {
  acting.value = session.path;
  try {
    const updated = await externalEdit.check(session.path);
    if (updated?.changed) message.info(`「${session.path}」在外部已被修改，可以回填了`);
    else message.info(`「${session.path}」与导出时一致，暂无可回填内容`);
  } catch (error: any) {
    message.error(`检查失败：${error?.message ?? error}`);
  } finally {
    acting.value = "";
  }
}

async function onApply(session: ExternalEditSession): Promise<void> {
  acting.value = session.path;
  try {
    const result = await externalEdit.apply(session.path);
    if (result?.applied) {
      message.success(`已回填 ${result.bytes} 个字符；${result.savedHint ?? "请保存 PVF 才会落盘"}`);
    } else {
      message.info(result?.reason || "没有需要回填的内容");
    }
  } catch (error: any) {
    message.error(`回填失败：${error?.message ?? error}`);
  } finally {
    acting.value = "";
  }
}

async function onReveal(session: ExternalEditSession): Promise<void> {
  try {
    await externalEdit.reveal(session.path);
  } catch (error: any) {
    message.error(`打开文件夹失败：${error?.message ?? error}`);
  }
}

async function onFinish(session: ExternalEditSession, discardLocal: boolean): Promise<void> {
  acting.value = session.path;
  try {
    await externalEdit.finish(session.path, discardLocal);
    message.success(discardLocal ? "已结束会话并删除工作副本" : "已结束会话（工作副本保留）");
  } catch (error: any) {
    message.error(`结束会话失败：${error?.message ?? error}`);
  } finally {
    acting.value = "";
  }
}
</script>

<template>
  <NModal
    :show="show"
    preset="card"
    title="外部编辑（大文件导出 → 外部程序改 → 回填）"
    :style="{ width: 'min(880px, calc(100vw - 48px))' }"
    @update:show="(value: boolean) => emit('update:show', value)"
  >
    <div class="ee-body">
      <NText depth="3" class="ee-tip">
        适合 <b>list/equipment.lst</b> 这类几十万行的清单：导出成文本 → 用系统自带的文本编辑器改 →
        回到这里点「回填到归档」（回填只写进内存，仍需照常<b>保存 PVF</b> 才落盘）。
        支持的类型：.lst / .etc / .txt / .tbl / .co / .cos / .csv / .md / .json / .lua / .nut。
      </NText>

      <div class="ee-start">
        <NInput
          v-model:value="externalEdit.manualPath"
          placeholder="归档内路径，例如 list/equipment.lst"
          @keyup.enter="startManual"
        />
        <NButton
          type="primary"
          :loading="!!acting && acting === normalizePath(externalEdit.manualPath)"
          @click="startManual"
        >
          导出并外部编辑
        </NButton>
      </div>

      <div class="ee-head">
        <NText strong>外部编辑会话（{{ externalEdit.sessions.length }}）</NText>
        <NButton size="tiny" quaternary @click="externalEdit.refresh()">刷新</NButton>
      </div>

      <NEmpty v-if="externalEdit.sessions.length === 0" description="还没有外部编辑会话" />

      <div v-else class="ee-list">
        <div v-for="session in externalEdit.sessions" :key="session.path" class="ee-item">
          <div class="ee-item-main">
            <div class="ee-item-title">
              <NText strong>{{ session.path }}</NText>
              <NTag v-if="session.changed" size="small" type="warning">已改动</NTag>
              <NTag v-else size="small" type="success">未改动</NTag>
            </div>
            <NText depth="3" class="ee-item-sub">
              {{ formatSize(session.size) }} · 导出 {{ session.startedAt }}
              <template v-if="session.modTime"> · 最后修改 {{ session.modTime }}</template>
            </NText>
            <NText depth="3" class="ee-item-sub">{{ session.localPath }}</NText>
            <NText v-if="session.note" type="warning" class="ee-item-sub">{{ session.note }}</NText>
          </div>
          <div class="ee-item-actions">
            <NButton size="small" :disabled="acting === session.path" @click="onCheck(session)">
              检查变更
            </NButton>
            <NButton
              size="small"
              type="primary"
              :loading="acting === session.path"
              @click="onApply(session)"
            >
              回填到归档
            </NButton>
            <NButton size="small" quaternary @click="onReveal(session)">打开文件夹</NButton>
            <NButton size="small" quaternary @click="onFinish(session, false)">结束</NButton>
            <NButton size="small" quaternary @click="onFinish(session, true)">删除副本</NButton>
          </div>
        </div>
      </div>
    </div>
    <template #footer>
      <div class="ee-footer">
        <NText depth="3">工作副本位置：用户文档\pvfine 外部编辑\&lt;时间戳&gt;\</NText>
        <NButton @click="close">关闭</NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.ee-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.ee-tip {
  line-height: 1.7;
}
.ee-start {
  display: flex;
  gap: 8px;
}
.ee-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.ee-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 46vh;
  overflow: auto;
}
.ee-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 10px;
  border: 1px solid var(--pvf-border, rgba(128, 128, 128, 0.24));
  border-radius: 6px;
}
.ee-item-main {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.ee-item-title {
  display: flex;
  align-items: center;
  gap: 8px;
}
.ee-item-sub {
  font-size: 12px;
  word-break: break-all;
}
.ee-item-actions {
  display: flex;
  flex: none;
  gap: 6px;
}
.ee-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
</style>
