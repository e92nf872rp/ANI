# Internal inference endpoint resolver implementation plan

Approved design: `docs/superpowers/specs/2026-09-29-inference-internal-endpoint-resolver-design.md`.
Scope: inference-service only; KB integration belongs to its owner. Work on the existing main
workspace, preserve unrelated changes, and do not commit/push or deploy as part of this batch.

## 1. Internal contract and generated Go client

Create `repo/api/proto/inference/resolver/v1/inference_endpoint_resolver.proto`:

```proto
syntax = "proto3";
package inference.internal.v1;
option go_package = "github.com/kubercloud/ani/pkg/generated/pb/inference/resolver/v1;inferenceinternalv1";
service InferenceEndpointResolver {
  rpc ResolveInternalEndpoint(ResolveInternalEndpointRequest) returns (ResolveInternalEndpointResponse);
}
message ResolveInternalEndpointRequest {
  string tenant_id = 1;
  string service_id = 2; // legacy direct lookup
  string served_model_name = 3; // preferred lookup key
}
message ResolveInternalEndpointResponse {
  string tenant_id = 1;
  string service_id = 2;
    string base_url = 3;
    string served_model_name = 4;
    string status = 5;
    string task = 6;
}
```

Use the `resolver` filesystem path because a Go package under `inference/internal/` cannot
be imported from Services. Keep the approved wire package `inference.internal.v1`.
Add a matching managed Go package override to `repo/api/proto/buf.gen.yaml`. Generate just
this proto with the repository's pinned protoc plugins, leaving previous generated code alone.

## 2. Resolver use case and behavior tests

Create `repo/services/inference-service/internal/service/endpoint_resolver_test.go`, then
`endpoint_resolver.go`. Use a tenant-scoped running-service lookup for
`served_model_name`, independent of AI Gateway publication, while retaining the direct service ID path for compatibility:

```go
type InternalEndpoint struct {
    TenantID, ServiceID uuid.UUID
    BaseURL, ServedModelName string
    Task domain.InferenceTask
    Status domain.Status
}
func (c *Controller) ResolveInternalEndpoint(ctx context.Context, tenantID, serviceID uuid.UUID) (InternalEndpoint, error)
func (c *Controller) ResolveInternalEndpointByServedModelName(ctx context.Context, tenantID uuid.UUID, servedModelName string) (InternalEndpoint, error)
```

Run the focused test before implementation (expected missing method failure), then implement
tenant/service ownership, deletion, running/readiness checks and the frozen execution task. Return stable sentinel errors
for not ready, missing endpoint and invalid endpoint; never return raw storage errors to clients.
Validate the stored URL against the tenant and service identity, HTTP scheme, explicit valid port,
no credentials/query/fragment/path, and normalize `.svc` to `.svc.cluster.local` before appending
`/v1`. Test successful short/FQDN addresses, cross-tenant/service targets, non-running/deleted
resources, stale generation, missing address, storage failure and invalid UUIDs.

## 3. Internal gRPC adapter and wiring

Create `repo/services/inference-service/internal/grpcapi/endpoint_resolver_test.go` and
`endpoint_resolver.go`. Register the independent service via `NewEndpointResolverServer`.
Use a narrow interface with the method in task 2. Validate request UUIDs; map not found to
`NOT_FOUND`, invalid arguments to `INVALID_ARGUMENT`, readiness/address errors to
`FAILED_PRECONDITION`, storage failures to a sanitized `INTERNAL` response.
Use an in-memory gRPC transport test to verify registration, serialized response fields,
error behavior and that public `InferenceControl` responses still omit private addresses.

Update `repo/services/inference-service/main.go` to instantiate one Controller and register
both existing control and new resolver services on the existing internal listener. Confirm
deployment trust assumptions; do not treat a tenant ID as caller authentication.

## 4. Documentation, review and verification

Write `repo/services/inference-service/internal-endpoint-resolver.md` with the RPC name,
request/response example, canonical tenant UUID requirement, error table, Go/Python generation
and grpcurl usage. Explain resolver lookup uses `served_model_name` (with legacy `service_id`
support), direct `/v1` calls use `served_model_name`, discovery is a snapshot,
and service-to-service network permissions must allow the actual KB/RAG caller.

Update `repo/development-records/inference-internal-endpoint-resolver-a.md`, its README index,
`repo/CURRENT-SPRINT.md` and `ANI-06-开发计划.md` with actual verification evidence only.

Run in `repo/` with CI Go 1.25.13 container when local Go differs:

```sh
go test ./services/inference-service/... -count=1
go test ./services/ani-gateway/internal/router/... -count=1
make test
make validate-services
make validate-doc-entrypoints
git diff --check
```

Inspect every result; distinguish prior working-tree generation drift from new regressions.
An independent review checks endpoint validation, tenant/state handling, registration and public
response isolation. No claim of deployment or KB end-to-end readiness without live evidence.
