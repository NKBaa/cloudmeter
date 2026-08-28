<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Save, CheckCircle2, RotateCcw } from "@lucide/vue";
import { api } from "../api";
import { useSiteConfig, type SystemSettings } from "../site-config";

const { setSystemName } = useSiteConfig();
const form = ref<SystemSettings>({
  systemName: "",
  serverUrl: "",
  appBaseDomain: "",
  logoUrl: "",
  footerText: "",
  aboutContent: "",
  homepageContent: "",
  termsOfService: "",
  privacyPolicy: "",
});

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
    if (res) {
      Object.assign(form.value, res);
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
  const trimmedName = form.value.systemName.trim();
  if (!trimmedName) {
    error.value = "系统名称不能为空";
    return;
  }
  form.value.systemName = trimmedName;

  saving.value = true;
  error.value = "";
  message.value = "";
  try {
    const payload = { ...form.value };
    delete payload.updatedAt;
    const res = await api<SystemSettings>("/admin/settings/system", {
      method: "PUT",
      body: JSON.stringify(payload),
    });
    if (res && res.systemName) {
      setSystemName(res.systemName);
      updatedAt.value = new Date().toLocaleString();
      message.value = "保存成功";
      setTimeout(() => { message.value = ""; }, 3000);
    }
  } catch (err: any) {
    error.value = err.message || "保存失败";
  } finally {
    saving.value = false;
  }
}

function reset() {
  load();
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
        <p class="quiet">自定义平台对外名称、品牌标识、页面展示内容及法务声明。</p>
      </div>
      <div class="actions flex gap-2">
        <button class="secondary compact" @click="reset" :disabled="loading || saving">
          <RotateCcw :size="16" />重置
        </button>
        <button class="primary compact" @click="save" :disabled="loading || saving">
          <Save :size="16" />{{ saving ? "保存中..." : "保存更改" }}
        </button>
      </div>
    </header>

    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok flex items-center gap-2">
      <CheckCircle2 :size="16" /> {{ message }}
    </p>

    <div v-if="!loading" class="flex flex-col gap-6 mt-4">
      <!-- 基础信息 -->
      <section class="nextdev-card p-0">
        <div class="card-header-bar">
          <div class="card-title-group">
            <span class="eyebrow">BASIC · 基础信息</span>
            <h3>系统基本信息</h3>
          </div>
        </div>
        <div class="card-divider"></div>
        <div class="p-6 flex flex-col">
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <div class="log-field">
              <label>系统名称</label>
              <input v-model="form.systemName" placeholder="请输入系统名称" maxlength="64" />
              <em>在整个应用程序中显示的名称</em>
            </div>
            <div class="log-field">
              <label>服务器地址</label>
              <input v-model="form.serverUrl" placeholder="例如：https://cloud.example.com" />
              <em>主系统的公开访问地址，用于应用子域名安全返回控制台；不参与应用计费</em>
            </div>
          </div>
          <div class="log-field mt-5">
            <label>徽标 URL</label>
            <input v-model="form.logoUrl" placeholder="您的徽标图片 URL（可选）" />
            <em>自定义您的平台展示 Logo</em>
          </div>
        </div>
      </section>

      <!-- 路由与代理设置 -->
      <section class="nextdev-card p-0">
        <div class="card-header-bar">
          <div class="card-title-group">
            <span class="eyebrow">NETWORK · 路由与网络</span>
            <h3>反向代理服务设置</h3>
          </div>
        </div>
        <div class="card-divider"></div>
        <div class="p-6 flex flex-col">
          <div class="log-field">
            <label>应用泛子域名 (App Base Domain)</label>
            <input v-model="form.appBaseDomain" placeholder="例如：apps.example.com" />
            <em>独立于服务器地址，为每个应用分配专属子域名（例如 app-user.apps.example.com）。必须将 *.apps.example.com 泛解析到当前服务器；不参与应用计费。</em>
          </div>
        </div>
      </section>

      <!-- 定制内容 -->
      <section class="nextdev-card p-0">
        <div class="card-header-bar">
          <div class="card-title-group">
            <span class="eyebrow">CONTENT · 页面定制</span>
            <h3>首页与页面内容</h3>
          </div>
        </div>
        <div class="card-divider"></div>
        <div class="p-6 flex flex-col gap-6">
          <div class="log-field">
            <label>前台首页内容</label>
            <textarea v-model="form.homepageContent" rows="6" placeholder="输入自定义的前台首页 HTML 富文本内容..."></textarea>
            <em>自定义前台首页 HTML 富文本展示内容，用户访问未登录主页时可见。</em>
          </div>

          <div class="log-field">
            <label>关于我们</label>
            <textarea v-model="form.aboutContent" rows="3" placeholder="输入安全 HTML 内容或完整 URL"></textarea>
            <em>用于主系统首页的品牌介绍；填写完整 URL 时展示为外部“了解我们”链接。</em>
          </div>

          <div class="log-field">
            <label>全局页脚</label>
            <textarea v-model="form.footerText" rows="2" placeholder="例如：© 2025 某某科技有限公司 保留所有权利。"></textarea>
            <em>显示在各个公开页面底部的页脚文本</em>
          </div>
        </div>
      </section>

      <!-- 法务声明 -->
      <section class="nextdev-card p-0">
        <div class="card-header-bar">
          <div class="card-title-group">
            <span class="eyebrow">LEGAL · 法务声明</span>
            <h3>协议与政策</h3>
          </div>
        </div>
        <div class="card-divider"></div>
        <div class="p-6 flex flex-col gap-6">
          <div class="log-field">
            <label>用户协议 (Terms of Service)</label>
            <textarea v-model="form.termsOfService" rows="8" placeholder="输入 Markdown 或 HTML 格式的用户协议内容..."></textarea>
            <em>留空以禁用协议要求。支持 Markdown、HTML 或重定向用户的完整 URL。</em>
          </div>

          <div class="log-field">
            <label>隐私政策 (Privacy Policy)</label>
            <textarea v-model="form.privacyPolicy" rows="8" placeholder="# 隐私政策..."></textarea>
            <em>支持 Markdown、HTML 或用于重定向用户的完整 URL。</em>
          </div>
        </div>
      </section>
    </div>
  </main>
</template>

<style scoped>
.page-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
}
.actions {
  margin-top: 10px;
}
.log-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.log-field label {
  font-size: 14px;
  font-weight: 650;
  color: var(--text);
}
.log-field input, .log-field textarea {
  width: 100%;
  background: var(--paper);
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 10px 14px;
  font-family: inherit;
  color: var(--text);
  font-size: 14px;
  transition: border-color 0.2s;
}
.log-field input:focus, .log-field textarea:focus {
  outline: none;
  border-color: var(--accent);
}
.log-field em {
  font-style: normal;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.4;
}
.flex {
  display: flex;
}
.flex-col {
  flex-direction: column;
}
.items-center {
  align-items: center;
}
.justify-between {
  justify-content: space-between;
}
.gap-2 {
  gap: 8px;
}
.gap-6 {
  gap: 24px;
}
.mt-4 {
  margin-top: 16px;
}
.mt-5 {
  margin-top: 20px;
}
.grid {
  display: grid;
}
.grid-cols-1 {
  grid-template-columns: minmax(0, 1fr);
}
@media (min-width: 768px) {
  .md\:grid-cols-2 {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
