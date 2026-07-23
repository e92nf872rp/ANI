# 实例可观测性补全方案（GPU 指标 / 日志持久化 / VM 指标）

> 基于 `prd-console-instance-observability.md` 当前实现现状，针对三个未闭环问题给出可行方案。
> 生成日期：2026-07-17
> 状态：待人工审核

---

## 一、问题背景与现状事实

### 问题 1：GPU 指标在 handler 路径下死分支

**现状事实：**
- [demo_instances.go:669-672](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/demo_instances.go) 的 `getMetrics` 调 `GetMetrics` 时**未把 `record.Kind` 填入 `InstanceObservationGetRequest.Kind`**，导致 `Kind` 恒为空字符串。
- [prometheus_instance_observability.go:172](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_instance_observability.go) 的 GPU 分支判断 `request.Kind == ports.WorkloadKindGPUContainer`，**在生产路径下永远为 false**，GPU 字段恒为 nil。
- DCGM exporter 已部署在 `ani-system` namespace，地址 `ani-dcgm-exporter.ani-system:9400`（[sprint13-gpu-inventory-dcgm-readiness.md](file:///e:/go/project/ANI/repo/development-records/sprint13-gpu-inventory-dcgm-readiness.md) 第 17、24 行）。
- 但 [sprint13-instance-observability-prometheus-live.yaml](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml) 的 Prometheus **未配置 DCGM scrape**，即使 Kind 传了也查不到 `DCGM_FI_*` 指标。

**目标：** `gpu_container` 实例的快照卡片和时序图展示 GPU 利用率、显存 used/total。

---

### 问题 2：日志无持久化，Pod 重启/删除后丢失

**现状事实：**
- [prometheus_instance_observability.go:84](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_instance_observability.go) 的 `ListLogs` 直接调 K8s `/api/v1/namespaces/{ns}/pods/{pod}/log`，等同 `kubectl logs --tail`。
- Pod 重启后旧容器日志丢失（K8s 默认保留已终止容器日志在节点上，但 `--previous` 才能访问）；Pod 删除后所有日志丢失。
- [local_instance_observability_service.go:48-52](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/local_instance_observability_service.go) 的 local adapter 返回硬编码合成数据，非真实存储。
- `repo/deploy` 下无 Loki/Fluent Bit/Promtail/ES 部署；`repo/pkg` 下无 LogStore/LogRepository port 抽象。
- PRD US-008 要求 cursor 分页，但 K8s pod log API 不支持 cursor，当前是前端模拟。

**目标：** Pod 重启或删除后仍可分页浏览历史日志，真分页（cursor 语义明确）。

---

### 问题 3：VM 指标采集链路缺失

**现状事实：**
- [prometheus_instance_observability.go:107-196](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_instance_observability.go) 的 `GetMetrics` 对 `kind=vm` **没有特殊分支**，走的是 container 专用的 `container_cpu_usage_seconds_total` 等 cAdvisor 指标。
- 这些指标反映的是 `virt-launcher` pod 内 QEMU 进程的 cgroup 资源，**不是 VM guest OS 的真实资源使用**。
- [sprint13-instance-observability-prometheus-live.yaml](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml) 的 scrape_configs 只有 `kubernetes-cadvisor` 一个 job，**没有 KubeVirt virt-handler 的 scrape**。
- VM 底层是 KubeVirt（`vm-management.md` 确认 `provider: "kubevirt"`），virt-handler 已部署在 `kubevirt` namespace，`kubevirt_vmi_*` 指标有数据源。

**目标：** VM 指标 Tab 必须展示 guest OS 真实资源数据（CPU/内存/网络），不能是 QEMU 进程 cgroup 数据。

---

## 二、方案决策汇总

| 问题 | 决策 | 理由 |
|---|---|---|
| GPU 指标触发 | 修 handler 传 `Kind` + 加 DCGM scrape | handler bug 是一行 fix；DCGM exporter 已外部部署，只需 Prometheus 加 scrape |
| 日志持久化 | Port 抽象 + 多 adapter 可选注入 | 部署 Loki 或 ES 时，adapter 层选择对应实现；未部署时 fallback 到 K8s API；port 层不暴露存储后端语义 |
| 日志采集器 | Fluent Bit（推荐） | Promtail EOL，Grafana Alloy 配置复杂，Fluent Bit 轻量、成熟、CNCF 毕业 |
| 注入方式 | 环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` | 与现有 `INSTANCE_OBSERVABILITY_PROVIDER` 模式一致 |
| 日志分页 cursor | opaque string | 不暴露存储后端语义，adapter 内部映射为 Loki time / ES search_after / K8s tailLines |
| Loki 多租户 | namespace label 过滤 | 与现有 `tenantNamespace` 逻辑一致 |
| 部署 yaml | 推荐示例，非必须部署 | 保留 Loki + Fluent Bit 完整 yaml 作为开箱即用示例，也可部署 ES/OpenSearch 替代 |
| VM 指标采集 | 路径 A：KubeVirt 自带 `kubevirt_vmi_*` 指标 | virt-handler 已部署，只需加 scrape + 改 adapter 分支，复杂度低；满足运维场景（VM 占用多少宿主机资源） |

---

## 三、方案一：GPU 指标采集（DCGM）

### 3.1 落地步骤

#### 步骤 1：修复 handler 传 Kind（与方案三共用）

文件：[demo_instances.go:669-672](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/demo_instances.go)

```go
// 修改前
result, err := api.observability.GetMetrics(ctx, ports.InstanceObservationGetRequest{
    TenantID:   demoTenantID(c),
    InstanceID: api.observabilityTargetID(record),
})

// 修改后
result, err := api.observability.GetMetrics(ctx, ports.InstanceObservationGetRequest{
    TenantID:   demoTenantID(c),
    InstanceID: api.observabilityTargetID(record),
    Kind:       record.Kind,  // 新增：透传实例类型
})
```

> 这个修复同时解决 GPU 和 VM 的问题，是方案一和方案三的共同前置步骤。

#### 步骤 2：修改 Prometheus scrape 配置

文件：[sprint13-instance-observability-prometheus-live.yaml](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml)

在 `scrape_configs` 下新增 DCGM exporter job，用 `static_config` 直接指向 Service 地址：

```yaml
- job_name: dcgm-exporter
  metrics_path: /metrics
  static_configs:
    - targets: ['ani-dcgm-exporter.ani-system:9400']
```

> **RBAC 说明：** `static_config` 直接 HTTP 访问 Service，不需要 Prometheus 有跨 namespace 的 RBAC 权限。只要网络可达即可。

#### 步骤 3：验证 adapter GPU 分支

文件：[prometheus_instance_observability.go:172-196](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_instance_observability.go)

现有 GPU 分支查询的指标名（对齐真实 DCGM exporter，live gate 2026-07-20 复现）：
- `DCGM_FI_DEV_GPU_UTIL`（GPU 利用率）
- `DCGM_FI_DEV_FB_USED`（显存已用，单位 MiB）
- `DCGM_FI_DEV_FB_FREE`（显存空闲，单位 MiB）；`GPUMemoryTotalMB = FB_FREE + FB_USED`

真实 DCGM exporter 不暴露 `DCGM_FI_DEV_FB_TOTAL`，adapter 改用 `FB_FREE + FB_USED` 推导 total，单位 MiB 直传无需换算。需确保步骤 1 传了 `Kind=gpu_container`，分支会触发。

### 3.2 验证标准

- [ ] Prometheus `DCGM_FI_DEV_GPU_UTIL` 指标可查到
- [ ] `gpu_container` 实例详情指标 Tab 快照卡片展示 GPU 利用率、显存 used/total
- [ ] GPU 指标时序图展示 GPU 利用率、显存使用率曲线
- [ ] 非 GPU kind 不展示 GPU 卡片（`Kind != gpu_container` 时 GPU 字段为 null）

---

## 四、方案二：日志持久化（Port 抽象 + 多 adapter 可选注入）

### 4.1 架构

```text
Console (LogsTab)
    ↓ 调
InstanceObservability.ListLogs (现有 port interface，不改)
    ↓ 内部
PrometheusInstanceObservability（现有 adapter，作为组合容器）
    ├── 持有 logStore 字段（ports.LogStore，可选，nil 时 fallback 到 K8s API）
    │   ↓ 运行时通过环境变量 INSTANCE_OBSERVABILITY_LOG_STORE 选择具体实现
    │   ├── LokiLogStore        ← 推荐（部署 Loki + Fluent Bit 时启用）
    │   ├── ElasticsearchLogStore ← 可选（部署 ES/OpenSearch 时启用）
    │   └── nil                  ← fallback 到 K8s API（现有逻辑，无持久化）
    │
    └── ListLogs 方法逻辑：
        if logStore != nil → 调 logStore.QueryLogs（走持久化存储）
        else               → 调 K8s pod log API（现有逻辑，无持久化）

部署侧（推荐示例，非必须）：
Pod stdout/stderr
    ↓
Fluent Bit DaemonSet（推荐，采集器）
    ↓ 添加 label: namespace, pod, container
Loki（推荐，存储后端，存 MinIO S3）
    ↓ LogQL: {namespace="ani-tenant-<tenant_id>",pod="<instance_id>"}
LokiLogStore.QueryLogs（adapter，cursor=opaque string）
```

**设计要点：**
- **port 层不绑定存储后端**：`ports.LogStore` interface 的 `Cursor` 是 opaque string，adapter 内部映射为 Loki time / ES search_after / K8s tailLines，port 层不暴露任何存储后端语义
- **adapter 层可选注入**：`PrometheusInstanceObservability` 内部持有 `logStore ports.LogStore` 字段，运行时通过环境变量决定具体实现或 nil
- **未部署存储后端时 fallback**：`logStore == nil` 时 `ListLogs` 走现有 K8s API 逻辑，保证不破坏现有行为
- **部署 yaml 是推荐示例**：保留完整 Loki + Fluent Bit yaml 作为开箱即用方案，但用户也可部署 ES/OpenSearch 替代，只需新增对应 adapter
- **采集器是部署侧组件**：`LogStore` port 不抽象采集层；当前推荐 Fluent Bit，若未来出现强数据清洗需求（字段解析、按字段路由、去重、采样），可切换为 Vector（VRL 原生 transforms 强于 Fluent Bit Lua），只需替换 DaemonSet 配置，不影响查询路径与业务代码

### 4.2 多租户隔离方案（Loki adapter 实现）

**不使用 Loki X-Scope-OrgID 多租户**，改用 namespace label 过滤：

- Fluent Bit 采集时自动把 K8s namespace 作为 Loki label `namespace` 写入
- ANI 的租户 namespace 格式为 `ani-tenant-<tenant_id>`（[dryrun_renderer.go:450-452](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/dryrun_renderer.go)）
- 查询时用 `{namespace="ani-tenant-<tenant_id>",pod="<instance_id>"}` 过滤
- 这与现有 PromQL 租户隔离逻辑（[prometheus_observability_service.go:265](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_observability_service.go) 用 `tenantNamespace(record.TenantID)`）天然契合

> **注意：** 多租户隔离逻辑是 adapter 内部实现细节，不在 port 层暴露。ES adapter 实现时会用等价的 namespace/index 过滤。

### 4.3 部署形态（推荐示例：Loki + Fluent Bit）

> **以下部署为推荐示例，非必须部署。** 用户也可部署 ES/OpenSearch 替代，只需新增 `ElasticsearchLogStore` adapter 并设置 `INSTANCE_OBSERVABILITY_LOG_STORE=elasticsearch`。

**Loki 单体模式（Single Binary）+ Fluent Bit DaemonSet：**

| 组件 | namespace | 镜像 | 说明 |
|---|---|---|---|
| Loki | `ani-s07-observability`（与 Prometheus 同 namespace，复用） | `grafana/loki:3.6.0` | 单进程读写一体，后端用 MinIO S3 兼容存储 |
| Fluent Bit | `ani-s07-observability`（DaemonSet 部署到每个节点） | `fluent/fluent-bit:3.2.0` | 采集 `/var/log/pods/*`，提取 namespace/pod/container 作为 label |
| MinIO | `ani-s05-objectstore`（已部署） | `minio/minio:RELEASE.2025-04-22T22-12-26Z` | Service `ani-s05-minio:9000`，凭据在 Secret `ani-s05-minio-root` |

**存储后端选型（Loki）— ANI-13 §2.0 准入评估：**

| 准入维度 | Loki 评估 | ES/OpenSearch 对比 |
|---|---|---|
| 社区热度 | GitHub 28k+ stars、4k+ forks、1.3k+ contributors；2026-04 仍有最新 release | ES stars 更高但商业版拆分；OpenSearch 是 AWS fork，社区相对分散 |
| 稳定成熟 | 2018 年 KubeCon 发布，Red Hat OpenShift Logging 默认后端，Wix/Paytm/DigitalOcean 生产案例；CNCF 生态 | ES 自 2010 年成熟稳定；OpenSearch 2021 年从 ES 7.10 fork，独立演进时间短 |
| 性能与效率 | 只索引 label（不索引全文），存储压缩 chunk 到 S3；ANI 日志查询场景是"按 namespace+pod 过滤 + 时间范围"，label 查询模式契合；资源占用低于 ES | 全文倒排索引，任意文本查询快；但 50GB/日 规模下实测 ES 内存与存储开销是 Loki 数倍（见 Grafana community 与腾讯云横比） |
| 源码与文档 | Go 实现，源码可读；grafana.com/docs/loki 完整；与 Prometheus 同生态，LogQL 接近 PromQL | Java 实现，文档成熟但体量大；查询 DSL 学习成本高于 LogQL |
| 运维运营 | 单体模式可 1 副本起步，支持 `/ready`、`/healthy` 健康检查；retention 内置；backup 靠 S3 对象生命周期；与 ANI 现有 MinIO 复用 | 需独立 ES 集群 + 索引生命周期管理（ILM）；集群运维复杂度高于 Loki 单体 |
| 可替换性 | AGPLv3 协议（需法务确认，但内部使用无传染风险）；通过 `LogStore` port 隔离，替换为 ES/OpenSearch 只需新增 adapter | Apache 2.0 协议；替换路径反向同理 |

**选择 Loki 作为默认实现的理由：**
1. ANI 日志查询模式是 `按 namespace+pod label 过滤 + 时间范围 + level`，属于 Loki 优化场景，不是 ES 全文检索场景
2. 与现有 Prometheus + MinIO 同生态复用，不引入新存储组件
3. 单体模式资源占用低，与 ANI 现有 `ani-s07-observability` namespace 同部署
4. `LogStore` port 已隔离后端语义，ES adapter 可后补，不锁死选型

**已知取舍：**
- Loki 不擅长任意文本模糊搜索（如全文检索 "connection refused"）；ANI PRD US-008 只要求按时间/级别/容器过滤分页浏览，无全文检索需求
- 大规模（>1TB/日）下 Loki 查询延迟劣于 ES；ANI 多租户单集群规模远未到此量级
- AGPLv3 协议：ANI 内部使用不对外分发 Loki 二进制，无传染风险；若未来对外分发需法务确认

---

**采集器选型（Fluent Bit）：**

| 采集器 | 状态 | 资源 | 配置 | 结论 |
|---|---|---|---|---|
| Promtail | **EOL 2026-03-02** | 低 | YAML | 硬伤：已停止维护 |
| Grafana Alloy | 活跃 | 中 | River 语法（学习成本） | 配置复杂度高 |
| **Fluent Bit** | 活跃、CNCF 毕业 | **最低（C，~5MB）** | YAML | **推荐**：轻量、成熟、无 EOL 风险 |
| Vector | 活跃 | 低 | TOML | 高性能但过重，Datadog 生态 |

选择 Fluent Bit 的理由：CNCF 毕业项目，与 Prometheus 同生态无厂商绑定，C 语言资源占用最低，K8s 日志采集是其核心场景，YAML 配置运维熟悉。

> **前置依赖（人工执行）：**
> 1. **MinIO bucket `ani-loki-logs`**：需先通过 ANI object store API `createStorageBucket` 或 MinIO Console 创建
> 2. **MinIO 持久化风险**：当前 MinIO 数据卷是 `emptyDir`（[sprint13-objectstore-minio-live.yaml:84-85](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-objectstore-minio-live.yaml)），非持久化。MinIO pod 重启后 Loki 日志 chunk 数据会丢失。**需要运维确认是否接受此风险，或先改 MinIO 用 PVC**。此问题超出本方案范围，但必须标注为前置风险。
> 3. **S3 凭据 Secret**：需在 `ani-s07-observability` namespace 重建 MinIO 凭据 Secret（见步骤 1 yaml 注释）

### 4.4 落地步骤

#### 步骤 1：部署 Loki + Fluent Bit（推荐示例）

新增文件：`repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`

以下为**可直接 kubectl apply 的完整 yaml 内容**，与现有 `sprint13-instance-observability-prometheus-live.yaml` 同风格（独立 namespace + Deployment + Service + ConfigMap + Secret + RBAC）。

> **前置准备（人工执行，不在 yaml 内）：**
> 1. MinIO bucket `ani-loki-logs` 需先创建（通过 ANI object store API `createStorageBucket` 或 MinIO Console 手动创建）
> 2. Loki S3 凭据复用 MinIO root secret `ani-s05-minio-root`（已在 [sprint13-objectstore-minio-live.yaml](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-objectstore-minio-live.yaml) 第 48-55 行定义，包含 `access_key_id` 和 `secret_access_key`）。Loki 与 MinIO 同集群，直接跨 namespace 引用此 Secret 需在 `ani-s07-observability` namespace 下重建一份（K8s Secret 不能跨 namespace 直接引用），或用 `externalName` Service 中转。下面 yaml 采用**在 observability namespace 重建 Secret**的方式。

```yaml
# ============================================================
# Sprint 13 切片 07 — 实例可观测性 Loki + Fluent Bit 部署（推荐示例）
# 依赖：MinIO（ani-s05-objectstore namespace）、bucket ani-loki-logs
# 架构：Fluent Bit DaemonSet（采集）→ Loki Deployment（存储到 MinIO S3）
# 注：此 yaml 为推荐示例，用户也可部署 ES/OpenSearch 替代，adapter 层选择对应实现
# ============================================================

# --- Namespace ---
apiVersion: v1
kind: Namespace
metadata:
  name: ani-s07-observability
  labels:
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
# 注：若 Prometheus 已部署在此 namespace，则复用，不重复创建

---
# --- Loki S3 凭据（从 ani-s05-minio-root 复制到本 namespace）---
# 运维需先执行：
#   kubectl get secret ani-s05-minio-root -n ani-s05-objectstore -o yaml | \
#   sed 's/namespace: ani-s05-objectstore/namespace: ani-s07-observability/' | \
#   kubectl apply -f -
# 或手动重建。以下 yaml 假设凭据已就位，此处用占位 Secret 供离线 apply。
apiVersion: v1
kind: Secret
metadata:
  name: ani-loki-s3-creds
  namespace: ani-s07-observability
  labels:
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
type: Opaque
stringData:
  access_key_id: <REPLACE_WITH_MINIO_ROOT_USER>
  secret_access_key: <REPLACE_WITH_MINIO_ROOT_PASSWORD>

---
# --- Loki ConfigMap ---
apiVersion: v1
kind: ConfigMap
metadata:
  name: ani-loki-config
  namespace: ani-s07-observability
  labels:
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
data:
  loki.yaml: |
    # 单租户模式（不用 X-Scope-OrgID，靠 namespace label 过滤租户）
    auth_enabled: false
    server:
      http_listen_port: 3100
    common:
      ring:
        instance_addr: 127.0.0.1
        kvstore:
          store: inmemory
        replication_factor: 1
      path_prefix: /loki
    # schema v13 + tsdb store，与 Loki 3.x 兼容
    schema_config:
      configs:
        - from: 2026-07-17
          store: tsdb
          object_store: s3
          schema: v13
          index:
            prefix: index_
            period: 24h
    storage_config:
      tsdb_shipper:
        active_index_directory: /loki/index
        cache_location: /loki/index_cache
      aws:
        # expanded S3 配置（官方示例 10），指向集群内 MinIO Service
        bucketnames: ani-loki-logs
        endpoint: ani-s05-minio.ani-s05-objectstore:9000
        access_key_id: ${S3_ACCESS_KEY_ID}
        secret_access_key: ${S3_SECRET_ACCESS_KEY}
        # MinIO 用 path-style，必须 true
        s3forcepathstyle: true
        # 内部集群通信，跳过 TLS
        insecure: true
        http_config:
          insecure_skip_verify: true
    # 保留策略 30 天
    limits_config:
      retention_period: 30d
    compactor:
      working_directory: /loki/compactor
      compaction_interval: 5m

---
# --- Loki Deployment ---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ani-loki
  namespace: ani-s07-observability
  labels:
    app.kubernetes.io/name: ani-loki
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: ani-loki
  template:
    metadata:
      labels:
        app.kubernetes.io/name: ani-loki
        ani.dev/sprint: "13"
        ani.dev/slice: s07-instance-observability
    spec:
      containers:
        - name: loki
          image: grafana/loki:3.6.0
          imagePullPolicy: IfNotPresent
          args:
            - -config.file=/etc/loki/loki.yaml
            - -config.expand-env=true
          env:
            # 从 Secret 注入 S3 凭据，ConfigMap 里用 ${S3_ACCESS_KEY_ID} 引用
            - name: S3_ACCESS_KEY_ID
              valueFrom:
                secretKeyRef:
                  name: ani-loki-s3-creds
                  key: access_key_id
            - name: S3_SECRET_ACCESS_KEY
              valueFrom:
                secretKeyRef:
                  name: ani-loki-s3-creds
                  key: secret_access_key
          ports:
            - name: http
              containerPort: 3100
          readinessProbe:
            httpGet:
              path: /ready
              port: http
            initialDelaySeconds: 10
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /healthy
              port: http
            initialDelaySeconds: 30
            periodSeconds: 30
          resources:
            requests:
              cpu: 100m
              memory: 256Mi
            limits:
              cpu: "1"
              memory: 1Gi
          volumeMounts:
            - name: config
              mountPath: /etc/loki
              readOnly: true
            - name: data
              mountPath: /loki
      volumes:
        - name: config
          configMap:
            name: ani-loki-config
        - name: data
          emptyDir: {}
          # 注：emptyDir 非持久化，pod 重启丢本地 index cache；但日志 chunk 存 MinIO，不丢
          # 若需持久化本地 index，改用 PVC（需 StorageClass）

---
# --- Loki Service（集群内访问）---
apiVersion: v1
kind: Service
metadata:
  name: ani-loki
  namespace: ani-s07-observability
  labels:
    app.kubernetes.io/name: ani-loki
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: ani-loki
  ports:
    - name: http
      port: 3100
      targetPort: http

---
# ============================================================
# Fluent Bit DaemonSet：采集 /var/log/pods/* 推送到 Loki
# 选型理由：Promtail EOL 2026-03-02，Fluent Bit 是 CNCF 毕业项目，轻量成熟
# ============================================================

# --- Fluent Bit ServiceAccount + RBAC ---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ani-fluent-bit
  namespace: ani-s07-observability
  labels:
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ani-fluent-bit-reader
  labels:
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
rules:
  - apiGroups: [""]
    resources: ["nodes", "services", "pods"]
    verbs: ["get", "watch", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ani-fluent-bit-reader
  labels:
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
subjects:
  - kind: ServiceAccount
    name: ani-fluent-bit
    namespace: ani-s07-observability
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: ani-fluent-bit-reader

---
# --- Fluent Bit ConfigMap ---
# Fluent Bit 配置分四段：SERVICE（全局）、INPUT（tail 读 K8s pod 日志）、
# FILTER（kubernetes filter 提取 namespace/pod/container label）、OUTPUT（推 Loki）
apiVersion: v1
kind: ConfigMap
metadata:
  name: ani-fluent-bit-config
  namespace: ani-s07-observability
  labels:
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
data:
  fluent-bit.conf: |
    [SERVICE]
      Flush         5
      Log_Level     info
      Parsers_File  parsers.conf

    [INPUT]
      Name              tail
      Path              /var/log/pods/*/*/*.log
      Parser            docker
      Tag               kube.*
      Mem_Buf_Limit     10MB
      Skip_Long_Lines    On
      Refresh_Interval   5

    [FILTER]
      Name                kubernetes
      Match               kube.*
      Kube_URL            https://kubernetes.default.svc:443
      Kube_CA_File        /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
      Kube_Token_File     /var/run/secrets/kubernetes.io/serviceaccount/token
      Kube_Tag_Prefix     kube.var.log.pods.
      Merge_Log           On
      K8S-Logging.Parser  On
      K8S-Logging.Exclude On

    [OUTPUT]
      Name            loki
      Match           kube.*
      Host            ani-loki.ani-s07-observability
      Port            3100
      Labels          namespace=$kubernetes['namespace_name'],pod=$kubernetes['pod_name'],container=$kubernetes['container_name']
      Line_Format     json
      Auto_Kubernetes_Labels Off

---
# --- Fluent Bit DaemonSet ---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: ani-fluent-bit
  namespace: ani-s07-observability
  labels:
    app.kubernetes.io/name: ani-fluent-bit
    ani.dev/sprint: "13"
    ani.dev/slice: s07-instance-observability
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: ani-fluent-bit
  template:
    metadata:
      labels:
        app.kubernetes.io/name: ani-fluent-bit
        ani.dev/sprint: "13"
        ani.dev/slice: s07-instance-observability
    spec:
      serviceAccountName: ani-fluent-bit
      containers:
        - name: fluent-bit
          image: fluent/fluent-bit:3.2.0
          imagePullPolicy: IfNotPresent
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 256Mi
          volumeMounts:
            - name: varlog
              mountPath: /var/log
              readOnly: true
            - name: config
              mountPath: /etc/fluent-bit/
              readOnly: true
            - name: positions
              mountPath: /tmp/fluent-bit/
      volumes:
        - name: varlog
          hostPath:
            path: /var/log
        - name: config
          configMap:
            name: ani-fluent-bit-config
        - name: positions
          emptyDir: {}
```

**部署后验证命令：**

```bash
# 1. 检查 Loki 就绪
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=ani-loki -n ani-s07-observability --timeout=120s
curl http://ani-loki.ani-s07-observability:3100/ready   # 期望 200 "ready"

# 2. 检查 Fluent Bit DaemonSet 就绪
kubectl rollout status daemonset/ani-fluent-bit -n ani-s07-observability

# 3. 检查 Loki labels 是否有 namespace/pod（采集链路通）
curl 'http://ani-loki.ani-s07-observability:3100/loki/api/v1/labels'   # 期望看到 namespace、pod、container

# 4. 查询某 pod 日志（验证多租户过滤）
curl -G 'http://ani-loki.ani-s07-observability:3100/loki/api/v1/query_range' \
  --data-urlencode 'query={namespace="ani-tenant-00000000-0000-0000-0000-000000000001",pod="<实例pod名>"}' \
  --data-urlencode 'limit=100' \
  --data-urlencode 'start=<RFC3339时间戳>'
```

> **注意：** 上述 yaml 中 S3 凭据 Secret `ani-loki-s3-creds` 的 `access_key_id`/`secret_access_key` 是占位值，部署前必须替换为 MinIO 真实凭据（来自 `ani-s05-minio-root` secret）。

#### 步骤 2：新增 LogStore port 抽象

新增文件：`repo/pkg/ports/log_store.go`

```go
// LogStore 是日志持久化存储的 port 抽象
// 不绑定具体存储后端，adapter 层选择具体实现（Loki/ES/K8s API fallback）
type LogStore interface {
    // QueryLogs 按实例分页查询日志，cursor 是 opaque string
    // adapter 内部映射为 Loki time / ES search_after / K8s tailLines，port 层不暴露语义
    QueryLogs(ctx context.Context, req LogQueryRequest) (LogQueryResult, error)
}

type LogQueryRequest struct {
    TenantID   string
    InstanceID string  // pod 名 / VMI 名
    Namespace  string  // ani-tenant-<tenant_id>
    Limit      int
    Cursor     string  // opaque string，空表示从头开始
    Level      string  // 可选过滤
}

type LogQueryResult struct {
    Items      []InstanceLogEntry
    NextCursor string  // opaque string，空表示无更多数据
}
```

#### 步骤 3：实现 Loki adapter

新增文件：`repo/pkg/adapters/runtime/loki_log_store.go`

- 实现 `LogStore` 接口
- 用 Loki HTTP API `/loki/api/v1/query_range` 查询
- LogQL：`{namespace="<namespace>",pod="<instance_id>"} | json`
- cursor opaque 映射：`cursor` 是 RFC3339 时间戳，转换为 Loki `start` 参数（Unix 纳秒）
- `next_cursor` 是结果最后一条的 timestamp
- 解析 Loki 返回的日志行，映射为 `InstanceLogEntry`
- 多租户隔离：通过 LogQL 的 `{namespace="ani-tenant-<tenant_id>"}` label 过滤实现

#### 步骤 4：adapter 注入（环境变量选择）

文件：[instance_observability_runtime.go](file:///e:/go/project/ANI/repo/services/ani-gateway/instance_observability_runtime.go)

在 `newGatewayInstanceObservability` 创建 `PrometheusInstanceObservability` 时，通过环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` 选择 `LogStore` 实现：

```go
func newGatewayInstanceObservability(cfg ...) (ports.InstanceObservability, bool, error) {
    switch provider := strings.TrimSpace(cfg.Provider); provider {
    case "prometheus_kubernetes":
        // 现有逻辑：创建 PrometheusInstanceObservability
        obs, err := runtimeadapter.NewPrometheusInstanceObservability(...)
        if err != nil {
            return nil, false, err
        }
        // 新增：根据环境变量注入 LogStore
        logStoreType := os.Getenv("INSTANCE_OBSERVABILITY_LOG_STORE")
        switch logStoreType {
        case "loki":
            obs.SetLogStore(runtimeadapter.NewLokiLogStore(lokiEndpoint))
        case "elasticsearch":
            obs.SetLogStore(runtimeadapter.NewElasticsearchLogStore(esEndpoint))
        case "", "k8s", "not_configured":
            // nil，ListLogs 走 K8s API fallback
        }
        return obs, true, nil
    }
}
```

**`PrometheusInstanceObservability` 结构体扩展：**

```go
type PrometheusInstanceObservability struct {
    // ... 现有字段 ...
    logStore ports.LogStore  // 可选，nil 时 fallback 到 K8s API
}

// SetLogStore 注入日志存储实现（由 runtime 在创建时调用）
func (o *PrometheusInstanceObservability) SetLogStore(store ports.LogStore) {
    o.logStore = store
}
```

#### 步骤 5：修改 ListLogs 走 LogStore（带 fallback）

文件：[prometheus_instance_observability.go:76-92](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_instance_observability.go)

`ListLogs` 优先调 `logStore`，nil 时 fallback 到现有 K8s API：

```go
func (o *PrometheusInstanceObservability) ListLogs(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceLogListResult, error) {
    // 优先走持久化存储
    if o.logStore != nil {
        result, err := o.logStore.QueryLogs(ctx, ports.LogQueryRequest{
            TenantID:   request.TenantID,
            InstanceID: request.InstanceID,
            Namespace:  tenantNamespace(request.TenantID),
            Limit:      request.Limit,
            Cursor:     request.Cursor,
            Level:      request.Level,
        })
        if err != nil {
            return ports.InstanceLogListResult{}, err
        }
        // 映射 LogQueryResult → InstanceLogListResult
        return ports.InstanceLogListResult{Items: result.Items, NextCursor: result.NextCursor}, nil
    }
    // fallback：现有 K8s pod log API 逻辑（未部署存储后端时）
    return o.listLogsFromK8sAPI(ctx, request)
}
```

> **fallback 设计理由：** 不破坏现有行为。未设置 `INSTANCE_OBSERVABILITY_LOG_STORE` 或存储后端未部署时，ListLogs 走现有 K8s API 逻辑，保证向后兼容。

#### 步骤 6：前端 cursor 处理

文件：[LogsTab.tsx](file:///e:/go/project/ANI/repo/frontends/console/src/features/instance-observability/LogsTab.tsx)

当前 `useInfiniteQuery` 的 `getNextPageParam` 收到 `next_cursor: nil` 停止分页。接 Loki 后 `next_cursor` 是 opaque string（非 nil），前端继续调用。需要确认：
- `getNextPageParam` 逻辑：`return lastPage.next_cursor || undefined`（当前已是此逻辑，无需改动）
- `fetchNextPage` 传参：`{ cursor: pageParam }`（当前已是此逻辑，无需改动）

**预期前端无需改动**，cursor 从"无值"变成"opaque string"对 `useInfiniteQuery` 透明。

### 4.5 验证标准

**未部署存储后端（fallback 模式）：**
- [ ] `INSTANCE_OBSERVABILITY_LOG_STORE` 未设置时，ListLogs 走 K8s API，行为与现状一致
- [ ] 现有 LogsTab 功能不受影响

**部署 Loki（推荐模式）：**
- [ ] Loki 健康检查通过（`/ready` 返回 200）
- [ ] Fluent Bit 采集到 pod 日志（Loki `/loki/api/v1/labels` 有 namespace/pod label）
- [ ] 创建一个 pod → 写日志 → 删除 pod → 通过 Loki 仍可查到该 pod 日志
- [ ] Console LogsTab 真分页：首页加载 100 条，"加载更多"加载下一页，cursor 传递正确
- [ ] 跨租户隔离：租户 A 查不到租户 B 的日志（namespace label 过滤）
- [ ] 日志保留 30 天后自动清理（Loki retention 生效）

---

## 五、方案三：VM 指标采集（KubeVirt `kubevirt_vmi_*`）

### 5.1 KubeVirt 官方指标名（已查证）

> 来源：[KubeVirt 官方 metrics 总表](https://kubevirt.io/monitoring/metrics.html)

**关键纠正：`kubevirt_vmi_memory_used_bytes` 不是官方标准指标名。**

| 资源 | 指标名 | 类型 | 说明 |
|---|---|---|---|
| CPU 使用 | `kubevirt_vmi_cpu_usage_seconds_total` | Counter | 需 `rate()` 得 CPU 秒/秒 |
| 内存已用 | `kubevirt_vmi_memory_resident_bytes` | Gauge | RSS，最接近"已用内存" |
| 内存总量 | `kubevirt_vmi_memory_domain_bytes` | Gauge | domain 分配总量 |
| 内存可用 | `kubevirt_vmi_memory_usable_bytes` | Gauge | 可用内存 |
| 网络 RX | `kubevirt_vmi_network_receive_bytes_total` | Counter | 需 `rate()` 得 bps |
| 网络 TX | `kubevirt_vmi_network_transmit_bytes_total` | Counter | 需 `rate()` 得 bps |

**指标 label：** `namespace`（K8s namespace）、`name`（VMI 名，不是 pod 名）

**内存使用率公式（官方推荐）：**
```promql
kubevirt_vmi_memory_domain_bytes{namespace="...",name="..."}
- kubevirt_vmi_memory_usable_bytes{namespace="...",name="..."}
```

### 5.2 落地步骤

#### 步骤 1：修改 Prometheus scrape 配置

文件：[sprint13-instance-observability-prometheus-live.yaml](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml)

在 `scrape_configs` 下新增 virt-handler job：

```yaml
- job_name: kubevirt-virt-handler
  scheme: https
  metrics_path: /metrics
  bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
  tls_config:
    insecure_skip_verify: true
  kubernetes_sd_configs:
    - role: pod
      namespaces:
        names: [kubevirt]
  relabel_configs:
    - source_labels: [__meta_kubernetes_pod_label_kubevirt_io]
      action: keep
      regex: virt-handler
    - source_labels: [__meta_kubernetes_pod_container_port_number]
      action: keep
      regex: "8443"
```

> **注意：** 需要确认 Prometheus 的 ClusterRole 有权 list/watch `kubevirt` namespace 的 pods。当前 [sprint13-instance-observability-prometheus-live.yaml:18-46](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml) 的 ClusterRole 只授权了 `nodes/metrics` 和 `/metrics/cadvisor`，需要新增 `pods` 读权限（或确认已有）。

#### 步骤 2：修改 GetMetrics 加 VM 分支

文件：[prometheus_instance_observability.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_instance_observability.go)

在 `GetMetrics` 方法中，`if request.Kind == ports.WorkloadKindGPUContainer` 之前新增 VM 分支：

```go
if request.Kind == ports.WorkloadKindVM {
    // VM 指标使用 kubevirt_vmi_* 系列，label 是 name=<vmi-name>，不是 pod
    vmiMatcher := fmt.Sprintf(`name="%s"`, request.InstanceID) // InstanceID 此时是 VM name
    // CPU: rate(kubevirt_vmi_cpu_usage_seconds_total[5m])
    // 内存已用: kubevirt_vmi_memory_resident_bytes
    // 内存总量: kubevirt_vmi_memory_domain_bytes
    // 网络 RX/TX: rate(kubevirt_vmi_network_receive_bytes_total[5m]) 等
    // ...查询逻辑与 container 分支类似，但指标名和 label 不同
}
```

**关键区别：**
- VM 指标的 label 是 `name="<vmi-name>"`，不是 `pod=~"instanceID(-.*)?"`
- CPU/网络是 Counter，快照要用 `rate()` 或 `increase()`，不能直接 sum 累计值
- 内存是 Gauge，可直接读值

**VMI 命名规则（已确认）：**
- ANI `kind=vm` 实例创建的 VMI `metadata.name` **不带随机后缀**，直接等于用户传入的实例名（只做 TrimSpace）
- 证据链：[demo_instances.go:993-996](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/demo_instances.go) handler → [dryrun_renderer.go:139](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/dryrun_renderer.go) `"name": spec.Name` → KubeVirt 控制器同名创建 VMI
- [demo_instances.go:868-873](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/demo_instances.go) 的 `observabilityTargetID` 在 `observabilityUsesInstanceName` 且 `record.Name` 非空时用 `record.Name`，与 VMI `metadata.name` 一致
- 因此 VM 指标 label 用精确匹配 `name="<record.Name>"`，不需要正则后缀匹配
- 注意区分：节点池（`k8s_cluster` node pool）路径的 VMI 带 CAPI/CAPk 两段随机后缀（如 `gpu-pool-live-v89lr-6256b`），但那是 `k8s_cluster` 不是 `kind=vm`，不在本方案范围

**验证前置依赖：** 真实环境需存在通过 ANI 实例 API 创建的 `kind=vm` 实例才能端到端验证。若真实环境只有节点池 VM，只能验证指标查询链路（Prometheus 可查 `kubevirt_vmi_*`），无法验证实例详情页真实数据展示。

#### 步骤 3：修复 handler 传 Kind（与 GPU 共用，见方案一步骤 1）

#### 步骤 4：更新 PromQL 模板（时序图）

文件：[promqlTemplates.ts](file:///e:/go/project/ANI/repo/frontends/console/src/features/instance-observability/promqlTemplates.ts)

为 VM kind 新增冻结 PromQL 模板，注入 `name` label 而非 `pod`：

```typescript
// VM CPU 利用率
`rate(kubevirt_vmi_cpu_usage_seconds_total{namespace="{{namespace}}",name="{{instance_id}}"}[5m])`
// VM 内存使用率
`(kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"} - kubevirt_vmi_memory_usable_bytes{namespace="{{namespace}}",name="{{instance_id}}"}) / kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"}`
```

> **注意：** PromQL 模板的 label 重写逻辑（[prometheus_observability_service.go:233-285](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_observability_service.go)）当前只重写 `namespace` 和 `pod` label。VM 指标用 `name` label，需要扩展重写逻辑支持 `name` label，或者在 VM 模板里直接用 `{{namespace}}` 和 `{{instance_id}}` 占位符由前端注入。

### 5.3 验证标准

- [ ] Prometheus `kubevirt_vmi_cpu_usage_seconds_total` 等指标可查到（`curl Prometheus /api/v1/query?query=kubevirt_vmi_cpu_usage_seconds_total`）
- [ ] VM 实例详情指标 Tab 快照卡片展示 CPU/内存/网络真实数据，非 null
- [ ] VM 指标时序图展示 CPU 利用率、内存使用率曲线
- [ ] 非 VM kind（container 等）不受影响，仍走 container_* 指标

---

## 六、实施建议（供人工审核）

### 不建议分期，直接实施完整方案

**原分期建议（`--previous` 止血 + Loki 二期）已废弃**，理由：
- `kubectl logs --previous` 只能拿到**最近一次**终止容器日志，容器重启 ≥2 次后更早的日志丢失
- 即第一期也无法完整覆盖"pod 重启"场景，留有缺口
- 且第一期与第二期语义不一致（K8s API vs Loki），增加维护成本

**直接实施完整方案（按依赖分批）：**
- **第一批（立即可做）**：
  - 方案一 GPU 指标：handler 传 Kind + DCGM scrape，与平台 VM 功能无关
  - 方案二 日志持久化：新增 LogStore port + Loki adapter + Fluent Bit 部署，与平台 VM 功能无关
- **第二批（依赖平台 VM 功能就绪）**：
  - 方案三 VM 指标：需真实环境存在通过 ANI 实例 API 创建的 `kind=vm` 实例才能端到端验证
  - 当前平台 VM 功能尚未完成，VM 指标方案暂缓实施，等平台 VM 功能就绪后再做
  - VM 方案的代码改动（GetMetrics VM 分支、PromQL 模板、label 重写）可先准备好，但不在当前迭代合入

**审核决策点：**
- GPU 和日志方案可以分两个 PR，GPU 先合，日志后合
- VM 方案不在当前迭代实施，等平台 VM 功能就绪后再启动
- 日志方案中 Loki adapter 和 ES adapter 可以分批实现，先 Loki，ES 后补

---

## 七、改动文件清单汇总

| 方案 | 文件 | 改动类型 |
|---|---|---|
| GPU+VM | [demo_instances.go](file:///e:/go/project/ANI/repo/services/ani-gateway/internal/router/demo_instances.go) | 修改 getMetrics 传 Kind |
| GPU+VM | [sprint13-instance-observability-prometheus-live.yaml](file:///e:/go/project/ANI/repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml) | 新增 DCGM + virt-handler scrape |
| GPU | [prometheus_instance_observability.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_instance_observability.go) | 验证现有 GPU 分支（指标名已正确，依赖 handler 传 Kind） |
| 日志 | `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml` | 新增 Loki + Fluent Bit 完整部署（推荐示例） |
| 日志 | `repo/pkg/ports/log_store.go` | 新增 LogStore port（opaque cursor，不绑定后端） |
| 日志 | `repo/pkg/adapters/runtime/loki_log_store.go` | 新增 Loki adapter（实现 LogStore） |
| 日志 | [instance_observability_runtime.go](file:///e:/go/project/ANI/repo/services/ani-gateway/instance_observability_runtime.go) | 环境变量注入 LogStore 到 PrometheusInstanceObservability |
| 日志 | [prometheus_instance_observability.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_instance_observability.go) | 加 logStore 字段 + SetLogStore + ListLogs 带 fallback |
| VM | [prometheus_instance_observability.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_instance_observability.go) | GetMetrics 加 VM 分支 |
| VM | [promqlTemplates.ts](file:///e:/go/project/ANI/repo/frontends/console/src/features/instance-observability/promqlTemplates.ts) | 新增 VM PromQL 模板 |
| VM | [prometheus_observability_service.go](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/prometheus_observability_service.go) | rewritePromQLLabels 支持 name label |

---

## 八、风险与前置依赖

| 风险 | 影响 | 缓解 |
|---|---|---|
| DCGM exporter 网络不可达或 Service 地址变更 | GPU 指标 scrape 失败 | 部署前 `curl ani-dcgm-exporter.ani-system:9400/metrics` 确认可达 |
| MinIO 数据卷是 emptyDir（非持久化） | Loki 后端数据可能丢失 | 运维确认 MinIO 持久化方案，或接受临时部署仅用于验证 |
| Loki + Fluent Bit 是新组件，增加运维复杂度 | 部署/运维成本 | 部署 yaml 为推荐示例，用户可选 ES/OpenSearch 替代；fallback 机制保证未部署时不破坏现有行为 |
| ES adapter 未实现 | `INSTANCE_OBSERVABILITY_LOG_STORE=elasticsearch` 无对应实现 | 先实现 Loki adapter，ES adapter 后补；未实现时该 env 值走 fallback |
| 采集器替换（Fluent Bit ↔ Vector）需保持 Loki labels 字段一致 | 替换后查询匹配不上 | 历史日志无需迁移，但替换期间可能存在两套采集器并存的双写窗口，需运维侧控制切换节奏 |
| Prometheus ClusterRole 无 kubevirt namespace pods 读权限 | virt-handler scrape 失败 | 部署前确认/新增 RBAC |
| KubeVirt VMI `metadata.name` 与 ANI `record.Name` 映射不确定 | VM 指标 label 匹配失败 | 实现前确认 KubeVirt VM 创建逻辑中 VMI 命名规则 |
| KubeVirt 指标 `name` label vs PromQL 重写逻辑只支持 `pod` label | VM 时序图 label 重写失败 | 扩展 rewritePromQLLabels 或 VM 模板用 `{{instance_id}}` 占位符 |

---

## 九、References

- [KubeVirt 官方 metrics 总表](https://kubevirt.io/monitoring/metrics.html)
- [KubeVirt component monitoring 用户指南](https://kubevirt.io/user-guide/user_workloads/component_monitoring/)
- [Prometheus queries for virtual resources - OKD](https://docs.okd.io/4.20/virt/monitoring/virt-prometheus-queries.html)
- [Loki 官方文档](https://grafana.com/docs/loki/latest/)
- [Loki S3 配置示例](https://grafana.com/docs/loki/latest/configure/examples/configuration-examples/)
- [Fluent Bit 官方文档](https://docs.fluentbit.io/)
- [Fluent Bit Loki output plugin](https://docs.fluentbit.io/manual/pipeline/outputs/loki)
- [Promtail EOL 公告](https://grafana.com/docs/loki/latest/send-data/promtail/installation/)
