package router

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	inferenceControlClient       InferenceControlClient
	inferencePolicyClient        InferencePolicyClient
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
	svc.GET("/inference-services/:service_id/policies", listInferenceServicePolicies)
	svc.GET("/inference-policies", listInferencePolicies)
	svc.POST("/inference-policies", createInferencePolicy)
	svc.GET("/inference-policies/:policy_id", getInferencePolicy)
	svc.PATCH("/inference-policies/:policy_id", patchInferencePolicy)
	svc.DELETE("/inference-policies/:policy_id", deleteInferencePolicy)
	svc.GET("/inference-policy-events", listInferencePolicyEvents)
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

// createInferenceAccessPolicyJSON is the public flat Services OpenAPI body.
// The protobuf Policy envelope is an internal transport detail and is never
// accepted from HTTP clients.
type createInferenceAccessPolicyJSON struct {
	IdempotencyKey string                                                        `json:"idempotency_key"`
	Name           string                                                        `json:"name"`
	Description    string                                                        `json:"description"`
	Status         optionalNonNullableJSON[string]                               `json:"status"`
	Priority       optionalNonNullableJSON[int32]                                `json:"priority"`
	Scope          inferenceAccessPolicyScopeJSON                                `json:"scope"`
	Access         *inferenceAccessPolicyAccessJSON                              `json:"access"`
	RateLimits     optionalNonNullableJSON[inferenceAccessPolicyRateLimitsJSON]  `json:"rate_limits"`
	Concurrency    optionalNonNullableJSON[inferenceAccessPolicyConcurrencyJSON] `json:"concurrency"`
}
type inferenceAccessPolicyScopeJSON struct {
	Type                string   `json:"type"`
	InferenceServiceIDs []string `json:"inference_service_ids"`
	APIKeyIDs           []string `json:"api_key_ids"`
}
type inferenceAccessPolicyAccessJSON struct {
	AllowAllTenantKeys bool     `json:"allow_all_tenant_keys"`
	AllowAPIKeyIDs     []string `json:"allow_api_key_ids"`
	DenyAPIKeyIDs      []string `json:"deny_api_key_ids"`
}
type inferenceAccessPolicyRateLimitsJSON struct {
	QPS optionalNullableInt32JSON `json:"qps"`
	RPM optionalNullableInt32JSON `json:"rpm"`
}
type inferenceAccessPolicyConcurrencyJSON struct {
	MaxInFlight     optionalNullableInt32JSON `json:"max_in_flight"`
	LeaseTTLSeconds optionalNullableInt32JSON `json:"lease_ttl_seconds"`
}

// optionalNullableInt32JSON preserves the three public JSON states needed by
// PATCH: omitted, explicit null, and an integer value.
type optionalNullableInt32JSON struct {
	Set   bool
	Value *int32
}

func (v *optionalNullableInt32JSON) UnmarshalJSON(data []byte) error {
	v.Set = true
	if string(data) == "null" {
		v.Value = nil
		return nil
	}
	var value int32
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	v.Value = &value
	return nil
}

// patchInferenceAccessPolicyJSON mirrors the public flat OpenAPI PATCH body.
// Pointer fields preserve omission so the handler can merge against the
// current policy before calling the internal full-replacement gRPC method.
type patchInferenceAccessPolicyJSON struct {
	IdempotencyKey string                                                        `json:"idempotency_key"`
	Name           optionalNonNullableJSON[string]                               `json:"name"`
	Description    json.RawMessage                                               `json:"description"`
	Status         optionalNonNullableJSON[string]                               `json:"status"`
	Priority       optionalNonNullableJSON[int32]                                `json:"priority"`
	Scope          optionalNonNullableJSON[inferenceAccessPolicyScopeJSON]       `json:"scope"`
	Access         optionalNonNullableJSON[inferenceAccessPolicyAccessJSON]      `json:"access"`
	RateLimits     optionalNonNullableJSON[inferenceAccessPolicyRateLimitsJSON]  `json:"rate_limits"`
	Concurrency    optionalNonNullableJSON[inferenceAccessPolicyConcurrencyJSON] `json:"concurrency"`
	PrivatePolicy  json.RawMessage                                               `json:"policy"`
}

type optionalNonNullableJSON[T any] struct {
	Set   bool
	Value *T
}

