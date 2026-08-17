# 运维与验收

## 首次部署

复制 `configs/.env.example` 为根目录 `.env`，替换密码和令牌。`PUBLIC_BASE_URL` 必须设置为用户实际访问平台的 HTTP(S) 源地址，例如 `https://cloud.example.com`；它只允许协议、主机和可选端口，不能包含路径、查询或片段。`PLATFORM_ALLOWED_HOST` 填同一地址的纯主机名 `cloud.example.com`，不能带协议、端口或路径。`GATEWAY_TRUSTED_PROXY_CIDRS` 是允许向平台入口提供 `X-Forwarded-*` 的外部 OpenResty 源网段；同机 Docker 发布端口通常可从 `172.16.0.0/12` 起步并根据实际 bridge 地址收窄。`API_TRUSTED_PROXY_CIDRS` 仅覆盖内部 Caddy 网络。执行 `docker compose --env-file .env -f deploy/compose.yaml up -d --build`，然后打开 `/setup`，仅填写管理员姓名、邮箱和密码来创建唯一超级管理员。初始化成功后页面会直接进入已登录的管理后台。

需要实际创建用户应用时，将 `DOCKER_EXECUTOR_ENABLED=true`，并确认 `DOCKER_SOCKET_PATH` 指向 Docker Engine Socket；Socket 只挂载到 Worker，API、Web、Router 和用户容器不会获得该权限。

首次启动前必须生成一次 `SECRETS_ENCRYPTION_KEY`：

```bash
openssl rand -base64 32 | tr -d '=\n'
```

将结果写入 `.env`。该 32 字节主密钥只注入 API 和执行应用部署的 Worker，用于 AES-256-GCM 加密 SMTP 密码、OAuth Client Secret、支付密钥和应用 Secret。必须与数据库备份一起离线保管；直接更换或遗失会导致已保存凭据无法解密。API 启动时会验证平台凭据密文，并自动将旧版本明文凭据原地升级为带版本前缀的密文。应用 Secret 每次修改都会创建不可变版本，Release 只保存版本引用，Worker 仅在创建容器时解密。

## 升级

保留 `postgres_data`、`redis_data` 和应用卷，执行：

```bash
docker compose --env-file .env -f deploy/compose.yaml run --rm migrate
docker compose --env-file .env -f deploy/compose.yaml up -d --build api worker app-router web gateway
```

不要使用 `down -v`。

## 产品目录生命周期

产品标识只在创建时设置，用于稳定路径、依赖和历史引用；后续只允许编辑展示名称。迁移 62 在数据库层保护产品的 `id`、`slug` 与 `created_at`，即使绕过 API 也不能篡改产品身份。后台“下架产品”采用软删除：普通用户目录立即隐藏该产品并拒绝新建版本、测试和发布，但现有用户应用、运行容器、Release 和版本快照都保持不变。重新恢复时，有已发布版本的产品回到 `published`，只有测试记录的产品回到 `testing`，从未测试的产品回到 `draft`。

产品有非终态版本测试时不能下架。其他已上架产品的已发布版本仍声明此产品为依赖时也不能下架；应先下架依赖方，再下架被依赖产品。恢复一个已有发布版本的产品前，API 会再次验证其每个已发布版本的依赖仍然可用。创建、名称变更、版本创建、下架和恢复全部写入追加式审计日志；重复提交相同状态不会重复写审计。

## 产品版本测试与发布

管理员创建产品版本后，先在“产品管理”中选择“测试部署”。每个测试固定使用请求当时的镜像 Digest、运行参数、路由与健康检查快照；同一版本同时只能有一个未完成测试。声明了 Secret 的版本会在测试弹窗中要求输入值，值只加密保存到该测试记录，不会写入产品版本或用户应用 Secret。

