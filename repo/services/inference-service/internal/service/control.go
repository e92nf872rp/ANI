package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

type ResourcesView struct {
	CPU         string              `json:"cpu"`
	Memory      string              `json:"memory"`
	Accelerator *domain.Accelerator `json:"accelerator,omitempty"`
}

// ServiceView 是对外产品投影：无 runtime_ref、无 ClusterIP、invocation_url 固定 null。
type ServiceView struct {
	ID                 uuid.UUID      `json:"id"`
	Name               string         `json:"name"`
	Model              string         `json:"model"`
	ModelVersionID     uuid.UUID      `json:"model_version_id"`
	ServedModelName    string         `json:"served_model_name"`
	ImageID            string         `json:"image_id,omitempty"`
	ImageRef           string         `json:"image_ref,omitempty"`
	Replicas           int            `json:"replicas"`
	ReadyReplicas      int            `json:"ready_replicas"`
	Resources          ResourcesView  `json:"resources"`
	PlacementMode      string         `json:"placement_mode"`
	Engine             *domain.Engine `json:"engine,omitempty"`
	LegacyGPUType      *string        `json:"gpu_type"`
	LegacyGPUCount     int            `json:"gpu_count_per_pod"`
	MaxConcurrency     int            `json:"max_concurrency"`
	Status             domain.Status  `json:"status"`
	StatusReason       *string        `json:"status_reason"`
	StatusMessage      *string        `json:"status_message"`
	Generation         int64          `json:"generation"`
	ObservedGeneration int64          `json:"observed_generation"`
	CurrentOperationID *uuid.UUID     `json:"current_operation_id"`
	InvocationURL      *string        `json:"invocation_url"`
	EndpointURL        *string        `json:"endpoint_url"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          *time.Time     `json:"updated_at"`
}

// OperationView 对齐 OpenAPI AsyncTask 形状。
type OperationView struct {
	ID             uuid.UUID             `json:"id"`
	TaskType       string                `json:"task_type"`
	ResourceType   string                `json:"resource_type"`
	ResourceID     uuid.UUID             `json:"resource_id"`
	IdempotencyKey string                `json:"idempotency_key"`
	Status         domain.OperationState `json:"status"`
	AttemptCount   int                   `json:"attempt_count"`
	ProgressPct    int                   `json:"progress_pct"`
	ErrorMessage   *string               `json:"error_message"`
	CreatedAt      time.Time             `json:"created_at"`
	CompletedAt    *time.Time            `json:"completed_at"`
}

// Controller 处理 list/get/scale/lifecycle/delete。mutation 同样在请求路径同步打 Core。
type Controller struct {
	store   repository.ControlStore
	runtime runtime.InferenceRuntime
	now     func() time.Time
}

func NewController(store repository.ControlStore, now func() time.Time) *Controller {
	if now == nil {
		now = time.Now
	}
	return &Controller{store: store, now: now}
}

func (c *Controller) WithRuntime(rt runtime.InferenceRuntime) *Controller {
	c.runtime = rt
	return c
}

// Get 返回产品投影。找不到或跨租户由 store 映射为 NotFound。
func (c *Controller) Get(ctx context.Context, tenantID, serviceID uuid.UUID) (ServiceView, error) {
	resource, err := c.store.GetService(ctx, tenantID, serviceID)
	if err != nil {
		return ServiceView{}, err
	}
	return projectService(resource), nil
}

// List 当前租户未删除的推理服务。
func (c *Controller) List(ctx context.Context, tenantID uuid.UUID) ([]ServiceView, error) {
	resources, err := c.store.ListServices(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	views := make([]ServiceView, 0, len(resources))
	for _, resource := range resources {
		views = append(views, projectService(resource))
	}
	return views, nil
}

// GetOperation 查询异步任务，形状对齐 OpenAPI AsyncTask。
func (c *Controller) GetOperation(ctx context.Context, tenantID, operationID uuid.UUID) (OperationView, error) {
	operation, err := c.store.GetOperation(ctx, tenantID, operationID)
	if err != nil {
		return OperationView{}, err
	}
	return projectOperation(operation), nil
}

// Scale 只改 replicas。Core 拒绝则 AbortPendingMutation，服务保持点击前状态。
func (c *Controller) Scale(ctx context.Context, tenantID, serviceID, idempotencyKey uuid.UUID, replicas int) (domain.Operation, error) {
	if idempotencyKey == uuid.Nil {
		return domain.Operation{}, fmt.Errorf("%w: idempotency key is required", ErrInvalidInput)
	}
	if replicas < 1 {
		return domain.Operation{}, fmt.Errorf("%w: replicas must be positive", ErrInvalidInput)
	}
	resource, err := c.store.GetService(ctx, tenantID, serviceID)
	if err != nil {
		return domain.Operation{}, err
	}
	if resource.DesiredSpec.PlacementMode == "multi_node" && replicas != 1 {
		return domain.Operation{}, fmt.Errorf("%w: multi-node inference requires exactly one replica", ErrUnsupportedTopology)
	}
	target := resource.DesiredSpec
	target.Replicas = replicas
	hash, err := hashMutation(serviceID, domain.ActionScale, struct {
		Replicas int `json:"replicas"`
	}{replicas})
	if err != nil {
		return domain.Operation{}, err
	}
	result, err := c.store.MutateService(ctx, repository.MutationRequest{
		TenantID: tenantID, ServiceID: serviceID, Action: domain.ActionScale, TargetSpec: target,
		OperationID: uuid.New(), OperationScope: "inference_service.scale",
		IdempotencyKey: idempotencyKey, RequestHash: hash, Now: c.now().UTC(),
	})
	if err != nil {
		return domain.Operation{}, err
	}
	return c.dispatchMutation(ctx, resource, result)
}

// Lifecycle 处理 start/stop/restart。
func (c *Controller) Lifecycle(ctx context.Context, tenantID, serviceID, idempotencyKey uuid.UUID, action domain.Action) (domain.Operation, error) {
	if idempotencyKey == uuid.Nil {
		return domain.Operation{}, fmt.Errorf("%w: idempotency key is required", ErrInvalidInput)
	}
	if action != domain.ActionStart && action != domain.ActionStop && action != domain.ActionRestart {
		return domain.Operation{}, fmt.Errorf("%w: lifecycle action must be start, stop, or restart", ErrInvalidInput)
	}
	resource, err := c.store.GetService(ctx, tenantID, serviceID)
	if err != nil {
		return domain.Operation{}, err
	}
	hash, err := hashMutation(serviceID, action, struct{}{})
	if err != nil {
		return domain.Operation{}, err
	}
	result, err := c.store.MutateService(ctx, repository.MutationRequest{
		TenantID: tenantID, ServiceID: serviceID, Action: action, OperationID: uuid.New(),
		OperationScope: "inference_service." + string(action), IdempotencyKey: idempotencyKey,
		RequestHash: hash, Now: c.now().UTC(),
	})
	if err != nil {
		return domain.Operation{}, err
	}
	return c.dispatchMutation(ctx, resource, result)
}

// Delete 使用稳定幂等键，同一服务重复删除会重放。
func (c *Controller) Delete(ctx context.Context, tenantID, serviceID uuid.UUID) (domain.Operation, error) {
	key := uuid.NewSHA1(uuid.NameSpaceURL, []byte("ani/inference-delete/"+tenantID.String()+"/"+serviceID.String()))
	hash, err := hashMutation(serviceID, domain.ActionDelete, struct{}{})
	if err != nil {
		return domain.Operation{}, err
	}
	resource, err := c.store.GetService(ctx, tenantID, serviceID)
	if err != nil {
		return domain.Operation{}, err
	}
	result, err := c.store.MutateService(ctx, repository.MutationRequest{
		TenantID: tenantID, ServiceID: serviceID, Action: domain.ActionDelete, OperationID: uuid.New(),
		OperationScope: "inference_service.delete", IdempotencyKey: key,
		RequestHash: hash, Now: c.now().UTC(),
	})
	if err != nil {
		return domain.Operation{}, err
	}
	return c.dispatchMutation(ctx, resource, result)
}

// dispatchMutation 同步打 Core；失败回滚 pending mutation。
func (c *Controller) dispatchMutation(ctx context.Context, before domain.Service, result repository.MutationResult) (domain.Operation, error) {
	if c.runtime == nil || result.Disposition != domain.TransitionCreated {
		return result.Operation, nil
	}
	observed, err := dispatchRuntime(ctx, c.runtime, result.Service, result.Operation)
	if observed.RuntimeRef != uuid.Nil && before.RuntimeRef == uuid.Nil {
		if bindErr := bindRuntime(ctx, c.store, result.Service, result.Operation, observed.RuntimeRef); bindErr != nil {
			return domain.Operation{}, bindErr
		}
	}
	if err != nil {
		_ = c.store.AbortPendingMutation(ctx, repository.MutationAbort{
			TenantID: result.Operation.TenantID, ServiceID: result.Operation.ServiceID,
			OperationID: result.Operation.ID, TargetGeneration: result.Operation.TargetGeneration,
			RestoredGeneration: before.Generation, RestoredSpec: before.DesiredSpec,
			RestoredStatus: before.Status, RestoredDesired: before.DesiredState,
		})
		return domain.Operation{}, mapRuntimeError(err)
	}
	return result.Operation, nil
}

func hashMutation(serviceID uuid.UUID, action domain.Action, intent any) (string, error) {
	encoded, err := json.Marshal(struct {
		ServiceID uuid.UUID     `json:"service_id"`
		Action    domain.Action `json:"action"`
		Intent    any           `json:"intent"`
	}{serviceID, action, intent})
	if err != nil {
		return "", fmt.Errorf("marshal normalized inference mutation: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ProjectService 给 gRPC 层用的导出投影。
func ProjectService(resource domain.Service) ServiceView {
	return projectService(resource)
}

func ProjectOperation(operation domain.Operation) OperationView {
	return projectOperation(operation)
}

// projectService 去掉内部 runtime 字段，invocation_url / endpoint_url 固定为 null。
func projectService(resource domain.Service) ServiceView {
	model := resource.Name
	var snapshot struct {
		DisplayName string `json:"display_name"`
	}
	if json.Unmarshal(resource.ModelSnapshot, &snapshot) == nil && snapshot.DisplayName != "" {
		model = snapshot.DisplayName
	}
	var legacyGPUType *string
	if resource.DesiredSpec.LegacyGPUType != "" {
		value := resource.DesiredSpec.LegacyGPUType
		legacyGPUType = &value
	}
	var statusReason, statusMessage *string
	if resource.StatusReason != "" {
		value := resource.StatusReason
		statusReason = &value
	}
	if resource.StatusMessage != "" {
		value := resource.StatusMessage
		statusMessage = &value
	}
	var currentOperationID *uuid.UUID
	if resource.CurrentOperationID != uuid.Nil {
		value := resource.CurrentOperationID
		currentOperationID = &value
	}
	updatedAt := resource.UpdatedAt
	return ServiceView{
		ID: resource.ID, Name: resource.Name, Model: model, ModelVersionID: resource.ModelVersionID,
		ServedModelName: resource.ServedModelName, Replicas: resource.DesiredSpec.Replicas,
		ImageID: resource.DesiredSpec.ExecutionProfile.ImageID, ImageRef: resource.DesiredSpec.ExecutionProfile.ImageRef,
		ReadyReplicas: resource.ReadyReplicas,
		Resources:     ResourcesView{CPU: resource.DesiredSpec.CPU, Memory: resource.DesiredSpec.Memory, Accelerator: resource.DesiredSpec.Accelerator},
		PlacementMode: resource.DesiredSpec.PlacementMode, Engine: resource.DesiredSpec.Engine, LegacyGPUType: legacyGPUType,
		LegacyGPUCount: resource.DesiredSpec.LegacyGPUCountPerPod, MaxConcurrency: 8,
		Status: resource.Status, StatusReason: statusReason, StatusMessage: statusMessage,
		Generation: resource.Generation, ObservedGeneration: resource.ObservedGeneration,
		CurrentOperationID: currentOperationID, InvocationURL: nil, EndpointURL: nil,
		CreatedAt: resource.CreatedAt, UpdatedAt: &updatedAt,
	}
}

func projectOperation(operation domain.Operation) OperationView {
	progress := 0
	if operation.State == domain.OperationCompleted {
		progress = 100
	}
	var errorMessage *string
	if operation.ErrorCode != "" || operation.ErrorMessage != "" {
		value := operation.ErrorMessage
		if operation.ErrorCode != "" && !strings.HasPrefix(value, operation.ErrorCode) {
			if value == "" {
				value = operation.ErrorCode
			} else {
				value = operation.ErrorCode + ": " + value
			}
		}
		errorMessage = &value
	}
	return OperationView{
		ID: operation.ID, TaskType: operation.TaskType(), ResourceType: "inference_service",
		ResourceID: operation.ServiceID, IdempotencyKey: operation.IdempotencyKey.String(),
		Status: operation.State, AttemptCount: operation.Attempt,
		ProgressPct: progress, ErrorMessage: errorMessage,
		CreatedAt: operation.CreatedAt, CompletedAt: operation.CompletedAt,
	}
}
