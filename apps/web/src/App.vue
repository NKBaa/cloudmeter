<script setup lang="ts">
import { RouterView } from 'vue-router'
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
