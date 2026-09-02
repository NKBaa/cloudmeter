<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  Activity,
  AppWindow,
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  FileCheck2,
  Globe2,
  KeyRound,
  Link2,
  ListChecks,
  LockKeyhole,
  Network,
  RefreshCw,
  Save,
  Server,
  ShieldCheck,
  TerminalSquare,
  Upload,
  X,
  Zap,
} from "@lucide/vue";
import { api } from "../api";

type CaddyUpstream = {
  address: string;
  numRequests: number;
  fails: number;
};

type CaddyOverview = {
  connected: boolean;
  latencyMs: number;
  serverCount: number;
  routeCount: number;
  proxyCount: number;
  listeners: string[];
  tlsMode: "automatic" | "disabled";
  accessLogEnabled: boolean;
  upstreams: CaddyUpstream[];
  sourceAvailable: boolean;
  sourceDigest: string;
  sourceModifiedAt?: string;
  sourceInSync?: boolean;
  checkedAt: string;
  statusMessage: string;
};

type GatewaySettings = {
  accessMode: "all_caddy" | "apps_only";
  serverUrl: string;
  appBaseDomain: string;
  portMappingHost: string;
  standalonePort: number;
  tlsEnabled: boolean;
  httpPolicy: "redirect" | "allow" | "https_only";
  hstsEnabled: boolean;
  http3Enabled: boolean;
  consoleCertificateMode: "automatic" | "imported";
  appCertificateMode: "automatic" | "imported";
  acmeEmail: string;
  acmeCa: string;
  acmeKeyType: "ed25519" | "p256" | "p384" | "rsa2048" | "rsa4096";
  acmeDnsProvider: "" | "cloudflare" | "alidns" | "tencentcloud" | "route53" | "digitalocean";
  acmeDnsConfigured: boolean;
  renewIntervalMinutes: number;
  accessModeManagedByEnvironment: boolean;
  updatedAt?: string;
};

type GatewayCertificate = {
  target: "console" | "applications";
  commonName: string;
  dnsNames: string[];
  issuer: string;
  notBefore: string;
  notAfter: string;
  fingerprintSha256: string;
  updatedAt: string;
};

type GatewayRoute = {
  id: string;
  instanceId: string;
  slug: string;
  serviceSlug: string;
  status: string;
  ownerSlug: string;
  ownerEmail: string;
  publicPath: string;
  publicUrl: string;
  host: string;
  upstream: string;
  upstreamPort: number;
  hostPort: number;
  domainRefreshDays?: number | null;
  domainNextRefreshAt?: string;
  updatedAt: string;
};

const form = ref<GatewaySettings>({
  accessMode: "apps_only",
  serverUrl: "http://127.0.0.1:8080",
  appBaseDomain: "",
  portMappingHost: "",
  standalonePort: 8080,
  tlsEnabled: false,
  httpPolicy: "redirect",
  hstsEnabled: false,
  http3Enabled: true,
  consoleCertificateMode: "automatic",
  appCertificateMode: "automatic",
  acmeEmail: "",
  acmeCa: "https://acme-v02.api.letsencrypt.org/directory",
  acmeKeyType: "p256",
  acmeDnsProvider: "",
  acmeDnsConfigured: false,
  renewIntervalMinutes: 10,
  accessModeManagedByEnvironment: false,
});
type DNSCredentialField = { key: string; label: string; placeholder: string; secret?: boolean };
const dnsProviderPresets: Array<{ id: GatewaySettings["acmeDnsProvider"]; label: string; detail: string; fields: DNSCredentialField[] }> = [
  { id: "cloudflare", label: "Cloudflare", detail: "API Token（Zone:Read + DNS:Edit）", fields: [
    { key: "apiToken", label: "API Token", placeholder: "Cloudflare scoped API token", secret: true },
  ] },
  { id: "alidns", label: "阿里云 DNS", detail: "AliDNS AccessKey", fields: [
    { key: "accessKeyId", label: "AccessKey ID", placeholder: "LTAI..." },
    { key: "accessKeySecret", label: "AccessKey Secret", placeholder: "输入 AccessKey Secret", secret: true },
  ] },
  { id: "tencentcloud", label: "腾讯云 DNSPod", detail: "腾讯云 API 密钥", fields: [
    { key: "secretId", label: "SecretId", placeholder: "AKID..." },
    { key: "secretKey", label: "SecretKey", placeholder: "输入 SecretKey", secret: true },
  ] },
  { id: "route53", label: "AWS Route 53", detail: "IAM Access Key", fields: [
    { key: "accessKeyId", label: "Access Key ID", placeholder: "AKIA..." },
    { key: "secretAccessKey", label: "Secret Access Key", placeholder: "输入 Secret Access Key", secret: true },
    { key: "region", label: "Region", placeholder: "us-east-1" },
  ] },
  { id: "digitalocean", label: "DigitalOcean", detail: "DNS API Token", fields: [
    { key: "authToken", label: "API Token", placeholder: "DigitalOcean API token", secret: true },
  ] },
];
const dnsCredentials = ref<Record<string, string>>({});
const activeDNSProvider = computed(() => dnsProviderPresets.find((item) => item.id === form.value.acmeDnsProvider));
function selectDNSProvider() {
  dnsCredentials.value = {};
  form.value.acmeDnsConfigured = false;
}
const activePanel = ref<"overview" | "websites" | "certificates" | "routes" | "runtime">("overview");
const panels = [
  { id: "overview", label: "概览", detail: "入口与健康状态" },
  { id: "websites", label: "网站", detail: "域名与代理策略" },
  { id: "certificates", label: "证书", detail: "申请、导入与续期" },
  { id: "routes", label: "应用路由", detail: "公网入口映射" },
  { id: "runtime", label: "运行状态", detail: "Caddy 配置生命周期" },
] as const;
const certificates = ref<GatewayCertificate[]>([]);
const routes = ref<GatewayRoute[]>([]);
const certificateTarget = ref<"console" | "applications" | "">("");
const certificateForm = ref({ certificatePem: "", privateKeyPem: "" });
const importingCertificate = ref(false);

const loading = ref(false);
const saving = ref(false);
const message = ref("");
const error = ref("");
const updatedAt = ref("");
const runtimeLoading = ref(false);
const operation = ref("");
const operationMessage = ref("");
const runtime = ref<CaddyOverview>({
  connected: false,
  latencyMs: 0,
  serverCount: 0,
  routeCount: 0,
  proxyCount: 0,
  listeners: [],
  tlsMode: "disabled",
  accessLogEnabled: false,
  upstreams: [],
  sourceAvailable: false,
  sourceDigest: "",
  checkedAt: "",
  statusMessage: "正在读取 Caddy 运行状态",
});

const consoleHost = computed(() => {
  try {
    return new URL(form.value.serverUrl).host || "未配置";
  } catch {
    return form.value.serverUrl.trim() || "未配置";
  }
});
const appWildcard = computed(() =>
  form.value.appBaseDomain.trim()
    ? `*.${form.value.appBaseDomain.trim().replace(/^\*\./, "")}`
    : "未配置",
);
const appExample = computed(() =>
  form.value.appBaseDomain.trim()
    ? `app-user.${form.value.appBaseDomain.trim().replace(/^\*\./, "")}`
    : "等待配置",
);
const usesHttps = computed(() => form.value.tlsEnabled);
const consoleCertificate = computed(() =>
  certificates.value.find((item) => item.target === "console"),
);
const appCertificate = computed(() =>
  certificates.value.find((item) => item.target === "applications"),
);
const modeLabel = computed(() =>
  form.value.accessMode === "all_caddy" ? "全站 Caddy" : "外部反代控制台",
);
const tlsModeLabel = computed(() =>
  form.value.tlsEnabled ? "Caddy 托管 HTTPS" : "当前仅 HTTP",
);
const healthyUpstreams = computed(
  () => runtime.value.upstreams.filter((item) => item.fails === 0).length,
);
const sourceModifiedText = computed(() =>
  runtime.value.sourceModifiedAt
    ? new Date(runtime.value.sourceModifiedAt).toLocaleString("zh-CN")
    : "—",
);
const automaticCertificateCount = computed(() => {
  let count = 0;
  if (form.value.tlsEnabled && form.value.accessMode === "all_caddy" && form.value.consoleCertificateMode === "automatic") count += 1;
  if (form.value.tlsEnabled && form.value.appBaseDomain && form.value.appCertificateMode === "automatic") count += 1;
  return count;
});
const wizardOpen = ref(false);
const wizardStep = ref(0);
const wizardError = ref("");
const wizardSteps = [
  { label: "接入模式", detail: "确定控制台入口" },
  { label: "域名", detail: "配置公开地址" },
  { label: "HTTPS 与 DNS", detail: "配置自动证书" },
  { label: "确认应用", detail: "检查并保存" },
] as const;
const needsAutomaticCertificate = computed(() => automaticCertificateCount.value > 0);
const dnsProviderLabel = computed(() => activeDNSProvider.value?.label || "未选择");

function openWizard() {
  wizardStep.value = 0;
  wizardError.value = "";
  wizardOpen.value = true;
}

function closeWizard() {
  if (saving.value) return;
  wizardOpen.value = false;
  wizardError.value = "";
}

