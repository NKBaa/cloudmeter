<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import {
  CheckCircle2,
  CircleAlert,
  Clock3,
  Inbox,
  MessageCircleMore,
  MessageSquareText,
  Plus,
  RefreshCw,
  Search,
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
  lastMessage: string;
  lastAuthorName: string;
  lastReplyStaff: boolean;
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
const searchQuery = ref("");
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
let refreshTimer: number | undefined;

const title = computed(() => props.admin ? "工单管理" : "工单支持");
const canReply = computed(() => detail.value?.status !== "closed");
const visibleTickets = computed(() => {
  const keyword = searchQuery.value.trim().toLocaleLowerCase("zh-CN");
  return tickets.value.filter((item) => {
    if (filterStatus.value === "finished" && item.status !== "resolved" && item.status !== "closed") return false;
    if (filterStatus.value && filterStatus.value !== "finished" && item.status !== filterStatus.value) return false;
    if (!keyword) return true;
    return [
      String(item.number), item.subject, item.requesterName, item.requesterEmail,
      item.lastMessage, categoryLabel(item.category), statusLabel(item.status),
    ].some((value) => value.toLocaleLowerCase("zh-CN").includes(keyword));
  });
});
const ticketStats = computed(() => ({
  total: tickets.value.length,
  open: tickets.value.filter((item) => item.status === "open").length,
  active: tickets.value.filter((item) => item.status === "in_progress").length,
  waiting: tickets.value.filter((item) => item.status === "waiting_user").length,
  finished: tickets.value.filter((item) => item.status === "resolved" || item.status === "closed").length,
}));

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
function formatFullTime(value: string) {
  return new Date(value).toLocaleString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
function initials(value: string) { return value.trim().slice(0, 1).toLocaleUpperCase("zh-CN") || "?"; }
function done(value: string) {
  notice.value = value;
  error.value = "";
  window.setTimeout(() => { if (notice.value === value) notice.value = ""; }, 2600);
}
function failed(value: unknown) { error.value = (value as Error).message; notice.value = ""; }
function responseHint(item: Ticket) {
  if (item.status === "closed" || item.status === "resolved") return "会话已结束";
  if (props.admin) return item.lastReplyStaff ? "等待用户回复" : "等待平台回复";
  return item.lastReplyStaff ? "等待你回复" : "平台处理中";
}
function needsAttention(item: Ticket) {
  if (item.status === "closed" || item.status === "resolved") return false;
  return props.admin ? !item.lastReplyStaff : item.lastReplyStaff;
}
function isOwnMessage(item: TicketMessage) { return props.admin ? item.staffReply : !item.staffReply; }
function messageRole(item: TicketMessage) { return item.staffReply ? "平台支持" : "用户"; }

async function scrollConversation(behavior: ScrollBehavior = "smooth") {
  await nextTick();
  const panel = document.querySelector<HTMLElement>(".ticket-conversation");
  panel?.scrollTo({ top: panel.scrollHeight, behavior });
}
async function loadDetail(id: string, quiet = false) {
  try {
    if (!quiet) busy.value = "detail";
    selectedID.value = id;
    const prefix = props.admin ? "/admin/tickets/" : "/tickets/";
    const previousCount = messages.value.length;
    const data = await api<{ ticket: Ticket; messages: TicketMessage[] }>(prefix + id);
    detail.value = data.ticket;
    messages.value = data.messages;
    if (!quiet || data.messages.length !== previousCount) await scrollConversation(quiet ? "auto" : "smooth");
    error.value = "";
  } catch (value) {
    if (!quiet) failed(value);
  } finally {
    if (!quiet) busy.value = "";
  }
}
async function load(preserve = true, quiet = false) {
  try {
    if (!quiet) busy.value = "list";
    const base = props.admin ? "/admin/tickets" : "/tickets";
    const data = await api<{ tickets: Ticket[] }>(base);
    tickets.value = data.tickets;
    const candidates = visibleTickets.value;
    const nextID = preserve && candidates.some((item) => item.id === selectedID.value)
      ? selectedID.value
      : candidates[0]?.id || "";
    if (nextID) await loadDetail(nextID, quiet);
    else { selectedID.value = ""; detail.value = null; messages.value = []; }
    error.value = "";
  } catch (value) {
    if (!quiet) failed(value);
  } finally {
    if (!quiet) busy.value = "";
  }
}
async function createTicket() {
  try {
    busy.value = "create";
    const created = await api<{ id: string; number: number }>("/tickets", { method: "POST", body: JSON.stringify(createForm) });
    Object.assign(createForm, { subject: "", category: "deployment", priority: "normal", body: "" });
    showCreate.value = false;
    filterStatus.value = "";
    searchQuery.value = "";
    done("工单 #" + created.number + " 已提交");
    await load(false);
    await loadDetail(created.id);
  } catch (value) { failed(value); } finally { busy.value = ""; }
}
async function reply() {
  if (!detail.value || !replyBody.value.trim() || busy.value === "reply") return;
  try {
    busy.value = "reply";
    const ticketID = detail.value.id;
    const prefix = props.admin ? "/admin/tickets/" : "/tickets/";
    await api(prefix + ticketID + "/messages", { method: "POST", body: JSON.stringify({ body: replyBody.value.trim() }) });
    replyBody.value = "";
    done("回复已发送");
    await loadDetail(ticketID);
    await refreshListOnly();
  } catch (value) { failed(value); } finally { busy.value = ""; }
}
async function refreshListOnly() {
  const base = props.admin ? "/admin/tickets" : "/tickets";
  const data = await api<{ tickets: Ticket[] }>(base);
  tickets.value = data.tickets;
}
async function updateStatus(status: string) {
  if (!props.admin || !detail.value || status === detail.value.status) return;
  try {
    busy.value = "status";
    const ticketID = detail.value.id;
    await api("/admin/tickets/" + ticketID + "/status", { method: "PATCH", body: JSON.stringify({ status }) });
    done("工单状态已改为“" + statusLabel(status) + "”");
    await loadDetail(ticketID);
    await refreshListOnly();
  } catch (value) { failed(value); } finally { busy.value = ""; }
}
async function closeTicket() {
  if (props.admin || !detail.value || !confirm("确认关闭这个工单？关闭后不能继续回复。")) return;
  try {
    busy.value = "close";
    const ticketID = detail.value.id;
    await api("/tickets/" + ticketID + "/close", { method: "POST" });
    done("工单已关闭");
    await loadDetail(ticketID);
    await refreshListOnly();
  } catch (value) { failed(value); } finally { busy.value = ""; }
}

watch([filterStatus, searchQuery], async () => {
  const first = visibleTickets.value[0];
  if (!first) { selectedID.value = ""; detail.value = null; messages.value = []; return; }
  if (!visibleTickets.value.some((item) => item.id === selectedID.value)) await loadDetail(first.id);
});
onMounted(async () => {
  await load(false);
  refreshTimer = window.setInterval(() => {
    if (!busy.value && !showCreate.value) void load(true, true);
  }, 15000);
});
onBeforeUnmount(() => { if (refreshTimer) window.clearInterval(refreshTimer); });
</script>

<template>
  <main class="workspace tickets-view">
    <header class="ticket-page-heading">
      <div>
        <p class="eyebrow">{{ admin ? '服务运营' : '帮助中心' }}</p>
        <h1>{{ title }}</h1>
        <p class="quiet">{{ admin ? '集中处理用户问题，完整保留每一次沟通与状态变化。' : '问题、补充信息和平台回复都集中在同一条会话中。' }}</p>
      </div>
      <div class="ticket-header-actions">
        <button class="secondary compact" :disabled="busy === 'list'" @click="load()"><RefreshCw :class="{ spin: busy === 'list' }" :size="16" />刷新</button>
        <button v-if="!admin" class="primary compact" @click="showCreate = true"><Plus :size="16" />新建工单</button>
      </div>
    </header>

    <section class="ticket-summary-strip" aria-label="工单状态概览">
      <button :class="{ active: filterStatus === '' }" @click="filterStatus = ''"><span>全部工单</span><strong>{{ ticketStats.total }}</strong></button>
      <button :class="{ active: filterStatus === 'open' }" @click="filterStatus = 'open'"><span>{{ admin ? '待接入' : '待处理' }}</span><strong>{{ ticketStats.open }}</strong></button>
      <button :class="{ active: filterStatus === 'in_progress' }" @click="filterStatus = 'in_progress'"><span>处理中</span><strong>{{ ticketStats.active }}</strong></button>
      <button :class="{ active: filterStatus === 'waiting_user' }" @click="filterStatus = 'waiting_user'"><span>{{ admin ? '等待用户' : '等待我' }}</span><strong>{{ ticketStats.waiting }}</strong></button>
      <button :class="{ active: filterStatus === 'finished' }" @click="filterStatus = 'finished'"><span>已完成</span><strong>{{ ticketStats.finished }}</strong></button>
    </section>

    <Transition name="toast-pop">
      <p v-if="error || notice" :class="['ticket-toast', error ? 'error' : 'success']" role="status">{{ error || notice }}</p>
    </Transition>

    <section class="ticket-workbench">
      <section class="ticket-list-panel">
        <div class="ticket-list-heading">
          <div><strong>{{ admin ? '服务队列' : '我的工单' }}</strong><small>{{ visibleTickets.length }} / {{ tickets.length }}</small></div>
          <label class="ticket-search"><Search :size="15" /><input v-model="searchQuery" type="search" aria-label="搜索工单" placeholder="搜索标题、编号或用户" /></label>
        </div>
        <div class="ticket-list">
          <button v-for="item in visibleTickets" :key="item.id" :class="['ticket-list-item', selectedID === item.id && 'selected']" @click="loadDetail(item.id)">
            <span class="ticket-list-top">
              <span><b>#{{ item.number }}</b><i v-if="needsAttention(item)"></i><small>{{ responseHint(item) }}</small></span>
              <time>{{ formatTime(item.lastMessageAt) }}</time>
            </span>
            <strong class="ticket-list-subject">{{ item.subject }}</strong>
            <span v-if="admin" class="ticket-requester"><span>{{ initials(item.requesterName) }}</span>{{ item.requesterName }}<small>{{ item.requesterEmail }}</small></span>
            <span class="ticket-last-message"><b>{{ item.lastReplyStaff ? '平台' : '用户' }}</b>{{ item.lastMessage }}</span>
            <span class="ticket-list-meta">
              <span :class="['status-pill', statusClass(item.status)]">{{ statusLabel(item.status) }}</span>
              <span>{{ categoryLabel(item.category) }}</span>
              <span :class="['ticket-priority-dot', item.priority]">{{ priorityLabel(item.priority) }}</span>
              <span>{{ item.messageCount }} 条消息</span>
            </span>
          </button>
          <div v-if="!visibleTickets.length && busy !== 'list'" class="ticket-list-empty">
            <Inbox :size="22" />
            <strong>{{ tickets.length ? '没有匹配的工单' : (admin ? '当前还没有工单' : '还没有提交过工单') }}</strong>
            <small>{{ tickets.length ? '调整搜索词或状态筛选。' : (admin ? '用户提交后会出现在这里。' : '需要帮助时可从右上角新建工单。') }}</small>
          </div>
        </div>
      </section>

      <Transition name="panel-swap" mode="out-in">
        <article v-if="detail" :key="detail.id" class="ticket-detail-panel">
          <header class="ticket-detail-heading">
            <div class="ticket-detail-title">
              <span class="ticket-detail-number">#{{ detail.number }}</span>
              <div><h2>{{ detail.subject }}</h2><p v-if="admin"><UserRound :size="14" />{{ detail.requesterName }} · {{ detail.requesterEmail }}</p></div>
            </div>
            <div class="ticket-detail-badges"><span :class="['status-pill', statusClass(detail.status)]">{{ statusLabel(detail.status) }}</span><span class="ticket-priority">{{ priorityLabel(detail.priority) }}优先级</span></div>
          </header>
          <div class="ticket-detail-meta">
            <span>{{ categoryLabel(detail.category) }}</span>
            <span><Clock3 :size="14" />创建于 {{ formatFullTime(detail.createdAt) }}</span>
            <span><MessageCircleMore :size="14" />{{ detail.messageCount }} 条消息</span>
          </div>
          <div class="ticket-conversation" aria-live="polite">
            <div v-if="busy === 'detail'" class="ticket-conversation-loading"><RefreshCw class="spin" :size="18" />正在读取会话…</div>
            <div v-for="item in messages" :key="item.id" :class="['ticket-message', item.staffReply && 'staff', isOwnMessage(item) && 'mine']">
              <span class="ticket-message-avatar">{{ initials(item.authorName) }}</span>
              <div class="ticket-message-content">
                <div><strong>{{ item.authorName }}</strong><span>{{ messageRole(item) }}</span><time>{{ formatFullTime(item.createdAt) }}</time></div>
                <p>{{ item.body }}</p>
              </div>
            </div>
          </div>
          <footer class="ticket-composer">
            <div v-if="admin" class="ticket-status-control">
              <span><b>处理状态</b><small>回复后默认等待用户补充</small></span>
              <select :value="detail.status" aria-label="处理状态" :disabled="busy === 'status'" @change="updateStatus(($event.target as HTMLSelectElement).value)"><option v-for="(label, value) in statusLabels" :key="value" :value="value">{{ label }}</option></select>
            </div>
            <form v-if="canReply" @submit.prevent="reply">
              <label><span>{{ admin ? '回复用户' : '发送补充信息' }}</span><textarea v-model="replyBody" rows="3" maxlength="10000" required :placeholder="admin ? '说明处理进展、解决方法或需要用户补充的信息…' : '补充现象、日志或复现步骤…'" @keydown.ctrl.enter.prevent="reply" @keydown.meta.enter.prevent="reply" /></label>
              <div class="ticket-composer-actions"><small>{{ replyBody.length }} / 10000 · Ctrl + Enter 发送</small><span><button v-if="!admin" type="button" class="secondary compact" :disabled="busy === 'close'" @click="closeTicket">关闭工单</button><button class="primary compact" :disabled="busy === 'reply' || !replyBody.trim()"><Send :size="15" />{{ busy === 'reply' ? '发送中…' : '发送回复' }}</button></span></div>
            </form>
            <p v-else class="ticket-closed-note"><CheckCircle2 :size="17" />工单已关闭。如问题仍未解决，请新建工单继续咨询。</p>
          </footer>
        </article>
        <div v-else class="ticket-detail-empty"><span class="context-empty-icon"><MessageSquareText :size="24" /></span><div><h2>{{ tickets.length ? '选择一条工单查看会话' : '暂无会话' }}</h2><p>{{ admin ? '工单进入队列后，可在这里查看上下文并回复用户。' : '新建工单后，平台回复会集中显示在这里。' }}</p></div></div>
      </Transition>
    </section>

    <Transition name="modal-pop">
      <div v-if="showCreate" class="modal-backdrop" @click.self="showCreate = false">
        <section class="secret-dialog ticket-create-dialog" role="dialog" aria-modal="true" aria-labelledby="ticket-create-title">
          <header><div><p class="eyebrow">帮助中心</p><h2 id="ticket-create-title">新建工单</h2></div><button class="icon-action" type="button" title="关闭" @click="showCreate = false"><X :size="18" /></button></header>
          <form @submit.prevent="createTicket"><label>问题标题<input v-model.trim="createForm.subject" minlength="2" maxlength="160" required placeholder="简要描述遇到的问题" /></label><div class="field-row"><label>问题类型<select v-model="createForm.category"><option v-for="(label, value) in categoryLabels" :key="value" :value="value">{{ label }}</option></select></label><label>优先级<select v-model="createForm.priority"><option v-for="(label, value) in priorityLabels" :key="value" :value="value">{{ label }}</option></select></label></div><label>问题描述<textarea v-model.trim="createForm.body" rows="7" maxlength="10000" required placeholder="请写明操作步骤、预期结果和实际现象；如有错误日志也请一并提供。" /></label><p class="ticket-create-hint"><CircleAlert :size="15" />请勿在工单中提交密码、Token 或私钥。</p><div class="deploy-dialog-actions"><button class="secondary compact" type="button" @click="showCreate = false">取消</button><button class="primary compact" :disabled="busy === 'create'"><Send :size="15" />{{ busy === 'create' ? '提交中…' : '提交工单' }}</button></div></form>
        </section>
      </div>
    </Transition>
  </main>
</template>
