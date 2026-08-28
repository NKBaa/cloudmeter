<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Code2, Eye, Save } from "@lucide/vue";
import { api } from "../api";
import { sanitizeHTML } from "../sanitize-html";
const html = ref(""),
  busy = ref(false),
  message = ref(""),
  error = ref("");
const preview = computed(() => sanitizeHTML(html.value));
onMounted(async () => {
  try {
    html.value = (await api<{ contentHtml: string }>("/homepage")).contentHtml;
  } catch (e) {
    error.value = (e as Error).message;
  }
});
async function save() {
  try {
    busy.value = true;
    error.value = "";
    await api("/admin/settings/homepage", {
      method: "PUT",
      body: JSON.stringify({ contentHtml: html.value }),
    });
    message.value = "首页内容已保存";
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <main class="workspace admin-workspace">
    <header>
      <div>
        <p class="eyebrow">站点设置</p>
        <h1>首页内容</h1>
        <p class="quiet">自定义前台首页 HTML 富文本展示与即时安全预览。</p>
      </div>
    </header>
    <p v-if="error" class="message">{{ error }}</p>
    <p v-if="message" class="status-ok">{{ message }}</p>
    <section class="homepage-editor">
      <article class="config-section">
        <div class="config-heading">
          <Code2 :size="18" />
          <div><strong>HTML 内容</strong></div>
        </div>
        <textarea
          v-model="html"
          class="html-editor"
          spellcheck="false"
        ></textarea
        ><button class="primary compact" :disabled="busy" @click="save">
          <Save :size="16" />保存首页
        </button>
      </article>
      <article class="config-section">
        <div class="config-heading">
          <Eye :size="18" />
          <div><strong>安全预览</strong></div>
        </div>
        <div class="homepage-preview" v-html="preview"></div>
      </article>
    </section>
  </main>
</template>
