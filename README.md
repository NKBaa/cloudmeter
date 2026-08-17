# CloudMeter

CloudMeter 是面向云端环境的 AI 应用部署与按量计费平台。本仓库当前完成第一阶段工程基线：核心数据库迁移、OpenAPI、部署与订单状态机、并发安全的网页初始化、登录会话、后端 RBAC、Worker 任务认领、Vue 控制台骨架，以及仅暴露一个业务端口的 Compose 拓扑。

## 当前能力

- 首次初始化仅创建唯一超级管理员；串行化事务与 PostgreSQL 全局事务锁保证只能初始化一次。
- 唯一超级管理员、默认免费套餐、钱包和审计记录在同一事务创建。
- 后端会话令牌只存 SHA-256 摘要；密码使用 bcrypt。
- 普通身份无法访问 /api/admin/*，权限判断不依赖前端。
- 钱包账本、应用发布快照由数据库触发器禁止修改或删除。
- 镜像字段必须使用 name@sha256:digest；部署任务以唯一幂等键入队。
- Worker 会通过受控 Docker Engine 客户端创建固定 Digest 容器、按用户创建内部网络、探测容器状态并写入 deployment_events；runtime 安全策略拒绝 privileged、host network、Docker Socket 和宿主机路径。
- Compose 仅 gateway 映射宿主机端口；`app-router` 从数据库读取活动路由，并通过用户内部网络转发 `/apps/{user_slug}/{app_slug}`。
- 迁移 58 预置 11 个标准费用项，并将系统盘、数据盘用量拆分计量；目录默认不带价格，管理员按需发布不可变价格版本。
- 用量生成时冻结价格快照；当时没有适用价格的窗口永久封存为 `unpriced`，不扣费且不因后续配置价格而追溯收费。
- 全额退款使用独立不可变记录与事件时间线；订单、退款、钱包负向账本和管理员审计在一个事务中完成，并发重放不会重复扣回余额。
- 审计日志由数据库触发器禁止更新、删除或清空；管理端只提供超级管理员可读的筛选与主键游标分页。

## 启动

需要 Docker Engine 与 Docker Compose。复制 configs/.env.example 为仓库根目录的 .env，替换其中的密码、令牌和加密主密钥；把 `PUBLIC_BASE_URL` 设置为用户实际访问平台的 HTTP(S) 源地址（只含协议、主机和可选端口，不含路径、查询或片段），并把 `PLATFORM_ALLOWED_HOST` 设置为同一地址的纯主机名。`GATEWAY_TRUSTED_PROXY_CIDRS` 只填写外部 OpenResty 到平台入口时可能使用的源网段，并应尽量收窄。然后运行：

```bash
docker compose --env-file .env -f deploy/compose.yaml up -d --build
```

浏览器打开 http://localhost:8080/setup。初始化页只要求管理员姓名、邮箱和密码；完成后会直接建立管理员会话。外部 OpenResty 配置参考 deploy/openresty.conf.example，域名、证书和 HTTPS 均由平台外部管理。

从空服务器部署时，请按 [Docker 部署指南](docs/docker-deployment.md) 完成环境检查、密钥生成、Docker Socket 权限、首次初始化、健康验证、升级和数据保留。生产升级不得使用 `docker compose down -v`。

## 验证

```bash
go test ./...
cd apps/web && npm install && npm run build
docker compose --env-file .env -f deploy/compose.yaml config --quiet
bash deploy/verify.sh
# Windows Docker Desktop:
# powershell -ExecutionPolicy Bypass -File deploy/verify.ps1
# powershell -ExecutionPolicy Bypass -File deploy/verify-account-features.ps1
# powershell -ExecutionPolicy Bypass -File deploy/verify-app-controls.ps1
```

账户专项脚本会使用临时 MailHog 实测 SMTP 配置不变量、验证码发送和限流、五次错误锁定、邮箱域白名单、邮箱别名阻止、注册与登录、公告可见性及审计记录。WSL/Linux 使用 `bash deploy/verify-account-features.sh`；若 WSL 要调用 Windows Docker Desktop CLI，可设置 `DOCKER_BIN="/mnt/c/Program Files/Docker/Docker/resources/bin/docker.exe"`。脚本会恢复原有注册、SMTP 与公告状态，不删除 PostgreSQL、Redis、备份或应用卷。

会话注销专项验收使用 `deploy/verify-session-logout.ps1`，会验证服务端撤销 `sessions.revoked_at`、旧令牌收到 `401`，并检查不可变 `auth.logout` 审计记录。前端退出会同时撤销当前代操作会话和原管理员会话；API 收到已失效令牌时会清理本地会话并回到登录页。

应用生命周期专项验收同时提供 `deploy/verify-app-controls.ps1` 和 `deploy/verify-app-controls.sh`。它真实覆盖产品测试与发布、声明 Secret 的初始加密与追加轮换、未声明 Secret 拒绝、启动/停止/暂停门禁、数据卷跨重启保留、备份与恢复、辅助容器清理，以及重建应用 Router 后公开路由恢复。它还会从数据库边界验证用户与应用公开标识、最后成功 Release 和活动路由目标不能被伪造。WSL/Linux 运行时应使用独立的 `COMPOSE_PROJECT_NAME` 和 `PLATFORM_PORT`，避免与正式栈共享 Docker 资源或宿主机端口；完整命令见 `docs/operations.md`。

## 已完成能力

产品模板 CRUD、固定 Digest 版本发布、用户应用创建、幂等部署任务、Docker Engine 执行、按用户内部网络、容器稳定服务别名、容器稳定健康观察、失败回退、HTTP 路径转发、原子路由切换及旧容器回收已完成。产品标识创建后保持不变，名称可编辑；下架采用可审计的软删除，停止新部署但保留现有应用、Release 与版本历史。测试进行中或仍被其他已上架产品依赖时不能下架，恢复上架前会重新校验已发布版本的依赖。产品版本可声明同账户依赖服务；依赖固定到已发布产品与稳定 `serviceSlug`，平台拒绝自身、未知和循环依赖。必需依赖未运行时产品目录会返回 `missingDependencies` 并禁止创建、更新或回退，Worker 在任务执行前还会核对真实容器健康；内部服务名自动加入 `NO_PROXY/no_proxy`。每个新产品版本必须先完成隔离测试部署才可发布：测试使用不可变镜像快照、独立内部网络、临时容器和 tmpfs 数据卷；测试 Secret 仅在执行期间解密，完成或失败后立即从数据库清除。发布 API 与数据库触发器都要求存在成功测试，不能绕过管理页面直接上架。应用 Secret 只能使用该产品已发布版本声明的键；控制台从 API 的 `allowedKeys` 选择键，Worker 按目标 Release 的 `runtimeSpec.secretKeys` 再次过滤并校验引用，旧快照中的额外键不会进入容器环境。备份恢复、带 3 天宽限期的订阅套餐、价格版本、钱包扣费、人工充值、退款、SMTP/邮箱验证、邮箱域白名单与别名策略、GitHub/LinuxDo OAuth 和公告系统已接入。备份容量在 API 预留和 Worker 完成复核两个阶段都使用精确十进制计算；备份及恢复记录由数据库强制归属、状态迁移和终态不可变。SMTP、OAuth 与支付凭据使用 API 独占主密钥执行 AES-256-GCM 静态加密。Worker 是唯一可以挂载 Docker Socket 的服务；生产环境需配置 `DOCKER_EXECUTOR_ENABLED=true`、`DOCKER_SOCKET_PATH` 与 Socket 的 `DOCKER_GID`。EPay 已支持配置后生成签名支付 URL，并通过回调验签、金额/商户/订单校验和幂等入账；在取得目标服务商正式文档前仍建议使用模拟服务商验收。WSL 运维与重启验收步骤见 `docs/operations.md`，可执行检查脚本为 `deploy/verify.sh`。

公网入口由 Caddy 对 `PLATFORM_ALLOWED_HOST` 做精确 Host 白名单检查，其他 Host 返回 `421`。Caddy 只从 `GATEWAY_TRUSTED_PROXY_CIDRS` 接受外部 OpenResty 的转发头，并向内部服务覆盖为规范化的单一客户端 IP；API 仅信任内部网关网段。应用 Router 还要求至少 32 字符的独立内部令牌，浏览器不能直接访问 Router。

应用 Router 只把 `app_routes` 当作“此应用当前公开”的开关，不信任表内可写的路径、主机或端口；实际路径、Release 网络别名和端口分别从不可变用户/应用身份及 Release 快照重新推导。迁移 65 固化用户 slug、应用归属、产品、slug、稳定服务名和创建时间，校验最后成功 Release 归属，并要求活动路由只能指向同一应用当前成功且健康的活动 Release。

产品生命周期、版本测试与发布的专项回归脚本为 `deploy/verify-product-version-tests.ps1`（Windows）和 `deploy/verify-product-version-tests.sh`（WSL/Linux），会覆盖标识校验、产品身份字段的数据库不可变约束、名称编辑、软下架与恢复、目录可见性、审计幂等、隔离测试、Secret 清理、版本与测试证据的更新/删除/清空保护、重复发布和孤立运行时回收。验收结束后临时产品会软下架并保留为证据。

产品依赖专项回归脚本为 `deploy/verify-product-dependencies.ps1`（Windows）和 `deploy/verify-product-dependencies.sh`（WSL/Linux），会覆盖畸形、自身、未知和循环依赖，产品目录就绪状态，API 部署门禁，排队后的 Worker 二次校验，以及同账户稳定别名和代理绕过变量。

计费恢复可执行验收已覆盖 Windows 与 WSL：`deploy/verify-billing-recovery.ps1` 和 `deploy/verify-billing-recovery.sh` 会实际验证运行时用量扣费、低余额预警、余额不足停机、路由移除、公开充值入账、通过部署状态机自动恢复，以及通知、扣费和钱包账本幂等。

Worker 的运行时计量以 Docker `stats` 能确认容器存在为前提，CPU、内存和系统盘按产品版本声明容量换算，数据盘还会核对实际 Docker 卷。无价格窗口会直接封存为 `unpriced`，既不扣费，也不阻止欠费账户充值后恢复。三项财务专项脚本会复用完整参数一致的验收价格版本，可连续运行以验证财务幂等性。

用户控制台会展示最近钱包账本，包含充值、用量扣费、退款、赠送、调账、冲正、发生时间和入账后余额。通知接口账户隔离可通过 `deploy/verify-notification-access.ps1` 或 `deploy/verify-notification-access.sh` 重复验收。

月度账单已按 UTC 自然月接入用户控制台，支持展开不可变费用明细和导出 UTF-8 CSV。Worker 在成功扣费事务内同时写入价格快照和更新账单总额；迁移会从已有 `usage_charges` 回填历史账单。专项验收脚本为 `deploy/verify-billing-statements.ps1` 和 `deploy/verify-billing-statements.sh`。

账户级赠送额度支持可选到期时间、唯一业务引用和管理员审计。Worker 按最早到期优先抵扣，额度不足时才扣钱包；用户控制台显示可用额度、批次余额和最近消费。专项验收脚本为 `deploy/verify-credit-grants.ps1` 和 `deploy/verify-credit-grants.sh`。

套餐版本可配置每月赠送额度。平台按 UTC 自然月向启用账户自动发放，额度在下一个 UTC 月初到期；同月重复分配不会重复发放，升级只补发新旧目标的正差额，降级不会追回已发放或已消费额度。API 分配套餐与 Worker 周期对账共用事务锁定的数据库函数，专项验收脚本为 `deploy/verify-plan-credit-grants.ps1` 和 `deploy/verify-plan-credit-grants.sh`。

用户可在控制台主动购买开放套餐，并在当前周期内手动续期或变更套餐；平台不自动续费。有效周期内升级只扣除当前套餐与目标套餐的正差价，降级或切换到更低价格的套餐不退款。管理员可把套餐设为“开放购买”或“仅管理员分配”；关闭购买只阻止新的自助购买，已有订阅用户仍可续期当前套餐。订阅购买、钱包账本、账单明细、通知与审计保持同一事务边界。Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-subscription-purchases.ps1`，Linux/WSL 执行 `bash deploy/verify-subscription-purchases.sh` 完成专项验收。

充值订单管理支持提供商状态查询和待处理订单关单。人工支付查询/关单已闭环；EPay 主动查询/关单在正式服务商文档未配置前保持待配置状态，所有操作结果写入不可变 `payment_provider_operations` 并审计。

人工充值的全额退款已形成独立的 `refunds` / `refund_events` 审计链。管理端可填写退款原因、查看操作人、账本引用和事件时间线；迁移 59 只回填具有精确负向钱包账本的历史退款订单。EPay 在目标服务商退款协议未配置时明确返回待配置，保持订单与钱包不变并记录拒绝审计，不会伪造服务商退款成功。Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-payments.ps1`，Linux/WSL 执行 `bash deploy/verify-payments.sh`，可验证并发幂等、钱包净变化、订单/账本/事件/审计一致性和数据库不可变约束。

迁移 60 将 `audit_logs` 固化为数据库级追加写记录，`UPDATE`、`DELETE` 与 `TRUNCATE` 都会被拒绝。迁移 61 同样禁止清空 `refunds` 与 `refund_events`，避免通过表级操作绕过退款历史的行级保护。迁移 63 进一步禁止删除或清空产品、产品版本、测试证据和应用 Secret 版本，并补齐钱包账本与应用 Release 的清空保护；迁移 64 固化应用 Secret 的 `id`、应用归属、键名与创建时间，并禁止删除或清空父记录；迁移 65 固化公开路由身份并阻止跨应用 Release 指针；迁移 66 固化 Release、部署任务、停止任务和部署事件的父子归属及状态历史；迁移 67 对备份与恢复任务应用同样的归属、状态机和不可变历史保护。Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-audit-logs.ps1`，Linux/WSL 执行 `bash deploy/verify-audit-logs.sh`，可验证匿名与普通用户门禁、超级管理员筛选、游标分页，以及三类数据库破坏操作全部失败；支付、产品和应用生命周期专项脚本会验证其余关键历史边界。

完整边界与验收要求见 FINAL_PROJECT_SPEC.md，API 契约见 docs/openapi.yaml，状态机约束见 docs/state-machines.md。
