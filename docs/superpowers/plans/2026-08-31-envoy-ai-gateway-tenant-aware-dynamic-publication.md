# Envoy AI Gateway Tenant-Aware Dynamic Publication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在单一 Envoy AI Gateway 公网入口上，以 ANI AK 解析出的租户和标准 OpenAI 请求体 `model` 精确路由到当前租户已发布的 chat 或 embeddings 推理服务，并把服务发布、撤销、限流和调用地址纳入持久化生命周期。

**Architecture:** inference-service 保存发布期望/观察状态并按 `(tenant_id, served_model_name, OpenAI path)` 解析已发布服务；独立 `inference-gateway-publisher` 通过受限 Kubernetes API 调谐每服务 Backend、AIServiceBackend、AIGatewayRoute。Gateway 级 SecurityPolicy 先调用 adapter 校验 AK，再调用 inference-service 获得可信服务 ID，adapter 注入可信租户/服务头并触发 `recomputeRoute`，Envoy 才选择最终后端。

**Tech Stack:** Go 1.25、PostgreSQL 17、gRPC/Protocol Buffers、Envoy Gateway v1.8、Envoy AI Gateway v1.0、Gateway API、Python 3.12、Kubernetes server-side apply、Redis。

## Global Constraints

- 只在本地 `main` 分支工作；禁止创建分支、worktree、rebase 或 force push。
- 每次写操作前运行 `git branch --show-current`，结果必须为 `main`。
- 当前工作树包含其他在途改动；只触碰本计划列出的文件，禁止清理、覆盖或格式化无关文件。
- 本计划不新增或删除 Services v1 端点、字段、状态码或错误码；公开契约只保留已确认的 description 修订。
- description 契约变更先独立通过 OpenAPI 门禁和上游契约评审；评审完成前不开始生产代码。
- 客户端只提交 `Authorization: Bearer ani_*` 和标准 OpenAI 请求体 `model`；不接收客户端 tenant、service UUID 或可信内部头。
- `served_model_name` 在活动服务内按 `(tenant_id, served_model_name)` 唯一，停止不释放，删除完成后释放。
- 数据面不接受登录 JWT，首版不要求 inference scope；缺失/无效 AK 为 401，策略拒绝为 403，租户内未发布/路径不匹配为 404，限流为 429，强依赖失败为 503。
- `/v1/models` 首版固定返回 404，不返回全局或跨租户模型列表。
- 当前安装的 `AIGatewayRoute v1beta1` 只用 Header match；OpenAI path 由 AI Gateway 端点识别和 `CheckInferenceAccess` 在重算路由前强制校验。
- 每服务只发布 Backend、AIServiceBackend、AIGatewayRoute；SecurityPolicy 和全局 BackendTrafficPolicy 是 Gateway 级共享资源。
- Publisher 的 `INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL` 使用 Deployment 直接 `env.value`；首版不增加 ConfigMap。
- Publisher 只管理 `ani-aigw` namespace 内带自身 owner labels 的三个 CR，不读写 Secret，不管理 workload，不代理推理请求。
- `endpoint_url` 始终为 `null`；`invocation_url` 只在当前发布 generation 已确认后返回公网 OpenAI path。
- C41 P0 的策略并发 lease 对普通 JSON 和 SSE 一律依赖 TTL 保守释放：标准 ext_authz 没有上游响应结束 hook，adapter 不得立即、定时或猜测性 release；精确释放延期至 P1 的 Envoy access-log、ext_proc 或等价结束回调。
- 日志、事件和 evidence 不得包含 AK 原文、Authorization、prompt、completion、embedding 输入、向量、数据库连接串或 Secret data。
- 每个实现任务严格走 RED → GREEN → focused tests → module tests → `git diff --check`。
- 每个 commit、push、PR 都是人工确认关卡；计划中的 commit 命令只有在用户明确批准后才能执行。
- C41 真实集群执行必须再次获得明确 live 授权；静态 dry-run 不能外推为 runtime ready。

---

## File Map

### Public contract and design

- Modify: `repo/api/openapi/services/v1.yaml` — 仅冻结 `served_model_name` 与 `invocation_url` 的既有公开语义描述。
- Modify: `docs/superpowers/specs/2026-08-31-envoy-ai-gateway-tenant-aware-dynamic-publication-design.md` — C41 权威设计和安装 CRD 的 Header-only 事实。

### Persistence and inference-service domain

- Create: `repo/deploy/migrations/20260831_001_inference_gateway_publication.sql` — 发布期望、观察 generation、phase、lease 和错误字段。
- Create: `repo/scripts/validate_inference_gateway_publication_migration.py` — migration 静态语义门禁。
- Create: `repo/scripts/validate_inference_gateway_publication_migration_test.py` — migration 变异测试。
- Modify: `repo/services/inference-service/internal/domain/resource.go` — 发布领域类型和 `Service.Publication`。
- Modify: `repo/services/inference-service/internal/repository/store.go` — 发布领取/完成/失败、撤销门禁和租户模型解析接口。
- Modify: `repo/services/inference-service/internal/repository/postgres.go` — PG publication CAS、resolver 和 lifecycle 原子更新。
- Modify: `repo/services/inference-service/internal/repository/postgres_test.go` — SQL fencing、租户过滤和投影测试。
- Modify: `repo/services/inference-service/internal/repository/postgres_integration_test.go` — 真实 PG RLS、claim、stale generation 和 resolver 集成测试。

### Access resolution and internal RPC

- Modify: `repo/services/inference-service/internal/domain/access_policy.go` — 策略 specificity 排序。
- Modify: `repo/services/inference-service/internal/domain/access_policy_test.go` — 四层 specificity 和 priority 测试。
- Modify: `repo/services/inference-service/internal/service/access_policy.go` — tenant/model/path 解析、QPS/RPM/concurrency 和 resolved service ID。
- Modify: `repo/services/inference-service/internal/service/access_policy_test.go` — 跨租户折叠、路径、发布状态和策略测试。
- Modify: `repo/api/proto/inference/control/v1/inference_control.proto` — 内部 access request/response 和 `invocation_url` 投影。
- Regenerate: `repo/pkg/generated/pb/inference/control/v1/inference_control.pb.go` — protobuf 生成物。
- Regenerate: `repo/pkg/generated/pb/inference/control/v1/inference_control_grpc.pb.go` — gRPC 生成物。
- Modify: `repo/services/inference-service/internal/grpcapi/server.go` — 新的模型解析输入和 resolved service ID 输出。
- Modify: `repo/services/inference-service/internal/grpcapi/convert.go` — `invocation_url` 内部投影。
- Modify: `repo/services/inference-service/internal/grpcapi/server_test.go` — access gRPC 与 HTTP 状态测试。
- Modify: `repo/services/inference-service/internal/grpcapi/convert_test.go` — 调用地址存在/为空测试。
- Modify: `repo/services/ani-gateway/internal/router/inference_resources.go` — 把内部 `invocation_url` 映射到既有 REST 字段。
- Modify: `repo/services/ani-gateway/internal/router/inference_resources_test.go` — 不暴露 ClusterIP、只回公网 URL。

### Envoy auth adapter

- Modify: `repo/services/envoy-authz-adapter/internal/config/config.go` — inference gRPC 必填和独立 timeout。
- Modify: `repo/services/envoy-authz-adapter/internal/config/config_test.go` — fail-closed 配置测试。
- Modify: `repo/services/envoy-authz-adapter/internal/policyclient/client.go` — served model 请求和结构化决定。
- Create: `repo/services/envoy-authz-adapter/internal/policyclient/client_test.go` — RPC 字段和错误映射测试。
- Modify: `repo/services/envoy-authz-adapter/internal/extauth/server.go` — 动态解析、可信头覆盖、404 models 和 Retry-After。
- Modify: `repo/services/envoy-authz-adapter/internal/extauth/server_test.go` — 401/403/404/429/503、spoof 和 trusted header 测试。
- Modify: `repo/services/envoy-authz-adapter/main.go` — inference-service 强依赖连接。
- Modify: `repo/services/envoy-authz-adapter/main_test.go` — health 与双依赖装配测试。

### Publisher

- Create: `repo/services/inference-service/internal/gatewaypublish/config.go` — URL、namespace、Gateway、controller 和超时配置。
- Create: `repo/services/inference-service/internal/gatewaypublish/config_test.go` — HTTPS/HTTP gate 和 URL 规范化测试。
- Create: `repo/services/inference-service/internal/gatewaypublish/types.go` — Kube client/object/condition 边界。
- Create: `repo/services/inference-service/internal/gatewaypublish/render.go` — 确定性三对象渲染。
- Create: `repo/services/inference-service/internal/gatewaypublish/render_test.go` — 资源名、头匹配、凭据移除和 Backend FQDN 测试。
- Create: `repo/services/inference-service/internal/gatewaypublish/kubeclient.go` — in-cluster raw REST SSA/GET/DELETE。
- Create: `repo/services/inference-service/internal/gatewaypublish/kubeclient_test.go` — URL、content-type、status 和敏感错误测试。
- Create: `repo/services/inference-service/internal/gatewaypublish/reconciler.go` — publish/unpublish、conditions、generation fencing。
- Create: `repo/services/inference-service/internal/gatewaypublish/reconciler_test.go` — 重复、部分失败、迟到 generation、删除顺序测试。
- Create: `repo/services/inference-service/cmd/inference-gateway-publisher/main.go` — 独立进程、health/readiness 和循环。
- Create: `repo/services/inference-service/cmd/inference-gateway-publisher/main_test.go` — 非法配置不调谐和优雅退出测试。
- Create: `repo/services/inference-service/Dockerfile.publisher` — 非 root 静态 publisher 镜像。

### Lifecycle, manifests, gates and records

