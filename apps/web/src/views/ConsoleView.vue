<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import {
  AppWindow,
  ArchiveRestore,
  BadgeDollarSign,
  CalendarDays,
  Check,
  CircleStop,
  CreditCard,
  ExternalLink,
  FileDown,
  Gauge,
  KeyRound,
  Link2,
  LogOut,
  Plus,
  Play,
  Receipt,
  RefreshCw,
  RotateCcw,
  Rocket,
  X,
} from "@lucide/vue";
import { api, logout } from "../api";
import BrandMark from "../components/BrandMark.vue";

type Dependency = { key: string; productId: string; serviceSlug: string; required: boolean };
type Product = {
  id: string;
  slug: string;
  name: string;
  versionId: string;
  version: number;
  deployable: boolean;
  missingDependencies?: string[];
  runtimeSpec?: { secretKeys?: string[]; dependencies?: Dependency[] };
  routeSpec?: { containerPort?: number };
};
type App = {
  id: string;
  slug: string;
  status: string;
  productSlug: string;
  lastSuccessfulReleaseId?: string;
  suspensionReason?: string;
  jobState?: string;
  previousReleaseId?: string;
  publicPath?: string;
};
type Usage = {
  usageCode: string;
  unit: string;
  windowStart: string;
  quantity: string;
  amountCents?: number | null;
  sealedAt?: string;
  billingDisposition?: "pending" | "charged" | "unpriced" | "waived_legacy";
};
type Order = {
  id: string;
  amountCents: number;
  status: string;
  createdAt: string;
};
type LedgerEntry = {
  id: number;
  businessType: string;
  businessRef: string;
  amountCents: number;
  balanceAfterCents: number;
  createdAt: string;
};
type Bill = { id: string; periodStart: string; periodEnd: string; currency: string; totalCents: number; itemCount: number; status: 'open' | 'finalized'; updatedAt: string };
type BillItem = { id: string; kind: "usage" | "subscription"; appSlug?: string; usageCode: string; unit: string; quantity: string; pricingVersionId: string; unitPriceMicros: number; amountCents: number; windowStart: string; windowEnd: string };
type CreditGrant = { id:string; amountCents:number; remainingCents:number; businessRef:string; note:string; expiresAt?:string; createdAt:string; active:boolean };
type CreditConsumption = { id:number; grantId:string; amountCents:number; usageCode:string; windowStart:string; createdAt:string };
type PaymentProvider = { provider: string; enabled: boolean };
type Release = {
  id: string;
  releaseNumber: number;
  productVersionId: string;
  state: string;
  createdAt: string;
  jobState?: string;
  jobId?: string;
};
type Announcement = {
  id: string;
  title: string;
  content: string;
  severity: "info" | "warning" | "critical";
  startsAt: string;
  endsAt?: string;
};
type Notification = {
  id: string;
  kind: "low_balance" | "billing_suspended" | "billing_recovered" | "subscription_purchased" | "subscription_purchase_failed" | "subscription_expiring" | "subscription_grace" | "subscription_expired";
  severity: "info" | "warning" | "critical";
  title: string;
  content: string;
  readAt?: string;
  createdAt: string;
};
type AppSecret = { key: string; version: number; createdAt: string };
type AppSecretResponse = { secrets: AppSecret[]; allowedKeys: string[] };
type SubscriptionPlan = {
  planId: string; planVersionId: string; code: string; name: string; version: number;
  cyclePriceCents: number; payableCents: number; purchaseAction: string; current: boolean;
  entitlements: { apps?: number; cpuCores?: number; memoryGiB?: number; creditGrantCents?: number };
};
type CurrentSubscription = {
  planId: string; planVersionId: string; code: string; name: string; status: string;
  cyclePriceCents: number; startsAt: string; endsAt?: string; graceEndsAt?: string;
};
type SubscriptionPurchase = {
  id: string; planVersionId: string; planCode: string; planName: string; action: string; status: string;
  amountCents: number; balanceAfterCents: number; servicePeriodStart: string; servicePeriodEnd: string;
  subscriptionEndsAt?: string; createdAt: string;
};
const products = ref<Product[]>([]),
  apps = ref<App[]>([]),
  usage = ref<Usage[]>([]),
  ledger = ref<LedgerEntry[]>([]),
  bills = ref<Bill[]>([]),
  creditGrants = ref<CreditGrant[]>([]), creditConsumptions = ref<CreditConsumption[]>([]), creditAvailable = ref(0),
  orders = ref<Order[]>([]),
  paymentProviders = ref<PaymentProvider[]>([]),
  announcements = ref<Announcement[]>([]),
  notifications = ref<Notification[]>([]),
  subscriptionPlans = ref<SubscriptionPlan[]>([]),
  currentSubscription = ref<CurrentSubscription | null>(null),
  subscriptionPurchases = ref<SubscriptionPurchase[]>([]),
  releases = ref<Record<string, Release[]>>({}),
  balance = ref(0),
  name = ref(""),
  error = ref(""),
  message = ref(""),
  topup = ref(0),
  busy = ref("");
