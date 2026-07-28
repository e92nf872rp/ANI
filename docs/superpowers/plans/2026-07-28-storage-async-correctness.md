# Storage Async Correctness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make storage asynchronous responses pollable and contract-complete, enforce filesystem mount preconditions, and prevent concurrent idempotent creates from duplicating provider operations.

**Architecture:** Keep the existing Core OpenAPI and local runtime boundaries. Add a tenant-scoped in-memory task registry at the Gateway router boundary for completed local tasks, declare the already-required `Location` response header, and serialize only same-key create operations inside the local storage service.

**Tech Stack:** Go 1.25, Hertz, OpenAPI 3, existing ANI ports/adapters and Go test suites.

## Global Constraints

- Preserve `repo/api/openapi/v1.yaml` as the Core contract source of truth.
- Do not introduce a new task service, database table, provider, or dependency.
- Keep `docs/superpowers/specs/2026-07-28-instance-management-design.md` untouched.
- Write and observe failing regression tests before production changes.
- Do not commit or push without explicit user instruction.

---

### Task 1: OpenAPI Location Headers

**Files:**
- Modify: `repo/api/openapi/v1.yaml`
- Test: existing OpenAPI validation scripts invoked by `repo/Makefile`

**Interfaces:**
- Consumes: global async contract requiring `Location: /api/v1/tasks/{task_id}`.
- Produces: explicit `headers.Location` declarations on every affected storage and vector-store `202` response.

- [x] **Step 1: Run a structural check that fails while affected 202 responses omit `Location`.**

Run the existing OpenAPI validation and inspect the named operations with a YAML parser.

- [x] **Step 2: Add `Location` header declarations.**

For each affected `202`, add:

```yaml
headers:
  Location:
    schema: { type: string }
    description: 任务轮询 URL
```

- [x] **Step 3: Run contract gates.**

Run `make validate-openapi-spec` and `make validate-core-api-compatibility`; expect exit code 0.

### Task 2: Pollable Local Async Tasks

**Files:**
- Modify: `repo/services/ani-gateway/internal/router/task_resources.go`
- Modify: `repo/services/ani-gateway/internal/router/storage_resources.go`
- Modify: `repo/services/ani-gateway/internal/router/vector_store_resources.go`
- Test: `repo/services/ani-gateway/internal/router/task_resources_test.go`
- Test: `repo/services/ani-gateway/internal/router/storage_resources_test.go`

**Interfaces:**
- Consumes: `storageSnapshotTaskResponse` emitted by completed local storage/vector operations.
- Produces: tenant-scoped `storeCompletedTask(tenantID, task)` and `GET /tasks/:task_id` returning the same valid task or 404.

- [x] **Step 1: Add failing tests.**

Test that an accepted storage task can be fetched by ID, cannot be fetched by another tenant, unknown IDs return 404, and the body contains all required `AsyncTask` fields.

- [x] **Step 2: Run router tests and observe the expected failure.**

Run `go test ./services/ani-gateway/internal/router -run 'Test.*Task' -count=1`.

- [x] **Step 3: Implement the minimal tenant-scoped in-memory registry.**

Register completed tasks immediately before writing each accepted response and make `getTask` read that registry instead of returning `status: unknown`.

- [x] **Step 4: Run router tests and expect exit code 0.**

### Task 3: Filesystem Preconditions

**Files:**
- Modify: `repo/pkg/adapters/runtime/storage_service.go`
- Test: `repo/pkg/adapters/runtime/storage_service_test.go`

**Interfaces:**
- Consumes: existing mount targets and `ports.StorageFilesystemMountRequest` / `StorageFilesystemUnmountRequest`.
- Produces: `ports.ErrFailedPrecondition` when no available target exists and `ports.ErrInvalid` when unmount lacks `instance_id`.

- [x] **Step 1: Add one failing test for mount without an available target.**
- [x] **Step 2: Run the focused test and observe it fail because mount currently succeeds.**
- [x] **Step 3: Reject mount unless an available target with a non-empty IP belongs to the filesystem.**
- [x] **Step 4: Run the focused test and expect it to pass.**
- [x] **Step 5: Add one failing test for empty unmount `instance_id`.**
- [x] **Step 6: Run it and observe it fail because unmount currently succeeds.**
- [x] **Step 7: Validate `instance_id` before taking the service lock.**
- [x] **Step 8: Run both focused tests and expect exit code 0.**

### Task 4: Concurrent Idempotent Creates

**Files:**
- Modify: `repo/pkg/adapters/runtime/storage_service.go`
- Test: `repo/pkg/adapters/runtime/storage_service_test.go`

**Interfaces:**
- Consumes: existing idempotency maps and provider renderer/executor.
- Produces: one provider execution and one returned resource identity for concurrent calls sharing tenant and idempotency key.

- [x] **Step 1: Add a blocking-provider concurrent volume-create regression test.**
- [x] **Step 2: Run it and observe duplicate provider calls and resource IDs.**
- [x] **Step 3: Add per-operation in-flight coordination to volume, filesystem, mount-target, and object-upload create paths.**
- [x] **Step 4: Ensure deferred release wakes waiters and permits retry after failure.**
- [x] **Step 5: Run focused tests with `-race` and expect one backend operation per key.**

### Task 5: Full Verification

**Files:**
- No additional files.

**Interfaces:**
- Consumes: all changes above.
- Produces: clean local verification evidence.

- [x] **Step 1: Run focused runtime and router tests.**
- [x] **Step 2: Run `PATH=/tmp/ani-pybin:$PATH make test`.**
- [x] **Step 3: Run `PATH=/tmp/ani-pybin:$PATH make validate-architecture`.**
- [x] **Step 4: Run `git diff --check` and inspect `git status --short`.**
- [x] **Step 5: Report changed files and verification without committing.**
