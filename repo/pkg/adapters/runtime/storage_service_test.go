package runtime

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalStorageServiceVolumeDevProfile(t *testing.T) {
	service := NewLocalStorageService()
	volume, err := service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "storage-volume-a",
		Name:           "data-a",
		SizeGiB:        100,
		StorageClass:   "fast",
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if volume.VolumeID == "" || volume.State != ports.StorageResourceAvailable || volume.StorageClass != "fast" {
		t.Fatalf("volume = %#v, want available fast volume", volume)
	}
	replay, err := service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "storage-volume-a",
		Name:           "data-a-retry",
		SizeGiB:        200,
		StorageClass:   "slow",
	})
	if err != nil {
		t.Fatalf("CreateVolume replay error = %v", err)
	}
	if replay.VolumeID != volume.VolumeID || replay.SizeGiB != volume.SizeGiB {
		t.Fatalf("replay volume = %#v, want original %#v", replay, volume)
	}
	if _, err := service.GetVolume(context.Background(), ports.StorageResourceGetRequest{TenantID: "tenant-b", ResourceID: volume.VolumeID}); err == nil {
		t.Fatalf("GetVolume from another tenant succeeded, want isolation error")
	}
	deleted, err := service.DeleteVolume(context.Background(), ports.StorageResourceGetRequest{TenantID: "tenant-a", ResourceID: volume.VolumeID})
	if err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
	if deleted.State != ports.StorageResourceDeleted {
		t.Fatalf("deleted state = %q, want deleted", deleted.State)
	}
}

func TestLocalStorageServiceFilesystemAndObjectDevProfile(t *testing.T) {
	service := NewLocalStorageService()
	filesystem, err := service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "storage-fs-a",
		Name:           "shared",
		Protocol:       "cephfs",
		SizeGiB:        500,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem() error = %v", err)
	}
	if filesystem.FilesystemID == "" || filesystem.Protocol != "cephfs" || filesystem.Endpoint == "" {
		t.Fatalf("filesystem = %#v, want cephfs endpoint", filesystem)
	}
	object, err := service.CreateObject(context.Background(), ports.StorageObjectCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "storage-object-a",
		Bucket:         "models",
		Key:            "llm/model.bin",
		SizeBytes:      1024,
		ContentType:    "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("CreateObject() error = %v", err)
	}
	if object.ObjectID == "" || object.State != ports.StorageResourceAvailable || object.Bucket != "models" {
		t.Fatalf("object = %#v, want available object metadata", object)
	}
	objects, err := service.ListObjects(context.Background(), ports.StorageResourceListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListObjects() error = %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("objects = %d, want 1", len(objects))
	}
}

