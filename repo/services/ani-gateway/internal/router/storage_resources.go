package router

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type storageAPI struct {
	service ports.StorageService
	tasks   ports.AsyncTaskStore
}

type storageCreateVolumeRequest struct {
	IdempotencyKey  string `json:"idempotency_key"`
	Name            string `json:"name"`
	SizeGiB         int64  `json:"size_gib"`
	StorageClass    string `json:"storage_class"`
	Zone            string `json:"zone,omitempty"`
	VolumeType      string `json:"volume_type,omitempty"`
	Encrypted       bool   `json:"encrypted,omitempty"`
	MountInstanceID string `json:"mount_instance_id,omitempty"`
	MountRoute      string `json:"mount_route,omitempty"`
}

type storageCreateFilesystemRequest struct {
	IdempotencyKey  string `json:"idempotency_key"`
	Name            string `json:"name"`
	Protocol        string `json:"protocol"`
	SizeGiB         int64  `json:"size_gib"`
	Zone            string `json:"zone,omitempty"`
	PerformanceMode string `json:"performance_mode,omitempty"`
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

type storageVolumeExpandRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	SizeGiB        int64  `json:"size_gib"`
}

type storageVolumeMountRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	InstanceID     string `json:"instance_id"`
	InstanceRoute  string `json:"instance_route"`
	MountName      string `json:"mount_name,omitempty"`
}

type storageVolumeUnmountRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type storageVolumeFromSnapshotRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	SizeGiB        int64  `json:"size_gib"`
	Zone           string `json:"zone,omitempty"`
}

type storageVolumeAutoSnapshotPolicyUpdateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Enabled        bool   `json:"enabled"`
	RetainDays     int    `json:"retain_days"`
	Schedule       string `json:"schedule"`
}

type storageVolumeOSInitCompleteRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Mode           string `json:"mode"`
}

type storageFilesystemExpandRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	SizeGiB        int64  `json:"size_gib"`
}

type storageFilesystemMountTargetCreateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	SubnetID       string `json:"subnet_id"`
	VPCID          string `json:"vpc_id,omitempty"`
}

type storageFilesystemMountRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	InstanceID     string `json:"instance_id"`
	InstanceRoute  string `json:"instance_route"`
	MountPath      string `json:"mount_path,omitempty"`
	AutoMount      bool   `json:"auto_mount,omitempty"`
}

type storageFilesystemUnmountRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	InstanceID     string `json:"instance_id"`
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
	SizeBytes      int64  `json:"size_bytes,omitempty"`
	StorageClass   string `json:"storage_class,omitempty"`
}

type storageBucketObjectUploadRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Key            string `json:"key"`
	ContentType    string `json:"content_type,omitempty"`
	SizeBytes      int64  `json:"size_bytes,omitempty"`
	StorageClass   string `json:"storage_class,omitempty"`
}

type storageBucketPrefixCreateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Prefix         string `json:"prefix"`
}

type storageBucketPresignedURLRequest struct {
	Key          string `json:"key"`
	Method       string `json:"method,omitempty"`
	ExpiresHours int    `json:"expires_hours,omitempty"`
}

type storageBucketACLUpdateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	ACL            string `json:"acl"`
}

type storageObjectCompleteRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type storageBucketClassUpdateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	StorageClass   string `json:"storage_class"`
}

type storageBucketLifecycleRuleRequest struct {
	ID               string `json:"id,omitempty"`
	Name             string `json:"name"`
	Prefix           string `json:"prefix"`
	ExpireDays       int    `json:"expire_days"`
	ToInfrequentDays int    `json:"to_infrequent_days"`
	Enabled          bool   `json:"enabled"`
}

type storageBucketLifecycleRulesUpdateRequest struct {
	IdempotencyKey string                              `json:"idempotency_key"`
	Rules          []storageBucketLifecycleRuleRequest `json:"rules"`
}

type storageBucketLifecycleRuleCreateRequest struct {
	IdempotencyKey   string `json:"idempotency_key"`
	Name             string `json:"name"`
	Prefix           string `json:"prefix"`
	ExpireDays       int    `json:"expire_days"`
	ToInfrequentDays int    `json:"to_infrequent_days"`
	Enabled          bool   `json:"enabled"`
}

type storageVolumeResponse struct {
	ID               string                              `json:"id"`
	TenantID         string                              `json:"tenant_id"`
	Name             string                              `json:"name"`
	SizeGiB          int64                               `json:"size_gib"`
	StorageClass     string                              `json:"storage_class"`
	Zone             string                              `json:"zone,omitempty"`
	VolumeType       string                              `json:"volume_type,omitempty"`
	IOPS             int                                 `json:"iops,omitempty"`
	Encrypted        bool                                `json:"encrypted,omitempty"`
	MountInstanceID  string                              `json:"mount_instance_id,omitempty"`
	MountRoute       string                              `json:"mount_route,omitempty"`
	MountName        string                              `json:"mount_name,omitempty"`
	SnapshotsCount   int                                 `json:"snapshots_count,omitempty"`
	AutoSnapshot     storageVolumeAutoSnapshotPolicyJSON `json:"auto_snapshot"`
	OSInitStatus     string                              `json:"os_init_status,omitempty"`
	OSInitDevice     string                              `json:"os_init_device,omitempty"`
	MountHistory     []storageVolumeMountHistoryJSON     `json:"mount_history,omitempty"`
	FromSnapshotID   string                              `json:"from_snapshot_id,omitempty"`
	FromSnapshotName string                              `json:"from_snapshot_name,omitempty"`
	State            string                              `json:"state"`
	Reason           string                              `json:"reason,omitempty"`
	DevProfile       coreDevProfileResponse              `json:"dev_profile"`
	CreatedAt        string                              `json:"created_at"`
	UpdatedAt        string                              `json:"updated_at"`
}

