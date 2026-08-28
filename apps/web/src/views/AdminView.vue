<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
} from "vue";
import {
  Activity,
  AppWindow,
  ArrowRight,
  BadgeCent,
  BadgeDollarSign,
  CheckCircle2,
  CircleAlert,
  CirclePause,
  CirclePlay,
  Coins,
  Container,
  Copy,
  CreditCard,
  Eye,
  FileClock,
  Globe2,
  KeyRound,
  LogIn,
  LogOut,
  MailCheck,
  Megaphone,
  MessageSquareText,
  Save,
  Settings2,
  ShieldPlus,
  Users,
  X,
} from "@lucide/vue";
import { api, logout } from "../api";
import BrandMark from "../components/BrandMark.vue";

type User = {
  id: string;
  email: string;
  displayName: string;
  status: string;
  roles: string[];
  createdAt: string;
  balanceCents: number;
};
type Announcement = {
  id: string;
  title: string;
  content: string;
  severity: string;
  published: boolean;
  createdAt: string;
};
type OAuthSettings = {
  enabled: boolean;
  clientId: string;
  clientSecret: string;
  scopes: string;
  secretConfigured: boolean;
  minimumTrustLevel: number;
  publicBaseUrlConfigured: boolean;
  callbackUrl: string;
};
type CurrentUser = {
  ID: string;
  Email: string;
  DisplayName: string;
  Roles: string[];
};

const props = defineProps<{
  page?:
    "overview" | "users" | "announcements" | "registration" | "mail" | "oauth";
}>();
const page = computed(() => props.page || "overview");
const pageTitle = computed(
  () =>
    ({
      overview: "平台总览",
      users: "用户管理",
      announcements: "公告管理",
      registration: "注册策略",
      mail: "SMTP 邮件",
      oauth: "OAuth 认证",
    })[page.value],
);
const pageDescription = computed(
  () =>
    ({
      overview: "全平台用户、模板、部署实例和系统服务运行指标总览。",
      users: "集中管理平台账户、角色权限、钱包余额调整与赠送额度发放。",
      announcements: "发布或下线全站全局广播公告通知与紧急提醒。",
      registration:
        "配置公开注册、密码策略、域名白名单、人机验证与前台侧栏显隐。",
      mail: "配置 SMTP 发信服务器与邮件验证码服务参数。",
      oauth: "配置 GitHub 与 LINUX DO 第三方登录 OAuth 凭据与信任等级。",
    })[page.value],
);

const summary = ref({ users: 0, products: 0, activeDeployments: 0 });
const users = ref<User[]>([]);
const announcements = ref<Announcement[]>([]);
const currentUser = ref<CurrentUser | null>(null);
const error = ref("");
const message = ref("");
const busy = ref("");
const testRecipient = ref("");
const account = reactive({
  displayName: "",
  email: "",
  password: "",
  role: "user",
});
const smtp = reactive({
  enabled: false,
  host: "",
  port: 587,
  username: "",
  password: "",
  passwordConfigured: false,
  fromEmail: "",
  fromName: "CloudMeter",
  tlsMode: "starttls",
});
const smtpReady = ref(false);
const auth = reactive({
  registrationEnabled: false,
  passwordLoginEnabled: true,
  passwordRegistrationEnabled: true,
  emailVerificationRequired: false,
  blockEmailAliases: true,
  emailDomainWhitelist: [] as string[],
});
const turnstile = reactive({
  enabled: false,
  siteKey: "",
  secretKey: "",
  secretConfigured: false,
  loginProtection: false,
  registrationProtection: false,
});
const sidebar = reactive<Record<string, boolean>>({
  overview: true,
  deploy: true,
  apps: true,
  releases: true,
  backups: true,
  billing: true,
  recharge: true,
  checkin: true,
  usage: true,
  tickets: true,
  faq: true,
});
const sidebarLabels: Record<string, string> = {
  overview: "概览",
  deploy: "部署应用",
  apps: "我的应用",
  releases: "版本历史",
  backups: "备份与恢复",
  billing: "余额与账单",
  recharge: "账户充值",
  checkin: "每日签到",
  usage: "用量明细",
  tickets: "工单支持",
  faq: "常见问答",
};
const github = reactive<OAuthSettings>({
  enabled: false,
  clientId: "",
  clientSecret: "",
  scopes: "read:user user:email",
  secretConfigured: false,
  minimumTrustLevel: 0,
  publicBaseUrlConfigured: false,
  callbackUrl: "",
});
const linuxdo = reactive<OAuthSettings>({
  enabled: false,
  clientId: "",
  clientSecret: "",
  scopes: "openid email profile",
  secretConfigured: false,
  minimumTrustLevel: 0,
  publicBaseUrlConfigured: false,
  callbackUrl: "",
});
const notice = reactive({
  title: "",
  content: "",
  severity: "info",
  published: true,
});
const credit = reactive({
  userId: "",
  amountCents: 0,
  businessRef: "",
  note: "",
  expiresAt: "",
});
const editingUser = ref<User | null>(null);
const walletEdit = reactive({ targetBalanceYuan: 0, note: "" });
const roleEdit = ref<"user" | "admin">("user");