func (v *optionalNonNullableJSON[T]) UnmarshalJSON(data []byte) error {
	v.Set = true
	if string(data) == "null" {
		v.Value = nil
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	v.Value = &value
	return nil
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
	if inferencePolicyClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenant, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	var req inferencecontrolv1.UpdateInferenceServicePoliciesRequest
	if err := c.BindJSON(&req); err != nil {
		writeInferenceInvalid(c, "invalid policy binding request")
		return
	}
	resp, err := inferencePolicyClient.UpdateInferenceServicePolicies(ctx, tenant, c.Param("service_id"), &req)
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, inferenceServicePoliciesJSON(resp))
}

func listInferencePolicies(ctx context.Context, c *app.RequestContext) {
	if inferencePolicyClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenant, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	resp, err := inferencePolicyClient.ListInferenceAccessPolicies(ctx, tenant)
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(resp.GetItems()))
	for _, policy := range resp.GetItems() {
		items = append(items, inferenceAccessPolicyJSON(policy))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}
func createInferencePolicy(ctx context.Context, c *app.RequestContext) {
	if inferencePolicyClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenant, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	var body createInferenceAccessPolicyJSON
	if err := c.BindJSON(&body); err != nil {
		writeInferenceInvalid(c, "invalid inference policy request")
		return
	}
	if strings.TrimSpace(body.IdempotencyKey) == "" || strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Scope.Type) == "" || body.Access == nil {
		writeInferenceInvalid(c, "idempotency_key, name, scope.type, and access are required")
		return
	}
	if err := validatePolicyCreateNonNullableFields(body); err != nil {
		writeInferenceInvalid(c, err.Error())
		return
	}
	status := "enabled"
	if body.Status.Set {
		status = strings.TrimSpace(*body.Status.Value)
	}
	priority := int32(1000)
	if body.Priority.Set {
		priority = *body.Priority.Value
	}
	if priority < 1 || priority > 10000 {
		writeInferenceInvalid(c, "priority must be between 1 and 10000")
		return
	}
	var rateLimits inferenceAccessPolicyRateLimitsJSON
	if body.RateLimits.Set {
		rateLimits = *body.RateLimits.Value
	}
	var concurrency inferenceAccessPolicyConcurrencyJSON
	if body.Concurrency.Set {
		concurrency = *body.Concurrency.Value
	}
	if err := validatePolicyLimitPointers(&rateLimits, &concurrency); err != nil {
		writeInferenceInvalid(c, err.Error())
		return
	}
	leaseTTL := int32(60)
	if concurrency.LeaseTTLSeconds.Set {
		leaseTTL = *concurrency.LeaseTTLSeconds.Value
	}
	req := &inferencecontrolv1.CreateInferenceAccessPolicyRequest{IdempotencyKey: strings.TrimSpace(body.IdempotencyKey), Policy: &inferencecontrolv1.InferenceAccessPolicy{Name: strings.TrimSpace(body.Name), Description: strings.TrimSpace(body.Description), Status: status, Priority: priority, Scope: &inferencecontrolv1.InferenceAccessPolicyScope{Type: body.Scope.Type, InferenceServiceIds: body.Scope.InferenceServiceIDs, ApiKeyIds: body.Scope.APIKeyIDs}, Access: &inferencecontrolv1.InferenceAccessPolicyAccess{AllowAllTenantKeys: body.Access.AllowAllTenantKeys, AllowApiKeyIds: body.Access.AllowAPIKeyIDs, DenyApiKeyIds: body.Access.DenyAPIKeyIDs}, RateLimits: &inferencecontrolv1.InferenceAccessPolicyRateLimits{Qps: optionalInt32Value(rateLimits.QPS), Rpm: optionalInt32Value(rateLimits.RPM)}, Concurrency: &inferencecontrolv1.InferenceAccessPolicyConcurrency{MaxInFlight: optionalInt32Value(concurrency.MaxInFlight), LeaseTtlSeconds: leaseTTL}}}
	resp, err := inferencePolicyClient.CreateInferenceAccessPolicy(ctx, tenant, req)
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, inferenceAccessPolicyJSON(resp))
}

