<script setup lang="ts">
import { BookOpen, ExternalLink } from "@lucide/vue";
</script>
<template>
  <main class="workspace">
    <header>
      <div>
        <p class="eyebrow">CloudMeter 文档</p>
        <h1>使用与部署指南</h1>
        <p>平台网络、应用模板、计费与运维规则的统一入口。</p>
      </div>
    </header>
    <section class="settings-card docs-grid">
      <article>
        <BookOpen :size="20" />
        <div>
          <h2>应用访问模型</h2>
          <p>
            用户应用没有宿主机端口映射，通过实例路径和平台网关进行身份校验与反向代理。
          </p>
        </div>
      </article>
      <article>
        <BookOpen :size="20" />
        <div>
          <h2>数据与备份</h2>
          <p>
            数据卷保存持久数据；备份按应用与数据卷分类，不与镜像层重复计费。
          </p>
        </div>
      </article>
      <article>
        <BookOpen :size="20" />
        <div>
          <h2>Docker 部署</h2>
          <p>
            完整的空环境部署、OpenResty 转发和镜像加速配置位于仓库部署文档。
          </p>
          <a
            href="https://github.com/NKBaa/cloudmeter/blob/main/docs/docker-deployment.md"
            target="_blank"
            rel="noopener"
            >查看部署文档<ExternalLink :size="14"
          /></a>
        </div>
      </article>
    </section>
  </main>
</template>
