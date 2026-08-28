<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { ArrowRight, Check, Code2, Eye, EyeOff, LoaderCircle } from "@lucide/vue";
import { api } from "../api";
import BrandMark from "../components/BrandMark.vue";
import TurnstileWidget from "../components/TurnstileWidget.vue";

const form = reactive({ email: "", password: "", turnstileToken: "" });
const showPassword = ref(false);
const loading = ref(false);
const oauthLoading = ref("");
const message = ref("");
const providers = ref<string[]>([]);
const registrationEnabled = ref(false);
const passwordLoginEnabled = ref(true);
const turnstileSiteKey = ref("");
const turnstileRequired = ref(false);

const brandPoints = [
  "开箱即用：内置认证、容器编排、计量与反向网关",
  "精准计量：CPU、内存与持久卷按量计费，实时账单",
  "数据保障：数据卷与容器生命周期解耦，支持快照恢复",
];

onMounted(async () => {
  if (localStorage.getItem("session_token")) {
    window.location.replace("/console");
    return;
  }
  const [providersResult, policyResult] = await Promise.allSettled([
    api<{ providers: string[] }>("/auth/oauth/providers"),
    api<{
      registrationEnabled: boolean;
      passwordLoginEnabled: boolean;
      turnstileSiteKey: string;
      turnstileLoginProtection: boolean;
    }>("/auth/registration-policy"),
  ]);
  if (providersResult.status === "fulfilled")
    providers.value = providersResult.value.providers;
  if (policyResult.status === "fulfilled") {
    registrationEnabled.value = policyResult.value.registrationEnabled;
    passwordLoginEnabled.value =
      policyResult.value.passwordLoginEnabled !== false;
    turnstileSiteKey.value = policyResult.value.turnstileSiteKey || "";
    turnstileRequired.value = policyResult.value.turnstileLoginProtection;
  }
});

async function login() {
  if (turnstileRequired.value && !form.turnstileToken) {
    message.value = "请先完成人机验证";
    return;
  }
  loading.value = true;
  message.value = "";
  try {
    const data = await api<{ token: string }>("/auth/login", {
      method: "POST",
      body: JSON.stringify(form),
    });
    localStorage.setItem("session_token", data.token);
    window.location.assign("/console");
  } catch (e) {
    message.value = (e as Error).message;
    form.turnstileToken = "";
  } finally {
    loading.value = false;
  }
}

async function oauth(provider: string) {
  oauthLoading.value = provider;
  message.value = "";
  try {
    const data = await api<{ authorizationUrl: string }>(
      "/auth/oauth/" + provider + "/start",
      { method: "POST" },
    );
    window.location.assign(data.authorizationUrl);
  } catch (e) {
    message.value = (e as Error).message;
    oauthLoading.value = "";
  }
}
</script>

