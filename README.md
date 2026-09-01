# CloudMeter

**English** | [中文](#cloudmeter-中文)

CloudMeter is a self-hosted AI application deployment and usage-based billing platform designed for cloud environments. It provides a complete software stack behind a single public entry port to manage containerized application deployments, resource metering (CPU, memory, disk), subscriptions, wallets, payments, and role-based access control with immutable audit trails.

---

## Key Features

- **Single Public Port Topology**: Only the Caddy gateway binds to a host port; database, cache, APIs, routers, workers, and user workloads stay isolated within internal Docker networks.
- **Secure One-Time Bootstrap**: First-time initialization creates the unique super-admin inside a serializable PostgreSQL transaction with advisory locking. Once initialized, the setup endpoint closes permanently.
- **Strict Workload Isolation**: User applications run in dedicated internal networks using pinned image digests, stable service aliases, and hardened runtime constraints (no privileged mode, host network, docker socket, or host path mounts).
- **Mandatory Release Verification**: Product versions require isolated deployment testing before publication; published releases enforce immutable image digests and strict secret allowlisting.
- **Granular Usage Metering & Billing**: CPU, memory, system disk, and persistent volume usage are measured against immutable price snapshots. Unpriced usage periods are permanently sealed as non-retroactive.
- **Transactional Financial Engine**: Wallets, plan subscriptions, credit grants, refunds, and statements share atomic transaction boundaries. Ledger histories and audit logs are append-only.
- **Auth & Integrations**: SHA-256 session digests, bcrypt passwords, OAuth (GitHub, LinuxDo), SMTP email verification, and pluggable payment providers (EPay).

---

## Quick Start

### Prerequisites
- Linux / macOS / Windows with Docker Desktop (or WSL2)
- Docker Engine 24+ and Docker Compose v2 (`docker compose version`)
- OpenSSL (for generating secrets)

### Installation

```bash
# 1. Clone the repository
git clone https://github.com/NKBaa/cloudmeter.git
cd cloudmeter

# 2. Prepare environment file
cp configs/.env.example .env

# 3. Generate cryptographic secrets and update .env
# Replace passwords, tokens, and SECRETS_ENCRYPTION_KEY
```

Generate secure keys:
```bash
# For DB / Redis / Router / Egress tokens
openssl rand -hex 32

# For SECRETS_ENCRYPTION_KEY (32-byte Base64, no padding)
openssl rand -base64 32 | tr -d '=\n'
```

### Start Services

```bash
docker compose --env-file .env -f deploy/compose.yaml up -d --build
```

Access the setup wizard at `http://<server-ip>:<PLATFORM_PORT>/setup` to create the super-administrator account, then configure the public URL in Website Settings.

For complete production guides, see [Docker Deployment Guide](docs/docker-deployment.md).

---

## Documentation

- [Docker Deployment Guide](docs/docker-deployment.md) - Fresh server installation, configuration, security hardening, and upgrades.
- [Operations & Verification Guide](docs/operations.md) - Routine operations, database backups, WSL keepalive, and automated test suites.
- [State Machines Specification](docs/state-machines.md) - Lifecycle states for deployments, releases, tasks, and payment orders.
- [System Open API Specification](docs/system-api.md) - Authentication, tool/automation endpoints, write boundaries, and integration examples.
- [OpenAPI 3.0 Specification](docs/openapi.yaml) - Complete HTTP REST API contract.

---

## System Open API

CloudMeter exposes a versioned API for trusted AI agents, scripts, operations tools, and third-party integrations. A super-administrator creates or rotates the access key in **System Settings → System Open API & Access Key**. The plaintext key is shown only once; the server stores only its SHA-256 digest.

```bash
export CLOUDMETER_URL="https://console.example.com"
export CLOUDMETER_API_KEY="cm_api_..."

curl -sS "$CLOUDMETER_URL/api/automation/v1/analysis" \
  -H "Authorization: Bearer $CLOUDMETER_API_KEY"
```

The API can read system analysis, settings, sanitized user information, and audit events. Whitelisted write operations can update display settings and user status, clear runtime logs, and run database `ANALYZE`. It never exposes arbitrary SQL, passwords, sessions, payment credentials, application secrets, or TLS/DNS credentials. Every write operation is audited.

See the [System Open API Specification](docs/system-api.md) for endpoint details and safety requirements. The legacy `/api/llm/v1` path and `cm_llm_` keys remain compatible; new integrations should use `/api/automation/v1` and `cm_api_` keys.

---

## Architecture Overview

```
[ Internet / Public Proxy ]
             │
      :8080  ▼
   ┌────────────────────────────────────────────────────────┐
   │                  Caddy Gateway                         │
   └───────┬─────────────────┬───────────────────┬──────────┘
           │ /api/*          │ /apps/*           │ /*
           ▼                 ▼                   ▼
    ┌─────────────┐   ┌─────────────┐     ┌─────────────┐
    │  Go API     │   │ App Router  │     │ Web Console │
    │  (:8081)    │   │  (:8082)    │     │  (:8080)    │
    └──────┬──────┘   └──────┬──────┘     └─────────────┘
           │                 │
    ┌──────┴─────────────────┴──────┐
    │   PostgreSQL 17 & Redis 7.4   │
    └──────────────┬────────────────┘
                   │
            ┌──────┴──────┐
            │   Worker    │ ───► [ Docker Engine / User App Containers ]
            └─────────────┘
```

---

## Automated Verification

Run end-to-end integration verifications after deployment:

```bash
# Linux / WSL
bash deploy/verify.sh

# Windows PowerShell
powershell -ExecutionPolicy Bypass -File deploy/verify.ps1
```

Additional test scripts for account features, billing recovery, application lifecycle, credit grants, and payment operations can be found in `deploy/`.

---

## Contributing

Contributions, issues, and feature requests are welcome! Feel free to check the [issues page](https://github.com/NKBaa/cloudmeter/issues).

---

## License

This project is licensed under the [MIT License](LICENSE).

---

# CloudMeter 中文

CloudMeter 是一款面向云端环境的开源 AI 应用部署与按量计费平台。系统通过单一公网入口提供容器化应用生命周期管理、计算资源（CPU/内存/磁盘）计量、套餐与钱包体系、支付接入及 RBAC 审计追踪等完整能力。

---

## 核心特性

- **单端口公网暴露**：仅 Caddy Gateway 对外映射宿主机端口；数据库、缓存、后端 API、路由服务、后台 Worker 与应用实例均运行于 Docker 内部网络。
- **并发安全初始化**：首次启动通过 PostgreSQL 串行事务与全局锁创建唯一的超级管理员，初始化完成后自动永久关闭该入口。
- **严格的工作负载隔离**：每个用户的应用运行在独立的内部网络中，强制使用指定 Digest 镜像与稳定服务别名，并从机制上禁止特权模式、宿主机网络及 Docker Socket 挂载。
- **不可变版本与测试准入**：产品模板发布新版本前，必须先通过隔离环境下的全链路自动化测试，上架后版本快照与镜像摘要不可篡改。
- **精准计量与按量计费**：按 UTC 周期计量 CPU、内存、系统盘与数据盘使用量，并基于冻结的价格版本扣费；未配置价格的区间永久封存为 `unpriced`，绝不追溯扣费。
- **金融级账本一致性**：钱包余额、套餐订阅、赠送金、退款及月度对账单处于严格的事务边界中，退款记录与审计日志在数据库层面只允许追加（Append-Only）。
- **完善的认证与集成**：支持密码 bcrypt 加密、会话 SHA-256 摘要存储、GitHub / LinuxDo OAuth 快捷登录、SMTP 邮箱验证码及易支付（EPay）集成。

---

## 快速上手

### 环境准备
- 操作系统：Linux / macOS / Windows (WSL2 / Docker Desktop)
- 工具依赖：Docker Engine 24+、Docker Compose v2 (`docker compose version`)、OpenSSL

### 部署步骤

```bash
# 1. 克隆代码仓库
git clone https://github.com/NKBaa/cloudmeter.git
cd cloudmeter

# 2. 复制环境配置模板
cp configs/.env.example .env

# 3. 生成必要安全密钥并修改 .env 配置文件
```

生成随机密码与主加密密钥：
```bash
# 生成各类服务密码与通信令牌
openssl rand -hex 32

# 生成 SECRETS_ENCRYPTION_KEY (32 字节无填充 Base64)
openssl rand -base64 32 | tr -d '=\n'
```

### 启动服务栈

```bash
docker compose --env-file .env -f deploy/compose.yaml up -d --build
```

通过浏览器访问 `http://<服务器 IP>:<PLATFORM_PORT>/setup` 完成超级管理员账号初始化，然后在“网站设置”中配置服务器公开 URL。

详细配置、域名反向代理设置及升级说明请参考 [Docker 部署指南](docs/docker-deployment.md)。

---

## 文档索引

- [Docker 部署指南](docs/docker-deployment.md) - 从零开始的生产级安装、环境调优、权限设置与版本平滑升级。
- [运维与验收指南](docs/operations.md) - 日常运维检查、WSL 常驻保活方案与自动化回归测试脚本说明。
- [状态机规范](docs/state-machines.md) - 部署、构建任务、备份还原及支付订单的完整状态机流转图。
- [系统开放 API 规范](docs/system-api.md) - 访问密钥、工具与自动化接口、写入边界及调用示例。
- [OpenAPI 规范](docs/openapi.yaml) - 平台标准 RESTful API 接口定义。

---

## 系统开放 API

CloudMeter 提供面向受信任的大模型、脚本、运维工具和第三方集成的版本化开放 API。超级管理员在 **系统设置 → 系统开放 API 与访问密钥** 中生成或轮换密钥；密钥明文只显示一次，服务端仅保存 SHA-256 摘要。

```bash
export CLOUDMETER_URL="https://console.example.com"
export CLOUDMETER_API_KEY="cm_api_..."

curl -sS "$CLOUDMETER_URL/api/automation/v1/analysis" \
  -H "Authorization: Bearer $CLOUDMETER_API_KEY"
```

接口支持读取系统分析、系统设置、脱敏用户信息和审计事件；白名单写操作支持修改展示设置及用户状态、清理运行日志和执行数据库 `ANALYZE`。接口不提供任意 SQL，也不会开放密码、会话、支付凭据、应用 Secret 或 TLS/DNS 凭据。所有写入均进入审计日志。

完整端点与安全规范参见 [系统开放 API 规范](docs/system-api.md)。旧版 `/api/llm/v1` 路径和 `cm_llm_` 密钥继续兼容，新接入应使用 `/api/automation/v1` 和 `cm_api_` 密钥。

---

## 部署验收

系统自带全套自动化验收套件，执行以下命令验证部署完整性：

```bash
# Linux / WSL 环境
bash deploy/verify.sh

# Windows PowerShell 环境
powershell -ExecutionPolicy Bypass -File deploy/verify.ps1
```

---

## 开源协议

本项目采用 [MIT 许可证](LICENSE)。
