# Docker 部署指南

本文说明如何使用 Docker Compose 从空环境部署、初始化、验证和升级 CloudMeter。生产环境只需要对外开放 `gateway` 的一个端口；PostgreSQL、Redis、API、Worker、Web、应用 Router 和出口代理都保留在 Docker 内部网络。

## 1. 准备环境

安装以下软件：

- Docker Engine 或 Docker Desktop；
- Docker Compose v2（能够执行 `docker compose version`）；
- Git；
- OpenSSL，用于生成随机密码和主密钥。

确认 Docker 可用：

```bash
docker version
docker compose version
```

Linux 上需要让部署用户能够访问 Docker Engine。使用 Docker Desktop 的 WSL 发行版时，应先在 Docker Desktop 中启用对应发行版的 WSL Integration。

## 2. 获取代码并创建配置

```bash
git clone https://github.com/NKBaa/cloudmeter.git
cd cloudmeter
cp configs/.env.example .env
```

`.env` 已被 Git 忽略，不应提交到仓库。至少替换以下值：

```bash
openssl rand -hex 32       # 可分别用于 PostgreSQL、Redis、Router 和出口计量令牌
openssl rand -base64 32 | tr -d '=\n'  # 用于 SECRETS_ENCRYPTION_KEY
```

在 `.env` 中配置：

```dotenv
POSTGRES_PASSWORD=<独立的长随机密码>
REDIS_PASSWORD=<另一个长随机密码>
ROUTER_INTERNAL_TOKEN=<至少 32 字符的随机令牌>
EGRESS_INGEST_TOKEN=<至少 32 字符的随机令牌>
SECRETS_ENCRYPTION_KEY=<上面生成的无填充 Base64 主密钥>
```

`SECRETS_ENCRYPTION_KEY` 用于加密 SMTP、OAuth、支付和应用 Secret。部署后必须与数据库备份一起离线保存；丢失或直接更换会导致已有密文无法解密。

### 本机直接访问

仅在本机验收时，可以使用：

```dotenv
PLATFORM_BIND_IP=127.0.0.1
PLATFORM_PORT=8080
PLATFORM_ALLOWED_HOST=127.0.0.1
PUBLIC_BASE_URL=http://127.0.0.1:8080
```

浏览器访问 `http://127.0.0.1:8080/setup`。

### 域名和反向代理

生产环境示例：

```dotenv
PLATFORM_BIND_IP=127.0.0.1
PLATFORM_PORT=8080
PLATFORM_ALLOWED_HOST=cloud.example.com
PUBLIC_BASE_URL=https://cloud.example.com
GATEWAY_TRUSTED_PROXY_CIDRS=172.16.0.0/12
```

`PLATFORM_ALLOWED_HOST` 只能填写主机名，不能包含协议、端口或路径。`PUBLIC_BASE_URL` 填写用户实际访问的源地址，不能带路径、查询或片段。TLS 由宿主机上的 Nginx/OpenResty、Caddy 或云负载均衡器终止；[OpenResty 示例](../deploy/openresty.conf.example)可作为外层反向代理起点。

`GATEWAY_TRUSTED_PROXY_CIDRS` 只应包含可信反向代理到容器入口时使用的源网段。部署后根据实际 Docker bridge 地址尽量收窄，不要设置为任意公网来源。

## 3. 配置应用容器执行权限

只使用账户、计费和管理功能时可以保留：

```dotenv
DOCKER_EXECUTOR_ENABLED=false
```

需要实际部署用户应用时设置：

```bash
stat -c '%g' /var/run/docker.sock
```

将输出的组 ID 写入 `.env`：

```dotenv
DOCKER_EXECUTOR_ENABLED=true
DOCKER_SOCKET=/var/run/docker.sock
DOCKER_SOCKET_PATH=/var/run/docker.sock
DOCKER_GID=<Docker Socket 的组 ID>
```

Docker Socket 只挂载到 Worker；不要把它挂载到 API、Web、Router 或用户应用容器。访问 Docker Socket 等同于获得宿主机上的高权限，应限制服务器登录权限并保护 `.env`。

## 4. 首次启动

检查最终 Compose 配置，然后构建并启动：

```bash
docker compose --env-file .env -f deploy/compose.yaml config --quiet
docker compose --env-file .env -f deploy/compose.yaml up -d --build
docker compose --env-file .env -f deploy/compose.yaml ps
```

