# M1-K8S-K — K8s 集群记录落库（PostgreSQL 持久化）

完成日期：2026-09-20
对应分支：`hotfix/network-store-read`
验证结果：live verified（ani-test2 端到端：迁移应用 + 现场接管 + 网关重启后记录仍可见 + 第二集群 409；单测 + go build + 门禁全绿）。

## 背景

M1-K8S-J 把「同租户只允许一个 K8s 集群」这条约束做到了内存层 + provider 底座层，但它同时暴露了一个更根本的缺口：

**集群控制面记录此前只存在 gateway 进程内存。**

- 无 `k8s_clusters` 表；`localK8sClusterService.ListClusters` 直接遍历内存 map；
- 只有 `k8s_cluster_proxy_targets` 落 DB（proxy 目标，不是集群记录本身）。

后果是用户在 ani-test2 遇到的死局（用户原始报错）：

```text
创建失败
capability resource conflict: namespace ani-tenant-00000000-0000-0000-0000-000000000001
already hosts vCluster k8sclu-db2a33e2-79fc-4fed-b4b5-ec21804c3781;
only one vCluster per tenant is supported
```

链条：用户先前建的 `k8sclu-db2a33e2-…` 底座（Helm release + 控制面 sts）一直健康运行（live 实测 41h），但期间 gateway 有滚动重启 → 内存记录清空 → `GET /api/v1/k8s-clusters` 返回 `total=0`，**Console 看不到这个集群**。而 M1-K8S-J 的 provider 层守卫只看底座事实（这正是它跨重启有效的原因），于是：

- 界面：没有任何集群可操作（既看不到详情，也没有删除入口）；
- 创建：被底座守卫 409 拒绝，因为 namespace 里确实已经有一个 vCluster。

**用户既删不掉、也建不了新的**，且唯一的出路是有人手动 `helm uninstall`。

## 用户决策

| 问题 | 决策 |
|---|---|
| 「底座存在、Console 看不见」的集群怎么处理 | **记录落库（代码修复，更彻底）** |
| 落库范围 | **只做集群记录（推荐）**——节点池仍为内存，另批次处理 |
| ani-test2 现存 `k8sclu-db2a33e2-…` 怎么处理 | **接管成正式记录（推荐）**——一次性数据修复写入正式记录，Console 可见并可正常删除 |

## 实现了什么

### 1. 迁移：`k8s_clusters`

`repo/deploy/migrations/20260920120000_k8s_clusters.sql`：

- 主键 `(tenant_id, cluster_id)`；`tenants(id) ON DELETE CASCADE`；
- `state` CHECK 限定 `provisioning` / `running` / `deleting`（与 `ports` 常量和既有内存状态机一致）；
- **`UNIQUE (tenant_id)`**：把「每租户一个 vCluster」从产品/底座约束沉淀为数据库层不可绕过的约束，与 M1-K8S-J 的内存校验、provider 底座守卫形成三层防护；
- 两个**部分唯一索引**承载幂等键：`(tenant_id, create_idempotency_key)` 与 `(tenant_id, upgrade_idempotency_key)`（`WHERE ... IS NOT NULL`）——幂等键随行保存，网关重启后同键重放仍能返回原记录，而不是被 `UNIQUE (tenant_id)` 顶成冲突；
- `GRANT SELECT, INSERT, UPDATE, DELETE ... TO ani_app`；
- RLS 三策略（与 `network_security_group_rules` 等既有表同模式）：`k8s_clusters_platform_bypass`（`PERMISSIVE`）+ `k8s_clusters_self`（`PERMISSIVE`）两个放行策略，加一个 `tenant_isolation`（`RESTRICTIVE`）兜底；`ENABLE` + `FORCE ROW LEVEL SECURITY`。

`atlas.sum` 同步重算（58 → 59 条，既有条目 hash 全部不变）。

### 2. `ports.K8sClusterStore`

`pkg/ports/k8s_clusters.go` 新增能力接口（`UpsertK8sCluster` / `GetK8sCluster` / `ListK8sClusters` / `DeleteK8sCluster` / `FindK8sClusterByCreateIdempotencyKey` / `SetK8sClusterUpgradeIdempotency` / `FindK8sClusterByUpgradeIdempotencyKey`），按组件边界规则走 port + adapter，而不是让 service 直连 PG。

### 3. `MetadataK8sClusterStore`（PG adapter）

`pkg/adapters/runtime/k8s_cluster_metadata_store.go`：经 `ports.MetadataStore.WithTenantTx` 执行，tenant 上下文由 `types.WithTenant` 注入（RLS 生效前提）。

- `pgx.ErrNoRows` → `ports.ErrNotFound`；
- `pgconn.PgError.Code == "23505"` → `ports.ErrConflict`（消息含 `only one vCluster per tenant is supported`），与 M1-K8S-J 的冲突语义一致；
- 非 UUID tenant 早退 `ports.ErrInvalid`，不进入事务。

### 4. `localK8sClusterService` 双模式

`pkg/adapters/runtime/local_k8s_cluster_service.go` 增加 `clusterStore` 字段与 `WithK8sClusterStore` 选项：

