<script setup lang="ts">
import { nextTick, onMounted, reactive, ref } from "vue";
import {
  ArrowLeft,
  BadgeCent,
  CheckCircle2,
  LogOut,
  Plus,
  RefreshCw,
  Trash2,
} from "@lucide/vue";
import { api, logout } from "../api";
import BrandMark from "../components/BrandMark.vue";
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
type Target = { id: string; name: string };
type Override = {
  id: string;
  pricingItemId: string;
  pricingVersionId: string;
  scope: string;
  scopeId: string;
  scopeName: string;
  code: string;
  version: number;
};
const items = ref<Item[]>([]),
  selected = ref(""),
  error = ref(""),
  message = ref(""),
  busy = ref(""),
  overrides = ref<Override[]>([]);
const users = ref<Target[]>([]),
  products = ref<Target[]>([]);
const item = reactive({ code: "app.runtime.minutes", unit: "minute" });
const version = reactive({
  unitPriceYuan: 0.01,
  precisionScale: 6,
  roundingMode: "half_up",
  minimumQuantity: "0",
  freeQuantity: "0",
  effectiveAt: "",
});
const pricingDrafts = new Map<string, { unitPriceYuan:number; precisionScale:number; roundingMode:string; minimumQuantity:string; freeQuantity:string; effectiveAt:string }>();
type OverrideForm = { pricingVersionId:string; scope:string; scopeId:string };
const overrideDrafts = new Map<string, OverrideForm>();
const switchingPricing = ref(false);
function defaultPricingVersion() { return { unitPriceYuan:0.01, precisionScale:6, roundingMode:"half_up", minimumQuantity:"0", freeQuantity:"0", effectiveAt:"" }; }
function defaultOverrideForm():OverrideForm { return { pricingVersionId:"", scope:"product", scopeId:"" }; }
function cloneDraft<T>(value:T):T { return JSON.parse(JSON.stringify(value)) as T; }
function savePricingDraft() {
  if (!selected.value) return;
  pricingDrafts.set(selected.value, cloneDraft(version));
  overrideDrafts.set(selected.value, cloneDraft(overrideForm));
}
function loadPricingDraft(id:string) {
  Object.assign(version, cloneDraft(pricingDrafts.get(id) || defaultPricingVersion()));
  Object.assign(overrideForm, cloneDraft(overrideDrafts.get(id) || defaultOverrideForm()));
}
async function selectPricing(id:string) {
  if (id===selected.value || switchingPricing.value) return;
  savePricingDraft();
  switchingPricing.value=true;
  selected.value=id;
  loadPricingDraft(id);
  await nextTick();
  window.setTimeout(()=>switchingPricing.value=false,180);
}
const overrideForm = reactive<OverrideForm>({
  pricingVersionId: "",
  scope: "product",
  scopeId: "",
});
async function load() {
  try {
    const [prices, os, us, ps] = await Promise.all([
      api<{ items: Item[] }>("/admin/pricing"),
      api<{ overrides: Override[] }>("/admin/pricing/overrides"),
      api<{ users: any[] }>("/admin/users"),
      api<{ products: any[] }>("/admin/products"),
    ]);
    items.value = prices.items.filter((entry) => entry.code !== "storage.system.gib_days");
    overrides.value = os.overrides;
    users.value = us.users.map((v) => ({
      id: v.id,
      name: v.displayName + " · " + v.email,
    }));
    products.value = ps.products.map((v) => ({ id: v.id, name: v.name }));
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
    overrideDrafts.set(result.id, defaultOverrideForm());
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
function targets() {
  return overrideForm.scope === "user" ? users.value : products.value;
}
async function saveOverride() {
  if (
    !selected.value ||
    !overrideForm.pricingVersionId ||
    !overrideForm.scopeId
  )
    return;
  try {
    busy.value = "override";
    await api("/admin/pricing/overrides", {
      method: "PUT",
      body: JSON.stringify({ ...overrideForm, pricingItemId: selected.value }),
    });
    done("价格覆盖已保存");
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function removeOverride(id: string) {
  try {
    busy.value = id;
    await api("/admin/pricing/overrides/" + id, { method: "DELETE" });
    done("价格覆盖已删除");
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
function price(v: Version) {
  return (v.unitPriceMicros / 100000000).toFixed(8).replace(/0+$/, "").replace(/\.$/, "");
}
</script>
<template>
  <main class="workspace admin-workspace">
    <header>
      <div>
        <p class="eyebrow">计费中心</p>
        <h1>用量价格</h1>
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
        <section v-if="selected" class="form-panel">
          <h2><BadgeCent :size="19" />价格覆盖</h2>
          <form @submit.prevent="saveOverride">
            <div class="field-row">
              <label
                >覆盖范围<select
                  v-model="overrideForm.scope"
                  @change="overrideForm.scopeId = ''"
                >
                  <option value="product">产品</option>
                  <option value="user">用户</option>
                </select></label
              ><label
                >目标<select v-model="overrideForm.scopeId" required>
                  <option value="" disabled>选择目标</option>
                  <option
                    v-for="target in targets()"
                    :key="target.id"
                    :value="target.id"
                  >
                    {{ target.name }}
                  </option>
                </select></label
              >
            </div>
            <label
              >价格版本<select v-model="overrideForm.pricingVersionId" required>
                <option value="" disabled>选择不可变版本</option>
                <option
                  v-for="v in items.find((i) => i.id === selected)?.versions ||
                  []"
                  :key="v.id"
                  :value="v.id"
                >
                  v{{ v.version }} · ¥ {{ price(v) }}
                </option>
              </select></label
            ><button class="secondary compact" :disabled="busy === 'override'">
              <Plus :size="16" />保存覆盖
            </button>
          </form>
          <div class="override-list">
            <article
              v-for="entry in overrides.filter(
                (v) => v.pricingItemId === selected,
              )"
              :key="entry.id"
              class="override-row"
            >
              <div>
                <strong>{{ entry.scopeName }}</strong
                ><small
                  >{{ entry.scope === "user" ? "用户" : "产品" }} · v{{
                    entry.version
                  }}</small
                >
              </div>
              <button
                class="icon-action"
                title="删除覆盖"
                :disabled="busy === entry.id"
                @click="removeOverride(entry.id)"
              >
                <Trash2 :size="17" />
              </button>
            </article>
            <p
              v-if="!overrides.some((v) => v.pricingItemId === selected)"
              class="quiet empty-copy"
            >
              未配置覆盖，将使用平台默认价格
            </p>
          </div>
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
