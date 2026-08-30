package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// fakeQuotaService records TCC calls for test assertions.
type fakeQuotaService struct {
	mu           sync.Mutex
	tryManyTxCtx context.Context
	tryManyTxReq []ports.QuotaTryRequest
	tryManyTxErr error
	cancelCtx    context.Context
	cancelTxIDs  []string
	cancelErr    error
	cancelCalls  int
}

func (f *fakeQuotaService) Try(context.Context, ports.QuotaTryRequest) (ports.QuotaReservation, error) {
	return ports.QuotaReservation{}, ports.ErrUnsupported
}

func (f *fakeQuotaService) TryMany(context.Context, []ports.QuotaTryRequest) ([]ports.QuotaReservation, error) {
	return nil, ports.ErrUnsupported
}

func (f *fakeQuotaService) TryTx(context.Context, ports.MetadataTx, ports.QuotaTryRequest) (ports.QuotaReservation, error) {
	return ports.QuotaReservation{}, ports.ErrUnsupported
}

func (f *fakeQuotaService) TryManyTx(ctx context.Context, _ ports.MetadataTx, reqs []ports.QuotaTryRequest) ([]ports.QuotaReservation, error) {
	f.mu.Lock()
	f.tryManyTxCtx = ctx
	f.tryManyTxReq = reqs
	f.mu.Unlock()
	if f.tryManyTxErr != nil {
		return nil, f.tryManyTxErr
	}
	reservations := make([]ports.QuotaReservation, len(reqs))
	for i, req := range reqs {
		reservations[i] = ports.QuotaReservation{TxID: "tx_" + req.TenantID + "_" + string(req.ResourceType)}
	}
	return reservations, nil
}

func (f *fakeQuotaService) Confirm(context.Context, ports.MetadataTx, []string, string) error {
	return nil
}

func (f *fakeQuotaService) Cancel(ctx context.Context, _ ports.MetadataTx, txIDs []string) error {
	f.mu.Lock()
	f.cancelCtx = ctx
	f.cancelTxIDs = txIDs
	f.cancelCalls++
	f.mu.Unlock()
	return f.cancelErr
}

func (f *fakeQuotaService) Release(context.Context, ports.MetadataTx, []string) error {
	return nil
}

var _ ports.QuotaService = (*fakeQuotaService)(nil)

// fakeQuotaStoreService returns a fixed QuotaView for GetMy.
type fakeQuotaStoreService struct {
	view ports.QuotaView
	err  error
}

func (f *fakeQuotaStoreService) Put(context.Context, string, ports.QuotaPutRequest) (ports.QuotaView, error) {
	return ports.QuotaView{}, ports.ErrUnsupported
}

func (f *fakeQuotaStoreService) List(context.Context, ports.QuotaListRequest) (ports.QuotaListResult, error) {
	return ports.QuotaListResult{}, ports.ErrUnsupported
}

func (f *fakeQuotaStoreService) GetMy(context.Context, string) (ports.QuotaView, error) {
	return f.view, f.err
}

func (f *fakeQuotaStoreService) GetTotalForUpdateTx(context.Context, ports.MetadataTx, string, ports.ResourceType) (int64, error) {
	return 0, ports.ErrUnsupported
}

var _ ports.QuotaStoreService = (*fakeQuotaStoreService)(nil)

// fakeQuotaAdminService returns a fixed ReservationView for GetReservationTx.
type fakeQuotaAdminService struct {
	reservation ports.ReservationView
	err         error
}

