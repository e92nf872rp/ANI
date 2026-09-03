package router

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	sessionv1 "github.com/zhangzhe-ctrl/ani-session-gateway/api/gen/session/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const defaultSessionGatewayTimeout = 5 * time.Second

type sessionGatewayIssuer struct {
	client  sessionv1.SessionServiceClient
	timeout time.Duration
}

func DialSessionGateway(addr string, timeout time.Duration) (*grpc.ClientConn, ports.InstanceSessionIssuer, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, nil, fmt.Errorf("session gateway gRPC address is empty")
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil || strings.TrimSpace(host) == "" {
		return nil, nil, fmt.Errorf("session gateway gRPC address is invalid")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, nil, fmt.Errorf("session gateway gRPC address is invalid")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("create session gateway gRPC client: %w", err)
	}
	return conn, NewSessionGatewayIssuer(sessionv1.NewSessionServiceClient(conn), timeout), nil
}

func NewSessionGatewayIssuer(client sessionv1.SessionServiceClient, timeout time.Duration) ports.InstanceSessionIssuer {
	if timeout <= 0 {
		timeout = defaultSessionGatewayTimeout
	}
	return &sessionGatewayIssuer{client: client, timeout: timeout}
}

func (i *sessionGatewayIssuer) CreateExecSession(ctx context.Context, request ports.InstanceExecSessionCreateRequest) (ports.InstanceExecSessionRecord, error) {
	response, err := i.createSession(ctx, &sessionv1.CreateSessionRequest{
		RequestId:      request.RequestID,
		IdempotencyKey: request.IdempotencyKey,
		Principal:      sessionPrincipal(request.TenantID, request.SubjectID),
		Target:         sessionTarget(request.InstanceID, request.WorkloadName, request.WorkloadKind),
		Mode: &sessionv1.CreateSessionRequest_Exec{Exec: &sessionv1.ExecOptions{
			Container: request.Container,
			Command:   append([]string(nil), request.Command...),
			Tty:       request.TTY,
			Rows:      int32(request.Rows),
			Cols:      int32(request.Cols),
		}},
	})
	if err != nil {
		return ports.InstanceExecSessionRecord{}, err
	}
	return ports.InstanceExecSessionRecord{
		ID:         response.GetSessionId(),
		InstanceID: request.InstanceID,
		WSURL:      response.GetConnectUrl(),
		ExpiresAt:  response.GetExpiresAt().AsTime(),
		DevProfile: sessionGatewayDevProfile(),
	}, nil
}

func (i *sessionGatewayIssuer) CreateConsoleSession(ctx context.Context, request ports.InstanceConsoleSessionCreateRequest) (ports.InstanceConsoleSessionRecord, error) {
	protocol := sessionv1.VMConsoleOptions_PROTOCOL_SERIAL
	if request.Protocol == "vnc" || request.Protocol == "novnc" {
		protocol = sessionv1.VMConsoleOptions_PROTOCOL_VNC
	}
	response, err := i.createSession(ctx, &sessionv1.CreateSessionRequest{
		RequestId:      request.RequestID,
		IdempotencyKey: request.IdempotencyKey,
		Principal:      sessionPrincipal(request.TenantID, request.SubjectID),
		Target:         sessionTarget(request.InstanceID, request.WorkloadName, request.WorkloadKind),
		Mode: &sessionv1.CreateSessionRequest_VmConsole{VmConsole: &sessionv1.VMConsoleOptions{
			Protocol:          protocol,
			RequestedProtocol: request.Protocol,
		}},
	})
	if err != nil {
		return ports.InstanceConsoleSessionRecord{}, err
	}
	return ports.InstanceConsoleSessionRecord{
		SessionID:  response.GetSessionId(),
		InstanceID: request.InstanceID,
		Protocol:   request.Protocol,
		ConnectURL: response.GetConnectUrl(),
		URL:        response.GetConnectUrl(),
		ExpiresAt:  response.GetExpiresAt().AsTime(),
		DevProfile: sessionGatewayDevProfile(),
	}, nil
}

func (i *sessionGatewayIssuer) createSession(ctx context.Context, request *sessionv1.CreateSessionRequest) (*sessionv1.CreateSessionResponse, error) {
	if i == nil || i.client == nil {
		return nil, fmt.Errorf("session gateway create session: %w", ports.ErrNotConfigured)
	}
	callCtx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()
	response, err := i.client.CreateSession(callCtx, request)
	if err != nil {
		return nil, mapSessionGatewayError(err)
	}
	if response == nil || response.GetSessionId() == "" || response.GetConnectUrl() == "" || response.GetExpiresAt() == nil || !response.GetExpiresAt().IsValid() {
		return nil, fmt.Errorf("session gateway create session: %w", ports.ErrSessionInternal)
	}
	return response, nil
}

func sessionPrincipal(tenantID, subjectID string) *sessionv1.Principal {
	return &sessionv1.Principal{TenantId: tenantID, SubjectId: subjectID}
}

func sessionTarget(instanceID, workloadName string, kind ports.WorkloadKind) *sessionv1.Target {
	return &sessionv1.Target{InstanceId: instanceID, WorkloadName: workloadName, WorkloadKind: sessionWorkloadKind(kind)}
}

func sessionWorkloadKind(kind ports.WorkloadKind) sessionv1.WorkloadKind {
	switch kind {
	case ports.WorkloadKindContainer:
		return sessionv1.WorkloadKind_WORKLOAD_KIND_CONTAINER
	case ports.WorkloadKindGPUContainer:
		return sessionv1.WorkloadKind_WORKLOAD_KIND_GPU_CONTAINER
	case ports.WorkloadKindSandbox:
		return sessionv1.WorkloadKind_WORKLOAD_KIND_SANDBOX
	case ports.WorkloadKindVM:
		return sessionv1.WorkloadKind_WORKLOAD_KIND_VM
	default:
		return sessionv1.WorkloadKind_WORKLOAD_KIND_UNSPECIFIED
	}
}

func mapSessionGatewayError(err error) error {
	var mapped error
	switch status.Code(err) {
	case codes.InvalidArgument:
		mapped = ports.ErrInvalid
	case codes.Unauthenticated, codes.PermissionDenied:
		mapped = ports.ErrInvalidCredentials
	case codes.NotFound:
		mapped = ports.ErrNotFound
	case codes.AlreadyExists:
		mapped = ports.ErrConflict
	case codes.FailedPrecondition:
		mapped = ports.ErrFailedPrecondition
	case codes.ResourceExhausted:
		mapped = ports.ErrSessionCapacity
	case codes.Canceled, codes.Unavailable, codes.DeadlineExceeded:
		mapped = ports.ErrUnavailable
	default:
		mapped = ports.ErrSessionInternal
	}
	return fmt.Errorf("session gateway create session: %w", mapped)
}

func sessionGatewayDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "real",
		Provider:     "ani-session-gateway",
		RealProvider: true,
		Reason:       "short-lived realtime session issued by ani-session-gateway",
	}
}

var _ ports.InstanceSessionIssuer = (*sessionGatewayIssuer)(nil)
