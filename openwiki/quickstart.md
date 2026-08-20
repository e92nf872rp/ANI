---
type: concept
title: ANI Repository Quickstart
description: "Entry point for the KuberCloud ANI AI Private Cloud platform wiki. Repository map, architecture overview, task-routing table, validation commands, and links to all major concept pages."
tags: [quickstart, entry-point, navigation]
---

# ANI Repository Quickstart

This wiki documents the **KuberCloud ANI** monorepo — an AI Private Cloud platform that separates the infrastructure control plane (ANI Core) from cloud services (ANI Services).

## Architecture in 30 Seconds

```
                                ┌─────────────────────────┐
                                │  Console / BOSS / CLI   │
                                │  (React 18 + TDesign)  │
                                └──────────┬──────────────┘
                                           │
                    ┌──────────────────────┼──────────────────────┐
                    │  Gateway (Hertz)     │                      │
                    │  /api/v1/*    /api/v1/svc/*                │
                    ├──────────┬───────────┘                      │
                    │          │                                  │
               ┌────▼────┐  ┌▼───────────────────┐  ┌──────────────┐
               │  Core   │  │  Services (gRPC)    │  │  AI Services │
               │  Services│  │  model / kb / inf  │  │  RAG/Parse   │
               │  gRPC   │  └────────────────────┘  └──────────────┘
               └────┬────┘
                    │
               ┌────▼────┐
               │  ports/ │
               │ adapters│
               └────┬────┘
                    │
          ┌─────────┼─────────┐
          ▼         ▼         ▼
       K8s/KubeVirt  Kube-OVN  PG/MinIO/NATS/Redis/Milvus/Harbor
```

**Key rules:**
- Core owns infrastructure platform (`pkg/`, `api/openapi/v1.yaml`, `deploy/`, `sdks/core/`, `cli/`)
- Services owns cloud services (`services/*`, `frontends/*`, `ai/`, `operators/`)
- Core opens API only; Services uses Core SDK — no direct imports from Core
- gRPC is for Core internal communication, not a bypass for Services to Core

## Repository Map

| Directory | Layer | Purpose |
|-----------|-------|---------|
| `pkg/ports/` | Core | 30+ capability interface abstractions |
| `pkg/adapters/` | Core | Default provider implementations (K8s, Kube-OVN, MinIO, Milvus, etc.) |
| `pkg/bootstrap/` | Core | Dependency initialization & lifecycle |
| `pkg/repo/`, `pkg/nats/`, `pkg/types/` | Core | Shared repository, messaging, types |
| `services/ani-gateway/` | Gateway | Unified HTTP entrypoint (Hertz) |
| `services/auth-service/` | Core Services | gRPC auth: JWT, OIDC, API Keys |
| `services/task-service/` | Core Services | gRPC async task CRUD |
| `services/tenant-service/` | Core Services | gRPC tenant plan & quota binding |
| `services/metering-service/` | Core Services | gRPC periodic metering collection |
| `services/reconcile-worker/` | Core Services | Background reconcile controller |
| `services/model-service/` | Services | gRPC model registry CRUD |
| `services/kb-service/` | Services | Python gRPC knowledge base service |
| `services/pkg/bootstrap/` | Services | Services-correct bootstrap (1 service uses it) |
| `frontends/console/` | Frontend | User Console (React 18 + TDesign) |
| `frontends/boss/` | Frontend | Admin BOSS portal (React 18 + TDesign) |
| `ai/rag-engine/` | AI | Python RAG engine (LangChain, Milvus, gRPC) |
| `sdks/core/` | SDKs | Core SDK (Go/Java/Python/TypeScript) |
| `sdks/services/` | SDKs | Services SDK (Go/Java/Python/TypeScript) |
| `cli/ani/` | CLI | ANI CLI tool (cobra/viper) |
| `api/openapi/` | API Contracts | Core (`v1.yaml`) and Services (`services/v1.yaml`) OpenAPI specs |
| `api/proto/` | API Contracts | Protobuf definitions for gRPC services |
| `deploy/helm/ani-platform/` | Deployment | Helm umbrella chart |
| `deploy/real-k8s-lab/` | Deployment | Real K8s lab manifest & live gates |
| `deploy/migrations/` | Deployment | SQL migration files |
| `deploy/release/` | Deployment | Release engineering artifacts |
| `deploy/docker/` | Deployment | Local dev docker-compose |
| `deploy/manifests/` | Deployment | M1 contract manifests (60+ YAML) |
| `architecture/` | Governance | Architecture baseline YAMLs (5 files) |
| `development-records/` | Records | Sprint closures, live gate evidence |
| `tests/e2e/` | Testing | E2E integration tests (SSE streaming) |

