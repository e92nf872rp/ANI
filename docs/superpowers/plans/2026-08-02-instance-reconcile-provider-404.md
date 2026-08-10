# Instance Reconcile Provider 404 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure an out-of-band deletion of a Kubernetes workload is persisted as `failed/ProviderResourceLost` instead of leaving ANI Core PostgreSQL in a stale running state.

**Architecture:** Translate only the primary workload observation HTTP 404 into the existing `ports.ErrNotFound` contract at the Kubernetes REST client boundary. Reuse the existing reconcile controller missing-provider transition and prove the complete Core API -> Kubernetes deletion -> reconcile-worker -> PostgreSQL path with an isolated live gate.

**Tech Stack:** Go 1.25, PostgreSQL 17, Kubernetes REST API, Python 3 live-gate validator.

## Global Constraints

- Core only; do not modify Services.
- Do not change `repo/api/openapi/v1.yaml` or existing v1 HTTP semantics.
- Map only primary provider-resource HTTP 404 to `ports.ErrNotFound`; preserve 403, 429, 5xx, timeout, and network errors.
- Repeated reconciliation of an already failed missing-provider instance must remain idempotent.
- Do not commit without explicit user confirmation.

---

### Task 1: Map primary Kubernetes observation 404

**Files:**
- Modify: `repo/pkg/adapters/runtime/kubernetes_rest_client_test.go`
- Modify: `repo/pkg/adapters/runtime/kubernetes_rest_client.go`

**Interfaces:**
- Consumes: `KubernetesRESTClient.Observe(context.Context, ports.WorkloadProviderStatusRequest)`.
- Produces: an error satisfying `errors.Is(err, ports.ErrNotFound)` only for a primary resource 404.

- [x] Add a table-driven observation test for HTTP 404 and non-404 provider failures.
- [x] Run the test and confirm the 404 case fails because it returns `resilience.StatusError`.
- [x] Add the minimal `isKubernetesNotFound` branch in `Observe` and wrap `ports.ErrNotFound` with provider context.
- [x] Run focused Kubernetes REST client tests and confirm all cases pass.

### Task 2: Prove controller persistence and idempotency

**Files:**
- Modify: `repo/pkg/adapters/runtime/reconcile_controller_test.go`

**Interfaces:**
- Consumes: the `ports.ErrNotFound` result from Task 1 and existing `markProviderMissing`.
- Produces: stored `failed/ProviderResourceLost` state with stable repeated reconcile behavior.

- [x] Add a controller test that uses a real `KubernetesRESTClient` behind `KubernetesProviderAdapter` and an HTTP 404 transport.
- [x] Assert first reconcile changes running to failed and persists `ProviderResourceLost`.
- [x] Assert a repeated reconcile remains failed without corrupting state or converting non-404 errors.
- [x] Run focused reconcile tests.

### Task 3: Add and run the provider-loss live gate

**Files:**
- Create: `repo/deploy/real-k8s-lab/instance-reconcile-provider-loss-live-gate.yaml`
- Create: `repo/scripts/validate_instance_reconcile_provider_loss_live_gate.py`
- Create: `repo/scripts/validate_instance_reconcile_provider_loss_live_gate_test.py`
- Create: `repo/development-records/live-evidence/instance-reconcile-provider-loss-live-20260802.json`

**Interfaces:**
- Consumes: Core `/api/v1/instances`, Kubernetes Deployment deletion, reconcile-worker, and PostgreSQL-backed instance reads.
- Produces: sanitized evidence proving `running -> failed/ProviderResourceLost` and provider resource cleanup.

- [x] Define static gate checks and validator contract tests.
- [x] Create a Sandbox through Core and wait for running.
- [x] Delete its Deployment directly through Kubernetes and wait for reconcile-worker to persist provider loss.
- [x] Confirm Core API returns `failed/ProviderResourceLost`, repeat observation is stable, and no workload resources remain.
- [x] Archive evidence without credentials or complete internal endpoints.

### Task 4: Record and verify the feature batch

**Files:**
- Create: `repo/development-records/instance-reconcile-provider-404-a.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

**Interfaces:**
- Consumes: focused tests and live evidence from Tasks 1-3.
- Produces: the four required ANI feature-batch records.

- [x] Record the exact changed boundary, tests, live result, and remaining SandboxRuntime restart risk.
- [x] Run focused Go and Python tests, OpenAPI compatibility, architecture, doc-entrypoint, and live-gate validators.
- [x] Run `PATH=/tmp/ani-pybin:$PATH make test` and `git diff --check`.
- [x] Confirm no token, credential, node IP, or complete internal endpoint appears in evidence.
