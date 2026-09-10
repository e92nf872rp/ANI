# PLATFORM-COMPONENT-STATUS-A — 平台组件状态与组件诊断（指标/日志）只读接口

完成日期：2026-09-05（实现与实测）；2026-09-07 补充业务指标列裁剪决策与环境回归实测
对应 Sprint：Sprint 13 / Core real provider 与 live gate 收敛（并行探索批次，分支 `feat/component-status`）
验证结果：make validate-architecture passed，validate-gateway-authz passed（311 routes / 0 error），git diff --check passed；新增单测 35 个全 PASS（test-go 仅剩 2 个预存 Windows symlink 失败，与本批次无关）；K8s 测试环境（10.10.1.66）真实部署实测通过

## 实现了什么

面向 BOSS 平台健康页的组件状态只读能力：

1. **组件状态列表** `GET /api/v1/platform/components`：按静态注册表（22 组件，service/dependency/platform 三组）逐个读 K8s Deployment/StatefulSet/DaemonSet 对象聚合状态/副本/版本；service 组七服务融合 Prometheus `up` + `target_info` 身份契约的 `scrape_status`；任一组件读取失败不阻塞 200（降级 unknown + reason）；进程内 15s TTL 缓存防高轮询打爆 API server。
2. **组件诊断（行内「指标 / 日志」下钻）**：
   - `GET /platform/components/{component_name}/metrics`：资源指标快照（CPU 5m rate 核数、内存 used/limit MiB、网络收发 B/s），Prometheus cAdvisor 按 namespace + pod 前缀正则聚合，单源失败字段 null 不阻塞 200；
   - `GET /platform/components/{component_name}/logs`：Loki 日志列表（backward 倒序 + `next_cursor` 翻页，每条带 pod/container 维度）；
   - `GET /platform/components/{component_name}/logs/stream`：SSE 日志流，完全复刻实例日志流语义（`log/error/done` 三帧、`: connected` 保活、10 分钟上限、进入 SSE 前未注册组件 404 预检）。
3. **产品边界（2026-09-07 决策）**：原型表格的 P99 / 错误率 / 依赖检查三列**裁剪不做**；组件级业务指标（P99/错误率/QPS）与依赖检查不在契约范围，OpenAPI metrics description 已改为产品决策声明。Trace 按钮对应能力未建设（无 span 埋点、无 trace 后端），未纳入本批次。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | 新增 4 个端点契约（components 列表 + metrics/logs/logs-stream），`x-ani-authz` boundary=platform，`scope:observability:read` |
| `pkg/ports/component_status.go` | 新增 | `ComponentStatusService` port 与快照/组件/分组 DTO |
| `pkg/ports/platform_component_diagnostics.go` | 新增 | `PlatformComponentMetricsReader` / `PlatformComponentLogReader` port 与 DTO、`ErrNotFound`/`ErrNotConfigured` 语义 |
| `pkg/adapters/runtime/component_status_registry.go` | 新增 | 22 组件静态注册表（基线 2026-09-04 集群地面真值，宁缺勿错） |
| `pkg/adapters/runtime/kubernetes_component_status.go` | 新增 | 组件状态 real adapter（K8s REST 逐对象读取 + scrape 融合 + 15s TTL 缓存） |
| `pkg/adapters/runtime/kubernetes_platform_component_diagnostics.go` | 新增 | 诊断 real adapter（Prometheus cAdvisor 指标 + Loki 日志/流，复用 LokiLogStore helper 不新增连接） |
| `pkg/adapters/runtime/local_platform_component_status.go` / `local_platform_component_diagnostics.go` | 新增 | 确定性 local 降级 adapter（provider 未配置时 gateway 仍可启动返回 200） |
| `services/ani-gateway/component_status_runtime.go` / `component_diagnostics_runtime.go` | 新增 | env 装配：`COMPONENT_STATUS_PROVIDER`（kubernetes_rest/local）、`COMPONENT_DIAGNOSTICS_PROVIDER`（prometheus_loki/local） |
| `services/ani-gateway/internal/router/component_status.go` / `component_diagnostics.go` | 新增 | 4 个路由注册与 handler、错误码映射（400/404/503/500，`COMPONENT_DIAGNOSTICS_FAILED`） |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go`、`sdks/core/*` | 生成 | authz policy + Core SDK 四语言生成物 |
| `services/ani-gateway/internal/router/*_test.go`（3 个） | 新增 | 35 个单测覆盖注册表聚合、错误映射、SSE 语义、local 降级 |

## 完工标准达成

- [x] 契约优先：OpenAPI 先行，authz 生成与 Core SDK 四语言零漂移（validate-gateway-authz 311 routes / 0 error）
- [x] make validate-architecture / validate-doc-entrypoints / git diff --check 通过
- [x] 新增单测全 PASS（含 real adapter 指标聚合、Loki 日志、SSE 帧语义、local 降级、错误映射）
- [x] K8s 测试环境实测（镜像 `dev-20260905-compdiag`，sha256:539cdadf…）：组件状态 200（22 组件三组，real provider）、metrics 200 真实数据、logs 200（带 pod 维度 + next_cursor）、SSE 200 正序回放帧、无凭证 401、未注册组件 404；回归 `/platform/services/health`、`/platform/capacity` 通过
- [x] 2026-09-07 环境回归实测：gateway 镜像被并行会话覆盖后已恢复 `dev-20260905-compdiag`，组件诊断三接口复测通过；`/platform/services/health` 503 与 scrape_status=unknown 定位为并行会话重部署 inference-service/model-service 镜像缺失 `target_info` 埋点所致（环境漂移，与本批次无关，证据见 `kjs-study/组件状态相关文档/gateway镜像恢复与组件接口实测-20260907.md`）

## 备注

- 产品决策（2026-09-07）：原型 P99/错误率/依赖检查三列裁剪不做；metrics 契约 description 已同步为产品边界声明。Trace 能力依赖 span 埋点 + trace 后端（Tempo/Jaeger 类），当前观测栈（Prometheus+Loki）不含，属独立后续主题。
- 已知环境边界：观测 reader（服务健康 + 组件状态 scrape 融合）为整体 fail-closed——任一 service 目标 `target_info` 身份契约失败会令全部 service 组件 `scrape_status=unknown`；并行会话用不带埋点的 digest 镜像重部署服务时会触发，属契约设计而非缺陷。
- 本地仓库 `services/auth-service/Dockerfile` 的 GOWORK=off 已按 owner 决策补回（efc4e8a）；构建机分叉版与本仓库版对 GOWORK 语义要求相反，重建镜像时需注意（详见该 commit message）。
- BOSS 前端接线不在本批次范围（此前 OBS-RUNTIME-P0 已明确前端 not_applicable 口径）。
