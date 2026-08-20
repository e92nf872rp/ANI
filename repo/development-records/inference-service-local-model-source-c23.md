# INFERENCE-SERVICE-LOCAL-MODEL-SOURCE-C23

> 日期：2026-08-17
> 状态：live passed
> 前置：`INFERENCE-SERVICE-MODEL-CATALOG-C6`、`INFERENCE-SERVICE-CONSOLE-SHAPED-E2E-C22`

## 完成范围

- 本地目录是第一刀真实模型来源：租户 Namespace 里已有 PVC，`storage_path=pvc://<claim>[#/path]`。
- Gateway `/api/v1/svc/models*` 接到 `model-service`。`GetModelVersion` 仍是内部 lookup，不挂租户 HTTP。
- HuggingFace / ModelScope 导入仍返回 `501 FEATURE_NOT_AVAILABLE`。
- inference-service catalog 只把 `pvc://` 版本当成可部署；按 capability `sglang` 选 SGLang，否则 vLLM。`engine.Launch` 按 `ExecutionProfile.Runtime` 分发。
- 集群内清单去掉 `INFERENCE_LAB_CATALOG`，改为 `MODEL_SERVICE_GRPC_ADDR`。
- 已在人工确认集群上 live passed：经现有 `ani-console` nginx `/api/` 打生产 `ani-gateway`，创建 Model + `pvc://vllm-model#/models/qwen` 版本，再用真实 `model_version_id` 完成 CPU vLLM create/`running`/stop/start/delete。`ANI_AUTH_MODE` 保持 `auth_service`。未删已有租户 Namespace 和 `vllm-model` PVC。GPU/LWS 仍 skip。
- 未改 OpenAPI，不得标记 runtime ready。

## Design Decisions

- 本地目录不是 hostPath，也不是第二条模型 API。产品链是：创建模型 → 登记 PVC 版本 → 用 `model_version_id` 创建 `InferenceService`。
- Core `platform-workloads` 仍然只吃 `image_ref` + `pvc://` artifact + command/args，不认识 vLLM/SGLang。
- OpenAPI 还没有 `engine_runtime` 字段。SGLang 用 capability `sglang` 作为过渡选择，不是契约字段。
- `CreateModel` proto 没有 `idempotency_key`；Gateway 校验该字段但不转发。
- 本集群 `ani` 库原先没有 `models`/`model_versions`（init schema 该段从未落地）；live 前按同一 schema 补表，不给现有 `inference_services` 加 FK。
- 一次性 PVC 播种 Job 使用 hostNetwork 只为写入租户本地目录，不是产品路径。

## 验证证据

```text
cd /root/kubercon/ANI/repo/services/ani-gateway && go test ./internal/router -count=1 -run 'TestCreateModel|TestImportModel|TestModelRoutes|TestGetModelMaps|TestCreateModelVersion'
cd /root/kubercon/ANI/repo/services/inference-service && go test ./internal/engine ./internal/catalog/modelsvc ./internal/service ./internal/runtime/coresdk -count=1
cd /root/kubercon/ANI/repo/services/model-service && go test ./internal/service -count=1
cd /root/kubercon/ANI/repo/pkg && go test ./adapters/runtime -count=1 -run 'TestPvcClaimName|TestRenderPlatformWorkloadManifestsMountsPVC'
python3 scripts/run_inference_local_model_source_e2e.py --kubeconfig /root/.kube/config
PATH=/tmp/ani-pybin:$PATH make validate-inference-local-model-source-live-gate
cd /root/kubercon/ANI/repo && PATH=/tmp/ani-pybin:$PATH make validate-services-route-contract validate-architecture validate-doc-entrypoints
```

evidence：`development-records/live-evidence/inference-local-model-source-live-20260817.json`

## 明确未完成

- HuggingFace / 魔塔导入和 MinIO 预签名上传属于 **model-service**，不是 inference-service。当前 Gateway `/api/v1/svc/models/import` 与 model-service `ImportModel`/`GetUploadURL` 仍是 `501 FEATURE_NOT_AVAILABLE`。推理服务只消费已就绪的 `model_version_id`（本刀仅 `pvc://`）。
- OpenAPI `engine_runtime` 字段。
- GPU device-plugin runtime live 与跨节点 LWS runtime live。
- 不得把本批次 CPU 产品路径标为 runtime ready。
