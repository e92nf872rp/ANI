---
type: concept
title: ANI Core Overview
description: "ANI Core: infrastructure control plane owning compute instances, network, storage, GPU, auth/RBAC, observability, metering, async tasks, quota, and reconcile controllers"
tags: [core, platform, infrastructure]
---

# ANI Core Overview

## Scope

ANI Core is the infrastructure platform layer of ANI. It owns all platform-level capabilities and exposes them through the Core OpenAPI REST API (`/api/v1`), Core SDK (4 languages), and CLI. Core must never implement model inference, RAG, knowledge base query logic, or PaaS business semantics.

## Owned Capabilities

| Domain | Responsibility | Entry Point |
|--------|---------------|-------------|
| **Compute (Instances)** | VM, container, GPU container, sandbox, batch job, K8s cluster lifecycle | `pkg/ports/workload_runtime.go`, `pkg/ports/instances.go` |
| **Network** | VPC, subnet, security group, Kube-OVN provider integration | `pkg/ports/network_resources.go` |
| **Storage** | Block volume, filesystem, object store, vector store APIs | `pkg/ports/storage_resources.go`, `pkg/ports/object_store.go`, `pkg/ports/vector_store.go` |
| **Auth/Security** | JWT, RBAC, API Key, OIDC, secrets, KMS/SM4 encryption, workload identity | `pkg/ports/secrets.go`, `pkg/ports/identity_provider.go` |
| **Observability** | Prometheus query proxy, Loki log store, DCGM GPU metrics | `pkg/ports/observability.go`, `pkg/ports/instance_observability.go` |
| **Metering** | Periodic usage collection, lifecycle-driven collection | `pkg/ports/metering.go` |
| **Async Tasks** | Task CRUD, outbox pattern, NATS JetStream messaging | `pkg/ports/async_task.go`, `pkg/ports/message_bus.go` |
| **Quota/Tenancy** | Resource quota management, tenant plans, RLS enforcement | `pkg/ports/quota.go`, `pkg/ports/tenant.go` |
| **Reconcile** | Status reconciliation, HA leader election, provider dry-run/apply gates | `pkg/ports/reconcile_controller.go`, various workload ports |

## Core Directory Layout

```
repo/
├── pkg/                   # Core shared library
│   ├── ports/             # Capability interfaces (30+ ports)
│   ├── adapters/          # Default provider implementations
│   │   ├── runtime/       # K8s provider adapters (lifecycle, network, storage, GPU, etc.)
│   │   ├── resilience/    # Circuit breaker, retry, timeout wrappers
│   │   ├── postgres/      # PostgreSQL adapter (metadata store)
│   │   ├── nats/          # NATS JetStream adapter (message bus)
│   │   ├── redis/         # Redis adapter (cache store)
│   │   ├── objectstore/   # MinIO adapter (object store)
│   │   ├── vectorstore/   # Milvus adapter (vector store)
│   │   ├── registry/      # Harbor adapter (image registry)
│   │   └── metering/      # Prometheus/DCGM collectors
│   ├── bootstrap/         # Dependency initialization, lifecycle, gRPC server bootstrap
│   ├── repo/              # Async task repo, outbox repo (Postgres-backed)
│   ├── nats/              # Canonical NATS subject definitions and payload types
│   ├── types/             # Tenant context, pagination, common error types
│   └── security/          # Sandbox token subsystem (HMAC)
├── api/openapi/v1.yaml    # Core OpenAPI spec (single source of truth)
├── sdks/core/             # Core SDK (Go, Python, TypeScript, Java)
├── cli/ani/               # Core CLI
├── deploy/                # Helm charts, real K8s lab manifests, Docker Compose
└── scripts/               # Build and release scripts
```

## Services That Run Core Code

| Service | Entry Point | Role |
|---------|-------------|------|
| `services/ani-gateway/` | `main.go` via Hertz | Unified HTTP gateway (Core + Services routes) |
| `services/auth-service/` | `main.go` via gRPC | JWT/OIDC/API Key authentication |
| `services/task-service/` | `main.go` via gRPC | Async task management and outbox publishing |
| `services/metering-service/` | `main.go` via gRPC | Periodic metering collection |
| `services/reconcile-worker/` | `main.go` via gRPC | Background reconcile controller |

**Note:** These services import `pkg/bootstrap` directly, which is the Core bootstrap. The architecturally correct approach is to use `services/pkg/bootstrap/` (see [Services Bootstrap](../services-layer/overview.md)).

## References

- [Architecture Overview](../architecture/overview.md)
- [Bootstrap](bootstrap.md) — `pkg/bootstrap` dependency initialization
- [Ports Catalog](ports-catalog.md) — All port interfaces
- [Adapters](adapters.md) — All adapter implementations
- [Gateway](gateway.md) — Unified HTTP entry point
- [Router](router.md) — Route registration details
- [Instances](instances.md) — Workload instance lifecycle
- [Resilience](resilience.md) — Circuit breaker, retry, timeout wrappers