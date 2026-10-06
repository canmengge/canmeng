<script setup lang="ts">
import { computed, ref } from "vue";
import { NButton, NInput, NModal, NSelect } from "naive-ui";
import { useEquipTemplateStore } from "../stores/equipTemplate";

interface AttrDef {
  cn: string;
  tag: string;
  format: "range" | "single" | "token";
  category: string;
  options?: { label: string; value: string }[];
}

const ATTR_CATALOG: AttrDef[] = [
  { cn: "物理攻击", tag: "equipment physical attack", format: "range", category: "核心战斗" },
  { cn: "魔法攻击", tag: "equipment magical attack", format: "range", category: "核心战斗" },
  { cn: "物理防御", tag: "equipment physical defense", format: "range", category: "核心战斗" },
  { cn: "附加物防", tag: "add equipment physical defense", format: "range", category: "核心战斗" },
  { cn: "独立攻击", tag: "separate attack", format: "range", category: "核心战斗" },
  { cn: "附加独立", tag: "add separate attack", format: "range", category: "核心战斗" },

  { cn: "攻击速度", tag: "attack speed", format: "single", category: "速度" },
  { cn: "施放速度", tag: "cast speed", format: "single", category: "速度" },
  { cn: "移动速度", tag: "move speed", format: "single", category: "速度" },

  { cn: "全属性强化", tag: "all elemental attack", format: "single", category: "元素" },
  { cn: "全属性抗性", tag: "all elemental resistance", format: "single", category: "元素" },
  { cn: "火属性强化", tag: "fire attack", format: "single", category: "元素" },
  { cn: "水属性强化", tag: "water attack", format: "single", category: "元素" },
  { cn: "暗属性强化", tag: "dark attack", format: "single", category: "元素" },
  { cn: "光属性强化", tag: "light attack", format: "single", category: "元素" },
  { cn: "火属性抗性", tag: "fire resistance", format: "single", category: "元素" },
  { cn: "水属性抗性", tag: "water resistance", format: "single", category: "元素" },
  { cn: "暗属性抗性", tag: "dark resistance", format: "single", category: "元素" },
  { cn: "光属性抗性", tag: "light resistance", format: "single", category: "元素" },
  {
    cn: "属性赋予",
    tag: "elemental property",
    format: "token",
    category: "元素",
    options: [
      { label: "[fire]", value: "[fire]" },
      { label: "[water]", value: "[water]" },
      { label: "[dark]", value: "[dark]" },
      { label: "[light]", value: "[light]" },
    ],
  },

  { cn: "物理暴击", tag: "physical critical hit", format: "single", category: "暴击/生存" },
  { cn: "魔法暴击", tag: "magical critical hit", format: "single", category: "暴击/生存" },
  { cn: "HP 上限", tag: "HP MAX", format: "single", category: "暴击/生存" },
  { cn: "MP 上限", tag: "MP MAX", format: "single", category: "暴击/生存" },
  { cn: "HP 回复", tag: "HP regen speed", format: "single", category: "暴击/生存" },
  { cn: "MP 回复", tag: "MP regen speed", format: "single", category: "暴击/生存" },
  { cn: "硬直", tag: "hit recovery", format: "single", category: "暴击/生存" },
  { cn: "跳跃力", tag: "jump power", format: "single", category: "暴击/生存" },

  {
    cn: "装备部位",
    tag: "equipment type",
    format: "token",
    category: "装备信息",
    options: [
      { label: "武器", value: "`[weapon]`" },
      { label: "上衣", value: "`[coat]`" },
      { label: "下装", value: "`[pants]`" },
      { label: "肩部", value: "`[shoulder]`" },
      { label: "腰带", value: "`[belt]`" },
      { label: "鞋", value: "`[shoes]`" },
      { label: "手镯", value: "`[bracelet]`" },
      { label: "项链", value: "`[necklace]`" },
      { label: "戒指", value: "`[ring]`" },
      { label: "辅助装备", value: "`[support]`" },
      { label: "魔法石", value: "`[magic stone]`" },
      { label: "耳环", value: "`[earring]`" },
    ],
  },
  { cn: "品级", tag: "grade", format: "single", category: "装备信息" },
  {
    cn: "稀有度",
    tag: "rarity",
    format: "token",
    category: "装备信息",
    options: [
      { label: "0 普通(白)", value: "0" },
      { label: "1 高级(蓝)", value: "1" },
      { label: "2 稀有(紫)", value: "2" },
      { label: "3 神器(粉)", value: "3" },
      { label: "4 史诗(橙)", value: "4" },
    ],
  },
  { cn: "等级门槛", tag: "minimum level", format: "single", category: "装备信息" },
  { cn: "耐久度", tag: "durability", format: "single", category: "装备信息" },
  { cn: "重量", tag: "weight", format: "single", category: "装备信息" },
  { cn: "名望", tag: "fame value", format: "single", category: "装备信息" },

  { cn: "价格", tag: "price", format: "single", category: "经济" },
  { cn: "修理价", tag: "repair price", format: "single", category: "经济" },
  { cn: "卖店价", tag: "value", format: "single", category: "经济" },
];

