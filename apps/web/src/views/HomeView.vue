<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  Activity,
  ArrowRight,
  Boxes,
  ChartNoAxesCombined,
  DatabaseBackup,
  GitCompareArrows,
  LogIn,
  Network,
  ShieldCheck,
} from "@lucide/vue";
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
    <section id="features" class="capability-section">
      <div class="capability-heading">
        <div><p class="eyebrow">产品能力</p><h2>从发布到计费，一套完整闭环</h2></div>
        <p>面向按量付费应用云的核心能力已经整合在同一个控制台中。</p>
      </div>
      <div class="capability-grid">
        <article><span><Boxes :size="20" /></span><div><strong>模板化应用交付</strong><p>管理员维护镜像版本、启动参数、环境变量、Secret、依赖与数据卷，用户选择后即可部署。</p></div></article>
        <article><span><ChartNoAxesCombined :size="20" /></span><div><strong>资源按量计费</strong><p>CPU、内存、持久数据卷、流量与应用运行费用独立计量，余额、账单和用量随时可查。</p></div></article>
        <article><span><Network :size="20" /></span><div><strong>统一公网入口</strong><p>应用容器不直接暴露端口，统一通过路径网关访问，并支持 WebSocket、SSE 与反向代理。</p></div></article>
        <article><span><GitCompareArrows :size="20" /></span><div><strong>版本与安全发布</strong><p>新版本先测试和健康检查再发布；更新失败自动恢复，历史版本支持人工回滚。</p></div></article>
        <article><span><DatabaseBackup :size="20" /></span><div><strong>持久数据保护</strong><p>数据卷独立于容器生命周期，可创建备份、查看状态并恢复到指定应用。</p></div></article>
        <article><span><ShieldCheck :size="20" /></span><div><strong>租户隔离与运营</strong><p>同一用户应用可通过内网服务名互访，跨用户隔离；管理员统一管理产品、用户、支付和审计。</p></div></article>
        <article class="capability-wide"><span><Activity :size="20" /></span><div><strong>运行状态一眼掌握</strong><p>概览集中展示已部署应用、预估月费、CPU 与内存占用以及公网访问地址，让资源状态和成本保持在同一视图。</p></div><RouterLink to="/console">进入控制台<ArrowRight :size="16" /></RouterLink></article>
      </div>
    </section>
    <footer>CloudMeter · 简洁、安全、可计量的应用云</footer>
  </main>
</template>
