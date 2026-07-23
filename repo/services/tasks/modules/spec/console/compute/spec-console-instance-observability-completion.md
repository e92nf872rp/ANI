# SPEC: 实例可观测性补全（GPU 指标 / 日志持久化 / VM 指标）

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md`
> - UX: `repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md`
> - 方案来源（用户指定优先）: `repo/services/tasks/modules/prd/console/compute/plan.md`
> Generated: 2026-07-20 | Product line: **core + console** | Code scope: Core handler/adapter 扩展 + Console 前端适配 + 推荐部署 yaml

> Scope: Core `ani-gateway` handler 修复 + `pkg/ports/log_store.go`（新增）+ `pkg/adapters/runtime/` Loki adapter 与 VM 分支扩展 + Console `instance-observability` 前端 PromQL 模板/配色适配 + `repo/deploy/real-k8s-lab/` 推荐部署示例
> Source of truth: consume `repo/api/openapi/v1.yaml` — 不改 API 契约，复用 `prd-console-instance-observability.md` 已定义的 `/instances/{instance_id}/logs`、`/instances/{instance_id}/metrics`、`/observability/query`

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 规定「实例可观测性补全」的完整技术实现，闭环三个未解决问题：

1. **GPU 指标死分支修复**：Gateway handler `getMetrics` 透传 `record.Kind` → adapter GPU 分支触发 → DCGM exporter 指标可在 Console 展示。
2. **日志持久化**：引入 `ports.LogStore` port 抽象 + `LokiLogStore` adapter + 环境变量注入 + Fluent Bit 采集 → Pod 重启/删除后仍可分页浏览历史日志。
3. **VM 指标采集链路**：`GetMetrics` 新增 VM 分支查询 `kubevirt_vmi_*` 指标 → Console VM 时序图新增 2 条冻结 PromQL 模板 → 展示 guest OS 真实资源数据。

本 SPEC **不修改 OpenAPI 契约**，全部改动复用 `v1.yaml` 已声明的 endpoint。

### 1.2 PRD Reference

- Source PRD: `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md`
- UX source: `repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md`
- 方案来源: `repo/services/tasks/modules/prd/console/compute/plan.md`（用户指定优先采用）
- User Stories covered: US-001 ~ US-012（全 12 个）
- Functional Requirements covered: FR-1 ~ FR-20（全 20 个）
- Open Questions: OQ-1 ~ OQ-6（OQ-4 由本 SPEC 决策关闭，其余维持 PRD 假设）

### 1.3 Design Decisions Summary

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| D-1 | handler 透传 Kind | `getMetrics` 在 `InstanceObservationGetRequest` 新增 `Kind: record.Kind` 字段 | US-001 / FR-1；最小改动触发 GPU/VM 分支 |
| D-2 | GPU 指标数据源 | DCGM exporter (`DCGM_FI_DEV_GPU_UTIL`、`DCGM_FI_DEV_FB_USED`、`DCGM_FI_DEV_FB_FREE`) + Prometheus scrape 新增 `dcgm-exporter` job；`GPUMemoryTotalMB = FB_FREE + FB_USED`（真实 DCGM exporter 不暴露 `FB_TOTAL`，live gate 2026-07-20 复现）；DCGM 单位为 MiB，adapter 直传无需换算 | US-002 / FR-2；adapter PromQL 对齐真实 DCGM exporter 指标 |
| D-3 | GPU 分支触发条件 | `request.Kind == ports.WorkloadKindGPUContainer` | 复用现有 `prometheus_instance_observability.go` 分支逻辑 |
| D-4 | 日志持久化抽象 | 新增 `ports.LogStore` interface，`Cursor` 为 opaque string | US-004 / FR-5；port 层不绑定存储后端语义 |
| D-5 | LogStore 注入方式 | `PrometheusInstanceObservability` 持有可选 `logStore` 字段 + `SetLogStore` 方法 + 环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` | US-006 / FR-6/7/8；未配置时 fallback 到 K8s API，零回归 |
| D-6 | Loki 多租户隔离 | LogQL `{namespace="ani-tenant-<tenant_id>",pod="<instance_id>"}` label 过滤，**不使用** Loki X-Scope-OrgID | US-005 / FR-9；单租户模式简化部署 |
| D-7 | Loki cursor 映射 | cursor = RFC3339 时间戳，adapter 内部转 Loki `start`（Unix 纳秒）；`next_cursor` = 结果最后一条 timestamp | US-005 / FR-10；opaque 对前端透明 |
| D-8 | 推荐部署 yaml | `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`，Loki 3.6.0 + Fluent Bit 3.2.0 + MinIO S3 后端 | US-007 / FR-11/12/13/20；标注「推荐示例，非必须部署」 |
| D-9 | VM 指标数据源 | KubeVirt `kubevirt_vmi_*` 指标 + Prometheus scrape 新增 `kubevirt-virt-handler` job | US-008 / FR-14 |
| D-10 | VM 分支触发条件 | `request.Kind == ports.WorkloadKindVM`，位于 GPU 分支之前 | US-009 / FR-15/18；最小改动 |
| D-11 | VM 指标 label | `name="<vmi-name>"` 精确匹配，**不用** `pod=~"..."` 正则 | US-009 / FR-16；VMI `metadata.name` = `record.Name`（无随机后缀） |
| D-12 | VM 内存使用率公式 | `kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes` | US-009 / FR-17；plan §六.2 公式 |
| D-13 | OQ-4 决策：PromQL label 重写 | **扩展 `rewritePromQLLabels` 支持 `name` label**，而非前端占位符注入 | 现有重写链路统一；前端模板风格一致 |
| D-14 | VM 时序图曲线数 | 2 条（CPU 利用率、内存使用率），**不展示网络 RX/TX 时序曲线** | PRD US-011 AC 修订；网络数据仅在快照卡片展示 |
| D-15 | VM 时序图配色 | 复用 container 的蓝/绿（`#0052D9` CPU / `#2BA471` 内存） | UX §8.4 假设；语义一致，不新增配色 |
| D-16 | VM 快照卡片 | 复用现有 `MetricsSnapshot.tsx` 通用渲染，无新增前端组件 | UX §8.4 假设；VM 仅数据源切换 |

---

## 2. Architecture

### 2.1 System Context

