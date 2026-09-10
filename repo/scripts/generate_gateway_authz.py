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
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_INPUT = ROOT / "api/openapi/v1.yaml"
DEFAULT_OUTPUT = ROOT / "services/ani-gateway/internal/authz/zz_generated_core_policies.go"

HTTP_METHODS = {"get", "post", "put", "patch", "delete", "head", "options"}
VALID_BOUNDARIES = {"own", "tenant", "platform"}
VALID_PRINCIPAL_KINDS = {"user", "service", "api_key", "sandbox"}
VALID_SECURITY_SCHEMES = {"BearerAuth", "ApiKeyAuth"}

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


def classify(spec: dict[str, Any], openapi_path: str, method: str, operation: dict[str, Any]) -> ParsedPolicy:
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
    if security_raw == []:
        if extension is not None:
            raise ValueError(f"{method.upper()} {openapi_path} public operation must not define x-ani-authz")
        return ParsedPolicy("public", operation_id, method.upper(), path_template, ())
    security = parse_security(security_raw)
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


def collect_policies(spec: dict[str, Any]) -> list[ParsedPolicy]:
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
            policy = classify(spec, path, method, operation)
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


def generate(input_path: Path, output_path: Path) -> None:
    policies = collect_policies(load_spec(input_path))
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
