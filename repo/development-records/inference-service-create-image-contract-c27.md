# INFERENCE-SERVICE-CREATE-IMAGE-CONTRACT-C27

> 日期：2026-08-18
> 状态：Services 契约本地验证完成，待人工评审与独立契约 PR
> 前置：`INFERENCE-SERVICE-CONTRACT-B`、`INFERENCE-REMOVE-LAB-CATALOG-C26`

> 范围：Services OpenAPI、专项语义门禁、Services SDK/API 文档/Console 类型生成物、设计文档；不含 Gateway handler、proto、inference-service 实现、live 或产品原型

## 目标

创建推理服务时必须能指定运行镜像，且来源与容器实例一致：既可从镜像仓库选择 `image_id`，也可由用户直接输入 `image_ref`。进程环境里的引擎默认镜像不是创建路径权威来源。

## 契约结果

- `CreateInferenceServiceRequest` 新增可选 `image_id`、可选 `image_ref`。两者至少填一个；同时传入时优先 `image_id`。创建前固定 digest。
- OpenAPI `required` 仍为 `idempotency_key, name, model`，避免把仓库选择做成唯一合法路径。两者都缺时由实现返回 `400 INVALID_ARGUMENT`。
- `InferenceService` 响应增量增加可选 `image_id` 与只读 `image_ref`（冻结 digest）。二者不进入 PATCH。
- `InferenceUnprocessableEntity` 增加稳定错误码 `IMAGE_UNAVAILABLE`：选定或输入的镜像无法解析为 digest。
- 产品原型按用户要求已还原，不随本批次改动。

## 强制边界

- 本批次不改 Gateway handler、`InferenceControl` proto、`inference-service` Creator/catalog/config。契约 PR 合入或明确批准前，不得把创建路径改成读取 `image_id` / `image_ref`。
- 不删除 `INFERENCE_SGLANG_*` / vLLM 默认镜像环境变量；那是后续实现批次的事。
- 无新 live。不得标记 runtime ready。

## 验证证据

```text
cd /root/kubercon/ANI/repo
python3 scripts/validate_inference_service_contract_test.py                  PASS（15 tests）
python3 scripts/validate_inference_service_contract.py                       PASS
python3 scripts/validate_yaml.py api/openapi/services/v1.yaml                PASS
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec                         PASS（15 tests）
PATH=/tmp/ani-pybin:$PATH make validate-services-contract                    PASS
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints                      PASS
git diff --check                                                              PASS
```

`make validate-services` 会刷新 SDK/API docs 并要求生成物相对提交后 HEAD 无漂移。本批次未提交时，该命令的 `git diff --exit-code -- sdks/core sdks/services docs/api` 会按预期停在未提交生成物上；提交后必须以个人仓库 GitHub Actions 为独立契约 PR 证据。

## 下一关

1. 人工评审本 Services schema：创建镜像为仓库 `image_id` 或手填 `image_ref`，至少填一个，优先 `image_id`。
2. 只提交本批契约、门禁、生成物和进度记录；个人仓库 CI 全绿后再创建上游独立契约 PR。
3. 契约批准后，实现层才把 create 的 `image_id` / `image_ref` 解析为 digest，冻结进 execution profile，并停止用进程默认镜像作为创建权威。
