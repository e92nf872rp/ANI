#!/usr/bin/env python3
"""Core Gateway 路由覆盖门禁（AUTHZ-POLICY-A）。

校验已注册 Core 路由与生成 authz registry 的覆盖关系：

- Core policy path（/healthz、/readyz、/api/v1/*，排除 /api/v1/svc/）必须
  出现在生成 registry；
- /api/v1/demo/* 属于 dev.yaml 演示层契约，显式声明为 out-of-scope；
- /health、/ready 是既有根级 infrastructure public route，显式豁免，
  不得伪造为 Core product policy；
- 其它已注册 route（/api/v1/svc/*、/v1/* OpenAI 兼容代理、/health、/ready、
  /api/v1/demo/*）必须落在显式 out-of-scope 集合中，未分类 route 一律失败，
  防止模糊前缀规则漏判。

扫描方式复用 validate_services_route_contract.py 的正则模式：
从 router.go 解析 register 函数到 group 前缀的映射，再从各 register 函数体
提取 .METHOD("/path") 路由注册。
"""

from __future__ import annotations

import argparse
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "api/openapi/v1.yaml"
ROUTER_DIR = ROOT / "services/ani-gateway/internal/router"
GENERATED_PATH = ROOT / "services/ani-gateway/internal/authz/zz_generated_core_policies.go"

HTTP_METHODS = ("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS")
ROUTE_CALL_PATTERN = re.compile(r'\b([A-Za-z_]\w*)\.(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\(\s*"([^"]+)"')

CORE_PREFIX = "/api/v1/"
# 显式 out-of-scope：Services 过渡面、OpenAI 兼容代理、dev.yaml 演示层。
OUT_OF_SCOPE_PREFIXES = ("/api/v1/svc/", "/v1/", "/api/v1/demo/")
# 根级 infrastructure 端点：/healthz、/readyz 是 Core policy path，
# /health、/ready 是既有 public route 豁免（见 AUTHZ-POLICY-A 计划 3.4）。
ROOT_INFRASTRUCTURE = {"/healthz", "/readyz", "/health", "/ready"}
ROOT_INFRASTRUCTURE_CORE = {"/healthz", "/readyz"}


@dataclass(frozen=True, order=True)
class Route:
    method: str
    path: str

    @property
    def key(self) -> tuple[str, str]:
        return self.method, self.path

    def describe(self) -> str:
        return f"{self.method} {self.path}"


