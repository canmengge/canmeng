import { defineStore } from "pinia";
import { ref } from "vue";

/**
 * 开发者面板的显隐状态。
 *
 * 只有开发人员专用构建（`VITE_DEV_TOOLS=1`）才会挂载面板与入口；
 * 正式交付版里本 store 不会被任何组件引用。
 */
export const useDevStore = defineStore("dev", () => {
  const visible = ref(false);

  function open(): void {
    visible.value = true;
  }

  function close(): void {
    visible.value = false;
  }

  function toggle(): void {
    visible.value = !visible.value;
  }

  return { visible, open, close, toggle };
});
