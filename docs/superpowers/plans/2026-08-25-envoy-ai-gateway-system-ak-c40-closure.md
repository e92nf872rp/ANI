# Envoy AI Gateway 系统 AK C40 Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不重写 Auth AK 校验的前提下，收口已实现的 C40 静态单服务链路，证明受管推理路由只接受 `Authorization: Bearer ani_*`、公网探针/未注册路径不暴露，并完成脱敏真实集群证据和进度记录。

**Architecture:** Envoy AI Gateway 继续作为唯一推理数据面，路由级 `SecurityPolicy` 通过标准 ext_authz gRPC 调用无状态 `envoy-authz-adapter`，adapter 再调用现有 `auth.v1.AuthService/ValidateToken`。Auth 继续是 AK 存储、过期、撤销和单 AK RPM 的唯一真实来源；ANI Gateway 不代理推理流量。Envoy 和 adapter 使用 Kubernetes 工作负载探针，不增加公网健康 Route。

**Tech Stack:** Go 1.25、gRPC、Envoy ext_authz v3、Envoy AI Gateway v1.0.0、Envoy Gateway v1.8.x、Gateway API/Envoy Gateway CRD、Kubernetes、Python 3.12、YAML 契约验证与真实集群 live runner。

## Global Constraints

- 只在本地 `main` 工作，不创建分支或 worktree。
- 本计划的实现基线是当前工作区已存在的 `repo/services/envoy-authz-adapter/`、C40 manifest、validator 和 import-safe live runner；不重写已完成的 adapter 主体。
- 不修改 Core/Services OpenAPI 或 Auth proto；本批没有新公开 API。
- adapter 只调用 `AuthService/ValidateToken`；不访问数据库、Redis、NATS，不保存或缓存 AK。
- 第一版允许条件固定为 `ani_*` + `ValidateToken` 成功 + tenant 与受信 route context 一致；不校验 inference scope。
- 客户端凭据仅接受 `Authorization: Bearer ani_*`；`x-api-key`、Cookie 和查询参数不能授权。
- 受管推理 `HTTPRoute/ani-c40` 下的所有已注册推理流量都绑定同一 route-level SecurityPolicy；不使用 Gateway-level 全局鉴权。
- 公网 `/healthz`、`/readyz` 和未注册路径必须返回 `404` 且不到达 vLLM。Envoy/adapter 健康检查只使用 Kubernetes readiness/liveness probe。
- AK 原文、哈希、Bearer 头和 Kubernetes Secret data 不得进入 vLLM、日志、测试输出、evidence 或可提交文件。
- ext_authz 固定 `failOpen: false` 和 `statusOnError: 503`。
- C40 仅包含单租户、单静态推理服务、现有单 AK RPM；不包含租户总 RPM、服务级并发、Token Metering、计费、C41 动态发布或 `invocation_url`。
- 真实运行器中的临时 AK 必须在 `finally` 内逐个撤销；adapter Deployment 的 replica 和 `AUTH_SERVICE_GRPC_ADDR` 必须精确回滚。
- 每个行为变更先写失败测试，再做最小实现。未获得用户明确提交/发运授权时，计划中的 commit 仅为审查点，不执行。

## Current Baseline and File Map

