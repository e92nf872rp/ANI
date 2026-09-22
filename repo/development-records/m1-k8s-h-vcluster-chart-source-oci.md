# M1-K8S-H — vCluster Chart 来源切换 Harbor OCI 与 Chart 版本锁定

完成日期：2026-09-18
对应分支：`hotfix/network-store-read`
验证结果：live verified（ani-test2 隔离环境 + ani-system）。`POST /api/v1/k8s-clusters` 端到端创建成功并返回 201，vCluster 控制面 StatefulSet `1/1` Ready。

## 背景

「创建 K8s 集群」在真实底座持续失败。实测定位为三层独立缺陷叠加，任何一层不修复都无法创建成功。

## 根因（三层，均已实测确认）

| 层 | 现象 | 根因 |
|---|---|---|
| 1 | helm/vcluster 命令报 `mkdir /home/ani: permission denied` | 网关容器以 uid 65532 运行，镜像 `HOME=/home/ani` 属 root 且不可写；Helm repository cache 与 vcluster CLI 状态都需要写 HOME |
| 2 | `helm upgrade --install` 拉取 chart 超时/失败 | 默认 chart 源 `https://charts.loft.sh` 在集群内不可达，无法直接使用远端 HTTP 仓库 |
| 3 | syncer 容器 `CrashLoopBackOff`，日志 `dial tcp 10.96.0.1:443: i/o timeout` | 租户 namespace `ani-tenant-00000000-…-000000000001` 绑定 32 个 kube-ovn 私有 VPC 网段（`private`、`natOutgoing=false`），无 `ovn-default`，控制面 Pod 落到 `10.60.1.0/24`，无法访问集群 ClusterIP service CIDR；syncer 连不上宿主 API Server 而崩溃，`vcluster connect --print` 随之悬挂 |

第 3 层修法与 ANI 既有 `PlatformWorkload` 约定一致（见 `pkg/adapters/runtime/kubernetes_platform_workload_runtime.go` 对控制面 Pod 的 ovn 注解渲染）：把 vCluster 控制面 Pod 钉回集群默认 overlay。普通租户 namespace（tenant-a / `f4d7c1e7…` / `11111111…`）本就使用 `ovn-default`，不受影响。

## 实现了什么

1. **chart 来源切到内网 Harbor OCI**：`VCLUSTER_CHART_NAME=oci://docker.changqingyun.cn/ani/charts/vcluster` + `VCLUSTER_CHART_REPO=none`（`none` 令 provider 省略 `--repo`，适配 OCI 引用）。
2. **chart 版本锁定**：新增 `VCLUSTER_CHART_VERSION`（网关 env → `VClusterHelmProviderConfig.ChartVersion` → 非空时追加 `--version`），使离线 OCI 仓库中的 chart 版本显式钉在 `0.34.1`，与镜像内 vcluster CLI 版本一致。
3. **HOME 可写**：Deployment pod `securityContext` 加 `fsGroup: 65532`，新增 `emptyDir` 卷 `ani-gateway-helm-cache` 挂到 `/home/ani`。
4. **控制面 Pod 网络钉定**：`VCLUSTER_HELM_SET_VALUES` 由单值扩展为 CSV（storageClass + 两个 ovn 注解 `ovn\.kubernetes\.io/logical_switch=ovn-default`、`ovn\.kubernetes\.io/vpc=ovn-cluster`），点号按 helm `--set` 语法转义。

未改 OpenAPI 契约、未改生成物、无 DB 迁移。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml` | 修改 | `fsGroup: 65532`；`ani-gateway-helm-cache` emptyDir → `/home/ani`；`VCLUSTER_CHART_NAME` / `VCLUSTER_CHART_REPO=none` / `VCLUSTER_CHART_VERSION=0.34.1` / `VCLUSTER_HELM_SET_VALUES` CSV |
| `deploy/real-k8s-lab/sprint13-production-auth-dex.yaml` | 修改 | 同上（auth-dex profile 内的 gateway Deployment） |
| `pkg/adapters/runtime/vcluster_helm_provider.go` | 修改 | `VClusterHelmProviderConfig.ChartVersion` → `helmUpgradeInstallArgs` 非空追加 `--version` |
| `pkg/adapters/runtime/vcluster_helm_provider_test.go` | 修改 | 新增 `TestVClusterHelmProviderAdapterPinsChartVersionForOCIRepository` |
| `services/ani-gateway/k8s_proxy_runtime.go` | 修改 | `VCLUSTER_CHART_VERSION` env → runtime config → provider `ChartVersion` |

## 完工标准达成

- [x] 单测覆盖 OCI chart 名 + 版本锁定（`go test ./adapters/runtime/... -run VClusterHelm` 通过）
- [x] 网关 Pod 内可写 HOME，helm 不再 `permission denied`
- [x] 网关 Pod 内可匿名 pull Harbor OCI chart `0.34.1`
- [x] ani-test2 端到端 `POST /api/v1/k8s-clusters` → 201，控制面 sts `1/1`
- [x] ani-system 端到端同上
- [ ] 孤儿 vcluster release 清理入口（见遗留）

## live 验证证据

**Harbor OCI**

- 引用：`oci://docker.changqingyun.cn/ani/charts/vcluster:0.34.1`
- digest：`sha256:a23addb2f0ef0e0b5cdd38636a8e9dc857c3d95ef303ef6f8e3f7d2dbe809139`
- 网关 Pod 内匿名 pull 成功。

