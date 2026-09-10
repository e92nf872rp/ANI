package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TestRunExtractsImportedArchiveIntoVersionDirectory(t *testing.T) {
	archiveData := func() []byte {
		var buffer bytes.Buffer
		gzipWriter := gzip.NewWriter(&buffer)
		tarWriter := tar.NewWriter(gzipWriter)
		if err := tarWriter.WriteHeader(&tar.Header{Name: "config.json", Mode: 0600, Size: 2, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte("{}")); err != nil {
			t.Fatal(err)
		}
		if err := tarWriter.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gzipWriter.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}()
	sum := sha256.Sum256(archiveData)
	objectRef := "object://models/tenant/model/import-id/archive/model.tar.gz"
	fake := &fakeModelServiceClient{url: "https://object.invalid/model.tar.gz", storagePath: objectRef}
	root := t.TempDir()
	versionDir := filepath.Join(root, "version-id")
	cfg := FetcherConfig{TenantID: "tenant-a", ModelVersionID: "version-id", ModelServiceGRPCAddr: "model-service:9103", ObjectRef: objectRef, ExpectedSize: int64(len(archiveData)), ExpectedSHA256: fmt.Sprintf("%x", sum), TargetPath: versionDir, AllowInsecureHTTP: true}
	dialer := func(context.Context, string) (modelDownloadURLClient, func() error, error) {
		return fake, func() error { return nil }, nil
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archiveData)), ContentLength: int64(len(archiveData))}, nil
	})}
	if err := runWithDependencies(context.Background(), cfg, dialer, httpClient); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(versionDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "{}" {
		t.Fatalf("extracted config = %q", content)
	}
	if _, err := os.Stat(filepath.Join(root, ".model-fetch-archive.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("temporary archive still exists: %v", err)
	}
}

type fakeModelServiceClient struct {
	request     *modelv1.GetModelDownloadURLRequest
	url         string
	storagePath string
}

type fakeModelServiceServer struct {
	modelv1.UnimplementedModelServiceServer
	request     *modelv1.GetModelDownloadURLRequest
	url         string
	storagePath string
}

func (f *fakeModelServiceServer) GetModelDownloadURL(_ context.Context, req *modelv1.GetModelDownloadURLRequest) (*modelv1.GetModelDownloadURLResponse, error) {
	f.request = req
	return &modelv1.GetModelDownloadURLResponse{DownloadUrl: f.url, StoragePath: f.storagePath}, nil
}

func (f *fakeModelServiceClient) GetModelDownloadURL(_ context.Context, req *modelv1.GetModelDownloadURLRequest, _ ...grpc.CallOption) (*modelv1.GetModelDownloadURLResponse, error) {
	f.request = req
	return &modelv1.GetModelDownloadURLResponse{DownloadUrl: f.url, StoragePath: f.storagePath}, nil
}

func TestParseConfigReadsFlagsAndRejectsUnsafeTarget(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--tenant-id", "tenant-a", "--model-version-id", "version-a",
		"--model-service-grpc-addr", "model-service:9103", "--object-ref", "object://models/a",
		"--size-bytes", "4", "--sha256", "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7",
		"--target-path", "/models/model.bin", "--allow-insecure-http",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TenantID != "tenant-a" || cfg.ModelVersionID != "version-a" || cfg.TargetPath != "/models/model.bin" || !cfg.AllowInsecureHTTP {
		t.Fatalf("config = %+v", cfg)
	}
	if _, err := parseConfig([]string{"--tenant-id", "tenant-a", "--model-version-id", "version-a", "--model-service-grpc-addr", "model-service:9103", "--size-bytes", "1", "--sha256", "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7", "--target-path", "/models/../secret"}); err == nil {
		t.Fatal("unsafe target path accepted")
	}
	if _, err := parseConfig([]string{"--tenant-id", "tenant-a", "--model-version-id", "version-a", "--model-service-grpc-addr", "model-service:9103", "--size-bytes", "1", "--sha256", "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7", "--target-path", "/models/model.bin"}); err == nil {
		t.Fatal("missing object reference accepted")
	}
}

func TestRunRejectsMissingObjectReferenceBeforeModelServiceLookup(t *testing.T) {
	called := false
	dialer := func(context.Context, string) (modelDownloadURLClient, func() error, error) {
		called = true
		return nil, nil, nil
	}
	cfg := FetcherConfig{
		TenantID: "tenant-a", ModelVersionID: "version-a", ModelServiceGRPCAddr: "model-service:9103",
		ExpectedSize: 4, ExpectedSHA256: "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7",
		TargetPath: filepath.Join(t.TempDir(), "model.bin"),
	}
	if err := runWithDependencies(context.Background(), cfg, dialer, nil); err == nil {
		t.Fatal("runWithDependencies() accepted missing object reference")
	}
	if called {
		t.Fatal("runWithDependencies() contacted model-service before object reference validation")
	}
}

func TestRunFetchesURLWithInitContainerIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data"))
	}))
	defer server.Close()
	fake := &fakeModelServiceClient{url: server.URL, storagePath: "object://models/a"}
	cfg := FetcherConfig{TenantID: "tenant-a", ModelVersionID: "version-a", ModelServiceGRPCAddr: "model-service:9103", ObjectRef: "object://models/a", ExpectedSize: 4, ExpectedSHA256: "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7", TargetPath: filepath.Join(t.TempDir(), "model.bin")}
	cfg.AllowInsecureHTTP = true
	dialer := func(context.Context, string) (modelDownloadURLClient, func() error, error) {
		return fake, func() error { return nil }, nil
	}
	if err := runWithDependencies(context.Background(), cfg, dialer, server.Client()); err != nil {
		t.Fatal(err)
	}
	if fake.request == nil || fake.request.GetTenantId() != "tenant-a" || fake.request.GetModelVersionId() != "version-a" || fake.request.GetRequester() != "init-container" {
		t.Fatalf("request = %+v", fake.request)
	}
	got, err := os.ReadFile(cfg.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "data" {
		t.Fatalf("file = %q", got)
	}
}

func TestRunRejectsModelServiceObjectBindingMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("data")) }))
	defer server.Close()
	fake := &fakeModelServiceClient{url: server.URL, storagePath: "object://models/other"}
	cfg := FetcherConfig{TenantID: "tenant-a", ModelVersionID: "version-a", ModelServiceGRPCAddr: "model-service:9103", ObjectRef: "object://models/a", ExpectedSize: 4, ExpectedSHA256: "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7", TargetPath: filepath.Join(t.TempDir(), "model.bin")}
	cfg.AllowInsecureHTTP = true
	dialer := func(context.Context, string) (modelDownloadURLClient, func() error, error) {
		return fake, func() error { return nil }, nil
	}
	if err := runWithDependencies(context.Background(), cfg, dialer, server.Client()); err == nil {
		t.Fatal("unexpected model object response accepted")
	}
}

func TestRunRejectsInsecureDownloadURLWithoutExplicitOptIn(t *testing.T) {
	fake := &fakeModelServiceClient{url: "http://minio.example/model.bin", storagePath: "object://models/a"}
	cfg := FetcherConfig{TenantID: "tenant-a", ModelVersionID: "version-a", ModelServiceGRPCAddr: "model-service:9103", ObjectRef: "object://models/a", ExpectedSize: 4, ExpectedSHA256: "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7", TargetPath: filepath.Join(t.TempDir(), "model.bin")}
	dialer := func(context.Context, string) (modelDownloadURLClient, func() error, error) {
		return fake, func() error { return nil }, nil
	}
	if err := runWithDependencies(context.Background(), cfg, dialer, nil); err == nil || !strings.Contains(err.Error(), "insecure") {
		t.Fatalf("err=%v, want insecure URL rejection", err)
	}
}

func TestRunUsesGeneratedModelServiceGRPCClient(t *testing.T) {
	grpcListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local socket unavailable in this test environment: %v", err)
	}
	serverCert, caPEM, clientCertPEM, clientKeyPEM := writeModelServiceTLSFixture(t)
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCert},
	})))
	modelServer := &fakeModelServiceServer{
		url:         "https://object.invalid/model.bin",
		storagePath: "object://models/tenant-a/model-a",
	}
	modelv1.RegisterModelServiceServer(grpcServer, modelServer)
	go func() { _ = grpcServer.Serve(grpcListener) }()
	defer grpcServer.Stop()
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	certFile := filepath.Join(t.TempDir(), "client.crt")
	keyFile := filepath.Join(t.TempDir(), "client.key")
	if err := os.WriteFile(caFile, caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, clientCertPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, clientKeyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MODEL_SERVICE_TLS_CA_FILE", caFile)
	t.Setenv("MODEL_SERVICE_TLS_CERT_FILE", certFile)
	t.Setenv("MODEL_SERVICE_TLS_KEY_FILE", keyFile)

	dir := t.TempDir()
	cfg := FetcherConfig{
		TenantID: "tenant-a", ModelVersionID: "version-a", ModelServiceGRPCAddr: grpcListener.Addr().String(),
		ObjectRef:    "object://models/tenant-a/model-a",
		ExpectedSize: 4, ExpectedSHA256: "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7",
		TargetPath:        filepath.Join(dir, "model.bin"),
		AllowInsecureHTTP: true,
	}
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("data")), ContentLength: 4}, nil
	})}
	if err := runWithDependencies(context.Background(), cfg, newModelServiceClient, httpClient); err != nil {
		t.Fatal(err)
	}
	if modelServer.request == nil || modelServer.request.GetTenantId() != cfg.TenantID || modelServer.request.GetModelVersionId() != cfg.ModelVersionID || modelServer.request.GetRequester() != "init-container" {
		t.Fatalf("request = %+v", modelServer.request)
	}
}

func writeModelServiceTLSFixture(t *testing.T) (tls.Certificate, []byte, []byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "model-service"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:         true,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return cert, certPEM, certPEM, keyPEM
}

func TestNewModelServiceClientRequiresTLSConfiguration(t *testing.T) {
	t.Setenv("MODEL_SERVICE_TLS_CA_FILE", "")
	t.Setenv("MODEL_SERVICE_TLS_CERT_FILE", "")
	t.Setenv("MODEL_SERVICE_TLS_KEY_FILE", "")
	if _, _, err := newModelServiceClient(context.Background(), "127.0.0.1:1"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "tls") {
		t.Fatalf("newModelServiceClient() error = %v, want TLS configuration error", err)
	}
}
