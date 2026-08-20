---
type: reference
title: Services OpenAPI & SDK
description: "Services OpenAPI specification (api/openapi/services/v1.yaml), SDK generation from services contract, API-first development workflow with validation gates"
tags: [services-layer, api, openapi, sdk, spec]
---

# Services OpenAPI & SDK

## Specification

**File**: `repo/api/openapi/services/v1.yaml`

Services API specification with URL prefix `/api/v1/svc`. Contains 21+ paths (all currently 501 stubs) covering:

| Path Group | Resources |
|------------|-----------|
| `/api/v1/svc/models` | Model listing, create, get, delete; model versions |
| `/api/v1/svc/inference-services` | Inference service CRUD, lifecycle operations |
| `/api/v1/svc/knowledge-bases` | KB CRUD, document management, chunk query, search |
| `/api/v1/svc/gpu-containers` | GPU container instances (Services-managed) |
| `/api/v1/svc/sandboxes` | Sandbox instances (Services-managed) |
| `/api/v1/svc/tenant-plans` | Tenant plan and quota operations |

## SDK Generation

Services SDKs are generated from `api/openapi/services/v1.yaml`:

| Language | Directory | Package |
|----------|-----------|---------|
| Go | `sdks/services/go/` | `github.com/kubercloud/ani-sdks/services-go/anisdk` |
| Python | `sdks/services/python/` | `kubercloud-ani-services` |
| TypeScript | `sdks/services/typescript/` | `@kubercloud/ani-services-client` |
| Java | `sdks/services/java/` | `com.kubercloud:ani-services-java` |

Generation command: `make gen-services-sdk`

## Mock Server

Core API mock server (Prism HTTP mock) at `http://127.0.0.1:4010/api/v1`. Used by SDK smoke tests and frontend development before real backends exist.

## Validation Gates

- `make validate-services` — aggregate gate: API split, Services boundary, route contract, semantic contract
- `make validate-services-api-contract` — spec-to-code/code-to-spec matching
- SDK drift gates: `validate-services-sdk-alpha`, `validate-services-sdk-beta`

## References

- [Services Layer Overview](overview.md) — Scope and ownership
- [Core vs Services Boundary](../architecture/core-vs-services-boundary.md) — Hard boundary rules
- Source: `api/openapi/services/v1.yaml`, `sdks/services/`