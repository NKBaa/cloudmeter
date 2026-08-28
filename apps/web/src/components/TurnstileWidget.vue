<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from "vue";

declare global {
  interface Window {
    turnstile?: {
      render: (
        element: HTMLElement,
        options: Record<string, unknown>,
      ) => string;
      reset: (id: string) => void;
      remove: (id: string) => void;
    };
  }
}
const props = defineProps<{ siteKey: string }>();
const emit = defineEmits<{ (event: "token", value: string): void }>();
const host = ref<HTMLElement | null>(null);
let widget = "";
let timer = 0;
function render() {
  if (!host.value || !window.turnstile || widget) return;
  widget = window.turnstile.render(host.value, {
    sitekey: props.siteKey,
    theme: "auto",
    callback: (value: string) => emit("token", value),
    "expired-callback": () => emit("token", ""),
    "error-callback": () => {
      emit("token", "");
      return true;
    },
  });
}
function load() {
  const existing = document.querySelector<HTMLScriptElement>(
    "script[data-cloudmeter-turnstile]",
  );
  if (existing) {
    timer = window.setInterval(() => {
      if (window.turnstile) {
        window.clearInterval(timer);
        render();
      }
    }, 100);
    return;
  }
  const script = document.createElement("script");
  script.src =
    "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
  script.async = true;
  script.defer = true;
  script.dataset.cloudmeterTurnstile = "true";
  script.onload = render;
  document.head.appendChild(script);
}
watch(
  () => props.siteKey,
  () => {
    if (widget && window.turnstile) {
      window.turnstile.remove(widget);
      widget = "";
    }
    emit("token", "");
    render();
  },
);
onMounted(load);
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer);
  if (widget && window.turnstile) window.turnstile.remove(widget);
});
</script>
<template>
  <div ref="host" class="turnstile-host" aria-label="人机验证"></div>
</template>
<style scoped>
.turnstile-host {
  min-height: 65px;
  display: flex;
  align-items: center;
  overflow: hidden;
  border-radius: 12px;
}
</style>
