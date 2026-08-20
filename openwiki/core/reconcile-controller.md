---
type: concept
title: Reconcile Controller & HA Leader Election
description: "Status reconciliation pipeline: provider status reader, status reconciler, reconcile controller with backoff. HA leader election via Postgres-backed lease."
tags: [core, reconcile, controller, leader-election, ha, backoff]
---

# Reconcile Controller & HA Leader Election

## Pipeline

```
┌──────────────┐    ┌──────────────┐    ┌─────────────────┐
│ Status       │    │ Reconcile    │    │ Instance         │
│ Reader       │───→│ Controller   │───→│ Store            │
│ (Provider)   │    │ (loop+backoff)│    │ (reconciled)     │
└──────────────┘    └──────────────┘    └─────────────────┘
       │
       ▼
┌──────────────┐
│ Reconcile    │
│ Status       │
│ (Provider)   │
└──────────────┘
```

## Port Interfaces

| Port | Responsibility | Key Method | 
|------|---------------|------------|
| `WorkloadProviderStatusReader` | Query provider-side instance status | `Observe(ctx, request) -> Observation` |
| `WorkloadStatusReconciler` | Compare + reconcile stored vs observed state | `Reconcile(ctx, record, observation) -> reconciledRecord` |
| `WorkloadReconcileController` | Scheduling loop with backoff | `ReconcileSingle(ctx, instanceID, tenantID) -> result` |
| `WorkloadReconcileControllerMetics` | Controller health metrics (queue depth, reconcile duration, errors) | `Metrics() -> ControllerMetrics` |

## Reconcile Controller (`LocalWorkloadReconcileController`)

### Reconcile Loop Structure

The `LocalWorkloadReconcileController` runs a continuous loop:

1. **List targets**: Call `ReconcileTargetLister.ListTargets(ctx, limit)` to query `MetadataInstanceStore` for all non-terminal instances (state != `deleted` and state != `failed`).
2. **For each target**:
   a. **Check backoff**: Skip if instance is in backoff map and backoff has not elapsed.
   b. **Observe status**: Call `WorkloadProviderStatusReader.Observe(ctx, request)` to get current provider-side status.
   c. **Reconcile**: Call `WorkloadStatusReconciler.Reconcile(ctx, storedRecord, observation)` to compute reconciled state.
   d. **Upsert**: Call `WorkloadInstanceStore.UpsertStatus(ctx, tenantID, instanceID, reconciledRecord)` to persist reconciled state.
3. **HA gate**: Only the leader runs this loop. Non-leaders sleep until next check interval.

### Provider Status Mapping (`LocalStatusReconciler`)

The `LocalStatusReconciler.Reconcile` method maps provider-observed phases to canonical `WorkloadState`:

| Provider Phase/Observation | Mapped WorkloadState | Reason |
|---------------------------|---------------------|--------|
| `"ready"`, `"running"` | `WorkloadStateRunning` | — |
| `"pending"`, `"provisioning"` | `WorkloadStatePending` | — |
| `"starting"` | `WorkloadStateStarting` | — |
| `"stopping"` | `WorkloadStateStopping` | — |
| `"stopped"` | `WorkloadStateStopped` | — |
| `"failed"`, `"error"` | `WorkloadStateFailed` | `ProviderStatusFailed` |
| `"deleting"` | `WorkloadStateDeleting` | — |
| Provider resource not found (Observe returns `ErrNotFound`) | `WorkloadStateFailed` | `ProviderResourceLost` |
| Empty/unexpected phase | `WorkloadStateFailed` | `InvalidProviderStatus` |

**ProviderMissing detection**: When `Observe` returns an error indicating the K8s resource was deleted out-of-band (e.g., `ErrNotFound` from `KubernetesProviderAdapter`), the reconciler transitions the instance to `Failed` with `ProviderResourceLost` reason. This is tested in `reconcile_controller_test.go:TestReconcile_ProviderMissing_TransitionsToFailed`.

### Backoff Strategy

| Condition | Backoff Behavior |
|-----------|-----------------|
| Provider observed state matches stored state → success | Linear scanning (no backoff) |
| Provider observed state differs → reconcile needed | Normal processing |
| Provider returns error → transient failure | Exponential backoff: `min(base * 2^{attempt}, maxBackoff)` |
| Provider unavailable → circuit breaker open | Skip until breaker closes (see [Resilience](resilience.md)) |

Backoff tracking: `controller.backoff map[string]time.Time` — instanceID → next allowed reconcile time. Cleared on successful reconcile.

### Reconcile Target Listing

`port.ReconcileTargetLister` — returns all instances that need reconciliation. Current implementation uses `MetadataInstanceStore` to list all non-terminal instances.

## HA Leader Election

### Postgres-backed Leader Elector (`MetadataReconcileLeaderElector`)

| Config Field | Environment Variable | Default |
|-------------|---------------------|---------|
| `LeaseName` | `RECONCILE_LEADER_LEASE_NAME` | `ani-reconcile-leader` |
| `LeaseIdentity` | `RECONCILE_LEADER_IDENTITY` | `hostname-{pid}` |
| `LeaseTTL` | `RECONCILE_LEADER_TTL_SECONDS` | 15s |
| `RenewInterval` | `RECONCILE_LEADER_RENEW_INTERVAL_SECONDS` | 5s |

Lease table: `control_plane_leases` (part of [migrations 024](../deployment/database-migrations.md)).

### Lease Algorithm

```text
Start():
  loop:
    TryAcquire():
      INSERT INTO control_plane_leases (name, identity, acquired_at, expires_at)
      VALUES ($name, $identity, NOW(), NOW() + $ttl)
      ON CONFLICT (name) DO UPDATE
        SET identity = $identity, acquired_at = NOW(), expires_at = NOW() + $ttl
        WHERE control_plane_leases.expires_at < NOW()
      IF rows_affected > 0 → leader; ELSE → follower

    Renew():
      UPDATE control_plane_leases SET expires_at = NOW() + $ttl
      WHERE name = $name AND identity = $identity
      IF rows_affected = 0 → lease lost → become follower

    Sleep($renewInterval)

Release():
  DELETE FROM control_plane_leases WHERE name = $name AND identity = $identity
```

When `WorkloadReconcileLeaderElectionEnabled=true`, the controller is wrapped in `LeaderElectingWorkloadReconcileController` which only runs the inner controller when this instance holds the lease.

## References

- [Resilience](resilience.md) — Circuit breaker integration with reconcile loop
- [Instances](instances.md) — Instance state machine (the state being reconciled)
- [Gateway](gateway.md) — Reconcile worker is a standalone background service
- [Task Service](../core-services/task-service.md) — Uses reconciler pattern for async task completion
- Source: `repo/pkg/adapters/runtime/reconcile_controller.go`, `repo/pkg/adapters/runtime/status_reconciler.go`, `repo/pkg/adapters/runtime/reconcile_leader_election.go`, `repo/pkg/adapters/runtime/provider_status_reader.go`, `repo/services/reconcile-worker/`