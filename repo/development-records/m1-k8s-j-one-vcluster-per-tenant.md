# M1-K8S-J — 同租户只允许一个 K8s 集群（内存校验 + provider 层 namespace 守卫）

完成日期：2026-09-20
对应分支：`hotfix/network-store-read`
验证结果：live verified（ani-test2 端到端 409 路径；单测 + go build + validate-architecture）。正向 201 路径未在 live 重跑，由单测与 `helm list` 空 namespace 实测覆盖（见 §live 验证）。

## 背景

用户提问：**「创建集群，同一个租户下只能有一个，这个符合产品语义吗？」**（用户在 Console 试建第二个 K8s 集群，请求约 10 分钟后以 400 失败）。

结论：**符合产品语义，且这是当前实现必须显式拒绝的约束**，依据两条：

1. **产品侧**：`ANI-02-产品边界与职责.md` §2.1.2 定义 v1.0.0 多租户隔离方案为「每租户一个 vCluster」，租户间网络物理隔离交由 KubeOVN VPC 承载；高隔离方案（Metal3 + Cluster API）属 Phase 2 延期能力。
2. **底座侧**：vcluster 硬约束——同一 namespace 只能存在一个虚拟集群。syncer 在发现同 ns 已有 vcluster 时 fatal：

```text
there is already a virtual cluster in namespace <ns>; creating multiple virtual clusters inside the same namespace is not supported
```

而当前实现的集群全部落在租户 namespace（`tenantNamespace(tenantID)` → `ani-tenant-<tenantID>`），因此「同租户第二个集群」不只是产品语义问题，在建底座时必然失败。

## 现场事实（改动前）

ani-test2，租户 `tenant-a`（`ani-tenant-00000000-0000-0000-0000-000000000001`）：

| 观察点 | 事实 |
|---|---|
| API 侧 | 只有 1 个集群 `k8sclu-db2a33e2-…`（`running`，09-18 09:51 创建） |
| 底座侧 | namespace 内有 **2 个** vcluster Helm release：`db2a33e2`（sts 1/1）与 `f15902f3`（**0/1 CrashLoopBackOff**，syncer 报上文的 `there is already a virtual cluster`） |
| 请求日志 | `POST /api/v1/k8s-clusters` 于 09-20 02:15:35 发起，**`latency_ms=604939`（≈10 分钟）后返回 400** |

三个独立事实叠加成一条缺陷链：

1. `CreateCluster` 没有任何同租户唯一性校验，第二个集群被放行进入 provider apply；
2. helm install 约 2 秒即建好 StatefulSet，随后 `vcluster connect --print` 等控制面就绪，syncer 因同 ns 已有 vcluster 而 CrashLoop → 等满超时 → provider apply 失败；
3. provider apply 失败走 `discardClusterRecord` 摘掉刚登记的记录，**但底座 Helm release 保留** → 产生 API 不可见的孤儿 release，无法通过界面删除，永久毒化该 tenant namespace（后续每次创建都重复上述 10 分钟失败）。

此外，**集群记录目前只存在 gateway 进程内存**（无 `k8s_clusters` 表，`ListClusters` 直接遍历内存 map；只有 `k8s_cluster_proxy_targets` 落 DB）。gateway 每次滚动重启记录即清空（live 实测重启后 `GET /k8s-clusters` `total=0`），因此**仅靠内存校验无法维持这条约束**——重启后残留的底座 release 会让第二个集群再次走完整条失败链。

## 实现了什么

按用户决策采用「内存校验 + provider 层 namespace 级检查」两层：

### 内存层：同租户唯一性校验

`localK8sClusterService.CreateCluster` 在锁内、**幂等键命中检查之后**新增同租户扫描：命中已有记录（含 `deleting`）即返回 `ports.ErrConflict`。顺序很关键——幂等重放必须优先返回原集群，不能被新校验改写成 409。

### provider 层：底座级守卫（跨重启有效）

