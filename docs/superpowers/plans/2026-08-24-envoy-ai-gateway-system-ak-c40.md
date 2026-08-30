# Envoy AI Gateway 系统 AK C40 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立一个仓库管理、真实集群可验证的 Envoy AI Gateway 单推理服务调用入口，使有效 `ani_*` 系统 AK 经现有 Auth gRPC 校验后调用 `/v1/chat/completions`，并保持租户隔离、撤销/过期/RPM、SSE 与 fail-closed 语义。

**Architecture:** 新建独立无状态 Go 服务 `envoy-authz-adapter`，实现 `envoy.service.auth.v3.Authorization/Check`，将 Bearer AK 委托给现有 `auth.v1.AuthService/ValidateToken`，再比较受信任 route context 中的 owner tenant。Envoy AI Gateway v1beta1 负责从请求体提取模型并路由到一个既有 vLLM ClusterIP；C40 使用静态绑定，不修改 inference-service、Auth proto 或公开 OpenAPI。

**Tech Stack:** Go 1.25、gRPC、`github.com/envoyproxy/go-control-plane/envoy v1.37.0`、Envoy AI Gateway v1.0.0 CRD v1beta1、Envoy Gateway v1.8.x SecurityPolicy、Kubernetes、Python 3.12 live-gate validator/runner。Envoy Go API 使用 `v1.37.0`，因为仓库既有 `google.golang.org/grpc v1.82.1` 通过 Go MVS 已要求该版本；这不改变集群侧 Envoy Gateway v1.8.x / AI Gateway v1.0.0 的部署版本。

## Global Constraints

- 只在本地 `main` 工作；不得创建分支或 worktree。
- 不修改 `repo/api/openapi/v1.yaml`、`repo/api/openapi/services/v1.yaml` 或 Auth proto；本批没有新公开 API。
- adapter 必须直接调用现有 `AuthService/ValidateToken`，不得新增 `ValidateAPIKey` RPC、数据库访问或 AK 缓存。
- 第一版只要求有效 `ani_*` AK，不检查 `scope:inference:invoke` 或 `scope:inference:*`。
- 必须比较 `ValidateToken` 返回的 tenant 与受信任 route context 中的 owner tenant。
- AK 原文不得到达 vLLM、日志、证据文件或 Kubernetes Secret。
- ext_authz 必须 `failOpen: false`，错误状态配置为 503。
- C40 只产品化一个静态服务；不实现 lifecycle controller，不填写 `invocation_url`。
- 真实集群使用现有 Envoy AI Gateway v1.0.0 / Envoy Gateway v1.8.x，不在本批升级。
- 每项代码改动遵循 TDD：先失败测试，再最小实现，再通过测试。
- 未获得用户明确 commit/ship 授权前，不执行任何计划中的 commit checkpoint。

## File Map

| 文件 | 职责 |
|---|---|
| `repo/services/envoy-authz-adapter/go.mod` / `go.sum` | adapter 独立 Go module 依赖 |
| `repo/services/envoy-authz-adapter/internal/config/config.go` | 解析 gRPC 端口、Auth 地址和超时 |
| `repo/services/envoy-authz-adapter/internal/authclient/client.go` | 对现有 `ValidateToken` 的有界 gRPC client |
| `repo/services/envoy-authz-adapter/internal/extauth/server.go` | 标准 Envoy ext_authz、AK 提取、租户比较、状态映射、header 清理 |
| `repo/services/envoy-authz-adapter/main.go` | 无状态 gRPC server、health service、优雅退出 |
| `repo/services/envoy-authz-adapter/Dockerfile` | 非 root 最小运行镜像 |
| `repo/go.work` | 把新 module 加入 workspace/CI 自动发现 |
| `repo/scripts/validate_services_boundary.py` | 将新 adapter 声明为受控 Services 源目录 |
| `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml` | adapter、NetworkPolicy、Backend、AIServiceBackend、AIGatewayRoute、SecurityPolicy |
| `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml` | C40 真实门禁契约 |
| `repo/scripts/validate_inference_envoy_ai_gateway_live_gate.py` | 清单/证据静态校验 |
| `repo/scripts/run_inference_envoy_ai_gateway_live.py` | 创建临时系统 AK 并执行真实 Chat/SSE/负例/脱敏验证 |
| `repo/Makefile` | Go 测试、镜像和 C40 validator/live gate 入口 |
| `repo/development-records/INFERENCE-SERVICE-ENVOY-AI-GATEWAY-C40.md` 等 | Feature batch 状态和真实证据闭环 |

