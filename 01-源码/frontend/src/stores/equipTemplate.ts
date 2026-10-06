import { ref } from "vue";
import { defineStore } from "pinia";

export const useEquipTemplateStore = defineStore("equipTemplate", () => {
  const visible = ref(false);

  function open() {
    visible.value = true;
  }

  function close() {
    visible.value = false;
  }

  return { visible, open, close };
});
