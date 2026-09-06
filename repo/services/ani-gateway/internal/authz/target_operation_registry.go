package authz

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Direct P2 keeps this registry separate from the currently deployed runtime
// policy until the Gateway vertical-slice ticket wires one protected operation.
type TargetBackendOwner string

const (
	TargetOwnerGateway     TargetBackendOwner = "gateway"
	TargetOwnerCoreControl TargetBackendOwner = "core-control"
	TargetOwnerIAM         TargetBackendOwner = "iam"
)

type TargetAuthClassification string

const (
	TargetAuthClassificationPublic        TargetAuthClassification = "public"
	TargetAuthClassificationAuthenticated TargetAuthClassification = "authenticated"
	TargetAuthClassificationAuthorized    TargetAuthClassification = "authorized"
)

type TargetPrincipalKind string
type TargetCredentialKind string
type TargetPermissionScope string
type TargetPermissionResource string
type TargetPermissionAction string
type TargetObligationType string
type TargetObligationHandler string
type TargetIAMDecision string

const (
	TargetIAMDecisionNone              TargetIAMDecision = "none"
	TargetIAMDecisionValidatePrincipal TargetIAMDecision = "validate_principal"
	TargetIAMDecisionCheckPermission   TargetIAMDecision = "check_permission"
)

type TargetObligation struct {
	Type    TargetObligationType
	Handler TargetObligationHandler
}

type TargetOperationPolicy struct {
	OperationID        string
	Method             string
	PathTemplate       string
	GatewayHandler     string
	BackendOwner       TargetBackendOwner
	AuthClassification TargetAuthClassification
	IAMDecision        TargetIAMDecision
	IAMDecisionCalls   int
	PrincipalKinds     []TargetPrincipalKind
	CredentialKinds    []TargetCredentialKind
	PermissionResource TargetPermissionResource
	PermissionActions  []TargetPermissionAction
	PermissionScope    TargetPermissionScope
	Obligations        []TargetObligation
}

type TargetStableError struct {
	ResponseComponent string
	Code              string
}

type TargetStableErrorReason struct {
	HTTPStatus        int
	ResponseComponent string
	Code              string
}

type TargetRegistryFailure struct {
	Reason string
}

func (e *TargetRegistryFailure) Error() string { return e.Reason }

const (
	TargetReasonPolicyMismatch        = "AUTHZ_POLICY_MISMATCH"
	TargetReasonOperationUnregistered = "AUTHZ_OPERATION_UNREGISTERED"
)

type TargetOperationRegistry struct {
	revision    string
	byRoute     map[string]TargetOperationPolicy
	byOperation map[string]TargetOperationPolicy
}

func NewTargetOperationRegistry(expectedRevision string) (TargetOperationRegistry, error) {
	if expectedRevision == "" || expectedRevision != TargetPolicyRevision {
		return TargetOperationRegistry{}, &TargetRegistryFailure{Reason: TargetReasonPolicyMismatch}
	}
	registry := TargetOperationRegistry{
		revision:    TargetPolicyRevision,
		byRoute:     make(map[string]TargetOperationPolicy, len(generatedTargetOperationPolicies)),
		byOperation: make(map[string]TargetOperationPolicy, len(generatedTargetOperationPolicies)),
	}
	for key, generated := range generatedTargetOperationPolicies {
		policy := cloneTargetOperationPolicy(generated)
		if err := validateTargetOperationPolicy(key, policy); err != nil {
			return TargetOperationRegistry{}, err
		}
		if _, exists := registry.byOperation[policy.OperationID]; exists {
			return TargetOperationRegistry{}, fmt.Errorf("duplicate target operation id %q", policy.OperationID)
		}
		registry.byRoute[key] = policy
		registry.byOperation[policy.OperationID] = cloneTargetOperationPolicy(policy)
	}
	return registry, nil
}

func cloneTargetOperationPolicy(policy TargetOperationPolicy) TargetOperationPolicy {
	policy.PrincipalKinds = append([]TargetPrincipalKind(nil), policy.PrincipalKinds...)
	policy.CredentialKinds = append([]TargetCredentialKind(nil), policy.CredentialKinds...)
	policy.PermissionActions = append([]TargetPermissionAction(nil), policy.PermissionActions...)
	policy.Obligations = append([]TargetObligation(nil), policy.Obligations...)
	return policy
}

