# INSTANCE-STORAGE-USAGE-A：存储资源占用标记与过滤

> 状态：待实施
> 批次类型：Feature batch
> 涉及层：Core OpenAPI 契约 → Gateway handler → Console 前端展示
> 本文档是实施的唯一方案来源；实施会话不需要依赖本文档之外的历史对话上下文。
>
> **范围决策（2026-09-04 确认）**：本批次**不做后端创建预检（409 拦截）**，挂载冲突暂时由前端通过 `in_use=false` 过滤来控制；后端预检作为后续批次（见第 2.3 节）。

---

## 1. 背景与问题

### 1.1 事故证据（2026-09-03，dev 集群 10.10.1.66）

实例 `test-mount-filesystem2` 创建后一直卡在 provisioning，根因：

1. RWO 卷 `vol_0b7dbc7f`（ani-block，40GiB）已被其他实例的 Pod 占用；
2. 新实例被调度到另一节点，kubelet attach 阶段报 `Multi-Attach error`；
3. ANI 侧没有任何"卷已被占用"的判定与提示，用户只能看到无限 provisioning。

### 1.2 现状缺陷

| 缺陷 | 说明 |
|---|---|
| 无占用判定 | 实例创建（`resolveStorage`）和 `attach_volume` 生命周期动作都不检查资源是否已被其他实例引用 |
| 无占用可见性 | `/volumes`、`/filesystems` 列表接口没有 `in_use` 字段和过滤参数，前端无法在创建实例时过滤 |
| `mount_instance_id` 不可信 | 该字段只在显式挂载 API（`MountVolume`，storage_service.go）时写入；实例通过 `storage_attachments` 挂载**不更新它**。Console"挂载实例"列因此显示的是 mount name 而非实例标识 |
| 前端无从过滤 | 存储列表只有 `limit/cursor` 参数；"谁在用这个卷"只能靠实例详情接口反查，无法分页过滤 |

### 1.3 存储语义（方案的前提，不可动摇）

| 资源 | 后端 | PVC 访问模式 | 共享语义 | 渲染位置 |
|---|---|---|---|---|
| 卷（volume，ani-block） | Ceph RBD 裸块设备 | RWO（ReadWriteOnce） | 节点级独占；跨节点不可共享；产品层按"单实例独占"建模 | `pkg/adapters/runtime/storage_renderer.go`（`ReadWriteOnce`） |
| 文件系统（filesystem，NFS/CephFS） | 网络文件系统 | RWX（ReadWriteMany） | 多实例、跨节点共享 | 同上（`ReadWriteMany`） |

RWO 的 "Once" 是节点级 attach，不是 Pod 级。同节点多个 Pod 理论可共享，但实例调度落点用户不可控，同节点共享是巧合不是能力，因此**产品语义按"一个卷同一时刻最多被一个活跃实例使用"建模**。文件系统天然多挂，`in_use` 仅作信息展示。

---

## 2. 目标与非目标

### 2.1 本批次目标

1. **占用可见性**：`/volumes`、`/filesystems` 列表与详情响应内联返回 `in_use`（bool）和 `used_by`（占用实例列表）。
2. **过滤参数**：`GET /volumes?in_use=false|true`（`/filesystems` 同样支持，但创建场景只对卷使用）。
3. **单一真实来源**：占用判定 helper 独立成型，未来后端预检直接复用，规则不允许两处漂移。
4. **Console 修正**："挂载实例"列数据源切换到 `used_by`。
5. **前端控制**：创建实例弹窗的卷下拉请求 `?in_use=false`，由前端保证用户选不到被占用卷。

### 2.2 非目标（本批次明确不做）

- **后端创建预检（409 拦截）**：`resolveStorage` 与 `attach_volume` 不加占用检查，本批次后端行为与现状完全一致。已知代价：绕过前端（API 直调、并发请求、前端缓存过期）时仍会出现卡 provisioning，依赖 K8s Multi-Attach 兜底。这是已确认的过渡态，不是遗漏。
- K8s 节点感知（VolumeAttachment 查询 / nodeAffinity 强制同节点共享）——已评估并否决：resolver 是 provider-neutral 边界，且实例调度被锁死的代价大于收益。有共享需求引导用户使用文件存储。
- 并发创建的强一致互斥。可后续追加"观察链把 FailedAttachVolume 原因写入 instance status.reason"作为增强，不在本批次。
- 不修 `mount_instance_id` 的历史数据，不删除该字段（向后兼容）。

### 2.3 后续批次（预告，不在本批次实施）

