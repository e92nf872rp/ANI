# INSTANCE-VM-START-AND-NETWORK-ROUTE-A — VM 创建后误判 stopped + 子网 OVN 逻辑交换机缺失

> 分支：`hotfix/vm-start-and-network-route`（worktree `ani-hotfix-vm-start`，基于 `origin/main` 0de02fcd）
> 日期：2026-10-09
> 状态：代码修复已实现并单测通过；已在 `ani-test2` 隔离环境完成热部署与功能验证，并已替换 `ani-system` gateway 镜像
> 镜像：`docker.changqingyun.cn/ani/ani-gateway:test2-20261009-vmstart`（两环境同镜像）
> 来源：用户报障「10.10.1.66 上创建的虚拟机无法启动」；另一份《VM 创建后立即 stop 问题排查交接》已独立记录问题 A 的部分证据

---

## 0. 结论速览

同一现象由**两个相互独立**的缺陷叠加造成，必须分开修：

| 编号 | 类别 | 根因 | 影响 |
|---|---|---|---|
| **A** | 产品代码缺陷 | 创建时的同步观测把「VM 刚 apply、VMI 尚未生成」的瞬时态按 CRD 默认值 `printableStatus=Stopped` 写成终态 `stopped`，且此后不可自愈 | 任何 VM 创建后立即显示 `stopped`，且永远停在 stopped |
| **B** | ANI 代码缺陷（引发集群故障） | 路由渲染把未经校验的用户自由文本写进 Kube-OVN `Vpc.spec.staticRoutes[].nextHopIP`；同一个 fieldManager 的 SSA 又顺带删掉了 `spec.namespaces` | kube-ovn `format vpc` 永久失败 → 该 VPC 之后新建的子网永远拿不到 OVN 逻辑交换机 → 落在其上的 VM/容器 Pod 起不来 |

A 决定「显示成什么」，B 决定「能不能真的跑起来」。只修 A，状态会从错误的 `stopped` 变成 `provisioning` 但永远到不了 `running`；只修 B，VM 能真正 Running，但 ANI 状态仍显示 `stopped`。

---

## 1. 问题 B：子网 OVN 逻辑交换机缺失

### 1.1 现象与证据

用户实例 `55555`（`inst_e8dd4aab-…`，租户 `00000000-…-000000000001`，子网 `subnet_2c430138-…` = `10.99.2.0/24`）：

```
kubectl get vm 55555    -> STATUS=Starting   （74 分钟不变）
kubectl get vmi 55555   -> PHASE=Scheduling
pod virt-launcher-55555-hvldr -> Init:0/2 ，事件：
  CreateOVNPortFailed ... not found logical switch
    "subnet-subnet-2c430138-5808-42d4-9a22-2d369ddcb5dd"
```

kube-ovn-controller 日志：

```
E ... subnet.go:336] namespace 'ani-tenant-00000000-…-000000000001' is out of range
                     to custom vpc 'vpc-vpc-68633c00-…', requeuing
E ... vpc.go:288] failed to format vpc vpc-vpc-68633c00-…: invalid next hop ip "test"
```

直接查 OVN NB：`subnet-subnet-2c430138-…` 的 logical switch **不存在**（同 VPC 里更早创建的 `4de8a1c2`/`c6285f72` 存在）。

### 1.2 根因（两处 ANI 缺陷叠加）

**① 非法 `nextHopIP` 打挂整个 VPC 的 reconcile。**
`KubeOVNNetworkRenderer.RenderRoute` 只校验 `next_hop_id` 非空，然后原样写进 `Vpc.spec.staticRoutes[].nextHopIP`。ANI 库里该 VPC 的路由记录 `rt_11634e69-…` 的 `next_hop_id` 是字面量 `"test"`（非 IP），于是 kube-ovn `format vpc` 直接失败并持续 requeue。**VPC 一旦 format 失败，其子网列表就不再更新，之后新建的子网不会被登记进 VPC、也不会创建 logical switch**——这解释了为什么老的子网正常、只有 09-18 之后新建的挂掉。

**② 同一次 apply 把 `spec.namespaces` 删掉。**
`RenderVPC` 渲染的 `Vpc` 带 `spec.namespaces`，而 `RenderRoute` 渲染的是**同一个 `Vpc` 对象、但 spec 里只有 `staticRoutes`**；两者经 `KubernetesRESTClient.ApplyManifests` 用**同一个 fieldManager**（`ani-gateway-sprint13-production-shaped`）做 server-side apply。SSA 语义下「我上次声明过、这次没声明」的字段会被删除，所以 `spec.namespaces` 被路由 apply 清空，子网侧随即报 `namespace is out of range to custom vpc`，形成第二个阻塞点。

