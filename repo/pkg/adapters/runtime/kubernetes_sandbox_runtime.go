package runtime

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

// KubernetesSandboxRuntime applies sandbox workload objects through Kubernetes
// with runtimeClassName (default sandbox-kata). Code-run, workspace files, and
// preview ports (NodePort Service) are real when apply is enabled; token and
// checkpoint remain on the embedded local session profile.
type KubernetesSandboxRuntime struct {
	local    *LocalSandboxRuntime
	client   *KubernetesRESTClient
	renderer *KubernetesDryRunRenderer
	executor sandboxPodExecutor
	enabled  bool
	now      func() time.Time
}

type KubernetesSandboxRuntimeOption func(*KubernetesSandboxRuntime)

func WithKubernetesSandboxApplyEnabled(enabled bool) KubernetesSandboxRuntimeOption {
	return func(runtime *KubernetesSandboxRuntime) {
		runtime.enabled = enabled
	}
}

func WithKubernetesSandboxClock(now func() time.Time) KubernetesSandboxRuntimeOption {
	return func(runtime *KubernetesSandboxRuntime) {
		if now != nil {
			runtime.now = now
		}
	}
}

func WithKubernetesSandboxLocal(local *LocalSandboxRuntime) KubernetesSandboxRuntimeOption {
	return func(runtime *KubernetesSandboxRuntime) {
		if local != nil {
			runtime.local = local
		}
	}
}

func WithKubernetesSandboxPodExecutor(executor sandboxPodExecutor) KubernetesSandboxRuntimeOption {
	return func(runtime *KubernetesSandboxRuntime) {
		if executor != nil {
			runtime.executor = executor
		}
	}
}

func NewKubernetesSandboxRuntime(client *KubernetesRESTClient, options ...KubernetesSandboxRuntimeOption) *KubernetesSandboxRuntime {
	runtime := &KubernetesSandboxRuntime{
		local:    NewLocalSandboxRuntime(),
		client:   client,
		renderer: NewKubernetesDryRunRenderer(NewPlanningRuntime()),
		executor: newKubectlSandboxPodExecutor(),
		now:      time.Now,
	}
	for _, option := range options {
		option(runtime)
	}
	if runtime.local.now == nil {
		runtime.local.now = runtime.now
	}
	if runtime.enabled {
		runtime.local.codeRunner = runtime.runCodeInPod
		runtime.wireFileBackend()
		runtime.wirePortBackend()
	}
	return runtime
}

