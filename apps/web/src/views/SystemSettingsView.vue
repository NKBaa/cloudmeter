<script setup lang="ts">
import { onMounted, ref } from "vue";
import { SlidersHorizontal, Save, CheckCircle2, ShieldCheck, RefreshCw } from "@lucide/vue";
import { api } from "../api";
import { useSiteConfig, type SystemSettings } from "../site-config";

const { systemName, setSystemName } = useSiteConfig();
const inputName = ref("");
const loading = ref(false);
const saving = ref(false);
const message = ref("");
const error = ref("");
const updatedAt = ref("");

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const res = await api<SystemSettings>("/admin/settings/system");
    if (res && res.systemName) {
      inputName.value = res.systemName;
      setSystemName(res.systemName);
      if (res.updatedAt) updatedAt.value = new Date(res.updatedAt).toLocaleString();
    }
  } catch (err: any) {
    error.value = err.message || "加载系统设置失败";
  } finally {
    loading.value = false;
  }
}

async function save() {
  const trimmed = inputName.value.trim();
  if (!trimmed) {
    error.value = "系统名称不能为空";
    return;
  }
  saving.value = true;
  error.value = "";
  message.value = "";
  try {
    const res = await api<SystemSettings>("/admin/settings/system", {
      method: "PUT",
      body: JSON.stringify({ systemName: trimmed }),
    });
    if (res && res.systemName) {
      setSystemName(res.systemName);
      inputName.value = res.systemName;
      updatedAt.value = new Date().toLocaleString();
      message.value = "系统名称已成功保存并实时生效！";
      setTimeout(() => { message.value = ""; }, 4000);
    }
  } catch (err: any) {
    error.value = err.message || "保存失败";
  } finally {
    saving.value = false;
  }
}

onMounted(() => {
  load();
});
</script>

<template>
  <main class="workspace admin-workspace">
    <header class="page-heading">
      <div>
        <p class="eyebrow">平台设置</p>
        <h1>系统设置</h1>
        <p class="quiet">自定义平台对外名称与全局品牌标识，保存后全站顶栏、登录页及文档即刻同步更新。</p>
      </div>
    </header>

    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok flex items-center gap-2">
      <CheckCircle2 :size="16" /> {{ message }}
    </p>

    <div class="system-settings-grid">
      <!-- 主配置卡片 -->
      <section class="nextdev-card p-6">
        <div class="card-title-group">
          <span class="eyebrow">BRANDING · 品牌名称</span>
          <h3>平台名称与展示设置</h3>
        </div>

        <form class="system-form mt-4" @submit.prevent="save">
          <div class="form-group">
            <label for="system-name-input">系统名称 (System Name)</label>
            <div class="input-wrapper">
              <input
                id="system-name-input"
                v-model="inputName"
                type="text"
                maxlength="64"
                placeholder="例如：CloudMeter / 极简应用云"
                required
                :disabled="saving || loading"
              />
            </div>
            <small class="form-hint">
              用于全站 Logo 文字、控制台面包屑、邮件通知落款及页面 Title 标题。
            </small>
          </div>

          <div class="brand-preview-box mt-4">
            <span class="eyebrow">LIVE PREVIEW · 实时预览</span>
            <div class="preview-brand-item mt-2">
              <span class="brand-mark">
                <svg
                  class="brand-icon"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2.4"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <path d="M3 17 L9 7 L13 14 L21 4" />
                  <circle cx="9" cy="7" r="1.5" fill="currentColor" stroke="none" />
                  <circle cx="21" cy="4" r="1.5" fill="currentColor" stroke="none" />
                </svg>
              </span>
              <strong class="preview-name">{{ inputName || "CloudMeter" }}</strong>
            </div>
          </div>

          <div class="form-actions mt-5 flex items-center justify-between">
            <small v-if="updatedAt" class="quiet mono-data">最后更新：{{ updatedAt }}</small>
            <span v-else></span>
            <button class="primary compact" type="submit" :disabled="saving || loading">
              <Save :size="16" />
              <span>{{ saving ? "正在保存..." : "保存设置" }}</span>
            </button>
          </div>
        </form>
      </section>

      <!-- 侧边说明卡片 -->
      <aside class="nextdev-card p-6">
        <div class="card-title-group">
          <span class="eyebrow">INFO · 说明</span>
          <h3>品牌定制提示</h3>
        </div>
        <ul class="system-tips-list mt-3">
          <li>
            <ShieldCheck :size="16" class="tip-icon" />
            <div>
              <strong>全站即时生效</strong>
              <p>管理员提交修改后，客户端将通过响应式状态自动渲染新名称，无需重新编译前端代码。</p>
            </div>
          </li>
          <li>
            <RefreshCw :size="16" class="tip-icon" />
            <div>
              <strong>多端一致性</strong>
              <p>系统名称将自动注入并同步至营销落地页、用户控制台、工单邮件及管理总览。</p>
            </div>
          </li>
        </ul>
      </aside>
    </div>
  </main>
</template>

<style scoped>
.system-settings-grid {
  display: grid;
  grid-template-columns: 1fr 340px;
  gap: 24px;
  align-items: start;
  margin-top: 16px;
}
.system-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.brand-preview-box {
  padding: 16px;
  border-radius: 10px;
  background: var(--surface);
  border: 1px solid var(--border);
}
.preview-brand-item {
  display: flex;
  align-items: center;
  gap: 10px;
}
.preview-name {
  font-size: 16px;
  font-weight: 800;
  color: var(--text);
}
.brand-mark {
  width: 32px;
  height: 32px;
  display: grid;
  place-items: center;
  background: var(--accent);
  color: var(--primary-foreground);
  border-radius: 8px;
  box-shadow: 0 2px 8px var(--accent-glow);
}
.brand-icon {
  width: 18px;
  height: 18px;
}
.system-tips-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.system-tips-list li {
  display: flex;
  gap: 12px;
  align-items: flex-start;
}
.tip-icon {
  color: var(--accent);
  flex: 0 0 16px;
  margin-top: 2px;
}
.system-tips-list strong {
  display: block;
  font-size: 13.5px;
  font-weight: 600;
  color: var(--text);
  margin-bottom: 2px;
}
.system-tips-list p {
  margin: 0;
  font-size: 12.5px;
  color: var(--text-muted);
  line-height: 1.5;
}
@media (max-width: 900px) {
  .system-settings-grid {
    grid-template-columns: 1fr;
  }
}
</style>
