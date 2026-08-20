---
type: service
title: Model Service (gRPC)
description: "Standalone gRPC service: model CRUD (CreateModel, GetModel, ListModels, SoftDeleteModel), model version management (CreateVersion, ListVersions). PostgreSQL-backed PostgresModelRepo. Proto definition at api/proto/model/v1/."
tags: [services-layer, model, crud, gRPC, postgres]
---

# Model Service (gRPC)

## Overview

**Service**: `services/model-service/` · **Bootstrap**: uses Core `pkg/bootstrap/` (accepted_baseline — see [Architecture Baselines](../ci-cd/architecture-baselines.md))

## gRPC API (proto: `api/proto/model/v1/model_service.proto`)

| RPC | Request | Response | Status |
|-----|---------|----------|--------|
| `CreateModel` | CreateModelRequest → Model | ✅ Implemented |
| `GetModel` | GetModelRequest → Model | ✅ Implemented |
| `ListModels` | ListModelsRequest → ListModelsResponse | ✅ Implemented |
| `SoftDeleteModel` | SoftDeleteModelRequest → Empty | ✅ Implemented |
| `CreateVersion` | CreateVersionRequest → ModelVersion | ✅ Implemented |
| `ListVersions` | ListVersionsRequest → ListVersionsResponse | ✅ Implemented |

## Implementation

### ModelService struct

```go
type ModelService struct {
    modelv1.UnimplementedModelServiceServer
    db   *pgxpool.Pool
    repo repo.ModelRepo
}
```

| Method | Business Logic |
|--------|---------------|
| `CreateModel` | Validate tenant_id, name (pattern), display_name required → Begin tx → repo.Create → Commit → return protobuf model |
| `GetModel` | Parse tenant+model IDs → repo.GetByID(pool, modelID) → return model |
| `ListModels` | Parse tenant+cursor+limit+filter → repo.List → return items+cursor+total |
| `SoftDeleteModel` | Parse tenant+model IDs → Begin tx → repo.SoftDelete → Commit |
| `CreateVersion` | Validate tenant+model IDs, version string, format → Begin tx → repo.CreateVersion → Commit |
| `ListVersions` | Parse tenant+model IDs → repo.ListVersions(pool, modelID) → return versions |

### ModelRepo interface

| Method | Implementation |
|--------|---------------|
| `Create(ctx, tx, req) -> *Model` | INSERT INTO models (tenant_id, name, display_name, description, capabilities, source, source_repo_id) |
| `GetByID(ctx, pool, id) -> *Model` | SELECT ... WHERE id = $1 AND is_deleted = false |
| `List(ctx, pool, filter) -> []*Model, int64, string` | SELECT + COUNT + pagination with cursor decoding |
| `SoftDelete(ctx, tx, id) -> error` | UPDATE models SET is_deleted = true, deleted_at = NOW() WHERE id = $1 |
| `CreateVersion(ctx, tx, req) -> *ModelVersion` | INSERT INTO model_versions (model_id, version, format, storage_path, checksum_sha256, size_bytes, is_encrypted, encrypt_algo) |
| `ListVersions(ctx, pool, modelID) -> []*ModelVersion` | SELECT ... WHERE model_id = $1 ORDER BY created_at DESC |

## References

- [Services API](services-api.md) — Services OpenAPI spec for model endpoints
- [Inference Contracts](inference.md) — Models consumed by inference services
- Source: `services/model-service/`, `api/proto/model/v1/`, `services/model-service/internal/repo/`