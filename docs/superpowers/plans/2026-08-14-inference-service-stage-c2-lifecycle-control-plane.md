# Inference Service Stage C2 Lifecycle Control Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the C1 create-only foundation into a restart-safe, queryable inference control plane that persists scale/start/stop/restart/delete operations and converges every action through fake dependencies.

**Architecture:** PostgreSQL remains the only authority. A tenant transaction locks one service row, classifies idempotency, applies `domain.BeginTransition`, cancels any preempted operation, and atomically writes the new desired state plus operation. The reconciler dispatches operation intent through the internal `InferenceRuntime` anti-corruption interface; no HTTP ingress, Gateway delegation, Core SDK adapter, Kubernetes object, or real PostgreSQL execution is included in C2.

**Tech Stack:** Go 1.25, PostgreSQL 17 SQL via pgx/v5, UUID, standard-library `testing`, existing ANI repository gates.

## Global Constraints

- Work only on local `main`; do not create branches or worktrees.
- C2 modifies no OpenAPI contract. Core PR #99 and Services PR #101 are already approved contract sources.
- Do not import ANI Core packages, generated Core internals, Kubernetes clients, LWS, Volcano, vLLM, or provider SDKs.
- Do not delete dormant inference gRPC/CRD/operator assets unless a demonstrated build/runtime conflict appears.
- Tenant operations set transaction-local `app.current_tenant_id`; worker mutations remain fenced by tenant/service/operation/generation/lease token.
- Same idempotency key + same normalized request returns the persisted operation; the same key + a different hash returns `ErrIdempotencyConflict`.
- Replaying an active stop/delete loads and returns the real persisted operation. An already-desired start/stop creates a persisted completed no-op operation without increasing service generation.
- Delete completion tombstones the service. Stop completion clears the internal endpoint and ready replicas but retains the stable runtime identity needed by start.
- PostgreSQL integration code may be extended, but connection to the cluster PostgreSQL remains deferred until the complete service chain exists, per user direction.
- Commit, push, and PR require separate explicit user instructions.

---

### Task 1: Persist atomic lifecycle transitions and query surfaces

**Files:**
- Modify: `repo/services/inference-service/internal/repository/store.go`
- Modify: `repo/services/inference-service/internal/repository/postgres.go`
- Modify: `repo/services/inference-service/internal/repository/postgres_test.go`
- Modify: `repo/services/inference-service/internal/repository/postgres_integration_test.go`

**Interfaces:**
- Produces: `MutationRequest`, `MutationResult`, and a narrow `ControlStore` interface with `GetService`, `GetOperation`, `ListServices`, and `MutateService`. Do not widen the existing create/worker test interfaces.
- `MutationResult.Disposition` uses `domain.TransitionCreated`, `domain.TransitionReuseOperation`, or `domain.TransitionAlreadyDesired`; every successful result contains a persisted operation.

- [x] **Step 1: Write failing repository contract tests**

Add independent tests that require:

```go
func TestMutationSQLLocksTenantServiceBeforeTransition(t *testing.T)
func TestMutationSQLCancelsPreemptedOperationBeforeInsert(t *testing.T)
func TestMutationSQLPersistsDesiredStateAndGenerationAtomically(t *testing.T)
func TestListSQLExcludesTombstonesAndInternalEndpointProjection(t *testing.T)
```

The tests must assert `FOR UPDATE`, tenant predicates, active-operation cancellation, operation idempotency identity, `current_operation_id`, and `deleted_at IS NULL`.

- [x] **Step 2: Verify RED**

Run from `repo/services/inference-service`:

```text
GOWORK=off go test ./internal/repository -run 'TestMutation|TestListSQL' -count=1
```

Expected: FAIL because the mutation/list SQL and methods do not exist.

- [x] **Step 3: Implement the transaction contract**

Add these exact request/result shapes:

```go
type MutationRequest struct {
    TenantID       uuid.UUID
    ServiceID      uuid.UUID
    Action         domain.Action
    TargetSpec     domain.Spec
    OperationID    uuid.UUID
    OperationScope string
    IdempotencyKey uuid.UUID
    RequestHash    string
    Now            time.Time
}

type MutationResult struct {
    Service     domain.Service
    Operation   domain.Operation
    Disposition domain.TransitionDisposition
}
```

`MutateService` must take an advisory lock for tenant/scope/key, replay an existing same-hash operation, lock the tenant service row, call `domain.BeginTransition`, and then:

