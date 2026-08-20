---
type: concept
title: Gateway Middleware Chain
description: "ANI Gateway middleware: Auth (JWT/API Key/sandbox token/dev bypass), RBAC, Audit, RateLimit, Idempotency, RequestID — middleware composition and client abstraction"
tags: [core, gateway, middleware, auth, rbac, audit, ratelimit]
---

# Gateway Middleware Chain

## Overview

The middleware chain in `services/ani-gateway/internal/middleware/` is assembled via `middleware.Chain()` (defined in `chain.go`). Each middleware is a `app.HandlerFunc` (Hertz handler signature). The chain runs on every request before the route handler.

## Middleware Execution Order

From `chain.go:Register` (source truth):

```text
RequestID → Auth → RBAC → RateLimit → Idempotency → Audit
```

| # | Middleware | File | Purpose |
|---|-----------|------|---------|
| 1 | **RequestID** | `request_id.go` | Inject `X-Request-ID` header for tracing; propagate from incoming header if present |
| 2 | **Auth** | `auth.go` | Validate JWT Bearer token, API Key header, or sandbox short-lived token; extract tenant_id/user_id/roles/scope into context. Sandbox HMAC tokens are verified **locally** (no gRPC round-trip). |
| 3 | **RBAC** | `rbac.go` | Authorize request based on roles and scope from Auth context. **Stub**: `inferPermission` is a placeholder (`// production will call OPA`). Sandbox token scoping is enforced here. |
| 4 | **RateLimit** | `ratelimit.go` | Token bucket rate limiter per tenant; configurable from env |
| 5 | **Idempotency** | `idempotency.go` | Validate and deduplicate idempotency key (`Idempotency-Key` header) for POST/PUT/PATCH/DELETE. See [Idempotency](idempotency.md). |
| 6 | **Audit** | `audit.go` | Log operation details **asynchronously** after response. Uses a non-blocking channel send (`auditCh`) — drops on full channel. DB write is **TODO** (`// TODO: batch-write to audit_logs table via DB pool`). |

## Auth Client Abstraction

`AuthClient` interface (defined in `auth.go`):

```go
type AuthClient interface {
    ValidateToken(ctx context.Context, token string) (*AuthClaims, error)
    ValidateAPIKey(ctx context.Context, apiKey string) (*AuthClaims, error)
}
```

Default implementation: `NewAuthClientFromEnv()` creates a gRPC client to `auth-service` (reads `AUTH_SERVICE_ADDR` from env). Dev mode (`ANI_AUTH_MODE=dev`): bypasses auth, reads `X-Dev-Tenant-ID` / `X-Dev-User-ID` headers, injects `tenant-admin` role.

## Auth Flow

1. Request arrives → `isPublicPath()` check (health, branding, OIDC callback are public)
2. Check `ANI_AUTH_MODE` — if `dev`, use dev bypass
3. Try Authorization header: `Bearer <token>` → try JWT, then sandbox HMAC token, then API Key
4. Extract claims: `tenant_id`, `user_id`, `roles`, `scope`
5. Set `tenant_id` in Hertz request context (accessible via `middleware.GetTenantID(c)`)
6. Also set `TenantContext` in Go `context.Context` (for RLS-aware stores: `types.WithTenant(ctx, ...)`)

## RBAC

Role-based access control checks the user's `roles` against the required scope for the operation. Scopes are hierarchical and resource-aware (e.g., `tenant:compute:instances:*`, `platform:admin:tenants:*`).

## Idempotency

All POST/PUT/PATCH/DELETE operations with side effects MUST support `Idempotency-Key` header. See [Idempotency](idempotency.md) for the complete design.

Key details:
- **Key format**: `{prefix}_32-hex-chars` (generated via `crypto/rand.Read(16 bytes)`, **not UUIDv7**)
- **Cache TTL**: 24h default; sandbox token paths use response `expires_at` with tombstone metadata
- **Cache key shape**: `idempotency:{scope}:{tenantID}:{method}:{path}:{sha256(key)}`

## References

- [Gateway](gateway.md) — Gateway entry point and runtime construction
- [Router](router.md) — Route registration
- [Auth Security](auth-security.md) — Detailed auth subsystem documentation
- Source: `repo/services/ani-gateway/internal/middleware/`