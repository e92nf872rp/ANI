# INFERENCE-SERVICE-FAILURE-ROLLBACK-LIVE-C19

> 日期：2026-08-15
> 状态：live passed（lab Gateway 进程，未 rollout in-cluster `ani-gateway`）
> C25 已删除 lab Gateway harness 及本批次 runner；evidence 仍由 `make validate-inference-failure-rollback-live-gate` 校验。
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 6.4 / 9.3 / 9.4 / 17.1
> 前置：`INFERENCE-SERVICE-FAILURE-ROLLBACK-C18`、`INFERENCE-SERVICE-CPU-VLLM-LIVE-C14`

## 完成范围

- 收口 C18 产品缺口：`RUNTIME_NOT_READY` 只按部署超时结束，不再按 attempt 进 dead-letter；worker 限制可由 `INFERENCE_MAX_ATTEMPTS` / `INFERENCE_DEPLOY_TIMEOUT_SECONDS` / `INFERENCE_RETRY_DELAY_SECONDS` 配置；产品 operation 的 `error_message` 带稳定错误码前缀。
- 失败清理不再复用 create Ensure 幂等键；独立 cleanup key 才能让 Core 删除 provider runtime。
- scale rollback 用服务当前 generation CAS，operation 更新不再误卡在 `target_generation`；rollback 窗口从 rollback 开始时刻重新计时。
- 同一 `InferenceService` 入口 live：缺失镜像 create 在超时后 `failed/DEPLOY_TIMEOUT` 并删除 runtime；真实 CPU vLLM `running` 后 PATCH replicas=2，RWO 下补偿回 1，operation `failed/SCALE_ROLLED_BACK`；delete 后 404。
- 未改 OpenAPI，未滚动生产 Gateway，未触碰 `ani-vllm-cpu-smoke`，不实现 `/test`。
- 不得标记 runtime ready。

## Design Decisions

- 默认 attempt 预算改为 180，覆盖 15 分钟 / 5 秒退避；not-ready 只看 `INFERENCE_DEPLOY_TIMEOUT_SECONDS`。
- AsyncTask 契约没有独立 `error_code` 字段，稳定码写入 `error_message` 前缀，例如 `DEPLOY_TIMEOUT: ...` / `SCALE_ROLLED_BACK: ...`。
- Core platform-workload intent 按幂等键+指纹去重。cleanup 若复用 create key，Delete 会 CONFLICT，服务会一直 `deploying`。
- Live 用短超时（20s / 25s）证明超时路径；默认生产超时仍是 15 分钟。
- 未给 Core renderer 加 Volcano `schedulerName`，避免破坏已过的 CPU live。

## 验证证据

```text
cd repo/services/inference-service
gofmt -w internal/config/config.go internal/reconcile/worker.go \
  internal/reconcile/failure.go internal/repository/postgres.go \
  internal/service/control.go main.go
GOWORK=off go test ./... -count=1

cd repo
PATH=/tmp/ani-pybin:$PATH make validate-inference-failure-rollback-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
python3 scripts/run_inference_failure_rollback_live_gate.py --kubeconfig /root/.kube/config
```

evidence：`development-records/live-evidence/inference-failure-rollback-live-20260815.json`

## 明确未完成

- 无 GPU device-plugin live，无 LWS / 跨节点，无公网调用网关。
- 真实 PostgreSQL integration 仍未连接执行。
- 不得把 in-cluster Gateway 或推理产品链路标为 control-plane/runtime ready。
