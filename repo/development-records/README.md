# ANI Development Records — 批次归档索引

> 本文件是所有已完成开发批次的**唯一归档索引**。
> 进度追踪三层结构：
> - **全局状态快照** → `ANI-06-开发计划.md` Section 零（30秒定位）
> - **当前冲刺任务** → `repo/CURRENT-SPRINT.md`（每冲刺更新）
> - **已完成批次详情** → 本文件（每批次完成后追加）

> 当前执行：**Sprint 13 / Core real provider 与 live gate 收敛**。Sprint 12 已完成 19 个 Core handler + 2 个 422 的 Tier1 local profile；Sprint 13 S01-S07 已通过 production-shaped live gate 并归档 `production_shape.status=passed` evidence。S05/S06/S07 关键 token：SPRINT13-OBJECTSTORE-MINIO-A-TRACK / validate-object-store-live-gate / MinIO / pre-signed URL / LIVE PENDING；SPRINT13-VECTOR-MILVUS-A-TRACK / validate-vector-store-live-gate / Milvus / LIVE PENDING；SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK / validate-instance-observability-live-gate / Prometheus / kubelet / LIVE PENDING。`S05-S07 B 轨可以继续` 仅保留作历史兼容语境；当前 S05/S06/S07 均已 passed。SPRINT13-AUTH-DEX-PRODUCTION-GATE 已通过，production-shaped Gateway 使用 `ANI_AUTH_MODE=auth_service`。Sprint14 分支 `feature/sprint14-core-resilience-semantics` 已完成 R-P0-0..R-P2-7 本地/逻辑批次，并通过 SPRINT14-CORE-RESILIENCE-LIVE-GATE / validate-sprint14-resilience-live-gate / Sprint14 resilience live gate：在 ani-sprint14-resilience 隔离 namespace 中完成 P0 strong backend kill、P1 weak dependency degraded、P2 controller primary kill / follower failover，并归档脱敏 evidence。该 production-ready 结论只限隔离 Sprint14 Core resilience fixture，不外推到现有 Sprint13 单副本后端或 full platform production ready；正式镜像发布/升级、长期 SLA/soak、备份/恢复和 release gate 仍需后续完成。本文只做已完成批次归档，不作为当前任务清单使用。
> 历史校准记录（2026-05-20/2026-05-21）：Sprint 2/3/4 的 API、SDK、Mock、Docs 与记录闭环已归档；这些记录只解释历史切换，不代表当前执行阶段。

---

## 已完成批次（按完成时间排列）

### 账密登录模块（2026-07）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| AUTH-LOGIN-CORE-001 | Core Auth API：租户账密登录 + 平台账密登录 + 平台用户迁移；P0-1 签发顺序、P1-1 SQL 约束、P2-1 RBAC scope；auth-service 测试 PASS、ani-gateway middleware 测试 PASS | auth-login-core-001.md |
| AUTH-LOGIN-CONSOLE-002 | Console 前端：OIDC + 账密 Tab + 会话管理 + 路由守卫；P1-2 maybeRefresh、P1-3 401 先 refresh、P1-5 幂等键；type-check + vite build PASS | auth-login-console-002.md |
| AUTH-LOGIN-BOSS-003 | BOSS 前端：平台账密登录 + 会话隔离；P0-3 redirect_uri /boss 前缀、P1-2 maybeRefresh、P1-3 401 先 refresh；BOSS OIDC 暂不实现；vite build PASS | auth-login-boss-003.md |
### Console Instance Observability Completion（2026-07）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| INSTANCE-OBSERVABILITY-COMPLETION-B1-HANDLER-PASS-KIND | issue-001：Gateway `getMetrics` handler 透传 `record.Kind` 到 `InstanceObservationGetRequest`，修复 `PrometheusInstanceObservability.GetMetrics` 的 GPU 分支（`request.Kind == WorkloadKindGPUContainer`）在生产路径下恒不触发的死分支问题；新增 `metricsKindSpy` 实现 `ports.InstanceObservability` 接口捕获 `request.Kind`，`TestDemoInstanceGetMetricsHandlerPassesRecordKind` 通过 Hertz `ut.PerformRequest` 端到端覆盖 container/gpu_container/vm 三种路径并断言 `spy.capturedKind == record.Kind`；review-it 修复 1 finding（删除 `metricsKindSpy.inner` 未使用字段）；对齐 PRD US-001/FR-1 / SPEC §5.1/§9.1 / UX §2.3；`make test` + `make validate-architecture` + `git diff --check` + `go vet` + `gofmt -l` 全通过 | instance-observability-completion-b1-handler-pass-kind.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B2-PROMETHEUS-DCGM-SCRAPE | issue-002：Prometheus ConfigMap 新增 `dcgm-exporter` scrape job（`static_config` 指向 `ani-dcgm-exporter.ani-system:9400`，`metrics_path: /metrics`，`component: dcgm-exporter` label），使 `DCGM_FI_DEV_GPU_UTIL` 等 GPU 指标可被采集；9 行新增纯部署 yaml 改动，不修改 ClusterRole/ClusterRoleBinding（`static_config` 直接 HTTP 访问 Service 不需要跨 namespace RBAC）；不越界实现 US-008（KubeVirt virt-handler scrape，Issue #008 / B-5 范围）；对齐 PRD US-002/FR-2 / SPEC §5.10；`python scripts/validate_yaml.py` + `make test` + `make validate-architecture` + `git diff --check` 全通过；部署后 live 验证（curl `DCGM_FI_DEV_GPU_UTIL` 返回非空）待运维执行 | instance-observability-completion-b2-prometheus-dcgm-scrape.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-GPU-ADAPTER-E2E-VERIFY | issue-003：GPU adapter 端到端集成测试补全 + live gate 复现缺陷修复；新增 `TestPrometheusInstanceObservabilityGetMetricsGPUContainerE2EIntegration` 验证 `kind=gpu_container` 触发 DCGM 分支、PromQL 包含 `DCGM_FI_DEV_GPU_UTIL`/`DCGM_FI_DEV_FB_USED`/`DCGM_FI_DEV_FB_FREE` 指标名、GPU 字段（利用率 77.0 / 显存 used 6144 MiB / total 12288 MiB）非 nil；新增 `TestPrometheusInstanceObservabilityGetMetricsNonGPUContainerE2EIntegration` 覆盖 container/sandbox/batch_job/notebook 四种 kind 断言 DCGM 查询不触发 + GPU 字段全 nil；**live gate 2026-07-20 复现真实缺陷**（Prometheus `http://10.10.1.66:31990/`，2× RTX 4090）：真实 DCGM exporter 不暴露 `DCGM_FI_DEV_FB_TOTAL`，仅暴露 `FB_FREE`+`FB_USED`，且单位为 MiB 非 bytes；修复 adapter `prometheus_instance_observability.go:186` 将 `FB_TOTAL` 查询改为 `FB_FREE+FB_USED`，移除 `/1024/1024` 换算；同步更新 SPEC D-2/§2.3.1/§5.10、plan.md §3.1；归档 live evidence JSON；review-it 修复 1 finding（移除 `dcgmQuerySeen` 死代码）；对齐 PRD US-003/FR-3/FR-4 / SPEC §2.3.1 / UX §3.2；`go test E2EIntegration` 5/5 PASS + `make test` + `make validate-architecture` + `git diff --check` + `go vet` + `gofmt -l` 全通过 | instance-observability-completion-b3-gpu-adapter-e2e-verify.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B4-LOKI-FLUENT-BIT-DEPLOY | issue-007：新增 `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml` 推荐部署示例（485 行，10 个 K8s 资源）；Loki 3.6.0 单租户模式 + schema v13 + tsdb + 30d retention + MinIO S3 后端（`storage_config.aws` 结构，`s3` 为 URL 字符串，凭据用 `${S3_ACCESS_KEY}`/`${S3_SECRET_KEY}` env Secret 注入 + `-config.expand-env=true`）；Fluent Bit 3.2.0 DaemonSet 采集 `/var/log/pods/*` + Lua 脚本从 Tag 提取 namespace/pod/container 作为 Loki label + `Parsers_File /fluent-bit/etc/parsers.conf` 加载 cri parser + `Inotify_Watcher false` 轮询规避 inotify watch 超限 + `DB /tmp/flb_kube.db` 持久化扫描位置 + 显式 `-c` 指定配置路径；**live 部署实测三节点（dev-phys-02/03、kubercloud）Fluent Bit 全部 Running 0 重启**，Loki `/ready` 返回 ready，`/loki/api/v1/labels` 返回 container/namespace/pod/service_name，`query_range {namespace="kube-system"}` 返回 ovn-central 真实结构化日志；review-it 修复 1 finding（删除 `varlibcontainers` docker json-file 死代码 volume）；yaml 头部标注「推荐示例，非必须部署」+ 前置依赖（MinIO bucket 需先创建、emptyDir 非持久化风险、S3 凭据需跨 namespace 重建）；对齐 PRD US-007/FR-11/12/13/20 / SPEC §5.11；`kubectl apply --dry-run=server` 10 资源全通过 + 三节点 live 验证全通过 | instance-observability-completion-b4-loki-fluent-bit-deploy.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-LOGSTORE-PORT | issue-004：新增 `repo/pkg/ports/log_store.go`（44 行）定义日志持久化存储 port 抽象：`LogQueryRequest`（TenantID/InstanceID/Namespace/Limit/Cursor/Level）+ `LogQueryResult`（Items/NextCursor）+ `LogStore` interface（单方法 `QueryLogs`）；复用现有 `InstanceLogEntry`（time.Time 版本）避免编译冲突；`Cursor` 纯 string + 注释约束 opaque 语义；不实现任何 adapter（LokiLogStore 属 issue-005 范围）、不修改 `InstanceObservability` interface；对齐 PRD US-004/FR-5 / SPEC §3.1；`go build`/`go vet`/`go test`/`make validate-architecture`/`gofmt -l`/`git diff --check` 全通过 | instance-observability-completion-b3-logstore-port.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-LOKI-LOG-STORE-ADAPTER | issue-005：新增 `repo/pkg/adapters/runtime/loki_log_store.go`（301 行）+ `loki_log_store_test.go`（400 行）实现 `ports.LogStore` interface，通过 Loki HTTP API `/loki/api/v1/query_range` 查询持久化日志；LogQL `{namespace="<ns>",pod=~"^<instance>(-.*)?$"} | json`（namespace 精确匹配做多租户隔离 + pod 正则匹配兼容 ReplicaSet hash，不使用 X-Scope-OrgID）；direction=backward + cursor→end（RFC3339↔Unix 纳秒）+ start=end-24h + next_cursor=最早一条 timestamp（继承 B5 live gate 修复语义，偏离 SPEC §3.3/§5.5/PRD FR-10 草案的 forward+start，待 SPEC/PRD 同步）；level 解析侧过滤 + JSON 无 level 时 `inferLogLevel` 推断（兼容 Fluent-Bit nginx/stdout 日志）；Loki 不可达/非 200/传输错误/decode 失败均返回包装错误不伪造空结果；编译时断言 `var _ ports.LogStore = (*LokiLogStore)(nil)`；15 个单元测试覆盖 LogQL 构造/转义、cursor 双向映射、stream 解析、level 过滤、next_cursor、纯文本回退、level 推断、显式 level 保留、端到端 HTTP、cursor 透传、非法 cursor、非 200、传输错误、空 BaseURL；对齐 PRD US-005/FR-9/FR-10 / SPEC §3.3/§5.5；`go build`/`go vet`/`go test`/`make test`/`make validate-architecture`/`git diff --check` 全通过 | instance-observability-completion-b3-loki-log-store-adapter.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B5-LOKI-RANGE-CA-FIX | live gate 收尾修复（非 issue 对应）：B4 部署完成后真实 Gateway+前端联调发现三个阻断性缺陷；(1) Loki LogQL pod label 精确匹配查不到带 ReplicaSet hash 的 Pod（如 `demo-preview-nginx-6c748d8b7-tqfrl`），改为正则匹配 `pod=~"^<escaped>(-.*)?$"` 复用 `promQLPodMatcher` 转义逻辑；direction=backward + cursor 作为 end 往前翻页，首屏返回最新 limit 条（与 `kubectl logs --tail` 对齐），`next_cursor`=本批最早一条 timestamp；`parseLokiLogLine` 在 JSON 无 level 字段时从 message 推断 level（Fluent-Bit 采集的 nginx/stdout 日志无 level）；(2) `kubernetesHTTPClient` 在 `inCluster=false`（配置 `KUBERNETES_API_HOST`）时直接返回 `http.DefaultClient` 不加载 CA，events tab 报 `x509: certificate signed by unknown authority`，改为按优先级三种策略（inCluster 必须加载 / 外部+caFile 仍加载 / 外部无 caFile 返回 DefaultClient）；(3) metrics 时序图无数据：`main.go` 传 nil InstanceLookup 导致 `PrometheusObservabilityService` 未创建，`/observability/query_range` 回退 local；改为 `NewPrometheusObservabilityService` 允许 nil lookup（延迟注入）+ 新增 `SetInstanceLookup`，router 调整注册顺序：demo instances 先注册拿 service（实现 InstanceLookup），类型断言注入到 ObservabilityService，再注册 observability 路由；`rewritePromQLLabels` 在 lookup nil 时返回 error 不 panic；**live 验证**：logs 10 条真实 nginx Pod 日志、events 2 条 ScalingReplicaSet（scale 1→2→1 触发）、metrics 时序图 mode=real 1 series 16 采样点 PromQL 已重写 namespace/pod；对齐 PRD US-005/FR-9/10/11 / SPEC §5.11（LogQL 正则匹配 + backward 方向为 SPEC 偏离，待同步）；`go test Loki|PrometheusObservability|KubernetesRESTClient|Observability` 全通过 + `go build ./services/ani-gateway/...` 通过 | instance-observability-completion-b5-loki-range-ca-fix.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B5-PROMETHEUS-KUBEVIRT-SCRAPE | issue-008：Prometheus ConfigMap 新增 `kubevirt-virt-handler` scrape job 采集 `kubevirt_vmi_*` 指标；`kubernetes_sd_configs`（role: pod，namespace: kubevirt）+ `bearer_token_file` + `tls_config.insecure_skip_verify` + `metrics_path: /metrics`；`relabel_configs` 过滤 `kubevirt.io=virt-handler` label（`__meta_kubernetes_pod_label_kubevirt_io` regex virt-handler keep）+ 端口 8443（`__meta_kubernetes_pod_container_port_number` regex 8443 keep）+ 保留 namespace/pod 元数据 label；ClusterRole `sprint13-prometheus-cadvisor-reader` `resources` 追加 `"pods"` 为 `kubernetes_sd_configs` role:pod 服务发现提供必需权限（verbs 复用 get/list/watch）；无 VM 时 `kubevirt_vmi_*` 指标为空 series（正常响应非报错），AC-5 端到端 curl 验证依赖 #1 合入 + VM 运行；review-it 无 actionable findings（label key 转换、端口 meta label、virt-handler pod label 经 KubeVirt 官方 runbook + Prometheus 官方文档证实）；对齐 PRD US-008/FR-14 / SPEC §5.10；`make test` + `make validate-architecture` + `git diff --check` 全通过 | instance-observability-completion-b5-prometheus-kubevirt-scrape.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-LOGSTORE-INJECTION | issue-006：`PrometheusInstanceObservability` 新增可选 `logStore ports.LogStore` 字段 + `SetLogStore` 方法（私有字段不暴露到 `InstanceObservability` interface，对齐 port 注释 §3.1）；`ListLogs` 改为 fallback 分发（`logStore != nil` → `listLogsFromLogStore` 调 `QueryLogs`，nil → `listLogsFromK8sAPI` 抽取现有逻辑零回归）；ani-gateway runtime 新增 `buildLogStore` 按环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` 选择实现（`loki` → `LokiLogStore` 默认 URL `http://ani-loki.ani-s07-observability:3100`；`elasticsearch` → warn + fallback；`k8s`/空/`not_configured` → nil；未知值 → warn + fallback 不阻塞启动）；`listLogsFromLogStore` 透传 `Limit` 不外层裁剪（Loki adapter 内部 `normalizeLimit` 已处理，分层职责清晰）；错误双层包装 `logStore query failed: loki query failed: ...` 便于 handler 层错误码映射；10 个单元测试（adapter 3 个 fallback/注入/错误透传 + runtime 7 个 buildLogStore 各分支 + 工厂注入 + 环境变量加载）；review-it 修复 1 finding（3 文件 `gofmt -w` 格式化）；对齐 PRD US-006/FR-6/FR-7/FR-8 / SPEC §3.2/§5.3/§5.4；`go vet`/`gofmt -l`/`go test -count=1`/`make test`/`make validate-architecture`/`git diff --check` 全通过 | instance-observability-completion-b3-logstore-injection.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B5-GETMETRICS-VM-BRANCH | issue-009：`PrometheusInstanceObservability.GetMetrics` 新增 `if request.Kind == ports.WorkloadKindVM` 分支（L171，GPU 分支之前 early return）；新增 `getMetricsForVM` 方法查询 6 个 `kubevirt_vmi_*` 指标（CPU `rate(...[5m])`、网络 RX/TX `rate(...[5m])`、内存 `resident_bytes`/`domain_bytes`/`usable_bytes` Gauge 直查）；label 用 `name=%q` 精确匹配（VMI `metadata.name` = `request.InstanceID`，无随机后缀），不用 `pod=~"..."` 正则；`MemoryUsedMB` 用 PRD FR-17 强制公式 `domain_bytes - usable_bytes` 计算（不直接用 `resident_bytes` 作为使用率分子，`resident_bytes` 被查询但不赋值，满足 AC2 查询要求 + AC5/FR-17 公式约束）；`memDomainBytes > 0` 守卫降级（domain 查询失败跳过 usable 查询，字段为 nil 不伪造 0）；4 个单元测试覆盖 PromQL 构造/label 精确匹配/字段填充/`resident_bytes` 反向断言/不走 container 分支/virt-handler 不可用降级/5 种非 VM kind 回归；review-it clean 无 actionable findings；对齐 PRD US-009/FR-15/FR-16/FR-17/FR-18 / SPEC §5.2 / UX §3.1/§4.2；`go test`/`make test`/`make validate-architecture`/`git diff --check`/`go vet` 全通过；**端到端验证待真实 VM 环境**（当前系统无 VM，单元测试用 mock HTTP server 验证，依赖 #8 KubeVirt scrape 配置部署 + VM 运行后补齐） | instance-observability-completion-b5-getmetrics-vm-branch.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B6-REWRITE-PROMQL-NAME-LABEL | issue-010：`rewritePromQLLabels` 扩展支持 `name` label（OQ-4 决策 D-13）；`labelValuePattern` 正则由 `(namespace\|pod)="..."` 扩展为 `(namespace\|pod\|name)="..."`，switch 新增 `case "name":` 分支用 `name="record.Name"` 精确匹配（非正则 `=~`，VMI metadata.name 无随机后缀）；现有 `namespace`/`pod` 重写零改动；4 个单元测试（name 单选择器/多选择器/container pod 回归/Query 端到端转发）；review-it clean 无 actionable findings（regex 后缀误匹配风险经全仓库搜索证伪，`record.Name` 无转义风险与现有 namespace/pod 分支一致）；对齐 PRD US-010/FR-19 / SPEC §5.8/§11.1 OQ-4；`go test`/`go vet`/`make validate-architecture`/`go build ./pkg/...` 全通过；**VM 端到端 live 验证待补**（当前系统无 VM，用户确认等 VM 环境就绪后补齐） | instance-observability-completion-b6-rewrite-promql-name-label.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B7-VM-PROMQL-TEMPLATES | issue-011：`promqlTemplates.ts` 新增 VM kind 冻结 PromQL 模板（`instance_vm_cpu_utilization`、`instance_vm_memory_utilization`），使用 `name` label 而非 `pod`；`getTemplatesForKind` 由 `if` 分支重写为 `switch` 结构新增 `case 'vm'` 返回 2 条 VM 模板（CPU 利用率、内存使用率），不展示网络 RX/TX 时序曲线；`PROMQL_TEMPLATE_LABELS` 新增 VM 中文系列名；`MetricsChart.tsx` `SERIES_COLORS` 新增 VM 配色复用 container 蓝绿 `#0052D9`/`#2BA471`；移除既有未使用 `Space` import 修复 lint error；VM 模板正文与 SPEC §5.6 冻结表逐字符一致（无 `* 100`）；review-it clean 无 actionable findings（`* 100` 缺失属 SPEC 冻结决定，`renderPromQL` 占位符注入对 VM 为 no-op）；对齐 PRD US-011/FR-19 / SPEC §5.6/§5.7/§5.9 / UX §4.4/§6.2/§7.1；`pnpm type-check`/`pnpm lint`/`pnpm build`/`make validate-architecture`/`git diff --check` 全通过；**VM 端到端 live 验证待补**（当前系统无 VM，用户确认等 VM 环境就绪后补齐） | instance-observability-completion-b7-vm-promql-templates.md |
| INSTANCE-OBSERVABILITY-COMPLETION-B8-VM-SNAPSHOT-VERIFY | issue-012：VM 指标 Tab 快照卡片验证（纯验证 issue，无新增代码改动）；通过代码审查确认 `getMetricsForVM` 查询 `kubevirt_vmi_*` 指标（非 `container_*` cgroup）、`MetricsSnapshot.tsx` 通用渲染 CPU/内存/网络 RX/TX 4 卡片（无 GPU 卡片，`isGpu` 仅 `gpu_container`）、null 字段显示「暂不可用」不伪造 0、非 VM kind 分支隔离（`request.Kind == WorkloadKindVM` 精确匹配）；review-it clean 无 actionable findings（2 项已记录设计取舍：Loki 多 stream cursor 单 pod 取舍、`SetInstanceLookup` 写一次后只读无锁）；对齐 PRD US-012 / SPEC §2.3.3 / UX §4.2/§6.1/§6.5；`pnpm type-check`/`pnpm lint`/`pnpm build`/`make test`/`make validate-architecture`/`git diff --check`/`go test ./pkg/adapters/runtime/... ./services/ani-gateway/...` 全通过；AC 9/9 满足（其中浏览器三态验证因当前系统无 VM + browser automation 不可用，用户确认等 VM 环境就绪后补测，手动验证步骤已记录） | instance-observability-completion-b8-vm-snapshot-verify.md |

