# PRD: GPU 规格与配额管理

> 来源计划: `plan.md`（GPU 规格与配额管理 — 技术方案）
> 承接关系: 承接 PR #46，基于 #46 分支并行开发，#46 合入后 rebase 到 main
> 闭环定义: GPU 卡切分 → 分配到租户 → 用户使用（选规格创建实例）
> 决策归档: `repo/development-records/gpu-spec-quota-grilling-log.md`（仅供追溯，若冲突以本文件为准）

---

## 1. Introduction/Overview

ANI 平台需要一套完整的 GPU 规格与配额管理能力，将当前"自由填写 GPU 型号和数量"的创建模式升级为"选择预定义规格 + 配额管控"的闭环模式。

**当前痛点：**
- BOSS GPU 资源池页仅提供只读视图，无规格切分、配额分配、设备状态管理能力
- Console 创建 GPU 容器实例时 `model` 是自由字符串，无规格枚举，用户需要自己知道选什么卡
- 无配额管控机制，多租户之间可能互相抢占 GPU 资源
- 调度依赖默认调度器，无 Volcano queue 准入控制

**本 PRD 覆盖的完整闭环：**
1. **节点打标签**：平台管理员给节点打 GPU 标签（gpu-spec/gpu-mode/gpu-sharing-spec），inventory 可读
2. **分配到租户**：BOSS 设配额上限（resource_quota.total）+ 预留额度（resource_reservation_allocations.allocated_gpu_count）
3. **用户使用**：Console 选规格创建实例，TCC 配额预检 + Volcano 调度 + Confirm/Cancel 生效

**技术方案要点：**
- 规格目录以 K8s CRD（GPUSpec）持久化，对齐节点标签，供创建实例下拉选择 + Adapter 翻译 Volcano 资源
- vGPU 集群级等分由节点标签表达，不由 ANI 按设备逐张切分；`gpu_slices` 表废弃
- 配额三层量：配额上限（total）→ 预留额度（allocated_gpu_count）→ 占用（used + reserved），运行时三道闸防超卖
- 调度走 Volcano（schedulerName: volcano + queue annotation），ANI 不选具体设备，不占位切片
- 配额归 Core，走 Core REST handler → QuotaService port → PG；TCC 接口复用已有 pkg/ports/quota.go

---

## 2. Goals

- 建立 GPU 规格目录（GPUSpec CRD），对齐节点标签，支持整卡和 vGPU 两种模式
- BOSS 实现配额分配（设 total + 设 allocated_gpu_count）和预留管理，数据入 PG
- BOSS 实现规格目录 CRUD（对齐节点标签校验）和设备状态管理（标维护/恢复空闲）
- Console 创建实例改为选规格模式，按规格可用性四态标注/过滤，避免盲选后提交才报 409
- Console 展示租户配额和预留额度，用户可见可用余量
- 创建实例走 TCC 配额预检（三道闸）+ Volcano 调度 + reconciler Confirm/Cancel
- 删除实例走 Cancel + Release 双调释放配额，覆盖 pending/running/failed 三种删除前原态
- 配额功能开关 GPU_QUOTA_ENABLED 控制 TCC 全链路启停，false 时配额完全旁路
- provisioning 超时机制（默认 10 分钟），超时标 failed 并 Cancel 释放配额
- 废弃 HAMi 代码，统一用 volcano scheduler 和 volcano.sh/node-vgpu-register annotation
- 通过 `make test`、`make validate-architecture`、`make validate-services`、`git diff --check`

---

## 3. User Stories

### US-001: Core API 契约层 — 规格目录 + 配额 + 设备管理接口定义

**Description:** 作为开发者，我需要先定义 Core OpenAPI 契约，包含规格目录 CRUD、配额管理、预留管理、设备管理、规格可用性等 12 项新增/扩展接口，作为后续 port/adapter/handler/前端的契约边界。