Worker 会拉取固定 Digest，在 `cm-test-net-*` 内部网络中创建 `cm-test-*` 临时容器。声明的数据卷会变成 tmpfs，不会挂载或修改任何用户卷；容器不会自动重启。若配置出口代理，测试容器只能通过该代理访问公网，测试流量不进入用户计费。健康检查每隔版本配置的间隔执行，最多 8 次。成功或失败后 Worker 清除测试 Secret，并强制删除临时容器和网络；重启后的对账也会回收没有活动测试记录的残留容器和网络。

只有成功测试的版本能发布。`POST /api/admin/products/{productID}/versions/{versionID}/publish` 会返回 `409 successful_test_required`，数据库触发器也会拒绝任何绕开 API 的 `published_at` 更新。测试失败可以重新发起，失败原因会显示在产品版本记录中。

产品版本测试的完整回归可执行：Windows 使用 `powershell -ExecutionPolicy Bypass -File deploy/verify-product-version-tests.ps1`，WSL/Linux 使用 `bash deploy/verify-product-version-tests.sh`。脚本要求 Worker 已启用 Docker Executor，并使用固定 Digest 的 Nginx 作为临时产品版本；它实际验证产品标识校验、产品 `id`/`slug`/`created_at` 数据库不可变、名称更新幂等、测试期间禁止下架、发布后软下架与恢复、用户目录可见性、生命周期审计、未测试禁止发布、必填 Secret 的 AES-GCM 密文存储、测试容器的内部网络/tmpfs/安全策略、健康检查成功后的 Secret 清除、测试快照与版本身份不可变、关键记录无法删除或清空、重复发布审计幂等，以及 Worker 对孤立测试容器、健康探针和网络的回收。脚本会撤销临时会话并软下架临时产品，保留版本与测试记录作为验收证据，不删除既有产品、用户、账本或应用卷。

## 产品依赖服务

管理员可以在产品版本的运行参数中声明 `dependencies`。每项依赖包含平台内唯一标识 `key`、目标已发布产品 `productId`、同账户固定服务名 `serviceSlug` 和是否为部署前置条件的 `required`。产品不能依赖自身，依赖图不能形成环；创建版本和发布都会在事务内重新校验，迁移 54 的数据库触发器同时阻止绕过 API 的非法发布。发布后的依赖定义属于不可变版本快照。

必需依赖只有在同一账户内存在目标产品、固定服务名匹配、应用状态为 `running`、活动路由指向最后成功 Release 且实际容器健康时才就绪。用户目录会把缺少项返回为 `missingDependencies` 并将 `deployable` 设为 `false`；创建、更新和回退 API 返回 `409 required_dependency_unavailable`。Worker 在任务从队列进入执行阶段时再次核对数据库状态和真实 Docker 容器，防止依赖在排队期间失效。失败任务会事务化收敛到 `failed`，保留已有成功 Release 与路由。

同账户容器通过 `http://{serviceSlug}:{目标产品端口}` 访问依赖，端口由目标产品版本定义；80 端口可省略。Worker 把所有声明的 `serviceSlug` 加入容器的 `NO_PROXY` 与 `no_proxy`，保证内部请求不经过公网出口代理；依赖重部署后稳定别名自动指向新 Release。Windows 执行：

```powershell
powershell -ExecutionPolicy Bypass -File deploy/verify-product-dependencies.ps1
```

WSL/Linux 执行：

```bash
export COMPOSE_PROJECT_NAME=cloudmeter-wsl
export PLATFORM_PORT=18080
bash deploy/verify-product-dependencies.sh
```

脚本使用固定 Nginx Digest 创建隔离的验收账户、套餐和两个临时产品，覆盖畸形、自身、未知及循环依赖，缺依赖 API 拒绝，依赖就绪后部署，排队期间容器失效由 Worker 拒绝，以及稳定别名与 `NO_PROXY/no_proxy`。清理只暂停本次验收账户、下架临时产品并移除其无卷容器和用户网络，不删除既有数据库、Redis、应用卷或其他应用容器。

## 应用 Secret

