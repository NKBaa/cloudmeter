<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
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
  CircleAlert,
  CircleHelp,
  ChevronDown,
  ExternalLink,
  FileDown,
  FileText,
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
  Trash2,
  X,
} from "@lucide/vue";
import { api, logout, openApp } from "../api";
import BrandMark from "../components/BrandMark.vue";
import { usageCodeLabel, usageUnitLabel } from "../billing-labels";

type Dependency = {
  key: string;
  productId: string;
  serviceSlug: string;
  required: boolean;
};
type Volume = { name: string; mountPath: string; sizeGiB: number };
type SecretOption = { key: string; description?: string; editable?: boolean };
type EditableOptions = { cpu?: boolean; memory?: boolean; dataVolume?: boolean; command?: boolean; dependencies?: boolean; environment?: boolean };
type Product = {
  id: string;
  slug: string;
  name: string;
  iconUrl?: string;
  versionId: string;
  version: number;
  versionLabel?: string;
  deployable: boolean;
  missingDependencies?: string[];
  runtimeSpec?: {
    cpuCores?: number;
    memoryMiB?: number;
    command?: string[];
    env?: Record<string, string>;
    editableEnvKeys?: string[];
    envDescriptions?: Record<string, string>;
    secretKeys?: string[];
    secretDescriptions?: Record<string, string>;
    editableSecretKeys?: string[];
    dependencies?: Dependency[];
    volumes?: Volume[];
    dataVolumeGiB?: number;
    editableOptions?: EditableOptions;
  };
  routeSpec?: {
    containerPort?: number;
    portMapping?: { available?: boolean };
  };
};
type ProductGroup = Omit<Product, "versionId" | "version" | "versionLabel" | "deployable" | "missingDependencies" | "runtimeSpec" | "routeSpec"> & {
  versions: Array<Pick<Product, "versionId" | "version" | "versionLabel" | "deployable" | "missingDependencies" | "runtimeSpec" | "routeSpec">>;
};
type AppConfiguration = {
  app: { id: string; slug: string; status: string; productSlug: string };
  current: { versionId: string; runtimeSpec: NonNullable<Product["runtimeSpec"]>; routeSpec: NonNullable<Product["routeSpec"]> };
  target: {
    productId: string;
    productSlug: string;
    name: string;
    iconUrl?: string;
    versionId: string;
    version: number;
    runtimeSpec: NonNullable<Product["runtimeSpec"]>;
    routeSpec: NonNullable<Product["routeSpec"]>;
  };
  configuredSecretKeys: string[];
};
type App = {
  id: string;
  instanceId?: string;
  slug: string;
  status: string;
  productSlug: string;
  lastSuccessfulReleaseId?: string;
  suspensionReason?: string;
  jobState?: string;
  previousReleaseId?: string;
  publicPath?: string;
  hostPort?: number;
  portMappingEnabled?: boolean;
  cpuCores?: number;
  memoryMiB?: number;
  cpuUsageCores?: number;
  memoryUsageMiB?: number;
  metricsSampledAt?: string;
  estimatedMonthlyCents?: number;
  estimateComplete?: boolean;
  jobLastError?: string;
};
type RuntimeMetric = {
  appId: string;
  cpuUsageCores: number;
  memoryUsageMiB: number;
  sampledAt: string;
};
type DeploymentEvent = { id?: number; fromState?: string; toState?: string; message?: string; createdAt?: string };
type DeploymentJob = { id: string; state: string; attempts: number; lastError?: string | null; createdAt: string; updatedAt: string; events: DeploymentEvent[] };
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
type PaymentMethod = { name: string; type: string; minAmountCents: number; enabled: boolean };
type PaymentProvider = { provider: string; enabled: boolean; paymentMethods?: PaymentMethod[]; amountOptions?: number[] };
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
type AppSecretResponse = { secrets: AppSecret[]; allowedKeys: string[]; editableKeys?: string[]; options?: SecretOption[] };
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
const router = useRouter();
const page = computed(() => props.page || "overview");
const locationHost = window.location.hostname;
const pageTitle = computed(  () =>
    ({
      overview: "概览",
      deploy: "部署应用",
      apps: "我的应用",
      billing: "余额与账单",
      recharge: "账户充值",
      usage: "用量明细",
      checkin: "每日签到",
    })[page.value],
);
const products = ref<Product[]>([]),
  productCatalog = ref<ProductGroup[]>([]),
  pickedProduct = ref<ProductGroup | null>(null),
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
  topup = ref(10),
  selectedTopup = ref(10),
  busy = ref("");
const appsLoading = ref(false);
const productsLoading = ref(false);
const selectedPaymentType = ref("");
const activeBill = ref<string>(""),
  billItems = ref<Record<string, BillItem[]>>({});
const impersonation = ref({ active: false, readOnly: true, actorName: "" });
const faqs = ref<{ id: string; question: string; answer: string }[]>([]);
const openFaq = ref<string>("");
const logApp = ref<App | null>(null);
const logData = ref<{ logs: string; status: string; lastError?: string; sampledAt?: string }>({ logs: "", status: "" });
const logBusy = ref(false);
const writeLocked = computed(
  () => impersonation.value.active && impersonation.value.readOnly,
);
const secretApp = ref<App | null>(null),
  secretItems = ref<AppSecret[]>([]),
  secretAllowedKeys = ref<string[]>([]),
  secretOptions = ref<SecretOption[]>([]),
  secretEditableKeys = ref<string[]>([]),
  secretKey = ref(""),
  secretValue = ref(""),
  secretBusy = ref(false);