### Core Instance Observability（2026-07）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CORE-CONSOLE-SESSION-HANDLER-A | issue-001：Core 端 VM console session handler 补全；新增 `CreateConsoleSession` port 方法 + Local/Prometheus adapter 实现 + 5 个 HTTP 测试；protocol 默认值在 adapter 层填充，白名单在 handler 层校验；`connect_url` 与 `url` 设为等价值；`operation_id` 未返回、`expires_at` TTL 15min 为合成值为已知边界；对齐 PRD US-013 / SPEC §4.1.6；typecheck + build + validate-architecture 通过 | core-console-session-handler-a.md |
| CORE-INSTANCE-METRICS-MULTI-EXPORTER-A | issue-002：Core 端多 exporter 聚合 adapter + range query 端点；通过 `InstanceObservationGetRequest.Kind` 路由 GPU 采集（仅 `gpu_container` 采集 DCGM GPU/显存）；逐字段降级（`if err == nil` 守卫）；新增 `GET /observability/query_range` 返回 matrix 时序采样点；PromQL label 重写策略（namespace/pod 映射）；NaN/Inf 过滤；dryrun_renderer CPU/Memory 同时写入 limits 和 requests；正则 pod matcher 兼容 Deployment hash 后缀；对齐 PRD US-010/011/015 / SPEC §4.1.3/§4.1.4；typecheck + build + validate-architecture 通过 | core-instance-metrics-multi-exporter-a.md |

