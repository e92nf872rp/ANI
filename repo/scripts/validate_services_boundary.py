#!/usr/bin/env python3
"""Validate the precise Services boundary baseline for controlled ANI Services PRs."""

from __future__ import annotations

import argparse
import ast
import pathlib
import re
import sys
from dataclasses import dataclass
from typing import Iterable

import yaml

import validate_spec_split_contract as spec_split


ROOT = pathlib.Path(__file__).resolve().parents[1]
BASELINE_STATUS = "accepted_baseline"
GO_IMPORT_RE = re.compile(r'"([^"]+)"')
CORE_PROTECTED_SERVICE_ROOTS = (
    "ani-gateway",
    "auth-service",
    "task-service",
    "metering-service",
    "reconcile-worker",
)
SERVICES_OWNED_SOURCE_ROOTS = ("model-service", "kb-service")
DOCS_ONLY_SERVICE_ROOTS = ("docs", "tasks", "prototypes")
KNOWN_SERVICE_ROOTS = frozenset(
    (*CORE_PROTECTED_SERVICE_ROOTS, *SERVICES_OWNED_SOURCE_ROOTS, *DOCS_ONLY_SERVICE_ROOTS)
)
DOCS_ONLY_SOURCE_SUFFIXES = (
    ".go",
    ".py",
    ".ts",
    ".tsx",
    ".js",
    ".jsx",
    ".mjs",
    ".cjs",
    ".sh",
    ".bash",
    ".zsh",
    ".sql",
    ".rs",
    ".java",
    ".kt",
    ".cs",
    ".rb",
    ".php",
)
GO_SCAN_ROOTS = tuple(f"services/{service}" for service in SERVICES_OWNED_SOURCE_ROOTS)
PYTHON_SCAN_ROOTS = ("ai",)
GATEWAY_ROOT = "services/ani-gateway"
SERVICE_IMPORT_PREFIX = "github.com/kubercloud/ani/services/"
FORBIDDEN_GO_PREFIXES = (
    "github.com/kubercloud/ani/pkg/ports",
    "github.com/kubercloud/ani/pkg/adapters",
    "github.com/kubercloud/ani/pkg/bootstrap",
    "github.com/kubercloud/ani/internal/",
    "github.com/kubercloud/ani/services/ani-gateway/internal/",
)
FORBIDDEN_PROVIDER_MODULES = ("pymilvus",)
FORBIDDEN_GATEWAY_SERVICE_IMPORT_PREFIXES = (
    "github.com/kubercloud/ani/services/model-service/",
    "github.com/kubercloud/ani/services/kb-service/",
    "github.com/kubercloud/ani/services/auth-service/",
    "github.com/kubercloud/ani/services/task-service/",
    "github.com/kubercloud/ani/services/metering-service/",
    "github.com/kubercloud/ani/services/reconcile-worker/",
)


@dataclass(frozen=True)
class BaselineEntry:
    path: str
    rule: str
    import_path: str
    status: str
    owner: str
    reason: str
    disposition: str

    @property
    def key(self) -> tuple[str, str, str]:
        return (self.path, self.rule, self.import_path)


@dataclass(frozen=True)
class Finding:
    path: str
    rule: str
    import_path: str
    detail: str

    @property
    def key(self) -> tuple[str, str, str]:
        return (self.path, self.rule, self.import_path)


@dataclass
class ValidationResult:
    warnings: list[str]
    errors: list[str]

    @property
    def warning_count(self) -> int:
        return len(self.warnings)

    @property
    def error_count(self) -> int:
        return len(self.errors)


def normalize_path(path: pathlib.Path, root: pathlib.Path) -> str:
    return path.resolve().relative_to(root.resolve()).as_posix()


def validate_relative_file_path(rel_path: str, root: pathlib.Path) -> str:
    normalized = str(rel_path).strip()
    if not normalized:
        raise ValueError("baseline entry requires exact file path")
    if normalized.startswith("/") or normalized.endswith("/"):
        raise ValueError(f"{normalized}: baseline path must be a relative exact file path")
    if any(token in normalized for token in ("*", "?", "[")):
        raise ValueError(f"{normalized}: baseline path must be an exact file path, not a wildcard")
    target = root / normalized
    if not target.exists() or not target.is_file():
        raise ValueError(f"{normalized}: baseline path must point to an existing file")
    return normalize_path(target, root)


