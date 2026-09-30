import { computed, type ComputedRef } from "vue";
import { useArchiveStore } from "../stores/archive";
import { useEditorStore } from "../stores/editor";
import { useFileSetStore } from "../stores/fileSets";
import { useVersionStore } from "../stores/version";
import { useScriptStore } from "../stores/script";

/**
 * 「当前工作区是否存在未保存的修改」的唯一判定口径。
 *
 * 关闭窗口（CloseGuard）与关闭 PVF（工具栏「关闭」按钮）都走这一处，
 * 避免两处各写一份条件、日后失配（漏判会丢改动，误判会频繁弹窗）。
 */
export function useUnsavedChanges(): ComputedRef<boolean> {
  const archive = useArchiveStore();
  const editor = useEditorStore();
  const fileSets = useFileSetStore();
  const version = useVersionStore();
  const script = useScriptStore();

  return computed(
    () =>
      archive.modifiedCount > 0 ||
      editor.dirtyCount > 0 ||
      // 大文件 TXT 视图里「改了段但还没提交」：tab.text 恒为空，dirtyCount 看不到，
      // 必须单独算进来，否则关窗口时不会提示、改动静默丢失。
      editor.pendingLargeEditCount > 0 ||
      fileSets.dirty ||
      // 工作区分离后脚本内容在独立窗口里：本窗口的 script.dirty 停留在分离那一刻
      // 的值（在那边保存也不会同步回来），必须改用独立窗口上报的 dirty，否则
      // 会在已保存的情况下误报有未保存修改。
      (script.workspaceDetached ? script.detachedDirty : script.dirty) ||
      version.status.changedFiles > 0 ||
      version.status.needsSave
  );
}
