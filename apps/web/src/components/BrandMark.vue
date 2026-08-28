<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useSiteConfig } from "../site-config";
const { systemName, fullSettings } = useSiteConfig();
const logoFailed = ref(false);
const logoUrl = computed(() => fullSettings.value?.logoUrl?.trim() || "");
watch(logoUrl, () => {
  logoFailed.value = false;
});
</script>
<template>
  <RouterLink class="brand" to="/" :aria-label="'返回 ' + systemName + ' 首页'">
    <span class="brand-mark">
      <img
        v-if="logoUrl && !logoFailed"
        :src="logoUrl"
        :alt="systemName + ' Logo'"
        @error="logoFailed = true"
      />
      <svg
        v-else
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
    <span class="brand-text">{{ systemName }}</span>
  </RouterLink>
</template>

<style scoped>
.brand {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  height: 32px;
  text-decoration: none;
  color: var(--text);
  font-weight: 700;
  font-size: 17px;
  letter-spacing: -0.03em;
  line-height: 1;
}
.brand-mark {
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  display: grid;
  place-items: center;
  background: var(--accent);
  color: var(--primary-foreground);
  border-radius: 8px;
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.1) inset, 0 2px 8px var(--accent-glow);
}
.brand-icon {
  width: 18px;
  height: 18px;
}
.brand-mark img {
  width: 100%;
  height: 100%;
  object-fit: contain;
  border-radius: inherit;
}
.brand-text {
  font-weight: 800;
  color: var(--text);
  line-height: 1;
  display: inline-flex;
  align-items: center;
}
.brand-sub {
  color: var(--text-muted);
  font-weight: 500;
}
</style>
