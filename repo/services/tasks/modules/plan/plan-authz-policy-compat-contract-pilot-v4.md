# ANI Gateway OpenAPI 鉴权四批次实施计划 V4

> 状态：头脑风暴确认后的实施计划，尚未实施
>
> 方案基线：`gateway-openapi-authorization-policy-migration-v4.md`
>
> 覆盖批次：`AUTHZ-POLICY-A`、`AUTHZ-COMPAT-B0`、`AUTHZ-CONTRACT-B1`、`AUTHZ-PILOT-C`
>
> PR 形式：四个批次拆成四个独立 PR
>
> 编制日期：2026-08-20
>
> 修订日期：2026-08-20（根据实现前审查修正 audience、错误映射、Principal 校验、own 边界、API Key legacy identity、pilot allowlist、运行时接口、public/AND security、dev-mode 交互、quota-meta security 兼容、API Key RLS 与 last-used、service/API Key scope、B0/C 启动接线、generated request context 和逐场景 V2 RPC 断言）

---

## 1. 请求、方案与仓库事实的边界

### 1.1 用户请求

本计划只负责把 V4 中四个 Functional MVP 批次拆成可执行、可测试、带完整代码骨架的四个 PR。当前任务不授权直接实现、提交、创建 PR、进入共享集群或实施 L1/L2。

### 1.2 V4 方案提供的设计约束

- 有 `x-ani-authz` 的 operation 使用 generated policy；无 `x-ani-authz` 的 operation 在迁移期继续走旧 middleware。
- `mode=off` 时，即使存在 generated policy，也不切换新链路。
- OpenAPI 是新 operation policy 的来源；Gateway 运行时不解析 YAML。
- 旧 `ValidateToken` / `CheckPermission` wire contract 和 platform 零 UUID 语义保持不变。
- V2 不接收 `roles`，auth-service 必须重验 raw credential 并读取权威权限。
- 首个且唯一 pilot 是 `GET /api/v1/admin/quota-meta`，`principal_kinds` 保持 `[user]`。
- V2 deny/error 不回退 legacy；auth-service 不可用返回 503。

### 1.3 头脑风暴冻结决策

| # | 决策 |
|---:|---|
| 1 | 四个批次分别形成四个独立 PR。 |
| 2 | 代码示例采用可直接作为实现起点的完整骨架。 |
| 3 | `x-ani-authz` 严格按 V4 字段继续细化。 |
| 4 | `listQuotaMeta` 的 `principal_kinds` 固定为 `[user]`。 |
| 5 | 不要求批量修改旧 route 的 `v1.yaml`。 |
| 6 | generator 内部根据 operation 是否存在 `x-ani-authz` 自动判定 generated/legacy。 |
| 7 | 不维护手工 legacy inventory，也不增加 route baseline 文件。 |
| 8 | 存量 route 缺 `x-ani-authz` 时自动走旧 middleware（兼容优先）；新增 route 必须加 `x-ani-authz`，否则拒绝合并代码。 |
| 9 | generated/legacy/public policy 写入同一个 Core 生成注册表。 |
| 10 | A PR 不给 `quota-meta` 增加 `x-ani-authz`；该改动留到 C PR。 |
| 11 | `mode=off` 对所有非 public operation 使用旧 middleware。 |
| 12 | C PR 的 `mode=pilot` 只允许 `listQuotaMeta` 进入新链路。 |

### 1.4 实现前审查冻结修正

以下修正不改变 V4 的阶段和迁移主线，只补足 V4 未冻结或计划示例写错的实现细节：

| 项目 | 冻结结论 | 依据 |
|---|---|---|
| service JWT audience | 使用当前 Core 契约的 `aud=ani-core`，不得改成 `ani-auth-service` | Core OpenAPI 是对外凭证契约；Caller Identity 与产品 JWT audience 分离 |
| V2 错误 | gRPC code 按固定表映射为 401/429/503/504；permission deny 仍为 decision + 403 | V4 要求稳定 401/403/503，且不得把凭证错误全部伪装成 503 |
| Principal | 严格验证 kind/scheme/domain/tenant/subject/credential ID/sandbox claims 组合 | V4 的 Principal 字段规则和 fail closed 原则 |
| own boundary | evaluator 只判断 permission scope；对象所有权继续由 handler/store/RLS 校验 | V4 明确 boundary check 不能替代数据面隔离 |
| B0 API Key | 保留 legacy tenant-scoped 横切 key；B1 得到 credential ID 后才切 per-key identity | 旧 `TenantContext` 没有 API Key ID，禁止伪造或记录原始 key |
| pilot allowlist | C 中必须严格等于 `{listQuotaMeta}`，并在 Gateway 启动前校验 | V4 首个 pilot 唯一 operation |
| 运行时接口 | 在各 PR 定义 Registry、ResolvedPolicy、context helper、Auth/RBAC 入口和 V2 AuthClient，不保留未定义符号 | 四个 PR 必须能独立编译，B1 mode=off 的 V2 调用数必须为零 |
| public + authz | `security: []` 与 `x-ani-authz` 同时出现时生成失败 | 避免静默吞掉矛盾策略 |
| AND security | 当前单凭证运行时不支持，A 构建期拒绝；保留单 scheme OR | V4 要求不支持的组合生成失败，不能静默放宽 |
| dev mode | `ANI_AUTH_MODE=dev` 只允许 policy mode=off；pilot/full 启动失败 | dev 会绕过 Auth/RBAC，不能用于证明 V2 |
| quota-meta security | 保留 Bearer/API Key OR；`principal_kinds: [user]` 负责把 tenant API Key 判为 403 | V4 要求迁移期 API Key 行为兼容 |
| API Key RLS | V2 查询、重验和副作用均复用 `Begin` + `types.SetDBTenant` | `api_keys` 使用 FORCE RLS，禁止 pool 直查 |
| service scope | `credential_domain` 表示 domain，`permissions` 表示非空权限集合；permission boundary 继承已验证 domain；`scope`/`roles` 降级为 deprecated legacy projection | 避免 platform service scope 被默认降为 tenant；避免 `scope`/`scopes` 只差复数导致接错 |
| B0/C 启动接线 | B0 固化 `ValidateBase` 和 `Register error` 通道；C 只增强为 registry-aware `Validate` | 避免后续批次引用尚未存在的启动接口 |
| generated request context | tenant/sandbox 同时安装 Hertz 字段和 `types.TenantContext`；platform 不注入伪 tenant | V4 6.3 要求 tenant data context，且 platform tenant ID 必须为空 |
| permission scope parser | API Key/service `permissions` 必须是规范 `scope:<resource>:<action>`，非法数据直接失败 | V4 要求权威 evaluator fail closed，不能把脏 permissions 修复成权限 | |
| pilot RPC 证据 | 每个 E2E 场景分别冻结 `ValidatePrincipal`、`CheckPermissionV2` 和 legacy RPC 精确调用次数 | 防止只看 HTTP 状态而未实际经过目标链路 |
| auth-service domain/boundary 不变量 | auth-service 在 permission store 查询前校验重验后 Principal domain 是否允许 required boundary；domain mismatch 返回 `CREDENTIAL_DOMAIN_MISMATCH` deny decision（403），未知 boundary 返回 `InvalidArgument`（500）；Gateway 和 auth-service 双重执行，两层职责不同 | 防止 tenant user 意外绑定 platform-admin 权限后跨 domain 放行；auth-service 不依赖 Gateway 前置检查 | |

### 1.5 明确不做

- 不引入 OPA、策略数据库或运行时 YAML parser。
- 不迁移 Services/OpenAI-compatible proxy。
- 不实施 `AUTHZ-BASELINE-S0`、NetworkPolicy、caller credential 或 mTLS。
- 不实现 `getPlatformMeteringUsage` 第二试点。
- 不删除旧 middleware、旧 RPC 或旧角色判断。
- 不把 L0 结果描述为 production-ready 或 zero-trust verified。

---

## 2. 总体链路与四 PR 边界

```text
repo/api/openapi/v1.yaml
        |
        v
generate_gateway_authz.py
        |
        v
internal/authz/zz_generated_core_policies.go
        |
        +-- security: [] --------------------------> public
        |
        +-- x-ani-authz 不存在 --------------------> legacy middleware
        |
        `-- x-ani-authz 存在
                |
                +-- mode=off ----------------------> legacy middleware
                +-- mode=pilot 且 operation 在 allowlist -> V2
                `-- mode=pilot 但不在 allowlist ---> legacy middleware
```

| PR | 批次 | 运行时变化 | 合并后默认行为 |
|---|---|---|---|
| 1 | `AUTHZ-POLICY-A` | 新增生成链和静态注册表 | 无切流，所有非 public operation 仍是 legacy |
| 2 | `AUTHZ-COMPAT-B0` | 新增 Principal/Policy mode/横切适配 | mode 默认 off，只调用旧 RPC |
| 3 | `AUTHZ-CONTRACT-B1` | auth-service additive V2 RPC 和 evaluator | Gateway 仍 mode=off，不调用 V2 |
| 4 | `AUTHZ-PILOT-C` | `listQuotaMeta` 增加策略并启用 pilot | 仅该 operation 使用 V2 |

---

## 3. PR 1：AUTHZ-POLICY-A

### 3.1 目标与交付物

建立 `x-ani-authz` validator、Core policy generator、统一生成注册表和 drift 门禁。A 不修改 `quota-meta`，因此 A 合并时 `v1.yaml` 可能仍没有任何 `x-ani-authz`；这不是失败，generator 应把现有非 public operation 全部生成为 legacy。

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/scripts/generate_gateway_authz.py` | 新增 | 解析 Core OpenAPI、校验扩展、生成 Go registry |
| `repo/scripts/generate_gateway_authz_test.py` | 新增 | schema、OR、AND 拒绝、public/authz 冲突、legacy、determinism 测试 |
| `repo/scripts/validate_gateway_authz_drift.py` | 新增 | 临时生成并比较 committed artifact |
| `repo/scripts/validate_core_gateway_authz_routes.py` | 新增 | 校验已注册 Core route 与生成 registry 的覆盖关系 |
| `repo/services/ani-gateway/internal/authz/policy.go` | 新增 | Policy 类型、枚举和 registry lookup |
| `repo/services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 生成 | Core public/generated/legacy 的统一注册表 |
| `repo/services/ani-gateway/internal/authz/policy_test.go` | 新增 | Go lookup、path template 测试 |
| `repo/Makefile` | 修改 | `gen-gateway-authz`、`validate-gateway-authz` |

### 3.2 步骤 A1：定义运行时 Policy 类型

文件：`repo/services/ani-gateway/internal/authz/policy.go`

```go
package authz

type PolicySource string

const (
    PolicySourcePublic    PolicySource = "public"
    PolicySourceGenerated PolicySource = "generated"
    PolicySourceLegacy    PolicySource = "legacy"
)

type Boundary string

const (
    BoundaryOwn      Boundary = "own"
    BoundaryTenant   Boundary = "tenant"
    BoundaryPlatform Boundary = "platform"
)

type PrincipalKind string

const (
    PrincipalUser    PrincipalKind = "user"
    PrincipalService PrincipalKind = "service"
    PrincipalAPIKey  PrincipalKind = "api_key"
    PrincipalSandbox PrincipalKind = "sandbox"
)

type OpenAPISecurityScheme string

const (
    OpenAPISecurityBearer OpenAPISecurityScheme = "BearerAuth"
    OpenAPISecurityAPIKey OpenAPISecurityScheme = "ApiKeyAuth"
)

type CredentialScheme string

const (
    CredentialBearer       CredentialScheme = "bearer"
    CredentialAPIKey       CredentialScheme = "api_key"
    CredentialSandboxToken CredentialScheme = "sandbox_bearer"
)

// SecurityRequirement 表示一个 AND 组合；Policy.SecurityAlternatives 表示 OR。
type SecurityRequirement struct {
    AllOf []OpenAPISecurityScheme
}

type Policy struct {
    Source               PolicySource
    OperationID          string
    Method               string
    PathTemplate         string
    SecurityAlternatives []SecurityRequirement
    Version              string
    Resource             string
    Action               string
    Boundary             Boundary
    PrincipalKinds       []PrincipalKind
}

func policyKey(method, pathTemplate string) string {
    return method + " " + pathTemplate
}

type Registry struct {
    byRoute     map[string]Policy
    byOperation map[string]Policy
}

func NewRegistry(policies map[string]Policy) (Registry, error) {
    registry := Registry{byRoute: maps.Clone(policies), byOperation: map[string]Policy{}}
    for key, policy := range policies {
        if policy.OperationID == "" { return Registry{}, fmt.Errorf("%s missing operation id", key) }
        if _, exists := registry.byOperation[policy.OperationID]; exists {
            return Registry{}, fmt.Errorf("duplicate operation id %q", policy.OperationID)
        }
        registry.byOperation[policy.OperationID] = policy
    }
    return registry, nil
}

func (r Registry) Lookup(method, pathTemplate string) (Policy, bool) {
    policy, ok := r.byRoute[policyKey(method, pathTemplate)]
    return policy, ok
}

func (r Registry) LookupOperation(operationID string) (Policy, bool) {
    policy, ok := r.byOperation[operationID]
    return policy, ok
}

func CoreRegistry() Registry {
    registry, err := NewRegistry(generatedCorePolicies)
    if err != nil { panic("invalid generated Core authz registry: " + err.Error()) }
    return registry
}
```

注意：legacy policy 不再包含人工维护的 `Owner/Reason/MigrateBy`。其事实来源就是“该 OpenAPI operation 没有 `x-ani-authz`”。

### 3.3 步骤 A2：实现 OpenAPI 分类与字段校验

文件：`repo/scripts/generate_gateway_authz.py`

```python
#!/usr/bin/env python3
from __future__ import annotations

import argparse
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

HTTP_METHODS = {"get", "post", "put", "patch", "delete", "head", "options"}
VALID_BOUNDARIES = {"own", "tenant", "platform"}
VALID_PRINCIPAL_KINDS = {"user", "service", "api_key", "sandbox"}
VALID_SECURITY_SCHEMES = {"BearerAuth", "ApiKeyAuth"}

@dataclass(frozen=True)
class ParsedPolicy:
    source: str
    operation_id: str
    method: str
    path_template: str
    security: tuple[tuple[str, ...], ...]
    version: str = ""
    resource: str = ""
    action: str = ""
    boundary: str = ""
    principal_kinds: tuple[str, ...] = ()

def effective_security(spec: dict[str, Any], operation: dict[str, Any]) -> list[dict[str, Any]]:
    # operation.security 覆盖全局 security；显式 [] 不能被 `or` 吞掉。
    if "security" in operation:
        value = operation["security"]
    else:
        value = spec.get("security", [])
    if not isinstance(value, list):
        raise ValueError("security must be an array")
    return value

def parse_security(value: list[dict[str, Any]]) -> tuple[tuple[str, ...], ...]:
    alternatives: list[tuple[str, ...]] = []
    for index, requirement in enumerate(value):
        if not isinstance(requirement, dict) or not requirement:
            raise ValueError(f"security[{index}] must be a non-empty object")
        schemes = tuple(sorted(requirement))
        unknown = set(schemes) - VALID_SECURITY_SCHEMES
        if unknown:
            raise ValueError(f"unsupported security schemes: {sorted(unknown)}")
        # 同一 Security Requirement object 的多个 scheme 是 AND。
        # 当前 Gateway/Proto 每次只携带一个 credential，无法执行多凭证 AND；
        # 按 V4 的“不支持组合生成期失败”规则显式拒绝。
        if len(schemes) != 1:
            raise ValueError(f"security[{index}] AND requirements are not supported")
        alternatives.append(schemes)
    return tuple(alternatives)  # 多个单 scheme array item 之间为 OR

def parse_authz(extension: Any) -> tuple[str, str, str, tuple[str, ...]]:
    if not isinstance(extension, dict):
        raise ValueError("x-ani-authz must be an object")
    required = {"version", "resource", "action", "boundary", "principal_kinds"}
    missing = required - extension.keys()
    unknown = extension.keys() - required
    if missing:
        raise ValueError(f"x-ani-authz missing fields: {sorted(missing)}")
    if unknown:
        raise ValueError(f"x-ani-authz unknown fields: {sorted(unknown)}")
    if extension["version"] != "v1":
        raise ValueError("x-ani-authz.version must be v1")
    if extension["boundary"] not in VALID_BOUNDARIES:
        raise ValueError("invalid x-ani-authz.boundary")
    kinds = extension["principal_kinds"]
    if not isinstance(kinds, list) or not kinds or any(not isinstance(kind, str) for kind in kinds):
        raise ValueError("invalid x-ani-authz.principal_kinds")
    if len(kinds) != len(set(kinds)) or set(kinds) - VALID_PRINCIPAL_KINDS:
        raise ValueError("invalid x-ani-authz.principal_kinds")
    for field in ("resource", "action"):
        value = extension[field]
        if not isinstance(value, str) or not value.strip():
            raise ValueError(f"x-ani-authz.{field} must be a non-empty string")
    return extension["resource"], extension["action"], extension["boundary"], tuple(kinds)

def classify(spec: dict[str, Any], path: str, method: str, operation: dict[str, Any]) -> ParsedPolicy:
    operation_id = operation.get("operationId")
    if not isinstance(operation_id, str) or not operation_id:
        raise ValueError(f"{method.upper()} {path} missing operationId")
    security_raw = effective_security(spec, operation)
    extension = operation.get("x-ani-authz")
    if security_raw == []:
        if extension is not None:
            raise ValueError(f"{method.upper()} {path} public operation must not define x-ani-authz")
        return ParsedPolicy("public", operation_id, method.upper(), path, ())
    security = parse_security(security_raw)
    if extension is None:
        # 已确认的兼容优先语义：无扩展即 legacy，不维护额外 inventory/baseline。
        return ParsedPolicy("legacy", operation_id, method.upper(), path, security)
    resource, action, boundary, kinds = parse_authz(extension)
    return ParsedPolicy(
        "generated", operation_id, method.upper(), path, security,
        "v1", resource, action, boundary, kinds,
    )
```

