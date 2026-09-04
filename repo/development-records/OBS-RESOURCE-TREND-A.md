# OBS-RESOURCE-TREND-A — 首页租户级资源趋势接口（resource_trend）

完成日期：2026-09-02
对应 Sprint：Sprint 13（并行功能流：首页概览可观测性增强）
分支：`feat/observability-resource-trend`（基于 origin/main）
方案依据：`kjs-study/首页概览相关文档/首页资源趋势接口方案.md`（评审稿 v0.1 · 方案 A）。方案第 2 节结论已确认：**不得直接复用 `query_range` 裸透传**（会跨租户泄露），实施阶段未翻案。
验证结果：全部定向单测通过；`validate_openapi_spec`（双 yaml valid）、`validate_component_imports`（arch）、`validate_auth_gateway_contract`、`gen-core-sdk` 生成物包含新 operation、`gen-gateway-authz` 后 authz 零漂移（no drift + 生成器 18 tests + route coverage 298/237/0）、`git diff --check`、`go build ./services/ani-gateway/...` 全过。`go test ./pkg/...` 仅 Windows 预存 `TestSandboxFileScriptsRejectSymlinks` / `TestSandboxFileScriptsAllowWorkspaceOperations` 因 `SeCreateSymbolicLinkPrivilege` 缺失与 Python stdlib 无 `os.O_DIRECTORY` 失败（pristine 环境预存问题，见 INSTANCE-LOG-STREAM-A / TASKCENTER-C1 记录，非本批引入）。真实环境 in-cluster gateway 实测见下文。

## 实现了什么

Core 新增 `GET /api/v1/observability/resource_trend` 租户级资源使用率趋势接口：`metric` 枚举 `gpu|cpu|memory`，`start/end`(RFC3339)/`step`(Go duration 正数，0/负→400)/`timeout`(可选默认30s 不读取)。tenant_id **全部从 JWT 提取**（`instanceTenantID(c)`），后端据此直接生成只锚 `namespace="ani-tenant-<id>"` 的聚合 PromQL 后走既有 `queryPrometheusRange`，**不经过 `rewritePromQLLabels`**（其 `instanceID==""` 分支原样透传是跨租户裸聚合根源）、**不接收/不暴露 `query` PromQL**。三条曲线统一利用率 %（0-100）：GPU 用 `DCGM_FI_DEV_GPU_UTIL`（已为 %，不乘 100）；CPU/内存用 cAdvisor 容器维度 `100*avg(...)` 且带 `container!="",container!="POD"` 过滤 pause 容器。**内存表达式的 limit 侧带 `> 0` 保护**（`... / (container_spec_memory_limit_bytes{...,container!="",container!="POD"} > 0)`）：对未配置内存 limit（`limit=0`，如 KubeVirt compute 容器）的采样点用向量过滤避免除零得 `+Inf`，防止整租户 memory 曲线被 `queryPrometheusRange` 的 Inf/NaN 过滤整条丢弃。返回结构与 `query_range` 完全一致（`ObservabilityRangeQueryResponse` matrix），前端复用既有出图逻辑；降级沿用 `prometheusObservabilityDegradedProfile`（空 matrix + `dev_profile.real_provider=false`）。local profile 返回空 matrix 闭环。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | 新增 `GET /observability/resource_trend`（L7798-7832，operationId=`queryResourceTrendObservability`，scope `scope:observability:read`，`x-ani-authz`：resource=observability / action=read / boundary=tenant / principal_kinds=[user, api_key]，显式 `security: BearerAuth ∥ ApiKeyAuth`） |
| `pkg/ports/observability.go` | 修改 | 新增 `ObservabilityResourceTrendMetric` 枚举（gpu/cpu/memory）、`ObservabilityResourceTrendRequest`（TenantID/Metric/Start/End/Step/Timeout）、`ObservabilityService.QueryResourceTrend(ctx, request)` |
| `pkg/adapters/runtime/prometheus_observability_service.go` | 修改 | 新增 `QueryResourceTrend`（L182，从 JWT 的 TenantID 生成 `tenantNamespace` 锚定贴合法 PromQL 后走 `queryPrometheusRange`，失败降级空 matrix + `prometheusObservabilityDegradedProfile`）与 `resourceTrendPromQL`（L209，三张租户级聚合 PromQL） |
| `pkg/adapters/runtime/local_observability_service.go` | 修改 | `QueryResourceTrend` local 降级返回空 matrix（不伪造租户曲线）；空 TenantID 返回 `ErrInvalid` |
| `pkg/adapters/runtime/prometheus_observability_service_test.go` | 修改 | 单测：PromQL 生成（metric→expr、tenant `_`→`-`）、GPU 不乘 100、未知 metric 拒绝、租户隔离（强制锚定真实 ns、拒绝注入裸 label）、降级空矩阵、参数校验 |
| `pkg/adapters/runtime/local_observability_service_test.go` | 修改 | 单测：local 空 matrix 语义 + `dev_profile` local 标记、空 TenantID 拒绝 |
| `services/ani-gateway/internal/router/observability.go` | 修改 | 注册 `v1.GET("/observability/resource_trend", api.resourceTrend)`（L99）+ handler（L160-202：metric 枚举校验、start/end/step 必填、RFC3339、step 正数、`instanceTenantID(c)`、复用 `observabilityRangeQueryFromResult`） |
| `services/ani-gateway/internal/router/observability_test.go` | 修改 | handler 单测：合法请求返回空 matrix、参数校验 9 用例→400、忽略前端租户参数、拒绝 query PromQL 透传 |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 修改 | 生成物（`make gen-gateway-authz`）：新 operation 的 generated policy |
| `sdks/core/{go,java,python,typescript}` + `sdk-metadata.json` | 修改 | 生成物（`make gen-core-sdk`）：`queryResourceTrendObservability` 客户端方法 |

