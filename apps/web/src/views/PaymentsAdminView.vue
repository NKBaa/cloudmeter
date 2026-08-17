<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ArrowLeft,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Clock3,
  LogOut,
  ReceiptText,
  RefreshCw,
  Search,
  Undo2,
  X,
  XCircle,
} from '@lucide/vue'
import { api, logout } from '../api'
import BrandMark from '../components/BrandMark.vue'

type RefundSummary = {
  id: string
  status: string
  completedAt?: string | null
}

type Order = {
  id: string
  email: string
  displayName: string
  amountCents: number
  provider: string
  status: string
  createdAt: string
  paidAt?: string | null
  refund?: RefundSummary
}

type RefundEvent = {
  id: number
  fromStatus?: string | null
  toStatus: string
  message: string
  createdAt: string
}

type Refund = {
  id: string
  orderId: string
  userId: string
  email: string
  displayName: string
  provider: string
  amountCents: number
  status: string
  reason: string
  ledgerEntryId?: number | null
  requestedByEmail?: string
  requestId: string
  failureMessage?: string
  createdAt: string
  completedAt?: string | null
  events: RefundEvent[]
}

type RefundResponse = {
  refundId: string
  refundStatus: string
  completedAt?: string | null
}

const orders = ref<Order[]>([])
const refunds = ref<Refund[]>([])
const error = ref('')
const message = ref('')
const busy = ref('')
const refundTarget = ref<Order | null>(null)
const refundReason = ref('管理员全额退款')
const expandedRefunds = ref<string[]>([])

async function load() {
  try {
    const [orderResult, refundResult] = await Promise.all([
      api<{ orders: Order[] }>('/admin/payments/orders'),
      api<{ refunds: Refund[] }>('/admin/payments/refunds'),
    ])
    orders.value = orderResult.orders
    refunds.value = refundResult.refunds
    error.value = ''
  } catch (cause) {
    error.value = (cause as Error).message
  }
}

onMounted(load)

async function markPaid(order: Order) {
  try {
    busy.value = order.id
    await api('/admin/payments/orders/' + order.id + '/mark-paid', { method: 'POST' })
    order.status = 'paid'
    message.value = '订单已入账'
    error.value = ''
  } catch (cause) {
    error.value = (cause as Error).message
  } finally {
    busy.value = ''
  }
}

function openRefund(order: Order) {
  refundTarget.value = order
  refundReason.value = '管理员全额退款'
}

function cancelRefund() {
  if (!busy.value) refundTarget.value = null
}

async function submitRefund() {
  const order = refundTarget.value
  if (!order) return
  try {
    busy.value = order.id
    const result = await api<RefundResponse>('/admin/payments/orders/' + order.id + '/refund', {
      method: 'POST',
      body: JSON.stringify({ reason: refundReason.value.trim() }),
    })
    order.status = 'refunded'
    order.refund = { id: result.refundId, status: result.refundStatus, completedAt: result.completedAt }
    refundTarget.value = null
    message.value = '全额退款已完成并写入不可变记录'
    error.value = ''
    await load()
  } catch (cause) {
    error.value = (cause as Error).message
  } finally {
    busy.value = ''
  }
}

async function query(order: Order) {
  try {
    busy.value = order.id
    const result = await api<{ providerStatus: string; message: string }>('/admin/payments/orders/' + order.id + '/query', { method: 'POST' })
    message.value = '提供商状态：' + result.providerStatus + ' · ' + result.message
    error.value = ''
  } catch (cause) {
    error.value = (cause as Error).message
  } finally {
    busy.value = ''
  }
}

async function close(order: Order) {
  if (!confirm('确认关闭此待处理订单？')) return
  try {
    busy.value = order.id
    await api('/admin/payments/orders/' + order.id + '/close', { method: 'POST' })
    order.status = 'closed'
    message.value = '订单已关闭'
    error.value = ''
  } catch (cause) {
    error.value = (cause as Error).message
  } finally {
    busy.value = ''
  }
}

function toggleRefund(id: string) {
  expandedRefunds.value = expandedRefunds.value.includes(id)
    ? expandedRefunds.value.filter((value) => value !== id)
    : [...expandedRefunds.value, id]
}

function money(cents: number) {
  return (cents / 100).toFixed(2)
}

function date(value?: string | null) {
  return value ? new Date(value).toLocaleString() : '处理中'
}

function statusLabel(status: string) {
  return { paid: '已入账', refunded: '已退款', refunding: '退款中', closed: '已关闭', pending: '待核验' }[status] || status
}

function statusClass(status: string) {
  if (status === 'paid' || status === 'succeeded') return 'active'
  if (status === 'pending' || status === 'refunding' || status === 'processing') return 'pending'
  if (status === 'failed') return 'danger'
  return 'suspended'
}

function refundStatusLabel(status: string) {
  return { processing: '处理中', succeeded: '退款成功', failed: '退款失败' }[status] || status
}
</script>

