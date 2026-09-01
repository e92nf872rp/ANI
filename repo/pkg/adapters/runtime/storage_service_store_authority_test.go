package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// sharedMemoryStorageStore is a process-local StorageResourceStore used to prove
// two LocalStorageService instances share PG-shaped authority without a real DB.
type sharedMemoryStorageStore struct {
	mu          sync.Mutex
	volumes     map[string]ports.StorageVolumeRecord
	byCreate    map[string]string
	buckets     map[string]ports.StorageBucketRecord
	bucketIdem  map[string]string
	snapshots   map[string]ports.VolumeSnapshotRecord
	snapIdem    map[string]string
	mounts      map[string]ports.FilesystemMountTargetRecord
	mountIdem   map[string]string
	filesystems map[string]ports.StorageFilesystemRecord
	objects     map[string]ports.StorageObjectRecord
}

func newSharedMemoryStorageStore() *sharedMemoryStorageStore {
	return &sharedMemoryStorageStore{
		volumes:     map[string]ports.StorageVolumeRecord{},
		byCreate:    map[string]string{},
		buckets:     map[string]ports.StorageBucketRecord{},
		bucketIdem:  map[string]string{},
		snapshots:   map[string]ports.VolumeSnapshotRecord{},
		snapIdem:    map[string]string{},
		mounts:      map[string]ports.FilesystemMountTargetRecord{},
		mountIdem:   map[string]string{},
		filesystems: map[string]ports.StorageFilesystemRecord{},
		objects:     map[string]ports.StorageObjectRecord{},
	}
}

func (s *sharedMemoryStorageStore) key(tenantID, id string) string {
	return tenantID + "/" + id
}

func (s *sharedMemoryStorageStore) UpsertVolume(_ context.Context, record ports.StorageVolumeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.volumes[s.key(record.TenantID, record.VolumeID)] = record
	if record.CreateIdempotencyKey != "" {
		s.byCreate[record.TenantID+"/"+record.CreateIdempotencyKey] = record.VolumeID
	}
	return nil
}

func (s *sharedMemoryStorageStore) GetVolume(_ context.Context, tenantID string, volumeID string) (ports.StorageVolumeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.volumes[s.key(tenantID, volumeID)]
	if !ok || record.State == ports.StorageResourceDeleted || !record.DeletedAt.IsZero() {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *sharedMemoryStorageStore) ListVolumes(_ context.Context, tenantID string) ([]ports.StorageVolumeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.StorageVolumeRecord, 0)
	for _, record := range s.volumes {
		if record.TenantID == tenantID && record.State != ports.StorageResourceDeleted && record.DeletedAt.IsZero() {
			out = append(out, record)
		}
	}
	return out, nil
}

func (s *sharedMemoryStorageStore) FindVolumeByCreateIdempotency(_ context.Context, tenantID string, idempotencyKey string) (ports.StorageVolumeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	volumeID, ok := s.byCreate[tenantID+"/"+idempotencyKey]
	if !ok {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	record, ok := s.volumes[s.key(tenantID, volumeID)]
	if !ok {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *sharedMemoryStorageStore) UpsertFilesystem(_ context.Context, record ports.StorageFilesystemRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filesystems[s.key(record.TenantID, record.FilesystemID)] = record
	return nil
}
func (s *sharedMemoryStorageStore) GetFilesystem(_ context.Context, tenantID string, filesystemID string) (ports.StorageFilesystemRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.filesystems[s.key(tenantID, filesystemID)]
	if !ok || record.State == ports.StorageResourceDeleted {
		return ports.StorageFilesystemRecord{}, ports.ErrNotFound
	}
	return record, nil
}
func (s *sharedMemoryStorageStore) ListFilesystems(_ context.Context, tenantID string) ([]ports.StorageFilesystemRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.StorageFilesystemRecord, 0)
	for _, record := range s.filesystems {
		if record.TenantID == tenantID && record.State != ports.StorageResourceDeleted {
			out = append(out, record)
		}
	}
	return out, nil
}
func (s *sharedMemoryStorageStore) UpsertObject(_ context.Context, record ports.StorageObjectRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[s.key(record.TenantID, record.ObjectID)] = record
	return nil
}
func (s *sharedMemoryStorageStore) GetObject(_ context.Context, tenantID string, objectID string) (ports.StorageObjectRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.objects[s.key(tenantID, objectID)]
	if !ok || record.State == ports.StorageResourceDeleted {
		return ports.StorageObjectRecord{}, ports.ErrNotFound
	}
	return record, nil
}
func (s *sharedMemoryStorageStore) ListObjects(_ context.Context, tenantID string) ([]ports.StorageObjectRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.StorageObjectRecord, 0)
	for _, record := range s.objects {
		if record.TenantID == tenantID && record.State != ports.StorageResourceDeleted {
			out = append(out, record)
		}
	}
	return out, nil
}