def load_baseline(path: pathlib.Path, root: pathlib.Path) -> dict[tuple[str, str, str], BaselineEntry]:
    with path.open("r", encoding="utf-8") as handle:
        document = yaml.safe_load(handle) or {}
    if document.get("version") != 1:
        raise ValueError(f"{path}: version must be 1")
    raw_entries = document.get("exceptions")
    if not isinstance(raw_entries, list):
        raise ValueError(f"{path}: exceptions must be a list")

    entries: dict[tuple[str, str, str], BaselineEntry] = {}
    for raw_entry in raw_entries:
        if not isinstance(raw_entry, dict):
            raise ValueError(f"{path}: baseline entries must be mappings")
        normalized_path = validate_relative_file_path(str(raw_entry.get("path", "")), root)
        rule = str(raw_entry.get("rule", "")).strip()
        import_path = str(raw_entry.get("import", "")).strip()
        status = str(raw_entry.get("status", "")).strip()
        owner = str(raw_entry.get("owner", "")).strip()
        reason = str(raw_entry.get("reason", "")).strip()
        disposition = str(raw_entry.get("disposition", "")).strip()
        if not rule or not import_path or not status or not owner or not reason or not disposition:
            raise ValueError(f"{normalized_path}: baseline entry requires rule, import, status, owner, reason, disposition")
        if status != BASELINE_STATUS:
            raise ValueError(f"{normalized_path}: status must be {BASELINE_STATUS}")
        entry = BaselineEntry(
            path=normalized_path,
            rule=rule,
            import_path=import_path,
            status=status,
            owner=owner,
            reason=reason,
            disposition=disposition,
        )
        if entry.key in entries:
            raise ValueError(f"{normalized_path}: duplicate baseline entry for {rule} {import_path}")
        entries[entry.key] = entry
    return entries


def parse_go_imports(path: pathlib.Path) -> list[str]:
    imports: list[str] = []
    in_block = False
    with path.open("r", encoding="utf-8") as handle:
        for raw_line in handle:
            line = raw_line.strip()
            if line.startswith("import ("):
                in_block = True
                continue
            if in_block and line.startswith(")"):
                in_block = False
                continue
            if in_block or line.startswith("import "):
                match = GO_IMPORT_RE.search(line)
                if match:
                    imports.append(match.group(1))
    return imports


def parse_python_imports(path: pathlib.Path) -> list[str]:
    tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    modules: list[str] = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                modules.append(alias.name)
        elif isinstance(node, ast.ImportFrom) and node.module:
            modules.append(node.module)
    return modules


def iter_source_files(root: pathlib.Path, relative_root: str, suffix: str) -> Iterable[pathlib.Path]:
    target_root = root / relative_root
    if not target_root.exists():
        return []
    return sorted(path for path in target_root.rglob(f"*{suffix}") if path.is_file())


def iter_files_with_suffixes(root: pathlib.Path, relative_root: str, suffixes: tuple[str, ...]) -> Iterable[pathlib.Path]:
    target_root = root / relative_root
    if not target_root.exists():
        return []
    return sorted(path for path in target_root.rglob("*") if path.is_file() and path.suffix in suffixes)


def service_name_for_path(path: pathlib.Path, root: pathlib.Path) -> str | None:
    rel_parts = normalize_path(path, root).split("/")
    if len(rel_parts) < 2 or rel_parts[0] != "services":
        return None
    return rel_parts[1]


def imported_service_internal_name(import_path: str) -> str | None:
    if not import_path.startswith(SERVICE_IMPORT_PREFIX):
        return None
    remainder = import_path[len(SERVICE_IMPORT_PREFIX) :]
    service_name, separator, internal_path = remainder.partition("/internal/")
    if not separator or not service_name or not internal_path:
        return None
    return service_name


def detect_service_root_classification_findings(root: pathlib.Path) -> list[Finding]:
    findings: list[Finding] = []
    services_root = root / "services"
    if not services_root.exists():
        return findings

    for child in sorted(path for path in services_root.iterdir() if path.is_dir()):
        rel_path = normalize_path(child, root)
        if child.name not in KNOWN_SERVICE_ROOTS:
            findings.append(
                Finding(
                    path=rel_path,
                    rule="unknown_service_root",
                    import_path=child.name,
                    detail=(
                        "repo/services immediate children must be classified as Core-protected, "
                        "Services-owned source, or docs-only before Services PRs can proceed"
                    ),
                )
            )
            continue
        if child.name in DOCS_ONLY_SERVICE_ROOTS:
            for source_path in iter_files_with_suffixes(root, rel_path, DOCS_ONLY_SOURCE_SUFFIXES):
                findings.append(
                    Finding(
                        path=normalize_path(source_path, root),
                        rule="docs_only_service_source_file",
                        import_path=child.name,
                        detail="Services docs/tasks/prototypes roots are docs-only and must not contain source files",
                    )
                )
    return findings


