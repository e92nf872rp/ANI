package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"strings"
	"testing"
	"time"
)

type policyStoreFake struct {
	policies   []domain.AccessPolicy
	events     []domain.AccessPolicyEvent
	published  map[string]domain.Service
	resolveErr error
	listErr    error
	updateHash string
}

func (f *policyStoreFake) ListServiceAccessPolicies(context.Context, uuid.UUID, uuid.UUID) ([]domain.AccessPolicy, error) {
	return f.policies, nil
}
func (f *policyStoreFake) RecordAccessPolicyEvent(_ context.Context, event domain.AccessPolicyEvent) error {
	f.events = append(f.events, event)
	return nil
}
func (f *policyStoreFake) ListAccessPolicies(context.Context, uuid.UUID) ([]domain.AccessPolicy, error) {
	return f.policies, f.listErr
}
func (*policyStoreFake) GetAccessPolicy(context.Context, uuid.UUID, uuid.UUID) (domain.AccessPolicy, error) {
	return domain.AccessPolicy{}, nil
}
func (*policyStoreFake) CreateAccessPolicy(context.Context, domain.AccessPolicy, uuid.UUID) (domain.AccessPolicy, error) {
	return domain.AccessPolicy{}, nil
}
func (f *policyStoreFake) UpdateAccessPolicy(_ context.Context, policy domain.AccessPolicy, _ uuid.UUID, requestHash string) (domain.AccessPolicy, error) {
	f.updateHash = requestHash
	return policy, nil
}
func (*policyStoreFake) DeleteAccessPolicy(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (*policyStoreFake) ReplaceServiceAccessPolicies(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID, uuid.UUID) ([]domain.AccessPolicy, error) {
	return nil, nil
}
func (*policyStoreFake) ListAccessPolicyEvents(context.Context, uuid.UUID, domain.AccessPolicyEventQuery) ([]domain.AccessPolicyEvent, string, error) {
	return nil, "", nil
}
func (f *policyStoreFake) ResolvePublishedService(_ context.Context, tenantID uuid.UUID, servedModelName string) (domain.Service, error) {
	if f.resolveErr != nil {
		return domain.Service{}, f.resolveErr
	}
	service, ok := f.published[tenantID.String()+"/"+servedModelName]
	if !ok {
		return domain.Service{}, repository.ErrNotFound
	}
	return service, nil
}

func (f *policyStoreFake) addPublished(tenantID, serviceID uuid.UUID, servedModelName string, task domain.InferenceTask) {
	if f.published == nil {
		f.published = make(map[string]domain.Service)
	}
	f.published[tenantID.String()+"/"+servedModelName] = domain.Service{
		ID: serviceID, TenantID: tenantID, ServedModelName: servedModelName,
		Status: domain.StatusRunning, DesiredState: domain.DesiredStateRunning,
		DesiredSpec: domain.Spec{ExecutionProfile: domain.ExecutionProfile{Task: task}},
	}
}

func TestUpdatePolicyForwardsOriginalPatchRequestHash(t *testing.T) {
	store := &policyStoreFake{}
	service := NewAccessPolicyService(store, nil, nil)
	want := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := service.UpdatePolicy(context.Background(), domain.AccessPolicy{}, uuid.New(), want); err != nil {
		t.Fatal(err)
	}
	if store.updateHash != want {
		t.Fatalf("request hash = %q, want %q", store.updateHash, want)
	}
}

type limiterFake struct {
	allow bool
	lease bool
}

func (f limiterFake) AllowFixedWindow(context.Context, string, int, time.Duration, time.Time) (bool, time.Duration, error) {
	return f.allow, 3 * time.Second, nil
}
func (f limiterFake) AcquireLease(context.Context, string, int, time.Duration, time.Time) (string, bool, time.Duration, error) {
	return "lease-1", f.lease, 4 * time.Second, nil
}
func (limiterFake) ReleaseLease(context.Context, string) error { return nil }

type qpsLimiterFake struct {
	calls []time.Duration
	key   string
}

func (f *qpsLimiterFake) AllowFixedWindow(_ context.Context, key string, _ int, window time.Duration, _ time.Time) (bool, time.Duration, error) {
	f.calls = append(f.calls, window)
	f.key = key
	return false, time.Second, nil
}
func (*qpsLimiterFake) AcquireLease(context.Context, string, int, time.Duration, time.Time) (string, bool, time.Duration, error) {
	return "", true, 0, nil
}
func (*qpsLimiterFake) ReleaseLease(context.Context, string) error { return nil }

func accessInput() AccessCheckInput {
	return AccessCheckInput{TenantID: uuid.New(), APIKeyID: uuid.New(), InferenceServiceID: uuid.New(), KeyPrefix: "ani_live", ServedModelName: "ani-model", OpenAIPath: "/v1/chat/completions"}
}

func TestCheckAccessResolvesModelOnlyInsideAuthenticatedTenant(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	serviceA, serviceB := uuid.New(), uuid.New()
	store := &policyStoreFake{}
	store.addPublished(tenantA, serviceA, "ani-qwen3", domain.InferenceTaskGenerate)
	store.addPublished(tenantB, serviceB, "ani-qwen3", domain.InferenceTaskGenerate)

	decision, err := NewAccessPolicyService(store, nil, time.Now).CheckAccess(context.Background(), AccessCheckInput{
		TenantID: tenantB, APIKeyID: uuid.New(), ServedModelName: "ani-qwen3", OpenAIPath: "/v1/chat/completions",
	})
	if err != nil || decision.HTTPStatus != 200 || decision.InferenceServiceID != serviceB {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestCheckAccessHidesMissingUnpublishedAndStoppedModels(t *testing.T) {
	for _, name := range []string{"missing", "unpublished", "stopped"} {
		t.Run(name, func(t *testing.T) {
			decision, err := NewAccessPolicyService(&policyStoreFake{}, nil, time.Now).CheckAccess(context.Background(), AccessCheckInput{
				TenantID: uuid.New(), APIKeyID: uuid.New(), ServedModelName: name, OpenAIPath: "/v1/chat/completions",
			})
			if err != nil || decision.HTTPStatus != 404 || decision.ReasonCode != "NOT_FOUND" {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
		})
	}
}

func TestCheckAccessRejectsTaskPathMismatchesAsNotFound(t *testing.T) {
	for _, test := range []struct {
		name string
		task domain.InferenceTask
		path string
	}{
		{name: "generate to embeddings", task: domain.InferenceTaskGenerate, path: "/v1/embeddings"},
		{name: "embed to chat", task: domain.InferenceTaskEmbed, path: "/v1/chat/completions"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenantID, serviceID := uuid.New(), uuid.New()
			store := &policyStoreFake{}
			store.addPublished(tenantID, serviceID, "ani-model", test.task)
			decision, err := NewAccessPolicyService(store, nil, time.Now).CheckAccess(context.Background(), AccessCheckInput{
				TenantID: tenantID, APIKeyID: uuid.New(), ServedModelName: "ani-model", OpenAIPath: test.path,
			})
			if err != nil || decision.HTTPStatus != 404 || decision.ReasonCode != "NOT_FOUND" {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
		})
	}
}

func TestOpenAIPathForTaskRejectsUnknownAndEmptyTasks(t *testing.T) {
	for _, task := range []domain.InferenceTask{"", "legacy"} {
		if path, ok := OpenAIPathForTask(task); ok || path != "" {
			t.Fatalf("OpenAIPathForTask(%q) = (%q, %v), want no route", task, path, ok)
		}
	}
}

func TestCheckAccessFailsClosedWhenPublishedServiceLookupFails(t *testing.T) {
	store := &policyStoreFake{resolveErr: errors.New("database down")}
	decision, err := NewAccessPolicyService(store, nil, time.Now).CheckAccess(context.Background(), AccessCheckInput{
		TenantID: uuid.New(), APIKeyID: uuid.New(), ServedModelName: "ani-model", OpenAIPath: "/v1/chat/completions",
	})
	if err == nil || decision.HTTPStatus != 503 || decision.ReasonCode != "POLICY_BACKEND_UNAVAILABLE" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestCheckAccessEnforcesQPSWithOneSecondFixedWindow(t *testing.T) {
	tenantID, serviceID, keyID := uuid.New(), uuid.New(), uuid.New()
	store := &policyStoreFake{policies: []domain.AccessPolicy{{
		ID: uuid.New(), TenantID: tenantID, Status: domain.AccessPolicyEnabled, Priority: 1,
		Scope:      domain.AccessPolicyScope{Type: domain.ScopeTenantDefault},
		Access:     domain.AccessPolicyAccess{AllowAllTenantKeys: true},
		RateLimits: domain.AccessPolicyRateLimits{QPS: 1},
	}}}
	store.addPublished(tenantID, serviceID, "ani-qwen3", domain.InferenceTaskGenerate)
	limiter := &qpsLimiterFake{}
	decision, err := NewAccessPolicyService(store, limiter, time.Now).CheckAccess(context.Background(), AccessCheckInput{
		TenantID: tenantID, APIKeyID: keyID, ServedModelName: "ani-qwen3", OpenAIPath: "/v1/chat/completions",
	})
	if err != nil || decision.HTTPStatus != 429 || decision.ReasonCode != "POLICY_QPS_LIMIT" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if len(limiter.calls) != 1 || limiter.calls[0] != time.Second {
		t.Fatalf("fixed-window calls=%v", limiter.calls)
	}
	if !strings.Contains(limiter.key, serviceID.String()) || strings.Contains(limiter.key, "ani-qwen3") {
		t.Fatalf("limiter key=%q", limiter.key)
	}
}

func TestCheckAccessReturnsDenyForAllowlistMiss(t *testing.T) {
	in := accessInput()
	store := &policyStoreFake{policies: []domain.AccessPolicy{{ID: uuid.New(), TenantID: in.TenantID, Status: domain.AccessPolicyEnabled, Priority: 1, Scope: domain.AccessPolicyScope{Type: domain.ScopeInferenceService, InferenceServiceIDs: []uuid.UUID{in.InferenceServiceID}}, Access: domain.AccessPolicyAccess{AllowAPIKeyIDs: []string{uuid.New().String()}}}}}
	store.addPublished(in.TenantID, in.InferenceServiceID, in.ServedModelName, domain.InferenceTaskGenerate)
	decision, err := NewAccessPolicyService(store, limiterFake{allow: true, lease: true}, time.Now).CheckAccess(context.Background(), in)
	if err != nil || decision.HTTPStatus != 403 || len(store.events) != 1 {
		t.Fatalf("decision=%+v err=%v events=%d", decision, err, len(store.events))
	}
}

func TestCheckAccessReturnsRateLimited(t *testing.T) {
	in := accessInput()
	store := &policyStoreFake{policies: []domain.AccessPolicy{{ID: uuid.New(), TenantID: in.TenantID, Status: domain.AccessPolicyEnabled, Priority: 1, Scope: domain.AccessPolicyScope{Type: domain.ScopeInferenceService, InferenceServiceIDs: []uuid.UUID{in.InferenceServiceID}}, Access: domain.AccessPolicyAccess{AllowAllTenantKeys: true}, RateLimits: domain.AccessPolicyRateLimits{RPM: 1}}}}
	store.addPublished(in.TenantID, in.InferenceServiceID, in.ServedModelName, domain.InferenceTaskGenerate)
	decision, err := NewAccessPolicyService(store, limiterFake{allow: false}, time.Now).CheckAccess(context.Background(), in)
	if err != nil || decision.Decision != "rate_limited" || decision.HTTPStatus != 429 {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestCheckAccessReturnsConcurrencyLimited(t *testing.T) {
	in := accessInput()
	store := &policyStoreFake{policies: []domain.AccessPolicy{{ID: uuid.New(), TenantID: in.TenantID, Status: domain.AccessPolicyEnabled, Priority: 1, Scope: domain.AccessPolicyScope{Type: domain.ScopeInferenceService, InferenceServiceIDs: []uuid.UUID{in.InferenceServiceID}}, Access: domain.AccessPolicyAccess{AllowAllTenantKeys: true}, Concurrency: domain.AccessPolicyConcurrency{MaxInFlight: 1, LeaseTTLSeconds: 30}}}}
	store.addPublished(in.TenantID, in.InferenceServiceID, in.ServedModelName, domain.InferenceTaskGenerate)
	decision, err := NewAccessPolicyService(store, limiterFake{allow: true, lease: false}, time.Now).CheckAccess(context.Background(), in)
	if err != nil || decision.Decision != "concurrency_limited" || decision.HTTPStatus != 429 {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}