用户只能创建或轮换其应用所属产品的任一已发布版本在 `runtimeSpec.secretKeys` 中声明的键。`GET /api/apps/{appID}/secrets` 返回不含明文的版本元数据及排序后的 `allowedKeys`；`PUT /api/apps/{appID}/secrets/{key}` 对未声明键返回 `400 secret_not_declared`。这样可以先为即将更新到的新版本准备 Secret，同时禁止借由自由键名向容器注入 `LD_PRELOAD`、代理变量或其他任意环境变量。

创建或更新 Release 时只快照目标版本声明键的最新 Secret 版本。Worker 在创建容器前按该 Release 的 `runtimeSpec.secretKeys` 独立重建白名单：声明键缺失、版本引用畸形、引用不属于当前应用或键不匹配都会使部署失败；历史快照中额外的未知键会被忽略，不会解密或注入。迁移 64 固化 `app_secrets` 的主键、应用归属、键名和创建时间，并禁止删除或清空父记录；版本本身继续保持追加写。应用生命周期专项脚本会验证初始 Secret 为 v1、两次轮换追加为 v2/v3、未声明键被 API 拒绝，以及父身份与版本记录都不能修改、删除或清空。

迁移 65 把用户 slug、应用 slug 和稳定服务名限制为小写 DNS 标签兼容格式，并在创建后固化用户公开身份及应用归属、产品和公开标识。`last_successful_release_id` 必须属于同一应用；活动路由必须与同一应用当前成功且状态为 `active` 的 Release 一致。Router 不采用 `app_routes` 保存的路径、主机或端口，而是从这些受保护身份和不可变 Release 快照实时推导，避免数据库可写路由目标成为转发信任源。应用生命周期专项脚本会直接尝试篡改 slug、跨应用 Release 指针、公开路径、上游别名、端口和容器名，并要求数据库全部拒绝。

## 重启验收

在 WSL Ubuntu 执行 bash deploy/verify.sh；在 Windows Docker Desktop 执行 powershell -ExecutionPolicy Bypass -File deploy/verify.ps1。脚本只重启服务，不删除数据卷，并验证 Compose 配置、Caddyfile 实际解析、合法 Host 健康接口、未知 Host 返回 `421`、数据库迁移已达到仓库最新版本且不是 dirty 状态、OpenAPI 覆盖全部已注册后端路由、OpenAPI YAML 结构与引用有效、Docker Socket 仅由 Worker 持有和重启后的状态。OpenAPI 结构校验使用固定版本的 Redocly CLI，并通过临时 Node 容器运行，首次执行需要能够访问 npm registry。隔离栈通过 IP 验收时应同时导出 `PLATFORM_ALLOWED_HOST=127.0.0.1`，不要修改正式栈的域名白名单。
Windows 与 WSL 共用 Docker Desktop Engine 时必须使用不同的 `COMPOSE_PROJECT_NAME` 和宿主机端口。Compose 项目名同时作为 `RUNTIME_OWNER` 注入 API 和 Worker；应用容器、产品测试容器、健康探针、辅助容器和托管网络会使用稳定的项目作用域名称并写入 `cloudmeter.owner` 标签。非正式 owner 的用户网络、应用数据卷和默认备份卷同样带作用域，避免克隆数据库后误挂载正式持久数据；正式 `cloudmeter` 保持历史 `user_net_*`、`cmv-*` 和 `cloudmeter_backup_data` 名称。Worker 只对账本项目的资源，避免正式、验收或灾备栈互相删除容器。升级前遗留的无标签资源仅由正式 owner 在当前数据库能证明归属时处理。

## 应用生命周期验收

Windows Docker Desktop 执行：

```powershell
powershell -ExecutionPolicy Bypass -File deploy/verify-app-controls.ps1
```

WSL/Linux 使用：

```bash
export COMPOSE_PROJECT_NAME=cloudmeter-wsl
export PLATFORM_PORT=18080
bash deploy/verify-app-controls.sh
```

