<script setup lang="ts">
import { onMounted, ref } from "vue";
import { CircleAlert, LoaderCircle } from "@lucide/vue";
import { api } from "../api";
import BrandMark from "../components/BrandMark.vue";
const error = ref("");
onMounted(async () => {
  const params = new URLSearchParams(window.location.search);
  const result = params.get("result"),
    providerError = params.get("error");
  if (providerError || !result) {
    error.value = providerError || "OAuth 登录结果无效";
    return;
  }
  history.replaceState({}, "", window.location.pathname);
  try {
    const data = await api<{ token: string }>("/auth/oauth/exchange", {
      method: "POST",
      body: JSON.stringify({ result }),
    });
    localStorage.setItem("session_token", data.token);
    window.location.replace("/console");
  } catch (e) {
    error.value = (e as Error).message;
  }
});
</script>
<template>
  <main class="callback-shell">
    <BrandMark />
    <section>
      <template v-if="!error"
        ><LoaderCircle class="spin" :size="28" />
        <h1>正在完成登录</h1>
        <p>正在安全地验证你的 OAuth 账户。</p></template
      ><template v-else
        ><CircleAlert :size="28" />
        <h1>登录未完成</h1>
        <p class="message">{{ error }}</p>
        <a class="secondary callback-link" href="/login">返回登录</a></template
      >
    </section>
  </main>
</template>