- **未注入 store** → 完全保持原有内存行为（逐行未改），保证 local profile 与既有测试不受影响；
- **注入 store** → `GetCluster` / `ListClusters` 直接走 store；`CreateCluster` / `DeleteCluster` / `UpgradeCluster` 走新增的 store 模式私有方法。

store 模式的关键语义（与内存模式的失败回滚体例对齐）：

- `createClusterFromStore`：先查 create 幂等键（命中直接返回原记录），再 `ListK8sClusters` 校验同租户已有集群 → `ErrConflict`；然后写 `provisioning` 占位记录（此时 `UNIQUE (tenant_id)` 才真正兜底）→ provider apply → 成功写回 `running` + 写 proxy target；**失败则删除该占位记录**（释放租户槽位，不制造「API 不可见但 DB 占位」的新死局）；
- `deleteClusterFromStore`：置 `deleting` → provider 卸载 → 成功后删 proxy target 与集群记录；**provider 失败则恢复原状态**，不产生假成功；
- `upgradeClusterFromStore`：upgrade 幂等键命中即返回原记录；非 `running` 拒绝；成功后写回记录并记录 upgrade 幂等键。

### 5. gateway 接线

`services/ani-gateway/k8s_proxy_runtime.go`：`cfg.MetadataStore != nil` 时构造 `NewMetadataK8sClusterStore(cfg.MetadataStore)` 并注入 `WithK8sClusterStore`。有 `DATABASE_URL` 的部署（含 ani-test2 / ani-system）即自动落库，无 store 的 local profile 行为不变。

**未改 OpenAPI 契约、未改生成物**：`k8s_clusters` 是控制面内部记录，`K8sClusterRecord` 响应形状不变。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/migrations/20260920120000_k8s_clusters.sql` | 新增 | 表 + `UNIQUE (tenant_id)` + 两个幂等键部分唯一索引 + RLS 三策略 + GRANT + COMMENT |
| `deploy/migrations/atlas.sum` | 修改 | 58 → 59 条，既有条目 hash 不变 |
| `pkg/ports/k8s_clusters.go` | 修改 | 新增 `K8sClusterStore` 能力接口 |
| `pkg/adapters/runtime/k8s_cluster_metadata_store.go` | 新增 | PG 实现（tenant tx / ErrNoRows→ErrNotFound / 23505→ErrConflict） |
| `pkg/adapters/runtime/k8s_cluster_metadata_store_test.go` | 新增 | 10 用例（见下） |
| `pkg/adapters/runtime/local_k8s_cluster_service.go` | 修改 | `clusterStore` 字段 + 选项 + 读取直连 store + 三个写路径的 store 模式方法 |
| `pkg/adapters/runtime/local_k8s_cluster_service_store_test.go` | 新增 | 6 用例（见下） |
| `services/ani-gateway/k8s_proxy_runtime.go` | 修改 | 有 MetadataStore 即注入 cluster store |
| `services/ani-gateway/k8s_proxy_runtime_test.go` | 修改 | 新增「跨 service 实例仍可见」用例；PG fake 扩展支持 `k8s_clusters` 11 列读写与事件路由 |

## 完工标准达成

- [x] store 模式读取：`GetCluster` / `ListClusters` 从 DB 返回（`TestMetadataK8sClusterStoreReadsRecord` / `...ListsRecords`）
- [x] tenant 上下文注入（`...InjectsTenantContext`）+ 非 UUID tenant 早退 `ErrInvalid`（`...RejectsNonUUIDTenant`）
- [x] 唯一冲突映射为 `ErrConflict`（`...MapsUniqueViolationToConflict`）；缺失记录映射为 `ErrNotFound`（`...MapsMissingRecord`）
- [x] upsert SQL/参数形状（含空幂等键写成 NULL、时间戳转 UTC）（`...UpsertsRecord` / `...UpsertOmitsBlankIdempotencyKey`）
- [x] delete 与 upgrade 幂等键写入（`...DeletesRecord` / `...SetsUpgradeIdempotency` / `...FindsByCreateIdempotencyKey`）
- [x] **核心回归**：`TestLocalK8sClusterServiceStoreModeSurvivesServiceRecreation`——新建 service 实例并复用同一 store 后 `ListClusters` 仍可见，即「网关重启不失忆」
- [x] store 模式下同租户第二集群仍被拒（`...RejectsSecondCluster`）
- [x] 删除后槽位释放（`...ReleasesSlotAfterDelete`）
- [x] provider 删除失败恢复原记录、不假成功（`...RestoresRecordWhenProviderDeleteFails`）；apply 失败释放占位记录（`...DiscardsRecordWhenProviderApplyFails`）
- [x] upgrade 幂等（`...UpgradesClusterIdempotently`）
- [x] gateway 侧「跨 service 构造仍可见」（`TestGatewayK8sClusterServiceFromConfigPersistsClusterRecords`）
- [x] `go build ./pkg/... ./services/ani-gateway/...`、`go test`（仅剩既有 Windows sandbox 用例失败，见下）、`gofmt -l`、`make validate-architecture`、`make validate-doc-entrypoints`、`git diff --check` 通过
- [x] live：迁移应用 + 现场接管 + 网关重启后记录仍可见 + 第二集群 409

## live 验证（ani-test2）

隔离环境 ani-test2：PG pod `ani-test2-postgres-95fd5549f-cr2m2`（库/用户均为 `ani`），gateway NodePort 30083，租户 `tenant-a`（`ani-tenant-00000000-0000-0000-0000-000000000001`）。

该环境**没有 `atlas_schema_revisions` 表**（本就是手工 seed 出来的），因此迁移按环境既有做法用 `psql -f <migration> --single-transaction` 直接应用，不存在 atlas 版本漂移问题。落地方式为「本地写 .sql → SFTP 上传宿主 → `kubectl cp` 进 PG pod → `psql -v ON_ERROR_STOP=1 -f`」，以规避嵌套引号。

### 1. 落地前现场（只读探查）

| 观察点 | 事实 |
|---|---|
| `k8s_clusters` | 不存在 |
| `k8s_cluster_proxy_targets` | tenant-a 有 **2 条**：`k8sclu-57ca6dd4-…`（无底座残留）与 `k8sclu-db2a33e2-…` |
| 租户 namespace 底座 | Helm release 仅 `k8sclu-db2a33e2-…`（`deployed`）；sts `k8sclu-db2a33e2-…` **1/1 Running 41h**；`data-k8sclu-db2a33e2-…-0` PVC Bound |
| 其他 | `k8sclu-57ca6dd4-…` 无 release / 无 sts / 无 PVC，仅剩一条 proxy target 行 |

即：底座活着、API 不可见——正是本批次要解决的现场。

### 2. 迁移与接管

```text
psql -f 20260920120000_k8s_clusters.sql  → CREATE TABLE / CREATE INDEX ×4 / GRANT / ALTER ×2 / CREATE POLICY ×3
接管 INSERT                              → INSERT 0 1
```

回读结果：

```text
tenant_id | cluster_id                                  | state   | provider | real_provider | provider_refs
…000000000001 | k8sclu-db2a33e2-79fc-4fed-b4b5-ec21804c3781 | running | vcluster | t | ["vcluster/HelmRelease/k8sclu-db2a33e2-…"]
```

索引 5 条（含 `UNIQUE (tenant_id)` 与两个幂等键部分唯一索引）、RLS 3 条策略（2 `PERMISSIVE` + 1 `RESTRICTIVE`）与迁移定义一致。接管行 `reason` 明确标注其为一次性数据修复（记录此前只存网关进程内存）。

### 3. 网关重启不失忆（核心证据）

镜像 `docker.changqingyun.cn/ani/ani-gateway:test2-20260920-clusterstore`（只 `kubectl set image` + rollout，未改 env）：

```text
重启前  GET /api/v1/k8s-clusters → 200  total=1  （k8sclu-db2a33e2-…, state=running）
        POST /api/v1/k8s-clusters → 409 CONFLICT
          message = capability resource conflict: tenant already has k8s cluster
                    k8sclu-db2a33e2-79fc-4fed-b4b5-ec21804c3781; only one vCluster per tenant is supported