- Modify: `repo/services/inference-service/internal/service/control.go` — stop/restart/delete 不在请求路径先操作 runtime。
- Modify: `repo/services/inference-service/internal/service/control_test.go` — 请求路径撤销优先测试。
- Modify: `repo/services/inference-service/internal/reconcile/worker.go` — stop/restart/delete 的 publication withdrawn gate。
- Modify: `repo/services/inference-service/internal/reconcile/worker_test.go` — 撤路由前不碰 runtime 的测试。
- Modify: `repo/services/inference-service/internal/reconcile/flow_test.go` — create/start/restart/stop/delete 端到端状态测试。
- Create: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c41.yaml` — C41 共享 adapter、SecurityPolicy、Publisher、RBAC、NetworkPolicy 和全局限流。
- Create: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c41-live-gate.yaml` — C41 动态真实验收契约。
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_c41_manifest.py` — C41 manifest 静态门禁。
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_c41_manifest_test.py` — manifest 变异测试。
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_c41_live_gate.py` — live gate 契约门禁。
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_c41_live_gate_test.py` — live gate/runner 安全测试。
- Create: `repo/scripts/run_inference_envoy_ai_gateway_c41_live.py` — 真实多租户 chat/embed、AK、限流和生命周期 runner。
- Modify: `repo/Makefile` — C41 migration/manifest/live gate、publisher image targets。
- Create: `repo/development-records/inference-envoy-ai-gateway-c41.md` — Feature batch 实现与证据边界。
- Modify: `repo/development-records/README.md` — C41 索引。
- Modify: `repo/CURRENT-SPRINT.md` — C41 当前状态和门禁。
- Modify: `ANI-06-开发计划.md` — C41 阶段边界。

### Explicitly unchanged

- `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml` — 保留 C40 静态验证资源，不改成动态清单。
- ANI Gateway 的 `/v1/chat/completions` 与 `/v1/embeddings` handler — 不新增、不实现数据面代理。
- auth-service 的 AK 存储和 `ValidateToken` 协议 — 继续作为唯一凭据认证来源。
- Services OpenAPI 的端点、字段集合和错误结构 — 不增加新公开契约。

---

### Task 0: Freeze the Description-Only Contract Before Implementation

**Files:**
- Modify: `repo/api/openapi/services/v1.yaml:InferenceServiceCreate.served_model_name`
- Modify: `repo/api/openapi/services/v1.yaml:InferenceService.invocation_url`
- Modify: `docs/superpowers/specs/2026-08-31-envoy-ai-gateway-tenant-aware-dynamic-publication-design.md`

**Interfaces:**
- Consumes: 已确认 C41 设计决策。
- Produces: 不新增字段的 Services v1 语义基线；后续任务只改内部实现。

- [ ] **Step 1: Verify the branch and isolate the contract diff**

Run:

```bash
git branch --show-current
git diff -- repo/api/openapi/services/v1.yaml docs/superpowers/specs/2026-08-31-envoy-ai-gateway-tenant-aware-dynamic-publication-design.md
```

Expected: branch is `main`; OpenAPI diff only clarifies that `served_model_name` is the OpenAI request-body model and `invocation_url` appears only after workload plus Gateway publication.

- [ ] **Step 2: Run the contract gates**

Run:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec
PATH=/tmp/ani-pybin:$PATH make validate-services-contract
git diff --check
```

Expected: all commands exit 0; no SDK regeneration is required because schema shape is unchanged.

- [ ] **Step 3: Hold the API-first approval checkpoint**

Present the exact two description changes and design correction for review. Do not edit Go, Proto, SQL, manifests, Make targets, or live runners until the contract review is explicitly approved under the repository API-first rule.

- [ ] **Step 4: Commit only after explicit authorization**

Run only after approval:

```bash
git fetch upstream main
git branch --show-current
git add repo/api/openapi/services/v1.yaml docs/superpowers/specs/2026-08-31-envoy-ai-gateway-tenant-aware-dynamic-publication-design.md
git diff --cached --check
git diff --cached --name-only
git commit -m "docs(api): clarify inference gateway invocation semantics"
```

Expected: staged names are exactly the two files above; commit is created on `main`. Push/PR follows the ANI contract gate and waits for personal Actions to pass before the upstream PR.

---

### Task 1: Persist Publication State with Generation Fencing

**Files:**
- Create: `repo/deploy/migrations/20260831_001_inference_gateway_publication.sql`
- Create: `repo/scripts/validate_inference_gateway_publication_migration.py`
- Create: `repo/scripts/validate_inference_gateway_publication_migration_test.py`
- Modify: `repo/services/inference-service/internal/domain/resource.go`
- Modify: `repo/services/inference-service/internal/repository/store.go`
- Modify: `repo/services/inference-service/internal/repository/postgres.go`
- Modify: `repo/services/inference-service/internal/repository/postgres_test.go`
- Modify: `repo/services/inference-service/internal/repository/postgres_integration_test.go`
- Modify: `repo/Makefile`

**Interfaces:**
- Consumes: existing `inference_services`, `generation`, `runtime_endpoint`, `invocation_url`, platform PG pool and tenant RLS transaction helpers.
- Produces: `domain.Publication`, `repository.PublicationTarget`, `repository.PublicationResult`, `ClaimPublication`, `CompletePublication`, `FailPublication`, `PublicationWithdrawn`, and `ResolvePublishedService`.

- [ ] **Step 1: Write migration mutation tests first**

Create tests that delete or alter one required clause at a time:

```python
REQUIRED = (
    "publication_desired",
    "publication_generation",
    "publication_observed_generation",
    "publication_phase",
    "publication_lease_owner",
    "publication_lease_until",
    "publication_lease_token",
    "publication_last_error",
    "idx_inference_services_publication_claim",
)

def test_rejects_missing_generation_fence(tmp_path):
    mutated = SQL.replace("publication_generation BIGINT NOT NULL DEFAULT 0", "")
    assert "publication_generation" in validate_text(mutated)

def test_rejects_non_null_initial_invocation_url(tmp_path):
    mutated = SQL.replace("invocation_url = NULL", "invocation_url = invocation_url")
    assert "invocation_url" in validate_text(mutated)
```

Run:

```bash
cd repo
python3 scripts/validate_inference_gateway_publication_migration_test.py
```

Expected RED: import or file-not-found failure because the validator and migration do not exist.

- [ ] **Step 2: Add the additive migration and validator**

Use these exact persisted states:

```sql
BEGIN;

ALTER TABLE inference_services
    ADD COLUMN IF NOT EXISTS publication_desired TEXT NOT NULL DEFAULT 'unpublished'
        CHECK (publication_desired IN ('published', 'unpublished')),
    ADD COLUMN IF NOT EXISTS publication_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS publication_observed_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS publication_phase TEXT NOT NULL DEFAULT 'unpublished'
        CHECK (publication_phase IN ('pending', 'publishing', 'published', 'unpublishing', 'unpublished', 'failed')),
    ADD COLUMN IF NOT EXISTS publication_lease_owner TEXT,
    ADD COLUMN IF NOT EXISTS publication_lease_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS publication_lease_token UUID,
    ADD COLUMN IF NOT EXISTS publication_last_error TEXT,
    ADD COLUMN IF NOT EXISTS publication_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE inference_services
SET publication_desired = 'unpublished',
    publication_generation = 0,
    publication_observed_generation = 0,
    publication_phase = 'unpublished',
    invocation_url = NULL,
    publication_updated_at = NOW()
WHERE publication_generation = 0;

CREATE INDEX IF NOT EXISTS idx_inference_services_publication_claim
    ON inference_services(publication_updated_at, id)
    WHERE deleted_at IS NULL
      AND (publication_generation <> publication_observed_generation
           OR publication_phase IN ('pending', 'publishing', 'unpublishing', 'failed'));

COMMIT;
```

The Python validator must normalize SQL, require every column/check/index above, require `invocation_url = NULL` in legacy initialization, and reject destructive `DROP COLUMN`, table recreation, Secret content, or credential literals.

- [ ] **Step 3: Add domain publication types and repository interfaces**

Add the exact types:

```go
type PublicationDesired string

const (
	PublicationPublished   PublicationDesired = "published"
	PublicationUnpublished PublicationDesired = "unpublished"
)

type PublicationPhase string

const (
	PublicationPending      PublicationPhase = "pending"
	PublicationPublishing   PublicationPhase = "publishing"
	PublicationPublishedOK  PublicationPhase = "published"
	PublicationUnpublishing PublicationPhase = "unpublishing"
	PublicationUnpublishedOK PublicationPhase = "unpublished"
	PublicationFailed       PublicationPhase = "failed"
)

type Publication struct {
	Desired            PublicationDesired
	Generation         int64
	ObservedGeneration int64
	Phase              PublicationPhase
	LastError          string
	UpdatedAt          time.Time
}
```

Embed `Publication Publication` in `domain.Service`, then define:

```go
type PublicationTarget struct {
	TenantID        uuid.UUID
	ServiceID       uuid.UUID
	Generation      int64
	Desired         domain.PublicationDesired
	ServedModelName string
	Task            domain.InferenceTask
	RuntimeEndpoint string
	LeaseToken      uuid.UUID
}

type PublicationResult struct {
	TenantID     uuid.UUID
	ServiceID    uuid.UUID
	Generation   int64
	LeaseToken   uuid.UUID
	Phase        domain.PublicationPhase
	InvocationURL string
	Now          time.Time
}

type PublicationStore interface {
	ClaimPublication(context.Context, string, time.Time, time.Duration) (PublicationTarget, bool, error)
	CompletePublication(context.Context, PublicationResult) error
	FailPublication(context.Context, PublicationTarget, string, time.Time) error
}

// Add this method to the existing worker Store interface.
PublicationWithdrawn(context.Context, uuid.UUID, uuid.UUID, int64) (bool, error)

// Add this method to the existing AccessPolicyStore interface.
ResolvePublishedService(context.Context, uuid.UUID, string) (domain.Service, error)
```

- [ ] **Step 4: Write focused SQL fencing tests**

Add assertions that claim uses `FOR UPDATE SKIP LOCKED`, expired leases only, and a random lease token; completion must match tenant, service, publication generation, lease token, and unexpired lease:

```go
func TestCompletePublicationSQLUsesGenerationAndLeaseFence(t *testing.T) {
	sql := compactSQL(completePublicationSQL)
	for _, required := range []string{
		"tenant_id = $1", "id = $2", "publication_generation = $3",
		"publication_lease_token = $4", "publication_lease_until > $5",
	} {
		if !strings.Contains(sql, required) { t.Fatalf("missing %q: %s", required, sql) }
	}
}
```

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/repository -run 'Publication|ResolvePublished' -count=1
```

Expected RED: undefined publication SQL/methods/types.

- [ ] **Step 5: Implement PostgreSQL claim, completion, failure, withdraw and resolver**

The resolver query must be tenant-scoped and non-leaking:

