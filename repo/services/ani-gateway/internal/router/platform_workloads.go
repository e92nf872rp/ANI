package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type platformWorkloadAPI struct {
	service ports.PlatformWorkloadService
	tasks   ports.AsyncTaskStore
}

type platformWorkloadCreateRequest struct {
	IdempotencyKey string                             `json:"idempotency_key"`
	Name           string                             `json:"name"`
	WorkloadClass  string                             `json:"workload_class"`
	RuntimeKind    string                             `json:"runtime_kind"`
	ImageRef       string                             `json:"image_ref"`
	Command        []string                           `json:"command"`
	Args           []string                           `json:"args"`
	Env            []platformWorkloadEnvVarRequest    `json:"env"`
	Replicas       int                                `json:"replicas"`
	Resources      platformWorkloadResourcesRequest   `json:"resources"`
	Topology       platformWorkloadTopologyRequest    `json:"topology"`
	Scheduling     platformWorkloadSchedulingRequest  `json:"scheduling"`
	Network        platformWorkloadNetworkRequest     `json:"network"`
	Artifacts      []platformWorkloadArtifactRequest  `json:"artifacts"`
	SecretBindings []platformWorkloadSecretRequest    `json:"secret_bindings"`
	HealthCheck    platformWorkloadHealthCheckRequest `json:"health_check"`
	Metadata       platformWorkloadMetadataRequest    `json:"metadata"`
}

type platformWorkloadResourcesRequest struct {
	CPU         string                              `json:"cpu"`
	Memory      string                              `json:"memory"`
	Accelerator *platformWorkloadAcceleratorRequest `json:"accelerator"`
}

type platformWorkloadAcceleratorRequest struct {
	// SpecID 是 GPU 型号，例如 gpu-nvidia-geforce-rtx-4090。只表示型号，不表示整卡或 vGPU。
	SpecID string `json:"spec_id"`
	Count  int    `json:"count"`
	// Memory 是申请 GPU 显存，单位 MiB。省略=整卡；填写=vGPU。不是 resources.memory。
	// JSON 若出现该字段，必须 >= 1。
	Memory *int `json:"memory,omitempty"`
}

type platformWorkloadRoleRequest struct {
	Count     int                               `json:"count"`
	Resources *platformWorkloadResourcesRequest `json:"resources"`
}

type platformWorkloadTopologyRequest struct {
	Mode           string                       `json:"mode"`
	ProfileID      string                       `json:"profile_id"`
	ProfileVersion string                       `json:"profile_version"`
	Leader         *platformWorkloadRoleRequest `json:"leader"`
	Workers        *platformWorkloadRoleRequest `json:"workers"`
}

type platformWorkloadSchedulingRequest struct {
	QueueClass string `json:"queue_class"`
	Gang       bool   `json:"gang"`
}

type platformWorkloadNetworkRequest struct {
	Exposure string `json:"exposure"`
	Ports    []struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	} `json:"ports"`
}

type platformWorkloadArtifactRequest struct {
	ObjectRef string `json:"object_ref"`
	MountPath string `json:"mount_path"`
}

type platformWorkloadEnvVarRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type platformWorkloadSecretRequest struct {
	SecretRef string `json:"secret_ref"`
	MountPath string `json:"mount_path"`
}

type platformWorkloadHealthCheckRequest struct {
	Protocol string `json:"protocol"`
	Path     string `json:"path"`
	PortName string `json:"port_name"`
}

type platformWorkloadMetadataRequest struct {
	OwnerRef string            `json:"owner_ref"`
	Labels   map[string]string `json:"labels"`
}

type platformWorkloadUpdateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Replicas       int    `json:"replicas"`
}

type platformWorkloadLifecycleRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Action         string `json:"action"`
}

func registerPlatformWorkloadResources(v1 *route.RouterGroup, service ports.PlatformWorkloadService, tasks ports.AsyncTaskStore) {
	if service == nil {
		service = runtimeadapter.NewLocalPlatformWorkloadService()
	}
	if tasks == nil {
		tasks = defaultTaskStore
	}
	api := &platformWorkloadAPI{service: service, tasks: tasks}
	v1.GET("/platform-workload-capabilities", api.capabilities)
	v1.POST("/platform-workloads", api.create)
	v1.GET("/platform-workloads/:workload_id", api.get)
	v1.PATCH("/platform-workloads/:workload_id", api.update)
	v1.DELETE("/platform-workloads/:workload_id", api.delete)
	v1.POST("/platform-workloads/:workload_id/lifecycle", api.lifecycle)
	v1.GET("/platform-workloads/:workload_id/logs", api.logs)
}

