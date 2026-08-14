# INFERENCE-SERVICE-CONTRACT-B

> 日期：2026-08-14
> 状态：Services 契约本地验证完成，待人工评审与独立契约 PR
> 范围：Services OpenAPI、专项语义门禁、精确 route/contract baseline、Services SDK/API 文档/Console 类型生成物；不含 handler、数据库、worker、reconciler、runtime 或集群部署

## 目标

按 `services/docs/console-modules/inference/inference-service-design.md` 阶段 B 冻结唯一的 Services `InferenceService` 产品契约，使后续可靠控制面只通过生成的 Core SDK 调用已合入的 `platform-workloads`，不继续扩展旧 inference gRPC、CRD 或 operator 链路。

## 契约结果

- 保持创建接口 `202 + InferenceService` 兼容语义，并新增 `current_operation_id`；PATCH、lifecycle 和删除返回 `202 + AsyncTask`，operation 查询返回 Services `AsyncTask`。
- 新增 `model_version_id`、`served_model_name`、统一 CPU/内存与可选 accelerator 资源规格、`placement_mode`、ready replicas、generation、状态诊断和更新时间字段。
- `gpu_type`、`gpu_count_per_pod`、`max_concurrency` 保留为 deprecated v1 兼容输入/投影；缺少 accelerator 时不得仅因历史 GPU count 默认值把请求推断为 GPU。
- 新增副本 PATCH、`start|stop|restart` lifecycle 和租户隔离的 inference operation 查询契约。
- 冻结异步任务语义：`task_type` 使用 `inference_service.create|scale|start|stop|restart|delete`，`resource_type` 固定为 `inference_service`。
- `invocation_url` 与兼容 `endpoint_url` 在 P0 均为 nullable；Services schema 不包含 `runtime_endpoint`、`runtime_ref` 或 Core `internal_endpoint`。
- 为所有 inference operations 补齐显式 Bearer/API key 认证声明，并移除对应历史 security baseline。
- policies PUT 保持 P1 兼容路径，新增 P0 `501 FEATURE_NOT_AVAILABLE`；受控 test 冻结 `422/502/503/504` 错误语义。
- 未绑定 path 的 `InferenceEndpoint/CreateInferenceEndpointRequest` 标记 deprecated，不允许发展成第二种产品资源。
- 新增专项 validator 与 14 个测试，并接入 `make validate-services-contract`；route baseline 只为尚未实现的 lifecycle 和 operation query 保留精确例外。
- 对既有 create/update/delete stub 的 `200/200/204` 与契约要求的 `202/202/202` 建立精确 handler baseline；阶段 C 修正任一 handler 后，陈旧 baseline 会使门禁失败并要求删除对应例外。

## 强制边界

- 本批次不修改 Core OpenAPI，不注册新的 Gateway handler，不创建 inference-service 目录、PG schema、worker、reconciler 或 Kubernetes 资源。
- Core `platform-workloads` 契约已合入；本 Services 契约仍须独立 PR 评审通过后，才可进入阶段 B.1/C 实现。
- 当前没有 CPU、单节点 GPU 或 LWS runtime evidence，不得标记 control-plane ready、runtime ready、cluster-internal inference ready 或 production ready。
- P0 不建设调用网关，不返回稳定公网调用地址，也不返回 ClusterIP。

## 验证

```text
python3 scripts/validate_inference_service_contract_test.py                  PASS（先 RED 后 GREEN，14 tests）
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec                         PASS（15 tests）
PATH=/tmp/ani-pybin:$PATH make validate-services-contract                    PASS
PATH=/tmp/ani-pybin:$PATH make validate-services-route-contract              PASS（39 accepted baseline warnings，0 errors）
PATH=/tmp/ani-pybin:$PATH make test                                           PASS
PATH=/tmp/ani-pybin:$PATH make validate-architecture                          PASS
git diff --check                                                              PASS
```

`make validate-services` 已运行至生成物漂移检查；前置 boundary、YAML、semantic、route、spec-split 与 SDK 门禁通过。该命令要求生成物相对提交后的 HEAD 无漂移，因此本地未提交契约批次会在 `git diff --exit-code -- sdks/core sdks/services docs/api` 按预期停止；提交后必须重新完整运行，并以个人仓库 GitHub Actions 作为独立契约 PR 的最终证据。

## 下一关

1. 人工评审本 Services schema、兼容字段、状态/错误语义和新增 operation。
2. 只提交本批契约、门禁、精确 baseline、生成物和进度记录；个人仓库 CI 全绿后再创建上游独立契约 PR。
3. 契约批准后进入阶段 B.1：退役旧 inference gRPC/CRD/operator 运行接线与新增调用守卫。
4. 随后进入阶段 C：inference-service PG resource/operation、lease worker、reconciler 和 fake Core/ModelCatalog 控制面闭环。