func validatePolicyCreateNonNullableFields(body createInferenceAccessPolicyJSON) error {
	if body.Status.Set && body.Status.Value == nil ||
		body.Priority.Set && body.Priority.Value == nil ||
		body.RateLimits.Set && body.RateLimits.Value == nil ||
		body.Concurrency.Set && body.Concurrency.Value == nil {
		return errors.New("non-nullable policy fields must not be null")
	}
	return nil
}
func getInferencePolicy(ctx context.Context, c *app.RequestContext) {
	if inferencePolicyClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenant, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	resp, err := inferencePolicyClient.GetInferenceAccessPolicy(ctx, tenant, c.Param("policy_id"))
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, inferenceAccessPolicyJSON(resp))
}
func patchInferencePolicy(ctx context.Context, c *app.RequestContext) {
	if inferencePolicyClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenant, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	var body patchInferenceAccessPolicyJSON
	if err := c.BindJSON(&body); err != nil {
		writeInferenceInvalid(c, "invalid inference policy request")
		return
	}
	if strings.TrimSpace(body.IdempotencyKey) == "" || len(body.PrivatePolicy) != 0 {
		writeInferenceInvalid(c, "idempotency_key is required and policy envelope is not accepted")
		return
	}
	if err := validatePolicyPatchNonNullableFields(body); err != nil {
		writeInferenceInvalid(c, err.Error())
		return
	}
	requestHash, err := hashInferencePolicyPatchIntent(c.Request.Body())
	if err != nil {
		writeInferenceInvalid(c, "invalid inference policy request")
		return
	}
	current, err := inferencePolicyClient.GetInferenceAccessPolicy(ctx, tenant, c.Param("policy_id"))
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	if current == nil {
		writeInferenceUnavailable(c)
		return
	}
	if err := validatePolicyLimitPointers(body.RateLimits.Value, body.Concurrency.Value); err != nil {
		writeInferenceInvalid(c, err.Error())
		return
	}
	policy := proto.Clone(current).(*inferencecontrolv1.InferenceAccessPolicy)
	if body.Name.Set {
		policy.Name = strings.TrimSpace(*body.Name.Value)
		if policy.Name == "" {
			writeInferenceInvalid(c, "name must not be empty")
			return
		}
	}
	if len(body.Description) != 0 {
		if string(body.Description) == "null" {
			policy.Description = ""
		} else if err := json.Unmarshal(body.Description, &policy.Description); err != nil {
			writeInferenceInvalid(c, "description must be a string or null")
			return
		}
	}
	if body.Status.Set {
		policy.Status = strings.TrimSpace(*body.Status.Value)
	}
	if body.Priority.Set {
		if *body.Priority.Value < 1 || *body.Priority.Value > 10000 {
			writeInferenceInvalid(c, "priority must be between 1 and 10000")
			return
		}
		policy.Priority = *body.Priority.Value
	}
	if body.Scope.Set {
		policy.Scope = &inferencecontrolv1.InferenceAccessPolicyScope{Type: body.Scope.Value.Type, InferenceServiceIds: body.Scope.Value.InferenceServiceIDs, ApiKeyIds: body.Scope.Value.APIKeyIDs}
	}
	if body.Access.Set {
		policy.Access = &inferencecontrolv1.InferenceAccessPolicyAccess{AllowAllTenantKeys: body.Access.Value.AllowAllTenantKeys, AllowApiKeyIds: body.Access.Value.AllowAPIKeyIDs, DenyApiKeyIds: body.Access.Value.DenyAPIKeyIDs}
	}
	if body.RateLimits.Set {
		if policy.RateLimits == nil {
			policy.RateLimits = &inferencecontrolv1.InferenceAccessPolicyRateLimits{}
		}
		if body.RateLimits.Value.QPS.Set {
			policy.RateLimits.Qps = optionalInt32Value(body.RateLimits.Value.QPS)
		}
		if body.RateLimits.Value.RPM.Set {
			policy.RateLimits.Rpm = optionalInt32Value(body.RateLimits.Value.RPM)
		}
	}
	if body.Concurrency.Set {
		if policy.Concurrency == nil {
			policy.Concurrency = &inferencecontrolv1.InferenceAccessPolicyConcurrency{LeaseTtlSeconds: 60}
		}
		if body.Concurrency.Value.MaxInFlight.Set {
			policy.Concurrency.MaxInFlight = optionalInt32Value(body.Concurrency.Value.MaxInFlight)
		}
		if body.Concurrency.Value.LeaseTTLSeconds.Set {
			policy.Concurrency.LeaseTtlSeconds = *body.Concurrency.Value.LeaseTTLSeconds.Value
		}
	}
	req := &inferencecontrolv1.PatchInferenceAccessPolicyRequest{IdempotencyKey: strings.TrimSpace(body.IdempotencyKey), Policy: policy, RequestHash: requestHash}
	resp, err := inferencePolicyClient.PatchInferenceAccessPolicy(ctx, tenant, c.Param("policy_id"), req)
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, inferenceAccessPolicyJSON(resp))
}