```text
┌─────────────────────────────────────────────────────────────────────┐
│ Console (repo/frontends/console/src/features/instance-observability)│
│  指标 Tab                                                            │
│  ├── 快照卡片 ← getInstanceMetrics（按 kind 渲染）                    │
│  └── 时序图   ← queryObservability?query=<frozen PromQL>             │
│      ├── container     → instance_cpu_utilization / memory          │
│      ├── gpu_container → + instance_gpu_utilization / gpu_memory     │
│      └── vm            → instance_vm_cpu_utilization / vm_memory     │
│  日志 Tab ← listInstanceLogs（cursor 真分页，Loki 透明）             │
└────────┬────────────────────────────────────┬───────────────────────┘
         │ coreApi.GET/POST                   │
         ▼                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Core Gateway (repo/services/ani-gateway)                            │
│  /api/v1/instances/{id}/metrics    (getMetrics handler 传 Kind)     │
│  /api/v1/instances/{id}/logs       (ListLogs fallback/LogStore)      │
│  /api/v1/observability/query       (PromQL 代理 + rewritePromQLLabels)│
└────────┬────────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Adapter 层 (repo/pkg/adapters/runtime/)                             │
│  PrometheusInstanceObservabilityService                             │
│  ├── GetMetrics                                                      │
│  │   ├── if Kind == VM      → kubevirt_vmi_* 指标（label: name）     │
│  │   ├── if Kind == GPUCont → DCGM 指标                              │
│  │   └── else                → cAdvisor / metrics.k8s.io            │
│  ├── ListLogs                                                        │
│  │   ├── if logStore != nil → logStore.QueryLogs（Loki 等）          │
│  │   └── else                → K8s pod log API（fallback）          │
│  └── logStore ports.LogStore（可选，环境变量注入）                   │
│      ├── LokiLogStore       ← INSTANCE_OBSERVABILITY_LOG_STORE=loki  │
│      └── nil                ← 未配置 / k8s / 未实现                  │
└────────┬────────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────────────┐
│ 存储后端（推荐示例，非必须部署）                                       │
│  Pod stdout/stderr → Fluent Bit DaemonSet → Loki（MinIO S3 后端）    │
│  Prometheus scrape:                                                  │
│  ├── job: kube-state-metrics（现有）                                  │
│  ├── job: dcgm-exporter（新增，ani-dcgm-exporter.ani-system:9400）    │
│  └── job: kubevirt-virt-handler（新增，kubevirt namespace pods:8443） │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.2 Component Design

#### 2.2.1 Core 端组件（修改/新增）

| 组件 | 职责 | 现状 | 本 SPEC 改动 |
|------|------|------|-------------|
| `demo_instances.go::getMetrics` | `/instances/{id}/metrics` handler | 未传 Kind | 新增 `Kind: record.Kind` 字段（US-001） |
| `prometheus_instance_observability.go::GetMetrics` | 指标 adapter | 仅 container/GPU 分支 | 新增 VM 分支（US-009） |
| `prometheus_instance_observability.go::ListLogs` | 日志 adapter | 直接调 K8s API | 新增 `logStore` 字段 + fallback 逻辑（US-006） |
| `prometheus_observability_service.go::rewritePromQLLabels` | PromQL label 重写 | 仅支持 `namespace`/`pod` | 扩展支持 `name` label（US-010 / OQ-4） |
| `instance_observability_runtime.go::newGatewayInstanceObservability` | runtime 工厂 | 无 LogStore 注入 | 新增 `INSTANCE_OBSERVABILITY_LOG_STORE` 环境变量处理（US-006） |
| `pkg/ports/log_store.go`（新增） | LogStore port 抽象 | 不存在 | 新增 interface + 请求/响应结构（US-004） |
| `pkg/adapters/runtime/loki_log_store.go`（新增） | Loki adapter | 不存在 | 实现 LogStore，走 Loki HTTP API（US-005） |

#### 2.2.2 Console 端组件（修改）

| 组件 | 职责 | 现状 | 本 SPEC 改动 |
|------|------|------|-------------|
| `promqlTemplates.ts` | PromQL 冻结模板 | 4 个模板，无 VM 分支 | 新增 `instance_vm_cpu_utilization`、`instance_vm_memory_utilization`；`getTemplatesForKind` 新增 VM 分支（US-011） |
| `MetricsChart.tsx` | 时序图组件 | 4 个模板配色 | `SERIES_COLORS` 新增 2 个 VM 模板配色（复用蓝/绿，US-011） |
| `MetricsSnapshot.tsx` | 快照卡片 | 通用渲染 | **无改动**（VM 复用，US-012） |
| `LogsTab.tsx` | 日志 Tab | useInfiniteQuery cursor | **无改动**（Loki 透明工作） |
| `observabilityTabsConfig.ts` | Tab 配置 | VM kind 已 `metricsSupported: true` | **无改动** |

#### 2.2.3 Deploy 端组件（新增/修改）

| 组件 | 职责 | 现状 | 本 SPEC 改动 |
|------|------|------|-------------|
| `sprint13-instance-observability-prometheus-live.yaml` | Prometheus 部署 | 现有 scrape | 新增 `dcgm-exporter` job + `kubevirt-virt-handler` job（US-002 / US-008） |
| `sprint13-instance-observability-loki-live.yaml`（新增） | Loki + Fluent Bit 部署 | 不存在 | 完整部署示例（US-007） |

### 2.3 Module Interactions

#### 2.3.1 GPU 指标端到端数据流

```text
Console 指标 Tab（gpu_container 实例）
  ↓ GET /api/v1/instances/{id}/metrics
Gateway handler getMetrics
  ↓ Kind: record.Kind = "gpu_container"  ← US-001 修复点
ports.InstanceObservability.GetMetrics
  ↓ PrometheusInstanceObservability.GetMetrics
  ↓ if request.Kind == WorkloadKindGPUContainer  ← 现有分支
  ↓ PromQL: DCGM_FI_DEV_GPU_UTIL / DCGM_FI_DEV_FB_USED / DCGM_FI_DEV_FB_TOTAL
Prometheus HTTP API /api/v1/query
  ↓ scrape job: dcgm-exporter（US-002 新增）
  ↓ target: ani-dcgm-exporter.ani-system:9400
DCGM exporter
```

#### 2.3.2 日志持久化端到端数据流

```text
Pod stdout/stderr
  ↓
