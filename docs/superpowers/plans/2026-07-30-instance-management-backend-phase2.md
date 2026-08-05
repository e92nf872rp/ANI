# Instance Management Backend Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the approved instance-management contract in the Core product ports, local instance service, and metadata persistence without bypassing provider adapters or adding quota behavior.

**Architecture:** Extend the existing `WorkloadInstanceService` boundary instead of creating another instance service. Canonical image, network, storage, GPU-spec, lifecycle, and response-summary data remains provider-neutral in `pkg/ports`; `LocalInstanceService` owns validation, idempotency, state transitions, and operation history; `MetadataInstanceStore` persists the new record summaries as JSONB. Gateway bootstrap injects this service with PostgreSQL stores and the Kubernetes REST provider. Real Registry/Network/Storage association orchestration remains a separate follow-up batch.

**Tech Stack:** Go 1.25+, PostgreSQL 17 migrations, existing ANI `pkg/ports` and `pkg/adapters/runtime` packages.

## Global Constraints

- Work only on `main`.
- The approved Core OpenAPI contract in `repo/api/openapi/v1.yaml` is the public source of truth.
- Gateway and domain services must not import Kubernetes SDKs or construct provider objects.
- GPU quota check/acquire/release is out of scope.
- New behavior follows test-driven development: observe each focused test fail before implementation.
- Do not commit or push without an explicit user confirmation.

---

### Task 1: Extend Provider-Neutral Instance Types

**Files:**
- Modify: `repo/pkg/ports/workload_runtime.go`
- Test: `repo/pkg/adapters/runtime/instance_service_test.go`

**Interfaces:**
- Produces: canonical instance image, network, disk, mount, port, environment, compute, access, and storage-summary records.
- Produces: the approved lifecycle constants and typed payload fields on `WorkloadInstanceLifecycleRequest`.

- [x] Add a compile-time-focused test that constructs all newly approved fields and verifies they survive a service create/get round trip.
- [x] Run `go test ./pkg/adapters/runtime -run TestLocalInstanceServicePersistsApprovedInstanceSummaries -count=1` and confirm it fails because the port fields do not exist.
- [x] Add only the provider-neutral fields required by the approved OpenAPI schemas.
- [x] Add operation-step task correlation fields: `TaskID`, `ResourceType`, and `ResourceID`.
- [x] Run the focused test and confirm it passes.

### Task 2: Enforce Create Intent and Idempotency Semantics

**Files:**
- Modify: `repo/pkg/adapters/runtime/instance_service.go`
- Modify: `repo/pkg/adapters/runtime/instance_service_test.go`
- Modify: `repo/pkg/adapters/runtime/operation_store.go`
- Modify: `repo/pkg/adapters/runtime/operation_store_test.go`

**Interfaces:**
- Consumes: `WorkloadSpec` canonical image/config/reference fields from Task 1.
- Produces: validation that exactly the matching kind config is supplied, disk source modes are exclusive, env value/secret references are exclusive, and GPU spec/legacy fields do not conflict.
- Produces: an idempotency fingerprint stored with the operation so the same key plus different intent returns `ports.ErrConflict`.

- [x] Write focused failing tests for invalid cross-kind config, invalid disk/env unions, and same idempotency key with a different create intent.
- [x] Run the tests and confirm each failure is caused by missing validation or fingerprint comparison.
- [x] Add minimal create-intent validation before operation/provider execution.
- [x] Persist a deterministic create-intent fingerprint in operation precheck metadata and reject mismatched replay.
- [x] Re-run focused and existing instance-service tests.

### Task 3: Implement Approved Lifecycle Payload and Matrix

**Files:**
- Modify: `repo/pkg/adapters/runtime/instance_service.go`
- Modify: `repo/pkg/adapters/runtime/instance_service_test.go`
- Modify: `repo/pkg/adapters/runtime/status_reconciler.go` if transition support is missing there
- Create: `repo/deploy/migrations/20260730_001_instance_management_lifecycle_ops.sql`

**Interfaces:**
- Consumes: typed lifecycle fields from Task 1.
- Produces: support for `attach_filesystem`, `detach_filesystem`, `scale`, `update_image`, `bind_secret`, `unbind_secret`, `change_security_groups`, `set_termination_protection`, `pause`, `resume`, `extend`, and `touch_idle`.
- Produces: operation records whose enum values are accepted by PostgreSQL.

