---
type: service
title: Auth Service (gRPC)
description: "Standalone gRPC authentication service: JWT signing/verification, OIDC/SSO login, refresh token management, API Key CRUD, password login, token blocklist, OIDC session management"
tags: [core-services, auth, jwt, oidc, api-keys, authentication]
---

# Auth Service (gRPC)

## Overview

Service: `services/auth-service/` · gRPC port: 9101 · Bootstrap: uses Core `pkg/bootstrap/` (accepted_baseline — see [Architecture Baselines](../ci-cd/architecture-baselines.md))

Standalone gRPC service providing all authentication and authorization primitives:

- **JWT** — RSA-based signing/verification (1h TTL), configurable issuer, public/private key from PEM files or env vars
- **OIDC/SSO** — Enterprise SSO login flow via OIDC Provider (GitHub, GitLab, enterprise AD/LDAP)
- **Refresh Tokens** — Sliding renewal pattern (configurable TTL, rotation on use)
- **API Keys** — Create/list/revoke with RBAC scope binding
- **Password Login** — Tenant user + platform admin password authentication (bcrypt hashed)
- **Token Blocklist** — Redis-backed JWT blocklist for logout/revocation
- **OIDC Session Management** — Auth code -> token exchange, session state, JWKS fetch

## Entrypoint

```go
func main() {
    cfg := config.Load()
    deps := bootstrap.MustConnect(cfg.Config)
    defer deps.Close()

    authSvc := service.NewAuthService(deps.DB, deps.Ports.Cache, service.JWTConfig{...})
    bootstrap.RunGRPC(cfg.GRPCPort, authSvc.Register, deps)
}
```

## Service Implementation: `AuthService`

| Method | gRPC | Description |
|--------|------|-------------|
| `Register(*grpc.Server)` | Proto `auth.v1.AuthService` | Registers all RPCs |
| `LoginPassword` | `rpc LoginPassword(LoginPasswordRequest) -> LoginResponse` | Tenant user password login |
| `LoginPlatform` | `rpc LoginPlatform(LoginPlatformRequest) -> LoginResponse` | Platform admin password login |
| `LoginOIDC` | `rpc LoginOIDC(LoginOIDCRequest) -> LoginResponse` | OIDC code -> token exchange |
| `RefreshToken` | `rpc RefreshToken(RefreshTokenRequest) -> LoginResponse` | Sliding refresh |
| `ValidateToken` | `rpc ValidateToken(ValidateTokenRequest) -> ValidateTokenResponse` | Auth middleware uses this |
| `ValidateAPIKey` | `rpc ValidateAPIKey(ValidateAPIKeyRequest) -> ValidateAPIKeyResponse` | Auth middleware uses this |
| `CreateAPIKey` | `rpc CreateAPIKey(CreateAPIKeyRequest) -> APIKey` | New API key |
| `ListAPIKeys` | `rpc ListAPIKeys(ListAPIKeysRequest) -> ListAPIKeysResponse` | List tenant's keys |
| `RevokeAPIKey` | `rpc RevokeAPIKey(RevokeAPIKeyRequest) -> Empty` | Revoke key |
| `Logout` | `rpc Logout(LogoutRequest) -> Empty` | Add token to blocklist |
| `BeginOIDCLogin` | `rpc BeginOIDCLogin(BeginOIDCLoginRequest) -> BeginOIDCLoginResponse` | Start OIDC auth flow |

## Key Subsystems

### JWT (`internal/service/jwt.go`)

- RSA key pair loaded from PEM files or inline PEM env vars
- JWT claims: `sub` (user_id), `tenant_id`, `roles[]`, `scope`, `principal_kind` (user|service), `aud`, `iss`, `exp`, `iat`, `jti`
- Service JWT: additional `principal_kind=service`, `aud=ani-core`, used for service-to-service auth
- Configurable via: `JWT_PRIVATE_KEY_PEM`, `JWT_PUBLIC_KEY_PEM`, `JWT_ISSUER`, `JWT_EXPIRY_SECONDS`

### OIDC (`internal/service/oidc.go`)

- OIDC Discovery URL -> JWKS fetch on startup
- Auth code -> token exchange at token endpoint
- JWT or userinfo call to extract identity claims
- Group-to-role mapping via `OIDC_GROUP_ROLE_MAP_JSON`
- Configurable via: `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_AUTH_URL`, `OIDC_TOKEN_URL`, `OIDC_JWKS_URL`

### Refresh Tokens (`internal/service/refresh_tokens.go`)

- Stored in `refresh_tokens` PG table with tenant_id, user_id, token_hash, expires_at, revoked_at
- Sliding renewal: each RefreshToken call rotates the token (new hash, extended expiry)
- Configurable via: `REFRESH_TOKEN_TTL_SECONDS`

### API Keys (`internal/service/api_keys.go`)

- Stored in `api_keys` PG table with tenant_id, name, key_hash, scope, state (active|revoked)
- Key plaintext returned only on creation; never stored
- Validation via constant-time hash comparison

### Token Blocklist (`internal/service/token_blocklist.go`)

- Redis-backed: `jwt_blocklist:{jti}` with TTL = remaining token lifetime
- Checked by `ValidateToken` before JWT signature verification
- Auto-expiry via Redis TTL (no cleanup needed)

## Tests

- `auth_service_test.go` — Core login flow (password)
- `jwt_test.go` — Signing, verification, expiry
- `oidc_test.go` — OIDC flow mock
- `oidc_sessions_test.go` — Session management
- `password_login_test.go` — Password auth scenarios
- `platform_login_test.go` — Platform admin login
- `refresh_tokens_test.go` — Sliding renewal
- `api_keys_test.go` — CRUD, scope validation
- `token_revocation_test.go` — Blocklist integration
- `auth_integration_test.go` — End-to-end auth flow

## References

- [Auth Security](../core/auth-security.md) — Gateway middleware consuming this service
- [Middleware](../core/middleware.md) — Auth middleware chain
- Source: `services/auth-service/`