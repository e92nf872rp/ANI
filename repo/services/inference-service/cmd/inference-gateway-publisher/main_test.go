package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/kubercloud/ani/services/inference-service/internal/gatewaypublish"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
)

type readinessStoreFake struct {
	pingErr error
	pings   int
}

func (s *readinessStoreFake) Ping(context.Context) error { s.pings++; return s.pingErr }
func (s *readinessStoreFake) ClaimPublication(context.Context, string, time.Time, time.Duration) (repository.PublicationTarget, bool, error) {
	return repository.PublicationTarget{}, false, nil
}
func (s *readinessStoreFake) RenewPublication(context.Context, repository.PublicationTarget, time.Time, time.Duration) error {
	return nil
}
func (s *readinessStoreFake) CompletePublication(context.Context, repository.PublicationResult) error {
	return nil
}
func (s *readinessStoreFake) FailPublication(context.Context, repository.PublicationTarget, string, time.Time) error {
	return nil
}

func TestInvalidConfigLeavesReadinessDownAndDoesNotInitializeDependencies(t *testing.T) {
	called := false
	process := newPublisherProcess(processDependencies{
		loadConfig: func() (gatewaypublish.Config, error) {
			return gatewaypublish.Config{HealthPort: 9206}, errors.New("invalid")
		},
		openStore: func(context.Context, string) (publisherStore, func(), error) {
			called = true
			return nil, nil, nil
		},
		newKube: func(time.Duration) (gatewaypublish.KubeAPI, error) { called = true; return nil, nil },
	})
	if err := process.initialize(context.Background()); err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	for path, want := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusServiceUnavailable} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		process.healthHandler().ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Fatalf("%s = %d", path, recorder.Code)
		}
	}
}

func validPublisherConfig() gatewaypublish.Config {
	base, _ := url.Parse("https://ai.example.com")
	return gatewaypublish.Config{DatabaseURL: "postgres://redacted", PublicBaseURL: base, GatewayNamespace: "ani-aigw", GatewayName: "ani-aigw", GatewayController: "gateway.envoyproxy.io/gatewayclass-controller", ReconcileInterval: time.Second, RequestTimeout: time.Second, StatusTimeout: time.Second, LeaseDuration: time.Second, HealthPort: 9206}
}

func TestInitializeRequiresStorePingBeforeReady(t *testing.T) {
	store := &readinessStoreFake{pingErr: errors.New("dsn must not leak")}
	closed, kubeCalled := 0, false
	p := newPublisherProcess(processDependencies{
		loadConfig: func() (gatewaypublish.Config, error) { return validPublisherConfig(), nil },
		openStore:  func(context.Context, string) (publisherStore, func(), error) { return store, func() { closed++ }, nil },
		newKube:    func(time.Duration) (gatewaypublish.KubeAPI, error) { kubeCalled = true; return nil, nil },
	})
	if err := p.initialize(context.Background()); err == nil || store.pings != 1 || closed != 1 || kubeCalled || p.ready.Load() {
		t.Fatalf("err=%v pings=%d closed=%d kube=%v ready=%v", err, store.pings, closed, kubeCalled, p.ready.Load())
	}
}

func TestInitializeHandlesNilStoreCloseOnPingFailure(t *testing.T) {
	store := &readinessStoreFake{pingErr: errors.New("not ready")}
	p := newPublisherProcess(processDependencies{
		loadConfig: func() (gatewaypublish.Config, error) { return validPublisherConfig(), nil },
		openStore:  func(context.Context, string) (publisherStore, func(), error) { return store, nil, nil },
	})
	if err := p.initialize(context.Background()); err == nil || p.ready.Load() {
		t.Fatalf("err=%v ready=%v", err, p.ready.Load())
	}
}

func TestRunPublisherReturnsStableServerBindFailureWithoutWaitingForSignal(t *testing.T) {
	p := newPublisherProcess(processDependencies{
		loadConfig: func() (gatewaypublish.Config, error) { return validPublisherConfig(), errors.New("bad config") },
		serve:      func(*http.Server) error { return errors.New("bind endpoint") },
	})
	if err := runPublisher(context.Background(), p); err == nil || err.Error() != "PUBLISHER_HEALTH_SERVER_FAILED" {
		t.Fatalf("err=%v", err)
	}
}
