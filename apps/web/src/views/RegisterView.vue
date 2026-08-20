<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ArrowRight, LoaderCircle } from '@lucide/vue'
import { api } from '../api'
import BrandMark from '../components/BrandMark.vue'
import TurnstileWidget from '../components/TurnstileWidget.vue'

type RegistrationPolicy = { emailVerificationRequired: boolean; registrationEnabled: boolean; turnstileSiteKey:string; turnstileRegistrationProtection:boolean }

const form = reactive({ displayName: '', email: '', password: '', code: '', turnstileToken: '' })
const loading = ref(false)
const message = ref('')
const verificationRequired = ref(false)
const registrationEnabled = ref(false)
const policyLoaded = ref(false)
const policyAvailable = ref(false)
const sending = ref(false)
const countdown = ref(0)
const turnstileSiteKey=ref(''), turnstileRequired=ref(false)
const formDisabled = computed(() => !policyAvailable.value || !registrationEnabled.value)
const submitLabel = computed(() => {
  if (!policyLoaded.value) return '正在检查注册状态'
  if (!policyAvailable.value) return '暂无法确认注册状态'
  if (!registrationEnabled.value) return '暂未开放注册'
  return '创建账户'
})

function unavailableMessage() {
  return policyAvailable.value ? '当前暂未开放公开注册' : '暂时无法确认注册状态，请稍后重试'
}

onMounted(async () => {
  try {
    const policy = await api<RegistrationPolicy>('/auth/registration-policy')
    verificationRequired.value = policy.emailVerificationRequired
    registrationEnabled.value = policy.registrationEnabled
    turnstileSiteKey.value=policy.turnstileSiteKey||''
    turnstileRequired.value=policy.turnstileRegistrationProtection
    policyAvailable.value = true
    if (!policy.registrationEnabled) message.value = '当前暂未开放公开注册'
  } catch (error) {
    message.value = (error as Error).message
  } finally {
    policyLoaded.value = true
  }
})

async function sendCode() {
  if (formDisabled.value) {
    message.value = unavailableMessage()
    return
  }
  if (!form.email) {
    message.value = '请先填写邮箱'
    return
  }

  sending.value = true
  message.value = ''
  try {
    await api('/auth/verification-code', { method: 'POST', body: JSON.stringify({ email: form.email }) })
    message.value = '验证码已发送，请检查邮箱'
    countdown.value = 60
    const timer = window.setInterval(() => {
      countdown.value--
      if (countdown.value <= 0) window.clearInterval(timer)
    }, 1000)
  } catch (error) {
    message.value = (error as Error).message
  } finally {
    sending.value = false
  }
}

async function register() {
  if (formDisabled.value) {
    message.value = unavailableMessage()
    return
  }
  if(turnstileRequired.value&&!form.turnstileToken){message.value='请先完成人机验证';return}

  loading.value = true
  message.value = ''
  try {
    await api('/auth/register', { method: 'POST', body: JSON.stringify(form) })
    window.location.assign('/login')
  } catch (error) {
    message.value = (error as Error).message
    form.turnstileToken=''
  } finally {
    loading.value = false
  }
}
</script>
<template><main class="auth-shell"><div class="auth-card"><div class="auth-brand"><BrandMark/><p class="eyebrow">加入平台</p><h1>从一个应用开始你的工作区</h1><p class="lede">注册后即可查看管理员发布的应用模板，并管理自己的部署。</p></div><form class="auth-form" :aria-busy="loading" @submit.prevent="register"><h2>创建账户</h2><p>{{ verificationRequired ? '使用真实邮箱完成验证后创建账户' : '使用真实邮箱接收平台通知' }}</p><label>姓名<input v-model="form.displayName" required maxlength="80" autocomplete="name" :disabled="formDisabled" /></label><label>邮箱<input v-model="form.email" type="email" required autocomplete="email" :disabled="formDisabled" /></label><div v-if="verificationRequired" class="code-field"><label>邮箱验证码<input v-model="form.code" required inputmode="numeric" maxlength="6" autocomplete="one-time-code" :disabled="formDisabled" /></label><button type="button" class="secondary send-btn" :disabled="formDisabled||sending||countdown>0" @click="sendCode">{{sending?'发送中':countdown>0?`${countdown}s`:'发送验证码'}}</button></div><label>密码<input v-model="form.password" type="password" required minlength="12" autocomplete="new-password" :disabled="formDisabled" /><small>至少 12 个字符</small></label><TurnstileWidget v-if="turnstileRequired&&turnstileSiteKey" :site-key="turnstileSiteKey" @token="form.turnstileToken=$event"/><p v-if="message" class="message" role="status">{{message}}</p><button class="primary" :disabled="loading||formDisabled||(turnstileRequired&&!form.turnstileToken)"><LoaderCircle v-if="loading" class="spin" :size="18"/><template v-else>{{submitLabel}}<ArrowRight v-if="registrationEnabled&&policyAvailable" :size="18"/></template></button></form><small class="auth-foot">已有账户？<a href="/login">返回登录</a></small></div></main></template>
