# 推理服务内部 SVC Resolver 设计

## 背景

知识库服务在集群内调用推理服务时，已经能够取得推理服务的 `service_id`。它需要根据
`tenant_id + service_id` 找到正在运行的推理服务内部 SVC，并直接访问 OpenAI 兼容接口。
这条内部数据面调用不经过公共 AI Gateway，也不使用租户用户的 API Key。

当前 Services API 的 `InferenceService` 投影故意不返回 `runtime_endpoint`，而现有 KB/RAG
路径只携带模型名，无法在多个推理服务之间确定目标 SVC。本设计只补内部服务发现能力，
不改变公开 Services API，不修改 KB 的数据库结构或业务代码。

## 目标与边界

目标：

- 提供一个只在集群内部使用的 gRPC resolver。
- 请求使用 `tenant_id` 和 `service_id`，不接受客户端提交的任意 URL。
- 只返回当前租户、当前推理服务已确认可用的内部 OpenAI base URL 和 served model name。
- 保持公开的 `GET /api/v1/svc/inference-services/{service_id}` 不返回内部地址。
- 不复活旧的 `inference.v1.InferenceServiceRPC/GetEndpointURL`。

不在本次范围内：

- KB 服务的字段、数据库、RAG 客户端或 protobuf 修改。
- 公共 AI Gateway 的路由、API Key 鉴权和 `invocation_url` 语义。
- Kubernetes API 的直接访问或由 KB 自己拼接 Service DNS。
- 新增用户可访问的 HTTP endpoint。

## 方案

在 inference-service 当前内部 gRPC server 上注册一个独立的
`inference.internal.v1.InferenceEndpointResolver` 服务。它与面向 ANI Gateway 的
`inference.control.v1.InferenceControl` 分离，避免把内部数据面地址加入公开控制面投影，
也避免复活已废弃的旧 proto。

建议 proto：

```proto
service InferenceEndpointResolver {
  rpc ResolveInternalEndpoint(ResolveInternalEndpointRequest)
      returns (ResolveInternalEndpointResponse);
}

message ResolveInternalEndpointRequest {
  string tenant_id = 1;
  string service_id = 2;
}

message ResolveInternalEndpointResponse {
  string tenant_id = 1;
  string service_id = 2;
  string base_url = 3;          // 例如 http://pw-<id>....svc.cluster.local:8000/v1
  string served_model_name = 4;
  string status = 5;            // 固定为 running
}
```

`base_url` 由 inference-service 使用已持久化并由 Core 观测确认的
`runtime_endpoint` 规范化得到，并追加 `/v1`。resolver 不根据请求中的 URL、模型名或
任意 Kubernetes 对象重新计算目标地址。

## 请求流程

```text
KB service
  │ tenant_id + service_id
  ▼
InferenceEndpointResolver (cluster-internal gRPC)
  │ repository.GetService(tenant_id, service_id)
  │ validate status == running and runtime_endpoint != empty
  ▼
base_url + served_model_name
  │
  ▼
KB/RAG → private SVC /v1/chat/completions or /v1/embeddings
```

resolver 直接读取 inference-service 自己的 repository，通过内部 use case 返回一个新的
resolver view；不能复用面向租户的 `ServiceView`，因为后者必须继续隐藏
`runtime_endpoint`、`runtime_ref` 和其他运行时字段。

服务身份由集群内部 gRPC 网络边界和部署的 ServiceAccount/NetworkPolicy 约束。该 RPC
不要求租户 JWT、用户 API Key 或 AI Gateway 路由头；`tenant_id` 仍用于资源归属校验，
不能被调用方用来跨租户读取地址。

## 校验与错误语义

- `tenant_id` 或 `service_id` 不是合法 UUID：返回 `INVALID_ARGUMENT`。
- 服务不存在、已删除或不属于该租户：统一返回 `NOT_FOUND`，避免跨租户枚举。
- 服务不是 `running`：返回 `FAILED_PRECONDITION`，消息包含稳定错误码
  `INFERENCE_SERVICE_NOT_READY`。
- 服务状态为 running 但 `runtime_endpoint` 为空：返回 `FAILED_PRECONDITION`，消息包含
  稳定错误码 `RUNTIME_ENDPOINT_MISSING`。
- 内部 endpoint 不是受信的集群 Service DNS 或无法规范化：返回
  `FAILED_PRECONDITION`，不得返回原始地址。

resolver 不返回公共 `invocation_url`，也不返回 Core workload ID、namespace、Pod 名或
Secret 内容。

## 兼容性

- 现有 Services OpenAPI、SDK、Gateway HTTP 路径和公开响应保持不变。
- 旧的 `default_inference_service` / `inference_service_name` 字段保持原状；KB 是否在
  后续增加 `service_id` 字段由 KB 负责，本次不改其契约。
- 新 proto 只增加一个独立的内部服务，已有 gRPC 方法和字段编号不变。
- resolver 不改变 AI Gateway 对外的多租户模型路由。

## 实现拆分

1. 新增 internal resolver proto 和生成物，定义 request/response 及独立 service。
2. 在 inference-service gRPC bootstrap 注册 resolver server。
3. 在 service 层新增内部 endpoint resolver use case，调用 repository 查询并执行租户、
   状态和 endpoint 校验。
4. 增加 proto handler、service 层和 endpoint 规范化测试。
5. 增加公开投影回归测试，确认 `runtime_endpoint` 仍不会出现在 Services API 或
   `InferenceControl` response 中。
6. 为 KB 团队提供 resolver 调用示例和部署地址配置说明；不修改 KB 实现。

## 验收标准

- 同租户、running、endpoint 已观测的 `service_id` 能返回唯一内部 `base_url`。
- 跨租户、未知、停止中和 endpoint 缺失的服务均返回预期 gRPC 错误。
- resolver 返回的 URL 只指向受信集群 Service，且路径为 `/v1`。
- 公开 Services API 和现有 AI Gateway 行为无回归。
- 相关 Go 测试、`make test`、`make validate-architecture` 和 `git diff --check` 通过。