**Acceptance Criteria:**
- [ ] `repo/api/openapi/v1.yaml` 新增 GPUSpec schema（含 node_affinity + volcano_resources 字段）
- [ ] 扩展 GPUNodeClass：新增 `gpu_mode`/`gpu_spec`/`gpu_sharing_spec`/`gpu_sharing_policy` 可选字段（从节点标签读）
- [ ] 扩展 CreateGPUContainerInstanceConfig：新增 `spec_id` 可选字段（传 spec_id 走规格选配 Volcano 调度）
- [ ] 扩展 CreateInstanceRequest：新增 `network_config` 可选字段（预留）
- [ ] 扩展 GPUOccupancyStats：新增 `vgpu_count`/`wholecard_count` 计数（可选）
- [ ] 新增配额端点：`GET /quotas/me`（Console 自查）、`GET /quotas`（BOSS 分页列表）
- [ ] 新增规格端点：`GET /gpu-specs`、`POST /gpu-specs`、`GET /gpu-specs/{spec_id}`、`DELETE /gpu-specs/{spec_id}`
- [ ] 新增设备端点：`PATCH /gpu-inventory/{device_id}`（body `{status: maintenance|idle}`）
- [ ] 新增规格可用性端点：`GET /gpu-specs/availability`（返回 quota_remaining + has_matching_nodes + has_idle_devices + device_idle_count + available_count + status 四态）
- [ ] 新增预留端点：`PUT /admin/tenants/{tenant_id}/reservations`、`GET /admin/tenants/{tenant_id}/reservations`、`GET /reservations/me`
- [ ] 扩展 GPUSchedulingQueue schema：新增 `status` 字段（含 `allocated` map 从 Volcano Queue CRD `status.allocated` 读取 + `state` 枚举 open/closed/unknown 归一自 Volcano Queue CRD `status.state`）
- [ ] 所有 POST/PUT/PATCH/DELETE 支持 `idempotency_key`
- [ ] `python scripts/validate_yaml.py api/openapi/v1.yaml` 通过
- [ ] `make validate-spec-split` 通过
- [ ] `make validate-core-api-compatibility` 通过
- [ ] `make gen-core-api` 重新生成 SDK / TS schema / API docs
- [ ] `git diff --check` 通过

### US-002: Ports 层 — GPUSpecStore + WorkloadInstanceStoreTx + GPUInventory 扩展

**Description:** 作为开发者，我需要定义规格目录 port 接口、扩展工作负载实例 store 接口和 GPU 库存接口，作为 adapter 实现的契约边界。

**Acceptance Criteria:**
- [ ] 新增 `pkg/ports/gpu_spec.go`，定义 `GPUSpecStore` 接口（List/Get/Create/Delete）
- [ ] 扩展 `pkg/ports/workload_runtime.go`，新增 `WorkloadInstanceStoreTx` 小接口（仅 `UpsertStatusTx`），不破坏现有 6 个 mock
- [ ] 扩展 `pkg/ports/gpu_inventory.go`，新增 `ListSpecAvailability(tenant_id)` 方法签名（按租户配额余量 + 节点标签匹配查询）
- [ ] 复用已有 `pkg/ports/quota.go` 的 `QuotaService`/`QuotaStoreService` 接口，不新建
- [ ] `go build ./pkg/ports/...` 通过
- [ ] `go test ./pkg/ports/...` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

### US-003: Adapters 层 — GPUSpec CRD + Volcano 翻译 + Inventory 扩展 + Reconciler 改造

**Description:** 作为开发者，我需要实现 GPUSpec CRD adapter、Volcano 资源翻译 Adapter、扩展 GPU inventory 和 reconciler，完成规格目录持久化、资源翻译、配额 Confirm/Cancel 等核心能力。

