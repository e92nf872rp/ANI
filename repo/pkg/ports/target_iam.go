package ports

import (
	"context"
	"fmt"
	"time"
)

// TargetIAM is the Gateway seam for the isolated Direct P2 IAM tracer.
// Callers provide product intent; the adapter owns gRPC, mTLS, deadlines, and
// frozen protobuf translation.
type TargetIAM interface {
	PasswordLogin(context.Context, TargetIAMPasswordLoginRequest) (TargetIAMPasswordLoginResult, error)
	CheckPermission(context.Context, TargetIAMCheckPermissionRequest) (TargetIAMAuthorizationDecision, error)
}

type TargetIAMFailureKind string

const (
	TargetIAMFailureUnknown           TargetIAMFailureKind = "unknown"
	TargetIAMFailureUnauthenticated   TargetIAMFailureKind = "unauthenticated"
	TargetIAMFailurePermissionDenied  TargetIAMFailureKind = "permission_denied"
	TargetIAMFailureResourceExhausted TargetIAMFailureKind = "resource_exhausted"
	TargetIAMFailureDeadlineExceeded  TargetIAMFailureKind = "deadline_exceeded"
	TargetIAMFailureAlreadyExists     TargetIAMFailureKind = "already_exists"
	TargetIAMFailureAborted           TargetIAMFailureKind = "aborted"
	TargetIAMFailureInvalidArgument   TargetIAMFailureKind = "invalid_argument"
	TargetIAMFailureUnavailable       TargetIAMFailureKind = "unavailable"
)

// TargetIAMError is the transport-neutral failure returned by a TargetIAM
// adapter. Reason and Metadata retain the stable internal IAM error contract;
// implementation details from the remote status message are not exposed.
type TargetIAMError struct {
	Kind     TargetIAMFailureKind
	Reason   string
	Metadata map[string]string
}

func (e *TargetIAMError) Error() string {
	if e == nil {
		return "target IAM request failed"
	}
	if e.Reason != "" {
		return fmt.Sprintf("target IAM request failed: %s", e.Reason)
	}
	return fmt.Sprintf("target IAM request failed: %s", e.Kind)
}

func NewTargetIAMError(kind TargetIAMFailureKind, reason string, metadata map[string]string) *TargetIAMError {
	return &TargetIAMError{Kind: kind, Reason: reason, Metadata: metadata}
}

type TargetIAMAudience string

const (
	TargetIAMAudienceConsole TargetIAMAudience = "console"
	TargetIAMAudienceBoss    TargetIAMAudience = "boss"
)

type TargetIAMBoundaryType string

const (
	TargetIAMBoundaryTenant   TargetIAMBoundaryType = "tenant"
	TargetIAMBoundaryPlatform TargetIAMBoundaryType = "platform"
)

type TargetIAMBoundary struct {
	Type     TargetIAMBoundaryType
	TenantID string
}

type TargetIAMPasswordLoginRequest struct {
	Account        string
	Password       string
	Audience       TargetIAMAudience
	Boundary       TargetIAMBoundary
	DeviceName     string
	IdempotencyKey string
}

type TargetIAMPrincipalType string

const (
	TargetIAMPrincipalHuman   TargetIAMPrincipalType = "human"
	TargetIAMPrincipalService TargetIAMPrincipalType = "service"
)

type TargetIAMPrincipalStatus string

const (
	TargetIAMPrincipalActive   TargetIAMPrincipalStatus = "active"
	TargetIAMPrincipalDisabled TargetIAMPrincipalStatus = "disabled"
)

type TargetIAMAuthnMethod string

const (
	TargetIAMAuthnPassword     TargetIAMAuthnMethod = "password"
	TargetIAMAuthnOIDC         TargetIAMAuthnMethod = "oidc"
	TargetIAMAuthnAPIKey       TargetIAMAuthnMethod = "api_key"
	TargetIAMAuthnServiceToken TargetIAMAuthnMethod = "service_token"
)

type TargetIAMGrantStatus string

const (
	TargetIAMGrantActive  TargetIAMGrantStatus = "active"
	TargetIAMGrantRevoked TargetIAMGrantStatus = "revoked"
	TargetIAMGrantExpired TargetIAMGrantStatus = "expired"
)

type TargetIAMSessionStatus string

const (
	TargetIAMSessionActive  TargetIAMSessionStatus = "active"
	TargetIAMSessionRevoked TargetIAMSessionStatus = "revoked"
	TargetIAMSessionExpired TargetIAMSessionStatus = "expired"
)

type TargetIAMPrincipal struct {
	ID           string
	Type         TargetIAMPrincipalType
	Status       TargetIAMPrincipalStatus
	Boundary     TargetIAMBoundary
	SessionID    string
	GrantID      string
	AuthnMethods []TargetIAMAuthnMethod
}

type TargetIAMGrant struct {
	ID       string
	Boundary TargetIAMBoundary
	Version  uint64
	Status   TargetIAMGrantStatus
}

type TargetIAMSession struct {
	ID                string
	Status            TargetIAMSessionStatus
	Grants            []TargetIAMGrant
	AuthnMethods      []TargetIAMAuthnMethod
	DeviceName        string
	CreatedAt         time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

type TargetIAMPasswordLoginResult struct {
	AccessToken      string
	ExpiresInSeconds uint32
	RefreshToken     string
	RefreshExpiresAt time.Time
	Principal        TargetIAMPrincipal
	Session          TargetIAMSession
	Grant            TargetIAMGrant
}

type TargetIAMCheckPermissionRequest struct {
	Credential     string
	OperationID    string
	PolicyRevision string
	TenantID       string
}

type TargetIAMAuthorizationDecision struct {
	Present        bool
	DecisionID     string
	Allowed        bool
	PolicyRevision string
	Principal      TargetIAMPrincipal
}
