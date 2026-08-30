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
	TenantID                 string
	VolumeID                 string
	Name                     string
	SizeGiB                  int64
	StorageClass             string
	Zone                     string
	VolumeType               string
	IOPS                     int
	Encrypted                bool
	MountInstanceID          string
	MountRoute               string
	MountName                string
	SnapshotsCount           int
	AutoSnapshot             StorageVolumeAutoSnapshotPolicy
	OSInitStatus             string
	OSInitDevice             string
	MountHistory             []StorageVolumeMountHistoryEntry
	FromSnapshotID           string
	FromSnapshotName         string
	State                    StorageResourceState
	Reason                   string
	CreatedAt                time.Time
	UpdatedAt                time.Time
	DeletedAt                time.Time
	CreateIdempotencyKey     string
	CreateRequestFingerprint string
}

type StorageFilesystemRecord struct {
	TenantID                 string
	FilesystemID             string
	Name                     string
	Protocol                 string
	SizeGiB                  int64
	Endpoint                 string
	Zone                     string
	PerformanceMode          string
	MountTargets             []FilesystemMountTargetRecord
	Mounts                   int
	MountCommand             string
	AttachedInstances        []FilesystemAttachment
	State                    StorageResourceState
	Reason                   string
	CreatedAt                time.Time
	UpdatedAt                time.Time
	DeletedAt                time.Time
	CreateIdempotencyKey     string
	CreateRequestFingerprint string
}

type StorageObjectRecord struct {
	TenantID                 string
	ObjectID                 string
	Bucket                   string
	Key                      string
	SizeBytes                int64
	ContentType              string
	State                    StorageResourceState
	Reason                   string
	CreatedAt                time.Time
	UpdatedAt                time.Time
	DeletedAt                time.Time
	CreateIdempotencyKey     string
	CreateRequestFingerprint string
}

type StorageBucketRecord struct {
	TenantID                 string
	BucketID                 string
	Name                     string
	Region                   string
	Endpoint                 string
	AccessMode               string
	ACL                      string
	ACLLabel                 string
	StorageClass             string
	Versioning               string
	ObjectCount              int
	SizeBytes                int64
	LifecycleRules           []StorageBucketLifecycleRule
	LifecycleNote            string
	State                    StorageResourceState
	Reason                   string
	CreatedAt                time.Time
	UpdatedAt                time.Time
	DeletedAt                time.Time
	CreateIdempotencyKey     string
	CreateRequestFingerprint string
}

type StorageBucketLifecycleRule struct {
	ID               string
	Name             string
	Prefix           string
	ExpireDays       int
	ToInfrequentDays int
	Enabled          bool
}

type StorageBucketObjectEntry struct {
	Kind         string
	Name         string
	Key          string
	SizeBytes    *int64
	SizeLabel    string
	StorageClass string
	UpdatedAt    time.Time
}

type StorageBucketObjectListResult struct {
	Items      []StorageBucketObjectEntry
	Total      int
	Prefix     string
	NextCursor string
}

type StorageBucketObjectListRequest struct {
	TenantID string
	BucketID string
	Prefix   string
	Limit    int
	Cursor   string
}

type StorageBucketObjectDeleteRequest struct {
	TenantID string
	BucketID string
	Key      string
}

type StorageBucketObjectDeleteResult struct {
	BucketID string
	Key      string
	Deleted  bool
}

type StorageBucketPrefixCreateRequest struct {
	TenantID       string
	BucketID       string
	IdempotencyKey string
	Prefix         string
}

type StorageBucketPresignedURLRequest struct {
	TenantID     string
	BucketID     string
	Key          string
	Method       string
	ExpiresHours int
}

type StorageBucketACLUpdateRequest struct {
	TenantID       string
	BucketID       string
	IdempotencyKey string
	ACL            string
}

type StorageBucketClassUpdateRequest struct {
	TenantID       string
	BucketID       string
	IdempotencyKey string
	StorageClass   string
}

type StorageBucketLifecycleRulesUpdateRequest struct {
	TenantID       string
	BucketID       string
	IdempotencyKey string
	Rules          []StorageBucketLifecycleRule
}

