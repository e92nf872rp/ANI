# GPU 规格与配额管理 — Grilling 决策历史归档

> **性质**:过程性决策记录(非方案正文)。本文件归档 plan.md 在 grilling 盘问过程中产生的决策表、修订清单和被 superseded 的历史决策,仅供追溯。
>
> **真实来源**:当前有效的方案结论以 [plan.md](../../plan.md) 为准。本文件中的"锁定决策"若与 plan.md 正文冲突,以 plan.md 正文为准。
>
> **归档时间**:2026-08-11 从 plan.md §三 + §十五 迁出。
>
> **维护规则**:本文件为只读归档,不再维护。新增决策直接落到 plan.md 正文或对应批次记录。

---

## 一、grilling 盘问决策记录(原 plan.md §三)

> **⚠️ Volcano 改造 superseded 说明(对应 plan.md §二 基础约定):** 以下决策表中涉及 `PlanScheduling 选切片`、`AcquireSlice 乐观锁`、`ExcludeSliceIDs`、`gpu_slices 表`、`GPUSliceStore`、`CountByTenantTx`/`AssignToTenantTx`、`切片状态流转`、`切片释放` 的决策(D2/D6/D7/D11/D18/D19/G3/G10/G14/G15/G16/G18 等)**已被 plan.md §二 基础约定(不绑定租户与节点、vGPU 集群级等分)superseded**。这些决策记录保留作为历史追溯,实际实现以 plan.md 的 Volcano 改造方案为准。

> 两阶段盘问:**阶段一** 4 轮解决 7 处原始矛盾 + 15 个关键决策;**阶段二** 13 轮深入 TCC 方案、共享池语义、分工边界、插入点,锁定 20 项决策。以下为锁定决策,不允许后续 PR 偷偷推翻。

### 阶段一:基础决策(4 轮)

| 问题               | 决策                                             | 理由                               |
| ---------------- | ---------------------------------------------- | -------------------------------- |
| Q1 等分 vs 粒度      | **近似等分**,256MB 取整,`shares` + `mb_per_share` 双存 | 国产卡 256/512MB 粒度,纯等分会切不下去        |
| Q2 规格实例粒度        | **卡型×档位持久化实体**,有 `spec_id`                     | A100-80G-4 ≠ H100-80G-4,是不同规格实例  |
| Q3 切分物理边界        | **卡级**,逻辑声明(实例创建时真切)                           | 8 卡节点每张卡各自选 2/4/8 档位             |
| Q3 预留            | 复用 `tenant_id` + 新增 `reserved` 位               | 非新增独立机制                          |
| Q4 租户预留          | 预留代码位置(非新 port)                                | 租户身份链路已通,缺的是配额能力                 |
| Q9 gpu\_type     | 非自由字符串,对齐 inventory 实际值                        | 规格创建时校验 `gpu_type` 存在于 inventory |
| Q9 设备标识          | 用物理设备 ID(Nvidia uuid 等)                        | 切分时按 device\_id 区分               |
| Q10 切分后状态        | 新增 `sliced` 枚举值 + `slices?` 可选字段               | 非破坏性,现有消费者零改动                    |
| Q13 PR #46 关系    | 承接(基于 #46 合入后的 main),不碰 follow-up              | #46 的 3 个 volcano 错误处理不在本次范围     |
| Q14 BOSS 取租户列表   | BOSS 引入 servicesApi                            | Console 已有先例,不违反 §3.2            |
| Q15 持久化方式        | 规格 CRD,切片 PG MetadataStore                     | 规格=静态少;切片=多+查询+ACID              |
| Q16 inventory 扩展 | (A) 新增 `slices?` + `sliced` 枚举                 | 现有消费者零改动                         |
| Q18 网络存储         | Core API 新增 `network_config` 可选字段(预留)          | adapter 兜底,钧伟合入后改读真实 VPC         |
| Q19 幂等           | (A) `idempotency_key` 作 CRD label / PG 列       | 和 GPUSchedulingQueueStore 现有模式一致 |
| Q22 创建实例迁移       | (A) 新增 `spec_id`,旧字段 deprecated 保留             | §4.5 非破坏性,有清晰迁移终点                |

### 阶段二:TCC 方案深化决策(13 轮,覆盖原 Q3/Q5/Q8/Q11/Q12/Q17/Q20)

