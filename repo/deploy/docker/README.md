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

## 七服务监控端点

七个 Go 服务启动后会在各自的 `HEALTH_PORT` 暴露 `/metrics`、`/healthz` 和 `/readyz`。docker-compose 当前只负责本地依赖，不包含 Kubernetes Pod discovery，也不会自动创建 Prometheus `ani-components` job。

本地排障可以直接访问单个服务的 `/metrics`；需要验证固定七服务聚合、标签身份、新鲜度和 fail-closed 语义时，应使用 Kubernetes 部署契约。对接方式、端口表和部署清单见[七服务 Prometheus 状态对接与部署指南](../../docs/operations/service-runtime-observability.md)。

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
