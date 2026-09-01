<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Save, CheckCircle2, RotateCcw, KeyRound, Copy, Trash2, BookOpen } from "@lucide/vue";
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
type LLMAPIKeyStatus = { configured: boolean; name?: string; prefix?: string; createdAt?: string; lastUsedAt?: string };
const llmKeyStatus = ref<LLMAPIKeyStatus>({ configured: false });
const llmKeyName = ref("默认大模型密钥");
const generatedLLMKey = ref("");
const llmKeyBusy = ref(false);
const llmKeyMessage = ref("");

async function loadLLMKey() {
  try {
    llmKeyStatus.value = await api<LLMAPIKeyStatus>("/admin/settings/llm-api-key");
    if (llmKeyStatus.value.name) llmKeyName.value = llmKeyStatus.value.name;
  } catch (err: any) {
    error.value = err.message || "加载大模型 API 密钥状态失败";
  }
}

async function rotateLLMKey() {
  llmKeyBusy.value = true;
  error.value = "";
  llmKeyMessage.value = "";
  try {
    const result = await api<{ key: string; status: LLMAPIKeyStatus }>("/admin/settings/llm-api-key/rotate", {
      method: "POST", body: JSON.stringify({ name: llmKeyName.value.trim() }),
    });
    generatedLLMKey.value = result.key;
    llmKeyStatus.value = result.status;
    llmKeyMessage.value = "新密钥已生成，离开页面后将无法再次查看明文。";
  } catch (err: any) { error.value = err.message || "生成密钥失败"; }
  finally { llmKeyBusy.value = false; }
}

async function copyLLMKey() {
  if (!generatedLLMKey.value) return;
  await navigator.clipboard.writeText(generatedLLMKey.value);
  llmKeyMessage.value = "密钥已复制到剪贴板。";
}

async function revokeLLMKey() {
  if (!window.confirm("撤销后，正在使用该密钥的模型调用将立即失败。确认撤销？")) return;
  llmKeyBusy.value = true;
  error.value = "";
  try {
    await api<void>("/admin/settings/llm-api-key", { method: "DELETE" });
    llmKeyStatus.value = { configured: false };
    generatedLLMKey.value = "";
    llmKeyMessage.value = "密钥已撤销。";
  } catch (err: any) { error.value = err.message || "撤销密钥失败"; }
  finally { llmKeyBusy.value = false; }
}

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
    // Website ingress is edited on its own page. Read the current values before
    // saving so an older System Settings tab cannot overwrite newer routing data.
    const current = await api<SystemSettings>("/admin/settings/system");
    const payload = {
      ...form.value,
      serverUrl: current.serverUrl,
      appBaseDomain: current.appBaseDomain,
    };
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