首次构建会下载基础镜像、编译后端和前端、创建 PostgreSQL/Redis 数据卷，并由 `migrate` 服务自动执行全部数据库迁移。正常状态下：

- `postgres`、`redis`、`api`、`app-router` 和 `egress-proxy` 显示 `healthy`；
- `migrate` 显示 `Exited (0)`；
- `gateway` 是唯一映射宿主机端口的业务服务；
- `worker`、`web` 和 `gateway` 保持运行。

如需查看启动过程：

```bash
docker compose --env-file .env -f deploy/compose.yaml logs -f --tail=200
```

健康检查：

```bash
curl -H "Host: ${PLATFORM_ALLOWED_HOST}" \
  "http://127.0.0.1:${PLATFORM_PORT}/api/healthz"
```

如果 `.env` 没有导出到当前 Shell，可把命令中的主机名和端口替换为实际值。

## 5. 首次初始化

打开：

```text
http://<服务器地址>:<PLATFORM_PORT>/setup
```

使用 HTTPS 反向代理时打开 `https://<域名>/setup`。填写管理员姓名、邮箱和至少 12 位密码。初始化采用数据库事务锁，只允许一个请求创建唯一的首个超级管理员；完成后初始化写入口会永久关闭。

## 6. 部署后验收

Linux/WSL：

```bash
bash deploy/verify.sh
```

Windows Docker Desktop：

```powershell
powershell -ExecutionPolicy Bypass -File deploy/verify.ps1
```

基础脚本验证 Compose、迁移版本、OpenAPI、Host 白名单、Docker Socket 隔离，以及服务重启后的健康状态。更多账户、应用、计费和支付专项脚本见[运维与验收文档](operations.md)。专项脚本会写入隔离的验收记录，建议在测试环境执行。

## 7. 升级

升级前备份 PostgreSQL、`.env`、`SECRETS_ENCRYPTION_KEY`、应用数据卷和备份卷。获取新代码后执行：

```bash
docker compose --env-file .env -f deploy/compose.yaml run --rm migrate
docker compose --env-file .env -f deploy/compose.yaml up -d --build \
  api worker app-router egress-proxy web gateway
docker compose --env-file .env -f deploy/compose.yaml ps
```

升级完成后重新运行 `deploy/verify.sh` 或 `deploy/verify.ps1`。升级和普通重启不要使用 `docker compose down -v`，因为 `-v` 会删除 PostgreSQL 和 Redis 数据卷。

## 8. 停止、启动与故障排查

保留数据停止：

```bash
docker compose --env-file .env -f deploy/compose.yaml stop
```

再次启动：

```bash
docker compose --env-file .env -f deploy/compose.yaml start
```

查看单个服务日志：

```bash
docker compose --env-file .env -f deploy/compose.yaml logs --tail=200 api
docker compose --env-file .env -f deploy/compose.yaml logs --tail=200 worker
docker compose --env-file .env -f deploy/compose.yaml logs --tail=200 gateway
```

常见问题：

- `port is already allocated`：修改 `.env` 中的 `PLATFORM_PORT`，或停止占用该端口的服务；
- `421 Misdirected Request`：请求的 `Host` 与 `PLATFORM_ALLOWED_HOST` 不一致；
- Worker 无法创建容器：确认 `DOCKER_EXECUTOR_ENABLED=true`、Socket 路径和 `DOCKER_GID`；
- `migrate` 非零退出：先查看迁移日志，不要强制修改 `schema_migrations` 或删除数据卷；
- OAuth 回调或支付返回地址错误：检查 `PUBLIC_BASE_URL` 是否与用户访问地址完全一致。

## 9. WSL 隔离验收

Windows 与 WSL 共用 Docker Engine 时，为避免与已有环境共享容器名、网络、卷和端口，应使用独立项目名和端口：

```bash
export COMPOSE_PROJECT_NAME=cloudmeter-wsl-acceptance
export PLATFORM_PORT=18080
export PLATFORM_ALLOWED_HOST=127.0.0.1
export PUBLIC_BASE_URL=http://127.0.0.1:18080
export DOCKER_GID="$(stat -c '%g' /var/run/docker.sock)"

docker compose --env-file .env -f deploy/compose.yaml up -d --build
bash deploy/verify.sh
```

正式栈和验收栈不得使用同一个 `COMPOSE_PROJECT_NAME`。需要删除验收数据时，先用 `docker compose ... ps` 确认当前项目名和资源范围，再仅对验收项目执行清理。
