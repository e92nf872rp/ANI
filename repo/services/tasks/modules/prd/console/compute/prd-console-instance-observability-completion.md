# PRD: 实例可观测性补全（GPU 指标 / 日志持久化 / VM 指标）

> 基于 `prd-console-instance-observability.md` 当前实现现状，闭环三个未解决问题。  
> Source plan: `repo/services/tasks/modules/prd/console/compute/plan.md`  
> 生成日期：2026-07-20  
> 状态：已确认

---

## 1. Introduction / Overview

`prd-console-instance-observability.md` 已确认并落地了统一观测框架的 Core API 契约和 Console UI 壳层，但仍有三个实际链路未闭环：

1. **GPU 指标死分支**：Gateway handler `getMetrics` 调用 adapter 时未透传 `record.Kind`，导致 `PrometheusInstanceObservability.GetMetrics` 的 GPU 分支恒不触发，`gpu_container` 实例的 GPU 利用率与显存指标在 Console 指标 Tab 中始终为 null。
2. **日志无持久化**：`ListLogs` 直接代理 K8s pod log API，Pod 重启/删除后历史日志丢失，且 K8s API 不支持 cursor 分页，PRD US-008 的真分页当前是前端模拟。
3. **VM 指标采集链路缺失**：`GetMetrics` 对 `kind=vm` 走 container 专用的 cAdvisor 指标，反映的是 `virt-launcher` QEMU 进程 cgroup 数据，不是 VM guest OS 的真实资源使用。

本 PRD 覆盖 **Core handler 修复 + Core adapter 扩展 + Console 前端适配 + 推荐部署示例 yaml**，联合交付以闭环上述三个问题。

---

## 2. Goals

- 修复 Gateway handler `getMetrics` 透传 `record.Kind`，使 GPU/VM adapter 分支在生产路径下触发
- 为 `gpu_container` 实例在指标 Tab 快照卡片和时序图展示真实 GPU 利用率与显存 used/total（数据源 DCGM exporter）
- 引入 `ports.LogStore` port 抽象，使日志查询链路与存储后端解耦；通过环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` 选择具体实现，未配置时 fallback 到 K8s pod log API
- 提供基于 Loki + Fluent Bit 的推荐部署示例 yaml（开箱即用），并保证用户可部署 ES/OpenSearch 替代，只需新增对应 adapter
- 实现 Pod 重启或删除后仍可分页浏览历史日志，cursor 为 opaque string，真分页语义明确
- 为 `kind=vm` 实例的指标 Tab 展示 guest OS 真实资源数据（CPU/内存/网络），数据源 KubeVirt `kubevirt_vmi_*` 指标
- 跨租户隔离保持现有 `tenantNamespace(record.TenantID)` 逻辑，不回退既有契约

---

## 3. User Stories

### US-001: Core — Gateway handler 透传 record.Kind

**Description:** 作为平台运维者，我希望 `getMetrics` handler 把实例的 `record.Kind` 透传到 `InstanceObservationGetRequest.Kind`，以便 adapter 的 GPU/VM 分支在生产路径下能正确触发。

**Acceptance Criteria:**
- [ ] 修改 `repo/services/ani-gateway/internal/router/demo_instances.go` 的 `getMetrics` 调用，在 `InstanceObservationGetRequest` 中新增 `Kind: record.Kind` 字段
- [ ] 修改后 `request.Kind` 在生产路径下等于 `record.Kind`（`container` / `gpu_container` / `vm` 等），不再是空字符串
- [ ] 现有 `container` kind 的指标行为不回归（分支逻辑不变）
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 `kind=container`、`kind=gpu_container`、`kind=vm` 三种路径，断言传入 adapter 的 `request.Kind` 值

### US-002: Core — Prometheus 新增 DCGM scrape 配置

**Description:** 作为平台运维者，我希望 Prometheus 配置新增 DCGM exporter 的 scrape job，以便 `DCGM_FI_DEV_GPU_UTIL` 等 GPU 指标可被采集和查询。

**Acceptance Criteria:**
- [ ] 修改 `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml`，在 `scrape_configs` 下新增 `job_name: dcgm-exporter`
- [ ] scrape 配置使用 `static_config` 指向 `ani-dcgm-exporter.ani-system:9400`，`metrics_path: /metrics`
- [ ] 部署后 `curl http://prometheus.ani-s07-observability:9090/api/v1/query?query=DCGM_FI_DEV_GPU_UTIL` 返回非空结果
- [ ] 不修改 Prometheus ClusterRole（`static_config` 直接 HTTP 访问 Service，不需要跨 namespace RBAC）
- [ ] `make validate-architecture` 通过

