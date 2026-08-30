package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

var (
	platformWorkloadNameRE  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	platformWorkloadImageRE = regexp.MustCompile(`^.+@sha256:[a-f0-9]{64}$`)
)

const (
	platformWorkloadIntentPending   = "pending"
	platformWorkloadIntentSucceeded = "succeeded"
)

type platformWorkloadIntent struct {
	fingerprint string
	workloadID  string
	status      string
}

func pendingIntent(fingerprint, workloadID string) platformWorkloadIntent {
	return platformWorkloadIntent{fingerprint: fingerprint, workloadID: workloadID, status: platformWorkloadIntentPending}
}

func succeededIntent(fingerprint, workloadID string) platformWorkloadIntent {
	return platformWorkloadIntent{fingerprint: fingerprint, workloadID: workloadID, status: platformWorkloadIntentSucceeded}
}

func (i platformWorkloadIntent) succeeded() bool {
	return i.status == platformWorkloadIntentSucceeded
}

type localPlatformWorkload struct {
	record  ports.PlatformWorkloadRecord
	spec    ports.PlatformWorkloadCreateSpec
	deleted bool
}

type LocalPlatformWorkloadService struct {
	mu      sync.Mutex
	now     func() time.Time
	items   map[string]localPlatformWorkload
	intents map[string]platformWorkloadIntent
	names   map[string]string
}

func NewLocalPlatformWorkloadService() *LocalPlatformWorkloadService {
	return &LocalPlatformWorkloadService{
		now:     func() time.Time { return time.Now().UTC() },
		items:   map[string]localPlatformWorkload{},
		intents: map[string]platformWorkloadIntent{},
		names:   map[string]string{},
	}
}

func (s *LocalPlatformWorkloadService) Capabilities(context.Context) (ports.PlatformWorkloadCapabilities, error) {
	return defaultPlatformWorkloadCapabilities(), nil
}