对照健康 VPC 都有 `spec.namespaces`，只有被写过路由的那批没有。

### 1.3 影响面（全集群扫描）

`spec.namespaces` 被清空的 VPC 共 6 个；其中 5 个还带非法 `nextHopIP`（`100`/`123`/`212`）：

| VPC | 非法 nextHop | 子网 LS 状态 |
|---|---|---|
| `vpc-vpc-68633c00-…` | `test` | `2c430138` 缺失（本次报障） |
| `vpc-vpc-dfeb8ab2-…` | `123` | `18c2e193` 缺失 |
| `vpc-vpc-42ade19c-…` | `100` | 早于路由创建，暂时存在但 VPC 已坏 |
| `vpc-vpc-4cfbca52-…` | `123` | 同上 |
| `vpc-vpc-dd88c28f-…` | `123` | 同上 |
| `vpc-vpc-3998ab50-…` | 无（路由为合法 `10.202.0.10`） | 无子网 |

ANI 库中 `state=available` 且 `next_hop_id` 非法的 gateway 路由共 4 条（含 `rt_09519fd9-…`）。

### 1.4 集群侧止血（已执行）

1. 6 个 VPC 用 merge patch 补回 `spec.namespaces=[ani-tenant-<tenant>]`；其中 5 个清掉非法 `staticRoutes`，`vpc-3998ab50` 保留其合法路由。
   验证：`vpc-vpc-68633c00` 的 `status.subnets` 补齐为 3 个子网；OVN NB 出现 `subnet-subnet-2c430138-…` 的 LS，`18c2e193` 的 LS 也一并建出；controller 不再报 `invalid next hop` / `out of range`。
2. ani-system 库里 4 条非法 gateway 路由按 ANI 语义**软删**（`state='deleted'`，`ani_app` 无 DELETE 权限、路由本就是软删设计），使记录与集群实际一致。

### 1.5 代码修复

`pkg/adapters/runtime/kubeovn_network_renderer.go` → `RenderRoute`：

1. **校验 `next_hop_id` 必须是合法 IP**（`net.ParseIP`），否则返回 `ports.ErrInvalid`，不再把用户自由文本透传给 kube-ovn；失败发生在 apply 之前，属 fail-closed。
2. **渲染的 `Vpc` spec 里带上 `spec.namespaces`**（与 `RenderVPC` 一致），消除同一 fieldManager 下路由 apply 清空 namespace 绑定的问题。

未改 OpenAPI 契约、无 DB 迁移、无生成物变更。

---

## 2. 问题 A：VM 创建后被写成终态 stopped

### 2.1 现象与证据

- ANI 库 `workload_instances.state='stopped'`（`55555`、`obsrace-510881`、`diagvm-a92b050d` 三条一致），而同一时刻 K8s 侧 `kubectl get vm` 是 `Starting`；
- 网关日志在 `POST /instances` 返回的同一瞬间出现 `persistWithQuotaTransition previous=provisioning next=stopped`；
- `kubectl get crd virtualmachines.kubevirt.io` 的 `status.printableStatus` **CRD 默认值就是 `Stopped`**。

### 2.2 根因

1. **段 1（触发）**：`KubernetesRESTClient.observeKubeVirtVMI` 的 VMI-404 分支，只要 VM 的 `printableStatus` 是 `stopped`/`halted` 就直接返回终态 `Stopped`。刚 apply 完的 VM 恰好在 virt-controller 创建 VMI 之前处于这个窗口（`printableStatus` 还是 CRD 默认 `Stopped`），于是创建流程内的同步观测把它判成 stopped。
2. **段 2（落库）**：`LocalStatusReconciler.mapProviderPhase` 把 `Stopped` 映射为 `WorkloadStateStopped`，`persistWithQuotaTransition` 写库。
3. **段 3（不可自愈）**：
   - gateway 读修复路径 `refreshOneVMStoreStatus`/`refreshOneStoreStatus` 有粘滞守卫（`state == stopping|stopped` 时不回写），库里的 stopped 永不改写；
   - `mapProviderPhase` 又缺 `scheduling` 分支，KubeVirt VMI 真实 phase `Scheduling` 会直接返回 `ErrUnsupported`（网关日志可见 `unsupported provider phase "Scheduling"`），使后台收敛路径也走不通。

