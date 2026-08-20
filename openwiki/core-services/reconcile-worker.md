---
type: service
title: Reconcile Worker (Background Service)
description: "Standalone background worker running the reconcile controller loop: list targets, query provider status, reconcile stored state, with HA leader election and backoff management"
tags: [core-services, reconcile, worker, controller, ha, leader-election]
---

# Reconcile Worker

## Overview

**Service**: `services/reconcile-worker/` · **Bootstrap**: uses Core `pkg/bootstrap/` (accepted_baseline) · **HealthPort**: 9205

Standalone background worker that runs the `ports.WorkloadReconcileController` loop:

- Lists reconcile targets via `ports.ReconcileTargetLister`
- Queries provider status via `ports.WorkloadProviderStatusReader`
- Reconciles with stored state via `ports.WorkloadStatusReconciler`
- HA via `MetadataReconcileLeaderElector` (Postgres-backed lease)
- Exponential backoff per instance on failure

## Entrypoint

```go
func main() {
    cfg := config.Load()
    deps := bootstrap.MustConnect(cfg)
    defer deps.Close()

    bootstrap.RunWorkloadReconcileWorker(deps)
}
```

Config structure (`internal/config/config.go`):

| Field | Env | Default |
|-------|-----|---------|
| `DatabaseURL` | `DATABASE_URL` | `postgres://ani_app_user:ani_dev_password@127.0.0.1:5432/ani?sslmode=disable` |
| `NATSURL` | `NATS_URL` | `nats://127.0.0.1:4222` |
| `RedisURL` | `REDIS_URL` | `redis://:ani_dev_password@127.0.0.1:6379/0` |
| `HealthPort` | `HEALTH_PORT` | 9205 |
| `ServiceName` | (hardcoded) | `reconcile-worker` |
| `ReconcileControllerEnabled` | (hardcoded) | `true` |

## Reconcile Loop

`bootstrap.RunWorkloadReconcileWorker(deps)`:

```text
loop:
  if !amLeader → sleep 5s, continue
  targets := controller.ListTargets(ctx)
  for each target:
    if target in backoff map AND backoff[target] > now → skip
    providerStatus := reader.Observe(ctx, target)
    reconciler.Reconcile(ctx, record, providerStatus)
    if error → backoff[target] = now + exponentialBackoff(attempt)
    else → delete backoff[target], reset attempt counter
  sleep 1s (tuneable via config)
```

Backoff formula: `min(baseBackoff * 2^attempt, maxBackoff)` where `baseBackoff = 5s`, `maxBackoff = 300s`.

## References

- [Reconcile Controller](../core/reconcile-controller.md) — Controller interface, leader election, backoff
- Source: `services/reconcile-worker/`, `pkg/bootstrap/reconcile_worker.go`