| 决策项                           | 锁定结论                                                                                                                                                | 理由                                                                                           |
| ----------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| **D1 配额归属(Q1.1'')**           | 配额归 Core:配额表 DDL + QuotaService port + adapter 归 Core;Core 暴露配额管理 REST API 端点;reconciler 走 QuotaService port 读写同表                                   | `通用资源配额与计量落地方案.md` 明确 QuotaService/QuotaMetaService 端口定义在 `pkg/ports/`(Core);配额是 Core 基础设施能力。配额维度/命名/表结构真实来源为 `配额表.md` |
| **D2 共享池语义(Q2.1)**            | "静态分配池":BOSS 分配给某租户后该租户才可使用,分配前任何租户看不到、用不了。PlanScheduling 只选 `tenant_id=A AND reserved=false AND status=available`,`tenant_id IS NULL` 切片不在任何租户可选范围 | 共享池是"未分配"状态,不触发配额扣减;实例删除后切片回退到租户池不回退共享池                                                      |
| **D3 四态边界(Q2.2)**             | `tenant_id`(归属)+ `reserved`(锁)+ `status`(占用)三个正交维度,四态都合法。`NULL+true`=锁住的共享池,`A+false`=A 专属可用(预留/分配态,PlanScheduling 可选),`A+true`=A 锁定/暂停(维护态,本期无操作入口,G3 锁定),`NULL+false`=共享可分配                      | `reserved` 不冗余,是"临时锁住"开关,和 tenant\_id 正交;预留=分配即可用(Q2 锁定),设 reserved=FALSE                                                     |
| **D4 配额维度(Q5.1)**             | 用 TCC `resource_type`(gpu\_count/cpu\_core/memory\_gb),**放弃 plan.md 原** **`spec_id`** **维度**。两者不并存                                                       | 切片归属已管控规格范围,spec\_id 配额重复;并存引入分数折算无解                                                         |
| **D5 配额单位(Q6.2)**             | 1 槽位 = 1,整卡和 vGPU 切片都算 1。`gpu_count` 实际语义是"GPU 份数/槽位数"                                                                                                  | 前端原型 `usedGpu += n` 证实;配额只管占用几个槽位,不管显存/算力                                                    |
| **D6 创建流程顺序(Q6.1)**           | PlanScheduling(纯读) → TryMany(预占配额) → AcquireSlice 乐观锁落库 → Apply K8s → reconciler Confirm/Cancel                                                     | PlanScheduling 纯读无副作用,配额预占在前                                                                 |
| **D7 切片并发(Q7.1)**             | 落库时乐观锁 `UPDATE ... WHERE slice_id=? AND status='available'`,RowsAffected=0 重选,最多 3 次,返回 409。PlanScheduling 加 `ExcludeSliceIDs` 参数                   | 不无限重选避免死循环;Cancel + 重选分两步(新事务),不跨 K8s 长事务                                                    |
| **D8 provisioning 超时(Q3.2)**  | **本期做**,默认 10 分钟,`PROVISIONING_TIMEOUT_MIN` 环境变量。插在 Observe 后 Reconcile 前,新增 `markProvisioningFailed`                                               | 不做则配额泄漏 + 租户配额被占死,必须修                                                                        |
| **D9 开关语义(Q4.1)**             | `GPU_QUOTA_ENABLED` 进程级启动读,true=强制(校验+扣减),false=完全旁路。只一个开关归 Core,Service 不需自己的开关                                                                    | TCC 下 false 时 TryMany 跳过,Confirm/Cancel/Release 跳过;true 时全执行                                 |
| **D10 开关 false→true(Q4.1)**   | used\_count 从 0 开始不回填,BOSS 仍能配置 total                                                                                                               | 开关 false 时 reserved/used 停 0,true 后新创建开始走 TCC                                                |
| **D11 分工-本次做(Q7.2)**          | GPUSpec/Slice port+adapter、PlanScheduling、CRD、切片建表、demo\_instances TryMany、reconciler Confirm/Cancel、删除释放、UpsertStatusTx、quota\_tx\_ids migration、**配额 port 接口落地 + mock adapter** | 整条 GPU 容器创建实例链路归本次;配额相关操作先做 stub(mock adapter),不被佳生阻塞(同 D13 outbox 模式) |
| **D12 分工-不做(Q7.2)**           | 配额 port 的 **PG adapter**(佳生)、配额表建表(Core DDL,Leader建)、outbox表(李宇)、publisher(佳生)、NATS消费(佳生)、计量(佳生)                                                   | TCC 方案 §7 落地顺序归他人;佳生 PG adapter(含 GetTotalForUpdateTx)合并后替换本次 mock |
| **D13 outbox 隔离(Q8.3)**       | outboxWriter 小接口 + mock,不被佳生阻塞,PR 先合单测,集成测后续                                                                                                        | outbox 表归李宇建,publisher 归佳生,我只写 INSERT                                                        |
| **D14 UpsertStatusTx(Q9.1)**  | 新建 `WorkloadInstanceStoreTx` 小接口,不破坏现有 6 个 mock,现有 `UpsertStatus` 调用保留                                                                              | 接口隔离原则,只有 reconciler 用 Tx 版                                                                  |
| **D15 Confirm 插入点(Q10.1)**    | Changed 分支(pending→running),开关 true 时走 `WithPlatformTx` 同事务:Confirm + UpsertStatusTx + outbox。`WithPlatformTx` 通过 MetadataStore 注入                  | TCC 要求同事务原子性                                                                                 |
| **D16 quota\_tx\_ids(Q10.1)** | workload\_instances 新增 JSONB 列存 TryMany 返回的预占流水 ID,本次加 migration                                                                                    | reconciler Confirm 需要 quotaTxIDs                                                             |
| **D17 Cancel 三处复用(Q10.2)**    | markProviderMissing + markProvisioningFailed + API层Apply失败,公共 `cancelQuotaAndFinalize` 方法                                                           | 三处 Cancel 逻辑相同                                                                               |
| **D18 创建失败切片释放(Q10.3)**       | Cancel 同事务:Quota.Cancel + 释放切片(`AcquireSlice` 逆向) + UpsertStatusTx(failed),共享 MetadataTx                                                            | 创建失败时切片必须释放,避免泄漏                                                                             |
| **D19 删除切片释放(Q10.3)**         | 删除时**同事务**:reconciler `deleting→deleted` 分支,`Quota.Release` + 释放切片 + `UpsertStatusTx` + outbox 全在 `WithPlatformTx` 内,任一失败整体回滚重试;GC 兜底应对极端场景,**GC 本期 TODO(不做)** | 删除流程切片泄漏本期可接受(有重试兜底)                                                                  |
| **D20 删除配额 Release(Q8.2)**    | `Quota.Release(ctx, tx, txIDs)` 接口签名见 [core-quota-port-contract.md](../../core-quota-port-contract.md) §1(佳生方案 §4.2 已定义 QuotaService,Release 为新增方法,佳生已确认),接收外部 tx 与 `UpsertStatusTx` + outbox 同事务原子,`state='confirmed' → released` 幂等 | 佳生实现 PG adapter(D12),我方先做 mock,在 `orchestrator.Delete` 同事务调用,佳生 PG adapter 合并后替换 |
| **D21 pending 实例删除**         | pending 状态实例被删时,reconciler `pending→deleting→deleted` 分支用 **`Quota.Cancel`**(释放 `reserved`)而非 `Quota.Release`(Release 只认 `confirmed`);同事务释放切片回 `available` + `UpsertStatusTx(deleted)` + outbox,与 running 实例删除对称。开关 false 时全跳过 | pending 实例配额仍处 `reserved` 未 Confirm,`Release` 只处理 `confirmed→released` 会漏释放;必须用 Cancel 释放 reserved 才不泄漏配额 |
| **D21' 删除双调(grilling 修正)**  | **删除流程不依赖原态判定**,reconciler `deleting→deleted` 分支同事务内 **Cancel + Release 顺序双调**:Cancel 释放 reserved(对 confirmed 态 no-op),Release 释放 used(对 reserved 态 no-op),靠 WHERE 守卫互斥。切片释放 + `UpsertStatusTx(deleted)` + outbox 仍同事务。开关 false 时配额操作全跳过。覆盖 pending/running/failed 三种删除前原态 | 原 D21 依赖"reconciler 知道删除前原态"但 API 层同步删除时已把 state 改成 deleting,reconciler 读 PG 拿不到原态;双调靠 reservation 单态性质 + WHERE 守卫天然互斥,无需原态判定。Cancel 幂等约定见 [core-quota-port-contract.md](../../core-quota-port-contract.md) §1.2 |
| **D22 Apply 失败保留 DB 行**      | K8s apply 同步报错时**不删 `workload_instances` 行**,改为 `UpsertStatusTx(state=failed)` 保留记录;同事务 Cancel 配额 + 释放切片(回 `available`)+ outbox INSERT(`event_type='instance.create_failed`),复用 `cancelQuotaAndFinalize`(D17) | 删 DB 行会丢失审计/排障/计量依据;D18 锁定的就是 `UpsertStatusTx(failed)`,原 6.3.1 第④步"DELETE 实例行"与 D18 矛盾,按 D18 修正 |

