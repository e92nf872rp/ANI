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

func TestStorageHTTPAsyncTasksKeepOperationTypeAndLocation(t *testing.T) {
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	v1 := h.Group("/api/v1")
	registerStorageResourcesWithServiceAndTasks(v1, runtimeadapter.NewLocalStorageService(), tasks)
	registerTasksWithStore(v1, tasks)

	created := performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes", `{"idempotency_key":"http-volume-async","name":"async-data","size_gib":10}`, http.StatusCreated)
	volumeID := jsonStringField(t, created, "id")

	requestBody := `{"idempotency_key":"http-volume-expand","size_gib":20}`
	req := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/volumes/"+volumeID+"/expand", &ut.Body{Body: bytes.NewBufferString(requestBody), Len: len(requestBody)}, ut.Header{Key: "Content-Type", Value: "application/json"})
	resp := req.Result()
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("expand status = %d body=%s, want 202", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("unmarshal expand body: %v", err)
	}
	if got := body["task_type"]; got != "volume.expand" {
		t.Fatalf("task_type = %v, want volume.expand", got)
	}
	if got := body["resource_type"]; got != "volume" {
		t.Fatalf("resource_type = %v, want volume", got)
	}
	taskID, _ := body["id"].(string)
	if taskID == "" {
		t.Fatalf("task id missing in body %s", resp.Body())
	}
	if got := string(resp.Header.Get("Location")); got != "/api/v1/tasks/"+taskID {
		t.Fatalf("Location = %q, want task URL for %s", got, taskID)
	}

	taskReq := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/tasks/"+taskID, nil)
	taskResp := taskReq.Result()
	if taskResp.StatusCode() != http.StatusOK {
		t.Fatalf("task status = %d body=%s, want 200", taskResp.StatusCode(), taskResp.Body())
	}
	var fetched map[string]any
	if err := json.Unmarshal(taskResp.Body(), &fetched); err != nil {
		t.Fatalf("unmarshal task body: %v", err)
	}
	for _, field := range []string{"id", "idempotency_key", "task_type", "status", "created_at"} {
		if fetched[field] == nil || fetched[field] == "" {
			t.Fatalf("task field %q missing in body %s", field, taskResp.Body())
		}
	}
	if fetched["id"] != taskID || fetched["task_type"] != "volume.expand" || fetched["status"] != "completed" {
		t.Fatalf("fetched task = %s, want completed volume.expand task %s", taskResp.Body(), taskID)
	}

	restarted := server.New()
	restarted.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	registerTasksWithStore(restarted.Group("/api/v1"), tasks)
	restartedReq := ut.PerformRequest(restarted.Engine, http.MethodGet, "/api/v1/tasks/"+taskID, nil)
	if got := restartedReq.Result().StatusCode(); got != http.StatusOK {
		t.Fatalf("task status after router restart = %d body=%s, want 200", got, restartedReq.Result().Body())
	}

	unknownReq := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/tasks/00000000-0000-0000-0000-000000000000", nil)
	if got := unknownReq.Result().StatusCode(); got != http.StatusNotFound {
		t.Fatalf("unknown task status = %d, want 404", got)
	}

	otherTenant := server.New()
	otherTenant.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-b")
		c.Next(ctx)
	})
	registerTasksWithStore(otherTenant.Group("/api/v1"), tasks)
	crossTenantReq := ut.PerformRequest(otherTenant.Engine, http.MethodGet, "/api/v1/tasks/"+taskID, nil)
	if got := crossTenantReq.Result().StatusCode(); got != http.StatusNotFound {
		t.Fatalf("cross-tenant task status = %d, want 404", got)
	}
}

func TestStorageHTTPCompleteObjectConfirmsPresignedUpload(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	registerStorageResourcesWithService(h.Group("/api/v1"), runtimeadapter.NewLocalStorageService())

	bucket := performJSONRequest(t, h, http.MethodPost, "/api/v1/buckets", `{"idempotency_key":"http-complete-bucket","name":"uploads-a","region":"local","access_mode":"private"}`, http.StatusCreated)
	bucketID := jsonStringField(t, bucket, "id")

	upload := performJSONRequest(t, h, http.MethodPost, "/api/v1/buckets/"+bucketID+"/objects/upload", `{"idempotency_key":"http-complete-upload","key":"raw/report.csv","content_type":"text/csv"}`, http.StatusOK)
	objectID := jsonStringField(t, upload, "object_id")
	if objectID == "" {
		t.Fatalf("upload response missing object_id: %s", upload)
	}

	performJSONRequest(t, h, http.MethodPost, "/api/v1/objects/"+objectID+"/complete", `{}`, http.StatusBadRequest)

	completed := performJSONRequest(t, h, http.MethodPost, "/api/v1/objects/"+objectID+"/complete", `{"idempotency_key":"http-complete-confirm"}`, http.StatusOK)
	if got := jsonStringField(t, completed, "id"); got != objectID {
		t.Fatalf("completed object id = %q, want %s", got, objectID)
	}
	if got := jsonStringField(t, completed, "state"); got != "available" {
		t.Fatalf("completed object state = %q, want available", got)
	}

	performJSONRequest(t, h, http.MethodPost, "/api/v1/objects/00000000-0000-0000-0000-000000000000/complete", `{"idempotency_key":"http-complete-missing"}`, http.StatusNotFound)
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

func TestStorageHTTPRejectsInvalidBatchLifecycleRuleDays(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	registerStorageResourcesWithService(h.Group("/api/v1"), runtimeadapter.NewLocalStorageService())

	bucketBody := performJSONRequest(
		t,
		h,
		http.MethodPost,
		"/api/v1/buckets",
		`{"idempotency_key":"http-lifecycle-bucket","name":"lifecycle-tests"}`,
		http.StatusCreated,
	)
	bucketID := jsonStringField(t, bucketBody, "id")
	performJSONRequest(
		t,
		h,
		http.MethodPut,
		"/api/v1/buckets/"+bucketID+"/lifecycle-rules",
		`{"idempotency_key":"http-lifecycle-invalid","rules":[{"name":"x","prefix":"logs/","expire_days":0,"to_infrequent_days":7,"enabled":true}]}`,
		http.StatusBadRequest,
	)
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
	performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes/"+volumeID+"/unmount", `{"idempotency_key":"http-volume-unmount"}`, http.StatusAccepted)
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
	if wantStatus == http.StatusAccepted {
		var task struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(resp.Body(), &task); err != nil {
			t.Fatalf("%s %s decode accepted task: %v", method, path, err)
		}
		if task.ID == "" {
			t.Fatalf("%s %s accepted task has no id: %s", method, path, resp.Body())
		}
		if got, want := string(resp.Header.Get("Location")), "/api/v1/tasks/"+task.ID; got != want {
			t.Fatalf("%s %s Location = %q, want %q", method, path, got, want)
		}
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
