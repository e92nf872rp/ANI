# Control Plane State Recovery Implementation Plan

> **状态：已拆分，不再直接执行。** 执行入口改为 [`2026-08-03-p0-core-resource-closure.md`](2026-08-03-p0-core-resource-closure.md)；本文仅保留为网络、存储、向量 PostgreSQL 设计输入。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make PostgreSQL the authoritative control-plane store for ANI Core network, storage, and vector-store resources so existing v1 `GET/LIST` and idempotent writes survive Gateway restart.

**Architecture:** Add normalized tenant-scoped tables, extend the existing resource-store ports with read APIs, and make real-provider services read PG on every request. Existing Redis middleware remains the HTTP duplicate-submission boundary. Gateway opens one shared metadata runtime for these domains and fails startup when a real provider is configured without a healthy, schema-ready PostgreSQL database.

**Tech Stack:** Go 1.25, PostgreSQL 17, pgx v5, Python 3 live-gate validators, Kube-OVN, Kubernetes/Rook-Ceph, MinIO, Milvus.

## Global Constraints

- Core only; do not modify ANI Services, model-service, kb-service, Console, or BOSS.
- Do not modify `repo/api/openapi/v1.yaml`; this batch implements existing v1 behavior only.
- Do not add prototype-only fields that are absent from v1.
- All tenant tables must use forced RLS with `app.current_tenant_id`.
- Real Provider profiles require a healthy PostgreSQL connection and required schema; no memory fallback.
- Explicit local profiles remain memory-backed.
- Every production-code behavior change starts with a focused failing test.
- Provider calls must not run inside long PostgreSQL transactions.
- Keep HTTP idempotency in the existing Redis middleware; do not add a duplicate PostgreSQL response ledger.
- Persist create idempotency key and request fingerprint on each creatable resource table so direct Service calls and multiple Gateway replicas cannot duplicate resources.
- Tokens, passwords, complete internal endpoints, embeddings, document contents, and pre-signed URLs must not be written to PG or evidence.
- Do not commit, push, create a PR, migrate the live database, restart Gateway, or run real write gates without explicit user confirmation at the relevant gate.

---

### Task 1: Freeze the migration and schema contract