关键行为测试：

```python
def test_missing_extension_is_legacy() -> None:
    spec = {"security": [{"BearerAuth": []}]}
    op = {"operationId": "listInstances"}
    policy = classify(spec, "/instances", "get", op)
    assert policy.source == "legacy"

def test_explicit_empty_security_is_public() -> None:
    spec = {"security": [{"BearerAuth": []}]}
    op = {"operationId": "health", "security": []}
    assert classify(spec, "/healthz", "get", op).source == "public"

def test_public_operation_rejects_authz_extension() -> None:
    spec = {"security": [{"BearerAuth": []}]}
    op = {"operationId": "health", "security": [], "x-ani-authz": {
        "version": "v1", "resource": "health", "action": "read",
        "boundary": "platform", "principal_kinds": ["user"],
    }}
    with pytest.raises(ValueError, match="must not define x-ani-authz"):
        classify(spec, "/healthz", "get", op)

def test_security_preserves_single_credential_or() -> None:
    assert parse_security([{"BearerAuth": []}, {"ApiKeyAuth": []}]) == (
        ("BearerAuth",), ("ApiKeyAuth",),
    )

def test_security_rejects_multi_credential_and() -> None:
    with pytest.raises(ValueError, match="AND requirements are not supported"):
        parse_security([{"BearerAuth": [], "ApiKeyAuth": []}])
```

### 3.4 步骤 A3：生成统一 Core registry

generator 遍历顺序必须稳定，并拒绝重复 method/path 与重复 operationId：

```python
def collect_policies(spec: dict[str, Any]) -> list[ParsedPolicy]:
    policies: list[ParsedPolicy] = []
    seen_routes: set[tuple[str, str]] = set()
    seen_operations: set[str] = set()
    for path in sorted(spec.get("paths", {})):
        path_item = spec["paths"][path]
        for method in sorted(set(path_item) & HTTP_METHODS):
            policy = classify(spec, gateway_path(path), method, path_item[method])
            route_key = (policy.method, policy.path_template)
            if route_key in seen_routes:
                raise ValueError(f"duplicate route {route_key}")
            if policy.operation_id in seen_operations:
                raise ValueError(f"duplicate operationId {policy.operation_id}")
            seen_routes.add(route_key)
            seen_operations.add(policy.operation_id)
            policies.append(policy)
    return policies
```

生成文件示意：

```go
// Code generated by scripts/generate_gateway_authz.py; DO NOT EDIT.
package authz

var generatedCorePolicies = map[string]Policy{
    "GET /healthz": {
        Source: PolicySourcePublic, OperationID: "health",
        Method: "GET", PathTemplate: "/healthz",
    },
    "GET /api/v1/instances": {
        Source: PolicySourceLegacy, OperationID: "listInstances",
        Method: "GET", PathTemplate: "/api/v1/instances",
        SecurityAlternatives: []SecurityRequirement{{AllOf: []OpenAPISecurityScheme{OpenAPISecurityBearer}}},
    },
}
```

这里的 `legacy inventory` 仍然是 V4 要求的运行时显式策略集合，但由 generator 从 OpenAPI 全量生成，不由人另维护一份 route baseline。缺少 `x-ani-authz` 的既有或新 operation 会在生成物中明确标记为 `PolicySourceLegacy`，resolver 只读取该注册表；这不会把“缺字段”解释成 public，也不会让 Gateway 运行时重新解析 YAML。

若 OpenAPI 的 path 不包含 `/api/v1` server prefix，generator 应在同一个函数中规范化一次，不能让运行时猜前缀。当前 `/healthz`、`/readyz` 在 Hertz 根组注册，是 Core OpenAPI server prefix 的已知例外，必须显式列举：

```python
ROOT_INFRASTRUCTURE_PATHS = {"/healthz", "/readyz"}

def gateway_path(openapi_path: str) -> str:
    if openapi_path in ROOT_INFRASTRUCTURE_PATHS:
        return openapi_path
    return "/api/v1" + openapi_path
```

当前额外注册的 `/health`、`/ready` 不在 Core OpenAPI 中，应作为既有 infrastructure public route 保持现状；A 不把它们伪造成 Core product policy。对应 route contract 校验器要显式确认这两个例外，不能使用 `startswith("/health")` 一类模糊规则。

### 3.5 步骤 A4：FullPath 规范化与 lookup

Hertz `FullPath()` 使用 `:id`，OpenAPI 使用 `{id}`：

```go
var hertzParam = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)

func NormalizeHertzFullPath(fullPath string) string {
    return hertzParam.ReplaceAllString(fullPath, `{$1}`)
}

func LookupByRequest(c *app.RequestContext) (Policy, bool) {
    path := NormalizeHertzFullPath(string(c.FullPath()))
    return LookupCorePolicy(string(c.Method()), path)
}
```

Core route coverage 校验的最小骨架：

```python
CORE_PREFIX = "/api/v1/"
OUT_OF_SCOPE_PREFIXES = ("/api/v1/svc/", "/v1/")
ROOT_INFRASTRUCTURE = {"/healthz", "/readyz", "/health", "/ready"}

def requires_core_registry(full_path: str) -> bool:
    if full_path in ROOT_INFRASTRUCTURE:
        return full_path in {"/healthz", "/readyz"}
    return full_path.startswith(CORE_PREFIX) and not full_path.startswith(OUT_OF_SCOPE_PREFIXES)

def validate_registered_core_routes(
    registered: set[tuple[str, str]],
    generated: set[tuple[str, str]],
) -> list[str]:
    return [
        f"registered Core route missing from authz registry: {method} {path}"
        for method, path in sorted(registered)
        if requires_core_registry(path) and (method, path) not in generated
    ]
```

实施时优先复用现有 route-contract 脚本的解析能力；不得另写一个只覆盖 `quota-meta` 的固定字符串检查。

测试：

```go
func TestNormalizeHertzFullPath(t *testing.T) {
    got := NormalizeHertzFullPath("/api/v1/admin/tenants/:tenant_id/quota")
    want := "/api/v1/admin/tenants/{tenant_id}/quota"
    if got != want { t.Fatalf("got %q, want %q", got, want) }
}
```

### 3.6 步骤 A5：生成与 drift 门禁

```make
.PHONY: gen-gateway-authz validate-gateway-authz

gen-gateway-authz:
	python scripts/generate_gateway_authz.py \
		--input api/openapi/v1.yaml \
		--output services/ani-gateway/internal/authz/zz_generated_core_policies.go
	gofmt -w services/ani-gateway/internal/authz/zz_generated_core_policies.go

validate-gateway-authz:
	python scripts/generate_gateway_authz_test.py
	python scripts/validate_gateway_authz_drift.py
	python scripts/validate_core_gateway_authz_routes.py
	go test -count=1 ./services/ani-gateway/internal/authz
```

drift 校验必须使用临时文件，不覆盖工作区：

```python
with tempfile.TemporaryDirectory() as directory:
    generated = Path(directory) / "zz_generated_core_policies.go"
    generate(input_path, generated)
    subprocess.run(["gofmt", "-w", str(generated)], check=True)
    if generated.read_bytes() != committed.read_bytes():
        raise SystemExit("generated gateway authz policy drift; run make gen-gateway-authz")
```

### 3.7 PR 1 测试与完成定义

- 所有无 `x-ani-authz`、非 public operation 生成为 legacy。
- 新增 route 必须加 `x-ani-authz`，否则拒绝合并代码；存量 route 缺扩展仍自动走 legacy。
- `security: []` 生成为 public；不能被全局 security 覆盖，也不得同时声明 `x-ani-authz`。
- 存在扩展但缺字段/未知枚举/未知字段时构建失败。
- Bearer/API Key 的单凭证 OR 保真；多凭证 AND 和未知 scheme 构建失败。
- 同一输入重复生成字节一致；手改生成物 drift 失败。
- A 不修改 `quota-meta`，不改变 Gateway middleware，不引入 mode 配置。

---

## 4. PR 2：AUTHZ-COMPAT-B0

### 4.1 目标与交付物

引入统一 Principal、policy mode 和 Principal-aware 横切键，但默认 off，所有非 public operation 仍只使用旧 `ValidateToken` / `CheckPermission`。

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/services/ani-gateway/internal/authz/principal.go` | 新增 | 规范 Principal、LegacyPrincipalView、domain/boundary 和 identity key |
| `repo/services/ani-gateway/internal/authz/mode.go` | 新增 | off/pilot/full、auth mode 组合和 operation allowlist 解析 |
| `repo/services/ani-gateway/internal/middleware/policy.go` | 新增 | `ResolvedPolicy`、请求 context helper 和 policy resolver |
| `repo/services/ani-gateway/internal/middleware/auth.go`、`rbac.go` | 修改 | legacy view；仍只用旧 RPC |
| `repo/services/ani-gateway/internal/middleware/ratelimit.go`、`idempotency.go`、`audit.go` | 修改 | 用户/platform/sandbox 使用稳定 identity；API Key B0 保留 legacy tenant key |
| `repo/services/ani-gateway/internal/middleware/chain.go` | 修改 | 启动前校验配置并注册 resolver；默认行为不变 |
| `repo/services/ani-gateway/main.go` | 修改 | 接收 `middleware.Register` 的配置错误，在监听前 fail closed |
| `repo/services/ani-gateway/internal/middleware/auth_errors.go` | 新增 | 统一 401/403/503 包装 helper，内部最终调用 `respondError` |

### 4.2 步骤 B0-1：定义 Principal 和结构校验

```go
type CredentialDomain string

const (
    DomainTenant   CredentialDomain = "tenant"
    DomainPlatform CredentialDomain = "platform"
    DomainSandbox  CredentialDomain = "sandbox"
)

type Principal struct {
    Kind             PrincipalKind
    CredentialScheme CredentialScheme
    CredentialDomain CredentialDomain
    TenantID         string
    SubjectID        string
    CredentialID     string
    LegacyRoles      []string // 只能由 legacy 路径读取
    SandboxClaims    *SandboxClaims
}

type SandboxClaims struct {
    TenantID  string
    InstanceID string
}

func requireNonZeroUUID(name, value string) error {
    id, err := uuid.Parse(value)
    if err != nil || id == uuid.Nil { return fmt.Errorf("%s must be a non-zero UUID", name) }
    return nil
}

func (p Principal) Validate() error {
    if len(p.LegacyRoles) != 0 { return errors.New("normative principal must not contain legacy roles") }
    switch p.Kind {
    case PrincipalUser, PrincipalService, PrincipalAPIKey, PrincipalSandbox:
    default:
        return errors.New("unknown principal kind")
    }
    switch p.CredentialDomain {
    case DomainPlatform:
        if p.TenantID != "" { return errors.New("platform principal tenant_id must be empty") }
    case DomainTenant, DomainSandbox:
        if err := requireNonZeroUUID("tenant_id", p.TenantID); err != nil { return err }
    default:
        return errors.New("unknown credential domain")
    }

    switch p.Kind {
    case PrincipalUser:
        if p.CredentialScheme != CredentialBearer { return errors.New("user requires bearer credential") }
        if p.CredentialDomain != DomainTenant && p.CredentialDomain != DomainPlatform {
            return errors.New("user requires tenant or platform domain")
        }
        if p.CredentialID != "" || p.SandboxClaims != nil { return errors.New("user contains unrelated credential fields") }
        return requireNonZeroUUID("user subject_id", p.SubjectID)
    case PrincipalService:
        if p.CredentialScheme != CredentialBearer { return errors.New("service requires bearer credential") }
        if p.CredentialDomain != DomainTenant && p.CredentialDomain != DomainPlatform {
            return errors.New("service requires tenant or platform domain")
        }
        if p.CredentialID != "" || p.SandboxClaims != nil { return errors.New("service contains unrelated credential fields") }
        if strings.TrimSpace(p.SubjectID) == "" { return errors.New("service subject_id required") }
    case PrincipalAPIKey:
        if p.CredentialScheme != CredentialAPIKey || p.CredentialDomain != DomainTenant {
            return errors.New("api key requires api_key credential and tenant domain")
        }
        if err := requireNonZeroUUID("credential_id", p.CredentialID); err != nil { return err }
        if p.SandboxClaims != nil { return errors.New("api key contains sandbox claims") }
        if p.SubjectID != "" {
            if err := requireNonZeroUUID("api key subject_id", p.SubjectID); err != nil { return err }
        }
    case PrincipalSandbox:
        if p.CredentialScheme != CredentialSandboxToken || p.CredentialDomain != DomainSandbox {
            return errors.New("sandbox requires sandbox_bearer credential and sandbox domain")
        }
        if p.CredentialID != "" { return errors.New("sandbox contains credential_id") }
        if p.SandboxClaims == nil { return errors.New("sandbox claims required") }
        if p.SandboxClaims.TenantID != p.TenantID { return errors.New("sandbox claim tenant mismatch") }
        if strings.TrimSpace(p.SandboxClaims.InstanceID) == "" { return errors.New("sandbox instance_id required") }
    }
    return nil
}

func (p Principal) WithoutLegacyRoles() Principal {
    p.LegacyRoles = nil
    return p
}

func (p Policy) AllowsPrincipalKind(kind PrincipalKind) bool {
    return slices.Contains(p.PrincipalKinds, kind)
}

func DomainAllowsBoundary(principal Principal, required Boundary) bool {
    switch required {
    case BoundaryOwn, BoundaryTenant:
        return (principal.CredentialDomain == DomainTenant && principal.TenantID != "") ||
            (principal.CredentialDomain == DomainSandbox && principal.TenantID != "")
    case BoundaryPlatform:
        return principal.CredentialDomain == DomainPlatform && principal.TenantID == ""
    default:
        return false
    }
}
```

### 4.3 步骤 B0-2：旧 TenantContext 转 LegacyPrincipalView

规范 `Principal` 只表示字段闭合的新身份。旧 `TenantContext` 没有 principal kind 和 API Key credential ID，不能强行转换成一个看似完整的规范 Principal；B0 使用单独的 legacy view。旧 platform 零 UUID只在 view 中规范为空，不改变旧 RPC 返回值：

```go
const zeroUUID = "00000000-0000-0000-0000-000000000000"

type LegacyPrincipalView struct {
    CredentialScheme CredentialScheme
    TenantID         string
    SubjectID        string
    Scope            string
    Roles            []string
    SandboxClaims    *SandboxClaims
}

func LegacyViewFromTenantContext(
    tc *commonv1.TenantContext,
    scheme CredentialScheme,
) (LegacyPrincipalView, error) {
    if tc == nil { return LegacyPrincipalView{}, errors.New("tenant context required") }
    tenantID := strings.TrimSpace(tc.GetTenantId())
    if tc.GetScope() == "platform" {
        if tenantID == zeroUUID { tenantID = "" }
    }
    view := LegacyPrincipalView{
        CredentialScheme: scheme, TenantID: tenantID,
        SubjectID: tc.GetUserId(), Scope: tc.GetScope(),
        Roles: append([]string(nil), tc.GetRoles()...),
    }
    if view.Scope == "platform" {
        if view.TenantID != "" { return LegacyPrincipalView{}, errors.New("platform legacy tenant must normalize empty") }
    } else if err := requireNonZeroUUID("legacy tenant_id", view.TenantID); err != nil {
        return LegacyPrincipalView{}, err
    }
    return view, nil
}
```

Gateway 可以根据实际请求头知道 legacy credential 是 Bearer 还是 API Key，但 B0 不知道 API Key ID。固定处理：

- B0 不把 legacy API Key 包装成 `PrincipalAPIKey`，不伪造 `CredentialID`。
- B0 不使用原始 API Key、hash 或 prefix 作为 identity key。
- legacy API Key 的横切键继续保持 tenant 级粒度；多个 key 共享该 namespace 是已知兼容行为。
- B1 的 `ValidatePrincipal` 返回稳定 `credential_id` 后，才切换到 `tenant:<tenant>:api_key:<credential_id>`。

### 4.4 步骤 B0-3：mode 和 pilot allowlist

```go
type Mode string

const (
    ModeOff   Mode = "off"
    ModePilot Mode = "pilot"
    ModeFull  Mode = "full"
)

type Config struct {
    Mode            Mode
    AuthMode        string
    PilotOperations map[string]struct{}
}