func (r *KubernetesSandboxRuntime) Create(ctx context.Context, request ports.SandboxCreateRequest) (ports.SandboxInstanceStatus, error) {
	instance, err := r.local.Create(ctx, request)
	if err != nil {
		return ports.SandboxInstanceStatus{}, err
	}
	if !r.enabled {
		return instance, nil
	}
	oldInstanceID := instance.InstanceID
	instance.InstanceID = uuid.NewString()
	r.local.mu.Lock()
	delete(r.local.instances, sandboxKey(instance.TenantID, oldInstanceID))
	r.local.instances[sandboxKey(instance.TenantID, instance.InstanceID)] = instance
	r.local.mu.Unlock()
	if r.client == nil || r.renderer == nil {
		_ = r.rollbackLocal(ctx, instance)
		return ports.SandboxInstanceStatus{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.Image) == "" {
		_ = r.rollbackLocal(ctx, instance)
		return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: image is required for real sandbox provider", ports.ErrInvalid)
	}

	config := instance.Config
	spec := ports.WorkloadSpec{
		TenantID:                   request.TenantID,
		Name:                       request.Name,
		Kind:                       ports.WorkloadKindSandbox,
		Image:                      request.Image,
		RuntimeClassName:           config.RuntimeClass,
		Sandbox:                    &config,
		SandboxCheckpointSourceRef: request.CheckpointSourceRef,
		Lifecycle:                  ports.InstanceLifecyclePolicy{AutoStart: request.AutoStart},
		Annotations: map[string]string{
			"ani.kubercloud.io/sandbox-instance-id": instance.InstanceID,
			"ani.kubercloud.io/runtime-adapter":     "kubernetes-sandbox-runtime",
		},
	}
	manifests, err := r.renderer.Render(ctx, spec)
	if err != nil {
		_ = r.rollbackLocal(ctx, instance)
		return ports.SandboxInstanceStatus{}, err
	}
	refs, err := r.client.ApplyManifests(ctx, manifests)
	if err != nil {
		_ = r.rollbackLocal(ctx, instance)
		return ports.SandboxInstanceStatus{}, err
	}
	if !request.AutoStart {
		if err := r.scaleDeployment(ctx, request.TenantID, refs, 0); err != nil {
			_ = r.deleteRefs(ctx, request.TenantID, refs)
			_ = r.rollbackLocal(ctx, instance)
			return ports.SandboxInstanceStatus{}, err
		}
	}

	instance.Provider = "kubernetes_sandbox_runtime"
	instance.ResourceRefs = append([]string(nil), refs...)
	instance.DevProfile = ports.DevProfileInfo{
		Mode:         "provider",
		Provider:     "kata-runtimeclass",
		RealProvider: true,
		Reason:       "applied Kubernetes Deployment with RuntimeClass; code-run/files/ports/checkpoint are real-provider; token remains local-session",
	}
	instance.UpdatedAt = firstNonZeroTime(request.CreatedAt, r.now().UTC())
	r.local.upsertInstance(instance)
	return instance, nil
}

func (r *KubernetesSandboxRuntime) Get(ctx context.Context, request ports.SandboxGetRequest) (ports.SandboxInstanceStatus, error) {
	return r.local.Get(ctx, request)
}

func (r *KubernetesSandboxRuntime) List(ctx context.Context, request ports.SandboxListRequest) ([]ports.SandboxInstanceStatus, error) {
	return r.local.List(ctx, request)
}

func (r *KubernetesSandboxRuntime) ApplyLifecycle(ctx context.Context, request ports.SandboxLifecycleRequest) (ports.SandboxInstanceStatus, error) {
	current, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution)
	if err != nil {
		return ports.SandboxInstanceStatus{}, err
	}
	refs := append([]string(nil), current.ResourceRefs...)
	instanceName := current.Name

	if r.enabled && r.client != nil && len(refs) > 0 {
		switch request.Action {
		case ports.WorkloadLifecyclePause:
			if err := r.scaleDeployment(ctx, request.TenantID, refs, 0); err != nil {
				return ports.SandboxInstanceStatus{}, err
			}
		case ports.WorkloadLifecycleResume:
			if err := r.scaleDeployment(ctx, request.TenantID, refs, 1); err != nil {
				return ports.SandboxInstanceStatus{}, err
			}
		case ports.WorkloadLifecycleDelete:
			if err := r.cleanupPortServices(ctx, request.TenantID, instanceName); err != nil {
				return ports.SandboxInstanceStatus{}, err
			}
			if err := r.deleteRefs(ctx, request.TenantID, refs); err != nil {
				return ports.SandboxInstanceStatus{}, err
			}
			if err := r.cleanupWorkspaceCheckpoints(ctx, request.TenantID, request.InstanceID); err != nil {
				return ports.SandboxInstanceStatus{}, err
			}
		}
	}

	instance, err := r.local.ApplyLifecycle(ctx, request)
	if err != nil {
		return ports.SandboxInstanceStatus{}, err
	}
	if request.Action != ports.WorkloadLifecycleDelete && len(refs) > 0 {
		instance.ResourceRefs = refs
		if r.enabled {
			instance.Provider = "kubernetes_sandbox_runtime"
			instance.DevProfile = ports.DevProfileInfo{
				Mode:         "provider",
				Provider:     "kata-runtimeclass",
				RealProvider: true,
				Reason:       "applied Kubernetes Deployment with RuntimeClass; code-run/files/ports/checkpoint are real-provider; token remains local-session",
			}
			r.local.upsertInstance(instance)
		}
	}
	return instance, nil
}

func (r *KubernetesSandboxRuntime) CreateToken(ctx context.Context, request ports.SandboxTokenRequest) (ports.SandboxTokenResult, error) {
	if _, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution); err != nil {
		return ports.SandboxTokenResult{}, err
	}
	return r.local.CreateToken(ctx, request)
}

