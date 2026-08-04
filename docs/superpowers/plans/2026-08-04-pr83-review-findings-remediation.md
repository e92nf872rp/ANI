# PR 83 Review Findings Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve every accepted PR 83 scanner finding with a focused regression test while preserving an explicit disposition for all 40 findings.

**Architecture:** Keep fixes inside existing runtime adapters, Gateway routers, validation scripts, Make targets, and container build files. Duplicate findings share one root-cause test cycle; rejected and deferred findings remain documented in the approved design rather than causing speculative code changes.

**Tech Stack:** Go 1.25, Python 3.12 `unittest`, GNU Make, Dockerfile, PostgreSQL migration validators.

**Execution Status (2026-08-04):** Complete. All accepted findings were implemented through RED -> GREEN cycles. The final independent review found one additional per-table RLS-policy gap; it was reproduced, fixed, and the follow-up review returned no actionable regressions.

## Global Constraints

- Preserve `stash@{0}` (`network-p0-contract-c1`) and do not mix its files into this work.
- Do not modify Core or Services OpenAPI contracts.
- Do not rewrite already-applied migration DDL for finding #3.
- Every behavior change follows RED -> GREEN and records the focused command result.
- Do not commit, push, update PR 83, or perform live infrastructure writes without separate user approval.
- The final verification is `PATH=/tmp/ani-pybin:$PATH make test`, `make validate-architecture`, relevant validator targets, and `git diff --check` from `repo/`.

---

### Task 1: Storage migration validator isolation (#29-#33)

**Files:**
- Modify: `repo/scripts/validate_storage_control_plane_state.py`
- Test: `repo/scripts/validate_storage_control_plane_state_test.py`

**Interfaces:**
- Consumes: `validate_migration_sql(sql: str) -> None`
- Produces: comment-stripped, case/whitespace-normalized per-table validation for CREATE TABLE, tenant-first PK, idempotency columns, RLS enable/force, and tenant policy.

- [ ] Add tests where one required table lacks its own PK, idempotency fields, RLS clause, or policy while another table contains them; add an `ALTER TABLE ONLY` formatting success case and session-key-in-comment cases.
- [ ] Run `python -m unittest scripts.validate_storage_control_plane_state_test -v`; expect the new negative cases to fail because validation currently searches the whole migration.
- [ ] Add focused helpers that strip comments, extract each table definition, normalize SQL tokens, and locate table-specific ALTER/POLICY statements; use the stripped SQL for required and forbidden session-key checks.
- [ ] Re-run the focused unittest; expect PASS.

### Task 2: Live-gate UUID query safety (#24, #35)

**Files:**
- Modify: `repo/scripts/run_instance_sandbox_stateless_live_gate.py`
- Modify: `repo/scripts/validate_storage_control_plane_state_live_gate.py`
- Test: `repo/scripts/run_instance_sandbox_stateless_live_gate_test.py`
- Test: `repo/scripts/validate_storage_control_plane_state_live_gate_test.py`

**Interfaces:**
- Produces: psql `-v` variables referenced with `:'name'` so opaque resource IDs never enter SQL source text.

- [x] Add tests passing opaque task/resource IDs and assert values are supplied as psql variables rather than embedded in SQL.
- [x] Run both focused unittest modules; observe FAIL because the current query builders interpolate values.
- [x] Pass values with psql `-v` and reference them using `:'name'` in the read-only SQL.
- [x] Re-run both focused unittest modules; PASS.

### Task 3: AsyncTask shared validation (#9, #10, #17)

**Files:**
- Modify: `repo/pkg/adapters/runtime/async_task_store.go`
- Test: `repo/pkg/adapters/runtime/async_task_store_test.go`

**Interfaces:**
- Produces: shared create/update validators returning errors wrapping `ports.ErrInvalid`; optional non-empty `ResourceID` must parse as UUID for metadata persistence.

- [ ] Add table tests proving Local and Metadata Create/Update reject invalid progress, status, identity, and malformed non-empty `ResourceID` consistently.
- [ ] Run `go test ./pkg/adapters/runtime -run 'AsyncTaskStore' -count=1`; expect the Local/Metadata mismatch cases to fail.
- [ ] Extract and apply shared validation helpers before locking or opening tenant transactions; reject malformed ResourceID rather than converting it to empty text.
- [ ] Re-run the focused Go tests; expect PASS.

