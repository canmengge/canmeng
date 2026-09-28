import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { ArchiveService } from "../../bindings/pvfine/services";
import type { TreeAnnotation } from "../../bindings/pvfine/services/models";
import { useExplorerStore, type SearchItem } from "./explorer";

/**
 * 搜索视窗的数据层：内容 = 当前搜索命中（来自资源管理器）+ 用户手动收进来的文件。
 * 两者按归档路径去重，显示格式交给 `searchTree.ts` 与左侧文件树共用同一套构树逻辑。
 */
export const useSearchWindowStore = defineStore("searchWindow", () => {
  const explorer = useExplorerStore();
  const manualEntries = ref<SearchItem[]>([]);

  const entries = computed<SearchItem[]>(() => {
    const seen = new Set<string>();
    const result: SearchItem[] = [];
    for (const item of [...manualEntries.value, ...explorer.hits]) {
      const path = normalize(item.path);
      if (!path || seen.has(path)) continue;
      seen.add(path);
      result.push(item);
    }
    return result;
  });

  const manualCount = computed(() => manualEntries.value.length);
  const hasContent = computed(() => entries.value.length > 0);
  const hitCount = computed(() => explorer.hits.length);

  /** 把文件收进搜索视窗（已存在的按路径跳过）。 */
  function addEntries(items: SearchItem[]): { added: number; skipped: number } {
    const next = [...manualEntries.value];
    const seen = new Set(next.map((item) => normalize(item.path)));
    let added = 0;
    let skipped = 0;
    for (const item of items) {
      const path = normalize(item.path);
      if (!path) continue;
      if (seen.has(path)) {
        skipped += 1;
        continue;
      }
      seen.add(path);
      next.push(item);
      added += 1;
    }
    manualEntries.value = next;
    return { added, skipped };
  }

  function removeEntry(key: string): void {
    manualEntries.value = manualEntries.value.filter((item) => item.key !== key);
  }

  function clearManual(): void {
    manualEntries.value = [];
  }

  return {
    manualEntries,
    entries,
    manualCount,
    hasContent,
    hitCount,
    addEntries,
    removeEntry,
    clearManual,
  };
});

function normalize(path: string): string {
  return path.replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
}

/**
 * 取「文件自身 + 各级祖先目录」的目录标注链，结构与搜索结果的 `pathAnnotations` 一致。
 *
 * 搜索命中的标注链由后端随命中一起给出；「收进搜索视窗」的文件只有路径，需要在这里
 * 按层补齐，否则搜索视窗里的目录行没有 `[装备]` 这类注释，与左侧文件树不一致
 * （用户 2026-09-24 反馈）。每层只查一次父目录列表，深度即路径层级，开销很小。
 */
export async function resolvePathAnnotations(
  path: string
): Promise<Record<string, TreeAnnotation[]>> {
  const parts = normalize(path).split("/").filter(Boolean);
  const result: Record<string, TreeAnnotation[]> = {};
  if (parts.length < 2) return result;

  for (let index = 0; index < parts.length - 1; index++) {
    const key = parts.slice(0, index + 1).join("/");
    const parent = parts.slice(0, index).join("/");
    try {
      const nodes = (await ArchiveService.ListChildren(parent)) ?? [];
      const node = nodes.find((candidate) => candidate && candidate.path === key);
      const annotations = (node?.annotations ?? []).filter(
        (annotation): annotation is TreeAnnotation => !!annotation
      );
      if (annotations.length > 0) result[key] = annotations;
    } catch {
      // 归档未打开/已切换：目录不显示注释即可，不影响文件条目本身。
      return result;
    }
  }
  return result;
}