**已作废决策(阶段二推翻):**

| 原决策                                  | 作废理由                                                |
| ------------------------------------ | --------------------------------------------------- |
| 原 Q3 共享空闲池 `tenant_id IS NULL` 的切片集合 | 阶段二 Q2.1 明确:共享池切片不被 PlanScheduling 选中,不是"任何租户可用"    |
| 原 Q11 配额本质在 tenant\_id 归属上增配额计量      | 阶段二 Q5.1 明确:用 TCC resource\_type 维度,放弃 spec\_id 维度  |
| 原 Q17 配额维度按 spec\_id 配               | 阶段二 Q5.1 推翻:用 TCC resource\_type                    |
| 原 Q20 配额表 WithPlatformTx             | 阶段二 Q1.1'' 明确:配额在 Core,DDL 变更经 CODEOWNERS 共同 review |

---

## 二、Volcano 改造 grilling 修订清单(原 plan.md §十五,0805/0811)

> **背景**:plan.md 原基于 HAMi 切片方案编写,现更换为 Volcano 调度逻辑。本节记录 0805/0811 grilling 盘问中确认的修订项。修订项分"必须修订"(影响实现正确性)和"补充说明"(改善可读性/完整性)两级。
>
> **当前状态**:以下所有修订项均已落地到 plan.md 正文。本清单仅作历史追溯,不再维护。

