<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { CalendarCheck, Clock, Gift, Save, TrendingUp, Users } from '@lucide/vue'
import { api } from '../api'

const model = reactive({ enabled: true, minRewardCents: 1, maxRewardCents: 10, updatedAt: '' })
const busy = ref(false), loading = ref(true), error = ref(''), message = ref('')

const formatYuan = (cents: number) => `¥${(cents / 100).toFixed(2)}`

const rewardRange = computed(() => {
  const min = model.minRewardCents
  const max = model.maxRewardCents
  return `${formatYuan(min)} ~ ${formatYuan(max)}`
})

const avgReward = computed(() => {
  return Math.round((model.minRewardCents + model.maxRewardCents) / 2)
})

async function load() {
  loading.value = true
  try { Object.assign(model, await api('/admin/settings/checkin')) }
  catch (e) { error.value = (e as Error).message }
  finally { loading.value = false }
}

async function save() {
  busy.value = true; error.value = ''; message.value = ''
  try {
    await api('/admin/settings/checkin', { method: 'PUT', body: JSON.stringify({ enabled: model.enabled, minRewardCents: model.minRewardCents, maxRewardCents: model.maxRewardCents }) })
    message.value = '签到设置已保存'
  } catch (e) { error.value = (e as Error).message }
  finally { busy.value = false }
}

onMounted(load)
</script>
<template>
  <main class="workspace admin-workspace checkin-settings-page">
    <header>
      <div>
        <p class="eyebrow">用户运营</p>
        <h1>签到设置</h1>
        <p class="quiet">配置每日签到功能的开关与奖励范围，用户按北京时间签到一次，奖励实时写入不可变赠送账本。</p>
      </div>
    </header>
    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok">{{ message }}</p>

    <section class="checkin-hero">
      <div class="checkin-hero-inner">
        <span class="checkin-hero-icon"><CalendarCheck :size="26" /></span>
        <div>
          <p class="eyebrow">每日签到</p>
          <h2>用持续的签到习惯激励用户</h2>
          <p>每天北京时间零点重置，用户签到后随机获得区间内奖励，直接进入不可篡改的赠送账本。</p>
        </div>
      </div>
      <div class="checkin-hero-meta" v-if="!loading">
        <div class="checkin-meta-chip"><Clock :size="15" /><div><strong>重置周期</strong><span>北京时间 00:00</span></div></div>
        <div class="checkin-meta-chip"><Gift :size="15" /><div><strong>奖励区间</strong><span>{{ rewardRange }}</span></div></div>
      </div>
    </section>

    <section class="checkin-panel" v-if="!loading">
      <div class="checkin-panel-head">
        <span class="checkin-panel-icon"><CalendarCheck :size="20" /></span>
        <div><p class="eyebrow">签到规则</p><h2>功能与奖励配置</h2></div>
      </div>
      <form class="checkin-form" @submit.prevent="save">
        <div class="checkin-toggle-row">
          <div class="checkin-toggle-row-text">
            <strong>允许用户每日签到</strong>
            <small>关闭后签到入口对用户隐藏，历史奖励记录不受影响，用户仍可查看已获得的奖励。</small>
          </div>
          <label class="switch"><input v-model="model.enabled" type="checkbox" /><span /></label>
        </div>

        <div class="checkin-fields">
          <label class="checkin-field">
            <span>最低奖励</span>
            <div class="checkin-cents-input">
              <span class="checkin-cents-currency">¥</span>
              <input v-model.number="model.minRewardCents" type="number" min="1" :max="model.maxRewardCents" step="1" required />
            </div>
            <em>每次签到至少获得 {{ formatYuan(model.minRewardCents) }}</em>
          </label>
          <label class="checkin-field">
            <span>最高奖励</span>
            <div class="checkin-cents-input">
              <span class="checkin-cents-currency">¥</span>
              <input v-model.number="model.maxRewardCents" type="number" :min="model.minRewardCents" max="10000" step="1" required />
            </div>
            <em>每次签到至多获得 {{ formatYuan(model.maxRewardCents) }}</em>
          </label>
        </div>

        <div class="checkin-actions">
          <button class="primary compact" :disabled="busy">
            <Save :size="16" />{{ busy ? '保存中…' : '保存设置' }}
          </button>
        </div>
      </form>
    </section>

    <div class="checkin-strip" v-if="!loading">
      <div class="checkin-strip-item">
        <span class="checkin-strip-icon"><TrendingUp :size="18" /></span>
        <div>
          <strong>奖励区间</strong>
          <span>每次签到随机获得 {{ rewardRange }}</span>
        </div>
      </div>
      <div class="checkin-strip-item">
        <span class="checkin-strip-icon"><Gift :size="18" /></span>
        <div>
          <strong>平均奖励</strong>
          <span>约 {{ formatYuan(avgReward) }} / 次</span>
        </div>
      </div>
      <div class="checkin-strip-item">
        <span class="checkin-strip-icon"><Users :size="18" /></span>
        <div>
          <strong>用户可见</strong>
          <span>{{ model.enabled ? '已开启，用户可在控制台签到' : '已关闭，用户无法签到' }}</span>
        </div>
      </div>
    </div>
  </main>