func (s *sharedMemoryStorageStore) UpsertBucket(_ context.Context, record ports.StorageBucketRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buckets[s.key(record.TenantID, record.BucketID)] = record
	if record.CreateIdempotencyKey != "" {
		s.bucketIdem[record.TenantID+"/"+record.CreateIdempotencyKey] = record.BucketID
	}
	return nil
}
func (s *sharedMemoryStorageStore) GetBucket(_ context.Context, tenantID string, bucketID string) (ports.StorageBucketRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.buckets[s.key(tenantID, bucketID)]
	if !ok || record.State == ports.StorageResourceDeleted || !record.DeletedAt.IsZero() {
		return ports.StorageBucketRecord{}, ports.ErrNotFound
	}
	return record, nil
}
func (s *sharedMemoryStorageStore) ListBuckets(_ context.Context, tenantID string) ([]ports.StorageBucketRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.StorageBucketRecord, 0)
	for _, record := range s.buckets {
		if record.TenantID == tenantID && record.State != ports.StorageResourceDeleted && record.DeletedAt.IsZero() {
			out = append(out, record)
		}
	}
	return out, nil
}
func (s *sharedMemoryStorageStore) FindBucketByCreateIdempotency(_ context.Context, tenantID string, idempotencyKey string) (ports.StorageBucketRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.bucketIdem[tenantID+"/"+idempotencyKey]
	if !ok {
		return ports.StorageBucketRecord{}, ports.ErrNotFound
	}
	return s.buckets[s.key(tenantID, id)], nil
}
func (s *sharedMemoryStorageStore) ReplaceBucketLifecycleRules(context.Context, string, string, []ports.StorageBucketLifecycleRule) error {
	return nil
}

func (s *sharedMemoryStorageStore) UpsertVolumeSnapshot(_ context.Context, record ports.VolumeSnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[s.key(record.TenantID, record.SnapshotID)] = record
	if record.CreateIdempotencyKey != "" {
		s.snapIdem[record.TenantID+"/"+record.CreateIdempotencyKey] = record.SnapshotID
	}
	return nil
}
func (s *sharedMemoryStorageStore) ListVolumeSnapshots(_ context.Context, tenantID string, volumeID string) ([]ports.VolumeSnapshotRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.VolumeSnapshotRecord, 0)
	for _, record := range s.snapshots {
		if record.TenantID == tenantID && record.VolumeID == volumeID && record.DeletedAt.IsZero() {
			out = append(out, record)
		}
	}
	return out, nil
}
func (s *sharedMemoryStorageStore) FindVolumeSnapshotByCreateIdempotency(_ context.Context, tenantID string, idempotencyKey string) (ports.VolumeSnapshotRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.snapIdem[tenantID+"/"+idempotencyKey]
	if !ok {
		return ports.VolumeSnapshotRecord{}, ports.ErrNotFound
	}
	return s.snapshots[s.key(tenantID, id)], nil
}