func validatePolicyPatchNonNullableFields(body patchInferenceAccessPolicyJSON) error {
	if body.Name.Set && body.Name.Value == nil ||
		body.Status.Set && body.Status.Value == nil ||
		body.Priority.Set && body.Priority.Value == nil ||
		body.Scope.Set && body.Scope.Value == nil ||
		body.Access.Set && body.Access.Value == nil ||
		body.RateLimits.Set && body.RateLimits.Value == nil ||
		body.Concurrency.Set && body.Concurrency.Value == nil {
		return errors.New("only description and nullable limit values may be null")
	}
	return nil
}

func hashInferencePolicyPatchIntent(body []byte) (string, error) {
	var intent map[string]any
	if err := json.Unmarshal(body, &intent); err != nil || intent == nil {
		return "", errors.New("PATCH body must be a JSON object")
	}
	delete(intent, "idempotency_key")
	encoded, err := json.Marshal(intent)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}
func deleteInferencePolicy(ctx context.Context, c *app.RequestContext) {
	if inferencePolicyClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenant, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	key := strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
	if key == "" {
		writeInferenceInvalid(c, "Idempotency-Key is required")
		return
	}
	if err := inferencePolicyClient.DeleteInferenceAccessPolicy(ctx, tenant, c.Param("policy_id"), key); err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func listInferenceServicePolicies(ctx context.Context, c *app.RequestContext) {
	if inferencePolicyClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenant, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	resp, err := inferencePolicyClient.ListInferenceServicePolicies(ctx, tenant, c.Param("service_id"))
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, inferenceServicePoliciesJSON(resp))
}
func listInferencePolicyEvents(ctx context.Context, c *app.RequestContext) {
	if inferencePolicyClient == nil {
		writeInferenceUnavailable(c)
		return
	}
	tenant, ok := requireInferenceTenant(c)
	if !ok {
		return
	}
	req := &inferencecontrolv1.ListInferencePolicyEventsRequest{TenantId: tenant, InferenceServiceId: c.Query("inference_service_id"), PolicyId: c.Query("policy_id"), ApiKeyId: c.Query("api_key_id"), Decision: c.Query("decision"), Cursor: c.Query("cursor")}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			writeInferenceInvalid(c, "limit must be between 1 and 200")
			return
		}
		req.Limit = int32(limit)
	}
	resp, err := inferencePolicyClient.ListInferencePolicyEvents(ctx, req)
	if err != nil {
		writeInferenceGRPCError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(resp.GetItems()))
	for _, event := range resp.GetItems() {
		items = append(items, inferenceAccessPolicyEventJSON(event))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "next_cursor": nullableString(resp.GetNextCursor())})
}

func optionalInt32Value(value optionalNullableInt32JSON) int32 {
	if value.Value == nil {
		return 0
	}
	return *value.Value
}

func validatePolicyLimitPointers(rate *inferenceAccessPolicyRateLimitsJSON, concurrency *inferenceAccessPolicyConcurrencyJSON) error {
	values := map[string]optionalNullableInt32JSON{}
	if rate != nil {
		values["qps"], values["rpm"] = rate.QPS, rate.RPM
	}
	if concurrency != nil {
		values["max_in_flight"] = concurrency.MaxInFlight
	}
	for name, value := range values {
		if value.Set && value.Value != nil && *value.Value < 1 {
			return fmt.Errorf("%s must be at least 1", name)
		}
	}
	if concurrency != nil && concurrency.LeaseTTLSeconds.Set {
		if concurrency.LeaseTTLSeconds.Value == nil {
			return errors.New("lease_ttl_seconds must be an integer between 1 and 3600")
		}
		if *concurrency.LeaseTTLSeconds.Value < 1 || *concurrency.LeaseTTLSeconds.Value > 3600 {
			return errors.New("lease_ttl_seconds must be between 1 and 3600")
		}
	}
	return nil
}