---

### Task 1: Scaffold the isolated adapter module and repository ownership

**Files:**
- Create: `repo/services/envoy-authz-adapter/go.mod`
- Modify: `repo/go.work`
- Modify: `repo/scripts/validate_services_boundary.py`
- Modify: `repo/scripts/validate_services_boundary_test.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Produces module path `github.com/kubercloud/ani/services/envoy-authz-adapter`.
- Makes `go.work`, `GO_PACKAGES`, Services boundary validation, and CI module discovery include the adapter.

- [ ] **Step 1: Write the failing boundary test**

Add to `repo/scripts/validate_services_boundary_test.py`:

```python
def test_envoy_authz_adapter_is_a_services_owned_source_root(self) -> None:
    self.assertIn("envoy-authz-adapter", guard.SERVICES_OWNED_SOURCE_ROOTS)
    self.assertIn("services/envoy-authz-adapter", guard.GO_SCAN_ROOTS)
```

- [ ] **Step 2: Run the focused test and confirm failure**

Run:

```bash
cd repo
python scripts/validate_services_boundary_test.py
```

Expected: FAIL because `envoy-authz-adapter` is not in `SERVICES_OWNED_SOURCE_ROOTS`.

- [ ] **Step 3: Add the module and boundary registration**

Create `repo/services/envoy-authz-adapter/go.mod`:

```go
module github.com/kubercloud/ani/services/envoy-authz-adapter

go 1.25.0

