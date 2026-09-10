# Model Repository Inference Materialization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make MinIO-backed model versions from the model repository materialize into tenant PVCs before vLLM starts, while preserving existing PVC-backed inference.

**Architecture:** model-service remains the tenant-fenced metadata/object authority. inference-service resolves an immutable model version and passes a provider-neutral materialization descriptor to Core. Core renders a digest-pinned model-fetcher init-container that obtains a short-lived download URL, downloads to a tenant PVC, verifies size/SHA-256, and gates the main runtime container.

**Tech Stack:** Go 1.25, gRPC/protobuf, Core PlatformWorkload ports, Kubernetes REST renderer, MinIO-compatible signed URLs, Alpine model-fetcher, PostgreSQL/RLS.

## Global Constraints

- Keep `model_version_id` as the only public inference model reference.
- Preserve `pvc://` inference behavior.
- Never log or persist signed URLs, AKs, Bearer tokens, object keys, or database credentials.
- Object-backed versions must fail closed when materialization is unavailable.
- ModelScope/HuggingFace import remains explicitly deferred.
- Use TDD: each production change starts with a failing focused test.
- Do not stage, commit, push, or apply live resources until separately authorized.

---

### Task 1: Freeze materialization descriptor at inference-service boundary

**Files:**
- Modify: `repo/services/inference-service/internal/catalog/catalog.go`
- Modify: `repo/services/inference-service/internal/catalog/modelsvc/adapter.go`
- Modify: `repo/services/inference-service/internal/domain/resource.go`
- Modify: `repo/services/inference-service/internal/service/service.go`
- Test: corresponding `*_test.go` files in catalog and service packages

**Interfaces:**
- Produces `catalog.ModelMaterialization` with tenant ID, model version ID, object reference, expected size, and normalized SHA-256.
- PVC versions continue to produce the existing `ArtifactRef` path.
- Object versions must never expose a signed URL from the catalog.

- [ ] **Step 1: Write failing tests** for object versions producing a frozen descriptor, cross-tenant rejection, missing checksum/size rejection, and PVC compatibility.
- [ ] **Step 2: Run focused tests** with `GOWORK=off go test ./internal/catalog/... ./internal/service/...`; verify failure is limited to missing descriptor behavior.
- [ ] **Step 3: Implement the descriptor and strict validation**; keep `GetModelVersion` as the only model-service lookup and preserve task/profile selection.
- [ ] **Step 4: Run focused tests, race tests, and vet** for catalog/service; verify no signed URL is present in persisted `domain.Spec` or logs.

### Task 2: Add Core PlatformWorkload model materialization contract

**Files:**
- Modify: `repo/pkg/ports/platform_workload.go`
- Modify: `repo/api/openapi/v1.yaml` (`PlatformWorkloadCreateRequest` materialization field; internal Core boundary)
- Modify: `repo/services/inference-service/internal/runtime/coresdk/adapter.go`
- Modify: `repo/services/inference-service/internal/runtime/coresdk/adapter_test.go`
- Modify: `repo/services/ani-gateway/internal/router/platform_workloads.go` to map the additive internal materialization field
- Test: Core OpenAPI/schema and Gateway DTO contract tests

**Interfaces:**
- Adds a provider-neutral `ModelMaterialization` field to `PlatformWorkloadCreateSpec`.
- Core receives object reference, tenant/version identifiers, expected size/checksum, model-service address, and fetcher image digest; it does not receive a signed URL.
- Existing PVC artifact serialization remains unchanged.

- [ ] **Step 1: Write failing tests** asserting object-backed inference sends materialization metadata and never sends `object://` as a silently ignored artifact.
- [ ] **Step 2: Run the focused Core SDK tests** and capture the expected compile/behavior failure.
- [ ] **Step 3: Implement the new port field and strict request mapping**; reject an object-backed version when required materialization fields are absent.
- [ ] **Step 4: Run Core SDK/schema tests, full inference compile, vet, and `git diff --check`.

### Task 3: Implement model-fetcher and exact download verification

**Files:**
- Create: `repo/services/model-fetcher/go.mod`
- Create: `repo/services/model-fetcher/main.go`
- Create: `repo/services/model-fetcher/download.go`
- Create: `repo/services/model-fetcher/download_test.go`
- Create: `repo/services/model-fetcher/Dockerfile`
- Modify: root build targets only if an existing image target pattern requires registration

**Interfaces:**
- Environment/config: tenant ID, model version ID, model-service gRPC address, output directory, expected size, expected SHA-256.
- `Download(ctx, descriptor, client, outputDir) error` performs temp-file download, size/hash checks, fsync, atomic rename, and idempotent reuse.