func (api *platformWorkloadAPI) capabilities(ctx context.Context, c *app.RequestContext) {
	if !requirePlatformWorkloadService(c, false) {
		return
	}
	caps, err := api.service.Capabilities(ctx)
	if err != nil {
		writePlatformWorkloadError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"supported_topology_modes": caps.SupportedTopologyModes,
		"leader_worker_set_ready":  caps.LeaderWorkerSetReady,
		"gang_scheduling_ready":    caps.GangSchedulingReady,
		"supported_topology_profiles": func() []map[string]any {
			items := make([]map[string]any, 0, len(caps.SupportedProfiles))
			for _, profile := range caps.SupportedProfiles {
				items = append(items, map[string]any{"id": profile.ID, "version": profile.Version, "mode": profile.Mode})
			}
			return items
		}(),
		"accelerator_specs": func() []map[string]any {
			items := make([]map[string]any, 0, len(caps.AcceleratorSpecs))
			for _, spec := range caps.AcceleratorSpecs {
				items = append(items, map[string]any{
					"spec_id": spec.SpecID, "available": spec.Available, "max_single_node_count": spec.MaxSingleNodeCount,
				})
			}
			return items
		}(),
	})
}

func (api *platformWorkloadAPI) create(ctx context.Context, c *app.RequestContext) {
	if !requirePlatformWorkloadService(c, true) {
		return
	}
	tenantID, ok := platformWorkloadTenantID(c)
	if !ok {
		return
	}
	var req platformWorkloadCreateRequest
	if err := c.BindAndValidate(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid platform workload request")
		return
	}
	spec, err := platformWorkloadSpecFromRequest(req)
	if err != nil {
		writePlatformWorkloadError(c, err)
		return
	}
	record, err := api.service.Create(ctx, tenantID, spec)
	if err != nil {
		writePlatformWorkloadError(c, err)
		return
	}
	api.writeAccepted(ctx, c, tenantID, req.IdempotencyKey, "platform_workload.create", record)
}

func (api *platformWorkloadAPI) get(ctx context.Context, c *app.RequestContext) {
	if !requirePlatformWorkloadService(c, false) {
		return
	}
	tenantID, ok := platformWorkloadTenantID(c)
	if !ok {
		return
	}
	record, err := api.service.Get(ctx, tenantID, c.Param("workload_id"))
	if err != nil {
		writePlatformWorkloadError(c, err)
		return
	}
	c.JSON(http.StatusOK, platformWorkloadJSON(record))
}

func (api *platformWorkloadAPI) update(ctx context.Context, c *app.RequestContext) {
	if !requirePlatformWorkloadService(c, true) {
		return
	}
	tenantID, ok := platformWorkloadTenantID(c)
	if !ok {
		return
	}
	var req platformWorkloadUpdateRequest
	if err := c.BindAndValidate(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid platform workload update")
		return
	}
	record, err := api.service.UpdateReplicas(ctx, tenantID, c.Param("workload_id"), req.IdempotencyKey, req.Replicas)
	if err != nil {
		writePlatformWorkloadError(c, err)
		return
	}
	api.writeAccepted(ctx, c, tenantID, req.IdempotencyKey, "platform_workload.scale", record)
}

func (api *platformWorkloadAPI) lifecycle(ctx context.Context, c *app.RequestContext) {
	if !requirePlatformWorkloadService(c, true) {
		return
	}
	tenantID, ok := platformWorkloadTenantID(c)
	if !ok {
		return
	}
	var req platformWorkloadLifecycleRequest
	if err := c.BindAndValidate(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid platform workload lifecycle request")
		return
	}
	record, err := api.service.ApplyLifecycle(ctx, tenantID, c.Param("workload_id"), req.IdempotencyKey, req.Action)
	if err != nil {
		writePlatformWorkloadError(c, err)
		return
	}
	api.writeAccepted(ctx, c, tenantID, req.IdempotencyKey, "platform_workload."+req.Action, record)
}

func (api *platformWorkloadAPI) delete(ctx context.Context, c *app.RequestContext) {
	if !requirePlatformWorkloadService(c, true) {
		return
	}
	tenantID, ok := platformWorkloadTenantID(c)
	if !ok {
		return
	}
	key := string(c.GetHeader("Idempotency-Key"))
	record, err := api.service.Delete(ctx, tenantID, c.Param("workload_id"), key)
	if err != nil {
		writePlatformWorkloadError(c, err)
		return
	}
	api.writeAccepted(ctx, c, tenantID, key, "platform_workload.delete", record)
}

