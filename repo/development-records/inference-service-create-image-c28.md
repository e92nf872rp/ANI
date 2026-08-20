# INFERENCE-SERVICE-CREATE-IMAGE-C28

> 日期：2026-08-18
> 状态：local/logic verified
> 前置：`INFERENCE-SERVICE-CREATE-IMAGE-CONTRACT-C27`
> 范围：Gateway 产品 HTTP 解析、`InferenceControl` proto、inference-service Creator/投影；不含 OpenAPI 再改、不含删除进程默认镜像环境变量、不含 live

## 目标

创建推理服务时使用契约里的 `image_id` / `image_ref`：至少填一个，同时传优先 `image_id`，创建前冻结 digest。进程环境里的引擎默认镜像不再是创建路径权威来源。

## 完成范围

- Gateway `POST /api/v1/svc/inference-services` 读取可选 `image_id` 与 `image_ref`。两者都缺返回 `400 INVALID_ARGUMENT`。
- 产品 HTTP 入口用现有 `ports.ImageRegistry`（与容器实例同一套 Harbor/local registry）解析仓库 `image_id`；手填且已是 `name@sha256:<64hex>` 的 `image_ref` 直接冻结。解析失败返回 `422 IMAGE_UNAVAILABLE`。
- 内部 `InferenceControl` proto 增加 create 入参与响应的 `image_id` / `image_ref`。Gateway 把原始 `image_id`（若有）和冻结 digest `image_ref` 传给 inference-service。gRPC `createInputFromProto` 拒绝两者都缺（`INVALID_ARGUMENT`）以及未 digest-pinned 的 `image_ref`（`IMAGE_UNAVAILABLE`），不再原样透传空字段。
- Creator 用请求里的 digest `image_ref` 覆盖 execution profile，不再写入 catalog/进程默认镜像。`image_id` 一并冻结供响应回读。未 pinned 的 `image_ref` 返回 `IMAGE_UNAVAILABLE`。
- 幂等 hash 包含 `image_id` / `image_ref`。响应投影增加可选 `image_id` 与只读 digest `image_ref`。二者不进入 PATCH。
- 未删除 `INFERENCE_CPU_IMAGE_REF` / `INFERENCE_GPU_IMAGE_REF` / `INFERENCE_SGLANG_*`；catalog 启动仍校验引擎底盘 digest，只是不再当作创建权威。
- 未改 OpenAPI。无新 live。不得标记 runtime ready。

## Design Decisions

- 镜像解析放在 Gateway 产品 HTTP 入口，而不是让 inference-service 调 Core registry。inference-service 不能 import Core；现有 service JWT 只有 `platform-workloads` 写权限，扩 `registry:read` 会改变跨层身份面。Gateway 已持有 `RegisterOptions.ImageRegistry`。
- 未把 `parseImageReference` 抽成公共包；Gateway 保留一份与实例解析同形的私有函数。
- 无 registry 的单测只接受已经 digest-pinned 的 `image_ref`；仓库路径用 stub `ListImages` 返回带 64-hex digest 的条目。
- 不把租户 JWT 转发到内部 gRPC。

## 验证证据

```text
cd /root/kubercon/ANI/repo/services/inference-service && go test ./... -count=1
  PASS
cd /root/kubercon/ANI/repo/services/ani-gateway && go test ./internal/router -count=1 -timeout 180s
  PASS
cd /root/kubercon/ANI/repo
make validate-architecture
  PASS
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
  PASS
git diff --check
  PASS
```

## 明确未完成

- 上游契约 PR #108 尚未合入；本实现留在本地 `main` 工作区，不与 C18–C26 混成一次发运。
- 未删除进程默认引擎镜像环境变量。
- 未重跑 C21/C22/C23 live；现网 create 在滚动前仍不读 `image_id` / `image_ref`。
- GPU/LWS runtime live。
- 不得把本批次标为 runtime ready。
