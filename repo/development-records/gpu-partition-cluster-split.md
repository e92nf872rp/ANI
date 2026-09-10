# GPU-PARTITION-A~D — 集群 GPU 等分切分（BOSS 异步操作）

完成日期：2026-09-09（live gate 同日通过）
对应 Sprint：hotfix 分支受控批次（ani-hotfix，rebase 自最新 main）
验证结果：go test ./services/ani-gateway/... ./pkg/... 通过（pkg/adapters/runtime 仅有与本批次无关的 Windows 沙箱 symlink 既有失败），make validate-architecture 通过；live gate PASS（2026-09-09，ani-test2 隔离环境 10.10.1.66:30083，gateway 镜像 `test2-20260909-e`）

## 实现了什么

新增 BOSS 专属集群 GPU 等分切分能力：`POST /api/v1/gpu-inventory/gpu-partitions`（platform scope，幂等）对集群内全部空闲整卡节点做统一 2/4/8 等分。异步任务自动完成节点级 volcano-vgpu-node-config 的 devicesplitcount 更新、重启节点 device plugin pod 触发重新注册、按各节点实际显存打 vGPU 节点标签并清除整卡 gpu-spec 标签；忙碌节点（存在持有 GPU 资源 Pod）整体跳过并记录在任务 result。本操作不创建 GPUSpec，切分完成后由运营通过 POST /gpu-specs 单独建规格。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | 新增 createGPUPartition op + GPUPartitionCreateRequest/GPUPartitionTaskAccepted schema + NoIdleWholecardGPUs 422；GET /tasks 两 op 移除 `x-ani-authz` 走 legacy 双域（platform 轮询 gpu_partition 任务需要） |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 修改 | generate_gateway_authz.py 重跑同步（GPU ops + GET /tasks 两 op 均转 PolicySourceLegacy） |
| `services/ani-gateway/internal/middleware/auth.go` | 修改 | scopeAllowedForPath 精确匹配 `/api/v1/gpu-inventory/gpu-partitions` → platform-only（防 gpu-inventory 前缀规则放行 tenant） |
| `pkg/ports/gpu_partition.go` | 新增 | GPUPartitionPlanner port（Plan/Apply）+ 跳过原因/计划/结果结构 |
| `pkg/adapters/runtime/kubernetes_gpu_partition.go` | 新增 | K8s 实现：节点圈定、忙 Pod 判定（节点级）、ConfigMap patch、节点标签 patch、插件 pod 重启、注册收敛轮询 |
| `pkg/adapters/runtime/kubernetes_gpu_partition_test.go` | 新增 | roundTripFunc 分发表测试 |
| `services/ani-gateway/internal/router/gpu_partition_resources.go` | 新增 | handler：shares/幂等校验、同步 Plan（0 eligible→422）、建 running 任务、受控 goroutine 执行（120s 超时）、进度/终态回写、lazy-resume（running 超阈值幂等重入） |
| `services/ani-gateway/internal/router/task_resources.go` | 修改 | GET /tasks/{id} 对 running 的 gpu_partition 任务走 lazy-resume hook（防 gateway 重启悬挂） |
| `services/ani-gateway/internal/router/router.go` | 修改 | RegisterOptions.GPUPartitionPlanner + 路由注册 |
| `services/ani-gateway/main.go` | 修改 | gpuInventory 类型断言注入 planner（kubernetes_rest 生效，local profile 503） |
| `services/ani-gateway/internal/router/gpu_partition_resources_test.go` | 新增 | 覆盖 202/422/400/503/幂等重放不重复执行/失败终态/lazy-resume/新任务不误恢复 |
| `deploy/real-k8s-lab/sprint13-production-shaped-gateway-rbac.yaml` | 修改 | nodes 补 patch（切分需改节点标签） |
| `deploy/real-k8s-lab/gpu-cluster-partition-live-gate.yaml` | 新增 | 真实集群 live gate：任务完成+ConfigMap/标签/注册证据+忙碌节点跳过+tenant 403 |

## 完工标准达成

- [x] go test gateway router + pkg adapters（GPU partition 用例全通）
- [x] make validate-architecture 通过
- [x] handler 拒绝 tenant scope（middleware exact-match，platform-only；见 auth_test.go 用例）
- [x] 异步结果可轮询：GET /api/v1/tasks/{task_id} 返回 applied_nodes/skipped_nodes/failed_nodes
- [x] live gate PASS（2026-09-09，ani-test2 10.10.1.66:30083，镜像 `test2-20260909-e`）

## Live gate 执行结果（2026-09-09）

证据：`development-records/live-evidence/gpu-cluster-partition-live.json`（执行脚本 `step9d_live_gate.py`）。8 项检查全部 PASS：

