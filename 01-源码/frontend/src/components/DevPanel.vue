<script setup lang="ts">
import { computed, ref } from "vue";
import {
  NButton,
  NIcon,
  NModal,
  NSwitch,
  NTag,
  NText,
  useMessage,
} from "naive-ui";
import {
  Alert20Regular,
  ArrowSync20Regular,
  CheckmarkCircle20Regular,
  Copy20Regular,
  DocumentText20Regular,
} from "@vicons/fluent";
import { useArchiveStore } from "../stores/archive";
import { useDevStore } from "../stores/dev";
import { useDoctorStore } from "../stores/doctor";
import { useEditorStore } from "../stores/editor";
import { useLogStore } from "../stores/log";
import { useSettingsStore } from "../stores/settings";
import { BUILD_LABEL, TEST_BUILD_NO } from "../buildInfo";
import { SimulateUpdateAvailable, TestCheckUpdateNow } from "../services/updateTestApi";

/**
 * 开发者面板（仅「开发人员专用」构建提供）。
 *
 * 用途：把测试编辑器时最常看的几样东西集中到一处——运行环境与日志位置、
 * 打开/建索引耗时、语义索引状态、归档体检、输出日志开关，以及会直接改变
 * 程序行为的几个内部开关（字符串表写保护、AI 写保护、保存前备份）。
 *
 * 全部使用既有服务与 store，不新增后端方法、不改 `frontend/bindings/`。
 */
const archive = useArchiveStore();
const dev = useDevStore();
const doctor = useDoctorStore();
const editor = useEditorStore();
const log = useLogStore();
const settings = useSettingsStore();
const message = useMessage();

const rebuildingIndex = ref(false);
const runningDoctor = ref(false);
const savingChannel = ref(false);
/** 更新通道测试：两个按钮各自的忙碌态 + 最近一次结果提示。 */
const testingUpdate = ref(false);
const simulatingUpdate = ref(false);
const updateTestHint = ref("");

/** 启动日志里记录的路径：前端没有单独的接口，从「进程启动」这条日志取。 */
const startupPaths = computed(() => {
  for (const entry of log.entries) {
    if (entry.message.includes("进程启动")) {
      const fields = entry.fields ?? "";
      return {
        logFile: fieldValue(fields, "日志文件"),
        cacheDir: fieldValue(fields, "缓存目录"),
      };
    }
  }
  return { logFile: "", cacheDir: "" };
});

const archivePath = computed(() => archive.info?.path ?? "");

const doctorSummary = computed(() => {
  const report = doctor.report;
  if (!report) return "";
  return [
    `文件数：${report.fileCount.toLocaleString()}`,
    `目录数：${report.dirCount.toLocaleString()}`,
    `扩展名：${report.extensions.length} 种`,
    `字符串表：${report.tableSummary.present}/${report.tableSummary.total} 存在，缺失 ${report.tableSummary.missing}，禁动 ${report.tableSummary.protected}`,
    `耗时：${formatMs(report.durationMs)}`,
  ].join("\n");
});

const indexSummary = computed(() =>
  [
    `状态：${archive.indexStatus.state}${archive.indexStatus.stage ? ` / ${archive.indexStatus.stage}` : ""}`,
    `进度：${archive.indexStatus.done.toLocaleString()} / ${archive.indexStatus.total.toLocaleString()}（跳过 ${archive.indexStatus.skipped.toLocaleString()}）`,
    `缓存命中：${archive.indexStatus.cacheHit ? "是" : "否"}`,
    archive.indexStatus.error ? `错误：${archive.indexStatus.error}` : "",
  ]
    .filter(Boolean)
    .join("\n")
);

/** 从结构化日志的 fields 串里取出某个键的值（值本身可能含空格）。 */
function fieldValue(fields: string, key: string): string {
  const prefix = `${key}=`;
  const at = fields.indexOf(prefix);
  if (at < 0) return "";
  let rest = fields.slice(at + prefix.length);
  for (const other of ["版本", "参数", "日志目录", "日志文件", "缓存目录", "缓存目录来源"]) {
    if (other === key) continue;
    const cut = rest.indexOf(` ${other}=`);
    if (cut >= 0) rest = rest.slice(0, cut);
  }
  return rest.trim() || "（未记录）";
}

