# Model tenant isolation and vector inference deployment design

Date: 2026-08-25
Status: user-reviewed and approved; implementation pending

## Goal

Close the observed cross-tenant metadata leak in model-service and allow an
`embedding` model version to reach `running` through the existing inference
control plane. Preserve current text-generation behavior and avoid a public API
contract change.

## Scope

This change covers:

- explicit tenant enforcement for all ModelRepository reads and mutations;
- task derivation from the existing immutable model capabilities;
- persistence of the derived task in the inference execution profile;
- task-aware vLLM launch arguments;
- task-aware runtime smoke checks for generation and embeddings;
- focused regression tests and relevant repository gates.

This change does not cover:

- a new public task selector;
- speech-to-text deployment;
- public `/v1/embeddings` routing through Envoy AI Gateway;
- changes to the currently stashed Envoy AI Gateway/C40 work;
- unrelated database-role or credential rotation work.

## Model tenant-isolation design

`tenant_id` remains parsed at the ModelService boundary. Repository operations
must also receive or obtain that tenant and include it in every authoritative
SQL predicate. PostgreSQL RLS remains enabled as defense in depth, but it is no
longer treated as the only application-level isolation boundary.

The following operations are tenant-bound:

- Get model: match both model ID and tenant ID.
- List models and count: match tenant ID before status/cursor pagination.
- Soft delete: update only the matching model ID and tenant ID.
- Create model version: insert only when the parent model belongs to the tenant;
  update the same tenant-owned parent model afterward.
- List model versions: join or use an existence predicate through the parent
  model and match its tenant ID.
- Get model version: retain its existing explicit parent-tenant predicate.

An ID owned by another tenant is indistinguishable from a missing ID. Read and
mutation calls return the existing NotFound semantics and do not disclose the
foreign resource's existence.

## Vector inference task design

The existing `Model.capabilities` field is the only task source. Catalog
resolution derives one internal immutable task:

| Capabilities | Frozen task | Result |
| --- | --- | --- |
| contains `text-generation` | `generate` | Existing behavior |
| `embedding` without `text-generation` | `embed` | Supported by vLLM |
| empty | `generate` | Existing backward-compatible default |
| only `speech-to-text` or unknown values | none | No compatible profile |

For a mixed `text-generation` + `embedding` model, `generate` wins to preserve
the behavior of existing models. SGLang embedding is not introduced in this
slice; embedding-only resolution selects the vLLM profiles.

The derived task is copied into the existing internal frozen execution profile.
It therefore survives persistence, restart, lifecycle operations, scale
rollback, and reconciliation without consulting mutable catalog metadata again.
No OpenAPI request field is added.

## Runtime behavior

For platform-generated vLLM commands:

- `generate` keeps the current command unchanged;
- `embed` adds the vLLM embedding task argument while retaining the current
  model path, served model name, host, port, CPU/GPU, and topology behavior.

A tenant-provided complete engine command remains authoritative and is not
rewritten. Catalog task metadata still controls the readiness smoke endpoint,
so a custom embedding command must expose the OpenAI-compatible embeddings API.

After `/health` succeeds, runtime smoke uses the frozen task:

- `generate`: POST `/v1/chat/completions`, then require a JSON `choices` field;
- `embed`: POST `/v1/embeddings`, then require a JSON `data` field containing at
  least one embedding result.

Both probes remain bounded, use the internal runtime endpoint, and return only
sanitized errors through the existing reconciliation failure mapping.

## Data flow

1. Gateway passes the authenticated tenant ID and model version ID to
   inference-service.
2. Catalog asks model-service for that version using the same tenant ID.
3. Model-service repository SQL proves parent ownership explicitly.
4. Catalog derives `generate` or `embed` from model capabilities.
5. Creator freezes the task into `ExecutionProfile` with image and artifact.
6. Runtime generates the task-appropriate vLLM command.
7. Reconciler waits for workload readiness, checks `/health`, then runs the
   task-appropriate OpenAI-compatible smoke request.
8. Only after the smoke succeeds does the inference service become `running`.

## Testing strategy

Implementation follows test-driven development.

Tenant isolation RED tests cover:

- cross-tenant GetModel returns NotFound;
- ListModels cannot return or count another tenant's rows;
- cross-tenant delete, create-version, and list-version operations cannot
  affect or expose another tenant's model;
- existing same-tenant behavior remains successful.

Vector deployment RED tests cover:

- embedding-only catalog resolution returns an embed-capable vLLM profile;
- text-generation and empty capabilities remain generate;
- mixed capabilities deterministically remain generate;
- speech-to-text remains incompatible;
- vLLM embed launch includes the embedding task argument;
- embedding smoke calls `/v1/embeddings` and validates `data`;
- generation smoke remains unchanged;
- worker passes the frozen task during create completion and rollback.

Focused model-service and inference-service tests run first, followed by the
repository's relevant Services and architecture gates plus `git diff --check`.

## Rollout and compatibility

The database schema and public API remain unchanged. Existing persisted
execution profiles that do not contain a task decode to the zero value and are
treated as `generate`, preserving current deployments. New embedding services
freeze `embed` at creation.

The Envoy AI Gateway/C40 implementation remains recoverable in the named stash
created before this work. Public embeddings routing is intentionally deferred
until that work is restored and reviewed as a separate route/auth change.
