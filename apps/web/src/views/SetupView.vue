<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import {
  Check,
  Database,
  HardDrive,
  LoaderCircle,
  ShieldCheck,
} from "@lucide/vue";
import { api } from "../api";
import BrandMark from "../components/BrandMark.vue";

type SetupStatus = {
  initialized: boolean;
  platformName: string;
  checks: Record<string, boolean>;
};
const status = ref<SetupStatus | null>(null);
const loading = ref(true);
const submitting = ref(false);
const message = ref("");
const form = reactive({ displayName: "", email: "", password: "", systemName: "CloudMeter" });
const ready = computed(
  () => status.value && Object.values(status.value.checks).every(Boolean),
);

onMounted(async () => {
  try {
    status.value = await api<SetupStatus>("/setup/status");
    if (status.value.initialized)
      message.value = "平台已经完成初始化，请直接登录。";
  } catch (error) {
    message.value = (error as Error).message;
  } finally {
    loading.value = false;
  }
});

async function initialize() {
  submitting.value = true;
  message.value = "";
  try {
    const result = await api<{ token: string }>("/setup/initialize", {
      method: "POST",
      body: JSON.stringify(form),
    });
    localStorage.setItem("session_token", result.token);
    window.location.assign("/console");
  } catch (error) {
    message.value = (error as Error).message;
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <main class="setup-shell">
    <header><BrandMark /><span class="quiet">安全初始化</span></header>
    <div class="setup-layout">
      <section class="setup-intro">
        <p class="eyebrow">首次启动</p>
        <h1>建立你的部署控制面</h1>
        <p class="lede">连接状态确认后，创建唯一的超级管理员与平台基础配置。</p>
        <div class="checks" aria-live="polite">
          <div>
            <span class="check-icon"><Database :size="18" /></span
            ><span><b>PostgreSQL</b><small>迁移与事务存储</small></span
            ><Check v-if="status?.checks.database" class="ok" :size="18" />
          </div>
          <div>
            <span class="check-icon"><HardDrive :size="18" /></span
            ><span><b>数据库结构</b><small>核心约束已加载</small></span
            ><Check v-if="status?.checks.migrations" class="ok" :size="18" />
          </div>
          <div>
            <span class="check-icon"><ShieldCheck :size="18" /></span
            ><span><b>隔离策略</b><small>单入口与后端权限</small></span
            ><Check v-if="ready" class="ok" :size="18" />
          </div>
        </div>
      </section>
      <section class="form-panel">
        <div class="panel-heading">
          <span class="step">01</span>
          <div>
            <h2>平台与管理员</h2>
            <p>初始化完成后，此入口将永久关闭。</p>
          </div>
        </div>
        <form @submit.prevent="initialize">
          <label
            >系统名称<input
              v-model="form.systemName"
              required
              maxlength="64"
              placeholder="例如：CloudMeter"
          /></label>
          <label
            >管理员姓名<input
              v-model="form.displayName"
              required
              maxlength="80"
              autocomplete="name"
          /></label>
          <label
            >管理员邮箱<input
              v-model="form.email"
              required
              type="email"
              autocomplete="email"
          /></label>
          <label
            >管理员密码<input
              v-model="form.password"
              required
              maxlength="128"
              type="password"
              autocomplete="new-password"
            /></label
          >
          <p v-if="message" class="message">{{ message }}</p>
          <button
            class="primary"
            :disabled="loading || submitting || !ready || status?.initialized"
          >
            <LoaderCircle
              v-if="submitting"
              class="spin"
              :size="18"
            /><ShieldCheck v-else :size="18" />{{
              status?.initialized ? "已完成初始化" : "完成安全初始化"
            }}
          </button>
        </form>
      </section>
    </div>
  </main>
</template>