func ConfigFromEnv() (Config, error) {
    mode := Mode(strings.ToLower(strings.TrimSpace(os.Getenv("GATEWAY_AUTHZ_POLICY_MODE"))))
    if mode == "" { mode = ModeOff }
    if mode != ModeOff && mode != ModePilot && mode != ModeFull {
        return Config{}, fmt.Errorf("invalid GATEWAY_AUTHZ_POLICY_MODE %q", mode)
    }
    allow := map[string]struct{}{}
    for _, value := range strings.Split(os.Getenv("GATEWAY_AUTHZ_PILOT_OPERATIONS"), ",") {
        if value = strings.TrimSpace(value); value != "" { allow[value] = struct{}{} }
    }
    cfg := Config{
        Mode: mode,
        AuthMode: strings.ToLower(strings.TrimSpace(os.Getenv("ANI_AUTH_MODE"))),
        PilotOperations: allow,
    }
    if err := cfg.ValidateBase(); err != nil { return Config{}, err }
    return cfg, nil
}

func (c Config) ValidateBase() error {
    if c.AuthMode == "dev" && c.Mode != ModeOff {
        return errors.New("ANI_AUTH_MODE=dev only supports GATEWAY_AUTHZ_POLICY_MODE=off")
    }
    if c.Mode != ModePilot && len(c.PilotOperations) != 0 {
        return errors.New("pilot operations require GATEWAY_AUTHZ_POLICY_MODE=pilot")
    }
    return nil
}

func (c Config) EffectiveSource(policy Policy) PolicySource {
    if policy.Source == PolicySourcePublic { return PolicySourcePublic }
    switch c.Mode {
    case ModeOff:
        return PolicySourceLegacy
    case ModePilot:
        if policy.Source == PolicySourceGenerated {
            if _, ok := c.PilotOperations[policy.OperationID]; ok { return PolicySourceGenerated }
        }
        return PolicySourceLegacy
    case ModeFull:
        return policy.Source
    default:
        return PolicySourceLegacy
    }
}
```

B0 只负责 `ValidateBase`：校验 mode、`ANI_AUTH_MODE=dev` 交互以及非 pilot 模式不得携带 allowlist；不在此批次冻结 Functional MVP 的 operation 集合。`middleware.Register` 在 B0 就改为返回 error，`main.go` 在 `h.Spin()` 前处理该 error。测试必须证明这些非法配置在注册 middleware/监听端口前失败；C 再在同一入口上叠加严格的 `{listQuotaMeta}` 校验。

```go
// B0：internal/middleware/chain.go
func Register(h *server.Hertz, store GatewayStore) error {
    if store == nil { return errors.New("gateway middleware store is required") }
    cfg, err := authz.ConfigFromEnv()
    if err != nil { return err } // ConfigFromEnv 已执行 ValidateBase
    registerLegacyCompatibleChain(h, store, NewAuthClientFromEnv(), authz.CoreRegistry(), cfg)
    return nil
}

func registerLegacyCompatibleChain(
    h *server.Hertz, store GatewayStore, client AuthClient,
    registry authz.Registry, cfg authz.Config,
) {
    h.Use(
        RequestID(),
        ResolveAuthzPolicy(registry, cfg),
        AuthWithResolvedPolicy(client),
        RBACWithResolvedPolicy(client),
        RateLimit(store), Idempotency(store), Audit(),
    )
}

// B0：main.go，在 h.Spin() 之前
if err := middleware.Register(h, gatewayStore); err != nil {
    logger.Error("failed to configure gateway authz", "err", err)
    os.Exit(1)
}
```

统一响应 helper 也在 B0 闭合，避免后续批次引用未定义符号：

```go
func respond401(c *app.RequestContext, _ string) {
    respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid credential")
}

func respond403(c *app.RequestContext, reason string) {
    if reason == "" { reason = "permission denied" }
    respondError(c, http.StatusForbidden, "FORBIDDEN", reason)
}

func respond503(c *app.RequestContext, _ string) {
    respondError(c, http.StatusServiceUnavailable, "AUTHZ_UNAVAILABLE", "authorization service unavailable")
}
```

### 4.5 步骤 B0-4：请求 policy context 和旧中间件分流

`ResolvedPolicy` 和请求 context helper 在本 PR 定义，不留隐式符号：

```go
const resolvedPolicyContextKey = "ani.authz.resolved_policy"

type ResolvedPolicy struct {
    Policy authz.Policy
    Source authz.PolicySource
}

func SetResolvedPolicy(c *app.RequestContext, resolved ResolvedPolicy) {
    c.Set(resolvedPolicyContextKey, resolved)
}

func GetResolvedPolicy(c *app.RequestContext) (ResolvedPolicy, error) {
    value, ok := c.Get(resolvedPolicyContextKey)
    if !ok { return ResolvedPolicy{}, errors.New("resolved policy missing") }
    resolved, ok := value.(ResolvedPolicy)
    if !ok { return ResolvedPolicy{}, errors.New("resolved policy has invalid type") }
    return resolved, nil
}
```

```go
func ResolveAuthzPolicy(registry authz.Registry, cfg authz.Config) app.HandlerFunc {
    return func(ctx context.Context, c *app.RequestContext) {
        fullPath := string(c.FullPath())
        if fullPath == "" {
            // 未匹配路由交给 Hertz NoRoute 返回 404，不对 raw path 做授权匹配。
            c.Next(ctx)
            return
        }
        normalized := authz.NormalizeHertzFullPath(fullPath)
        if !authz.IsCorePolicyPath(normalized) {
            // Services、OpenAI-compatible proxy 和额外根级 infrastructure 路由
            // 不属于本次 Core registry，继续走既有 legacy/public middleware。
            SetResolvedPolicy(c, ResolvedPolicy{Source: authz.PolicySourceLegacy})
            c.Next(ctx)
            return
        }
        policy, ok := registry.Lookup(string(c.Method()), normalized)
        if !ok {
            respondError(c, http.StatusServiceUnavailable, "AUTHZ_POLICY_MISSING", "registered route has no authz policy")
            return
        }
        resolved := ResolvedPolicy{Policy: policy, Source: cfg.EffectiveSource(policy)}
        SetResolvedPolicy(c, resolved)
        c.Next(ctx)
    }
}
```

`IsCorePolicyPath` 必须使用与 A 的 route coverage 相同的显式域规则；Core 注册 route 缺 registry 才返回 503，Services/proxy 不得因本批次尚未迁移而被误伤。

```go
func IsCorePolicyPath(path string) bool {
    switch path {
    case "/healthz", "/readyz":
        return true
    case "/health", "/ready":
        return false
    }
    return strings.HasPrefix(path, "/api/v1/") &&
        !strings.HasPrefix(path, "/api/v1/svc/")
}
```

链路骨架：

```go
h.Use(
    RequestID(),
    ResolveAuthzPolicy(authz.CoreRegistry(), cfg),
    AuthWithResolvedPolicy(authClient),      // B0 generated 也按 legacy 调 ValidateToken
    RBACWithResolvedPolicy(authClient),      // B0 只调旧 CheckPermission
    RateLimitByPrincipal(store),
    IdempotencyByPrincipal(store),
    AuditPrincipalDecision(),
)
```

B0 中 `AuthWithResolvedPolicy` / `RBACWithResolvedPolicy` 是对既有 Auth/RBAC 的显式重构入口，不是未定义占位符：

```go
func AuthWithResolvedPolicy(client AuthClient) app.HandlerFunc {
    return func(ctx context.Context, c *app.RequestContext) {
        resolved, err := GetResolvedPolicy(c)
        if err != nil { respond503(c, "authz policy context missing"); return }
        if resolved.Source == authz.PolicySourcePublic { c.Next(ctx); return }
        if resolved.Source != authz.PolicySourceLegacy {
            // B0 不允许新链路；出现 generated 说明配置/接线违约。
            respond503(c, "generated authentication is not enabled")
            return
        }
        authenticateLegacy(ctx, c, client) // 从现有 AuthWithClient 提取，行为不变
    }
}

func RBACWithResolvedPolicy(client AuthClient) app.HandlerFunc {
    return func(ctx context.Context, c *app.RequestContext) {
        resolved, err := GetResolvedPolicy(c)
        if err != nil { respond503(c, "authz policy context missing"); return }
        if resolved.Source == authz.PolicySourcePublic { c.Next(ctx); return }
        if resolved.Source != authz.PolicySourceLegacy {
            respond503(c, "generated authorization is not enabled")
            return
        }
        authorizeLegacy(ctx, c, client) // 从现有 RBACWithClient 提取，只调旧 RPC
    }
}
```

### 4.6 步骤 B0-5：横切 identity key

```go
func (p Principal) IdentityKey() (string, error) {
    if err := p.Validate(); err != nil { return "", err }
    switch p.Kind {
    case PrincipalAPIKey:
        return "tenant:" + p.TenantID + ":api_key:" + p.CredentialID, nil
    case PrincipalService:
        return string(p.CredentialDomain) + ":service:" + p.SubjectID, nil
    case PrincipalSandbox:
        return "tenant:" + p.TenantID + ":sandbox:" + p.SandboxClaims.InstanceID, nil
    case PrincipalUser:
        if p.CredentialDomain == DomainPlatform { return "platform:user:" + p.SubjectID, nil }
        return "tenant:" + p.TenantID + ":user:" + p.SubjectID, nil
    default:
        return "", errors.New("unknown principal kind")
    }
}

func (p LegacyPrincipalView) IdentityKey() (string, error) {
    switch p.CredentialScheme {
    case CredentialAPIKey:
        // B0 没有 credential ID，保留 tenant 粒度；不读取原始 API Key。
        if err := requireNonZeroUUID("legacy api key tenant_id", p.TenantID); err != nil { return "", err }
        return "tenant:" + p.TenantID + ":api_key:legacy", nil
    case CredentialSandboxToken:
        if p.SandboxClaims == nil || strings.TrimSpace(p.SandboxClaims.InstanceID) == "" {
            return "", errors.New("legacy sandbox claims required")
        }
        return "tenant:" + p.TenantID + ":sandbox:" + p.SandboxClaims.InstanceID, nil
    case CredentialBearer:
        if err := requireNonZeroUUID("legacy subject_id", p.SubjectID); err != nil { return "", err }
        if p.Scope == "platform" { return "platform:user:" + p.SubjectID, nil }
        return "tenant:" + p.TenantID + ":user:" + p.SubjectID, nil
    default:
        return "", errors.New("unknown legacy credential scheme")
    }
}
```

请求 context helper 位于 `internal/middleware/policy.go`，与 `authz` 包类型显式分层：

```go
const (
    principalContextKey       = "ani.authz.principal"
    legacyPrincipalContextKey = "ani.authz.legacy_principal"
)

func SetPrincipal(c *app.RequestContext, principal authz.Principal) {
    c.Set(principalContextKey, principal)
}

func GetPrincipal(c *app.RequestContext) (authz.Principal, error) {
    value, ok := c.Get(principalContextKey)
    if !ok { return authz.Principal{}, errors.New("principal missing") }
    principal, ok := value.(authz.Principal)
    if !ok { return authz.Principal{}, errors.New("principal has invalid type") }
    return principal, principal.Validate()
}

func SetLegacyPrincipalView(c *app.RequestContext, view authz.LegacyPrincipalView) {
    c.Set(legacyPrincipalContextKey, view)
}

func RequestIdentityKey(c *app.RequestContext) (string, error) {
    if principal, err := GetPrincipal(c); err == nil { return principal.IdentityKey() }
    value, ok := c.Get(legacyPrincipalContextKey)
    if !ok { return "", errors.New("request identity missing") }
    view, ok := value.(authz.LegacyPrincipalView)
    if !ok { return "", errors.New("legacy principal has invalid type") }
    return view.IdentityKey()
}
```

限流不再遇到 platform 空 tenant 就跳过：

```go
identityKey, err := RequestIdentityKey(c)
if err != nil {
    respondError(c, http.StatusUnauthorized, "INVALID_PRINCIPAL", "invalid principal")
    return
}
allowed, err := checkLimit(ctx, store, identityKey, string(c.Method()), string(c.Path()), limit)
```

幂等 cache key 将旧 `TenantContext.Scope + tenantID` 替换为 `identityKey`（由 `RequestIdentityKey` 统一计算），请求 fingerprint/replay 语义不变。`identityKey` 在 V2 路径由 `Principal.CredentialDomain` + `TenantID` + `SubjectID`/`CredentialID` 组成，在 legacy 路径由 `LegacyPrincipalView.Scope`（映射自旧 `TenantContext.Scope`）+ `TenantID` + `SubjectID` 组成，两种路径的输出格式一致，因此切换前后同一请求的 cache key 不变。Audit 只记录 kind/domain/subject 或 credential ID，不记录 token/API Key。

### 4.7 PR 2 测试与完成定义

```go
func TestModeOffAlwaysUsesLegacy(t *testing.T) {
    cfg := Config{Mode: ModeOff}
    policy := Policy{Source: PolicySourceGenerated, OperationID: "listQuotaMeta"}
    if got := cfg.EffectiveSource(policy); got != PolicySourceLegacy {
        t.Fatalf("got %q, want legacy", got)
    }
}

func TestPlatformLegacyPrincipalHasEmptyTenantInternally(t *testing.T) {
    tc := &commonv1.TenantContext{TenantId: zeroUUID, UserId: uuid.NewString(), Scope: "platform"}
    view, err := LegacyViewFromTenantContext(tc, CredentialBearer)
    if err != nil || view.TenantID != "" { t.Fatalf("view=%+v err=%v", view, err) }
}
```

- 默认 mode off，非法 mode 启动失败。
- `ANI_AUTH_MODE=dev` 与 pilot/full 组合启动失败；off/full 携带 pilot allowlist 启动失败。
- mode off 只调用旧 RPC，旧 Gateway/auth-service 行为不变。
- platform 请求获得非空 identity key，限流和审计不再因空 tenant 绕过。
- B0 API Key 只使用 tenant-scoped legacy key；不得出现 raw key/hash/prefix；per-key identity 延后 B1。
- kind/scheme/domain、空 user/service subject、空 API Key credential ID、nil sandbox claims 正反测试通过且无 panic。
- tenant、API Key、sandbox、dev/local 既有回归通过。

---

## 5. PR 3：AUTHZ-CONTRACT-B1

### 5.1 目标与交付物

additive 增加 V2 Proto、JWT/API Key 规范 Principal、服务端权威 permission evaluator 和 Gateway V2 client。Gateway mode 仍为 off。

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/api/proto/auth/v1/auth_service.proto` | 修改 | additive V2 RPC/message；旧 tag/wire 不变 |
| `repo/pkg/generated/pb/auth/v1/*` | 生成 | `make gen-proto` 生成 V2 client/server |
| `repo/services/auth-service/internal/service/jwt.go` | 修改 | principal kind、`aud=ani-core` 和兼容解析 |
| `repo/services/auth-service/internal/service/api_keys.go` | 修改 | credential ID、无副作用重验和错误分类 |
| `repo/services/auth-service/internal/service/permissions.go` | 新增 | 权威 permission store/evaluator |
| `repo/services/auth-service/internal/service/auth_service.go` | 修改 | ValidatePrincipal/CheckPermissionV2 handler |
| `repo/services/ani-gateway/internal/middleware/auth_client.go` | 修改 | AuthClient/grpcAuthClient 增加 V2 方法 |
| `repo/services/ani-gateway/internal/middleware/auth_rpc_errors.go` | 新增 | gRPC 到 HTTP 的稳定错误映射 |
| 对应 `*_test.go` | 修改/新增 | compatibility、audience、错误和 evaluator 测试 |

`AuthService` 在 B1 增加 `permissions PermissionStore` 字段，并在 `NewAuthService` 中用同一 DB pool 初始化；如果该依赖未就绪，V2 必须返回 `Unavailable`，不能 panic 或把请求伪装成无权限：

```go
type AuthService struct {
    authv1.UnimplementedAuthServiceServer
    jwt *JWTValidator
    issuer *JWTIssuer
    apiKeys *apiKeyStore
    permissions PermissionStore // B1 新增，接口类型，生产注入 *permissionStore，测试注入 spy
    // 既有 refreshTokens、blocklist、oidc、passwordLogin、platformLogin 保持；
    // user JWT 签发端需在 B1 阶段为 IssueAccessToken/IssuePlatformAccessToken 补写
    // principal_kind 和 credential_domain，详见 5.4 IssueAccessToken/IssuePlatformAccessToken 签发端兼容映射。
}

// 在现有 NewAuthService 的 return literal 中新增这一项，不重写既有初始化：
permissions: newPermissionStore(db),
```

### 5.2 步骤 B1-1：additive Proto

文件：`repo/api/proto/auth/v1/auth_service.proto`

