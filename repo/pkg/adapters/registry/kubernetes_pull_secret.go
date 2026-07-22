package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesManifestApplier interface {
	ApplyManifests(ctx context.Context, manifests []ports.WorkloadManifest) ([]string, error)
}

// KubernetesPullSecretWriter applies only docker registry credentials to the
// namespace ANI derives for the calling tenant. The request namespace is never
// trusted as an authority to cross tenant boundaries.
type KubernetesPullSecretWriter struct{ client KubernetesManifestApplier }

func NewKubernetesPullSecretWriter(client KubernetesManifestApplier) *KubernetesPullSecretWriter {
	return &KubernetesPullSecretWriter{client: client}
}

func (w *KubernetesPullSecretWriter) ApplyRegistryPullSecret(ctx context.Context, request ports.RegistryPullSecretWriteRequest) error {
	if w.client == nil {
		return ports.ErrNotConfigured
	}
	tenantID, name := strings.TrimSpace(request.TenantID), strings.TrimSpace(request.Name)
	if tenantID == "" || name == "" || strings.TrimSpace(request.DockerConfigJSON) == "" {
		return fmt.Errorf("%w: tenant_id, name, and docker config are required", ports.ErrInvalid)
	}
	namespace := registryTenantNamespace(tenantID)
	if request.Namespace != namespace {
		return fmt.Errorf("%w: namespace must be the tenant namespace", ports.ErrInvalid)
	}
	var dockerConfig map[string]any
	if err := json.Unmarshal([]byte(request.DockerConfigJSON), &dockerConfig); err != nil {
		return fmt.Errorf("%w: invalid docker config JSON", ports.ErrInvalid)
	}
	doc, err := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata":   map[string]any{"name": name, "namespace": namespace, "labels": map[string]string{"app.kubernetes.io/managed-by": "ani-core", "ani.kubercloud.io/tenant-id": tenantID, "ani.kubercloud.io/registry": strings.TrimSpace(request.Registry)}},
		"type":       "kubernetes.io/dockerconfigjson",
		"stringData": map[string]string{".dockerconfigjson": request.DockerConfigJSON},
	})
	if err != nil {
		return fmt.Errorf("%w: encode pull secret manifest", ports.ErrInvalid)
	}
	_, err = w.client.ApplyManifests(ctx, []ports.WorkloadManifest{{Provider: "kubernetes", Kind: "Secret", Name: name, Content: string(doc)}})
	return err
}

func registryTenantNamespace(tenantID string) string {
	return "ani-tenant-" + strings.ReplaceAll(tenantID, "_", "-")
}

var _ ports.RegistryPullSecretWriter = (*KubernetesPullSecretWriter)(nil)
