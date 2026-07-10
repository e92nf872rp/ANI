package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesLifecycleExecutor struct {
	client  *KubernetesRESTClient
	enabled bool
	now     func() time.Time
}

type KubernetesLifecycleOption func(*KubernetesLifecycleExecutor)

func WithKubernetesLifecycleEnabled(enabled bool) KubernetesLifecycleOption {
	return func(executor *KubernetesLifecycleExecutor) {
		executor.enabled = enabled
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

	resource, err := resourceFromRecordRef(record, "")
	if err != nil {
		return ports.WorkloadInstanceLifecycleResult{}, err
	}
	if err := e.execute(ctx, request, resource, record); err != nil {
		return ports.WorkloadInstanceLifecycleResult{}, err
	}
	return ports.WorkloadInstanceLifecycleResult{
		Action:    request.Action,
		Accepted:  true,
		Reason:    "accepted by Kubernetes lifecycle executor",
		CheckedAt: e.now().UTC(),
	}, nil
}

func (e *KubernetesLifecycleExecutor) execute(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest, resource kubernetesResource, record ports.WorkloadInstanceRecord) error {
	switch request.Action {
	case ports.WorkloadLifecycleStart:
		return e.start(ctx, resource)
	case ports.WorkloadLifecycleStop:
		return e.stop(ctx, resource)
	case ports.WorkloadLifecycleRestart:
		return e.restart(ctx, resource)
	case ports.WorkloadLifecycleResize:
		return e.restart(ctx, resource)
	case ports.WorkloadLifecycleDetachVolume:
		return e.detachVolume(ctx, resource, request.VolumeID)
	case ports.WorkloadLifecycleDelete:
		for _, ref := range providerResourceRefsForLifecycleDelete(record.ResourceRefs) {
			resource, err := resourceFromRecordRef(record, ref)
			if err != nil {
				return err
			}
			if _, err := e.client.do(ctx, http.MethodDelete, e.client.resourceURL(resource, ""), "", nil); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported Kubernetes lifecycle action %q", ports.ErrUnsupported, request.Action)
	}
}

func (e *KubernetesLifecycleExecutor) start(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		_, err := e.client.do(ctx, http.MethodPut, e.kubeVirtVMSubresourceURL(resource, "start"), "application/json", kubeVirtVMSubresourceOptions("StartOptions"))
		return err
	}
	return e.patchScale(ctx, resource, 1)
}

func (e *KubernetesLifecycleExecutor) stop(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		_, err := e.client.do(ctx, http.MethodPut, e.kubeVirtVMSubresourceURL(resource, "stop"), "application/json", kubeVirtVMSubresourceOptions("StopOptions"))
		return err
	}
	return e.patchScale(ctx, resource, 0)
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

func (e *KubernetesLifecycleExecutor) detachVolume(ctx context.Context, resource kubernetesResource, volumeID string) error {
	volumeID = strings.TrimSpace(volumeID)
	if resource.Kind != "VirtualMachine" {
		return fmt.Errorf("%w: detach_volume provider lifecycle is only supported for KubeVirt VirtualMachine", ports.ErrUnsupported)
	}
	if volumeID == "" {
		return fmt.Errorf("%w: volumeID is required for provider detach_volume", ports.ErrInvalid)
	}
	data, err := e.client.do(ctx, http.MethodGet, e.client.resourceURL(resource, ""), "", nil)
	if err != nil {
		return err
	}
	var vm map[string]any
	if err := json.Unmarshal(data, &vm); err != nil {
		return fmt.Errorf("%w: decode KubeVirt VM for detach_volume: %v", ports.ErrInvalid, err)
	}
	disks, err := kubeVirtVMList(vm, []string{"spec", "template", "spec", "domain", "devices", "disks"})
	if err != nil {
		return err
	}
	volumes, err := kubeVirtVMList(vm, []string{"spec", "template", "spec", "volumes"})
	if err != nil {
		return err
	}
	nextDisks := removeNamedKubernetesEntries(disks, volumeID)
	nextVolumes := removeNamedKubernetesEntries(volumes, volumeID)
	if len(nextDisks) == len(disks) && len(nextVolumes) == len(volumes) {
		return fmt.Errorf("%w: volume %q is not attached to KubeVirt VM", ports.ErrConflict, volumeID)
	}
	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"domain": map[string]any{
						"devices": map[string]any{"disks": nextDisks},
					},
					"volumes": nextVolumes,
				},
			},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = e.client.do(ctx, http.MethodPatch, e.client.resourceURL(resource, ""), "application/merge-patch+json", body)
	return err
}

func (e *KubernetesLifecycleExecutor) patchScale(ctx context.Context, resource kubernetesResource, replicas int) error {
	endpoint := e.client.host + resource.resourcePath() + "/scale"
	body := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := e.client.do(ctx, http.MethodPatch, endpoint, "application/merge-patch+json", []byte(body))
	return err
}

func (e *KubernetesLifecycleExecutor) kubeVirtVMSubresourceURL(resource kubernetesResource, action string) string {
	return e.client.host + "/apis/subresources.kubevirt.io/v1/namespaces/" + url.PathEscape(resource.Namespace) + "/virtualmachines/" + url.PathEscape(resource.Name) + "/" + url.PathEscape(action)
}

func kubeVirtVMSubresourceOptions(kind string) []byte {
	return []byte(`{"apiVersion":"subresources.kubevirt.io/v1","kind":"` + kind + `"}`)
}

func kubeVirtVMList(doc map[string]any, path []string) ([]any, error) {
	var current any = doc
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: KubeVirt VM field %q is not an object", ports.ErrInvalid, key)
		}
		current = object[key]
	}
	list, ok := current.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: KubeVirt VM list field %s is missing", ports.ErrInvalid, strings.Join(path, "."))
	}
	return list, nil
}

func removeNamedKubernetesEntries(items []any, name string) []any {
	next := make([]any, 0, len(items))
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if entry["name"] == name {
			continue
		}
		next = append(next, item)
	}
	return next
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
	ref, err := primaryWorkloadResourceRef(record.ResourceRefs)
	if err != nil {
		return kubernetesResource{}, err
	}
	return resourceFromRecordRef(record, ref)
}

func resourceFromRecordRef(record ports.WorkloadInstanceRecord, ref string) (kubernetesResource, error) {
	if len(record.ResourceRefs) == 0 {
		return kubernetesResource{}, fmt.Errorf("%w: resource refs are required for lifecycle execution", ports.ErrInvalid)
	}
	if strings.TrimSpace(ref) == "" {
		var err error
		ref, err = primaryWorkloadResourceRef(record.ResourceRefs)
		if err != nil {
			return kubernetesResource{}, err
		}
	}
	namespace := tenantNamespace(record.TenantID)
	resource, err := resourceFromRef("", namespace, ref)
	if err != nil {
		return kubernetesResource{}, err
	}
	if resource.Name == "" {
		return kubernetesResource{}, fmt.Errorf("%w: lifecycle resource name is required", ports.ErrInvalid)
	}
	return resource, nil
}

var _ ports.WorkloadInstanceLifecycleExecutor = (*KubernetesLifecycleExecutor)(nil)
