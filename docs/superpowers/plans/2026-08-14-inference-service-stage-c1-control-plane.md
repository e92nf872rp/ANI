# Inference Service Stage C1 Control Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the restart-safe ANI Services inference control-plane foundation: domain invariants, PostgreSQL resource/operation persistence, lease claiming, and a fake dependency create-to-running reconciliation loop.

**Architecture:** `services/inference-service` owns Services business state and depends only on internal `ModelCatalog` and `InferenceRuntime` interfaces. PostgreSQL is the authority for resource state, generations, idempotency, and operation leases; fake catalog/runtime implementations prove the state machine without Kubernetes or Core imports. Gateway HTTP delegation and the generated Core SDK adapter remain separate later batches.

**Tech Stack:** Go 1.25, PostgreSQL 17, pgx/v5, UUID, standard-library `testing`, repository migration/architecture gates.

## Global Constraints

- Work only on local `main`; do not create branches or worktrees.
- Do not import ANI Core packages from `services/inference-service`; the real runtime adapter will consume generated Core SDK artifacts in a later batch.
- Do not delete dormant inference gRPC, CRD, or operator code unless it causes a demonstrated build/runtime conflict.
- PostgreSQL is authoritative; no NATS/Kafka queue is introduced.
- Tenant-owned reads and writes use transaction-scoped `app.current_tenant_id`; worker lease operations use an explicit platform transaction path and still carry `tenant_id` in every update predicate.
- `runtime_ref` and `runtime_endpoint` remain internal and never enter Services response DTOs.
- No Gateway handler, Kubernetes object, LWS, vLLM runtime, invocation gateway, or live-ready claim belongs in C1.
- Commit and push require a separate explicit user instruction.

---

### Task 1: Scaffold the isolated service module and freeze domain invariants

**Files:**
- Create: `services/inference-service/go.mod`
- Create: `services/inference-service/internal/domain/resource.go`
- Create: `services/inference-service/internal/domain/transition.go`
- Create: `services/inference-service/internal/domain/transition_test.go`
- Modify: `go.work`

**Interfaces:**
- Produces: `domain.Service`, `domain.Spec`, `domain.Operation`, `domain.Action`, `domain.BeginTransition(Service, Action, Spec, uuid.UUID) (TransitionResult, error)`；result 显式区分新建 operation、复用真实 operation ID 和 already-desired no-op，禁止合成不完整任务。
- Enforces: status/action matrix, generation increment, stop/delete preemption, one active generation, and no GPU inference from deprecated nullable fields.

- [x] **Step 1: Write failing transition tests**

Cover at least these independent behaviors:

```go
func TestBeginTransitionScaleRequiresRunning(t *testing.T)
func TestBeginTransitionStopPreemptsCreate(t *testing.T)
func TestBeginTransitionDeleteCannotBeReversed(t *testing.T)
func TestSpecUsesAcceleratorPresenceInsteadOfLegacyGPUFields(t *testing.T)
```

- [x] **Step 2: Verify RED**

Run: `go test ./services/inference-service/internal/domain -count=1`

Expected: FAIL because the module/domain package does not exist.

- [x] **Step 3: Implement the minimum domain model**

Use explicit string types for `Status`, `DesiredState`, `Action`, and `OperationState`. `BeginTransition` must copy the resource, increment `Generation` exactly once for a new desired state/spec, create a pending operation targeting that generation, and reject invalid transitions with stable sentinel errors.

- [x] **Step 4: Verify GREEN**

Run: `go test ./services/inference-service/internal/domain -count=1`

Expected: PASS.

### Task 2: Add the additive PostgreSQL control-plane migration

**Files:**
- Create: `deploy/migrations/20260814_001_inference_control_plane.sql`
- Create: `scripts/validate_inference_control_plane_migration.py`
- Create: `scripts/validate_inference_control_plane_migration_test.py`
- Modify: `Makefile`

**Interfaces:**
- Consumes: the legacy `inference_services` table from `20260501_001_init_schema.sql`.
- Produces: additive normalized service columns and `inference_operations` with the unique key `(tenant_id, operation_scope, idempotency_key)`.

- [x] **Step 1: Write a failing migration contract test**

Assert the migration contains normalized JSONB snapshots, desired/applied generation fields, internal runtime references, tombstone timestamps, operation lease/retry/result/error fields, partial active-name indexes, and restrictive tenant RLS. Task 3 separately freezes the `FOR UPDATE SKIP LOCKED` claim query.

- [x] **Step 2: Verify RED**

Run: `python3 scripts/validate_inference_control_plane_migration_test.py`

Expected: FAIL because the migration is absent.

- [x] **Step 3: Write an additive idempotent migration**

Preserve legacy columns. Add new nullable/defaulted columns before backfilling safe values, replace the old unconditional `(tenant_id, name)` constraint with an active-resource partial unique index, create `inference_operations`, and add restrictive tenant RLS with both `USING` and `WITH CHECK`.

- [x] **Step 4: Verify GREEN**

Run: `python3 scripts/validate_inference_control_plane_migration_test.py && python3 scripts/validate_inference_control_plane_migration.py`

Expected: PASS.

### Task 3: Implement atomic resource and operation persistence

**Files:**
- Create: `services/inference-service/internal/repository/store.go`
- Create: `services/inference-service/internal/repository/postgres.go`
- Create: `services/inference-service/internal/repository/postgres_test.go`

