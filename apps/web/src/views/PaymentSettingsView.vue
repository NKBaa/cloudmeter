<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ArrowLeft, CheckCircle2, KeyRound, LogOut, Save } from '@lucide/vue'
import { api, logout } from '../api'
import BrandMark from '../components/BrandMark.vue'
const model=reactive({enabled:false,merchantId:'',endpoint:'',secret:''}),configured=ref(false),error=ref(''),message=ref('')
async function load(){try{const r=await api<any>('/admin/settings/payments');const p=r.providers.find((v:any)=>v.provider==='epay');if(p){model.enabled=p.enabled;model.merchantId=p.merchantId;model.endpoint=p.endpoint;configured.value=p.secretConfigured}}catch(e){error.value=(e as Error).message}}
onMounted(load)
async function save(){try{await api('/admin/settings/payments/epay',{method:'PUT',body:JSON.stringify(model)});message.value='EPay 设置已保存';error.value='';await load()}catch(e){error.value=(e as Error).message}}
</script>
<template><div class="app-shell"><aside><BrandMark/><nav><a href="/admin"><ArrowLeft :size="18"/>平台设置</a><a class="active"><KeyRound :size="18"/>支付设置</a></nav><button class="icon-text" @click="logout"><LogOut :size="17"/>退出</button></aside><main class="workspace admin-workspace"><header><div><p class="eyebrow">资金运营</p><h1>支付设置</h1></div></header><p v-if="error" class="message">{{error}}</p><p v-if="message" class="status-ok">{{message}}</p><section class="form-panel payment-settings"><h2><KeyRound :size="19"/>EPay</h2><form @submit.prevent="save"><label class="toggle"><input v-model="model.enabled" type="checkbox"/>启用 EPay 回调</label><label>商户号<input v-model="model.merchantId" required/></label><label>接口地址<input v-model="model.endpoint" type="url" required/></label><label>签名密钥<input v-model="model.secret" type="password" placeholder="留空保持现有密钥" :required="model.enabled&&!configured"/></label><p class="quiet"><CheckCircle2 v-if="configured" class="ok" :size="15"/> {{configured?'密钥已配置，接口不会显示原文':'尚未配置密钥'}}</p><button class="primary compact"><Save :size="16"/>保存设置</button></form></section></main></div></template>
