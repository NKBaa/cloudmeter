<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import {
  CheckCircle2,
  CircleAlert,
  Container,
  Copy,
  Database,
  KeyRound,
  Network,
  RefreshCw,
  Save,
  ShieldCheck,
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
    busy.value = "load";
    const data = await api<DockerSettings>("/admin/settings/docker");
    Object.assign(model, data, { registryPassword: "" });
    mirrorsText.value = (data.registryMirrors || []).join("\n");
    if (!silent) {
      error.value = "";
      message.value = "";
    }
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
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
    done("Docker 配置已保存；镜像库凭据和拉取超时立即生效，Daemon 配置需在宿主机应用");
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

onMounted(() => load());
</script>

<template>
  <main class="workspace admin-workspace docker-settings-view">
    <header>
      <div>
        <p class="eyebrow">容器基础设施</p>
        <h1>Docker 设置</h1>
        <p class="quiet">统一管理镜像来源、Registry 凭据、代理和 Worker 拉取策略。</p>
      </div>
      <button class="secondary compact" :disabled="busy === 'load'" @click="load()">
        <RefreshCw :class="{ spin: busy === 'load' }" :size="16" />刷新探测
      </button>
    </header>
    <p v-if="error" class="message sticky-message">{{ error }}</p>
    <p v-if="message" class="status-ok sticky-message">{{ message }}</p>

    <section class="docker-boundary-note">
      <ShieldCheck :size="20" />
      <div>
        <strong>安全边界清晰可见</strong>
        <p>平台只保存期望配置。默认镜像库、凭据和拉取超时由 Worker 使用；镜像加速与代理仍需在 Docker 宿主机写入 daemon.json 并重启 Docker。</p>
      </div>
    </section>

    <div class="docker-settings-layout">
      <form class="form-panel docker-settings-form" @submit.prevent="save">
        <section class="docker-form-section">
          <div class="config-heading">
            <Network :size="18" /><div><strong>镜像来源</strong><small>一行一个 HTTPS 镜像加速地址；可维护私有 Registry 默认前缀</small></div>
          </div>
          <label>Registry Mirrors
            <textarea v-model="mirrorsText" rows="4" placeholder="https://mirror.example.com" />
            <small>仅接受无内嵌账号密码的 HTTPS 地址，最多 16 个。</small>
          </label>
          <label>默认镜像库 / 命名空间
            <input v-model="model.defaultRegistry" placeholder="registry.example.com/team" />
            <small>产品版本填写相对镜像名时自动补全；已含 Registry 的完整镜像地址保持不变。</small>
          </label>
          <div class="field-row">
            <label>Registry 用户名<input v-model="model.registryUsername" autocomplete="username" /></label>
            <label>Registry 密码
              <input v-model="model.registryPassword" type="password" autocomplete="new-password" :placeholder="model.registryPasswordConfigured ? '已加密保存，留空保持不变' : '未配置'" />
            </label>
          </div>
        </section>

        <section class="docker-form-section">
          <div class="config-heading">
            <Container :size="18" /><div><strong>Daemon 代理</strong><small>用于 Docker 拉取镜像，不会注入用户应用容器</small></div>
          </div>
          <div class="field-row">
            <label>HTTP Proxy<input v-model="model.httpProxy" type="url" placeholder="http://proxy.example.com:3128" /></label>
            <label>HTTPS Proxy<input v-model="model.httpsProxy" type="url" placeholder="http://proxy.example.com:3128" /></label>
          </div>
          <label>NO_PROXY<input v-model="model.noProxy" placeholder="localhost,127.0.0.1,::1,.internal" /></label>
          <label>镜像拉取超时（秒）
            <input v-model.number="model.pullTimeoutSeconds" type="number" min="30" max="1800" step="1" required />
            <small>允许 30–1800 秒；超时后任务会返回可读的镜像拉取错误。</small>
          </label>
        </section>
        <div class="docker-form-actions">
          <p><KeyRound :size="15" />密码加密保存，接口与页面均不回显原文。</p>
          <button class="primary compact" :disabled="busy === 'save'"><Save :size="16" />保存设置</button>
        </div>
      </form>

      <aside class="docker-runtime-aside">
        <section class="form-panel daemon-status-card">
          <div class="daemon-status-heading">
            <span :class="['context-empty-icon', model.daemonRestartRequired && 'warning']">
              <CircleAlert v-if="model.daemonRestartRequired" :size="21" />
              <CheckCircle2 v-else :size="21" />
            </span>
            <div><p class="eyebrow">Worker 探测</p><h2>{{ model.daemonRestartRequired ? '配置尚未应用' : 'Daemon 配置一致' }}</h2></div>
          </div>
          <p v-if="model.lastCheckError" class="configuration-status blocked"><CircleAlert :size="15" />{{ model.lastCheckError }}</p>
          <dl class="daemon-facts">
            <div><dt>最近探测</dt><dd>{{ checkTime }}</dd></div>
            <div><dt>实际镜像源</dt><dd>{{ detectedMirrorText }}</dd></div>
            <div><dt>实际 HTTP 代理</dt><dd>{{ model.detectedHttpProxy || '未配置' }}</dd></div>
            <div><dt>实际 HTTPS 代理</dt><dd>{{ model.detectedHttpsProxy || '未配置' }}</dd></div>
            <div><dt>实际 NO_PROXY</dt><dd>{{ model.detectedNoProxy || '未配置' }}</dd></div>
          </dl>
        </section>

        <section class="form-panel daemon-json-card">
          <div class="config-heading with-action">
            <Database :size="18" /><div><strong>daemon.json 示例</strong><small>将期望配置应用到宿主机</small></div>
            <button class="icon-action" type="button" title="复制" @click="copyDaemonJSON"><Copy :size="16" /></button>
          </div>
          <pre>{{ model.daemonJson }}</pre>
          <ol>
            <li>写入宿主机 <code>/etc/docker/daemon.json</code></li>
            <li>执行 <code>sudo systemctl restart docker</code></li>
            <li>返回本页刷新，等待 Worker 重新探测</li>
          </ol>
        </section>
      </aside>
    </div>
  </main>
</template>
