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
  Users,
  Container,
  MessageSquareText,
  CircleHelp,
} from "@lucide/vue";
import { logout } from "../api";
import BrandMark from "./BrandMark.vue";

const route = useRoute();
const roles = ref<string[]>([]);
const user = ref<{ Email?: string; DisplayName?: string } | null>(null);
const balanceCents = ref(0);
const visibility=ref<Record<string,boolean>>({})
const shown=(key:string)=>isAdmin.value||visibility.value[key]!==false
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
    const visibilityResponse=await fetch('/api/sidebar-visibility',{headers:{Authorization:'Bearer '+token}})
    if(visibilityResponse.ok)visibility.value=(await visibilityResponse.json()).visibility||{}
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

function isAnchorActive(path: string, hash: string): boolean {
  return route.path === path && route.hash === hash;
}
</script>

<template>
  <div class="app-shell">
    <aside>
      <BrandMark />
      <nav>
        <div class="nav-group">用户控制台</div>
        <RouterLink v-if="shown('overview')" to="/console" exact-active-class="active"
          ><Gauge :size="18" />概览</RouterLink
        >
        <RouterLink v-if="shown('deploy')" to="/console/deploy" active-class="active"
          ><Package :size="18" />部署应用</RouterLink
        >
        <RouterLink v-if="shown('apps')" to="/console/apps" active-class="active"
          ><AppWindow :size="18" />我的应用</RouterLink
        >
        <RouterLink v-if="shown('releases')" to="/console/releases" active-class="active"
          ><ArchiveRestore :size="18" />版本历史</RouterLink
        >
        <RouterLink v-if="shown('backups')" to="/console/backups" active-class="active"
          ><Package :size="18" />备份与恢复</RouterLink
        >
        <RouterLink v-if="shown('billing')" to="/console/billing" active-class="active"
          ><CreditCard :size="18" />余额与账单</RouterLink
        >
        <RouterLink v-if="shown('recharge')" to="/console/recharge" active-class="active"
          ><BadgeDollarSign :size="18" />账户充值</RouterLink
        >
        <RouterLink v-if="shown('checkin')" to="/console/checkin" active-class="active"
          ><CalendarCheck :size="18" />每日签到</RouterLink
        >
        <RouterLink v-if="shown('usage')" to="/console/usage" active-class="active"
          ><Receipt :size="18" />用量明细</RouterLink
        >
        <RouterLink v-if="shown('tickets')" to="/console/tickets" active-class="active"
          ><MessageSquareText :size="18" />工单支持</RouterLink
        >
        <RouterLink v-if="shown('faq')" to="/console/faq" active-class="active"><CircleHelp :size="18" />常见问答</RouterLink>
        <template v-if="isAdmin">
          <div class="nav-group">管理控制台</div>
          <RouterLink to="/admin" exact-active-class="active"
            ><LayoutDashboard :size="18" />平台总览</RouterLink
          >
          <RouterLink to="/admin/users" active-class="active"
            ><Users :size="18" />用户管理</RouterLink
          >
          <RouterLink to="/admin/products" active-class="active"
            ><AppWindow :size="18" />产品管理</RouterLink
          >
          <RouterLink to="/admin/tickets" active-class="active"
            ><MessageSquareText :size="18" />工单管理</RouterLink
          >
          <RouterLink to="/admin/faq" active-class="active"><CircleHelp :size="18" />问答管理</RouterLink>
          <div v-if="isSuperAdmin" class="nav-group">资金与运营</div>
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/pricing"
            active-class="active"
            ><BadgeCent :size="18" />定价中心</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/payments"
            active-class="active"
            ><CreditCard :size="18" />支付订单</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/payment-settings"
            active-class="active"
            ><KeyRound :size="18" />支付设置</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/checkin-settings"
            active-class="active"
            ><CalendarCheck :size="18" />签到设置</RouterLink
          >
          <div class="nav-group">平台设置</div>
          <RouterLink to="/admin/announcements" active-class="active"
            ><Settings2 :size="18" />公告管理</RouterLink
          >
          <RouterLink v-if="isSuperAdmin" to="/admin/homepage" active-class="active"><PanelsTopLeft :size="18" />首页设置</RouterLink>
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/registration"
            active-class="active"
            ><Settings2 :size="18" />注册策略</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/oauth"
            active-class="active"
            ><KeyRound :size="18" />OAuth 认证</RouterLink
          >
          <RouterLink v-if="isSuperAdmin" to="/admin/mail" active-class="active"
            ><Settings2 :size="18" />SMTP 邮件</RouterLink
          >
          <RouterLink
            v-if="isSuperAdmin"
            to="/admin/audit"
            active-class="active"
            ><FileClock :size="18" />审计日志</RouterLink
          >
          <div v-if="isAdmin" class="nav-group infrastructure-nav-group">容器基础设施</div>
          <RouterLink
            v-if="isAdmin"
            to="/admin/docker"
            active-class="active"
            class="infrastructure-nav-link"
            ><Container :size="18" />Docker 与镜像源</RouterLink
          >
          <RouterLink v-if="isAdmin" to="/admin/metrics" active-class="active"><Gauge :size="18" />性能监控</RouterLink>
        </template>
      </nav>
      <div class="sidebar-user" v-if="user">
        <div class="sidebar-user-avatar">{{ userInitial }}</div>
        <div class="sidebar-user-info">
          <strong>{{ user.Email }}</strong>
          <span>{{ balanceText }}</span>
        </div>
      </div>
      <button class="icon-text" @click="logout">
        <LogOut :size="17" />退出
      </button>
    </aside>
    <main class="app-main">
      <RouterView v-slot="{ Component, route }">
        <Transition name="workspace-slide">
          <component :is="Component" :key="route.fullPath" />
        </Transition>
      </RouterView>
    </main>
  </div>
</template>
