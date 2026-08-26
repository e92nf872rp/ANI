# Inference Service Stage C3 HTTP Application Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the approved inference-service create/query/scale/lifecycle/delete control surface through a cluster-internal, contract-shaped HTTP application adapter without modifying ANI Gateway or exposing runtime endpoints.

**Architecture:** A standard-library `net/http` adapter translates the approved Services OpenAPI DTOs into the existing Creator and Controller use cases. Tenant identity is supplied through an injected `TenantResolver`; C3 deliberately provides no trust-on-header production resolver and no public listener wiring. The adapter owns strict JSON/UUID validation, 202/200 status semantics, stable error codes, and public projections, while PostgreSQL remains the only control-plane authority.

**Tech Stack:** Go 1.25, standard-library `net/http`/`encoding/json`, UUID, existing inference-service application/domain packages, `httptest`.

## Global Constraints

- Work only on local `main`; do not create branches or worktrees.
- The approved Services OpenAPI contract is unchanged in C3; API-first PR #101 remains the source.
- Do not modify ANI Gateway in this batch. The user explicitly deferred the gateway until the inference service itself runs correctly.
- Do not accept tenant identity from request JSON or an unauthenticated raw header. Every request uses an injected `TenantResolver` whose production implementation belongs to later service-identity wiring.
- Do not expose `runtime_ref`, `runtime_endpoint`, Core task IDs, ClusterIP addresses, `endpoint_url`, or a non-null `invocation_url`.
- Do not add logs, inference test, policies, ModelCatalog adapter, Core SDK runtime adapter, Kubernetes/LWS/Volcano/vLLM, or live PostgreSQL execution.
- Commit, push, and PR require separate explicit user instructions.

---

### Task 1: Freeze transport DTO and error semantics

**Files:**
- Create: `repo/services/inference-service/internal/httpapi/types.go`
- Create: `repo/services/inference-service/internal/httpapi/types_test.go`

**Interfaces:**
- Produces: request DTOs `CreateRequest`, `ScaleRequest`, `LifecycleRequest`; response DTOs aliasing the existing `service.ServiceView` and `service.OperationView`; `ErrorResponse`; strict decode and mapping helpers.
- Consumes: `service.CreateInput`, `domain.Spec`, `domain.Accelerator`, repository/catalog/domain sentinel errors.

- [x] **Step 1: Write failing DTO normalization tests**

Cover these exact cases:

```go
func TestCreateRequestUsesModelVersionUUIDAndUnifiedResources(t *testing.T)
func TestCreateRequestRejectsConflictingModelIdentifiers(t *testing.T)
func TestCreateRequestDoesNotInferAcceleratorFromLegacyGPUCount(t *testing.T)
func TestDecodeRejectsUnknownFieldsAndTrailingJSON(t *testing.T)
func TestErrorMappingUsesApprovedInferenceCodes(t *testing.T)
```

The normalized request must parse `model_version_id`, or use `model` when it is a UUID; when both exist they must match. It must copy CPU/memory and optional accelerator, default replicas to 1 and placement to auto, and never infer accelerator from `gpu_count_per_pod` alone.

- [x] **Step 2: Verify RED**

```text
GOWORK=off go test ./internal/httpapi -run 'TestCreateRequest|TestDecode|TestErrorMapping' -count=1
```

Expected: FAIL because `internal/httpapi` does not exist.

- [x] **Step 3: Implement strict DTO normalization and error mapping**

Use these stable mappings:

```text
invalid JSON/UUID/field combination -> 400 INVALID_ARGUMENT
repository.ErrNotFound/catalog.ErrModelNotFound -> 404 NOT_FOUND
repository.ErrNameConflict -> 409 NAME_CONFLICT
repository.ErrIdempotencyConflict -> 409 IDEMPOTENCY_CONFLICT
domain.ErrOperationInProgress -> 409 OPERATION_IN_PROGRESS
catalog.ErrModelNotReady -> 422 MODEL_NOT_READY
catalog.ErrNoCompatibleProfile -> 422 MODEL_INCOMPATIBLE
domain.ErrInvalidTransition/domain.ErrDeleted/domain.ErrLegacyQuarantined -> 422 INVALID_STATE_TRANSITION
unknown dependency error -> 503 DEPENDENCY_UNAVAILABLE
```

`ErrorResponse` contains only `code` and a sanitized `message`; it must not serialize raw SQL/provider errors.

- [x] **Step 4: Verify GREEN**

```text
GOWORK=off go test -race ./internal/httpapi -run 'TestCreateRequest|TestDecode|TestErrorMapping' -count=1
```

Expected: PASS.

### Task 2: Implement the contract-shaped HTTP handler

**Files:**
- Create: `repo/services/inference-service/internal/httpapi/handler.go`
- Create: `repo/services/inference-service/internal/httpapi/handler_test.go`

**Interfaces:**
- Consumes: injected `CreateUseCase`, `ControlUseCase`, and `TenantResolver` interfaces defined in `handler.go`.
- Produces: `NewHandler(creator CreateUseCase, controller ControlUseCase, tenants TenantResolver) http.Handler`.

