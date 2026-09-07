// Package iam adapts the frozen Direct P2 IAM gRPC contract to the ANI
// product-level TargetIAM port.
package iam

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	iamv1 "github.com/kubercloud/ani/pkg/generated/pb/iam/v1"
	"github.com/kubercloud/ani/pkg/ports"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const decisionTimeout = 500 * time.Millisecond

type MutualTLSConfig struct {
	ServerName      string
	CAFile          string
	CertificateFile string
	PrivateKeyFile  string
}

type grpcClient struct {
	authentication iamv1.AuthenticationServiceClient
	authorization  iamv1.AuthorizationServiceClient
}

func NewGRPCClient(connection grpc.ClientConnInterface) ports.TargetIAM {
	return &grpcClient{
		authentication: iamv1.NewAuthenticationServiceClient(connection),
		authorization:  iamv1.NewAuthorizationServiceClient(connection),
	}
}

func Dial(address string, config MutualTLSConfig) (ports.TargetIAM, func() error, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, nil, errors.New("target IAM address is required")
	}
	config.ServerName = strings.TrimSpace(config.ServerName)
	config.CAFile = strings.TrimSpace(config.CAFile)
	config.CertificateFile = strings.TrimSpace(config.CertificateFile)
	config.PrivateKeyFile = strings.TrimSpace(config.PrivateKeyFile)
	if config.ServerName == "" {
		return nil, nil, errors.New("target IAM TLS server name is required")
	}
	if config.CAFile == "" {
		return nil, nil, errors.New("target IAM TLS CA file is required")
	}
	if config.CertificateFile == "" {
		return nil, nil, errors.New("target IAM TLS certificate file is required")
	}
	if config.PrivateKeyFile == "" {
		return nil, nil, errors.New("target IAM TLS private key file is required")
	}
	caPEM, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read target IAM TLS CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, nil, errors.New("parse target IAM TLS CA: no certificates found")
	}
	certificate, err := tls.LoadX509KeyPair(config.CertificateFile, config.PrivateKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load target IAM TLS client key pair: %w", err)
	}
	transport := credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		ServerName:   config.ServerName,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
	})
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(transport),
		grpc.WithDisableRetry(),
	)
	if err != nil {
		return nil, nil, err
	}
	return NewGRPCClient(connection), connection.Close, nil
}

func (c *grpcClient) PasswordLogin(
	ctx context.Context,
	request ports.TargetIAMPasswordLoginRequest,
) (ports.TargetIAMPasswordLoginResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, decisionTimeout)
	defer cancel()
	response, err := c.authentication.PasswordLogin(callCtx, &iamv1.PasswordLoginRequest{
		Account:        request.Account,
		Password:       request.Password,
		Audience:       toProtoAudience(request.Audience),
		Boundary:       toProtoBoundary(request.Boundary),
		DeviceName:     request.DeviceName,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return ports.TargetIAMPasswordLoginResult{}, mapTargetIAMError(err)
	}
	if response == nil {
		return ports.TargetIAMPasswordLoginResult{}, nil
	}
	return ports.TargetIAMPasswordLoginResult{
		AccessToken:      response.GetAccessToken(),
		ExpiresInSeconds: response.GetExpiresInSeconds(),
		RefreshToken:     response.GetRefreshToken(),
		RefreshExpiresAt: protoTime(response.GetRefreshExpiresAt()),
		Principal:        toPortPrincipal(response.GetPrincipal()),
		Session:          toPortSession(response.GetSession()),
		Grant:            toPortGrant(response.GetGrant()),
	}, nil
}