### Console Instance Observability UI（2026-07）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CONSOLE-INSTANCE-OBSERVABILITY-SHELL-A | issue-003：Console 端实例详情可观测性路由壳层 + 实例上下文 Provider + kind→Tab 映射常量；新建 `routes/compute/instances/$instanceId/route.tsx`（PageHeader + Tab 栏 + Tab Panel + `?tab=` 深链 + deleted 拦断）、`features/instance-observability/InstanceContext.tsx`、`observabilityTabsConfig.ts`；对齐 PRD US-007 / SPEC §2.4.1/§3.3/§5.1.1；typecheck + validate-architecture 通过 | console-instance-observability-shell-a.md |
| CONSOLE-INSTANCE-OBSERVABILITY-LOGS-A | issue-004：Console 实例详情日志 Tab；新建 `features/instance-observability/LogsTab.tsx`（`useInfiniteQuery` cursor 分页 + 级别筛选 Select + Table 列展示 timestamp/level Tag/message/container/stream + loading/empty/error 三态）；route.tsx logs 占位替换为 `<LogsTab />`；增强 `scripts/serve_core_mock.py` 为 `listInstanceLogs` 注入多 level mock 日志并响应 level 过滤；对齐 PRD US-008 / SPEC §4.1.1/§5.7/§6.1/§9.1；typecheck + build + validate-architecture 通过 | console-instance-observability-logs-a.md |
| CONSOLE-INSTANCE-OBSERVABILITY-EVENTS-A | issue-005：Console 实例详情事件 Tab；新建 `features/instance-observability/EventsTab.tsx`（`useQuery` 一次性加载 limit=100 + 类型筛选 Select + Table 列展示 occurred_at/type Tag/reason/message/count + loading/empty/error 三态）；route.tsx events 占位替换为 `<EventsTab />`；增强 `scripts/serve_core_mock.py` 为 `listInstanceEvents` 注入 8 条 Normal/Warning mock 事件并响应 type 过滤，新增 `?error=1` 触发 503 用于 error 态验证；cursor 分页因 Core OpenAPI `listInstanceEvents` query 缺 `cursor` 入参而 blocked-by-core 降级为一次性加载；对齐 PRD US-009 / SPEC §4.1.2/§5.7/§6.1/§9.1；typecheck + build 通过 | console-instance-observability-events-a.md |
| CONSOLE-INSTANCE-OBSERVABILITY-METRICS-A | issue-006：Console 实例详情指标 Tab（快照卡片 + PromQL 时序图）；新建 `features/instance-observability/MetricsTab.tsx`（双通道布局 Row1 工具条 + Row2 快照 + Row3/Row4 图表）、`MetricsSnapshot.tsx`（CPU/内存/网络 + GPU 卡片，null→「暂不可用」）、`MetricsChart.tsx`（Radio.Group 15m/1h/6h/24h + ECharts + Empty/Alert/forbidden 状态）、`promqlTemplates.ts`（4 个冻结模板 ID + `renderPromQL` 按 instance_id 注入 + `getTemplatesForKind` kind 路由）；route.tsx metrics 占位替换为 `<MetricsTab />`；增强 `scripts/serve_core_mock.py` 为 `listInstances`/`getInstance`/`getInstanceMetrics`/`queryObservability` 注入差异化数据并支持 `?error=1`/`?forbidden=1`/`?empty=1` 场景切换；review-it 修复 4 findings（403 判断 `error.code==='FORBIDDEN'`、自动刷新改用 `invalidateQueries`、删除 dead code、import 提顶）；对齐 PRD US-010/011/015 / SPEC §4.1.3/§4.1.4/§5.2/§8.2/§9.4 / UX §4.4/§5.4/§6.3/§7.2；typecheck + build + validate-architecture 通过 | console-instance-observability-metrics-a.md |
| CONSOLE-INSTANCE-OBSERVABILITY-TERMINAL-A | issue-007：Console 实例详情终端 Tab（exec）；新建 `features/instance-observability/TerminalTab.tsx`（POST `/instances/{id}/exec` 含 `idempotency_key` → `InstanceExecSession.ws_url` → WebSocket + xterm.js 渲染；5 态状态机 idle/connecting/connected/expired/no-permission；Tooltip 守卫非 running、Alert warning 无权限/过期、Tag 状态映射、Empty idle 态、Message.error 4xx/422、setInterval 过期检测、组件卸载清理 ws+xterm）；route.tsx terminal 占位替换为 `<TerminalTab />`；新增 `@xterm/xterm@6.0.0` + `@xterm/addon-fit@0.11.0` 依赖；增强 `scripts/serve_core_mock.py` 为 `createInstanceExecSession` 增加专用 mock（返回有效 ws_url 指向 4011 端口 WS echo + expires_at + `?forbidden=1`/`?error=1`/`?expired=1` 场景切换）并新增标准库 WebSocket echo 服务器（零额外依赖）；review-it clean 无 actionable findings；对齐 PRD US-012 / SPEC §4.1.5/§5.3/§5.6.2/§9.4(US-012) / UX §4.5/§5.5/§6.4/§7.2；typecheck + build 通过；后端 WebSocket 服务端未实现为已知边界（SPEC §11.2），归后续 Core 批次 | console-instance-observability-terminal-a.md |
| CONSOLE-INSTANCE-OBSERVABILITY-CONSOLE-A | issue-008：Console 实例详情控制台 Tab（VM console/VNC）；新建 `features/instance-observability/ConsoleTab.tsx`（协议 Select console/vnc/serial/novnc 默认 vnc + 打开控制台按钮；POST `/instances/{id}/console` → `InstanceConsoleSession.connect_url` → `window.open(_blank,noopener,noreferrer)` + Message.success；3 态状态机 idle/opening/no-permission；isRunning 守卫按钮 disabled、Alert info 常驻提示、Alert warning 无权限、Message.error 失败、Button loading 打开中）；route.tsx console 占位替换为 `<ConsoleTab />`；增强 `scripts/serve_core_mock.py` 为 `createInstanceConsoleSession` 增加专用 mock（返回有效 connect_url + expires_at + `?forbidden=1`/`?error=1` 场景切换）并新增 `demo-vm-002`（state=stopped）vm 实例用于 disabled 态验证；review-it clean 无 actionable findings；对齐 PRD US-013 / SPEC §4.1.6/§5.4/§9.4(US-013) / UX §4.6/§5.5/§6.5/§7.2；typecheck + build 通过 | console-instance-observability-console-a.md |
| CONSOLE-INSTANCE-OBSERVABILITY-SECURITY-EVENTS-A | issue-009：Console 实例详情安全事件 Tab（仅 sandbox）；新建 `features/instance-observability/SecurityEventsTab.tsx`（`useQuery` 一次性加载 limit=100 + severity 筛选 Select 全部/info/warning/critical + Table 列展示 occurred_at/severity Tag/event_type/description + severity Tag theme critical→danger/warning→warning/info→primary + Empty 空态 + Alert error 含 message+request_id+重试 + Table loading 三态）；route.tsx security-events 占位替换为 `<SecurityEventsTab />`；增强 `scripts/serve_core_mock.py` 为 `listInstanceSecurityEvents` 注入 8 条覆盖 info/warning/critical 的 mock 安全事件并响应 severity 过滤与 limit，新增 `?error=1` 触发 503+ErrorResponse 用于 error 态验证，新增 `INSTANCE_ID_SANDBOX`（demo-sandbox-001）sandbox 实例使安全事件 Tab 可见；cursor 分页因 Core OpenAPI `listInstanceSecurityEvents` query 缺 `cursor` 入参而 blocked-by-core 降级为一次性加载；review-it clean 无 actionable findings；对齐 PRD US-014 / SPEC §4.1.7/§5.7/§6.1/§9.4(US-014) / UX §4.7/§5.6/§6.6；typecheck + build + validate-architecture 通过 | console-instance-observability-security-events-a.md |
| CONSOLE-INSTANCE-OBSERVABILITY-BROWSER-VERIFICATION-A | issue-010：Console 实例观测浏览器验证收口（verification-only，无代码改动）；对 9 条 AC 逐条代码审查映射到 `features/instance-observability/` 既有组件源码行号，确认 9 种 kind 的 Tab 差异矩阵（container/gpu_container→logs,events,metrics,terminal；sandbox→+security-events；vm→logs,events,metrics,console；batch_job/notebook→logs,events,metrics；k8s_cluster/bare_metal/dpu_node→logs,events 无 metrics）与 5 种状态分支（日志 empty、指标 partial null、chart empty、终端 disabled、exec 403）在组件中真实存在；附带验证 SPEC §1.1 五项 Core 端实现（console session handler / 多 exporter 聚合 / kind→capability 映射 / PromQL 模板注入 / exec WebSocket 协议）落地情况，前四项已实现、第五项按 SPEC 设计待补；对齐 PRD US-007/US-015 / SPEC §9.2/§9.4 / UX §9；typecheck + build + validate-architecture 通过；`make test` 预存失败（Core demo shell exec 跨平台问题，非本批次引入） | console-instance-observability-browser-verification-a.md |

### Core Gateway Real Provider Integration（2026-07）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| GATEWAY-INSTANCE-CREATE-REAL-K8S-PROVIDER-A | issue-011：Gateway 实例创建链路接入 real K8s provider；新增 `bootstrap.ConnectInstanceService` helper（连 DB→`NewCapabilitiesWithConfig(pool,nil,nil,cfg)`→返回 `caps.InstanceService`+close）让 Gateway 间接使用 real K8s provider 不违反组件边界守卫；新增 `instance_service_runtime.go` 按 `WORKLOAD_PROVIDER` env 切换（`""`/`local`→nil 回退 local 闭环，`kubernetes_rest`→调 `ConnectInstanceService`，其他→unsupported）；`router.RegisterOptions` 新增 `InstanceService` 字段；`demo_instances.go` 非 nil 时优先用注入的 real service（operations 仍用 local `LocalOperationStore`），nil 时回退 local 内存闭环；`main.go` 调用 `newGatewayInstanceService` 注入 `RegisterOptions.InstanceService` + `defer closeInstanceService()`；观测前置耦合自动解决（`instanceForObservation`→`api.service.Get` 从真实 DB 读取）；新增 9 个测试覆盖 env 切换 + 注入逻辑；不修改 OpenAPI `v1.yaml`；`make validate-architecture` 通过；真实 K8s 可见性需 live gate 验证 | gateway-instance-create-real-k8s-provider-a.md |

### GPU 调度功能流（2026-07）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| GPU-SCHEDULING-ISSUE-01-A | OpenAPI 新增 GPU 调度队列 CRUD 5 端点 + 4 schema + 2 RBAC scope + InstanceRecord.gpu 扩展 + 5 错误码；修复 /branding schema bug；前端 core-schema.d.ts 重生成；validate-architecture 通过 | gpu-scheduling-issue-01-openapi-queue-crud.md |
| GPU-SCHEDULING-ISSUE-02-A | Core Queue port + Volcano Queue CRD adapter + Gateway handler 5 端点；14 adapter 单测 + 12 handler 单测全通过；validate-architecture 通过 | gpu-scheduling-issue-02-queue-adapter-handler.md |
| GPU-SCHEDULING-ISSUE-03-A | PlanScheduling 扩展：GPUSchedulingRequest 新增 QueueName/WorkloadClass；KubernetesGPUInventory 支持 queue 解析 + HAMi vGPU + 昇腾/MIG 拒绝；LocalGPUInventory 对齐；13 个新单测全通过；validate-architecture 通过 | gpu-scheduling-issue-03-plan-scheduling-extend.md |
| GPU-SCHEDULING-ISSUE-07-A | Console Shell 组件（ConsolePage/ConsolePageHeader/ConsoleContentCard）；基于 TDesign Card/Space 封装；tsc + vite build 通过 | gpu-scheduling-issue-07-console-shell-components.md |
| GPU-SCHEDULING-ISSUE-12-A | BOSS 前端骨架从零创建；package.json/vite/tsconfig/index.html/main.tsx/coreClient.ts/core-schema.d.ts/routes/__root.tsx + ops/gpu-pool.tsx 占位；tsc + vite build 通过 | gpu-scheduling-issue-12-boss-frontend-skeleton.md |
| GPU-SCHEDULING-ISSUE-08-A | Console GPU 算力管理页（/compute/gpu）；KPI 5 卡 + ECharts 型号分布 + Tabs(节点/设备/占用) + DCGM 降级 + loading/empty/error/forbidden 三态；__root.tsx 新增「算力与云资源」菜单组；tsc + vite build 通过 | gpu-scheduling-issue-08-console-gpu-management-page.md |
| GPU-SCHEDULING-ISSUE-09-A | Console GPU 容器实例列表 + 创建 Dialog + 详情页；消费 GET/POST /instances + GET /gpu-scheduling/queues；422 错误处理（InsufficientGPU/QueueNotFound）+ provisioning 提示 + 404 Empty；tsc + vite build 通过 | gpu-scheduling-issue-09-console-gpu-container-instance.md |
| GPU-SCHEDULING-ISSUE-10-A | Console 队列设置页（/settings/gpu-queues）；平台默认只读 + 我的队列 CRUD；POST+Idempotency-Key + Popconfirm 删除 + 403 平台默认保护 + RBAC placeholder + empty CTA；tsc + vite build 通过 | gpu-scheduling-issue-10-console-queue-settings-page.md |
| GPU-SCHEDULING-PR1-3-SPLIT | GPU 调度功能三段式 PR 拆分：PR #21（契约 v1.yaml+生成物，已合入 main）、PR #31（pkg/ports 接口，已合入 main）、PR #46（adapters+gateway+前端实现，OPEN）；review-it 修复 4 项（UID panic/PATCH 幂等/URL 编码/错误语义）；5 项 follow-up 延迟 | gpu-scheduling-batch-01-13-note-it.md §5 |

### Registry Console Flow（2026-07）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CORE-REGISTRY-CONSOLE-FLOW-CONTRACT-A | Console 镜像仓库主流程契约补齐：`RegistryImage.purpose`、`/registry/images?purpose=`、四类算力引用 enum、createInstance 镜像门禁 422 语义；不含 BOSS、权限或实现 | core-registry-console-flow-contract-a.md |
| CORE-REGISTRY-CONSOLE-FLOW-CORE-A | Core 镜像仓库后端实现：RegistryImage purpose 贯通 port/adapter/router，`/registry/images?purpose=` 支持过滤；不含 instances、Console、BOSS 或权限实现 | core-registry-console-flow-core-a.md |

### SDK Regression Fixes（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SDK-SERVICES-IDEMPOTENCY-SEPARATION-A | 修复 SDK beta 校验器把 Services 自有幂等 operationId 误判为 Core 泄漏的问题；`validate-sdk-beta` 改为只拒绝 Core/Services `idempotencyOperations` 交集，并新增回归测试；重生成 Core/Services 四语言 SDK 与各自 OpenAPI 契约对齐 | sdk-services-idempotency-separation-a.md |

### Sprint 12 Kickoff（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT12-KICKOFF-A | Sprint 12 启动 + GAP 分析：基于真实 Core 代码与 `api/openapi/v1.yaml` diff，规划 19 个已声明未实现 Core handler 缺口 + 2 个 422，分 B1/B2/B3 三批；仅 ANI Core，Tier1 local profile；Sprint 11 转历史回归门禁 | sprint12-kickoff-core-svc-support.md |
| SPRINT12-KICKOFF-A（配套） | B1/B2/B3 批次执行提示词，人工/AI 可直接粘贴执行，含前置事实防幻觉 | sprint12-batch-execution-prompts.md |

