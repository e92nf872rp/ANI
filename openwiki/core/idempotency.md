---
type: concept
title: Idempotency
description: "Idempotency design: crypto/rand key generation (not UUIDv7), Gateway middleware with 24h TTL, DB-level ON CONFLICT DO NOTHING enforcement, request fingerprinting, sandbox token expiry-aware caching."
tags: [core, idempotency, middleware, sdk, replay-protection]
---

# Idempotency

## Overview

Idempotency is enforced at **two levels**:

1. **Gateway middleware** — HTTP-level replay detection: caches completed response in Redis, returns `Idempotent-Replay: true` header for duplicates.
2. **Database stores** — SQL-level deduplication: `ON CONFLICT (tenant_id, idempotency_key) DO NOTHING` guarantees at-most-once semantics even if the Gateway layer is bypassed or crashes after middleware.

Every mutating POST, PUT, PATCH, DELETE operation on the ANI platform requires an idempotency key.

## Key Generation (SDK)

```go
func NewIdempotencyKey(prefix string) (string, error) {
    if prefix == "" {
        prefix = "ani"
    }
    random := make([]byte, 16)
    if _, err := rand.Read(random); err != nil {
        return "", fmt.Errorf("generate idempotency key: %w", err)
    }
    return prefix + "_" + hex.EncodeToString(random), nil
}
```

**Format**: `{prefix}_32-hex-chars` (e.g., `ani_a1b2c3d4e5f6...`). Generated from `crypto/rand.Read(16 bytes)` — **not UUIDv7**.

Two SDKs provide the same implementation:

| SDK | File |
|-----|------|
| Core Go SDK | `sdks/core/go/anisdk/client.go` — `NewIdempotencyKey(prefix)` |
| Services Go SDK | `sdks/services/go/anisdk/client.go` — same function duplicated |

`WithIdempotencyKey(body, key)` auto-generates a key with prefix `ani` when the provided key is empty, then injects it into the JSON body under `idempotency_key`.

## Gateway Middleware

**File**: `services/ani-gateway/internal/middleware/idempotency.go`

The `Idempotency(store)` middleware runs as the 6th layer in the [Middleware Chain](middleware.md), after RateLimit and before SandboxAuth.

### Flow

```text
Request arrives
  │
  ├─ idempotencyApplies(method)?  (POST/PUT/PATCH/DELETE only)
  │   No  → c.Next()
  │   Yes → continue
  │
  ├─ Read idempotency key from:
  │     • Idempotency-Key header (first priority)
  │     • idempotency_key field in JSON body (fallback)
  │   Empty → pass through (no replay protection for this request)
  │
  ├─ Compute cache key:
  │   "idempotency:{scope}:{tenantID}:{method}:{path}:{sha256(key)}"
  │
  ├─ Compute request fingerprint (SHA-256 of method + path + query + canonical JSON body)
  │
  ├─ Read existing record from Redis
  │   ├─ Found "completed" with matching fingerprint
  │   │   → return cached response (Idempotent-Replay: true, status 200)
  │   ├─ Found "completed" with DIFFERENT fingerprint
  │   │   → 409 CONFLICT "IDEMPOTENCY_KEY_REUSED"
  │   └─ Not found → continue
  │
  ├─ SetNX cacheKey → "processing" state
  │   ├─ Already locked → competitor in flight → wait/read → replay or 409
  │   └─ Acquired → c.Next()
  │
  └─ On response write:
      Save completed record (status, body, content-type, fingerprint) to Redis
      TTL = 24h (default)
      For /sandbox/tokens paths: TTL = response.expires_at, "expired" tombstone via :metadata key
```

### Cache Scheme

| Key | Value | TTL |
|-----|-------|-----|
| `idempotency:{scope}:{tenantID}:{method}:{path}:{sha256(key)}` | `{"state":"completed", "fingerprint":"...", "status_code":200, ...}` | 24h (or sandbox token expiry) |
| Same key + `:metadata` (sandbox only) | `{"state":"expired", "fingerprint":"...", "expires_at":"..."}` | 24h (tombstone) |

### Conflict Detection

| Scenario | HTTP Response |
|----------|---------------|
| Same key, same request → replay | `200 OK` + `Idempotent-Replay: true` header |
| Same key, different request | `409 CONFLICT` + `IDEMPOTENCY_KEY_REUSED` error code |
| Same key, still processing | `409 CONFLICT` + `IDEMPOTENCY_IN_PROGRESS` |
| Store unavailable | `503 SERVICE UNAVAILABLE` + `IDEMPOTENCY_UNAVAILABLE` |

### Source Files

- `repo/services/ani-gateway/internal/middleware/idempotency.go` — Implementation
- `repo/services/ani-gateway/internal/middleware/idempotency_test.go` — Comprehensive tests

## Database-Level Deduplication

Several store implementations enforce idempotency at the SQL layer:

```sql
INSERT INTO async_tasks (..., idempotency_key, ...)
VALUES (..., $1, ...)
ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
RETURNING *;
```

This is present in:

| Store | Table | UNIQUE Constraint |
|-------|-------|-------------------|
| `AsyncTaskRepo` | `async_tasks` | `(tenant_id, idempotency_key)` |
| Operation store | `operations` | `(tenant_id, idempotency_key)` |
| Storage resource store | Storage resource tables | `(tenant_id, idempotency_key)` |
| Vector store resource store | Vector resource tables | `(tenant_id, idempotency_key)` |
| `LocalGpuSchedulingQueueStore` | In-memory | `map[tenantID]map[key]record` |

## Idempotency in gRPC Services

Unlike the Gateway HTTP path, gRPC services (tenant-service, task-service, kb-service) **do not have an idempotency interceptor**. Instead, idempotency key is part of the request message:

```protobuf
message BindPlanQuotaRequest {
    string tenant_id = 1;
    string plan_id = 2;
    string idempotency_key = 3; // optional; if empty, service treats as first attempt
}
```

The Gateway's Idempotency middleware handles deduplication before forwarding to gRPC, so the gRPC service receives at most one execution per key per TTL window.

## References

- [Middleware Chain](middleware.md) — Chain position and composition
- [Gateway](../services-layer/gateway.md) — Gateway entry point
- [Async Tasks](async-tasks.md) — `async_tasks` table UNIQUE constraint on idempotency key
- Source: `repo/services/ani-gateway/internal/middleware/idempotency.go`
- Source: `repo/sdks/core/go/anisdk/client.go`