| 文件 | 当前职责 | 本计划动作 |
|---|---|---|
| `repo/services/auth-service/internal/service/auth_service.go` | `ValidateToken` 把 `ani_*` 交给现有 AK store | 只读验证，不修改 |
| `repo/services/envoy-authz-adapter/internal/extauth/server.go` | Bearer 提取、Auth RPC、tenant 比较、HTTP 映射和 header removal | 只在新增失败测试暴露缺口时做最小修正 |
| `repo/services/envoy-authz-adapter/internal/extauth/server_test.go` | ext_authz 决策单测 | 增加非 Authorization 凭据位置负例 |
| `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml` | 八个 C40 静态资源 | 原则上不改；只在新契约测试证明偏离方案时修改 |
| `repo/scripts/validate_inference_envoy_ai_gateway_manifest.py` | 静态 manifest 严格验证 | 锁定路由级鉴权、无公网 probe Route、gRPC probe |
| `repo/scripts/validate_inference_envoy_ai_gateway_manifest_test.py` | manifest 变异测试 | 增加公网 probe/path 边界变异 |
| `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml` | C40 live gate 契约 | 增加凭据位置、公网路径和 workload probe 检查 |
| `repo/scripts/validate_inference_envoy_ai_gateway_live_gate.py` | gate/evidence 校验 | 扩展必需 check ID 和 2026-08-25 evidence 路径 |
| `repo/scripts/validate_inference_envoy_ai_gateway_live_gate_test.py` | runner/gate 无副作用单测 | 为新 helper、顺序、脱敏和 probe 判定先写失败测试 |
| `repo/scripts/run_inference_envoy_ai_gateway_live.py` | 创建/撤销临时 AK、调用数据面、回滚并原子写 evidence | 扩展安全通用请求、公网路径边界和 probe 检查 |
| `repo/development-records/live-evidence/inference-envoy-ai-gateway-live-20260825.json` | 脱敏真实证据 | live 通过后由 runner 原子创建 |
| `repo/development-records/INFERENCE-SERVICE-ENVOY-AI-GATEWAY-C40.md` | Feature batch 记录 | live 通过后创建 |
| `repo/development-records/README.md` / `repo/CURRENT-SPRINT.md` / `ANI-06-开发计划.md` | 完成索引与当前进度 | 仅在 live 通过后更新 |

---

### Task 1: Verify and freeze the existing Auth/adapter baseline

**Files:**
- Read: `repo/services/auth-service/internal/service/auth_service.go:131`
- Read: `repo/services/auth-service/internal/service/api_keys.go`
- Test: `repo/services/envoy-authz-adapter/internal/extauth/server_test.go`
- Test: `repo/services/envoy-authz-adapter/internal/authclient/client_test.go`

**Interfaces:**
- Consumes `auth.v1.AuthService/ValidateToken(ValidateTokenRequest) -> common.v1.TenantContext`.
- Produces a verified baseline in which only Auth owns AK existence, expiration, revocation, tenant and RPM semantics.

- [ ] **Step 1: Confirm the Auth branch and adapter boundary without editing production code**

Check that `ValidateToken` still dispatches `ani_*` through `s.apiKeys.validate`, maps `errAPIKeyRateLimitExceeded` to `codes.ResourceExhausted`, and returns `TenantContext.TenantId`. Check that adapter imports no database, Redis or NATS client.

Run:

```bash
cd /root/kubercon/ANI
rg -n "isAPIKey|apiKeys.validate|errAPIKeyRateLimitExceeded|ResourceExhausted" repo/services/auth-service/internal/service
rg -n "database/sql|pgx|redis|nats" repo/services/envoy-authz-adapter --glob '*.go'
```

Expected: the first command identifies the existing Auth path; the second prints no matches.

- [ ] **Step 2: Run the current adapter baseline**

```bash
cd /root/kubercon/ANI/repo/services/envoy-authz-adapter
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go test ./... -count=1 -v
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go test -race ./...
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go vet ./...
```

Expected: all commands pass. If any fail, stop and diagnose that baseline failure before changing C40 scope.

- [ ] **Step 3: Record a no-change review checkpoint**

Run `git status --short` and confirm this task produced no file changes. There is no commit for this audit task.

### Task 2: Lock the accepted credential and public-path boundary with tests

**Files:**
- Modify: `repo/services/envoy-authz-adapter/internal/extauth/server_test.go`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_manifest.py`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_manifest_test.py`

**Interfaces:**
- Consumes the existing `extauth.Server.Check` and static eight-resource manifest.
- Produces explicit regression coverage proving only Authorization Bearer is parsed and no public health HTTPRoute can enter the C40 manifest.

- [ ] **Step 1: Add non-Authorization credential characterization tests**

Add a table test which supplies no `authorization` header but places a syntactically valid-looking AK in `x-api-key`, `cookie`, or `:path`:

```go
func TestCheckIgnoresCredentialsOutsideAuthorization(t *testing.T) {
	for name, headers := range map[string]map[string]string{
		"x-api-key": {"x-api-key": "ani_dev_not_a_real_key"},
		"cookie":    {"cookie": "api_key=ani_dev_not_a_real_key"},
		"query":     {":path": "/v1/chat/completions?api_key=ani_dev_not_a_real_key"},
	} {
		t.Run(name, func(t *testing.T) {
			validator := &fakeValidator{}
			request := checkRequest("", "tenant-a", "service-a")
			request.Attributes.Request.Http.Headers = headers
			response, err := New(validator).Check(context.Background(), request)
			if err != nil { t.Fatal(err) }
			if got := response.GetDeniedResponse().GetStatus().GetCode(); got != typev3.StatusCode(http.StatusUnauthorized) {
				t.Fatalf("status = %v, want 401", got)
			}
			if validator.calls != 0 { t.Fatalf("validator calls = %d, want 0", validator.calls) }
		})
	}
}
```

