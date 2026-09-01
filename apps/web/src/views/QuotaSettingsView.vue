<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import {
  BadgeDollarSign,
  CalendarCheck,
  Clock,
  Gem,
  Gift,
  Save,
  Sparkles,
  UserPlus,
  Users,
} from "@lucide/vue";
import { api } from "../api";
const form = reactive({ initialGrantCents: 0, inviteRewardCents: 0 });
const checkin = reactive({ enabled: true, minRewardCents: 1, maxRewardCents: 10 });
const busy = ref(false),
  loading = ref(true),
  message = ref(""),
  error = ref("");
const formatYuan = (cents: number) => `¥${(cents / 100).toFixed(2)}`;
const checkinRange = computed(
  () => `${formatYuan(checkin.minRewardCents)} ~ ${formatYuan(checkin.maxRewardCents)}`,
);
async function load() {
  loading.value = true;
  try {
    const [quota, checkinSettings] = await Promise.all([
      api("/admin/settings/quota"),
      api("/admin/settings/checkin"),
    ]);
    Object.assign(form, quota);
    Object.assign(checkin, checkinSettings);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
async function save() {
  busy.value = true;
  error.value = "";
  message.value = "";
  try {
    await Promise.all([
      api("/admin/settings/quota", {
        method: "PUT",
        body: JSON.stringify(form),
      }),
      api("/admin/settings/checkin", {
        method: "PUT",
        body: JSON.stringify(checkin),
      }),
    ]);
    message.value = "额度设置已保存";
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
onMounted(load);
</script>
<template>
  <main class="workspace admin-workspace quota-settings-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">资金与运营</p>
        <h1>额度设置</h1>
        <p class="quiet">
          集中管理新用户、邀请和每日签到的赠送额度。修改只影响后续发放，不追溯已有余额。
        </p>
      </div>
      <div class="quota-header-actions">
        <button class="primary compact" :disabled="busy || loading" @click="save">
          <Save :size="15" />{{ busy ? "保存中…" : "保存额度设置" }}
        </button>
      </div>
    </header>

    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok">{{ message }}</p>

    <section class="quota-hero">
      <div class="quota-hero-inner">
        <span class="quota-hero-icon"><Gem :size="24" /></span>
        <div>
          <p class="eyebrow">账户赠送规则</p>
          <h2>让每个新账户从一笔可用额度开始</h2>
          <p>
            额度进入不可变的赠送账本，可用来抵扣用量与订阅费用，与充值余额分开记录。
          </p>
        </div>
      </div>
      <div class="quota-hero-actions">
        <RouterLink class="secondary compact" to="/admin/users">
          <Users :size="15" />查看用户
        </RouterLink>
        <RouterLink class="secondary compact" to="/admin/payments">
          <BadgeDollarSign :size="15" />支付订单
        </RouterLink>
      </div>
    </section>

    <section class="checkin-quota-panel" v-if="!loading">
      <div class="checkin-quota-heading">
        <span class="quota-field-icon"><CalendarCheck :size="19" /></span>
        <div>
          <p class="eyebrow">每日签到额度</p>
          <h2>签到开关与随机奖励区间</h2>
          <p>每天北京时间零点重置，奖励直接进入不可变赠送账本。</p>
        </div>
        <div class="checkin-quota-status">
          <Clock :size="15" />{{ checkin.enabled ? checkinRange : "已停用" }}
        </div>
      </div>
      <div class="checkin-quota-toggle">
        <div>
          <strong>允许用户每日签到</strong>
          <small>关闭后隐藏用户签到入口，历史奖励记录不受影响。</small>
        </div>
        <label class="switch"><input v-model="checkin.enabled" type="checkbox" /><span /></label>
      </div>
      <div class="checkin-quota-fields">
        <label>
          <span>最低奖励（分）</span>
          <input v-model.number="checkin.minRewardCents" type="number" min="1" :max="checkin.maxRewardCents" step="1" required />
          <small>每次至少 {{ formatYuan(checkin.minRewardCents) }}</small>
        </label>
        <label>
          <span>最高奖励（分）</span>
          <input v-model.number="checkin.maxRewardCents" type="number" :min="checkin.minRewardCents" max="10000" step="1" required />
          <small>每次至多 {{ formatYuan(checkin.maxRewardCents) }}</small>
        </label>
      </div>
    </section>

    <section class="quota-grid">
      <article class="quota-field-card">
        <div class="quota-field-head">
          <span class="quota-field-icon"><UserPlus :size="18" /></span>
          <div class="quota-field-body">
            <h3>新用户初始额度</h3>
            <p>通过密码、OAuth 或管理员创建的新账户，统一发放进不可变赠送账本。</p>
          </div>
        </div>
        <div class="quota-input-wrapper">
          <label class="quota-amount">
            <span class="quota-currency">¥</span>
            <input
              :value="form.initialGrantCents / 100"
              type="number"
              min="0"
              step="0.01"
              :disabled="loading"
              @input="
                form.initialGrantCents = Math.round(
                  Number(($event.target as HTMLInputElement).value) * 100,
                )
              "
            />
          </label>
          <span class="quota-current">当前设置：{{ formatYuan(form.initialGrantCents) }}</span>
        </div>
      </article>

      <article class="quota-field-card">
        <div class="quota-field-head">
          <span class="quota-field-icon"><Sparkles :size="18" /></span>
          <div class="quota-field-body">
            <h3>邀请者奖励</h3>
            <p>每个邀请码只能被一个账户核销，奖励写入独立账目，可追踪核销来源。</p>
          </div>
        </div>
        <div class="quota-input-wrapper">
          <label class="quota-amount">
            <span class="quota-currency">¥</span>
            <input
              :value="form.inviteRewardCents / 100"
              type="number"
              min="0"
              step="0.01"
              :disabled="loading"
              @input="
                form.inviteRewardCents = Math.round(
                  Number(($event.target as HTMLInputElement).value) * 100,
                )
              "
            />
          </label>
          <span class="quota-current">当前设置：{{ formatYuan(form.inviteRewardCents) }}</span>
        </div>
      </article>
    </section>

    <section class="quota-tips">
      <div class="quota-tips-item">
        <Gift :size="16" />
        <div>
          <strong>不影响已有账户</strong>
          <small>保存后只对以后创建的新账户生效，历史余额保持不变。</small>
        </div>
      </div>
      <div class="quota-tips-item">
        <Users :size="16" />
        <div>
          <strong>与充值分开记录</strong>
          <small>赠送额度与充值余额分账记录，过期策略由账本规则统一管理。</small>
        </div>
      </div>
      <div class="quota-tips-item">
        <Sparkles :size="16" />
        <div>
          <strong>可搭配签到</strong>
          <small>初始额度叠加每日签到奖励，为运营提供灵活的组合空间。</small>
        </div>
      </div>
    </section>
  </main>
</template>

<style scoped>
.quota-settings-page {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.quota-settings-page > header {
  margin-bottom: 0;
}

.quota-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.quota-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  padding: 18px 22px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--paper);
  box-shadow: var(--shadow-xs);
}
.quota-hero-inner {
  display: flex;
  align-items: center;
  gap: 16px;
  min-width: 0;
}
.quota-hero-icon {
  width: 44px;
  height: 44px;
  flex: 0 0 44px;
  display: grid;
  place-items: center;
  border-radius: 12px;
  background: var(--accent);
  color: #fff;
}
.quota-hero-inner .eyebrow {
  margin-bottom: 2px;
}
.quota-hero-inner h2 {
  margin: 0 0 4px;
  font-size: 17px;
  font-weight: 700;
  color: var(--text);
}
.quota-hero-inner p {
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.5;
}
.quota-hero-actions {
  display: flex;
  gap: 8px;
  flex: 0 0 auto;
}

.quota-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
}

.quota-field-card {
  padding: 20px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--paper);
  box-shadow: var(--shadow-xs);
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.quota-field-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.quota-field-icon {
  width: 38px;
  height: 38px;
  flex: 0 0 38px;
  display: grid;
  place-items: center;
  border-radius: 10px;
  background: var(--accent-soft, rgba(56, 189, 248, 0.12));
  color: var(--accent);
}

.quota-field-body h3 {
  margin: 0 0 4px;
  font-size: 15px;
  font-weight: 700;
  color: var(--text);
}

.quota-field-body p {
  margin: 0;
  color: var(--text-muted);
  font-size: 12.5px;
  line-height: 1.5;
}

.quota-input-wrapper {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: auto;
}

.quota-amount {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 42px;
  width: 180px;
  padding: 0 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--field);
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.quota-amount:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-glow);
}

