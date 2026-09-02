package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/adapters/resilience"
	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesLifecycleExecutor struct {
	client  *KubernetesRESTClient
	enabled bool
	now     func() time.Time
	// translator converts a target GPUSpec spec_id into Volcano scheduling
	// fragments (nodeSelector/schedulerName/resourceRequests/queue annotation)
	// used to rebuild the workload on resize. nil disables spec_id resize.
	translator *VolcanoResourceTranslator
}

type KubernetesLifecycleOption func(*KubernetesLifecycleExecutor)

func WithKubernetesLifecycleEnabled(enabled bool) KubernetesLifecycleOption {
	return func(executor *KubernetesLifecycleExecutor) {
		executor.enabled = enabled
	}
}

func WithKubernetesLifecycleTranslator(translator *VolcanoResourceTranslator) KubernetesLifecycleOption {
	return func(executor *KubernetesLifecycleExecutor) {
		executor.translator = translator
	}
}

func WithKubernetesLifecycleClock(now func() time.Time) KubernetesLifecycleOption {
	return func(executor *KubernetesLifecycleExecutor) {
		if now != nil {
			executor.now = now
		}
	}
}

func NewKubernetesLifecycleExecutor(client *KubernetesRESTClient, options ...KubernetesLifecycleOption) *KubernetesLifecycleExecutor {
	executor := &KubernetesLifecycleExecutor{client: client, now: time.Now}
	for _, option := range options {
		option(executor)
	}
	return executor
}

func (e *KubernetesLifecycleExecutor) Apply(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) (ports.WorkloadInstanceLifecycleResult, error) {
	if err := validateLifecycleExecutionRequest(request, record); err != nil {
		return ports.WorkloadInstanceLifecycleResult{}, err
	}
	if !e.enabled {
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  false,
			Reason:    "kubernetes lifecycle execution is disabled by execution switch",
			CheckedAt: e.now().UTC(),
		}, nil
	}
	if e.client == nil {
		return ports.WorkloadInstanceLifecycleResult{}, ports.ErrNotConfigured
	}

	if request.Action == ports.WorkloadLifecycleDelete {
		if err := e.deleteResources(ctx, record); err != nil {
			return ports.WorkloadInstanceLifecycleResult{}, err
		}
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  true,
			Reason:    "accepted by Kubernetes lifecycle executor",
			CheckedAt: e.now().UTC(),
		}, nil
	}

	if request.Action == ports.WorkloadLifecycleResize {
		if err := e.applyResize(ctx, request, record); err != nil {
			return ports.WorkloadInstanceLifecycleResult{}, err
		}
		return ports.WorkloadInstanceLifecycleResult{
			Action:    request.Action,
			Accepted:  true,
			Reason:    "resized by Kubernetes lifecycle executor (targeted patch)",
			CheckedAt: e.now().UTC(),
		}, nil
	}

	resource, err := resourceFromRecord(record)
	if err != nil {
		return ports.WorkloadInstanceLifecycleResult{}, err
	}
	if err := e.execute(ctx, request.Action, resource, replicasFromRequest(request.Replicas)); err != nil {
		return ports.WorkloadInstanceLifecycleResult{}, err
	}
	return ports.WorkloadInstanceLifecycleResult{
		Action:    request.Action,
		Accepted:  true,
		Reason:    "accepted by Kubernetes lifecycle executor",
		CheckedAt: e.now().UTC(),
	}, nil
}

func (e *KubernetesLifecycleExecutor) deleteResources(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	namespace := tenantNamespace(record.TenantID)
	var deleteErrors []error
	for _, ref := range record.ResourceRefs {
		// Each ref encodes its own provider (kubevirt/... vs kubernetes/Secret/...).
		// Do not force record.Provider onto every ref or mixed-provider cleanup fails.
		resource, err := resourceFromRef("", namespace, ref)
		if err != nil {
			deleteErrors = append(deleteErrors, err)
			continue
		}
		_, status, err := e.client.Do(ctx, http.MethodDelete, e.client.resourceURL(resource, ""), "", nil)
		if err != nil && status != http.StatusNotFound {
			deleteErrors = append(deleteErrors, err)
		}
	}
	return errors.Join(deleteErrors...)
}

func (e *KubernetesLifecycleExecutor) execute(ctx context.Context, action ports.WorkloadLifecycleAction, resource kubernetesResource, replicas int) error {
	switch action {
	case ports.WorkloadLifecycleStart:
		return e.start(ctx, resource)
	case ports.WorkloadLifecycleStop:
		return e.stop(ctx, resource)
	case ports.WorkloadLifecycleRestart:
		return e.restart(ctx, resource)
	case ports.WorkloadLifecycleScale:
		if replicas < 1 {
			return fmt.Errorf("%w: scale replicas must be at least 1, got %d", ports.ErrInvalid, replicas)
		}
		return e.patchScale(ctx, resource, replicas)
	default:
		return fmt.Errorf("%w: unsupported Kubernetes lifecycle action %q", ports.ErrUnsupported, action)
	}
}

