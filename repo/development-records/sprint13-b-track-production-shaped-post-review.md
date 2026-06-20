# SPRINT13-B-TRACK-PRODUCTION-SHAPED-POST-REVIEW - bootstrap in-cluster provider hardening

> 记录类型：Sprint 13 B-track production-shaped post-review hardening
> 日期：2026-06-20
> 范围：仅 ANI Core production-shaped Kubernetes provider 装配路径；不改 Services，不推远端
> 状态：**production-shaped acceptance passed for S01-S04**；不代表 full platform production ready。

## 目标

对 S01-S04 B 轨 production-shaped closure 做深度代码审查，确认生产部署路径不只覆盖 Gateway runtime 单一路径，还覆盖 `pkg/bootstrap` 能力装配路径。

## 审查发现

`KubernetesRESTClient` 已支持 in-cluster ServiceAccount token/CA，Gateway network/storage/gpu runtime 也已透传 `KUBERNETES_SERVICE_HOST/PORT` 与 ServiceAccount 文件路径；但 `pkg/bootstrap.Config` 与 `NewCapabilitiesWithConfig` 仍只向 Kubernetes REST provider 传递显式 `KUBERNETES_API_HOST` / bearer token / field manager。

影响边界：

- S01 `NETWORK_PROVIDER=kubeovn_rest` 的 bootstrap route provider 装配路径可能仍要求显式 API host。
- S03 `STORAGE_PROVIDER=kubernetes_rest` 的 bootstrap storage provider 装配路径可能仍要求显式 API host。
- S04 `GPU_INVENTORY_PROVIDER=kubernetes_rest` 的 bootstrap GPU inventory 装配路径可能仍要求显式 API host。
- S07 `INSTANCE_OBSERVABILITY_PROVIDER=prometheus_kubernetes` 后续 B 轨也会复用同一 Kubernetes REST 装配路径，需提前统一。

## 本次修复

- `pkg/bootstrap.Config` 新增并从环境读取：
  - `KUBERNETES_SERVICE_HOST`
  - `KUBERNETES_SERVICE_PORT`
  - `KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE`
  - `KUBERNETES_SERVICE_ACCOUNT_CA_FILE`
- `pkg/bootstrap/deps.go` 新增统一 `kubernetesRESTClientConfig` helper，所有 bootstrap Kubernetes REST provider 复用同一显式配置。
- `PrometheusInstanceObservabilityConfig` 同步支持 in-cluster ServiceAccount token/CA file，避免 S07 后续 B 轨沿用旧缺口。
- `KubernetesRESTClient` 不再直接读取 ambient process env；环境变量只在 Gateway/bootstrap 配置层读取，adapter 层保持显式 config，降低 CI 和生产排障的不确定性。
- Gateway `secret_runtime` 与 `k8s_proxy_runtime` 也同步显式透传 in-cluster ServiceAccount 配置，避免 adapter 显式化后回归 Sprint 5 Secret / node-pool provider 路径。
- 后续 S01 rerun 发现 Gateway network provider 仍为 route-only，已继续修复为 VPC/Subnet/SecurityGroup/LoadBalancer/Route 通用 provider pipeline；Kubernetes dry-run 也改为 server-side apply PATCH `dryRun=All`，避免 route 更新既有 VPC 时 POST create 409。

## 测试覆盖

- bootstrap env override 覆盖 in-cluster ServiceAccount 字段。
- bootstrap 装配覆盖 S01 network route、S03 storage、S04 GPU inventory 与 S07 Prometheus observability 的 in-cluster Kubernetes config。
- Gateway runtime env 覆盖 network、storage、GPU inventory、Secret 与 K8s node-pool provider 的 in-cluster Kubernetes config。
- Kubernetes REST adapter 覆盖显式 in-cluster config 可用，并拒绝只依赖 ambient env 的隐式配置。

## 生产边界

本次修复提升的是生产部署代码路径一致性和可验收性；S01-S04 已在正式 Gateway + in-cluster ServiceAccount/RBAC + 非本地 transport 路径重新执行对应 `--production-shaped` live gate，并产出新的非敏感 evidence JSON，均为 `production_shape.status=passed`。

该结论仍不等于 full platform production ready；当前 production-shaped Gateway 仍为 `ANI_AUTH_MODE=dev`，Auth/Dex production gate 尚未通过生产形态验证，生产发布还需要 Auth/Dex、正式镜像与升级、长期运行、备份/恢复和故障注入等门禁。因此 S01-S04 和平台聚合状态不能标记为 production ready；S05-S07 B 轨可以继续，但仍按组件级 production-shaped acceptance 标准验收。

Auth/Dex 边界复审见 `sprint13-s01-s04-production-readiness-review.md`。