func TestLocalStorageServiceSnapshotsAndMountTargets(t *testing.T) {
	service := NewLocalStorageService()
	volume, err := service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "snapshot-volume-a",
		Name:           "db-data",
		SizeGiB:        8,
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	snapshot, err := service.CreateVolumeSnapshot(context.Background(), ports.VolumeSnapshotCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "snapshot-a",
		VolumeID:       volume.VolumeID,
		Name:           "db-data-snap",
		Description:    "daily backup",
	})
	if err != nil {
		t.Fatalf("CreateVolumeSnapshot error = %v", err)
	}
	retry, err := service.CreateVolumeSnapshot(context.Background(), ports.VolumeSnapshotCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "snapshot-a",
		VolumeID:       volume.VolumeID,
		Name:           "changed-name",
	})
	if err != nil {
		t.Fatalf("CreateVolumeSnapshot retry error = %v", err)
	}
	if retry.SnapshotID != snapshot.SnapshotID || retry.Name != snapshot.Name {
		t.Fatalf("idempotent snapshot = %+v, want original %+v", retry, snapshot)
	}
	snapshots, err := service.ListVolumeSnapshots(context.Background(), ports.VolumeSnapshotListRequest{TenantID: "tenant-a", VolumeID: volume.VolumeID})
	if err != nil {
		t.Fatalf("ListVolumeSnapshots error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Status != ports.VolumeSnapshotAvailable {
		t.Fatalf("snapshots = %+v, want one available snapshot", snapshots)
	}
	reloadedVolume, err := service.GetVolume(context.Background(), ports.StorageResourceGetRequest{TenantID: "tenant-a", ResourceID: volume.VolumeID})
	if err != nil {
		t.Fatalf("GetVolume after snapshot error = %v", err)
	}
	if reloadedVolume.SnapshotsCount != 1 {
		t.Fatalf("SnapshotsCount = %d, want 1", reloadedVolume.SnapshotsCount)
	}
	volumes, err := service.ListVolumes(context.Background(), ports.StorageResourceListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListVolumes after snapshot error = %v", err)
	}
	if len(volumes) != 1 || volumes[0].SnapshotsCount != 1 {
		t.Fatalf("ListVolumes = %+v, want one volume with snapshots_count=1", volumes)
	}

	filesystem, err := service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "mount-fs-a",
		Name:           "shared",
		SizeGiB:        32,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem error = %v", err)
	}
	targets, err := service.ListFilesystemMountTargets(context.Background(), ports.FilesystemMountTargetListRequest{
		TenantID:     "tenant-a",
		FilesystemID: filesystem.FilesystemID,
	})
	if err != nil {
		t.Fatalf("ListFilesystemMountTargets error = %v", err)
	}
	if len(targets) != 1 || targets[0].FilesystemID != filesystem.FilesystemID || targets[0].Status != ports.MountTargetAvailable {
		t.Fatalf("mount targets = %+v, want generated available target", targets)
	}
}

func TestLocalStorageServiceCanUseKubernetesStorageProviderPipeline(t *testing.T) {
	provider := &fakeStorageProvider{}
	service := NewLocalStorageService(
		WithStorageProvider(
			NewKubernetesStorageRenderer(),
			provider,
			provider,
			provider,
			StorageProviderExecutionConfig{
				UserID:          "ani-core-storage-provider",
				PermissionProof: "rbac-scope:storage.write",
			},
		),
		WithStorageServiceClock(func() time.Time { return time.Unix(3000, 0) }),
	)

	volume, err := service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "provider-volume-a",
		Name:           "provider-data",
		SizeGiB:        1,
		StorageClass:   "ani-rbd-ssd",
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	snapshot, err := service.CreateVolumeSnapshot(context.Background(), ports.VolumeSnapshotCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "provider-snapshot-a",
		VolumeID:       volume.VolumeID,
		Name:           "provider-data-snap",
	})
	if err != nil {
		t.Fatalf("CreateVolumeSnapshot error = %v", err)
	}
	filesystem, err := service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "provider-fs-a",
		Name:           "provider-shared",
		Protocol:       "nfs",
		SizeGiB:        1,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem error = %v", err)
	}
	targets, err := service.ListFilesystemMountTargets(context.Background(), ports.FilesystemMountTargetListRequest{
		TenantID:     "tenant-a",
		FilesystemID: filesystem.FilesystemID,
	})
	if err != nil {
		t.Fatalf("ListFilesystemMountTargets error = %v", err)
	}

	if volume.State != ports.StorageResourceAvailable || snapshot.Status != ports.VolumeSnapshotAvailable || len(targets) != 1 {
		t.Fatalf("provider resources volume=%+v snapshot=%+v targets=%+v, want available", volume, snapshot, targets)
	}
	if provider.dryRuns != 4 || provider.applies != 4 || provider.observes != 4 {
		t.Fatalf("provider calls dry=%d apply=%d observe=%d, want 4/4/4", provider.dryRuns, provider.applies, provider.observes)
	}
	wantKinds := []string{"volume", "volume_snapshot", "filesystem", "filesystem_mount_target"}
	for i, want := range wantKinds {
		if provider.dryRunKinds[i] != want {
			t.Fatalf("provider dry-run kinds = %#v, want %s at index %d", provider.dryRunKinds, want, i)
		}
	}
	if provider.lastDryRun.UserID != "ani-core-storage-provider" || provider.lastDryRun.PermissionProof == "" {
		t.Fatalf("provider execution identity = %#v, want explicit storage provider identity", provider.lastDryRun)
	}
}

