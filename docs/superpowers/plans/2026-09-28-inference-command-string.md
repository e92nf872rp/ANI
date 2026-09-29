# Inference command string Implementation Plan

> **For agentic workers:** 使用 executing-plans 在当前会话内执行；用户已选定普通字符串方案，不再重复询问该选项。

**Goal:** 前端将自定义命令文本直接传给 `engine.command`，旧数组客户端继续可用。

**Architecture:** Services OpenAPI 创建 schema 扩展为 string/array，Gateway 解析后转发现有 protobuf argv。响应与内部存储维持数组。

**Tech Stack:** Go 标准库、Hertz HTTP handler 测试、OpenAPI YAML、现有 Python 契约与生成工具。

## Global Constraints

- 只在 main 工作，保留会话前已有修改；不自动 commit、push 或部署。
- 语法和边界以同名 design 为准；不引入 Shell 执行器、依赖或数据库迁移。
- 先更新 Services v1 契约，再新增失败测试，再实现。

## Task 1: 请求契约及 HTTP 适配

- [x] `repo/api/openapi/services/v1.yaml` 增加 `CreateInferenceServiceEngine`；创建请求使用新 schema，响应 schema 不变。
- [x] `repo/services/ani-gateway/internal/router/inference_engine_command_test.go` 覆盖 HTTP 文本转 argv、单/双引号、转义、空参数/语法错误、长度和参数数量限制、旧数组兼容及数组回显。
- [x] 运行 `go test ./services/ani-gateway/internal/router -run 'TestInferenceCreate.*Command' -count=1`，记录文本输入被旧实现 400 拒绝的失败。
- [x] 在 Gateway HTTP DTO 边界解析 command 文本；内部 protobuf、数据库和运行时继续使用 argv 数组。
- [x] 运行上述聚焦测试至通过；再运行 Gateway 模块和 inference-service 模块测试。

## Task 2: 契约验证与生成物

- [x] 更新现有 `repo/scripts/validate_inference_service_contract.py` 及其测试以验证创建专用 schema、文本/数组分支以及原有响应不变。
- [x] 运行契约测试、契约脚本和 OpenAPI 校验。
- [x] 运行现有 `scripts/gen_sdk_alpha.py` 与 `scripts/generate_api_docs.py`；生成 diff 仅属 Services。
- [x] 追加批次记录和规定的三个索引；运行 `make validate-services`、`make test`、`make validate-architecture`、`git diff --check`，记录实际失败/跳过，不把未部署变更称为 live。
