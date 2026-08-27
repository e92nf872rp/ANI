package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalStorageService struct {
	mu                sync.RWMutex
	idempotencyMu     sync.Mutex
	idempotencyFlight map[string]chan struct{}
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
	// bucketPrefixes keys are "tenantID/bucketID" -> prefix key set
	bucketPrefixes    map[string]map[string]ports.StorageBucketObjectEntry
	snapshots         map[string]ports.VolumeSnapshotRecord
	mountTargets      map[string]ports.FilesystemMountTargetRecord
	volumeIdempotency map[string]string
	fsIdempotency     map[string]string
	volumeOpIdem      map[string]string
	fsOpIdem          map[string]string
	objectIdempotency map[string]string
	bucketIdem        map[string]string
	uploadIdem        map[string]string
	snapshotIdem      map[string]string
	prefixIdem        map[string]string
	bucketUpdateIdem  map[string]string
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
		bucketPrefixes:    map[string]map[string]ports.StorageBucketObjectEntry{},
		snapshots:         map[string]ports.VolumeSnapshotRecord{},
		mountTargets:      map[string]ports.FilesystemMountTargetRecord{},
		volumeIdempotency: map[string]string{},
		fsIdempotency:     map[string]string{},
		volumeOpIdem:      map[string]string{},
		fsOpIdem:          map[string]string{},
		objectIdempotency: map[string]string{},
		bucketIdem:        map[string]string{},
		uploadIdem:        map[string]string{},
		snapshotIdem:      map[string]string{},
		prefixIdem:        map[string]string{},
		bucketUpdateIdem:  map[string]string{},
		idempotencyFlight: map[string]chan struct{}{},
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
	release, err := s.acquireStorageIdempotency(ctx, "volume.create/"+idemKey)
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	defer release()

	if s.store != nil {
		if existing, err := s.store.FindVolumeByCreateIdempotency(ctx, request.TenantID, request.IdempotencyKey); err == nil {
			return s.enrichStorageVolumeRecord(existing), nil
		} else if !errors.Is(err, ports.ErrNotFound) {
			return ports.StorageVolumeRecord{}, err
		}
	}

	s.mu.Lock()
	if id, ok := s.volumeIdempotency[idemKey]; ok {
		if record, exists := s.volumes[id]; exists {
			s.mu.Unlock()
			return s.enrichStorageVolumeRecord(record), nil
		}
	}
	now := s.now().UTC()
	volumeType := firstNetworkNonEmpty(request.VolumeType, "ssd")
	providerConfigured := s.storageProviderConfigured()
	record := ports.StorageVolumeRecord{
		TenantID:        request.TenantID,
		VolumeID:        "vol_" + uuid.NewString(),
		Name:            strings.TrimSpace(request.Name),
		SizeGiB:         request.SizeGiB,
		StorageClass:    firstNetworkNonEmpty(request.StorageClass, "standard"),
		Zone:            strings.TrimSpace(request.Zone),
		VolumeType:      volumeType,
		IOPS:            storageVolumeIOPS(volumeType),
		Encrypted:       request.Encrypted,
		MountInstanceID: strings.TrimSpace(request.MountInstanceID),
		MountRoute:      strings.TrimSpace(request.MountRoute),
		AutoSnapshot: ports.StorageVolumeAutoSnapshotPolicy{
			Enabled:    false,
			RetainDays: 7,
			Schedule:   "daily@02:00",
		},
		OSInitStatus:             storageVolumeInitialOSStatus(request.MountInstanceID),
		OSInitDevice:             "/dev/disk/by-id/ani-" + strings.TrimSpace(request.Name),
		State:                    ports.StorageResourceAvailable,
		Reason:                   "created by local storage profile",
		CreatedAt:                now,
		UpdatedAt:                now,
		CreateIdempotencyKey:     request.IdempotencyKey,
		CreateRequestFingerprint: storageVolumeCreateFingerprint(request),
	}
	if providerConfigured {
		record.State = ports.StorageResourcePending
		record.Reason = "pending provider apply"
	}
	if record.MountInstanceID != "" {
		record.MountName = record.Name
		record.MountHistory = append(record.MountHistory, storageVolumeHistory(now, "mount", "success", record.MountInstanceID))
	}
	s.volumes[record.VolumeID] = record
	s.volumeIdempotency[idemKey] = record.VolumeID
	s.mu.Unlock()

	if err := s.upsertVolume(ctx, record); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	if !providerConfigured {
		return s.enrichStorageVolumeRecord(record), nil
	}

	observation, err := s.executeStorageProvider(ctx, "volume", record.VolumeID, func() ([]ports.WorkloadManifest, error) {
		return s.providerRenderer.RenderVolume(ctx, record)
	})
	if err != nil {
		record.State = ports.StorageResourceFailed
		record.Reason = err.Error()
		record.UpdatedAt = s.now().UTC()
		s.mu.Lock()
		s.volumes[record.VolumeID] = record
		s.mu.Unlock()
		_ = s.upsertVolume(ctx, record)
		return ports.StorageVolumeRecord{}, err
	}
	record.State = observation.State
	record.Reason = observation.Reason
	record.UpdatedAt = observation.ObservedAt
	s.mu.Lock()
	s.volumes[record.VolumeID] = record
	s.mu.Unlock()
	if err := s.upsertVolume(ctx, record); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return s.enrichStorageVolumeRecord(record), nil
}

func (s *LocalStorageService) ListVolumes(ctx context.Context, request ports.StorageResourceListRequest) ([]ports.StorageVolumeRecord, error) {
	if s.store != nil {
		items, err := s.store.ListVolumes(ctx, request.TenantID)
		if err != nil {
			return nil, err
		}
		out := make([]ports.StorageVolumeRecord, 0, len(items))
		for _, record := range items {
			out = append(out, s.enrichStorageVolumeRecord(record))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.StorageVolumeRecord, 0, len(s.volumes))
	for _, record := range s.volumes {
		if record.TenantID == request.TenantID && record.State != ports.StorageResourceDeleted {
			items = append(items, s.enrichStorageVolumeLocked(record))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalStorageService) GetVolume(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageVolumeRecord, error) {
	if s.store != nil {
		record, err := s.store.GetVolume(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.StorageVolumeRecord{}, err
		}
		return s.enrichStorageVolumeRecord(record), nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.volumes[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	return s.enrichStorageVolumeLocked(record), nil
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
		record.DeletedAt = record.UpdatedAt
		if err := s.upsertVolume(ctx, record); err != nil {
			return ports.StorageVolumeRecord{}, err
		}
		s.mu.Lock()
		s.volumes[record.VolumeID] = record
		s.mu.Unlock()
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

func (s *LocalStorageService) ExpandVolume(ctx context.Context, request ports.StorageVolumeExpandRequest) (ports.StorageVolumeRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.volumes[strings.TrimSpace(request.VolumeID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	if id, ok := s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "expand")]; ok && id == record.VolumeID {
		return record, nil
	}
	if request.SizeGiB <= record.SizeGiB {
		return ports.StorageVolumeRecord{}, fmt.Errorf("%w: size_gib must be greater than current volume size", ports.ErrInvalid)
	}
	record.SizeGiB = request.SizeGiB
	record.Reason = "expanded by local storage profile"
	record.UpdatedAt = s.now().UTC()
	s.volumes[record.VolumeID] = record
	s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "expand")] = record.VolumeID
	if err := s.upsertVolume(ctx, record); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) MountVolume(ctx context.Context, request ports.StorageVolumeMountRequest) (ports.StorageVolumeRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	if strings.TrimSpace(request.InstanceID) == "" || strings.TrimSpace(request.InstanceRoute) == "" {
		return ports.StorageVolumeRecord{}, fmt.Errorf("%w: instance_id and instance_route are required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.volumes[strings.TrimSpace(request.VolumeID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	if id, ok := s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "mount")]; ok && id == record.VolumeID {
		return record, nil
	}
	now := s.now().UTC()
	record.MountInstanceID = strings.TrimSpace(request.InstanceID)
	record.MountRoute = strings.TrimSpace(request.InstanceRoute)
	record.MountName = firstNetworkNonEmpty(strings.TrimSpace(request.MountName), record.Name)
	record.OSInitStatus = "pending"
	if record.OSInitDevice == "" {
		record.OSInitDevice = "/dev/disk/by-id/ani-" + record.VolumeID
	}
	record.MountHistory = append(record.MountHistory, storageVolumeHistory(now, "mount", "success", record.MountInstanceID))
	record.Reason = "mounted by local storage profile"
	record.UpdatedAt = now
	s.volumes[record.VolumeID] = record
	s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "mount")] = record.VolumeID
	if err := s.upsertVolume(ctx, record); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) UnmountVolume(ctx context.Context, request ports.StorageVolumeUnmountRequest) (ports.StorageVolumeRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.volumes[strings.TrimSpace(request.VolumeID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	if id, ok := s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "unmount")]; ok && id == record.VolumeID {
		return record, nil
	}
	now := s.now().UTC()
	target := record.MountInstanceID
	record.MountInstanceID = ""
	record.MountRoute = ""
	record.MountName = ""
	record.MountHistory = append(record.MountHistory, storageVolumeHistory(now, "unmount", "success", target))
	record.Reason = "unmounted by local storage profile"
	record.UpdatedAt = now
	s.volumes[record.VolumeID] = record
	s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "unmount")] = record.VolumeID
	if err := s.upsertVolume(ctx, record); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) CreateVolumeFromSnapshot(ctx context.Context, request ports.StorageVolumeFromSnapshotRequest) (ports.StorageVolumeRecord, error) {
	if err := requireStorageTenantAndName(request.TenantID, request.Name); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.volumes[strings.TrimSpace(request.VolumeID)]
	if !ok || source.TenantID != request.TenantID || source.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	snapshot, ok := s.snapshots[strings.TrimSpace(request.SnapshotID)]
	if !ok || snapshot.TenantID != request.TenantID || snapshot.VolumeID != source.VolumeID {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	if id, ok := s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "snapshot-volume")]; ok {
		if record, exists := s.volumes[id]; exists {
			return record, nil
		}
	}
	if request.SizeGiB < source.SizeGiB {
		return ports.StorageVolumeRecord{}, fmt.Errorf("%w: size_gib must be >= snapshot source volume size", ports.ErrInvalid)
	}
	now := s.now().UTC()
	record := ports.StorageVolumeRecord{
		TenantID:         request.TenantID,
		VolumeID:         "vol_" + uuid.NewString(),
		Name:             strings.TrimSpace(request.Name),
		SizeGiB:          request.SizeGiB,
		StorageClass:     source.StorageClass,
		Zone:             firstNetworkNonEmpty(strings.TrimSpace(request.Zone), source.Zone),
		VolumeType:       source.VolumeType,
		IOPS:             source.IOPS,
		Encrypted:        source.Encrypted,
		AutoSnapshot:     source.AutoSnapshot,
		OSInitStatus:     "n_a",
		OSInitDevice:     source.OSInitDevice,
		FromSnapshotID:   snapshot.SnapshotID,
		FromSnapshotName: snapshot.Name,
		MountHistory:     []ports.StorageVolumeMountHistoryEntry{storageVolumeHistory(now, "create_from_snapshot", "success", snapshot.SnapshotID)},
		State:            ports.StorageResourceAvailable,
		Reason:           "created from snapshot by local storage profile",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	s.volumes[record.VolumeID] = record
	s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "snapshot-volume")] = record.VolumeID
	if err := s.upsertVolume(ctx, record); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) SetVolumeAutoSnapshotPolicy(ctx context.Context, request ports.StorageVolumeAutoSnapshotPolicyUpdateRequest) (ports.StorageVolumeRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	if request.RetainDays < 1 || request.RetainDays > 365 || strings.TrimSpace(request.Schedule) == "" {
		return ports.StorageVolumeRecord{}, fmt.Errorf("%w: invalid auto snapshot policy", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.volumes[strings.TrimSpace(request.VolumeID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	if id, ok := s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "auto-snapshot")]; ok && id == record.VolumeID {
		return record, nil
	}
	record.AutoSnapshot = ports.StorageVolumeAutoSnapshotPolicy{Enabled: request.Enabled, RetainDays: request.RetainDays, Schedule: strings.TrimSpace(request.Schedule)}
	record.Reason = "auto snapshot policy updated by local storage profile"
	record.UpdatedAt = s.now().UTC()
	s.volumes[record.VolumeID] = record
	s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "auto-snapshot")] = record.VolumeID
	if err := s.upsertVolume(ctx, record); err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) GetVolumeOSInitGuide(ctx context.Context, request ports.StorageResourceGetRequest) (ports.VolumeOSInitGuide, error) {
	record, err := s.GetVolume(ctx, request)
	if err != nil {
		return ports.VolumeOSInitGuide{}, err
	}
	status := firstNetworkNonEmpty(record.OSInitStatus, "n_a")
	device := firstNetworkNonEmpty(record.OSInitDevice, "/dev/disk/by-id/ani-"+record.VolumeID)
	return ports.VolumeOSInitGuide{
		Status: status,
		Device: device,
		Steps: []ports.VolumeOSInitStep{
			{Title: "查看设备", Command: "ls -l " + device},
			{Title: "创建文件系统", Command: "mkfs.ext4 " + device},
			{Title: "挂载数据盘", Command: "mount " + device + " /mnt/data"},
		},
		Hint: "local profile only; run commands inside the target instance after attachment",
	}, nil
}

