<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import {
  ArrowLeft,
  BadgeDollarSign,
  LogOut,
  Plus,
  RefreshCw,
  Save,
  ShoppingCart,
  UserRoundCheck,
} from "@lucide/vue";
import { api, logout } from "../api";
import BrandMark from "../components/BrandMark.vue";
type Version = {
  id: string;
  version: number;
  cyclePriceCents: number;
  entitlements: {
    apps: number;
    cpuCores: number;
    memoryGiB: number;
    systemDiskGiB: number;
    dataDiskGiB: number;
    backupStorageGiB: number;
    backupOperationsPerMonth: number;
    concurrentDeployments: number;
    publicIngresses: number;
    ingressOverageEnabled: boolean;
    egressGiB: number;
    egressOverageEnabled: boolean;
    creditGrantCents: number;
    allowedProductIds?: string[];
  };
  effectiveAt: string;
};
type Plan = {
  id: string;
  code: string;
  name: string;
  purchaseEnabled: boolean;
  versions: Version[];
};
type User = {
  id: string;
  email: string;
  displayName: string;
  subscriptionStatus?: string;
  planName?: string;
  subscriptionEndsAt?: string;
  graceEndsAt?: string;
};
type Product = { id: string; slug: string; name: string };
const plans = ref<Plan[]>([]),
  users = ref<User[]>([]),
  products = ref<Product[]>([]),
  selected = ref(""),
  selectedUser = ref(""),
  selectedVersion = ref(""),
  error = ref(""),
  message = ref(""),
  busy = ref("");
const planForm = reactive({ code: "pro", name: "专业版" }),
  versionForm = reactive({
    cyclePriceCents: 9900,
    apps: 5,
    cpuCores: 4,
    memoryGiB: 8,
    systemDiskGiB: 50,
    dataDiskGiB: 100,
    backupStorageGiB: 100,
    backupOperationsPerMonth: 50,
    concurrentDeployments: 2,
    publicIngresses: 5,
    ingressOverageEnabled: false,
    egressGiB: 10,
    egressOverageEnabled: false,
    creditGrantCents: 0,
    allowedProductIds: [] as string[],
    effectiveAt: "",
  }),
  subscriptionForm = reactive({ endsAt: "" });