```sql
SELECT service.id, service.tenant_id, service.name, service.model_version_id,
       service.served_model_name, service.model_display_snapshot,
       service.status, COALESCE(service.status_reason, ''), COALESCE(service.status_message, ''),
       service.desired_state, service.generation, service.observed_generation,
       service.desired_spec, service.applied_spec,
       COALESCE(service.runtime_ref, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE(service.runtime_endpoint, ''), COALESCE(service.invocation_url, ''),
       service.ready_replicas,
       COALESCE(service.current_operation_id, '00000000-0000-0000-0000-000000000000'::uuid),
       '' AS active_type, '' AS active_state,
       service.created_at, service.updated_at, service.deleted_at, service.legacy_quarantined,
       service.publication_desired, service.publication_generation,
       service.publication_observed_generation, service.publication_phase,
       COALESCE(service.publication_last_error, ''), service.publication_updated_at
FROM inference_services AS service
WHERE service.tenant_id = $1
  AND service.served_model_name = $2
  AND service.deleted_at IS NULL
  AND service.status = 'running'
  AND service.desired_state = 'running'
  AND service.publication_desired = 'published'
  AND service.publication_phase = 'published'
  AND service.publication_generation = service.publication_observed_generation
  AND service.invocation_url IS NOT NULL
LIMIT 1
```

`ClaimPublication` uses the platform pool, `FOR UPDATE SKIP LOCKED`, a bounded lease, and `gen_random_uuid()`. `CompletePublication` sets observed generation and phase; only publish success sets `invocation_url`, while unpublish success forces it to `NULL`. `FailPublication` writes a redacted bounded error, clears the lease, preserves desired state, and keeps publish `invocation_url` null.

- [ ] **Step 6: Run static and PG integration tests**

Run:

```bash
cd repo
python3 scripts/validate_inference_gateway_publication_migration_test.py
python3 scripts/validate_inference_gateway_publication_migration.py
cd services/inference-service
GOWORK=off go test ./internal/domain ./internal/repository -count=1
GOWORK=off go test -tags=integration ./internal/repository -run 'Publication|ResolvePublished' -count=1
```

Expected GREEN: mutation tests pass; focused Go tests pass; integration tests either pass against the configured PostgreSQL gate or report the repository’s explicit missing-test-database skip without claiming live success.

- [ ] **Step 7: Add the Make target and hold the commit checkpoint**

Add:

```make
validate-inference-gateway-publication-migration:
	$(PYTHON) scripts/validate_inference_gateway_publication_migration_test.py
	$(PYTHON) scripts/validate_inference_gateway_publication_migration.py
```

Run:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make validate-inference-gateway-publication-migration
git diff --check
```

After explicit user approval only:

```bash
git add repo/deploy/migrations/20260831_001_inference_gateway_publication.sql repo/scripts/validate_inference_gateway_publication_migration.py repo/scripts/validate_inference_gateway_publication_migration_test.py repo/services/inference-service/internal/domain/resource.go repo/services/inference-service/internal/repository/store.go repo/services/inference-service/internal/repository/postgres.go repo/services/inference-service/internal/repository/postgres_test.go repo/services/inference-service/internal/repository/postgres_integration_test.go repo/Makefile
git commit -m "feat(inference): persist gateway publication state"
```

---

### Task 2: Resolve Published Tenant Models and Enforce Policy Specificity

**Files:**
- Modify: `repo/services/inference-service/internal/repository/store.go`
- Modify: `repo/services/inference-service/internal/repository/postgres.go`
- Modify: `repo/services/inference-service/internal/domain/access_policy.go`
- Modify: `repo/services/inference-service/internal/domain/access_policy_test.go`
- Modify: `repo/services/inference-service/internal/service/access_policy.go`
- Modify: `repo/services/inference-service/internal/service/access_policy_test.go`

**Interfaces:**
- Consumes: `ResolvePublishedService(ctx, tenantID, servedModelName)` from Task 1 and existing `ports.RateLimiter`.
- Produces: `AccessCheckInput.ServedModelName`, `AccessDecision.InferenceServiceID`, exact task/path validation, specificity-first policy selection, QPS/RPM/concurrency enforcement.

- [ ] **Step 1: Write resolver and non-leakage tests**

Cover same model name in tenant A and B, missing model, unpublished service, stopped service, generate-to-embeddings mismatch, and embed-to-chat mismatch:

```go
func TestCheckAccessResolvesModelOnlyInsideAuthenticatedTenant(t *testing.T) {
	store := newAccessStore()
	store.addPublished(tenantA, serviceA, "ani-qwen3", domain.InferenceTaskGenerate)
	store.addPublished(tenantB, serviceB, "ani-qwen3", domain.InferenceTaskGenerate)
	decision, err := NewAccessPolicyService(store, nil, fixedNow).CheckAccess(context.Background(), AccessCheckInput{
		TenantID: tenantB, APIKeyID: keyB, ServedModelName: "ani-qwen3", OpenAIPath: "/v1/chat/completions",
	})
	if err != nil || decision.HTTPStatus != 200 || decision.InferenceServiceID != serviceB {
		t.Fatalf("decision = %+v, err = %v", decision, err)
	}
}
```

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/service -run 'Resolve|Published|TaskPath' -count=1
```

Expected RED: `ServedModelName` and `InferenceServiceID` are undefined; current code still requires caller-supplied service ID.

- [ ] **Step 2: Write specificity and QPS tests**

The selected order must be service+AK, AK, service, tenant default; priority is compared only inside the same specificity:

```go
func TestSelectPolicyUsesSpecificityBeforePriority(t *testing.T) {
	selected, ok := SelectPolicy([]AccessPolicy{
		policy(ScopeTenantDefault, 1),
		policy(ScopeInferenceServiceAPIKey, 9000),
	}, serviceID, keyID)
	if !ok || selected.Scope.Type != ScopeInferenceServiceAPIKey {
		t.Fatalf("selected = %+v", selected)
	}
}
```

Add a QPS test expecting one-second fixed-window enforcement and reason `POLICY_QPS_LIMIT`.

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/domain ./internal/service -run 'Specificity|Priority|QPS' -count=1
```

Expected RED: current `MatchPolicies` sorts globally by priority and `CheckAccess` has no QPS call.

Implement the selector with this exact ranking and deterministic tie break:

```go
func policySpecificity(scope AccessPolicyScopeType) int {
	switch scope {
	case ScopeInferenceServiceAPIKey:
		return 4
	case ScopeAPIKey:
		return 3
	case ScopeInferenceService:
		return 2
	case ScopeTenantDefault:
		return 1
	default:
		return 0
	}
}

func SelectPolicy(policies []AccessPolicy, serviceID, keyID uuid.UUID) (AccessPolicy, bool) {
	matched := MatchPolicies(policies, serviceID, keyID)
	sort.SliceStable(matched, func(i, j int) bool {
		left, right := policySpecificity(matched[i].Scope.Type), policySpecificity(matched[j].Scope.Type)
		if left != right { return left > right }
		if matched[i].Priority != matched[j].Priority { return matched[i].Priority < matched[j].Priority }
		return matched[i].ID.String() < matched[j].ID.String()
	})
	if len(matched) == 0 { return AccessPolicy{}, false }
	return matched[0], true
}
```

- [ ] **Step 3: Implement exact path mapping and input/output types**

Use these helpers and types:

```go
type AccessCheckInput struct {
	TenantID        uuid.UUID
	UserID          uuid.UUID
	APIKeyID        uuid.UUID
	KeyPrefix       string
	ServedModelName string
	OpenAIPath      string
	RequestID       string
	Stream          bool
}

type AccessDecision struct {
	Decision          string
	HTTPStatus        int
	ReasonCode        string
	InferenceServiceID uuid.UUID
	PolicyID          uuid.UUID
	LeaseID           string
	RetryAfter        time.Duration
}

func OpenAIPathForTask(task domain.InferenceTask) (string, bool) {
	switch domain.NormalizeInferenceTask(task) {
	case domain.InferenceTaskGenerate:
		return "/v1/chat/completions", true
	case domain.InferenceTaskEmbed:
		return "/v1/embeddings", true
	default:
		return "", false
	}
}
```

Normalize the request path by removing only the query component. Any path other than the two exact supported paths returns a 404 decision before policy evaluation.

- [ ] **Step 4: Implement resolver-first policy selection**

`CheckAccess` must:

```go
service, err := s.store.ResolvePublishedService(ctx, in.TenantID, strings.TrimSpace(in.ServedModelName))
if errors.Is(err, repository.ErrNotFound) {
	return AccessDecision{Decision: "not_found", HTTPStatus: 404, ReasonCode: "NOT_FOUND"}, nil
}
if err != nil {
	return AccessDecision{Decision: "policy_unavailable", HTTPStatus: 503, ReasonCode: "POLICY_BACKEND_UNAVAILABLE"}, err
}
expected, ok := OpenAIPathForTask(service.DesiredSpec.ExecutionProfile.Task)
if !ok || normalizeOpenAIPath(in.OpenAIPath) != expected {
	return AccessDecision{Decision: "not_found", HTTPStatus: 404, ReasonCode: "NOT_FOUND"}, nil
}
decision := AccessDecision{Decision: "allow", HTTPStatus: 200, ReasonCode: "NO_CUSTOM_POLICY", InferenceServiceID: service.ID}
```

Load all current-tenant policies using `ListAccessPolicies`, call `SelectPolicy`, then enforce exactly the one selected policy. Execute QPS with a one-second fixed window before RPM, then concurrency. All limiter errors return 503 and never allow.

Build limiter keys only from resolved identities:

```go
func rateKey(in AccessCheckInput, serviceID, policyID uuid.UUID, dimension string) string {
	return in.TenantID.String() + "/" + serviceID.String() + "/" + in.APIKeyID.String() + "/" + policyID.String() + "/" + dimension
}
```

No limiter key may use a caller-provided service ID, raw AK or served model text.

- [ ] **Step 5: Keep event compatibility without a second model concept**

Replace internal `ExternalModel` use with `ServedModelName`, but populate the existing event field for public/storage compatibility:

```go
event := domain.AccessPolicyEvent{
	TenantID: in.TenantID,
	InferenceServiceID: &decision.InferenceServiceID,
	APIKeyID: &in.APIKeyID,
	OpenAIPath: normalizeOpenAIPath(in.OpenAIPath),
	ExternalModel: in.ServedModelName,
	Decision: decision.Decision,
	ReasonCode: decision.ReasonCode,
	HTTPStatus: decision.HTTPStatus,
}
```

Do not add a new database column or public event field named `served_model_name` in this batch.

- [ ] **Step 6: Run focused and module tests**

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/domain ./internal/service ./internal/repository -count=1
GOWORK=off go test -race ./internal/domain ./internal/service -count=1
GOWORK=off go vet ./internal/domain ./internal/service ./internal/repository
cd ../..
git diff --check
```

Expected GREEN: tenant/model resolution, 404 non-leakage, specificity, QPS, RPM and concurrency tests pass.

