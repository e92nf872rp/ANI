package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// FetcherConfig is the non-secret init-container configuration. The signed
// object URL is obtained at runtime from model-service and is never a config
// value or log field.
type FetcherConfig struct {
	TenantID             string
	ModelVersionID       string
	ModelServiceGRPCAddr string
	ObjectRef            string
	OutputDirectory      string
	Filename             string
	TargetPath           string
	ExpectedSize         int64
	ExpectedSHA256       string
	// AllowInsecureHTTP is an explicit opt-in for a controlled, in-cluster
	// MinIO endpoint. It is false by default; public presigned URLs must use
	// HTTPS.
	AllowInsecureHTTP bool
}

// Configuration is retained as an alias for callers that used the original
// process-boundary name.
type Configuration = FetcherConfig

type modelDownloadURLClient interface {
	GetModelDownloadURL(context.Context, *modelv1.GetModelDownloadURLRequest, ...grpc.CallOption) (*modelv1.GetModelDownloadURLResponse, error)
}

type modelServiceDialer func(context.Context, string) (modelDownloadURLClient, func() error, error)

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		// Do not print flag errors: values can contain credentials or signed URLs.
		_, _ = fmt.Fprintln(os.Stderr, "model-fetcher configuration error")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		// Keep the process error stable and free of service addresses and URLs.
		_, _ = fmt.Fprintln(os.Stderr, "model-fetcher failed")
		os.Exit(1)
	}
}