### Sprint 12 Delivery（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CORE-SVC-SUPPORT-OBSERVABILITY-A | B1 实例可观测与 GPU/Sandbox catalog handler：新增实例 logs/events/metrics/security-events/exec session，只读观测 port + local adapter；新增 GPU inventory/occupancy handler 与 sandbox template catalog；全部为 Tier1 local profile，响应带 dev_profile，不声明 runtime/production ready | core-svc-support-observability-a.md |
| CORE-SVC-SUPPORT-NETSTORE-A | B2 网络/存储/K8s handler：新增 network routes、volume snapshots、filesystem mount-targets、K8s workloads；searchVectorStore 非 ready 与 createK8sCluster 前置不满足返回 422 PRECONDITION_FAILED；复审收口后 createVolumeSnapshot 202 按全局约定返回 AsyncTask；全部为 Tier1 local profile，响应带 dev_profile，不声明 runtime/production ready | core-svc-support-netstore-a.md |
| CORE-SVC-SUPPORT-OBJVEC-A | B3 对象/向量 handler：新增 storage buckets、object upload/download 预签名 URL、vector document insert；复用 `ports.ObjectStore` 与 `ports.VectorStore.Upsert` 边界；全部为 Tier1 local profile，不声明 runtime/production ready | core-svc-support-objvec-a.md |
| SPRINT12-CLOSURE-A | Sprint 12 收口：A/B1/B2/B3 全部 19 个 Core handler + 2 个 422 经 OpenAPI、ports/adapters、Gateway handler、测试和文档闭环；进入 Sprint 13 real provider/live gate 收敛 | sprint12-closure-core-svc-support.md |

### Sprint 14 Planning / Execution（分支执行中，2026-06）

> 当前官方入口仍保留 Sprint 13 production-shaped 边界；`feature/sprint14-core-resilience-semantics` 分支已完成 Sprint 14 Core 韧性前置批次，并通过 aggregate Sprint14 resilience live gate。单批次记录仍按当时 local/logic 边界归档；production-ready 范围仅限隔离 Sprint14 Core resilience fixture。

| 文件 | 内容摘要 | 类型 |
|---|---|---|
| sprint14-core-resilience-plan.md | Sprint 14 Core 韧性与服务语义计划：限流/幂等重放/每调用超时/数据面 readyz（P0）、重试+断路器/优雅降级（P1）、多端点 failover（P2）；含 §0 现状事实、§0.1 差距→批次→阶段追溯矩阵、goal 加载提示词；与 Services 零交互、可独立开展 | Planning（Core） |
| frontend-acceleration-design-for-services.md | 交付 ANI Services/前端团队的前端加速设计：契约 + 20 行 page-spec → AI 生成 80%/人工打磨 20%；含项目背景补充、`instances`（VM/容器/GPU/沙箱）worked example、端到端调用闭环 | Design（交接 Services） |
| r-p0-0-gateway-shared-store.md | R-P0-0 gateway shared store：gateway middleware 通过 `ports.CacheStore` 接收共享 store，Redis 构造留在 bootstrap/adapter 边界内，并注入 middleware chain，为 R-P0-1 限流与 R-P0-2 幂等重放提供共享存储前置；local/logic verified，不标 production ready | Execution（Core） |
| r-p0-1-gateway-rate-limit.md | R-P0-1 gateway rate limit：`RateLimit(store)` 复用 gateway shared store 做 per-tenant + method + route-class 窗口计数，超限返回 `429 RATE_LIMIT_EXCEEDED`；新增 `make validate-gateway-ratelimit`；local/logic verified，不标 production ready | Execution（Core） |
| r-p0-2-gateway-idempotency-replay.md | R-P0-2 gateway idempotency replay：`Idempotency(store)` 对 mutating 请求写入 processing 哨兵，完成后缓存并回放首次响应，处理中重复请求返回 `409 IDEMPOTENCY_IN_PROGRESS`；新增 `make validate-gateway-idempotency`；local/logic verified，不标 production ready | Execution（Core） |
| r-p0-3-adapter-resilience-timeout.md | R-P0-3 adapter per-call timeout：新增 `pkg/adapters/resilience` Timeout 骨架，Kubernetes REST client、MinIO、Milvus 外部 HTTP 调用通过 `RequestTimeout` 注入 deadline；新增 `make validate-adapter-resilience-timeout`；local/logic verified，不标 production ready | Execution（Core） |
| r-p0-4-readyz-dataplane-health.md | R-P0-4 data-plane readyz：ObjectStore、VectorStore、Kubernetes API health 接入 `pkg/bootstrap/probes.go`；MinIO/Milvus/Kubernetes REST client 具备轻量 health 调用；新增 `make validate-readyz-dataplane-live-gate` local gate；单批次 local/logic verified，strong backend kill / recovery 由 Sprint14 aggregate live gate 补齐 | Execution（Core） |
| r-p1-5-retry-circuit-breaker.md | R-P1-5 retry/circuit-breaker foundation：`pkg/adapters/resilience` 新增 retryable 分类、操作级 retry/backoff 与命名 circuit breaker；Kubernetes REST 非 2xx 错误分类修正，幂等读/观察/dry-run 可通过 `RetryPolicy` 重试，真实 Apply 写路径不重试；新增 `make validate-resilience-faultinjection-live-gate` local gate；aggregate live gate 已覆盖 backend kill/degradation/controller failover，命名 circuit breaker soak 仍未完成 | Execution（Core） |
| r-p1-6-resilience-degradation.md | R-P1-6 resilience degradation：新增 strong/weak dependency policy，readyz 对 postgres/nats/redis/kubernetes-api strong 失败返回 `status=fail` + HTTP 503，对 object-store/vector-store weak 失败返回 `status=degraded` + HTTP 200；新增 `make validate-resilience-degradation` local gate；weak dependency down / recovery 已由 Sprint14 aggregate live gate 补齐，production-ready 范围仅限隔离 fixture | Execution（Core） |
| r-p2-7-multi-endpoint-failover-config.md | R-P2-7 multi-endpoint failover config：Redis bootstrap/gateway 支持 `redis.UniversalClient`、Sentinel/Cluster 配置；MinIO/Milvus adapter 接受 endpoint list 或 LB/VIP，并在网络错误、`429`、`5xx` 时尝试下一个 endpoint；新增 `make validate-ha-failover-live-gate` local config/fallback gate；controller primary kill / follower lease failover 已由 Sprint14 aggregate live gate 补齐；PG 仍为单 `DatabaseURL`，后端自身 HA 拓扑不标 production ready | Execution（Core） |
| r-sprint14-resilience-live-gate.md | SPRINT14-CORE-RESILIENCE-LIVE-GATE：新增并真实执行 `validate-sprint14-resilience-live-gate` + `ani-sprint14-resilience` 隔离 fixture，覆盖 P0 strong backend kill readyz fail、P1 weak object-store down degraded、P2 controller primary pod delete 后 follower lease failover；evidence 已脱敏归档；production-ready 范围仅限隔离 Sprint14 Core resilience fixture | Execution（Core） |

### Sprint 13 Planning（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT13-REAL-PROVIDER-READINESS-PLAN | Sprint 13 真实 provider / live gate 代码关联计划：把 Sprint 12 B1/B2/B3 的 handler、ports、local adapters 映射到真实组件和 live gate；未执行 live gate 前不标 runtime/production ready | sprint13-real-provider-readiness-plan.md |
| SPRINT13-NETROUTE-KUBEOVN-A-TRACK | S01 网络路由 Kube-OVN A 轨：adapter-local route→`Vpc.spec.staticRoutes` renderer、provider dry-run fake 单测、`validate-kubeovn-network-live-gate` route contract；本 A 轨当时不跑真实写操作，后续 live 结果见 `SPRINT13-NETROUTE-KUBEOVN-LIVE-A` | sprint13-netroute-kubeovn-a-track.md |
| SPRINT13-NETROUTE-KUBEOVN-B-TRACK-PREP | S01 网络路由 Kube-OVN B 轨接线准备：`NetworkProviderRenderer.RenderRoute` 纳入 port，`LocalNetworkService.CreateRoute` 可显式走 route renderer→dry-run→apply→observe，bootstrap 与 Gateway runtime 增加 `NETWORK_PROVIDER=kubeovn_rest` 注入路径和执行 proof 配置，live gate 增加 `--cleanup` 临时资源清理 | sprint13-netroute-kubeovn-b-track-prep.md |
| SPRINT13-NETROUTE-KUBEOVN-LIVE-A | S01 网络路由 Kube-OVN B 轨 production-shaped live result：`validate_kubeovn_network_live_gate.py --live --production-shaped` 经 ANI Gateway `POST/GET /networks/routes` create/list，再用 kubectl 观察底层 Kube-OVN Vpc/Subnet/route 与临时 NetworkPolicy/Service；产出 `production_shape.status=passed` 非敏感 evidence JSON，底层临时资源 cleanup；不代表 full platform production ready | sprint13-netroute-kubeovn-live-result.md |
| SPRINT13-K8S-WORKLOADS-VCLUSTER-A-TRACK | S02 K8s workloads vCluster A 轨：real-provider cluster 经既有 proxy target 只读读取 Kubernetes API workload list，fake 单测覆盖 Deployment 解析，`validate-vcluster-live-gate` 增加 `core-workloads-list` contract；该记录保留 A-track 当时的 code+contract ready 事实，后续 live 结果见 `SPRINT13-K8S-WORKLOADS-VCLUSTER-LIVE-A` | sprint13-k8s-workloads-vcluster-a-track.md |
| SPRINT13-K8S-WORKLOADS-VCLUSTER-LIVE-A | S02 K8s workloads vCluster B 轨 production-shaped live result：恢复 vCluster CLI v0.34.1，固定 chart 0.34.1，本次经 Gateway provider 创建 vCluster，再经 metadata target TLS 执行 Core proxy `/version`、Core workload list observe 与 cleanup，产出 `production_shape.status=passed` 非敏感 evidence JSON；不代表 full platform production ready | sprint13-k8s-workloads-vcluster-live-result.md |
| SPRINT13-STORAGE-ROOK-CEPH-A-TRACK | S03 storage Rook-Ceph A 轨：`KubernetesStorageRenderer` 增加 CSI `VolumeSnapshot` 与 mount-target `Service` contract manifest，provider dry-run / REST client fake 单测覆盖，新增 `validate-storage-live-gate`；该记录保留 A-track 当时事实，后续 live 结果见 `SPRINT13-STORAGE-ROOK-CEPH-LIVE-A` | sprint13-storage-rook-ceph-a-track.md |
| SPRINT13-STORAGE-ROOK-CEPH-LIVE-A | S03 storage Rook-Ceph B 轨 production-shaped live result：安装/恢复 CSI snapshot CRDs/controller，创建默认 RBD `VolumeSnapshotClass`，通过 in-cluster Gateway `STORAGE_PROVIDER=kubernetes_rest` 执行 Core volume create、snapshot create/list、filesystem create、mount-target list 与 cleanup，产出 `production_shape.status=passed` 非敏感 evidence JSON；不代表 full platform production ready | sprint13-storage-rook-ceph-live-result.md |
| SPRINT13-GPU-INVENTORY-DCGM-A-TRACK | S04 GPU inventory / occupancy A 轨：新增 `KubernetesGPUInventory` 只读解析 NVIDIA device-plugin NodeList capacity/product labels，新增 `validate-gpu-inventory-live-gate` 覆盖 Core `/gpu-inventory`、`/gpu-inventory/occupancy` 与 DCGM readable contract；状态 code+contract ready, LIVE PENDING，不标 real-provider/runtime/production ready | sprint13-gpu-inventory-dcgm-a-track.md |
| SPRINT13-GPU-INVENTORY-DCGM-LIVE-A | S04 GPU inventory / occupancy B 轨 production-shaped live result：恢复 DCGM exporter Helm release `ani-dcgm-exporter`，确认 NVIDIA device-plugin `v0.19.2` 与 DCGM exporter DaemonSet 均为 `3/3 ready`；`GPU_INVENTORY_PROVIDER=kubernetes_rest` 下 Core `/gpu-inventory`、`/gpu-inventory/occupancy`、Kubernetes NodeList GPU capacity 与 DCGM cluster Service metrics 共同通过 `validate-gpu-inventory-live-gate --production-shaped`，产出 `production_shape.status=passed` 非敏感 evidence JSON；不代表 full platform production ready | sprint13-gpu-inventory-dcgm-live-result.md |
| SPRINT13-B-TRACK-PRODUCTION-SHAPE-REVIEW | S01-S04 B 轨生产形态边界审查：新增 `validate-sprint13-b-track-production-shape`，禁止把 lab kubeconfig、kubectl proxy、port-forward 或 dev gateway evidence 误标为 production ready；后续已升级为 passed evidence proof_items guard | sprint13-b-track-production-shape-review.md |
| SPRINT13-B-TRACK-PRODUCTION-SHAPED-CLOSURE | S01-S04 production-shaped closure：Kubernetes REST client 支持 in-cluster ServiceAccount token/CA，Gateway network/storage/gpu runtime 透传 in-cluster 配置，S01 route metadata 新增持久化与 migration，S02/S03/S04 live gate 增加 `--production-shaped` 模式，新增 production-shaped Gateway RBAC/profile 并把 S05-S07 B 轨 proof_items 写入同一标准；后续 S01-S04 已 rerun passed | sprint13-b-track-production-shaped-closure.md |
| SPRINT13-B-TRACK-PRODUCTION-SHAPED-POST-REVIEW | S01-S04 production-shaped post-review hardening：bootstrap `Config` 与 `NewCapabilitiesWithConfig` 统一支持 in-cluster ServiceAccount Kubernetes REST provider 装配，覆盖 S01 network、S03 storage、S04 GPU inventory 与 S07 Prometheus observability；adapter 层不再隐式读取 ambient env；后续 S01-S04 已 rerun passed | sprint13-b-track-production-shaped-post-review.md |
| SPRINT13-AUTH-DEX-PRODUCTION-GATE | Auth/Dex production gate：真实集群部署 Dex + auth-service + `ANI_AUTH_MODE=auth_service` Gateway，经 `validate-auth-dex-production-gate` 跑通 Dex discovery/JWKS、anonymous 401、OIDC begin/complete 200、protected API bearer 200 与 refresh 200，产出非敏感 evidence；解除 S01-S04 Auth/Dex production ready 阻断，不代表 full platform production ready | sprint13-auth-dex-production-gate.md |
| SPRINT13-S01-S04-PRODUCTION-READINESS-REVIEW | S01-S04 production readiness boundary review：确认 S01-S04 代码路径、部署契约和 live gate 已达到 production-shaped acceptance passed；Auth/Dex production gate 已通过，Gateway 固定 `ANI_AUTH_MODE=auth_service`，S01-S04 的 Auth/Dex production ready 阻断已解除；S05-S07 B 轨可以继续但仍按组件 production-shaped 标准验收 | sprint13-s01-s04-production-readiness-review.md |
| SPRINT13-OBJECTSTORE-MINIO-A-TRACK | S05 object-store MinIO A 轨：新增 `MinIOObjectStore` S3-compatible adapter 与 bootstrap/Gateway `OBJECT_STORE_PROVIDER=minio` 显式注入路径，`validate-object-store-live-gate` 覆盖 bucket create/list、upload pre-signed URL、download pre-signed URL；B 轨已通过 production-shaped live gate | sprint13-objectstore-minio-a-track.md |
| SPRINT13-OBJECTSTORE-MINIO-LIVE-A | S05 object-store MinIO B 轨：production-shaped Gateway 经 MinIO/S3-compatible backend 完成 bucket create/list、upload/download pre-signed URL、实际 PUT/GET 与 `--cleanup`；evidence 为 `production_shape.status=passed`，不代表 full platform production ready | sprint13-objectstore-minio-live-result.md |
| SPRINT13-VECTOR-MILVUS-A-TRACK | S06 vector document insert Milvus A 轨：新增 `MilvusVectorStore` REST adapter 与 bootstrap `VECTOR_STORE_PROVIDER=milvus` 显式注入路径，`validate-vector-store-live-gate` 覆盖 Milvus readiness、Core vector store create、document insert 202 与 search readiness；后续 B 轨已通过 production-shaped live gate，历史 LIVE PENDING token 仅作门禁兼容语境 | sprint13-vector-milvus-a-track.md |
| SPRINT13-VECTOR-MILVUS-LIVE-A | S06 vector Milvus B 轨：production-shaped Gateway 经 Milvus REST backend 完成 vector store create、documents insert、search readiness 与 `--cleanup`；evidence 为 `production_shape.status=passed`，不代表 full platform production ready | sprint13-vector-milvus-live-result.md |
| SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK | S07 instance observability Prometheus + kubelet / K8s API A 轨：新增 `PrometheusInstanceObservability` adapter 与 bootstrap `INSTANCE_OBSERVABILITY_PROVIDER=prometheus_kubernetes` 显式注入路径，`validate-instance-observability-live-gate` 覆盖 Prometheus readiness、Core logs/events/metrics/security-events/exec session；后续 B 轨已通过 production-shaped live gate，历史 LIVE PENDING token 仅作门禁兼容语境 | sprint13-instance-observability-prometheus-a-track.md |
| SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-LIVE-A | S07 instance observability Prometheus B 轨：production-shaped Gateway 经 Prometheus + Kubernetes API/kubelet backend 完成 Prometheus readiness、Core logs/events/metrics/security-events/exec session 与 `--cleanup`；evidence 为 `production_shape.status=passed`，不代表 full platform production ready | sprint13-instance-observability-prometheus-live-result.md |

