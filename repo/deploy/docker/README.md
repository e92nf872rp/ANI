# 本地开发环境

## 前提条件

- Docker Desktop 4.x+ 或 Docker Engine 24+（含 Compose V2）
- 可用内存 ≥ 8GB（Milvus 吃内存）
- 磁盘空间 ≥ 20GB

## 快速启动

```bash
# 从仓库根目录执行
make deps          # 启动所有依赖服务

# 验证服务就绪
make deps-status
```

首次 `make deps` 会在空 PostgreSQL 数据卷上自动执行 `deploy/postgres/ani-dev-database-init.sql`（经 `init-scripts/postgres/001-ani-dev-database-init.sql` 挂载）。该脚本包含 auth 表、Core runtime metadata、Gateway 元数据 P1/P3 表及内置 roles/tenants seed。

若本机已有旧的 Postgres 数据卷，init 不会重跑；需要完整 schema 时请执行 `make deps-clean` 后重新 `make deps`，或对已有卷执行 `make db-upgrade-gateway-metadata`。

## 服务访问

| 服务 | 地址 | 账号/密码 |
|---|---|---|
| PostgreSQL | localhost:5432 | ani / ani_dev_password |
| MinIO Console | http://localhost:9001 | ani-admin / ani_dev_password |
| NATS Monitor | http://localhost:8222 | — |
| Redis | localhost:6379 | 密码: ani_dev_password |
| Milvus | localhost:19530 | — |
| Milvus Attu | http://localhost:3000（需 `--profile tools`）| — |

## 启动可选工具

```bash
# 启动 Milvus Attu（Web UI 管理 Milvus）
docker compose -f deploy/docker/docker-compose.yml --profile tools up -d attu

# 启动 Dex（OIDC 认证，完整认证流程测试）
docker compose -f deploy/docker/docker-compose.yml --profile auth up -d dex
```

Dex 开发配置位于 `deploy/docker/config/dex-dev.yaml`：

| 项 | 值 |
|---|---|
| Issuer | `http://127.0.0.1:5556/dex` |
| Client ID | `ani-console` |
| Client Secret | `ani-console-secret` |
| 测试账号 | `admin@ani.local` |
| 测试密码 | `ani-dev-password` |

auth-service 只需要配置 `AUTH_OIDC_ISSUER_URL`、`AUTH_OIDC_CLIENT_ID`、`AUTH_OIDC_CLIENT_SECRET`。
Dex-compatible 端点会自动推导为 `{issuer}/auth`、`{issuer}/token`、`{issuer}/keys`；接入非 Dex-compatible IdP 时再显式覆盖 `AUTH_OIDC_AUTH_URL` / `AUTH_OIDC_TOKEN_URL` / `AUTH_OIDC_JWKS_URL`。

## Kubernetes REST provider（本地连集群）

默认 `make deps` + Gateway 使用 **local profile**，不连接 Kubernetes。

启用真实 K8s provider（如 `SECRET_PROVIDER_MODE=kubernetes_rest`）时，凭证按以下优先级自动解析：

1. 显式 `KUBERNETES_API_HOST` / `KUBERNETES_BEARER_TOKEN` / `KUBERNETES_SERVICE_*`（生产部署）
2. `KUBECONFIG` 或 `~/.kube/config`（本地开发）
3. Pod 内 ServiceAccount（in-cluster）

关闭自动解析：`KUBERNETES_CONFIG_AUTO_LOAD=false`。详见 `.env.example`。

## Dex smoke 验收

具备 Docker 的环境执行：

```bash
make validate-auth-dex-smoke
```

该命令会启动 `auth` profile 的 Dex，并用 `scripts/smoke_auth_dex.py` 验证 discovery、JWKS、用户名密码登录、authorization code callback 和 token endpoint。无 Docker 的本地环境不执行此项；CI 或外部验收环境需要用该命令完成 M2.2 的真实 Dex 登录签收。

## 环境变量

复制 `.env.example` 为 `.env`，按注释修改后各服务自动加载：

```bash
cp .env.example .env
```

## 关闭和清理

```bash
make deps-down        # 停止服务，保留数据卷
make deps-clean       # 停止服务并删除所有数据（危险！）
```