脚本会创建隔离的产品、套餐与账户，实际验证测试发布、声明 Secret 的初始加密与追加轮换、未声明键门禁、启动、停止、暂停门禁、数据卷持久化、备份、恢复、辅助容器清理，以及重建 `app-router` 后公开路由重新可达。清理阶段仅停用本次验收数据并移除其运行容器，不会删除既有用户、账本、应用卷或备份卷。网关使用 Caddy 的动态 A 记录解析 API、Web 和 Router 服务，因此无状态服务重建后不会长期保留旧容器 IP；不要把这些反向代理目标改回静态 Docker DNS 解析。

管理员代用户功能可额外执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-impersonation.ps1` 验收。脚本要求库中已有一个启用的超级管理员和普通用户；它只创建 15 分钟内有效的临时测试会话，验证默认只读、写操作邮箱确认、审计记录和退出撤销，不修改用户余额、应用或平台配置。

运行时隔离在 Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-runtime-isolation.ps1`，在 WSL/Linux 执行 `bash deploy/verify-runtime-isolation.sh`。脚本检查实际运行中的 `cm-*` 应用容器没有特权、host 网络、host PID、Docker Socket 或宿主机路径绑定，只连接一个内部用户网络；同时验证 Router/出站代理的稳定别名、同用户发布别名可达、平台数据库/API/Redis 名称不可解析，以及独立用户网络无法解析其他网络的发布别名。

支付与退款在 Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-payments.ps1`，在 WSL/Linux 执行 `bash deploy/verify-payments.sh`。脚本创建并最终停用一个隔离账户，以 1 分人工订单验证创建重放、参数冲突、入账重放和两路并发退款；随后核对唯一退款记录、`processing -> succeeded` 两条事件、原订单终态、精确负向账本、钱包净变化为零、管理员审计和退款记录/事件的更新删除保护。订单、账本、退款与审计历史会保留为验收证据，临时会话会撤销。

应用迁移 59 前可先检查 `SELECT count(*) FROM payment_orders WHERE status='refunding';` 必须为 0，并核对每个历史 `refunded` 订单都有同一账户、`business_type='refund'`、`business_ref=order_id` 且金额等于订单金额负值的账本记录。迁移会拒绝缺少该证据的历史退款，不会伪造账本或事件。继续应用审计、退款历史与关键业务历史不可变迁移后，隔离环境应确认 `schema_migrations` 为 `65|clean`，再执行上述专项脚本。

审计日志专项验收在 Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-audit-logs.ps1`，在 WSL/Linux 执行 `bash deploy/verify-audit-logs.sh`。脚本验证匿名访问返回 `401`、普通用户返回 `403`、超级管理员可按操作与账户筛选并使用主键游标稳定分页；随后在显式回滚事务内尝试更新、删除与清空审计表，三种操作都必须被迁移 60 的数据库触发器拒绝。两条带唯一标记的验收审计会保留为追加写证据，临时会话会撤销。

欠费停机与充值恢复在 Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-billing-recovery.ps1`。在 WSL/Linux 独立 Compose 项目中执行：

```bash
export COMPOSE_PROJECT_NAME=cloudmeter-wsl
export PLATFORM_PORT=18080
bash deploy/verify-billing-recovery.sh
```

脚本自行创建隔离账户、钱包、套餐、产品、应用和成功 Release，不依赖库中的固定用户或应用。它会严格复用参数完全一致的验收价格版本，仅在不存在匹配版本时创建；随后通过公开人工充值 API 注入启动余额，先扣到低余额阈值验证预警通知，再写入唯一欠费用量窗口，验证欠费暂停、路由删除、充值后通过部署状态机自动恢复、三类幂等通知、唯一用量扣费和唯一账本记录。脚本还会写入一个没有价格快照的窗口，确认它永久封存为 `unpriced`、不扣钱包、不产生扣费记录，也不阻止充值后的恢复。清理钩子会撤销临时会话并移除本次验收运行时资源，保留财务和审计记录作为不可变证据。

通知账户隔离在 Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-notification-access.ps1`，在 WSL/Linux 执行 `bash deploy/verify-notification-access.sh`。脚本用两名已有启用用户创建唯一临时通知和 15 分钟会话，验证未认证返回 401、列表只包含当前账户数据、跨账户标记已读返回 404，以及通知所有者可正常标记；退出时会撤销会话并删除临时通知。