func (f *fakeQuotaAdminService) CreateTenantQuota(context.Context, string, []ports.QuotaItemInput) ([]ports.QuotaInfo, error) {
	return nil, ports.ErrUnsupported
}
func (f *fakeQuotaAdminService) UpdateTenantQuota(context.Context, string, []ports.QuotaItemUpdate) ([]ports.QuotaInfo, error) {
	return nil, ports.ErrUnsupported
}
func (f *fakeQuotaAdminService) GetTenantQuota(context.Context, string) ([]ports.QuotaInfo, error) {
	return nil, ports.ErrUnsupported
}
func (f *fakeQuotaAdminService) DeleteTenantQuota(context.Context, string) error {
	return ports.ErrUnsupported
}
func (f *fakeQuotaAdminService) ListQuotaMeta(context.Context) ([]ports.QuotaMeta, error) {
	return nil, ports.ErrUnsupported
}
func (f *fakeQuotaAdminService) UpsertTenantQuota(context.Context, string, []ports.QuotaItemInput) ([]ports.QuotaInfo, error) {
	return nil, ports.ErrUnsupported
}
func (f *fakeQuotaAdminService) PutReservation(context.Context, string, ports.ReservationPutRequest) (ports.ReservationView, error) {
	return ports.ReservationView{}, ports.ErrUnsupported
}
func (f *fakeQuotaAdminService) GetReservation(context.Context, string) (ports.ReservationView, error) {
	return f.reservation, f.err
}
func (f *fakeQuotaAdminService) GetReservationTx(context.Context, ports.MetadataTx, string) (ports.ReservationView, error) {
	return f.reservation, f.err
}

var _ ports.QuotaAdminService = (*fakeQuotaAdminService)(nil)

