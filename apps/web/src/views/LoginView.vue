<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ArrowRight, Code2, LoaderCircle } from '@lucide/vue'
import { api } from '../api'
import BrandMark from '../components/BrandMark.vue'
const form=reactive({email:'',password:''}), loading=ref(false), oauthLoading=ref(''), message=ref(''), providers=ref<string[]>([]), registrationEnabled=ref(false)
onMounted(async()=>{
  const [providersResult, policyResult] = await Promise.allSettled([
    api<{providers:string[]}>('/auth/oauth/providers'),
    api<{registrationEnabled:boolean}>('/auth/registration-policy'),
  ])
  if (providersResult.status === 'fulfilled') providers.value = providersResult.value.providers
  if (policyResult.status === 'fulfilled') registrationEnabled.value = policyResult.value.registrationEnabled
})
async function login(){loading.value=true;message.value='';try{const data=await api<{token:string}>('/auth/login',{method:'POST',body:JSON.stringify(form)});localStorage.setItem('session_token',data.token);window.location.assign('/console')}catch(e){message.value=(e as Error).message}finally{loading.value=false}}
async function oauth(provider:string){oauthLoading.value=provider;message.value='';try{const data=await api<{authorizationUrl:string}>(`/auth/oauth/${provider}/start`,{method:'POST'});window.location.assign(data.authorizationUrl)}catch(e){message.value=(e as Error).message;oauthLoading.value=''}}
</script>
<template><main class="auth-shell"><div class="auth-card"><div class="auth-brand"><BrandMark/><p class="eyebrow">部署、计量、结算</p><h1>一处掌握应用的整个运行周期</h1><p class="lede">从模板发布到按量扣费，每个变更都可追溯。</p></div><form class="auth-form" @submit.prevent="login"><h2>登录控制台</h2><p>使用你的平台账户继续</p><label>邮箱<input v-model="form.email" type="email" required autocomplete="email" placeholder="name@example.com" /></label><label>密码<input v-model="form.password" type="password" required autocomplete="current-password" placeholder="输入密码" /></label><p v-if="message" class="message" role="status">{{message}}</p><button class="primary" :disabled="loading||!!oauthLoading"><LoaderCircle v-if="loading" class="spin" :size="18"/><template v-else>登录<ArrowRight :size="18"/></template></button><template v-if="providers.length"><div class="oauth-divider"><span>或</span></div><button v-if="providers.includes('github')" type="button" class="oauth-button" :disabled="!!oauthLoading" @click="oauth('github')"><LoaderCircle v-if="oauthLoading==='github'" class="spin" :size="18"/><Code2 v-else :size="18"/>使用 GitHub 登录</button><button v-if="providers.includes('linuxdo')" type="button" class="oauth-button" :disabled="!!oauthLoading" @click="oauth('linuxdo')"><LoaderCircle v-if="oauthLoading==='linuxdo'" class="spin" :size="18"/><span v-else class="linuxdo-icon">L</span>使用 LinuxDo 登录</button></template><a v-if="registrationEnabled" class="form-link" href="/register">创建账户</a></form><small class="auth-foot">AI 应用云部署平台</small></div></main></template>
