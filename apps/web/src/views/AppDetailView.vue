<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  ArrowLeft,
  Check,
  CircleAlert,
  Copy,
  ExternalLink,
  FileText,
  RefreshCw,
  Rocket,
  Server,
  Trash2,
  X,
  ChevronDown,
  ChevronRight,
} from "@lucide/vue";
import { api, openApp } from "../api";

type App = {
  id: string;
  instanceId?: string;
  slug: string;
  productSlug: string;
  status: string;
  publicPath?: string;
  hostPort?: number;
  portMappingEnabled?: boolean;
};
type Release = {
  id: string;
  releaseNumber: number;
  productVersionId: string;
  state: string;
  createdAt: string;
  jobState?: string;
};
type Dependency = {
  key: string;
  productId: string;
  serviceSlug: string;
  required: boolean;
};
type RuntimeSpec = {
  cpuCores?: number;
  memoryMiB?: number;
  dataVolumeGiB?: number;
  volumes?: Array<{ name: string; mountPath: string; sizeGiB: number }>;
  editableOptions?: {
    cpu?: boolean;
    memory?: boolean;
    dataVolume?: boolean;
    command?: boolean;
    dependencies?: boolean;
    environment?: boolean;
  };
  secretKeys?: string[];
  editableSecretKeys?: string[];
  secretDescriptions?: Record<string, string>;
  command?: string[];
  env?: Record<string, string>;
  editableEnvKeys?: string[];
  envDescriptions?: Record<string, string>;
  dependencies?: Dependency[];
};
type Version = {
  id: string;
  version: number;
  versionLabel: string;
  current: boolean;
  publishedAt: string;
  runtimeSpec: RuntimeSpec;
  routeSpec: Record<string, unknown>;
  healthSpec: Record<string, unknown>;
};
type Configuration = {
  current?: { runtimeSpec?: RuntimeSpec };
  configuredSecretKeys: string[];
};

const route = useRoute(),
  router = useRouter();
const windowHost = window.location.hostname;
const loading = ref(true),
  updating = ref(false),
  error = ref(""),
  message = ref(""),
  configExpanded = ref(false);
const app = ref<App | null>(null),
  releases = ref<Release[]>([]),
  versions = ref<Version[]>([]),
  configuredSecretKeys = ref<string[]>([]);
const currentRuntime = ref<RuntimeSpec>({});
const selectedVersionID = ref(""),
  cpu = ref(1),
  memory = ref(512),
  volume = ref(0),
  secrets = ref<Record<string, string>>({});
const envValues = ref<Record<string, string>>({});
const commandText = ref("");
const dependencies = ref<Dependency[]>([]);
const selectedVersion = computed(
  () =>
    versions.value.find((item) => item.id === selectedVersionID.value) || null,
);
const editableSecrets = computed(() => {
  const spec = selectedVersion.value?.runtimeSpec;
  return (spec?.secretKeys || []).filter((key) =>
    (spec?.editableSecretKeys || []).includes(key),
  );
});
const editableEnvKeys = computed(() => {
  const spec = selectedVersion.value?.runtimeSpec;
  if (spec?.editableOptions?.environment !== true) return [];
  return spec?.editableEnvKeys || [];
});
const editableEnvMap = computed(() => {
  const m: Record<string, string> = {};
  for (const k of editableEnvKeys.value)
    m[k] = selectedVersion.value?.runtimeSpec?.envDescriptions?.[k] || "";
  return m;
});
const currentVersion = computed(() =>
  versions.value.find((item) => item.current),
);
const containerPort = computed(() =>
  Number(selectedVersion.value?.routeSpec?.containerPort || 0),
);
const volumeFloor = computed(() =>
  Number(
    selectedVersion.value?.runtimeSpec?.dataVolumeGiB ||
      Math.max(
        0,
        ...(selectedVersion.value?.runtimeSpec?.volumes || []).map((item) =>
          Number(item.sizeGiB || 0),
        ),
      ),
  ),
);
const editableEnvCount = computed(() => editableEnvKeys.value.length);
const commandEditable = computed(
  () => selectedVersion.value?.runtimeSpec?.editableOptions?.command === true,
);
const dependenciesEditable = computed(
  () =>
    selectedVersion.value?.runtimeSpec?.editableOptions?.dependencies === true,
);
const portMappingAvailable = computed(() => {
  const rm = (selectedVersion.value?.routeSpec || {}) as Record<
    string,
    unknown
  >;
  const pm = (rm.portMapping || {}) as Record<string, unknown>;
  return pm.available === true;
});
const portMapping = ref(false);

