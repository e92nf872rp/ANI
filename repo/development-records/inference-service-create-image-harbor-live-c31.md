# INFERENCE-SERVICE-CREATE-IMAGE-HARBOR-LIVE-C31

> 日期：2026-08-18
> 状态：live passed（生产 `ani-system/ani-gateway` + `inference-service`，`ANI_AUTH_MODE=auth_service`）
> 前置：`INFERENCE-SERVICE-CREATE-IMAGE-LIVE-C30`、`INFERENCE-SERVICE-CREATE-IMAGE-C28`、`INFERENCE-SERVICE-LOCAL-MODEL-SOURCE-C23`

## 完成范围

- 把 CPU vLLM digest 推入当时为空的租户 Harbor 项目，镜像 ID 形如 `{tenant}/vllm-openai-cpu:c31`，并冻结同一 digest `image_ref`。
- 经现有 `ani-console` nginx `/api/` 打生产 Gateway：缺 `image_id`/`image_ref` → `400 INVALID_ARGUMENT`；未知 `image_id` → `422 IMAGE_UNAVAILABLE`；仓库 `image_id` → `202`，Deployment 使用 Harbor digest，完成 CPU vLLM create/`running`/stop/start/delete。
- 现网仍使用 C30 镜像 `create-image-c30-20260818`，未重建产品二进制。`ANI_AUTH_MODE` 保持 `auth_service`。不部署第二条 Gateway。未删已有租户 Namespace 和 `vllm-model` PVC。GPU/LWS 仍 skip。
- 租户 Harbor 项目本次为 public，因为 platform-workload 不挂 `imagePullSecrets`；私有 Harbor pull 未做。
- 未改 OpenAPI。不得标记 runtime ready。

## Design Decisions

- 本刀只补 Harbor `image_id` 数据与 live runner；产品解析仍在 Gateway `ListImages`，运行镜像只来自创建请求冻结的 digest。
- live 前刷新 inference-service 静态 `CORE_SERVICE_TOKEN` 并 rollout。取消态 delete 使用稳定幂等键，runner 会给 cancelled/failed delete 换新 key 后再走产品 DELETE。
- Harbor 项目公开是为了让节点拉镜像；不是私有仓库产品闭环。

## 验证证据

```text
cd /root/kubercon/ANI/repo
python3 scripts/run_inference_create_image_harbor_live.py --kubeconfig /root/.kube/config
PATH=/tmp/ani-pybin:$PATH make validate-inference-create-image-harbor-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

evidence：`development-records/live-evidence/inference-create-image-harbor-live-20260818.json`
gate：`deploy/real-k8s-lab/inference-create-image-harbor-live-gate.yaml`（`status: live`）

## 明确未完成

- 私有 Harbor 项目的节点 pull（`imagePullSecrets`）。
- GPU device-plugin runtime live 与跨节点 LWS runtime live；等切换集群后再做。
- 不得把本批次标为 runtime ready。
