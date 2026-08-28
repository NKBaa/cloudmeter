import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import AppShell from './components/AppShell.vue'
import SetupView from './views/SetupView.vue'
import LoginView from './views/LoginView.vue'
import RegisterView from './views/RegisterView.vue'
import ConsoleView from './views/ConsoleView.vue'
import AdminView from './views/AdminView.vue'
import OAuthCallbackView from './views/OAuthCallbackView.vue'
import ProductsAdminView from './views/ProductsAdminView.vue'
import PaymentsAdminView from './views/PaymentsAdminView.vue'
import PricingAdminView from './views/PricingAdminView.vue'
import PaymentSettingsView from './views/PaymentSettingsView.vue'
import ReleasesView from './views/ReleasesView.vue'
import BackupsView from './views/BackupsView.vue'
import AuditAdminView from './views/AuditAdminView.vue'
import CheckinSettingsView from './views/CheckinSettingsView.vue'
import HomeView from './views/HomeView.vue'
import HomeSettingsView from './views/HomeSettingsView.vue'
import SystemSettingsView from './views/SystemSettingsView.vue'
import DockerSettingsView from './views/DockerSettingsView.vue'
import TicketsView from './views/TicketsView.vue'
import AppDetailView from './views/AppDetailView.vue'
import FAQView from './views/FAQView.vue'
import HostMetricsView from './views/HostMetricsView.vue'
import BalanceAlertView from './views/BalanceAlertView.vue'
import QuotaSettingsView from './views/QuotaSettingsView.vue'
import LogSettingsView from './views/LogSettingsView.vue'
import DocsView from './views/DocsView.vue'
import { initTheme } from './theme'
import './styles.css'

initTheme()

const router = createRouter({
  history: createWebHistory(),
  scrollBehavior(to) {
    if (to.hash) {
      return new Promise((resolve) => {
        setTimeout(() => {
          const el = document.querySelector(to.hash)
          resolve(el ? { el: to.hash, top: 18, behavior: 'smooth' } : {})
        }, 60)
      })
    }
    return { top: 0 }
  },
  routes: [
    { path: '/setup', component: SetupView },
    { path: '/login', component: LoginView },
    { path: '/register', component: RegisterView },
    { path: '/oauth/callback', component: OAuthCallbackView },
    { path: '/', component: HomeView },
    { path: '/docs', component: DocsView },
    {
      path: '/',
      component: AppShell,
      children: [
        { path: 'console', component: ConsoleView, props: { page: 'overview' } },
        { path: 'console/home', component: HomeView },
        { path: 'console/docs', component: DocsView },
        { path: 'console/deploy', component: ConsoleView, props: { page: 'deploy' } },
        { path: 'console/apps', component: ConsoleView, props: { page: 'apps' } },
        { path: 'console/apps/:instanceId', component: AppDetailView, meta: { appDetail: true } },
        { path: 'console/billing', component: ConsoleView, props: { page: 'billing' } },
        { path: 'console/recharge', component: ConsoleView, props: { page: 'recharge' } },
        { path: 'console/checkin', component: ConsoleView, props: { page: 'checkin' } },
        { path: 'console/usage', component: ConsoleView, props: { page: 'usage' } },
        { path: 'console/releases', component: ReleasesView },
        { path: 'console/backups', component: BackupsView },
        { path: 'console/tickets', component: TicketsView, props: { admin: false } },
        { path: 'console/faq', component: FAQView, props: { admin: false } },
        { path: 'console/balance-alert', component: BalanceAlertView },
        { path: 'admin', component: AdminView, props: { page: 'overview' }, meta: { admin: true } },
        { path: 'admin/users', component: AdminView, props: { page: 'users' }, meta: { admin: true } },
        { path: 'admin/announcements', component: AdminView, props: { page: 'announcements' }, meta: { admin: true } },
        { path: 'admin/registration', component: AdminView, props: { page: 'registration' }, meta: { superAdmin: true } },
        { path: 'admin/mail', component: AdminView, props: { page: 'mail' }, meta: { superAdmin: true } },
        { path: 'admin/oauth', component: AdminView, props: { page: 'oauth' }, meta: { superAdmin: true } },
        { path: 'admin/products', component: ProductsAdminView, meta: { admin: true } },
        { path: 'admin/payments', component: PaymentsAdminView, meta: { superAdmin: true } },
        { path: 'admin/pricing', component: PricingAdminView, meta: { superAdmin: true } },
        { path: 'admin/payment-settings', component: PaymentSettingsView, meta: { superAdmin: true } },
        { path: 'admin/checkin-settings', component: CheckinSettingsView, meta: { superAdmin: true } },
        { path: 'admin/quota-settings', component: QuotaSettingsView, meta: { superAdmin: true } },
        { path: 'admin/log-settings', component: LogSettingsView, meta: { superAdmin: true } },
        { path: 'admin/audit', component: AuditAdminView, meta: { superAdmin: true } },
        { path: 'admin/system', component: SystemSettingsView, meta: { superAdmin: true } },
        { path: 'admin/homepage', component: HomeSettingsView, meta: { superAdmin: true } },
        { path: 'admin/tickets', component: TicketsView, props: { admin: true }, meta: { admin: true } },
        { path: 'admin/docker', component: DockerSettingsView, meta: { admin: true } },
        { path: 'admin/metrics', component: HostMetricsView, meta: { admin: true } },
        { path: 'admin/faq', component: FAQView, props: { admin: true }, meta: { admin: true } },
      ],
    },
  ],
})

router.beforeEach(async (to) => {
  try {
    const response = await fetch('/api/setup/status')
    const status = await response.json() as { initialized?: boolean; hasAdmin?: boolean }
    if (!status.initialized && to.path !== '/setup') return '/setup'
    if (status.initialized && status.hasAdmin && to.path === '/setup') return '/login'
  } catch { /* Let the page surface service availability errors. */ }
  const token = localStorage.getItem('session_token')
  if (token && (to.path === '/login' || to.path === '/register')) {
    return '/console'
  }
  if (!['/', '/docs', '/setup', '/login', '/register', '/oauth/callback'].includes(to.path) && !token) return '/login'
  if (to.meta.admin || to.meta.superAdmin) {
    try {
      const token = localStorage.getItem('session_token')
      const response = await fetch('/api/me', { headers: { Authorization: `Bearer ${token}` } })
      if (!response.ok) throw new Error('session unavailable')
      const current = await response.json() as { Roles?: string[] }
      const roles = current.Roles || []
      if (to.meta.superAdmin && !roles.includes('super_admin')) return '/admin'
      if (!roles.some(role => role === 'admin' || role === 'super_admin')) return '/console'
    } catch {
      localStorage.removeItem('admin_session_token')
      localStorage.removeItem('session_token')
      return '/login'
    }
  }
})

createApp(App).use(router).mount('#app')
