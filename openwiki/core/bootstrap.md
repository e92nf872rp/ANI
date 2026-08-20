---
type: concept
title: Core Bootstrap (pkg/bootstrap)
description: "Core-layer dependency initialization: MustConnect wiring PostgreSQL, NATS, Redis; readiness/liveness probes; resilience Policy catalog; services-layer vs core-layer bootstrap split."
tags: [core, bootstrap, dependency-injection, lifecycle, probes]
---

# Core Bootstrap (`pkg/bootstrap`)

## Overview

**Directory**: `pkg/bootstrap/`

The Core bootstrap package provides dependency initialization and lifecycle management for Core-layer services (Core API, Gateway, and Core services use this bootstrap — NOT `services/pkg/bootstrap/`).

## MustConnect

`bootstrap.MustConnect(cfg)` is the primary entry point. It:

1. Connects to **PostgreSQL** (via `pgx` pool, config from `cfg.DatabaseURL`)
2. Connects to **NATS** (JetStream, config from `cfg.NATSEmbeddedURL`)
3. Connects to **Redis** (config from `cfg.RedisURL`)
4. Returns a `Deps` struct with ready-to-use connections

```go
type Deps struct {
    DB   *pgxpool.Pool
    NATS *nats.Conn
    Redis redis.UniversalClient
    Cfg  *config.Config
}
```

Panics on any connection failure (fail-fast). Closed via `deps.Close()` for graceful shutdown.

## Readiness/Liveness Probes

Probe handlers in `pkg/bootstrap/probes.go`:

| Endpoint | Behavior |
|----------|----------|
| `/readyz` | Checks DB + NATS + Redis connectivity. Returns 200 OK or `"degraded"` status when non-critical dependencies are unavailable. |
| `/livez` | Returns 200 OK if the process is alive (no dependency check). |

The readiness probe supports **degradation mode**: when circuit breakers open for non-critical dependencies, `/readyz` returns HTTP 200 with `{"status": "degraded"}` instead of 503, and sets the `X-ANI-Degraded-Capabilities` header.

## Services-Layer Bootstrap (`services/pkg/bootstrap/`)

The Services layer has its **own bootstrap** package at `services/pkg/bootstrap/` — the two are architecturally separate:

| Bootstrap | Used By | Difference |
|-----------|---------|------------|
| `pkg/bootstrap/` | Core API, Gateway, Core services (auth, task, tenant, metering, reconcile-worker) | Direct DB/NATS/Redis connections |
| `services/pkg/bootstrap/` | Services-layer services (model-service, kb-service) | Uses Core SDK client for DB access; may include gRPC client setup |

The Services bootstrap package uses gRPC unary interceptors (`loggingUnaryInterceptor`, `recoveryUnaryInterceptor`) but **no tenant unary interceptor** — tenant context is passed as protobuf message fields, not gRPC metadata.

## References

- [Gateway](gateway.md) — Uses Core bootstrap for dependency initialization
- [Middleware](middleware.md) — Middleware chain composition
- [Resilience](resilience.md) — `/readyz` degradation mode
- Source: `pkg/bootstrap/`, `services/pkg/bootstrap/`