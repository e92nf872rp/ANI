---
type: concept
title: Core Storage Subsystem
description: "Storage resource model (block volume, filesystem), ports.StorageService, object store (MinIO), vector store (Milvus), Rook-Ceph provider, state machine"
tags: [core, storage, volume, filesystem, object-store, vector-store, rook-ceph, minio, milvus]
---

# Core Storage Subsystem

## Storage Resource Model

| Resource | Port Type | Key Fields |
|----------|-----------|------------|
| **Block Volume** | `StorageVolumeRecord` | TenantID, VolumeID, Name, SizeGiB, StorageClass, Zone, State, MountInstanceID, FromSnapshotID, Encrypted, IOPS |
| **Filesystem** | `StorageFilesystemRecord` | TenantID, FilesystemID, Name, Protocol (NFS/SMB), SizeGiB, Endpoint, MountTargets, MountCommand, State |
| **Volume Snapshot** | `VolumeSnapshotRecord` | VolumeID, SnapshotID, SizeGiB, State, CreatedAt |

### Volume State Machine

`Pending → Available → Failed → Deleting → Deleted`

### Filesystem State Machine

`Pending → Available → Failed → Deleting → Deleted`

## Port Interfaces

| Port | Key Methods | Purpose |
|------|-------------|---------|
| `StorageService` | `CreateVolume`, `GetVolume`, `ListVolumes`, `DeleteVolume`, `CreateFilesystem`, ... | Volume/filesystem CRUD and overview |
| `StorageResourceStore` | `Create`, `Get`, `List`, `Delete`, `UpdateStatus` | Volume/filesystem record persistence (PG) |
| `StorageProviderRenderer` | `RenderVolumeManifest`, `RenderFilesystemManifest` | Generate Rook-Ceph CRDs |
| `StorageProviderDryRun` | `DryRun(manifests) -> DryRunResult` | Server-side dry-run |
| `StorageProviderApply` | `Apply(manifests) -> ApplyResult` | Apply storage manifests to K8s |
| `StorageStatusReconciler` | `Reconcile(ctx, records) -> []status` | Reconcile stored vs observed storage state |

## Object Store

`ports.ObjectStore` provides S3-compatible object storage via MinIO:

- `EnsureBucket(ctx, class)` — create bucket if not exists (bucket classes: model, dataset, kb-docs, branding)
- `PutObject(ctx, input) -> ObjectMetadata` — upload object
- `GetObject(ctx, ref) -> io.ReadCloser` — download object
- `DeleteObject(ctx, ref)` — delete object
- `SignURL(ctx, ref, duration) -> SignedURL` — pre-signed URL for direct access
- `ListObjects(ctx, ref, prefix) -> []ObjectMetadata` — list objects by prefix

Default adapter: `minio_store.go` (MinIO / S3-compatible).

## Vector Store

`ports.VectorStoreService` provides Milvus-backed vector collection management:

- `CreateCollection`, `GetCollection`, `ListCollections`, `DeleteCollection`
- `UpsertVectors`, `SearchVectors`, `DeleteVectors`
- Vector state machine: `Pending → Ready → Failed → Deleting → Deleted`
- `VectorStoreKnowledgeBaseRef` for KB-to-vector-store mapping

Default adapter: `milvus_store.go` (Milvus via pymilvus/grpc).

## References

- [Architecture Overview](../architecture/overview.md)
- [Ports Catalog](ports-catalog.md)
- [Adapters](adapters.md)
- Source: `repo/pkg/ports/storage_resources.go`, `repo/pkg/ports/object_store.go`, `repo/pkg/ports/vector_store.go`, `repo/pkg/adapters/runtime/storage_*.go`, `repo/pkg/adapters/objectstore/minio_store.go`, `repo/pkg/adapters/vectorstore/milvus_store.go`