Run:

```bash
cd /root/kubercon/ANI/repo/services/envoy-authz-adapter
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go test ./internal/extauth -run TestCheckIgnoresCredentialsOutsideAuthorization -count=1 -v
```

Expected: the test passes against the already implemented extractor and makes the accepted credential location explicit. If it fails, preserve the failure as RED and continue to Step 2.

- [ ] **Step 2: Make only the minimal adapter correction if RED exposed one**

The accepted extractor remains exactly:

```go
func bearerAPIKey(headers map[string]string) (string, bool) {
	raw := strings.TrimSpace(headers["authorization"])
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	return token, strings.HasPrefix(token, "ani_")
}
```

Do not add parsers for `x-api-key`, Cookie, URL or body. Rerun the focused test and expect PASS.

- [ ] **Step 3: Add manifest boundary mutation tests**

Add these mutations to `validate_inference_envoy_ai_gateway_manifest_test.py`:

```python
def test_rejects_public_health_route_or_path_match(self) -> None:
    documents = self.documents()
    documents.append({
        "apiVersion": "gateway.networking.k8s.io/v1",
        "kind": "HTTPRoute",
        "metadata": {"name": "public-health", "namespace": manifest.NAMESPACE},
        "spec": {"parentRefs": [{"name": "ani-aigw"}], "rules": []},
    })
    self.assert_rejected(documents)

    documents = self.documents()
    route = manifest.by_kind_name(documents, "AIGatewayRoute", "ani-c40")
    route["spec"]["rules"][0]["matches"][0]["path"] = {
        "type": "PathPrefix", "value": "/healthz"
    }
    self.assert_rejected(documents)

def test_rejects_gateway_wide_security_policy(self) -> None:
    documents = self.documents()
    policy = manifest.by_kind_name(documents, "SecurityPolicy", "ani-c40-ext-auth")
    policy["spec"]["targetRefs"] = [{
        "group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "ani-aigw"
    }]
    self.assert_rejected(documents)
```

Run `cd /root/kubercon/ANI/repo && python3 scripts/validate_inference_envoy_ai_gateway_manifest_test.py`.

Expected: tests pass only when the validator retains exactly eight managed resources, an exact model-only AIGatewayRoute match, and route-level `HTTPRoute/ani-c40` SecurityPolicy targeting. If an assertion is not rejected, strengthen `validate_data_plane` or `validate_security_policy` with an exact comparison; do not add a health Route.

- [ ] **Step 4: Run focused local GREEN**

```bash
cd /root/kubercon/ANI/repo
python3 scripts/validate_inference_envoy_ai_gateway_manifest_test.py
python3 scripts/validate_inference_envoy_ai_gateway_manifest.py
cd services/envoy-authz-adapter
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go test ./internal/extauth -count=1 -v
```

Expected: all pass; `git diff --check` is clean.

- [ ] **Step 5: Review checkpoint**

Stage only the three test/validator files and `server.go` only if the new test proved a real production gap. Suggested commit after explicit authorization: `test(authz): lock Envoy AK credential boundary`.

### Task 3: Extend the import-safe live gate for path, credential, and probe guarantees

**Files:**
- Modify: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_live_gate.py`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_live_gate_test.py`
- Modify: `repo/scripts/run_inference_envoy_ai_gateway_live.py`

**Interfaces:**
- Produces `gateway_request(path, *, method, body, headers, query, timeout) -> tuple[int, object]` that never prints response bodies or credentials.
- Produces `has_container_probes(deployment, required_names) -> bool`.
- Adds required evidence checks `credential-location-boundary`, `public-path-boundary`, and `workload-probes-ready`.

- [ ] **Step 1: Write failing contract and lifecycle tests**

Extend the test expectations first:

```python
def test_required_checks_include_confirmed_c40_boundaries(self) -> None:
    self.assertTrue({
        "credential-location-boundary",
        "public-path-boundary",
        "workload-probes-ready",
    } <= gate.REQUIRED_CHECKS)

def test_runner_lifecycle_includes_confirmed_c40_boundaries(self) -> None:
    plan = runner.lifecycle_plan()
    self.assertLess(plan.index("missing-and-malformed"), plan.index("credential-location-boundary"))
    self.assertLess(plan.index("credential-location-boundary"), plan.index("public-path-boundary"))
    self.assertLess(plan.index("public-path-boundary"), plan.index("expired-key"))
```

Run `cd /root/kubercon/ANI/repo && python3 scripts/validate_inference_envoy_ai_gateway_live_gate_test.py`.

Expected RED: the three IDs and lifecycle stages do not yet exist.

- [ ] **Step 2: Add failing helper tests before changing the runner**

Use mocked `urllib.request.urlopen` and Kubernetes objects:

```python
def test_gateway_request_never_promotes_alternate_credentials(self) -> None:
    class Unauthorized:
        code = 401
        def read(self) -> bytes: return b"discarded"
    with mock.patch.dict(os.environ, {"ANI_C40_GATEWAY_URL": "https://invoke.example"}, clear=False):
        with mock.patch.object(runner.urllib.request, "urlopen", side_effect=runner.urllib.error.HTTPError(
            "https://invoke.example/v1/chat/completions", 401, "unauthorized", {}, Unauthorized()
        )) as urlopen:
            status, _ = runner.gateway_request(
                "/v1/chat/completions",
                method="POST",
                body={"model": runner.PUBLIC_MODEL_ID},
                headers={"x-api-key": "ani_dev_not_a_real_key"},
            )
    self.assertEqual(status, 401)
    request = urlopen.call_args.args[0]
    self.assertNotIn("Authorization", request.headers)

def test_workload_probe_predicate_requires_both_probes_on_every_named_container(self) -> None:
    deployment = {"spec": {"template": {"spec": {"containers": [
        {"name": "envoy", "readinessProbe": {"httpGet": {}}, "livenessProbe": {"httpGet": {}}},
        {"name": "shutdown-manager", "readinessProbe": {"httpGet": {}}, "livenessProbe": {"httpGet": {}}},
    ]}}}}
    self.assertTrue(runner.has_container_probes(deployment, {"envoy", "shutdown-manager"}))
    del deployment["spec"]["template"]["spec"]["containers"][0]["livenessProbe"]
    self.assertFalse(runner.has_container_probes(deployment, {"envoy", "shutdown-manager"}))
```

Expected RED: `gateway_request` and `has_container_probes` do not exist.

- [ ] **Step 3: Implement the safe generic data-plane helper and keep `chat` thin**

Implement this shape; HTTP error bodies are always discarded:

```python
def gateway_request(
    path: str,
    *,
    method: str = "GET",
    body: dict[str, Any] | None = None,
    headers: dict[str, str] | None = None,
    query: dict[str, str] | None = None,
    timeout: int = 90,
) -> tuple[int, object]:
    base = required_env("ANI_C40_GATEWAY_URL").rstrip("/")
    encoded_query = urllib.parse.urlencode(query or {})
    url = base + path + (("?" + encoded_query) if encoded_query else "")
    payload = json.dumps(body).encode("utf-8") if body is not None else None
    request_headers = dict(headers or {})
    if payload is not None:
        request_headers.setdefault("Content-Type", "application/json")
    request = urllib.request.Request(url, data=payload, headers=request_headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read(1_000_000).decode("utf-8", errors="replace")
            return response.status, raw
    except urllib.error.HTTPError as err:
        err.read()
        return err.code, {}
    except urllib.error.URLError:
        fail("data-plane request could not be completed")
        return 0, {}
```

Refactor `chat` to build the existing body, set `Authorization` only when `api_key is not None`, call `gateway_request`, and preserve existing JSON/SSE decoding. Do not log the URL, headers, request body or response body.

- [ ] **Step 4: Implement live boundary helpers**

Use a real owner key only in headers, never in a query string:

```python
def verify_credential_location_boundary(owner_key: str) -> None:
    body = {"model": PUBLIC_MODEL_ID, "messages": [{"role": "user", "content": temporary_name("credential-boundary")}], "stream": False, "max_tokens": 8}
    for headers in (
        {"Content-Type": "application/json", "x-api-key": owner_key},
        {"Content-Type": "application/json", "Cookie": "api_key=" + owner_key},
    ):
        status, _ = gateway_request("/v1/chat/completions", method="POST", body=body, headers=headers)
        require_status(status, 401, "alternate credential location")
    status, _ = gateway_request(
        "/v1/chat/completions",
        method="POST",
        body=body,
        headers={"Content-Type": "application/json"},
        query={"api_key": "not-a-credential"},
    )
    require_status(status, 401, "query credential location")

def verify_public_path_boundary() -> list[str]:
    markers: list[str] = []
    for path in ("/healthz", "/readyz", "/v1/not-registered"):
        marker = temporary_name("unregistered")
        markers.append(marker)
        status, _ = gateway_request(path, method="POST", body={"marker": marker})
        require_status(status, 404, "public unregistered path")
    return markers

def has_container_probes(deployment: dict[str, Any], required_names: set[str]) -> bool:
    containers = (((deployment.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or []
    found = {
        item.get("name")
        for item in containers
        if isinstance(item, dict) and item.get("readinessProbe") and item.get("livenessProbe")
    }
    return required_names <= found
```

`verify_workload_probes` must read `Deployment/envoy-authz-adapter` in `ani-aigw` and the Deployment selected by `gateway.envoyproxy.io/owning-gateway-name=ani-aigw` in `envoy-gateway-system`; require adapter container `{envoy-authz-adapter}` and Envoy containers `{envoy, shutdown-manager}` to have both probes. It must not mutate either Deployment.

After the public-path calls, scan the exact retained vLLM log targets in memory and fail if any returned marker occurs.

- [ ] **Step 5: Extend the gate and evidence contract**

Add to `REQUIRED_CHECKS`:

```python
"credential-location-boundary",
"public-path-boundary",
"workload-probes-ready",
```

Add the same IDs to `inference-envoy-ai-gateway-live-gate.yaml` with these exact meanings:

```yaml
- id: credential-location-boundary
  command: try x-api-key, Cookie, and a non-secret query sentinel without Authorization
  pass_condition: alternate_locations_do_not_authorize
- id: public-path-boundary
  command: request /healthz, /readyz, and /v1/not-registered through the public Gateway
  pass_condition: all_http_404_and_markers_absent_from_vllm
- id: workload-probes-ready
  command: inspect adapter and owning Envoy Deployments
  pass_condition: every_required_container_has_readiness_and_liveness
```

Insert the runner stages after missing/malformed AK and before expiry. Record only passed check IDs; do not add paths, header values, markers, Kubernetes JSON or AK material to evidence.

- [ ] **Step 6: Move the evidence target to the actual execution date**

Change both `DEFAULT_EVIDENCE` and runner `EVIDENCE` to:

```text
development-records/live-evidence/inference-envoy-ai-gateway-live-20260825.json
```

Update the gate YAML `evidence` field to the same path. Add a unit assertion that all three locations agree, so the runner cannot write a different file from the validator contract.

- [ ] **Step 7: Run local GREEN and safety checks**

```bash
cd /root/kubercon/ANI/repo
python3 scripts/validate_inference_envoy_ai_gateway_live_gate_test.py
python3 scripts/validate_inference_envoy_ai_gateway_live_gate.py
python3 -m py_compile \
  scripts/validate_inference_envoy_ai_gateway_live_gate.py \
  scripts/validate_inference_envoy_ai_gateway_live_gate_test.py \
  scripts/run_inference_envoy_ai_gateway_live.py
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-manifest
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-live-gate
git diff --check
```

Expected: manifest mutation suite, expanded live-gate suite, validators and compile checks pass; gate remains `status: contract`; no HTTP, AK, image or cluster mutation occurred.

- [ ] **Step 8: Review checkpoint**

Review that unit tests mock every HTTP/kubectl path and that importing the runner has no side effects. Suggested commit after explicit authorization: `test(inference): tighten C40 invocation boundaries`.

### Task 4: Verify the deployable artifact and schema without claiming live

