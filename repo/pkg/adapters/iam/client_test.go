package iam

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	iamv1 "github.com/kubercloud/ani/pkg/generated/pb/iam/v1"
	"github.com/kubercloud/ani/pkg/ports"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testTenantID    = "22222222-2222-2222-2222-222222222222"
	testPrincipalID = "33333333-3333-3333-3333-333333333333"
	testSessionID   = "44444444-4444-4444-4444-444444444444"
	testGrantID     = "55555555-5555-5555-5555-555555555555"
)

type passwordLoginServer struct {
	iamv1.UnimplementedAuthenticationServiceServer
	request  *iamv1.PasswordLoginRequest
	deadline time.Duration
}

type failingPasswordLoginServer struct {
	iamv1.UnimplementedAuthenticationServiceServer
}

func (failingPasswordLoginServer) PasswordLogin(
	context.Context,
	*iamv1.PasswordLoginRequest,
) (*iamv1.PasswordLoginResponse, error) {
	value, err := status.New(codes.ResourceExhausted, "internal detail must not leak").WithDetails(
		&errdetails.ErrorInfo{
			Reason: "AUTH_RATE_LIMITED", Domain: "iam.ani.internal",
			Metadata: map[string]string{"retry_after_seconds": "900"},
		},
	)
	if err != nil {
		return nil, err
	}
	return nil, value.Err()
}

func (s *passwordLoginServer) PasswordLogin(
	ctx context.Context,
	request *iamv1.PasswordLoginRequest,
) (*iamv1.PasswordLoginResponse, error) {
	s.request = request
	s.deadline = remainingDeadline(ctx)
	createdAt := time.Date(2026, time.September, 6, 2, 0, 0, 0, time.UTC)
	boundary := &iamv1.Boundary{Boundary: &iamv1.Boundary_Tenant{
		Tenant: &iamv1.TenantBoundary{TenantId: testTenantID},
	}}
	grant := &iamv1.SessionGrantSummary{
		GrantId: testGrantID, Boundary: boundary, Version: 7,
		Status: iamv1.GrantStatus_GRANT_STATUS_ACTIVE,
	}
	return &iamv1.PasswordLoginResponse{
		AccessToken:      "target-access",
		ExpiresInSeconds: 900,
		RefreshToken:     "target-refresh",
		RefreshExpiresAt: timestamppb.New(createdAt.Add(30 * 24 * time.Hour)),
		Principal: &iamv1.PrincipalContext{
			PrincipalId: testPrincipalID, PrincipalType: iamv1.PrincipalType_PRINCIPAL_TYPE_HUMAN,
			PrincipalStatus: iamv1.PrincipalStatus_PRINCIPAL_STATUS_ACTIVE, Boundary: boundary,
			SessionId: testSessionID, GrantId: testGrantID,
			AuthnMethods: []iamv1.AuthnMethod{iamv1.AuthnMethod_AUTHN_METHOD_PASSWORD},
		},
		Session: &iamv1.SessionSummary{
			SessionId: testSessionID, Status: iamv1.SessionStatus_SESSION_STATUS_ACTIVE,
			Grants:       []*iamv1.SessionGrantSummary{grant},
			AuthnMethods: []iamv1.AuthnMethod{iamv1.AuthnMethod_AUTHN_METHOD_PASSWORD},
			DeviceName:   "browser-a", CreatedAt: timestamppb.New(createdAt),
			IdleExpiresAt:     timestamppb.New(createdAt.Add(24 * time.Hour)),
			AbsoluteExpiresAt: timestamppb.New(createdAt.Add(30 * 24 * time.Hour)),
		},
		Grant: grant,
	}, nil
}

type deadlineAuthorizationServer struct {
	iamv1.UnimplementedAuthorizationServiceServer
	deadline time.Duration
	calls    atomic.Int32
	block    bool
}

func (s *deadlineAuthorizationServer) CheckPermission(
	ctx context.Context,
	request *iamv1.CheckPermissionRequest,
) (*iamv1.CheckPermissionResponse, error) {
	s.calls.Add(1)
	s.deadline = remainingDeadline(ctx)
	if s.block {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "deadline")
	}
	return &iamv1.CheckPermissionResponse{Decision: &iamv1.AuthorizationDecision{
		DecisionId:     "11111111-1111-1111-1111-111111111111",
		Allowed:        true,
		PolicyRevision: request.GetPolicyRevision(),
		Principal: &iamv1.PrincipalContext{
			PrincipalId:     testPrincipalID,
			PrincipalType:   iamv1.PrincipalType_PRINCIPAL_TYPE_HUMAN,
			PrincipalStatus: iamv1.PrincipalStatus_PRINCIPAL_STATUS_ACTIVE,
			Boundary: &iamv1.Boundary{Boundary: &iamv1.Boundary_Tenant{
				Tenant: &iamv1.TenantBoundary{TenantId: testTenantID},
			}},
			SessionId:    testSessionID,
			GrantId:      testGrantID,
			AuthnMethods: []iamv1.AuthnMethod{iamv1.AuthnMethod_AUTHN_METHOD_PASSWORD},
		},
	}}, nil
}