require (
	github.com/envoyproxy/go-control-plane/envoy v1.37.0
	github.com/kubercloud/ani/pkg v0.0.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

replace github.com/kubercloud/ani/pkg => ../../pkg
```

Add `./services/envoy-authz-adapter` to `repo/go.work`. Add `envoy-authz-adapter` to `SERVICES_OWNED_SOURCE_ROOTS` in `validate_services_boundary.py`, and add `./services/envoy-authz-adapter/...` to `GO_PACKAGES` in `repo/Makefile`.

- [ ] **Step 4: Resolve module checksums and rerun focused validation**

Run:

```bash
cd repo/services/envoy-authz-adapter
GOWORK=off go mod tidy
cd ../..
python scripts/validate_services_boundary_test.py
python scripts/validate_services_boundary.py --root .
python scripts/list_go_modules_test.py
python scripts/list_go_modules.py
```

Expected: all tests pass; module listing contains `services/envoy-authz-adapter`.

- [ ] **Step 5: Review checkpoint**

Run `git diff --check` and review only the five declared files plus generated `go.sum`. Commit only after explicit user authorization, with suggested message `build(services): register envoy authz adapter module`.

### Task 2: Add strict process configuration and the existing Auth RPC client

**Files:**
- Create: `repo/services/envoy-authz-adapter/internal/config/config.go`
- Create: `repo/services/envoy-authz-adapter/internal/config/config_test.go`
- Create: `repo/services/envoy-authz-adapter/internal/authclient/client.go`
- Create: `repo/services/envoy-authz-adapter/internal/authclient/client_test.go`

**Interfaces:**
- Produces `config.Load() (config.Config, error)` with `GRPCPort int`, `AuthServiceGRPCAddr string`, and `AuthTimeout time.Duration`.
- Produces `authclient.New(authv1.AuthServiceClient, time.Duration) *Client`.
- Produces `(*Client).ValidateToken(context.Context, string) (*commonv1.TenantContext, error)`.

- [ ] **Step 1: Write failing config tests**

Cover defaults and invalid required input:

```go
func TestLoadDefaults(t *testing.T) {
	t.Setenv("AUTH_SERVICE_GRPC_ADDR", "ani-auth-service.ani-system.svc.cluster.local:9101")
	t.Setenv("GRPC_PORT", "")
	t.Setenv("AUTH_TIMEOUT", "")
	cfg, err := Load()
	if err != nil { t.Fatal(err) }
	if cfg.GRPCPort != 9002 || cfg.AuthTimeout != 2*time.Second { t.Fatalf("unexpected config: %+v", cfg) }
}

func TestLoadRequiresAuthAddress(t *testing.T) {
	t.Setenv("AUTH_SERVICE_GRPC_ADDR", "")
	if _, err := Load(); err == nil { t.Fatal("expected missing auth address error") }
}
```

- [ ] **Step 2: Run config tests and confirm failure**

Run `cd repo/services/envoy-authz-adapter && GOWORK=off go test ./internal/config -v`.

Expected: FAIL because `Load` does not exist.

- [ ] **Step 3: Implement minimal config parsing**

Use exactly these defaults:

```go
type Config struct {
	GRPCPort           int
	AuthServiceGRPCAddr string
	AuthTimeout         time.Duration
}

func Load() (Config, error) {
	addr := strings.TrimSpace(os.Getenv("AUTH_SERVICE_GRPC_ADDR"))
	if addr == "" { return Config{}, errors.New("AUTH_SERVICE_GRPC_ADDR is required") }
	port, err := positiveInt("GRPC_PORT", 9002)
	if err != nil { return Config{}, err }
	timeout, err := positiveDuration("AUTH_TIMEOUT", 2*time.Second)
	if err != nil { return Config{}, err }
	return Config{GRPCPort: port, AuthServiceGRPCAddr: addr, AuthTimeout: timeout}, nil
}
```

Reject explicit zero/negative/malformed values instead of silently falling back.

- [ ] **Step 4: Write a failing bounded-call client test**

Use a fake `authv1.AuthServiceClient` that blocks until `ctx.Done()` and assert:

```go
client := New(fake, 10*time.Millisecond)
_, err := client.ValidateToken(context.Background(), "ani_dev_tenant_secret")
if status.Code(err) != codes.DeadlineExceeded { t.Fatalf("got %v", err) }
```

Also assert the exact raw token is passed once to `ValidateTokenRequest.Token` and is never transformed or logged.

- [ ] **Step 5: Implement the thin Auth client**

```go
type Client struct {
	rpc     authv1.AuthServiceClient
	timeout time.Duration
}

func New(rpc authv1.AuthServiceClient, timeout time.Duration) *Client {
	return &Client{rpc: rpc, timeout: timeout}
}

func (c *Client) ValidateToken(ctx context.Context, token string) (*commonv1.TenantContext, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.rpc.ValidateToken(callCtx, &authv1.ValidateTokenRequest{Token: token})
}
```

- [ ] **Step 6: Run package tests**

Run:

```bash
cd repo/services/envoy-authz-adapter
GOWORK=off go test -mod=mod ./internal/config ./internal/authclient -v
```

Expected: PASS. Task 1 intentionally pins the Envoy API module before Task 3 imports it, so Go 1.25 treats the direct dependency as temporarily unused during Task 2. Keep that required pin and use `-mod=mod` only for this sequencing window; Task 3 must import Envoy, run `go mod tidy`, and return all verification to ordinary no-flag commands.

- [ ] **Step 7: Review checkpoint**

Run `gofmt -w internal/config internal/authclient`, rerun tests and `git diff --check`. Suggested gated commit: `feat(authz): add bounded ANI auth client`.

### Task 3: Implement the standard Envoy ext_authz decision service

**Files:**
- Create: `repo/services/envoy-authz-adapter/internal/extauth/server.go`
- Create: `repo/services/envoy-authz-adapter/internal/extauth/server_test.go`

**Interfaces:**
- Consumes `TokenValidator.ValidateToken(context.Context, string) (*commonv1.TenantContext, error)`.
- Consumes context keys `ani.target_tenant_id` and `ani.inference_service_id`.
- Produces `extauth.New(TokenValidator) *Server` implementing `authv3.AuthorizationServer`.

- [ ] **Step 1: Write the denial-path tests first**

Table-test all of these exact cases:

```text
missing Authorization                       -> 401, validator calls 0
Basic or malformed Bearer                   -> 401, validator calls 0
Bearer JWT/non-ani_ token                    -> 401, validator calls 0
missing target tenant/service context        -> 503, validator calls 0
ValidateToken Unauthenticated                -> 401
ValidateToken ResourceExhausted              -> 429
ValidateToken DeadlineExceeded/Unavailable   -> 503
valid AK but returned tenant differs         -> 404
```

Build requests through:

```go
func checkRequest(authHeader, tenantID, serviceID string) *authv3.CheckRequest {
	return &authv3.CheckRequest{Attributes: &authv3.AttributeContext{
		Request: &authv3.AttributeContext_Request{Http: &authv3.AttributeContext_HttpRequest{
			Headers: map[string]string{"authorization": authHeader},
		}},
		ContextExtensions: map[string]string{
			"ani.target_tenant_id": tenantID,
			"ani.inference_service_id": serviceID,
		},
	}}
}
```

- [ ] **Step 2: Run the focused server tests and confirm failure**

Run `cd repo/services/envoy-authz-adapter && GOWORK=off go test ./internal/extauth -v`.

Expected: FAIL because `Server` and `Check` do not exist.

- [ ] **Step 3: Implement extraction and status mapping**

Use these explicit helpers:

```go
const (
	targetTenantKey  = "ani.target_tenant_id"
	targetServiceKey = "ani.inference_service_id"
)

type TokenValidator interface {
	ValidateToken(context.Context, string) (*commonv1.TenantContext, error)
}

func bearerAPIKey(headers map[string]string) (string, bool) {
	raw := strings.TrimSpace(headers["authorization"])
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") { return "", false }
	token := strings.TrimSpace(parts[1])
	return token, strings.HasPrefix(token, "ani_")
}
```

Map gRPC codes only; never inspect error text:

```go
switch status.Code(err) {
case codes.Unauthenticated:
	return denied(http.StatusUnauthorized), nil
case codes.ResourceExhausted:
	return denied(http.StatusTooManyRequests), nil
default:
	return denied(http.StatusServiceUnavailable), nil
}
```

`denied` must set both `google.rpc.Status` and `DeniedHttpResponse.Status`. Do not include Auth error messages in the response body.

- [ ] **Step 4: Write success and security tests**

Assert that:

- matching tenant produces an OK response;
- `roles` may contain only `scope:models:read` and still succeeds, proving C40 does not enforce inference scope;
- OK response contains `HeadersToRemove: ["authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"]`;
- response body and headers do not contain the AK;
- mixed-case `Authorization` header is accepted after normalizing incoming header names;
- validator is called exactly once per `Check`, including a request representing an SSE call.

- [ ] **Step 5: Implement the success path**

```go
if principal.GetTenantId() != targetTenant {
	return denied(http.StatusNotFound), nil
}
return &authv3.CheckResponse{
	Status: &statuspb.Status{Code: int32(codes.OK)},
	HttpResponse: &authv3.CheckResponse_OkResponse{OkResponse: &authv3.OkHttpResponse{
		HeadersToRemove: []string{"authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"},
	}},
}, nil
```

Do not add `CheckPermission`, scope parsing, key ID, caching, database access, or model-body parsing.

- [ ] **Step 6: Run and race-test the adapter packages**

Run:

```bash
cd repo/services/envoy-authz-adapter
GOWORK=off go mod tidy
GOWORK=off go test ./... -v
GOWORK=off go test -race ./...
```

Expected: `go mod tidy` retains the now-used Envoy direct dependency, then both ordinary no-flag test commands PASS with no races.

- [ ] **Step 7: Review checkpoint**

Run `gofmt -w internal/extauth`, `go vet ./...`, and `git diff --check`. Suggested gated commit: `feat(authz): adapt Envoy checks to system API keys`.

### Task 4: Add the stateless process, container, and image build

**Files:**
- Create: `repo/services/envoy-authz-adapter/main.go`
- Create: `repo/services/envoy-authz-adapter/main_test.go`
- Create: `repo/services/envoy-authz-adapter/Dockerfile`
- Modify: `repo/Makefile`

**Interfaces:**
- Exposes ext_authz gRPC on `GRPC_PORT`, default 9002.
- Registers `grpc.health.v1.Health` on the same server for Kubernetes gRPC probes.
- Dials only `AUTH_SERVICE_GRPC_ADDR`; it does not call `bootstrap.MustConnect`.

- [ ] **Step 1: Write a failing registration smoke test**

Start the server on `bufconn`, register ext_authz and health, then assert:

```go
healthResp, err := healthv1.NewHealthClient(conn).Check(ctx, &healthv1.HealthCheckRequest{})
if err != nil || healthResp.Status != healthv1.HealthCheckResponse_SERVING { t.Fatalf("health: %v %#v", err, healthResp) }

_, err = authv3.NewAuthorizationClient(conn).Check(ctx, validCheckRequest)
if err != nil { t.Fatal(err) }
```

- [ ] **Step 2: Run the test and confirm failure**

Run `cd repo/services/envoy-authz-adapter && GOWORK=off go test . -v`.

Expected: FAIL because process assembly helpers do not exist.

- [ ] **Step 3: Implement minimal server assembly**

Create a `newGRPCServer(validator extauth.TokenValidator) *grpc.Server` helper which registers:

```go
srv := grpc.NewServer()
authv3.RegisterAuthorizationServer(srv, extauth.New(validator))
healthServer := health.NewServer()
healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
healthv1.RegisterHealthServer(srv, healthServer)
return srv
```

`main` loads config, dials the current plaintext cluster-internal Auth endpoint with `grpc.NewClient(cfg.AuthServiceGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))`, listens on `:<GRPCPort>`, handles SIGINT/SIGTERM, calls `GracefulStop`, and falls back to `Stop` after five seconds. Logs may contain service state and gRPC status category, never token/header values. Backend TLS is a later hardening batch because the currently deployed `ani-auth-service:9101` is plaintext.

- [ ] **Step 4: Create the non-root Dockerfile**

Follow the repository image pattern:

```dockerfile
FROM golang:1.25.13-alpine AS build
WORKDIR /src
COPY pkg ./pkg
COPY services/envoy-authz-adapter ./services/envoy-authz-adapter
ENV GOWORK=off CGO_ENABLED=0
RUN apk add --no-cache git ca-certificates
RUN cd services/envoy-authz-adapter && go build -ldflags "-s -w" -o /out/envoy-authz-adapter .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -H -u 65532 ani
COPY --from=build /out/envoy-authz-adapter /usr/local/bin/envoy-authz-adapter
USER 65532:65532
EXPOSE 9002
ENTRYPOINT ["/usr/local/bin/envoy-authz-adapter"]
```

- [ ] **Step 5: Add image targets**

Add `image-envoy-authz-adapter` to `repo/Makefile` and make the aggregate `image` target include it:

```make
image-envoy-authz-adapter:
	docker build -t $(REGISTRY)/envoy-authz-adapter:$(VERSION) \
		-f services/envoy-authz-adapter/Dockerfile \
		.
