# M1-K8S-I — K8s 集群删除真正释放 vCluster 资源与创建期锁范围收窄

完成日期：2026-09-18
对应分支：`hotfix/network-store-read`
验证结果：live verified（ani-test2 与 ani-system 双环境端到端；单测 + go build + validate-architecture）。用户现场问题已通过环境侧清理恢复，代码修复实机证据见下文 §live 验证。

## 背景

用户实测反馈：在 Console 界面创建 K8s 集群后长时间不完成，且期间集群列表「一直在加载」。

现场定位到三个独立事实，前两个是代码缺陷，第三个是环境前提：

| # | 现象 | 性质 |
|---|---|---|
| 1 | 用户新建的集群 `k8sclu-c447cc9b-…` 控制面 StatefulSet `0/1` CrashLoopBackOff，syncer 日志 `there is already a virtual cluster in namespace ani-tenant-00000000-…-000000000001; creating multiple virtual clusters inside the same namespace is not supported` | 环境前提（vcluster 硬约束）+ 缺陷 1 的后果 |
| 2 | 租户 namespace 内残留 4 个 `data-k8sclu-*` PVC，其中两个（`57ca6dd4`、`66b589b9`）对应 Helm release 已不存在 | 缺陷 1 的直接证据 |
| 3 | 创建请求在飞行中时，`GET /api/v1/k8s-clusters` 被阻塞；控制台出现 `499`（浏览器放弃）与 token 过期 `401` | 缺陷 2 |

### 缺陷 1：`DeleteCluster` 不释放底座资源

全仓库此前**没有任何 `helm uninstall` 调用**。`localK8sClusterService.DeleteCluster` 只做两件事：内存记录置 `deleting`、删除 `k8s_cluster_proxy_targets` 代理目标行。vCluster 的 Helm release、控制面 StatefulSet/Service/Secret 与数据卷全部留在租户 namespace 里。

这与 OpenAPI 契约不符：`deleteK8sCluster`（`repo/api/openapi/v1.yaml`）声明的语义是「删除 K8s 集群」，返回 `K8sCluster`；下游副作用要求「删除即释放」。

后果链：用户在界面删除集群 → 界面显示删除成功 → 底座 vcluster 仍在 → 再次创建集群 → 命中 vcluster「同 namespace 只允许一个虚拟集群」约束 → 新集群 CrashLoop、界面永远等不到可用状态。

> M1-K8S-H 记录中的遗留结论「ANI 当前没有 K8s 集群删除 API，缺清理入口」**不准确**：`DELETE /api/v1/k8s-clusters/{cluster_id}` 路由与 handler 早已存在（`services/ani-gateway/internal/router/k8s_cluster_resources.go`），缺的是删除动作里的 provider 卸载能力。本批次修正该结论。

### 缺陷 2：创建全程持有服务级互斥锁

`CreateCluster` 以 `s.mu.Lock(); defer s.mu.Unlock()` 覆盖整个方法体，而 provider apply（`helm upgrade --install` + `vcluster connect --print`）就在锁内执行，实测单次 30~39s（见 M1-K8S-H live 证据）。`GetCluster` / `ListClusters` / `DeleteCluster` 共用同一把 `s.mu`，于是创建期间所有集群读请求被串行阻塞——这正是界面「查询一直在加载」的机制性原因。

## 实现了什么

### 新增 provider 卸载能力（缺陷 1 前半）

- `pkg/ports/k8s_clusters.go`：新增 `K8sClusterProviderDeleteRequest`（`TenantID`/`ClusterID`/`Name`）、`K8sClusterProviderDeleteResult`（`Deleted`/`Provider`/`ResourceRefs`/`Reason`/`DeletedAt`）与接口 `K8sClusterProviderDelete`。
- `pkg/adapters/runtime/vcluster_helm_provider.go`：新增 `DeleteK8sCluster` → `helm uninstall <clusterID> --namespace <tenant-namespace> --ignore-not-found`。`--ignore-not-found` 令「release 已不存在」按成功处理，删除动作因此可重放。

### 删除路径真正释放资源（缺陷 1 后半）

`localK8sClusterService.DeleteCluster` 重写为「短锁 + 长操作」：

1. 锁内校验归属、置 `deleting`、锁内复制 provider 引用后 **解锁**；
2. 对 `RealProvider` 记录调用 `providerDelete.DeleteK8sCluster`（真实 `helm uninstall`）；
3. 删除 proxy target（原有行为）；
4. `purgeClusterIndexes` 清掉集群记录、`idem`（按 clusterID 反查）与 `upgradeIdem` 中的关联条目。

失败语义：provider 卸载失败或返回 `Deleted=false` 时，`restoreClusterAfterFailedDelete` 把记录状态**回滚为 `running`**，proxy target 保留，接口返回错误——租户可原样重试，不会出现「界面上删了、底座还在、又无法再删」的死局。

