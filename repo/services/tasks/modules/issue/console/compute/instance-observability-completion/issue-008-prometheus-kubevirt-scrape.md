# Prometheus 新增 KubeVirt virt-handler scrape 配置

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

修改 Prometheus 部署 yaml，新增 `kubevirt-virt-handler` scrape job，采集 `kubevirt_vmi_*` 指标。使用 `kubernetes_sd_configs`（role: pod，namespace: kubevirt），`relabel_configs` 过滤 `kubevirt.io=virt-handler` label 且端口为 8443。依赖 #1 合入后才能端到端验证 VM 分支。

KubeVirt virt-handler 已部署在 `kubevirt` namespace，`kubevirt_vmi_*` 指标有数据源。

## Scope
- Product line: core
- Code paths allowed: `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml`

## Acceptance Criteria

- [ ] 修改 `repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml`，在 `scrape_configs` 下新增 `job_name: kubevirt-virt-handler`
- [ ] scrape 配置使用 `kubernetes_sd_configs`（role: pod，namespace: kubevirt），`bearer_token_file` + `tls_config.insecure_skip_verify`，`metrics_path: /metrics`
- [ ] `relabel_configs` 过滤 `kubevirt.io=virt-handler` label 且端口为 8443
- [ ] 若 Prometheus ClusterRole 无 `kubevirt` namespace pods 读权限，新增对应权限（或确认已有）
- [ ] 部署后 `curl http://prometheus.ani-s07-observability:9090/api/v1/query?query=kubevirt_vmi_cpu_usage_seconds_total` 返回非空结果
- [ ] `make validate-architecture` 通过

## Dependencies
#1 — handler 传 Kind（合入后才能端到端验证 VM 分支）

## Type
core

## Priority
high

## Labels
core

## Batch
B-5

## References
- SPEC: §5.10 Prometheus scrape 配置（US-008）
- UX: N/A（后端部署改动）
- PRD: US-008 / FR-14
