<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { Activity, AppWindow, BadgeCent, BadgeDollarSign, CheckCircle2, CircleAlert, CirclePause, CirclePlay, Coins, Copy, CreditCard, Eye, FileClock, Globe2, KeyRound, LogIn, LogOut, MailCheck, Megaphone, Save, Settings2, ShieldPlus, Users, X } from '@lucide/vue'
import { api, logout } from '../api'
import BrandMark from '../components/BrandMark.vue'

type User = { id:string; email:string; displayName:string; status:string; roles:string[]; createdAt:string; balanceCents:number }
type Announcement = { id:string; title:string; content:string; severity:string; published:boolean; createdAt:string }
type OAuthSettings = { enabled:boolean; clientId:string; clientSecret:string; scopes:string; secretConfigured:boolean; minimumTrustLevel:number; publicBaseUrlConfigured:boolean; callbackUrl:string }
type CurrentUser = { ID:string; Email:string; DisplayName:string; Roles:string[] }

const props=defineProps<{page?:'overview'|'users'|'announcements'|'registration'|'mail'|'oauth'}>()
const page=computed(()=>props.page||'overview')
const pageTitle=computed(()=>({overview:'平台总览',users:'用户管理',announcements:'公告管理',registration:'注册策略',mail:'SMTP 邮件',oauth:'OAuth 认证'}[page.value]))

const summary=ref({users:0,products:0,activeDeployments:0})
const users=ref<User[]>([])
const announcements=ref<Announcement[]>([])
const currentUser=ref<CurrentUser|null>(null)
const error=ref('')
const message=ref('')
const busy=ref('')
const testRecipient=ref('')
const account=reactive({displayName:'',email:'',password:'',role:'user'})
const smtp=reactive({enabled:false,host:'',port:587,username:'',password:'',passwordConfigured:false,fromEmail:'',fromName:'CloudMeter',tlsMode:'starttls'})
const smtpReady=ref(false)
const auth=reactive({registrationEnabled:false,emailVerificationRequired:false,blockEmailAliases:true,emailDomainWhitelist:[] as string[]})
const github=reactive<OAuthSettings>({enabled:false,clientId:'',clientSecret:'',scopes:'read:user user:email',secretConfigured:false,minimumTrustLevel:0,publicBaseUrlConfigured:false,callbackUrl:''})
const linuxdo=reactive<OAuthSettings>({enabled:false,clientId:'',clientSecret:'',scopes:'openid email profile',secretConfigured:false,minimumTrustLevel:0,publicBaseUrlConfigured:false,callbackUrl:''})
const notice=reactive({title:'',content:'',severity:'info',published:true})
const credit=reactive({userId:'',amountCents:0,businessRef:'',note:'',expiresAt:''})
const editingUser=ref<User|null>(null)
const walletEdit=reactive({targetBalanceYuan:0,note:''})

const isSuperAdmin=computed(()=>currentUser.value?.Roles.includes('super_admin')===true)
const smtpFormReady=computed(()=>smtp.enabled&&smtp.host.trim().length>0&&smtp.port>=1&&smtp.port<=65535&&(!smtp.username.trim()||smtp.passwordConfigured||smtp.password.length>0)&&/^[^\s@]+@[^\s@]+$/.test(smtp.fromEmail.trim())&&['none','starttls','tls'].includes(smtp.tlsMode))

function done(text:string){message.value=text;error.value=''}
function failed(value:unknown){error.value=(value as Error).message;message.value=''}

async function load(){
  try{
    const me=await api<CurrentUser>('/me')
    currentUser.value=me
    if(!me.Roles.some(role=>role==='admin'||role==='super_admin')){location.replace('/console');return}

    const [summaryData,userData,noticeData]=await Promise.all([
      api<typeof summary.value>('/admin/summary'),
      api<{users:User[]}>('/admin/users'),
      api<{announcements:Announcement[]}>('/admin/announcements'),
    ])
    summary.value=summaryData
    users.value=userData.users
    announcements.value=noticeData.announcements
    if(!credit.userId&&users.value.length) credit.userId=users.value.find(item=>item.roles.includes('user'))?.id||users.value[0].id

    if(isSuperAdmin.value){
      const [mailData,authData,oauthData]=await Promise.all([
        api<typeof smtp & {ready:boolean}>('/admin/settings/mail'),
        api<typeof auth>('/admin/settings/auth'),
        api<{providers:(OAuthSettings&{provider:string})[]}>('/admin/settings/oauth'),
      ])
      const {ready,...mailSettings}=mailData
      smtpReady.value=ready
      Object.assign(smtp,mailSettings)
      Object.assign(auth,authData)
      oauthData.providers.forEach(item=>Object.assign(item.provider==='github'?github:linuxdo,item))
    }
    error.value=''
  }catch(value){
    failed(value)
  }
}

