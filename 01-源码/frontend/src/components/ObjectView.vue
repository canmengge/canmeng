<script setup lang="ts">
import { onMounted } from "vue";
import { ArrowSync24Regular, BoxMultiple24Regular } from "@vicons/fluent";
import {
  NButton,
  NEmpty,
  NIcon,
  NInput,
  NSelect,
  NTag,
  NText,
  NTooltip,
} from "naive-ui";
import { useEditorStore } from "../stores/editor";
import { useObjectViewStore } from "../stores/objectView";
import type {
  ObjectViewFile,
  ObjectViewRegistration,
} from "../services/objectViewApi";

/**
 * 对象视图：以「游戏对象」为单位，把横跨多个 PVF 文件的相关内容聚合到一处。
 *
 * 与左侧文件树互补——文件树以文件为中心，这里以对象为中心。
 * 聚合全部由 Go 侧 `ObjectViewService` 完成（只读），本组件只呈现与跳转。
 */
const objectView = useObjectViewStore();
const editor = useEditorStore();

onMounted(() => {
  void objectView.loadTypes();
});

type TagType = "default" | "success" | "info" | "warning";

function roleLabel(role: string): string {
  if (role === "script") return "脚本";
  if (role === "list") return "登记表";
  if (role === "indexHash") return "索引";
  if (role === "stringTable") return "字符串表";
  if (role === "scriptCandidate") return "同名脚本";
  return role;
}

function roleTagType(role: string): TagType {
  if (role === "script") return "success";
  if (role === "list") return "info";
  if (role === "indexHash") return "warning";
  if (role === "stringTable") return "info";
  if (role === "scriptCandidate") return "success";
  return "default";
}

/** 关联文件行的悬停说明：区分"登记表指向的脚本"与"按 ID 扫到的候选脚本"。 */
function fileHint(file: ObjectViewFile): string {
  if (!file.exists) return "";
  if (file.role === "scriptCandidate") return "按 ID 在归档内扫到的同名脚本，点击打开";
  if (file.role === "stringTable") return "显示文本所在的字符串表，点击打开";
  return "在编辑器中打开";
}

function openFile(file: ObjectViewFile): void {
  if (!file.exists || file.fileIndex < 0) return;
  void editor.openFile(file.fileIndex);
}

function openList(registration: ObjectViewRegistration): void {
  if (registration.listFileIndex < 0) return;
  void editor.openFile(registration.listFileIndex);
}
</script>

