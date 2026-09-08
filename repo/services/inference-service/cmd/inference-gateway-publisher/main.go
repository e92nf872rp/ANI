package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kubercloud/ani/services/inference-service/internal/gatewaypublish"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
)

type processDependencies struct {
	loadConfig func() (gatewaypublish.Config, error)
	openStore  func(context.Context, string) (publisherStore, func(), error)
	newKube    func(time.Duration) (gatewaypublish.KubeAPI, error)
	serve      func(*http.Server) error
}

type publisherStore interface {
	repository.PublicationStore
	Ping(context.Context) error
}

type publisherProcess struct {
	deps       processDependencies
	cfg        gatewaypublish.Config
	ready      atomic.Bool
	reconciler *gatewaypublish.Reconciler
	closeStore func()
	closeOnce  sync.Once
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runPublisher(ctx, newPublisherProcess(defaultDependencies())); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("publisher stopped", "reason", "PUBLISHER_STOPPED")
	}
}

func defaultDependencies() processDependencies {
	return processDependencies{
		loadConfig: gatewaypublish.LoadConfig,
		openStore: func(ctx context.Context, databaseURL string) (publisherStore, func(), error) {
			store, closeStore, err := repository.OpenStore(ctx, databaseURL, databaseURL)
			if err != nil {
				return nil, nil, err
			}
			return store, closeStore, nil
		},
		newKube: func(timeout time.Duration) (gatewaypublish.KubeAPI, error) {
			return gatewaypublish.NewInClusterKubeClient(timeout)
		},
		serve: func(server *http.Server) error { return server.ListenAndServe() },
	}
}

func newPublisherProcess(deps processDependencies) *publisherProcess {
	return &publisherProcess{deps: deps}
}

func runPublisher(ctx context.Context, process *publisherProcess) error {
	config, configErr := process.loadConfiguration()
	server := &http.Server{Addr: ":" + strconv.Itoa(config.HealthPort), Handler: process.healthHandler(), ReadHeaderTimeout: 2 * time.Second}
	serverErr := make(chan error, 1)
	serve := process.deps.serve
	if serve == nil {
		serve = func(server *http.Server) error { return server.ListenAndServe() }
	}
	go func() { serverErr <- serve(server) }()
	if configErr != nil {
		slog.Error("publisher configuration invalid")
		return waitUnready(ctx, server, serverErr)
	}
	if err := process.initializeWithConfig(ctx, config); err != nil {
		slog.Error("publisher initialization failed", "reason", "PUBLISHER_INITIALIZATION_FAILED")
		return waitUnready(ctx, server, serverErr)
	}
	defer process.close()
	go process.loop(ctx)
	select {
	case <-ctx.Done():
		return shutdownServer(server)
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("publisher health server failed")
	}
}

func (p *publisherProcess) loadConfiguration() (gatewaypublish.Config, error) {
	cfg, err := p.deps.loadConfig()
	if cfg.HealthPort <= 0 {
		cfg.HealthPort = 9206
	}
	p.cfg = cfg
	return cfg, err
}

func (p *publisherProcess) initialize(ctx context.Context) error {
	cfg, err := p.loadConfiguration()
	if err != nil {
		return err
	}
	return p.initializeWithConfig(ctx, cfg)
}

func (p *publisherProcess) initializeWithConfig(ctx context.Context, cfg gatewaypublish.Config) error {
	initCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()
	store, closeStore, err := p.deps.openStore(initCtx, cfg.DatabaseURL)
	if err != nil || store == nil {
		return errors.New("publisher database initialization failed")
	}
	if err := store.Ping(initCtx); err != nil {
		if closeStore != nil {
			closeStore()
		}
		return errors.New("publisher database readiness failed")
	}
	kube, err := p.deps.newKube(cfg.RequestTimeout)
	if err != nil || kube == nil {
		if closeStore != nil {
			closeStore()
		}
		return errors.New("publisher kubernetes initialization failed")
	}
	p.closeStore = closeStore
	p.reconciler = gatewaypublish.NewReconciler(store, kube, cfg, publisherOwner(), time.Now)
	p.ready.Store(true)
	return nil
}

func (p *publisherProcess) loop(ctx context.Context) {
	p.runOnce(ctx)
	ticker := time.NewTicker(p.cfg.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runOnce(ctx)
		}
	}
}

func (p *publisherProcess) runOnce(ctx context.Context) {
	if p.reconciler == nil {
		return
	}
	if _, err := p.reconciler.RunOnce(ctx); err != nil {
		slog.Warn("publisher reconciliation failed", "reason", "GATEWAY_RECONCILIATION_FAILED")
	}
}

func (p *publisherProcess) close() {
	p.closeOnce.Do(func() {
		if p.closeStore != nil {
			p.closeStore()
			p.closeStore = nil
		}
	})
}

func (p *publisherProcess) healthHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/healthz":
			writer.WriteHeader(http.StatusOK)
		case "/readyz":
			if p.ready.Load() {
				writer.WriteHeader(http.StatusOK)
				return
			}
			writer.WriteHeader(http.StatusServiceUnavailable)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})
}

func publisherOwner() string {
	if owner := strings.TrimSpace(os.Getenv("INFERENCE_AI_GATEWAY_PUBLISHER_OWNER")); owner != "" {
		return owner
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "inference-gateway-publisher"
	}
	return "inference-gateway-publisher/" + host
}

func shutdownServer(server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("publisher health server shutdown failed")
	}
	return nil
}

func waitUnready(ctx context.Context, server *http.Server, serverErr <-chan error) error {
	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("PUBLISHER_HEALTH_SERVER_FAILED")
	case <-ctx.Done():
		return shutdownServer(server)
	}
}