### 2.3 修复

1. `kubernetes_rest_client.go`：新增 `kubeVirtVMExpectsRunning`（读 `spec.running==true` 或 `spec.runStrategy∈{Always,RerunOnFailure}`），并把该意图传入 `observeKubeVirtVMI`；VMI-404 且 VM 状态为 `stopped`/`halted` 时，若 VM 有开机意图则返回 `Pending` 而不是终态 `Stopped`。用户主动停机时 KubeVirt stop 子资源会清掉 `spec.running`，因此真实 stopped 语义不变。
2. `status_reconciler.go`：`mapProviderPhase` 的 provisioning 组补 `scheduling`。
3. **未动**粘滞守卫：区分「用户主动 stop」与「观测误判」需要产品/架构决策，单独评估。

---

## 3. 验证

### 3.1 单测（本机）

新增/调整用例：

- `TestKubeOVNNetworkRendererRejectsNonIPGatewayNextHop`：非 IP gateway next_hop → `ErrInvalid`；
- `TestKubeOVNNetworkRendererRendersRouteAsVpcStaticRoute`：断言渲染结果同时含 `staticRoutes` 与 `namespaces`；
- `TestKubernetesRESTClientKubeVirtVMIPhaseIsAuthoritative` 新增三例：`spec.running=true` / `runStrategy=Always` 的 VMI-404 → `Pending`；`running=false + runStrategy=Halted` → 仍 `Stopped`；
- `TestLocalStatusReconcilerMapsKubeVirtSchedulingObservation`：`Scheduling` → provisioning。

结果：以上用例全 PASS；`go build`（pkg + gateway）通过；`pkg/adapters/runtime` 全量 `go test` 仅既有 Windows sandbox symlink 两用例环境性失败（需管理员权限，与批次无关）；`gofmt -l` 改动文件无输出。

### 3.2 ani-test2 隔离环境（热部署）

镜像 `test2-20261009-vmstart`，`kubectl set image` 到 `ani-test2/ani-gateway`，Pod 1/1 Running、`http://10.10.1.66:30083/healthz`=200。三项功能断言全 PASS：

```
PASS  B1  POST /networks/routes next_hop_id="test"
          -> 400 "gateway next_hop_id \"test\" must be an IP address"（fail-closed）
PASS  B2  POST /networks/routes next_hop_id=10.240.0.254 -> 201（real_provider=kubeovn）
          K8s Vpc 实测 = {"namespaces":["ani-tenant-aabbcc00-…"],
                          "staticRoutes":[{"cidr":"0.0.0.0/0","nextHopIP":"10.240.0.254",...}]}
          （修复前该 apply 会把 namespaces 删掉）
PASS  A   创建 VM -> 201 state=provisioning
          reason="VirtualMachineInstance not found while VirtualMachine status is Stopped"
          即刻 GET -> pending（均非终态 stopped）
          VMI 最终 Running / IP 10.240.1.2 / 节点 dev-phys-03（整链路跑通）
```

部署插曲：ani-test2 PG `max_connections=100` 被 gateway 多连接池打满，滚动更新时新 Pod 起不来（`FATAL: remaining connection slots are reserved for roles with the SUPERUSER attribute`，即《ani-test2 重建指南》§8 记录的已知坑）。因该 Deployment 用默认 `maxSurge=1/maxUnavailable=0`，old+new 双 Pod 同时连库必然死锁；本次用 `scale 0 → 1` 过桥。另：`tc-test-tenant` 的租户命名空间在集群不存在（建 VM 报 404），验证时临时创建、验证后已连带清理。

### 3.3 ani-system 生产形态

`kubectl set image deployment/ani-gateway -n ani-system ani-gateway=<image>`；该 Deployment 本就配置 `maxSurge=0/maxUnavailable=1`（GATEWAY-GPU-PLATFORM-SCOPE-A 遗留的规避），故无 PG 连接翻倍问题，rollout 一次通过。验证：Pod 1/1 Running、restarts=0、`/healthz`=200、`/readyz`=200、日志无 ERROR、`vpc-vpc-68633c00` 的 namespaces 与 3 个子网保持健康、kube-ovn 无相关报错。

