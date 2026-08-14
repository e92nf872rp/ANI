# Platform Workloads Core Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the additive ANI Core `platform-workloads` OpenAPI contract required by inference-service, with a CPU Deployment request example and no runtime implementation.

**Architecture:** Core exposes a service-identity-only, provider-neutral PlatformWorkload resource. The contract accepts container intent, optional GPUSpec-backed accelerator resources, and either single-node or leader-worker topology; all mutations are asynchronous and reuse Core AsyncTask. This batch changes only contract, contract tests, generated SDK/docs metadata, and required development records.

**Tech Stack:** OpenAPI 3.1 YAML, Python `unittest`, repository SDK/API-doc generators, ANI validation Make targets.

## Global Constraints

- Work directly on `main` as explicitly authorized by the user.
- Do not add handlers, ports, adapters, Kubernetes objects, database code, or gateway routing in this batch.
- Services may consume this contract only through generated Core SDKs.
- `internal_endpoint` is service-identity-only and must never become an `/instances` or Services response field.
- Every mutation returns `202 + AsyncTask` and supports idempotency.
- Do not commit, push, or create a PR without a separate explicit user request.

---

### Task 1: Freeze the semantic contract with a failing test

**Files:**
- Modify: `repo/scripts/validate_openapi_spec_test.py`

**Interfaces:**
- Consumes: `repo/api/openapi/v1.yaml`
- Produces: `test_platform_workload_service_contract_is_frozen`

- [ ] Add a test asserting all PlatformWorkload paths, operation IDs, RBAC scopes, schemas, AsyncTask enums, topology rules, and endpoint visibility.
- [ ] Run `python3 scripts/validate_openapi_spec_test.py` from `repo/` and confirm the test fails because the contract is absent.

### Task 2: Add the Core OpenAPI contract

**Files:**
- Modify: `repo/api/openapi/v1.yaml`

**Interfaces:**
- Produces: `PlatformWorkloadCapabilities`, `PlatformWorkloadCreateRequest`, `PlatformWorkloadUpdateRequest`, `PlatformWorkloadLifecycleRequest`, `PlatformWorkload`, and log schemas.
- Produces paths: capabilities, create/get/update/delete, lifecycle, and logs.

- [ ] Add provider-neutral schemas with strict required fields and descriptions.
- [ ] Add a CPU Deployment example using a digest-pinned image, ClusterIP-only exposure, and no accelerator object.
- [ ] Add PlatformWorkload task/resource enum values to AsyncTask.
- [ ] Add service-only paths with read/write RBAC scopes, standard errors, `Location` headers, and `202 + AsyncTask` mutation responses.
- [ ] Add the `PlatformWorkloads` Swagger tag.
- [ ] Run the focused Python test and confirm it passes.

### Task 3: Refresh generated contract artifacts

**Files:**
- Modify generated Core SDK metadata/sources selected by `make gen-core-sdk`.
- Modify generated static API docs selected by `make gen-api-docs`.

**Interfaces:**
- Consumes: Core OpenAPI operation IDs and schemas.
- Produces: generated clients/types/docs only; no handwritten runtime behavior.

- [ ] Run `make gen-core-sdk`.
- [ ] Run `make gen-api-docs`.
- [ ] Review generated diffs for Core-only placement and absence of Services resource leakage.

### Task 4: Run contract gates and stop for review

**Files:**
- Review all changed files; update required feature-batch records only if the contract batch is being declared complete.

**Interfaces:**
- Produces: fresh verification evidence, not a runtime-ready claim.

- [ ] Run `make validate-openapi-spec`.
- [ ] Run `make validate-core-api-compatibility`.
- [ ] Run `make validate-spec-split`.
- [ ] Run `make validate-doc-api` and SDK generation drift checks used by the repository.
- [ ] Run `make validate-architecture` and `git diff --check`.
- [ ] Review `git diff` and stop at the contract approval gate without implementing handlers.