type StorageBucketLifecycleRuleCreateRequest struct {
	TenantID         string
	BucketID         string
	IdempotencyKey   string
	Name             string
	Prefix           string
	ExpireDays       int
	ToInfrequentDays int
	Enabled          bool
}

type StorageBucketLifecycleRuleDeleteRequest struct {
	TenantID string
	BucketID string
	RuleID   string
}

type StorageBucketLifecycleRuleListResult struct {
	Items []StorageBucketLifecycleRule
	Total int
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
	TenantID                 string
	SnapshotID               string
	VolumeID                 string
	Name                     string
	Description              string
	Status                   VolumeSnapshotStatus
	SizeBytes                int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
	DeletedAt                time.Time
	CreateIdempotencyKey     string
	CreateRequestFingerprint string
}

type MountTargetStatus string

const (
	MountTargetCreating  MountTargetStatus = "creating"
	MountTargetAvailable MountTargetStatus = "available"
	MountTargetDeleting  MountTargetStatus = "deleting"
	MountTargetError     MountTargetStatus = "error"
)

type FilesystemMountTargetRecord struct {
	TenantID                 string
	MountTargetID            string
	FilesystemID             string
	SubnetID                 string
	VPCID                    string
	IPAddress                string
	Status                   MountTargetStatus
	CreatedAt                time.Time
	UpdatedAt                time.Time
	DeletedAt                time.Time
	CreateIdempotencyKey     string
	CreateRequestFingerprint string
}

type StorageVolumeAutoSnapshotPolicy struct {
	Enabled    bool
	RetainDays int
	Schedule   string
}

type StorageVolumeMountHistoryEntry struct {
	At     time.Time
	Action string
	Result string
	Target string
}

type VolumeOSInitGuide struct {
	Status string
	Device string
	Steps  []VolumeOSInitStep
	Hint   string
}

type VolumeOSInitStep struct {
	Title   string
	Command string
}

type FilesystemAttachment struct {
	InstanceID    string
	InstanceName  string
	InstanceRoute string
	MountPath     string
	IPAddress     string
	Protocol      string
	AutoMount     bool
	AttachedAt    time.Time
}

type FilesystemMountCommand struct {
	Command   string
	Protocol  string
	IPAddress string
	MountPath string
}

type StorageVolumeCreateRequest struct {
	TenantID        string
	IdempotencyKey  string
	Name            string
	SizeGiB         int64
	StorageClass    string
	Zone            string
	VolumeType      string
	Encrypted       bool
	MountInstanceID string
	MountRoute      string
}

type StorageFilesystemCreateRequest struct {
	TenantID        string
	IdempotencyKey  string
	Name            string
	Protocol        string
	SizeGiB         int64
	Zone            string
	PerformanceMode string
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
	SizeBytes      int64
	StorageClass   string
	ExpiresSeconds int
}

type StorageObjectDownloadRequest struct {
	TenantID       string
	ObjectID       string
	ExpiresSeconds int
}

type StorageObjectCompleteRequest struct {
	TenantID       string
	ObjectID       string
	IdempotencyKey string
}

type VolumeSnapshotCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	VolumeID       string
	Name           string
	Description    string
}

type StorageVolumeExpandRequest struct {
	TenantID       string
	VolumeID       string
	IdempotencyKey string
	SizeGiB        int64
}

type StorageVolumeMountRequest struct {
	TenantID       string
	VolumeID       string
	IdempotencyKey string
	InstanceID     string
	InstanceRoute  string
	MountName      string
}

type StorageVolumeUnmountRequest struct {
	TenantID       string
	VolumeID       string
	IdempotencyKey string
}

type StorageVolumeFromSnapshotRequest struct {
	TenantID       string
	VolumeID       string
	SnapshotID     string
	IdempotencyKey string
	Name           string
	SizeGiB        int64
	Zone           string
}

type StorageVolumeAutoSnapshotPolicyUpdateRequest struct {
	TenantID       string
	VolumeID       string
	IdempotencyKey string
	Enabled        bool
	RetainDays     int
	Schedule       string
}

