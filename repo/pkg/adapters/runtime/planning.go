package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type PlanningRuntime struct {
	gpuInventory ports.GPUInventory
	now          func() time.Time

	mu        sync.RWMutex
	sequence  atomic.Uint64
	instances map[string]ports.WorkloadStatus
}

type PlanningOption func(*PlanningRuntime)

func WithGPUInventory(inventory ports.GPUInventory) PlanningOption {
	return func(runtime *PlanningRuntime) {
		runtime.gpuInventory = inventory
	}
}

func WithClock(now func() time.Time) PlanningOption {
	return func(runtime *PlanningRuntime) {
		if now != nil {
			runtime.now = now
		}
	}
}

func NewPlanningRuntime(options ...PlanningOption) *PlanningRuntime {
	runtime := &PlanningRuntime{
		gpuInventory: ports.GPUInventory(nil),
		now:          time.Now,
		instances:    make(map[string]ports.WorkloadStatus),
	}
	for _, option := range options {
		option(runtime)
	}
	return runtime
}

func (r *PlanningRuntime) Capabilities(context.Context) (ports.WorkloadRuntimeCapabilities, error) {
	return ports.WorkloadRuntimeCapabilities{
		SupportedKinds: []ports.WorkloadKind{
			ports.WorkloadKindVM,
			ports.WorkloadKindContainer,
			ports.WorkloadKindGPUContainer,
			ports.WorkloadKindInference,
			ports.WorkloadKindNotebook,
			ports.WorkloadKindAgentSandbox,
			ports.WorkloadKindBatchJob,
		},
		SupportsGPU:            r.gpuInventory != nil,
		SupportsVM:             true,
		SupportsRuntimeClass:   true,
		SupportsTenantNetwork:  true,
		SupportsInstanceResize: true,
	}, nil
}

