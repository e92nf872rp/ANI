# Envoy 推理限流 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Envoy AI Gateway 的真实推理数据面为 C40 chat/embeddings 路由增加共享全局保护限流，并验证 auth-service 的 AK RPM 限流通过 ext_authz 正确返回 429/503。

**Architecture:** Envoy AI Gateway 继续作为唯一推理入口。C40 Gateway 通过 `BackendTrafficPolicy` 使用 Envoy Gateway Global RateLimit；Redis 由 Envoy Gateway 的 rate-limit backend 使用。envoy-authz-adapter 只负责 ext_authz 协议转换，auth-service 继续负责 `ani_*` AK 校验和每 AK RPM 限流；ANI Gateway `/v1` 不参与此链路。

**Tech Stack:** Envoy Gateway v1.8 `BackendTrafficPolicy`, Envoy AI Gateway `AIGatewayRoute`, Kubernetes YAML, Python contract validators, Go ext_authz tests, Redis-backed Envoy RateLimit Service.

## Global Constraints

- 不修改或注册 ANI Gateway `/v1/chat/completions` 反向代理；该路径不属于 Envoy AI Gateway 数据面。
- 不修改 Services OpenAPI；本轮不启用 `/inference-policies` 动态策略 CRUD。
- 只有 `Authorization: Bearer ani_*` 可作为 C40 推理凭据；不把 AK 明文写入 Secret、日志、CRD 或 rate-limit key。
- Global RateLimit 必须使用 Redis 共享后端；Local RateLimit 不作为跨副本业务限流。
- C40 路由和 adapter 失败默认 fail-closed；429/503 错误不得泄露凭据、内部地址或 SQL 细节。
- 修改前和每次写操作前确认当前分支为 `main`；提交前执行 `git fetch upstream main`、相关测试、`make validate-architecture` 和 `git diff --check`。

---

### Task 1: 固定 C40 Envoy Global RateLimit 资源契约

**Files:**
- Modify: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_manifest.py`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_manifest_test.py`
- Modify: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml`

**Interfaces:**
- Consumes: existing `AIGatewayRoute` named `ani-c40` in namespace `ani-aigw`.
- Produces: one `gateway.envoyproxy.io/v1alpha1` `BackendTrafficPolicy` targeting Gateway/`ani-aigw`, with explicit global limits inherited by the registered chat and embeddings routes.

- [ ] **Step 1: Write the failing manifest tests**

Add assertions that the manifest contains exactly one `BackendTrafficPolicy` with:

```python
{
    "apiVersion": "gateway.envoyproxy.io/v1alpha1",
    "kind": "BackendTrafficPolicy",
    "metadata": {"name": "ani-c40-ratelimit", "namespace": "ani-aigw"},
    "spec": {
        "targetRefs": [{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "ani-aigw"}],
        "rateLimit": {
            "global": {
                "rules": [{"limit": {"requests": 600, "unit": "Minute"}, "shared": True}]
            }
        },
    },
}
```

The validator test must fail before the resource and must reject a policy targeting a different Gateway, a local-only policy, or a missing `shared: true` global rule.

- [ ] **Step 2: Run the focused test to verify RED**

Run:

```bash
cd repo
python3 scripts/validate_inference_envoy_ai_gateway_manifest_test.py
```

Expected: failure because the C40 manifest has no `BackendTrafficPolicy`.

- [ ] **Step 3: Add the resource and validator rules**

Append the policy after `SecurityPolicy` in the C40 manifest. Extend the validator to require the exact target Gateway, global rule, `requests: 600`, `unit: Minute`, and `shared: true`. Add the same resource to the live-gate expected resource set so the gate proves it exists.

- [ ] **Step 4: Run focused tests to verify GREEN**

Run:

```bash
cd repo
python3 scripts/validate_inference_envoy_ai_gateway_manifest_test.py
python3 scripts/validate_inference_envoy_ai_gateway_manifest.py
```

Expected: all validator tests pass and the standalone validator prints `inference envoy ai gateway manifest valid`.

- [ ] **Step 5: Commit**

```bash
git add deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml \
  scripts/validate_inference_envoy_ai_gateway_manifest.py \
  scripts/validate_inference_envoy_ai_gateway_manifest_test.py \
  deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml
git commit -m "feat(envoy): add C40 inference global rate limit policy"
```

### Task 2: Define Envoy Gateway Redis backend prerequisite

**Files:**
- Create: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-ratelimit-config.yaml`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_ratelimit_config.py`
- Create: `repo/scripts/validate_inference_envoy_ai_gateway_ratelimit_config_test.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Consumes: an existing Redis Service endpoint supplied through a Secret reference in `envoy-gateway-system`.
- Produces: a validated `ConfigMap` fragment for `EnvoyGateway.spec.rateLimit.backend.type=Redis` and a make target that checks it without applying it.

- [ ] **Step 1: Write failing config tests**

Test that the config requires `apiVersion: v1`, `kind: ConfigMap`, namespace `envoy-gateway-system`, an `envoy-gateway.yaml` document with `rateLimit.backend.type: Redis`, and `redis.urlRef.secretKeyRef` (not a plaintext URL). Test rejection of plaintext `url`, wrong namespace, and non-Redis backends.

- [ ] **Step 2: Run RED**

```bash
cd repo
python3 scripts/validate_inference_envoy_ai_gateway_ratelimit_config_test.py
```

Expected: `ModuleNotFoundError` or missing-config failure before the files exist.

- [ ] **Step 3: Implement the config and validator**

Use a Secret reference named `ani-envoy-ratelimit-redis` with key `REDIS_ENDPOINT`; do not include endpoint credentials or values in the ConfigMap. The validator must parse the embedded YAML and reject any literal AK/token-like values.

- [ ] **Step 4: Add and run the Make target**

Add:

```make
validate-inference-envoy-ai-gateway-ratelimit-config:
	python3 scripts/validate_inference_envoy_ai_gateway_ratelimit_config.py
```

Run the focused tests, standalone validator, and Make target; expected result is GREEN.

- [ ] **Step 5: Commit**

```bash
git add deploy/real-k8s-lab/inference-envoy-ai-gateway-ratelimit-config.yaml \
  scripts/validate_inference_envoy_ai_gateway_ratelimit_config.py \
  scripts/validate_inference_envoy_ai_gateway_ratelimit_config_test.py Makefile
git commit -m "feat(envoy): validate shared rate limit redis backend"
```

### Task 3: Preserve ext_authz AK rate-limit error mapping

**Files:**
- Modify: `repo/services/envoy-authz-adapter/internal/extauth/server_test.go`
- Modify: `repo/services/envoy-authz-adapter/internal/extauth/server.go` only if a test exposes a mapping gap
- Modify: `repo/services/ani-gateway/internal/middleware/auth_client_test.go` only if existing mapping lacks coverage

**Interfaces:**
- Consumes: auth-service gRPC `codes.ResourceExhausted` and `codes.Unavailable`.
- Produces: Envoy ext_authz denied responses with HTTP 429 for AK RPM exhaustion and HTTP 503 for dependency failure, with redacted bodies.

- [ ] **Step 1: Add failing regression tests**

Cover `ResourceExhausted` → 429, `Unavailable`/`DeadlineExceeded` → 503, and assert response bodies do not contain the upstream error text, raw Authorization header, or API-key material.

