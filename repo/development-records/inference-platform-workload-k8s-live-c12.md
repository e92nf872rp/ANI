# INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-C12

> 日期：2026-08-15
> 状态：live passed（仅限隔离 CPU PlatformWorkload lab）
> C25 已删除 lab Gateway harness（`cmd/platform-workload-live`）及本批次 runner；evidence 仍由 `make validate-platform-workload-k8s-live-gate` 校验。
> 方案依据：`INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-GATE-C9`
> 前置：`INFERENCE-PLATFORM-WORKLOAD-K8S-C8`、`INFERENCE-PLATFORM-WORKLOAD-K8S-C9`、人工确认集群可访问

## 完成范围

- 经人工确认后，对真实集群执行 C9 live checks：create / label / running / scale=2 / stop / start / lab Gateway 进程重启回读 / delete / 租户 403 / logs 空列表。
- 生产 `ani-system/ani-gateway` 仍是旧镜像，不含 C8/C9。本批次用本地 lab harness（当前代码 + `kubernetes_rest` + 集群 PG）访问真实 Kubernetes API，**没有** rollout 集群内 Gateway。
- 使用集群内已缓存的 digest-pinned busybox 作为 CPU runtime 镜像；标签 `ani.platform_workload=inference`，不写 `ani.kubercloud.io/instance`，不写 `/instances`。
- 在集群 PostgreSQL 应用 additive `platform_workloads` migration（该库没有 `ani_app` 角色，live runner 对 GRANT 做条件化，不改正式 migration 文件）。
- 脱敏 evidence：`development-records/live-evidence/platform-workload-k8s-live-20260815.json`。gate `status=live`。
- 未改 OpenAPI。无 LWS/GPU/vLLM，无产品推理 e2e，无 `/test`。

## Design Decisions

- 不滚动生产 Gateway：旧镜像没有 platform-workloads Kubernetes provider，滚动会失败或把未评审代码推进生产。
- kubeconfig 是 client cert，C8 REST client 只接受 Bearer。live runner 创建短期 ServiceAccount token，结束后删除 SA/RBAC。
- 重启检查是 lab 进程重启 + PG 回读，不是 `ani-system` Deployment rollout。
- 鉴权用 `ANI_AUTH_MODE=dev` 证明 handler 的 service-only 门（租户 403）。这不是 C7 生产 service JWT live。

## 验证证据

```text
cd repo
python3 scripts/run_platform_workload_k8s_live_gate.py
PATH=/tmp/ani-pybin:$PATH make validate-platform-workload-k8s-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 不得把 in-cluster Gateway、full platform 或推理产品链路标为 runtime ready / production-shaped。
- 无 inference-service 产品 e2e（Gateway `/inference-services` → gRPC → platform-workloads → 集群）。
- 无真实 Pod/Loki log store，无 LWS/GPU/vLLM，不实现 `/test`。

## 下一批次边界

要把产品入口接到真实集群，需要另开批次：部署含 C8/C9 的 Gateway。lab 进程路径已在 C25 删除。未明确要求前不滚动生产 Gateway。
