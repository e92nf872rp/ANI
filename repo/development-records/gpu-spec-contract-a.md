# GPU-SPEC-CONTRACT-A

> 日期: 2026-07-28
> 类型: Core API 契约批次
> 状态: 契约与生成物完成，等待个人仓库 CI 和契约评审

## 目标

为实例管理的 `gpu_container_config.gpu.spec_id` 提供最小、只读、可评审的 Core GPU 规格契约。该批次只定义规格查询与实例引用，不实现 handler、port、adapter、CRD、切片或租户配额。

## 契约

- 新增 `GPUSpecSummary` 和 `GPUSpecListResponse`。
- 新增 `GET /gpu-specs` 和 `GET /gpu-specs/{spec_id}`。
- `CreateGPUContainerInstanceConfig.gpu` 新增可选 `spec_id`。
- `vendor`、`model`、`count`、`allocation_mode` 保留并标记 deprecated。
- `POST /instances` 的 422 语义增加 `GPUSpecNotFound`、`GPUSpecUnavailable`、`GPUSpecInventoryMismatch`。
- 规格只描述 `gpu_type`、`shares`、`mb_per_share` 等资源形态；契约不包含 quota、used_count，也不执行配额 check/acquire/release。

## 生成物

- Core Go/Python/Java/TypeScript SDK。
- Console Core TypeScript schema。
- Core 静态 API 文档和 SDK metadata。

## 门禁

已通过:

```text
python3 scripts/validate_openapi_spec_test.py
python3 scripts/validate_yaml.py api/openapi/v1.yaml
make validate-openapi-spec
make validate-core-api-compatibility
make validate-sdk-alpha
make validate-doc-api
make validate-ci-workflow
make validate-architecture
make test
make build
npm --prefix frontends/console audit --audit-level=high
npm --prefix frontends/console run type-check
npm --prefix frontends/console run lint
npm --prefix frontends/console run build
docker run ... golang:1.25.12 # build/test-go/gosec/gofmt/golangci-lint/coverage/govulncheck
docker run ... python:3.12    # pip-audit -r ai/rag-engine/requirements.txt
git diff --check
```

`validate_openapi_spec_test.py` 已接入 `make validate-openapi-spec` 和 GitHub Actions `api-spec-lint`，持续固定 `spec_id` 与“本期无配额语义”的边界。

补充说明:

- Console lint 通过，保留一个与本批次无关的既有 `react-hooks/exhaustive-deps` warning；Vite build 保留既有 chunk-size warning。
- `make validate-services` 的 boundary、semantic contract、route contract、spec split 和 SDK Beta 均通过；最后的生成物漂移检查在未提交工作区按设计失败，因为本批次正包含 SDK/API docs 预期变更。该检查必须以提交后的干净 HEAD 或个人仓库 GitHub Actions 为最终证据。

## 未包含

- `/gpu-specs` handler、store、CRD 或真实 provider。
- GPU slice 创建、分配和持久化。
- GPU quota API、数据表、扣减、占用或释放。
- 实例主契约、实例 handler/ports/adapters 或 Console 页面实现。
- real-provider 或 production-ready 声明。
