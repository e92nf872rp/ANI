package coresdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/engine"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

// Runtime 用 Core SDK 调 /platform-workloads。Services 禁止 import Core 内部包。
type Runtime struct {
	client      anisdk.Client
	httpClient  *http.Client
	staticToken string
	minter      *Minter // 按租户 mint JWT；没有则用 staticToken
}

func New(baseURL, token string) *Runtime {
	return &Runtime{
		client:      anisdk.NewClient(strings.TrimRight(baseURL, "/"), ""),
		staticToken: strings.TrimSpace(token),
	}
}

func (r *Runtime) WithMinter(minter *Minter) *Runtime {
	r.minter = minter
	return r
}

// Ensure 无 RuntimeRef 则 POST /platform-workloads；已有则 PATCH replicas。
func (r *Runtime) Ensure(ctx context.Context, request runtime.EnsureRequest) (runtime.Observation, error) {
	if request.RuntimeRef != uuid.Nil {
		_, err := r.request(ctx, request.TenantID, "PATCH", "/platform-workloads/"+request.RuntimeRef.String(), anisdk.RequestOptions{
			Body: map[string]any{"idempotency_key": request.IdempotencyKey.String(), "replicas": request.Spec.Replicas},
		})
		if err != nil {
			return runtime.Observation{}, err
		}
		return r.Observe(ctx, runtime.RuntimeIdentity{TenantID: request.TenantID, ServiceID: request.ServiceID, RuntimeRef: request.RuntimeRef})
	}
	plan, err := r.plan(ctx, request.TenantID, request.Spec)
	if err != nil {
		return runtime.Observation{}, err
	}
	body := createBody(request, plan)
	payload, err := r.request(ctx, request.TenantID, "POST", "/platform-workloads", anisdk.RequestOptions{Body: body})
	if err != nil {
		return runtime.Observation{}, err
	}
	workloadID, err := uuidFromAny(payload["resource_id"])
	if err != nil {
		return runtime.Observation{}, err
	}
	observed, err := r.Observe(ctx, runtime.RuntimeIdentity{TenantID: request.TenantID, ServiceID: request.ServiceID, RuntimeRef: workloadID})
	if err != nil {
		return runtime.Observation{RuntimeRef: workloadID}, err
	}
	return observed, nil
}

// Observe 只读 GET platform-workload，不创建。
func (r *Runtime) Observe(ctx context.Context, identity runtime.RuntimeIdentity) (runtime.Observation, error) {
	if identity.RuntimeRef == uuid.Nil {
		return runtime.Observation{}, runtime.ErrRuntimeNotFound
	}
	payload, err := r.request(ctx, identity.TenantID, "GET", "/platform-workloads/"+identity.RuntimeRef.String(), anisdk.RequestOptions{})
	if err != nil {
		return runtime.Observation{}, err
	}
	return observationFromWorkload(payload)
}

// ApplyLifecycle 调 Core /lifecycle：start/stop/restart。
func (r *Runtime) ApplyLifecycle(ctx context.Context, request runtime.LifecycleRequest) (runtime.Observation, error) {
	if request.RuntimeRef == uuid.Nil {
		return runtime.Observation{}, runtime.ErrRuntimeNotFound
	}
	_, err := r.request(ctx, request.TenantID, "POST", "/platform-workloads/"+request.RuntimeRef.String()+"/lifecycle", anisdk.RequestOptions{
		Body: map[string]any{"idempotency_key": request.IdempotencyKey.String(), "action": string(request.Action)},
	})
	if err != nil {
		return runtime.Observation{}, err
	}
	if request.Action == domain.ActionStop {
		observed, observeErr := r.Observe(ctx, runtime.RuntimeIdentity{TenantID: request.TenantID, ServiceID: request.ServiceID, RuntimeRef: request.RuntimeRef})
		if observeErr != nil {
			return runtime.Observation{}, observeErr
		}
		return observed, nil
	}
	return r.Observe(ctx, runtime.RuntimeIdentity{TenantID: request.TenantID, ServiceID: request.ServiceID, RuntimeRef: request.RuntimeRef})
}