const activePlan = computed(() =>
  plans.value.find((v) => v.id === selected.value),
);
async function load() {
  try {
    const [p, u, c] = await Promise.all([
      api<{ plans: Plan[] }>("/admin/plans"),
      api<{ users: User[] }>("/admin/users"),
      api<{ products: Product[] }>("/admin/products"),
    ]);
    plans.value = p.plans;
    users.value = u.users;
    products.value = c.products;
    if (!selected.value && plans.value.length)
      selected.value = plans.value[0].id;
    if (!selectedUser.value && users.value.length)
      selectedUser.value = users.value[0].id;
    error.value = "";
  } catch (e) {
    error.value = (e as Error).message;
  }
}
onMounted(load);
async function createPlan() {
  try {
    busy.value = "plan";
    const result = await api<{ id: string }>("/admin/plans", {
      method: "POST",
      body: JSON.stringify(planForm),
    });
    selected.value = result.id;
    message.value = "套餐已创建";
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
    await api("/admin/plans/" + selected.value + "/versions", {
      method: "POST",
      body: JSON.stringify({
        ...versionForm,
        effectiveAt: versionForm.effectiveAt
          ? new Date(versionForm.effectiveAt).toISOString()
          : new Date().toISOString(),
      }),
    });
    message.value = "套餐版本已发布";
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function updateAvailability(plan: Plan, enabled: boolean) {
  const previous = plan.purchaseEnabled;
  plan.purchaseEnabled = enabled;
  try {
    busy.value = "availability";
    error.value = "";
    await api("/admin/plans/" + plan.id + "/availability", {
      method: "PATCH",
      body: JSON.stringify({ enabled }),
    });
    message.value = enabled ? "已开放用户自助购买" : "已关闭用户自助购买";
    await load();
  } catch (e) {
    plan.purchaseEnabled = previous;
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function assign() {
  if (!selectedUser.value || !selectedVersion.value) return;
  try {
    busy.value = "assign";
    const result = await api<{
      resumeJobs: number;
      creditGrantedCents: number;
    }>(
      "/admin/users/" + selectedUser.value + "/subscription",
      {
        method: "PUT",
        body: JSON.stringify({
          planVersionId: selectedVersion.value,
          endsAt: subscriptionForm.endsAt
            ? new Date(subscriptionForm.endsAt).toISOString()
            : null,
        }),
      },
    );
    const details: string[] = [];
    if (result.creditGrantedCents)
      details.push(`已发放 ¥${(result.creditGrantedCents / 100).toFixed(2)} 本月额度`);
    if (result.resumeJobs)
      details.push(`正在恢复 ${result.resumeJobs} 个应用`);
    message.value = details.length
      ? `用户套餐已更新，${details.join("，")}`
      : "用户套餐已更新，本月额度未重复发放";
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
function subscriptionLabel(user: User) {
  if (user.subscriptionStatus === "grace_period")
    return `宽限期至 ${new Date(user.graceEndsAt || "").toLocaleString()}`;
  if (user.subscriptionStatus === "expired") return "已过期";
  if (user.subscriptionStatus === "active")
    return user.subscriptionEndsAt
      ? `有效至 ${new Date(user.subscriptionEndsAt).toLocaleString()}`
      : "长期有效";
  return "未分配";
}
</script>
<template>
  <div class="app-shell">
    <aside>
      <BrandMark />
      <nav>
        <a href="/admin"><ArrowLeft :size="18" />平台设置</a
        ><a class="active"><BadgeDollarSign :size="18" />套餐管理</a>
      </nav>
      <button class="icon-text" @click="logout">
        <LogOut :size="17" />退出
      </button>
    </aside>
    <main class="workspace admin-workspace">
      <header>
        <div>
          <p class="eyebrow">订阅权益</p>
          <h1>套餐管理</h1>
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
              <p class="eyebrow">套餐目录</p>
              <h2>可分配套餐</h2>
            </div>
          </div>
          <button
            v-for="plan in plans"
            :key="plan.id"
            :class="['pricing-select', { selected: selected === plan.id }]"
            @click="selected = plan.id"
          >
            <BadgeDollarSign :size="18" /><span
              ><strong>{{ plan.name }}</strong
              ><small
                >{{ plan.code }} · {{ plan.versions.length }} 个版本 ·
                {{ plan.purchaseEnabled ? "开放购买" : "仅管理员分配" }}</small
              ></span
            >
          </button>
          <form class="inline-form pricing-create" @submit.prevent="createPlan">
            <label>代码<input v-model="planForm.code" required /></label
            ><label>名称<input v-model="planForm.name" required /></label
            ><button class="secondary compact" :disabled="busy === 'plan'">
              <Plus :size="16" />创建套餐
            </button>
          </form>
        </section>
        <div class="pricing-main">
          <section v-if="activePlan" class="form-panel">
            <div class="plan-availability">
              <span class="plan-availability-icon"><ShoppingCart :size="18" /></span>
              <span>
                <strong>用户自助购买</strong>
                <small>{{ activePlan.purchaseEnabled ? "已开放" : "未开放" }}</small>
              </span>
              <label class="toggle">
                <input
                  :checked="activePlan.purchaseEnabled"
                  type="checkbox"
                  :disabled="busy === 'availability'"
                  @change="
                    updateAvailability(
                      activePlan,
                      ($event.target as HTMLInputElement).checked,
                    )
                  "
                />
                {{ activePlan.purchaseEnabled ? "开放" : "关闭" }}
              </label>
            </div>
            <h2><Save :size="19" />发布套餐版本</h2>
            <form @submit.prevent="createVersion">
              <div class="field-row">
                <label
                  >周期价格（分）<input
                    v-model.number="versionForm.cyclePriceCents"
                    type="number"
                    min="0"
                    required /></label
                ><label
                  >应用数量<input
                    v-model.number="versionForm.apps"
                    type="number"
                    min="1"
                    required
                /></label>
              </div>
              <div class="field-row">
                <label
                  >CPU 核数<input
                    v-model.number="versionForm.cpuCores"
                    type="number"
                    min="0.1"
                    step="0.1"
                    required /></label
                ><label
                  >内存（GiB）<input
                    v-model.number="versionForm.memoryGiB"
                    type="number"
                    min="1"
                    required
                /></label>
              </div>
              <div class="field-row">
                <label
                  >系统盘总量（GiB）<input
                    v-model.number="versionForm.systemDiskGiB"
                    type="number"
                    min="1"
                    step="1"
                    required
                /></label>
                <label
                  >数据盘总量（GiB）<input
                    v-model.number="versionForm.dataDiskGiB"
                    type="number"
                    min="0"
                    step="1"
                    required
                /></label>
              </div>
              <fieldset class="product-entitlements">
                <legend>备份权益</legend>
                <div class="field-row">
                  <label
                    >备份存储（GiB）<input
                      v-model.number="versionForm.backupStorageGiB"
                      type="number"
                      min="0"
                      step="1"
                      required
                  /></label>
                  <label
                    >每月备份次数<input
                      v-model.number="versionForm.backupOperationsPerMonth"
                      type="number"
                      min="0"
                      step="1"
                      required
                  /></label>
                </div>
              </fieldset>
              <label
                >并发部署任务<input
                  v-model.number="versionForm.concurrentDeployments"
                  type="number"
                  min="1"
                  max="1000"
                  step="1"
                  required
                /><small>创建、更新和回滚共用此并发额度。</small></label
              >
              <fieldset class="product-entitlements">
                <legend>赠送额度</legend>
                <label
                  >每月赠送额度（分）<input
                    v-model.number="versionForm.creditGrantCents"
                    type="number"
                    min="0"
                    max="1000000000000"
                    step="1"
                    required
                  /><small>每位用户每个 UTC 自然月最多发放一次；同月换套餐不会重复发放。</small></label
                >
              </fieldset>
              <fieldset class="product-entitlements">
                <legend>公网入口</legend>
                <div class="field-row">
                  <label>入口数量<input v-model.number="versionForm.publicIngresses" type="number" min="0" max="1000" step="1" required /></label>
                  <label class="toggle"><input v-model="versionForm.ingressOverageEnabled" type="checkbox" />允许超额并按价格计费</label>
                </div>
              </fieldset>
              <fieldset class="product-entitlements">
                <legend>允许部署的产品</legend>
                <label
                  v-for="product in products"
                  :key="product.id"
                  class="toggle"
                >
                  <input
                    v-model="versionForm.allowedProductIds"
                    type="checkbox"
                    :value="product.id"
                  />
                  {{ product.name }} <small>{{ product.slug }}</small>
                </label>
                <fieldset class="product-entitlements">
                  <legend>公网出站</legend>
                  <div class="field-row">
                    <label
                      >每月公网出站（GiB）<input
                        v-model.number="versionForm.egressGiB"
                        type="number"
                        min="0"
                        step="0.1"
                        required /></label
                    ><label class="toggle"
                      ><input
                        v-model="versionForm.egressOverageEnabled"
                        type="checkbox"
                      />允许超额并按价格计费</label
                    >
                  </div>
                </fieldset>
                <p v-if="!products.length" class="quiet">
                  还没有可配置的产品。
                </p>
                <small>不勾选表示允许全部产品；勾选后仅允许所选产品。</small>
              </fieldset>
              <label
                >生效时间<input
                  v-model="versionForm.effectiveAt"
                  type="datetime-local"
                /><small>留空立即生效</small></label
              ><button class="primary compact" :disabled="busy === 'version'">
                <Save :size="16" />发布版本
              </button>
            </form>
          </section>
          <section class="form-panel">
            <h2><UserRoundCheck :size="19" />分配用户套餐</h2>
            <form @submit.prevent="assign">
              <label
                >用户<select v-model="selectedUser" required>
                  <option v-for="user in users" :key="user.id" :value="user.id">
                    {{ user.displayName }} · {{ user.email }} ·
                    {{ user.planName || "未分配" }}（{{
                      subscriptionLabel(user)
                    }}）
                  </option>
                </select></label
              ><label
                >套餐版本<select v-model="selectedVersion" required>
                  <optgroup
                    v-for="plan in plans"
                    :key="plan.id"
                    :label="plan.name"
                  >
                    <option
                      v-for="version in plan.versions"
                      :key="version.id"
                      :value="version.id"
                    >
                      v{{ version.version }} ·
                      {{ version.entitlements.apps }} 个应用 · ¥
                      {{ (version.cyclePriceCents / 100).toFixed(2) }} · 赠送
                      {{ ((version.entitlements.creditGrantCents || 0) / 100).toFixed(2) }}
                    </option>
                  </optgroup>
                </select></label
              ><label
                >到期时间<input
                  v-model="subscriptionForm.endsAt"
                  type="datetime-local"
                /><small>留空表示长期有效；到期后有 3 天宽限期。</small></label
              ><button class="primary compact" :disabled="busy === 'assign'">
                <UserRoundCheck :size="16" />分配套餐
              </button>
            </form>
          </section>
        </div>
      </div>
    </main>
  </div>
</template>