### 修订项 1:§六 接口清单 — 配额端点复用已有(必须修订)

已有 Core OpenAPI(v1.yaml:7553-7670)定义了完整的配额管理 CRUD,`scope=platform`(仅平台管理员)。经 RBAC 核实:Console 租户用户 `scope=tenant`,不能调 `/admin/` 前缀端点。

| 用途 | 端点 | RBAC scope | 动作 |
|---|---|---|---|
| BOSS 批量新建租户配额 | `POST /admin/tenants/{tenant_id}/quota` | `scope:quota:write`(platform) | **已有,复用** |
| BOSS 修改配额上限 | `PUT /admin/tenants/{tenant_id}/quota` | `scope:quota:write`(platform) | **已有,复用** |
| BOSS 查指定租户配额 | `GET /admin/tenants/{tenant_id}/quota` | `scope:quota:read`(platform) | **已有,复用** |
| BOSS 删除租户配额 | `DELETE /admin/tenants/{tenant_id}/quota` | `scope:quota:write`(platform) | **已有,复用** |
| BOSS 查配额元数据 | `GET /admin/quota-meta` | `scope:quota:read`(platform) | **已有,复用** |
| Console 查自己配额 | `GET /quotas/me` | `scope:quota:read`(tenant) | **需新增** |
| BOSS 列租户配额(分页) | `GET /quotas` | `scope:quota:read`(platform) | **需新增** |

