# Model Repository Remote Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the existing `/api/v1/svc/models/import` operation import public ModelScope and Hugging Face revisions into ANI MinIO, without changing the v1 OpenAPI or protobuf contracts.

**Architecture:** The Gateway maps the already-published import request to model-service. Model-service atomically creates the tenant-scoped model/import task and async task record. A separate model-import worker consumes the existing `ani.tasks.model.import` event, downloads only public HTTPS repository files, writes a deterministic tar archive to MinIO, and finalizes a verified model version. The existing MinIO-to-PVC materialization path recognizes the internal archive suffix and safely extracts the package before vLLM starts. Private-repository credentials and a public artifact-layout field remain deferred to a later v1 contract batch.

**Tech Stack:** Go 1.25, existing gRPC/protobuf, PostgreSQL/RLS, NATS JetStream, `pkg/ports.ObjectStore`, MinIO, standard-library HTTPS, deterministic tar+gzip archives.

## Global Constraints

- Do not modify `repo/api/openapi/v1.yaml`, `repo/api/openapi/services/v1.yaml`, or any protobuf contract in this batch.
- Support only public repositories; reject credential fields, non-HTTPS source URLs, redirects, path traversal, and repositories outside the allowlisted source hosts.
- Preserve tenant isolation, idempotency replay/conflict, existing PVC-backed versions, and existing object-backed versions.
- Never log or persist access tokens, signed URLs, raw repository file URLs, database credentials, or object-store credentials.
- The worker must download into a bounded temporary workspace, verify limits, produce deterministic archive bytes, and upload through `ports.ObjectStore`.
- The archive is an internal implementation convention (`model.tar.gz` object key); it is not a new public v1 field.
- Every production behavior change starts with a focused failing test; run Go tests with a writable `GOCACHE`.
- Do not apply live migrations, deploy images, push, or commit without explicit authorization.

---

### Task 1: Wire the existing import endpoint through Gateway

**Files:**
- Modify: `repo/services/ani-gateway/internal/router/model_grpc_client.go`
- Modify: `repo/services/ani-gateway/internal/router/model_resources.go`
- Modify: `repo/services/ani-gateway/internal/router/model_resources_test.go`

**Interfaces:**
- Add `ImportModel(context.Context, string, *modelv1.ImportModelRequest) (*commonv1.AsyncTaskRef, error)` to `ModelServiceClient`.
- Parse the existing flat JSON fields `source`, `repo_id`, `revision`, `idempotency_key`, and optional `webhook_url`.
- Inject the authenticated tenant ID into the gRPC request and return HTTP 202 with `Location: /api/v1/tasks/{task_id}`.

- [ ] **Step 1: Write the failing tests** for a valid Hugging Face request, a valid ModelScope request, missing source/repo/idempotency fields, unsupported source, and gRPC error mapping.
- [ ] **Step 2: Run** `cd repo/services/ani-gateway && GOCACHE=/tmp/ani-import-gateway-red GOWORK=off go test ./internal/router -run TestImportModel -count=1`; confirm the tests fail because the handler is still a 501 stub and the client lacks `ImportModel`.
- [ ] **Step 3: Implement the client and handler** using only the existing contract fields; validate source against `huggingface|modelscope`, trim repository/revision, default revision to `main`, and never accept a token field.
- [ ] **Step 4: Run** `GOCACHE=/tmp/ani-import-gateway-green GOWORK=off go test ./internal/router -run TestImportModel -count=1` and `go vet ./...`; expect PASS.

### Task 2: Atomically create the import task and async task

**Files:**
- Modify: `repo/services/model-service/internal/repo/model_repo.go`
- Modify: `repo/services/model-service/internal/service/model_service.go`
- Modify: `repo/services/model-service/internal/service/model_service_test.go`
- Create: `repo/services/model-service/internal/repo/import_repo.go`
- Create: `repo/deploy/migrations/20260902000400_model_import_task_fencing.sql`

