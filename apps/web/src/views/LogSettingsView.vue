<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { AlertTriangle, Clock, Database, RefreshCw, Save, ScrollText, Trash2 } from '@lucide/vue'
import { api } from '../api'

const model = ref({ retentionHours: 168, retentionBytes: 1048576 })
const busy = ref(false), loading = ref(true), clearing = ref(false), error = ref(''), message = ref('')

const options = [
  { value: 65536, label: '64 KiB' },
  { value: 262144, label: '256 KiB' },
  { value: 1048576, label: '1 MiB' },
  { value: 4194304, label: '4 MiB' },
  { value: 16777216, label: '16 MiB' },
]

function humanize(bytes: number) {
  if (bytes >= 1024 ** 3) return (bytes / 1024 ** 3).toFixed(1) + ' GiB'
  if (bytes >= 1024 ** 2) return (bytes / 1024 ** 2).toFixed(1) + ' MiB'
  return Math.round(bytes / 1024) + ' KiB'
}

const retentionSummary = computed(() => {
  const h = model.value.retentionHours
  if (h < 24) return `${h} 小时`
  if (h % 24 === 0) return `${h / 24} 天`
  return `${h} 小时（约 ${(h / 24).toFixed(1)} 天）`
})

const spaceTier = computed(() => {
  const b = model.value.retentionBytes
  return b >= 16777216 ? '宽松' : b >= 4194304 ? '平衡' : b >= 1048576 ? '标准' : '紧凑'
})

async function load() {
  loading.value = true
  try { Object.assign(model.value, await api('/admin/settings/logs')) }
  catch (e) { error.value = (e as Error).message }
  finally { loading.value = false }
}

async function save() {
  busy.value = true; error.value = ''; message.value = ''
  try {
    await api('/admin/settings/logs', { method: 'PUT', body: JSON.stringify(model.value) })
    message.value = '日志保留设置已保存'
  } catch (e) { error.value = (e as Error).message }
  finally { busy.value = false }
}

async function clearAll() {
  if (!confirm('确认清空全部已缓存的实例运行日志？此操作立即生效且不可恢复。')) return
  try {
    clearing.value = true
    await api('/admin/logs/clear', { method: 'POST', body: '{}' })
    message.value = '已清空全部缓存日志'
    error.value = ''
  } catch (e) { error.value = (e as Error).message }
  finally { clearing.value = false }
}

onMounted(load)
</script>
<template>
  <main class="workspace admin-workspace log-settings-page">
    <header>
      <div>
        <p class="eyebrow">系统设置</p>
        <h1>日志设置</h1>
        <p class="quiet">统一管理应用运行日志在平台侧的缓存保留策略，控制磁盘占用与分析窗口。</p>
      </div>
    </header>
    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok">{{ message }}</p>

    <section class="log-hero">
      <div class="log-hero-inner">
        <span class="log-hero-icon"><ScrollText :size="26" /></span>
        <div>
          <p class="eyebrow">运行日志缓存</p>
          <h2>保留多久、多大，由你掌控</h2>
          <p>日志按实例缓存，超过保留时长或单条上限的旧内容自动裁剪，避免长期累积占用磁盘。</p>
        </div>
      </div>
      <div class="log-hero-meta">
        <div class="log-meta-chip"><Clock :size="15" /><div><strong>缓存清理</strong><span>超时或超限后按行自动裁剪</span></div></div>
      </div>
    </section>

    <section class="log-panel" v-if="!loading">
      <div class="log-panel-head">
        <span class="log-panel-icon"><Database :size="20" /></span>
        <div><p class="eyebrow">保留策略</p><h2>日志缓存规则</h2></div>
      </div>
      <form class="log-form" @submit.prevent="save">
        <label class="log-field">
          <span>保留时长</span>
          <div class="log-input-with-unit">
            <input v-model.number="model.retentionHours" type="number" min="1" max="8760" step="1" required />
            <span class="log-unit">小时</span>
          </div>
          <em>超过该时长未更新的日志自动清除，当前约 {{ retentionSummary }}</em>
        </label>

        <label class="log-field">
          <span>单条日志上限</span>
          <select v-model.number="model.retentionBytes">
            <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
          </select>
          <em>超过上限时仅保留最近 {{ humanize(model.retentionBytes) }}</em>
        </label>

        <div class="log-actions">
          <button class="primary compact" :disabled="busy">
            <Save :size="16" />{{ busy ? '保存中…' : '保存设置' }}
          </button>
        </div>
      </form>
    </section>

    <div class="log-summary-strip" v-if="!loading">
      <div class="log-summary-item"><span>保留时长</span><strong>{{ retentionSummary }}</strong></div>
      <div class="log-summary-item"><span>单条上限</span><strong>{{ humanize(model.retentionBytes) }}</strong></div>
      <div class="log-summary-item"><span>空间策略</span><strong>{{ spaceTier }}</strong></div>
    </div>

    <section class="log-danger-panel">
      <div class="log-danger">
        <span class="log-danger-icon"><AlertTriangle :size="20" /></span>
        <div><strong>清空全部缓存日志</strong><p>移除所有实例的运行日志缓存，磁盘占用立即释放。此操作不可恢复。</p></div>
        <button type="button" class="danger compact" :disabled="clearing" @click="clearAll">
          <Trash2 :size="15" />{{ clearing ? '清理中…' : '清空缓存' }}
        </button>
      </div>
    </section>
  </main>