- load the real operation for `TransitionReuseOperation`;
- insert a completed, queryable no-op operation at the current generation for `TransitionAlreadyDesired`;
- cancel `PreemptedOperationID`, update service desired state/spec/generation/current operation, and insert the pending operation for `TransitionCreated`.

Any error must roll back every change.

- [x] **Step 4: Extend the tagged PG test without executing it**

Add real SQL assertions for mutation rollback, stop-preempts-create, repeated stop returns the same operation, already-stopped stop persists a completed no-op without generation change, and list/get tenant isolation. Do not configure or connect a DSN in this step.

- [x] **Step 5: Verify GREEN**

```text
GOWORK=off go test -race ./internal/repository -count=1
GOWORK=off go test -tags=integration ./internal/repository -run TestPostgresControlPlaneIntegration -count=1 -v
```

Expected: unit tests PASS; tagged test compiles and explicitly SKIPs when `INFERENCE_TEST_DATABASE_URL` is absent.

### Task 2: Add lifecycle/query application use cases

**Files:**
- Create: `repo/services/inference-service/internal/service/control.go`
- Create: `repo/services/inference-service/internal/service/control_test.go`

**Interfaces:**
- Consumes: `repository.ControlStore.GetService`, `GetOperation`, `ListServices`, and `MutateService`.
- Produces: `Controller.Get`, `Controller.List`, `Controller.Scale`, `Controller.Lifecycle`, `Controller.Delete`, and `Controller.GetOperation`.

- [x] **Step 1: Write failing use-case tests**

Cover exact behavior:

```go
func TestScaleHashesNormalizedServiceAndReplicaIntent(t *testing.T)
func TestScaleRejectsMultiNodeReplicaChangeBeforeMutation(t *testing.T)
func TestLifecycleReturnsRealOperationForActiveStopReplay(t *testing.T)
func TestLifecycleReturnsPersistedCompletedNoop(t *testing.T)
func TestDeleteUsesServiceDesiredStateForDeduplication(t *testing.T)
func TestQueriesNeverProjectRuntimeEndpoint(t *testing.T)
```

- [x] **Step 2: Verify RED**

```text
GOWORK=off go test ./internal/service -run 'TestScale|TestLifecycle|TestDelete|TestQueries' -count=1
```

Expected: FAIL because `Controller` is absent.

- [x] **Step 3: Implement the controller**

Use deterministic SHA-256 hashes over tenant, service ID, action, normalized target spec, and idempotency key-independent request content. Use operation scopes `inference_service.scale/start/stop/restart/delete`. Delete derives its deduplication from service identity and desired deleted state rather than introducing a new required API key.

Return domain entities only. HTTP DTO mapping belongs to the later ingress batch; `runtime_endpoint` and `runtime_ref` must not be copied into any public projection type.

- [x] **Step 4: Verify GREEN**

```text
GOWORK=off go test -race ./internal/service -count=1
```

Expected: PASS.

### Task 3: Expand the runtime anti-corruption interface and fake runtime

**Files:**
- Modify: `repo/services/inference-service/internal/runtime/runtime.go`
- Modify: `repo/services/inference-service/internal/runtime/fake/fake.go`
- Create: `repo/services/inference-service/internal/runtime/fake/fake_test.go`

**Interfaces:**
- Produces: `RuntimeRequest`, `LifecycleRequest`, `InferenceRuntime.Ensure`, `Observe`, `ApplyLifecycle`, and `Delete`.
- Every mutation request contains tenant ID, service ID, target generation, and a deterministic idempotency key.

- [x] **Step 1: Write failing fake-runtime tests**

```go
func TestEnsureReplaysOneRuntimePerServiceIdentity(t *testing.T)
func TestApplyLifecycleStopRetainsRuntimeIdentityAndClearsEndpoint(t *testing.T)
func TestApplyLifecycleStartRestoresEndpoint(t *testing.T)
func TestDeleteRemovesRuntimeAndIsIdempotent(t *testing.T)
func TestStaleGenerationCannotReplaceNewerFakeRuntime(t *testing.T)
```

- [x] **Step 2: Verify RED**

```text
GOWORK=off go test ./internal/runtime/fake -count=1
```

Expected: FAIL because lifecycle/observe/delete methods are absent.

- [x] **Step 3: Implement deterministic fake behavior**