```

- [ ] **Step 6: Verify process and image**

Run:

```bash
cd repo
go test ./services/envoy-authz-adapter/... -v
make image-envoy-authz-adapter VERSION=c40-local
docker inspect harbor.ani.internal/ani/envoy-authz-adapter:c40-local --format '{{.Config.User}}'
```

Expected: tests PASS, image builds, user is `65532:65532` or `65532`.

- [ ] **Step 7: Review checkpoint**

Run `git diff --check`. Suggested gated commit: `build(authz): package Envoy authorization adapter`.

### Task 5: Repository-manage the static C40 Envoy data plane

**Files:**
- Create: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_manifest.py`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_manifest_test.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Produces public chat model ID `ani-c40-chat` and embedding model ID `ani-c40-embed`.
- Binds chat to InferenceService `182df9a4-4a6a-4eed-9d50-51a458a15f6a`, owner tenant `00000000-0000-0000-0000-000000000001`, and retained vLLM DNS `pw-182df9a4-4a6a-4eed-9d50-51a458a15f6a.ani-tenant-00000000-0000-0000-0000-000000000001.svc.cluster.local:8000`.
- Binds embeddings to retained vLLM DNS `pw-6ae6f951-415d-454f-8459-cd38d32dc58f.ani-tenant-00000000-0000-0000-0000-000000000001.svc.cluster.local:8000`.
- Produces generated HTTPRoute `ani-c40` targeted by SecurityPolicy `ani-c40-ext-auth`.