// replicasFromRequest returns the target replica count carried on a lifecycle
// request, 0 when unset so callers can validate before applying.
func replicasFromRequest(replicas *int32) int {
	if replicas == nil {
		return 0
	}
	return int(*replicas)
}

func (e *KubernetesLifecycleExecutor) start(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		// KubeVirt VM lifecycle subresources accept PUT with an empty body.
		_, err := e.client.do(ctx, http.MethodPut, e.client.host+kubeVirtVMSubresourcePath(resource.Namespace, resource.Name, "start"), "", nil)
		return ignoreKubeVirtLifecycleConflict(err, "already running")
	}
	return e.patchScale(ctx, resource, 1)
}

func (e *KubernetesLifecycleExecutor) stop(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		// KubeVirt VM lifecycle subresources accept PUT with an empty body.
		_, err := e.client.do(ctx, http.MethodPut, e.client.host+kubeVirtVMSubresourcePath(resource.Namespace, resource.Name, "stop"), "", nil)
		return ignoreKubeVirtLifecycleConflict(err, "not running", "is not running", "halted")
	}
	return e.patchScale(ctx, resource, 0)
}

func ignoreKubeVirtLifecycleConflict(err error, messageSnippets ...string) error {
	if err == nil {
		return nil
	}
	var statusErr *resilience.StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusConflict {
		return err
	}
	body := strings.ToLower(statusErr.Body)
	for _, snippet := range messageSnippets {
		if snippet != "" && strings.Contains(body, strings.ToLower(snippet)) {
			return nil
		}
	}
	return err
}

