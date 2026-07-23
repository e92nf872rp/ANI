# Loki + Fluent Bit 推荐部署示例 yaml

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

新增 `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`，包含 Loki Deployment/Service/ConfigMap/Secret + Fluent Bit DaemonSet/ServiceAccount/ClusterRole/ClusterRoleBinding/ConfigMap。yaml 头部标注「推荐示例，非必须部署」，注释说明可替换为 ES/OpenSearch。依赖 #6 LogStore 注入机制合入后才能端到端验证日志持久化。

## Scope
- Product line: core
- Code paths allowed: `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`（新增）

## Acceptance Criteria

- [ ] 新增文件 `repo/deploy/real-k8s-lab/sprint13-instance-observability-loki-live.yaml`，包含 Namespace、Loki Deployment/Service/ConfigMap/Secret、Fluent Bit DaemonSet/ServiceAccount/ClusterRole/ClusterRoleBinding/ConfigMap
- [ ] Loki 镜像 `grafana/loki:3.6.0`，单租户模式（`auth_enabled: false`），后端 MinIO S3（`bucketnames: ani-loki-logs`，`endpoint: ani-s05-minio.ani-s05-objectstore:9000`，`s3forcepathstyle: true`）
- [ ] Loki schema v13 + tsdb store，`retention_period: 30d`
- [ ] Fluent Bit 镜像 `fluent/fluent-bit:3.2.0`，DaemonSet 部署，采集 `/var/log/pods/*`，提取 namespace/pod/container 作为 Loki label
- [ ] yaml 头部明确标注「推荐示例，非必须部署」，注释说明可替换为 ES/OpenSearch（只需新增 adapter）
- [ ] S3 凭据 Secret `ani-loki-s3-creds` 用占位值，注释说明部署前必须替换为 MinIO 真实凭据（来自 `ani-s05-minio-root`）
- [ ] yaml 头部标注前置依赖：MinIO bucket `ani-loki-logs` 需先创建、MinIO 数据卷 emptyDir 非持久化风险需运维确认
- [ ] 部署后验证命令在 yaml 注释或同级文档中给出：`kubectl wait ... /ready` 返回 200、`curl /loki/api/v1/labels` 返回 namespace/pod label、`query_range` 可查到指定 pod 日志

## Dependencies
#6 — LogStore 注入机制（合入后才能端到端验证日志持久化）

## Type
core

## Priority
medium

## Labels
core

## Batch
B-4

## References
- SPEC: §5.11 Loki + Fluent Bit 部署 yaml（US-007）
- UX: N/A（部署改动）
- PRD: US-007 / FR-11 / FR-12 / FR-13 / FR-20
