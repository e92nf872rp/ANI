---
type: index
title: Development Records
description: "Index of repo/development-records/: sprint closure records (sprint3-sprint14), live gate evidence (m1-k8s-live-*, m1-network-live-*, m1-kubevirt-live-*, m1-encrypt-live-*, m1-secrets-live-*, m1-reconcile-live-*, sprint13-s01-s07-*, sprint14-resilience), GPU scheduling issue records (13 issues), resilience R-P records (R-P0-0-R-P2-7), auth login records, guard series index (299 guards)."
tags: [development, records, evidence, historical]
---
# Development Records

**Directory**: `repo/development-records/` · **Master Index**: `repo/development-records/README.md`

## Record Categories

### Sprint Closure Records
Sprint3 through Sprint14 closure records with contract validation evidence.

### Live Gate Evidence
Production-shaped and real-provider live gate evidence JSON and markdown summaries:
- **m1-k8s-live-***: vCluster, node pool, upgrade (A through M)
- **m1-network-live-***: Kube-OVN network (A through D)
- **m1-kubevirt-live-***: KubeVirt VM (A through D)
- **m1-encrypt-live-***: KMS/SM4 (A through C)
- **m1-secrets-live-***: Secrets (A through D)
- **m1-reconcile-live-***: HA reconcile (A through C)
- **sprint13-s01-s07-***: Sprint 13 production-shaped gates
- **sprint14-resilience**: Sprint 14 resilience live gate

### GPU Scheduling
13 issue records covering: OpenAPI queue CRUD, adapter/handler, plan/scheduling extend, lab HAMi/Volcano/DCGM, GPU smoke live gate, queue CRUD live gate, Console GPU components, BOSS GPU pool.

### Resilience R-P Records
R-P0-0 through R-P2-7: shared store, rate limit, idempotency, timeout, readyz health, retry/circuit-breaker, degradation, failover.

### Auth Login Records
Core API (#001), Console (#004-#005), BOSS (#006).

### Guard Series Index
`guard-series/REAL-K8S-LAB-guard-index.md`: 299 guards with ID convention M1-REAL-LAB-{letter}, 9 categories, append-only.

## Writing Convention

Per CLAUDE.md rules:
- **Feature batches**: write full record at `development-records/{batch-name}.md`, update README.md, update CURRENT-SPRINT.md, update ANI-06 Section Zero
- **Guard micro-batches**: append one row to guard-series index, update CURRENT-SPRINT.md guard marker only
- Evidence must be non-sensitive (no credentials, IPs, or internal endpoints)

## References
- [Sprint Tracking](sprint-tracking.md) — Current sprint status
- [Task Management](task-management.md) — Team task management
- [Validation Gates](../ci-cd/validation-gates.md) — Gate catalog