func (e *KubernetesLifecycleExecutor) restart(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		if err := e.stop(ctx, resource); err != nil {
			return err
		}
		return e.start(ctx, resource)
	}
	body := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"ani.kubercloud.io/restarted-at":%q}}}}}`, e.now().UTC().Format(time.RFC3339))
	_, err := e.client.do(ctx, http.MethodPatch, e.client.resourceURL(resource, ""), "application/merge-patch+json", []byte(body))
	return err
}

// applyResize rerenders the workload in place for a resize action via a
// targeted strategic-merge patch (方案B): it rewrites only the Volcano
// scheduling fragments and container GPU resources triggered by spec_id and
// cpu/memory, leaving env/ports/command and the rest of the Deployment intact.
// The patch content type is strategic-merge so the containers list is keyed by
// name and nested resource maps merge rather than being wholesale replaced.
func (e *KubernetesLifecycleExecutor) applyResize(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) error {
	resource, err := resourceFromRecord(record)
	if err != nil {
		return err
	}
	if resource.Kind == "VirtualMachine" {
		if strings.TrimSpace(request.SpecID) != "" {
			return fmt.Errorf("%w: gpu spec resize is only supported for container and gpu_container instances", ports.ErrUnsupported)
		}
		// VM has no container resources to patch; keep the historical
		// stop+start restart behaviour for cpu/memory-only resize.
		return e.restart(ctx, resource)
	}
	patch, err := e.buildResizePatch(ctx, request, record, resource.Name)
	if err != nil {
		return err
	}
	_, err = e.client.do(ctx, http.MethodPatch, e.client.resourceURL(resource, ""), "application/strategic-merge-patch+json", patch)
	return err
}

// GPU resource keys retained for spec-mode switches. Swapping from vGPU to
// wholecard (or back) must clear the other mode's resource keys, otherwise
// both stale and new GPU resources would be requested simultaneously.
var (
	volcanoVGPUResourceKeys = []string{"volcano.sh/vgpu-number", volcanoVGPUResourceName, "volcano.sh/vgpu-cores"}
	legacyGPUResourceKeys   = []string{"nvidia.com/gpu", "nvidia.com/vgpu"}
)

func (e *KubernetesLifecycleExecutor) buildResizePatch(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord, containerName string) ([]byte, error) {
	specID := strings.TrimSpace(request.SpecID)
	requests := map[string]any{}
	limits := map[string]any{}
	podAnnotations := map[string]string{}
	schedulerName := ""
	var nodeSelector map[string]string

	if specID != "" {
		if e.translator == nil {
			return nil, fmt.Errorf("%w: volcano translator is not configured for spec_id resize", ports.ErrNotConfigured)
		}
		count := record.GPU.Count
		if count < 1 {
			count = 1
		}
		translation, err := e.translator.Translate(ctx, specID, record.GPU.QueueName, count)
		if err != nil {
			return nil, err
		}
		wholecard := false
		for key := range translation.ResourceRequests {
			if strings.EqualFold(key, "nvidia.com/gpu") {
				wholecard = true
			}
		}
		stale := legacyGPUResourceKeys
		if wholecard {
			stale = volcanoVGPUResourceKeys
		}
		for _, key := range stale {
			requests[key] = nil
			limits[key] = nil
		}
		for key, value := range translation.ResourceRequests {
			requests[key] = value
			limits[key] = value
		}
		for key, value := range translation.Annotations {
			podAnnotations[key] = value
		}
		if translation.SchedulerName != "" {
			schedulerName = translation.SchedulerName
			podAnnotations["ani.kubercloud.io/scheduler-name"] = translation.SchedulerName
		}
		if len(translation.NodeSelector) > 0 {
			nodeSelector = translation.NodeSelector
			data, _ := json.Marshal(translation.NodeSelector)
			podAnnotations[volcanoNodeSelectorAnnotation] = string(data)
		}
		if len(translation.ResourceRequests) > 0 {
			data, _ := json.Marshal(translation.ResourceRequests)
			podAnnotations[volcanoResourceRequestAnnotation] = string(data)
		}
	}

	if cpu := strings.TrimSpace(request.Resources.CPU); cpu != "" {
		requests["cpu"] = cpu
		limits["cpu"] = cpu
	}
	if memory := strings.TrimSpace(request.Resources.Memory); memory != "" {
		requests["memory"] = memory
		limits["memory"] = memory
	}

	templateSpec := map[string]any{}
	if schedulerName != "" {
		templateSpec["schedulerName"] = schedulerName
	}
	if len(nodeSelector) > 0 {
		// strategic-merge patches maps by merging, so a spec switch (e.g.
		// vGPU -> wholecard) would leave the other mode's node labels behind.
		// $patch: replace makes the nodeSelector wholesale-replaced instead.
		nodeSelector["$patch"] = "replace"
		templateSpec["nodeSelector"] = nodeSelector
	}
	if len(requests) > 0 || len(limits) > 0 {
		templateSpec["containers"] = []any{
			map[string]any{
				"name": containerName,
				"resources": map[string]any{
					"requests": requests,
					"limits":   limits,
				},
			},
		}
	}
	templateMeta := map[string]any{}
	if len(podAnnotations) > 0 {
		templateMeta["annotations"] = podAnnotations
	}
	if len(templateSpec) == 0 && len(templateMeta) == 0 {
		return nil, fmt.Errorf("%w: resize carries no cpu/memory/spec_id change", ports.ErrInvalid)
	}
	template := map[string]any{}
	if len(templateMeta) > 0 {
		template["metadata"] = templateMeta
	}
	if len(templateSpec) > 0 {
		template["spec"] = templateSpec
	}
	return json.Marshal(map[string]any{"spec": map[string]any{"template": template}})
}

func kubeVirtVMSubresourcePath(namespace string, vmName string, subresource string) string {
	return "/apis/subresources.kubevirt.io/v1/namespaces/" + url.PathEscape(namespace) + "/virtualmachines/" + url.PathEscape(vmName) + "/" + url.PathEscape(subresource)
}

func (e *KubernetesLifecycleExecutor) patchScale(ctx context.Context, resource kubernetesResource, replicas int) error {
	endpoint := e.client.host + resource.resourcePath() + "/scale"
	body := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := e.client.do(ctx, http.MethodPatch, endpoint, "application/merge-patch+json", []byte(body))
	return err
}

func validateLifecycleExecutionRequest(request ports.WorkloadInstanceLifecycleRequest, record ports.WorkloadInstanceRecord) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return fmt.Errorf("%w: tenantID and instanceID are required for lifecycle execution", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.UserID) == "" || strings.TrimSpace(request.PermissionProof) == "" {
		return fmt.Errorf("%w: user id and permission proof are required for lifecycle execution", ports.ErrInvalid)
	}
	if request.TenantID != record.TenantID || request.InstanceID != record.InstanceID {
		return fmt.Errorf("%w: lifecycle request does not match instance record", ports.ErrInvalid)
	}
	if len(record.ResourceRefs) == 0 {
		return fmt.Errorf("%w: resource refs are required for lifecycle execution", ports.ErrInvalid)
	}
	return nil
}

func resourceFromRecord(record ports.WorkloadInstanceRecord) (kubernetesResource, error) {
	namespace := tenantNamespace(record.TenantID)
	provider := record.Provider
	if provider == "" && len(record.ResourceRefs) > 0 {
		provider = strings.Split(record.ResourceRefs[0], "/")[0]
	}
	resource, err := resourceFromRef(provider, namespace, record.ResourceRefs[0])
	if err != nil {
		return kubernetesResource{}, err
	}
	if resource.Name == "" {
		return kubernetesResource{}, fmt.Errorf("%w: lifecycle resource name is required", ports.ErrInvalid)
	}
	return resource, nil
}

var _ ports.WorkloadInstanceLifecycleExecutor = (*KubernetesLifecycleExecutor)(nil)