```proto
service AuthService {
  // 旧 RPC 保留，tag 和 wire 语义不变。
  rpc ValidateToken(ValidateTokenRequest) returns (common.v1.TenantContext);
  rpc CheckPermission(CheckPermissionRequest) returns (CheckPermissionResponse);

  // V2 additive RPC。
  rpc ValidatePrincipal(ValidatePrincipalRequest) returns (PrincipalContext);
  rpc CheckPermissionV2(AuthorizationRequest) returns (AuthorizationDecision);
}

message ValidatePrincipalRequest {
  string credential = 1;          // raw JWT/API key，禁止写日志
  string credential_scheme = 2;   // bearer|api_key
}

message PrincipalContext {
  string principal_kind = 1;      // user|service|api_key
  string credential_scheme = 2;   // bearer|api_key
  string credential_domain = 3;   // tenant|platform
  string tenant_id = 4;           // platform 必须为空
  string subject_id = 5;
  string credential_id = 6;
}

message AuthorizationRequest {
  PrincipalContext principal = 1;
  string resource = 2;
  string action = 3;
  string required_boundary = 4;
  string operation_id = 5;
  string credential = 6;          // 服务端必须重验；禁止记录
  string credential_scheme = 7;
}

message AuthorizationDecision {
  bool allowed = 1;
  string reason_code = 2;
}

// IssueServiceTokenRequest additive 变更：旧 scope 字段 deprecated，
// 新增 permissions 和 credential_domain，tag 不变保证 wire 兼容。
message IssueServiceTokenRequest {
  string caller_service = 1;
  string caller_secret  = 2;
  string tenant_id      = 3;

  string scope = 4 [deprecated = true]; // 旧权限字段，格式 scope:<resource>:<action>

  int32 ttl_seconds = 5;

  // V2 additive 字段
  repeated string permissions = 6;     // 权限集合，格式 scope:<resource>:<action>
  string credential_domain = 7;        // tenant|platform
}
```

禁止增加：`roles`、Gateway 计算出的 permission、可跳过 credential 重验的 trusted flag。

### 5.3 步骤 B1-2：闭合 Gateway AuthClient V2 接口

现有 `AuthClient` 保留全部旧方法，并 additive 增加以下两个方法；所有 fake 必须编译期实现完整接口：

```go
type AuthClient interface {
    // 既有 Login/Refresh/API Key/legacy 方法保持不变。
    ValidateToken(ctx context.Context, token string) (*commonv1.TenantContext, error)
    CheckPermission(ctx context.Context, req *authv1.CheckPermissionRequest) (*authv1.CheckPermissionResponse, error)

    ValidatePrincipal(
        ctx context.Context,
        credential string,
        scheme authz.CredentialScheme,
    ) (*authv1.PrincipalContext, error)
    CheckPermissionV2(
        ctx context.Context,
        req *authv1.AuthorizationRequest,
    ) (*authv1.AuthorizationDecision, error)
}

var _ AuthClient = (*grpcAuthClient)(nil)

func (c *grpcAuthClient) ValidatePrincipal(
    ctx context.Context,
    credential string,
    scheme authz.CredentialScheme,
) (*authv1.PrincipalContext, error) {
    callCtx, cancel := context.WithTimeout(ctx, c.timeout)
    defer cancel()
    return c.client.ValidatePrincipal(callCtx, &authv1.ValidatePrincipalRequest{
        Credential: credential, CredentialScheme: string(scheme),
    })
}

func (c *grpcAuthClient) CheckPermissionV2(
    ctx context.Context,
    req *authv1.AuthorizationRequest,
) (*authv1.AuthorizationDecision, error) {
    callCtx, cancel := context.WithTimeout(ctx, c.timeout)
    defer cancel()
    return c.client.CheckPermissionV2(callCtx, req)
}
```

`repo/services/ani-gateway/internal/router/auth_test.go` 等现有 fake 因接口 additive 变更必须同步补 V2 stub；B1 mode=off 测试断言这些 stub 调用数始终为零。

Proto 与内部 Principal 的转换也在 B1 闭合，并在转换后立即执行结构校验：

```go
func PrincipalFromProto(value *authv1.PrincipalContext) (Principal, error) {
    if value == nil { return Principal{}, errors.New("principal context required") }
    principal := Principal{
        Kind: PrincipalKind(value.GetPrincipalKind()),
        CredentialScheme: CredentialScheme(value.GetCredentialScheme()),
        CredentialDomain: CredentialDomain(value.GetCredentialDomain()),
        TenantID: value.GetTenantId(), SubjectID: value.GetSubjectId(),
        CredentialID: value.GetCredentialId(),
    }
    return principal, principal.Validate()
}

func (p Principal) Proto() *authv1.PrincipalContext {
    return &authv1.PrincipalContext{
        PrincipalKind: string(p.Kind), CredentialScheme: string(p.CredentialScheme),
        CredentialDomain: string(p.CredentialDomain), TenantId: p.TenantID,
        SubjectId: p.SubjectID, CredentialId: p.CredentialID,
    }
}
```

### 5.4 步骤 B1-3：JWT claims 语义分离

```go
type jwtPayload struct {
    Subject       string   `json:"sub"`
    Issuer        string   `json:"iss"`
    Audience      any      `json:"aud,omitempty"` // 实现时兼容 string/[]string
    Expires       int64    `json:"exp"`
    NotBefore     int64    `json:"nbf"`
    IssuedAt      int64    `json:"iat"`
    JTI           string   `json:"jti"`
    PrincipalKind string   `json:"principal_kind,omitempty"`
    TenantID      string   `json:"tid"`
    UserID        string   `json:"uid"`

    // V2 规范字段：credential_domain 表示凭证边界；permissions 表示签名携带的权限集合。
    CredentialDomain string   `json:"credential_domain,omitempty"`
    Permissions      []string `json:"permissions,omitempty"` // service/API 权威权限；不来自 Gateway

    // Deprecated legacy projection：仅供旧 ValidateToken / 旧 Gateway 消费；V2 不读取。
    Scope string   `json:"scope,omitempty"` // deprecated，旧 TenantContext.Scope
    Roles []string `json:"roles"`          // deprecated，旧 CheckPermission roles
}

// auth-service 内部规范记录；不依赖 Gateway internal/authz 包。
type principalRecord struct {
    Kind             string
    CredentialScheme string
    Domain           string
    TenantID         string
    SubjectID        string
    CredentialID     string
    Permissions      []string // 仅 auth-service 内部重验结果使用，不进入 Gateway Proto
}

// 旧 RPC 仍需返回的 legacy projection；roles/scope 不进入 V2 Proto。
type legacyJWTProjection struct {
    Scope string
    Roles []string
}

// 同时承载新 Principal 和旧 RPC projection；V2 只读 Principal，legacy 只读 Legacy。
type validatedJWT struct {
    Principal principalRecord
    Legacy    legacyJWTProjection
    JTI       string
}

const serviceJWTAudience = "ani-core"

func normalizeJWTPrincipal(payload jwtPayload) (*validatedJWT, error) {
    kind := payload.PrincipalKind
    if kind == "" { kind = "user" } // 旧 JWT 兼容规则
    if kind != "user" && kind != "service" { return nil, errInvalidJWT }

    // V2 从 credential_domain 读取边界，不做 scope fallback。
    // 旧 JWT 没有 credential_domain 时 domain 为空，由调用方决定是否拒绝：
    //   - ValidatePrincipal（V2 路径）：domain 为空则 fail
    //   - ValidateToken（legacy 路径）：不校验 domain，只读 Legacy.Scope
    domain := payload.CredentialDomain
    if domain != "" && domain != "tenant" && domain != "platform" { return nil, errInvalidJWT }

    // Core OpenAPI 已冻结 service token audience=ani-core。
    // ani-auth-service 是内部服务名，不是产品 service JWT 的 audience。
    if kind == "service" && !audienceContains(payload.Audience, serviceJWTAudience) {
        return nil, errInvalidJWT
    }

    tenantID := strings.TrimSpace(payload.TenantID)
    // domain 为空时跳过 tenantID 校验（旧 JWT 没有 credential_domain，由调用方决定是否拒绝）。
    if domain == "platform" {
        if tenantID != "" { return nil, errInvalidJWT }
    } else if domain == "tenant" {
        if id, err := uuid.Parse(tenantID); err != nil || id == uuid.Nil { return nil, errInvalidJWT }
    }

    subjectID := strings.TrimSpace(payload.UserID)
    if kind == "user" {
        if id, err := uuid.Parse(subjectID); err != nil || id == uuid.Nil { return nil, errInvalidJWT }
    } else {
        subjectID = strings.TrimSpace(payload.Subject)
        if subjectID == "" { return nil, errInvalidJWT }
    }

    // V2 projection：credential_domain + permissions。
    principal := principalRecord{
        Kind: kind, CredentialScheme: "bearer", Domain: domain,
        TenantID: tenantID, SubjectID: subjectID,
        Permissions: append([]string(nil), payload.Permissions...),
    }

    // legacy projection：scope + roles。
    // 如果 V2 没填 scope，按 domain 回填，保证旧 TenantContext.Scope 不为空。
    legacyScope := payload.Scope
    if legacyScope == "" { legacyScope = domain }
    legacy := legacyJWTProjection{
        Scope: legacyScope,
        Roles: append([]string(nil), payload.Roles...),
    }

    return &validatedJWT{Principal: principal, Legacy: legacy, JTI: payload.JTI}, nil
}

// validPermissionScopeClaims 校验签名凭证携带的 permissions 是否符合 scope:<resource>:<action> 格式。
func validPermissionScopeClaims(permissions []string) bool {
    if len(permissions) == 0 { return false }
    for _, raw := range permissions {
        if !strings.HasPrefix(strings.TrimSpace(raw), "scope:") { return false }
        value := strings.TrimPrefix(strings.TrimSpace(raw), "scope:")
        parts := strings.SplitN(value, ":", 2)
        if len(parts) != 2 || parts[0] == "" || parts[1] == "" { return false }
    }
    return true
}

func (p principalRecord) Proto() *authv1.PrincipalContext {
    return &authv1.PrincipalContext{
        PrincipalKind: p.Kind, CredentialScheme: p.CredentialScheme,
        CredentialDomain: p.Domain, TenantId: p.TenantID,
        SubjectId: p.SubjectID, CredentialId: p.CredentialID,
    }
}

func jwtPrincipalContext(value *validatedJWT) (*authv1.PrincipalContext, error) {
    if value == nil { return nil, errInvalidJWT }
    return value.Principal.Proto(), nil
}
```

这里冻结 JWT 的语义分离规则：`credential_domain` 是 V2 规范字段，表示凭证边界，只能是 `tenant|platform`；`permissions` 是 V2 规范字段，表示签名凭证携带的权限集合，当前格式为 `scope:<resource>:<action>`。service 必须有非空、格式正确的 `permissions`，API Key permissions 从数据库读取；Gateway 不提交或解释这些 permissions。权限的 boundary 继承已验证 Principal 的 domain，因此 platform service 的 permission 生成 `Permission.Scope=platform`，tenant API Key/service 生成 `tenant`。`scope` 和 `roles` 是 deprecated legacy projection，仅供旧 `ValidateToken` / 旧 Gateway 消费，V2 不读取。`normalizeJWTPrincipal` 只负责解析填充，不做 `scope` fallback、不做 V2 特有校验；service `permissions` 非空校验由 `ValidatePrincipal`（V2 路径）执行，不在 `normalizeJWTPrincipal` 中拒绝旧 service JWT。

```go
// 例如：platform service 的 claims 为
// credential_domain=platform, permissions=["scope:quota:read"],
// scope="scope:quota:read"(deprecated legacy projection), roles=["service"](deprecated)；
// tenant API Key 的 permissions 来自 api_keys.scopes，边界固定为 tenant。
// 兼容期 legacy scope 只保留 permissions[0]，旧 Gateway 只能看到第一项权限；多权限场景需走 V2。
```

`audienceContains` 必须兼容 JWT 标准允许的 string/array 形式，并 fail closed：

```go
func audienceContains(raw any, want string) bool {
    switch value := raw.(type) {
    case string:
        return value == want
    case []any:
        for _, item := range value {
            if audience, ok := item.(string); ok && audience == want { return true }
        }
    }
    return false
}
```

- 旧 user JWT 缺 `principal_kind` 时继续按 user 兼容，不因缺 audience 被误拒绝。
- service JWT 必须同时满足 ANI 签名、issuer、`principal_kind=service`、`aud=ani-core`、expiry/JTI 和明确 `permissions`（V2 路径校验，legacy 路径不校验）。
- 不得为兼容同时接受 `ani-core` 与 `ani-auth-service`；若未来要改 audience，必须先改 Core OpenAPI 契约并走兼容迁移。
- 旧 JWT 缺 `credential_domain` 时 V2 路径 fail（`ValidatePrincipal` 返回错误），legacy 路径不受影响（`ValidateToken` 只读 `Legacy.Scope`）。B1 阶段 user JWT 签发端（`IssueAccessToken`/`IssuePlatformAccessToken`）已补写 `credential_domain`，B1 上线后新签发的 user JWT 自然满足 V2 要求；B1 前已签发的旧 JWT 通过 TTL 到期自然淘汰，pilot 切换点与 B1 上线之间需留足 TTL 窗口。

现有 `JWTValidator.Validate` 在完成签名、issuer、expiry/JTI 和 blocklist 校验后调用 `normalizeJWTPrincipal`，返回 `validatedJWT`。旧 `ValidateToken` 只从其中读取 `Legacy.Scope` / `Legacy.Roles`，并把 platform 的空 tenant 转回零 UUID 后构造旧 `TenantContext`；`ValidatePrincipal` 只读取 `Principal`（`CredentialDomain` + `Permissions`），因此 V2 永远不携带 `scope`/`roles`。`ValidatePrincipal` 在读取 `Principal` 后额外校验：`CredentialDomain` 非空（旧 JWT 缺该字段时 V2 fail）、service `permissions` 非空且格式正确。这个适配保证旧 wire 行为不变，同时避免维护两套 JWT 验签。

#### IssueServiceToken 签发端兼容映射

`IssueServiceTokenRequest` 的 `scope` 字段（tag 4）当前表示权限 `scope:platform-workloads:read`，与 V2 `permissions` 语义重叠但命名不同。为保证现有 inference-service 暂不改动，签发端按以下规则映射：

```go
// resolveIssueServiceTokenClaims 将 IssueServiceTokenRequest 映射为 V2 JWT claims。
func resolveIssueServiceTokenClaims(req *authv1.IssueServiceTokenRequest) (jwtPayload, error) {
    hasLegacyScope := strings.TrimSpace(req.GetScope()) != ""
    hasPermissions := len(req.GetPermissions()) != 0

    // 同时提交 scope 和 permissions 时，二者不一致必须拒绝，不能静默选一个。
    if hasLegacyScope && hasPermissions {
        expected := []string{req.GetScope()}
        if !equalStringSlice(expected, req.GetPermissions()) {
            return jwtPayload{}, status.Error(codes.InvalidArgument,
                "scope and permissions are both set but inconsistent")
        }
    }

    var permissions []string
    switch {
    case hasPermissions:
        permissions = req.GetPermissions()
    case hasLegacyScope:
        permissions = []string{req.GetScope()}
    default:
        return jwtPayload{}, status.Error(codes.InvalidArgument,
            "either scope or permissions must be set")
    }

    // credential_domain：优先取请求值；旧调用方未填时按 tenant_id 推导。
    domain := strings.TrimSpace(req.GetCredentialDomain())
    if domain == "" {
        if strings.TrimSpace(req.GetTenantId()) != "" {
            domain = "tenant"
        } else {
            domain = "platform"
        }
    }
    if domain != "tenant" && domain != "platform" {
        return jwtPayload{}, status.Error(codes.InvalidArgument, "invalid credential_domain")
    }

    // 签发时同时写入 V2 规范字段和 deprecated legacy projection。
    return jwtPayload{
        PrincipalKind:    "service",
        Audience:          serviceJWTAudience, // ani-core
        TenantID:          req.GetTenantId(),
        Subject:           req.GetCallerService(),
        CredentialDomain:  domain,
        Permissions:       permissions,
        // deprecated legacy projection：旧 ValidateToken / 旧 Gateway 仍需消费。
        Scope:             permissions[0], // 旧 scope 取第一个权限，保持旧 Gateway 行为
        Roles:             []string{"service"},
    }, nil
}

func equalStringSlice(a, b []string) bool {
    if len(a) != len(b) { return false }
    for i := range a { if a[i] != b[i] { return false } }
    return true
}

// IssueAccessToken 签发 tenant user JWT。
// passwordLogin / oidc / tenant refresh 均复用此方法。
// 现有字段（scope、roles、audience 等）保持原样，只补 principal_kind 和 credential_domain。
func (i *JWTIssuer) IssueAccessToken(principal refreshPrincipal, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = defaultAccessTokenTTL
	}
	now := i.now()
	payload := jwtPayload{
		Subject:          principal.UserID.String(),
		Issuer:           i.issuer,
		Expires:           now.Add(ttl).Unix(),
		NotBefore:         now.Unix(),
		IssuedAt:          now.Unix(),
		JTI:               uuid.NewString(),
		TenantID:          principal.TenantID.String(),
		UserID:            principal.UserID.String(),
		PrincipalKind:    "user",    // V2 规范字段：凭证身份类型
		CredentialDomain: "tenant",  // V2 规范字段：凭证所属边界
		Roles:             principal.Roles,
		Scope:             "tenant",
	}
	return i.sign(payload)
}

// IssuePlatformAccessToken 签发 platform user JWT。
// platformLogin / platform refresh 均复用此方法。
// 现有字段保持原样，只补 principal_kind 和 credential_domain。
func (i *JWTIssuer) IssuePlatformAccessToken(principal platformPrincipal, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = defaultAccessTokenTTL
	}
	now := i.now()
	roles := principal.Roles
	if len(roles) == 0 {
		roles = []string{"platform-admin"}
	}
	payload := jwtPayload{
		Subject:          principal.UserID.String(),
		Issuer:           i.issuer,
		Expires:           now.Add(ttl).Unix(),
		NotBefore:         now.Unix(),
		IssuedAt:          now.Unix(),
		JTI:               uuid.NewString(),
		TenantID:          "",
		UserID:            principal.UserID.String(),
		PrincipalKind:    "user",      // V2 规范字段：凭证身份类型
		CredentialDomain: "platform",  // V2 规范字段：凭证所属边界
		Roles:             roles,
		Scope:             "platform",
	}
	return i.sign(payload)
}
```