func (r *KubernetesSandboxRuntime) CreatePort(ctx context.Context, request ports.SandboxPortRequest) (ports.SandboxPortResult, error) {
	if _, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution); err != nil {
		return ports.SandboxPortResult{}, err
	}
	return r.local.CreatePort(ctx, request)
}

func (r *KubernetesSandboxRuntime) DeletePort(ctx context.Context, request ports.SandboxPortDeleteRequest) (ports.SandboxPortResult, error) {
	instance, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution)
	if err != nil {
		return ports.SandboxPortResult{}, err
	}
	if r.enabled {
		if err := validateSandboxPortIdentity(request.TenantID, request.InstanceID, request.IdempotencyKey, request.Port); err != nil {
			return ports.SandboxPortResult{}, err
		}
		if instance.State != ports.SandboxStateRunning {
			return ports.SandboxPortResult{}, fmt.Errorf("%w: sandbox port requires running sandbox", ports.ErrFailedPrecondition)
		}
		return r.closePortService(ctx, request, instance, ports.SandboxPortResult{Port: request.Port})
	}
	return r.local.DeletePort(ctx, request)
}

func (r *KubernetesSandboxRuntime) ListFiles(ctx context.Context, request ports.SandboxFileListRequest) (ports.SandboxFileListResult, error) {
	if _, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution); err != nil {
		return ports.SandboxFileListResult{}, err
	}
	return r.local.ListFiles(ctx, request)
}

func (r *KubernetesSandboxRuntime) WriteFile(ctx context.Context, request ports.SandboxFileWriteRequest) (ports.SandboxFileResult, error) {
	if _, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution); err != nil {
		return ports.SandboxFileResult{}, err
	}
	return r.local.WriteFile(ctx, request)
}

func (r *KubernetesSandboxRuntime) DeleteFile(ctx context.Context, request ports.SandboxFileDeleteRequest) error {
	if _, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution); err != nil {
		return err
	}
	return r.local.DeleteFile(ctx, request)
}

func (r *KubernetesSandboxRuntime) CreateCheckpoint(ctx context.Context, request ports.SandboxCheckpointCreateRequest) (ports.SandboxCheckpointResult, error) {
	instance, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution)
	if err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	if r.enabled {
		if request.KeepMemory {
			return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: sandbox memory checkpoint is not supported", ports.ErrUnsupported)
		}
		if _, ok := sandboxWorkspacePVCRef(instance.ResourceRefs); !ok {
			return ports.SandboxCheckpointResult{}, fmt.Errorf("%w: legacy emptyDir sandbox must be recreated before checkpoint", ports.ErrUnsupported)
		}
		return r.createWorkspaceCheckpoint(ctx, request, instance)
	}
	return r.local.CreateCheckpoint(ctx, request)
}

func (r *KubernetesSandboxRuntime) ListCheckpoints(ctx context.Context, request ports.SandboxCheckpointListRequest) (ports.SandboxCheckpointListResult, error) {
	instance, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution)
	if err != nil {
		return ports.SandboxCheckpointListResult{}, err
	}
	if r.enabled {
		return r.listWorkspaceCheckpoints(ctx, request, instance)
	}
	return r.local.ListCheckpoints(ctx, request)
}

func (r *KubernetesSandboxRuntime) RestoreCheckpoint(ctx context.Context, request ports.SandboxCheckpointRestoreRequest) (ports.SandboxCheckpointResult, error) {
	instance, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution)
	if err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	if r.enabled {
		return r.restoreWorkspaceCheckpoint(ctx, request, instance)
	}
	return r.local.RestoreCheckpoint(ctx, request)
}

func (r *KubernetesSandboxRuntime) CloneCheckpoint(ctx context.Context, request ports.SandboxCheckpointCloneRequest) (ports.SandboxCheckpointResult, error) {
	instance, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution)
	if err != nil {
		return ports.SandboxCheckpointResult{}, err
	}
	if r.enabled {
		return r.cloneWorkspaceCheckpoint(ctx, request, instance)
	}
	return r.local.CloneCheckpoint(ctx, request)
}

