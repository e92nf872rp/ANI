# GPU-OCCUPANCY-CARD-COUNT-A — occupancy 物理卡/逻辑卡口径修复

完成日期：2026-09-24
对应分支：`hotfix/gpu-occupancy-scope`（承接 GPU-OCCUPANCY-SCOPE-A，同分支第二批次）
验证结果：go build + go test（pkg、ani-gateway 全量）通过；`validate_openapi_spec`、SDK/docs 生成幂等（`validate_generated_idempotence`）、`validate_component_imports`、`validate_inference_legacy_control_plane`、`validate_gateway_authz_drift`（no drift）、`validate_core_gateway_authz_routes`（324 路由 / 0 error）、`validate_doc_entrypoints`、`git diff --check` 通过；live 验证 PASS（2026-09-24，ani-test2，gateway 镜像 `dev-20260924-gpu-card-count`）

## 背景

用户报障 BOSS「GPU 资源池态势」页统计卡「物理卡/逻辑卡」数据不对（接口返回 `physical_card_count=24`、`logical_card_count=96`）。BOSS 该统计卡的数据源是 `GET /api/v1/gpu-inventory/occupancy`（`repo/design/gpu-pool-status-frontend-integration.md` §1 映射）。

## 集群真实拓扑（ground truth）

| 节点 | 物理卡 | 每卡切分 | 逻辑卡（切片） |
|---|---|---|---|
| dev-phys-02 | 2 | 4（quarter，4914MiB/片） | 8 |
| dev-phys-03 | 2 | 4 | 8 |
| kubercloud | 2 | 4 | 8 |
| **合计** | **6** | — | **24** |

证据：每节点 `nvidia.com/gpu.count=2`（NVIDIA device plugin label）、`volcano.sh/node-vgpu-register` 注解每节点 2 段（每段一张物理卡）、切分前 `capacity nvidia.com/gpu=2`、切分后 `volcano.sh/vgpu-number=8`。

## 根因

`gpuNodeClassesFromKubernetesNodeList` 为 vGPU 节点生成的**设备记录是切片粒度**（每张物理卡 × 每个切片一条记录，记录携带 `Shares=每卡切分数`）。而 `gpuOccupancyFromNodes` 的统计假设「每条设备记录即一张物理卡」：

```go
response.PhysicalCardCount++        // 每条切片记录 +1 → 24（实为切片数）
response.LogicalCardCount += shares // 每条切片记录 +4 → 24×4=96（切片数×每卡切分数，双重计数）
```

该假设对整卡节点成立（1 记录=1 卡），对 vGPU 节点不成立。契约 `v1.yaml` 中 `physical_card_count` 的 description「与设备记录数同口径」把错误口径固化。GPU-POOL-SURFACE-A 当时的 live 验证只核对「字段出现且值=24/96」，把错误值当成了预期，未发现语义错误。

正确口径（对齐 gap-plan 设计意图「物理卡去重、逻辑卡为 vGPU 份数合计」）：

- **物理卡** = 每节点去重物理卡数 = register 注解段数（vGPU 节点）/ 设备记录数（整卡节点）
- **逻辑卡** = 整卡数 + vGPU 切片数合计 = **设备记录总数**（每条记录即一个可调度单元，恒等于 `total`）

## 实现了什么

1. **`ports.GPUNodeClass` 新增 `PhysicalCards int`**（节点级派生物理卡数）：由 adapter 派生，vGPU 注解路径 = 注解段数，整卡路径 = 设备记录数；0 表示未提供，消费方回退按设备记录数计（整卡语义）。结构体注释明确「vGPU 节点的 Devices 是切片粒度，不能用 len(Devices) 推物理卡数」。
2. **adapter 两条路径派生**（`kubernetes_gpu_inventory.go`）：
   - 注解路径：`PhysicalCards = len(cardShares)`
   - 无注解回退路径：整卡记录数 + nvidia.com/vgpu 记录数（无卡分组信息，按 1 张/条保守计）+ volcano 切片按 `nvidia.com/gpu.count` label 归组（label 缺失时假定单卡持有全部切片）