function validateWizardStep(step = wizardStep.value) {
  wizardError.value = "";
  if (step === 1) {
    try {
      const url = new URL(form.value.serverUrl.trim());
      if (!['http:', 'https:'].includes(url.protocol)) throw new Error();
    } catch {
      wizardError.value = "请输入包含 http:// 或 https:// 的有效控制台地址。";
      return false;
    }
    const domain = form.value.appBaseDomain.trim().replace(/^\*\./, "");
    if (!domain || domain.includes("/") || domain.includes("://") || !domain.includes(".")) {
      wizardError.value = "请输入有效的应用泛子域名，例如 apps.example.com。";
      return false;
    }
    form.value.appBaseDomain = domain;
  }
  if (step === 2 && needsAutomaticCertificate.value) {
    if (!/^\S+@\S+\.\S+$/.test(form.value.acmeEmail.trim())) {
      wizardError.value = "自动申请证书需要有效的 ACME 联系邮箱。";
      return false;
    }
    if (!activeDNSProvider.value) {
      wizardError.value = "请选择 DNS 服务商以完成 DNS-01 验证。";
      return false;
    }
    const missingCredential = activeDNSProvider.value.fields.some(
      (field) => !dnsCredentials.value[field.key]?.trim(),
    );
    if (!form.value.acmeDnsConfigured && missingCredential) {
      wizardError.value = `请完整填写 ${activeDNSProvider.value.label} 的 DNS API 凭据。`;
      return false;
    }
  }
  return true;
}

function nextWizardStep() {
  if (!validateWizardStep()) return;
  wizardStep.value = Math.min(wizardStep.value + 1, wizardSteps.length - 1);
}

function previousWizardStep() {
  wizardError.value = "";
  wizardStep.value = Math.max(wizardStep.value - 1, 0);
}

function certificateState(target: "console" | "applications") {
  if (!form.value.tlsEnabled) return "HTTPS 未启用";
  if (target === "console" && form.value.accessMode === "apps_only") return "不由 Caddy 管理";
  const mode = target === "console" ? form.value.consoleCertificateMode : form.value.appCertificateMode;
  if (mode === "imported") {
    const certificate = target === "console" ? consoleCertificate.value : appCertificate.value;
    return certificate ? "已导入 · 需人工更新" : "等待导入";
  }
  return target === "applications" ? "按首次 HTTPS 访问申请 · 后台自动续期" : "保存后自动申请 · 后台自动续期";
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const res = await api<{ settings: GatewaySettings; certificates: GatewayCertificate[] }>(
      "/admin/caddy/settings",
    );
    Object.assign(form.value, res.settings);
    if (form.value.accessMode === "apps_only" && !form.value.serverUrl) {
      form.value.serverUrl = `${window.location.protocol}//${window.location.hostname}:${form.value.standalonePort}`;
    }
    certificates.value = res.certificates || [];
    if (res.settings.updatedAt)
      updatedAt.value = new Date(res.settings.updatedAt).toLocaleString("zh-CN");
  } catch (err: any) {
    error.value = err.message || "加载网站设置失败";
  } finally {
    loading.value = false;
  }
}

async function loadRoutes() {
  try {
    const result = await api<{ routes: GatewayRoute[] }>("/admin/caddy/routes");
    routes.value = result.routes || [];
  } catch (err: any) {
    error.value = err.message || "加载应用路由失败";
  }
}

async function loadRuntime() {
  runtimeLoading.value = true;
  try {
    runtime.value = await api<CaddyOverview>("/admin/caddy/overview");
  } catch (err: any) {
    runtime.value.connected = false;
    runtime.value.statusMessage = err.message || "读取 Caddy 状态失败";
  } finally {
    runtimeLoading.value = false;
  }
}

async function refreshAll() {
  await Promise.all([load(), loadRuntime(), loadRoutes()]);
}

async function validateConfig() {
  operation.value = "validate";
  error.value = "";
  operationMessage.value = "";
  try {
    const result = await api<{ valid: boolean; digest: string; latencyMs: number }>(
      "/admin/caddy/validate",
      { method: "POST", body: "{}" },
    );
    operationMessage.value = `配置校验通过 · ${result.digest} · ${result.latencyMs} ms`;
  } catch (err: any) {
    error.value = err.message || "Caddyfile 校验失败";
  } finally {
    operation.value = "";
  }
}

async function reloadConfig() {
  operation.value = "reload";
  error.value = "";
  operationMessage.value = "";
  try {
    const result = await api<{ reloaded: boolean; digest: string }>(
      "/admin/caddy/reload",
      { method: "POST", body: "{}" },
    );
    operationMessage.value = `配置已无中断重载 · ${result.digest}`;
    await loadRuntime();
  } catch (err: any) {
    error.value = err.message || "Caddy 配置重载失败";
  } finally {
    operation.value = "";
  }
}

async function save(): Promise<{ ok: true } | { ok: false; message: string }> {
  saving.value = true;
  error.value = "";
  message.value = "";
  try {
    const res = await api<{
      settings: GatewaySettings;
      certificates: GatewayCertificate[];
      reloaded: boolean;
    }>("/admin/caddy/settings", {
      method: "PUT",
      body: JSON.stringify({
        ...form.value,
        serverUrl: form.value.serverUrl.trim(),
        appBaseDomain: form.value.appBaseDomain.trim(),
        acmeEmail: form.value.acmeEmail.trim(),
        acmeCa: form.value.acmeCa.trim(),
        acmeDnsCredentials: Object.values(dnsCredentials.value).some((value) => value.trim())
          ? dnsCredentials.value
          : undefined,
      }),
    });
    Object.assign(form.value, res.settings);
    dnsCredentials.value = {};
    certificates.value = res.certificates || [];
    updatedAt.value = new Date(res.settings.updatedAt || Date.now()).toLocaleString("zh-CN");
    message.value = "网站入口已保存并完成 Caddy 无中断重载";
    await Promise.all([loadRuntime(), loadRoutes()]);
    window.setTimeout(() => {
      message.value = "";
    }, 3000);
    return { ok: true };
  } catch (err: any) {
    const failureMessage = err.message || "保存网站设置失败";
    error.value = failureMessage;
    return { ok: false, message: failureMessage };
  } finally {
    saving.value = false;
  }
}

async function finishWizard() {
  if (!validateWizardStep(1) || !validateWizardStep(2)) return;
  wizardError.value = "";
  const result = await save();
  if (!result.ok) {
    wizardError.value = result.message;
    return;
  }
  wizardStep.value = 0;
  wizardOpen.value = false;
}

function openCertificateImport(target: "console" | "applications") {
  certificateTarget.value = target;
  certificateForm.value = { certificatePem: "", privateKeyPem: "" };
}

async function importCertificate() {
  if (!certificateTarget.value) return;
  importingCertificate.value = true;
  error.value = "";
  try {
    const result = await api<{ certificates: GatewayCertificate[] }>(
      "/admin/caddy/certificates/import",
      {
        method: "POST",
        body: JSON.stringify({ target: certificateTarget.value, ...certificateForm.value }),
      },
    );
    certificates.value = result.certificates || [];
    if (certificateTarget.value === "console") form.value.consoleCertificateMode = "imported";
    else form.value.appCertificateMode = "imported";
    certificateTarget.value = "";
    certificateForm.value = { certificatePem: "", privateKeyPem: "" };
    message.value = "证书已加密保存并应用到 Caddy";
    await loadRuntime();
  } catch (err: any) {
    error.value = err.message || "导入证书失败";
  } finally {
    importingCertificate.value = false;
  }
}

function certificateExpiry(certificate?: GatewayCertificate) {
  if (!certificate) return "未导入";
  return new Date(certificate.notAfter).toLocaleDateString("zh-CN");
}

onMounted(refreshAll);
</script>

