package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
)

var ErrPolicyUnavailable = errors.New("inference access policy unavailable")

type AccessCheckInput struct {
	TenantID        uuid.UUID
	UserID          uuid.UUID
	APIKeyID        uuid.UUID
	KeyPrefix       string
	ServedModelName string
	OpenAIPath      string
	RequestID       string
	Stream          bool

	// Deprecated compatibility fields. Callers must not supply authorization
	// identities through these fields; CheckAccess resolves the service from
	// TenantID and ServedModelName.
	InferenceServiceID uuid.UUID
	ExternalModel      string
}

type AccessDecision struct {
	Decision           string
	HTTPStatus         int
	ReasonCode         string
	InferenceServiceID uuid.UUID
	PolicyID           uuid.UUID
	LeaseID            string
	RetryAfter         time.Duration
}

type AccessPolicyService struct {
	store   repository.AccessPolicyStore
	limiter RateLimiter
	now     func() time.Time
}

func (s *AccessPolicyService) ListPolicies(ctx context.Context, tenantID uuid.UUID) ([]domain.AccessPolicy, error) {
	return s.store.ListAccessPolicies(ctx, tenantID)
}
func (s *AccessPolicyService) GetPolicy(ctx context.Context, tenantID, policyID uuid.UUID) (domain.AccessPolicy, error) {
	return s.store.GetAccessPolicy(ctx, tenantID, policyID)
}
func (s *AccessPolicyService) CreatePolicy(ctx context.Context, policy domain.AccessPolicy, key uuid.UUID) (domain.AccessPolicy, error) {
	return s.store.CreateAccessPolicy(ctx, policy, key)
}
func (s *AccessPolicyService) UpdatePolicy(ctx context.Context, policy domain.AccessPolicy, key uuid.UUID, requestHash string) (domain.AccessPolicy, error) {
	return s.store.UpdateAccessPolicy(ctx, policy, key, requestHash)
}
func (s *AccessPolicyService) DeletePolicy(ctx context.Context, tenantID, policyID, key uuid.UUID) error {
	return s.store.DeleteAccessPolicy(ctx, tenantID, policyID, key)
}
func (s *AccessPolicyService) ListServicePolicies(ctx context.Context, tenantID, serviceID uuid.UUID) ([]domain.AccessPolicy, error) {
	return s.store.ListServiceAccessPolicies(ctx, tenantID, serviceID)
}
func (s *AccessPolicyService) ReplaceServicePolicies(ctx context.Context, tenantID, serviceID uuid.UUID, ids []uuid.UUID, key uuid.UUID) ([]domain.AccessPolicy, error) {
	return s.store.ReplaceServiceAccessPolicies(ctx, tenantID, serviceID, ids, key)
}
func (s *AccessPolicyService) ListEvents(ctx context.Context, tenantID uuid.UUID, q domain.AccessPolicyEventQuery) ([]domain.AccessPolicyEvent, string, error) {
	return s.store.ListAccessPolicyEvents(ctx, tenantID, q)
}

func NewAccessPolicyService(store repository.AccessPolicyStore, limiter RateLimiter, now func() time.Time) *AccessPolicyService {
	if now == nil {
		now = time.Now
	}
	return &AccessPolicyService{store: store, limiter: limiter, now: now}
}

