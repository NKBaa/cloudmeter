<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Archive, ArchiveRestore, ChevronDown, Database, HardDrive, PackagePlus, RefreshCw, RotateCcw, ShieldCheck, Trash2 } from '@lucide/vue'
import { api } from '../api'

type App = { id: string; slug: string; productSlug: string; status: string }
type Volume = { name: string; mountPath: string }
type Backup = {
  id: string
  volumeKey: string
  status: string
  sizeBytes?: number
  lastError?: string
  createdAt: string
  completedAt?: string
  deletionStatus?: string
}
type BackupData = {
  backups: Backup[]
  volumes: Volume[]
  capacityGiB: number
  volumeUsageBytes: Record<string, number>
  backupUsageBytes: number
}
type VolumeGroup = Volume & { active: boolean; backups: Backup[]; usageBytes: number }

const apps = ref<App[]>([])
const data = ref<Record<string, BackupData>>({})
const expandedAppID = ref('')
const expandedVolumeID = ref('')
const error = ref('')
const message = ref('')
const busy = ref('')
const loading = ref(false)
const labels: Record<string, string> = { queued: '排队中', running: '处理中', succeeded: '已完成', failed: '失败' }
let timer: number | undefined

function emptyData(): BackupData {
  return { backups: [], volumes: [], capacityGiB: 0, volumeUsageBytes: {}, backupUsageBytes: 0 }
}
function appData(appID: string): BackupData {
  return data.value[appID] || emptyData()
}
function volumeGroups(appID: string): VolumeGroup[] {
  const current = appData(appID)
  const groups = new Map<string, VolumeGroup>()
  for (const volume of current.volumes) {
    groups.set(volume.name, { ...volume, active: true, backups: [], usageBytes: current.volumeUsageBytes?.[volume.name] || 0 })
  }
  for (const backup of current.backups) {
    const group = groups.get(backup.volumeKey) || {
      name: backup.volumeKey,
      mountPath: '历史数据卷（当前版本已移除）',
      active: false,
      backups: [],
      usageBytes: current.volumeUsageBytes?.[backup.volumeKey] || 0,
    }
    group.backups.push(backup)
    groups.set(group.name, group)
  }
  return [...groups.values()]
    .map((group) => ({ ...group, backups: [...group.backups].sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt)) }))
    .sort((a, b) => Number(b.active) - Number(a.active) || a.name.localeCompare(b.name, 'zh-CN'))
}
function toggleApp(appID: string) {
  expandedAppID.value = expandedAppID.value === appID ? '' : appID
  if (!expandedAppID.value) expandedVolumeID.value = ''
}
function volumeID(appID: string, volumeName: string) { return `${appID}:${volumeName}` }
function toggleVolume(appID: string, volumeName: string) {
  const id = volumeID(appID, volumeName)
  expandedVolumeID.value = expandedVolumeID.value === id ? '' : id
}
function latestBackup(group: VolumeGroup) { return group.backups[0] }
function formatDate(value?: string) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未备份'
}
function formatBytes(value?: number) {
  if (value == null || value < 1) return value === 0 ? '0 B' : '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${(value / 1024 ** index).toFixed(index > 1 ? 1 : 0)} ${units[index]}`
}
function liveUsage(appID: string) {
  return Object.values(appData(appID).volumeUsageBytes || {}).reduce((sum, value) => sum + Number(value || 0), 0)
}
function capacityBytes(appID: string) { return Math.max(0, appData(appID).capacityGiB || 0) * 1024 ** 3 }
function totalUsage(appID: string) { return liveUsage(appID) + Number(appData(appID).backupUsageBytes || 0) }
function remainingBytes(appID: string) { return Math.max(0, capacityBytes(appID) - totalUsage(appID)) }
function usagePercent(appID: string) {
  const capacity = capacityBytes(appID)
  return capacity > 0 ? Math.min(100, totalUsage(appID) / capacity * 100) : 0
}
function backupStatus(backup: Backup) {
  if (backup.deletionStatus === 'queued' || backup.deletionStatus === 'running') return '删除中'
  if (backup.deletionStatus === 'failed') return '删除失败'
  return labels[backup.status] || backup.status
}
function backupStatusClass(backup: Backup) {
  if (backup.deletionStatus === 'queued' || backup.deletionStatus === 'running') return 'pending'
  if (backup.deletionStatus === 'failed' || backup.status === 'failed') return 'danger'
  return backup.status === 'succeeded' ? 'active' : 'pending'
}
const totalBackups = computed(() => Object.values(data.value).reduce((sum, item) => sum + item.backups.length, 0))

