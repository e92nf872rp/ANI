package router

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	inferenceControlClient       InferenceControlClient
	inferenceImageRegistry       ports.ImageRegistry
	errInvalidInferenceLogQuery  = errors.New("invalid inference log query")
	errInferenceImageMissing     = errors.New("image_id or image_ref is required")
	errInferenceImageUnavailable = errors.New("inference runtime image is unavailable")
	inferenceDigestPinnedImage   = regexp.MustCompile(`^.+@sha256:[a-f0-9]{64}$`)
)

// registerInferenceServices 把产品 HTTP 挂到 /api/v1/svc。租户身份来自 auth middleware。
func registerInferenceServices(svc *route.RouterGroup) {
	svc.GET("/inference-services", listInferenceServices)
	svc.POST("/inference-services", createInferenceService)
	svc.GET("/inference-services/:service_id", getInferenceService)
	svc.PATCH("/inference-services/:service_id", updateInferenceService)
	svc.DELETE("/inference-services/:service_id", deleteInferenceService)
	svc.POST("/inference-services/:service_id/lifecycle", applyInferenceServiceLifecycle)
	svc.GET("/inference-services/:service_id/logs", getInferenceServiceLogs)
	svc.PUT("/inference-services/:service_id/policies", updateInferenceServicePolicies)
	svc.GET("/inference-operations/:operation_id", getInferenceOperation)
}

type createInferenceServiceJSON struct {
	IdempotencyKey  string                         `json:"idempotency_key"`
	Name            string                         `json:"name"`
	Model           string                         `json:"model"`
	ModelVersionID  string                         `json:"model_version_id"`
	ServedModelName string                         `json:"served_model_name"`
	Replicas        int32                          `json:"replicas"`
	Resources       *inferenceServiceResourcesJSON `json:"resources"`
	PlacementMode   string                         `json:"placement_mode"`
	GPUType         string                         `json:"gpu_type"`
	GPUCountPerPod  int32                          `json:"gpu_count_per_pod"`
	ImageID         string                         `json:"image_id"`
	ImageRef        string                         `json:"image_ref"`
	Engine          *inferenceServiceEngineJSON    `json:"engine"`
}

type inferenceServiceEngineJSON struct {
	Env     []inferenceServiceEngineEnvJSON `json:"env"`
	Command []string                        `json:"command"`
}

type inferenceServiceEngineEnvJSON struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type inferenceServiceResourcesJSON struct {
	CPU         string                           `json:"cpu"`
	Memory      string                           `json:"memory"` // Pod 内存预算；GPU 显存在 accelerator.memory
	Accelerator *inferenceServiceAcceleratorJSON `json:"accelerator"`
}

type inferenceServiceAcceleratorJSON struct {
	// SpecID 是 GPU 型号，例如 gpu-nvidia-geforce-rtx-4090。只表示型号，不表示整卡或 vGPU。
	SpecID          string `json:"spec_id"`
	CountPerReplica int32  `json:"count_per_replica"`
	// Memory 是申请 GPU 显存，单位 MiB。省略=整卡；填写=vGPU。不是 resources.memory。
	// JSON 若出现该字段，必须 >= 1。
	Memory *int32 `json:"memory,omitempty"`
}

type updateInferenceServiceJSON struct {
	IdempotencyKey string `json:"idempotency_key"`
	Replicas       int32  `json:"replicas"`
}

type inferenceServiceLifecycleJSON struct {
	IdempotencyKey string `json:"idempotency_key"`
	Action         string `json:"action"`
}

