package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
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

func TestStorageAPIUsesInjectedService(t *testing.T) {
	service := runtimeadapter.NewLocalStorageService()
	api := newStorageAPIWithService(service)
	if api.service != service {
		t.Fatalf("api.service = %T, want injected storage service", api.service)
	}
	volume, err := api.service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-injected-volume",
		Name:           "injected",
		SizeGiB:        1,
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	if volume.VolumeID == "" {
		t.Fatalf("volume = %+v, want injected service to create volume", volume)
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

func TestStorageAPIBucketAndSignedURLResponsesMatchCoreSchema(t *testing.T) {
	api := newStorageAPI()
	bucket, err := api.service.CreateStorageBucket(context.Background(), ports.StorageBucketCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-bucket-a",
		Name:           "models-a",
		Region:         "local",
		AccessMode:     "private",
	})
	if err != nil {
		t.Fatalf("CreateStorageBucket error = %v", err)
	}
	if got := storageBucketFromRecord(bucket); got.ID == "" || got.Name != "models-a" || got.AccessMode != "private" || got.CreatedAt == "" {
		t.Fatalf("bucket response = %+v, want StorageBucketRecord fields", got)
	}

	upload, err := api.service.CreateStorageObjectUpload(context.Background(), ports.StorageObjectUploadRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-upload-a",
		BucketID:       bucket.BucketID,
		Key:            "llm/model.bin",
		ContentType:    "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("CreateStorageObjectUpload error = %v", err)
	}
	if got := storageObjectUploadFromRecord(upload); got.ObjectID == "" || got.UploadURL == "" || got.ExpiresAt == "" {
		t.Fatalf("upload response = %+v, want StorageObjectUploadResponse fields", got)
	}

	download, err := api.service.GetStorageObjectDownload(context.Background(), ports.StorageObjectDownloadRequest{
		TenantID:       "tenant-a",
		ObjectID:       upload.ObjectID,
		ExpiresSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("GetStorageObjectDownload error = %v", err)
	}
	if got := storageObjectDownloadFromRecord(download); got.DownloadURL == "" || got.ExpiresAt == "" || got.ContentType != "application/octet-stream" {
		t.Fatalf("download response = %+v, want StorageObjectDownloadInfo fields", got)
	}

	buckets, err := api.service.ListStorageBuckets(context.Background(), ports.StorageResourceListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListStorageBuckets error = %v", err)
	}
	if got := storageBucketListFromRecords(buckets); got.Total != 1 || got.NextCursor != nil || len(got.Items) != 1 || got.Items[0].Name != "models-a" {
		t.Fatalf("bucket list response = %+v, want items,total,next_cursor aligned with StorageBucketListResponse", got)
	}
}

func TestStorageAPIBucketConsoleResponsesMatchCoreSchema(t *testing.T) {
	api := newStorageAPI()
	bucket, err := api.service.CreateStorageBucket(context.Background(), ports.StorageBucketCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-bucket-console",
		Name:           "datasets-console",
		Region:         "cn-east-1",
		AccessMode:     "private",
	})
	if err != nil {
		t.Fatalf("CreateStorageBucket error = %v", err)
	}
	gotBucket := storageBucketFromRecord(bucket)
	if gotBucket.Endpoint == "" || gotBucket.ACL != "private" || gotBucket.StorageClass != "standard" || gotBucket.Versioning != "disabled" {
		t.Fatalf("bucket response = %+v, want console StorageBucketRecord fields", gotBucket)
	}

	prefix, err := api.service.CreateBucketPrefix(context.Background(), ports.StorageBucketPrefixCreateRequest{
		TenantID:       "tenant-a",
		BucketID:       bucket.BucketID,
		IdempotencyKey: "api-prefix-a",
		Prefix:         "logs/",
	})
	if err != nil {
		t.Fatalf("CreateBucketPrefix error = %v", err)
	}
	if got := storageBucketObjectEntryFromRecord(prefix); got.Kind != "prefix" || got.Key != "logs/" || got.Name == "" {
		t.Fatalf("prefix response = %+v, want StorageBucketObjectEntry", got)
	}

	upload, err := api.service.CreateStorageObjectUpload(context.Background(), ports.StorageObjectUploadRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-upload-console",
		BucketID:       bucket.BucketID,
		Key:            "logs/app.log",
		ContentType:    "text/plain",
		SizeBytes:      42,
	})
	if err != nil {
		t.Fatalf("CreateStorageObjectUpload error = %v", err)
	}
	_ = upload

	objects, err := api.service.ListBucketObjects(context.Background(), ports.StorageBucketObjectListRequest{
		TenantID: "tenant-a",
		BucketID: bucket.BucketID,
		Prefix:   "logs/",
	})
	if err != nil {
		t.Fatalf("ListBucketObjects error = %v", err)
	}
	if objects.Total < 1 {
		t.Fatalf("objects = %#v, want at least one entry under logs/", objects)
	}

	rule, err := api.service.CreateStorageBucketLifecycleRule(context.Background(), ports.StorageBucketLifecycleRuleCreateRequest{
		TenantID:         "tenant-a",
		BucketID:         bucket.BucketID,
		IdempotencyKey:   "api-rule-a",
		Name:             "expire-logs",
		Prefix:           "logs/",
		ExpireDays:       30,
		ToInfrequentDays: 7,
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("CreateStorageBucketLifecycleRule error = %v", err)
	}
	if got := storageBucketLifecycleRuleFromRecord(rule); got.ID == "" || got.Name != "expire-logs" || got.ExpireDays != 30 {
		t.Fatalf("rule response = %+v, want StorageBucketLifecycleRule fields", got)
	}

	aclBucket, err := api.service.SetStorageBucketACL(context.Background(), ports.StorageBucketACLUpdateRequest{
		TenantID:       "tenant-a",
		BucketID:       bucket.BucketID,
		IdempotencyKey: "api-acl-a",
		ACL:            "tenant_read",
	})
	if err != nil {
		t.Fatalf("SetStorageBucketACL error = %v", err)
	}
	if got := storageBucketFromRecord(aclBucket); got.ACL != "tenant_read" || got.ACLLabel == "" {
		t.Fatalf("acl response = %+v, want tenant_read", got)
	}

	deleted, err := api.service.DeleteBucketObject(context.Background(), ports.StorageBucketObjectDeleteRequest{
		TenantID: "tenant-a",
		BucketID: bucket.BucketID,
		Key:      "logs/app.log",
	})
	if err != nil {
		t.Fatalf("DeleteBucketObject error = %v", err)
	}
	if !deleted.Deleted || deleted.Key != "logs/app.log" {
		t.Fatalf("delete response = %#v, want deleted logs/app.log", deleted)
	}
}

func TestStorageAPIVolumeOperationResponsesMatchCoreSchema(t *testing.T) {
	api := newStorageAPI()
	volume, err := api.service.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "api-volume-ops",
		Name:           "data-ops",
		SizeGiB:        100,
		VolumeType:     "ssd",
		Zone:           "az-a",
	})
	if err != nil {
		t.Fatalf("CreateVolume error = %v", err)
	}
	mounted, err := api.service.MountVolume(context.Background(), ports.StorageVolumeMountRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		IdempotencyKey: "api-volume-mount",
		InstanceID:     "vm-001",
		InstanceRoute:  "/compute/instances/vm",
		MountName:      "data",
	})
	if err != nil {
		t.Fatalf("MountVolume error = %v", err)
	}
	got := storageVolumeFromRecord(mounted)
	if got.Zone != "az-a" || got.VolumeType != "ssd" || got.MountInstanceID != "vm-001" || len(got.MountHistory) == 0 || got.AutoSnapshot.Schedule == "" {
		t.Fatalf("mounted response = %+v, want extended StorageVolume fields", got)
	}
	guide, err := api.service.GetVolumeOSInitGuide(context.Background(), ports.StorageResourceGetRequest{TenantID: "tenant-a", ResourceID: volume.VolumeID})
	if err != nil {
		t.Fatalf("GetVolumeOSInitGuide error = %v", err)
	}
	if gotGuide := volumeOSInitGuideFromRecord(guide); gotGuide.Device == "" || len(gotGuide.Steps) == 0 {
		t.Fatalf("guide response = %+v, want device and steps", gotGuide)
	}
	snapshot, err := api.service.CreateVolumeSnapshot(context.Background(), ports.VolumeSnapshotCreateRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		IdempotencyKey: "api-volume-snapshot",
		Name:           "snap-a",
	})
	if err != nil {
		t.Fatalf("CreateVolumeSnapshot error = %v", err)
	}
	restored, err := api.service.CreateVolumeFromSnapshot(context.Background(), ports.StorageVolumeFromSnapshotRequest{
		TenantID:       "tenant-a",
		VolumeID:       volume.VolumeID,
		SnapshotID:     snapshot.SnapshotID,
		IdempotencyKey: "api-volume-restore",
		Name:           "restored-a",
		SizeGiB:        100,
	})
	if err != nil {
		t.Fatalf("CreateVolumeFromSnapshot error = %v", err)
	}
	if gotRestored := storageVolumeFromRecord(restored); gotRestored.FromSnapshotID != snapshot.SnapshotID {
		t.Fatalf("restored response = %+v, want from_snapshot_id", gotRestored)
	}
}