- [x] Write failing tests for action-kind matrix rejection and required action payloads.
- [x] Write failing success tests for local state/summary updates that do not require a real provider.
- [x] Add lifecycle prechecks with 400-equivalent `ports.ErrInvalid`, 409-equivalent `ports.ErrConflict`, and unsupported-capability `ports.ErrUnsupported` distinctions.
- [x] Extend transitions and operation summaries without fabricating Storage task results.
- [x] Add an additive migration for summary columns, kind/operation constraints, and operation-step task correlation.
- [x] Re-run focused lifecycle tests.

### Task 4: Persist Approved Instance Summaries

**Files:**
- Modify: `repo/pkg/adapters/runtime/instance_store.go`
- Modify: `repo/pkg/adapters/runtime/instance_store_test.go`
- Modify: `repo/deploy/migrations/20260730_001_instance_management_lifecycle_ops.sql`

**Interfaces:**
- Consumes: response-summary fields from Task 1.
- Produces: restart-safe storage for description, labels, image, compute, network summary, access summary, storage attachments, and sandbox status.

- [x] Write a failing store test that asserts the new JSONB columns and arguments are present.
- [x] Add additive JSONB/text columns in the migration.
- [x] Extend upsert/select/scan paths while preserving tenant RLS and current fields.
- [x] Re-run instance-store and instance-service tests.

### Task 5: Add Filtering Without Changing the Store Interface Shape

**Files:**
- Modify: `repo/pkg/ports/workload_runtime.go`
- Modify: `repo/pkg/adapters/runtime/instance_service.go`
- Modify: `repo/pkg/adapters/runtime/instance_service_test.go`

**Interfaces:**
- Produces: service-level filtering for state, keyword, spec/image/node and kind-specific status, plus deterministic sort and cursor fields in the request/result model.
- Does not claim database keyset pagination until the store interface and Gateway batch implement it end to end.

- [x] Write failing tests for tenant-local state/keyword/spec filters and deterministic created-time sorting.
- [x] Implement filtering over store results using canonical persisted summaries.
- [x] Reject invalid limit/sort values instead of silently changing their meaning.
- [x] Re-run focused tests.

### Task 6: Verification and Development Record