const deploymentApp = ref<App | null>(null);
const deploymentJobs = ref<DeploymentJob[]>([]);
const deploymentBusy = ref(false);
const deployProduct = ref<Product | null>(null),
  deployMode = ref<"create" | "update">("create"),
  editingApp = ref<App | null>(null),
  deployConfigurationLoading = ref(false),
  deployConfiguredSecretKeys = ref<string[]>([]),
  deploySlug = ref(""),
  deploySecrets = ref<Record<string, string>>({}),
  deployCPU = ref(1),
  deployMemory = ref(512),
  deployDataVolumeGiB = ref(0);
const deployVolumeFloorGiB = ref(0);
const deployCommand = ref("");
const deployPort = ref(8080);
const deployPortMapping = ref(false);
const deployPortMappingAvailable = computed(() => deployProduct.value?.routeSpec?.portMapping?.available === true);
const deployEnvironment = ref<{ key: string; value: string }[]>([]);
const deployDependencies = ref<Dependency[]>([]);
const missingDeploySecretKeys = computed(() =>
  (deployProduct.value?.runtimeSpec?.secretKeys || []).filter(
    (key) => !deployConfiguredSecretKeys.value.includes(key) && !String(deploySecrets.value[key] || '').trim(),
  ),
);
const deploySecretOptions = computed<SecretOption[]>(() => {
  const runtime = deployProduct.value?.runtimeSpec;
  const keys = runtime?.secretKeys || [];
  const editable = runtime?.editableSecretKeys;
  return keys.map((key) => ({
    key,
    description: runtime?.secretDescriptions?.[key] || "",
    // Legacy versions did not persist editableSecretKeys, therefore retain
    // the backwards-compatible default of editable.
    editable: editable ? editable.includes(key) : true,
  }));
});
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
const epayProvider = computed(() => paymentProviders.value.find((item) => item.provider === "epay"));
const topupOptions = computed(() => epayProvider.value?.amountOptions?.length ? epayProvider.value.amountOptions : [10,20,50,100,200,300,400,500]);
const paymentMethods = computed(() => (epayProvider.value?.paymentMethods || []).filter((item) => item.enabled));
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
function logStatusLabel(status: string) {
  return (
    {
      queued: "等待拉取",
      running: "拉取中",
      succeeded: "已拉取",
      cached: "已缓存",
      failed: "拉取失败",
    }[status] || status || "未知"
  );
}
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
const instanceCountBySlug = computed(() => {
  const counts = new Map<string, number>();
  for (const app of apps.value)
    counts.set(app.slug, (counts.get(app.slug) || 0) + 1);
  return counts;
});
function instanceLabel(app: App) {
  if ((instanceCountBySlug.value.get(app.slug) || 1) <= 1) return app.slug;
  const ordered = apps.value.filter((item) => item.slug === app.slug);
  return `${app.slug} #${ordered.indexOf(app) + 1}`;
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
  productsLoading.value = true;
  try {
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
      faqData,
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
      api<{ faqs: { id: string; question: string; answer: string }[] }>("/faqs"),
    ]);
    name.value = m.DisplayName;
  impersonation.value = {
    active: Boolean(m.Impersonating),
    readOnly: Boolean(m.ImpersonationReadOnly),
    actorName: m.ActorDisplayName || "管理员",
  };
  faqs.value = faqData.faqs || [];
  products.value = catalogToFlat(p.products);
  productCatalog.value = p.products;
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
  if (!topupOptions.value.includes(selectedTopup.value)) chooseTopup(topupOptions.value[0] || 10);
  if (!paymentMethods.value.some((item) => item.type === selectedPaymentType.value)) selectedPaymentType.value = paymentMethods.value[0]?.type || "";
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
  } finally {
    productsLoading.value = false;
  }
}
let appsRefreshTimer: number | undefined;
let metricsRefreshTimer: number | undefined;
let appsRefreshInFlight = false;
let metricsRefreshInFlight = false;
const metricsClock = ref(Date.now());
async function refreshApps(reportError = false) {
  if (appsRefreshInFlight) return;
  appsRefreshInFlight = true;
  appsLoading.value = true;
  try {
    apps.value = (await api<{ apps: App[] }>("/apps")).apps;
  } catch (e) {
    if (reportError) error.value = (e as Error).message;
  } finally {
    appsRefreshInFlight = false;
    appsLoading.value = false;
  }
}
async function refreshRuntimeMetrics(reportError = false) {
  if (
    metricsRefreshInFlight ||
    (page.value !== "overview" && page.value !== "apps")
  )
    return;
  metricsRefreshInFlight = true;
  try {
    const data = await api<{ metrics: RuntimeMetric[] }>(
      "/apps/runtime-metrics",
    );
    const current = new Map(data.metrics.map((item) => [item.appId, item]));
    apps.value = apps.value.map((app) => {
      const metric = current.get(app.id);
      return metric
        ? {
            ...app,
            cpuUsageCores: metric.cpuUsageCores,
            memoryUsageMiB: metric.memoryUsageMiB,
            metricsSampledAt: metric.sampledAt,
          }
        : {
            ...app,
            cpuUsageCores: undefined,
            memoryUsageMiB: undefined,
            metricsSampledAt: undefined,
          };
    });
  } catch (e) {
    if (reportError) error.value = (e as Error).message;
  } finally {
    metricsClock.value = Date.now();
    metricsRefreshInFlight = false;
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
  const flash = sessionStorage.getItem("cloudmeter_flash");
  if (flash) {
    message.value = flash;
    sessionStorage.removeItem("cloudmeter_flash");
  }
  try {
    await load();
    await refreshRuntimeMetrics();
    if (page.value === "checkin") await loadCheckin();
  } catch (e) {
    error.value = (e as Error).message;
  }
  appsRefreshTimer = window.setInterval(() => {
    if (!document.hidden) void refreshApps();
  }, 5000);
  metricsRefreshTimer = window.setInterval(() => {
    if (!document.hidden) void refreshRuntimeMetrics();
  }, 3000);
});
 onBeforeUnmount(() => {
   if (appsRefreshTimer !== undefined) window.clearInterval(appsRefreshTimer);
   if (metricsRefreshTimer !== undefined) window.clearInterval(metricsRefreshTimer);
   if (logTimer !== undefined) window.clearInterval(logTimer);
 });