func (c *grpcClient) CheckPermission(
	ctx context.Context,
	request ports.TargetIAMCheckPermissionRequest,
) (ports.TargetIAMAuthorizationDecision, error) {
	callCtx, cancel := context.WithTimeout(ctx, decisionTimeout)
	defer cancel()
	response, err := c.authorization.CheckPermission(callCtx, &iamv1.CheckPermissionRequest{
		Credential:     &iamv1.BearerCredential{Value: request.Credential},
		OperationId:    request.OperationID,
		PolicyRevision: request.PolicyRevision,
		Target:         &iamv1.AuthorizationTarget{TenantId: request.TenantID},
	})
	if err != nil {
		return ports.TargetIAMAuthorizationDecision{}, mapTargetIAMError(err)
	}
	decision := response.GetDecision()
	if decision == nil {
		return ports.TargetIAMAuthorizationDecision{}, nil
	}
	return ports.TargetIAMAuthorizationDecision{
		Present:        true,
		DecisionID:     decision.GetDecisionId(),
		Allowed:        decision.GetAllowed(),
		PolicyRevision: decision.GetPolicyRevision(),
		Principal:      toPortPrincipal(decision.GetPrincipal()),
	}, nil
}

var _ ports.TargetIAM = (*grpcClient)(nil)

func toProtoAudience(value ports.TargetIAMAudience) iamv1.Audience {
	return map[ports.TargetIAMAudience]iamv1.Audience{
		ports.TargetIAMAudienceConsole: iamv1.Audience_AUDIENCE_CONSOLE,
		ports.TargetIAMAudienceBoss:    iamv1.Audience_AUDIENCE_BOSS,
	}[value]
}

func toProtoBoundary(value ports.TargetIAMBoundary) *iamv1.Boundary {
	switch value.Type {
	case ports.TargetIAMBoundaryTenant:
		return &iamv1.Boundary{Boundary: &iamv1.Boundary_Tenant{
			Tenant: &iamv1.TenantBoundary{TenantId: value.TenantID},
		}}
	case ports.TargetIAMBoundaryPlatform:
		return &iamv1.Boundary{Boundary: &iamv1.Boundary_Platform{
			Platform: &iamv1.PlatformBoundary{},
		}}
	default:
		return nil
	}
}

func toPortBoundary(value *iamv1.Boundary) ports.TargetIAMBoundary {
	if value.GetTenant() != nil {
		return ports.TargetIAMBoundary{Type: ports.TargetIAMBoundaryTenant, TenantID: value.GetTenant().GetTenantId()}
	}
	if value.GetPlatform() != nil {
		return ports.TargetIAMBoundary{Type: ports.TargetIAMBoundaryPlatform}
	}
	return ports.TargetIAMBoundary{}
}

func toPortPrincipal(value *iamv1.PrincipalContext) ports.TargetIAMPrincipal {
	if value == nil {
		return ports.TargetIAMPrincipal{}
	}
	methods := make([]ports.TargetIAMAuthnMethod, 0, len(value.GetAuthnMethods()))
	for _, method := range value.GetAuthnMethods() {
		methods = append(methods, toPortAuthnMethod(method))
	}
	return ports.TargetIAMPrincipal{
		ID: value.GetPrincipalId(),
		Type: map[iamv1.PrincipalType]ports.TargetIAMPrincipalType{
			iamv1.PrincipalType_PRINCIPAL_TYPE_HUMAN:   ports.TargetIAMPrincipalHuman,
			iamv1.PrincipalType_PRINCIPAL_TYPE_SERVICE: ports.TargetIAMPrincipalService,
		}[value.GetPrincipalType()],
		Status: map[iamv1.PrincipalStatus]ports.TargetIAMPrincipalStatus{
			iamv1.PrincipalStatus_PRINCIPAL_STATUS_ACTIVE:   ports.TargetIAMPrincipalActive,
			iamv1.PrincipalStatus_PRINCIPAL_STATUS_DISABLED: ports.TargetIAMPrincipalDisabled,
		}[value.GetPrincipalStatus()],
		Boundary:     toPortBoundary(value.GetBoundary()),
		SessionID:    value.GetSessionId(),
		GrantID:      value.GetGrantId(),
		AuthnMethods: methods,
	}
}