- **INSTANCE-STORAGE-PRECHECK-B**（暂名）：把第 3 节判定 helper 接入三处路径，占用命中返回 409（错误信息带占用实例标识）。判定规则以本批次 helper 为唯一来源：
  1. `resolveStorage`（实例创建）
  2. `attach_volume` 生命周期动作
  3. **lifecycle `start` / `restart`**——覆盖以下真实场景（2026-09-04 评审发现）：实例 A running 时占用卷 X → A stop（占用释放）→ 新实例 B 创建并占用卷 X（attach 到 B 所在节点）→ A 再次 start/restart，Pod 重新调度且 **K8s 调度器不感知已有 VolumeAttachment**，落到不同节点即 FailedAttachVolume 卡 provisioning。此场景前端过滤与 create 预检都覆盖不到，必须在 start/restart 入口拦截，提示用户"卷正被实例 X 使用，请先停止该实例"。

---

## 3. 占用判定规则（单一真实来源）

**判定 helper**（建议放 `pkg/adapters/runtime`，与 resolver 同包）：

```go
// StorageResourceConsumers 返回租户下引用指定存储资源的活跃实例。
// 判定规则（本批次列表打标与后续 PRECHECK-B 预检共用，禁止各自实现）：
//   1. 实例的 StorageAttachments 中 resource_id 匹配目标资源；
//   2. 实例状态 ∈ {provisioning, running}；
//   3. stopped（replicas=0）、deleted、failed 等终态/非活跃状态不占用
//      （Deployment replicas=0 时 K8s 已释放 attach，事故验证过）；
//   4. 卷与文件系统使用同一判定规则；消费方语义由调用方决定——
//      本批次两者都仅用于展示与过滤，不做拦截。
func ListStorageConsumers(ctx context.Context, store ports.WorkloadInstanceStore,
    tenantID, resourceID string) ([]ports.StorageConsumer, error)
```

实现要点：

