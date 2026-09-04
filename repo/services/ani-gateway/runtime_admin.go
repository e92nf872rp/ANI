package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kubercloud/ani/runtimeadmin"
)

const (
	gatewayCanonicalServiceName = "ani-gateway"
	gatewayServiceNamespace     = "ani"
	gatewayDefaultHealthPort    = 9200
)

func gatewayHealthPort() (int, error) {
	value := strings.TrimSpace(os.Getenv("HEALTH_PORT"))
	if value == "" {
		return gatewayDefaultHealthPort, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("HEALTH_PORT must be an integer from 1 through 65535")
	}
	return port, nil
}

func newGatewayRuntimeAdmin(logger *slog.Logger) (*runtimeadmin.Runtime, error) {
	return runtimeadmin.New(runtimeadmin.Config{
		Identity: runtimeadmin.Identity{
			Namespace: gatewayServiceNamespace,
			Name:      gatewayCanonicalServiceName,
		},
		Logger: logger,
	})
}

type gatewayRuntimeAdminServer struct {
	runtime      *runtimeadmin.Runtime
	server       *http.Server
	logger       *slog.Logger
	shutdownOnce sync.Once
	shutdownErr  error
}

func startGatewayRuntimeAdmin(logger *slog.Logger) (*gatewayRuntimeAdminServer, error) {
	port, err := gatewayHealthPort()
	if err != nil {
		return nil, err
	}
	runtime, err := newGatewayRuntimeAdmin(logger)
	if err != nil {
		return nil, fmt.Errorf("initialize runtime admin: %w", err)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		_ = runtime.Shutdown(context.Background())
		return nil, fmt.Errorf("listen for runtime admin on port %d: %w", port, err)
	}
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           runtime,
		ReadHeaderTimeout: 5 * time.Second,
	}
	management := &gatewayRuntimeAdminServer{runtime: runtime, server: server, logger: logger}
	go func() {
		logger.Info("runtime admin server listening", "port", port)
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("runtime admin serve error", "err", serveErr)
			os.Exit(1)
		}
	}()
	return management, nil
}

func (server *gatewayRuntimeAdminServer) SetServing(serving bool) {
	server.runtime.SetServing(serving)
}

func (server *gatewayRuntimeAdminServer) Shutdown(ctx context.Context) error {
	server.SetServing(false)
	server.shutdownOnce.Do(func() {
		server.shutdownErr = errors.Join(server.server.Shutdown(ctx), server.runtime.Shutdown(ctx))
	})
	return server.shutdownErr
}

func waitForGatewayPublicListener(
	ctx context.Context,
	listenAddress string,
	serverErrors <-chan error,
) error {
	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return fmt.Errorf("parse gateway listen address: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	probeURL := "http://" + net.JoinHostPort(host, port) + "/healthz"
	client := &http.Client{Timeout: 200 * time.Millisecond}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, requestErr := client.Get(probeURL)
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway public listener did not become ready: %w", ctx.Err())
		case serveErr := <-serverErrors:
			if serveErr == nil {
				return errors.New("gateway public listener stopped before becoming ready")
			}
			return fmt.Errorf("gateway public listener stopped before becoming ready: %w", serveErr)
		case <-ticker.C:
		}
	}
}