func TestLocalStorageServiceBucketsAndSignedObjectURLsUseObjectStorePort(t *testing.T) {
	objectStore := &fakeObjectStore{
		uploadURL:   "https://objects.local/upload/model.bin",
		downloadURL: "https://objects.local/download/model.bin",
		expiresAt:   time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC),
	}
	service := NewLocalStorageService(WithStorageObjectStore(objectStore))

	bucket, err := service.CreateStorageBucket(context.Background(), ports.StorageBucketCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "bucket-a",
		Name:           "models-a",
		Region:         "local",
		AccessMode:     "private",
	})
	if err != nil {
		t.Fatalf("CreateStorageBucket() error = %v", err)
	}
	if bucket.BucketID == "" || bucket.Name != "models-a" || bucket.AccessMode != "private" {
		t.Fatalf("bucket = %#v, want private models-a bucket", bucket)
	}
	if objectStore.ensureBucket != ports.BucketClass("models-a") {
		t.Fatalf("EnsureBucket class = %q, want models-a", objectStore.ensureBucket)
	}

	replay, err := service.CreateStorageBucket(context.Background(), ports.StorageBucketCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "bucket-a",
		Name:           "changed-name",
	})
	if err != nil {
		t.Fatalf("CreateStorageBucket replay error = %v", err)
	}
	if replay.BucketID != bucket.BucketID || replay.Name != bucket.Name {
		t.Fatalf("replay bucket = %#v, want original %#v", replay, bucket)
	}

	upload, err := service.CreateStorageObjectUpload(context.Background(), ports.StorageObjectUploadRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "upload-a",
		BucketID:       bucket.BucketID,
		Key:            "llm/model.bin",
		ContentType:    "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("CreateStorageObjectUpload() error = %v", err)
	}
	if upload.UploadURL != objectStore.uploadURL || upload.ObjectID == "" || upload.ExpiresAt != objectStore.expiresAt {
		t.Fatalf("upload = %#v, want signed upload response", upload)
	}
	if objectStore.uploadRef.BucketClass != ports.BucketClass("models-a") || objectStore.uploadRef.ObjectKey != "llm/model.bin" {
		t.Fatalf("upload ref = %#v, want bucket models-a key llm/model.bin", objectStore.uploadRef)
	}

	download, err := service.GetStorageObjectDownload(context.Background(), ports.StorageObjectDownloadRequest{
		TenantID:       "tenant-a",
		ObjectID:       upload.ObjectID,
		ExpiresSeconds: 600,
	})
	if err != nil {
		t.Fatalf("GetStorageObjectDownload() error = %v", err)
	}
	if download.DownloadURL != objectStore.downloadURL || download.ContentType != "application/octet-stream" {
		t.Fatalf("download = %#v, want signed download response", download)
	}
	if objectStore.downloadRef.BucketClass != ports.BucketClass("models-a") || objectStore.downloadRef.ObjectKey != "llm/model.bin" {
		t.Fatalf("download ref = %#v, want bucket models-a key llm/model.bin", objectStore.downloadRef)
	}
	if _, err := service.DeleteObject(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   "tenant-a",
		ResourceID: upload.ObjectID,
	}); err != nil {
		t.Fatalf("DeleteObject() error = %v", err)
	}
	if objectStore.deleteRef.BucketClass != ports.BucketClass("models-a") || objectStore.deleteRef.ObjectKey != "llm/model.bin" {
		t.Fatalf("delete ref = %#v, want bucket models-a key llm/model.bin", objectStore.deleteRef)
	}

	buckets, err := service.ListStorageBuckets(context.Background(), ports.StorageResourceListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListStorageBuckets() error = %v", err)
	}
	if len(buckets) != 1 || buckets[0].ObjectCount != 0 {
		t.Fatalf("buckets = %#v, want one bucket with deleted object excluded", buckets)
	}
}

