<script setup lang="ts">
import { onMounted } from "vue";
import { RouterView } from "vue-router";
import { useSiteConfig } from "./site-config";

const { fetchSiteConfig } = useSiteConfig();
onMounted(() => {
  fetchSiteConfig();
});
</script>
<template>
  <RouterView v-slot="{ Component, route }">
    <Transition name="route-slide" mode="out-in">
      <component
        :is="Component"
        :key="route.matched.length > 1 ? 'app-shell' : route.fullPath"
      />
    </Transition>
  </RouterView>
</template>
