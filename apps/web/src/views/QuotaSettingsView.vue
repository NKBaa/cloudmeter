<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { BadgeDollarSign, Gem, Gift, Save, Sparkles, UserPlus, Users } from '@lucide/vue'
import { api } from '../api'
const form = reactive({ initialGrantCents: 0, inviteRewardCents: 0 })
const busy = ref(false), loading = ref(true), message = ref(''), error = ref('')
const formatYuan = (cents: number) => `¥${(cents / 100).toFixed(2)}`
async function load() {
  loading.value = true
  try { Object.assign(form, await api('/admin/settings/quota')) }
  catch (e) { error.value = (e as Error).message }
  finally { loading.value = false }
}
async function save() {
  busy.value = true; error.value = ''; message.value = ''
  try {
    await api('/admin/settings/quota', { method: 'PUT', body: JSON.stringify(form) })
    message.value = '额度设置已保存'
  } catch (e) { error.value = (e as Error).message }
  finally { busy.value = false }
}
onMounted(load)
</script>
<template>
  <main class="workspace admin-workspace quota-settings-page">
    <header>
      <div>
        <p class="eyebrow">资金与运营</p>
        <h1>额度设置</h1>
        <p class="quiet">控制新账户进入平台时的初始赠送额度，以及邀请他人时发放给邀请者的奖励。支持仅修改远期规则，不追溯已有余额。</p>
      </div>
    </header>
    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok">{{ message }}</p>

    <section class="quota-hero">
      <div class="quota-hero-inner">
        <span class="quota-hero-icon"><Gem :size="26" /></span>
        <div>
          <p class="eyebrow">账户赠送规则</p>
          <h2>让每个新账户从一笔可用额度开始</h2>
          <p>额度进入不可变的赠送账本，可用来抵扣用量与订阅费用，与充值余额分开记录。</p>
        </div>
      </div>
      <div class="quota-hero-actions">
        <RouterLink class="secondary compact" to="/admin/users"><Users :size="15" />查看用户</RouterLink>
        <RouterLink class="secondary compact" to="/admin/payments"><BadgeDollarSign :size="15" />支付订单</RouterLink>
      </div>
    </section>

    <section class="quota-grid">
      <article class="quota-field-card">
        <span class="quota-field-icon"><UserPlus :size="20" /></span>
        <div class="quota-field-body">
          <h3>新用户初始额度</h3>
          <p>通过密码、OAuth 或管理员创建的新账户，统一进入不可变赠送账本。</p>
        </div>
        <label class="quota-amount">
          <span class="quota-currency">¥</span>
          <input
            :value="form.initialGrantCents / 100"
            type="number" min="0" step="0.01"
            :disabled="loading"
            @input="form.initialGrantCents = Math.round(Number(($event.target as HTMLInputElement).value) * 100)"
          />
        </label>
        <span class="quota-current">当前 {{ formatYuan(form.initialGrantCents) }}</span>
      </article>

      <article class="quota-field-card">
        <span class="quota-field-icon"><Sparkles :size="20" /></span>
        <div class="quota-field-body">
          <h3>邀请者奖励</h3>
          <p>每个邀请码只能被一个账户核销，奖励写入独立账目，可追踪来源。</p>
        </div>
        <label class="quota-amount">
          <span class="quota-currency">¥</span>
          <input
            :value="form.inviteRewardCents / 100"
            type="number" min="0" step="0.01"
            :disabled="loading"
            @input="form.inviteRewardCents = Math.round(Number(($event.target as HTMLInputElement).value) * 100)"
          />
        </label>
        <span class="quota-current">当前 {{ formatYuan(form.inviteRewardCents) }}</span>
      </article>
    </section>

    <section class="quota-tips">
      <div class="quota-tips-item"><Gift :size="16" /><div><strong>不影响已有账户</strong><small>保存后只对以后创建的新账户生效，历史余额保持不变。</small></div></div>
      <div class="quota-tips-item"><Users :size="16" /><div><strong>与充值分开记录</strong><small>赠送额度与流量、充值余额分账记录，过期策略由账本规则统一管理。</small></div></div>
      <div class="quota-tips-item"><Sparkles :size="16" /><div><strong>可搭配签到</strong><small>初始额度叠加每日签到奖励，为运营提供灵活的组合空间。</small></div></div>
    </section>

    <div class="quota-actions">
      <button class="primary compact" :disabled="busy || loading" @click="save">
        <Save :size="16" />{{ busy ? '保存中…' : '保存额度设置' }}
      </button>
    </div>
  </main>
