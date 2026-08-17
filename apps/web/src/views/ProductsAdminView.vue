<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import {
  AppWindow,
  Archive,
  ArrowLeft,
  Box,
  CheckCircle2,
  CircleAlert,
  Database,
  FileDown,
  HeartPulse,
  KeyRound,
  Link2,
  LogOut,
  LoaderCircle,
  Network,
  Pencil,
  Plus,
  Rocket,
  RotateCcw,
  Save,
  Settings2,
  Terminal,
  Trash2,
  X,
} from "@lucide/vue";
import { api, logout } from "../api";
import BrandMark from "../components/BrandMark.vue";

type Volume = { name: string; mountPath: string; sizeGiB: number };
type Dependency = {
  key: string;
  productId: string;
  serviceSlug: string;
  required: boolean;
};
type RuntimeSpec = {
  cpuCores?: number;
  memoryMiB?: number;
  systemDiskGiB?: number;
  command?: string[];
  env?: Record<string, string>;
  editableEnvKeys?: string[];
  secretKeys?: string[];
  volumes?: Volume[];
  dependencies?: Dependency[];
};
type RouteSpec = {
  containerPort?: number;
  basePath?: string;
  stripPrefix?: boolean;
  websocket?: boolean;
  sse?: boolean;
  cookiePath?: string;
};
type HealthSpec = {
  path?: string;
  intervalSeconds?: number;
  timeoutSeconds?: number;
};
type UpdateSpec = { dataPolicy?: string };
type Version = {
  id: string;
  version: number;
  imageDigest: string;
  runtimeSpec: RuntimeSpec;
  routeSpec: RouteSpec;
  healthSpec: HealthSpec;
  updateSpec: UpdateSpec;
  publishedAt: string | null;
  latestTest?: {
    id: string;
    state: string;
    attempts: number;
    lastError?: string | null;
    completedAt?: string | null;
  };
};
type Product = {
  id: string;
  slug: string;
  name: string;
  status: string;
  versions: Version[];
};
type KeyValue = { key: string; value: string; editable: boolean };
type VersionForm = {
  imageDigest: string;
  cpuCores: number;
  memoryMiB: number;
  systemDiskGiB: number;
  command: { value: string }[];
  environment: KeyValue[];
  secrets: { key: string }[];
  volumes: Volume[];
  dependencies: Dependency[];
  containerPort: number;
  basePath: string;
  stripPrefix: boolean;
  websocket: boolean;
  sse: boolean;
  cookiePath: string;
  healthPath: string;
  intervalSeconds: number;
  timeoutSeconds: number;
  dataPolicy: "stateless" | "volume_compatible" | "backup_required";
};

const products = ref<Product[]>([]);
const error = ref(""),
  message = ref(""),
  busy = ref(""),
  selected = ref("");
const product = reactive({ name: "", slug: "" });
const versionForm = reactive<VersionForm>(defaultVersionForm());
const testVersion = ref<Version | null>(null);
const testProductID = ref("");
const testSecrets = reactive<Record<string, string>>({});
const editingProduct = ref<Product | null>(null);
const editName = ref("");
const lifecycleProduct = ref<Product | null>(null);
const templateInput = ref<HTMLInputElement | null>(null);
const templateSummary = ref<string[]>([]);
let pollTimer: number | undefined;
const selectedProduct = computed(() =>
  products.value.find((item) => item.id === selected.value),
);
const dependencyProducts = computed(() =>
  products.value.filter(
    (item) => item.id !== selected.value && item.status === "published",
  ),
);