type storageVolumeAutoSnapshotPolicyJSON struct {
	Enabled    bool   `json:"enabled"`
	RetainDays int    `json:"retain_days"`
	Schedule   string `json:"schedule"`
}

type storageVolumeMountHistoryJSON struct {
	At     string `json:"at"`
	Action string `json:"action"`
	Result string `json:"result"`
	Target string `json:"target,omitempty"`
}

type volumeOSInitGuideResponse struct {
	Status string                     `json:"status"`
	Device string                     `json:"device"`
	Steps  []volumeOSInitStepResponse `json:"steps"`
	Hint   string                     `json:"hint"`
}

type volumeOSInitStepResponse struct {
	Title   string `json:"title"`
	Command string `json:"command"`
}

type storageFilesystemResponse struct {
	ID                string                         `json:"id"`
	TenantID          string                         `json:"tenant_id"`
	Name              string                         `json:"name"`
	Protocol          string                         `json:"protocol"`
	SizeGiB           int64                          `json:"size_gib"`
	Endpoint          string                         `json:"endpoint,omitempty"`
	Zone              string                         `json:"zone,omitempty"`
	PerformanceMode   string                         `json:"performance_mode,omitempty"`
	MountTargets      []storageMountTargetResponse   `json:"mount_targets,omitempty"`
	Mounts            int                            `json:"mounts,omitempty"`
	MountCommand      string                         `json:"mount_command,omitempty"`
	AttachedInstances []filesystemAttachmentResponse `json:"attached_instances,omitempty"`
	State             string                         `json:"state"`
	Reason            string                         `json:"reason,omitempty"`
	DevProfile        coreDevProfileResponse         `json:"dev_profile"`
	CreatedAt         string                         `json:"created_at"`
	UpdatedAt         string                         `json:"updated_at"`
}

type filesystemAttachmentResponse struct {
	InstanceID    string `json:"instance_id"`
	InstanceName  string `json:"instance_name,omitempty"`
	InstanceRoute string `json:"instance_route"`
	MountPath     string `json:"mount_path"`
	IPAddress     string `json:"ip_address,omitempty"`
	Protocol      string `json:"protocol"`
	AutoMount     bool   `json:"auto_mount"`
	AttachedAt    string `json:"attached_at"`
}

type filesystemMountCommandResponse struct {
	Command   string `json:"command"`
	Protocol  string `json:"protocol"`
	IPAddress string `json:"ip_address,omitempty"`
	MountPath string `json:"mount_path,omitempty"`
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
	VPCID        string                 `json:"vpc_id,omitempty"`
	IPAddress    string                 `json:"ip_address"`
	Status       string                 `json:"status"`
	CreatedAt    string                 `json:"created_at"`
	DevProfile   coreDevProfileResponse `json:"dev_profile"`
}

type storageBucketResponse struct {
	ID             string                           `json:"id"`
	Name           string                           `json:"name"`
	Region         string                           `json:"region,omitempty"`
	Endpoint       string                           `json:"endpoint,omitempty"`
	AccessMode     string                           `json:"access_mode"`
	ACL            string                           `json:"acl,omitempty"`
	ACLLabel       string                           `json:"acl_label,omitempty"`
	StorageClass   string                           `json:"storage_class,omitempty"`
	Versioning     string                           `json:"versioning,omitempty"`
	ObjectCount    int                              `json:"object_count"`
	SizeBytes      int64                            `json:"size_bytes"`
	LifecycleRules []storageBucketLifecycleRuleJSON `json:"lifecycle_rules,omitempty"`
	LifecycleNote  string                           `json:"lifecycle_note,omitempty"`
	CreatedAt      string                           `json:"created_at"`
	UpdatedAt      string                           `json:"updated_at,omitempty"`
}

type storageBucketLifecycleRuleJSON struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Prefix           string `json:"prefix"`
	ExpireDays       int    `json:"expire_days"`
	ToInfrequentDays int    `json:"to_infrequent_days"`
	Enabled          bool   `json:"enabled"`
}

