package runtime

import (
	"context"
	"io"
	"strings"
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
	pending, err := service.GetObject(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   "tenant-a",
		ResourceID: upload.ObjectID,
	})
	if err != nil {
		t.Fatalf("GetObject() after upload error = %v", err)
	}
	if pending.State != ports.StorageResourcePending || pending.SizeBytes != 0 {
		t.Fatalf("pending object = %#v, want pending with zero size", pending)
	}

	objectStore.statMeta = ports.ObjectMetadata{
		Ref:         objectStore.uploadRef,
		ContentType: "application/octet-stream",
		SizeBytes:   4096,
		UpdatedAt:   objectStore.expiresAt,
	}
	completed, err := service.CompleteStorageObjectUpload(context.Background(), ports.StorageObjectCompleteRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "complete-upload-a",
		ObjectID:       upload.ObjectID,
	})
	if err != nil {
		t.Fatalf("CompleteStorageObjectUpload() error = %v", err)
	}
	if completed.State != ports.StorageResourceAvailable || completed.SizeBytes != 4096 {
		t.Fatalf("completed object = %#v, want available with reconciled size", completed)
	}
	if objectStore.statRef.TenantID != "tenant-a" || objectStore.statRef.ObjectKey != "llm/model.bin" {
		t.Fatalf("stat ref = %#v, want tenant-a llm/model.bin", objectStore.statRef)
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

func TestLocalStorageServiceObjectUploadPersistsToMetadataStore(t *testing.T) {
	tx := &fakeMetadataTx{
		row: bucketFakeRow{
			record: ports.StorageBucketRecord{
				TenantID:   "tenant-a",
				BucketID:   "bucket-a",
				Name:       "models-a",
				AccessMode: "private",
				CreatedAt:  time.Date(2026, 6, 30, 9, 0, 0, 0, time.UTC),
			},
		},
	}
	service := NewLocalStorageService(
		WithStorageResourceStore(NewMetadataStorageStore(fakeMetadataStore{tx: tx})),
		WithStorageObjectStore(&fakeObjectStore{
			uploadURL: "https://objects.local/upload/demo.txt",
			expiresAt: time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC),
		}),
	)

	upload, err := service.CreateStorageObjectUpload(context.Background(), ports.StorageObjectUploadRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "upload-a",
		BucketID:       "bucket-a",
		Key:            "demo.txt",
		ContentType:    "text/plain",
	})
	if err != nil {
		t.Fatalf("CreateStorageObjectUpload() error = %v", err)
	}
	if upload.ObjectID == "" {
		t.Fatal("upload.ObjectID is empty")
	}
	found := false
	for _, sql := range tx.execs {
		if strings.Contains(sql, "INSERT INTO storage_objects") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("execs = %#v, want storage_objects insert", tx.execs)
	}
}

type fakeObjectStore struct {
	ensureBucket ports.BucketClass
	uploadRef    ports.ObjectRef
	downloadRef  ports.ObjectRef
	deleteRef    ports.ObjectRef
	statRef      ports.ObjectRef
	statMeta     ports.ObjectMetadata
	statErr      error
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

func (s *fakeObjectStore) StatObject(_ context.Context, ref ports.ObjectRef) (ports.ObjectMetadata, error) {
	s.statRef = ref
	if s.statErr != nil {
		return ports.ObjectMetadata{}, s.statErr
	}
	if s.statMeta.Ref.TenantID != "" || s.statMeta.SizeBytes > 0 || s.statMeta.ContentType != "" {
		return s.statMeta, nil
	}
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