**§六接口清单需删除:** `PUT /quotas`(#4)、`POST /quota-meta`(#2)、`PATCH /quota-meta/{resource_type}`(#3)——已有端点覆盖。
**§六接口清单需保留:** `GET /quotas/me`(#6,Console 租户视角)、`GET /quotas`(#5,BOSS 分页列表)。
**§六需标注:** `PUT/GET/DELETE /admin/tenants/{tenant_id}/quota` + `GET /admin/quota-meta` 为"已有,复用"。

> core-quota-port-contract.md §2.3 定义的 `PUT /quotas` / `GET /quotas` / `GET /quotas/me` 3 端点中,`PUT /quotas` 作废(复用已有 `PUT /admin/tenants/{tenant_id}/quota`),`GET /quotas` 和 `GET /quotas/me` 保留(已有端点无此路径)。core-quota-port-contract.md §2.3 需同步更新。

### 修订项 2:§5.4.1 决策 A — 缩容语义对齐已有契约(必须修订)

已有 `QuotaUpdateRequest`(v1.yaml:7588-7590)缩容时用 `GREATEST(total, used+reserved)` **clamp**(不拒绝),plan.md 决策 A 写的是 TCC CHECK **拒绝**(409),两者直接矛盾。

**修订:** 决策 A 改为以已有契约为准:`PUT /admin/tenants/{tenant_id}/quota` 缩容时服务端用 `GREATEST(total, used+reserved)` clamp 到 `used+reserved`,返回 `tightened=true`。不拒绝,不实现延迟生效机制。

### 修订项 3:HAMi 迁移边界 — 新增章节(必须修订)

**决策:** 保留 HAMi 的 volcano-vgpu-device-plugin(它本就和 Volcano 协同),只删 ANI 代码中 `hami-scheduler` 调度器选择逻辑,统一用 `volcano` scheduler。本期就删,不延期。

> **技术背景:** `volcano.sh/vgpu-number` / `volcano.sh/vgpu-memory` 由 volcano-vgpu-device-plugin(基于 HAMi-core)在节点上注册为 extended resource,Volcano scheduler 感知这些资源做调度。ANI 只翻译资源请求,不感知 device plugin 实现。

**需删/改的代码(放在阶段 3 GPU-SPEC-ADAPTERS-A 批次):**

| 文件 | 当前行为 | 改造动作 |
|---|---|---|
| [kubernetes_gpu_inventory.go](../../repo/pkg/adapters/runtime/kubernetes_gpu_inventory.go) 行 22 `kubernetesHAMISchedulerName` | HAMi 节点选 hami-scheduler | 删除常量 + 删除行 143-146 的 `if isHAMINode` 分支,统一用 `volcano` |
| 同文件行 26-29 `hami.io/node-nvidia-register` annotation | 从 HAMi annotation 解析设备信息 | 改读 `volcano.sh/node-vgpu-register` annotation |
| 同文件行 179-194 `resourceNameForNode` | HAMi 节点 vGPU 用 `nvidia.com/gpu`,非 HAMi 用 `nvidia.com/vgpu` | vGPU 统一改用 `volcano.sh/vgpu-number` + `volcano.sh/vgpu-memory` |
| 同文件行 284-295 `runtimeClassNameForNode` | HAMi 节点返回 `hami-vgpu` runtime class | **本期不改**(不在 API/前端暴露,影响极小,见修订项 3.1) |
| 同文件行 324-332 `isHAMINode` | 判断是否 HAMi 节点 | 改为 `isVolcanoVGPUJNode` 或直接删,用 `gpu-mode` 标签判断 |
| 同文件行 387-481 `parseHAMIAnnotation` / `hamiPhysicalDevice` | 解析 HAMi annotation | 改为解析 `volcano.sh/node-vgpu-register` annotation,结构体改名 |
| [values.yaml](../../repo/deploy/helm/ani-platform/values.yaml) 行 118 `provider: hami` | Helm 配置 | 改为 `provider: volcano-vgpu`(或保留 hami 但说明语义) |
| [v1.yaml](../../repo/api/openapi/v1.yaml) 行 781 `vgpu=HAMi vGPU` | OpenAPI 描述 | 改为 `vgpu=Volcano vGPU`(品牌脱敏) |
| [core-schema.d.ts](../../repo/frontends/console/src/api/core-schema.d.ts) 行 3355 | 前端 schema 同步 | 跟随 v1.yaml 更新 |

**旧节点标签迁移:** 现有代码读 `nvidia.com/gpu.product` label 和 `ani.kubercloud.io/gpu-model` label。plan.md §5.2 新增 `ani.kubercloud.io/gpu-spec`。过渡期 inventory 优先读新 label,回退读旧 label;或直接只读新 label,要求节点重新打标签(需确认)。

#### 修订项 3.1:runtime class `hami-vgpu` 本期不改

经核实:`hami-vgpu` 只在 1 个 Go 常量(kubernetes_gpu_inventory.go:29)和 2 个测试断言中出现。**不在 Core OpenAPI 暴露**(GPU schema 无 runtime_class 字段,只有 Sandbox 有)。**前端不可见**。**部署清单无依赖**(deploy/ 中无 `hami-vgpu` 字面值)。属于 adapter 内部实现细节。按 Karpathy 原则三(只触碰必须改动的部分),本期不改。

### 修订项 4:§5.2 节点标签体系 — 补 annotation(必须修订)

plan.md §5.2 只列了节点 label,没列 annotation。实际 volcano-vgpu-device-plugin 在节点上打的是 annotation `volcano.sh/node-vgpu-register`(不是 label),inventory 解析设备信息要读这个 annotation。

**§5.2 需补:**

| 场景 | 节点 label | 节点 annotation |
|---|---|---|
| vGPU A100 20GB | `ani.kubercloud.io/gpu-sharing-spec: NVIDIA-A100-SXM4-20GB`<br>`ani.kubercloud.io/gpu-sharing-policy: quarter`<br>`ani.kubercloud.io/gpu-mode: vgpu` | `volcano.sh/node-vgpu-register`(设备注册信息,inventory 解析设备详情) |

### 修订项 5:§5.3 规格到 Volcano 映射 — 补资源字段来源说明(补充说明)

plan.md §5.3 写 vGPU 用 `volcano.sh/vgpu-memory` + `volcano.sh/vgpu-number`,但没说明这些字段从哪来。需补一句:

> `volcano.sh/vgpu-number` / `volcano.sh/vgpu-memory` 由 volcano-vgpu-device-plugin(基于 HAMi-core)在节点上注册为 extended resource,Volcano scheduler 感知这些资源做调度。ANI 只翻译资源请求,不感知 device plugin 实现。

### 修订项 6:§7.3.2 reconciler — 补对账循环逻辑(必须修订)

实现方案 §七.2 要求:"必须有 reconciler 定时对账:`ANI.used ?= K8s 中该租户非终态 GPU Pod 的卡数总和`,以 K8s 实际状态为准修正 ANI,防止漂移。"

现有 reconcile_controller.go 只做状态转换(observe → map phase → upsert status),不做 count 对账。plan.md §7.3.2 也没写对账逻辑。

**§7.3.2 需补:** 在 reconciler tick 里加对账逻辑:遍历 K8s GPU Pod 计算租户实际 used,与 PG `resource_quota.used` 比对,不一致则修正。本期先做 mock/桩(依赖 QuotaService 的 PG adapter,佳生实现)。

### 修订项 7:§5.6 GPUInventoryRecord — 改用实际名称(必须修订)

plan.md §5.6 说"GPUInventoryRecord 扩展",但代码库没有 `GPUInventoryRecord` 这个 Go 结构体。实际:
- Go port 层用 `GPUNodeClass` + `GPUDeviceClass`(见 [gpu_inventory.go](../../repo/pkg/ports/gpu_inventory.go) 行 22-49)
- OpenAPI 层用 `GPUInventoryListResponse` / `GPUOccupancyStats`

**§5.6 需改:** "GPUInventoryRecord" 改为 `GPUNodeClass` / `GPUDeviceClass`(Go port 层)+ `GPUInventoryListResponse`(OpenAPI 层)。新增字段 `gpu_mode`/`gpu_spec`/`gpu_sharing_spec`/`gpu_sharing_policy` 加到 `GPUNodeClass`。

### 修订项 8:§7.3.1 PlanScheduling — 保留但改语义(必须修订)

**决策:** PlanScheduling 保留,但去掉"选切片"逻辑,改为"读节点标签 + 校验 spec_id 有匹配节点",返回 nodeSelector 线索供 Adapter 用。

> Volcano 自己做节点选择,ANI 只翻译资源请求。PlanScheduling 不再选具体设备/切片,只做"规格有没有匹配节点"的校验性查询。

### 修订项 9:§十 已废弃清单 — 补 HAMi 代码废弃条目(补充说明)

§十"已废弃"清单需追加:

| 原代码 | 废弃理由 |
|---|---|
| `kubernetesHAMISchedulerName` 常量(kubernetes_gpu_inventory.go:22) | 统一用 volcano scheduler,不再选 hami-scheduler |
| `isHAMINode` 函数(kubernetes_gpu_inventory.go:324-332) | 改用 `gpu-mode` 标签判断,或改名 `isVolcanoVGPUJNode` |
| `parseHAMIAnnotation` / `hamiPhysicalDevice`(kubernetes_gpu_inventory.go:387-481) | 改读 `volcano.sh/node-vgpu-register` annotation,改名 `parseVolcanoVGPUAnnotation` |
| `hami-scheduler` 分支(kubernetes_gpu_inventory.go:143-146) | 统一用 volcano scheduler |

### 修订项 10:§十三 风险表 — 补对账相关风险(补充说明)

§十三 风险表需追加:

| 风险 | 对策 |
|---|---|
| 配额计数器漂移(Pod 被 kubectl 删/节点宕机/Volcano 状态反馈丢失) | reconciler 对账循环:遍历 K8s GPU Pod 计算租户实际 used,与 PG `resource_quota.used` 比对,不一致则修正(以 K8s 实际状态为准) |

### 修订项 11:core-quota-port-contract.md §2.3 — 同步更新(必须修订)

core-quota-port-contract.md §2.3 定义的 3 端点需与修订项 1 对齐:
- `PUT /quotas` → 作废,复用已有 `PUT /admin/tenants/{tenant_id}/quota`
- `GET /quotas` → 保留(BOSS 分页列表,已有端点无此路径)
- `GET /quotas/me` → 保留(Console 租户视角,已有端点无此路径)
- §6 "与 core-quota-api.md 的关系"需更新:不再"两套并存",改为"已有端点 + 新增 2 端点"

### 修订项 12:§5.3 资源映射表 — 补 schedulerName(必须修订)

验证文档(volcano-queue最小演示.md 第 64、228 行)证明 `schedulerName: volcano` 是 Pod 进入 Volcano 队列调度的关键字段。不设此字段 Pod 走默认调度器,Volcano queue 完全不生效。

**§5.3 映射表已补:** 新增 `schedulerName` 列,所有模式统一为 `volcano`。代码已有(kubernetes_gpu_inventory.go:142-146),Volcano 改造后删除 `isHAMINode` 分支统一用 `volcano`(修订项 3)。

### 修订项 13:§十 Volcano 资源翻译 Adapter — 补 queue annotation 翻译(必须修订)

验证文档(volcano-queue最小演示.md 第 245 行)证明 `scheduling.volcano.sh/queue-name` 必须写在 `spec.template.metadata.annotations`,不是 Deployment 顶层 `metadata.annotations`。

**§十 Adapter 已补:** `volcano_resource_translator.go` 新增 queue annotation 翻译逻辑:将 `ani.kubercloud.io/gpu-queue`(现有 annotation,planning.go:213 写入)翻译为 `scheduling.volcano.sh/queue-name`(Volcano 原生 annotation),只写入 `spec.template.metadata.annotations`。现有 `ani.kubercloud.io/gpu-queue` 写在 Deployment 顶层的那份不影响编码,不删。

### 修订项 14:Queue 不设 capability — 明确写入架构图(必须修订)

P0 Volcano Queue 不设 `capability`(配额隔离由 ANI TCC 两道闸保证),Queue 只负责排序/权重/排队。验证文档(volcano-queue最小演示.md §6)证明配额收缩不驱逐存量(无 reclaim 插件),Queue capability 是"准入"限制不是"强杀"手段。

**§四架构图已补:** Volcano 调度层新增"Queue 不设 capability(P0),配额隔离由 ANI TCC 保证""Queue 只负责排序/权重/排队,不负责租户间容量归属"两行。

### 修订项 15:Queue status 回显 + Console 队列 UI(必须修订)

**缺失 3 项(代码核实):**
1. Queue status 不回显:`volcanoQueueCRD` 无 `Status` 字段,`crdToQueue` 不返回 status,`GPUSchedulingQueue` schema 无 `allocated` 字段
2. Console 队列配置 UI(US-008):plan 阶段 6 无队列配置页
3. Queue capability 可选支持(后续,不在 P0)

**已补:**
- §六 #18:`GPUSchedulingQueue` schema 新增 `status` 字段(nullable object,含 `allocated` map)
- §十:`volcano_queue_store.go` 新增 `Status` 字段 + `crdToQueue` 映射 allocated
- §十一 阶段 3:新增 `volcano_queue_store.go` 扩展(读 Volcano Queue CRD status.allocated)
- §十一 阶段 6:新增队列配置页(`gpu-scheduling-queues.tsx`)+ 创建表单队列下拉

### 修订项 16:§7.3.2 reconciler — 排队中 vs 调度失败消歧(必须修订)

Volcano 返回 Pod Pending 时需区分"排队中"(queue 配额不足)和"调度失败"(节点资源不足),填充 v1.yaml 第 639 行 `scheduling_state` 字段。验证文档证明两种 Pending 的 Pod Events 文案不同,但节点资源不足时 PodGroup 事件也可能带 `queue resource quota insufficient`(消歧陷阱)。

**§7.3.2 已补:** 方案 A + 数值校验消歧。Events 文本匹配为主判据(`queue resource quota insufficient` → queued;`Insufficient volcano.sh/vgpu-memory` → failed),`Pod 请求 vs 节点 idle` 数值校验兜底。reconciler 新增 Pod Events 读取 + 节点 idle 查询能力。

### 修订项 17:§二.7 + §5.4 配额设计 — 对齐已有 TCC 契约(解法 Y,必须修订)

原 plan 把 `resource_quota.total`(已有契约定义"配额上限")当作"预留额度"用,语义冲突。解法 Y:契约语义为准,改 plan。

**已补:**
- §二.7 重写:三层量映射(`配额上限` → `resource_quota.total`;`占用` → `resource_quota.used`;`预留` → 新增 `resource_reservation_allocations.allocated`);两道闸(配额上限校验 + 预留空闲校验)
- §5.4.1 字段映射表重写
- §5.4.4 集成点:BOSS 预留 = 两步(设 total + 设 allocated)
- §5.5 计数器流转图重写
- §7.3.1 创建流程:两道闸 + TryMany
- §7.3.4 校验时机:两道闸 + TCC 预占
- §六 新增预留管理 3 端点(#15/#16/#17)
- §十一 阶段 1:契约新增预留端点;阶段 3:新增 `resource_reservation_allocations` migration;阶段 5:BOSS 预留分配弹框

### 修订项 18:§六 #14 返回结构 — 补 status 枚举(必须修订)

US-002 要求 Console 展示"有空闲/已满/不可用"三态。#14 原只返回 `quota_remaining` + `has_matching_nodes`,无法表达"已满"(配额用完但设备正常)。

**§六 #14 已补:** 新增 `status` 枚举字段(`available`|`full`|`unavailable`),判定规则:
- `quota_remaining > 0 && has_matching_nodes` → `available`
- `quota_remaining = 0 && has_matching_nodes` → `full`
- `!has_matching_nodes` → `unavailable`

**§六 补充说明已补:** `status` 枚举判定规则 + 余量来源改为 `resource_reservation_allocations.allocated_gpu_count - resource_quota.used - resource_quota.reserved`(对齐解法 Y + 补 reserved)。

### 修订项 19:三道闸补 reserved + 补 allocated 校验 + 预留改 gpu_count 单维度(必须修订)

**用户发现的三个超卖漏洞:**

1. **闸 2 漏算 TCC reserved:**plan 闸 2 只减 `used`(已 Confirm),没减 `reserved`(pending 实例占用的额度)。allocated=4, reserved=4, used=0,新请求 1 → 闸 2:4-0=4>=1 ✓ 通过 → 5 个实例占 4 张预留卡,违反 `占用 ≤ 预留` 不变量(FR-4 失效)

2. **配额维度与预留维度不一致:**D4 锁定"放弃 spec_id 维度",配额按 `(tenant_id, resource_type=gpu_count)` 单维度;但预留表 `resource_reservation_allocations` 按 `(tenant_id, gpu_spec)` 维度。`allocated - used` 把按 spec 的 allocated 减去按 gpu_count 的 used,维度不匹配

3. **TCC Try SQL 只校验 total 不校验 allocated:**TCC 原子层完全不保证预留隔离,配合问题 1 可直接超卖

**修复(方案 B:预留按 gpu_count 单维度,对齐 D4):**

- `resource_reservation_allocations` 表改单维度:`(tenant_id, allocated_gpu_count)`,删除 `gpu_spec` 列
- 闸 2 补 reserved:`(allocated_gpu_count - used - reserved) >= request`
- 闸 3 TCC Try 补 allocated 校验:`reserved + used + request <= total AND <= allocated_gpu_count`
- 不变量更新:`0 ≤ used ≤ (used + reserved) ≤ allocated_gpu_count ≤ total`

**已改位置:**§二.7 + §5.4.1 + §5.4.4 + §5.5 + §7.3.1 + §7.3.4 + §六 #14 + §十一 阶段 5

### 修订项 20:total 语义残留 + #14 公式矛盾 + available_count 计算(必须修订)

**用户发现的三个内部矛盾:**

4. **`resource_quota.total` 语义自相矛盾:**§6 #2 和 §12 把 `total` 当"实现方案的 reserved(BOSS 分配额度)",但 §二.7 和 §5.5 把 `total` 当"配额上限(limit)",预留另存到 `allocated_gpu_count`。这是解法 Y 改了一半的残留

5. **#14 的 `quota_remaining` 公式不一致:**§6 #14 用 `total - used - reserved`,§6 补充说明用 `allocated - used`,数值不同

6. **#14 的 `available_count` 计算不合理:**`min(quota_remaining, has_matching_nodes ? 1 : 0)` → `min(quota_remaining, 1)` 永远 ≤1,若 `quota_remaining=4` 且有匹配节点,`available_count=1`?语义应是"能创建几个",不是"是否可创建 1 个"

**修复:**

- §6 #2:`PUT /admin/tenants/{tenant_id}/quota` 描述改为"BOSS 设配额上限"(不是预留);预留走 `PUT /admin/tenants/{tenant_id}/reservations`(#15)
- §12 闭环验收:分配到租户 = 设 `resource_quota.total`(配额上限)+ `allocated_gpu_count`(预留额度)
- §6 #14 + §6 补充说明 + §7.2:`quota_remaining` 统一为 `allocated_gpu_count - used - reserved`
- §6 #14 + §7.2:`available_count` 改为 `has_matching_nodes ? quota_remaining : 0`(去掉 `min(quota_remaining, 1)`)

**已改位置:**§六 #2 + §十二 + §六 #14 + §六 补充说明 + §7.2

### 修订项 21:used 语义 + 下调 allocated 行为 + Deployment vs Volcano Job(必须修订)

**用户发现的三个与实现方案/需求文档的冲突:**

7. **`used` 语义冲突:** 实现方案和需求文档的 `used`/`占用` 含 pending 实例,plan 的 `resource_quota.used` 只含 running(Confirm 后)。导致闸 2 计算语义不清

8. **下调 `allocated` 行为冲突:** plan §十四 说"拒绝操作",但需求文档 US-007 说"Console 刷新后展示新的 limit/预留"(拒绝就没法展示)。实现方案说"延迟生效",plan 说"不实现延迟生效"。plan 对 `total` 用 clamp,对 `allocated` 却拒绝,不一致

9. **Deployment vs Volcano Job 冲突:** 实现方案 §六.4 说"翻译成 Volcano Job",plan §7.3.3 用 K8s Deployment

**修复:**

- **问题 7:** §5.4.1 补"used vs 占用 语义澄清"段落。`resource_quota.used` = Confirm 后实扣(仅 running);需求文档"占用" = `used + reserved`(running + pending);闸 2 已正确减 `used + reserved`
- **问题 8:** §十四 + §5.4.1 改为 `allocated_gpu_count` 下调也用 `GREATEST(allocated_gpu_count, used+reserved)` clamp(对齐需求文档 §9.5.3,只回收空闲部分,不拒绝,不杀已有实例)
- **问题 9:** §7.3.3 补"工作负载类型取舍"说明。P0 用 Deployment + `schedulerName: volcano` + queue annotation(验证文档已证明可行),不用 Volcano Job CRD。Volcano Job 留给批任务(P0 不做)

**已改位置:**§5.4.1 + §十四 + §7.3.3