const CATEGORIES = [...new Set(ATTR_CATALOG.map((a) => a.category))];

interface SelectedAttr {
  id: number;
  def: AttrDef;
  valA: string;
  valB: string;
  tokenVal: string;
}

const equipTemplate = useEquipTemplateStore();

let nextId = 1;
const selected = ref<SelectedAttr[]>([]);
const copied = ref(false);

const catalogByCategory = computed(() => {
  const map = new Map<string, AttrDef[]>();
  for (const cat of CATEGORIES) map.set(cat, []);
  for (const attr of ATTR_CATALOG) map.get(attr.category)!.push(attr);
  return map;
});

function addAttr(def: AttrDef) {
  selected.value.push({
    id: nextId++,
    def,
    valA: def.format === "range" ? "" : "",
    valB: "",
    tokenVal: def.options?.[0]?.value ?? "",
  });
}

function removeAttr(id: number) {
  selected.value = selected.value.filter((s) => s.id !== id);
}

function clearAll() {
  selected.value = [];
}

const pvfOutput = computed(() => {
  const blocks: string[] = [];
  for (const item of selected.value) {
    let valueStr = "";
    if (item.def.format === "range") {
      const a = item.valA.trim();
      const b = item.valB.trim();
      if (!a && !b) continue;
      valueStr = `${a || "0"}\t${b || a || "0"}`;
    } else if (item.def.format === "token") {
      if (!item.tokenVal) continue;
      valueStr = item.tokenVal;
    } else {
      const a = item.valA.trim();
      if (!a) continue;
      valueStr = a;
    }
    blocks.push(`[${item.def.tag}]\n\t${valueStr}`);
  }
  return blocks.join("\n\n");
});

async function copyOutput() {
  if (!pvfOutput.value) return;
  try {
    await navigator.clipboard.writeText(pvfOutput.value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1500);
  } catch {
    const ta = document.createElement("textarea");
    ta.value = pvfOutput.value;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand("copy");
    document.body.removeChild(ta);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1500);
  }
}
</script>

<template>
  <NModal
    :show="equipTemplate.visible"
    preset="card"
    title="装备属性模板生成"
    style="width: 900px; max-width: 95vw"
    :mask-closable="true"
    @update:show="(v: boolean) => (v ? equipTemplate.open() : equipTemplate.close())"
  >
    <div class="etg-layout">
      <div class="etg-catalog">
        <div class="etg-catalog-title">属性目录（点击添加）</div>
        <div v-for="cat in CATEGORIES" :key="cat" class="etg-cat-group">
          <div class="etg-cat-name">{{ cat }}</div>
          <div class="etg-cat-items">
            <button
              v-for="attr in catalogByCategory.get(cat)"
              :key="attr.tag"
              type="button"
              class="etg-add-btn"
              :title="`[${attr.tag}]`"
              @click="addAttr(attr)"
            >
              {{ attr.cn }}
            </button>
          </div>
        </div>
      </div>

      <div class="etg-editor">
        <div class="etg-editor-title">已选属性</div>
        <div v-if="selected.length === 0" class="etg-empty">
          从左侧点击添加属性
        </div>
        <div v-else class="etg-rows">
          <div v-for="item in selected" :key="item.id" class="etg-row">
            <div class="etg-row-label">
              <span class="etg-cn">{{ item.def.cn }}</span>
              <span class="etg-tag">[{{ item.def.tag }}]</span>
            </div>
            <div class="etg-row-input">
              <template v-if="item.def.format === 'range'">
                <NInput
                  v-model:value="item.valA"
                  size="small"
                  placeholder="最小值"
                  class="etg-input"
                />
                <span class="etg-sep">~</span>
                <NInput
                  v-model:value="item.valB"
                  size="small"
                  placeholder="最大值"
                  class="etg-input"
                />
              </template>
              <template v-else-if="item.def.format === 'token'">
                <NSelect
                  v-model:value="item.tokenVal"
                  :options="item.def.options"
                  size="small"
                  class="etg-input"
                />
              </template>
              <template v-else>
                <NInput
                  v-model:value="item.valA"
                  size="small"
                  placeholder="数值"
                  class="etg-input"
                />
              </template>
            </div>
            <NButton
              size="tiny"
              quaternary
              type="error"
              @click="removeAttr(item.id)"
            >
              ✕
            </NButton>
          </div>
        </div>

        <div class="etg-output-section">
          <div class="etg-output-header">
            <span class="etg-output-title">生成的 PVF 文本</span>
            <div class="etg-output-actions">
              <NButton
                size="tiny"
                type="primary"
                :disabled="!pvfOutput"
                @click="copyOutput"
              >
                {{ copied ? "已复制" : "复制" }}
              </NButton>
              <NButton size="tiny" quaternary @click="clearAll">
                清空
              </NButton>
            </div>
          </div>
          <textarea
            class="etg-output"
            readonly
            :value="pvfOutput"
            placeholder="选择属性并填值后，这里自动生成 PVF 文本"
          />
        </div>
      </div>
    </div>
  </NModal>
