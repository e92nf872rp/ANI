package ports

import (
	"context"
	"time"
)

type StorageResourceState string

const (
	StorageResourcePending   StorageResourceState = "pending"
	StorageResourceAvailable StorageResourceState = "available"
	StorageResourceFailed    StorageResourceState = "failed"
	StorageResourceDeleting  StorageResourceState = "deleting"
	StorageResourceDeleted   StorageResourceState = "deleted"
)

type StorageVolumeRecord struct {
	TenantID     string
	VolumeID     string
	Name         string
	SizeGiB      int64
	StorageClass string
	State        StorageResourceState
	Reason       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type StorageFilesystemRecord struct {
	TenantID     string
	FilesystemID string
	Name         string
	Protocol     string
	SizeGiB      int64
	Endpoint     string
	State        StorageResourceState
	Reason       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type StorageObjectRecord struct {
	TenantID    string
	ObjectID    string
	Bucket      string
	Key         string
	SizeBytes   int64
	ContentType string
	State       StorageResourceState
	Reason      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type StorageBucketRecord struct {
	TenantID    string
	BucketID    string
	Name        string
	Region      string
	AccessMode  string
	ObjectCount int
	SizeBytes   int64
	CreatedAt   time.Time
}

type StorageObjectUploadRecord struct {
	ObjectID  string
	UploadURL string
	ExpiresAt time.Time
}

type StorageObjectDownloadRecord struct {
	DownloadURL string
	ExpiresAt   time.Time
	ContentType string
	SizeBytes   int64
}

type VolumeSnapshotStatus string

const (
	VolumeSnapshotCreating  VolumeSnapshotStatus = "creating"
	VolumeSnapshotAvailable VolumeSnapshotStatus = "available"
	VolumeSnapshotError     VolumeSnapshotStatus = "error"
	VolumeSnapshotDeleting  VolumeSnapshotStatus = "deleting"
)

type VolumeSnapshotRecord struct {
	TenantID    string
	SnapshotID  string
	VolumeID    string
	Name        string
	Description string
	Status      VolumeSnapshotStatus
	SizeBytes   int64
	CreatedAt   time.Time
}

type MountTargetStatus string

const (
	MountTargetCreating  MountTargetStatus = "creating"
	MountTargetAvailable MountTargetStatus = "available"
	MountTargetDeleting  MountTargetStatus = "deleting"
	MountTargetError     MountTargetStatus = "error"
)

type FilesystemMountTargetRecord struct {
	TenantID      string
	MountTargetID string
	FilesystemID  string
	SubnetID      string
	IPAddress     string
	Status        MountTargetStatus
	CreatedAt     time.Time
}

type StorageVolumeCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	SizeGiB        int64
	StorageClass   string
}

type StorageFilesystemCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	Protocol       string
	SizeGiB        int64
}

type StorageObjectCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Bucket         string
	Key            string
	SizeBytes      int64
	ContentType    string
}

type StorageBucketCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	Region         string
	AccessMode     string
}

type StorageObjectUploadRequest struct {
	TenantID       string
	IdempotencyKey string
	BucketID       string
	Key            string
	ContentType    string
	ExpiresSeconds int
}

type StorageObjectDownloadRequest struct {
	TenantID       string
	ObjectID       string
	ExpiresSeconds int
}

type StorageObjectCompleteRequest struct {
	TenantID       string
	IdempotencyKey string
	ObjectID       string
}

type VolumeSnapshotCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	VolumeID       string
	Name           string
	Description    string
}

type StorageResourceGetRequest struct {
	TenantID   string
	ResourceID string
}

type StorageResourceListRequest struct {
	TenantID string
	Limit    int
	Cursor   string
}

type VolumeSnapshotListRequest struct {
	TenantID string
	VolumeID string
	Limit    int
	Cursor   string
}

type FilesystemMountTargetListRequest struct {
	TenantID     string
	FilesystemID string
	Limit        int
	Cursor       string
}