- [ ] **Step 1: Write failing tests** for successful download, checksum mismatch, size mismatch, redirect rejection, non-2xx response, partial-file cleanup, and matching-file reuse.
- [ ] **Step 2: Run `GOWORK=off go test ./...` in the fetcher module** and confirm RED.
- [ ] **Step 3: Implement the minimal fetcher** with bounded HTTP client, no redirect policy, stable redacted errors, and atomic file replacement.
- [ ] **Step 4: Build the digest-pinned fetcher image** and run tests, race, vet, and `go build`; do not push yet.

### Task 4: Render Kubernetes init-container and PVC gate

**Files:**
- Modify: `repo/pkg/adapters/runtime/kubernetes_platform_workload_runtime.go`
- Modify: `repo/pkg/adapters/runtime/kubernetes_platform_workload_test.go`
- Modify: `repo/pkg/adapters/runtime/kubernetes_platform_workload_capabilities.go` if capability validation is needed
- Modify: `repo/services/ani-gateway/platform_workload_runtime.go` to inject object-store/model-service configuration without leaking credentials

**Interfaces:**
- `renderPlatformWorkloadManifests` renders a shared PVC mount and one init-container only for object-backed model materialization.
- Init-container has no GPU request, read-only config, bounded resources, exact owner labels, and exits before vLLM starts.
- The main container mounts the verified model directory read-only where possible.

- [ ] **Step 1: Write failing renderer tests** for exact init-container image/args/env, shared mount, owner labels, no signed URL literal, and PVC-only regression.
- [ ] **Step 2: Run renderer tests and capture RED.**
- [ ] **Step 3: Implement exact manifest rendering and validation**; reject extra caller fields and object materialization without a configured fetcher.
- [ ] **Step 4: Run renderer full/race/vet/build tests and `kubectl apply --server-side --dry-run=server` against the installed Core schema; record any environment-only CRD/schema mismatch without weakening the manifest.

### Task 5: Wire model-service download authorization and deployment configuration

**Files:**
- Modify: `repo/services/model-service/internal/service/model_service.go` only for missing strict checks/tests
- Modify: `repo/services/inference-service/internal/config/config.go`
- Modify: `repo/services/inference-service/main.go`
- Modify: `repo/deploy/helm/ani-platform/values.yaml` and the inference deployment template/config map
- Test: model-service object-store tests and config/deployment contract tests

**Interfaces:**
- Model-service keeps `requester=init-container` and returns a 30-minute signed URL only after tenant/ready/object checks.
- Inference deployment receives model-service address and fetcher image digest through ConfigMap/image configuration, not hard-coded source code.
- No model-service MinIO SDK import is added to inference-service internals.

- [ ] **Step 1: Write failing tests** for requester enforcement, tenant/object path mismatch, ready-state enforcement, and deployment config presence.
- [ ] **Step 2: Run model-service/config focused tests and capture RED.**
- [ ] **Step 3: Implement strict checks and configuration wiring.**
- [ ] **Step 4: Run model-service/inference focused tests, race, vet, build, architecture, and service validators.

### Task 6: Real MinIO object-backed inference gate

**Files:**
- Create: `repo/deploy/real-k8s-lab/model-repository-inference-materialization-live-gate.yaml`
- Create: `repo/scripts/run_model_repository_inference_materialization_live.py`
- Create: `repo/scripts/validate_model_repository_inference_materialization_live_gate.py`
- Test: local runner/validator tests

**Interfaces:**
- Runner uses existing tenant login/AK context and existing cluster MinIO only; it does not print secrets.
- Gate records upload/register/create/wait/download/hash/restart/cross-tenant results and preserves created resources for inspection.

- [ ] **Step 1: Write failing validator/runner tests** for required probes, no secret output, no direct PVC bypass, and cleanup-preservation behavior.
- [ ] **Step 2: Run local validator tests and capture RED.**
- [ ] **Step 3: Implement the runner and exact evidence redaction.**
- [ ] **Step 4: Run local validator, Python compile, Make targets, and all Go gates.**
- [ ] **Step 5: Only after explicit live approval, upload a small test artifact to MinIO, register it, create the inference service, wait for init-container/vLLM readiness, call Envoy AI Gateway, and verify restart/cross-tenant failures.**

### Task 7: Documentation and handoff

**Files:**
- Modify: `docs/superpowers/specs/2026-08-31-envoy-ai-gateway-tenant-aware-dynamic-publication-design.md` if status text is stale
- Modify: `docs/superpowers/specs/2026-08-27-model-repository-p0-contract-design.md` with implementation status only
- Create: `repo/development-records/model-repository-inference-materialization-20260902.md`

- [ ] **Step 1: Record actual implementation and environment-only skips** (no claim of ModelScope/HuggingFace import).
- [ ] **Step 2: Run `make test`, `make validate-services`, `make validate-architecture`, `make validate-doc-entrypoints`, and `git diff --check`.**
- [ ] **Step 3: Stop before commit/push/PR unless the user explicitly requests shipping.**