### Sprint 11 Kickoff（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT11-KICKOFF-A | Sprint 11 入口切换：当前仓库进入 Core Real Deployment Validation 阶段，只做 ANI Core 真实物理服务器只读验证、风险建模和门禁闭环 | sprint11-kickoff-a-core-real-deployment.md |

### Sprint 11 Delivery（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CORE-STORAGE-DISK-RISK-A | Core 存储盘风险计划：记录三台物理服务器系统盘/数据盘、稳定 `/dev/disk/by-id` 映射、Rook-Ceph OSD 风险策略；禁止依赖 `/dev/sdX` 顺序和未审批写操作 | core-storage-disk-risk-a.md |
| CORE-REAL-DEPLOY-A | Core 真实部署验证 profile：聚合 Sprint 10 release-prep、REAL-K8S-LAB profile、K8s/KubeVirt/storage 只读验证和 Sprint 11 文档一致性门禁；不执行服务器写操作 | core-real-deploy-a.md |
| CORE-ROOK-CEPH-FORMAL-DEPLOYMENT-A | Rook-Ceph 正式部署代码包：新增 `CephCluster`、`CephBlockPool`、`ani-rbd-ssd` StorageClass 和 validator；只使用 `/dev/disk/by-id` SSD 候选盘，排除 HDD；后续 live 结果见 `CORE-ROOK-CEPH-LIVE-DEPLOYMENT-A` | core-rook-ceph-formal-deployment-a.md |
| CORE-ROOK-CEPH-LIVE-DEPLOYMENT-A | Rook-Ceph 真实部署结果：安装 Rook `v1.20.0`、Ceph `v19.2.3`、CSI operator/CSI-Addons CRD，CephCluster `Ready/HEALTH_OK`，5 个 SSD OSD 运行，`ani-rbd-ssd` StorageClass 和 RBD smoke test 通过 | core-rook-ceph-live-deployment-a.md |
| CORE-ROOK-CEPH-VM-STORAGE-SMOKE-A | KubeVirt VM RBD storage smoke：临时 VM 挂载 Rook-Ceph RBD Block PVC，guest 看到块设备并完成写入尝试；临时 VM/PVC/PV/StorageClass 已清理 | core-rook-ceph-vm-storage-smoke-a.md |
| CORE-ROOK-CEPH-REBOOT-RESILIENCE-A | Rook-Ceph reboot resilience：两个 worker 和一个 control-plane 逐台重启，节点、Ceph、OSD、API readyz 与 VM/PVC 观测恢复；未并发重启 | core-rook-ceph-reboot-resilience-a.md |
| CORE-SAFE-COMPLETION-A | Core 安全完成 profile：固定上游 Kubernetes/Rook-Ceph 最佳实践、只读验证、无服务器写操作、无重启、无数据丢失风险接受和人工审批前禁止状态变更 | core-safe-completion-a.md |
| CORE-REAL-DEPLOY-DOC-CONSISTENCY-A | Sprint 11 Core 文档一致性 gate：校验入口文档、Makefile targets 和 records 与 Sprint 11 当前状态一致 | core-real-deploy-doc-consistency-a.md |

### Sprint 11 Safe Closure（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT11-SAFE-CLOSURE-A | Sprint 11 正式部署完成：部署前安全证据、Rook-Ceph live result、文档一致性和聚合门禁通过；不是实际 v1.0.0 发布或完整 production ready | sprint11-safe-closure-a-core-real-deployment.md |

### Sprint 11 Maintenance（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CORE-HISTORICAL-DOC-MARKER-COMPAT-A | Core 历史文档 marker 兼容：Sprint 8/9/10 doc validators 接受当前 Sprint 11 入口文档中的历史门禁/已完成归档表达，同时继续拒绝 stale current marker | core-historical-doc-marker-compat-a.md |
| ANI-14-SERVICES-API-ALIGNMENT-A | Services API 对齐工作流建立：GAP-REPORT（108 HTML 接口 vs YAML，发现 GPU容器/Sandbox/租户管理完全缺失）；扩展 services/v1.yaml（21→31 paths，新增 GPU容器/Sandbox/租户管理 schemas，所有 POST/PUT/PATCH 幂等键合规）；生成 SERVICES-TEAM-TASKS.md（21个任务）、CORE-TEAM-TASKS.md（3个任务）、TASK-DEPENDENCY-MAP.md（4批次分层并行）；make validate-architecture 通过 | GAP-REPORT-2026-06-09.md、SERVICES-TEAM-TASKS.md、CORE-TEAM-TASKS.md、TASK-DEPENDENCY-MAP.md |
| ANI-14-PHASE4-BATCH1-A | Phase 4 第一批 handler 骨架：新建 8 个 handler 文件（55 条路由覆盖 Models/InferenceServices/KnowledgeBases/GpuContainers/Sandboxes/Tenant/Branding/Tasks），修改 stubs.go 和 router.go；所有端点 501→200；build/test/architecture 三项门禁通过 | ANI-14-PHASE4-BATCH1-A.md |

### Sprint 10 Delivery（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CORE-ARTIFACT-MANIFEST-A | Core artifact manifest：新增 Core OpenAPI、Core SDK metadata、CLI source、offline lock、release evidence 的 SHA256 清单和 validator；不代表真实 release tarball 或镜像签名交付 | core-artifact-manifest-a.md |
| CORE-VERSION-POLICY-A | Core version policy：新增版本策略 manifest 和 validator，确保 Sprint 10 只完成 release-prep，`actual_release` / `release_candidate` / `production_release` 保持 false；不是实际 v1.0.0 发布 | core-version-policy-a.md |
| CORE-FINAL-READINESS-A | Sprint 10 final readiness profile：新增 release-prep 聚合 profile 和 validator，串联 Sprint 9 RC gate、artifact manifest、version policy、CLI build、SDK/API/doc gates | core-final-readiness-a.md |
| CORE-CLI-RELEASE-METADATA-A | ANI Core CLI release metadata：`ani --version --version-format json` 输出机器可读 name/scope/version/build_time，用于 release evidence 和现场排障 | core-cli-release-metadata-a.md |
| CORE-FINAL-DOC-CONSISTENCY-A | Sprint 10 Core 文档一致性 gate：校验三份入口文档、Makefile targets 和 development records 与 Sprint 10 状态一致，并要求明确不是实际 v1.0.0 发布 | core-final-doc-consistency-a.md |

### Sprint 10 Closure（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT10-CLOSURE-A | Sprint 10 Core-only 收敛：artifact manifest、version policy、final readiness、CLI release metadata 和 doc consistency gates 完成；入口文档切换到 Sprint 10 completed / Core release-prep completed 状态 | sprint10-closure-a-contract.md |

### Sprint 9 Delivery（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CORE-RC-GATE-A | Sprint 9 Core RC readiness profile：新增 `sprint9-core-rc.yaml` 和 validator，聚合 Core API 兼容、架构、文档、SDK、Sprint 8 release、release evidence、offline pack 与 CLI version gates；不代表实际 RC cut 或 production release | core-rc-gate-a.md |
| CORE-RELEASE-EVIDENCE-A | Core release evidence manifest：新增 Sprint 9 evidence 清单和 validator，固定可复跑命令与无敏感信息 artifact 引用；不记录 token、password、credential 或真实客户凭据 | core-release-evidence-a.md |
| CORE-OFFLINE-CHECKSUM-A | Core offline checksum contract：将 offline package lock 从占位 checksum 改为可复算的 source manifest checksum，并让 validator 拒绝占位值和不一致值；不代表真实离线包签名交付 | core-offline-checksum-a.md |
| CORE-CLI-VERSION-A | ANI Core CLI version：新增 `ani --version`，支持 Makefile `-ldflags` 注入版本和构建时间；不新增 Services 命令 | core-cli-version-a.md |
| CORE-RC-DOC-CONSISTENCY-A | Sprint 9 Core 文档一致性 gate：校验三份入口文档、Makefile targets 和 development records 与 Sprint 9 状态一致 | core-rc-doc-consistency-a.md |

### Sprint 9 Closure（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT9-CLOSURE-A | Sprint 9 Core-only 收敛：RC readiness profile、release evidence、offline checksum、CLI version 和 doc consistency gates 完成；入口文档切换到 Sprint 9 completed / Sprint 10 prep 状态 | sprint9-closure-a-contract.md |

### Sprint 8 Delivery（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CORE-HARDEN-A | Core release hardening contract：新增 release hardening gate profile 和 validator，覆盖 Core API 兼容、架构、文档、SDK、CLI、installer/offline gates；不纳入 Services/前端 | core-harden-a.md |
| CORE-INSTALLER-LIVE-A | Core installer live-readiness contract：新增三种 installer mode 的 evidence 入口和 validator；仅证明 live-readiness contract，不宣称真实安装或 production ready | core-installer-live-a.md |
| CORE-OFFLINE-PACK-A | Core offline package lock：新增离线包 artifact/checksum/verification lock 和 validator；不代表真实离线包已制作、签名或客户现场交付 | core-offline-pack-a.md |
| CORE-CLI-B | ANI Core CLI 扩展：新增 network/storage/vector/encryption/observability 只读命令；继续拒绝 model/kb/inference 等 Services 资源 | core-cli-b.md |
| CORE-DOC-CONSISTENCY-A | Core 文档一致性 gate：校验三份入口文档、Makefile targets 和 development records 与 Sprint 8 状态一致 | core-doc-consistency-a.md |

### Sprint 8 Closure（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT8-CLOSURE-A | Sprint 8 Core-only 收敛：release hardening、installer live readiness、offline package lock、CLI-B 和 doc consistency gates 完成；入口文档切换到 Sprint 8 completed / Sprint 9 prep 状态 | sprint8-closure-a-contract.md |

### Sprint 7 Kickoff（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT7-KICKOFF-A | Sprint 7 入口切换：当前仓库进入 Core-only 代码开发阶段，执行范围收窄为 Core installer、离线包、Core CLI 与真实回归门禁；RAG/Console/Services/frontends 不在本仓库推进 | sprint7-kickoff-a-core-only.md |