func TestStorageAPIFilesystemOperationResponsesMatchCoreSchema(t *testing.T) {
	api := newStorageAPI()
	filesystem, err := api.service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:        "tenant-a",
		IdempotencyKey:  "api-fs-ops",
		Name:            "shared-ops",
		Protocol:        "nfs",
		SizeGiB:         500,
		Zone:            "az-a",
		PerformanceMode: "throughput",
	})
	if err != nil {
		t.Fatalf("CreateFilesystem error = %v", err)
	}
	target, err := api.service.CreateFilesystemMountTarget(context.Background(), ports.FilesystemMountTargetCreateRequest{
		TenantID:       "tenant-a",
		FilesystemID:   filesystem.FilesystemID,
		IdempotencyKey: "api-fs-target",
		SubnetID:       "subnet-a",
		VPCID:          "vpc-a",
	})
	if err != nil {
		t.Fatalf("CreateFilesystemMountTarget error = %v", err)
	}
	if gotTarget := storageMountTargetFromRecord(target); gotTarget.VPCID != "vpc-a" || gotTarget.IPAddress == "" {
		t.Fatalf("target response = %+v, want vpc and IP", gotTarget)
	}
	mounted, err := api.service.MountFilesystem(context.Background(), ports.StorageFilesystemMountRequest{
		TenantID:       "tenant-a",
		FilesystemID:   filesystem.FilesystemID,
		IdempotencyKey: "api-fs-mount",
		InstanceID:     "vm-001",
		InstanceRoute:  "/compute/instances/vm",
		MountPath:      "/mnt/share",
		AutoMount:      true,
	})
	if err != nil {
		t.Fatalf("MountFilesystem error = %v", err)
	}
	got := storageFilesystemFromRecord(mounted)
	if got.Zone != "az-a" || got.PerformanceMode != "throughput" || got.Mounts != 1 || len(got.AttachedInstances) != 1 {
		t.Fatalf("filesystem response = %+v, want mount fields", got)
	}
	command, err := api.service.GetFilesystemMountCommand(context.Background(), ports.StorageResourceGetRequest{TenantID: "tenant-a", ResourceID: filesystem.FilesystemID})
	if err != nil {
		t.Fatalf("GetFilesystemMountCommand error = %v", err)
	}
	if gotCommand := filesystemMountCommandFromRecord(command); gotCommand.Command == "" || gotCommand.Protocol != "nfs" {
		t.Fatalf("command response = %+v, want nfs command", gotCommand)
	}
}