`purgeClusterIndexes` 同时消除一个隐患：原实现删除后 `idem` 仍指向已消失的集群 ID，同幂等键重放会命中 `s.byID[id]` 的零值记录并静默返回空对象。

### 创建期锁范围收窄（缺陷 2）

`CreateCluster` 改为：锁内做校验 + 幂等命中检查 + 注册**占位记录**（`provisioning`）→ 解锁 → 执行 provider apply 与 proxy target 写入 → 重新加锁写回 `running`。创建期间 `ListClusters`/`GetCluster` 立即可返回，界面能看到一条 `provisioning` 记录而不是无限加载。

占位状态取值：provider apply 已配置时用 `provisioning`（端口常量与 OpenAPI `K8sCluster.state` enum 中均已存在），未配置 provider 的 local profile 仍直接为 `running`；失败路径统一通过新增的 `discardClusterRecord` 原子摘除占位记录与幂等键，重试语义与改动前一致。

未改 OpenAPI 契约、未改生成物、无 DB 迁移。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/k8s_clusters.go` | 修改 | 新增 `K8sClusterProviderDeleteRequest/Result` 与 `K8sClusterProviderDelete` 接口 |
| `pkg/adapters/runtime/vcluster_helm_provider.go` | 修改 | 新增 `DeleteK8sCluster` + `helmUninstallArgs` + `validateK8sClusterProviderDeleteRequest` + 接口断言 |
| `pkg/adapters/runtime/local_k8s_cluster_service.go` | 修改 | 新增 `WithK8sClusterProviderDelete` 选项与 `providerDelete` 字段；`CreateCluster` 锁范围收窄 + `provisioning` 占位；`DeleteCluster` 调 provider 卸载并清理索引；新增 `discardClusterRecord`/`restoreClusterAfterFailedDelete`/`purgeClusterIndexes` |
| `pkg/adapters/runtime/vcluster_helm_provider_test.go` | 修改 | 新增 `TestVClusterHelmProviderAdapterUninstallsHelmRelease` |
| `pkg/adapters/runtime/local_k8s_cluster_service_test.go` | 修改 | 新增 3 用例（见下）+ `fakeK8sClusterProviderDelete`、`blockingK8sClusterProviderApply` 测试基建 |
| `services/ani-gateway/k8s_proxy_runtime.go` | 修改 | `vcluster_helm` 分支接线 `WithK8sClusterProviderDelete(provider)` |

## 完工标准达成

- [x] 删除走 provider：`helm uninstall <clusterID> --namespace <ns> --ignore-not-found` 参数断言（`TestVClusterHelmProviderAdapterUninstallsHelmRelease`）
- [x] 删除后集群记录、proxy target、幂等索引全部清理，同幂等键可重新创建（`TestLocalK8sClusterServiceDeletesClusterThroughProvider`）
- [x] provider 卸载失败时记录回滚 `running`、proxy target 保留（`TestLocalK8sClusterServiceRestoresClusterWhenProviderDeleteFails`）
- [x] provider apply 在飞行中时 `ListClusters` 不被阻塞且能看到 `provisioning` 记录（`TestLocalK8sClusterServiceListsClustersWhileProviderApplyIsRunning`）
- [x] `go build ./pkg/... ./services/ani-gateway/...`、全量 Go 单测、`make validate-architecture`、`gofmt -l`、`git diff --check` 通过
- [x] live：创建期间并发读不阻塞且列表可见 `provisioning`（ani-test2 67 次读最大 0.034s / ani-system 84 次读最大 0.084s）
- [x] live：`DELETE` 后 Helm release 与控制面 sts/svc/secret 真实消失（ani-test2 与 ani-system 双环境）
- [x] live：删除后再次创建控制面 Pod 1/1 Running，不再出现 `there is already a virtual cluster in namespace …`
- [x] 用户现场 namespace 恢复干净（见 §现场恢复），本次验证新增的 chart 遗留 PVC 亦已手工清除

## 现场恢复

清理前租户 namespace `ani-tenant-00000000-0000-0000-0000-000000000001` 残留：2 个 Helm release（`k8sclu-49f7371a-…`、`k8sclu-c447cc9b-…`）、2 个 StatefulSet、4 个 `data-k8sclu-*` PVC。

恢复步骤（本批次代码修复前的等效手工操作，新代码部署后由 `DELETE /k8s-clusters/{id}` 承担）：

1. `helm uninstall <release> --namespace <tenant-namespace>` 逐个卸载（本次两个 release）；
2. `kubectl delete pvc data-k8sclu-… -n <tenant-namespace>` 删除 Helm 不管的 chart 遗留卷；
3. 清理 `k8s_cluster_proxy_targets` 中指向已卸载 release 的孤儿行；
4. 重启网关 Pod（集群记录目前仍是进程内存，重启即清空幽灵记录）。

清理后 namespace 内 `sts/pod/svc/secret/pvc` 已无 `k8sclu` 相关对象。

## live 验证

镜像 `docker.changqingyun.cn/ani/ani-gateway:test2-20260918-clusterdel`（digest `sha256:de5d65a3…5976`），ani-system 用同产物 retag `fix-20260918-clusterdel`（digest 一致），两个环境均只 `kubectl set image`，未改 env、未改配置。租户 `tenant-a`（`admin`）经 `POST /api/v1/auth/password/login` 取 token 后走真实 HTTP 路径。

### ani-test2（NodePort 30083）

创建（缺陷 2 主证据）：

```text
POST /api/v1/k8s-clusters -> 201  elapsed=34.71s
  state=running  reason="vCluster Helm release applied"  dev_profile.real_provider=true
