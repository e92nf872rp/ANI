# INSTANCE-ORPHAN-GPU-FILTER-A：实例列表孤儿探测仅收 GPU Deployment

> 状态：已完成（live 验证通过，10.10.1.66 rollout 镜像 `dev-20260907-orphan-a`）
> 批次类型：hotfix（修复 `?kind=gpu_container` 列表混入非 GPU 实例）
> 分支：ani-hotfix（commit `cb78f06`）

## 1. 背景与问题现象

Console 查询 GPU 容器实例列表：

```
GET /instances?limit=10&kind=gpu_container
```

返回结果中混入明显不是 GPU 实例的记录（nginx、sandbox、纯容器测试实例等）。

## 2. 根因

Gateway 列表链路 = `store.List`（按 kind 正确过滤）+ `discoverOrphanDeployments`（孤儿合并）。问题出在孤儿合并：

- `discoverOrphanDeployments`（`services/ani-gateway/internal/router/instances.go`）枚举租户 namespace 内带 `ani.kubercloud.io/tenant-id` 标签、且不在内存 store 的 Deployment，**无条件硬编码 `Kind=gpu_container` 生成孤儿记录**；
- `obs.GPUCount > 0` 的判断只控制是否填充 `GPU` 状态字段，**不控制记录是否生成**；
- 于是 gateway 重启后（store 为空，所有实例都是"孤儿"），任何带租户标签的非 GPU Deployment（纯容器、sandbox、手工测试 Deployment 等）都被当成 `gpu_container` 回显；
- 列表端 `if kind != "" && orphan.Kind != kind { continue }` 的 kind 过滤形同虚设，因为 `orphan.Kind` 恒为 `gpu_container`。

附带发现：`observeOrphan` 的 GPU 数量探测只识别 `nvidia.com/gpu*`，不识别本集群实际使用的 Volcano vGPU 资源键 `volcano.sh/vgpu-number`——即使真实 GPU 孤儿也会报 `gpu=0`（与本仓库既定约定"孤儿过滤须同时识别 vgpu-number"不一致）。

## 3. 实现

`services/ani-gateway/internal/router/instances.go` 两处修改：

1. `discoverOrphanDeployments`：`obs.GPUCount <= 0` 时直接 `continue`，不再生成孤儿记录；`GPU` 状态字段填充随之无条件执行（能走到这里必然 GPUCount > 0）。
2. `observeOrphan`：GPU 资源键匹配从 `nvidia.com/gpu*` 扩展为 `nvidia.com/gpu* || volcano.sh/vgpu-number`。

**行为收紧说明**：孤儿探测的定位是"gateway 重启后 GPU 实例不丢"的兜底能力（对应批次约定：孤儿只允许 GPU 资源 Deployment 入列表）。收紧后非 GPU 未入库实例重启后不再出现在任何实例列表——这是与既定约定对齐，不是功能回退。

## 4. 测试

- 新增 `TestListOrphanDiscoverySkipsNonGPUDeployments`（`task_resources_test.go`）：fake K8s 集群放一个 `volcano.sh/vgpu-number=2` 的 GPU 孤儿和一个只有 cpu/memory 的普通 Deployment，断言 `GET /instances?kind=gpu_container` 只返回前者（name/kind 双断言）；
- 既有 `TestInstanceLifecycleOrphanRetryWritesTask` 的 fake 孤儿 Deployment 补上 `volcano.sh/vgpu-number:"1"` limits，匹配新行为；
- `go build ./...`（pkg + gateway）、`gofmt` 通过；gateway `internal/router` 全量测试通过；`pkg/adapters/runtime` 仅剩 2 个已知的 Windows symlink 环境必挂项（CI Linux 正常，与本批无关）。

## 5. Live 验证（10.10.1.66，rollout `dev-20260907-orphan-a`）

- 部署后 Pod 1/1 Running，`/healthz` 200；
- `GET /instances?kind=gpu_container`（tenant-a）：返回 29 条 = 26 条 `inst_*` store 记录 + 3 条孤儿（`d0e0edc6…`、`d2bff8a9…`、`pw-8359538a…`）；
- 集群侧核对 `ani-tenant-…0001` 命名空间 35 个 Deployment：18 个不带 GPU 资源（nginx、nginx-dongjm、sandbox、sandbox-dongjm、sb-d、test、test-instance、123、111、1111、precheck-d-*、probe-cephfs-attach、reobserve-live-test、test-mount-filesystem2 等）**全部不再回显**；
- 3 条孤儿记录经集群核对全部真实携带 `nvidia.com/gpu=1`，`volcano.sh/vgpu-number` 型 GPU Deployment（vgpu-dongjm-1 等）经 store 记录正常出现。

## 6. 已知边界与后续项

- 孤儿记录仍硬编码 `gpu_container`：真实 GPU Deployment 携带的资源键足以证明是 GPU 工作负载，但无法区分 vgpu/wholecard 之外的语义（如未来 inference 类 GPU 工作负载），届时需要从 Deployment 注解/标签回读真实 kind；
- 非 GPU 实例的"重启后不丢"能力当前依赖 store 穿透读（PG），孤儿探测不再兜底非 GPU——若未来需要，应按真实 kind 泛化孤儿探测而不是放开 GPU 过滤。