def detect_go_findings(root: pathlib.Path) -> list[Finding]:
    findings: list[Finding] = []
    for relative_root in GO_SCAN_ROOTS:
        for path in iter_source_files(root, relative_root, ".go"):
            if path.name.endswith("_test.go"):
                continue
            rel_path = normalize_path(path, root)
            current_service = service_name_for_path(path, root)
            for import_path in parse_go_imports(path):
                if import_path.startswith(FORBIDDEN_GO_PREFIXES):
                    findings.append(
                        Finding(
                            path=rel_path,
                            rule="core_internal_go_import",
                            import_path=import_path,
                            detail="Services 业务代码不得直接导入 Core 内部 pkg 或 Core service internal 包",
                        )
                    )
                    continue
                imported_service = imported_service_internal_name(import_path)
                if imported_service and imported_service != current_service:
                    findings.append(
                        Finding(
                            path=rel_path,
                            rule="cross_service_internal_go_import",
                            import_path=import_path,
                            detail="Services 业务模块只能导入自身 service 的 internal 包，不得跨 service 导入别的 /internal/ 实现",
                        )
                    )
    return findings


def detect_python_findings(root: pathlib.Path) -> list[Finding]:
    findings: list[Finding] = []
    for relative_root in PYTHON_SCAN_ROOTS:
        for path in iter_source_files(root, relative_root, ".py"):
            rel_path = normalize_path(path, root)
            for module in parse_python_imports(path):
                top_level = module.split(".", 1)[0]
                if top_level in FORBIDDEN_PROVIDER_MODULES:
                    findings.append(
                        Finding(
                            path=rel_path,
                            rule="provider_sdk_python_import",
                            import_path=top_level,
                            detail="AI 业务代码不得新增未登记的 provider SDK 直连",
                        )
                    )
    return findings


def detect_gateway_cross_layer_findings(root: pathlib.Path) -> list[Finding]:
    findings: list[Finding] = []
    for path in iter_source_files(root, GATEWAY_ROOT, ".go"):
        if path.name.endswith("_test.go"):
            continue
        rel_path = normalize_path(path, root)
        for import_path in parse_go_imports(path):
            if import_path.startswith(FORBIDDEN_GATEWAY_SERVICE_IMPORT_PREFIXES):
                findings.append(
                    Finding(
                        path=rel_path,
                        rule="gateway_cross_layer_import",
                        import_path=import_path,
                        detail="ani-gateway 只能通过 API 边界承载 Services 路由，不得直接依赖 Services 业务实现",
                    )
                )
    return findings


def reconcile_findings(
    findings: Iterable[Finding],
    baseline: dict[tuple[str, str, str], BaselineEntry],
) -> ValidationResult:
    warnings: list[str] = []
    errors: list[str] = []
    matched: set[tuple[str, str, str]] = set()

    for finding in sorted(findings, key=lambda item: (item.path, item.rule, item.import_path)):
        entry = baseline.get(finding.key)
        if entry is None:
            errors.append(f"{finding.path}: {finding.rule} {finding.import_path}: {finding.detail}")
            continue
        matched.add(entry.key)
        warnings.append(
            f"{finding.path}: accepted baseline {finding.rule} {finding.import_path} "
            f"(owner={entry.owner}; reason={entry.reason}; disposition={entry.disposition})"
        )

    unused = sorted(set(baseline) - matched)
    for key in unused:
        entry = baseline[key]
        errors.append(
            f"{entry.path}: baseline entry is stale or over-broad because no current finding matches "
            f"{entry.rule} {entry.import_path}"
        )
    return ValidationResult(warnings=warnings, errors=errors)


def validate_gateway_split_contract(run_spec_split: bool, errors: list[str]) -> None:
    if not run_spec_split:
        return
    try:
        spec_split.main()
    except SystemExit as exc:
        code = exc.code if isinstance(exc.code, int) else 1
        if code:
            errors.append(f"spec split contract invalid: {exc}")


def validate_workspace(
    root: pathlib.Path,
    *,
    baseline_path: pathlib.Path | None = None,
    run_spec_split: bool = True,
) -> ValidationResult:
    root = root.resolve()
    baseline_file = baseline_path or (root / "architecture" / "services-boundary-baseline.yaml")
    baseline = load_baseline(baseline_file, root)
    findings = [
        *detect_service_root_classification_findings(root),
        *detect_go_findings(root),
        *detect_python_findings(root),
        *detect_gateway_cross_layer_findings(root),
    ]
    result = reconcile_findings(findings, baseline)
    errors = list(result.errors)
    validate_gateway_split_contract(run_spec_split, errors)
    return ValidationResult(warnings=result.warnings, errors=errors)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=".", help="repository root")
    parser.add_argument(
        "--baseline",
        default="architecture/services-boundary-baseline.yaml",
        help="baseline YAML relative to --root",
    )
    args = parser.parse_args(argv)

    root = pathlib.Path(args.root).resolve()
    baseline_path = root / args.baseline
    try:
        result = validate_workspace(root, baseline_path=baseline_path, run_spec_split=True)
    except (OSError, ValueError, SyntaxError, yaml.YAMLError) as err:
        print(err, file=sys.stderr)
        return 1

    for warning in result.warnings:
        print(f"WARNING: {warning}")
    if result.errors:
        for error in result.errors:
            print(error, file=sys.stderr)
        return 1
    print(f"services boundary guard passed with {result.warning_count} accepted baseline warning(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