**Acceptance Criteria:**
- [ ] 新增 `deploy/manifests/gpu-spec-a/00-gpuspec-crd.yaml`，定义 GPUSpec CRD（集群级，含 node_affinity + volcano_resources）
- [ ] 新增 `pkg/adapters/runtime/crd_gpu_spec_store.go`，实现 `GPUSpecStore` 接口（K8s CRD 读写，幂等用 idempotency_key 作 label）
- [ ] 新增 `pkg/adapters/runtime/volcano_resource_translator.go`，实现 spec_id → nodeSelector + schedulerName: volcano + Pod 资源请求 + queue annotation 翻译
- [ ] 扩展 `kubernetes_gpu_inventory.go`：实现 `ListSpecAvailability`（查 K8s 节点标签匹配 + 解析 volcano.sh/node-vgpu-register / hami.io/node-nvidia-register annotation 得设备总量 + 查已调度 Pod 资源请求求差得物理空闲数 + 叠加配额余量）
- [ ] 扩展 `kubernetes_gpu_inventory.go`：GPUNodeClass 新增 gpu_mode/gpu_spec/gpu_sharing_spec/gpu_sharing_policy 字段从节点标签派生
- [ ] 扩展 `kubernetes_gpu_inventory.go`：新增 `parseVolcanoVGPUAnnotation` 替代旧 `parseHAMIAnnotation`；删除 `isHAMINode`、`kubernetesHAMISchedulerName` 常量、`hami-scheduler` 分支
- [ ] 扩展 `reconcile_controller.go`：Confirm/Cancel 同事务（监听 Volcano 状态）+ `markProvisioningFailed`（超时机制）+ `cancelQuotaAndFinalize` 公共方法 + 删除分支 Cancel+Release 双调
- [ ] 扩展 `reconcile_controller.go`：新增 Pod Events 读取能力 + 节点 idle 查询，区分排队中（queued）vs 调度失败（failed）
- [ ] 扩展 `reconcile_controller.go`：对账循环（遍历 K8s 中该租户非终态 GPU Pod 计算实际 used，与 PG 比对，本期只打 log 告警不修正）
- [ ] 扩展 `instance_orchestrator.go`：删除时调 Quota.Release（已有接口）+ Apply 失败保留 DB 行 UpsertStatusTx(failed)
- [ ] 扩展 `demo_instances.go`：API 层 TryManyTx 预占（同事务）+ Volcano 资源翻译 + Apply 异常分支 Cancel
- [ ] 新增 `pkg/adapters/runtime/outbox_writer.go`，定义 `outboxWriter` 小接口 + mock 实现
- [ ] 扩展 `instance_store.go`：新增 `WorkloadInstanceStoreTx` 实现（UpsertStatusTx）
- [ ] 扩展 `volcano_queue_store.go`：volcanoQueueCRD 新增 Status 字段（含 Allocated + State）+ crdToQueue 映射 allocated 与 state（Open→open / Closed→closed / 空/其他→unknown 大小写归一）到 GPUSchedulingQueue.Status
- [ ] 扩展 `bootstrap/server.go`：新增 `GPUQuotaEnabled` + `GPU_QUOTA_ENABLED` + `PROVISIONING_TIMEOUT_MIN` 配置
- [ ] 扩展 `bootstrap/deps.go`：注入新 store + 已有 PostgresQuota
- [ ] 新增 `migrations/quota_tx_ids.sql`：workload_instances 新增 `quota_tx_ids JSONB` 列
- [ ] 新增 `migrations/resource_reservation_allocations.sql`：BOSS 预留账本表（tenant_id → allocated_gpu_count，单维度不分 spec）
- [ ] `go build ./pkg/... ./services/ani-gateway/...` 通过
- [ ] `go test ./pkg/adapters/runtime/ -run "TestGPUSpec|TestVolcanoTranslator|TestReconcile|TestUpsertStatusTx" -count=1` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

### US-004: Gateway Handler 层 — 规格目录 + 设备管理 + 创建实例路由

**Description:** 作为开发者，我需要实现规格目录 4 端点 handler、设备管理 PATCH handler 和创建实例 handler 的 spec_id 接入，完成 Core REST API 的业务逻辑层。

**Acceptance Criteria:**
- [ ] 新增 `internal/router/gpu_spec.go`，实现规格目录 4 端点 handler（GET/POST/GET-by-id/DELETE）
- [ ] 扩展 `internal/router/gpu_inventory.go`，实现设备管理 PATCH handler（标维护/恢复空闲）+ inventory 返回 gpu_mode/gpu_spec 等扩展字段
- [ ] 扩展 `internal/router/router.go`，注册新路由
- [ ] 扩展 `internal/router/instance.go`，创建实例 handler 接 spec_id（传 spec_id 走规格模式，不传走旧模式）+ network_config 默认值兜底
- [ ] `go build ./services/ani-gateway/...` 通过
- [ ] `go test ./services/ani-gateway/...` 通过
- [ ] `make validate-architecture` 通过
- [ ] `make validate-mock-a` 通过
- [ ] `git diff --check` 通过

### US-005: BOSS 前端 — GPU 资源池改版 + 规格管理 + 配额/预留分配

**Description:** 作为平台运维管理员，我可以在 BOSS 运营平台管理 GPU 规格目录、分配配额和预留额度给租户、查看设备状态，这样租户用户就能在 Console 选规格创建实例。