type StorageService interface {
	CreateVolume(ctx context.Context, request StorageVolumeCreateRequest) (StorageVolumeRecord, error)
	ListVolumes(ctx context.Context, request StorageResourceListRequest) ([]StorageVolumeRecord, error)
	GetVolume(ctx context.Context, request StorageResourceGetRequest) (StorageVolumeRecord, error)
	DeleteVolume(ctx context.Context, request StorageResourceGetRequest) (StorageVolumeRecord, error)

	CreateFilesystem(ctx context.Context, request StorageFilesystemCreateRequest) (StorageFilesystemRecord, error)
	ListFilesystems(ctx context.Context, request StorageResourceListRequest) ([]StorageFilesystemRecord, error)
	GetFilesystem(ctx context.Context, request StorageResourceGetRequest) (StorageFilesystemRecord, error)
	DeleteFilesystem(ctx context.Context, request StorageResourceGetRequest) (StorageFilesystemRecord, error)

	CreateObject(ctx context.Context, request StorageObjectCreateRequest) (StorageObjectRecord, error)
	ListObjects(ctx context.Context, request StorageResourceListRequest) ([]StorageObjectRecord, error)
	GetObject(ctx context.Context, request StorageResourceGetRequest) (StorageObjectRecord, error)
	DeleteObject(ctx context.Context, request StorageResourceGetRequest) (StorageObjectRecord, error)

	CreateStorageBucket(ctx context.Context, request StorageBucketCreateRequest) (StorageBucketRecord, error)
	ListStorageBuckets(ctx context.Context, request StorageResourceListRequest) ([]StorageBucketRecord, error)
	CreateStorageObjectUpload(ctx context.Context, request StorageObjectUploadRequest) (StorageObjectUploadRecord, error)
	CompleteStorageObjectUpload(ctx context.Context, request StorageObjectCompleteRequest) (StorageObjectRecord, error)
	GetStorageObjectDownload(ctx context.Context, request StorageObjectDownloadRequest) (StorageObjectDownloadRecord, error)

	CreateVolumeSnapshot(ctx context.Context, request VolumeSnapshotCreateRequest) (VolumeSnapshotRecord, error)
	ListVolumeSnapshots(ctx context.Context, request VolumeSnapshotListRequest) ([]VolumeSnapshotRecord, error)
	ListFilesystemMountTargets(ctx context.Context, request FilesystemMountTargetListRequest) ([]FilesystemMountTargetRecord, error)
}

type StorageResourceStore interface {
	UpsertVolume(ctx context.Context, record StorageVolumeRecord) error
	UpsertFilesystem(ctx context.Context, record StorageFilesystemRecord) error
	UpsertObject(ctx context.Context, record StorageObjectRecord) error
	UpdateResourceState(ctx context.Context, request StorageResourceStateUpdateRequest) error

	ListVolumes(ctx context.Context, tenantID string) ([]StorageVolumeRecord, error)
	GetVolume(ctx context.Context, tenantID string, volumeID string) (StorageVolumeRecord, error)
	ListFilesystems(ctx context.Context, tenantID string) ([]StorageFilesystemRecord, error)
	GetFilesystem(ctx context.Context, tenantID string, filesystemID string) (StorageFilesystemRecord, error)
	ListObjects(ctx context.Context, tenantID string) ([]StorageObjectRecord, error)
	GetObject(ctx context.Context, tenantID string, objectID string) (StorageObjectRecord, error)

	UpsertBucket(ctx context.Context, record StorageBucketRecord, idempotencyKey string) error
	ListBuckets(ctx context.Context, tenantID string) ([]StorageBucketRecord, error)
	GetBucket(ctx context.Context, tenantID string, bucketID string) (StorageBucketRecord, error)
	GetBucketByName(ctx context.Context, tenantID string, name string) (StorageBucketRecord, error)
	GetBucketByIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (StorageBucketRecord, error)

	UpsertVolumeSnapshot(ctx context.Context, record VolumeSnapshotRecord, idempotencyKey string) error
	ListVolumeSnapshots(ctx context.Context, tenantID string, volumeID string) ([]VolumeSnapshotRecord, error)
	GetVolumeSnapshotByIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (VolumeSnapshotRecord, error)

	UpsertFilesystemMountTarget(ctx context.Context, record FilesystemMountTargetRecord) error
	GetFilesystemMountTarget(ctx context.Context, tenantID string, filesystemID string) (FilesystemMountTargetRecord, error)
}

