# INFERENCE-SERVICE-FAILURE-ROLLBACK-C18

> 日期：2026-08-15
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 6.4 / 9.3 / 9.4 / 17.1
> 前置：`INFERENCE-SERVICE-CLUSTERIP-NP-LIVE-C17`

## 完成范围

- Worker 按设计区分不可重试与可重试：accelerator 不可用、镜像不存在/未授权、未批准 engine profile、保留字段冲突进入终态；网络/未就绪等继续退避。
- create/start/restart 在不可重试错误、部署超时或重试耗尽后，先释放 provider runtime 并清空 `runtime_endpoint`，PlatformWorkload 记录可留；重试耗尽将 operation 标为 `dead_letter`。
- scale 失败不再只留下含糊 `failed`：事务把 `desired_spec` 恢复为 `applied_spec`、generation+1，并写入 `rollback_generation`；worker 用 operation ID + rollback generation 派生幂等键 Ensure 旧副本。
- 回滚成功：服务以旧规格 `running`，原 scale operation 为 `failed/SCALE_ROLLED_BACK`。回滚失败：服务 `failed/ROLLBACK_FAILED`。`desired_state=deleted` 时不发起补偿。
- 未改 OpenAPI，不复活 `/test`，未滚动生产 Gateway，无新的 vLLM/GPU live，不得标记 runtime ready。

## Design Decisions

- 有界重试默认 20 次；部署超时默认 15 分钟。超时记 `DEPLOY_TIMEOUT`/`failed`，次数耗尽记 `dead_letter`。自动重试不换 generation，也不换原 Ensure 幂等键。
- Core SDK 把 `IMAGE_*` / `ENGINE_PROFILE_UNAPPROVED` / `RESERVED_FIELD_CONFLICT` 映射为 runtime 哨兵错误；当前集群没有对应 live 注入，分类由单测覆盖。
- scale rollback 是同一 operation 的补偿代次，不是新的用户 task。ApplyObservation 仍按原 `target_generation` CAS，回滚完成走独立 `FinishScaleRollback`。
- 本批次只做 local/logic。RWO 下把 replicas 拉到 2 再宣称 `running` 再回滚，不适合当前 live 集群，因此不把 C18 标成 rollback live。

## 验证证据

```text
cd repo/services/inference-service
gofmt -w internal/reconcile/worker.go internal/reconcile/failure.go \
  internal/repository/store.go internal/repository/postgres.go \
  internal/runtime/runtime.go internal/runtime/coresdk/adapter.go
GOWORK=off go test ./... -count=1

cd repo
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 无 scale rollback / dead-letter 的真实 vLLM live evidence。
- 无 GPU device-plugin live，无 LWS / 跨节点，无公网调用网关。
- 真实 PostgreSQL integration 仍未连接执行。
- 不得把 in-cluster Gateway 或推理产品链路标为 control-plane/runtime ready。

## 下一批次边界

当前集群仍无 device-plugin 与 LWS。下一刀只做仍 unblock 且可验证的产品切片；未明确要求前不滚动生产 Gateway，不复活 `/test`。
