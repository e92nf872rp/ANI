package router

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	sessionv1 "github.com/zhangzhe-ctrl/ani-session-gateway/api/gen/session/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type recordingSessionServiceClient struct {
	request  *sessionv1.CreateSessionRequest
	response *sessionv1.CreateSessionResponse
	err      error
}

func TestSessionGatewayIssuerMapsConsoleModes(t *testing.T) {
	for _, tc := range []struct {
		requested string
		want      sessionv1.VMConsoleOptions_Protocol
	}{{"console", sessionv1.VMConsoleOptions_PROTOCOL_SERIAL}, {"serial", sessionv1.VMConsoleOptions_PROTOCOL_SERIAL}, {"vnc", sessionv1.VMConsoleOptions_PROTOCOL_VNC}, {"novnc", sessionv1.VMConsoleOptions_PROTOCOL_VNC}} {
		t.Run(tc.requested, func(t *testing.T) {
			client := &recordingSessionServiceClient{response: &sessionv1.CreateSessionResponse{
				SessionId: "session-console", ConnectUrl: "wss://sessions.example/console", ExpiresAt: timestamppb.Now(),
			}}
			issuer := NewSessionGatewayIssuer(client, time.Second)
			record, err := issuer.CreateConsoleSession(context.Background(), ports.InstanceConsoleSessionCreateRequest{
				RequestID: "req-console", IdempotencyKey: "idem-console", TenantID: "tenant-a", SubjectID: "user-a",
				InstanceID: "instance-vm", WorkloadName: "vm-a", WorkloadKind: ports.WorkloadKindVM, Protocol: tc.requested,
			})
			if err != nil {
				t.Fatalf("CreateConsoleSession() error = %v", err)
			}
			mode := client.request.GetVmConsole()
			if mode == nil || mode.GetProtocol() != tc.want || mode.GetRequestedProtocol() != tc.requested {
				t.Fatalf("vm_console = %+v", mode)
			}
			if record.Protocol != tc.requested || record.ConnectURL != client.response.GetConnectUrl() || record.URL != record.ConnectURL || !record.DevProfile.RealProvider {
				t.Fatalf("record = %+v", record)
			}
		})
	}
}

func TestSessionGatewayIssuerMapsTypedWorkloadKinds(t *testing.T) {
	for _, tc := range []struct {
		kind ports.WorkloadKind
		want sessionv1.WorkloadKind
	}{{ports.WorkloadKindContainer, sessionv1.WorkloadKind_WORKLOAD_KIND_CONTAINER}, {ports.WorkloadKindGPUContainer, sessionv1.WorkloadKind_WORKLOAD_KIND_GPU_CONTAINER}, {ports.WorkloadKindSandbox, sessionv1.WorkloadKind_WORKLOAD_KIND_SANDBOX}, {ports.WorkloadKindVM, sessionv1.WorkloadKind_WORKLOAD_KIND_VM}} {
		t.Run(string(tc.kind), func(t *testing.T) {
			if got := sessionWorkloadKind(tc.kind); got != tc.want {
				t.Fatalf("sessionWorkloadKind(%q) = %s, want %s", tc.kind, got, tc.want)
			}
		})
	}
}

func TestSessionGatewayIssuerMapsGRPCErrorsWithoutLeakingDetails(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		want error
	}{{codes.InvalidArgument, ports.ErrInvalid}, {codes.Unauthenticated, ports.ErrInvalidCredentials}, {codes.PermissionDenied, ports.ErrInvalidCredentials}, {codes.NotFound, ports.ErrNotFound}, {codes.AlreadyExists, ports.ErrConflict}, {codes.FailedPrecondition, ports.ErrFailedPrecondition}, {codes.ResourceExhausted, ports.ErrSessionCapacity}, {codes.Canceled, ports.ErrUnavailable}, {codes.Unavailable, ports.ErrUnavailable}, {codes.DeadlineExceeded, ports.ErrUnavailable}, {codes.Internal, ports.ErrSessionInternal}, {codes.Unknown, ports.ErrSessionInternal}} {
		t.Run(tc.code.String(), func(t *testing.T) {
			err := mapSessionGatewayError(status.Error(tc.code, "ticket=must-not-leak"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if got := err.Error(); got == "" || containsSensitiveDetail(got) {
				t.Fatalf("error leaked gRPC detail: %q", got)
			}
		})
	}
}

func containsSensitiveDetail(value string) bool {
	return strings.Contains(value, "ticket=must-not-leak")
}

type blockingSessionServiceClient struct{}

func (blockingSessionServiceClient) CreateSession(ctx context.Context, _ *sessionv1.CreateSessionRequest, _ ...grpc.CallOption) (*sessionv1.CreateSessionResponse, error) {
	<-ctx.Done()
	return nil, status.Error(codes.DeadlineExceeded, "deadline")
}

func TestSessionGatewayIssuerAppliesPerCallTimeout(t *testing.T) {
	issuer := NewSessionGatewayIssuer(blockingSessionServiceClient{}, 20*time.Millisecond)
	started := time.Now()
	_, err := issuer.CreateExecSession(context.Background(), ports.InstanceExecSessionCreateRequest{})
	if !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("call took %s, want bounded timeout", elapsed)
	}
}