func listInferenceServices(ctx context.Context, c *app.RequestContext) {
	if inferenceControlClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenantID, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	resp, err := inferenceControlClient.ListInferenceServices(ctx, tenantID)
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		items = append(items, inferenceServiceJSON(item))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func createInferenceService(ctx context.Context, c *app.RequestContext) {
	if inferenceControlClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenantID, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	var req createInferenceServiceJSON
	if err := c.BindJSON(&req); err != nil {
		writeInferenceInvalid(c, "invalid inference service request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Model) == "" {
		writeInferenceInvalid(c, "idempotency_key, name, and model are required")
		return
	}
	if err := validateInferenceAcceleratorMemory(req.Resources); err != nil {
		writeInferenceInvalid(c, err.Error())
		return
	}
	engine, err := protoEngineFromJSON(req.Engine)
	if err != nil {
		writeInferenceInvalid(c, err.Error())
		return
	}
	imageID, imageRef, err := resolveInferenceCreateImage(ctx, tenantID, req.ImageID, req.ImageRef)
	if err != nil {
		if errors.Is(err, errInferenceImageMissing) {
			writeInferenceInvalid(c, "image_id or image_ref is required")
			return
		}
		writeInferenceUnprocessable(c, "IMAGE_UNAVAILABLE", "inference runtime image is unavailable")
		return
	}
	// Product create takes a real model_version_id (or the same UUID in model).
	created, err := inferenceControlClient.CreateInferenceService(ctx, tenantID, &inferencecontrolv1.CreateInferenceServiceRequest{
		IdempotencyKey:  strings.TrimSpace(req.IdempotencyKey),
		Name:            strings.TrimSpace(req.Name),
		Model:           strings.TrimSpace(req.Model),
		ModelVersionId:  strings.TrimSpace(req.ModelVersionID),
		ServedModelName: strings.TrimSpace(req.ServedModelName),
		Replicas:        req.Replicas,
		Resources:       protoResources(req.Resources),
		PlacementMode:   strings.TrimSpace(req.PlacementMode),
		GpuType:         strings.TrimSpace(req.GPUType),
		GpuCountPerPod:  req.GPUCountPerPod,
		ImageId:         imageID,
		ImageRef:        imageRef,
		Engine:          engine,
	})
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, inferenceServiceJSON(created))
}

func getInferenceService(ctx context.Context, c *app.RequestContext) {
	if inferenceControlClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenantID, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	got, err := inferenceControlClient.GetInferenceService(ctx, tenantID, c.Param("service_id"))
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, inferenceServiceJSON(got))
}

func updateInferenceService(ctx context.Context, c *app.RequestContext) {
	if inferenceControlClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenantID, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	var req updateInferenceServiceJSON
	if err := c.BindJSON(&req); err != nil {
		writeInferenceInvalid(c, "invalid inference service scale request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" || req.Replicas < 1 {
		writeInferenceInvalid(c, "idempotency_key and a positive replicas value are required")
		return
	}
	operation, err := inferenceControlClient.ScaleInferenceService(ctx, tenantID, c.Param("service_id"), strings.TrimSpace(req.IdempotencyKey), req.Replicas)
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, inferenceOperationJSON(operation))
}

func deleteInferenceService(ctx context.Context, c *app.RequestContext) {
	if inferenceControlClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenantID, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	operation, err := inferenceControlClient.DeleteInferenceService(ctx, tenantID, c.Param("service_id"))
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, inferenceOperationJSON(operation))
}

func applyInferenceServiceLifecycle(ctx context.Context, c *app.RequestContext) {
	if inferenceControlClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenantID, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	var req inferenceServiceLifecycleJSON
	if err := c.BindJSON(&req); err != nil {
		writeInferenceInvalid(c, "invalid inference service lifecycle request")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" || strings.TrimSpace(req.Action) == "" {
		writeInferenceInvalid(c, "idempotency_key and action are required")
		return
	}
	operation, err := inferenceControlClient.ApplyInferenceServiceLifecycle(ctx, tenantID, c.Param("service_id"), strings.TrimSpace(req.IdempotencyKey), strings.TrimSpace(req.Action))
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, inferenceOperationJSON(operation))
}

func getInferenceOperation(ctx context.Context, c *app.RequestContext) {
	if inferenceControlClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenantID, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	operation, err := inferenceControlClient.GetInferenceOperation(ctx, tenantID, c.Param("operation_id"))
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, inferenceOperationJSON(operation))
}

func getInferenceServiceLogs(ctx context.Context, c *app.RequestContext) {
	if inferenceControlClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenantID, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	limit, err := parseInferenceLogLimit(string(c.Query("limit")))
	if err != nil {
		writeInferenceInvalid(c, "limit must be an integer between 1 and 1000")
		return
	}
	level := strings.TrimSpace(string(c.Query("level")))
	if level != "" && level != "debug" && level != "info" && level != "warn" && level != "error" {
		writeInferenceInvalid(c, "level must be debug, info, warn, or error")
		return
	}
	resp, err := inferenceControlClient.ListInferenceServiceLogs(
		ctx, tenantID, c.Param("service_id"), limit, string(c.Query("cursor")), level,
	)
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, inferenceLogsJSON(resp))
}

func parseInferenceLogLimit(raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 1000 {
		return 0, errInvalidInferenceLogQuery
	}
	return int32(limit), nil
}

func updateInferenceServicePolicies(ctx context.Context, c *app.RequestContext) {
	_ = middleware.GetTenantID(c)
	writeInstanceError(c, http.StatusNotImplemented, "FEATURE_NOT_AVAILABLE", "inference service policies are not available in P0")
	_ = ctx
}

