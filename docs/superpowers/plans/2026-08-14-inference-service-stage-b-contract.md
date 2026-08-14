# Inference Service Stage B Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Freeze the additive ANI Services InferenceService v1 contract required by Stage B of the approved inference-service design.

**Architecture:** `repo/api/openapi/services/v1.yaml` remains the sole Services product contract. This batch adds normalized inference resource schemas and lifecycle operations while preserving v1 compatibility; it does not implement handlers, persistence, Core SDK calls, Kubernetes resources, LWS, or an invocation gateway. A focused semantic gate locks the design-sensitive contract, and the existing route baseline records operations intentionally awaiting the later implementation PR.

**Tech Stack:** OpenAPI 3.1 YAML, Python 3.12 `unittest`/PyYAML contract gates, generated multi-language SDK metadata/docs, generated TypeScript schema.

## Global Constraints

- Work, validation, commit, push, and PR only from local `main`; no branch or worktree.
- Keep ANI Services resources in `repo/api/openapi/services/v1.yaml`; do not add inference business semantics to Core.
- Preserve existing v1 required request fields and responses unless the approved design explicitly fixes them additively.
- `invocation_url` and `endpoint_url` remain nullable compatibility fields; no internal ClusterIP endpoint enters the Services schema.
- This is a contract-only batch. Handler, store, worker, reconciler, runtime, provider, and live-gate code wait for contract approval.

---

### Task 1: Lock Stage B semantics with a failing contract test

**Files:**
- Create: `repo/scripts/validate_inference_service_contract.py`
- Create: `repo/scripts/validate_inference_service_contract_test.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Consumes: `repo/api/openapi/services/v1.yaml` as a parsed YAML mapping.
- Produces: `validate(spec) -> tuple[str, ...]`, where an empty tuple means the inference contract matches Stage B.

- [ ] **Step 1: Write the failing test**

Add fixture assertions requiring the normalized schemas and fields, PATCH/lifecycle/operation paths, AsyncTask responses, explicit security, policy `501`, nullable external endpoint fields, and deprecation of legacy endpoint schemas.

- [ ] **Step 2: Run the test to verify RED**

Run: `cd repo && python scripts/validate_inference_service_contract_test.py`

Expected: FAIL because the current Services OpenAPI lacks the Stage B paths and schemas.

- [ ] **Step 3: Add the minimum validator**

Implement path/schema lookup helpers and deterministic error messages. Add both the fixture test and validator command to `validate-services-contract` so future drift blocks `make validate-services`.

- [ ] **Step 4: Re-run the focused test**

Run: `cd repo && python scripts/validate_inference_service_contract_test.py`

Expected: still FAIL against the unchanged Services OpenAPI, proving the gate detects the missing contract.

### Task 2: Add the Stage B Services OpenAPI contract

**Files:**
- Modify: `repo/api/openapi/services/v1.yaml`
- Modify: `repo/architecture/services-contract-baseline.yaml`
- Modify: `repo/architecture/services-route-baseline.yaml`

**Interfaces:**
- Consumes: approved inference design Sections 6, 8, and 14.
- Produces: additive InferenceService create/update/lifecycle/operation schemas and paths.

- [ ] **Step 1: Add normalized schemas**

Add `InferenceServiceResources`, optional `InferenceServiceAccelerator`, `CreateInferenceServiceRequest`, `UpdateInferenceServiceRequest`, and `InferenceServiceLifecycleRequest`. Keep legacy `model`, `gpu_type`, `gpu_count_per_pod`, and `max_concurrency` as deprecated compatibility inputs.

- [ ] **Step 2: Extend the resource response**

Add `model_version_id`, `served_model_name`, `ready_replicas`, normalized resources, placement, diagnostics, generation fields, `current_operation_id`, `invocation_url`, and `updated_at`. Mark both endpoint URL fields nullable and never expose `runtime_endpoint` or `runtime_ref`.

- [ ] **Step 3: Add operations**

Declare PATCH, lifecycle POST, and inference-operation GET with the approved response/status semantics and authentication. Add explicit security to all inference operations and add policy `501 FEATURE_NOT_AVAILABLE` semantics without implementing the P1 GET policy path.

- [ ] **Step 4: Preserve compatibility and update exact baselines**

Mark `InferenceEndpoint` and `CreateInferenceEndpointRequest` deprecated. Remove resolved inference security and PATCH route exceptions; add exact route exceptions for lifecycle and operation-query paths awaiting the implementation batch.

- [ ] **Step 5: Verify GREEN**

Run: `cd repo && python scripts/validate_inference_service_contract_test.py && python scripts/validate_inference_service_contract.py`

Expected: PASS and `inference service contract valid`.

### Task 3: Regenerate artifacts and validate the contract batch

**Files:**
- Modify generated files under `repo/sdks/services/`, `repo/docs/api/`, and `repo/frontends/console/src/api/schema.d.ts` only as produced from the Services OpenAPI source.

**Interfaces:**
- Consumes: updated Services OpenAPI.
- Produces: synchronized SDK metadata/client surfaces, static API docs, and Console types.

- [ ] **Step 1: Regenerate artifacts**

Run: `cd repo && python scripts/gen_sdk_alpha.py && python scripts/generate_api_docs.py && npm --prefix frontends/console run gen-api`

Expected: generated outputs reflect the new paths and schemas.

- [ ] **Step 2: Run focused gates**

Run: `cd repo && make validate-openapi-spec && make validate-services-contract && make validate-services-route-contract`

Expected: all commands exit 0; only exact accepted route/legacy warnings remain.

- [ ] **Step 3: Run the full Services and repository gates**

Run: `cd repo && make validate-services && make test && make validate-architecture && git diff --check`

Expected: all commands exit 0 and generated-artifact drift checks are clean.

- [ ] **Step 4: Review scope**

Run: `git status --short && git diff --stat && git diff -- repo/api/openapi/services/v1.yaml repo/scripts/validate_inference_service_contract.py repo/scripts/validate_inference_service_contract_test.py repo/architecture/services-contract-baseline.yaml repo/architecture/services-route-baseline.yaml repo/Makefile`

Expected: only Stage B contract, its semantic gate, exact baselines, generated artifacts, and this plan are changed; the pre-existing untracked Core contract plan remains untouched.
