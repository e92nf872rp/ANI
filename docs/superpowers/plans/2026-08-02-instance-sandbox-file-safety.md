# Instance Sandbox File Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent Sandbox file list, write, and delete operations from escaping `/workspace` through symbolic links, hard links, or directory rename races while preserving the frozen Core v1 HTTP contract.

**Architecture:** Keep lexical request validation in `LocalSandboxRuntime`, mount an isolated `emptyDir` at `/workspace`, and enforce descriptor-relative containment inside the Sandbox pod scripts. Traverse path components from the workspace directory descriptor with `O_NOFOLLOW`, reject multi-link write targets before truncation, and map unsafe paths to `ports.ErrInvalid` so the Gateway returns HTTP 400.

**Tech Stack:** Go, embedded Python 3 pod scripts, Hertz Gateway, Core OpenAPI v1.

## Global Constraints

- `repo/api/openapi/v1.yaml` is authoritative; this batch does not change it.
- File write remains `POST` with HTTP 201; file delete remains `DELETE` with `Idempotency-Key` and HTTP 204.
- Cross-tenant resources remain 404; unsupported kind/state/provider remains 422.
- Do not follow symbolic links during list, write, or delete.
- Keep `/workspace` on an isolated filesystem so hard links and directory renames cannot cross the workspace boundary.
- Do not commit without explicit user confirmation.

---

### Task 1: Reproduce symlink escapes

**Files:**
- Modify: `repo/pkg/adapters/runtime/kubernetes_sandbox_runtime_test.go`

- [x] Add tests that execute the embedded Python scripts against temporary directories.
- [x] Cover a symlinked list root, symlinked write parent, symlinked write target, and symlinked delete target.
- [x] Cover an existing hard-linked write target.
- [x] Run `go test ./pkg/adapters/runtime -run 'TestSandboxFileScriptsRejectSymlinks' -count=1` and confirm it fails because the current scripts follow symlinks.

### Task 2: Enforce descriptor-relative containment

**Files:**
- Modify: `repo/pkg/adapters/runtime/kubernetes_sandbox_files.go`
- Test: `repo/pkg/adapters/runtime/kubernetes_sandbox_runtime_test.go`

- [x] Add a dedicated unsafe-path exit code and map it to `ports.ErrInvalid`.
- [x] Open `/workspace` and each directory component with `O_DIRECTORY|O_NOFOLLOW`.
- [x] Open write targets with `dir_fd` and `O_NOFOLLOW`; delete with the verified parent `dir_fd`.
- [x] Mount an isolated `emptyDir` at `/workspace` and reject multi-link write targets before truncation.
- [x] List through a verified directory descriptor without following directory symlinks.
- [x] Run the focused adapter tests and confirm they pass.

### Task 3: Verify the v1 HTTP boundary

**Files:**
- Modify only if coverage is missing: `repo/services/ani-gateway/internal/router/instances_test.go`

- [x] Confirm unsafe paths map to HTTP 400 through `writeSandboxRuntimeError`.
- [x] Confirm payload-too-large remains 413, conflict remains 409, and successful delete remains 204.
- [x] Run `go test ./services/ani-gateway/internal/router -run 'Test.*SandboxFile' -count=1`.

### Task 4: Validate and record the feature batch

**Files:**
- Create: `repo/development-records/instance-sandbox-file-safety-a.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

- [x] Run focused adapter and Gateway tests.
- [x] Run `make validate-openapi-spec`, `make validate-core-api-compatibility`, `make validate-architecture`, and `make test`.
- [x] Run `git diff --check`.
- [x] Record exact verification evidence and retain the no-commit user confirmation gate.

### Task 5: Run the real Sandbox file-safety gate

**Files:**
- Modify: `repo/scripts/validate_sandbox_live_gate.py`
- Modify: `repo/scripts/validate_sandbox_live_gate_test.py`
- Modify: `repo/deploy/real-k8s-lab/instance-sandbox-live-gate.yaml`
- Create: `repo/development-records/instance-sandbox-file-safety-live-gate-a.md`
- Create: `repo/development-records/live-evidence/instance-sandbox-file-safety-live-20260802.json`

- [x] Require the Sandbox Deployment to mount an isolated `emptyDir` at `/workspace`.
- [x] Create symlink and hard-link fixtures through real Sandbox code-run.
- [x] Confirm unsafe list/write/delete operations return HTTP 400 and do not mutate external content.
- [x] Build, push, and roll out `ani-gateway:instance-sandbox-file-safety-20260802-v1`.
- [x] Run the enhanced live gate, archive evidence, and remove the test Sandbox through Core lifecycle delete.

### Task 6: Clear stale instance data and revalidate PostgreSQL persistence

**Files:**
- Create: `repo/development-records/instance-pg-clean-revalidation-a.md`
- Create: `repo/development-records/live-evidence/instance-sandbox-post-clean-live-20260802.json`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

- [x] Back up PostgreSQL before deleting instance-management data.
- [x] Delete only workload identities and the four instance-management persistence tables in one transaction.
- [x] Restart Gateway and reconcile-worker, then verify the Core instance list is empty.
- [x] Re-run the full Sandbox live gate and retain only the new deleted instance audit trail.
- [x] Confirm Kubernetes has no matching Deployment, Pod, or Service after lifecycle delete.
- [x] Record the unresolved provider 404 reconciliation mapping risk without claiming it fixed.
