# Inference GPUSpec IDs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Accept the public GPU catalog ID in inference creation and render matching GPU resources.
**Architecture:** Core capability discovery joins GPUSpecStore with GPU inventory. Services resolves compatible historical aliases using Core REST metadata; Core validates and freezes the scheduling result before applying Kubernetes manifests.
**Tech Stack:** Go, existing Core ports/adapters, Kubernetes REST client, Volcano, OpenAPI.

## Global Constraints
- Approved design: `docs/superpowers/specs/2026-09-28-inference-accelerator-spec-id-design.md`.
- Main only, preserve prior changes, no commit/push/deploy; frontend source is in another repository.
- New Services requests use `resources.accelerator.spec_id` from catalog `items[].id`, positive count_per_replica; Core uses count.
- Wholecard omits memory; vGPU supplies memory equal to selected spec mb_per_share, positive MiB. Unknown/disabled/unmatched specs cannot fall back to CPU or arbitrary model.
- Internal legacy model aliases can resolve only unambiguously by mode/memory. Add optional `aliases`, `gpu_mode`, `memory_per_share_mib` to capability metadata; no provider objects across Core/Services boundary.

### Task 1: Public contracts
**Files:** `repo/api/openapi/v1.yaml`, `repo/api/openapi/services/v1.yaml`.
- [ ] Change spec_id documentation to GPUSpec ID with deprecated model alias compatibility; add optional capability metadata.
```yaml
gpu_mode: { type: string, enum: [wholecard, vgpu] }
memory_per_share_mib: { type: integer, minimum: 1 }
aliases: { type: array, items: { type: string } }
```
- [ ] Keep quantity types and field names stable. Explicitly document catalog availability versus free capacity.

### Task 2: Core catalog admission and rendering
**Files:** `repo/pkg/ports/platform_workload.go`, `repo/pkg/adapters/runtime/kubernetes_platform_workload*.go`, `local_platform_workload.go`, `repo/services/ani-gateway/internal/router/platform_workloads.go`.
**Interfaces:** Existing GPUSpecStore + GPUInventory; optional metadata in PlatformWorkloadAcceleratorCapability; Core internal scheduling snapshot stays out of REST request.
- [ ] Add regression with CRD `rtx4090-12g-4` and matching vGPU node. Capabilities must include canonical ID. Create must preserve selected ID, request Volcano number/memory, and constrain node mode/type.
- [ ] Run `cd repo && go test ./pkg/adapters/runtime -run 'Test.*PlatformWorkload.*GPUSpec' -count=1` and observe failure.
- [ ] Resolve exact IDs before alias matching. Reject ambiguous alias/mode-memory mismatch as ErrInvalid; unavailable specs as ErrFailedPrecondition. Reuse existing GPU labels and Volcano translation rules.
- [ ] Freeze resolved scheduling metadata in workload spec; replay existing intents without depending on mutable catalog availability. Validate role resources consistently for leader-worker groups.
- [ ] Test wholecard, vGPU, invalid ID, disabled spec, mismatched memory/mode, ambiguous aliases, retries and CPU regressions. Run runtime package tests.

### Task 3: Services normalization
**Files:** `repo/services/inference-service/internal/runtime/{runtime.go,coresdk/adapter.go}`, planner and creator tests; Gateway accelerator input validation.
- [ ] Decode Core metadata and resolve old alias before planning/persistence, keeping original request fingerprint stable for idempotency.
- [ ] Test alias normalization and canonical ID traversal to Core. Never strip suffixes from public catalog IDs. Unknown `acc` and nested `accelerator.count` must fail visibly, not downgrade to CPU.
- [ ] Run `cd repo && go test ./services/inference-service/... ./services/ani-gateway/... -count=1`.

### Task 4: Generated artifacts, review, verification
**Files:** Core/Services SDKs and API docs; `repo/development-records/inference-gpu-spec-and-list-a.md` and indexes.
- [ ] Regenerate using existing make targets, run relevant OpenAPI compatibility/services/architecture gates and `make test`; review diff and run `git diff --check`.
- [ ] Check old cluster read-only for CRD fields and resource units. Do not claim a live rollout from local tests; deployment/real inference remains separately reported.
