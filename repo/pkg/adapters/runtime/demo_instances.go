package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// QuotaAwareInstanceOrchestrator wraps a WorkloadInstanceOrchestrator with
// TCC quota pre-deduction (TryManyTx), Volcano resource translation, and
// Apply exception Cancel (SPEC §5.1 three-gate check).
//
// When GPU_QUOTA_ENABLED=false (quotaEnabled=false), all quota operations
// are bypassed and Create delegates directly to the inner orchestrator
// (SPEC §5.1 GPU_QUOTA_ENABLED=false case).
//
// When GPU_QUOTA_ENABLED=true (quotaEnabled=true) and the request carries a
// GPUSpec, the wrapper:
//  1. Gate 1 — quota cap check (used + reserved + request <= total).
//  2. Volcano resource translation (spec_id → Pod spec fragments).
//  3. Gate 3 — TryManyTx atomic pre-deduct in a tenant transaction.
//  4. Injects QuotaTxIDs into the request so the inner orchestrator
//     persists them on every UpsertStatus call.
//  5. Delegates to the inner orchestrator (Apply + Observe + Reconcile).
//  6. On Apply failure — Cancel the reserved quota.
type QuotaAwareInstanceOrchestrator struct {
	inner         ports.WorkloadInstanceOrchestrator
	quotaEnabled  bool
	quotaService  ports.QuotaService
	quotaStore    ports.QuotaStoreService
	quotaAdmin    ports.QuotaAdminService
	metadataStore ports.MetadataStore
	storeTx       ports.WorkloadInstanceStoreTx
	store         ports.WorkloadInstanceStore
	translator    *VolcanoResourceTranslator
	outboxWriter  OutboxWriter
	now           func() time.Time
}

// QuotaAwareInstanceOrchestratorOption configures the wrapper.
type QuotaAwareInstanceOrchestratorOption func(*QuotaAwareInstanceOrchestrator)

// WithQuotaAwareQuotaEnabled toggles the GPU_QUOTA_ENABLED switch. When false,
// all quota calls are bypassed (SPEC §5.1).
func WithQuotaAwareQuotaEnabled(enabled bool) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		o.quotaEnabled = enabled
	}
}

// WithQuotaAwareQuotaService injects the TCC quota service used for TryManyTx
// pre-deduction and Cancel on Apply failure.
func WithQuotaAwareQuotaService(service ports.QuotaService) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		o.quotaService = service
	}
}

// WithQuotaAwareQuotaStore injects the quota store used for the gate 1 quota
// cap pre-check (GetMy).
func WithQuotaAwareQuotaStore(store ports.QuotaStoreService) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		o.quotaStore = store
	}
}

// WithQuotaAwareQuotaAdmin injects the quota admin service used for the gate 2
// reservation check (GetReservationTx). plan.md §6.3.1 闸 2: lock
// resource_reservation_allocations FOR UPDATE and verify
// allocated_gpu_count - used - reserved >= request.
func WithQuotaAwareQuotaAdmin(admin ports.QuotaAdminService) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		o.quotaAdmin = admin
	}
}

// WithQuotaAwareMetadataStore injects the tenant transaction store used so
// TryManyTx and the Cancel on Apply failure commit atomically.
func WithQuotaAwareMetadataStore(store ports.MetadataStore) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		o.metadataStore = store
	}
}

// WithQuotaAwareStoreTx injects the transactional status writer used to
// persist QuotaTxIDs on the instance record after a successful Create.
func WithQuotaAwareStoreTx(storeTx ports.WorkloadInstanceStoreTx) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		o.storeTx = storeTx
	}
}

// WithQuotaAwareStore injects the non-transactional status writer used to
// persist QuotaTxIDs when transactional support is not configured.
func WithQuotaAwareStore(store ports.WorkloadInstanceStore) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		o.store = store
	}
}

// WithQuotaAwareTranslator injects the Volcano resource translator used for
// spec_id → Pod spec fragment translation (SPEC §5.1).
func WithQuotaAwareTranslator(translator *VolcanoResourceTranslator) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		o.translator = translator
	}
}

// WithQuotaAwareOutboxWriter injects the outbox event writer used to emit
// instance.create_failed events on Apply failure (plan.md §6.3.1). When nil,
// outbox events are skipped.
func WithQuotaAwareOutboxWriter(w OutboxWriter) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		o.outboxWriter = w
	}
}