**Acceptance Criteria:**
- [ ] 引入 coreApi（取租户列表 + 配额管理调用），`boss/src/api/client.ts` 新增 quota 相关调用
- [ ] GPU 资源池页改版（`gpu-pool.tsx`）：设备列表 + 节点标签只读展示（gpu_mode/gpu_spec/gpu_sharing_spec/gpu_sharing_policy）
- [ ] KPI 扩展：整卡/vGPU 节点数（不只是总量/已分配/空闲/异常）
- [ ] 配额分配弹框（`gpu-pool-quota-dialog.tsx`，NEW）：走 Core API `PUT /admin/tenants/{tenant_id}/quota` 设 total
- [ ] 预留分配弹框（`gpu-pool-reservation-dialog.tsx`，NEW）：走 Core API `PUT /admin/tenants/{tenant_id}/reservations` 设 allocated_gpu_count（<= total，单维度不分 spec）
- [ ] 规格管理页（`gpu-specs.tsx`，NEW）：规格目录 CRUD，创建时校验 gpu_type 对齐节点标签
- [ ] `cd repo/frontends/boss && npx tsc --noEmit` 通过
- [ ] `cd repo/frontends/boss && npx vite build` 通过
- [ ] Verify in browser using an available browser automation tool or record manual verification steps

### US-006: Console 前端 — 选规格创建实例 + 配额/预留展示 + 队列配置

**Description:** 作为租户用户，我可以在 Console 选规格创建 GPU 容器实例，看到自己的配额和预留额度，选规格时按可用性四态标注/过滤，避免盲选后提交才报错。

**Acceptance Criteria:**
- [ ] 创建 Dialog 改选规格模式（`create-dialog.tsx`，MODIFY）：移除旧字段输入项，新增规格下拉
- [ ] 规格下拉调用 `GET /gpu-specs/availability`（非裸 `GET /gpu-specs`），按返回 status 四态标注/过滤：
  - `available`（available_count > 0）→ 可选，展示"剩余 N"
  - `full`（quota_remaining = 0）→ 置灰标注"配额已满"
  - `device_full`（quota_remaining > 0 && !has_idle_devices）→ 置灰标注"设备已满，暂无空闲"
  - `unavailable`（!has_matching_nodes）→ 置灰标注"暂无匹配节点"
- [ ] 前端选规格后实时重算：`new_quota_remaining = quota_remaining - sum(已选规格的 gpu_count)`，每规格重算 `available_count` 并刷新下拉，避免跨规格共享配额超选
- [ ] 传 `spec_id` 创建实例（走规格模式 Volcano 调度）
- [ ] 配额展示（`gpu-containers/index.tsx`，MODIFY）：调用 `GET /quotas/me`（Core API）展示本租户配额
- [ ] 预留展示（`gpu-containers/index.tsx`，MODIFY）：调用 `GET /reservations/me`（Core API）展示本租户预留额度（allocated）+ 可用余量（allocated - used）
- [ ] 队列配置页（`gpu-scheduling-queues.tsx`，NEW）：租户/项目队列 CRUD（新建/编辑/删除），管理员写、成员读，平台默认队列只读，展示 Queue status（allocated + state 徽标，Open=绿色/Closed=灰色/Unknown=默认）
- [ ] 创建表单队列下拉（`create-dialog.tsx`，MODIFY）：新增调度队列下拉（GET /gpu-scheduling/queues），单副本实例也必须关联队列
- [ ] `cd repo/frontends/console && npx tsc --noEmit` 通过
- [ ] `cd repo/frontends/console && npx vite build` 通过
- [ ] Verify in browser using an available browser automation tool or record manual verification steps

### US-007: 集成验收 — 闭环验证 + 文档更新

**Description:** 作为开发者，我需要完成集成验收，确保规格目录 → 配额分配 → 选规格创建实例的完整闭环可用，并更新文档记录。

**Acceptance Criteria:**
- [ ] 新增 `repo/development-records/gpu-spec-quota-batch.md`，记录批次实现与验证细节
- [ ] 更新 `repo/CURRENT-SPRINT.md`，追加 GPU 规格配额条目
- [ ] 更新 `repo/development-records/README.md`，追加批次索引
- [ ] `make test` 通过
- [ ] `make validate-architecture` 通过
- [ ] `make validate-doc-entrypoints` 通过
- [ ] `git diff --check` 通过
- [ ] 闭环验证：节点打标签 → BOSS 分配配额/预留 → Console 选规格创建实例 → TCC 预检 + Volcano 调度 + Confirm/Cancel 生效