const activeBill = ref<string>(""), billItems = ref<Record<string, BillItem[]>>({});
const pendingPlan = ref<SubscriptionPlan | null>(null), purchaseKey = ref("");
const impersonation = ref({active:false,readOnly:true,actorName:""});
const writeLocked=computed(()=>impersonation.value.active&&impersonation.value.readOnly);
const secretApp = ref<App | null>(null),
  secretItems = ref<AppSecret[]>([]),
  secretAllowedKeys = ref<string[]>([]),
  secretKey = ref(""),
  secretValue = ref(""),
  secretBusy = ref(false);
const deployProduct = ref<Product | null>(null),
  deploySlug = ref(""),
  deploySecrets = ref<Record<string, string>>({});
const labels: Record<string, string> = {
  queued: "排队中",
  pulling: "拉取镜像",
  starting: "启动容器",
  health_checking: "健康检查",
  switching_route: "切换路由",
  succeeded: "部署完成",
  rolling_back: "恢复上一版本",
  failed: "部署失败",
  deploying: "部署中",
  updating: "更新中",
  stopping: "停止中",
  stopped: "已停止",
  suspended: "已暂停",
};
const state = (value?: string) => labels[value || ""] || value || "等待任务";
const transientAppStates = new Set(["deploying", "updating"]);
function appState(app: App) {
  if (app.status === "running") return "运行中";
  if (app.status === "stopping") return "停止中";
  if (app.status === "stopped") return "已停止";
  if (app.status === "failed") return "部署失败";
  if (app.status === "suspended") {
    if (app.suspensionReason === "billing_insufficient") return "余额不足，已暂停";
    if (app.suspensionReason === "subscription_expired") return "套餐到期，已暂停";
    return "已暂停";
  }
  if (transientAppStates.has(app.status)) return state(app.jobState || app.status);
  return state(app.status);
}
function appStateClass(app: App) {
  if (app.status === "running") return "active";
  if (transientAppStates.has(app.status) || app.status === "stopping") return "pending";
  if (app.status === "failed") return "danger";
  return "suspended";
}
const ledgerLabel = (value: string) => ({
  topup: "充值入账",
  usage: "用量扣费",
  subscription: "套餐费用",
  refund: "退款",
  grant: "赠送额度",
  adjustment: "账户调账",
  reversal: "账本冲正",
}[value] || value);
async function load() {
  const [m, p, a, b, u, l, statementData, creditData, o, n, notices, providers, subscriptions] = await Promise.all([
    api<any>("/me"),
    api<any>("/products"),
    api<any>("/apps"),
    api<any>("/billing/summary"),
    api<any>("/billing/usage"),
    api<{ entries: LedgerEntry[] }>("/billing/ledger"),
    api<{ bills: Bill[] }>("/billing/bills"),
    api<{ availableCents:number; grants:CreditGrant[]; consumptions:CreditConsumption[] }>("/billing/credits"),
    api<any>("/payments/orders"),
    api<{ announcements: Announcement[] }>("/announcements"),
    api<{ notifications: Notification[] }>("/notifications"),
    api<{ providers: PaymentProvider[] }>("/payments/providers"),
    api<{ plans: SubscriptionPlan[]; current: CurrentSubscription | null; purchases: SubscriptionPurchase[] }>("/subscriptions/plans"),
  ]);
  name.value = m.DisplayName;
  impersonation.value={active:Boolean(m.Impersonating),readOnly:Boolean(m.ImpersonationReadOnly),actorName:m.ActorDisplayName||"管理员"};
  products.value = p.products;
  apps.value = a.apps;
  balance.value = b.balanceCents;
  usage.value = u.usage;
  ledger.value = l.entries;
  bills.value = statementData.bills;
  creditAvailable.value=creditData.availableCents;creditGrants.value=creditData.grants;creditConsumptions.value=creditData.consumptions;
  orders.value = o.orders;
  paymentProviders.value = providers.providers;
  announcements.value = n.announcements;
  notifications.value = notices.notifications;
  subscriptionPlans.value = subscriptions.plans;
  currentSubscription.value = subscriptions.current;
  subscriptionPurchases.value = subscriptions.purchases;
  const pairs = await Promise.all(
    apps.value.map(
      async (app) =>
        [
          app.id,
          (await api<any>("/apps/" + app.id + "/releases")).releases,
        ] as const,
    ),
  );
  releases.value = Object.fromEntries(pairs);
}
let appsRefreshTimer: number | undefined;
let appsRefreshInFlight = false;
async function refreshApps(reportError = false) {
  if (appsRefreshInFlight) return;
  appsRefreshInFlight = true;
  try {
    apps.value = (await api<{ apps: App[] }>("/apps")).apps;
  } catch (e) {
    if (reportError) error.value = (e as Error).message;
  } finally {
    appsRefreshInFlight = false;
  }
}
async function toggleBill(bill: Bill) {
  if (activeBill.value === bill.id) { activeBill.value = ""; return; }
  activeBill.value = bill.id;
  if (!billItems.value[bill.id]) {
    const detail = await api<{ items: BillItem[] }>(`/billing/bills/${bill.id}`);
    billItems.value[bill.id] = detail.items;
  }
}
async function exportBill(bill: Bill) {
  try {
    const response = await fetch(`/api/billing/bills/${bill.id}/export`, { headers: { Authorization: `Bearer ${localStorage.getItem('session_token') || ''}` } });
    if (!response.ok) throw new Error(`导出失败（${response.status}）`);
    const url = URL.createObjectURL(await response.blob());
    const link = document.createElement('a'); link.href = url; link.download = `cloudmeter-bill-${bill.periodStart.slice(0, 7)}.csv`; link.click(); URL.revokeObjectURL(url);
  } catch (e) { error.value = (e as Error).message; }
}
async function markNotificationRead(item: Notification) {
  if (item.readAt) return;
  try {
    await api(`/notifications/${item.id}/read`, { method: "PATCH" });
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  }
}
onMounted(async () => {
  try {
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  }
  appsRefreshTimer = window.setInterval(() => {
    if (!document.hidden) void refreshApps();
  }, 5000);
});
onBeforeUnmount(() => {
  if (appsRefreshTimer !== undefined) window.clearInterval(appsRefreshTimer);
});
function openDeploy(p: Product) {
  if (!p.deployable) {
    error.value = p.missingDependencies?.length
      ? `请先部署并运行依赖服务：${p.missingDependencies.join("、")}`
      : "当前套餐不包含此产品";
    return;
  }
  deployProduct.value = p;
  deploySlug.value = p.slug;
  deploySecrets.value = Object.fromEntries(
    (p.runtimeSpec?.secretKeys || []).map((key) => [key, ""]),
  );
}
function closeDeploy() {
  deployProduct.value = null;
  deploySlug.value = "";
  deploySecrets.value = {};
}
function dependencyEndpoint(dependency: Dependency) {
  const target = products.value.find((product) => product.id === dependency.productId);
  const port = target?.routeSpec?.containerPort;
  return `http://${dependency.serviceSlug}${port && port !== 80 ? `:${port}` : ""}`;
}
async function deploy() {
  const p = deployProduct.value;
  if (!p) return;
  try {
    busy.value = "deploy";
    await api("/apps", {
      method: "POST",
      body: JSON.stringify({
        productId: p.id,
        versionId: p.versionId,
        slug: deploySlug.value.trim(),
        idempotencyKey: crypto.randomUUID(),
        secrets: deploySecrets.value,
      }),
    });
    closeDeploy();
    message.value = "部署任务已创建";
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function updateApp(app: App) {
  const p = products.value.find((v) => v.slug === app.productSlug);
  if (!p) return;
  try {
    busy.value = app.id;
    await api("/apps/" + app.id + "/releases", {
      method: "POST",
      body: JSON.stringify({
        versionId: p.versionId,
        idempotencyKey: crypto.randomUUID(),
      }),
    });
    message.value = "更新任务已创建";
    await refreshApps(true);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function rollback(app: App) {
  if (!app.previousReleaseId) {
    error.value = "没有可回滚的历史版本";
    return;
  }
  try {
    busy.value = app.id;
    await api("/apps/" + app.id + "/rollback", {
      method: "POST",
      body: JSON.stringify({
        releaseId: app.previousReleaseId,
        idempotencyKey: crypto.randomUUID(),
      }),
    });
    message.value = "回滚任务已创建";
    await refreshApps(true);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function stopApp(app: App) {
  if (!confirm(`确定停止 ${app.slug}？公网入口会立即下线，CPU 和内存运行计费随即停止；持久卷仍会保留并按实际用量计费。`)) return;
  try {
    busy.value = app.id;
    error.value = "";
    await api(`/apps/${app.id}/stop`, {
      method: "POST",
      body: JSON.stringify({ idempotencyKey: crypto.randomUUID() }),
    });
    message.value = `${app.slug} 正在停止`;
    await refreshApps(true);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function startApp(app: App) {
  if (!confirm(`确定重新启动 ${app.slug}？平台会使用最后一次成功部署的配置重新创建运行实例。`)) return;
  try {
    busy.value = app.id;
    error.value = "";
    await api(`/apps/${app.id}/start`, {
      method: "POST",
      body: JSON.stringify({ idempotencyKey: crypto.randomUUID() }),
    });
    message.value = `${app.slug} 正在启动`;
    await refreshApps(true);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function openSecrets(app: App) {
  secretApp.value = app;
  secretItems.value = [];
  secretAllowedKeys.value = [];
  secretKey.value = "";
  secretValue.value = "";
  try {
    const result = await api<AppSecretResponse>("/apps/" + app.id + "/secrets");
    secretItems.value = result.secrets;
    secretAllowedKeys.value = result.allowedKeys;
    secretKey.value = result.allowedKeys[0] || "";
  } catch (e) {
    error.value = (e as Error).message;
  }
}
function secretKeyLabel(key: string) {
  const current = secretItems.value.find((item) => item.key === key);
  return current ? `${key} · 当前 v${current.version}` : `${key} · 尚未设置`;
}
async function saveSecret() {
  if (!secretApp.value) return;
  const key = secretKey.value;
  if (!secretAllowedKeys.value.includes(key) || !secretValue.value || secretValue.value.length > 65536) {
    error.value = "请选择产品声明的 Secret，并填写不超过 64 KiB 的新值";
    return;
  }
  try {
    secretBusy.value = true;
    await api(
      "/apps/" + secretApp.value.id + "/secrets/" + encodeURIComponent(key),
      { method: "PUT", body: JSON.stringify({ value: secretValue.value }) },
    );
    const result = await api<AppSecretResponse>(
      "/apps/" + secretApp.value.id + "/secrets",
    );
    secretItems.value = result.secrets;
    secretAllowedKeys.value = result.allowedKeys;
    secretKey.value = result.allowedKeys.includes(key) ? key : result.allowedKeys[0] || "";
    secretValue.value = "";
    message.value = "Secret 已创建新版本，下次更新应用时生效";
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    secretBusy.value = false;
  }
}
async function createOrder() {
  if (topup.value <= 0) {
    error.value = "请输入充值金额";
    return;
  }
  try {
    const provider = paymentProviders.value.some((v) => v.provider === "epay" && v.enabled) ? "epay" : "manual";
    const result = await api<{ checkoutUrl?: string }>("/payments/orders", {
      method: "POST",
      body: JSON.stringify({
        amountCents: Math.round(topup.value * 100),
        provider,
        idempotencyKey: crypto.randomUUID(),
      }),
    });
    topup.value = 0;
    if (result.checkoutUrl) {
      window.open(result.checkoutUrl, "_blank", "noopener,noreferrer");
      message.value = "支付页面已打开，请完成支付后返回控制台";
    } else {
      message.value = "充值申请已提交，等待管理员核验入账";
    }
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  }
}
const subscriptionAction = (value: string) => ({ purchase: "购买", renewal: "续期", upgrade: "升级", downgrade: "降级", change: "更换" }[value] || value);
function subscriptionStatus() {
  const current = currentSubscription.value;
  if (!current) return "未分配";
  if (current.status === "grace_period") return `宽限期至 ${new Date(current.graceEndsAt || "").toLocaleString()}`;
  if (current.status === "expired") return "已过期";
  if (current.status === "active") return current.endsAt ? `有效至 ${new Date(current.endsAt).toLocaleString()}` : "长期有效";
  return current.status;
}
function openPurchase(plan: SubscriptionPlan) {
  pendingPlan.value = plan;
  purchaseKey.value = crypto.randomUUID();
  error.value = "";
}
async function confirmPurchase() {
  if (!pendingPlan.value || balance.value < pendingPlan.value.payableCents) return;
  try {
    busy.value = "subscription";
    const result = await api<{ purchase: SubscriptionPurchase; creditGrantedCents: number; resumeJobs: number }>("/subscriptions/purchases", {
      method: "POST",
      body: JSON.stringify({ planVersionId: pendingPlan.value.planVersionId, idempotencyKey: purchaseKey.value }),
    });
    const details = [`已扣款 ¥${(result.purchase.amountCents / 100).toFixed(2)}`];
    if (result.creditGrantedCents) details.push(`已发放 ¥${(result.creditGrantedCents / 100).toFixed(2)} 额度`);
    if (result.resumeJobs) details.push(`正在恢复 ${result.resumeJobs} 个应用`);
    message.value = `${result.purchase.planName}${subscriptionAction(result.purchase.action)}成功，${details.join("，")}`;
    pendingPlan.value = null;
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
async function exitImpersonation(){const token=localStorage.getItem("admin_session_token");if(!token){await logout();return}try{await api('/impersonation',{method:'DELETE'});localStorage.setItem("session_token",token);localStorage.removeItem("admin_session_token");location.assign("/admin")}catch{
  // The global API handler clears both tokens on 401; restore the actor token so
  // the shared logout flow can still revoke the original administrator session.
  localStorage.setItem("admin_session_token",token);
  await logout();
}}
</script>

<template>
  <div class="app-shell">
    <aside>
      <BrandMark />
      <nav>
        <a class="active"><Gauge :size="18" />概览</a
        ><a href="#apps"><AppWindow :size="18" />我的应用</a
        ><a href="#billing"><CreditCard :size="18" />余额与账单</a
        ><a href="#subscription"><BadgeDollarSign :size="18" />套餐</a
        ><a href="#usage"><CreditCard :size="18" />用量明细</a
        ><a href="/console/releases"><Rocket :size="18" />版本历史</a
        ><a href="/console/backups"><ArchiveRestore :size="18" />备份与恢复</a>
      </nav>
      <button class="icon-text" @click="logout">
        <LogOut :size="17" />退出
      </button>
    </aside>
    <main class="workspace">
      <section v-if="impersonation.active" class="impersonation-banner"><div><strong>{{impersonation.actorName}} 正在查看此账户</strong><span>{{impersonation.readOnly?'只读模式，所有写操作已由后端阻止':'代操作模式，所有写操作都会记录审计'}}</span></div><button class="secondary compact" @click="exitImpersonation">返回管理后台</button></section>
      <header>
        <div>
          <p class="eyebrow">控制台</p>
          <h1>晚上好，{{ name || "..." }}</h1>
        </div>
      </header>
      <p v-if="error" class="message">{{ error }}</p>
      <p v-if="message" class="status-ok">{{ message }}</p>
      <section class="metrics">
        <article>
          <small>钱包余额</small
          ><strong>¥ {{ (balance / 100).toFixed(2) }}</strong
          ><span>赠送额度 ¥ {{ (creditAvailable / 100).toFixed(2) }}</span>
        </article>
        <article>
          <small>运行中应用</small
          ><strong>{{
            apps.filter((v) => v.status === "running").length
          }}</strong
          ><span>{{ apps.length }} 个应用实例</span>
        </article>
        <article>
          <small>可用模板</small><strong>{{ products.length }}</strong
          ><span>管理员已发布</span>
        </article>
      </section>
      <section id="subscription" class="subscription-panel">
        <div class="section-heading">
          <div><p class="eyebrow">订阅套餐</p><h2>{{ currentSubscription?.name || "选择套餐" }}</h2></div>
          <span>{{ subscriptionStatus() }}</span>
        </div>
        <div class="subscription-current">
          <div><CalendarDays :size="19" /><span><small>当前周期价</small><strong>¥{{ ((currentSubscription?.cyclePriceCents || 0) / 100).toFixed(2) }}</strong></span></div>
          <p>套餐由你主动购买或续期，不会自动扣款。有效期结束后进入 3 天宽限期。</p>
        </div>
        <div class="plan-options">
          <article v-for="plan in subscriptionPlans" :key="plan.planVersionId" :class="{ current: plan.current }">
            <span class="product-icon"><BadgeDollarSign :size="19" /></span>
            <div class="plan-copy"><strong>{{ plan.name }}</strong><small>{{ plan.code }} · {{ plan.entitlements.apps || 0 }} 个应用 · {{ plan.entitlements.cpuCores || 0 }} 核 / {{ plan.entitlements.memoryGiB || 0 }} GiB</small></div>
            <div class="plan-price"><strong>¥{{ (plan.cyclePriceCents / 100).toFixed(2) }}</strong><small>本次 ¥{{ (plan.payableCents / 100).toFixed(2) }}</small></div>
            <button class="secondary compact" :disabled="writeLocked || busy === 'subscription' || (plan.current && currentSubscription?.status === 'active' && !currentSubscription?.endsAt)" @click="openPurchase(plan)">
              {{ plan.current && currentSubscription?.status === 'active' && !currentSubscription?.endsAt ? '当前套餐' : subscriptionAction(plan.purchaseAction) }}
            </button>
          </article>
        </div>
        <div v-if="subscriptionPurchases.length" class="subscription-history">
          <div class="ledger-heading"><strong>套餐交易</strong><span>最近 {{ Math.min(subscriptionPurchases.length, 5) }} 条</span></div>
          <article v-for="item in subscriptionPurchases.slice(0, 5)" :key="item.id">
            <div><strong>{{ item.planName }} · {{ subscriptionAction(item.action) }}</strong><small>{{ new Date(item.createdAt).toLocaleString() }} · 服务期至 {{ new Date(item.servicePeriodEnd).toLocaleString() }}</small></div>
            <span :class="['ledger-amount', item.status === 'succeeded' ? 'debit' : '']">{{ item.status === 'succeeded' ? `¥${(item.amountCents / 100).toFixed(2)}` : '余额不足' }}</span>
          </article>
        </div>
      </section>
      <section v-if="notifications.length || announcements.length" class="announcement-feed">
        <div class="section-heading">
          <div>
            <p class="eyebrow">平台通知</p>
            <h2>账户消息与公告</h2>
          </div>
          <span>{{ notifications.filter((item) => !item.readAt).length }} 条未读</span>
        </div>
        <article
          v-for="item in notifications"
          :key="item.id"
          :class="['announcement-card', item.severity, { read: item.readAt }]"
        >
          <span class="severity-dot"></span>
          <div>
            <strong>{{ item.title }}</strong>
            <p>{{ item.content }}</p>
            <small>{{ new Date(item.createdAt).toLocaleString() }}</small>
          </div>
          <button v-if="!item.readAt" class="icon-action" title="标记已读" @click="markNotificationRead(item)"><Check :size="17" /></button>
        </article>
        <article
          v-for="item in announcements"
          :key="item.id"
          :class="['announcement-card', item.severity]"
        >
          <span class="severity-dot"></span>
          <div>
            <strong>{{ item.title }}</strong>
            <p>{{ item.content }}</p>
            <small>{{ new Date(item.startsAt).toLocaleString() }}</small>
          </div>
        </article>
      </section>
      <section id="billing" class="billing-panel">
        <div>
          <p class="eyebrow">余额与账单</p>
          <h2>{{ paymentProviders.some((v) => v.provider === 'epay' && v.enabled) ? '账户充值' : '人工充值' }}</h2>
          <p class="quiet">{{ paymentProviders.some((v) => v.provider === 'epay' && v.enabled) ? '使用已配置的在线支付完成充值。' : '提交申请后由管理员核验入账。' }}</p>
        </div>
        <form class="topup-form" @submit.prevent="createOrder">
          <label
            >充值金额（元）<input
              v-model.number="topup"
              type="number"
              min="1"
              step="0.01"
              placeholder="100.00" /></label
          ><button class="primary compact" :disabled="writeLocked">
            <Receipt :size="16" />{{ paymentProviders.some((v) => v.provider === 'epay' && v.enabled) ? '立即支付' : '提交充值申请' }}
          </button>
        </form>
        <div v-if="orders.length" class="order-list">
          <article v-for="order in orders.slice(0, 5)" :key="order.id">
            <span>¥ {{ (order.amountCents / 100).toFixed(2) }}</span
            ><small
              >{{
                order.status === "paid"
                  ? "已入账"
                  : order.status === "refunded"
                    ? "已退款"
                    : "待管理员核验"
              }}
              · {{ new Date(order.createdAt).toLocaleString() }}</small
            >
          </article>
        </div>
        <div class="ledger-list">
          <div class="ledger-heading">
            <strong>账本明细</strong>
            <span>最近 8 条</span>
          </div>
          <article v-for="entry in ledger.slice(0, 8)" :key="entry.id">
            <span :class="['ledger-amount', entry.amountCents >= 0 ? 'credit' : 'debit']">
              {{ entry.amountCents >= 0 ? '+' : '' }}¥{{ (entry.amountCents / 100).toFixed(2) }}
            </span>
            <div>
              <strong>{{ ledgerLabel(entry.businessType) }}</strong>
              <small>{{ new Date(entry.createdAt).toLocaleString() }} · 余额 ¥{{ (entry.balanceAfterCents / 100).toFixed(2) }}</small>
            </div>
          </article>
          <p v-if="!ledger.length" class="quiet empty-copy">还没有账本记录</p>
        </div>
        <div class="statement-list">
          <div class="ledger-heading"><strong>月度账单</strong><span>UTC 自然月</span></div>
          <article v-for="bill in bills" :key="bill.id" class="statement-row">
            <button class="statement-summary" @click="toggleBill(bill)">
              <span><strong>{{ bill.periodStart.slice(0, 7) }}</strong><small>{{ bill.itemCount }} 项费用 · {{ bill.status === 'open' ? '进行中' : '已结算' }}</small></span>
              <strong>¥{{ (bill.totalCents / 100).toFixed(2) }}</strong>
            </button>
            <button class="icon-action" title="导出 CSV" @click="exportBill(bill)"><FileDown :size="17" /></button>
            <div v-if="activeBill === bill.id" class="statement-items">
              <div v-for="item in billItems[bill.id] || []" :key="item.id">
                <span><strong>{{ item.kind === 'subscription' ? `${item.appSlug || '套餐'} · ${subscriptionAction(item.usageCode.replace('subscription.', ''))}` : item.usageCode }}</strong><small>{{ item.kind === 'subscription' ? `服务周期 ${new Date(item.windowStart).toLocaleDateString()} - ${new Date(item.windowEnd).toLocaleDateString()}` : `${item.appSlug || '账户级'} · ${item.quantity} ${item.unit} · ${new Date(item.windowStart).toLocaleString()}` }}</small></span>
                <strong>¥{{ (item.amountCents / 100).toFixed(2) }}</strong>
              </div>
            </div>
          </article>
          <p v-if="!bills.length" class="quiet empty-copy">当前还没有月度账单</p>
        </div>
        <div class="credit-list">
          <div class="ledger-heading"><strong>赠送额度</strong><span>可用 ¥{{(creditAvailable/100).toFixed(2)}}</span></div>
          <article v-for="grant in creditGrants.slice(0,6)" :key="grant.id"><div><strong>{{grant.note||'平台赠送'}}</strong><small>{{grant.businessRef}} · {{grant.expiresAt?'到期 '+new Date(grant.expiresAt).toLocaleString():'长期有效'}}</small></div><span :class="['status-pill',grant.active?'active':'suspended']">¥{{(grant.remainingCents/100).toFixed(2)}} / ¥{{(grant.amountCents/100).toFixed(2)}}</span></article>
          <div v-if="creditConsumptions.length" class="credit-consumptions"><strong>最近抵扣</strong><small v-for="item in creditConsumptions.slice(0,5)" :key="item.id">{{item.usageCode}} · -¥{{(item.amountCents/100).toFixed(2)}} · {{new Date(item.windowStart).toLocaleString()}}</small></div>
          <p v-if="!creditGrants.length" class="quiet empty-copy">当前没有赠送额度</p>
        </div>
      </section>
      <section id="apps" v-if="apps.length" class="product-list">
        <div class="section-heading">
          <div>
            <p class="eyebrow">我的应用</p>
            <h2>部署任务</h2>
          </div>
          <span>{{ apps.length }} 个应用</span>
        </div>
        <article v-for="app in apps" :key="app.id" class="product-row">
          <span class="product-icon"><AppWindow :size="20" /></span>
          <div>
            <strong>{{ app.slug }}</strong
            ><small
              >{{ app.productSlug }} · {{ appState(app) }}</small
            >
          </div>
          <div class="product-controls"><span class="status-pill" :class="appStateClass(app)">{{ appState(app) }}</span><button
            class="icon-action"
            title="管理 Secret"
            @click="openSecrets(app)"
          >
            <KeyRound :size="17" /></button
          ><a
            v-if="app.status === 'running' && app.publicPath"
            class="icon-action"
            title="打开应用"
            :href="app.publicPath"
            target="_blank"
            rel="noopener"
            ><ExternalLink :size="17" /></a
          ><button
            v-if="app.status === 'running'"
            class="icon-action"
            title="更新应用"
            :disabled="writeLocked || busy === app.id"
            @click="updateApp(app)"
          >
            <RefreshCw :size="17" /></button
          ><button
            v-if="app.status === 'running' && app.previousReleaseId"
            class="icon-action"
            title="回滚应用"
            :disabled="writeLocked || busy === app.id"
            @click="rollback(app)"
          >
            <RotateCcw :size="17" />
          </button><button
            v-if="app.status === 'running'"
            class="icon-action stop-action"
            title="停止应用"
            :disabled="writeLocked || busy === app.id"
            @click="stopApp(app)"
          >
            <CircleStop :size="17" />
          </button><button
            v-if="app.status === 'stopped' || (app.status === 'failed' && Boolean(app.lastSuccessfulReleaseId))"
            class="icon-action start-action"
            title="启动应用"
            :disabled="writeLocked || busy === app.id"
            @click="startApp(app)"
          >
            <Play :size="17" />
          </button>
          </div>
        </article>
      </section>
      <section id="usage" class="product-list">
        <div class="section-heading">
          <div>
            <p class="eyebrow">用量与扣费</p>
            <h2>最近用量</h2>
          </div>
          <span>{{ usage.length }} 个窗口</span>
        </div>
        <article
          v-for="item in usage.slice(0, 12)"
          :key="item.usageCode + item.windowStart"
          class="product-row"
        >
          <span class="product-icon"><CreditCard :size="20" /></span>
          <div>
            <strong>{{ item.usageCode }}</strong
            ><small
              >{{ item.quantity }} {{ item.unit }} ·
              {{ new Date(item.windowStart).toLocaleString() }}</small
            >
          </div>
          <span
            class="status-pill"
            :class="item.billingDisposition === 'unpriced' ? 'pending' : item.sealedAt ? 'active' : 'suspended'"
            >{{ item.billingDisposition === 'unpriced' ? "未配置价格" : item.sealedAt ? "已结算" : "待结算"
            }}<template v-if="item.amountCents != null">
              · {{ item.amountCents }} 分</template
            ></span
          >
        </article>
        <p v-if="!usage.length" class="quiet empty-copy">暂无用量记录</p>
      </section>
      <section v-if="products.length" class="product-list">
        <div class="section-heading">
          <div>
            <p class="eyebrow">应用模板</p>
            <h2>产品目录</h2>
          </div>
          <span
            >{{
              products.filter((value) => value.deployable).length
            }}
            个可部署版本</span
          >
        </div>
        <article
          v-for="product in products"
          :key="product.versionId"
          :class="['product-row', { unavailable: !product.deployable }]"
        >
          <span class="product-icon"><AppWindow :size="20" /></span>
          <div>
            <strong>{{ product.name }}</strong
            ><small
              >{{ product.slug }} · v{{ product.version
              }}<template v-if="product.missingDependencies?.length">
                · 缺少 {{ product.missingDependencies.join("、") }}</template><template v-else-if="!product.deployable">
                · 当前套餐未包含</template
              ></small
            >
          </div>
          <button
            class="secondary compact"
            :disabled="writeLocked || !product.deployable"
            @click="openDeploy(product)"
          >
            <Plus :size="16" />部署
          </button>
        </article>
      </section>
    </main>
  </div>
  <div v-if="deployProduct" class="modal-backdrop" @click.self="closeDeploy">
    <section class="secret-dialog deploy-dialog">
      <header>
        <div><p class="eyebrow">部署应用</p><h2>{{ deployProduct.name }}</h2></div>
        <button class="icon-action" title="关闭" @click="closeDeploy"><X :size="18" /></button>
      </header>
      <form @submit.prevent="deploy">
        <label>应用标识<input v-model="deploySlug" required pattern="[a-z0-9][a-z0-9-]{0,62}" autocomplete="off" /></label>
        <div v-if="deployProduct.runtimeSpec?.dependencies?.length" class="deploy-dependencies">
          <div class="deploy-secret-heading"><Link2 :size="17" /><div><strong>内部依赖</strong><small>同一账户内的稳定服务地址</small></div></div>
          <article v-for="dependency in deployProduct.runtimeSpec.dependencies" :key="dependency.key"><div><strong>{{ dependency.key }}</strong><small>{{ dependency.required ? '必须运行' : '可选' }}</small></div><code>{{ dependencyEndpoint(dependency) }}</code></article>
        </div>
        <template v-if="deployProduct.runtimeSpec?.secretKeys?.length">
          <div class="deploy-secret-heading"><KeyRound :size="17" /><div><strong>部署 Secret</strong><small>加密保存，提交后不会再次显示</small></div></div>
          <label v-for="key in deployProduct.runtimeSpec.secretKeys" :key="key">{{ key }}<input v-model="deploySecrets[key]" type="password" required autocomplete="new-password" /></label>
        </template>
        <p v-else class="quiet">此模板不需要额外 Secret。</p>
        <div class="deploy-dialog-actions"><button type="button" class="secondary compact" @click="closeDeploy">取消</button><button class="primary compact" :disabled="writeLocked || busy === 'deploy'"><Rocket :size="16" />创建部署</button></div>
      </form>
    </section>
  </div>
  <div v-if="secretApp" class="modal-backdrop" @click.self="secretApp = null">
    <section class="secret-dialog">
      <header>
        <div>
          <p class="eyebrow">{{ secretApp.slug }}</p>
          <h2>应用 Secret</h2>
        </div>
        <button class="icon-action" title="关闭" @click="secretApp = null">
          <X :size="18" />
        </button>
      </header>
      <div class="secret-list">
        <article v-for="item in secretItems" :key="item.key">
          <KeyRound :size="17" />
          <div>
            <strong>{{ item.key }}</strong
            ><small
              >版本 {{ item.version }} ·
              {{ new Date(item.createdAt).toLocaleString() }}</small
            >
          </div>
        </article>
        <p v-if="!secretItems.length" class="quiet">尚未设置 Secret</p>
      </div>
      <form v-if="secretAllowedKeys.length" @submit.prevent="saveSecret">
        <label
          >Secret 名称<select v-model="secretKey" required>
            <option v-for="key in secretAllowedKeys" :key="key" :value="key">{{ secretKeyLabel(key) }}</option>
          </select></label
        ><label
          >新值<input
            v-model="secretValue"
            type="password"
            maxlength="65536"
            required
            autocomplete="new-password"
            placeholder="不会再次显示" /></label
        ><button class="primary compact" :disabled="writeLocked || secretBusy">
          <KeyRound :size="16" />保存新版本
        </button>
      </form>
      <p v-else class="secret-empty quiet">此产品的已发布版本没有声明可配置的 Secret。</p>
    </section>
  </div>
  <div v-if="pendingPlan" class="modal-backdrop" @click.self="pendingPlan = null">
    <section class="purchase-dialog">
      <header><div><p class="eyebrow">确认套餐交易</p><h2>{{ pendingPlan.name }} · {{ subscriptionAction(pendingPlan.purchaseAction) }}</h2></div><button class="icon-action" title="关闭" @click="pendingPlan = null"><X :size="18" /></button></header>
      <div class="purchase-amount"><span>本次钱包扣款</span><strong>¥{{ (pendingPlan.payableCents / 100).toFixed(2) }}</strong></div>
      <dl><div><dt>周期价格</dt><dd>¥{{ (pendingPlan.cyclePriceCents / 100).toFixed(2) }}</dd></div><div><dt>操作</dt><dd>{{ subscriptionAction(pendingPlan.purchaseAction) }}</dd></div><div><dt>扣款后余额</dt><dd>¥{{ ((balance - pendingPlan.payableCents) / 100).toFixed(2) }}</dd></div></dl>
      <p class="transaction-note">套餐不会自动续费。升级仅收取当前有效周期的正差价；降级或更换低价套餐不退还已经支付的费用。</p>
      <p v-if="balance < pendingPlan.payableCents" class="message">钱包余额不足，请先充值。</p>
      <footer><button class="secondary compact" @click="pendingPlan = null">取消</button><button class="primary compact" :disabled="busy === 'subscription' || balance < pendingPlan.payableCents" @click="confirmPurchase"><BadgeDollarSign :size="16" />确认扣款</button></footer>
    </section>
  </div>
</template>