**Interfaces:**
- Produces: `Store.CreateWithOperation`, `Store.GetService`, `Store.GetOperation`, `Store.ClaimOperation`, `Store.ApplyObservation`, and `Store.FailOperation`.
- `ClaimOperation(ctx, owner, now, leaseDuration)` returns at most one operation and uses `FOR UPDATE SKIP LOCKED` plus `lease_until`.

- [x] **Step 1: Write failing SQL/behavior tests**

Test exact tenant predicates, atomic create+operation insertion, same-key/same-hash replay, same-key/different-hash conflict, lease expiry takeover, and generation-CAS observation updates.

- [x] **Step 2: Verify RED**

Run: `go test ./services/inference-service/internal/repository -count=1`

Expected: FAIL because the repository does not exist.

- [x] **Step 3: Implement the pgx store**

Keep SQL in repository files, set tenant context inside tenant transactions, and keep platform lease claims separate from tenant request transactions. Every worker update must include both service ID and target generation.

- [x] **Step 4: Verify GREEN**

Run: `go test ./services/inference-service/internal/repository -count=1`

Expected: PASS.

### Task 4: Add dependency ports and the create use case

**Files:**
- Create: `services/inference-service/internal/catalog/catalog.go`
- Create: `services/inference-service/internal/runtime/runtime.go`
- Create: `services/inference-service/internal/service/service.go`
- Create: `services/inference-service/internal/service/service_test.go`

**Interfaces:**
- Consumes: `repository.Store`, `catalog.ModelCatalog.Resolve`, normalized create input.
- Produces: `service.Create(ctx, tenantID, input) (domain.Service, domain.Operation, error)`.

- [x] **Step 1: Write failing create tests**

Cover ready model resolution, model-not-ready rejection before DB mutation, deterministic normalized request hash, same-key replay, name conflict, and CPU/GPU selection based only on accelerator presence.

- [x] **Step 2: Verify RED**

Run: `go test ./services/inference-service/internal/service -count=1`

Expected: FAIL because the use case does not exist.

- [x] **Step 3: Implement the minimum create coordinator**

Freeze the catalog snapshot and execution profile in `desired_spec`; never persist plaintext credentials. Insert the pending service and `inference_service.create` operation through one store call and return immediately without invoking runtime.

- [x] **Step 4: Verify GREEN**

Run: `go test ./services/inference-service/internal/service -count=1`

Expected: PASS.

### Task 5: Implement a lease worker and fake create-to-running reconciliation

**Files:**
- Create: `services/inference-service/internal/reconcile/worker.go`
- Create: `services/inference-service/internal/reconcile/worker_test.go`
- Create: `services/inference-service/internal/runtime/fake/fake.go`
- Create: `services/inference-service/internal/catalog/fake/fake.go`

**Interfaces:**
- Consumes: `repository.Store.ClaimOperation`, `runtime.InferenceRuntime`.
- Produces: `Worker.RunOnce(ctx) (bool, error)` and deterministic fake dependencies.

- [x] **Step 1: Write failing worker tests**

Cover one-winner lease behavior, deterministic runtime idempotency key from service ID + generation, immediate `runtime_ref` persistence, stale-generation callback suppression, ready+health+smoke transition to running, and retryable dependency failure without a new generation.

- [x] **Step 2: Verify RED**

Run: `go test ./services/inference-service/internal/reconcile -count=1`

Expected: FAIL because the worker does not exist.

- [x] **Step 3: Implement one-operation reconciliation**

`RunOnce` claims one due operation, creates/replays the fake runtime, observes readiness, performs bounded health/smoke hooks, and applies success using generation CAS. Retryable failures increment attempt/next-attempt while retaining the original target generation and Core idempotency key.

- [x] **Step 4: Verify GREEN and race safety**

Run: `go test -race ./services/inference-service/internal/reconcile -count=1`

Expected: PASS.

### Task 6: Close the C1 batch without claiming runtime readiness

**Files:**
- Modify: `development-records/README.md`
- Create: `development-records/inference-service-control-plane-c1.md`
- Modify: `CURRENT-SPRINT.md`
- Modify: `../ANI-06-开发计划.md`

**Interfaces:**
- Produces: truthful C1 completion evidence and the next-batch boundary.

- [x] **Step 1: Run focused and repository gates**

Run:

```text
go test ./services/inference-service/... -count=1
python3 scripts/validate_inference_control_plane_migration_test.py
python3 scripts/validate_inference_control_plane_migration.py
INFERENCE_TEST_DATABASE_URL=... make test-inference-control-plane-postgres  # 门禁已实现；按用户确认在完整服务链路形成后连接隔离 PG 执行
PATH=/tmp/ani-pybin:$PATH make test
PATH=/tmp/ani-pybin:$PATH make validate-architecture
git diff --check
```

Expected: all commands exit 0.

- [x] **Step 2: Update feature-batch records**

State exactly that C1 proves domain/PG/fake-dependency control-plane logic. Do not claim Gateway delegation, real Core adapter, Deployment/LWS, cluster-internal inference, or runtime readiness.

- [x] **Step 3: Review and stop**

Confirm only C1 files and this plan are changed. Stop for explicit commit/push approval; do not start HTTP/Gateway or real Core work in the same unapproved batch.
