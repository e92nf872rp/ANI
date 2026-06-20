# SPRINT13-S01-S04-PRODUCTION-READINESS-REVIEW - Auth/Dex boundary review

> 记录类型：Sprint 13 S01-S04 B-track production readiness boundary review
> 日期：2026-06-20
> 范围：仅 ANI Core S01-S04 B 轨代码路径、部署契约、production-shaped gate 与 Auth/Dex 生产边界复审；不改 Services，不推远端
> 状态：**S01-S04 production-shaped acceptance passed；Auth/Dex production ready 阻断已解除**。

## 结论

S01-S04 B 轨的代码路径、部署契约和门禁已经达到 production-shaped acceptance standard ready，并且对应 evidence 已为 `production_shape.status=passed`。这证明的是组件生产形态验收：Gateway 走 in-cluster ServiceAccount/RBAC 或 metadata target / cluster Service 路径，live gate 不再依赖本机 kubectl proxy、port-forward 或 dev gateway 证据。

`SPRINT13-AUTH-DEX-PRODUCTION-GATE` / Auth/Dex production gate 已在真实集群通过。production-shaped Gateway 固定 `ANI_AUTH_MODE=auth_service`，通过集群内 `AUTH_SERVICE_ADDR=ani-auth-service.ani-system.svc.cluster.local:9101` 调用 auth-service，并经 `validate-auth-dex-production-gate` 证明：

- anonymous protected API 返回 401。
- Gateway OIDC begin 返回 200。
- Dex 登录后 Gateway OIDC complete 返回 200。
- Dex-backed ANI access token 访问 protected API 返回 200。
- refresh token flow 返回 200。

因此 S01-S04 的 Auth/Dex production ready 阻断已解除。该结论仍不等于 full platform v1.0.0 production ready：正式镜像发布/升级、长期 SLA/soak、备份/恢复和故障注入仍需后续 release gate。

## 审查矩阵

| 范围 | 当前证据 | 结论 |
|---|---|---|
| S01 网络路由 Kube-OVN | Gateway `POST/GET /networks/routes` create/list + in-cluster ServiceAccount/RBAC + Kube-OVN 底层观测，`production_shape.status=passed` | production-shaped acceptance passed；Auth/Dex 阻断已解除 |
| S02 K8s workloads vCluster | Gateway provider create vCluster + metadata target TLS + workload list observe + cleanup，`production_shape.status=passed` | production-shaped acceptance passed；Auth/Dex 阻断已解除 |
| S03 storage Rook-Ceph | Gateway storage provider + in-cluster RBAC + volume/snapshot/filesystem/mount-target lifecycle + cleanup，`production_shape.status=passed` | production-shaped acceptance passed；Auth/Dex 阻断已解除 |
| S04 GPU inventory/DCGM | Gateway GPU inventory + Kubernetes NodeList + DCGM cluster Service metrics，`production_shape.status=passed` | production-shaped acceptance passed；Auth/Dex 阻断已解除 |
| Auth/Dex | Gateway deployment 为 `ANI_AUTH_MODE=auth_service`；Dex discovery/JWKS、OIDC begin/complete、protected API、refresh 均为 passed evidence | Auth/Dex production gate passed |

## 生产可用边界

S01-S04 现在具备 production-shaped acceptance + Auth/Dex production gate passed 的组合证据。允许把 S01-S04 的 Auth/Dex 阻断状态更新为 resolved。

仍不得把该结论扩展为 full platform production ready，除非后续 release gate 单独通过：

- 正式镜像发布、签名、升级和回滚。
- 长期 SLA/soak。
- backup/restore 演练。
- 故障注入和恢复演练。
- S05-S07 对应 production-shaped live gate。

## S05-S07 准入判断

**S05-S07 B 轨可以继续**，但准入条件必须保持无歧义：

- S05-S07 B 轨继续按 `sprint13-production-shaped-gateway-profile.yaml` 的 proof_items 标准执行。
- S05-S07 通过后也只能先标记对应切片 production-shaped acceptance passed。
- 若后续要把平台聚合状态升级为 full platform production ready，必须追加 release gate，而不是复用 S01-S04 或 Auth/Dex evidence。

## 门禁更新

`make validate-sprint13-b-track-production-shape` 已更新为强制校验 Auth/Dex evidence：

- `development-records/live-evidence/sprint13-auth-dex-production-evidence.json` 必须存在。
- `auth_dex_production_shape.status` 必须为 `passed`。
- `gateway_auth_mode` 必须为 `auth_service`。
- proof_items 必须包含 `gateway_non_dev_auth`、`dex_discovery_and_jwks`、`gateway_rejects_anonymous`、`gateway_accepts_dex_oidc_token`、`gateway_refresh_token`、`auth_service_rbac_check`。
- `anonymous_status=401`，`oidc_begin_status=200`，`oidc_complete_status=200`，`authorized_status=200`，`refresh_status=200`。

入口文档必须同时记录 `SPRINT13-AUTH-DEX-PRODUCTION-GATE`、`validate-auth-dex-production-gate`、`Auth/Dex production gate`、`ANI_AUTH_MODE=auth_service`、`production ready` 和 `S05-S07 B 轨可以继续`，确保人和 AI 都不会再引用旧的 dev auth 阻断结论。