## Task Routing Table

| If you want to... | Start here | Owning files | Focused tests | Validate with |
|-------------------|------------|--------------|---------------|---------------|
| Understand the architecture | [architecture/overview.md](architecture/overview.md) | `ANI-05*`, `architecture/*.yaml` | — | `make validate-architecture` |
| Add a new Core API endpoint | [core/router.md](core/router.md) + `api/openapi/v1.yaml` | `services/ani-gateway/internal/router/*.go` | `*_test.go` alongside | `make validate-architecture` |
| Add a new Services API endpoint | [services-layer/services-api.md](services-layer/services-api.md) + `api/openapi/services/v1.yaml` | `services/ani-gateway/internal/router/*_resources.go` | `*_test.go` alongside | `make validate-services` |
| Modify instance lifecycle | [core/instances.md](core/instances.md) | `pkg/ports/workload_runtime.go`, `pkg/adapters/runtime/instance*.go` | `instance_*_test.go`, `*_integration_test.go` | `make validate-instance-contracts` |
| Modify GPU scheduling | [core/gpu.md](core/gpu.md) | `pkg/ports/gpu*.go`, `pkg/adapters/runtime/gpu*` | `*_test.go` | `make validate-gpu-contracts` |
| Modify auth middleware | [core/middleware.md](core/middleware.md) | `services/ani-gateway/internal/middleware/auth*.go` | `auth_test.go`, `rbac_test.go` | `make validate-services` |
| Add a migration | [deployment/database-migrations.md](deployment/database-migrations.md) | `deploy/migrations/YYYYMMDD_NNN_*.sql` | — | Atlas validate |
| Deploy a real provider gate | [deployment/real-k8s-lab.md](deployment/real-k8s-lab.md) | `deploy/real-k8s-lab/*.yaml` | `development-records/live-evidence/*.json` | `make validate-<provider>-live-gate` |
| Change resilience behavior | [core/resilience.md](core/resilience.md) | `pkg/adapters/resilience/*.go` | `resilience_test.go` | `make validate-sprint14-resilience-live-gate` |
| Work on RAG engine | [ai-services/rag-engine.md](ai-services/rag-engine.md) | `ai/rag-engine/app/**/*.py` | `ai/rag-engine/tests/*.py` | `make test-python` |
| Modify Console frontend | [frontends/console.md](frontends/console.md) | `frontends/console/src/**/*.tsx` | — | `make build-console && make validate-console` |
| Run all tests | — | — | — | `make test` |
| Check architecture compliance | — | — | — | `make validate-architecture && make validate-services` |

## Major Workflows

### Instance Creation Flow
```
User → Gateway POST /api/v1/instances
  → middleware chain (auth/RBAC/audit/idempotency)
  → registerInstances handler
    → WorkloadAdmission (guardrails)
    → WorkloadPlanAuditStore (audit trail)
    → WorkloadProviderDryRun (server-side K8s dry-run)
    → WorkloadProviderApply (create K8s resources)
    → WorkloadStatusReconciler (observe & reconcile)
  → 202 Accepted + AsyncTask
```