- [ ] **Step 1: Write failing manifest-validator tests**

The validator test must mutate a parsed copy and prove rejection when:

- SecurityPolicy uses `apiKeyAuth` or any Secret credential reference;
- `failOpen` is true or `statusOnError` is not 503;
- target kind is not `HTTPRoute` named `ani-c40`;
- either context extension is absent or tenant value differs;
- `Authorization` is not removed before the backend;
- either Backend is not the exact retained ClusterIP DNS/port;
- adapter Deployment contains DB, Redis, NATS, AK, JWT or password env;
- NetworkPolicy allows ingress outside the owning Envoy proxy selector or egress outside DNS/Auth.

- [ ] **Step 2: Run validator tests and confirm failure**

Run `cd repo && python scripts/validate_inference_envoy_ai_gateway_manifest_test.py`.

Expected: FAIL because validator/manifest do not exist.

- [ ] **Step 3: Add the exact C40 Kubernetes resources**

The multi-document YAML must contain:

```text
ServiceAccount/envoy-authz-adapter (automountServiceAccountToken: false)
Deployment/envoy-authz-adapter (1 replica, non-root/read-only, grpc probes, AUTH_SERVICE_GRPC_ADDR only)
Service/envoy-authz-adapter (port 9002 named grpc)
NetworkPolicy/envoy-authz-adapter
Backend/ani-c40-chat-vllm
AIServiceBackend/ani-c40-chat-vllm
Backend/ani-c40-embed-vllm
AIServiceBackend/ani-c40-embed-vllm
AIGatewayRoute/ani-c40
SecurityPolicy/ani-c40-ext-auth
```