**Files:**
- Create: `repo/development-records/INSTANCE-PORTS-SERVICE-A.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

- [x] Run `gofmt` on changed Go files.
- [x] Run `GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'Test.*(Instance|Operation)' -count=1`.
- [x] Run `GOCACHE=/tmp/ani-go-cache go test ./pkg/ports/... ./pkg/adapters/runtime/... -count=1`.
- [x] Run `make test`.
- [x] Run `make validate-architecture`.
- [x] Run `git diff --check`.
- [x] Record only commands that actually passed and state that Gateway, real provider orchestration, Sandbox subresources, quota, and live gate remain follow-up work.
- [x] Review the complete diff and stop before commit/push for user confirmation.

### Task 7: Wire and Prove the Container Real-Provider Path

**Files:**
- Create: `repo/pkg/bootstrap/instance.go`
- Create: `repo/services/ani-gateway/instance_service_runtime.go`
- Create: `repo/services/reconcile-worker/Dockerfile`
- Modify: `repo/services/ani-gateway/main.go`
- Modify: `repo/services/ani-gateway/internal/router/instances.go`
- Modify: `repo/pkg/adapters/runtime/reconcile_controller.go`
- Evidence: `repo/development-records/live-evidence/instance-management-container-e2e-20260730.json`

- [x] Inject the PostgreSQL-backed service/store/operation runtime into the Gateway router.
- [x] Build and deploy Gateway and reconcile-worker as separate images.
- [x] Fix cross-process instance ID collisions, multi-resource delete, PostgreSQL timestamp parsing, idempotent provider delete, and terminal-state reconcile guards with focused tests.
- [x] Reproduce and fix reconcile-worker's missing tenant RLS context with a failing test.
- [x] Run a real container create/query/stop/start/delete flow through the authenticated Gateway.
- [x] Verify the Deployment and Pod use the Harbor mirror and receive a cluster-network IP.
- [x] Wait longer than the active reconcile interval and verify the deleted state remains terminal.
- [x] Verify all labeled Deployment, Pod, and workload-identity Secret resources are removed.
- [x] Re-run the complete local CI-equivalent gates after the live fixes.

### Task 8: Complete Gateway Intent Conversion and Manifest Consumption

**Files:**
- Modify: `repo/services/ani-gateway/internal/router/instances.go`
- Modify: `repo/services/ani-gateway/internal/router/instances_test.go`
- Modify: `repo/pkg/adapters/runtime/dryrun_renderer.go`
- Modify: `repo/pkg/adapters/runtime/dryrun_renderer_test.go`

**Interfaces:**
- Consumes: the approved HTTP create/lifecycle payload fields for image identity,
  network references, disks, volume/filesystem mounts, ports, env, workload identity,
  GPU spec reference, and Sandbox configuration.
- Produces: provider-neutral `WorkloadSpec` and `WorkloadInstanceLifecycleRequest`
  values without silently falling back to demo-only request shapes.
- Produces: Kubernetes manifests that honor container replicas, named ports, env,
  and storage mount intent.

- [x] Write failing Gateway conversion tests before adding the missing request DTO fields.
- [x] Map provider-neutral create fields while retaining existing flat aliases for compatibility.
- [x] Map the complete lifecycle payload and route newer actions through `ApplyLifecycle`.
- [x] Write a failing renderer test for replicas, ports, env, and mounts.
- [x] Make the Kubernetes renderer consume the mapped container intent and deduplicate
  storage attachments.
- [x] Run the Gateway router and runtime adapter package tests.

### Task 9: Resolve Cross-Resource References Before Instance Create

**Files:**
- Modify: `repo/pkg/ports/workload_runtime.go`
- Create: `repo/pkg/adapters/runtime/instance_resource_resolver.go`
- Create: `repo/pkg/adapters/runtime/instance_resource_resolver_test.go`
- Modify: `repo/pkg/adapters/runtime/instance_service.go`
- Modify: `repo/pkg/adapters/runtime/instance_service_test.go`
- Modify: `repo/pkg/bootstrap/deps.go`

**Interfaces:**
- Produces: a provider-neutral resource resolver boundary for instance create.
- Validates: tenant ownership and `available` state for VPC, subnet, security group,
  volume, and filesystem references before workload provider execution.
- Records: resolved resource references in the create operation precheck metadata.
- Does not yet resolve Harbor image identity or GPU spec records; those require the
  corresponding real capabilities to be injected into bootstrap.

- [x] Write failing service/resolver tests before adding the resolver port.
- [x] Add the resolver port and local metadata-backed implementation.
- [x] Inject it into the bootstrap-created instance service.
- [x] Run resolver, instance service, bootstrap, router, and runtime adapter tests.

### Task 10: Implement GPU Spec Selection and Create-Time Resolution

**Files:**
- Modify: `repo/pkg/ports/gpu_inventory.go`
- Create: `repo/pkg/adapters/runtime/gpu_spec_service.go`
- Create: `repo/pkg/adapters/runtime/gpu_spec_service_test.go`
- Modify: `repo/pkg/adapters/runtime/instance_resource_resolver.go`
- Modify: `repo/services/ani-gateway/internal/router/gpu_inventory_resources.go`
- Modify: `repo/pkg/bootstrap/deps.go`

**Interfaces:**
- Produces: read-only `/gpu-specs` and `/gpu-specs/{spec_id}` behavior from the
  configured GPU inventory.
- Resolves `spec_id` to GPU type, shares, and memory before instance validation.
- Rejects missing or unavailable specs for new instances; does not perform quota
  check, acquire, or release.

- [x] Write inventory-derived GPU spec tests before wiring the catalog.
- [x] Add deterministic full-card and virtualized spec aggregation.
- [x] Register GPU spec query routes and preserve historical unavailable specs.
- [x] Inject GPU spec resolution into the instance resource resolver.

### Task 11: Resolve Harbor Image References at Instance Create

**Files:**
- Modify: `repo/pkg/bootstrap/server.go`
- Modify: `repo/pkg/bootstrap/instance.go`
- Modify: `repo/pkg/bootstrap/deps.go`
- Modify: `repo/pkg/adapters/runtime/instance_resource_resolver.go`
- Modify: `repo/pkg/adapters/runtime/instance_resource_resolver_test.go`

**Interfaces:**
- Consumes: `REGISTRY_PROVIDER_MODE=harbor`, `HARBOR_*` configuration and the
  existing `ports.ImageRegistry` boundary.
- Resolves Harbor project/repository/tag or digest for explicit `image_ref` values,
  enforces tenant project ownership, and fixes the workload image summary to the
  resolved digest.
- Keeps legacy/external `image` behavior compatible when Registry is not configured.

- [x] Add bootstrap Harbor configuration fields and environment overrides.
- [x] Inject the configured registry into the instance resource resolver.
- [x] Add tenant/project and digest parsing tests.
- [x] Run full local CI after the registry wiring.