type VolumeOSInitCompleteRequest struct {
	TenantID       string
	VolumeID       string
	IdempotencyKey string
	Mode           string
}

type StorageFilesystemExpandRequest struct {
	TenantID       string
	FilesystemID   string
	IdempotencyKey string
	SizeGiB        int64
}

type FilesystemMountTargetCreateRequest struct {
	TenantID       string
	FilesystemID   string
	IdempotencyKey string
	SubnetID       string
	VPCID          string
}

type StorageFilesystemMountRequest struct {
	TenantID       string
	FilesystemID   string
	IdempotencyKey string
	InstanceID     string
	InstanceRoute  string
	MountPath      string
	AutoMount      bool
}

type StorageFilesystemUnmountRequest struct {
	TenantID       string
	FilesystemID   string
	IdempotencyKey string
	InstanceID     string
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
	ExpandVolume(ctx context.Context, request StorageVolumeExpandRequest) (StorageVolumeRecord, error)
	MountVolume(ctx context.Context, request StorageVolumeMountRequest) (StorageVolumeRecord, error)
	UnmountVolume(ctx context.Context, request StorageVolumeUnmountRequest) (StorageVolumeRecord, error)
	CreateVolumeFromSnapshot(ctx context.Context, request StorageVolumeFromSnapshotRequest) (StorageVolumeRecord, error)
	SetVolumeAutoSnapshotPolicy(ctx context.Context, request StorageVolumeAutoSnapshotPolicyUpdateRequest) (StorageVolumeRecord, error)
	GetVolumeOSInitGuide(ctx context.Context, request StorageResourceGetRequest) (VolumeOSInitGuide, error)
	CompleteVolumeOSInit(ctx context.Context, request VolumeOSInitCompleteRequest) (StorageVolumeRecord, error)

	CreateFilesystem(ctx context.Context, request StorageFilesystemCreateRequest) (StorageFilesystemRecord, error)
	ListFilesystems(ctx context.Context, request StorageResourceListRequest) ([]StorageFilesystemRecord, error)
	GetFilesystem(ctx context.Context, request StorageResourceGetRequest) (StorageFilesystemRecord, error)
	DeleteFilesystem(ctx context.Context, request StorageResourceGetRequest) (StorageFilesystemRecord, error)
	ExpandFilesystem(ctx context.Context, request StorageFilesystemExpandRequest) (StorageFilesystemRecord, error)
	CreateFilesystemMountTarget(ctx context.Context, request FilesystemMountTargetCreateRequest) (FilesystemMountTargetRecord, error)
	MountFilesystem(ctx context.Context, request StorageFilesystemMountRequest) (StorageFilesystemRecord, error)
	UnmountFilesystem(ctx context.Context, request StorageFilesystemUnmountRequest) (StorageFilesystemRecord, error)
	GetFilesystemMountCommand(ctx context.Context, request StorageResourceGetRequest) (FilesystemMountCommand, error)

	CreateObject(ctx context.Context, request StorageObjectCreateRequest) (StorageObjectRecord, error)
	ListObjects(ctx context.Context, request StorageResourceListRequest) ([]StorageObjectRecord, error)
	GetObject(ctx context.Context, request StorageResourceGetRequest) (StorageObjectRecord, error)
	DeleteObject(ctx context.Context, request StorageResourceGetRequest) (StorageObjectRecord, error)
	CompleteStorageObject(ctx context.Context, request StorageObjectCompleteRequest) (StorageObjectRecord, error)

	CreateStorageBucket(ctx context.Context, request StorageBucketCreateRequest) (StorageBucketRecord, error)
	ListStorageBuckets(ctx context.Context, request StorageResourceListRequest) ([]StorageBucketRecord, error)
	GetStorageBucket(ctx context.Context, request StorageResourceGetRequest) (StorageBucketRecord, error)
	ListBucketObjects(ctx context.Context, request StorageBucketObjectListRequest) (StorageBucketObjectListResult, error)
	DeleteBucketObject(ctx context.Context, request StorageBucketObjectDeleteRequest) (StorageBucketObjectDeleteResult, error)
	CreateBucketPrefix(ctx context.Context, request StorageBucketPrefixCreateRequest) (StorageBucketObjectEntry, error)
	GenerateBucketObjectPresignedURL(ctx context.Context, request StorageBucketPresignedURLRequest) (StorageObjectDownloadRecord, error)
	SetStorageBucketACL(ctx context.Context, request StorageBucketACLUpdateRequest) (StorageBucketRecord, error)
	SetStorageBucketClass(ctx context.Context, request StorageBucketClassUpdateRequest) (StorageBucketRecord, error)
	ListStorageBucketLifecycleRules(ctx context.Context, request StorageResourceGetRequest) (StorageBucketLifecycleRuleListResult, error)
	SetStorageBucketLifecycleRules(ctx context.Context, request StorageBucketLifecycleRulesUpdateRequest) (StorageBucketLifecycleRuleListResult, error)
	CreateStorageBucketLifecycleRule(ctx context.Context, request StorageBucketLifecycleRuleCreateRequest) (StorageBucketLifecycleRule, error)
	DeleteStorageBucketLifecycleRule(ctx context.Context, request StorageBucketLifecycleRuleDeleteRequest) (StorageBucketLifecycleRuleListResult, error)
	CreateStorageObjectUpload(ctx context.Context, request StorageObjectUploadRequest) (StorageObjectUploadRecord, error)
	GetStorageObjectDownload(ctx context.Context, request StorageObjectDownloadRequest) (StorageObjectDownloadRecord, error)

	CreateVolumeSnapshot(ctx context.Context, request VolumeSnapshotCreateRequest) (VolumeSnapshotRecord, error)
	ListVolumeSnapshots(ctx context.Context, request VolumeSnapshotListRequest) ([]VolumeSnapshotRecord, error)
	ListFilesystemMountTargets(ctx context.Context, request FilesystemMountTargetListRequest) ([]FilesystemMountTargetRecord, error)
}

