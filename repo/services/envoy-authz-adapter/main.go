package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"github.com/kubercloud/ani/services/envoy-authz-adapter/internal/authclient"
	"github.com/kubercloud/ani/services/envoy-authz-adapter/internal/config"
	"github.com/kubercloud/ani/services/envoy-authz-adapter/internal/extauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

const gracefulStopTimeout = 5 * time.Second

func main() {
	if err := run(); err != nil {
		log.Printf("envoy authorization adapter stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	authConnection, err := grpc.NewClient(
		cfg.AuthServiceGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("set up auth service connection: %w", err)
	}
	defer func() {
		if err := authConnection.Close(); err != nil {
			log.Printf("close auth service connection: %v", err)
		}
	}()

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			log.Printf("close gRPC listener: %v", err)
		}
	}()

	server := newGRPCServer(authclient.New(authv1.NewAuthServiceClient(authConnection), cfg.AuthTimeout))
	shutdownContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-shutdownContext.Done()
		gracefulStop(server)
	}()

	log.Printf("envoy authorization adapter serving on gRPC port %d", cfg.GRPCPort)
	if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve gRPC: %w", err)
	}
	return nil
}

func newGRPCServer(validator extauth.TokenValidator) *grpc.Server {
	server := grpc.NewServer()
	authv3.RegisterAuthorizationServer(server, extauth.New(validator))
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(server, healthServer)
	return server
}

func gracefulStop(server *grpc.Server) {
	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()

	timer := time.NewTimer(gracefulStopTimeout)
	defer timer.Stop()
	select {
	case <-stopped:
	case <-timer.C:
		log.Printf("graceful gRPC shutdown timed out; force stopping")
		server.Stop()
		<-stopped
	}
}