func (r *KubernetesSandboxRuntime) CreateCodeRun(ctx context.Context, request ports.SandboxCodeRunRequest) (ports.SandboxCodeRunResult, error) {
	if _, err := r.hydrateExecution(ctx, request.TenantID, request.InstanceID, request.Execution); err != nil {
		return ports.SandboxCodeRunResult{}, err
	}
	if r.enabled && r.local.codeRunner == nil {
		r.local.codeRunner = r.runCodeInPod
	}
	return r.local.CreateCodeRun(ctx, request)
}

func (r *KubernetesSandboxRuntime) hydrateExecution(ctx context.Context, tenantID, instanceID string, execution *ports.SandboxExecutionContext) (ports.SandboxInstanceStatus, error) {
	if execution == nil {
		return r.local.Get(ctx, ports.SandboxGetRequest{TenantID: tenantID, InstanceID: instanceID})
	}
	if execution.TenantID != tenantID || execution.InstanceID != instanceID {
		return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: sandbox execution identity does not match request", ports.ErrFailedPrecondition)
	}
	if r.enabled && execution.Provider != "kubernetes_sandbox_runtime" {
		return ports.SandboxInstanceStatus{}, fmt.Errorf("%w: sandbox provider %q is not kubernetes_sandbox_runtime", ports.ErrFailedPrecondition, execution.Provider)
	}
	instance := ports.SandboxInstanceStatus{
		TenantID: execution.TenantID, InstanceID: execution.InstanceID, Name: execution.Name,
		Kind: ports.WorkloadKindSandbox, Provider: execution.Provider, State: execution.State,
		SessionState: execution.SessionState, Config: execution.Config, TemplateID: execution.Config.TemplateID,
		DevProfile: execution.DevProfile, Ports: append([]ports.SandboxPortResult(nil), execution.Ports...), ResourceRefs: append([]string(nil), execution.ResourceRefs...),
		CreatedAt: execution.CreatedAt, UpdatedAt: execution.UpdatedAt,
	}
	r.local.upsertInstance(instance)
	return instance, nil
}

func (r *KubernetesSandboxRuntime) rollbackLocal(ctx context.Context, instance ports.SandboxInstanceStatus) error {
	_, err := r.local.ApplyLifecycle(ctx, ports.SandboxLifecycleRequest{
		TenantID:    instance.TenantID,
		InstanceID:  instance.InstanceID,
		Action:      ports.WorkloadLifecycleDelete,
		RequestedAt: r.now().UTC(),
	})
	return err
}

func (r *KubernetesSandboxRuntime) deleteRefs(ctx context.Context, tenantID string, refs []string) error {
	namespace := tenantNamespace(tenantID)
	for index := len(refs) - 1; index >= 0; index-- {
		ref := refs[index]
		resource, err := resourceFromRef("", namespace, ref)
		if err != nil {
			return err
		}
		_, status, err := r.client.Do(ctx, http.MethodDelete, r.client.resourceURL(resource, ""), "", nil)
		if err != nil && status != http.StatusNotFound {
			return err
		}
	}
	return nil
}

func (r *KubernetesSandboxRuntime) scaleDeployment(ctx context.Context, tenantID string, refs []string, replicas int) error {
	ref, err := sandboxDeploymentRef(refs)
	if err != nil {
		return err
	}
	resource, err := resourceFromRef("kubernetes", tenantNamespace(tenantID), ref)
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err = r.client.do(ctx, http.MethodPatch, r.client.host+resource.resourcePath()+"/scale", "application/merge-patch+json", []byte(body))
	return err
}

func sandboxDeploymentRef(refs []string) (string, error) {
	for _, ref := range refs {
		resource, err := resourceFromRef("kubernetes", "", ref)
		if err == nil && resource.Kind == "Deployment" {
			return ref, nil
		}
	}
	return "", fmt.Errorf("%w: sandbox Deployment ref is required", ports.ErrInvalid)
}

func sandboxWorkspacePVCRef(refs []string) (string, bool) {
	for _, ref := range refs {
		resource, err := resourceFromRef("kubernetes", "", ref)
		if err == nil && resource.Kind == "PersistentVolumeClaim" {
			return ref, true
		}
	}
	return "", false
}

var _ ports.SandboxRuntime = (*KubernetesSandboxRuntime)(nil)