type StorageResourceStore interface {
	UpsertVolume(ctx context.Context, record StorageVolumeRecord) error
	GetVolume(ctx context.Context, tenantID string, volumeID string) (StorageVolumeRecord, error)
	ListVolumes(ctx context.Context, tenantID string) ([]StorageVolumeRecord, error)
	FindVolumeByCreateIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (StorageVolumeRecord, error)
	UpsertFilesystem(ctx context.Context, record StorageFilesystemRecord) error
	GetFilesystem(ctx context.Context, tenantID string, filesystemID string) (StorageFilesystemRecord, error)
	ListFilesystems(ctx context.Context, tenantID string) ([]StorageFilesystemRecord, error)
	UpsertObject(ctx context.Context, record StorageObjectRecord) error
	GetObject(ctx context.Context, tenantID string, objectID string) (StorageObjectRecord, error)
	ListObjects(ctx context.Context, tenantID string) ([]StorageObjectRecord, error)
	UpsertBucket(ctx context.Context, record StorageBucketRecord) error
	GetBucket(ctx context.Context, tenantID string, bucketID string) (StorageBucketRecord, error)
	ListBuckets(ctx context.Context, tenantID string) ([]StorageBucketRecord, error)
	FindBucketByCreateIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (StorageBucketRecord, error)
	ReplaceBucketLifecycleRules(ctx context.Context, tenantID string, bucketID string, rules []StorageBucketLifecycleRule) error
	UpsertVolumeSnapshot(ctx context.Context, record VolumeSnapshotRecord) error
	ListVolumeSnapshots(ctx context.Context, tenantID string, volumeID string) ([]VolumeSnapshotRecord, error)
	FindVolumeSnapshotByCreateIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (VolumeSnapshotRecord, error)
	UpsertFilesystemMountTarget(ctx context.Context, record FilesystemMountTargetRecord) error
	ListFilesystemMountTargets(ctx context.Context, tenantID string, filesystemID string) ([]FilesystemMountTargetRecord, error)
	FindFilesystemMountTargetByCreateIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (FilesystemMountTargetRecord, error)
	UpdateResourceState(ctx context.Context, request StorageResourceStateUpdateRequest) error
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
