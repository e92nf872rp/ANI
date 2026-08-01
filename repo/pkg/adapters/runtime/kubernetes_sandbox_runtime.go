package runtime

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// KubernetesSandboxRuntime applies sandbox workload objects through Kubernetes
// with runtimeClassName (default sandbox-kata). Token/port/file/checkpoint remain
// on the embedded local session profile; code-run executes inside the ready Pod
// when apply is enabled.
type KubernetesSandboxRuntime struct {
	local    *LocalSandboxRuntime
	client   *KubernetesRESTClient
	renderer *KubernetesDryRunRenderer
	executor sandboxPodExecutor
	enabled  bool
	now      func() time.Time
	mu       sync.Mutex
	refs     map[string][]string
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
		refs:     make(map[string][]string),
	}
	for _, option := range options {
		option(runtime)
	}
	if runtime.local.now == nil {
		runtime.local.now = runtime.now
	}
	if runtime.enabled {
		runtime.local.codeRunner = runtime.runCodeInPod
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
		TenantID:         request.TenantID,
		Name:             request.Name,
		Kind:             ports.WorkloadKindSandbox,
		Image:            request.Image,
		RuntimeClassName: config.RuntimeClass,
		Sandbox:          &config,
		Lifecycle:        ports.InstanceLifecyclePolicy{AutoStart: request.AutoStart},
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
		Reason:       "applied Kubernetes Deployment with RuntimeClass; code-run executes in Pod; token/port/file/checkpoint remain local-session",
	}
	instance.UpdatedAt = firstNonZeroTime(request.CreatedAt, r.now().UTC())
	r.local.upsertInstance(instance)
	r.mu.Lock()
	r.refs[sandboxKey(instance.TenantID, instance.InstanceID)] = append([]string(nil), refs...)
	r.mu.Unlock()
	return instance, nil
}

func (r *KubernetesSandboxRuntime) Get(ctx context.Context, request ports.SandboxGetRequest) (ports.SandboxInstanceStatus, error) {
	return r.local.Get(ctx, request)
}

func (r *KubernetesSandboxRuntime) List(ctx context.Context, request ports.SandboxListRequest) ([]ports.SandboxInstanceStatus, error) {
	return r.local.List(ctx, request)
}

func (r *KubernetesSandboxRuntime) ApplyLifecycle(ctx context.Context, request ports.SandboxLifecycleRequest) (ports.SandboxInstanceStatus, error) {
	key := sandboxKey(request.TenantID, request.InstanceID)
	r.mu.Lock()
	refs := append([]string(nil), r.refs[key]...)
	r.mu.Unlock()
	if len(refs) == 0 {
		if current, err := r.local.Get(ctx, ports.SandboxGetRequest{TenantID: request.TenantID, InstanceID: request.InstanceID}); err == nil {
			refs = append([]string(nil), current.ResourceRefs...)
		}
	}

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
			if err := r.deleteRefs(ctx, request.TenantID, refs); err != nil {
				return ports.SandboxInstanceStatus{}, err
			}
			r.mu.Lock()
			delete(r.refs, key)
			r.mu.Unlock()
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
				Reason:       "applied Kubernetes Deployment with RuntimeClass; code-run executes in Pod; token/port/file/checkpoint remain local-session",
			}
			r.local.upsertInstance(instance)
		}
	}
	return instance, nil
}

func (r *KubernetesSandboxRuntime) CreateToken(ctx context.Context, request ports.SandboxTokenRequest) (ports.SandboxTokenResult, error) {
	return r.local.CreateToken(ctx, request)
}

func (r *KubernetesSandboxRuntime) CreatePort(ctx context.Context, request ports.SandboxPortRequest) (ports.SandboxPortResult, error) {
	return r.local.CreatePort(ctx, request)
}

func (r *KubernetesSandboxRuntime) DeletePort(ctx context.Context, request ports.SandboxPortDeleteRequest) (ports.SandboxPortResult, error) {
	return r.local.DeletePort(ctx, request)
}

func (r *KubernetesSandboxRuntime) ListFiles(ctx context.Context, request ports.SandboxFileListRequest) (ports.SandboxFileListResult, error) {
	return r.local.ListFiles(ctx, request)
}

func (r *KubernetesSandboxRuntime) WriteFile(ctx context.Context, request ports.SandboxFileWriteRequest) (ports.SandboxFileResult, error) {
	return r.local.WriteFile(ctx, request)
}

func (r *KubernetesSandboxRuntime) DeleteFile(ctx context.Context, request ports.SandboxFileDeleteRequest) error {
	return r.local.DeleteFile(ctx, request)
}

func (r *KubernetesSandboxRuntime) CreateCheckpoint(ctx context.Context, request ports.SandboxCheckpointCreateRequest) (ports.SandboxCheckpointResult, error) {
	return r.local.CreateCheckpoint(ctx, request)
}

func (r *KubernetesSandboxRuntime) ListCheckpoints(ctx context.Context, request ports.SandboxCheckpointListRequest) (ports.SandboxCheckpointListResult, error) {
	return r.local.ListCheckpoints(ctx, request)
}

func (r *KubernetesSandboxRuntime) RestoreCheckpoint(ctx context.Context, request ports.SandboxCheckpointRestoreRequest) (ports.SandboxCheckpointResult, error) {
	return r.local.RestoreCheckpoint(ctx, request)
}

func (r *KubernetesSandboxRuntime) CloneCheckpoint(ctx context.Context, request ports.SandboxCheckpointCloneRequest) (ports.SandboxCheckpointResult, error) {
	return r.local.CloneCheckpoint(ctx, request)
}

func (r *KubernetesSandboxRuntime) CreateCodeRun(ctx context.Context, request ports.SandboxCodeRunRequest) (ports.SandboxCodeRunResult, error) {
	if r.enabled && r.local.codeRunner == nil {
		r.local.codeRunner = r.runCodeInPod
	}
	return r.local.CreateCodeRun(ctx, request)
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
	for _, ref := range refs {
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
	if len(refs) == 0 {
		return fmt.Errorf("%w: sandbox resource refs are required for scale", ports.ErrInvalid)
	}
	resource, err := resourceFromRef("kubernetes", tenantNamespace(tenantID), refs[0])
	if err != nil {
		return err
	}
	if !strings.EqualFold(resource.Kind, "Deployment") {
		return fmt.Errorf("%w: sandbox scale requires Deployment ref, got %s", ports.ErrInvalid, resource.Kind)
	}
	body := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err = r.client.do(ctx, http.MethodPatch, r.client.host+resource.resourcePath()+"/scale", "application/merge-patch+json", []byte(body))
	return err
}

var _ ports.SandboxRuntime = (*KubernetesSandboxRuntime)(nil)
