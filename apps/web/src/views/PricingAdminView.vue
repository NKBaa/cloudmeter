<script setup lang="ts">
import { nextTick, onMounted, reactive, ref } from "vue";
import {
  ArrowLeft,
  BadgeCent,
  CheckCircle2,
  Plus,
  RefreshCw,
} from "@lucide/vue";
import { api } from "../api";
import { usageCodeLabel, usageUnitLabel } from "../billing-labels";
type Version = {
  id: string;
  version: number;
  unitPriceMicros: number;
  precisionScale: number;
  roundingMode: string;
  minimumQuantity: string;
  freeQuantity: string;
  effectiveAt: string;
  createdAt: string;
};
type Item = {
  id: string;
  code: string;
  unit: string;
  createdAt: string;
  versions: Version[];
};
const items = ref<Item[]>([]),
  selected = ref(""),
  error = ref(""),
  message = ref(""),
  busy = ref("");
const item = reactive({ code: "app.runtime.minutes", unit: "minute" });
const version = reactive({
  unitPriceYuan: 0.01,
  precisionScale: 6,
  roundingMode: "half_up",
  minimumQuantity: "0",
  freeQuantity: "0",
  effectiveAt: "",
});
const pricingDrafts = new Map<
  string,
  {
    unitPriceYuan: number;
    precisionScale: number;
    roundingMode: string;
    minimumQuantity: string;
    freeQuantity: string;
    effectiveAt: string;
  }