</template>

<style scoped>
.quota-settings-page { display: grid; gap: 16px; max-width: 1080px; }
.quota-settings-page > header h1 { margin: 2px 0 8px; font-size: 22px; }
.quota-settings-page > header .quiet { margin: 0; color: var(--text-muted); font-size: 13px; line-height: 1.6; }

.quota-hero {
  display: flex; align-items: center; justify-content: space-between; gap: 18px;
  padding: 20px 22px; border: 1px solid var(--line); border-radius: 16px;
  background: linear-gradient(120deg, var(--accent-soft), transparent 60%);
}
.quota-hero-inner { display: flex; align-items: center; gap: 16px; min-width: 0; }
.quota-hero-icon {
  width: 48px; height: 48px; flex: 0 0 48px; display: grid; place-items: center;
  border-radius: 14px; background: var(--accent); color: #fff;
}
.quota-hero-inner h2 { margin: 2px 0 6px; font-size: 18px; }
.quota-hero-inner p { margin: 0; color: var(--text-muted); font-size: 13px; line-height: 1.55; }
.quota-hero-actions { display: flex; gap: 8px; flex: 0 0 auto; }

.quota-form { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.quota-field-card {
  position: relative; padding: 22px; border: 1px solid var(--line); border-radius: 16px;
  background: var(--paper); display: flex; flex-direction: column; gap: 16px;
}
.quota-field-card > .quota-field-icon,
.quota-field-card > span.quota-field-icon {
  width: 40px; height: 40px; display: grid; place-items: center; border-radius: 12px;
  background: var(--accent-soft); color: var(--accent);
}
.quota-field-body h3 { margin: 0 0 6px; font-size: 16px; }
.quota-field-body p { margin: 0; color: var(--text-muted); font-size: 12.5px; line-height: 1.55; }
.quota-amount {
  display: inline-flex; align-items: center; gap: 6px; margin-top: auto;
  padding: 8px 12px; border: 1px solid var(--border-soft); border-radius: 12px;
  background: var(--surface);
}
.quota-currency { font-size: 18px; font-weight: 700; color: var(--text-muted); }
.quota-amount input {
  min-width: 0; flex: 1; border: 0; background: transparent; padding: 2px 0;
  font-size: 24px; font-weight: 700; color: var(--text); outline: none;
}
.quota-current { align-self: flex-end; color: var(--text-muted); font-size: 12px; }

.quota-tips { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; }
.quota-tips-item { display: flex; align-items: flex-start; gap: 10px; padding: 14px 16px; border: 1px solid var(--line); border-radius: 14px; background: var(--surface); }
.quota-tips-item > svg { flex: 0 0 auto; color: var(--accent); margin-top: 1px; }
.quota-tips-item strong { display: block; font-size: 13px; margin-bottom: 3px; }
.quota-tips-item small { color: var(--text-muted); font-size: 12px; line-height: 1.5; }

.quota-actions { display: flex; justify-content: flex-end; gap: 10px; }
.quota-actions button { height: 40px; padding: 0 20px; }

@media (max-width: 900px) {
  .quota-hero { align-items: flex-start; flex-direction: column; }
  .quota-form { grid-template-columns: 1fr; }
  .quota-tips { grid-template-columns: 1fr; }
}
</style>