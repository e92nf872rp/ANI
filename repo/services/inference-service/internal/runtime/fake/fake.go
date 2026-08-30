package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	runtimeport "github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

type identityKey struct {
	tenantID  uuid.UUID
	serviceID uuid.UUID
}

type runtimeState struct {
	generation  int64
	spec        domain.Spec
	observation runtimeport.Observation
	logs        []runtimeport.LogEntry
}

type mutationIntent struct {
	generation     int64
	idempotencyKey uuid.UUID
	fingerprint    string
	terminal       bool
}

type Runtime struct {
	mu             sync.Mutex
	runtimes       map[identityKey]runtimeState
	deleted        map[identityKey]int64
	intents        map[identityKey]mutationIntent
	EnsureError    error
	LifecycleError error
	DeleteError    error
	HealthError    error
	SmokeError     error
	EnsureCalls    []runtimeport.EnsureRequest
	LifecycleCalls []runtimeport.LifecycleRequest
	DeleteCalls    []runtimeport.DeleteRequest
}

func New() *Runtime {
	return &Runtime{
		runtimes: make(map[identityKey]runtimeState), deleted: make(map[identityKey]int64),
		intents: make(map[identityKey]mutationIntent),
	}
}

func (r *Runtime) Ensure(_ context.Context, request runtimeport.EnsureRequest) (runtimeport.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.EnsureCalls = append(r.EnsureCalls, request)
	if r.EnsureError != nil {
		return runtimeport.Observation{}, r.EnsureError
	}
	key := identityKey{tenantID: request.TenantID, serviceID: request.ServiceID}
	fingerprint, err := fingerprint("ensure", struct {
		Name            string      `json:"name"`
		ServedModelName string      `json:"served_model_name"`
		Spec            domain.Spec `json:"spec"`
	}{request.Name, request.ServedModelName, request.Spec})
	if err != nil {
		return runtimeport.Observation{}, err
	}
	if _, err := r.acceptIntent(key, request.Generation, request.IdempotencyKey, fingerprint, false); err != nil {
		return runtimeport.Observation{}, err
	}
	if state, ok := r.runtimes[key]; ok {
		if request.Generation == state.generation {
			return state.observation, nil
		}
		state.generation = request.Generation
		state.spec = request.Spec
		state.observation.RuntimeEndpoint = fakeEndpoint(request.ServiceID)
		state.observation.ReadyReplicas = request.Spec.Replicas
		state.observation.Ready = true
		r.runtimes[key] = state
		return state.observation, nil
	}
	observation := runtimeport.Observation{
		RuntimeRef: uuid.New(), RuntimeEndpoint: fakeEndpoint(request.ServiceID),
		ReadyReplicas: request.Spec.Replicas, Ready: true,
	}
	r.runtimes[key] = runtimeState{
		generation: request.Generation, spec: request.Spec, observation: observation,
		logs: []runtimeport.LogEntry{{
			Timestamp: time.Unix(1, 0).UTC(), Level: "info", Message: "runtime accepted",
			Container: "serve", Stream: "stdout",
		}},
	}
	delete(r.deleted, key)
	return observation, nil
}

func (r *Runtime) Observe(_ context.Context, identity runtimeport.RuntimeIdentity) (runtimeport.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.runtimes[identityKey{tenantID: identity.TenantID, serviceID: identity.ServiceID}]
	if !ok || (identity.RuntimeRef != uuid.Nil && state.observation.RuntimeRef != identity.RuntimeRef) {
		return runtimeport.Observation{}, runtimeport.ErrRuntimeNotFound
	}
	return state.observation, nil
}

func (r *Runtime) ApplyLifecycle(_ context.Context, request runtimeport.LifecycleRequest) (runtimeport.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.LifecycleCalls = append(r.LifecycleCalls, request)
	if r.LifecycleError != nil {
		return runtimeport.Observation{}, r.LifecycleError
	}
	key := identityKey{tenantID: request.TenantID, serviceID: request.ServiceID}
	state, ok := r.runtimes[key]
	if !ok {
		if request.Action == domain.ActionStop {
			fingerprint, err := fingerprint("lifecycle", struct {
				RuntimeRef uuid.UUID     `json:"runtime_ref"`
				Action     domain.Action `json:"action"`
			}{request.RuntimeRef, request.Action})
			if err != nil {
				return runtimeport.Observation{}, err
			}
			if _, err := r.acceptIntent(key, request.Generation, request.IdempotencyKey, fingerprint, false); err != nil {
				return runtimeport.Observation{}, err
			}
			return runtimeport.Observation{}, nil
		}
		return runtimeport.Observation{}, runtimeport.ErrRuntimeNotFound
	}
	if request.RuntimeRef != uuid.Nil && state.observation.RuntimeRef != request.RuntimeRef {
		return runtimeport.Observation{}, runtimeport.ErrRuntimeNotFound
	}
	fingerprint, err := fingerprint("lifecycle", struct {
		RuntimeRef uuid.UUID     `json:"runtime_ref"`
		Action     domain.Action `json:"action"`
	}{request.RuntimeRef, request.Action})
	if err != nil {
		return runtimeport.Observation{}, err
	}
	if _, err := r.acceptIntent(key, request.Generation, request.IdempotencyKey, fingerprint, false); err != nil {
		return runtimeport.Observation{}, err
	}
	if request.Generation == state.generation {
		return state.observation, nil
	}
	state.generation = request.Generation
	switch request.Action {
	case domain.ActionStop:
		state.observation.RuntimeEndpoint = ""
		state.observation.ReadyReplicas = 0
		state.observation.Ready = false
	case domain.ActionStart, domain.ActionRestart:
		state.observation.RuntimeEndpoint = fakeEndpoint(request.ServiceID)
		state.observation.ReadyReplicas = state.spec.Replicas
		state.observation.Ready = true
	default:
		return runtimeport.Observation{}, fmt.Errorf("unsupported fake runtime lifecycle action %s", request.Action)
	}
	r.runtimes[key] = state
	return state.observation, nil
}

