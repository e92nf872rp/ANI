# Harbor Registry Provider Design

## Status

Approved design for implementing the already-approved Core API v1 registry
contract against the in-cluster Harbor 2.15.1 deployment. This document does
not change the v1 API contract.

## Goal

Replace the registry local profile with an opt-in real Harbor/Trivy provider
for the existing project, repository, artifact, permission, scan, and pull
secret operations. The provider must use the Harbor service reachable from the
Gateway Pod at `http://harbor.harbor.svc.cluster.local`.

## Scope

- Add a Harbor implementation of the existing `ports.ImageRegistry` boundary.
- Select the provider through Gateway configuration. `local` remains the
  default; `harbor` explicitly enables the real provider.
- Read the Harbor control-plane credential from a Kubernetes Secret mounted or
  injected into Gateway configuration. Never write credentials to repository
  files, API responses, logs, or development records.
- Map tenant IDs to Harbor project names and use Harbor repository, artifact,
  member, scan, and robot-account APIs for all existing v1 registry operations.
- Create or reuse a project-scoped, pull-only Harbor Robot Account when the
  existing pull-secret endpoint is called; create a Kubernetes
  `kubernetes.io/dockerconfigjson` Secret from that robot credential.
- Read tag references through a `RegistryImageReferenceReader` backed by ANI
  workload-instance records. A reader error fails deletion closed.
- Add unit, Gateway wiring, Kubernetes Secret, and controlled live-gate
  coverage.

## Out Of Scope

- New Core API paths or response fields.
- Harbor user, LDAP, retention, replication, garbage-collection, or global
  quota administration.
- Giving any workload the Harbor administrator credential.
- A separate `harbor-proxy` service. The existing ANI port/adapter boundary is
  sufficient for this provider.

## Architecture

`registryAPI` continues to depend only on `ports.ImageRegistry`. A small
Gateway provider factory selects one implementation:

- `REGISTRY_PROVIDER_MODE=local` creates `LocalImageRegistry`.
- `REGISTRY_PROVIDER_MODE=harbor` creates `HarborImageRegistry` using a
  narrowly scoped HTTP client and configuration loaded from Gateway runtime.
- Invalid provider configuration fails closed at startup; the Gateway never
  substitutes local response data for a Harbor failure.

The Harbor adapter owns request construction, pagination conversion, response
mapping, timeout handling, and upstream error classification. Gateway handlers
remain unaware of Harbor routes and credentials.

The tag-reference reader is an ANI read-only boundary. Its implementation
compares the requested canonical image against same-tenant container and GPU
container instance images, returns the existing v1 reference records, and
blocks Harbor deletion for either references or lookup failure.

## Operation Mapping

| ANI v1 operation | Harbor behavior |
| --- | --- |
| project create/list | Create/list tenant-named Harbor projects. |
| repository/artifact list | Read Harbor repositories and artifacts, including digest, tag, size, push time, and scan overview. |
| repository permission | Preserve the v1 repository field in the ANI response and map actions to a Harbor **project** member role: `pull`/`scan` -> Guest (4), `push` -> Developer (3), `delete` -> Maintainer (2). Existing members are updated with `PUT`; absent members are created with `POST`. Harbor authorization therefore applies to every repository in the project. |
| image/project scan | Read Harbor/Trivy scan overview and report state. |
| pull secret | Create or reuse a pull-only Robot Account, then create the workload namespace dockerconfigjson Secret. |
| tag references/delete | Query ANI workload references before deleting the Harbor artifact/tag; return the existing v1 conflict when a reference blocks deletion. |

## Security And Error Semantics

The Harbor administrator credential is used only by Gateway to administer
projects and project-scoped robot accounts. Each generated workload pull secret
contains a pull-only robot credential. Harbor transport and credential failures
use the existing Gateway internal-error behavior because the approved v1
registry operations do not declare provider-specific 502 or 503 responses.
They never surface upstream response bodies or secrets.

Harbor does not expose repository-scoped member roles. The approved ANI v1
endpoint is nevertheless implemented as directed: ANI retains the requested
repository in its response while the provider applies the least-privilege
available Harbor role at the containing project scope. This limitation must be
included in controlled live-gate evidence and the feature development record.

## Validation

Implementation follows test-driven development:

1. Add a failing fake-Harbor HTTP test for each adapter operation and verify
   its intended failure.
2. Implement the smallest adapter behavior needed to pass that test.
3. Add Gateway provider-selection and failure-mapping tests.
4. Add Kubernetes Secret creation tests with a fake Kubernetes client/boundary.
5. Run a controlled live gate using an isolated Harbor project and Robot
   Account. It may create temporary project, robot, and test resources, and
   must clean them up after verification.

The final live gate must prove the actual Gateway Pod reaches Harbor through
the in-cluster DNS name. It must not claim platform-wide production readiness.
