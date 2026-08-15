package coresdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type Runtime struct {
	client      anisdk.Client
	httpClient  *http.Client
	staticToken string
	minter      *Minter
	tenants     map[uuid.UUID]uuid.UUID
}

func New(baseURL, token string) *Runtime {
	return &Runtime{
		client:      anisdk.NewClient(strings.TrimRight(baseURL, "/"), ""),
		staticToken: strings.TrimSpace(token),
		tenants:     map[uuid.UUID]uuid.UUID{},
	}
}

func (r *Runtime) WithMinter(minter *Minter) *Runtime {
	r.minter = minter
	return r
}

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
	body := createBody(request)
	payload, err := r.request(ctx, request.TenantID, "POST", "/platform-workloads", anisdk.RequestOptions{Body: body})
	if err != nil {
		return runtime.Observation{}, err
	}
	workloadID, err := uuidFromAny(payload["resource_id"])
	if err != nil {
		return runtime.Observation{}, err
	}
	r.tenants[workloadID] = request.TenantID
	observed, err := r.Observe(ctx, runtime.RuntimeIdentity{TenantID: request.TenantID, ServiceID: request.ServiceID, RuntimeRef: workloadID})
	if err != nil {
		return runtime.Observation{RuntimeRef: workloadID}, err
	}
	return observed, nil
}

func (r *Runtime) Observe(ctx context.Context, identity runtime.RuntimeIdentity) (runtime.Observation, error) {
	if identity.RuntimeRef == uuid.Nil {
		return runtime.Observation{}, runtime.ErrRuntimeNotFound
	}
	payload, err := r.request(ctx, identity.TenantID, "GET", "/platform-workloads/"+identity.RuntimeRef.String(), anisdk.RequestOptions{})
	if err != nil {
		return runtime.Observation{}, err
	}
	r.tenants[identity.RuntimeRef] = identity.TenantID
	return observationFromWorkload(payload)
}

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

func (r *Runtime) Delete(ctx context.Context, request runtime.DeleteRequest) error {
	if request.RuntimeRef == uuid.Nil {
		return runtime.ErrRuntimeNotFound
	}
	_, err := r.request(ctx, request.TenantID, "DELETE", "/platform-workloads/"+request.RuntimeRef.String(), anisdk.RequestOptions{
		Headers: map[string]string{"Idempotency-Key": request.IdempotencyKey.String()},
	})
	return err
}

func (r *Runtime) Health(ctx context.Context, runtimeRef uuid.UUID) error {
	endpoint, err := r.runtimeEndpoint(ctx, runtimeRef)
	if err != nil {
		return err
	}
	return probeHealth(ctx, r.http(), endpoint)
}

func (r *Runtime) Smoke(ctx context.Context, runtimeRef uuid.UUID, servedModelName string) error {
	endpoint, err := r.runtimeEndpoint(ctx, runtimeRef)
	if err != nil {
		return err
	}
	return probeSmoke(ctx, r.http(), endpoint, servedModelName)
}

func (r *Runtime) runtimeEndpoint(ctx context.Context, runtimeRef uuid.UUID) (string, error) {
	observed, err := r.Observe(ctx, runtime.RuntimeIdentity{TenantID: r.tenants[runtimeRef], RuntimeRef: runtimeRef})
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
	return &http.Client{Timeout: 15 * time.Second}
}

func (r *Runtime) Admit(ctx context.Context, tenantID uuid.UUID, spec domain.Spec) error {
	if spec.Accelerator == nil {
		return nil
	}
	payload, err := r.request(ctx, tenantID, "GET", "/platform-workload-capabilities", anisdk.RequestOptions{})
	if err != nil {
		return err
	}
	rawSpecs, _ := payload["accelerator_specs"].([]any)
	for _, raw := range rawSpecs {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		if fmt.Sprint(item["spec_id"]) != spec.Accelerator.SpecID {
			continue
		}
		available, _ := item["available"].(bool)
		if available && intFromAny(item["max_single_node_count"]) >= spec.Accelerator.CountPerReplica {
			return nil
		}
	}
	return runtime.ErrRuntimeUnsupported
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
	decoded, err := r.client.Request(method, "/api/v1"+path, options)
	if err != nil {
		return nil, mapCoreError(err)
	}
	payload, _ := decoded.(map[string]any)
	if payload == nil {
		return map[string]any{}, nil
	}
	_ = ctx
	return payload, nil
}

func createBody(request runtime.EnsureRequest) map[string]any {
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
	command, args := engine.Launch(request.Spec, request.ServedModelName)
	resources := map[string]any{"cpu": cpu, "memory": memory}
	if request.Spec.Accelerator != nil {
		resources["accelerator"] = map[string]any{
			"spec_id": request.Spec.Accelerator.SpecID,
			"count":   request.Spec.Accelerator.CountPerReplica,
		}
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
		"topology":        map[string]any{"mode": "single_node", "profile_id": "container-single-node", "profile_version": "v1"},
		"scheduling":      map[string]any{"queue_class": "inference", "gang": false},
		"network":         map[string]any{"exposure": "cluster_internal", "ports": []map[string]any{{"name": "http", "port": 8000}}},
		"health_check":    map[string]any{"protocol": "http", "path": "/health", "port_name": "http"},
		"metadata": map[string]any{
			"owner_ref": request.ServiceID.String(),
			"labels":    map[string]string{"services.ani.io/inference-service-id": request.ServiceID.String()},
		},
	}
	if objectRef, _ := engine.Artifact(request.Spec.ExecutionProfile.ArtifactRef); objectRef != "" {
		body["artifacts"] = []map[string]any{{"object_ref": objectRef, "mount_path": "/models"}}
	}
	return body
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
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runtime health returned %d", resp.StatusCode)
	}
	return nil
}

func probeSmoke(ctx context.Context, client *http.Client, endpoint, servedModelName string) error {
	target, err := probeURL(endpoint, "/v1/chat/completions")
	if err != nil {
		return err
	}
	model := strings.TrimSpace(servedModelName)
	if model == "" {
		model = "default"
	}
	payload, err := json.Marshal(map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens":  8,
		"temperature": 0,
	})
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
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runtime smoke returned %d", resp.StatusCode)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("runtime smoke returned a non-json body")
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
		case "PRECONDITION_FAILED":
			return runtime.ErrRuntimeUnsupported
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
