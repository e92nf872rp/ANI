#!/usr/bin/env python3
"""Validate Sprint 3 M1-NETWORK-A Core API contract."""

from pathlib import Path
import re
import sys
import yaml


EXPECTED_PATHS = {
    "/networks/vpcs": {
        "get": ("listNetworkVPCs", "scope:networks:read", {"200", "401", "403"}),
        "post": ("createNetworkVPC", "scope:networks:create", {"201", "400", "401", "403"}),
    },
    "/networks/vpcs/{vpc_id}": {
        "get": ("getNetworkVPC", "scope:networks:read", {"200", "401", "403", "404"}),
        "delete": ("deleteNetworkVPC", "scope:networks:delete", {"200", "401", "403", "404", "409"}),
    },
    "/networks/subnets": {
        "get": ("listNetworkSubnets", "scope:networks:read", {"200", "401", "403"}),
        "post": ("createNetworkSubnet", "scope:networks:create", {"201", "400", "401", "403", "404"}),
    },
    "/networks/subnets/{subnet_id}": {
        "get": ("getNetworkSubnet", "scope:networks:read", {"200", "401", "403", "404"}),
        "delete": ("deleteNetworkSubnet", "scope:networks:delete", {"200", "401", "403", "404", "409"}),
    },
    "/networks/security-groups": {
        "get": ("listNetworkSecurityGroups", "scope:networks:read", {"200", "401", "403"}),
        "post": ("createNetworkSecurityGroup", "scope:networks:create", {"201", "400", "401", "403"}),
    },
    "/networks/security-groups/{security_group_id}": {
        "get": ("getNetworkSecurityGroup", "scope:networks:read", {"200", "401", "403", "404"}),
        "patch": ("updateNetworkSecurityGroup", "scope:networks:update", {"200", "400", "401", "403", "404", "409"}),
        "delete": ("deleteNetworkSecurityGroup", "scope:networks:delete", {"200", "401", "403", "404", "409"}),
    },
    "/networks/load-balancers": {
        "get": ("listNetworkLoadBalancers", "scope:networks:read", {"200", "401", "403"}),
        "post": ("createNetworkLoadBalancer", "scope:networks:create", {"201", "400", "401", "403", "404"}),
    },
    "/networks/load-balancers/{load_balancer_id}": {
        "get": ("getNetworkLoadBalancer", "scope:networks:read", {"200", "401", "403", "404"}),
        "delete": ("deleteNetworkLoadBalancer", "scope:networks:delete", {"200", "401", "403", "404", "409"}),
    },
    "/networks/routes": {
        "get": ("listNetworkRoutes", "scope:networks:read", {"200", "401", "403"}),
        "post": ("createNetworkRoute", "scope:networks:create", {"201", "400", "401", "403", "404", "409"}),
    },
    "/networks/routes/{route_id}": {
        "get": ("getNetworkRoute", "scope:networks:read", {"200", "401", "403", "404"}),
        "delete": ("deleteNetworkRoute", "scope:networks:delete", {"200", "401", "403", "404"}),
    },
}

EXPECTED_SCHEMAS = {
    "NetworkResourceState",
    "NetworkVPC",
    "NetworkSubnet",
    "NetworkSecurityGroupRule",
    "NetworkSecurityGroup",
    "NetworkLoadBalancerListener",
    "NetworkLoadBalancer",
    "CreateNetworkVPCRequest",
    "CreateNetworkSubnetRequest",
    "CreateNetworkSecurityGroupRequest",
    "UpdateNetworkSecurityGroupRequest",
    "CreateNetworkLoadBalancerRequest",
    "NetworkRoute",
    "NetworkRouteListResponse",
    "CreateNetworkRouteRequest",
}

EXPECTED_FIELDS = {
    "NetworkVPC": {"id", "tenant_id", "name", "cidr", "state", "reason", "created_at", "updated_at"},
    "NetworkSubnet": {"id", "tenant_id", "vpc_id", "name", "cidr", "gateway", "state", "reason", "created_at", "updated_at"},
    "NetworkSecurityGroup": {"id", "tenant_id", "name", "description", "rules", "state", "reason", "created_at", "updated_at"},
    "NetworkLoadBalancer": {"id", "tenant_id", "name", "vpc_id", "subnet_id", "scheme", "vip", "listeners", "state", "reason", "created_at", "updated_at"},
    "NetworkRoute": {"id", "vpc_id", "destination_cidr", "next_hop_type", "next_hop_id", "description", "created_at", "dev_profile"},
    "NetworkRouteListResponse": {"items", "total", "next_cursor"},
    "UpdateNetworkSecurityGroupRequest": {"idempotency_key", "description", "rules"},
}