**Interfaces:**
- `CreateImport(ctx context.Context, tx pgx.Tx, req CreateImportRequest) (*ImportTask, bool, error)` creates/replays the tenant model, `model_import_tasks`, and `async_tasks` rows in one transaction.
- `ImportTask` contains only tenant/model/task IDs, source, repo ID, revision, and immutable target object identity; it never stores credentials or signed URLs.
- Existing task-service `GetTask` can read the returned `async_tasks` row by the `AsyncTaskRef.task_id`.

- [ ] **Step 1: Write the failing repository/service tests** for first create, same-key replay, same-key different-payload conflict, tenant isolation, invalid source/repo, and rollback when either task insert fails.
- [ ] **Step 2: Run** `cd repo/services/model-service && GOCACHE=/tmp/ani-import-repo-red GOWORK=off go test ./internal/repo ./internal/service -run 'Import|ModelImport' -count=1`; capture RED.
- [ ] **Step 3: Add the additive migration** for import-task fencing/retry metadata only if the existing table lacks the required columns; keep the existing source check and RLS policy, and make the migration rerunnable.
- [ ] **Step 4: Implement transaction-bound creation** with a deterministic request hash and an outbox payload on subject `ani.tasks.model.import`; do not publish directly from the request handler.
- [ ] **Step 5: Run** the focused tests, migration validator, race tests, and vet; expect PASS. If `INFERENCE_TEST_DATABASE_URL` is unset, compile the integration test and record the runtime skip.

### Task 3: Add public ModelScope/Hugging Face source adapters and deterministic archive packaging

**Files:**
- Create: `repo/services/model-service/internal/importer/source.go`
- Create: `repo/services/model-service/internal/importer/huggingface.go`
- Create: `repo/services/model-service/internal/importer/modelscope.go`
- Create: `repo/services/model-service/internal/importer/archive.go`
- Create: `repo/services/model-service/internal/importer/source_test.go`
- Create: `repo/services/model-service/internal/importer/archive_test.go`

**Interfaces:**
- `type Source interface { List(context.Context, Repository) ([]RemoteFile, error); Open(context.Context, Repository, string) (io.ReadCloser, int64, error) }`.
- `Repository` contains validated `Source`, `RepoID`, and `Revision`; it contains no credential field.
- `BuildArchive(ctx, Source, Repository, ArchiveLimits) (io.ReadCloser, ArchiveManifest, error)` returns deterministic gzip/tar bytes and a manifest with file count, total size, and SHA-256.

- [ ] **Step 1: Write failing tests** for host allowlists, HTTPS-only URLs, redirect rejection, revision/repo path traversal, non-2xx responses, file-count/size limits, deterministic ordering, duplicate paths, symlink-like metadata, and redacted errors.
- [ ] **Step 2: Run** `cd repo/services/model-service && GOCACHE=/tmp/ani-import-source-red GOWORK=off go test ./internal/importer -count=1`; capture RED.
- [ ] **Step 3: Implement Hugging Face tree resolution** through the public HTTPS Hub API and immutable revision file URLs; allow only `huggingface.co` and its documented API host.
- [ ] **Step 4: Implement ModelScope file-tree resolution** through its public HTTPS model API; allow only `modelscope.cn` and reject redirects to any other host.
- [ ] **Step 5: Implement deterministic tar+gzip packaging** with zeroed timestamps, stable lexical paths, bounded readers, no symlinks/hardlinks, and a manifest hash.
- [ ] **Step 6: Run** focused normal/race/vet tests and verify no test output contains a source URL, token, object key, or request body.

### Task 4: Implement the model-import worker and MinIO finalization

**Files:**
- Create: `repo/services/model-service/internal/importer/worker.go`
- Create: `repo/services/model-service/internal/importer/worker_test.go`
- Create: `repo/services/model-service/cmd/model-import-worker/main.go`
- Modify: `repo/services/model-service/internal/repo/model_repo.go`
- Modify: `repo/services/model-service/go.mod` only if an existing dependency is required

**Interfaces:**
- `Worker.Handle(context.Context, ImportMessage) error` acquires the existing async-task lease, downloads the public revision, writes `model.tar.gz` through `ports.ObjectStore.PutObject`, inserts a verified ready `model_versions` row, and completes the task.
- Failures update both import-task and async-task status with a stable redacted error; retryable dependency failures return an error to NATS for bounded redelivery.
- A repeated delivery with the same task ID is a no-op after the persisted completed state is observed.

