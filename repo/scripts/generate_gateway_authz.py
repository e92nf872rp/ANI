#!/usr/bin/env python3
"""从 Core OpenAPI 生成 Gateway authz policy 注册表（AUTHZ-POLICY-A）。

唯一事实来源是 api/openapi/v1.yaml：
- 显式 security: []            → public（不得同时声明 x-ani-authz）
- 带 x-ani-authz 扩展          → generated（严格 5 字段校验）
- 无 x-ani-authz 的既有/新 operation → legacy（兼容优先，不维护独立 inventory）

输出 services/ani-gateway/internal/authz/zz_generated_core_policies.go。
同一输入重复生成字节一致；drift 门禁由 validate_gateway_authz_drift.py 承载。
"""

from __future__ import annotations

import argparse
import json
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_INPUT = ROOT / "api/openapi/v1.yaml"
DEFAULT_OUTPUT = ROOT / "services/ani-gateway/internal/authz/zz_generated_core_policies.go"
DEFAULT_TARGET_REGISTRY = ROOT / "api/openapi/operation-registry.v1.json"
DEFAULT_REPLACEMENT_MANIFEST = ROOT / "api/openapi/iam-replacement.v1.yaml"
ACCEPTED_MAIN_COMMIT = "bde4ea72b5a91cd43cc271dd44c09ff262c637e5"
ACCEPTED_MAIN_TREE = "3595d1b7cec729adf00a5bdc67f655e10e4f3bb2"
ACCEPTED_MAIN_OPENAPI = "repo/api/openapi/v1.yaml"

HTTP_METHODS = {"get", "post", "put", "patch", "delete", "head", "options"}
VALID_BOUNDARIES = {"own", "tenant", "platform"}
VALID_PRINCIPAL_KINDS = {"user", "service", "api_key", "sandbox"}
VALID_SECURITY_SCHEMES = {"BearerAuth", "ApiKeyAuth", "ConsoleRefreshCookie", "BossRefreshCookie"}

# 根级 infrastructure 端点：注册在 Hertz 根组，是 Core OpenAPI server prefix 的已知例外。
ROOT_INFRASTRUCTURE_PATHS = {"/healthz", "/readyz"}

# core-v1-compatibility-baseline.yaml 将下列 operation 的 operation_id 冻结为空串，
# 修改 operationId 属于 v1 破坏性变更，因此不能回填 v1.yaml。
# 生成器按 (method, openapi_path) 派生稳定 operationId；新 operation 缺
# operationId 且不在本表时立即失败，禁止静默猜测。
DERIVED_OPERATION_IDS = {
    ("POST", "/auth/refresh"): "refreshToken",
    ("GET", "/branding"): "getBranding",
    ("GET", "/tasks/{task_id}"): "getTask",
}


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


