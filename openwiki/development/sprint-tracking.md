---
type: concept
title: Sprint Tracking
description: "Sprint tracking via repo/CURRENT-SPRINT.md: Sprint 13 (Core real provider convergence, S01-S07), Sprint 14 (Core resilience, R-P0-0 through R-P2-7), auth login module (Core #001-#003, Console #004-#005, BOSS #006), GPU scheduling 13-issue suite."
tags: [development, sprint, tracking, agile]
---

# Sprint Tracking

## Source of Truth

**File**: `repo/CURRENT-SPRINT.md` — This is the authoritative source for current sprint status. `repo/CURRENT-SPRINT.md` always reflects reality over any generated wiki page.

## Sprint Structure

| Sprint | Theme | Status |
|--------|-------|--------|
| 13 | Core real provider convergence (S01-S07) | Production-shaped live gates passed |
| 14 | Core resilience (R-P0-0 through R-P2-7) | Resilience live gate passed in isolated namespace |
| Auth Login | Core API + Console + BOSS password/OIDC login | Core #001-#003, Console #004-#005, BOSS #006 completed |
| GPU Scheduling | 13-issue suite (OpenAPI → adapters → frontends) | Issues #1-#13 completed |

## Sprint 13 Gates

| Slice | Provider | Gate Token | Status |
|-------|----------|------------|--------|
| S01 | Kube-OVN Network | SPRINT13-NETROUTE-KUBEOVN-A-TRACK | Production-shaped passed |
| S02 | vCluster Workloads | SPRINT13-K8S-WORKLOADS-VCLUSTER-A-TRACK | Production-shaped passed |
| S03 | Rook-Ceph Storage | SPRINT13-STORAGE-ROOK-CEPH-A-TRACK | Production-shaped passed |
| S04 | GPU Inventory/DCGM | SPRINT13-GPU-INVENTORY-DCGM-A-TRACK | Production-shaped passed |
| S05 | MinIO Object Store | SPRINT13-OBJECTSTORE-MINIO-A-TRACK | Production-shaped passed |
| S06 | Milvus Vector Store | SPRINT13-VECTOR-MILVUS-A-TRACK | Production-shaped passed |
| S07 | Prometheus Observability | SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK | Production-shaped passed |

## Sprint 14 Resilience Gates

| Batch | Component | Evidence |
|-------|-----------|----------|
| R-P0-0 | Gateway shared store | r-p0-0-gateway-shared-store.md |
| R-P0-1 | Gateway rate limit | r-p0-1-gateway-rate-limit.md |
| R-P0-2 | Gateway idempotency replay | r-p0-2-gateway-idempotency-replay.md |
| R-P0-3 | Adapter resilience timeout | r-p0-3-adapter-resilience-timeout.md |
| R-P0-4 | Data-plane readyz health | r-p0-4-readyz-dataplane-health.md |
| R-P1-5 | Retry/circuit-breaker | r-p1-5-retry-circuit-breaker.md |
| R-P1-6 | Resilience degradation | r-p1-6-resilience-degradation.md |
| R-P2-7 | Multi-endpoint failover | r-p2-7-multi-endpoint-failover-config.md |

## References

- [Development Records](development-records.md) — Historical batch records
- [Task Management](task-management.md) — Core/Services team tasks
- [Validation Gates](../ci-cd/validation-gates.md) — Gate catalog
- [Reconcile Controller](../core/reconcile-controller.md) — Status reconciliation
- Source: `repo/CURRENT-SPRINT.md`, `repo/development-records/`