Use these interfaces exactly:

```go
type CreateUseCase interface {
    Create(context.Context, uuid.UUID, service.CreateInput) (domain.Service, domain.Operation, error)
}

type ControlUseCase interface {
    Get(context.Context, uuid.UUID, uuid.UUID) (service.ServiceView, error)
    List(context.Context, uuid.UUID) ([]service.ServiceView, error)
    GetOperation(context.Context, uuid.UUID, uuid.UUID) (service.OperationView, error)
    Scale(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) (domain.Operation, error)
    Lifecycle(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, domain.Action) (domain.Operation, error)
    Delete(context.Context, uuid.UUID, uuid.UUID) (domain.Operation, error)
}

type TenantResolver interface {
    ResolveTenant(*http.Request) (uuid.UUID, error)
}
```

- [x] **Step 1: Write failing handler tests**

Cover:

```go
func TestCreateReturns202ResourceAndNeverInternalEndpoint(t *testing.T)
func TestListAndGetReturn200TenantScopedProjection(t *testing.T)
func TestScaleLifecycleDeleteReturn202AsyncTask(t *testing.T)
func TestGetOperationReturns200AsyncTask(t *testing.T)
func TestHandlerRejectsMissingTenantMalformedUUIDAndUnknownRoute(t *testing.T)
func TestHandlerMapsUseCaseErrorsWithoutLeakingCause(t *testing.T)
```

Mount exactly these cluster-internal contract paths:

```text
GET    /api/v1/svc/inference-services
POST   /api/v1/svc/inference-services
GET    /api/v1/svc/inference-services/{service_id}
PATCH  /api/v1/svc/inference-services/{service_id}
DELETE /api/v1/svc/inference-services/{service_id}
POST   /api/v1/svc/inference-services/{service_id}/lifecycle
GET    /api/v1/svc/inference-operations/{operation_id}
```

- [x] **Step 2: Verify RED**

```text
GOWORK=off go test ./internal/httpapi -run 'TestCreateReturns|TestListAndGet|TestScaleLifecycle|TestGetOperation|TestHandler' -count=1
```

Expected: FAIL because `NewHandler` is absent.

- [x] **Step 3: Implement minimal routing and handlers**

Use Go 1.25 `http.ServeMux` method patterns. Resolve tenant before parsing or invoking a use case. Encode all JSON with `Content-Type: application/json`; return 202 for create/scale/lifecycle/delete, 200 for list/get/operation, 404 for unregistered paths, and 405 from the mux for unsupported methods. Convert returned domain operations through one exported-safe projection helper; never marshal domain resources directly.

- [x] **Step 4: Verify GREEN and race safety**

```text
GOWORK=off go test -race ./internal/httpapi -count=1
```

Expected: PASS.

### Task 3: Add an internal server lifecycle wrapper without inventing auth

**Files:**
- Create: `repo/services/inference-service/internal/httpapi/server.go`
- Create: `repo/services/inference-service/internal/httpapi/server_test.go`

**Interfaces:**
- Consumes: an already constructed `http.Handler`.
- Produces: `Server` with `Run(context.Context) error`, explicit address and timeout configuration, and graceful shutdown.

- [x] **Step 1: Write failing server lifecycle tests**

```go
func TestServerRejectsPublicWildcardAddress(t *testing.T)
func TestServerRequiresPositiveTimeouts(t *testing.T)
func TestServerShutsDownWhenContextIsCancelled(t *testing.T)
```

C3 only permits loopback or an explicitly supplied cluster-pod IP; reject `:port`, `0.0.0.0:port`, and `[::]:port` so an incomplete auth boundary cannot be exposed accidentally.

- [x] **Step 2: Verify RED**

```text
GOWORK=off go test ./internal/httpapi -run TestServer -count=1
```

Expected: FAIL because `Server` is absent.

- [x] **Step 3: Implement bounded server lifecycle**

Configure `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, and a bounded shutdown timeout. `Run` must return nil for context-driven `http.ErrServerClosed`, propagate listen failures, and never log request bodies or tenant/model data.

- [x] **Step 4: Verify GREEN**

```text
GOWORK=off go test -race ./internal/httpapi -run TestServer -count=1
```

Expected: PASS.

### Task 4: Close C3 without Gateway or live claims

**Files:**
- Create: `repo/development-records/inference-service-http-adapter-c3.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

**Interfaces:**
- Produces: local/logic evidence for the cluster-internal HTTP application adapter and an explicit boundary for later process composition/service identity.

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

Expected: local/static gates exit 0; tagged PG test explicitly SKIPs without a DSN.

- [x] **Step 2: Update feature records**

Record only the injected HTTP adapter, DTO/error semantics, safe projections, and server lifecycle wrapper. Explicitly state that there is no production tenant resolver, executable composition root, Gateway change, live PostgreSQL, ModelCatalog/Core adapter, or runtime/live readiness.

- [x] **Step 3: Review before dependency adapters**

Request independent review and fix every Critical/Important finding. The following batch must implement service identity plus real ModelCatalog/Core SDK adapters and a composition root; ANI Gateway remains deferred until the standalone inference service is proven running.
