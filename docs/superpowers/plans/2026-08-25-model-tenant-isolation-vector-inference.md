# Model Tenant Isolation and Vector Inference Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent cross-tenant model reads and mutations, and allow embedding-only model versions to deploy and reach `running` with vLLM embedding launch and readiness semantics.

**Architecture:** Make tenant ownership an explicit compiler-visible argument at every ModelRepository operation while retaining PostgreSQL RLS as a second guard. Derive an internal inference task from the existing immutable model capabilities, freeze it in `ExecutionProfile`, and use it consistently for vLLM launch and readiness smoke. No public OpenAPI or database schema change is required.

**Tech Stack:** Go 1.25, gRPC, pgx v5, PostgreSQL RLS, vLLM OpenAI-compatible HTTP API, Python/Make repository validators.

## Global Constraints

- Work only on local `main`; do not create a branch or worktree.
- Do not restore or modify `stash@{0}` during this plan.
- Do not stage, commit, push, apply, or mutate the live cluster without a new explicit authorization.
- Preserve unrelated dirty-worktree files.
- Cross-tenant IDs must return NotFound semantics and must not disclose existence.
- Keep PostgreSQL RLS enabled; explicit SQL tenant predicates are defense in depth, not a replacement.
- Derive task from `Model.capabilities`; do not add a public task selector.
- Empty persisted task means `generate` for backward compatibility.
- `speech-to-text` remains unsupported.
- Public `/v1/embeddings` Envoy routing is outside this plan.

## File structure

- `repo/services/model-service/internal/repo/model_repo.go`: compiler-visible tenant repository arguments and tenant-fenced SQL.
- `repo/services/model-service/internal/repo/model_repo_test.go`: SQL fence regression contracts.
- `repo/services/model-service/internal/service/model_service.go`: defense-in-depth ownership checks and tenant argument propagation.
- `repo/services/model-service/internal/service/model_service_test.go`: cross-tenant service response regression tests.
- `repo/services/inference-service/internal/domain/resource.go`: persisted internal `InferenceTask` and execution-profile field.
- `repo/services/inference-service/internal/catalog/catalog.go`: task-bearing engine profile.
- `repo/services/inference-service/internal/catalog/modelsvc/adapter.go`: capability-to-task/profile resolution.
- `repo/services/inference-service/internal/catalog/modelsvc/adapter_test.go`: task derivation regression tests.
- `repo/services/inference-service/internal/service/service.go`: freeze selected profile task.
- `repo/services/inference-service/internal/service/service_test.go`: creation persistence and compatibility tests.
- `repo/services/inference-service/internal/engine/launch.go`: task-aware generated vLLM command.
- `repo/services/inference-service/internal/engine/launch_test.go`: embedding launch regression tests.
- `repo/services/inference-service/internal/runtime/runtime.go`: task-aware runtime smoke interface.
- `repo/services/inference-service/internal/runtime/coresdk/adapter.go`: embeddings request/response smoke implementation.
- `repo/services/inference-service/internal/runtime/coresdk/adapter_test.go`: generate/embed probe tests.
- `repo/services/inference-service/internal/runtime/fake/fake.go`: interface-compatible fake runtime.
- `repo/services/inference-service/internal/reconcile/worker.go`: pass target/rollback frozen task to smoke.
- `repo/services/inference-service/internal/reconcile/worker_test.go`: task propagation assertions.
- `repo/development-records/model-tenant-isolation-vector-inference.md`: implementation and verification record.
- `repo/development-records/README.md`, `repo/CURRENT-SPRINT.md`, `ANI-06-开发计划.md`: feature-batch status updates required by repository rules.

---

### Task 1: Enforce tenant ownership in every ModelRepository operation

**Files:**

- Modify: `repo/services/model-service/internal/repo/model_repo.go`
- Create: `repo/services/model-service/internal/repo/model_repo_test.go`
- Modify: `repo/services/model-service/internal/service/model_service.go`
- Modify: `repo/services/model-service/internal/service/model_service_test.go`

**Interfaces:**