func (s *sharedMemoryStorageStore) UpsertFilesystemMountTarget(_ context.Context, record ports.FilesystemMountTargetRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mounts[s.key(record.TenantID, record.MountTargetID)] = record
	if record.CreateIdempotencyKey != "" {
		s.mountIdem[record.TenantID+"/"+record.CreateIdempotencyKey] = record.MountTargetID
	}
	return nil
}
func (s *sharedMemoryStorageStore) ListFilesystemMountTargets(_ context.Context, tenantID string, filesystemID string) ([]ports.FilesystemMountTargetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.FilesystemMountTargetRecord, 0)
	for _, record := range s.mounts {
		if record.TenantID == tenantID && record.FilesystemID == filesystemID && record.DeletedAt.IsZero() {
			out = append(out, record)
		}
	}
	return out, nil
}
func (s *sharedMemoryStorageStore) FindFilesystemMountTargetByCreateIdempotency(_ context.Context, tenantID string, idempotencyKey string) (ports.FilesystemMountTargetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.mountIdem[tenantID+"/"+idempotencyKey]
	if !ok {
		return ports.FilesystemMountTargetRecord{}, ports.ErrNotFound
	}
	return s.mounts[s.key(tenantID, id)], nil
}

func (s *sharedMemoryStorageStore) UpdateResourceState(context.Context, ports.StorageResourceStateUpdateRequest) error {
	return nil
}

func TestLocalStorageServiceSharedStoreIsReadAuthority(t *testing.T) {
	store := newSharedMemoryStorageStore()
	clock := func() time.Time { return time.Unix(200, 0).UTC() }
	writer := NewLocalStorageService(WithStorageResourceStore(store), WithStorageServiceClock(clock))
	reader := NewLocalStorageService(WithStorageResourceStore(store), WithStorageServiceClock(clock))

	created, err := writer.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "shared-store-volume",
		Name:           "shared-volume",
		SizeGiB:        20,
		Zone:           "az-a",
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}

	got, err := reader.GetVolume(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: created.VolumeID,
	})
	if err != nil {
		t.Fatalf("reader GetVolume() error = %v", err)
	}
	if got.VolumeID != created.VolumeID || got.Name != "shared-volume" || got.Zone != "az-a" {
		t.Fatalf("reader GetVolume() = %#v, want created record from shared store", got)
	}

	replay, err := reader.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "shared-store-volume",
		Name:           "shared-volume",
		SizeGiB:        20,
		Zone:           "az-a",
	})
	if err != nil {
		t.Fatalf("idempotent CreateVolume() error = %v", err)
	}
	if replay.VolumeID != created.VolumeID {
		t.Fatalf("idempotent replay VolumeID = %s, want %s", replay.VolumeID, created.VolumeID)
	}

	bucket, err := writer.CreateStorageBucket(context.Background(), ports.StorageBucketCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "shared-store-bucket",
		Name:           "shared-bucket",
		AccessMode:     "private",
	})
	if err != nil {
		t.Fatalf("CreateStorageBucket() error = %v", err)
	}
	gotBucket, err := reader.GetStorageBucket(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: bucket.BucketID,
	})
	if err != nil {
		t.Fatalf("reader GetStorageBucket() error = %v", err)
	}
	if gotBucket.BucketID != bucket.BucketID || gotBucket.Name != "shared-bucket" {
		t.Fatalf("reader GetStorageBucket() = %#v", gotBucket)
	}

	otherTenant := NewLocalStorageService(WithStorageResourceStore(store), WithStorageServiceClock(clock))
	if _, err := otherTenant.GetVolume(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   "5dbb1d01-0000-4000-8000-000000000099",
		ResourceID: created.VolumeID,
	}); err != ports.ErrNotFound {
		t.Fatalf("cross-tenant GetVolume() error = %v, want ErrNotFound", err)
	}

	if _, err := writer.DeleteVolume(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: created.VolumeID,
	}); err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
	if _, err := reader.GetVolume(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: created.VolumeID,
	}); err != ports.ErrNotFound {
		t.Fatalf("deleted GetVolume() error = %v, want ErrNotFound", err)
	}
}

type sharedMemoryVectorStore struct {
	mu       sync.Mutex
	stores   map[string]ports.VectorStoreRecord
	byCreate map[string]string
	links    map[string]ports.VectorStoreKnowledgeBaseRef
}

func newSharedMemoryVectorStore() *sharedMemoryVectorStore {
	return &sharedMemoryVectorStore{
		stores:   map[string]ports.VectorStoreRecord{},
		byCreate: map[string]string{},
		links:    map[string]ports.VectorStoreKnowledgeBaseRef{},
	}
}