type fakeObjectStore struct {
	ensureBucket ports.BucketClass
	uploadRef    ports.ObjectRef
	downloadRef  ports.ObjectRef
	deleteRef    ports.ObjectRef
	uploadURL    string
	downloadURL  string
	expiresAt    time.Time
}

func (s *fakeObjectStore) EnsureBucket(_ context.Context, class ports.BucketClass) error {
	s.ensureBucket = class
	return nil
}

func (s *fakeObjectStore) Health(context.Context) error {
	return nil
}

func (s *fakeObjectStore) PutObject(context.Context, ports.PutObjectInput) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, ports.ErrUnsupported
}

func (s *fakeObjectStore) GetObject(context.Context, ports.ObjectRef) (io.ReadCloser, ports.ObjectMetadata, error) {
	return nil, ports.ObjectMetadata{}, ports.ErrUnsupported
}

func (s *fakeObjectStore) DeleteObject(_ context.Context, ref ports.ObjectRef) error {
	s.deleteRef = ref
	return nil
}

func (s *fakeObjectStore) StatObject(context.Context, ports.ObjectRef) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, ports.ErrUnsupported
}

func (s *fakeObjectStore) SignedUploadURL(_ context.Context, ref ports.ObjectRef, _ time.Duration) (ports.SignedURL, error) {
	s.uploadRef = ref
	return ports.SignedURL{URL: s.uploadURL, ExpiresAt: s.expiresAt}, nil
}

func (s *fakeObjectStore) SignedDownloadURL(_ context.Context, ref ports.ObjectRef, _ time.Duration) (ports.SignedURL, error) {
	s.downloadRef = ref
	return ports.SignedURL{URL: s.downloadURL, ExpiresAt: s.expiresAt}, nil
}

type fakeStorageProvider struct {
	dryRuns     int
	applies     int
	observes    int
	dryRunKinds []string
	lastDryRun  ports.StorageProviderDryRunRequest
}

func (p *fakeStorageProvider) DryRun(_ context.Context, request ports.StorageProviderDryRunRequest) (ports.StorageProviderDryRunResult, error) {
	p.dryRuns++
	p.dryRunKinds = append(p.dryRunKinds, request.ResourceKind)
	p.lastDryRun = request
	return ports.StorageProviderDryRunResult{
		Accepted:      true,
		Provider:      "kubernetes",
		ManifestCount: len(request.Manifests),
		ResourceRefs:  []string{"kubernetes/" + request.Manifests[0].Kind + "/" + request.Manifests[0].Name},
		Reason:        "accepted by fake Kubernetes storage provider",
		CheckedAt:     time.Unix(3001, 0),
	}, nil
}

func (p *fakeStorageProvider) Apply(_ context.Context, request ports.StorageProviderApplyRequest) (ports.StorageProviderApplyResult, error) {
	p.applies++
	return ports.StorageProviderApplyResult{
		Applied:       true,
		Provider:      "kubernetes",
		ManifestCount: len(request.Manifests),
		Operation:     request.Operation,
		ResourceRefs:  append([]string(nil), request.DryRunResult.ResourceRefs...),
		Reason:        "applied by fake Kubernetes storage provider",
		AppliedAt:     time.Unix(3002, 0),
	}, nil
}

func (p *fakeStorageProvider) Observe(_ context.Context, request ports.StorageProviderStatusRequest) (ports.StorageProviderStatusResult, error) {
	p.observes++
	return ports.StorageProviderStatusResult{
		TenantID:     request.TenantID,
		ResourceKind: request.ResourceKind,
		ResourceID:   request.ResourceID,
		Provider:     request.ApplyResult.Provider,
		ResourceRefs: append([]string(nil), request.ApplyResult.ResourceRefs...),
		State:        ports.StorageResourceAvailable,
		Reason:       "observed by fake Kubernetes storage provider",
		ObservedAt:   time.Unix(3003, 0),
	}, nil
}