def load_yaml(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        value = yaml.safe_load(handle) or {}
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a YAML object")
    return value


def normalize_path(path: str) -> str:
    path = path.strip()
    if not path.startswith("/"):
        path = "/" + path
    return re.sub(r":([A-Za-z0-9_]+)", r"{\1}", path)


def gateway_path(openapi_path: str) -> str:
    """Core OpenAPI 路由在 Gateway 上的 full path（/api/v1 前缀规范化一次）。"""
    if openapi_path in {"/healthz", "/readyz"}:
        return openapi_path
    return "/api/v1" + openapi_path


def openapi_registry_routes(spec: dict[str, Any]) -> set[Route]:
    routes: set[Route] = set()
    for path, path_item in (spec.get("paths") or {}).items():
        if not isinstance(path_item, dict):
            continue
        for method in HTTP_METHODS:
            if isinstance(path_item.get(method.lower()), dict):
                routes.add(Route(method, gateway_path(path)))
    return routes


def _function_bodies(text: str) -> dict[str, str]:
    """提取顶层 func 定义及其函数体（大括号配对）。"""
    bodies: dict[str, str] = {}
    for match in re.finditer(r"^func\s+([A-Za-z_]\w*)\s*\(", text, flags=re.MULTILINE):
        name = match.group(1)
        open_brace = text.find("{", match.end())
        if open_brace < 0:
            continue
        depth = 0
        for index in range(open_brace, len(text)):
            char = text[index]
            if char == "{":
                depth += 1
            elif char == "}":
                depth -= 1
                if depth == 0:
                    bodies[name] = text[open_brace + 1 : index]
                    break
    return bodies


def _register_prefixes(register_body: str) -> dict[str, str]:
    """router.go RegisterWithOptions 中 register 函数到 group 前缀的映射。

    覆盖三种写法：`registerX(v1)`（group 变量）、`registerX(h.Group(""))`
    （内联 group）、`h.Group("/v1").POST(...)`（内联代理 route，以 __inline__ 前缀区分）。
    """
    prefixes: dict[str, str] = {}
    group_vars: dict[str, str] = {}
    for var, prefix in re.findall(r'([A-Za-z_]\w*)\s*:=\s*h\.Group\(\s*"([^"]*)"\s*\)', register_body):
        group_vars[var] = prefix
    # 变量形式：registerX(v1)
    for name, arg in re.findall(r"\b(register[A-Za-z0-9_]*)\(\s*([A-Za-z_]\w*)\b", register_body):
        if arg in group_vars:
            prefixes[name] = group_vars[arg]
    # 内联 group：registerX(h.Group("/prefix"))
    for name, prefix in re.findall(r'\b(register[A-Za-z0-9_]*)\(\s*h\.Group\(\s*"([^"]*)"\s*\)\s*\)', register_body):
        prefixes[name] = prefix
    # 内联代理 route：h.Group("/v1").POST("/chat/completions", ...)
    for prefix in re.findall(r'h\.Group\(\s*"([^"]*)"\s*\)\.(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\(', register_body):
        if prefix not in group_vars:
            prefixes[f"__inline__{prefix}"] = prefix
    return prefixes


def gateway_registered_routes(router_dir: Path = ROUTER_DIR) -> set[Route]:
    """扫描各 register 函数体内的 .METHOD("/path")，按 group 前缀拼 full path。"""
    router_go = (router_dir / "router.go").read_text(encoding="utf-8")
    register_body = _function_bodies(router_go).get("RegisterWithOptions", "")
    prefixes = _register_prefixes(register_body)
    inline_prefixes = {p[len("__inline__"):] for p in prefixes if p.startswith("__inline__")}
    prefixes = {name: p for name, p in prefixes.items() if not name.startswith("__inline__")}

    bodies: dict[str, str] = {}
    for path in sorted(router_dir.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        bodies.update(_function_bodies(path.read_text(encoding="utf-8")))

    routes: set[Route] = set()
    for name, body in bodies.items():
        prefix = prefixes.get(name)
        if prefix is None:
            continue
        for _receiver, method, route_path in ROUTE_CALL_PATTERN.findall(body):
            routes.add(Route(method, normalize_path(prefix + route_path)))
    # 内联代理 route 直接落在 RegisterWithOptions 体内
    for _var, method, route_path in re.findall(
        r'h\.Group\(\s*"([^"]*)"\s*\)\.(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\(\s*"([^"]+)"',
        register_body,
    ):
        routes.add(Route(method, normalize_path(_var + route_path)))
    _ = inline_prefixes
    return routes


def generated_registry_routes(generated_path: Path = GENERATED_PATH) -> set[Route]:
    if not generated_path.is_file():
        raise SystemExit(f"generated authz registry missing: {generated_path}; run make gen-gateway-authz")
    text = generated_path.read_text(encoding="utf-8")
    # 生成物 key 形如 "GET /api/v1/admin/quota-meta"，直接取完整 key。
    return {
        Route(method, path)
        for method, path in re.findall(r'"(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) (/[^"]*)": \{', text)
    }


def classify_full_path(full_path: str) -> str:
    """返回 core_policy / infrastructure_exempt / out_of_scope / unclassified。"""
    if full_path in ROOT_INFRASTRUCTURE:
        return "core_policy" if full_path in ROOT_INFRASTRUCTURE_CORE else "infrastructure_exempt"
    if full_path.startswith(OUT_OF_SCOPE_PREFIXES):
        return "out_of_scope"
    if full_path.startswith(CORE_PREFIX):
        return "core_policy"
    return "unclassified"


def validate(registered: set[Route], generated: set[Route], spec_routes: set[Route]) -> list[str]:
    errors: list[str] = []
    for route in sorted(registered):
        classification = classify_full_path(route.path)
        if classification == "unclassified":
            errors.append(f"registered route is not classified as Core policy or explicit out-of-scope: {route.describe()}")
            continue
        if classification == "core_policy" and route not in generated:
            errors.append(f"registered Core route missing from authz registry: {route.describe()}")
    for route in sorted(spec_routes - generated):
        errors.append(f"Core OpenAPI route missing from authz registry: {route.describe()}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate Core Gateway authz route coverage.")
    parser.add_argument("--root", type=Path, default=ROOT)
    args = parser.parse_args()
    root = args.root.resolve()
    registered = gateway_registered_routes(root / "services/ani-gateway/internal/router")
    generated = generated_registry_routes(root / "services/ani-gateway/internal/authz/zz_generated_core_policies.go")
    spec_routes = openapi_registry_routes(load_yaml(root / "api/openapi/v1.yaml"))
    errors = validate(registered, generated, spec_routes)
    for error in errors:
        print(f"ERROR: {error}")
    print(
        f"Core gateway authz route coverage: {len(registered)} registered route(s), "
        f"{len(generated)} registry route(s), {len(errors)} error(s)"
    )
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
