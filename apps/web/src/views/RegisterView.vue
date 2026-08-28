<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ArrowRight, Check, Eye, EyeOff, LoaderCircle } from "@lucide/vue";
import { api } from "../api";
import BrandMark from "../components/BrandMark.vue";
import TurnstileWidget from "../components/TurnstileWidget.vue";

type RegistrationPolicy = {
  emailVerificationRequired: boolean;
  registrationEnabled: boolean;
  passwordRegistrationEnabled: boolean;
  turnstileSiteKey: string;
  turnstileRegistrationProtection: boolean;
  emailDomainWhitelist?: string[];
  blockEmailAliases?: boolean;
};

const form = reactive({
  displayName: "",
  email: "",
  password: "",
  code: "",
  turnstileToken: "",
});
const showPassword = ref(false);
const loading = ref(false);
const message = ref("");
const verificationRequired = ref(false);
const registrationEnabled = ref(false);
const passwordRegistrationEnabled = ref(true);
const emailDomainWhitelist = ref<string[]>([]);
const blockEmailAliases = ref(false);
const policyLoaded = ref(false);
const policyAvailable = ref(false);
const sending = ref(false);
const countdown = ref(0);
const turnstileSiteKey = ref("");
const turnstileRequired = ref(false);

const brandPoints = [
  "即刻获赠：新用户注册即享初始测试赠额",
  "极速开通：一键挑选应用模板，秒级实例拉起",
  "灵活结算：按量计费，无隐形费用与绑定周期",
];

const formDisabled = computed(
  () =>
    !policyAvailable.value ||
    !registrationEnabled.value ||
    !passwordRegistrationEnabled.value,
);
const submitLabel = computed(() => {
  if (!policyLoaded.value) return "正在检查注册状态";
  if (!policyAvailable.value) return "暂无法确认注册状态";
  if (!registrationEnabled.value) return "暂未开放注册";
  if (!passwordRegistrationEnabled.value) return "密码注册已关闭";
  return "创建平台账户";
});

function unavailableMessage() {
  if (policyAvailable.value && !registrationEnabled.value)
    return "当前暂未开放公开注册";
  if (policyAvailable.value && !passwordRegistrationEnabled.value)
    return "密码注册已由管理员关闭";
  return policyAvailable.value
    ? "当前暂未开放公开注册"
    : "暂时无法确认注册状态，请稍后重试";
}

onMounted(async () => {
  if (localStorage.getItem("session_token")) {
    window.location.replace("/console");
    return;
  }
  try {
    const policy = await api<RegistrationPolicy>("/auth/registration-policy");
    verificationRequired.value = policy.emailVerificationRequired;
    registrationEnabled.value = policy.registrationEnabled;
    passwordRegistrationEnabled.value =
      policy.passwordRegistrationEnabled !== false;
    emailDomainWhitelist.value = policy.emailDomainWhitelist || [];
    blockEmailAliases.value = policy.blockEmailAliases === true;
    turnstileSiteKey.value = policy.turnstileSiteKey || "";
    turnstileRequired.value = policy.turnstileRegistrationProtection;
    policyAvailable.value = true;
    if (!policy.registrationEnabled) message.value = "当前暂未开放公开注册";
    else if (!policy.passwordRegistrationEnabled)
      message.value = "密码注册已由管理员关闭";
  } catch (error) {
    message.value = (error as Error).message;
  } finally {
    policyLoaded.value = true;
  }
});

async function sendCode() {
  if (formDisabled.value) {
    message.value = unavailableMessage();
    return;
  }
  if (!form.email) {
    message.value = "请先填写邮箱";
    return;
  }

  sending.value = true;
  message.value = "";
  try {
    await api("/auth/verification-code", {
      method: "POST",
      body: JSON.stringify({ email: form.email }),
    });
    message.value = "验证码已发送，请检查邮箱";
    countdown.value = 60;
    const timer = window.setInterval(() => {
      countdown.value--;
      if (countdown.value <= 0) window.clearInterval(timer);
    }, 1000);
  } catch (error) {
    message.value = (error as Error).message;
  } finally {
    sending.value = false;
  }
}