- [ ] **Step 7: Hold the commit checkpoint**

After explicit approval only:

```bash
git add repo/services/inference-service/internal/repository/store.go repo/services/inference-service/internal/repository/postgres.go repo/services/inference-service/internal/domain/access_policy.go repo/services/inference-service/internal/domain/access_policy_test.go repo/services/inference-service/internal/service/access_policy.go repo/services/inference-service/internal/service/access_policy_test.go
git commit -m "feat(inference): resolve tenant models before policy checks"
```

---

### Task 3: Change the Internal gRPC Contract and Project Invocation URLs

**Files:**
- Modify: `repo/api/proto/inference/control/v1/inference_control.proto`
- Regenerate: `repo/pkg/generated/pb/inference/control/v1/inference_control.pb.go`
- Regenerate: `repo/pkg/generated/pb/inference/control/v1/inference_control_grpc.pb.go`
- Modify: `repo/services/inference-service/internal/grpcapi/server.go`
- Modify: `repo/services/inference-service/internal/grpcapi/server_test.go`
- Modify: `repo/services/inference-service/internal/grpcapi/convert.go`
- Modify: `repo/services/inference-service/internal/grpcapi/convert_test.go`
- Modify: `repo/services/inference-service/internal/service/control.go`
- Modify: `repo/services/inference-service/internal/service/control_test.go`
- Modify: `repo/services/ani-gateway/internal/router/inference_resources.go`
- Modify: `repo/services/ani-gateway/internal/router/inference_resources_test.go`

**Interfaces:**
- Consumes: `AccessCheckInput.ServedModelName` and `AccessDecision.InferenceServiceID` from Task 2.
- Produces: internal `CheckInferenceAccess` that no longer trusts a caller service ID; internal `InferenceService.invocation_url`; existing REST `invocation_url` projection.

- [ ] **Step 1: Write failing gRPC tests**

Assert the handler accepts tenant/key/model/path without service ID and returns the resolved ID:

```go
resp, err := server.CheckInferenceAccess(ctx, &inferencecontrolv1.CheckInferenceAccessRequest{
	TenantId: tenantID.String(), ApiKeyId: keyID.String(), ServedModelName: "ani-qwen3",
	OpenaiPath: "/v1/chat/completions", RequestId: "req-c41",
})
if err != nil || resp.GetInferenceServiceId() != serviceID.String() {
	t.Fatalf("resp = %+v, err = %v", resp, err)
}
```

Add conversion tests where a published view returns `https://ai.example.com/v1/chat/completions`, while an unpublished view returns an empty proto field and REST `null`.

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/grpcapi ./internal/service -run 'CheckInferenceAccess|InvocationURL' -count=1
```

Expected RED: new proto getters and response field are undefined.

- [ ] **Step 2: Update the internal protobuf exactly**

Use wire number 6 for the renamed internal model input, reserve the obsolete caller service field, add resolved service ID to the response, and add the existing public URL to the internal service projection:

```proto
message InferenceService {
  // fields 1..23 unchanged
  string invocation_url = 24;
}

message CheckInferenceAccessRequest {
  string tenant_id = 1;
  string user_id = 2;
  string api_key_id = 3;
  string key_prefix = 4;
  reserved 5;
  reserved "inference_service_id";
  string served_model_name = 6;
  string openai_path = 7;
  string request_id = 8;
  bool stream = 9;
}

message CheckInferenceAccessResponse {
  string decision = 1;
  int32 http_status = 2;
  string reason_code = 3;
  string policy_id = 4;
  string lease_id = 5;
  int32 retry_after_seconds = 6;
  string inference_service_id = 7;
}
```

- [ ] **Step 3: Regenerate protobuf files without retaining unrelated formatting drift**

Run:

```bash
cd repo
make gen-proto
git diff -- pkg/generated/pb/inference/control/v1/inference_control.pb.go pkg/generated/pb/inference/control/v1/inference_control_grpc.pb.go
git diff --name-only -- pkg/generated/pb
```

Expected: intended inference-control generated files reflect the proto. If the generator formats unrelated generated files, restore only those unrelated paths from `HEAD` after checking they contain no user changes; never use a broad restore.

- [ ] **Step 4: Implement the gRPC mapping and URL projection**

The server input and response must be:

```go
decision, err := s.policies.CheckAccess(ctx, service.AccessCheckInput{
	TenantID: tenantID,
	UserID: userID,
	APIKeyID: keyID,
	KeyPrefix: req.GetKeyPrefix(),
	ServedModelName: strings.TrimSpace(req.GetServedModelName()),
	OpenAIPath: req.GetOpenaiPath(),
	RequestID: req.GetRequestId(),
	Stream: req.GetStream(),
})

return &inferencecontrolv1.CheckInferenceAccessResponse{
	Decision: decision.Decision,
	HttpStatus: int32(decision.HTTPStatus),
	ReasonCode: decision.ReasonCode,
	PolicyId: uuidString(decision.PolicyID),
	LeaseId: decision.LeaseID,
	RetryAfterSeconds: int32(decision.RetryAfter.Seconds()),
	InferenceServiceId: uuidString(decision.InferenceServiceID),
}, nil
```

`projectService` must set only a non-empty public URL:

```go
var invocationURL *string
if value := strings.TrimSpace(resource.InvocationURL); value != "" {
	invocationURL = &value
}
view.InvocationURL = invocationURL
view.EndpointURL = nil
```

`protoService` copies `InvocationUrl`; `inferenceServiceJSON` uses `emptyToNil(msg.GetInvocationUrl())` and keeps `endpoint_url: nil`.

- [ ] **Step 5: Run proto, inference-service and Gateway tests**

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/grpcapi ./internal/service -count=1
GOWORK=off go vet ./internal/grpcapi ./internal/service
cd ../ani-gateway
GOWORK=off go test ./internal/router -run 'Inference.*Invocation|InferenceServiceJSON' -count=1
GOWORK=off go vet ./internal/router
cd ../..
git diff --check
```

Expected GREEN: internal endpoint never appears; invocation URL is null until published and public afterward.

- [ ] **Step 6: Hold the generated-files commit checkpoint**

After explicit approval only, stage only the proto, its two generated files, and the listed consumers:

```bash
git add repo/api/proto/inference/control/v1/inference_control.proto repo/pkg/generated/pb/inference/control/v1/inference_control.pb.go repo/pkg/generated/pb/inference/control/v1/inference_control_grpc.pb.go repo/services/inference-service/internal/grpcapi/server.go repo/services/inference-service/internal/grpcapi/server_test.go repo/services/inference-service/internal/grpcapi/convert.go repo/services/inference-service/internal/grpcapi/convert_test.go repo/services/inference-service/internal/service/control.go repo/services/inference-service/internal/service/control_test.go repo/services/ani-gateway/internal/router/inference_resources.go repo/services/ani-gateway/internal/router/inference_resources_test.go
git diff --cached --name-only
git commit -m "feat(inference): return resolved service and invocation url"
```

---

### Task 4: Make ext_authz Resolve Models and Inject Trusted Route Headers

**Files:**
- Modify: `repo/services/envoy-authz-adapter/internal/config/config.go`
- Modify: `repo/services/envoy-authz-adapter/internal/config/config_test.go`
- Modify: `repo/services/envoy-authz-adapter/internal/policyclient/client.go`
- Create: `repo/services/envoy-authz-adapter/internal/policyclient/client_test.go`
- Modify: `repo/services/envoy-authz-adapter/internal/extauth/server.go`
- Modify: `repo/services/envoy-authz-adapter/internal/extauth/server_test.go`
- Modify: `repo/services/envoy-authz-adapter/main.go`
- Modify: `repo/services/envoy-authz-adapter/main_test.go`

**Interfaces:**
- Consumes: Task 3 internal `CheckInferenceAccess` request and response.
- Produces: standard Envoy ext_authz response that overwrites `x-ani-tenant-id` and `x-ani-inference-service-id`, removes credentials, and preserves exact 401/403/404/429/503 semantics.

- [ ] **Step 1: Write the adapter RED matrix**

Create table tests for:

```go
tests := []struct {
	name string
	path string
	headers map[string]string
	validateErr error
	decision AccessDecision
	wantStatus int
}{
	{"missing AK", "/v1/chat/completions", map[string]string{"x-ai-eg-model": "ani-qwen3"}, nil, AccessDecision{}, 401},
	{"login JWT", "/v1/chat/completions", map[string]string{"authorization": "Bearer ey.test", "x-ai-eg-model": "ani-qwen3"}, nil, AccessDecision{}, 401},
	{"unknown model", "/v1/chat/completions", validHeaders(), nil, AccessDecision{HTTPStatus: 404}, 404},
	{"policy deny", "/v1/chat/completions", validHeaders(), nil, AccessDecision{HTTPStatus: 403}, 403},
	{"rate limit", "/v1/chat/completions", validHeaders(), nil, AccessDecision{HTTPStatus: 429, RetryAfterSeconds: 7}, 429},
	{"dependency down", "/v1/chat/completions", validHeaders(), nil, AccessDecision{HTTPStatus: 503}, 503},
	{"models blocked", "/v1/models", validHeaders(), nil, AccessDecision{}, 404},
}
```

Add an allow test where the client supplies fake `x-ani-tenant-id` and `x-ani-inference-service-id`; the OK response must overwrite both with authenticated values, remove Authorization and identity headers before upstream, and never send the API key to the policy checker.

Run:

```bash
cd repo/services/envoy-authz-adapter
GOWORK=off go test ./internal/extauth ./internal/policyclient ./internal/config -count=1
```

Expected RED: current adapter requires static contextExtensions, returns only an integer checker status, and injects no headers.

- [ ] **Step 2: Define the structured checker result and RPC mapping**

Use one shared adapter result type:

```go
type AccessDecision struct {
	HTTPStatus          int
	InferenceServiceID string
	LeaseID            string
	RetryAfterSeconds  int
}

type AccessChecker interface {
	CheckInferenceAccess(context.Context, string, string, string, string, string, string, string, bool) (AccessDecision, error)
}
```

Arguments are tenant ID, user ID, API key ID, non-secret key prefix, served model name, OpenAI path, request ID, stream. The adapter derives at most the first 12 characters as `key_prefix`; the policy client sends no raw token and maps the gRPC response into `AccessDecision`. Any RPC error returns an error and is mapped by the server to 503.

`LeaseID` is intentionally not sent to Envoy or vLLM and this task adds no release call: standard ext_authz lacks a response-end hook. For both non-streaming and SSE requests C41 P0 relies on the policy lease TTL; only a future P1 access-log, ext_proc, or equivalent completion channel may perform an exact release.