var _ ports.StorageProviderDryRun = (*fakeStorageProvider)(nil)
var _ ports.StorageProviderApply = (*fakeStorageProvider)(nil)
var _ ports.StorageProviderStatusReader = (*fakeStorageProvider)(nil)

func TestLocalStorageServiceBucketConsoleAPIs(t *testing.T) {
	objectStore := &fakeObjectStore{
		uploadURL:   "https://objects.local/upload/readme.md",
		downloadURL: "https://objects.local/download/readme.md",
		expiresAt:   time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
	}
	service := NewLocalStorageService(WithStorageObjectStore(objectStore))

	bucket, err := service.CreateStorageBucket(context.Background(), ports.StorageBucketCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "bucket-console-a",
		Name:           "datasets-a",
		Region:         "cn-east-1",
		AccessMode:     "private",
	})
	if err != nil {
		t.Fatalf("CreateStorageBucket() error = %v", err)
	}
	if bucket.Endpoint == "" || bucket.ACL != "private" || bucket.StorageClass != "standard" || bucket.Versioning != "disabled" {
		t.Fatalf("bucket defaults = %#v, want endpoint/acl/storage_class/versioning defaults", bucket)
	}

	prefix, err := service.CreateBucketPrefix(context.Background(), ports.StorageBucketPrefixCreateRequest{
		TenantID:       "tenant-a",
		BucketID:       bucket.BucketID,
		IdempotencyKey: "prefix-a",
		Prefix:         "models",
	})
	if err != nil {
		t.Fatalf("CreateBucketPrefix() error = %v", err)
	}
	if prefix.Kind != "prefix" || prefix.Key != "models/" {
		t.Fatalf("prefix = %#v, want models/ prefix entry", prefix)
	}

	upload, err := service.CreateStorageObjectUpload(context.Background(), ports.StorageObjectUploadRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "upload-console-a",
		BucketID:       bucket.BucketID,
		Key:            "models/readme.md",
		ContentType:    "text/markdown",
		SizeBytes:      1200,
		StorageClass:   "standard",
	})
	if err != nil {
		t.Fatalf("CreateStorageObjectUpload() error = %v", err)
	}

	objects, err := service.ListBucketObjects(context.Background(), ports.StorageBucketObjectListRequest{
		TenantID: "tenant-a",
		BucketID: bucket.BucketID,
		Prefix:   "",
	})
	if err != nil {
		t.Fatalf("ListBucketObjects() error = %v", err)
	}
	if objects.Total < 1 {
		t.Fatalf("objects = %#v, want at least models/ prefix", objects)
	}
	foundPrefix := false
	for _, item := range objects.Items {
		if item.Kind == "prefix" && item.Key == "models/" {
			foundPrefix = true
		}
	}
	if !foundPrefix {
		t.Fatalf("objects = %#v, want models/ prefix entry", objects)
	}

	listedUnderPrefix, err := service.ListBucketObjects(context.Background(), ports.StorageBucketObjectListRequest{
		TenantID: "tenant-a",
		BucketID: bucket.BucketID,
		Prefix:   "models/",
	})
	if err != nil {
		t.Fatalf("ListBucketObjects(models/) error = %v", err)
	}
	foundObject := false
	for _, item := range listedUnderPrefix.Items {
		if item.Kind == "object" && item.Key == "models/readme.md" {
			foundObject = true
			if item.SizeBytes == nil || *item.SizeBytes != 1200 {
				t.Fatalf("object size = %#v, want 1200", item)
			}
		}
	}
	if !foundObject {
		t.Fatalf("listedUnderPrefix = %#v, want models/readme.md object", listedUnderPrefix)
	}

	aclBucket, err := service.SetStorageBucketACL(context.Background(), ports.StorageBucketACLUpdateRequest{
		TenantID:       "tenant-a",
		BucketID:       bucket.BucketID,
		IdempotencyKey: "acl-a",
		ACL:            "tenant_read",
	})
	if err != nil {
		t.Fatalf("SetStorageBucketACL() error = %v", err)
	}
	if aclBucket.ACL != "tenant_read" || aclBucket.AccessMode != "public_read" || aclBucket.ACLLabel == "" {
		t.Fatalf("acl bucket = %#v, want tenant_read/public_read", aclBucket)
	}

	classBucket, err := service.SetStorageBucketClass(context.Background(), ports.StorageBucketClassUpdateRequest{
		TenantID:       "tenant-a",
		BucketID:       bucket.BucketID,
		IdempotencyKey: "class-a",
		StorageClass:   "infrequent_access",
	})
	if err != nil {
		t.Fatalf("SetStorageBucketClass() error = %v", err)
	}
	if classBucket.StorageClass != "infrequent_access" {
		t.Fatalf("class bucket = %#v, want infrequent_access", classBucket)
	}

	rule, err := service.CreateStorageBucketLifecycleRule(context.Background(), ports.StorageBucketLifecycleRuleCreateRequest{
		TenantID:         "tenant-a",
		BucketID:         bucket.BucketID,
		IdempotencyKey:   "rule-a",
		Name:             "日志过期",
		Prefix:           "logs/",
		ExpireDays:       90,
		ToInfrequentDays: 30,
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("CreateStorageBucketLifecycleRule() error = %v", err)
	}
	if rule.ID == "" || rule.Name != "日志过期" {
		t.Fatalf("rule = %#v, want named lifecycle rule", rule)
	}
	rules, err := service.ListStorageBucketLifecycleRules(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   "tenant-a",
		ResourceID: bucket.BucketID,
	})
	if err != nil {
		t.Fatalf("ListStorageBucketLifecycleRules() error = %v", err)
	}
	if rules.Total != 1 {
		t.Fatalf("rules = %#v, want 1 rule", rules)
	}

	presigned, err := service.GenerateBucketObjectPresignedURL(context.Background(), ports.StorageBucketPresignedURLRequest{
		TenantID:     "tenant-a",
		BucketID:     bucket.BucketID,
		Key:          "models/readme.md",
		Method:       "GET",
		ExpiresHours: 2,
	})
	if err != nil {
		t.Fatalf("GenerateBucketObjectPresignedURL() error = %v", err)
	}
	if presigned.DownloadURL != objectStore.downloadURL {
		t.Fatalf("presigned = %#v, want download URL from object store", presigned)
	}

	deleted, err := service.DeleteBucketObject(context.Background(), ports.StorageBucketObjectDeleteRequest{
		TenantID: "tenant-a",
		BucketID: bucket.BucketID,
		Key:      "models/readme.md",
	})
	if err != nil {
		t.Fatalf("DeleteBucketObject() error = %v", err)
	}
	if !deleted.Deleted || deleted.Key != "models/readme.md" {
		t.Fatalf("deleted = %#v, want deleted models/readme.md", deleted)
	}
	if objectStore.deleteRef.ObjectKey != "models/readme.md" {
		t.Fatalf("delete ref = %#v, want models/readme.md", objectStore.deleteRef)
	}

	remaining, err := service.DeleteStorageBucketLifecycleRule(context.Background(), ports.StorageBucketLifecycleRuleDeleteRequest{
		TenantID: "tenant-a",
		BucketID: bucket.BucketID,
		RuleID:   rule.ID,
	})
	if err != nil {
		t.Fatalf("DeleteStorageBucketLifecycleRule() error = %v", err)
	}
	if remaining.Total != 0 {
		t.Fatalf("remaining rules = %#v, want empty", remaining)
	}

	_ = upload
}

