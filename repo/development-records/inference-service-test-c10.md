# INFERENCE-SERVICE-TEST-C10

> 日期：2026-08-15
> 状态：superseded
> 方案依据：产品口径纠正——`/test` 只是契约兼容测试路径，不是推理服务产品能力
> 前置：`INFERENCE-SERVICE-GATEWAY-GRPC-C4`

C10 曾实现 `POST /inference-services/{service_id}/test` 的 Gateway + 内部 gRPC + Tester。该入口会把控制面请求送到 runtime，增加 SSRF、超时和幂等面，且产品侧用不到。实现已删除。

OpenAPI 仍保留该兼容路径（PR #101，未改契约）。Gateway 不注册对应路由，`architecture/services-route-baseline.yaml` 重新登记 `spec_not_in_code`。policies 继续 501；logs 仍空列表。

## 明确未完成

- 不实现 `/test`，也不把它作为 live gate 条件。
- 不得标记 control-plane ready / runtime ready / live passed。