注意：ani-system 原镜像为 `kb-20260930-listfilter`，新镜像基于 `origin/main` 构建，**同时带入 main 已合并的其它改动**（如 #200 KB 列表过滤），并非只含本批次。回滚锚：`docker.changqingyun.cn/ani/ani-gateway:kb-20260930-listfilter`。

---

## 4. 门禁

`gofmt -l`（改动文件）无输出、`go build`（pkg + gateway）、`pkg/adapters/runtime` 全量 `go test`（仅既有 Windows symlink 用例环境性失败）、`git diff --check`；仓库级 `make test` / `make validate-architecture` 结果见提交记录（本机 make 可用，Go 段以等价命令直接执行）。

未改 OpenAPI 契约 → 无 Core API 兼容性、SDK、静态文档、authz registry 生成物漂移；未改 Services API。

### 4.1 随批次的 CI 工具链升级（与本批次正交，附带解封）

本 PR 首次推送后 CI 的 required job `Services Boundary / API / Docs Gate`（内部跑 `make validate-service-runtime-observability` → `govulncheck`）失败，根因与业务代码无关：

- 失败内容为「Your code is affected by 5 vulnerabilities from the Go standard library」，`GO-2026-6603/6611/6612/6613/6617`，`Found in: net/http@go1.25.13`，`Fixed in: net/http@go1.26.9`。这 5 条公告 `published=2026-10-08T22:31:09Z`，晚于 main 上一次全绿（2026-10-08T11:23:51Z），故**任何新 PR（含 main 自身）都会被拦**，rebase 无用。
- 注意：名字更像的 `Dependency CVE Scan` job 反而**不阻断**——它在 `ci.yml` 标了 `continue-on-error: true`，且未列入 `required-gates.needs`。
- 修复：`ci.yml` 的 `GO_VERSION` `1.25.13` → `1.26.9`；仓库 CI 策略校验 `scripts/validate_ci_workflow.py` 强制 `GO_DOCKERFILE_PATHS`（ani-gateway / auth-service / reconcile-worker）的 Dockerfile 必须含 `FROM golang:{GO_VERSION}-`，故同步把 **12 个 Go 构建 Dockerfile**（含 inference-service 的 `Dockerfile.publisher`；model-service / model-fetcher 两个 digest 钉死项重新解析为 `golang:1.26.9-alpine@sha256:cdfd4fe2…b84e0`）升到 `golang:1.26.9-alpine`。
- 未动 `go.work`/`go.mod` 的 `go 1.25.0` 指令，未动 `golang.org/x/net`（govulncheck 报告其不可达，非阻断项）。若抬 go 指令会波及 10+ 模块的 go.sum，属不必要的爆炸半径。

本地/构建机验证（构建机内以 `golang:1.26.9-alpine` 容器执行）：

- 最小 net/http 探针：`1.25.13` → 5 个 stdlib 漏洞（exit 3）；`1.26.9` → `No vulnerabilities found`（exit 0）。
- `docker build --no-cache` 构建 ani-gateway 通过（exit 0）。
- `pkg/adapters/runtime` 目标用例在 1.26.9 下 `ok`。
- `python scripts/validate_ci_workflow.py` → `CI workflow contract valid`；`validate_ci_workflow_test.py` → 13 tests OK。

遗留：已发布到 Harbor 的旧镜像仍是 1.25.13 stdlib，需手动触发一次重新构建/发布才算镜像侧闭环；`build-image.yml` 仅在版本 tag 或 `workflow_dispatch` 触发，合并代码不会自动重建。

---

## 5. 遗留与后续

1. **粘滞守卫不可逆**：库里已是 `stopped` 的历史实例（如 `55555`）在修好 A 之后仍需人工干预或等后台 reconcile 收敛，本批次不放松守卫。
2. **存量坏数据**：ani-system 已软删 4 条非法路由；`rt_91ae7c66-…`（`next_hop_type=instance`、`next_hop_id="123"`、`state=failed`，从未 apply、无集群影响）保留。
3. **契约层面仍无 `next_hop_id` 格式说明**：当前校验放在渲染层（adapter），未进 OpenAPI schema 的 `pattern`；如需 API 层更早报错属契约批次。
4. **ani-test2 部署策略**：建议给 `ani-test2/ani-gateway` 也配置 `maxUnavailable: 1`，或收敛 gateway 的连接池数量，避免每次滚动更新都要 `scale 0/1`。
5. `next_hop_type=instance` 的 `next_hop_id` 仍无非空以外的校验（渲染层本就拒绝非 gateway 类型，暂不影响）。