### GPU Scheduling Flow
```
Tenant Admin → Gateway POST /api/v1/queues
  → GPUSchedulingQueueStore (Volcano Queue CRUD)
  → Volcano scheduler dispatches pods
  → HAMi handles vGPU fraction allocation
  → DCGM exports GPU utilization metrics
  → Metering collection records usage
```

### KB Query SSE Streaming Flow
```
Console → Gateway POST /api/v1/svc/knowledge-bases/{id}/query (stream=true)
  → Gateway gRPC client → kb-service
    → kb-service queries RAG engine via gRPC
      → RAG engine retrieves from Milvus
    → Streaming chunks back through gRPC
  → Gateway SSE proxy streams to Console
```

### API Contract Gate Flow
```
Edit OpenAPI YAML
  → Run `make gen-core-api-compat-baseline` / `make gen-api`
  → Run `make validate-architecture` / `make validate-services`
  → Run `make gen-core-sdk` / `make gen-services-sdk`
  → Run `make validate-sdk-drift`
  → PR with CODEOWNERS approval
```

## Quick Validation Commands

```bash
# Must run before any commit
make test && make validate-architecture && git diff --check

# Full validation suite
make validate-architecture    # Core layer compliance
make validate-services        # Services layer compliance  
make validate-sprint13-b-track-production-shape   # Sprint 13 gates
make validate-sprint14-resilience-live-gate       # Sprint 14 gates

# Service-specific
make test-go              # All Go tests
make test-python          # All Python tests
make test-cover           # Coverage report
```

## Wiki Navigation

| Section | Key Pages |
|---------|-----------|
| [Architecture](architecture/overview.md) | Core vs Services boundary, ports/adapters pattern, tech stack |
| [Core Platform](core/overview.md) | Ports catalog, adapters, [bootstrap](core/bootstrap.md), gateway, [middleware](core/middleware.md), [router](core/router.md), instances, network, storage, GPU, auth/security, observability, async tasks, quota/tenancy, reconcile controller, resilience, [idempotency](core/idempotency.md), [tenant context propagation](core/tenant-context-propagation.md) |
| [Core Services](core-services/auth-service.md) | Auth, task, tenant, metering, reconcile worker |
| [Services Layer](services-layer/overview.md) | Services API, [Gateway (services perspective)](services-layer/gateway.md), model service, KB service, inference |
| [Frontends](frontends/console.md) | Console, BOSS portal, module documentation index |
| [AI Services](ai-services/rag-engine.md) | RAG engine, doc parser (planned), whisper (planned) |
| [Operators](operators/inference-operator.md) | Inference operator, upgrade operator (planned) |
| [SDKs](sdks/overview.md) | Core SDK, Services SDK, multi-language clients |
| [CLI](cli/ani-cli.md) | ANI CLI tool |
| [Deployment](deployment/helm-charts.md) | Helm charts, real K8s lab, installer, docker-compose, DB migrations, release artifacts, manifests, [K8s namespace layout](deployment/k8s-namespace-layout.md) |
| [CI/CD](ci-cd/build-system.md) | Build system, architecture baselines, validation gates, E2E tests, GitHub workflows |
| [Development](development/sprint-tracking.md) | Sprint tracking, development records, task management |
| [Design Docs](design-docs/index.md) | ANI-* document series, design documents |

## References

- **Source**: `/repo/` (monorepo root)
- **Product docs**: `/ANI-*.md` (in repository root)
- **CLAUDE.md**: Engineering rules, architecture boundaries, commit discipline
- **Current sprint**: `/repo/CURRENT-SPRINT.md`
- **Services team guide**: `/repo/services/ANI-SERVICES-TEAM-GUIDE.md`
- **Core OpenAPI**: `/repo/api/openapi/v1.yaml`
- **Services OpenAPI**: `/repo/api/openapi/services/v1.yaml`
- **User instructions**: `/openwiki/INSTRUCTIONS.md`