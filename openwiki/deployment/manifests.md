---
type: reference
title: M1 Contract Manifests
description: "M1 contract manifests in deploy/manifests/ (60+ YAML files across 24 subdirectories): contract profiles, guardrails, state machines, validation templates organized by contract domain (instance, infra, runtime, GPU, E2E)."
tags: [deployment, manifests, contract, validation, m1]
---

# M1 Contract Manifests

**Directory**: `deploy/manifests/` · **Contents**: 60+ YAML files across 24 subdirectories

## Structure

The manifests define the M1 contract profiles used by architecture validation gates:

| Subdirectory | Domain | Key Files |
|-------------|--------|-----------|
| `m1-instance-a/` | Instance object contract | `00-instance-object-contract.yaml`, `10-instance-network-plan.yaml`, `20-instance-storage-plan.yaml`, `30-instance-examples.yaml` |
| `m1-instance-b/` | Instance planner | `00-instance-planner-contract.yaml`, `10-instance-planner-state-machine.yaml` |
| `m1-instance-c/` | Provider renderer | `00-provider-renderer-contract.yaml`, `10-provider-rendering-examples.yaml` |
| `m1-instance-d/` | Admission guardrails | `00-admission-guardrail-contract.yaml`, `10-server-side-dry-run-policy.yaml` |
| `m1-instance-e/` | Plan audit | `00-plan-audit-contract.yaml`, `10-audit-flow-contract.yaml` |
| `m1-instance-f/` | Provider dry-run | `00-provider-dry-run-contract.yaml`, `10-provider-dry-run-flow.yaml` |
| `m1-instance-g/` | Provider apply gate | `00-provider-apply-gate-contract.yaml`, `10-provider-apply-flow.yaml` |
| `m1-instance-h/` | Status reconcile | `00-status-reconcile-contract.yaml`, `10-status-phase-mapping.yaml` |
| `m1-instance-i/` | Provider status reader + orchestrator | `00-provider-status-reader-contract.yaml`, `10-instance-orchestrator-contract.yaml` |
| `m1-instance-j/` | Instance store + query | `00-instance-store-contract.yaml`, `10-instance-query-contract.yaml` |
| `m1-instance-k/` | Provider adapter | `00-provider-adapter-contract.yaml`, `10-provider-adapter-flow.yaml` |
| `m1-instance-l/` | Instance service API | `00-instance-service-api-contract.yaml`, `10-instance-service-flow.yaml` |
| `m1-instance-m/` | Instance lifecycle + ops API | `00-instance-lifecycle-api-contract.yaml`, `10-instance-ops-api-contract.yaml` |
| `m1-instance-n/` | K8s provider execution profile | `00-kubernetes-provider-execution-profile.yaml`, `10-real-cluster-readiness.yaml` |
| `m1-instance-o/` | K8s REST client profile | `00-kubernetes-rest-client-profile.yaml`, `10-kubernetes-rest-client-flow.yaml` |
| `m1-instance-p/` | K8s bootstrap wiring | `00-kubernetes-bootstrap-wiring.yaml` |
| `m1-instance-q/` | K8s lifecycle execution | `00-kubernetes-lifecycle-execution.yaml` |
| `m1-instance-r/` | K8s ops execution | `00-kubernetes-ops-execution.yaml` |
| `m1-runtime-a/` | Workload runtime contracts | `00-workload-runtime-contract.yaml`, `10-runtime-class-contract.yaml`, `20-instance-schema.yaml` |
| `m1-infra-a/` through `m1-infra-f/` | Infrastructure contracts | Namespaces, platform config, network policy, service accounts, tenant namespaces, GPU infrastructure |
| `m1-gpu-a/` | GPU contracts | Heterogeneous GPU contract, node class schema, runtime compatibility matrix |
| `m1-e2e/` | E2E profile | Instance E2E profile, guardrails |
| `m1-e2e-b/` | Real provider regression profile | Real provider regression test profile |
| `m1-sandbox-kata/` | Sandbox runtime | Kata container deploy values |

## Purpose

Each subdirectory contains numbered files (00 = primary contract, 10+ = detailed contracts/flows) that define the expected behavior for a specific capability. These are consumed by `make validate-instance-*` and `make validate-sprint*` gates.

## References

- [Architecture Baselines](../ci-cd/architecture-baselines.md) — Baseline enforcement
- [Validation Gates](../ci-cd/validation-gates.md) — Gate catalog
- [Helm Charts](helm-charts.md) — Deployment packaging