func TestStorageHTTPVolumeFilesystemOperationsEndToEnd(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	registerStorageResourcesWithService(h.Group("/api/v1"), runtimeadapter.NewLocalStorageService())

	volumeResp := performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes", `{"idempotency_key":"http-volume-a","name":"data-http","size_gib":100,"storage_class":"standard","zone":"az-a","volume_type":"ssd"}`, http.StatusCreated)
	volumeID := jsonStringField(t, volumeResp, "id")
	performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes/"+volumeID+"/expand", `{"idempotency_key":"http-volume-expand","size_gib":150}`, http.StatusAccepted)
	mounted := performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes/"+volumeID+"/mount", `{"idempotency_key":"http-volume-mount","instance_id":"vm-001","instance_route":"/compute/instances/vm","mount_name":"data"}`, http.StatusAccepted)
	if jsonNestedStringField(t, mounted, "result", "volume", "mount_instance_id") != "vm-001" {
		t.Fatalf("mounted body = %s, want mount_instance_id vm-001", mounted)
	}
	guide := performJSONRequest(t, h, http.MethodGet, "/api/v1/volumes/"+volumeID+"/os-init-guide", "", http.StatusOK)
	if jsonStringField(t, guide, "device") == "" {
		t.Fatalf("guide body = %s, want device", guide)
	}
	performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes/"+volumeID+"/os-init-complete", `{"idempotency_key":"http-volume-os","mode":"done"}`, http.StatusOK)
	snapshotTask := performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes/"+volumeID+"/snapshots", `{"idempotency_key":"http-snapshot","name":"snap-http"}`, http.StatusAccepted)
	snapshotID := jsonNestedStringField(t, snapshotTask, "result", "snapshot", "id")
	restored := performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes/"+volumeID+"/snapshots/"+snapshotID+"/create-volume", `{"idempotency_key":"http-restore","name":"restored-http","size_gib":150}`, http.StatusAccepted)
	if jsonNestedStringField(t, restored, "result", "volume", "from_snapshot_id") != snapshotID {
		t.Fatalf("restored body = %s, want from_snapshot_id %s", restored, snapshotID)
	}

	filesystemResp := performJSONRequest(t, h, http.MethodPost, "/api/v1/filesystems", `{"idempotency_key":"http-fs-a","name":"shared-http","protocol":"nfs","size_gib":500,"zone":"az-a","performance_mode":"throughput"}`, http.StatusCreated)
	filesystemID := jsonStringField(t, filesystemResp, "id")
	performJSONRequest(t, h, http.MethodPost, "/api/v1/filesystems/"+filesystemID+"/expand", `{"idempotency_key":"http-fs-expand","size_gib":600}`, http.StatusAccepted)
	target := performJSONRequest(t, h, http.MethodPost, "/api/v1/filesystems/"+filesystemID+"/mount-targets", `{"idempotency_key":"http-fs-target","subnet_id":"subnet-a","vpc_id":"vpc-a"}`, http.StatusAccepted)
	if jsonNestedStringField(t, target, "result", "mount_target", "ip_address") == "" {
		t.Fatalf("target body = %s, want ip_address", target)
	}
	mountedFS := performJSONRequest(t, h, http.MethodPost, "/api/v1/filesystems/"+filesystemID+"/mount", `{"idempotency_key":"http-fs-mount","instance_id":"vm-001","instance_route":"/compute/instances/vm","mount_path":"/mnt/share","auto_mount":true}`, http.StatusAccepted)
	if jsonNestedNumberField(t, mountedFS, "result", "filesystem", "mounts") != 1 {
		t.Fatalf("mounted filesystem body = %s, want mounts 1", mountedFS)
	}
	command := performJSONRequest(t, h, http.MethodGet, "/api/v1/filesystems/"+filesystemID+"/mount-command", "", http.StatusOK)
	if jsonStringField(t, command, "command") == "" {
		t.Fatalf("command body = %s, want command", command)
	}
	performJSONRequest(t, h, http.MethodPost, "/api/v1/filesystems/"+filesystemID+"/unmount", `{"idempotency_key":"http-fs-unmount","instance_id":"vm-001"}`, http.StatusAccepted)
}