type storageBucketObjectEntryResponse struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Key          string `json:"key"`
	SizeBytes    *int64 `json:"size_bytes,omitempty"`
	SizeLabel    string `json:"size_label,omitempty"`
	StorageClass string `json:"storage_class,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type storageBucketObjectListResponse struct {
	Items      []storageBucketObjectEntryResponse `json:"items"`
	Total      int                                `json:"total"`
	Prefix     string                             `json:"prefix"`
	NextCursor *string                            `json:"next_cursor"`
}

type storageBucketObjectDeleteResponse struct {
	BucketID string `json:"bucket_id"`
	Key      string `json:"key"`
	Deleted  bool   `json:"deleted"`
}

type storageBucketLifecycleRuleListResponse struct {
	Items []storageBucketLifecycleRuleJSON `json:"items"`
	Total int                              `json:"total"`
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
	ResourceType   string         `json:"resource_type,omitempty"`
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
	return newStorageAPIWithServiceAndTasks(service, defaultTaskStore)
}

func newStorageAPIWithServiceAndTasks(service ports.StorageService, tasks ports.AsyncTaskStore) *storageAPI {
	if service == nil {
		service = runtimeadapter.NewLocalStorageService()
	}
	if tasks == nil {
		tasks = defaultTaskStore
	}
	return &storageAPI{service: service, tasks: tasks}
}

func registerStorageResourcesWithService(v1 *route.RouterGroup, service ports.StorageService) {
	registerStorageResourcesWithServiceAndTasks(v1, service, defaultTaskStore)
}

func registerStorageResourcesWithServiceAndTasks(v1 *route.RouterGroup, service ports.StorageService, tasks ports.AsyncTaskStore) {
	api := newStorageAPIWithServiceAndTasks(service, tasks)
	v1.GET("/volumes", api.listVolumes)
	v1.POST("/volumes", api.createVolume)
	v1.GET("/volumes/:volume_id", api.getVolume)
	v1.DELETE("/volumes/:volume_id", api.deleteVolume)
	v1.GET("/volumes/:volume_id/snapshots", api.listVolumeSnapshots)
	v1.POST("/volumes/:volume_id/snapshots", api.createVolumeSnapshot)
	v1.POST("/volumes/:volume_id/expand", api.expandVolume)
	v1.POST("/volumes/:volume_id/mount", api.mountVolume)
	v1.POST("/volumes/:volume_id/unmount", api.unmountVolume)
	v1.POST("/volumes/:volume_id/snapshots/:snapshot_id/create-volume", api.createVolumeFromSnapshot)
	v1.PUT("/volumes/:volume_id/auto-snapshot-policy", api.setVolumeAutoSnapshotPolicy)
	v1.GET("/volumes/:volume_id/os-init-guide", api.getVolumeOSInitGuide)
	v1.POST("/volumes/:volume_id/os-init-complete", api.completeVolumeOSInit)

	v1.GET("/filesystems", api.listFilesystems)
	v1.POST("/filesystems", api.createFilesystem)
	v1.GET("/filesystems/:filesystem_id", api.getFilesystem)
	v1.DELETE("/filesystems/:filesystem_id", api.deleteFilesystem)
	v1.GET("/filesystems/:filesystem_id/mount-targets", api.listFilesystemMountTargets)
	v1.POST("/filesystems/:filesystem_id/mount-targets", api.createFilesystemMountTarget)
	v1.POST("/filesystems/:filesystem_id/expand", api.expandFilesystem)
	v1.POST("/filesystems/:filesystem_id/mount", api.mountFilesystem)
	v1.POST("/filesystems/:filesystem_id/unmount", api.unmountFilesystem)
	v1.GET("/filesystems/:filesystem_id/mount-command", api.getFilesystemMountCommand)

	v1.GET("/buckets", api.listStorageBuckets)
	v1.POST("/buckets", api.createStorageBucket)
	v1.GET("/buckets/:bucket_id/objects", api.listBucketObjects)
	v1.DELETE("/buckets/:bucket_id/objects", api.deleteBucketObject)
	v1.POST("/buckets/:bucket_id/objects/upload", api.uploadBucketObject)
	v1.POST("/buckets/:bucket_id/prefixes", api.createBucketPrefix)
	v1.POST("/buckets/:bucket_id/objects/presigned-url", api.generateBucketObjectPresignedURL)
	v1.PUT("/buckets/:bucket_id/acl", api.setStorageBucketACL)
	v1.PUT("/buckets/:bucket_id/storage-class", api.setStorageBucketClass)
	v1.GET("/buckets/:bucket_id/lifecycle-rules", api.listStorageBucketLifecycleRules)
	v1.PUT("/buckets/:bucket_id/lifecycle-rules", api.setStorageBucketLifecycleRules)
	v1.POST("/buckets/:bucket_id/lifecycle-rules", api.createStorageBucketLifecycleRule)
	v1.DELETE("/buckets/:bucket_id/lifecycle-rules/:rule_id", api.deleteStorageBucketLifecycleRule)

	v1.GET("/objects", api.listObjects)
	v1.POST("/objects", api.createObject)
	v1.POST("/objects/upload", api.uploadStorageObject)
	v1.GET("/objects/:object_id", api.getObject)
	v1.DELETE("/objects/:object_id", api.deleteObject)
	v1.GET("/objects/:object_id/download", api.downloadStorageObject)
	v1.POST("/objects/:object_id/complete", api.completeStorageObject)
}

func (api *storageAPI) createVolume(ctx context.Context, c *app.RequestContext) {
	var req storageCreateVolumeRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid volume request")
		return
	}
	record, err := api.service.CreateVolume(ctx, ports.StorageVolumeCreateRequest{
		TenantID:       instanceTenantID(c),
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
	records, err := api.service.ListVolumes(ctx, ports.StorageResourceListRequest{TenantID: instanceTenantID(c)})
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
	record, err := api.service.GetVolume(ctx, ports.StorageResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("volume_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageVolumeFromRecord(record))
}

func (api *storageAPI) deleteVolume(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteVolume(ctx, ports.StorageResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("volume_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageVolumeFromRecord(record))
}

func (api *storageAPI) expandVolume(ctx context.Context, c *app.RequestContext) {
	var req storageVolumeExpandRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid volume expand request")
		return
	}
	record, err := api.service.ExpandVolume(ctx, ports.StorageVolumeExpandRequest{
		TenantID:       instanceTenantID(c),
		VolumeID:       c.Param("volume_id"),
		IdempotencyKey: req.IdempotencyKey,
		SizeGiB:        req.SizeGiB,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	storageWriteAcceptedTask(ctx, c, api.tasks, storageCompletedTask("volume.expand", "volume", req.IdempotencyKey, map[string]any{"volume": storageVolumeFromRecord(record)}, record.UpdatedAt))
}

func (api *storageAPI) mountVolume(ctx context.Context, c *app.RequestContext) {
	var req storageVolumeMountRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid volume mount request")
		return
	}
	record, err := api.service.MountVolume(ctx, ports.StorageVolumeMountRequest{
		TenantID:       instanceTenantID(c),
		VolumeID:       c.Param("volume_id"),
		IdempotencyKey: req.IdempotencyKey,
		InstanceID:     req.InstanceID,
		InstanceRoute:  req.InstanceRoute,
		MountName:      req.MountName,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	storageWriteAcceptedTask(ctx, c, api.tasks, storageCompletedTask("volume.mount", "volume", req.IdempotencyKey, map[string]any{"volume": storageVolumeFromRecord(record)}, record.UpdatedAt))
}

func (api *storageAPI) unmountVolume(ctx context.Context, c *app.RequestContext) {
	var req storageVolumeUnmountRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid volume unmount request")
		return
	}
	record, err := api.service.UnmountVolume(ctx, ports.StorageVolumeUnmountRequest{
		TenantID:       instanceTenantID(c),
		VolumeID:       c.Param("volume_id"),
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	storageWriteAcceptedTask(ctx, c, api.tasks, storageCompletedTask("volume.unmount", "volume", req.IdempotencyKey, map[string]any{"volume": storageVolumeFromRecord(record)}, record.UpdatedAt))
}

func (api *storageAPI) createVolumeFromSnapshot(ctx context.Context, c *app.RequestContext) {
	var req storageVolumeFromSnapshotRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid snapshot create-volume request")
		return
	}
	record, err := api.service.CreateVolumeFromSnapshot(ctx, ports.StorageVolumeFromSnapshotRequest{
		TenantID:       instanceTenantID(c),
		VolumeID:       c.Param("volume_id"),
		SnapshotID:     c.Param("snapshot_id"),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		SizeGiB:        req.SizeGiB,
		Zone:           req.Zone,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	storageWriteAcceptedTask(ctx, c, api.tasks, storageCompletedTask("volume.create_from_snapshot", "volume", req.IdempotencyKey, map[string]any{"volume": storageVolumeFromRecord(record)}, record.UpdatedAt))
}

func (api *storageAPI) setVolumeAutoSnapshotPolicy(ctx context.Context, c *app.RequestContext) {
	var req storageVolumeAutoSnapshotPolicyUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid auto snapshot policy request")
		return
	}
	record, err := api.service.SetVolumeAutoSnapshotPolicy(ctx, ports.StorageVolumeAutoSnapshotPolicyUpdateRequest{
		TenantID:       instanceTenantID(c),
		VolumeID:       c.Param("volume_id"),
		IdempotencyKey: req.IdempotencyKey,
		Enabled:        req.Enabled,
		RetainDays:     req.RetainDays,
		Schedule:       req.Schedule,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageVolumeFromRecord(record))
}

func (api *storageAPI) getVolumeOSInitGuide(ctx context.Context, c *app.RequestContext) {
	guide, err := api.service.GetVolumeOSInitGuide(ctx, ports.StorageResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("volume_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, volumeOSInitGuideFromRecord(guide))
}

func (api *storageAPI) completeVolumeOSInit(ctx context.Context, c *app.RequestContext) {
	var req storageVolumeOSInitCompleteRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid os init complete request")
		return
	}
	record, err := api.service.CompleteVolumeOSInit(ctx, ports.VolumeOSInitCompleteRequest{
		TenantID:       instanceTenantID(c),
		VolumeID:       c.Param("volume_id"),
		IdempotencyKey: req.IdempotencyKey,
		Mode:           req.Mode,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageVolumeFromRecord(record))
}

func (api *storageAPI) createFilesystem(ctx context.Context, c *app.RequestContext) {
	var req storageCreateFilesystemRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid filesystem request")
		return
	}
	record, err := api.service.CreateFilesystem(ctx, ports.StorageFilesystemCreateRequest{
		TenantID:        instanceTenantID(c),
		IdempotencyKey:  req.IdempotencyKey,
		Name:            req.Name,
		Protocol:        req.Protocol,
		SizeGiB:         req.SizeGiB,
		Zone:            req.Zone,
		PerformanceMode: req.PerformanceMode,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusCreated, storageFilesystemFromRecord(record))
}

func (api *storageAPI) listFilesystems(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListFilesystems(ctx, ports.StorageResourceListRequest{TenantID: instanceTenantID(c)})
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
	record, err := api.service.GetFilesystem(ctx, ports.StorageResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("filesystem_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageFilesystemFromRecord(record))
}

func (api *storageAPI) deleteFilesystem(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteFilesystem(ctx, ports.StorageResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("filesystem_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageFilesystemFromRecord(record))
}

func (api *storageAPI) expandFilesystem(ctx context.Context, c *app.RequestContext) {
	var req storageFilesystemExpandRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid filesystem expand request")
		return
	}
	record, err := api.service.ExpandFilesystem(ctx, ports.StorageFilesystemExpandRequest{
		TenantID:       instanceTenantID(c),
		FilesystemID:   c.Param("filesystem_id"),
		IdempotencyKey: req.IdempotencyKey,
		SizeGiB:        req.SizeGiB,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	storageWriteAcceptedTask(ctx, c, api.tasks, storageCompletedTask("filesystem.expand", "filesystem", req.IdempotencyKey, map[string]any{"filesystem": storageFilesystemFromRecord(record)}, record.UpdatedAt))
}

func (api *storageAPI) createFilesystemMountTarget(ctx context.Context, c *app.RequestContext) {
	var req storageFilesystemMountTargetCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid filesystem mount target request")
		return
	}
	record, err := api.service.CreateFilesystemMountTarget(ctx, ports.FilesystemMountTargetCreateRequest{
		TenantID:       instanceTenantID(c),
		FilesystemID:   c.Param("filesystem_id"),
		IdempotencyKey: req.IdempotencyKey,
		SubnetID:       req.SubnetID,
		VPCID:          req.VPCID,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	storageWriteAcceptedTask(ctx, c, api.tasks, storageCompletedTask("filesystem.mount_target.create", "filesystem_mount_target", req.IdempotencyKey, map[string]any{"mount_target": storageMountTargetFromRecord(record)}, record.CreatedAt))
}

func (api *storageAPI) mountFilesystem(ctx context.Context, c *app.RequestContext) {
	var req storageFilesystemMountRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid filesystem mount request")
		return
	}
	record, err := api.service.MountFilesystem(ctx, ports.StorageFilesystemMountRequest{
		TenantID:       instanceTenantID(c),
		FilesystemID:   c.Param("filesystem_id"),
		IdempotencyKey: req.IdempotencyKey,
		InstanceID:     req.InstanceID,
		InstanceRoute:  req.InstanceRoute,
		MountPath:      req.MountPath,
		AutoMount:      req.AutoMount,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	storageWriteAcceptedTask(ctx, c, api.tasks, storageCompletedTask("filesystem.mount", "filesystem", req.IdempotencyKey, map[string]any{"filesystem": storageFilesystemFromRecord(record)}, record.UpdatedAt))
}

func (api *storageAPI) unmountFilesystem(ctx context.Context, c *app.RequestContext) {
	var req storageFilesystemUnmountRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid filesystem unmount request")
		return
	}
	record, err := api.service.UnmountFilesystem(ctx, ports.StorageFilesystemUnmountRequest{
		TenantID:       instanceTenantID(c),
		FilesystemID:   c.Param("filesystem_id"),
		IdempotencyKey: req.IdempotencyKey,
		InstanceID:     req.InstanceID,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	storageWriteAcceptedTask(ctx, c, api.tasks, storageCompletedTask("filesystem.unmount", "filesystem", req.IdempotencyKey, map[string]any{"filesystem": storageFilesystemFromRecord(record)}, record.UpdatedAt))
}

func (api *storageAPI) getFilesystemMountCommand(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetFilesystemMountCommand(ctx, ports.StorageResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("filesystem_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, filesystemMountCommandFromRecord(record))
}

func (api *storageAPI) createObject(ctx context.Context, c *app.RequestContext) {
	var req storageCreateObjectRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid object request")
		return
	}
	record, err := api.service.CreateObject(ctx, ports.StorageObjectCreateRequest{
		TenantID:       instanceTenantID(c),
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

func (api *storageAPI) listBucketObjects(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.ListBucketObjects(ctx, ports.StorageBucketObjectListRequest{
		TenantID: instanceTenantID(c),
		BucketID: c.Param("bucket_id"),
		Prefix:   c.Query("prefix"),
		Limit:    queryInt(c, "limit", 0),
		Cursor:   c.Query("cursor"),
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	items := make([]storageBucketObjectEntryResponse, 0, len(result.Items))
	for _, entry := range result.Items {
		items = append(items, storageBucketObjectEntryFromRecord(entry))
	}
	c.JSON(http.StatusOK, storageBucketObjectListResponse{
		Items:      items,
		Total:      result.Total,
		Prefix:     result.Prefix,
		NextCursor: stringPtrOrNil(result.NextCursor),
	})
}

func (api *storageAPI) deleteBucketObject(ctx context.Context, c *app.RequestContext) {
	key := c.Query("key")
	if key == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "key is required")
		return
	}
	result, err := api.service.DeleteBucketObject(ctx, ports.StorageBucketObjectDeleteRequest{
		TenantID: instanceTenantID(c),
		BucketID: c.Param("bucket_id"),
		Key:      key,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageBucketObjectDeleteResponse{
		BucketID: result.BucketID,
		Key:      result.Key,
		Deleted:  result.Deleted,
	})
}

func (api *storageAPI) uploadBucketObject(ctx context.Context, c *app.RequestContext) {
	var req storageBucketObjectUploadRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid bucket object upload request")
		return
	}
	record, err := api.service.CreateStorageObjectUpload(ctx, ports.StorageObjectUploadRequest{
		TenantID:       instanceTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		BucketID:       c.Param("bucket_id"),
		Key:            req.Key,
		ContentType:    req.ContentType,
		SizeBytes:      req.SizeBytes,
		StorageClass:   req.StorageClass,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectUploadFromRecord(record))
}

func (api *storageAPI) createBucketPrefix(ctx context.Context, c *app.RequestContext) {
	var req storageBucketPrefixCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid bucket prefix request")
		return
	}
	entry, err := api.service.CreateBucketPrefix(ctx, ports.StorageBucketPrefixCreateRequest{
		TenantID:       instanceTenantID(c),
		BucketID:       c.Param("bucket_id"),
		IdempotencyKey: req.IdempotencyKey,
		Prefix:         req.Prefix,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusCreated, storageBucketObjectEntryFromRecord(entry))
}

func (api *storageAPI) generateBucketObjectPresignedURL(ctx context.Context, c *app.RequestContext) {
	var req storageBucketPresignedURLRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid presigned url request")
		return
	}
	record, err := api.service.GenerateBucketObjectPresignedURL(ctx, ports.StorageBucketPresignedURLRequest{
		TenantID:     instanceTenantID(c),
		BucketID:     c.Param("bucket_id"),
		Key:          req.Key,
		Method:       req.Method,
		ExpiresHours: req.ExpiresHours,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectDownloadFromRecord(record))
}

func (api *storageAPI) setStorageBucketACL(ctx context.Context, c *app.RequestContext) {
	var req storageBucketACLUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid bucket acl request")
		return
	}
	record, err := api.service.SetStorageBucketACL(ctx, ports.StorageBucketACLUpdateRequest{
		TenantID:       instanceTenantID(c),
		BucketID:       c.Param("bucket_id"),
		IdempotencyKey: req.IdempotencyKey,
		ACL:            req.ACL,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageBucketFromRecord(record))
}

func (api *storageAPI) setStorageBucketClass(ctx context.Context, c *app.RequestContext) {
	var req storageBucketClassUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid bucket storage class request")
		return
	}
	record, err := api.service.SetStorageBucketClass(ctx, ports.StorageBucketClassUpdateRequest{
		TenantID:       instanceTenantID(c),
		BucketID:       c.Param("bucket_id"),
		IdempotencyKey: req.IdempotencyKey,
		StorageClass:   req.StorageClass,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageBucketFromRecord(record))
}

func (api *storageAPI) listStorageBucketLifecycleRules(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.ListStorageBucketLifecycleRules(ctx, ports.StorageResourceGetRequest{
		TenantID:   instanceTenantID(c),
		ResourceID: c.Param("bucket_id"),
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	items := make([]storageBucketLifecycleRuleJSON, 0, len(result.Items))
	for _, rule := range result.Items {
		items = append(items, storageBucketLifecycleRuleFromRecord(rule))
	}
	c.JSON(http.StatusOK, storageBucketLifecycleRuleListResponse{Items: items, Total: result.Total})
}

func (api *storageAPI) setStorageBucketLifecycleRules(ctx context.Context, c *app.RequestContext) {
	var req storageBucketLifecycleRulesUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid lifecycle rules request")
		return
	}
	rules := make([]ports.StorageBucketLifecycleRule, 0, len(req.Rules))
	for _, rule := range req.Rules {
		rules = append(rules, ports.StorageBucketLifecycleRule{
			ID:               rule.ID,
			Name:             rule.Name,
			Prefix:           rule.Prefix,
			ExpireDays:       rule.ExpireDays,
			ToInfrequentDays: rule.ToInfrequentDays,
			Enabled:          rule.Enabled,
		})
	}
	result, err := api.service.SetStorageBucketLifecycleRules(ctx, ports.StorageBucketLifecycleRulesUpdateRequest{
		TenantID:       instanceTenantID(c),
		BucketID:       c.Param("bucket_id"),
		IdempotencyKey: req.IdempotencyKey,
		Rules:          rules,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	items := make([]storageBucketLifecycleRuleJSON, 0, len(result.Items))
	for _, rule := range result.Items {
		items = append(items, storageBucketLifecycleRuleFromRecord(rule))
	}
	c.JSON(http.StatusOK, storageBucketLifecycleRuleListResponse{Items: items, Total: result.Total})
}

func (api *storageAPI) createStorageBucketLifecycleRule(ctx context.Context, c *app.RequestContext) {
	var req storageBucketLifecycleRuleCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid lifecycle rule request")
		return
	}
	rule, err := api.service.CreateStorageBucketLifecycleRule(ctx, ports.StorageBucketLifecycleRuleCreateRequest{
		TenantID:         instanceTenantID(c),
		BucketID:         c.Param("bucket_id"),
		IdempotencyKey:   req.IdempotencyKey,
		Name:             req.Name,
		Prefix:           req.Prefix,
		ExpireDays:       req.ExpireDays,
		ToInfrequentDays: req.ToInfrequentDays,
		Enabled:          req.Enabled,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusCreated, storageBucketLifecycleRuleFromRecord(rule))
}

func (api *storageAPI) deleteStorageBucketLifecycleRule(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.DeleteStorageBucketLifecycleRule(ctx, ports.StorageBucketLifecycleRuleDeleteRequest{
		TenantID: instanceTenantID(c),
		BucketID: c.Param("bucket_id"),
		RuleID:   c.Param("rule_id"),
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	items := make([]storageBucketLifecycleRuleJSON, 0, len(result.Items))
	for _, rule := range result.Items {
		items = append(items, storageBucketLifecycleRuleFromRecord(rule))
	}
	c.JSON(http.StatusOK, storageBucketLifecycleRuleListResponse{Items: items, Total: result.Total})
}

func (api *storageAPI) listObjects(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListObjects(ctx, ports.StorageResourceListRequest{TenantID: instanceTenantID(c)})
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
	record, err := api.service.GetObject(ctx, ports.StorageResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("object_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectFromRecord(record))
}

func (api *storageAPI) deleteObject(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteObject(ctx, ports.StorageResourceGetRequest{TenantID: instanceTenantID(c), ResourceID: c.Param("object_id")})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectFromRecord(record))
}

func (api *storageAPI) createStorageBucket(ctx context.Context, c *app.RequestContext) {
	var req storageCreateBucketRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid bucket request")
		return
	}
	record, err := api.service.CreateStorageBucket(ctx, ports.StorageBucketCreateRequest{
		TenantID:       instanceTenantID(c),
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
		TenantID: instanceTenantID(c),
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
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid object upload request")
		return
	}
	record, err := api.service.CreateStorageObjectUpload(ctx, ports.StorageObjectUploadRequest{
		TenantID:       instanceTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		BucketID:       req.BucketID,
		Key:            req.Key,
		ContentType:    req.ContentType,
		SizeBytes:      req.SizeBytes,
		StorageClass:   req.StorageClass,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectUploadFromRecord(record))
}

func (api *storageAPI) downloadStorageObject(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetStorageObjectDownload(ctx, ports.StorageObjectDownloadRequest{
		TenantID:       instanceTenantID(c),
		ObjectID:       c.Param("object_id"),
		ExpiresSeconds: queryInt(c, "expires_seconds", 3600),
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectDownloadFromRecord(record))
}

func (api *storageAPI) completeStorageObject(ctx context.Context, c *app.RequestContext) {
	var req storageObjectCompleteRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid storage object complete request")
		return
	}
	record, err := api.service.CompleteStorageObject(ctx, ports.StorageObjectCompleteRequest{
		TenantID:       instanceTenantID(c),
		ObjectID:       c.Param("object_id"),
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		writeStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, storageObjectFromRecord(record))
}

func (api *storageAPI) createVolumeSnapshot(ctx context.Context, c *app.RequestContext) {
	var req storageCreateSnapshotRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid snapshot request")
		return
	}
	record, err := api.service.CreateVolumeSnapshot(ctx, ports.VolumeSnapshotCreateRequest{
		TenantID:       instanceTenantID(c),
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
		TenantID: instanceTenantID(c),
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
		TenantID:     instanceTenantID(c),
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
	history := make([]storageVolumeMountHistoryJSON, 0, len(record.MountHistory))
	for _, item := range record.MountHistory {
		history = append(history, storageVolumeMountHistoryFromRecord(item))
	}
	return storageVolumeResponse{
		ID:              record.VolumeID,
		TenantID:        record.TenantID,
		Name:            record.Name,
		SizeGiB:         record.SizeGiB,
		StorageClass:    record.StorageClass,
		Zone:            record.Zone,
		VolumeType:      record.VolumeType,
		IOPS:            record.IOPS,
		Encrypted:       record.Encrypted,
		MountInstanceID: record.MountInstanceID,
		MountRoute:      record.MountRoute,
		MountName:       record.MountName,
		SnapshotsCount:  record.SnapshotsCount,
		AutoSnapshot: storageVolumeAutoSnapshotPolicyJSON{
			Enabled:    record.AutoSnapshot.Enabled,
			RetainDays: record.AutoSnapshot.RetainDays,
			Schedule:   record.AutoSnapshot.Schedule,
		},
		OSInitStatus:     record.OSInitStatus,
		OSInitDevice:     record.OSInitDevice,
		MountHistory:     history,
		FromSnapshotID:   record.FromSnapshotID,
		FromSnapshotName: record.FromSnapshotName,
		State:            string(record.State),
		Reason:           record.Reason,
		DevProfile:       localCoreDevProfile("local-storage-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:        networkTime(record.CreatedAt),
		UpdatedAt:        networkTime(record.UpdatedAt),
	}
}

func storageVolumeMountHistoryFromRecord(record ports.StorageVolumeMountHistoryEntry) storageVolumeMountHistoryJSON {
	return storageVolumeMountHistoryJSON{
		At:     networkTime(record.At),
		Action: record.Action,
		Result: record.Result,
		Target: record.Target,
	}
}

func volumeOSInitGuideFromRecord(record ports.VolumeOSInitGuide) volumeOSInitGuideResponse {
	steps := make([]volumeOSInitStepResponse, 0, len(record.Steps))
	for _, step := range record.Steps {
		steps = append(steps, volumeOSInitStepResponse{Title: step.Title, Command: step.Command})
	}
	return volumeOSInitGuideResponse{Status: record.Status, Device: record.Device, Steps: steps, Hint: record.Hint}
}

func storageFilesystemFromRecord(record ports.StorageFilesystemRecord) storageFilesystemResponse {
	mountTargets := make([]storageMountTargetResponse, 0, len(record.MountTargets))
	for _, target := range record.MountTargets {
		mountTargets = append(mountTargets, storageMountTargetFromRecord(target))
	}
	attachments := make([]filesystemAttachmentResponse, 0, len(record.AttachedInstances))
	for _, attachment := range record.AttachedInstances {
		attachments = append(attachments, filesystemAttachmentFromRecord(attachment))
	}
	return storageFilesystemResponse{
		ID:                record.FilesystemID,
		TenantID:          record.TenantID,
		Name:              record.Name,
		Protocol:          record.Protocol,
		SizeGiB:           record.SizeGiB,
		Endpoint:          record.Endpoint,
		Zone:              record.Zone,
		PerformanceMode:   record.PerformanceMode,
		MountTargets:      mountTargets,
		Mounts:            record.Mounts,
		MountCommand:      record.MountCommand,
		AttachedInstances: attachments,
		State:             string(record.State),
		Reason:            record.Reason,
		DevProfile:        localCoreDevProfile("local-storage-service", "Core dev/local profile; provider execution is gated separately"),
		CreatedAt:         networkTime(record.CreatedAt),
		UpdatedAt:         networkTime(record.UpdatedAt),
	}
}

func filesystemAttachmentFromRecord(record ports.FilesystemAttachment) filesystemAttachmentResponse {
	return filesystemAttachmentResponse{
		InstanceID:    record.InstanceID,
		InstanceName:  record.InstanceName,
		InstanceRoute: record.InstanceRoute,
		MountPath:     record.MountPath,
		IPAddress:     record.IPAddress,
		Protocol:      record.Protocol,
		AutoMount:     record.AutoMount,
		AttachedAt:    networkTime(record.AttachedAt),
	}
}

func filesystemMountCommandFromRecord(record ports.FilesystemMountCommand) filesystemMountCommandResponse {
	return filesystemMountCommandResponse{
		Command:   record.Command,
		Protocol:  record.Protocol,
		IPAddress: record.IPAddress,
		MountPath: record.MountPath,
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
	rules := make([]storageBucketLifecycleRuleJSON, 0, len(record.LifecycleRules))
	for _, rule := range record.LifecycleRules {
		rules = append(rules, storageBucketLifecycleRuleFromRecord(rule))
	}
	updatedAt := ""
	if !record.UpdatedAt.IsZero() {
		updatedAt = networkTime(record.UpdatedAt)
	}
	return storageBucketResponse{
		ID:             record.BucketID,
		Name:           record.Name,
		Region:         record.Region,
		Endpoint:       record.Endpoint,
		AccessMode:     record.AccessMode,
		ACL:            record.ACL,
		ACLLabel:       record.ACLLabel,
		StorageClass:   record.StorageClass,
		Versioning:     record.Versioning,
		ObjectCount:    record.ObjectCount,
		SizeBytes:      record.SizeBytes,
		LifecycleRules: rules,
		LifecycleNote:  record.LifecycleNote,
		CreatedAt:      networkTime(record.CreatedAt),
		UpdatedAt:      updatedAt,
	}
}

func storageBucketLifecycleRuleFromRecord(rule ports.StorageBucketLifecycleRule) storageBucketLifecycleRuleJSON {
	return storageBucketLifecycleRuleJSON{
		ID:               rule.ID,
		Name:             rule.Name,
		Prefix:           rule.Prefix,
		ExpireDays:       rule.ExpireDays,
		ToInfrequentDays: rule.ToInfrequentDays,
		Enabled:          rule.Enabled,
	}
}

func storageBucketObjectEntryFromRecord(entry ports.StorageBucketObjectEntry) storageBucketObjectEntryResponse {
	updatedAt := ""
	if !entry.UpdatedAt.IsZero() {
		updatedAt = networkTime(entry.UpdatedAt)
	}
	return storageBucketObjectEntryResponse{
		Kind:         entry.Kind,
		Name:         entry.Name,
		Key:          entry.Key,
		SizeBytes:    entry.SizeBytes,
		SizeLabel:    entry.SizeLabel,
		StorageClass: entry.StorageClass,
		UpdatedAt:    updatedAt,
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
	return storageCompletedTask("volume.snapshot.create", "volume_snapshot", idempotencyKey, map[string]any{"snapshot": storageSnapshotFromRecord(record)}, record.CreatedAt, taskID)
}

func storageCompletedTask(taskType string, resourceType string, idempotencyKey string, result map[string]any, completedAt time.Time, taskIDs ...string) storageSnapshotTaskResponse {
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	taskID := uuid.NewString()
	if len(taskIDs) > 0 && taskIDs[0] != "" {
		taskID = taskIDs[0]
	}
	completedAtText := networkTime(completedAt)
	return storageSnapshotTaskResponse{
		ID:             taskID,
		IdempotencyKey: idempotencyKey,
		TaskType:       taskType,
		ResourceType:   resourceType,
		Status:         "completed",
		AttemptCount:   1,
		MaxAttempts:    1,
		ProgressPct:    100,
		Result:         result,
		CreatedAt:      completedAtText,
		CompletedAt:    completedAtText,
	}
}

func storageWriteAcceptedTask(ctx context.Context, c *app.RequestContext, store ports.AsyncTaskStore, task storageSnapshotTaskResponse) {
	created, _, err := store.Create(ctx, taskRecordFromResponse(instanceTenantID(c), task))
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "TASK_PERSIST_FAILED", err.Error())
		return
	}
	response := taskResponseFromRecord(created)
	c.Response.Header.Set("Location", "/api/v1/tasks/"+response.ID)
	c.JSON(http.StatusAccepted, response)
}

func stringPtrOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func storageMountTargetFromRecord(record ports.FilesystemMountTargetRecord) storageMountTargetResponse {
	return storageMountTargetResponse{
		ID:           record.MountTargetID,
		FilesystemID: record.FilesystemID,
		SubnetID:     record.SubnetID,
		VPCID:        record.VPCID,
		IPAddress:    record.IPAddress,
		Status:       string(record.Status),
		CreatedAt:    networkTime(record.CreatedAt),
		DevProfile:   localCoreDevProfile("local-storage-service", "Core dev/local profile; mount target provider execution is gated separately"),
	}
}

func writeStorageError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeInstanceError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeInstanceError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrFailedPrecondition):
		writeInstanceError(c, http.StatusPreconditionFailed, "PRECONDITION_FAILED", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeInstanceError(c, http.StatusBadRequest, "UNSUPPORTED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