`VClusterHelmProviderAdapter.ApplyK8sCluster` 在 `helm upgrade --install` **之前**新增 `ensureNamespaceHostsSingleVCluster`：执行 `helm list --namespace <tenant-ns> --output json`，若该 namespace 已有**非本次同名**的 vcluster release，返回 `ports.ErrConflict`（不进入安装）。

判定细节：

- 以 Helm release 的 chart 名是否含 `vcluster` 区分，namespace 内的非 vcluster release（如 `metrics-server`）不误伤；
- release 名与本次要安装的 `clusterID` 相同时放行，保证 provider apply 本身可重放；
- `helm list` 输出为空（namespace 不存在 / 无 release）时放行，正常首个集群路径不受影响；
- `helm list` 解析失败返回 `ports.ErrInvalid`，不静默放行。

`writeK8sClusterError` 既有映射把 `ErrConflict` 转为 **409 CONFLICT**（`POST /k8s-clusters` 契约本就声明 409），因此**未改 OpenAPI 契约、未改生成物、无 DB 迁移**。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/local_k8s_cluster_service.go` | 修改 | `CreateCluster` 锁内新增同租户已有集群校验（幂等命中优先），命中返回 `ports.ErrConflict` |
| `pkg/adapters/runtime/vcluster_helm_provider.go` | 修改 | `ApplyK8sCluster` 安装前调用新守卫 `ensureNamespaceHostsSingleVCluster`；新增 `helmListArgs`、`firstForeignVClusterRelease` |
| `pkg/adapters/runtime/vcluster_helm_provider_test.go` | 修改 | fake runner 支持 `helm list`（`listOutput`，默认 `[]`）+ 断言序号适配；新增 3 用例（见下） |
| `pkg/adapters/runtime/local_k8s_cluster_service_test.go` | 修改 | 新增 3 用例（见下） |
| `services/ani-gateway/k8s_proxy_runtime_test.go` | 修改 | gateway 侧 fake runner 响应 `helm list`，适配新增调用 |

## 完工标准达成

- [x] 同租户第二个集群被拒：`ErrConflict`（`TestLocalK8sClusterServiceRejectsSecondClusterForTenant`）
- [x] 同幂等键重放仍返回原集群，不被新校验改写（同上用例）
- [x] 其他租户不受影响、`ListClusters` 仍只有原集群（同上用例）
- [x] 删除后可再次创建（租户槽位释放，`TestLocalK8sClusterServiceAllowsNewClusterAfterTenantClusterDeleted`）
- [x] provider apply 失败后租户槽位释放、可换幂等键重试（`TestLocalK8sClusterServiceReleasesTenantSlotWhenProviderApplyFails`）
- [x] 底座已有 foreign vcluster release 时安装前即拒绝，且不执行 helm install（`TestVClusterHelmProviderAdapterRejectsVClusterAlreadyInTenantNamespace`：仅 1 次 `helm list` 调用、无 `upgrade`）
- [x] namespace 内非 vcluster release 不误伤（`TestVClusterHelmProviderAdapterIgnoresForeignNonVClusterRelease`：`list` → `upgrade` 放行）
- [x] 同名 release 重放放行（`TestVClusterHelmProviderAdapterAllowsReapplyOfSameReleaseName`）
- [x] `go build`、全量 Go 单测、`make validate-architecture`、`make validate-doc-entrypoints`、`gofmt -l`、`git diff --check` 通过
- [x] live：同租户第二个集群创建返回 409，且不再出现 10 分钟挂起

## 现场清理

按用户批准清理 ani-test2 `tenant-a` 的残留（清理后 namespace 仅剩 `db2a33e2`）：

1. `kubectl exec -n ani-test2 deploy/ani-gateway -- helm uninstall k8sclu-f15902f3-… --namespace ani-tenant-00000000-0000-0000-0000-000000000001 --ignore-not-found`（CrashLoop 孤儿 release）；
2. 删除 chart 遗留的孤儿卷 `data-k8sclu-3181c07e-…-0` 与 `data-k8sclu-f15902f3-…-0`（`helm uninstall` 不带走的 PVC，M1-K8S-I 已记录的既有遗留）。

清理后 `helm list` 仅 `db2a33e2` 一条、`data-k8sclu-*` PVC 仅 `db2a33e2` 一个。

## live 验证

镜像 `docker.changqingyun.cn/ani/ani-gateway:test2-20260920-onecluster`（ani-test2 gateway Deployment，只 `kubectl set image` + rollout，未改 env、未改配置；pod `ani-gateway-555d5b9d7d-lgv7q` 1/1 Running / restart 0）。租户 `tenant-a`（`admin`）经 `POST /api/v1/auth/password/login` 取 token 后走真实 HTTP 路径。

### helm 层守卫（安装前判定）

```text
不存在的 namespace -> helm list -> []
tenant-a namespace -> helm list ->
  [{"name":"k8sclu-db2a33e2-79fc-4fed-b4b5-ec21804c3781","status":"deployed","chart":"vcluster-0.34.1"}]
