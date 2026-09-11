# GPU 资源池态势页面接口缺口分析与实施计划

> 状态：分析完成，决策已拍板（见 §4.1），可实施。
> 背景：BOSS「GPU 资源池态势」页面（原型见 `产品原型-9.08 v2/index.html`，已实现界面当前数据伪造）需要接入真实 Core API。本文档记录界面元素 ↔ 现有契约的逐项对照、缺口清单、可行性判断（含 Volcano 底层约束）和建议实施顺序，供后续开发会话直接使用。
> 契约真实来源：`repo/api/openapi/v1.yaml`。

---

## 1. 现有 Core API GPU 面（已覆盖部分）

| 端点 | 用途 |
|---|---|
| `GET /gpu-inventory` | 设备清单（node_name、gpu_type、gpu_index、memory_total_mb、gpu_mode、gpu_sharing_spec/policy、shares、tenant_id 只读回显、instance_id） |
| `GET /gpu-inventory/occupancy` | 占用统计（total / in_use / available / fault / vgpu_count / wholecard_count / by_gpu_type） |
| `PATCH /gpu-inventory/{device_id}` | 设备状态翻转，body 仅 `status: maintenance \| idle`（`GPUDeviceStatusUpdateRequest`，v1.yaml ~L4019） |
| `POST /gpu-inventory/gpu-partitions` | 集群级等分切分（仅 2/4/8，异步任务，Location → `GET /tasks/{task_id}`） |
| `GET/POST /gpu-specs`、`GET/DELETE /gpu-specs/{spec_id}`、`GET /gpu-specs/availability` | GPU 规格读写与可用性四态 |
| `GET/POST /gpu-scheduling/queues`、`GET/PATCH/DELETE /gpu-scheduling/queues/{queue_id}` | Volcano Queue 映射 |
| `PUT/GET /admin/tenants/{tenant_id}/reservations`、`GET /reservations/me` | **数量型** GPU 预留额度（allocated_gpu_count + GREATEST clamp） |
| `GET /quotas`、`PUT /admin/tenants/{tenant_id}/quota` 等 | 配额直接读写（无申请审批流） |
| `GET /metering/usage`、`GET /metering/usage/platform` | 计量（group_by=tenant/day/hour） |
| `POST /instances` 准入 | InsufficientGPU / QUOTA_EXCEEDED / RESERVED_INSUFFICIENT |

## 2. 界面元素逐项对照与缺口

