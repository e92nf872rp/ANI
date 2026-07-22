# Harbor Registry Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the approved Core API v1 registry contract with the in-cluster Harbor and Trivy deployment without exposing administrator credentials.

**Architecture:** Gateway depends on `ports.ImageRegistry`; a `HarborImageRegistry` adapter is selected only by `REGISTRY_PROVIDER_MODE=harbor`. Kubernetes pull-secret writes use a dedicated ANI port. `local` remains default and Harbor failures never return local data.

**Tech Stack:** Go 1.25, Hertz, Harbor 2.15 REST API, Kubernetes REST API, Go `net/http`.

## Constraints

- Do not change `repo/api/openapi/v1.yaml`; the registry v1 contract is approved.
- Use TDD: start each new behavior with an observed failing test.
- No Harbor SDK or Harbor HTTP calls in Gateway handlers.
- Only Gateway receives Harbor administrator credentials; workloads receive project-scoped pull-only Robot credentials.
- The approved repository-permission endpoint maps onto Harbor project members:
  `pull`/`scan` -> Guest (4), `push` -> Developer (3), and `delete` ->
  Maintainer (2). The returned ANI repository remains the requested repository,
  but Harbor grants the role project-wide.
- Leave `.gitignore` and `repo/tools/kms-sm4-live-fixture/go.sum` unstaged.

## Task 1: Expand The Registry Boundary

**Files:** Modify `repo/pkg/ports/image_registry.go`, `repo/pkg/adapters/registry/local_image_registry.go`, and `repo/pkg/adapters/registry/not_configured.go`; test `repo/pkg/adapters/registry/local_image_registry_test.go`.

- [ ] Write a failing test that calls `GetOverview` and `ListImages` on `LocalImageRegistry`.
- [ ] Run `cd repo && go test ./pkg/adapters/registry -run TestLocalImageRegistryImplementsConsoleRegistryOperations -count=1`; expect missing methods.
- [ ] Add v1-internal overview, image-list, push-instruction, tag-delete, and tag-reference DTOs/methods. Add `RegistryPullSecretWriter.ApplyRegistryPullSecret(context.Context, RegistryPullSecretWriteRequest) error` and `RegistryImageReferenceReader.ListRegistryImageReferences(context.Context, RegistryImageReferenceListRequest) (RegistryImageReferenceListResult, error)`.
- [ ] Add minimal local and `NotConfigured` implementations, then run `cd repo && go test ./pkg/adapters/registry -count=1`; expect PASS.

## Task 2: Build Harbor HTTP Foundation

**Files:** Create `repo/pkg/adapters/registry/harbor_image_registry.go` and `repo/pkg/adapters/registry/harbor_image_registry_test.go`.

- [ ] Write `TestHarborImageRegistryCreatesTenantProject` with `httptest.Server`, asserting `POST /api/v2.0/projects` and Basic authentication.
- [ ] Run `cd repo && go test ./pkg/adapters/registry -run TestHarborImageRegistryCreatesTenantProject -count=1`; expect missing `NewHarborImageRegistry`.
- [ ] Implement `NewHarborImageRegistry(HarborImageRegistryConfig)` with endpoint, username, password, optional HTTP client, timeout, and pull-secret writer. Map 404 to `ports.ErrNotFound`, 409 to `ports.ErrConflict`, malformed responses to a typed provider protocol error, and do not retain upstream bodies.
- [ ] Run `cd repo && go test ./pkg/adapters/registry -count=1`; expect PASS.

## Task 3: Map Harbor Registry Operations

**Files:** Modify the Harbor adapter and test from Task 2.

- [ ] Add failing fake-Harbor tests for project/repository/artifact pagination, Trivy severity mapping, absent scan overview, overview, image list, push instructions, permissions, and upstream 503.
- [ ] Run `cd repo && go test ./pkg/adapters/registry -run TestHarborImageRegistry -count=1`; expect failed assertions.
- [ ] Implement all approved v1 mappings, return `DevProfileInfo{Mode: "provider", Provider: "harbor", RealProvider: true}`, use `not_scanned` when scan data is absent, and upsert Harbor project membership for every approved permission action set using the documented role mapping.
- [ ] Run `cd repo && go test ./pkg/adapters/registry -count=1`; expect PASS.