func TestLocalStorageServiceVolumeOperations(t *testing.T) {
	service := NewLocalStorageService()
	volume, err := service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "volume-ops-a",
		Name:            "data-ops",
		SizeGiB:         100,
		StorageClass:    "standard",
		Zone:            "az-a",
		VolumeType:      "high_performance_ssd",
		Encrypted:       true,
		MountInstanceID: "vm-001",
		MountRoute:      "/compute/instances/vm",
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if volume.VolumeType != "high_performance_ssd" || volume.IOPS != 20000 || volume.OSInitStatus != "pending" || len(volume.MountHistory) != 1 {
		t.Fatalf("volume defaults = %#v, want console fields", volume)
	}
	expanded, err := service.ExpandVolume(context.Background(), ports.StorageVolumeExpandRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		IdempotencyKey: "volume-expand-a",
		SizeGiB:        150,
	})
	if err != nil {
		t.Fatalf("ExpandVolume() error = %v", err)
	}
	if expanded.SizeGiB != 150 {
		t.Fatalf("expanded size = %d, want 150", expanded.SizeGiB)
	}
	mounted, err := service.MountVolume(context.Background(), ports.StorageVolumeMountRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		IdempotencyKey: "volume-mount-a",
		InstanceID:     "container-001",
		InstanceRoute:  "/compute/instances/container",
		MountName:      "data",
	})
	if err != nil {
		t.Fatalf("MountVolume() error = %v", err)
	}
	if mounted.MountInstanceID != "container-001" || mounted.MountName != "data" {
		t.Fatalf("mounted = %#v, want container mount", mounted)
	}
	policy, err := service.SetVolumeAutoSnapshotPolicy(context.Background(), ports.StorageVolumeAutoSnapshotPolicyUpdateRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		IdempotencyKey: "volume-policy-a",
		Enabled:        true,
		RetainDays:     30,
		Schedule:       "daily@02:00",
	})
	if err != nil {
		t.Fatalf("SetVolumeAutoSnapshotPolicy() error = %v", err)
	}
	if !policy.AutoSnapshot.Enabled || policy.AutoSnapshot.RetainDays != 30 {
		t.Fatalf("policy = %#v, want enabled 30-day policy", policy.AutoSnapshot)
	}
	guide, err := service.GetVolumeOSInitGuide(context.Background(), ports.StorageResourceGetRequest{TenantID: "tenant-a", ResourceID: volume.VolumeID})
	if err != nil {
		t.Fatalf("GetVolumeOSInitGuide() error = %v", err)
	}
	if guide.Device == "" || len(guide.Steps) == 0 {
		t.Fatalf("guide = %#v, want device and steps", guide)
	}
	completed, err := service.CompleteVolumeOSInit(context.Background(), ports.VolumeOSInitCompleteRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		IdempotencyKey: "volume-os-init-a",
		Mode:           "done",
	})
	if err != nil {
		t.Fatalf("CompleteVolumeOSInit() error = %v", err)
	}
	if completed.OSInitStatus != "done" {
		t.Fatalf("os init status = %q, want done", completed.OSInitStatus)
	}
	snapshot, err := service.CreateVolumeSnapshot(context.Background(), ports.VolumeSnapshotCreateRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		IdempotencyKey: "volume-snapshot-a",
		Name:           "snap-a",
	})
	if err != nil {
		t.Fatalf("CreateVolumeSnapshot() error = %v", err)
	}
	restored, err := service.CreateVolumeFromSnapshot(context.Background(), ports.StorageVolumeFromSnapshotRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		SnapshotID:     snapshot.SnapshotID,
		IdempotencyKey: "volume-restore-a",
		Name:           "restored-a",
		SizeGiB:        150,
	})
	if err != nil {
		t.Fatalf("CreateVolumeFromSnapshot() error = %v", err)
	}
	if restored.FromSnapshotID != snapshot.SnapshotID || restored.SizeGiB != 150 {
		t.Fatalf("restored = %#v, want snapshot origin", restored)
	}
	unmounted, err := service.UnmountVolume(context.Background(), ports.StorageVolumeUnmountRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		IdempotencyKey: "volume-unmount-a",
	})
	if err != nil {
		t.Fatalf("UnmountVolume() error = %v", err)
	}
	if unmounted.MountInstanceID != "" {
		t.Fatalf("unmounted = %#v, want no mount target", unmounted)
	}
}

