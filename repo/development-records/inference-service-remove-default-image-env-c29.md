# INFERENCE-SERVICE-REMOVE-DEFAULT-IMAGE-ENV-C29

> 日期：2026-08-18
> 状态：local/logic verified
> 前置：`INFERENCE-SERVICE-CREATE-IMAGE-C28`
> 范围：删除进程默认引擎镜像环境变量与 catalog 占位 digest；不含 OpenAPI、不含 live

## 目标

运行镜像只来自创建请求的 `image_id` / `image_ref`。inference-service 不再提供 `INFERENCE_CPU_IMAGE_REF` / `INFERENCE_GPU_IMAGE_REF` / `INFERENCE_SGLANG_*` 进程默认镜像。

## 完成范围

- 从 `config.Load` 删除四条镜像 env 字段；残留 env 不再被读取。
- `main` 用 `modelsvc.DefaultProfiles()` 装配 catalog，不再 `ProfilesFromImages`。
- catalog 引擎底盘只保留 ID / Version / Runtime（vLLM 或 SGLang）；启动不再要求 digest-pinned 默认镜像。
- 删除 C21 清单和 runner 里的 `INFERENCE_CPU_IMAGE_REF`。
- 未改 OpenAPI。无新 live。不得标记 runtime ready。

## 验证证据

```text
cd /root/kubercon/ANI/repo/services/inference-service && go test ./... -count=1
  PASS
cd /root/kubercon/ANI/repo
make validate-architecture
  PASS
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
  PASS
python3 scripts/validate_yaml.py deploy/real-k8s-lab/inference-incluster-e2e.yaml
  PASS
git diff --check
  PASS
```

## 明确未完成

- 未重跑 C21/C22/C23 live；现网 create 仍须在请求里带 `image_id` 或 digest `image_ref`。
- GPU/LWS runtime live。
- 不得把本批次标为 runtime ready。
