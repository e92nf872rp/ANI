---
type: reference
title: Architecture Governance Baselines
description: "Architecture governance baselines in repo/architecture/ (5 YAML files): component-import-allowlist.yaml, services-boundary-baseline.yaml, services-contract-baseline.yaml, services-route-baseline.yaml, inference-service-handler-baseline.yaml. Coupling levels, accepted_baseline exception regime, enforcement via validate-architecture and validate-services."
tags: [ci-cd, architecture, governance, baselines, enforcement]
---

# Architecture Governance Baselines

## Overview

**Directory**: `repo/architecture/` · **Files**: 5 YAML baseline files

The architecture baselines define the governance rules that enforce the ANI Core/Services boundary and component coupling discipline. They are consumed by `make validate-architecture` and `make validate-services`.

## Baseline Files

### 1. `component-import-allowlist.yaml`

Catalogs every bounded_direct import with its coupling level and rationale:

| Coupling Level | Description | Examples |
|----------------|-------------|----------|
| `port_required` | Must use port interface, not direct SDK | Any new capability must define a port |
| `adapter_with_extensions` | May extend adapter but must use port boundary | Runtime adapter customization |
| `bounded_direct` | Allowed direct SDK usage (K8s API, postgres drivers, NATS SDK) | K8s client-go, pgx, nats.go |
| `temporary_exception` | Must be removed or migrated to proper level; tracked with expiration (`migrate_by`) | `ai/rag-engine/` — 4 files importing `asyncpg` directly with `migrate_by: Phase 2 — KB gRPC API replaces direct PG access` (recorded in `component-import-allowlist.yaml`); model-service `pkg/bootstrap` imports |

### 2. `services-boundary-baseline.yaml`

Accepted Core import violations by Services code (model-service, ai/ code). Records the specific import paths and files that violate the boundary, classified as `accepted_baseline` — must NOT spread to new code.

### 3. `services-contract-baseline.yaml`

Services OpenAPI security gaps: all operations missing `security` declarations. Recorded as `accepted_baseline` for historical code. New operations must include security declarations.

### 4. `services-route-baseline.yaml`

Gateway route registration gaps:

| Gap Type | Description |
|----------|-------------|
| `spec_not_in_code` | Path exists in OpenAPI spec but not registered in Gateway handler |
| `code_not_in_spec` | Path exists in Gateway handler but not in OpenAPI spec |

### 5. `inference-service-handler-baseline.yaml`

Inference handler status code differences: 200 vs 202 stubs. Recorded as `accepted_baseline`.

## Enforcement

| Gate | Command | Checks |
|------|---------|--------|
| Architecture gate | `make validate-architecture` | Baselines 1-2 + port/import purity |
| Services boundary gate | `make validate-services` | Baselines 3-5 + API split + route contract |

## Exception Regime

The `accepted_baseline` mechanism acknowledges existing violations without allowing new ones. When adding new code:
1. New imports must use `port_required` or `adapter_with_extensions`
2. New API paths must have security declarations
3. New Gateway routes must appear in both spec and code
4. New handler status codes must match spec

## References

- [Core vs Services Boundary](../architecture/core-vs-services-boundary.md) — Boundary rules
- [Ports and Adapters](../architecture/ports-and-adapters.md) — Port abstraction pattern
- [Validation Gates](validation-gates.md) — Gate catalog
- Source: `repo/architecture/`