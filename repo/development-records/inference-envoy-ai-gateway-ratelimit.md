# INFERENCE-ENVOY-AI-GATEWAY-RATELIMIT — Envoy 推理入口限流

完成日期：2026-08-28
对应 Sprint：Sprint 13
验证结果：`make test` EXIT:0；`make validate-architecture` EXIT:0；Envoy C40 专项校验通过

## 实现了什么

Envoy AI Gateway 继续作为 chat/embeddings 的唯一推理数据面入口，新增 Gateway 级共享全局 600 requests/minute `BackendTrafficPolicy`。Redis 限流后端通过 `envoy-gateway-system` 的 ConfigMap 片段和 Secret 引用配置；AK 校验、租户隔离及每 AK RPM 仍由 auth-service 经 ext_authz adapter 负责。

## 关键文件改动

| 文件 | 说明 |
|---|---|
| `deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml` | 新增 Gateway 级 BackendTrafficPolicy |
| `deploy/real-k8s-lab/inference-envoy-ai-gateway-ratelimit-config.yaml` | Redis Secret 引用配置片段 |
| `scripts/validate_inference_envoy_ai_gateway_manifest.py` | 限流资源契约校验 |
| `scripts/validate_inference_envoy_ai_gateway_ratelimit_config.py` | Redis 配置片段与敏感信息校验 |
| `scripts/run_inference_envoy_ai_gateway_live.py` | live gate 等待限流策略 Accepted |

## 完工标准达成

- [x] C40 manifest 校验通过
- [x] Redis 配置片段校验通过
- [x] Envoy live-gate 契约测试通过（35 tests）
- [x] `make test` 全通
- [x] `make validate-architecture` 通过
- [x] AK 限流 429 通过 gRPC status details 传递窗口剩余时间，并输出 `Retry-After`（代码已实现，待新镜像发布后做真实 Gateway 验证）

## 备注

本批次未执行真实集群 apply、Redis 压测或 600 请求压力验证；真实 429 仍由 auth-service 的小 RPM AK 门禁验证。ANI Gateway `/v1` 代理和动态 `/inference-policies` CRUD 不在范围内。