async function load(silent = false) {
  try {
    if (!silent) loading.value = true
    const result = await api<{ apps: App[] }>('/apps')
    apps.value = result.apps
    const pairs = await Promise.all(result.apps.map(async (app) => [app.id, await api<BackupData>(`/apps/${app.id}/backups`)] as const))
    data.value = Object.fromEntries(pairs)
    if (expandedAppID.value && !result.apps.some((app) => app.id === expandedAppID.value)) expandedAppID.value = ''
    error.value = ''
  } catch (value) {
    error.value = (value as Error).message
  } finally {
    loading.value = false
  }
}
async function createBackup(app: App, key: string) {
  try {
    busy.value = `create:${app.id}:${key}`
    error.value = ''
    await api(`/apps/${app.id}/backups`, { method: 'POST', body: JSON.stringify({ volumeKey: key }) })
    message.value = `${app.slug} / ${key} 的备份任务已创建`
    expandedVolumeID.value = volumeID(app.id, key)
    await load(true)
  } catch (value) {
    error.value = (value as Error).message
  } finally { busy.value = '' }
}
async function restore(app: App, backup: Backup) {
  if (!confirm(`确定用 ${formatDate(backup.createdAt)} 的备份恢复 ${app.slug} / ${backup.volumeKey}？恢复期间应用会短暂离线。`)) return
  try {
    busy.value = `restore:${backup.id}`
    error.value = ''
    await api(`/apps/${app.id}/backups/${backup.id}/restore`, { method: 'POST', body: JSON.stringify({ idempotencyKey: crypto.randomUUID() }) })
    message.value = `${app.slug} 正在恢复 ${backup.volumeKey}`
    await load(true)
  } catch (value) {
    error.value = (value as Error).message
  } finally { busy.value = '' }
}
async function removeBackup(app: App, backup: Backup) {
  const retry = backup.deletionStatus === 'failed'
  if (!confirm(`${retry ? '重新尝试删除' : '确定删除'} ${app.slug} / ${backup.volumeKey} 在 ${formatDate(backup.createdAt)} 创建的备份？归档清理后不能恢复。`)) return
  try {
    busy.value = `delete:${backup.id}`
    error.value = ''
    await api(`/apps/${app.id}/backups/${backup.id}`, { method: 'DELETE' })
    message.value = retry ? '已重新提交备份删除任务' : '备份已进入删除队列'
    await load(true)
  } catch (value) {
    error.value = (value as Error).message
  } finally { busy.value = '' }
}

onMounted(async () => { await load(); timer = window.setInterval(() => { if (!document.hidden) void load(true) }, 5000) })
onBeforeUnmount(() => { if (timer) window.clearInterval(timer) })
</script>

