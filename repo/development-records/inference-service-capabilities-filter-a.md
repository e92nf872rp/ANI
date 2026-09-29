# INFERENCE-SERVICE-CAPABILITIES-FILTER-A — 列表返回模型能力并支持 capability 筛选

完成日期：2026-09-29
验证结果：local verified；未部署或执行集群 live 验证

## 变更

创建推理服务时，inference-service 从 model-service 返回的模型版本能力读取
`capabilities`，做小写、去空值和去重后冻结到
`desired_spec.execution_profile.capabilities`。服务创建后不再根据镜像名、引擎或模型名推断能力。

Services `InferenceService` 的列表和详情响应新增只读 `capabilities` 数组；列表支持
`GET /api/v1/svc/inference-services?capability=embedding`，过滤在租户范围内的持久层执行，旧记录未冻结能力时返回空数组。
该批次不新增 Kubernetes label，不把 capability 过滤改成 `task` 过滤，也不修改 KB 服务。

## 兼容性与验证

这是 Services OpenAPI、内部 `InferenceControl` proto、Gateway JSON 和持久化投影的向后兼容新增字段；旧客户端可忽略该字段。已通过：

- `go test ./services/inference-service/... ./services/ani-gateway/internal/router/... -count=1`
- `PATH=/tmp/ani-pybin:$PATH make validate-services`
- `git diff --check`