**Files:**
- Verify: `repo/services/envoy-authz-adapter/Dockerfile`
- Verify: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml`
- Verify: `repo/Makefile`

**Interfaces:**
- Consumes image tag `docker.changqingyun.cn/ani/envoy-authz-adapter:c40-20260824`.
- Produces build/runtime-user evidence and installed-CRD server dry-run evidence, but no live readiness claim.

- [ ] **Step 1: Run complete adapter and local C40 verification**

```bash
cd /root/kubercon/ANI/repo/services/envoy-authz-adapter
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go test ./... -count=1 -v
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go test -race ./...
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go vet ./...
cd ../..
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-manifest
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-live-gate
```

Expected: all pass.

- [ ] **Step 2: Run the installed-CRD server-side dry run**

```bash
kubectl apply --server-side --dry-run=server \
  -f /root/kubercon/ANI/repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml
```

Expected: all eight resources report server dry-run acceptance; `kubectl get` confirms the dry run created nothing new.

- [ ] **Step 3: Inspect the existing exact image when production code is unchanged**

```bash
docker inspect docker.changqingyun.cn/ani/envoy-authz-adapter:c40-20260824 \
  --format '{{.Config.User}}'
```

Expected: the existing image is present locally and runtime user is `65532:65532` or `65532`. If the image is not local, inspect the deployed Pod image ID and registry manifest read-only; do not push.

- [ ] **Step 4: Build a new immutable tag only if Task 2 changed adapter production code**

```bash
cd /root/kubercon/ANI/repo
make image-envoy-authz-adapter \
  REGISTRY=docker.changqingyun.cn/ani \
  VERSION=c40-20260825
docker inspect docker.changqingyun.cn/ani/envoy-authz-adapter:c40-20260825 \
  --format '{{.Config.User}}'
docker push docker.changqingyun.cn/ani/envoy-authz-adapter:c40-20260825
docker image inspect docker.changqingyun.cn/ani/envoy-authz-adapter:c40-20260825 \
  --format '{{index .RepoDigests 0}}'
```

This step is skipped completely when adapter production code is unchanged. If it runs, update only the adapter Deployment image in `inference-envoy-ai-gateway-c40.yaml` to `c40-20260825`, rerun the manifest validator and server dry-run, then require the new Pod image ID after rollout. Never overwrite or repush `c40-20260824`; never write registry credentials or Docker config to evidence.

- [ ] **Step 5: Review checkpoint**

This task creates no source changes. Record command results for the eventual development record; do not mark C40 live.

### Task 5: Execute the bounded real-cluster gate and write redacted evidence

**Files:**
- Create: `repo/development-records/live-evidence/inference-envoy-ai-gateway-live-20260825.json`
- Modify: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml`

**Interfaces:**
- Consumes local credentials only through `KUBECONFIG`, `ANI_C40_CONTROL_PLANE_URL`, `ANI_C40_GATEWAY_URL`, `ANI_C40_OWNER_ACCESS_TOKEN`, and `ANI_C40_FOREIGN_ACCESS_TOKEN`.
- Produces pre-cutover `status: passed` redacted evidence while the gate remains `contract`; the final `live` promotion waits for Task 6 to remove the legacy Secret path and rerun every check.

- [ ] **Step 1: Preflight without printing credentials**

Confirm each variable is set using only presence/length checks. Do not run `env`, `set`, `printenv`, shell tracing or commands which echo values. Confirm `ANI_C40_CONTROL_PLANE_URL` ends in `/api/v1` and `ANI_C40_GATEWAY_URL` is an absolute HTTP(S) base.

- [ ] **Step 2: Execute the import-safe runner**

```bash
cd /root/kubercon/ANI/repo
PATH=/tmp/ani-pybin:$PATH make run-inference-envoy-ai-gateway-live
```

Expected sequence:

```text
apply and wait current generation
owner non-stream + SSE
missing/malformed credentials
alternate credential locations rejected
public probes/unregistered path return 404
workload probes present
expiration + revocation + single-AK RPM
foreign tenant hidden with 404
adapter down + Auth unreachable fail closed with 503
credential/header/backend/log/Secret scans
control-plane regressions
all temporary keys revoked and adapter state restored
atomic mode-0600 evidence replacement
```

If any step fails, do not change gate status. Verify cleanup and repair only the failed in-scope seam before rerunning the whole gate.

- [ ] **Step 3: Independently inspect cleanup and redaction**

