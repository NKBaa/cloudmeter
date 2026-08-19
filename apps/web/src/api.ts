export type ApiError = { error?: { code?: string; message?: string } }

const errorMessages: Record<string, string> = {
  smtp_required_for_email_verification: '请先启用并保存有效的 SMTP 配置，再开启注册邮箱验证',
  email_verification_requires_smtp: '注册邮箱验证已启用，请先关闭该策略再停用或清空 SMTP',
  invalid_email_domain_whitelist: '邮箱域白名单包含无效域名，请填写 example.com 这类域名',
  smtp_not_configured: 'SMTP 尚未启用或配置不完整',
  smtp_send_failed: '邮件发送失败，请检查 SMTP 地址、凭据和连接安全设置',
  verification_rate_limited: '验证码发送过于频繁，请稍后再试',
  registration_disabled: '当前暂未开放公开注册',
  email_policy_blocked: '该邮箱不符合当前注册策略',
  invalid_verification_code: '验证码无效或已过期，请重新获取',
  oauth_public_base_url_required: '启用 OAuth 前需在部署环境配置有效的 PUBLIC_BASE_URL',
  app_stop_in_progress: '应用正在停止，请等待当前任务完成',
  app_suspended: '应用已被平台暂停，请先处理余额或套餐问题',
  app_not_running: '只有运行中的应用可以执行此操作',
  app_operation_in_progress: '应用有正在执行的部署或恢复任务',
  app_required_by_dependents: '该应用仍被其他运行中的应用依赖，请先停止依赖它的应用',
  app_not_stopped: '只有已停止或部署失败的应用可以重新启动',
  successful_release_required: '应用没有可用于启动的成功版本',
  idempotency_conflict: '此操作标识已用于其他请求，请重试',
  deployment_in_progress: '应用已有正在执行的部署任务',
  subscription_required: '需要有效套餐才能启动应用',
  product_not_in_plan: '当前套餐不包含此应用产品',
  public_ingress_quota_exceeded: '公网入口额度已用完，请升级套餐或停止其他应用',
  required_dependency_unavailable: '必需的依赖应用尚未运行',
  product_retired: '该产品已下架，请先在产品管理中恢复',
  product_test_in_progress: '产品仍有测试任务进行中，暂时不能下架',
  product_is_dependency: '仍有已上架产品依赖此产品，请先处理依赖关系',
  product_dependency_conflict: '产品依赖当前不可用，无法完成此操作',
  required_secret_missing: '应用缺少必需的 Secret，请先补充配置',
  secret_not_declared: '该 Secret 未由此产品的已发布版本声明，不能保存',
  insufficient_balance: '钱包余额不足，请先充值后再继续',
  refund_in_progress: '退款正在处理，请稍后刷新查看结果',
  order_not_refundable: '只有已入账且未退款的订单可以退款',
  payment_provider_refund_unconfigured: '该支付渠道尚未配置服务商退款接口，订单和钱包均未变更',
  plan_version_unavailable: '该套餐当前不可购买或版本已失效',
  subscription_no_change: '当前已经是这个套餐，无需重复购买',
  prepaid_cycles_locked: '当前套餐仍有预付周期，请等本周期开始后再变更',
  account_unavailable: '账户当前不可用，请重新登录后再试',
  unauthenticated: '登录状态已失效，请重新登录',
  forbidden: '你没有执行此操作的权限',
  last_super_admin: '平台至少需要保留一名活跃超级管理员',
  super_admin_role_immutable: '超级管理员权限受保护，不能在这里降级',
  user_not_found: '没有找到这个用户，列表可能已经发生变化，请刷新后重试',
  ticket_not_found: '没有找到这个工单，或你没有查看权限',
  ticket_closed: '工单已经关闭，不能继续回复',
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = localStorage.getItem('session_token')
  const headers = new Headers(options.headers)
  headers.set('Content-Type', 'application/json')
  if (token) headers.set('Authorization', `Bearer ${token}`)
  const response = await fetch(`/api${path}`, { ...options, headers })
  const body = (await response.json().catch(() => ({}))) as T & ApiError
  if (!response.ok) {
    if (response.status === 401 && token) {
      localStorage.removeItem('admin_session_token')
      localStorage.removeItem('session_token')
      if (location.pathname !== '/login') location.assign('/login')
    }
    const code = body.error?.code || ''
    throw new Error(errorMessages[code] || body.error?.message || `请求失败（${response.status}）`)
  }
  return body
}

async function revokeSession(token: string): Promise<void> {
  const response = await fetch('/api/auth/logout', {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
  })
  if (!response.ok && response.status !== 401) {
    throw new Error(`注销失败（${response.status}）`)
  }
}

export async function logout(): Promise<void> {
  const sessionToken = localStorage.getItem('session_token')
  const adminSessionToken = localStorage.getItem('admin_session_token')
  const tokens = [sessionToken, adminSessionToken].filter((token, index, all): token is string => Boolean(token) && all.indexOf(token) === index)
  await Promise.allSettled(tokens.map((token) => revokeSession(token)))
  localStorage.removeItem('admin_session_token')
  localStorage.removeItem('session_token')
  location.assign('/login')
}
