<script setup lang="ts">
import { computed } from "vue";
import { NButton, NTag, useMessage } from "naive-ui";
import { useExternalEditStore } from "../stores/externalEdit";

/**
 * 大文件卡片：**文本根本不许进窗口**。
 *
 * 实测结论（2026-09-30，见操作时间线 / SCRZ 日志）：`list/equipment.lst` 解码后
 * 2740 万字符，只要把它送进 WebView，前端就停摆 43 秒，之后每 11 秒一轮 ——
 * 且与「有没有挂 CodeMirror」「渲染多少行」「文档是空的还是 41 万行」全都无关
 * （空文档挂载 CodeMirror 也要 30 秒）。因此这一档文件改为：
 *   仅展示信息 + 走「导出 → 外部编辑器 → 回填」，后端连文本都不再下发。
 */
const props = defineProps<{
  path: string;
  size: number;
  dataType: number;
}>();

const message = useMessage();
const externalEdit = useExternalEditStore();

const session = computed(
  () => externalEdit.sessions.find((item) => item.path === props.path) ?? null
);
const sizeText = computed(() => `${(props.size / 1048576).toFixed(2)} MB`);
const busy = computed(() => externalEdit.busy === props.path);

async function onOpenInEditor(): Promise<void> {
  try {
    const created = await externalEdit.start(props.path);
    message.success(
      created?.opened
        ? "已导出副本并用系统默认程序打开，改完记得回来「回填到归档」"
        : "已导出副本，请到工作目录里手动打开"
    );
  } catch (error: any) {
    message.error(`导出失败：${error?.message ?? error}`);
  }
}

async function onCheck(): Promise<void> {
  try {
    const checked = await externalEdit.check(props.path);
    if (checked?.changed) message.info("检测到本地副本有改动，可以回填了");
    else message.info("本地副本目前还没有改动");
  } catch (error: any) {
    message.error(`检查失败：${error?.message ?? error}`);
  }
}

async function onApply(): Promise<void> {
  try {
    const result = await externalEdit.apply(props.path);
    if (result?.applied) {
      message.success(result.savedHint || "已回填到归档内存，请继续「保存 PVF」落盘");
    } else {
      message.warning(result?.reason || "没有可回填的内容");
    }
  } catch (error: any) {
    message.error(`回填失败：${error?.message ?? error}`);
  }
}

async function onReveal(): Promise<void> {
  try {
    await externalEdit.reveal(props.path);
  } catch (error: any) {
    message.error(`打开工作目录失败：${error?.message ?? error}`);
  }
}
</script>

<template>
  <div class="large-file-panel">
    <div class="lfp-card">
      <div class="lfp-head">
        <NTag size="small" :bordered="false" type="warning">大文件</NTag>
        <span class="lfp-name">{{ path }}</span>
      </div>

      <div class="lfp-meta">
        归档内大小 {{ sizeText }} · 类型 text {{ dataType }}
      </div>

      <p class="lfp-why">
        这个文件的文本有数千万字符，载入窗口会把界面拖死（实测停摆 40 秒以上），
        所以<b>文本没有载入</b>，也不再给它挂任何注解 / 着色 / 跳转关联。
      </p>

      <p class="lfp-how">
        要改内容：点「用外部编辑器打开」改本地副本 → 回到这里点「回填到归档」→
        最后按工具栏「保存 PVF」落盘。
      </p>

      <div v-if="session" class="lfp-session">
        <div>本地副本：{{ session.localPath }}</div>
        <div>
          状态：<b :class="session.changed ? 'lfp-changed' : ''">{{
            session.changed ? "有改动待回填" : "与导出时一致"
          }}</b>
          · {{ session.size.toLocaleString() }} 字节
        </div>
      </div>

      <div class="lfp-actions">
        <NButton size="small" type="primary" :loading="busy" @click="onOpenInEditor">
          用外部编辑器打开
        </NButton>
        <NButton v-if="session" size="small" :loading="busy" @click="onCheck">
          检查变更
        </NButton>
        <NButton
          v-if="session"
          size="small"
          type="warning"
          :disabled="!session.changed"
          :loading="busy"
          @click="onApply"
        >
          回填到归档
        </NButton>
        <NButton v-if="session" size="small" quaternary @click="onReveal">
          打开工作目录
        </NButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.large-file-panel {
  display: flex;
  flex: 1;
  align-items: flex-start;
  justify-content: center;
  min-height: 0;
  overflow: auto;
  padding: 28px 16px;
}
.lfp-card {
  width: min(720px, 100%);
  padding: 16px 18px;
  border: 1px solid var(--pvf-border-subtle, rgba(255, 255, 255, 0.12));
  border-radius: 8px;
  background: var(--pvf-surface-panel, rgba(255, 255, 255, 0.03));
}
.lfp-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.lfp-name {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  overflow-wrap: anywhere;
}
.lfp-meta {
  margin-top: 8px;
  color: var(--pvf-text-secondary);
  font-size: 12px;
}
.lfp-why,
.lfp-how {
  margin: 10px 0 0;
  color: var(--pvf-text-secondary);
  font-size: 12.5px;
  line-height: 1.7;
}
.lfp-session {
  margin-top: 10px;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--pvf-surface-subtle, rgba(255, 255, 255, 0.04));
  color: var(--pvf-text-secondary);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.lfp-changed {
  color: var(--pvf-warning);
}
.lfp-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
}
</style>
