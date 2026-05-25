package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalWorkloadReconcileControllerReconcileNowUpdatesStore(t *testing.T) {
	store := newReconcileMemoryStore()
	record := reconcileTestRecord(ports.WorkloadStateProvisioning)
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	controller := NewLocalWorkloadReconcileController(
		store,
		store,
		NewLocalProviderStatusReader(WithStatusReaderClock(func() time.Time { return time.Unix(210, 0) })),
		NewLocalStatusReconciler(WithReconcileClock(func() time.Time { return time.Unix(220, 0) })),
		ports.ReconcileControllerConfig{},
		WithReconcileControllerClock(func() time.Time { return time.Unix(200, 0) }),
	)

	result, err := controller.ReconcileNow(context.Background(), ports.ReconcileTarget{
		TenantID:   record.TenantID,
		InstanceID: record.InstanceID,
		Kind:       record.Kind,
		Provider:   record.Provider,
		State:      record.Status.State,
	})
	if err != nil {
		t.Fatalf("ReconcileNow() error = %v", err)
	}
	if !result.StateChanged || result.PreviousState != ports.WorkloadStateProvisioning || result.CurrentState != ports.WorkloadStateRunning {
		t.Fatalf("unexpected reconcile result: %+v", result)
	}
	updated, err := store.Get(context.Background(), record.TenantID, record.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("stored state = %s, want running", updated.Status.State)
	}
}