The Deployment image is `docker.changqingyun.cn/ani/envoy-authz-adapter:c40-20260824`, uses `imagePullPolicy: Always`, and sets only `AUTH_SERVICE_GRPC_ADDR=ani-auth-service.ani-system.svc.cluster.local:9101`, `AUTH_TIMEOUT=2s`, and `GRPC_PORT=9002`.

The route backendRef must include defense-in-depth removal:

```yaml
headerMutation:
  remove:
    - Authorization
    - x-api-key
    - x-ani-tenant-id
    - x-ani-user-id
```

The route contains two exact model rules:

```yaml
rules:
  - matches:
      - headers:
          - name: x-ai-eg-model
            type: Exact
            value: ani-c40-chat
    backendRefs:
      - name: ani-c40-chat-vllm
  - matches:
      - headers:
          - name: x-ai-eg-model
            type: Exact
            value: ani-c40-embed
    backendRefs:
      - name: ani-c40-embed-vllm
```

The SecurityPolicy must be route-specific:

```yaml
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: ani-c40
  extAuth:
    failOpen: false
    statusOnError: 503
    headersToExtAuth: [authorization]
    contextExtensions:
      - name: ani.target_tenant_id
        type: Value
        value: 00000000-0000-0000-0000-000000000001
      - name: ani.inference_service_id
        type: Value
        value: 182df9a4-4a6a-4eed-9d50-51a458a15f6a
    grpc:
      backendRefs:
        - name: envoy-authz-adapter
          port: 9002
```

Ingress NetworkPolicy accepts only namespace `envoy-gateway-system` and pods with `app.kubernetes.io/name=envoy` plus owning Gateway labels. Egress permits kube-system DNS on TCP/UDP 53 and `ani-system` pods labeled `app.kubernetes.io/name=ani-auth-service` on TCP 9101.

- [ ] **Step 4: Implement and run the static manifest validator**

Expose `load_documents(path)`, `by_kind_name(documents, kind, name)`, and `validate(documents)` in the validator. It must validate exact apiVersions currently installed:

```text
AIGatewayRoute/AIServiceBackend: aigateway.envoyproxy.io/v1beta1
Backend/SecurityPolicy: gateway.envoyproxy.io/v1alpha1
Gateway API objects: gateway.networking.k8s.io/v1
```

Run:

```bash
cd repo
python scripts/validate_inference_envoy_ai_gateway_manifest_test.py
python scripts/validate_inference_envoy_ai_gateway_manifest.py
```

Expected: PASS.

- [ ] **Step 5: Add the Make target and server-side dry run**

Add:

```make
validate-inference-envoy-ai-gateway-manifest:
	python scripts/validate_inference_envoy_ai_gateway_manifest_test.py
	python scripts/validate_inference_envoy_ai_gateway_manifest.py
```

Run the validator, then:

```bash
kubectl apply --server-side --dry-run=server -f repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml
```

Expected: all resources accepted by the installed CRDs without mutating the cluster.

- [ ] **Step 6: Review checkpoint**

Run `git diff --check`. Suggested gated commit: `feat(inference): declare C40 Envoy AI Gateway route`.

### Task 6: Build the C40 real-cluster gate and redacted evidence policy