**ani-test2（NodePort 30083）**

- 镜像 `docker.changqingyun.cn/ani/ani-gateway:test2-20260918-vclusteroci`
- `POST /api/v1/k8s-clusters` → 201（30.7s），控制面 sts `1/1`。

**ani-system（NodePort 30080，生产形态）**

- 部署形态为**镜像**（非 hostPath 二进制）：容器 `/proc/1/exe -> /usr/local/bin/ani-gateway`，Pod 内无 `/opt/ani/bin`。
- 镜像 `docker.changqingyun.cn/ani/ani-gateway:fix-20260918-vclusteroci`（由 test2 同产物 retag + push 得到，未重新构建）。
- `POST /api/v1/k8s-clusters` → 201（38.8s），控制面 sts `1/1` Ready，syncer Pod Ready。

**遗留观察（未深究，不影响创建成功）**：vcluster 内同步出的 coredns Pod 落在 `10.60.1.7`（VPC 网段）且 `0/1`。

## 运维落地与新环境复用手册

> 红线：本文不记录任何凭据/口令；chart tgz 不入 git。

### 1. chart 分发（Harbor OCI）

参数口径：

| 项 | 值 |
|---|---|
| OCI 仓库前缀 | `oci://docker.changqingyun.cn/ani/charts` |
| chart 名 | `vcluster` |
| 版本 | `0.34.1`（须与网关镜像内 vcluster CLI 版本一致） |
| 完整引用 | `oci://docker.changqingyun.cn/ani/charts/vcluster` + `--version 0.34.1` |

首次分发（在可访问 chart 源的机器上取 tgz，再推内网）：

```bash
# 取 chart（本环境集群内不可达 charts.loft.sh，需在可访问公网的构建机执行）
helm pull vcluster --repo https://charts.loft.sh --version 0.34.1
# 推送（Harbor 凭据通过 docker login / HELM_REGISTRY_CONFIG 提供，不入库）
helm push vcluster-0.34.1.tgz oci://docker.changqingyun.cn/ani/charts
# 校验
helm show chart oci://docker.changqingyun.cn/ani/charts/vcluster --version 0.34.1
```

### 2. 网关侧必需配置

Deployment 需同时满足：

- `VCLUSTER_CHART_NAME=oci://docker.changqingyun.cn/ani/charts/vcluster`
- `VCLUSTER_CHART_REPO=none`（OCI 引用时不传 `--repo`）
- `VCLUSTER_CHART_VERSION=0.34.1`
- `VCLUSTER_HELM_SET_VALUES='controlPlane.statefulSet.persistence.volumeClaim.storageClass=ani-rbd-ssd,controlPlane.statefulSet.pods.annotations.ovn\.kubernetes\.io/logical_switch=ovn-default,controlPlane.statefulSet.pods.annotations.ovn\.kubernetes\.io/vpc=ovn-cluster'`
- pod `securityContext.fsGroup: 65532`
- volume `ani-gateway-helm-cache`（emptyDir）→ `mountPath: /home/ani`

### 3. 换环境时的复用边界

| 项 | 换环境是否需要重做 |
|---|---|
| Harbor OCI 中的 chart | 不需要。内网 Harbor 为集群共享，新 namespace / 新集群直接引用同一 OCI 地址 |
| 网关 env（chart 名/仓库/版本/helm set values） | 依赖具体环境：storageClass（`ani-rbd-ssd`）和 ovn 注解须按目标集群实际 StorageClass 与 kube-ovn VPC 命名核对；非 kube-ovn 集群应移除两个 ovn 注解 |
| `fsGroup` + HOME emptyDir | 不需要重做。只要容器仍以非 root uid 运行且 HOME 不可写，就必须保留 |
| chart 版本号 | 与镜像内 vcluster CLI 版本绑定。升级 vcluster CLI 时必须同步重新分发对应 chart 版本并更新 env |

### 4. 私有 Harbor 项目退路

若目标 Harbor 的 `ani/charts` 为私有项目，Pod 内匿名 pull 会失败。可选退路：

1. 给 vCluster 控制面所在租户 namespace 配置 image pull secret 并让 helm 使用同一凭据；
2. 或改用集群内可访问的其它 OCI/HTTP chart 源，并通过 `VCLUSTER_CHART_NAME` + `VCLUSTER_CHART_REPO` 重新指向。

### 5. 版本口径

- anchor：chart `0.34.1` ↔ vcluster CLI `v0.34.1` ↔ 本批次网关 env `VCLUSTER_CHART_VERSION=0.34.1`。
- 三者必须一致；不一致会导致 helm 解析出的 chart 模板与 CLI 行为不匹配。

## 遗留

- 同 namespace 存在跨天遗留的孤儿 vcluster Helm release，重试创建会命中 `there is already a virtual cluster in namespace …`；ANI 当前没有 K8s 集群删除 API（仅 get/kubeconfig/upgrade/node-pools/proxy），缺清理入口。
- 源目录未随本批次改动的 `scripts/validate_vcluster_live_gate.py` 默认 chart 源仍为远端 HTTP；该默认值调整不在本批次范围。