function scrollToCurrentSection(){
  const hash=location.hash.slice(1)
  if(!hash)return
  let id=hash
  try{id=decodeURIComponent(hash)}catch{}
  document.getElementById(id)?.scrollIntoView({block:'start'})
}
async function settleSectionPosition(){
  await nextTick()
  requestAnimationFrame(()=>requestAnimationFrame(scrollToCurrentSection))
}
function handleHashChange(){void settleSectionPosition()}
onMounted(async()=>{window.addEventListener('hashchange',handleHashChange);await load();await settleSectionPosition()})
onBeforeUnmount(()=>window.removeEventListener('hashchange',handleHashChange))

async function createUser(){
  try{
    busy.value='create-user'
    await api('/admin/users',{method:'POST',body:JSON.stringify(account)})
    Object.assign(account,{displayName:'',email:'',password:'',role:'user'})
    done('账户已创建')
    await load()
  }catch(value){failed(value)}finally{busy.value=''}
}
async function setUserStatus(user:User){
  const status=user.status==='active'?'suspended':'active'
  try{
    busy.value=user.id
    await api('/admin/users/'+user.id+'/status',{method:'PATCH',body:JSON.stringify({status})})
    user.status=status
    done(status==='active'?'账户已恢复':'账户已停用')
  }catch(value){failed(value)}finally{busy.value=''}
}
function openUserEditor(user:User){editingUser.value=user;walletEdit.targetBalanceYuan=user.balanceCents/100;walletEdit.note=''}
async function saveWalletBalance(){
  const user=editingUser.value;if(!user)return
  const target=Math.round(walletEdit.targetBalanceYuan*100),amount=target-user.balanceCents
  if(amount===0){failed(new Error('余额没有变化'));return}
  try{busy.value='wallet-adjust';const result=await api<{balanceCents:number}>('/admin/users/'+user.id+'/wallet/adjust',{method:'POST',body:JSON.stringify({amountCents:amount,businessRef:'admin-edit/'+crypto.randomUUID(),note:walletEdit.note.trim()||'管理员在用户编辑栏调整余额'})});user.balanceCents=result.balanceCents;done('用户余额已更新并写入调账账本');editingUser.value=null}catch(value){failed(value)}finally{busy.value=''}
}
async function impersonate(user:User,writeEnabled=false){
  if(writeEnabled&&!confirm('代用户执行写操作会记录真实管理员身份。确认继续？'))return
  const confirmation=writeEnabled?prompt('输入目标用户邮箱以启用写操作：'+user.email,'')||'':''
  if(writeEnabled&&!confirmation)return
  try{
    busy.value='impersonate-'+user.id
    const current=localStorage.getItem('session_token')||''
    const result=await api<{token:string}>('/admin/users/'+user.id+'/impersonation',{method:'POST',body:JSON.stringify({writeEnabled,confirmation})})
    localStorage.setItem('admin_session_token',current)
    localStorage.setItem('session_token',result.token)
    location.assign('/console')
  }catch(value){failed(value)}finally{busy.value=''}
}
async function saveAuth(){
  if(auth.emailVerificationRequired&&!smtpReady.value){failed(new Error('请先启用并保存有效的 SMTP 配置，再开启注册邮箱验证'));return}
  try{busy.value='auth';await api('/admin/settings/auth',{method:'PUT',body:JSON.stringify(auth)});done('注册策略已保存')}catch(value){failed(value)}finally{busy.value=''}
}
async function saveOAuth(provider:string,value:OAuthSettings){
  const payload={enabled:value.enabled,clientId:value.clientId,clientSecret:value.clientSecret,scopes:value.scopes,minimumTrustLevel:value.minimumTrustLevel}
  try{busy.value='oauth-'+provider;await api('/admin/settings/oauth/'+provider,{method:'PUT',body:JSON.stringify(payload)});value.clientSecret='';done('OAuth 设置已保存');await load()}catch(reason){failed(reason)}finally{busy.value=''}
}
async function copyCallback(value:string){
  try{await navigator.clipboard.writeText(value);done('回调地址已复制')}catch{failed(new Error('无法复制，请手动选择回调地址'))}
}
async function saveMail(){
  if(auth.emailVerificationRequired&&!smtpFormReady.value){failed(new Error('注册邮箱验证已启用，SMTP 必须保持启用并填写有效配置'));return}
  try{
    busy.value='mail'
    const result=await api<{ready:boolean;passwordConfigured:boolean}>('/admin/settings/mail',{method:'PUT',body:JSON.stringify(smtp)})
    smtpReady.value=result.ready
    smtp.passwordConfigured=result.passwordConfigured
    smtp.password=''
    done('SMTP 设置已保存')
  }catch(value){failed(value)}finally{busy.value=''}
}
async function testMail(){
  if(!testRecipient.value){error.value='请填写测试收件邮箱';return}
  try{busy.value='test-mail';await api('/admin/settings/mail/test',{method:'POST',body:JSON.stringify({email:testRecipient.value})});done('测试邮件已发送')}catch(value){failed(value)}finally{busy.value=''}
}
async function publish(){
  try{
    busy.value='publish-announcement'
    await api('/admin/announcements',{method:'POST',body:JSON.stringify(notice)})
    Object.assign(notice,{title:'',content:'',severity:'info',published:true})
    done('公告已创建')
    await load()
  }catch(value){failed(value)}finally{busy.value=''}
}
async function toggleAnnouncement(item:Announcement){
  try{busy.value=item.id;await api('/admin/announcements/'+item.id,{method:'PATCH',body:JSON.stringify({published:!item.published})});item.published=!item.published;done(item.published?'公告已发布':'公告已下线')}catch(value){failed(value)}finally{busy.value=''}
}
async function grantCredit(){
  if(!credit.userId||credit.amountCents<=0||!credit.businessRef.trim()){error.value='请选择账户并填写正整数额度和业务引用';return}
  try{
    busy.value='grant-credit'
    await api('/admin/users/'+credit.userId+'/credits',{method:'POST',body:JSON.stringify({...credit,amountCents:Math.trunc(credit.amountCents),businessRef:credit.businessRef.trim(),expiresAt:credit.expiresAt?new Date(credit.expiresAt).toISOString():null})})
    Object.assign(credit,{amountCents:0,businessRef:'',note:'',expiresAt:''})
    done('赠送额度已发放')
  }catch(value){failed(value)}finally{busy.value=''}
}
</script>

