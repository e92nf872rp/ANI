---
type: reference
title: Build System
description: "Makefile (repo/Makefile) build system: Go workspace (go.work, 15 modules), service builds, code generation (OpenAPI→SDK, Proto→gRPC, API docs), lint, test, validation gates, image builds, offline packaging."
tags: [ci-cd, build, makefile, go, workspace, code-generation]
---

# Build System

## Makefile

**File**: `repo/Makefile` (63KB)

### Build Targets

| Target | Description |
|--------|-------------|
| `build` | Build all Go services |
| `build-gateway` | Build ANI Gateway binary |
| `build-auth-service` | Build Auth Service binary |
| `build-model-service` | Build Model Service binary |
| `build-task-service` | Build Task Service binary |
| `build-reconcile-worker` | Build Reconcile Worker binary |
| `build-cli` | Build ANI CLI binary |
| `build-installer` | Build ANI Installer binary |

### Code Generation

| Target | Description |
|--------|-------------|
| `gen-api` | Generate Gateway Go code + Console TS types from OpenAPI specs |
| `gen-console-api` | Generate Console TS types from services/v1.yaml |
| `gen-proto` | Generate Go gRPC code from Protobuf definitions |
| `gen-core-sdk` | Generate 4-language Core SDK from v1.yaml |
| `gen-services-sdk` | Generate 4-language Services SDK from services/v1.yaml |
| `gen-api-docs` | Generate static HTML API docs from OpenAPI specs |

### Lint

| Target | Description |
|--------|-------------|
| `lint-go` | Go linting (golangci-lint or staticcheck) |
| `lint-python` | Python linting (ruff) |
| `lint-ts` | TypeScript linting (eslint) |

### Test

| Target | Description |
|--------|-------------|
| `test` | Run all tests (Go + Python) |
| `test-go` | Run all Go tests |
| `test-cover` | Generate test coverage report |

## Go Workspace

**File**: `repo/go.work`

Modules:
- `./cli/ani`
- `./pkg`
- `./sdks/core/go`
- `./services/ani-gateway`
- `./services/auth-service`
- `./services/metering-service`
- `./services/model-service`
- `./services/reconcile-worker`
- `./services/task-service`
- `./services/tenant-service`
- `./tools/kms-sm4-live-fixture`
- (scaffolded: `./services/kb-service`, `./operators/inference-operator`, `./operators/upgrade-operator`, `./installer/ani-installer`)

## Code Generation Pipeline

<!-- openwiki: mermaid parse failed and this diagram was converted to a text fence so it does not break rendering. Fix the diagram source and restore the mermaid fence. Parser error: Heuristic: an unescaped angle bracket inside a label breaks rendering; rephrase the label. -->
```text
flowchart LR
    v1["api/openapi/v1.yaml<br/>(Core)"] --> genCore["gen-core-sdk"]
    v1 --> genGw["gen-api<br/>(Gateway handlers)"]
    svc["api/openapi/services/v1.yaml<br/>(Services)"] --> genSvc["gen-services-sdk"]
    svc --> genConsole["gen-console-api"]
    proto["api/proto/**/*.proto"] --> genGrpc["gen-proto<br/>(gRPC stubs)"]
    v1 & svc --> genDocs["gen-api-docs<br/>(HTML)"]
```

## References

<!-- openwiki: broken internal link [repo/go.work] file "repo/go.work" does not exist. Fix the href or restore the target, then delete this comment. -->
- [Go Workspace](repo/go.work) — Module layout
- [Architecture Baselines](architecture-baselines.md) — Build-time enforcement
- [Validation Gates](validation-gates.md) — Validation targets
- [GitHub Workflows](github-workflows.md) — CI integration
- Source: `repo/Makefile`, `repo/go.work`