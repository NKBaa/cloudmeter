<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import {
  AppWindow,
  ArchiveRestore,
  BadgeCent,
  BadgeDollarSign,
  CreditCard,
  FileClock,
  Gauge,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Package,
  Receipt,
  Settings2,
  SlidersHorizontal,
  Users,
  Container,
  MessageSquareText,
  CircleHelp,
  BellRing,
  Gift,
  RefreshCw,
  Moon,
  Sun,
  Bot,
  Globe2,
} from "@lucide/vue";
import { logout } from "../api";
import { toggleTheme, theme } from "../theme";
import BrandMark from "./BrandMark.vue";

const route = useRoute();
const showShellTopbar = computed(
  () => route.path === "/console/home" || route.path === "/console/docs",
);
const roles = ref<string[]>([]);
const user = ref<{ Email?: string; DisplayName?: string } | null>(null);
const balanceCents = ref(0);
const visibility = ref<Record<string, boolean>>({});
const shown = (key: string) => isAdmin.value || visibility.value[key] !== false;
const isAdmin = computed(() =>
  roles.value.some((role) => role === "admin" || role === "super_admin"),
);
const isSuperAdmin = computed(() => roles.value.includes("super_admin"));

async function refreshShellBalance() {
  const token = localStorage.getItem("session_token");
  if (!token) return;
  const response = await fetch("/api/billing/summary", {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (response.ok) {
    const summary = await response.json();
    balanceCents.value = Number(summary.balanceCents) || 0;
  }
}

function handleBalanceChanged(event: Event) {
  const next = Number((event as CustomEvent<{ balanceCents?: number }>).detail?.balanceCents);
  if (Number.isFinite(next)) balanceCents.value = next;
}

onMounted(async () => {
  window.addEventListener("focus", refreshShellBalance);
  window.addEventListener("cloudmeter:balance-changed", handleBalanceChanged);
  try {
    const token = localStorage.getItem("session_token");
    const response = await fetch("/api/me", {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (response.ok) {
      const me = await response.json();
      roles.value = me.Roles || [];
      user.value = me;
    }
    await refreshShellBalance();
    const visibilityResponse = await fetch("/api/sidebar-visibility", {
      headers: { Authorization: "Bearer " + token },
    });
    if (visibilityResponse.ok)
      visibility.value = (await visibilityResponse.json()).visibility || {};
  } catch {
    /* The page surfaces session errors itself. */
  }
});

onBeforeUnmount(() => {
  window.removeEventListener("focus", refreshShellBalance);
  window.removeEventListener("cloudmeter:balance-changed", handleBalanceChanged);
});

const balanceText = computed(() => {
  return `余额 ¥${(balanceCents.value / 100).toFixed(2)}`;
});
const userDisplayName = computed(
  () => user.value?.DisplayName?.trim() || user.value?.Email || "账户",
);
const userInitial = computed(() =>
  userDisplayName.value ? Array.from(userDisplayName.value)[0].toUpperCase() : "?",
);
</script>

<template>
  <div class="app-shell">
    <aside>
      <div class="sidebar-header">
        <BrandMark />
      </div>
      <nav>
        <div class="nav-group">控制台</div>
        <RouterLink
          v-if="shown('overview')"
          to="/console"
          exact-active-class="active"
          ><Gauge :size="17" />概览看板</RouterLink
        >
        <RouterLink
          v-if="shown('deploy')"
          to="/console/deploy"
          active-class="active"
          ><Package :size="17" />部署应用</RouterLink
        >
        <RouterLink
          v-if="shown('apps')"
          to="/console/apps"
          active-class="active"
          ><AppWindow :size="17" />我的应用</RouterLink
        >
        <RouterLink
          v-if="shown('releases')"
          to="/console/releases"
          active-class="active"
          ><ArchiveRestore :size="17" />版本历史</RouterLink
        >
        <RouterLink
          v-if="shown('backups')"
          to="/console/backups"
          active-class="active"
          ><Package :size="17" />备份与恢复</RouterLink
        >
        <RouterLink
          v-if="shown('billing')"
          to="/console/billing"
          active-class="active"
          ><CreditCard :size="17" />余额与账单</RouterLink
        >
        <RouterLink
          v-if="shown('recharge')"
          to="/console/recharge"
          active-class="active"
          ><BadgeDollarSign :size="17" />账户充值</RouterLink
        >
        <RouterLink
          v-if="shown('usage')"
          to="/console/usage"
          active-class="active"
          ><Receipt :size="17" />用量明细</RouterLink
        >
        <RouterLink
          v-if="shown('tickets')"
          to="/console/tickets"
          active-class="active"
          ><MessageSquareText :size="17" />工单支持</RouterLink
        >
        <RouterLink
          v-if="shown('faq')"
          to="/console/faq"
          active-class="active"
          ><CircleHelp :size="17" />常见问答</RouterLink
        >
        <RouterLink to="/console/balance-alert" active-class="active"
          ><BellRing :size="17" />余额提醒</RouterLink
        >

        <template v-if="isAdmin">
          <div class="nav-group">管理控制台</div>
          <RouterLink to="/admin" exact-active-class="active"
            ><LayoutDashboard :size="17" />平台总览</RouterLink
          >
          <RouterLink to="/admin/users" active-class="active"
            ><Users :size="17" />用户管理</RouterLink
          >
          <RouterLink to="/admin/products" active-class="active"
            ><AppWindow :size="17" />产品管理</RouterLink
          >
          <RouterLink to="/admin/tickets" active-class="active"
            ><MessageSquareText :size="17" />工单管理</RouterLink
          >
          <RouterLink to="/admin/faq" active-class="active"
            ><CircleHelp :size="17" />问答管理</RouterLink
          >

          <div v-if="isSuperAdmin" class="nav-group">资金与运营</div>
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/pricing"
            active-class="active"
            ><BadgeCent :size="17" />定价中心</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/payments"
            active-class="active"
            ><CreditCard :size="17" />支付订单</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/payment-settings"
            active-class="active"
            ><KeyRound :size="17" />支付设置</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/quota-settings"
            active-class="active"
            ><Gift :size="17" />额度设置</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/log-settings"
            active-class="active"
            ><RefreshCw :size="17" />日志设置</RouterLink
          >

          <div class="nav-group">平台设置</div>
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/system"
            active-class="active"
            ><SlidersHorizontal :size="17" />系统设置</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/ai-support"
            active-class="active"
            ><Bot :size="17" />AI 助手设置</RouterLink
          >
          <RouterLink to="/admin/announcements" active-class="active"
            ><Settings2 :size="17" />公告管理</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/registration"
            active-class="active"
            ><Settings2 :size="17" />注册与认证</RouterLink
          >
          <RouterLink v-if="isSuperAdmin" to="/admin/mail" active-class="active"
            ><Settings2 :size="17" />SMTP 邮件</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/audit"
            active-class="active"
            ><FileClock :size="17" />审计日志</RouterLink
          >

          <div v-if="isAdmin" class="nav-group infrastructure-nav-group">
            基础设施
          </div>
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/website"
            active-class="infrastructure-current"
            class="infrastructure-nav-link"
            ><Globe2 :size="17" />网站设置</RouterLink
          >
          <RouterLink
            v-if="isAdmin"
            to="/admin/docker"
            active-class="infrastructure-current"
            class="infrastructure-nav-link"
            ><Container :size="17" />Docker 与镜像源</RouterLink
          >
          <RouterLink
            v-if="isAdmin"
            to="/admin/metrics"
            active-class="infrastructure-current"
            class="infrastructure-nav-link"
            ><Gauge :size="17" />性能监控</RouterLink
          >
        </template>
      </nav>

      <div class="sidebar-footer">
        <div class="sidebar-utilities">
          <RouterLink
            class="sidebar-balance"
            to="/console/billing"
            title="查看余额与账单"
          >
            <span class="balance-dot"></span>
            <span>{{ balanceText }}</span>
          </RouterLink>
          <button
            class="sidebar-theme-button"
            :title="theme === 'dark' ? '切换浅色主题' : '切换深色主题'"
            @click="toggleTheme"
          >
            <Sun v-if="theme === 'dark'" :size="15" />
            <Moon v-else :size="15" />
          </button>
        </div>

        <div v-if="user" class="sidebar-user-card">
          <div class="sidebar-user-avatar">{{ userInitial }}</div>
          <div class="sidebar-user-meta">
            <span class="user-email-text" :title="user.Email">{{ userDisplayName }}</span>
            <span class="user-role-badge">{{ isAdmin ? (isSuperAdmin ? '超级管理员' : '管理员') : '用户' }}</span>
          </div>
          <button class="logout-mini-btn" title="退出登录" @click="logout">
            <LogOut :size="15" />
          </button>
        </div>
      </div>
    </aside>

    <main class="app-main">
      <header v-if="showShellTopbar" class="shell-topbar">
        <nav>
          <RouterLink to="/console/home" active-class="active">主页</RouterLink>
          <RouterLink to="/console/docs" active-class="active">文档</RouterLink>
          <RouterLink to="/console" exact-active-class="active">控制台</RouterLink>
        </nav>
      </header>

      <RouterView v-slot="{ Component, route }">
        <Transition name="workspace-slide">
          <component :is="Component" :key="route.fullPath" />
        </Transition>
      </RouterView>
    </main>
  </div>
</template>
