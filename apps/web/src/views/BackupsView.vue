<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { Archive, ArchiveRestore, ArrowLeft, Database, LogOut, RefreshCw, RotateCcw } from '@lucide/vue'
import { api, logout } from '../api'
import BrandMark from '../components/BrandMark.vue'

type App = { id:string; slug:string; productSlug:string; status:string }
type Volume = { name:string; mountPath:string }
type Backup = { id:string; volumeKey:string; status:string; sizeBytes?:number; lastError?:string; createdAt:string; completedAt?:string }
type BackupData = { backups:Backup[]; volumes:Volume[] }
const apps=ref<App[]>([]),data=ref<Record<string,BackupData>>({}),error=ref(''),message=ref(''),busy=ref('')
const labels:Record<string,string>={queued:'排队中',running:'处理中',succeeded:'已完成',failed:'失败'}
let timer:number|undefined
async function load(){
  try{const result=await api<{apps:App[]}>('/apps');apps.value=result.apps;const pairs=await Promise.all(result.apps.map(async app=>[app.id,await api<BackupData>('/apps/'+app.id+'/backups')] as const));data.value=Object.fromEntries(pairs);error.value=''}catch(e){error.value=(e as Error).message}
}
async function createBackup(app:App,key:string){try{busy.value=app.id+key;await api('/apps/'+app.id+'/backups',{method:'POST',body:JSON.stringify({volumeKey:key})});message.value=`${app.slug} 的备份任务已创建`;await load()}catch(e){error.value=(e as Error).message}finally{busy.value=''}}
async function restore(app:App,backup:Backup){if(!confirm(`确定用此备份恢复 ${app.slug}？恢复期间应用会短暂离线。`))return;try{busy.value=backup.id;await api('/apps/'+app.id+'/backups/'+backup.id+'/restore',{method:'POST',body:JSON.stringify({idempotencyKey:crypto.randomUUID()})});message.value=`${app.slug} 正在恢复`;await load()}catch(e){error.value=(e as Error).message}finally{busy.value=''}}
onMounted(async()=>{await load();timer=window.setInterval(load,5000)})
onBeforeUnmount(()=>{if(timer)window.clearInterval(timer)})
</script>

<template><main class="workspace admin-workspace"><header><div><p class="eyebrow">数据保护</p><h1>备份与恢复</h1></div><button class="secondary compact" @click="load"><RefreshCw :size="16"/>刷新</button></header><p v-if="error" class="message">{{error}}</p><p v-if="message" class="status-ok">{{message}}</p>
<section v-for="app in apps" :key="app.id" class="backup-app"><div class="section-heading"><div><p class="eyebrow">{{app.productSlug}}</p><h2>{{app.slug}}</h2></div><span :class="['status-pill',app.status==='running'?'active':'suspended']">{{app.status==='running'?'运行中':app.status}}</span></div><div v-if="data[app.id]?.volumes.length" class="volume-actions"><div v-for="volume in data[app.id].volumes" :key="volume.name"><Database :size="18"/><span><strong>{{volume.name}}</strong><small>{{volume.mountPath}}</small></span><button class="secondary compact" :disabled="app.status!=='running'||busy===app.id+volume.name" @click="createBackup(app,volume.name)"><Archive :size="16"/>立即备份</button></div></div><p v-else class="quiet empty-copy">当前版本没有声明持久卷。</p><div v-if="data[app.id]?.backups.length" class="backup-list"><article v-for="backup in data[app.id].backups" :key="backup.id"><span><strong>{{backup.volumeKey}}</strong><small>{{new Date(backup.createdAt).toLocaleString()}}<template v-if="backup.lastError"> · {{backup.lastError}}</template></small></span><span :class="['status-pill',backup.status==='succeeded'?'active':'suspended']">{{labels[backup.status]||backup.status}}</span><button class="icon-action" title="恢复此备份" :disabled="backup.status!=='succeeded'||app.status!=='running'||busy===backup.id" @click="restore(app,backup)"><RotateCcw :size="17"/></button></article></div></section><p v-if="!apps.length" class="quiet empty-copy">暂无应用。</p></main></template>
