# INFERENCE-SERVICE-INCLUSTER-E2E-C21

> 日期：2026-08-17
> 状态：live passed（生产 `ani-system/ani-gateway`，`ANI_AUTH_MODE=auth_service`）
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 D / 1.1
> 前置：`INFERENCE-SERVICE-CPU-VLLM-LIVE-C14`、`INFERENCE-SERVICE-LEGACY-CONTROL-PLANE-B1`

## 完成范围

- 在 `ani-system` 部署 `inference-service`（产品控制面 gRPC）。
- 滚动现有生产 `ani-gateway` 到当前 Gateway 二进制，并追加 `INFERENCE_SERVICE_GRPC_ADDR` 与 `PLATFORM_WORKLOAD_PROVIDER=kubernetes_rest`。
- 产品路径：生产 Gateway HTTP `/api/v1/svc/inference-services*` → inference-service gRPC → Core `platform-workloads` → 真实 Kubernetes CPU vLLM。
- 健康检查走 ClusterIP，不再用本机 lab 进程、`kubernetes_proxy` 或第二条 Gateway。
- `ANI_AUTH_MODE` 保持 `auth_service`。产品 API 使用租户 JWT；`platform-workloads` 使用 service JWT（`CORE_SERVICE_TOKEN`）。为此滚动现有 `ani-auth-service` 到含 C7 ValidateToken 的当前二进制。
- 给现有 `ani-gateway` ServiceAccount 追加 ClusterRole：`nodes` get/list、CRD get/list、Volcano PodGroup、LWS。不替换生产 `ani-gateway-core-provider`。
- `cmd/platform-workload-live` 已在 C25 删除；产品入口只剩现网 `ani-gateway`。
- 不启用 Helm `infrastructure.runtime.providers.inference`（旧 operator 保持 false）。
- 不建 `ani-inference` Queue，不装 LWS，不标 GPU/runtime ready，不触碰 `ani-vllm-cpu-smoke`。
- 未改 OpenAPI。
- 单步等待：docker/deploy/stop 180s，服务 `running` 300s。

## Design Decisions

- 不再部署 `ani-inference-gateway`。Gateway 主入口已经存在，C21 只滚动它。
- Catalog 仍用 `INFERENCE_LAB_CATALOG=1` + smoke PVC 快照，以便在未接线真实 model-service 版本前跑通 CPU runtime。
- 不把生产 Gateway 改成 `ANI_AUTH_MODE=dev`。
- Create 路径会 `GET /api/v1/nodes` 填 NetworkPolicy CIDR；生产 ClusterRole 现场没有 nodes 规则，因此只在附加 Role 里补 `nodes`/`CRDs`。
- 失败时 runner 把 `ani-gateway` / `ani-auth-service` 镜像和环境回滚到滚动前状态；本次 live 成功，保持 C21 镜像。

## 验证证据

```text
cd /root/kubercon/ANI/repo
python3 scripts/run_inference_incluster_e2e.py --kubeconfig /root/.kube/config --skip-build
PATH=/tmp/ani-pybin:$PATH make validate-inference-incluster-e2e-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

evidence：`development-records/live-evidence/inference-incluster-e2e-live-20260817.json`
gate：`deploy/real-k8s-lab/inference-incluster-e2e-live-gate.yaml`（`status: live`）

## 明确未完成

- 未接真实 ModelCatalog / auth-service `IssueServiceToken` mint。
- 无 GPU device-plugin runtime，无 LWS runtime。
- 不得标记 GPU / LWS / full platform ready。