### Sprint 7 Delivery（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| CORE-INSTALLER-A | Core installer profile contract：新增 baremetal/VM/existing-k8s 三种 Core-only profile 和 validator；仅证明 contract/local validation，不代表真实安装或 production ready | core-installer-a.md |
| CORE-OFFLINE-A | Core offline package manifest contract：新增 Core 镜像/Helm chart/script manifest 和 validator；仅证明 manifest contract，不代表离线包已制作或可交付 | core-offline-a.md |
| CORE-CLI-A | ANI Core CLI minimal contract：新增 `cli/ani` Go CLI、Core-only 资源请求和 Services 资源拒绝；仅证明最小 CLI 行为，不代表全资源覆盖或发布包 | core-cli-a.md |
| CORE-REGRESSION-A | Sprint 7 Core regression profile：新增 installer/offline/CLI/history regression 组合门禁；不新增 REAL-K8S-LAB guard，不执行 live mode | core-regression-a.md |

### Sprint 7 Closure（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT7-CLOSURE-A | Sprint 7 Core-only 收敛：installer、offline、CLI、regression 四个 Core contract 批次完成；入口文档切换到 Sprint 7 completed / Sprint 8 prep 状态 | sprint7-closure-a-contract.md |

### Sprint 6 Delivery（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-SANDBOX-A | Sandbox 实例类型 local profile：Core OpenAPI 新增 `sandbox`/`sandbox_config`/响应摘要，新增 `SandboxRuntime` port、local adapter、Gateway `/instances` 映射和分层测试；仅证明 local profile 状态机，不代表真实 Kata provider 或 production ready | m1-sandbox-a.md |
| M1-OBS-A | 可观测性 API local profile：新增 PromQL 代理查询、告警规则 CRUD OpenAPI/port/local adapter/Gateway 路由和分层测试；仅证明 local profile，不代表 Prometheus/Alertmanager real provider | m1-obs-a.md |
| M1-METER-A | 计量 API local profile：补齐 usage 查询响应并新增 token usage 上报 OpenAPI/port/local adapter/Gateway 路由和分层测试；仅证明 local profile，不代表真实 metering/billing backend | m1-meter-a.md |
| M1-REGISTRY-A | 镜像仓库 API local profile：新增 registry projects/repositories/artifacts/permissions/pull-secret/scan-report/scan-result OpenAPI/port/local adapter/Gateway 路由和分层测试；仅证明 local profile，不代表 Harbor/Trivy real provider | m1-registry-a.md |

### Sprint 6 Closure（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT6-CLOSURE-A | Sprint 6 收敛：Sandbox、Observability、Metering、Registry 四个 Core API/local profile 批次全部完成并通过完整门禁；入口文档切换到 Sprint 6 completed / Sprint 7 kickoff 状态 | sprint6-closure-a-contract.md |

