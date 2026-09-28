<script setup lang="ts">
import { NButton, NInput, NModal, NTag, useMessage } from "naive-ui";
import { useAnnotationEditStore } from "../stores/annotationEdit";

const annotationEdit = useAnnotationEditStore();
const message = useMessage();

async function onSave(): Promise<void> {
  const wasSaved = annotationEdit.title.trim().length > 0;
  await annotationEdit.save();
  if (!annotationEdit.visible) {
    message.success(wasSaved ? "注释已保存，立即生效" : "注释已清除，回退到内置注释");
  }
}

async function onRemove(): Promise<void> {
  await annotationEdit.remove();
  if (!annotationEdit.visible) {
    message.success("已恢复为软件内置注释");
  }
}
</script>

<template>
  <NModal
    :show="annotationEdit.visible"
    preset="card"
    title="编辑注释"
    style="width: 480px"
    :mask-closable="true"
    @update:show="annotationEdit.close()"
  >
    <div class="ae-body">
      <div class="ae-path">
        <NTag size="small" :bordered="false" :type="annotationEdit.isDir ? 'warning' : 'info'">
          {{ annotationEdit.isDir ? "目录" : "文件" }}
        </NTag>
        <span class="ae-path-text" :title="annotationEdit.path">{{ annotationEdit.path }}</span>
      </div>
      <div class="ae-field">
        <div class="ae-label">
          注释标题（树上 / 标签页显示的文本，如「NPC商店」）
          <NTag v-if="annotationEdit.builtinTitle && !annotationEdit.hasOverride" size="tiny" :bordered="false">
            内置：{{ annotationEdit.builtinTitle }}
          </NTag>
          <NTag v-else-if="annotationEdit.hasOverride" size="tiny" :bordered="false" type="warning">
            已有自定义注释
          </NTag>
        </div>
        <NInput
          v-model:value="annotationEdit.title"
          size="small"
          maxlength="40"
          placeholder="留空保存 = 删除该自定义注释"
        />
      </div>
      <div class="ae-field">
        <div class="ae-label">详细说明（鼠标悬停在标签上时显示，可选）</div>
        <NInput
          v-model:value="annotationEdit.content"
          type="textarea"
          size="small"
          rows="3"
          maxlength="500"
          placeholder="例如：这里存放各类商店与 NPC 相关文件"
        />
      </div>
      <div v-if="annotationEdit.error" class="ae-error">{{ annotationEdit.error }}</div>
      <div class="ae-hint">
        保存后立即生效并持久化（对所有归档有效）；「恢复默认」删除自定义注释、回退到软件内置注释。
      </div>
    </div>
    <template #footer>
      <div class="ae-footer">
        <NButton
          size="small"
          quaternary
          :disabled="!annotationEdit.hasOverride || annotationEdit.saving"
          @click="onRemove"
        >
          恢复默认
        </NButton>
        <div class="ae-footer-right">
          <NButton size="small" quaternary @click="annotationEdit.close()">取消</NButton>
          <NButton
            size="small"
            type="primary"
            :loading="annotationEdit.saving"
            @click="onSave"
          >
            保存
          </NButton>
        </div>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.ae-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.ae-path {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.ae-path-text {
  overflow: hidden;
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ae-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.ae-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
}
.ae-error {
  color: #e88080;
  font-size: 12px;
}
.ae-hint {
  color: var(--pvf-text-muted, #9aa4b2);
  font-size: 11px;
  line-height: 1.5;
}
.ae-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.ae-footer-right {
  display: flex;
  gap: 8px;
}
</style>
