package runtime

import (
	"context"
	"fmt"
	"net/http"
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
	if err := e.execute(ctx, request.Action, resource, record); err != nil {
		return ports.WorkloadInstanceLifecycleResult{}, err
	}
	return ports.WorkloadInstanceLifecycleResult{
		Action:    request.Action,
		Accepted:  true,
		Reason:    "accepted by Kubernetes lifecycle executor",
		CheckedAt: e.now().UTC(),
	}, nil
}

func (e *KubernetesLifecycleExecutor) execute(ctx context.Context, action ports.WorkloadLifecycleAction, resource kubernetesResource, record ports.WorkloadInstanceRecord) error {
	switch action {
	case ports.WorkloadLifecycleStart:
		return e.start(ctx, resource)
	case ports.WorkloadLifecycleStop:
		return e.stop(ctx, resource)
	case ports.WorkloadLifecycleRestart:
		return e.restart(ctx, resource)
	case ports.WorkloadLifecycleResize:
		return e.restart(ctx, resource)
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
		return fmt.Errorf("%w: unsupported Kubernetes lifecycle action %q", ports.ErrUnsupported, action)
	}
}

func (e *KubernetesLifecycleExecutor) start(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		_, err := e.client.do(ctx, http.MethodPut, e.client.resourceURL(resource, "start=true"), "application/json", []byte(`{}`))
		return err
	}
	return e.patchScale(ctx, resource, 1)
}

func (e *KubernetesLifecycleExecutor) stop(ctx context.Context, resource kubernetesResource) error {
	if resource.Kind == "VirtualMachine" {
		_, err := e.client.do(ctx, http.MethodPut, e.client.resourceURL(resource, "stop=true"), "application/json", []byte(`{}`))
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