<template>
  <div class="nextdev-auth-layout">
    <!-- 左侧：NextDevTpl 深色品牌面板 (AuthBrandPanel) -->
    <aside class="auth-brand-panel aura">
      <div class="auth-grid-bg bg-grid"></div>
      
      <!-- 顶部 Logo -->
      <div class="panel-top">
        <BrandMark />
      </div>

      <!-- 中部卖点文案 -->
      <div class="panel-middle">
        <span class="eyebrow">ENTERPRISE SAAS READY</span>
        <h2 class="panel-headline">
          一处掌握应用的<br />
          整个运行与计量周期
        </h2>
        <p class="panel-sub">
          从镜像发布、网关反向代理到按量精准扣费，每个实例的生命周期变更与流水清晰可溯。
        </p>

        <ul class="panel-points">
          <li v-for="(pt, idx) in brandPoints" :key="idx">
            <span class="point-check"><Check :size="12" /></span>
            <span>{{ pt }}</span>
          </li>
        </ul>
      </div>

      <!-- 底部指标 -->
      <div class="panel-footer">
        <div class="mono-data text-2xl font-bold">99.99%</div>
        <div class="footer-note">生产环境运行时 SLA 保障，多节点秒级自愈与健康探针。</div>
      </div>
    </aside>

    <!-- 右侧：表单主卡片与页脚 -->
    <div class="auth-form-side">
      <main class="auth-form-container">
        <div class="auth-form-card">
          <!-- 移动端 Logo -->
          <div class="mobile-logo">
            <BrandMark />
          </div>

          <!-- 标题区 -->
          <div class="form-header">
            <h1>欢迎回来</h1>
            <p>使用你的 CloudMeter 账户登录控制台</p>
          </div>

          <!-- 错误提醒 -->
          <div v-if="message" class="auth-alert" role="alert">
            {{ message }}
          </div>

          <!-- OAuth 第三方快捷登录 -->
          <div v-if="providers.length" class="oauth-section">
            <button
              v-if="providers.includes('github')"
              type="button"
              class="oauth-btn"
              :disabled="!!oauthLoading"
              @click="oauth('github')"
            >
              <LoaderCircle v-if="oauthLoading === 'github'" class="spin" :size="16" />
              <Code2 v-else :size="16" />
              使用 GitHub 登录
            </button>
            <button
              v-if="providers.includes('linuxdo')"
              type="button"
              class="oauth-btn"
              :disabled="!!oauthLoading"
              @click="oauth('linuxdo')"
            >
              <LoaderCircle v-if="oauthLoading === 'linuxdo'" class="spin" :size="16" />
              <span v-else class="linuxdo-badge">L</span>
              使用 LinuxDo 登录
            </button>
          </div>

          <!-- 分隔线 -->
          <div v-if="providers.length && passwordLoginEnabled" class="form-divider">
            <span>或者使用邮箱登录</span>
          </div>

          <!-- 邮箱密码登录表单 -->
          <form v-if="passwordLoginEnabled" class="form-body" @submit.prevent="login">
            <div class="form-group">
              <label for="email">电子邮箱</label>
              <input
                id="email"
                v-model="form.email"
                type="email"
                required
                autocomplete="email"
                placeholder="name@example.com"
                :disabled="loading"
              />
            </div>

            <div class="form-group">
              <div class="label-row">
                <label for="password">登录密码</label>
              </div>
              <div class="password-input-wrap">
                <input
                  id="password"
                  v-model="form.password"
                  :type="showPassword ? 'text' : 'password'"
                  required
                  autocomplete="current-password"
                  placeholder="输入你的登录密码"
                  :disabled="loading"
                />
                <button
                  type="button"
                  class="password-toggle"
                  tabindex="-1"
                  @click="showPassword = !showPassword"
                >
                  <EyeOff v-if="showPassword" :size="16" />
                  <Eye v-else :size="16" />
                </button>
              </div>
            </div>

            <!-- Turnstile 验证 -->
            <TurnstileWidget
              v-if="turnstileRequired && turnstileSiteKey"
              :site-key="turnstileSiteKey"
              @token="form.turnstileToken = $event"
            />

            <!-- 登录按钮 -->
            <button
              type="submit"
              class="primary-btn submit-btn"
              :disabled="loading || !!oauthLoading || (turnstileRequired && !form.turnstileToken)"
            >
              <LoaderCircle v-if="loading" class="spin" :size="16" />
              <template v-else>
                登录控制台
                <ArrowRight :size="16" />
              </template>
            </button>
          </form>

          <!-- 注册引导 -->
          <p v-if="registrationEnabled" class="form-footer-link">
            还没有账户？
            <RouterLink to="/register">立即注册新账户</RouterLink>
          </p>
        </div>
      </main>

      <!-- 页脚 -->
      <footer class="auth-simple-footer">
        <p class="mono-data text-xs text-muted">
          © {{ new Date().getFullYear() }} CloudMeter. 现代应用云平台.
        </p>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.nextdev-auth-layout {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 1.05fr 1fr;
  background: var(--canvas);
  color: var(--text);
}