function versionName(item: Version) {
  return item.versionLabel
    ? item.versionLabel + "（版本 " + item.version + "）"
    : "版本 " + item.version;
}
function statusText(status: string) {
  return (
    {
      running: "运行中",
      stopped: "已停止",
      failed: "部署失败",
      suspended: "已暂停",
      deploying: "部署中",
      updating: "更新中",
    }[status] || status
  );
}
function applyVersion() {
  const spec = selectedVersion.value?.runtimeSpec;
  if (!spec) return;
  cpu.value = Math.max(
    Number(currentRuntime.value.cpuCores || 0),
    Number(spec.cpuCores || 1),
  );
  memory.value = Math.max(
    Number(currentRuntime.value.memoryMiB || 0),
    Number(spec.memoryMiB || 512),
  );
  const currentVolume = Number(
    currentRuntime.value.dataVolumeGiB ||
      Math.max(
        0,
        ...(currentRuntime.value.volumes || []).map((item) =>
          Number(item.sizeGiB || 0),
        ),
      ),
  );
  volume.value = Math.max(currentVolume, volumeFloor.value);
  secrets.value = Object.fromEntries(
    editableSecrets.value.map((key) => [key, ""]),
  );
  const env: Record<string, string> = {};
  for (const key of spec.editableEnvKeys || []) env[key] = "";
  envValues.value = env;
  commandText.value = (spec.command || []).join(" ");
  dependencies.value = (spec.dependencies || []).map((d) => ({
    key: d.key,
    productId: d.productId,
    serviceSlug: d.serviceSlug,
    required: d.required,
  }));
}
watch(selectedVersionID, applyVersion);
async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await api<{ apps: App[] }>("/apps");
    app.value =
      result.apps.find(
        (item) =>
          item.instanceId === route.params.instanceId ||
          item.id === route.params.instanceId,
      ) || null;
    if (!app.value) {
      error.value = "应用不存在或无权访问";
      return;
    }
    portMapping.value = app.value.portMappingEnabled === true;
    const [releaseResult, versionResult, configuration] = await Promise.all([
      api<{ releases: Release[] }>("/apps/" + app.value.id + "/releases"),
      api<{ versions: Version[] }>("/apps/" + app.value.id + "/versions"),
      api<Configuration>("/apps/" + app.value.id + "/configuration"),
    ]);
    releases.value = releaseResult.releases;
    versions.value = versionResult.versions;
    currentRuntime.value = configuration.current?.runtimeSpec || {};
    configuredSecretKeys.value = configuration.configuredSecretKeys || [];
    selectedVersionID.value =
      (versions.value.find((item) => item.current) || versions.value[0])?.id ||
      "";
    applyVersion();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