<template>
  <div class="app-shell">
    <aside>
      <BrandMark />
      <nav>
        <a href="/admin"><ArrowLeft :size="18" />平台设置</a>
        <a class="active"><ReceiptText :size="18" />资金流水</a>
      </nav>
      <button class="icon-text" @click="logout"><LogOut :size="17" />退出</button>
    </aside>

    <main class="workspace admin-workspace">
      <header>
        <div>
          <p class="eyebrow">资金运营</p>
          <h1>充值与退款</h1>
        </div>
        <button class="secondary compact" @click="load"><RefreshCw :size="16" />刷新</button>
      </header>
      <p v-if="error" class="message">{{ error }}</p>
      <p v-if="message" class="status-ok">{{ message }}</p>

      <section class="payment-list">
        <div class="section-heading">
          <div>
            <p class="eyebrow">支付流水</p>
            <h2>充值订单</h2>
          </div>
          <span>{{ orders.filter((value) => value.status === 'pending').length }} 待处理</span>
        </div>
        <article v-for="order in orders" :key="order.id" class="payment-row">
          <div class="payment-copy">
            <strong>{{ order.displayName }} · {{ order.email }}</strong>
            <small>{{ order.provider === 'manual' ? '人工充值' : 'EPay' }} · {{ date(order.createdAt) }}</small>
            <small v-if="order.refund" class="refund-reference">退款 {{ order.refund.id.slice(0, 8) }} · {{ refundStatusLabel(order.refund.status) }}</small>
          </div>
          <b>¥ {{ money(order.amountCents) }}</b>
          <span :class="['status-pill', statusClass(order.status)]">{{ statusLabel(order.status) }}</span>
          <div class="payment-actions">
            <button v-if="order.status === 'pending'" class="primary compact" :disabled="busy === order.id" @click="markPaid(order)">
              <CheckCircle2 :size="16" />确认入账
            </button>
            <button v-if="order.status === 'pending'" class="secondary compact" :disabled="busy === order.id" @click="query(order)">
              <Search :size="16" />查询状态
            </button>
            <button v-if="order.status === 'pending'" class="icon-action" title="关闭订单" :disabled="busy === order.id" @click="close(order)">
              <XCircle :size="18" />
            </button>
            <button v-if="order.status === 'paid' && order.provider === 'manual'" class="secondary compact" :disabled="busy === order.id" @click="openRefund(order)">
              <Undo2 :size="16" />退款
            </button>
            <button v-else-if="order.status === 'paid'" class="secondary compact" disabled title="需先配置支付服务商退款协议">
              <Undo2 :size="16" />退款待配置
            </button>
            <button v-if="order.status === 'paid'" class="secondary compact" :disabled="busy === order.id" @click="query(order)">
              <Search :size="16" />查询状态
            </button>
          </div>
        </article>
        <p v-if="!orders.length" class="quiet empty-copy">暂无充值订单</p>
      </section>

      <section class="payment-list refund-list">
        <div class="section-heading">
          <div>
            <p class="eyebrow">不可变记录</p>
            <h2>退款记录</h2>
          </div>
          <span>{{ refunds.length }} 条记录</span>
        </div>
        <article v-for="refund in refunds" :key="refund.id" class="refund-record">
          <div class="refund-record-summary">
            <div class="payment-copy">
              <strong>{{ refund.displayName }} · {{ refund.email }}</strong>
              <small>订单 {{ refund.orderId.slice(0, 8) }} · {{ refund.provider === 'manual' ? '人工充值' : 'EPay' }} · {{ date(refund.createdAt) }}</small>
            </div>
            <b>¥ {{ money(refund.amountCents) }}</b>
            <span :class="['status-pill', statusClass(refund.status)]">{{ refundStatusLabel(refund.status) }}</span>
            <button class="icon-action" :title="expandedRefunds.includes(refund.id) ? '收起退款详情' : '展开退款详情'" @click="toggleRefund(refund.id)">
              <ChevronUp v-if="expandedRefunds.includes(refund.id)" :size="18" />
              <ChevronDown v-else :size="18" />
            </button>
          </div>
          <div v-if="expandedRefunds.includes(refund.id)" class="refund-record-detail">
            <dl class="refund-metadata">
              <div><dt>退款原因</dt><dd>{{ refund.reason }}</dd></div>
              <div><dt>操作人</dt><dd>{{ refund.requestedByEmail || '历史迁移' }}</dd></div>
              <div><dt>账本引用</dt><dd>{{ refund.ledgerEntryId || '未写入' }}</dd></div>
              <div><dt>完成时间</dt><dd>{{ date(refund.completedAt) }}</dd></div>
            </dl>
            <ol class="refund-timeline">
              <li v-for="event in refund.events" :key="event.id">
                <Clock3 :size="15" />
                <span><strong>{{ refundStatusLabel(event.toStatus) }}</strong><small>{{ event.message }} · {{ date(event.createdAt) }}</small></span>
              </li>
            </ol>
          </div>
        </article>
        <p v-if="!refunds.length" class="quiet empty-copy">暂无退款记录</p>
      </section>
    </main>
  </div>

  <Teleport to="body">
    <div v-if="refundTarget" class="dialog-backdrop" @click.self="cancelRefund">
      <section class="dialog-panel refund-dialog" role="dialog" aria-modal="true" aria-labelledby="refund-dialog-title">
        <header>
          <div>
            <p class="eyebrow">资金确认</p>
            <h2 id="refund-dialog-title">全额退款</h2>
          </div>
          <button class="icon-action" title="关闭" :disabled="busy === refundTarget.id" @click="cancelRefund"><X :size="18" /></button>
        </header>
        <div class="refund-dialog-amount">
          <span>{{ refundTarget.displayName }} · {{ refundTarget.email }}</span>
          <strong>¥ {{ money(refundTarget.amountCents) }}</strong>
        </div>
        <form @submit.prevent="submitRefund">
          <label>退款原因<textarea v-model="refundReason" rows="3" maxlength="500" required autofocus /></label>
          <p class="quiet">提交后会从账户钱包扣回本次充值金额，并写入不可变退款记录和事件时间线。</p>
          <div class="dialog-actions">
            <button type="button" class="secondary" :disabled="busy === refundTarget.id" @click="cancelRefund">取消</button>
            <button class="primary" :disabled="busy === refundTarget.id || !refundReason.trim()"><Undo2 :size="17" />确认退款</button>
          </div>
        </form>
      </section>
    </div>
  </Teleport>
</template>
