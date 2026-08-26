package runtime

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
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

func TestLocalWorkloadReconcileControllerPersistsKubernetesPrimaryResourceLoss(t *testing.T) {
	store := newReconcileMemoryStore()
	record := reconcileTestRecord(ports.WorkloadStateRunning)
	record.Name = "app-a"
	record.Kind = ports.WorkloadKindSandbox
	record.Provider = "kubernetes_sandbox_runtime"
	record.ResourceRefs = []string{"kubernetes/Deployment/app-a"}
	record.Status.Ref.Kind = ports.WorkloadKindSandbox
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, `{"message":"deployments.apps app-a not found"}`), nil
	})
	client := newTestKubernetesRESTClient(t, transport)
	controller := NewLocalWorkloadReconcileController(
		store,
		store,
		NewKubernetesProviderAdapter(client),
		NewLocalStatusReconciler(),
		ports.ReconcileControllerConfig{},
		WithReconcileControllerClock(func() time.Time { return time.Unix(300, 0) }),
	)
	target := ports.ReconcileTarget{
		TenantID:   record.TenantID,
		InstanceID: record.InstanceID,
		Kind:       record.Kind,
		Provider:   record.Provider,
		State:      record.Status.State,
	}

	first, err := controller.ReconcileNow(context.Background(), target)
	if err != nil {
		t.Fatalf("ReconcileNow(first) error = %v", err)
	}
	if !first.ProviderMissing || !first.StateChanged || first.CurrentState != ports.WorkloadStateFailed || first.Reason != "ProviderResourceLost" {
		t.Fatalf("first reconcile result = %+v, want changed failed ProviderResourceLost", first)
	}
	stored, err := store.Get(context.Background(), record.TenantID, record.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status.State != ports.WorkloadStateFailed || stored.Status.Reason != "ProviderResourceLost" {
		t.Fatalf("stored status = %+v, want failed ProviderResourceLost", stored.Status)
	}

	second, err := controller.ReconcileNow(context.Background(), target)
	if err != nil {
		t.Fatalf("ReconcileNow(second) error = %v", err)
	}
	if !second.ProviderMissing || second.StateChanged || second.CurrentState != ports.WorkloadStateFailed || second.Reason != "ProviderResourceLost" {
		t.Fatalf("second reconcile result = %+v, want stable failed ProviderResourceLost", second)
	}
}

