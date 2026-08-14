# INFERENCE-PLATFORM-WORKLOAD-CONTRACT-A

> 日期：2026-08-13
> 状态：Core 契约本地验证完成，待人工评审与独立契约 PR
> 范围：OpenAPI、专项契约测试、Core SDK/API 文档生成物；不含 handler、port、adapter、数据库或集群部署

## 目标

为 Services inference-service 提供唯一合法的 Core 跨层工作负载契约，避免 Services 直接操作 Kubernetes、复用租户 `/instances`，或接入旧 inference gRPC/CRD/operator 控制面。

## 契约结果

- Core v1 新增 `PlatformWorkload*` provider-neutral schemas。
- 新增能力查询，以及 create/get/scale/delete/lifecycle/logs 共 7 个 operation。
- 所有 mutation 使用 `202 + AsyncTask`；创建、PATCH、lifecycle 使用 body `idempotency_key`，DELETE 使用 `Idempotency-Key` header。
- AsyncTask 新增 `platform_workload.create/scale/start/stop/restart/delete` 和 `platform_workload` resource type。
- 显式冻结 service-only 认证：只接受 Bearer service JWT，要求 `aud=ani-core`、`principal_kind=service`、`tenant_id` 和相应 platform-workloads scope；API key 与租户用户 JWT 不可调用。
- 所有 operation 标记 `x-ani-exposure: internal`；部署层不得通过租户或公网 Ingress 发布，只允许 inference-service 通过集群内部 Core endpoint 调用。
- CPU 示例不提交 accelerator，使用 `single_node`、digest-pinned image 和 `cluster_internal` exposure。
- accelerator 只引用 Core GPUSpec `spec_id`；leader-worker 只表达 role resources/profile，调用方不能提交 LWS/PodGroup/Volcano 原生字段。
- `internal_endpoint` 仅对授权服务身份返回，且明确禁止进入 `/instances` 或租户侧 Services API。

## 关键边界

- 本批次不注册 Gateway 路由，不实现 PlatformWorkload 存储或 reconcile，不创建任何真实 workload。
- P0 Core 契约允许 single-node 和 leader-worker shape，但 leader-worker 固定 `replicas=1`；真实 LWS/Volcano/Ray 能力必须在后续实现与 live gate 单独证明。
- Services InferenceService 契约仍需下一份独立契约变更；本批次不修改 `api/openapi/services/v1.yaml`。
- 校验器曾引用已更名的 `demo_instances.go/demo_instances_test.go`，现已统一更新为 `instances.go/instances_test.go`，并新增回归测试接入 `validate-core-beta`；同时把 Core Alpha 的正式 console 路由期望对齐为 `api.createConsoleSession`。

## 验证

```text
python3 scripts/validate_openapi_spec_test.py -k platform_workload_service_contract_is_frozen  PASS（先 RED 后 GREEN）
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec                         PASS（15 tests）
PATH=/tmp/ani-pybin:$PATH make validate-core-api-compatibility               PASS
PATH=/tmp/ani-pybin:$PATH make validate-core-beta                            PASS
PATH=/tmp/ani-pybin:$PATH make validate-core-alpha                           PASS
PATH=/tmp/ani-pybin:$PATH make validate-spec-split                           PASS
PATH=/tmp/ani-pybin:$PATH make validate-sdk-alpha validate-doc-api            PASS
PATH=/tmp/ani-pybin:$PATH make validate-mock-a                                PASS
PATH=/tmp/ani-pybin:$PATH make validate-architecture                          PASS
PATH=/tmp/ani-pybin:$PATH make test                                           PASS
```

## 下一关

1. 人工评审 Core schema、服务 JWT claims 和 7 个 operation。
2. 按 API-first 规则单独 commit/push/CI/PR；契约未批准前不得实现 handler/port/adapter。
3. 契约批准后，先实现 CPU single-node PlatformWorkload 最小纵切，再分别推进 GPU Deployment 与 LWS live gate。
