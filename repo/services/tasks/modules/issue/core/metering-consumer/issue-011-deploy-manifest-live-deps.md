# Issue 011: 部署清单 metering-service-live-deps.yaml

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

K8s 部署清单，部署 metering-service 并强制单副本运行。参照 `sprint13-production-auth-dex.yaml` auth-service 部署段格式。

## Scope
- Product line: core
- Code paths allowed: `repo/deploy/real-k8s-lab/`

## Acceptance Criteria
- [ ] 新增 `repo/deploy/real-k8s-lab/metering-service-live-deps.yaml`
- [ ] 包含 ServiceAccount（name: `ani-metering-service`，namespace: `ani-system`）
- [ ] 包含 Deployment（replicas: 1，强制单副本）
- [ ] 包含 Service（port: 9210 health）
- [ ] container.command: `/opt/ani/bin/metering-service`，hostPath 挂 `/opt/ani/bin`
- [ ] env: `DATABASE_URL`（secret `ani-metering-runtime` key `database_url`）、`NATS_URL`（secret key `nats_url`）、`METERING_PROMETHEUS_URL`（明文）、`METERING_COLLECTION_INTERVAL_SECONDS=60`、`HEALTH_PORT=9210`
- [ ] readinessProbe/livenessProbe: tcpSocket port health
- [ ] resources: requests cpu=100m memory=128Mi，limits cpu=1 memory=512Mi
- [ ] securityContext: runAsNonRoot, runAsUser=65532, seccompProfile RuntimeDefault, allowPrivilegeEscalation=false, capabilities drop ALL
- [ ] 参照 `sprint13-production-auth-dex.yaml` auth-service 部署段格式
- [ ] Typecheck/lint 通过

## Dependencies
- Issue #010（集成测试验证通过后部署）

## Type
core

## Priority
medium

## Labels
core

## Batch
PR-M5

## SPEC Reference
- SPEC §2.4 File Structure
- PRD FR-38
