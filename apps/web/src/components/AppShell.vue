<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import {
  AppWindow,
  ArchiveRestore,
  BadgeCent,
  BadgeDollarSign,
  CreditCard,
  CalendarCheck,
  FileClock,
  Gauge,
  KeyRound,
  LayoutDashboard,
  PanelsTopLeft,
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

onMounted(async () => {
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
    const billingResponse = await fetch("/api/billing/summary", {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (billingResponse.ok) {
      const summary = await billingResponse.json();
      balanceCents.value = summary.balanceCents || 0;
    }
    const visibilityResponse = await fetch("/api/sidebar-visibility", {
      headers: { Authorization: "Bearer " + token },
    });
    if (visibilityResponse.ok)
      visibility.value = (await visibilityResponse.json()).visibility || {};
  } catch {
    /* The page surfaces session errors itself. */
  }
});

const balanceText = computed(() => {
  return `余额 ¥${(balanceCents.value / 100).toFixed(2)}`;
});
const userInitial = computed(() =>
  user.value?.Email ? user.value.Email[0].toUpperCase() : "?",
);
const pageTitle = computed(() => {
  const titles: Record<string, string> = {
    "/console": "概览",
    "/console/deploy": "部署应用",
    "/console/apps": "我的应用",
    "/console/releases": "版本历史",
    "/console/backups": "备份与恢复",
    "/console/billing": "余额与账单",
    "/console/recharge": "账户充值",
    "/console/checkin": "每日签到",
    "/console/usage": "用量明细",
    "/console/tickets": "工单支持",
    "/console/faq": "常见问答",
  };
  if (route.path.startsWith("/admin/products")) return "产品管理";
  if (route.path.startsWith("/admin/users")) return "用户管理";
  if (route.path.startsWith("/admin/tickets")) return "工单管理";
  if (route.path.startsWith("/admin")) return "管理控制台";
  return titles[route.path] || "CloudMeter";
});

function isAnchorActive(path: string, hash: string): boolean {
  return route.path === path && route.hash === hash;
}
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
          v-if="shown('checkin')"
          to="/console/checkin"
          active-class="active"
          ><CalendarCheck :size="17" />每日签到</RouterLink
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
            to="/admin/checkin-settings"
            active-class="active"
            ><CalendarCheck :size="17" />签到设置</RouterLink
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
          <RouterLink to="/admin/announcements" active-class="active"
            ><Settings2 :size="17" />公告管理</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/homepage"
            active-class="active"
            ><PanelsTopLeft :size="17" />首页设置</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/registration"
            active-class="active"
            ><Settings2 :size="17" />注册策略</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/oauth"
            active-class="active"
            ><KeyRound :size="17" />OAuth 认证</RouterLink
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
            v-if="isAdmin"
            to="/admin/docker"
            active-class="active"
            class="infrastructure-nav-link"
            ><Container :size="17" />Docker 与镜像源</RouterLink
          >
          <RouterLink v-if="isAdmin" to="/admin/metrics" active-class="active"
            ><Gauge :size="17" />性能监控</RouterLink
          >
        </template>
      </nav>

      <!-- 底部用户信息卡片 (NextDevTpl Profile Popover) -->
      <div v-if="user" class="sidebar-user-card">
        <div class="sidebar-user-avatar">{{ userInitial }}</div>
        <div class="sidebar-user-meta">
          <span class="user-email-text">{{ user.Email }}</span>
          <span class="user-role-badge">{{ isAdmin ? (isSuperAdmin ? '超级管理员' : '管理员') : '用户' }}</span>
        </div>
        <button class="logout-mini-btn" title="退出登录" @click="logout">
          <LogOut :size="15" />
        </button>
      </div>
    </aside>

    <main class="app-main">
      <header class="console-header">
        <div class="console-header-breadcrumbs">
          <span class="crumb-parent">{{ route.path.startsWith('/admin') ? '管理后台' : '控制台' }}</span>
          <span class="crumb-sep">/</span>
          <span class="crumb-current">{{ pageTitle }}</span>
        </div>
        <div class="console-header-actions">
          <button
            class="theme-toggle"
            :title="theme === 'dark' ? '切换浅色主题' : '切换深色主题'"
            @click="toggleTheme"
          >
            <Sun v-if="theme === 'dark'" :size="16" />
            <Moon v-else :size="16" />
          </button>
          <span class="header-balance mono-data">
            <span class="balance-dot"></span>
            {{ balanceText }}
          </span>
          <div v-if="user" class="header-avatar" :title="user.Email">
            {{ userInitial }}
          </div>
        </div>
      </header>

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