Call `control_request("GET", "/auth/api-keys", owner_access)` and the same operation with `foreign_access`; require HTTP 200, `items` to be a list, and no item satisfying `str(item.get("name", "")).startswith("c40-") and item.get("is_active") is True`. Perform the predicate in memory and print only a final boolean/count. Check adapter desired/available replicas and `AUTH_SERVICE_GRPC_ADDR` against the manifest baseline. Run the evidence validator; never print API key response objects, key prefixes, Secret objects or raw logs.

- [ ] **Step 4: Validate candidate evidence without promoting the gate**

Keep `status: contract`. Validate the exact runner output explicitly:

```bash
cd /root/kubercon/ANI/repo
python3 -c 'from pathlib import Path; from scripts.validate_inference_envoy_ai_gateway_live_gate import validate_evidence; validate_evidence(Path("development-records/live-evidence/inference-envoy-ai-gateway-live-20260825.json"))'
python3 scripts/validate_inference_envoy_ai_gateway_live_gate.py
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-live-gate
git diff --check
```

Expected: candidate evidence and both contract validators pass; evidence contains only readiness booleans plus passed check IDs; gate remains `contract` pending the separately approved legacy Secret removal.

- [ ] **Step 5: Review checkpoint**

Inspect the staged evidence as text before any commit. Suggested commit after explicit authorization: `test(inference): prove C40 Envoy system AK live path`.

### Task 6: Retire the superseded Secret authentication path after proof

**Files:**
- Modify: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_live_gate.py`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_live_gate_test.py`
- Modify: `repo/scripts/run_inference_envoy_ai_gateway_live.py`
- Modify: `repo/development-records/live-evidence/inference-envoy-ai-gateway-live-20260825.json`

**Interfaces:**
- Deletes only `SecurityPolicy/ani-aigw-apikey` and `Secret/ani-aigw-apikey` in namespace `ani-aigw` after C40 live evidence passes.
- Produces required check `legacy-secret-auth-removed` without serializing Secret contents.

- [ ] **Step 1: Write the failing post-cutover evidence contract first**

Add `legacy-secret-auth-removed` to `REQUIRED_CHECKS`, the gate YAML and the evidence test fixture. Add a validator test that removes the check and expects rejection. Keep the gate at `contract` during test development if deletion has not happened.

Add this import-safe predicate and its mocked unit test before cluster deletion:

```python
def legacy_secret_auth_removed() -> bool:
    policy = kubectl([
        "-n", ADAPTER_NAMESPACE, "get", "securitypolicy", "ani-aigw-apikey",
        "--ignore-not-found", "-o", "name",
    ])
    secret = kubectl([
        "-n", ADAPTER_NAMESPACE, "get", "secret", "ani-aigw-apikey",
        "--ignore-not-found", "-o", "name",
    ])
    return not policy.strip() and not secret.strip()
```

The test must mock both `kubectl` results, prove two empty results return `True`, and prove either non-empty object name returns `False`. Do not use `kubectl_json` because a missing named object is an expected state.

- [ ] **Step 2: Resolve the exact legacy targets read-only**

```bash
kubectl -n ani-aigw get securitypolicy ani-aigw-apikey -o name
kubectl -n ani-aigw get secret ani-aigw-apikey -o name
kubectl -n ani-aigw get securitypolicy ani-c40-ext-auth -o name
```

Require the new C40 gate to be live/passed before proceeding. If either legacy object name differs, stop; do not broaden deletion with labels or globs.

- [ ] **Step 3: Obtain a separate destructive-action confirmation**

Present the two exact names and explain that deleting the Secret is not recoverable from this repository because its contents are intentionally not stored. Do not treat prior approval for live deployment as deletion approval.

- [ ] **Step 4: Delete only the confirmed legacy objects**

After approval:

```bash
kubectl -n ani-aigw delete securitypolicy ani-aigw-apikey
kubectl -n ani-aigw delete secret ani-aigw-apikey
```

Do not delete Gateway, GatewayClass, EnvoyProxy, Backend, AIServiceBackend, AIGatewayRoute, HTTPRoute, adapter, vLLM workload or tenant namespace.

- [ ] **Step 5: Rerun the full live gate after cutover**

At the end of `run_live`, before comparing `set(checks)` with `REQUIRED_CHECKS`, require:

```python
if not legacy_secret_auth_removed():
    fail("legacy Secret authentication objects still exist")
checks["legacy-secret-auth-removed"] = {"status": "passed"}
```