- [ ] **Step 3: Make both dependencies mandatory in configuration**

Use:

```go
type Config struct {
	GRPCPort                 int
	AuthServiceGRPCAddr      string
	InferenceServiceGRPCAddr string
	AuthTimeout              time.Duration
	InferenceTimeout         time.Duration
}
```

`AUTH_SERVICE_GRPC_ADDR` and `INFERENCE_SERVICE_GRPC_ADDR` are required. `AUTH_TIMEOUT` and `INFERENCE_TIMEOUT` default to `2s` and must be positive. `main.go` always creates both clients; failure to configure either prevents serving.

- [ ] **Step 4: Implement model/path extraction and trusted header overwrite**

The adapter must not parse or buffer the request body. It reads the AI Gateway-populated normalized header and Envoy path:

```go
headers := normalizeHeaders(httpRequest.GetHeaders())
model := strings.TrimSpace(headers["x-ai-eg-model"])
path := normalizeOpenAIPath(httpRequest.GetPath())
if path == "/v1/models" { return denied(http.StatusNotFound, 0), nil }
if model == "" || (path != "/v1/chat/completions" && path != "/v1/embeddings") {
	return denied(http.StatusNotFound, 0), nil
}
```

After auth and access allow, return:

```go
&authv3.OkHttpResponse{
	Headers: []*corev3.HeaderValueOption{
		trustedHeader("x-ani-tenant-id", principal.GetTenantId()),
		trustedHeader("x-ani-inference-service-id", decision.InferenceServiceID),
	},
	HeadersToRemove: []string{
		"authorization", "x-api-key", "x-ani-user-id",
	},
}
```

`trustedHeader` sets `AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD`; do not put the two trusted headers in `HeadersToRemove`, because overwrite replaces spoofed client values. `x-ai-eg-model` remains until route recomputation and is removed by the selected AIGatewayRoute before vLLM. A 429 denied response includes `Retry-After` only when the server returned a positive integer. The adapter never logs headers or token values.

Construct the optional 429 header without exposing the reason body:

```go
func denied(httpStatus, retryAfterSeconds int) *authv3.CheckResponse {
	response := &authv3.DeniedHttpResponse{Status: &typev3.HttpStatus{Code: typev3.StatusCode(httpStatus)}}
	if httpStatus == http.StatusTooManyRequests && retryAfterSeconds > 0 {
		response.Headers = []*corev3.HeaderValueOption{trustedHeader("retry-after", strconv.Itoa(retryAfterSeconds))}
	}
	return &authv3.CheckResponse{
		Status: &status.Status{Code: int32(codes.PermissionDenied)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: response},
	}
}
```

- [ ] **Step 5: Verify auth, adapter and fail-closed behavior**

Run:

```bash
cd repo/services/envoy-authz-adapter
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
cd ../..
git diff --check
```

Expected GREEN: all adapter tests pass, including missing inference address startup failure, header spoof resistance, `/v1/models` 404 and Retry-After.

- [ ] **Step 6: Hold the commit checkpoint**

After explicit approval only:

```bash
git add repo/services/envoy-authz-adapter/internal/config/config.go repo/services/envoy-authz-adapter/internal/config/config_test.go repo/services/envoy-authz-adapter/internal/policyclient/client.go repo/services/envoy-authz-adapter/internal/policyclient/client_test.go repo/services/envoy-authz-adapter/internal/extauth/server.go repo/services/envoy-authz-adapter/internal/extauth/server_test.go repo/services/envoy-authz-adapter/main.go repo/services/envoy-authz-adapter/main_test.go
git commit -m "feat(gateway): resolve inference routes through ext auth"
```

---

### Task 5: Render Per-Service Envoy Resources and Add a Bounded Kubernetes Client

**Files:**
- Create: `repo/services/inference-service/internal/gatewaypublish/types.go`
- Create: `repo/services/inference-service/internal/gatewaypublish/render.go`
- Create: `repo/services/inference-service/internal/gatewaypublish/render_test.go`
- Create: `repo/services/inference-service/internal/gatewaypublish/kubeclient.go`
- Create: `repo/services/inference-service/internal/gatewaypublish/kubeclient_test.go`

**Interfaces:**
- Consumes: `repository.PublicationTarget` from Task 1 and the installed Backend v1alpha1 / AIServiceBackend v1beta1 / AIGatewayRoute v1beta1 schemas.
- Produces: deterministic `Render(target) (Objects, error)` and a raw REST `KubeClient` restricted to apply/get/delete of the three managed kinds plus get Gateway.

- [ ] **Step 1: Write rendering tests before production code**

Use a real-shaped target:

```go
target := repository.PublicationTarget{
	TenantID: tenantID,
	ServiceID: serviceID,
	Generation: 7,
	Desired: domain.PublicationPublished,
	ServedModelName: "ani-qwen3",
	Task: domain.InferenceTaskGenerate,
	RuntimeEndpoint: "http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc.cluster.local:8000",
}
```

Assert:

```go
if got.Name != "ani-inf-"+serviceID.String() { t.Fatalf("name = %q", got.Name) }
if routeHeader(route, "x-ai-eg-model") != "ani-qwen3" { t.Fatal("missing model match") }
if routeHeader(route, "x-ani-tenant-id") != tenantID.String() { t.Fatal("missing tenant match") }
if routeHeader(route, "x-ani-inference-service-id") != serviceID.String() { t.Fatal("missing service match") }
for _, header := range []string{"Authorization", "x-api-key", "x-ai-eg-model", "x-ani-tenant-id", "x-ani-inference-service-id", "x-ani-user-id"} {
	if !routeRemoves(route, header) { t.Fatalf("route does not remove %q", header) }
}
```

Add rejection cases for IP endpoints, missing ports, non-HTTP scheme, userinfo, query, fragment, non-`.svc` host, empty model, unknown task and zero generation.

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/gatewaypublish -run 'Render|Endpoint|Name' -count=1
```

Expected RED: package or functions do not exist.

- [ ] **Step 2: Define bounded object and client types**

Use focused types instead of importing a Kubernetes SDK:

```go
type Kind string

const (
	KindBackend          Kind = "Backend"
	KindAIServiceBackend Kind = "AIServiceBackend"
	KindAIGatewayRoute   Kind = "AIGatewayRoute"
	KindGateway          Kind = "Gateway"
)

type Object struct {
	Kind       Kind
	Namespace  string
	Name       string
	Generation int64
	Body       map[string]any
	Status     map[string]any
}

type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	ObservedGeneration int64  `json:"observedGeneration"`
}

type RouteParentStatus struct {
	ParentRef      map[string]any `json:"parentRef"`
	ControllerName string         `json:"controllerName"`
	Conditions     []Condition    `json:"conditions"`
}

type Objects struct {
	Backend          Object
	AIServiceBackend Object
	AIGatewayRoute   Object
}

type KubeAPI interface {
	Apply(context.Context, Object) (Object, error)
	Get(context.Context, Kind, string, string) (Object, error)
	Delete(context.Context, Kind, string, string) error
}
```

Define `ErrNotFound` and `ErrStaleCondition`; never expose response bodies in returned errors.

- [ ] **Step 3: Implement deterministic rendering**

Resource name and labels are exact:

```go
func ResourceName(serviceID uuid.UUID) string { return "ani-inf-" + serviceID.String() }

func ownerLabels(target repository.PublicationTarget) map[string]any {
	return map[string]any{
		"app.kubernetes.io/managed-by": "ani-inference-gateway-publisher",
		"ani.kubercloud.io/tenant-id": target.TenantID.String(),
		"ani.kubercloud.io/inference-service-id": target.ServiceID.String(),
		"ani.kubercloud.io/publication-generation": strconv.FormatInt(target.Generation, 10),
	}
}
```

Render these API versions and references:

```yaml
Backend: gateway.envoyproxy.io/v1alpha1, spec.type=Endpoints
AIServiceBackend: aigateway.envoyproxy.io/v1beta1, schema.name=OpenAI, schema.version=v1
AIGatewayRoute: aigateway.envoyproxy.io/v1beta1, parentRef Gateway/ani-aigw
```

The route has exactly one match containing all three Exact headers and one backendRef to the deterministic AIServiceBackend name. It contains no path match because the installed CRD does not support one.

- [ ] **Step 4: Write raw client request tests**

With `httptest.Server`, assert exact resource URLs:

```text
/apis/gateway.envoyproxy.io/v1alpha1/namespaces/ani-aigw/backends/{name}
/apis/aigateway.envoyproxy.io/v1beta1/namespaces/ani-aigw/aiservicebackends/{name}
/apis/aigateway.envoyproxy.io/v1beta1/namespaces/ani-aigw/aigatewayroutes/{name}
/apis/gateway.networking.k8s.io/v1/namespaces/ani-aigw/gateways/ani-aigw
```

Assert apply uses:

```text
PATCH ?fieldManager=ani-inference-gateway-publisher&force=true
Content-Type: application/apply-patch+yaml
```

Test 200/201 success, 404 mapping, 409/422 failure, timeout and an error body containing a fake credential. The returned error string may contain only method, kind/name and HTTP status, never the body.

- [ ] **Step 5: Implement the in-cluster REST client**

Read only:

```text
/var/run/secrets/kubernetes.io/serviceaccount/token
/var/run/secrets/kubernetes.io/serviceaccount/ca.crt
KUBERNETES_SERVICE_HOST
KUBERNETES_SERVICE_PORT_HTTPS
```

Create an `http.Client` with the service-account CA, Bearer transport, and configured request timeout. Marshal `Object.Body` as JSON, which is valid YAML for server-side apply. `Delete` treats 404 as success. Do not add Kubernetes module dependencies to `go.mod`.

- [ ] **Step 6: Run focused client/render tests**

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/gatewaypublish -count=1
GOWORK=off go test -race ./internal/gatewaypublish -count=1
GOWORK=off go vet ./internal/gatewaypublish
cd ../..
git diff --check
```

Expected GREEN: rendering and HTTP client tests pass without live Kubernetes access.

- [ ] **Step 7: Hold the commit checkpoint**

After explicit approval only:

```bash
git add repo/services/inference-service/internal/gatewaypublish/types.go repo/services/inference-service/internal/gatewaypublish/render.go repo/services/inference-service/internal/gatewaypublish/render_test.go repo/services/inference-service/internal/gatewaypublish/kubeclient.go repo/services/inference-service/internal/gatewaypublish/kubeclient_test.go
git commit -m "feat(inference): render dynamic AI Gateway resources"
```