func performJSONRequest(t *testing.T, h *server.Hertz, method string, path string, body string, wantStatus int) []byte {
	t.Helper()
	var reqBody *ut.Body
	var headers []ut.Header
	if body != "" {
		reqBody = &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}
		headers = append(headers, ut.Header{Key: "Content-Type", Value: "application/json"})
	}
	resp := ut.PerformRequest(h.Engine, method, path, reqBody, headers...).Result()
	if resp.StatusCode() != wantStatus {
		t.Fatalf("%s %s status = %d body = %s, want %d", method, path, resp.StatusCode(), resp.Body(), wantStatus)
	}
	return append([]byte(nil), resp.Body()...)
}

func jsonStringField(t *testing.T, body []byte, key string) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	value, _ := decoded[key].(string)
	return value
}

func jsonNestedStringField(t *testing.T, body []byte, first string, second string, third string) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	firstMap, _ := decoded[first].(map[string]any)
	secondMap, _ := firstMap[second].(map[string]any)
	value, _ := secondMap[third].(string)
	return value
}

func jsonNestedNumberField(t *testing.T, body []byte, first string, second string, third string) float64 {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	firstMap, _ := decoded[first].(map[string]any)
	secondMap, _ := firstMap[second].(map[string]any)
	value, _ := secondMap[third].(float64)
	return value
}