function defaultVersionForm(): VersionForm {
  return {
    imageDigest: "",
    cpuCores: 1,
    memoryMiB: 512,
    systemDiskGiB: 5,
    command: [],
    environment: [],
    secrets: [],
    volumes: [],
    dependencies: [],
    containerPort: 8080,
    basePath: "/",
    stripPrefix: true,
    websocket: true,
    sse: true,
    cookiePath: "/",
    healthPath: "/health",
    intervalSeconds: 10,
    timeoutSeconds: 5,
    dataPolicy: "volume_compatible",
  };
}
function resetVersionForm() {
  Object.assign(versionForm, defaultVersionForm());
  templateSummary.value = [];
}
function numberValue(value: unknown, fallback: number) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}
function openTemplateImport() {
  templateInput.value?.click();
}
async function importTemplate(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) return;
  try {
    const data = JSON.parse(await file.text()) as Record<string, any>;
    const productData = data.product || data.application || {};
    const productSlug = String(
      productData.slug || data.productSlug || data.slug || "",
    )
      .trim()
      .toLowerCase();
    const productName = String(
      productData.name || data.productName || data.name || "",
    ).trim();
    const existing = products.value.find(
      (item) => item.id === data.productId || item.slug === productSlug,
    );
    if (existing) selected.value = existing.id;
    else {
      selected.value = "";
      product.name = productName;
      product.slug = productSlug;
    }

    const source = data.version || data;
    const runtime =
      source.runtimeSpec || source.runtime || source.resources || {};
    const route = source.routeSpec || source.route || {};
    const health = source.healthSpec || source.health || {};
    const update = source.updateSpec || source.update || {};
    const env = runtime.env || runtime.environment || {};
    const editable = new Set(
      Array.isArray(runtime.editableEnvKeys)
        ? runtime.editableEnvKeys.map(String)
        : [],
    );
    const secretSource = runtime.secretKeys || runtime.secrets || [];
    const volumeSource = runtime.volumes || [];
    const dependencySource = runtime.dependencies || [];

    Object.assign(versionForm, {
      imageDigest: String(source.imageDigest || source.image || ""),
      cpuCores: numberValue(runtime.cpuCores, 1),
      memoryMiB: numberValue(runtime.memoryMiB, 512),
      systemDiskGiB: numberValue(runtime.systemDiskGiB, 5),
      command: Array.isArray(runtime.command)
        ? runtime.command.map((value: unknown) => ({ value: String(value) }))
        : [],
      environment: Object.entries(env).map(([key, value]) => ({
        key,
        value: String(value),
        editable: editable.has(key),
      })),
      secrets: (Array.isArray(secretSource)
        ? secretSource
        : Object.keys(secretSource)
      ).map((key: unknown) => ({ key: String(key) })),
      volumes: Array.isArray(volumeSource)
        ? volumeSource.map((volume: any) => ({
            name: String(volume.name || "data"),
            mountPath: String(volume.mountPath || "/data"),
            sizeGiB: numberValue(volume.sizeGiB, 10),
          }))
        : [],
      containerPort: numberValue(
        route.containerPort ?? runtime.containerPort,
        8080,
      ),
      basePath: String(route.basePath || "/"),
      stripPrefix: route.stripPrefix !== false,
      websocket: route.websocket !== false,
      sse: route.sse !== false,
      cookiePath: String(route.cookiePath ?? "/"),
      healthPath: String(health.path ?? "/health"),
      intervalSeconds: numberValue(health.intervalSeconds, 10),
      timeoutSeconds: numberValue(health.timeoutSeconds, 5),
      dataPolicy: [
        "stateless",
        "volume_compatible",
        "backup_required",
      ].includes(update.dataPolicy)
        ? update.dataPolicy
        : "volume_compatible",
    });
    versionForm.dependencies = Array.isArray(dependencySource)
      ? dependencySource.flatMap((dependency: any) => {
          const target = products.value.find(
            (item) =>
              item.id === dependency.productId ||
              item.slug === dependency.productSlug,
          );
          return target
            ? [
                {
                  key: String(dependency.key || target.slug),
                  productId: target.id,
                  serviceSlug: String(dependency.serviceSlug || target.slug),
                  required: dependency.required !== false,
                },
              ]
            : [];
        })
      : [];
    if (versionForm.dataPolicy === "stateless") versionForm.volumes = [];

    templateSummary.value = [
      source.imageDigest || source.image ? "镜像" : "镜像待填写",
      `资源 ${versionForm.cpuCores} 核 / ${versionForm.memoryMiB} MiB / ${versionForm.systemDiskGiB} GiB`,
      `环境变量 ${versionForm.environment.length} 项`,
      `Secret ${versionForm.secrets.length} 项`,
      `数据卷 ${versionForm.volumes.length} 项`,
      `依赖 ${versionForm.dependencies.length} 项`,
      `容器内网端口 ${versionForm.containerPort}`,
    ];
    done(
      existing
        ? `模板已导入到 ${existing.name} 的新版本表单，请检查后创建`
        : "模板已解析，请先创建产品，再检查并创建版本",
    );
  } catch (value) {
    failed(value);
  } finally {
    input.value = "";
  }
}
function done(value: string) {
  message.value = value;
  error.value = "";
}
function failed(value: unknown) {
  error.value = (value as Error).message;
  message.value = "";
}
async function load(silent = false) {
  try {
    products.value = (
      await api<{ products: Product[] }>("/admin/products")
    ).products;
    if (!selected.value && products.value.length)
      selected.value = products.value[0].id;
  } catch (value) {
    if (!silent) failed(value);
  }
}
onMounted(load);
onMounted(() => {
  pollTimer = window.setInterval(() => {
    if (
      products.value.some((item) =>
        item.versions.some((version) => isTestRunning(version)),
      )
    )
      load(true);
  }, 2500);
});
onBeforeUnmount(() => {
  if (pollTimer !== undefined) window.clearInterval(pollTimer);
});
async function createProduct() {
  try {
    busy.value = "product";
    const created = await api<{ id: string }>("/admin/products", {
      method: "POST",
      body: JSON.stringify(product),
    });
    Object.assign(product, { name: "", slug: "" });
    selected.value = created.id;
    done("产品已创建");
    await load();
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
function productStatusLabel(status: string) {
  return (
    (
      {
        draft: "草稿",
        testing: "测试中",
        published: "已上架",
        retired: "已下架",
      } as Record<string, string>
    )[status] || status
  );
}
function productStatusClass(status: string) {
  if (status === "published") return "active";
  if (status === "testing") return "pending";
  return "suspended";
}
function openEditProduct(item: Product) {
  editingProduct.value = item;
  editName.value = item.name;
}
function closeEditProduct() {
  editingProduct.value = null;
  editName.value = "";
}
async function saveProductName() {
  if (!editingProduct.value) return;
  try {
    busy.value = "edit-product";
    await api(`/admin/products/${editingProduct.value.id}`, {
      method: "PATCH",
      body: JSON.stringify({ name: editName.value }),
    });
    closeEditProduct();
    done("产品名称已更新");
    await load();
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
function openProductAvailability(item: Product) {
  lifecycleProduct.value = item;
}
function closeProductAvailability() {
  lifecycleProduct.value = null;
}
async function updateProductAvailability() {
  if (!lifecycleProduct.value) return;
  const enabled = lifecycleProduct.value.status === "retired";
  try {
    busy.value = "product-availability";
    await api(`/admin/products/${lifecycleProduct.value.id}/availability`, {
      method: "PATCH",
      body: JSON.stringify({ enabled }),
    });
    closeProductAvailability();
    done(enabled ? "产品已恢复" : "产品已下架");
    await load();
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
function removeAt<T>(items: T[], index: number) {
  items.splice(index, 1);
}
function addVolume() {
  if (versionForm.dataPolicy === "stateless")
    versionForm.dataPolicy = "volume_compatible";
  versionForm.volumes.push({ name: "data", mountPath: "/data", sizeGiB: 10 });
}
function addDependency() {
  const target = dependencyProducts.value[0];
  if (!target) return;
  versionForm.dependencies.push({
    key: target.slug,
    productId: target.id,
    serviceSlug: target.slug,
    required: true,
  });
}
function applyDataPolicy() {
  if (versionForm.dataPolicy === "stateless") versionForm.volumes.splice(0);
}
function applyPrefixMode() {
  if (!versionForm.stripPrefix) versionForm.basePath = "/";
}
function uniqueEntries(entries: KeyValue[]) {
  const values: Record<string, string> = {};
  for (const entry of entries) {
    const key = entry.key.trim();
    if (!key) continue;
    if (Object.prototype.hasOwnProperty.call(values, key))
      throw new Error(`环境变量 ${key} 重复`);
    values[key] = entry.value;
  }
  return values;
}
function editableEnvironmentKeys() {
  return versionForm.environment
    .filter((entry) => entry.editable && entry.key.trim())
    .map((entry) => entry.key.trim());
}
function secretKeys() {
  const values = versionForm.secrets
    .map((entry) => entry.key.trim().toUpperCase())
    .filter(Boolean);
  if (new Set(values).size !== values.length)
    throw new Error("Secret 名称不能重复");
  return values;
}
function dependencies() {
  const values = versionForm.dependencies.map((entry) => ({
    key: entry.key.trim().toLowerCase(),
    productId: entry.productId,
    serviceSlug: entry.serviceSlug.trim().toLowerCase(),
    required: entry.required,
  }));
  if (new Set(values.map((entry) => entry.key)).size !== values.length)
    throw new Error("依赖标识不能重复");
  if (new Set(values.map((entry) => entry.serviceSlug)).size !== values.length)
    throw new Error("依赖服务名不能重复");
  return values;
}
async function createVersion() {
  if (!selected.value) {
    error.value = "请先选择产品";
    return;
  }
  try {
    busy.value = "version";
    const runtimeSpec: RuntimeSpec = {
      cpuCores: versionForm.cpuCores,
      memoryMiB: versionForm.memoryMiB,
      systemDiskGiB: versionForm.systemDiskGiB,
      volumes: versionForm.volumes.map((volume) => ({
        name: volume.name.trim().toLowerCase(),
        mountPath: volume.mountPath.trim(),
        sizeGiB: volume.sizeGiB,
      })),
      env: uniqueEntries(versionForm.environment),
      editableEnvKeys: editableEnvironmentKeys(),
      secretKeys: secretKeys(),
      dependencies: dependencies(),
    };
    const command = versionForm.command
      .map((argument) => argument.value.trim())
      .filter(Boolean);
    if (command.length) runtimeSpec.command = command;
    await api("/admin/products/" + selected.value + "/versions", {
      method: "POST",
      body: JSON.stringify({
        imageDigest: versionForm.imageDigest.trim(),
        runtimeSpec,
        routeSpec: {
          containerPort: versionForm.containerPort,
          basePath: versionForm.basePath.trim() || "/",
          stripPrefix: versionForm.stripPrefix,
          websocket: versionForm.websocket,
          sse: versionForm.sse,
          cookiePath: versionForm.cookiePath.trim(),
        },
        healthSpec: {
          path: versionForm.healthPath.trim(),
          intervalSeconds: versionForm.intervalSeconds,
          timeoutSeconds: versionForm.timeoutSeconds,
        },
        updateSpec: { dataPolicy: versionForm.dataPolicy },
      }),
    });
    resetVersionForm();
    done("新版本已创建");
    await load();
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function publish(productID: string, item: Version) {
  try {
    busy.value = item.id;
    await api(
      "/admin/products/" + productID + "/versions/" + item.id + "/publish",
      { method: "POST" },
    );
    done("版本已发布并上架");
    await load();
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
function isTestRunning(item: Version) {
  return (
    !!item.latestTest &&
    ["queued", "pulling", "starting", "health_checking"].includes(
      item.latestTest.state,
    )
  );
}
function testStateLabel(item: Version) {
  if (item.publishedAt) return "已发布";
  const state = item.latestTest?.state;
  return (
    (
      {
        succeeded: "测试通过",
        failed: "测试失败",
        queued: "排队中",
        pulling: "拉取镜像",
        starting: "启动容器",
        health_checking: "健康检查",
      } as Record<string, string>
    )[state || ""] || "待测试"
  );
}
function testStateClass(item: Version) {
  if (item.publishedAt || item.latestTest?.state === "succeeded")
    return "active";
  if (item.latestTest?.state === "failed") return "danger";
  if (isTestRunning(item)) return "pending";
  return "suspended";
}
function openTest(item: Version) {
  if (!selected.value) return;
  testProductID.value = selected.value;
  testVersion.value = item;
  for (const key of item.runtimeSpec.secretKeys || []) testSecrets[key] = "";
}
function closeTest() {
  testVersion.value = null;
  testProductID.value = "";
  for (const key of Object.keys(testSecrets)) delete testSecrets[key];
}
async function startTest() {
  if (!testProductID.value || !testVersion.value) return;
  try {
    busy.value = "test-" + testVersion.value.id;
    await api(
      `/admin/products/${testProductID.value}/versions/${testVersion.value.id}/tests`,
      { method: "POST", body: JSON.stringify({ secrets: { ...testSecrets } }) },
    );
    closeTest();
    done("测试部署已排队，完成后才可发布");
    await load();
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
function dataPolicyLabel(value?: string) {
  return (
    {
      stateless: "无状态",
      volume_compatible: "卷兼容更新",
      backup_required: "更新前备份",
    }[value || "volume_compatible"] || value
  );
}
</script>

<template>
  <main class="workspace admin-workspace">
    <header>
      <div>
        <p class="eyebrow">应用目录</p>
        <h1>产品与版本</h1>
      </div>
      <div class="admin-template-actions">
        <input
          ref="templateInput"
          class="visually-hidden"
          type="file"
          accept="application/json,.json"
          @change="importTemplate"
        />
        <button
          class="secondary compact"
          type="button"
          @click="openTemplateImport"
        >
          <FileDown :size="16" />从模板导入
        </button>
      </div>
    </header>
    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok">{{ message }}</p>
    <div class="product-admin-layout">
      <section class="product-sidebar">
        <div class="section-heading">
          <div>
            <p class="eyebrow">产品</p>
            <h2>应用目录</h2>
          </div>
          <span>{{ products.length }}</span>
        </div>
        <button
          v-for="item in products"
          :key="item.id"
          :class="[
            'product-select',
            selected === item.id && 'selected',
            item.status === 'retired' && 'retired',
          ]"
          @click="selected = item.id"
        >
          <Box :size="18" /><span
            ><strong>{{ item.name }}</strong
            ><small
              >{{ item.slug }} · {{ productStatusLabel(item.status) }}</small
            ></span
          >
        </button>
        <p v-if="!products.length" class="quiet empty-copy">还没有产品</p>
      </section>
      <div class="product-admin-main">
        <section class="form-panel compact-panel">
          <h2><Plus :size="19" />创建产品</h2>
          <form class="horizontal-form" @submit.prevent="createProduct">
            <label
              >产品名称<input
                v-model="product.name"
                required
                placeholder="Open WebUI"
            /></label>
            <label
              >产品标识<input
                v-model="product.slug"
                required
                maxlength="63"
                pattern="[a-z0-9][a-z0-9-]{0,62}"
                placeholder="open-webui"
                @blur="product.slug = product.slug.trim().toLowerCase()"
            /></label>
            <button class="primary compact" :disabled="busy === 'product'">
              <Save :size="16" />创建
            </button>
          </form>
        </section>
        <section v-if="selected" class="form-panel version-builder">
          <div class="builder-heading">
            <div>
              <p class="eyebrow">{{ selectedProduct?.name }}</p>
              <h2><Rocket :size="19" />添加版本</h2>
            </div>
            <div v-if="selectedProduct" class="builder-heading-actions">
              <span
                :class="[
                  'status-pill',
                  productStatusClass(selectedProduct.status),
                ]"
                >{{ productStatusLabel(selectedProduct.status) }}</span
              >
              <button
                class="icon-action"
                type="button"
                title="编辑产品名称"
                @click="openEditProduct(selectedProduct)"
              >
                <Pencil :size="16" />
              </button>
              <button
                class="icon-action"
                type="button"
                :title="
                  selectedProduct.status === 'retired' ? '恢复产品' : '下架产品'
                "
                @click="openProductAvailability(selectedProduct)"
              >
                <RotateCcw
                  v-if="selectedProduct.status === 'retired'"
                  :size="16"
                /><Archive v-else :size="16" />
              </button>
            </div>
          </div>
          <div
            v-if="templateSummary.length"
            class="import-summary admin-import-summary"
          >
            <strong>模板配置已回填</strong>
            <small>{{ templateSummary.join(" · ") }}</small>
            <small>导入只填写管理员表单，不会自动创建、测试或发布。</small>
          </div>
          <div
            v-if="selectedProduct?.status === 'retired'"
            class="product-retired-notice"
          >
            <Archive :size="22" />
            <div>
              <strong>产品已下架</strong
              ><small
                >用户目录不再展示此产品，现有应用与历史版本保持不变。恢复后可以继续创建版本。</small
              >
            </div>
          </div>
          <form v-else @submit.prevent="createVersion">
            <section class="config-section first">
              <div class="config-heading">
                <Settings2 :size="18" />
                <div>
                  <strong>镜像与资源</strong
                  ><small>镜像必须固定到 SHA-256 Digest</small>
                </div>
              </div>
              <label
                >镜像 Digest<input
                  v-model="versionForm.imageDigest"
                  required
                  placeholder="ghcr.io/org/app@sha256:..."
              /></label>
              <div class="config-grid three">
                <label
                  >最低 CPU 核心<input
                    v-model.number="versionForm.cpuCores"
                    type="number"
                    min="0.1"
                    max="64"
                    step="0.1"
                    required
                /></label>
                <label
                  >最低内存 MiB<input
                    v-model.number="versionForm.memoryMiB"
                    type="number"
                    min="64"
                    max="262144"
                    step="64"
                    required
                /></label>
                <label
                  >最低系统盘 GiB<input
                    v-model.number="versionForm.systemDiskGiB"
                    type="number"
                    min="1"
                    max="1024"
                    step="1"
                    required
                /></label>
              </div>
            </section>
            <section class="config-section">
              <div class="config-heading with-action">
                <Terminal :size="18" />
                <div>
                  <strong>启动命令</strong
                  ><small>留空时使用镜像默认命令，每一行是一个参数</small>
                </div>
                <button
                  type="button"
                  class="secondary compact"
                  @click="versionForm.command.push({ value: '' })"
                >
                  <Plus :size="15" />参数
                </button>
              </div>
              <div class="repeat-list">
                <div
                  v-for="(argument, index) in versionForm.command"
                  :key="index"
                  class="repeat-row command-row"
                >
                  <input
                    v-model="argument.value"
                    :placeholder="index === 0 ? 'python' : '-m'"
                  /><button
                    type="button"
                    class="icon-action"
                    title="删除参数"
                    @click="removeAt(versionForm.command, index)"
                  >
                    <Trash2 :size="16" />
                  </button>
                </div>
                <p
                  v-if="!versionForm.command.length"
                  class="quiet empty-inline"
                >
                  使用镜像默认命令
                </p>
              </div>
            </section>
            <section class="config-section">
              <div class="config-heading with-action">
                <Settings2 :size="18" />
                <div>
                  <strong>普通环境变量</strong
                  ><small>可选择是否向用户展示并允许覆盖默认值</small>
                </div>
                <button
                  type="button"
                  class="secondary compact"
                  @click="
                    versionForm.environment.push({
                      key: '',
                      value: '',
                      editable: false,
                    })
                  "
                >
                  <Plus :size="15" />变量
                </button>
              </div>
              <div class="repeat-list">
                <div
                  v-for="(entry, index) in versionForm.environment"
                  :key="index"
                  class="repeat-row key-value-row editable-env-row"
                >
                  <input v-model="entry.key" placeholder="APP_MODE" /><input
                    v-model="entry.value"
                    placeholder="production"
                  /><label class="toggle"
                    ><input
                      v-model="entry.editable"
                      type="checkbox"
                    />用户可改</label
                  ><button
                    type="button"
                    class="icon-action"
                    title="删除变量"
                    @click="removeAt(versionForm.environment, index)"
                  >
                    <Trash2 :size="16" />
                  </button>
                </div>
                <p
                  v-if="!versionForm.environment.length"
                  class="quiet empty-inline"
                >
                  没有普通环境变量
                </p>
              </div>
            </section>
            <section class="config-section">
              <div class="config-heading with-action">
                <KeyRound :size="18" />
                <div>
                  <strong>部署 Secret</strong
                  ><small>用户部署时必须填写，平台加密保存且不回显</small>
                </div>
                <button
                  type="button"
                  class="secondary compact"
                  @click="versionForm.secrets.push({ key: '' })"
                >
                  <Plus :size="15" />Secret
                </button>
              </div>
              <div class="repeat-list">
                <div
                  v-for="(entry, index) in versionForm.secrets"
                  :key="index"
                  class="repeat-row command-row"
                >
                  <input
                    v-model="entry.key"
                    placeholder="API_KEY"
                    @blur="entry.key = entry.key.trim().toUpperCase()"
                  /><button
                    type="button"
                    class="icon-action"
                    title="删除 Secret"
                    @click="removeAt(versionForm.secrets, index)"
                  >
                    <Trash2 :size="16" />
                  </button>
                </div>
                <p
                  v-if="!versionForm.secrets.length"
                  class="quiet empty-inline"
                >
                  部署时不要求 Secret
                </p>
              </div>
            </section>
            <section class="config-section">
              <div class="config-heading with-action">
                <Link2 :size="18" />
                <div>
                  <strong>依赖服务</strong
                  ><small>绑定同账户内的固定服务名</small>
                </div>
                <button
                  type="button"
                  class="secondary compact"
                  :disabled="!dependencyProducts.length"
                  @click="addDependency"
                >
                  <Plus :size="15" />依赖
                </button>
              </div>
              <div class="repeat-list">
                <div
                  v-for="(dependency, index) in versionForm.dependencies"
                  :key="index"
                  class="repeat-row dependency-row"
                >
                  <label
                    >依赖标识<input
                      v-model="dependency.key"
                      required
                      pattern="[a-z][a-z0-9-]{0,31}"
                      placeholder="model-api" /></label
                  ><label
                    >目标产品<select v-model="dependency.productId" required>
                      <option
                        v-for="target in dependencyProducts"
                        :key="target.id"
                        :value="target.id"
                      >
                        {{ target.name }} · {{ target.slug }}
                      </option>
                    </select></label
                  ><label
                    >固定服务名<input
                      v-model="dependency.serviceSlug"
                      required
                      pattern="[a-z0-9][a-z0-9-]{0,62}"
                      placeholder="ollama" /></label
                  ><label class="toggle dependency-required"
                    ><input
                      v-model="dependency.required"
                      type="checkbox"
                    />部署前必须运行</label
                  ><button
                    type="button"
                    class="icon-action"
                    title="删除依赖"
                    @click="removeAt(versionForm.dependencies, index)"
                  >
                    <Trash2 :size="16" />
                  </button>
                </div>
                <p
                  v-if="!versionForm.dependencies.length"
                  class="quiet empty-inline"
                >
                  没有依赖服务
                </p>
              </div>
            </section>
            <section class="config-section">
              <div class="config-heading with-action">
                <Database :size="18" />
                <div>
                  <strong>数据卷与更新</strong
                  ><small>命名卷由平台创建，不允许宿主机路径</small>
                </div>
                <button
                  type="button"
                  class="secondary compact"
                  @click="addVolume"
                >
                  <Plus :size="15" />数据卷
                </button>
              </div>
              <label
                >数据策略<select
                  v-model="versionForm.dataPolicy"
                  @change="applyDataPolicy"
                >
                  <option value="stateless">无状态，不使用数据卷</option>
                  <option value="volume_compatible">
                    版本间兼容现有数据卷
                  </option>
                  <option value="backup_required">
                    更新或回退前必须完成备份
                  </option>
                </select></label
              >
              <div class="repeat-list volume-list">
                <div
                  v-for="(volume, index) in versionForm.volumes"
                  :key="index"
                  class="repeat-row volume-row"
                >
                  <label
                    >卷名称<input
                      v-model="volume.name"
                      placeholder="data" /></label
                  ><label
                    >容器路径<input
                      v-model="volume.mountPath"
                      placeholder="/data" /></label
                  ><label
                    >容量 GiB<input
                      v-model.number="volume.sizeGiB"
                      type="number"
                      min="1"
                      max="16384"
                      step="1" /></label
                  ><button
                    type="button"
                    class="icon-action"
                    title="删除数据卷"
                    @click="removeAt(versionForm.volumes, index)"
                  >
                    <Trash2 :size="16" />
                  </button>
                </div>
                <p
                  v-if="!versionForm.volumes.length"
                  class="quiet empty-inline"
                >
                  没有持久化数据卷
                </p>
              </div>
            </section>
            <section class="config-section">
              <div class="config-heading">
                <Network :size="18" />
                <div>
                  <strong>路径路由</strong
                  ><small
                    >公网只经统一 Gateway 和 OpenResty
                    进入，应用容器不暴露宿主机端口</small
                  >
                </div>
              </div>
              <div class="config-grid three">
                <label
                  >容器内网监听端口<input
                    v-model.number="versionForm.containerPort"
                    type="number"
                    min="1"
                    max="65535"
                    step="1"
                    required
                  /><small
                    >由镜像决定，仅 Docker 内网可达；同用户容器使用固定服务名 +
                    此端口访问</small
                  ></label
                ><label
                  >内部 Base Path<input
                    v-model="versionForm.basePath"
                    required
                    placeholder="/" /></label
                ><label
                  >Cookie Path<input
                    v-model="versionForm.cookiePath"
                    placeholder="/"
                  /><small>留空不改写 Cookie Path</small></label
                >
              </div>
              <p class="network-notice">
                公网访问路径固定为
                /apps/{user_slug}/{app_slug}/*；平台不会为应用创建 ports
                映射，也不会注入 PORT 环境变量。
              </p>
              <div class="toggle-grid">
                <label class="toggle"
                  ><input
                    v-model="versionForm.stripPrefix"
                    type="checkbox"
                    @change="applyPrefixMode"
                  />移除平台应用前缀</label
                ><label class="toggle"
                  ><input v-model="versionForm.websocket" type="checkbox" />允许
                  WebSocket</label
                ><label class="toggle"
                  ><input v-model="versionForm.sse" type="checkbox" />允许 SSE
                  流式响应</label
                >
              </div>
            </section>
            <section class="config-section">
              <div class="config-heading">
                <HeartPulse :size="18" />
                <div>
                  <strong>健康检查</strong
                  ><small>通过后才会原子切换用户访问路由</small>
                </div>
              </div>
              <div class="config-grid three">
                <label
                  >检查路径<input
                    v-model="versionForm.healthPath"
                    placeholder="/health"
                  /><small>留空仅检查容器运行状态</small></label
                ><label
                  >检查间隔（秒）<input
                    v-model.number="versionForm.intervalSeconds"
                    type="number"
                    min="1"
                    max="120"
                    step="1"
                    required /></label
                ><label
                  >超时（秒）<input
                    v-model.number="versionForm.timeoutSeconds"
                    type="number"
                    min="1"
                    max="30"
                    step="1"
                    required
                /></label>
              </div>
            </section>
            <div class="builder-actions">
              <button
                type="button"
                class="secondary compact"
                @click="resetVersionForm"
              >
                重置</button
              ><button class="primary compact" :disabled="busy === 'version'">
                <Plus :size="16" />创建不可变版本
              </button>
            </div>
          </form>
        </section>
        <section v-if="selectedProduct" class="version-list">
          <div class="section-heading version-heading">
            <div>
              <p class="eyebrow">发布记录</p>
              <h2>产品版本</h2>
            </div>
            <span>{{ selectedProduct.versions.length }} 个版本</span>
          </div>
          <article
            v-for="item in selectedProduct.versions"
            :key="item.id"
            :class="[
              'version-row',
              item.latestTest?.state === 'failed' && 'version-row-failed',
            ]"
          >
            <span class="version-number">v{{ item.version }}</span>
            <div class="version-copy">
              <strong>{{ item.imageDigest.split("@")[0] }}</strong>
              <small
                >最低 {{ item.runtimeSpec.cpuCores || 1 }} 核 ·
                {{ item.runtimeSpec.memoryMiB || 512 }} MiB · 容器内网端口
                {{ item.routeSpec.containerPort || 8080 }} ·
                {{ dataPolicyLabel(item.updateSpec?.dataPolicy) }}</small
              >
              <small class="digest-copy">{{ item.imageDigest }}</small>
            </div>
            <div class="version-state">
              <LoaderCircle
                v-if="isTestRunning(item)"
                class="spin"
                :size="16"
              />
              <span :class="['status-pill', testStateClass(item)]">{{
                testStateLabel(item)
              }}</span>
            </div>
            <div class="version-actions">
              <button
                v-if="
                  selectedProduct.status !== 'retired' &&
                  !item.publishedAt &&
                  !isTestRunning(item) &&
                  item.latestTest?.state !== 'succeeded'
                "
                class="secondary compact"
                :disabled="busy === 'test-' + item.id"
                @click="openTest(item)"
              >
                <RotateCcw
                  v-if="item.latestTest?.state === 'failed'"
                  :size="16"
                /><Rocket v-else :size="16" />{{
                  item.latestTest?.state === "failed" ? "重新测试" : "测试部署"
                }}
              </button>
              <button
                v-else-if="
                  selectedProduct.status !== 'retired' &&
                  !item.publishedAt &&
                  item.latestTest?.state === 'succeeded'
                "
                class="primary compact"
                :disabled="busy === item.id"
                @click="publish(selectedProduct.id, item)"
              >
                <Rocket :size="16" />发布
              </button>
              <CheckCircle2
                v-else-if="item.publishedAt"
                class="ok"
                :size="20"
              />
              <Archive
                v-else-if="selectedProduct.status === 'retired'"
                class="quiet"
                :size="20"
                aria-label="产品已下架"
              />
            </div>
            <p
              v-if="
                item.latestTest?.state === 'failed' && item.latestTest.lastError
              "
              class="version-error"
            >
              <CircleAlert :size="15" />{{ item.latestTest.lastError }}
            </p>
          </article>
          <p v-if="!selectedProduct.versions.length" class="quiet empty-copy">
            还没有版本
          </p>
        </section>
      </div>
    </div>
  </main>
  <div v-if="testVersion" class="modal-backdrop" @click.self="closeTest">
    <section
      class="secret-dialog product-test-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="product-test-title"
    >
      <header>
        <div>
          <p class="eyebrow">发布前验证</p>
          <h2 id="product-test-title">测试部署 v{{ testVersion.version }}</h2>
        </div>
        <button
          class="icon-action"
          type="button"
          title="关闭"
          @click="closeTest"
        >
          <X :size="18" />
        </button>
      </header>
      <p class="quiet test-dialog-copy">
        平台会使用固定镜像版本、临时容器和临时数据卷执行健康检查。测试通过后才可以发布给用户。
      </p>
      <p
        v-if="
          testVersion.latestTest?.state === 'failed' &&
          testVersion.latestTest.lastError
        "
        class="test-dialog-error"
      >
        <CircleAlert :size="16" />上次测试失败：{{
          testVersion.latestTest.lastError
        }}
      </p>
      <form @submit.prevent="startTest">
        <div
          v-if="(testVersion.runtimeSpec.secretKeys || []).length"
          class="test-secret-list"
        >
          <div class="deploy-secret-heading">
            <KeyRound :size="18" />
            <div>
              <strong>测试 Secret</strong
              ><small>仅用于本次测试，测试结束后立即清除</small>
            </div>
          </div>
          <label
            v-for="key in testVersion.runtimeSpec.secretKeys || []"
            :key="key"
            >{{ key
            }}<input
              v-model="testSecrets[key]"
              type="password"
              autocomplete="off"
              required
          /></label>
        </div>
        <p v-else class="test-no-secrets">
          此版本不需要 Secret，可直接开始测试。
        </p>
        <div class="deploy-dialog-actions">
          <button type="button" class="secondary compact" @click="closeTest">
            取消</button
          ><button
            class="primary compact"
            :disabled="busy === 'test-' + testVersion.id"
          >
            <LoaderCircle
              v-if="busy === 'test-' + testVersion.id"
              class="spin"
              :size="16"
            /><Rocket v-else :size="16" />开始测试
          </button>
        </div>
      </form>
    </section>
  </div>
  <div
    v-if="editingProduct"
    class="modal-backdrop"
    @click.self="closeEditProduct"
  >
    <section
      class="secret-dialog product-edit-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="product-edit-title"
    >
      <header>
        <div>
          <p class="eyebrow">产品资料</p>
          <h2 id="product-edit-title">编辑产品名称</h2>
        </div>
        <button
          class="icon-action"
          type="button"
          title="关闭"
          @click="closeEditProduct"
        >
          <X :size="18" />
        </button>
      </header>
      <form @submit.prevent="saveProductName">
        <label
          >产品名称<input v-model="editName" required maxlength="120"
        /></label>
        <label
          >产品标识<input :value="editingProduct.slug" disabled /><small
            >标识用于应用路径和历史引用，创建后保持不变</small
          ></label
        >
        <div class="deploy-dialog-actions">
          <button
            type="button"
            class="secondary compact"
            @click="closeEditProduct"
          >
            取消</button
          ><button class="primary compact" :disabled="busy === 'edit-product'">
            <Save :size="16" />保存
          </button>
        </div>
      </form>
    </section>
  </div>
  <div
    v-if="lifecycleProduct"
    class="modal-backdrop"
    @click.self="closeProductAvailability"
  >
    <section
      class="secret-dialog product-lifecycle-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="product-lifecycle-title"
    >
      <header>
        <div>
          <p class="eyebrow">目录状态</p>
          <h2 id="product-lifecycle-title">
            {{
              lifecycleProduct.status === "retired" ? "恢复产品" : "下架产品"
            }}
          </h2>
        </div>
        <button
          class="icon-action"
          type="button"
          title="关闭"
          @click="closeProductAvailability"
        >
          <X :size="18" />
        </button>
      </header>
      <div class="product-lifecycle-summary">
        <RotateCcw
          v-if="lifecycleProduct.status === 'retired'"
          :size="22"
        /><Archive v-else :size="22" />
        <div>
          <strong>{{ lifecycleProduct.name }}</strong
          ><small>{{ lifecycleProduct.slug }}</small>
        </div>
      </div>
      <p class="quiet lifecycle-copy">
        {{
          lifecycleProduct.status === "retired"
            ? "恢复后，已发布版本会重新出现在用户目录中；恢复前平台会再次核对产品依赖。"
            : "下架后不再接受新部署，但不会停止或删除用户现有应用，也不会删除任何历史版本。"
        }}
      </p>
      <div class="deploy-dialog-actions">
        <button
          type="button"
          class="secondary compact"
          @click="closeProductAvailability"
        >
          取消</button
        ><button
          :class="[
            'compact',
            lifecycleProduct.status === 'retired'
              ? 'primary'
              : 'secondary danger-button',
          ]"
          :disabled="busy === 'product-availability'"
          @click="updateProductAvailability"
        >
          <RotateCcw
            v-if="lifecycleProduct.status === 'retired'"
            :size="16"
          /><Archive v-else :size="16" />{{
            lifecycleProduct.status === "retired" ? "确认恢复" : "确认下架"
          }}
        </button>
      </div>
    </section>
  </div>
</template>
