#!/usr/bin/env python3
"""Validate Sprint 3 M1-VSTORE-A Core API contract."""

from pathlib import Path
import re
import sys
import yaml


EXPECTED_PATHS = {
    "/vector-stores": {
        "get": ("listVectorStores", "scope:vector-stores:read", {"200", "401", "403"}),
        "post": ("createVectorStore", "scope:vector-stores:create", {"201", "400", "401", "403"}),
    },
    "/vector-stores/{vector_store_id}": {
        "get": ("getVectorStore", "scope:vector-stores:read", {"200", "401", "403", "404"}),
        "delete": ("deleteVectorStore", "scope:vector-stores:delete", {"200", "401", "403", "404"}),
    },
    "/vector-stores/{vector_store_id}/search": {
        "post": ("searchVectorStore", "scope:vector-stores:search", {"200", "400", "401", "403", "404", "422"}),
    },
}

EXPECTED_SCHEMAS = {
    "VectorStoreState",
    "VectorStore",
    "VectorStoreKnowledgeBaseLink",
    "VectorStoreListResponse",
    "CreateVectorStoreRequest",
    "VectorStoreSearchRequest",
    "VectorStoreSearchHit",
    "VectorStoreSearchResponse",
}

EXPECTED_FIELDS = {
    "VectorStore": {"id", "tenant_id", "name", "dimension", "metric", "state", "reason", "vector_count", "index_status", "linked_knowledge_bases", "created_at", "updated_at"},
    "VectorStoreKnowledgeBaseLink": {"knowledge_base_id", "name", "linked_at"},
    "VectorStoreSearchHit": {"id", "score", "metadata"},
}

EXPECTED_ROUTES = {
    'v1.GET("/vector-stores"',
    'v1.POST("/vector-stores"',
    'v1.GET("/vector-stores/:vector_store_id"',
    'v1.DELETE("/vector-stores/:vector_store_id"',
    'v1.POST("/vector-stores/:vector_store_id/search"',
}


def load_yaml(path: Path) -> dict:
    with path.open(encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def fail(errors: list[str]) -> None:
    if errors:
        for error in errors:
            print(f"vector alpha contract error: {error}", file=sys.stderr)
        raise SystemExit(1)


def validate_openapi(root: Path, errors: list[str]) -> None:
    core = load_yaml(root / "api/openapi/v1.yaml")
    services = load_yaml(root / "api/openapi/services/v1.yaml")
    paths = core.get("paths", {})
    schemas = core.get("components", {}).get("schemas", {})

    for path, methods in EXPECTED_PATHS.items():
        if path not in paths:
            errors.append(f"api/openapi/v1.yaml missing path {path}")
            continue
        for method, (operation_id, scope, expected_responses) in methods.items():
            operation = paths[path].get(method)
            if not operation:
                errors.append(f"api/openapi/v1.yaml missing {method.upper()} {path}")
                continue
            if operation.get("operationId") != operation_id:
                errors.append(f"{method.upper()} {path} operationId must be {operation_id}")
            if operation.get("x-ani-rbac-scope") != scope:
                errors.append(f"{method.upper()} {path} RBAC scope must be {scope}")
            missing = expected_responses - set(operation.get("responses", {}).keys())
            if missing:
                errors.append(f"{method.upper()} {path} missing responses: {sorted(missing)}")

    for schema in EXPECTED_SCHEMAS:
        if schema not in schemas:
            errors.append(f"api/openapi/v1.yaml missing schema {schema}")
    for schema, fields in EXPECTED_FIELDS.items():
        properties = schemas.get(schema, {}).get("properties", {})
        missing = fields - set(properties.keys())
        if missing:
            errors.append(f"schema {schema} missing fields: {sorted(missing)}")
    expected_states = {"pending", "ready", "failed", "deleting", "deleted"}
    if set(schemas.get("VectorStoreState", {}).get("enum", [])) != expected_states:
        errors.append(f"VectorStoreState enum must be {sorted(expected_states)}")

    service_paths = services.get("paths", {})
    leaked = [path for path in service_paths if path.startswith("/vector-stores")]
    if leaked:
        errors.append(f"Services API must not contain Core vector store paths: {leaked}")


def validate_gateway(root: Path, errors: list[str]) -> None:
    routes_go = (root / "services/ani-gateway/internal/router/vector_store_resources.go").read_text(encoding="utf-8")
    router_go = (root / "services/ani-gateway/internal/router/router.go").read_text(encoding="utf-8")
    ports_go = (root / "pkg/ports/vector_store.go").read_text(encoding="utf-8")
    service_go = (root / "pkg/adapters/runtime/vector_store_service.go").read_text(encoding="utf-8")
    bootstrap_go = (root / "pkg/bootstrap/deps.go").read_text(encoding="utf-8")

    for route in EXPECTED_ROUTES:
        if route not in routes_go:
            errors.append(f"vector_store_resources.go missing route token {route}")
    if "registerVectorStoreResources(v1)" not in router_go:
        errors.append("router.go must register vector store resources")
    for token in ("VectorStoreService interface", "VectorStoreState", "VectorStoreRecord", "VectorStoreResourceSearchRequest"):
        if token not in ports_go:
            errors.append(f"pkg/ports/vector_store.go missing token {token}")
    for token in ("NewLocalVectorStoreService", "CreateVectorStore", "ListVectorStores", "GetVectorStore", "DeleteVectorStore", "SearchVectorStore"):
        if token not in service_go:
            errors.append(f"vector_store_service.go missing token {token}")
    for pattern, label in (
        (r"VectorStoreResources\s+ports\.VectorStoreService", "VectorStoreResources ports.VectorStoreService"),
        (r"NewLocalVectorStoreService", "NewLocalVectorStoreService"),
    ):
        if not re.search(pattern, bootstrap_go):
            errors.append(f"pkg/bootstrap/deps.go missing token {label}")


def main() -> None:
    root = Path(__file__).resolve().parents[1]
    errors: list[str] = []
    validate_openapi(root, errors)
    validate_gateway(root, errors)
    fail(errors)
    print("vector alpha contract valid")


if __name__ == "__main__":
    main()