**Files:**
- Create: `repo/deploy/migrations/20260803_001_control_plane_state_recovery.sql`
- Create: `repo/scripts/validate_control_plane_state_recovery.py`
- Create: `repo/scripts/validate_control_plane_state_recovery_test.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Consumes: existing `network_*`, `storage_*`, `async_tasks`, `tenants`, and `workload_instances` tables.
- Produces: normalized network/storage/vector tables, corrected route RLS, and a static validation gate.

- [ ] Write failing validator tests requiring every table, tenant-first key/index, create-idempotency columns/unique constraints, composite foreign key, state check, soft-delete field, RLS policy, and the corrected `app.current_tenant_id` route policy.
- [ ] Run `python3 scripts/validate_control_plane_state_recovery_test.py` and confirm it fails because the migration and validator do not exist.
- [ ] Add the validator with explicit positive and negative fixtures; reject `ani.tenant_id`, missing `WITH CHECK`, missing tenant keys, and tables without soft-delete handling where required.
- [ ] Add the additive SQL migration described in the approved design. Keep legacy JSONB columns, backfill SG rules and LB listeners, and fail on malformed legacy rows.
- [ ] Add `make validate-control-plane-state-recovery` and run the focused Python tests until green.
- [ ] Run `python3 scripts/validate_control_plane_state_recovery.py` and `git diff --check`.

### Task 2: Make the network Store fully readable

**Files:**
- Modify: `repo/pkg/ports/network_resources.go`
- Modify: `repo/pkg/adapters/runtime/network_store.go`
- Modify: `repo/pkg/adapters/runtime/network_store_test.go`

**Interfaces:**
- Produces: tenant-scoped `Get/List` and child-resource read/write methods for VPCs, subnets, security groups, rules, bindings, IP allocations, load balancers/listeners, and routes.
- Consumes: the Task 1 schema and existing network record types.

- [ ] Write failing Store tests for every main resource `Get/List`, cursor ordering, child aggregation, cross-tenant not-found, and deleted-row filtering.
- [ ] Write failing atomic-create tests for same key/same fingerprint replay, same key/different fingerprint conflict, and two concurrent Store instances.
- [ ] Add failing write tests for normalized rules, bindings, allocations and listeners.
- [ ] Verify tests fail because `NetworkResourceStore` is write-only.
- [ ] Extend the port with explicit typed methods; do not add a generic `GetResource(map[string]any)` API.
- [ ] Implement SQL readers/scanners and normalized child writes in `MetadataNetworkStore`.
- [ ] Preserve current response ordering and map PostgreSQL no-row results to `ports.ErrNotFound`.
- [ ] Run focused network Store tests and existing `network_store`/status reconciler tests until green.

### Task 3: Switch NetworkService authority from maps to PG

**Files:**
- Modify: `repo/pkg/adapters/runtime/network_service.go`
- Modify: `repo/pkg/adapters/runtime/network_service_test.go`

**Interfaces:**
- Consumes: readable `NetworkResourceStore` and existing Redis HTTP idempotency middleware.
- Produces: PG-backed create/get/list/update/delete behavior while retaining the local in-memory profile when no Store is supplied.

- [ ] Write failing service tests using a shared fake persistent Store across two service instances; create through instance A and get/list/create-replay through instance B.
- [ ] Add tests proving internal same-key/different-intent conflict and deleted resources remain hidden but retain tombstones; HTTP response replay remains covered by existing Redis middleware tests.
- [ ] Route all real-store reads to PG instead of service maps.
- [ ] Persist stable resource IDs before Provider apply and record `pending -> available/failed` transitions.
- [ ] Keep Provider calls outside PG transactions and preserve current v1 error semantics.
- [ ] Run all network service/store/provider tests.

### Task 4: Make the storage Store fully readable

**Files:**
- Modify: `repo/pkg/ports/storage_resources.go`
- Modify: `repo/pkg/adapters/runtime/storage_store.go`
- Modify: `repo/pkg/adapters/runtime/storage_store_test.go`

**Interfaces:**
- Produces: typed persistence for volumes, policies, mount history, snapshots, filesystems, mount targets, attachments, buckets, lifecycle rules, and Core-created object metadata.
- Consumes: the Task 1 schema and existing storage record types.

- [ ] Write failing tests for volume/filesystem/object/bucket `Get/List`, cursor behavior, soft deletes, and cross-tenant isolation.
- [ ] Write failing atomic-create tests for same key replay, fingerprint conflict and concurrent Store instances.
- [ ] Write failing tests for snapshot, mount-history, mount-target, attachment and lifecycle-rule aggregation.
- [ ] Extend `StorageResourceStore` with typed methods matching existing v1 service operations.
- [ ] Implement SQL scanners and writes, using separate append-only mount-event records.
- [ ] Keep MinIO object browsing provider-backed; persist bucket configuration and Core object metadata only.
- [ ] Run focused storage Store tests and existing storage reconciler tests until green.

### Task 5: Switch StorageService authority from maps to PG

**Files:**
- Modify: `repo/pkg/adapters/runtime/storage_service.go`
- Modify: `repo/pkg/adapters/runtime/storage_service_test.go`

**Interfaces:**
- Consumes: readable `StorageResourceStore`, existing Redis HTTP idempotency middleware, Kubernetes storage provider, and MinIO object store.
- Produces: restart-safe storage metadata without changing the separate provider-lifecycle gaps identified for the next batch.

- [ ] Write failing two-service-instance tests for volumes, snapshots, filesystems, mount targets, buckets, rules, objects and create replay.
- [ ] Add tests that bucket object browsing still reads MinIO after service recreation while bucket identity/settings come from PG.
- [ ] Replace map authority with Store reads whenever a persistent Store is configured.
- [ ] For provider-backed creates, commit the `pending` resource and create-idempotency fields before Provider apply, then persist the observed final state.
- [ ] Persist every existing v1 mutation and its stable result; regenerate pre-signed URLs instead of storing them.
- [ ] Do not claim real expand/mount/delete Provider execution in this task; preserve current behavior and document the boundary.
- [ ] Run all storage service/store/object-provider tests.

### Task 6: Add Vector Store metadata persistence

**Files:**
- Modify: `repo/pkg/ports/vector_store.go`
- Create: `repo/pkg/adapters/runtime/vector_store_store.go`
- Create: `repo/pkg/adapters/runtime/vector_store_store_test.go`
- Modify: `repo/pkg/adapters/runtime/vector_store_service.go`
- Modify: `repo/pkg/adapters/runtime/vector_store_service_test.go`

**Interfaces:**
- Produces: `VectorStoreResourceStore` for vector-store definitions and KB links.
- Consumes: Milvus `ports.VectorStore`, existing `AsyncTaskStore`, and existing Redis HTTP idempotency middleware.

- [ ] Write failing Store tests for atomic create replay/fingerprint conflict, get/list/state update, KB link/unlink, soft delete and cross-tenant isolation.
- [ ] Write failing two-service-instance tests: create via A, then create-replay/get/list/search/rebuild/delete-precheck via B.
- [ ] Implement `MetadataVectorStoreStore` and inject it into `LocalVectorStoreService` as the authoritative metadata Store.
- [ ] Commit a pending vector-store row before `EnsureCollection`; use the same resource ID to observe/recover after interruption.
- [ ] Keep embeddings/documents/search results in Milvus and document insert task state in existing PG `async_tasks`.
- [ ] Persist `vector_count`, `index_status`, `last_indexed_at`, state, reason and KB reference summaries.
- [ ] Run vector Store/service/Milvus adapter and async-task tests.

### Task 7: Add shared Gateway PostgreSQL runtime and fail-fast behavior

**Files:**
- Create: `repo/services/ani-gateway/control_plane_runtime.go`
- Create: `repo/services/ani-gateway/control_plane_runtime_test.go`
- Modify: `repo/services/ani-gateway/main.go`
- Modify: `repo/services/ani-gateway/network_runtime.go`
- Modify: `repo/services/ani-gateway/network_runtime_test.go`
- Modify: `repo/services/ani-gateway/storage_runtime.go`
- Modify: `repo/services/ani-gateway/storage_runtime_test.go`
- Modify: `repo/services/ani-gateway/vector_store_runtime.go`
- Modify: `repo/services/ani-gateway/vector_store_runtime_test.go`

**Interfaces:**
- Produces: one shared `MetadataStore`, domain Stores and close function for Network/Storage/Vector real profiles.
- Consumes: `bootstrap.ConnectMetadataStore`, `DATABASE_URL`, provider-mode configuration and a schema readiness query.

- [ ] Write failing runtime tests for missing `DATABASE_URL`, invalid/unreachable PG, missing schema, shared Store injection, and local-profile memory behavior.
- [ ] Add a minimal control-plane runtime constructor that connects once and owns close order.
- [ ] Require PG when any of `NETWORK_PROVIDER`, `STORAGE_PROVIDER`, or `VECTOR_STORE_PROVIDER` is real.
- [ ] Check required tables before constructing services; return a startup error on mismatch.
- [ ] Inject the same persistent services into instance orchestration as today.
- [ ] Run all ani-gateway runtime and router tests.

### Task 8: Add the restart and idempotency live gate

**Files:**
- Create: `repo/deploy/real-k8s-lab/control-plane-state-recovery-live-gate.yaml`
- Create: `repo/scripts/validate_control_plane_state_recovery_live_gate.py`
- Create: `repo/scripts/validate_control_plane_state_recovery_live_gate_test.py`
- Create after execution: `repo/development-records/live-evidence/control-plane-state-recovery-live-20260803.json`
- Modify: `repo/Makefile`

**Interfaces:**
- Consumes: production-shaped Gateway, PostgreSQL, Kube-OVN, Rook-Ceph, MinIO and Milvus.
- Produces: sanitized evidence for pre/post-rollout reads, PG rows, idempotency replay and cleanup.

- [ ] Write static and unit tests requiring all domain resources, restart proof, tenant isolation, replay counts, tombstones and credential redaction.
- [ ] Add `make validate-control-plane-state-recovery-live-gate` for static validation; live writes require explicit approval.
- [ ] In an isolated tenant/resource namespace, create the minimum network, storage and vector graph through Core v1.
- [ ] Record response hashes and provider resource counts without saving sensitive URLs or endpoints.
- [ ] Roll out Gateway and verify original IDs, lists, relationships and tasks remain queryable.
- [ ] Replay original idempotency keys and verify no duplicate PG or Provider resources; verify changed intent conflicts.
- [ ] Delete test resources through available Core paths, verify API filtering and PG tombstones, then clean any provider-only fixture resources.

### Task 9: Record the feature batch and run full gates

**Files:**
- Create: `repo/development-records/control-plane-state-recovery-a.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