async function register() {
  if (formDisabled.value) {
    message.value = unavailableMessage();
    return;
  }
  if (turnstileRequired.value && !form.turnstileToken) {
    message.value = "请先完成人机验证";
    return;
  }

  loading.value = true;
  message.value = "";
  try {
    await api("/auth/register", { method: "POST", body: JSON.stringify(form) });
    window.location.assign("/login");
  } catch (error) {
    message.value = (error as Error).message;
    form.turnstileToken = "";
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <div class="nextdev-auth-layout">
    <!-- 左侧：NextDevTpl 深色品牌面板 (AuthBrandPanel) -->
    <aside class="auth-brand-panel aura">
      <div class="auth-grid-bg bg-grid"></div>
      
      <div class="panel-top">
        <BrandMark />
      </div>

      <div class="panel-middle">
        <span class="eyebrow">JOIN CLOUDMETER</span>
        <h2 class="panel-headline">
          加入 CloudMeter<br />
          开启现代应用云之旅
        </h2>
        <p class="panel-sub">
          注册后即可挑选管理员发布的高性能应用模板，享受毫秒级按量计费与持久数据卷保护。
        </p>

        <ul class="panel-points">
          <li v-for="(pt, idx) in brandPoints" :key="idx">
            <span class="point-check"><Check :size="12" /></span>
            <span>{{ pt }}</span>
          </li>
        </ul>
      </div>

      <div class="panel-footer">
        <div class="mono-data text-2xl font-bold">100%</div>
        <div class="footer-note">容器实例隔离与按量计费，安全可靠的现代化架构。</div>
      </div>
    </aside>

    <!-- 右侧：表单主卡片与页脚 -->
    <div class="auth-form-side">
      <main class="auth-form-container">
        <div class="auth-form-card">
          <div class="mobile-logo">
            <BrandMark />
          </div>

          <div class="form-header">
            <h1>创建新账户</h1>
            <p>
              {{
                verificationRequired
                  ? "使用真实电子邮箱完成验证后开启工作区"
                  : "填写基本信息即可立即开启工作区"
              }}
            </p>
          </div>

          <div v-if="message" class="auth-alert" role="alert">
            {{ message }}
          </div>

          <form class="form-body" :aria-busy="loading" @submit.prevent="register">
            <div class="form-group">
              <label for="displayName">用户昵称 / 姓名</label>
              <input
                id="displayName"
                v-model="form.displayName"
                required
                maxlength="80"
                autocomplete="name"
                placeholder="例如：张三"
                :disabled="formDisabled"
              />
            </div>

            <div class="form-group">
              <label for="reg-email">电子邮箱</label>
              <input
                id="reg-email"
                v-model="form.email"
                type="email"
                required
                autocomplete="email"
                placeholder="name@example.com"
                :disabled="formDisabled"
              />
              <small v-if="emailDomainWhitelist.length" class="field-hint">
                仅允许域名：{{ emailDomainWhitelist.join("、") }}
              </small>
              <small v-else-if="blockEmailAliases" class="field-hint">
                不允许使用包含 + 或 . 的别名邮箱
              </small>
            </div>

            <!-- 邮箱验证码 -->
            <div v-if="verificationRequired" class="form-group">
              <label for="code">邮箱验证码</label>
              <div class="code-input-row">
                <input
                  id="code"
                  v-model="form.code"
                  required
                  inputmode="numeric"
                  maxlength="6"
                  autocomplete="one-time-code"
                  placeholder="6 位数字验证码"
                  :disabled="formDisabled"
                />
                <button
                  type="button"
                  class="ghost-btn send-btn"
                  :disabled="formDisabled || sending || countdown > 0"
                  @click="sendCode"
                >
                  {{
                    sending
                      ? "发送中"
                      : countdown > 0
                        ? `${countdown}s`
                        : "发送验证码"
                  }}
                </button>
              </div>
            </div>

            <!-- 密码输入 -->
            <div class="form-group">
              <label for="reg-password">设置登录密码</label>
              <div class="password-input-wrap">
                <input
                  id="reg-password"
                  v-model="form.password"
                  :type="showPassword ? 'text' : 'password'"
                  required
                  autocomplete="new-password"
                  placeholder="请输入您的登录密码"
                  :disabled="formDisabled"
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

            <!-- 提交注册按钮 -->
            <button
              type="submit"
              class="primary-btn submit-btn"
              :disabled="loading || formDisabled || (turnstileRequired && !form.turnstileToken)"
            >
              <LoaderCircle v-if="loading" class="spin" :size="16" />
              <template v-else>
                {{ submitLabel }}
                <ArrowRight v-if="registrationEnabled && policyAvailable" :size="16" />
              </template>
            </button>
          </form>

          <p class="form-footer-link">
            已经拥有账户？
            <RouterLink to="/login">返回登录控制台</RouterLink>
          </p>
        </div>
      </main>

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
.field-hint {
  font-size: 11px;
  color: var(--text-muted);
}
.code-input-row {
  display: grid;
  grid-template-columns: 1fr 110px;
  gap: 8px;
}
.send-btn {
  height: 40px;
  padding: 0 10px;
  font-size: 12.5px;
  white-space: nowrap;
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