func validateTargetOperationPolicy(key string, policy TargetOperationPolicy) error {
	if policy.OperationID == "" || policy.Method == "" || policy.PathTemplate == "" || policy.GatewayHandler == "" {
		return fmt.Errorf("target policy %s has incomplete identity", key)
	}
	if policy.BackendOwner != TargetOwnerGateway && policy.BackendOwner != TargetOwnerCoreControl && policy.BackendOwner != TargetOwnerIAM {
		return fmt.Errorf("target policy %s has invalid owner %q", key, policy.BackendOwner)
	}
	switch policy.AuthClassification {
	case TargetAuthClassificationPublic:
		if policy.IAMDecision != TargetIAMDecisionNone || policy.IAMDecisionCalls != 0 || len(policy.PrincipalKinds) != 0 || len(policy.CredentialKinds) != 0 || policy.PermissionResource != "" {
			return fmt.Errorf("target public policy %s must make zero IAM decisions", key)
		}
	case TargetAuthClassificationAuthenticated:
		if policy.IAMDecision != TargetIAMDecisionValidatePrincipal || policy.IAMDecisionCalls != 1 || len(policy.PrincipalKinds) == 0 || len(policy.CredentialKinds) == 0 || policy.PermissionResource != "" {
			return fmt.Errorf("target authenticated policy %s is incomplete", key)
		}
	case TargetAuthClassificationAuthorized:
		if policy.IAMDecision != TargetIAMDecisionCheckPermission || policy.IAMDecisionCalls != 1 || len(policy.PrincipalKinds) == 0 || len(policy.CredentialKinds) == 0 || policy.PermissionResource == "" || len(policy.PermissionActions) == 0 || policy.PermissionScope == "" {
			return fmt.Errorf("target authorized policy %s is incomplete", key)
		}
	default:
		return fmt.Errorf("target policy %s has invalid auth classification %q", key, policy.AuthClassification)
	}
	for _, obligation := range policy.Obligations {
		owner, ok := generatedTargetObligationHandlers[obligation.Handler]
		_, typeOK := generatedTargetObligationTypes[obligation.Type]
		if !ok || !typeOK || owner != policy.BackendOwner || obligation.Type == "" {
			return fmt.Errorf("target policy %s has unhandled obligation %q", key, obligation.Handler)
		}
	}
	if policy.PermissionResource != "" {
		if _, ok := generatedTargetPermissionResources[policy.PermissionResource]; !ok {
			return fmt.Errorf("target policy %s has unregistered permission resource %q", key, policy.PermissionResource)
		}
	}
	for _, action := range policy.PermissionActions {
		if _, ok := generatedTargetPermissionActions[action]; !ok {
			return fmt.Errorf("target policy %s has unregistered permission action %q", key, action)
		}
	}
	return nil
}

func (r TargetOperationRegistry) Revision() string { return r.revision }

func (r TargetOperationRegistry) Lookup(method, pathTemplate, policyRevision string) (TargetOperationPolicy, error) {
	if policyRevision == "" || policyRevision != r.revision {
		return TargetOperationPolicy{}, &TargetRegistryFailure{Reason: TargetReasonPolicyMismatch}
	}
	policy, ok := r.byRoute[method+" "+pathTemplate]
	if !ok {
		return TargetOperationPolicy{}, &TargetRegistryFailure{Reason: TargetReasonOperationUnregistered}
	}
	return cloneTargetOperationPolicy(policy), nil
}

func (r TargetOperationRegistry) LookupOperation(operationID, policyRevision string) (TargetOperationPolicy, error) {
	if policyRevision == "" || policyRevision != r.revision {
		return TargetOperationPolicy{}, &TargetRegistryFailure{Reason: TargetReasonPolicyMismatch}
	}
	policy, ok := r.byOperation[operationID]
	if !ok {
		return TargetOperationPolicy{}, &TargetRegistryFailure{Reason: TargetReasonOperationUnregistered}
	}
	return cloneTargetOperationPolicy(policy), nil
}

func IsTargetRegistryFailure(err error, reason string) bool {
	var failure *TargetRegistryFailure
	return errors.As(err, &failure) && failure.Reason == reason
}

func TargetStableErrorForStatus(status int) (TargetStableError, bool) {
	stable, ok := generatedTargetStableErrors[status]
	return stable, ok
}

func TargetStableErrorForReason(reason string) (TargetStableErrorReason, bool) {
	stable, ok := generatedTargetStableErrorReasons[reason]
	return stable, ok
}

// TargetRetryAfter validates the frozen Retry-After metadata shared by target
// authentication and authorization failures.
func TargetRetryAfter(metadata map[string]string) (string, bool) {
	raw := metadata["retry_after_seconds"]
	if raw == "" || strings.Trim(raw, "0123456789") != "" {
		return "", false
	}
	seconds, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || seconds == 0 {
		return "", false
	}
	return strconv.FormatUint(seconds, 10), true
}