| 界面元素 | 现有支撑 | 缺口判定 |
|---|---|---|
| 物理卡/逻辑卡 `8/0` | occupancy 只有设备记录数口径 | ⚠️ 半缺：需 `physical_card_count` / `logical_card_count`（物理卡去重、逻辑卡为 vGPU 份数合计） |
| 空闲未分配 | occupancy.available | ✓ |
| **已预留** | 无 | → 决策修订（D1）：预留 = 数量型，走既有 `PUT /admin/tenants/{tenant_id}/reservations`；页面展示预留额度而非预留卡 |
| 已占用 · 覆盖 N 个租户 | occupancy.in_use ✓ | ⚠️ 半缺：`tenant_count`（或 by_tenant） |
| 异常（维护 1 · 不可用 1） | occupancy 只有 fault | ⚠️ 半缺：`maintenance_count` 分项；「不可用」语义缺失（见 §4 enum 扩展） |
| 表格：节点/型号/显存/切分形态 | listGPUInventory | ✓ |
| 租户列（预留后显示租户） | tenant_id 只读回显 | → 决策修订（D1）：无设备级预留写入路径；租户列展示 in_use 占用归属 |
| 占用对象/**原因列**（驱动升级窗口、Xid 79） | instance_id ✓ | ✓ 已补：record 加 `reason` 字段，PATCH body 可带原因（D4） |
| 操作「切分」（行内） | gpu-partitions 在 | ⚠️ 接口是集群级，行内按钮语义需对齐（见 §4-5） |
| 操作「分配」 | 无 | → 决策修订（D1）：随预留缺口撤销，不做行内分配 |
| **联动事件流** | 无（仅 `GET /instances/{id}/events` 先例） | ✓ 已补：`GET /gpu-inventory/events` 设备事件查询端点 |

## 3. Volcano 底层硬约束（不可绕过，产品语义必须收敛）

1. **切分粒度是节点级**：`volcano-vgpu-node-config` 的 `devicesplitcount` / `gpuMemoryFactor` 对该节点全部 GPU 统一生效；同一节点上单卡独立切分（GPU-0 切 4 份、GPU-1 整卡）不支持。
2. **分区 one-way**：切分后回切整卡无自动路径（已验证，重启 device plugin 触发重注册是破坏性操作）。
3. **无设备级 pinning**：vGPU 调度器不支持「Pod 必须落在 node-X 的 PCI GPU-1」语义；整卡 `nvidia.com/gpu` 的 device plugin 同样不支持 Pod 侧选卡。**卡级硬隔离做不了**。
4. **不等分显存 / 任意份数 / 算力份额自定义**：插件模型只支持等分，vgpu-cores 由份数固定派生。突破只有换 MIG 路线（仅 A100/H100，另一套 device plugin 链路，与现有 vGPU 体系不兼容）。
5. 动态 `filterdevices.uuid/index` 屏蔽某张卡技术上可行，但需重启 device plugin（破坏性 + one-way），**不能做成产品接口**。

## 4. 缺口可行性与实施清单

### 能做（纯 Core 平台侧，成本低）

- **② reason 字段**：`GPUInventoryRecord` 加 `reason`（nullable）；`GPUDeviceStatusUpdateRequest` 加可选 `reason`；落 PG（不要污染节点 annotation）。支撑「原因列」。
- **③ 设备事件流**：新增 `GET /gpu-inventory/events`（照 `listInstanceEvents` 模式，v1.yaml ~L5430 先例）+ 事件表。事件源：PATCH 翻转、切分任务回调。**注意**：异步回调写库必须用 detached tenant ctx（`types.WithTenant` over `context.Background` + uuid 解析；禁止 hertz request ctx——pooled/reset 会 crash-loop，参考 gpu_partition_resources.go 的 `detachedTaskContext`）。
- **④ occupancy 补齐**：`GPUOccupancyStats` 加 `physical_card_count` / `logical_card_count` / `maintenance_count` / `tenant_count`。数据源现成（node-vgpu-register 注解 / shares 派生）。跨租户统计走 platform-only handler + RLS bypass（exact match 白名单，参考 `GET /quotas` 的先例，**不得**让租户 token 访问）。
- **⑤ 行内切分对齐**：交互调整——行内「切分」改为打开集群切分弹窗（2/4/8），提交既有 `POST /gpu-inventory/gpu-partitions` 并轮询 tasks/{id}；或按 gpu_type 预筛展示。**不改接口**。

### 有条件能做（核心缺口）

**① 设备预留/收回 + `reserved` 状态** —— **已撤销（2026-09-11 二次拍板，见 §4.1 D1/D2 修订）**：预留语义收敛为数量型（既有 `PUT /admin/tenants/{tenant_id}/reservations`），不做设备级分配。曾按「软预留」实现过 assign/revoke 接口 + `reserved` 状态 + 预留表，已全部回退。

#### 4.1 已拍板的决策（2026-09-11）

| # | 决策项 | 结论 |
|---|---|---|
| D1 | 预留强制语义 | **修订：预留 = 预留数量，不做设备级分配**。理由：Volcano 无设备级 pinning（§3-3），卡级预留只能是记账语义、无法调度强制，与既有数量型预留接口重叠且易误导。预留额度统一走 `PUT /admin/tenants/{tenant_id}/reservations`（allocated_gpu_count） |
| D2 | 冲突与回收规则 | **随 D1 修订作废**（设备级 assign/抢占/收回不再存在） |
| D3 | 「不可用」状态建模 | **status enum 加 `unavailable`**：fault=自动检测故障（Xid 等），unavailable=人工标记不可用；occupancy 分别计数（maintenance_count + unavailable_count） |
| D4 | reason 持久化 | **PG 平台账**：设备表加 reason 列，inventory 记录合并返回；不写节点 annotation |

补充口径约定（实施时直接采用）：
- `physical_card_count` = 按 node × 物理卡 index 去重计数（数据源 node-vgpu-register 注解）；`logical_card_count` = 整卡数 + vGPU 切片数合计。
- 状态翻转、切分任务事件统一写入设备事件流（§4-③ 端点），联动事件块从这里取数。

### 做不了

- 卡级硬隔离（见 §3-3）。
- 单卡独立切分（§3-1/2）。
- 不等分显存 / 任意份数 / 算力份额分配（§3-4）。

## 5. 建议实施顺序

1. ② reason（PATCH + record 扩展；按 D4 落 PG，按 D3 同步加 unavailable enum）
2. ④ occupancy 统计补齐
3. ③ 设备事件流
4. ~~① 预留接口~~（已撤销：预留 = 数量型，走既有 `PUT /admin/tenants/{tenant_id}/reservations`）
5. ⑤ 行内切分交互对齐（可并行）

## 6. 实施时必须遵守的仓库门禁（防踩坑清单）

1. **契约先行**：所有新端点/字段先改 `repo/api/openapi/v1.yaml`，再改 handler/SDK/生成物。
2. **新操作必须带注解**：`x-ani-handler` / `x-ani-owner` / `x-ani-auth-classification`（main 上 PR #145 曾因 `/admin/tenants*` 缺注解导致 `dp2_operation_registry.py --check` 失败）。
3. **新增错误码三同步**：`sdk-metadata.json`（+重新生成 SDK）、`docs/api` 静态文档、operation registry。
4. **authz 分类变更需再生成**：`zz_generated_target_operation_registry.go` / `zz_generated_core_policies.go`（`scripts/generate_gateway_authz.py`）；`target_operation_registry_test.go` 有冻结分类计数（public=11 / authenticated=22 / authorized=265），移动操作分类时必须更新。
5. GPU 资源双域（platform+tenant）走 legacy middleware `scopeAllowedForPath`（V2 authz 只支持单域，别把 GPU 路径挂 `x-ani-authz` 后又回滚——GPU→V1 回滚教训）。
6. 所有 POST/有副作用操作支持 `Idempotency-Key`（v1.yaml 既有惯例）。
7. 验收命令：`cd repo && make test && make validate-architecture && git diff --check`；涉及契约另跑 `make validate-gateway-authz`、`make validate-doc-entrypoints`（若动文档入口）。
8. 注意 main 上已有预存 drift（admin tenants 注解缺失、compatibility baseline 过期）会阻塞 2 个 gate——属于 main 分支契约修复范围，勿与本任务混淆。

## 7. 相关文件索引

- 契约：`repo/api/openapi/v1.yaml`（GPU 段 ~L9054-9366；schema 段 ~L3772-4048；预留段 ~L9560-9622）
- 原型：`产品原型-9.08 v2/index.html` + `gpu-link.js`（BOSS 池管理/Console 联动 mock）+ `gpu-coach.js`（演示流程）
- 已有先例：`GET /instances/{instance_id}/events`（事件端点模式，v1.yaml ~L5430）；`PUT /admin/tenants/{tenant_id}/reservations`（数量型预留 clamp 逻辑）
- 切分落地记录：`repo/development-records/`（GPU-PARTITION-A）+ live evidence `gpu-cluster-partition-live.json`
