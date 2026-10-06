/**
 * A6 v2：把「内容搜索」的命中行号发布给编辑器，供概览条（滚动条内侧）打点。
 *
 * 为什么单开一个小模块：搜索发生在「高级搜索」弹窗里，而点要画在编辑器上 —— 两者是
 * **兄弟组件**，没有父子通道。用一个模块级响应式 Map 传递（归档文件索引 → 行号列表），
 * 弹窗写、编辑器读；不为一个单向只读通道去改全局 store。
 *
 * 标记：pvfContentHitMarksA6_20261006
 */
import { ref, type Ref } from "vue";

/** 归档文件索引 → 命中行号（1 基、去重升序） */
export const searchHitLines: Ref<Map<number, number[]>> = ref(new Map());

/** 发布一批命中行号：**整体替换**，避免上一轮的结果残留成"幽灵点"。 */
export function publishSearchHitLines(next: Map<number, number[]>): void {
  searchHitLines.value = next;
}

/** 清空：清空搜索框 / 关闭搜索面板 / 关掉内容搜索时调用。 */
export function clearSearchHitLines(): void {
  if (searchHitLines.value.size > 0) searchHitLines.value = new Map();
}