### US-003: Core — 验证 GPU adapter 分支端到端

**Description:** 作为 `gpu_container` 实例用户，我希望指标 Tab 快照卡片展示真实 GPU 利用率与显存 used/total，而不是 null。

**Acceptance Criteria:**
- [ ] `repo/pkg/adapters/runtime/prometheus_instance_observability.go` 现有 GPU 分支（`DCGM_FI_DEV_GPU_UTIL`、`DCGM_FI_DEV_FB_USED`、`DCGM_FI_DEV_FB_TOTAL`）依赖 US-001 修复后触发，指标名无需改动
- [ ] `kind=gpu_container` 实例调用 `GET /api/v1/instances/{instance_id}/metrics` 时，GPU 相关字段（利用率、显存 used/total）为非 null 值（DCGM exporter 可用时）
- [ ] `kind != gpu_container` 实例调用同一接口时，GPU 字段为 null（分支不触发）
- [ ] Typecheck/lint passes
- [ ] 集成测试覆盖 `kind=gpu_container` 路径，断言 GPU 字段非 null

### US-004: Core — 新增 LogStore port 抽象

**Description:** 作为平台开发者，我希望引入 `ports.LogStore` 接口抽象日志持久化存储，以便 adapter 层选择具体实现（Loki/ES/K8s API fallback），port 层不暴露存储后端语义。

**Acceptance Criteria:**
- [ ] 新增文件 `repo/pkg/ports/log_store.go`，定义 `LogStore` interface、`LogQueryRequest`、`LogQueryResult` 结构
- [ ] `LogStore.QueryLogs(ctx, req)` 方法签名：入参含 `TenantID`、`InstanceID`、`Namespace`、`Limit`、`Cursor`、`Level`；出参含 `Items []InstanceLogEntry`、`NextCursor string`
- [ ] `Cursor` 为 opaque string，port 层不约束其内部语义（adapter 内部映射为 Loki time / ES search_after / K8s tailLines）
- [ ] 不修改现有 `InstanceObservability` interface（LogStore 是内部组合，不对外暴露）
- [ ] Typecheck/lint passes
- [ ] `make validate-architecture` 通过（port 抽象符合 ports/adapters 规则）

### US-005: Core — 实现 Loki adapter

**Description:** 作为平台运维者，我希望在部署 Loki 时，`ListLogs` 能通过 Loki HTTP API 查询持久化日志，以便 Pod 删除后仍可浏览历史日志。

**Acceptance Criteria:**
- [ ] 新增文件 `repo/pkg/adapters/runtime/loki_log_store.go`，实现 `LogStore` interface
- [ ] 使用 Loki HTTP API `/loki/api/v1/query_range`，LogQL：`{namespace="<namespace>",pod="<instance_id>"} | json`
- [ ] cursor 映射：`cursor` 是 RFC3339 时间戳，adapter 内部转换为 Loki `start` 参数（Unix 纳秒）；`next_cursor` 是结果最后一条的 timestamp
- [ ] 多租户隔离：通过 LogQL 的 `{namespace="ani-tenant-<tenant_id>"}` label 过滤，不使用 Loki X-Scope-OrgID
- [ ] 解析 Loki 返回的日志行，映射为 `InstanceLogEntry`（timestamp、level、message、container/stream）
- [ ] Loki 不可达时返回包装错误，不伪造空结果
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 LogQL 构造、cursor 双向映射、Loki HTTP 响应解析

### US-006: Core — PrometheusInstanceObservability 注入 LogStore（带 fallback）

**Description:** 作为平台运维者，我希望 `PrometheusInstanceObservability` 在创建时根据环境变量注入 `LogStore` 实现，未配置时 fallback 到 K8s API，以便不破坏现有行为。

