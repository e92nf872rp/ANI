# INFERENCE-REMOVE-LAB-GATEWAY-HARNESS-C25

> 日期：2026-08-17
> 状态：local/logic verified
> 前置：`INFERENCE-SERVICE-INCLUSTER-E2E-C21`、`INFERENCE-SERVICE-CONSOLE-SHAPED-E2E-C22`、`INFERENCE-SERVICE-LOCAL-MODEL-SOURCE-C23`

## 完成范围

- 删除第二条 Gateway 入口：`services/ani-gateway/cmd/platform-workload-live`（含 `main_test.go`）。
- 删除只靠该 harness 起本机 Gateway 的 live runner：C12、C16、C17、C19、C20。
- `scripts/run_inference_cpu_vllm_live_gate.py` 只保留 C21+ 共用的 helper；直接执行会明确失败，不再 `go build ./cmd/platform-workload-live`。
- 产品 live 仍走现网 `ani-gateway`：C21 集群内、C22 Console nginx `/api/`、C23 本地 PVC 模型来源。
- C12–C20 历史 evidence 与 `make validate-*-live-gate` 保留；不改写已 passed 的 lab 结论。
- 未改 OpenAPI。无新 live。不得标记 runtime ready。

## Design Decisions

- 不把旧 lab runner 改写成打现网 Gateway。C16/C17/C19/C20 若要复跑，另开批次走生产 `ani-gateway`。
- 不删除历史 validator：它们校验的是当时 lab 进程 evidence（`gateway=lab-process-not-in-cluster-ani-gateway`）。
- C14 脚本继续作为 helper 库，避免把 kubectl/redact/PVC clone 再拆一层。

## 验证证据

```text
test ! -e services/ani-gateway/cmd/platform-workload-live/main.go
python3 scripts/run_inference_cpu_vllm_live_gate.py ; echo exit:$?
PATH=/tmp/ani-pybin:$PATH make validate-platform-workload-k8s-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-inference-cpu-vllm-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-inference-gpu-lws-volcano-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 未把 C16 GPU 准入、C17 ClusterIP/NetworkPolicy、C19 rollback、C20 GPU/LWS 重写成现网 Gateway live。
- 不得把删除 lab harness 标成 runtime ready。