**Interfaces:**
- Consumes: focused tests, migration validator and approved live evidence.
- Produces: the four required ANI feature-batch records with exact readiness boundaries.

- [ ] Record the database authority model, table coverage, fail-fast rule, restart result and known Provider lifecycle exclusions.
- [ ] Run `make validate-control-plane-state-recovery` and the live-gate static validator.
- [ ] Run focused Go/Python tests, `make validate-openapi-spec`, and `make validate-core-api-compatibility` to prove no v1 drift.
- [ ] Run `PATH=/tmp/ani-pybin:$PATH make test`, `make validate-architecture`, `make validate-doc-entrypoints`, and `git diff --check`.
- [ ] Scan changed files and evidence for credentials, tokens, private endpoints and pre-signed URLs.
- [ ] Stop before commit/push and present the exact diff and verification results for human confirmation.

## Execution Checkpoints

1. **Design checkpoint:** approve this plan before code changes.
2. **Migration checkpoint:** review Task 1 DDL and data backfill before applying it to any real PG.
3. **Local implementation checkpoint:** Tasks 2-7 tests and architecture gates pass before building a Gateway image.
4. **Live-write checkpoint:** obtain explicit approval before Task 8 creates resources, applies migration, or restarts Gateway.
5. **Shipping checkpoint:** obtain explicit approval before commit, push or PR.