---

## 4. Functional Requirements

- FR-1: 系统必须支持 GPUSpec CRD 的 CRUD 操作，spec_id 格式为 `{gpu_type}-{mem}-{shares}`，创建时校验 gpu_type 存在于集群节点标签中
- FR-2: 系统必须支持整卡规格（shares=1, gpu_mode=wholecard）和 vGPU 规格（shares>1, gpu_mode=vgpu），统一模型
- FR-3: 系统必须在删除 GPUSpec 前查 workload_instances 是否有引用，有引用时禁止删除
- FR-4: 系统必须从 K8s 节点标签读取 gpu_mode/gpu_spec/gpu_sharing_spec/gpu_sharing_policy，派生到 GPUNodeClass 字段（只读，不写入 PG）
- FR-5: 系统必须支持 `PATCH /gpu-inventory/{device_id}` 翻转设备状态（maintenance/idle），改 K8s 节点标签/cordoned 状态，不碰配额
- FR-6: 系统必须支持 BOSS 设配额上限：`PUT /admin/tenants/{tenant_id}/quota` 设 resource_quota.total（gpu_count 维度）
- FR-7: 系统必须支持 BOSS 设预留额度：`PUT /admin/tenants/{tenant_id}/reservations` 设 resource_reservation_allocations.allocated_gpu_count（单维度不分 spec，<= total）
- FR-8: 系统必须支持 BOSS 下调 total 时用 `GREATEST(total, used+reserved)` clamp，返回 tightened=true，不拒绝，不实现延迟生效机制
- FR-9: 系统必须支持 BOSS 下调 allocated_gpu_count 时用 `GREATEST(allocated_gpu_count, used+reserved)` clamp，返回 tightened=true，不拒绝，不杀已有实例
- FR-10: 系统必须支持 Console 查自己配额：`GET /quotas/me`（tenant scope）
- FR-11: 系统必须支持 Console 查自己预留：`GET /reservations/me`（tenant scope）
- FR-12: 系统必须支持规格可用性查询 `GET /gpu-specs/availability`：返回 quota_remaining（allocated_gpu_count - used - reserved）+ 每规格 has_matching_nodes + has_idle_devices + device_idle_count + available_count（min(quota_remaining, device_idle_count)）+ status 四态（available/full/device_full/unavailable）
- FR-13: 系统计算 device_idle_count 时不能读 allocatable（实测 vgpu-number 不随 Pod 占用递减），必须解析节点 annotation 得设备总量 + 查已调度 Pod 资源请求求差
- FR-14: 系统必须在创建实例时传 spec_id 走规格模式：Adapter 查 GPUSpec CRD 取 node_affinity + volcano_resources，翻译为 nodeSelector + schedulerName: volcano + Pod 资源请求 + queue annotation
- FR-15: 系统必须在创建实例时执行三道闸校验：闸 1 配额上限（used + reserved + request <= total，失败 QUOTA_EXCEEDED）；闸 2 预留空闲（allocated_gpu_count - used - reserved >= request，失败 RESERVED_INSUFFICIENT）；闸 3 TCC Try 原子预占（reserved += request，TCC SQL 只校验 <= total）
- FR-16: 系统必须在 TryManyTx + InsertPendingTx 同事务原子提交（含 quota_tx_ids JSONB 列），失败整体回滚无补偿
- FR-17: 系统必须在 reconciler 监听 Volcano 状态：pending→running 触发 Confirm（reserved→used）；pending→failed 触发 Cancel（释放 reserved）；running→failed 触发 Release（释放 used）
- FR-18: 系统必须在删除实例时 reconciler 走 Cancel + Release 双调（不依赖原态判定），覆盖 pending/running/failed 三种删除前原态
- FR-19: 系统必须实现 provisioning 超时机制：默认 10 分钟，PROVISIONING_TIMEOUT_MIN 环境变量，超时标 failed 并 Cancel 释放配额
- FR-20: 系统必须实现 GPU_QUOTA_ENABLED 开关：false 时 TryManyTx/Confirm/Cancel/Release 全跳过（配额完全旁路），true 时强制生效
- FR-21: 系统必须在 reconciler 区分排队中（queued）vs 调度失败（failed）：Pod Pending 时查 Events 文本匹配 + 节点 idle 数值校验双判据
- FR-22: 系统必须在 reconciler 做对账循环：遍历 K8s 中该租户非终态 GPU Pod 计算实际 used，与 PG 比对，本期只打 log 告警不修正
- FR-23: 系统必须废弃 HAMi 代码：删除 kubernetesHAMISchedulerName 常量、isHAMINode 函数、parseHAMIAnnotation、hami-scheduler 分支，统一用 volcano scheduler 和 volcano.sh/node-vgpu-register annotation
- FR-24: 系统必须扩展 GPUSchedulingQueue schema：新增 status 字段（含 allocated map 从 Volcano Queue CRD status.allocated 读取 + state 枚举 open/closed/unknown 归一自 Volcano Queue CRD status.state）
- FR-25: 系统必须支持 queue annotation 翻译：将 ani.kubercloud.io/gpu-queue 翻译为 scheduling.volcano.sh/queue-name，只写入 spec.template.metadata.annotations
- FR-26: 系统必须在 Volcano 资源翻译时自动算出 volcano.sh/vgpu-memory（由 gpu-sharing-spec 派生），不能传空字符串
- FR-27: 系统必须通过 gpu-mode 标签 nodeSelector 物理隔离整卡节点和 vGPU 节点，防止两种工作负载混跑同一张物理卡
- FR-28: 系统必须在 Apply 失败时保留 DB 行（UpsertStatusTx state=failed UPDATE pending→failed），复用 cancelQuotaAndFinalize 同事务 Cancel 配额 + outbox，方便审计

