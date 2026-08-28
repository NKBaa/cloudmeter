<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import {
  Activity,
  ArrowDown,
  ArrowUp,
  Cpu,
  HardDrive,
  MemoryStick,
  RefreshCw,
} from "@lucide/vue";
import { api } from "../api";
type Section = { error: string };
type Metrics = {
  cpu: Section & { usagePercent: number | null };
  memory: Section & {
    totalBytes: number | null;
    usedBytes: number | null;
    availableBytes: number | null;
  };
  disk: Section & {
    totalBytes: number | null;
    usedBytes: number | null;
    availableBytes: number | null;
  };
  network: Section & {
    rxBytes: number | null;
    txBytes: number | null;
    rxBytesPerSecond: number | null;
    txBytesPerSecond: number | null;
  };
  sampledAt: string;
};
const data = ref<Metrics | null>(null),
  loading = ref(true),
  error = ref("");
let timer = 0;
const stale = computed(
  () =>
    !data.value ||
    Date.now() - new Date(data.value.sampledAt).getTime() > 20000,
);
function bytes(value: number | null | undefined) {
  if (value == null) return "--";
  if (value < 1024) return value.toFixed(0) + " B";
  if (value < 1024 ** 2) return (value / 1024).toFixed(1) + " KiB";
  if (value < 1024 ** 3) return (value / 1024 ** 2).toFixed(1) + " MiB";
  return (value / 1024 ** 3).toFixed(2) + " GiB";
}
function percent(used: number | null, total: number | null) {
  return used != null && total ? Math.min(100, (used / total) * 100) : 0;
}
async function load() {
  try {
    data.value = await api<Metrics>("/admin/host-metrics");
    error.value = "";
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    loading.value = false;
  }
}
onMounted(async () => {
  await load();
  timer = window.setInterval(load, 5000);
});
onBeforeUnmount(() => window.clearInterval(timer));
</script>
<template>
  <main class="workspace admin-workspace metrics-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">基础设施</p>
        <h1>性能监控</h1>
        <p class="quiet">宿主机资源与 Docker 数据盘，每 5 秒自动刷新。</p>
      </div>
      <button class="secondary compact" :disabled="loading" @click="load">
        <RefreshCw :class="{ spin: loading }" :size="16" />刷新
      </button>
    </header>
    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="stale && !loading" class="configuration-status blocked">
      采样数据已超过 20 秒，Worker 可能暂时不可用。
    </p>
    <section v-if="loading && !data" class="host-metric-grid" aria-busy="true">
      <article v-for="index in 4" :key="index" class="metric-skeleton-card">
        <span class="skeleton skeleton-text" style="width: 96px"></span>
        <span class="skeleton skeleton-title" style="width: 70%"></span>
        <span class="skeleton skeleton-text" style="width: 50%"></span>
      </article>
    </section>
    <section v-else-if="data" class="host-metric-grid">
      <article class="host-metric-card">
        <div class="card-head">
          <Cpu :size="18" />
          <span>CPU 使用率</span>
        </div>
        <div class="card-metric">
          <strong>{{
            data.cpu.error
              ? "--"
              : Number(data.cpu.usagePercent || 0).toFixed(1) + "%"
          }}</strong>
        </div>
        <div class="card-bar">
          <i><em :style="{ width: (data.cpu.usagePercent || 0) + '%' }"></em></i>
        </div>
        <small class="card-footer">{{ data.cpu.error || "全部宿主机核心的瞬时平均值" }}</small>
      </article>

      <article class="host-metric-card">
        <div class="card-head">
          <MemoryStick :size="18" />
          <span>内存使用</span>
        </div>
        <div class="card-metric">
          <strong>{{ bytes(data.memory.usedBytes) }}</strong>
          <span class="card-metric-sub">/ {{ bytes(data.memory.totalBytes) }}</span>
        </div>
        <div class="card-bar">
          <i
            ><em
              :style="{
                width:
                  percent(data.memory.usedBytes, data.memory.totalBytes) + '%',
              }"
            ></em
          ></i>
        </div>
        <small class="card-footer">{{
          data.memory.error || "可用 " + bytes(data.memory.availableBytes)
        }}</small>
      </article>

      <article class="host-metric-card">
        <div class="card-head">
          <HardDrive :size="18" />
          <span>Docker 数据盘</span>
        </div>
        <div class="card-metric">
          <strong>{{ bytes(data.disk.usedBytes) }}</strong>
          <span class="card-metric-sub">/ {{ bytes(data.disk.totalBytes) }}</span>
        </div>
        <div class="card-bar">
          <i
            ><em
              :style="{
                width: percent(data.disk.usedBytes, data.disk.totalBytes) + '%',
              }"
            ></em
          ></i>
        </div>
        <small class="card-footer">{{
          data.disk.error || "剩余 " + bytes(data.disk.availableBytes)
        }}</small>
      </article>

      <article class="host-metric-card">
        <div class="card-head">
          <Activity :size="18" />
          <span>宿主机网络</span>
        </div>
        <div class="network-rates">
          <span class="rate-rx">
            <ArrowDown :size="16" />{{ bytes(data.network.rxBytesPerSecond) }}/s
          </span>
          <span class="rate-tx">
            <ArrowUp :size="16" />{{ bytes(data.network.txBytesPerSecond) }}/s
          </span>
        </div>
        <div class="card-bar empty-bar"></div>
        <small class="card-footer">{{
          data.network.error ||
          "累计接收 " +
            bytes(data.network.rxBytes) +
            " · 发送 " +
            bytes(data.network.txBytes)
        }}</small>
      </article>
    </section>
    <footer v-if="data" class="metrics-footer">
      <small class="quiet">
        最近采样：{{ new Date(data.sampledAt).toLocaleString("zh-CN") }}
      </small>
    </footer>
  </main>
</template>

<style scoped>
.metrics-page {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.metrics-page > header {
  margin-bottom: 0;
}

.host-metric-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
}

.host-metric-card,
.metric-skeleton-card {
  padding: 20px 22px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--paper);
  box-shadow: var(--shadow-xs);
  display: flex;
  flex-direction: column;
  gap: 10px;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-muted);
}
.card-head svg {
  color: var(--accent);
}

.card-metric {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin: 4px 0 2px;
}
.card-metric strong {
  font-size: 24px;
  font-weight: 800;
  color: var(--text);
  letter-spacing: -0.02em;
}
.card-metric-sub {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-muted);
}

.card-bar {
  margin: 2px 0 4px;
}
.card-bar.empty-bar {
  height: 6px;
}
.card-bar i {
  display: block;
  height: 6px;
  border-radius: 999px;
  background: var(--field);
  border: 1px solid var(--line);
  overflow: hidden;
}
.card-bar i em {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--accent);
  transition: width 0.35s ease;
}

.card-footer {
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.5;
  margin-top: auto;
}

.network-rates {
  display: flex;
  align-items: center;
  gap: 20px;
  font-size: 18px;
  font-weight: 700;
  margin: 6px 0 4px;
}
.network-rates .rate-rx {
  display: flex;
  align-items: center;
  gap: 6px;
  color: #34d399;
}
.network-rates .rate-tx {
  display: flex;
  align-items: center;
  gap: 6px;
  color: #60a5fa;
}

.metrics-footer {
  margin-top: 4px;
}
.metrics-footer small {
  color: var(--text-muted);
  font-size: 12px;
}

@media (max-width: 768px) {
  .host-metric-grid {
    grid-template-columns: 1fr;
  }
}
</style>