const isSuperAdmin = computed(
  () => currentUser.value?.Roles.includes("super_admin") === true,
);
const isAdmin = computed(
  () =>
    currentUser.value?.Roles.some(
      (role) => role === "admin" || role === "super_admin",
    ) === true,
);
const smtpFormReady = computed(
  () =>
    smtp.enabled &&
    smtp.host.trim().length > 0 &&
    smtp.port >= 1 &&
    smtp.port <= 65535 &&
    (!smtp.username.trim() ||
      smtp.passwordConfigured ||
      smtp.password.length > 0) &&
    /^[^\s@]+@[^\s@]+$/.test(smtp.fromEmail.trim()) &&
    ["none", "starttls", "tls"].includes(smtp.tlsMode),
);

function done(text: string) {
  message.value = text;
  error.value = "";
}
function failed(value: unknown) {
  error.value = (value as Error).message;
  message.value = "";
}

async function load() {
  try {
    const me = await api<CurrentUser>("/me");
    currentUser.value = me;
    if (!me.Roles.some((role) => role === "admin" || role === "super_admin")) {
      location.replace("/console");
      return;
    }

    const [summaryData, userData, noticeData] = await Promise.all([
      api<typeof summary.value>("/admin/summary"),
      api<{ users: User[] }>("/admin/users"),
      api<{ announcements: Announcement[] }>("/admin/announcements"),
    ]);
    summary.value = summaryData;
    users.value = userData.users;
    announcements.value = noticeData.announcements;
    if (!credit.userId && users.value.length)
      credit.userId =
        users.value.find((item) => item.roles.includes("user"))?.id ||
        users.value[0].id;

    if (isSuperAdmin.value) {
      const [mailData, authData, oauthData, turnstileData, sidebarData] =
        await Promise.all([
          api<typeof smtp & { ready: boolean }>("/admin/settings/mail"),
          api<typeof auth>("/admin/settings/auth"),
          api<{ providers: (OAuthSettings & { provider: string })[] }>(
            "/admin/settings/oauth",
          ),
          api<typeof turnstile>("/admin/settings/turnstile"),
          api<{ visibility: Record<string, boolean> }>("/sidebar-visibility"),
        ]);
      const { ready, ...mailSettings } = mailData;
      smtpReady.value = ready;
      Object.assign(smtp, mailSettings);
      Object.assign(auth, authData);
      Object.assign(turnstile, turnstileData, { secretKey: "" });
      Object.assign(sidebar, sidebarData.visibility);
      oauthData.providers.forEach((item) =>
        Object.assign(item.provider === "github" ? github : linuxdo, item),
      );
    }
    error.value = "";
  } catch (value) {
    failed(value);
  }
}

function scrollToCurrentSection() {
  const hash = location.hash.slice(1);
  if (!hash) return;
  let id = hash;
  try {
    id = decodeURIComponent(hash);
  } catch {}
  document.getElementById(id)?.scrollIntoView({ block: "start" });
}
async function settleSectionPosition() {
  await nextTick();
  requestAnimationFrame(() => requestAnimationFrame(scrollToCurrentSection));
}
function handleHashChange() {
  void settleSectionPosition();
}
onMounted(async () => {
  window.addEventListener("hashchange", handleHashChange);
  await load();
  await settleSectionPosition();
});
onBeforeUnmount(() =>
  window.removeEventListener("hashchange", handleHashChange),
);

