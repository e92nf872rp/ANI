package router

import (
	"context"
	"errors"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type storageAPI struct {
	service ports.StorageService
}

type storageCreateVolumeRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	SizeGiB        int64  `json:"size_gib"`
	StorageClass   string `json:"storage_class"`
}

type storageCreateFilesystemRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	SizeGiB        int64  `json:"size_gib"`
}

type storageCreateObjectRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Bucket         string `json:"bucket"`
	Key            string `json:"key"`
	SizeBytes      int64  `json:"size_bytes"`
	ContentType    string `json:"content_type"`
}

type storageCreateSnapshotRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
}

type storageCreateBucketRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Region         string `json:"region,omitempty"`
	AccessMode     string `json:"access_mode,omitempty"`
}

type storageObjectUploadRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	BucketID       string `json:"bucket_id"`
	Key            string `json:"key"`
	ContentType    string `json:"content_type,omitempty"`
}

type storageObjectCompleteRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type storageVolumeResponse struct {
	ID           string                 `json:"id"`
	TenantID     string                 `json:"tenant_id"`
	Name         string                 `json:"name"`
	SizeGiB      int64                  `json:"size_gib"`
	StorageClass string                 `json:"storage_class"`
	State        string                 `json:"state"`
	Reason       string                 `json:"reason,omitempty"`
	DevProfile   coreDevProfileResponse `json:"dev_profile"`
	CreatedAt    string                 `json:"created_at"`
	UpdatedAt    string                 `json:"updated_at"`
}

type storageFilesystemResponse struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	Name       string                 `json:"name"`
	Protocol   string                 `json:"protocol"`
	SizeGiB    int64                  `json:"size_gib"`
	Endpoint   string                 `json:"endpoint,omitempty"`
	State      string                 `json:"state"`
	Reason     string                 `json:"reason,omitempty"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
}

type storageObjectResponse struct {
	ID          string                 `json:"id"`
	TenantID    string                 `json:"tenant_id"`
	Bucket      string                 `json:"bucket"`
	Key         string                 `json:"key"`
	SizeBytes   int64                  `json:"size_bytes"`
	ContentType string                 `json:"content_type"`
	State       string                 `json:"state"`
	Reason      string                 `json:"reason,omitempty"`
	DevProfile  coreDevProfileResponse `json:"dev_profile"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
}

type storageSnapshotResponse struct {
	ID         string                 `json:"id"`
	VolumeID   string                 `json:"volume_id"`
	Name       string                 `json:"name"`
	Status     string                 `json:"status"`
	SizeBytes  int64                  `json:"size_bytes"`
	CreatedAt  string                 `json:"created_at"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
}

type storageMountTargetResponse struct {
	ID           string                 `json:"id"`
	FilesystemID string                 `json:"filesystem_id"`
	SubnetID     string                 `json:"subnet_id"`
	IPAddress    string                 `json:"ip_address"`
	Status       string                 `json:"status"`
	CreatedAt    string                 `json:"created_at"`
	DevProfile   coreDevProfileResponse `json:"dev_profile"`
}

type storageBucketResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Region      string `json:"region,omitempty"`
	AccessMode  string `json:"access_mode"`
	ObjectCount int    `json:"object_count"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
}

type storageBucketListResponse struct {
	Items      []storageBucketResponse `json:"items"`
	Total      int                     `json:"total"`
	NextCursor *string                 `json:"next_cursor"`
}

type storageObjectUploadResponse struct {
	UploadURL string `json:"upload_url"`
	ObjectID  string `json:"object_id"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type storageObjectDownloadResponse struct {
	DownloadURL string `json:"download_url"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
}

type storageSnapshotTaskResponse struct {
	ID             string         `json:"id"`
	IdempotencyKey string         `json:"idempotency_key"`
	TaskType       string         `json:"task_type"`
	ResourceType   string         `json:"resource_type"`
	Status         string         `json:"status"`
	AttemptCount   int            `json:"attempt_count"`
	MaxAttempts    int            `json:"max_attempts"`
	ProgressPct    int            `json:"progress_pct"`
	Result         map[string]any `json:"result"`
	CreatedAt      string         `json:"created_at"`
	CompletedAt    string         `json:"completed_at"`
}

func newStorageAPI() *storageAPI {
	return newStorageAPIWithService(nil)
}

func newStorageAPIWithService(service ports.StorageService) *storageAPI {
	if service == nil {
		service = runtimeadapter.NewLocalStorageService()
	}
	return &storageAPI{service: service}
}

