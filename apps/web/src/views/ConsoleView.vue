<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import {
  AppWindow,
  ArchiveRestore,
  BadgeDollarSign,
  CalendarDays,
  CalendarCheck,
  ChevronLeft,
  ChevronRight,
  Gift,
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
  Settings2,
  X,
} from "@lucide/vue";
import { api, logout } from "../api";
import BrandMark from "../components/BrandMark.vue";
import { usageCodeLabel, usageUnitLabel } from "../billing-labels";

type Dependency = {
  key: string;
  productId: string;
  serviceSlug: string;
  required: boolean;
};
type Volume = { name: string; mountPath: string; sizeGiB: number };
type Product = {
  id: string;
  slug: string;
  name: string;
  versionId: string;
  version: number;
  deployable: boolean;
  missingDependencies?: string[];
  runtimeSpec?: {
    cpuCores?: number;
    memoryMiB?: number;
    systemDiskGiB?: number;
    command?: string[];
    env?: Record<string, string>;
    editableEnvKeys?: string[];
    secretKeys?: string[];
    dependencies?: Dependency[];
    volumes?: Volume[];
  };
  routeSpec?: {
    containerPort?: number;
  };
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
type Bill = {
  id: string;
  periodStart: string;
  periodEnd: string;
  currency: string;
  totalCents: number;
  itemCount: number;
  status: "open" | "finalized";
  updatedAt: string;
};
type BillItem = {
  id: string;
  kind: "usage" | "subscription";
  appSlug?: string;
  usageCode: string;
  unit: string;
  quantity: string;
  pricingVersionId: string;
  unitPriceMicros: number;
  amountCents: number;
  windowStart: string;
  windowEnd: string;
};
type CreditGrant = {
  id: string;
  amountCents: number;
  remainingCents: number;
  businessRef: string;
  note: string;
  expiresAt?: string;
  createdAt: string;
  active: boolean;
};
type CreditConsumption = {
  id: number;
  grantId: string;
  amountCents: number;
  usageCode: string;
  windowStart: string;
  createdAt: string;
};
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
  kind:
    | "low_balance"
    | "billing_suspended"
    | "billing_recovered"
    | "subscription_purchased"
    | "subscription_purchase_failed"
    | "subscription_expiring"
    | "subscription_grace"
    | "subscription_expired";
  severity: "info" | "warning" | "critical";
  title: string;
  content: string;
  readAt?: string;
  createdAt: string;
};
type AppSecret = { key: string; version: number; createdAt: string };
type AppSecretResponse = { secrets: AppSecret[]; allowedKeys: string[] };
type CheckinSummary = {
  enabled: boolean;
  checkedInToday: boolean;
  totalCheckins: number;
  monthRewardCents: number;
  totalRewardCents: number;
  month: string;
  checkedDates: string[];
  minRewardCents: number;
  maxRewardCents: number;
};
const props = defineProps<{
  page?:
    | "overview"
    | "deploy"
    | "apps"
    | "billing"
    | "recharge"
    | "usage"
    | "checkin";
}>();
const page = computed(() => props.page || "overview");
const pageTitle = computed(
  () =>
    ({
      overview: "资源概览",
      deploy: "部署应用",
      apps: "我的应用",
      billing: "余额与账单",
      recharge: "账户充值",
      usage: "用量明细",
      checkin: "每日签到",
    })[page.value],
);
const products = ref<Product[]>([]),
  apps = ref<App[]>([]),
  usage = ref<Usage[]>([]),
  ledger = ref<LedgerEntry[]>([]),
  bills = ref<Bill[]>([]),
  creditGrants = ref<CreditGrant[]>([]),
  creditConsumptions = ref<CreditConsumption[]>([]),
  creditAvailable = ref(0),
  orders = ref<Order[]>([]),
  paymentProviders = ref<PaymentProvider[]>([]),
  announcements = ref<Announcement[]>([]),
  notifications = ref<Notification[]>([]),
  releases = ref<Record<string, Release[]>>({}),
  balance = ref(0),
  name = ref(""),
  error = ref(""),
  message = ref(""),
  topup = ref(0),
  selectedTopup = ref(100),
  busy = ref("");
const activeBill = ref<string>(""),
  billItems = ref<Record<string, BillItem[]>>({});