func TestLocalWorkloadReconcileControllerDoesNotReconcileDeletedInstance(t *testing.T) {
	store := newReconcileMemoryStore()
	record := reconcileTestRecord(ports.WorkloadStateDeleted)
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	controller := NewLocalWorkloadReconcileController(
		store,
		store,
		missingProviderStatusReader{},
		NewLocalStatusReconciler(),
		ports.ReconcileControllerConfig{},
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
	if result.CurrentState != ports.WorkloadStateDeleted || result.ProviderMissing || result.StateChanged {
		t.Fatalf("result = %+v, want unchanged deleted state", result)
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

func TestLocalWorkloadReconcileControllerInjectsTargetTenantContext(t *testing.T) {
	store := newReconcileMemoryStore()
	record := reconcileTestRecord(ports.WorkloadStateProvisioning)
	record.TenantID = "5dbb1d01-0000-4000-8000-000000000001"
	record.Status.Ref.TenantID = record.TenantID
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	tenantStore := &tenantContextReconcileStore{
		reconcileMemoryStore: store,
		expectedTenantID:     uuid.MustParse(record.TenantID),
	}
	controller := NewLocalWorkloadReconcileController(
		tenantStore,
		tenantStore,
		NewLocalProviderStatusReader(),
		NewLocalStatusReconciler(),
		ports.ReconcileControllerConfig{},
	)

	_, err := controller.ReconcileNow(context.Background(), ports.ReconcileTarget{
		TenantID:   record.TenantID,
		InstanceID: record.InstanceID,
		Kind:       record.Kind,
		Provider:   record.Provider,
		State:      record.Status.State,
	})
	if err != nil {
		t.Fatalf("ReconcileNow() error = %v", err)
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

type tenantContextReconcileStore struct {
	*reconcileMemoryStore
	expectedTenantID uuid.UUID
}

func (s *tenantContextReconcileStore) Get(ctx context.Context, tenantID string, instanceID string) (ports.WorkloadInstanceRecord, error) {
	tenant, ok := types.TryFromContext(ctx)
	if !ok || tenant.TenantID != s.expectedTenantID {
		return ports.WorkloadInstanceRecord{}, errors.New("target tenant context missing")
	}
	return s.reconcileMemoryStore.Get(ctx, tenantID, instanceID)
}

func (s *tenantContextReconcileStore) UpsertStatus(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	tenant, ok := types.TryFromContext(ctx)
	if !ok || tenant.TenantID != s.expectedTenantID {
		return errors.New("target tenant context missing")
	}
	return s.reconcileMemoryStore.UpsertStatus(ctx, record)
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

// ── Quota reconciliation mocks (SPEC §5.1) ────────────────────────────────────

// reconcileFakeQuotaService records Confirm/Cancel/Release invocations so the
// quota transition tests can assert which TCC action fired and with which tx
// IDs. Prefixed to avoid colliding with fakes in other test files.
type reconcileFakeQuotaService struct {
	confirmCalls []reconcileQuotaCall
	cancelCalls  []reconcileQuotaCall
	releaseCalls []reconcileQuotaCall
	confirmErr   error
	cancelErr    error
	releaseErr   error
}

type reconcileQuotaCall struct {
	txIDs       []string
	resourceRef string
}

func (s *reconcileFakeQuotaService) Try(context.Context, ports.QuotaTryRequest) (ports.QuotaReservation, error) {
	return ports.QuotaReservation{}, nil
}

func (s *reconcileFakeQuotaService) TryMany(context.Context, []ports.QuotaTryRequest) ([]ports.QuotaReservation, error) {
	return nil, nil
}

func (s *reconcileFakeQuotaService) TryTx(context.Context, ports.MetadataTx, ports.QuotaTryRequest) (ports.QuotaReservation, error) {
	return ports.QuotaReservation{}, nil
}

func (s *reconcileFakeQuotaService) TryManyTx(context.Context, ports.MetadataTx, []ports.QuotaTryRequest) ([]ports.QuotaReservation, error) {
	return nil, nil
}

func (s *reconcileFakeQuotaService) Confirm(_ context.Context, _ ports.MetadataTx, txIDs []string, resourceRef string) error {
	s.confirmCalls = append(s.confirmCalls, reconcileQuotaCall{txIDs: append([]string(nil), txIDs...), resourceRef: resourceRef})
	return s.confirmErr
}

func (s *reconcileFakeQuotaService) Cancel(_ context.Context, _ ports.MetadataTx, txIDs []string) error {
	s.cancelCalls = append(s.cancelCalls, reconcileQuotaCall{txIDs: append([]string(nil), txIDs...)})
	return s.cancelErr
}

func (s *reconcileFakeQuotaService) Release(_ context.Context, _ ports.MetadataTx, txIDs []string) error {
	s.releaseCalls = append(s.releaseCalls, reconcileQuotaCall{txIDs: append([]string(nil), txIDs...)})
	return s.releaseErr
}

var _ ports.QuotaService = (*reconcileFakeQuotaService)(nil)

// reconcileFakeMetadataStore runs the callback with a stub MetadataTx and
// records how many tenant transactions were opened.
type reconcileFakeMetadataStore struct {
	txCount int
	txErr   error
}

func (s *reconcileFakeMetadataStore) Ping(context.Context) error { return nil }

func (s *reconcileFakeMetadataStore) WithTenantTx(_ context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	s.txCount++
	if s.txErr != nil {
		return s.txErr
	}
	return fn(context.Background(), reconcileFakeMetadataTx{})
}

func (s *reconcileFakeMetadataStore) WithPlatformTx(_ context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(context.Background(), reconcileFakeMetadataTx{})
}

var _ ports.MetadataStore = (*reconcileFakeMetadataStore)(nil)

// reconcileFakeMetadataTx is a no-op MetadataTx used by the tests; the real
// status persistence goes through storeTx.UpsertStatusTx.
type reconcileFakeMetadataTx struct{}

func (reconcileFakeMetadataTx) Exec(context.Context, string, ...any) (ports.CommandTag, error) {
	return ports.CommandTag{}, nil
}

func (reconcileFakeMetadataTx) Query(context.Context, string, ...any) (ports.Rows, error) {
	return nil, nil
}

func (reconcileFakeMetadataTx) QueryRow(context.Context, string, ...any) ports.Row {
	return nil
}

var _ ports.MetadataTx = reconcileFakeMetadataTx{}

// reconcileFakeInstanceStoreTx records UpsertStatusTx writes so tests can
// assert the status landed in the same transaction as the quota call.
type reconcileFakeInstanceStoreTx struct {
	txWrites []ports.WorkloadInstanceRecord
	txErr    error
}

func (s *reconcileFakeInstanceStoreTx) UpsertStatusTx(_ context.Context, _ ports.MetadataTx, record ports.WorkloadInstanceRecord) error {
	s.txWrites = append(s.txWrites, record)
	return s.txErr
}

var _ ports.WorkloadInstanceStoreTx = (*reconcileFakeInstanceStoreTx)(nil)

// phaseConfigurableStatusReader returns a configured provider phase so the
// reconciler can map it to running/failed/provisioning.
type phaseConfigurableStatusReader struct {
	phase    string
	reason   string
	nodeName string
}

func (r phaseConfigurableStatusReader) Observe(_ context.Context, request ports.WorkloadProviderStatusRequest) (ports.WorkloadProviderObservation, error) {
	if !request.ApplyResult.Applied {
		return ports.WorkloadProviderObservation{}, errors.New("apply must be applied")
	}
	return ports.WorkloadProviderObservation{
		TenantID:     request.TenantID,
		InstanceID:   request.InstanceID,
		Kind:         request.Kind,
		Provider:     request.ApplyResult.Provider,
		ResourceRefs: append([]string(nil), request.ApplyResult.ResourceRefs...),
		Phase:        r.phase,
		Reason:       r.reason,
		NodeName:     r.nodeName,
		ObservedAt:   time.Unix(220, 0),
	}, nil
}

var _ ports.WorkloadProviderStatusReader = phaseConfigurableStatusReader{}

// newReconcileTestRecordWithQuota returns a record pre-populated with TCC tx IDs
// and a configurable state, suitable for the quota transition tests.
func newReconcileTestRecordWithQuota(state ports.WorkloadState) ports.WorkloadInstanceRecord {
	updatedAt := time.Unix(100, 0).UTC()
	return ports.WorkloadInstanceRecord{
		TenantID:     "11111111-1111-4111-8111-111111111111",
		InstanceID:   "22222222-2222-4222-8222-222222222222",
		Name:         "vm-a",
		Kind:         ports.WorkloadKindVM,
		Provider:     "local",
		AuditID:      "11111111-1111-4111-8111-111111111111",
		ResourceRefs: []string{"VirtualMachine/tenant-a/vm-a"},
		QuotaTxIDs:   []string{"tx-1", "tx-2"},
		Status: ports.WorkloadStatus{
			Ref: ports.WorkloadRef{
				TenantID:   "11111111-1111-4111-8111-111111111111",
				InstanceID: "22222222-2222-4222-8222-222222222222",
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

// newQuotaEnabledController builds a controller wired with the quota mocks and
// a configurable status reader. The outboxWriter is injected so tests can
// assert outbox events were written in the same transaction.
func newQuotaEnabledController(
	t *testing.T,
	store *reconcileMemoryStore,
	quota *reconcileFakeQuotaService,
	meta *reconcileFakeMetadataStore,
	storeTx *reconcileFakeInstanceStoreTx,
	reader ports.WorkloadProviderStatusReader,
	clock func() time.Time,
	outbox *MockOutboxWriter,
) *LocalWorkloadReconcileController {
	t.Helper()
	return NewLocalWorkloadReconcileController(
		store,
		store,
		reader,
		NewLocalStatusReconciler(WithReconcileClock(func() time.Time { return time.Unix(220, 0) })),
		ports.ReconcileControllerConfig{},
		WithReconcileControllerClock(clock),
		WithQuotaService(quota),
		WithMetadataStore(meta),
		WithWorkloadInstanceStoreTx(storeTx),
		WithOutboxWriter(outbox),
	)
}

// TestReconcileConfirmOnRunning asserts pending -> running triggers a TCC
// Confirm (reserved -> used) inside the same tenant transaction as the status
// write (SPEC §5.1).
func TestReconcileConfirmOnRunning(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStatePending)
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	meta := &reconcileFakeMetadataStore{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	outbox := &MockOutboxWriter{}
	controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
		phaseConfigurableStatusReader{phase: "Running"},
		func() time.Time { return time.Unix(200, 0) },
		outbox)

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
	if result.CurrentState != ports.WorkloadStateRunning || !result.StateChanged {
		t.Fatalf("result = %+v, want pending->running changed", result)
	}
	if len(quota.confirmCalls) != 1 {
		t.Fatalf("Confirm calls = %d, want 1", len(quota.confirmCalls))
	}
	if len(quota.cancelCalls) != 0 || len(quota.releaseCalls) != 0 {
		t.Fatalf("Cancel=%d Release=%d, want 0/0", len(quota.cancelCalls), len(quota.releaseCalls))
	}
	if len(quota.confirmCalls[0].txIDs) != 2 || quota.confirmCalls[0].txIDs[0] != "tx-1" {
		t.Fatalf("Confirm txIDs = %#v, want [tx-1 tx-2]", quota.confirmCalls[0].txIDs)
	}
	if quota.confirmCalls[0].resourceRef != record.InstanceID {
		t.Fatalf("Confirm resourceRef = %q, want %q", quota.confirmCalls[0].resourceRef, record.InstanceID)
	}
	if len(storeTx.txWrites) != 1 || storeTx.txWrites[0].Status.State != ports.WorkloadStateRunning {
		t.Fatalf("storeTx writes = %+v, want one running write", storeTx.txWrites)
	}
	if meta.txCount != 1 {
		t.Fatalf("tenant tx count = %d, want 1", meta.txCount)
	}
	if len(outbox.events) != 1 {
		t.Fatalf("outbox events = %d, want 1", len(outbox.events))
	}
	if outbox.events[0].EventType != "instance.confirmed" {
		t.Fatalf("outbox event_type = %q, want instance.confirmed", outbox.events[0].EventType)
	}
	if outbox.events[0].TenantID != record.TenantID || outbox.events[0].AggregateID != record.InstanceID {
		t.Fatalf("outbox event = %+v, want tenant=%s id=%s", outbox.events[0], record.TenantID, record.InstanceID)
	}
}

// TestReconcileCancelOnFailed asserts pending -> failed triggers a TCC Cancel
// (release reserved) inside the same tenant transaction.
func TestReconcileCancelOnFailed(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStatePending)
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	meta := &reconcileFakeMetadataStore{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	outbox := &MockOutboxWriter{}
	controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
		phaseConfigurableStatusReader{phase: "Failed"},
		func() time.Time { return time.Unix(200, 0) },
		outbox)

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
	if result.CurrentState != ports.WorkloadStateFailed || !result.StateChanged {
		t.Fatalf("result = %+v, want pending->failed changed", result)
	}
	if len(quota.cancelCalls) != 1 {
		t.Fatalf("Cancel calls = %d, want 1", len(quota.cancelCalls))
	}
	if len(quota.confirmCalls) != 0 || len(quota.releaseCalls) != 1 {
		t.Fatalf("Confirm=%d Release=%d, want 0/1 (Cancel+Release fallback in cancelQuota)", len(quota.confirmCalls), len(quota.releaseCalls))
	}
	if len(quota.cancelCalls[0].txIDs) != 2 {
		t.Fatalf("Cancel txIDs = %#v, want [tx-1 tx-2]", quota.cancelCalls[0].txIDs)
	}
	if len(storeTx.txWrites) != 1 || storeTx.txWrites[0].Status.State != ports.WorkloadStateFailed {
		t.Fatalf("storeTx writes = %+v, want one failed write", storeTx.txWrites)
	}
	if len(outbox.events) != 1 {
		t.Fatalf("outbox events = %d, want 1", len(outbox.events))
	}
	if outbox.events[0].EventType != "instance.cancelled" {
		t.Fatalf("outbox event_type = %q, want instance.cancelled", outbox.events[0].EventType)
	}
}

// TestReconcileReleaseOnFailed asserts running -> failed triggers a TCC
// Cancel + Release double-call (release both reserved and used) inside the
// same tenant transaction. The Cancel handles the case where the reservation
// was still in 'reserved' state (pod crashed before selfHealConfirm).
func TestReconcileReleaseOnFailed(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStateRunning)
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	meta := &reconcileFakeMetadataStore{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	outbox := &MockOutboxWriter{}
	controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
		phaseConfigurableStatusReader{phase: "Failed"},
		func() time.Time { return time.Unix(200, 0) },
		outbox)

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
	if result.CurrentState != ports.WorkloadStateFailed || !result.StateChanged {
		t.Fatalf("result = %+v, want running->failed changed", result)
	}
	if len(quota.releaseCalls) != 1 {
		t.Fatalf("Release calls = %d, want 1", len(quota.releaseCalls))
	}
	if len(quota.cancelCalls) != 1 {
		t.Fatalf("Cancel calls = %d, want 1 (Cancel before Release)", len(quota.cancelCalls))
	}
	if len(quota.confirmCalls) != 0 {
		t.Fatalf("Confirm=%d, want 0", len(quota.confirmCalls))
	}
	if len(storeTx.txWrites) != 1 || storeTx.txWrites[0].Status.State != ports.WorkloadStateFailed {
		t.Fatalf("storeTx writes = %+v, want one failed write", storeTx.txWrites)
	}
	if len(outbox.events) != 1 {
		t.Fatalf("outbox events = %d, want 1", len(outbox.events))
	}
	if outbox.events[0].EventType != "instance.released" {
		t.Fatalf("outbox event_type = %q, want instance.released", outbox.events[0].EventType)
	}
}

// TestReconcileProvisioningTimeout asserts that a non-terminal instance older
// than the provisioning deadline is marked failed and its reserved quota is
// cancelled.
func TestReconcileProvisioningTimeout(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStateProvisioning)
	// Created 30 minutes ago; default timeout is 10 minutes.
	created := time.Unix(100, 0).UTC()
	record.CreatedAt = created
	record.UpdatedAt = created
	record.Status.UpdatedAt = created
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	meta := &reconcileFakeMetadataStore{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	outbox := &MockOutboxWriter{}
	controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
		phaseConfigurableStatusReader{phase: "Provisioning"},
		func() time.Time { return created.Add(30 * time.Minute) },
		outbox)

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
	if result.CurrentState != ports.WorkloadStateFailed || !result.StateChanged || result.Reason != "ProvisioningTimeout" {
		t.Fatalf("result = %+v, want failed ProvisioningTimeout", result)
	}
	if len(quota.cancelCalls) != 1 {
		t.Fatalf("Cancel calls = %d, want 1 for timeout", len(quota.cancelCalls))
	}
	if len(quota.confirmCalls) != 0 || len(quota.releaseCalls) != 0 {
		t.Fatalf("Confirm=%d Release=%d, want 0/0 on timeout", len(quota.confirmCalls), len(quota.releaseCalls))
	}
	if len(storeTx.txWrites) != 1 || storeTx.txWrites[0].Status.Reason != "ProvisioningTimeout" {
		t.Fatalf("storeTx writes = %+v, want failed ProvisioningTimeout", storeTx.txWrites)
	}
	if len(outbox.events) != 1 {
		t.Fatalf("outbox events = %d, want 1", len(outbox.events))
	}
	if outbox.events[0].EventType != "instance.create_failed" {
		t.Fatalf("outbox event_type = %q, want instance.create_failed", outbox.events[0].EventType)
	}
}

// TestReconcileProvisioningNotYetTimedOut asserts a freshly-created provisioning
// instance is NOT timed out and proceeds with normal reconciliation.
func TestReconcileProvisioningNotYetTimedOut(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStateProvisioning)
	created := time.Unix(100, 0).UTC()
	record.CreatedAt = created
	record.UpdatedAt = created
	record.Status.UpdatedAt = created
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	meta := &reconcileFakeMetadataStore{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	// Only 2 minutes after creation — well within the 10 minute deadline.
	controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
		phaseConfigurableStatusReader{phase: "Running"},
		func() time.Time { return created.Add(2 * time.Minute) },
		&MockOutboxWriter{})

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
	if result.Reason == "ProvisioningTimeout" {
		t.Fatalf("result = %+v, must not time out within deadline", result)
	}
}

// TestReconcileDeleteDualCall asserts CancelQuotaAndFinalize invokes both
// Cancel and Release idempotently within a single tenant transaction,
// regardless of the original instance state.
func TestReconcileDeleteDualCall(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStateRunning)
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	meta := &reconcileFakeMetadataStore{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	outbox := &MockOutboxWriter{}
	controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
		phaseConfigurableStatusReader{phase: "Running"},
		func() time.Time { return time.Unix(200, 0) },
		outbox)

	if err := controller.CancelQuotaAndFinalize(context.Background(), record); err != nil {
		t.Fatalf("CancelQuotaAndFinalize() error = %v", err)
	}
	if len(quota.cancelCalls) != 1 || len(quota.releaseCalls) != 1 {
		t.Fatalf("Cancel=%d Release=%d, want 1/1 dual call", len(quota.cancelCalls), len(quota.releaseCalls))
	}
	if len(quota.confirmCalls) != 0 {
		t.Fatalf("Confirm=%d, want 0 on delete", len(quota.confirmCalls))
	}
	if len(storeTx.txWrites) != 1 || storeTx.txWrites[0].Status.State != ports.WorkloadStateDeleted {
		t.Fatalf("storeTx writes = %+v, want one deleted write", storeTx.txWrites)
	}
	if meta.txCount != 1 {
		t.Fatalf("tenant tx count = %d, want 1", meta.txCount)
	}
	if len(outbox.events) != 1 {
		t.Fatalf("outbox events = %d, want 1", len(outbox.events))
	}
	if outbox.events[0].EventType != "instance.deleted" {
		t.Fatalf("outbox event_type = %q, want instance.deleted", outbox.events[0].EventType)
	}
}

// TestReconcileDeleteDualCallCoversAllStates asserts the dual call works
// whether the instance was pending, running, or failed before delete.
func TestReconcileDeleteDualCallCoversAllStates(t *testing.T) {
	states := []ports.WorkloadState{
		ports.WorkloadStatePending,
		ports.WorkloadStateRunning,
		ports.WorkloadStateFailed,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			store := newReconcileMemoryStore()
			record := newReconcileTestRecordWithQuota(state)
			quota := &reconcileFakeQuotaService{}
			meta := &reconcileFakeMetadataStore{}
			storeTx := &reconcileFakeInstanceStoreTx{}
			controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
				phaseConfigurableStatusReader{phase: "Running"},
				func() time.Time { return time.Unix(200, 0) },
				&MockOutboxWriter{})

			if err := controller.CancelQuotaAndFinalize(context.Background(), record); err != nil {
				t.Fatalf("CancelQuotaAndFinalize() error = %v", err)
			}
			if len(quota.cancelCalls) != 1 || len(quota.releaseCalls) != 1 {
				t.Fatalf("state=%s: Cancel=%d Release=%d, want 1/1", state, len(quota.cancelCalls), len(quota.releaseCalls))
			}
		})
	}
}

// TestReconcileQuotaDisabled asserts that when quotaService is nil, no quota
// calls are made and the status is written via the non-transactional store.
func TestReconcileQuotaDisabled(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStatePending)
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	// No quota wiring: metadataStore and storeTx are nil.
	controller := NewLocalWorkloadReconcileController(
		store,
		store,
		phaseConfigurableStatusReader{phase: "Running"},
		NewLocalStatusReconciler(WithReconcileClock(func() time.Time { return time.Unix(220, 0) })),
		ports.ReconcileControllerConfig{},
		WithReconcileControllerClock(func() time.Time { return time.Unix(200, 0) }),
		WithQuotaService(quota),
		// Deliberately omit WithMetadataStore and WithWorkloadInstanceStoreTx.
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
	if result.CurrentState != ports.WorkloadStateRunning || !result.StateChanged {
		t.Fatalf("result = %+v, want pending->running changed", result)
	}
	if len(quota.confirmCalls) != 0 || len(quota.cancelCalls) != 0 || len(quota.releaseCalls) != 0 {
		t.Fatalf("quota calls Confirm=%d Cancel=%d Release=%d, want 0/0/0 when tx support missing",
			len(quota.confirmCalls), len(quota.cancelCalls), len(quota.releaseCalls))
	}
	// Status must still be persisted via the non-transactional store.
	updated, err := store.Get(context.Background(), record.TenantID, record.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("stored state = %s, want running", updated.Status.State)
	}
}

// TestReconcileEmptyQuotaTxIDs asserts that when QuotaTxIDs is empty (GPU_QUOTA
// disabled per-instance), Confirm/Cancel/Release are skipped but the status
// still lands in the tenant transaction.
func TestReconcileEmptyQuotaTxIDs(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStatePending)
	record.QuotaTxIDs = nil // GPU_QUOTA_ENABLED=false for this instance
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	meta := &reconcileFakeMetadataStore{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
		phaseConfigurableStatusReader{phase: "Running"},
		func() time.Time { return time.Unix(200, 0) },
		&MockOutboxWriter{})

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
	if result.CurrentState != ports.WorkloadStateRunning || !result.StateChanged {
		t.Fatalf("result = %+v, want pending->running changed", result)
	}
	if len(quota.confirmCalls) != 0 || len(quota.cancelCalls) != 0 || len(quota.releaseCalls) != 0 {
		t.Fatalf("quota calls Confirm=%d Cancel=%d Release=%d, want 0/0/0 for empty txIDs",
			len(quota.confirmCalls), len(quota.cancelCalls), len(quota.releaseCalls))
	}
	if len(storeTx.txWrites) != 1 || storeTx.txWrites[0].Status.State != ports.WorkloadStateRunning {
		t.Fatalf("storeTx writes = %+v, want one running write", storeTx.txWrites)
	}
}

