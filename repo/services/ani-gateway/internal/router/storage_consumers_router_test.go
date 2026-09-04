package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// storageConsumerStoreFake is a kind-aware WorkloadInstanceStore fake used to
// drive storage occupancy tagging in router tests.
type storageConsumerStoreFake struct {
	byKind map[ports.WorkloadKind][]ports.WorkloadInstanceRecord
}

func (s *storageConsumerStoreFake) UpsertStatus(context.Context, ports.WorkloadInstanceRecord) error {
	return nil
}

func (s *storageConsumerStoreFake) Get(context.Context, string, string) (ports.WorkloadInstanceRecord, error) {
	return ports.WorkloadInstanceRecord{}, ports.ErrNotFound
}

func (s *storageConsumerStoreFake) List(_ context.Context, _ string, kind ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	return append([]ports.WorkloadInstanceRecord(nil), s.byKind[kind]...), nil
}

var _ ports.WorkloadInstanceStore = (*storageConsumerStoreFake)(nil)

func storageConsumerRecord(instanceID, name string, kind ports.WorkloadKind, state ports.WorkloadState, attachments ...ports.WorkloadStorageAttachment) ports.WorkloadInstanceRecord {
	return ports.WorkloadInstanceRecord{
		TenantID:           "tenant-a",
		InstanceID:         instanceID,
		Name:               name,
		Kind:               kind,
		StorageAttachments: attachments,
		Status:             ports.WorkloadStatus{State: state},
	}
}

func storageVolumeAttach(volumeID, mountPath string) ports.WorkloadStorageAttachment {
	return ports.WorkloadStorageAttachment{
		Name:         "mount-" + volumeID,
		Kind:         ports.StorageAttachmentSharedPVC,
		ResourceType: "volume",
		ResourceID:   volumeID,
		MountPath:    mountPath,
		Required:     true,
		Status:       "resolved",
	}
}

func storageFilesystemAttach(filesystemID, mountPath string) ports.WorkloadStorageAttachment {
	return ports.WorkloadStorageAttachment{
		Name:         "mount-" + filesystemID,
		Kind:         ports.StorageAttachmentSharedPVC,
		ResourceType: "filesystem",
		ResourceID:   filesystemID,
		MountPath:    mountPath,
		Required:     true,
		Status:       "resolved",
	}
}

// storeBuilder lets each test build its instance store fixture from the real
// generated volume/filesystem IDs.
type storeBuilder func(volumes, filesystems map[string]string) ports.WorkloadInstanceStore

func setupStorageConsumerTestServer(t *testing.T, buildStore storeBuilder) (*server.Hertz, map[string]string, map[string]string) {
	t.Helper()
	service := runtimeadapter.NewLocalStorageService()
	createdVolumes := map[string]string{}
	createdFilesystems := map[string]string{}
	for i, name := range []string{"vol-busy", "vol-free", "vol-spare"} {
		volume, err := service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
			TenantID:       "tenant-a",
			IdempotencyKey: fmt.Sprintf("usage-volume-%d", i),
			Name:           name,
			SizeGiB:        10,
		})
		if err != nil {
			t.Fatalf("CreateVolume(%s) error = %v", name, err)
		}
		createdVolumes[name] = volume.VolumeID
	}
	filesystem, err := service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "usage-fs-0",
		Name:           "fs-shared",
		Protocol:       "nfs",
		SizeGiB:        100,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem error = %v", err)
	}
	createdFilesystems["fs-shared"] = filesystem.FilesystemID

	var instanceStore ports.WorkloadInstanceStore
	if buildStore != nil {
		instanceStore = buildStore(createdVolumes, createdFilesystems)
	}

	h := server.Default()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		tenantID := string(c.GetHeader("X-Dev-Tenant-ID"))
		if tenantID == "" {
			tenantID = "tenant-a"
		}
		c.Set("tenant_id", tenantID)
		c.Next(ctx)
	})
	v1 := h.Group("/api/v1")
	registerStorageResourcesWithServiceAndTasksAndStore(v1, service, defaultTaskStore, instanceStore)
	return h, createdVolumes, createdFilesystems
}