func (s *sharedMemoryVectorStore) key(tenantID, storeID string) string {
	return tenantID + "/" + storeID
}

func (s *sharedMemoryVectorStore) Upsert(_ context.Context, record ports.VectorStoreRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stores[s.key(record.TenantID, record.StoreID)] = record
	if record.CreateIdempotencyKey != "" {
		s.byCreate[record.TenantID+"/"+record.CreateIdempotencyKey] = record.StoreID
	}
	if record.KnowledgeBaseRef.Name != "" || record.KnowledgeBaseRef.ID != "" {
		s.links[s.key(record.TenantID, record.StoreID)] = record.KnowledgeBaseRef
	}
	return nil
}

func (s *sharedMemoryVectorStore) Get(_ context.Context, tenantID string, storeID string) (ports.VectorStoreRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.stores[s.key(tenantID, storeID)]
	if !ok || record.State == ports.VectorStoreDeleted || !record.DeletedAt.IsZero() {
		return ports.VectorStoreRecord{}, ports.ErrNotFound
	}
	record.KnowledgeBaseRef = s.links[s.key(tenantID, storeID)]
	return record, nil
}

func (s *sharedMemoryVectorStore) List(_ context.Context, tenantID string) ([]ports.VectorStoreRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.VectorStoreRecord, 0)
	for _, record := range s.stores {
		if record.TenantID == tenantID && record.State != ports.VectorStoreDeleted && record.DeletedAt.IsZero() {
			record.KnowledgeBaseRef = s.links[s.key(record.TenantID, record.StoreID)]
			out = append(out, record)
		}
	}
	return out, nil
}

func (s *sharedMemoryVectorStore) FindByCreateIdempotency(_ context.Context, tenantID string, idempotencyKey string) (ports.VectorStoreRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byCreate[tenantID+"/"+idempotencyKey]
	if !ok {
		return ports.VectorStoreRecord{}, ports.ErrNotFound
	}
	record := s.stores[s.key(tenantID, id)]
	record.KnowledgeBaseRef = s.links[s.key(tenantID, id)]
	return record, nil
}

func (s *sharedMemoryVectorStore) SetKnowledgeBaseLink(_ context.Context, tenantID string, storeID string, ref ports.VectorStoreKnowledgeBaseRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links[s.key(tenantID, storeID)] = ref
	return nil
}

func (s *sharedMemoryVectorStore) ClearKnowledgeBaseLink(_ context.Context, tenantID string, storeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.links, s.key(tenantID, storeID))
	return nil
}

func TestLocalVectorStoreServiceSharedStoreIsReadAuthority(t *testing.T) {
	store := newSharedMemoryVectorStore()
	clock := func() time.Time { return time.Unix(300, 0).UTC() }
	writer := NewLocalVectorStoreService(WithVectorStoreResourceStore(store), WithVectorStoreServiceClock(clock))
	reader := NewLocalVectorStoreService(WithVectorStoreResourceStore(store), WithVectorStoreServiceClock(clock))

	created, err := writer.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "shared-vector",
		Name:           "shared-vectors",
		Dimension:      8,
		Metric:         "cosine",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}
	got, err := reader.GetVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: created.StoreID,
	})
	if err != nil {
		t.Fatalf("reader GetVectorStore() error = %v", err)
	}
	if got.StoreID != created.StoreID || got.Dimension != 8 {
		t.Fatalf("reader GetVectorStore() = %#v", got)
	}

	linked, err := writer.SetVectorStoreKnowledgeBaseLink(context.Background(), ports.VectorStoreKnowledgeBaseLinkRequest{
		TenantID:       storageStoreTenantID,
		ResourceID:     created.StoreID,
		IdempotencyKey: "link-1",
		KnowledgeBaseRef: ports.VectorStoreKnowledgeBaseRef{
			ID:     "kb-1",
			Name:   "kb-one",
			Source: "external",
		},
	})
	if err != nil {
		t.Fatalf("SetVectorStoreKnowledgeBaseLink() error = %v", err)
	}
	if linked.KnowledgeBaseRef.Name != "kb-one" {
		t.Fatalf("linked ref = %#v", linked.KnowledgeBaseRef)
	}
	gotLinked, err := reader.GetVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: created.StoreID,
	})
	if err != nil || gotLinked.KnowledgeBaseRef.ID != "kb-1" {
		t.Fatalf("reader linked Get = %#v err=%v", gotLinked, err)
	}

	if _, err := writer.DeleteVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: created.StoreID,
	}); err != nil {
		t.Fatalf("DeleteVectorStore() error = %v", err)
	}
	if _, err := reader.GetVectorStore(context.Background(), ports.VectorStoreResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: created.StoreID,
	}); err != ports.ErrNotFound {
		t.Fatalf("deleted GetVectorStore() error = %v, want ErrNotFound", err)
	}
}