func (r *PlanningRuntime) Create(ctx context.Context, spec ports.WorkloadSpec) (ports.WorkloadRef, error) {
	planned, err := r.plan(ctx, spec)
	if err != nil {
		return ports.WorkloadRef{}, err
	}

	sequence := r.sequence.Add(1)
	ref := ports.WorkloadRef{
		TenantID:   spec.TenantID,
		InstanceID: "inst_" + strconv.FormatUint(sequence, 10),
		Kind:       spec.Kind,
		ProviderID: providerID(spec, sequence),
	}
	state := ports.WorkloadStatePending
	if spec.Lifecycle.AutoStart {
		state = ports.WorkloadStateProvisioning
	}

	status := ports.WorkloadStatus{
		Ref:       ref,
		State:     state,
		Endpoint:  endpointFor(spec, ref),
		Networks:  planned.Network.Attachments,
		Storage:   planned.Storage,
		UpdatedAt: r.now().UTC(),
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances[key(ref)] = status
	return ref, nil
}

func (r *PlanningRuntime) Get(_ context.Context, ref ports.WorkloadRef) (ports.WorkloadStatus, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	status, ok := r.instances[key(ref)]
	if !ok {
		return ports.WorkloadStatus{}, ports.ErrNotFound
	}
	return status, nil
}

func (r *PlanningRuntime) ApplyLifecycle(_ context.Context, ref ports.WorkloadRef, action ports.WorkloadLifecycleAction) (ports.WorkloadStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	status, ok := r.instances[key(ref)]
	if !ok {
		return ports.WorkloadStatus{}, ports.ErrNotFound
	}

	next, err := transition(status.State, action)
	if err != nil {
		return ports.WorkloadStatus{}, err
	}
	status.State = next
	status.UpdatedAt = r.now().UTC()
	r.instances[key(ref)] = status
	return status, nil
}

func (r *PlanningRuntime) Delete(ctx context.Context, ref ports.WorkloadRef) error {
	_, err := r.ApplyLifecycle(ctx, ref, ports.WorkloadLifecycleDelete)
	return err
}

func (r *PlanningRuntime) List(_ context.Context, tenantID string, kind ports.WorkloadKind) ([]ports.WorkloadStatus, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var statuses []ports.WorkloadStatus
	for _, status := range r.instances {
		if tenantID != "" && status.Ref.TenantID != tenantID {
			continue
		}
		if kind != "" && status.Ref.Kind != kind {
			continue
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (r *PlanningRuntime) plan(ctx context.Context, spec ports.WorkloadSpec) (ports.WorkloadSpec, error) {
	if strings.TrimSpace(spec.TenantID) == "" {
		return ports.WorkloadSpec{}, fmt.Errorf("%w: tenantID is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(spec.Name) == "" {
		return ports.WorkloadSpec{}, fmt.Errorf("%w: name is required", ports.ErrInvalid)
	}
	if !supportedKind(spec.Kind) {
		return ports.WorkloadSpec{}, fmt.Errorf("%w: unsupported workload kind %q", ports.ErrUnsupported, spec.Kind)
	}

	planned := spec
	planned.Network.Attachments = normalizeNetworkAttachments(spec.Kind, spec.Network.Attachments)
	if err := validateNetworkAttachments(planned.Network.Attachments); err != nil {
		return ports.WorkloadSpec{}, err
	}

	planned.Storage = normalizeStorageAttachments(spec)
	if err := validateStorageAttachments(spec.Kind, planned.Storage); err != nil {
		return ports.WorkloadSpec{}, err
	}

	switch spec.Kind {
	case ports.WorkloadKindVM:
		if spec.VM == nil {
			return ports.WorkloadSpec{}, fmt.Errorf("%w: vm spec is required", ports.ErrInvalid)
		}
		if strings.TrimSpace(spec.VM.BootImage) == "" {
			return ports.WorkloadSpec{}, fmt.Errorf("%w: vm bootImage is required", ports.ErrInvalid)
		}
		if spec.VM.RootDisk.Kind != ports.StorageAttachmentRootDisk || spec.VM.RootDisk.SizeGiB <= 0 {
			return ports.WorkloadSpec{}, fmt.Errorf("%w: vm root disk must be a positive root_disk attachment", ports.ErrInvalid)
		}
	case ports.WorkloadKindContainer, ports.WorkloadKindGPUContainer, ports.WorkloadKindInference, ports.WorkloadKindNotebook, ports.WorkloadKindAgentSandbox, ports.WorkloadKindBatchJob:
		if strings.TrimSpace(spec.Image) == "" {
			return ports.WorkloadSpec{}, fmt.Errorf("%w: image is required for %s", ports.ErrInvalid, spec.Kind)
		}
	}

	if requiresGPU(spec.Kind) {
		if spec.Resources.GPU.RequiredCount <= 0 {
			return ports.WorkloadSpec{}, fmt.Errorf("%w: gpu requiredCount must be positive", ports.ErrInvalid)
		}
		if r.gpuInventory == nil {
			return ports.WorkloadSpec{}, fmt.Errorf("%w: gpu inventory is required for %s", ports.ErrNotConfigured, spec.Kind)
		}
		decision, err := r.gpuInventory.PlanScheduling(ctx, spec.Resources.GPU)
		if err != nil {
			return ports.WorkloadSpec{}, err
		}
		planned.RuntimeClassName = firstNonEmpty(spec.RuntimeClassName, decision.RuntimeClassName)
		planned.SchedulerName = firstNonEmpty(spec.SchedulerName, decision.SchedulerName)
		if planned.Annotations == nil {
			planned.Annotations = map[string]string{}
		}
		planned.Annotations["ani.kubercloud.io/gpu-resource-name"] = decision.ResourceName
		planned.Annotations["ani.kubercloud.io/gpu-resource-quantity"] = firstNonEmpty(decision.ResourceQuantity, strconv.Itoa(spec.Resources.GPU.RequiredCount))
		planned.Annotations["ani.kubercloud.io/gpu-queue"] = decision.QueueName
		if decision.SelectedNodeModel != "" {
			planned.Annotations["ani.kubercloud.io/gpu-selected-model"] = decision.SelectedNodeModel
		}
		if planned.Resources.GPU.Pool == "" {
			planned.Resources.GPU.Pool = decision.QueueName
		}
	}

	return planned, nil
}

func supportedKind(kind ports.WorkloadKind) bool {
	switch kind {
	case ports.WorkloadKindVM,
		ports.WorkloadKindContainer,
		ports.WorkloadKindGPUContainer,
		ports.WorkloadKindInference,
		ports.WorkloadKindNotebook,
		ports.WorkloadKindAgentSandbox,
		ports.WorkloadKindBatchJob:
		return true
	default:
		return false
	}
}

func requiresGPU(kind ports.WorkloadKind) bool {
	return kind == ports.WorkloadKindGPUContainer || kind == ports.WorkloadKindInference
}

func normalizeNetworkAttachments(kind ports.WorkloadKind, attachments []ports.WorkloadNetworkAttachment) []ports.WorkloadNetworkAttachment {
	if len(attachments) > 0 {
		return attachments
	}

	switch kind {
	case ports.WorkloadKindVM:
		return []ports.WorkloadNetworkAttachment{
			requiredPlane(ports.NetworkPlaneTenantVPC, true),
			requiredPlane(ports.NetworkPlaneFoundationMesh, false),
			requiredPlane(ports.NetworkPlaneManagement, false),
		}
	case ports.WorkloadKindGPUContainer, ports.WorkloadKindInference, ports.WorkloadKindNotebook, ports.WorkloadKindBatchJob:
		return []ports.WorkloadNetworkAttachment{
			requiredPlane(ports.NetworkPlaneTenantVPC, true),
			requiredPlane(ports.NetworkPlaneFoundationMesh, false),
			requiredPlane(ports.NetworkPlaneStorage, false),
		}
	default:
		return []ports.WorkloadNetworkAttachment{
			requiredPlane(ports.NetworkPlaneTenantVPC, true),
			requiredPlane(ports.NetworkPlaneFoundationMesh, false),
		}
	}
}

func requiredPlane(plane ports.NetworkPlane, primary bool) ports.WorkloadNetworkAttachment {
	return ports.WorkloadNetworkAttachment{
		Plane:     plane,
		NetworkID: string(plane),
		Primary:   primary,
		Required:  true,
	}
}

func validateNetworkAttachments(attachments []ports.WorkloadNetworkAttachment) error {
	if len(attachments) == 0 {
		return fmt.Errorf("%w: at least one network attachment is required", ports.ErrInvalid)
	}

	primaryCount := 0
	seen := map[ports.NetworkPlane]bool{}
	for _, attachment := range attachments {
		if attachment.Plane == "" {
			return fmt.Errorf("%w: network attachment plane is required", ports.ErrInvalid)
		}
		if attachment.Required && strings.TrimSpace(attachment.NetworkID) == "" {
			return fmt.Errorf("%w: required network %s must include networkID", ports.ErrInvalid, attachment.Plane)
		}
		if attachment.Primary {
			primaryCount++
		}
		seen[attachment.Plane] = true
	}
	if primaryCount != 1 {
		return fmt.Errorf("%w: exactly one primary network attachment is required", ports.ErrInvalid)
	}
	if !seen[ports.NetworkPlaneTenantVPC] {
		return fmt.Errorf("%w: tenant_vpc attachment is required for business connectivity", ports.ErrInvalid)
	}
	return nil
}

func normalizeStorageAttachments(spec ports.WorkloadSpec) []ports.WorkloadStorageAttachment {
	if len(spec.Storage) > 0 {
		return spec.Storage
	}
	if spec.VM != nil {
		storage := []ports.WorkloadStorageAttachment{spec.VM.RootDisk}
		storage = append(storage, spec.VM.DataDisks...)
		return storage
	}
	if spec.Container != nil && len(spec.Container.Volumes) > 0 {
		return spec.Container.Volumes
	}
	if spec.Resources.StorageGiB > 0 {
		return []ports.WorkloadStorageAttachment{{
			Name:         "default",
			Kind:         ports.StorageAttachmentEphemeral,
			MountPath:    "/workspace",
			SizeGiB:      spec.Resources.StorageGiB,
			StorageClass: spec.Resources.StorageClass,
			Required:     true,
		}}
	}
	return nil
}

func validateStorageAttachments(kind ports.WorkloadKind, attachments []ports.WorkloadStorageAttachment) error {
	if kind == ports.WorkloadKindVM && len(attachments) == 0 {
		return fmt.Errorf("%w: vm requires root disk storage", ports.ErrInvalid)
	}

	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.Name) == "" {
			return fmt.Errorf("%w: storage attachment name is required", ports.ErrInvalid)
		}
		if attachment.Kind == "" {
			return fmt.Errorf("%w: storage attachment kind is required", ports.ErrInvalid)
		}
		if attachment.SizeGiB < 0 {
			return fmt.Errorf("%w: storage attachment size cannot be negative", ports.ErrInvalid)
		}
		if attachment.Kind == ports.StorageAttachmentObjectFuse && strings.TrimSpace(attachment.SourceRef) == "" {
			return fmt.Errorf("%w: object_fuse storage requires sourceRef", ports.ErrInvalid)
		}
	}
	return nil
}

func transition(state ports.WorkloadState, action ports.WorkloadLifecycleAction) (ports.WorkloadState, error) {
	switch action {
	case ports.WorkloadLifecycleStart:
		if state == ports.WorkloadStateDeleted || state == ports.WorkloadStateDeleting {
			return "", fmt.Errorf("%w: cannot start deleted instance", ports.ErrConflict)
		}
		return ports.WorkloadStateRunning, nil
	case ports.WorkloadLifecycleStop:
		if state == ports.WorkloadStateDeleted || state == ports.WorkloadStateDeleting {
			return "", fmt.Errorf("%w: cannot stop deleted instance", ports.ErrConflict)
		}
		return ports.WorkloadStateStopped, nil
	case ports.WorkloadLifecycleRestart:
		if state != ports.WorkloadStateRunning {
			return "", fmt.Errorf("%w: restart requires running instance", ports.ErrConflict)
		}
		return ports.WorkloadStateRunning, nil
	case ports.WorkloadLifecycleResize:
		if state == ports.WorkloadStateDeleted || state == ports.WorkloadStateDeleting {
			return "", fmt.Errorf("%w: cannot resize deleted instance", ports.ErrConflict)
		}
		return state, nil
	case ports.WorkloadLifecycleRebuild:
		if state == ports.WorkloadStateDeleted || state == ports.WorkloadStateDeleting {
			return "", fmt.Errorf("%w: cannot rebuild deleted instance", ports.ErrConflict)
		}
		return ports.WorkloadStateProvisioning, nil
	case ports.WorkloadLifecycleDelete:
		return ports.WorkloadStateDeleted, nil
	case ports.WorkloadLifecycleSnapshot, ports.WorkloadLifecycleAttachVolume, ports.WorkloadLifecycleDetachVolume:
		if state == ports.WorkloadStateDeleted || state == ports.WorkloadStateDeleting {
			return "", fmt.Errorf("%w: cannot %s deleted instance", ports.ErrConflict, action)
		}
		return state, nil
	case ports.WorkloadLifecycleRollback:
		if state == ports.WorkloadStateDeleted || state == ports.WorkloadStateDeleting {
			return "", fmt.Errorf("%w: cannot rollback deleted instance", ports.ErrConflict)
		}
		return ports.WorkloadStateRunning, nil
	case ports.WorkloadLifecycleCreate:
		return state, nil
	default:
		return "", fmt.Errorf("%w: unsupported lifecycle action %q", ports.ErrUnsupported, action)
	}
}

func providerID(spec ports.WorkloadSpec, sequence uint64) string {
	return fmt.Sprintf("planning/%s/%s/%d", spec.Kind, spec.TenantID, sequence)
}

func endpointFor(spec ports.WorkloadSpec, ref ports.WorkloadRef) string {
	if spec.Network.AllowIngressFromGateway || spec.Kind == ports.WorkloadKindInference || spec.Kind == ports.WorkloadKindNotebook {
		return "/instances/" + ref.InstanceID
	}
	return ""
}

func key(ref ports.WorkloadRef) string {
	return ref.TenantID + "/" + string(ref.Kind) + "/" + ref.InstanceID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var (
	_ ports.WorkloadRuntime = (*PlanningRuntime)(nil)
)
