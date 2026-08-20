---
type: concept
title: Auth, Security & Secrets
description: "Auth middleware (JWT/API Key/sandbox token/dev bypass), RBAC, audit, secrets service (create/bind/unbind), KMS/SM4 encryption provider, workload identity, and sandbox token subsystem"
tags: [core, auth, security, rbac, secrets, kms, sm4, sandbox-token]
---

# Auth, Security & Secrets

## Auth Middleware

See [Middleware](middleware.md) for the full middleware chain description. This page covers the auth-specific components.

### Authentication Methods

| Method | Mechanism | When Used |
|--------|-----------|-----------|
| **JWT Bearer Token** | RSA-signed JWT (1h expiry, issued by auth-service) | Console users, CLI, SDK clients |
| **API Key** | Long-lived key in `X-API-Key` header | Automated scripts, CI/CD, service-to-service |
| **Sandbox Token** | HMAC-SHA256 short-lived token (scoped to sandbox instance) | Sandbox instances accessing Gateway |
| **Dev Bypass** | `X-Dev-Tenant-ID`/`X-Dev-User-ID` headers (when `ANI_AUTH_MODE=dev`) | Local development without auth-service |

### Auth Client Abstraction

```go
type AuthClient interface {
    ValidateToken(ctx context.Context, token string) (*AuthClaims, error)
    ValidateAPIKey(ctx context.Context, apiKey string) (*AuthClaims, error)
}
```

Default: gRPC client to auth-service (reads `AUTH_SERVICE_ADDR` from env). Dev: reads dev headers directly.

### Token Priority Order

1. `Authorization: Bearer <token>` → Try JWT → Try sandbox token → Try API Key
2. If all fail → 401 UNAUTHORIZED

## Sandbox Token Subsystem (`pkg/security/sandboxtoken/`)

HMAC-based short-lived tokens for sandbox instances, verified locally without auth-service roundtrip.

### Token Format

```
ani.sbx.{base64-encoded-payload}.{base64-encoded-signature}
```

Prefix `ani.sbx.` distinguishes sandbox tokens from JWT Bearer tokens.

### Key Operations

- **`LooksLike(token string) bool`** — Checks `ani.sbx.` prefix; used by middleware to route to the right parser
- **`Issue(lifetime time.Duration, instanceID string) (token string, expiry time.Time, err error)`** — Generates HMAC-SHA256 signed token with embedded instanceID, tenantID, and expiry
- **`Parse(token string, key []byte, now time.Time) (claims *SandboxClaims, err error)`** — Verifies HMAC, checks expiry, extracts claims
- **`SigningKey() []byte`** — Reads signing key from `SANDBOX_SIGNING_KEY` env var (32+ bytes)

### Security Contract

- **Scoped**: Each token is bound to a specific sandbox instance (instanceID embedded in claims)
- **Short-lived**: TTL is limited (configurable, typically minutes); `ErrExpiredToken` after expiry
- **Local verification**: No auth-service dependency — verified in Gateway middleware via HMAC
- **No enumeration**: HMAC signature prevents token forgery without the signing key

### Error Returns

| Error | Condition |
|-------|-----------|
| `ErrExpiredToken` | Token expiry passed |
| `ErrInvalidToken` | HMAC verification failed, malformed payload, or unknown prefix |
| `ErrInvalidSigningKey` | Signing key is empty or too short |

### Source Files

- `repo/pkg/security/sandboxtoken/token.go` — Core implementation
- `repo/pkg/security/sandboxtoken/token_test.go` — Comprehensive tests

## RBAC

Role-based access control middleware (`middleware/rbac.go`):

| Role Level | Scope Prefix | Example |
|------------|-------------|---------|
| **Tenant Admin** | `tenant:*` | `tenant:compute:instances:*` |
| **Tenant User** | `tenant:...:read` (read-only subset) | `tenant:compute:instances:read` |
| **Platform Admin** | `platform:*` | `platform:admin:tenants:*` |
| **Platform Operator** | `platform:ops:*` | `platform:ops:instances:*` |

## Audit

Audit middleware (`middleware/audit.go`) logs every mutating operation with:
- TenantID, UserID, Roles
- Method, Path, StatusCode, Duration
- RequestID (for correlation)
- Operation-specific details in structured JSON

## Secrets Service

`ports.SecretService` manages secrets and their binding to instances/volumes:

### Operations

| Operation | Endpoint | Description |
|-----------|----------|-------------|
| Create | `POST /api/v1/secrets` | Create secret (name, type, data key/value pairs) |
| Get | `GET /api/v1/secrets/:secret_id` | Get secret metadata (data values NOT returned) |
| List | `GET /api/v1/secrets` | List secrets for tenant |
| Bind | `POST /api/v1/secrets/:secret_id/bindings` | Bind secret to instance/volume (target type, target ID, mount path, env prefix) |
| Unbind | `DELETE /api/v1/secrets/:secret_id/bindings/:binding_id` | Remove binding |
| ListBindings | `GET /api/v1/secrets/:secret_id/bindings` | List all bindings for secret |

### Secret Types

- `opaque` — generic key/value pairs
- `tls` — TLS certificate + private key
- `ssh-key` — SSH private key
- `docker-registry` — Docker registry credentials

### Adapters

