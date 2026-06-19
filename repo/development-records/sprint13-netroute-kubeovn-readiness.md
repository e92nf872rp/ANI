# Sprint 13 切片 01 — 网络路由 Kube-OVN real provider 就绪声明（先声明，后接入）

> 记录类型：Per-slice readiness（ANI-06「真实底座组件引入强制门禁」§153 的执行前声明）
> 工件归属：Sprint 13 / Core real provider 与 live gate 收敛
> 执行地图：[`sprint13-real-provider-readiness-plan.md`](sprint13-real-provider-readiness-plan.md)
> 状态：**planning（尚未跑通 live gate）。在 evidence 产出前，网络路由能力只可标 Tier1 local profile。**

---

## 0. 已核对的真实事实（禁止臆测）

1. Sprint 12 已落地路由契约与本地实现：`ports.NetworkService.CreateRoute/ListRoutes`（`pkg/ports/network_resources.go`），网关 `GET/POST /networks/routes`（`services/ani-gateway/internal/router/network_resources.go`），当前由 in-memory `NetworkService`（`pkg/adapters/runtime/network_service.go`）支撑 = Tier1 local profile。
2. 网络真实 provider 既有管线：`ports.NetworkProvider`（`DryRun`/`Apply`/`Observe`）+ `KubeOVNNetworkProviderAdapter`（`pkg/adapters/runtime/kubeovn_network_provider.go`）+ `KubernetesNetworkProviderClient`。VPC/Subnet/SG/LB 已按 render→apply→observe 走真实 provider（Sprint 5 evidence：`m1-network-live-c-kubeovn-real-lab-result.md`）。**路由暂未纳入该 provider 管线。**
3. live gate 入口：`make validate-kubeovn-network-live-gate` → `scripts/validate_kubeovn_network_live_gate.py`(+test)；fixtures 在 `deploy/real-k8s-lab/kubeovn-network-live-gate.yaml`。
4. 底座（Sprint 5/11 已部署，三台物理服务器）：Kube-OVN `v1.15.8`、Kubernetes `v1.36.1`、CNI/CoreDNS Ready。

## 1. §153 五项声明

| 项 | 内容 |
|---|---|
| **当前状态** | contract + Tier1 local profile（路由经 in-memory `NetworkService`）。目标：real-provider（Kube-OVN）。 |
| **真实组件 + 版本** | Kube-OVN `v1.15.8`，Kubernetes `v1.36.1`（三台物理开发服务器）。 |
| **live gate 命令** | 扩展并运行 `make validate-kubeovn-network-live-gate`，覆盖 route create/list 的 render→apply→observe；在真实 lab 执行。 |
| **evidence 输出路径** | `repo/development-records/sprint13-netroute-kubeovn-live-result.md` + 复跑命令与非敏感 evidence JSON（沿用 Sprint 5 network live evidence 结构）。 |
| **失败边界（不得声称）** | 若路由在真实 Kube-OVN 上的 Apply/Observe 未跑通，网络路由能力**只保持 Tier1 local profile**，不得标 real-provider / runtime ready / production ready；不得用 Sprint 5 VPC/Subnet 的 evidence 替代路由 evidence。 |

## 2. 代码边界（不改 handler、不改 port 签名）

- real route provider 走既有 `NetworkProvider` render→apply→observe 管线；新增「路由 → Kube-OVN manifest」渲染 + provider 选择（forwarding，与 VPC/Subnet 同构），`NetworkService` route 方法在 real 模式下转发到 provider。
- **执行前必须先确认** Kube-OVN `v1.15.8` 的静态路由表达方式（`Vpc.spec.staticRoutes` 还是 `VpcStaticRoute`/`StaticRoute` CRD），以部署版本的真实 API 为准再渲染 manifest——不得照搬假设的字段。
- K8s/Kube-OVN SDK 只能在 adapter/provider/client 边界（`bounded_direct`），**禁止进 Gateway handler**；新依赖需在 `validate_component_imports` 登记 allowlist + `coupling_level` + 理由。
- 契约：若真实 provider 暴露 local profile 没有的字段，先在 `api/openapi/v1.yaml` **只增可选字段**再实现，保持 v1 兼容。

