# INFERENCE-REMOVE-LAB-CATALOG-C26

> 日期：2026-08-17
> 状态：local/logic verified
> 前置：`INFERENCE-SERVICE-LOCAL-MODEL-SOURCE-C23`、`INFERENCE-REMOVE-LAB-GATEWAY-HARNESS-C25`

## 完成范围

- 从 `inference-service` 产品进程去掉 lab/fake catalog 装配：删除 `labCatalog` 和 `INFERENCE_LAB_CATALOG=1` 捷径。
- 进程启动必须配置 `MODEL_SERVICE_GRPC_ADDR` 和 `CORE_API_BASE_URL`；缺配或仍开 `INFERENCE_LAB_CATALOG=1` 直接 panic。
- 删除 `internal/catalog/fake`：`NewLab` 随 lab 捷径一起去掉；flow 单测把内存 catalog 写进 `flow_test.go`。
- C21 runner 不再注入已失效的 `INFERENCE_LAB_IMAGE_REF`。
- C12–C20 历史 live-gate yaml 的 `required_env: INFERENCE_LAB_CATALOG` 保留，不改写已 passed 的 lab 结论。
- 未改 OpenAPI。无新 live。不得标记 runtime ready。

## Design Decisions

- 产品 `main.go` 只装配真实 model-service 与 Core SDK。单测替身不进业务进程。
- 仍设置 `INFERENCE_LAB_CATALOG=1` 时 fail-closed，避免集群漏配后静默忽略该开关。
- 不保留独立 `catalog/fake` 包。Go 的 `*_test.go` 不能被别的包 import，但这个替身只有 flow 测试在用，直接写进对应测试文件。

## 验证证据

```text
cd /root/kubercon/ANI/repo && GOWORK=/root/kubercon/ANI/repo/go.work go test ./services/inference-service/... -count=1
git diff --check
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
```

## 明确未完成

- 未把 C16/C17/C19/C20 重写成现网 Gateway live。
- 不得把删除 lab catalog 标成 runtime ready。
