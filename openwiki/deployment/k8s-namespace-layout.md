---
type: concept
title: Kubernetes Namespace Layout
description: "Namespace architecture: ani-system (primary), ani-sN-<component> infrastructure namespaces, ani-gpu-system, ani-tenant-template for tenant isolation, helm profile-driven deployment, manifests directory structure."
tags: [deployment, kubernetes, namespaces, manifests, profiles]
---

# Kubernetes Namespace Layout

## Namespace Architecture

The ANI platform uses a **multi-namespace** deployment model:

| Namespace | Purpose | Created By |
|-----------|---------|------------|
| `ani-system` | Primary namespace: all Core controllers, Gateway, services, CRDs | Helm default (`global.namespace`) |
| `ani-s05-objectstore` | MinIO object store infrastructure | Real-lab manifests |
| `ani-s06-vectorstore` | Milvus vector store infrastructure | Real-lab manifests |
| `ani-s07-observability` | Prometheus, Loki, Grafana observability stack | Real-lab manifests |
| `ani-gpu-system` | GPU operator, HAMi scheduler, DCGM exporter | GPU profile manifests |
| `ani-tenant-template` | Template network policies for tenant isolation | Platform bootstrap |
| `ani-sprint14-resilience` | Sprint 14 resilience live-gate testbed | Lab-specific |
| `ani-sprint11-vm-storage-smoke` | Sprint 11 VM storage smoke tests | Lab-specific |

### Primary Namespace: `ani-system`

Configured via `global.namespace` in Helm values (default `ani-system`). All core services run here:

- Controllers (reconcile, GPU scheduling, instance management)
- Gateway (ani-gateway)
- Core API (ani-core-api)
- Services (tenant-service, task-service, kb-service)
- Auth service (ani-auth)
- CRDs and controllers

### Infrastructure Namespaces (`ani-sN-<component>`)

Named after sprint milestones to track which stage each dependency was validated:

| Namespace | Sprint/Milestone | Dependencies |
|-----------|-----------------|--------------|
| `ani-s05-objectstore` | Sprint 05 | MinIO operator, buckets |
| `ani-s06-vectorstore` | Sprint 06 | Milvus cluster, attu |
| `ani-s07-observability` | Sprint 07 | Prometheus, Loki, Grafana |

### GPU Namespace: `ani-gpu-system`

Houses GPU infrastructure components:

- **Volcano scheduler** — GPU queue scheduling
- **HAMi** — GPU device sharing/memory partitioning
- **DCGM exporter** — GPU telemetry
- **GPU Operator** — NVIDIA driver management, MIG partitioning

### Tenant Isolation Template: `ani-tenant-template`

**File**: `deploy/manifests/m1-infra-c/20-tenant-networkpolicy-template.yaml`

Template namespace for tenant network isolation. Actual tenant namespaces are created dynamically by replacing `ani-tenant-template` with the tenant-specific namespace name and `REPLACE_WITH_TENANT_UUID` with the tenant's UUID.

Default policies applied:

| Policy | Effect |
|--------|--------|
| `ani-tenant-default-deny` | Default deny ingress + egress (zero-trust baseline) |
| `ani-allow-gateway-to-tenant-services` | Allow Gateway (`ani-system` → tenant namespace) on port 8000 |
| `ani-allow-tenant-dns` | Allow DNS (port 53 UDP/TCP) to kube-dns |

## Manifests Directory Structure

```
deploy/manifests/
├── m1-e2e-b/            # End-to-end test manifests (batch B)
├── m1-e2e/              # End-to-end test manifests
├── m1-gpu-a/            # GPU infrastructure manifests (Volcano, HAMi, DCGM)
├── m1-infra-a/          # Infrastructure group A (PostgreSQL, NATS, Redis)
├── m1-infra-c/          # Infrastructure group C (NetworkPolicies, tenant template)
├── m1-infra-d/          # Infrastructure group D
├── m1-infra-e/          # Infrastructure group E
├── m1-infra-f/          # Infrastructure group F
├── m1-instance-a/ thru m1-instance-r/  # Instance lifecycle manifests (18 groups)
├── m1-runtime-a/        # Runtime workload manifests
└── m1-sandbox-kata/     # Sandbox (Kata Containers) manifests
```

## Helm Profile-to-Namespace Mapping

| Profile | Deploys Into | Infrastructure Namespaces Created |
|---------|-------------|----------------------------------|
| `dev` | `ani-system` | None (external deps via docker-compose) |
| `attachK8s` | `ani-system` | User-managed |
| `gpuScheduling` | `ani-system`, `ani-gpu-system` | Volcano, HAMi, DCGM |
| `gpuSchedulingE2E` | `ani-system` | No infra, references existing GPU setup |
| `runtimeFoundation` | `ani-system` | No infra |

## ConfigMap & Secret Management

ConfigMaps and Secrets are managed through Helm chart values and the `global.secretName` / `global.secretKey` pattern:

```yaml
infrastructure:
  postgresql:
    secretName: ani-bootstrap-placeholders
    secretKey: database-url
  nats:
    secretName: ani-bootstrap-placeholders
    secretKey: nats-url
  redis:
    secretName: ani-bootstrap-placeholders
    secretKey: redis-url
```

The `ani-bootstrap-placeholders` Secret holds connection strings for external dependencies. In production, these are replaced with real credentials during GitOps/CI-CD deployment. The SecretService (see [Auth & Security](../core/auth-security.md)) handles runtime secret operations (create, bind, unbind) separate from bootstrap secrets.

## Ingress / Public Exposure

External access to console, API, and services is configured through the `publicIngress` setting in the Helm chart:

```yaml
# deploy/helm/ani-platform/values.yaml
publicIngress:
  enabled: true
  provider: ani-gateway
  defaultEnabled: false   # per-instance opt-in
```

**No Kubernetes Ingress resources are defined** in this repository. The `publicIngress` provider is `ani-gateway`, meaning the Gateway itself handles external traffic (no NGINX Ingress Controller, Traefik, or other ingress controller is required). Instance-level ingress exposure is opt-in per workload via the `instanceFoundation` profile (`publicIngress.defaultEnabled: false`).

## References

- [Helm Umbrella Chart](helm-charts.md) — Chart structure and deployment profiles
- [Real K8s Lab](real-k8s-lab.md) — Live gate manifests
- [Quota & Tenancy](../core/quota-tenancy.md) — RLS-based tenant isolation
- Source: `deploy/helm/ani-platform/values.yaml`
- Source: `deploy/manifests/`