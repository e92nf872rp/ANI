#!/usr/bin/env python3
"""Static safety gate for the inference gateway publication migration."""

from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MIGRATION_PATH = ROOT / "deploy/migrations/20260831_001_inference_gateway_publication.sql"

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

REQUIRED_CLAUSES = (
    "add column if not exists publication_desired text not null default 'unpublished'",
    "check (publication_desired in ('published', 'unpublished'))",
    "add column if not exists publication_generation bigint not null default 0",
    "add column if not exists publication_observed_generation bigint not null default 0",
    "add column if not exists publication_phase text not null default 'unpublished'",
    "check (publication_phase in ('pending', 'publishing', 'published', 'unpublishing', 'unpublished', 'failed'))",
    "add column if not exists publication_lease_owner text",
    "add column if not exists publication_lease_until timestamptz",
    "add column if not exists publication_lease_token uuid",
    "add column if not exists publication_last_error text",
    "add column if not exists publication_updated_at timestamptz not null default now()",
    "update inference_services set publication_desired = 'unpublished', publication_generation = 0, publication_observed_generation = 0, publication_phase = 'unpublished', invocation_url = null, publication_updated_at = now() where publication_generation = 0",
    "create index if not exists idx_inference_services_publication_claim on inference_services(publication_updated_at, id) where deleted_at is null and (publication_generation <> publication_observed_generation or publication_phase in ('pending', 'publishing', 'unpublishing', 'failed'))",
)

def strip_sql_comments(sql: str) -> tuple[str, bool]:
    """Remove SQL comments while preserving quoted values and nested block structure."""
    result: list[str] = []
    index = 0
    length = len(sql)
    while index < length:
        if sql.startswith("--", index):
            newline = sql.find("\n", index)
            if newline == -1:
                break
            result.append("\n")
            index = newline + 1
            continue
        if sql.startswith("/*", index):
            depth = 1
            index += 2
            while index < length and depth:
                if sql.startswith("/*", index):
                    depth += 1
                    index += 2
                elif sql.startswith("*/", index):
                    depth -= 1
                    index += 2
                else:
                    index += 1
            if depth:
                return "".join(result), False
            result.append(" ")
            continue

        quote = sql[index]
        if quote in ("'", '"'):
            result.append(quote)
            index += 1
            while index < length:
                current = sql[index]
                result.append(current)
                index += 1
                if current == quote:
                    if index < length and sql[index] == quote:
                        result.append(sql[index])
                        index += 1
                        continue
                    break
                if current == "\\" and index < length:
                    result.append(sql[index])
                    index += 1
            continue

        result.append(quote)
        index += 1
    return "".join(result), True


def normalize(sql: str) -> str:
    without_comments, _ = strip_sql_comments(sql)
    return re.sub(r"\s+", " ", without_comments.lower()).strip()


def validate_text(sql: str) -> tuple[str, ...]:
    without_comments, comments_closed = strip_sql_comments(sql)
    normalized = re.sub(r"\s+", " ", without_comments.lower()).strip()
    errors: list[str] = []

    if not comments_closed:
        errors.append("migration contains an unclosed block comment")

    if "begin;" not in normalized or "commit;" not in normalized:
        errors.append("migration must be transactional")
    if "alter table inference_services" not in normalized:
        errors.append("migration must alter inference_services additively")
    for required in REQUIRED:
        if required not in normalized:
            errors.append(f"migration missing {required}")

    for clause in REQUIRED_CLAUSES:
        if clause not in normalized:
            errors.append(f"migration missing required clause: {clause}")

    if re.search(r"\bdrop\s+column\b", normalized):
        errors.append("migration must not drop column")
    if re.search(r"\b(drop|truncate)\s+table\b|\bcreate\s+table\s+(?:if\s+not\s+exists\s+)?inference_services\b", normalized):
        errors.append("migration must not recreate or destructively replace inference_services")
    if re.search(r"\b(secret|password|credential|api[_ -]?key)\s*[:=]", normalized):
        errors.append("migration must not contain Secret content or credential literals")

    return tuple(errors)


def main() -> int:
    try:
        sql = MIGRATION_PATH.read_text(encoding="utf-8")
    except OSError as exc:
        print(f"inference gateway publication migration invalid: {exc}")
        return 1
    errors = validate_text(sql)
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        print(f"inference gateway publication migration blocked: {len(errors)} error(s)")
        return 1
    print("inference gateway publication migration valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