### Task 4: AsyncTask clone errors (#11)

**Files:**
- Modify: `repo/pkg/adapters/runtime/async_task_store.go`
- Test: `repo/pkg/adapters/runtime/async_task_store_test.go`

**Interfaces:**
- Produces: `cloneAnyMap(value map[string]any) (map[string]any, error)` and `cloneAsyncTaskRecord(record ports.AsyncTaskRecord) (ports.AsyncTaskRecord, error)`; nil maps normalize to non-nil empty maps.

- [ ] Add Create and Update tests using an unsupported JSON value (for example a channel) and assert an error is returned without persisting a partially cloned record; retain a nil-result non-nil-map assertion.
- [ ] Run the AsyncTask focused tests; expect FAIL because marshal/unmarshal errors are discarded.
- [ ] Propagate clone errors through Local Create/Get/Update and preserve non-nil empty maps for nil results.
- [ ] Re-run the focused tests; expect PASS.

### Task 5: Provider primary fallback (#12)

**Files:**
- Modify: `repo/pkg/adapters/runtime/kubernetes_provider_adapter.go`
- Test: `repo/pkg/adapters/runtime/kubernetes_provider_adapter_test.go`

**Interfaces:**
- Consumes: existing `primaryProvider([]ports.WorkloadManifest) string`
- Produces: empty provider fallback derived from the primary non-auxiliary manifest.

- [ ] Add an Apply test with an auxiliary manifest first, primary workload second, and an empty provider in the client result.
- [ ] Run `go test ./pkg/adapters/runtime -run 'KubernetesProviderAdapter' -count=1`; expect provider mismatch failure.
- [ ] Replace `request.Manifests[0].Provider` fallback with `primaryProvider(request.Manifests)`.
- [ ] Re-run the focused tests; expect PASS.

### Task 6: Best-effort lifecycle deletion (#15)

**Files:**
- Modify: `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor.go`
- Test: `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor_test.go`

**Interfaces:**
- Produces: delete attempts for every resource ref and `errors.Join` of all failures.

- [ ] Add a test with at least three refs where the first and last deletion fail; assert every ref was attempted and both errors are discoverable with `errors.Is`.
- [ ] Run the lifecycle executor focused test; expect FAIL because deletion stops at the first error.
- [ ] Collect deletion errors, continue the loop, and return `errors.Join(errs...)`.
- [ ] Re-run the focused tests; expect PASS.

### Task 7: Vector link injected clock (#19)

**Files:**
- Modify: `repo/pkg/adapters/runtime/vector_store_store.go`
- Test: `repo/pkg/adapters/runtime/vector_store_store_test.go`

**Interfaces:**
- Consumes: store `now func() time.Time`
- Produces: KB link timestamps sourced from `s.now().UTC()`.

- [ ] Add a deterministic-clock link upsert test asserting exact created/updated timestamp arguments.
- [ ] Run `go test ./pkg/adapters/runtime -run 'Vector.*Knowledge|Knowledge.*Vector' -count=1`; expect timestamp mismatch.
- [ ] Replace direct wall-clock access in the link path with the injected clock.
- [ ] Re-run focused tests; expect PASS.

### Task 8: Gateway error semantics (#28, #36, #40)

**Files:**
- Modify: `repo/services/ani-gateway/main.go` or the existing environment helper file containing `gatewayIntFromEnv`
- Modify: `repo/services/ani-gateway/internal/router/metering_resources.go`
- Modify: `repo/services/ani-gateway/storage_control_plane_runtime.go`
- Test: `repo/services/ani-gateway/main_test.go` or existing runtime test containing environment helper coverage
- Test: `repo/services/ani-gateway/internal/router/metering_resources_test.go`
- Test: `repo/services/ani-gateway/storage_control_plane_runtime_test.go`

