package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
)

type endpointStore struct {
	controlStoreStub
	err error
}

func (s *endpointStore) GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error) {
	if s.err != nil {
		return domain.Service{}, s.err
	}
	return s.service, nil
}

func (s *endpointStore) ResolveRunningServiceByServedModelName(_ context.Context, tenantID uuid.UUID, servedModelName string) (domain.Service, error) {
	if s.err != nil {
		return domain.Service{}, s.err
	}
	if s.service.TenantID != tenantID || s.service.ServedModelName != servedModelName {
		return domain.Service{}, repository.ErrNotFound
	}
	return s.service, nil
}

func TestResolveInternalEndpointCanonicalizesRuntimeServiceURL(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	serviceID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	resource := runningControlService()
	resource.TenantID = tenantID
	resource.ID = serviceID
	resource.ServedModelName = "qwen3.5-0.8b"
	resource.DesiredSpec.ExecutionProfile.Task = domain.InferenceTaskEmbed
	resource.RuntimeEndpoint = "http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc:8000"

	controller := NewController(&endpointStore{controlStoreStub: controlStoreStub{service: resource}}, nil)
	got, err := controller.ResolveInternalEndpoint(context.Background(), tenantID, serviceID)
	if err != nil {
		t.Fatalf("ResolveInternalEndpoint: %v", err)
	}
	want := "http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc.cluster.local:8000/v1"
	if got.BaseURL != want || got.ServedModelName != resource.ServedModelName || got.Status != domain.StatusRunning || got.Task != domain.InferenceTaskEmbed {
		t.Fatalf("endpoint = %+v, want base_url=%q model=%q task=%q status=%q", got, want, resource.ServedModelName, domain.InferenceTaskEmbed, domain.StatusRunning)
	}
}

func TestResolveInternalEndpointByServedModelName(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	serviceID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	resource := runningControlService()
	resource.TenantID = tenantID
	resource.ID = serviceID
	resource.ServedModelName = "qwen3.5-0.8b"
	resource.RuntimeEndpoint = "http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc:8000"

	controller := NewController(&endpointStore{controlStoreStub: controlStoreStub{service: resource}}, nil)
	got, err := controller.ResolveInternalEndpointByServedModelName(context.Background(), tenantID, resource.ServedModelName)
	if err != nil {
		t.Fatalf("ResolveInternalEndpointByServedModelName: %v", err)
	}
	if got.ServiceID != serviceID || got.ServedModelName != resource.ServedModelName || got.BaseURL == "" {
		t.Fatalf("endpoint = %+v", got)
	}
}

func TestResolveInternalEndpointAcceptsHexLeadingAndLeaderWorkerServiceNames(t *testing.T) {
	tests := []struct {
		name string
		id   uuid.UUID
		host string
	}{
		{name: "hex leading service", id: uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), host: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
		{name: "leader worker http service", id: uuid.MustParse("22222222-2222-2222-2222-222222222222"), host: "pw-22222222-2222-2222-2222-222222222222-http"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
			resource := runningControlService()
			resource.TenantID = tenantID
			resource.ID = test.id
			resource.RuntimeEndpoint = "http://" + test.host + ".ani-tenant-" + tenantID.String() + ".svc.cluster.local:8000"
			controller := NewController(&endpointStore{controlStoreStub: controlStoreStub{service: resource}}, nil)
			got, err := controller.ResolveInternalEndpoint(context.Background(), tenantID, test.id)
			if err != nil {
				t.Fatalf("ResolveInternalEndpoint: %v", err)
			}
			if got.BaseURL != "http://"+test.host+".ani-tenant-"+tenantID.String()+".svc.cluster.local:8000/v1" {
				t.Fatalf("base_url = %q", got.BaseURL)
			}
		})
	}
}

func TestResolveInternalEndpointRejectsUnreadyOrMissingRuntime(t *testing.T) {
	tenantID := uuid.New()
	serviceID := uuid.New()
	resource := runningControlService()
	resource.TenantID = tenantID
	resource.ID = serviceID
	tests := []struct {
		name   string
		mutate func(*domain.Service)
		want   error
	}{
		{name: "not running", mutate: func(s *domain.Service) { s.Status = domain.StatusDeploying }, want: ErrInferenceServiceNotReady},
		{name: "stale generation", mutate: func(s *domain.Service) { s.ObservedGeneration = s.Generation - 1 }, want: ErrInferenceServiceNotReady},
		{name: "missing endpoint", mutate: func(s *domain.Service) { s.RuntimeEndpoint = "" }, want: ErrRuntimeEndpointMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := resource
			test.mutate(&current)
			controller := NewController(&endpointStore{controlStoreStub: controlStoreStub{service: current}}, nil)
			_, err := controller.ResolveInternalEndpoint(context.Background(), tenantID, serviceID)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestResolveInternalEndpointRejectsUntrustedRuntimeEndpoint(t *testing.T) {
	tenantID := uuid.New()
	serviceID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	resource := runningControlService()
	resource.TenantID = tenantID
	resource.ID = serviceID
	for _, endpoint := range []string{
		"https://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc:8000",
		"http://pw-" + uuid.NewString() + ".ani-tenant-" + tenantID.String() + ".svc:8000",
		"http://pw-" + serviceID.String() + ".ani-tenant-" + uuid.NewString() + ".svc:8000",
		"http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc:9000",
		"http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc:8000/v1",
	} {
		resource.RuntimeEndpoint = endpoint
		controller := NewController(&endpointStore{controlStoreStub: controlStoreStub{service: resource}}, nil)
		if _, err := controller.ResolveInternalEndpoint(context.Background(), tenantID, serviceID); !errors.Is(err, ErrRuntimeEndpointInvalid) {
			t.Fatalf("endpoint %q error = %v, want %v", endpoint, err, ErrRuntimeEndpointInvalid)
		}
	}
}

func TestResolveInternalEndpointPreservesNotFoundAndStorageErrors(t *testing.T) {
	tenantID := uuid.New()
	serviceID := uuid.New()
	for _, want := range []error{repository.ErrNotFound, context.Canceled} {
		controller := NewController(&endpointStore{err: want}, nil)
		_, err := controller.ResolveInternalEndpoint(context.Background(), tenantID, serviceID)
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
	}
}