func TestQuotaEnabledSwitch(t *testing.T) {
	innerStore := &fakeInstanceStore{}
	inner := newTestInstanceOrchestrator(true, innerStore)
	quotaStore := &fakeQuotaStoreService{
		view: ports.QuotaView{
			TenantID: "tenant-a",
			Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 10},
			Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 2},
			Reserved: map[ports.ResourceType]int64{ports.QuotaGPUCount: 1},
		},
	}
	metadataStore := fakeMetadataStore{tx: &fakeMetadataTx{}}

	t.Run("disabled bypasses quota", func(t *testing.T) {
		innerStore.upserts = 0
		quotaSvc := &fakeQuotaService{}
		orchestrator := NewQuotaAwareInstanceOrchestrator(inner,
			WithQuotaAwareQuotaEnabled(false),
			WithQuotaAwareQuotaService(quotaSvc),
			WithQuotaAwareQuotaStore(quotaStore),
			WithQuotaAwareMetadataStore(metadataStore),
		)
		_, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
			Spec:            gpuTestSpec("tenant-a"),
			UserID:          "user-a",
			PermissionProof: "rbac:create:workload",
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		quotaSvc.mu.Lock()
		if quotaSvc.tryManyTxReq != nil {
			t.Fatalf("TryManyTx called when quota disabled")
		}
		quotaSvc.mu.Unlock()
	})

	t.Run("enabled calls TryManyTx", func(t *testing.T) {
		innerStore.upserts = 0
		quotaSvc := &fakeQuotaService{}
		orchestrator := NewQuotaAwareInstanceOrchestrator(inner,
			WithQuotaAwareQuotaEnabled(true),
			WithQuotaAwareQuotaService(quotaSvc),
			WithQuotaAwareQuotaStore(quotaStore),
			WithQuotaAwareMetadataStore(metadataStore),
		)
		_, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
			Spec:            gpuTestSpec("tenant-a"),
			UserID:          "user-a",
			PermissionProof: "rbac:create:workload",
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		quotaSvc.mu.Lock()
		if quotaSvc.tryManyTxReq == nil {
			t.Fatalf("TryManyTx not called when quota enabled")
		}
		if len(quotaSvc.tryManyTxReq) != 1 {
			t.Fatalf("TryManyTx reqs = %d, want 1", len(quotaSvc.tryManyTxReq))
		}
		if quotaSvc.tryManyTxReq[0].ResourceType != ports.QuotaGPUCount {
			t.Fatalf("ResourceType = %q, want gpu_count", quotaSvc.tryManyTxReq[0].ResourceType)
		}
		quotaSvc.mu.Unlock()
	})

	t.Run("enabled rejects on quota exceeded", func(t *testing.T) {
		innerStore.upserts = 0
		quotaSvc := &fakeQuotaService{}
		exceededStore := &fakeQuotaStoreService{
			view: ports.QuotaView{
				TenantID: "tenant-a",
				Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 3},
				Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 2},
				Reserved: map[ports.ResourceType]int64{ports.QuotaGPUCount: 1},
			},
		}
		orchestrator := NewQuotaAwareInstanceOrchestrator(inner,
			WithQuotaAwareQuotaEnabled(true),
			WithQuotaAwareQuotaService(quotaSvc),
			WithQuotaAwareQuotaStore(exceededStore),
			WithQuotaAwareMetadataStore(metadataStore),
		)
		_, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
			Spec:            gpuTestSpec("tenant-a"),
			UserID:          "user-a",
			PermissionProof: "rbac:create:workload",
		})
		if err == nil {
			t.Fatalf("Create() error = nil, want quota exceeded")
		}
	})

	t.Run("enabled rejects on reserved insufficient (gate 2)", func(t *testing.T) {
		innerStore.upserts = 0
		quotaSvc := &fakeQuotaService{}
		// Gate 1 passes (total=10, used=0, reserved=0, request=1 → 1 <= 10 ✓).
		// Gate 2 fails: allocated=0, used=0, reserved=0 → available=0 < 1.
		insufficientAdmin := &fakeQuotaAdminService{
			reservation: ports.ReservationView{
				TenantID:          "tenant-a",
				AllocatedGPUCount: 0,
				Used:              0,
				Reserved:          0,
				Available:         0,
			},
		}
		orchestrator := NewQuotaAwareInstanceOrchestrator(inner,
			WithQuotaAwareQuotaEnabled(true),
			WithQuotaAwareQuotaService(quotaSvc),
			WithQuotaAwareQuotaStore(quotaStore),
			WithQuotaAwareQuotaAdmin(insufficientAdmin),
			WithQuotaAwareMetadataStore(metadataStore),
		)
		_, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
			Spec:            gpuTestSpec("tenant-a"),
			UserID:          "user-a",
			PermissionProof: "rbac:create:workload",
		})
		if err == nil {
			t.Fatalf("Create() error = nil, want reserved insufficient")
		}
		if !errors.Is(err, ports.ErrReservedInsufficient) {
			t.Fatalf("Create() error = %v, want ErrReservedInsufficient", err)
		}
		// TryManyTx must NOT be called when Gate 2 rejects.
		quotaSvc.mu.Lock()
		if quotaSvc.tryManyTxReq != nil {
			t.Fatalf("TryManyTx called when Gate 2 rejected")
		}
		quotaSvc.mu.Unlock()
	})

	t.Run("enabled rejects on reserved insufficient with pending (oversell prevention)", func(t *testing.T) {
		innerStore.upserts = 0
		quotaSvc := &fakeQuotaService{}
		// Gate 1 passes (total=8, used=0, reserved=4, request=1 → 5 <= 8 ✓).
		// Gate 2 fails: allocated=4, used=0, reserved=4 → available=0 < 1.
		// This is the oversell scenario from plan.md §4.5: 4 pending instances
		// (reserved=4) exhaust the allocation (allocated=4).
		oversellStore := &fakeQuotaStoreService{
			view: ports.QuotaView{
				TenantID: "tenant-a",
				Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 8},
				Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 0},
				Reserved: map[ports.ResourceType]int64{ports.QuotaGPUCount: 4},
			},
		}
		oversellAdmin := &fakeQuotaAdminService{
			reservation: ports.ReservationView{
				TenantID:          "tenant-a",
				AllocatedGPUCount: 4,
				Used:              0,
				Reserved:          4,
				Available:         0,
			},
		}
		orchestrator := NewQuotaAwareInstanceOrchestrator(inner,
			WithQuotaAwareQuotaEnabled(true),
			WithQuotaAwareQuotaService(quotaSvc),
			WithQuotaAwareQuotaStore(oversellStore),
			WithQuotaAwareQuotaAdmin(oversellAdmin),
			WithQuotaAwareMetadataStore(metadataStore),
		)
		_, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
			Spec:            gpuTestSpec("tenant-a"),
			UserID:          "user-a",
			PermissionProof: "rbac:create:workload",
		})
		if err == nil {
			t.Fatalf("Create() error = nil, want reserved insufficient (oversell prevented)")
		}
		if !errors.Is(err, ports.ErrReservedInsufficient) {
			t.Fatalf("Create() error = %v, want ErrReservedInsufficient (oversell prevented)", err)
		}
	})

	t.Run("enabled passes when reservation has room", func(t *testing.T) {
		innerStore.upserts = 0
		quotaSvc := &fakeQuotaService{}
		// Gate 1 passes (total=10, used=2, reserved=1, request=1 → 4 <= 10 ✓).
		// Gate 2 passes: allocated=5, used=2, reserved=1 → available=2 >= 1 ✓.
		sufficientAdmin := &fakeQuotaAdminService{
			reservation: ports.ReservationView{
				TenantID:          "tenant-a",
				AllocatedGPUCount: 5,
				Used:              2,
				Reserved:          1,
				Available:         2,
			},
		}
		orchestrator := NewQuotaAwareInstanceOrchestrator(inner,
			WithQuotaAwareQuotaEnabled(true),
			WithQuotaAwareQuotaService(quotaSvc),
			WithQuotaAwareQuotaStore(quotaStore),
			WithQuotaAwareQuotaAdmin(sufficientAdmin),
			WithQuotaAwareMetadataStore(metadataStore),
		)
		_, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
			Spec:            gpuTestSpec("tenant-a"),
			UserID:          "user-a",
			PermissionProof: "rbac:create:workload",
		})
		if err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}
		quotaSvc.mu.Lock()
		if quotaSvc.tryManyTxReq == nil {
			t.Fatalf("TryManyTx not called when both gates pass")
		}
		quotaSvc.mu.Unlock()
	})

	t.Run("enabled cancels on Apply failure", func(t *testing.T) {
		innerStore.upserts = 0
		quotaSvc := &fakeQuotaService{}
		failingInner := &failingOrchestrator{}
		outbox := &MockOutboxWriter{}
		orchestrator := NewQuotaAwareInstanceOrchestrator(failingInner,
			WithQuotaAwareQuotaEnabled(true),
			WithQuotaAwareQuotaService(quotaSvc),
			WithQuotaAwareQuotaStore(quotaStore),
			WithQuotaAwareMetadataStore(metadataStore),
			WithQuotaAwareOutboxWriter(outbox),
		)
		_, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
			Spec:            gpuTestSpec("tenant-a"),
			UserID:          "user-a",
			PermissionProof: "rbac:create:workload",
		})
		if err == nil {
			t.Fatalf("Create() error = nil, want Apply failure")
		}
		quotaSvc.mu.Lock()
		if quotaSvc.cancelCalls == 0 {
			t.Fatalf("Cancel not called on Apply failure")
		}
		quotaSvc.mu.Unlock()
		if len(outbox.events) != 1 {
			t.Fatalf("outbox events = %d, want 1 on Apply failure", len(outbox.events))
		}
		if outbox.events[0].EventType != "instance.create_failed" {
			t.Fatalf("outbox event_type = %q, want instance.create_failed", outbox.events[0].EventType)
		}
		if outbox.events[0].TenantID != "tenant-a" {
			t.Fatalf("outbox tenant_id = %q, want tenant-a", outbox.events[0].TenantID)
		}
	})
}

// gpuTestSpec returns a valid GPU container spec for testing.
func gpuTestSpec(tenantID string) ports.WorkloadSpec {
	return ports.WorkloadSpec{
		TenantID: tenantID,
		Name:     "gpu-app",
		Kind:     ports.WorkloadKindGPUContainer,
		Image:    "harbor.example/gpu-app:1",
		Resources: ports.WorkloadResourceRequest{
			GPU: ports.GPUSchedulingRequest{
				RequiredCount: 1,
			},
		},
		GPUSpec: &ports.InstanceGPUSpecReference{
			SpecID:     "nvidia-a100-80gb",
			GPUType:    "NVIDIA-A100-80GB",
			Shares:     1,
			MBPerShare: 80640,
		},
	}
}

// failingOrchestrator implements ports.WorkloadInstanceOrchestrator and
// always returns an error from Create to simulate Apply failure.
type failingOrchestrator struct{}

func (failingOrchestrator) Create(context.Context, ports.WorkloadInstanceCreateRequest) (ports.WorkloadInstanceCreateResult, error) {
	return ports.WorkloadInstanceCreateResult{}, ports.ErrConflict
}

var _ ports.WorkloadInstanceOrchestrator = (*failingOrchestrator)(nil)
