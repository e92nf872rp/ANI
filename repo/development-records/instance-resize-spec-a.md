# INSTANCE-RESIZE-SPEC-A

> 日期：2026-09-02
> 范围：ANI Core / Gateway / instance lifecycle `resize` + GPU 规格（`spec_id`）变配
> 状态：local verified（代码 + 单测 + 架构门禁通过；未做真实集群 resize live gate）
> 分支：`hotfix/network-store-read`（ani-hotfix worktree）

## 问题背景

`resize` action 之前只给 Deployment template 打 `ani.kubercloud.io/restarted-at` 注解触发滚动重启，
**不修改任何资源字段**，是"假变配 / 纯重启"。契约层 `InstanceLifecycleRequest` 没有 `spec_id`，
无法换 GPU 规格；状态机对 running 也放行 resize（行为是纯重启）。计划文档：
`repo/design/instance-resize-spec-plan.md`（批次 `INSTANCE-RESIZE-SPEC-A`）。

## 设计决策（已确认）

- **D1 状态约束**：`resize` 仅允许在 **stopped** 状态执行；running 请求返回 409 要求先 stop。
- **D2 字段组合**：一次 `resize` 允许同时携带 `cpu`/`memory`/`spec_id`（至少一项），不再强制默认值。
- **执行路径**：经确认采用**方案B（targeted strategic-merge patch）**，不引入完整 render/apply
  （方案A 需放宽 provider apply gate + record→完整 WorkloadSpec 还原，重建失败风险高）。

## 实现

- `repo/api/openapi/v1.yaml`：`InstanceLifecycleRequest` 新增可选 `spec_id`（描述：换 GPU 规格、
  经 `/gpu-specs` 查询、与 cpu/memory 可同时传、仅 stopped 可执行）。v1 兼容（新增可选字段，非破坏）。
- `repo/pkg/ports/workload_runtime.go`：`WorkloadInstanceLifecycleRequest` 新增 `SpecID`。
- `repo/services/ani-gateway/internal/router/instances.go`：解析 `spec_id`；去掉 resize 默认
  `cpu=4/memory=8Gi` 的强制默认（改为透传实际值，由服务层校验至少一项）。
- `repo/pkg/adapters/runtime/planning.go`：`transition()` 的 Resize 分支要求 `state == stopped`，
  否则 409（D1）。
- `repo/pkg/adapters/runtime/instance_service.go`：
  - `validateLifecycleIntent`：resize 要求 cpu/memory/spec_id 至少一项（D2）。
  - `unexpectedLifecycleFields`：resize 放行 `spec_id`。
  - `resolveResizeGPUSpec`：调 `GPUSpecService.GetGPUSpec` 校验存在且 `Available`；与当前
    `record.Compute.SpecID` 相同 → 409；经 `GPUInventory.ListSpecAvailability` 校验租户可用性，
    不可用 → 409（避免滚动被 Volcano quota 卡死）。
  - `applyApprovedLifecycleSummary`：cpu/memory 仅在传入时更新；变配成功后回写
    `record.Compute.SpecID/GPUType/GPUShares/GPUMBPerShare`。
- `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor.go`（方案B）：
  - 注入 `*VolcanoResourceTranslator`（`WithKubernetesLifecycleTranslator`）。
  - `Apply` 对 `case Resize` 走 `applyResize`：VM 带 spec_id → `ErrUnsupported`；VM 仅 cpu/memory →
    保持 stop+start 重启；container/gpu_container 走 `buildResizePatch`。
  - `buildResizePatch`：带 spec_id 时 `translator.Translate(spec_id, queueName, count)` 生成
    strategic-merge patch，写入 `volcano.sh/vgpu-*` 资源、`schedulerName=volcano`、
    `scheduling.volcano.sh/queue-name` 注解、nodeSelector；切换 wholecard/vgpu 模式时清掉另一模式
    资源键（`volcanoVGPUResourceKeys` / `legacyGPUResourceKeys`），避免双请求；cpu/memory 单独
    patch 对应容器 resources。
- `repo/pkg/bootstrap/deps.go`：lifecycle executor 构造移到 `volcanoTranslator` 解析后，注入
  translator；instance service 注入 `WithInstanceGPUSpecService` / `WithInstanceGPUInventory`。

## 测试

- `planning_test.go`：resize 仅 stopped；running/其余态 → 409。
- `instance_service_test.go`：resize 字段组合（至少一项）、spec_id 校验失败路径、变配后
  record.Compute 更新断言、running resize 拒绝；既有用例改为先 stop 再 resize。
- `kubernetes_lifecycle_executor_test.go`：新增 5 个用例——无 spec_id 只 patch cpu/memory、
  带 spec_id 走 translate→patch（断言 `volcano.sh/vgpu-*`/schedulerName/queue/nodeSelector）、
  缺 translator 报错、VM 带 spec_id 不支持、VM 仅 cpu/memory 走 restart。
- `m1_e2e_profile_test.go`：terminal/exec ops 断言移到 lifecycle stop 之前。

## 验证命令

```bash
cd repo
go test -run "TestPlanning|TestLocalInstanceService|TestKubernetesLifecycleExecutor|TestM1E2E" ./pkg/adapters/runtime/...   # 全过
go test ./services/ani-gateway/internal/router/... ./pkg/bootstrap/...                                                  # 全过
make validate-architecture   # ✅ architecture guardrails valid
git diff --check             # 通过
python scripts/validate_yaml.py api/openapi/v1.yaml       # validated 1 YAML files
```

> 注：`make test` 的 test-go 在 Windows 本机因 Makefile 使用 POSIX 环境变量前缀
> （`GOCACHE=... go test`）无法直接执行；等价 `go test` 全包通过（仅存量
> `TestSandboxFileScripts*` symlink 测试在 Windows 必挂，与本次改动无关，CI/Linux 正常）。

## 能力边界

- 本批为 local verified：代码、单测、架构门禁通过；**未做真实集群 resize live gate**
  （stopped 实例变配到新规格、Volcano quota 不足拦截、vGPU 规格切换等场景待 REAL-K8S-LAB 验证）。
- 方案B 只 patch Volcano 调度片段与容器 GPU 资源，不整体重渲染 Deployment；
  env/ports/command 等既有意图保持不变。
- 不外推 GPU runtime ready / production ready。