| Adapter | Use Case | Source |
|---------|----------|--------|
| `KubernetesSecretProvider` | Production — creates K8s Secret objects, uses K8s Secret CSI driver for volume mount injection | `pkg/adapters/runtime/kubernetes_secret_provider.go` |
| `LocalSecretService` | Dev — in-memory secret storage | `pkg/adapters/runtime/local_secret_service.go` |

### Secret Bindings

A secret binding (`SecretBindingRecord`) records:
- Which secret is bound (`SecretID`)
- To which target (`TargetType`: instance/volume, `TargetID`)
- How it's mounted (`MountPath` for filesystem mount, `EnvPrefix` for env var injection)
- State (binding → bound → unbinding → unbound)

## KMS / SM4 Encryption Provider

`ports.EncryptionService` provides at-rest encryption operations:

| Operation | Description |
|-----------|-------------|
| `Encrypt(ctx, plaintext, keyRef) -> (ciphertext, keyID, error)` | Encrypt data |
| `Decrypt(ctx, ciphertext, keyID) -> (plaintext, error)` | Decrypt data |
| `GenerateKey(ctx, algorithm) -> (keyID, error)` | Generate a new encryption key |
| `ListKeys(ctx) -> []KeyMetadata` | List available keys |

### Algorithms

- `sm4` — SM4 symmetric cipher (Chinese national standard, used in KMS integration)
- `aes-256-gcm` — AES-256-GCM (fallback / non-KMS deployments)

### Adapter: `KMSEncryptionProvider`

Real provider adapter at `pkg/adapters/runtime/kms_encryption_provider.go`:
- Integrates with KMS service over K8s API
- Supports SM4 algorithm via GM/T 0002-2012 standard
- Key lifecycle managed via KMS (generation, rotation, deletion)
- Sprint 5 live gate (KMS-SM4 live) validates real KMS integration

### Adapter: `LocalEncryptionService`

Dev adapter at `pkg/adapters/runtime/local_encryption_service.go`
- In-memory key storage
- SM4 and AES-256-GCM both supported in software
- No KMS dependency — use `isConfigured=false` for dev mode

## Workload Identity

`ports.WorkloadIdentityService` manages workload-scoped API keys:

- `IssueWorkloadKey(ctx, instanceID) -> (keyID, secret, error)` — Create key for a workload instance
- `RevokeWorkloadKey(ctx, keyID) -> error` — Revoke key
- `ValidateWorkloadKey(ctx, keyID, secret) -> (instanceID, error)` — Validate (used by Gateway to authenticate workload → Gateway calls)

Implementation: `MetadataWorkloadIdentityService` (Postgres-backed via `pkg/adapters/runtime/workload_identity_service.go`).

## Cross-Service Authentication

### Service-to-Service Model

| Layer | Mechanism | mTLS? |
|-------|-----------|-------|
| **Gateway → Services (HTTP→gRPC)** | JWT Bearer from user session; no additional service-level auth | No |
| **Services → Core API (HTTP)** | Core Go SDK injects long-lived API Key (`X-API-Key` header) | No |
| **Gateway → K8s Cluster Proxy** | Per-target mTLS (configurable via `k8s_cluster_proxy_targets` table, migration 013) | Yes, per-proxy-target |
| **Service-to-Service (gRPC)** | None (internal network only, no tenant metadata in gRPC metadata — tenant context is passed in request message fields) | No |

### What Is Not Implemented

- **SPIRE/SPIFFE** — There is no native SPIRE agent or SPIFFE workload attestation in Go code. SPIFFE references exist only in gRPC dependency cache (`A87-mtls-spiffe-support.md` within protobuf tooling).
- **Certificate rotation** — No automated certificate rotation logic exists in this repository. TLS certificates for K8s cluster proxy targets are stored in the database and managed as data, not rotated programmatically.
- **gRPC tenant interceptor** — Neither `services/pkg/bootstrap/server.go` nor Core `pkg/bootstrap/server.go` has a gRPC unary interceptor that extracts tenant context from gRPC metadata. Tenant identity is passed in protobuf message fields (e.g., `req.TenantId`), not via gRPC metadata. See [Tenant Context Propagation](tenant-context-propagation.md) for details.

### mTLS for K8s Cluster Proxy

Migration `20260620_013_k8s_cluster_proxy_target_mtls.sql` adds mTLS configuration fields to the `k8s_cluster_proxy_targets` table. The proxy forwarding service (`pkg/adapters/runtime/k8s_cluster_proxy_forwarding_service.go`) uses these fields when establishing connections to target clusters. Test evidence in `k8s_cluster_proxy_forwarding_service_test.go` uses idempotency keys `"create-mtls-vc"`, `"mtls-vc"`, and `"proxy-mtls"`.

## References

- [Middleware](middleware.md) — Middleware chain composition
- [Idempotency](idempotency.md) — Idempotency key design
- [Tenant Context Propagation](tenant-context-propagation.md) — HTTP vs gRPC context split
- [Gateway](../services-layer/gateway.md) — Auth middleware integration in Gateway boot
- [Kubernetes Namespace Layout](../deployment/k8s-namespace-layout.md) — Infrastructure namespace structure
- Source: `repo/pkg/security/sandboxtoken/`, `repo/services/ani-gateway/internal/middleware/`, `repo/pkg/ports/secrets.go`, `repo/pkg/ports/encryption.go`, `repo/pkg/ports/identity.go`, `repo/pkg/adapters/runtime/*secret*.go`, `repo/pkg/adapters/runtime/*encryption*.go`, `repo/pkg/adapters/runtime/workload_identity*.go`