- `GetByID(context.Context, *pgxpool.Pool, tenantID, modelID uuid.UUID) (*Model, error)`
- `GetVersionByID(context.Context, *pgxpool.Pool, tenantID, versionID uuid.UUID) (*Model, *ModelVersion, error)`
- `ListFilter` gains `TenantID uuid.UUID`.
- `SoftDelete(context.Context, pgx.Tx, tenantID, modelID uuid.UUID) error`
- `CreateVersionReq` gains `TenantID uuid.UUID`.
- `ListVersions(context.Context, *pgxpool.Pool, tenantID, modelID uuid.UUID) ([]*ModelVersion, error)`

- [ ] **Step 1: Write service-level failing tests for the observed leak**

Extend `stubRepo` so Get/List can return configured values, then add tests with
request tenant A and repository rows owned by tenant B:

```go
func TestGetModelRejectsForeignTenantResult(t *testing.T) {
    tenantA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
    tenantB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
    modelID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
    svc := NewModelService(nil, &stubRepo{model: &repo.Model{TenantID: tenantB, ID: modelID}})

    _, err := svc.GetModel(context.Background(), &modelv1.GetModelRequest{
        TenantId: tenantA.String(), ModelId: modelID.String(),
    })
    if status.Code(err) != codes.NotFound {
        t.Fatalf("code = %v, want NotFound", status.Code(err))
    }
}

func TestListModelsFailsClosedOnForeignTenantResult(t *testing.T) {
    tenantA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
    tenantB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
    svc := NewModelService(nil, &stubRepo{models: []*repo.Model{{TenantID: tenantB, ID: uuid.New()}}})

    _, err := svc.ListModels(context.Background(), &modelv1.ListModelsRequest{TenantId: tenantA.String()})
    if status.Code(err) != codes.Internal {
        t.Fatalf("code = %v, want Internal fail-closed response", status.Code(err))
    }
}
```

- [ ] **Step 2: Run the focused tests and capture RED**

Run:

```bash
cd /root/kubercon/ANI/repo/services/model-service
GOWORK=off go test ./internal/service -run 'Test(GetModelRejectsForeignTenantResult|ListModelsFailsClosedOnForeignTenantResult)' -count=1 -v
```

Expected: both tests fail because the current service returns the foreign model data.

- [ ] **Step 3: Add compiler-visible tenant arguments and service checks**

Update the repository interface with the signatures above. At every service call,
pass the parsed `tenantID`. Add this helper and use it after Get/GetVersion and
for every List row:

```go
func modelOwnedBy(model *repo.Model, tenantID uuid.UUID) bool {
    return model != nil && tenantID != uuid.Nil && model.TenantID == tenantID
}
```

For Get/GetVersion mismatch, return `toStatus(types.Wrapf(types.ErrNotFound,
"model not found"))`. For a foreign row in List, return a sanitized Internal
error because the repository violated its contract; never filter while retaining
a foreign-derived total/cursor.

- [ ] **Step 4: Write repository SQL fence tests before changing SQL**

Move the authoritative statements into package constants or small builders used
by production code, then add `model_repo_test.go` tests that normalize whitespace
and assert these exact ownership relationships:

```go
func TestModelQueriesContainExplicitTenantFence(t *testing.T) {
    checks := map[string][]string{
        "get":            {getModelByIDSQL, "id=$1", "tenant_id=$2"},
        "list":           {listModelsBaseWhere, "tenant_id=$1"},
        "delete":         {softDeleteModelSQL, "id=$1", "tenant_id=$2"},
        "create-version": {createModelVersionSQL, "FROM models", "tenant_id=$2"},
        "list-versions":  {listModelVersionsSQL, "JOIN models", "m.tenant_id=$2"},
    }
    for name, check := range checks {
        sql := strings.Join(strings.Fields(check[0]), " ")
        for _, fragment := range check[1:] {
            if !strings.Contains(sql, fragment) {
                t.Errorf("%s SQL lacks %q: %s", name, fragment, sql)
            }
        }
    }
}
```

- [ ] **Step 5: Run repository tests and capture RED**

Run:

```bash
cd /root/kubercon/ANI/repo/services/model-service
GOWORK=off go test ./internal/repo -run TestModelQueriesContainExplicitTenantFence -count=1 -v
```

Expected: compile failure for the not-yet-defined SQL constants/builders.

- [ ] **Step 6: Implement tenant-fenced SQL**

Use explicit predicates in the production statements:

```sql
-- Get
WHERE id=$1 AND tenant_id=$2 AND status <> 'deleted'

-- List/count base
WHERE tenant_id=$1 AND status <> 'deleted'

-- Delete
WHERE id=$1 AND tenant_id=$2 AND status <> 'deleted'

-- Create version: the INSERT source proves parent ownership
INSERT INTO model_versions (...)
SELECT $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9,
       NULLIF($10, ''), $11
FROM models
WHERE id=$1 AND tenant_id=$2 AND status <> 'deleted'
RETURNING ...

-- List versions
FROM model_versions v
JOIN models m ON m.id=v.model_id
WHERE v.model_id=$1 AND m.tenant_id=$2 AND m.status <> 'deleted'
```

Map zero-row version insert/update/delete results to `types.ErrNotFound`. Keep
`types.SetDBTenant` calls and transaction boundaries unchanged.

- [ ] **Step 7: Run focused and full model-service GREEN tests**

Run:

```bash
cd /root/kubercon/ANI/repo/services/model-service
GOWORK=off go test ./internal/repo ./internal/service -count=1 -v
GOWORK=off go test ./... -count=1
GOWORK=off go vet ./...
```

Expected: all commands exit 0; foreign Get is NotFound, foreign List fails closed,
and every repository SQL fence test passes.

- [ ] **Step 8: Review checkpoint; do not commit**

Run `git diff --check` and inspect only the four Task 1 files. Do not stage or
commit without explicit user authorization.

---

### Task 2: Derive and freeze the inference task from model capabilities

**Files:**

- Modify: `repo/services/inference-service/internal/domain/resource.go`
- Modify: `repo/services/inference-service/internal/catalog/catalog.go`
- Modify: `repo/services/inference-service/internal/catalog/modelsvc/adapter.go`
- Modify: `repo/services/inference-service/internal/catalog/modelsvc/adapter_test.go`
- Modify: `repo/services/inference-service/internal/service/service.go`
- Modify: `repo/services/inference-service/internal/service/service_test.go`

**Interfaces:**

- `domain.InferenceTask` with `InferenceTaskGenerate` and `InferenceTaskEmbed`.
- `domain.NormalizeInferenceTask(task InferenceTask) InferenceTask` maps empty
  and unknown persisted values to `generate`.
- `domain.ExecutionProfile.Task InferenceTask` persists inside existing JSONB.
- `catalog.EngineProfile.Task domain.InferenceTask` carries catalog selection.
- `Profiles` gains `EmbedCPU` and `EmbedGPU` vLLM profiles.

- [ ] **Step 1: Change the existing embedding-incompatible test to expected support**

Replace `TestResolveEmbeddingOnlyIsIncompatible` with:

```go
func TestResolveEmbeddingOnlySelectsVLLMEmbedProfiles(t *testing.T) {
    tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
    versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
    modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
    cat := mustCatalog(t, &stubClient{resp: readyResponse(
        tenantID, modelID, versionID, "safetensors", "ready", []string{"embedding"}, false, "",
    )})

    got, err := cat.Resolve(context.Background(), tenantID, versionID)
    if err != nil {
        t.Fatalf("Resolve: %v", err)
    }
    if got.CPUProfile == nil || got.GPUProfile == nil ||
        got.CPUProfile.Task != domain.InferenceTaskEmbed ||
        got.GPUProfile.Runtime != "vllm" {
        t.Fatalf("embedding profiles = cpu=%+v gpu=%+v", got.CPUProfile, got.GPUProfile)
    }
}
```

Add table tests for empty, generation, mixed generation+embedding, and
speech-to-text capability sets.

- [ ] **Step 2: Run catalog tests and capture RED**

Run:

```bash
cd /root/kubercon/ANI/repo/services/inference-service
GOWORK=off go test ./internal/catalog/modelsvc -run 'TestResolve(Embedding|Capability)' -count=1 -v
```