function formatMs(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(3)} s`;
}

async function copyText(text: string, label: string): Promise<void> {
  if (!text) {
    message.warning(`${label} 为空，暂无可复制内容`);
    return;
  }
  try {
    await navigator.clipboard.writeText(text);
    message.success(`已复制${label}`);
  } catch (error: any) {
    message.error(`复制失败：${error?.message ?? error}`);
  }
}

async function onRebuildIndex(): Promise<void> {
  if (rebuildingIndex.value) return;
  rebuildingIndex.value = true;
  try {
    await archive.rebuildSearchIndex();
    message.success("已触发语义索引重建");
  } catch (error: any) {
    message.error(`重建索引失败：${error?.message ?? error}`);
  } finally {
    rebuildingIndex.value = false;
  }
}

async function onRunDoctor(): Promise<void> {
  runningDoctor.value = true;
  try {
    const report = await doctor.run();
    if (report) message.success(`体检完成，用时 ${formatMs(report.durationMs)}`);
    else message.error(`体检失败：${doctor.error}`);
  } finally {
    runningDoctor.value = false;
  }
}

async function onSwitchChannel(channel: "stable" | "dev"): Promise<void> {
  if (settings.updateChannel === channel || savingChannel.value) return;
  savingChannel.value = true;
  try {
    await settings.saveUpdateChannel(channel);
    message.success(
      channel === "dev"
        ? "更新通道已切到「开发人员专用」，重启应用后生效"
        : "更新通道已切到「正式版」，重启应用后生效"
    );
  } catch (error: any) {
    message.error(`保存更新通道失败：${error?.message ?? error}`);
  } finally {
    savingChannel.value = false;
  }
}

/**
 * 「测试手动检查更新」：真实请求当前更新通道的清单并比较版本 —— 效果等同于设置里的
 * 「检查更新」（发现新版本会弹「提示更新」窗）。区别是本入口不依赖框架更新器，
 * 所以开发版也能用；设置里那个保持原样（开发版仍提示"更新功能未初始化"）。
 */
async function onTestCheckUpdate(): Promise<void> {
  if (testingUpdate.value) return;
  testingUpdate.value = true;
  updateTestHint.value = "";
  try {
    const info = await TestCheckUpdateNow();
    updateTestHint.value = info.hasUpdate
      ? `通道连通，当前 ${info.currentVersion || "(未注入)"} → 清单最新 ${info.latestVersion}`
      : `通道连通，已是最新版本（${info.latestVersion || info.currentVersion}）`;
    if (info.hasUpdate) message.warning(`发现新版本 ${info.latestVersion}`);
    else message.success("已是最新版本");
  } catch (error: any) {
    updateTestHint.value = `请求失败：${error?.message ?? error}`;
    message.error(`测试检查更新失败：${error?.message ?? error}`);
  } finally {
    testingUpdate.value = false;
  }
}

/**
 * 「测试自动弹窗更新」：模拟"发布了新版本"，走与真实检查**完全相同的事件链路**，
 * 让界面弹出顾客打开编辑器时会看到的那扇「提示更新」窗口（不发任何网络请求）。
 */
async function onSimulateUpdate(): Promise<void> {
  if (simulatingUpdate.value) return;
  simulatingUpdate.value = true;
  try {
    const info = await SimulateUpdateAvailable();
    updateTestHint.value = `已模拟发现新版本 ${info.latestVersion}，应弹出「提示更新」窗口`;
  } catch (error: any) {
    updateTestHint.value = `模拟失败：${error?.message ?? error}`;
    message.error(`模拟更新弹窗失败：${error?.message ?? error}`);
  } finally {
    simulatingUpdate.value = false;
  }
}

/** 写一条只有界面能看到、不落盘的日志，用来验证日志面板的即时显示链路。 */
function onWriteTestLog(): void {
  const now = new Date();
  const time = [now.getHours(), now.getMinutes(), now.getSeconds()]
    .map((value) => String(value).padStart(2, "0"))
    .join(":");
  log.append({
    time,
    level: "INFO",
    module: "dev",
    message: "开发者面板测试日志",
    fields: `版本=${TEST_BUILD_NO}`,
  });
  if (!log.visible) log.toggle();
  message.success("已写入一条测试日志（仅面板显示，不写入日志文件）");
}

/** 复制当前面板里显示的全部日志行。 */
async function onCopyLogs(): Promise<void> {
  const lines = log.filtered.map(
    (entry) =>
      entry.text ?? `${entry.time} ${entry.level} [${entry.module}] ${entry.message} ${entry.fields ?? ""}`
  );
  await copyText(lines.join("\n"), "日志");
}

function currentFileInfo(): string {
  const tab = editor.activeTab;
  if (!tab) return "";
  return [
    `路径：${tab.path}`,
    `大小：${(tab.size ?? 0).toLocaleString()} 字节`,
    `版本号：${BUILD_LABEL}`,
  ].join("\n");
}

async function onToggleStringGuard(value: boolean): Promise<void> {
  try {
    await settings.saveProtectedStringTableGuard(value);
    message.success(`字符串表写保护已${value ? "开启" : "关闭"}`);
  } catch (error: any) {
    message.error(`保存失败：${error?.message ?? error}`);
  }
}

async function onToggleAIEnabled(value: boolean): Promise<void> {
  try {
    await settings.saveAIAssistant({ ...settings.ai, enabled: value });
    message.success(`AI 助手已${value ? "启用" : "停用"}`);
  } catch (error: any) {
    message.error(`保存失败：${error?.message ?? error}`);
  }
}

async function onToggleAIWriteProtection(value: boolean): Promise<void> {
  try {
    await settings.saveAIAssistant({ ...settings.ai, writeProtection: value });
    message.success(`AI 写保护已${value ? "开启" : "关闭"}`);
  } catch (error: any) {
    message.error(`保存失败：${error?.message ?? error}`);
  }
}

async function onToggleBackup(value: boolean): Promise<void> {
  try {
    await settings.saveBackupSourceOnSave(value);
    message.success(`覆盖保存前备份已${value ? "开启" : "关闭"}`);
  } catch (error: any) {
    message.error(`保存失败：${error?.message ?? error}`);
  }
}

const diagnostics = computed(() =>
  [
    `版本号：${TEST_BUILD_NO}`,
    `更新通道：${settings.updateChannel}`,
    `归档：${archivePath.value || "（未打开）"}`,
    `文件数：${archive.info?.fileCount?.toLocaleString() ?? "—"}`,
    `打开耗时：${formatMs(archive.indexStatus.openDurationMs)}`,
    `建索引耗时：${formatMs(archive.indexStatus.buildDurationMs)}`,
    `索引：${archive.indexStatus.state}`,
    `日志文件：${startupPaths.value.logFile}`,
    `缓存目录：${startupPaths.value.cacheDir}`,
    `日志错误数：${log.errorCount}`,
  ].join("\n")
);
</script>

<template>
  <NModal
    v-model:show="dev.visible"
    preset="card"
    :bordered="false"
    class="dev-modal"
    :style="{ width: 'min(760px, calc(100vw - 32px))' }"
  >
    <template #header>
      <div class="dev-head">
        <span class="dev-badge">DEV</span>
        <span class="dev-title">开发者面板</span>
        <NTag size="small" :bordered="false" type="warning">{{ BUILD_LABEL }}</NTag>
        <span class="dev-sub">仅开发人员专用版本提供 · Ctrl+Shift+D</span>
      </div>
    </template>

    <div class="dev-body">
      <section class="dev-card">
        <div class="dev-card-title">运行环境</div>
        <div class="dev-row">
          <span class="dev-key">更新通道</span>
          <div class="dev-val dev-val--inline">
            <NButton
              size="tiny"
              :type="settings.updateChannel === 'stable' ? 'primary' : 'default'"
              :loading="savingChannel"
              @click="onSwitchChannel('stable')"
            >
              正式版
            </NButton>
            <NButton
              size="tiny"
              :type="settings.updateChannel === 'dev' ? 'primary' : 'default'"
              :loading="savingChannel"
              @click="onSwitchChannel('dev')"
            >
              开发人员专用
            </NButton>
            <NText depth="3">切换后需重启应用生效</NText>
          </div>
        </div>
        <div class="dev-row">
          <span class="dev-key">更新测试</span>
          <div class="dev-val dev-val--inline">
            <NButton size="tiny" :loading="testingUpdate" @click="onTestCheckUpdate">
              <template #icon><NIcon><ArrowSync20Regular /></NIcon></template>
              测试手动检查更新
            </NButton>
            <NButton size="tiny" :loading="simulatingUpdate" @click="onSimulateUpdate">
              <template #icon><NIcon><Alert20Regular /></NIcon></template>
              测试自动弹窗更新
            </NButton>
          </div>
        </div>
        <div v-if="updateTestHint" class="dev-row">
          <span class="dev-key">上次结果</span>
          <span class="dev-val">{{ updateTestHint }}</span>
        </div>
        <div class="dev-row">
          <span class="dev-key">日志文件</span>
          <div class="dev-val">
            <span class="dev-mono">{{ startupPaths.logFile || "（等待启动日志）" }}</span>
            <NButton size="tiny" quaternary @click="copyText(startupPaths.logFile, '日志文件路径')">
              <template #icon><NIcon><Copy20Regular /></NIcon></template>
            </NButton>
          </div>
        </div>
        <div class="dev-row">
          <span class="dev-key">缓存目录</span>
          <div class="dev-val">
            <span class="dev-mono">{{ startupPaths.cacheDir || "（等待启动日志）" }}</span>
            <NButton size="tiny" quaternary @click="copyText(startupPaths.cacheDir, '缓存目录')">
              <template #icon><NIcon><Copy20Regular /></NIcon></template>
            </NButton>
          </div>
        </div>
        <div class="dev-row">
          <span class="dev-key">当前归档</span>
          <div class="dev-val">
            <span class="dev-mono">{{ archivePath || "（未打开）" }}</span>
            <NButton size="tiny" quaternary :disabled="!archivePath" @click="copyText(archivePath, '归档路径')">
              <template #icon><NIcon><Copy20Regular /></NIcon></template>
            </NButton>
          </div>
        </div>
      </section>

      <section class="dev-card">
        <div class="dev-card-title">性能与索引</div>
        <div class="dev-row">
          <span class="dev-key">打开至可操作</span>
          <span class="dev-val dev-strong">{{ formatMs(archive.indexStatus.openDurationMs) }}</span>
        </div>
        <div class="dev-row">
          <span class="dev-key">构建索引</span>
          <span class="dev-val dev-strong">
            {{ formatMs(archive.indexStatus.buildDurationMs) }}
            <NText v-if="archive.indexStatus.cacheHit" depth="3">（缓存命中）</NText>
          </span>
        </div>
        <div class="dev-row">
          <span class="dev-key">文件 / 分组</span>
          <span class="dev-val">
            {{ (archive.info?.fileCount ?? 0).toLocaleString() }} /
            {{ (archive.info?.groupCount ?? 0).toLocaleString() }}
            <NText depth="3"> · {{ archive.info?.paged110 ? "Paged110" : "标准归档" }}</NText>
          </span>
        </div>
        <div class="dev-pre">{{ indexSummary }}</div>
        <div class="dev-actions">
          <NButton size="tiny" :disabled="!archive.open" @click="archive.refreshIndexStatus()">
            <template #icon><NIcon><ArrowSync20Regular /></NIcon></template>
            刷新索引状态
          </NButton>
          <NButton size="tiny" :loading="rebuildingIndex" :disabled="!archive.open" @click="onRebuildIndex">
            重建语义索引
          </NButton>
        </div>
      </section>

      <section class="dev-card">
        <div class="dev-card-title">输出日志（测试）</div>
        <div class="dev-row">
          <span class="dev-key">面板</span>
          <div class="dev-val dev-val--inline">
            <NButton size="tiny" @click="log.toggle()">
              {{ log.visible ? "收起" : "展开" }}
            </NButton>
            <NText depth="3">
              {{ log.entries.length.toLocaleString() }} 行缓存 · 显示
              {{ log.filtered.length.toLocaleString() }} 行
              <template v-if="log.errorCount > 0"> · {{ log.errorCount }} 条错误</template>
            </NText>
          </div>
        </div>
        <div class="dev-actions">
          <NButton size="tiny" @click="log.paused ? log.resume() : (log.paused = true)">
            {{ log.paused ? "继续跟进" : "暂停跟进" }}
          </NButton>
          <NButton size="tiny" @click="onCopyLogs">
            <template #icon><NIcon><Copy20Regular /></NIcon></template>
            复制日志
          </NButton>
          <NButton size="tiny" @click="log.clear()">清空面板</NButton>
          <NButton size="tiny" type="warning" @click="onWriteTestLog">
            <template #icon><NIcon><DocumentText20Regular /></NIcon></template>
            写入测试日志
          </NButton>
        </div>
      </section>

      <section class="dev-card">
        <div class="dev-card-title">归档体检</div>
        <div v-if="doctorSummary" class="dev-pre">{{ doctorSummary }}</div>
        <div v-else class="dev-val">
          <NText depth="3">尚未运行。体检为只读操作，用于核对文件数、目录数、扩展名分布与字符串表指纹。</NText>
        </div>
        <div class="dev-actions">
          <NButton size="tiny" type="primary" :loading="runningDoctor" :disabled="!archive.open" @click="onRunDoctor">
            运行体检
          </NButton>
          <NButton size="tiny" quaternary :disabled="!doctorSummary" @click="copyText(doctorSummary, '体检摘要')">
            复制摘要
          </NButton>
        </div>
      </section>

      <section class="dev-card">
        <div class="dev-card-title">功能开关（立即生效）</div>
        <div class="dev-row">
          <span class="dev-key">字符串表写保护</span>
          <NSwitch size="small" :value="settings.protectedStringTableGuard" @update:value="onToggleStringGuard" />
        </div>
        <div class="dev-row">
          <span class="dev-key">覆盖保存前备份</span>
          <NSwitch size="small" :value="settings.backupSourceOnSave" @update:value="onToggleBackup" />
        </div>
        <div class="dev-row">
          <span class="dev-key">AI 助手</span>
          <NSwitch size="small" :value="settings.ai.enabled" @update:value="onToggleAIEnabled" />
        </div>
        <div class="dev-row">
          <span class="dev-key">AI 写保护</span>
          <NSwitch size="small" :value="settings.ai.writeProtection" @update:value="onToggleAIWriteProtection" />
        </div>
      </section>

      <section class="dev-card">
        <div class="dev-card-title">诊断信息</div>
        <div class="dev-pre">{{ diagnostics }}</div>
        <div class="dev-actions">
          <NButton size="tiny" type="primary" @click="copyText(diagnostics, '诊断信息')">
            <template #icon><NIcon><CheckmarkCircle20Regular /></NIcon></template>
            复制诊断信息
          </NButton>
          <NButton size="tiny" quaternary :disabled="!editor.activeTab" @click="copyText(currentFileInfo(), '当前文件信息')">
            复制当前文件信息
          </NButton>
        </div>
      </section>
    </div>
  </NModal>
</template>

<style scoped>
.dev-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.dev-badge {
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.5px;
  padding: 2px 7px;
  border-radius: 5px;
  color: #1b1b1b;
  background: linear-gradient(135deg, #ffcc66, #f0a020);
}
.dev-title {
  font-size: 15px;
  font-weight: 700;
  color: var(--pvf-text-primary);
}
.dev-sub {
  margin-left: auto;
  font-size: 11px;
  color: var(--pvf-text-faint);
}
.dev-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: min(560px, calc(100vh - 220px));
  overflow: auto;
}
.dev-card {
  border: 1px solid var(--pvf-border-subtle);
  border-radius: 8px;
  background: var(--pvf-surface-card);
  padding: 8px 10px 10px;
}
.dev-card-title {
  font-size: 12px;
  font-weight: 700;
  color: var(--pvf-text-secondary);
  margin-bottom: 6px;
}
.dev-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 24px;
  font-size: 12px;
}
.dev-key {
  flex: 0 0 108px;
  color: var(--pvf-text-muted);
}
.dev-val {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--pvf-text-primary);
  overflow: hidden;
}
.dev-val--inline {
  gap: 8px;
}
.dev-strong {
  color: var(--pvf-primary);
  font-weight: 700;
}
.dev-mono {
  font-family: var(--vscode-editor-font-family, Consolas, "Courier New", monospace);
  font-size: 11.5px;
  color: var(--pvf-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dev-pre {
  margin: 4px 0 6px;
  padding: 6px 8px;
  border-radius: 6px;
  background: rgba(128, 128, 128, 0.08);
  font-family: var(--vscode-editor-font-family, Consolas, "Courier New", monospace);
  font-size: 11.5px;
  line-height: 1.6;
  color: var(--pvf-text-secondary);
  white-space: pre-wrap;
  word-break: break-all;
}
.dev-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 6px;
}
</style>