## Task 4: Pull-Only Robot Secret And Tag Safety

**Files:** Create `repo/pkg/adapters/runtime/kubernetes_registry_pull_secret.go` and test; modify the Harbor adapter and test.

- [ ] Write failing tests proving a namespace-specific `kubernetes.io/dockerconfigjson` Secret is applied through `KubernetesRESTClient.ApplyManifests`, only a pull Robot Account is created/reused, administrator password is absent, and references block deletion.
- [ ] Run `cd repo && go test ./pkg/adapters/runtime ./pkg/adapters/registry -run 'Test(KubernetesRegistryPullSecretWriter|HarborImageRegistry)' -count=1`; expect failures.
- [ ] Implement `stringData[".dockerconfigjson"]`, project-scoped pull-only Robot creation/reuse, and `ports.ErrConflict` before Harbor DELETE when `RegistryImageReferenceReader` returns references. Reader errors also block deletion and do not issue Harbor DELETE.
- [ ] Run `cd repo && go test ./pkg/adapters/runtime ./pkg/adapters/registry -count=1`; expect PASS.

## Task 5: Gateway Selection And HTTP Injection

**Files:** Create `repo/services/ani-gateway/registry_runtime.go` and test; modify `repo/services/ani-gateway/main.go`, `repo/services/ani-gateway/internal/router/router.go`, `repo/services/ani-gateway/internal/router/registry_resources.go`, and router tests.

- [ ] Write failing tests for default local mode, missing Harbor credentials, injected registry use, and existing internal-error mapping for Harbor provider failures.
- [ ] Run `cd repo && go test ./services/ani-gateway ./services/ani-gateway/internal/router -run 'Test(NewGatewayRegistry|RegistryHTTP)' -count=1`; expect failures.
- [ ] Implement `newGatewayRegistry` using `REGISTRY_PROVIDER_MODE`, `HARBOR_ENDPOINT`, `HARBOR_USERNAME`, `HARBOR_PASSWORD`, `KUBERNETES_*`, and `REGISTRY_PULL_SECRET_FIELD_MANAGER`; inject a `RegistryImageReferenceReader` backed by `WorkloadInstanceStore`. Empty/local builds local; harbor builds real; unknown/incomplete configuration fails startup. Register approved v1 routes exactly once and preserve the approved internal-error behavior for provider failures.
- [ ] Run `cd repo && go test ./services/ani-gateway ./services/ani-gateway/internal/router -count=1`; expect PASS.

## Task 6: Deployment, RBAC, And Controlled Live Gate

**Files:** Modify Gateway deployment/RBAC manifests found by `rg -l 'name: ani-gateway' deploy repo/deploy`; create `repo/scripts/validate_harbor_registry_live_gate.py` and test; update `repo/Makefile` and required feature-batch records.

- [ ] Write failing checks for a non-secret `HARBOR_ENDPOINT`, `secretKeyRef` credentials, and rejection of missing `--allow-write` or `--cleanup-confirmed`.
- [ ] Inject no literal credential; grant Gateway only Secret `get`, `create`, `patch`, and `update` in permitted tenant namespaces.
- [ ] Implement a gate using an isolated project: Gateway-to-Harbor health, registry/scan response, pull-only robot, dockerconfigjson Secret, redacted evidence, and cleanup limited to explicit temporary resource names.
- [ ] Before any live write, obtain explicit approval for the targets and cleanup scope. Then run `make test`, `make validate-architecture`, `make validate-core-api-compatibility`, `make validate-doc-entrypoints`, and `git diff --check`; expect PASS.

## Self-Review

- The tasks cover all approved v1 operations, real-provider selection, Robot credentials, Secret writes, error mapping, RBAC, live evidence, and feature-batch records.
- Runtime and router use `ports.ImageRegistry`; the Harbor adapter reaches Kubernetes only through `RegistryPullSecretWriter`.
