import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { Run as runDoctorReport, type DoctorReport } from "../services/doctorApi";

/** 归档体检状态：结果保留在内存里，关闭归档后由面板按需重置。 */
export const useDoctorStore = defineStore("doctor", () => {
  const report = ref<DoctorReport | null>(null);
  const running = ref(false);
  const error = ref("");

  const tables = computed(() => report.value?.tables ?? []);
  const missingTables = computed(() => tables.value.filter((table) => !table.exists));
  const protectedTables = computed(() => tables.value.filter((table) => table.protected));
  const topExtensions = computed(() => (report.value?.extensions ?? []).slice(0, 12));
  const unregisteredGroups = computed(() => report.value?.unregisteredTop ?? []);

  async function run(): Promise<DoctorReport | null> {
    if (running.value) return report.value;
    running.value = true;
    error.value = "";
    try {
      const result = await runDoctorReport();
      report.value = result ?? null;
      return report.value;
    } catch (value: unknown) {
      error.value = value instanceof Error ? value.message : String(value);
      return null;
    } finally {
      running.value = false;
    }
  }

  function reset(): void {
    report.value = null;
    error.value = "";
  }

  return {
    report,
    running,
    error,
    tables,
    missingTables,
    protectedTables,
    topExtensions,
    unregisteredGroups,
    run,
    reset,
  };
});
