package router

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestStorageAPIDevProfileVolumeFilesystemAndObject(t *testing.T) {
	api := newStorageAPI()
	volume, err := api.service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-volume-a",
		Name:           "data-a",
		SizeGiB:        100,
		StorageClass:   "fast",
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	if got := storageVolumeFromRecord(volume); got.ID == "" || got.State != "available" || got.TenantID != "tenant-a" {
		t.Fatalf("volume response = %+v, want available tenant-a volume", got)
	} else {
		requireLocalCoreDevProfile(t, got.DevProfile, "local-storage-service")
	}
	filesystem, err := api.service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-fs-a",
		Name:           "shared",
		Protocol:       "nfs",
		SizeGiB:        500,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem error = %v", err)
	}
	if got := storageFilesystemFromRecord(filesystem); got.ID == "" || got.Protocol != "nfs" || got.Endpoint == "" {
		t.Fatalf("filesystem response = %+v, want nfs endpoint", got)
	} else {
		requireLocalCoreDevProfile(t, got.DevProfile, "local-storage-service")
	}
	object, err := api.service.CreateObject(context.Background(), ports.StorageObjectCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-object-a",
		Bucket:         "models",
		Key:            "llm/model.bin",
		SizeBytes:      1024,
		ContentType:    "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("CreateObject error = %v", err)
	}
	if got := storageObjectFromRecord(object); got.ID == "" || got.Bucket != "models" || got.State != "available" {
		t.Fatalf("object response = %+v, want object metadata", got)
	} else {
		requireLocalCoreDevProfile(t, got.DevProfile, "local-storage-service")
	}
}

func TestStorageAPIServiceKeepsTenantIsolation(t *testing.T) {
	api := newStorageAPI()
	volume, err := api.service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-volume-b",
		Name:           "tenant-a-volume",
		SizeGiB:        10,
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	if _, err := api.service.GetVolume(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   "tenant-b",
		ResourceID: volume.VolumeID,
	}); err == nil {
		t.Fatalf("GetVolume from another tenant succeeded, want isolation error")
	}
}

func TestStorageAPIDevProfileSnapshotAndMountTarget(t *testing.T) {
	api := newStorageAPI()
	volume, err := api.service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-snapshot-volume-a",
		Name:           "db-data",
		SizeGiB:        16,
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	snapshot, err := api.service.CreateVolumeSnapshot(context.Background(), ports.VolumeSnapshotCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-snapshot-a",
		VolumeID:       volume.VolumeID,
		Name:           "db-data-snap",
	})
	if err != nil {
		t.Fatalf("CreateVolumeSnapshot error = %v", err)
	}
	if got := storageSnapshotFromRecord(snapshot); got.ID == "" || got.VolumeID != volume.VolumeID || got.Status != "available" || got.SizeBytes <= 0 {
		t.Fatalf("snapshot response = %+v, want available snapshot", got)
	}
	task := storageSnapshotTaskFromRecord(snapshot, "api-snapshot-a", "00000000-0000-0000-0000-000000000123")
	if task.TaskType != "volume.snapshot.create" || task.ResourceType != "volume_snapshot" || task.Status != "completed" || task.ProgressPct != 100 {
		t.Fatalf("snapshot task = %+v, want completed volume snapshot task", task)
	}
	taskSnapshot, ok := task.Result["snapshot"].(storageSnapshotResponse)
	if !ok || taskSnapshot.ID != snapshot.SnapshotID || taskSnapshot.VolumeID != volume.VolumeID {
		t.Fatalf("snapshot task result = %+v, want embedded snapshot response", task.Result)
	}
	filesystem, err := api.service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-mount-fs-a",
		Name:           "shared",
		SizeGiB:        64,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem error = %v", err)
	}
	targets, err := api.service.ListFilesystemMountTargets(context.Background(), ports.FilesystemMountTargetListRequest{
		TenantID:     "tenant-a",
		FilesystemID: filesystem.FilesystemID,
	})
	if err != nil {
		t.Fatalf("ListFilesystemMountTargets error = %v", err)
	}
	if got := storageMountTargetFromRecord(targets[0]); got.ID == "" || got.FilesystemID != filesystem.FilesystemID || got.Status != "available" || got.IPAddress == "" {
		t.Fatalf("mount target response = %+v, want available mount target", got)
	}
}
