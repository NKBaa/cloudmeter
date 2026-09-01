# AI 应用云部署与按量计费平台：最终工程规格

## 1. 目标与边界

构建部署在云端 Linux 环境上的 AI 应用平台。普通用户可注册、购买套餐、充值余额、按量使用并部署管理员上架的应用；管理员可运营用户、应用产品、版本、价格、订单、支付、退款和审计。

必须实现：网页首次初始化、严格后端权限隔离、管理员用户视角，普通用户视角、应用模板部署、同用户容器互访、独立计费项定价、预付费钱包、EPay 适配、应用更新与回退、单 IP+端口入口、路径访问。

明确不做：Kubernetes、多节点、GPU、自动续费、多币种、复杂监控、企业级容灾、任意镜像上传、平台管理 DNS/证书、复杂 Token/请求计费。

## 2. 公网与部署形态

用户只提供一个二级域名，例如 cloud.example.com。外部 OpenResty 负责 HTTPS、证书、DNS 和全部反向代理；平台只暴露一个宿主机业务端口，例如 8080。

Browser -> OpenResty -> SERVER_IP:8080 -> internal gateway -> platform web/API or user containers

Compose 只有 gateway 使用 ports；PostgreSQL、Redis、API、Worker、网关管理端口和应用容器只在 Docker 内网可达。OpenResty 必须转发 Host、X-Forwarded-*、WebSocket Upgrade，流式响应关闭缓冲。

