---
type: reference
title: Validation Gates
description: "Validation gate catalog: architecture gate, services boundary gate, live gate catalog (Sprint 13 S01-S07, Sprint 14 resilience), guard series (299 guards with append-only index), SDK drift detection. Gate-to-command mapping."
tags: [ci-cd, validation, gates, live-gates, guard-series]
---

# Validation Gates

## Architecture Gate

| Command | Purpose |
|---------|---------|
| `make validate-architecture` | Check port/import purity, coupling levels, architecture baselines 1-2 |

## Services Boundary Gate

| Command | Purpose |
|---------|---------|
| `make validate-services` | API split, Services boundary, route contract, semantic contract, baseline enforcement |

## Live Gates

### Sprint 13 Production-Shaped Gates (S01-S07)

| Gate | Token | Evidence |
|------|-------|----------|
| S01 Kube-OVN Network | SPRINT13-NETROUTE-KUBEOVN-A-TRACK | sprint13-netroute-kubeovn-live-result.md |
| S02 vCluster Workloads | SPRINT13-K8S-WORKLOADS-VCLUSTER-A-TRACK | sprint13-k8s-workloads-vcluster-live-result.md |
| S03 Rook-Ceph Storage | SPRINT13-STORAGE-ROOK-CEPH-A-TRACK | sprint13-storage-rook-ceph-live-result.md |
| S04 GPU/DCGM | SPRINT13-GPU-INVENTORY-DCGM-A-TRACK | sprint13-gpu-inventory-dcgm-live-result.md |
| S05 MinIO Object Store | SPRINT13-OBJECTSTORE-MINIO-A-TRACK | sprint13-objectstore-minio-live-result.md |
| S06 Milvus Vector Store | SPRINT13-VECTOR-MILVUS-A-TRACK | sprint13-vector-milvus-live-result.md |
| S07 Instance Observability | SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK | sprint13-instance-observability-prometheus-live-result.md |

### Sprint 14 Resilience Live Gate

| Token | Evidence |
|-------|----------|
| SPRINT14-CORE-RESILIENCE-LIVE-GATE | r-sprint14-resilience-live-gate.md |

### Provider-Specific Live Gates

| Provider | Manifest | Evidence |
|----------|----------|----------|
| vCluster | deploy/real-k8s-lab/vcluster-live-gate.yaml | m1-k8s-live-a-vcluster-live-gate.md |
| vCluster Upgrade | deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml | m1-k8s-live-c-vcluster-upgrade-live-gate.md |
| Node Pool | deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml | m1-k8s-live-b-node-pool-live-gate.md |
| Kube-OVN Network | deploy/real-k8s-lab/kubeovn-network-live-gate.yaml | m1-network-live-a-kubeovn-network-live-gate.md |
| KubeVirt VM | deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml | m1-kubevirt-live-a-vm-live-gate.md |
| HA Reconcile | deploy/real-k8s-lab/reconcile-ha-live-gate.yaml | m1-reconcile-live-a-ha-live-gate.md |
| KMS/SM4 | deploy/real-k8s-lab/kms-sm4-live-gate.yaml | m1-encrypt-live-a-kms-sm4-live-gate.md |
| Secrets | deploy/real-k8s-lab/secrets-live-gate.yaml | m1-secrets-live-a-secret-live-gate.md |
| Storage (Rook-Ceph) | deploy/real-k8s-lab/storage-live-gate.yaml | sprint13-storage-rook-ceph-live-result.md |
| GPU Inventory | deploy/real-k8s-lab/gpu-inventory-live-gate.yaml | sprint13-gpu-inventory-dcgm-live-result.md |
| GPU Scheduling | deploy/real-k8s-lab/queue-crud-live-gate.yaml | gpu-scheduling-issue-*.md |
| Object Store (MinIO) | deploy/real-k8s-lab/object-store-live-gate.yaml | sprint13-objectstore-minio-live-result.md |
| Vector Store (Milvus) | deploy/real-k8s-lab/vector-store-live-gate.yaml | sprint13-vector-milvus-live-result.md |
| Instance Observability | deploy/real-k8s-lab/instance-observability-live-gate.yaml | sprint13-instance-observability-prometheus-live-result.md |
| Registry (Harbor) | deploy/real-k8s-lab/registry-harbor-live-gate.yaml | sprint13-registry-harbor-live-gate.md |
| Instance Management | deploy/real-k8s-lab/instance-management-live-gate.yaml | instance-management-live-gate-a.md |
| Sandbox | deploy/real-k8s-lab/instance-sandbox-live-gate.yaml | instance-sandbox-file-safety-live-gate-a.md |

## Guard Series

**Index**: `repo/development-records/guard-series/REAL-K8S-LAB-guard-index.md`

| Convention | Value |
|------------|-------|
| Guard ID pattern | M1-REAL-LAB-{letter} (B through KX across multiple series) |
| Categories | infra, env, summary-report, evidence, live-check-profile, contract-gate, path, kubeconfig, live-command |
| Total guards | 299 (as of latest count) |
| Update rule | Append-only: new guard adds one row to index file, no individual batch record |

Each guard maps to a specific validator script or test. The append-only convention ensures traceability without per-guard documentation overhead.

## SDK Drift Gates

| Gate | Purpose |
|------|---------|
| validate-sdk-alpha | SDK Alpha baseline validation |
| validate-sdk-beta | SDK Beta baseline validation |
| validate-sdk-mock-smoke-(A|B|C|D) | SDK mock server smoke tests |

## References

- [Architecture Baselines](architecture-baselines.md) — Baseline YAML files
- [Build System](build-system.md) — Makefile integration
- [GitHub Workflows](github-workflows.md) — CI pipeline
- [Development Records](../development/development-records.md) — Live gate evidence
- [E2E Tests](e2e-tests.md) — Integration test suite