function catalogToFlat(catalog: ProductGroup[]): Product[] {
  const flat: Product[] = [];
  for (const group of catalog) {
    for (const version of group.versions) {
      flat.push({
        ...version,
        id: group.id,
        slug: group.slug,
        name: group.name,
        iconUrl: group.iconUrl,
      } as Product);
    }
  }
  return flat;
}
function openVersionPicker(group: ProductGroup) {
  pickedProduct.value = group;
}
function pickVersion(group: ProductGroup, version: ProductGroup["versions"][number]) {
  pickedProduct.value = null;
  openDeploy({ ...version, id: group.id, slug: group.slug, name: group.name, iconUrl: group.iconUrl } as Product);
}
function openDeploy(p: Product) {
  if (!p.deployable) {
    error.value = p.missingDependencies?.length
      ? `请先部署并运行依赖服务：${p.missingDependencies.join("、")}`
      : "该产品当前不可部署";
    return;
  }
  deployMode.value = "create";
  editingApp.value = null;
  deployConfiguredSecretKeys.value = [];
  deployProduct.value = p;
  deploySlug.value = p.slug;
  deploySecrets.value = Object.fromEntries(
    (p.runtimeSpec?.secretKeys || []).map((key) => [key, ""]),
  );
  deployCPU.value = p.runtimeSpec?.cpuCores || 1;
  deployMemory.value = p.runtimeSpec?.memoryMiB || 512;
  deployDataVolumeGiB.value = p.runtimeSpec?.dataVolumeGiB || Math.max(0, ...(p.runtimeSpec?.volumes || []).map((volume) => volume.sizeGiB));
  deployVolumeFloorGiB.value = deployDataVolumeGiB.value;
  deployCommand.value = (p.runtimeSpec?.command || []).join(" ");
  deployPort.value = p.routeSpec?.containerPort || 8080;
  deployPortMapping.value = false;
  deployEnvironment.value = (p.runtimeSpec?.editableEnvKeys || []).map(
    (key) => ({ key, value: p.runtimeSpec?.env?.[key] || "" }),
  );
  deployDependencies.value = (p.runtimeSpec?.dependencies || []).map(
    (dependency) => ({ ...dependency }),
  );
}
async function openEditApp(app: App) {
  try {
    deployConfigurationLoading.value = true;
    busy.value = app.id;
    error.value = "";
    const configuration = await api<AppConfiguration>(`/apps/${app.id}/configuration`);
    const target: Product = {
      id: configuration.target.productId,
      slug: configuration.target.productSlug,
      name: configuration.target.name,
      iconUrl: configuration.target.iconUrl,
      versionId: configuration.target.versionId,
      version: configuration.target.version,
      deployable: true,
      runtimeSpec: configuration.target.runtimeSpec,
      routeSpec: configuration.target.routeSpec,
    };
    const current = configuration.current.runtimeSpec || {};
    const minimumCPU = target.runtimeSpec?.cpuCores || 1;
    const minimumMemory = target.runtimeSpec?.memoryMiB || 512;
    const minimumVolume = target.runtimeSpec?.dataVolumeGiB
      || Math.max(0, ...(target.runtimeSpec?.volumes || []).map((volume) => volume.sizeGiB));
    const currentVolume = current.dataVolumeGiB
      || Math.max(0, ...(current.volumes || []).map((volume) => volume.sizeGiB));
    deployMode.value = "update";
    editingApp.value = app;
    deployConfiguredSecretKeys.value = configuration.configuredSecretKeys || [];
    deployProduct.value = target;
    deploySlug.value = app.slug;
    deploySecrets.value = Object.fromEntries(
      (target.runtimeSpec?.secretKeys || []).map((key) => [key, ""]),
    );
    deployCPU.value = Math.max(Number(current.cpuCores || 0), minimumCPU);
    deployMemory.value = Math.max(Number(current.memoryMiB || 0), minimumMemory);
    deployDataVolumeGiB.value = Math.max(currentVolume, minimumVolume);
    deployVolumeFloorGiB.value = deployDataVolumeGiB.value;
    deployPortMapping.value = app.portMappingEnabled === true;
    deployCommand.value = (current.command || target.runtimeSpec?.command || []).join(" " );
    deployPort.value = target.routeSpec?.containerPort || 8080;
    deployEnvironment.value = (target.runtimeSpec?.editableEnvKeys || []).map((key) => ({
      key,
      value: current.env?.[key] ?? target.runtimeSpec?.env?.[key] ?? "",
    }));
    deployDependencies.value = (
      target.runtimeSpec?.editableOptions?.dependencies
        ? current.dependencies || target.runtimeSpec?.dependencies || []
        : target.runtimeSpec?.dependencies || []
    ).map((dependency) => ({ ...dependency }));
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    deployConfigurationLoading.value = false;
    busy.value = "";
  }
}
function closeDeploy() {
  deployProduct.value = null;
  deployMode.value = "create";
  editingApp.value = null;
  deployConfiguredSecretKeys.value = [];
  deploySlug.value = "";
  deploySecrets.value = {};
  deployDataVolumeGiB.value = 0;
  deployVolumeFloorGiB.value = 0;
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
  if (deployMode.value === "update" && missingDeploySecretKeys.value.length) {
    error.value = `新版本需要先配置 Secret：${missingDeploySecretKeys.value.join("、")}`;
    return;
  }
  try {
    busy.value = "deploy";
    const resources = {
      cpuCores: p.runtimeSpec?.editableOptions?.cpu !== false ? deployCPU.value : undefined,
      memoryMiB: p.runtimeSpec?.editableOptions?.memory !== false ? deployMemory.value : undefined,
      dataVolumeGiB: p.runtimeSpec?.editableOptions?.dataVolume !== false && (p.runtimeSpec?.volumes?.length || 0) ? deployDataVolumeGiB.value : undefined,
      command: p.runtimeSpec?.editableOptions?.command
        ? (deployCommand.value.trim() ? deployCommand.value.trim().split(/\s+/) : [])
        : undefined,
      environment: Object.fromEntries(
        deployEnvironment.value
          .filter((item) => item.key.trim())
          .map((item) => [item.key.trim(), item.value]),
      ),
      dependencies: p.runtimeSpec?.editableOptions?.dependencies ? deployDependencies.value : undefined,
      portMappingEnabled: deployPortMappingAvailable.value ? deployPortMapping.value : undefined,
    };
    const changedSecrets = Object.fromEntries(
      Object.entries(deploySecrets.value).filter(([, value]) => String(value).trim() !== ""),
    );
    if (deployMode.value === "update" && editingApp.value) {
      await api(`/apps/${editingApp.value.id}/releases`, {
        method: "POST",
        body: JSON.stringify({
          versionId: p.versionId,
          idempotencyKey: crypto.randomUUID(),
          resources,
          secrets: changedSecrets,
        }),
      });
      message.value = `${editingApp.value.slug} 的配置更新任务已创建`;
      closeDeploy();
      await refreshApps(true);
    } else {
      const created = await api<{ slug?: string }>("/apps", {
        method: "POST",
        body: JSON.stringify({
        productId: p.id,
        versionId: p.versionId,
        slug: deploySlug.value.trim(),
        idempotencyKey: crypto.randomUUID(),
        secrets: deploySecrets.value,
        resources,
        }),
      });
      sessionStorage.setItem(
        "cloudmeter_flash",
        `部署任务已创建${created.slug && created.slug !== deploySlug.value.trim() ? `，应用标识已自动调整为 ${created.slug}` : ""}，已转到我的应用查看运行状态`,
      );
      closeDeploy();
      await router.push("/console/apps");
    }
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
function deploymentErrorHint(value?: string) {
  const text = String(value || '');
  if (/no such image/i.test(text)) return '镜像不存在或版本号不可用，请检查管理员发布的镜像地址与 Tag，并确认镜像源可访问。';
  if (/pull access denied|unauthorized|authentication required/i.test(text)) return '镜像仓库拒绝访问，请让管理员检查 Registry 地址、凭据或镜像是否为私有仓库。';
  if (/timeout|timed out|deadline exceeded/i.test(text)) return '拉取镜像超时，请检查镜像加速、代理、DNS 和网络连通性。';
  if (/connection refused|port is already allocated/i.test(text)) return '容器启动端口或内部服务不可用，请让管理员检查内网端口与健康检查配置。';
  return '部署任务未完成，请查看下方原始错误与事件时间线，或联系管理员。';
}
function deploymentErrorText(value?: string) {
  const text = String(value || '').trim();
  return text.length > 1200 ? text.slice(0, 1200) + '…' : text;
}
async function openDeploymentDetails(app: App) {
  deploymentApp.value = app;
  deploymentJobs.value = [];
  deploymentBusy.value = true;
  try {
    const result = await api<{ deployments: DeploymentJob[] }>('/apps/' + app.id + '/deployments');
    deploymentJobs.value = result.deployments || [];
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    deploymentBusy.value = false;
  }
}
async function visitApp(app: App) {
  try {
    await openApp(app.id);
  } catch (e) {
    error.value = (e as Error).message;
  }
}
function metricAgeSeconds(app: App) {
  if (!app.metricsSampledAt) return Number.POSITIVE_INFINITY;
  const sampledAt = new Date(app.metricsSampledAt).getTime();
  if (!Number.isFinite(sampledAt)) return Number.POSITIVE_INFINITY;
  return Math.max(0, Math.floor((metricsClock.value - sampledAt) / 1000));
}
function metricsAvailable(app: App) {
  return app.status === "running" && metricAgeSeconds(app) <= 15;
}
function metricFreshness(app: App) {
  if (app.status !== "running") return "应用未运行";
  if (!metricsAvailable(app)) return "等待实时采样";
  const seconds = metricAgeSeconds(app);
  return seconds < 5 ? "实时 · 刚刚更新" : `实时 · ${seconds} 秒前`;
}
async function deleteApp(app: App) {
  if (writeLocked.value || !window.confirm(`删除应用“${app.slug}”？运行容器、路由和持久数据卷会被回收，账单和发布审计历史仍会保留。此操作不可恢复。`)) return;
  try {
    busy.value = app.id;
    await api(`/apps/${app.id}`, { method: "DELETE" });
    message.value = "应用已进入删除清理队列";
    await load();
  } catch (e) { error.value = (e as Error).message; } finally { busy.value = ""; }
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
let logTimer: number | undefined;
async function openLogs(app: App) {
  logApp.value = app;
  logData.value = { logs: "", status: "" };
  await refreshLogs();
  if (logTimer !== undefined) window.clearInterval(logTimer);
  logTimer = window.setInterval(() => { void loadLogs(); }, 3000);
}
function closeLogs() {
  logApp.value = null;
  if (logTimer !== undefined) { window.clearInterval(logTimer); logTimer = undefined; }
}
async function loadLogs() {
  if (!logApp.value) return;
  logBusy.value = true;
  try {
    logData.value = await api<{ logs: string; status: string; lastError?: string; sampledAt?: string }>(
      `/apps/${logApp.value.id}/logs`,
    );
  } catch (e) {
    logData.value = { logs: "", status: "failed", lastError: (e as Error).message };
  } finally {
    logBusy.value = false;
  }
}
async function refreshLogs() {
  if (!logApp.value) return;
  logBusy.value = true;
  try {
    const result = await api<{ status: string }>(`/apps/${logApp.value.id}/logs/refresh`, { method: "POST", body: "{}" });
    logData.value = { ...logData.value, status: result.status };
    await loadLogs();
  } catch (e) {
    logData.value = { ...logData.value, status: "failed", lastError: (e as Error).message };
  } finally {
    logBusy.value = false;
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
    const method = paymentMethods.value.find((item) => item.type === selectedPaymentType.value);
    if (!method) throw new Error("请选择可用的付款方式");
    if (Math.round(topup.value * 100) < method.minAmountCents) throw new Error(method.name + "最低充值 ¥" + (method.minAmountCents / 100).toFixed(2));
    const result = await api<{ checkoutUrl?: string }>("/payments/orders", {
      method: "POST",
      body: JSON.stringify({
        amountCents: Math.round(topup.value * 100),
        provider,
        paymentType: selectedPaymentType.value,
        idempotencyKey: crypto.randomUUID(),
      }),
    });
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
    <section v-if="page === 'overview'" class="overview-apps">
      <div class="section-heading"><div><p class="eyebrow">已部署资源</p><h2>应用概要</h2></div><RouterLink class="secondary compact" to="/console/apps">查看全部</RouterLink></div>
      <div v-if="appsLoading && !apps.length" class="overview-app-grid" aria-busy="true"><article v-for="index in 3" :key="index" class="overview-app-card"><div class="overview-app-title"><span class="skeleton" style="width:38px;height:38px;border-radius:11px"></span><div style="display:grid;gap:8px"><span class="skeleton skeleton-title" style="width:120px"></span><span class="skeleton skeleton-text" style="width:80px"></span></div></div><div class="runtime-sample-state"><span class="skeleton skeleton-text" style="width:100px"></span></div><div class="app-resource-bars"><div><span class="skeleton skeleton-text" style="width:60%"></span><span class="skeleton skeleton-text" style="width:30%"></span></div><div><span class="skeleton skeleton-text" style="width:60%"></span><span class="skeleton skeleton-text" style="width:30%"></span></div></div></article></div>
      <div v-else class="overview-app-grid"><article v-for="app in apps" :key="app.id" class="overview-app-card"><div class="overview-app-title"><span class="app-icon"><AppWindow :size="18" /></span><div><strong>{{ instanceLabel(app) }}</strong><small>{{ app.productSlug }}</small></div><span :class="['status-pill', app.status === 'running' ? 'active' : 'pending']">{{ app.status === 'running' ? '运行中' : app.status }}</span></div><div class="runtime-sample-state"><i :class="{ live: metricsAvailable(app) }"></i>{{ metricFreshness(app) }}</div><div class="app-resource-bars"><div><span>CPU 实时占用</span><b>{{ metricsAvailable(app) ? (app.cpuUsageCores || 0).toFixed(2) : '--' }} / {{ app.cpuCores || 0 }} 核</b><i><em :style="{width: metricsAvailable(app) ? Math.min(100, (app.cpuUsageCores || 0) / Math.max(app.cpuCores || 1, 0.1) * 100) + '%' : '0%'}"></em></i></div><div><span>内存实时占用</span><b>{{ metricsAvailable(app) ? Math.round(app.memoryUsageMiB || 0) : '--' }} / {{ app.memoryMiB || 0 }} MiB</b><i><em :style="{width: metricsAvailable(app) ? Math.min(100, (app.memoryUsageMiB || 0) / Math.max(app.memoryMiB || 1, 1) * 100) + '%' : '0%'}"></em></i></div></div><footer><div><small>预计每月</small><strong>¥ {{ ((app.estimatedMonthlyCents || 0) / 100).toFixed(2) }}</strong><small v-if="!app.estimateComplete">（不含未定价项与流量）</small></div><span class="overview-app-access"><template v-if="app.hostPort"><a class="direct-access" :href="'http://' + locationHost + ':' + app.hostPort" target="_blank" rel="noopener" @click.stop>直连 :{{ app.hostPort }}<ExternalLink :size="12" /></a></template><button v-if="app.publicPath" class="primary compact" @click="visitApp(app)">访问应用<ExternalLink :size="14" /></button><span v-else class="quiet">尚无公网地址</span></span></footer></article><p v-if="!apps.length" class="quiet empty-copy">还没有部署应用，可从“部署应用”选择管理员发布的产品。</p></div>
    </section>
    <section v-if="page === 'overview' && faqs.length" class="faq-overview-panel">
      <div class="section-heading"><div><p class="eyebrow">帮助中心</p><h2>常见问答</h2></div><RouterLink class="secondary compact" to="/console/faq">查看全部</RouterLink></div>
      <div class="faq-overview-list">
        <article v-for="item in faqs.slice(0, 5)" :key="item.id" class="faq-overview-item">
          <button type="button" class="faq-overview-question" :aria-expanded="openFaq === item.id" @click="openFaq = openFaq === item.id ? '' : item.id"><CircleHelp :size="18" /><strong>{{ item.question }}</strong><ChevronDown :class="{ rotated: openFaq === item.id }" :size="17" /></button>
          <div v-if="openFaq === item.id" class="faq-overview-answer"><p>{{ item.answer }}</p></div>
        </article>
      </div>
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
    <section v-if="page === 'apps' && appsLoading && !apps.length" class="product-list app-skeleton-list" aria-busy="true">
      <div class="section-heading"><div><p class="eyebrow">我的应用</p><h2>部署任务</h2></div><span class="skeleton skeleton-text" style="width:64px"></span></div>
      <article v-for="index in 3" :key="index" class="product-row"><span class="skeleton" style="width:40px;height:40px;border-radius:11px"></span><div style="flex:1;display:grid;gap:8px"><span class="skeleton skeleton-title" style="width:40%"></span><span class="skeleton skeleton-text" style="width:60%"></span></div><span class="skeleton skeleton-text" style="width:88px"></span></article>
    </section>
    <section v-if="page === 'apps' && !apps.length && !appsLoading" class="context-empty apps-empty">
      <span class="context-empty-icon" aria-hidden="true">
        <Rocket :size="28" />
      </span>
      <div class="apps-empty-content">
        <p class="eyebrow">应用实例</p>
        <h2>还没有部署应用</h2>
        <p>
          从管理员发布的产品中部署第一个应用；后续每次部署和更新都会形成可追溯的版本记录。
        </p>
        <div class="empty-actions">
          <RouterLink class="primary compact" to="/console/deploy">
            <Plus :size="16" />部署第一个应用
          </RouterLink>
          <RouterLink class="secondary compact" to="/console">
            <Gauge :size="16" />返回概览
          </RouterLink>
        </div>
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
      <article v-for="app in apps" :key="app.id" class="product-row app-product-row" role="link" tabindex="0" @click="router.push('/console/apps/' + (app.instanceId || app.id))" @keydown.enter="router.push('/console/apps/' + (app.instanceId || app.id))">
        <span class="product-icon"><AppWindow :size="20" /></span>
        <div>
          <strong>{{ instanceLabel(app) }}</strong
          ><small>{{ app.productSlug }} · {{ appState(app) }}<template v-if="app.hostPort"> · 直连 :{{ app.hostPort }}</template></small>
        </div>
        <div class="product-row-main">
          <div v-if="app.status === 'failed' || app.jobLastError" class="deployment-error-card">
            <div class="deployment-error-title"><CircleAlert :size="16"/><strong>{{ app.status === 'failed' ? '部署失败' : '部署任务异常' }}</strong><button class="secondary compact" type="button" @click="openDeploymentDetails(app)">查看部署详情</button></div>
            <p>{{ deploymentErrorHint(app.jobLastError) }}</p>
            <code v-if="app.jobLastError">{{ deploymentErrorText(app.jobLastError) }}</code>
          </div>
        </div>
        <div class="product-controls" @click.stop>
          <span class="status-pill" :class="appStateClass(app)">{{
            appState(app)
          }}</span
          ><button
            class="icon-action"
            title="查看日志"
            :disabled="busy === app.id"
            @click="openLogs(app)"
          >
            <FileText :size="17" /></button
          ><button
            class="icon-action"
            title="管理 Secret"
            @click="openSecrets(app)"
          >
            <KeyRound :size="17" /></button
          ><button
            v-if="app.status === 'running' && app.publicPath"
            class="icon-action"
            title="打开应用"
            @click="visitApp(app)"
            ><ExternalLink :size="17" /></button
          ><button
            v-if="app.status === 'running'"
            class="icon-action"
            title="编辑配置并重新部署"
            :disabled="writeLocked || busy === app.id || deployConfigurationLoading"
            @click="openEditApp(app)"
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
          </button
          ><button
            class="icon-action stop-action"
            title="删除应用和持久数据"
            :disabled="writeLocked || busy === app.id"
            @click="deleteApp(app)"
          >
            <Trash2 :size="17" />
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
              productCatalog.reduce((sum, group) => sum + group.versions.filter((v) => v.deployable).length, 0)
            }}
            个可部署版本</span
          >
        </div>
      </div>
      <div v-if="productsLoading&&!productCatalog.length" class="skeleton-list" aria-busy="true"><article v-for="index in 3" :key="index" class="product-row skeleton-row"><span class="skeleton" style="width:40px;height:40px;border-radius:11px"></span><div style="flex:1;display:grid;gap:8px"><span class="skeleton skeleton-title" style="width:36%"></span><span class="skeleton skeleton-text" style="width:56%"></span></div><span class="skeleton skeleton-text" style="width:96px"></span></article></div>
      <template v-else>
      <article
        v-for="group in productCatalog"
        :key="group.id"
        class="product-row deploy-product-group"
        :class="{ unavailable: !group.versions.some((v) => v.deployable) }"
        role="button"
        tabindex="0"
        @click="openVersionPicker(group)"
        @keydown.enter="openVersionPicker(group)"
      >
        <span class="product-icon"><AppWindow :size="20"/><img v-if="group.iconUrl" :src="group.iconUrl" alt="" @error="($event.currentTarget as HTMLImageElement).style.display='none'"/></span>
        <div>
          <strong>{{ group.name }}</strong
          ><small
            >{{ group.slug }}<template v-if="group.versions.length"> · {{ group.versions.length }} 个已发布版本</template><template v-else> · 暂无已发布版本</template></small
          >
        </div>
        <button
          v-if="group.versions.length"
          class="secondary compact"
          :disabled="writeLocked || !group.versions.some((v) => v.deployable)"
          @click.stop="openVersionPicker(group)"
        >
          <Plus :size="16" />选择版本
        </button>
        <span v-else class="status-pill suspended">暂无版本</span>
      </article>
      <p v-if="!productCatalog.length" class="quiet empty-copy">还没有可部署的应用模板</p>
      </template>
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
              <button v-for="method in paymentMethods" :key="method.type" type="button" :class="['payment-method',{active:selectedPaymentType===method.type}]" @click="selectedPaymentType=method.type"><CreditCard :size="19"/><span><strong>{{method.name}}</strong><small>最低充值 ¥{{(method.minAmountCents/100).toFixed(2)}}</small></span><Check v-if="selectedPaymentType===method.type" :size="16"/></button>
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
                  topup <= 0 || !selectedPaymentType ||
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
    ><div v-if="pickedProduct" class="modal-backdrop" @click.self="pickedProduct = null">
      <section class="secret-dialog deploy-dialog version-picker-dialog">
        <header>
          <div>
            <p class="eyebrow">{{ pickedProduct.slug }}</p>
            <h2>{{ pickedProduct.name }}</h2>
          </div>
          <button class="icon-action" title="关闭" @click="pickedProduct = null"><X :size="18" /></button>
        </header>
        <div v-if="pickedProduct.versions.length" class="version-picker-list">
          <article v-for="version in pickedProduct.versions" :key="version.versionId" :class="['version-picker-row', { unavailable: !version.deployable }]">
            <div><strong>{{ version.versionLabel || ('版本 v' + version.version) }}</strong><small>v{{ version.version }}<template v-if="version.missingDependencies?.length"> · 缺少 {{ version.missingDependencies.join('、') }}</template><template v-else-if="!version.deployable"> · 当前套餐未包含</template></small></div>
            <button class="primary compact" :disabled="writeLocked || !version.deployable" @click="pickVersion(pickedProduct, version)"><Rocket :size="15" />部署</button>
          </article>
        </div>
        <div v-else class="context-empty compact-empty"><AppWindow :size="22" /><p>该应用暂无已发布版本，请联系管理员。</p></div>
      </section>
    </div></Transition
  >
  <Transition name="modal-pop"
    ><div v-if="deployProduct" class="modal-backdrop" @click.self="closeDeploy">
      <section class="secret-dialog deploy-dialog">
        <header>
          <div>
            <p class="eyebrow">{{ deployMode === 'update' ? '编辑配置 · 重新部署' : '部署应用' }}</p>
            <h2>{{ deployProduct.name }}<small v-if="deployMode === 'update'"> · {{ deploySlug }}</small></h2>
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
              :readonly="deployMode === 'update'"
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
                :readonly="deployProduct.runtimeSpec?.editableOptions?.cpu === false"
              /><small
                >最低 {{ deployProduct.runtimeSpec?.cpuCores || 1 }} 核{{ deployProduct.runtimeSpec?.editableOptions?.cpu === false ? '，管理员已固定' : '，可向上调整' }}</small
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
                :readonly="deployProduct.runtimeSpec?.editableOptions?.memory === false"
              /><small
                >最低
                {{ deployProduct.runtimeSpec?.memoryMiB || 512 }} MiB{{ deployProduct.runtimeSpec?.editableOptions?.memory === false ? '，管理员已固定' : '，可向上调整' }}</small
              ></label
            >
            <label
              >容器内网端口<input :value="deployPort" readonly /><small
                >管理员按镜像实际监听端口固定；公网请求由 Gateway 转发到容器的
                {{ deployPort }} 端口，用户调整 CPU、内存或数据卷不会改变它</small
              ></label
            >
            <div v-if="deployPortMappingAvailable" class="switch-setting deploy-port-mapping">
              <div><strong>开启端口映射（直连）</strong><small>在宿主机直接发布端口，绕过网关直连；端口由系统自动分配</small></div>
              <label class="switch"><input v-model="deployPortMapping" type="checkbox"/><span/></label>
            </div>
          </div>
          <label v-if="deployProduct.runtimeSpec?.volumes?.length"
            >共享数据卷容量 GiB<input
              v-model.number="deployDataVolumeGiB"
              type="number"
              :min="deployVolumeFloorGiB || deployProduct.runtimeSpec?.dataVolumeGiB || Math.max(...(deployProduct.runtimeSpec?.volumes || []).map((volume) => volume.sizeGiB))"
              max="16384"
              step="1"
              required
              :readonly="deployProduct.runtimeSpec?.editableOptions?.dataVolume === false"
            /><small
              >全部挂载（{{ (deployProduct.runtimeSpec?.volumes || []).map((volume) => volume.mountPath).join('、') }}）和成功备份共享同一容量，{{ deployMode === 'update' ? '当前配置只允许扩容，最低' : '最低' }} {{ deployVolumeFloorGiB || deployProduct.runtimeSpec?.dataVolumeGiB || deployDataVolumeGiB }} GiB，只计费一次</small
            ></label
          >
          <label v-if="deployProduct.runtimeSpec?.editableOptions?.command"
            >启动命令<input
              v-model="deployCommand"
              placeholder="留空使用镜像默认命令"
              autocomplete="off"
            /><small
              >按空格拆分参数；复杂参数建议由镜像入口脚本处理</small
            ></label
          >
          <div v-if="deployProduct.runtimeSpec?.editableOptions?.environment === true && (deployProduct.runtimeSpec?.editableEnvKeys?.length || 0)" class="deploy-custom-list">
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
              <small class="env-help">{{ deployProduct.runtimeSpec?.envDescriptions?.[item.key] || "管理员未提供说明" }}</small>
            </div>
          </div>
          <div v-if="deployProduct.runtimeSpec?.editableOptions?.dependencies" class="deploy-dependencies">
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
          <template v-if="deploySecretOptions.length">
            <div class="deploy-secret-heading">
              <KeyRound :size="17" />
              <div>
                <strong>{{ deployMode === 'update' ? 'Secret 配置' : '部署 Secret' }}</strong
                ><small v-if="deployMode === 'create'">加密保存，提交后不会再次显示</small
                ><small v-else>明文不会回显；留空表示继续使用当前版本，只有管理员开放的字段可修改</small>
              </div>
            </div>
            <template v-if="deployMode === 'create'">
              <label v-for="option in deploySecretOptions" :key="option.key" class="deploy-secret-field">
                {{ option.key }}
                <small v-if="option.description" class="env-help">{{ option.description }}</small>
                <input v-model="deploySecrets[option.key]" type="password" required autocomplete="new-password" :placeholder="option.description || '请输入 Secret 值'" />
              </label>
            </template>
            <div v-else class="deploy-secret-update-list">
              <template v-for="option in deploySecretOptions" :key="option.key">
                <label v-if="option.editable" class="deploy-secret-field">
                  {{ option.key }}
                  <small v-if="option.description" class="env-help">{{ option.description }}</small>
                  <input v-model="deploySecrets[option.key]" type="password" autocomplete="new-password" :placeholder="deployConfiguredSecretKeys.includes(option.key) ? '留空继续使用当前版本' : '尚未配置，必须填写'" :required="!deployConfiguredSecretKeys.includes(option.key)" />
                </label>
                <div v-else class="deploy-secret-locked">
                  <KeyRound :size="15" />
                  <div><strong>{{ option.key }}</strong><small>{{ option.description || '管理员未提供说明' }}</small></div>
                  <span>仅管理员</span>
                </div>
              </template>
              <p v-if="missingDeploySecretKeys.length" class="deploy-secret-warning">尚未配置：{{ missingDeploySecretKeys.join('、') }}。固定 Secret 需要管理员先配置；可编辑 Secret 可在上方填写。</p>
            </div>
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
              <Rocket :size="16" />{{ deployMode === 'update' ? '保存配置并重新部署' : '创建部署' }}
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
      <div class="secret-explainer">
        <KeyRound :size="18" />
        <p><strong>应用 Secret 用于 API Key、令牌和数据库密码等敏感值。</strong><span>平台加密保存且永不回显；每次修改创建新版本。正在运行的容器继续使用当前发布固定的旧版本，执行“编辑配置并重新部署”后才会注入最新版本。</span></p>
      </div>
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
  <div v-if="deploymentApp" class="modal-backdrop" @click.self="deploymentApp = null">
    <section class="secret-dialog deployment-dialog">
      <header>
        <div>
          <p class="eyebrow">{{ deploymentApp.slug }}</p>
          <h2>部署详情</h2>
        </div>
        <button class="icon-action" title="关闭" @click="deploymentApp = null"><X :size="18" /></button>
      </header>
      <p v-if="deploymentBusy" class="quiet">正在读取部署事件…</p>
      <p v-else-if="!deploymentJobs.length" class="quiet">暂无部署任务记录</p>
      <div v-for="job in deploymentJobs" :key="job.id" class="deployment-job">
        <div class="deployment-job-heading">
          <span :class="['status-pill', job.state === 'succeeded' ? 'active' : job.state === 'failed' ? 'danger' : 'pending']">{{ state(job.state) }}</span>
          <small>{{ new Date(job.updatedAt || job.createdAt).toLocaleString() }} · 尝试 {{ job.attempts }} 次</small>
        </div>
        <p v-if="job.lastError" class="deployment-detail-hint">{{ deploymentErrorHint(job.lastError) }}</p>
        <code v-if="job.lastError" class="deployment-raw-error">{{ deploymentErrorText(job.lastError) }}</code>
        <ol v-if="job.events.length" class="deployment-timeline">
          <li v-for="event in job.events" :key="event.id || event.createdAt">
            <span>{{ event.toState ? state(event.toState) : '事件' }}</span>
            <small>{{ event.message || '状态已更新' }} · {{ event.createdAt ? new Date(event.createdAt).toLocaleString() : '' }}</small>
          </li>
        </ol>
      </div>
    </section>
  </div>
  <div v-if="logApp" class="modal-backdrop" @click.self="closeLogs">
    <section class="secret-dialog log-dialog">
      <header>
        <div>
          <p class="eyebrow">{{ logApp.slug }}</p>
          <h2>运行日志</h2>
          <small v-if="logData.sampledAt">采样于 {{ new Date(logData.sampledAt).toLocaleString() }} · 每 3 秒自动刷新</small>
        </div>
        <button class="icon-action" title="关闭" @click="closeLogs"><X :size="18" /></button>
      </header>
      <div class="log-toolbar">
        <span :class="['status-pill', logData.status === 'succeeded' || logData.status === 'cached' ? 'active' : logData.status === 'failed' ? 'danger' : 'pending']">{{ logStatusLabel(logData.status) }}</span>
        <button class="secondary compact" :disabled="logBusy" @click="refreshLogs"><RefreshCw :class="{ spin: logBusy }" :size="15" />立即刷新</button>
      </div>
      <p v-if="logData.lastError" class="message">{{ logData.lastError }}</p>
      <p v-if="!logData.logs && !logBusy" class="quiet log-empty">还没有可显示的日志，点击“拉取最新日志”获取容器运行日志。</p>
      <pre class="log-viewer" :class="{ 'log-loading': logBusy }">{{ logData.logs || (logBusy ? '正在拉取日志…' : '') }}</pre>
    </section>
  </div>
</template>