// TestReconcileQuotaLoopLogsOnly asserts ReconcileQuota does not panic and does
// not correct the quota ledger when usage diverges; it only logs.
func TestReconcileQuotaLoopLogsOnly(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStateProvisioning)
	record.Kind = ports.WorkloadKindGPUContainer
	record.GPU = &ports.GPUInstanceStatus{Count: 2}
	record.ResourceRefs = []string{"kubernetes/Pod/gpu-a"}
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	meta := &reconcileFakeMetadataStore{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	reader := phaseConfigurableStatusReader{phase: "Running"}
	controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
		reader,
		func() time.Time { return time.Unix(200, 0) },
		&MockOutboxWriter{})

	// Should not panic and should not mutate quota state (warning only).
	controller.ReconcileQuota(context.Background(), []ports.ReconcileTarget{
		{
			TenantID:   record.TenantID,
			InstanceID: record.InstanceID,
			Kind:       record.Kind,
			Provider:   record.Provider,
			State:      record.Status.State,
		},
	})
	if len(quota.cancelCalls) != 0 || len(quota.releaseCalls) != 0 || len(quota.confirmCalls) != 0 {
		t.Fatalf("quota loop must not call Cancel/Release/Confirm: Cancel=%d Release=%d Confirm=%d",
			len(quota.cancelCalls), len(quota.releaseCalls), len(quota.confirmCalls))
	}
}

// TestReconcileQuotaLoopSkipsNonGPU asserts the quota loop ignores non-GPU
// workloads.
func TestReconcileQuotaLoopSkipsNonGPU(t *testing.T) {
	store := newReconcileMemoryStore()
	record := newReconcileTestRecordWithQuota(ports.WorkloadStateProvisioning)
	record.Kind = ports.WorkloadKindVM // non-GPU
	if err := store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	quota := &reconcileFakeQuotaService{}
	meta := &reconcileFakeMetadataStore{}
	storeTx := &reconcileFakeInstanceStoreTx{}
	controller := newQuotaEnabledController(t, store, quota, meta, storeTx,
		phaseConfigurableStatusReader{phase: "Running"},
		func() time.Time { return time.Unix(200, 0) },
		&MockOutboxWriter{})

	controller.ReconcileQuota(context.Background(), []ports.ReconcileTarget{
		{
			TenantID:   record.TenantID,
			InstanceID: record.InstanceID,
			Kind:       record.Kind,
			Provider:   record.Provider,
			State:      record.Status.State,
		},
	})
	if len(quota.confirmCalls)+len(quota.cancelCalls)+len(quota.releaseCalls) != 0 {
		t.Fatalf("quota loop must skip non-GPU workloads")
	}
}
