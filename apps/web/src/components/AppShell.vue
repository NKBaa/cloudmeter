<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  AppWindow,
  ArchiveRestore,
  BadgeCent,
  BadgeDollarSign,
  CreditCard,
  FileClock,
  Gauge,
  KeyRound,
  Layers,
  LayoutDashboard,
  LogOut,
  Package,
  Receipt,
  Settings2,
  Users,
} from '@lucide/vue'
import { logout } from '../api'
import BrandMark from './BrandMark.vue'

const route = useRoute()
const roles = ref<string[]>([])
const isAdmin = computed(() => roles.value.some((role) => role === 'admin' || role === 'super_admin'))
const isSuperAdmin = computed(() => roles.value.includes('super_admin'))

onMounted(async () => {
  try {
    const token = localStorage.getItem('session_token')
    const response = await fetch('/api/me', { headers: { Authorization: `Bearer ${token}` } })
    if (response.ok) {
      const me = await response.json()
      roles.value = me.Roles || []
    }
  } catch { /* The page surfaces session errors itself. */ }
})

function isAnchorActive(path: string, hash: string): boolean {
  return route.path === path && route.hash === hash
}
</script>

<template>
  <div class="app-shell">
    <aside>
      <BrandMark />
      <nav>
        <div class="nav-group">用户控制台</div>
        <RouterLink to="/console" exact-active-class="active"><Gauge :size="18" />概览</RouterLink>
        <RouterLink to="/console#apps" :class="{ active: isAnchorActive('/console', '#apps') }"><AppWindow :size="18" />我的应用</RouterLink>
        <RouterLink to="/console/releases" active-class="active"><ArchiveRestore :size="18" />版本历史</RouterLink>
        <RouterLink to="/console/backups" active-class="active"><Package :size="18" />备份与恢复</RouterLink>
        <RouterLink to="/console#billing" :class="{ active: isAnchorActive('/console', '#billing') }"><CreditCard :size="18" />余额与账单</RouterLink>
        <RouterLink to="/console#usage" :class="{ active: isAnchorActive('/console', '#usage') }"><Receipt :size="18" />用量明细</RouterLink>
        <RouterLink to="/console#subscription" :class="{ active: isAnchorActive('/console', '#subscription') }"><BadgeDollarSign :size="18" />套餐订阅</RouterLink>
        <template v-if="isAdmin">
          <div class="nav-group">管理控制台</div>
          <RouterLink to="/admin" exact-active-class="active"><LayoutDashboard :size="18" />平台总览</RouterLink>
          <RouterLink to="/admin#accounts" :class="{ active: isAnchorActive('/admin', '#accounts') }"><Users :size="18" />用户管理</RouterLink>
          <RouterLink to="/admin/products" active-class="active"><AppWindow :size="18" />产品管理</RouterLink>
          <RouterLink v-if="isSuperAdmin" to="/admin/pricing" active-class="active"><BadgeCent :size="18" />定价中心</RouterLink>
          <RouterLink v-if="isSuperAdmin" to="/admin/plans" active-class="active"><Layers :size="18" />套餐计划</RouterLink>
          <RouterLink v-if="isSuperAdmin" to="/admin/payments" active-class="active"><CreditCard :size="18" />支付订单</RouterLink>
          <RouterLink v-if="isSuperAdmin" to="/admin/payment-settings" active-class="active"><KeyRound :size="18" />支付设置</RouterLink>
          <RouterLink to="/admin#announcements" :class="{ active: isAnchorActive('/admin', '#announcements') }"><Settings2 :size="18" />公告管理</RouterLink>
          <RouterLink v-if="isSuperAdmin" to="/admin#oauth" :class="{ active: isAnchorActive('/admin', '#oauth') }"><KeyRound :size="18" />OAuth 认证</RouterLink>
          <RouterLink v-if="isSuperAdmin" to="/admin#mail" :class="{ active: isAnchorActive('/admin', '#mail') }"><Settings2 :size="18" />SMTP 邮件</RouterLink>
          <RouterLink v-if="isSuperAdmin" to="/admin/audit" active-class="active"><FileClock :size="18" />审计日志</RouterLink>
        </template>
      </nav>
      <button class="icon-text" @click="logout"><LogOut :size="17" />退出</button>
    </aside>
    <RouterView />
  </div>
</template>