const impersonation = ref({ active: false, readOnly: true, actorName: "" });
const writeLocked = computed(
  () => impersonation.value.active && impersonation.value.readOnly,
);
const secretApp = ref<App | null>(null),
  secretItems = ref<AppSecret[]>([]),
  secretAllowedKeys = ref<string[]>([]),
  secretKey = ref(""),
  secretValue = ref(""),
  secretBusy = ref(false);
const deployProduct = ref<Product | null>(null),
  deploySlug = ref(""),
  deploySecrets = ref<Record<string, string>>({}),
  deployCPU = ref(1),
  deployMemory = ref(512),
  deploySystemDisk = ref(5),
  deployVolumes = ref<Record<string, number>>({});
const deployCommand = ref("");
const deployPort = ref(8080);
const deployEnvironment = ref<{ key: string; value: string }[]>([]);
const deployDependencies = ref<Dependency[]>([]);
const checkin = ref<CheckinSummary | null>(null),
  checkinMonth = ref(
    new Date()
      .toLocaleDateString("sv-SE", { timeZone: "Asia/Shanghai" })
      .slice(0, 7),
  );
const calendarDays = computed(() => {
  const [year, month] = checkinMonth.value.split("-").map(Number);
  const count = new Date(year, month, 0).getDate();
  const offset = (new Date(year, month - 1, 1).getDay() + 6) % 7;
  return [
    ...Array(offset).fill(null),
    ...Array.from({ length: count }, (_, i) => i + 1),
  ];
});
const todayShanghai = () =>
  new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Shanghai" });
const dateKey = (day: number) =>
  `${checkinMonth.value}-${String(day).padStart(2, "0")}`;
const isChecked = (day: number) =>
  Boolean(checkin.value?.checkedDates.includes(dateKey(day)));
const isToday = (day: number) => dateKey(day) === todayShanghai();
async function loadCheckin() {
  checkin.value = await api<CheckinSummary>(
    `/checkin?month=${checkinMonth.value}`,
  );
}
async function changeCheckinMonth(offset: number) {
  const [year, month] = checkinMonth.value.split("-").map(Number);
  const next = new Date(year, month - 1 + offset, 1);
  checkinMonth.value = `${next.getFullYear()}-${String(next.getMonth() + 1).padStart(2, "0")}`;
  await loadCheckin();
}
async function doCheckin() {
  try {
    busy.value = "checkin";
    error.value = "";
    const result = await api<{ rewardCents: number; balanceCents: number }>(
      "/checkin",
      { method: "POST" },
    );
    balance.value = result.balanceCents;
    message.value = `签到成功，获得 ¥${(result.rewardCents / 100).toFixed(2)}`;
    await Promise.all([loadCheckin(), load()]);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
const topupOptions = [10, 20, 50, 100, 200, 300, 400, 500];
const totalSpend = computed(() =>
  ledger.value
    .filter((item) => item.amountCents < 0)
    .reduce((sum, item) => sum - item.amountCents, 0),
);
function addEnvironment() {
  const product = deployProduct.value;
  const key = (product?.runtimeSpec?.editableEnvKeys || []).find(
    (value) => !deployEnvironment.value.some((item) => item.key === value),
  );
  if (key)
    deployEnvironment.value.push({
      key,
      value: product?.runtimeSpec?.env?.[key] || "",
    });
}
function removeEnvironment(index: number) {
  deployEnvironment.value.splice(index, 1);
}
function addDependency() {
  const candidate = products.value.find(
    (product) =>
      !deployDependencies.value.some((item) => item.productId === product.id),
  );
  if (candidate)
    deployDependencies.value.push({
      key: candidate.slug,
      productId: candidate.id,
      serviceSlug: candidate.slug,
      required: true,
    });
}
function removeDependency(index: number) {
  deployDependencies.value.splice(index, 1);
}
function chooseTopup(amount: number) {
  selectedTopup.value = amount;
  topup.value = amount;
}
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
    if (app.suspensionReason === "billing_insufficient")
      return "余额不足，已暂停";
    if (app.suspensionReason === "subscription_expired")
      return "套餐到期，已暂停";
    return "已暂停";
  }
  if (transientAppStates.has(app.status))
    return state(app.jobState || app.status);
  return state(app.status);
}
function appStateClass(app: App) {
  if (app.status === "running") return "active";
  if (transientAppStates.has(app.status) || app.status === "stopping")
    return "pending";
  if (app.status === "failed") return "danger";
  return "suspended";
}
const ledgerLabel = (value: string) =>
  ({
    topup: "充值入账",
    usage: "用量扣费",
    subscription: "历史套餐费用",
    refund: "退款",
    grant: "赠送额度",
    adjustment: "账户调账",
    checkin_reward: "签到奖励",
    reversal: "账本冲正",
  })[value] || value;