历史账单在 Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-billing-statements.ps1`，在 WSL/Linux 执行 `bash deploy/verify-billing-statements.sh`。脚本验证未认证拒绝、账单列表和详情按账户隔离、账单总额等于明细合计，以及 UTF-8 CSV 导出。账单按 UTC 自然月聚合；进行中账期会随成功扣费原子增加，账期结束后接口标记为已结算。每条明细保存应用标识、费用项、数量、价格版本、微分单价、金额和用量窗口，数据库禁止修改或删除明细，也禁止账单总额减少。验收价格版本按完整计价参数复用，脚本重复执行不会持续创建不可变版本。

赠送额度在 Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-credit-grants.ps1`，在 WSL/Linux 执行 `bash deploy/verify-credit-grants.sh`。脚本验证过期额度跳过、发放幂等与参数冲突、仅发额度触发欠费重试、最早到期额度优先，以及额度不足时由钱包承担净额。账单始终保存费用原价，`credit_consumptions` 单独记录抵扣来源；额度批次和消费记录受数据库不可变约束。验收价格版本按完整计价参数复用，脚本重复执行不会持续创建不可变版本。

套餐月度额度在后台“套餐管理”的版本权益中配置，以整数分保存。启用账户按 UTC 自然月获得目标额度，批次在下一个 UTC 月初到期；同月重试不重复发放，升级只补发目标额度的正差额，降级不追回已经发放或消费的额度。API 套餐分配与 Worker 每 30 秒对账使用相同的数据库锁和幂等业务引用，手工赠送接口不得使用保留的 `subscription-credit/` 前缀。Windows 执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-plan-credit-grants.ps1`，WSL/Linux 执行：

```bash
export COMPOSE_PROJECT_NAME=cloudmeter-wsl
export PLATFORM_PORT=18080
bash deploy/verify-plan-credit-grants.sh
```

专项脚本验证管理员权限、保留引用保护、重复分配、六路并发、同月升级补差、降级保留、下月 UTC 到期和 Worker 自动补发。脚本会创建带唯一标记的验收用户与套餐，并保留财务和审计记录作为不可变验收证据。

支付提供商主动操作在管理后台“充值订单”页面执行。`query` 只读取提供商状态并写入不可变 `payment_provider_operations`；`close` 只有在提供商确认后才把待处理订单转为 `closed`。人工支付已支持本地查询/关单；EPay 的主动查询/关单在未取得正式服务商协议前明确返回待配置，不会伪造成功。正式 EPay 协议接入时应将查询和关单端点接入同一 `PaymentProvider` 适配器，并保留验签、金额和商户校验。

EPay 启用后，用户端会优先创建 EPay 订单并打开后端生成的签名支付 URL；未启用时继续使用人工充值。当前适配器使用 HMAC-SHA256 对非空字段按键名排序签名，回调会验证商户号、签名、金额、订单状态和服务商流水号，并对重复回调返回幂等成功。接入真实服务商前必须根据其正式文档核对字段名、签名算法、`sign_type`、成功响应正文和主动查询/关单接口，不应仅凭兼容 EPay 的名称假定协议完全一致。
Windows 可执行 `powershell -ExecutionPolicy Bypass -File deploy/verify-epay.ps1` 验证支付 URL、错误签名、金额篡改、有效回调、重复回调和服务商退款门禁；脚本会恢复原有 EPay 配置。目标服务商退款协议尚未接入时，EPay 退款必须返回 `503`，保持订单为 `paid`、钱包余额不变且不生成 `refunds` 记录，同时写入 `payment.refund.rejected` 审计。

## 订阅到期

管理员分配套餐时可设置到期时间，也可留空表示长期有效。到期后订阅先进入 3 天宽限期，期间权益、应用和路由保持可用；宽限期结束后 Worker 将应用置为暂停、关闭路由并停止容器。重新分配套餐会恢复 active、清除旧宽限期，并使用最后成功 Release 的不可变快照创建恢复部署任务。

## 订阅购买与续期

管理员在“套餐管理”中控制每个套餐是否开放用户自助购买。关闭购买后，新用户不能购买该套餐；已经持有该套餐的用户仍可在控制台手动续期，避免运营开关意外中断已有订阅。`free` 套餐默认开放，其余套餐默认仅管理员分配。购买开关只有超级管理员可以修改，变更会写入审计。

用户购买与续期均为主动操作，平台不保存自动续费授权，也不会自动从钱包扣款。首次购买按套餐当前周期价扣款；有效周期内升级只收目标价格减当前价格的正差额，降级或切换到低价套餐不退款；续期按目标套餐当前完整周期价扣款并延长服务周期。每次操作会在一个事务内锁定钱包与订阅，追加不可变钱包账本、交易记录、订阅账单明细和通知，重复幂等键不会重复扣款。余额不足时整个事务回滚。

Windows 专项验收：

```powershell
powershell -ExecutionPolicy Bypass -File deploy/verify-subscription-purchases.ps1
```

Linux/WSL 专项验收：

```bash
export COMPOSE_PROJECT_NAME=cloudmeter-wsl
export PLATFORM_PORT=18080
bash deploy/verify-subscription-purchases.sh
```

脚本覆盖购买开关权限、关闭套餐不可新购、已有用户可续期、首次购买、升级补差、降级不退款、续期、余额不足回滚，以及交易、钱包账本、账单明细和通知的幂等一致性。

## SMTP

后台可选择 STARTTLS、TLS/SMTPS 或无加密连接。STARTTLS 模式要求服务器明确支持升级，TLS/SMTPS 从建立连接起即使用 TLS 1.2 或更高版本；网络连接超时为 10 秒。生产环境不应使用无加密模式。

“注册时要求邮箱验证码”只能在 SMTP 已启用且主机、端口、发件邮箱和认证凭据均有效时开启；开启后也不能直接停用 SMTP、清空必填字段或破坏认证配置。管理 API 和迁移 56 的数据库触发器同时维护这项双向不变量，避免绕过后台页面留下无法注册的状态。SMTP 密码使用 `SECRETS_ENCRYPTION_KEY` 加密，读取设置时只返回是否已配置，不返回明文；更新时留空密码会保留原密文。

每次验证码为六位数字，有效期 10 分钟；同一邮箱的新验证码会立即作废旧验证码。同一邮箱 60 秒内不能重发、每小时最多发送 5 次，同一来源 IP 每小时最多发送 20 次；触发限制时返回 `429 verification_rate_limited` 和 `Retry-After: 60`。连续输错 5 次后该验证码立即消耗，即使随后输入正确值也不能使用。

## 注册与邮箱策略

首次部署始终由 `/setup` 创建超级管理员，公开注册默认关闭。管理员进入“注册策略”后可以独立控制是否允许自行注册；邮箱验证码选项只有在 SMTP 就绪时可开启。域白名单留空代表允许全部域，填写 `example.com` 时同时允许其子域（例如 `sub.example.com`），比较时忽略大小写并拒绝畸形域名。启用“阻止邮箱别名”后，只要邮箱本地部分包含 `+` 或 `.` 就会拒绝注册和验证码发送。

账户、SMTP、注册策略和公告可在 Windows Docker Desktop 完整验收：

```powershell
powershell -ExecutionPolicy Bypass -File deploy/verify-account-features.ps1
```

WSL/Linux 执行：

```bash
export COMPOSE_PROJECT_NAME=cloudmeter-wsl
export PLATFORM_PORT=18080
bash deploy/verify-account-features.sh
```

WSL 使用 Windows Docker Desktop CLI 而非 WSL 内另一套 Engine 时，显式指定：

```bash
export DOCKER_BIN="/mnt/c/Program Files/Docker/Docker/resources/bin/docker.exe"
bash deploy/verify-account-features.sh
```

脚本启动一次性 MailHog，验证 SMTP 配置保护、立即重发限流、域白名单、`+`/`.` 别名拒绝、五次错误锁定、成功注册与登录、公告可见性和审计。退出时会恢复原有策略与 SMTP 密文，移除临时邮件容器并撤销测试会话；为保留审计外键，验收账户会停用而不会硬删除。

## OAuth

GitHub 和 LinuxDo OAuth 都使用 `PUBLIC_BASE_URL` 生成固定回调地址与登录结果页，绝不依据请求 `Host` 头拼接 URL。后台会显示并允许复制准确回调地址：`{PUBLIC_BASE_URL}/api/auth/oauth/github/callback` 和 `{PUBLIC_BASE_URL}/api/auth/oauth/linuxdo/callback`。必须先把对应地址登记到提供商，再填写 Client ID、Client Secret 和 Scopes；缺少有效 `PUBLIC_BASE_URL` 时后端拒绝启用。Client Secret 使用 `SECRETS_ENCRYPTION_KEY` 加密保存，读取接口不返回原文。

GitHub 登录只接受提供商确认过的邮箱；LinuxDo 登录拒绝未激活、已禁言或信任等级低于后台阈值的账户，阈值范围为 0 至 4。OAuth 绑定已有账户前会校验邮箱，创建新账户时仍受“允许用户自行注册”、域白名单和邮箱别名策略约束，并写入 `user.oauth_create` 审计记录。

## 备份恢复

后台“备份与恢复”页面会列出当前版本声明的卷。恢复期间应用进入 `updating`，Worker 停止容器、覆盖目标卷、重新启动并等待健康检查；成功后恢复为 `running`。备份 helper 使用固定 Digest 镜像和无网络模式，用户容器不会获得 Docker Socket。
## 公网出站计量边界

用户应用网络保持 `internal`，不得把 Docker 容器总 `NetIO` 当作公网流量。平台仅接受专用出口网关或代理按应用归因的字节增量，通过内部 `/api/internal/egress/{appID}` 上报。每个样本必须有唯一 `sampleId`，重复上报幂等；只接受当前尚未关闭的五分钟窗口，Worker 在窗口关闭后封存为 `network.egress_gib`。代理上报失败只重试有限次数，无法确认的区间不自动扣费；没有专用出口采集数据时不生成公网流量费用，同用户内部互访免费。

## 标准费用目录与无价格窗口

迁移 58 为新旧安装统一预置平台会产生的标准费用项，但不自动创建价格版本：`app.runtime.minutes`、`cpu.core_hours`、`memory.gib_hours`、`storage.system.gib_days`、`storage.data.gib_days`、`network.egress_gib`、`app.deployment`、`product.authorization`、`network.public_ingress`、`backup.operation` 和 `backup.storage.gib_days`。管理员应按业务需要分别发布不可变价格版本。系统盘与数据盘分开计量；旧 `storage.gib_days` 只作为历史记录保留，新窗口不再写入该代码。

用量事件创建时即解析并冻结 `price_version_id`，后续扣费只能使用该快照。若当时没有适用价格，聚合窗口会直接封存为 `unpriced`：不会扣除赠送额度或钱包余额，不会生成扣费与账单明细，后续补建或回填价格也不会追溯收费。迁移 58 会以相同规则封存升级前遗留的无价格待处理窗口，并用数据库约束禁止再次产生 `pending` 且没有价格快照的聚合。`waived_legacy` 仅用于历史兼容或迁移回滚，不能重新进入待扣费队列。