.quota-currency {
  font-size: 16px;
  font-weight: 700;
  color: var(--accent);
}

.quota-amount input {
  min-width: 0;
  width: 100%;
  border: 0;
  background: transparent;
  padding: 0;
  font-size: 18px;
  font-weight: 700;
  color: var(--text);
  outline: none;
  box-shadow: none !important;
}

.quota-current {
  font-size: 12px;
  color: var(--text-muted);
}

.quota-tips {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
}

.checkin-quota-panel {
  padding: 20px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--paper);
  box-shadow: var(--shadow-xs);
}
.checkin-quota-heading {
  display: grid;
  grid-template-columns: 38px minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding-bottom: 18px;
  border-bottom: 1px solid var(--line);
}
.checkin-quota-heading h2 { margin: 2px 0 4px; font-size: 16px; }
.checkin-quota-heading p { margin: 0; color: var(--text-muted); font-size: 12px; }
.checkin-quota-status {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--accent);
  font-size: 12px;
  font-weight: 650;
}
.checkin-quota-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 18px 0;
}
.checkin-quota-toggle strong,
.checkin-quota-toggle small { display: block; }
.checkin-quota-toggle strong { font-size: 13px; }
.checkin-quota-toggle small { margin-top: 4px; color: var(--text-muted); font-size: 12px; }
.checkin-quota-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.checkin-quota-fields label { display: grid; gap: 7px; font-size: 12px; font-weight: 650; }
.checkin-quota-fields input { width: 100%; }
.checkin-quota-fields small { color: var(--text-muted); font-weight: 400; }

.quota-tips-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 14px 16px;
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--paper);
}

.quota-tips-item > svg {
  flex: 0 0 auto;
  color: var(--accent);
  margin-top: 2px;
}

.quota-tips-item strong {
  display: block;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  margin-bottom: 2px;
}

.quota-tips-item small {
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.5;
}

@media (max-width: 900px) {
  .quota-hero {
    flex-direction: column;
    align-items: flex-start;
  }
  .quota-grid {
    grid-template-columns: 1fr;
  }
  .quota-tips {
    grid-template-columns: 1fr;
  }
  .checkin-quota-fields { grid-template-columns: 1fr; }
  .checkin-quota-heading { grid-template-columns: 38px minmax(0, 1fr); }
  .checkin-quota-status { grid-column: 2; }
}
</style>