func toPortGrant(value *iamv1.SessionGrantSummary) ports.TargetIAMGrant {
	if value == nil {
		return ports.TargetIAMGrant{}
	}
	return ports.TargetIAMGrant{
		ID:       value.GetGrantId(),
		Boundary: toPortBoundary(value.GetBoundary()),
		Version:  value.GetVersion(),
		Status: map[iamv1.GrantStatus]ports.TargetIAMGrantStatus{
			iamv1.GrantStatus_GRANT_STATUS_ACTIVE:  ports.TargetIAMGrantActive,
			iamv1.GrantStatus_GRANT_STATUS_REVOKED: ports.TargetIAMGrantRevoked,
			iamv1.GrantStatus_GRANT_STATUS_EXPIRED: ports.TargetIAMGrantExpired,
		}[value.GetStatus()],
	}
}

func toPortSession(value *iamv1.SessionSummary) ports.TargetIAMSession {
	if value == nil {
		return ports.TargetIAMSession{}
	}
	grants := make([]ports.TargetIAMGrant, 0, len(value.GetGrants()))
	for _, grant := range value.GetGrants() {
		grants = append(grants, toPortGrant(grant))
	}
	methods := make([]ports.TargetIAMAuthnMethod, 0, len(value.GetAuthnMethods()))
	for _, method := range value.GetAuthnMethods() {
		methods = append(methods, toPortAuthnMethod(method))
	}
	return ports.TargetIAMSession{
		ID: value.GetSessionId(),
		Status: map[iamv1.SessionStatus]ports.TargetIAMSessionStatus{
			iamv1.SessionStatus_SESSION_STATUS_ACTIVE:  ports.TargetIAMSessionActive,
			iamv1.SessionStatus_SESSION_STATUS_REVOKED: ports.TargetIAMSessionRevoked,
			iamv1.SessionStatus_SESSION_STATUS_EXPIRED: ports.TargetIAMSessionExpired,
		}[value.GetStatus()],
		Grants:            grants,
		AuthnMethods:      methods,
		DeviceName:        value.GetDeviceName(),
		CreatedAt:         protoTime(value.GetCreatedAt()),
		IdleExpiresAt:     protoTime(value.GetIdleExpiresAt()),
		AbsoluteExpiresAt: protoTime(value.GetAbsoluteExpiresAt()),
	}
}

func toPortAuthnMethod(value iamv1.AuthnMethod) ports.TargetIAMAuthnMethod {
	return map[iamv1.AuthnMethod]ports.TargetIAMAuthnMethod{
		iamv1.AuthnMethod_AUTHN_METHOD_PASSWORD:      ports.TargetIAMAuthnPassword,
		iamv1.AuthnMethod_AUTHN_METHOD_OIDC:          ports.TargetIAMAuthnOIDC,
		iamv1.AuthnMethod_AUTHN_METHOD_API_KEY:       ports.TargetIAMAuthnAPIKey,
		iamv1.AuthnMethod_AUTHN_METHOD_SERVICE_TOKEN: ports.TargetIAMAuthnServiceToken,
	}[value]
}

func protoTime(value *timestamppb.Timestamp) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.AsTime()
}

func mapTargetIAMError(err error) error {
	grpcStatus := status.Convert(err)
	kind := map[codes.Code]ports.TargetIAMFailureKind{
		codes.Unauthenticated:   ports.TargetIAMFailureUnauthenticated,
		codes.PermissionDenied:  ports.TargetIAMFailurePermissionDenied,
		codes.ResourceExhausted: ports.TargetIAMFailureResourceExhausted,
		codes.DeadlineExceeded:  ports.TargetIAMFailureDeadlineExceeded,
		codes.AlreadyExists:     ports.TargetIAMFailureAlreadyExists,
		codes.Aborted:           ports.TargetIAMFailureAborted,
		codes.InvalidArgument:   ports.TargetIAMFailureInvalidArgument,
		codes.Unavailable:       ports.TargetIAMFailureUnavailable,
	}[grpcStatus.Code()]
	if kind == "" {
		kind = ports.TargetIAMFailureUnknown
	}
	failure := &ports.TargetIAMError{Kind: kind}
	for _, detail := range grpcStatus.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.GetDomain() != "iam.ani.internal" {
			continue
		}
		failure.Reason = info.GetReason()
		failure.Metadata = make(map[string]string, len(info.GetMetadata()))
		for key, value := range info.GetMetadata() {
			failure.Metadata[key] = value
		}
		break
	}
	return failure
}