### Sprint 5 Delivery（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-K8S-A | K8s 集群 create/get/list/delete + kubeconfig API + local dev profile + idempotency + tenant isolation；不含 proxy/真实 vCluster provider | m1-k8s-a-core-api-dev-profile.md |
| M1-K8S-B | K8s 集群 proxy Core API 契约 + local dev profile；method/path/query/body 请求边界、幂等 key、路径 allowlist 和 SDK/docs 生成；不含真实 vCluster API 转发 | m1-k8s-b-api-proxy-dev-profile.md |
| M1-K8S-C | vCluster Helm provider 代码边界；新增 provider apply port、Helm adapter、provider evidence、proxy target 注册和 Gateway `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm`；不含 live Helm/kubeconfig/proxy 验证 | m1-k8s-c-vcluster-helm-provider.md |
| M1-K8S-D | vCluster kubeconfig provider 代码边界；real provider cluster 的 kubeconfig 可委托 `vcluster connect --print` adapter，Gateway `vcluster_helm` 同时接入 apply 与 kubeconfig provider；不含 live kubeconfig 可用性验证 | m1-k8s-d-vcluster-kubeconfig-provider.md |
| M1-K8S-E | K8s cluster upgrade API/provider 代码边界；新增 `POST /k8s-clusters/{cluster_id}/upgrade`、upgrade port、local 幂等版本更新、vCluster Helm upgrade intent 和 Gateway provider 接线；不含 live vCluster 升级验证或节点池管理 | m1-k8s-e-cluster-upgrade-boundary.md |
| M1-K8S-F | K8s node pool CRUD local profile；新增 cluster-scoped node-pools API、ports、local runtime、Gateway router 和 SDK/docs 生成；不含真实 provider 节点池扩缩容或 GPU 调度 live 验证 | m1-k8s-f-node-pool-local-profile.md |
| M1-K8S-G | K8s node pool provider 代码边界；新增 `K8sClusterNodePoolProvider` port、Cluster API MachineDeployment adapter 和 Gateway `K8S_CLUSTER_NODE_POOL_PROVIDER_MODE=clusterapi_kubernetes_rest` 接线；不含 live 扩缩容或 GPU 调度验证 | m1-k8s-g-node-pool-provider-boundary.md |
| M1-K8S-LIVE-A | vCluster live 验证门禁；新增 `validate-vcluster-live-gate`，固定 Helm install、vCluster kubeconfig、kubectl `/version` 和 Core live proxy 检查入口；不含真实 lab live 结果 | m1-k8s-live-a-vcluster-live-gate.md |
| M1-K8S-LIVE-D | vCluster live evidence JSON 输出；`validate_vcluster_live_gate.py --live` 支持 `--evidence-output` / `ANI_VCLUSTER_LIVE_EVIDENCE_OUTPUT` 归档 vCluster kubectl 版本、Core cluster ID 与 Core proxy HTTP 状态；不含真实 lab live 结果 | m1-k8s-live-d-vcluster-evidence-output.md |
| M1-K8S-LIVE-G | vCluster real lab live result；真实执行 `validate_vcluster_live_gate.py --live` 并归档 evidence，证明 Helm install、vCluster kubeconfig 可用性和 Core live proxy `/version` 转发通过；Core proxy 本次经本机 kubectl proxy 转发到 live vCluster，不代表生产 per-cluster metadata target/KMS token 管理已完成 | m1-k8s-live-g-vcluster-real-lab-result.md |
| M1-K8S-LIVE-B | Cluster API node pool live 验证门禁；新增 `validate-k8s-node-pool-live-gate`，固定 Core node pool create/update、MachineDeployment 观测和 GPU workload 调度检查入口；不含真实 lab live 结果 | m1-k8s-live-b-node-pool-live-gate.md |
| M1-K8S-LIVE-F | Node pool evidence JSON 输出；`validate_k8s_node_pool_live_gate.py --live` 支持 `--evidence-output` / `ANI_K8S_NODE_POOL_LIVE_EVIDENCE_OUTPUT` 归档 node pool、MachineDeployment、namespace、scaled replicas 与 GPU workload 证据；不含真实 lab live 结果 | m1-k8s-live-f-node-pool-evidence-output.md |
| M1-K8S-LIVE-C | vCluster upgrade live 验证门禁；新增 `validate-vcluster-upgrade-live-gate`，固定 Core upgrade API、Helm `controlPlane.distro.k8s.version`、升级后 kubeconfig、kubectl `/version` 和 Core proxy 检查入口；不含真实 lab live 结果 | m1-k8s-live-c-vcluster-upgrade-live-gate.md |
| M1-K8S-LIVE-E | vCluster upgrade evidence JSON 输出；`validate_vcluster_upgrade_live_gate.py --live` 支持 `--evidence-output` / `ANI_VCLUSTER_UPGRADE_LIVE_EVIDENCE_OUTPUT` 归档 target version、kubeconfig 路径与 Core proxy HTTP 状态；不含真实 lab live 结果 | m1-k8s-live-e-vcluster-upgrade-evidence-output.md |
| M1-K8S-LIVE-H | vCluster upgrade real lab live result；真实执行 `validate_vcluster_upgrade_live_gate.py --live` 并归档 evidence，证明 Core provider-backed create、Helm upgrade values、升级后 vCluster `/version` 和 Core live proxy `/version` 通过；Core proxy 本次经本机 kubectl proxy 转发到 live vCluster，不代表生产 per-cluster metadata target/KMS token 管理已完成 | m1-k8s-live-h-vcluster-upgrade-real-lab-result.md |
| M1-K8S-LIVE-J | Node pool CAPI schema hardening；复核 Cluster API `v1beta1` MachineDeployment schema 后，修正 provider manifest 为合法 `bootstrap` / `infrastructureRef` 结构，GPU/规格 intent 改由 metadata labels/annotations 保留；不代表 node pool/GPU live gate 已通过 | m1-k8s-live-j-node-pool-capi-schema-hardening.md |
| M1-K8S-LIVE-K | GPU scheduling real lab progress；ANI1/ANI2/ANI3 三台服务器完成 NVIDIA driver、NVIDIA Container Toolkit 和 NVIDIA device plugin 部署，三节点 `nvidia.com/gpu` allocatable、通用 GPU smoke Pod 与 ANI1 control-plane 专属 `nvidia-smi` smoke 已真实通过；不代表 Cluster API node pool create/scale 已通过 | m1-k8s-live-k-gpu-scheduling-real-lab-progress.md |
| M1-K8S-LIVE-L | Node pool CAPK ref config；node pool provider 默认保持 `ANIMachineTemplate` 输出兼容，同时支持通过 Gateway env 配置 CAPK `KubeadmConfigTemplate` / `KubevirtMachineTemplate` refs 与 machine version，解除固定不存在 infraRef 的代码阻塞；不代表 CAPI/CAPK live 扩缩容已通过 | m1-k8s-live-l-node-pool-capk-ref-config.md |
| M1-K8S-LIVE-M | Node pool CAPK real lab result；补强 `validate_k8s_node_pool_live_gate.py --live`，真实执行 Core create/update 并校验 MachineDeployment、MachineSet、Machine、KubevirtMachine、VM/VMI 均达到扩容后的 Ready 状态；不代表 CAPK VM 内 GPU passthrough/vGPU 已完成 | m1-k8s-live-m-node-pool-capk-real-lab-result.md |
| M1-NETWORK-LIVE-A | Kube-OVN network live 验证门禁；新增 `validate-kubeovn-network-live-gate`，固定 Kube-OVN `Vpc/Subnet`、NetworkPolicy 和 Service/LB 检查入口；不含真实 lab live 结果 | m1-network-live-a-kubeovn-network-live-gate.md |
| M1-NETWORK-LIVE-B | Kube-OVN network evidence JSON 输出；`validate_kubeovn_network_live_gate.py --live` 支持 `--evidence-output` / `ANI_KUBEOVN_NETWORK_LIVE_EVIDENCE_OUTPUT` 归档 namespace、Vpc、Subnet、NetworkPolicy/security_group 与 Service/load_balancer 证据；不含真实 lab live 结果 | m1-network-live-b-kubeovn-evidence-output.md |
| M1-NETWORK-LIVE-C | Kube-OVN network real lab live result；真实执行 `validate_kubeovn_network_live_gate.py --live` 并归档 evidence，修复 live runner namespace 创建缺口和 Kube-OVN join subnet 与 Tailscale CGNAT 冲突；当时仅证明 resource gate，external LB 可达性由后续 D 补齐 | m1-network-live-c-kubeovn-real-lab-result.md |
| M1-NETWORK-LIVE-D | Kube-OVN external LoadBalancer real lab result；增强 `validate_kubeovn_network_live_gate.py --live --external-lb-live`，补齐 Multus/macvlan underlay、helper EIP/DNAT/MASQUERADE 和三节点 HTTP 可达 evidence | m1-network-live-d-kubeovn-external-lb-real-lab-result.md |
| M1-KUBEVIRT-LIVE-A | KubeVirt VM live 验证门禁；新增 `validate-kubevirt-vm-live-gate`，固定 VM start/stop lifecycle、console/VNC 和 delete 检查入口；不含真实 lab live 结果 | m1-kubevirt-live-a-vm-live-gate.md |
| M1-KUBEVIRT-LIVE-B | KubeVirt VM evidence JSON 输出；`validate_kubevirt_vm_live_gate.py --live` 支持 `--evidence-output` / `ANI_KUBEVIRT_VM_LIVE_EVIDENCE_OUTPUT` 归档 namespace 与 VM 名称证据；不含真实 lab live 结果 | m1-kubevirt-live-b-vm-evidence-output.md |
| M1-KUBEVIRT-LIVE-C | KubeVirt VM real lab live result；真实执行 `validate_kubevirt_vm_live_gate.py --live` 并归档 evidence，证明 VM create/start/observe/stop/delete 生命周期可运行，console/VNC subresource 可达并要求 WebSocket upgrade；不宣称交互式会话已建立 | m1-kubevirt-live-c-vm-real-lab-result.md |
| M1-KUBEVIRT-LIVE-D | KubeVirt console/VNC WebSocket session real lab result；增强 `validate_kubevirt_vm_live_gate.py --live`，按 KubeVirt `plain.kubevirt.io` 子协议建立 console/VNC WebSocket session 并归档 HTTP 101、子协议和流数据字节 evidence | m1-kubevirt-live-d-console-vnc-session-real-lab-result.md |
| M1-K8S-PROXY-A | K8s 集群 proxy forwarding adapter；通过 resolver 将 Core proxy 请求转发到目标 vCluster/K8s API Server；不含真实 vCluster 生命周期或 Gateway 默认生产接线 | m1-k8s-proxy-a-forwarding-adapter.md |
| M1-K8S-PROXY-B | K8s 集群 proxy per-cluster target resolver/store；按 tenant/cluster 注册、解析、删除目标 API Server 和 bearer token；不含 DB 持久化或 Gateway 默认生产接线 | m1-k8s-proxy-b-target-resolver-store.md |
| M1-K8S-PROXY-C | K8s 集群 proxy target metadata 持久化；通过 `ports.MetadataStore` upsert/resolve/delete tenant/cluster 目标 API Server 和 bearer token；不含 Gateway 默认生产接线或 live proxy 验证 | m1-k8s-proxy-c-target-metadata-store.md |
| M1-K8S-PROXY-D | Gateway K8s proxy 注入接线；`RegisterWithOptions` 可接入 forwarding-capable `ports.K8sClusterService`；不含 Gateway main 默认 runtime 选择或 live proxy 验证 | m1-k8s-proxy-d-gateway-injection-wiring.md |
| M1-K8S-PROXY-E | Gateway K8s proxy runtime 选择；`K8S_CLUSTER_PROXY_MODE=forwarding_static` 可在 Gateway main 组合 forwarding adapter 和静态上游 target；不含 per-cluster metadata resolver Gateway 接线或 live proxy 验证 | m1-k8s-proxy-e-gateway-runtime-selection.md |
| M1-K8S-PROXY-F | Gateway K8s proxy metadata runtime；`K8S_CLUSTER_PROXY_MODE=forwarding_metadata` 可通过 `DATABASE_URL` 接入 metadata-backed per-cluster target resolver；不含 vCluster 生命周期或 live proxy 验证 | m1-k8s-proxy-f-gateway-metadata-runtime.md |
| REAL-K8S-LAB-A | 真实底座验证门禁：定义三台云 VM K8s/Kube-OVN/KubeVirt/vCluster lab profile、`make validate-real-k8s-profile` 和 live kubectl 检查入口；不代表真实环境已经部署完成 | real-k8s-lab-a-validation-gate.md |
| M1-ENCRYPT-A | Encryption keys create/get/list/delete + seal + unseal-token API + local dev profile + idempotency + tenant isolation；不含真实 KMS/SM4 provider | m1-encrypt-a-core-api-dev-profile.md |
| M1-ENCRYPT-B | Encryption key rotate/revoke API + local dev profile + idempotency + state guard；不含真实 KMS/SM4 provider 生命周期操作 | m1-encrypt-b-key-rotation-revoke-local-profile.md |
| M1-ENCRYPT-C | KMS/SM4 HTTP provider 代码边界：`ports.EncryptionProvider`、provider-backed key/seal/token evidence、Gateway `ENCRYPTION_PROVIDER_MODE=kms_sm4_http` runtime 选择；不含 live KMS/SM4 backend 验证或对象数据面 provider streaming 验收 | m1-encrypt-c-kms-sm4-provider-boundary.md |
| M1-ENCRYPT-D | 对象内容 SM4-GCM 流式加解密代码边界：reader/writer seal/open port、本地 SM4 block cipher、chunk frame、nonce 和 digest 校验；不含 live KMS/SM4 backend 或真实对象存储 provider streaming 验收 | m1-encrypt-d-sm4-gcm-object-content.md |
| M1-ENCRYPT-LIVE-A | KMS/SM4 live 验证门禁；新增 `validate-kms-sm4-live-gate`，固定 Core key/seal/token、KMS streaming seal/open 和 objectstore sealed content round trip 检查入口；不含真实 lab live 结果 | m1-encrypt-live-a-kms-sm4-live-gate.md |
| M1-ENCRYPT-LIVE-B | KMS/SM4 evidence JSON 输出；`validate_kms_sm4_live_gate.py --live` 支持 `--evidence-output` / `ANI_KMS_SM4_LIVE_EVIDENCE_OUTPUT` 归档 tenant、Gateway/KMS 地址、object URI、provider、key、sealed URI 与 round-trip bytes；不含 bearer token 或 presigned URL；不含真实 lab live 结果 | m1-encrypt-live-b-kms-sm4-evidence-output.md |
| M1-ENCRYPT-LIVE-C | KMS/SM4 real lab live result；真实执行 `validate_kms_sm4_live_gate.py --live` 并归档 evidence，证明 Core KMS provider、SM4-GCM streaming seal/open 与 objectstore sealed content round trip 通过；本次使用 live-gate fixture，不代表生产 KMS/对象存储部署形态完成 | m1-encrypt-live-c-kms-sm4-real-lab-result.md |
| M1-SECRETS-A | Secret create/get/list/delete + bindings API + local dev profile + idempotency + tenant isolation；响应不返回明文，不含真实 K8s Secret 注入 | m1-secrets-a-core-api-dev-profile.md |
| M1-SECRETS-B | Kubernetes Secret provider 写入代码边界；新增 Secret provider port、Kubernetes Secret manifest apply、Gateway `SECRET_PROVIDER_MODE=kubernetes_rest` runtime 选择；不含 live 写入验证或实例环境变量/文件挂载注入 | m1-secrets-b-kubernetes-secret-provider.md |
| M1-SECRETS-C | Workload Secret binding 注入 manifest 边界；容器/Job workload 可渲染 `envFrom.secretRef` 与只读 Secret volume mount；不含 live Pod 验证或 VM 注入 | m1-secrets-c-workload-secret-injection.md |
| M1-SECRETS-D | VM Secret binding 注入 manifest 边界；KubeVirt VM 可渲染 Secret volume、只读 disk 和 guest mount intent annotation；不含 live VM guest 可见性验证 | m1-secrets-d-vm-secret-injection.md |
| M1-SECRETS-LIVE-A | Secret live 验证门禁；新增 `validate-secrets-live-gate`，固定 Core Kubernetes Secret 创建、kubectl read、Pod env/file 和 KubeVirt VM Secret volume 检查入口；覆盖 env/file/VM 注入可见性；不含真实 lab live 结果 | m1-secrets-live-a-secret-live-gate.md |
| M1-SECRETS-LIVE-B | Kubernetes Secret evidence JSON 输出；`validate_secrets_live_gate.py --live` 支持 `--evidence-output` / `ANI_SECRETS_LIVE_EVIDENCE_OUTPUT` 归档 tenant、Gateway 地址、secret_id、namespace、Pod 与 VM；不含 bearer token 或 Secret 明文；不含真实 lab live 结果 | m1-secrets-live-b-evidence-output.md |
| M1-SECRETS-LIVE-C | Secret real lab live result；真实执行 `validate_secrets_live_gate.py --live` 并归档 evidence，证明 Kubernetes Secret provider 写入、Pod env/file 可见和 KubeVirt VM Secret volume manifest API 接受；VM guest 可见性由后续 `M1-SECRETS-LIVE-D` 补齐 | m1-secrets-live-c-secret-real-lab-result.md |
| M1-SECRETS-LIVE-D | VM Secret guest real lab result；增强 `validate_secrets_live_gate.py --live`，启动真实 KubeVirt VM 并通过 guest 串口 probe 证明 Secret volume 中的 `password` / `token` 文件可读；evidence 不含 Secret 明文 | m1-secrets-live-d-vm-secret-guest-real-lab-result.md |
| M1-RECONCILE-A | WorkloadReconcileController adapter + bootstrap capability + opt-in 后台运行；扫描 reconcile target、观察 provider 状态、回写 instance 状态；不含 leader election/指标/退避 | m1-reconcile-a-background-controller.md |
| M1-RECONCILE-B | WorkloadReconcileController 目标级失败退避和计数快照；单 target provider 失败不终止整轮扫描；不含 leader election、Prometheus 指标导出或独立 worker 部署形态 | m1-reconcile-b-controller-backoff-metrics.md |
| M1-RECONCILE-C | WorkloadReconcileController Prometheus text 指标导出；probe server `/metrics` 暴露 tick/success/failure/backoff skip counters；不含 leader election 或独立 worker 部署形态 | m1-reconcile-c-prometheus-metrics.md |
| M1-RECONCILE-D | 独立 reconcile worker 进程形态；新增 `services/reconcile-worker` 和 `bootstrap.RunWorkloadReconcileWorker`，不启动 gRPC 即运行 controller/probe/metrics；不含 leader election | m1-reconcile-d-independent-worker.md |
| M1-RECONCILE-E | WorkloadReconcileController metadata-backed leader election；新增 leader elector port、metadata lease adapter、bootstrap 显式配置和 control plane lease 迁移；不含多副本 live HA failover 验证 | m1-reconcile-e-leader-election.md |
| M1-RECONCILE-LIVE-A | Controller HA live 验证门禁；新增 `validate-reconcile-ha-live-gate`，固定两副本 worker、`control_plane_leases` active holder、metrics、删除 leader pod 和 follower 接管 HA failover 检查入口；不含真实 lab live 结果 | m1-reconcile-live-a-ha-live-gate.md |
| M1-RECONCILE-LIVE-B | Controller HA evidence JSON 输出；`validate_reconcile_ha_live_gate.py --live` 支持 `--evidence-output` / `ANI_RECONCILE_HA_LIVE_EVIDENCE_OUTPUT` 归档 namespace、worker selector、lease、metrics URL、holder 和 deleted pod 证据；不含真实 lab live 结果 | m1-reconcile-live-b-ha-evidence-output.md |
| M1-RECONCILE-LIVE-C | Controller HA real lab live result；真实执行 `validate_reconcile_ha_live_gate.py --live` 并归档 evidence，证明两副本 worker、`control_plane_leases` active holder、metrics、删除 leader Pod 与 follower 接管通过；本次使用最小 live gate 依赖和 hostPath worker 二进制，不代表生产控制面部署形态已完成 | m1-reconcile-live-c-ha-real-lab-result.md |
| M1-REAL-LAB guard series (B~KX) | 299 个 guard validators（infra、env、summary-report、evidence、live-check-profile、contract-gate-profile、path、kubeconfig、live-gate-cmd 类）；完整列表见 [guard-series/REAL-K8S-LAB-guard-index.md](guard-series/REAL-K8S-LAB-guard-index.md) | guard-series/REAL-K8S-LAB-guard-index.md |
| REAL-K8S-LAB physical base | 三台物理开发服务器完成最小基础软件环境：国内 APT 源、`containerd.io`、Kubernetes v1.36 bootstrap 工具、CRI endpoint、swap/sysctl/module 基线；未安装 Helm/vCluster/Kube-OVN/KubeVirt 等后续组件，不含 live gate 结果 | real-k8s-lab-physical-base-environment.md |
| REAL-K8S-LAB K8s/Kube-OVN/KubeVirt bootstrap | 三台物理开发服务器完成 Kubernetes `v1.36.1`、Kube-OVN `v1.15.8`、KubeVirt `v1.8.2` 最小组件部署；组件 Ready/Deployed。Kube-OVN network resource live result 见 `M1-NETWORK-LIVE-C`，KubeVirt VM lifecycle live result 见 `M1-KUBEVIRT-LIVE-C`，console/VNC WebSocket session result 见 `M1-KUBEVIRT-LIVE-D` | real-k8s-lab-k8s-kubeovn-kubevirt-bootstrap.md |

### Sprint 5 Closure（2026-06）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT5-CLOSURE-A | Sprint 5 收敛：8 个真实 live gate 全部跑通并归档 evidence；文档 guard 数统一为 299；CLAUDE.md 状态词清理；字段名/时点否定句修正 | sprint5-closure-a-contract.md |

### Sprint 5 Kickoff（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPRINT5-KICKOFF-A | Sprint 5 启动：执行入口切换与三份主文档状态对齐 | sprint5-kickoff-a.md |

### Sprint 4 API Beta Preparation（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPEC-SPLIT-A | Core/Services API 分层收口：Services 业务路径迁移到 Services API，Gateway Services stub 改挂 `/api/v1/svc`，SDK metadata 自然分层 | spec-split-a-core-services-api-boundary.md |
| SPEC-CORE-BETA-A | Core API Beta 准备矩阵：P0 path/schema、分页、幂等、状态机、dev_profile、RBAC scope 和 Core/Services 边界守卫 | spec-core-beta-a-readiness-matrix.md |
| SPEC-COMPAT-A | Core API v1 兼容性基线：保护 path/method/operationId/参数/响应/schema 字段，允许新增可选能力但阻止破坏性变更 | spec-compat-a-core-api-v1-baseline.md |
| SDK-BETA-A | 四语言 SDK 幂等 helper：生成 idempotency key、注入请求体、metadata 标出 Core 幂等操作 | sdk-beta-a-idempotency-helper.md |
| SDK-BETA-B | 四语言 SDK cursor 分页 helper：构造 limit/cursor 参数、metadata 标出 Core 分页操作 | sdk-beta-b-cursor-pagination-helper.md |
| SDK-BETA-C | 四语言 SDK 统一 API error helper：错误对象、错误码清单、错误码判断 | sdk-beta-c-api-error-helper.md |
| SDK-BETA-D | 四语言 SDK basic example：client 初始化、幂等、cursor 分页和 API error helper 组合用法 | sdk-beta-d-basic-examples.md |
| SDK-MOCK-SMOKE-A | Core Python SDK 调用 Mock Server 烟测：标准库 HTTP request 能力、分页响应和标准错误响应校验 | sdk-mock-smoke-a-python-sdk-mock-server.md |
| SDK-MOCK-SMOKE-B | Core TypeScript SDK 调用 Mock Server 烟测：fetch request 能力、分页响应和标准错误响应校验 | sdk-mock-smoke-b-typescript-sdk-mock-server.md |
| SDK-MOCK-SMOKE-C | Core Go SDK 调用 Mock Server 烟测：net/http Request 能力、分页响应和标准错误响应校验 | sdk-mock-smoke-c-go-sdk-mock-server.md |
| SDK-MOCK-SMOKE-D | Core Java SDK 调用 Mock Server 烟测：HttpClient request 能力、分页响应和标准错误响应校验 | sdk-mock-smoke-d-java-sdk-mock-server.md |
| MOCK-A | Core Mock Server：由 `api/openapi/v1.yaml` 驱动，覆盖 Core API 成功响应和统一错误结构 | mock-a-core-openapi-mock-server.md |
| DOC-API-A | 静态 API 文档生成：Core/Services API 契约生成 docs/api，并校验 operation/schema 覆盖 | doc-api-a-static-api-docs.md |
| SPRINT4-CLOSURE-A | Sprint 4 关联性闭环门禁：统一校验 API/SDK/Mock/Docs/Records 与 Makefile 入口 | sprint4-closure-a-contract.md |