兼容规则总结：

- 新调用方优先提交 `permissions` 和 `credential_domain`。
- 旧调用方只提交 `scope` 时：`permissions = [scope]`，`tenant_id` 非空时推导 `credential_domain=tenant`，否则推导 `platform`。
- 同时提交 `scope` 和 `permissions` 时：二者不一致必须拒绝（`InvalidArgument`），不能静默选一个。
- 签发出的 JWT 同时写入 V2 规范字段（`credential_domain` + `permissions`）和 deprecated legacy projection（`scope` + `roles=["service"]`），保证旧 `ValidateToken` 和旧 Gateway 仍能正常消费。
- 兼容期 legacy `scope` 只保留 `permissions[0]`，旧 Gateway 只能看到第一项权限；多权限场景需走 V2。
- `permissions` 为空或格式非法时 fail closed（`InvalidArgument`），不签发无权限的 service JWT。
- user JWT 签发端（`IssueAccessToken`/`IssuePlatformAccessToken`）补写 `principal_kind="user"` 和 `credential_domain`（tenant/platform）。`passwordLogin`/`oidc`/tenant `refreshTokens` 复用 `IssueAccessToken`，`platformLogin`/platform `refreshTokens` 复用 `IssuePlatformAccessToken`，四条路径自动收口。
- `scope`、`roles`、`audience` 等现有字段保持原样，legacy 链路继续消费，不因 deprecated 而清空。
- `normalizeJWTPrincipal` 的 `kind == "" → "user"` 兼容规则不变，旧 JWT 仍走 legacy 路径。
- user JWT 不携带 `permissions`，权限由数据库权威读取。
- 不新增 `userJWTAudience` 常量；user JWT 的 audience 行为保持现状。
- 旧 JWT 处理：B1 上线后新签发的 user JWT 自然包含 `credential_domain`；B1 前已签发的旧 JWT 缺该字段，V2 路径 fail 是预期行为，通过 TTL 到期自然淘汰。pilot 切换点与 B1 上线之间需留足 TTL 窗口。

### 5.5 步骤 B1-4：API Key 返回 credential ID

当前 `apiKeyStore.validate` 查询需要增加 `id`：

```go
type apiKeyPrincipal struct {
    CredentialID uuid.UUID
    TenantID     uuid.UUID
    UserID       uuid.UUID
    Permissions []string // 对应数据库 api_keys.scopes 列，V2 权限集合
}

err = tx.QueryRow(ctx, `
    SELECT id, tenant_id, user_id, scopes, rate_limit_rpm
    FROM api_keys
    WHERE tenant_id=$1
      AND key_hash=$2
      AND revoked_at IS NULL
      AND (expires_at IS NULL OR expires_at > NOW())
`, tenantID, hashAPIKey(rawKey)).Scan(
    &principal.CredentialID, &principal.TenantID, &userID,
    &principal.Permissions, &rateLimitRPM,
)
```

V2 Principal：

```go
subjectID := ""
if principal.UserID != uuid.Nil { subjectID = principal.UserID.String() }
return &authv1.PrincipalContext{
    PrincipalKind: "api_key", CredentialScheme: "api_key",
    CredentialDomain: "tenant", TenantId: principal.TenantID.String(),
    SubjectId: subjectID,
    CredentialId: principal.CredentialID.String(),
}, nil
```

API Key Principal 转换和“按 credential ID 重读”共用同一个内部记录：

```go
func apiKeyPrincipalContext(value *apiKeyPrincipal) (*authv1.PrincipalContext, error) {
    if value == nil || value.CredentialID == uuid.Nil || value.TenantID == uuid.Nil {
        return nil, errors.New("invalid api key principal")
    }
    subjectID := ""
    if value.UserID != uuid.Nil { subjectID = value.UserID.String() }
    return &authv1.PrincipalContext{
        PrincipalKind: "api_key", CredentialScheme: "api_key",
        CredentialDomain: "tenant", TenantId: value.TenantID.String(),
        SubjectId: subjectID, CredentialId: value.CredentialID.String(),
    }, nil
}
```

该 helper 不得退化成 `s.db.QueryRow`：`api_keys` 启用了 `FORCE ROW LEVEL SECURITY`，所以查询、`last_used_at` 更新以及任何 credential 状态重验都必须在带 `types.SetDBTenant` 的事务中执行。`validateWithOptions` 的两次调用只允许改变 rate-limit/last-used 副作用选项，不能改变 RLS 初始化路径。

现有 `apiKeyStore.validate` 同时做 credential 校验、rate limit increment 和 `last_used_at` 更新。V2 一次 HTTP 请求会先调用 `ValidatePrincipal`，随后 `CheckPermissionV2` 再重验 credential；不能因此把一次请求计成两次。

应将读取/重验与单次请求副作用拆开：

```go
type apiKeyValidationOptions struct {
    EnforceRateLimit bool
    TouchLastUsed    bool
}

func (s *apiKeyStore) loadActiveByRawCredential(
    ctx context.Context, rawKey string,
) (*apiKeyPrincipal, int32, error) {
    tenantID, err := parseAPIKeyTenant(rawKey)
    if err != nil { return nil, 0, err }
    // API Key 只能按 key 中解析出的 tenant 建立 RLS 上下文。
    ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID})
    tx, err := s.db.Begin(ctx)
    if err != nil { return nil, 0, err }
    defer rollbackTx(ctx, tx)
    if err := types.SetDBTenant(ctx, tx); err != nil { return nil, 0, err }

    var principal apiKeyPrincipal
    var userID pgtype.UUID
    var rateLimitRPM int32
    err = tx.QueryRow(ctx, `
        SELECT id, tenant_id, user_id, scopes, rate_limit_rpm
        FROM api_keys
        WHERE tenant_id=$1 AND key_hash=$2
          AND revoked_at IS NULL
          AND (expires_at IS NULL OR expires_at > NOW())
    `, tenantID, hashAPIKey(rawKey)).Scan(
        &principal.CredentialID, &principal.TenantID, &userID,
        &principal.Permissions, &rateLimitRPM,
    )
    if err != nil { return nil, 0, err }
    if userID.Valid { principal.UserID = uuid.UUID(userID.Bytes) }
    if err := tx.Commit(ctx); err != nil { return nil, 0, err }
    return &principal, rateLimitRPM, nil
}

func (s *apiKeyStore) validateWithOptions(
    ctx context.Context,
    rawKey string,
    options apiKeyValidationOptions,
) (*apiKeyPrincipal, error) {
    principal, rateLimitRPM, err := s.loadActiveByRawCredential(ctx, rawKey)
    if err != nil { return nil, err }
    if options.EnforceRateLimit {
        if err := s.enforceRateLimit(ctx, hashAPIKey(rawKey), rateLimitRPM); err != nil { return nil, err }
    }
    if options.TouchLastUsed {
        if err := s.touchLastUsed(ctx, principal.TenantID, principal.CredentialID); err != nil { return nil, err }
    }
    return principal, nil
}

func (s *apiKeyStore) touchLastUsed(
    ctx context.Context, tenantID, credentialID uuid.UUID,
) error {
    if tenantID == uuid.Nil || credentialID == uuid.Nil {
        return errors.New("invalid api key identity")
    }
    ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID})
    tx, err := s.db.Begin(ctx)
    if err != nil { return err }
    defer rollbackTx(ctx, tx)
    if err := types.SetDBTenant(ctx, tx); err != nil { return err }
    tag, err := tx.Exec(ctx, `
        UPDATE api_keys
        SET last_used_at = NOW()
        WHERE tenant_id = $1 AND id = $2
          AND revoked_at IS NULL
          AND (expires_at IS NULL OR expires_at > NOW())
    `, tenantID, credentialID)
    if err != nil { return err }
    if tag.RowsAffected() != 1 { return pgx.ErrNoRows }
    return tx.Commit(ctx)
}
```

- `ValidatePrincipal` 使用 `{EnforceRateLimit: true, TouchLastUsed: true}`。
- `CheckPermissionV2` 重验使用 `{EnforceRateLimit: false, TouchLastUsed: false}`，但仍重新查询 hash、revoked、expired、permissions 和 credential ID。。
- 旧 `ValidateToken` 保持现有一次 rate limit 和 last-used 副作用。
- `touchLastUsed` 的集成测试必须断言事务内执行 `SetDBTenant`，跨 tenant credential ID 更新为 0 行并返回 credential invalid/unauthenticated，不能因为 RLS 失败而更新其它租户记录。
- 测试断言一次 V2 HTTP 请求最多 increment 一次；虽然 `quota-meta` 拒绝 API Key，该守卫必须为后续 tenant operation 保留。

### 5.6 步骤 B1-5：ValidatePrincipal 与服务端错误分类

```go
func credentialValidationStatus(err error) error {
    switch {
    case errors.Is(err, errAPIKeyRateLimitExceeded):
        return status.Error(codes.ResourceExhausted, "credential rate limit exceeded")
    case errors.Is(err, context.DeadlineExceeded):
        return status.Error(codes.DeadlineExceeded, "credential validation deadline exceeded")
    case errors.Is(err, errInvalidJWT), errors.Is(err, pgx.ErrNoRows):
        // expired/revoked/API Key hash miss 均不暴露具体原因。
        return status.Error(codes.Unauthenticated, "invalid credential")
    default:
        // blocklist/cache/DB 等依赖错误不能伪装成无效凭证。
        return status.Error(codes.Unavailable, "credential backend unavailable")
    }
}

func (s *AuthService) ValidatePrincipal(ctx context.Context, req *authv1.ValidatePrincipalRequest) (*authv1.PrincipalContext, error) {
    credential := req.GetCredential()
    if credential == "" { return nil, status.Error(codes.Unauthenticated, "credential required") }
    switch req.GetCredentialScheme() {
    case "api_key":
        principal, err := s.apiKeys.validateWithOptions(ctx, credential, apiKeyValidationOptions{
            EnforceRateLimit: true, TouchLastUsed: true,
        })
        if err != nil { return nil, credentialValidationStatus(err) }
        return apiKeyPrincipalContext(principal), nil
    case "bearer":
        if s.jwt == nil { return nil, status.Error(codes.Unavailable, "credential backend unavailable") }
        claims, err := s.jwt.Validate(ctx, credential)
        if err != nil { return nil, credentialValidationStatus(err) }
        principal, err := jwtPrincipalContext(claims)
        if err != nil {
            // 签名通过但 Principal claim 组合非法，仍属于无效 credential；
            // 不允许裸 error 变成 gRPC Unknown 后被 Gateway 误映射为 503。
            return nil, status.Error(codes.Unauthenticated, "invalid credential")
        }
        // V2 路径额外校验：CredentialDomain 非空。
        // 旧 JWT 缺 credential_domain 时 Domain 为空，V2 路径 fail；
        // legacy 路径（ValidateToken）不校验 Domain，只读 Legacy.Scope，不受影响。
        if claims.Principal.Domain == "" {
            return nil, status.Error(codes.Unauthenticated, "invalid credential")
        }
        // service 必须携带非空且格式正确的 permissions。
        // user 的权限由数据库权威读取，不校验 JWT 中的 permissions。
        if claims.Principal.Kind == "service" && !validPermissionScopeClaims(claims.Principal.Permissions) {
            return nil, status.Error(codes.Unauthenticated, "invalid credential")
        }
        return principal, nil
    default:
        return nil, status.Error(codes.InvalidArgument, "unsupported credential scheme")
    }
}
```

错误文本只能描述类别，不得拼接 `credential`、JWT payload、API Key hash 或 SQL。实现时必须让 JWT validator 的“签名/expiry/revoked”和“blocklist/cache backend error”保持可由 `errors.Is` 分类，不能最后都压成同一个 `errInvalidJWT`。

### 5.7 步骤 B1-6：权威 user permission 查询

```go
// PermissionStore 是权威权限查询的接口；生产注入 *permissionStore，测试注入 spy。
type PermissionStore interface {
    Allows(ctx context.Context, principal principalRecord, resource, action, boundary string) (bool, error)
}

type permissionStore struct {
    db *pgxpool.Pool
}

func newPermissionStore(db *pgxpool.Pool) *permissionStore {
    return &permissionStore{db: db}
}

type Permission struct {
    Resource string   `json:"resource"`
    Actions  []string `json:"actions"`
    Scope    string   `json:"scope"`
}

var errInvalidPermissionScope = errors.New("invalid permission scope")

var errInvalidAuthorizationBoundary = errors.New("invalid authorization boundary")

// permissionsFromScopes 将签名凭证携带的 permissions（格式 scope:<resource>:<action>）
// 解析为 Permission 结构；boundary 继承已验证 Principal 的 domain。
func permissionsFromScopes(permissions []string, boundary string) ([]Permission, error) {
    if boundary != "tenant" && boundary != "platform" {
        return nil, fmt.Errorf("%w: boundary", errInvalidPermissionScope)
    }
    var result []Permission
    for _, raw := range permissions {
        normalized := strings.TrimSpace(raw)
        if !strings.HasPrefix(normalized, "scope:") {
            return nil, fmt.Errorf("%w: prefix", errInvalidPermissionScope)
        }
        value := strings.TrimPrefix(normalized, "scope:")
        parts := strings.SplitN(value, ":", 2)
        if len(parts) != 2 || !validScopePart(parts[0]) || !validScopePart(parts[1]) {
            return nil, fmt.Errorf("%w: resource/action", errInvalidPermissionScope)
        }
        result = append(result, Permission{Resource: parts[0], Actions: []string{parts[1]}, Scope: boundary})
    }
    if len(result) == 0 { return nil, fmt.Errorf("%w: empty", errInvalidPermissionScope) }
    return result, nil
}

func (s *permissionStore) userPermissions(ctx context.Context, principal principalRecord) ([]Permission, error) {
    rows, err := s.db.Query(ctx, `
        SELECT r.permissions
        FROM users u
        JOIN user_roles ur ON ur.user_id = u.id
        JOIN roles r ON r.id = ur.role_id
        WHERE u.id = $1
          AND u.status = 'active'
          AND (($2 = 'platform' AND u.tenant_id IS NULL AND r.tenant_id IS NULL)
            OR ($2 = 'tenant' AND u.tenant_id = $3
                AND (r.tenant_id = $3 OR r.tenant_id IS NULL)))
    `, principal.SubjectID, principal.Domain, nullableTenant(principal.TenantID))
    if err != nil { return nil, fmt.Errorf("query authoritative permissions: %w", err) }
    defer rows.Close()
    return decodePermissionRows(rows)
}

func (s *permissionStore) Allows(
    ctx context.Context, principal principalRecord,
    resource, action, requiredBoundary string,
) (bool, error) {
    var permissions []Permission
    var err error
    switch principal.Kind {
    case "user":
        permissions, err = s.userPermissions(ctx, principal)
    case "api_key", "service":
        permissions, err = permissionsFromScopes(principal.Permissions, principal.Domain)
    default:
        return false, errors.New("unsupported principal kind")
    }
    if err != nil { return false, err }
    for _, permission := range permissions {
        if permissionAllows(permission, resource, action, requiredBoundary) { return true, nil }
    }
    return false, nil
}
```

API Key 创建时的 `normalizeAPIKeyScopes` 与 evaluator 的 `permissionsFromScopes` 必须共享 `validScopePart` 和最终持久化格式 `scope:<resource>:<action>`。创建 API Key 的请求仍可兼容现有简写输入，但必须先规范化成带 `scope:` 前缀的值再写数据库；evaluator 只接受规范持久化值，不负责把历史脏数据“修好”。缺前缀、空 scope、未知格式或非法 resource/action 一律返回 `errInvalidPermissionScope`，由 `CheckPermissionV2` 映射为 `FailedPrecondition`/HTTP 503，且不产生 allow decision。

