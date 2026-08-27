package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const storageStoreTenantID = "5dbb1d01-0000-4000-8000-000000000002"

func TestMetadataStorageStoreUpsertsVolume(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataStorageStore(fakeMetadataStore{tx: tx}, WithStorageStoreClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	err := store.UpsertVolume(context.Background(), ports.StorageVolumeRecord{
		TenantID:     storageStoreTenantID,
		VolumeID:     "vol-test",
		Name:         "data",
		SizeGiB:      100,
		StorageClass: "fast",
		State:        ports.StorageResourceAvailable,
		Reason:       "created",
		CreatedAt:    time.Unix(90, 0),
	})
	if err != nil {
		t.Fatalf("UpsertVolume() error = %v", err)
	}
	if len(tx.execs) == 0 || !strings.Contains(tx.execs[0], "INSERT INTO storage_volumes") {
		t.Fatalf("execs[0] = %q, want storage_volumes insert", tx.execs)
	}
	if !strings.Contains(tx.execs[0], "create_idempotency_key") {
		t.Fatalf("sql = %q, want create_idempotency_key column", tx.execs[0])
	}
}

func TestMetadataStorageStoreUpsertsFilesystemAndObject(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataStorageStore(fakeMetadataStore{tx: tx}, WithStorageStoreClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	err := store.UpsertFilesystem(context.Background(), ports.StorageFilesystemRecord{
		TenantID:     storageStoreTenantID,
		FilesystemID: "fs-test",
		Name:         "shared",
		Protocol:     "nfs",
		SizeGiB:      500,
		Endpoint:     "local://shared",
		State:        ports.StorageResourceAvailable,
	})
	if err != nil {
		t.Fatalf("UpsertFilesystem() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO storage_filesystems") {
		t.Fatalf("sql = %q, want storage_filesystems insert", tx.sql)
	}

	err = store.UpsertObject(context.Background(), ports.StorageObjectRecord{
		TenantID:    storageStoreTenantID,
		ObjectID:    "obj-test",
		Bucket:      "models",
		Key:         "llm/model.bin",
		SizeBytes:   1024,
		ContentType: "application/octet-stream",
		State:       ports.StorageResourceAvailable,
	})
	if err != nil {
		t.Fatalf("UpsertObject() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO storage_objects") {
		t.Fatalf("sql = %q, want storage_objects insert", tx.sql)
	}
	if got, want := tx.args[3], "llm/model.bin"; got != want {
		t.Fatalf("object_key arg = %v, want %s", got, want)
	}
}

func TestLocalStorageServicePersistsCreateAndDelete(t *testing.T) {
	store := newSharedMemoryStorageStore()
	service := NewLocalStorageService(WithStorageResourceStore(store))

	volume, err := service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "persisted-volume",
		Name:           "persisted-volume",
		SizeGiB:        10,
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if _, err := service.DeleteVolume(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: volume.VolumeID,
	}); err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
	if _, err := store.GetVolume(context.Background(), storageStoreTenantID, volume.VolumeID); err != ports.ErrNotFound {
		t.Fatalf("store GetVolume after delete error = %v, want ErrNotFound", err)
	}
	if got := store.volumes[store.key(storageStoreTenantID, volume.VolumeID)].State; got != ports.StorageResourceDeleted {
		t.Fatalf("tombstone state = %s, want deleted", got)
	}

	filesystem, err := service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "persisted-filesystem",
		Name:           "persisted-filesystem",
		Protocol:       "nfs",
		SizeGiB:        10,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem() error = %v", err)
	}
	// Simulate Gateway restart: fresh service, same PG/memory store authority.
	restarted := NewLocalStorageService(WithStorageResourceStore(store))
	if _, err := restarted.DeleteFilesystem(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: filesystem.FilesystemID,
	}); err != nil {
		t.Fatalf("DeleteFilesystem after restart error = %v", err)
	}
	if _, err := store.GetFilesystem(context.Background(), storageStoreTenantID, filesystem.FilesystemID); err != ports.ErrNotFound {
		t.Fatalf("store GetFilesystem after delete error = %v, want ErrNotFound", err)
	}
}

func TestLocalStorageServiceBucketObjectOperationsAfterRestart(t *testing.T) {
	store := newSharedMemoryStorageStore()
	service := NewLocalStorageService(WithStorageResourceStore(store))

	bucket, err := service.CreateStorageBucket(context.Background(), ports.StorageBucketCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "persisted-bucket",
		Name:           "persisted-bucket",
	})
	if err != nil {
		t.Fatalf("CreateStorageBucket() error = %v", err)
	}

	// Upload and complete an object before the simulated restart so the
	// store authority holds a finished object record.
	upload, err := service.CreateStorageObjectUpload(context.Background(), ports.StorageObjectUploadRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "persisted-upload",
		BucketID:       bucket.BucketID,
		Key:            "persisted.csv",
		ContentType:    "text/csv",
	})
	if err != nil {
		t.Fatalf("CreateStorageObjectUpload() error = %v", err)
	}
	if _, err := service.CompleteStorageObject(context.Background(), ports.StorageObjectCompleteRequest{
		TenantID:       storageStoreTenantID,
		ObjectID:       upload.ObjectID,
		IdempotencyKey: "persisted-complete",
	}); err != nil {
		t.Fatalf("CompleteStorageObject() error = %v", err)
	}

	// Simulate Gateway restart: fresh service, same PG/memory store authority.
	restarted := NewLocalStorageService(WithStorageResourceStore(store))
	listed, err := restarted.ListBucketObjects(context.Background(), ports.StorageBucketObjectListRequest{
		TenantID: storageStoreTenantID,
		BucketID: bucket.BucketID,
		Prefix:   "/",
	})
	if err != nil {
		t.Fatalf("ListBucketObjects after restart error = %v", err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].Kind != "object" || listed.Items[0].Key != "persisted.csv" {
		t.Fatalf("ListBucketObjects after restart = %#v, want the persisted object listed", listed)
	}
	deleted, err := restarted.DeleteBucketObject(context.Background(), ports.StorageBucketObjectDeleteRequest{
		TenantID: storageStoreTenantID,
		BucketID: bucket.BucketID,
		Key:      "persisted.csv",
	})
	if err != nil || !deleted.Deleted {
		t.Fatalf("DeleteBucketObject after restart = %#v err=%v, want persisted object deletable", deleted, err)
	}
	if _, err := restarted.CreateBucketPrefix(context.Background(), ports.StorageBucketPrefixCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "persisted-bucket-prefix",
		BucketID:       bucket.BucketID,
		Prefix:         "models/",
	}); err != nil {
		t.Fatalf("CreateBucketPrefix after restart error = %v", err)
	}
	if _, err := restarted.ListStorageBucketLifecycleRules(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: bucket.BucketID,
	}); err != nil {
		t.Fatalf("ListStorageBucketLifecycleRules after restart error = %v", err)
	}
	if _, err := restarted.SetStorageBucketACL(context.Background(), ports.StorageBucketACLUpdateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "persisted-bucket-acl",
		BucketID:       bucket.BucketID,
		ACL:            "private",
	}); err != nil {
		t.Fatalf("SetStorageBucketACL after restart error = %v", err)
	}
}

func TestMetadataStorageStoreUpdatesResourceState(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataStorageStore(fakeMetadataStore{tx: tx}, WithStorageStoreClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	err := store.UpdateResourceState(context.Background(), ports.StorageResourceStateUpdateRequest{
		TenantID:     storageStoreTenantID,
		ResourceKind: "volume",
		ResourceID:   "vol-test",
		State:        ports.StorageResourceFailed,
		Reason:       "PVC lost",
		UpdatedAt:    time.Unix(120, 0),
	})
	if err != nil {
		t.Fatalf("UpdateResourceState() error = %v", err)
	}
	if !strings.Contains(tx.sql, "UPDATE storage_volumes") {
		t.Fatalf("sql = %q, want storage_volumes update", tx.sql)
	}
	if got, want := tx.args[2], string(ports.StorageResourceFailed); got != want {
		t.Fatalf("state arg = %v, want %s", got, want)
	}
}