func (s *LocalStorageService) CompleteVolumeOSInit(ctx context.Context, request ports.VolumeOSInitCompleteRequest) (ports.StorageVolumeRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	mode := strings.TrimSpace(request.Mode)
	if mode != "done" && mode != "skipped" {
		return ports.StorageVolumeRecord{}, fmt.Errorf("%w: mode must be done or skipped", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.volumes[strings.TrimSpace(request.VolumeID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageVolumeRecord{}, ports.ErrNotFound
	}
	if id, ok := s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "os-init")]; ok && id == record.VolumeID {
		return record, nil
	}
	now := s.now().UTC()
	record.OSInitStatus = mode
	record.MountHistory = append(record.MountHistory, storageVolumeHistory(now, "os_init", "success", mode))
	record.Reason = "os init marked " + mode + " by local storage profile"
	record.UpdatedAt = now
	s.volumes[record.VolumeID] = record
	s.volumeOpIdem[storageOperationIdempotencyKey(idemKey, "os-init")] = record.VolumeID
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
	release, err := s.acquireStorageIdempotency(ctx, "filesystem.create/"+idemKey)
	if err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	defer release()
	s.mu.Lock()
	if id, ok := s.fsIdempotency[idemKey]; ok {
		if record, exists := s.filesystems[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	now := s.now().UTC()
	record := ports.StorageFilesystemRecord{
		TenantID:                 request.TenantID,
		FilesystemID:             "fs_" + uuid.NewString(),
		Name:                     strings.TrimSpace(request.Name),
		Protocol:                 protocol,
		SizeGiB:                  request.SizeGiB,
		Endpoint:                 "local://" + strings.TrimSpace(request.Name),
		Zone:                     strings.TrimSpace(request.Zone),
		PerformanceMode:          firstNetworkNonEmpty(request.PerformanceMode, "standard"),
		State:                    ports.StorageResourceAvailable,
		Reason:                   "created by local storage profile",
		CreatedAt:                now,
		UpdatedAt:                now,
		CreateIdempotencyKey:     request.IdempotencyKey,
		CreateRequestFingerprint: strings.Join([]string{strings.TrimSpace(request.Name), protocol, strconv.FormatInt(request.SizeGiB, 10)}, "|"),
	}
	record.MountCommand = storageFilesystemMountCommand(record, "127.0.0.1", "/mnt/"+record.Name).Command
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
		items, err := s.store.ListFilesystems(ctx, request.TenantID)
		if err != nil {
			return nil, err
		}
		out := make([]ports.StorageFilesystemRecord, 0, len(items))
		s.mu.RLock()
		for _, record := range items {
			out = append(out, s.enrichFilesystemLocked(record))
		}
		s.mu.RUnlock()
		sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.StorageFilesystemRecord, 0, len(s.filesystems))
	for _, record := range s.filesystems {
		if record.TenantID == request.TenantID && record.State != ports.StorageResourceDeleted {
			items = append(items, s.enrichFilesystemLocked(record))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *LocalStorageService) GetFilesystem(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageFilesystemRecord, error) {
	if s.store != nil {
		record, err := s.store.GetFilesystem(ctx, request.TenantID, request.ResourceID)
		if err != nil {
			return ports.StorageFilesystemRecord{}, err
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.enrichFilesystemLocked(record), nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.filesystems[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageFilesystemRecord{}, ports.ErrNotFound
	}
	return s.enrichFilesystemLocked(record), nil
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
		record.DeletedAt = record.UpdatedAt
		if err := s.upsertFilesystem(ctx, record); err != nil {
			return ports.StorageFilesystemRecord{}, err
		}
		s.mu.Lock()
		s.filesystems[record.FilesystemID] = record
		s.mu.Unlock()
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
	record.DeletedAt = record.UpdatedAt
	s.filesystems[record.FilesystemID] = record
	if err := s.upsertFilesystem(ctx, record); err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	return record, nil
}

func (s *LocalStorageService) ExpandFilesystem(ctx context.Context, request ports.StorageFilesystemExpandRequest) (ports.StorageFilesystemRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.filesystems[strings.TrimSpace(request.FilesystemID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageFilesystemRecord{}, ports.ErrNotFound
	}
	if id, ok := s.fsOpIdem[storageOperationIdempotencyKey(idemKey, "expand")]; ok && id == record.FilesystemID {
		return s.enrichFilesystemLocked(record), nil
	}
	if request.SizeGiB <= record.SizeGiB {
		return ports.StorageFilesystemRecord{}, fmt.Errorf("%w: size_gib must be greater than current filesystem size", ports.ErrInvalid)
	}
	record.SizeGiB = request.SizeGiB
	record.Reason = "expanded by local storage profile"
	record.UpdatedAt = s.now().UTC()
	s.filesystems[record.FilesystemID] = record
	s.fsOpIdem[storageOperationIdempotencyKey(idemKey, "expand")] = record.FilesystemID
	if err := s.upsertFilesystem(ctx, record); err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	return s.enrichFilesystemLocked(record), nil
}

func (s *LocalStorageService) CreateFilesystemMountTarget(ctx context.Context, request ports.FilesystemMountTargetCreateRequest) (ports.FilesystemMountTargetRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.FilesystemMountTargetRecord{}, err
	}
	if strings.TrimSpace(request.SubnetID) == "" {
		return ports.FilesystemMountTargetRecord{}, fmt.Errorf("%w: subnet_id is required", ports.ErrInvalid)
	}
	release, err := s.acquireStorageIdempotency(ctx, "filesystem.mount-target.create/"+idemKey)
	if err != nil {
		return ports.FilesystemMountTargetRecord{}, err
	}
	defer release()
	if s.store != nil {
		if existing, err := s.store.FindFilesystemMountTargetByCreateIdempotency(ctx, request.TenantID, request.IdempotencyKey); err == nil {
			return existing, nil
		} else if !errors.Is(err, ports.ErrNotFound) {
			return ports.FilesystemMountTargetRecord{}, err
		}
	}
	s.mu.Lock()
	filesystem, fsOK := s.filesystems[strings.TrimSpace(request.FilesystemID)]
	mountCount := len(s.mountTargets)
	if id, found := s.fsOpIdem[storageOperationIdempotencyKey(idemKey, "mount-target")]; found {
		for _, target := range s.mountTargets {
			if target.MountTargetID == id {
				s.mu.Unlock()
				return target, nil
			}
		}
	}
	s.mu.Unlock()
	ok := fsOK
	if (!ok || filesystem.TenantID != request.TenantID || filesystem.State == ports.StorageResourceDeleted) && s.store != nil {
		loaded, err := s.store.GetFilesystem(ctx, request.TenantID, strings.TrimSpace(request.FilesystemID))
		if err != nil {
			return ports.FilesystemMountTargetRecord{}, ports.ErrNotFound
		}
		filesystem = loaded
		ok = true
	}
	if !ok || filesystem.TenantID != request.TenantID || filesystem.State == ports.StorageResourceDeleted {
		return ports.FilesystemMountTargetRecord{}, ports.ErrNotFound
	}
	now := s.now().UTC()
	providerConfigured := s.storageProviderConfigured()
	target := ports.FilesystemMountTargetRecord{
		TenantID:                 filesystem.TenantID,
		MountTargetID:            "mt_" + uuid.NewString(),
		FilesystemID:             filesystem.FilesystemID,
		SubnetID:                 strings.TrimSpace(request.SubnetID),
		VPCID:                    strings.TrimSpace(request.VPCID),
		IPAddress:                storageFilesystemMountIP(mountCount + 10),
		Status:                   ports.MountTargetAvailable,
		CreatedAt:                now,
		UpdatedAt:                now,
		CreateIdempotencyKey:     request.IdempotencyKey,
		CreateRequestFingerprint: strings.Join([]string{filesystem.FilesystemID, strings.TrimSpace(request.SubnetID)}, "|"),
	}
	if providerConfigured {
		target.Status = ports.MountTargetCreating
	}
	if err := s.upsertFilesystemMountTarget(ctx, target); err != nil {
		return ports.FilesystemMountTargetRecord{}, err
	}
	if providerConfigured {
		observation, err := s.executeStorageProvider(ctx, "filesystem_mount_target", target.MountTargetID, func() ([]ports.WorkloadManifest, error) {
			return s.providerRenderer.RenderFilesystemMountTarget(ctx, target)
		})
		if err != nil {
			target.Status = ports.MountTargetError
			target.UpdatedAt = s.now().UTC()
			_ = s.upsertFilesystemMountTarget(ctx, target)
			return ports.FilesystemMountTargetRecord{}, err
		}
		target.Status = mountTargetStatusFromStorageState(observation.State)
		if !observation.ObservedAt.IsZero() {
			target.CreatedAt = observation.ObservedAt
			target.UpdatedAt = observation.ObservedAt
		}
		if err := s.upsertFilesystemMountTarget(ctx, target); err != nil {
			return ports.FilesystemMountTargetRecord{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mountTargets[target.MountTargetID] = target
	s.fsOpIdem[storageOperationIdempotencyKey(idemKey, "mount-target")] = target.MountTargetID
	return target, nil
}

func (s *LocalStorageService) MountFilesystem(ctx context.Context, request ports.StorageFilesystemMountRequest) (ports.StorageFilesystemRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	if strings.TrimSpace(request.InstanceID) == "" || strings.TrimSpace(request.InstanceRoute) == "" {
		return ports.StorageFilesystemRecord{}, fmt.Errorf("%w: instance_id and instance_route are required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.filesystems[strings.TrimSpace(request.FilesystemID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageFilesystemRecord{}, ports.ErrNotFound
	}
	if id, ok := s.fsOpIdem[storageOperationIdempotencyKey(idemKey, "mount")]; ok && id == record.FilesystemID {
		return s.enrichFilesystemLocked(record), nil
	}
	mountPath := firstNetworkNonEmpty(strings.TrimSpace(request.MountPath), "/mnt/nfs")
	attachment := ports.FilesystemAttachment{
		InstanceID:    strings.TrimSpace(request.InstanceID),
		InstanceRoute: strings.TrimSpace(request.InstanceRoute),
		MountPath:     mountPath,
		Protocol:      record.Protocol,
		AutoMount:     request.AutoMount,
		AttachedAt:    s.now().UTC(),
	}
	for _, target := range s.mountTargets {
		if target.FilesystemID == record.FilesystemID &&
			target.Status == ports.MountTargetAvailable &&
			strings.TrimSpace(target.IPAddress) != "" {
			attachment.IPAddress = target.IPAddress
			break
		}
	}
	if attachment.IPAddress == "" {
		return ports.StorageFilesystemRecord{}, fmt.Errorf("%w: an available filesystem mount target is required", ports.ErrFailedPrecondition)
	}
	record.AttachedInstances = append(replaceFilesystemAttachment(record.AttachedInstances, attachment), attachment)
	record.Mounts = len(record.AttachedInstances)
	record.MountCommand = storageFilesystemMountCommand(record, attachment.IPAddress, mountPath).Command
	record.Reason = "mounted by local storage profile"
	record.UpdatedAt = s.now().UTC()
	s.filesystems[record.FilesystemID] = record
	s.fsOpIdem[storageOperationIdempotencyKey(idemKey, "mount")] = record.FilesystemID
	if err := s.upsertFilesystem(ctx, record); err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	return s.enrichFilesystemLocked(record), nil
}

func (s *LocalStorageService) UnmountFilesystem(ctx context.Context, request ports.StorageFilesystemUnmountRequest) (ports.StorageFilesystemRecord, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	if strings.TrimSpace(request.InstanceID) == "" {
		return ports.StorageFilesystemRecord{}, fmt.Errorf("%w: instance_id is required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.filesystems[strings.TrimSpace(request.FilesystemID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.StorageFilesystemRecord{}, ports.ErrNotFound
	}
	if id, ok := s.fsOpIdem[storageOperationIdempotencyKey(idemKey, "unmount")]; ok && id == record.FilesystemID {
		return s.enrichFilesystemLocked(record), nil
	}
	instanceID := strings.TrimSpace(request.InstanceID)
	kept := make([]ports.FilesystemAttachment, 0, len(record.AttachedInstances))
	for _, attachment := range record.AttachedInstances {
		if attachment.InstanceID != instanceID {
			kept = append(kept, attachment)
		}
	}
	record.AttachedInstances = kept
	record.Mounts = len(kept)
	record.Reason = "unmounted by local storage profile"
	record.UpdatedAt = s.now().UTC()
	s.filesystems[record.FilesystemID] = record
	s.fsOpIdem[storageOperationIdempotencyKey(idemKey, "unmount")] = record.FilesystemID
	if err := s.upsertFilesystem(ctx, record); err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	return s.enrichFilesystemLocked(record), nil
}

func (s *LocalStorageService) GetFilesystemMountCommand(_ context.Context, request ports.StorageResourceGetRequest) (ports.FilesystemMountCommand, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.filesystems[strings.TrimSpace(request.ResourceID)]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		return ports.FilesystemMountCommand{}, ports.ErrNotFound
	}
	ipAddress := "127.0.0.1"
	for _, target := range s.mountTargets {
		if target.FilesystemID == record.FilesystemID && target.Status == ports.MountTargetAvailable {
			ipAddress = target.IPAddress
			break
		}
	}
	return storageFilesystemMountCommand(record, ipAddress, "/mnt/"+record.Name), nil
}

func (s *LocalStorageService) CreateObject(ctx context.Context, request ports.StorageObjectCreateRequest) (ports.StorageObjectRecord, error) {
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
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
		TenantID:                 request.TenantID,
		ObjectID:                 "obj_" + uuid.NewString(),
		Bucket:                   strings.TrimSpace(request.Bucket),
		Key:                      strings.TrimSpace(request.Key),
		SizeBytes:                request.SizeBytes,
		ContentType:              firstNetworkNonEmpty(request.ContentType, "application/octet-stream"),
		State:                    ports.StorageResourceAvailable,
		Reason:                   "created by local storage profile",
		CreatedAt:                now,
		UpdatedAt:                now,
		CreateIdempotencyKey:     request.IdempotencyKey,
		CreateRequestFingerprint: strings.Join([]string{strings.TrimSpace(request.Bucket), strings.TrimSpace(request.Key), strconv.FormatInt(request.SizeBytes, 10)}, "|"),
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
		items, err := s.store.ListObjects(ctx, request.TenantID)
		if err != nil {
			return nil, err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
		return items, nil
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
	s.mu.RLock()
	record, ok := s.objects[request.ResourceID]
	if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
		s.mu.RUnlock()
		record = ports.StorageObjectRecord{}
		ok = false
	}
	objectStore := s.objectStore
	s.mu.RUnlock()
	if !ok && s.store != nil {
		if loaded, err := s.store.GetObject(ctx, request.TenantID, request.ResourceID); err == nil && loaded.ObjectID != "" &&
			loaded.TenantID == request.TenantID && loaded.State != ports.StorageResourceDeleted {
			record = loaded
			ok = true
		}
	}
	if !ok {
		return ports.StorageObjectRecord{}, ports.ErrNotFound
	}

	if objectStore != nil {
		if err := objectStore.DeleteObject(ctx, storageObjectRef(record)); err != nil && err != ports.ErrNotFound {
			return ports.StorageObjectRecord{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if current, exists := s.objects[request.ResourceID]; exists {
		if current.TenantID != request.TenantID || current.State == ports.StorageResourceDeleted {
			return ports.StorageObjectRecord{}, ports.ErrNotFound
		}
		record = current
	}
	record.State = ports.StorageResourceDeleted
	record.Reason = "deleted by local storage profile"
	record.UpdatedAt = s.now().UTC()
	s.objects[record.ObjectID] = record
	if err := s.upsertObject(ctx, record); err != nil {
		return ports.StorageObjectRecord{}, err
	}
	slog.Info("storage object deleted",
		"tenant_id", record.TenantID,
		"object_id", record.ObjectID,
		"bucket", record.Bucket,
		"key", record.Key,
	)
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
		if existing, err := s.store.FindBucketByCreateIdempotency(ctx, request.TenantID, request.IdempotencyKey); err == nil {
			return s.enrichStorageBucketRecord(existing), nil
		} else if !errors.Is(err, ports.ErrNotFound) {
			return ports.StorageBucketRecord{}, err
		}
	}

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

	now := s.now().UTC()
	region := strings.TrimSpace(request.Region)
	if region == "" {
		region = "cn-east-1"
	}
	record := ports.StorageBucketRecord{
		TenantID:                 request.TenantID,
		BucketID:                 uuid.NewString(),
		Name:                     strings.TrimSpace(request.Name),
		Region:                   region,
		Endpoint:                 storageBucketEndpoint(region),
		AccessMode:               accessMode,
		ACL:                      "private",
		ACLLabel:                 storageBucketACLLabel("private"),
		StorageClass:             "standard",
		Versioning:               "disabled",
		LifecycleRules:           []ports.StorageBucketLifecycleRule{},
		LifecycleNote:            "未配置生命周期规则",
		State:                    ports.StorageResourcePending,
		Reason:                   "pending object store apply",
		CreatedAt:                now,
		UpdatedAt:                now,
		CreateIdempotencyKey:     request.IdempotencyKey,
		CreateRequestFingerprint: strings.Join([]string{strings.TrimSpace(request.Name), accessMode, region}, "|"),
	}
	if s.objectStore == nil {
		record.State = ports.StorageResourceAvailable
		record.Reason = "created by local storage profile"
	}
	if err := s.upsertBucket(ctx, record); err != nil {
		return ports.StorageBucketRecord{}, err
	}
	if s.objectStore != nil {
		if err := s.objectStore.EnsureBucket(ctx, ports.BucketClass(record.Name)); err != nil {
			slog.Warn("storage bucket object store ensure failed",
				"tenant_id", record.TenantID,
				"bucket_id", record.BucketID,
				"name", record.Name,
				"err", err,
			)
			record.State = ports.StorageResourceFailed
			record.Reason = err.Error()
			record.UpdatedAt = s.now().UTC()
			_ = s.upsertBucket(ctx, record)
			return ports.StorageBucketRecord{}, err
		}
		record.State = ports.StorageResourceAvailable
		record.Reason = "created by local storage profile"
		record.UpdatedAt = s.now().UTC()
		if err := s.upsertBucket(ctx, record); err != nil {
			return ports.StorageBucketRecord{}, err
		}
	}
	slog.Info("storage bucket created",
		"tenant_id", record.TenantID,
		"bucket_id", record.BucketID,
		"name", record.Name,
		"access_mode", record.AccessMode,
		"object_store_configured", s.objectStore != nil,
		"control_plane_store_configured", s.store != nil,
	)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buckets[record.BucketID] = record
	s.bucketIdem[idemKey] = record.BucketID
	return s.enrichStorageBucketLocked(record), nil
}

func (s *LocalStorageService) ListStorageBuckets(ctx context.Context, request ports.StorageResourceListRequest) ([]ports.StorageBucketRecord, error) {
	if s.store != nil {
		items, err := s.store.ListBuckets(ctx, request.TenantID)
		if err != nil {
			return nil, err
		}
		out := make([]ports.StorageBucketRecord, 0, len(items))
		for _, bucket := range items {
			out = append(out, s.enrichStorageBucketRecord(bucket))
		}
		for i := range out {
			s.enrichBucketUsage(ctx, &out[i])
		}
		sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
		return out, nil
	}
	s.mu.RLock()
	items := make([]ports.StorageBucketRecord, 0, len(s.buckets))
	for _, bucket := range s.buckets {
		if bucket.TenantID != request.TenantID || bucket.State == ports.StorageResourceDeleted {
			continue
		}
		items = append(items, s.enrichStorageBucketLocked(bucket))
	}
	s.mu.RUnlock()
	for i := range items {
		s.enrichBucketUsage(ctx, &items[i])
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
	release, err := s.acquireStorageIdempotency(ctx, "object.upload.create/"+idemKey)
	if err != nil {
		return ports.StorageObjectUploadRecord{}, err
	}
	defer release()

	s.mu.RLock()
	if id, ok := s.uploadIdem[idemKey]; ok {
		if object, exists := s.objects[id]; exists && object.TenantID == request.TenantID && object.State != ports.StorageResourceDeleted {
			s.mu.RUnlock()
			return s.signedUploadForObject(ctx, object, request.ExpiresSeconds)
		}
	}
	s.mu.RUnlock()
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageObjectUploadRecord{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}

	now := s.now().UTC()
	object := ports.StorageObjectRecord{
		TenantID:    request.TenantID,
		ObjectID:    uuid.NewString(),
		Bucket:      bucket.Name,
		Key:         strings.TrimSpace(request.Key),
		SizeBytes:   request.SizeBytes,
		ContentType: firstNetworkNonEmpty(request.ContentType, "application/octet-stream"),
		State:       ports.StorageResourceAvailable,
		Reason:      "created by local storage upload profile",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_ = firstNetworkNonEmpty(request.StorageClass, "standard")
	result, err := s.signedUploadForObject(ctx, object, request.ExpiresSeconds)
	if err != nil {
		return ports.StorageObjectUploadRecord{}, err
	}
	// Persist at creation so a presigned upload survives gateway restarts
	// between upload and complete; the store is the control-plane authority.
	if err := s.upsertObject(ctx, object); err != nil {
		return ports.StorageObjectUploadRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[object.ObjectID] = object
	s.uploadIdem[idemKey] = object.ObjectID
	slog.Info("storage object upload created",
		"tenant_id", object.TenantID,
		"object_id", object.ObjectID,
		"bucket_id", strings.TrimSpace(request.BucketID),
		"bucket", object.Bucket,
		"key", object.Key,
		"expires_at", result.ExpiresAt.Format(time.RFC3339),
	)
	return result, nil
}

func (s *LocalStorageService) GetStorageObjectDownload(ctx context.Context, request ports.StorageObjectDownloadRequest) (ports.StorageObjectDownloadRecord, error) {
	s.mu.RLock()
	object, ok := s.objects[strings.TrimSpace(request.ObjectID)]
	s.mu.RUnlock()
	if (!ok || object.TenantID != request.TenantID || object.State == ports.StorageResourceDeleted) && s.store != nil {
		if loaded, err := s.store.GetObject(ctx, request.TenantID, strings.TrimSpace(request.ObjectID)); err == nil && loaded.ObjectID != "" {
			object = loaded
			ok = true
		}
	}
	if !ok || object.TenantID != request.TenantID || object.State == ports.StorageResourceDeleted {
		return ports.StorageObjectDownloadRecord{}, ports.ErrNotFound
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

func (s *LocalStorageService) CompleteStorageObject(ctx context.Context, request ports.StorageObjectCompleteRequest) (ports.StorageObjectRecord, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ObjectID) == "" {
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: tenant_id and object_id are required", ports.ErrInvalid)
	}
	if _, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey); err != nil {
		return ports.StorageObjectRecord{}, err
	}
	objectID := strings.TrimSpace(request.ObjectID)
	s.mu.RLock()
	object, ok := s.objects[objectID]
	s.mu.RUnlock()
	if !ok && s.store != nil {
		if loaded, err := s.store.GetObject(ctx, request.TenantID, objectID); err == nil && loaded.ObjectID != "" {
			object = loaded
			ok = true
		}
	}
	if !ok || object.TenantID != request.TenantID || object.State == ports.StorageResourceDeleted {
		slog.Warn("storage object complete miss",
			"tenant_id", request.TenantID,
			"object_id", objectID,
			"control_plane_store_configured", s.store != nil,
		)
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: object %s not found", ports.ErrNotFound, objectID)
	}
	if s.objectStore != nil {
		metadata, err := s.objectStore.StatObject(ctx, storageObjectRef(object))
		if err != nil {
			slog.Warn("storage object complete precondition failed",
				"tenant_id", request.TenantID,
				"object_id", objectID,
				"bucket", object.Bucket,
				"key", object.Key,
				"err", err,
			)
			return ports.StorageObjectRecord{}, fmt.Errorf("%w: object content not uploaded yet: %v", ports.ErrFailedPrecondition, err)
		}
		if metadata.SizeBytes > 0 {
			object.SizeBytes = metadata.SizeBytes
		}
		if strings.TrimSpace(metadata.ContentType) != "" {
			object.ContentType = metadata.ContentType
		}
	}
	object.State = ports.StorageResourceAvailable
	object.Reason = "upload completed"
	object.UpdatedAt = s.now().UTC()
	s.mu.Lock()
	s.objects[objectID] = object
	s.mu.Unlock()
	if err := s.upsertObject(ctx, object); err != nil {
		return ports.StorageObjectRecord{}, err
	}
	slog.Info("storage object completed",
		"tenant_id", object.TenantID,
		"object_id", objectID,
		"bucket", object.Bucket,
		"key", object.Key,
		"size_bytes", object.SizeBytes,
		"control_plane_store_configured", s.store != nil,
	)
	return object, nil
}

func (s *LocalStorageService) GetStorageBucket(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageBucketRecord, error) {
	if s.store != nil {
		bucket, err := s.store.GetBucket(ctx, request.TenantID, strings.TrimSpace(request.ResourceID))
		if err != nil {
			return ports.StorageBucketRecord{}, err
		}
		enriched := s.enrichStorageBucketRecord(bucket)
		s.enrichBucketUsage(ctx, &enriched)
		return enriched, nil
	}
	s.mu.RLock()
	bucket, ok := s.buckets[strings.TrimSpace(request.ResourceID)]
	if !ok || bucket.TenantID != request.TenantID || bucket.State == ports.StorageResourceDeleted {
		s.mu.RUnlock()
		return ports.StorageBucketRecord{}, ports.ErrNotFound
	}
	enriched := s.enrichStorageBucketLocked(bucket)
	s.mu.RUnlock()
	s.enrichBucketUsage(ctx, &enriched)
	return enriched, nil
}

// hydrateObjectsFromStore backfills the in-memory object cache from the
// control-plane store authority so bucket-level object operations survive
// gateway restarts. Records already cached win to preserve in-flight
// mutations; lookup failures only log and keep the cache as-is.
func (s *LocalStorageService) hydrateObjectsFromStore(ctx context.Context, tenantID string) {
	if s.store == nil {
		return
	}
	loaded, err := s.store.ListObjects(ctx, tenantID)
	if err != nil {
		slog.Warn("storage object hydrate from store failed",
			"tenant_id", tenantID,
			"err", err,
		)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range loaded {
		if record.TenantID != tenantID || record.ObjectID == "" || record.State == ports.StorageResourceDeleted {
			continue
		}
		if _, exists := s.objects[record.ObjectID]; exists {
			continue
		}
		s.objects[record.ObjectID] = record
	}
}

// resolveBucket returns the bucket record for bucketID, preferring the
// in-memory cache and falling back to the shared store authority so
// bucket-level operations survive gateway restarts.
func (s *LocalStorageService) resolveBucket(ctx context.Context, tenantID string, bucketID string) (ports.StorageBucketRecord, bool) {
	bucketID = strings.TrimSpace(bucketID)
	s.mu.RLock()
	bucket, ok := s.buckets[bucketID]
	s.mu.RUnlock()
	if ok && (bucket.TenantID != tenantID || bucket.State == ports.StorageResourceDeleted) {
		s.logBucketResolveMiss(tenantID, bucketID, "memory_record_unusable", bucket.State, nil)
		return ports.StorageBucketRecord{}, false
	}
	if !ok && s.store != nil {
		loaded, err := s.store.GetBucket(ctx, tenantID, bucketID)
		if err == nil && loaded.BucketID != "" {
			return loaded, true
		}
		s.logBucketResolveMiss(tenantID, bucketID, "store_lookup_miss", ports.StorageResourceState(""), err)
		return ports.StorageBucketRecord{}, false
	}
	if !ok {
		s.logBucketResolveMiss(tenantID, bucketID, "memory_miss", ports.StorageResourceState(""), nil)
	}
	return bucket, ok
}

// logBucketResolveMiss explains why a bucket-level request resolved to
// NOT_FOUND so operators can distinguish missing control-plane store config,
// cross-tenant access and genuinely absent buckets.
func (s *LocalStorageService) logBucketResolveMiss(tenantID string, bucketID string, reason string, state ports.StorageResourceState, storeErr error) {
	attrs := []any{
		"tenant_id", tenantID,
		"bucket_id", bucketID,
		"reason", reason,
		"control_plane_store_configured", s.store != nil,
	}
	if state != "" {
		attrs = append(attrs, "memory_state", string(state))
	}
	if storeErr != nil && !errors.Is(storeErr, ports.ErrNotFound) {
		attrs = append(attrs, "store_err", storeErr.Error())
	}
	slog.Warn("storage bucket resolve miss", attrs...)
}

func (s *LocalStorageService) ListBucketObjects(ctx context.Context, request ports.StorageBucketObjectListRequest) (ports.StorageBucketObjectListResult, error) {
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageBucketObjectListResult{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}
	// Bucket object listings must survive gateway restarts: backfill the
	// in-memory object cache from the control-plane store authority first.
	s.hydrateObjectsFromStore(ctx, request.TenantID)
	prefix := strings.TrimSpace(request.Prefix)
	if prefix == "/" {
		prefix = ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.StorageBucketObjectEntry, 0)
	// prefixes first
	if prefixes, exists := s.bucketPrefixes[storageBucketPrefixMapKey(bucket.TenantID, bucket.BucketID)]; exists {
		for key, entry := range prefixes {
			if !storageBucketKeyUnderPrefix(key, prefix) {
				continue
			}
			// only show direct children under prefix
			rel := strings.TrimPrefix(key, prefix)
			if rel == "" {
				continue
			}
			if strings.Count(strings.TrimSuffix(rel, "/"), "/") > 0 && strings.HasSuffix(key, "/") {
				// nested prefix: collapse to first segment
				seg := strings.SplitN(rel, "/", 2)[0] + "/"
				childKey := prefix + seg
				items = append(items, ports.StorageBucketObjectEntry{
					Kind: "prefix",
					Name: seg,
					Key:  childKey,
				})
				continue
			}
			if strings.HasSuffix(key, "/") && strings.Count(strings.TrimSuffix(rel, "/"), "/") == 0 {
				items = append(items, entry)
			}
		}
	}
	seenPrefix := map[string]bool{}
	directPrefixes := make([]ports.StorageBucketObjectEntry, 0)
	for _, item := range items {
		if seenPrefix[item.Key] {
			continue
		}
		seenPrefix[item.Key] = true
		directPrefixes = append(directPrefixes, item)
	}
	items = directPrefixes

	for _, object := range s.objects {
		if object.TenantID != bucket.TenantID || object.Bucket != bucket.Name || object.State == ports.StorageResourceDeleted {
			continue
		}
		if !storageBucketKeyUnderPrefix(object.Key, prefix) {
			continue
		}
		rel := strings.TrimPrefix(object.Key, prefix)
		if rel == "" {
			continue
		}
		if idx := strings.Index(rel, "/"); idx >= 0 {
			// object under nested path becomes a prefix entry for the first segment
			seg := rel[:idx+1]
			childKey := prefix + seg
			if !seenPrefix[childKey] {
				seenPrefix[childKey] = true
				items = append(items, ports.StorageBucketObjectEntry{
					Kind: "prefix",
					Name: seg,
					Key:  childKey,
				})
			}
			continue
		}
		size := object.SizeBytes
		items = append(items, ports.StorageBucketObjectEntry{
			Kind:         "object",
			Name:         rel,
			Key:          object.Key,
			SizeBytes:    &size,
			SizeLabel:    storageSizeLabel(size),
			StorageClass: firstNetworkNonEmpty(bucket.StorageClass, "standard"),
			UpdatedAt:    object.UpdatedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "prefix"
		}
		return items[i].Key < items[j].Key
	})
	total := len(items)
	// Local profile uses a simple offset cursor over an in-memory snapshot. Real
	// object-store adapters must use provider-native continuation tokens.
	start := 0
	if request.Cursor != "" {
		offset, err := strconv.Atoi(strings.TrimSpace(request.Cursor))
		if err != nil || offset < 0 || offset > total {
			return ports.StorageBucketObjectListResult{}, fmt.Errorf("%w: invalid cursor", ports.ErrInvalid)
		}
		start = offset
	}
	limit := request.Limit
	if limit <= 0 {
		limit = total
	}
	if limit > 100 {
		limit = 100
	}
	end := start + limit
	if end > total {
		end = total
	}
	nextCursor := ""
	if end < total {
		nextCursor = strconv.Itoa(end)
	}
	return ports.StorageBucketObjectListResult{
		Items:      items[start:end],
		Total:      total,
		Prefix:     firstNetworkNonEmpty(prefix, "/"),
		NextCursor: nextCursor,
	}, nil
}

func (s *LocalStorageService) DeleteBucketObject(ctx context.Context, request ports.StorageBucketObjectDeleteRequest) (ports.StorageBucketObjectDeleteResult, error) {
	key := strings.TrimSpace(request.Key)
	if key == "" {
		return ports.StorageBucketObjectDeleteResult{}, fmt.Errorf("%w: key is required", ports.ErrInvalid)
	}
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageBucketObjectDeleteResult{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}
	// Backfill the object cache from the store authority so deletes survive
	// gateway restarts the same way listings do.
	s.hydrateObjectsFromStore(ctx, request.TenantID)
	s.mu.Lock()
	// delete prefix marker
	if prefixes, exists := s.bucketPrefixes[storageBucketPrefixMapKey(bucket.TenantID, bucket.BucketID)]; exists {
		if _, has := prefixes[key]; has {
			delete(prefixes, key)
			s.mu.Unlock()
			return ports.StorageBucketObjectDeleteResult{BucketID: bucket.BucketID, Key: key, Deleted: true}, nil
		}
	}
	var target *ports.StorageObjectRecord
	var targetID string
	for id, object := range s.objects {
		if object.TenantID == bucket.TenantID && object.Bucket == bucket.Name && object.Key == key && object.State != ports.StorageResourceDeleted {
			obj := object
			target = &obj
			targetID = id
			break
		}
	}
	s.mu.Unlock()
	if target == nil {
		return ports.StorageBucketObjectDeleteResult{}, ports.ErrNotFound
	}
	if s.objectStore != nil {
		if err := s.objectStore.DeleteObject(ctx, storageObjectRef(*target)); err != nil && err != ports.ErrNotFound {
			return ports.StorageBucketObjectDeleteResult{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if object, ok := s.objects[targetID]; ok {
		object.State = ports.StorageResourceDeleted
		object.UpdatedAt = s.now().UTC()
		s.objects[targetID] = object
	}
	return ports.StorageBucketObjectDeleteResult{BucketID: bucket.BucketID, Key: key, Deleted: true}, nil
}

func (s *LocalStorageService) CreateBucketPrefix(ctx context.Context, request ports.StorageBucketPrefixCreateRequest) (ports.StorageBucketObjectEntry, error) {
	prefix := strings.TrimSpace(request.Prefix)
	if prefix == "" {
		return ports.StorageBucketObjectEntry{}, fmt.Errorf("%w: prefix is required", ports.ErrInvalid)
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageBucketObjectEntry{}, err
	}
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageBucketObjectEntry{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mapKey := storageBucketPrefixMapKey(bucket.TenantID, bucket.BucketID)
	if existingKey, ok := s.prefixIdem[idemKey]; ok {
		if prefixes, exists := s.bucketPrefixes[mapKey]; exists {
			if entry, found := prefixes[existingKey]; found {
				return entry, nil
			}
		}
	}
	if prefixes, exists := s.bucketPrefixes[mapKey]; exists {
		if _, found := prefixes[prefix]; found {
			return ports.StorageBucketObjectEntry{}, fmt.Errorf("%w: prefix already exists", ports.ErrConflict)
		}
	}
	entry := ports.StorageBucketObjectEntry{
		Kind: "prefix",
		Name: storageBucketEntryName(prefix),
		Key:  prefix,
	}
	if s.bucketPrefixes[mapKey] == nil {
		s.bucketPrefixes[mapKey] = map[string]ports.StorageBucketObjectEntry{}
	}
	s.bucketPrefixes[mapKey][prefix] = entry
	s.prefixIdem[idemKey] = prefix
	return entry, nil
}

func (s *LocalStorageService) GenerateBucketObjectPresignedURL(ctx context.Context, request ports.StorageBucketPresignedURLRequest) (ports.StorageObjectDownloadRecord, error) {
	key := strings.TrimSpace(request.Key)
	if key == "" {
		return ports.StorageObjectDownloadRecord{}, fmt.Errorf("%w: key is required", ports.ErrInvalid)
	}
	method := strings.ToUpper(firstNetworkNonEmpty(strings.TrimSpace(request.Method), "GET"))
	if method != "GET" && method != "PUT" {
		return ports.StorageObjectDownloadRecord{}, fmt.Errorf("%w: unsupported method %q", ports.ErrUnsupported, request.Method)
	}
	hours := request.ExpiresHours
	if hours <= 0 {
		hours = 24
	}
	if hours > 168 {
		hours = 168
	}
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageObjectDownloadRecord{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}
	s.mu.RLock()
	var object ports.StorageObjectRecord
	found := false
	for _, item := range s.objects {
		if item.TenantID == bucket.TenantID && item.Bucket == bucket.Name && item.Key == key && item.State != ports.StorageResourceDeleted {
			object = item
			found = true
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		// allow PUT for not-yet-uploaded keys
		if method != "PUT" {
			return ports.StorageObjectDownloadRecord{}, ports.ErrNotFound
		}
		object = ports.StorageObjectRecord{
			TenantID:    request.TenantID,
			ObjectID:    uuid.NewString(),
			Bucket:      bucket.Name,
			Key:         key,
			ContentType: "application/octet-stream",
		}
	}
	ttl := time.Duration(hours) * time.Hour
	ref := storageObjectRef(object)
	var signed ports.SignedURL
	var err error
	if method == "PUT" {
		signed, err = s.signedUploadURL(ctx, ref, ttl)
	} else {
		signed, err = s.signedDownloadURL(ctx, ref, ttl)
	}
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

func (s *LocalStorageService) SetStorageBucketACL(ctx context.Context, request ports.StorageBucketACLUpdateRequest) (ports.StorageBucketRecord, error) {
	acl := strings.TrimSpace(request.ACL)
	if acl != "private" && acl != "tenant_read" {
		return ports.StorageBucketRecord{}, fmt.Errorf("%w: unsupported acl %q", ports.ErrUnsupported, request.ACL)
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageBucketRecord{}, err
	}
	s.mu.RLock()
	replayID, hasReplay := s.bucketUpdateIdem[idemKey]
	s.mu.RUnlock()
	if hasReplay {
		if bucket, ok := s.resolveBucket(ctx, request.TenantID, replayID); ok {
			return s.enrichStorageBucketRecord(bucket), nil
		}
	}
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageBucketRecord{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket.ACL = acl
	bucket.ACLLabel = storageBucketACLLabel(acl)
	if acl == "tenant_read" {
		bucket.AccessMode = "public_read"
	} else {
		bucket.AccessMode = "private"
	}
	bucket.UpdatedAt = s.now().UTC()
	s.buckets[bucket.BucketID] = bucket
	s.bucketUpdateIdem[idemKey] = bucket.BucketID
	return s.enrichStorageBucketLocked(bucket), nil
}

func (s *LocalStorageService) SetStorageBucketClass(ctx context.Context, request ports.StorageBucketClassUpdateRequest) (ports.StorageBucketRecord, error) {
	class := strings.TrimSpace(request.StorageClass)
	if class != "standard" && class != "infrequent_access" {
		return ports.StorageBucketRecord{}, fmt.Errorf("%w: unsupported storage_class %q", ports.ErrUnsupported, request.StorageClass)
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageBucketRecord{}, err
	}
	s.mu.RLock()
	replayID, hasReplay := s.bucketUpdateIdem[idemKey]
	s.mu.RUnlock()
	if hasReplay {
		if bucket, ok := s.resolveBucket(ctx, request.TenantID, replayID); ok {
			return s.enrichStorageBucketRecord(bucket), nil
		}
	}
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageBucketRecord{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket.StorageClass = class
	bucket.UpdatedAt = s.now().UTC()
	s.buckets[bucket.BucketID] = bucket
	s.bucketUpdateIdem[idemKey] = bucket.BucketID
	return s.enrichStorageBucketLocked(bucket), nil
}

func (s *LocalStorageService) ListStorageBucketLifecycleRules(ctx context.Context, request ports.StorageResourceGetRequest) (ports.StorageBucketLifecycleRuleListResult, error) {
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.ResourceID)
	if !ok {
		return ports.StorageBucketLifecycleRuleListResult{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.ResourceID))
	}
	rules := append([]ports.StorageBucketLifecycleRule{}, bucket.LifecycleRules...)
	return ports.StorageBucketLifecycleRuleListResult{Items: rules, Total: len(rules)}, nil
}

func (s *LocalStorageService) SetStorageBucketLifecycleRules(ctx context.Context, request ports.StorageBucketLifecycleRulesUpdateRequest) (ports.StorageBucketLifecycleRuleListResult, error) {
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageBucketLifecycleRuleListResult{}, err
	}
	rules := make([]ports.StorageBucketLifecycleRule, 0, len(request.Rules))
	for _, rule := range request.Rules {
		item := rule
		if strings.TrimSpace(item.ID) == "" {
			item.ID = uuid.NewString()
		}
		if err := validateStorageBucketLifecycleRuleFields(item.Name, item.ExpireDays, item.ToInfrequentDays); err != nil {
			return ports.StorageBucketLifecycleRuleListResult{}, err
		}
		rules = append(rules, item)
	}
	if s.store != nil {
		bucket, err := s.store.GetBucket(ctx, request.TenantID, strings.TrimSpace(request.BucketID))
		if err != nil {
			return ports.StorageBucketLifecycleRuleListResult{}, err
		}
		bucket.LifecycleRules = rules
		bucket.LifecycleNote = storageBucketLifecycleNote(len(rules))
		bucket.UpdatedAt = s.now().UTC()
		if err := s.store.ReplaceBucketLifecycleRules(ctx, request.TenantID, bucket.BucketID, rules); err != nil {
			return ports.StorageBucketLifecycleRuleListResult{}, err
		}
		if err := s.upsertBucket(ctx, bucket); err != nil {
			return ports.StorageBucketLifecycleRuleListResult{}, err
		}
		s.mu.Lock()
		s.buckets[bucket.BucketID] = bucket
		s.bucketUpdateIdem[idemKey] = bucket.BucketID
		s.mu.Unlock()
		return ports.StorageBucketLifecycleRuleListResult{Items: append([]ports.StorageBucketLifecycleRule{}, rules...), Total: len(rules)}, nil
	}
	s.mu.RLock()
	replayID, hasReplay := s.bucketUpdateIdem[idemKey]
	var replayRules []ports.StorageBucketLifecycleRule
	if hasReplay {
		if bucket, exists := s.buckets[replayID]; exists {
			replayRules = append([]ports.StorageBucketLifecycleRule{}, bucket.LifecycleRules...)
		}
	}
	s.mu.RUnlock()
	if hasReplay && replayRules != nil {
		return ports.StorageBucketLifecycleRuleListResult{Items: replayRules, Total: len(replayRules)}, nil
	}
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageBucketLifecycleRuleListResult{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket.LifecycleRules = rules
	bucket.LifecycleNote = storageBucketLifecycleNote(len(rules))
	bucket.UpdatedAt = s.now().UTC()
	s.buckets[bucket.BucketID] = bucket
	s.bucketUpdateIdem[idemKey] = bucket.BucketID
	return ports.StorageBucketLifecycleRuleListResult{Items: append([]ports.StorageBucketLifecycleRule{}, rules...), Total: len(rules)}, nil
}

func (s *LocalStorageService) CreateStorageBucketLifecycleRule(ctx context.Context, request ports.StorageBucketLifecycleRuleCreateRequest) (ports.StorageBucketLifecycleRule, error) {
	if err := validateStorageBucketLifecycleRuleFields(request.Name, request.ExpireDays, request.ToInfrequentDays); err != nil {
		return ports.StorageBucketLifecycleRule{}, err
	}
	idemKey, err := requireIdempotencyKey(request.TenantID, request.IdempotencyKey)
	if err != nil {
		return ports.StorageBucketLifecycleRule{}, err
	}
	replayedName := strings.TrimSpace(request.Name)
	s.mu.RLock()
	replayID, hasReplay := s.bucketUpdateIdem[idemKey]
	var replayedRule *ports.StorageBucketLifecycleRule
	if hasReplay {
		if bucket, exists := s.buckets[replayID]; exists {
			for _, rule := range bucket.LifecycleRules {
				if rule.Name == replayedName {
					item := rule
					replayedRule = &item
					break
				}
			}
		}
	}
	s.mu.RUnlock()
	if replayedRule != nil {
		return *replayedRule, nil
	}
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageBucketLifecycleRule{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rule := ports.StorageBucketLifecycleRule{
		ID:               uuid.NewString(),
		Name:             strings.TrimSpace(request.Name),
		Prefix:           strings.TrimSpace(request.Prefix),
		ExpireDays:       request.ExpireDays,
		ToInfrequentDays: request.ToInfrequentDays,
		Enabled:          request.Enabled,
	}
	bucket.LifecycleRules = append(bucket.LifecycleRules, rule)
	bucket.LifecycleNote = storageBucketLifecycleNote(len(bucket.LifecycleRules))
	bucket.UpdatedAt = s.now().UTC()
	s.buckets[bucket.BucketID] = bucket
	s.bucketUpdateIdem[idemKey] = bucket.BucketID
	return rule, nil
}

func (s *LocalStorageService) DeleteStorageBucketLifecycleRule(ctx context.Context, request ports.StorageBucketLifecycleRuleDeleteRequest) (ports.StorageBucketLifecycleRuleListResult, error) {
	bucket, ok := s.resolveBucket(ctx, request.TenantID, request.BucketID)
	if !ok {
		return ports.StorageBucketLifecycleRuleListResult{}, fmt.Errorf("%w: bucket %s not found", ports.ErrNotFound, strings.TrimSpace(request.BucketID))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ruleID := strings.TrimSpace(request.RuleID)
	kept := make([]ports.StorageBucketLifecycleRule, 0, len(bucket.LifecycleRules))
	found := false
	for _, rule := range bucket.LifecycleRules {
		if rule.ID == ruleID {
			found = true
			continue
		}
		kept = append(kept, rule)
	}
	if !found {
		return ports.StorageBucketLifecycleRuleListResult{}, ports.ErrNotFound
	}
	bucket.LifecycleRules = kept
	bucket.LifecycleNote = storageBucketLifecycleNote(len(kept))
	bucket.UpdatedAt = s.now().UTC()
	s.buckets[bucket.BucketID] = bucket
	return ports.StorageBucketLifecycleRuleListResult{Items: append([]ports.StorageBucketLifecycleRule{}, kept...), Total: len(kept)}, nil
}

func (s *LocalStorageService) enrichStorageBucketRecord(bucket ports.StorageBucketRecord) ports.StorageBucketRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enrichStorageBucketLocked(bucket)
}

// enrichBucketUsage reflects live usage from the object store authority into
// bucket statistics so object_count/size_bytes match the S3-compatible
// backend instead of control-plane records alone. Lookup failures keep the
// control-plane stats and only log at debug level.
func (s *LocalStorageService) enrichBucketUsage(ctx context.Context, bucket *ports.StorageBucketRecord) {
	if s.objectStore == nil {
		return
	}
	usage, err := s.objectStore.BucketUsage(ctx, ports.BucketClass(bucket.Name), bucket.TenantID)
	if err != nil {
		slog.Debug("storage bucket usage lookup failed",
			"tenant_id", bucket.TenantID,
			"bucket", bucket.Name,
			"err", err,
		)
		return
	}
	bucket.ObjectCount = int(usage.ObjectCount)
	bucket.SizeBytes = usage.SizeBytes
}

func (s *LocalStorageService) enrichStorageBucketLocked(bucket ports.StorageBucketRecord) ports.StorageBucketRecord {
	if s.store == nil {
		bucket.ObjectCount = 0
		bucket.SizeBytes = 0
		for _, object := range s.objects {
			if object.TenantID == bucket.TenantID && object.Bucket == bucket.Name && object.State != ports.StorageResourceDeleted {
				bucket.ObjectCount++
				bucket.SizeBytes += object.SizeBytes
			}
		}
	}
	if bucket.ACL == "" {
		bucket.ACL = "private"
	}
	if bucket.ACLLabel == "" {
		bucket.ACLLabel = storageBucketACLLabel(bucket.ACL)
	}
	if bucket.StorageClass == "" {
		bucket.StorageClass = "standard"
	}
	if bucket.Versioning == "" {
		bucket.Versioning = "disabled"
	}
	if bucket.Region == "" {
		bucket.Region = "cn-east-1"
	}
	if bucket.Endpoint == "" {
		bucket.Endpoint = storageBucketEndpoint(bucket.Region)
	}
	if bucket.LifecycleRules == nil {
		bucket.LifecycleRules = []ports.StorageBucketLifecycleRule{}
	}
	if bucket.LifecycleNote == "" {
		bucket.LifecycleNote = storageBucketLifecycleNote(len(bucket.LifecycleRules))
	}
	if bucket.UpdatedAt.IsZero() {
		bucket.UpdatedAt = bucket.CreatedAt
	}
	return bucket
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
		if existing, err := s.store.FindVolumeSnapshotByCreateIdempotency(ctx, request.TenantID, request.IdempotencyKey); err == nil {
			return existing, nil
		} else if !errors.Is(err, ports.ErrNotFound) {
			return ports.VolumeSnapshotRecord{}, err
		}
	}
	s.mu.Lock()
	if id, ok := s.snapshotIdem[idemKey]; ok {
		if record, exists := s.snapshots[id]; exists {
			s.mu.Unlock()
			return record, nil
		}
	}
	volume, ok := s.volumes[strings.TrimSpace(request.VolumeID)]
	s.mu.Unlock()
	if (!ok || volume.TenantID != request.TenantID || volume.State == ports.StorageResourceDeleted) && s.store != nil {
		loaded, err := s.store.GetVolume(ctx, request.TenantID, strings.TrimSpace(request.VolumeID))
		if err != nil {
			return ports.VolumeSnapshotRecord{}, fmt.Errorf("%w: volume not found", ports.ErrNotFound)
		}
		volume = loaded
		ok = true
	}
	if !ok || volume.TenantID != request.TenantID || volume.State == ports.StorageResourceDeleted {
		return ports.VolumeSnapshotRecord{}, fmt.Errorf("%w: volume not found", ports.ErrNotFound)
	}
	now := s.now().UTC()
	providerConfigured := s.storageProviderConfigured()
	record := ports.VolumeSnapshotRecord{
		TenantID:                 request.TenantID,
		SnapshotID:               "snap_" + uuid.NewString(),
		VolumeID:                 volume.VolumeID,
		Name:                     strings.TrimSpace(request.Name),
		Description:              strings.TrimSpace(request.Description),
		Status:                   ports.VolumeSnapshotAvailable,
		SizeBytes:                volume.SizeGiB * 1024 * 1024 * 1024,
		CreatedAt:                now,
		UpdatedAt:                now,
		CreateIdempotencyKey:     request.IdempotencyKey,
		CreateRequestFingerprint: strings.Join([]string{volume.VolumeID, strings.TrimSpace(request.Name)}, "|"),
	}
	if providerConfigured {
		record.Status = ports.VolumeSnapshotCreating
	}
	if err := s.upsertVolumeSnapshot(ctx, record); err != nil {
		return ports.VolumeSnapshotRecord{}, err
	}
	if providerConfigured {
		observation, err := s.executeStorageProvider(ctx, "volume_snapshot", record.SnapshotID, func() ([]ports.WorkloadManifest, error) {
			return s.providerRenderer.RenderVolumeSnapshot(ctx, record)
		})
		if err != nil {
			record.Status = ports.VolumeSnapshotError
			record.UpdatedAt = s.now().UTC()
			_ = s.upsertVolumeSnapshot(ctx, record)
			return ports.VolumeSnapshotRecord{}, err
		}
		record.Status = volumeSnapshotStatusFromStorageState(observation.State)
		if !observation.ObservedAt.IsZero() {
			record.CreatedAt = observation.ObservedAt
			record.UpdatedAt = observation.ObservedAt
		}
		if err := s.upsertVolumeSnapshot(ctx, record); err != nil {
			return ports.VolumeSnapshotRecord{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[record.SnapshotID] = record
	s.snapshotIdem[idemKey] = record.SnapshotID
	return record, nil
}

func (s *LocalStorageService) ListVolumeSnapshots(ctx context.Context, request ports.VolumeSnapshotListRequest) ([]ports.VolumeSnapshotRecord, error) {
	if s.store != nil {
		if _, err := s.store.GetVolume(ctx, request.TenantID, strings.TrimSpace(request.VolumeID)); err != nil {
			return nil, ports.ErrNotFound
		}
		items, err := s.store.ListVolumeSnapshots(ctx, request.TenantID, strings.TrimSpace(request.VolumeID))
		if err != nil {
			return nil, err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
		return items, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	volume, ok := s.volumes[strings.TrimSpace(request.VolumeID)]
	if !ok || volume.TenantID != request.TenantID || volume.State == ports.StorageResourceDeleted {
		return nil, ports.ErrNotFound
	}
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
	if s.store != nil {
		if _, err := s.store.GetFilesystem(ctx, request.TenantID, strings.TrimSpace(request.FilesystemID)); err != nil {
			return nil, ports.ErrNotFound
		}
		items, err := s.store.ListFilesystemMountTargets(ctx, request.TenantID, strings.TrimSpace(request.FilesystemID))
		if err != nil {
			return nil, err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
		return items, nil
	}
	s.mu.Lock()
	filesystem, ok := s.filesystems[strings.TrimSpace(request.FilesystemID)]
	if !ok || filesystem.TenantID != request.TenantID || filesystem.State == ports.StorageResourceDeleted {
		s.mu.Unlock()
		return nil, ports.ErrNotFound
	}
	items := make([]ports.FilesystemMountTargetRecord, 0)
	for _, target := range s.mountTargets {
		if target.FilesystemID == filesystem.FilesystemID {
			items = append(items, target)
		}
	}
	if len(items) > 0 {
		s.mu.Unlock()
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
		return items, nil
	}
	target := ports.FilesystemMountTargetRecord{
		TenantID:      filesystem.TenantID,
		MountTargetID: "mt_" + uuid.NewString(),
		FilesystemID:  filesystem.FilesystemID,
		SubnetID:      "local-subnet",
		VPCID:         "local-vpc",
		IPAddress:     "127.0.0.1",
		Status:        ports.MountTargetAvailable,
		CreatedAt:     s.now().UTC(),
	}
	s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mountTargets[target.MountTargetID] = target
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
	if err := s.store.UpsertObject(ctx, record); err != nil {
		slog.Warn("storage object control-plane persist failed",
			"tenant_id", record.TenantID,
			"object_id", record.ObjectID,
			"bucket", record.Bucket,
			"state", string(record.State),
			"err", err,
		)
		return err
	}
	return nil
}

func (s *LocalStorageService) upsertBucket(ctx context.Context, record ports.StorageBucketRecord) error {
	if s.store == nil {
		return nil
	}
	if err := s.store.UpsertBucket(ctx, record); err != nil {
		slog.Warn("storage bucket control-plane persist failed",
			"tenant_id", record.TenantID,
			"bucket_id", record.BucketID,
			"name", record.Name,
			"state", string(record.State),
			"err", err,
		)
		return err
	}
	return nil
}

func (s *LocalStorageService) upsertVolumeSnapshot(ctx context.Context, record ports.VolumeSnapshotRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertVolumeSnapshot(ctx, record)
}

func (s *LocalStorageService) upsertFilesystemMountTarget(ctx context.Context, record ports.FilesystemMountTargetRecord) error {
	if s.store == nil {
		return nil
	}
	return s.store.UpsertFilesystemMountTarget(ctx, record)
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

func validateStorageBucketLifecycleRuleFields(name string, expireDays int, toInfrequentDays int) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: lifecycle rule name is required", ports.ErrInvalid)
	}
	if expireDays < 1 || toInfrequentDays < 1 {
		return fmt.Errorf("%w: expire_days and to_infrequent_days must be >= 1", ports.ErrInvalid)
	}
	return nil
}

func storageVolumeIOPS(volumeType string) int {
	switch strings.ToLower(strings.TrimSpace(volumeType)) {
	case "hdd":
		return 1000
	case "high_performance_ssd":
		return 20000
	default:
		return 5000
	}
}

func storageVolumeInitialOSStatus(mountInstanceID string) string {
	if strings.TrimSpace(mountInstanceID) == "" {
		return "n_a"
	}
	return "pending"
}

func storageVolumeHistory(at time.Time, action string, result string, target string) ports.StorageVolumeMountHistoryEntry {
	return ports.StorageVolumeMountHistoryEntry{
		At:     at,
		Action: action,
		Result: result,
		Target: target,
	}
}

func storageOperationIdempotencyKey(idempotencyKey string, operation string) string {
	return operation + ":" + idempotencyKey
}

func (s *LocalStorageService) acquireStorageIdempotency(ctx context.Context, key string) (func(), error) {
	for {
		s.idempotencyMu.Lock()
		if flight, ok := s.idempotencyFlight[key]; ok {
			s.idempotencyMu.Unlock()
			select {
			case <-flight:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		flight := make(chan struct{})
		s.idempotencyFlight[key] = flight
		s.idempotencyMu.Unlock()
		return func() {
			s.idempotencyMu.Lock()
			delete(s.idempotencyFlight, key)
			close(flight)
			s.idempotencyMu.Unlock()
		}, nil
	}
}

func storageFilesystemMountIP(offset int) string {
	if offset < 1 {
		offset = 10
	}
	if offset > 250 {
		offset = 250
	}
	return fmt.Sprintf("10.0.0.%d", offset)
}

func storageFilesystemMountCommand(record ports.StorageFilesystemRecord, ipAddress string, mountPath string) ports.FilesystemMountCommand {
	ipAddress = firstNetworkNonEmpty(ipAddress, "127.0.0.1")
	mountPath = firstNetworkNonEmpty(mountPath, "/mnt/"+record.Name)
	command := "mount -t nfs " + ipAddress + ":/" + record.Name + " " + mountPath
	if record.Protocol == "cephfs" {
		command = "mount -t ceph " + ipAddress + ":/" + record.Name + " " + mountPath
	}
	return ports.FilesystemMountCommand{
		Command:   command,
		Protocol:  record.Protocol,
		IPAddress: ipAddress,
		MountPath: mountPath,
	}
}

func replaceFilesystemAttachment(items []ports.FilesystemAttachment, next ports.FilesystemAttachment) []ports.FilesystemAttachment {
	result := make([]ports.FilesystemAttachment, 0, len(items))
	for _, item := range items {
		if item.InstanceID != next.InstanceID {
			result = append(result, item)
		}
	}
	return result
}

func (s *LocalStorageService) enrichStorageVolumeRecord(record ports.StorageVolumeRecord) ports.StorageVolumeRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enrichStorageVolumeLocked(record)
}

func (s *LocalStorageService) enrichStorageVolumeLocked(record ports.StorageVolumeRecord) ports.StorageVolumeRecord {
	if record.SnapshotsCount == 0 {
		count := 0
		for _, snapshot := range s.snapshots {
			if snapshot.TenantID == record.TenantID && snapshot.VolumeID == record.VolumeID {
				count++
			}
		}
		record.SnapshotsCount = count
	}
	return record
}

func storageVolumeCreateFingerprint(request ports.StorageVolumeCreateRequest) string {
	return strings.Join([]string{
		strings.TrimSpace(request.Name),
		strconv.FormatInt(request.SizeGiB, 10),
		strings.TrimSpace(request.StorageClass),
		strings.TrimSpace(request.Zone),
		strings.TrimSpace(request.VolumeType),
		strconv.FormatBool(request.Encrypted),
		strings.TrimSpace(request.MountInstanceID),
		strings.TrimSpace(request.MountRoute),
	}, "|")
}

func (s *LocalStorageService) enrichFilesystemLocked(record ports.StorageFilesystemRecord) ports.StorageFilesystemRecord {
	targets := make([]ports.FilesystemMountTargetRecord, 0)
	for _, target := range s.mountTargets {
		if target.FilesystemID == record.FilesystemID {
			targets = append(targets, target)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].CreatedAt.After(targets[j].CreatedAt) })
	record.MountTargets = targets
	record.Mounts = len(record.AttachedInstances)
	if record.PerformanceMode == "" {
		record.PerformanceMode = "standard"
	}
	if record.MountCommand == "" {
		record.MountCommand = storageFilesystemMountCommand(record, "127.0.0.1", "/mnt/"+record.Name).Command
	}
	return record
}

func storageBucketEndpoint(region string) string {
	region = firstNetworkNonEmpty(strings.TrimSpace(region), "cn-east-1")
	return "https://s3." + region + ".ani.local"
}

func storageBucketACLLabel(acl string) string {
	switch acl {
	case "tenant_read":
		return "租户内读"
	default:
		return "私有"
	}
}

func storageBucketLifecycleNote(count int) string {
	if count <= 0 {
		return "未配置生命周期规则"
	}
	return fmt.Sprintf("已配置 %d 条规则（可编辑）", count)
}

func storageBucketPrefixMapKey(tenantID, bucketID string) string {
	return tenantID + "/" + bucketID
}

func storageBucketKeyUnderPrefix(key, prefix string) bool {
	if prefix == "" {
		return true
	}
	return strings.HasPrefix(key, prefix)
}

func storageBucketEntryName(key string) string {
	trimmed := strings.TrimSuffix(key, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[idx+1:] + "/"
	}
	if strings.HasSuffix(key, "/") {
		return trimmed + "/"
	}
	return key
}

func storageSizeLabel(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1fKiB", float64(size)/1024)
	}
	if size < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMiB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.1fGiB", float64(size)/(1024*1024*1024))
}

var _ ports.StorageService = (*LocalStorageService)(nil)
