<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import {
  CheckCircle2,
  CircleAlert,
  Container,
  Copy,
  Database,
  KeyRound,
  Network,
  Power,
  RefreshCw,
  Save,
  Trash2,
  X,
} from "@lucide/vue";
import { api } from "../api";

type DockerSettings = {
  registryMirrors: string[];
  defaultRegistry: string;
  registryUsername: string;
  registryPassword: string;
  registryPasswordConfigured: boolean;
  httpProxy: string;
  httpsProxy: string;
  noProxy: string;
  pullTimeoutSeconds: number;
  detectedRegistryMirrors: string[];
  detectedHttpProxy: string;
  detectedHttpsProxy: string;
  detectedNoProxy: string;
  lastCheckedAt: string | null;
  lastCheckError: string;
  daemonRestartRequired: boolean;
  daemonJson: string;
};
type RestartRequest = {
  id: string;
  status: "queued" | "running" | "succeeded" | "failed";
  attempts: number;
  lastError: string;
  createdAt: string;
  startedAt: string | null;
  completedAt: string | null;
};
type DockerImage = {
  id: string;
  repoTags: string[];
  sizeBytes: number;
  createdAt: string | null;
  containerReferences: number;
  sampledAt: string;
};
type ImageDeletionJob = {
  id: string;
  imageId: string;
  status: string;
  lastError: string;
  createdAt: string;
  completedAt: string | null;
};

const model = reactive<DockerSettings>({
  registryMirrors: [],
  defaultRegistry: "",
  registryUsername: "",
  registryPassword: "",
  registryPasswordConfigured: false,
  httpProxy: "",
  httpsProxy: "",
  noProxy: "localhost,127.0.0.1,::1",
  pullTimeoutSeconds: 300,
  detectedRegistryMirrors: [],
  detectedHttpProxy: "",
  detectedHttpsProxy: "",
  detectedNoProxy: "",
  lastCheckedAt: null,
  lastCheckError: "",
  daemonRestartRequired: false,
  daemonJson: "{}",
});
const mirrorsText = ref("");
const busy = ref("");
const error = ref("");
const message = ref("");
const restartRequest = ref<RestartRequest | null>(null);
const restartDialogOpen = ref(false);
const restartBusy = ref(false);
const restartConnectionLost = ref(false);
const images = ref<DockerImage[]>([]),
  imageJobs = ref<ImageDeletionJob[]>([]),
  imageBusy = ref("");
let restartTimer: number | undefined;

const checkTime = computed(() =>
  model.lastCheckedAt
    ? new Date(model.lastCheckedAt).toLocaleString("zh-CN")
    : "等待 Worker 首次探测",
);
const detectedMirrorText = computed(() =>
  model.detectedRegistryMirrors.length
    ? model.detectedRegistryMirrors.join("、")
    : "未探测到镜像加速源",
);
const restartActive = computed(
  () =>
    restartRequest.value?.status === "queued" ||
    restartRequest.value?.status === "running",
);
const restartStatus = computed(() => {
  if (restartConnectionLost.value && restartActive.value)
    return {
      label: "连接恢复中",
      detail: "平台服务正在切换，页面会自动重试连接。",
      tone: "running",
    };
  if (!restartRequest.value) return null;
  const status = restartRequest.value.status;
  if (status === "queued")
    return {
      label: "等待执行",
      detail: "重启任务已进入队列，Worker 即将处理。",
      tone: "running",
    };
  if (status === "running")
    return {
      label: "正在重启",
      detail: "平台控制面会短暂中断，请勿重复提交。",
      tone: "running",
    };
  if (status === "succeeded")
    return {
      label: "重启完成",
      detail: "平台服务已恢复，用户应用未受重启范围影响。",
      tone: "success",
    };
  return {
    label: "重启失败",
    detail:
      restartRequest.value.lastError ||
      "平台服务未能完成重启，请检查 Worker 日志。",
    tone: "failed",
  };
});