func TestGRPCAdapterChecksPermissionOnceWithBoundedDeadline(t *testing.T) {
	server := &deadlineAuthorizationServer{}
	client, closeClient := newBufconnTargetIAM(t, server)
	defer closeClient()

	decision, err := client.CheckPermission(context.Background(), ports.TargetIAMCheckPermissionRequest{
		Credential:     "target-access",
		OperationID:    "listInstances",
		PolicyRevision: "2026-09-04.dp2-02.v1",
		TenantID:       "22222222-2222-2222-2222-222222222222",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || decision.PolicyRevision != "2026-09-04.dp2-02.v1" {
		t.Fatalf("CheckPermission() = %#v", decision)
	}
	if server.calls.Load() != 1 {
		t.Fatalf("CheckPermission calls = %d, want 1", server.calls.Load())
	}
	if server.deadline <= 0 || server.deadline > 500*time.Millisecond {
		t.Fatalf("remaining deadline = %s, want (0, 500ms]", server.deadline)
	}
}

func TestGRPCAdapterTranslatesAuthorizedPrincipalAtThePort(t *testing.T) {
	server := &deadlineAuthorizationServer{}
	client, closeClient := newBufconnTargetIAM(t, server)
	defer closeClient()

	decision, err := client.CheckPermission(context.Background(), ports.TargetIAMCheckPermissionRequest{
		Credential: "target-access", OperationID: "listInstances",
		PolicyRevision: "2026-09-04.dp2-02.v1", TenantID: testTenantID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Principal.ID != testPrincipalID ||
		decision.Principal.Type != ports.TargetIAMPrincipalHuman ||
		decision.Principal.Status != ports.TargetIAMPrincipalActive ||
		decision.Principal.Boundary.Type != ports.TargetIAMBoundaryTenant ||
		decision.Principal.Boundary.TenantID != testTenantID ||
		decision.Principal.SessionID != testSessionID || decision.Principal.GrantID != testGrantID ||
		len(decision.Principal.AuthnMethods) != 1 || decision.Principal.AuthnMethods[0] != ports.TargetIAMAuthnPassword {
		t.Fatalf("translated principal = %#v", decision.Principal)
	}
}

func TestGRPCAdapterTranslatesPasswordLoginAtThePort(t *testing.T) {
	server := &passwordLoginServer{}
	client, closeClient := newBufconnTargetIAM(t, server, nil)
	defer closeClient()

	result, err := client.PasswordLogin(context.Background(), ports.TargetIAMPasswordLoginRequest{
		Account: "user@example.com", Password: "correct horse",
		Audience:   ports.TargetIAMAudienceConsole,
		Boundary:   ports.TargetIAMBoundary{Type: ports.TargetIAMBoundaryTenant, TenantID: testTenantID},
		DeviceName: "browser-a", IdempotencyKey: "login-001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.request.GetAudience() != iamv1.Audience_AUDIENCE_CONSOLE ||
		server.request.GetBoundary().GetTenant().GetTenantId() != testTenantID ||
		server.request.GetIdempotencyKey() != "login-001" {
		t.Fatalf("translated request = %#v", server.request)
	}
	if result.AccessToken != "target-access" || result.RefreshToken != "target-refresh" ||
		result.Principal.ID != testPrincipalID || result.Session.ID != testSessionID ||
		result.Grant.ID != testGrantID || result.Grant.Version != 7 {
		t.Fatalf("translated result = %#v", result)
	}
}

func TestGRPCAdapterMapsStableIAMFailureAtThePort(t *testing.T) {
	client, closeClient := newBufconnTargetIAM(t, failingPasswordLoginServer{})
	defer closeClient()

	_, err := client.PasswordLogin(context.Background(), ports.TargetIAMPasswordLoginRequest{})
	var failure *ports.TargetIAMError
	if !errors.As(err, &failure) {
		t.Fatalf("PasswordLogin() error = %T %v, want *ports.TargetIAMError", err, err)
	}
	if failure.Kind != ports.TargetIAMFailureResourceExhausted ||
		failure.Reason != "AUTH_RATE_LIMITED" ||
		failure.Metadata["retry_after_seconds"] != "900" ||
		failure.Error() == "internal detail must not leak" {
		t.Fatalf("mapped failure = %#v (%v)", failure, failure)
	}
}

func TestGRPCAdapterTimesOutOnceWithoutRetry(t *testing.T) {
	server := &deadlineAuthorizationServer{block: true}
	client, closeClient := newBufconnTargetIAM(t, server)
	defer closeClient()

	started := time.Now()
	_, err := client.CheckPermission(context.Background(), ports.TargetIAMCheckPermissionRequest{})
	var failure *ports.TargetIAMError
	if !errors.As(err, &failure) || failure.Kind != ports.TargetIAMFailureDeadlineExceeded {
		t.Fatalf("CheckPermission() error = %T %v, want deadline TargetIAMError", err, err)
	}
	if server.calls.Load() != 1 {
		t.Fatalf("CheckPermission calls = %d, want 1", server.calls.Load())
	}
	if elapsed := time.Since(started); elapsed < 400*time.Millisecond || elapsed > time.Second {
		t.Fatalf("timeout elapsed = %s, want around 500ms", elapsed)
	}
}

func TestDialRejectsBlankTargetAddress(t *testing.T) {
	client, closeClient, err := Dial("   ", MutualTLSConfig{})
	if err == nil || client != nil || closeClient != nil {
		t.Fatalf(
			"Dial(blank): clientNil=%v closeNil=%v error=%v, want true/true/non-nil",
			client == nil, closeClient == nil, err,
		)
	}
}

func TestDialRejectsIncompleteMutualTLSConfiguration(t *testing.T) {
	config, _ := newMutualTLSFixture(t)
	cases := []struct {
		name   string
		mutate func(*MutualTLSConfig)
	}{
		{name: "server name", mutate: func(c *MutualTLSConfig) { c.ServerName = "" }},
		{name: "CA file", mutate: func(c *MutualTLSConfig) { c.CAFile = "" }},
		{name: "certificate file", mutate: func(c *MutualTLSConfig) { c.CertificateFile = "" }},
		{name: "private key file", mutate: func(c *MutualTLSConfig) { c.PrivateKeyFile = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := config
			tc.mutate(&candidate)
			client, closeClient, err := Dial("127.0.0.1:1", candidate)
			if err == nil || client != nil || closeClient != nil {
				t.Fatalf("Dial(incomplete config): clientNil=%v closeNil=%v error=%v", client == nil, closeClient == nil, err)
			}
		})
	}
}

func TestDialUsesVerifiedMutualTLS13(t *testing.T) {
	config, serverTLS := newMutualTLSFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	iamv1.RegisterAuthenticationServiceServer(server, &passwordLoginServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, closeClient, err := Dial(listener.Addr().String(), config)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = closeClient() })
	response, err := client.PasswordLogin(context.Background(), ports.TargetIAMPasswordLoginRequest{})
	if err != nil || response.AccessToken != "target-access" {
		t.Fatalf("PasswordLogin() = %#v, %v", response, err)
	}
}

func newBufconnTargetIAM(
	t *testing.T,
	servers ...any,
) (ports.TargetIAM, func()) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	for _, implementation := range servers {
		switch value := implementation.(type) {
		case iamv1.AuthenticationServiceServer:
			iamv1.RegisterAuthenticationServiceServer(server, value)
		case iamv1.AuthorizationServiceServer:
			iamv1.RegisterAuthorizationServiceServer(server, value)
		case nil:
		default:
			t.Fatalf("unsupported IAM test server %T", value)
		}
	}
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	return NewGRPCClient(connection), func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
	}
}

func remainingDeadline(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return time.Until(deadline)
}

func newMutualTLSFixture(t *testing.T) (MutualTLSConfig, *tls.Config) {
	t.Helper()
	directory := t.TempDir()
	caCertificate, caKey := newTargetTestCertificateAuthority(t)
	serverCertificateFile, serverKeyFile := writeTargetTestLeafCertificate(t, directory, "server", "iam.dp2.test", x509.ExtKeyUsageServerAuth, caCertificate, caKey)
	clientCertificateFile, clientKeyFile := writeTargetTestLeafCertificate(t, directory, "client", "ani-gateway", x509.ExtKeyUsageClientAuth, caCertificate, caKey)
	caFile := filepath.Join(directory, "ca.pem")
	writeTargetTestPEM(t, caFile, "CERTIFICATE", caCertificate.Raw)
	serverCertificate, err := tls.LoadX509KeyPair(serverCertificateFile, serverKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(caCertificate)
	return MutualTLSConfig{
			ServerName:      "iam.dp2.test",
			CAFile:          caFile,
			CertificateFile: clientCertificateFile,
			PrivateKeyFile:  clientKeyFile,
		}, &tls.Config{
			MinVersion:   tls.VersionTLS13,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    clientCAs,
			Certificates: []tls.Certificate{serverCertificate},
		}
}

func newTargetTestCertificateAuthority(t *testing.T) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "DP2-05 Gateway test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, privateKey
}

func writeTargetTestLeafCertificate(
	t *testing.T,
	directory, prefix, commonName string,
	usage x509.ExtKeyUsage,
	caCertificate *x509.Certificate,
	caKey ed25519.PrivateKey,
) (string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     []string{commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCertificate, publicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificateFile := filepath.Join(directory, prefix+".pem")
	privateKeyFile := filepath.Join(directory, prefix+"-key.pem")
	writeTargetTestPEM(t, certificateFile, "CERTIFICATE", der)
	writeTargetTestPEM(t, privateKeyFile, "PRIVATE KEY", privateKeyDER)
	return certificateFile, privateKeyFile
}

func writeTargetTestPEM(t *testing.T, path, blockType string, contents []byte) {
	t.Helper()
	encoded := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: contents})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}
