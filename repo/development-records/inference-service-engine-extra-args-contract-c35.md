# INFERENCE-SERVICE-ENGINE-EXTRA-ARGS-CONTRACT-C35

> 日期：2026-08-19  
> 状态：Services 契约本地验证完成，待人工评审与独立契约 PR  
> 前置：`INFERENCE-SERVICE-CREATE-IMAGE-CONTRACT-C27`  
> 范围：Services OpenAPI、专项语义门禁、Services SDK/API 文档/Console 类型生成物、进度记录；不含 Gateway handler、proto、`engine.Launch` 实现、live、Console 表单实现

## 目标

创建推理服务时由前端传入环境变量与**完整启动命令**并冻结，避免每个模型不适配都改 `launch.go` 再重建 inference-service。启动命令不与平台默认 command 拼接、不追加。

## 契约结果

- `CreateInferenceServiceRequest` 新增可选 `engine`（`$ref: InferenceServiceEngine`）。省略表示沿用平台默认启动命令和环境。
- `InferenceService` 响应增量增加可选 `engine`，创建时冻结，只读；不进入 PATCH。
- `engine.env` 最多 32 项。每项 `name`+`value` 必填（`name` 为 `^[A-Za-z_][A-Za-z0-9_]*$`）。
- `engine.command` 是完整 argv（`string[]`，1–64 项），例如 `["python3","-m","vllm.entrypoints.openai.api_server","--model","/models/qwen","--host","0.0.0.0","--port","8000"]`。原样作为容器启动命令。
- 不再使用 `extra_args` / `args` / `InferenceServiceEngineArg`。不是 flag 白名单，也不追加在平台 command 之后。
- `x-ani-reserved-engine-env-names` 冻结：`CUDA_VISIBLE_DEVICES`、`NVIDIA_VISIBLE_DEVICES`、`NVIDIA_DRIVER_CAPABILITIES`、`PYTHONPATH`、`PATH`、`LD_PRELOAD`、`LD_LIBRARY_PATH`、`RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES`。命中保留 env 名由后续实现返回 `400 INVALID_ARGUMENT`。
- 不保留 CLI arg 名黑名单：完整命令由前端给出。`additionalProperties: false`。不是 shell 字符串。

## 强制边界

- 本批次不改 Gateway handler、`InferenceControl` proto、`inference-service` `engine.Launch`、Console 创建表单。契约 PR 合入或明确批准前，不得把 `env`/`command` 写入容器。
- 无新 live。不得标记 GPU ready / runtime ready。

## 验证证据

```text
cd /root/kubercon/ANI/repo
python3 scripts/validate_inference_service_contract_test.py                  PASS
python3 scripts/validate_inference_service_contract.py                       PASS
python3 scripts/validate_yaml.py api/openapi/services/v1.yaml                PASS
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec                         PASS
PATH=/tmp/ani-pybin:$PATH make validate-services-contract                    PASS
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints                      PASS
git diff --check                                                              PASS
```

`make validate-services` 会刷新 SDK/API docs 并要求生成物相对提交后 HEAD 无漂移。本批次未提交时，该命令的生成物漂移检查会按预期停在未提交生成物上；提交后必须以个人仓库 GitHub Actions 为独立契约 PR 证据。

## 下一关

1. 人工评审：create 可选 `engine.env` + 完整 `engine.command` argv，env 保留名 400，PATCH 不能改引擎参数。
2. 把本批契约更新推到已开的独立契约 PR。
3. 契约批准后，实现层才把冻结的 env 写入容器环境变量、把 `command` 原样作为容器启动命令；Console 创建页再绑定这两个字段。