<template>
  <div class="object-view">
    <div class="object-heading">
      <div class="object-title">
        <NIcon :size="16"><BoxMultiple24Regular /></NIcon>
        <span>对象</span>
        <NTag size="tiny" :bordered="false">{{ objectView.types.length }}</NTag>
      </div>
      <div class="object-actions">
        <NTooltip>
          <template #trigger>
            <NButton
              quaternary
              circle
              size="tiny"
              aria-label="重载对象类型规则"
              :loading="objectView.typesLoading"
              @click="objectView.reloadRules()"
            >
              <template #icon><NIcon><ArrowSync24Regular /></NIcon></template>
            </NButton>
          </template>
          重载对象类型规则（config/objectview.json）
        </NTooltip>
      </div>
    </div>

    <div class="object-form">
      <NSelect
        v-model:value="objectView.typeId"
        size="small"
        placeholder="对象类型"
        :options="objectView.typeOptions"
      />
      <NInput
        v-model:value="objectView.objectId"
        size="small"
        placeholder="对象 ID，如 10018"
        @keydown.enter.prevent="objectView.resolve()"
      />
      <NButton
        size="small"
        type="primary"
        :disabled="!objectView.canResolve"
        :loading="objectView.resolving"
        @click="objectView.resolve()"
      >
        解析
      </NButton>
    </div>

    <NText v-if="objectView.typesError" type="error" class="object-message">
      {{ objectView.typesError }}
    </NText>

    <div v-if="!objectView.ready" class="object-empty">
      <NEmpty description="请先打开一个 PVF 归档" size="small" />
    </div>
    <div v-else-if="objectView.error" class="object-empty">
      <NEmpty :description="objectView.error" size="small" />
    </div>
    <div v-else-if="!objectView.view" class="object-empty">
      <NEmpty description="输入对象 ID 后点「解析」" size="small" />
    </div>

    <div v-else class="object-body">
      <div class="object-name" :title="objectView.view.objectId">
        {{ objectView.view.name || "（无显示文本）" }}
      </div>
      <div class="object-meta">
        <NTag size="tiny" :bordered="false">{{ objectView.view.objectLabel }}</NTag>
        <NTag size="tiny" :bordered="false">ID {{ objectView.view.objectId }}</NTag>
      </div>

      <div class="object-section">关联文件</div>
      <div
        v-for="file in objectView.view.files"
        :key="file.role + ':' + file.path"
        :class="['object-row', { 'object-row--clickable': file.exists }]"
        :title="fileHint(file)"
        @click="openFile(file)"
      >
        <NTag size="tiny" :bordered="false" :type="roleTagType(file.role)">
          {{ roleLabel(file.role) }}
        </NTag>
        <div class="object-row-main">
          <span class="object-row-path">{{ file.path }}</span>
        </div>
        <NTag v-if="!file.exists" size="tiny" type="warning" :bordered="false">
          不存在
        </NTag>
      </div>

      <div class="object-section">显示文本</div>
      <div v-if="objectView.view.texts.length === 0" class="object-hint">
        脚本内没有 &lt;表号::键名&gt; 占位符，该类型也未配置键名模式
      </div>
      <div
        v-for="text in objectView.view.texts"
        :key="text.tableIndex + '::' + text.key"
        class="object-row"
      >
        <NTag size="tiny" :bordered="false">表 {{ text.tableIndex }}</NTag>
        <div class="object-row-main">
          <span class="object-row-path">{{ text.key }}</span>
          <span class="object-row-value">{{ text.found ? text.value : "未解析" }}</span>
        </div>
        <NTag v-if="!text.found" size="tiny" type="warning" :bordered="false">缺</NTag>
        <NTag v-else-if="text.fallback" size="tiny" type="info" :bordered="false">
          覆盖层
        </NTag>
      </div>

      <div class="object-section">登记项</div>
      <div v-if="objectView.view.registrations.length === 0" class="object-hint">
        该脚本未在任何登记表中被引用
      </div>
      <div
        v-for="registration in objectView.view.registrations"
        :key="registration.listPath + ':' + registration.id"
        :class="[
          'object-row',
          { 'object-row--clickable': registration.listFileIndex >= 0 },
        ]"
        :title="registration.listFileIndex >= 0 ? '打开登记表' : ''"
        @click="openList(registration)"
      >
        <NTag size="tiny" :bordered="false">ID {{ registration.id }}</NTag>
        <div class="object-row-main">
          <span class="object-row-path">{{ registration.entryPath || "—" }}</span>
          <span class="object-row-value">
            {{ registration.category }} · {{ registration.listPath }}
          </span>
        </div>
      </div>

      <template v-if="objectView.view.warnings.length > 0">
        <div class="object-section">告警</div>
        <div
          v-for="(warning, index) in objectView.view.warnings"
          :key="index"
          class="object-warning"
        >
          {{ warning }}
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.object-view {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
}
.object-heading,
.object-form,
.object-title,
.object-actions,
.object-meta,
.object-row {
  display: flex;
  align-items: center;
}
.object-heading {
  justify-content: space-between;
  gap: 8px;
  padding: 8px 8px 6px 12px;
  flex-shrink: 0;
}
.object-title {
  min-width: 0;
  gap: 6px;
  color: var(--pvf-text-primary);
  font-weight: 600;
}
.object-title :deep(.n-icon) {
  color: var(--pvf-text-secondary);
}
.object-actions {
  gap: 1px;
  flex-shrink: 0;
}
.object-form {
  gap: 6px;
  padding: 0 8px 8px 12px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--pvf-border-subtle);
}
.object-form .n-select {
  flex: 0 0 96px;
}
.object-form .n-input {
  min-width: 0;
  flex: 1;
}
.object-message {
  display: block;
  padding: 0 12px 8px;
  font-size: 12px;
}
.object-empty {
  margin-top: 56px;
}
.object-body {
  min-height: 0;
  flex: 1;
  overflow: auto;
  padding: 8px 0 12px;
}
.object-name {
  padding: 0 12px;
  color: var(--pvf-text-primary);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.object-meta {
  gap: 4px;
  padding: 4px 12px 8px;
}
.object-section {
  padding: 8px 12px 4px;
  color: var(--pvf-text-tertiary);
  font-size: 11px;
  font-weight: 600;
}
.object-hint {
  padding: 2px 12px 4px;
  color: var(--pvf-text-muted);
  font-size: 11px;
}
.object-row {
  gap: 6px;
  min-width: 0;
  padding: 4px 12px;
}
.object-row--clickable {
  cursor: pointer;
}
.object-row--clickable:hover {
  background: var(--pvf-surface-hover);
}
.object-row-main {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 1px;
}
.object-row-path,
.object-row-value {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.object-row-path {
  color: var(--pvf-text-secondary);
}
.object-row-value {
  color: var(--pvf-text-muted);
  font-size: 11px;
}
.object-warning {
  padding: 2px 12px;
  color: var(--pvf-warning, #d03050);
  font-size: 11px;
}
</style>