func (s *LocalPlatformWorkloadService) Create(_ context.Context, tenantID string, spec ports.PlatformWorkloadCreateSpec) (ports.PlatformWorkloadRecord, error) {
	if err := validatePlatformWorkloadCreate(spec); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	if err := admitPlatformWorkloadTopology(defaultPlatformWorkloadCapabilities(), spec); err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	fingerprint, err := platformWorkloadFingerprint("create", spec)
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.intents[intentKey(tenantID, spec.IdempotencyKey)]; ok {
		item, found := s.items[existing.workloadID]
		if !found || existing.fingerprint != fingerprint {
			return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: idempotency key reused for a different platform workload", ports.ErrConflict)
		}
		return item.record, nil
	}
	if _, taken := s.names[nameKey(tenantID, spec.Name)]; taken {
		return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: platform workload name already exists", ports.ErrConflict)
	}
	now := s.now()
	id := uuid.NewString()
	record := ports.PlatformWorkloadRecord{
		ID:                     id,
		TenantID:               tenantID,
		Name:                   spec.Name,
		State:                  ports.PlatformWorkloadRunning,
		Generation:             1,
		ObservedGeneration:     1,
		DesiredReplicas:        spec.Replicas,
		ReadyReplicas:          spec.Replicas,
		RuntimeShape:           "deployment",
		TopologyProfileID:      spec.Topology.ProfileID,
		TopologyProfileVersion: spec.Topology.ProfileVersion,
		InternalEndpoint:       localPlatformWorkloadEndpoint(id),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	s.items[id] = localPlatformWorkload{record: record, spec: spec}
	s.intents[intentKey(tenantID, spec.IdempotencyKey)] = platformWorkloadIntent{fingerprint: fingerprint, workloadID: id}
	s.names[nameKey(tenantID, spec.Name)] = id
	return record, nil
}

func (s *LocalPlatformWorkloadService) Get(_ context.Context, tenantID, workloadID string) (ports.PlatformWorkloadRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, err := s.lookup(tenantID, workloadID)
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	return item.record, nil
}

func (s *LocalPlatformWorkloadService) UpdateReplicas(_ context.Context, tenantID, workloadID, idempotencyKey string, replicas int) (ports.PlatformWorkloadRecord, error) {
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
	defer s.mu.Unlock()
	if existing, ok := s.intents[intentKey(tenantID, idempotencyKey)]; ok {
		item, found := s.items[existing.workloadID]
		if !found || existing.fingerprint != fingerprint || existing.workloadID != workloadID {
			return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: idempotency key reused for a different platform workload", ports.ErrConflict)
		}
		return item.record, nil
	}
	item, err := s.lookup(tenantID, workloadID)
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	if item.spec.Topology.Mode != "single_node" {
		return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: only single_node replicas can be updated", ports.ErrFailedPrecondition)
	}
	item.record.DesiredReplicas = replicas
	if item.record.State == ports.PlatformWorkloadRunning {
		item.record.ReadyReplicas = replicas
	}
	item.record.Generation++
	item.record.ObservedGeneration = item.record.Generation
	item.record.UpdatedAt = s.now()
	s.items[workloadID] = item
	s.intents[intentKey(tenantID, idempotencyKey)] = platformWorkloadIntent{fingerprint: fingerprint, workloadID: workloadID}
	return item.record, nil
}

func (s *LocalPlatformWorkloadService) ApplyLifecycle(_ context.Context, tenantID, workloadID, idempotencyKey, action string) (ports.PlatformWorkloadRecord, error) {
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
	defer s.mu.Unlock()
	if existing, ok := s.intents[intentKey(tenantID, idempotencyKey)]; ok {
		item, found := s.items[existing.workloadID]
		if !found || existing.fingerprint != fingerprint || existing.workloadID != workloadID {
			return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: idempotency key reused for a different platform workload", ports.ErrConflict)
		}
		return item.record, nil
	}
	item, err := s.lookup(tenantID, workloadID)
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	switch action {
	case "stop":
		item.record.State = ports.PlatformWorkloadStopped
		item.record.ReadyReplicas = 0
		item.record.InternalEndpoint = ""
	case "start", "restart":
		item.record.State = ports.PlatformWorkloadRunning
		item.record.ReadyReplicas = item.record.DesiredReplicas
		item.record.InternalEndpoint = localPlatformWorkloadEndpoint(item.record.ID)
	}
	item.record.UpdatedAt = s.now()
	s.items[workloadID] = item
	s.intents[intentKey(tenantID, idempotencyKey)] = platformWorkloadIntent{fingerprint: fingerprint, workloadID: workloadID}
	return item.record, nil
}

func (s *LocalPlatformWorkloadService) Delete(_ context.Context, tenantID, workloadID, idempotencyKey string) (ports.PlatformWorkloadRecord, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: Idempotency-Key is required", ports.ErrInvalid)
	}
	fingerprint, err := platformWorkloadFingerprint("delete", map[string]any{"workload_id": workloadID})
	if err != nil {
		return ports.PlatformWorkloadRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.intents[intentKey(tenantID, idempotencyKey)]; ok {
		item, found := s.items[existing.workloadID]
		if !found || existing.fingerprint != fingerprint || existing.workloadID != workloadID {
			return ports.PlatformWorkloadRecord{}, fmt.Errorf("%w: idempotency key reused for a different platform workload", ports.ErrConflict)
		}
		return item.record, nil
	}
	item, ok := s.items[workloadID]
	if !ok || item.record.TenantID != tenantID {
		return ports.PlatformWorkloadRecord{}, ports.ErrNotFound
	}
	item.deleted = true
	item.record.State = ports.PlatformWorkloadDeleted
	item.record.ReadyReplicas = 0
	item.record.InternalEndpoint = ""
	item.record.UpdatedAt = s.now()
	s.items[workloadID] = item
	delete(s.names, nameKey(tenantID, item.record.Name))
	s.intents[intentKey(tenantID, idempotencyKey)] = platformWorkloadIntent{fingerprint: fingerprint, workloadID: workloadID}
	return item.record, nil
}

func (s *LocalPlatformWorkloadService) Logs(_ context.Context, tenantID, workloadID string, _ int, _, _ string) (ports.PlatformWorkloadLogList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.lookup(tenantID, workloadID); err != nil {
		return ports.PlatformWorkloadLogList{}, err
	}
	return ports.PlatformWorkloadLogList{Items: []ports.PlatformWorkloadLogEntry{}}, nil
}

func (s *LocalPlatformWorkloadService) lookup(tenantID, workloadID string) (localPlatformWorkload, error) {
	item, ok := s.items[workloadID]
	if !ok || item.deleted || item.record.TenantID != tenantID {
		return localPlatformWorkload{}, ports.ErrNotFound
	}
	return item, nil
}

func validatePlatformWorkloadCreate(spec ports.PlatformWorkloadCreateSpec) error {
	if _, err := uuid.Parse(strings.TrimSpace(spec.IdempotencyKey)); err != nil {
		return fmt.Errorf("%w: idempotency_key must be a uuid", ports.ErrInvalid)
	}
	if !platformWorkloadNameRE.MatchString(spec.Name) || len(spec.Name) > 63 {
		return fmt.Errorf("%w: name must be a DNS label", ports.ErrInvalid)
	}
	if spec.WorkloadClass != "inference" || spec.RuntimeKind != "container" {
		return fmt.Errorf("%w: only inference container workloads are supported", ports.ErrInvalid)
	}
	if !platformWorkloadImageRE.MatchString(spec.ImageRef) {
		return fmt.Errorf("%w: image_ref must be digest-pinned", ports.ErrInvalid)
	}
	if len(spec.Command) == 0 || spec.Replicas < 1 {
		return fmt.Errorf("%w: command and replicas are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(spec.Resources.CPU) == "" || strings.TrimSpace(spec.Resources.Memory) == "" {
		return fmt.Errorf("%w: cpu and memory are required", ports.ErrInvalid)
	}
	if spec.Resources.AcceleratorSpecID != "" || spec.Resources.AcceleratorCount > 0 {
		if strings.TrimSpace(spec.Resources.AcceleratorSpecID) == "" || spec.Resources.AcceleratorCount < 1 {
			return fmt.Errorf("%w: accelerator requires spec_id and a positive count", ports.ErrInvalid)
		}
	}
	if spec.Resources.AcceleratorMemoryMB < 0 || spec.Topology.Leader.Resources.AcceleratorMemoryMB < 0 || spec.Topology.Workers.Resources.AcceleratorMemoryMB < 0 {
		return fmt.Errorf("%w: accelerator memory must be at least 1 MiB", ports.ErrInvalid)
	}
	if strings.TrimSpace(spec.Topology.ProfileID) == "" || strings.TrimSpace(spec.Topology.ProfileVersion) == "" {
		return fmt.Errorf("%w: topology profile is required", ports.ErrInvalid)
	}
	if spec.Scheduling.QueueClass != "inference" {
		return fmt.Errorf("%w: scheduling must use the inference queue", ports.ErrInvalid)
	}
	if err := validatePlatformWorkloadTopology(spec); err != nil {
		return err
	}
	if spec.Network.Exposure != "cluster_internal" || len(spec.Network.Ports) == 0 {
		return fmt.Errorf("%w: network must be cluster_internal with at least one port", ports.ErrInvalid)
	}
	if spec.HealthCheck.Protocol != "http" || !strings.HasPrefix(spec.HealthCheck.Path, "/") || spec.HealthCheck.PortName == "" {
		return fmt.Errorf("%w: http health_check is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(spec.Metadata.OwnerRef) == "" {
		return fmt.Errorf("%w: metadata.owner_ref is required", ports.ErrInvalid)
	}
	return nil
}

func validatePlatformWorkloadTopology(spec ports.PlatformWorkloadCreateSpec) error {
	switch spec.Topology.Mode {
	case "single_node":
		if spec.Topology.HasLeader || spec.Topology.HasWorkers || spec.Topology.Leader.Count > 0 || spec.Topology.Workers.Count > 0 {
			return fmt.Errorf("%w: single_node topology must not submit leader or workers", ports.ErrInvalid)
		}
		if spec.Scheduling.Gang {
			return fmt.Errorf("%w: single_node scheduling must use inference queue without gang", ports.ErrInvalid)
		}
		return nil
	case "leader_worker":
		if !spec.Topology.HasLeader || !spec.Topology.HasWorkers || spec.Topology.Leader.Count != 1 || spec.Topology.Workers.Count < 1 {
			return fmt.Errorf("%w: leader_worker topology requires leader.count=1 and workers.count>=1", ports.ErrInvalid)
		}
		if spec.Replicas != 1 {
			return fmt.Errorf("%w: leader_worker only admits replicas=1", ports.ErrFailedPrecondition)
		}
		if !spec.Scheduling.Gang {
			return fmt.Errorf("%w: leader_worker scheduling must enable gang", ports.ErrInvalid)
		}
		if strings.TrimSpace(spec.Resources.AcceleratorSpecID) == "" || spec.Resources.AcceleratorCount < 2 {
			return fmt.Errorf("%w: leader_worker requires an accelerator count of at least 2", ports.ErrInvalid)
		}
		leaderGPUs := spec.Topology.Leader.Resources.AcceleratorCount
		if leaderGPUs < 1 {
			leaderGPUs = 1
		}
		workerGPUs := spec.Topology.Workers.Resources.AcceleratorCount
		if workerGPUs < 1 {
			workerGPUs = 1
		}
		if leaderGPUs*spec.Topology.Leader.Count+workerGPUs*spec.Topology.Workers.Count != spec.Resources.AcceleratorCount {
			return fmt.Errorf("%w: leader and worker accelerator counts must equal resources.accelerator.count", ports.ErrInvalid)
		}
		return nil
	default:
		return fmt.Errorf("%w: topology mode must be single_node or leader_worker", ports.ErrInvalid)
	}
}

func admitPlatformWorkloadTopology(caps ports.PlatformWorkloadCapabilities, spec ports.PlatformWorkloadCreateSpec) error {
	if spec.Topology.Mode != "leader_worker" {
		return nil
	}
	if !caps.LeaderWorkerSetReady || !caps.GangSchedulingReady || !platformWorkloadSupportsMode(caps, "leader_worker") {
		return fmt.Errorf("%w: leader_worker topology is not available", ports.ErrFailedPrecondition)
	}
	return nil
}

func admitPlatformWorkloadAccelerator(caps ports.PlatformWorkloadCapabilities, spec ports.PlatformWorkloadResources, topologyMode string) error {
	if strings.TrimSpace(spec.AcceleratorSpecID) == "" && spec.AcceleratorCount == 0 {
		return nil
	}
	want := canonicalAcceleratorSpecID(spec.AcceleratorSpecID)
	for _, item := range caps.AcceleratorSpecs {
		if canonicalAcceleratorSpecID(item.SpecID) != want || !item.Available {
			continue
		}
		capacity := item.MaxWholeCardCount
		if spec.AcceleratorMemoryMB > 0 {
			capacity = item.MaxVGPUCount
		}
		if capacity < 1 {
			continue
		}
		if topologyMode != "leader_worker" && capacity < spec.AcceleratorCount {
			return fmt.Errorf("%w: accelerator spec is not available", ports.ErrFailedPrecondition)
		}
		return nil
	}
	return fmt.Errorf("%w: accelerator spec is not available", ports.ErrFailedPrecondition)
}

func defaultPlatformWorkloadCapabilities() ports.PlatformWorkloadCapabilities {
	return ports.PlatformWorkloadCapabilities{
		SupportedTopologyModes: []string{"single_node"},
		SupportedProfiles: []ports.PlatformWorkloadTopologyProfile{
			{ID: "container-single-node", Version: "v1", Mode: "single_node"},
		},
		AcceleratorSpecs: []ports.PlatformWorkloadAcceleratorCapability{},
	}
}

func platformWorkloadSupportsMode(caps ports.PlatformWorkloadCapabilities, mode string) bool {
	for _, item := range caps.SupportedTopologyModes {
		if item == mode {
			return true
		}
	}
	return false
}

func platformWorkloadRuntimeShapeFor(spec ports.PlatformWorkloadCreateSpec) string {
	if spec.Topology.Mode == "leader_worker" {
		return "leader_worker_set"
	}
	return "deployment"
}

func platformWorkloadFingerprint(kind string, value any) (string, error) {
	encoded, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}{kind, value})
	if err != nil {
		return "", fmt.Errorf("marshal platform workload intent: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func intentKey(tenantID, idempotencyKey string) string {
	return tenantID + "\x00" + idempotencyKey
}

func nameKey(tenantID, name string) string {
	return tenantID + "\x00" + name
}

func localPlatformWorkloadEndpoint(id string) string {
	return "http://platform-workload-" + id + ".ani-internal.svc:8000"
}

var _ ports.PlatformWorkloadService = (*LocalPlatformWorkloadService)(nil)