Expected: compile failure because task types/profiles are absent, or the current
embedding-only rejection fails the expected-success assertion.

- [ ] **Step 3: Implement task types and deterministic catalog selection**

Add to `domain/resource.go`:

```go
type InferenceTask string

const (
    InferenceTaskGenerate InferenceTask = "generate"
    InferenceTaskEmbed    InferenceTask = "embed"
)

func NormalizeInferenceTask(task InferenceTask) InferenceTask {
    if task == InferenceTaskEmbed {
        return InferenceTaskEmbed
    }
    return InferenceTaskGenerate
}
```

Add `Task InferenceTask` to `ExecutionProfile` with `json:"task,omitempty"`.
Add `Task domain.InferenceTask` to `catalog.EngineProfile`. Default generation
profiles use `generate`; new profile IDs `vllm-embed-cpu` and
`vllm-embed-gpu` use `embed` and runtime `vllm`.

Replace `supportsChat` with a deterministic resolver:

```go
func inferenceTask(capabilities []string) (domain.InferenceTask, bool) {
    if len(capabilities) == 0 {
        return domain.InferenceTaskGenerate, true
    }
    hasEmbedding := false
    for _, capability := range capabilities {
        switch strings.ToLower(strings.TrimSpace(capability)) {
        case "text-generation", "sglang":
            return domain.InferenceTaskGenerate, true
        case "embedding":
            hasEmbedding = true
        }
    }
    if hasEmbedding {
        return domain.InferenceTaskEmbed, true
    }
    return "", false
}
```

Embedding selects only `EmbedCPU`/`EmbedGPU`; generation retains the current
vLLM/SGLang selection. Validate every configured profile.

- [ ] **Step 4: Write a failing creator persistence test**

Add a test whose catalog returns an embed CPU profile and assert the stored
service contains `DesiredSpec.ExecutionProfile.Task == InferenceTaskEmbed`.
Also assert `NormalizeInferenceTask("") == InferenceTaskGenerate`.

- [ ] **Step 5: Run the creator test and capture RED**

Run:

```bash
cd /root/kubercon/ANI/repo/services/inference-service
GOWORK=off go test ./internal/service -run 'TestCreateFreezesEmbeddingTask|TestLegacyEmptyInferenceTaskDefaultsToGenerate' -count=1 -v
```

Expected: failure because Creator does not copy task into ExecutionProfile.

- [ ] **Step 6: Freeze the selected profile task and run GREEN tests**

In `Creator.Create`, set:

```go
Task: domain.NormalizeInferenceTask(profile.Task),
```

Run:

```bash
cd /root/kubercon/ANI/repo/services/inference-service
GOWORK=off go test ./internal/catalog/modelsvc ./internal/service -count=1 -v
```

Expected: all task derivation and creator persistence tests pass.

- [ ] **Step 7: Review checkpoint; do not commit**

Run `git diff --check` and inspect only Task 2 files. Do not stage or commit.

---

### Task 3: Generate task-aware vLLM launch commands

**Files:**

- Modify: `repo/services/inference-service/internal/engine/launch.go`
- Modify: `repo/services/inference-service/internal/engine/launch_test.go`

**Interfaces:**

- Consumes `domain.ExecutionProfile.Task` from Task 2.
- `Launch` and `LaunchLeader` signatures remain unchanged.

- [ ] **Step 1: Write failing CPU/GPU embedding launch tests**

```go
func TestLaunchVLLMEmbeddingAddsEmbedTask(t *testing.T) {
    for _, accelerator := range []*domain.Accelerator{nil, &domain.Accelerator{SpecID: "gpu-a100", CountPerReplica: 1}} {
        _, args := Launch(domain.Spec{
            CPU: "4", Memory: "16Gi", Accelerator: accelerator,
            ExecutionProfile: domain.ExecutionProfile{
                Runtime: "vllm", Task: domain.InferenceTaskEmbed,
                ArtifactRef: "pvc://vllm-model#/models/bge-m3",
            },
        }, "bge-m3")
        if !containsPair(args, "--task", "embed") {
            t.Fatalf("embedding args = %#v", args)
        }
    }
}

func TestLaunchVLLMGenerateDoesNotAddTaskOverride(t *testing.T) {
    _, args := Launch(domain.Spec{ExecutionProfile: domain.ExecutionProfile{
        Runtime: "vllm", Task: domain.InferenceTaskGenerate,
        ArtifactRef: "pvc://vllm-model#/models/qwen",
    }}, "qwen")
    if containsArg(args, "--task") {
        t.Fatalf("generate args changed: %#v", args)
    }
}
```

