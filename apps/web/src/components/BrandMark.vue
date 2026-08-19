<script setup lang="ts">
import { Boxes } from '@lucide/vue'
</script>
<template>
  <RouterLink class="brand" to="/" aria-label="返回 CloudMeter 首页">
    <span class="brand-mark"><Boxes :size="19" /></span>
    <strong>CloudMeter</strong>
  </RouterLink>
</template>