// Delete 调 Core DELETE /platform-workloads/{id}。
func (r *Runtime) Delete(ctx context.Context, request runtime.DeleteRequest) error {
	if request.RuntimeRef == uuid.Nil {
		return runtime.ErrRuntimeNotFound
	}
	_, err := r.request(ctx, request.TenantID, "DELETE", "/platform-workloads/"+request.RuntimeRef.String(), anisdk.RequestOptions{
		Headers: map[string]string{"Idempotency-Key": request.IdempotencyKey.String()},
	})
	return err
}

// Health GET 引擎 /health。
func (r *Runtime) Health(ctx context.Context, tenantID, runtimeRef uuid.UUID) error {
	endpoint, err := r.runtimeEndpoint(ctx, tenantID, runtimeRef)
	if err != nil {
		return err
	}
	return probeHealth(ctx, r.http(), endpoint)
}

// Smoke 对 ClusterIP 发有界 Chat Completions 或 Embeddings 探活。
func (r *Runtime) Smoke(ctx context.Context, tenantID, runtimeRef uuid.UUID, servedModelName string, task domain.InferenceTask) error {
	endpoint, err := r.runtimeEndpoint(ctx, tenantID, runtimeRef)
	if err != nil {
		return err
	}
	return probeSmoke(ctx, r.http(), endpoint, servedModelName, task)
}

func (r *Runtime) runtimeEndpoint(ctx context.Context, tenantID, runtimeRef uuid.UUID) (string, error) {
	observed, err := r.Observe(ctx, runtime.RuntimeIdentity{TenantID: tenantID, RuntimeRef: runtimeRef})
	if err != nil {
		return "", err
	}
	if !observed.Ready || observed.RuntimeEndpoint == "" {
		return "", fmt.Errorf("runtime endpoint is not ready")
	}
	return observed.RuntimeEndpoint, nil
}

