# CloudMeter Docker 部署指南 (Docker Deployment Guide)

本指南介绍如何在 Linux、macOS 或 Windows（WSL2 / Docker Desktop）系统上，使用 Docker Compose 从零构建并运行生产级 CloudMeter 平台。

CloudMeter 采用**单公网端口拓扑设计**：除了入口 Gateway 之外，数据库（PostgreSQL）、缓存（Redis）、后端 API 服务、路由分发（App Router）、后台 Worker 及所有用户应用容器均在 Docker 内部网络运行，不对外直接暴露任何端口。

---

## 目录
1. [环境准备](#1-环境准备)
2. [代码获取与密钥配置](#2-代码获取与密钥配置)
3. [Worker 权限与 Docker Socket 配置](#3-worker-权限与-docker-socket-配置)
4. [启动服务栈](#4-启动服务栈)
5. [超级管理员初始化](#5-超级管理员初始化)
6. [外层反向代理与 HTTPS](#6-外层反向代理与-https)
7. [平台升级与数据平滑迁移](#7-平台升级与数据平滑迁移)
8. [日常运维与故障排查](#8-日常运维与故障排查)

---

## 1. 环境准备

### 基础软件依赖
- **Docker Engine**: v24.0+ 或 **Docker Desktop**
- **Docker Compose**: v2.20+ (`docker compose version`)
- **Git**
- **OpenSSL**: 用于生成安全的随机密码和主加密密钥

### 运行环境检查

在终端运行以下命令确认 Docker 环境正常：

```bash
docker version
docker compose version
```

> **注意（Linux 用户）**：请确保将当前部署用户加入 `docker` 用户组（`sudo usermod -aG docker $USER`），避免运行 Compose 时出现权限不足问题。

---

## 2. 代码获取与密钥配置

### 2.1 克隆代码仓库

```bash
git clone https://github.com/NKBaa/cloudmeter.git
cd cloudmeter
```

### 2.2 准备配置文件 `.env`

基于配置模板创建 `.env` 文件：

```bash
cp configs/.env.example .env
```

`.env` 文件包含所有的环境变量和密码配置，已自动列入 `.gitignore`，**请勿将生产 `.env` 提交至 Git 仓库**。

### 2.3 生成加密密钥

运行以下命令生成安全随机密钥：

```bash
# 生成各类服务内部通信密码与令牌
openssl rand -hex 32

# 生成 SECRETS_ENCRYPTION_KEY (32 字节 Base64，无填充)
openssl rand -base64 32 | tr -d '=\n'
```

编辑 `.env` 文件，替换以下核心字段：

| 环境变量 | 说明 | 示例 / 提示 |
| :--- | :--- | :--- |
| `POSTGRES_PASSWORD` | PostgreSQL 数据库独立高强度密码 | 独立随机字符串 |
| `REDIS_PASSWORD` | Redis 缓存服务密码 | 独立随机字符串 |
| `ROUTER_INTERNAL_TOKEN` | Gateway 与 Router 通信令牌（最少 32 位） | 独立随机字符串 |
| `EGRESS_INGEST_TOKEN` | Worker 与出口代理通信令牌 | 独立随机字符串 |
| `SECRETS_ENCRYPTION_KEY` | 静态加密主密钥（主加密密码，务必离线备份） | 32-Byte Base64 无填充 |
| `PLATFORM_ALLOWED_HOST` | 平台访问 Host 名（仅主机名，无协议端口路径） | `cloud.example.com` 或 `127.0.0.1` |
| `PUBLIC_BASE_URL` | 用户访问平台的完整 URL 根路径 | `https://cloud.example.com` |

> ⚠️ **警告**：`SECRETS_ENCRYPTION_KEY` 用于加密保存平台的第三方凭据（如 SMTP 密码、OAuth Client Secret、支付通道密钥和用户应用敏感环境变量）。部署后请务必保存备份！一旦丢失，数据库内存存的加密字段将不可解密。

---

## 3. Worker 权限与 Docker Socket 配置

CloudMeter 的 Worker 是平台内**唯一**需要访问宿主机 Docker Engine 的服务，用于按需创建和管理用户的隔离应用容器。

### 3.1 启用容器执行引擎

在 `.env` 中设置：

```dotenv
DOCKER_EXECUTOR_ENABLED=true
DOCKER_SOCKET=/var/run/docker.sock
DOCKER_SOCKET_PATH=/var/run/docker.sock
```

### 3.2 确认 Docker Socket 组 ID (Linux)

在 Linux 宿主机上获取 Docker Socket 的 Group ID：

```bash
stat -c '%g' /var/run/docker.sock
```

将返回的数字填入 `.env` 中的 `DOCKER_GID`：

```dotenv
DOCKER_GID=998
```

> **安全提示**：请勿将 Docker Socket 挂载到 API、Web 控制台或任何用户容器中。拥有 Docker Socket 权限等同于宿主机的 root 权限。

---

## 4. 启动服务栈

### 4.1 配置语法校验与容器构建

在启动前，可先验证配置文件格式是否正确：

```bash
docker compose --env-file .env -f deploy/compose.yaml config --quiet
```

一键构建并启动所有服务栈：

```bash
docker compose --env-file .env -f deploy/compose.yaml up -d --build
```

### 4.2 检查服务运行状态

查看所有容器是否正常运行：

```bash
docker compose --env-file .env -f deploy/compose.yaml ps
```

健康的服务栈状态应该如下：
- `postgres`, `redis`, `api`, `app-router`, `egress-proxy` 状态显示为 `healthy`；
- `migrate` 自动执行完数据迁移后显示为 `Exited (0)`；
- `gateway` 为唯一映射宿主机端口的服务；
- `worker` 与 `web` 处于持续运行状态 (`Up`)。

查看启动实时日志：

```bash
docker compose --env-file .env -f deploy/compose.yaml logs -f --tail=100
```

---

## 5. 超级管理员初始化

容器启动成功后，通过浏览器访问平台初始化页面：

```text
http://<PLATFORM_ALLOWED_HOST>:<PLATFORM_PORT>/setup
```

### 初始化规则与并发锁
1. 首次打开 `/setup` 页面，输入管理员姓名、邮箱以及至少 12 位的复杂密码。
2. 提交后，后端使用 PostgreSQL `SERIALIZABLE` 隔离级别与 Advisory Lock 全局串行化锁，在同一个原子事务中创建首个**超级管理员账号**、初始化钱包账本与系统审计记录。
3. **初始化完成后，setup 写接口将永久关闭**，后续任何重复调用均会被服务端拒绝。

---

## 6. 外层反向代理与 HTTPS

在生产环境中，平台通常会部署在域名（如 `https://cloud.example.com`）之后，由外层的 Nginx / OpenResty / Caddy 或云厂商负载均衡器终止 TLS 并转发至平台的 `gateway` 端口。

### 配置要求
- 平台 `.env` 中 `PUBLIC_BASE_URL` 设置为 `https://cloud.example.com`。
- `PLATFORM_ALLOWED_HOST` 设置为 `cloud.example.com`。
- `GATEWAY_TRUSTED_PROXY_CIDRS` 设置为外层反向代理服务器所在的源 IP 网段（例如 `172.16.0.0/12` 或具体 IP）。

相关反向代理配置范例请参考仓库中的 [`deploy/openresty.conf.example`](../deploy/openresty.conf.example)。

---

## 7. 平台升级与数据平滑迁移

当平台发布新版本需要升级时，请按照以下标准步骤操作：

### 7.1 数据与配置备份
在升级前，强烈建议备份 PostgreSQL 数据库卷、应用数据卷以及当前 `.env` 配置文件。

### 7.2 执行平滑升级

```bash
# 1. 拉取最新代码
git pull origin main

# 2. 预先运行数据库迁移任务
docker compose --env-file .env -f deploy/compose.yaml run --rm migrate

# 3. 重新构建并平滑重启业务服务
docker compose --env-file .env -f deploy/compose.yaml up -d --build api worker app-router egress-proxy web gateway
```

> ⚠️ **警告**：生产升级**切勿使用 `docker compose down -v`** 命令！带 `-v` 参数会直接清空 PostgreSQL 数据库卷和 Redis 缓存卷！

---

## 8. 日常运维与故障排查

### 8.1 部署状态验收

平台内置了全套自动化状态检查脚本，升级或运维后可直接执行验收：

```bash
# Linux / WSL
bash deploy/verify.sh

# Windows Docker Desktop
powershell -ExecutionPolicy Bypass -File deploy/verify.ps1
```

### 8.2 常见问题速查

1. **端口冲突 (`port is already allocated`)**
   修改 `.env` 中的 `PLATFORM_PORT`（例如改为 `8088`），或停止占用该端口的宿主机其他进程。

2. **421 请求错误 (`421 Misdirected Request`)**
   请求中的 HTTP `Host` 报头与 `.env` 中定义的 `PLATFORM_ALLOWED_HOST` 不一致。检查反向代理是否正确透传了原 Host 报头。

3. **Worker 无法部署应用**
   检查宿主机 Docker Socket 路径与权限，确认 `.env` 中 `DOCKER_EXECUTOR_ENABLED=true` 且 `DOCKER_GID` 与宿主机 `stat -c '%g' /var/run/docker.sock` 一致。

4. **数据库 Migration 报错**
   运行 `docker compose --env-file .env -f deploy/compose.yaml logs migrate` 查看具体的 SQL 错误，绝不要手动修改或乱动 PostgreSQL 中的 `schema_migrations` 表。

---

祝使用愉快！如遇到问题欢迎在 GitHub 仓库提交 [Issue](https://github.com/NKBaa/cloudmeter/issues)。