def load_spec(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        spec = yaml.safe_load(handle)
    if not isinstance(spec, dict) or not isinstance(spec.get("paths"), dict):
        raise ValueError(f"{path} must be an OpenAPI document with a paths object")
    return spec


def load_target_operations(path: Path) -> dict[str, dict[str, Any]]:
    with path.open(encoding="utf-8") as handle:
        registry = json.load(handle)
    operations = registry.get("operations") if isinstance(registry, dict) else None
    if not isinstance(operations, list):
        raise ValueError(f"{path} must contain an operations array")
    by_id: dict[str, dict[str, Any]] = {}
    for index, operation in enumerate(operations):
        if not isinstance(operation, dict):
            raise ValueError(f"{path} operations[{index}] must be an object")
        operation_id = operation.get("operation_id")
        if not isinstance(operation_id, str) or not operation_id:
            raise ValueError(f"{path} operations[{index}] missing operation_id")
        if operation_id in by_id:
            raise ValueError(f"{path} contains duplicate operation_id {operation_id}")
        by_id[operation_id] = operation
    return by_id


def load_compatibility_policies(path: Path) -> dict[tuple[str, str], ParsedPolicy]:
    with path.open(encoding="utf-8") as handle:
        manifest = yaml.safe_load(handle)
    if not isinstance(manifest, dict):
        raise ValueError(f"{path} must contain a YAML object")
    source = manifest.get("source")
    if not isinstance(source, dict):
        raise ValueError(f"{path} missing source object")
    commit = source.get("commit")
    core_openapi = source.get("core_openapi")
    if not isinstance(commit, str) or not commit or not isinstance(core_openapi, str) or not core_openapi:
        raise ValueError(f"{path} source must contain commit and core_openapi")
    spec = load_git_openapi(commit, core_openapi, "compatibility")
    return {(policy.method, policy.path_template): policy for policy in collect_policies(spec)}


def load_git_openapi(
    commit: str,
    core_openapi: str,
    label: str,
    expected_tree: str = "",
) -> dict[str, Any]:
    try:
        if expected_tree:
            actual_tree = subprocess.run(
                ["git", "-C", str(ROOT.parent), "rev-parse", f"{commit}^{{tree}}"],
                check=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            ).stdout.strip()
            if actual_tree != expected_tree:
                raise ValueError(f"{label} tree is {actual_tree}, want {expected_tree}")
        text = subprocess.run(
            ["git", "-C", str(ROOT.parent), "show", f"{commit}:{core_openapi}"],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        ).stdout
    except subprocess.CalledProcessError as error:
        detail = error.stderr.strip() or str(error)
        raise ValueError(f"cannot read {label} Core OpenAPI {commit}:{core_openapi}: {detail}") from error
    spec = yaml.safe_load(text)
    if not isinstance(spec, dict) or not isinstance(spec.get("paths"), dict):
        raise ValueError(f"{label} Core OpenAPI {commit}:{core_openapi} is invalid")
    return spec


def load_accepted_main_compatibility_policies() -> dict[tuple[str, str], ParsedPolicy]:
    spec = load_git_openapi(
        ACCEPTED_MAIN_COMMIT,
        ACCEPTED_MAIN_OPENAPI,
        "accepted main",
        ACCEPTED_MAIN_TREE,
    )
    return {(policy.method, policy.path_template): policy for policy in collect_policies(spec)}


def load_core_compatibility_policies(path: Path) -> dict[tuple[str, str], ParsedPolicy]:
    # The replacement manifest pins the pre-Direct-P2 source. Overlay the exact,
    # human-accepted main snapshot so routes added after that source retain their
    # current Core auth behavior while IAM_TARGET_MODE is disabled.
    policies = load_compatibility_policies(path)
    for route, policy in load_accepted_main_compatibility_policies().items():
        policies.setdefault(route, policy)
    return policies


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


def validate_target_operation(
    operation_id: str,
    openapi_path: str,
    method: str,
    extension: Any,
    public: bool,
    target_operation: dict[str, Any] | None,
) -> None:
    if target_operation is None:
        raise ValueError(f"Direct P2 operation {operation_id} missing from target registry")
    expected_classification = "public" if public else "authorized" if extension is not None else "authenticated"
    expected = {
        "operation_id": operation_id,
        "method": method.upper(),
        "path": openapi_path,
        "auth_classification": expected_classification,
        "iam_decision": {
            "public": "none",
            "authenticated": "validate_principal",
            "authorized": "check_permission",
        }[expected_classification],
        "iam_decision_calls": 0 if expected_classification == "public" else 1,
    }
    mismatches = {
        field: (target_operation.get(field), value)
        for field, value in expected.items()
        if target_operation.get(field) != value
    }
    if mismatches:
        raise ValueError(f"Direct P2 operation {operation_id} target registry mismatch: {mismatches}")


def classify(
    spec: dict[str, Any],
    openapi_path: str,
    method: str,
    operation: dict[str, Any],
    target_operation: dict[str, Any] | None = None,
    compatibility_policy: ParsedPolicy | None = None,
) -> ParsedPolicy:
    operation_id = operation.get("operationId")
    if not isinstance(operation_id, str) or not operation_id:
        # 3 个 baseline 冻结 operation_id='' 的既有 operation：确定性派生，见 DERIVED_OPERATION_IDS。
        derived = DERIVED_OPERATION_IDS.get((method.upper(), openapi_path))
        if derived is None:
            raise ValueError(f"{method.upper()} {openapi_path} missing operationId (not in DERIVED_OPERATION_IDS)")
        operation_id = derived
    security_raw = effective_security(spec, operation)
    extension = operation.get("x-ani-authz")
    path_template = gateway_path(openapi_path)
    if target_operation is not None:
        validate_target_operation(
            operation_id,
            openapi_path,
            method,
            extension,
            security_raw == [],
            target_operation,
        )
    if compatibility_policy is not None:
        return ParsedPolicy(
            compatibility_policy.source,
            operation_id,
            method.upper(),
            path_template,
            compatibility_policy.security,
            compatibility_policy.version,
            compatibility_policy.resource,
            compatibility_policy.action,
            compatibility_policy.boundary,
            compatibility_policy.principal_kinds,
        )
    if security_raw == []:
        if extension is not None:
            raise ValueError(f"{method.upper()} {openapi_path} public operation must not define x-ani-authz")
        return ParsedPolicy("public", operation_id, method.upper(), path_template, ())
    security = parse_security(security_raw)
    if target_operation is not None:
        # Direct P2 authorization is owned by the target registry. Keeping the
        # Core policy source legacy preserves disabled-mode authentication and
        # authorization until an explicit target route is selected at runtime.
        return ParsedPolicy("legacy", operation_id, method.upper(), path_template, security)
    if extension is None:
        # 已确认的兼容优先语义：无扩展即 legacy，不维护额外 inventory/baseline。
        return ParsedPolicy("legacy", operation_id, method.upper(), path_template, security)
    resource, action, boundary, kinds = parse_authz(extension)
    return ParsedPolicy(
        "generated", operation_id, method.upper(), path_template, security,
        "v1", resource, action, boundary, kinds,
    )


def gateway_path(openapi_path: str) -> str:
    if openapi_path in ROOT_INFRASTRUCTURE_PATHS:
        return openapi_path
    return "/api/v1" + openapi_path


def collect_policies(
    spec: dict[str, Any],
    target_operations: dict[str, dict[str, Any]] | None = None,
    compatibility_policies: dict[tuple[str, str], ParsedPolicy] | None = None,
) -> list[ParsedPolicy]:
    policies: list[ParsedPolicy] = []
    seen_routes: set[tuple[str, str]] = set()
    seen_operations: set[str] = set()
    for path in sorted(spec.get("paths", {})):
        path_item = spec["paths"][path]
        if not isinstance(path_item, dict):
            continue
        for method in sorted(set(path_item) & HTTP_METHODS):
            operation = path_item[method]
            if not isinstance(operation, dict):
                raise ValueError(f"{method.upper()} {path} must be an operation object")
            operation_id = operation.get("operationId")
            if not isinstance(operation_id, str) or not operation_id:
                operation_id = DERIVED_OPERATION_IDS.get((method.upper(), path), "")
            target_operation = target_operations.get(operation_id) if target_operations is not None else None
            route_key = (method.upper(), gateway_path(path))
            compatibility_policy = compatibility_policies.get(route_key) if compatibility_policies is not None else None
            policy = classify(spec, path, method, operation, target_operation, compatibility_policy)
            route_key = (policy.method, policy.path_template)
            if route_key in seen_routes:
                raise ValueError(f"duplicate route {route_key}")
            if policy.operation_id in seen_operations:
                raise ValueError(f"duplicate operationId {policy.operation_id}")
            seen_routes.add(route_key)
            seen_operations.add(policy.operation_id)
            policies.append(policy)
    return policies


GO_SOURCE_CONSTANTS = {
    "public": "PolicySourcePublic",
    "generated": "PolicySourceGenerated",
    "legacy": "PolicySourceLegacy",
}
GO_SCHEME_CONSTANTS = {
    "BearerAuth": "OpenAPISecurityBearer",
    "ApiKeyAuth": "OpenAPISecurityAPIKey",
    "ConsoleRefreshCookie": 'OpenAPISecurityScheme("ConsoleRefreshCookie")',
    "BossRefreshCookie": 'OpenAPISecurityScheme("BossRefreshCookie")',
}
GO_BOUNDARY_CONSTANTS = {
    "own": "BoundaryOwn",
    "tenant": "BoundaryTenant",
    "platform": "BoundaryPlatform",
}
GO_KIND_CONSTANTS = {
    "user": "PrincipalUser",
    "service": "PrincipalService",
    "api_key": "PrincipalAPIKey",
    "sandbox": "PrincipalSandbox",
}


def render_policy_lines(policy: ParsedPolicy) -> list[str]:
    lines = [
        f'    "{policy.method} {policy.path_template}": {{',
        f"        Source: {GO_SOURCE_CONSTANTS[policy.source]},",
        f'        OperationID: "{policy.operation_id}",',
        f'        Method: "{policy.method}",',
        f'        PathTemplate: "{policy.path_template}",',
    ]
    if policy.security:
        requirements = ", ".join(
            "{AllOf: []OpenAPISecurityScheme{" + ", ".join(GO_SCHEME_CONSTANTS[s] for s in schemes) + "}}"
            for schemes in policy.security
        )
        lines.append(f"        SecurityAlternatives: []SecurityRequirement{{{requirements}}},")
    if policy.source == "generated":
        lines.append(f'        Version: "{policy.version}",')
        lines.append(f'        Resource: "{policy.resource}",')
        lines.append(f'        Action: "{policy.action}",')
        lines.append(f"        Boundary: {GO_BOUNDARY_CONSTANTS[policy.boundary]},")
        lines.append(
            "        PrincipalKinds: []PrincipalKind{"
            + ", ".join(GO_KIND_CONSTANTS[k] for k in policy.principal_kinds)
            + "},"
        )
    lines.append("    },")
    return lines


def render_go(policies: list[ParsedPolicy]) -> str:
    lines = [
        "// Code generated by scripts/generate_gateway_authz.py; DO NOT EDIT.",
        "package authz",
        "",
        "var generatedCorePolicies = map[string]Policy{",
    ]
    for policy in policies:
        lines.extend(render_policy_lines(policy))
    lines.append("}")
    lines.append("")
    return "\n".join(lines)


def generate(
    input_path: Path,
    output_path: Path,
    target_registry_path: Path = DEFAULT_TARGET_REGISTRY,
    replacement_manifest_path: Path = DEFAULT_REPLACEMENT_MANIFEST,
) -> None:
    policies = collect_policies(
        load_spec(input_path),
        load_target_operations(target_registry_path),
        load_core_compatibility_policies(replacement_manifest_path),
    )
    output_path.write_text(render_go(policies), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description="Generate the Core Gateway authz policy registry.")
    parser.add_argument("--input", type=Path, default=DEFAULT_INPUT)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    try:
        generate(args.input, args.output)
    except (ValueError, OSError) as error:
        print(f"ERROR: {error}")
        return 1
    print(f"generated {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