EXPECTED_ROUTES = {
    'v1.GET("/networks/vpcs"',
    'v1.POST("/networks/vpcs"',
    'v1.GET("/networks/vpcs/:vpc_id"',
    'v1.DELETE("/networks/vpcs/:vpc_id"',
    'v1.GET("/networks/subnets"',
    'v1.POST("/networks/subnets"',
    'v1.GET("/networks/security-groups"',
    'v1.POST("/networks/security-groups"',
    'v1.PATCH("/networks/security-groups/:security_group_id"',
    'v1.GET("/networks/load-balancers"',
    'v1.POST("/networks/load-balancers"',
    'v1.GET("/networks/routes"',
    'v1.POST("/networks/routes"',
    'v1.GET("/networks/routes/:route_id"',
    'v1.DELETE("/networks/routes/:route_id"',
}


def load_yaml(path: Path) -> dict:
    with path.open(encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def fail(errors: list[str]) -> None:
    if errors:
        for error in errors:
            print(f"network alpha contract error: {error}", file=sys.stderr)
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
            responses = set(operation.get("responses", {}).keys())
            missing = expected_responses - responses
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
    state_enum = set(schemas.get("NetworkResourceState", {}).get("enum", []))
    expected_states = {"pending", "available", "failed", "deleting", "deleted"}
    if state_enum != expected_states:
        errors.append(f"NetworkResourceState enum must be {sorted(expected_states)}")

    service_paths = services.get("paths", {})
    leaked = [path for path in service_paths if path.startswith("/networks")]
    if leaked:
        errors.append(f"Services API must not contain Core network paths: {leaked}")


def validate_gateway(root: Path, errors: list[str]) -> None:
    routes_go = (root / "services/ani-gateway/internal/router/network_resources.go").read_text(encoding="utf-8")
    router_go = (root / "services/ani-gateway/internal/router/router.go").read_text(encoding="utf-8")
    ports_go = (root / "pkg/ports/network_resources.go").read_text(encoding="utf-8")
    adapter_go = (root / "pkg/adapters/runtime/network_service.go").read_text(encoding="utf-8")
    store_go = (root / "pkg/adapters/runtime/network_store.go").read_text(encoding="utf-8")
    reconciler_go = (root / "pkg/adapters/runtime/network_status_reconciler.go").read_text(encoding="utf-8")
    renderer_go = (root / "pkg/adapters/runtime/kubeovn_network_renderer.go").read_text(encoding="utf-8")
    provider_go = (root / "pkg/adapters/runtime/kubeovn_network_provider.go").read_text(encoding="utf-8")
    rest_go = (root / "pkg/adapters/runtime/kubernetes_rest_client.go").read_text(encoding="utf-8")
    bootstrap_go = (root / "pkg/bootstrap/deps.go").read_text(encoding="utf-8")
    for route in EXPECTED_ROUTES:
        if route not in routes_go:
            errors.append(f"network_resources.go missing route token {route}")
    if "registerNetworkResources(v1)" not in router_go and "registerNetworkResourcesWithService(v1, options.NetworkService)" not in router_go:
        errors.append("router.go must register network resources")
    for token in ("NetworkService interface", "NetworkResourceStore interface", "NetworkProviderRenderer interface", "NetworkProviderDryRun interface", "NetworkProviderApply interface", "NetworkProviderStatusReader interface", "NetworkStatusReconciler interface", "NetworkVPCRecord", "NetworkSubnetRecord", "NetworkLoadBalancerRecord", "NetworkRouteRecord", "NetworkRouteCreateRequest", "NetworkRouteListRequest"):
        if token not in ports_go:
            errors.append(f"pkg/ports/network_resources.go missing token {token}")
    for token in ("NewLocalNetworkService", "WithNetworkResourceStore", "CreateVPC", "CreateSubnet", "CreateSecurityGroup", "CreateLoadBalancer", "CreateRoute", "ListRoutes"):
        if token not in adapter_go:
            errors.append(f"network_service.go missing token {token}")
    for token in ("MetadataNetworkStore", "UpsertVPC", "UpsertSubnet", "UpsertSecurityGroup", "UpsertLoadBalancer", "UpdateResourceState"):
        if token not in store_go:
            errors.append(f"network_store.go missing token {token}")
    for token in ("LocalNetworkStatusReconciler", "Reconcile", "UpdateResourceState"):
        if token not in reconciler_go:
            errors.append(f"network_status_reconciler.go missing token {token}")
    for token in ("KubeOVNNetworkRenderer", "RenderVPC", "RenderSubnet", "RenderSecurityGroup", "RenderLoadBalancer"):
        if token not in renderer_go:
            errors.append(f"kubeovn_network_renderer.go missing token {token}")
    for token in ("KubeOVNNetworkProviderAdapter", "DryRun", "Apply", "Observe", "WithKubeOVNNetworkProviderApplyEnabled"):
        if token not in provider_go:
            errors.append(f"kubeovn_network_provider.go missing token {token}")
    for token in ("ApplyManifests", "ObserveNetworkResource", "networkStateFromKubernetesObject", "kubeovn/Vpc", "kubeovn/Subnet", "kubernetes/NetworkPolicy", "kubernetes/Service"):
        if token not in rest_go:
            errors.append(f"kubernetes_rest_client.go missing network provider token {token}")
    for pattern, label in (
        (r"NetworkStore\s+ports\.NetworkResourceStore", "NetworkStore ports.NetworkResourceStore"),
        (r"NetworkRenderer\s+ports\.NetworkProviderRenderer", "NetworkRenderer ports.NetworkProviderRenderer"),
        (r"NetworkDryRun\s+ports\.NetworkProviderDryRun", "NetworkDryRun ports.NetworkProviderDryRun"),
        (r"NetworkApply\s+ports\.NetworkProviderApply", "NetworkApply ports.NetworkProviderApply"),
        (r"NetworkStatus\s+ports\.NetworkProviderStatusReader", "NetworkStatus ports.NetworkProviderStatusReader"),
        (r"NetworkReconcile\s+ports\.NetworkStatusReconciler", "NetworkReconcile ports.NetworkStatusReconciler"),
        (r"NetworkResources\s+ports\.NetworkService", "NetworkResources ports.NetworkService"),
        (r"NewKubeOVNNetworkRenderer", "NewKubeOVNNetworkRenderer"),
        (r"NewKubeOVNNetworkProviderAdapter", "NewKubeOVNNetworkProviderAdapter"),
        (r"NewMetadataNetworkStore", "NewMetadataNetworkStore"),
        (r"NewLocalNetworkStatusReconciler", "NewLocalNetworkStatusReconciler"),
    ):
        if not re.search(pattern, bootstrap_go):
            errors.append(f"pkg/bootstrap/deps.go missing token {label}")


def validate_persistence(root: Path, errors: list[str]) -> None:
    migration = root / "deploy/migrations/20260520_005_network_resources.sql"
    if not migration.exists():
        errors.append("missing network persistence migration 20260520_005_network_resources.sql")
        return
    sql = migration.read_text(encoding="utf-8")
    for table in (
        "network_vpcs",
        "network_subnets",
        "network_security_groups",
        "network_load_balancers",
    ):
        if f"CREATE TABLE IF NOT EXISTS {table}" not in sql:
            errors.append(f"network migration missing table {table}")
        if f"ALTER TABLE {table} ENABLE ROW LEVEL SECURITY" not in sql:
            errors.append(f"network migration missing RLS enable for {table}")
        if f"DROP POLICY IF EXISTS tenant_isolation ON {table}" not in sql:
            errors.append(f"network migration missing tenant policy reset for {table}")
    for token in ("current_setting('app.current_tenant_id'", "CHECK (state IN", "PRIMARY KEY (tenant_id", "GRANT SELECT, INSERT, UPDATE, DELETE ON"):
        if token not in sql:
            errors.append(f"network migration missing token {token}")


def main() -> None:
    root = Path(__file__).resolve().parents[1]
    errors: list[str] = []
    validate_openapi(root, errors)
    validate_gateway(root, errors)
    validate_persistence(root, errors)
    fail(errors)
    print("network alpha contract valid")


if __name__ == "__main__":
    main()