- [ ] **Step 2: Run engine tests and capture RED**

Run:

```bash
cd /root/kubercon/ANI/repo/services/inference-service
GOWORK=off go test ./internal/engine -run 'TestLaunchVLLM(Embedding|Generate)' -count=1 -v
```

Expected: embedding test fails because `--task embed` is absent; generation test passes.

- [ ] **Step 3: Add the minimal vLLM task argument**

After constructing the common vLLM server argv and before CPU/GPU-specific
flags, add:

```go
if domain.NormalizeInferenceTask(spec.ExecutionProfile.Task) == domain.InferenceTaskEmbed {
    server = append(server, "--task", "embed")
}
```

Do not rewrite tenant-provided complete commands and do not add SGLang embedding.

- [ ] **Step 4: Run focused and full engine tests**

Run:

```bash
cd /root/kubercon/ANI/repo/services/inference-service
GOWORK=off go test ./internal/engine -count=1 -v
```

Expected: all engine tests pass and existing generation argv remains unchanged.

- [ ] **Step 5: Review checkpoint; do not commit**

Run `git diff --check` and inspect the two Task 3 files. Do not stage or commit.

---

### Task 4: Use the frozen task for runtime readiness smoke

**Files:**

- Modify: `repo/services/inference-service/internal/runtime/runtime.go`
- Modify: `repo/services/inference-service/internal/runtime/coresdk/adapter.go`
- Modify: `repo/services/inference-service/internal/runtime/coresdk/adapter_test.go`
- Modify: `repo/services/inference-service/internal/runtime/fake/fake.go`
- Modify: `repo/services/inference-service/internal/reconcile/worker.go`
- Modify: `repo/services/inference-service/internal/reconcile/worker_test.go`

**Interfaces:**

- Replace `Smoke(ctx, tenantID, runtimeRef, servedModelName)` with
  `Smoke(ctx, tenantID, runtimeRef, servedModelName, task domain.InferenceTask)`.
- Replace internal `probeSmoke(..., servedModelName)` with
  `probeSmoke(..., servedModelName, task)`.

- [ ] **Step 1: Write failing generate/embed probe tests**

Use an `httptest.Server` that records path/body and returns task-appropriate JSON:

```go
func TestProbeSmokeUsesTaskEndpoint(t *testing.T) {
    tests := []struct {
        name, path, response string
        task domain.InferenceTask
    }{
        {"generate", "/v1/chat/completions", `{"choices":[{}]}`, domain.InferenceTaskGenerate},
        {"legacy-empty", "/v1/chat/completions", `{"choices":[{}]}`, ""},
        {"embed", "/v1/embeddings", `{"data":[{"embedding":[0.1]}]}`, domain.InferenceTaskEmbed},
    }
    // For each case, call probeSmoke and assert the exact path plus model value.
}
```

Add negative cases where embed returns no `data`, empty `data`, or non-JSON.

- [ ] **Step 2: Run coresdk tests and capture RED**

Run:

```bash
cd /root/kubercon/ANI/repo/services/inference-service
GOWORK=off go test ./internal/runtime/coresdk -run 'TestProbeSmokeUsesTaskEndpoint|TestProbeSmokeRejectsInvalidEmbeddingResponse' -count=1 -v
```

Expected: compile failure for the new task argument or path mismatch on embeddings.

- [ ] **Step 3: Implement task-aware payload and validation**

Normalize the task first. For embed use:

```go
path := "/v1/embeddings"
payload := map[string]any{"model": model, "input": []string{"ping"}}
```

