<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Save, CheckCircle2, Bot, RotateCcw, RefreshCw } from "@lucide/vue";
import { api } from "../api";

interface AISupportSettings {
  enabled: boolean;
  provider: string;
  baseUrl: string;
  apiKey: string;
  modelName: string;
  systemPrompt: string;
  knowledgeBase: string;
}

const form = ref<AISupportSettings>({
  enabled: false,
  provider: "openai",
  baseUrl: "https://api.openai.com/v1",
  apiKey: "",
  modelName: "gpt-4o",
  systemPrompt: "You are a helpful IT support AI agent. You will read the user's issue and previous replies, and provide a clear, concise, and professional answer. If you cannot solve the issue, escalate it to human staff.",
  knowledgeBase: "",
});

const loading = ref(false);
const saving = ref(false);

const testingModel = ref(false);

async function testModel() {
  testingModel.value = true;
  error.value = "";
  message.value = "";

  if (!form.value.baseUrl || !form.value.apiKey || !form.value.modelName) {
    error.value = "请先填写 Base URL、API Key 和模型名称";
    testingModel.value = false;
    return;
  }

  try {
    const res = await api<{ message: string }>("/admin/settings/ai-support/test", {
      method: "POST",
      body: JSON.stringify(form.value)
    });

    if (res && res.message) {
      message.value = res.message;
      setTimeout(() => { message.value = ""; }, 6000);
    }
  } catch (err: any) {
    error.value = "模型连接测试失败: " + (err.message || "未知错误");
  } finally {
    testingModel.value = false;
  }
}

const message = ref("");
const error = ref("");

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const res = await api<AISupportSettings>("/admin/settings/ai-support");
    if (res) {
      Object.assign(form.value, res);
    }
  } catch (err: any) {
    error.value = err.message || "加载 AI 助手设置失败";
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  error.value = "";
  message.value = "";
  try {
    const res = await api<AISupportSettings>("/admin/settings/ai-support", {
      method: "PUT",
      body: JSON.stringify(form.value),
    });
    if (res) {
      Object.assign(form.value, res);
      message.value = "保存成功";
      setTimeout(() => { message.value = ""; }, 3000);
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
        <h1>AI 助手设置</h1>
        <p class="quiet">配置用于工单自动回复与智能客服的大语言模型 (LLM) API。</p>
      </div>
      <div class="actions flex gap-2">
        <button class="secondary compact" @click="load" :disabled="loading || saving">
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
      <!-- 基础开关 -->
      <section class="nextdev-card p-0">
        <div class="card-header-bar">
          <div class="card-title-group">
            <span class="eyebrow">STATUS · 状态</span>
            <h3>开启 AI 工单回复</h3>
          </div>
        </div>
        <div class="card-divider"></div>
        <div class="p-6 flex flex-col">
          <div class="switch-setting" style="display: flex; justify-content: space-between; align-items: center;">
            <div>
              <strong style="font-size: 15px; display: block; margin-bottom: 4px;">启用 AI 自动回复</strong>
              <small style="color: var(--text-muted);">启用后，用户提交新工单或回复时，系统将调用大模型自动进行解答。</small>
            </div>
            <label class="switch">
              <input v-model="form.enabled" type="checkbox" />
              <span></span>
            </label>
          </div>
        </div>
      </section>

      <!-- 模型配置 -->
      <section class="nextdev-card p-0" :style="{ opacity: form.enabled ? 1 : 0.5, pointerEvents: form.enabled ? 'auto' : 'none' }">
                <div class="card-header-bar">
          <div class="card-title-group">
            <span class="eyebrow">LLM CONFIGURATION · 模型参数</span>
            <h3>大模型 API 配置</h3>
          </div>
          <button class="secondary compact" @click="testModel" :disabled="testingModel">
            <RefreshCw v-if="testingModel" class="spin" :size="14" />
            <Bot v-else :size="14" />
            {{ testingModel ? '测试中...' : '测试模型' }}
          </button>
        </div>
        <div class="card-divider"></div>
        <div class="p-6 flex flex-col">
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <div class="log-field">
              <label>API 供应商</label>
              <select v-model="form.provider">
                <option value="openai">OpenAI</option>
                <option value="deepseek">DeepSeek</option>
                <option value="custom">OpenAI 兼容接口（Qwen 等）</option>
              </select>
              <em>当前统一使用 OpenAI Chat Completions 兼容协议。</em>
            </div>
            <div class="log-field">
              <label>模型名称 (Model Name)</label>
              <input v-model="form.modelName" placeholder="例如：gpt-4o, deepseek-chat, qwen-max" required />
              <em>填入供应商支持的具体模型名称。</em>
            </div>
          </div>
          <div class="log-field mt-5">
            <label>API 基础地址 (Base URL)</label>
            <input v-model="form.baseUrl" placeholder="例如：https://api.openai.com/v1" required />
            <em>必须填写完整的 Base URL 路径。</em>
          </div>
          <div class="log-field mt-5">
            <label>API 密钥 (API Key)</label>
            <input v-model="form.apiKey" type="password" placeholder="sk-..." required />
            <em>保存后不会在管理界面回显密钥原文。</em>
          </div>
        </div>
      </section>

      <!-- 角色与知识库设定 -->
      <section class="nextdev-card p-0" :style="{ opacity: form.enabled ? 1 : 0.5, pointerEvents: form.enabled ? 'auto' : 'none' }">
        <div class="card-header-bar">
          <div class="card-title-group">
            <span class="eyebrow">PROMPT & KNOWLEDGE · 提示词与知识库</span>
            <h3>角色设定</h3>
          </div>
        </div>
        <div class="card-divider"></div>
        <div class="p-6 flex flex-col gap-6">
          <div class="log-field">
            <label>系统提示词 (System Prompt)</label>
            <textarea v-model="form.systemPrompt" rows="4" placeholder="定义 AI 的角色和回答语气..."></textarea>
            <em>系统级别的前置指令，决定 AI 的基本人设和响应规则。</em>
          </div>
          <div class="log-field">
            <label>专有知识库与规则 (Knowledge Base)</label>
            <textarea v-model="form.knowledgeBase" rows="8" placeholder="补充您的业务介绍、常见问题解答或特殊规则..."></textarea>
            <em>将直接作为上下文注入给模型，便于 AI 结合您的特定业务进行更精准的回复。</em>
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
.log-field input, .log-field textarea, .log-field select {
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
.log-field input:focus, .log-field textarea:focus, .log-field select:focus {
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