---

### Task 6: Reconcile Publication and Build the Independent Publisher Process

**Files:**
- Create: `repo/services/inference-service/internal/gatewaypublish/config.go`
- Create: `repo/services/inference-service/internal/gatewaypublish/config_test.go`
- Create: `repo/services/inference-service/internal/gatewaypublish/reconciler.go`
- Create: `repo/services/inference-service/internal/gatewaypublish/reconciler_test.go`
- Create: `repo/services/inference-service/cmd/inference-gateway-publisher/main.go`
- Create: `repo/services/inference-service/cmd/inference-gateway-publisher/main_test.go`
- Create: `repo/services/inference-service/Dockerfile.publisher`
- Modify: `repo/Makefile`

**Interfaces:**
- Consumes: `repository.PublicationStore` from Task 1 and `gatewaypublish.KubeAPI/Render` from Task 5.
- Produces: one claimed publication reconciliation per `RunOnce`, readiness endpoint, non-root publisher image and image Make target.

- [ ] **Step 1: Write configuration RED tests**

Test these exact environment rules:

```go
func TestLoadConfigNormalizesPublicBaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://redacted")
	t.Setenv("INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL", "https://ai.example.com/")
	cfg, err := LoadConfig()
	if err != nil || cfg.PublicBaseURL.String() != "https://ai.example.com" {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}
```

Reject missing URL, relative URL, userinfo, query, fragment and non-HTTPS production URL. Permit HTTP only when `INFERENCE_AI_GATEWAY_ALLOW_HTTP=true`. Defaults are namespace `ani-aigw`, Gateway `ani-aigw`, controller `gateway.envoyproxy.io/gatewayclass-controller`, reconcile interval `1s`, request timeout `2s`, status timeout `45s`, lease `30s`, health port `9206`.

Use this exact configuration shape:

```go
type Config struct {
	DatabaseURL       string
	PublicBaseURL     *url.URL
	GatewayNamespace  string
	GatewayName       string
	GatewayController string
	ReconcileInterval time.Duration
	RequestTimeout    time.Duration
	StatusTimeout     time.Duration
	LeaseDuration     time.Duration
	HealthPort        int
	AllowHTTP         bool
}
```

Normalize a non-root base path by removing only its trailing slash; retain the path prefix when constructing invocation URLs.

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/gatewaypublish -run Config -count=1
```

Expected RED: config types/functions do not exist.

- [ ] **Step 2: Write the publish/unpublish RED matrix**

The fake store and fake Kube API must prove:

```text
publish: Backend apply -> AIServiceBackend apply -> AIGatewayRoute apply -> current Gateway/objects status -> Complete(published, URL)
unpublish: delete AIGatewayRoute -> confirm 404 -> delete AIServiceBackend -> delete Backend -> confirm 404 -> Complete(unpublished, empty URL)
```

Add tests for repeated apply, partial apply failure, stale Gateway generation, stale route parent conditions, wrong controller, old database generation completing late, publisher restart with existing resources, and delete timeout. No failure path may call `CompletePublication`.

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/gatewaypublish -run 'Publish|Unpublish|Stale|Restart' -count=1
```

Expected RED: `Reconciler` is undefined.

- [ ] **Step 3: Implement condition predicates against installed status shapes**

Use current object metadata generation, never the database generation, for Kubernetes condition freshness:

```go
func currentTrue(conditions []Condition, typ string, generation int64) bool {
	for _, condition := range conditions {
		if condition.Type == typ && condition.Status == "True" && condition.ObservedGeneration == generation {
			return true
		}
	}
	return false
}
```

Acceptance rules:

- Gateway: top-level `Programmed=True` for current metadata generation.
- Backend: top-level `Accepted=True` for current metadata generation.
- AIServiceBackend: top-level `Accepted=True` for current metadata generation.
- AIGatewayRoute: a parent matching group `gateway.networking.k8s.io`, kind `Gateway`, namespace `ani-aigw`, name `ani-aigw`, controller `gateway.envoyproxy.io/gatewayclass-controller`; that parent has current-generation `Accepted=True` and `ResolvedRefs=True`.

If the runtime-installed v1beta1 route serializes only top-level conditions, fail closed and record `GATEWAY_ROUTE_STATUS_UNSUPPORTED`; do not silently weaken the controller/parent check.

- [ ] **Step 4: Implement the reconciler with bounded persistence**

Core flow:

```go
func (r *Reconciler) RunOnce(ctx context.Context) (bool, error) {
	target, claimed, err := r.store.ClaimPublication(ctx, r.owner, r.now(), r.lease)
	if err != nil || !claimed { return claimed, err }
	if target.Desired == domain.PublicationPublished {
		return true, r.publish(ctx, target)
	}
	return true, r.unpublish(ctx, target)
}
```

Publish derives `invocation_url` only after every condition is current:

```go
path, ok := service.OpenAIPathForTask(target.Task)
if !ok { return r.fail(ctx, target, "GATEWAY_TASK_UNSUPPORTED") }
invocationURL := strings.TrimRight(r.publicBase.String(), "/") + path
```

Errors passed to `FailPublication` are stable reason codes plus a bounded, redacted category. Do not persist raw Kubernetes response bodies, runtime endpoints or connection strings in `publication_last_error`.

- [ ] **Step 5: Implement health/readiness and process wiring**

The process must expose:

```text
GET :9206/healthz -> 200 while process is alive
GET :9206/readyz  -> 200 only after config, DB and Kubernetes client initialization succeed
```

Invalid public URL starts the health server, keeps readiness 503, logs only `publisher configuration invalid`, and never calls `RunOnce`. Normal shutdown cancels the loop and gives the HTTP server five seconds to stop.

Build only the publisher command:

```dockerfile
FROM golang:1.25.13-alpine AS build
WORKDIR /src
COPY pkg ./pkg
COPY sdks/core/go ./sdks/core/go
COPY services/inference-service ./services/inference-service
ENV GOWORK=off CGO_ENABLED=0
RUN apk add --no-cache git ca-certificates
RUN cd services/inference-service && go build -ldflags "-s -w" -o /out/inference-gateway-publisher ./cmd/inference-gateway-publisher

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -H -u 65532 ani
COPY --from=build /out/inference-gateway-publisher /usr/local/bin/inference-gateway-publisher
USER 65532:65532
EXPOSE 9206
ENTRYPOINT ["/usr/local/bin/inference-gateway-publisher"]
```

- [ ] **Step 6: Add the image target and verify the process**

Add:

```make
image-inference-service:
	docker build -f services/inference-service/Dockerfile -t $(REGISTRY)/inference-service:$(VERSION) .

image-inference-gateway-publisher:
	docker build -f services/inference-service/Dockerfile.publisher -t $(REGISTRY)/inference-gateway-publisher:$(VERSION) .
```

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/gatewaypublish ./cmd/inference-gateway-publisher -count=1
GOWORK=off go test -race ./internal/gatewaypublish ./cmd/inference-gateway-publisher -count=1
GOWORK=off go vet ./internal/gatewaypublish ./cmd/inference-gateway-publisher
GOWORK=off go build ./cmd/inference-gateway-publisher
cd ../..
git diff --check
```

Expected GREEN: process tests, race, vet and build pass. Docker build is a separate approved action when an image is needed; do not claim it passed unless actually run.

- [ ] **Step 7: Hold the commit checkpoint**

After explicit approval only:

```bash
git add repo/services/inference-service/internal/gatewaypublish/config.go repo/services/inference-service/internal/gatewaypublish/config_test.go repo/services/inference-service/internal/gatewaypublish/reconciler.go repo/services/inference-service/internal/gatewaypublish/reconciler_test.go repo/services/inference-service/cmd/inference-gateway-publisher/main.go repo/services/inference-service/cmd/inference-gateway-publisher/main_test.go repo/services/inference-service/Dockerfile.publisher repo/Makefile
git commit -m "feat(inference): add AI Gateway publication worker"
```

---

### Task 7: Coordinate Publish and Withdraw with the Inference Lifecycle

**Files:**
- Modify: `repo/services/inference-service/internal/repository/postgres.go`
- Modify: `repo/services/inference-service/internal/repository/postgres_test.go`
- Modify: `repo/services/inference-service/internal/service/control.go`
- Modify: `repo/services/inference-service/internal/service/control_test.go`
- Modify: `repo/services/inference-service/internal/reconcile/worker.go`
- Modify: `repo/services/inference-service/internal/reconcile/worker_test.go`
- Modify: `repo/services/inference-service/internal/reconcile/flow_test.go`

**Interfaces:**
- Consumes: publication store/status from Task 1 and Publisher completion from Task 6.
- Produces: workload-ready-before-publish, route-withdrawn-before-stop/delete/restart, scale-with-stable-route and public URL lifecycle.

- [ ] **Step 1: Write request-path ordering tests**

For `stop`, `restart`, and `delete`, assert the controller writes the operation but does not call runtime synchronously:

```go
func TestStopDoesNotTouchRuntimeBeforePublisherWithdraws(t *testing.T) {
	runtime := &runtimeStub{}
	controller := NewController(store, fixedNow).WithRuntime(runtime)
	_, err := controller.Lifecycle(ctx, tenantID, serviceID, key, domain.ActionStop)
	if err != nil { t.Fatal(err) }
	if runtime.lifecycleCalls != 0 { t.Fatalf("runtime calls = %d", runtime.lifecycleCalls) }
}
```

For scale, preserve the existing synchronous/worker behavior and do not change publication fields.

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/service -run 'Stop.*Publisher|Restart.*Publisher|Delete.*Publisher|Scale' -count=1
```

Expected RED: current `dispatchMutation` sends all created mutations to runtime.

- [ ] **Step 2: Write worker withdrawal gates**

Use a fake store returning false then true from `PublicationWithdrawn`:

```go
processed, err := worker.RunOnce(ctx)
if !processed || err != nil { t.Fatalf("processed=%v err=%v", processed, err) }
if runtime.stopCalls != 0 { t.Fatal("runtime stopped before route withdrawal") }
if store.lastFailureCode != "GATEWAY_UNPUBLISH_PENDING" { t.Fatalf("code=%q", store.lastFailureCode) }
```

On the next claim with withdrawn=true, runtime lifecycle/delete is allowed. Repeat for stop, restart and delete. Add a create test proving `desired publication=published` is not written until Ready + Health + task Smoke all pass.

- [ ] **Step 3: Make mutation SQL atomically request withdrawal**

When a new stop/restart/delete generation is accepted, the same transaction that updates `generation` must set:

```sql
UPDATE inference_services
SET desired_spec = $3, desired_state = $4, generation = $5,
    current_operation_id = $6, status = $7, updated_at = $8,
    publication_desired = CASE WHEN $9 THEN 'unpublished' ELSE publication_desired END,
    publication_generation = CASE WHEN $9 THEN $5 ELSE publication_generation END,
    publication_phase = CASE WHEN $9 THEN 'pending' ELSE publication_phase END,
    publication_last_error = CASE WHEN $9 THEN NULL ELSE publication_last_error END,
    publication_updated_at = CASE WHEN $9 THEN $8 ELSE publication_updated_at END,
    invocation_url = CASE WHEN $9 THEN NULL ELSE invocation_url END
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
```

Pass `$9=true` only for stop, restart and delete. Start does not publish at mutation time. Scale does not change any publication column. Replayed and no-op transitions do not create a new publication generation.

- [ ] **Step 4: Publish only on successful runtime completion**

When `ApplyObservation` completes create/start/restart with `StatusRunning`, the same fenced transaction sets:

```sql
publication_desired = CASE WHEN $11 AND $14 THEN 'published' ELSE publication_desired END,
publication_generation = CASE WHEN $11 AND $14 THEN $3 ELSE publication_generation END,
publication_phase = CASE WHEN $11 AND $14 THEN 'pending' ELSE publication_phase END,
publication_last_error = CASE WHEN $11 AND $14 THEN NULL ELSE publication_last_error END,
publication_updated_at = CASE WHEN $11 AND $14 THEN $10 ELSE publication_updated_at END,
invocation_url = CASE WHEN $11 AND $14 THEN NULL ELSE invocation_url END
```

Add `Publish bool` to `repository.Observation`, pass it to `ApplyObservation` as `$14`, and set it true only for successful create/start/restart observations. Stop/delete completion leaves publication `unpublished`. Scale completion leaves the already accepted route and URL unchanged because the stable ClusterIP identity is unchanged.

- [ ] **Step 5: Gate worker runtime mutations on withdrawal**

Before `applyRuntimeIntent` or `runtime.Delete`:

```go
if requiresWithdrawal(operation.Type) {
	withdrawn, err := w.store.PublicationWithdrawn(ctx, operation.TenantID, operation.ServiceID, operation.TargetGeneration)
	if err != nil {
		return true, w.retry(ctx, operation, "GATEWAY_UNPUBLISH_CHECK_FAILED", err)
	}
	if !withdrawn {
		return true, w.retry(ctx, operation, "GATEWAY_UNPUBLISH_PENDING", errors.New("gateway route withdrawal is pending"))
	}
}
```

`requiresWithdrawal` returns true only for stop, restart and delete. A failed publisher never permits the runtime mutation; the operation remains bounded and observable rather than leaving a route to a dead backend.

- [ ] **Step 6: Run full inference lifecycle tests**

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./internal/service ./internal/reconcile ./internal/repository -count=1
GOWORK=off go test -race ./internal/service ./internal/reconcile -count=1
GOWORK=off go vet ./internal/service ./internal/reconcile ./internal/repository
cd ../..
git diff --check
```

Expected GREEN: create/start publish only after smoke; stop/restart/delete wait for withdrawal; scale retains route; stale generations cannot re-publish.

- [ ] **Step 7: Hold the commit checkpoint**

After explicit approval only:

```bash
git add repo/services/inference-service/internal/repository/postgres.go repo/services/inference-service/internal/repository/postgres_test.go repo/services/inference-service/internal/service/control.go repo/services/inference-service/internal/service/control_test.go repo/services/inference-service/internal/reconcile/worker.go repo/services/inference-service/internal/reconcile/worker_test.go repo/services/inference-service/internal/reconcile/flow_test.go
git commit -m "feat(inference): coordinate gateway publication with lifecycle"
```

---

### Task 8: Install Shared C41 Security, Publisher RBAC and Network Boundaries

**Files:**
- Create: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c41.yaml`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_c41_manifest.py`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_c41_manifest_test.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Consumes: adapter port 9002, inference-service gRPC 9104, publisher health 9206, shared Gateway `ani-aigw`, existing `ani-services-runtime/database_url` Secret reference.
- Produces: shared Gateway-targeted ext_auth with route recomputation, publisher Deployment/SA/RBAC, adapter/inference egress and no static per-service routes.

- [ ] **Step 1: Write manifest validator mutation tests**

Reject each of these mutations independently:

```text
SecurityPolicy targets HTTPRoute instead of Gateway
failOpen=true or missing
recomputeRoute=false or missing
static contextExtensions present
x-ai-eg-model missing from headersToExtAuth
INFERENCE_SERVICE_GRPC_ADDR missing
adapter egress to inference-service:9104 missing
publisher public base URL uses ConfigMap/Secret instead of direct value
publisher ServiceAccount lacks token or Role can read secrets/workloads
publisher manages cluster-scoped resources
static Backend/AIServiceBackend/AIGatewayRoute embedded in shared manifest
publisher/adapter runs as root or can escalate
AK/plain credential literal appears in YAML
```

Run:

```bash
cd repo
python3 scripts/validate_inference_envoy_ai_gateway_c41_manifest_test.py
```

Expected RED: validator/module missing.

- [ ] **Step 2: Create the Gateway-level SecurityPolicy**

Use the installed field placement exactly:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: ani-inference-ext-auth
  namespace: ani-aigw
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: ani-aigw
  extAuth:
    failOpen: false
    statusOnError: 503
    recomputeRoute: true
    headersToExtAuth:
      - authorization
      - x-ai-eg-model
      - accept
    grpc:
      backendRefs:
        - name: envoy-authz-adapter
          port: 9002
```

Do not add `contextExtensions`, body forwarding or per-service values.

- [ ] **Step 3: Update the adapter deployment and network policy in the new manifest**

Adapter environment is exactly non-secret configuration:

```yaml
- name: AUTH_SERVICE_GRPC_ADDR
  value: ani-auth-service.ani-system.svc.cluster.local:9101
- name: INFERENCE_SERVICE_GRPC_ADDR
  value: inference-service.ani-system.svc.cluster.local:9104
- name: AUTH_TIMEOUT
  value: 2s
- name: INFERENCE_TIMEOUT
  value: 2s
- name: GRPC_PORT
  value: "9002"
```

NetworkPolicy permits owning Envoy ingress on 9002, DNS, auth-service 9101 and inference-service 9104 only. The adapter ServiceAccount keeps `automountServiceAccountToken: false`. The selected route, not the adapter response, removes the trusted tenant/service headers and `x-ai-eg-model` before vLLM.

- [ ] **Step 4: Add publisher deployment and namespace-scoped RBAC**

Run the Publisher in `ani-system` so it can reuse the existing inference database Secret reference; bind it into `ani-aigw` with a RoleBinding whose subject namespace is `ani-system`.

Role permissions are exactly:

```yaml
- apiGroups: ["gateway.envoyproxy.io"]
  resources: ["backends"]
  verbs: ["get", "list", "watch", "create", "patch", "delete"]
- apiGroups: ["aigateway.envoyproxy.io"]
  resources: ["aiservicebackends", "aigatewayroutes"]
  verbs: ["get", "list", "watch", "create", "patch", "delete"]
- apiGroups: ["gateway.networking.k8s.io"]
  resources: ["gateways"]
  verbs: ["get"]
```

No permission includes secrets, pods, deployments, services, namespaces, CRDs or status updates. Publisher env includes:

```yaml
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: ani-services-runtime
      key: database_url
- name: INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL
  value: https://ai.example.com
- name: INFERENCE_AI_GATEWAY_NAMESPACE
  value: ani-aigw
- name: INFERENCE_AI_GATEWAY_NAME
  value: ani-aigw
```

The Pod uses non-root UID 65532, read-only root filesystem, dropped capabilities, RuntimeDefault seccomp, health/readiness on 9206 and `automountServiceAccountToken: true` only for this Publisher SA.

The Publisher NetworkPolicy permits DNS TCP/UDP 53, the selected PostgreSQL pod on TCP 5432, and outbound TCP 443 for the Kubernetes API service. The portable TCP 443 rule has no destination selector because Kubernetes NetworkPolicy cannot consistently select the API server's host-network endpoint; no other arbitrary destination port is allowed.

Use C41 image references in this manifest:

```yaml
envoy-authz-adapter: docker.changqingyun.cn/ani/envoy-authz-adapter:c41-20260831
inference-gateway-publisher: docker.changqingyun.cn/ani/inference-gateway-publisher:c41-20260831
```

The existing `inference-service` Deployment is not redefined by this Gateway manifest. Before live execution it must run `docker.changqingyun.cn/ani/inference-service:c41-20260831`; the runner verifies that exact image and fails without mutating the Deployment when it does not match.

- [ ] **Step 5: Preserve the shared global limit and validate no per-service resources**

Carry forward the Gateway-targeted shared 600 requests/minute `BackendTrafficPolicy`. Do not publish product policies into BackendTrafficPolicy. The static C41 manifest contains no `Backend`, `AIServiceBackend` or `AIGatewayRoute`; the Publisher creates those dynamically.

- [ ] **Step 6: Add Make target, run static tests and server dry-run**

Add:

```make
validate-inference-envoy-ai-gateway-c41-manifest:
	$(PYTHON) scripts/validate_inference_envoy_ai_gateway_c41_manifest_test.py
	$(PYTHON) scripts/validate_inference_envoy_ai_gateway_c41_manifest.py
```