kubectl rollout restart deployment/ani-gateway -n ani-test2 → 成功

重启后  GET /api/v1/k8s-clusters → 200  total=1  （同一条记录，created_at/updated_at 不变）
```

对照 M1-K8S-J 现场同类重启后 `total=0`：**记录不再随进程消失**，界面「失忆」死局破除——用户现在能看到该集群，并可通过 `DELETE /api/v1/k8s-clusters/{cluster_id}` 正常删除。

### 4. 未在 live 执行的部分

**删除路径未在 live 跑**：tenant-a 那个 `db2a33e2` 是用户 41h 的运行中 vCluster，`DELETE` 会真实 `helm uninstall` 卸载它。删除路径的正确性由 store 模式单测覆盖（记录删除 + proxy target 清理 + 槽位释放 + provider 失败恢复原状态），未在 live 以「卸载用户现存集群」为代价换取证据。

## 遗留

- **节点池仍未落库**（用户明确只做集群记录）：gateway 重启后 `GET /k8s-clusters/{id}/node-pools` 仍会清空，与本批次同类问题，需独立批次。
- 迁移在本环境是**手工 psql 应用**（该环境无 `atlas_schema_revisions`）：正式环境首次启用该表须走 atlas 迁移流程。
- `k8s_cluster_proxy_targets` 中 tenant-a 的 `k8sclu-57ca6dd4-…` 残留行（无底座）未清理——它不构成约束冲突（proxy target 无「每租户唯一」语义），且超出本批次范围。
- M1-K8S-J 遗留的「provider apply 失败在底座留下孤儿 release」在本批次仍有：store 模式失败会删除占位记录（不再产生「DB 占位但 API 不可见」的新问题），但底座侧孤儿仍需 `helm uninstall` 或后续 provider 回滚批次解决。
- 既有（与批次无关）的 Windows 本地测试失败：`TestSandboxFileScriptsRejectSymlinks`（无 symlink 权限）、`TestSandboxFileScriptsAllowWorkspaceOperations`（无 shell，exit 9009）。