async function updateApp() {
  if (!app.value || !selectedVersion.value || updating.value) return;
  error.value = "";
  message.value = "";
  updating.value = true;
  try {
    const spec = selectedVersion.value.runtimeSpec,
      resources: Record<string, unknown> = {};
    if (spec.editableOptions?.cpu !== false) resources.cpuCores = cpu.value;
    if (spec.editableOptions?.memory !== false)
      resources.memoryMiB = memory.value;
    if (
      spec.editableOptions?.dataVolume !== false &&
      (spec.volumes?.length || 0)
    )
      resources.dataVolumeGiB = volume.value;
    if (commandEditable.value && commandText.value.trim())
      resources.command = commandText.value.trim().split(/\s+/);
    if (editableEnvCount.value) {
      const environment = Object.fromEntries(
        editableEnvKeys.value
          .filter(
            (key) => envValues.value[key] && envValues.value[key].trim() !== "",
          )
          .map((key) => [key, envValues.value[key].trim()]),
      );
      if (Object.keys(environment).length) resources.environment = environment;
    }
    if (dependenciesEditable.value) resources.dependencies = dependencies.value;
    if (portMappingAvailable.value)
      resources.portMappingEnabled = portMapping.value;
    const changedSecrets = Object.fromEntries(
      Object.entries(secrets.value).filter(([, value]) => value.trim()),
    );
    await api("/apps/" + app.value.id + "/releases", {
      method: "POST",
      body: JSON.stringify({
        versionId: selectedVersion.value.id,
        idempotencyKey: crypto.randomUUID(),
        resources,
        secrets: changedSecrets,
      }),
    });
    message.value = "更新任务已创建，系统正在重新部署。现有数据卷会继续保留。";
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    updating.value = false;
  }
}
async function visitApp() {
  if (!app.value) return;
  try {
    await openApp(app.value.id);
  } catch (e) {
    error.value = (e as Error).message;
  }
}
function addDependency() {
  dependencies.value.push({
    key: "",
    productId: "",
    serviceSlug: "",
    required: false,
  });
}
function removeDependency(index: number) {
  dependencies.value.splice(index, 1);
}
const copied = ref(false);
async function copyInstanceId() {
  if (!app.value?.instanceId) return;
  try {
    await navigator.clipboard.writeText(app.value.instanceId);
    copied.value = true;
    setTimeout(() => {
      copied.value = false;
    }, 1500);
  } catch {
    /* ignore */
  }
}
const logOpen = ref(false),
  logBusy = ref(false),
  logData = ref<{
    logs: string;
    status: string;
    lastError?: string;
    sampledAt?: string;
  }>({ logs: "", status: "" });
