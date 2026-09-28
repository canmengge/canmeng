import { defineStore } from "pinia";
import { computed, ref, watch } from "vue";
import {
  ListObjectTypes,
  ReloadRules,
  ResolveObject,
  type ObjectTypeInfo,
  type ObjectView,
} from "../services/objectViewApi";
import { useArchiveStore } from "./archive";

/**
 * 对象视图面板状态：以「对象类型 + 对象 ID」为中心聚合一个对象的关联文件与显示文本。
 *
 * 数据全部来自 Go 侧 `ObjectViewService`（只读聚合），这里只负责取数与界面状态。
 */
export const useObjectViewStore = defineStore("objectView", () => {
  const archive = useArchiveStore();

  const types = ref<ObjectTypeInfo[]>([]);
  const rulePath = ref("");
  const typesLoading = ref(false);
  const typesError = ref("");

  const typeId = ref("");
  const objectId = ref("");

  const view = ref<ObjectView | null>(null);
  const resolving = ref(false);
  const error = ref("");

  /** 归档切换时自增，用于丢弃迟到的响应。 */
  const sessionId = ref(0);

  const ready = computed(() => archive.open);
  const canResolve = computed(
    () =>
      ready.value &&
      !resolving.value &&
      typeId.value !== "" &&
      objectId.value.trim() !== ""
  );
  const typeOptions = computed(() =>
    types.value.map((entry) => ({ label: entry.label, value: entry.id }))
  );

  watch(
    () => archive.info?.path ?? "",
    () => reset()
  );

  /** 读取对象类型目录（不依赖已打开的归档）。 */
  async function loadTypes(force = false): Promise<void> {
    if (typesLoading.value) return;
    if (!force && types.value.length > 0) return;
    typesLoading.value = true;
    typesError.value = "";
    try {
      const result = await ListObjectTypes();
      types.value = result?.types ?? [];
      rulePath.value = result?.rulePath ?? "";
      if (typeId.value === "" && types.value.length > 0) {
        typeId.value =
          types.value.find((entry) => entry.id === "equipment")?.id ?? types.value[0].id;
      }
    } catch (issue: any) {
      typesError.value = String(issue?.message ?? issue);
    } finally {
      typesLoading.value = false;
    }
  }

  /** 重新读取数据文件（config/objectview.json）。 */
  async function reloadRules(): Promise<void> {
    if (typesLoading.value) return;
    typesLoading.value = true;
    typesError.value = "";
    try {
      const result = await ReloadRules();
      types.value = result?.types ?? [];
      rulePath.value = result?.rulePath ?? "";
    } catch (issue: any) {
      typesError.value = String(issue?.message ?? issue);
    } finally {
      typesLoading.value = false;
    }
  }

  async function resolve(): Promise<void> {
    if (!canResolve.value) return;
    const archivePath = archive.info?.path ?? "";
    const session = sessionId.value;
    resolving.value = true;
    error.value = "";
    view.value = null;
    try {
      const result = await ResolveObject(typeId.value, objectId.value.trim());
      if (session !== sessionId.value || (archive.info?.path ?? "") !== archivePath) return;
      view.value = result;
    } catch (issue: any) {
      if (session !== sessionId.value) return;
      error.value = String(issue?.message ?? issue);
    } finally {
      if (session === sessionId.value) resolving.value = false;
    }
  }

  /** 清空当前结果（归档切换、或用户手动清空）。 */
  function reset(): void {
    sessionId.value += 1;
    view.value = null;
    error.value = "";
    resolving.value = false;
  }

  return {
    types,
    rulePath,
    typesLoading,
    typesError,
    typeId,
    objectId,
    view,
    resolving,
    error,
    ready,
    canResolve,
    typeOptions,
    loadTypes,
    reloadRules,
    resolve,
    reset,
  };
});