>();
const switchingPricing = ref(false);
function defaultPricingVersion() {
  return {
    unitPriceYuan: 0.01,
    precisionScale: 6,
    roundingMode: "half_up",
    minimumQuantity: "0",
    freeQuantity: "0",
    effectiveAt: "",
  };
}
function cloneDraft<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}
function savePricingDraft() {
  if (!selected.value) return;
  pricingDrafts.set(selected.value, cloneDraft(version));
}
function loadPricingDraft(id: string) {
  Object.assign(
    version,
    cloneDraft(pricingDrafts.get(id) || defaultPricingVersion()),
  );
}
async function selectPricing(id: string) {
  if (id === selected.value || switchingPricing.value) return;
  savePricingDraft();
  switchingPricing.value = true;
  selected.value = id;
  loadPricingDraft(id);
  await nextTick();
  window.setTimeout(() => (switchingPricing.value = false), 180);
}
async function load() {
  try {
    const prices = await api<{ items: Item[] }>("/admin/pricing");
    items.value = prices.items.filter(
      (entry) => entry.code !== "storage.system.gib_days",
    );
    if (!selected.value && items.value.length)
      await selectPricing(items.value[0].id);
    error.value = "";
  } catch (e) {
    error.value = (e as Error).message;
  }
}
onMounted(load);
function done(text: string) {
  message.value = text;
  error.value = "";
}
async function createItem() {
  try {
    busy.value = "item";
    savePricingDraft();
    const result = await api<{ id: string }>("/admin/pricing/items", {
      method: "POST",
      body: JSON.stringify(item),
    });
    pricingDrafts.set(result.id, defaultPricingVersion());
    selected.value = result.id;
    loadPricingDraft(result.id);
    done("费用项已创建");
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function createVersion() {
  if (!selected.value) return;
  try {
    busy.value = "version";
    await api("/admin/pricing/items/" + selected.value + "/versions", {
      method: "POST",
      body: JSON.stringify({
        unitPriceMicros: Math.round(version.unitPriceYuan * 100000000),
        precisionScale: version.precisionScale,
        roundingMode: version.roundingMode,
        minimumQuantity: version.minimumQuantity,
        freeQuantity: version.freeQuantity,
        effectiveAt: version.effectiveAt
          ? new Date(version.effectiveAt).toISOString()
          : new Date().toISOString(),
      }),
    });
    const currentID = selected.value;
    const freshDraft = defaultPricingVersion();
    pricingDrafts.set(currentID, freshDraft);
    Object.assign(version, freshDraft);
    done("新价格版本已生效");
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
function price(v: Version) {
  return (v.unitPriceMicros / 100000000)
    .toFixed(8)
    .replace(/0+$/, "")
    .replace(/\.$/, "");
}
</script>
<template>
  <main class="workspace admin-workspace">
    <header>
      <div>
        <p class="eyebrow">计费中心</p>
        <h1>用量价格</h1>
        <p class="quiet">统一管理全平台费用项，并发布不可变价格版本。</p>
      </div>
      <button class="secondary compact" @click="load">
        <RefreshCw :size="16" />刷新
      </button>
    </header>
    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok">{{ message }}</p>
    <div class="pricing-layout">
      <section class="pricing-sidebar">
        <div class="section-heading">
          <div>
            <p class="eyebrow">费用目录</p>
            <h2>费用项</h2>
          </div>
        </div>
        <button
          v-for="entry in items"
          :key="entry.id"
          :class="['pricing-select', { selected: selected === entry.id }]"
          @click="selectPricing(entry.id)"
        >
          <BadgeCent :size="18" /><span
            ><strong>{{ usageCodeLabel(entry.code) }}</strong
            ><small
              >{{ entry.code }} · {{ usageUnitLabel(entry.unit) }}</small
            ></span
          >
        </button>
        <form class="inline-form pricing-create" @submit.prevent="createItem">
          <label>代码<input v-model="item.code" required /></label
          ><label>单位<input v-model="item.unit" required /></label
          ><button class="secondary compact" :disabled="busy === 'item'">
            <Plus :size="16" />添加费用项
          </button>
        </form>
      </section>
      <Transition name="panel-swap" mode="out-in">
        <div
          :key="selected || 'pricing-empty'"
          :class="['pricing-main', switchingPricing && 'is-switching']"
        >
          <section v-if="selected" class="form-panel">
            <h2><BadgeCent :size="19" />发布价格版本</h2>
            <form @submit.prevent="createVersion">
              <div class="field-row">
                <label
                  >每单位价格（元）<input
                    v-model.number="version.unitPriceYuan"
                    type="number"
                    min="0"
                    step="0.00000001"
                    required
                  /><small>统一以人民币元录入，最多支持 8 位小数</small></label
                ><label
                  >生效时间<input
                    v-model="version.effectiveAt"
                    type="datetime-local"
                  /><small>留空立即生效</small></label
                >
              </div>
              <div class="field-row">
                <label
                  >免费用量<input
                    v-model="version.freeQuantity"
                    inputmode="decimal" /></label
                ><label
                  >最低计费用量<input
                    v-model="version.minimumQuantity"
                    inputmode="decimal"
                /></label>
              </div>
              <label
                >舍入方式<select v-model="version.roundingMode">
                  <option value="half_up">四舍五入</option>
                  <option value="up">向上取整</option>
                  <option value="down">向下取整</option>
                </select></label
              ><button class="primary compact" :disabled="busy === 'version'">
                <Plus :size="16" />发布新版本
              </button>
            </form>
          </section>
          <section
            v-for="entry in items.filter((v) => v.id === selected)"
            :key="entry.id"
            class="version-list"
          >
            <div class="section-heading">
              <div>
                <p class="eyebrow">不可变记录</p>
                <h2>{{ usageCodeLabel(entry.code) }}</h2>
              </div>
              <span>{{ entry.versions.length }} 个版本</span>
            </div>
            <article v-for="v in entry.versions" :key="v.id" class="price-row">
              <span class="version-number">v{{ v.version }}</span>
              <div>
                <strong
                  >¥ {{ price(v) }} / {{ usageUnitLabel(entry.unit) }}</strong
                ><small
                  >{{ new Date(v.effectiveAt).toLocaleString() }} 生效 · 免费
                  {{ v.freeQuantity }}</small
                >
              </div>
              <span class="status-pill active">已发布</span
              ><CheckCircle2 class="ok" :size="19" />
            </article>
            <p v-if="!entry.versions.length" class="quiet empty-copy">
              尚无价格，用量会标记为未配置价格且不会扣费
            </p>
          </section>
        </div>
      </Transition>
    </div>
  </main>
</template>