- [ ] **Step 2: Run RED and inspect only the failing mapping**

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./services/envoy-authz-adapter/internal/extauth -run 'Rate|Unavailable|Redact' -v
```

Expected: any failure identifies the missing mapping or redaction only; do not change unrelated auth behavior.

- [ ] **Step 3: Implement the minimum mapping fix**

Keep the adapter stateless and continue calling `AuthService/ValidateToken`; never add a database or Redis client to the adapter. Map only the tested gRPC status classes and use fixed public messages.

- [ ] **Step 4: Run GREEN and full adapter tests**

```bash
cd repo/services/envoy-authz-adapter
GOCACHE=/tmp/ani-go-cache go test ./...
```

Expected: adapter tests pass with no masked failures.

- [ ] **Step 5: Commit**

```bash
git add services/envoy-authz-adapter/internal/extauth services/ani-gateway/internal/middleware
git commit -m "fix(authz): preserve Envoy rate-limit error semantics"
```

### Task 4: Extend C40 live-gate assertions for Envoy rate limiting

**Files:**
- Modify: `repo/scripts/run_inference_envoy_ai_gateway_live.py`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_live_gate_test.py`
- Modify: `repo/scripts/validate_inference_envoy_ai_gateway_live_gate.py`
- Modify: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml`

**Interfaces:**
- Consumes: Envoy Gateway external address, valid owner AK, foreign AK, and the C40 `ani-c40` route.
- Produces: live evidence showing chat and embeddings pass, repeated requests hit 429 at the configured route limit, and adapter/auth failures remain fail-closed.

- [ ] **Step 1: Add mocked live-gate tests**

Add tests for: rate-limit resource status, no vLLM request after a 429, 429 response classification, owner/foreign tenant behavior, and redacted evidence. All tests must mock HTTP/kubectl; no live command in unit tests.

- [ ] **Step 2: Run RED**

```bash
cd repo
python3 scripts/validate_inference_envoy_ai_gateway_live_gate_test.py
```

Expected: focused failures for missing rate-limit resource/evidence assertions.

- [ ] **Step 3: Implement live-gate checks**

Have the runner read only status and response metadata. Send requests through Envoy’s external address with `Authorization: Bearer ani_*`; never call the vLLM ClusterIP directly. Record only status, route/model labels, timing, and redacted reason codes. Do not persist the AK in evidence.

- [ ] **Step 4: Run local GREEN and contract checks**

```bash
cd repo
python3 scripts/validate_inference_envoy_ai_gateway_live_gate_test.py
python3 scripts/validate_inference_envoy_ai_gateway_live_gate.py
python3 -m py_compile scripts/run_inference_envoy_ai_gateway_live.py
```

Expected: all local tests and contract validator pass.

- [ ] **Step 5: Commit**

```bash
git add scripts/run_inference_envoy_ai_gateway_live.py \
  scripts/validate_inference_envoy_ai_gateway_live_gate.py \
  scripts/validate_inference_envoy_ai_gateway_live_gate_test.py \
  deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml
git commit -m "test(envoy): cover inference gateway rate-limit live gate"
```

### Task 5: Full local and real-environment verification

**Files:**
- Modify: `repo/development-records/README.md`
- Create: `repo/development-records/inference-envoy-ai-gateway-ratelimit.md`
- Modify: `repo/CURRENT-SPRINT.md`

- [ ] **Step 1: Run local gates on committed main**

```bash
cd repo
make validate-inference-envoy-ai-gateway-manifest
make validate-inference-envoy-ai-gateway-ratelimit-config
make validate-inference-envoy-ai-gateway-live-gate
make test
make validate-architecture
git diff --check
```

- [ ] **Step 2: Fetch upstream and confirm main**

```bash
cd /root/kubercon/ANI
git fetch upstream main
git branch --show-current
```

Expected: branch is `main`; resolve any upstream divergence with a normal merge only if required by repository policy.

- [ ] **Step 3: Run the approved real Envoy gate**

Use the available kubeconfig and explicitly supplied owner/foreign tokens. Apply only the C40 rate-limit/config resources, route traffic through the Envoy AI Gateway external address, and leave pre-existing C40 services untouched unless the gate’s rollback contract requires removal. Capture redacted evidence.

- [ ] **Step 4: Record evidence and update sprint records**

Record commands, status codes, rate-limit threshold, Redis backend readiness, and cleanup/retention result without tokens, hashes, internal addresses, or Secret contents. Update `CURRENT-SPRINT.md` and the development-record index only after live evidence is actually produced.

- [ ] **Step 5: Commit final records**

```bash
git add development-records/README.md development-records/inference-envoy-ai-gateway-ratelimit.md CURRENT-SPRINT.md
git commit -m "docs: record Envoy inference rate-limit verification"
```

## Completion Checklist

- [ ] Envoy `BackendTrafficPolicy` is server-side dry-run valid and targets Gateway `ani-aigw` (covering the `ani-c40` AIGatewayRoute).
- [ ] Envoy Gateway Global RateLimit uses a Redis Secret reference, not a plaintext endpoint.
- [ ] auth-service AK RPM remains the only credential-specific limiter in C40.
- [ ] chat and embeddings traffic is tested through Envoy AI Gateway, never ANI Gateway `/v1` or vLLM ClusterIP directly.
- [ ] 429/503/401/404 semantics and redaction tests pass.
- [ ] Local gates, full tests, architecture validation, and approved real gate all pass.
