# SPRINT13-S01-S04-PRODUCTION-READINESS-REVIEW - Auth/Dex boundary review

> 记录类型：Sprint 13 S01-S04 B-track production readiness boundary review
> 日期：2026-06-20
> 范围：仅 ANI Core S01-S04 B 轨代码路径、部署契约、production-shaped gate 与 Auth/Dex 生产边界复审；不改 Services，不推远端
> 状态：**S01-S04 production-shaped acceptance passed；不能标记为 production ready**。

## 结论

S01-S04 B 轨的代码路径、部署契约和门禁已经达到 production-shaped acceptance standard ready，并且对应 evidence 已为 `production_shape.status=passed`。这证明的是组件生产形态验收：Gateway 走 in-cluster ServiceAccount/RBAC 或 metadata target / cluster Service 路径，live gate 不再依赖本机 kubectl proxy、port-forward 或 dev gateway 证据。

但 S01-S04 不能标记为 full platform production ready。当前 production-shaped Gateway manifest 仍显式使用 `ANI_AUTH_MODE=dev`，Auth/Dex production gate 尚未在 S01-S04 范围内执行并产出证据；`validate-auth-dex-smoke` 仍是本地 Docker/dev smoke，不是 in-cluster 生产形态 OIDC/Dex evidence。

因此：**S05-S07 B 轨可以继续**，但只能作为组件级 production-shaped live gate 继续推进；在 Auth/Dex production gate 通过前，整个平台和 S01-S04 聚合状态都不能标记为 production ready。

## 审查矩阵

| 范围 | 当前证据 | 结论 |
|---|---|---|
| S01 网络路由 Kube-OVN | Gateway `POST/GET /networks/routes` create/list + in-cluster ServiceAccount/RBAC + Kube-OVN 底层观测，`production_shape.status=passed` | production-shaped acceptance passed；不能标记为 production ready |
| S02 K8s workloads vCluster | Gateway provider create vCluster + metadata target TLS + workload list observe + cleanup，`production_shape.status=passed` | production-shaped acceptance passed；不能标记为 production ready |
| S03 storage Rook-Ceph | Gateway storage provider + in-cluster RBAC + volume/snapshot/filesystem/mount-target lifecycle + cleanup，`production_shape.status=passed` | production-shaped acceptance passed；不能标记为 production ready |
| S04 GPU inventory/DCGM | Gateway GPU inventory + Kubernetes NodeList + DCGM cluster Service metrics，`production_shape.status=passed` | production-shaped acceptance passed；不能标记为 production ready |
| Auth/Dex | Gateway deployment 仍为 `ANI_AUTH_MODE=dev`；本仓库只有 contract/local Docker smoke，没有 S01-S04 production-shaped Auth/Dex evidence | Auth/Dex production gate 未通过，是 full production release blocker |

## 后续生产可用门禁

若要把 S01-S04 聚合状态从 production-shaped acceptance passed 升级为 production ready，必须新增并跑通单独 Auth/Dex production gate，至少包含：

- Gateway 以非 dev auth 模式运行，受保护 API route 强制经过认证与 RBAC。
- Dex/OIDC issuer、JWKS、token、refresh 或等价会话链路经 in-cluster 或正式受控 endpoint 验证。
- evidence JSON 不包含 token、password、client secret、真实 kubeconfig 或服务器私密信息。
- 文档同步把 `ANI_AUTH_MODE=dev` 边界移除，并说明新的生产 Auth/Dex 证据路径。
- `validate-sprint13-b-track-production-shape` 更新为校验 Auth/Dex production gate，而不是接受 dev auth 边界。

## S05-S07 准入判断

当前状态可以进入 S05-S07 B 轨，但准入条件必须保持无歧义：

- S05-S07 B 轨继续按 `sprint13-production-shaped-gateway-profile.yaml` 的 proof_items 标准执行。
- S05-S07 通过后也只能标记对应切片 production-shaped acceptance passed。
- 在 Auth/Dex production gate 通过前，任何入口文档、development record 或 evidence 汇总都不能标记为 production ready。

## 门禁更新

`make validate-sprint13-b-track-production-shape` 已新增 production readiness boundary 检查：

- production-shaped Gateway manifest 中 `ANI_AUTH_MODE=dev` 必须被文档明确记录为 Auth/Dex production gate 阻断项。
- `ANI-DOCS-INDEX.md`、`ANI-06-开发计划.md`、`repo/CURRENT-SPRINT.md`、`repo/development-records/README.md` 和本记录必须同时说明 `S05-S07 B 轨可以继续` 与 `不能标记为 production ready`。
- 若后续实现真正 Auth/Dex production gate，必须同步更新 deployment、evidence、门禁和文档，不能只改状态文字。