```go
func TestPermissionsFromScopesFailClosed(t *testing.T) {
    invalid := [][]string{
        nil,
        {},
        {"quota:read"},
        {"scope:"},
        {"scope:quota"},
        {"scope:quota:read:extra"},
        {"scope:quota/read"},
    }
    for _, permissions := range invalid {
        if _, err := permissionsFromScopes(permissions, "tenant"); !errors.Is(err, errInvalidPermissionScope) {
            t.Fatalf("permissionsFromScopes(%v) error = %v, want errInvalidPermissionScope", permissions, err)
        }
    }
    got, err := permissionsFromScopes([]string{"scope:quota:read"}, "platform")
    if err != nil { t.Fatal(err) }
    want := []Permission{{Resource: "quota", Actions: []string{"read"}, Scope: "platform"}}
    if diff := cmp.Diff(want, got); diff != "" { t.Fatalf("permissions mismatch (-want +got):\n%s", diff) }
}
```

实现时必须重新核对 tenant built-in role 的绑定语义，不能只凭 `r.tenant_id IS NULL` 给 tenant user platform scope；最终 evaluator 仍受 requested boundary 限制。

### 5.8 步骤 B1-7：统一 permission evaluator 与 own 所有权边界

本节同时冻结 auth-service Principal domain / operation boundary 不变量。以下结论与权限 evaluator 共同构成 auth-service 的授权闭环：

- domain/boundary 校验由 Gateway 和 auth-service 双重执行。Gateway 使用 `DomainAllowsBoundary` 尽早拒绝，减少无意义 RPC；auth-service 使用重验后的 Principal 判断，保证自己永远不返回跨 domain 的 allow decision。两层职责不同，不互相替代。
- auth-service 使用重验后的 Principal 判断，不能使用 Gateway 自报的其它身份信息。
- domain mismatch 返回 deny decision，reason 为 `CREDENTIAL_DOMAIN_MISMATCH`（HTTP 403）。credential 有效（不是 401），boundary 是合法值（不是 500），只是身份不允许进入该边界。
- 未知 boundary 返回 `InvalidArgument`（HTTP 500 `AUTHZ_CONTRACT_ERROR`），因为 boundary 来自生成策略，出现未知值代表 Gateway/Auth 契约错误，不应归咎用户。
- domain mismatch 必须在 permission store 查询前短路，即使数据库中该 Principal 意外绑定了跨 domain 的权限也不能放行。
- 该检查不替代 own 资源所有权检查；own boundary 通过 domain 校验后仍由 handler/store/RLS 校验对象所有权。
- 该检查不等于 caller authentication，不声明零信任完成。

```go
func permissionAllows(p Permission, resource, action, requiredBoundary string) bool {
    resourceMatch := p.Resource == resource || p.Resource == "*"
    actionMatch := slices.Contains(p.Actions, action) || slices.Contains(p.Actions, "*")
    if !resourceMatch || !actionMatch { return false }
    switch requiredBoundary {
    case "platform":
        return p.Scope == "platform"
    case "tenant":
        return p.Scope == "tenant"
    case "own":
        return p.Scope == "own" || p.Scope == "tenant"
    default:
        return false
    }
}
```

API Key permissions 要先解析为同一 Permission 结构；tenant API Key 永远不能得到 platform boundary。service JWT 只消费签名 token 中受控 service permissions，不为未来 service registry 新增表。

这里的 `own` 分支只表示 permission scope 允许进入 own-boundary operation。它不证明目标对象属于当前 user，且不得被 handler 当作对象所有权结论：

```text
CheckPermissionV2
  -> 只回答 principal 是否具备 resource/action/own 权限范围

handler/store/RLS
  -> 使用权威资源记录校验 resource.tenant_id
  -> 对 own operation 继续校验 resource.owner_id == principal.subject_id
  -> 跨用户对象返回 404/403，按现有资源契约冻结
```

本批次不向 V2 RPC 增加 Gateway 自报的 `owner_id`，因为 Gateway 不能成为对象所有权权威来源。后续迁移任何 own operation 时，必须在该资源的 handler/store 测试中增加“同租户不同用户拒绝”；没有所有权闭环的 operation 不得从 legacy 迁入 generated。`quota-meta` 是 platform boundary，不受该未实施 own 数据面门禁阻塞。

### 5.9 步骤 B1-8：CheckPermissionV2 重验与一致性比较

```go
func samePrincipal(want, got *authv1.PrincipalContext) bool {
    return want.GetPrincipalKind() == got.GetPrincipalKind() &&
        want.GetCredentialScheme() == got.GetCredentialScheme() &&
        want.GetCredentialDomain() == got.GetCredentialDomain() &&
        want.GetTenantId() == got.GetTenantId() &&
        want.GetSubjectId() == got.GetSubjectId() &&
        want.GetCredentialId() == got.GetCredentialId()
}

// validateRequiredBoundary 校验 boundary 是否为合法枚举值。
// 未知 boundary 代表 Gateway/Auth 契约错误，返回 InvalidArgument。
func validateRequiredBoundary(boundary string) error {
    switch strings.TrimSpace(boundary) {
    case "own", "tenant", "platform":
        return nil
    default:
        return errInvalidAuthorizationBoundary
    }
}

// principalDomainAllowsBoundary 校验重验后的 Principal domain 是否允许进入 required boundary。
// 使用重验后的 Principal 判断，不使用 Gateway 自报的身份信息。
// "允许继续判断"不等于授权成功，后面还要查 permission。
func principalDomainAllowsBoundary(principal principalRecord, requiredBoundary string) bool {
    switch requiredBoundary {
    case "platform":
        return principal.Domain == "platform" && principal.TenantID == ""
    case "tenant", "own":
        return principal.Domain == "tenant" && principal.TenantID != ""
    default:
        return false
    }
}

func (s *AuthService) validateCredentialForAuthorization(
    ctx context.Context, scheme, credential string,
) (*principalRecord, error) {
    switch scheme {
    case "api_key":
        value, err := s.apiKeys.validateWithOptions(ctx, credential, apiKeyValidationOptions{})
        if err != nil { return nil, credentialValidationStatus(err) }
        subjectID := ""
        if value.UserID != uuid.Nil { subjectID = value.UserID.String() }
        return &principalRecord{
            Kind: "api_key", CredentialScheme: "api_key", Domain: "tenant",
            TenantID: value.TenantID.String(), SubjectID: subjectID,
            CredentialID: value.CredentialID.String(), Permissions: append([]string(nil), value.Permissions...),
        }, nil
    case "bearer":
        if s.jwt == nil { return nil, status.Error(codes.Unavailable, "credential backend unavailable") }
        value, err := s.jwt.Validate(ctx, credential)
        if err != nil { return nil, credentialValidationStatus(err) }
        return &value.Principal, nil
    default:
        return nil, status.Error(codes.InvalidArgument, "unsupported credential scheme")
    }
}

func (s *AuthService) CheckPermissionV2(ctx context.Context, req *authv1.AuthorizationRequest) (*authv1.AuthorizationDecision, error) {
    // 1. 校验请求结构：7 字段非空，用 TrimSpace 防空白。
    if req.GetPrincipal() == nil ||
        strings.TrimSpace(req.GetResource()) == "" ||
        strings.TrimSpace(req.GetAction()) == "" ||
        strings.TrimSpace(req.GetRequiredBoundary()) == "" ||
        strings.TrimSpace(req.GetOperationId()) == "" ||
        strings.TrimSpace(req.GetCredential()) == "" ||
        strings.TrimSpace(req.GetCredentialScheme()) == "" {
        return nil, status.Error(codes.InvalidArgument, "authorization request incomplete")
    }

    // 2. 校验 boundary 是合法枚举值；未知值代表 Gateway/Auth 契约错误。
    if err := validateRequiredBoundary(req.GetRequiredBoundary()); err != nil {
        return nil, status.Error(codes.InvalidArgument, "unsupported authorization boundary")
    }

    // 3. 重验 raw credential，读取权威状态。
    //    不重复执行 API Key rate-limit increment / last_used_at 等单次 HTTP 请求副作用。
    verifiedRecord, err := s.validateCredentialForAuthorization(
        ctx, req.GetCredentialScheme(), req.GetCredential(),
    )
    if err != nil { return nil, err }

    // 4. 比较 Gateway Principal 与重验 Principal，防止伪造。
    verified := verifiedRecord.Proto()
    if !samePrincipal(req.GetPrincipal(), verified) {
        return &authv1.AuthorizationDecision{Allowed: false, ReasonCode: "PRINCIPAL_MISMATCH"}, nil
    }

    // 5. 校验重验后的 Principal domain 是否允许 required boundary。
    //    即使 Principal 没被伪造，也不能跨 credential domain。
    //    domain mismatch 返回 deny decision（403），不是 gRPC error：
    //    credential 有效（不是 401），boundary 是合法值（不是 500），只是身份不允许进入该边界。
    if !principalDomainAllowsBoundary(*verifiedRecord, req.GetRequiredBoundary()) {
        return &authv1.AuthorizationDecision{Allowed: false, ReasonCode: "CREDENTIAL_DOMAIN_MISMATCH"}, nil
    }

    // 6. 读取权威 permission 并匹配 resource/action/permission scope。
    if s.permissions == nil { return nil, status.Error(codes.Unavailable, "authorization backend unavailable") }
    allowed, err := s.permissions.Allows(ctx, *verifiedRecord, req.GetResource(), req.GetAction(), req.GetRequiredBoundary())
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return nil, status.Error(codes.DeadlineExceeded, "authorization deadline exceeded")
        }
        if errors.Is(err, errInvalidPermissionScope) {
            return nil, status.Error(codes.FailedPrecondition, "authoritative permission scope invalid")
        }
        return nil, status.Error(codes.Unavailable, "authorization backend unavailable")
    }

    // 7. 返回 decision。
    if !allowed { return &authv1.AuthorizationDecision{Allowed: false, ReasonCode: "PERMISSION_DENIED"}, nil }
    return &authv1.AuthorizationDecision{Allowed: true, ReasonCode: "ALLOWED"}, nil
}
```

`validateCredentialForAuthorization` 必须复用与 `ValidatePrincipal` 相同的 credential 错误分类，只把"无效/过期/撤销"返回 `Unauthenticated`，把限流返回 `ResourceExhausted`，把 deadline 返回 `DeadlineExceeded`，把 DB/cache/blocklist 故障返回 `Unavailable`；不得返回未经 `status.Error` 包装的裸错误。

Gateway 对 ValidatePrincipal/CheckPermissionV2 共用一张固定错误表：

| gRPC code | HTTP | API code | 语义 |
|---|---:|---|---|
| `Unauthenticated` | 401 | `UNAUTHORIZED` | 无效、过期或已撤销 credential |
| `PermissionDenied` | 403 | `FORBIDDEN` | 防御性映射；正常权限拒绝使用 decision |
| `ResourceExhausted` | 429 | `RATE_LIMIT_EXCEEDED` | API Key/credential 限流 |
| `Unavailable` / `FailedPrecondition` | 503 | `AUTHZ_UNAVAILABLE` | auth-service 或依赖未就绪 |
| `DeadlineExceeded` | 504 | `AUTHZ_DEADLINE_EXCEEDED` | auth RPC 超时 |
| `InvalidArgument` | 500 | `AUTHZ_CONTRACT_ERROR` | Gateway/Proto 接线错误，不归咎用户 |
| 其它 | 503 | `AUTHZ_UNAVAILABLE` | fail closed，不泄漏内部详情 |

```go
func writeAuthRPCError(c *app.RequestContext, err error) {
    switch status.Code(err) {
    case codes.Unauthenticated:
        respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired credential")
    case codes.PermissionDenied:
        respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied")
    case codes.ResourceExhausted:
        respondError(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "credential rate limit exceeded")
    case codes.DeadlineExceeded:
        respondError(c, http.StatusGatewayTimeout, "AUTHZ_DEADLINE_EXCEEDED", "authorization deadline exceeded")
    case codes.InvalidArgument:
        respondError(c, http.StatusInternalServerError, "AUTHZ_CONTRACT_ERROR", "authorization contract error")
    case codes.Unavailable, codes.FailedPrecondition:
        respondError(c, http.StatusServiceUnavailable, "AUTHZ_UNAVAILABLE", "authorization service unavailable")
    default:
        respondError(c, http.StatusServiceUnavailable, "AUTHZ_UNAVAILABLE", "authorization service unavailable")
    }
}
```

日志只允许记录 gRPC code、operation ID、decision reason code 和 request ID；不得记录 `err.Error()` 中可能出现的 credential/SQL，也不得把 auth-service 原始错误文本直接返回 HTTP。

### 5.10 PR 3 测试与完成定义

- Proto 是 additive diff，旧 field tag/message/RPC 不变，V2 无 roles。
- 旧 JWT 缺 `principal_kind` 时只按 user；service 必须有 kind 且严格 `aud=ani-core`，错误 audience 拒绝。
- service 必须有非空且格式正确的 `permissions`；`credential_domain=platform` 的 service permissions 只能生成 platform permission，不能降级为 tenant。
- platform V2 tenant 为空；legacy platform TenantContext 仍是零 UUID。
- API Key V2 返回 credential ID，并从带 `SetDBTenant` 的 RLS 事务重读 revoked/expired/permissions；`touchLastUsed` 也必须自建相同 tenant transaction，跨租户更新为 0 行。
- API Key revoked/expired 为 Unauthenticated，rate limit 为 ResourceExhausted，DB/cache error 为 Unavailable，deadline 为 DeadlineExceeded。
- API Key/service permissions 缺 `scope:` 前缀、为空或格式非法时 fail closed，并映射为 `FailedPrecondition`/HTTP 503，不能形成 allow decision。
- Principal 任一字段不一致返回 `PRINCIPAL_MISMATCH`。
- Principal domain 不允许 required boundary 时返回 `CREDENTIAL_DOMAIN_MISMATCH` deny decision（HTTP 403），必须在 permission store 查询前短路；未知 boundary 返回 `InvalidArgument`（HTTP 500 `AUTHZ_CONTRACT_ERROR`）。
- `CheckPermissionV2` 请求结构校验 7 字段非空（Principal/Resource/Action/RequiredBoundary/OperationId/Credential/CredentialScheme）。
- domain/boundary 校验由 Gateway 和 auth-service 双重执行，两层职责不同，不互相替代。
- user permission 从 `users -> user_roles -> roles.permissions` 读取。
- evaluator 的 own scope 测试不替代 handler/store 的跨用户对象拒绝门禁。
- AuthClient、grpcAuthClient 和所有 fake 的 V2 接口编译闭合。
- Gateway mode 仍 off，B1 合并后不产生 V2 HTTP 流量。

#### 5.10.1 auth-service domain/boundary 单元测试

Gateway E2E 不够，因为 Gateway 会提前拒绝 tenant principal。必须直接测试 `CheckPermissionV2`，证明即使绕过 Gateway 前置检查，auth-service 仍然拒绝跨 domain 请求。

**`principalDomainAllowsBoundary` 组合用例**：

```go
func TestPrincipalDomainAllowsBoundary(t *testing.T) {
    cases := []struct {
        name      string
        domain    string
        tenantID  string
        boundary  string
        wantAllow bool
    }{
        {
            name:      "tenant to tenant",
            domain:    "tenant",
            tenantID:  uuid.NewString(),
            boundary:  "tenant",
            wantAllow: true,
        },
        {
            name:      "tenant to own",
            domain:    "tenant",
            tenantID:  uuid.NewString(),
            boundary:  "own",
            wantAllow: true,
        },
        {
            name:      "tenant to platform",
            domain:    "tenant",
            tenantID:  uuid.NewString(),
            boundary:  "platform",
            wantAllow: false,
        },
        {
            name:      "platform to platform",
            domain:    "platform",
            tenantID:  "",
            boundary:  "platform",
            wantAllow: true,
        },
        {
            name:      "platform to tenant",
            domain:    "platform",
            tenantID:  "",
            boundary:  "tenant",
            wantAllow: false,
        },
        {
            name:      "platform to own",
            domain:    "platform",
            tenantID:  "",
            boundary:  "own",
            wantAllow: false,
        },
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            principal := principalRecord{
                Domain:   tc.domain,
                TenantID: tc.tenantID,
            }

            got := principalDomainAllowsBoundary(
                principal,
                tc.boundary,
            )

            if got != tc.wantAllow {
                t.Fatalf("got %v, want %v", got, tc.wantAllow)
            }
        })
    }
}
```

**异常角色绑定短路测试**——模拟 tenant-domain user + 数据库 platform-admin wildcard permission + required_boundary=platform，必须返回拒绝，并证明 permission store 根本没有被调用：

```go
func TestCheckPermissionV2RejectsTenantDomainBeforePermissionLookup(t *testing.T) {
    // permissionStoreSpy 记录 Allows 调用次数；
    // allowed=true 模拟数据库意外绑定了 platform-admin 权限。
    permissions := &permissionStoreSpy{
        allowed: true,
    }

    service := newTestAuthService(t)
    service.permissions = permissions

    response, err := service.CheckPermissionV2(
        context.Background(),
        tenantPrincipalPlatformBoundaryRequest(t),
    )
    if err != nil {
        t.Fatal(err)
    }

    if response.GetAllowed() {
        t.Fatal("tenant principal must not enter platform boundary")
    }

    if response.GetReasonCode() != "CREDENTIAL_DOMAIN_MISMATCH" {
        t.Fatalf("reason = %q", response.GetReasonCode())
    }

    // 关键断言：permission store 调用次数必须为 0，
    // 证明错误身份域不会进入权限查询。
    if permissions.calls != 0 {
        t.Fatalf("permission store calls = %d, want 0", permissions.calls)
    }
}
```