async function createUser() {
  try {
    busy.value = "create-user";
    await api("/admin/users", {
      method: "POST",
      body: JSON.stringify(account),
    });
    Object.assign(account, {
      displayName: "",
      email: "",
      password: "",
      role: "user",
    });
    done("账户已创建");
    await load();
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function setUserStatus(user: User) {
  const status = user.status === "active" ? "suspended" : "active";
  try {
    busy.value = user.id;
    await api("/admin/users/" + user.id + "/status", {
      method: "PATCH",
      body: JSON.stringify({ status }),
    });
    user.status = status;
    done(status === "active" ? "账户已恢复" : "账户已停用");
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
function primaryRole(user: User): "user" | "admin" {
  return user.roles.includes("admin") ? "admin" : "user";
}
function roleLabel(user: User) {
  return user.roles.includes("super_admin")
    ? "超级管理员"
    : user.roles.includes("admin")
      ? "管理员"
      : "普通用户";
}
function openUserEditor(user: User) {
  editingUser.value = user;
  walletEdit.targetBalanceYuan = user.balanceCents / 100;
  walletEdit.note = "";
  roleEdit.value = primaryRole(user);
}
async function saveUserChanges() {
  const user = editingUser.value;
  if (!user) return;
  const target = Math.round(walletEdit.targetBalanceYuan * 100),
    amount = target - user.balanceCents;
  const roleChanged =
    !user.roles.includes("super_admin") && roleEdit.value !== primaryRole(user);
  if (amount === 0 && !roleChanged) {
    failed(new Error("账户信息没有变化"));
    return;
  }
  try {
    busy.value = "user-save";
    const updates: string[] = [];
    if (roleChanged) {
      const result = await api<{ roles: string[] }>(
        "/admin/users/" + user.id + "/role",
        { method: "PATCH", body: JSON.stringify({ role: roleEdit.value }) },
      );
      user.roles = result.roles;
      updates.push(
        roleEdit.value === "admin"
          ? "权限已切换为管理员"
          : "权限已切换为普通用户",
      );
    }
    if (amount !== 0) {
      const result = await api<{ balanceCents: number }>(
        "/admin/users/" + user.id + "/wallet/adjust",
        {
          method: "POST",
          body: JSON.stringify({
            amountCents: amount,
            businessRef: "admin-edit/" + crypto.randomUUID(),
            note: walletEdit.note.trim() || "管理员在用户编辑栏调整余额",
          }),
        },
      );
      user.balanceCents = result.balanceCents;
      updates.push("余额已更新并写入调账账本");
    }
    done(updates.join("；"));
    editingUser.value = null;
    await load();
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function impersonate(user: User, writeEnabled = false) {
  if (
    writeEnabled &&
    !confirm("代用户执行写操作会记录真实管理员身份。确认继续？")
  )
    return;
  const confirmation = writeEnabled
    ? prompt("输入目标用户邮箱以启用写操作：" + user.email, "") || ""
    : "";
  if (writeEnabled && !confirmation) return;
  try {
    busy.value = "impersonate-" + user.id;
    const current = localStorage.getItem("session_token") || "";
    const result = await api<{ token: string }>(
      "/admin/users/" + user.id + "/impersonation",
      { method: "POST", body: JSON.stringify({ writeEnabled, confirmation }) },
    );
    localStorage.setItem("admin_session_token", current);
    localStorage.setItem("session_token", result.token);
    location.assign("/console");
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function saveAuth() {
  if (auth.emailVerificationRequired && !smtpReady.value) {
    failed(new Error("请先启用并保存有效的 SMTP 配置，再开启注册邮箱验证"));
    return;
  }
  try {
    busy.value = "auth";
    await api("/admin/settings/auth", {
      method: "PUT",
      body: JSON.stringify(auth),
    });
    done("注册策略已保存");
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function saveTurnstile() {
  if (
    turnstile.enabled &&
    (!turnstile.siteKey.trim() ||
      !(turnstile.secretConfigured || turnstile.secretKey.trim()))
  ) {
    failed(new Error("启用前请填写完整的 Site Key 和 Secret Key"));
    return;
  }
  try {
    busy.value = "turnstile";
    const result = await api<{ secretConfigured: boolean }>(
      "/admin/settings/turnstile",
      { method: "PUT", body: JSON.stringify(turnstile) },
    );
    turnstile.secretConfigured = result.secretConfigured;
    turnstile.secretKey = "";
    done("机器人保护设置已保存");
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function saveSidebar() {
  try {
    busy.value = "sidebar";
    await api("/admin/settings/sidebar-visibility", {
      method: "PUT",
      body: JSON.stringify({ visibility: sidebar }),
    });
    done("普通用户侧栏显示设置已保存");
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function saveOAuth(provider: string, value: OAuthSettings) {
  const payload = {
    enabled: value.enabled,
    clientId: value.clientId,
    clientSecret: value.clientSecret,
    scopes: value.scopes,
    minimumTrustLevel: value.minimumTrustLevel,
  };
  try {
    busy.value = "oauth-" + provider;
    await api("/admin/settings/oauth/" + provider, {
      method: "PUT",
      body: JSON.stringify(payload),
    });
    value.clientSecret = "";
    done("OAuth 设置已保存");
    await load();
  } catch (reason) {
    failed(reason);
  } finally {
    busy.value = "";
  }
}
async function copyCallback(value: string) {
  try {
    await navigator.clipboard.writeText(value);
    done("回调地址已复制");
  } catch {
    failed(new Error("无法复制，请手动选择回调地址"));
  }
}
async function saveMail() {
  if (auth.emailVerificationRequired && !smtpFormReady.value) {
    failed(new Error("注册邮箱验证已启用，SMTP 必须保持启用并填写有效配置"));
    return;
  }
  try {
    busy.value = "mail";
    const result = await api<{ ready: boolean; passwordConfigured: boolean }>(
      "/admin/settings/mail",
      { method: "PUT", body: JSON.stringify(smtp) },
    );
    smtpReady.value = result.ready;
    smtp.passwordConfigured = result.passwordConfigured;
    smtp.password = "";
    done("SMTP 设置已保存");
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function testMail() {
  if (!testRecipient.value) {
    error.value = "请填写测试收件邮箱";
    return;
  }
  try {
    busy.value = "test-mail";
    await api("/admin/settings/mail/test", {
      method: "POST",
      body: JSON.stringify({ email: testRecipient.value }),
    });
    done("测试邮件已发送");
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function publish() {
  try {
    busy.value = "publish-announcement";
    await api("/admin/announcements", {
      method: "POST",
      body: JSON.stringify(notice),
    });
    Object.assign(notice, {
      title: "",
      content: "",
      severity: "info",
      published: true,
    });
    done("公告已创建");
    await load();
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function toggleAnnouncement(item: Announcement) {
  try {
    busy.value = item.id;
    await api("/admin/announcements/" + item.id, {
      method: "PATCH",
      body: JSON.stringify({ published: !item.published }),
    });
    item.published = !item.published;
    done(item.published ? "公告已发布" : "公告已下线");
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function grantCredit() {
  if (!credit.userId || credit.amountCents <= 0 || !credit.businessRef.trim()) {
    error.value = "请选择账户并填写正整数额度和业务引用";
    return;
  }
  try {
    busy.value = "grant-credit";
    await api("/admin/users/" + credit.userId + "/credits", {
      method: "POST",
      body: JSON.stringify({
        ...credit,
        amountCents: Math.trunc(credit.amountCents),
        businessRef: credit.businessRef.trim(),
        expiresAt: credit.expiresAt
          ? new Date(credit.expiresAt).toISOString()
          : null,
      }),
    });
    Object.assign(credit, {
      amountCents: 0,
      businessRef: "",
      note: "",
      expiresAt: "",
    });
    done("赠送额度已发放");
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
</script>

<template>
  <main class="workspace admin-workspace">
    <header id="overview" class="page-heading">
      <div>
        <p class="eyebrow">{{ isSuperAdmin ? "超级管理员" : "管理员" }}</p>
        <h1>{{ pageTitle }}</h1>
        <p class="quiet">{{ pageDescription }}</p>
      </div>
    </header>
    <p v-if="error" class="message sticky-message">{{ error }}</p>
    <p v-if="message" class="status-ok sticky-message">{{ message }}</p>

    <section v-if="page === 'overview'" class="nextdev-stat-grid">
      <div class="stat-card accent">
        <div class="stat-card-header">
          <span class="stat-label">平台注册用户</span>
          <Users class="stat-icon" :size="16" />
        </div>
        <div class="stat-value mono-data">{{ summary.users }}</div>
        <div class="stat-hint">含超级管理与普通租户</div>
      </div>

      <div class="stat-card">
        <div class="stat-card-header">
          <span class="stat-label">应用模板总数</span>
          <AppWindow class="stat-icon" :size="16" />
        </div>
        <div class="stat-value mono-data">{{ summary.products }}</div>
        <div class="stat-hint">全部已配置产品模板</div>
      </div>

      <div class="stat-card">
        <div class="stat-card-header">
          <span class="stat-label">活跃运行部署</span>
          <Activity class="stat-icon" :size="16" />
        </div>
        <div class="stat-value mono-data">{{ summary.activeDeployments }}</div>
        <div class="stat-hint">正在执行编排的任务</div>
      </div>
    </section>
    <section v-if="page === 'overview'" class="admin-quick-grid">
      <RouterLink to="/admin/products"
        ><span><AppWindow :size="18" /></span>
        <div>
          <strong>产品与版本</strong><small>创建产品、导入模板并测试发布</small>
        </div>
        <ArrowRight :size="16"
      /></RouterLink>
      <RouterLink to="/admin/tickets"
        ><span><MessageSquareText :size="18" /></span>
        <div>
          <strong>工单管理</strong><small>处理用户问题和回复进度</small>
        </div>
        <ArrowRight :size="16"
      /></RouterLink>
      <RouterLink to="/admin/docker" class="docker-quick-link"
        ><span><Container :size="18" /></span>
        <div>
          <strong>Docker 与镜像源</strong
          ><small>镜像加速、Registry、代理和拉取探测</small>
        </div>
        <ArrowRight :size="16"
      /></RouterLink>
    </section>

    <section v-if="page === 'users'" class="admin-section">
      <div class="section-heading">
        <div>
          <p class="eyebrow">身份与权限</p>
          <h2>{{ isSuperAdmin ? "账户管理" : "账户目录" }}</h2>
        </div>
        <span>{{ users.length }} 个账户</span>
      </div>
      <div :class="['admin-split', { 'read-only': !isSuperAdmin }]">
        <form
          v-if="isSuperAdmin"
          class="inline-form"
          @submit.prevent="createUser"
        >
          <h3>创建账户</h3>
          <label>姓名<input v-model="account.displayName" required /></label>
          <label
            >邮箱<input v-model="account.email" type="email" required
          /></label>
          <label
            >初始密码<input
              v-model="account.password"
              type="password"
              required
          /></label>
          <label
            >角色<select v-model="account.role">
              <option value="user">用户</option>
              <option value="admin">管理员</option>
            </select></label
          >
          <button class="primary compact" :disabled="busy === 'create-user'">
            <ShieldPlus :size="16" />创建账户
          </button>
        </form>
        <div class="data-list">
          <article v-for="user in users" :key="user.id" class="data-row">
            <div>
              <strong>{{ user.displayName }}</strong
              ><small>{{ user.email }} · {{ roleLabel(user) }}</small>
            </div>
            <span :class="['status-pill', user.status]">{{
              user.status === "active" ? "正常" : "已停用"
            }}</span>
            <span class="user-balance"
              >¥{{ (user.balanceCents / 100).toFixed(2) }}</span
            >
            <div v-if="isAdmin" class="user-row-actions">
              <template
                v-if="user.roles.includes('user') && user.status === 'active'"
              >
                <template v-if="isSuperAdmin">
                  <button
                    class="icon-action"
                    title="只读查看用户控制台"
                    :disabled="busy === 'impersonate-' + user.id"
                    @click="impersonate(user)"
                  >
                    <Eye :size="16" />
                  </button>
                  <button
                    class="icon-action"
                    title="代用户执行操作"
                    :disabled="busy === 'impersonate-' + user.id"
                    @click="impersonate(user, true)"
                  >
                    <LogIn :size="16" />
                  </button>
                </template>
              </template>
              <button
                v-if="isSuperAdmin"
                class="icon-action"
                :title="user.status === 'active' ? '停用账户' : '恢复账户'"
                :disabled="busy === user.id"
                @click="setUserStatus(user)"
              >
                <CirclePause
                  v-if="user.status === 'active'"
                  :size="16"
                /><CirclePlay v-else :size="16" />
              </button>
              <button
                class="icon-action"
                title="编辑用户与余额"
                @click="openUserEditor(user)"
              >
                <Settings2 :size="16" />
              </button>
            </div>
          </article>
        </div>
      </div>
    </section>

    <section
      v-if="page === 'users' && isSuperAdmin"
      class="form-panel credit-admin"
    >
      <h2><Coins :size="19" />发放赠送额度</h2>
      <form @submit.prevent="grantCredit">
        <label
          >目标账户<select v-model="credit.userId" required>
            <option
              v-for="user in users.filter((item) => item.status === 'active')"
              :key="user.id"
              :value="user.id"
            >
              {{ user.displayName }} · {{ user.email }}
            </option>
          </select></label
        >
        <div class="field-row">
          <label
            >额度（分）<input
              v-model.number="credit.amountCents"
              type="number"
              min="1"
              step="1"
              required /></label
          ><label
            >到期时间<input v-model="credit.expiresAt" type="datetime-local"
          /></label>
        </div>
        <label
          >唯一业务引用<input
            v-model="credit.businessRef"
            maxlength="128"
            placeholder="campaign-2026-08/user-001"
            required
        /></label>
        <label>说明<input v-model="credit.note" maxlength="500" /></label>
        <button class="primary compact" :disabled="busy === 'grant-credit'">
          <Coins :size="16" />发放额度
        </button>
      </form>
    </section>

    <div
      v-if="isSuperAdmin && ['registration', 'mail', 'oauth'].includes(page)"
      class="admin-grid"
    >
      <section v-if="page === 'registration'" class="form-panel">
        <h2><Globe2 :size="19" />注册策略</h2>
        <form @submit.prevent="saveAuth">
          <div class="switch-setting">
            <div>
              <strong>注册已启用</strong
              ><small>关闭后公开注册暂停，但下列配置会被保留</small>
            </div>
            <label class="switch"
              ><input
                v-model="auth.registrationEnabled"
                type="checkbox" /><span
            /></label>
          </div>
          <div class="switch-setting">
            <div>
              <strong>允许密码登录</strong
              ><small>关闭后密码方式登录将被后端拒绝</small>
            </div>
            <label class="switch"
              ><input
                v-model="auth.passwordLoginEnabled"
                type="checkbox"
                :disabled="!auth.registrationEnabled" /><span
            /></label>
          </div>
          <div class="switch-setting">
            <div>
              <strong>允许密码注册</strong
              ><small>可单独控制是否允许密码方式注册</small>
            </div>
            <label class="switch"
              ><input
                v-model="auth.passwordRegistrationEnabled"
                type="checkbox"
                :disabled="!auth.registrationEnabled" /><span
            /></label>
          </div>
          <div class="switch-setting">
            <div>
              <strong>注册时要求邮箱验证码</strong
              ><small>需先启用并保存有效的 SMTP 配置</small>
            </div>
            <label class="switch"
              ><input
                v-model="auth.emailVerificationRequired"
                type="checkbox"
                :disabled="!auth.registrationEnabled" /><span
            /></label>
          </div>
          <p
            v-if="!smtpReady && auth.emailVerificationRequired"
            class="configuration-hint"
          >
            <CircleAlert :size="16" />需先在右侧启用并保存有效 SMTP 配置
          </p>
          <div class="switch-setting">
            <div>
              <strong>阻止邮箱别名</strong
              ><small>拒绝邮箱用户名部分包含 + 或 . 的地址</small>
            </div>
            <label class="switch"
              ><input
                v-model="auth.blockEmailAliases"
                type="checkbox"
                :disabled="!auth.registrationEnabled" /><span
            /></label>
          </div>
          <label
            :class="['field-disabled', { disabled: !auth.registrationEnabled }]"
            >邮箱域白名单<textarea
              :disabled="!auth.registrationEnabled"
              :value="auth.emailDomainWhitelist.join('\n')"
              @input="
                auth.emailDomainWhitelist = (
                  $event.target as HTMLTextAreaElement
                ).value
                  .split(/\r?\n/)
                  .map((item) => item.trim())
                  .filter(Boolean)
              "
              rows="5"
              placeholder="163.com&#10;qq.com&#10;icloud.com"
            /><small
              >留空允许全部域名；每行一个域名，仅精确匹配该域名</small
            ></label
          >
          <button class="primary compact" :disabled="busy === 'auth'">
            <Save :size="16" />保存策略
          </button>
        </form>
      </section>

      <section v-if="page === 'registration'" class="form-panel">
        <h2><ShieldPlus :size="19" />Cloudflare Turnstile</h2>
        <p class="configuration-hint neutral">
          Token 会由服务端向 Cloudflare 校验，Secret Key
          仅加密保存且不会通过读取接口返回。
        </p>
        <form @submit.prevent="saveTurnstile">
          <div class="switch-setting">
            <div>
              <strong>启用 Turnstile</strong
              ><small>Token 由服务端向 Cloudflare 校验</small>
            </div>
            <label class="switch"
              ><input v-model="turnstile.enabled" type="checkbox" /><span
            /></label>
          </div>
          <label
            >Site Key<input
              v-model="turnstile.siteKey"
              maxlength="256"
              :required="turnstile.enabled"
              placeholder="0x4AAAAA..."
          /></label>
          <label
            >Secret Key<input
              v-model="turnstile.secretKey"
              type="password"
              maxlength="512"
              autocomplete="new-password"
              :required="turnstile.enabled && !turnstile.secretConfigured"
              :placeholder="
                turnstile.secretConfigured
                  ? '已加密保存，留空保持不变'
                  : '启用时必填'
              "
            /><small>{{
              turnstile.secretConfigured
                ? "Secret 已配置，接口不会显示原文"
                : "尚未配置 Secret"
            }}</small></label
          >
          <div class="switch-setting">
            <div>
              <strong>保护密码登录</strong><small>登录时要求通过人机验证</small>
            </div>
            <label class="switch"
              ><input
                v-model="turnstile.loginProtection"
                type="checkbox"
                :disabled="!turnstile.enabled" /><span
            /></label>
          </div>
          <div class="switch-setting">
            <div>
              <strong>保护用户注册</strong><small>注册时要求通过人机验证</small>
            </div>
            <label class="switch"
              ><input
                v-model="turnstile.registrationProtection"
                type="checkbox"
                :disabled="!turnstile.enabled" /><span
            /></label>
          </div>
          <button class="primary compact" :disabled="busy === 'turnstile'">
            <Save :size="16" />保存机器人保护
          </button>
        </form>
      </section>

      <section v-if="page === 'registration'" class="form-panel">
        <h2><Settings2 :size="19" />普通用户侧栏</h2>
        <p class="configuration-hint neutral">
          这里仅控制菜单是否显示，不等于取消用户权限；业务 API
          仍按各自规则校验。
        </p>
        <form @submit.prevent="saveSidebar">
          <div
            v-for="(label, key) in sidebarLabels"
            :key="key"
            class="switch-setting"
          >
            <div>
              <strong>显示“{{ label }}”</strong>
            </div>
            <label class="switch"
              ><input v-model="sidebar[key]" type="checkbox" /><span
            /></label>
          </div>
          <button class="primary compact" :disabled="busy === 'sidebar'">
            <Save :size="16" />保存侧栏设置
          </button>
        </form>
      </section>

      <section v-if="page === 'mail'" class="form-panel">
        <h2><MailCheck :size="19" />SMTP 邮箱</h2>
        <p :class="['configuration-status', smtpReady ? 'ready' : 'blocked']">
          <CheckCircle2 v-if="smtpReady" :size="16" /><CircleAlert
            v-else
            :size="16"
          />{{
            smtpReady
              ? "已保存，可用于注册邮箱验证"
              : "尚未就绪，邮箱验证码暂不可用"
          }}
        </p>
        <form @submit.prevent="saveMail">
          <div class="switch-setting">
            <div>
              <strong>启用 SMTP</strong
              ><small>开启后可用于邮箱验证与系统通知</small>
            </div>
            <label class="switch"
              ><input v-model="smtp.enabled" type="checkbox" /><span
            /></label>
          </div>
          <div class="field-row">
            <label
              >主机<input
                v-model="smtp.host"
                :required="smtp.enabled"
                placeholder="smtp.example.com" /></label
            ><label
              >端口<input
                v-model.number="smtp.port"
                type="number"
                min="1"
                max="65535"
                required
            /></label>
          </div>
          <label
            >连接安全<select v-model="smtp.tlsMode">
              <option value="starttls">STARTTLS</option>
              <option value="tls">TLS / SMTPS</option>
              <option value="none">无加密</option>
            </select></label
          >
          <label
            >用户名<input v-model="smtp.username" autocomplete="username"
          /></label>
          <label
            >密码<input
              v-model="smtp.password"
              type="password"
              autocomplete="new-password"
              :placeholder="
                smtp.passwordConfigured
                  ? '已加密保存，留空保持不变'
                  : '使用认证用户名时必填'
              "
            /><small v-if="smtp.passwordConfigured"
              >密码已加密保存，接口不会显示原文</small
            ></label
          >
          <div class="field-row">
            <label
              >发件邮箱<input
                v-model="smtp.fromEmail"
                type="email"
                :required="smtp.enabled" /></label
            ><label>发件名称<input v-model="smtp.fromName" /></label>
          </div>
          <button class="primary compact" :disabled="busy === 'mail'">
            <Save :size="16" />保存 SMTP
          </button>
        </form>
        <div class="mail-test">
          <label
            >测试收件邮箱<input
              v-model="testRecipient"
              type="email"
              placeholder="admin@example.com" /></label
          ><button
            class="secondary compact"
            :disabled="busy === 'test-mail' || !smtpReady"
            @click="testMail"
          >
            发送测试邮件
          </button>
        </div>
      </section>

      <section v-if="page === 'oauth'" class="form-panel">
        <h2><KeyRound :size="19" />GitHub OAuth</h2>
        <p
          v-if="!github.publicBaseUrlConfigured"
          class="configuration-status blocked"
        >
          <CircleAlert :size="16" />需先在部署环境配置 PUBLIC_BASE_URL
        </p>
        <form @submit.prevent="saveOAuth('github', github)">
          <div class="switch-setting">
            <div>
              <strong>启用 GitHub</strong
              ><small>需先配置有效的 PUBLIC_BASE_URL</small>
            </div>
            <label class="switch"
              ><input
                v-model="github.enabled"
                type="checkbox"
                :disabled="
                  !github.publicBaseUrlConfigured && !github.enabled
                " /><span
            /></label>
          </div>
          <label>Client ID<input v-model="github.clientId" /></label>
          <label
            >Client Secret<input
              v-model="github.clientSecret"
              type="password"
              placeholder="留空保持不变"
              :required="github.enabled && !github.secretConfigured"
          /></label>
          <p class="quiet">
            {{
              github.secretConfigured
                ? "密钥已加密保存，接口不会显示原文"
                : "尚未配置密钥"
            }}
          </p>
          <label>Scopes<input v-model="github.scopes" /></label>
          <label v-if="github.callbackUrl"
            >OAuth 回调地址
            <div class="copy-field">
              <input :value="github.callbackUrl" readonly /><button
                type="button"
                class="icon-action"
                title="复制回调地址"
                @click="copyCallback(github.callbackUrl)"
              >
                <Copy :size="17" />
              </button></div
          ></label>
          <button class="secondary compact" :disabled="busy === 'oauth-github'">
            保存 GitHub
          </button>
        </form>
      </section>

      <section v-if="page === 'oauth'" class="form-panel">
        <h2><KeyRound :size="19" />LinuxDo OAuth</h2>
        <p
          v-if="!linuxdo.publicBaseUrlConfigured"
          class="configuration-status blocked"
        >
          <CircleAlert :size="16" />需先在部署环境配置 PUBLIC_BASE_URL
        </p>
        <form @submit.prevent="saveOAuth('linuxdo', linuxdo)">
          <div class="switch-setting">
            <div>
              <strong>启用 LinuxDo</strong
              ><small>需先配置有效的 PUBLIC_BASE_URL</small>
            </div>
            <label class="switch"
              ><input
                v-model="linuxdo.enabled"
                type="checkbox"
                :disabled="
                  !linuxdo.publicBaseUrlConfigured && !linuxdo.enabled
                " /><span
            /></label>
          </div>
          <label>Client ID<input v-model="linuxdo.clientId" /></label>
          <label
            >Client Secret<input
              v-model="linuxdo.clientSecret"
              type="password"
              placeholder="留空保持不变"
              :required="linuxdo.enabled && !linuxdo.secretConfigured"
          /></label>
          <p class="quiet">
            {{
              linuxdo.secretConfigured
                ? "密钥已加密保存，接口不会显示原文"
                : "尚未配置密钥"
            }}
          </p>
          <label>Scopes<input v-model="linuxdo.scopes" /></label>
          <label
            >最低信任等级<select v-model.number="linuxdo.minimumTrustLevel">
              <option
                v-for="level in [0, 1, 2, 3, 4]"
                :key="level"
                :value="level"
              >
                等级 {{ level }}
              </option></select
            ><small>禁言、未激活或低于该等级的账户无法登录</small></label
          >
          <label v-if="linuxdo.callbackUrl"
            >OAuth 回调地址
            <div class="copy-field">
              <input :value="linuxdo.callbackUrl" readonly /><button
                type="button"
                class="icon-action"
                title="复制回调地址"
                @click="copyCallback(linuxdo.callbackUrl)"
              >
                <Copy :size="17" />
              </button></div
          ></label>
          <button
            class="secondary compact"
            :disabled="busy === 'oauth-linuxdo'"
          >
            保存 LinuxDo
          </button>
        </form>
      </section>
    </div>

    <section v-if="page === 'announcements'" class="admin-section">
      <div class="section-heading">
        <div>
          <p class="eyebrow">站内信息</p>
          <h2>公告管理</h2>
        </div>
        <span>{{ announcements.length }} 条记录</span>
      </div>
      <div class="admin-split">
        <form class="inline-form" @submit.prevent="publish">
          <h3>创建公告</h3>
          <label>标题<input v-model="notice.title" required /></label>
          <label
            >内容<textarea v-model="notice.content" rows="5" required />
          </label>
          <label
            >级别<select v-model="notice.severity">
              <option value="info">信息</option>
              <option value="warning">提醒</option>
              <option value="critical">重要</option>
            </select></label
          >
          <div class="switch-setting">
            <div><strong>立即发布</strong></div>
            <label class="switch"
              ><input v-model="notice.published" type="checkbox" /><span
            /></label>
          </div>
          <button
            class="primary compact"
            :disabled="busy === 'publish-announcement'"
          >
            <Megaphone :size="16" />创建公告
          </button>
        </form>
        <div class="data-list">
          <article
            v-for="item in announcements"
            :key="item.id"
            class="announcement-row"
          >
            <div>
              <span :class="['severity-dot', item.severity]"></span
              ><strong>{{ item.title }}</strong
              ><small>{{ item.content }}</small>
            </div>
            <span
              :class="['status-pill', item.published ? 'active' : 'suspended']"
              >{{ item.published ? "已发布" : "草稿" }}</span
            >
            <button
              class="icon-action"
              :title="item.published ? '下线公告' : '发布公告'"
              :disabled="busy === item.id"
              @click="toggleAnnouncement(item)"
            >
              <CirclePause v-if="item.published" :size="18" /><CirclePlay
                v-else
                :size="18"
              />
            </button>
          </article>
          <p v-if="!announcements.length" class="quiet empty-copy">
            还没有公告记录
          </p>
        </div>
      </div>
    </section>
  </main>
  <div
    v-if="editingUser"
    class="modal-backdrop user-editor-backdrop"
    @click.self="editingUser = null"
  >
    <aside class="user-editor">
      <header>
        <div>
          <p class="eyebrow">编辑用户</p>
          <h2>{{ editingUser.displayName }}</h2>
          <small>{{ editingUser.email }}</small>
        </div>
        <button class="icon-action" title="关闭" @click="editingUser = null">
          <X :size="18" />
        </button>
      </header>
      <form @submit.prevent="saveUserChanges">
        <section>
          <h3>基本信息</h3>
          <label
            >显示名称<input :value="editingUser.displayName" disabled /></label
          ><label>邮箱<input :value="editingUser.email" disabled /></label
          ><label
            >账号权限<select
              v-model="roleEdit"
              :disabled="editingUser.roles.includes('super_admin')"
            >
              <option value="user">普通用户</option>
              <option value="admin">管理员</option></select
            ><small v-if="editingUser.roles.includes('super_admin')"
              >超级管理员权限受保护，不能在此降级</small
            ><small v-else
              >保存后立即生效；管理员会在普通用户控制台基础上获得管理能力</small
            ></label
          >
        </section>
        <section v-if="isSuperAdmin">
          <h3>余额与调账</h3>
          <label
            >目标余额（元）<input
              v-model.number="walletEdit.targetBalanceYuan"
              type="number"
              min="0"
              step="0.01"
              required
            /><small
              >当前 ¥{{
                (editingUser.balanceCents / 100).toFixed(2)
              }}，余额有变化时自动写入差额账本</small
            ></label
          ><label
            >管理员备注<textarea
              v-model="walletEdit.note"
              rows="3"
              maxlength="500"
              placeholder="余额调整原因（仅调整权限时可留空）"
            />
          </label>
        </section>
        <footer>
          <button type="button" class="secondary" @click="editingUser = null">
            取消</button
          ><button class="primary" :disabled="busy === 'user-save'">
            <Save :size="17" />保存更改
          </button>
        </footer>
      </form>
    </aside>
  </div>
</template>
