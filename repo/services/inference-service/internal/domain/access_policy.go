package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrRawAPIKeyRejected = errors.New("raw API key is not accepted; use api key id")

type AccessPolicyStatus string

const (
	AccessPolicyEnabled  AccessPolicyStatus = "enabled"
	AccessPolicyDisabled AccessPolicyStatus = "disabled"
)

type AccessPolicyScopeType string

const (
	ScopeTenantDefault          AccessPolicyScopeType = "tenant_default"
	ScopeInferenceService       AccessPolicyScopeType = "inference_service"
	ScopeAPIKey                 AccessPolicyScopeType = "api_key"
	ScopeInferenceServiceAPIKey AccessPolicyScopeType = "inference_service_api_key"
)

type AccessPolicyScope struct {
	Type                AccessPolicyScopeType
	InferenceServiceIDs []uuid.UUID
	APIKeyIDs           []string
}
type AccessPolicyAccess struct {
	AllowAllTenantKeys bool
	AllowAPIKeyIDs     []string
	DenyAPIKeyIDs      []string
}
type AccessPolicyRateLimits struct {
	QPS int
	RPM int
}
type AccessPolicyConcurrency struct {
	MaxInFlight     int
	LeaseTTLSeconds int
}
type AccessPolicy struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Name        string
	Status      AccessPolicyStatus
	Description string
	Priority    int
	Scope       AccessPolicyScope
	Access      AccessPolicyAccess
	RateLimits  AccessPolicyRateLimits
	Concurrency AccessPolicyConcurrency
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type AccessPolicyEvent struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	PolicyID           *uuid.UUID
	InferenceServiceID *uuid.UUID
	APIKeyID           *uuid.UUID
	KeyPrefix          string
	RequestID          string
	OpenAIPath         string
	ExternalModel      string
	Decision           string
	ReasonCode         string
	HTTPStatus         int
	RetryAfterSeconds  int
	CreatedAt          time.Time
}

type AccessPolicyEventQuery struct {
	InferenceServiceID *uuid.UUID
	PolicyID           *uuid.UUID
	APIKeyID           *uuid.UUID
	Decision           string
	Limit              int
	Cursor             string
}

func (p AccessPolicy) Validate() error {
	if p.TenantID == uuid.Nil {
		return errors.New("tenant id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("policy name is required")
	}
	if p.Status != AccessPolicyEnabled && p.Status != AccessPolicyDisabled {
		return fmt.Errorf("invalid policy status %q", p.Status)
	}
	if p.Priority < 1 || p.Priority > 10000 {
		return errors.New("priority must be between 1 and 10000")
	}
	switch p.Scope.Type {
	case ScopeTenantDefault:
	case ScopeInferenceService:
		if len(p.Scope.InferenceServiceIDs) == 0 {
			return errors.New("inference service scope requires service ids")
		}
	case ScopeAPIKey:
		if len(p.Scope.APIKeyIDs) == 0 {
			return errors.New("api key scope requires key ids")
		}
	case ScopeInferenceServiceAPIKey:
		if len(p.Scope.InferenceServiceIDs) == 0 || len(p.Scope.APIKeyIDs) == 0 {
			return errors.New("service api key scope requires both ids")
		}
	default:
		return fmt.Errorf("invalid policy scope %q", p.Scope.Type)
	}
	for _, id := range p.Scope.APIKeyIDs {
		if strings.HasPrefix(id, "ani_") {
			return ErrRawAPIKeyRejected
		}
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("invalid api key id: %w", err)
		}
	}
	for _, id := range append(append([]string{}, p.Access.AllowAPIKeyIDs...), p.Access.DenyAPIKeyIDs...) {
		if strings.HasPrefix(id, "ani_") {
			return ErrRawAPIKeyRejected
		}
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("invalid api key id: %w", err)
		}
	}
	for _, id := range p.Scope.InferenceServiceIDs {
		if id == uuid.Nil {
			return errors.New("invalid inference service id")
		}
	}
	if p.RateLimits.QPS < 0 || p.RateLimits.RPM < 0 || p.Concurrency.MaxInFlight < 0 {
		return errors.New("limits cannot be negative")
	}
	if p.Concurrency.LeaseTTLSeconds < 0 || p.Concurrency.LeaseTTLSeconds > 3600 {
		return errors.New("lease ttl must be between 0 and 3600 seconds")
	}
	return nil
}

func (p AccessPolicy) Matches(serviceID uuid.UUID, keyID uuid.UUID) bool {
	if p.Status != AccessPolicyEnabled {
		return false
	}
	serviceMatch, keyMatch := true, true
	if p.Scope.Type == ScopeInferenceService || p.Scope.Type == ScopeInferenceServiceAPIKey {
		serviceMatch = containsUUID(p.Scope.InferenceServiceIDs, serviceID)
	}
	if p.Scope.Type == ScopeAPIKey || p.Scope.Type == ScopeInferenceServiceAPIKey {
		keyMatch = containsString(p.Scope.APIKeyIDs, keyID.String())
	}
	return serviceMatch && keyMatch
}

func MatchPolicies(policies []AccessPolicy, serviceID, keyID uuid.UUID) []AccessPolicy {
	matched := make([]AccessPolicy, 0, len(policies))
	for _, p := range policies {
		if p.Matches(serviceID, keyID) {
			matched = append(matched, p)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].Priority < matched[j].Priority })
	return matched
}

func policySpecificity(scope AccessPolicyScopeType) int {
	switch scope {
	case ScopeInferenceServiceAPIKey:
		return 4
	case ScopeAPIKey:
		return 3
	case ScopeInferenceService:
		return 2
	case ScopeTenantDefault:
		return 1
	default:
		return 0
	}
}

// SelectPolicy returns the most specific matching policy. Priority only
// resolves conflicts inside the same scope specificity.
func SelectPolicy(policies []AccessPolicy, serviceID, keyID uuid.UUID) (AccessPolicy, bool) {
	matched := MatchPolicies(policies, serviceID, keyID)
	sort.SliceStable(matched, func(i, j int) bool {
		left, right := policySpecificity(matched[i].Scope.Type), policySpecificity(matched[j].Scope.Type)
		if left != right {
			return left > right
		}
		if matched[i].Priority != matched[j].Priority {
			return matched[i].Priority < matched[j].Priority
		}
		return matched[i].ID.String() < matched[j].ID.String()
	})
	if len(matched) == 0 {
		return AccessPolicy{}, false
	}
	return matched[0], true
}

func DefaultAccessAllowed(_ []AccessPolicy) bool { return true }
func containsUUID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
