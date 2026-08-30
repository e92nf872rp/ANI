package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type kubernetesPlatformWorkload struct {
	record  ports.PlatformWorkloadRecord
	spec    ports.PlatformWorkloadCreateSpec
	deleted bool
}

type KubernetesPlatformWorkloadService struct {
	runtime platformWorkloadRuntime
	state   platformWorkloadStore
	mu      sync.Mutex
	now     func() time.Time
}

func NewKubernetesPlatformWorkloadService(runtime platformWorkloadRuntime) *KubernetesPlatformWorkloadService {
	return NewKubernetesPlatformWorkloadServiceWithStore(runtime, newMemoryPlatformWorkloadStore())
}

func NewKubernetesPlatformWorkloadServiceWithStore(runtime platformWorkloadRuntime, store platformWorkloadStore) *KubernetesPlatformWorkloadService {
	if store == nil {
		store = newMemoryPlatformWorkloadStore()
	}
	return &KubernetesPlatformWorkloadService{
		runtime: runtime,
		state:   store,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (s *KubernetesPlatformWorkloadService) Capabilities(ctx context.Context) (ports.PlatformWorkloadCapabilities, error) {
	if source, ok := s.runtime.(platformWorkloadCapabilitySource); ok {
		return source.DiscoverCapabilities(ctx)
	}
	return defaultPlatformWorkloadCapabilities(), nil
}

func (s *KubernetesPlatformWorkloadService) Create(ctx context.Context, tenantID string, spec ports.PlatformWorkloadCreateSpec) (ports.PlatformWorkloadRecord, error) {
	if err := validatePlatformWorkloadCreate(spec); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	caps, err := s.Capabilities(ctx)
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	if err := admitPlatformWorkloadAccelerator(caps, spec.Resources, spec.Topology.Mode); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	if err := admitPlatformWorkloadTopology(caps, spec); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	fingerprint, err := platformWorkloadFingerprint("create", spec)
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	for {
		s.mu.Lock()
		existing, found, err := s.state.intent(tenantID, spec.IdempotencyKey)
		if err != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, err
		}
		if found {
			item, ok, getErr := s.state.getRaw(tenantID, existing.workloadID)
			s.mu.Unlock()
			if getErr != nil {
				return ports.PlatformWorkloadRecord{}, getErr
			}
			if !ok || existing.fingerprint != fingerprint {
				return ports.PlatformWorkloadRecord{}, platformWorkloadIntentConflict()
			}
			if existing.succeeded() {
				return item.record, nil
			}
			return s.finishCreate(ctx, tenantID, item.record.ID, spec.IdempotencyKey, fingerprint, item.spec, false)
		}
		if _, taken := s.state.nameID(tenantID, spec.Name); taken {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: platform workload name already exists", ports.ErrConflict)
		}
		now := s.now()
		id := uuid.NewString()
		if parsed, err := uuid.Parse(strings.TrimSpace(spec.Name)); err == nil {
			id = parsed.String()
		}
		item := kubernetesPlatformWorkload{
			record: ports.PlatformWorkloadRecord{
				ID:                     id,
				TenantID:               tenantID,
				Name:                   spec.Name,
				State:                  ports.PlatformWorkloadProvisioning,
				Generation:             1,
				DesiredReplicas:        spec.Replicas,
				RuntimeShape:           platformWorkloadRuntimeShapeFor(spec),
				TopologyProfileID:      spec.Topology.ProfileID,
				TopologyProfileVersion: spec.Topology.ProfileVersion,
				CreatedAt:              now,
				UpdatedAt:              now,
			},
			spec: spec,
		}
		if err := s.state.putWithIntent(item, spec.IdempotencyKey, pendingIntent(fingerprint, id)); err != nil {
			s.mu.Unlock()
			if errors.Is(err, errPlatformWorkloadIntentReplay) {
				continue
			}
			return ports.PlatformWorkloadRecord{}, err
		}
		s.mu.Unlock()
		return s.finishCreate(ctx, tenantID, id, spec.IdempotencyKey, fingerprint, spec, true)
	}
}

func (s *KubernetesPlatformWorkloadService) finishCreate(ctx context.Context, tenantID, workloadID, idempotencyKey, fingerprint string, spec ports.PlatformWorkloadCreateSpec, rollbackOnFailure bool) (ports.PlatformWorkloadRecord, error) {
	obs, err := s.runtime.Apply(ctx, tenantID, workloadID, spec)
	if err != nil {
		if rollbackOnFailure {
			if delErr := s.runtime.Delete(ctx, tenantID, workloadID, spec); delErr != nil {
				return ports.PlatformWorkloadRecord{}, err
			}
			s.mu.Lock()
			s.state.remove(tenantID, workloadID, spec.Name, idempotencyKey)
			s.mu.Unlock()
		}
		return ports.PlatformWorkloadRecord{}, err
	}
	if observed, observeErr := s.runtime.Observe(ctx, tenantID, workloadID, spec); observeErr == nil {
		obs = observed
	}
	record := s.storeObservation(tenantID, workloadID, obs, "")
	if err := s.markIntentSucceeded(tenantID, idempotencyKey, fingerprint, workloadID); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	return record, nil
}

func (s *KubernetesPlatformWorkloadService) Get(ctx context.Context, tenantID, workloadID string) (ports.PlatformWorkloadRecord, error) {
	s.mu.Lock()
	item, err := s.state.get(tenantID, workloadID)
	if err != nil {
		s.mu.Unlock()
		return ports.PlatformWorkloadRecord{}, err
	}
	if item.record.State == ports.PlatformWorkloadStopped {
		s.mu.Unlock()
		return item.record, nil
	}
	spec := item.spec
	s.mu.Unlock()

	obs, err := s.runtime.Observe(ctx, tenantID, workloadID, spec)
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	return s.storeObservation(tenantID, workloadID, obs, ""), nil
}

func (s *KubernetesPlatformWorkloadService) UpdateReplicas(ctx context.Context, tenantID, workloadID, idempotencyKey string, replicas int) (ports.PlatformWorkloadRecord, error) {
	if replicas < 1 {
		return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: replicas must be at least 1", ports.ErrInvalid)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	fingerprint, err := platformWorkloadFingerprint("scale", map[string]any{"workload_id": workloadID, "replicas": replicas})
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	s.mu.Lock()
	existing, found, err := s.state.intent(tenantID, idempotencyKey)
	if err != nil {
		s.mu.Unlock()
		return ports.PlatformWorkloadRecord{}, err
	}
	var spec ports.PlatformWorkloadCreateSpec
	var stopped bool
	var record ports.PlatformWorkloadRecord
	if found {
		item, ok, getErr := s.state.getRaw(tenantID, existing.workloadID)
		s.mu.Unlock()
		if getErr != nil {
			return ports.PlatformWorkloadRecord{}, getErr
		}
		if !ok || existing.fingerprint != fingerprint || existing.workloadID != workloadID {
			return ports.PlatformWorkloadRecord{}, platformWorkloadIntentConflict()
		}
		if existing.succeeded() {
			return item.record, nil
		}
		spec = item.spec
		stopped = item.record.State == ports.PlatformWorkloadStopped
		record = item.record
	} else {
		item, err := s.state.get(tenantID, workloadID)
		if err != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, err
		}
		if item.spec.Topology.Mode != "single_node" {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: only single_node replicas can be updated", ports.ErrFailedPrecondition)
		}
		item.spec.Replicas = replicas
		item.record.DesiredReplicas = replicas
		item.record.Generation++
		item.record.UpdatedAt = s.now()
		if err := s.state.put(item); err != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, err
		}
		if err := s.state.putIntent(tenantID, idempotencyKey, pendingIntent(fingerprint, workloadID)); err != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, err
		}
		stopped = item.record.State == ports.PlatformWorkloadStopped
		spec = item.spec
		record = item.record
		s.mu.Unlock()
	}
	if stopped {
		if err := s.markIntentSucceeded(tenantID, idempotencyKey, fingerprint, workloadID); err != nil {
			return ports.PlatformWorkloadRecord{}, err
		}
		return record, nil
	}
	if _, err := s.runtime.Apply(ctx, tenantID, workloadID, spec); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	obs, err := s.runtime.Observe(ctx, tenantID, workloadID, spec)
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	updated := s.storeObservation(tenantID, workloadID, obs, "")
	if err := s.markIntentSucceeded(tenantID, idempotencyKey, fingerprint, workloadID); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	return updated, nil
}

