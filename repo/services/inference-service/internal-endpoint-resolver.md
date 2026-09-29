# 推理服务内部 SVC Resolver

KB 服务在集群内需要直连某个推理服务时，调用 inference-service 的内部 gRPC：

```text
inference.internal.v1.InferenceEndpointResolver/ResolveInternalEndpoint
```

请求使用服务归属租户和 `served_model_name` 查找运行中的推理服务：

```json
{
  "tenant_id": "<tenant-uuid>",
  "served_model_name": "qwen3.5-0.8b"
}
```

响应会返回实际命中的 `service_id`。旧调用仍可传 `service_id`，但新调用优先使用
`served_model_name`。

成功响应中的 `base_url` 是内部 OpenAI 兼容地址，例如：

```json
{
  "tenant_id": "<tenant-uuid>",
  "service_id": "<inference-service-uuid>",
  "base_url": "http://pw-<service-id>.ani-tenant-<tenant-id>.svc.cluster.local:8000/v1",
  "served_model_name": "qwen3.5-0.8b",
  "task": "generate",
  "status": "running"
}
```

KB/RAG 使用 `base_url` 作为 OpenAI client 的 API base，并使用
`served_model_name` 作为请求中的 `model`。根据 `task` 选择 OpenAI 路径：`generate` 调用
`/v1/chat/completions`，`embed` 调用 `/v1/embeddings`。不要从模型名、镜像名或 URL 猜测类型。
`base_url` 是当前运行状态的快照；服务停止、
重启或重新调度后，调用方应重新解析，不能永久缓存该地址。

resolver 会在 inference-service 内部校验：

- `tenant_id` 必须是非空 UUID，`served_model_name` 必须非空；
- 按模型名只匹配当前租户的服务，资源必须属于传入租户且没有被删除；不依赖 AI Gateway 发布状态；
- 服务状态必须是 `running`；
- `generation` 必须已经被 `observed_generation` 确认；
- 返回的 `task` 只有 `generate` 或 `embed`，来源是服务创建时冻结的模型能力；
- 已观测的 runtime endpoint 必须是当前服务租户命名空间中的 ClusterIP Service，端口为 `8000`；
- 不接受 HTTPS、IP、外部域名、URL path、query、fragment 或用户凭据。

KB 连接的是 inference-service 的集群内部 gRPC Service 地址，而不是模型 SVC；现有部署默认
地址为 `inference-service.ani-system.svc.cluster.local:9104`，以实际部署配置为准。

错误码：

| gRPC code | message | 含义 |
| --- | --- | --- |
| `INVALID_ARGUMENT` | `INVALID_ARGUMENT` | UUID 无效 |
| `NOT_FOUND` | `NOT_FOUND` | 服务不存在、已删除或不属于租户 |
| `FAILED_PRECONDITION` | `INFERENCE_SERVICE_NOT_READY` | 服务还没有 running，或 runtime generation 尚未确认 |
| `FAILED_PRECONDITION` | `RUNTIME_ENDPOINT_MISSING` | 尚未观测到内部 endpoint |
| `FAILED_PRECONDITION` | `RUNTIME_ENDPOINT_INVALID` | endpoint 不是受信的推理 Service |

这是集群内服务接口，不使用租户 JWT、用户 API Key 或 AI Gateway 路由头。部署时仍需用
集群网络策略限制 9104 gRPC 端口的调用方；当前 resolver 本身不把调用方提交的
`tenant_id` 当作调用者身份，也不返回公共 `invocation_url`、Core workload ID、Pod 名或
Secret。

生成的 Go client 位于：

```text
pkg/generated/pb/inference/resolver/v1
```

proto 源文件为：

```text
api/proto/inference/resolver/v1/inference_endpoint_resolver.proto
```