URL 约定：/、/setup、/login、/register、/console/*、/admin/*、/api/*、/payments/*、/apps/{user_slug}/{app_slug}/*。应用使用 Path 模式；产品必须声明 Base Path、是否移除前缀、健康检查、WebSocket/SSE、Cookie Path。

## 3. 技术架构

- apps/web：Vue 3 + TypeScript + Vite 用户端、管理端、初始化页。
- apps/api：Go HTTP API。
- apps/worker：Go 异步 Worker，执行 Docker、健康检查、路由切换、回退、计量和到期任务。
- PostgreSQL + Redis + Docker Engine；内部网关使用 Traefik 或轻量路由服务。

Docker 网络：edge_network（网关及公开应用）、platform_network（网关/Web/API）、data_network（API/Worker/数据库）、user_net_{user_id}（该用户全部应用）。同用户容器以稳定 service_slug 互访，如 http://ollama:11434；跨用户隔离；应用不得访问数据库、Redis、Docker Socket、host 网络或 privileged 模式。

## 4. 项目目录

project/
├─ apps/web/                    # 用户端、管理端、初始化页
├─ apps/api/                    # Go API 入口
├─ apps/worker/                 # 部署、计量、扣费、到期任务
├─ internal/                    # auth、rbac、users、setup、audit、products、apps、deployments、runtime、gateway、plans、pricing、usage、billing、wallet、payments、refunds、notifications、secrets、storage、settings
├─ migrations/                  # PostgreSQL 迁移
├─ packages/web-ui/             # 前端共享组件
├─ deploy/                      # Compose、Dockerfile、Ubuntu/WSL 脚本、Secret 示例
├─ configs/                     # 环境变量和价格示例
├─ docs/                        # API、EPay、OpenResty、运维、测试
├─ tests/                       # integration、e2e、billing、permissions、deployment
└─ README.md

## 5. 核心业务

首次访问 /setup 检查初始化状态并创建唯一超级管理员。向导只要求管理员姓名、邮箱和密码；平台名称、注册策略与默认套餐由系统采用安全默认值。状态保存在 system_state.initialized_at、initialized_by、installation_id；并发初始化用事务和全局锁；成功后 Setup 写接口永久关闭。

角色至少为 super_admin、admin、user。每个 API 必须检查身份、角色、资源所有权、资源状态、订阅和配额；普通用户访问 /api/admin/* 返回 403。用户视角默认只读，代操作需明确开启和二次确认，审计同时记录 actor_user_id 与 subject_user_id。

管理员可以网页创建应用产品和版本：镜像 Digest、启动命令、端口、健康检查、资源范围、卷、环境变量/Secret 表单、依赖服务、Path 路由、Base Path、WebSocket/SSE 和更新策略。所有产品统一使用平台全局价格。新产品先测试部署，再发布上架，无需改代码或重启 Compose。

每次成功部署生成不可变 app_release，保存镜像 Digest、资源、卷、环境变量快照、Secret 版本、路由和健康检查。更新流程为保存旧快照、拉取 Digest、启动新发布、健康检查、切换路由；失败自动恢复 last_successful_release_id。数据卷默认不自动回退，产品声明 stateless、volume_compatible 或 backup_required。

## 6. 套餐、独立计费与钱包

套餐包含周期价、应用数、CPU/内存/存储/公网配额、并发任务、可用产品、赠送额度和超额策略；用户订阅保存权益快照。到期进入约 3 天宽限期，结束后停止应用并关闭路由。

每项单独配置并独立版本化：CPU 核小时、内存 GiB 小时、系统盘 GiB 天、数据盘 GiB 天、公网出站 GiB、应用运行基础费、公网入口、备份存储、备份操作、部署费、产品授权费。每项有编码、单位、单价、精度、舍入、最小粒度、免费额度、超额开关、计费开始/停止条件、生效时间。价格解析顺序为用户专属、产品覆盖、套餐绑定、平台默认；账期开始时保存价格快照。

推荐预付费：EPay/人工充值 -> 钱包入账 -> Worker 按运行区间计量 -> 赠送额度抵扣 -> 钱包扣款 -> 余额预警/欠费停机。账本不可修改，只能追加冲正；每次充值、消费、退款、赠送、调账有唯一业务引用。最终金额用整数分，中间计算使用 PostgreSQL NUMERIC，禁止 float。

计量规则：Docker running 后计 CPU/内存；卷存在即计存储；同用户内部流量免费；仅公网出站计量。每 5 分钟聚合、每小时封存，幂等键包含账户、应用、计量项、窗口和价格版本；Worker/服务器重启后对账补齐，无法确认的区间不自动扣费。

## 7. 支付、更新和安全

实现 PaymentProvider：CreatePayment、VerifyCallback、QueryPayment、ClosePayment；首期 manual 和 epay。EPay 字段、签名排序、编码、成功响应必须以目标服务商文档为准。异步通知必须验签、校验金额/商户号/订单号/状态、限流、幂等；同步 return 只展示状态。

禁止任意镜像、特权、host 网络、宿主机路径和 Docker Socket；Secret 加密且不回显；受信代理 CIDR 白名单后才接受 X-Forwarded-*；未知 Host/未授权应用路由拒绝；所有管理员代操作、调账、退款、支付配置修改、产品发布、应用更新/回退写入审计。

## 8. 数据表建议

users、roles、permissions、user_roles、sessions、system_state、audit_logs、impersonation_sessions；app_products、app_product_versions、product_fields、product_dependencies、product_volumes、user_apps、app_releases、deployment_jobs、deployment_steps、deployment_events、app_routes、app_secrets、app_secret_versions、backups；plans、plan_versions、plan_entitlements、user_subscriptions、user_entitlements、pricing_items、pricing_versions、billing_accounts、usage_intervals、usage_events、usage_aggregates、bills、bill_items、credit_grants、credit_consumptions；orders、order_items、order_events、payments、payment_events、payment_provider_configs、refunds、refund_events、wallets、wallet_transactions、wallet_ledger_entries。

## 9. 测试与最终验收

必须在 WSL Ubuntu 完成空环境 Compose 安装、网页初始化、用户注册、人工充值、按量计量扣费、应用部署、同用户互访、跨用户隔离、更新成功、健康检查失败自动回退、Worker/容器重启恢复、EPay 重复/错误回调和迁移升级。

验收标准：宿主机只公开一个 IP+端口；初始化并发只能成功一次且完成后关闭；普通用户无法越权；管理员可后台添加新产品；应用可部署/更新/回退；同用户可用稳定服务名互访；每项价格独立且历史账单可追溯；充值和扣费幂等；欠费能停机、充值后可恢复；OpenResty 只需反代唯一二级域名到该端口；密码、支付密钥和 Secret 不泄露。

## 10. 实施顺序

工程骨架与 Compose -> Setup/认证/权限 -> 产品与首次部署 -> Docker Worker/网络/路径 -> 更新与回退 -> 套餐和价格版本 -> 钱包账本 -> 用量聚合与扣费 -> 人工支付 -> EPay -> 用户端/管理端完善 -> WSL 全链路与升级恢复测试。

## 11. 交给实施 AI 的硬性指令

先建立数据库迁移、OpenAPI 和状态机，再实现页面。所有权限必须由后端强制执行。所有财务金额不得使用浮点数；支付、充值、扣费、权益发放和账单必须事务化、幂等化并可重试。EPay 未有目标服务商文档时只能实现适配器、模拟支付和待配置状态。镜像必须固定 Digest；发布记录不可变；更新失败必须自动回退。用户只能部署管理员已发布模板。平台只暴露一个 IP+端口，OpenResty、域名和 HTTPS 在平台外部管理。初始化必须以事务和全局锁保证仅执行一次，禁止默认 admin/admin。先用人工支付完成闭环，再接真实 EPay；先通过 WSL 空环境、升级和重启恢复测试，再宣称部署完成。