- `WorkloadInstanceStore.List(ctx, tenantID, kind)` 按 kind 查询（[workload_runtime.go#L875](file:///c:/ProgramProject/ChangQinYun/kuberai/ANI/ani-hotfix/repo/pkg/ports/workload_runtime.go#L875)），helper 内部遍历全部 kind（container / vm / batch_job / sandbox），合并结果。
- **列表打标禁止 N+1**：gateway handler 每页只调用一次"批量版"——把租户下所有活跃实例的 attachments 收集成 `map[resourceID][]consumer`，给当页资源逐个打标。为避免每页全量拉实例，允许 helper 提供 `ListAllConsumers(tenantID)` 批量形态；预检路径用单资源形态。
- 判定只依赖 ANI store（内存 + DB 兜底，沿用 `lookupVolumeRecord` 已建立的 store fallback 模式），不查 K8s。
- 已知局限（可接受，写入批次记录）：孤儿 K8s Deployment（ANI store 已无记录）不计入占用。这类残留靠运维清理，不阻塞本方案。

---

## 4. API 契约变更（先改契约，再改实现——项目强制规则）

契约文件：`repo/api/openapi/v1.yaml`（Core 唯一真实来源）。全部为 **additive** 变更，不破坏 v1 兼容性。

### 4.1 `/volumes`（约 L6172）与 `/filesystems`（约 L6475）的 list 操作

新增 query 参数（两个接口一致）：

```yaml
- { name: in_use, in: query, schema: { type: boolean }, description: "按占用状态过滤；省略时不过滤。占用判定：被本租户 provisioning/running 状态实例的 storage_attachments 引用" }
```

### 4.2 响应 schema

`StorageVolume`（约 L2240）与对应 filesystem schema 新增字段：

```yaml
in_use: { type: boolean, description: "是否被活跃实例占用" }
used_by:
  type: array
  description: "占用/引用该资源的实例列表；卷为独占占用，文件系统为共享引用"
  items:
    $ref: '#/components/schemas/StorageConsumerInfo'
```

新增共享 schema：

```yaml
StorageConsumerInfo:
  type: object
  required: [instance_id, instance_name, kind, state]
  properties:
    instance_id:   { type: string }
    instance_name: { type: string }
    kind:          { type: string, enum: [container, vm, batch_job, sandbox] }
    state:         { type: string, description: "实例状态（provisioning/running 等）" }
    mount_path:    { type: string, nullable: true, description: "挂载点，实例 attachments 中带出" }
```

### 4.3 错误语义

本批次无错误语义变更（不做 409 预检）。后续 PRECHECK-B 将复用现有 `Conflict` response，错误 message 格式届时约定：

```
volume vol_xxx is already attached to instance test-mount-filesystem2 (inst_fd237a44)
```

### 4.4 契约变更后的生成物

- 运行 `make gen-core-sdk`（如该目标存在则以 Makefile 实际目标为准）重新生成 core-schema / SDK；
- CI 有 OpenAPI Spec Lint 与生成物漂移检查，提交前必须本地跑过；
- `spec_id` 类 additive 字段变更不影响兼容性基线（`repo/api/core-v1-compatibility-baseline.yaml`）。

---

## 5. 实现落点

| # | 位置 | 改动 |
|---|---|---|
| 1 | `pkg/ports/`（storage_resources.go 或新文件） | 新增 `StorageConsumerInfo` port 结构；如需要，给 StorageService port 增加 consumers 查询方法 |
| 2 | `pkg/adapters/runtime/`（新 helper 或挂在 storage_service.go） | 占用判定 helper（第 3 节）：单资源形态 + 批量形态；数据源 `WorkloadInstanceStore`。**不做任何拦截** |
| 3 | `services/ani-gateway/internal/router/storage_resources.go` | list handler 解析 `in_use` 参数；每页批量打标 `in_use`/`used_by`；detail handler 单资源打标。handler 需拿到 `WorkloadInstanceStore`（gateway 装配处已持有，确认注入即可） |
| 4 | Console 前端（本仓库 services 之外，按前端团队流程） | 块存储列表"挂载实例"列改用 `used_by`；创建实例弹窗卷下拉改请求 `/volumes?in_use=false`；文件存储下拉不过滤，可显示"已被 N 个实例使用" |

> 实施会话注意：以上行号基于 2026-09-04 的 ani-hotfix 分支，动手前先 rg 重新定位。
> **明确不改**：`instance_resource_resolver.go`（resolveStorage）、`attach_volume` 执行路径——保持现状，后端拦截留给 PRECHECK-B。

---

## 6. 测试

### 6.1 单元测试（判定 helper + handler 两层）

判定 helper：

1. 卷被 running 实例引用 → 返回 1 个 consumer
2. 卷仅被 stopped（replicas=0）/ deleted 实例引用 → 空
3. 文件系统被多个 running 实例引用 → 全部返回
4. 跨 kind（container + vm 同时引用）→ 合并去重

gateway handler：

5. `?in_use=false` 只返回未占用卷；`?in_use=true` 只返回占用卷；省略返回全部
6. 响应含 `in_use` / `used_by`，used_by 内容与 helper 输出一致
7. `/filesystems` 同样支持参数与打标（文件系统不拦截，仅展示）
8. store 为空 / 实例无 attachments 时，所有资源 `in_use=false`、`used_by=[]`，接口不报错

### 6.2 真实环境验证（live gate，Sprint 5+ 强制）

环境：dev 集群 10.10.1.66（SSH / kubectl 可用，gateway NodePort 30080）。

1. 用 ani-block 卷 A 创建容器实例 1 → Running；`GET /volumes` 中卷 A `in_use=true`，`used_by` 含实例 1
2. stop 实例 1（replicas=0）→ 卷 A `in_use` 翻转为 `false`，`used_by` 为空
3. `GET /volumes?in_use=false` 包含卷 A；`?in_use=true` 不包含
4. 文件系统被实例挂载时 `in_use=true`；多实例挂载同一文件系统时 `used_by` 返回多条（RWX 共享展示正确）
5. （过渡态确认，非缺陷）创建实例 2 挂载仍被占用的卷 A：API 不拦截（与现状一致），最终由 K8s Multi-Attach 卡住——这是第 2.2 节确认的过渡行为；验证后删除实例 2 即可

---

## 7. 验收命令

```bash
cd repo
make test
make validate-architecture
git diff --check
# OpenAPI 生成物与契约漂移（以 Makefile 实际目标为准）
```

已知环境问题（与本批次无关，勿浪费时间排查）：Windows 本地 `make test-go` 存在 GOCACHE 环境变量传递问题与两个 symlink 相关测试失败（TestSandboxFileScriptsRejectSymlinks、AllowWorkspaceOperations），CI 通过即可。

---

## 8. 批次文档闭环（Feature batch 四件套，缺一不可）

1. `repo/development-records/INSTANCE-STORAGE-USAGE-A.md` — 实现与验证细节
2. `repo/development-records/README.md` — 批次索引
3. `repo/CURRENT-SPRINT.md` — 当前 Sprint 状态
4. `ANI-06-开发计划.md` Section 零 — 计划同步

---

## 9. 给实施会话的执行顺序建议

1. 读 `CLAUDE.md` → `ANI-DOCS-INDEX.md` → `repo/CURRENT-SPRINT.md`（强制加载顺序）；
2. 改 OpenAPI 契约（第 4 节）→ 重新生成 SDK/schema → 确认 lint 通过；
3. 实现判定 helper + 单测（第 3、6.1 节）——helper 独立成型，不做拦截，为 PRECHECK-B 留好复用口；
4. 接 gateway list/detail 打标与过滤（第 5 节 #3）；
5. 跑验收命令（第 7 节）；
6. 真实环境 live 验证（第 6.2 节）；
7. 补四件套批次文档（第 8 节）；
8. 提 PR 前按 `ANI-15-GitHub-协作规范与提交纪律.md` 执行（本批次触碰 API 契约与生成物，需 CODEOWNERS 共同审查）。