func TestLocalStorageServiceFilesystemOperations(t *testing.T) {
	service := NewLocalStorageService()
	filesystem, err := service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "fs-ops-a",
		Name:            "shared-ops",
		Protocol:        "nfs",
		SizeGiB:         500,
		Zone:            "az-a",
		PerformanceMode: "throughput",
	})
	if err != nil {
		t.Fatalf("CreateFilesystem() error = %v", err)
	}
	if filesystem.PerformanceMode != "throughput" || filesystem.MountCommand == "" {
		t.Fatalf("filesystem = %#v, want throughput and mount command", filesystem)
	}
	expanded, err := service.ExpandFilesystem(context.Background(), ports.StorageFilesystemExpandRequest{
		TenantID:       "tenant-a",
		FilesystemID:   filesystem.FilesystemID,
		IdempotencyKey: "fs-expand-a",
		SizeGiB:        700,
	})
	if err != nil {
		t.Fatalf("ExpandFilesystem() error = %v", err)
	}
	if expanded.SizeGiB != 700 {
		t.Fatalf("expanded size = %d, want 700", expanded.SizeGiB)
	}
	target, err := service.CreateFilesystemMountTarget(context.Background(), ports.FilesystemMountTargetCreateRequest{
		TenantID:       "tenant-a",
		FilesystemID:   filesystem.FilesystemID,
		IdempotencyKey: "fs-target-a",
		SubnetID:       "subnet-a",
		VPCID:          "vpc-a",
	})
	if err != nil {
		t.Fatalf("CreateFilesystemMountTarget() error = %v", err)
	}
	if target.SubnetID != "subnet-a" || target.IPAddress == "" {
		t.Fatalf("target = %#v, want subnet and IP", target)
	}
	mounted, err := service.MountFilesystem(context.Background(), ports.StorageFilesystemMountRequest{
		TenantID:       "tenant-a",
		FilesystemID:   filesystem.FilesystemID,
		IdempotencyKey: "fs-mount-a",
		InstanceID:     "vm-001",
		InstanceRoute:  "/compute/instances/vm",
		MountPath:      "/mnt/share",
		AutoMount:      true,
	})
	if err != nil {
		t.Fatalf("MountFilesystem() error = %v", err)
	}
	if mounted.Mounts != 1 || len(mounted.AttachedInstances) != 1 || mounted.AttachedInstances[0].IPAddress == "" {
		t.Fatalf("mounted = %#v, want one attachment with IP", mounted)
	}
	command, err := service.GetFilesystemMountCommand(context.Background(), ports.StorageResourceGetRequest{TenantID: "tenant-a", ResourceID: filesystem.FilesystemID})
	if err != nil {
		t.Fatalf("GetFilesystemMountCommand() error = %v", err)
	}
	if command.Command == "" || command.Protocol != "nfs" {
		t.Fatalf("command = %#v, want nfs mount command", command)
	}
	unmounted, err := service.UnmountFilesystem(context.Background(), ports.StorageFilesystemUnmountRequest{
		TenantID:       "tenant-a",
		FilesystemID:   filesystem.FilesystemID,
		IdempotencyKey: "fs-unmount-a",
		InstanceID:     "vm-001",
	})
	if err != nil {
		t.Fatalf("UnmountFilesystem() error = %v", err)
	}
	if unmounted.Mounts != 0 || len(unmounted.AttachedInstances) != 0 {
		t.Fatalf("unmounted = %#v, want no attachments", unmounted)
	}
}