3. **router occupancy 统计修正**（`gpuOccupancyFromNodes`）：物理卡优先累加 `node.PhysicalCards`（未提供回退记录数）；逻辑卡改为每条设备记录 +1（不再按 `Shares` 累加）。
4. **契约描述修正**（contract-first）：`v1.yaml` 两处 description 改为正确口径。生成器不携带 schema 属性描述文本，SDK/docs 生成物经 `validate_generated_idempotence` 校验零漂移。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | `physical_card_count` / `logical_card_count` description 修正为正确口径 |
| `pkg/ports/gpu_inventory.go` | 修改 | `GPUNodeClass.PhysicalCards` 字段 + 语义注释 |
| `pkg/adapters/runtime/kubernetes_gpu_inventory.go` | 修改 | 两条设备生成路径派生 `PhysicalCards` |
| `pkg/adapters/runtime/kubernetes_gpu_inventory_test.go` | 修改 | 新增 `TestListNodeClassesDerivesPhysicalCards`（vGPU 注解 / 整卡 / gpu.count 回退三路径） |
| `services/ani-gateway/internal/router/gpu_inventory_resources.go` | 修改 | occupancy 物理卡/逻辑卡统计口径修正 |
| `services/ani-gateway/internal/router/gpu_inventory_resources_test.go` | 修改 | 新增 `TestGPUOccupancyPhysicalAndLogicalCardCounts`（3×vGPU[2卡×4切片]+1×整卡 → 8/26，错误实现会得 26/98） |

**响应 schema 形状零变更**（字段名/类型/required 不变，仅 description 语义修正），无 DB 迁移。

## 完工标准达成

- [x] go build + go test（pkg / ani-gateway 全量；pkg 仅既有 Windows symlink 用例失败，与本批次无关）
- [x] validate_openapi_spec / SDK+docs 生成幂等 / validate_component_imports / validate_inference_legacy_control_plane / validate_gateway_authz_drift / validate_core_gateway_authz_routes / validate_doc_entrypoints / git diff --check
- [x] live 验证 PASS（2026-09-24，ani-test2，镜像 `dev-20260924-gpu-card-count`）

## Live 验证结果（2026-09-24，ani-test2 10.10.1.66:30083）

| 检查项 | 修复前 | 修复后 |
|---|---|---|
| `physical_card_count` | 24（切片数） | **6**（2 卡 × 3 节点） |
| `logical_card_count` | 96（切片数×4） | **24**（切片合计） |
| `total` / `vgpu_count` | 24 / 24 | 24 / 24（不变） |
| `in_use` / `available` | 7 / 17 | 7 / 17（不变，GPU-OCCUPANCY-SCOPE-A 口径保持） |
| `GET /platform/capacity` | gpu_free=17 | gpu_free=17（不变） |

经 BOSS 前端 30087 代理链路复核一致。部署插曲：构建脚本 tag 替换失配导致新产物一度覆盖旧 tag `dev-20260923-gpu-occupancy-scope`（无任何环境在跑该 tag，无实际影响），已按约定立即补打正确 tag `dev-20260924-gpu-card-count` 推送并部署。

## 已知边界

- 混合切分节点（同节点既有整卡又有 vGPU 卡）：注解路径天然正确（逐段累计）；无注解回退路径对 nvidia.com/vgpu 记录按 1 卡/条保守计，无法还原卡分组（该路径仅适用于未部署 volcano vgpu 插件的集群）。
- `logical_card_count` 恒等于 `total`——语义即「全部可调度单元数」，保留独立字段是为 BOSS 统计卡的展示语义稳定。
- ani-system 未部署本批次（该环境已被 metering 改动线覆盖，等待 PR #184 合入后从 main 统一构建）。
- 本地 Windows `make test-go` 因 Makefile Unix 风格 env 前缀不可执行（预存问题），以等效环境变量手动执行同列表测试。