func requireInferenceTenant(c *app.RequestContext) (string, bool) {
	tenantID := strings.TrimSpace(middleware.GetTenantID(c))
	if tenantID == "" {
		writeInferenceUnauthorized(c)
		return "", false
	}
	return tenantID, true
}

func protoResources(resources *inferenceServiceResourcesJSON) *inferencecontrolv1.InferenceServiceResources {
	if resources == nil {
		return nil
	}
	msg := &inferencecontrolv1.InferenceServiceResources{
		Cpu: strings.TrimSpace(resources.CPU), Memory: strings.TrimSpace(resources.Memory),
	}
	if resources.Accelerator != nil && strings.TrimSpace(resources.Accelerator.SpecID) != "" {
		msg.Accelerator = &inferencecontrolv1.InferenceServiceAccelerator{
			SpecId:          strings.TrimSpace(resources.Accelerator.SpecID),
			CountPerReplica: resources.Accelerator.CountPerReplica,
		}
		if resources.Accelerator.Memory != nil {
			msg.Accelerator.Memory = *resources.Accelerator.Memory
		}
	}
	return msg
}

func validateInferenceAcceleratorMemory(resources *inferenceServiceResourcesJSON) error {
	if resources == nil || resources.Accelerator == nil || resources.Accelerator.Memory == nil {
		return nil
	}
	if *resources.Accelerator.Memory < 1 {
		return errors.New("accelerator memory must be at least 1 MiB")
	}
	return nil
}

func inferenceServiceJSON(msg *inferencecontrolv1.InferenceService) map[string]any {
	if msg == nil {
		return map[string]any{}
	}
	body := map[string]any{
		"id":                   msg.GetId(),
		"name":                 msg.GetName(),
		"model":                msg.GetModel(),
		"model_version_id":     emptyToNil(msg.GetModelVersionId()),
		"served_model_name":    msg.GetServedModelName(),
		"image_id":             emptyToNil(msg.GetImageId()),
		"image_ref":            emptyToNil(msg.GetImageRef()),
		"replicas":             msg.GetReplicas(),
		"ready_replicas":       msg.GetReadyReplicas(),
		"resources":            inferenceResourcesJSON(msg.GetResources()),
		"placement_mode":       msg.GetPlacementMode(),
		"gpu_type":             emptyToNil(msg.GetGpuType()),
		"gpu_count_per_pod":    msg.GetGpuCountPerPod(),
		"max_concurrency":      msg.GetMaxConcurrency(),
		"status":               msg.GetStatus(),
		"status_reason":        emptyToNil(msg.GetStatusReason()),
		"status_message":       emptyToNil(msg.GetStatusMessage()),
		"generation":           msg.GetGeneration(),
		"observed_generation":  msg.GetObservedGeneration(),
		"current_operation_id": emptyToNil(msg.GetCurrentOperationId()),
		"invocation_url":       nil,
		"endpoint_url":         nil,
		"created_at":           timestampJSON(msg.GetCreatedAt()),
		"updated_at":           timestampJSON(msg.GetUpdatedAt()),
	}
	if engine := inferenceEngineJSON(msg.GetEngine()); engine != nil {
		body["engine"] = engine
	}
	return body
}

func inferenceResourcesJSON(msg *inferencecontrolv1.InferenceServiceResources) map[string]any {
	if msg == nil {
		return map[string]any{"cpu": "", "memory": ""}
	}
	body := map[string]any{"cpu": msg.GetCpu(), "memory": msg.GetMemory()}
	if acc := msg.GetAccelerator(); acc != nil && strings.TrimSpace(acc.GetSpecId()) != "" {
		item := map[string]any{
			"spec_id": acc.GetSpecId(), "count_per_replica": acc.GetCountPerReplica(),
		}
		if acc.GetMemory() > 0 {
			item["memory"] = acc.GetMemory()
		}
		body["accelerator"] = item
	}
	return body
}

func inferenceLogsJSON(msg *inferencecontrolv1.ListInferenceServiceLogsResponse) map[string]any {
	items := make([]map[string]any, 0)
	if msg != nil {
		for _, item := range msg.GetItems() {
			if item == nil {
				continue
			}
			items = append(items, map[string]any{
				"timestamp": timestampJSON(item.GetTimestamp()),
				"level":     item.GetLevel(),
				"message":   item.GetMessage(),
				"container": emptyToNil(item.GetContainer()),
				"stream":    emptyToNil(item.GetStream()),
			})
		}
	}
	nextCursor := any(nil)
	if msg != nil && strings.TrimSpace(msg.GetNextCursor()) != "" {
		nextCursor = msg.GetNextCursor()
	}
	return map[string]any{"items": items, "next_cursor": nextCursor}
}