**Interfaces:**
- Produces: invalid integer env diagnostics to stderr/log while preserving default semantics; unknown metering errors map to sanitized 500; schema inspection wraps underlying errors with `%w` while retaining `ports.ErrUnavailable` classification.

- [ ] Add focused tests for invalid integer diagnostics, metering `ErrInvalid` 400 versus opaque error 500 without backend text, and `errors.Is` preservation for schema inspection errors.
- [ ] Run the three focused Go test selections; expect FAIL on current behavior.
- [ ] Add the minimal diagnostic branch, split metering error mapping, and replace `%v` with `%w` for underlying schema errors.
- [ ] Re-run focused tests; expect PASS.

### Task 9: Validator missing-file failures (#22, #23)

**Files:**
- Modify: `repo/scripts/validate_async_task_store.py`
- Test: add focused coverage to the existing async-task validator test module, or create `repo/scripts/validate_async_task_store_test.py` if none exists.

**Interfaces:**
- Produces: one safe text reader returning validation messages instead of uncaught `FileNotFoundError`.

- [ ] Add tests patching the root to a tree missing each directly-read file and assert exit code 1, concise stderr, and no traceback.
- [ ] Run the focused unittest; expect ERROR/traceback.
- [ ] Route both `require` and task-router reads through the same guarded reader.
- [ ] Re-run focused tests; expect PASS.

### Task 10: VM lifecycle terminal-state sequencing (#25)

**Files:**
- Modify: `repo/scripts/validate_instance_management_live_gate.py`
- Test: `repo/scripts/validate_instance_management_live_gate_test.py`

**Interfaces:**
- Consumes: `wait_for_state(...)`
- Produces: stop -> wait stopped -> start -> wait running -> delete -> wait deleted/404 sequencing and evidence from terminal states.

- [ ] Extend the fake HTTP client test to assert no next lifecycle POST occurs before the preceding terminal GET result; represent deletion completion by the API's actual deleted terminal response.
- [ ] Run the focused unittest; expect ordering failure.
- [ ] Add explicit waits after each lifecycle action and use waited documents for evidence.
- [ ] Re-run focused tests; expect PASS.

### Task 11: Make and container gates (#7, #8, #26, #27, #37)

**Files:**
- Modify: `repo/Makefile`
- Modify: the CI/workflow installer file identified by the existing kubectl download assertion
- Modify: `repo/services/reconcile-worker/Dockerfile`
- Test: `repo/scripts/validate_ci_workflow_test.py` and the existing Make/production-shape validator tests that cover these targets.

**Interfaces:**
- Produces: two target completion messages and help entries; kubectl `v1.36.1` download plus official SHA-256 verification; reconcile-worker `go build -tags stdjson`.

- [ ] Add static tests asserting help/receipt text, kubectl version/checksum verification, and the reconcile-worker build tag.
- [ ] Run the focused validator unittests; expect FAIL.
- [ ] Update Make recipes/help, installer version/checksum steps, and Dockerfile build flags using existing repository patterns.
- [ ] Re-run focused tests and `docker build` for reconcile-worker when the local daemon is available; expect PASS.

### Task 12: Full disposition and regression verification (#1-#40)

**Files:**
- Verify: `docs/superpowers/specs/2026-08-04-pr83-review-findings-remediation-design.md`
- Verify: all files changed by Tasks 1-11

**Interfaces:**
- Produces: final evidence mapping all 40 scanner IDs to fixed, merged, rejected, false-positive, or deferred status.

- [ ] Confirm accepted IDs #7-#12, #15, #17, #19, #22-#33, #35-#37, and #40 have passing regression evidence.
- [ ] Confirm rejected/deferred IDs #1-#6, #13-#14, #16, #18, #20-#21, #34, #38-#39 have no speculative production edits and retain rationale in the design.
- [ ] From `repo/`, run all focused tests, relevant `make validate-*` targets, `PATH=/tmp/ani-pybin:$PATH make test`, `make validate-architecture`, and `git diff --check`.
- [ ] Use `review-it` to review the final uncommitted diff; fix verified regressions through new RED/GREEN cycles.
- [ ] Verify `git stash list` still shows the network C1 stash and `git status --short` contains no restored network files.