type StorageProviderRenderer interface {
	RenderVolume(ctx context.Context, record StorageVolumeRecord) ([]WorkloadManifest, error)
	RenderFilesystem(ctx context.Context, record StorageFilesystemRecord) ([]WorkloadManifest, error)
	RenderObject(ctx context.Context, record StorageObjectRecord) ([]WorkloadManifest, error)
	RenderVolumeSnapshot(ctx context.Context, record VolumeSnapshotRecord) ([]WorkloadManifest, error)
	RenderFilesystemMountTarget(ctx context.Context, record FilesystemMountTargetRecord) ([]WorkloadManifest, error)
}

type StorageProviderOperation string

const (
	StorageProviderOperationCreate StorageProviderOperation = "create"
	StorageProviderOperationDelete StorageProviderOperation = "delete"
)

type StorageProviderDryRunRequest struct {
	TenantID        string
	UserID          string
	ResourceKind    string
	ResourceID      string
	Operation       StorageProviderOperation
	Manifests       []WorkloadManifest
	PermissionProof string
	RequestedAt     time.Time
}

type StorageProviderDryRunResult struct {
	Accepted      bool
	Provider      string
	ManifestCount int
	ResourceRefs  []string
	Reason        string
	Warnings      []string
	CheckedAt     time.Time
}

type StorageProviderApplyRequest struct {
	TenantID        string
	UserID          string
	ResourceKind    string
	ResourceID      string
	Operation       StorageProviderOperation
	Manifests       []WorkloadManifest
	PermissionProof string
	DryRunResult    StorageProviderDryRunResult
	RequestedAt     time.Time
}

type StorageProviderApplyResult struct {
	Applied       bool
	Provider      string
	ManifestCount int
	Operation     StorageProviderOperation
	ResourceRefs  []string
	Reason        string
	Warnings      []string
	AppliedAt     time.Time
}

type StorageProviderStatusRequest struct {
	TenantID        string
	UserID          string
	ResourceKind    string
	ResourceID      string
	ApplyResult     StorageProviderApplyResult
	PermissionProof string
	RequestedAt     time.Time
}

type StorageProviderStatusResult struct {
	TenantID     string
	ResourceKind string
	ResourceID   string
	Provider     string
	ResourceRefs []string
	State        StorageResourceState
	Reason       string
	ObservedAt   time.Time
}

type StorageResourceStateUpdateRequest struct {
	TenantID     string
	ResourceKind string
	ResourceID   string
	State        StorageResourceState
	Reason       string
	UpdatedAt    time.Time
}

type StorageReconcileRequest struct {
	TenantID     string
	ResourceKind string
	ResourceID   string
	ApplyResult  StorageProviderApplyResult
	Observation  StorageProviderStatusResult
	RequestedAt  time.Time
}

type StorageReconcileResult struct {
	TenantID     string
	ResourceKind string
	ResourceID   string
	State        StorageResourceState
	Reason       string
	Persisted    bool
	ReconciledAt time.Time
}

type StorageProviderDryRun interface {
	DryRun(ctx context.Context, request StorageProviderDryRunRequest) (StorageProviderDryRunResult, error)
}

type StorageProviderApply interface {
	Apply(ctx context.Context, request StorageProviderApplyRequest) (StorageProviderApplyResult, error)
}

type StorageProviderStatusReader interface {
	Observe(ctx context.Context, request StorageProviderStatusRequest) (StorageProviderStatusResult, error)
}

type StorageStatusReconciler interface {
	Reconcile(ctx context.Context, request StorageReconcileRequest) (StorageReconcileResult, error)
}
