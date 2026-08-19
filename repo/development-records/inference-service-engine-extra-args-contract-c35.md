# INFERENCE-SERVICE-ENGINE-EXTRA-ARGS-CONTRACT-C35

> 日期：2026-08-19  
> 状态：Services 契约本地验证完成，待人工评审与独立契约 PR  
> 前置：`INFERENCE-SERVICE-CREATE-IMAGE-CONTRACT-C27`  
> 范围：Services OpenAPI、专项语义门禁、Services SDK/API 文档/Console 类型生成物、进度记录；不含 Gateway handler、proto、`engine.Launch` 实现、live

## 目标

创建推理服务时可以追加引擎 CLI 参数并冻结，避免每个模型不适配都改 `launch.go` 再重建 inference-service。平台仍独占入口、模型路径、监听端口、tensor parallel、LWS Ray backend。

## 契约结果

- `CreateInferenceServiceRequest` 新增可选 `engine`（`$ref: InferenceServiceEngine`）。省略表示沿用平台默认启动参数。
- `InferenceService` 响应增量增加可选 `engine`，创建时冻结，只读；不进入 PATCH。
- `engine.extra_args` 最多 32 项。每项 `name` 必填（`^[a-z0-9][a-z0-9-]*$`，不含 `--`），`value` 可选。
- `InferenceServiceEngine.x-ani-reserved-engine-arg-names` 冻结保留名：`model`、`host`、`port`、`served-model-name`、`tensor-parallel-size`、`tp-size`、`distributed-executor-backend`、`device`。命中保留名由后续实现返回 `400 INVALID_ARGUMENT`。
- `additionalProperties: false`。不是 shell，不能更换入口二进制。

## 强制边界

- 本批次不改 Gateway handler、`InferenceControl` proto、`inference-service` `engine.Launch`。契约 PR 合入或明确批准前，不得把 extra_args 拼进容器 command。
- 无新 live。不得标记 GPU ready / runtime ready。

## 验证证据

```text
cd /root/kubercon/ANI/repo
python3 scripts/validate_inference_service_contract_test.py                  PASS（16 tests）
python3 scripts/validate_inference_service_contract.py                       PASS
python3 scripts/validate_yaml.py api/openapi/services/v1.yaml                PASS
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec                         PASS（15 tests）
PATH=/tmp/ani-pybin:$PATH make validate-services-contract                    PASS
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints                      PASS
git diff --check                                                              PASS
```

`make validate-services` 会刷新 SDK/API docs 并要求生成物相对提交后 HEAD 无漂移。本批次未提交时，该命令的生成物漂移检查会按预期停在未提交生成物上；提交后必须以个人仓库 GitHub Actions 为独立契约 PR 证据。

## 下一关

1. 人工评审：create 可选 `engine.extra_args`，保留名 400，PATCH 不能改引擎参数。
2. 只提交本批契约、门禁、生成物和进度记录；个人仓库 CI 全绿后再创建上游独立契约 PR。
3. 契约批准后，实现层才把冻结的 extra_args 追加到平台 command 之后。
