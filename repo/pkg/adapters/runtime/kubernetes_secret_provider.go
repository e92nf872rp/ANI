package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesSecretProviderClient interface {
	ApplyManifests(ctx context.Context, manifests []ports.WorkloadManifest) ([]string, error)
}

type KubernetesSecretProviderAdapter struct {
	client KubernetesSecretProviderClient
	now    func() time.Time
}

type KubernetesSecretProviderOption func(*KubernetesSecretProviderAdapter)

func WithKubernetesSecretProviderClock(now func() time.Time) KubernetesSecretProviderOption {
	return func(adapter *KubernetesSecretProviderAdapter) {
		if now != nil {
			adapter.now = now
		}
	}
}

func NewKubernetesSecretProviderAdapter(client KubernetesSecretProviderClient, options ...KubernetesSecretProviderOption) *KubernetesSecretProviderAdapter {
	adapter := &KubernetesSecretProviderAdapter{client: client, now: time.Now}
	for _, option := range options {
		option(adapter)
	}
	return adapter
}

func (a *KubernetesSecretProviderAdapter) ApplySecret(ctx context.Context, request ports.SecretProviderApplyRequest) (ports.SecretProviderApplyResult, error) {
	if err := validateSecretProviderApplyRequest(request); err != nil {
		return ports.SecretProviderApplyResult{}, err
	}
	if a.client == nil {
		return ports.SecretProviderApplyResult{}, fmt.Errorf("%w: Kubernetes Secret provider client is required", ports.ErrNotConfigured)
	}
	manifest, err := renderKubernetesSecretManifest(request)
	if err != nil {
		return ports.SecretProviderApplyResult{}, err
	}
	refs, err := a.client.ApplyManifests(ctx, []ports.WorkloadManifest{manifest})
	if err != nil {
		return ports.SecretProviderApplyResult{}, err
	}
	return ports.SecretProviderApplyResult{
		Applied:      true,
		Provider:     "kubernetes",
		ResourceRefs: refs,
		Reason:       "applied by Kubernetes Secret provider",
		AppliedAt:    a.now().UTC(),
	}, nil
}

func validateSecretProviderApplyRequest(request ports.SecretProviderApplyRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.SecretID) == "" {
		return fmt.Errorf("%w: tenant_id and secret_id are required for Kubernetes Secret apply", ports.ErrInvalid)
	}
	if len(request.Data) == 0 {
		return fmt.Errorf("%w: secret data is required for Kubernetes Secret apply", ports.ErrInvalid)
	}
	return nil
}

func renderKubernetesSecretManifest(request ports.SecretProviderApplyRequest) (ports.WorkloadManifest, error) {
	secretType := request.Type
	if secretType == "" || strings.EqualFold(secretType, "opaque") {
		secretType = "Opaque"
	}
	name := request.SecretID
	doc := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": tenantNamespace(request.TenantID),
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "ani-core",
				"ani.kubercloud.io/secret-id":  request.SecretID,
			},
		},
		"type":       secretType,
		"stringData": cloneSecretData(request.Data),
	}
	content, err := json.Marshal(doc)
	if err != nil {
		return ports.WorkloadManifest{}, err
	}
	return ports.WorkloadManifest{
		Provider: "kubernetes",
		Kind:     "Secret",
		Name:     name,
		Content:  string(content),
	}, nil
}

var _ ports.SecretProviderApply = (*KubernetesSecretProviderAdapter)(nil)