```

即首个集群（空 namespace）路径不被误伤，已有 vcluster 的 namespace 能给出权威判定。

### API（用户原始场景回归）

网关重启后内存记录已清空（`GET /api/v1/k8s-clusters` → `count=0`），正是「纯内存校验失效」的现场：此时再创建第二个集群，必须由 provider 层守卫兜住。

```text
GET  /api/v1/k8s-clusters -> count=0
POST /api/v1/k8s-clusters -> 409 CONFLICT  latency_ms=82
POST /api/v1/k8s-clusters -> 409 CONFLICT  latency_ms=139   （两次不同幂等键）
message = capability resource conflict: namespace ani-tenant-00000000-0000-0000-0000-000000000001
          already hosts vCluster k8sclu-db2a33e2-79fc-4fed-b4b5-ec21804c3781;
          only one vCluster per tenant is supported
```

对照改动前同一场景：`latency_ms=604939`（≈10 分钟）后 400，并在 namespace 内留下 CrashLoop 孤儿 release。现在两次请求均在百毫秒级被拒，且**无部分安装**：拒绝后 namespace 内 `k8sclu` 相关对象数量与验证前一致（Helm release、StatefulSet 均未新增）。

### 未在 live 重跑的断言

正向 201 路径未在 live 重跑——那需要先删除 tenant-a 现有集群或再装一个真实 vcluster，代价与风险都超出本次验证目的。该路径由以下证据覆盖：单测（首建放行、同名重放放行、非 vcluster release 不误伤）+ live `helm list` 对不存在/空 namespace 返回 `[]` 的实测。

## 遗留

- **provider apply 失败仍会留下底座孤儿 release**：本次只做到「不产生新孤儿」，未给失败路径加 provider 回滚（用户明确未选该方案）。既有孤儿只能靠 `helm uninstall` 手工清理，因为 API 侧没有对应记录、界面无法删除。彻底解法是失败路径回滚 provider apply，或把集群记录落库后提供「列出底座孤儿」入口。
- **集群记录仍是进程内存**（无 `k8s_clusters` 表）：网关重启后界面看不到任何集群，但底座 release 仍在——这才是本次约束必须做在 provider 层的原因。集群记录落库是独立批次。
- 同 ns 只能存在一个 vCluster（vcluster 硬约束）：本批次按产品语义显式拒绝同租户第二个集群，未改为「每集群独立 namespace」方案（后者涉及命名/配额/网络注解渲染，属独立设计变更）。
- 拒绝语义按「同租户全部集群记录」判定，包含 `deleting` 状态的记录：即删除未收敛期间不允许建新集群，租户需等待删除完成。
- vcluster 内同步出的 coredns Pod 仍 `0/1`（M1-K8S-H 遗留项，未深究，不影响本批次路径）。