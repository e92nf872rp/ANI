package targetiam

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	iamv1 "github.com/kubercloud/ani/services/ani-gateway/internal/targetiam/gen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type deadlineAuthenticationServer struct {
	iamv1.UnimplementedAuthenticationServiceServer
	deadline time.Duration
}

func (s *deadlineAuthenticationServer) PasswordLogin(ctx context.Context, _ *iamv1.PasswordLoginRequest) (*iamv1.PasswordLoginResponse, error) {
	s.deadline = remainingDeadline(ctx)
	return &iamv1.PasswordLoginResponse{AccessToken: "target-access"}, nil
}

type deadlineAuthorizationServer struct {
	iamv1.UnimplementedAuthorizationServiceServer
	deadline time.Duration
	calls    atomic.Int32
	block    bool
}

func (s *deadlineAuthorizationServer) CheckPermission(ctx context.Context, _ *iamv1.CheckPermissionRequest) (*iamv1.CheckPermissionResponse, error) {
	s.calls.Add(1)
	s.deadline = remainingDeadline(ctx)
	if s.block {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "deadline")
	}
	return &iamv1.CheckPermissionResponse{Decision: &iamv1.AuthorizationDecision{Allowed: true}}, nil
}

func TestGRPCClientUsesFrozenServicesAndFiveHundredMillisecondDeadline(t *testing.T) {
	authentication := &deadlineAuthenticationServer{}
	authorization := &deadlineAuthorizationServer{}
	client, closeClient := newBufconnTargetClient(t, authentication, authorization)
	defer closeClient()

	login, err := client.PasswordLogin(context.Background(), &iamv1.PasswordLoginRequest{})
	if err != nil || login.GetAccessToken() != "target-access" {
		t.Fatalf("PasswordLogin() = %#v, %v", login, err)
	}
	decision, err := client.CheckPermission(context.Background(), &iamv1.CheckPermissionRequest{})
	if err != nil || !decision.GetDecision().GetAllowed() {
		t.Fatalf("CheckPermission() = %#v, %v", decision, err)
	}
	for name, deadline := range map[string]time.Duration{
		"PasswordLogin":   authentication.deadline,
		"CheckPermission": authorization.deadline,
	} {
		if deadline <= 0 || deadline > 500*time.Millisecond {
			t.Fatalf("%s remaining deadline = %s, want (0, 500ms]", name, deadline)
		}
	}
}

func TestGRPCClientTimesOutOnceWithoutRetry(t *testing.T) {
	authentication := &deadlineAuthenticationServer{}
	authorization := &deadlineAuthorizationServer{block: true}
	client, closeClient := newBufconnTargetClient(t, authentication, authorization)
	defer closeClient()

	started := time.Now()
	_, err := client.CheckPermission(context.Background(), &iamv1.CheckPermissionRequest{})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("CheckPermission() error = %v, want DeadlineExceeded", err)
	}
	if authorization.calls.Load() != 1 {
		t.Fatalf("CheckPermission calls = %d, want 1", authorization.calls.Load())
	}
	if elapsed := time.Since(started); elapsed < 400*time.Millisecond || elapsed > time.Second {
		t.Fatalf("timeout elapsed = %s, want around 500ms", elapsed)
	}
}

func TestDialRejectsBlankTargetAddress(t *testing.T) {
	client, closeClient, err := Dial("   ", MutualTLSConfig{})
	if err == nil || client != nil || closeClient != nil {
		t.Fatalf("Dial(blank): clientNil=%v closeNil=%v error=%v, want true/true/non-nil", client == nil, closeClient == nil, err)
	}
}

func TestDialUsesVerifiedMutualTLS13(t *testing.T) {
	config, serverTLS := newMutualTLSFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	iamv1.RegisterAuthenticationServiceServer(server, &deadlineAuthenticationServer{})
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
	response, err := client.PasswordLogin(context.Background(), &iamv1.PasswordLoginRequest{})
	if err != nil || response.GetAccessToken() != "target-access" {
		t.Fatalf("PasswordLogin() = %#v, %v", response, err)
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

func newBufconnTargetClient(
	t *testing.T,
	authentication iamv1.AuthenticationServiceServer,
	authorization iamv1.AuthorizationServiceServer,
) (Client, func()) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	iamv1.RegisterAuthenticationServiceServer(server, authentication)
	iamv1.RegisterAuthorizationServiceServer(server, authorization)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	return NewGRPCClient(conn), func() {
		_ = conn.Close()
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

func writeTargetTestLeafCertificate(t *testing.T, directory, prefix, commonName string, usage x509.ExtKeyUsage, caCertificate *x509.Certificate, caKey ed25519.PrivateKey) (string, string) {
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
