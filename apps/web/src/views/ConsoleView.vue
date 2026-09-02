<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import {
  AppWindow,
  ArchiveRestore,
  BadgeDollarSign,
  Boxes,
  ChevronLeft,
  ChevronRight,
  Coins,
  CreditCard,
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
  MessageSquareText,
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
import { directPortURL, useSiteConfig } from "../site-config";

type Dependency = {
  key: string;
  productId: string;
  serviceSlug: string;
  required: boolean;
};
type Volume = { name: string; mountPath: string; sizeGiB: number };
type SecretOption = { key: string; description?: string; editable?: boolean };
type EditableOptions = {
  cpu?: boolean;
  memory?: boolean;
  dataVolume?: boolean;
  command?: boolean;
  dependencies?: boolean;
  environment?: boolean;
};
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
    listeners?: ListenerPort[];
    portMapping?: { available?: boolean };
  };
};
type ListenerPort = { key: string; containerPort: number; remark: string; primary: boolean; userEditable: boolean; mappingAvailable: boolean; mappingEnabled?: boolean };
type ProductGroup = Omit<
  Product,
  | "versionId"
  | "version"
  | "versionLabel"
  | "deployable"
  | "missingDependencies"
  | "runtimeSpec"
  | "routeSpec"
> & {
  versions: Array<
    Pick<
      Product,
      | "versionId"
      | "version"
      | "versionLabel"
      | "deployable"
      | "missingDependencies"
      | "runtimeSpec"
      | "routeSpec"
    >
  >;
};
type AppConfiguration = {
  app: { id: string; slug: string; status: string; productSlug: string };
  current: {
    versionId: string;
    runtimeSpec: NonNullable<Product["runtimeSpec"]>;
    routeSpec: NonNullable<Product["routeSpec"]>;
  };
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
  access: { passwordEnabled: boolean; username: string; passwordConfigured: boolean };
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
  routeHostLabel?: string;
  domainRefreshDays?: number | null;
  domainNextRefreshAt?: string;
  containerPort?: number;
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
type DeploymentEvent = {
  id?: number;
  fromState?: string;
  toState?: string;
  message?: string;
  createdAt?: string;
};
type DeploymentJob = {
  id: string;
  state: string;
  attempts: number;
  lastError?: string | null;
  createdAt: string;
  updatedAt: string;
  events: DeploymentEvent[];
};
type Usage = {
  appId?: string | null;
  appSlug?: string | null;
  productSlug?: string | null;
  appDeleted?: boolean;
  usageCode: string;
  unit: string;
  windowStart: string;
  windowEnd: string;
  quantity: string;
  amountCents?: number | null;
  unitPriceMicros?: number | null;
  pricingVersionId?: string | null;
  sealedAt?: string;
  billingDisposition?: "pending" | "charged" | "unpriced" | "waived_legacy";
};
type UsageAppGroup = {
  key: string;
  appId?: string;
  appSlug: string;
  productName: string;
  iconUrl?: string;
  items: Usage[];
  categoryCount: number;
  totalAmountCents: number;
  latestAt: string;
  deleted: boolean;
};
type UsageCategorySummary = {
  key: string;
  usageCode: string;
  unit: string;
  quantity: number;
  amountCents: number;
  unitPrices: number[];
  records: Usage[];
  latestAt: string;
  allSettled: boolean;
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
type PaymentMethod = {
  name: string;
  type: string;
  minAmountCents: number;
  enabled: boolean;
};
type PaymentProvider = {
  provider: string;
  enabled: boolean;
  paymentMethods?: PaymentMethod[];
  amountOptions?: number[];
};
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
type AppSecretResponse = {
  secrets: AppSecret[];
  allowedKeys: string[];
  editableKeys?: string[];
  options?: SecretOption[];
};
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
    | "usage";
}>();
const router = useRouter();
const page = computed(() => props.page || "overview");
const { fullSettings } = useSiteConfig();
const pageTitle = computed(
  () =>
    ({
      overview: "概览",
      deploy: "部署应用",
      apps: "我的应用",
      billing: "余额与账单",
      recharge: "账户充值",
      usage: "用量明细",
    })[page.value],
);
const pageEyebrow = computed(
  () =>
    ({
      overview: "",
      deploy: "应用部署",
      apps: "实例管理",
      billing: "财务结算",
      recharge: "资金管理",
      usage: "用量明细",
    })[page.value] || "",
);
const pageDescription = computed(
  () =>
    ({
      overview: "监控系统资源、钱包余额与已部署实例概况。",
      deploy: "选择管理员已发布的产品模板，一键启动容器实例。",
      apps: "管理您正在运行的容器应用、运行状态与日志配置。",
      billing: "查看账本变动流水、月度结算账单与赠送额度明细。",
      recharge: "支持多种支付渠道充值，余额即时到账。",
      usage: "按应用汇总资源用量，进入应用后查看各计费分类的单价与合计。",
    })[page.value],
);
const products = ref<Product[]>([]),
  productCatalog = ref<ProductGroup[]>([]),
  pickedProduct = ref<ProductGroup | null>(null),
  apps = ref<App[]>([]),
  usage = ref<Usage[]>([]),
  ledger = ref<LedgerEntry[]>([]),
  bills = ref<Bill[]>([]),
  dailyBills = ref<any[]>([]),
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
const logData = ref<{
  logs: string;
  status: string;
  lastError?: string;
  sampledAt?: string;
}>({ logs: "", status: "" });
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
const deployListenerPorts = ref<ListenerPort[]>([]);
const deployDomainPermanent = ref(true);
const deployDomainRefreshDays = ref(30);
const deployPortMapping = ref(false);
const deployPasswordAccess = ref(false);
const deployAccessUsername = ref("");
const deployAccessPassword = ref("");
const deployAccessPasswordConfigured = ref(false);
const deployPortMappingAvailable = computed(
  () => deployListenerPorts.value.some((item) => item.mappingAvailable),
);
function selectedDeployListeners(route?: Product["routeSpec"]): ListenerPort[] {
  if (route?.listeners?.length) return route.listeners.map((item) => ({ ...item, mappingEnabled: item.mappingEnabled === true }));
  return [{ key: "web", containerPort: route?.containerPort || 8080, remark: "Web 入口", primary: true, userEditable: false, mappingAvailable: route?.portMapping?.available === true, mappingEnabled: false }];
}
const deployEnvironment = ref<{ key: string; value: string }[]>([]);
const deployDependencies = ref<Dependency[]>([]);
const deployPricePrediction = computed(() => {
  const cpuPriceMicros = pricing.value["cpu.core_hours"] || 0;
  const memoryPriceMicros = pricing.value["memory.gib_hours"] || 0;
  const diskPriceMicros = pricing.value["storage.data.gib_days"] || 0;

  const cpuRmbPerMonth = (deployCPU.value * 720 * cpuPriceMicros) / 100000000;
  const memoryRmbPerMonth = ((deployMemory.value / 1024) * 720 * memoryPriceMicros) / 100000000;
  const diskRmbPerMonth = (deployDataVolumeGiB.value * 30 * diskPriceMicros) / 100000000;

  return (cpuRmbPerMonth + memoryRmbPerMonth + diskRmbPerMonth).toFixed(2);
});
const missingDeploySecretKeys = computed(() =>
  (deployProduct.value?.runtimeSpec?.secretKeys || []).filter(
    (key) =>
      !deployConfiguredSecretKeys.value.includes(key) &&
      !String(deploySecrets.value[key] || "").trim(),
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
const pricing = ref<Record<string, number>>({});
const globalPriceItems = ref<{ code: string; unit: string; unitPriceMicros: number }[]>([]);
function formatGlobalPrice(unitPriceMicros: number) {
  return (unitPriceMicros / 100000000)
    .toFixed(8)
    .replace(/0+$/, "")
    .replace(/\.$/, "");
}
const checkin = ref<CheckinSummary | null>(null);
async function loadCheckin() {
  checkin.value = await api<CheckinSummary>("/checkin");
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
    window.dispatchEvent(
      new CustomEvent("cloudmeter:balance-changed", {
        detail: { balanceCents: result.balanceCents },
      }),
    );
    message.value = `签到成功，获得 ¥${(result.rewardCents / 100).toFixed(2)}`;
    await Promise.all([loadCheckin(), load()]);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = "";
  }
}
const epayProvider = computed(() =>
  paymentProviders.value.find((item) => item.provider === "epay"),
);
const topupOptions = computed(() =>
  epayProvider.value?.amountOptions?.length
    ? epayProvider.value.amountOptions
    : [10, 20, 50, 100, 200, 300, 400, 500],
);
const paymentMethods = computed(() =>
  (epayProvider.value?.paymentMethods || []).filter((item) => item.enabled),
);
const totalSpend = computed(() =>
  ledger.value
    .filter((item) => item.amountCents < 0)
    .reduce((sum, item) => sum - item.amountCents, 0),
);
const selectedUsageAppKey = ref("");
const pricedUsage = computed(() =>
  usage.value.filter(
    (item) => item.billingDisposition !== "unpriced",
  ),
);
const usageAppGroups = computed<UsageAppGroup[]>(() => {
  const groups = new Map<string, Omit<UsageAppGroup, "categoryCount">>();

  for (const item of pricedUsage.value) {
    const key = item.appId || "account";
    const app = item.appId
      ? apps.value.find((entry) => entry.id === item.appId)
      : undefined;
    const productSlug = item.productSlug || app?.productSlug;
    const product = productCatalog.value.find(
      (entry) => entry.slug === productSlug,
    );
    const current = groups.get(key) || {
      key,
      appId: item.appId || undefined,
      appSlug: item.appSlug || app?.slug || "账户级用量",
      productName: product?.name || productSlug || "平台公共费用",
      iconUrl: product?.iconUrl,
      items: [],
      totalAmountCents: 0,
      latestAt: item.windowStart,
      deleted: item.appDeleted === true,
    };

    current.items.push(item);
    current.totalAmountCents += item.amountCents ?? 0;
    if (
      !current.latestAt ||
      new Date(item.windowStart) > new Date(current.latestAt)
    ) {
      current.latestAt = item.windowStart;
    }
    groups.set(key, current);
  }

  return Array.from(groups.values())
    .map((group) => ({
      ...group,
      categoryCount: new Set(group.items.map((item) => item.usageCode)).size,
    }))
    .sort(
      (left, right) =>
        (Date.parse(right.latestAt) || 0) - (Date.parse(left.latestAt) || 0),
    );
});
const activeUsageAppGroups = computed(() =>
  usageAppGroups.value.filter((group) => group.appId && !group.deleted),
);
const deletedUsageAppGroups = computed(() =>
  usageAppGroups.value.filter((group) => group.appId && group.deleted),
);
const accountUsageGroups = computed(() =>
  usageAppGroups.value.filter((group) => !group.appId),
);
const usageGroupSections = computed(() =>
  [
    { key: "active", title: "当前应用", groups: activeUsageAppGroups.value },
    { key: "deleted", title: "已删除应用", groups: deletedUsageAppGroups.value },
    { key: "account", title: "账户级费用", groups: accountUsageGroups.value },
  ].filter((section) => section.groups.length),
);
const selectedUsageGroup = computed(() =>
  usageAppGroups.value.find((group) => group.key === selectedUsageAppKey.value),
);
const selectedUsageCategories = computed<UsageCategorySummary[]>(() => {
  if (!selectedUsageGroup.value) return [];
  const categories = new Map<string, UsageCategorySummary>();

  for (const item of selectedUsageGroup.value.items) {
    const key = `${item.usageCode}:${item.unit}`;
    const current = categories.get(key) || {
      key,
      usageCode: item.usageCode,
      unit: item.unit,
      quantity: 0,
      amountCents: 0,
      unitPrices: [],
      records: [],
      latestAt: item.windowStart,
      allSettled: true,
    };
    const quantity = Number(item.quantity);
    if (Number.isFinite(quantity)) current.quantity += quantity;
    current.amountCents += item.amountCents ?? 0;
    if (
      item.unitPriceMicros != null &&
      !current.unitPrices.includes(item.unitPriceMicros)
    ) {
      current.unitPrices.push(item.unitPriceMicros);
    }
    current.records.push(item);
    current.allSettled &&= Boolean(item.sealedAt);
    if (new Date(item.windowStart) > new Date(current.latestAt)) {
      current.latestAt = item.windowStart;
    }
    categories.set(key, current);
  }

  return Array.from(categories.values()).sort(
    (left, right) =>
      new Date(right.latestAt).getTime() - new Date(left.latestAt).getTime(),
  );
});

function formatUsageQuantity(quantity: number) {
  return new Intl.NumberFormat("zh-CN", {
    maximumFractionDigits: 6,
  }).format(quantity);
}

function formatUsageAmount(cents: number) {
  return `¥ ${(cents / 100).toFixed(2)}`;
}

function formatUsageUnitPrice(prices: number[], unit: string) {
  if (!prices.length) return "¥0 / " + usageUnitLabel(unit);
  const sorted = [...prices].sort((left, right) => left - right);
  const format = (micros: number) =>
    `¥${(micros / 100000000)
      .toFixed(8)
      .replace(/0+$/, "")
      .replace(/\.$/, "")}`;
  const value =
    sorted[0] === sorted[sorted.length - 1]
      ? format(sorted[0])
      : `${format(sorted[0])} – ${format(sorted[sorted.length - 1])}`;
  return `${value} / ${usageUnitLabel(unit)}`;
}
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
    }[status] ||
    status ||
    "未知"
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
      dailyBillsData,
      pricingData,
      pricingCatalog,
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
      api<{ faqs: { id: string; question: string; answer: string }[] }>(
        "/faqs",
      ),
      api<any>("/billing/daily-bills"),
      api<Record<string, number>>("/pricing"),
      api<{ items: { code: string; unit: string; unitPriceMicros: number }[] }>("/pricing/catalog"),
    ]);
    name.value = m.DisplayName;
    impersonation.value = {
      active: Boolean(m.Impersonating),
      readOnly: Boolean(m.ImpersonationReadOnly),
      actorName: m.ActorDisplayName || "管理员",
    };
    pricing.value = pricingData || {};
    globalPriceItems.value = (pricingCatalog.items || []).sort((left, right) =>
      usageCodeLabel(left.code).localeCompare(usageCodeLabel(right.code), "zh-CN"),
    );
    faqs.value = faqData.faqs || [];
    products.value = catalogToFlat(p.products);
    productCatalog.value = p.products;
    apps.value = a.apps;
    balance.value = b.balanceCents;
    window.dispatchEvent(
      new CustomEvent("cloudmeter:balance-changed", {
        detail: { balanceCents: b.balanceCents },
      }),
    );
    usage.value = u.usage;
    ledger.value = l.entries;
    bills.value = statementData.bills;
    dailyBills.value = dailyBillsData.dailyBills || [];
    creditAvailable.value = creditData.availableCents;
    creditGrants.value = creditData.grants;
    creditConsumptions.value = creditData.consumptions;
    orders.value = o.orders;
    paymentProviders.value = providers.providers;
    if (!topupOptions.value.includes(selectedTopup.value))
      chooseTopup(topupOptions.value[0] || 10);
    if (
      !paymentMethods.value.some(
        (item) => item.type === selectedPaymentType.value,
      )
    )
      selectedPaymentType.value = paymentMethods.value[0]?.type || "";
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
    if (page.value === "overview") await loadCheckin();
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
  if (metricsRefreshTimer !== undefined)
    window.clearInterval(metricsRefreshTimer);
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
function pickVersion(
  group: ProductGroup,
  version: ProductGroup["versions"][number],
) {
  pickedProduct.value = null;
  openDeploy({
    ...version,
    id: group.id,
    slug: group.slug,
    name: group.name,
    iconUrl: group.iconUrl,
  } as Product);
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
  deployDataVolumeGiB.value =
    p.runtimeSpec?.dataVolumeGiB ||
    Math.max(
      0,
      ...(p.runtimeSpec?.volumes || []).map((volume) => volume.sizeGiB),
    );
  deployVolumeFloorGiB.value = deployDataVolumeGiB.value;
  deployCommand.value = (p.runtimeSpec?.command || []).join(" ");
  deployPort.value = p.routeSpec?.containerPort || 8080;
  deployListenerPorts.value = selectedDeployListeners(p.routeSpec);
  deployDomainPermanent.value = true;
  deployDomainRefreshDays.value = 30;
  deployPortMapping.value = false;
  deployPasswordAccess.value = false;
  deployAccessUsername.value = "";
  deployAccessPassword.value = "";
  deployAccessPasswordConfigured.value = false;
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
    const configuration = await api<AppConfiguration>(
      `/apps/${app.id}/configuration`,
    );
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
    const minimumVolume =
      target.runtimeSpec?.dataVolumeGiB ||
      Math.max(
        0,
        ...(target.runtimeSpec?.volumes || []).map((volume) => volume.sizeGiB),
      );
    const currentVolume =
      current.dataVolumeGiB ||
      Math.max(0, ...(current.volumes || []).map((volume) => volume.sizeGiB));
    deployMode.value = "update";
    editingApp.value = app;
    deployConfiguredSecretKeys.value = configuration.configuredSecretKeys || [];
    deployProduct.value = target;
    deploySlug.value = app.slug;
    deploySecrets.value = Object.fromEntries(
      (target.runtimeSpec?.secretKeys || []).map((key) => [key, ""]),
    );
    deployCPU.value = Math.max(Number(current.cpuCores || 0), minimumCPU);
    deployMemory.value = Math.max(
      Number(current.memoryMiB || 0),
      minimumMemory,
    );
    deployDataVolumeGiB.value = Math.max(currentVolume, minimumVolume);
    deployVolumeFloorGiB.value = deployDataVolumeGiB.value;
    deployPortMapping.value = app.portMappingEnabled === true;
    deployPasswordAccess.value = configuration.access?.passwordEnabled === true;
    deployAccessUsername.value = configuration.access?.username || "";
    deployAccessPassword.value = "";
    deployAccessPasswordConfigured.value = configuration.access?.passwordConfigured === true;
    deployCommand.value = (
      current.command ||
      target.runtimeSpec?.command ||
      []
    ).join(" ");
    deployPort.value =
      configuration.current.routeSpec?.containerPort ||
      target.routeSpec?.containerPort ||
      8080;
    const currentListeners = selectedDeployListeners(configuration.current.routeSpec);
    deployListenerPorts.value = selectedDeployListeners(target.routeSpec).map((listener) => {
      const previous = currentListeners.find((item) => item.key === listener.key);
      return { ...listener, containerPort: previous && listener.userEditable ? previous.containerPort : listener.containerPort, mappingEnabled: previous?.mappingEnabled === true };
    });
    deployEnvironment.value = (target.runtimeSpec?.editableEnvKeys || []).map(
      (key) => ({
        key,
        value: current.env?.[key] ?? target.runtimeSpec?.env?.[key] ?? "",
      }),
    );
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
  deployDomainPermanent.value = true;
  deployDomainRefreshDays.value = 30;
  deployPortMapping.value = false;
  deployPasswordAccess.value = false;
  deployAccessUsername.value = "";
  deployAccessPassword.value = "";
  deployAccessPasswordConfigured.value = false;
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
  if (deployListenerPorts.value.some((item) => !Number.isInteger(item.containerPort) || item.containerPort < 1 || item.containerPort > 65535)) {
    error.value = "应用监听端口必须是 1 到 65535 之间的整数";
    return;
  }
  if (deployPasswordAccess.value) {
    if (!deployAccessUsername.value.trim() || deployAccessUsername.value.trim().length > 64) {
      error.value = "密码访问用户名必须为 1 到 64 个字符";
      return;
    }
    if (!deployAccessPasswordConfigured.value && deployAccessPassword.value.length < 8) {
      error.value = "首次开启密码访问时，密码至少需要 8 个字符";
      return;
    }
    if (new TextEncoder().encode(deployAccessPassword.value).length > 72) {
      error.value = "密码访问的密码不能超过 72 字节";
      return;
    }
  }
  if (
    deployMode.value === "create" &&
    !deployDomainPermanent.value &&
    (!Number.isInteger(deployDomainRefreshDays.value) || deployDomainRefreshDays.value < 1)
  ) {
    error.value = "独立子域名刷新周期至少为 1 天";
    return;
  }
  try {
    busy.value = "deploy";
    const resources = {
      cpuCores:
        p.runtimeSpec?.editableOptions?.cpu !== false
          ? deployCPU.value
          : undefined,
      memoryMiB:
        p.runtimeSpec?.editableOptions?.memory !== false
          ? deployMemory.value
          : undefined,
      dataVolumeGiB:
        p.runtimeSpec?.editableOptions?.dataVolume !== false &&
        (p.runtimeSpec?.volumes?.length || 0)
          ? deployDataVolumeGiB.value
          : undefined,
      command: p.runtimeSpec?.editableOptions?.command
        ? deployCommand.value.trim()
          ? deployCommand.value.trim().split(/\s+/)
          : []
        : undefined,
      environment: p.runtimeSpec?.editableOptions?.environment
        ? Object.fromEntries(
            deployEnvironment.value
              .filter((item) => item.key.trim())
              .map((item) => [item.key.trim(), item.value]),
          )
        : undefined,
      dependencies: p.runtimeSpec?.editableOptions?.dependencies
        ? deployDependencies.value
        : undefined,
      containerPort: deployPort.value,
      listenerPorts: deployListenerPorts.value.map((item) => ({ key: item.key, containerPort: item.containerPort, mappingEnabled: item.mappingEnabled === true })),
    };
    const changedSecrets = Object.fromEntries(
      Object.entries(deploySecrets.value).filter(
        ([, value]) => String(value).trim() !== "",
      ),
    );
    if (deployMode.value === "update" && editingApp.value) {
      await api(`/apps/${editingApp.value.id}/releases`, {
        method: "POST",
        body: JSON.stringify({
          versionId: p.versionId,
          idempotencyKey: crypto.randomUUID(),
          resources,
          secrets: changedSecrets,
          access: {
            passwordEnabled: deployPasswordAccess.value,
            username: deployAccessUsername.value.trim(),
            password: deployAccessPassword.value,
          },
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
          domainRefreshDays: deployDomainPermanent.value
            ? null
            : deployDomainRefreshDays.value,
          idempotencyKey: crypto.randomUUID(),
          secrets: deploySecrets.value,
          resources,
          access: {
            passwordEnabled: deployPasswordAccess.value,
            username: deployAccessUsername.value.trim(),
            password: deployAccessPassword.value,
          },
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
  const text = String(value || "");
  if (/no such image/i.test(text))
    return "镜像不存在或版本号不可用，请检查管理员发布的镜像地址与 Tag，并确认镜像源可访问。";
  if (/pull access denied|unauthorized|authentication required/i.test(text))
    return "镜像仓库拒绝访问，请让管理员检查 Registry 地址、凭据或镜像是否为私有仓库。";
  if (/timeout|timed out|deadline exceeded/i.test(text))
    return "拉取镜像超时，请检查镜像加速、代理、DNS 和网络连通性。";
  if (/connection refused|port is already allocated/i.test(text))
    return "容器启动端口或内部服务不可用，请让管理员检查内网端口与健康检查配置。";
  return "部署任务未完成，请查看下方原始错误与事件时间线，或联系管理员。";
}
function deploymentErrorText(value?: string) {
  const text = String(value || "").trim();
  return text.length > 1200 ? text.slice(0, 1200) + "…" : text;
}
async function openDeploymentDetails(app: App) {
  deploymentApp.value = app;
  deploymentJobs.value = [];
  deploymentBusy.value = true;
  try {
    const result = await api<{ deployments: DeploymentJob[] }>(
      "/apps/" + app.id + "/deployments",
    );
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
  if (
    writeLocked.value ||
    !window.confirm(
      `删除应用“${app.slug}”？运行容器、路由和持久数据卷会被回收，账单和发布审计历史仍会保留。此操作不可恢复。`,
    )
  )
    return;
  try {
    busy.value = app.id;
    await api(`/apps/${app.id}`, { method: "DELETE" });
    message.value = "应用已进入删除清理队列";
    await load();
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
let logTimer: number | undefined;
async function openLogs(app: App) {
  logApp.value = app;
  logData.value = { logs: "", status: "" };
  await refreshLogs();
  if (logTimer !== undefined) window.clearInterval(logTimer);
  logTimer = window.setInterval(() => {
    void loadLogs();
  }, 3000);
}
function closeLogs() {
  logApp.value = null;
  if (logTimer !== undefined) {
    window.clearInterval(logTimer);
    logTimer = undefined;
  }
}
async function loadLogs() {
  if (!logApp.value) return;
  logBusy.value = true;
  try {
    logData.value = await api<{
      logs: string;
      status: string;
      lastError?: string;
      sampledAt?: string;
    }>(`/apps/${logApp.value.id}/logs`);
  } catch (e) {
    logData.value = {
      logs: "",
      status: "failed",
      lastError: (e as Error).message,
    };
  } finally {
    logBusy.value = false;
  }
}
async function refreshLogs() {
  if (!logApp.value) return;
  logBusy.value = true;
  try {
    const result = await api<{ status: string }>(
      `/apps/${logApp.value.id}/logs/refresh`,
      { method: "POST", body: "{}" },
    );
    logData.value = { ...logData.value, status: result.status };
    await loadLogs();
  } catch (e) {
    logData.value = {
      ...logData.value,
      status: "failed",
      lastError: (e as Error).message,
    };
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
    const method = paymentMethods.value.find(
      (item) => item.type === selectedPaymentType.value,
    );
    if (!method) throw new Error("请选择可用的付款方式");
    if (Math.round(topup.value * 100) < method.minAmountCents)
      throw new Error(
        method.name + "最低充值 ¥" + (method.minAmountCents / 100).toFixed(2),
      );
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
    <header class="page-heading">
      <div>
        <p v-if="pageEyebrow" class="eyebrow">{{ pageEyebrow }}</p>
        <h1>
          {{ page === "overview" ? `晚上好，${name || "..."}` : pageTitle }}
        </h1>
        <p class="quiet">{{ pageDescription }}</p>
      </div>
      <button v-if="page !== 'overview'" class="secondary compact" @click="load">
        <RefreshCw :size="16" />刷新
      </button>
    </header>
    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok">{{ message }}</p>
    <section v-if="page === 'overview'" class="nextdev-stat-grid">
      <div class="stat-card accent">
        <div class="stat-card-header">
          <span class="stat-label">钱包余额</span>
          <Coins class="stat-icon" :size="16" />
        </div>
        <div class="stat-value mono-data">¥ {{ (balance / 100).toFixed(2) }}</div>
        <div class="stat-hint">含赠送额度 ¥ {{ (creditAvailable / 100).toFixed(2) }}</div>
      </div>

      <div class="stat-card">
        <div class="stat-card-header">
          <span class="stat-label">运行中实例</span>
          <Boxes class="stat-icon" :size="16" />
        </div>
        <div class="stat-value mono-data">{{ apps.filter((v) => v.status === 'running').length }}</div>
        <div class="stat-hint">共计 {{ apps.length }} 个应用实例</div>
      </div>

      <div class="stat-card">
        <div class="stat-card-header">
          <span class="stat-label">可用模板</span>
          <AppWindow class="stat-icon" :size="16" />
        </div>
        <div class="stat-value mono-data">{{ products.length }}</div>
        <div class="stat-hint">管理员已发布可用产品</div>
      </div>

      <div class="stat-card">
        <div class="stat-card-header">
          <span class="stat-label">预估月费</span>
          <BadgeDollarSign class="stat-icon" :size="16" />
        </div>
        <div class="stat-value mono-data">
          ¥ {{ ((apps.reduce((sum, a) => sum + (a.estimatedMonthlyCents || 0), 0)) / 100).toFixed(2) }}
        </div>
        <div class="stat-hint">按实际用量毫秒计费</div>
      </div>
    </section>

    <div v-if="page === 'overview'" class="nextdev-overview-layout">
      <!-- 左列 (2/3): 已部署应用大卡片 + 常见问答 -->
      <div class="overview-main-col">
        <!-- 已部署应用卡片 -->
        <div class="nextdev-card p-0">
          <div class="card-header-bar">
            <div class="card-title-group">
              <span class="eyebrow">DEPLOYMENTS · 实例列表</span>
              <h3>已部署应用</h3>
            </div>
            <RouterLink class="secondary compact" to="/console/apps">
              查看全部 <ChevronRight :size="14" />
            </RouterLink>
          </div>
          <div class="card-divider"></div>
          <div class="card-inner-body">
            <div
              v-if="appsLoading && !apps.length"
              class="empty-state-nextdev"
              aria-busy="true"
            >
              <span class="skeleton skeleton-title" style="width: 140px"></span>
              <span class="skeleton skeleton-text" style="width: 260px"></span>
            </div>
            <div v-else-if="!apps.length" class="empty-state-nextdev">
              <div class="empty-icon-circle">
                <Boxes :size="26" />
              </div>
              <h4>暂无运行中的应用实例</h4>
              <p>从应用市场挑选官方维护的应用模板，一键配置并快速拉起容器实例。</p>
              <RouterLink to="/console/deploy" class="primary compact mt-3">
                <Rocket :size="15" /> 部署首个应用
              </RouterLink>
            </div>
            <div v-else class="overview-app-grid p-4">
              <article v-for="app in apps" :key="app.id" class="overview-app-card">
                <div class="overview-app-title">
                  <span class="app-icon"><AppWindow :size="18" /></span>
                  <div>
                    <strong>{{ instanceLabel(app) }}</strong>
                    <small>{{ app.productSlug }}</small>
                  </div>
                  <span
                    :class="[
                      'status-pill',
                      app.status === 'running' ? 'active' : 'pending',
                    ]"
                  >{{ app.status === "running" ? "运行中" : app.status }}</span>
                </div>
                <div class="runtime-sample-state">
                  <i :class="{ live: metricsAvailable(app) }"></i>
                  {{ metricFreshness(app) }}
                </div>
                <div class="app-resource-bars">
                  <div>
                    <span>CPU 实时占用</span>
                    <b>{{ metricsAvailable(app) ? (app.cpuUsageCores || 0).toFixed(2) : "--" }} / {{ app.cpuCores || 0 }} 核</b>
                    <i>
                      <em
                        :style="{
                          width: metricsAvailable(app)
                            ? Math.min(100, ((app.cpuUsageCores || 0) / Math.max(app.cpuCores || 1, 0.1)) * 100) + '%'
                            : '0%',
                        }"
                      ></em>
                    </i>
                  </div>
                  <div>
                    <span>内存实时占用</span>
                    <b>{{ metricsAvailable(app) ? Math.round(app.memoryUsageMiB || 0) : "--" }} / {{ app.memoryMiB || 0 }} MiB</b>
                    <i>
                      <em
                        :style="{
                          width: metricsAvailable(app)
                            ? Math.min(100, ((app.memoryUsageMiB || 0) / Math.max(app.memoryMiB || 1, 1)) * 100) + '%'
                            : '0%',
                        }"
                      ></em>
                    </i>
                  </div>
                </div>
                <footer>
                  <div>
                    <small>预计每月</small>
                    <strong>¥ {{ ((app.estimatedMonthlyCents || 0) / 100).toFixed(2) }}</strong>
                    <small v-if="!app.estimateComplete">（不含未定价项）</small>
                  </div>
                  <span class="overview-app-access">
                    <template v-if="app.hostPort">
                      <a
                        class="direct-access"
                        :href="directPortURL(fullSettings, app.hostPort)"
                        target="_blank"
                        rel="noopener"
                        @click.stop
                      >直连 :{{ app.hostPort }}<ExternalLink :size="12" /></a>
                    </template>
                    <button
                      v-if="app.publicPath"
                      class="primary compact"
                      @click="visitApp(app)"
                    >
                      访问应用<ExternalLink :size="14" />
                    </button>
                    <span v-else class="quiet">尚无公网地址</span>
                  </span>
                </footer>
              </article>
            </div>
          </div>
        </div>

        <!-- 帮助中心 / 常见问答卡片 -->
        <div v-if="faqs.length" class="nextdev-card p-0">
          <div class="card-header-bar">
            <div class="card-title-group">
              <span class="eyebrow">HELP · 帮助中心</span>
              <h3>常见问答</h3>
            </div>
            <RouterLink class="secondary compact" to="/console/faq">
              查看全部 <ChevronRight :size="14" />
            </RouterLink>
          </div>
          <div class="card-divider"></div>
          <div class="faq-overview-list p-4">
            <article
              v-for="item in faqs.slice(0, 4)"
              :key="item.id"
              class="faq-overview-item"
            >
              <button
                type="button"
                class="faq-overview-question"
                :aria-expanded="openFaq === item.id"
                @click="openFaq = openFaq === item.id ? '' : item.id"
              >
                <CircleHelp :size="17" />
                <strong>{{ item.question }}</strong>
                <ChevronDown
                  :class="{ rotated: openFaq === item.id }"
                  :size="16"
                />
              </button>
              <div v-if="openFaq === item.id" class="faq-overview-answer">
                <p>{{ item.answer }}</p>
              </div>
            </article>
          </div>
        </div>
      </div>

      <!-- 右列 (1/3): 账户计划卡 + 快捷操作 + 平台通知 -->
      <div class="overview-side-col">
        <div class="nextdev-card p-0">
          <div class="card-header-bar">
            <div class="card-title-group">
              <span class="eyebrow">GLOBAL PRICING · 全局定价</span>
              <h3>当前价格</h3>
            </div>
            <BadgeDollarSign :size="18" />
          </div>
          <div class="card-divider"></div>
          <div v-if="globalPriceItems.length" class="global-price-list">
            <div v-for="entry in globalPriceItems" :key="entry.code" class="global-price-row">
              <div>
                <strong>{{ usageCodeLabel(entry.code) }}</strong>
                <small>{{ usageUnitLabel(entry.unit) }}</small>
              </div>
              <span class="mono-data">¥ {{ formatGlobalPrice(entry.unitPriceMicros) }}</span>
            </div>
          </div>
          <p v-else class="quiet global-price-empty">暂无已生效的全局价格</p>
        </div>

        <!-- 账户计划卡片 (NextDevTpl PlanCard) -->
        <div class="nextdev-card p-6">
          <span class="eyebrow">CURRENT PLAN · 账户计划</span>
          <div class="plan-name-title mt-3">按量标准版</div>
          <p class="plan-hint-text">毫秒级精准扣费 · 停机仅保留数据卷</p>
          <div class="plan-balance-stat mono-data mt-4">
            <small>当前可用余额</small>
            <strong>¥ {{ (balance / 100).toFixed(2) }}</strong>
          </div>
          <button
            type="button"
            class="primary w-full mt-4"
            :disabled="
              writeLocked ||
              busy === 'checkin' ||
              !checkin?.enabled ||
              checkin?.checkedInToday
            "
            @click="doCheckin"
          >
            <Gift :size="15" />{{
              !checkin?.enabled
                ? "签到已暂停"
                : checkin?.checkedInToday
                  ? "今日已签到"
                  : "签到"
            }}
          </button>
        </div>

        <!-- 快捷操作卡片 (NextDevTpl QuickActions) -->
        <div class="nextdev-card p-2">
          <div class="card-mini-title">
            <span class="eyebrow">QUICK ACTIONS · 快捷操作</span>
          </div>
          <div class="quick-action-list">
            <RouterLink class="quick-action-row" to="/console/deploy">
              <Rocket :size="16" />
              <span>部署新应用</span>
              <ChevronRight :size="14" class="arrow" />
            </RouterLink>
            <RouterLink class="quick-action-row" to="/console/billing">
              <Coins :size="16" />
              <span>余额与账单明细</span>
              <ChevronRight :size="14" class="arrow" />
            </RouterLink>
            <RouterLink class="quick-action-row" to="/console/tickets">
              <MessageSquareText :size="16" />
              <span>工单与技术支持</span>
              <ChevronRight :size="14" class="arrow" />
            </RouterLink>
            <RouterLink class="quick-action-row" to="/console/usage">
              <Gauge :size="16" />
              <span>资源用量统计</span>
              <ChevronRight :size="14" class="arrow" />
            </RouterLink>
          </div>
        </div>

        <!-- 平台通知与公告 -->
        <div v-if="notifications.length || announcements.length" class="nextdev-card p-4">
          <div class="card-mini-title flex justify-between items-center">
            <span class="eyebrow">NOTICES · 平台通知</span>
            <small class="mono-data text-xs">{{ notifications.filter((item) => !item.readAt).length }} 条未读</small>
          </div>
          <div class="notice-list mt-3">
            <article
              v-for="item in notifications.slice(0, 3)"
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
                <Check :size="14" />
              </button>
            </article>
            <article
              v-for="item in announcements.slice(0, 2)"
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
          </div>
        </div>
      </div>
    </div>
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
      <section class="nextdev-card p-0">
        <div class="card-header-bar flex items-center justify-between">
          <div class="card-title-group">
            <span class="eyebrow">LEDGER · 账本流水</span>
            <h3>账本明细</h3>
          </div>
          <span class="mono-data text-sm">最近 8 条</span>
        </div>
        <div class="card-divider"></div>
        <div class="ledger-list">
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
          <div v-if="!ledger.length" class="card-inner-body" style="min-height:180px;">
            <div class="empty-state-nextdev">
              <div class="empty-icon-circle"><Coins :size="22" /></div>
              <h4>还没有账本记录</h4>
              <p>充值或消费后，账本流水将在此处显示。</p>
            </div>
          </div>
        </div>
      </section>
      <section class="nextdev-card p-0">
        <div class="card-header-bar flex items-center justify-between">
          <div class="card-title-group">
            <span class="eyebrow">BILLS · 月度账单</span>
            <h3>月度账单</h3>
          </div>
          <span class="mono-data text-sm">UTC 自然月</span>
        </div>
        <div class="card-divider"></div>
        <div class="statement-list">
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
          <p v-if="!bills.length" class="quiet empty-copy" style="display:none"></p>
          <div v-if="!bills.length" class="card-inner-body" style="min-height:180px;">
            <div class="empty-state-nextdev">
              <div class="empty-icon-circle"><Receipt :size="22" /></div>
              <h4>当前还没有月度账单</h4>
              <p>每自然月结算一次，有用量费用时自动生成账单。</p>
            </div>
          </div>
        </div>
      </section>
      <section class="nextdev-card p-0">
        <div class="card-header-bar flex items-center justify-between">
          <div class="card-title-group">
            <span class="eyebrow">CREDITS · 赠送额度</span>
            <h3>赠送额度</h3>
          </div>
          <span class="mono-data text-sm">可用 ¥{{ (creditAvailable / 100).toFixed(2) }}</span>
        </div>
        <div class="card-divider"></div>
        <div class="credit-list">
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
          <div v-if="!creditGrants.length" class="card-inner-body" style="min-height:180px;">
            <div class="empty-state-nextdev">
              <div class="empty-icon-circle"><Gift :size="22" /></div>
              <h4>当前没有赠送额度</h4>
              <p>平台赠送的免费额度将在此处显示，可用于抵扣按量费用。</p>
            </div>
          </div>
        </div>
      </section>
      <section class="nextdev-card p-0">
        <div class="card-header-bar flex items-center justify-between">
          <div class="card-title-group">
            <span class="eyebrow">DAILY BILLS · 每日账单</span>
            <h3>每日账单</h3>
          </div>
          <span class="mono-data text-sm">UTC 自然日</span>
        </div>
        <div class="card-divider"></div>
        <div class="statement-list">
          <article v-for="bill in dailyBills" :key="bill.date" class="statement-row">
            <div class="statement-summary" style="padding-right: 20px;">
              <span
                ><strong>{{ bill.date }}</strong
                ><small>{{ bill.itemCount }} 笔用量扣费</small
                ></span
              >
              <strong>¥{{ (bill.totalCents / 100).toFixed(2) }}</strong>
            </div>
          </article>
          <div v-if="!dailyBills.length" class="card-inner-body" style="min-height:180px;">
            <div class="empty-state-nextdev">
              <div class="empty-icon-circle"><Receipt :size="22" /></div>
              <h4>当前还没有每日账单</h4>
              <p>每自然日结算出账一次，有用量费用时自动汇总为每日账单。</p>
            </div>
          </div>
        </div>
      </section>
    </section>
    <section
      v-if="page === 'apps' && appsLoading && !apps.length"
      class="product-list app-skeleton-list"
      aria-busy="true"
    >
      <div class="section-heading">
        <div>
          <p class="eyebrow">我的应用</p>
          <h2>部署任务</h2>
        </div>
        <span class="skeleton skeleton-text" style="width: 64px"></span>
      </div>
      <article v-for="index in 3" :key="index" class="product-row">
        <span
          class="skeleton"
          style="width: 40px; height: 40px; border-radius: 11px"
        ></span>
        <div style="flex: 1; display: grid; gap: 8px">
          <span class="skeleton skeleton-title" style="width: 40%"></span
          ><span class="skeleton skeleton-text" style="width: 60%"></span>
        </div>
        <span class="skeleton skeleton-text" style="width: 88px"></span>
      </article>
    </section>
    <section v-if="page === 'apps'" class="nextdev-card p-0">
      <div class="card-header-bar flex items-center justify-between">
        <div class="card-title-group">
          <span class="eyebrow">INSTANCES · 容器实例</span>
          <h3>我的应用列表</h3>
        </div>
        <RouterLink to="/console/deploy" class="primary compact">
          <Plus :size="14" />
          <span>部署新应用</span>
        </RouterLink>
      </div>
      <div class="card-divider"></div>
      <div v-if="!apps.length && !appsLoading" class="card-inner-body">
        <div class="empty-state-nextdev">
          <div class="empty-icon-circle">
            <Boxes :size="24" />
          </div>
          <h4>暂无部署应用</h4>
          <p>从应用市场挑选官方维护的应用模板，或输入自定义 Docker 镜像一键拉起容器实例。</p>
          <div class="flex items-center gap-3 mt-3">
            <RouterLink class="primary compact" to="/console/deploy">
              <Plus :size="15" />部署首个应用
            </RouterLink>
            <RouterLink class="ghost compact" to="/console">
              <Gauge :size="15" />返回概览
            </RouterLink>
          </div>
        </div>
      </div>
      <div v-else-if="apps.length" class="product-list p-4">
        <article
          v-for="app in apps"
          :key="app.id"
          class="product-row app-product-row"
          role="link"
          tabindex="0"
          @click="router.push('/console/apps/' + (app.instanceId || app.id))"
          @keydown.enter="
            router.push('/console/apps/' + (app.instanceId || app.id))
          "
        >
          <span class="product-icon"><AppWindow :size="20" /></span>
          <div>
            <strong>{{ instanceLabel(app) }}</strong>
            <small
              >{{ app.productSlug }} · {{ appState(app)
              }}<template v-if="app.hostPort">
                · 直连 :{{ app.hostPort }}</template
              ><template v-if="app.containerPort"> · 应用端口 {{ app.containerPort }}</template>
              · 子域名{{ app.domainRefreshDays ? `每 ${app.domainRefreshDays} 天刷新` : "永久不变" }}</small
            >
          </div>
          <div class="product-row-main">
            <div
              v-if="app.status === 'failed' || app.jobLastError"
              class="deployment-error-card"
            >
              <div class="deployment-error-title">
                <CircleAlert :size="16" /><strong>{{
                  app.status === "failed" ? "部署失败" : "部署任务异常"
                }}</strong
                ><button
                  class="secondary compact"
                  type="button"
                  @click="openDeploymentDetails(app)"
                >
                  查看部署详情
                </button>
              </div>
              <p>{{ deploymentErrorHint(app.jobLastError) }}</p>
              <code v-if="app.jobLastError">{{
                deploymentErrorText(app.jobLastError)
              }}</code>
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
            >
              <ExternalLink :size="17" /></button
            ><button
              v-if="app.status === 'running'"
              class="icon-action"
              title="编辑配置并重新部署"
              :disabled="
                writeLocked || busy === app.id || deployConfigurationLoading
              "
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
              <Play :size="17" /></button
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
      </div>
    </section>
    <section v-if="page === 'usage'" class="nextdev-card usage-browser p-0">
      <template v-if="!selectedUsageGroup">
        <div class="card-header-bar flex items-center justify-between">
          <div class="card-title-group">
            <span class="eyebrow">USAGE · 按应用汇总</span>
            <h3>应用用量</h3>
          </div>
          <span class="mono-data text-sm">{{
            activeUsageAppGroups.length + deletedUsageAppGroups.length
          }} 个应用</span>
        </div>
        <div class="card-divider"></div>
        <div v-if="!usageAppGroups.length" class="card-inner-body">
          <div class="empty-state-nextdev">
            <div class="empty-icon-circle">
              <Gauge :size="24" />
            </div>
            <h4>暂无用量记录</h4>
            <p>部署并运行应用后，系统将采集 CPU、内存、存储与网络用量，并在这里按应用归类。</p>
          </div>
        </div>
        <div v-else class="usage-app-sections">
          <section
            v-for="section in usageGroupSections"
            :key="section.key"
            class="usage-app-section"
          >
            <div class="usage-app-section-heading">
              <strong>{{ section.title }}</strong>
              <span>{{ section.groups.length }}</span>
            </div>
            <div class="usage-app-grid">
              <button
                v-for="group in section.groups"
                :key="group.key"
                type="button"
                class="usage-app-card"
                @click="selectedUsageAppKey = group.key"
              >
                <span class="usage-app-icon">
                  <AppWindow v-if="group.appId" :size="21" />
                  <CreditCard v-else :size="21" />
                  <img
                    v-if="group.iconUrl"
                    :src="group.iconUrl"
                    :alt="group.productName + ' 图标'"
                    @error="
                      ($event.currentTarget as HTMLImageElement).style.display =
                        'none'
                    "
                  />
                </span>
                <span class="usage-app-main">
                  <strong>{{ group.appSlug }}</strong>
                  <small>{{ group.productName }}</small>
                </span>
                <span class="usage-app-price">
                  <small>记录内费用</small>
                  <strong>{{ formatUsageAmount(group.totalAmountCents) }}</strong>
                </span>
                <span class="usage-app-meta">
                  {{ group.categoryCount }} 个计费分类 · {{ group.items.length }} 个用量窗口
                  <em v-if="group.deleted">已删除</em>
                </span>
                <ChevronRight class="usage-app-arrow" :size="18" />
              </button>
            </div>
          </section>
        </div>
      </template>

      <template v-else>
        <div class="usage-detail-header">
          <button
            type="button"
            class="usage-back-button"
            @click="selectedUsageAppKey = ''"
          >
            <ChevronLeft :size="17" />返回应用列表
          </button>
          <div class="usage-detail-title">
            <span class="usage-app-icon">
              <AppWindow v-if="selectedUsageGroup.appId" :size="21" />
              <CreditCard v-else :size="21" />
              <img
                v-if="selectedUsageGroup.iconUrl"
                :src="selectedUsageGroup.iconUrl"
                :alt="selectedUsageGroup.productName + ' 图标'"
                @error="
                  ($event.currentTarget as HTMLImageElement).style.display =
                    'none'
                "
              />
            </span>
            <div>
              <span class="eyebrow">PRICE DETAIL · 分类价格</span>
              <h3>{{ selectedUsageGroup.appSlug }}</h3>
              <p>{{ selectedUsageGroup.productName }} · 最近 100 条账户用量中的应用明细</p>
            </div>
          </div>
          <div class="usage-detail-total">
            <small>记录内费用</small>
            <strong>{{ formatUsageAmount(selectedUsageGroup.totalAmountCents) }}</strong>
          </div>
        </div>
        <div class="card-divider"></div>
        <div v-if="!selectedUsageCategories.length" class="card-inner-body">
          <div class="empty-state-nextdev usage-detail-empty">
            <div class="empty-icon-circle">
              <Gauge :size="24" />
            </div>
            <h4>该应用暂无用量记录</h4>
            <p>应用开始运行并产生资源用量后，各计费分类和价格会显示在这里。</p>
          </div>
        </div>
        <div v-else class="usage-category-list">
          <article
            v-for="(category, categoryIndex) in selectedUsageCategories"
            :key="category.key"
            class="usage-category-row"
          >
            <span class="usage-category-index mono-data">
              {{ String(categoryIndex + 1).padStart(2, "0") }}
            </span>
            <div class="usage-category-main">
              <strong>{{ usageCodeLabel(category.usageCode) }}</strong>
              <code>{{ category.usageCode }}</code>
              <small>
                {{ formatUsageQuantity(category.quantity) }}
                {{ usageUnitLabel(category.unit) }} · {{ category.records.length }} 个窗口 ·
                最近 {{ new Date(category.latestAt).toLocaleString() }}
              </small>
            </div>
            <div class="usage-category-price">
              <small>计费单价</small>
              <strong>{{ formatUsageUnitPrice(category.unitPrices, category.unit) }}</strong>
            </div>
            <div class="usage-category-total">
              <small>分类合计</small>
              <strong>{{ formatUsageAmount(category.amountCents) }}</strong>
              <span
                class="status-pill"
                :class="category.allSettled ? 'active' : 'suspended'"
              >{{
                category.allSettled ? "已结算" : "待结算"
              }}</span>
            </div>
          </article>
        </div>
      </template>
    </section>
    <section v-if="page === 'deploy'" class="nextdev-card p-0">
      <div class="card-header-bar flex items-center justify-between">
        <div class="card-title-group">
          <span class="eyebrow">CATALOG · 应用模板</span>
          <h3>产品目录</h3>
        </div>
        <span class="mono-data text-sm">{{
          productCatalog.reduce(
            (sum, group) =>
              sum + group.versions.filter((v) => v.deployable).length,
            0,
          )
        }} 个可部署版本</span>
      </div>
      <div class="card-divider"></div>
      <div
        v-if="productsLoading && !productCatalog.length"
        class="skeleton-list p-4"
        aria-busy="true"
      >
        <article
          v-for="index in 3"
          :key="index"
          class="product-row skeleton-row"
        >
          <span
            class="skeleton"
            style="width: 40px; height: 40px; border-radius: 11px"
          ></span>
          <div style="flex: 1; display: grid; gap: 8px">
            <span class="skeleton skeleton-title" style="width: 36%"></span
            ><span class="skeleton skeleton-text" style="width: 56%"></span>
          </div>
          <span class="skeleton skeleton-text" style="width: 96px"></span>
        </article>
      </div>
      <div v-else class="product-list">
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
          <span class="product-icon"
            ><AppWindow :size="20" /><img
              v-if="group.iconUrl"
              :src="group.iconUrl"
              alt=""
              @error="
                ($event.currentTarget as HTMLImageElement).style.display =
                  'none'
              "
          /></span>
          <div>
            <strong>{{ group.name }}</strong
            ><small
              >{{ group.slug
              }}<template v-if="group.versions.length">
                · {{ group.versions.length }} 个已发布版本</template
              ><template v-else> · 暂无已发布版本</template></small
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
        <div v-if="!productCatalog.length" class="card-inner-body">
          <div class="empty-state-nextdev">
            <div class="empty-icon-circle">
              <AppWindow :size="24" />
            </div>
            <h4>暂无可用应用模板</h4>
            <p>管理员尚未发布任何产品模板，请联系管理员在产品管理中添加并发布产品。</p>
          </div>
        </div>
      </div>
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
              <button
                v-for="method in paymentMethods"
                :key="method.type"
                type="button"
                :class="[
                  'payment-method',
                  { active: selectedPaymentType === method.type },
                ]"
                @click="selectedPaymentType = method.type"
              >
                <CreditCard :size="19" /><span
                  ><strong>{{ method.name }}</strong
                  ><small
                    >最低充值 ¥{{
                      (method.minAmountCents / 100).toFixed(2)
                    }}</small
                  ></span
                ><Check v-if="selectedPaymentType === method.type" :size="16" />
              </button>
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
                !selectedPaymentType ||
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
  </main>
  <Transition name="modal-pop"
    ><div
      v-if="pickedProduct"
      class="modal-backdrop"
      @click.self="pickedProduct = null"
    >
      <section class="secret-dialog deploy-dialog version-picker-dialog">
        <header>
          <div>
            <p class="eyebrow">{{ pickedProduct.slug }}</p>
            <h2>{{ pickedProduct.name }}</h2>
          </div>
          <button
            class="icon-action"
            title="关闭"
            @click="pickedProduct = null"
          >
            <X :size="18" />
          </button>
        </header>
        <div v-if="pickedProduct.versions.length" class="version-picker-list">
          <article
            v-for="version in pickedProduct.versions"
            :key="version.versionId"
            :class="[
              'version-picker-row',
              { unavailable: !version.deployable },
            ]"
          >
            <div>
              <strong>{{
                version.versionLabel || "版本 v" + version.version
              }}</strong
              ><small
                >v{{ version.version
                }}<template v-if="version.missingDependencies?.length">
                  · 缺少 {{ version.missingDependencies.join("、") }}</template
                ><template v-else-if="!version.deployable">
                  · 当前套餐未包含</template
                ></small
              >
            </div>
            <button
              class="primary compact"
              :disabled="writeLocked || !version.deployable"
              @click="pickVersion(pickedProduct, version)"
            >
              <Rocket :size="15" />部署
            </button>
          </article>
        </div>
        <div v-else class="context-empty compact-empty">
          <AppWindow :size="22" />
          <p>该应用暂无已发布版本，请联系管理员。</p>
        </div>
      </section>
    </div></Transition
  >
  <Transition name="modal-pop"
    ><div v-if="deployProduct" class="modal-backdrop" @click.self="closeDeploy">
      <section class="secret-dialog deploy-dialog">
        <header>
          <div>
            <p class="eyebrow">
              {{ deployMode === "update" ? "编辑配置 · 重新部署" : "部署应用" }}
            </p>
            <h2>
              {{ deployProduct.name
              }}<small v-if="deployMode === 'update'">
                · {{ deploySlug }}</small
              >
            </h2>
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
                :readonly="
                  deployProduct.runtimeSpec?.editableOptions?.cpu === false
                "
              /><small
                >最低 {{ deployProduct.runtimeSpec?.cpuCores || 1 }} 核{{
                  deployProduct.runtimeSpec?.editableOptions?.cpu === false
                    ? "，管理员已固定"
                    : "，可向上调整"
                }}</small
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
                :readonly="
                  deployProduct.runtimeSpec?.editableOptions?.memory === false
                "
              /><small
                >最低 {{ deployProduct.runtimeSpec?.memoryMiB || 512 }} MiB{{
                  deployProduct.runtimeSpec?.editableOptions?.memory === false
                    ? "，管理员已固定"
                    : "，可向上调整"
                }}</small
              ></label
            >
            <section class="deploy-network-section">
              <div class="deploy-listeners">
                <div class="deploy-section-heading">
                  <div><strong>应用监听端口</strong><small>配置容器端口及需要开放的直连入口</small></div>
                </div>
                <div v-for="listener in deployListenerPorts" :key="listener.key" class="deploy-listener-row">
                  <div><strong>{{ listener.remark || listener.key }}</strong><small>{{ listener.primary ? "域名网关主入口" : listener.key }}</small></div>
                  <input v-model.number="listener.containerPort" type="number" min="1" max="65535" :readonly="!listener.userEditable" />
                  <div v-if="listener.mappingAvailable" class="listener-map-control"><span>端口直连</span><label class="switch" title="端口直连"><input v-model="listener.mappingEnabled" type="checkbox" /><span /></label></div>
                  <span v-else class="listener-internal-only">仅内网</span>
                </div>
                <small class="deploy-section-note">每个直连端口由系统独立分配宿主机端口；直连不会经过域名密码保护。</small>
              </div>
              <div v-if="deployMode === 'create'" class="deploy-domain-settings">
                <label
                  >独立子域名刷新方式<select v-model="deployDomainPermanent">
                    <option :value="true">永久不变</option>
                    <option :value="false">按天自动刷新</option>
                  </select><small
                    >每个应用使用独立子域名；刷新后旧地址立即失效</small
                  ></label
                >
                <label v-if="!deployDomainPermanent"
                  >刷新周期（天）<input
                    v-model.number="deployDomainRefreshDays"
                    type="number"
                    min="1"
                    step="1"
                    required
                  /><small>最低 1 天；如需长期固定，请选择“永久不变”</small></label
                >
              </div>
            </section>
            <section class="deploy-access-section">
              <div class="switch-setting deploy-password-access">
                <div>
                  <strong>密码访问</strong>
                  <small>仅保护应用域名入口；端口直连仍可同时使用，且不会经过此密码验证</small>
                </div>
                <label class="switch"><input v-model="deployPasswordAccess" type="checkbox" /><span /></label>
              </div>
              <div v-if="deployPasswordAccess" class="deploy-access-grid">
                <label class="deploy-access-field">访问用户名<input v-model="deployAccessUsername" maxlength="64" autocomplete="off" required /><small>用于浏览器 HTTP Basic Auth 登录</small></label>
                <label class="deploy-access-field">访问密码<input v-model="deployAccessPassword" type="password" :required="!deployAccessPasswordConfigured" :placeholder="deployAccessPasswordConfigured ? '已配置，留空保持不变' : '至少 8 个字符'" autocomplete="new-password" /><small>仅保存加密哈希，不会显示或返回原密码</small></label>
              </div>
            </section>
          </div>
          <label v-if="deployProduct.runtimeSpec?.volumes?.length"
            >共享数据卷容量 GiB<input
              v-model.number="deployDataVolumeGiB"
              type="number"
              :min="
                deployVolumeFloorGiB ||
                deployProduct.runtimeSpec?.dataVolumeGiB ||
                Math.max(
                  ...(deployProduct.runtimeSpec?.volumes || []).map(
                    (volume) => volume.sizeGiB,
                  ),
                )
              "
              max="16384"
              step="1"
              required
              :readonly="
                deployProduct.runtimeSpec?.editableOptions?.dataVolume === false
              "
            /><small
              >全部挂载（{{
                (deployProduct.runtimeSpec?.volumes || [])
                  .map((volume) => volume.mountPath)
                  .join("、")
              }}）和成功备份共享同一容量，{{
                deployMode === "update" ? "当前配置只允许扩容，最低" : "最低"
              }}
              {{
                deployVolumeFloorGiB ||
                deployProduct.runtimeSpec?.dataVolumeGiB ||
                deployDataVolumeGiB
              }}
              GiB，只计费一次</small
            ></label
          >
          <label v-if="deployProduct.runtimeSpec?.editableOptions?.command" class="wide-field"
            ><span>启动命令</span
            ><textarea
              v-model="deployCommand"
              placeholder="留空使用镜像默认命令"
              autocomplete="off"
              rows="2"
            ></textarea><small
              >按空格拆分参数；复杂参数建议由镜像入口脚本处理</small
            ></label
          >
          <div
            v-if="
              deployProduct.runtimeSpec?.editableOptions?.environment ===
                true &&
              (deployProduct.runtimeSpec?.editableEnvKeys?.length || 0)
            "
            class="deploy-custom-list"
          >
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
              <small class="env-help">{{
                deployProduct.runtimeSpec?.envDescriptions?.[item.key] ||
                "管理员未提供说明"
              }}</small>
            </div>
          </div>
          <div
            v-if="deployProduct.runtimeSpec?.editableOptions?.dependencies"
            class="deploy-dependencies"
          >
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
                <strong>{{
                  deployMode === "update" ? "Secret 配置" : "部署 Secret"
                }}</strong
                ><small v-if="deployMode === 'create'"
                  >加密保存，提交后不会再次显示</small
                ><small v-else
                  >明文不会回显；留空表示继续使用当前版本，只有管理员开放的字段可修改</small
                >
              </div>
            </div>
            <template v-if="deployMode === 'create'">
              <label
                v-for="option in deploySecretOptions"
                :key="option.key"
                class="deploy-secret-field"
              >
                {{ option.key }}
                <small v-if="option.description" class="env-help">{{
                  option.description
                }}</small>
                <input
                  v-model="deploySecrets[option.key]"
                  type="password"
                  required
                  autocomplete="new-password"
                  :placeholder="option.description || '请输入 Secret 值'"
                />
              </label>
            </template>
            <div v-else class="deploy-secret-update-list">
              <template v-for="option in deploySecretOptions" :key="option.key">
                <label v-if="option.editable" class="deploy-secret-field">
                  {{ option.key }}
                  <small v-if="option.description" class="env-help">{{
                    option.description
                  }}</small>
                  <input
                    v-model="deploySecrets[option.key]"
                    type="password"
                    autocomplete="new-password"
                    :placeholder="
                      deployConfiguredSecretKeys.includes(option.key)
                        ? '留空继续使用当前版本'
                        : '尚未配置，必须填写'
                    "
                    :required="!deployConfiguredSecretKeys.includes(option.key)"
                  />
                </label>
                <div v-else class="deploy-secret-locked">
                  <KeyRound :size="15" />
                  <div>
                    <strong>{{ option.key }}</strong
                    ><small>{{
                      option.description || "管理员未提供说明"
                    }}</small>
                  </div>
                  <span>仅管理员</span>
                </div>
              </template>
              <p
                v-if="missingDeploySecretKeys.length"
                class="deploy-secret-warning"
              >
                尚未配置：{{ missingDeploySecretKeys.join("、") }}。固定 Secret
                需要管理员先配置；可编辑 Secret 可在上方填写。
              </p>
            </div>
          </template>
          <p v-else class="quiet">此模板不需要额外 Secret。</p>
          <div class="deploy-price-prediction" style="margin-bottom: 1.5rem; display: flex; justify-content: flex-end; align-items: baseline; gap: 8px;">
            <span style="color: var(--color-foreground-muted); font-size: 0.85rem;">配置价格预测（按自然月计）：</span>
            <strong style="color: var(--color-foreground); font-size: 1.1rem; font-weight: 600;">¥ {{ deployPricePrediction }}</strong>
          </div>
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
              <Rocket :size="16" />{{
                deployMode === "update" ? "保存配置并重新部署" : "创建部署"
              }}
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
        <p>
          <strong>应用 Secret 用于 API Key、令牌和数据库密码等敏感值。</strong
          ><span
            >平台加密保存且永不回显；每次修改创建新版本。正在运行的容器继续使用当前发布固定的旧版本，执行“编辑配置并重新部署”后才会注入最新版本。</span
          >
        </p>
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
  <div
    v-if="deploymentApp"
    class="modal-backdrop"
    @click.self="deploymentApp = null"
  >
    <section class="secret-dialog deployment-dialog">
      <header>
        <div>
          <p class="eyebrow">{{ deploymentApp.slug }}</p>
          <h2>部署详情</h2>
        </div>
        <button class="icon-action" title="关闭" @click="deploymentApp = null">
          <X :size="18" />
        </button>
      </header>
      <p v-if="deploymentBusy" class="quiet">正在读取部署事件…</p>
      <p v-else-if="!deploymentJobs.length" class="quiet">暂无部署任务记录</p>
      <div v-for="job in deploymentJobs" :key="job.id" class="deployment-job">
        <div class="deployment-job-heading">
          <span
            :class="[
              'status-pill',
              job.state === 'succeeded'
                ? 'active'
                : job.state === 'failed'
                  ? 'danger'
                  : 'pending',
            ]"
            >{{ state(job.state) }}</span
          >
          <small
            >{{ new Date(job.updatedAt || job.createdAt).toLocaleString() }} ·
            尝试 {{ job.attempts }} 次</small
          >
        </div>
        <p v-if="job.lastError" class="deployment-detail-hint">
          {{ deploymentErrorHint(job.lastError) }}
        </p>
        <code v-if="job.lastError" class="deployment-raw-error">{{
          deploymentErrorText(job.lastError)
        }}</code>
        <ol v-if="job.events.length" class="deployment-timeline">
          <li v-for="event in job.events" :key="event.id || event.createdAt">
            <span>{{ event.toState ? state(event.toState) : "事件" }}</span>
            <small
              >{{ event.message || "状态已更新" }} ·
              {{
                event.createdAt
                  ? new Date(event.createdAt).toLocaleString()
                  : ""
              }}</small
            >
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
          <small v-if="logData.sampledAt"
            >采样于 {{ new Date(logData.sampledAt).toLocaleString() }} · 每 3
            秒自动刷新</small
          >
        </div>
        <button class="icon-action" title="关闭" @click="closeLogs">
          <X :size="18" />
        </button>
      </header>
      <div class="log-toolbar">
        <span
          :class="[
            'status-pill',
            logData.status === 'succeeded' || logData.status === 'cached'
              ? 'active'
              : logData.status === 'failed'
                ? 'danger'
                : 'pending',
          ]"
          >{{ logStatusLabel(logData.status) }}</span
        >
        <button
          class="secondary compact"
          :disabled="logBusy"
          @click="refreshLogs"
        >
          <RefreshCw :class="{ spin: logBusy }" :size="15" />立即刷新
        </button>
      </div>
      <p v-if="logData.lastError" class="message">{{ logData.lastError }}</p>
      <p v-if="!logData.logs && !logBusy" class="quiet log-empty">
        还没有可显示的日志，点击“拉取最新日志”获取容器运行日志。
      </p>
      <pre class="log-viewer" :class="{ 'log-loading': logBusy }">{{
        logData.logs || (logBusy ? "正在拉取日志…" : "")
      }}</pre>
    </section>
  </div>
</template>