**Acceptance Criteria:**
- [ ] `PrometheusInstanceObservability` 结构体新增 `logStore ports.LogStore` 字段（可选，nil 时 fallback）
- [ ] 新增 `SetLogStore(store ports.LogStore)` 方法，由 runtime 在创建时调用
- [ ] 修改 `repo/services/ani-gateway/instance_observability_runtime.go`，在 `newGatewayInstanceObservability` 中根据环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` 选择实现：`loki` → `LokiLogStore`；`elasticsearch` → 暂未实现走 fallback；空/`k8s`/`not_configured` → nil
- [ ] `ListLogs` 逻辑：`logStore != nil` 时调 `logStore.QueryLogs`，nil 时 fallback 到现有 K8s pod log API 逻辑
- [ ] 未设置 `INSTANCE_OBSERVABILITY_LOG_STORE` 时，`ListLogs` 行为与现状完全一致
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 fallback 路径和注入路径

### US-007: Core — 推荐部署示例 yaml（Loki + Fluent Bit）

**Description:** 作为平台运维者，我希望获得一份开箱即用的 Loki + Fluent Bit 部署 yaml，以便快速启用日志持久化；同时保留替换为 ES/OpenSearch 的能力。

**Acceptance Criteria:**
- [ ] 新增文件 `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`，包含 Namespace、Loki Deployment/Service/ConfigMap/Secret、Fluent Bit DaemonSet/ServiceAccount/ClusterRole/ClusterRoleBinding/ConfigMap
- [ ] Loki 镜像 `grafana/loki:3.6.0`，单租户模式（`auth_enabled: false`），后端 MinIO S3（`bucketnames: ani-loki-logs`，`endpoint: ani-s05-minio.ani-s05-objectstore:9000`，`s3forcepathstyle: true`）
- [ ] Loki schema v13 + tsdb store，`retention_period: 30d`
- [ ] Fluent Bit 镜像 `fluent/fluent-bit:3.2.0`，DaemonSet 部署，采集 `/var/log/pods/*`，提取 namespace/pod/container 作为 Loki label
- [ ] yaml 头部明确标注「推荐示例，非必须部署」，注释说明可替换为 ES/OpenSearch（只需新增 adapter）
- [ ] S3 凭据 Secret `ani-loki-s3-creds` 用占位值，注释说明部署前必须替换为 MinIO 真实凭据（来自 `ani-s05-minio-root`）
- [ ] yaml 头部标注前置依赖：MinIO bucket `ani-loki-logs` 需先创建、MinIO 数据卷 emptyDir 非持久化风险需运维确认
- [ ] 部署后验证命令在 yaml 注释或同级文档中给出：`kubectl wait ... /ready` 返回 200、`curl /loki/api/v1/labels` 返回 namespace/pod label、`query_range` 可查到指定 pod 日志

### US-008: Core — Prometheus 新增 KubeVirt virt-handler scrape 配置

**Description:** 作为平台运维者，我希望 Prometheus 配置新增 KubeVirt virt-handler 的 scrape job，以便 `kubevirt_vmi_*` 指标可被采集和查询。

**Acceptance Criteria:**
- [ ] 修改 `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml`，在 `scrape_configs` 下新增 `job_name: kubevirt-virt-handler`
- [ ] scrape 配置使用 `kubernetes_sd_configs`（role: pod，namespace: kubevirt），`bearer_token_file` + `tls_config.insecure_skip_verify`，`metrics_path: /metrics`
- [ ] `relabel_configs` 过滤 `kubevirt.io=virt-handler` label 且端口为 8443
- [ ] 若 Prometheus ClusterRole 无 `kubevirt` namespace pods 读权限，新增对应权限（或确认已有）
- [ ] 部署后 `curl http://prometheus.ani-s07-observability:9090/api/v1/query?query=kubevirt_vmi_cpu_usage_seconds_total` 返回非空结果
- [ ] `make validate-architecture` 通过

### US-009: Core — GetMetrics 新增 VM 分支

**Description:** 作为 `kind=vm` 实例用户，我希望指标 Tab 展示 guest OS 真实资源数据（CPU/内存/网络），而不是 virt-launcher QEMU 进程的 cgroup 数据。

**Acceptance Criteria:**
- [ ] `repo/pkg/adapters/runtime/prometheus_instance_observability.go` 的 `GetMetrics` 方法新增 `if request.Kind == ports.WorkloadKindVM` 分支，位于现有 GPU 分支之前
- [ ] VM 分支查询指标：`kubevirt_vmi_cpu_usage_seconds_total`（CPU，Counter，快照用 `rate(...[5m])`）、`kubevirt_vmi_memory_resident_bytes`（内存已用，Gauge）、`kubevirt_vmi_memory_domain_bytes`（内存总量，Gauge）、`kubevirt_vmi_network_receive_bytes_total` / `kubevirt_vmi_network_transmit_bytes_total`（网络，Counter，快照用 `rate(...[5m])`）
- [ ] VM 指标 label 用 `name="<vmi-name>"` 精确匹配，不用 `pod=~"..."` 正则
- [ ] VMI `metadata.name` 等于 `record.Name`（已确认无随机后缀），VM 指标用 `request.InstanceID` 作为 `name` label 值
- [ ] 内存使用率公式：`kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes`
- [ ] `kind != vm` 实例不受影响，仍走现有 container/GPU 分支
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 VM 分支的 PromQL 构造和 label 匹配

### US-010: Core — PromQL label 重写支持 name label

**Description:** 作为平台开发者，我希望 `rewritePromQLLabels` 支持 `name` label 重写，以便 VM 时序图模板的 `name` label 能正确注入 `instance_id`。

**Acceptance Criteria:**
- [ ] 修改 `repo/pkg/adapters/runtime/prometheus_observability_service.go` 的 `rewritePromQLLabels`，支持 `name` label 重写（当前只支持 `namespace` 和 `pod`）
- [ ] 或采用替代方案：VM 模板直接用 `{{namespace}}` 和 `{{instance_id}}` 占位符由前端注入，不依赖后端重写（二选一，由 SPEC 定）
- [ ] 现有 `container` / `gpu_container` 的 `pod` label 重写不回归
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 `name` label 重写路径

### US-011: Console — VM 指标 PromQL 模板与时序图

**Description:** 作为 `kind=vm` 实例用户，我希望指标 Tab 时序图展示 VM CPU 利用率、内存使用率曲线（基于 KubeVirt `kubevirt_vmi_*` 指标，使用 `name` label）。VM 时序图只展示 2 条曲线（CPU 利用率、内存使用率），不展示网络 RX/TX 时序曲线（网络数据仅在快照卡片中展示）。

**Acceptance Criteria:**
- [ ] `repo/frontends/console/src/features/instance-observability/promqlTemplates.ts` 新增 VM kind 的冻结 PromQL 模板，使用 `name` label 而非 `pod`
- [ ] VM CPU 利用率模板：`rate(kubevirt_vmi_cpu_usage_seconds_total{namespace="{{namespace}}",name="{{instance_id}}"}[5m])`
- [ ] VM 内存使用率模板：`(kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"} - kubevirt_vmi_memory_usable_bytes{namespace="{{namespace}}",name="{{instance_id}}"}) / kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"}`
- [ ] `getTemplatesForKind` 对 `kind=vm` 返回 VM 模板列表（2 条曲线：CPU 利用率、内存使用率），而非现有 container 模板
- [ ] VM kind 时序图不展示网络 RX/TX 曲线（网络数据仅在快照卡片中展示）
- [ ] 非 VM kind 不展示 VM 模板曲线
- [ ] Typecheck/lint passes
- [ ] 在浏览器中通过可用的 browser automation 工具或 MCP 验证 loading / empty / error 三态；若不可用则记录手动验证步骤

### US-012: Console — VM 指标 Tab 快照卡片（依赖 US-009 后端修复）

**Description:** 作为 `kind=vm` 实例用户，我希望指标 Tab 快照卡片展示 guest OS 真实 CPU/内存/网络数据。Console 端卡片渲染逻辑已通用就绪（`MetricsSnapshot.tsx` 对所有 kind 通用渲染 CPU/内存/网络卡片），本 story 的剩余工作是确保 US-009 完成后 VM 快照数据从 kubevirt_vmi 指标返回，而非 QEMU cgroup 数据。

**Acceptance Criteria:**
- [ ] US-009 完成后，`kind=vm` 实例调用 `GET /api/v1/instances/{instance_id}/metrics` 时 CPU/内存/网络字段为非 null 值，且数据来源是 `kubevirt_vmi_*` 指标（不是 `container_*` cgroup 指标）
- [ ] VM 快照卡片通过 `MetricsSnapshot.tsx` 通用渲染，展示 CPU 利用率、内存 used/total、网络 RX/TX
- [ ] KubeVirt virt-handler 不可用时字段为 null，卡片显示「暂不可用」，不伪造 0（已实现，无新增工作）
- [ ] 非 VM kind 不走 VM 分支，卡片行为不回归（依赖 US-009 的分支隔离）
- [ ] Typecheck/lint passes
- [ ] 在浏览器中通过可用的 browser automation 工具或 MCP 验证 VM 实例指标 Tab 的 loading / partial-null / error 三态；若不可用则记录手动验证步骤

---

## 4. Functional Requirements

- **FR-1:** Gateway handler `getMetrics` 必须把 `record.Kind` 透传到 `InstanceObservationGetRequest.Kind`，不得遗漏。
- **FR-2:** Prometheus scrape 配置必须新增 `dcgm-exporter` job，`static_config` 指向 `ani-dcgm-exporter.ani-system:9400`。
- **FR-3:** `kind=gpu_container` 实例的 `getInstanceMetrics` 必须返回 GPU 利用率与显存 used/total（DCGM 可用时）；DCGM 不可用时为 null，不得伪造 0。
- **FR-4:** `kind != gpu_container` 实例的 `getInstanceMetrics` GPU 字段必须为 null。
- **FR-5:** 系统必须新增 `ports.LogStore` interface，`Cursor` 为 opaque string，port 层不绑定存储后端语义。
- **FR-6:** `PrometheusInstanceObservability` 必须持有可选 `logStore ports.LogStore` 字段，nil 时 fallback 到 K8s pod log API。
- **FR-7:** runtime 必须根据环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` 选择 `LogStore` 实现：`loki` → `LokiLogStore`；未配置/未实现 → nil（fallback）。
- **FR-8:** `ListLogs` 在 `logStore != nil` 时必须调 `logStore.QueryLogs`，不得同时走 K8s API。
- **FR-9:** Loki adapter 必须通过 LogQL `{namespace="ani-tenant-<tenant_id>",pod="<instance_id>"}` 实现多租户隔离，不得使用 Loki X-Scope-OrgID。
- **FR-10:** Loki adapter 的 cursor 必须是 opaque string，内部映射为 Loki `start` 参数（Unix 纳秒），`next_cursor` 为结果最后一条的 timestamp。
- **FR-11:** 系统必须提供 `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml` 推荐示例，包含 Loki + Fluent Bit 完整部署，标注「推荐示例，非必须部署」。
- **FR-12:** 推荐示例 yaml 必须复用现有 MinIO S3（`ani-loki-logs` bucket），不得引入新的对象存储组件。
- **FR-13:** 推荐示例 yaml 的 S3 凭据必须用占位值，注释说明部署前必须替换为 MinIO 真实凭据。
- **FR-14:** Prometheus scrape 配置必须新增 `kubevirt-virt-handler` job，采集 `kubevirt_vmi_*` 指标。
- **FR-15:** `GetMetrics` 必须为 `kind=vm` 新增独立分支，查询 `kubevirt_vmi_cpu_usage_seconds_total`、`kubevirt_vmi_memory_resident_bytes`、`kubevirt_vmi_memory_domain_bytes`、`kubevirt_vmi_network_receive_bytes_total`、`kubevirt_vmi_network_transmit_bytes_total`。
- **FR-16:** VM 指标 label 必须用 `name="<vmi-name>"` 精确匹配，不得用 `pod=~"..."` 正则。
- **FR-17:** VM 内存使用率必须用公式 `kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes` 计算，不得直接用 `kubevirt_vmi_memory_resident_bytes` 作为使用率分子。
- **FR-18:** `kind != vm` 实例的 `getInstanceMetrics` 必须仍走现有 container/GPU 分支，不受 VM 分支影响。
- **FR-19:** Console VM 时序图必须使用冻结 PromQL 模板，不得硬编码未文档化 label。
- **FR-20:** 推荐示例 yaml 必须标注前置依赖：MinIO bucket `ani-loki-logs` 需先创建、MinIO 数据卷 emptyDir 非持久化风险需运维确认。

---

## 5. Non-Goals (Out of Scope)

- 修改 `repo/api/openapi/v1.yaml` 或 `repo/api/openapi/services/v1.yaml`（所有 API 路径和 schema 不变，复用 `prd-console-instance-observability.md` 已定义的契约）
- 实现 ElasticsearchLogStore adapter（本 PRD 只实现 Loki adapter，ES adapter 后补）
- 实现 Loki X-Scope-OrgID 多租户模式（用 namespace label 过滤替代）
- 解决 MinIO 数据卷 emptyDir 非持久化问题（由运维确认是否接受或先改 MinIO 用 PVC）
- K8s 工作负载级观测（`k8s-workloads.md` 范围）
- Boss 平台大盘 UI
- `batch_job`、`notebook` 的 exec Tab（延续原 PRD Non-Goals）
- 节点池（`k8s_cluster` node pool）路径的 VM 指标（带 CAPI/CAPk 两段随机后缀的 VMI 不在本 PRD 范围）
- 采集器从 Fluent Bit 切换为 Vector（本 PRD 选定 Fluent Bit，未来切换另开 PRD）
- exec WebSocket 实现细节（延续原 PRD Non-Goals）
- 发明未在 `v1.yaml` 声明的 API 路径

---

## 6. Design Considerations

### 6.1 GPU 指标链路

```text
Console 指标 Tab
├── 快照卡片 ← GET /instances/{id}/metrics
│   └── handler 传 Kind=gpu_container → adapter GPU 分支 → DCGM 指标
└── 时序图表 ← GET /observability/query?query=<frozen PromQL>
    └── PromQL: DCGM_FI_DEV_GPU_UTIL / DCGM_FI_DEV_FB_USED / DCGM_FI_DEV_FB_TOTAL
```

### 6.2 日志持久化链路

```text
Console LogsTab
    ↓ 调
InstanceObservability.ListLogs (现有 port interface，不改)
    ↓ 内部
PrometheusInstanceObservability（现有 adapter，作为组合容器）
    ├── 持有 logStore 字段（ports.LogStore，可选，nil 时 fallback 到 K8s API）
    │   ↓ 运行时通过环境变量 INSTANCE_OBSERVABILITY_LOG_STORE 选择具体实现
    │   ├── LokiLogStore        ← 推荐（部署 Loki + Fluent Bit 时启用）
    │   ├── ElasticsearchLogStore ← 可选（部署 ES/OpenSearch 时启用，本 PRD 不实现）
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

### 6.3 VM 指标链路

```text
Console 指标 Tab
├── 快照卡片 ← GET /instances/{id}/metrics
│   └── handler 传 Kind=vm → adapter VM 分支 → kubevirt_vmi_* 指标（label: name=<vmi-name>）
└── 时序图表 ← GET /observability/query?query=<frozen PromQL>
    └── PromQL: kubevirt_vmi_cpu_usage_seconds_total / kubevirt_vmi_memory_* / kubevirt_vmi_network_*
```

### 6.4 Kind × 指标路径矩阵

| kind | 指标 Tab | adapter 分支 | 数据源 | label |
|------|----------|-------------|--------|-------|
| container | ✅ | container 分支（现有） | cAdvisor / metrics.k8s.io | `pod` |
| gpu_container | ✅ | GPU 分支（现有，依赖 handler 传 Kind） | DCGM exporter | `pod`（container 部分）、DCGM GPU 标签 |
| vm | ✅ | **新增** VM 分支 | KubeVirt virt-handler | **`name`**（非 `pod`） |
| sandbox | ✅ | container 分支（现有） | cAdvisor | `pod` |
| batch_job | ✅ | container 分支（现有） | cAdvisor | `pod` |
| notebook | ✅ | container 分支（现有） | cAdvisor | `pod` |
| k8s_cluster | 隐藏 | — | — | — |
| bare_metal | 隐藏 | — | — | — |
| dpu_node | 隐藏 | — | — | — |

### 6.5 可复用组件

- 复用现有 `InstanceObservability` interface（不新增 port）
- 复用现有 `PrometheusInstanceObservability` adapter（作为组合容器，新增 `logStore` 字段）
- 复用现有 `tenantNamespace(record.TenantID)` 多租户隔离逻辑
- 复用现有 `LogsTab.tsx` 的 `useInfiniteQuery` cursor 逻辑（已就绪，部署 Loki 后透明工作）
- 复用现有 Prometheus 部署 namespace `ani-s07-observability`
- 复用现有 `MetricsSnapshot.tsx` 通用卡片渲染（VM 快照无新增前端组件）

---

## 7. Technical Considerations

### 7.1 已知约束

- DCGM exporter 已部署在 `ani-system` namespace，地址 `ani-dcgm-exporter.ani-system:9400`（见 `repo/development-records/sprint13-gpu-inventory-dcgm-readiness.md`）
- KubeVirt virt-handler 已部署在 `kubevirt` namespace，`kubevirt_vmi_*` 指标有数据源
- ANI 租户 namespace 格式为 `ani-tenant-<tenant_id>`（见 `repo/pkg/adapters/runtime/dryrun_renderer.go`）
- ANI `kind=vm` 实例创建的 VMI `metadata.name` 不带随机后缀，直接等于用户传入的实例名（见 `demo_instances.go` → `dryrun_renderer.go`）
- 节点池（`k8s_cluster` node pool）路径的 VMI 带 CAPI/CAPk 两段随机后缀，不在本 PRD 范围

### 7.2 集成点

- `PrometheusInstanceObservability.ListLogs` 现有 K8s API 逻辑需保留为 fallback 方法（重命名为 `listLogsFromK8sAPI` 或类似）
- `rewritePromQLLabels` 当前只重写 `namespace` 和 `pod` label，VM 模板需要扩展支持 `name` label 或改用前端占位符注入
- Prometheus ClusterRole 需确认是否已授权 `kubevirt` namespace pods 读权限，若无则新增

### 7.3 前置依赖（人工执行，不在代码改动内）

- MinIO bucket `ani-loki-logs` 需先通过 ANI object store API `createStorageBucket` 或 MinIO Console 创建
- MinIO 数据卷当前是 emptyDir（`repo/deploy/real-k8s-lab/sprint13-objectstore-minio-live.yaml`），非持久化。Loki 日志 chunk 存 MinIO，MinIO pod 重启会丢数据。**运维需确认是否接受此风险，或先改 MinIO 用 PVC**
- S3 凭据需在 `ani-s07-observability` namespace 重建 MinIO 凭据 Secret（K8s Secret 不能跨 namespace 直接引用）

### 7.4 性能假设

- 日志首屏 ≤ 2s（100 条，Loki 单体模式 local profile 基准）`[Assumption]`
- 快照刷新 ≤ 1s（local profile 基准，延续原 PRD）`[Assumption]`
- Loki 资源占用：requests cpu 100m / memory 256Mi，limits cpu 1 / memory 1Gi（单体模式）

---

## 8. Success Metrics

- `gpu_container` 实例指标 Tab 快照卡片和时序图展示真实 GPU 利用率与显存 used/total（非 null）
- `kind=vm` 实例指标 Tab 快照卡片和时序图展示 guest OS 真实 CPU/内存/网络数据（非 QEMU cgroup 数据）
- 部署 Loki 后，Pod 重启或删除后仍可通过 Console LogsTab 分页浏览历史日志
- 跨租户隔离：租户 A 查不到租户 B 的日志（namespace label 过滤验证通过）
- 未部署 Loki 时，Console LogsTab 行为与现状完全一致（fallback 无回归）
- 日志真分页：首页加载 100 条，「加载更多」加载下一页，cursor 传递正确
- 日志保留 30 天后自动清理（Loki retention 生效）
- `make test`、`make validate-architecture`、`git diff --check` 全部通过

---

## 9. Open Questions

| ID | 问题 | 默认假设 | 决策方 |
|----|------|---------|--------|
| OQ-1 | VM 方案是否在当前迭代合入，还是等平台 VM 功能就绪后再合入？plan §六建议代码改动可先准备但暂不合入 | `[Assumption]` VM 方案代码改动在本 PRD 范围内准备，但端到端验证依赖平台 VM 功能就绪；合入时机由人工审核决定 | 人工审核 |
| OQ-2 | Loki + Fluent Bit 部署 yaml 是否在当前迭代实际部署到 real-k8s-lab，还是仅作为推荐示例代码入库？ | `[Assumption]` yaml 入库作为推荐示例，实际部署由运维决定 | 运维 |
| OQ-3 | MinIO emptyDir 非持久化风险是否接受，还是必须先改 MinIO 用 PVC？ | `[Assumption]` 运维确认接受临时部署仅用于验证，或先改 MinIO 持久化 | 运维 |
| OQ-4 | PromQL label 重写支持 `name` label 的实现方式：扩展 `rewritePromQLLabels` 还是 VM 模板用 `{{instance_id}}` 占位符由前端注入？ | `[Assumption]` 由 SPEC 决定，二选一 | SPEC |
| OQ-5 | ES adapter 何时实现？本 PRD 只实现 Loki adapter | `[Assumption]` ES adapter 后补，未实现时 `INSTANCE_OBSERVABILITY_LOG_STORE=elasticsearch` 走 fallback | 后续 PRD |
| OQ-6 | Prometheus ClusterRole 是否已授权 `kubevirt` namespace pods 读权限？若无则需新增 | `[Assumption]` 部署前确认，若无则新增 RBAC | 运维 |

---

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core + console |
| Code scope | Core：`repo/services/ani-gateway/internal/router/demo_instances.go` + `repo/services/ani-gateway/instance_observability_runtime.go` + `repo/pkg/adapters/runtime/prometheus_instance_observability.go` + `repo/pkg/adapters/runtime/loki_log_store.go`（新增）+ `repo/pkg/adapters/runtime/prometheus_observability_service.go` + `repo/pkg/ports/log_store.go`（新增）；Console：`repo/frontends/console/src/features/instance-observability/`；Deploy：`repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml` + `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`（新增） |
| OpenAPI authority | consume only — `repo/api/openapi/v1.yaml`（不改 API 契约，复用 `prd-console-instance-observability.md` 已定义的 `/instances/{instance_id}/logs`、`/instances/{instance_id}/metrics`、`/observability/query`） |
| Frozen exclusions | Services 后端实现、新增 OpenAPI 路径、Boss 大盘、ES adapter 实现、MinIO 持久化改造、节点池 VM 指标 |
| idempotency_key | N/A（本 PRD 不涉及新增有副作用的 POST 端点，延续原 PRD 的 `POST /instances/{instance_id}/exec` 幂等要求） |
| Module main doc | `repo/services/docs/console-modules/compute/container-observability.md`（需同步 VM/日志持久化补全口径） |
| Instance kinds | `container` / `gpu_container` / `vm` / `sandbox` / `batch_job` / `notebook`（指标 Tab 按 capability 隐藏规则不变） |
| Prometheus | 时序经 `/observability/query`；快照经 `InstanceMetrics` + adapter/exporter；不暴露 Prometheus 地址 |
| Log store | `ports.LogStore` port 抽象，Loki adapter 推荐，ES adapter 后补，未配置时 fallback 到 K8s API |
| Deploy yaml | 推荐示例，非必须部署；可替换为 ES/OpenSearch，只需新增对应 adapter |

---

## References

- `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md`（原 PRD）
- `repo/services/tasks/modules/prd/console/compute/plan.md`（本 PRD 的 plan 来源）
- `repo/api/openapi/v1.yaml`（Core API 契约，不改）
- `repo/pkg/ports/workload_runtime.go`（`WorkloadKindVM` / `WorkloadKindGPUContainer` 定义）
- [KubeVirt 官方 metrics 总表](https://kubevirt.io/monitoring/metrics.html)
- [Loki 官方文档](https://grafana.com/docs/loki/latest/)
- [Fluent Bit 官方文档](https://docs.fluentbit.io/)
- [Promtail EOL 公告](https://grafana.com/docs/loki/latest/send-data/promtail/installation/)
- `repo/development-records/sprint13-gpu-inventory-dcgm-readiness.md`（DCGM exporter 部署事实）