<template>
  <main class="workspace admin-workspace website-settings-view">
    <header class="website-page-heading">
      <div>
        <p class="eyebrow">CADDY · 网站入口</p>
        <h1>网站设置</h1>
        <p class="quiet">管理控制台域名、应用泛子域名和 Caddy 反向代理入口。</p>
      </div>
      <div class="website-actions">
        <button class="secondary compact" :disabled="loading || saving" @click="openWizard">
          <ListChecks :size="16" />设置向导
        </button>
        <button class="secondary compact" :disabled="loading || runtimeLoading || saving" @click="refreshAll">
          <RefreshCw :class="{ spin: runtimeLoading }" :size="16" />刷新状态
        </button>
        <button class="primary compact" :disabled="loading || saving" @click="save">
          <Save :size="16" />{{ saving ? "保存中..." : "保存入口" }}
        </button>
      </div>
    </header>

    <p v-if="error" class="message sticky-message">{{ error }}</p>
    <p v-if="message" class="status-ok sticky-message website-message">
      <CheckCircle2 :size="16" />{{ message }}
    </p>
    <p v-if="operationMessage" class="status-ok sticky-message website-message">
      <FileCheck2 :size="16" />{{ operationMessage }}
    </p>

    <div v-if="loading" class="website-loading" aria-busy="true">
      <span class="skeleton skeleton-title"></span>
      <span class="skeleton skeleton-text"></span>
      <span class="skeleton skeleton-text short"></span>
    </div>

    <template v-else>
      <nav class="website-subnav" aria-label="网站设置子界面">
        <button
          v-for="panel in panels"
          :key="panel.id"
          :class="{ active: activePanel === panel.id }"
          type="button"
          @click="activePanel = panel.id"
        >
          <strong>{{ panel.label }}</strong>
          <small>{{ panel.detail }}</small>
        </button>
      </nav>

      <section v-if="activePanel === 'websites'" class="access-mode-switch" aria-label="访问模式">
        <button
          :class="{ active: form.accessMode === 'all_caddy' }"
          type="button"
          :disabled="form.accessModeManagedByEnvironment"
          @click="form.accessMode = 'all_caddy'"
        >
          <span>模式 1</span>
          <strong>控制台与应用统一走 Caddy</strong>
          <small>公网只开放 80 / 443；控制台匹配主域名，应用匹配泛子域名。</small>
        </button>
        <button
          :class="{ active: form.accessMode === 'apps_only' }"
          type="button"
          :disabled="form.accessModeManagedByEnvironment"
          @click="form.accessMode = 'apps_only'"
        >
          <span>模式 2</span>
          <strong>控制台独立端口，应用走 Caddy</strong>
          <small>控制台由外部反向代理转发到部署端口 {{ form.standalonePort }}；Caddy 只接管应用域名。</small>
        </button>
        <p v-if="form.accessModeManagedByEnvironment" class="environment-mode-note">
          当前模式由环境变量 GATEWAY_ACCESS_MODE 管理。修改变量并重建 API 服务后生效。
        </p>
      </section>

      <section v-if="activePanel === 'overview'" class="runtime-facts" aria-label="Caddy 运行状态">
        <article>
          <span :class="['fact-icon', runtime.connected ? 'online' : 'offline']"><Activity :size="18" /></span>
          <div><small>网关状态</small><strong>{{ runtime.connected ? "运行中" : "未连接" }}</strong></div>
          <em>{{ runtime.connected ? `${runtime.latencyMs} ms` : "检查内部 API" }}</em>
        </article>
        <article>
          <span class="fact-icon"><Network :size="18" /></span>
          <div><small>活动路由</small><strong>{{ runtime.routeCount }}</strong></div>
          <em>{{ runtime.proxyCount }} 个代理处理器</em>
        </article>
        <article>
          <span class="fact-icon"><Zap :size="18" /></span>
          <div><small>上游服务</small><strong>{{ runtime.upstreams.length }}</strong></div>
          <em>{{ healthyUpstreams }} 个无失败记录</em>
        </article>
        <article>
          <span class="fact-icon"><LockKeyhole :size="18" /></span>
          <div><small>TLS 模式</small><strong>{{ form.tlsEnabled ? "HTTPS" : "HTTP" }}</strong></div>
          <em>{{ tlsModeLabel }}</em>
        </article>
      </section>

      <section v-if="activePanel === 'overview'" class="gateway-overview" aria-label="Caddy 路由概览">
        <div class="gateway-summary">
          <span class="gateway-icon"><Globe2 :size="22" /></span>
          <div>
            <p>Caddy 运行入口</p>
            <strong>{{ runtime.statusMessage }}</strong>
          </div>
          <span :class="['gateway-state', runtime.connected ? 'ready' : 'pending']">
            {{ runtime.connected ? "Admin API 已连接" : "连接不可用" }}
          </span>
        </div>

        <div class="route-map">
          <article>
            <span class="route-kicker">控制台入口</span>
            <strong>{{ consoleHost }}</strong>
            <small>{{ form.accessMode === "all_caddy" ? "由 Caddy 匹配主域名" : "公开 URL，由外部反向代理提供访问" }}</small>
          </article>
          <ArrowRight class="route-arrow" :size="18" />
          <div :class="['caddy-node', { bypass: form.accessMode === 'apps_only' }]">
            <Network v-if="form.accessMode === 'all_caddy'" :size="20" />
            <Server v-else :size="20" />
            <span>{{ form.accessMode === "all_caddy" ? "Caddy" : "外部反代" }}</span>
            <small>{{ form.accessMode === "all_caddy" ? modeLabel : `转发到 :${form.standalonePort}` }}</small>
          </div>
          <ArrowRight class="route-arrow" :size="18" />
          <article>
            <span class="route-kicker">应用入口</span>
            <strong>{{ appWildcard }}</strong>
            <small>转发到应用路由服务</small>
          </article>
        </div>

        <div class="automation-summary">
          <div>
            <span>自动证书</span>
            <strong>{{ form.tlsEnabled ? `${automaticCertificateCount} 个入口由 Caddy 托管` : "未启用" }}</strong>
          </div>
          <div>
            <span>自动续期</span>
            <strong>{{ automaticCertificateCount ? `每 ${form.renewIntervalMinutes} 分钟检查` : "无托管证书" }}</strong>
          </div>
          <div>
            <span>续期窗口</span>
            <strong>{{ automaticCertificateCount ? "由 CA ARI / Caddy 自动决定" : "—" }}</strong>
          </div>
          <button class="text-action" type="button" @click="activePanel = 'certificates'">查看证书策略 <ArrowRight :size="14" /></button>
        </div>
      </section>

      <div v-if="activePanel === 'websites'" class="website-settings-layout">
        <section class="nextdev-card p-0 website-form-card">
          <div class="card-header-bar">
            <div class="card-title-group">
              <span class="eyebrow">ROUTING · 路由与网络</span>
              <h3>反向代理服务设置</h3>
            </div>
            <time v-if="updatedAt">最近保存 {{ updatedAt }}</time>
          </div>
          <div class="card-divider"></div>
          <form class="website-form" @submit.prevent="save">
            <label>
              <span>{{ form.accessMode === "all_caddy" ? "控制台主域名" : "服务器公开 URL" }}</span>
              <input
                v-model="form.serverUrl"
                type="url"
                inputmode="url"
                :placeholder="form.accessMode === 'all_caddy' ? 'https://console.example.com' : 'https://console.example.com'"
              />
              <small v-if="form.accessMode === 'all_caddy'">保存时会按 HTTPS 开关统一协议并移除端口，只保留主域名。</small>
              <small v-else>对外公开地址，用于 OAuth 回调、Webhook、支付通知和系统外链。公网 HTTPS 与反向代理由你的外部代理实现，不代表控制台监听地址。</small>
            </label>

            <div v-if="form.accessMode === 'apps_only'" class="standalone-listener">
              <div><span>控制台内部监听</span><strong>0.0.0.0:{{ form.standalonePort }}</strong></div>
              <code>外部代理 → http://服务器地址:{{ form.standalonePort }}</code>
              <small>端口由部署环境变量 PLATFORM_PORT 控制；Caddy 不创建控制台站点，也不管理其证书。</small>
            </div>

            <label>
              <span>应用泛子域名（App Base Domain）</span>
              <div class="domain-input">
                <i>*.</i>
                <input
                  v-model="form.appBaseDomain"
                  inputmode="url"
                  placeholder="apps.example.com"
                />
              </div>
              <small>与服务器地址相互独立。保存后，新旧应用都通过专属子域名访问。</small>
            </label>

            <div class="website-toggle-row">
              <div><strong>启用 Caddy HTTPS</strong><small>自动管理或使用导入证书，同时接管 443 与 HTTP/3。</small></div>
              <label class="switch"><input v-model="form.tlsEnabled" type="checkbox" /><span /></label>
            </div>

            <label v-if="form.tlsEnabled">
              <span>HTTP 访问策略</span>
              <select v-model="form.httpPolicy">
                <option value="redirect">自动跳转 HTTPS</option>
                <option value="allow">同时允许 HTTP 与 HTTPS</option>
                <option value="https_only">拒绝 HTTP</option>
              </select>
            </label>

            <div v-if="form.tlsEnabled" class="website-option-grid">
              <div class="website-toggle-row compact-row">
                <div><strong>HSTS</strong><small>浏览器后续强制使用 HTTPS。</small></div>
                <label class="switch"><input v-model="form.hstsEnabled" type="checkbox" /><span /></label>
              </div>
              <div class="website-toggle-row compact-row">
                <div><strong>HTTP/3</strong><small>通过 UDP 443 提供 QUIC。</small></div>
                <label class="switch"><input v-model="form.http3Enabled" type="checkbox" /><span /></label>
              </div>
            </div>

            <div class="website-form-note">
              <ShieldCheck :size="17" />
              <p>这些设置只决定网站入口与反向代理路由，不计入用户应用用量。</p>
            </div>
          </form>
        </section>

        <aside class="deployment-checklist">
          <div class="checklist-heading">
            <Server :size="19" />
            <div>
              <p class="eyebrow">部署检查</p>
              <h2>DNS 与 HTTPS</h2>
            </div>
          </div>

          <ol>
            <li>
              <span>01</span>
              <div>
                <strong>控制台域名解析</strong>
                <p>{{ form.accessMode === "all_caddy" ? `将 ${consoleHost} 的 A / AAAA 记录指向当前服务器。` : `让 ${consoleHost} 指向你的外部反向代理，再由它转发到控制台端口 ${form.standalonePort}。` }}</p>
              </div>
            </li>
            <li>
              <span>02</span>
              <div>
                <strong>应用泛域名解析</strong>
                <p>将 {{ appWildcard }} 解析到同一入口，示例：{{ appExample }}。</p>
              </div>
            </li>
            <li>
              <span>03</span>
              <div>
                <strong>证书覆盖范围</strong>
                <p>{{ usesHttps ? "Caddy 已配置 HTTPS；自动证书会续期，导入证书需关注到期时间。" : "生产环境建议启用 HTTPS，并准备控制台与应用域名证书。" }}</p>
              </div>
            </li>
            <li>
              <span>04</span>
              <div>
                <strong>端口与防火墙</strong>
                <p>应用网关放行 TCP 80/443 与 UDP 443；模式 2 的 {{ form.standalonePort }} 端口只需对外部反向代理可达。</p>
              </div>
            </li>
          </ol>
        </aside>
      </div>

      <section v-if="activePanel === 'certificates'" class="acme-workspace">
        <header class="runtime-panel-heading">
          <div>
            <p class="eyebrow">ACME ACCOUNT · 自动签发账户</p>
            <h2>默认证书自动化</h2>
            <p class="panel-description">Caddy 使用此账户申请托管证书，并在续期窗口内后台更新，无需停机或替换配置。</p>
          </div>
          <span :class="['automation-state', automaticCertificateCount ? 'ready' : 'pending']">
            {{ automaticCertificateCount ? `${automaticCertificateCount} 个托管目标` : "等待启用" }}
          </span>
        </header>

        <div class="acme-account-grid">
          <label>
            <span>账户联系邮箱</span>
            <input v-model="form.acmeEmail" type="email" placeholder="ops@example.com" />
            <small>用于 ACME 注册和接收证书异常通知。</small>
          </label>
          <label>
            <span>ACME 目录地址</span>
            <input v-model="form.acmeCa" type="url" inputmode="url" placeholder="https://acme-v02.api.letsencrypt.org/directory" />
            <small>支持 Let's Encrypt 正式/测试环境或兼容 ACME 的自定义 CA。</small>
          </label>
          <label>
            <span>证书密钥类型</span>
            <select v-model="form.acmeKeyType">
              <option value="p256">ECDSA P-256（推荐）</option>
              <option value="p384">ECDSA P-384</option>
              <option value="ed25519">Ed25519</option>
              <option value="rsa2048">RSA 2048</option>
              <option value="rsa4096">RSA 4096</option>
            </select>
            <small>仅影响后续签发；已存在证书会在下次更新时采用新策略。</small>
          </label>
        </div>

        <div class="dns-challenge-settings">
          <div class="dns-challenge-heading">
            <div>
              <strong>DNS-01 验证</strong>
              <p>签发与续期时自动创建临时 TXT 记录，适用于泛域名，也不要求验证请求能从公网访问 80/443 端口。</p>
            </div>
          </div>
          <label class="dns-provider-select">
            <span>DNS 服务商</span>
            <select v-model="form.acmeDnsProvider" @change="selectDNSProvider">
              <option value="" disabled>选择 DNS 服务商</option>
              <option v-for="provider in dnsProviderPresets" :key="provider.id" :value="provider.id">
                {{ provider.label }} · {{ provider.detail }}
              </option>
            </select>
          </label>
          <div v-if="activeDNSProvider" class="dns-credential-grid">
            <label v-for="field in activeDNSProvider.fields" :key="field.key">
              <span>{{ field.label }}</span>
              <input
                v-model="dnsCredentials[field.key]"
                :type="field.secret ? 'password' : 'text'"
                :placeholder="form.acmeDnsConfigured ? '已安全保存，留空保持不变' : field.placeholder"
                autocomplete="off"
              />
            </label>
          </div>
          <div v-if="form.acmeDnsConfigured" class="dns-credential-state">
            <ShieldCheck :size="16" />
            <span>当前服务商凭据已加密保存。只有重新填写凭据后才会覆盖。</span>
          </div>
        </div>

        <div class="renewal-policy">
          <div class="policy-copy">
            <strong>自动续期策略</strong>
            <p>托管证书默认自动续期；Caddy 每 {{ form.renewIntervalMinutes }} 分钟检查，并优先遵循证书颁发机构的 ARI 建议窗口，未提供 ARI 时使用 Caddy 安全默认值。</p>
          </div>
          <label>
            <span>检查周期（分钟）</span>
            <input v-model.number="form.renewIntervalMinutes" type="number" min="1" max="1440" step="1" />
          </label>
          <div class="renewal-managed-window">
            <span>续期窗口</span>
            <strong>自动管理</strong>
            <small>遵循 CA ARI；无 ARI 时按证书生命周期的安全默认窗口续期。</small>
          </div>
        </div>

        <div class="acme-guidance">
          <FileCheck2 :size="17" />
          <p><strong>DNS 自动申请：</strong>控制台域名保存后通过 DNS-01 签发。应用继续使用受授权的按需 TLS，只有数据库中正在运行的应用域名可申请；Caddy 会通过所选服务商自动创建并清理验证记录。</p>
        </div>
      </section>

      <section v-if="activePanel === 'certificates'" class="certificate-panel">
        <header class="runtime-panel-heading">
          <div>
            <p class="eyebrow">CERTIFICATES · 证书</p>
            <h2>HTTPS 证书管理</h2>
          </div>
          <KeyRound :size="20" />
        </header>
        <div v-if="!form.tlsEnabled" class="certificate-disabled-note">
          <CircleAlert :size="18" />
          <div><strong>HTTPS 尚未启用</strong><p>先在“网站”子界面启用 Caddy HTTPS；你仍可提前导入证书和编辑 ACME 账户。</p></div>
          <button class="secondary compact" type="button" @click="activePanel = 'websites'">前往网站设置</button>
        </div>
        <div class="certificate-grid">
          <article :class="{ disabled: form.accessMode === 'apps_only' }">
            <div class="certificate-card-head">
              <div><small>控制台站点</small><strong>{{ consoleHost }}</strong></div>
              <span>{{ consoleCertificate && form.consoleCertificateMode === "imported" ? certificateExpiry(consoleCertificate) : certificateState('console') }}</span>
            </div>
            <label>
              <span>证书来源</span>
              <select v-model="form.consoleCertificateMode" :disabled="form.accessMode === 'apps_only'">
                <option value="automatic">Caddy 自动申请与续期</option>
                <option value="imported">使用导入证书</option>
              </select>
            </label>
            <dl v-if="consoleCertificate" class="certificate-meta">
              <div><dt>签发者</dt><dd>{{ consoleCertificate.issuer || "—" }}</dd></div>
              <div><dt>指纹</dt><dd><code>{{ consoleCertificate.fingerprintSha256.slice(0, 16) }}</code></dd></div>
            </dl>
            <div v-else-if="form.consoleCertificateMode === 'automatic'" class="managed-certificate-note">
              <RefreshCw :size="15" /><span>证书、私钥和续期任务由 Caddy 的持久化存储管理。</span>
            </div>
            <button class="secondary compact" type="button" :disabled="form.accessMode === 'apps_only'" @click="openCertificateImport('console')">
              <Upload :size="15" />导入控制台证书
            </button>
          </article>

          <article>
            <div class="certificate-card-head">
              <div><small>应用站点</small><strong>{{ appWildcard }}</strong></div>
              <span>{{ appCertificate && form.appCertificateMode === "imported" ? certificateExpiry(appCertificate) : certificateState('applications') }}</span>
            </div>
            <label>
              <span>证书来源</span>
              <select v-model="form.appCertificateMode">
                <option value="automatic">按活动应用自动申请与续期</option>
                <option value="imported">使用泛域名导入证书</option>
              </select>
            </label>
            <dl v-if="appCertificate" class="certificate-meta">
              <div><dt>签发者</dt><dd>{{ appCertificate.issuer || "—" }}</dd></div>
              <div><dt>指纹</dt><dd><code>{{ appCertificate.fingerprintSha256.slice(0, 16) }}</code></dd></div>
            </dl>
            <div v-else-if="form.appCertificateMode === 'automatic'" class="managed-certificate-note">
              <RefreshCw :size="15" /><span>按需申请受内部域名授权接口限制，避免未知域名消耗 CA 配额。</span>
            </div>
            <button class="secondary compact" type="button" @click="openCertificateImport('applications')">
              <Upload :size="15" />导入应用泛域名证书
            </button>
          </article>
        </div>

        <form v-if="certificateTarget" class="certificate-import-form" @submit.prevent="importCertificate">
          <div>
            <p class="eyebrow">PEM IMPORT</p>
            <h3>导入{{ certificateTarget === "console" ? "控制台" : "应用泛域名" }}证书</h3>
            <p>后端会校验证书与私钥是否匹配、证书是否覆盖当前域名，并加密保存私钥。</p>
          </div>
          <label>
            <span>证书链（PEM）</span>
            <textarea v-model="certificateForm.certificatePem" rows="8" required placeholder="-----BEGIN CERTIFICATE-----" />
          </label>
          <label>
            <span>私钥（PEM）</span>
            <textarea v-model="certificateForm.privateKeyPem" rows="8" required placeholder="-----BEGIN PRIVATE KEY-----" />
          </label>
          <div class="certificate-import-actions">
            <button class="ghost compact" type="button" @click="certificateTarget = ''">取消</button>
            <button class="primary compact" :disabled="importingCertificate">
              <Upload :size="15" />{{ importingCertificate ? "校验并导入中" : "校验并导入" }}
            </button>
          </div>
        </form>
      </section>

      <section v-if="activePanel === 'routes'" class="route-inventory">
        <header class="runtime-panel-heading">
          <div>
            <p class="eyebrow">ROUTES · 应用入口</p>
            <h2>当前应用路径与反向代理路由</h2>
          </div>
          <span>{{ routes.length }} 条应用路由</span>
        </header>
        <div v-if="routes.length" class="route-inventory-table">
          <div class="route-inventory-head">
            <span>应用 / 所有者</span><span>公网入口</span><span>内部上游</span><span>状态</span>
          </div>
          <article v-for="route in routes" :key="route.id">
            <div class="route-app-cell">
              <span><AppWindow :size="15" /></span>
              <div><strong>{{ route.serviceSlug || route.slug }}</strong><small>{{ route.ownerEmail }}</small></div>
            </div>
            <a :href="route.publicUrl" target="_blank" rel="noreferrer">
              <Link2 :size="14" /><span>{{ route.publicUrl }}<small>
                {{ route.domainRefreshDays ? `每 ${route.domainRefreshDays} 天刷新${route.domainNextRefreshAt ? ` · 下次 ${new Date(route.domainNextRefreshAt).toLocaleString()}` : ""}` : "永久不变" }}
              </small></span>
            </a>
            <code>{{ route.upstream }}:{{ route.upstreamPort }}</code>
            <em :class="route.status">{{ route.status === "running" ? "运行中" : route.status }}</em>
          </article>
        </div>
        <div v-else class="runtime-empty">
          <CircleAlert :size="20" />
          <div><strong>暂无活动应用路由</strong><p>应用成功部署并进入运行状态后，会在这里显示专属域名、公开路径和容器上游。</p></div>
        </div>
      </section>

      <div v-if="activePanel === 'runtime'" class="caddy-console-grid">
        <section class="runtime-panel upstream-panel">
          <header class="runtime-panel-heading">
            <div>
              <p class="eyebrow">UPSTREAMS · 实时状态</p>
              <h2>反向代理上游</h2>
            </div>
            <span>{{ runtime.upstreams.length }} 个服务</span>
          </header>

          <div v-if="runtimeLoading" class="upstream-loading" aria-busy="true">
            <span v-for="index in 3" :key="index" class="skeleton skeleton-text"></span>
          </div>
          <div v-else-if="runtime.upstreams.length" class="upstream-table">
            <div class="upstream-table-head">
              <span>地址</span><span>活动请求</span><span>失败记忆</span><span>状态</span>
            </div>
            <div v-for="item in runtime.upstreams" :key="item.address" class="upstream-row">
              <code>{{ item.address }}</code>
              <span>{{ item.numRequests }}</span>
              <span>{{ item.fails }}</span>
              <em :class="item.fails ? 'degraded' : 'healthy'">{{ item.fails ? "需检查" : "正常" }}</em>
            </div>
          </div>
          <div v-else class="runtime-empty">
            <CircleAlert :size="20" />
            <div>
              <strong>{{ runtime.connected ? "暂未读取到上游" : "Caddy Admin API 未连接" }}</strong>
              <p>{{ runtime.connected ? "动态 DNS 上游会在首次解析后出现在这里。" : "更新部署并重启 gateway、api 后即可显示实时上游。" }}</p>
            </div>
          </div>
        </section>

        <section class="runtime-panel lifecycle-panel">
          <header class="runtime-panel-heading">
            <div>
              <p class="eyebrow">CONFIG · 配置生命周期</p>
              <h2>校验与重载</h2>
            </div>
            <TerminalSquare :size="20" />
          </header>

          <dl class="config-facts">
            <div><dt>配置源</dt><dd>{{ runtime.sourceAvailable ? "平台托管 Caddyfile" : "不可读取" }}</dd></div>
            <div><dt>内容指纹</dt><dd><code>{{ runtime.sourceDigest || "—" }}</code></dd></div>
            <div><dt>最近修改</dt><dd>{{ sourceModifiedText }}</dd></div>
            <div><dt>同步状态</dt><dd>{{ runtime.sourceInSync === undefined ? "未知" : runtime.sourceInSync ? "与活动配置一致" : "等待重载" }}</dd></div>
            <div><dt>监听地址</dt><dd>{{ runtime.listeners.length ? runtime.listeners.join("、") : "—" }}</dd></div>
            <div><dt>访问日志</dt><dd>{{ runtime.accessLogEnabled ? "JSON / Docker 日志" : "未启用" }}</dd></div>
          </dl>

          <div class="lifecycle-note">
            <ShieldCheck :size="17" />
            <p>重载前先调用 Caddy 适配器校验；失败时保留当前配置，成功时无中断切换。</p>
          </div>
          <div class="lifecycle-actions">
            <button class="secondary compact" :disabled="Boolean(operation) || !runtime.sourceAvailable || !runtime.connected" @click="validateConfig">
              <FileCheck2 :size="16" />{{ operation === "validate" ? "校验中" : "校验配置" }}
            </button>
            <button class="primary compact" :disabled="Boolean(operation) || !runtime.sourceAvailable || !runtime.connected" @click="reloadConfig">
              <RefreshCw :class="{ spin: operation === 'reload' }" :size="16" />{{ operation === "reload" ? "重载中" : "无中断重载" }}
            </button>
          </div>
        </section>
      </div>
    </template>

    <Teleport to="body">
      <Transition name="dialog-fade">
        <div v-if="wizardOpen" class="dialog-backdrop wizard-backdrop" @click.self="closeWizard">
          <section class="dialog-panel setup-wizard" role="dialog" aria-modal="true" aria-labelledby="setup-wizard-title">
            <header class="wizard-header">
              <div>
                <p class="eyebrow">SETUP · 逐步配置</p>
                <h2 id="setup-wizard-title">网站设置向导</h2>
                <p>按照顺序完成入口、域名和证书配置。</p>
              </div>
              <button class="wizard-close" type="button" aria-label="关闭设置向导" title="关闭" :disabled="saving" @click="closeWizard">
                <X :size="18" />
              </button>
            </header>

            <ol class="wizard-progress" aria-label="设置进度">
              <li v-for="(step, index) in wizardSteps" :key="step.label" :class="{ active: wizardStep === index, complete: wizardStep > index }">
                <span>{{ wizardStep > index ? "✓" : index + 1 }}</span>
                <div><strong>{{ step.label }}</strong><small>{{ step.detail }}</small></div>
              </li>
            </ol>

            <div class="wizard-body">
              <section v-if="wizardStep === 0" class="wizard-step">
                <div class="wizard-step-heading"><span>01</span><div><h3>选择接入模式</h3><p>决定控制台是否由内置 Caddy 直接接管。</p></div></div>
                <div class="wizard-choice-grid">
                  <button type="button" :class="{ selected: form.accessMode === 'all_caddy' }" :disabled="form.accessModeManagedByEnvironment" @click="form.accessMode = 'all_caddy'">
                    <Network :size="20" /><strong>全站使用 Caddy</strong><small>控制台与应用统一通过 80 / 443 对外服务。</small>
                  </button>
                  <button type="button" :class="{ selected: form.accessMode === 'apps_only' }" :disabled="form.accessModeManagedByEnvironment" @click="form.accessMode = 'apps_only'">
                    <Server :size="20" /><strong>仅应用使用 Caddy</strong><small>控制台由外部代理转发到端口 {{ form.standalonePort }}。</small>
                  </button>
                </div>
                <p v-if="form.accessModeManagedByEnvironment" class="wizard-note"><CircleAlert :size="16" />当前模式由 GATEWAY_ACCESS_MODE 管理，向导中不可修改。</p>
              </section>

              <section v-else-if="wizardStep === 1" class="wizard-step">
                <div class="wizard-step-heading"><span>02</span><div><h3>配置公开域名</h3><p>这些地址将用于控制台外链和应用专属入口。</p></div></div>
                <div class="wizard-fields">
                  <label><span>{{ form.accessMode === "all_caddy" ? "控制台主域名" : "服务器公开 URL" }}</span><input v-model="form.serverUrl" type="url" inputmode="url" placeholder="https://console.example.com" /><small>{{ form.accessMode === "apps_only" ? "对外公开地址，" : "" }}请输入完整 URL，包括 http:// 或 https://。</small></label>
                  <label><span>应用泛子域名</span><div class="domain-input"><i>*.</i><input v-model="form.appBaseDomain" inputmode="url" placeholder="apps.example.com" /></div><small>为每个应用生成独立子域名，无需输入 *. 前缀。</small></label>
                  <label><span>端口映射域名</span><input v-model="form.portMappingHost" inputmode="url" placeholder="direct.example.com" /><small>应用直连地址使用此域名和分配端口；留空时使用当前控制台访问域名。无需填写协议和端口。</small></label>
                </div>
              </section>

              <section v-else-if="wizardStep === 2" class="wizard-step">
                <div class="wizard-step-heading"><span>03</span><div><h3>配置 HTTPS 与 DNS</h3><p>自动证书通过 DNS-01 验证申请，并由 Caddy 续期。</p></div></div>
                <div class="wizard-toggle"><div><strong>启用 Caddy HTTPS</strong><small>为 Caddy 管理的入口启用 TLS。</small></div><label class="switch"><input v-model="form.tlsEnabled" type="checkbox" /><span /></label></div>
                <template v-if="form.tlsEnabled">
                  <div class="wizard-inline-fields">
                    <label><span>HTTP 策略</span><select v-model="form.httpPolicy"><option value="redirect">自动跳转 HTTPS</option><option value="allow">同时允许 HTTP 与 HTTPS</option><option value="https_only">拒绝 HTTP</option></select></label>
                    <label><span>ACME 联系邮箱</span><input v-model="form.acmeEmail" type="email" placeholder="admin@example.com" /></label>
                    <label><span>DNS 服务商</span><select v-model="form.acmeDnsProvider" @change="selectDNSProvider"><option value="">请选择</option><option v-for="provider in dnsProviderPresets" :key="provider.id" :value="provider.id">{{ provider.label }}</option></select></label>
                  </div>
                  <div v-if="activeDNSProvider" class="wizard-credentials">
                    <div><strong>{{ activeDNSProvider.label }} API 凭据</strong><small>{{ activeDNSProvider.detail }}</small></div>
                    <div class="wizard-inline-fields"><label v-for="field in activeDNSProvider.fields" :key="field.key"><span>{{ field.label }}</span><input v-model="dnsCredentials[field.key]" :type="field.secret ? 'password' : 'text'" :placeholder="form.acmeDnsConfigured ? '已配置，留空则保持不变' : field.placeholder" /></label></div>
                    <p v-if="form.acmeDnsConfigured" class="wizard-configured"><CheckCircle2 :size="15" />服务器已有可用凭据，留空将保持不变。</p>
                  </div>
                </template>
                <p v-else class="wizard-note"><CircleAlert :size="16" />HTTPS 已关闭，将不会申请或续期证书。</p>
              </section>

              <section v-else class="wizard-step">
                <div class="wizard-step-heading"><span>04</span><div><h3>确认并应用</h3><p>保存后将校验配置并无中断重载 Caddy。</p></div></div>
                <dl class="wizard-summary">
                  <div><dt>接入模式</dt><dd>{{ modeLabel }}</dd></div><div><dt>控制台入口</dt><dd>{{ form.serverUrl || "未配置" }}</dd></div>
                  <div><dt>应用入口</dt><dd>{{ appWildcard }}</dd></div><div><dt>HTTPS</dt><dd>{{ form.tlsEnabled ? "已启用" : "未启用" }}</dd></div>
                  <div><dt>证书申请</dt><dd>{{ needsAutomaticCertificate ? "DNS-01 自动申请" : "无需自动申请" }}</dd></div><div><dt>DNS 服务商</dt><dd>{{ needsAutomaticCertificate ? dnsProviderLabel : "—" }}</dd></div>
                </dl>
                <p class="wizard-apply-note"><ShieldCheck :size="17" />应用失败时当前 Caddy 配置会继续运行，不会切换到无效配置。</p>
              </section>
              <p v-if="wizardError" class="wizard-error"><CircleAlert :size="16" />{{ wizardError }}</p>
            </div>

            <footer class="wizard-actions">
              <button class="ghost compact" type="button" :disabled="saving" @click="closeWizard">取消</button>
              <span />
              <button v-if="wizardStep > 0" class="secondary compact" type="button" :disabled="saving" @click="previousWizardStep"><ArrowLeft :size="16" />上一步</button>
              <button v-if="wizardStep < wizardSteps.length - 1" class="primary compact" type="button" @click="nextWizardStep">下一步<ArrowRight :size="16" /></button>
              <button v-else class="primary compact" type="button" :disabled="saving" @click="finishWizard"><Save :size="16" />{{ saving ? "应用中..." : "保存并应用" }}</button>
            </footer>
          </section>
        </div>
      </Transition>
    </Teleport>
  </main>