Keep fake state keyed by tenant + service, guarded by a mutex. Reject an older generation, replay the same generation/key, preserve `RuntimeRef` across stop/start, clear only the endpoint on stop, and remove state on delete. Return no provider-specific fields.

- [x] **Step 4: Verify GREEN and race safety**

```text
GOWORK=off go test -race ./internal/runtime/fake -count=1
```

Expected: PASS.

### Task 4: Reconcile the complete operation matrix

**Files:**
- Modify: `repo/services/inference-service/internal/reconcile/worker.go`
- Modify: `repo/services/inference-service/internal/reconcile/worker_test.go`
- Modify: `repo/services/inference-service/internal/reconcile/flow_test.go`
- Modify: `repo/services/inference-service/internal/repository/store.go`
- Modify: `repo/services/inference-service/internal/repository/postgres.go`

**Interfaces:**
- Consumes: all persisted operation actions and the expanded runtime interface.
- Produces: create/scale/start/stop/restart/delete terminal observations, including tombstone and endpoint-clearing semantics.

- [x] **Step 1: Write failing worker tests**

```go
func TestWorkerScaleWaitsForTargetReadyReplicas(t *testing.T)
func TestWorkerStopClearsEndpointButRetainsRuntimeRef(t *testing.T)
func TestWorkerStartRestoresStoppedRuntime(t *testing.T)
func TestWorkerRestartUsesLifecycleIdempotencyKey(t *testing.T)
func TestWorkerDeleteTombstonesOnlyAfterRuntimeDeletion(t *testing.T)
func TestPreemptedCreateCannotRestoreRuntimeAfterStop(t *testing.T)
```

- [x] **Step 2: Verify RED**

```text
GOWORK=off go test ./internal/reconcile -run 'TestWorker|TestPreempted' -count=1
```

Expected: FAIL on unsupported C1 operations.

- [x] **Step 3: Implement action dispatch and finalization**

Create/scale use `Ensure`; start/stop/restart use `ApplyLifecycle`; delete uses `Delete`. Observe runtime after mutations. Create/scale/start/restart complete only after ready replica count, health, and smoke checks. Stop completes with `status=stopped`, endpoint empty, ready replicas zero, and the same runtime ref. Delete completes by setting `deleted_at` and clearing endpoint/ready replicas. All callbacks retain lease-token and generation/current-operation fencing.

- [x] **Step 4: Verify full fake state-machine flow**

Extend `flow_test.go` to execute:

```text
create -> running -> scale -> running -> stop -> stopped -> start -> running -> restart -> running -> delete -> tombstoned/not found
```

Also execute `create -> deploying -> stop` and prove the old create observation cannot restore running.

- [x] **Step 5: Verify GREEN and race safety**

```text
GOWORK=off go test -race ./internal/reconcile ./internal/repository ./internal/service ./internal/runtime/fake -count=1
```

Expected: PASS.

### Task 5: Close C2 and prepare the separate ingress batch

**Files:**
- Create: `repo/development-records/inference-service-lifecycle-control-plane-c2.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`
- Modify: `repo/Makefile` only if focused C2 tests need a stable aggregate target.

**Interfaces:**
- Produces: truthful local/logic evidence and an explicit C3 ingress boundary.

- [x] **Step 1: Run final gates**

```text
cd repo/services/inference-service
GOWORK=off go test -race ./... -count=1
GOWORK=off go test -tags=integration ./internal/repository -run TestPostgresControlPlaneIntegration -count=1 -v
cd ../..
python3 scripts/validate_inference_control_plane_migration_test.py
python3 scripts/validate_inference_control_plane_migration.py
PATH=/tmp/ani-pybin:$PATH make validate-services
PATH=/tmp/ani-pybin:$PATH make test
PATH=/tmp/ani-pybin:$PATH make validate-architecture
git diff --check
```

Expected: all local/static commands exit 0; tagged PG test explicitly reports SKIP without a DSN.

- [x] **Step 2: Update feature records**

Record only persisted lifecycle/query behavior and fake dependency convergence. Do not claim HTTP ingress, Gateway delegation, real ModelCatalog/Core adapter, migration apply, runtime readiness, or cluster inference.

- [x] **Step 3: Review before C3**

Request an independent code review. Fix every Critical/Important finding. The next plan is a separate C3 batch for the standalone inference-service HTTP process and ANI Gateway delegation; do not mix it into C2.