func (s *KubernetesPlatformWorkloadService) ApplyLifecycle(ctx context.Context, tenantID, workloadID, idempotencyKey, action string) (ports.PlatformWorkloadRecord, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	switch action {
	case "start", "stop", "restart":
	default:
		return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: unsupported lifecycle action", ports.ErrInvalid)
	}
	fingerprint, err := platformWorkloadFingerprint("lifecycle", map[string]any{"workload_id": workloadID, "action": action})
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	s.mu.Lock()
	existing, found, err := s.state.intent(tenantID, idempotencyKey)
	if err != nil {
		s.mu.Unlock()
		return ports.PlatformWorkloadRecord{}, err
	}
	var spec ports.PlatformWorkloadCreateSpec
	if found {
		item, ok, getErr := s.state.getRaw(tenantID, existing.workloadID)
		s.mu.Unlock()
		if getErr != nil {
			return ports.PlatformWorkloadRecord{}, getErr
		}
		if !ok || existing.fingerprint != fingerprint || existing.workloadID != workloadID {
			return ports.PlatformWorkloadRecord{}, platformWorkloadIntentConflict()
		}
		if existing.succeeded() {
			return item.record, nil
		}
		spec = item.spec
	} else {
		item, err := s.state.get(tenantID, workloadID)
		if err != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, err
		}
		if err := s.state.putIntent(tenantID, idempotencyKey, pendingIntent(fingerprint, workloadID)); err != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, err
		}
		spec = item.spec
		s.mu.Unlock()
	}

	switch action {
	case "stop":
		if err := s.runtime.Delete(ctx, tenantID, workloadID, spec); err != nil {
			return ports.PlatformWorkloadRecord{}, err
		}
		s.mu.Lock()
		item, err := s.state.get(tenantID, workloadID)
		if err != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, err
		}
		item.record.State = ports.PlatformWorkloadStopped
		item.record.ReadyReplicas = 0
		item.record.InternalEndpoint = ""
		item.record.Reason = "stopped"
		item.record.UpdatedAt = s.now()
		if err := s.state.put(item); err != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, err
		}
		s.mu.Unlock()
		if err := s.markIntentSucceeded(tenantID, idempotencyKey, fingerprint, workloadID); err != nil {
			return ports.PlatformWorkloadRecord{}, err
		}
		return item.record, nil
	case "restart":
		if err := s.runtime.Delete(ctx, tenantID, workloadID, spec); err != nil {
			return ports.PlatformWorkloadRecord{}, err
		}
	}
	if _, err := s.runtime.Apply(ctx, tenantID, workloadID, spec); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	obs, err := s.runtime.Observe(ctx, tenantID, workloadID, spec)
	if err != nil {
		obs = platformWorkloadObservation{Endpoint: platformWorkloadEndpoint(tenantID, spec)}
	}
	record := s.storeObservation(tenantID, workloadID, obs, action)
	if err := s.markIntentSucceeded(tenantID, idempotencyKey, fingerprint, workloadID); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	return record, nil
}