`permissionStoreSpy` 实现 `PermissionStore` 接口，`Allows` 每次被调用时 `calls++`；`tenantPrincipalPlatformBoundaryRequest` 构造一个 tenant domain Principal + `required_boundary=platform` 的 `AuthorizationRequest`。最后 `calls == 0` 很重要，它证明错误身份域不会进入权限查询。

---

## 6. PR 4：AUTHZ-PILOT-C

### 6.1 目标与交付物

只为 `listQuotaMeta` 增加 `x-ani-authz`，生成 policy，并在 pilot allowlist 中启用 V2。其它 operation 无扩展或不在 allowlist时继续走旧 middleware。

### 6.2 步骤 C1：修改 Core OpenAPI

文件：`repo/api/openapi/v1.yaml`

```yaml
/admin/quota-meta:
  get:
    operationId: listQuotaMeta
    security:
      - BearerAuth: []
      - ApiKeyAuth: []
    x-ani-rbac-scope: "scope:quota:read"
    x-ani-authz:
      version: v1
      resource: quota
      action: read
      boundary: platform
      principal_kinds: [user]
```

重新运行 `make gen-gateway-authz` 后，registry 对应项应从 legacy 变为：

```go
"GET /api/v1/admin/quota-meta": {
    Source: PolicySourceGenerated, OperationID: "listQuotaMeta",
    Method: "GET", PathTemplate: "/api/v1/admin/quota-meta",
    SecurityAlternatives: []SecurityRequirement{
        {AllOf: []OpenAPISecurityScheme{OpenAPISecurityBearer}},
        {AllOf: []OpenAPISecurityScheme{OpenAPISecurityAPIKey}},
    },
    Version: "v1", Resource: "quota", Action: "read",
    Boundary: BoundaryPlatform,
    PrincipalKinds: []PrincipalKind{PrincipalUser},
},
```

### 6.3 步骤 C2：部署配置和唯一 allowlist

```yaml
env:
  - name: ANI_AUTH_MODE
    value: "auth_service"
  - name: GATEWAY_AUTHZ_POLICY_MODE
    value: "pilot"
  - name: GATEWAY_AUTHZ_PILOT_OPERATIONS
    value: "listQuotaMeta"
```

配置校验必须把 Functional MVP pilot 集合冻结为严格唯一集合，而不是只验证“配置项都存在”：

```go
var functionalMVPPilotOperations = map[string]struct{}{
    "listQuotaMeta": {},
}

func sameOperationSet(left, right map[string]struct{}) bool {
    if len(left) != len(right) { return false }
    for operationID := range left {
        if _, ok := right[operationID]; !ok { return false }
    }
    return true
}

func (c Config) Validate(registry Registry) error {
    if err := c.ValidateBase(); err != nil { return err }
    switch c.Mode {
    case ModeOff, ModeFull:
        // ValidateBase 已保证 allowlist 为空。
        return nil
    case ModePilot:
        if c.AuthMode != "auth_service" {
            return errors.New("pilot requires ANI_AUTH_MODE=auth_service")
        }
        if !sameOperationSet(c.PilotOperations, functionalMVPPilotOperations) {
            return errors.New("Functional MVP pilot operations must equal {listQuotaMeta}")
        }
    default:
        return errors.New("unsupported authz policy mode")
    }
    for operationID := range functionalMVPPilotOperations {
        policy, ok := registry.LookupOperation(operationID)
        if !ok || policy.Source != PolicySourceGenerated {
            return fmt.Errorf("pilot operation %q has no generated policy", operationID)
        }
    }
    return nil
}
```

这里保留 V4 迁移期既有的 Bearer/API Key OR 契约。`principal_kinds: [user]` 是主体类型限制，不是 credential scheme 删除：tenant API Key 仍可进入 V2 认证，随后因 `api_key` 不在允许主体类型中返回 403。这样不会把已有 API Key OpenAPI 契约静默收窄成 Bearer-only。

这里不是重新引入启动错误通道：B0 已经让 `middleware.Register` 返回 error，并由 `main.go` 在监听前处理。C 只是在同一入口把 B0 的 `ValidateBase` 增强为带 registry 的完整 `Config.Validate`，因此严格 pilot 校验仍发生在 `h.Spin()` 之前：

```go
func Register(h *server.Hertz, store GatewayStore) error {
    if store == nil { return errors.New("gateway middleware store is required") }
    registry := authz.CoreRegistry()
    cfg, err := authz.ConfigFromEnv()
    if err != nil { return err }
    if err := cfg.Validate(registry); err != nil { return err }
    registerChain(h, store, NewAuthClientFromEnv(), registry, cfg)
    return nil
}

func registerChain(
    h *server.Hertz, store GatewayStore, client AuthClient,
    registry authz.Registry, cfg authz.Config,
) {
    h.Use(
        RequestID(),
        ResolveAuthzPolicy(registry, cfg),
        AuthenticatePrincipal(client),
        AuthorizePrincipal(client),
        RateLimitByPrincipal(store),
        IdempotencyByPrincipal(store),
        AuditPrincipalDecision(),
    )
}

// main.go：B0 已接入；C 不改变调用位置，只让 Register 执行完整 Validate。
if err := middleware.Register(h, gatewayStore); err != nil {
    logger.Error("failed to configure gateway authz", "err", err)
    os.Exit(1)
}
```

必须测试：空 allowlist、额外 operation、拼写错误、off/full 携带 allowlist、dev+pilot、pilot operation 尚未 generated 均启动失败。

### 6.4 步骤 C3：generated 认证和授权

```go
type rawCredentialContext struct {
    Value  string
    Scheme authz.CredentialScheme
}

const rawCredentialContextKey = "ani.authz.raw_credential"

func SetRawCredentialForAuthz(c *app.RequestContext, value string, scheme authz.CredentialScheme) {
    c.Set(rawCredentialContextKey, rawCredentialContext{Value: value, Scheme: scheme})
}

func GetRawCredentialForAuthz(c *app.RequestContext) (string, authz.CredentialScheme, error) {
    value, ok := c.Get(rawCredentialContextKey)
    if !ok { return "", "", errors.New("authorization credential missing") }
    credential, ok := value.(rawCredentialContext)
    if !ok || credential.Value == "" { return "", "", errors.New("authorization credential invalid") }
    return credential.Value, credential.Scheme, nil
}

func ClearRawCredentialForAuthz(c *app.RequestContext) {
    c.Set(rawCredentialContextKey, rawCredentialContext{})
}

func credentialFromRequest(c *app.RequestContext, policy authz.Policy) (string, authz.CredentialScheme, error) {
    authHeader := strings.TrimSpace(string(c.GetHeader("Authorization")))
    apiKey := strings.TrimSpace(string(c.GetHeader("X-API-Key")))
    bearer := ""
    if strings.HasPrefix(authHeader, "Bearer ") { bearer = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer ")) }
    if bearer != "" && apiKey != "" { return "", "", errors.New("multiple credentials are not supported") }
    if bearer != "" {
        scheme := authz.CredentialBearer
        if sandboxtoken.LooksLike(bearer) { scheme = authz.CredentialSandboxToken }
        if !policyAllowsCredentialScheme(policy, scheme) { return "", "", errors.New("credential scheme not allowed") }
        return bearer, scheme, nil
    }
    if apiKey != "" {
        if !policyAllowsCredentialScheme(policy, authz.CredentialAPIKey) {
            return "", "", errors.New("credential scheme not allowed")
        }
        return apiKey, authz.CredentialAPIKey, nil
    }
    return "", "", errors.New("credential required")
}

func policyAllowsCredentialScheme(policy authz.Policy, scheme authz.CredentialScheme) bool {
    required := authz.OpenAPISecurityBearer
    if scheme == authz.CredentialAPIKey { required = authz.OpenAPISecurityAPIKey }
    for _, alternative := range policy.SecurityAlternatives {
        if len(alternative.AllOf) == 1 && alternative.AllOf[0] == required { return true }
    }
    return false
}

func validatePrincipalAgainstPolicy(principal authz.Principal, policy authz.Policy) error {
    if err := principal.Validate(); err != nil { return err }
    if !policy.AllowsPrincipalKind(principal.Kind) { return errors.New("principal kind denied") }
    if !policyAllowsCredentialScheme(policy, principal.CredentialScheme) { return errors.New("credential scheme denied") }
    if !authz.DomainAllowsBoundary(principal, policy.Boundary) { return errors.New("credential domain denied") }
    return nil
}

func InstallGeneratedPrincipalContext(
    ctx context.Context, c *app.RequestContext, principal authz.Principal,
) (context.Context, error) {
    if err := principal.Validate(); err != nil { return ctx, err }

    // 兼容现有 handler 的 Hertz request context；V2 不写入 legacy roles。
    tenantID := principal.TenantID
    userID := principal.SubjectID
    setTenantContext(c, tenantID, userID, nil, string(principal.CredentialDomain))
    c.Set("principal_kind", string(principal.Kind))
    c.Set("credential_scheme", string(principal.CredentialScheme))

    if principal.CredentialDomain == authz.DomainPlatform {
        // V4：platform 不属于单一租户，不向 Go context 注入伪造零 UUID tenant。
        return ctx, nil
    }
    tenantUUID, err := uuid.Parse(principal.TenantID)
    if err != nil || tenantUUID == uuid.Nil {
        return ctx, errors.New("generated tenant principal has invalid tenant id")
    }
    tenantContext := &types.TenantContext{TenantID: tenantUUID}
    // user/API Key 的 subject 是 UUID；service subject 可以是签名 service ID，
    // 此时保留 zero UserID，服务身份仍以 Principal 作为权威主体。
    if principal.SubjectID != "" {
        if subjectUUID, parseErr := uuid.Parse(principal.SubjectID); parseErr == nil {
            tenantContext.UserID = subjectUUID
        } else if principal.Kind == authz.PrincipalUser || principal.Kind == authz.PrincipalAPIKey {
            return ctx, errors.New("generated user credential has invalid subject id")
        }
    }
    return types.WithTenant(ctx, tenantContext), nil
}

func AuthenticatePrincipal(client AuthClient) app.HandlerFunc {
    return func(ctx context.Context, c *app.RequestContext) {
        resolved, err := GetResolvedPolicy(c)
        if err != nil { respond503(c, "authz policy context missing"); return }
        if resolved.Source == authz.PolicySourcePublic { c.Next(ctx); return }
        if resolved.Source == authz.PolicySourceLegacy {
            authenticateLegacy(ctx, c, client) // 完整保留旧 header/parser/RPC 语义
            return
        }
        credential, scheme, err := credentialFromRequest(c, resolved.Policy)
        if err != nil { respond401(c, "invalid credential"); return }
        if scheme == authz.CredentialSandboxToken {
            claims, err := sandboxtoken.Parse(
                credential, sandboxtoken.SigningKey(), time.Now().UTC(),
            )
            if err != nil { respond401(c, "invalid sandbox credential"); return }
            principal := authz.Principal{
                Kind: authz.PrincipalSandbox,
                CredentialScheme: authz.CredentialSandboxToken,
                CredentialDomain: authz.DomainSandbox,
                TenantID: claims.TenantID,
                SubjectID: sandboxtoken.SandboxActorUID,
                SandboxClaims: &authz.SandboxClaims{
                    TenantID: claims.TenantID, InstanceID: claims.InstanceID,
                },
            }
            if err := validatePrincipalAgainstPolicy(principal, resolved.Policy); err != nil {
                respond403(c, "principal not allowed by operation policy"); return
            }
            setSandboxContext(c, claims) // 复用现有 capability/instance 上下文
            SetPrincipal(c, principal)
            ctx, err = InstallGeneratedPrincipalContext(ctx, c, principal)
            if err != nil { respond503(c, "generated identity context unavailable"); return }
            c.Next(ctx)
            return
        }
        principalPB, err := client.ValidatePrincipal(ctx, credential, scheme)
        if err != nil { writeAuthRPCError(c, err); return }
        principal, err := authz.PrincipalFromProto(principalPB)
        if err != nil { respond503(c, "auth service returned invalid principal"); return }
        if err := validatePrincipalAgainstPolicy(principal, resolved.Policy); err != nil {
            respond403(c, "principal not allowed by operation policy"); return
        }
        SetPrincipal(c, principal)
        ctx, err = InstallGeneratedPrincipalContext(ctx, c, principal)
        if err != nil { respond503(c, "generated identity context unavailable"); return }
        SetRawCredentialForAuthz(c, credential, scheme) // request-local only；禁止日志读取
        c.Next(ctx)
    }
}
```

generated 认证成功的完成条件不只是 `SetPrincipal`：tenant user/API Key/service 和 sandbox 必须同时拥有 Hertz 的 `tenant_id/user_id/scope` 字段和可供 RLS store 使用的 `types.TenantContext`；platform Principal 只设置 platform scope、subject 和 Principal context，不设置零 UUID tenant。这里的 `types.TenantContext` 只是 tenant RLS 兼容投影，`Roles` 必须为空，身份权威仍是 `Principal`。后续 platform handler 若需要访问跨租户资源，必须显式使用 Principal 与目标 tenant 边界，不能调用依赖 tenant context 的 tenant store。当前 pilot 的 `listQuotaMeta -> QuotaAdminService.ListQuotaMeta -> WithPlatformTx` 是 platform-safe 路径，C PR 必须用测试冻结它不调用 `types.FromContext`。

```go
func TestInstallGeneratedPrincipalContext(t *testing.T) {
    tenantID := uuid.New()
    userID := uuid.New()
    tenantRequest := &app.RequestContext{}
    tenantCtx, err := InstallGeneratedPrincipalContext(
        context.Background(), tenantRequest, authz.Principal{
            Kind: authz.PrincipalUser, CredentialScheme: authz.CredentialBearer,
            CredentialDomain: authz.DomainTenant,
            TenantID: tenantID.String(), SubjectID: userID.String(),
        },
    )
    if err != nil { t.Fatal(err) }
    projected, ok := types.TryFromContext(tenantCtx)
    if !ok || projected.TenantID != tenantID || projected.UserID != userID || len(projected.Roles) != 0 {
        t.Fatalf("tenant projection = %#v, present=%v", projected, ok)
    }
    if got := tenantRequest.GetString("tenant_id"); got != tenantID.String() { t.Fatalf("tenant_id = %q", got) }
    if got := tenantRequest.GetString("user_id"); got != userID.String() { t.Fatalf("user_id = %q", got) }
    if got := tenantRequest.GetString("scope"); got != "tenant" { t.Fatalf("scope = %q", got) }

    platformRequest := &app.RequestContext{}
    platformCtx, err := InstallGeneratedPrincipalContext(
        context.Background(), platformRequest, authz.Principal{
            Kind: authz.PrincipalUser, CredentialScheme: authz.CredentialBearer,
            CredentialDomain: authz.DomainPlatform, SubjectID: userID.String(),
        },
    )
    if err != nil { t.Fatal(err) }
    if _, ok := types.TryFromContext(platformCtx); ok {
        t.Fatal("platform principal must not install tenant context")
    }
    if got := platformRequest.GetString("tenant_id"); got != "" { t.Fatalf("platform tenant_id = %q", got) }
    if got := platformRequest.GetString("scope"); got != "platform" { t.Fatalf("scope = %q", got) }
}
```

```go
func AuthorizePrincipal(client AuthClient) app.HandlerFunc {
    return func(ctx context.Context, c *app.RequestContext) {
        resolved, err := GetResolvedPolicy(c)
        if err != nil { respond503(c, "authz policy context missing"); return }
        if resolved.Source == authz.PolicySourcePublic { c.Next(ctx); return }
        if resolved.Source == authz.PolicySourceLegacy {
            authorizeLegacy(ctx, c, client) // 只调用旧 CheckPermission
            return
        }
        principal, err := GetPrincipal(c)
        if err != nil { respond503(c, "principal context missing"); return }
        if principal.Kind == authz.PrincipalSandbox {
            // V4 冻结：sandbox 由 Gateway 本地 capability + instance binding 授权，
            // 不把 sandbox credential 发送给 CheckPermissionV2。
            if !sandboxTokenAllows(c, string(c.Path())) {
                respond403(c, "sandbox capability denied"); return
            }
            c.Next(ctx)
            return
        }
        credential, scheme, err := GetRawCredentialForAuthz(c)
        if err != nil { respond503(c, "authorization credential context missing"); return }
        defer ClearRawCredentialForAuthz(c)
        decision, err := client.CheckPermissionV2(ctx, &authv1.AuthorizationRequest{
            Principal: principal.WithoutLegacyRoles().Proto(),
            Resource: resolved.Policy.Resource, Action: resolved.Policy.Action,
            RequiredBoundary: string(resolved.Policy.Boundary), OperationId: resolved.Policy.OperationID,
            Credential: credential, CredentialScheme: string(scheme),
        })
        if err != nil { writeAuthRPCError(c, err); return }
        if !decision.Allowed { respond403(c, decision.ReasonCode); return }
        c.Next(ctx)
    }
}
```

这里没有 `authorizeLegacy` fallback：一旦 resolved source 是 generated，V2 deny/error 都在当前分支终止。

