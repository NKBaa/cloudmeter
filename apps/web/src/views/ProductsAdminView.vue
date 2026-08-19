<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import {
  AppWindow,
  Archive,
  ArrowLeft,
  Box,
  CheckCircle2,
  CircleAlert,
  ChevronDown,
  ChevronRight,
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
  command?: string[];
  env?: Record<string, string>;
  editableEnvKeys?: string[];
  envDescriptions?: Record<string, string>;
  secretKeys?: string[];
  secretDescriptions?: Record<string, string>;
  editableSecretKeys?: string[];
  volumes?: Volume[];
  dependencies?: Dependency[];
  dataVolumeGiB?: number;
  editableOptions?: EditableOptions;
};
type EditableOptions = { cpu: boolean; memory: boolean; dataVolume: boolean; command: boolean; dependencies: boolean };
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
  acceptedStatusCodes?: number[];
};
type UpdateSpec = { dataPolicy?: string };
type Version = {
  id: string;
  version: number;
  versionLabel: string;
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
  iconUrl: string;
  status: string;
  versions: Version[];
};
type KeyValue = { key: string; value: string; editable: boolean; description: string };
type SecretForm = { key: string; description: string; editable: boolean };
type VersionForm = {
  versionLabel: string;
  imageDigest: string;
  cpuCores: number;
  memoryMiB: number;
  command: { value: string }[];
  environment: KeyValue[];
  secrets: SecretForm[];
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
  acceptedStatusCodes: string;
  dataPolicy: "stateless" | "volume_compatible" | "backup_required";
  dataVolumeGiB: number;
  editableOptions: EditableOptions;
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
const editIconURL = ref("");
const lifecycleProduct = ref<Product | null>(null);
const templateInput = ref<HTMLInputElement | null>(null);
const templateSummary = ref<string[]>([]);
const versionFormElement = ref<HTMLFormElement | null>(null);
const versionEditorElement = ref<HTMLElement | null>(null);
const fieldErrors = ref<Record<string, string>>({});
const switchingProduct = ref(false);
const versionDrafts = new Map<
  string,
  { form: VersionForm; summary: string[]; sourceVersion?: Version }
>();
const showTemplateExample = ref(false);
const showCreateProduct = ref(false);
const expandedVersions = ref(new Set<string>());
const editingVersion = ref<Version | null>(null);
const templateExample = JSON.stringify({ product: { name: "示例应用", slug: "example-app" }, version: { imageDigest: "ghcr.io/example/app:v1.0.0", runtimeSpec: { cpuCores: 1, memoryMiB: 512, dataVolumeGiB: 10, editableOptions: { cpu: true, memory: true, dataVolume: true, command: false, dependencies: false }, command: ["server", "--production"], env: { APP_MODE: "production", LOG_LEVEL: "info" }, envDescriptions: { APP_MODE: "应用运行模式", LOG_LEVEL: "日志级别，可选 debug/info/warn/error" }, editableEnvKeys: ["LOG_LEVEL"], secretKeys: ["API_KEY"], secretDescriptions: { API_KEY: "第三方服务 API 密钥" }, editableSecretKeys: ["API_KEY"], volumes: [{ name: "data", mountPath: "/data", sizeGiB: 10 }], dependencies: [] }, routeSpec: { containerPort: 3000, basePath: "/", stripPrefix: true, websocket: true, sse: true, cookiePath: "/" }, healthSpec: { path: "/health", intervalSeconds: 10, timeoutSeconds: 5, acceptedStatusCodes: [] }, updateSpec: { dataPolicy: "volume_compatible" } } }, null, 2);
const templateExampleWithIcon = templateExample.replace('"slug": "example-app"', '"slug": "example-app",\n    "iconUrl": "https://example.com/app-icon.png"');
async function copyTemplateExample() { await navigator.clipboard.writeText(templateExampleWithIcon); done("示例模板已复制"); }
function downloadTemplateExample() { const link=document.createElement("a"); link.href=URL.createObjectURL(new Blob([templateExampleWithIcon],{type:"application/json"})); link.download="cloudmeter-product-template.example.json"; link.click(); URL.revokeObjectURL(link.href); }
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
    versionLabel: "",
    imageDigest: "",
    cpuCores: 1,
    memoryMiB: 512,
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
    acceptedStatusCodes: "",
    dataPolicy: "volume_compatible",
    dataVolumeGiB: 10,
    editableOptions: { cpu: true, memory: true, dataVolume: true, command: false, dependencies: false },
  };
}
function resetVersionForm() {
  Object.assign(versionForm, defaultVersionForm());
  templateSummary.value = [];
  fieldErrors.value = {};
  editingVersion.value = null;
  if (selected.value) versionDrafts.delete(selected.value);
}
function cloneVersionForm(source: VersionForm): VersionForm {
  return JSON.parse(JSON.stringify(source)) as VersionForm;
}
function saveCurrentDraft() {
  if (!selected.value) return;
  versionDrafts.set(selected.value, {
    form: cloneVersionForm(versionForm),
    summary: [...templateSummary.value],
    sourceVersion: editingVersion.value || undefined,
  });
}
function loadDraft(productID: string) {
  const draft = versionDrafts.get(productID);
  Object.assign(versionForm, draft ? cloneVersionForm(draft.form) : defaultVersionForm());
  templateSummary.value = draft ? [...draft.summary] : [];
  editingVersion.value = draft?.sourceVersion || null;
  fieldErrors.value = {};
}
async function selectProduct(productID: string) {
  if (productID === selected.value || switchingProduct.value) return;
  saveCurrentDraft();
  switchingProduct.value = true;
  selected.value = productID;
  loadDraft(productID);
  await nextTick();
  await new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
  });
  window.setTimeout(() => { switchingProduct.value = false; }, 180);
}
async function revealVersionEditor(focus = true) {
  // Close the create/import surface before waiting for the new editor. Keeping
  // the backdrop mounted while the list refreshes made the page look frozen.
  showCreateProduct.value = false;
  switchingProduct.value = true;
  await nextTick();
  await new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
  });
  const editor = versionEditorElement.value;
  if (editor) editor.scrollIntoView({ behavior: "smooth", block: "start" });
  window.setTimeout(() => {
    switchingProduct.value = false;
    if (focus)
      editor
        ?.querySelector<HTMLInputElement>('[data-field="versionLabel"]')
        ?.focus({ preventScroll: true });
  }, 300);
}
function fieldError(key: string) { return fieldErrors.value[key] || ""; }
function clearFieldError(key: string) {
  if (!fieldErrors.value[key]) return;
  const next = { ...fieldErrors.value };
  delete next[key];
  fieldErrors.value = next;
}
async function focusInvalidField(key: string, detail: string) {
  fieldErrors.value = { ...fieldErrors.value, [key]: detail };
  error.value = detail;
  message.value = "";
  await nextTick();
  const target = document.querySelector<HTMLElement>(`[data-field="${key}"]`);
  target?.scrollIntoView({ behavior: "smooth", block: "center" });
  window.setTimeout(() => target?.focus({ preventScroll: true }), 260);
}
function handleInvalid(event: Event) {
  event.preventDefault();
  const target = event.target as HTMLInputElement | HTMLSelectElement;
  const key = target.dataset.field || target.name;
  if (!key || fieldErrors.value[key]) return;
  void focusInvalidField(key, target.validationMessage || "请检查此项");
}
function numberValue(value: unknown, fallback: number) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}
function importedEditableOption(
  runtime: RuntimeSpec,
  key: keyof EditableOptions,
  legacyDefault: boolean,
) {
  const options = runtime.editableOptions;
  return options ? options[key] === true : legacyDefault;
}
function importedEnvironment(
  value: unknown,
  editable: Set<string>,
  descriptions: Record<string, unknown>,
): KeyValue[] {
  const entries: [string, unknown][] = Array.isArray(value)
    ? value.flatMap((item): [string, unknown][] => {
        if (typeof item === "string") {
          const split = item.indexOf("=");
          return split < 0
            ? [[item, ""]]
            : [[item.slice(0, split), item.slice(split + 1)]];
        }
        if (item && typeof item === "object" && "key" in item)
          return [[String(item.key), "value" in item ? item.value : ""]];
        return [];
      })
    : value && typeof value === "object"
      ? Object.entries(value as Record<string, unknown>)
      : [];
  return entries.map(([rawKey, rawValue]) => {
    const key = rawKey.trim();
    return {
      key,
      value: rawValue == null ? "" : String(rawValue),
      editable: editable.has(key),
      description: String(descriptions[key] || ""),
    };
  });
}
function importedSecretKeys(
  value: unknown,
  descriptions: Record<string, unknown> = {},
  editableKeys?: Set<string>,
): SecretForm[] {
  const entries: { key: string; description?: unknown; editable?: unknown }[] =
    Array.isArray(value)
      ? value.map((item) => {
          if (item && typeof item === "object" && "key" in item) {
            const record = item as Record<string, unknown>;
            return { key: String(record.key), description: record.description, editable: record.editable };
          }
          return { key: String(item) };
        })
      : value && typeof value === "object"
        ? Object.entries(value as Record<string, unknown>).map(([key, description]) => ({ key, description }))
        : [];
  return entries.map((entry) => ({
    key: entry.key,
    description: String(entry.description ?? descriptions[entry.key] ?? ""),
    // Legacy templates did not have editableSecretKeys; keep those keys editable.
    editable: entry.editable === undefined ? (editableKeys ? editableKeys.has(entry.key) : true) : entry.editable === true,
  }));
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
    if (!data || Array.isArray(data) || typeof data !== "object")
      throw new Error("模板根节点必须是 JSON 对象");
    const productData = data.product || data.application || {};
    const productSlug = String(
      productData.slug || data.productSlug || data.slug || "",
    )
      .trim()
      .toLowerCase();
    const productName = String(
      productData.name || data.productName || data.name || "",
    ).trim();
    if (!productName) throw new Error("模板缺少 product.name（产品名称）");
    if (!/^[a-z0-9][a-z0-9-]{0,62}$/.test(productSlug))
      throw new Error("模板中的 product.slug 只能包含小写字母、数字和连字符");
    const existing = products.value.find(
      (item) => item.id === data.productId || item.slug === productSlug,
    );

    const source = data.version || data;
    const runtime =
      source.runtimeSpec || source.runtime || source.resources || {};
    const route = source.routeSpec || source.route || {};
    const health = source.healthSpec || source.health || {};
    const update = source.updateSpec || source.update || {};
    const env = runtime.env ?? runtime.environment ?? {};
    const editable = new Set<string>(
      Array.isArray(runtime.editableEnvKeys)
        ? runtime.editableEnvKeys.map((value: unknown) => String(value))
        : [],
    );
    const secretSource = runtime.secretKeys || runtime.secrets || [];
    const importedEditableSecrets: Set<string> | undefined = Array.isArray(runtime.editableSecretKeys)
      ? new Set<string>(runtime.editableSecretKeys.map((value: unknown) => String(value)))
      : undefined;
    const volumeSource = runtime.volumes || [];
    const dependencySource = runtime.dependencies || [];

    const importedForm = defaultVersionForm();
    Object.assign(importedForm, {
      imageDigest: String(source.imageDigest || source.image || ""),
      versionLabel: String(source.versionLabel || source.label || ""),
      cpuCores: numberValue(runtime.cpuCores, 1),
      memoryMiB: numberValue(runtime.memoryMiB, 512),
      command: Array.isArray(runtime.command)
        ? runtime.command.map((value: unknown) => ({ value: String(value) }))
        : [],
      environment: importedEnvironment(
        env,
        editable,
        runtime.envDescriptions || {},
      ),
      secrets: importedSecretKeys(secretSource, runtime.secretDescriptions || {}, importedEditableSecrets),
      volumes: Array.isArray(volumeSource)
        ? volumeSource.map((volume: any) => ({
            name: String(volume.name || "data"),
            mountPath: String(volume.mountPath || "/data"),
            sizeGiB: numberValue(volume.sizeGiB, 10),
          }))
        : [],
      dataVolumeGiB: numberValue(
        runtime.dataVolumeGiB,
        Math.max(10, ...((Array.isArray(volumeSource) ? volumeSource : []).map((volume: any) => numberValue(volume.sizeGiB, 10)))),
      ),
      editableOptions: {
        cpu: importedEditableOption(runtime, "cpu", true),
        memory: importedEditableOption(runtime, "memory", true),
        dataVolume: importedEditableOption(runtime, "dataVolume", true),
        command: importedEditableOption(runtime, "command", false),
        dependencies: importedEditableOption(runtime, "dependencies", false),
      },
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
      acceptedStatusCodes: Array.isArray(health.acceptedStatusCodes)
        ? health.acceptedStatusCodes.join(", " )
        : "",
      dataPolicy: [
        "stateless",
        "volume_compatible",
        "backup_required",
      ].includes(update.dataPolicy)
        ? update.dataPolicy
        : "volume_compatible",
    });
    importedForm.dependencies = Array.isArray(dependencySource)
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
    if (importedForm.dataPolicy === "stateless") importedForm.volumes = [];

    const importedSummary = [
      source.imageDigest || source.image ? "镜像" : "镜像待填写",
      `资源 ${importedForm.cpuCores} 核 / ${importedForm.memoryMiB} MiB`,
      `环境变量 ${importedForm.environment.length} 项`,
      `Secret ${importedForm.secrets.length} 项`,
      `数据卷 ${importedForm.volumes.length} 项`,
      importedForm.volumes.length ? `共享容量最低 ${importedForm.dataVolumeGiB} GiB` : "无共享卷容量",
      `依赖 ${importedForm.dependencies.length} 项`,
      `容器内网端口 ${importedForm.containerPort}`,
    ];
    saveCurrentDraft();
    let targetID = existing?.id || "";
    if (!targetID) {
      busy.value = "template-import";
      const created = await api<{ id: string }>("/admin/products", {
        method: "POST",
        body: JSON.stringify({
          name: productName,
          slug: productSlug,
          iconUrl: String(productData.iconUrl || productData.icon || "").trim(),
        }),
      });
      targetID = created.id;
    }
    versionDrafts.set(targetID, {
      form: cloneVersionForm(importedForm),
      summary: [...importedSummary],
    });
    selected.value = targetID;
    loadDraft(targetID);
    await load(true);
    if (!products.value.some((item) => item.id === targetID))
      throw new Error("产品已创建，但刷新目录后没有找到它，请刷新页面后重试");
    done("模板已导入，已进入对应产品的版本编辑界面。");
    await revealVersionEditor();
  } catch (value) {
    failed(value);
  } finally {
    if (busy.value === "template-import") busy.value = "";
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
    if (!selected.value && products.value.length && !templateSummary.value.length)
      await selectProduct(products.value[0].id);
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
    fieldErrors.value = {};
    busy.value = "product";
    saveCurrentDraft();
    const created = await api<{ id: string }>("/admin/products", {
      method: "POST",
      body: JSON.stringify(product),
    });
    Object.assign(product, { name: "", slug: "" });
    versionDrafts.set(created.id, {
      form: defaultVersionForm(),
      summary: [],
    });
    selected.value = created.id;
    loadDraft(created.id);
    await load(true);
    if (!products.value.some((item) => item.id === created.id))
      throw new Error("产品已创建，但刷新目录后没有找到它，请刷新页面后重试");
    done("产品已创建，正在填写第一个版本。");
    await revealVersionEditor();
  } catch (value) {
    const text = (value as Error).message || "产品信息有误";
    if (text.toLowerCase().includes("slug") || text.includes("标识"))
      await focusInvalidField("productSlug", text);
    else if (text.toLowerCase().includes("name") || text.includes("名称"))
      await focusInvalidField("productName", text);
    else failed(value);
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
  editIconURL.value = item.iconUrl || "";
}
function closeEditProduct() {
  editingProduct.value = null;
  editName.value = "";
  editIconURL.value = "";
}
async function saveProductName() {
  if (!editingProduct.value) return;
  try {
    busy.value = "edit-product";
    await api(`/admin/products/${editingProduct.value.id}`, {
      method: "PATCH",
      body: JSON.stringify({ name: editName.value, iconUrl: editIconURL.value }),
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
  versionForm.volumes.push({ name: "data", mountPath: "/data", sizeGiB: versionForm.dataVolumeGiB });
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
function environmentDescriptions() { return Object.fromEntries(versionForm.environment.filter((entry) => entry.key.trim() && entry.description.trim()).map((entry) => [entry.key.trim(), entry.description.trim()])); }
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
function secretDescriptions() {
  return Object.fromEntries(
    versionForm.secrets
      .filter((entry) => entry.key.trim() && entry.description.trim())
      .map((entry) => [entry.key.trim().toUpperCase(), entry.description.trim()]),
  );
}
function editableSecretKeys() {
  return versionForm.secrets
    .filter((entry) => entry.editable && entry.key.trim())
    .map((entry) => entry.key.trim().toUpperCase());
}
function healthAcceptedStatusCodes() {
  const raw = versionForm.acceptedStatusCodes.trim();
  if (!raw) return [];
  const values = raw
    .split(/[\s,，]+/)
    .filter(Boolean)
    .map(Number);
  if (
    values.some(
      (value) => !Number.isInteger(value) || value < 100 || value > 599,
    )
  )
    throw new Error(
      "额外成功状态码必须是 100 到 599 的整数，多个状态码用逗号分隔",
    );
  const unique = [...new Set(values)];
  if (unique.length > 32)
    throw new Error("额外成功状态码最多填写 32 个");
  return unique;
}
async function createVersion() {
  if (!selected.value) {
    error.value = "请先选择产品";
    return;
  }
  try {
    fieldErrors.value = {};
    if (versionFormElement.value && !versionFormElement.value.checkValidity()) {
      versionFormElement.value.reportValidity();
      return;
    }
    busy.value = "version";
    const runtimeSpec: RuntimeSpec = {
      cpuCores: versionForm.cpuCores,
      memoryMiB: versionForm.memoryMiB,
      volumes: versionForm.volumes.map((volume) => ({
        name: volume.name.trim().toLowerCase(),
        mountPath: volume.mountPath.trim(),
        sizeGiB: versionForm.dataVolumeGiB,
      })),
      editableOptions: versionForm.editableOptions,
      env: uniqueEntries(versionForm.environment),
      editableEnvKeys: editableEnvironmentKeys(),
      envDescriptions: environmentDescriptions(),
      secretKeys: secretKeys(),
      secretDescriptions: secretDescriptions(),
      editableSecretKeys: editableSecretKeys(),
      dependencies: dependencies(),
    };
    if (versionForm.volumes.length) runtimeSpec.dataVolumeGiB = versionForm.dataVolumeGiB;
    const command = versionForm.command
      .map((argument) => argument.value.trim())
      .filter(Boolean);
    if (command.length) runtimeSpec.command = command;
    await api("/admin/products/" + selected.value + "/versions", {
      method: "POST",
      body: JSON.stringify({
        versionLabel: versionForm.versionLabel.trim(),
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
          acceptedStatusCodes: healthAcceptedStatusCodes(),
        },
        updateSpec: { dataPolicy: versionForm.dataPolicy },
      }),
    });
    const basedOnPublishedVersion = Boolean(editingVersion.value);
    resetVersionForm();
    done(basedOnPublishedVersion ? "已基于已发布版本创建新版本" : "新版本已创建");
    await load();
  } catch (value) {
    const text = (value as Error).message || "版本配置有误";
    if (text.includes("image must") || text.includes("镜像")) {
      await focusInvalidField("imageDigest", "请填写带版本号的镜像地址，例如 nginx:1.27");
    } else if (text.includes("环境变量") && text.includes("重复")) {
      const duplicate = text.match(/环境变量\s+(\S+)\s+重复/)?.[1];
      const index = versionForm.environment.findIndex((entry, current) =>
        entry.key.trim() === duplicate && versionForm.environment.findIndex((other) => other.key.trim() === duplicate) !== current);
      await focusInvalidField(`environment-${Math.max(index, 0)}-key`, text);
    } else if (text.includes("Secret") && text.includes("重复")) {
      await focusInvalidField("secret-0", text);
    } else if (text.includes("依赖标识")) {
      await focusInvalidField("dependency-0-key", text);
    } else if (text.includes("依赖服务名")) {
      await focusInvalidField("dependency-0-service", text);
    } else if (text.includes("状态码")) {
      await focusInvalidField("acceptedStatusCodes", text);
    } else failed(value);
  } finally {
    busy.value = "";
  }
}
function versionFormFromItem(item: Version): VersionForm {
  const runtime = item.runtimeSpec || {};
  const volumes = (runtime.volumes || []).map((volume) => ({ ...volume }));
  return {
    ...defaultVersionForm(),
    versionLabel: item.versionLabel || "",
    imageDigest: item.imageDigest,
    cpuCores: numberValue(runtime.cpuCores, 1),
    memoryMiB: numberValue(runtime.memoryMiB, 512),
    command: (runtime.command || []).map((value) => ({ value })),
    environment: importedEnvironment(
      runtime.env || {},
      new Set(runtime.editableEnvKeys || []),
      runtime.envDescriptions || {},
    ),
    secrets: importedSecretKeys(
      runtime.secretKeys || [],
      runtime.secretDescriptions || {},
      Array.isArray(runtime.editableSecretKeys)
        ? new Set(runtime.editableSecretKeys)
        : undefined,
    ),
    volumes,
    dependencies: (runtime.dependencies || []).map((dependency) => ({ ...dependency })),
    dataVolumeGiB: numberValue(
      runtime.dataVolumeGiB,
      Math.max(1, ...volumes.map((volume) => numberValue(volume.sizeGiB, 1))),
    ),
    editableOptions: {
      cpu: importedEditableOption(runtime, "cpu", true),
      memory: importedEditableOption(runtime, "memory", true),
      dataVolume: importedEditableOption(runtime, "dataVolume", true),
      command: importedEditableOption(runtime, "command", false),
      dependencies: importedEditableOption(runtime, "dependencies", false),
    },
    containerPort: numberValue(item.routeSpec?.containerPort, 8080),
    basePath: item.routeSpec?.basePath ?? "/",
    stripPrefix: item.routeSpec?.stripPrefix !== false,
    websocket: item.routeSpec?.websocket !== false,
    sse: item.routeSpec?.sse !== false,
    cookiePath: item.routeSpec?.cookiePath ?? "",
    healthPath: item.healthSpec?.path ?? "",
    intervalSeconds: numberValue(item.healthSpec?.intervalSeconds, 5),
    timeoutSeconds: numberValue(item.healthSpec?.timeoutSeconds, 5),
    acceptedStatusCodes: (item.healthSpec?.acceptedStatusCodes || []).join(", "),
    dataPolicy: ["stateless", "volume_compatible", "backup_required"].includes(
      item.updateSpec?.dataPolicy || "",
    )
      ? (item.updateSpec?.dataPolicy as VersionForm["dataPolicy"])
      : "volume_compatible",
  };
}
async function loadVersionIntoEditor(item: Version) {
  if (!selected.value) return;
  const form = versionFormFromItem(item);
  const summary = [
    `已载入版本 ${item.versionLabel || `v${item.version}`}`,
    `镜像 ${item.imageDigest}`,
    `资源 ${form.cpuCores} 核 / ${form.memoryMiB} MiB`,
    `环境变量 ${form.environment.length} 项`,
    `数据卷 ${form.volumes.length} 项`,
  ];
  editingVersion.value = item;
  versionDrafts.set(selected.value, {
    form: cloneVersionForm(form),
    summary: [...summary],
    sourceVersion: item,
  });
  loadDraft(selected.value);
  await revealVersionEditor(false);
  done("已载入已发布版本；修改后创建的是新版本，原版本保持不变");
}
async function toggleVersion(item: Version) {
  const id = item.id;
  const next = new Set(expandedVersions.value);
  const wasExpanded = next.has(id);
  wasExpanded ? next.delete(id) : next.add(id);
  expandedVersions.value = next;
  if (
    !wasExpanded &&
    editingVersion.value?.id !== item.id &&
    item.publishedAt &&
    selectedProduct.value?.status !== "retired"
  )
    await loadVersionIntoEditor(item);
}
async function deleteSelectedProduct() {
  const item = selectedProduct.value;
  if (!item || !window.confirm(`永久移除模板“${item.name}”？发布和审计历史会保留；已有用户应用时系统会拒绝删除。`)) return;
  try {
    busy.value = "delete-product";
    await api(`/admin/products/${item.id}`, { method: "DELETE" });
    selected.value = "";
    done("应用模板已删除");
    await load();
  } catch (value) { failed(value); } finally { busy.value = ""; }
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
        <button class="secondary compact" type="button" @click="showTemplateExample = true">查看示例</button>
        <button class="primary compact" type="button" @click="showCreateProduct = true"><Plus :size="16"/>创建产品</button>
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
          @click="selectProduct(item.id)"
        >
          <span class="catalog-icon"><Box :size="18" /><img v-if="item.iconUrl" :src="item.iconUrl" alt="" @error="($event.currentTarget as HTMLImageElement).style.display='none'" /></span><span
            ><strong>{{ item.name }}</strong
            ><small
              >{{ item.slug }} · {{ productStatusLabel(item.status) }}</small
            ></span
          >
        </button>
        <p v-if="!products.length" class="quiet empty-copy">还没有产品</p>
      </section>
      <div :class="['product-admin-main', switchingProduct && 'is-switching']">
        <section
          v-if="selected"
          ref="versionEditorElement"
          :key="selected"
          :class="['form-panel', 'version-builder', switchingProduct && 'is-switching']"
        >
          <div class="builder-heading">
            <div>
              <p class="eyebrow">{{ selectedProduct?.name }}</p>
              <h2><Rocket :size="19" />{{ editingVersion ? "编辑并创建新版本" : "添加版本" }}</h2>
              <div class="product-flow-steps" aria-label="产品发布流程"><span class="done">1 产品信息</span><ChevronRight :size="13"/><span class="current">2 配置版本</span><ChevronRight :size="13"/><span>3 测试部署</span><ChevronRight :size="13"/><span>4 发布上架</span></div>
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
              <button
                class="icon-action stop-action"
                type="button"
                title="删除应用模板"
                :disabled="busy === 'delete-product'"
                @click="deleteSelectedProduct"
              >
                <Trash2 :size="16" />
              </button>
            </div>
          </div>
          <div
            v-if="templateSummary.length"
            class="import-summary admin-import-summary"
          >
            <strong>{{ editingVersion ? "已发布版本已回填" : "模板配置已回填" }}</strong>
            <small>{{ templateSummary.join(" · ") }}</small>
            <small>{{ editingVersion ? "保存时会创建新的不可变版本，原版本与现有用户部署不受影响。" : "产品已就绪；请检查配置后创建版本，测试通过后再发布。" }}</small>
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
          <form ref="versionFormElement" v-else @invalid.capture="handleInvalid" @submit.prevent="createVersion">
            <section class="config-section first">
              <div class="config-heading">
                <Settings2 :size="18" />
                <div>
                  <strong>镜像与资源</strong
                  ><small>填写镜像版本号即可，也兼容 SHA-256 Digest</small>
                </div>
              </div>
              <label
                >版本号或版本名称<input
                  v-model="versionForm.versionLabel"
                  data-field="versionLabel"
                  maxlength="64"
                  placeholder="例如 v1.2.0、2026.08 稳定版"
                /><small>用于管理员识别和用户查看；留空时使用自动序号</small></label
              >
              <label
                >镜像地址与版本<input
                  v-model="versionForm.imageDigest"
                  data-field="imageDigest"
                  :class="{ 'field-invalid': fieldError('imageDigest') }"
                  @input="clearFieldError('imageDigest')"
                  required
                  placeholder="例如 nginx:1.27 或 ghcr.io/org/app:v2.3.1"
              /><small v-if="fieldError('imageDigest')" class="field-error">{{ fieldError('imageDigest') }}</small></label>
              <div class="config-grid three">
                <label
                  >最低 CPU 核心<input
                    v-model.number="versionForm.cpuCores"
                    data-field="cpuCores"
                    :class="{ 'field-invalid': fieldError('cpuCores') }"
                    @input="clearFieldError('cpuCores')"
                    type="number"
                    min="0.1"
                    max="64"
                    step="0.1"
                    required
                /><span class="field-editable-toggle"><input v-model="versionForm.editableOptions.cpu" type="checkbox" />允许用户提高配置</span></label>
                <label
                  >最低内存 MiB<input
                    v-model.number="versionForm.memoryMiB"
                    data-field="memoryMiB"
                    :class="{ 'field-invalid': fieldError('memoryMiB') }"
                    @input="clearFieldError('memoryMiB')"
                    type="number"
                    min="64"
                    max="262144"
                    step="64"
                    required
                /><span class="field-editable-toggle"><input v-model="versionForm.editableOptions.memory" type="checkbox" />允许用户提高配置</span></label>
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
              <label class="toggle option-permission"><input v-model="versionForm.editableOptions.command" type="checkbox" />允许用户修改启动命令</label>
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
                      description: '',
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
                  <input v-model="entry.key" :data-field="`environment-${index}-key`" :class="{ 'field-invalid': fieldError(`environment-${index}-key`) }" @input="clearFieldError(`environment-${index}-key`)" placeholder="APP_MODE" /><input
                    v-model="entry.value"
                    placeholder="production"
                  /><label class="toggle"
                    ><input
                      v-model="entry.editable"
                      type="checkbox"
                    />用户可改</label
                  ><input v-model="entry.description" class="env-description" placeholder="注释：帮助用户理解该变量"
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
                  @click="versionForm.secrets.push({ key: '', description: '', editable: true })"
                >
                  <Plus :size="15" />Secret
                </button>
              </div>
              <div class="repeat-list">
                <div
                  v-for="(entry, index) in versionForm.secrets"
                  :key="index"
                  class="repeat-row secret-config-row"
                >
                  <input
                    v-model="entry.key"
                    :data-field="`secret-${index}`"
                    :class="{ 'field-invalid': fieldError(`secret-${index}`) || (index === 0 && fieldError('secret-0')) }"
                    @input="clearFieldError(`secret-${index}`); clearFieldError('secret-0')"
                    placeholder="API_KEY"
                    @blur="entry.key = entry.key.trim().toUpperCase()"
                  /><input
                    v-model="entry.description"
                    class="env-description"
                    placeholder="注释：帮助用户理解该 Secret"
                  /><label class="toggle"
                    ><input v-model="entry.editable" type="checkbox" />用户可修改</label
                  ><span v-if="!entry.editable" class="locked-hint">仅管理员</span
                  ><button
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
              <label class="toggle option-permission"><input v-model="versionForm.editableOptions.dependencies" type="checkbox" />允许用户调整依赖绑定</label>
              <div class="repeat-list">
                <div
                  v-for="(dependency, index) in versionForm.dependencies"
                  :key="index"
                  class="repeat-row dependency-row"
                >
                  <label
                    >依赖标识<input
                      v-model="dependency.key"
                      :data-field="`dependency-${index}-key`"
                      :class="{ 'field-invalid': fieldError(`dependency-${index}-key`) || (index === 0 && fieldError('dependency-0-key')) }"
                      @input="clearFieldError(`dependency-${index}-key`); clearFieldError('dependency-0-key')"
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
                      :data-field="`dependency-${index}-service`"
                      :class="{ 'field-invalid': fieldError(`dependency-${index}-service`) || (index === 0 && fieldError('dependency-0-service')) }"
                      @input="clearFieldError(`dependency-${index}-service`); clearFieldError('dependency-0-service')"
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
              <div v-if="versionForm.volumes.length" class="shared-volume-setting">
                <label>最低共享卷容量 GiB<input v-model.number="versionForm.dataVolumeGiB" data-field="dataVolumeGiB" :class="{ 'field-invalid': fieldError('dataVolumeGiB') }" @input="clearFieldError('dataVolumeGiB')" type="number" min="1" max="16384" step="1" required /><small>所有持久化挂载共享这一个容量额度，只计费一次</small></label>
                <label class="toggle option-permission"><input v-model="versionForm.editableOptions.dataVolume" type="checkbox" />允许用户提高共享卷容量</label>
              </div>
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
                  没有持久化数据卷；此应用不会产生存储容量费用
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
                <label class="aligned-config-field"
                  ><span>容器内网监听端口</span><input
                    v-model.number="versionForm.containerPort"
                    data-field="containerPort"
                    :class="{ 'field-invalid': fieldError('containerPort') }"
                    @input="clearFieldError('containerPort')"
                    type="number"
                    min="1"
                    max="65535"
                    step="1"
                    required
                  /><small
                    >必须与镜像进程实际监听端口一致；它不是宿主机映射端口。平台会用此端口执行健康检查、内网转发和同用户容器互访</small
                  ></label
                ><label class="aligned-config-field"
                  ><span>内部 Base Path</span><input
                    v-model="versionForm.basePath"
                    data-field="basePath"
                    :class="{ 'field-invalid': fieldError('basePath') }"
                    @input="clearFieldError('basePath')"
                    required
                    placeholder="/" /><small>应用在容器内部响应请求的基础路径</small></label
                ><label class="aligned-config-field"
                  ><span>Cookie Path</span><input
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
              <div class="config-grid four health-grid">
                <label class="aligned-config-field"
                  ><span>检查路径</span><input
                    v-model="versionForm.healthPath"
                    placeholder="/health"
                  /><small>留空仅检查容器运行状态</small></label
                ><label class="aligned-config-field"
                  ><span>检查间隔（秒）</span><input
                    v-model.number="versionForm.intervalSeconds"
                    data-field="intervalSeconds"
                    :class="{ 'field-invalid': fieldError('intervalSeconds') }"
                    @input="clearFieldError('intervalSeconds')"
                    type="number"
                    min="1"
                    max="120"
                    step="1"
                    required /><small>两次健康检查之间的等待时间</small></label
                ><label class="aligned-config-field"
                  ><span>超时（秒）</span><input
                    v-model.number="versionForm.timeoutSeconds"
                    data-field="timeoutSeconds"
                    :class="{ 'field-invalid': fieldError('timeoutSeconds') }"
                    @input="clearFieldError('timeoutSeconds')"
                    type="number"
                    min="1"
                    max="30"
                    step="1"
                    required
                /><small>单次健康检查允许的最长响应时间</small></label
                ><label class="aligned-config-field"
                  ><span>额外成功状态码</span><input
                    v-model="versionForm.acceptedStatusCodes"
                    data-field="acceptedStatusCodes"
                    :class="{
                      'field-invalid': fieldError('acceptedStatusCodes'),
                    }"
                    @input="clearFieldError('acceptedStatusCodes')"
                    placeholder="例如 401"
                  /><small
                    >默认接受全部 2xx；认证型应用可填 401，多个用逗号分隔</small
                  ></label
                >
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
                <Save v-if="editingVersion" :size="16" /><Plus v-else :size="16" />{{ editingVersion ? "保存为新版本" : "创建" }}
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
              expandedVersions.has(item.id) && 'expanded',
              editingVersion?.id === item.id && 'editing-source',
            ]"
            @click="toggleVersion(item)"
          >
            <span class="version-number">{{ item.versionLabel || `v${item.version}` }}</span>
            <div class="version-copy">
              <strong>版本 {{ item.version }} · {{ item.imageDigest.split("@")[0] }}</strong>
              <small v-if="expandedVersions.has(item.id)"
                >最低 {{ item.runtimeSpec.cpuCores || 1 }} 核 ·
                {{ item.runtimeSpec.memoryMiB || 512 }} MiB · 容器内网端口
                {{ item.routeSpec.containerPort || 8080 }} ·
                {{ dataPolicyLabel(item.updateSpec?.dataPolicy) }}</small
              >
              <small v-if="expandedVersions.has(item.id)" class="digest-copy">{{ item.imageDigest }}</small>
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
            <div class="version-actions" @click.stop>
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
              <button
                v-else-if="item.publishedAt && selectedProduct.status !== 'retired'"
                class="secondary compact"
                type="button"
                @click="loadVersionIntoEditor(item)"
              ><Pencil :size="15" />载入编辑</button>
              <Archive
                v-else-if="selectedProduct.status === 'retired'"
                class="quiet"
                :size="20"
                aria-label="产品已下架"
              />
            </div>
            <ChevronDown :class="['version-chevron', expandedVersions.has(item.id) && 'open']" :size="18" />
            <p
              v-if="
                expandedVersions.has(item.id) &&
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
    <Transition name="modal-pop">
      <div v-if="showCreateProduct" class="modal-backdrop" @click.self="showCreateProduct=false">
        <section class="secret-dialog product-create-dialog" role="dialog" aria-modal="true" aria-labelledby="create-product-title">
          <header><div><p class="eyebrow">应用目录</p><h2 id="create-product-title">创建产品</h2></div><button class="icon-action" type="button" @click="showCreateProduct=false"><X :size="18"/></button></header>
          <form @invalid.capture="handleInvalid" @submit.prevent="createProduct"><p class="product-create-note">这里只建立产品目录。创建完成后会立即进入该产品的版本配置，不会停留在当前弹窗。</p><label>产品名称<input v-model="product.name" data-field="productName" :class="{ 'field-invalid': fieldError('productName') }" @input="clearFieldError('productName')" required maxlength="120" placeholder="SillyTavern"/><small v-if="fieldError('productName')" class="field-error">{{ fieldError('productName') }}</small></label><label>产品标识<input v-model="product.slug" data-field="productSlug" :class="{ 'field-invalid': fieldError('productSlug') }" @input="clearFieldError('productSlug')" @blur="product.slug = product.slug.trim().toLowerCase()" required maxlength="63" pattern="[a-z0-9][a-z0-9-]{0,62}" placeholder="sillytavern"/><small v-if="fieldError('productSlug')" class="field-error">{{ fieldError('productSlug') }}</small></label><div class="deploy-dialog-actions"><button type="button" class="secondary compact" @click="showCreateProduct=false">取消</button><button class="primary compact" :disabled="busy==='product'"><Save :size="16"/>创建并填写版本</button></div></form>
        </section>
      </div>
    </Transition>
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
        <label>产品图标 URL<input v-model.trim="editIconURL" type="url" maxlength="2048" placeholder="https://example.com/icon.png" /><small>建议使用正方形 PNG、WebP 或 SVG；留空使用默认应用图标</small></label>
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
  <div v-if="showTemplateExample" class="modal-backdrop" @click.self="showTemplateExample = false">
    <section class="modal template-example-modal">
      <header><div><p class="eyebrow">产品模板</p><h2>完整示例</h2></div><button class="icon-action" @click="showTemplateExample = false"><X :size="18" /></button></header>
      <pre>{{ templateExampleWithIcon }}</pre>
      <div class="builder-actions"><button class="secondary compact" @click="downloadTemplateExample">下载 JSON</button><button class="primary compact" @click="copyTemplateExample">复制示例</button></div>
    </section>
  </div>
</template>
