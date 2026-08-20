---
type: reference
title: Real K8s Lab
description: "Real K8s provider lab (deploy/real-k8s-lab/): production-shaped live gate manifests and evidence JSON, profile configuration, live gate catalog (Kube-OVN, vCluster, KubeVirt, HA reconcile, KMS/SM4, secrets, Rook-Ceph, GPU/DCGM, MinIO, Milvus, Prometheus, Harbor, instance orchestration, sandbox, checkpoint), Sprint 13 S01-S07 evidence, Sprint 14 resilience evidence."
tags: [deployment, live-gate, real-provider, k8s, evidence, production-shaped]
---
# Real K8s Lab

## Overview

**Directory**: `deploy/real-k8s-lab/`

The real K8s lab contains the production-shaped live gate manifests and evidence JSON that validate ANI Core against real infrastructure providers. This is the authoritative source for determining whether a capability has passed production-shaped acceptance.

## Structure

```
deploy/real-k8s-lab/
├── profile.yaml                          # Lab profile — selects namespace, credentials, gate list
├── <gate-name>-live-gate.yaml            # Gate definition: target, validation steps, evidence specs
├── <gate-name>-live-gate-evidence.json   # Gate evidence: timestamp, status, proof items (committed separately)
├── <gate-name>-deps.yaml                 # Gate dependency manifests (shared infrastructure)
├── <gate-name>-live-result.yaml          # Sprint-level result summary
├── sprints/
│   └── sprint13/                         # Sprint 13 b-track production-shaped evidence
└── live-evidence/                        # Historical evidence JSON (committed)
```

## Live Gates Catalog

| Gate | Provider | Sprint Gate | Status |
|------|----------|-------------|--------|
| `kubeovn-network-live-gate` | Kube-OVN | S01 | ✅ Passed |
| `vcluster-live-gate` | vCluster | S02 | ✅ Passed |
| `vcluster-upgrade-live-gate` | vCluster | S02 | ✅ Passed |
| `kubevirt-vm-live-gate` | KubeVirt | S02 | ✅ Passed |
| `rook-ceph-storage-live-gate` | Rook-Ceph | S03 | ✅ Passed |
| `gpu-inventory-live-gate` | NVIDIA DCGM | S04 | ✅ Passed |
| `gpu-scheduling-smoke-live-gate` | Volcano + HAMi | S04 | ✅ Passed |
| `object-store-live-gate` | MinIO | S05 | ✅ Passed |
| `vector-store-live-gate` | Milvus | S06 | ✅ Passed |
| `instance-observability-live-gate` | Prometheus/Loki | S07 | ✅ Passed |
| `registry-harbor-live-gate` | Harbor | S07 | ✅ Passed |
| `instance-management-live-gate` | K8s provider | — | ✅ Passed |
| `instance-sandbox-live-gate` | K8s sandbox | — | ✅ Passed |
| `instance-sandbox-stateless-live-gate` | K8s sandbox | — | ✅ Passed |
| `instance-sandbox-checkpoint-live-gate` | K8s sandbox | — | ✅ Passed |
| `instance-reconcile-provider-loss-live-gate` | K8s reconciler | — | ✅ Passed |
| `instance-orchestration-live-gate` | K8s orchestrator | — | ✅ Passed |
| `kms-sm4-live-gate` | KMS/SM4 | — | ✅ Passed |
| `secrets-live-gate` | K8s Secrets | — | ✅ Passed |
| `reconcile-ha-live-gate` | HA reconciler | — | ✅ Passed |
| `sprint14-resilience-live-gate` | Resilience | Sprint 14 | ✅ Passed |

## Evidence Format

```json
{
  "gate": "string — gate ID",
  "timestamp": "string — ISO 8601 UTC",
  "status": "string — passed | failed | pending",
  "evidence": {
    "name": "string — evidence item name",
    "condition": "string — what was validated",
    "proof": "string — diagnostic output or reference",
    "passed": true
  },
  "profile": { "name": "string", "version": "string" }
}
```

## Execution

Gates are run via `make validate-<gate-name>`. The `make validate-sprint13-b-track-production-shape` aggregate target runs all Sprint 13 gates.

## References

- [Validation Gates](../ci-cd/validation-gates.md) — Full validation gate catalog
- [Architecture Baselines](../ci-cd/architecture-baselines.md) — Governance baselines
- [Database Migrations](database-migrations.md) — Schema migrations
- [Release Artifacts](release-artifacts.md) — Release packaging
- Source: `deploy/real-k8s-lab/`