func inferenceServicePoliciesJSON(resp *inferencecontrolv1.InferenceServicePolicies) map[string]any {
	if resp == nil {
		return map[string]any{"service_id": "", "policies": []map[string]any{}}
	}
	policies := make([]map[string]any, 0, len(resp.GetPolicies()))
	for _, policy := range resp.GetPolicies() {
		policies = append(policies, inferenceAccessPolicyJSON(policy))
	}
	return map[string]any{"service_id": resp.GetInferenceServiceId(), "policies": policies}
}

func inferenceAccessPolicyJSON(policy *inferencecontrolv1.InferenceAccessPolicy) map[string]any {
	if policy == nil {
		return map[string]any{}
	}
	scope := map[string]any{"type": policy.GetScope().GetType()}
	if values := policy.GetScope().GetInferenceServiceIds(); len(values) > 0 {
		scope["inference_service_ids"] = values
	}
	if values := policy.GetScope().GetApiKeyIds(); len(values) > 0 {
		scope["api_key_ids"] = values
	}
	access := map[string]any{"allow_all_tenant_keys": policy.GetAccess().GetAllowAllTenantKeys()}
	if values := policy.GetAccess().GetAllowApiKeyIds(); len(values) > 0 {
		access["allow_api_key_ids"] = values
	}
	if values := policy.GetAccess().GetDenyApiKeyIds(); len(values) > 0 {
		access["deny_api_key_ids"] = values
	}
	rateLimits := map[string]any{}
	if value := policy.GetRateLimits().GetQps(); value > 0 {
		rateLimits["qps"] = value
	}
	if value := policy.GetRateLimits().GetRpm(); value > 0 {
		rateLimits["rpm"] = value
	}
	concurrency := map[string]any{}
	if value := policy.GetConcurrency().GetMaxInFlight(); value > 0 {
		concurrency["max_in_flight"] = value
	}
	if value := policy.GetConcurrency().GetLeaseTtlSeconds(); value > 0 {
		concurrency["lease_ttl_seconds"] = value
	}
	result := map[string]any{
		"id": policy.GetId(), "tenant_id": policy.GetTenantId(), "name": policy.GetName(),
		"status": policy.GetStatus(), "priority": policy.GetPriority(), "scope": scope,
		"access": access, "rate_limits": rateLimits, "concurrency": concurrency,
		"created_at": nullableTimestamp(policy.GetCreatedAt()),
	}
	if policy.GetDescription() != "" {
		result["description"] = policy.GetDescription()
	}
	result["updated_at"] = nullableTimestamp(policy.GetUpdatedAt())
	return result
}

func inferenceAccessPolicyEventJSON(event *inferencecontrolv1.InferenceAccessPolicyEvent) map[string]any {
	if event == nil {
		return map[string]any{}
	}
	result := map[string]any{
		"id": event.GetId(), "tenant_id": event.GetTenantId(),
		"inference_service_id": event.GetInferenceServiceId(), "decision": event.GetDecision(),
		"reason_code": event.GetReasonCode(), "http_status": event.GetHttpStatus(),
		"created_at": nullableTimestamp(event.GetCreatedAt()),
	}
	for name, value := range map[string]string{
		"policy_id": event.GetPolicyId(), "api_key_id": event.GetApiKeyId(), "key_prefix": event.GetKeyPrefix(),
		"request_id": event.GetRequestId(), "openai_path": event.GetOpenaiPath(), "external_model": event.GetExternalModel(),
	} {
		if value != "" {
			result[name] = value
		}
	}
	if event.GetRetryAfterSeconds() > 0 {
		result["retry_after_seconds"] = event.GetRetryAfterSeconds()
	}
	return result
}

func nullableTimestamp(value *timestamppb.Timestamp) any {
	if value == nil || !value.IsValid() {
		return nil
	}
	return value.AsTime().UTC().Format(time.RFC3339Nano)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
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
		"invocation_url":       emptyToNil(msg.GetInvocationUrl()),
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
