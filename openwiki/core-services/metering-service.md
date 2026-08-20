---
type: service
title: Metering Service (gRPC)
description: "Standalone gRPC service: periodic metering collection with per-resource ticker management, Prometheus collectors (CPU/mem), DCGM GPU collector, NATS lifecycle event consumer, startup rebuilder. Writes to Core DB metering_usage_records."
tags: [core-services, metering, prometheus, dcgm, collectors, lifecycle-events]
---

# Metering Service (gRPC)

## Overview

**Service**: `services/metering-service/` · **Bootstrap**: uses Core `pkg/bootstrap/` (accepted_baseline)

Three subsystems work together:

1. **MeteringCollectionService** — Per-resource collection ticker management
2. **Lifecycle Event Consumer** — Subscribes to `ani.events.instance.>` NATS subjects to start/stop collection
3. **Startup Rebuilder** — Scans all running instances on startup to rebuild collection tickers

## MeteringCollectionService

### Port Interface (`ports.MeteringCollectionService`)

| Method | Description |
|--------|-------------|
| `StartCollection(ctx, spec)` | Start periodic collection for a resource (idempotent: already-running returns nil) |
| `StopCollection(ctx, resourceRef)` | Stop collection ticker |
| `IsCollecting(resourceRef) -> bool` | Query if ticker is running |
| `EverCollected(resourceRef) -> bool` | Query if collection has ever run for this resource |

### Implementation

```go
type meteringCollectionService struct {
    mu            sync.Mutex
    tickers       map[string]*time.Ticker
    stopChs       map[string]chan struct{}
    specs         map[string]*ports.CollectionSpec
    everCollected map[string]bool
    db            *pgxpool.Pool
    logger        *slog.Logger
    collectAll    CollectAllFunc
    persistFn     persistFunc
}
```

- Each resource gets its own ticker (configurable interval via `spec.IntervalSeconds`)
- Collection goroutine: ticks → `collectAll(ctx, spec)` → `persistRecords(ctx, tenantID, records)`
- `collectAll` function reads from Prometheus/DCGM collectors (injected from `pkg/adapters/metering/`)
- `persistRecords` writes to `metering_usage_records` table via `ani_metering_writer` DB role

## Lifecycle Event Consumer

**File**: `services/metering-service/internal/consumer.go`

Subscribes to `ani.events.instance.>` NATS subjects to drive collection start/stop:

### `handleEvent(ctx, msg)`

| Event Type | Action |
|------------|--------|
| `running` | `StartCollection` with spec matching the instance kind (GPU container → DCGM collection, VM/container → Prometheus CPU/mem) |
| `stopped` | `StopCollection` |
| `deleted` | `StopCollection` |
| `failed` | `StopCollection` |

### Subscription Config

- Subjects: `ani.events.instance.>` (wildcard)
- `AckWait: 30s`, `MaxDeliver: 10`, `MaxInflight: 16`
- Deduplication via `seenSeq` map (NATS sequence tracking, periodic pruning)

## Startup Rebuilder

**File**: `services/metering-service/internal/rebuilder.go`

On startup (in `main.go` after `MustConnect`):

1. Reads all running instances across all tenants using `WithPlatformTx` (BYPASSRLS bypass)
2. For each running instance, calls `StartCollection` with the appropriate spec
3. Idempotent: `StartCollection` no-ops if collection already running

This ensures no collection gap after service restart.

## Collectors

**File**: `pkg/adapters/metering/collectors.go`

| Collector | Source | Metrics |
|-----------|--------|---------|
| `CollectCPU` | Prometheus (`container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`) | CPU cores, memory GB |
| `CollectGPU` | DCGM-Exporter Prometheus endpoint | GPU utilization, memory utilization, temperature, power, PCIe bandwidth |
| `CollectAll` | Aggregates CPU + GPU collectors | All metrics |

Integration: `main.go` calls `metering.RegisterAll(prometheusURL, prometheusHTTPClient)` to register collectors before creating the service.

## References

- [Observability & Metering](../core/observability-metering.md) — Port interfaces and collector details
- [Async Tasks](../core/async-tasks.md) — NATS lifecycle event pattern
- [Task Service](task-service.md) — Outbox publisher companion
- Source: `services/metering-service/`, `pkg/adapters/metering/collectors.go`