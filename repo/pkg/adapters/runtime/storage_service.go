package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalStorageService struct {
	mu                sync.RWMutex
	now               func() time.Time
	store             ports.StorageResourceStore
	objectStore       ports.ObjectStore
	providerRenderer  ports.StorageProviderRenderer
	providerDryRun    ports.StorageProviderDryRun
	providerApply     ports.StorageProviderApply
	providerStatus    ports.StorageProviderStatusReader
	providerExecution StorageProviderExecutionConfig
	volumes           map[string]ports.StorageVolumeRecord
	filesystems       map[string]ports.StorageFilesystemRecord
	objects           map[string]ports.StorageObjectRecord
	buckets           map[string]ports.StorageBucketRecord
	snapshots         map[string]ports.VolumeSnapshotRecord
	mountTargets      map[string]ports.FilesystemMountTargetRecord
	volumeIdempotency map[string]string
	fsIdempotency     map[string]string
	objectIdempotency map[string]string
	bucketIdem        map[string]string
	uploadIdem        map[string]string
	snapshotIdem      map[string]string
}

type StorageServiceOption func(*LocalStorageService)

type StorageProviderExecutionConfig struct {
	UserID          string
	PermissionProof string
}

func WithStorageServiceClock(now func() time.Time) StorageServiceOption {
	return func(service *LocalStorageService) {
		if now != nil {
			service.now = now
		}
	}
}

func WithStorageResourceStore(store ports.StorageResourceStore) StorageServiceOption {
	return func(service *LocalStorageService) {
		service.store = store
	}
}

func WithStorageObjectStore(store ports.ObjectStore) StorageServiceOption {
	return func(service *LocalStorageService) {
		service.objectStore = store
	}
}

func WithStorageProvider(
	renderer ports.StorageProviderRenderer,
	dryRun ports.StorageProviderDryRun,
	apply ports.StorageProviderApply,
	status ports.StorageProviderStatusReader,
	execution StorageProviderExecutionConfig,
) StorageServiceOption {
	return func(service *LocalStorageService) {
		service.providerRenderer = renderer
		service.providerDryRun = dryRun
		service.providerApply = apply
		service.providerStatus = status
		service.providerExecution = execution
	}
}