```bash
cd /root/kubercon/ANI/repo
PATH=/tmp/ani-pybin:$PATH make run-inference-envoy-ai-gateway-live
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-live-gate
```

Expected: every C40 check still passes; evidence contains `legacy-secret-auth-removed` only as a passed check ID and contains no Secret data.

- [ ] **Step 6: Promote the gate to live and validate**

Change only the gate status from `contract` to `live`, then run:

```bash
cd /root/kubercon/ANI/repo
python3 scripts/validate_inference_envoy_ai_gateway_live_gate.py
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-live-gate
git diff --check
```

Expected: the validator loads the final post-cutover evidence, requires every passed check including `legacy-secret-auth-removed`, and reports C40 live valid.

- [ ] **Step 7: Review checkpoint**

Confirm `kubectl get ... --ignore-not-found` returns no legacy objects and the C40 SecurityPolicy remains Accepted. Suggested commit after explicit authorization: `chore(inference): retire legacy Envoy API key secret`.

### Task 7: Close the Feature batch and run repository gates

**Files:**
- Create: `repo/development-records/INFERENCE-SERVICE-ENVOY-AI-GATEWAY-C40.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`
- Verify: all C40 source, manifest, validator, gate and evidence files

**Interfaces:**
- Produces one bounded product claim: static single-service Envoy invocation with existing ANI system AK is live-passed.
- Explicitly keeps dynamic publication, `invocation_url`, aggregate limiting, metering, billing and full runtime readiness false/out of scope.

- [ ] **Step 1: Write the Feature batch record from actual evidence only**

Use this exact conclusion block:

```text
Batch: INFERENCE-SERVICE-ENVOY-AI-GATEWAY-C40
Result: static single-service Envoy invocation live passed
Data plane: Envoy AI Gateway -> vLLM ClusterIP; ANI Gateway is control plane only
Auth: envoy-authz-adapter -> existing AuthService/ValidateToken
Credential: Authorization: Bearer ani_* only
Authorization: valid key + trusted route tenant equality; inference scope deferred
Rate limit: existing single-AK RPM only
Public path boundary: /healthz, /readyz and unregistered paths are 404
Metering/billing/service concurrency/tenant aggregate RPM: not implemented
Dynamic lifecycle publication and invocation_url: not implemented (C41)
Full inference runtime/full platform production ready: not claimed
```

List only commands actually executed and the redacted evidence path. Do not copy credentials, raw logs, Secrets, IPs or local credential-file locations.

- [ ] **Step 2: Update the three required progress indexes**

Add the same bounded claim to `repo/development-records/README.md`, `repo/CURRENT-SPRINT.md`, and ANI-06 Section zero/current inference sequence. Do not update `CLAUDE.md` or `ANI-DOCS-INDEX.md` because C40 does not switch the global Sprint.

- [ ] **Step 3: Run focused C40 verification**

```bash
cd /root/kubercon/ANI/repo/services/envoy-authz-adapter
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go test ./... -count=1 -v
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go test -race ./...
GOWORK=off GOCACHE=/tmp/ani-c40-go-cache go vet ./...
cd ../..
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-manifest
PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-inference-control-plane
git diff --check
```

Expected: all pass.

- [ ] **Step 4: Run ANI Feature-batch repository gates**

```bash
cd /root/kubercon/ANI/repo
PATH=/tmp/ani-pybin:$PATH make validate-services
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make test
git diff --check
```

Expected: all pass. Review generated-output drift after `make validate-services`; do not claim a clean tree if pre-existing unrelated changes remain.

- [ ] **Step 5: Perform completion review**

Invoke `requesting-code-review`, fix only C40 findings, then invoke `verification-before-completion` and rerun any affected focused commands. Confirm:

```text
No Auth proto/OpenAPI change
No AK/Secret material in tracked files or staged diff
Only route-level SecurityPolicy
No public health Route
All temporary AKs revoked
Adapter state restored
Legacy Secret authentication removed only with separate approval
C41/metering/billing claims remain false
```

- [ ] **Step 6: Ship checkpoint**

Do not commit, push or create a PR until the user explicitly approves `/ship-it`. When approved, follow ANI main-only rules, fetch `upstream/main`, stage explicit C40 paths only, run the required clean-HEAD/CI sequence, and use a Conventional Commit such as `feat(inference): close Envoy system AK C40 path`.
