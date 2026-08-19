# INFERENCE-SERVICE-GPU-LWS-VOLCANO-C20

> 日期：2026-08-17
> 状态：local/logic verified + PG integration passed + GPU/LWS live gate contract
> C25 已删除 lab Gateway harness 及本批次 runner；契约仍由 `make validate-inference-gpu-lws-volcano-live-gate` 校验。GPU/LWS runtime 复跑需另开批次走现网 `ani-gateway`。
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 6.2 / 10.2 / 10.3
> 前置：`INFERENCE-SERVICE-GPU-ADMISSION-LIVE-C16`、`INFERENCE-SERVICE-FAILURE-ROLLBACK-LIVE-C19`

## 完成范围

- Core `platform-workload-capabilities` 探测节点 `nvidia.com/gpu`、LeaderWorkerSet CRD 与 Volcano PodGroup CRD；无 Volcano 时 accelerator `available=false`。不探测、不绑定名为 `ani-inference` 的 Queue CR。
- 单节点 GPU Deployment 只写 `schedulerName=volcano`；CPU Deployment 不写 Volcano，避免破坏已过的 CPU live。
- `leader_worker` 渲染 LeaderWorkerSet + 一个 PodGroup + 只选 leader 的 ClusterIP；缺 LWS/Volcano 时 `422`，不得静默改 Deployment/CPU。
- inference-service 按 `placement_mode` + Core capabilities 选择 single_node 或 LWS；客户端仍不能填 `scheduler_name` / `volcano_queue` / `lws_size`。
- 真实 PostgreSQL integration 已在 lab 隔离 schema 执行：`CREATE ROLE`、RLS、幂等 create、lease。
- GPU/LWS runtime live gate 已落契约与 runner，`status: contract`。本集群已装 Volcano 1.15.0（scheduler/controller/admission Running，PodGroup CRD 存在，仅内置 `default`/`root` Queue，未 apply `ani-inference`）。C20 runner 复跑：`gang_scheduling_ready=true`、`volcano_crd=true`；仍无 LWS CRD、无 `nvidia.com/gpu` allocatable，GPU/LWS create 保持 422。不得标 live passed / GPU ready / runtime ready。
- 未改 OpenAPI，未滚动生产 Gateway，未触碰 `ani-vllm-cpu-smoke`，不实现 `/test`。

## Design Decisions

- P0 LWS execution plan 固定 `TP = count_per_replica`、`PP = 1`，1 个 leader + N-1 个 worker，每 Pod 1 GPU；`count_per_replica < 2` 的 multi_node 直接 `UNSUPPORTED_TOPOLOGY`。
- GPU 与 LWS 都要求 Volcano 就绪才把 spec 标 available / 接受 `leader_worker`；缺组件 fail-closed，不摘掉 `schedulerName`。
- Local profile 仍只支持 `single_node`；local 可创建 GPU 规格记录，但不探测真实 device-plugin。
- GPU/LWS runtime live 必须换到带卡且已装 LWS controller 的集群后再跑 runner；本集群只证明 Volcano PodGroup 探测与无 GPU 时 fail-closed。

## 验证证据

```text
cd /root/kubercon/ANI/repo
gofmt -w pkg/ports/platform_workload.go pkg/adapters/runtime/local_platform_workload.go \
  pkg/adapters/runtime/kubernetes_platform_workload.go \
  pkg/adapters/runtime/kubernetes_platform_workload_runtime.go \
  pkg/adapters/runtime/kubernetes_platform_workload_capabilities.go \
  services/inference-service/internal/runtime/topology.go \
  services/inference-service/internal/runtime/coresdk/adapter.go \
  services/inference-service/internal/engine/launch.go
GOWORK=off go test -C pkg ./adapters/runtime/ -count=1 -run 'PlatformWorkload|AcceleratorSpecs|RenderLeader|KubernetesPlatform|LocalPlatform'
GOWORK=off go test -C services/inference-service ./... -count=1
GOWORK=off go test -C services/ani-gateway ./internal/router -count=1 -run PlatformWorkload
PATH=/tmp/ani-pybin:$PATH make validate-inference-gpu-lws-volcano-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
python3 scripts/run_inference_control_plane_postgres.py --kubeconfig /root/.kube/config
python3 scripts/run_inference_gpu_lws_volcano_live_gate.py --kubeconfig /root/.kube/config
```

PG evidence：`development-records/live-evidence/inference-control-plane-postgres-20260817.json`
GPU/LWS fail-closed/skip evidence：`development-records/live-evidence/inference-gpu-lws-volcano-live-20260817.json`
GPU/LWS gate：`deploy/real-k8s-lab/inference-gpu-lws-volcano-live-gate.yaml`（`status: contract`，不是 GPU runtime live）

## 明确未完成

- 无 GPU device-plugin runtime live，无跨节点 LWS runtime live。
- 本集群 Volcano 已就绪；换到带 GPU 卡且已装 LWS controller 的集群后，另开批次走现网 `ani-gateway` 才能尝试 GPU/LWS runtime。
- 不得把 in-cluster Gateway 或推理产品链路标为 control-plane/runtime/GPU ready。