func (s *AccessPolicyService) CheckAccess(ctx context.Context, in AccessCheckInput) (AccessDecision, error) {
	if in.TenantID == uuid.Nil || in.APIKeyID == uuid.Nil {
		return AccessDecision{Decision: "deny", HTTPStatus: 403, ReasonCode: "INVALID_IDENTITY"}, nil
	}
	if s.store == nil {
		return AccessDecision{Decision: "policy_unavailable", HTTPStatus: 503, ReasonCode: "POLICY_BACKEND_UNAVAILABLE"}, ErrPolicyUnavailable
	}
	resolved, err := s.store.ResolvePublishedService(ctx, in.TenantID, strings.TrimSpace(in.ServedModelName))
	if errors.Is(err, repository.ErrNotFound) {
		return AccessDecision{Decision: "not_found", HTTPStatus: 404, ReasonCode: "NOT_FOUND"}, nil
	}
	if err != nil {
		return AccessDecision{Decision: "policy_unavailable", HTTPStatus: 503, ReasonCode: "POLICY_BACKEND_UNAVAILABLE"}, err
	}
	expected, ok := OpenAIPathForTask(resolved.DesiredSpec.ExecutionProfile.Task)
	if !ok || normalizeOpenAIPath(in.OpenAIPath) != expected {
		return AccessDecision{Decision: "not_found", HTTPStatus: 404, ReasonCode: "NOT_FOUND"}, nil
	}
	decision := AccessDecision{Decision: "allow", HTTPStatus: 200, ReasonCode: "NO_CUSTOM_POLICY", InferenceServiceID: resolved.ID}
	policies, err := s.store.ListAccessPolicies(ctx, in.TenantID)
	if err != nil {
		return AccessDecision{Decision: "policy_unavailable", HTTPStatus: 503, ReasonCode: "POLICY_BACKEND_UNAVAILABLE", InferenceServiceID: resolved.ID}, err
	}
	policy, ok := domain.SelectPolicy(policies, resolved.ID, in.APIKeyID)
	if !ok {
		return decision, nil
	}
	decision.PolicyID, decision.ReasonCode = policy.ID, "ALLOWED"
	if contains(policy.Access.DenyAPIKeyIDs, in.APIKeyID.String()) || (!policy.Access.AllowAllTenantKeys && len(policy.Access.AllowAPIKeyIDs) > 0 && !contains(policy.Access.AllowAPIKeyIDs, in.APIKeyID.String())) {
		decision.Decision, decision.HTTPStatus, decision.ReasonCode = "deny", 403, "POLICY_ACCESS_DENIED"
		s.record(ctx, in, decision)
		return decision, nil
	}
	if s.limiter == nil && (policy.RateLimits.QPS > 0 || policy.RateLimits.RPM > 0 || policy.Concurrency.MaxInFlight > 0) {
		decision.Decision, decision.HTTPStatus, decision.ReasonCode = "policy_unavailable", 503, "POLICY_LIMITER_UNAVAILABLE"
		s.record(ctx, in, decision)
		return decision, ErrPolicyUnavailable
	}
	if policy.RateLimits.QPS > 0 {
		allowed, retry, limitErr := s.limiter.AllowFixedWindow(ctx, rateKey(in, resolved.ID, policy.ID, "qps"), policy.RateLimits.QPS, time.Second, s.now())
		if limitErr != nil {
			decision.Decision, decision.HTTPStatus, decision.ReasonCode = "policy_unavailable", 503, "POLICY_LIMITER_UNAVAILABLE"
			s.record(ctx, in, decision)
			return decision, limitErr
		}
		if !allowed {
			decision.Decision, decision.HTTPStatus, decision.ReasonCode, decision.RetryAfter = "rate_limited", 429, "POLICY_QPS_LIMIT", retry
			s.record(ctx, in, decision)
			return decision, nil
		}
	}
	if policy.RateLimits.RPM > 0 {
		allowed, retry, limitErr := s.limiter.AllowFixedWindow(ctx, rateKey(in, resolved.ID, policy.ID, "rpm"), policy.RateLimits.RPM, time.Minute, s.now())
		if limitErr != nil {
			decision.Decision, decision.HTTPStatus, decision.ReasonCode = "policy_unavailable", 503, "POLICY_LIMITER_UNAVAILABLE"
			s.record(ctx, in, decision)
			return decision, limitErr
		}
		if !allowed {
			decision.Decision, decision.HTTPStatus, decision.ReasonCode, decision.RetryAfter = "rate_limited", 429, "POLICY_RPM_LIMIT", retry
			s.record(ctx, in, decision)
			return decision, nil
		}
	}
	if policy.Concurrency.MaxInFlight > 0 {
		ttl := time.Duration(policy.Concurrency.LeaseTTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = 60 * time.Second
		}
		lease, allowed, retry, limitErr := s.limiter.AcquireLease(ctx, rateKey(in, resolved.ID, policy.ID, "concurrency"), policy.Concurrency.MaxInFlight, ttl, s.now())
		if limitErr != nil {
			decision.Decision, decision.HTTPStatus, decision.ReasonCode = "policy_unavailable", 503, "POLICY_LIMITER_UNAVAILABLE"
			s.record(ctx, in, decision)
			return decision, limitErr
		}
		if !allowed {
			decision.Decision, decision.HTTPStatus, decision.ReasonCode, decision.RetryAfter = "concurrency_limited", 429, "POLICY_CONCURRENCY_LIMIT", retry
			s.record(ctx, in, decision)
			return decision, nil
		}
		decision.LeaseID = lease
	}
	return decision, nil
}

func (s *AccessPolicyService) ReleaseAccessLease(ctx context.Context, leaseID string) error {
	if s.limiter == nil || leaseID == "" {
		return nil
	}
	return s.limiter.ReleaseLease(ctx, leaseID)
}
func (s *AccessPolicyService) record(ctx context.Context, in AccessCheckInput, decision AccessDecision) {
	if s.store == nil {
		return
	}
	policy := decision.PolicyID
	serviceID := decision.InferenceServiceID
	_ = s.store.RecordAccessPolicyEvent(ctx, domain.AccessPolicyEvent{TenantID: in.TenantID, PolicyID: &policy, InferenceServiceID: &serviceID, APIKeyID: &in.APIKeyID, KeyPrefix: in.KeyPrefix, RequestID: in.RequestID, OpenAIPath: normalizeOpenAIPath(in.OpenAIPath), ExternalModel: in.ServedModelName, Decision: decision.Decision, ReasonCode: decision.ReasonCode, HTTPStatus: decision.HTTPStatus, RetryAfterSeconds: int(decision.RetryAfter.Seconds())})
}
func OpenAIPathForTask(task domain.InferenceTask) (string, bool) {
	switch task {
	case domain.InferenceTaskGenerate:
		return "/v1/chat/completions", true
	case domain.InferenceTaskEmbed:
		return "/v1/embeddings", true
	default:
		return "", false
	}
}

func normalizeOpenAIPath(path string) string {
	path, _, _ = strings.Cut(path, "?")
	return path
}

func rateKey(in AccessCheckInput, serviceID, policyID uuid.UUID, dimension string) string {
	return in.TenantID.String() + "/" + serviceID.String() + "/" + in.APIKeyID.String() + "/" + policyID.String() + "/" + dimension
}
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