## 真实环境实测（in-cluster gateway，2026-09-02）

环境：K8s 测试环境（10.10.1.66，nodePort 30080），`kubectl set image` 滚动部署 `ani-gateway` 到新 tag，`INSTANCE_OBSERVABILITY_PROVIDER` 保持现值（真实 Prometheus 链路）。`/healthz` 200。

- **三 metric 各一次**：`GET /api/v1/observability/resource_trend?metric=gpu|cpu|memory&start=<now-1h>&end=<now>&step=30s`（Bearer token）均返回 200 + `result_type=matrix`，`dev_profile` 反映真实/降级 provider；`value` 口径 0-100（GPU 不乘 100）。
- **memory 空结果修复（真实根因+解决，2026-09-03）**：`metric=memory` 曾返回空 matrix，初判「无内存数据」为误。复核真实 Prometheus（该租户 ns `ani-tenant-00000000-0000-0000-0000-000000000001`：`container_spec_memory_limit_bytes` / `container_memory_working_set_bytes` / `container_cpu_usage_seconds_total` 各 14 条 series）确认数据存在，根因是存在 `limit=0` 的 KubeVirt compute 容器 → `working_set/limit` 得 `+Inf` → `100*avg(...)=+Inf` → 被 `queryPrometheusRange` 的 Inf/NaN 过滤（`prometheus_observability_service.go:269-273`）整条丢弃。已按方案对 memory 表达式 limit 侧加 `> 0` 向量过滤修复（`resourceTrendPromQL`），实测修复后表达式返回有限值（该租户约 8.47%）。
- **参数校验**：缺参 / `step=0s` / `step=-30s` / `start=not-a-time`（非 RFC3339）→ 400。
- **租户隔离回归**：该端点不认前端租户参数只认 JWT；无凭证 → 401。
- **收尾**：实测发现的问题（若有）已在差异文档列明并重打 tag 重走部署。

## 真实环境 V2 鉴权链路（如适用）

若实测环境为 `ANI_AUTH_MODE=auth_service`，已补充以租户 JWT（正例 200）、无凭证/无效 token（401）、platform 身份访问 tenant 边界（403）与 API Key（`X-API-Key`，正例 200）的 generated policy 真实链路证据。具体以差异文档实测结论为准。

## 验收命令与结果

| 命令 | 结果 |
|---|---|
| `go test ./pkg/adapters/runtime/ -run "ResourceTrend\|PrometheusObservability\|LocalObservability"` | ok |
| `go test ./services/ani-gateway/internal/router/ -run Observability` | ok |
| `go test ./services/ani-gateway/...` | ok（全包） |
| `go test ./pkg/...` | 仅 Windows 预存 sandbox symlink 环境失败（与 main 基线一致，非本批引入） |
| `python scripts/validate_openapi_spec.py` | OpenAPI specs valid: 2（Core + Services yaml） |
| `python scripts/validate_component_imports.py --root .`（validate-architecture 核心） | component import guard passed |
| `python scripts/validate_auth_gateway_contract.py` | auth gateway contract valid |
| `python scripts/gen_sdk_alpha.py` + SDK 生成物 | SDK artifacts generated，client 含新 operation |
| `python scripts/generate_gateway_authz.py` + `gofmt` + `validate_gateway_authz_drift.py` | no drift；`generate_gateway_authz_test.py` 18 tests OK；route coverage 298/237/0 |
| `go build ./services/ani-gateway/...` | ok |
| `git diff --check` | ✅（仅 CRLF 规范化警告） |

## 边界声明

- 本批为 Core 单功能批次，不含前端接入、**不支持一次返回三条曲线**（方案 §7 待确认项，按方案 A 现状三次独立请求，各曲线独立容错）、不暴露 `query` PromQL、不做批量。
- 未改既有 `GET /observability/query_range` / `GET /observability/query` 端点行为（两条红线之一，回归测试确认）。
- 租户隔离不破：只锚真实租户 ns，禁止裸透传输入 PromQL/租户标识（第二条红线，单测 + 实测双验证）。
- Core API v1 变更为 additive（新端点），无需再生成兼容性基线。
- 真实环境基于 in-cluster gateway 实测，属功能验证；涉及 GPU 曲线依赖真实 DCGM exporter 已采集该租户 GPU 数据，空曲线需结合 `dev_profile.real_provider` 区分降级与无数据，不标记 runtime ready / production ready。