<template>
  <main class="workspace admin-workspace backups-view">
    <header>
      <div><p class="eyebrow">数据保护</p><h1>备份与恢复</h1><p>应用、数据卷和备份记录逐层展开；备份与应用数据共享同一容量池，不重复收取存储费。</p></div>
      <button class="secondary compact" :disabled="loading" @click="load()"><RefreshCw :class="{ spin: loading }" :size="16" />刷新</button>
    </header>
    <p v-if="error" class="message sticky-message">{{ error }}</p>
    <p v-if="message" class="status-ok sticky-message">{{ message }}</p>

    <section v-if="apps.length" class="backup-application-list">
      <div class="backup-page-summary"><span>{{ apps.length }} 个应用</span><span>{{ totalBackups }} 条可见备份</span><small>卷数据 + 成功备份共同占用用户选择的共享数据卷容量</small></div>
      <article v-for="app in apps" :key="app.id" :class="['backup-application-item', { expanded: expandedAppID === app.id }]">
        <button class="backup-application-row" type="button" :aria-expanded="expandedAppID === app.id" @click="toggleApp(app.id)">
          <span class="backup-app-icon"><Database :size="19" /></span>
          <span class="backup-app-identity"><strong>{{ app.slug }}</strong><small>{{ app.productSlug }}</small></span>
          <span class="backup-app-capacity"><small>共享容量</small><strong>{{ appData(app.id).capacityGiB || 0 }} GiB</strong><i><em :style="{ width: usagePercent(app.id) + '%' }"></em></i></span>
          <span class="backup-app-facts"><span><strong>{{ volumeGroups(app.id).length }}</strong><small>数据卷</small></span><span><strong>{{ appData(app.id).backups.length }}</strong><small>备份</small></span></span>
          <span :class="['status-pill', app.status === 'running' ? 'active' : 'suspended']">{{ app.status === 'running' ? '运行中' : app.status }}</span>
          <ChevronDown class="backup-expand-icon" :size="18" />
        </button>

        <Transition name="backup-detail">
          <div v-if="expandedAppID === app.id" class="backup-application-detail">
            <div class="backup-capacity-strip">
              <span><small>应用数据</small><strong>{{ formatBytes(liveUsage(app.id)) }}</strong></span>
              <span><small>备份归档</small><strong>{{ formatBytes(appData(app.id).backupUsageBytes) }}</strong></span>
              <span><small>合计占用</small><strong>{{ formatBytes(totalUsage(app.id)) }}</strong></span>
              <span><small>剩余容量</small><strong>{{ formatBytes(remainingBytes(app.id)) }}</strong></span>
            </div>
            <section class="backup-subsection">
              <div class="backup-subsection-heading"><div><strong>按数据卷管理</strong><small>点击数据卷展开按时间倒序排列的备份</small></div><span>{{ volumeGroups(app.id).length }} 个</span></div>
              <div v-if="volumeGroups(app.id).length" class="backup-volume-groups">
                <article v-for="group in volumeGroups(app.id)" :key="group.name" :class="['backup-volume-group', { expanded: expandedVolumeID === volumeID(app.id, group.name) }]">
                  <div class="backup-volume-row">
                    <button class="backup-volume-toggle" type="button" :aria-expanded="expandedVolumeID === volumeID(app.id, group.name)" @click="toggleVolume(app.id, group.name)">
                      <span class="backup-row-primary"><HardDrive :size="16" /><span><strong>{{ group.name }}</strong><small>{{ group.mountPath }}</small></span></span>
                      <span><small>卷占用</small><strong>{{ formatBytes(group.usageBytes) }}</strong></span>
                      <span><small>备份记录</small><strong>{{ group.backups.length }} 条</strong></span>
                      <span><small>最近备份</small><strong>{{ formatDate(latestBackup(group)?.createdAt) }}</strong></span>
                      <ChevronDown class="backup-volume-chevron" :size="17" />
                    </button>
                    <button v-if="group.active" class="secondary compact" :disabled="app.status !== 'running' || busy !== ''" @click="createBackup(app, group.name)"><Archive :size="15" />立即备份</button>
                    <span v-else class="status-pill suspended">历史卷</span>
                  </div>
                  <Transition name="backup-detail">
                    <div v-if="expandedVolumeID === volumeID(app.id, group.name)" class="backup-volume-history">
                      <div v-if="group.backups.length" class="backup-history-table">
                        <div class="backup-table-head"><span>备份时间</span><span>归档容量</span><span>状态</span><span>操作</span></div>
                        <div v-for="backup in group.backups" :key="backup.id" class="backup-history-row">
                          <span class="backup-row-time"><strong>{{ formatDate(backup.createdAt) }}</strong><small v-if="backup.lastError">{{ backup.lastError }}</small></span>
                          <span>{{ formatBytes(backup.sizeBytes) }}</span>
                          <span :class="['status-pill', backupStatusClass(backup)]">{{ backupStatus(backup) }}</span>
                          <span class="backup-row-actions">
                            <button class="icon-action" title="恢复此备份" :disabled="backup.status !== 'succeeded' || Boolean(backup.deletionStatus) || app.status !== 'running' || busy !== ''" @click="restore(app, backup)"><RotateCcw :size="16" /></button>
                            <button class="icon-action danger-action" :title="backup.deletionStatus === 'failed' ? '重试删除' : '删除此备份'" :disabled="['queued','running'].includes(backup.status) || ['queued','running'].includes(backup.deletionStatus || '') || busy !== ''" @click="removeBackup(app, backup)"><Trash2 :size="16" /></button>
                          </span>
                        </div>
                      </div>
                      <div v-else class="backup-history-empty">这个数据卷还没有备份。点击“立即备份”创建第一条可恢复记录。</div>
                    </div>
                  </Transition>
                </article>
              </div>
              <div v-else class="context-empty compact-empty backup-inline-empty"><Database :size="21" /><div><strong>此应用没有可备份的数据卷</strong><p>只有管理员在产品版本中声明的持久卷才会出现在这里。</p></div></div>
            </section>
          </div>
        </Transition>
      </article>
    </section>

    <section v-else-if="!loading" class="context-empty">
      <span class="context-empty-icon"><ArchiveRestore :size="26" /></span>
      <div><p class="eyebrow">备份空间</p><h2>还没有可备份的应用</h2><p>先部署一个包含持久卷的应用，运行后即可在这里创建备份和执行恢复。</p>
        <div class="empty-actions"><RouterLink class="primary compact" to="/console/deploy"><PackagePlus :size="16" />部署应用</RouterLink><RouterLink class="secondary compact" to="/console/apps"><ShieldCheck :size="16" />查看我的应用</RouterLink></div>
      </div>
    </section>
  </main>
</template>