func registerStorageResources(v1 *route.RouterGroup) {
	registerStorageResourcesWithService(v1, nil)
}

func registerStorageResourcesWithService(v1 *route.RouterGroup, service ports.StorageService) {
	api := newStorageAPIWithService(service)
	v1.GET("/volumes", api.listVolumes)
	v1.POST("/volumes", api.createVolume)
	v1.GET("/volumes/:volume_id", api.getVolume)
	v1.DELETE("/volumes/:volume_id", api.deleteVolume)
	v1.GET("/volumes/:volume_id/snapshots", api.listVolumeSnapshots)
	v1.POST("/volumes/:volume_id/snapshots", api.createVolumeSnapshot)

	v1.GET("/filesystems", api.listFilesystems)
	v1.POST("/filesystems", api.createFilesystem)
	v1.GET("/filesystems/:filesystem_id", api.getFilesystem)
	v1.DELETE("/filesystems/:filesystem_id", api.deleteFilesystem)
	v1.GET("/filesystems/:filesystem_id/mount-targets", api.listFilesystemMountTargets)

	v1.GET("/buckets", api.listStorageBuckets)
	v1.POST("/buckets", api.createStorageBucket)

	v1.GET("/objects", api.listObjects)
	v1.POST("/objects", api.createObject)
	v1.POST("/objects/upload", api.uploadStorageObject)
	v1.POST("/objects/:object_id/complete", api.completeStorageObjectUpload)
	v1.GET("/objects/:object_id", api.getObject)
	v1.DELETE("/objects/:object_id", api.deleteObject)
	v1.GET("/objects/:object_id/download", api.downloadStorageObject)
}

func (api *storageAPI) createVolume(ctx context.Context, c *app.RequestContext) {
	var req storageCreateVolumeRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid volume request")
		return
	}
	record, err := api.service.CreateVolume(ctx, ports.StorageVolumeCreateRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		SizeGiB:        req.SizeGiB,
		StorageClass:   req.StorageClass,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusCreated, storageVolumeFromRecord(record))
}

