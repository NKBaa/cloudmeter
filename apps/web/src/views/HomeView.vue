<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { ArrowRight, Boxes, LogIn } from "@lucide/vue";
import BrandMark from "../components/BrandMark.vue";
import { sanitizeHTML } from "../sanitize-html";
const content = ref("");
const safeContent = computed(() => sanitizeHTML(content.value));
onMounted(async () => {
  try { const response = await fetch("/api/homepage"); if (response.ok) content.value = (await response.json()).contentHtml || ""; } catch { /* Static shell remains available. */ }
});
</script>
<template>
  <main class="public-home">
    <nav class="home-nav"><BrandMark /><div><a href="#features">产品能力</a><RouterLink to="/console">控制台</RouterLink></div><div class="home-nav-actions"><RouterLink class="ghost-link" to="/login"><LogIn :size="16" />登录</RouterLink><RouterLink class="solid-link" to="/register">开始使用<ArrowRight :size="16" /></RouterLink></div></nav>
    <div class="home-grid"><div class="home-content" v-html="safeContent"></div><aside class="home-console-preview"><div class="preview-bar"><i></i><i></i><i></i><span>CloudMeter Console</span></div><div class="preview-body"><div class="preview-sidebar"><b></b><span></span><span></span><span></span><span></span></div><div class="preview-main"><small>资源概览</small><h3>应用，尽在掌握</h3><div class="preview-stats"><b>6<small>运行中</small></b><b>32%<small>CPU</small></b><b>1.8G<small>内存</small></b></div><div class="preview-app"><Boxes :size="24" /><div><strong>example-app</strong><small>运行中 · /apps/demo/example-app/</small></div><em>访问</em></div></div></div></aside></div>
    <footer>CloudMeter · 简洁、安全、可计量的应用云</footer>
  </main>
</template>
