# INFERENCE-SERVICE-CREATE-IMAGE-LIVE-C30

> 日期：2026-08-18
> 状态：live passed（生产 `ani-system/ani-gateway` + `inference-service`，`ANI_AUTH_MODE=auth_service`）
> 前置：`INFERENCE-SERVICE-CREATE-IMAGE-C28`、`INFERENCE-SERVICE-REMOVE-DEFAULT-IMAGE-ENV-C29`、`INFERENCE-SERVICE-LOCAL-MODEL-SOURCE-C23`

## 完成范围

- 滚动现网 `ani-gateway` 与 `inference-service` 到含 C28/C29 的二进制（tag `create-image-c30-20260818`）。
- 从现网 `inference-service` 去掉残留 `INFERENCE_CPU_IMAGE_REF` / `INFERENCE_GPU_IMAGE_REF` / `INFERENCE_SGLANG_*`。
- 经现有 `ani-console` nginx `/api/` 打生产 Gateway：两者都缺 → `400 INVALID_ARGUMENT`；未 pin 的 `image_ref` → `422 IMAGE_UNAVAILABLE`；digest-pinned `image_ref` → `202`，响应冻结同一 digest，Deployment 使用该镜像，完成 CPU vLLM create/`running`/stop/start/delete。
- `ANI_AUTH_MODE` 保持 `auth_service`。不部署第二条 Gateway。未删已有租户 Namespace 和 `vllm-model` PVC。GPU/LWS 仍 skip。
- 未改 OpenAPI。不得标记 runtime ready。

## Design Decisions

- 产品入口仍是现网 Gateway；镜像解析在 Gateway，运行镜像只来自创建请求。
- 本刀 live 走手填 digest `image_ref`。仓库 `image_id` 解析在 Gateway 单测覆盖，本集群租户 Harbor 未作为本刀 live 目标。
- 失败时 runner 把 `ani-gateway` / `inference-service` 镜像滚回滚动前 tag；本次 live 成功，保持 C30 镜像。

## 验证证据

```text
cd /root/kubercon/ANI/repo
python3 scripts/run_inference_create_image_live.py --kubeconfig /root/.kube/config
PATH=/tmp/ani-pybin:$PATH make validate-inference-create-image-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

evidence：`development-records/live-evidence/inference-create-image-live-20260818.json`
gate：`deploy/real-k8s-lab/inference-create-image-live-gate.yaml`（`status: live`）

## 明确未完成

- 仓库 `image_id` 选择路径的集群 live。
- GPU device-plugin runtime live 与跨节点 LWS runtime live。
- 不得把本批次标为 runtime ready。