func (api *storageAPI) listVolumes(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListVolumes(ctx, ports.StorageResourceListRequest{TenantID: middleware.GetTenantID(c)})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	items := make([]storageVolumeResponse, 0, len(records))
	for _, record := range records {
		items = append(items, storageVolumeFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *storageAPI) getVolume(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetVolume(ctx, ports.StorageResourceGetRequest{TenantID: middleware.GetTenantID(c), ResourceID: c.Param("volume_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageVolumeFromRecord(record))
}

func (api *storageAPI) deleteVolume(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteVolume(ctx, ports.StorageResourceGetRequest{TenantID: middleware.GetTenantID(c), ResourceID: c.Param("volume_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageVolumeFromRecord(record))
}

func (api *storageAPI) createFilesystem(ctx context.Context, c *app.RequestContext) {
	var req storageCreateFilesystemRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid filesystem request")
		return
	}
	record, err := api.service.CreateFilesystem(ctx, ports.StorageFilesystemCreateRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Protocol:       req.Protocol,
		SizeGiB:        req.SizeGiB,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusCreated, storageFilesystemFromRecord(record))
}

func (api *storageAPI) listFilesystems(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListFilesystems(ctx, ports.StorageResourceListRequest{TenantID: middleware.GetTenantID(c)})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	items := make([]storageFilesystemResponse, 0, len(records))
	for _, record := range records {
		items = append(items, storageFilesystemFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *storageAPI) getFilesystem(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetFilesystem(ctx, ports.StorageResourceGetRequest{TenantID: middleware.GetTenantID(c), ResourceID: c.Param("filesystem_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageFilesystemFromRecord(record))
}

func (api *storageAPI) deleteFilesystem(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteFilesystem(ctx, ports.StorageResourceGetRequest{TenantID: middleware.GetTenantID(c), ResourceID: c.Param("filesystem_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageFilesystemFromRecord(record))
}

func (api *storageAPI) createObject(ctx context.Context, c *app.RequestContext) {
	var req storageCreateObjectRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid object request")
		return
	}
	record, err := api.service.CreateObject(ctx, ports.StorageObjectCreateRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Bucket:         req.Bucket,
		Key:            req.Key,
		SizeBytes:      req.SizeBytes,
		ContentType:    req.ContentType,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusCreated, storageObjectFromRecord(record))
}

func (api *storageAPI) listObjects(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListObjects(ctx, ports.StorageResourceListRequest{TenantID: middleware.GetTenantID(c)})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	items := make([]storageObjectResponse, 0, len(records))
	for _, record := range records {
		items = append(items, storageObjectFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *storageAPI) getObject(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetObject(ctx, ports.StorageResourceGetRequest{TenantID: middleware.GetTenantID(c), ResourceID: c.Param("object_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectFromRecord(record))
}

func (api *storageAPI) deleteObject(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteObject(ctx, ports.StorageResourceGetRequest{TenantID: middleware.GetTenantID(c), ResourceID: c.Param("object_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectFromRecord(record))
}

func (api *storageAPI) createStorageBucket(ctx context.Context, c *app.RequestContext) {
	var req storageCreateBucketRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid bucket request")
		return
	}
	record, err := api.service.CreateStorageBucket(ctx, ports.StorageBucketCreateRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		Region:         req.Region,
		AccessMode:     req.AccessMode,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusCreated, storageBucketFromRecord(record))
}

func (api *storageAPI) listStorageBuckets(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListStorageBuckets(ctx, ports.StorageResourceListRequest{
		TenantID: middleware.GetTenantID(c),
		Limit:    queryInt(c, "limit", 20),
		Cursor:   c.Query("cursor"),
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageBucketListFromRecords(records))
}

func (api *storageAPI) uploadStorageObject(ctx context.Context, c *app.RequestContext) {
	var req storageObjectUploadRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid object upload request")
		return
	}
	record, err := api.service.CreateStorageObjectUpload(ctx, ports.StorageObjectUploadRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		BucketID:       req.BucketID,
		Key:            req.Key,
		ContentType:    req.ContentType,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectUploadFromRecord(record))
}

func (api *storageAPI) completeStorageObjectUpload(ctx context.Context, c *app.RequestContext) {
	var req storageObjectCompleteRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid object complete request")
		return
	}
	record, err := api.service.CompleteStorageObjectUpload(ctx, ports.StorageObjectCompleteRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		ObjectID:       c.Param("object_id"),
	})
	if err != nil {
		writeStorageCompleteError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectFromRecord(record))
}

func (api *storageAPI) downloadStorageObject(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetStorageObjectDownload(ctx, ports.StorageObjectDownloadRequest{
		TenantID:       middleware.GetTenantID(c),
		ObjectID:       c.Param("object_id"),
		ExpiresSeconds: queryInt(c, "expires_seconds", 3600),
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectDownloadFromRecord(record))
}

func (api *storageAPI) createVolumeSnapshot(ctx context.Context, c *app.RequestContext) {
	var req storageCreateSnapshotRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid snapshot request")
		return
	}
	record, err := api.service.CreateVolumeSnapshot(ctx, ports.VolumeSnapshotCreateRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		VolumeID:       c.Param("volume_id"),
		Name:           req.Name,
		Description:    req.Description,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	taskID := uuid.NewString()
	c.Response.Header.Set("Location", "/api/v1/tasks/"+taskID)
	c.JSON(http.StatusAccepted, storageSnapshotTaskFromRecord(record, req.IdempotencyKey, taskID))
}

func (api *storageAPI) listVolumeSnapshots(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListVolumeSnapshots(ctx, ports.VolumeSnapshotListRequest{
		TenantID: middleware.GetTenantID(c),
		VolumeID: c.Param("volume_id"),
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	items := make([]storageSnapshotResponse, 0, len(records))
	for _, record := range records {
		items = append(items, storageSnapshotFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *storageAPI) listFilesystemMountTargets(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListFilesystemMountTargets(ctx, ports.FilesystemMountTargetListRequest{
		TenantID:     middleware.GetTenantID(c),
		FilesystemID: c.Param("filesystem_id"),
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	items := make([]storageMountTargetResponse, 0, len(records))
	for _, record := range records {
		items = append(items, storageMountTargetFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func storageVolumeFromRecord(record ports.StorageVolumeRecord) storageVolumeResponse {
	return storageVolumeResponse{
		ID:           record.VolumeID,
		TenantID:     record.TenantID,
		Name:         record.Name,
		SizeGiB:      record.SizeGiB,
		StorageClass: record.StorageClass,
		State:        string(record.State),
		Reason:       record.Reason,
		DevProfile:   localCoreDevProfile("local-storage-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:    networkTime(record.CreatedAt),
		UpdatedAt:    networkTime(record.UpdatedAt),
	}
}

func storageFilesystemFromRecord(record ports.StorageFilesystemRecord) storageFilesystemResponse {
	return storageFilesystemResponse{
		ID:         record.FilesystemID,
		TenantID:   record.TenantID,
		Name:       record.Name,
		Protocol:   record.Protocol,
		SizeGiB:    record.SizeGiB,
		Endpoint:   record.Endpoint,
		State:      string(record.State),
		Reason:     record.Reason,
		DevProfile: localCoreDevProfile("local-storage-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:  networkTime(record.CreatedAt),
		UpdatedAt:  networkTime(record.UpdatedAt),
	}
}

func storageObjectFromRecord(record ports.StorageObjectRecord) storageObjectResponse {
	return storageObjectResponse{
		ID:          record.ObjectID,
		TenantID:    record.TenantID,
		Bucket:      record.Bucket,
		Key:         record.Key,
		SizeBytes:   record.SizeBytes,
		ContentType: record.ContentType,
		State:       string(record.State),
		Reason:      record.Reason,
		DevProfile:  localCoreDevProfile("local-storage-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:   networkTime(record.CreatedAt),
		UpdatedAt:   networkTime(record.UpdatedAt),
	}
}

func storageBucketFromRecord(record ports.StorageBucketRecord) storageBucketResponse {
	return storageBucketResponse{
		ID:          record.BucketID,
		Name:        record.Name,
		Region:      record.Region,
		AccessMode:  record.AccessMode,
		ObjectCount: record.ObjectCount,
		SizeBytes:   record.SizeBytes,
		CreatedAt:   networkTime(record.CreatedAt),
	}
}

func storageBucketListFromRecords(records []ports.StorageBucketRecord) storageBucketListResponse {
	items := make([]storageBucketResponse, 0, len(records))
	for _, record := range records {
		items = append(items, storageBucketFromRecord(record))
	}
	return storageBucketListResponse{Items: items, Total: len(items), NextCursor: nil}
}

func storageObjectUploadFromRecord(record ports.StorageObjectUploadRecord) storageObjectUploadResponse {
	return storageObjectUploadResponse{
		UploadURL: record.UploadURL,
		ObjectID:  record.ObjectID,
		ExpiresAt: networkTime(record.ExpiresAt),
	}
}

func storageObjectDownloadFromRecord(record ports.StorageObjectDownloadRecord) storageObjectDownloadResponse {
	return storageObjectDownloadResponse{
		DownloadURL: record.DownloadURL,
		ExpiresAt:   networkTime(record.ExpiresAt),
		ContentType: record.ContentType,
		SizeBytes:   record.SizeBytes,
	}
}

func storageSnapshotFromRecord(record ports.VolumeSnapshotRecord) storageSnapshotResponse {
	return storageSnapshotResponse{
		ID:         record.SnapshotID,
		VolumeID:   record.VolumeID,
		Name:       record.Name,
		Status:     string(record.Status),
		SizeBytes:  record.SizeBytes,
		CreatedAt:  networkTime(record.CreatedAt),
		DevProfile: localCoreDevProfile("local-storage-service", "Core dev/local profile; snapshot provider execution is gated separately"),
	}
}

func storageSnapshotTaskFromRecord(record ports.VolumeSnapshotRecord, idempotencyKey string, taskID string) storageSnapshotTaskResponse {
	completedAt := networkTime(record.CreatedAt)
	return storageSnapshotTaskResponse{
		ID:             taskID,
		IdempotencyKey: idempotencyKey,
		TaskType:       "volume.snapshot.create",
		ResourceType:   "volume_snapshot",
		Status:         "completed",
		AttemptCount:   1,
		MaxAttempts:    1,
		ProgressPct:    100,
		Result:         map[string]any{"snapshot": storageSnapshotFromRecord(record)},
		CreatedAt:      completedAt,
		CompletedAt:    completedAt,
	}
}

func storageMountTargetFromRecord(record ports.FilesystemMountTargetRecord) storageMountTargetResponse {
	return storageMountTargetResponse{
		ID:           record.MountTargetID,
		FilesystemID: record.FilesystemID,
		SubnetID:     record.SubnetID,
		IPAddress:    record.IPAddress,
		Status:       string(record.Status),
		CreatedAt:    networkTime(record.CreatedAt),
		DevProfile:   localCoreDevProfile("local-storage-service", "Core dev/local profile; mount target provider execution is gated separately"),
	}
}

func writeStorageError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeDemoError(c, http.StatusBadRequest, "UNSUPPORTED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}

func writeStorageCompleteError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrNotConfigured):
		writeDemoError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
	default:
		writeStorageError(c, err)
	}
}