## 3. 真实服务器安全（Sprint 11 规则继续有效）

- 任何写操作前**重新只读盘点 + 人工确认**预期影响和回滚；优先在临时/隔离 VPC 上验证路由，跑通后清理临时资源。
- 不动系统盘 / fstab / 默认 StorageClass；不并发重启；凭据只在本机 `local-secrets/`，**绝不写入可提交文件、evidence、日志或回复**。

## 4. 执行提示词（人工 / AI 可直接粘贴）

```text
角色：ANI Core 平台工程师。ANI 是生产级基础设施平台，代码必须严谨、可落地交付。

加载（按序）：CLAUDE.md → ANI-DOCS-INDEX.md → repo/CURRENT-SPRINT.md →
ANI-06-开发计划.md（§0 + §真实底座组件引入强制门禁）→
repo/development-records/sprint13-real-provider-readiness-plan.md →
repo/development-records/sprint13-netroute-kubeovn-readiness.md →
repo/api/openapi/v1.yaml（networks/routes 段）。

分支：feature/sprint12-core-support，不碰 main、不推远端。

切片：Sprint 13 网络路由 Kube-OVN real provider（CORE-SVC-SUPPORT-NETROUTE-LIVE-A）。
目标：把 ports.NetworkService 的 CreateRoute/ListRoutes 在 real 模式接到 Kube-OVN，
经既有 NetworkProvider(DryRun/Apply/Observe)+KubeOVNNetworkProviderAdapter 管线，
不改 Gateway handler、不改 port 签名。

前置（必须先做，禁止臆测）：
1. 在真实 lab 确认 Kube-OVN v1.15.8 的静态路由 API（Vpc.spec.staticRoutes 或 VpcStaticRoute/StaticRoute CRD），以部署版本为准。
2. 在隔离/临时 VPC 上验证，跑通后清理临时资源；任何写操作前重新只读盘点 + 人工确认。

实现：
- 新增「路由 → Kube-OVN manifest」渲染 + provider 路径接线（与 VPC/Subnet 同构）。
- NetworkService route 方法 real 模式转发到 provider；local 模式保持不变。
- 扩展 scripts/validate_kubeovn_network_live_gate.py(+fixtures) 覆盖 route create/list 的 render→apply→observe。
- 新组件依赖在 validate_component_imports 登记 allowlist+coupling_level+理由；handler 不直接 import SDK。
- 契约差异先改 v1.yaml（只增可选字段）。

完成判定（全绿并贴出输出）：
cd repo && make test && make validate-network-alpha validate-kubeovn-network-live-gate && python scripts/validate_yaml.py api/openapi/v1.yaml && git diff --check
真实 lab 跑 route create/list/observe，输出非敏感 evidence JSON。

收尾：新增 repo/development-records/sprint13-netroute-kubeovn-live-result.md（含 §153 五项实测结果 + 边界），
更新 development-records/README.md、repo/CURRENT-SPRINT.md、ANI-06-开发计划.md §0；
跑通才把网络路由标 real-provider，否则保持 Tier1 local profile。全部提交到 feature/sprint12-core-support。
```

## 5. 关联文档

- Sprint 13 执行地图：[`sprint13-real-provider-readiness-plan.md`](sprint13-real-provider-readiness-plan.md)
- 当前冲刺入口：[`../CURRENT-SPRINT.md`](../CURRENT-SPRINT.md)
- 真实底座门禁：[`../../ANI-06-开发计划.md`](../../ANI-06-开发计划.md) §「真实底座组件引入强制门禁」
- Kube-OVN 历史 live evidence：`m1-network-live-c-kubeovn-real-lab-result.md`、`m1-network-live-d-kubeovn-external-lb-real-lab-result.md`
- 代码：`pkg/ports/network_resources.go`、`pkg/adapters/runtime/kubeovn_network_provider.go`、`pkg/adapters/runtime/network_service.go`、`services/ani-gateway/internal/router/network_resources.go`