---

## 5. Non-Goals (Out of Scope)

- 算力（core）切分 —— 字段预留 compute_per_share，不实现
- 租户管理 CRUD —— Services 层，本次不碰
- VPC/子网真实接入 —— 本次只留 adapter 兜底 + API 字段预留
- PR #46 的 3 个 volcano follow-up —— 不在本次范围
- MIG 模式 —— port 层有枚举，API 不暴露
- 真实环境 live gate —— 后续 Sprint
- outbox_events 表建表 + outbox publisher —— 李宇建表，佳生实现 publisher
- NATS 消费侧 + MessageBus Subscribe 健壮性 —— 佳生实现
- 计量（MeteringService + Collector + metering_usage_records）—— 佳生实现
- BOSS 下调额度延迟生效 —— P0 不实现"允许临时 used > reserved 只阻塞新建"的延迟机制
- 配额申请-审批链路（US-003/FR-7）—— P1，本期不做
- 配额策略页（套餐 CRUD + 发布 + 分配给租户）—— 后续迭代
- 待审批配额页面 —— 后续迭代
- 批量分配空闲卡 —— 本次单卡分配已覆盖
- 原型"预留超上限自动抬升配额"行为 —— 不实现，BOSS 设 total 时超限返回 409
- 多卡种/多集群/异构切分 —— 本期基于单集群、单卡种、等分假设，后续 Sprint 扩展
- 节点标签管理接口 —— P0 不做，节点标签由平台驱动安装后自动打，ANI 只读

---

## 6. Design Considerations

### 6.1 架构边界

- 规格目录 + 节点标签只读 = Core 能力（走 Core API）
- Volcano 资源翻译 = Core 能力（Adapter 实现，spec_id → Pod 资源请求 + 节点亲和性）
- 配额表 + 配额 port = Core 能力（已有 pkg/ports/quota.go + postgres_quota.go，复用）
- 配额管理 REST API = Core handler（走 Core REST → QuotaService port → PG）
- reconciler Confirm/Cancel = Core 组件（同事务 UpsertStatusTx + outbox）
- GPU_QUOTA_ENABLED 开关归 Core
- BOSS/Console → Core REST；Core → QuotaService port（同库调用，不经过 HTTP）

### 6.2 数据模型

- GPUSpec CRD：K8s CRD（集群级，静态配置，数量少 = 卡型 × 档位）
- 节点标签：K8s node labels（不持久化到 PG，ANI 只读）
- 配额表：PG（resource_quota / resource_quota_meta / resource_reservations 已建；新增 resource_reservation_allocations）
- 实例表：PG MetadataStore（workload_instances 已有，新增 quota_tx_ids JSONB 列）
- gpu_slices 表已废弃（不绑定租户与节点，vGPU 集群级等分由节点标签表达）

### 6.3 前端复用

- BOSS：复用现有 gpu-pool.tsx 页面结构，扩展 KPI + 新增弹框
- Console：复用现有 create-dialog.tsx，改"随意填"为"选规格"
- 复用现有 TDesign 组件库

