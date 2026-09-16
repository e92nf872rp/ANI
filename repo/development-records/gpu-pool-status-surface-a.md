# GPU-POOL-SURFACE-A — GPU 资源池状态台账缺口补齐（reason/unavailable、occupancy 口径、设备事件流）

完成日期：2026-09-11（live 验证同日通过，ani-test2）
对应 Sprint：hotfix 分支受控批次（ani-hotfix，rebase 自最新 main）
设计依据：`repo/design/gpu-pool-status-surface-gap-plan.md`（§4.1 记录 2026-09-11 二次拍板：预留=数量型，设备级预留回退）
验证结果：go build + go test（pkg、ani-gateway 12 模块）通过；make validate-architecture、validate-gateway-authz（323 路由 0 错误无漂移）、validate-doc-entrypoints、git diff --check 通过；live 验证 PASS（2026-09-11，ani-test2 隔离环境 10.10.1.66:30083，gateway 镜像 `test2-20260911-b`）

## 实现了什么

承接 BOSS「GPU 资源池态势」页面后端缺口（设计文档 D1~D4）：

1. **人工状态覆盖（D3/D4）**：`PATCH /api/v1/gpu-inventory/{device_id}`（platform-only，幂等键）对单卡翻转 `maintenance`（维护中）/ `unavailable`（不可用）/ `idle`（清除覆盖恢复空闲），可带 `reason`（≤512）；`fault` 为自动观测态不可人工设置（400）。覆盖态按 device_id 落 PG 平台账表，平台清单/占用视图合并返回（`GPUInventoryRecord.reason` 字段 + `status` enum 增 `unavailable`）。
2. **occupancy 统计补齐（D1）**：`GET /gpu-inventory/occupancy` 增加 `physical_card_count` / `logical_card_count`（整卡 + vGPU 切片合计）/ `maintenance_count` / `unavailable_count` / `tenant_count`（覆盖 in_use 租户数），全部 omitempty。
3. **设备事件流（D2 修订）**：`GET /api/v1/gpu-inventory/events`（platform-only，device_id/event_type/limit 过滤）返回台账事件，事件类型收敛为 `status_changed` + `partition_applied`；PATCH 成功后写入事件并携带 node_name/gpu_type/actor。
4. **数据层**：迁移 `20260911_001_gpu_device_surface.sql` 建 `gpu_device_overlays`（人工覆盖）与 `gpu_device_events`（事件流）两表，平台级 RLS（platform_bypass 单策略）+ `ani_app` 授权，atlas.sum 同步重算。