// WithQuotaAwareClock overrides the clock used for timestamps.
func WithQuotaAwareClock(now func() time.Time) QuotaAwareInstanceOrchestratorOption {
	return func(o *QuotaAwareInstanceOrchestrator) {
		if now != nil {
			o.now = now
		}
	}
}

// NewQuotaAwareInstanceOrchestrator wraps the inner orchestrator with TCC
// quota pre-deduction and Volcano translation.
func NewQuotaAwareInstanceOrchestrator(
	inner ports.WorkloadInstanceOrchestrator,
	options ...QuotaAwareInstanceOrchestratorOption,
) *QuotaAwareInstanceOrchestrator {
	orchestrator := &QuotaAwareInstanceOrchestrator{
		inner: inner,
		now:   time.Now,
	}
	for _, option := range options {
		option(orchestrator)
	}
	return orchestrator
}

// Create implements ports.WorkloadInstanceOrchestrator. When quota is
// disabled or the request has no GPUSpec, it delegates directly to the inner
// orchestrator. Otherwise it performs the three-gate check, Volcano
// translation, TryManyTx pre-deduction, and Apply exception Cancel.
func (o *QuotaAwareInstanceOrchestrator) Create(ctx context.Context, request ports.WorkloadInstanceCreateRequest) (ports.WorkloadInstanceCreateResult, error) {
	if !o.quotaEnabled || request.Spec.GPUSpec == nil {
		return o.inner.Create(ctx, request)
	}
	specID := request.Spec.GPUSpec.SpecID
	if specID == "" {
		return o.inner.Create(ctx, request)
	}
	count := gpuRequestCount(request.Spec)
	queueName := annotationValue(request.Spec, gpuQueueAnnotation)

	// Gate 1 — quota cap check (SPEC §5.1, plan.md §6.3.1 闸 1).
	// Fast-fail before opening a transaction: used + reserved + request <= total.
	if o.quotaStore != nil {
		quota, err := o.quotaStore.GetMy(ctx, request.Spec.TenantID)
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
		gpuTotal := quota.Total[ports.QuotaGPUCount]
		gpuUsed := quota.Used[ports.QuotaGPUCount]
		gpuReserved := quota.Reserved[ports.QuotaGPUCount]
		if gpuUsed+gpuReserved+int64(count) > gpuTotal {
			return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("%w: gpu quota exceeded (used=%d reserved=%d request=%d total=%d)",
				ports.ErrQuotaExceeded, gpuUsed, gpuReserved, count, gpuTotal)
		}
	}

	// Volcano resource translation (SPEC §5.1).
	if o.translator != nil {
		translation, err := o.translator.Translate(ctx, specID, queueName, count)
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("volcano translation for spec %q: %w", specID, err)
		}
		injectVolcanoTranslation(&request.Spec, translation)
	}

	// Gate 2 + Gate 3 — reservation check + TCC atomic pre-deduct
	// (plan.md §6.3.1 闸 2 + 闸 3, same WithTenantTx for serialised locking).
	//
	// Gate 2 locks resource_reservation_allocations FOR UPDATE and verifies
	// allocated_gpu_count - used - reserved >= request. Must subtract reserved
	// (pending instances occupy reservation) to prevent oversell.
	// Gate 3 runs TryManyTx in the same transaction (only checks <= total;
	// allocated isolation is enforced by Gate 2 at the application layer).
	var txIDs []string
	if o.quotaService != nil && o.metadataStore != nil {
		err := o.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
			// Gate 2 — reservation check (plan.md §6.3.1 闸 2).
			if o.quotaAdmin != nil {
				reservation, err := o.quotaAdmin.GetReservationTx(txCtx, tx, request.Spec.TenantID)
				if err != nil {
					return err
				}
				available := reservation.AllocatedGPUCount - reservation.Used - reservation.Reserved
				if available < int64(count) {
					return fmt.Errorf("%w: reserved insufficient (allocated=%d used=%d reserved=%d request=%d available=%d)",
						ports.ErrReservedInsufficient,
						reservation.AllocatedGPUCount, reservation.Used, reservation.Reserved,
						count, available)
				}
			}

			// Gate 3 — TCC TryManyTx atomic pre-deduct (SPEC §5.1).
			reservations, err := o.quotaService.TryManyTx(txCtx, tx, []ports.QuotaTryRequest{{
				TenantID:     request.Spec.TenantID,
				ResourceType: ports.QuotaGPUCount,
				Amount:       int64(count),
			}})
			if err != nil {
				return err
			}
			txIDs = reservationTxIDs(reservations)
			return nil
		})
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
	}

	// Inject QuotaTxIDs into the request so the inner orchestrator persists
	// them on every UpsertStatus call. This ensures the reconciler can
	// Confirm/Cancel/Release even if the status transitions synchronously
	// during Create (e.g. the inner Reconcile observes Running immediately)
	// or the async reconciler loop fires before a separate persist step.
	request.QuotaTxIDs = txIDs

	// Delegate to inner orchestrator (Apply + Observe + Reconcile).
	result, err := o.inner.Create(ctx, request)
	if err != nil {
		// Apply exception — Cancel reserved quota and emit outbox event
		// (SPEC §5.1 FR-28, plan.md §6.3.1 cancelQuotaAndFinalize).
		createErr := err
		if len(txIDs) > 0 && o.quotaService != nil && o.metadataStore != nil {
			if txErr := o.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
				if cancelErr := o.quotaService.Cancel(txCtx, tx, txIDs); cancelErr != nil {
					slog.Error("Apply failure: quota Cancel failed, reserved quota may leak",
						"idempotency_key", request.IdempotencyKey,
						"quota_tx_ids", txIDs,
						"create_err", createErr,
						"cancel_err", cancelErr,
					)
					return cancelErr
				}
				if o.outboxWriter != nil {
					payload, _ := encodeOutboxPayload(map[string]any{
						"name":  request.Spec.Name,
						"kind":  string(request.Spec.Kind),
						"error": createErr.Error(),
					})
					_ = o.outboxWriter.WriteTx(txCtx, tx, OutboxEvent{
						AggregateType: "workload_instance",
						AggregateID:   extractUUIDFromInstanceID(request.IdempotencyKey),
						EventType:     "instance.create_failed",
						TenantID:      request.Spec.TenantID,
						Payload:       payload,
					})
				}
				return nil
			}); txErr != nil {
				slog.Error("Apply failure: Cancel+outbox transaction rolled back",
					"idempotency_key", request.IdempotencyKey,
					"quota_tx_ids", txIDs,
					"create_err", createErr,
					"tx_err", txErr,
				)
			}
		}
		return ports.WorkloadInstanceCreateResult{}, err
	}

	return result, nil
}

