---
type: concept
title: Core vs Services Boundary
description: "Hard architectural boundary between ANI Core and ANI Services layers: directory ownership, dependency direction, import rules, API split"
tags: [architecture, core, services, boundary, governance]
---

# Core vs Services Boundary

## Directory Ownership

| Layer | Owned Directories | Description |
|-------|-------------------|-------------|
| **Core** | `pkg/`, `api/openapi/v1.yaml`, `deploy/`, `sdks/core/`, `cli/`, `scripts/` | Infrastructure platform: ports, adapters, bootstrap, shared repos, Core API |
| **Services** | `services/*`, `frontends/*`, `ai/`, `operators/` | Cloud services: gRPC microservices, Console/BOSS frontends, AI engines, K8s operators |
| **Mixed (shared review)** | `services/ani-gateway/` | Unified HTTP gateway with Core handler + Services handler split by route prefix |

## Dependency Direction

```
Services code ──► Core SDK / Core OpenAPI REST API ──► Core services
                                                            │
Services code ───(IMPORT NOT ALLOWED)──► Core pkg/ports, pkg/adapters, pkg/bootstrap directly
```

**Hard rule:** Services must never import Core code packages directly. The only allowed communication path is through the Core OpenAPI REST API or Core SDK (which wraps the API). Direct imports from `pkg/ports`, `pkg/adapters`, `pkg/bootstrap`, `pkg/repo`, `pkg/nats`, or `pkg/types` by Services code are prohibited.

**Accepted_baseline exceptions** (recorded in two baseline YAML files):

1. **`repo/architecture/component-import-allowlist.yaml`** — Records every bounded_direct import with coupling level and rationale. Includes model-service imports of `pkg/bootstrap` and `pkg/types` at `temporary_exception` level, with required migration disposition.

2. **`repo/architecture/services-boundary-baseline.yaml`** — Higher-level record of Services-to-Core import violations that are accepted for now but must not spread. Lists specific import paths per service.

Current accepted violations:
- `model-service`: imports `pkg/bootstrap` and `pkg/types` — `temporary_exception`, must migrate during Services boundary hardening.
- `kb-service`, `auth-service`, `task-service`, `metering-service`, `reconcile-worker`: use `pkg/bootstrap` directly — accepted as `accepted_baseline` in services-boundary-baseline.yaml, tracked as `bounded_direct` in component-import-allowlist.yaml.
- `ai/rag-engine/: 4 files import `asyncpg` directly — `temporary_exception` with `migrate_by: Phase 2 — KB gRPC API replaces direct PG access`.

## API Split

- **Core API**: `repo/api/openapi/v1.yaml` — prefix `/api/v1` — owned by Core team
- **Services API**: `repo/api/openapi/services/v1.yaml` — prefix `/api/v1/svc` — owned by Services team
- Core resources must never appear in Services API, and Services resources must never appear in Core API.
- Validation: `make validate-architecture` (API split, route contract, architecture gate)

## Gateway Route Split

In `services/ani-gateway/`, routes are split by prefix:
- `/api/v1/*` — Core handler — owned by Core team
- `/api/v1/svc/*` — Services handler — owned by Services team with Core review

## References

- [Architecture Overview](overview.md)
- [Ports and Adapters](ports-and-adapters.md)
- [Architecture Baselines](../ci-cd/architecture-baselines.md)
- [Gateway Router](../core/router.md)
- [Validation Gates](../ci-cd/validation-gates.md)
- ANI-05 System Architecture Design (`/ANI-05-系统架构设计.md`)
- ANI-SERVICES-TEAM-GUIDE (`/ANI-SERVICES-TEAM-GUIDE.md`)