type bufconnSessionServer struct {
	sessionv1.UnimplementedSessionServiceServer
	request *sessionv1.CreateSessionRequest
}

func (s *bufconnSessionServer) CreateSession(_ context.Context, request *sessionv1.CreateSessionRequest) (*sessionv1.CreateSessionResponse, error) {
	s.request = request
	return &sessionv1.CreateSessionResponse{SessionId: "buf-session", ConnectUrl: "wss://sessions.example/buf", ExpiresAt: timestamppb.Now()}, nil
}

func TestSessionGatewayIssuerBufconnContractAndConnectionShutdown(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	implementation := &bufconnSessionServer{}
	sessionv1.RegisterSessionServiceServer(server, implementation)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	issuer := NewSessionGatewayIssuer(sessionv1.NewSessionServiceClient(conn), time.Second)
	_, err = issuer.CreateExecSession(context.Background(), ports.InstanceExecSessionCreateRequest{
		RequestID: "req-buf", IdempotencyKey: "idem-buf", TenantID: "tenant-a", SubjectID: "user-a",
		InstanceID: "instance-a", WorkloadName: "pod-a", WorkloadKind: ports.WorkloadKindContainer,
	})
	if err != nil || implementation.request.GetRequestId() != "req-buf" {
		t.Fatalf("bufconn CreateExecSession error=%v request=%+v", err, implementation.request)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err = issuer.CreateExecSession(context.Background(), ports.InstanceExecSessionCreateRequest{})
	if !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("call after close error = %v, want unavailable", err)
	}
}

func (c *recordingSessionServiceClient) CreateSession(_ context.Context, request *sessionv1.CreateSessionRequest, _ ...grpc.CallOption) (*sessionv1.CreateSessionResponse, error) {
	c.request = request
	return c.response, c.err
}

func TestSessionGatewayIssuerMapsExecRequestAndResponse(t *testing.T) {
	expiresAt := time.Date(2026, 9, 2, 12, 30, 0, 0, time.UTC)
	client := &recordingSessionServiceClient{response: &sessionv1.CreateSessionResponse{
		SessionId:  "session-1",
		ConnectUrl: "wss://sessions.example/api/v1/realtime/sessions/session-1?ticket=redacted",
		ExpiresAt:  timestamppb.New(expiresAt),
	}}
	issuer := NewSessionGatewayIssuer(client, 5*time.Second)

	record, err := issuer.CreateExecSession(context.Background(), ports.InstanceExecSessionCreateRequest{
		RequestID:      "req-1",
		IdempotencyKey: "idem-1",
		TenantID:       "tenant-a",
		SubjectID:      "user-a",
		InstanceID:     "instance-1",
		WorkloadName:   "workload-1",
		WorkloadKind:   ports.WorkloadKindGPUContainer,
		Container:      "main",
		Command:        []string{"/bin/sh", "-lc", "id"},
		TTY:            true,
		Rows:           30,
		Cols:           120,
	})
	if err != nil {
		t.Fatalf("CreateExecSession() error = %v", err)
	}
	if client.request.GetRequestId() != "req-1" || client.request.GetIdempotencyKey() != "idem-1" {
		t.Fatalf("request identity = request_id %q idempotency_key %q", client.request.GetRequestId(), client.request.GetIdempotencyKey())
	}
	if principal := client.request.GetPrincipal(); principal.GetTenantId() != "tenant-a" || principal.GetSubjectId() != "user-a" {
		t.Fatalf("principal = %+v", principal)
	}
	if target := client.request.GetTarget(); target.GetInstanceId() != "instance-1" || target.GetWorkloadName() != "workload-1" || target.GetWorkloadKind() != sessionv1.WorkloadKind_WORKLOAD_KIND_GPU_CONTAINER {
		t.Fatalf("target = %+v", target)
	}
	exec := client.request.GetExec()
	if exec == nil || exec.GetContainer() != "main" || !exec.GetTty() || exec.GetRows() != 30 || exec.GetCols() != 120 {
		t.Fatalf("exec = %+v", exec)
	}
	if got := exec.GetCommand(); len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-lc" || got[2] != "id" {
		t.Fatalf("command = %#v", got)
	}
	if record.ID != "session-1" || record.InstanceID != "instance-1" || record.WSURL != client.response.GetConnectUrl() || !record.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("record = %+v", record)
	}
	if !record.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want real_provider=true", record.DevProfile)
	}
}