func NewLocalStorageService(options ...StorageServiceOption) *LocalStorageService {
	service := &LocalStorageService{
		now:               func() time.Time { return time.Now().UTC() },
		volumes:           map[string]ports.StorageVolumeRecord{},
		filesystems:       map[string]ports.StorageFilesystemRecord{},
		objects:           map[string]ports.StorageObjectRecord{},
		buckets:           map[string]ports.StorageBucketRecord{},
		snapshots:         map[string]ports.VolumeSnapshotRecord{},
		mountTargets:      map[string]ports.FilesystemMountTargetRecord{},
		volumeIdempotency: map[string]string{},
		fsIdempotency:     map[string]string{},
		objectIdempotency: map[string]string{},
		bucketIdem:        map[string]string{},
		uploadIdem:        map[string]string{},
		snapshotIdem:      map[string]string{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *LocalStorageService) CreateVolume(ctx context.Context, request ports.StorageVolumeCreateRequest) (ports.StorageVolumeRecord, error) {
	if err := requireStorageTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	if request.SizeGiB <= 0 {
		return ports.StorageVolumeRecord{}, fmt.Errorf("%w: volume size_gib must be greater than zero", ports.ErrInvalid)
	}
	s.mu.Lock()
	if id, ok := s.volumeIdempotency[idemKey]; ok {
		if record, exists := s.volumes[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	now := s.now().UTC()
	record := ports.StorageVolumeRecord{
		TenantID:     request.TenantID,
		VolumeID:     "vol_" + uuid.NewString(),
		Name:         strings.TrimSpace(request.Name),
		SizeGiB:      request.SizeGiB,
		StorageClass: firstNetworkNonEmpty(request.StorageClass, "standard"),
		State:        ports.StorageResourceAvailable,
		Reason:       "created by local storage profile",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.mu.Unlock()
	if s.storageProviderConfigured() {
		observation, err := s.executeStorageProvider(ctx, "volume", record.VolumeID, func() ([]ports.WorkloadManifest, error) {
			return s.providerRenderer.RenderVolume(ctx, record)
		})
		if err != nil {
			return ports.StorageVolumeRecord{}, err
		}
		record.State = observation.State
		record.Reason = observation.Reason
		record.UpdatedAt = observation.ObservedAt
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.volumes[record.VolumeID] = record
	s.volumeIdempotency[idemKey] = record.VolumeID
	if err := s.upsertVolume(ctx, record); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) ListVolumes(ctx context.Context, request ports.StorageResourceListRequest) ([]ports.StorageVolumeRecord, error) {
	if s.store != nil {
		return s.store.ListVolumes(ctx, request.TenantID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.StorageVolumeRecord, 0, len(s.volumes))
	for _, record := range s.volumes {
		if record.TenantID == request.TenantID && record.State != ports.StorageResourceDeleted {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalStorageService) GetVolume(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageVolumeRecord, error) {
	if s.store != nil {
		return s.store.GetVolume(ctx, request.TenantID, request.ResourceID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.volumes[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalStorageService) DeleteVolume(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageVolumeRecord, error) {
	if s.store != nil {
		record, err := s.store.GetVolume(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.StorageVolumeRecord{}, err
		}
		record.State = ports.StorageResourceDeleted
		record.Reason = "deleted by local storage profile"
		record.UpdatedAt = s.now().UTC()
		if err := s.upsertVolume(ctx, record); err != nil {
			return ports.StorageVolumeRecord{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.volumes[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	record.State = ports.StorageResourceDeleted
	record.Reason = "deleted by local storage profile"
	record.UpdatedAt = s.now().UTC()
	s.volumes[record.VolumeID] = record
	if err := s.upsertVolume(ctx, record); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) CreateFilesystem(ctx context.Context, request ports.StorageFilesystemCreateRequest) (ports.StorageFilesystemRecord, error) {
	if err := requireStorageTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	if request.SizeGiB <= 0 {
		return ports.StorageFilesystemRecord{}, fmt.Errorf("%w: filesystem size_gib must be greater than zero", ports.ErrInvalid)
	}
	protocol := strings.ToLower(strings.TrimSpace(request.Protocol))
	if protocol == "" {
		protocol = "nfs"
	}
	if protocol != "nfs" && protocol != "cephfs" {
		return ports.StorageFilesystemRecord{}, fmt.Errorf("%w: unsupported filesystem protocol %q", ports.ErrUnsupported, request.Protocol)
	}
	s.mu.Lock()
	if id, ok := s.fsIdempotency[idemKey]; ok {
		if record, exists := s.filesystems[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	now := s.now().UTC()
	record := ports.StorageFilesystemRecord{
		TenantID:     request.TenantID,
		FilesystemID: "fs_" + uuid.NewString(),
		Name:         strings.TrimSpace(request.Name),
		Protocol:     protocol,
		SizeGiB:      request.SizeGiB,
		Endpoint:     "local://" + strings.TrimSpace(request.Name),
		State:        ports.StorageResourceAvailable,
		Reason:       "created by local storage profile",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.mu.Unlock()
	if s.storageProviderConfigured() {
		observation, err := s.executeStorageProvider(ctx, "filesystem", record.FilesystemID, func() ([]ports.WorkloadManifest, error) {
			return s.providerRenderer.RenderFilesystem(ctx, record)
		})
		if err != nil {
			return ports.StorageFilesystemRecord{}, err
		}
		record.State = observation.State
		record.Reason = observation.Reason
		record.UpdatedAt = observation.ObservedAt
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filesystems[record.FilesystemID] = record
	s.fsIdempotency[idemKey] = record.FilesystemID
	if err := s.upsertFilesystem(ctx, record); err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) ListFilesystems(ctx context.Context, request ports.StorageResourceListRequest) ([]ports.StorageFilesystemRecord, error) {
	if s.store != nil {
		return s.store.ListFilesystems(ctx, request.TenantID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.StorageFilesystemRecord, 0, len(s.filesystems))
	for _, record := range s.filesystems {
		if record.TenantID == request.TenantID && record.State != ports.StorageResourceDeleted {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalStorageService) GetFilesystem(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageFilesystemRecord, error) {
	if s.store != nil {
		return s.store.GetFilesystem(ctx, request.TenantID, request.ResourceID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.filesystems[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageFilesystemRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalStorageService) DeleteFilesystem(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageFilesystemRecord, error) {
	if s.store != nil {
		record, err := s.store.GetFilesystem(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.StorageFilesystemRecord{}, err
		}
		record.State = ports.StorageResourceDeleted
		record.Reason = "deleted by local storage profile"
		record.UpdatedAt = s.now().UTC()
		if err := s.upsertFilesystem(ctx, record); err != nil {
			return ports.StorageFilesystemRecord{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.filesystems[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageFilesystemRecord{}, ports.ErrNotFound
	}
	record.State = ports.StorageResourceDeleted
	record.Reason = "deleted by local storage profile"
	record.UpdatedAt = s.now().UTC()
	s.filesystems[record.FilesystemID] = record
	if err := s.upsertFilesystem(ctx, record); err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) CreateObject(ctx context.Context, request ports.StorageObjectCreateRequest) (ports.StorageObjectRecord, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: tenant_id/idempotency_key required", ports.ErrInvalid)
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageObjectRecord{}, err
	}
	if strings.TrimSpace(request.Bucket) == "" || strings.TrimSpace(request.Key) == "" {
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: bucket and key are required", ports.ErrInvalid)
	}
	if request.SizeBytes < 0 {
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: object size_bytes must not be negative", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.objectIdempotency[idemKey]; ok {
		if record, exists := s.objects[id]; exists {
			return record, nil
		}
	}
	now := s.now().UTC()
	record := ports.StorageObjectRecord{
		TenantID:    request.TenantID,
		ObjectID:    "obj_" + uuid.NewString(),
		Bucket:      strings.TrimSpace(request.Bucket),
		Key:         strings.TrimSpace(request.Key),
		SizeBytes:   request.SizeBytes,
		ContentType: firstNetworkNonEmpty(request.ContentType, "application/octet-stream"),
		State:       ports.StorageResourceAvailable,
		Reason:      "created by local storage profile",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.objects[record.ObjectID] = record
	s.objectIdempotency[idemKey] = record.ObjectID
	if err := s.upsertObject(ctx, record); err != nil {
		return ports.StorageObjectRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) ListObjects(ctx context.Context, request ports.StorageResourceListRequest) ([]ports.StorageObjectRecord, error) {
	if s.store != nil {
		return s.store.ListObjects(ctx, request.TenantID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.StorageObjectRecord, 0, len(s.objects))
	for _, record := range s.objects {
		if record.TenantID == request.TenantID && record.State != ports.StorageResourceDeleted {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalStorageService) GetObject(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageObjectRecord, error) {
	if s.store != nil {
		return s.store.GetObject(ctx, request.TenantID, request.ResourceID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.objects[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageObjectRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalStorageService) DeleteObject(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageObjectRecord, error) {
	if s.store != nil {
		record, err := s.store.GetObject(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.StorageObjectRecord{}, err
		}
		objectStore := s.objectStore
		if objectStore != nil {
			if err := objectStore.DeleteObject(ctx, storageObjectRef(record)); err != nil && err != ports.ErrNotFound {
				return ports.StorageObjectRecord{}, err
			}
		}
		record.State = ports.StorageResourceDeleted
		record.Reason = "deleted by local storage profile"
		record.UpdatedAt = s.now().UTC()
		if err := s.upsertObject(ctx, record); err != nil {
			return ports.StorageObjectRecord{}, err
		}
		return record, nil
	}
	s.mu.RLock()
	record, ok := s.objects[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		s.mu.RUnlock()
		return ports.StorageObjectRecord{}, ports.ErrNotFound
	}
	objectStore := s.objectStore
	s.mu.RUnlock()

	if objectStore != nil {
		if err := objectStore.DeleteObject(ctx, storageObjectRef(record)); err != nil && err != ports.ErrNotFound {
			return ports.StorageObjectRecord{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.objects[request.ResourceID]
	if !ok || current.TenantID != request.TenantID || current.State == ports.StorageResourceDeleted {
		return ports.StorageObjectRecord{}, ports.ErrNotFound
	}
	record = current
	record.State = ports.StorageResourceDeleted
	record.Reason = "deleted by local storage profile"
	record.UpdatedAt = s.now().UTC()
	s.objects[record.ObjectID] = record
	if err := s.upsertObject(ctx, record); err != nil {
		return ports.StorageObjectRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) CreateStorageBucket(ctx context.Context, request ports.StorageBucketCreateRequest) (ports.StorageBucketRecord, error) {
	if err := requireStorageTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.StorageBucketRecord{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageBucketRecord{}, err
	}
	accessMode := firstNetworkNonEmpty(strings.ToLower(strings.TrimSpace(request.AccessMode)), "private")
	if accessMode != "private" && accessMode != "public_read" {
		return ports.StorageBucketRecord{}, fmt.Errorf("%w: unsupported bucket access_mode %q", ports.ErrUnsupported, request.AccessMode)
	}

	if s.store != nil {
		if record, err := s.store.GetBucketByIdempotency(ctx, request.TenantID, idemKey); err == nil {
			return record, nil
		}
		if _, err := s.store.GetBucketByName(ctx, request.TenantID, strings.TrimSpace(request.Name)); err == nil {
			return ports.StorageBucketRecord{}, fmt.Errorf("%w: bucket name already exists", ports.ErrConflict)
		}
	} else {
		s.mu.Lock()
		if id, ok := s.bucketIdem[idemKey]; ok {
			if record, exists := s.buckets[id]; exists {
				s.mu.Unlock()
				return record, nil
			}
		}
		for _, record := range s.buckets {
			if record.TenantID == request.TenantID && record.Name == strings.TrimSpace(request.Name) {
				s.mu.Unlock()
				return ports.StorageBucketRecord{}, fmt.Errorf("%w: bucket name already exists", ports.ErrConflict)
			}
		}
		s.mu.Unlock()
	}

	bucketClass := ports.BucketClass(strings.TrimSpace(request.Name))
	if s.objectStore != nil {
		if err := s.objectStore.EnsureBucket(ctx, bucketClass); err != nil {
			return ports.StorageBucketRecord{}, err
		}
	}

	now := s.now().UTC()
	record := ports.StorageBucketRecord{
		TenantID:   request.TenantID,
		BucketID:   uuid.NewString(),
		Name:       strings.TrimSpace(request.Name),
		Region:     strings.TrimSpace(request.Region),
		AccessMode: accessMode,
		CreatedAt:  now,
	}
	if s.store != nil {
		if err := s.store.UpsertBucket(ctx, record, idemKey); err != nil {
			return ports.StorageBucketRecord{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buckets[record.BucketID] = record
	s.bucketIdem[idemKey] = record.BucketID
	return record, nil
}

func (s *LocalStorageService) ListStorageBuckets(ctx context.Context, request ports.StorageResourceListRequest) ([]ports.StorageBucketRecord, error) {
	if s.store != nil {
		return s.store.ListBuckets(ctx, request.TenantID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.StorageBucketRecord, 0, len(s.buckets))
	for _, bucket := range s.buckets {
		if bucket.TenantID != request.TenantID {
			continue
		}
		bucket.ObjectCount = 0
		bucket.SizeBytes = 0
		for _, object := range s.objects {
			if object.TenantID == bucket.TenantID && object.Bucket == bucket.Name && object.State != ports.StorageResourceDeleted {
				bucket.ObjectCount++
				bucket.SizeBytes += object.SizeBytes
			}
		}
		items = append(items, bucket)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *LocalStorageService) CreateStorageObjectUpload(ctx context.Context, request ports.StorageObjectUploadRequest) (ports.StorageObjectUploadRecord, error) {
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.StorageObjectUploadRecord{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageObjectUploadRecord{}, err
	}
	if strings.TrimSpace(request.BucketID) == "" || strings.TrimSpace(request.Key) == "" {
		return ports.StorageObjectUploadRecord{}, fmt.Errorf("%w: bucket_id and key are required", ports.ErrInvalid)
	}

	s.mu.RLock()
	if id, ok := s.uploadIdem[idemKey]; ok {
		if object, exists := s.objects[id]; exists && object.TenantID == request.TenantID && object.State != ports.StorageResourceDeleted {
			s.mu.RUnlock()
			return s.signedUploadForObject(ctx, object, request.ExpiresSeconds)
		}
	}
	s.mu.RUnlock()

	bucket, err := s.lookupBucket(ctx, request.TenantID, strings.TrimSpace(request.BucketID))
	if err != nil {
		return ports.StorageObjectUploadRecord{}, err
	}

	now := s.now().UTC()
	object := ports.StorageObjectRecord{
		TenantID:    request.TenantID,
		ObjectID:    uuid.NewString(),
		Bucket:      bucket.Name,
		Key:         strings.TrimSpace(request.Key),
		ContentType: firstNetworkNonEmpty(request.ContentType, "application/octet-stream"),
		State:       ports.StorageResourcePending,
		Reason:      "awaiting client upload to pre-signed URL",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	result, err := s.signedUploadForObject(ctx, object, request.ExpiresSeconds)
	if err != nil {
		return ports.StorageObjectUploadRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[object.ObjectID] = object
	s.uploadIdem[idemKey] = object.ObjectID
	if err := s.upsertObject(ctx, object); err != nil {
		return ports.StorageObjectUploadRecord{}, err
	}
	return result, nil
}

func (s *LocalStorageService) CompleteStorageObjectUpload(ctx context.Context, request ports.StorageObjectCompleteRequest) (ports.StorageObjectRecord, error) {
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.ObjectID) == "" {
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: object id is required", ports.ErrInvalid)
	}
	record, err := s.GetObject(ctx, ports.StorageResourceGetRequest{
		TenantID:   request.TenantID,
		ResourceID: strings.TrimSpace(request.ObjectID),
	})
	if err != nil {
		return ports.StorageObjectRecord{}, err
	}
	if record.State == ports.StorageResourceDeleted {
		return ports.StorageObjectRecord{}, ports.ErrNotFound
	}
	if s.objectStore == nil {
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: object store is required to complete upload", ports.ErrNotConfigured)
	}
	meta, err := s.objectStore.StatObject(ctx, storageObjectRef(record))
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return ports.StorageObjectRecord{}, fmt.Errorf("%w: object bytes not found in object store", ports.ErrNotFound)
		}
		return ports.StorageObjectRecord{}, err
	}
	record.SizeBytes = meta.SizeBytes
	if contentType := strings.TrimSpace(meta.ContentType); contentType != "" {
		record.ContentType = contentType
	}
	record.State = ports.StorageResourceAvailable
	record.Reason = "upload completed; object store metadata reconciled"
	record.UpdatedAt = s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[record.ObjectID] = record
	if err := s.upsertObject(ctx, record); err != nil {
		return ports.StorageObjectRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) GetStorageObjectDownload(ctx context.Context, request ports.StorageObjectDownloadRequest) (ports.StorageObjectDownloadRecord, error) {
	object, err := s.GetObject(ctx, ports.StorageResourceGetRequest{
		TenantID:   request.TenantID,
		ResourceID: strings.TrimSpace(request.ObjectID),
	})
	if err != nil {
		return ports.StorageObjectDownloadRecord{}, err
	}
	ttl := storageSignedURLTTL(request.ExpiresSeconds)
	ref := storageObjectRef(object)
	signed, err := s.signedDownloadURL(ctx, ref, ttl)
	if err != nil {
		return ports.StorageObjectDownloadRecord{}, err
	}
	return ports.StorageObjectDownloadRecord{
		DownloadURL: signed.URL,
		ExpiresAt:   signed.ExpiresAt,
		ContentType: object.ContentType,
		SizeBytes:   object.SizeBytes,
	}, nil
}

func (s *LocalStorageService) CreateVolumeSnapshot(ctx context.Context, request ports.VolumeSnapshotCreateRequest) (ports.VolumeSnapshotRecord, error) {
	if err := requireStorageTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.VolumeSnapshotRecord{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.VolumeSnapshotRecord{}, err
	}
	if strings.TrimSpace(request.VolumeID) == "" {
		return ports.VolumeSnapshotRecord{}, fmt.Errorf("%w: volume_id is required", ports.ErrInvalid)
	}
	if s.store != nil {
		if record, err := s.store.GetVolumeSnapshotByIdempotency(ctx, request.TenantID, idemKey); err == nil {
			return record, nil
		}
	} else {
		s.mu.Lock()
		if id, ok := s.snapshotIdem[idemKey]; ok {
			if record, exists := s.snapshots[id]; exists {
				s.mu.Unlock()
				return record, nil
			}
		}
		s.mu.Unlock()
	}
	volume, err := s.lookupVolume(ctx, request.TenantID, strings.TrimSpace(request.VolumeID))
	if err != nil {
		return ports.VolumeSnapshotRecord{}, err
	}
	record := ports.VolumeSnapshotRecord{
		TenantID:    request.TenantID,
		SnapshotID:  "snap_" + uuid.NewString(),
		VolumeID:    volume.VolumeID,
		Name:        strings.TrimSpace(request.Name),
		Description: strings.TrimSpace(request.Description),
		Status:      ports.VolumeSnapshotAvailable,
		SizeBytes:   volume.SizeGiB * 1024 * 1024 * 1024,
		CreatedAt:   s.now().UTC(),
	}
	if s.storageProviderConfigured() {
		observation, err := s.executeStorageProvider(ctx, "volume_snapshot", record.SnapshotID, func() ([]ports.WorkloadManifest, error) {
			return s.providerRenderer.RenderVolumeSnapshot(ctx, record)
		})
		if err != nil {
			return ports.VolumeSnapshotRecord{}, err
		}
		record.Status = volumeSnapshotStatusFromStorageState(observation.State)
		if !observation.ObservedAt.IsZero() {
			record.CreatedAt = observation.ObservedAt
		}
	}
	if s.store != nil {
		if err := s.store.UpsertVolumeSnapshot(ctx, record, idemKey); err != nil {
			return ports.VolumeSnapshotRecord{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[record.SnapshotID] = record
	s.snapshotIdem[idemKey] = record.SnapshotID
	return record, nil
}

func (s *LocalStorageService) ListVolumeSnapshots(ctx context.Context, request ports.VolumeSnapshotListRequest) ([]ports.VolumeSnapshotRecord, error) {
	if _, err := s.lookupVolume(ctx, request.TenantID, strings.TrimSpace(request.VolumeID)); err != nil {
		return nil, err
	}
	if s.store != nil {
		return s.store.ListVolumeSnapshots(ctx, request.TenantID, request.VolumeID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.VolumeSnapshotRecord, 0, len(s.snapshots))
	for _, record := range s.snapshots {
		if record.TenantID == request.TenantID && record.VolumeID == request.VolumeID {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *LocalStorageService) ListFilesystemMountTargets(ctx context.Context, request ports.FilesystemMountTargetListRequest) ([]ports.FilesystemMountTargetRecord, error) {
	filesystem, err := s.lookupFilesystem(ctx, request.TenantID, strings.TrimSpace(request.FilesystemID))
	if err != nil {
		return nil, err
	}
	if s.store != nil {
		if target, err := s.store.GetFilesystemMountTarget(ctx, filesystem.TenantID, filesystem.FilesystemID); err == nil {
			return []ports.FilesystemMountTargetRecord{target}, nil
		}
	} else {
		s.mu.Lock()
		if target, ok := s.mountTargets[filesystem.FilesystemID]; ok {
			s.mu.Unlock()
			return []ports.FilesystemMountTargetRecord{target}, nil
		}
		s.mu.Unlock()
	}
	target := ports.FilesystemMountTargetRecord{
		TenantID:      filesystem.TenantID,
		MountTargetID: "mt_" + uuid.NewString(),
		FilesystemID:  filesystem.FilesystemID,
		SubnetID:      "local-subnet",
		IPAddress:     "127.0.0.1",
		Status:        ports.MountTargetAvailable,
		CreatedAt:     s.now().UTC(),
	}
	if s.storageProviderConfigured() {
		observation, err := s.executeStorageProvider(ctx, "filesystem_mount_target", target.MountTargetID, func() ([]ports.WorkloadManifest, error) {
			return s.providerRenderer.RenderFilesystemMountTarget(ctx, target)
		})
		if err != nil {
			return nil, err
		}
		target.Status = mountTargetStatusFromStorageState(observation.State)
		if !observation.ObservedAt.IsZero() {
			target.CreatedAt = observation.ObservedAt
		}
	}
	if s.store != nil {
		if err := s.store.UpsertFilesystemMountTarget(ctx, target); err != nil {
			return nil, err
		}
		return []ports.FilesystemMountTargetRecord{target}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mountTargets[filesystem.FilesystemID] = target
	return []ports.FilesystemMountTargetRecord{target}, nil
}

func (s *LocalStorageService) upsertVolume(ctx context.Context, record ports.StorageVolumeRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertVolume(ctx, record)
}

func (s *LocalStorageService) upsertFilesystem(ctx context.Context, record ports.StorageFilesystemRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertFilesystem(ctx, record)
}

func (s *LocalStorageService) upsertObject(ctx context.Context, record ports.StorageObjectRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertObject(ctx, record)
}

func (s *LocalStorageService) storageProviderConfigured() bool {
	return s.providerRenderer != nil || s.providerDryRun != nil || s.providerApply != nil || s.providerStatus != nil
}

func (s *LocalStorageService) executeStorageProvider(ctx context.Context, resourceKind string, resourceID string, render func() ([]ports.WorkloadManifest, error)) (ports.StorageProviderStatusResult, error) {
	if s.providerRenderer == nil || s.providerDryRun == nil || s.providerApply == nil || s.providerStatus == nil {
		return ports.StorageProviderStatusResult{}, fmt.Errorf("%w: storage provider renderer, dry-run, apply and status reader are required", ports.ErrNotConfigured)
	}
	manifests, err := render()
	if err != nil {
		return ports.StorageProviderStatusResult{}, err
	}
	now := s.now().UTC()
	dryRun, err := s.providerDryRun.DryRun(ctx, ports.StorageProviderDryRunRequest{
		TenantID:        tenantIDFromStorageResource(resourceID, manifests),
		UserID:          s.providerExecution.UserID,
		ResourceKind:    resourceKind,
		ResourceID:      resourceID,
		Operation:       ports.StorageProviderOperationCreate,
		Manifests:       manifests,
		PermissionProof: s.providerExecution.PermissionProof,
		RequestedAt:     now,
	})
	if err != nil {
		return ports.StorageProviderStatusResult{}, err
	}
	apply, err := s.providerApply.Apply(ctx, ports.StorageProviderApplyRequest{
		TenantID:        tenantIDFromStorageResource(resourceID, manifests),
		UserID:          s.providerExecution.UserID,
		ResourceKind:    resourceKind,
		ResourceID:      resourceID,
		Operation:       ports.StorageProviderOperationCreate,
		Manifests:       manifests,
		PermissionProof: s.providerExecution.PermissionProof,
		DryRunResult:    dryRun,
		RequestedAt:     now,
	})
	if err != nil {
		return ports.StorageProviderStatusResult{}, err
	}
	return s.providerStatus.Observe(ctx, ports.StorageProviderStatusRequest{
		TenantID:        tenantIDFromStorageResource(resourceID, manifests),
		UserID:          s.providerExecution.UserID,
		ResourceKind:    resourceKind,
		ResourceID:      resourceID,
		ApplyResult:     apply,
		PermissionProof: s.providerExecution.PermissionProof,
		RequestedAt:     now,
	})
}

func tenantIDFromStorageResource(_ string, manifests []ports.WorkloadManifest) string {
	if len(manifests) == 0 {
		return ""
	}
	doc, err := parseManifestDocument(manifests[0].Content)
	if err != nil {
		return ""
	}
	metadata, _ := doc["metadata"].(map[string]any)
	labels, _ := metadata["labels"].(map[string]any)
	if tenantID, _ := labels["ani.kubercloud.io/tenant-id"].(string); tenantID != "" {
		return tenantID
	}
	return ""
}

func volumeSnapshotStatusFromStorageState(state ports.StorageResourceState) ports.VolumeSnapshotStatus {
	switch state {
	case ports.StorageResourceAvailable:
		return ports.VolumeSnapshotAvailable
	case ports.StorageResourceFailed:
		return ports.VolumeSnapshotError
	case ports.StorageResourceDeleting, ports.StorageResourceDeleted:
		return ports.VolumeSnapshotDeleting
	default:
		return ports.VolumeSnapshotCreating
	}
}

func mountTargetStatusFromStorageState(state ports.StorageResourceState) ports.MountTargetStatus {
	switch state {
	case ports.StorageResourceAvailable:
		return ports.MountTargetAvailable
	case ports.StorageResourceFailed:
		return ports.MountTargetError
	case ports.StorageResourceDeleting, ports.StorageResourceDeleted:
		return ports.MountTargetDeleting
	default:
		return ports.MountTargetCreating
	}
}

func (s *LocalStorageService) signedUploadForObject(ctx context.Context, object ports.StorageObjectRecord, expiresSeconds int) (ports.StorageObjectUploadRecord, error) {
	ttl := storageSignedURLTTL(expiresSeconds)
	signed, err := s.signedUploadURL(ctx, storageObjectRef(object), ttl)
	if err != nil {
		return ports.StorageObjectUploadRecord{}, err
	}
	return ports.StorageObjectUploadRecord{
		ObjectID:  object.ObjectID,
		UploadURL: signed.URL,
		ExpiresAt: signed.ExpiresAt,
	}, nil
}

func (s *LocalStorageService) signedUploadURL(ctx context.Context, ref ports.ObjectRef, ttl time.Duration) (ports.SignedURL, error) {
	if s.objectStore != nil {
		return s.objectStore.SignedUploadURL(ctx, ref, ttl)
	}
	return ports.SignedURL{
		URL:       localStorageSignedURL("upload", ref),
		ExpiresAt: s.now().UTC().Add(ttl),
	}, nil
}

func (s *LocalStorageService) signedDownloadURL(ctx context.Context, ref ports.ObjectRef, ttl time.Duration) (ports.SignedURL, error) {
	if s.objectStore != nil {
		return s.objectStore.SignedDownloadURL(ctx, ref, ttl)
	}
	return ports.SignedURL{
		URL:       localStorageSignedURL("download", ref),
		ExpiresAt: s.now().UTC().Add(ttl),
	}, nil
}

func storageObjectRef(object ports.StorageObjectRecord) ports.ObjectRef {
	return ports.ObjectRef{
		TenantID:    object.TenantID,
		BucketClass: ports.BucketClass(object.Bucket),
		ObjectKey:   object.Key,
		Version:     object.ObjectID,
	}
}

func storageSignedURLTTL(expiresSeconds int) time.Duration {
	if expiresSeconds <= 0 {
		expiresSeconds = 3600
	}
	if expiresSeconds < 60 {
		expiresSeconds = 60
	}
	if expiresSeconds > 86400 {
		expiresSeconds = 86400
	}
	return time.Duration(expiresSeconds) * time.Second
}

func localStorageSignedURL(action string, ref ports.ObjectRef) string {
	return "https://local-object-store.dev/" + action + "/" + url.PathEscape(string(ref.BucketClass)) + "/" + url.PathEscape(ref.ObjectKey)
}

func requireStorageTenantAndName(tenantID string, name string) error {
	if strings.TrimSpace(tenantID) == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", ports.ErrInvalid)
	}
	return nil
}

func (s *LocalStorageService) lookupVolume(ctx context.Context, tenantID, volumeID string) (ports.StorageVolumeRecord, error) {
	if s.store != nil {
		record, err := s.store.GetVolume(ctx, tenantID, volumeID)
		if err != nil {
			return ports.StorageVolumeRecord{}, fmt.Errorf("%w: volume not found", ports.ErrNotFound)
		}
		if record.State == ports.StorageResourceDeleted {
			return ports.StorageVolumeRecord{}, fmt.Errorf("%w: volume not found", ports.ErrNotFound)
		}
		return record, nil
	}
	s.mu.RLock()
	record, ok := s.volumes[volumeID]
	s.mu.RUnlock()
	if !ok || record.TenantID != tenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, fmt.Errorf("%w: volume not found", ports.ErrNotFound)
	}
	return record, nil
}

func (s *LocalStorageService) lookupFilesystem(ctx context.Context, tenantID, filesystemID string) (ports.StorageFilesystemRecord, error) {
	if s.store != nil {
		record, err := s.store.GetFilesystem(ctx, tenantID, filesystemID)
		if err != nil {
			return ports.StorageFilesystemRecord{}, ports.ErrNotFound
		}
		if record.State == ports.StorageResourceDeleted {
			return ports.StorageFilesystemRecord{}, ports.ErrNotFound
		}
		return record, nil
	}
	s.mu.RLock()
	record, ok := s.filesystems[filesystemID]
	s.mu.RUnlock()
	if !ok || record.TenantID != tenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageFilesystemRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *LocalStorageService) lookupBucket(ctx context.Context, tenantID, bucketID string) (ports.StorageBucketRecord, error) {
	if s.store != nil {
		record, err := s.store.GetBucket(ctx, tenantID, bucketID)
		if err != nil {
			return ports.StorageBucketRecord{}, fmt.Errorf("%w: bucket not found", ports.ErrNotFound)
		}
		return record, nil
	}
	s.mu.RLock()
	record, ok := s.buckets[bucketID]
	s.mu.RUnlock()
	if !ok || record.TenantID != tenantID {
		return ports.StorageBucketRecord{}, fmt.Errorf("%w: bucket not found", ports.ErrNotFound)
	}
	return record, nil
}

var _ ports.StorageService = (*LocalStorageService)(nil)
