package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type fakeExpirableSandboxLister struct {
	records []ports.WorkloadInstanceRecord
	err     error
}

func (l *fakeExpirableSandboxLister) ListRunningSandboxes(context.Context, int) ([]ports.WorkloadInstanceRecord, error) {
	return l.records, l.err
}

type fakeExpirationStore struct {
	last     ports.WorkloadInstanceRecord
	upserted int
	err      error
}

func (s *fakeExpirationStore) UpsertStatus(_ context.Context, record ports.WorkloadInstanceRecord) error {
	if s.err != nil {
		return s.err
	}
	s.last = record
	s.upserted++
	return nil
}

func (s *fakeExpirationStore) UpsertStatusAsPlatform(_ context.Context, record ports.WorkloadInstanceRecord) error {
	return s.UpsertStatus(context.Background(), record)
}

func (s *fakeExpirationStore) Get(context.Context, string, string) (ports.WorkloadInstanceRecord, error) {
	return s.last, nil
}

func (s *fakeExpirationStore) List(context.Context, string, ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	return nil, nil
}

func TestSandboxConfigExpiredSessionDeadline(t *testing.T) {
	now := time.Unix(5000, 0).UTC()
	cases := []struct {
		name   string
		config ports.SandboxConfig
		want   bool
	}{
		{"future session deadline", ports.SandboxConfig{ExpiresAt: now.Add(time.Hour)}, false},
		{"past session deadline", ports.SandboxConfig{ExpiresAt: now.Add(-time.Minute)}, true},
		{"zero expiry never expired", ports.SandboxConfig{}, false},
		{"idle past deadline", ports.SandboxConfig{IdleTimeout: 10 * time.Minute, LastActivityAt: now.Add(-11 * time.Minute)}, true},
		{"idle within deadline", ports.SandboxConfig{IdleTimeout: 10 * time.Minute, LastActivityAt: now.Add(-5 * time.Minute)}, false},
		{"idle disabled zero activity", ports.SandboxConfig{IdleTimeout: 10 * time.Minute}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxConfigExpired(tc.config, now); got != tc.want {
				t.Fatalf("sandboxConfigExpired() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOnTimeoutActionMapsKillToDelete(t *testing.T) {
	if onTimeoutAction("kill") != ports.WorkloadLifecycleDelete {
		t.Fatalf("kill = %s, want delete", onTimeoutAction("kill"))
	}
	if onTimeoutAction("pause") != ports.WorkloadLifecyclePause {
		t.Fatalf("pause = %s, want pause", onTimeoutAction("pause"))
	}
	if onTimeoutAction("") != ports.WorkloadLifecyclePause {
		t.Fatalf("empty = %s, want pause default", onTimeoutAction(""))
	}
	if onTimeoutAction("unexpected") != ports.WorkloadLifecyclePause {
		t.Fatalf("unexpected = %s, want pause default", onTimeoutAction("unexpected"))
	}
}

func TestSandboxExpirationControllerReconcilesExpiredSandbox(t *testing.T) {
	now := time.Unix(5000, 0).UTC()
	lr := NewLocalSandboxRuntime(WithSandboxRuntimeClock(func() time.Time { return now }))

	// Created at 3000, session timeout defaults to 30m -> ExpiresAt 4800 < now.
	status, err := lr.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "expirable",
		AutoStart: true,
		Config:    ports.SandboxConfig{OnTimeout: "pause"},
		CreatedAt: time.Unix(3000, 0),
	})
	if err != nil {
		t.Fatalf("sandbox.Create() error = %v", err)
	}
	record := ports.WorkloadInstanceRecord{
		TenantID:   "tenant-a",
		InstanceID: status.InstanceID,
		Name:       status.Name,
		Provider:   status.Provider,
		Kind:       ports.WorkloadKindSandbox,
		Status:     ports.WorkloadStatus{State: ports.WorkloadStateRunning},
		Sandbox:    &status,
	}
	lister := &fakeExpirableSandboxLister{records: []ports.WorkloadInstanceRecord{record}}
	store := &fakeExpirationStore{}
	controller := NewSandboxExpirationController(lister, store, lr,
		WithSandboxExpirationClock(func() time.Time { return now }),
	)

	if err := controller.reconcileExpired(context.Background()); err != nil {
		t.Fatalf("reconcileExpired() error = %v", err)
	}
	if store.upserted != 1 {
		t.Fatalf("upsert count = %d, want 1", store.upserted)
	}
	if store.last.Sandbox == nil || store.last.Sandbox.State != ports.SandboxStateExpired {
		t.Fatalf("sandbox state = %+v, want expired", store.last.Sandbox)
	}
	if store.last.Status.State != ports.WorkloadStateStopped {
		t.Fatalf("workload state = %s, want stopped (expired mapping)", store.last.Status.State)
	}
	if store.last.Status.Reason != "SandboxExpired" {
		t.Fatalf("reason = %q, want SandboxExpired", store.last.Status.Reason)
	}
}

func TestSandboxExpirationControllerSkipsNonExpired(t *testing.T) {
	now := time.Unix(5000, 0).UTC()
	lr := NewLocalSandboxRuntime(WithSandboxRuntimeClock(func() time.Time { return now }))
	status, err := lr.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "not-yet-expired",
		AutoStart: true,
		CreatedAt: time.Unix(4900, 0), // ExpiresAt 6700 > now.
	})
	if err != nil {
		t.Fatalf("sandbox.Create() error = %v", err)
	}
	record := ports.WorkloadInstanceRecord{
		TenantID:   "tenant-a",
		InstanceID: status.InstanceID,
		Name:       status.Name,
		Provider:   status.Provider,
		Kind:       ports.WorkloadKindSandbox,
		Status:     ports.WorkloadStatus{State: ports.WorkloadStateRunning},
		Sandbox:    &status,
	}
	lister := &fakeExpirableSandboxLister{records: []ports.WorkloadInstanceRecord{record}}
	store := &fakeExpirationStore{}
	controller := NewSandboxExpirationController(lister, store, lr,
		WithSandboxExpirationClock(func() time.Time { return now }),
	)
	if err := controller.reconcileExpired(context.Background()); err != nil {
		t.Fatalf("reconcileExpired() error = %v", err)
	}
	if store.upserted != 0 {
		t.Fatalf("upsert count = %d, want 0", store.upserted)
	}
}

func TestSandboxExpirationControllerNotFoundLister(t *testing.T) {
	controller := NewSandboxExpirationController(nil, nil, nil)
	if err := controller.Start(context.Background()); err != nil {
		t.Fatalf("Start() with missing deps error = %v, want nil (no-op)", err)
	}
}