func TestLocalWorkloadReconcileControllerMarksProviderMissing(t *testing.T) {
	store := newReconcileMemoryStore()
	record := reconcileTestRecord(ports.WorkloadStateRunning)
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	controller := NewLocalWorkloadReconcileController(
		store,
		store,
		missingProviderStatusReader{},
		NewLocalStatusReconciler(),
		ports.ReconcileControllerConfig{},
		WithReconcileControllerClock(func() time.Time { return time.Unix(300, 0) }),
	)

	result, err := controller.ReconcileNow(context.Background(), ports.ReconcileTarget{
		TenantID:   record.TenantID,
		InstanceID: record.InstanceID,
		Kind:       record.Kind,
		Provider:   record.Provider,
		State:      record.Status.State,
	})
	if err != nil {
		t.Fatalf("ReconcileNow() error = %v", err)
	}
	if !result.ProviderMissing || result.CurrentState != ports.WorkloadStateFailed {
		t.Fatalf("unexpected missing-provider result: %+v", result)
	}
	updated, err := store.Get(context.Background(), record.TenantID, record.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != ports.WorkloadStateFailed || updated.Status.Reason != "ProviderResourceLost" {
		t.Fatalf("stored status = %+v, want failed ProviderResourceLost", updated.Status)
	}
}

func TestLocalWorkloadReconcileControllerRunOnceUsesTargetLister(t *testing.T) {
	store := newReconcileMemoryStore()
	record := reconcileTestRecord(ports.WorkloadStateProvisioning)
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	controller := NewLocalWorkloadReconcileController(
		store,
		store,
		NewLocalProviderStatusReader(),
		NewLocalStatusReconciler(),
		ports.ReconcileControllerConfig{MaxConcurrentReconciles: 1, StaleThresholdSeconds: 60},
		WithReconcileControllerClock(func() time.Time { return time.Unix(400, 0) }),
	)

	active, err := controller.runOnce(context.Background())
	if err != nil {
		t.Fatalf("runOnce() error = %v", err)
	}
	if !active {
		t.Fatalf("runOnce() active = false, want true for transient target")
	}
	if store.listRequests != 1 {
		t.Fatalf("ListReconcileTargets calls = %d, want 1", store.listRequests)
	}
}

func TestLocalWorkloadReconcileControllerBacksOffFailedTargetsAndContinues(t *testing.T) {
	store := newReconcileMemoryStore()
	failing := reconcileTestRecord(ports.WorkloadStateProvisioning)
	failing.InstanceID = "inst-failing"
	failing.Status.Ref.InstanceID = "inst-failing"
	ok := reconcileTestRecord(ports.WorkloadStateProvisioning)
	ok.InstanceID = "inst-ok"
	ok.Status.Ref.InstanceID = "inst-ok"
	ok.ResourceRefs = []string{"kubernetes/Deployment/ok"}
	if err := store.UpsertStatus(context.Background(), failing); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertStatus(context.Background(), ok); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(500, 0)
	reader := &selectiveFailingStatusReader{failInstanceID: failing.InstanceID}
	controller := NewLocalWorkloadReconcileController(
		store,
		store,
		reader,
		NewLocalStatusReconciler(WithReconcileClock(func() time.Time { return now.Add(1 * time.Second) })),
		ports.ReconcileControllerConfig{FailureBackoffSeconds: 30},
		WithReconcileControllerClock(func() time.Time { return now }),
	)

	active, err := controller.runOnce(context.Background())
	if err != nil {
		t.Fatalf("runOnce() error = %v, want nil when one target fails", err)
	}
	if !active {
		t.Fatalf("runOnce() active = false, want true")
	}
	updated, err := store.Get(context.Background(), ok.TenantID, ok.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("second target state = %s, want running after first target failure", updated.Status.State)
	}
	metrics := controller.Metrics()
	if metrics.Ticks != 1 || metrics.Successes != 1 || metrics.Failures != 1 || metrics.SkippedBackoff != 0 {
		t.Fatalf("metrics after first run = %+v, want ticks=1 successes=1 failures=1 skipped=0", metrics)
	}

	active, err = controller.runOnce(context.Background())
	if err != nil {
		t.Fatalf("second runOnce() error = %v", err)
	}
	if !active {
		t.Fatalf("second runOnce() active = false, want true")
	}
	metrics = controller.Metrics()
	if reader.callsFor(failing.InstanceID) != 1 {
		t.Fatalf("failing target observe calls = %d, want still 1 inside backoff", reader.callsFor(failing.InstanceID))
	}
	if metrics.Ticks != 2 || metrics.SkippedBackoff != 1 {
		t.Fatalf("metrics after backoff skip = %+v, want ticks=2 skipped=1", metrics)
	}

	now = now.Add(31 * time.Second)
	if _, err := controller.runOnce(context.Background()); err != nil {
		t.Fatalf("third runOnce() error = %v", err)
	}
	if reader.callsFor(failing.InstanceID) != 2 {
		t.Fatalf("failing target observe calls = %d, want retry after backoff", reader.callsFor(failing.InstanceID))
	}
}

func TestLeaderElectingWorkloadReconcileControllerRunsDelegateUnderElector(t *testing.T) {
	delegate := &fakeReconcileDelegate{}
	elector := &fakeReconcileLeaderElector{}
	controller := NewLeaderElectingWorkloadReconcileController(delegate, elector)

	if err := controller.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !elector.ran {
		t.Fatalf("leader elector was not used")
	}
	if delegate.starts != 1 {
		t.Fatalf("delegate starts = %d, want 1", delegate.starts)
	}
}

func TestLeaderElectingWorkloadReconcileControllerDelegatesMetrics(t *testing.T) {
	delegate := &fakeReconcileDelegate{metrics: ports.ReconcileControllerMetrics{Ticks: 3, Successes: 2}}
	controller := NewLeaderElectingWorkloadReconcileController(delegate, &fakeReconcileLeaderElector{})

	metrics := controller.Metrics()
	if metrics.Ticks != 3 || metrics.Successes != 2 {
		t.Fatalf("Metrics() = %+v, want delegated ticks=3 successes=2", metrics)
	}
}

type selectiveFailingStatusReader struct {
	failInstanceID string
	calls          map[string]int
}

func (r *selectiveFailingStatusReader) Observe(ctx context.Context, request ports.WorkloadProviderStatusRequest) (ports.WorkloadProviderObservation, error) {
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	r.calls[request.InstanceID]++
	if request.InstanceID == r.failInstanceID {
		return ports.WorkloadProviderObservation{}, errors.New("provider timeout")
	}
	return NewLocalProviderStatusReader().Observe(ctx, request)
}

func (r *selectiveFailingStatusReader) callsFor(instanceID string) int {
	if r.calls == nil {
		return 0
	}
	return r.calls[instanceID]
}

type missingProviderStatusReader struct{}

func (missingProviderStatusReader) Observe(context.Context, ports.WorkloadProviderStatusRequest) (ports.WorkloadProviderObservation, error) {
	return ports.WorkloadProviderObservation{}, ports.ErrNotFound
}

type fakeReconcileDelegate struct {
	starts  int
	metrics ports.ReconcileControllerMetrics
}

func (c *fakeReconcileDelegate) Start(context.Context) error {
	c.starts++
	return nil
}

func (*fakeReconcileDelegate) ReconcileNow(context.Context, ports.ReconcileTarget) (ports.ReconcileResult, error) {
	return ports.ReconcileResult{TenantID: "tenant-a", InstanceID: "inst-a"}, nil
}

func (c *fakeReconcileDelegate) Metrics() ports.ReconcileControllerMetrics {
	return c.metrics
}

type fakeReconcileLeaderElector struct {
	ran bool
}

func (e *fakeReconcileLeaderElector) Run(ctx context.Context, run func(context.Context) error) error {
	e.ran = true
	return run(ctx)
}

var _ ports.WorkloadReconcileController = (*fakeReconcileDelegate)(nil)
var _ ports.ReconcileControllerMetricsReader = (*fakeReconcileDelegate)(nil)
var _ ports.ReconcileLeaderElector = (*fakeReconcileLeaderElector)(nil)

type reconcileMemoryStore struct {
	records      map[string]ports.WorkloadInstanceRecord
	listRequests int
}

func newReconcileMemoryStore() *reconcileMemoryStore {
	return &reconcileMemoryStore{records: map[string]ports.WorkloadInstanceRecord{}}
}

func (s *reconcileMemoryStore) UpsertStatus(_ context.Context, record ports.WorkloadInstanceRecord) error {
	s.records[record.TenantID+"/"+record.InstanceID] = record
	return nil
}

func (s *reconcileMemoryStore) Get(_ context.Context, tenantID string, instanceID string) (ports.WorkloadInstanceRecord, error) {
	record, ok := s.records[tenantID+"/"+instanceID]
	if !ok {
		return ports.WorkloadInstanceRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *reconcileMemoryStore) List(_ context.Context, tenantID string, kind ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	var records []ports.WorkloadInstanceRecord
	for _, record := range s.records {
		if record.TenantID != tenantID {
			continue
		}
		if kind != "" && record.Kind != kind {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *reconcileMemoryStore) ListReconcileTargets(_ context.Context, request ports.ReconcileTargetListRequest) ([]ports.ReconcileTarget, error) {
	s.listRequests++
	if request.Limit == 0 {
		return nil, errors.New("limit must be defaulted by controller")
	}
	var targets []ports.ReconcileTarget
	for _, record := range s.records {
		targets = append(targets, ports.ReconcileTarget{
			TenantID:       record.TenantID,
			InstanceID:     record.InstanceID,
			Kind:           record.Kind,
			State:          record.Status.State,
			Provider:       record.Provider,
			LastObservedAt: record.UpdatedAt,
		})
	}
	return targets, nil
}

func reconcileTestRecord(state ports.WorkloadState) ports.WorkloadInstanceRecord {
	updatedAt := time.Unix(100, 0).UTC()
	return ports.WorkloadInstanceRecord{
		TenantID:     "tenant-a",
		InstanceID:   "inst-a",
		Name:         "vm-a",
		Kind:         ports.WorkloadKindVM,
		Provider:     "local",
		AuditID:      "11111111-1111-4111-8111-111111111111",
		ResourceRefs: []string{"VirtualMachine/tenant-a/vm-a"},
		Status: ports.WorkloadStatus{
			Ref: ports.WorkloadRef{
				TenantID:   "tenant-a",
				InstanceID: "inst-a",
				Kind:       ports.WorkloadKindVM,
				ProviderID: "vm-a",
			},
			State:     state,
			Reason:    "before reconcile",
			UpdatedAt: updatedAt,
		},
		CreatedAt: updatedAt,
		UpdatedAt: updatedAt,
	}
}

var _ ports.WorkloadInstanceStore = (*reconcileMemoryStore)(nil)
var _ ports.ReconcileTargetLister = (*reconcileMemoryStore)(nil)
