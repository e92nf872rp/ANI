# Prometheus 新增 DCGM exporter scrape 配置

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

修改 Prometheus 部署 yaml，新增 `dcgm-exporter` scrape job，指向 `ani-dcgm-exporter.ani-system:9400`，使 `DCGM_FI_DEV_GPU_UTIL` 等 GPU 指标可被采集和查询。依赖 #1 合入后才能端到端验证 GPU 分支触发。

DCGM exporter 已部署在 `ani-system` namespace，地址 `ani-dcgm-exporter.ani-system:9400`（见 `repo/development-records/sprint13-gpu-inventory-dcgm-readiness.md`）。

## Scope
- Product line: core
- Code paths allowed: `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml`

## Acceptance Criteria

- [ ] 修改 `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml`，在 `scrape_configs` 下新增 `job_name: dcgm-exporter`
- [ ] scrape 配置使用 `static_config` 指向 `ani-dcgm-exporter.ani-system:9400`，`metrics_path: /metrics`
- [ ] 部署后 `curl http://prometheus.ani-s07-observability:9090/api/v1/query?query=DCGM_FI_DEV_GPU_UTIL` 返回非空结果
- [ ] 不修改 Prometheus ClusterRole（`static_config` 直接 HTTP 访问 Service，不需要跨 namespace RBAC）
- [ ] `make validate-architecture` 通过

## Dependencies
#1 — handler 传 Kind（合入后才能端到端验证 GPU 分支触发）

## Type
core

## Priority
high

## Labels
core

## Batch
B-2

## References
- SPEC: §5.10 Prometheus scrape 配置（US-002）
- UX: N/A（后端部署改动）
- PRD: US-002 / FR-2