async function load() {
  const [
    m,
    p,
    a,
    b,
    u,
    l,
    statementData,
    creditData,
    o,
    n,
    notices,
    providers,
  ] = await Promise.all([
    api<any>("/me"),
    api<any>("/products"),
    api<any>("/apps"),
    api<any>("/billing/summary"),
    api<any>("/billing/usage"),
    api<{ entries: LedgerEntry[] }>("/billing/ledger"),
    api<{ bills: Bill[] }>("/billing/bills"),
    api<{
      availableCents: number;
      grants: CreditGrant[];
      consumptions: CreditConsumption[];
    }>("/billing/credits"),
    api<any>("/payments/orders"),
    api<{ announcements: Announcement[] }>("/announcements"),
    api<{ notifications: Notification[] }>("/notifications"),
    api<{ providers: PaymentProvider[] }>("/payments/providers"),
  ]);
  name.value = m.DisplayName;
  impersonation.value = {
    active: Boolean(m.Impersonating),
    readOnly: Boolean(m.ImpersonationReadOnly),
    actorName: m.ActorDisplayName || "管理员",
  };
  products.value = p.products;
  apps.value = a.apps;
  balance.value = b.balanceCents;
  usage.value = u.usage;
  ledger.value = l.entries;
  bills.value = statementData.bills;
  creditAvailable.value = creditData.availableCents;
  creditGrants.value = creditData.grants;
  creditConsumptions.value = creditData.consumptions;
  orders.value = o.orders;
  paymentProviders.value = providers.providers;
  announcements.value = n.announcements;
  notifications.value = notices.notifications;
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
  if (activeBill.value === bill.id) {
    activeBill.value = "";
    return;
  }
  activeBill.value = bill.id;
  if (!billItems.value[bill.id]) {
    const detail = await api<{ items: BillItem[] }>(
      `/billing/bills/${bill.id}`,
    );
    billItems.value[bill.id] = detail.items;
  }
}
async function exportBill(bill: Bill) {
  try {
    const response = await fetch(`/api/billing/bills/${bill.id}/export`, {
      headers: {
        Authorization: `Bearer ${localStorage.getItem("session_token") || ""}`,
      },
    });
    if (!response.ok) throw new Error(`导出失败（${response.status}）`);
    const url = URL.createObjectURL(await response.blob());
    const link = document.createElement("a");
    link.href = url;
    link.download = `cloudmeter-bill-${bill.periodStart.slice(0, 7)}.csv`;
    link.click();
    URL.revokeObjectURL(url);
  } catch (e) {
    error.value = (e as Error).message;
  }
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
    if (page.value === "checkin") await loadCheckin();
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
      : "该产品当前不可部署";
    return;
  }
  deployProduct.value = p;
  deploySlug.value = p.slug;
  deploySecrets.value = Object.fromEntries(
    (p.runtimeSpec?.secretKeys || []).map((key) => [key, ""]),
  );
  deployCPU.value = p.runtimeSpec?.cpuCores || 1;
  deployMemory.value = p.runtimeSpec?.memoryMiB || 512;
  deploySystemDisk.value = p.runtimeSpec?.systemDiskGiB || 5;
  deployVolumes.value = Object.fromEntries(
    (p.runtimeSpec?.volumes || []).map((volume) => [
      volume.name,
      volume.sizeGiB,
    ]),
  );
  deployCommand.value = (p.runtimeSpec?.command || []).join(" ");
  deployPort.value = p.routeSpec?.containerPort || 8080;
  deployEnvironment.value = (p.runtimeSpec?.editableEnvKeys || []).map(
    (key) => ({ key, value: p.runtimeSpec?.env?.[key] || "" }),
  );
  deployDependencies.value = (p.runtimeSpec?.dependencies || []).map(
    (dependency) => ({ ...dependency }),
  );
}
function closeDeploy() {
  deployProduct.value = null;
  deploySlug.value = "";
  deploySecrets.value = {};
  deployVolumes.value = {};
  deployCommand.value = "";
  deployEnvironment.value = [];
  deployDependencies.value = [];
}
function dependencyEndpoint(dependency: Dependency) {
  const target = products.value.find(
    (product) => product.id === dependency.productId,
  );
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
        resources: {
          cpuCores: deployCPU.value,
          memoryMiB: deployMemory.value,
          systemDiskGiB: deploySystemDisk.value,
          volumeSizes: deployVolumes.value,
          command: deployCommand.value.trim()
            ? deployCommand.value.trim().split(/\s+/)
            : [],
          environment: Object.fromEntries(
            deployEnvironment.value
              .filter((item) => item.key.trim())
              .map((item) => [item.key.trim(), item.value]),
          ),
          dependencies: deployDependencies.value,
        },
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
  if (
    !confirm(
      `确定停止 ${app.slug}？公网入口会立即下线，CPU 和内存运行计费随即停止；持久卷仍会保留并按实际用量计费。`,
    )
  )
    return;
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
  if (
    !confirm(
      `确定重新启动 ${app.slug}？平台会使用最后一次成功部署的配置重新创建运行实例。`,
    )
  )
    return;
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
  if (
    !secretAllowedKeys.value.includes(key) ||
    !secretValue.value ||
    secretValue.value.length > 65536
  ) {
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
    secretKey.value = result.allowedKeys.includes(key)
      ? key
      : result.allowedKeys[0] || "";
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
    if (!paymentProviders.value.some((v) => v.provider === "epay" && v.enabled))
      throw new Error("在线支付尚未开通，请联系管理员调整余额");
    const provider = "epay";
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
async function exitImpersonation() {
  const token = localStorage.getItem("admin_session_token");
  if (!token) {
    await logout();
    return;
  }
  try {
    await api("/impersonation", { method: "DELETE" });
    localStorage.setItem("session_token", token);
    localStorage.removeItem("admin_session_token");
    location.assign("/admin");
  } catch {
    // The global API handler clears both tokens on 401; restore the actor token so
    // the shared logout flow can still revoke the original administrator session.
    localStorage.setItem("admin_session_token", token);
    await logout();
  }
}
</script>

<template>
  <main class="workspace">
    <section v-if="impersonation.active" class="impersonation-banner">
      <div>
        <strong>{{ impersonation.actorName }} 正在查看此账户</strong
        ><span>{{
          impersonation.readOnly
            ? "只读模式，所有写操作已由后端阻止"
            : "代操作模式，所有写操作都会记录审计"
        }}</span>
      </div>
      <button class="secondary compact" @click="exitImpersonation">
        返回管理后台
      </button>
    </section>
    <header>
      <div>
        <p class="eyebrow">控制台</p>
        <h1>
          {{ page === "overview" ? `晚上好，${name || "..."}` : pageTitle }}
        </h1>
      </div>
    </header>
    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok">{{ message }}</p>
    <section v-if="page === 'overview'" class="metrics">
      <article>
        <small>钱包余额</small
        ><strong>¥ {{ (balance / 100).toFixed(2) }}</strong
        ><span>赠送额度 ¥ {{ (creditAvailable / 100).toFixed(2) }}</span>
      </article>
      <article>
        <small>运行中应用</small
        ><strong>{{ apps.filter((v) => v.status === "running").length }}</strong
        ><span>{{ apps.length }} 个应用实例</span>
      </article>
      <article>
        <small>可用模板</small><strong>{{ products.length }}</strong
        ><span>管理员已发布</span>
      </article>
    </section>
    <section
      v-if="
        page === 'overview' && (notifications.length || announcements.length)
      "
      class="announcement-feed"
    >
      <div class="section-heading">
        <div>
          <p class="eyebrow">平台通知</p>
          <h2>账户消息与公告</h2>
        </div>
        <span
          >{{
            notifications.filter((item) => !item.readAt).length
          }}
          条未读</span
        >
      </div>
      <p v-if="!products.length" class="quiet empty-copy">
        暂无可部署应用，请等待管理员创建、测试并发布应用。
      </p>
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
        <button
          v-if="!item.readAt"
          class="icon-action"
          title="标记已读"
          @click="markNotificationRead(item)"
        >
          <Check :size="17" />
        </button>
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
    <section v-if="page === 'billing'" class="billing-panel">
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
          <span
            :class="[
              'ledger-amount',
              entry.amountCents >= 0 ? 'credit' : 'debit',
            ]"
          >
            {{ entry.amountCents >= 0 ? "+" : "" }}¥{{
              (entry.amountCents / 100).toFixed(2)
            }}
          </span>
          <div>
            <strong>{{ ledgerLabel(entry.businessType) }}</strong>
            <small
              >{{ new Date(entry.createdAt).toLocaleString() }} · 余额 ¥{{
                (entry.balanceAfterCents / 100).toFixed(2)
              }}</small
            >
          </div>
        </article>
        <p v-if="!ledger.length" class="quiet empty-copy">还没有账本记录</p>
      </div>
      <div class="statement-list">
        <div class="ledger-heading">
          <strong>月度账单</strong><span>UTC 自然月</span>
        </div>
        <article v-for="bill in bills" :key="bill.id" class="statement-row">
          <button class="statement-summary" @click="toggleBill(bill)">
            <span
              ><strong>{{ bill.periodStart.slice(0, 7) }}</strong
              ><small
                >{{ bill.itemCount }} 项费用 ·
                {{ bill.status === "open" ? "进行中" : "已结算" }}</small
              ></span
            >
            <strong>¥{{ (bill.totalCents / 100).toFixed(2) }}</strong>
          </button>
          <button
            class="icon-action"
            title="导出 CSV"
            @click="exportBill(bill)"
          >
            <FileDown :size="17" />
          </button>
          <div v-if="activeBill === bill.id" class="statement-items">
            <div v-for="item in billItems[bill.id] || []" :key="item.id">
              <span
                ><strong>{{
                  item.kind === "subscription"
                    ? `历史套餐 · ${item.usageCode}`
                    : usageCodeLabel(item.usageCode)
                }}</strong
                ><small>{{
                  item.kind === "subscription"
                    ? `历史服务周期 ${new Date(item.windowStart).toLocaleDateString()} - ${new Date(item.windowEnd).toLocaleDateString()}`
                    : `${item.appSlug || "账户级"} · ${item.quantity} ${usageUnitLabel(item.unit)} · ${new Date(item.windowStart).toLocaleString()}`
                }}</small></span
              >
              <strong>¥{{ (item.amountCents / 100).toFixed(2) }}</strong>
            </div>
          </div>
        </article>
        <p v-if="!bills.length" class="quiet empty-copy">当前还没有月度账单</p>
      </div>
      <div class="credit-list">
        <div class="ledger-heading">
          <strong>赠送额度</strong
          ><span>可用 ¥{{ (creditAvailable / 100).toFixed(2) }}</span>
        </div>
        <article v-for="grant in creditGrants.slice(0, 6)" :key="grant.id">
          <div>
            <strong>{{ grant.note || "平台赠送" }}</strong
            ><small
              >{{ grant.businessRef }} ·
              {{
                grant.expiresAt
                  ? "到期 " + new Date(grant.expiresAt).toLocaleString()
                  : "长期有效"
              }}</small
            >
          </div>
          <span :class="['status-pill', grant.active ? 'active' : 'suspended']"
            >¥{{ (grant.remainingCents / 100).toFixed(2) }} / ¥{{
              (grant.amountCents / 100).toFixed(2)
            }}</span
          >
        </article>
        <div v-if="creditConsumptions.length" class="credit-consumptions">
          <strong>最近抵扣</strong
          ><small v-for="item in creditConsumptions.slice(0, 5)" :key="item.id"
            >{{ usageCodeLabel(item.usageCode) }} · -¥{{
              (item.amountCents / 100).toFixed(2)
            }}
            · {{ new Date(item.windowStart).toLocaleString() }}</small
          >
        </div>
        <p v-if="!creditGrants.length" class="quiet empty-copy">
          当前没有赠送额度
        </p>
      </div>
    </section>
    <section v-if="page === 'apps' && apps.length" class="product-list">
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
          ><small>{{ app.productSlug }} · {{ appState(app) }}</small>
        </div>
        <div class="product-controls">
          <span class="status-pill" :class="appStateClass(app)">{{
            appState(app)
          }}</span
          ><button
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
            <RotateCcw :size="17" /></button
          ><button
            v-if="app.status === 'running'"
            class="icon-action stop-action"
            title="停止应用"
            :disabled="writeLocked || busy === app.id"
            @click="stopApp(app)"
          >
            <CircleStop :size="17" /></button
          ><button
            v-if="
              app.status === 'stopped' ||
              (app.status === 'failed' && Boolean(app.lastSuccessfulReleaseId))
            "
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
    <section v-if="page === 'usage'" class="product-list">
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
          <strong>{{ usageCodeLabel(item.usageCode) }}</strong
          ><small
            >{{ item.quantity }} {{ usageUnitLabel(item.unit) }} ·
            {{ new Date(item.windowStart).toLocaleString() }}</small
          >
        </div>
        <span
          class="status-pill"
          :class="
            item.billingDisposition === 'unpriced'
              ? 'pending'
              : item.sealedAt
                ? 'active'
                : 'suspended'
          "
          >{{
            item.billingDisposition === "unpriced"
              ? "未配置价格"
              : item.sealedAt
                ? "已结算"
                : "待结算"
          }}<template v-if="item.amountCents != null">
            · {{ item.amountCents }} 分</template
          ></span
        >
      </article>
      <p v-if="!usage.length" class="quiet empty-copy">暂无用量记录</p>
    </section>
    <section v-if="page === 'deploy'" class="product-list">
      <div class="section-heading">
        <div>
          <p class="eyebrow">应用模板</p>
          <h2>产品目录</h2>
        </div>
        <div class="deploy-catalog-actions">
          <span
            >{{
              products.filter((value) => value.deployable).length
            }}
            个可部署版本</span
          >
        </div>
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
              · 缺少 {{ product.missingDependencies.join("、") }}</template
            ><template v-else-if="!product.deployable">
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
    <section v-if="page === 'recharge'" class="recharge-page">
      <div class="recharge-metrics">
        <article>
          <span class="product-icon"><CreditCard :size="20" /></span>
          <div>
            <small>当前余额</small
            ><strong>¥{{ (balance / 100).toFixed(2) }}</strong>
            <p>可用于全部按量费用</p>
          </div>
        </article>
        <article>
          <span class="product-icon"><Receipt :size="20" /></span>
          <div>
            <small>累计消费</small
            ><strong>¥{{ (totalSpend / 100).toFixed(2) }}</strong>
            <p>历史钱包支出</p>
          </div>
        </article>
        <article>
          <span class="product-icon"><FileDown :size="20" /></span>
          <div>
            <small>充值订单</small><strong>{{ orders.length }}</strong>
            <p>全部充值记录</p>
          </div>
        </article>
      </div>
      <section class="recharge-panel">
        <div class="section-heading">
          <div>
            <p class="eyebrow">添加资金</p>
            <h2>选择充值金额</h2>
          </div>
          <span>{{
            paymentProviders.some(
              (item) => item.provider === "epay" && item.enabled,
            )
              ? "在线支付"
              : "暂未开通"
          }}</span>
        </div>
        <form @submit.prevent="createOrder">
          <div class="topup-options">
            <button
              v-for="amount in topupOptions"
              :key="amount"
              type="button"
              :class="{ selected: selectedTopup === amount }"
              @click="chooseTopup(amount)"
            >
              <strong>¥{{ amount }}</strong
              ><small>充值 {{ amount }} 元</small>
            </button>
          </div>
          <label
            >自定义金额（元）<input
              v-model.number="topup"
              type="number"
              min="1"
              max="1000000"
              step="0.01"
              @input="selectedTopup = 0"
              required
          /></label>
          <div class="payment-choice">
            <strong>付款方式</strong>
            <div>
              <span
                :class="[
                  'payment-method',
                  {
                    active: paymentProviders.some(
                      (item) => item.provider === 'epay' && item.enabled,
                    ),
                  },
                ]"
                ><CreditCard :size="19" />在线支付（支付宝）</span
              >
            </div>
            <p
              v-if="
                !paymentProviders.some(
                  (item) => item.provider === 'epay' && item.enabled,
                )
              "
              class="quiet"
            >
              在线支付未开通，余额由管理员在用户编辑栏调整。
            </p>
          </div>
          <div class="recharge-submit">
            <span
              >待支付金额
              <strong>¥{{ Number(topup || 0).toFixed(2) }}</strong></span
            ><button
              class="primary"
              :disabled="
                writeLocked ||
                topup <= 0 ||
                !paymentProviders.some(
                  (item) => item.provider === 'epay' && item.enabled,
                )
              "
            >
              <CreditCard :size="18" />确认充值
            </button>
          </div>
        </form>
        <div v-if="orders.length" class="order-list recharge-orders">
          <div class="ledger-heading">
            <strong>订单历史</strong
            ><span>最近 {{ Math.min(orders.length, 8) }} 条</span>
          </div>
          <article v-for="order in orders.slice(0, 8)" :key="order.id">
            <span>¥ {{ (order.amountCents / 100).toFixed(2) }}</span
            ><small
              >{{
                order.status === "paid"
                  ? "已入账"
                  : order.status === "refunded"
                    ? "已退款"
                    : "处理中"
              }}
              · {{ new Date(order.createdAt).toLocaleString() }}</small
            >
          </article>
        </div>
      </section>
    </section>
    <section v-if="page === 'checkin'" class="checkin-page">
      <div class="checkin-hero">
        <span class="checkin-icon"><CalendarCheck :size="28" /></span>
        <div>
          <p class="eyebrow">北京时间 · 每日一次</p>
          <h2>签到领取余额奖励</h2>
          <p>
            每天获得 ¥{{
              ((checkin?.minRewardCents || 0) / 100).toFixed(2)
            }}–¥{{
              ((checkin?.maxRewardCents || 0) / 100).toFixed(2)
            }}，直接进入钱包余额。
          </p>
        </div>
        <button
          class="primary"
          :disabled="
            writeLocked ||
            busy === 'checkin' ||
            !checkin?.enabled ||
            checkin?.checkedInToday
          "
          @click="doCheckin"
        >
          <Gift :size="18" />{{
            !checkin?.enabled
              ? "签到已暂停"
              : checkin?.checkedInToday
                ? "今日已签到"
                : "立即签到"
          }}
        </button>
      </div>
      <div class="checkin-metrics">
        <article>
          <small>累计签到</small
          ><strong>{{ checkin?.totalCheckins || 0 }}<em> 次</em></strong>
        </article>
        <article>
          <small>本月获得</small
          ><strong
            >¥{{ ((checkin?.monthRewardCents || 0) / 100).toFixed(2) }}</strong
          >
        </article>
        <article>
          <small>累计获得</small
          ><strong
            >¥{{ ((checkin?.totalRewardCents || 0) / 100).toFixed(2) }}</strong
          >
        </article>
      </div>
      <div class="checkin-calendar">
        <header>
          <button
            class="icon-action"
            title="上个月"
            @click="changeCheckinMonth(-1)"
          >
            <ChevronLeft :size="18" />
          </button>
          <div>
            <strong>{{ checkinMonth.replace("-", " 年 ") }} 月</strong
            ><small>黑色日期表示已签到</small>
          </div>
          <button
            class="icon-action"
            title="下个月"
            @click="changeCheckinMonth(1)"
          >
            <ChevronRight :size="18" />
          </button>
        </header>
        <div class="calendar-week">
          <span
            v-for="label in ['一', '二', '三', '四', '五', '六', '日']"
            :key="label"
            >{{ label }}</span
          >
        </div>
        <div class="calendar-grid">
          <span
            v-for="(day, index) in calendarDays"
            :key="index"
            :class="{
              blank: !day,
              checked: day && isChecked(day),
              today: day && isToday(day),
            }"
            ><template v-if="day"
              >{{ day }}<Check v-if="isChecked(day)" :size="12" /></template
          ></span>
        </div>
      </div>
    </section>
  </main>
  <Transition name="modal-pop"
    ><div v-if="deployProduct" class="modal-backdrop" @click.self="closeDeploy">
      <section class="secret-dialog deploy-dialog">
        <header>
          <div>
            <p class="eyebrow">部署应用</p>
            <h2>{{ deployProduct.name }}</h2>
          </div>
          <button class="icon-action" title="关闭" @click="closeDeploy">
            <X :size="18" />
          </button>
        </header>
        <form @submit.prevent="deploy">
          <label
            >应用标识<input
              v-model="deploySlug"
              required
              pattern="[a-z0-9][a-z0-9-]{0,62}"
              autocomplete="off"
          /></label>
          <div class="deploy-resource-grid">
            <label
              >CPU 核心<input
                v-model.number="deployCPU"
                type="number"
                :min="deployProduct.runtimeSpec?.cpuCores || 0.1"
                max="64"
                step="0.1"
                required
              /><small
                >最低 {{ deployProduct.runtimeSpec?.cpuCores || 1 }} 核</small
              ></label
            >
            <label
              >内存 MiB<input
                v-model.number="deployMemory"
                type="number"
                :min="deployProduct.runtimeSpec?.memoryMiB || 64"
                max="262144"
                step="64"
                required
              /><small
                >最低
                {{ deployProduct.runtimeSpec?.memoryMiB || 512 }} MiB</small
              ></label
            >
            <label
              >系统盘 GiB<input
                v-model.number="deploySystemDisk"
                type="number"
                :min="deployProduct.runtimeSpec?.systemDiskGiB || 1"
                max="1024"
                step="1"
                required
              /><small
                >最低
                {{ deployProduct.runtimeSpec?.systemDiskGiB || 5 }} GiB</small
              ></label
            >
            <label
              >容器内网端口<input :value="deployPort" readonly /><small
                >管理员按镜像监听端口固定，仅 Docker
                内网可达；公网经统一反向代理访问</small
              ></label
            >
          </div>
          <label
            v-for="volume in deployProduct.runtimeSpec?.volumes || []"
            :key="volume.name"
            >{{ volume.name }} 数据卷 GiB<input
              v-model.number="deployVolumes[volume.name]"
              type="number"
              :min="volume.sizeGiB"
              max="16384"
              step="1"
              required
            /><small
              >挂载 {{ volume.mountPath }}，最低 {{ volume.sizeGiB }} GiB</small
            ></label
          >
          <label
            >启动命令<input
              v-model="deployCommand"
              placeholder="留空使用镜像默认命令"
              autocomplete="off"
            /><small
              >按空格拆分参数；复杂参数建议由镜像入口脚本处理</small
            ></label
          >
          <div class="deploy-custom-list">
            <div class="deploy-secret-heading">
              <Settings2 :size="17" />
              <div>
                <strong>环境变量</strong
                ><small
                  >仅管理员标记为可编辑的字段可提交；平台不会擅自注入
                  PORT</small
                >
              </div>
              <button
                type="button"
                class="secondary compact"
                :disabled="
                  deployEnvironment.length >=
                  (deployProduct.runtimeSpec?.editableEnvKeys?.length || 0)
                "
                @click="addEnvironment"
              >
                <Plus :size="15" />添加
              </button>
            </div>
            <div
              v-for="(item, index) in deployEnvironment"
              :key="index"
              class="deploy-key-value"
            >
              <select v-model="item.key">
                <option
                  v-for="key in deployProduct.runtimeSpec?.editableEnvKeys ||
                  []"
                  :key="key"
                  :value="key"
                >
                  {{ key }}
                </option></select
              ><input v-model="item.value" placeholder="值" /><button
                type="button"
                class="icon-action"
                title="删除"
                @click="removeEnvironment(index)"
              >
                <X :size="16" />
              </button>
            </div>
          </div>
          <div class="deploy-dependencies">
            <div class="deploy-secret-heading">
              <Link2 :size="17" />
              <div>
                <strong>内部依赖</strong
                ><small>选择同一账户内需要连接的应用产品</small>
              </div>
              <button
                type="button"
                class="secondary compact"
                @click="addDependency"
              >
                <Plus :size="15" />添加
              </button>
            </div>
            <article
              v-for="(dependency, index) in deployDependencies"
              :key="dependency.key + index"
            >
              <div>
                <input v-model="dependency.key" placeholder="依赖标识" /><select
                  v-model="dependency.productId"
                >
                  <option
                    v-for="product in products"
                    :key="product.id"
                    :value="product.id"
                  >
                    {{ product.name }}
                  </option></select
                ><input v-model="dependency.serviceSlug" placeholder="服务名" />
              </div>
              <code>{{ dependencyEndpoint(dependency) }}</code
              ><button
                type="button"
                class="icon-action"
                title="删除依赖"
                @click="removeDependency(index)"
              >
                <X :size="16" />
              </button>
            </article>
          </div>
          <template v-if="deployProduct.runtimeSpec?.secretKeys?.length">
            <div class="deploy-secret-heading">
              <KeyRound :size="17" />
              <div>
                <strong>部署 Secret</strong
                ><small>加密保存，提交后不会再次显示</small>
              </div>
            </div>
            <label
              v-for="key in deployProduct.runtimeSpec.secretKeys"
              :key="key"
              >{{ key
              }}<input
                v-model="deploySecrets[key]"
                type="password"
                required
                autocomplete="new-password"
            /></label>
          </template>
          <p v-else class="quiet">此模板不需要额外 Secret。</p>
          <div class="deploy-dialog-actions">
            <button
              type="button"
              class="secondary compact"
              @click="closeDeploy"
            >
              取消</button
            ><button
              class="primary compact"
              :disabled="writeLocked || busy === 'deploy'"
            >
              <Rocket :size="16" />创建部署
            </button>
          </div>
        </form>
      </section>
    </div></Transition
  >
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
            <option v-for="key in secretAllowedKeys" :key="key" :value="key">
              {{ secretKeyLabel(key) }}
            </option>
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
      <p v-else class="secret-empty quiet">
        此产品的已发布版本没有声明可配置的 Secret。
      </p>
    </section>
  </div>
</template>