func (api *platformWorkloadAPI) logs(ctx context.Context, c *app.RequestContext) {
	if !requirePlatformWorkloadService(c, false) {
		return
	}
	tenantID, ok := platformWorkloadTenantID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(string(c.Query("limit")))
	list, err := api.service.Logs(ctx, tenantID, c.Param("workload_id"), limit, string(c.Query("cursor")), string(c.Query("level")))
	if err != nil {
		writePlatformWorkloadError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		entry := map[string]any{
			"timestamp": item.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
			"level":     item.Level,
			"message":   item.Message,
		}
		if item.Container != "" {
			entry["container"] = item.Container
		}
		if item.Stream != "" {
			entry["stream"] = item.Stream
		}
		items = append(items, entry)
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (api *platformWorkloadAPI) writeAccepted(ctx context.Context, c *app.RequestContext, tenantID, key, taskType string, record ports.PlatformWorkloadRecord) {
	task := storageCompletedTask(taskType, "platform_workload", key, map[string]any{
		"resource_id": record.ID,
		"state":       string(record.State),
	}, record.UpdatedAt)
	recordToStore := taskRecordFromResponse(tenantID, task)
	recordToStore.ResourceID = record.ID
	created, _, err := api.tasks.Create(ctx, recordToStore)
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "TASK_PERSIST_FAILED", err.Error())
		return
	}
	response := taskResponseFromRecord(created)
	c.Response.Header.Set("Location", "/api/v1/tasks/"+response.ID)
	payload := map[string]any{
		"id": response.ID, "idempotency_key": response.IdempotencyKey, "task_type": response.TaskType,
		"resource_type": "platform_workload", "resource_id": record.ID, "status": response.Status,
		"attempt_count": response.AttemptCount, "max_attempts": response.MaxAttempts,
		"progress_pct": response.ProgressPct, "result": response.Result,
		"created_at": response.CreatedAt, "completed_at": response.CompletedAt,
	}
	c.JSON(http.StatusAccepted, payload)
}

func platformWorkloadSpecFromRequest(req platformWorkloadCreateRequest) (ports.PlatformWorkloadCreateSpec, error) {
	leader, err := platformWorkloadRoleFromRequest(req.Topology.Leader)
	if err != nil {
		return ports.PlatformWorkloadCreateSpec{}, err
	}
	workers, err := platformWorkloadRoleFromRequest(req.Topology.Workers)
	if err != nil {
		return ports.PlatformWorkloadCreateSpec{}, err
	}
	spec := ports.PlatformWorkloadCreateSpec{
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		WorkloadClass:  req.WorkloadClass,
		RuntimeKind:    req.RuntimeKind,
		ImageRef:       req.ImageRef,
		Command:        req.Command,
		Args:           req.Args,
		Env:            platformWorkloadEnvFromRequest(req.Env),
		Replicas:       req.Replicas,
		Resources:      ports.PlatformWorkloadResources{CPU: req.Resources.CPU, Memory: req.Resources.Memory},
		Topology: ports.PlatformWorkloadTopology{
			Mode: req.Topology.Mode, ProfileID: req.Topology.ProfileID, ProfileVersion: req.Topology.ProfileVersion,
			HasLeader: req.Topology.Leader != nil, HasWorkers: req.Topology.Workers != nil,
			Leader:  leader,
			Workers: workers,
		},
		Scheduling:  ports.PlatformWorkloadScheduling{QueueClass: req.Scheduling.QueueClass, Gang: req.Scheduling.Gang},
		Network:     ports.PlatformWorkloadNetwork{Exposure: req.Network.Exposure},
		HealthCheck: ports.PlatformWorkloadHealthCheck{Protocol: req.HealthCheck.Protocol, Path: req.HealthCheck.Path, PortName: req.HealthCheck.PortName},
		Metadata:    ports.PlatformWorkloadMetadata{OwnerRef: req.Metadata.OwnerRef, Labels: req.Metadata.Labels},
	}
	if req.Resources.Accelerator != nil {
		memoryMB, err := acceleratorMemoryFromJSON(req.Resources.Accelerator.Memory)
		if err != nil {
			return ports.PlatformWorkloadCreateSpec{}, err
		}
		spec.Resources.AcceleratorSpecID = req.Resources.Accelerator.SpecID
		spec.Resources.AcceleratorCount = req.Resources.Accelerator.Count
		spec.Resources.AcceleratorMemoryMB = memoryMB
	}
	for _, port := range req.Network.Ports {
		spec.Network.Ports = append(spec.Network.Ports, ports.PlatformWorkloadPort{Name: port.Name, Port: port.Port})
	}
	for _, artifact := range req.Artifacts {
		spec.Artifacts = append(spec.Artifacts, ports.PlatformWorkloadArtifact{ObjectRef: artifact.ObjectRef, MountPath: artifact.MountPath})
	}
	for _, binding := range req.SecretBindings {
		spec.SecretBindings = append(spec.SecretBindings, ports.PlatformWorkloadSecretBinding{SecretRef: binding.SecretRef, MountPath: binding.MountPath})
	}
	return spec, nil
}