<template>

    <main class="workspace admin-workspace">
      <header id="overview">
        <div>
          <p class="eyebrow">{{isSuperAdmin?'超级管理员':'管理员'}}</p>
          <h1>{{pageTitle}}</h1>
        </div>
      </header>
      <p v-if="error" class="message sticky-message">{{error}}</p>
      <p v-if="message" class="status-ok sticky-message">{{message}}</p>

      <section v-if="page==='overview'" class="metrics">
        <article><small>平台账户</small><strong>{{summary.users}}</strong><span>含管理员与普通用户</span></article>
        <article><small>应用产品</small><strong>{{summary.products}}</strong><span>全部模板产品</span></article>
        <article><small>进行中部署</small><strong>{{summary.activeDeployments}}</strong><span>尚未完成的任务</span></article>
      </section>

      <section v-if="page==='users'" class="admin-section">
        <div class="section-heading">
          <div><p class="eyebrow">身份与权限</p><h2>{{isSuperAdmin?'账户管理':'账户目录'}}</h2></div>
          <span>{{users.length}} 个账户</span>
        </div>
        <div :class="['admin-split',{'read-only':!isSuperAdmin}]">
          <form v-if="isSuperAdmin" class="inline-form" @submit.prevent="createUser">
            <h3>创建账户</h3>
            <label>姓名<input v-model="account.displayName" required/></label>
            <label>邮箱<input v-model="account.email" type="email" required/></label>
            <label>初始密码<input v-model="account.password" type="password" minlength="12" required/></label>
            <label>角色<select v-model="account.role"><option value="user">用户</option><option value="admin">管理员</option></select></label>
            <button class="primary compact" :disabled="busy==='create-user'"><ShieldPlus :size="16"/>创建账户</button>
          </form>
          <div class="data-list">
            <article v-for="user in users" :key="user.id" class="data-row">
              <div><strong>{{user.displayName}}</strong><small>{{user.email}} · {{user.roles.join(' / ')}}</small></div>
              <span :class="['status-pill',user.status]">{{user.status==='active'?'正常':'已停用'}}</span>
              <span class="user-balance">¥{{(user.balanceCents/100).toFixed(2)}}</span>
              <template v-if="isSuperAdmin&&user.roles.includes('user')&&user.status==='active'">
                <button class="icon-action" title="只读查看用户控制台" :disabled="busy==='impersonate-'+user.id" @click="impersonate(user)"><Eye :size="18"/></button>
                <button class="icon-action" title="代用户执行操作" :disabled="busy==='impersonate-'+user.id" @click="impersonate(user,true)"><LogIn :size="18"/></button>
              </template>
              <button v-if="isSuperAdmin" class="icon-action" :title="user.status==='active'?'停用账户':'恢复账户'" :disabled="busy===user.id" @click="setUserStatus(user)"><CirclePause v-if="user.status==='active'" :size="18"/><CirclePlay v-else :size="18"/></button>
              <button v-if="isSuperAdmin" class="icon-action" title="编辑用户与余额" @click="openUserEditor(user)"><Settings2 :size="18"/></button>
            </article>
          </div>
        </div>
      </section>

      <section v-if="page==='users'&&isSuperAdmin" class="form-panel credit-admin">
        <h2><Coins :size="19"/>发放赠送额度</h2>
        <form @submit.prevent="grantCredit">
          <label>目标账户<select v-model="credit.userId" required><option v-for="user in users.filter(item=>item.status==='active')" :key="user.id" :value="user.id">{{user.displayName}} · {{user.email}}</option></select></label>
          <div class="field-row"><label>额度（分）<input v-model.number="credit.amountCents" type="number" min="1" step="1" required/></label><label>到期时间<input v-model="credit.expiresAt" type="datetime-local"/></label></div>
          <label>唯一业务引用<input v-model="credit.businessRef" maxlength="128" placeholder="campaign-2026-08/user-001" required/></label>
          <label>说明<input v-model="credit.note" maxlength="500"/></label>
          <button class="primary compact" :disabled="busy==='grant-credit'"><Coins :size="16"/>发放额度</button>
        </form>
      </section>

      <div v-if="isSuperAdmin&&['registration','mail','oauth'].includes(page)" class="admin-grid route-admin-grid">
        <section v-if="page==='registration'" class="form-panel">
          <h2><Globe2 :size="19"/>注册策略</h2>
          <form @submit.prevent="saveAuth">
            <label class="toggle"><input v-model="auth.registrationEnabled" type="checkbox"/>允许用户自行注册</label>
            <label :class="['toggle',{disabled:!smtpReady&&!auth.emailVerificationRequired}]"><input v-model="auth.emailVerificationRequired" type="checkbox" :disabled="!smtpReady&&!auth.emailVerificationRequired"/>注册时要求邮箱验证码</label>
            <p v-if="!smtpReady" class="configuration-hint"><CircleAlert :size="16"/>需先在右侧启用并保存有效 SMTP 配置</p>
            <label class="toggle"><input v-model="auth.blockEmailAliases" type="checkbox"/>阻止邮箱别名</label>
            <p v-if="auth.blockEmailAliases" class="configuration-hint neutral">开启后拒绝邮箱用户名部分包含 + 或 . 的地址</p>
            <label>邮箱域白名单<input :value="auth.emailDomainWhitelist.join(', ')" @input="auth.emailDomainWhitelist=($event.target as HTMLInputElement).value.split(',').map(item=>item.trim()).filter(Boolean)" placeholder="example.com, company.cn"/><small>留空允许全部域名；填写域名时也允许其子域名</small></label>
            <button class="primary compact" :disabled="busy==='auth'"><Save :size="16"/>保存策略</button>
          </form>
        </section>

        <section v-if="page==='mail'" class="form-panel">
          <h2><MailCheck :size="19"/>SMTP 邮箱</h2>
          <p :class="['configuration-status',smtpReady?'ready':'blocked']"><CheckCircle2 v-if="smtpReady" :size="16"/><CircleAlert v-else :size="16"/>{{smtpReady?'已保存，可用于注册邮箱验证':'尚未就绪，邮箱验证码暂不可用'}}</p>
          <form @submit.prevent="saveMail">
            <label class="toggle"><input v-model="smtp.enabled" type="checkbox"/>启用 SMTP</label>
            <div class="field-row"><label>主机<input v-model="smtp.host" :required="smtp.enabled" placeholder="smtp.example.com"/></label><label>端口<input v-model.number="smtp.port" type="number" min="1" max="65535" required/></label></div>
            <label>连接安全<select v-model="smtp.tlsMode"><option value="starttls">STARTTLS</option><option value="tls">TLS / SMTPS</option><option value="none">无加密</option></select></label>
            <label>用户名<input v-model="smtp.username" autocomplete="username"/></label>
            <label>密码<input v-model="smtp.password" type="password" autocomplete="new-password" :placeholder="smtp.passwordConfigured?'已加密保存，留空保持不变':'使用认证用户名时必填'"/><small v-if="smtp.passwordConfigured">密码已加密保存，接口不会显示原文</small></label>
            <div class="field-row"><label>发件邮箱<input v-model="smtp.fromEmail" type="email" :required="smtp.enabled"/></label><label>发件名称<input v-model="smtp.fromName"/></label></div>
            <button class="primary compact" :disabled="busy==='mail'"><Save :size="16"/>保存 SMTP</button>
          </form>
          <div class="mail-test"><label>测试收件邮箱<input v-model="testRecipient" type="email" placeholder="admin@example.com"/></label><button class="secondary compact" :disabled="busy==='test-mail'||!smtpReady" @click="testMail">发送测试邮件</button></div>
        </section>

        <section v-if="page==='oauth'" class="form-panel">
          <h2><KeyRound :size="19"/>GitHub OAuth</h2>
          <p v-if="!github.publicBaseUrlConfigured" class="configuration-status blocked"><CircleAlert :size="16"/>需先在部署环境配置 PUBLIC_BASE_URL</p>
          <form @submit.prevent="saveOAuth('github',github)">
            <label :class="['toggle',{disabled:!github.publicBaseUrlConfigured&&!github.enabled}]"><input v-model="github.enabled" type="checkbox" :disabled="!github.publicBaseUrlConfigured&&!github.enabled"/>启用 GitHub</label>
            <label>Client ID<input v-model="github.clientId"/></label>
            <label>Client Secret<input v-model="github.clientSecret" type="password" placeholder="留空保持不变" :required="github.enabled&&!github.secretConfigured"/></label>
            <p class="quiet">{{github.secretConfigured?'密钥已加密保存，接口不会显示原文':'尚未配置密钥'}}</p>
            <label>Scopes<input v-model="github.scopes"/></label>
            <label v-if="github.callbackUrl">OAuth 回调地址<div class="copy-field"><input :value="github.callbackUrl" readonly/><button type="button" class="icon-action" title="复制回调地址" @click="copyCallback(github.callbackUrl)"><Copy :size="17"/></button></div></label>
            <button class="secondary compact" :disabled="busy==='oauth-github'">保存 GitHub</button>
          </form>
        </section>

        <section v-if="page==='oauth'" class="form-panel">
          <h2><KeyRound :size="19"/>LinuxDo OAuth</h2>
          <p v-if="!linuxdo.publicBaseUrlConfigured" class="configuration-status blocked"><CircleAlert :size="16"/>需先在部署环境配置 PUBLIC_BASE_URL</p>
          <form @submit.prevent="saveOAuth('linuxdo',linuxdo)">
            <label :class="['toggle',{disabled:!linuxdo.publicBaseUrlConfigured&&!linuxdo.enabled}]"><input v-model="linuxdo.enabled" type="checkbox" :disabled="!linuxdo.publicBaseUrlConfigured&&!linuxdo.enabled"/>启用 LinuxDo</label>
            <label>Client ID<input v-model="linuxdo.clientId"/></label>
            <label>Client Secret<input v-model="linuxdo.clientSecret" type="password" placeholder="留空保持不变" :required="linuxdo.enabled&&!linuxdo.secretConfigured"/></label>
            <p class="quiet">{{linuxdo.secretConfigured?'密钥已加密保存，接口不会显示原文':'尚未配置密钥'}}</p>
            <label>Scopes<input v-model="linuxdo.scopes"/></label>
            <label>最低信任等级<select v-model.number="linuxdo.minimumTrustLevel"><option v-for="level in [0,1,2,3,4]" :key="level" :value="level">等级 {{level}}</option></select><small>禁言、未激活或低于该等级的账户无法登录</small></label>
            <label v-if="linuxdo.callbackUrl">OAuth 回调地址<div class="copy-field"><input :value="linuxdo.callbackUrl" readonly/><button type="button" class="icon-action" title="复制回调地址" @click="copyCallback(linuxdo.callbackUrl)"><Copy :size="17"/></button></div></label>
            <button class="secondary compact" :disabled="busy==='oauth-linuxdo'">保存 LinuxDo</button>
          </form>
        </section>
      </div>

      <section v-if="page==='announcements'" class="admin-section">
        <div class="section-heading"><div><p class="eyebrow">站内信息</p><h2>公告管理</h2></div><span>{{announcements.length}} 条记录</span></div>
        <div class="admin-split">
          <form class="inline-form" @submit.prevent="publish">
            <h3>创建公告</h3>
            <label>标题<input v-model="notice.title" required/></label>
            <label>内容<textarea v-model="notice.content" rows="5" required/></label>
            <label>级别<select v-model="notice.severity"><option value="info">信息</option><option value="warning">提醒</option><option value="critical">重要</option></select></label>
            <label class="toggle"><input v-model="notice.published" type="checkbox"/>立即发布</label>
            <button class="primary compact" :disabled="busy==='publish-announcement'"><Megaphone :size="16"/>创建公告</button>
          </form>
          <div class="data-list">
            <article v-for="item in announcements" :key="item.id" class="announcement-row">
              <div><span :class="['severity-dot',item.severity]"></span><strong>{{item.title}}</strong><small>{{item.content}}</small></div>
              <span :class="['status-pill',item.published?'active':'suspended']">{{item.published?'已发布':'草稿'}}</span>
              <button class="icon-action" :title="item.published?'下线公告':'发布公告'" :disabled="busy===item.id" @click="toggleAnnouncement(item)"><CirclePause v-if="item.published" :size="18"/><CirclePlay v-else :size="18"/></button>
            </article>
            <p v-if="!announcements.length" class="quiet empty-copy">还没有公告记录</p>
          </div>
        </div>
      </section>
    </main>
    <div v-if="editingUser" class="modal-backdrop user-editor-backdrop" @click.self="editingUser=null"><aside class="user-editor"><header><div><p class="eyebrow">编辑用户</p><h2>{{editingUser.displayName}}</h2><small>{{editingUser.email}}</small></div><button class="icon-action" title="关闭" @click="editingUser=null"><X :size="18"/></button></header><form @submit.prevent="saveWalletBalance"><section><h3>基本信息</h3><label>显示名称<input :value="editingUser.displayName" disabled/></label><label>邮箱<input :value="editingUser.email" disabled/></label></section><section><h3>余额与调账</h3><label>目标余额（CNY）<input v-model.number="walletEdit.targetBalanceYuan" type="number" min="0" step="0.01" required/><small>当前 ¥{{(editingUser.balanceCents/100).toFixed(2)}}，保存时自动写入差额账本</small></label><label>管理员备注<textarea v-model="walletEdit.note" rows="4" maxlength="500" placeholder="本次余额调整原因"/></label></section><footer><button type="button" class="secondary" @click="editingUser=null">取消</button><button class="primary" :disabled="busy==='wallet-adjust'"><Save :size="17"/>保存更改</button></footer></form></aside></div>
</template>