---

## 7. Technical Considerations

### 7.1 已就绪基础设施

| 能力 | 位置 | 状态 |
|------|------|------|
| PG MetadataStore | pkg/ports/metadata.go + pkg/bootstrap/db.go | 已就绪，含 WithTenantTx/WithPlatformTx |
| PG RLS 租户隔离 | pkg/types/tenant.go SetDBTenant | 已就绪 |
| K8s CRD 操作模式 | volcano_queue_store.go | 已就绪，可复用 |
| Console servicesApi | console/src/api/client.ts | 已引入，BOSS 待引入 |
| GPU Inventory | kubernetes_gpu_inventory.go | 已就绪，可扩展读节点标签 |
| 租户身份链路 | identity_provider.go | 已就绪，JWT 提取 TenantID |
| QuotaService/QuotaStoreService | pkg/ports/quota.go + postgres_quota.go | 已就绪，直接复用 |

### 7.2 关键技术约束

- TCC 方案不可改（团队已锁定），本方案只做字段映射，不改 TCC 表结构/接口/流程
- volcano.sh/vgpu-memory 由 Adapter 自动算出，不能传空字符串
- schedulerName: volcano 是关键，不设此字段 Pod 走默认调度器，Volcano queue 完全不生效
- volcano.sh/vgpu-number extended resource 不随 Pod 调度递减，不能直接读 allocatable 剩余
- 配额表 schema 变更经 CODEOWNERS 共同 review（因 Core adapter 依赖此表）

### 7.3 基础约定（不可变前提）

- 节点 GPU 同型号，安装驱动后由平台自动打标签
- vGPU 集群级等分，切分规格通过节点标签表达，不由 ANI 按设备逐张切分
- BOSS 以"卡"为维度分配，一个整卡 = 1 卡，一个 vGPU = 1 卡
- 用户规格二选一，整卡规格和 vGPU 规格互斥
- ANI 预检 + Volcano 调度，不绑定租户与节点
- 配额三层量：0 ≤ 占用（used + reserved）≤ 预留（allocated_gpu_count）≤ 配额上限（total）
- 本期基于单集群、单卡种、等分假设

---

## 8. Success Metrics

- BOSS 可设配额上限 + 预留额度，数据入 PG 可查
- Console 选规格创建实例，按四态标注/过滤，避免盲选后提交才报 409
- TCC 配额预检生效，并发场景不超卖（4 张预留卡不被 5 个 pending 实例超卖）
- Volcano 调度生效，Pod 带 schedulerName: volcano + queue annotation 正确走 Volcano queue 准入
- reconciler Confirm/Cancel 同事务原子，配额无泄漏（provisioning 超时自动 Cancel 释放）
- 删除实例配额正确释放（Cancel + Release 双调覆盖三种原态）
- GPU_QUOTA_ENABLED=false 时配额完全旁路，=true 时强制生效
- HAMi 代码清理完毕，统一用 volcano scheduler

---

## 9. Open Questions

- 本地闭环边界：tenant_id 先用默认值（租户功能未完成），VPC/Subnet 用占位默认值（vpc 代码合入前），真实环境 live gate 后续 Sprint
- outbox 集成测等佳生 adapter 合并后补
- 旧节点标签迁移：现有代码读 nvidia.com/gpu.product 和 ani.kubercloud.io/gpu-model，过渡期 inventory 优先读新 label 回退读旧 label，后续统一只读新 label

---

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core + console + boss |
| Code scope | repo/api/openapi/v1.yaml（契约）+ repo/pkg/ports/ + repo/pkg/adapters/ + repo/services/ani-gateway/ + repo/frontends/boss/ + repo/frontends/console/ |
| OpenAPI authority | Core change batch（新增 12 项接口 + 扩展 4 个 schema） |
| Frozen exclusions | repo/api/openapi/services/v1.yaml（Services API 不碰）、PR #46 的 3 个 volcano follow-up |
| idempotency_key | required on: POST /gpu-specs、DELETE /gpu-specs/{spec_id}、PUT /admin/tenants/{tenant_id}/quota、PUT /admin/tenants/{tenant_id}/reservations、PATCH /gpu-inventory/{device_id}、POST /instances |
| Module main doc | repo/services/docs/boss-modules/ops/gpu-pool-management.md（BOSS）、repo/services/docs/console-modules/compute/gpu-management.md（Console） |
