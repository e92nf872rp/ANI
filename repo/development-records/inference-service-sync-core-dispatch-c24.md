# INFERENCE-SERVICE-SYNC-CORE-DISPATCH-C24

> 日期：2026-08-17
> 状态：local/logic verified
> 前置：`INFERENCE-SERVICE-CONTROL-PLANE-C1`、`INFERENCE-SERVICE-LIFECYCLE-CONTROL-PLANE-C2`、`INFERENCE-SERVICE-GATEWAY-GRPC-C4`

## 完成范围

- 点击创建 / 扩缩 / 启停 / 删除时，inference-service 请求路径立刻调用 Core `platform-workloads`（Ensure / lifecycle / delete）。
- 可预知失败（容量不足、镜像不可用、拓扑不支持等）在本次 HTTP/gRPC 返回，不再先 `202` 再让 worker 消化。
- Core 未接受时：create 删除未提交行；scale/lifecycle/delete 回滚 pending mutation，服务保持点击前状态。
- Core 已接受后：写入 `runtime_ref`，状态 `deploying`，operation 仍 pending；worker 只做 Observe / Health / Smoke / 超时回收，不再替用户点第一次按钮。
- 请求路径与 worker 复用同一 generation 幂等键。Gateway inference gRPC 默认超时 30s。
- 未改 OpenAPI。现有 `202`/`422`/`503` 覆盖「已受理」和「Core 当场拒绝」。无 live，不得标记 runtime ready。

## Design Decisions

- worker 不是通用对账宿主，也不并进 Core `reconcile-worker`。它只对齐已 dispatch 的 runtime。
- 无 runtime 的单测仍只写库，保持 catalog/幂等用例最小。生产 `main` 始终注入 `InferenceRuntime`。
- create 在写库后 dispatch，保证名字冲突先于 Core apply。Core 拒绝则 AbortCreate。
- 瞬时 Observe 失败但已有 `runtime_ref` 仍返回 202，交给 worker 对齐。

## 验证证据

```text
cd /root/kubercon/ANI/repo/services/inference-service && go test ./... -count=1
cd /root/kubercon/ANI/repo/services/ani-gateway && go test ./internal/router ./... -count=1 -timeout 120s
```

## 明确未完成

- 未在集群 live 重跑 C21/C22/C23。现网 inference-service 仍是请求只写库、worker 第一次调 Core，需滚动后才生效。
- GPU/LWS runtime live。
- 不得把本批次标为 runtime ready。
