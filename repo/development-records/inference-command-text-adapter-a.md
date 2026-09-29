# INFERENCE-COMMAND-TEXT-ADAPTER-A

日期：2026-09-28
状态：LOCAL_VERIFIED / 未部署

## 目标

让 Services v1 的推理服务创建接口直接接受前端文本框中的
engine.command，同时保留旧客户端提交 []string argv 的兼容性。

## 实现

- CreateInferenceServiceEngine.command 在 Services OpenAPI 创建入参中支持 string/array 两个分支；响应仍使用 InferenceServiceEngine.command: string[]。
- ANI Gateway 只在 HTTP 边界解析文本为 argv，支持空格/制表符、单/双引号、相邻引用片段和受限反斜杠转义。
- 不执行 shell，不做变量、glob 或命令替换；未引用 Shell 操作符、未转义美元符号/反引号、未闭合引号、空参数、NUL、超限输入返回 400 INVALID_ARGUMENT。
- gRPC、数据库 desired_spec.engine.command、运行时继续使用 []string；未改 protobuf、数据库和 vLLM dtype 默认值。

## 验证

- go test ./services/ani-gateway/internal/router -run 'TestParseInferenceEngineCommand|TestInferenceCreate.*Command' -count=1
- go test ./services/ani-gateway/... -count=1
- go test ./services/inference-service/... -count=1
- python3 scripts/validate_inference_service_contract_test.py
- python3 scripts/validate_inference_service_contract.py
- python3 scripts/validate_openapi_spec.py api/openapi/services/v1.yaml
- SDK/API docs 生成及幂等性校验通过。

本批次未执行真实集群部署、Harbor 推送或 live inference 验证，不标记 runtime/production ready。
