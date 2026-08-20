---
type: concept
title: Helm Umbrella Chart
description: "ANI Platform Helm umbrella chart (deploy/helm/ani-platform/): Chart.yaml, values.yaml, deployment profiles (dev, attachK8s, offline, clusterValidation, gpuScheduling, gpuSchedulingE2E, runtimeFoundation, instanceFoundation), component contract YAMLs, platform-level configuration."
tags: [deployment, helm, umbrella-chart, profiles, component-contracts]
---

# Helm Umbrella Chart

## Overview

**Directory**: `deploy/helm/ani-platform/` · **Type**: Helm umbrella chart

The `ani-platform` umbrella chart orchestrates all ANI Core infrastructure components as a single deployable unit.

## Chart Structure

```
deploy/helm/ani-platform/
├── Chart.yaml                     # Chart definition (v0.1.0-dev, application type)
├── values.yaml                    # Global configuration + profile definitions
├── profiles/
│   ├── dev.yaml                   # Local developer profile (docker-compose)
│   ├── attach-k8s.yaml            # Attach to existing K8s cluster
│   ├── cluster-validation.yaml    # Preflight checks before install/upgrade
│   ├── gpu-scheduling.yaml        # GPU scheduling contracts (HAMi, Volcano, DCGM)
│   ├── gpu-scheduling-e2e.yaml    # GPU scheduling E2E verification profile
│   ├── runtime-foundation.yaml    # Workload runtime contracts
│   ├── instance-foundation.yaml   # First-class instance object contracts
│   └── offline.yaml               # Offline/customer-site profile
└── component-contracts/
    ├── gpu-inventory.yaml         # GPU inventory component contract
    ├── gpu-scheduling.yaml        # GPU scheduling component contract
    ├── harbor.yaml                # Harbor registry component contract
    ├── instance-fabric.yaml       # Instance fabric component contract
    ├── milvus.yaml                # Milvus vector store component contract
    ├── minio.yaml                 # MinIO object store component contract
    ├── nats.yaml                  # NATS JetStream component contract
    ├── postgresql.yaml            # PostgreSQL component contract
    ├── redis.yaml                 # Redis component contract
    └── workload-runtime.yaml      # Workload runtime component contract
```

## Deployment Profiles

| Profile | Description | Dependencies | Use Case |
|---------|-------------|--------------|----------|
| `dev` | Local developer profile | External deps via docker-compose | Local development |
| `attachK8s` | Attach to existing K8s cluster | Install + configure components | Staging/production |
| `offline` | Customer-site offline install | Pre-mirrored images/packages | Air-gapped deployment |
| `clusterValidation` | Preflight checks | No components installed | Pre-install validation |
| `gpuScheduling` | GPU scheduling enablement | Volcano, HAMi, DCGM | GPU cluster setup |
| `gpuSchedulingE2E` | GPU E2E verification | GPU scheduling profile + checks | GPU validation |
| `runtimeFoundation` | Workload runtime contracts | Runtime infrastructure | Instance runtime |
| `instanceFoundation` | Instance object contracts | Full instance foundation | Instance management |

## Global Configuration

| Key | Description | Default |
|-----|-------------|---------|
| `global.namespace` | Target namespace | `ani-system` |
| `global.imageRegistry` | Container image registry | `harbor.ani.internal/ani` |
| `global.imagePullPolicy` | Image pull policy | `IfNotPresent` |
| `global.profile` | Active deployment profile | `dev` |
| `global.networkPolicy.enabled` | Enable network policies | `true` |
| `global.networkPolicy.defaultDeny` | Default deny ingress/egress | `true` |

## Kubernetes Requirements

- **Minimum version**: 1.28.0
- **Target version**: 1.36.x
- **Preferred CNI**: Kube-OVN

## GitOps / ArgoCD

**Not configured in this repository.** There are no ArgoCD Application manifests, app-of-apps patterns, or GitOps sync strategies. The Helm chart would be compatible with ArgoCD (plain Kubernetes manifests), but promotion workflows (dev → staging → prod) and sync policies must be defined externally.

For environment promotion and CI/CD pipelines, see [Validation Gates](../ci-cd/validation-gates.md) and [Build System](../ci-cd/build-system.md).

## References

- [Kubernetes Namespace Layout](k8s-namespace-layout.md) — Namespace architecture and conventions
- [Real K8s Lab](real-k8s-lab.md) — Production-shaped live gates
- [Installer](installer.md) — Offline installation
- [Docker Compose](docker-compose.md) — Local dev environment
- [Database Migrations](database-migrations.md) — Schema management
- [Release Artifacts](release-artifacts.md) — Release packaging
- Source: `deploy/helm/ani-platform/`