</template>

<style scoped>
.checkin-settings-page { display: grid; gap: 16px; max-width: 1080px; }
.checkin-settings-page > header h1 { margin: 2px 0 8px; font-size: 22px; }
.checkin-settings-page > header .quiet { margin: 0; color: var(--text-muted); font-size: 13px; line-height: 1.6; }

.checkin-hero {
  display: flex; align-items: center; justify-content: space-between; gap: 18px;
  padding: 20px 22px; border: 1px solid var(--line); border-radius: 16px;
  background: linear-gradient(120deg, var(--accent-soft), transparent 60%);
}
.checkin-hero-inner { display: flex; align-items: center; gap: 16px; min-width: 0; }
.checkin-hero-icon {
  width: 48px; height: 48px; flex: 0 0 48px; display: grid; place-items: center;
  border-radius: 14px; background: var(--accent); color: #fff;
}
.checkin-hero-inner h2 { margin: 2px 0 6px; font-size: 18px; }
.checkin-hero-inner p { margin: 0; color: var(--text-muted); font-size: 13px; line-height: 1.55; }
.checkin-hero-meta { display: flex; gap: 8px; flex: 0 0 auto; }
.checkin-meta-chip {
  display: flex; align-items: center; gap: 8px; padding: 10px 14px;
  border: 1px solid var(--border-soft); border-radius: 12px; background: var(--surface);
}
.checkin-meta-chip strong { display: block; font-size: 13px; }
.checkin-meta-chip span { color: var(--text-muted); font-size: 12px; }

.checkin-panel { background: var(--paper); border: 1px solid var(--line); border-radius: 16px; padding: 24px; }
.checkin-panel-head { display: flex; align-items: center; gap: 12px; margin-bottom: 22px; }
.checkin-panel-icon {
  width: 40px; height: 40px; display: grid; place-items: center; border-radius: 12px;
  background: var(--accent-soft); color: var(--accent);
}
.checkin-panel-head p { margin: 0; font-size: 11px; letter-spacing: .04em; text-transform: uppercase; color: var(--muted); }
.checkin-panel-head h2 { margin: 2px 0 0; font-size: 18px; }

.checkin-form { display: grid; gap: 22px; }
.checkin-toggle-row { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 12px 0; }
.checkin-toggle-row-text strong { display: block; font-size: 14px; margin-bottom: 3px; }
.checkin-toggle-row-text small { color: var(--text-muted); font-size: 12px; line-height: 1.5; }

.checkin-fields { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.checkin-field { display: grid; gap: 8px; font-size: 13px; font-weight: 650; }
.checkin-field em { font-style: normal; color: var(--text-muted); font-size: 12px; font-weight: 400; }
.checkin-cents-input {
  display: flex; align-items: center; gap: 6px; padding: 6px 12px;
  border: 1px solid var(--border-soft); border-radius: 12px; background: var(--surface);
}
.checkin-cents-currency { font-size: 18px; font-weight: 700; color: var(--text-muted); }
.checkin-cents-input input { flex: 1; min-width: 0; border: 0; background: transparent; padding: 4px 0; font-size: 20px; font-weight: 700; color: var(--text); outline: none; }

.checkin-actions { display: flex; justify-content: flex-end; padding-top: 4px; }
.checkin-actions button { height: 40px; padding: 0 18px; }

.checkin-strip { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
.checkin-strip-item {
  display: flex; align-items: center; gap: 12px; padding: 16px 18px;
  border: 1px solid var(--line); border-radius: 14px; background: var(--surface);
}
.checkin-strip-icon { flex: 0 0 auto; color: var(--accent); }
.checkin-strip-item strong { display: block; font-size: 13px; margin-bottom: 2px; }
.checkin-strip-item span { color: var(--text-muted); font-size: 12px; }

@media (max-width: 800px) {
  .checkin-hero { align-items: flex-start; flex-direction: column; }
  .checkin-hero-meta { width: 100%; }
  .checkin-fields { grid-template-columns: 1fr; }
  .checkin-strip { grid-template-columns: 1fr; }
}
</style>