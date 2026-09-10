package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/bootstrap"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/services/model-service/internal/config"
	"github.com/kubercloud/ani/services/model-service/internal/repo"
	"github.com/kubercloud/ani/services/model-service/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func main() {
	cfg := config.Load()
	deps := bootstrap.MustConnect(cfg)
	defer deps.Close()

	modelRepo := repo.NewPostgresModelRepo()
	svc := service.NewModelServiceWithObjectStore(deps.DB, modelRepo, bootstrap.NewModelObjectStore(deps.Ports.ObjectStore))
	if port, err := fetcherMTLSPort(); err == nil && port > 0 {
		if err := serveFetcherMTLS(port, svc, deps); err != nil {
			deps.Logger.Error("model-fetcher mTLS listener failed", "err", err)
			os.Exit(1)
		}
	} else if err != nil {
		deps.Logger.Error("model-fetcher mTLS configuration invalid", "err", err)
		os.Exit(1)
	}
	bootstrap.RunGRPC(cfg.GRPCPort, func(server *grpc.Server) {
		modelv1.RegisterModelServiceServer(server, publicModelServiceServer{ModelServiceServer: svc})
	}, deps)
}

// publicModelServiceServer is the ordinary 9103 control-plane surface. The
// download URL method is intentionally omitted from that surface: signed
// object URLs are only issued through the tenant-bound mTLS listener below.
// Embedding the generated server interface delegates every other method to
// the regular model service while keeping the public registration explicit.
type publicModelServiceServer struct {
	modelv1.ModelServiceServer
}

func (s publicModelServiceServer) GetModelDownloadURL(context.Context, *modelv1.GetModelDownloadURLRequest) (*modelv1.GetModelDownloadURLResponse, error) {
	return nil, status.Error(codes.PermissionDenied, "download URL requires fetcher identity")
}

type fetcherOnlyServer struct {
	modelv1.UnimplementedModelServiceServer
	delegate interface {
		GetModelDownloadURL(context.Context, *modelv1.GetModelDownloadURLRequest) (*modelv1.GetModelDownloadURLResponse, error)
	}
}

func (s fetcherOnlyServer) GetModelDownloadURL(ctx context.Context, req *modelv1.GetModelDownloadURLRequest) (*modelv1.GetModelDownloadURLResponse, error) {
	if err := verifyFetcherTenant(ctx, req.GetTenantId()); err != nil {
		return nil, err
	}
	return s.delegate.GetModelDownloadURL(ctx, req)
}

func verifyFetcherTenant(ctx context.Context, tenantID string) error {
	peerInfo, ok := peer.FromContext(ctx)
	if !ok || peerInfo.AuthInfo == nil {
		return status.Error(codes.Unauthenticated, "fetcher identity required")
	}
	tlsInfo, ok := peerInfo.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
		return status.Error(codes.Unauthenticated, "fetcher certificate required")
	}
	certificate := tlsInfo.State.PeerCertificates[0]
	wantNamespace := "ani-tenant-" + strings.TrimSpace(tenantID)
	for _, uri := range certificate.URIs {
		if uri.Scheme != "spiffe" || uri.Host != "ani.dev" {
			continue
		}
		parts := strings.Split(strings.Trim(uri.Path, "/"), "/")
		if len(parts) == 4 && parts[0] == "ns" && parts[1] == wantNamespace && parts[2] == "sa" && parts[3] == "ani-inference-fetcher" {
			return nil
		}
	}
	return status.Error(codes.PermissionDenied, "fetcher tenant identity mismatch")
}

func fetcherMTLSPort() (int, error) {
	raw := strings.TrimSpace(os.Getenv("MODEL_FETCHER_GRPC_PORT"))
	if raw == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid MODEL_FETCHER_GRPC_PORT")
	}
	return port, nil
}

func serveFetcherMTLS(port int, svc *service.ModelService, deps *bootstrap.Deps) error {
	transport, err := modelServiceServerCredentials()
	if err != nil {
		return err
	}
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	srv := grpc.NewServer(grpc.Creds(transport))
	modelv1.RegisterModelServiceServer(srv, fetcherOnlyServer{delegate: svc})
	go func() {
		if err := srv.Serve(lis); err != nil {
			deps.Logger.Error("model-fetcher mTLS serve error", "err", err)
		}
	}()
	return nil
}

func modelServiceServerCredentials() (credentials.TransportCredentials, error) {
	certFile := strings.TrimSpace(os.Getenv("MODEL_SERVICE_TLS_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("MODEL_SERVICE_TLS_KEY_FILE"))
	clientCAFile := strings.TrimSpace(os.Getenv("MODEL_SERVICE_TLS_CLIENT_CA_FILE"))
	if certFile == "" || keyFile == "" || clientCAFile == "" {
		return nil, fmt.Errorf("model-service mTLS files are required")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(clientCAFile)
	if err != nil {
		return nil, err
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("invalid model-service client CA")
	}
	return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert}), nil
}
