# SPRINT13-AUTH-DEX-PRODUCTION-GATE - Auth/Dex production-shaped gate

> 记录类型：Sprint 13 cross-cutting Auth/Dex production-shaped gate
> 日期：2026-06-20
> 范围：仅 ANI Core Gateway/Auth service/Dex 生产形态认证门禁；不改 Services，不推远端
> 状态：**passed**；已解除 S01-S04 Auth/Dex production ready 阻断，但不代表 full platform production ready。

## 目标

补齐 S01-S04 B 轨从 `production-shaped acceptance passed` 升级到 production-ready acceptance 缺失的横切认证证据：

- Gateway 必须以 `ANI_AUTH_MODE=auth_service` 运行，禁止继续使用 dev auth。
- Gateway 必须通过 `AUTH_SERVICE_ADDR=ani-auth-service.ani-system.svc.cluster.local:9101` 调用集群内 auth-service。
- Dex discovery/JWKS、Gateway OIDC begin/complete、refresh token、受保护 Core API anonymous 401 与 bearer token 200 必须形成非敏感 evidence。

## 新增门禁

```bash
make validate-auth-dex-production-gate
```

该门禁检查：

- `deploy/real-k8s-lab/auth-dex-production-gate.yaml`
- `deploy/real-k8s-lab/sprint13-production-auth-dex.yaml`
- `deploy/real-k8s-lab/auth-dex-production-db-init.sql`
- `scripts/validate_auth_dex_production_gate.py`
- `scripts/validate_auth_dex_production_live.py`
- `development-records/live-evidence/sprint13-auth-dex-production-evidence.json`

## Live 结果

真实集群已部署 Dex + auth-service + production-shaped Gateway：

- Gateway auth mode：`ANI_AUTH_MODE=auth_service`
- Dex issuer：集群内 `ani-dex.ani-system.svc.cluster.local`
- Dex live transport：受控 NodePort，只用于从 Mac/Tailscale 执行 OIDC 浏览器式 flow
- 受保护验证路径：`/api/v1/auth/api-keys`
- evidence：`development-records/live-evidence/sprint13-auth-dex-production-evidence.json`

evidence 记录：

```text
status=passed
auth_dex_production_shape.status=passed
anonymous_status=401
oidc_begin_status=200
oidc_complete_status=200
authorized_status=200
refresh_status=200
```

## 修复项

本次 live gate 首次执行发现 Dex token request 返回 `invalid client_secret`。根因是 Dex 不自动展开 ConfigMap 中的 `$ANI_DEX_CLIENT_SECRET`，导致 Dex 与 auth-service 使用不同 client secret。

已修复为 Dex 容器启动时从 Kubernetes Secret 渲染 `/tmp/dex-config.yaml`，再执行 `dex serve`。门禁同步强制检查该运行时渲染路径，避免回归到未展开占位符。

## 边界

`SPRINT13-AUTH-DEX-PRODUCTION-GATE` 已通过，`validate-auth-dex-production-gate` 和 `validate-sprint13-b-track-production-shape` 均必须把该 evidence 作为 S01-S04 production readiness 的横切前置。

该结论只解除 Auth/Dex production ready 阻断；不代表 full platform v1.0.0 production ready。正式镜像发布/升级、长期 SLA/soak、备份/恢复和故障注入仍是后续 release gate。

S05-S07 B 轨可以继续，但仍必须按同一 production-shaped proof_items 标准验收。