func (s *KubernetesPlatformWorkloadService) Delete(ctx context.Context, tenantID, workloadID, idempotencyKey string) (ports.PlatformWorkloadRecord, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: Idempotency-Key is required", ports.ErrInvalid)
	}
	fingerprint, err := platformWorkloadFingerprint("delete", map[string]any{"workload_id": workloadID})
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	s.mu.Lock()
	existing, found, err := s.state.intent(tenantID, idempotencyKey)
	if err != nil {
		s.mu.Unlock()
		return ports.PlatformWorkloadRecord{}, err
	}
	var spec ports.PlatformWorkloadCreateSpec
	if found {
		item, ok, getErr := s.state.getRaw(tenantID, existing.workloadID)
		s.mu.Unlock()
		if getErr != nil {
			return ports.PlatformWorkloadRecord{}, getErr
		}
		if !ok || existing.fingerprint != fingerprint || existing.workloadID != workloadID {
			return ports.PlatformWorkloadRecord{}, platformWorkloadIntentConflict()
		}
		if existing.succeeded() {
			return item.record, nil
		}
		spec = item.spec
	} else {
		item, ok, getErr := s.state.getRaw(tenantID, workloadID)
		if getErr != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, getErr
		}
		if !ok {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, ports.ErrNotFound
		}
		spec = item.spec
		if err := s.state.putIntent(tenantID, idempotencyKey, pendingIntent(fingerprint, workloadID)); err != nil {
			s.mu.Unlock()
			return ports.PlatformWorkloadRecord{}, err
		}
		s.mu.Unlock()
	}

	if err := s.runtime.Delete(ctx, tenantID, workloadID, spec); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}

	s.mu.Lock()
	item, ok, getErr := s.state.getRaw(tenantID, workloadID)
	if getErr != nil {
		s.mu.Unlock()
		return ports.PlatformWorkloadRecord{}, getErr
	}
	if !ok {
		s.mu.Unlock()
		return ports.PlatformWorkloadRecord{}, ports.ErrNotFound
	}
	item.deleted = true
	item.record.State = ports.PlatformWorkloadDeleted
	item.record.ReadyReplicas = 0
	item.record.InternalEndpoint = ""
	item.record.UpdatedAt = s.now()
	if err := s.state.put(item); err != nil {
		s.mu.Unlock()
		return ports.PlatformWorkloadRecord{}, err
	}
	s.state.deleteName(tenantID, item.record.Name)
	s.mu.Unlock()
	if err := s.markIntentSucceeded(tenantID, idempotencyKey, fingerprint, workloadID); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	return item.record, nil
}

