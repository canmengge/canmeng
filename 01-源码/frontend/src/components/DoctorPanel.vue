<script setup lang="ts">
import { computed, ref } from "vue";
import { ArrowSync24Regular, ChevronRight16Regular } from "@vicons/fluent";
import {
  NAlert,
  NButton,
  NCheckbox,
  NEmpty,
  NIcon,
  NTag,
  NText,
  useMessage,
} from "naive-ui";
import { ArchiveService } from "../../bindings/pvfine/services";
import type { TreeNode } from "../../bindings/pvfine/services/models";
import { useDoctorStore } from "../stores/doctor";
import { useEditorStore } from "../stores/editor";

/**
 * 归档体检：一条命令看清文件/目录总量、扩展名分布、字符串表指纹与 lst 登记覆盖。
 * 全部由 Go 侧 `DoctorService` 只读完成，本组件只呈现与跳转。
 */
const doctor = useDoctorStore();
const editor = useEditorStore();
const message = useMessage();
const onlyMissing = ref(false);
const expandedGroups = ref<Set<string>>(new Set());

const visibleTables = computed(() =>
  onlyMissing.value ? doctor.tables.filter((table) => !table.exists) : doctor.tables
);

function sizeText(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

function shortHash(value: string): string {
  return value ? value.slice(0, 12) : "-";
}

async function runDoctor(): Promise<void> {
  const report = await doctor.run();
  if (doctor.error) {
    message.error(doctor.error);
    return;
  }
  if (report) {
    message.success(
      `体检完成：${report.fileCount.toLocaleString()} 个文件，用时 ${(report.durationMs / 1000).toFixed(1)} 秒`
    );
  }
}

function toggleGroup(ext: string): void {
  const next = new Set(expandedGroups.value);
  if (next.has(ext)) next.delete(ext);
  else next.add(ext);
  expandedGroups.value = next;
}

async function openSample(path: string): Promise<void> {
  const nodes = (await ArchiveService.ResolveFiles([path])) ?? [];
  const node = nodes.find((item): item is TreeNode => !!item && !item.isDir);
  if (!node) {
    message.warning("归档里找不到该文件");
    return;
  }
  await editor.openFile(node.fileIndex);
}
</script>

<template>
  <div class="doctor-panel">
    <div class="doctor-header">
      <NText strong>归档体检</NText>
      <NButton size="tiny" :loading="doctor.running" @click="runDoctor">
        {{ doctor.report ? "重新体检" : "开始体检" }}
      </NButton>
    </div>
    <NText depth="3" class="doctor-note">
      只读检查，不改任何内容：文件/目录总量、扩展名分布、字符串表指纹、lst 登记覆盖。
    </NText>

    <NAlert v-if="doctor.error" type="error" :show-icon="false">{{ doctor.error }}</NAlert>

    <div v-if="doctor.running && !doctor.report" class="doctor-loading">
      <NIcon :size="16" class="spin-icon"><ArrowSync24Regular /></NIcon>
      <NText depth="3">正在遍历归档（大归档约 10~30 秒）…</NText>
    </div>

    <NEmpty
      v-if="!doctor.report && !doctor.running"
      size="small"
      description="点「开始体检」生成报告"
    />

    <div v-if="doctor.report" class="doctor-body">
      <div class="doctor-grid">
        <div class="doctor-stat">
          <span class="doctor-stat-label">文件数</span>
          <span>{{ doctor.report.fileCount.toLocaleString() }}</span>
        </div>
        <div class="doctor-stat">
          <span class="doctor-stat-label">目录数</span>
          <span>{{ doctor.report.dirCount.toLocaleString() }}</span>
        </div>
        <div class="doctor-stat">
          <span class="doctor-stat-label">数据块</span>
          <span>{{ doctor.report.groupCount.toLocaleString() }}</span>
        </div>
        <div class="doctor-stat">
          <span class="doctor-stat-label">包体</span>
          <span>{{ sizeText(doctor.report.bodySize) }}</span>
        </div>
        <div class="doctor-stat">
          <span class="doctor-stat-label">类型</span>
          <span>{{ doctor.report.paged110 ? "Paged110" : "标准" }}</span>
        </div>
        <div class="doctor-stat">
          <span class="doctor-stat-label">耗时</span>
          <span>{{ (doctor.report.durationMs / 1000).toFixed(1) }} s</span>
        </div>
      </div>

      <NAlert
        v-for="(warn, index) in doctor.report.warnings"
        :key="`warn-${index}`"
        type="warning"
        :show-icon="false"
      >
        {{ warn }}
      </NAlert>

      <div class="doctor-section">
        <div class="doctor-section-head">
          <NText strong>字符串表</NText>
          <NTag size="tiny" :bordered="false">
            {{ doctor.report.tableSummary.present }}/{{ doctor.report.tableSummary.total }} 存在 ·
            {{ doctor.report.tableSummary.protected }} 张禁动
          </NTag>
          <NCheckbox v-model:checked="onlyMissing" size="small">只看缺失</NCheckbox>
        </div>
        <div class="doctor-table-list">
          <div v-for="table in visibleTables" :key="`t-${table.tableIndex}`" class="doctor-table-row">
            <span class="doctor-table-index">表 {{ table.tableIndex }}</span>
            <span class="doctor-table-path" :title="table.path">{{ table.path }}</span>
            <span class="doctor-table-size">{{ table.exists ? sizeText(table.size) : "缺失" }}</span>
            <span class="doctor-table-hash" :title="table.sha256">{{ shortHash(table.sha256) }}</span>
            <NTag v-if="table.protected" size="tiny" type="warning" :bordered="false">禁动</NTag>
          </div>
        </div>
      </div>

      <div class="doctor-section">
        <div class="doctor-section-head">
          <NText strong>登记覆盖</NText>
          <NTag
            size="tiny"
            :type="doctor.report.listCoverage.unregistered > 0 ? 'warning' : 'success'"
            :bordered="false"
          >
            未登记 {{ doctor.report.listCoverage.unregistered.toLocaleString() }} / 检查
            {{ doctor.report.listCoverage.checked.toLocaleString() }}
          </NTag>
        </div>
        <NText depth="3" class="doctor-note">
          依据：{{ doctor.report.listCoverage.listPaths.join("、") || "无" }}
        </NText>
        <div v-for="group in doctor.unregisteredGroups" :key="`g-${group.ext}`" class="doctor-group">
          <button type="button" class="doctor-group-head" @click="toggleGroup(group.ext)">
            <NIcon :class="['doctor-caret', { 'is-expanded': expandedGroups.has(group.ext) }]">
              <ChevronRight16Regular />
            </NIcon>
            <span>{{ group.ext }}</span>
            <span class="doctor-group-count">{{ group.count.toLocaleString() }} 个未登记</span>
          </button>
          <div v-if="expandedGroups.has(group.ext)" class="doctor-samples">
            <button
              v-for="sample in group.samples"
              :key="sample"
              type="button"
              class="doctor-sample"
              :title="`在编辑器中打开 ${sample}`"
              @click="openSample(sample)"
            >
              {{ sample }}
            </button>
          </div>
        </div>
      </div>

      <div class="doctor-section">
        <div class="doctor-section-head"><NText strong>扩展名分布</NText></div>
        <div class="doctor-tags">
          <NTag v-for="item in doctor.topExtensions" :key="`e-${item.ext}`" size="small" :bordered="false">
            {{ item.ext || "(无扩展名)" }} · {{ item.count.toLocaleString() }}
          </NTag>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.doctor-panel {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
  min-height: 0;
  padding: 10px 12px;
  overflow-y: auto;
}
.doctor-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.doctor-note {
  font-size: 12px;
  line-height: 1.5;
}
.doctor-loading {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 12px 0;
}
.doctor-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.doctor-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px 10px;
  font-size: 12px;
}
.doctor-stat {
  display: flex;
  gap: 6px;
  justify-content: space-between;
}
.doctor-stat-label {
  color: var(--pvf-text-faint, #8b949e);
}
.doctor-section {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding-top: 6px;
  border-top: 1px solid var(--pvf-border, rgba(127, 127, 127, 0.2));
}
.doctor-section-head {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.doctor-table-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 260px;
  overflow-y: auto;
}
.doctor-table-row {
  display: flex;
  gap: 6px;
  align-items: center;
  font-size: 12px;
}
.doctor-table-index {
  flex: none;
  width: 46px;
  color: var(--pvf-text-faint, #8b949e);
}
.doctor-table-path {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.doctor-table-size,
.doctor-table-hash {
  flex: none;
  color: var(--pvf-text-faint, #8b949e);
  font-family: Consolas, monospace;
}
.doctor-group {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.doctor-group-head {
  display: flex;
  gap: 6px;
  align-items: center;
  padding: 3px 2px;
  font-size: 12px;
  color: var(--pvf-text-primary, inherit);
  text-align: left;
  cursor: pointer;
  background: transparent;
  border: none;
}
.doctor-group-count {
  margin-left: auto;
  color: var(--pvf-text-faint, #8b949e);
}
.doctor-caret {
  transition: transform 0.12s ease;
}
.doctor-caret.is-expanded {
  transform: rotate(90deg);
}
.doctor-samples {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding-left: 16px;
}
.doctor-sample {
  padding: 1px 0;
  font-size: 11px;
  color: var(--pvf-text-faint, #8b949e);
  text-align: left;
  cursor: pointer;
  background: transparent;
  border: none;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.doctor-sample:hover {
  color: var(--pvf-text-primary, inherit);
  text-decoration: underline;
}
.doctor-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.spin-icon {
  animation: doctor-spin 1s linear infinite;
}
@keyframes doctor-spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}
</style>