Fluent Bit DaemonSet（采集 /var/log/pods/*，提取 namespace/pod/container label）
  ↓ HTTP push
Loki Service（单租户模式，auth_enabled: false，S3 后端 ani-loki-logs bucket）
  ↓
Console LogsTab
  ↓ GET /api/v1/instances/{id}/logs?limit=100&cursor=<opaque>
Gateway handler listInstanceLogs
  ↓ ports.InstanceObservability.ListLogs
PrometheusInstanceObservability.ListLogs
  ↓ if logStore != nil（INSTANCE_OBSERVABILITY_LOG_STORE=loki）
  ↓ logStore.QueryLogs(ctx, req)
LokiLogStore.QueryLogs
  ↓ LogQL: {namespace="ani-tenant-<tenant_id>",pod="<instance_id>"} | json
  ↓ start = cursor 转 Unix 纳秒，limit = req.Limit
Loki HTTP API /loki/api/v1/query_range
  ↓ 解析 stream 结果，映射为 InstanceLogEntry
  ↓ next_cursor = 最后一条 timestamp（RFC3339）
返回 Console，useInfiniteQuery 加载下一页
```

#### 2.3.3 VM 指标端到端数据流

```text
Console 指标 Tab（vm 实例）
  ├── 快照卡片
  │   ↓ GET /api/v1/instances/{id}/metrics
  │   Gateway handler getMetrics
  │   ↓ Kind: record.Kind = "vm"  ← US-001 修复点
  │   PrometheusInstanceObservability.GetMetrics
  │   ↓ if request.Kind == WorkloadKindVM  ← US-009 新增分支
  │   ↓ PromQL:
  │     ├── kubevirt_vmi_cpu_usage_seconds_total{name="<vmi-name>"}（rate[5m]）
  │     ├── kubevirt_vmi_memory_resident_bytes / domain_bytes（used/total）
  │     └── kubevirt_vmi_network_receive_bytes_total / transmit_bytes_total（rate[5m]）
  │   Prometheus HTTP API
  │   ↓ scrape job: kubevirt-virt-handler（US-008 新增）
  │   ↓ target: kubevirt namespace pods:8443（kubevirt.io=virt-handler label）
  │   KubeVirt virt-handler
  │
  └── 时序图
      ↓ GET /api/v1/observability/query?query=<frozen PromQL>
      Gateway handler queryObservability
      ↓ rewritePromQLLabels（扩展支持 name label，US-010）
      ↓ PromQL:
        ├── instance_vm_cpu_utilization:    rate(kubevirt_vmi_cpu_usage_seconds_total{namespace="...",name="..."}[5m])
        └── instance_vm_memory_utilization: (domain_bytes - usable_bytes) / domain_bytes
      Prometheus HTTP API
```

### 2.4 File Structure

```text
repo/
├── pkg/
│   ├── ports/
│   │   └── log_store.go                      # 新增：LogStore interface
│   └── adapters/runtime/
│       ├── prometheus_instance_observability.go  # 修改：VM 分支 + logStore 字段
│       ├── prometheus_observability_service.go   # 修改：rewritePromQLLabels 扩展
│       └── loki_log_store.go                 # 新增：LokiLogStore 实现
├── services/
│   └── ani-gateway/
│       ├── internal/router/demo_instances.go # 修改：getMetrics 传 Kind
│       └── instance_observability_runtime.go # 修改：LogStore 注入
├── frontends/console/src/features/instance-observability/
│   ├── promqlTemplates.ts                    # 修改：新增 VM 模板
│   └── MetricsChart.tsx                       # 修改：SERIES_COLORS 新增 VM 配色
└── deploy/real-k8s-lab/
    ├── sprint13-instance-observability-prometheus-live.yaml  # 修改：新增 2 个 scrape job
    └── sprint13-instance-observability-loki-live.yaml         # 新增：Loki + Fluent Bit
```

---

## 3. Data Model

### 3.1 `ports.LogStore` interface（新增）

文件：`repo/pkg/ports/log_store.go`

```go
package ports

import "context"

// LogQueryRequest 是日志查询的入参，port 层不绑定存储后端语义。
// Cursor 为 opaque string，由 adapter 内部映射为具体存储的游标（Loki time / ES search_after / K8s tailLines）。
type LogQueryRequest struct {
    TenantID   string // 租户 ID，用于多租户隔离
    InstanceID string // 实例 ID，对应 record.Name（也是 pod name / VMI name）
    Namespace  string // 租户 namespace，格式 ani-tenant-<tenant_id>
    Limit      int    // 单页条数上限
    Cursor     string // opaque string，空字符串表示从头开始
    Level      string // 日志级别过滤（info/warn/error/空表示全部），adapter 可选实现
}

// LogQueryResult 是日志查询的出参。
// NextCursor 为空表示已到末尾；非空表示下一页起点，对前端透明。
type LogQueryResult struct {
    Items       []InstanceLogEntry
    NextCursor  string
}

// InstanceLogEntry 是日志条目，字段与现有 OpenAPI `InstanceLog` schema 对齐。
type InstanceLogEntry struct {
    Timestamp string // RFC3339
    Level     string
    Message   string
    Container string
    Stream    string
}

// LogStore 是日志持久化存储的 port 抽象。
// 实现方：LokiLogStore（推荐）、ElasticsearchLogStore（后续 PRD）、K8s API fallback（不走此 port）。
// Cursor 语义由 adapter 自定义，port 层不约束。
type LogStore interface {
    QueryLogs(ctx context.Context, req LogQueryRequest) (LogQueryResult, error)
}
```

### 3.2 `PrometheusInstanceObservability` 扩展

文件：`repo/pkg/adapters/runtime/prometheus_instance_observability.go`

```go
// 结构体新增字段（可选，nil 时 fallback 到 K8s API）
type PrometheusInstanceObservability struct {
    // ... 现有字段 ...
    logStore ports.LogStore // 可选，环境变量注入，nil 时 fallback
}

// SetLogStore 由 runtime 在创建时调用，注入 LogStore 实现
func (s *PrometheusInstanceObservability) SetLogStore(store ports.LogStore) {
    s.logStore = store
}
```

### 3.3 `LokiLogStore` 实现

文件：`repo/pkg/adapters/runtime/loki_log_store.go`

```go
package runtime

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/url"
    "strconv"
    "time"

    "ani/repo/pkg/ports"
)

// LokiLogStore 实现 ports.LogStore，通过 Loki HTTP API 查询持久化日志。
// 多租户隔离：通过 LogQL 的 namespace label 过滤，不使用 X-Scope-OrgID。
// Cursor 映射：cursor 是 RFC3339 时间戳，内部转 Loki start（Unix 纳秒）。
type LokiLogStore struct {
    baseURL string // 例：http://ani-loki.ani-s07-observability:3100
    client  *http.Client
}

func NewLokiLogStore(baseURL string) *LokiLogStore {
    return &LokiLogStore{
        baseURL: baseURL,
        client:  &http.Client{Timeout: 10 * time.Second},
    }
}

// QueryLogs 调用 Loki /loki/api/v1/query_range
func (s *LokiLogStore) QueryLogs(ctx context.Context, req ports.LogQueryRequest) (ports.LogQueryResult, error) {
    // 1. 构造 LogQL：{namespace="<ns>",pod="<instance_id>"} | json
    logql := fmt.Sprintf(`{namespace=%q,pod=%q} | json`, req.Namespace, req.InstanceID)

    // 2. cursor → start（Unix 纳秒）
    var startNs int64
    if req.Cursor != "" {
        t, err := time.Parse(time.RFC3339, req.Cursor)
        if err != nil {
            return ports.LogQueryResult{}, fmt.Errorf("invalid cursor %q: %w", req.Cursor, err)
        }
        startNs = t.UnixNano()
    } else {
        // 默认从最近 24 小时开始（可配置）
        startNs = time.Now().Add(-24 * time.Hour).UnixNano()
    }

    // 3. 构造 query 参数
    params := url.Values{}
    params.Set("query", logql)
    params.Set("start", strconv.FormatInt(startNs, 10))
    params.Set("limit", strconv.Itoa(req.Limit))
    params.Set("direction", "forward")

    // 4. HTTP GET /loki/api/v1/query_range
    reqURL := s.baseURL + "/loki/api/v1/query_range?" + params.Encode()
    httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
    if err != nil {
        return ports.LogQueryResult{}, err
    }

    resp, err := s.client.Do(httpReq)
    if err != nil {
        return ports.LogQueryResult{}, fmt.Errorf("loki query failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return ports.LogQueryResult{}, fmt.Errorf("loki returned status %d", resp.StatusCode)
    }

    // 5. 解析 Loki 响应（stream 模式）
    var lokiResp struct {
        Status string `json:"status"`
        Data   struct {
            ResultType string `json:"resultType"`
            Result     []struct {
                Stream map[string]string `json:"stream"`
                Values [][]string         `json:"values"` // [timestamp_ns, json_line]
            } `json:"result"`
        } `json:"data"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&lokiResp); err != nil {
        return ports.LogQueryResult{}, fmt.Errorf("decode loki response: %w", err)
    }

    // 6. 映射为 InstanceLogEntry，计算 next_cursor
    var items []ports.InstanceLogEntry
    var lastTs string
    for _, stream := range lokiResp.Data.Result {
        for _, v := range stream.Values {
            tsNs := v[0]
            line := v[1]
            // line 是 JSON，包含 level/message/stream 等字段
            var entry struct {
                Level   string `json:"level"`
                Message string `json:"message"`
                Stream  string `json:"stream"`
            }
            _ = json.Unmarshal([]byte(line), &entry)
            // timestamp ns → RFC3339
            tsInt, _ := strconv.ParseInt(tsNs, 10, 64)
            t := time.Unix(0, tsInt)
            items = append(items, ports.InstanceLogEntry{
                Timestamp: t.Format(time.RFC3339),
                Level:     entry.Level,
                Message:   entry.Message,
                Container: stream.Stream["container"],
                Stream:    entry.Stream,
            })
            lastTs = t.Format(time.RFC3339)
        }
    }

    // 7. 如果返回条数 < limit，说明已到末尾，NextCursor 为空
    var nextCursor string
    if len(items) >= req.Limit && lastTs != "" {
        nextCursor = lastTs
    }

    return ports.LogQueryResult{
        Items:      items,
        NextCursor: nextCursor,
    }, nil
}
```

### 3.4 数据模型映射表

| 概念 | OpenAPI schema | port 结构 | adapter 实现 |
|------|----------------|-----------|---------------|
| 日志条目 | `InstanceLog` | `ports.InstanceLogEntry` | LokiLogStore 映射 Loki stream values |
| 日志查询请求 | `ListInstanceLogsRequest` | `ports.LogQueryRequest` | 增加 `Namespace`、`Cursor` 字段 |
| 日志查询响应 | `ListInstanceLogsResponse` | `ports.LogQueryResult` | `Items` + `NextCursor` |
| 指标快照 | `InstanceMetrics` | 现有 `ports.InstanceMetrics` | VM 分支填充 kubevirt_vmi 数据 |
| PromQL 查询 | `ObservabilityQueryRequest` | 现有结构 | `rewritePromQLLabels` 扩展 |

### 3.5 Migration Plan

本 SPEC 不涉及数据迁移：
- LogStore 是新增 port，不影响现有数据
- Loki 部署是可选推荐示例，不部署时 fallback 到 K8s API，零回归
- VM 分支是新增逻辑，不影响现有 container/GPU 分支

---

## 4. API Design

### 4.1 Frozen Facts Table（API 契约不变）

本 SPEC **不修改** `repo/api/openapi/v1.yaml`。所有改动复用现有已声明 endpoint：

| Endpoint | Method | 用途 | 本 SPEC 改动 |
|----------|--------|------|-------------|
| `/instances/{instance_id}/metrics` | GET | 指标快照 | handler 新增 `Kind` 透传（请求内部字段，不改 OpenAPI schema） |
| `/instances/{instance_id}/logs` | GET | 日志列表 | adapter 内部 fallback/LogStore 切换，不改响应 schema |
| `/observability/query` | POST | PromQL 时序查询 | `rewritePromQLLabels` 扩展支持 `name` label，不改 schema |

### 4.2 Endpoints 消费视角

#### 4.2.1 `GET /instances/{instance_id}/metrics`

**请求**（OpenAPI 不变）：Path `instance_id`，Query `start`/`end`/`step`（可选）。

**handler 改动**（US-001）：

```go
// repo/services/ani-gateway/internal/router/demo_instances.go
func (r *demoInstanceAPI) getMetrics(ctx context.Context, req *...) (*..., error) {
    record, err := r.store.GetInstance(ctx, req.InstanceID)
    if err != nil {
        return nil, err
    }
    resp, err := r.observability.GetMetrics(ctx, ports.InstanceObservationGetRequest{
        InstanceID: req.InstanceID,
        TenantID:    record.TenantID,
        Kind:        record.Kind,  // ← 新增：透传 Kind
        // ... 其他现有字段 ...
    })
    // ...
}
```

**响应**（OpenAPI `InstanceMetrics` schema 不变）：
- `cpu`、`memory`、`network`、`gpu` 字段按 kind 填充
- `kind=vm` 时 CPU/内存来自 `kubevirt_vmi_*`，网络来自 `kubevirt_vmi_network_*`
- `kind=gpu_container` 时 GPU 字段非 null（DCGM 可用时）
- `kind != gpu_container` 时 `gpu` 字段为 null
- `kind != vm` 时仍走 container/GPU 分支，不受 VM 分支影响

#### 4.2.2 `GET /instances/{instance_id}/logs`

**请求**（OpenAPI 不变）：Path `instance_id`，Query `limit`/`cursor`/`level`。

**响应**（OpenAPI `ListInstanceLogsResponse` schema 不变）：`items: InstanceLog[]` + `next_cursor: string`（opaque，Loki/K8s 两种实现都返回）。

**adapter 内部切换**（US-006）：见 §5.3。

#### 4.2.3 `POST /observability/query`

**请求**（OpenAPI 不变）：Body `{ query, start, end, step }`。

**handler 改动**（US-010 / OQ-4）：`rewritePromQLLabels` 扩展支持 `name` label，见 §5.8。

### 4.3 Error Responses

| 场景 | HTTP 状态 | Error Code | 说明 |
|------|-----------|------------|------|
| Loki 不可达 | 500 | `LOG_STORE_UNAVAILABLE` | `LokiLogStore.QueryLogs` 返回包装错误，handler 透传 |
| cursor 格式错误 | 400 | `INVALID_CURSOR` | adapter 解析 cursor 失败 |
| 实例不存在 | 404 | `INSTANCE_NOT_FOUND` | 现有行为不变 |
| 租户隔离校验失败 | 403 | `TENANT_ISOLATION_VIOLATION` | 现有行为不变 |
| DCGM exporter 不可用 | 200 | — | GPU 字段为 null，不报错（现有行为） |
| KubeVirt virt-handler 不可用 | 200 | — | VM 字段为 null，不报错 |
| Prometheus 查询失败 | 502 | `PROMETHEUS_QUERY_FAILED` | 现有行为不变 |

### 4.4 Breaking Changes

**无破坏性变更**：
- 所有 OpenAPI schema 不变
- `Kind` 是 handler 内部字段透传，不影响 API 契约
- `logStore` 是 adapter 内部字段，不影响 API 契约
- `rewritePromQLLabels` 扩展是后端内部逻辑，不影响 API 契约
- 未配置 `INSTANCE_OBSERVABILITY_LOG_STORE` 时所有行为与现状完全一致

---

## 5. Business Logic

### 5.1 handler 传 Kind（US-001）

文件：`repo/services/ani-gateway/internal/router/demo_instances.go`

修改 `getMetrics` handler：

```go
resp, err := r.observability.GetMetrics(ctx, ports.InstanceObservationGetRequest{
    InstanceID: req.InstanceID,
    TenantID:    record.TenantID,
    Kind:        record.Kind,  // 新增
    Start:       req.Start,
    End:         req.End,
    Step:        req.Step,
})
```

**验证点**：`kind=container` 请求 `request.Kind == "container"`；`kind=gpu_container` 请求 `request.Kind == "gpu_container"`；`kind=vm` 请求 `request.Kind == "vm"`；现有 container 行为不回归。

### 5.2 GetMetrics VM 分支（US-009）

文件：`repo/pkg/adapters/runtime/prometheus_instance_observability.go`

在现有 GPU 分支**之前**新增 VM 分支：

```go
func (s *PrometheusInstanceObservability) GetMetrics(ctx context.Context, req ports.InstanceObservationGetRequest) (*ports.InstanceMetrics, error) {
    record, err := s.store.GetInstance(ctx, req.InstanceID)
    if err != nil {
        return nil, err
    }
    // 租户隔离校验（现有逻辑）
    if record.TenantID != req.TenantID {
        return nil, ports.ErrTenantIsolationViolation
    }

    // 新增：VM 分支（位于 GPU 分支之前）
    if req.Kind == ports.WorkloadKindVM {
        return s.getMetricsForVM(ctx, record, req)
    }

    // 现有 GPU 分支
    if req.Kind == ports.WorkloadKindGPUContainer {
        return s.getMetricsForGPUContainer(ctx, record, req)
    }

    // 现有 container 分支
    return s.getMetricsForContainer(ctx, record, req)
}

// 新增方法
func (s *PrometheusInstanceObservability) getMetricsForVM(ctx context.Context, record *InstanceRecord, req ports.InstanceObservationGetRequest) (*ports.InstanceMetrics, error) {
    namespace := tenantNamespace(record.TenantID)
    vmiName := record.Name  // VMI metadata.name 等于 record.Name，无随机后缀

    // CPU 使用率：rate(kubevirt_vmi_cpu_usage_seconds_total{namespace,name}[5m])
    cpuQuery := fmt.Sprintf(
        `rate(kubevirt_vmi_cpu_usage_seconds_total{namespace="%s",name="%s"}[5m])`,
        namespace, vmiName,
    )
    cpuVal, err := s.queryPrometheus(ctx, cpuQuery)
    // ... 错误处理 ...

    // 内存 used：kubevirt_vmi_memory_resident_bytes
    // 内存 total：kubevirt_vmi_memory_domain_bytes
    // 内存使用率公式：domain_bytes - usable_bytes（PRD FR-17）
    memUsedQuery := fmt.Sprintf(
        `kubevirt_vmi_memory_resident_bytes{namespace="%s",name="%s"}`,
        namespace, vmiName,
    )
    memTotalQuery := fmt.Sprintf(
        `kubevirt_vmi_memory_domain_bytes{namespace="%s",name="%s"}`,
        namespace, vmiName,
    )
    // ... 查询并计算 ...

    // 网络 RX/TX：rate(kubevirt_vmi_network_receive_bytes_total[5m]) / rate(kubevirt_vmi_network_transmit_bytes_total[5m])
    rxQuery := fmt.Sprintf(
        `rate(kubevirt_vmi_network_receive_bytes_total{namespace="%s",name="%s"}[5m])`,
        namespace, vmiName,
    )
    txQuery := fmt.Sprintf(
        `rate(kubevirt_vmi_network_transmit_bytes_total{namespace="%s",name="%s"}[5m])`,
        namespace, vmiName,
    )
    // ... 查询 ...

    return &ports.InstanceMetrics{
        CPU:    cpuVal,
        Memory: memUsedVal,
        // ... 映射 ...
    }, nil
}
```

**关键点**：
- VM 分支位于 GPU 分支**之前**，避免 GPU 分支误匹配
- `name` label 精确匹配（非正则），因为 VMI name 等于 `record.Name`（无随机后缀）
- 内存使用率公式：`kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes`（PRD FR-17）
- KubeVirt virt-handler 不可用时字段为 null，不伪造 0

### 5.3 ListLogs fallback / LogStore 切换（US-006）

文件：`repo/pkg/adapters/runtime/prometheus_instance_observability.go`

```go
func (s *PrometheusInstanceObservability) ListLogs(ctx context.Context, req ports.InstanceObservationListLogsRequest) (*ports.InstanceObservationListLogsResponse, error) {
    // 租户隔离校验（现有逻辑）
    record, err := s.store.GetInstance(ctx, req.InstanceID)
    if err != nil {
        return nil, err
    }
    if record.TenantID != req.TenantID {
        return nil, ports.ErrTenantIsolationViolation
    }

    // 新增：logStore 注入路径
    if s.logStore != nil {
        result, err := s.logStore.QueryLogs(ctx, ports.LogQueryRequest{
            TenantID:   req.TenantID,
            InstanceID:  req.InstanceID,
            Namespace:   tenantNamespace(record.TenantID),
            Limit:       req.Limit,
            Cursor:      req.Cursor,
            Level:       req.Level,
        })
        if err != nil {
            return nil, fmt.Errorf("logStore query failed: %w", err)
        }
        return &ports.InstanceObservationListLogsResponse{
            Items:      result.Items,
            NextCursor: result.NextCursor,
        }, nil
    }

    // 现有逻辑：fallback 到 K8s pod log API
    return s.listLogsFromK8sAPI(ctx, req, record)
}

// 现有 K8s API 逻辑重命名为 listLogsFromK8sAPI（或保留匿名内联）
func (s *PrometheusInstanceObservability) listLogsFromK8sAPI(ctx context.Context, req ports.InstanceObservationListLogsRequest, record *InstanceRecord) (*ports.InstanceObservationListLogsResponse, error) {
    // ... 现有 K8s pod log API 调用逻辑 ...
}
```

### 5.4 runtime 注入 LogStore（US-006）

文件：`repo/services/ani-gateway/instance_observability_runtime.go`

```go
func newGatewayInstanceObservability(...) ports.InstanceObservability {
    svc := runtime.NewPrometheusInstanceObservability(...)

    // 新增：根据环境变量注入 LogStore
    storeType := os.Getenv("INSTANCE_OBSERVABILITY_LOG_STORE")
    switch storeType {
    case "loki":
        lokiURL := os.Getenv("INSTANCE_OBSERVABILITY_LOKI_URL")
        if lokiURL == "" {
            lokiURL = "http://ani-loki.ani-s07-observability:3100"
        }
        svc.SetLogStore(runtime.NewLokiLogStore(lokiURL))
    case "elasticsearch":
        // ES adapter 暂未实现，走 fallback（US-005 Non-Goals）
        log.Warn("INSTANCE_OBSERVABILITY_LOG_STORE=elasticsearch not yet implemented, falling back to K8s API")
    case "k8s", "":
        // fallback 到 K8s API，不注入
    default:
        log.Warn("unknown INSTANCE_OBSERVABILITY_LOG_STORE value, falling back to K8s API", "value", storeType)
    }

    return svc
}
```

### 5.5 Loki adapter 查询逻辑（US-005）

详见 §3.3 `LokiLogStore` 实现。

**关键点**：
- LogQL：`{namespace="ani-tenant-<tenant_id>",pod="<instance_id>"} | json`
- 多租户隔离：namespace label 过滤，**不使用** X-Scope-OrgID（单租户模式）
- cursor：RFC3339 时间戳 → Loki `start`（Unix 纳秒）
- next_cursor：结果最后一条 timestamp（RFC3339）
- Loki 不可达时返回包装错误，不伪造空结果

### 5.6 PromQL 模板冻结表（US-011）

文件：`repo/frontends/console/src/features/instance-observability/promqlTemplates.ts`

现有模板（不变）：

| Template ID | Kind | PromQL |
|-------------|------|--------|
| `instance_cpu_utilization` | container/gpu_container/sandbox/batch_job/notebook | `sum(rate(container_cpu_usage_seconds_total{namespace="{{namespace}}",pod="{{pod}}"}[5m]))` |
| `instance_memory_utilization` | 同上 | `sum(container_memory_working_set_bytes{namespace="{{namespace}}",pod="{{pod}}"})` |
| `instance_gpu_utilization` | gpu_container | `DCGM_FI_DEV_GPU_UTIL{namespace="{{namespace}}",pod="{{pod}}"}` |
| `instance_gpu_memory_utilization` | gpu_container | `DCGM_FI_DEV_FB_USED{namespace="{{namespace}}",pod="{{pod}}"}` / (`DCGM_FI_DEV_FB_FREE{namespace="{{namespace}}",pod="{{pod}}"}` + `DCGM_FI_DEV_FB_USED{namespace="{{namespace}}",pod="{{pod}}"}`) |

**新增 VM 模板**：

| Template ID | Kind | PromQL |
|-------------|------|--------|
| `instance_vm_cpu_utilization` | vm | `rate(kubevirt_vmi_cpu_usage_seconds_total{namespace="{{namespace}}",name="{{instance_id}}"}[5m])` |
| `instance_vm_memory_utilization` | vm | `(kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"} - kubevirt_vmi_memory_usable_bytes{namespace="{{namespace}}",name="{{instance_id}}"}) / kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"}` |

**关键差异**：VM 模板用 `name="{{instance_id}}"` 而非 `pod="{{pod}}"`，因为 KubeVirt VMI 指标 label 是 `name`（VMI name）而非 `pod`（virt-launcher pod name）。

### 5.7 `getTemplatesForKind` 扩展（US-011）

```typescript
// promqlTemplates.ts
export function getTemplatesForKind(kind: InstanceKind): PromQLTemplateId[] {
  switch (kind) {
    case 'gpu_container':
      return [
        'instance_cpu_utilization',
        'instance_memory_utilization',
        'instance_gpu_utilization',
        'instance_gpu_memory_utilization',
      ]
    case 'vm':  // ← 新增
      return [
        'instance_vm_cpu_utilization',
        'instance_vm_memory_utilization',
      ]
    case 'container':
    case 'sandbox':
    case 'batch_job':
    case 'notebook':
    default:
      return [
        'instance_cpu_utilization',
        'instance_memory_utilization',
      ]
  }
}
```

**关键点**：
- VM kind 只返回 2 条模板（CPU 利用率、内存使用率），**不返回网络 RX/TX 时序曲线**（PRD US-011 AC 修订）
- 网络数据仅在快照卡片展示（通过 `getInstanceMetrics` 返回的 `network` 字段）
- VM 模板使用 `name="{{instance_id}}"`，需 `rewritePromQLLabels` 扩展支持 `name` label（见 §5.8）

### 5.8 `rewritePromQLLabels` 扩展（US-010 / OQ-4 决策）

文件：`repo/pkg/adapters/runtime/prometheus_observability_service.go`

**OQ-4 决策**：**扩展 `rewritePromQLLabels` 支持 `name` label**，而非前端占位符注入。

**理由**：
- 现有 `namespace`/`pod` label 重写链路已统一，VM 新增 `name` label 保持架构一致性
- 前端模板风格保持一致（都用 `{{namespace}}`、`{{instance_id}}` 占位符）
- 后端统一处理租户隔离和实例名注入，前端不感知重写逻辑

```go
func (s *PrometheusObservabilityService) rewritePromQLLabels(ctx context.Context, tenantID string, query string) (string, error) {
    // ... 现有：查实例记录、租户隔离校验 ...
    realNamespace := tenantNamespace(record.TenantID)
    podMatcher := promQLPodMatcher(record.Name)

    // 逐个替换 label 值
    switch labelName {
    case "namespace":
        b.WriteString(`namespace="`)
        b.WriteString(realNamespace)
        b.WriteString(`"`)

    case "pod":
        b.WriteString(`pod=~"`)
        b.WriteString(podMatcher)
        b.WriteString(`"`)

    case "name":  // ← 新增：VM 指标的 name label
        b.WriteString(`name="`)
        b.WriteString(record.Name)  // 精确匹配，非正则
        b.WriteString(`"`)
    }
    // ... 其他现有逻辑 ...
}
```

**关键差异**：
- `name` label 用精确匹配 `name="record.Name"`，**非**正则 `name=~"..."`
- 因为 VMI `metadata.name` = `record.Name`（无随机后缀，见 PRD §7.1 已知约束）
- 现有 `container`/`gpu_container` 的 `pod` label 重写不回归

### 5.9 `MetricsChart.tsx` SERIES_COLORS 扩展（US-011）

文件：`repo/frontends/console/src/features/instance-observability/MetricsChart.tsx`

```typescript
const SERIES_COLORS: Record<PromQLTemplateId, string> = {
  // 现有（不变）
  instance_cpu_utilization: '#0052D9',         // 蓝
  instance_memory_utilization: '#2BA471',      // 绿
  instance_gpu_utilization: '#D54941',         // 红
  instance_gpu_memory_utilization: '#E37318',  // 橙
  // 新增 VM 模板（复用蓝/绿，语义一致）
  instance_vm_cpu_utilization: '#0052D9',      // 蓝
  instance_vm_memory_utilization: '#2BA471',   // 绿
}
```

**决策**：VM 时序图配色复用 container 的蓝/绿（UX §8.4 假设），因为 VM 的 CPU/内存语义与 container 一致，不新增配色避免视觉混乱。

### 5.10 Prometheus scrape 配置（US-002 / US-008）

文件：`repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml`

新增 2 个 scrape job：

```yaml
scrape_configs:
  # ... 现有 job ...

  # 新增：DCGM exporter for GPU 指标（US-002）
  - job_name: 'dcgm-exporter'
    metrics_path: /metrics
    static_configs:
      - targets: ['ani-dcgm-exporter.ani-system:9400']
        labels:
          component: 'dcgm-exporter'

  # 新增：KubeVirt virt-handler for VM 指标（US-008）
  - job_name: 'kubevirt-virt-handler'
    metrics_path: /metrics
    bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
    tls_config:
      insecure_skip_verify: true
    kubernetes_sd_configs:
      - role: pod
        namespaces:
          names: ['kubevirt']
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_kubevirt_io]
        regex: virt-handler
        action: keep
      - source_labels: [__meta_kubernetes_pod_container_port_number]
        regex: 8443
        action: keep
      - source_labels: [__meta_kubernetes_namespace]
        target_label: namespace
      - source_labels: [__meta_kubernetes_pod_name]
        target_label: pod
```

**RBAC 确认**（OQ-6）：
- 现有 Prometheus ClusterRole 若无 `kubevirt` namespace pods 读权限，需新增
- 部署前确认，若无则新增 RBAC 规则

### 5.11 Loki + Fluent Bit 部署 yaml（US-007）

文件：`repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`

**头部标注**：

```yaml
# ============================================================
# 推荐示例，非必须部署
# ------------------------------------------------------------
# - 可替换为 ES/OpenSearch，只需新增对应 adapter（见 prd FR-11）
# - 前置依赖：
#   1. MinIO bucket `ani-loki-logs` 需先通过 ANI object store API 创建
#   2. MinIO 数据卷当前是 emptyDir（非持久化），pod 重启会丢数据
#      运维需确认是否接受此风险，或先改 MinIO 用 PVC
#   3. S3 凭据需在 ani-s07-observability namespace 重建 MinIO 凭据 Secret
#      （K8s Secret 不能跨 namespace 直接引用）
# - 部署前必须替换 S3 凭据占位值为 MinIO 真实凭据（来自 ani-s05-minio-root）
# - 部署后验证：
#   kubectl wait deployment/ani-loki -n ani-s07-observability --for=condition=ready
#   curl http://ani-loki.ani-s07-observability:3100/ready  # 返回 200
#   curl http://ani-loki.ani-s07-observability:3100/loki/api/v1/labels  # 返回 namespace/pod label
#   curl 'http://ani-loki.ani-s07-observability:3100/loki/api/v1/query_range?query={namespace="ani-tenant-<id>"}&limit=10'
# ============================================================
```

**核心资源**（详见 plan.md §五完整 yaml）：
- Namespace: `ani-s07-observability`（复用现有）
- Loki Deployment: `grafana/loki:3.6.0`，单租户模式（`auth_enabled: false`）
- Loki ConfigMap: schema v13 + tsdb store，`retention_period: 30d`
- Loki Secret `ani-loki-s3-creds`: S3 凭据占位值
- Loki S3 后端: `bucketnames: ani-loki-logs`，`endpoint: ani-s05-minio.ani-s05-objectstore:9000`，`s3forcepathstyle: true`
- Fluent Bit DaemonSet: `fluent/fluent-bit:3.2.0`，采集 `/var/log/pods/*`，提取 namespace/pod/container 作为 Loki label
- Fluent Bit ServiceAccount/ClusterRole/ClusterRoleBinding/ConfigMap

### 5.12 Validation Rules

| 输入 | 规则 | 错误码 |
|------|------|--------|
| `instance_id` | 非空，存在性校验 | 404 `INSTANCE_NOT_FOUND` |
| `record.Kind` | 枚举值 `container`/`gpu_container`/`vm`/`sandbox`/`batch_job`/`notebook` | — |
| `cursor` | opaque string，adapter 内部校验格式 | 400 `INVALID_CURSOR` |
| `limit` | 1-500，默认 100 | 400 `INVALID_LIMIT` |
| `INSTANCE_OBSERVABILITY_LOG_STORE` | 枚举 `loki`/`elasticsearch`/`k8s`/空 | — |
| `INSTANCE_OBSERVABILITY_LOKI_URL` | 非空 URL，默认 `http://ani-loki.ani-s07-observability:3100` | — |
| PromQL `name` label | 精确匹配，非正则 | — |

### 5.13 State Machine

本 SPEC 无状态机改动，所有查询均为无状态 GET 请求。

### 5.14 Edge Cases

| 场景 | 处理 |
|------|------|
| Loki 不可达 | `LokiLogStore.QueryLogs` 返回包装错误，handler 返回 500，不伪造空结果 |
| cursor 为空 | 从最近 24 小时开始查询（可配置） |
| Loki 返回空结果 | `Items: []`，`NextCursor: ""`，前端显示「暂无日志」 |
| DCGM exporter 不可用 | GPU 字段为 null，不报错（现有行为） |
| KubeVirt virt-handler 不可用 | VM 字段为 null，不报错 |
| `kind=vm` 但 VMI 不存在 | 查询返回空，字段为 null |
| `INSTANCE_OBSERVABILITY_LOG_STORE=loki` 但 Loki URL 未配置 | 使用默认 URL `http://ani-loki.ani-s07-observability:3100` |
| `INSTANCE_OBSERVABILITY_LOG_STORE=elasticsearch` | 记录 warn 日志，走 fallback（K8s API） |
| 未知 `INSTANCE_OBSERVABILITY_LOG_STORE` 值 | 记录 warn 日志，走 fallback |
| `rewritePromQLLabels` 遇到未知 label | 保持原样不替换（现有行为） |
| 节点池 VM 指标（带 CAPI/CAPk 随机后缀） | 不在本 SPEC 范围，走现有 container 分支 |

---

## 6. Error Handling

### 6.1 错误分类与处理策略

| 错误类型 | 来源 | 处理策略 | HTTP 状态 |
|----------|------|----------|-----------|
| `INSTANCE_NOT_FOUND` | store 层 | 透传到 handler | 404 |
| `TENANT_ISOLATION_VIOLATION` | adapter 层 | 透传到 handler | 403 |
| `LOG_STORE_UNAVAILABLE` | LokiLogStore | 包装 Loki 错误，透传 | 500 |
| `INVALID_CURSOR` | LokiLogStore | cursor 解析失败 | 400 |
| `PROMETHEUS_QUERY_FAILED` | Prometheus HTTP | 现有行为 | 502 |
| `LOKI_HTTP_ERROR` | Loki HTTP | 非 200 状态码 | 500 |
| `LOKI_DECODE_ERROR` | Loki 响应解析 | JSON decode 失败 | 500 |

### 6.2 错误传播链路

```text
Loki/DCGM/KubeVirt 后端
  ↓ 包装错误（保留原始错误）
LokiLogStore / PrometheusInstanceObservability
  ↓ 透传错误（不吞没）
ports.InstanceObservability
  ↓ 透传错误
Gateway handler
  ↓ 映射为 HTTP 状态码 + error code
HTTP Response
  ↓ Console useQuery 错误态
UI 错误提示
```

### 6.3 错误响应格式

遵循现有 Core API 错误响应格式（`repo/api/openapi/v1.yaml` 已定义）：

```json
{
  "error": {
    "code": "LOG_STORE_UNAVAILABLE",
    "message": "loki query failed: connection refused",
    "details": {}
  }
}
```

### 6.4 可恢复性

| 错误 | 客户端重试 | 用户操作 |
|------|-----------|---------|
| `LOG_STORE_UNAVAILABLE` | 指数退避重试 3 次 | 检查 Loki 部署状态 |
| `INVALID_CURSOR` | 不重试，重置 cursor | 重新加载日志 |
| `PROMETHEUS_QUERY_FAILED` | 指数退避重试 3 次 | 检查 Prometheus 状态 |
| 其他 4xx | 不重试 | 修正请求参数 |

---

## 7. Security

### 7.1 多租户隔离

| 资源 | 隔离机制 | 验证点 |
|------|----------|--------|
| 指标查询 | `tenantNamespace(record.TenantID)` 注入 namespace label | 租户 A 查不到租户 B 的指标 |
| 日志查询（K8s API） | namespace label 过滤 + 租户隔离校验 | 现有行为不变 |
| 日志查询（Loki） | LogQL `{namespace="ani-tenant-<tenant_id>",pod="<instance_id>"}` label 过滤 | 租户 A 查不到租户 B 的日志 |
| PromQL 重写 | `rewritePromQLLabels` 注入 `namespace`/`pod`/`name` label | 租户隔离在重写阶段保证 |

**关键约束**：
- Loki 单租户模式（`auth_enabled: false`），**不使用** X-Scope-OrgID 多租户隔离
- 多租户隔离**完全依赖** LogQL 的 `namespace` label 过滤
- 租户 namespace 格式：`ani-tenant-<tenant_id>`（见 `dryrun_renderer.go`）

### 7.2 RBAC

| 组件 | 权限 | 说明 |
|------|------|------|
| Prometheus | `kubevirt` namespace pods 读权限 | US-002 scrape kubevirt-virt-handler 所需（OQ-6） |
| Prometheus | `ani-system` namespace services 读权限 | US-002 scrape dcgm-exporter（static_config，无需 RBAC） |
| Fluent Bit | `pods/log` 读权限 + `nodes/proxy` | DaemonSet 采集 `/var/log/pods/*` 所需 |
| Loki | 无 K8s API 权限需求 | 单租户模式，仅读写 MinIO S3 |

### 7.3 Secret 管理

| Secret | 内容 | 位置 |
|--------|------|------|
| `ani-loki-s3-creds` | MinIO S3 access_key / secret_key | `ani-s07-observability` namespace |
| `ani-s05-minio-root` | MinIO root 凭据 | `ani-s05-objectstore` namespace（现有） |

**约束**：
- K8s Secret 不能跨 namespace 直接引用
- S3 凭据需在 `ani-s07-observability` namespace 重建（从 `ani-s05-minio-root` 读取真实值）
- yaml 中用占位值，注释说明部署前必须替换

### 7.4 网络安全

| 通信 | 加密 | 说明 |
|------|------|------|
| Console → Gateway | HTTPS | 现有 |
| Gateway → Prometheus | HTTP（集群内） | 现有 |
| Gateway → Loki | HTTP（集群内） | 推荐 `http://ani-loki.ani-s07-observability:3100` |
| Fluent Bit → Loki | HTTP（集群内） | DaemonSet push logs |
| Loki → MinIO S3 | HTTP（集群内） | `s3forcepathstyle: true` |

### 7.5 审计

本 SPEC 不涉及审计日志改动，延续现有 Core API 审计机制。

### 7.6 敏感信息保护

- S3 凭据通过 K8s Secret 注入，不写入 ConfigMap 或环境变量明文
- yaml 中 S3 凭据用占位值 `<REPLACE_WITH_MINIO_ACCESS_KEY>`，注释说明替换
- AI agent 不得把真实凭据写入可提交文件

---

## 8. Performance

### 8.1 性能目标

| 指标 | 目标 | 说明 |
|------|------|------|
| 指标快照响应 | ≤ 1s | local profile 基准，延续原 PRD |
| 指标时序查询 | ≤ 2s | Prometheus 查询，延续原 PRD |
| 日志首屏（Loki） | ≤ 2s | 100 条，Loki 单体模式 local profile 基准 `[Assumption]` |
| 日志翻页（Loki） | ≤ 1s | cursor 分页，limit=100 |
| 日志首屏（K8s API fallback） | ≤ 1s | 现有行为不变 |

### 8.2 资源占用

| 组件 | requests | limits | 说明 |
|------|----------|--------|------|
| Loki | cpu 100m / memory 256Mi | cpu 1 / memory 1Gi | 单体模式 |
| Fluent Bit | cpu 50m / memory 64Mi | cpu 200m / memory 128Mi | DaemonSet，每节点一份 |

### 8.3 容量规划

| 资源 | 容量 | 说明 |
|------|------|------|
| Loki 日志保留 | 30 天 | `retention_period: 30d` |
| Loki 存储 | MinIO S3 bucket `ani-loki-logs` | 复用现有 MinIO，不引入新组件 |
| Fluent Bit 缓冲 | 内存 + 文件系统 | DaemonSet 本地缓冲 |

### 8.4 并发与限流

| 操作 | 并发上限 | 限流策略 |
|------|----------|----------|
| 指标查询 | 现有 Prometheus 限流 | 不变 |
| 日志查询（Loki） | Loki 默认限流 | 不额外配置 |
| 日志查询（K8s API） | 现有 K8s API 限流 | 不变 |

### 8.5 性能风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| MinIO emptyDir 非持久化 | pod 重启丢日志 | 运维确认或改 PVC（OQ-3） |
| Loki 单点部署 | 无高可用 | 推荐示例，生产环境需扩展 |
| Fluent Bit 资源占用 | 每节点一份 | 限制 limits |
| 大量日志查询 | Loki 压力 | cursor 分页 + limit 上限 500 |

---

## 9. Testing Strategy

### 9.1 单元测试

| 模块 | 测试范围 | 覆盖路径 |
|------|----------|----------|
| `demo_instances.go::getMetrics` | handler 传 Kind | container/gpu_container/vm 三种 kind，断言 `request.Kind` 值 |
| `prometheus_instance_observability.go::GetMetrics` | VM 分支 | VM kind 触发 VM 分支，非 VM kind 不触发 |
| `prometheus_instance_observability.go::GetMetrics` | GPU 分支 | GPU kind 触发 GPU 分支（依赖 handler 传 Kind） |
| `prometheus_instance_observability.go::ListLogs` | fallback/LogStore 切换 | `logStore != nil` 走 LogStore，nil 走 K8s API |
| `loki_log_store.go::QueryLogs` | LogQL 构造 | namespace/pod label 正确注入 |
| `loki_log_store.go::QueryLogs` | cursor 双向映射 | RFC3339 ↔ Unix 纳秒 |
| `loki_log_store.go::QueryLogs` | Loki HTTP 响应解析 | stream 模式解析 |
| `prometheus_observability_service.go::rewritePromQLLabels` | name label 重写 | VM 模板的 name label 正确注入 |
| `prometheus_observability_service.go::rewritePromQLLabels` | 不回归 | container/gpu_container 的 pod label 重写不变 |
| `instance_observability_runtime.go` | 环境变量注入 | `loki`/`elasticsearch`/`k8s`/空 四种情况 |
| `promqlTemplates.ts::getTemplatesForKind` | VM 分支 | vm kind 返回 2 条 VM 模板，非 vm kind 不返回 |

### 9.2 集成测试

| 场景 | 验证点 |
|------|--------|
| `kind=gpu_container` 端到端 | GPU 字段非 null（DCGM 可用时） |
| `kind=vm` 端到端 | CPU/内存/网络字段来自 `kubevirt_vmi_*` 指标 |
| `kind=container` 不回归 | CPU/内存字段来自 cAdvisor，行为不变 |
| `kind != gpu_container` GPU 字段 | 为 null |
| `kind != vm` 不走 VM 分支 | 不受 VM 分支影响 |
| Loki 部署后日志查询 | Pod 删除后仍可分页浏览历史日志 |
| 未部署 Loki fallback | 行为与现状完全一致 |
| 跨租户隔离 | 租户 A 查不到租户 B 的日志/指标 |

### 9.3 部署验证

| 命令 | 期望结果 |
|------|----------|
| `kubectl wait deployment/ani-loki -n ani-s07-observability --for=condition=ready` | 成功 |
| `curl http://ani-loki.ani-s07-observability:3100/ready` | 返回 200 |
| `curl http://prometheus.ani-s07-observability:9090/api/v1/query?query=DCGM_FI_DEV_GPU_UTIL` | 非空结果 |
| `curl http://prometheus.ani-s07-observability:9090/api/v1/query?query=kubevirt_vmi_cpu_usage_seconds_total` | 非空结果 |
| `curl http://ani-loki.ani-s07-observability:3100/loki/api/v1/labels` | 返回 namespace/pod label |
| `curl 'http://ani-loki.ani-s07-observability:3100/loki/api/v1/query_range?query={namespace="ani-tenant-<id>"}&limit=10'` | 可查到指定 pod 日志 |

### 9.4 前端验证

| 场景 | 验证点 |
|------|--------|
| `kind=vm` 指标 Tab loading 态 | 显示 loading |
| `kind=vm` 指标 Tab empty 态 | 显示「暂不可用」 |
| `kind=vm` 指标 Tab error 态 | 显示错误提示 |
| `kind=vm` 时序图 2 条曲线 | CPU 利用率（蓝）、内存使用率（绿） |
| `kind=vm` 快照卡片 | CPU/内存/网络字段非 null（virt-handler 可用时） |
| `kind=gpu_container` 指标 Tab | GPU 字段非 null（DCGM 可用时） |
| `kind=container` 不回归 | 行为不变 |

### 9.5 Acceptance Criteria Mapping

| PRD AC | SPEC 测试覆盖 |
|--------|---------------|
| US-001 AC: handler 传 Kind | §9.1 handler 单测 |
| US-002 AC: DCGM scrape 配置 | §9.3 部署验证 |
| US-003 AC: GPU 字段非 null | §9.2 集成测试 |
| US-004 AC: LogStore port | §9.1 port 结构单测 |
| US-005 AC: Loki adapter | §9.1 LokiLogStore 单测 |
| US-006 AC: fallback/注入 | §9.1 ListLogs 单测 + runtime 单测 |
| US-007 AC: 推荐 yaml | §9.3 部署验证 |
| US-008 AC: KubeVirt scrape | §9.3 部署验证 |
| US-009 AC: VM 分支 | §9.1 GetMetrics VM 分支单测 |
| US-010 AC: name label 重写 | §9.1 rewritePromQLLabels 单测 |
| US-011 AC: VM 模板 | §9.1 getTemplatesForKind 单测 |
| US-012 AC: VM 快照 | §9.2 集成测试 + §9.4 前端验证 |

---

## 10. Implementation Plan

### 10.1 批次划分

| 批次 | 范围 | 依赖 | 验收命令 |
|------|------|------|----------|
| **B-1: handler 传 Kind + GPU 分支验证** | US-001 + US-003（依赖 US-002 scrape） | 无 | `make test` + `make validate-architecture` |
| **B-2: DCGM scrape 配置** | US-002 | B-1 | 部署后 `curl DCGM_FI_DEV_GPU_UTIL` 非空 |
| **B-3: LogStore port + Loki adapter + runtime 注入** | US-004 + US-005 + US-006 | 无 | `make test` + `make validate-architecture` |
| **B-4: Loki + Fluent Bit 部署 yaml** | US-007 | B-3 | §9.3 部署验证命令 |
| **B-5: KubeVirt scrape + VM 分支** | US-008 + US-009 | B-1 | 部署后 `curl kubevirt_vmi_*` 非空 |
| **B-6: PromQL name label 重写** | US-010 | 无 | `make test` |
| **B-7: Console VM 模板 + 配色** | US-011 | B-5 + B-6 | `npm run typecheck` + 浏览器验证 |
| **B-8: VM 快照验证** | US-012 | B-5 | §9.4 前端验证 |

### 10.2 依赖关系

```text
B-1 (handler 传 Kind) ─┬─→ B-2 (DCGM scrape) ─→ B-3 验证
                       └─→ B-5 (KubeVirt scrape + VM 分支) ─→ B-8 (VM 快照验证)
                                                                          ↑
B-3 (LogStore + Loki) ───→ B-4 (Loki 部署 yaml)                          │
                                                                          │
B-6 (name label 重写) ───→ B-7 (Console VM 模板) ─────────────────────────┘
```

### 10.3 实现顺序

1. **B-1（handler 传 Kind）**：最小改动，触发 GPU/VM 分支，无外部依赖，先合入解锁后续验证。
2. **B-3（LogStore + Loki adapter）**：独立模块，不依赖 B-1，可并行开发。
3. **B-6（name label 重写）**：独立改动，不依赖其他批次，可并行开发。
4. **B-2（DCGM scrape）**：依赖 B-1 合入，部署 yaml 后验证 GPU 指标端到端。
5. **B-5（KubeVirt scrape + VM 分支）**：依赖 B-1 合入，部署 yaml 后验证 VM 指标端到端。
6. **B-4（Loki 部署 yaml）**：依赖 B-3 合入，部署后验证日志持久化。
7. **B-7（Console VM 模板）**：依赖 B-5 + B-6，前端适配 VM 时序图。
8. **B-8（VM 快照验证）**：依赖 B-5，前端验证 VM 快照卡片。

### 10.4 交付物清单

| 交付物 | 路径 | 批次 |
|--------|------|------|
| handler 传 Kind | `repo/services/ani-gateway/internal/router/demo_instances.go` | B-1 |
| DCGM scrape 配置 | `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml` | B-2 |
| LogStore port | `repo/pkg/ports/log_store.go` | B-3 |
| Loki adapter | `repo/pkg/adapters/runtime/loki_log_store.go` | B-3 |
| LogStore 注入 | `repo/pkg/adapters/runtime/prometheus_instance_observability.go` + `repo/services/ani-gateway/instance_observability_runtime.go` | B-3 |
| Loki 部署 yaml | `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml` | B-4 |
| KubeVirt scrape 配置 | `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml` | B-5 |
| VM 分支 | `repo/pkg/adapters/runtime/prometheus_instance_observability.go` | B-5 |
| name label 重写 | `repo/pkg/adapters/runtime/prometheus_observability_service.go` | B-6 |
| VM PromQL 模板 | `repo/frontends/console/src/features/instance-observability/promqlTemplates.ts` | B-7 |
| VM 配色 | `repo/frontends/console/src/features/instance-observability/MetricsChart.tsx` | B-7 |

---

## 11. Open Questions

### 11.1 已关闭的 Open Questions

| ID | 问题 | 决策 | 关闭依据 |
|----|------|------|----------|
| **OQ-4** | PromQL label 重写支持 `name` label 的实现方式：扩展 `rewritePromQLLabels` 还是 VM 模板用 `{{instance_id}}` 占位符由前端注入？ | **扩展 `rewritePromQLLabels` 支持 `name` label**（本 SPEC D-13） | 现有重写链路统一；前端模板风格一致；后端统一处理租户隔离和实例名注入 |

### 11.2 维持 PRD 假设的 Open Questions

| ID | 问题 | PRD 假设 | 决策方 | 本 SPEC 影响 |
|----|------|---------|--------|---------------|
| OQ-1 | VM 方案是否在当前迭代合入，还是等平台 VM 功能就绪后再合入？ | VM 方案代码改动在本 PRD 范围内准备，但端到端验证依赖平台 VM 功能就绪；合入时机由人工审核决定 | 人工审核 | 本 SPEC 提供完整 VM 方案技术实现，合入时机由人工决定 |
| OQ-2 | Loki + Fluent Bit 部署 yaml 是否在当前迭代实际部署到 real-k8s-lab，还是仅作为推荐示例代码入库？ | yaml 入库作为推荐示例，实际部署由运维决定 | 运维 | 本 SPEC 提供 yaml 入库，部署由运维决定 |
| OQ-3 | MinIO emptyDir 非持久化风险是否接受，还是必须先改 MinIO 用 PVC？ | 运维确认接受临时部署仅用于验证，或先改 MinIO 持久化 | 运维 | 本 SPEC yaml 头部标注风险，由运维决定 |
| OQ-5 | ES adapter 何时实现？本 PRD 只实现 Loki adapter | ES adapter 后补，未实现时 `INSTANCE_OBSERVABILITY_LOG_STORE=elasticsearch` 走 fallback | 后续 PRD | 本 SPEC 不实现 ES adapter，runtime 代码已预留 case 分支 |
| OQ-6 | Prometheus ClusterRole 是否已授权 `kubevirt` namespace pods 读权限？若无则需新增 | 部署前确认，若无则新增 RBAC | 运维 | 本 SPEC §5.10 标注 RBAC 确认，部署前检查 |

### 11.3 SPEC 新增 Open Questions

无新增 Open Questions。本 SPEC 的所有技术决策都在 §1.3 Design Decisions Summary 中明确，无遗留问题。

---

## References

- PRD: `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md`
- UX: `repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md`
- 方案来源: `repo/services/tasks/modules/prd/console/compute/plan.md`
- 原 SPEC: `repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md`
- Core API 契约: `repo/api/openapi/v1.yaml`（不改）
- KubeVirt metrics 总表: https://kubevirt.io/monitoring/metrics.html
- Loki 官方文档: https://grafana.com/docs/loki/latest/
- Fluent Bit 官方文档: https://docs.fluentbit.io/
- DCGM exporter 部署事实: `repo/development-records/sprint13-gpu-inventory-dcgm-readiness.md`
