# Model Repository P0 Backend Implementation Plan

## Goal

Implement the first usable backend loop for the existing model-repository contract: a tenant can register a model, register a verified version, obtain an upload URL, list/filter models and versions, and later resolve an owned ready version for inference. Remote ModelScope/Hugging Face import remains explicitly deferred.

## Constraints

- Work on `main`; preserve the existing unstaged `repo/api/openapi/services/v1.yaml` description-only change.
- Use additive protobuf evolution and regenerate only intended generated files.
- Keep tenant isolation and idempotency fail-closed.
- Do not import shared `pkg/ports` or adapters from model-service internal production packages; use a local interface and structural wiring.
- Keep existing PVC-backed laboratory inference behavior working.
- No live cluster, database, secret, image, commit, or push action is part of this implementation unless separately approved.

## Tasks

### 1. Extend the internal model RPC contract

Add idempotency fields, list filters, and a paginated `ListModelVersions` RPC to `repo/api/proto/model/v1/model_service.proto`. Add only additive fields/RPCs, regenerate protobuf output, and verify unrelated generated files remain unchanged. Write focused compile/behavior tests first and capture RED before implementation.

### 2. Persist idempotency and protect referenced models

Add a forward migration for model mutation idempotency records (tenant-scoped request hash/result) and repository/service behavior for replay versus hash conflict. Make delete fail with a stable `MODEL_IN_USE` error when an inference service references a model version. Cover RLS/tenant predicates, concurrent replay, and rollback behavior with unit tests and an optional integration test that skips when no test DSN is configured.

### 3. Add verified object-storage registration

Introduce a model-service-local object-store interface. Wire the existing bootstrap object store structurally from `main`, without adding forbidden Services-to-shared-adapter imports in internal packages. Implement upload URL generation and version registration checks: deterministic tenant-owned object keys, authoritative size/checksum from `StatObject`, ready only after verification, and fail-closed object-store errors. Preserve `pvc://` registration for the current lab path.

### 4. Complete Gateway model repository endpoints

Extend the Gateway gRPC client and handlers for list filters/pagination, model versions, and `POST /models/{model_id}/upload-url`. Forward idempotency keys and map stable errors. Never proxy model bytes through Gateway. Keep remote import returning the documented not-implemented response until an async worker exists. Add focused route/DTO tests.

### 5. Resolve object-backed versions for inference

Extend the inference catalog adapter to resolve an owned, ready object-backed version through an internal download descriptor while keeping the public model name unchanged and retaining PVC support. Reject cross-tenant, deleted, unready, missing-object, and unsupported-task cases. Add resolver tests for both object and PVC paths.

### 6. Record the feature and run gates

Add a development record and update `README.md`, `CURRENT-SPRINT.md`, and `ANI-06-开发计划.md` with the delivered P0 scope and explicit remote-import deferral. Run focused tests, full tests, race/vet/build checks, architecture/services validators, and `git diff --check`. Report any environment-only skips separately.

## Verification commands

```text
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
PATH=/tmp/ani-pybin:$PATH make test
PATH=/tmp/ani-pybin:$PATH make validate-services
make validate-architecture
git diff --check
```

If `INFERENCE_TEST_DATABASE_URL` is absent, database integration tests must be reported as skipped rather than silently treated as passing.