</template>

<style scoped>
.log-settings-page { display: grid; gap: 16px; max-width: 1080px; }
.log-settings-page > header h1 { margin: 2px 0 8px; font-size: 22px; }
.log-settings-page > header .quiet { margin: 0; color: var(--text-muted); font-size: 13px; line-height: 1.6; }

.log-hero {
  display: flex; align-items: center; justify-content: space-between; gap: 18px;
  padding: 20px 22px; border: 1px solid var(--line); border-radius: 16px;
  background: linear-gradient(120deg, var(--accent-soft), transparent 60%);
}
.log-hero-inner { display: flex; align-items: center; gap: 16px; min-width: 0; }
.log-hero-icon {
  width: 48px; height: 48px; flex: 0 0 48px; display: grid; place-items: center;
  border-radius: 14px; background: var(--accent); color: #fff;
}
.log-hero-inner h2 { margin: 2px 0 6px; font-size: 18px; }
.log-hero-inner p { margin: 0; color: var(--text-muted); font-size: 13px; line-height: 1.55; }
.log-hero-meta { flex: 0 0 auto; }
.log-meta-chip {
  display: flex; align-items: center; gap: 8px; padding: 10px 14px;
  border: 1px solid var(--border-soft); border-radius: 12px; background: var(--surface);
}
.log-meta-chip strong { display: block; font-size: 13px; }
.log-meta-chip span { color: var(--text-muted); font-size: 12px; }

.log-panel { background: var(--paper); border: 1px solid var(--line); border-radius: 16px; padding: 24px; }
.log-panel-head { display: flex; align-items: center; gap: 12px; margin-bottom: 22px; }
.log-panel-icon {
  width: 40px; height: 40px; display: grid; place-items: center; border-radius: 12px;
  background: var(--accent-soft); color: var(--accent);
}
.log-panel-head p { margin: 0; font-size: 11px; letter-spacing: .04em; text-transform: uppercase; color: var(--muted); }
.log-panel-head h2 { margin: 2px 0 0; font-size: 18px; }

.log-form { display: grid; grid-template-columns: 1fr 1fr auto; gap: 20px; align-items: end; }
.log-field { display: grid; gap: 8px; font-size: 14px; font-weight: 650; }
.log-field em { font-style: normal; color: var(--text-muted); font-size: 12px; line-height: 1.4; }
.log-input-with-unit { display: flex; align-items: center; gap: 8px; }
.log-input-with-unit input { flex: 1; min-width: 0; font-size: 16px; }
.log-unit { color: var(--text-muted); font-size: 13px; white-space: nowrap; }
.log-field select { font-size: 15px; }
.log-actions { display: flex; align-items: flex-end; }
.log-actions button { height: 42px; padding: 0 18px; }

.log-summary-strip {
  display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px;
}
.log-summary-item {
  padding: 16px 18px; border: 1px solid var(--line); border-radius: 14px;
  background: var(--surface); display: grid; gap: 5px;
}
.log-summary-item span { color: var(--text-muted); font-size: 12px; }
.log-summary-item strong { font-size: 16px; }

.log-danger-panel {
  border: 1px solid var(--line); border-radius: 16px; padding: 18px 22px;
  background: var(--paper);
}
.log-danger { display: flex; align-items: center; gap: 14px; }
.log-danger-icon {
  width: 40px; height: 40px; flex: 0 0 40px; display: grid; place-items: center;
  border-radius: 12px; background: rgba(185,47,47,.12); color: #b72f2f;
}
.log-danger strong { display: block; font-size: 14px; margin-bottom: 3px; }
.log-danger p { margin: 0; color: var(--text-muted); font-size: 12.5px; line-height: 1.55; }
.log-danger button { flex: 0 0 auto; }

@media (max-width: 800px) {
  .log-hero { align-items: flex-start; flex-direction: column; }
  .log-form { grid-template-columns: 1fr; }
  .log-actions button { width: 100%; }
  .log-summary-strip { grid-template-columns: 1fr; }
  .log-danger { align-items: flex-start; flex-direction: column; }
  .log-danger button { width: 100%; }
}
</style>