onMounted(() => { Promise.all([load(), loadLLMKey()]); });
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
          <div class="log-field">
            <label>系统名称</label>
            <input v-model="form.systemName" placeholder="请输入系统名称" maxlength="64" />
            <em>在整个应用程序中显示的名称</em>
          </div>
          <div class="log-field mt-5">
            <label>徽标 URL</label>
            <input v-model="form.logoUrl" placeholder="您的徽标图片 URL（可选）" />
            <em>自定义您的平台展示 Logo</em>
          </div>
        </div>
      </section>

      <section class="nextdev-card p-0 llm-api-card">
        <div class="card-header-bar">
          <div class="card-title-group"><span class="eyebrow">LLM API · 模型接入</span><h3>大模型 API 与鉴权密钥</h3></div>
          <span :class="['llm-key-state', llmKeyStatus.configured ? 'ready' : 'idle']">{{ llmKeyStatus.configured ? "已启用" : "未配置" }}</span>
        </div>
        <div class="card-divider"></div>
        <div class="llm-api-layout">
          <div class="llm-key-panel">
            <div class="llm-key-heading"><KeyRound :size="19" /><div><strong>机器访问密钥</strong><p>仅超级管理员可生成、轮换或撤销。明文只显示一次。</p></div></div>
            <label class="log-field"><span>密钥名称</span><input v-model="llmKeyName" maxlength="64" placeholder="例如：生产环境分析模型" /></label>
            <dl v-if="llmKeyStatus.configured" class="llm-key-facts">
              <div><dt>密钥前缀</dt><dd><code>{{ llmKeyStatus.prefix }}...</code></dd></div>
              <div><dt>创建时间</dt><dd>{{ llmKeyStatus.createdAt ? new Date(llmKeyStatus.createdAt).toLocaleString() : "—" }}</dd></div>
              <div><dt>最近调用</dt><dd>{{ llmKeyStatus.lastUsedAt ? new Date(llmKeyStatus.lastUsedAt).toLocaleString() : "尚未调用" }}</dd></div>
            </dl>
            <div v-if="generatedLLMKey" class="llm-generated-key"><span>请立即保存此密钥</span><div><code>{{ generatedLLMKey }}</code><button type="button" title="复制密钥" aria-label="复制密钥" @click="copyLLMKey"><Copy :size="16" /></button></div></div>
            <p v-if="llmKeyMessage" class="llm-key-message"><CheckCircle2 :size="15" />{{ llmKeyMessage }}</p>
            <div class="llm-key-actions">
              <button class="primary compact" type="button" :disabled="llmKeyBusy" @click="rotateLLMKey"><RotateCcw :size="16" />{{ llmKeyStatus.configured ? "轮换密钥" : "生成密钥" }}</button>
              <button v-if="llmKeyStatus.configured" class="secondary compact danger-action" type="button" :disabled="llmKeyBusy" @click="revokeLLMKey"><Trash2 :size="16" />撤销密钥</button>
            </div>
          </div>
          <div class="llm-api-reference">
            <div class="llm-reference-heading"><BookOpen :size="18" /><div><strong>调用规范</strong><p>使用 Bearer 密钥访问版本化接口。</p></div></div>
            <div class="llm-code-block"><span>鉴权请求头</span><code>Authorization: Bearer cm_llm_...</code></div>
            <div class="llm-endpoints"><div><code>GET /api/llm/v1/analysis</code><small>系统设置与运行汇总分析</small></div><div><code>GET /api/llm/v1/settings/system</code><small>读取系统展示设置</small></div><div><code>PATCH /api/llm/v1/settings/system</code><small>修改允许的大模型写入字段</small></div></div>
            <p class="llm-safety-note">模型可按规范维护用户状态、运行日志和数据库统计，但不能读取或修改用户密码、会话、支付凭据、应用 Secret、网站 TLS 与 DNS 凭据，也不能执行任意 SQL。所有写入都会进入审计日志。</p>
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
.llm-key-state { padding: 5px 8px; border-radius: 6px; font-size: 10px; font-weight: 700; }
.llm-key-state.ready { color: var(--accent); background: var(--accent-soft); }
.llm-key-state.idle { color: var(--text-muted); background: var(--surface); }
.llm-api-layout { display: grid; grid-template-columns: minmax(0, 1.05fr) minmax(340px, .95fr); }
.llm-key-panel, .llm-api-reference { min-width: 0; display: grid; align-content: start; gap: 17px; padding: 22px 24px; }
.llm-key-panel { border-right: 1px solid var(--line); }
.llm-key-heading, .llm-reference-heading { display: flex; align-items: flex-start; gap: 10px; }
.llm-key-heading > svg, .llm-reference-heading > svg { color: var(--accent); }
.llm-key-heading strong, .llm-reference-heading strong { font-size: 13px; }
.llm-key-heading p, .llm-reference-heading p { margin: 4px 0 0; color: var(--text-muted); font-size: 11px; }
.llm-key-panel .log-field span { font-size: 12px; font-weight: 650; }
.llm-key-facts { display: grid; margin: 0; border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
.llm-key-facts div { display: flex; justify-content: space-between; gap: 16px; padding: 10px 12px; border-bottom: 1px solid var(--line); font-size: 10px; }
.llm-key-facts div:last-child { border-bottom: 0; }
.llm-key-facts dt { color: var(--text-muted); } .llm-key-facts dd { margin: 0; text-align: right; }
.llm-key-facts code, .llm-code-block code, .llm-endpoints code { color: var(--accent); font-family: var(--font-mono); }
.llm-generated-key { display: grid; gap: 7px; padding: 12px; background: var(--accent-soft); border: 1px solid color-mix(in srgb, var(--accent) 28%, transparent); border-radius: 8px; }
.llm-generated-key > span { color: var(--accent); font-size: 10px; font-weight: 700; }
.llm-generated-key > div { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr) 34px; gap: 8px; align-items: center; }
.llm-generated-key code { overflow: hidden; font-family: var(--font-mono); font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.llm-generated-key button { width: 34px; height: 34px; display: grid; place-items: center; padding: 0; color: var(--accent); background: var(--paper); border: 1px solid var(--line); border-radius: 7px; }
.llm-key-message { display: flex; align-items: center; gap: 7px; margin: 0; color: var(--accent); font-size: 10px; }
.llm-key-actions { display: flex; gap: 8px; } .danger-action { color: #b42318; }
.llm-code-block { display: grid; gap: 7px; padding: 12px; background: var(--surface); border: 1px solid var(--line); border-radius: 8px; }
.llm-code-block span { color: var(--text-muted); font-size: 9px; } .llm-code-block code { overflow-wrap: anywhere; font-size: 10px; }
.llm-endpoints { display: grid; border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
.llm-endpoints div { display: grid; gap: 4px; padding: 11px 12px; border-bottom: 1px solid var(--line); }
.llm-endpoints div:last-child { border-bottom: 0; } .llm-endpoints code { font-size: 10px; } .llm-endpoints small { color: var(--text-muted); font-size: 9px; }
.llm-safety-note { margin: 0; color: var(--text-muted); font-size: 10px; line-height: 1.6; }
@media (max-width: 860px) { .llm-api-layout { grid-template-columns: 1fr; } .llm-key-panel { border-right: 0; border-bottom: 1px solid var(--line); } }
@media (min-width: 768px) {
  .md\:grid-cols-2 {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