</template>

<style scoped>
.etg-layout {
  display: flex;
  gap: 16px;
  min-height: 420px;
}

.etg-catalog {
  width: 240px;
  flex: none;
  overflow-y: auto;
  border-right: 1px solid var(--pvf-border-subtle, #333);
  padding-right: 12px;
}

.etg-catalog-title,
.etg-editor-title {
  font-weight: 600;
  font-size: 13px;
  margin-bottom: 8px;
  color: var(--pvf-text-primary, #e6e6e6);
}

.etg-cat-group {
  margin-bottom: 10px;
}

.etg-cat-name {
  font-size: 11px;
  color: var(--pvf-text-secondary, #999);
  margin-bottom: 4px;
  font-weight: 600;
}

.etg-cat-items {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.etg-add-btn {
  padding: 2px 8px;
  border-radius: 4px;
  border: 1px solid var(--pvf-border-subtle, #444);
  background: var(--pvf-surface-card, #1e1e1e);
  color: var(--pvf-text-primary, #e6e6e6);
  font-size: 12px;
  cursor: pointer;
  transition: background 100ms;
}

.etg-add-btn:hover {
  background: var(--pvf-hover, #2a2a2a);
  border-color: var(--pvf-accent, #4a9eff);
}

.etg-editor {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.etg-empty {
  color: var(--pvf-text-secondary, #888);
  font-size: 13px;
  padding: 24px 0;
  text-align: center;
}

.etg-rows {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 240px;
  overflow-y: auto;
}

.etg-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 0;
}

.etg-row-label {
  width: 180px;
  flex: none;
  display: flex;
  flex-direction: column;
}

.etg-cn {
  font-size: 13px;
  font-weight: 500;
}

.etg-tag {
  font-size: 10px;
  color: var(--pvf-text-secondary, #888);
  font-family: monospace;
}

.etg-row-input {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 4px;
}

.etg-input {
  flex: 1;
  min-width: 0;
}

.etg-sep {
  color: var(--pvf-text-secondary, #888);
  font-size: 12px;
}

.etg-output-section {
  margin-top: 12px;
  flex: 1;
  display: flex;
  flex-direction: column;
}

.etg-output-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 6px;
}

.etg-output-title {
  font-weight: 600;
  font-size: 13px;
  color: var(--pvf-text-primary, #e6e6e6);
}

.etg-output-actions {
  display: flex;
  gap: 4px;
}

.etg-output {
  flex: 1;
  min-height: 100px;
  resize: vertical;
  border: 1px solid var(--pvf-border-subtle, #333);
  border-radius: 6px;
  background: var(--pvf-surface-input, #141414);
  color: var(--pvf-text-primary, #e6e6e6);
  font-family: "Cascadia Code", "Fira Code", "Consolas", monospace;
  font-size: 12px;
  line-height: 1.5;
  padding: 8px;
  tab-size: 4;
}
</style>