| 检查项 | 结果 |
|---|---|
| platform/tenant 登录 | 200 |
| tenant 提交切分 | 403（platform-only 边界） |
| POST /gpu-inventory/gpu-partitions shares=4 | 202 + task_id |
| 任务终态 | completed / progress 100 |
| CM devicesplitcount | dev-phys-02 = 4 |
| 节点 relabel | gpu-mode=vgpu、gpu-sharing-policy=quarter、gpu-sharing-spec=NVIDIA-RTX-4090-12285MiB，整卡 gpu-spec 标签移除 |
| vGPU 注册 | volcano.sh/node-vgpu-register 注解写入（4 切分，4914MiB/份），device plugin pod 重启 Running |
| GET /tasks?task_type=gpu_partition | 200，platform token 可筛选 |

任务 result 实测验证忙碌节点跳过：dev-phys-03、kubercloud 因持有 GPU Pod（`node_busy`）跳过并附阻塞 Pod 名单，仅空闲的 dev-phys-02 进入 applied_nodes。

## Live gate 期间修复的缺陷

1. **apply goroutine 租户上下文 panic**：`runApply` 用裸 `context.Background()`，PG 版 `MetadataAsyncTaskStore.Update` → `WithTenantTx` → `SetDBTenant` → `FromContext` panic 导致 gateway 崩溃重启、任务悬挂 running。修复：新增 `detachedTaskContext`（uuid 解析租户 + `types.WithTenant` 构造脱离请求生命周期的后台 ctx；hertz 请求 ctx 会被池化复用，不可直接传 goroutine）。
2. **GET /tasks 两 op 对 platform 403**：V2 `x-ani-authz` 单域授权无法表达"BOSS 轮询集群任务 + tenant 轮询本租户任务"双域语义，与 GATEWAY-GPU-V1-ROLLBACK-A 同因；移除 `x-ani-authz` 走 legacy `scopeAllowedForPath` 双域 + rbac 角色准入，`zz_generated_core_policies.go` 同步重生成。
3. **step9d 脚本 curl URL 未加引号**：`?a=b&c=d` 的 `&` 被远端 shell 当后台符，`-H 'Authorization: ...'` 被切给第二条命令，列表请求裸奔 401；URL 加双引号修复。
4. **部署教训（非代码缺陷）**：同 tag 重推镜像（`test2-20260909-d` 二次构建）`kubectl set image` 是 spec 空操作，`rollout restart` 又因 IfNotPresent 复用节点缓存旧镜像，导致修复"部署后" panic 依旧；换新 tag `test2-20260909-e` 强制真实拉取后解决。

## 备注

- 设计与取舍（不采用 lazy-sync、节点级忙闲判定、幂等重入）见 `.trae/documents/gpu-partition-cluster-split-plan.md`。
- live gate 已通过并回填上文；证据在 `development-records/live-evidence/gpu-cluster-partition-live.json`。当前切分状态为 dev-phys-02 已 4 等分（vgpu），如需还原整卡需反向操作（另行批次）。
- 已知无关失败：pkg/adapters/runtime 沙箱文件脚本 symlink 测试在 Windows 本地挂（TestSandboxFileScriptsRejectSymlinks 等），与 GPU-PARTITION 无关。

## 追加：inventory `shares` 字段（2026-09-09，test2-20260909-f 实测）

BOSS 前端需要展示"每张卡切成几份"，但 inventory 原有字段只有语义化的 `gpu_sharing_policy`（half/quarter/eighth）。新增显式数值字段：

- 契约：`GPUInventoryRecord` 新增可选 `shares`（integer ≥1；整卡=1，vgpu=2/4/8）
- 取数链路：`ports.GPUDeviceClass` 加 `Shares`；K8s adapter 三条路径填充 — ① `volcano.sh/node-vgpu-register` 注解逐卡解析（新增 `parseVolcanoVGPUCardCounts`，与 sum 解析器保持"每卡之和=总数"不变量）；② 无注解回退：`volcano.sh/vgpu-number ÷ nvidia.com/gpu.count` 推导；③ 整卡=1。handler 侧 `Shares<=0` 防御性归 1（local profile 等实现无需改动）
- 语义口径：`shares` 是**每张物理卡**的份数；vgpu 节点按切片返回多条记录（卡数×份数条，每条携带所属卡的份数）
- 实测（ani-test2，platform token GET /gpu-inventory）：dev-phys-02（单卡4等分）→ 4 条 shares=4；dev-phys-03、kubercloud（双卡各4等分）→ 各 8 条 shares=4。附：kubercloud 因其上的推理 Pod 在 06:51~07:59 间被删除，07:59 前端切分任务 268a2ca9 将其与 dev-phys-02 一并切分（集群级"全部空闲整卡节点"语义，符合设计）
- 验证：新增 `TestParseVolcanoVGPUCardCounts` + `TestInventoryDeviceSharesFromAnnotation` + 整卡 Shares=1 断言；SDK/docs 生成物经重生成比对无内容变化（schema 字段不入产物）