期间并发 GET /k8s-clusters：67 次全部 200，最大延迟 0.034s
  t+0.57s  http=200 latency=0.025s total=1 states=['provisioning']
  …（此后每次 ~0.5s 一次，latency 均 0.005~0.034s，states 恒为 provisioning）
```

改动前该窗口内 `GET /k8s-clusters` 与创建共用 `s.mu`，实测被阻塞到请求超时；现在 34.71s 的 apply 全程读接口维持在几十毫秒，且列表从 t+0.57s 起就能看到 `provisioning` 记录——界面不再无限加载。

底座与删除（缺陷 1 主证据）：

```text
创建后：helm list -> k8sclu-013ba1a2-…  deployed  vcluster-0.34.1
        sts 1/1、svc 3 个、secret 3 个、pvc data-k8sclu-013ba1a2-…-0 Bound
DELETE /api/v1/k8s-clusters/k8sclu-013ba1a2-… -> 200 elapsed=1.08s
        state=deleting  reason="vCluster Helm release uninstalled"
删除后：helm list -> 空；sts/svc/secret 中 k8sclu 相关对象全部消失
        同一租户 GET /k8s-clusters -> total=0，cluster still listed: False
```

用户原始场景回归（残留 release 是否仍会阻塞新集群）：

```text
再次 POST /api/v1/k8s-clusters -> 201 elapsed=33.16s state=running
  sts k8sclu-e40fc3b7-… 1/1，pod k8sclu-e40fc3b7-…-0 1/1 Running
```

对照改动前：残留 release 会让新集群 syncer CrashLoopBackOff 并报 `there is already a virtual cluster in namespace …`；本次删除后重建控制面 Pod 直接 1/1 Running，无该错误。

### ani-system（NodePort 30080，镜像形态非 hostPath）

```text
POST /api/v1/k8s-clusters -> 201 elapsed=43.86s state=running
期间并发 GET /k8s-clusters：84 次全部 200，最大延迟 0.084s，t+0.53s 起 states=['provisioning']
DELETE /api/v1/k8s-clusters/k8sclu-954dbe36-… -> 200 elapsed=1.18s reason="vCluster Helm release uninstalled"
删除后 helm list 空、sts/svc/secret 中 k8sclu 对象消失、GET /k8s-clusters total=0
```

### 遗留对象清理

每次删除后租户 namespace 内仍留 1 个 chart 自身创建的 `data-k8sclu-<cluster_id>-0` PVC（该对象不在 Helm release 内，`helm uninstall` 不带），本次 3 个已手工 `kubectl delete pvc` 清除，验证结束时 namespace 内无 `k8sclu` 相关对象。这是既有遗留项（见下）在 live gate 中的再次确认，非本次回归。

## 遗留

- **一个租户 namespace 只能有一个 vCluster**：`proxyServerTemplate` 等服务端模板按 `{cluster_id}.{namespace}` 派生，暗示「一个 namespace 承载多个集群」，但 vcluster syncer 明确拒绝同 namespace 多虚拟集群。当前实现按租户 namespace 创建，因此同一租户创建第二个集群必然 CrashLoop。彻底解决需要为每个集群分配独立 namespace（涉及命名/配额/网络注解渲染），属独立设计变更，不在本批次。
- **集群记录仍是进程内存**：网关重启会丢失集群记录（底层 Helm release 保留），此时 `DELETE /k8s-clusters/{id}` 返回 404，只能手工 `helm uninstall`。集群记录落库是独立批次。
- 删除只卸载 Helm release 与 proxy target；该集群的 node pool 记录仍留在内存（集群记录消失后不可达，但也不会被清理）。
- chart 自身创建的 `data-k8sclu-<cluster_id>-0` PVC 不属于 Helm release，`helm uninstall` 不会带走它（live gate 三次删除各留 1 个，本次手工清除）。后续批次可评估在删除路径中按 clusterID 前缀清理该 PVC，或改为由 chart 值控制卷生命周期。
- vcluster chart 自身创建的 coredns Pod 仍 `0/1`（`10.60.1.7`，M1-K8S-H 遗留项，不影响本次删除路径）。