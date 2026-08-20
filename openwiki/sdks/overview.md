---
type: concept
title: SDK Architecture
description: "ANI Core and Services SDKs: 4-language generated clients (Go, Python, TypeScript, Java), generation pipeline from OpenAPI specs, drift detection gates, mock server integration, SDK metadata tracking."
tags: [sdks, core, services, code-generation, openapi, go, python, typescript, java]
---

# SDK Architecture

## Overview

ANI produces two SDK families, each generated from its respective OpenAPI specification:

| SDK Family | Source Spec | Languages | Validation Gate |
|------------|-------------|-----------|-----------------|
| **Core SDK** | `api/openapi/v1.yaml` | Go, Python, TypeScript, Java | `validate-sdk-alpha`, `validate-sdk-beta` |
| **Services SDK** | `api/openapi/services/v1.yaml` | Go, Python, TypeScript, Java | `validate-sdk-mock-smoke-*` |

## Directory Layout

```
sdks/
├── core/
│   ├── go/              # Core SDK Go client (anisdk.Client)
│   │   ├── anisdk/      # Client package with request/response helpers
│   │   ├── examples/    # Basic usage examples
│   │   └── client_test.go
│   ├── java/
│   ├── python/
│   ├── typescript/      # TypeScript client with full types
│   └── sdk-metadata.json  # Generated checksums for drift detection
└── services/
    ├── go/
    ├── java/
    ├── python/
    ├── typescript/
    └── sdk-metadata.json
```

## SDK Generation Pipeline

```
OpenAPI spec (v1.yaml / services/v1.yaml)
  → openapi-generator / openapi-typescript / go-generate
  → Language-specific client with typed models
  → sdk-metadata.json with SHA256 checksums
  → Validation gates compare generated output vs committed output
```

## Cross-Layer Rule

- **Core SDK** only contains Core resources (instances, networks, storage, GPU, etc.)
- **Services SDK** only contains Services resources (models, inference services, knowledge bases)
- Services must NOT import Core SDK resource types for Services resources
- SDK drift detection gates prevent accidental cross-layer contamination

## Mock Server Integration

The Core API mock server (Prism-based at `http://127.0.0.1:4010/api/v1`) is used for SDK smoke tests. The CLI defaults to this mock server URL when `ANI_BASE_URL` is not set.

## References

- [Core OpenAPI](../architecture/ports-and-adapters.md) — API specification
- [Services API](../services-layer/services-api.md) — Services API specification
- [CLI](../cli/ani-cli.md) — CLI usage of Core SDK
- Source: `sdks/core/`, `sdks/services/`