function done(value: string) {
  message.value = value;
  error.value = "";
}
function failed(value: unknown) {
  error.value = (value as Error).message;
  message.value = "";
}
function clearRestartTimer() {
  if (restartTimer) window.clearTimeout(restartTimer);
  restartTimer = undefined;
}
function scheduleRestartPoll(delay = 2500) {
  clearRestartTimer();
  restartTimer = window.setTimeout(() => loadRestartStatus(true), delay);
}
async function load(silent = false) {
  try {
    busy.value = "load";
    const data = await api<DockerSettings>("/admin/settings/docker");
    Object.assign(model, data, { registryPassword: "" });
    mirrorsText.value = (data.registryMirrors || []).join("\n");
    if (!silent) {
      error.value = "";
      message.value = "";
    }
  } catch (value) {
    if (!silent) failed(value);
  } finally {
    busy.value = "";
  }
}
async function loadImages() {
  try {
    imageBusy.value = "load";
    const result = await api<{
      images: DockerImage[];
      deletionJobs: ImageDeletionJob[];
    }>("/admin/docker/images");
    images.value = result.images;
    imageJobs.value = result.deletionJobs;
  } catch (value) {
    failed(value);
  } finally {
    imageBusy.value = "";
  }
}
function imageSize(bytes: number) {
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KiB";
  if (bytes < 1024 * 1024 * 1024)
    return (bytes / 1024 / 1024).toFixed(1) + " MiB";
  return (bytes / 1024 / 1024 / 1024).toFixed(2) + " GiB";
}
async function deleteImage(image: DockerImage) {
  if (image.containerReferences > 0) {
    failed(new Error("镜像仍被容器引用，不能删除"));
    return;
  }
  const suffix = image.id.slice(-12),
    confirmation =
      prompt(
        "删除镜像后再次使用需要重新拉取。请输入镜像 ID 末 12 位确认：" + suffix,
        "",
      ) || "";
  if (!confirmation) return;
  try {
    imageBusy.value = image.id;
    await api("/admin/docker/images/" + encodeURIComponent(image.id), {
      method: "DELETE",
      body: JSON.stringify({ confirmation }),
    });
    done("镜像删除任务已提交，Worker 会再次检查容器引用");
    await loadImages();
  } catch (value) {
    failed(value);
  } finally {
    imageBusy.value = "";
  }
}
async function loadRestartStatus(polling = false) {
  try {
    const result = await api<{ request: RestartRequest | null }>(
      "/admin/system/restart",
    );
    const previousStatus = restartRequest.value?.status;
    restartRequest.value = result.request;
    restartConnectionLost.value = false;
    if (restartActive.value) {
      scheduleRestartPoll();
    } else {
      clearRestartTimer();
      if (
        previousStatus &&
        previousStatus !== result.request?.status &&
        result.request?.status === "succeeded"
      ) {
        done("CloudMeter 平台服务已完成重启");
        await load(true);
      }
    }
  } catch (value) {
    if (restartActive.value || restartBusy.value || polling) {
      restartConnectionLost.value = true;
      scheduleRestartPoll();
      return;
    }
    failed(value);
  }
}
async function save() {
  try {
    busy.value = "save";
    const registryMirrors = mirrorsText.value
      .split(/\r?\n|,/)
      .map((item) => item.trim())
      .filter(Boolean);
    await api("/admin/settings/docker", {
      method: "PUT",
      body: JSON.stringify({
        registryMirrors,
        defaultRegistry: model.defaultRegistry.trim(),
        registryUsername: model.registryUsername.trim(),
        registryPassword: model.registryPassword,
        httpProxy: model.httpProxy.trim(),
        httpsProxy: model.httpsProxy.trim(),
        noProxy: model.noProxy.trim(),
        pullTimeoutSeconds: model.pullTimeoutSeconds,
      }),
    });
    done(
      "Docker 配置已保存；Registry 凭据和拉取策略立即生效，Daemon 配置仍需在宿主机应用",
    );
    await load(true);
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function copyDaemonJSON() {
  try {
    await navigator.clipboard.writeText(model.daemonJson);
    done("daemon.json 示例已复制");
  } catch {
    failed(new Error("复制失败，请手动选择配置内容"));
  }
}
async function restartPlatform() {
  try {
    restartBusy.value = true;
    const result = await api<{ request: RestartRequest }>(
      "/admin/system/restart",
      { method: "POST", body: "{}" },
    );
    restartRequest.value = result.request;
    restartDialogOpen.value = false;
    done("系统重启任务已提交，页面将自动跟踪执行状态");
    scheduleRestartPoll(1200);
  } catch (value) {
    failed(value);
  } finally {
    restartBusy.value = false;
  }
}

onMounted(async () => {
  await Promise.all([load(), loadRestartStatus(), loadImages()]);
});
onBeforeUnmount(clearRestartTimer);
</script>

<template>
  <main class="workspace admin-workspace docker-settings-view">
    <header>
      <div>
        <p class="eyebrow">容器基础设施</p>
        <h1>Docker 设置</h1>
        <p class="quiet">
          统一管理镜像来源、Registry 凭据、代理和 Worker 拉取策略。
        </p>
      </div>
      <button
        class="danger-button compact platform-restart-button"
        :disabled="restartActive"
        @click="restartDialogOpen = true"
      >
        <Power :class="{ 'power-pulse': restartActive }" :size="16" />{{
          restartActive ? "重启进行中" : "系统重启"
        }}
      </button>
    </header>
    <p v-if="error" class="message sticky-message">{{ error }}</p>
    <p v-if="message" class="status-ok sticky-message">{{ message }}</p>

    <section
      v-if="restartStatus"
      :class="['platform-restart-status', restartStatus.tone]"
    >
      <span class="platform-restart-status-icon">
        <CheckCircle2 v-if="restartStatus.tone === 'success'" :size="18" />
        <CircleAlert v-else-if="restartStatus.tone === 'failed'" :size="18" />
        <Power v-else :size="18" />
      </span>
      <div>
        <strong>{{ restartStatus.label }}</strong>
        <p>{{ restartStatus.detail }}</p>
      </div>
      <time v-if="restartRequest">{{
        new Date(restartRequest.createdAt).toLocaleString("zh-CN")
      }}</time>
    </section>

    <div class="docker-settings-layout">
      <form class="form-panel docker-settings-form" @submit.prevent="save">
        <section class="docker-form-section">
          <div class="config-heading">
            <Network :size="18" />
            <div>
              <strong>镜像来源</strong
              ><small
                >一行一个 HTTPS 镜像加速地址；可维护私有 Registry
                默认前缀</small
              >
            </div>
          </div>
          <label
            >Registry Mirrors
            <textarea
              v-model="mirrorsText"
              rows="4"
              placeholder="https://mirror.example.com"
            />
            <small>仅接受无内嵌账号密码的 HTTPS 地址，最多 16 个。</small>
          </label>
          <label
            >默认镜像库 / 命名空间
            <input
              v-model="model.defaultRegistry"
              placeholder="registry.example.com/team"
            />
            <small
              >产品版本填写相对镜像名时自动补全；完整镜像地址保持不变。</small
            >
          </label>
          <div class="field-row">
            <label
              >Registry 用户名<input
                v-model="model.registryUsername"
                autocomplete="username"
            /></label>
            <label
              >Registry 密码<input
                v-model="model.registryPassword"
                type="password"
                autocomplete="new-password"
                :placeholder="
                  model.registryPasswordConfigured
                    ? '已加密保存，留空保持不变'
                    : '未配置'
                "
            /></label>
          </div>
        </section>

        <section class="docker-form-section">
          <div class="config-heading">
            <Container :size="18" />
            <div>
              <strong>代理与拉取策略</strong
              ><small>代理只供 Docker 拉取镜像，不注入用户应用容器</small>
            </div>
          </div>
          <div class="field-row">
            <label
              >HTTP Proxy<input
                v-model="model.httpProxy"
                type="url"
                placeholder="http://proxy.example.com:3128"
            /></label>
            <label
              >HTTPS Proxy<input
                v-model="model.httpsProxy"
                type="url"
                placeholder="http://proxy.example.com:3128"
            /></label>
          </div>
          <label
            >NO_PROXY<input
              v-model="model.noProxy"
              placeholder="localhost,127.0.0.1,::1,.internal"
          /></label>
          <label
            >Worker 镜像拉取超时（秒）
            <input
              v-model.number="model.pullTimeoutSeconds"
              type="number"
              min="30"
              max="1800"
              step="1"
              required
            />
            <small>允许 30–1800 秒；超时后部署任务会返回可读错误。</small>
          </label>
        </section>
        <div class="docker-form-actions">
          <p><KeyRound :size="15" />密码加密保存，接口与页面均不回显原文。</p>
          <button class="primary compact" :disabled="busy === 'save'">
            <Save :size="16" />{{ busy === "save" ? "保存中" : "保存设置" }}
          </button>
        </div>
      </form>

      <aside class="docker-runtime-aside">
        <section class="form-panel daemon-status-card">
          <div class="daemon-status-heading">
            <span
              :class="[
                'context-empty-icon',
                model.daemonRestartRequired && 'warning',
              ]"
              ><CircleAlert
                v-if="model.daemonRestartRequired"
                :size="21" /><CheckCircle2 v-else :size="21"
            /></span>
            <div>
              <p class="eyebrow">Worker 探测</p>
              <h2>
                {{
                  model.daemonRestartRequired
                    ? "配置尚未应用"
                    : "Daemon 配置一致"
                }}
              </h2>
            </div>
          </div>
          <p v-if="model.lastCheckError" class="configuration-status blocked">
            <CircleAlert :size="15" />{{ model.lastCheckError }}
          </p>
          <dl class="daemon-facts">
            <div>
              <dt>最近探测</dt>
              <dd>{{ checkTime }}</dd>
            </div>
            <div>
              <dt>实际镜像源</dt>
              <dd>{{ detectedMirrorText }}</dd>
            </div>
            <div>
              <dt>实际 HTTP 代理</dt>
              <dd>{{ model.detectedHttpProxy || "未配置" }}</dd>
            </div>
            <div>
              <dt>实际 HTTPS 代理</dt>
              <dd>{{ model.detectedHttpsProxy || "未配置" }}</dd>
            </div>
            <div>
              <dt>实际 NO_PROXY</dt>
              <dd>{{ model.detectedNoProxy || "未配置" }}</dd>
            </div>
          </dl>
        </section>

        <section class="form-panel daemon-json-card">
          <div class="config-heading with-action">
            <Database :size="18" />
            <div>
              <strong>daemon.json 示例</strong
              ><small>将期望配置应用到宿主机</small>
            </div>
            <button
              class="icon-action"
              type="button"
              title="复制"
              @click="copyDaemonJSON"
            >
              <Copy :size="16" />
            </button>
          </div>
          <pre>{{ model.daemonJson }}</pre>
          <ol>
            <li>写入宿主机 <code>/etc/docker/daemon.json</code></li>
            <li>执行 <code>sudo systemctl restart docker</code></li>
            <li>等待 Worker 下一次自动探测</li>
          </ol>
        </section>
      </aside>
    </div>

    <section class="form-panel docker-images-panel">
      <div class="section-heading">
        <div>
          <p class="eyebrow">本机库存</p>
          <h2>Docker 镜像</h2>
        </div>
        <button
          class="secondary compact"
          :disabled="imageBusy === 'load'"
          @click="loadImages"
        >
          <RefreshCw :class="{ spin: imageBusy === 'load' }" :size="16" />刷新
        </button>
      </div>
      <p class="quiet">
        库存由 Worker 从 Docker Engine 同步。删除时 API 与 Worker
        都会检查全部容器引用，运行中或已停止容器引用的镜像均不可删除。
      </p>
      <div
        v-if="imageBusy === 'load' && !images.length"
        class="docker-image-list"
        aria-busy="true"
      >
        <article v-for="index in 4" :key="index" class="skeleton-row">
          <div style="flex: 1; display: grid; gap: 8px">
            <span class="skeleton skeleton-title" style="width: 40%"></span
            ><span class="skeleton skeleton-text" style="width: 26%"></span>
          </div>
          <span class="skeleton skeleton-text" style="width: 80px"></span
          ><span class="skeleton skeleton-text" style="width: 110px"></span>
        </article>
      </div>
      <div v-else-if="images.length" class="docker-image-list">
        <article
          v-for="image in images"
          :key="image.id"
          class="docker-image-row"
        >
          <div>
            <strong>{{
              image.repoTags.length ? image.repoTags.join("、") : "未标记镜像"
            }}</strong
            ><code>{{ image.id }}</code>
          </div>
          <span>{{ imageSize(image.sizeBytes) }}</span
          ><span
            :class="[
              'status-pill',
              image.containerReferences ? 'pending' : 'active',
            ]"
            >{{ image.containerReferences }} 个容器引用</span
          ><button
            class="icon-action danger"
            title="删除镜像"
            :disabled="image.containerReferences > 0 || imageBusy === image.id"
            @click="deleteImage(image)"
          >
            <Trash2 :size="16" />
          </button>
        </article>
      </div>
      <div v-else class="context-empty">
        <Container :size="23" />
        <p>没有可显示的 Docker 镜像，或 Worker 尚未完成首次同步。</p>
      </div>
      <div v-if="imageJobs.length" class="image-job-history">
        <strong>最近删除任务</strong>
        <div v-for="job in imageJobs.slice(0, 8)" :key="job.id">
          <code>{{ job.imageId.slice(0, 24) }}…</code
          ><span
            :class="[
              'status-pill',
              job.status === 'succeeded' ? 'active' : 'pending',
            ]"
            >{{ job.status }}</span
          ><small>{{
            job.lastError || new Date(job.createdAt).toLocaleString("zh-CN")
          }}</small>
        </div>
      </div>
    </section>

    <Transition name="dialog-fade">
      <div
        v-if="restartDialogOpen"
        class="dialog-backdrop"
        @click.self="restartDialogOpen = false"
      >
        <section
          class="dialog-panel platform-restart-dialog"
          role="dialog"
          aria-modal="true"
          aria-labelledby="platform-restart-title"
        >
          <header>
            <div>
              <p class="eyebrow">平台维护</p>
              <h2 id="platform-restart-title">重启 CloudMeter 平台服务？</h2>
            </div>
            <button
              class="icon-action"
              title="关闭"
              @click="restartDialogOpen = false"
            >
              <X :size="18" />
            </button>
          </header>
          <div class="platform-restart-dialog-body">
            <p>
              Gateway、Web、API、应用路由、出口代理和 Worker
              会依次重启，控制台可能短暂断开，页面会自动恢复连接。
            </p>
            <dl>
              <div>
                <dt>不会重启</dt>
                <dd>PostgreSQL、Redis、Docker Engine、用户应用</dd>
              </div>
              <div>
                <dt>不会应用</dt>
                <dd>尚未写入宿主机的 daemon.json 配置</dd>
              </div>
            </dl>
          </div>
          <footer class="dialog-actions">
            <button
              class="secondary compact"
              :disabled="restartBusy"
              @click="restartDialogOpen = false"
            >
              取消</button
            ><button
              class="danger-button compact"
              :disabled="restartBusy"
              @click="restartPlatform"
            >
              <Power :size="16" />{{ restartBusy ? "正在提交" : "确认重启" }}
            </button>
          </footer>
        </section>
      </div>
    </Transition>
  </main>
</template>
