package runtime

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalSecretServiceAppliesSecretToConfiguredProvider(t *testing.T) {
	provider := &fakeSecretProviderApply{}
	service := NewLocalSecretService(
		WithSecretProviderApply(provider),
	)

	record, err := service.CreateSecret(context.Background(), ports.SecretCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-db-secret",
		Name:           "db-password",
		Type:           "opaque",
		Data:           map[string]string{"password": "secret-value", "username": "ani"},
	})
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if provider.last.TenantID != "tenant-a" || provider.last.SecretID != record.SecretID || provider.last.Name != "db-password" {
		t.Fatalf("provider request = %+v, want tenant/secret/name from record", provider.last)
	}
	if provider.last.Data["password"] != "secret-value" || provider.last.Data["username"] != "ani" {
		t.Fatalf("provider data = %#v, want secret values", provider.last.Data)
	}
	if record.State != "active" {
		t.Fatalf("record state = %s, want active", record.State)
	}
	if !record.RealProvider || record.Provider != "kubernetes" || len(record.ProviderRefs) != 1 {
		t.Fatalf("record provider evidence = %+v, want Kubernetes provider ref", record)
	}
}

type fakeSecretProviderApply struct {
	calls int
	last  ports.SecretProviderApplyRequest
}

func (p *fakeSecretProviderApply) ApplySecret(_ context.Context, request ports.SecretProviderApplyRequest) (ports.SecretProviderApplyResult, error) {
	p.calls++
	p.last = request
	return ports.SecretProviderApplyResult{
		Applied:      true,
		Provider:     "kubernetes",
		ResourceRefs: []string{"kubernetes/Secret/" + request.SecretID},
	}, nil
}

var _ ports.SecretProviderApply = (*fakeSecretProviderApply)(nil)