func inferenceOperationJSON(msg *inferencecontrolv1.InferenceOperation) map[string]any {
	if msg == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":              msg.GetId(),
		"idempotency_key": msg.GetIdempotencyKey(),
		"task_type":       msg.GetTaskType(),
		"resource_type":   msg.GetResourceType(),
		"resource_id":     emptyToNil(msg.GetResourceId()),
		"status":          msg.GetStatus(),
		"attempt_count":   msg.GetAttemptCount(),
		"progress_pct":    msg.GetProgressPct(),
		"error_message":   emptyToNil(msg.GetErrorMessage()),
		"created_at":      timestampJSON(msg.GetCreatedAt()),
		"completed_at":    timestampJSON(msg.GetCompletedAt()),
	}
}

func emptyToNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func resolveInferenceCreateImage(ctx context.Context, tenantID, imageID, imageRef string) (string, string, error) {
	imageID = strings.TrimSpace(imageID)
	imageRef = strings.TrimSpace(imageRef)
	if imageID == "" && imageRef == "" {
		return "", "", errInferenceImageMissing
	}
	if imageID != "" {
		digest, err := lookupInferenceRegistryImage(ctx, tenantID, imageID)
		if err == nil {
			return imageID, digest, nil
		}
		if inferenceDigestPinnedImage.MatchString(imageID) {
			return imageID, imageID, nil
		}
		return "", "", errInferenceImageUnavailable
	}
	if inferenceDigestPinnedImage.MatchString(imageRef) {
		return "", imageRef, nil
	}
	digest, err := lookupInferenceRegistryImage(ctx, tenantID, imageRef)
	if err != nil {
		return "", "", errInferenceImageUnavailable
	}
	return "", digest, nil
}

func lookupInferenceRegistryImage(ctx context.Context, tenantID, imageRef string) (string, error) {
	if inferenceImageRegistry == nil {
		return "", errInferenceImageUnavailable
	}
	_, project, repository, tag, digest := parseInferenceImageReference(imageRef)
	if repository == "" {
		return "", errInferenceImageUnavailable
	}
	if project != "" && project != tenantID {
		return "", errInferenceImageUnavailable
	}
	result, err := inferenceImageRegistry.ListImages(ctx, ports.RegistryImageListRequest{
		TenantID: tenantID, Project: tenantID, Repository: repository, Tag: tag,
	})
	if err != nil {
		return "", errInferenceImageUnavailable
	}
	for _, item := range result.Items {
		if digest != "" && item.Digest != digest {
			continue
		}
		pinned, ok := pinInferenceRegistryImage(item)
		if !ok {
			continue
		}
		return pinned, nil
	}
	return "", errInferenceImageUnavailable
}

func pinInferenceRegistryImage(image ports.RegistryImage) (string, bool) {
	if inferenceDigestPinnedImage.MatchString(strings.TrimSpace(image.Image)) {
		return strings.TrimSpace(image.Image), true
	}
	digest := strings.TrimSpace(image.Digest)
	if digest != "" && !strings.HasPrefix(digest, "sha256:") {
		digest = "sha256:" + digest
	}
	name := strings.TrimSpace(image.Image)
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at]
	}
	slash := strings.LastIndex(name, "/")
	if colon := strings.LastIndex(name, ":"); colon > slash && colon >= 0 {
		name = name[:colon]
	}
	pinned := name + "@" + digest
	if !inferenceDigestPinnedImage.MatchString(pinned) {
		return "", false
	}
	return pinned, true
}

func parseInferenceImageReference(value string) (registryHost, project, repository, tag, digest string) {
	value = strings.TrimSpace(value)
	if at := strings.Index(value, "@"); at >= 0 {
		digest = value[at+1:]
		value = value[:at]
	}
	parts := strings.Split(value, "/")
	if len(parts) > 0 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		registryHost = parts[0]
		parts = parts[1:]
	}
	if len(parts) > 1 {
		project = parts[0]
		repository = strings.Join(parts[1:], "/")
	} else if len(parts) == 1 {
		repository = parts[0]
	}
	if colon := strings.LastIndex(repository, ":"); colon >= 0 {
		tag = repository[colon+1:]
		repository = repository[:colon]
	}
	return registryHost, project, repository, tag, digest
}

func timestampJSON(value *timestamppb.Timestamp) any {
	if value == nil || !value.IsValid() {
		return nil
	}
	return value.AsTime().UTC().Format(time.RFC3339)
}