func decodeStorageVolumeList(t *testing.T, body []byte) []storageVolumeResponse {
	t.Helper()
	var payload struct {
		Items []storageVolumeResponse `json:"items"`
		Total int                     `json:"total"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode volume list = %v", err)
	}
	return payload.Items
}

func TestStorageVolumeListTagsOccupancy(t *testing.T) {
	store := &storageConsumerStoreFake{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
		ports.WorkloadKindContainer: {
			storageConsumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, ports.WorkloadStateRunning,
				storageVolumeAttach("vol-busy", "/data")),
			storageConsumerRecord("inst-2", "app-2", ports.WorkloadKindContainer, ports.WorkloadStateStopped,
				storageVolumeAttach("vol-free", "/data")),
		},
	}}
	h, volumes, _ := setupStorageConsumerTestServer(t, func(volumes, _ map[string]string) ports.WorkloadInstanceStore {
		// remap fixture IDs to the real generated volume IDs
		remapped := &storageConsumerStoreFake{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{}}
		for kind, records := range store.byKind {
			for _, record := range records {
				for i, attachment := range record.StorageAttachments {
					if realID, ok := volumes[attachment.ResourceID]; ok {
						record.StorageAttachments[i] = storageVolumeAttach(realID, attachment.MountPath)
					}
				}
				remapped.byKind[kind] = append(remapped.byKind[kind], record)
			}
		}
		return remapped
	})

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/volumes", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("GET /volumes status = %d, want 200", resp.StatusCode())
	}
	items := decodeStorageVolumeList(t, resp.Body())
	if len(items) != 3 {
		t.Fatalf("expected 3 volumes, got %d", len(items))
	}
	byID := map[string]storageVolumeResponse{}
	for _, item := range items {
		byID[item.ID] = item
	}
	busy := byID[volumes["vol-busy"]]
	if !busy.InUse || len(busy.UsedBy) != 1 || busy.UsedBy[0].InstanceID != "inst-1" || busy.UsedBy[0].MountPath != "/data" {
		t.Fatalf("busy volume = %+v, want in_use with inst-1 /data", busy)
	}
	if got := byID[volumes["vol-free"]]; got.InUse {
		t.Fatalf("volume held by stopped instance must be in_use=false, got %+v", got)
	}
	if got := byID[volumes["vol-spare"]]; got.InUse || got.UsedBy == nil || len(got.UsedBy) != 0 {
		t.Fatalf("spare volume = %+v, want in_use=false used_by=[]", got)
	}
}

func TestStorageVolumeListInUseFilter(t *testing.T) {
	h, volumes, _ := setupStorageConsumerTestServer(t, func(volumes, _ map[string]string) ports.WorkloadInstanceStore {
		return &storageConsumerStoreFake{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
			ports.WorkloadKindContainer: {
				storageConsumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, ports.WorkloadStateRunning,
					storageVolumeAttach(volumes["vol-busy"], "/data")),
			},
		}}
	})

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/volumes?in_use=false", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	items := decodeStorageVolumeList(t, resp.Body())
	if len(items) != 2 {
		t.Fatalf("in_use=false: expected 2 volumes, got %d", len(items))
	}
	for _, item := range items {
		if item.ID == volumes["vol-busy"] {
			t.Fatalf("in_use=false must exclude the occupied volume, got %+v", item)
		}
	}

	resp = ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/volumes?in_use=true", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	items = decodeStorageVolumeList(t, resp.Body())
	if len(items) != 1 || items[0].ID != volumes["vol-busy"] {
		t.Fatalf("in_use=true: expected only the busy volume, got %+v", items)
	}

	resp = ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/volumes?in_use=bogus", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("in_use=bogus status = %d, want 400", resp.StatusCode())
	}
}

func TestStorageVolumeDetailTagsOccupancy(t *testing.T) {
	h, volumes, _ := setupStorageConsumerTestServer(t, func(volumes, _ map[string]string) ports.WorkloadInstanceStore {
		return &storageConsumerStoreFake{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
			ports.WorkloadKindContainer: {
				storageConsumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, ports.WorkloadStateRunning,
					storageVolumeAttach(volumes["vol-busy"], "/data")),
			},
		}}
	})

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/volumes/"+volumes["vol-busy"], nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("GET volume status = %d, want 200", resp.StatusCode())
	}
	var detail storageVolumeResponse
	if err := json.Unmarshal(resp.Body(), &detail); err != nil {
		t.Fatalf("decode volume detail = %v", err)
	}
	if !detail.InUse || len(detail.UsedBy) != 1 || detail.UsedBy[0].InstanceName != "app-1" {
		t.Fatalf("volume detail = %+v, want in_use with app-1", detail)
	}
}

func TestStorageFilesystemListTagsSharedConsumers(t *testing.T) {
	h, _, filesystems := setupStorageConsumerTestServer(t, func(_, filesystems map[string]string) ports.WorkloadInstanceStore {
		return &storageConsumerStoreFake{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
			ports.WorkloadKindContainer: {
				storageConsumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, ports.WorkloadStateRunning,
					storageFilesystemAttach(filesystems["fs-shared"], "/data1")),
			},
			ports.WorkloadKindGPUContainer: {
				storageConsumerRecord("inst-2", "gpu-2", ports.WorkloadKindGPUContainer, ports.WorkloadStateProvisioning,
					storageFilesystemAttach(filesystems["fs-shared"], "/data2")),
			},
		}}
	})

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/filesystems?in_use=true", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("GET /filesystems?in_use=true status = %d, want 200", resp.StatusCode())
	}
	var payload struct {
		Items []storageFilesystemResponse `json:"items"`
	}
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		t.Fatalf("decode filesystem list = %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ID != filesystems["fs-shared"] {
		t.Fatalf("filesystems = %+v, want only fs-shared", payload.Items)
	}
	usedBy := payload.Items[0].UsedBy
	if len(usedBy) != 2 {
		t.Fatalf("expected 2 shared consumers, got %+v", usedBy)
	}
	if usedBy[0].Kind != "container" || usedBy[1].Kind != "gpu_container" {
		t.Fatalf("consumers = %+v, want container + gpu_container", usedBy)
	}
}

func TestStorageOccupancyNilStoreStaysUnused(t *testing.T) {
	h, _, _ := setupStorageConsumerTestServer(t, nil)

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/volumes", nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	items := decodeStorageVolumeList(t, resp.Body())
	if len(items) == 0 {
		t.Fatalf("expected volumes without store, got none")
	}
	for _, item := range items {
		if item.InUse || item.UsedBy == nil || len(item.UsedBy) != 0 {
			t.Fatalf("nil store volume = %+v, want in_use=false used_by=[]", item)
		}
	}
}