### 6.5 步骤 C4：quota-meta E2E 测试

```go
type authRPCErrorStage string

const (
    authRPCErrorNone     authRPCErrorStage = ""
    authRPCErrorValidate authRPCErrorStage = "validate_principal"
    authRPCErrorCheck    authRPCErrorStage = "check_permission_v2"
)

type authRPCCallCounts struct {
    ValidatePrincipal int
    CheckPermissionV2 int
    ValidateToken     int
    CheckPermission   int
}

type fakeAuthBehavior struct {
    Allowed    bool
    ErrorStage authRPCErrorStage
    Err        error
}

func TestQuotaMetaPilotAuthorizationMatrix(t *testing.T) {
    cases := []struct {
        name       string
        credential testCredential
        behavior   fakeAuthBehavior
        wantStatus int
        wantCalls  authRPCCallCounts
    }{
        {
            name: "no credential", credential: none(),
            wantStatus: http.StatusUnauthorized,
            wantCalls:  authRPCCallCounts{},
        },
        {
            name: "tenant user", credential: tenantUser(),
            wantStatus: http.StatusForbidden,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
        },
        {
            name: "tenant admin", credential: tenantAdmin(),
            wantStatus: http.StatusForbidden,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
        },
        {
            name: "tenant api key", credential: tenantAPIKey(),
            wantStatus: http.StatusForbidden,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
        },
        {
            name: "platform service", credential: platformService(),
            wantStatus: http.StatusForbidden,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
        },
        {
            name: "platform user no permission", credential: platformUser(),
            behavior:   fakeAuthBehavior{Allowed: false},
            wantStatus: http.StatusForbidden,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
        },
        {
            name: "platform admin empty tenant", credential: platformAdmin(),
            behavior:   fakeAuthBehavior{Allowed: true},
            wantStatus: http.StatusOK,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
        },
        {
            name: "credential revoked between V2 calls", credential: platformAdmin(),
            behavior: fakeAuthBehavior{
                ErrorStage: authRPCErrorCheck,
                Err: status.Error(codes.Unauthenticated, "revoked"),
            },
            wantStatus: http.StatusUnauthorized,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
        },
        {
            name: "credential rate limited", credential: platformAdmin(),
            behavior: fakeAuthBehavior{
                ErrorStage: authRPCErrorValidate,
                Err: status.Error(codes.ResourceExhausted, "limited"),
            },
            wantStatus: http.StatusTooManyRequests,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
        },
        {
            name: "validate auth service unavailable", credential: platformAdmin(),
            behavior: fakeAuthBehavior{
                ErrorStage: authRPCErrorValidate,
                Err: status.Error(codes.Unavailable, "down"),
            },
            wantStatus: http.StatusServiceUnavailable,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
        },
        {
            name: "check auth service unavailable", credential: platformAdmin(),
            behavior: fakeAuthBehavior{
                ErrorStage: authRPCErrorCheck,
                Err: status.Error(codes.Unavailable, "down"),
            },
            wantStatus: http.StatusServiceUnavailable,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
        },
        {
            name: "auth service deadline", credential: platformAdmin(),
            behavior: fakeAuthBehavior{
                ErrorStage: authRPCErrorCheck,
                Err: status.Error(codes.DeadlineExceeded, "timeout"),
            },
            wantStatus: http.StatusGatewayTimeout,
            wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            gateway, fake := newPilotGateway(t, tc.credential, tc.behavior)
            response := doGET(t, gateway, "/api/v1/admin/quota-meta")
            if response.StatusCode != tc.wantStatus { t.Fatalf("got %d, want %d", response.StatusCode, tc.wantStatus) }
            gotCalls := authRPCCallCounts{
                ValidatePrincipal: fake.ValidatePrincipalCalls,
                CheckPermissionV2: fake.CheckPermissionV2Calls,
                ValidateToken:     fake.ValidateTokenCalls,
                CheckPermission:   fake.LegacyCheckPermissionCalls,
            }
            if gotCalls != tc.wantCalls {
                t.Fatalf("RPC calls = %+v, want %+v", gotCalls, tc.wantCalls)
            }
        })
    }
}
```

`newPilotGateway` 必须显式设置 `ANI_AUTH_MODE=auth_service`，并在测试结束恢复环境；另加负向启动测试证明 `ANI_AUTH_MODE=dev + policy mode=pilot` 无法构造 Gateway。fake 必须按 `ErrorStage` 在对应 RPC 内注错：`authRPCErrorValidate` 在 `ValidatePrincipal` 返回错误，`authRPCErrorCheck` 先让认证成功、再在 `CheckPermissionV2` 返回错误；禁止用一个无阶段的共享 `authErr`，否则无法证明第二个 RPC 是否真正发生。每个用例都精确比较四个计数，既冻结 V2 短路位置，也证明 generated 请求没有调用 legacy RPC。

回滚测试：

```go
func TestQuotaMetaModeOffUsesLegacy(t *testing.T) {
    gateway, fake := newGatewayWithMode(t, authz.ModeOff)
    _ = doGET(t, gateway, "/api/v1/admin/quota-meta")
    gotCalls := authRPCCallCounts{
        ValidatePrincipal: fake.ValidatePrincipalCalls,
        CheckPermissionV2: fake.CheckPermissionV2Calls,
        ValidateToken:     fake.ValidateTokenCalls,
        CheckPermission:   fake.LegacyCheckPermissionCalls,
    }
    wantCalls := authRPCCallCounts{ValidateToken: 1, CheckPermission: 1}
    if gotCalls != wantCalls {
        t.Fatalf("RPC calls = %+v, want %+v", gotCalls, wantCalls)
    }
}
```

### 6.6 PR 4 完成定义

- 只有 `listQuotaMeta` 的 registry source 从 legacy 变 generated。
- pilot allowlist 必须严格等于 `{listQuotaMeta}`；空集、额外项、off/full 带 allowlist、dev+pilot 均启动失败。
- platform-admin V2 Principal tenant 为空并返回 200。
- generated tenant/sandbox 请求同时设置 Hertz `tenant_id/user_id/scope` 和无 legacy roles 的 `types.TenantContext`；platform 请求不得注入零 UUID tenant，`listQuotaMeta` 数据面保持 platform-safe。
- 无凭证/撤销 401、tenant API Key 因 `principal_kinds: [user]` 返回 403、其它主体或策略不匹配 403、限流 429、auth-service 故障 503、deadline 504。
- generated 的 401/403/429/503/504 全部不回退旧 RPC。
- sandbox bearer 继续本地验签和 capability/instance binding；不调用 V2，也不把 sandbox error 映射成 auth-service 503。
- 全部 E2E 显式运行 `ANI_AUTH_MODE=auth_service`，按认证/授权注错阶段精确断言每个场景的 ValidatePrincipal/CheckPermissionV2/legacy RPC 调用次数；不能只对成功场景计数。
- mode 切回 off 后 `quota-meta` 整体回到旧 middleware。
- 只声明 local/logic verified。

---

## 7. 四 PR 测试矩阵

| 场景 | A | B0 | B1 | C |
|---|---:|---:|---:|---:|
| 缺扩展自动 legacy（含新 route） | 必须 | 回归 | 回归 | 回归 |
| `security: []` public | 必须 | 回归 | 回归 | 回归 |
| `security: []` 与 `x-ani-authz` 冲突 | 必须失败 | - | - | 回归 |
| 单凭证 OR 保真 | 必须 | - | - | 回归 |
| 多凭证 AND 构建期拒绝 | 必须失败 | - | - | 回归 |
| mode off 不切流 | - | 必须 | 必须 | 回滚验证 |
| `ANI_AUTH_MODE=dev` 禁止 pilot/full | - | 必须 | 回归 | 必须 |
| platform 零 UUID legacy | - | 必须 | 必须 | 回归 |
| platform 空 tenant V2 | - | - | 必须 | E2E |
| user JWT 签发端写入 `principal_kind`+`credential_domain`（tenant/platform） | - | 必须 | - | 单测 |
| 新 user JWT + V2 路径 `CredentialDomain` 非空校验通过 | - | - | 必须 | E2E |
| 旧 user JWT（无 `credential_domain`）+ V2 路径 fail | - | - | 必须 | 单测 |
| 新 user JWT + `roles` 透传 + legacy 路径行为不变 | - | 回归 | - | 回归 |
| quota-meta Bearer/API Key OR 保持且 API Key 403 | - | - | - | 必须 |
| Principal kind/domain/ID/sandbox 严格校验 | - | 必须 | 必须 | E2E |
| API Key B0 tenant 粗粒度、B1 credential ID | - | 必须 | 必须 | 回归 |
| API Key query/touch 均使用 tenant RLS transaction | - | - | 必须 | 回归 |
| service JWT `aud=ani-core` | - | - | 必须 | E2E |
| service `permissions` 非空及 tenant/platform boundary | - | - | 必须 | 回归 |
| API Key/service 非规范 `permissions` fail closed | - | - | 必须 | 回归 |
| sandbox 本地验签与 capability binding | - | 回归 | 回归 | 回归 |
| V2 不读 `scope`/`roles`，权威权限从 DB/`permissions` 读取 | - | - | 必须 | E2E |
| 401/403/429/503/504 固定映射 | - | - | 必须 | 必须 |
| own 权限与资源所有权分离 | - | - | 必须 | 回归 |
| auth-service domain/boundary 短路（domain mismatch deny，permission store 0 次） | - | - | 必须 | 单测 |
| unknown boundary → InvalidArgument → HTTP 500 | - | - | 必须 | 单测 |
| deny/error no-fallback | - | - | 单测 | 必须 |
| generated tenant/sandbox 安装 data context，platform 不安装 tenant context | - | - | helper 单测 | 必须 |
| 每个 generated 场景精确冻结 V2/legacy RPC 调用次数 | - | - | 客户端单测 | 必须 |
| pilot allowlist 严格唯一且启动前校验 | - | - | - | 必须 |
| quota-meta 权限矩阵 | - | - | service 单测 | 必须 |

统一验收命令：

```bash
cd repo
python scripts/generate_gateway_authz_test.py
python scripts/validate_gateway_authz_drift.py
go test -count=1 ./services/ani-gateway/internal/authz
go test -count=1 ./services/ani-gateway/internal/middleware
go test -count=1 ./services/ani-gateway/internal/router
go test -count=1 ./services/auth-service/internal/service
python scripts/validate_auth_gateway_contract.py
python scripts/validate_core_api_compatibility.py
make test
make validate-architecture
git diff --check
```

Proto 变更后追加：

```bash
cd repo
make gen-proto
git diff --exit-code -- pkg/generated
```

若生成物命令本身会更新文件，应由 CI 使用临时工作树或 drift script 比较，不能在验证目标中悄悄修改工作区。

---

## 8. 风险、取舍与回滚

| 风险/取舍 | 已确认策略 | 缓解/回滚 |
|---|---|---|
| 新 route 漏写 `x-ani-authz` 也会变 legacy | 新增 route 必须加 `x-ani-authz`，否则拒绝合并；存量 route 缺扩展仍自动走 legacy（兼容优先） | CI 门禁区分存量和新 route；存量 legacy inventory 由 generator 产出，指标记录 legacy operation ID |
| `security: []` 吞掉扩展或 AND 被静默放宽 | public 与扩展冲突构建失败；多凭证 AND 构建失败 | A 的生成器负例和 drift 门禁 |
| generated policy 标错 boundary | generator 只能校验格式，不能证明业务语义 | C 只迁移一个只读 platform route；正反矩阵审查 |
| mode 配错导致扩大 pilot | allowlist 严格等于唯一集合，并在监听前校验 | 非法配置启动失败；回滚 mode=off |
| `ANI_AUTH_MODE=dev` 绕过 V2 | dev 只允许 policy mode=off；pilot/full 启动失败 | E2E 强制 `auth_service`；启动负例覆盖 |
| platform 空 tenant 破坏横切 | 使用规范 Principal identity key，不依赖 tenant ID | B0 legacy view 回归；mode off 仍可回旧链 |
| generated tenant route 缺 data context | 认证成功后统一安装 Hertz 字段和无 legacy roles 的 `types.TenantContext` | tenant store/RLS 集成测试；platform 明确禁止伪 tenant |
| B0 API Key identity 碰撞 | B0 明确使用 tenant 级 legacy key，不伪造 credential ID | B1 返回稳定 credential ID 后切 per-key；记录迁移窗口 |
| API Key `last_used_at` 绕过或缺失 RLS | query 和 touch 都使用 tenant transaction + `SetDBTenant` | 同租户成功、跨租户 0 行、未设置 tenant 的失败测试 |
| service JWT audience 与 Core 契约冲突 | service 严格校验 `aud=ani-core`，不接受 `ani-auth-service` | JWT string/array audience 正反测试 |
| V2 信任 Gateway 自报身份 | auth-service 重验 credential 并逐字段比较 | mismatch deny；不提供 bypass flag |
| raw credential 泄漏 | request-local 保存、用后清理、日志禁止读取 | 脱敏测试；关闭 pilot 并修复 |
| V2 错误被粗暴映射 503 | 固定 gRPC→HTTP 表，区分 401/429/503/504 | revoked/rate-limit/backend/deadline 测试；默认 fail closed |
| 非规范 API Key/service `permissions` 被误解析成权限 | 只接受 `scope:<resource>:<action>`，非法权威数据返回 FailedPrecondition | normalize/evaluator 共用语法；缺前缀和脏数据负例 |
| `own` 被误当成 tenant 全量权限 | evaluator 只判 scope；handler/store/RLS 判 tenant 与 owner | own operation 必须有同租户不同 owner 拒绝测试 |
| B1 数据库 evaluator 出错 | 统一返回稳定 Unavailable，不暴露 SQL | C 返回 503，不回退 legacy |
| 运行时接口或 helper 未闭合 | B0 明确定义 registry/context/handler 接口；B1 明确 AuthClient V2 与生成物 | 四 PR 各自编译测试；mode off 调用计数为零 |
| 四 PR 滚动升级 | A/B0/B1 默认不切流 | C 仅在全部 auth-service V2 ready 后 pilot |

---

## 9. 实施顺序

### PR 1：AUTHZ-POLICY-A

1. 新增 `Policy` 类型和 `Registry` lookup。
2. 实现 generator 分类：public/generated/legacy；缺扩展自动生成 legacy。
3. 实现 V4 `x-ani-authz` validator、单凭证 OR 保真和多凭证 AND 构建期拒绝。
4. 拒绝 `security: []` 与 `x-ani-authz` 的矛盾声明。
5. 生成单一 `zz_generated_core_policies.go`，覆盖 public/generated/legacy。
6. 增加 FullPath 规范化、determinism、route coverage 和 drift 门禁。
7. 不修改 `quota-meta`，验证无运行时变化。

### PR 2：AUTHZ-COMPAT-B0

1. 新增严格 `Principal.Validate`、`IdentityKey` 和独立 `LegacyPrincipalView`。
2. 明确 B0 API Key tenant 级 legacy identity，不伪造 credential ID。
3. 新增 mode/off/pilot/full、`ANI_AUTH_MODE` 交互和 allowlist 基础解析。
4. 定义 `Registry`、`ResolvedPolicy`、request context helper 以及 Auth/RBAC 重构入口。
5. 注册 policy resolver，但 mode 默认 off；旧 Auth/RBAC 继续只调旧 RPC。
6. 改造 rate limit/idempotency/audit 使用规范或 legacy identity key。
7. 在监听前配置校验，跑完整旧功能兼容矩阵。

### PR 3：AUTHZ-CONTRACT-B1

1. additive 修改 Proto 并生成代码，补齐 Gateway/Auth Service 两侧接口。
2. 扩展 JWT principal kind 兼容解析，service 严格校验 `aud=ani-core`。
3. API Key validation 返回稳定 credential ID，并拆分重验副作用；query 和 `touchLastUsed` 都在 tenant RLS transaction 中执行。
4. 实现 `ValidatePrincipal` 及 invalid/revoked/rate-limit/backend/deadline 错误分类。
5. 实现 user/API Key/service 权威 evaluator，严格拒绝非规范 scope，并保留 own 的数据面边界。
6. 实现 `CheckPermissionV2` 重验、一致性比较和固定 HTTP 映射。
7. Gateway V2 client 编译接线；mode 仍 off，并验证 generated 成功路径调用两个 V2 RPC。

### PR 4：AUTHZ-PILOT-C

1. 给 `listQuotaMeta` 增加冻结的 V4 `x-ani-authz`。
2. 重生成 Core registry，确认该 operation 从 legacy 变为 generated。
3. 配置 `ANI_AUTH_MODE=auth_service`、pilot 和严格唯一 operation allowlist。
4. 在 Gateway 监听前校验配置并接通 ValidatePrincipal/CheckPermissionV2；generated tenant/sandbox 安装 data context，platform 保持无 tenant context。
5. 完成 401/403/429/503/504、no-fallback、横切、逐场景精确 RPC 调用计数与回滚测试。
6. 归档 local/隔离 CI evidence。

---

计划文件创建不代表实现完成，不应修改当前 Sprint 状态。提交、PR 和合入必须另行取得人工确认。
