---
type: concept
title: Gateway (Services-Layer Perspective)
description: "ANI Gateway: unified HTTP entry point for Core and Services APIs, middleware chain, route registration with nil-provider guards, services-layer route groups (/api/v1/svc/*), SSE streaming proxy, auth middleware integration for services-layer APIs."
tags: [services-layer, gateway, hertz, http, routing, middleware]
---

# Gateway (Services-Layer Perspective)

## Overview

The Gateway (`services/ani-gateway/`) serves as the single HTTP entry point for both Core and Services APIs. See [Gateway (Core perspective)](../core/gateway.md) for the full runtime construction and middleware chain. This page covers the Services-layer perspective — how services-layer routes are registered and how the Gateway integrates with services-layer backends.

## Services-Layer Route Registration

Services-layer routes are registered under `/api/v1/svc/` in `router.RegisterWithOptions()`:

| Path Group | Backend | Status |
|------------|---------|--------|
| `/api/v1/svc/knowledge-bases` | gRPC proxy → kb-service | **Implemented** — full CRUD + SSE streaming |
| `/api/v1/svc/tenant-plans` | gRPC proxy → tenant-service | **Implemented** — CRUD + BindPlanQuota |
| `/api/v1/svc/models` | — | **Stub** — returns empty `items: []` |
| `/api/v1/svc/inference-services` | — | **Stub** — returns empty `items: []` |
| `/api/v1/svc/gpu-containers` | — | **Stub** — returns empty `items: []` |
| `/api/v1/svc/sandboxes` | — | **Stub** — returns empty `items: []` |
| `/api/v1/svc/tenants/*` | — | **Stub** — members, roles, SSO, webhooks return empty JSON |

### Nil-Provider Guards

| Condition | Behavior |
|-----------|----------|
| `KBServiceClient == nil` | All KB gRPC handlers return 503 UNAVAILABLE |
| `KBSSEConfig.RagClient == nil` | SSE handler degrades to empty token stream (sources=[] + done) |
| `KBSSEConfig.VLLMStreamer == nil` | Same degradation as above |

## Auth Integration

Auth middleware runs for ALL routes under `/api/v1/` and `/api/v1/svc/` — the same JWT/API Key/sandbox token validation applies to Services-layer APIs. Services-layer routes do NOT bypass auth; they inherit the full middleware chain (see [Middleware Chain](../core/middleware.md)).

Tenant identity is extracted by the Auth middleware and forwarded to gRPC services as protobuf request fields (see [Tenant Context Propagation](../core/tenant-context-propagation.md)).

## SSE Streaming Proxy

<!-- openwiki: broken internal link [kb-service.md#sse-streaming] heading anchor "sse-streaming" does not exist in "kb-service.md". Fix the href or restore the target, then delete this comment. -->
The Gateway hosts an SSE streaming endpoint for KB queries (see [KB Service SSE](kb-service.md#sse-streaming)). The Gateway handles all SSE framing, client disconnect detection, and emits structured events (`token`, `sources`, `done`, `error`). Services-layer gRPC services do NOT directly expose SSE; the Gateway wraps gRPC streaming responses into SSE events.

## References

- [Gateway (Core perspective)](../core/gateway.md) — Runtime construction, provider wiring, main.go
- [Middleware Chain](../core/middleware.md) — Auth, RBAC, rate limit, idempotency, audit
- [Router](../core/router.md) — Full route registration with nil-provider guards
- [KB Service](kb-service.md) — gRPC service and SSE streaming
<!-- openwiki: broken internal link [tenant-service.md] file "tenant-service.md" does not exist. Fix the href or restore the target, then delete this comment. -->
- [Tenant Service](tenant-service.md) — gRPC service for tenant plan management
- Source: `services/ani-gateway/internal/router/router.go`