# INFERENCE-PLATFORM-WORKLOAD-K8S-C9

> 日期：2026-08-15
> 状态：local/logic verified；live 执行见 `INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-C12`
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 D
> 前置：`INFERENCE-PLATFORM-WORKLOAD-K8S-C8`

## 完成范围

- Kubernetes Apply 先确保租户 Namespace（`ani-tenant-{id}`），再 apply Deployment/Service。不在 delete 时删除 Namespace。
- PlatformWorkload 控制面记录可持久化：memory store 可在进程间共享以证明重启回读；`DATABASE_URL` 存在时 Gateway 使用 PostgreSQL metadata store（RLS）。
- Additive migration `20260815000100_platform_workloads.sql`：`platform_workloads` + `platform_workload_intents`，active-name 部分唯一索引，FORCE RLS。
- Live gate 契约 `INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-GATE-C9`：本批次只落地契约与 local/logic。真实集群执行与 evidence 由 `INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-C12` 收口。
- 未改 OpenAPI；logs 仍空；无 LWS/GPU/vLLM。

## Design Decisions

- 产品 port 仍是 `PlatformWorkloadService`。store 是 adapter 内部状态，不新增产品 port。
- `PLATFORM_WORKLOAD_PROVIDER=kubernetes_rest` 且无 `DATABASE_URL` 时继续用 memory store，避免本地 K8s 实验被 PG 阻断；live 重启检查要求 `DATABASE_URL`。
- live validator 在 `status=live` 时要求脱敏 evidence 且 10 个 check 全 passed，防止未归档 evidence 就升格。

## 验证证据

```text
cd repo
go test ./pkg/adapters/runtime/ -count=1 -run 'PlatformWorkload'
go test ./services/ani-gateway/ -count=1 -run PlatformWorkload
PATH=/tmp/ani-pybin:$PATH make validate-platform-workload-k8s-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 本批次本身无 live evidence；后续 C12 已归档隔离 CPU lab evidence，仍不得标记 runtime ready / real-provider ready。
- logs 真实采集、LWS/GPU/vLLM 仍未做。不实现 `/test`。

## 下一批次边界

已由 `INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-C12` 执行 live。后续不要滚动生产 Gateway，也不要把产品推理链路标为 runtime ready。
