package router

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestSecretAPIDevProfileIdempotencyAndBinding(t *testing.T) {
	api := newSecretAPI()
	a, err := api.service.CreateSecret(context.Background(), ports.SecretCreateRequest{
		TenantID:       "t1",
		IdempotencyKey: "secret-1",
		Name:           "db-password",
		Type:           "opaque",
		Data:           map[string]string{"password": "secret-value", "username": "ani"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := api.service.CreateSecret(context.Background(), ports.SecretCreateRequest{
		TenantID:       "t1",
		IdempotencyKey: "secret-1",
		Name:           "db-password",
		Type:           "opaque",
		Data:           map[string]string{"password": "secret-value", "username": "ani"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.SecretID != b.SecretID {
		t.Fatalf("want idempotent secret id, got %s != %s", a.SecretID, b.SecretID)
	}
	if len(a.Keys) != 2 || a.Keys[0] != "password" || a.Keys[1] != "username" {
		t.Fatalf("want sorted secret keys without values, got %#v", a.Keys)
	}
	resp := secretFromRecord(a)
	requireLocalCoreDevProfile(t, resp.DevProfile, "local-secret-service")

	binding, err := api.service.BindSecret(context.Background(), ports.SecretBindRequest{
		TenantID:       "t1",
		IdempotencyKey: "bind-secret-1",
		SecretID:       a.SecretID,
		TargetType:     "instance",
		TargetID:       "inst-1",
		EnvPrefix:      "DB_",
	})
	if err != nil {
		t.Fatal(err)
	}
	if binding.State != "bound" {
		t.Fatalf("want bound state, got %s", binding.State)
	}
	bindingResp := secretBindingFromRecord(binding)
	requireLocalCoreDevProfile(t, bindingResp.DevProfile, "local-secret-service")
}

func TestSecretAPIUsesInjectedService(t *testing.T) {
	service := &fakeSecretService{
		record: ports.SecretRecord{
			SecretID:  "sec-injected",
			TenantID:  "tenant-a",
			Name:      "db-password",
			Type:      "opaque",
			Keys:      []string{"password"},
			State:     "active",
			CreatedAt: 100,
			UpdatedAt: 100,
		},
	}
	api := newSecretAPIWithService(service)

	got, err := api.service.CreateSecret(context.Background(), ports.SecretCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "idem-a",
		Name:           "db-password",
		Data:           map[string]string{"password": "secret-value"},
	})
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}
	if !service.createCalled {
		t.Fatalf("injected service CreateSecret was not called")
	}
	if got.SecretID != "sec-injected" {
		t.Fatalf("secret id = %s, want injected record", got.SecretID)
	}
}

func TestSecretResponseMarksRealProviderWhenKubernetesSecretWasWritten(t *testing.T) {
	resp := secretFromRecord(ports.SecretRecord{
		SecretID:     "sec-real",
		TenantID:     "tenant-a",
		Name:         "db-password",
		Type:         "opaque",
		Keys:         []string{"password"},
		State:        "active",
		Provider:     "kubernetes",
		RealProvider: true,
		ProviderRefs: []string{"kubernetes/Secret/sec-real"},
		CreatedAt:    100,
		UpdatedAt:    100,
	})
	if resp.DevProfile.Mode != "real" || !resp.DevProfile.RealProvider || resp.DevProfile.Provider != "kubernetes-secret-provider" {
		t.Fatalf("dev_profile = %+v, want Kubernetes Secret provider", resp.DevProfile)
	}
}

type fakeSecretService struct {
	createCalled bool
	record       ports.SecretRecord
}

func (s *fakeSecretService) CreateSecret(_ context.Context, req ports.SecretCreateRequest) (ports.SecretRecord, error) {
	s.createCalled = true
	s.record.TenantID = req.TenantID
	return s.record, nil
}

func (s *fakeSecretService) GetSecret(context.Context, ports.SecretGetRequest) (ports.SecretRecord, error) {
	return ports.SecretRecord{}, ports.ErrUnsupported
}

func (s *fakeSecretService) ListSecrets(context.Context, ports.SecretListRequest) ([]ports.SecretRecord, error) {
	return nil, ports.ErrUnsupported
}

func (s *fakeSecretService) DeleteSecret(context.Context, ports.SecretGetRequest) (ports.SecretRecord, error) {
	return ports.SecretRecord{}, ports.ErrUnsupported
}

func (s *fakeSecretService) BindSecret(context.Context, ports.SecretBindRequest) (ports.SecretBindingRecord, error) {
	return ports.SecretBindingRecord{}, ports.ErrUnsupported
}

var _ ports.SecretService = (*fakeSecretService)(nil)
