package runtime

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// consumerStoreFixture is a kind-aware WorkloadInstanceStore fake that
// returns records bucketed by workload kind.
type consumerStoreFixture struct {
	byKind map[ports.WorkloadKind][]ports.WorkloadInstanceRecord
}

func (s *consumerStoreFixture) UpsertStatus(context.Context, ports.WorkloadInstanceRecord) error {
	return nil
}

func (s *consumerStoreFixture) Get(_ context.Context, _ string, instanceID string) (ports.WorkloadInstanceRecord, error) {
	for _, records := range s.byKind {
		for _, record := range records {
			if record.InstanceID == instanceID {
				return record, nil
			}
		}
	}
	return ports.WorkloadInstanceRecord{}, ports.ErrNotFound
}

func (s *consumerStoreFixture) List(_ context.Context, _ string, kind ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	return append([]ports.WorkloadInstanceRecord(nil), s.byKind[kind]...), nil
}

var _ ports.WorkloadInstanceStore = (*consumerStoreFixture)(nil)

func consumerRecord(instanceID, name string, kind ports.WorkloadKind, state ports.WorkloadState, attachments ...ports.WorkloadStorageAttachment) ports.WorkloadInstanceRecord {
	return ports.WorkloadInstanceRecord{
		TenantID:           "tenant-a",
		InstanceID:         instanceID,
		Name:               name,
		Kind:               kind,
		StorageAttachments: attachments,
		Status:             ports.WorkloadStatus{State: state},
	}
}

func volumeAttachment(volumeID, mountPath string) ports.WorkloadStorageAttachment {
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

func filesystemAttachment(filesystemID, mountPath string) ports.WorkloadStorageAttachment {
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

func TestListStorageConsumersVolumeOccupiedByRunning(t *testing.T) {
	store := &consumerStoreFixture{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
		ports.WorkloadKindContainer: {
			consumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, ports.WorkloadStateRunning,
				volumeAttachment("vol-1", "/data")),
		},
	}}

	consumers, err := ListStorageConsumers(context.Background(), store, "tenant-a", "volume", "vol-1")
	if err != nil {
		t.Fatalf("ListStorageConsumers: %v", err)
	}
	if len(consumers) != 1 {
		t.Fatalf("expected 1 consumer, got %d", len(consumers))
	}
	got := consumers[0]
	if got.InstanceID != "inst-1" || got.InstanceName != "app-1" || got.MountPath != "/data" {
		t.Fatalf("unexpected consumer: %+v", got)
	}
	if got.Kind != ports.WorkloadKindContainer || got.State != ports.WorkloadStateRunning {
		t.Fatalf("unexpected kind/state: %+v", got)
	}
}

func TestListStorageConsumersStoppedInstanceReleasesVolume(t *testing.T) {
	store := &consumerStoreFixture{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
		ports.WorkloadKindContainer: {
			consumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, ports.WorkloadStateStopped,
				volumeAttachment("vol-1", "/data")),
			consumerRecord("inst-2", "app-2", ports.WorkloadKindContainer, ports.WorkloadStateDeleted,
				volumeAttachment("vol-1", "/data")),
		},
	}}

	consumers, err := ListStorageConsumers(context.Background(), store, "tenant-a", "volume", "vol-1")
	if err != nil {
		t.Fatalf("ListStorageConsumers: %v", err)
	}
	if len(consumers) != 0 {
		t.Fatalf("expected 0 consumers for stopped/deleted instances, got %d", len(consumers))
	}
}

func TestListStorageConsumersFilesystemSharedByRunningInstances(t *testing.T) {
	store := &consumerStoreFixture{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
		ports.WorkloadKindContainer: {
			consumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, ports.WorkloadStateRunning,
				filesystemAttachment("fs-1", "/data1")),
		},
		ports.WorkloadKindVM: {
			consumerRecord("inst-2", "vm-1", ports.WorkloadKindVM, ports.WorkloadStateRunning,
				filesystemAttachment("fs-1", "/data2")),
		},
	}}

	consumers, err := ListStorageConsumers(context.Background(), store, "tenant-a", "filesystem", "fs-1")
	if err != nil {
		t.Fatalf("ListStorageConsumers: %v", err)
	}
	if len(consumers) != 2 {
		t.Fatalf("expected 2 consumers across kinds, got %d", len(consumers))
	}
	if consumers[0].InstanceID == consumers[1].InstanceID {
		t.Fatalf("expected distinct consumers, got duplicates: %+v", consumers)
	}
}

