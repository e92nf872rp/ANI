package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// RunGRPC starts a gRPC server on port and blocks until SIGINT/SIGTERM.
// register is called to register all service implementations.
func RunGRPC(port int, register func(*grpc.Server), deps *Deps) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		deps.Logger.Error("failed to listen", "port", port, "err", err)
		os.Exit(1)
	}

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			loggingUnaryInterceptor(deps.Logger),
			recoveryUnaryInterceptor(deps.Logger),
		),
	)

	register(srv)
	reflection.Register(srv)

	var probe *http.Server
	var probeListener net.Listener
	var runtimeAdminShutdown func(context.Context) error
	var setServing func(bool)
	if deps.HealthPort > 0 {
		runtimeAdmin, runtimeErr := newRuntimeAdminForDeps(deps)
		if runtimeErr != nil {
			_ = lis.Close()
			deps.Logger.Error("failed to initialize runtime admin", "err", runtimeErr)
			os.Exit(1)
		}
		probeListener, err = net.Listen("tcp", fmt.Sprintf(":%d", deps.HealthPort))
		if err != nil {
			_ = runtimeAdmin.Shutdown(context.Background())
			_ = lis.Close()
			deps.Logger.Error("failed to listen for runtime admin", "port", deps.HealthPort, "err", err)
			os.Exit(1)
		}
		probe = &http.Server{
			Addr:              fmt.Sprintf(":%d", deps.HealthPort),
			Handler:           runtimeAdmin,
			ReadHeaderTimeout: 5 * time.Second,
		}
		runtimeAdminShutdown = runtimeAdmin.Shutdown
		setServing = runtimeAdmin.SetServing
		go func() {
			deps.Logger.Info("runtime admin server listening", "port", deps.HealthPort)
			if serveErr := probe.Serve(probeListener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				deps.Logger.Error("runtime admin serve error", "err", serveErr)
				os.Exit(1)
			}
		}()
	}

	go func() {
		deps.Logger.Info("gRPC server listening", "port", port)
		if err := srv.Serve(lis); err != nil {
			deps.Logger.Error("gRPC serve error", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if setServing != nil {
		setServing(true)
	}
	<-ctx.Done()

	deps.Logger.Info("shutting down gRPC server gracefully")
	if setServing != nil {
		setServing(false)
	}
	if probe != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := probe.Shutdown(shutdownCtx); err != nil {
			deps.Logger.Error("runtime admin shutdown error", "err", err)
		}
		if runtimeAdminShutdown != nil {
			if err := runtimeAdminShutdown(shutdownCtx); err != nil {
				deps.Logger.Error("runtime telemetry shutdown error", "err", err)
			}
		}
		cancel()
	}
	srv.GracefulStop()
	deps.Logger.Info("gRPC server stopped")
}

func loggingUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			logger.ErrorContext(ctx, "gRPC error", "method", info.FullMethod, "err", err)
		}
		return resp, err
	}
}

func recoveryUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorContext(ctx, "gRPC panic recovered", "method", info.FullMethod, "panic", r)
				err = fmt.Errorf("internal error")
			}
		}()
		return handler(ctx, req)
	}
}
