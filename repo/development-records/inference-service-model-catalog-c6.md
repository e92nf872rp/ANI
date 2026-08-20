# INFERENCE-SERVICE-MODEL-CATALOG-C6

> 日期：2026-08-15
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 4.3 / 9.1 / 10.1
> 前置：Core PR #99、Services PR #101、`INFERENCE-SERVICE-GATEWAY-GRPC-C4`、`INFERENCE-PLATFORM-WORKLOAD-LOCAL-C5`

## 完成范围

- model-service 增加内部 gRPC `GetModelVersion(tenant_id, model_version_id)`，按 version 回读父模型与不可变版本；跨租户 / 已删除返回 NOT_FOUND。
- 该 RPC 不是租户产品 HTTP。未改 Core/Services OpenAPI，Gateway 不新增 models version 路由。
- repo 增加 `GetVersionByID`：`model_versions` JOIN `models`，并显式校验 `tenant_id`。
- `GetModelVersion` 响应清空 `encrypt_hint`；inference-service catalog 只冻结 `model-encrypt/{version_id}` Key reference。
- inference-service 新增 `internal/catalog/modelsvc` adapter：按 format + capabilities 冻结 CPU/GPU engine profile；镜像必须 digest-pinned，禁止 `latest`。
- `MODEL_SERVICE_GRPC_ADDR` 非空时 Creator 走真实 catalog；未配置时继续 fake，避免本地启动依赖 model-service。
- 兼容映射：`safetensors`/`pytorch` → CPU+GPU；`gguf` → 仅 CPU；非 `text-generation` 或未知 format → `MODEL_INCOMPATIBLE`；父模型 `status != ready` → `Ready=false`（Creator 422 `MODEL_NOT_READY`）。

## Design Decisions

- 不扫 `ListModels` 找 version_id：分页会漏，也不能作为正式 catalog。
- engine profile 不从 model-service 读。model proto 没有 image/profile；由 inference-service 按设计 10.1 allowlist 冻结。
- 默认镜像是 digest-pinned 占位符，可用 `INFERENCE_CPU_IMAGE_REF` / `INFERENCE_GPU_IMAGE_REF` 覆盖；未验证真实 vLLM 镜像存在。

## Deviations

- 无 model-service PostgreSQL live、无真实加密 KMS、无 Harbor 已批准镜像准入。
- 无 CPU single-node 真实 provider / live gate、无 service JWT、无 `/test` 真调用、policies 仍 501。

## 验证证据

```text
cd repo/services/model-service && GOWORK=off go test -count=1 ./internal/service/
cd repo/services/inference-service && GOWORK=off go test -race ./... -count=1
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 无真实 K8s/LWS/Volcano/vLLM、无 service JWT、无 PG PlatformWorkload store、无推理 live evidence。
- 不得标记 control-plane ready、runtime ready 或 production ready。

## 下一批次边界

CPU single-node PlatformWorkload 真实 provider / live gate，或 service JWT。