func (r *Runtime) Delete(_ context.Context, request runtimeport.DeleteRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.DeleteCalls = append(r.DeleteCalls, request)
	if r.DeleteError != nil {
		return r.DeleteError
	}
	key := identityKey{tenantID: request.TenantID, serviceID: request.ServiceID}
	state, ok := r.runtimes[key]
	fingerprint, err := fingerprint("delete", struct {
		RuntimeRef uuid.UUID `json:"runtime_ref"`
	}{request.RuntimeRef})
	if err != nil {
		return err
	}
	if !ok {
		if _, err := r.acceptIntent(key, request.Generation, request.IdempotencyKey, fingerprint, true); err != nil {
			return err
		}
		r.deleted[key] = request.Generation
		return nil
	}
	if request.RuntimeRef != uuid.Nil && state.observation.RuntimeRef != request.RuntimeRef {
		return runtimeport.ErrRuntimeNotFound
	}
	if _, err := r.acceptIntent(key, request.Generation, request.IdempotencyKey, fingerprint, true); err != nil {
		return err
	}
	delete(r.runtimes, key)
	r.deleted[key] = request.Generation
	return nil
}

func (r *Runtime) acceptIntent(key identityKey, generation int64, idempotencyKey uuid.UUID, value string, terminal bool) (bool, error) {
	if idempotencyKey == uuid.Nil {
		return false, runtimeport.ErrRuntimeIntentConflict
	}
	current, ok := r.intents[key]
	if ok {
		if generation < current.generation || (current.terminal && generation > current.generation) {
			return false, runtimeport.ErrStaleRuntimeGeneration
		}
		if generation == current.generation {
			if current.idempotencyKey != idempotencyKey || current.fingerprint != value || current.terminal != terminal {
				return false, runtimeport.ErrRuntimeIntentConflict
			}
			return true, nil
		}
	}
	r.intents[key] = mutationIntent{
		generation: generation, idempotencyKey: idempotencyKey, fingerprint: value, terminal: terminal,
	}
	return false, nil
}

func fingerprint(kind string, value any) (string, error) {
	encoded, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}{kind, value})
	if err != nil {
		return "", fmt.Errorf("marshal fake runtime intent: %w", err)
	}
	return string(encoded), nil
}

func (r *Runtime) Health(context.Context, uuid.UUID, uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.HealthError
}

func (r *Runtime) Smoke(context.Context, uuid.UUID, uuid.UUID, string, domain.InferenceTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.SmokeError
}

func (r *Runtime) Logs(_ context.Context, query runtimeport.LogQuery) (runtimeport.LogPage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.runtimes[identityKey{tenantID: query.TenantID, serviceID: query.ServiceID}]
	if !ok || (query.RuntimeRef != uuid.Nil && state.observation.RuntimeRef != query.RuntimeRef) {
		return runtimeport.LogPage{}, runtimeport.ErrRuntimeNotFound
	}
	return pageFakeLogs(state.logs, query.Limit, query.Cursor, query.Level), nil
}

func pageFakeLogs(items []runtimeport.LogEntry, limit int, cursor, level string) runtimeport.LogPage {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	filtered := make([]runtimeport.LogEntry, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if level != "" && !strings.EqualFold(item.Level, level) {
			continue
		}
		filtered = append(filtered, item)
	}
	start := 0
	if cursor != "" {
		offset, err := strconv.Atoi(cursor)
		if err != nil || offset < 0 {
			return runtimeport.LogPage{Items: []runtimeport.LogEntry{}}
		}
		start = offset
	}
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := runtimeport.LogPage{Items: append([]runtimeport.LogEntry{}, filtered[start:end]...)}
	if end < len(filtered) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page
}

func fakeEndpoint(serviceID uuid.UUID) string {
	return fmt.Sprintf("http://inference-%s.internal.svc:8000", serviceID)
}

var _ runtimeport.InferenceRuntime = (*Runtime)(nil)