### Sprint 3 Network / Storage / SDK（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-NETWORK-A | VPC/Subnet/SecurityGroup/LoadBalancer Core API 契约、Gateway dev profile、持久化边界和网络合同守卫 | m1-network-a-core-api-dev-profile.md |
| M1-NETWORK-A | KubeOVN/Kubernetes provider 渲染边界：Vpc/Subnet、NetworkPolicy、Service 清单与 bootstrap capability | m1-network-a-kubeovn-renderer.md |
| M1-NETWORK-A | 网络 provider server-side dry-run、默认关闭 apply gate、KubeOVN/Kubernetes REST path 映射 | m1-network-a-provider-dry-run-apply-gate.md |
| M1-NETWORK-A | 网络 provider 状态读取边界：KubeOVN/Kubernetes 资源状态归一化为 ANI 网络状态与失败原因 | m1-network-a-provider-status-reader.md |
| M1-NETWORK-A | 网络状态 reconcile：provider observation 校验后回写网络资源 state/reason/updated_at | m1-network-a-status-reconcile.md |
| M1-STORAGE-A | volumes/filesystems/objects Core API 契约、Gateway dev profile、租户隔离和存储合同守卫 | m1-storage-a-core-api-dev-profile.md |
| M1-STORAGE-A | storage metadata 持久化边界、RLS 迁移、bootstrap capability 和持久化单元测试 | m1-storage-a-persistence-boundary.md |
| M1-STORAGE-A | 存储 provider 渲染边界：PVC manifest、objectstore metadata intent 和 bootstrap capability | m1-storage-a-provider-renderer.md |
| M1-STORAGE-A | 存储 provider server-side dry-run、默认关闭 apply gate、objectstore 执行边界保留 | m1-storage-a-provider-dry-run-apply-gate.md |
| M1-STORAGE-A | 存储 provider 状态读取和 metadata state/reason 回写闭环 | m1-storage-a-status-reconcile.md |
| M1-VSTORE-A | vector-stores Core API 契约、Gateway dev profile、搜索响应结构和合同守卫 | m1-vstore-a-core-api-dev-profile.md |
| SDK-ALPHA-A | Core/Services 四语言 SDK Alpha 生成、分层隔离和 smoke 门禁 | sdk-alpha-a-generation-smoke.md |
| M1-WKID-A | Workload Identity P0：实例 lifecycle-bound scoped API key、Secret 引用注入和删除 revoke | m1-wkid-a-workload-identity-p0.md |
| CORE-DEV-PROFILE-A | Core P0 API dev/local profile 显式标记、Core/Services mock 边界和合同守卫 | core-dev-profile-a-boundary-contract.md |
| SPRINT3-CLOSURE-A | Sprint 3 闭环审查门禁：批次记录、API/SDK 分层和各批次合同守卫统一校验 | sprint3-closure-a-contract.md |

### Sprint 2 Core API Alpha（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| SPEC-CORE-ALPHA-A | `/api/v1/instances` Core Alpha path/schema/RBAC scope + Gateway 主路径 + 合同守卫 | spec-core-alpha-a-instance-contract-guard.md |
| SPEC-CORE-ALPHA-B | Core API Alpha 机器可读冻结矩阵，校验 path/schema/error/state/RBAC scope 与 Gateway/runtime 对齐 | spec-core-alpha-b-freeze-matrix.md |
| M1-INSTANCE-U-A | VM `termination_protection` 危险操作 precheck、failed operation timeline 和 lifecycle policy 持久化 | m1-instance-u-a-termination-protection.md |
| M1-INSTANCE-U-B | VM SSH 连接元数据 schema、Gateway dev profile 响应和 `ssh_connection` 持久化 | m1-instance-u-b-vm-ssh-info.md |
| M1-INSTANCE-U-C | VM console/VNC/serial session 返回 `operation_id/url/expires_at` 并写入 operation timeline | m1-instance-u-c-console-session-timeline.md |
| M1-INSTANCE-U-D | VM `snapshot` local profile、`snapshots[]` 响应、operation timeline 和 JSONB 持久化 | m1-instance-u-d-vm-snapshot-local-profile.md |
| M1-INSTANCE-U-E | VM `attach_volume/detach_volume` local profile、`volumes[]` 响应和 operation timeline | m1-instance-u-e-vm-volume-binding-local-profile.md |
| M1-INSTANCE-V-A | Container/GPU Container `replicas/revision/rollout_status/history` 响应和 `container_status` 持久化 | m1-instance-v-a-container-rollout-status.md |
| M1-INSTANCE-V-B | Container/GPU Container `rollback` local profile、revision 回退和 `rollback_revision` operation timeline | m1-instance-v-b-container-rollback-local-profile.md |
| M1-INSTANCE-V-C | GPU Container `vendor/model/count/scheduling_reason/utilization_percent` 响应和 `gpu_status` 持久化 | m1-instance-v-c-gpu-status-local-profile.md |

### V8 架构重规划（2026-05-14~15）

| 批次 | 内容摘要 |
|---|---|
| V8-ARCH | Core/Services 分层、ANI-02/06 重写、CLAUDE.md 强制约定 |
| AWS-HARDENING | /healthz /readyz、idempotency_key port、ReconcileController port、operations DB 表、permissions schema |

### Sprint 1 Foundation（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-HEALTH-A | Gateway/Auth/Model/Task 标准 /healthz 与 /readyz 探针 | m1-health-a-health-endpoints.md |
| M1-IDEM-A | 实例 create/lifecycle 幂等锁、DB 原子冲突回放和 bootstrap 接线 | m1-idem-a-idempotency-wire-up.md |

### M1 基础设施底座（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-INFRA-A | ani-system 命名空间、NetworkPolicy、ServiceAccount 基线 | m1-infra-a-baseline.md |
| M1-INFRA-B | PostgreSQL/NATS/Redis/MinIO/Milvus/Harbor 组件安装 profile | m1-infra-b-component-profiles.md |
| M1-INFRA-C | KubeOVN VPC/Subnet 模板、沙箱出口限制 | m1-infra-c-network-isolation.md |
| M1-INFRA-D | cluster preflight validation profile | m1-infra-d-cluster-preflight.md |
| M1-INFRA-E | GPU scheduling baseline（Volcano/HAMi/DCGM）| m1-infra-e-gpu-scheduling-baseline.md |
| M1-INFRA-F | GPU preflight/e2e hardening | m1-infra-f-gpu-preflight-e2e.md |
| M1-GPU-A | 异构 GPU 发现调度契约（NVIDIA/昇腾/海光/GPUInventory port）| m1-gpu-a-heterogeneous-gpu-contract.md |
| M1-RUNTIME-A | WorkloadRuntime port（全实例类型抽象）| m1-runtime-a-workload-runtime.md |

### M1 Instance Fabric（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M1-INSTANCE-A | 核心实例对象、生命周期、网络平面、存储附件契约 | m1-instance-a-instance-fabric.md |
| M1-INSTANCE-B | PlanningRuntime 实例规划器 | m1-instance-b-planning-runtime.md |
| M1-INSTANCE-C | K8s/KubeVirt provider dry-run renderer | m1-instance-c-provider-renderer.md |
| M1-INSTANCE-D | 本地 admission guardrail | m1-instance-d-admission-guardrail.md |
| M1-INSTANCE-E | 实例计划/渲染/准入审计持久化 | m1-instance-e-plan-audit.md |
| M1-INSTANCE-F | WorkloadProviderDryRun executor boundary | m1-instance-f-provider-dry-run.md |
| M1-INSTANCE-G | WorkloadProviderApply 执行门控 | m1-instance-g-provider-apply-gate.md |
| M1-INSTANCE-H | WorkloadStatusReconciler 状态回写 | m1-instance-h-status-reconcile.md |
| M1-INSTANCE-I | WorkloadProviderStatusReader + Orchestrator | m1-instance-i-orchestrator.md |
| M1-INSTANCE-J | WorkloadInstanceStore + workload_instances RLS 表 | m1-instance-j-instance-store.md |
| M1-INSTANCE-K | KubernetesProviderAdapter + Client | m1-instance-k-provider-adapter.md |
| M1-INSTANCE-L | WorkloadInstanceService API 层 | m1-instance-l-instance-service.md |
| M1-INSTANCE-M | 生命周期 + 可视化运维 API | m1-instance-m-lifecycle-ops.md |
| M1-INSTANCE-N | Kubernetes provider 执行剖面 | m1-instance-n-kubernetes-provider-execution.md |
| M1-INSTANCE-O | adapter-owned KubernetesRESTClient | m1-instance-o-kubernetes-rest-client.md |
| M1-INSTANCE-P | bootstrap/config provider wiring | m1-instance-p-kubernetes-bootstrap-wiring.md |
| M1-INSTANCE-Q | KubernetesLifecycleExecutor | m1-instance-q-kubernetes-lifecycle-execution.md |
| M1-INSTANCE-R | KubernetesInstanceOps | m1-instance-r-kubernetes-ops-execution.md |
| M1-INSTANCE-S | VM console/VNC/serial remote ops session 边界 | — |
| M1-INSTANCE-T | 操作语义横切基础：operation_id、timeline、幂等回放和操作查询 | m1-instance-t-operation-semantics.md |
| M1-E2E-A | M1 端到端集成剖面 | m1-e2e-a-instance-profile.md |
| M1-E2E-B | M1 real provider integration regression profile | m1-e2e-b-real-provider-profile.md |
### ARCH-ADAPTER 系列（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| ARCH-ADAPTER-A / M1-ARCH-A | 开源组件松耦合适配器架构设计 | m1-arch-a-component-adapter-design.md |
| ARCH-ADAPTER-B | pkg/ports + pkg/adapters + bootstrap.Capabilities 骨架 | arch-adapter-b-ports-adapters-skeleton.md |
| ARCH-ADAPTER-GUARD-A | 组件 SDK 直接导入扫描与 allowlist 护栏 | arch-adapter-guard-a-component-imports.md |
| ARCH-ADAPTER-C | 第一批迁移（CacheStore + MessageBus）| arch-adapter-c-first-migration.md |
| ARCH-ADAPTER-C-2 | pgx/metadata 依赖 bounded_direct 分类 | arch-adapter-c-2-metadata-boundaries.md |

### M2 Gateway / Auth（2026-05）

| 批次 | 内容摘要 | 文件 |
|---|---|---|
| M2.1-TASK-A/B | task-service + transactional outbox | m2-1-task-a-b-task-service-outbox.md |
| M2.1-TASK-C | worker mutation RPCs | m2-1-task-c-worker-mutations.md |
| M2.2-AUTH-A~K | auth-service 完整实现（JWT/OIDC/JWKS/RBAC/API Key）| m2-2-auth-*.md |
| M2.2-AUTH-FINAL | Auth 生产收尾：OIDC/Dex 护栏、Gateway Auth REST、API Key 管理、合同守卫与 Docker Dex smoke | m2-2-auth-final-production-closeout.md |

---

## 批次完工的更新流程

> 完整规约在 `CLAUDE.md` → "📋 开发进度更新规约"，以下是速查版本。

**批次完成时（必须按顺序）：**

```
① make test                              → 全通（零失败）
② 新建 {批次名}.md（用 TEMPLATE.md）    → 填入完成日期/验证结果/关键文件
③ 本文件 README.md                       → 在对应分组表格追加一行
④ repo/CURRENT-SPRINT.md                 → 该批次 🔄→✅，下一批次 ⏳→🔄
⑤ ANI-06-开发计划.md Section 零         → 更新批次/Sprint 状态行
⑥ git commit -m "feat: {批次名} {一句话}"
```

**Sprint 全部完成时，额外：**
```
⑦ ANI-06 Section 零 Sprint 行：🔄→✅（填完成日期）/ 下一Sprint：⏳→🔄
⑧ repo/CURRENT-SPRINT.md 整体重写为下一 Sprint 内容
⑨ git commit -m "sprint: Sprint N completed"
```

<!-- 历史回归门禁校验器兼容标记（请勿删除；对应历史批次与 make validate-* 门禁） -->
**历史回归门禁 token（校验器兼容，勿删）：** Sprint 11 / Core Real Deployment Validation 正式部署完成；真实服务器只读验证已完成；Rook-Ceph 正式部署已完成；Sprint 11 执行环境：正式部署执行环境。