func acceleratorMemoryFromJSON(mem *int) (int, error) {
	if mem == nil {
		return 0, nil
	}
	if *mem < 1 {
		return 0, fmt.Errorf("%w: accelerator memory must be at least 1 MiB", ports.ErrInvalid)
	}
	return *mem, nil
}

func platformWorkloadEnvFromRequest(items []platformWorkloadEnvVarRequest) []ports.PlatformWorkloadEnvVar {
	if len(items) == 0 {
		return nil
	}
	out := make([]ports.PlatformWorkloadEnvVar, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		out = append(out, ports.PlatformWorkloadEnvVar{Name: name, Value: item.Value})
	}
	return out
}

func platformWorkloadRoleFromRequest(role *platformWorkloadRoleRequest) (ports.PlatformWorkloadRole, error) {
	if role == nil {
		return ports.PlatformWorkloadRole{}, nil
	}
	out := ports.PlatformWorkloadRole{Count: role.Count}
	if role.Resources != nil {
		out.Resources = ports.PlatformWorkloadResources{CPU: role.Resources.CPU, Memory: role.Resources.Memory}
		if role.Resources.Accelerator != nil {
			memoryMB, err := acceleratorMemoryFromJSON(role.Resources.Accelerator.Memory)
			if err != nil {
				return ports.PlatformWorkloadRole{}, err
			}
			out.Resources.AcceleratorSpecID = role.Resources.Accelerator.SpecID
			out.Resources.AcceleratorCount = role.Resources.Accelerator.Count
			out.Resources.AcceleratorMemoryMB = memoryMB
		}
	}
	return out, nil
}

func platformWorkloadJSON(record ports.PlatformWorkloadRecord) map[string]any {
	var endpoint any
	if record.InternalEndpoint != "" {
		endpoint = record.InternalEndpoint
	}
	var reason any
	if record.Reason != "" {
		reason = record.Reason
	}
	var message any
	if record.Message != "" {
		message = record.Message
	}
	return map[string]any{
		"id":                       record.ID,
		"tenant_id":                record.TenantID,
		"name":                     record.Name,
		"state":                    string(record.State),
		"generation":               record.Generation,
		"observed_generation":      record.ObservedGeneration,
		"desired_replicas":         record.DesiredReplicas,
		"ready_replicas":           record.ReadyReplicas,
		"runtime_shape":            record.RuntimeShape,
		"topology_profile_id":      record.TopologyProfileID,
		"topology_profile_version": record.TopologyProfileVersion,
		"internal_endpoint":        endpoint,
		"reason":                   reason,
		"message":                  message,
		"created_at":               networkTime(record.CreatedAt),
		"updated_at":               networkTime(record.UpdatedAt),
	}
}

func requirePlatformWorkloadService(c *app.RequestContext, write bool) bool {
	if middleware.GetPrincipalKind(c) != "service" {
		writeInstanceError(c, http.StatusForbidden, "FORBIDDEN", "platform-workloads require a service principal")
		return false
	}
	need := "scope:platform-workloads:read"
	if write {
		need = "scope:platform-workloads:write"
	}
	scope := middleware.GetServiceScope(c)
	if !strings.Contains(scope, need) && !strings.Contains(scope, "scope:platform-workloads:write") {
		writeInstanceError(c, http.StatusForbidden, "FORBIDDEN", "missing platform-workloads scope")
		return false
	}
	return true
}

func platformWorkloadTenantID(c *app.RequestContext) (string, bool) {
	tenantID := strings.TrimSpace(middleware.GetTenantID(c))
	if tenantID == "" {
		writeInstanceError(c, http.StatusUnauthorized, "UNAUTHORIZED", "tenant identity is required")
		return "", false
	}
	return tenantID, true
}

func writePlatformWorkloadError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeInstanceError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeInstanceError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrFailedPrecondition):
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, ports.ErrUnavailable):
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", err.Error())
	default:
		writeInstanceError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
	}
}