func (r *Runtime) http() *http.Client {
	if r != nil && r.httpClient != nil {
		return r.httpClient
	}
	if client := kubeHTTPClient(); client != nil {
		return client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

// Admit 写库前问 Core 容量/拓扑。CPU 单节点直接通过。
func (r *Runtime) Admit(ctx context.Context, tenantID uuid.UUID, spec domain.Spec) error {
	if spec.Accelerator == nil && spec.PlacementMode != "multi_node" {
		return nil
	}
	_, err := r.plan(ctx, tenantID, spec)
	return err
}

func (r *Runtime) plan(ctx context.Context, tenantID uuid.UUID, spec domain.Spec) (runtime.TopologyPlan, error) {
	if spec.Accelerator == nil && spec.PlacementMode != "multi_node" {
		return runtime.PlanTopology(spec, runtime.CapabilityView{})
	}
	payload, err := r.request(ctx, tenantID, "GET", "/platform-workload-capabilities", anisdk.RequestOptions{})
	if err != nil {
		return runtime.TopologyPlan{}, err
	}
	return runtime.PlanTopology(spec, capabilityViewFromPayload(payload))
}

func (r *Runtime) Logs(ctx context.Context, query runtime.LogQuery) (runtime.LogPage, error) {
	if query.RuntimeRef == uuid.Nil {
		return runtime.LogPage{}, runtime.ErrRuntimeNotFound
	}
	params := map[string]string{}
	if query.Limit > 0 {
		params["limit"] = strconv.Itoa(query.Limit)
	}
	if query.Cursor != "" {
		params["cursor"] = query.Cursor
	}
	if query.Level != "" {
		params["level"] = query.Level
	}
	payload, err := r.request(ctx, query.TenantID, "GET", "/platform-workloads/"+query.RuntimeRef.String()+"/logs", anisdk.RequestOptions{Params: params})
	if err != nil {
		return runtime.LogPage{}, err
	}
	return logPageFromPayload(payload), nil
}

// request 给 Core OpenAPI 带上租户 JWT。minter 优先于 staticToken。
func (r *Runtime) request(ctx context.Context, tenantID uuid.UUID, method, path string, options anisdk.RequestOptions) (map[string]any, error) {
	if options.Headers == nil {
		options.Headers = map[string]string{}
	}
	token := r.staticToken
	if r.minter != nil {
		minted, err := r.minter.Token(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		token = minted
	}
	if looksLikeJWT(token) {
		options.Headers["Authorization"] = "Bearer " + token
	} else {
		if tenantID != uuid.Nil {
			options.Headers["X-Dev-Tenant-ID"] = tenantID.String()
		}
		options.Headers["X-Dev-Principal-Kind"] = "service"
		options.Headers["X-Dev-Service-Scope"] = "scope:platform-workloads:write"
		if token != "" {
			options.Headers["Authorization"] = "Bearer " + token
		}
	}
	if key, _ := options.Body["idempotency_key"].(string); strings.TrimSpace(key) != "" && strings.TrimSpace(options.Headers["Idempotency-Key"]) == "" {
		options.Headers["Idempotency-Key"] = strings.TrimSpace(key)
	}
	var decoded any
	var err error
	for attempt := 0; attempt < idempotencyInProgressAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		decoded, err = r.client.Request(method, "/api/v1"+path, options)
		if err == nil || !isIdempotencyInProgress(err) {
			break
		}
		timer := time.NewTimer(idempotencyInProgressDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if err != nil {
		slog.Error("core sdk request failed", "method", method, "path", path, "err", err)
		return nil, mapCoreError(err)
	}
	payload, _ := decoded.(map[string]any)
	if payload == nil {
		return map[string]any{}, nil
	}
	return payload, nil
}

const (
	idempotencyInProgressAttempts = 40
	idempotencyInProgressDelay    = 250 * time.Millisecond
)

func isIdempotencyInProgress(err error) bool {
	var apiErr anisdk.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "IDEMPOTENCY_IN_PROGRESS"
}

// createBody 组装 Core POST /platform-workloads。Core 只收 image_ref + command/args，不知道 vLLM。
func createBody(request runtime.EnsureRequest, plan runtime.TopologyPlan) map[string]any {
	image := request.Spec.ExecutionProfile.ImageRef
	if image == "" {
		image = "registry.ani.internal/platform/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	cpu, memory := request.Spec.CPU, request.Spec.Memory
	if cpu == "" {
		cpu = "4"
	}
	if memory == "" {
		memory = "16Gi"
	}
	// Engine choice is already frozen on ExecutionProfile.Runtime. Core only
	// stores the resulting image_ref / command / args / pvc artifact.
	command, args := engine.Launch(request.Spec, request.ServedModelName)
	if plan.Mode == "leader_worker" {
		command, args = engine.LaunchLeader(request.Spec, request.ServedModelName)
	}
	resources := map[string]any{"cpu": cpu, "memory": memory}
	if request.Spec.Accelerator != nil {
		resources["accelerator"] = acceleratorBody(request.Spec.Accelerator, request.Spec.Accelerator.CountPerReplica)
	}
	topology := map[string]any{"mode": plan.Mode, "profile_id": plan.ProfileID, "profile_version": plan.ProfileVersion}
	if plan.Mode == "leader_worker" {
		role := func(count, gpus int) map[string]any {
			item := map[string]any{"cpu": cpu, "memory": memory}
			if request.Spec.Accelerator != nil {
				item["accelerator"] = acceleratorBody(request.Spec.Accelerator, gpus)
			}
			return map[string]any{"count": count, "resources": item}
		}
		topology["leader"] = role(plan.LeaderCount, plan.LeaderGPUs)
		topology["workers"] = role(plan.WorkerCount, plan.WorkerGPUs)
	}
	body := map[string]any{
		"idempotency_key": request.IdempotencyKey.String(),
		"name":            request.ServiceID.String(),
		"workload_class":  "inference",
		"runtime_kind":    "container",
		"image_ref":       image,
		"command":         command,
		"args":            args,
		"replicas":        request.Spec.Replicas,
		"resources":       resources,
		"topology":        topology,
		"scheduling":      map[string]any{"queue_class": "inference", "gang": plan.Gang},
		"network":         map[string]any{"exposure": "cluster_internal", "ports": []map[string]any{{"name": "http", "port": 8000}}},
		"health_check":    map[string]any{"protocol": "http", "path": "/health", "port_name": "http"},
		"metadata": map[string]any{
			"owner_ref": request.ServiceID.String(),
			"labels":    map[string]string{"services.ani.io/inference-service-id": request.ServiceID.String()},
		},
	}
	if request.Spec.Engine != nil && len(request.Spec.Engine.Env) > 0 {
		env := make([]map[string]string, 0, len(request.Spec.Engine.Env))
		for _, item := range request.Spec.Engine.Env {
			env = append(env, map[string]string{"name": item.Name, "value": item.Value})
		}
		body["env"] = env
	}
	// Local models are pvc://<claim>. Core mounts that claim at /models; the
	// engine --model path is the #fragment from ArtifactRef, not a subPath.
	if objectRef, _ := engine.Artifact(request.Spec.ExecutionProfile.ArtifactRef); objectRef != "" {
		body["artifacts"] = []map[string]any{{"object_ref": objectRef, "mount_path": "/models"}}
	}
	return body
}

// acceleratorBody 把推理加速器映射到 Core platform-workloads。
// spec_id 是型号；count 是卡数；memory>0 才带上，表示 vGPU 显存（MiB）。
func acceleratorBody(acc *domain.Accelerator, count int) map[string]any {
	if acc == nil {
		return nil
	}
	out := map[string]any{"spec_id": acc.SpecID, "count": count}
	if acc.MemoryMB > 0 {
		out["memory"] = acc.MemoryMB
	}
	return out
}

func probeHealth(ctx context.Context, client *http.Client, endpoint string) error {
	target, err := probeURL(endpoint, "/health")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runtime health returned %d", resp.StatusCode)
	}
	return nil
}

func probeSmoke(ctx context.Context, client *http.Client, endpoint, servedModelName string, task domain.InferenceTask) error {
	task = domain.NormalizeInferenceTask(task)
	path := "/v1/chat/completions"
	model := strings.TrimSpace(servedModelName)
	if model == "" {
		model = "default"
	}
	payloadBody := map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens":  8,
		"temperature": 0,
	}
	if task == domain.InferenceTaskEmbed {
		path = "/v1/embeddings"
		payloadBody = map[string]any{"model": model, "input": []string{"ping"}}
	}
	target, err := probeURL(endpoint, path)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(payloadBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	const maxSmokeResponseBytes = 1 << 20
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxSmokeResponseBytes+1))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runtime smoke returned %d", resp.StatusCode)
	}
	if len(body) > maxSmokeResponseBytes {
		return fmt.Errorf("runtime smoke response exceeds %d bytes", maxSmokeResponseBytes)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("runtime smoke returned a non-json body")
	}
	if task == domain.InferenceTaskEmbed {
		data, ok := decoded["data"].([]any)
		if !ok || len(data) == 0 {
			return fmt.Errorf("runtime embedding smoke missing data")
		}
		return nil
	}
	if _, ok := decoded["choices"]; !ok {
		return fmt.Errorf("runtime smoke missing choices")
	}
	return nil
}

func probeURL(endpoint, path string) (string, error) {
	if via := strings.TrimSpace(os.Getenv("INFERENCE_RUNTIME_PROBE_VIA")); via == "kubernetes_proxy" {
		return kubeProxyURL(endpoint, path)
	}
	return engineURL(endpoint, path)
}

func engineURL(endpoint, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("untrusted runtime endpoint")
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func observationFromWorkload(payload map[string]any) (runtime.Observation, error) {
	id, err := uuidFromAny(payload["id"])
	if err != nil {
		return runtime.Observation{}, runtime.ErrRuntimeNotFound
	}
	endpoint, _ := payload["internal_endpoint"].(string)
	replicas := intFromAny(payload["ready_replicas"])
	return runtime.Observation{
		RuntimeRef:      id,
		RuntimeEndpoint: endpoint,
		ReadyReplicas:   replicas,
		Ready:           fmt.Sprint(payload["state"]) == "running" && replicas > 0 && endpoint != "",
	}, nil
}

func mapCoreError(err error) error {
	if apiErr, ok := err.(anisdk.APIError); ok {
		switch apiErr.Code {
		case "NOT_FOUND":
			return runtime.ErrRuntimeNotFound
		case "CONFLICT":
			return runtime.ErrRuntimeIntentConflict
		case "UNSUPPORTED_TOPOLOGY":
			return runtime.ErrUnsupportedTopology
		case "INSUFFICIENT_CAPACITY":
			return runtime.ErrInsufficientCapacity
		case "PRECONDITION_FAILED", "ACCELERATOR_SPEC_UNAVAILABLE":
			if strings.Contains(strings.ToLower(apiErr.Message), "leader_worker") || strings.Contains(strings.ToLower(apiErr.Message), "topology") {
				return runtime.ErrUnsupportedTopology
			}
			return runtime.ErrRuntimeUnsupported
		case "IMAGE_UNAVAILABLE", "IMAGE_NOT_FOUND", "IMAGE_UNAUTHORIZED":
			return runtime.ErrImageUnavailable
		case "ENGINE_PROFILE_UNAPPROVED":
			return runtime.ErrEngineProfileUnapproved
		case "RESERVED_FIELD_CONFLICT":
			return runtime.ErrReservedFieldConflict
		}
	}
	return err
}

func uuidFromAny(value any) (uuid.UUID, error) {
	text, _ := value.(string)
	return uuid.Parse(text)
}

func looksLikeJWT(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\n") {
		return false
	}
	parts := strings.Split(token, ".")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}

func logPageFromPayload(payload map[string]any) runtime.LogPage {
	rawItems, _ := payload["items"].([]any)
	items := make([]runtime.LogEntry, 0, len(rawItems))
	for _, raw := range rawItems {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		entry := runtime.LogEntry{
			Level:     fmt.Sprint(item["level"]),
			Message:   fmt.Sprint(item["message"]),
			Container: stringFromAny(item["container"]),
			Stream:    stringFromAny(item["stream"]),
		}
		if ts, err := time.Parse(time.RFC3339, fmt.Sprint(item["timestamp"])); err == nil {
			entry.Timestamp = ts.UTC()
		}
		items = append(items, entry)
	}
	return runtime.LogPage{Items: items, NextCursor: stringFromAny(payload["next_cursor"])}
}

func stringFromAny(value any) string {
	text, _ := value.(string)
	return text
}

func capabilityViewFromPayload(payload map[string]any) runtime.CapabilityView {
	view := runtime.CapabilityView{
		LeaderWorkerSetReady: boolFromAny(payload["leader_worker_set_ready"]),
		GangSchedulingReady:  boolFromAny(payload["gang_scheduling_ready"]),
	}
	for _, raw := range anySlice(payload["supported_topology_modes"]) {
		view.SupportedTopologyModes = append(view.SupportedTopologyModes, fmt.Sprint(raw))
	}
	for _, raw := range anySlice(payload["accelerator_specs"]) {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		view.AcceleratorSpecs = append(view.AcceleratorSpecs, runtime.AcceleratorView{
			SpecID:             fmt.Sprint(item["spec_id"]),
			Available:          boolFromAny(item["available"]),
			MaxSingleNodeCount: intFromAny(item["max_single_node_count"]),
		})
	}
	return view
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func boolFromAny(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

var _ runtime.InferenceRuntime = (*Runtime)(nil)