func (s *KubernetesPlatformWorkloadService) Logs(ctx context.Context, tenantID, workloadID string, limit int, cursor, level string) (ports.PlatformWorkloadLogList, error) {
	s.mu.Lock()
	item, err := s.state.get(tenantID, workloadID)
	s.mu.Unlock()
	if err != nil {
		return ports.PlatformWorkloadLogList{}, err
	}
	if item.record.State == ports.PlatformWorkloadStopped || item.record.State == ports.PlatformWorkloadDeleted {
		return ports.PlatformWorkloadLogList{Items: []ports.PlatformWorkloadLogEntry{}}, nil
	}
	return s.runtime.Logs(ctx, tenantID, workloadID, item.spec, limit, cursor, level)
}

func (s *KubernetesPlatformWorkloadService) markIntentSucceeded(tenantID, idempotencyKey, fingerprint, workloadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.putIntent(tenantID, idempotencyKey, succeededIntent(fingerprint, workloadID))
}

func (s *KubernetesPlatformWorkloadService) storeObservation(tenantID, workloadID string, obs platformWorkloadObservation, action string) ports.PlatformWorkloadRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok, err := s.state.getRaw(tenantID, workloadID)
	if err != nil || !ok {
		return ports.PlatformWorkloadRecord{}
	}
	item.record.DesiredReplicas = item.spec.Replicas
	item.record.ReadyReplicas = obs.ReadyReplicas
	item.record.InternalEndpoint = obs.Endpoint
	item.record.Reason = obs.Reason
	item.record.UpdatedAt = s.now()
	switch {
	case obs.Reason == "NotFound" && item.record.State == ports.PlatformWorkloadRunning:
		item.record.State = ports.PlatformWorkloadFailed
		item.record.ReadyReplicas = 0
		item.record.InternalEndpoint = ""
		item.record.Message = "provider runtime not found"
	case obs.Ready && obs.ReadyReplicas >= item.spec.Replicas && item.spec.Replicas > 0 && obs.Endpoint != "":
		item.record.State = ports.PlatformWorkloadRunning
		item.record.ObservedGeneration = item.record.Generation
		item.record.Message = ""
	case action == "start" || action == "restart":
		item.record.State = ports.PlatformWorkloadStarting
	case item.record.State == ports.PlatformWorkloadStopped:
	default:
		if item.record.State == "" || item.record.State == ports.PlatformWorkloadPending {
			item.record.State = ports.PlatformWorkloadProvisioning
		}
	}
	_ = s.state.put(item)
	return item.record
}

var _ ports.PlatformWorkloadService = (*KubernetesPlatformWorkloadService)(nil)