Run:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-c41-manifest
kubectl apply --server-side --dry-run=server -f deploy/real-k8s-lab/inference-envoy-ai-gateway-c41.yaml
git diff --check
```

Expected: validator tests pass; server dry-run accepts every object and persists nothing. The dry-run is schema evidence only, not live readiness.

- [ ] **Step 7: Hold the commit checkpoint**

After explicit approval only:

```bash
git add repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c41.yaml repo/scripts/validate_inference_envoy_ai_gateway_c41_manifest.py repo/scripts/validate_inference_envoy_ai_gateway_c41_manifest_test.py repo/Makefile
git commit -m "feat(gateway): add shared C41 AI Gateway resources"
```

---

### Task 9: Build the Redacted Multi-Tenant C41 Live Gate

**Files:**
- Create: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c41-live-gate.yaml`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_c41_live_gate.py`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_c41_live_gate_test.py`
- Create: `repo/scripts/run_inference_envoy_ai_gateway_c41_live.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Consumes: control-plane login bearer tokens for two tenants, model/image inputs for generate and embed, a public Envoy AI Gateway URL and an approved kubeconfig.
- Produces: import-safe live runner and redacted JSON evidence proving dynamic publish, tenant isolation, AK auth, rate limits, lifecycle and cleanup.

- [ ] **Step 1: Write runner safety and contract tests first**

Tests must mock `urllib` and `subprocess`; they must never make a real HTTP or Kubernetes call. Cover:

```text
no import-time side effects
HTTPS/public URL validation
control-plane and AI Gateway URLs cannot be ClusterIP/.svc hosts
temporary AK registered for cleanup before key value validation
evidence atomic write with mode 0600
Authorization, Bearer, ani_ values, prompts, completions and vectors absent from evidence
kubectl errors do not include command output that could contain credentials
runner never executes kubectl get secret data or decodes Secret values
only runner-created AKs/services/policies are cleanup targets
failed cleanup makes the gate fail
```

Run:

```bash
cd repo
python3 scripts/validate_inference_envoy_ai_gateway_c41_live_gate_test.py
```

Expected RED: validator/runner modules missing.

- [ ] **Step 2: Define exact required environment and checks**

The live contract requires:

```yaml
required_env:
  - KUBECONFIG
  - ANI_C41_CONTROL_PLANE_URL
  - ANI_C41_GATEWAY_URL
  - ANI_C41_TENANT_A_ACCESS_TOKEN
  - ANI_C41_TENANT_B_ACCESS_TOKEN
  - ANI_C41_CHAT_MODEL_VERSION_ID
  - ANI_C41_EMBED_MODEL_VERSION_ID
  - ANI_C41_CHAT_IMAGE_REF
  - ANI_C41_EMBED_IMAGE_REF
```

Both URLs must be external/port-forward HTTP(S) endpoints, not internal ClusterIP DNS. Model versions and digest-pinned images are supplied; the runner does not invent catalog records or pull credentials.

- [ ] **Step 3: Implement safe temporary control-plane resources**

The runner:

1. Creates tenant A AK, tenant B AK, one RPM=1 AK and one revoked AK via `POST /auth/api-keys`.
2. Registers each key ID in cleanup before validating the returned one-time plaintext key.
3. Creates tenant A generate `ani-c41-shared`, tenant B generate `ani-c41-shared`, tenant A embed `ani-c41-embed`, and tenant A-only generate `ani-c41-a-only` through `/api/v1/svc/inference-services` with unique idempotency keys.
4. Polls control-plane resources until workload status is running and `invocation_url` equals the expected public chat/embeddings URL.
5. Never writes key values, tokens, model inputs or outputs to disk.

Before creating resources, it snapshots the manifest-owned Publisher Deployment, sets `INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL` to the validated `ANI_C41_GATEWAY_URL`, sets `INFERENCE_AI_GATEWAY_ALLOW_HTTP=true` only when that URL uses HTTP, waits for readiness, and then records the new baseline. The `finally` block restores the exact prior Deployment state or removes only the runner-created Deployment when it was previously absent.

Use deterministic cleanup stacks containing tenant token + resource ID only. Cleanup order is policies, AKs, inference services; it waits until Publisher-owned resources disappear before reporting cleanup success.

- [ ] **Step 4: Implement the full data-plane acceptance matrix**

The runner performs and records only pass/fail/count metadata for:

```text
tenant A chat JSON 200 with non-empty choices
tenant A chat SSE 200 with data frames and [DONE]
tenant A embeddings 200 with non-empty numeric vector
tenant A and B same model route to their own service markers/counters
tenant B request for ani-c41-a-only -> 404
generate on /v1/embeddings -> 404
embed on /v1/chat/completions -> 404
missing/random/login JWT/revoked AK -> 401
body model A plus client x-ai-eg-model B still resolves A
client x-ani tenant/service spoof cannot change route
/v1/models -> 404
RPM first allowed, second -> 429 with positive Retry-After
service+AK policy overrides lower-specificity tenant policy
adapter/auth/inference/Redis fault injection -> 503 and no vLLM counter increase
stop removes route before workload stop; request -> 404
start republishes and request -> 200
delete removes all three owner-labelled resources and releases same-tenant model name
Publisher restart and duplicate reconcile create no duplicates
```

The vLLM input/output checks remain in memory. Evidence stores booleans such as `chat_choices_nonempty`, `embedding_vector_length_gt_zero`, and status codes only.

- [ ] **Step 5: Implement redaction without reading Secret contents**

Scan relevant adapter, Envoy, inference-service, Publisher and selected vLLM logs in memory for full temporary keys and a non-trivial prefix. Query only Secret metadata to assert C41 did not create a managed AK Secret; do not run `kubectl get secrets -o json`, do not decode `.data`, and do not serialize log text.

Write evidence atomically:

```python
fd, temp_name = tempfile.mkstemp(prefix=target.name + ".", dir=target.parent)
try:
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(redacted_evidence, handle, ensure_ascii=False, indent=2, sort_keys=True)
        handle.write("\n")
    os.replace(temp_name, target)
finally:
    if os.path.exists(temp_name):
        os.unlink(temp_name)
```

- [ ] **Step 6: Add local contract targets and verify without live execution**

Add:

```make
validate-inference-envoy-ai-gateway-c41-live-gate:
	$(PYTHON) scripts/validate_inference_envoy_ai_gateway_c41_live_gate_test.py
	$(PYTHON) scripts/validate_inference_envoy_ai_gateway_c41_live_gate.py

run-inference-envoy-ai-gateway-c41-live:
	$(PYTHON) scripts/run_inference_envoy_ai_gateway_c41_live.py
```

Run local-only checks:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-c41-live-gate
python3 -m py_compile scripts/validate_inference_envoy_ai_gateway_c41_live_gate.py scripts/validate_inference_envoy_ai_gateway_c41_live_gate_test.py scripts/run_inference_envoy_ai_gateway_c41_live.py
git diff --check
```

Expected GREEN: contract/safety tests pass; no live runner, kubectl mutation, API key creation or HTTP request occurs.

- [ ] **Step 7: Execute live only after a new explicit approval**

After the user provides live authorization and required ephemeral credentials through environment variables, run:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make run-inference-envoy-ai-gateway-c41-live
```

Expected: every declared check passes, cleanup completes, and evidence contains no sensitive values. If any check fails, report the exact non-secret check ID, preserve only resources explicitly requested by the user, and do not mark C41 runtime ready.

- [ ] **Step 8: Hold the commit checkpoint**

After explicit approval only:

```bash
git add repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c41-live-gate.yaml repo/scripts/validate_inference_envoy_ai_gateway_c41_live_gate.py repo/scripts/validate_inference_envoy_ai_gateway_c41_live_gate_test.py repo/scripts/run_inference_envoy_ai_gateway_c41_live.py repo/Makefile
git commit -m "test(inference): add dynamic AI Gateway live gate"
```

---

### Task 10: Run Full Gates and Close the Feature Batch

**Files:**
- Create: `repo/development-records/inference-envoy-ai-gateway-c41.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

**Interfaces:**
- Consumes: verified results from Tasks 1-9.
- Produces: accurate C41 local/logic or live readiness record, full repository gates and a reviewable shipping set.

- [ ] **Step 1: Run focused module gates from clean command boundaries**

Run:

```bash
cd repo/services/inference-service
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
cd ../envoy-authz-adapter
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
```

Expected: all module tests, race tests and vet pass.

- [ ] **Step 2: Run C41 and Services gates**

Run:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make validate-inference-gateway-publication-migration
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-c41-manifest
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-c41-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-inference-access-policy-contract
PATH=/tmp/ani-pybin:$PATH make validate-inference-control-plane
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec
PATH=/tmp/ani-pybin:$PATH make validate-services-contract
```

Expected: every focused contract and validator exits 0.

- [ ] **Step 3: Run repository-wide required gates**

Run:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make test
PATH=/tmp/ani-pybin:$PATH make validate-services
PATH=/tmp/ani-pybin:$PATH make validate-architecture
git diff --check
```

Expected: all commands pass. Because `make validate-services` regenerates files, inspect `git status --short` immediately afterward and ensure no unrelated generated drift remains.

- [ ] **Step 4: Perform security and scope review**

Run:

```bash
cd /root/kubercon/ANI
rg -n 'Authorization: Bearer|ani_(dev|prod)_[A-Za-z0-9_-]+|postgres://[^[:space:]]+:[^[:space:]@]+@' docs/superpowers repo/development-records repo/deploy/real-k8s-lab repo/scripts
git diff --name-only
git status --short
```

Expected: no committed secret material; changed files match the File Map plus pre-existing user-owned changes. Review the staged diff separately from the working-tree diff.

- [ ] **Step 5: Write the feature record from actual evidence only**

The C41 record must include:

```markdown
- Batch: INFERENCE-SERVICE-C41
- Contract: no new Services v1 endpoint or field; description-only clarification
- Local gates: exact commands and exit results
- Kubernetes dry-run: exact command and accepted object count
- Live status: `not-run`, `failed`, or `passed` from actual execution
- Readiness: local/logic verified unless the complete live matrix passed
- Security: no AK/Authorization/prompt/vector persisted; no Secret data read
- Known boundary: `/v1/models` remains 404; billing, multi-cluster and weighted backends remain out of scope
```

Update README, CURRENT-SPRINT and ANI-06 consistently. Do not claim `runtime ready` or `production ready` from static tests or server dry-run.

- [ ] **Step 6: Re-run documentation and diff gates**

Run:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

Expected: documentation entrypoint and whitespace checks pass.

- [ ] **Step 7: Request final code review before shipping**

Review against:

```text
tenant isolation and 404 non-leakage
AK-only authentication and fail-closed dependencies
trusted header overwrite plus recomputeRoute
policy specificity and QPS/RPM/concurrency
publication generation/lease fencing
withdraw-before-runtime lifecycle ordering
Publisher least privilege and no Secret reads
public invocation URL without ClusterIP exposure
live evidence redaction and accurate readiness wording
```

Fix findings with new failing tests and rerun the affected focused/full gates.

- [ ] **Step 8: Hold the final commit/push/PR checkpoint**

Only after explicit user approval:

```bash
git fetch upstream main
git branch --show-current
git add repo/development-records/inference-envoy-ai-gateway-c41.md repo/development-records/README.md repo/CURRENT-SPRINT.md ANI-06-开发计划.md
git diff --cached --check
git commit -m "docs: record C41 dynamic AI Gateway publication"
git push origin main
```

Wait for personal repository Actions to turn green. Only then, and only after user confirmation, create or update the upstream PR from `origin/main` to `upstream/main` with the actual test commands and Actions run link.