/* 左侧深色面板 */
.auth-brand-panel {
  position: relative;
  overflow: hidden;
  background: #0e110f;
  color: #edf2ee;
  padding: 48px;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  border-right: 1px solid rgba(255, 255, 255, 0.08);
}
.auth-grid-bg {
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: 0.4;
  mask-image: radial-gradient(75% 75% at 30% 20%, black, transparent);
  -webkit-mask-image: radial-gradient(75% 75% at 30% 20%, black, transparent);
}
.panel-top,
.panel-middle,
.panel-footer {
  position: relative;
  z-index: 1;
}
.panel-headline {
  font-size: clamp(26px, 2.5vw, 36px);
  font-weight: 800;
  line-height: 1.15;
  letter-spacing: -0.03em;
  margin: 12px 0 16px;
  color: #edf2ee;
}
.panel-sub {
  font-size: 14.5px;
  line-height: 1.65;
  color: #9caaa0;
  max-width: 440px;
  margin-bottom: 28px;
}
.panel-points {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.panel-points li {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 13.5px;
  color: #edf2ee;
}
.point-check {
  width: 20px;
  height: 20px;
  flex: 0 0 20px;
  border-radius: 50%;
  background: var(--accent);
  color: #0e110f;
  display: grid;
  place-items: center;
}
.panel-footer {
  padding-top: 24px;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
  display: flex;
  align-items: center;
  gap: 16px;
}
.footer-note {
  font-size: 11.5px;
  color: #9caaa0;
  max-width: 280px;
  line-height: 1.5;
}

/* 右侧表单区域 */
.auth-form-side {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  background: var(--canvas);
}
.auth-form-container {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px 24px;
}
.auth-form-card {
  width: 100%;
  max-width: 400px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.mobile-logo {
  display: none;
  margin-bottom: 8px;
}
.form-header h1 {
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.02em;
  margin: 0 0 6px;
}
.form-header p {
  font-size: 13.5px;
  color: var(--text-muted);
  margin: 0;
}
.auth-alert {
  padding: 10px 14px;
  border-radius: 8px;
  background: rgba(239, 68, 68, 0.12);
  border: 1px solid rgba(239, 68, 68, 0.25);
  color: #f87171;
  font-size: 12.5px;
}
.oauth-section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.oauth-btn {
  height: 40px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--surface);
  color: var(--text);
  font-size: 13px;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  cursor: pointer;
  transition: all 0.15s ease;
}
.oauth-btn:hover:not(:disabled) {
  background: var(--hover);
  border-color: var(--line-strong);
}
.linuxdo-badge {
  width: 16px;
  height: 16px;
  border-radius: 4px;
  background: var(--text);
  color: var(--canvas);
  font-size: 10px;
  font-weight: 800;
  display: grid;
  place-items: center;
}
.form-divider {
  display: flex;
  align-items: center;
  text-align: center;
  color: var(--text-muted);
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  margin: 4px 0;
}
.form-divider::before,
.form-divider::after {
  content: "";
  flex: 1;
  border-bottom: 1px solid var(--line);
}
.form-divider span {
  padding: 0 12px;
}
.form-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.form-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.form-group label {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text);
}
.label-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.password-input-wrap {
  position: relative;
  display: flex;
  align-items: center;
}
.password-input-wrap input {
  width: 100%;
  padding-right: 36px;
}
.password-toggle {
  position: absolute;
  right: 10px;
  background: transparent;
  border: 0;
  color: var(--text-muted);
  display: grid;
  place-items: center;
  cursor: pointer;
}
.password-toggle:hover {
  color: var(--text);
}
.submit-btn {
  width: 100%;
  height: 42px;
  font-size: 14px;
  margin-top: 4px;
}
.form-footer-link {
  text-align: center;
  font-size: 13px;
  color: var(--text-muted);
  margin: 0;
}
.form-footer-link a {
  color: var(--accent);
  text-decoration: none;
  font-weight: 600;
}
.form-footer-link a:hover {
  text-decoration: underline;
}
.auth-simple-footer {
  padding: 20px;
  text-align: center;
  border-top: 1px solid var(--line);
}

@media (max-width: 960px) {
  .nextdev-auth-layout {
    grid-template-columns: 1fr;
  }
  .auth-brand-panel {
    display: none;
  }
  .mobile-logo {
    display: block;
  }
}
</style>