For generate retain the existing chat payload unchanged. After HTTP 200, require:

```go
data, ok := decoded["data"].([]any)
if !ok || len(data) == 0 {
    return fmt.Errorf("runtime embedding smoke missing data")
}
```

Never log the response body or model contents.

- [ ] **Step 4: Write failing worker task-propagation tests**

Extend `runtimeStub` with `smokeTasks []domain.InferenceTask`. Its Smoke method
records the task. Add one create-completion test with embed target spec and one
rollback test with embed previous spec; assert the recorded task is `embed`.

- [ ] **Step 5: Run worker tests and capture RED**

Run:

```bash
cd /root/kubercon/ANI/repo/services/inference-service
GOWORK=off go test ./internal/reconcile -run 'Test.*Embedding.*Smoke' -count=1 -v
```

Expected: compile failure or recorded task remains empty because worker does not pass it.

- [ ] **Step 6: Propagate target and rollback task**

For normal reconciliation call Smoke with:

```go
operation.TargetSpec.ExecutionProfile.Task
```

For rollback call Smoke with:

```go
spec.ExecutionProfile.Task
```

Update exactly these three `Smoke` implementations without changing behavior
beyond accepting or recording the task:

- `internal/runtime/coresdk/adapter.go` (`*Runtime`)
- `internal/runtime/fake/fake.go` (`*Runtime`)
- `internal/reconcile/worker_test.go` (`*runtimeStub`)

- [ ] **Step 7: Run full inference-service GREEN tests**

Run:

```bash
cd /root/kubercon/ANI/repo/services/inference-service
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
```

Expected: all commands exit 0; embedding path and legacy generation behavior pass.

- [ ] **Step 8: Review checkpoint; do not commit**

Run `git diff --check` and inspect only Task 4 files. Do not stage or commit.

---

### Task 5: Record and validate the combined feature batch

**Files:**

- Create: `repo/development-records/model-tenant-isolation-vector-inference.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

**Interfaces:** None; documentation must reflect only freshly verified facts.

- [ ] **Step 1: Write the feature-batch record**

Record the root cause, exact tenant fences, capability-to-task rules, backward
compatibility, files changed, RED evidence, GREEN commands, and remaining
boundary that public `/v1/embeddings` Envoy routing is not part of this batch.
Do not include credentials, cluster endpoints, model data, or unverified live claims.

- [ ] **Step 2: Update the required feature-batch indexes**

Add one concise current-status entry to `repo/development-records/README.md`,
`repo/CURRENT-SPRINT.md`, and ANI-06 Section zero. Do not edit `CLAUDE.md` or
`ANI-DOCS-INDEX.md` because the project phase and entrypoint do not change.

- [ ] **Step 3: Run final repository verification**

Run:

```bash
cd /root/kubercon/ANI/repo
GOWORK=off go test ./services/model-service/... ./services/inference-service/... -count=1
make validate-services
make validate-architecture
make validate-doc-entrypoints
make test
cd /root/kubercon/ANI
git diff --check
git status --short
```

If `GOWORK=off` cannot resolve multiple module paths from `repo/`, instead run
the already specified full module tests inside each service directory and
record that exact substitution. Do not modify `go.work.sum` merely to satisfy a
local toolchain mismatch.

Expected: relevant service tests and all applicable gates exit 0; status shows
only this task's files plus the pre-existing untracked user files. Any generated
drift from `make validate-services` must be reviewed and either included only
when contract-derived or reverted by explicit-path patching before completion.

- [ ] **Step 4: Security and compatibility review**

Confirm from the final diff:

- every model query/mutation contains explicit tenant ownership;
- no list total/cursor derives from foreign rows;
- cross-tenant IDs use NotFound;
- old execution profiles with empty task normalize to generate;
- embedding-only selects vLLM, launches in embed mode, and uses embeddings smoke;
- no public OpenAPI, Envoy stash, credential, or live-cluster resource changed.

- [ ] **Step 5: Stop before commit**

Report verified results and remaining gaps. Do not stage, commit, restore the
Envoy stash, push, or perform live rollout until the user explicitly requests it.