func parseConfig(args []string) (FetcherConfig, error) {
	flags := flag.NewFlagSet("model-fetcher", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cfg := FetcherConfig{}
	flags.StringVar(&cfg.TenantID, "tenant-id", envValue("MODEL_TENANT_ID"), "")
	flags.StringVar(&cfg.ModelVersionID, "model-version-id", envValue("MODEL_VERSION_ID"), "")
	flags.StringVar(&cfg.ModelServiceGRPCAddr, "model-service-grpc-addr", envValue("MODEL_SERVICE_GRPC_ADDR"), "")
	flags.StringVar(&cfg.ObjectRef, "object-ref", envValue("MODEL_OBJECT_REF"), "")
	flags.StringVar(&cfg.OutputDirectory, "output-dir", envValue("MODEL_OUTPUT_DIRECTORY"), "")
	flags.StringVar(&cfg.Filename, "filename", envValue("MODEL_FILENAME"), "")
	flags.StringVar(&cfg.TargetPath, "target-path", envValue("MODEL_TARGET_PATH"), "")
	allowInsecureHTTP, err := envBool("MODEL_FETCHER_ALLOW_INSECURE_HTTP")
	if err != nil {
		return FetcherConfig{}, errors.New("invalid insecure HTTP setting")
	}
	flags.BoolVar(&cfg.AllowInsecureHTTP, "allow-insecure-http", allowInsecureHTTP, "")
	var size string
	flags.StringVar(&size, "size-bytes", envValue("MODEL_EXPECTED_SIZE_BYTES"), "")
	flags.StringVar(&cfg.ExpectedSHA256, "sha256", envValue("MODEL_CHECKSUM_SHA256"), "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return FetcherConfig{}, errors.New("invalid arguments")
	}
	cfg.ExpectedSize, err = strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	if err != nil {
		return FetcherConfig{}, errors.New("invalid expected size")
	}
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	cfg.ModelVersionID = strings.TrimSpace(cfg.ModelVersionID)
	cfg.ModelServiceGRPCAddr = strings.TrimSpace(cfg.ModelServiceGRPCAddr)
	cfg.ObjectRef = strings.TrimSpace(cfg.ObjectRef)
	cfg.ExpectedSHA256 = normalizeDigest(cfg.ExpectedSHA256)
	if cfg.TargetPath == "" {
		if cfg.OutputDirectory == "" || cfg.Filename == "" {
			return FetcherConfig{}, errors.New("target path required")
		}
		cfg.TargetPath = filepath.Join(cfg.OutputDirectory, cfg.Filename)
	}
	outputDir, filename, err := targetParts(cfg.TargetPath)
	if err != nil {
		return FetcherConfig{}, err
	}
	cfg.TargetPath = filepath.Join(outputDir, filename)
	cfg.OutputDirectory, cfg.Filename = outputDir, filename
	if cfg.TenantID == "" || cfg.ModelVersionID == "" || cfg.ModelServiceGRPCAddr == "" || cfg.ObjectRef == "" || cfg.ExpectedSize < 0 || cfg.ExpectedSize > maxDownloadSize || !validDigest(cfg.ExpectedSHA256) {
		return FetcherConfig{}, errors.New("incomplete configuration")
	}
	return cfg, nil
}

func run(ctx context.Context, cfg FetcherConfig) error {
	return runWithDependencies(ctx, cfg, newModelServiceClient, http.DefaultClient)
}

func runWithDependencies(ctx context.Context, cfg FetcherConfig, dialer modelServiceDialer, httpClient *http.Client) error {
	if dialer == nil {
		return errors.New("model-service client unavailable")
	}
	outputDir, filename, err := targetParts(cfg.TargetPath)
	if err != nil || cfg.TenantID == "" || cfg.ModelVersionID == "" || cfg.ModelServiceGRPCAddr == "" || cfg.ObjectRef == "" || cfg.ExpectedSize < 0 || !validDigest(cfg.ExpectedSHA256) {
		return errors.New("invalid fetcher configuration")
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, closeClient, err := dialer(lookupCtx, cfg.ModelServiceGRPCAddr)
	if err != nil || client == nil {
		return errors.New("model-service client unavailable")
	}
	if closeClient != nil {
		defer closeClient()
	}
	response, err := client.GetModelDownloadURL(lookupCtx, &modelv1.GetModelDownloadURLRequest{
		TenantId: cfg.TenantID, ModelVersionId: cfg.ModelVersionID, Requester: "init-container",
	})
	if err != nil || response == nil || strings.TrimSpace(response.GetDownloadUrl()) == "" {
		return errors.New("model-service download lookup failed")
	}
	if err := validateDownloadURL(response.GetDownloadUrl(), cfg.AllowInsecureHTTP); err != nil {
		return err
	}
	// The model-service response is an additional object binding fence. Core
	// passes the canonical object reference; never download a URL for another
	// object even if a buggy or compromised service returns one with matching
	// size/hash metadata.
	if cfg.ObjectRef != "" && strings.TrimSpace(response.GetStoragePath()) != cfg.ObjectRef {
		return errors.New("model-service returned an unexpected object")
	}
	if isModelArchiveObject(response.GetStoragePath()) || isModelArchiveObject(cfg.ObjectRef) {
		// Archives are downloaded beside (not inside) the final version
		// directory, then atomically extracted into TargetPath. TargetPath is
		// supplied by the renderer as /models/<model-version-id> for this case.
		archiveDir := filepath.Dir(cfg.TargetPath)
		archiveFilename := ".model-fetch-archive.tar.gz"
		archivePath := filepath.Join(archiveDir, archiveFilename)
		if err := Download(ctx, Descriptor{
			URL: response.GetDownloadUrl(), Filename: archiveFilename,
			ExpectedSize: cfg.ExpectedSize, ExpectedSHA256: cfg.ExpectedSHA256,
			AllowInsecureHTTP: cfg.AllowInsecureHTTP,
		}, httpClient, archiveDir); err != nil {
			return err
		}
		defer os.Remove(archivePath)
		return ExtractArchive(ctx, archivePath, cfg.TargetPath)
	}
	return Download(ctx, Descriptor{
		URL: response.GetDownloadUrl(), Filename: filename,
		ExpectedSize: cfg.ExpectedSize, ExpectedSHA256: cfg.ExpectedSHA256,
		AllowInsecureHTTP: cfg.AllowInsecureHTTP,
	}, httpClient, outputDir)
}

func newModelServiceClient(_ context.Context, addr string) (modelDownloadURLClient, func() error, error) {
	// The model-fetcher is an identity-bearing init container.  There is no
	// plaintext fallback: production calls to the restricted 9105 listener
	// must prove the tenant-scoped workload identity with mTLS.
	transport, err := modelServiceClientCredentials(addr)
	if err != nil {
		return nil, nil, errors.New("model-service TLS client unavailable")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(transport))
	if err != nil {
		return nil, nil, errors.New("model-service TLS client unavailable")
	}
	return modelv1.NewModelServiceClient(conn), conn.Close, nil
}

func modelServiceClientCredentials(addr string) (credentials.TransportCredentials, error) {
	caPEM, err := os.ReadFile(envValue("MODEL_SERVICE_TLS_CA_FILE"))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("invalid model-service CA")
	}
	cert, err := tls.LoadX509KeyPair(envValue("MODEL_SERVICE_TLS_CERT_FILE"), envValue("MODEL_SERVICE_TLS_KEY_FILE"))
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return nil, errors.New("invalid model-service address")
	}
	return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, Certificates: []tls.Certificate{cert}, ServerName: host}), nil
}

func targetParts(path string) (string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", "", errors.New("target path must be absolute")
	}
	if strings.HasSuffix(path, string(filepath.Separator)) {
		return "", "", errors.New("target path must name a file")
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if part == ".." {
			return "", "", errors.New("target path escapes output directory")
		}
	}
	filename := filepath.Base(path)
	if filename == "." || filename == ".." || filename == string(filepath.Separator) || filepath.Base(filename) != filename {
		return "", "", errors.New("invalid target filename")
	}
	return filepath.Dir(path), filename, nil
}

func envValue(key string) string { return strings.TrimSpace(os.Getenv(key)) }

func envBool(key string) (bool, error) {
	raw := envValue(key)
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, err
	}
	return value, nil
}
