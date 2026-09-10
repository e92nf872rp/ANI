# OpenAI 兼容 API

## 页面定位

本文说明独立 **Envoy AI Gateway** 数据面的 OpenAI 兼容调用。它不属于
`ani-gateway` 控制面，也不是 `services/v1.yaml` 的 Services 资源。

`ani-gateway` 不再注册 `POST /v1/chat/completions`；该旧占位路由已移除，
不会再返回一个看似可用的 ANI 代理入口。推理服务内部仍可使用 vLLM 的
OpenAI 协议，不能把内部地址当作租户公网入口。

## 数据面入口

| 组件 | 路径 | 用途 |
|---|---|---|
| Envoy AI Gateway | `POST /v1/chat/completions` | OpenAI chat 请求与流式响应 |
| Envoy AI Gateway | `POST /v1/embeddings` | OpenAI embedding 请求 |
| ANI Gateway | 无 `/v1` chat 代理 | 仅提供 `/api/v1` 控制面与 Services API |

客户端应使用推理服务返回的 `invocation_url`（在服务运行且路由发布成功后
才有值）或平台公布的 Envoy AI Gateway 地址。不要把
`ani-gateway` 的控制面地址或租户内 vLLM ClusterIP 填入 OpenAI SDK。

## 鉴权与路由

- 使用平台签发的 API Key/Bearer 凭据；租户身份由认证上下文确定，不由请求体
  或自定义租户头决定。
- 请求体的 `model` 与已发布的 `served_model_name` 精确匹配；chat 与
  embeddings 的路径也参与路由匹配。
- 未发布、已停止、已删除或跨租户的模型应由数据面返回拒绝/找不到，不能回退
  到其它租户或内部端点。
- OpenAI 调用不需要 `idempotency_key`；创建模型和推理服务等控制面写操作
  仍遵循 Services API 的幂等要求。

## 控制面与调试

- 模型、版本和推理服务通过 `/api/v1/svc/*` 管理。
- `InferenceService.invocation_url` 是数据面地址的唯一产品投影；为空时，
  客户端应等待状态变为 `running` 且路由发布完成。
- Console 的服务测试页面与外部 OpenAI 调用是两条链路，不应假定控制面
  `/api/v1` 能处理 chat completion。

## 相关模块

- `inference-call-test.md` — Console/Services 内部测试契约
- `open-integration-overview.md` — 接入总入口
- `api-key-management.md` — API Key 生命周期
- `docs/superpowers/specs/2026-08-31-envoy-ai-gateway-tenant-aware-dynamic-publication-design.md`
  — Envoy 租户感知路由设计

## 边界

Envoy AI Gateway 的公网 URL、路由资源和限流策略由独立数据面发布流程
管理。本文不为 `ani-gateway` 增加 OpenAPI 路径，也不把旧占位路由重新标记
为生产能力。
