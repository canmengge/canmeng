<script setup lang="ts">
import { h, ref, watch } from "vue";
import { NButton, useDialog } from "naive-ui";
import { useArchiveStore } from "../stores/archive";
import { GetKeyStoreInfo, PickKeyFileDialog } from "../services/keyApi";

// 统一处理「打开归档失败」的顶层弹窗。放在 NDialogProvider 之内，任何入口
// （工具栏 / 状态栏 / 快捷键 / 命令面板 / 拖拽 / 开始页 / 最近记录）打开失败都会走到这里。
//
// 2026-09-29 用户要求：**这里成为唯一的「打开失败」提示** ——
//   · 各调用方不再各发一条右下角 message（否则会同时出现弹窗 + message，观感很乱）；
//   · 密钥不匹配时，弹窗内直接提供「选择 sk.dat 文件…」按钮（选一次进密钥库，永久生效）；
//   · 弹窗必须由用户手动关闭（不自动消失），避免错过关键信息。
const archive = useArchiveStore();
const dialog = useDialog();

watch(
  () => archive.openError,
  (error) => {
    if (!error) return;
    const isKey = error.kind === "skdat";
    const storeDir = ref("");
    const picking = ref(false);

    // 只有密钥类错误才需要展示密钥库位置。
    if (isKey) {
      void GetKeyStoreInfo()
        .then((info) => {
          storeDir.value = info?.dir ?? "";
        })
        .catch(() => {
          storeDir.value = "";
        });
    }

    // 记住这个「打开 PVF 失败」弹窗：密钥成功入库后要把它一起关掉，只留成功提示。
    let failedDialog: ReturnType<typeof dialog.error> | undefined;

    const pickKeyFile = async (): Promise<void> => {
      if (picking.value) return;
      picking.value = true;
      try {
        const tip = await PickKeyFileDialog();
        if (tip) {
          // 密钥已入库 —— 关掉失败弹窗，只留「已加入密钥库」这一条，用户接着重新打开即可。
          failedDialog?.destroy();
          dialog.success({
            title: "已加入密钥库",
            content: tip,
            positiveText: "关闭",
            closable: true,
          });
        }
      } catch (e: unknown) {
        dialog.error({
          title: "选择密钥失败",
          content: String((e as { message?: string } | null)?.message ?? e),
          positiveText: "关闭",
          closable: true,
        });
      } finally {
        picking.value = false;
      }
    };

    failedDialog = dialog.error({
      title: "打开 PVF 失败",
      content: () =>
        h("div", { style: "line-height:1.8" }, [
          h("div", { style: "white-space:pre-wrap; word-break:break-all" }, error.message),
          h(
            "div",
            { style: "margin-top:10px; color:#9aa4b2; font-size:12px" },
            isKey
              ? "编辑器已内置两份 sk.dat，通常无需任何额外文件。可点下面的按钮把与该 PVF 配套的 sk.dat 选进密钥库（选一次即永久生效），或直接把它放到 PVF 同一文件夹。"
              : "请确认文件存在、有读取权限，且确实是受支持的 PVF 归档。"
          ),
          isKey
            ? h(
                NButton,
                {
                  size: "small",
                  secondary: true,
                  disabled: picking.value,
                  style: "margin-top:8px",
                  onClick: () => void pickKeyFile(),
                },
                { default: () => "选择 sk.dat 文件…" }
              )
            : null,
          isKey && storeDir.value
            ? h(
                "div",
                { style: "margin-top:6px; color:#9aa4b2; font-size:12px; word-break:break-all" },
                `密钥库目录：${storeDir.value}`
              )
            : null,
        ]),
      positiveText: "关闭",
      closable: true,
      maskClosable: true,
      closeOnEsc: true,
    });
    archive.clearOpenError();
  }
);
</script>

<template>
  <span class="archive-error-dialog" aria-hidden="true" />
</template>