func TestListStorageConsumersActiveTransientsHoldAttachments(t *testing.T) {
	activeStates := []ports.WorkloadState{
		ports.WorkloadStatePending,
		ports.WorkloadStateProvisioning,
		ports.WorkloadStateStarting,
		ports.WorkloadStateRunning,
		ports.WorkloadStateStopping,
	}
	for _, state := range activeStates {
		store := &consumerStoreFixture{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
			ports.WorkloadKindContainer: {
				consumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, state,
					volumeAttachment("vol-1", "/data")),
			},
		}}
		consumers, err := ListStorageConsumers(context.Background(), store, "tenant-a", "volume", "vol-1")
		if err != nil {
			t.Fatalf("state %s: ListStorageConsumers: %v", state, err)
		}
		if len(consumers) != 1 {
			t.Fatalf("state %s: expected attachment to hold, got %d consumers", state, len(consumers))
		}
	}

	releasedStates := []ports.WorkloadState{
		ports.WorkloadStateStopped,
		ports.WorkloadStateFailed,
		ports.WorkloadStateDeleting,
		ports.WorkloadStateDeleted,
	}
	for _, state := range releasedStates {
		store := &consumerStoreFixture{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
			ports.WorkloadKindContainer: {
				consumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, state,
					volumeAttachment("vol-1", "/data")),
			},
		}}
		consumers, err := ListStorageConsumers(context.Background(), store, "tenant-a", "volume", "vol-1")
		if err != nil {
			t.Fatalf("state %s: ListStorageConsumers: %v", state, err)
		}
		if len(consumers) != 0 {
			t.Fatalf("state %s: expected released, got %d consumers", state, len(consumers))
		}
	}
}

func TestListAllStorageConsumersIndexAndDedup(t *testing.T) {
	store := &consumerStoreFixture{byKind: map[ports.WorkloadKind][]ports.WorkloadInstanceRecord{
		ports.WorkloadKindContainer: {
			// same volume mounted twice with different paths -> one consumer row
			consumerRecord("inst-1", "app-1", ports.WorkloadKindContainer, ports.WorkloadStateRunning,
				volumeAttachment("vol-1", "/data"), volumeAttachment("vol-1", "/data2")),
			consumerRecord("inst-2", "idle-2", ports.WorkloadKindContainer, ports.WorkloadStateStopped,
				volumeAttachment("vol-2", "/data")),
		},
		ports.WorkloadKindGPUContainer: {
			consumerRecord("inst-3", "gpu-3", ports.WorkloadKindGPUContainer, ports.WorkloadStateProvisioning,
				volumeAttachment("vol-1", "/data"), filesystemAttachment("fs-1", "/share")),
		},
	}}

	index, err := ListAllStorageConsumers(context.Background(), store, "tenant-a")
	if err != nil {
		t.Fatalf("ListAllStorageConsumers: %v", err)
	}
	if len(index["volume/vol-1"]) != 2 {
		t.Fatalf("expected 2 consumers for volume/vol-1, got %d", len(index["volume/vol-1"]))
	}
	if len(index["filesystem/fs-1"]) != 1 {
		t.Fatalf("expected 1 consumer for filesystem/fs-1, got %d", len(index["filesystem/fs-1"]))
	}
	if _, ok := index["volume/vol-2"]; ok {
		t.Fatalf("stopped instance must not contribute consumers")
	}
	if _, ok := index["volume/"]; ok {
		t.Fatalf("blank resource id must be skipped")
	}
}

func TestListStorageConsumersNilStoreAndEmptyResourceID(t *testing.T) {
	consumers, err := ListStorageConsumers(context.Background(), nil, "tenant-a", "volume", "vol-1")
	if err != nil {
		t.Fatalf("nil store: %v", err)
	}
	if consumers != nil {
		t.Fatalf("nil store: expected nil consumers, got %+v", consumers)
	}

	store := &consumerStoreFixture{}
	consumers, err = ListStorageConsumers(context.Background(), store, "tenant-a", "volume", "")
	if err != nil {
		t.Fatalf("empty resource id: %v", err)
	}
	if len(consumers) != 0 {
		t.Fatalf("empty resource id: expected 0 consumers, got %d", len(consumers))
	}
}
