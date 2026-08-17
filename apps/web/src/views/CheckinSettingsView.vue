<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { CalendarCheck, Save } from '@lucide/vue'
import { api } from '../api'
const model=reactive({enabled:true,minRewardCents:1,maxRewardCents:10})
const error=ref(''),message=ref(''),busy=ref(false)
async function load(){try{Object.assign(model,await api('/admin/settings/checkin'))}catch(e){error.value=(e as Error).message}}
async function save(){busy.value=true;error.value='';message.value='';try{await api('/admin/settings/checkin',{method:'PUT',body:JSON.stringify(model)});message.value='签到设置已保存'}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
onMounted(load)
</script>
<template><main class="workspace admin-workspace"><header><div><p class="eyebrow">用户运营</p><h1>签到设置</h1></div></header><p v-if="error" class="message">{{error}}</p><p v-if="message" class="status-ok">{{message}}</p><section class="form-panel checkin-settings-panel"><h2><CalendarCheck :size="20"/>每日签到</h2><p class="quiet">用户每天按北京时间可签到一次，随机奖励将直接写入不可修改的钱包账本。</p><form @submit.prevent="save"><label class="toggle"><input v-model="model.enabled" type="checkbox"/>允许用户每日签到</label><div class="field-row"><label>最低奖励（分）<input v-model.number="model.minRewardCents" type="number" min="1" max="10000" step="1" required/><small>当前 ¥{{(model.minRewardCents/100).toFixed(2)}}</small></label><label>最高奖励（分）<input v-model.number="model.maxRewardCents" type="number" :min="model.minRewardCents" max="10000" step="1" required/><small>当前 ¥{{(model.maxRewardCents/100).toFixed(2)}}</small></label></div><button class="primary compact" :disabled="busy"><Save :size="16"/>保存设置</button></form></section></main></template>
