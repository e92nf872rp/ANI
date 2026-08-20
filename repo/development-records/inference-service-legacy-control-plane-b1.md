# INFERENCE-SERVICE-LEGACY-CONTROL-PLANE-B1

> 日期：2026-08-17
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 B.1
> 前置：`INFERENCE-SERVICE-GATEWAY-GRPC-C4`

## 完成范围

- 将旧 `inference.v1.InferenceServiceRPC` proto 标记 deprecated；禁止新增调用者。
- 产品路径保持 Gateway HTTP → `inference.control.v1.InferenceControl`；Gateway 不得 import `services/inference-service` 或旧 proto，不得复活 `GetEndpointURL` / `UpdateStatus`。
- Helm P0 保持 `infrastructure.runtime.providers.inference.enabled=false`，不部署 `inference-operator`。
- 当前集群未发现 InferenceService CRD / inference-operator，无需迁移存量旧控制面资源。
- Volcano 已为 C20 安装（scheduler + PodGroup），与本批次退役的旧 operator 控制面无关；未装 LWS/GPU。
- 未改 OpenAPI，未 rollout in-cluster Gateway，未 commit。

## 验证证据

```text
cd /root/kubercon/ANI/repo
PATH=/tmp/ani-pybin:$PATH make validate-inference-legacy-control-plane
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

集群只读盘点：无 InferenceService CRD，无 inference-operator Deployment。`volcano-system` 已由后续 C20 安装，不代表旧控制面复活。