**Files:**
- Create: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_live_gate.py`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_live_gate_test.py`
- Create: `repo/scripts/run_inference_envoy_ai_gateway_live.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Runner consumes `ANI_C40_CONTROL_PLANE_URL`, `ANI_C40_GATEWAY_URL`, `ANI_C40_OWNER_ACCESS_TOKEN`, and `ANI_C40_FOREIGN_ACCESS_TOKEN` without logging them.
- Runner creates short-lived owner/foreign AKs through `/api/v1/auth/api-keys`, invokes `/v1/chat/completions`, revokes all temporary keys in `finally`, and writes only redacted evidence.
- Validator requires evidence profile `INFERENCE-SERVICE-ENVOY-AI-GATEWAY-LIVE-C40` and `status: passed` before the batch can claim live.

- [ ] **Step 1: Write failing evidence-validator tests**

Define required passed check IDs:

```python
REQUIRED_CHECKS = {
    "envoy-resources-accepted",
    "adapter-ready",
    "valid-ak-nonstream-chat",
    "valid-ak-sse-done",
    "missing-ak-401",
    "malformed-ak-401",
    "expired-ak-401",
    "revoked-ak-immediate-401",
    "rpm-limit-429",
    "foreign-tenant-404",
    "authz-fail-closed-503",
    "auth-service-unreachable-503",
    "authorization-not-forwarded",
    "clusterip-only-backend",
    "control-plane-regression-pass",
    "secret-redaction-pass",
}
```

Tests must reject evidence containing case-insensitive `authorization`, `bearer `, `ani_dev_`, `ani_prod_`, `password`, connection strings, or Secret data.

- [ ] **Step 2: Run validator tests and confirm failure**

Run `cd repo && python scripts/validate_inference_envoy_ai_gateway_live_gate_test.py`.

Expected: FAIL because validator/gate do not exist.

- [ ] **Step 3: Implement the contract/evidence validator**

Follow the existing C39 validator shape: `load_gate`, `validate_contract`, `validate_evidence`, and `main`. Require `status` to be `contract` or `live`; when `live`, require the evidence file and every required check. Require readiness claims to remain:

```json
{"envoy_invocation_ready": true, "dynamic_publication_ready": false, "runtime_ready": false}
```

- [ ] **Step 4: Implement the runner's safe AK lifecycle**

Provide helpers with these signatures:

```python
def control_request(method: str, path: str, access_token: str, body: dict | None = None) -> tuple[int, dict]: ...
def create_api_key(access_token: str, *, name: str, scopes: list[str], rpm: int, expires_at: str | None = None) -> tuple[str, str]: ...
def revoke_api_key(access_token: str, key_id: str) -> None: ...
def chat(api_key: str | None, *, stream: bool, marker: str) -> tuple[int, object]: ...
def wait_condition(kind: str, name: str, condition: str, namespace: str, timeout: int = 120) -> None: ...
```

Create keys with `scopes=["scope:models:read"]` to prove no inference scope is required. Use `try/finally` and retain only `key_id` in memory for cleanup; never print or serialize `key_value`.

- [ ] **Step 5: Implement the positive and negative live sequence**

Execute in this order:

1. Apply the C40 manifest and wait for adapter Ready plus Gateway/route/backend/policy Accepted.
2. Create owner AK at normal RPM; verify non-stream JSON has a non-empty `choices` array.
3. Verify `stream=true` receives at least one `data:` event and `[DONE]`.
4. Verify missing and malformed AK return 401.
5. Create a key expiring within five seconds, wait boundedly until expiry, verify 401.
6. Create a key, verify one success, revoke through ANI AK API, verify the next call is 401 without changing a Secret.
7. Create RPM=1 key, verify first request succeeds and the next request in the same minute returns 429.
8. Create foreign-tenant key and verify the C40 model returns 404.
9. Scale `envoy-authz-adapter` to zero, send a uniquely marked request, verify 503, restore one replica in `finally`, and confirm the marker did not reach vLLM logs.
10. Set only the adapter Deployment's `AUTH_SERVICE_GRPC_ADDR` to `127.0.0.1:1`, wait for rollout, verify a uniquely marked request returns 503, then restore `ani-auth-service.ani-system.svc.cluster.local:9101` and Ready state in `finally`; do not stop or mutate auth-service itself.
11. Read the generated `HTTPRoute/ani-c40` and require its effective request-header mutation to remove `Authorization`, `x-api-key`, and `x-ani-*`; then scan the selected vLLM Pod logs in memory for each full temporary AK and require zero matches. Record only the boolean `authorization_not_forwarded=true`, never the searched values.
12. Run `make validate-inference-control-plane` and the retained C39 validator as regressions.
13. Scan logs/evidence/Secrets by key prefix only in memory; record booleans and counts, never matched values.

- [ ] **Step 6: Add gate targets and run local validation**

Add:

```make
validate-inference-envoy-ai-gateway-live-gate:
	python scripts/validate_inference_envoy_ai_gateway_live_gate_test.py
	python scripts/validate_inference_envoy_ai_gateway_live_gate.py

run-inference-envoy-ai-gateway-live:
	python scripts/run_inference_envoy_ai_gateway_live.py
