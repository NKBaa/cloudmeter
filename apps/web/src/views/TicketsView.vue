<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from "vue";
import {
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  Clock3,
  Inbox,
  MessageSquareText,
  Plus,
  RefreshCw,
  Send,
  UserRound,
  X,
} from "@lucide/vue";
import { api } from "../api";

type Ticket = {
  id: string;
  number: number;
  userId: string;
  requesterName: string;
  requesterEmail: string;
  subject: string;
  category: string;
  priority: string;
  status: string;
  messageCount: number;
  lastMessageAt: string;
  createdAt: string;
  updatedAt: string;
};
type TicketMessage = {
  id: string;
  authorId: string;
  authorName: string;
  staffReply: boolean;
  body: string;
  createdAt: string;
};

const props = withDefaults(defineProps<{ admin?: boolean }>(), { admin: false });
const tickets = ref<Ticket[]>([]);
const selectedID = ref("");
const detail = ref<Ticket | null>(null);
const messages = ref<TicketMessage[]>([]);
const filterStatus = ref("");
const replyBody = ref("");
const showCreate = ref(false);
const busy = ref("");
const error = ref("");
const notice = ref("");
const createForm = reactive({
  subject: "",
  category: "deployment",
  priority: "normal",
  body: "",
});

const listPath = computed(() => {
  const base = props.admin ? "/admin/tickets" : "/tickets";
  return props.admin && filterStatus.value
    ? `${base}?status=${encodeURIComponent(filterStatus.value)}`
    : base;
});
const title = computed(() => props.admin ? "工单管理" : "工单支持");
const canReply = computed(() => detail.value?.status !== "closed");

const categoryLabels: Record<string, string> = {
  deployment: "部署与应用", billing: "账单与支付", account: "账户与权限", product: "产品建议", other: "其他问题",
};
const priorityLabels: Record<string, string> = { low: "低", normal: "普通", high: "高", urgent: "紧急" };
const statusLabels: Record<string, string> = { open: "待处理", in_progress: "处理中", waiting_user: "等待用户", resolved: "已解决", closed: "已关闭" };