func TestLocalStorageServiceCompleteObjectPersistsToSharedStore(t *testing.T) {
	store := newSharedMemoryStorageStore()
	clock := func() time.Time { return time.Unix(300, 0).UTC() }
	service := NewLocalStorageService(WithStorageResourceStore(store), WithStorageServiceClock(clock))

	bucket, err := service.CreateStorageBucket(context.Background(), ports.StorageBucketCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "complete-store-bucket",
		Name:           "complete-store",
		Region:         "local",
		AccessMode:     "private",
	})
	if err != nil {
		t.Fatalf("CreateStorageBucket() error = %v", err)
	}

	upload, err := service.CreateStorageObjectUpload(context.Background(), ports.StorageObjectUploadRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "complete-store-upload",
		BucketID:       bucket.BucketID,
		Key:            "raw/report.csv",
		ContentType:    "text/csv",
	})
	if err != nil {
		t.Fatalf("CreateStorageObjectUpload() error = %v", err)
	}

	record, err := service.CompleteStorageObject(context.Background(), ports.StorageObjectCompleteRequest{
		TenantID:       storageStoreTenantID,
		ObjectID:       upload.ObjectID,
		IdempotencyKey: "complete-store-confirm",
	})
	if err != nil {
		t.Fatalf("CompleteStorageObject() error = %v", err)
	}
	if record.State != ports.StorageResourceAvailable {
		t.Fatalf("completed record state = %q, want available", record.State)
	}

	// A restarted service instance sharing the same store must read back the
	// completed object (complete persists to the control-plane authority).
	restarted := NewLocalStorageService(WithStorageResourceStore(store), WithStorageServiceClock(clock))
	loaded, err := restarted.GetObject(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: upload.ObjectID,
	})
	if err != nil {
		t.Fatalf("restarted GetObject() error = %v", err)
	}
	if loaded.State != ports.StorageResourceAvailable || loaded.Key != "raw/report.csv" || loaded.Bucket != "complete-store" {
		t.Fatalf("restarted GetObject() = %#v, want completed object persisted to store", loaded)
	}

	// A presigned upload must also survive a restart before completion:
	// the upload record is persisted at creation, so the restarted instance
	// can resolve it from the store and complete it.
	lateUpload, err := service.CreateStorageObjectUpload(context.Background(), ports.StorageObjectUploadRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "complete-store-late-upload",
		BucketID:       bucket.BucketID,
		Key:            "raw/late.csv",
		ContentType:    "text/csv",
	})
	if err != nil {
		t.Fatalf("CreateStorageObjectUpload() late error = %v", err)
	}
	lateRecord, err := restarted.CompleteStorageObject(context.Background(), ports.StorageObjectCompleteRequest{
		TenantID:       storageStoreTenantID,
		ObjectID:       lateUpload.ObjectID,
		IdempotencyKey: "complete-store-late-confirm",
	})
	if err != nil {
		t.Fatalf("restarted CompleteStorageObject() error = %v", err)
	}
	if lateRecord.State != ports.StorageResourceAvailable || lateRecord.Key != "raw/late.csv" {
		t.Fatalf("restarted CompleteStorageObject() = %#v, want completed late upload", lateRecord)
	}
}