- [ ] **Step 1: Write failing worker tests** for success, retry, duplicate delivery, source failure, archive limit failure, MinIO failure, database failure, and cross-tenant message rejection.
- [ ] **Step 2: Run** the focused worker tests and capture RED.
- [ ] **Step 3: Implement lease/fencing and progress updates** using the existing async task semantics; never trust a tenant header over the payload tenant and never construct an object ref from unvalidated input.
- [ ] **Step 4: Upload the archive** to a tenant-scoped `object://models/.../model.tar.gz` key with authoritative size/SHA-256, then create the model version and mark the model ready only after the object-store write succeeds.
- [ ] **Step 5: Add the worker binary** with existing bootstrap/NATS/ObjectStore wiring and graceful signal shutdown; do not alter the model-service gRPC contract.
- [ ] **Step 6: Run** focused/full/race/vet/build tests and `go mod tidy -diff` for the module.

### Task 5: Safely extract imported archives during inference materialization

**Files:**
- Modify: `repo/services/model-fetcher/download.go`
- Modify: `repo/services/model-fetcher/main.go`
- Modify: `repo/services/model-fetcher/download_test.go`
- Modify: `repo/pkg/adapters/runtime/kubernetes_platform_workload_runtime.go`
- Modify: `repo/pkg/adapters/runtime/kubernetes_platform_workload_test.go`
- Modify: `repo/services/inference-service/internal/runtime/coresdk/adapter.go`
- Modify: corresponding focused tests

**Interfaces:**
- Imported archive object keys ending in `/model.tar.gz` are extracted into `/models/<model-version-id>/`; ordinary object versions retain the current single-file path.
- Extraction rejects absolute paths, `..`, symlinks, hardlinks, duplicate files, oversized entries, and archive bombs; extraction occurs in a temporary sibling directory followed by an atomic rename.
- Renderer rewrites `--model` and `--model-path` to the extracted version directory for archive-backed objects and still rejects RWO replicas greater than one.

- [ ] **Step 1: Write failing tests** for archive detection, safe extraction, traversal/symlink/duplicate/limit rejection, restart reuse, renderer directory target, and PVC regression.
- [ ] **Step 2: Run** `cd repo/services/model-fetcher && GOCACHE=/tmp/ani-import-fetcher-red GOWORK=off go test ./... -run 'Archive|Extract|ModelPath' -count=1`; capture RED.
- [ ] **Step 3: Implement bounded extraction** without following links and without logging archive paths or object URLs.
- [ ] **Step 4: Update renderer/SDK target selection** using the existing internal object-key suffix convention; do not add a public materialization field.
- [ ] **Step 5: Run** fetcher/runtime/inference focused tests, race, vet, build, and architecture checks. Sandbox `httptest` IPv6 failures must be rerun externally before claiming full green.

### Task 6: Local gates, deployment wiring, and documentation

**Files:**
- Modify: `repo/Makefile`
- Modify: `repo/deploy/real-k8s-lab/inference-incluster-e2e.yaml` only for the worker image/configuration contract
- Create: `repo/scripts/validate_model_repository_remote_import.py`
- Create: `repo/scripts/validate_model_repository_remote_import_test.py`
- Create: `repo/development-records/model-repository-remote-import.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md` Section zero

- [ ] **Step 1: Write failing validator tests** for public-only source restrictions, no plaintext credentials, worker deployment settings, tenant-scoped object keys, and archive marker consistency.
- [ ] **Step 2: Implement the validator and Make target** without applying resources or reading Secret contents.
- [ ] **Step 3: Run** the validator, `make validate-openapi-spec`, `make validate-services`, `make validate-architecture`, model-service/inference/fetcher tests, and `git diff --check`.
- [ ] **Step 4: Record exact local results and explicit live prerequisites** (authenticated internal gRPC identity, tenant NetworkPolicy, non-empty digest-pinned worker/fetcher images, MinIO Secret, and HTTPS object endpoint).
- [ ] **Step 5: Stop before live execution, commit, or push** unless separately authorized.