**范围拍板（2026-09-11，用户决策）**：设备级预留（assign/revoke/reserved 状态/reservations 表/抢占事件）实现后**整体回退**——预留语义收敛为数量型额度（既有 `PUT /admin/tenants/{tenant_id}/reservations`），不把特定卡绑定租户（Volcano 无 device-level pinning，卡级预留是误导性记账）。设计文档 §4.1 已记录修订。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | GPUInventoryRecord 加 `reason`、status enum 加 `unavailable`；GPUOccupancyStats 加 5 个统计字段；新增 `updateGPUDeviceStatus`（PATCH）与 `listGPUDeviceEvents`（GET /gpu-inventory/events）+ GPUDeviceStatusUpdateRequest/GPUDeviceEvent/GPUDeviceEventListResponse schema；两新 op 均 required Idempotency-Key |
| `deploy/migrations/20260911_001_gpu_device_surface.sql` | 新增 | gpu_device_overlays / gpu_device_events 两表 + RLS platform_bypass + ani_app 授权 |
| `deploy/migrations/atlas.sum` | 修改 | 重算（既有 50 条目逐一校验一致后追加新条目） |
| `pkg/ports/gpu_device_surface.go` | 新增 | `GPUDeviceSurfaceStore` port（Set/Delete/Get/List overlay + Append/List events） |
| `pkg/adapters/runtime/postgres_gpu_device_surface.go` | 新增 | PG 实现（WithPlatformTx、REQUIRES NEW 事务追加事件防主操作回滚连带） |
| `services/ani-gateway/internal/router/gpu_device_surface_resources.go` | 新增 | PATCH handler（状态白名单、reason 钳制、幂等键校验、事件在设备记录解析后写入带 node/gpu_type）+ events handler |
| `services/ani-gateway/internal/router/gpu_inventory_resources.go` | 修改 | occupancy 补齐 5 字段；清单/占用响应合并台账覆盖态（平台 scope 才加载 surface state） |
| `services/ani-gateway/internal/router/router.go` | 修改 | RegisterOptions.GPUDeviceSurfaceStore + 路由注册 |
| `services/ani-gateway/internal/middleware/auth.go` | 修改 | scopeAllowedForPath：`/gpu-inventory/events`、`/gpu-inventory/{uuid}`、`/gpu-inventory/{uuid}/assign` 精确收紧 platform-only（UUID 段识别，防 gpu-inventory 前缀双域规则放行 tenant） |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 修改 | generate_gateway_authz.py 重跑同步（新 op） |
| `sdks/core/*`、`sdks/services/*`、`docs/api/*` | 修改 | 四语言 SDK + sdk-metadata + 静态 API 文档重生成 |
| `design/gpu-pool-status-surface-gap-plan.md` | 修改 | §4.1 记录 D1 修订与 D2 作废（设备级预留回退） |
| `design/gpu-pool-status-frontend-integration.md` | 新增 | 前端对接文档（元素↔接口映射、零值字段省略、cursor 现状、验收清单） |

## 完工标准达成

- [x] go build + go test（pkg / ani-gateway，含 router + middleware GPU 用例）
- [x] make validate-architecture / validate-gateway-authz / validate-doc-entrypoints / git diff --check
- [x] 契约优先：v1.yaml 先行，SDK/docs/authz 生成物幂等重生成
- [x] tenant 隔离：PATCH/events platform-only（middleware 用例 + live 403 双重锁定）
- [x] live 验证 PASS（2026-09-11，ani-test2，镜像 `test2-20260911-b`）

## Live 验证结果（2026-09-11，ani-test2 10.10.1.66:30083）

迁移已在 ani-test2 PG 应用（表 owner `ani`，RLS/授权生效，插入/读取/清理冒烟通过）。业务验证（平台 root + 租户 fe-test 双 token，验证脚本按指南 §5 用完即删，未入库）：

| 检查项 | 结果 |
|---|---|
| 平台 `GET /gpu-inventory`（24 卡，dev-phys-02，vgpu quarter） | 200 |
| PATCH → maintenance + reason | 200，记录态/原因合并正确 |
| PATCH → idle 清除覆盖 | 200，maintenance_count 随之消失 |
| PATCH 非法状态 `fault` | 400 |
| `GET /gpu-inventory/events` | 200，status_changed + reason + actor + node_name/gpu_type |
| occupancy：physical_card_count=24 / logical_card_count=96（4 等分） | 200 |
| occupancy 动态：维护时 maintenance_count=1 出现、恢复后消失（0 值 omitempty） | 200 |
| 租户 PATCH 设备 / 读事件流 | 403 / 403 |
| 租户 occupancy 无平台专属字段 | 200 |

## 已知边界

- cursor 分页当前后端忽略（`next_cursor` 恒 null），limit 上限 200；当前集群规模一次拉全，契约不变，后续数据量大再实现真翻页
- occupancy 零值字段 omitempty 不出现在响应（前端对接文档已注明「缺字段=0」）
- 验证期间对 dev-phys-02 卡 0 的翻转均已恢复 idle；事件流留有验证事件记录，可在 PG 按需清理
- 本地 Windows `make test-go` 因 Makefile Unix 风格 env 前缀不可执行（预存问题），以等效环境变量手动执行同列表测试
