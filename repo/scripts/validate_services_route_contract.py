#!/usr/bin/env python3
"""Validate Services OpenAPI operations against Gateway route registrations.

The Services handlers are transitional placeholders, so this gate checks only
the method/path surface. Exact pre-existing differences are warning-only when
listed in the route baseline; new differences and stale baseline entries fail.
"""

from __future__ import annotations

import argparse
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "api/openapi/services/v1.yaml"
ROUTER_DIR = ROOT / "services/ani-gateway/internal/router"
BASELINE_PATH = ROOT / "architecture/services-route-baseline.yaml"
HTTP_METHODS = {"GET", "POST", "PUT", "PATCH", "DELETE"}
ROUTE_CALL_PATTERN = re.compile(r'\b([A-Za-z_]\w*)\.(GET|POST|PUT|PATCH|DELETE)\(\s*"([^"]+)"')
ROUTE_GROUP_ALIAS_PATTERN = re.compile(r'\b([A-Za-z_]\w*)\s*:=\s*([A-Za-z_]\w*)\b')


@dataclass(frozen=True, order=True)
class Route:
    method: str
    path: str

    @property
    def key(self) -> tuple[str, str]:
        return self.method, self.path


@dataclass(frozen=True)
class BaselineEntry:
    kind: str
    method: str
    path: str
    status: str
    owner: str
    reason: str

    @property
    def route(self) -> Route:
        return Route(self.method, self.path)

    @property
    def key(self) -> tuple[str, str]:
        return self.kind, self.route.key


@dataclass(frozen=True)
class Result:
    warnings: tuple[str, ...]
    errors: tuple[str, ...]


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


def openapi_routes(spec: dict[str, Any]) -> set[Route]:
    routes: set[Route] = set()
    for path, path_item in (spec.get("paths") or {}).items():
        if not isinstance(path_item, dict):
            continue
        for method in HTTP_METHODS:
            if isinstance(path_item.get(method.lower()), dict):
                routes.add(Route(method, normalize_path(path)))
    return routes


def gateway_routes(router_dir: Path = ROUTER_DIR) -> set[Route]:
    routes: set[Route] = set()
    for path in sorted(router_dir.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(encoding="utf-8")
        service_receivers = {"svc"} if re.search(r"\bsvc\b", text) else set()
        aliases = dict(ROUTE_GROUP_ALIAS_PATTERN.findall(text))
        changed = True
        while changed:
            changed = False
            for alias, receiver in aliases.items():
                if receiver in service_receivers and alias not in service_receivers:
                    service_receivers.add(alias)
                    changed = True
        for receiver, method, route_path in ROUTE_CALL_PATTERN.findall(text):
            if receiver not in service_receivers:
                continue
            routes.add(Route(method, normalize_path(route_path)))
    return routes


def load_baseline(path: Path = BASELINE_PATH) -> dict[tuple[str, tuple[str, str]], BaselineEntry]:
    raw = load_yaml(path)
    entries: dict[tuple[str, tuple[str, str]], BaselineEntry] = {}
    for item in raw.get("exceptions", []):
        if not isinstance(item, dict):
            raise ValueError(f"{path}: every exception must be an object")
        entry = BaselineEntry(
            kind=str(item.get("kind", "")),
            method=str(item.get("method", "")).upper(),
            path=normalize_path(str(item.get("path", ""))),
            status=str(item.get("status", "")),
            owner=str(item.get("owner", "")),
            reason=str(item.get("reason", "")),
        )
        if entry.kind not in {"code_not_in_spec", "spec_not_in_code"}:
            raise ValueError(f"{path}: unsupported baseline kind {entry.kind!r}")
        if entry.method not in HTTP_METHODS or not entry.path or not entry.owner or not entry.reason:
            raise ValueError(f"{path}: incomplete route baseline entry: {item!r}")
        if entry.status != "accepted_baseline":
            raise ValueError(f"{path}: route baseline entries must be accepted_baseline")
        if entry.key in entries:
            raise ValueError(f"{path}: duplicate route baseline entry: {entry.key!r}")
        entries[entry.key] = entry
    return entries


def validate(
    spec_routes: set[Route],
    code_routes: set[Route],
    baseline: dict[tuple[str, tuple[str, str]], BaselineEntry],
) -> Result:
    code_only = code_routes - spec_routes
    spec_only = spec_routes - code_routes
    actual: dict[tuple[str, tuple[str, str]], str] = {}
    for route in sorted(code_only):
        actual[("code_not_in_spec", route.key)] = "code route is not declared in Services OpenAPI"
    for route in sorted(spec_only):
        actual[("spec_not_in_code", route.key)] = "OpenAPI operation has no registered Services Gateway route"

    warnings = tuple(
        f"accepted Services route baseline: {kind} {method} {path} — {baseline[(kind, (method, path))].reason}"
        for kind, (method, path) in sorted(actual)
        if (kind, (method, path)) in baseline
    )
    errors = [
        f"new Services route mismatch: {kind} {method} {path} — {detail}"
        for (kind, (method, path)), detail in sorted(actual.items())
        if (kind, (method, path)) not in baseline
    ]
    errors.extend(
        f"stale Services route baseline: {kind} {method} {path}"
        for kind, (method, path) in sorted(baseline)
        if (kind, (method, path)) not in actual
    )
    return Result(warnings=warnings, errors=tuple(errors))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=ROOT)
    args = parser.parse_args()
    root = args.root.resolve()
    spec_path = root / "api/openapi/services/v1.yaml"
    router_dir = root / "services/ani-gateway/internal/router"
    baseline_path = root / "architecture/services-route-baseline.yaml"
    result = validate(openapi_routes(load_yaml(spec_path)), gateway_routes(router_dir), load_baseline(baseline_path))
    for warning in result.warnings:
        print(f"WARNING: {warning}")
    for error in result.errors:
        print(f"ERROR: {error}")
    print(f"Services route contract: {len(result.warnings)} accepted baseline warning(s), {len(result.errors)} error(s)")
    return 1 if result.errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