let logTimer: number | undefined;
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
async function loadLogs() {
  if (!app.value) return;
  logBusy.value = true;
  try {
    logData.value = await api<{
      logs: string;
      status: string;
      lastError?: string;
      sampledAt?: string;
    }>("/apps/" + app.value.id + "/logs");
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
  if (!app.value) return;
  logBusy.value = true;
  try {
    const result = await api<{ status: string }>(
      "/apps/" + app.value.id + "/logs/refresh",
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
async function openLogs() {
  if (!app.value) return;
  logOpen.value = true;
  logData.value = { logs: "", status: "" };
  await refreshLogs();
  logTimer = window.setInterval(() => {
    void loadLogs();
  }, 3000);
}
function closeLogs() {
  logOpen.value = false;
  if (logTimer !== undefined) {
    window.clearInterval(logTimer);
    logTimer = undefined;
  }
}
onBeforeUnmount(() => {
  if (logTimer !== undefined) window.clearInterval(logTimer);
});
onMounted(load);
</script>

<template>
  <main class="workspace app-detail-page">
    <header class="app-detail-header">
      <div class="app-detail-title">
        <div class="app-detail-breadcrumb">
          <button
            class="secondary compact"
            @click="router.push('/console/apps')"
          >
            <ArrowLeft :size="15" />我的应用</button
          ><span class="eyebrow">应用实例</span>
        </div>
        <div class="app-name-row">
          <h1>{{ app?.slug || "应用详情" }}</h1>
          <small>{{ app?.productSlug || "" }}</small>
        </div>
        <div v-if="app?.instanceId" class="instance-id-row">
          <code>{{ app.instanceId }}</code
          ><button
            class="icon-action"
            :title="copied ? '已复制' : '复制实例 ID'"
            @click="copyInstanceId"
          >
            <Check v-if="copied" :size="14" /><Copy v-else :size="14" />
          </button>
        </div>
      </div>
      <div class="app-detail-actions">
        <button class="secondary compact" :disabled="!app" @click="openLogs">
          <FileText :size="15" />查看日志</button
        ><button class="secondary compact" :disabled="loading" @click="load">
          <RefreshCw :class="{ spin: loading }" :size="15" />刷新
        </button>
      </div>
    </header>
    <p v-if="error" class="message error-message">{{ error }}</p>
    <p v-if="message" class="message success-message">{{ message }}</p>
    <section v-if="loading" class="app-detail-skeleton" aria-busy="true">
      <div class="skeleton skeleton-title" style="width: 180px"></div>
      <div
        class="skeleton skeleton-text"
        style="width: 260px; margin-top: 10px"
      ></div>
      <div class="status-grid">
        <article
          class="skeleton"
          style="height: 76px; border-radius: 14px"
        ></article>
        <article
          class="skeleton"
          style="height: 76px; border-radius: 14px"
        ></article>
      </div>
      <div class="skeleton" style="height: 300px; border-radius: 16px"></div>
    </section>
    <template v-else-if="app">
      <section class="status-grid">
        <article class="status-card">
          <span class="status-icon"><Server :size="18" /></span>
          <div>
            <span class="status-label">运行状态</span
            ><strong class="status-value">{{ statusText(app.status) }}</strong>
          </div>
        </article>
        <article class="status-card">
          <span class="status-icon"><Rocket :size="18" /></span>
          <div>
            <span class="status-label">公网入口</span
            ><strong class="status-value"
              ><button
                v-if="app.publicPath"
                class="primary compact"
                @click="visitApp"
              >
                打开应用 <ExternalLink :size="14" /></button
              ><span v-else class="status-muted">未启用</span></strong
            ><a
              v-if="app.hostPort"
              class="direct-access"
              :href="'http://' + windowHost + ':' + app.hostPort"
              target="_blank"
              rel="noopener"
              @click.stop
              >直连 :{{ app.hostPort }}<ExternalLink :size="12"
            /></a>
          </div>
        </article>
      </section>
      <section class="config-section">
        <div class="config-section-head" @click="configExpanded = !configExpanded" style="cursor: pointer; user-select: none;">
          <div style="display: flex; align-items: center; gap: 8px;">
            <ChevronDown v-if="configExpanded" :size="20" style="color: var(--text-tertiary);" />
            <ChevronRight v-else :size="20" style="color: var(--text-tertiary);" />
            <div>
              <p class="eyebrow">配置更新</p>
              <h2>选择版本并重新部署</h2>
            </div>
          </div>
          <div class="current-version">
            当前 {{ currentVersion ? versionName(currentVersion) : "未知" }}
          </div>
        </div>
        <div v-show="configExpanded" class="config-expanded-wrapper">
        <div v-if="versions.length" class="config-form">
          <div class="version-row">
            <label class="version-select"
              ><span>目标版本</span
              ><select v-model="selectedVersionID">
                <option
                  v-for="item in versions"
                  :key="item.id"
                  :value="item.id"
                >
                  {{ versionName(item) }}{{ item.current ? " · 当前" : "" }}
                </option>
              </select></label
            >
            <p class="version-hint">
              <CircleAlert :size="14" />{{
                selectedVersionID === currentVersion?.id
                  ? "当前即运行版本，未发生版本变更"
                  : "将更新为 " + versionName(selectedVersion!)
              }}
            </p>
          </div>
          <div class="field-grid">
            <label
              ><span>CPU 核数</span
              ><input
                v-model.number="cpu"
                type="number"
                step="0.1"
                :min="selectedVersion?.runtimeSpec.cpuCores || 0.1"
                :disabled="
                  selectedVersion?.runtimeSpec.editableOptions?.cpu === false
                "
            /></label>
            <label
              ><span>内存（MiB）</span
              ><input
                v-model.number="memory"
                type="number"
                step="64"
                :min="selectedVersion?.runtimeSpec.memoryMiB || 64"
                :disabled="
                  selectedVersion?.runtimeSpec.editableOptions?.memory === false
                "
            /></label>
            <label v-if="selectedVersion?.runtimeSpec.volumes?.length"
              ><span>共享数据卷（GiB）</span
              ><input
                v-model.number="volume"
                type="number"
                step="1"
                :min="volumeFloor"
                :disabled="
                  selectedVersion?.runtimeSpec.editableOptions?.dataVolume ===
                  false
                "
            /></label>
            <div class="readonly-field">
              <span class="readonly-tag">只读</span
              ><span class="ro-label">容器内网端口</span
              ><strong>{{ containerPort || "未声明" }}</strong
              ><small>由内部网关反向代理，不映射宿主机</small>
            </div>
          </div>
          <div
            v-if="portMappingAvailable"
            class="switch-setting app-detail-portmapping"
          >
            <div>
              <strong>开启端口映射（直连）</strong
              ><small
                >在宿主机发布端口直连访问，端口每次部署自动分配；网关转发仍然有效</small
              >
            </div>
            <label class="switch"
              ><input v-model="portMapping" type="checkbox" /><span
            /></label>
          </div>
          <p
            class="field-note"
            v-if="
              selectedVersion?.runtimeSpec.editableOptions?.cpu === false ||
              selectedVersion?.runtimeSpec.editableOptions?.memory === false ||
              selectedVersion?.runtimeSpec.editableOptions?.dataVolume === false
            "
          >
            部分资源由管理员固定，不可调整。
          </p>
          <label v-if="commandEditable" class="wide-field"
            ><span>启动命令</span
            ><textarea
              v-model="commandText"
              placeholder="留空使用模板默认命令"
              rows="2"
            ></textarea></label>
          <div v-if="editableEnvCount" class="env-block">
            <div class="block-head">
              <strong>环境变量</strong><small>留空保持不变，仅提交非空值</small>
            </div>
            <label v-for="key in editableEnvKeys" :key="key"
              ><span>{{ key }}</span
              ><input
                v-model="envValues[key]"
                :placeholder="editableEnvMap[key] || '留空保持不变'"
            /></label>
          </div>
          <div v-if="dependenciesEditable" class="env-block">
            <div class="block-head">
              <strong>依赖服务</strong
              ><button
                type="button"
                class="secondary compact"
                @click="addDependency"
              >
                <span class="plus-label">+ 添加依赖</span>
              </button>
            </div>
            <article
              v-for="(dep, index) in dependencies"
              :key="index"
              class="dep-row"
            >
              <input v-model="dep.key" placeholder="依赖标识" /><input
                v-model="dep.productId"
                placeholder="依赖产品ID"
              /><input
                v-model="dep.serviceSlug"
                placeholder="服务名"
              /><button
                type="button"
                class="icon-action stop-action"
                @click="removeDependency(index)"
              >
                <Trash2 :size="15" />
              </button>
            </article>
          </div>
          <div v-if="editableSecrets.length" class="env-block">
            <div class="block-head">
              <strong>Secret</strong
              ><small>留空表示继续使用已保存值，服务端不返回明文</small>
            </div>
            <label v-for="key in editableSecrets" :key="key"
              ><span
                >{{ key }}
                <em v-if="configuredSecretKeys.includes(key)">已配置</em></span
              ><input
                v-model="secrets[key]"
                type="password"
                autocomplete="new-password"
                placeholder="留空则不修改"
            /></label>
          </div>
          <div class="config-actions">
            <p>
              <CircleAlert
                :size="15"
              />更新会创建新的不可变发布快照；失败时保留当前成功版本，现有数据卷不受影响。
            </p>
            <button
              class="primary"
              :disabled="updating || !['running', 'failed', 'stopped'].includes(app.status)"
              @click="updateApp"
            >
              <RefreshCw v-if="updating" class="spin" :size="16" />{{
                updating ? "正在创建更新任务..." : "更新并重新部署"
              }}
            </button>
          </div>
        </div>
        <div v-else class="context-empty compact-empty">
          <p>当前没有可用于更新的已发布版本</p>
        </div>
        </div>
      </section>
      <section class="history-section">
        <div class="section-heading">
          <div>
            <p class="eyebrow">发布记录</p>
            <h2>部署历史</h2>
          </div>
          <span>{{ releases.length }} 个版本</span>
        </div>
        <article
          v-for="release in releases"
          :key="release.id"
          class="history-row"
        >
          <span class="history-version">v{{ release.releaseNumber }}</span>
          <div>
            <strong>{{
              release.state === "active" ? "当前版本" : release.state
            }}</strong
            ><small
              >{{ release.productVersionId.slice(0, 8) }} ·
              {{ new Date(release.createdAt).toLocaleString() }}</small
            >
          </div>
          <span
            :class="[
              'status-pill',
              release.state === 'active' ? 'active' : 'pending',
            ]"
            >{{ release.state === "active" ? "生效" : "历史" }}</span
          >
        </article>
        <div v-if="!releases.length" class="context-empty compact-empty">
          <p>还没有发布记录</p>
        </div>
      </section>
    </template>
  </main>
  <div v-if="logOpen" class="modal-backdrop" @click.self="closeLogs">
    <section class="secret-dialog log-dialog">
      <header>
        <div>
          <p class="eyebrow">{{ app?.slug }}</p>
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
        ><button
          class="secondary compact"
          :disabled="logBusy"
          @click="refreshLogs"
        >
          <RefreshCw :class="{ spin: logBusy }" :size="15" />立即刷新
        </button>
      </div>
      <p v-if="logData.lastError" class="message">{{ logData.lastError }}</p>
      <p v-if="!logData.logs && !logBusy" class="quiet log-empty">
        正在拉取日志…
      </p>
      <pre class="log-viewer" :class="{ 'log-loading': logBusy }">{{
        logData.logs || (logBusy ? "正在拉取日志…" : "")
      }}</pre>
    </section>
  </div>
</template>

<style scoped>
.app-detail-page {
  display: grid;
  gap: 14px;
  padding-bottom: 40px;
}
.app-detail-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
}
.app-detail-title {
  display: grid;
  gap: 6px;
  min-width: 0;
}
.app-detail-breadcrumb {
  display: flex;
  align-items: center;
  gap: 12px;
}
.app-detail-breadcrumb .eyebrow {
  margin: 0;
  font-size: 12px;
  color: var(--text-muted);
}
.app-name-row {
  display: flex;
  align-items: baseline;
  gap: 12px;
  flex-wrap: wrap;
}
.app-name-row h1 {
  margin: 0;
  font-size: 22px;
}
.app-name-row small {
  color: var(--text-muted);
  font-size: 13px;
}
.instance-id-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.instance-id-row code {
  font-size: 12px;
  color: var(--text-muted);
  padding: 3px 8px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.04);
}
.instance-id-row .icon-action {
  width: 26px;
  height: 26px;
}
.app-detail-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
}
.app-detail-skeleton {
  display: grid;
  gap: 14px;
}
.status-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.status-card {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px 18px;
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: 14px;
}
.status-icon {
  width: 38px;
  height: 38px;
  flex: 0 0 38px;
  display: grid;
  place-items: center;
  border-radius: 11px;
  background: var(--accent-soft);
  color: var(--accent);
}
.status-card > div {
  display: grid;
  gap: 3px;
  min-width: 0;
}
.status-card .direct-access {
  justify-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-top: 4px;
  font-size: 12px;
  font-weight: 700;
  color: var(--accent);
  padding: 5px 10px;
  border: 1px solid var(--line-strong);
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.04);
  text-decoration: none;
}
.status-label {
  font-size: 12.5px;
  color: var(--text-muted);
}
.status-value {
  font-size: 16px;
  font-weight: 700;
}
.status-muted {
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 500;
}
.config-section,
.history-section {
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: 16px;
  padding: 20px 22px;
}
.config-section {
}
.config-section-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}
.config-section-head h2 {
  margin: 4px 0 0;
  font-size: 18px;
}
.current-version {
  font-size: 13px;
  color: var(--text-muted);
  padding: 6px 10px;
  border: 1px solid var(--line);
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.04);
  white-space: nowrap;
}
.config-form {
  display: grid;
  gap: 16px;
}
.version-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 14px;
  align-items: end;
}
.version-select {
  display: grid;
  gap: 7px;
}
.version-select > span,
.field-grid label > span,
.wide-field > span,
.env-block label > span {
  font-size: 13px;
  font-weight: 650;
}
.version-select select {
  width: 100%;
}
.version-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0 0 8px;
  color: var(--accent-dark);
  font-size: 12.5px;
  max-width: 280px;
  line-height: 1.5;
}
.field-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}
.field-grid label {
  display: grid;
  gap: 7px;
}
.field-grid input {
  width: 100%;
  box-sizing: border-box;
}
.readonly-field {
  display: grid;
  gap: 4px;
  padding: 10px 12px;
  border: 1px dashed var(--line-strong);
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.02);
}
.readonly-tag {
  justify-self: start;
  font-size: 10px;
  font-weight: 800;
  color: var(--text-muted);
  padding: 1px 6px;
  border-radius: 6px;
  border: 1px solid var(--line);
}
.ro-label {
  font-size: 12.5px;
  color: var(--text-muted);
}
.readonly-field strong {
  font-size: 16px;
}
.readonly-field small {
  color: var(--text-muted);
  font-size: 11px;
  line-height: 1.4;
}
.field-note {
  color: var(--amber);
  font-size: 12.5px;
  margin: 0;
}
.app-detail-portmapping {
  justify-content: flex-start;
  padding: 12px 14px;
  border: 1px dashed var(--line-strong);
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.02);
}
.wide-field {
  display: grid;
  gap: 7px;
}
.wide-field input {
  width: 100%;
}
.env-block {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  padding-top: 14px;
  border-top: 1px solid var(--line);
}
.env-block .block-head {
  grid-column: 1/-1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.block-head strong {
  font-size: 13px;
}
.block-head small {
  color: var(--text-muted);
  font-size: 12px;
}
.env-block label {
  display: grid;
  gap: 7px;
}
.env-block label > span {
  display: flex;
  align-items: baseline;
  gap: 6px;
}
.env-block em {
  font-style: normal;
  font-size: 11px;
  color: var(--text-muted);
}
.env-block input {
  width: 100%;
  box-sizing: border-box;
}
.plus-label {
  font-size: 12px;
  font-weight: 600;
}
.dep-row {
  display: grid;
  grid-template-columns: 1fr 1fr auto auto;
  gap: 10px;
  align-items: center;
  grid-column: 1/-1;
}
.dep-row input {
  width: 100%;
}
.dep-required {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}
.config-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--line);
}
.config-actions p {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin: 0;
  color: #a8b0ba;
  font-size: 12.5px;
  line-height: 1.5;
  max-width: 440px;
}
.config-actions button {
  flex: 0 0 auto;
}
.history-section .section-heading {
  margin-bottom: 0;
}
.history-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 9px 14px;
  border-top: 1px solid var(--line);
  min-height: 44px;
}
.history-row:first-of-type {
  border-top: 0;
}
.history-version {
  font-size: 12px;
  font-weight: 800;
  color: var(--text-muted);
  width: 34px;
}
.history-row > div {
  display: grid;
  gap: 2px;
  flex: 1;
}
.history-row strong {
  font-size: 13px;
}
.history-row small {
  color: var(--text-muted);
  font-size: 11.5px;
}
.success-message {
  color: #34d399;
}
.error-message {
  color: #f87171;
}
@media (max-width: 900px) {
  .field-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .version-row {
    grid-template-columns: 1fr;
  }
  .dep-row {
    grid-template-columns: 1fr 1fr;
  }
}
@media (max-width: 620px) {
  .field-grid,
  .env-block {
    grid-template-columns: 1fr;
  }
  .app-detail-header {
    flex-direction: column;
  }
  .app-detail-actions {
    width: 100%;
  }
  .app-detail-actions button {
    flex: 1;
  }
  .config-actions {
    flex-direction: column;
    align-items: stretch;
  }
  .config-actions button {
    width: 100%;
  }
  .dep-row {
    grid-template-columns: 1fr;
  }
  .dep-required {
    justify-content: flex-start;
  }
}
</style>