function statusLabel(value: string) { return statusLabels[value] || value; }
function categoryLabel(value: string) { return categoryLabels[value] || value; }
function priorityLabel(value: string) { return priorityLabels[value] || value; }
function statusClass(value: string) {
  if (value === "open" || value === "in_progress") return "pending";
  if (value === "waiting_user") return "active";
  if (value === "closed") return "suspended";
  return "resolved";
}
function formatTime(value: string) {
  return new Date(value).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
function done(value: string) { notice.value = value; error.value = ""; }
function failed(value: unknown) { error.value = (value as Error).message; notice.value = ""; }

async function loadDetail(id: string) {
  try {
    busy.value = "detail";
    selectedID.value = id;
    const prefix = props.admin ? "/admin/tickets/" : "/tickets/";
    const data = await api<{ ticket: Ticket; messages: TicketMessage[] }>(prefix + id);
    detail.value = data.ticket;
    messages.value = data.messages;
    await nextTick();
    document.querySelector(".ticket-conversation")?.scrollTo({ top: 100000, behavior: "smooth" });
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function load(preserve = true) {
  try {
    busy.value = "list";
    const data = await api<{ tickets: Ticket[] }>(listPath.value);
    tickets.value = data.tickets;
    const nextID = preserve && tickets.value.some((item) => item.id === selectedID.value)
      ? selectedID.value
      : tickets.value[0]?.id || "";
    if (nextID) await loadDetail(nextID);
    else { selectedID.value = ""; detail.value = null; messages.value = []; }
    error.value = "";
  } catch (value) {
    failed(value);
  } finally {
    busy.value = "";
  }
}
async function createTicket() {
  try {
    busy.value = "create";
    const created = await api<{ id: string; number: number }>("/tickets", { method: "POST", body: JSON.stringify(createForm) });
    Object.assign(createForm, { subject: "", category: "deployment", priority: "normal", body: "" });
    showCreate.value = false;
    done(`工单 #${created.number} 已提交`);
    await load(false);
    await loadDetail(created.id);
  } catch (value) { failed(value); } finally { busy.value = ""; }
}
async function reply() {
  if (!detail.value || !replyBody.value.trim()) return;
  try {
    busy.value = "reply";
    const prefix = props.admin ? "/admin/tickets/" : "/tickets/";
    await api(`${prefix}${detail.value.id}/messages`, { method: "POST", body: JSON.stringify({ body: replyBody.value.trim() }) });
    replyBody.value = "";
    done("回复已发送");
    await loadDetail(detail.value.id);
    await refreshListOnly();
  } catch (value) { failed(value); } finally { busy.value = ""; }
}
async function refreshListOnly() {
  const data = await api<{ tickets: Ticket[] }>(listPath.value);
  tickets.value = data.tickets;
}
async function updateStatus(status: string) {
  if (!props.admin || !detail.value || status === detail.value.status) return;
  try {
    busy.value = "status";
    await api(`/admin/tickets/${detail.value.id}/status`, { method: "PATCH", body: JSON.stringify({ status }) });
    done(`工单状态已改为“${statusLabel(status)}”`);
    await loadDetail(detail.value.id);
    await refreshListOnly();
  } catch (value) { failed(value); } finally { busy.value = ""; }
}
async function closeTicket() {
  if (props.admin || !detail.value || !confirm("确认关闭这个工单？关闭后不能继续回复。")) return;
  try {
    busy.value = "close";
    await api(`/tickets/${detail.value.id}/close`, { method: "POST" });
    done("工单已关闭");
    await loadDetail(detail.value.id);
    await refreshListOnly();
  } catch (value) { failed(value); } finally { busy.value = ""; }
}

onMounted(() => load(false));
</script>

<template>
  <main class="workspace tickets-view">
    <header>
      <div><p class="eyebrow">{{ admin ? '服务运营' : '帮助中心' }}</p><h1>{{ title }}</h1><p class="quiet">{{ admin ? '处理用户问题、回复并推进工单状态。' : '提交问题并在同一条会话中持续跟进。' }}</p></div>
      <div class="ticket-header-actions">
        <button class="secondary compact" :disabled="busy === 'list'" @click="load()"><RefreshCw :class="{ spin: busy === 'list' }" :size="16" />刷新</button>
        <button v-if="!admin" class="primary compact" @click="showCreate = true"><Plus :size="16" />新建工单</button>
      </div>
    </header>
    <p v-if="error" class="message sticky-message">{{ error }}</p>
    <p v-if="notice" class="status-ok sticky-message">{{ notice }}</p>

    <section class="ticket-workbench">
      <aside class="ticket-list-panel">
        <div class="ticket-list-heading">
          <div><strong>{{ admin ? '全部工单' : '我的工单' }}</strong><small>{{ tickets.length }} 条</small></div>
          <select v-if="admin" v-model="filterStatus" aria-label="筛选工单状态" @change="load(false)">
            <option value="">全部状态</option><option v-for="(label, value) in statusLabels" :key="value" :value="value">{{ label }}</option>
          </select>
        </div>
        <div class="ticket-list">
          <button v-for="item in tickets" :key="item.id" :class="['ticket-list-item', selectedID === item.id && 'selected']" @click="loadDetail(item.id)">
            <span class="ticket-list-top"><b>#{{ item.number }}</b><span :class="['status-pill', statusClass(item.status)]">{{ statusLabel(item.status) }}</span></span>
            <strong>{{ item.subject }}</strong>
            <small v-if="admin">{{ item.requesterName }} · {{ item.requesterEmail }}</small>
            <span class="ticket-list-meta"><span>{{ categoryLabel(item.category) }}</span><span>{{ item.messageCount }} 条消息</span><span>{{ formatTime(item.lastMessageAt) }}</span></span>
            <ChevronRight :size="16" />
          </button>
          <div v-if="!tickets.length && busy !== 'list'" class="ticket-list-empty"><Inbox :size="24" /><strong>{{ admin ? '当前筛选下没有工单' : '还没有提交过工单' }}</strong><small>{{ admin ? '调整状态筛选，或等待用户提交新问题。' : '遇到部署、账单或账户问题时，可以创建工单。' }}</small><button v-if="!admin" class="secondary compact" @click="showCreate = true"><Plus :size="15" />新建工单</button></div>
        </div>
      </aside>

      <Transition name="panel-swap" mode="out-in">
        <article v-if="detail" :key="detail.id" class="ticket-detail-panel">
          <header class="ticket-detail-heading">
            <div><p class="eyebrow">工单 #{{ detail.number }}</p><h2>{{ detail.subject }}</h2><p v-if="admin"><UserRound :size="14" />{{ detail.requesterName }} · {{ detail.requesterEmail }}</p></div>
            <div class="ticket-detail-badges"><span :class="['status-pill', statusClass(detail.status)]">{{ statusLabel(detail.status) }}</span><span class="ticket-priority">{{ priorityLabel(detail.priority) }}优先级</span></div>
          </header>
          <div class="ticket-detail-meta"><span>{{ categoryLabel(detail.category) }}</span><span><Clock3 :size="14" />创建于 {{ formatTime(detail.createdAt) }}</span></div>
          <div class="ticket-conversation">
            <div v-for="item in messages" :key="item.id" :class="['ticket-message', item.staffReply && 'staff']">
              <div><strong>{{ item.staffReply ? '平台支持 · ' : '' }}{{ item.authorName }}</strong><time>{{ formatTime(item.createdAt) }}</time></div>
              <p>{{ item.body }}</p>
            </div>
          </div>
          <footer class="ticket-composer">
            <div v-if="admin" class="ticket-status-control"><label>处理状态<select :value="detail.status" :disabled="busy === 'status'" @change="updateStatus(($event.target as HTMLSelectElement).value)"><option v-for="(label, value) in statusLabels" :key="value" :value="value">{{ label }}</option></select></label></div>
            <form v-if="canReply" @submit.prevent="reply"><label>回复内容<textarea v-model="replyBody" rows="3" maxlength="10000" required :placeholder="admin ? '给用户回复处理进展…' : '补充现象、日志或复现步骤…'" /></label><div><button v-if="!admin" type="button" class="secondary compact" :disabled="busy === 'close'" @click="closeTicket">关闭工单</button><button class="primary compact" :disabled="busy === 'reply' || !replyBody.trim()"><Send :size="15" />发送回复</button></div></form>
            <p v-else class="ticket-closed-note"><CheckCircle2 :size="17" />工单已关闭，不能继续回复。</p>
          </footer>
        </article>
        <div v-else class="ticket-detail-empty"><span class="context-empty-icon"><MessageSquareText :size="24" /></span><div><p class="eyebrow">工单会话</p><h2>选择一条工单查看详情</h2><p>{{ admin ? '左侧会显示用户提交的待处理问题。' : '新建工单后，平台回复会集中显示在这里。' }}</p></div></div>
      </Transition>
    </section>

    <Transition name="modal-pop">
      <div v-if="showCreate" class="modal-backdrop" @click.self="showCreate = false">
        <section class="secret-dialog ticket-create-dialog" role="dialog" aria-modal="true" aria-labelledby="ticket-create-title">
          <header><div><p class="eyebrow">帮助中心</p><h2 id="ticket-create-title">新建工单</h2></div><button class="icon-action" type="button" title="关闭" @click="showCreate = false"><X :size="18" /></button></header>
          <form @submit.prevent="createTicket"><label>问题标题<input v-model.trim="createForm.subject" minlength="2" maxlength="160" required placeholder="简要描述遇到的问题" /></label><div class="field-row"><label>问题类型<select v-model="createForm.category"><option v-for="(label, value) in categoryLabels" :key="value" :value="value">{{ label }}</option></select></label><label>优先级<select v-model="createForm.priority"><option v-for="(label, value) in priorityLabels" :key="value" :value="value">{{ label }}</option></select></label></div><label>问题描述<textarea v-model.trim="createForm.body" rows="7" maxlength="10000" required placeholder="请写明操作步骤、预期结果和实际现象；如有错误日志也请一并提供。" /></label><p class="ticket-create-hint"><CircleAlert :size="15" />请勿在工单中提交密码、Token 或私钥。</p><div class="deploy-dialog-actions"><button class="secondary compact" type="button" @click="showCreate = false">取消</button><button class="primary compact" :disabled="busy === 'create'"><Send :size="15" />提交工单</button></div></form>
        </section>
      </div>
    </Transition>
  </main>
</template>