// gpuRequestCount returns the GPU count from the spec, defaulting to 1.
func gpuRequestCount(spec ports.WorkloadSpec) int {
	if spec.Resources.GPU.RequiredCount > 0 {
		return spec.Resources.GPU.RequiredCount
	}
	return 1
}

// reservationTxIDs extracts the TxID strings from a slice of reservations.
func reservationTxIDs(reservations []ports.QuotaReservation) []string {
	if len(reservations) == 0 {
		return nil
	}
	ids := make([]string, len(reservations))
	for i, r := range reservations {
		ids[i] = r.TxID
	}
	return ids
}

// Volcano annotation keys for passing nodeSelector and resource requests from
// the translator/planner to the renderer via spec.Annotations. The values are
// JSON-encoded maps to avoid K8s annotation key restrictions (a key may contain
// at most one '/').
const (
	volcanoNodeSelectorAnnotation    = "ani.kubercloud.io/volcano-node-selector"
	volcanoResourceRequestAnnotation = "ani.kubercloud.io/volcano-resource-requests"
)

// injectVolcanoTranslation merges the Volcano translation result into the
// workload spec's annotations so the renderer can pick up the scheduler
// name, queue annotation, nodeSelector and resource requests.
func injectVolcanoTranslation(spec *ports.WorkloadSpec, translation VolcanoTranslationResult) {
	if spec.Annotations == nil {
		spec.Annotations = make(map[string]string)
	}
	for key, value := range translation.Annotations {
		spec.Annotations[key] = value
	}
	if translation.SchedulerName != "" {
		spec.SchedulerName = translation.SchedulerName
		spec.Annotations["ani.kubercloud.io/scheduler-name"] = translation.SchedulerName
	}
	if len(translation.NodeSelector) > 0 {
		data, _ := json.Marshal(translation.NodeSelector)
		spec.Annotations[volcanoNodeSelectorAnnotation] = string(data)
	}
	if len(translation.ResourceRequests) > 0 {
		data, _ := json.Marshal(translation.ResourceRequests)
		spec.Annotations[volcanoResourceRequestAnnotation] = string(data)
	}
}

var _ ports.WorkloadInstanceOrchestrator = (*QuotaAwareInstanceOrchestrator)(nil)