```

Run:

```bash
cd repo
make validate-inference-envoy-ai-gateway-manifest
make validate-inference-envoy-ai-gateway-live-gate
```

Expected before live execution: validator PASS with contract status and no readiness claim.

- [ ] **Step 7: Execute the live gate with explicit cluster-change approval**

After building/pushing the exact image and obtaining explicit approval for cluster writes, run:

```bash
cd repo
make image-envoy-authz-adapter REGISTRY=docker.changqingyun.cn/ani VERSION=c40-20260824
docker push docker.changqingyun.cn/ani/envoy-authz-adapter:c40-20260824
make run-inference-envoy-ai-gateway-live
make validate-inference-envoy-ai-gateway-live-gate
```

Expected: runner writes `development-records/live-evidence/inference-envoy-ai-gateway-live-20260824.json`; validator reports C40 live valid.

- [ ] **Step 8: Review checkpoint**

Inspect evidence manually for secrets, run `git diff --check`, and ensure temporary keys were revoked even if the runner failed. Suggested gated commit: `test(inference): prove Envoy system AK live path`.

### Task 7: Cut over from the ad-hoc Secret policy and close the feature batch

**Files:**
- Modify: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_live_gate.py`
- Modify: `repo/development-records/live-evidence/inference-envoy-ai-gateway-live-20260824.json`
- Create: `repo/development-records/INFERENCE-SERVICE-ENVOY-AI-GATEWAY-C40.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

**Interfaces:**
- Retires only `ani-aigw/ani-aigw-apikey` SecurityPolicy and same-named Secret after ext_authz has passed.
- Records C40 as static single-service invocation ready, explicitly not C41 dynamic publication or full runtime ready.

- [ ] **Step 1: Capture pre-delete proof and request destructive-action approval**

Run read-only checks:

```bash
kubectl get securitypolicy ani-aigw-apikey -n ani-aigw -o name
kubectl get secret ani-aigw-apikey -n ani-aigw -o name
kubectl get securitypolicy ani-c40-ext-auth -n ani-aigw -o yaml
```

Confirm C40 ext_authz evidence is passed. Stop and request explicit approval before deleting the old credential policy/Secret.

- [ ] **Step 2: Remove only the superseded ad-hoc objects**

After approval:

```bash
kubectl delete securitypolicy ani-aigw-apikey -n ani-aigw
kubectl delete secret ani-aigw-apikey -n ani-aigw
```

Do not delete the shared Gateway, GatewayClass, EnvoyProxy, AIGatewayRoute, Backend, vLLM workload, or tenant namespace.

- [ ] **Step 3: Rerun the ext_authz gate after cutover**

Run:

```bash
cd repo
make run-inference-envoy-ai-gateway-live
make validate-inference-envoy-ai-gateway-live-gate
```

Expected: valid system AK calls still pass; missing/invalid keys remain blocked; evidence includes `legacy_secret_policy_removed: true` without Secret content.

- [ ] **Step 4: Write the Feature batch record**

The record must state:

```text
Batch: INFERENCE-SERVICE-ENVOY-AI-GATEWAY-C40
Result: static single-service Envoy invocation live passed
Auth: existing ani_* AK via AuthService/ValidateToken
Scope enforcement: deferred
Tenant isolation: passed through trusted route tenant binding
Dynamic lifecycle publication: not implemented (C41)
invocation_url: unchanged/null
GPU/LWS/full platform runtime ready: not claimed
```

Include only commands actually run and the redacted evidence path.

- [ ] **Step 5: Update the three required progress indexes**

Add the same bounded conclusion to:

- `repo/development-records/README.md`
- `repo/CURRENT-SPRINT.md`
- `ANI-06-开发计划.md` Section zero/current inference sequence

Do not update `CLAUDE.md` or `ANI-DOCS-INDEX.md`; C40 does not switch the global Sprint.

- [ ] **Step 6: Run final focused and repository gates**

Run from `repo/`:

```bash
go test ./services/envoy-authz-adapter/... -v
make validate-inference-envoy-ai-gateway-manifest
make validate-inference-envoy-ai-gateway-live-gate
make validate-inference-control-plane
make validate-services
make validate-architecture
make test
git diff --check
```

Expected: all pass. `make validate-services` generated-output diff must be reviewed; do not claim a clean generated drift check until the relevant HEAD/CI state is clean.

- [ ] **Step 7: Final review and ship checkpoint**

Use `requesting-code-review` and `verification-before-completion`. Confirm `git status --short` contains only C40 files plus pre-existing unrelated untracked files. Do not commit, push, or create a PR unless the user explicitly invokes/approves the ship workflow. Suggested final commit after approval: `feat(inference): add Envoy AI Gateway system AK path`.
