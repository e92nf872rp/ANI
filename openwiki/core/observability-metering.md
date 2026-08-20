---
type: concept
title: Observability & Metering
description: "Prometheus query proxy, Loki log store, DCGM GPU metrics, instance events, metering collection service with lifecycle event consumer and rebuilder — Sprint 14 resilience degradation integration"
tags: [core, observability, prometheus, loki, metering, dcgm, readiness]
---

# Observability & Metering

## Observability Ports

| Port | Key Methods | Default Adapter | Source |
|------|-------------|----------------|--------|
| `ObservabilityService` | `QueryPromQL(ctx, query, time) -> QueryResult` | `PrometheusObservabilityService` | `pkg/adapters/runtime/prometheus_observability_service.go` |
| `LogStore` | `QueryLogs(ctx, filter) -> LogEntries[]`, `StreamLogs(ctx, filter) -> chan LogEntry` | `LokiLogStore` | `pkg/adapters/runtime/loki_log_store.go` |
| `InstanceObservability` | `GetConsole(ctx, instanceID)`, `GetMetrics(ctx, instanceID)`, `GetLogs(ctx, instanceID)`, `GetEvents(ctx, instanceID)`, `GetTerminal(ctx, instanceID)` | `PrometheusInstanceObservability` | `pkg/adapters/runtime/prometheus_instance_observability.go` |

### Prometheus Query Proxy

`PrometheusObservabilityService` wraps a Prometheus HTTP API endpoint:

- `QueryPromQL` — pass-through PromQL query (supports vector, matrix, scalar, string result types)
- Instance-aware: when constructed via `SetInstanceLookup(lookup)`, it can resolve instance names to K8s namespaces/pods for PromQL label matching
- Dev/local: `LocalObservabilityService` returns canned data

### Loki Log Store

`LokiLogStore` wraps a Loki HTTP API endpoint:

- `QueryLogs` — log query with tenant isolation (tenant_id label filter injected automatically)
- `StreamLogs` — live tail via Loki's Tail API
- Cursor-based pagination (`NextCursor` token)

### Instance Observability

`PrometheusInstanceObservability` integrates Prometheus + Loki + DCGM for per-instance views:

- **Metrics**: CPU/memory usage (Prometheus container_cpu_usage_seconds, container_memory_working_set_bytes), GPU metrics (DCGM_FI_DEV_GPU_UTIL, DCGM_FI_DEV_MEM_COPY_UTIL, etc.)
- **Logs**: per-instance log query from Loki (filtered by pod labels)
- **Events**: K8s events for the instance (type Normal/Warning, reason, message, count)
- **Terminal**: WebSocket-based terminal proxy via K8s exec API (sandbox instances use sandbox terminal)
- **Console**: VNC console proxy for VM instances (KubeVirt VNC)

## Metering

### MeteringCollectionService (ports)

`ports.MeteringCollectionService` manages per-resource collection tickers:

- `StartCollection(ctx, spec)` — Start periodic collection for a resource (idempotent: already-running returns nil)
- `StopCollection(ctx, resourceRef)` — Stop collection ticker
- `IsCollecting(resourceRef) -> bool`
- `EverCollected(resourceRef) -> bool`

### Lifecycle Event Consumer (`services/metering-service/internal/consumer.go`)

Subscribes to `ani.events.instance.>` NATS subjects to start/stop metering collection based on instance lifecycle events:

- `handleEvent(ctx, msg)` — Routes by event type:
  - `running` → `StartCollection` with appropriate spec (GPU container → DCGM collection, VM/container → Prometheus CPU/mem collection)
  - `deleted`/`stopped` → `StopCollection`
- Deduplication via `seenSeq` map (NATS sequence-based dedup, periodic pruning)
- Subscription: `JetStream.PullSubscribe(ani.events.instance.>)` with `AckWait=30s, MaxDeliver=10, MaxInflight=16`

### Rebuilder (`services/metering-service/internal/rebuilder.go`)

On startup, scans all running instances and starts collection tickers for each:

- Uses `WithPlatformTx` (BYPASSRLS bypass) to read all tenants' instances
- Cross-tenant scan ensures no collection gap after service restart
- Idempotent: the per-resource StartCollection no-ops if already running

### EventConsumer Package (`services/metering-service/internal/eventconsumer/`)

Generic NATS JetStream pull consumer with:
- Configurable AckWait, MaxDeliver, MaxInflight
- Header-based tenant context propagation
- Error handling — handler returns nil → Ack; returns error → Nak (redeliver)
- Panic recovery — Nak on panic

### Collectors (`pkg/adapters/metering/collectors.go`)

| Collector | Data Source | Metrics Collected |
|-----------|-------------|-------------------|
| `DCGMCollector` | DCGM-Exporter (Prometheus endpoint) | GPU utilization, memory usage, temperature, power, PCIe bandwidth |
| `PrometheusCPUCollector` | Prometheus | CPU usage (cores), memory usage (GB) |
| `PrometheusGPUCollector` | Prometheus | GPU metrics via DCGM-Exporter (alternative path) |

Persistence: `MeteringCollectionService` writes to Core DB `metering_usage_records` table via `ani_metering_writer` DB role.

## Integration with /readyz

- Resilient wrappers report dependency health status
- Metering failing → degraded, not fatal
- Observability (Prometheus/Loki) failing → degraded, not fatal
- See [Resilience](resilience.md) for degradation mode semantics

## References

- [Adapters](adapters.md) — Adapter implementations
- [Gateway](gateway.md) — Observability routes
- [Resilience](resilience.md) — Degradation mode for non-critical dependencies
- [Auth Security](auth-security.md) — sandbox token for terminal/console auth
- Source: `pkg/ports/observability.go`, `pkg/ports/log_store.go`, `pkg/ports/instance_observability.go`, `pkg/ports/metering.go`, `services/metering-service/internal/`, `pkg/adapters/metering/`, `pkg/adapters/runtime/*observability*.go`, `pkg/adapters/runtime/*loki*.go`