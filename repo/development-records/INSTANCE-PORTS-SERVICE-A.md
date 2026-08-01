# INSTANCE-PORTS-SERVICE-A

> 日期: 2026-07-30
> 类型: Core 实例 ports/service/metadata + Kubernetes real-provider 基础闭环
> 状态: 本地门禁与真实容器实例 E2E 已通过；已随同批 VM/Sandbox/code-run live 提交

## 目标

在已确认的 `INSTANCE-CONTRACT-A` 公开契约之后，补齐统一实例管理的
provider-neutral ports、`LocalInstanceService` 语义和 PostgreSQL metadata
持久化，使 VM、Container、GPU Container 与 Sandbox 的创建摘要、列表筛选、
生命周期校验、幂等操作记录具备可测试的后端基础。

## 已实现

- 扩展实例 image、compute、network、access、storage、GPU spec 和 Sandbox 摘要类型。
- 创建请求校验 kind config、磁盘 source union、环境变量 value/secret union，以及
  GPU spec 与旧 GPU selector 的冲突。
- 创建和 lifecycle 强制幂等 key，使用请求指纹防止同 key 不同 intent；旧操作缺少
  指纹时 fail closed，失败操作重放保留原错误类别。
- lifecycle 支持文件系统挂载、扩缩容、镜像更新、Secret、安全组、终止保护和
  Sandbox pause/resume/extend/touch_idle；动作与 payload 按 kind 校验。
- VM、Container 和 GPU Container 均允许卷绑定；根卷禁止卸载。
- Sandbox 创建和生命周期通过 `ports.SandboxRuntime`，缺少 runtime 时不登记幽灵操作。
- 列表支持通用与 kind-specific 筛选和确定性排序；当前 service result 边界不支持
  cursor，显式返回 unsupported，避免伪造分页语义。
- metadata store 持久化稳定详情摘要和 Sandbox 状态；operation step 可记录
  `task_id`、`resource_type`、`resource_id`。
- 新增 additive PostgreSQL migration，扩展实例摘要列、实例 kind check、
  lifecycle operation check 和 operation step task 关联列。
- 修复既有 Gateway instance lifecycle 对 `attach_volume.mount_path` 的字段透传。
- 将实例 Router 的 `demo` 内部命名改为中性 `instance` 命名；内存状态实现明确命名为
  `memoryInstanceStore`，不改变现有 provider 注入行为。
- Gateway 启动时通过 bootstrap 注入 PostgreSQL-backed instance service/store/operation
  store 与 Kubernetes REST provider，不再由 Router 固定创建进程内实例服务。
- 为 `reconcile-worker` 增加独立 Dockerfile；后台 reconcile 按 target tenant 注入
  RLS context，并跳过 `deleting/deleted` 终态，避免 worker panic 或删除后回写
  `failed`。
- Kubernetes 生命周期删除会清理实例的全部 provider resource refs，并将 404
  视为幂等删除成功；实例 ID 改为跨进程唯一 UUID，避免 Gateway 重启后冲突。
- 真实集群 E2E 已验证：Bearer 认证、PostgreSQL 实例与 operation 持久化、
  Kubernetes Deployment/Pod、Harbor 内网镜像拉取、Pod 集群网络 IP、stop/start、
  delete 多资源清理，以及跨 worker reconcile 周期保持 `deleted`。
- Gateway 已补齐实例创建请求到 provider-neutral ports 的字段转换，覆盖镜像 ID/引用、
  网络/VPC/子网/安全组、VM 磁盘与文件系统挂载、容器端口/env/卷挂载、workload identity、
  GPU spec 引用和 Sandbox 配置。
- Gateway lifecycle 已转换快照、文件系统、扩容、换镜像、Secret、安全组、终止保护和
  Sandbox 时长等字段；非 resize 操作不会被默认 CPU/内存字段污染。
- Kubernetes manifest renderer 已消费容器副本数、命名端口、env、卷/文件系统挂载，
  并对同一资源的重复挂载做去重。
- 实例创建已增加 provider-neutral resource resolver：bootstrap 创建的真实实例服务会在
  workload provider 执行前校验 VPC、子网、安全组、卷和文件系统的租户归属及
  `available` 状态，并把解析出的资源引用写入 create operation precheck。
- 已实现只读 GPU Spec catalog 和 `/gpu-specs` 查询路由；实例创建时会通过 `spec_id`
  解析 GPU 类型、切分份数和显存，并拒绝不存在或不可用于新实例的规格。该流程不执行
  quota check、acquire 或 release。
- bootstrap 已支持通过 `REGISTRY_PROVIDER_MODE=harbor` 接入 Harbor；显式 `image_ref`
  会在实例创建前校验租户 project、repository、tag/digest，并将固定后的 digest 写入
  实例 image summary 和 operation resource refs。

## 正确性边界

- 本批对 **container 基础生命周期** 声明 real-provider E2E passed；不外推为全部
  instance kind runtime-ready 或 full platform production-ready。
- Network/Security Group、Storage volume/filesystem task 仍需通过对应 port/adapter 做
  更完整的实例级真实关联编排；当前 Network/Storage 已完成创建前引用校验，GPU Spec
  与显式 Harbor image_ref 已完成解析，但尚未把配额和所有异步资源 task 编排成跨资源事务。
- Sandbox token、preview ports、files、checkpoints 仍为后续；code-run real-provider
  已由 `INSTANCE-SANDBOX-CODERUN-A` 单独闭环。
- 数据库 keyset cursor、HTTP `next_cursor`、配额 check/acquire/release 和 GPU Container
  统一实例 live、完整 ORCHESTRATION 仍为后续批次（VM/Sandbox create-lifecycle/code-run
  live 已另档闭环）。
- 集群节点访问 Docker Hub 超时；本次真实运行镜像已镜像到 Harbor。该结果证明
  Harbor 内网路径可用，不证明集群具备公网镜像拉取能力。

## 验证

已通过:

```text
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime/... ./services/ani-gateway/... -count=1
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'Test.*(Instance|Operation)' -count=1
GOCACHE=/tmp/ani-go-cache go test ./pkg/ports/... ./pkg/adapters/runtime/... -count=1
PATH=/tmp/ani-pybin:$PATH make test
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
PATH=/tmp/ani-pybin:$PATH make validate-ci-workflow
git diff --check
```

真实环境证据：

- `development-records/live-evidence/instance-management-container-e2e-20260730.json`
- Gateway image: `docker.changqingyun.cn/ani/ani-gateway:instance-e2e-20260730-v6`
- Reconcile worker image:
  `docker.changqingyun.cn/ani/reconcile-worker:instance-e2e-20260730-v2`
