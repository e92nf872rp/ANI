---
type: concept
title: ANI Technology Stack
description: "Technology stack decisions for the KuberCloud ANI platform: languages, runtime, networking, storage, GPU scheduling, and component selection rationale"
tags: [architecture, tech-stack, kubernetes, golang, python, typescript]
---

# ANI Technology Stack

## Platform Languages

| Layer | Language | Why |
|-------|----------|-----|
| **ANI Core** | Go | Unified collaboration, strong typing, good K8s ecosystem integration, single binary deployment |
| **AI Services** | Python | Ecosystem maturity for ML/NLP/LLM (vLLM, LangChain, Milvus SDK, Whisper) |
| **Frontends** | TypeScript (React 18) | Type safety, TDesign component library, TanStack Router for type-safe routing |
| **Operators** | Go | K8s controller-runtime ecosystem, single binary |

## Runtime & Orchestration

- **Container runtime:** containerd 2.x (dockershim removed, mainstream choice)
- **Orchestration:** Kubernetes 1.36 target (Sprint 5 real-provider baseline)
- **Not:** OpenShift (K8s customizations increase XinChuang adaptation difficulty, locks into Red Hat ecosystem)

## Networking

- **Kube-OVN 1.13+** — chosen over Calico/Cilium for native VPC tenant isolation, strong Chinese community (QingCloud), XinChuang hardware support
- **Join subnet:** moved to `172.30.0.0/16` to avoid CGNAT range conflict (`100.64.0.0/10`) with Tailscale/Headscale overlay network
- **External LB:** Kube-OVN with OVN LB provider for external service exposure
- **CNI:** Kube-OVN is the preferred CNI; other CNIs are not actively tested

## Storage

| Type | Component | Port/Adapter |
|------|-----------|-------------|
| Block (RWO) | Rook-Ceph | `StorageProviderRenderer`/`Apply` |
| File (RWX) | Rook-Ceph NFS/CephFS (planned) | `StorageProviderRenderer`/`Apply` |
| Object (S3) | MinIO | `ObjectStore` |
| Vector | Milvus | `VectorStore` / `VectorStoreService` |

## GPU Scheduling

| Component | Role | Integration Point |
|-----------|------|-------------------|
| **Volcano** | Batch scheduling, queue management | `GPUSchedulingQueueStore` port |
| **HAMi** | vGPU sharing (MPS-equivalent per-device partitioning) | Node labels: `ani.kubercloud.io/gpu-mode=vgpu` |
| **NVIDIA device-plugin** | Whole-card GPU discovery | `GPUInventory` port |
| **DCGM** | GPU metrics (nvml, dcgm-exporter) | DCGM collector in metering service |

## Identity & Security

| Component | Role |
|-----------|------|
| **Dex** | OIDC federation (GitHub, enterprise AD/LDAP) |
| **KMS/SM4** | National secret SM4 algorithm for at-rest encryption |
| **K8s Secrets** | Secret material storage and binding to workloads |
| **Harbor** | Container image registry with vulnerability scanning |

## Messaging & Async

| Component | Role |
|-----------|------|
| **NATS JetStream** | Async task queue, outbox event publishing, inference deployment messages |
| **Redis** | Token blocklist, rate-limit counters, sandbox session cache |
| **PostgreSQL (CloudNativePG)** | Primary metadata store, async task persistence, outbox table, RLS enforcement |

## References

- [Architecture Overview](overview.md)
- [Ports and Adapters](ports-and-adapters.md)
- ANI-04: Tech Stack Design (`/ANI-04-技术栈设计.md`)
- ANI-07: Deployment Engineering (`/ANI-07-部署工程设计.md`)
- Component evaluation matrices in `deploy/helm/ani-platform/component-contracts/`