</template>

<style scoped>
.website-page-heading,
.website-actions,
.gateway-summary,
.route-map,
.checklist-heading,
.website-message,
.website-form-note {
  display: flex;
  align-items: center;
}
.website-page-heading {
  justify-content: space-between;
  align-items: flex-start;
  gap: 24px;
}
.website-actions { gap: 8px; margin-top: 10px; }
.website-message { gap: 8px; }
.website-loading {
  display: grid;
  gap: 12px;
  min-height: 180px;
  margin-top: 20px;
  padding: 28px;
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
}
.website-loading .skeleton-title { width: 32%; }
.website-loading .skeleton-text { width: 68%; }
.website-loading .short { width: 46%; }
.website-subnav {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 4px;
  margin-top: 22px;
  padding: 4px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 11px;
}
.website-subnav button {
  min-width: 0;
  display: grid;
  gap: 2px;
  padding: 10px 12px;
  text-align: left;
  color: var(--text-muted);
  background: transparent;
  border: 1px solid transparent;
  border-radius: 7px;
  transition: color .2s ease, background-color .2s ease, border-color .2s ease;
}
.website-subnav button:hover { color: var(--text); background: var(--paper); }
.website-subnav button.active { color: var(--text); background: var(--paper); border-color: var(--line-strong); }
.website-subnav strong { overflow: hidden; font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.website-subnav button.active strong { color: var(--accent); }
.website-subnav small { overflow: hidden; font-size: 9px; text-overflow: ellipsis; white-space: nowrap; }
.access-mode-switch {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-top: 22px;
}
.access-mode-switch button {
  display: grid;
  gap: 5px;
  padding: 18px 20px;
  text-align: left;
  color: var(--text);
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: 12px;
  transition: border-color .2s ease, background-color .2s ease, transform .2s ease;
}
.access-mode-switch button:hover { transform: translateY(-1px); border-color: var(--line-strong); }
.access-mode-switch button.active { border-color: var(--accent); background: var(--accent-soft); }
.access-mode-switch button:disabled { cursor: not-allowed; transform: none; }
.access-mode-switch span { color: var(--accent); font-family: var(--font-mono); font-size: 10px; font-weight: 750; }
.access-mode-switch strong { font-size: 14px; }
.access-mode-switch small { max-width: 58ch; color: var(--text-muted); font-size: 11px; line-height: 1.55; }
.environment-mode-note {
  grid-column: 1 / -1;
  margin: 0;
  padding: 10px 12px;
  color: var(--text-muted);
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 8px;
  font-size: 10px;
}
.runtime-facts {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin-top: 22px;
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.runtime-facts article {
  min-width: 0;
  display: grid;
  grid-template-columns: 38px minmax(0, 1fr);
  gap: 4px 11px;
  align-items: center;
  padding: 17px 18px;
  border-right: 1px solid var(--line);
}
.runtime-facts article:last-child { border-right: 0; }
.fact-icon {
  grid-row: 1 / span 2;
  width: 38px;
  height: 38px;
  display: grid;
  place-items: center;
  color: var(--text-muted);
  background: var(--surface);
  border-radius: 8px;
}
.fact-icon.online { color: var(--accent); background: var(--accent-soft); }
.fact-icon.offline { color: #f59e0b; background: rgba(245, 158, 11, .1); }
.runtime-facts small,
.runtime-facts strong { display: block; }
.runtime-facts small { color: var(--text-muted); font-size: 10px; }
.runtime-facts strong { margin-top: 2px; font-size: 17px; font-variant-numeric: tabular-nums; }
.runtime-facts em {
  grid-column: 2;
  overflow: hidden;
  color: var(--text-muted);
  font-size: 10px;
  font-style: normal;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.gateway-overview {
  margin-top: 14px;
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.gateway-summary {
  gap: 13px;
  padding: 17px 20px;
  border-bottom: 1px solid var(--line);
}
.gateway-icon {
  width: 40px;
  height: 40px;
  display: grid;
  place-items: center;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: 9px;
}
.gateway-summary p,
.gateway-summary strong { display: block; margin: 0; }
.gateway-summary p { color: var(--text-muted); font-size: 12px; }
.gateway-summary strong { margin-top: 2px; font-size: 15px; }
.gateway-state {
  margin-left: auto;
  padding: 5px 9px;
  border-radius: 6px;
  font-size: 11px;
  font-weight: 700;
}
.gateway-state.ready { color: var(--accent); background: var(--accent-soft); }
.gateway-state.pending { color: var(--text-muted); background: var(--soft); }
.route-map {
  justify-content: center;
  gap: 22px;
  padding: 28px clamp(18px, 4vw, 52px) 31px;
}
.route-map article {
  flex: 1;
  min-width: 0;
  padding: 0 4px;
}
.route-map article:last-child { text-align: right; }
.route-map strong,
.route-map small,
.route-kicker { display: block; }
.route-kicker {
  margin-bottom: 7px;
  color: var(--text-muted);
  font-size: 11px;
  font-weight: 650;
}
.route-map strong {
  overflow: hidden;
  color: var(--text);
  font-family: var(--font-mono);
  font-size: clamp(13px, 1.3vw, 17px);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.route-map small { margin-top: 6px; color: var(--text-muted); font-size: 11px; }
.route-arrow { flex: 0 0 auto; color: var(--muted-2); }
.caddy-node {
  flex: 0 0 118px;
  min-height: 82px;
  display: grid;
  place-items: center;
  align-content: center;
  gap: 3px;
  color: var(--accent);
  background: var(--accent-soft);
  border: 1px solid color-mix(in srgb, var(--accent) 28%, transparent);
  border-radius: 10px;
}
.caddy-node span { color: var(--text); font-size: 13px; font-weight: 750; }
.caddy-node small { color: var(--text-muted); font-size: 10px; }
.caddy-node.bypass { color: var(--text-muted); background: var(--surface); border-color: var(--line); }
.automation-summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr)) auto;
  gap: 1px;
  align-items: stretch;
  padding: 0 20px 20px;
}
.automation-summary > div {
  display: grid;
  gap: 4px;
  padding: 13px 14px;
  background: var(--surface);
  border-right: 1px solid var(--line);
}
.automation-summary > div:first-child { border-radius: 8px 0 0 8px; }
.automation-summary span { color: var(--text-muted); font-size: 9px; }
.automation-summary strong { font-size: 11px; font-weight: 650; }
.text-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 0 14px;
  color: var(--accent);
  background: var(--accent-soft);
  border: 0;
  border-radius: 0 8px 8px 0;
  font-size: 10px;
  font-weight: 700;
}
.website-settings-layout {
  display: grid;
  grid-template-columns: minmax(0, 1.55fr) minmax(290px, 0.65fr);
  gap: 18px;
  margin-top: 18px;
  align-items: start;
}
.website-form-card time { color: var(--text-muted); font-size: 11px; }
.website-form { display: grid; gap: 23px; padding: 24px; }
.website-form label { display: grid; gap: 8px; }
.website-form label > span { font-size: 14px; font-weight: 650; }
.website-form input {
  width: 100%;
  min-width: 0;
  padding: 11px 13px;
  color: var(--text);
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: 8px;
  outline: none;
  transition: border-color .2s ease, box-shadow .2s ease;
}
.website-form input:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
}
.website-form small { color: var(--text-muted); font-size: 12px; line-height: 1.5; }
.website-toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 14px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 9px;
}
.website-toggle-row strong,
.website-toggle-row small { display: block; }
.website-toggle-row strong { font-size: 13px; }
.website-toggle-row small { margin-top: 4px; font-size: 11px; }
.website-option-grid,
.acme-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.website-toggle-row.compact-row { min-height: 74px; }
.domain-input { display: grid; grid-template-columns: auto minmax(0, 1fr); }
.domain-input i {
  display: grid;
  place-items: center;
  padding: 0 12px;
  color: var(--text-muted);
  background: var(--surface);
  border: 1px solid var(--line);
  border-right: 0;
  border-radius: 8px 0 0 8px;
  font-family: var(--font-mono);
  font-style: normal;
}
.domain-input input { border-radius: 0 8px 8px 0; }
.standalone-listener {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 7px 18px;
  align-items: center;
  padding: 14px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 9px;
}
.standalone-listener div { display: grid; gap: 3px; }
.standalone-listener span,
.standalone-listener small { color: var(--text-muted); font-size: 10px; }
.standalone-listener strong,
.standalone-listener code { font-family: var(--font-mono); font-size: 11px; }
.standalone-listener code { color: var(--accent); }
.standalone-listener small { grid-column: 1 / -1; line-height: 1.5; }
.website-form-note {
  align-items: flex-start;
  gap: 10px;
  padding: 13px 14px;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: 8px;
}
.website-form-note p { margin: 0; color: var(--text-soft); font-size: 12px; line-height: 1.5; }
.deployment-checklist {
  padding: 20px;
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
}
.checklist-heading { align-items: flex-start; gap: 11px; }
.checklist-heading > svg { margin-top: 2px; color: var(--accent); }
.checklist-heading .eyebrow { margin-bottom: 2px; }
.checklist-heading h2 { margin: 0; font-size: 17px; }
.deployment-checklist ol { display: grid; gap: 0; margin: 18px 0 0; padding: 0; list-style: none; }
.deployment-checklist li {
  display: grid;
  grid-template-columns: 28px minmax(0, 1fr);
  gap: 11px;
  padding: 14px 0;
  border-top: 1px solid var(--line);
}
.deployment-checklist li > span {
  padding-top: 1px;
  color: var(--accent);
  font-family: var(--font-mono);
  font-size: 10px;
}
.deployment-checklist strong { font-size: 13px; }
.deployment-checklist p {
  margin: 5px 0 0;
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}
.acme-workspace {
  margin-top: 18px;
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.panel-description { max-width: 68ch; margin: 5px 0 0; color: var(--text-muted); font-size: 11px; line-height: 1.5; }
.automation-state { padding: 5px 8px; border-radius: 6px; }
.automation-state.ready { color: var(--accent); background: var(--accent-soft); }
.automation-state.pending { color: var(--text-muted); background: var(--surface); }
.acme-account-grid {
  display: grid;
  grid-template-columns: minmax(180px, .7fr) minmax(280px, 1.3fr) minmax(180px, .7fr);
  gap: 14px;
  padding: 18px;
}
.acme-account-grid label,
.renewal-policy label,
.renewal-managed-window { display: grid; align-content: start; gap: 7px; }
.acme-account-grid label > span,
.renewal-policy label > span,
.renewal-managed-window > span { font-size: 11px; font-weight: 650; }
.acme-account-grid small,
.renewal-policy small,
.renewal-managed-window small { color: var(--text-muted); font-size: 9px; line-height: 1.45; }
.dns-challenge-settings {
  display: grid;
  grid-template-columns: minmax(240px, .75fr) minmax(280px, 1.25fr);
  gap: 14px 18px;
  margin: 0 18px 16px;
  padding: 16px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 9px;
}
.dns-challenge-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.dns-challenge-heading strong { font-size: 12px; }
.dns-challenge-heading p { max-width: 48ch; margin: 5px 0 0; color: var(--text-muted); font-size: 10px; line-height: 1.55; }
.dns-provider-select,
.dns-credential-grid label { display: grid; align-content: start; gap: 7px; }
.dns-provider-select > span,
.dns-credential-grid label > span { font-size: 11px; font-weight: 650; }
.dns-credential-grid { grid-column: 1 / -1; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.dns-credential-state { grid-column: 1 / -1; display: flex; align-items: center; gap: 8px; color: var(--accent); font-size: 10px; }
.renewal-managed-window strong { color: var(--accent); font-size: 12px; }
.renewal-policy {
  display: grid;
  grid-template-columns: minmax(260px, 1fr) minmax(150px, .42fr) minmax(220px, .58fr);
  gap: 18px;
  align-items: start;
  margin: 0 18px;
  padding: 16px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 9px;
}
.policy-copy strong { font-size: 12px; }
.policy-copy p { max-width: 58ch; margin: 5px 0 0; color: var(--text-muted); font-size: 10px; line-height: 1.55; }
.acme-guidance,
.certificate-disabled-note,
.managed-certificate-note { display: flex; align-items: flex-start; gap: 9px; }
.acme-guidance { margin: 14px 18px 18px; padding: 13px 14px; color: var(--accent); background: var(--accent-soft); border-radius: 8px; }
.acme-guidance p { margin: 0; color: var(--text-soft); font-size: 10px; line-height: 1.55; }
.certificate-disabled-note { align-items: center; margin: 18px 18px 0; padding: 13px 14px; color: var(--text-muted); background: var(--surface); border: 1px solid var(--line); border-radius: 8px; }
.certificate-disabled-note div { flex: 1; }
.certificate-disabled-note strong { color: var(--text); font-size: 12px; }
.certificate-disabled-note p { margin: 3px 0 0; font-size: 10px; }
.managed-certificate-note { min-height: 35px; color: var(--accent); }
.managed-certificate-note span { color: var(--text-muted); font-size: 10px; line-height: 1.5; }
.certificate-panel,
.route-inventory {
  margin-top: 18px;
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.certificate-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  padding: 18px;
}
.certificate-grid article {
  display: grid;
  align-content: start;
  gap: 14px;
  padding: 18px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 10px;
}
.certificate-grid article.disabled { opacity: .58; }
.certificate-card-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 14px; }
.certificate-card-head small,
.certificate-card-head strong { display: block; }
.certificate-card-head small { color: var(--text-muted); font-size: 10px; }
.certificate-card-head strong { margin-top: 4px; font-family: var(--font-mono); font-size: 13px; }
.certificate-card-head > span { color: var(--text-muted); font-size: 10px; white-space: nowrap; }
.certificate-grid label { display: grid; gap: 7px; font-size: 12px; font-weight: 650; }
.certificate-grid button { justify-self: start; }
.certificate-meta { margin: 0; }
.certificate-meta div { display: flex; justify-content: space-between; gap: 16px; padding: 7px 0; border-top: 1px solid var(--line); font-size: 10px; }
.certificate-meta dt { color: var(--text-muted); }
.certificate-meta dd { margin: 0; }
.certificate-meta code { color: var(--accent); font-family: var(--font-mono); }
.certificate-import-form {
  display: grid;
  grid-template-columns: minmax(180px, .55fr) repeat(2, minmax(0, 1fr));
  gap: 16px;
  padding: 18px;
  border-top: 1px solid var(--line);
}
.certificate-import-form h3 { margin: 2px 0 5px; font-size: 15px; }
.certificate-import-form p { margin: 0; color: var(--text-muted); font-size: 11px; line-height: 1.55; }
.certificate-import-form label { display: grid; gap: 7px; font-size: 12px; font-weight: 650; }
.certificate-import-form textarea { min-height: 150px; font-family: var(--font-mono); font-size: 10px; resize: vertical; }
.certificate-import-actions { grid-column: 1 / -1; display: flex; justify-content: flex-end; gap: 8px; }
.route-inventory-table { min-width: 0; }
.route-inventory-head,
.route-inventory-table article {
  display: grid;
  grid-template-columns: minmax(170px, .8fr) minmax(260px, 1.35fr) minmax(170px, .75fr) 72px;
  align-items: center;
  gap: 14px;
  padding: 11px 18px;
}
.route-inventory-head { color: var(--text-muted); background: var(--surface); font-size: 10px; font-weight: 650; }
.route-inventory-table article { min-height: 58px; border-top: 1px solid var(--line); font-size: 11px; }
.route-app-cell { display: flex; align-items: center; gap: 10px; min-width: 0; }
.route-app-cell > span { width: 30px; height: 30px; flex: 0 0 30px; display: grid; place-items: center; color: var(--accent); background: var(--accent-soft); border-radius: 8px; }
.route-app-cell strong,
.route-app-cell small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.route-app-cell small { margin-top: 2px; color: var(--text-muted); }
.route-inventory-table a { min-width: 0; display: flex; align-items: center; gap: 7px; color: var(--accent); }
.route-inventory-table a span,
.route-inventory-table code { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.route-inventory-table code { color: var(--text-soft); font-family: var(--font-mono); }
.route-inventory-table em { justify-self: start; padding: 4px 7px; color: var(--text-muted); background: var(--soft); border-radius: 5px; font-size: 10px; font-style: normal; }
.route-inventory-table em.running { color: var(--accent); background: var(--accent-soft); }
.caddy-console-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(320px, .65fr);
  gap: 18px;
  margin-top: 18px;
}
.runtime-panel {
  min-width: 0;
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.runtime-panel-heading {
  min-height: 74px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 19px;
  border-bottom: 1px solid var(--line);
}
.runtime-panel-heading .eyebrow { margin-bottom: 3px; }
.runtime-panel-heading h2 { margin: 0; font-size: 17px; }
.runtime-panel-heading > span {
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 11px;
}
.runtime-panel-heading > svg { color: var(--accent); }
.upstream-table-head,
.upstream-row {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) 92px 82px 74px;
  align-items: center;
  gap: 12px;
  padding: 11px 18px;
}
.upstream-table-head {
  color: var(--text-muted);
  background: var(--surface);
  font-size: 10px;
  font-weight: 650;
}
.upstream-row {
  min-height: 46px;
  border-top: 1px solid var(--line);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.upstream-row:first-of-type { border-top: 0; }
.upstream-row code {
  overflow: hidden;
  color: var(--text-soft);
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.upstream-row em {
  justify-self: start;
  padding: 4px 7px;
  border-radius: 5px;
  font-size: 10px;
  font-style: normal;
  font-weight: 700;
}
.upstream-row em.healthy { color: var(--accent); background: var(--accent-soft); }
.upstream-row em.degraded { color: #f59e0b; background: rgba(245, 158, 11, .1); }
.upstream-loading { display: grid; gap: 16px; padding: 22px 18px; }
.runtime-empty {
  min-height: 156px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 24px;
  color: var(--text-muted);
}
.runtime-empty strong { color: var(--text); font-size: 13px; }
.runtime-empty p { max-width: 44ch; margin: 5px 0 0; font-size: 11px; line-height: 1.5; }
.config-facts { margin: 0; padding: 8px 19px; }
.config-facts div {
  display: flex;
  justify-content: space-between;
  gap: 18px;
  padding: 11px 0;
  border-bottom: 1px solid var(--line);
  font-size: 11px;
}
.config-facts div:last-child { border-bottom: 0; }
.config-facts dt { color: var(--text-muted); }
.config-facts dd { margin: 0; text-align: right; overflow-wrap: anywhere; }
.config-facts code { font-family: var(--font-mono); color: var(--accent); }
.lifecycle-note {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  margin: 6px 19px 0;
  padding: 12px;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: 7px;
}
.lifecycle-note p { margin: 0; color: var(--text-soft); font-size: 11px; line-height: 1.5; }
.lifecycle-actions { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; padding: 15px 19px 19px; }
.lifecycle-actions button { justify-content: center; }
.wizard-backdrop { z-index: 1200; padding: 24px; }
.setup-wizard {
  width: min(920px, 100%);
  max-width: 920px;
  max-height: min(760px, calc(100vh - 48px));
  display: grid;
  grid-template-rows: auto auto minmax(0, 1fr) auto;
  padding: 0;
  overflow: hidden;
}
.wizard-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 24px; padding: 22px 24px 18px; border-bottom: 1px solid var(--line); }
.wizard-header h2 { margin: 2px 0 0; font-size: 21px; }
.wizard-header p:last-child { margin: 6px 0 0; color: var(--text-muted); font-size: 11px; }
.wizard-close { width: 34px; height: 34px; flex: 0 0 auto; display: grid; place-items: center; padding: 0; color: var(--text-muted); background: var(--surface); border: 1px solid var(--line); border-radius: 7px; }
.wizard-close:hover { color: var(--text); border-color: var(--line-strong); }
.wizard-progress { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); margin: 0; padding: 14px 24px; list-style: none; background: var(--surface); border-bottom: 1px solid var(--line); }
.wizard-progress li { min-width: 0; display: flex; align-items: center; gap: 9px; position: relative; color: var(--text-muted); }
.wizard-progress li:not(:last-child)::after { content: ""; position: absolute; top: 13px; left: 36px; right: 10px; height: 1px; background: var(--line-strong); }
.wizard-progress li > span { width: 26px; height: 26px; z-index: 1; flex: 0 0 auto; display: grid; place-items: center; background: var(--paper); border: 1px solid var(--line-strong); border-radius: 50%; font-family: var(--font-mono); font-size: 10px; font-weight: 750; }
.wizard-progress li div { min-width: 0; z-index: 1; padding-right: 8px; background: var(--surface); }
.wizard-progress strong, .wizard-progress small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.wizard-progress strong { color: var(--text-soft); font-size: 11px; }
.wizard-progress small { margin-top: 2px; font-size: 9px; }
.wizard-progress li.active > span, .wizard-progress li.complete > span { color: white; background: var(--accent); border-color: var(--accent); }
.wizard-progress li.active strong, .wizard-progress li.complete strong { color: var(--text); }
.wizard-body { min-height: 390px; overflow-y: auto; padding: 25px 24px; }
.wizard-step { display: grid; gap: 19px; }
.wizard-step-heading { display: flex; align-items: flex-start; gap: 13px; }
.wizard-step-heading > span { color: var(--accent); font-family: var(--font-mono); font-size: 10px; font-weight: 750; }
.wizard-step-heading h3 { margin: 0; font-size: 17px; }
.wizard-step-heading p { margin: 5px 0 0; color: var(--text-muted); font-size: 11px; }
.wizard-choice-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.wizard-choice-grid button { min-height: 126px; display: grid; align-content: start; justify-items: start; gap: 8px; padding: 18px; text-align: left; color: var(--text-muted); background: var(--paper); border: 1px solid var(--line); border-radius: 8px; }
.wizard-choice-grid button strong { color: var(--text); font-size: 13px; }
.wizard-choice-grid button small { font-size: 11px; line-height: 1.55; }
.wizard-choice-grid button.selected { color: var(--accent); background: var(--accent-soft); border-color: var(--accent); }
.wizard-choice-grid button:disabled { cursor: not-allowed; }
.wizard-fields, .wizard-inline-fields { display: grid; gap: 16px; }
.wizard-fields { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.wizard-inline-fields { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.wizard-fields label, .wizard-inline-fields label { min-width: 0; display: grid; align-content: start; gap: 7px; }
.wizard-fields label > span, .wizard-inline-fields label > span { font-size: 11px; font-weight: 650; }
.wizard-fields small, .wizard-credentials small { color: var(--text-muted); font-size: 10px; line-height: 1.5; }
.wizard-fields input, .wizard-inline-fields input, .wizard-inline-fields select { width: 100%; min-width: 0; }
.wizard-toggle { display: flex; align-items: center; justify-content: space-between; gap: 18px; padding: 14px; background: var(--surface); border: 1px solid var(--line); border-radius: 8px; }
.wizard-toggle strong, .wizard-toggle small { display: block; }
.wizard-toggle strong { font-size: 12px; }
.wizard-toggle small { margin-top: 4px; color: var(--text-muted); font-size: 10px; }
.wizard-credentials { display: grid; gap: 14px; padding: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: 8px; }
.wizard-credentials > div:first-child { display: grid; gap: 3px; }
.wizard-credentials > div:first-child strong { font-size: 12px; }
.wizard-configured, .wizard-note, .wizard-apply-note, .wizard-error { display: flex; align-items: flex-start; gap: 8px; margin: 0; padding: 11px 12px; border-radius: 7px; font-size: 10px; line-height: 1.5; }
.wizard-configured, .wizard-apply-note { color: var(--accent); background: var(--accent-soft); }
.wizard-note { color: var(--text-muted); background: var(--surface); border: 1px solid var(--line); }
.wizard-error { margin-top: 16px; color: #b42318; background: rgba(180, 35, 24, .08); border: 1px solid rgba(180, 35, 24, .18); }
.wizard-summary { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); margin: 0; border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
.wizard-summary div { min-width: 0; display: grid; grid-template-columns: 110px minmax(0, 1fr); gap: 12px; padding: 13px 14px; border-bottom: 1px solid var(--line); }
.wizard-summary div:nth-child(odd) { border-right: 1px solid var(--line); }
.wizard-summary div:nth-last-child(-n+2) { border-bottom: 0; }
.wizard-summary dt { color: var(--text-muted); font-size: 10px; }
.wizard-summary dd { min-width: 0; margin: 0; overflow-wrap: anywhere; font-size: 11px; font-weight: 650; }
.wizard-actions { display: grid; grid-template-columns: auto 1fr auto auto; gap: 8px; padding: 14px 24px; background: var(--surface); border-top: 1px solid var(--line); }
.wizard-actions button { justify-content: center; }
@media (max-width: 980px) {
  .certificate-import-form { grid-template-columns: 1fr 1fr; }
  .certificate-import-form > div:first-child { grid-column: 1 / -1; }
  .website-settings-layout { grid-template-columns: 1fr; }
  .caddy-console-grid { grid-template-columns: 1fr; }
  .runtime-facts { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .runtime-facts article:nth-child(2) { border-right: 0; }
  .runtime-facts article:nth-child(-n+2) { border-bottom: 1px solid var(--line); }
  .route-map { gap: 12px; }
  .website-subnav { grid-template-columns: repeat(5, minmax(112px, 1fr)); overflow-x: auto; }
  .acme-account-grid { grid-template-columns: 1fr 1fr; }
  .acme-account-grid label:nth-child(2) { grid-column: 1 / -1; grid-row: 2; }
  .renewal-policy { grid-template-columns: 1fr 1fr; }
  .dns-challenge-settings { grid-template-columns: 1fr; }
  .dns-credential-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .policy-copy { grid-column: 1 / -1; }
  .wizard-inline-fields { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 680px) {
  .access-mode-switch,
  .website-option-grid,
  .acme-fields,
  .certificate-grid,
  .certificate-import-form { grid-template-columns: 1fr; }
  .certificate-import-form > div:first-child { grid-column: auto; }
  .route-inventory-table { overflow-x: auto; }
  .route-inventory-head,
  .route-inventory-table article { min-width: 760px; }
  .website-page-heading { flex-direction: column; }
  .website-actions { width: 100%; margin-top: 0; }
  .website-actions button { flex: 1; }
  .route-map { align-items: stretch; flex-direction: column; }
  .route-map article, .route-map article:last-child { text-align: left; }
  .route-arrow { align-self: center; transform: rotate(90deg); }
  .caddy-node { width: 100%; min-height: 74px; }
  .card-header-bar { align-items: flex-start; flex-direction: column; gap: 8px; }
  .runtime-facts { grid-template-columns: 1fr; }
  .runtime-facts article,
  .runtime-facts article:nth-child(2) { border-right: 0; border-bottom: 1px solid var(--line); }
  .runtime-facts article:last-child { border-bottom: 0; }
  .upstream-table { overflow-x: auto; }
  .upstream-table-head,
  .upstream-row { min-width: 570px; }
  .lifecycle-actions { grid-template-columns: 1fr; }
  .automation-summary { grid-template-columns: 1fr; }
  .automation-summary > div { border-right: 0; border-bottom: 1px solid var(--line); }
  .automation-summary > div:first-child { border-radius: 8px 8px 0 0; }
  .text-action { min-height: 40px; border-radius: 0 0 8px 8px; }
  .acme-account-grid,
  .renewal-policy,
  .dns-credential-grid { grid-template-columns: 1fr; }
  .acme-account-grid label:nth-child(2),
  .policy-copy { grid-column: auto; grid-row: auto; }
  .certificate-disabled-note { align-items: flex-start; flex-wrap: wrap; }
  .standalone-listener { grid-template-columns: 1fr; }
  .standalone-listener small { grid-column: auto; }
  .wizard-backdrop { padding: 0; }
  .setup-wizard { width: 100%; max-height: 100vh; min-height: 100vh; border-radius: 0; }
  .wizard-header { padding: 17px 16px 14px; }
  .wizard-progress { grid-template-columns: repeat(4, 1fr); padding: 11px 16px; }
  .wizard-progress li { justify-content: center; }
  .wizard-progress li div { display: none; }
  .wizard-progress li:not(:last-child)::after { left: calc(50% + 14px); right: calc(-50% + 14px); }
  .wizard-body { min-height: 0; padding: 20px 16px; }
  .wizard-choice-grid, .wizard-fields, .wizard-inline-fields, .wizard-summary { grid-template-columns: 1fr; }
  .wizard-choice-grid button { min-height: 108px; }
  .wizard-summary div, .wizard-summary div:nth-child(odd), .wizard-summary div:nth-last-child(-n+2) { border-right: 0; border-bottom: 1px solid var(--line); }
  .wizard-summary div:last-child { border-bottom: 0; }
  .wizard-actions { grid-template-columns: auto 1fr auto; padding: 12px 16px; }
  .wizard-actions > span { display: none; }
  .wizard-actions .primary { grid-column: 3; }
}
</style>
