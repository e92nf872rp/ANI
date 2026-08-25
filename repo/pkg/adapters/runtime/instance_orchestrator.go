package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type LocalInstanceOrchestrator struct {
	runtime    ports.WorkloadRuntime
	renderer   ports.WorkloadRenderer
	admission  ports.WorkloadAdmission
	audit      ports.WorkloadPlanAuditStore
	dryRun     ports.WorkloadProviderDryRun
	apply      ports.WorkloadProviderApply
	reader     ports.WorkloadProviderStatusReader
	reconciler ports.WorkloadStatusReconciler
	store      ports.WorkloadInstanceStore
	identity   ports.WorkloadIdentityService
	now        func() time.Time
	// quotaService performs TCC Cancel/Release on Apply failure and Delete.
	// nil means GPU quota is disabled and all quota calls are no-ops
	// (SPEC §5.1 GPU_QUOTA_ENABLED=false case).
	quotaService ports.QuotaService
	// metadataStore opens tenant-scoped transactions for the quota + status
	// atomic write on Apply failure. nil falls back to the non-transactional
	// store.UpsertStatus.
	metadataStore ports.MetadataStore
	// storeTx writes instance status inside an externally-owned MetadataTx
	// (same transaction as Cancel/Release on Apply failure). nil falls back
	// to the non-transactional store.UpsertStatus.
	storeTx ports.WorkloadInstanceStoreTx
	// translator converts spec_id to Volcano Pod resource requests, node
	// selector, schedulerName and queue annotation. This is a Core capability
	// that must work regardless of GPU_QUOTA_ENABLED (plan.md §4.7: "节点标签
	// 读取/规格管理是 Core 能力,不受 GPU_QUOTA_ENABLED 开关影响"). nil skips
	// translation (no GPUSpec CRD available).
	translator *VolcanoResourceTranslator
}

type InstanceOrchestratorOption func(*LocalInstanceOrchestrator)

func WithInstanceOrchestratorClock(now func() time.Time) InstanceOrchestratorOption {
	return func(orchestrator *LocalInstanceOrchestrator) {
		if now != nil {
			orchestrator.now = now
		}
	}
}

func WithInstanceStore(store ports.WorkloadInstanceStore) InstanceOrchestratorOption {
	return func(orchestrator *LocalInstanceOrchestrator) {
		orchestrator.store = store
	}
}

func WithInstanceOrchestratorWorkloadIdentityService(identity ports.WorkloadIdentityService) InstanceOrchestratorOption {
	return func(orchestrator *LocalInstanceOrchestrator) {
		orchestrator.identity = identity
	}
}

// WithInstanceOrchestratorQuotaService injects the TCC quota service used for
// Cancel on Apply failure and Release on Delete. When nil, quota calls are
// skipped (SPEC §5.1 GPU_QUOTA_ENABLED=false case).
func WithInstanceOrchestratorQuotaService(service ports.QuotaService) InstanceOrchestratorOption {
	return func(orchestrator *LocalInstanceOrchestrator) {
		orchestrator.quotaService = service
	}
}

// WithInstanceOrchestratorMetadataStore injects the tenant transaction store
// used so Cancel/Release and the status write commit atomically on Apply
// failure and Delete.
func WithInstanceOrchestratorMetadataStore(store ports.MetadataStore) InstanceOrchestratorOption {
	return func(orchestrator *LocalInstanceOrchestrator) {
		orchestrator.metadataStore = store
	}
}

// WithInstanceOrchestratorStoreTx injects the transactional status writer used
// inside the same tenant transaction as Cancel/Release on Apply failure.
func WithInstanceOrchestratorStoreTx(storeTx ports.WorkloadInstanceStoreTx) InstanceOrchestratorOption {
	return func(orchestrator *LocalInstanceOrchestrator) {
		orchestrator.storeTx = storeTx
	}
}

// WithInstanceOrchestratorTranslator injects the Volcano resource translator.
// This is a Core capability independent of GPU_QUOTA_ENABLED (plan.md §4.7).
func WithInstanceOrchestratorTranslator(translator *VolcanoResourceTranslator) InstanceOrchestratorOption {
	return func(orchestrator *LocalInstanceOrchestrator) {
		orchestrator.translator = translator
	}
}

func NewLocalInstanceOrchestrator(
	runtime ports.WorkloadRuntime,
	renderer ports.WorkloadRenderer,
	admission ports.WorkloadAdmission,
	audit ports.WorkloadPlanAuditStore,
	dryRun ports.WorkloadProviderDryRun,
	apply ports.WorkloadProviderApply,
	reader ports.WorkloadProviderStatusReader,
	reconciler ports.WorkloadStatusReconciler,
	options ...InstanceOrchestratorOption,
) *LocalInstanceOrchestrator {
	orchestrator := &LocalInstanceOrchestrator{
		runtime:    runtime,
		renderer:   renderer,
		admission:  admission,
		audit:      audit,
		dryRun:     dryRun,
		apply:      apply,
		reader:     reader,
		reconciler: reconciler,
		now:        time.Now,
	}
	for _, option := range options {
		option(orchestrator)
	}
	return orchestrator
}

func (o *LocalInstanceOrchestrator) Create(ctx context.Context, request ports.WorkloadInstanceCreateRequest) (ports.WorkloadInstanceCreateResult, error) {
	if err := o.validate(); err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	if request.UserID == "" {
		return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("%w: user id is required for instance orchestration", ports.ErrInvalid)
	}
	if request.PermissionProof == "" {
		return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("%w: permission proof is required for instance orchestration", ports.ErrInvalid)
	}

	ref, err := o.runtime.Create(ctx, request.Spec)
	if err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	current, err := o.runtime.Get(ctx, ref)
	if err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	var identity *ports.WorkloadIdentityBinding
	if o.identity != nil {
		binding, err := o.identity.BindScopedKey(ctx, ports.WorkloadIdentityBindRequest{
			TenantID:     ref.TenantID,
			InstanceID:   ref.InstanceID,
			InstanceName: request.Spec.Name,
			Kind:         ref.Kind,
			UserID:       request.UserID,
			RequestedAt:  firstNonZeroTime(request.RequestedAt, o.now().UTC()),
		})
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
		identity = &binding
		request.Spec.Identity = identity
	}
	// Volcano resource translation — Core capability, runs regardless of
	// GPU_QUOTA_ENABLED (plan.md §4.7: "节点标签读取/规格管理是 Core 能力,
	// 不受 GPU_QUOTA_ENABLED 开关影响").
	if o.translator != nil && request.Spec.GPUSpec != nil && request.Spec.GPUSpec.SpecID != "" {
		count := gpuRequestCount(request.Spec)
		queueName := annotationValue(request.Spec, gpuQueueAnnotation)
		translation, err := o.translator.Translate(ctx, request.Spec.GPUSpec.SpecID, queueName, count)
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("volcano translation for spec %q: %w", request.Spec.GPUSpec.SpecID, err)
		}
		injectVolcanoTranslation(&request.Spec, translation)
	}

	manifests, err := o.renderer.Render(ctx, request.Spec)
	if err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	admission, err := o.admission.Review(ctx, manifests)
	if err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	provider := ""
	if len(manifests) > 0 {
		provider = manifests[0].Provider
	}
	auditID, err := o.audit.RecordPlan(ctx, ports.WorkloadPlanAuditRecord{
		TenantID:        request.Spec.TenantID,
		UserID:          request.UserID,
		InstanceID:      ref.InstanceID,
		InstanceName:    request.Spec.Name,
		WorkloadKind:    request.Spec.Kind,
		Provider:        provider,
		Manifests:       manifests,
		AdmissionResult: admission,
		CreatedAt:       firstNonZeroTime(request.RequestedAt, o.now().UTC()),
	})
	if err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	dryRun, err := o.dryRun.DryRun(ctx, manifests, admission)
	if err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	apply, err := o.apply.Apply(ctx, ports.WorkloadProviderApplyRequest{
		TenantID:        request.Spec.TenantID,
		UserID:          request.UserID,
		InstanceID:      ref.InstanceID,
		AuditID:         auditID,
		PermissionProof: request.PermissionProof,
		Operation:       ports.WorkloadLifecycleCreate,
		Manifests:       manifests,
		AdmissionResult: admission,
		DryRunResult:    dryRun,
		RequestedAt:     firstNonZeroTime(request.RequestedAt, o.now().UTC()),
	})
	if err != nil {
		// SPEC §5.1 FR-28: Apply 失败保留 DB 行 UpsertStatusTx(state=failed),
		// 复用 cancelQuotaAndFinalize 同事务 Cancel 配额，方便审计。
		o.markApplyFailed(ctx, request, ref, auditID, provider, err)
		return ports.WorkloadInstanceCreateResult{}, err
	}

	result := ports.WorkloadInstanceCreateResult{
		Ref:         ref,
		AuditID:     auditID,
		Manifests:   manifests,
		Admission:   admission,
		DryRun:      dryRun,
		Apply:       apply,
		FinalStatus: current,
		Identity:    identity,
	}
	if o.store != nil {
		if err := o.store.UpsertStatus(ctx, instanceRecordFromResult(request.Spec, ref, auditID, provider, nil, current, firstNonZeroTime(request.RequestedAt, o.now().UTC()), request.QuotaTxIDs)); err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
	}
	if !apply.Applied {
		return result, nil
	}

	observation, err := o.reader.Observe(ctx, ports.WorkloadProviderStatusRequest{
		TenantID:    request.Spec.TenantID,
		InstanceID:  ref.InstanceID,
		Kind:        request.Spec.Kind,
		ApplyResult: apply,
		RequestedAt: firstNonZeroTime(request.RequestedAt, o.now().UTC()),
	})
	if err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	reconcile, err := o.reconciler.Reconcile(ctx, ports.WorkloadReconcileRequest{
		AuditID:     auditID,
		Current:     current,
		ApplyResult: apply,
		Observation: observation,
	})
	if err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}

	result.Observation = observation
	result.Reconcile = reconcile
	result.FinalStatus = reconcile.Status
	result.Orchestrated = true
	if o.store != nil {
		record := instanceRecordFromResult(request.Spec, ref, auditID, provider, apply.ResourceRefs, reconcile.Status, firstNonZeroTime(request.RequestedAt, o.now().UTC()), request.QuotaTxIDs)
		if err := o.persistWithQuotaTransition(ctx, record, current.State, reconcile.Status.State); err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
	}
	return result, nil
}

func (o *LocalInstanceOrchestrator) validate() error {
	if o.runtime == nil {
		return fmt.Errorf("%w: workload runtime is required for instance orchestration", ports.ErrNotConfigured)
	}
	if o.renderer == nil {
		return fmt.Errorf("%w: workload renderer is required for instance orchestration", ports.ErrNotConfigured)
	}
	if o.admission == nil {
		return fmt.Errorf("%w: workload admission is required for instance orchestration", ports.ErrNotConfigured)
	}
	if o.audit == nil {
		return fmt.Errorf("%w: workload plan audit is required for instance orchestration", ports.ErrNotConfigured)
	}
	if o.dryRun == nil {
		return fmt.Errorf("%w: workload provider dry-run is required for instance orchestration", ports.ErrNotConfigured)
	}
	if o.apply == nil {
		return fmt.Errorf("%w: workload provider apply is required for instance orchestration", ports.ErrNotConfigured)
	}
	if o.reader == nil {
		return fmt.Errorf("%w: workload provider status reader is required for instance orchestration", ports.ErrNotConfigured)
	}
	if o.reconciler == nil {
		return fmt.Errorf("%w: workload status reconciler is required for instance orchestration", ports.ErrNotConfigured)
	}
	return nil
}

// hasTransactionalQuotaSupport reports whether the orchestrator can perform
// transactional quota + status writes on Apply failure.
func (o *LocalInstanceOrchestrator) hasTransactionalQuotaSupport() bool {
	return o.metadataStore != nil && o.storeTx != nil
}

// persistWithQuotaTransition writes the instance status and, when the
// reconcile produced a quota-relevant transition with QuotaTxIDs present,
// performs the TCC action in the same tenant transaction (SPEC §5.1):
//   - pending/provisioning → running: Confirm (reserved → used)
//   - pending/provisioning → failed:  Cancel  (release reserved)
//
// This covers the case where the inner Create's synchronous Observe+Reconcile
// immediately observes Running or Failed (e.g. the local provider returns
// "Running" for Create, or CrashLoopBackOff/ImagePullBackOff is observed
// immediately), which would otherwise bypass the Reconciler's
// applyStateTransition and leave the quota stuck in "reserved" (SPEC §5.1).
func (o *LocalInstanceOrchestrator) persistWithQuotaTransition(ctx context.Context, record ports.WorkloadInstanceRecord, previous, next ports.WorkloadState) error {
	slog.Info("persistWithQuotaTransition",
		"instance_id", record.InstanceID,
		"previous", previous,
		"next", next,
		"quota_tx_ids", record.QuotaTxIDs,
		"has_tx_quota", o.hasTransactionalQuotaSupport(),
	)
	if !o.hasTransactionalQuotaSupport() {
		slog.Warn("persistWithQuotaTransition: no tx quota support, plain UpsertStatus",
			"instance_id", record.InstanceID,
		)
		return o.store.UpsertStatus(ctx, record)
	}
	needsConfirm := (previous == ports.WorkloadStatePending || previous == ports.WorkloadStateProvisioning) &&
		next == ports.WorkloadStateRunning &&
		len(record.QuotaTxIDs) > 0
	needsCancel := (previous == ports.WorkloadStatePending || previous == ports.WorkloadStateProvisioning) &&
		next == ports.WorkloadStateFailed &&
		len(record.QuotaTxIDs) > 0
	if !needsConfirm && !needsCancel {
		slog.Info("persistWithQuotaTransition: no TCC action, plain UpsertStatus",
			"instance_id", record.InstanceID,
			"previous", previous,
			"next", next,
			"quota_tx_ids_len", len(record.QuotaTxIDs),
		)
		return o.store.UpsertStatus(ctx, record)
	}
	action := "Confirm"
	if needsCancel {
		action = "Cancel"
	}
	slog.Info("persistWithQuotaTransition: executing TCC action in tx",
		"instance_id", record.InstanceID,
		"action", action,
		"quota_tx_ids", record.QuotaTxIDs,
	)
	return o.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
		if o.quotaService != nil {
			if needsConfirm {
				if err := o.quotaService.Confirm(txCtx, tx, record.QuotaTxIDs, record.InstanceID); err != nil {
					slog.Error("persistWithQuotaTransition: Confirm failed",
						"instance_id", record.InstanceID,
						"err", err,
					)
					return err
				}
				slog.Info("persistWithQuotaTransition: Confirm succeeded",
					"instance_id", record.InstanceID,
				)
			} else {
				if err := o.quotaService.Cancel(txCtx, tx, record.QuotaTxIDs); err != nil {
					slog.Error("persistWithQuotaTransition: Cancel failed",
						"instance_id", record.InstanceID,
						"err", err,
					)
					return err
				}
				slog.Info("persistWithQuotaTransition: Cancel succeeded",
					"instance_id", record.InstanceID,
				)
			}
		}
		return o.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

// markApplyFailed persists the instance with state=failed after an Apply
// error (SPEC §5.1 FR-28). When transactional quota support is configured
// it writes the failed status and cancels any reserved quota in the same
// tenant transaction. When quota is disabled it falls back to the
// non-transactional store.UpsertStatus. The original Apply error is
// returned to the caller; this method only records the failure for audit.
func (o *LocalInstanceOrchestrator) markApplyFailed(
	ctx context.Context,
	request ports.WorkloadInstanceCreateRequest,
	ref ports.WorkloadRef,
	auditID string,
	provider string,
	applyErr error,
) {
	if o.store == nil {
		return
	}
	now := o.now().UTC()
	failedStatus := ports.WorkloadStatus{
		Ref:       ref,
		State:     ports.WorkloadStateFailed,
		Reason:    fmt.Sprintf("apply failed: %v", applyErr),
		UpdatedAt: now,
	}
	record := instanceRecordFromResult(request.Spec, ref, auditID, provider, nil, failedStatus, now, request.QuotaTxIDs)
	if !o.hasTransactionalQuotaSupport() {
		_ = o.store.UpsertStatus(ctx, record)
		return
	}
	_ = o.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
		if o.quotaService != nil && len(record.QuotaTxIDs) > 0 {
			if cancelErr := o.quotaService.Cancel(txCtx, tx, record.QuotaTxIDs); cancelErr != nil {
				slog.Error("markApplyFailed: quota Cancel failed, reserved quota may leak",
					"instance_id", record.InstanceID,
					"quota_tx_ids", record.QuotaTxIDs,
					"apply_err", applyErr,
					"cancel_err", cancelErr,
				)
			}
		}
		return o.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

// Delete releases quota and removes the instance (SPEC §5.1 FR-18). It calls
// Quota.Release on the instance's QuotaTxIDs. When transactional support is
// configured the Release and the status write commit atomically in the same
// tenant transaction. When quota is disabled it only writes the status.
// The caller is responsible for loading the current record before calling
// Delete so the QuotaTxIDs are available.
func (o *LocalInstanceOrchestrator) Delete(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	if o.store == nil {
		return ports.ErrNotConfigured
	}
	now := o.now().UTC()
	record.Status.State = ports.WorkloadStateDeleted
	record.Status.UpdatedAt = now
	record.UpdatedAt = now
	if !o.hasTransactionalQuotaSupport() {
		return o.store.UpsertStatus(ctx, record)
	}
	return o.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
		if o.quotaService != nil && len(record.QuotaTxIDs) > 0 {
			// Cancel releases reserved (if any, idempotent); Release releases
			// used (if any, idempotent). Both are safe to call regardless of
			// the original reservation state (SPEC §5.1 delete flow).
			if err := o.quotaService.Cancel(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
			if err := o.quotaService.Release(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
		}
		return o.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

var _ ports.WorkloadInstanceOrchestrator = (*LocalInstanceOrchestrator)(nil)

func instanceRecordFromResult(spec ports.WorkloadSpec, ref ports.WorkloadRef, auditID string, provider string, resourceRefs []string, status ports.WorkloadStatus, createdAt time.Time, quotaTxIDs []string) ports.WorkloadInstanceRecord {
	status.Ref = ref
	return ports.WorkloadInstanceRecord{
		TenantID:           spec.TenantID,
		InstanceID:         ref.InstanceID,
		Name:               spec.Name,
		Description:        spec.Description,
		Labels:             cloneInstanceLabels(spec.Labels),
		Kind:               spec.Kind,
		Provider:           provider,
		AuditID:            auditID,
		Image:              instanceImageSummary(spec),
		Compute:            instanceComputeSummary(spec, status),
		Network:            instanceNetworkSummary(spec, status),
		Access:             instanceAccessSummary(spec.Kind, status.State),
		StorageAttachments: instanceStorageAttachments(spec, status),
		Lifecycle:          spec.Lifecycle,
		SSH:                sshConnectionInfo(spec, ref, status),
		Container:          containerStatusInfo(spec, status, createdAt),
		GPU:                gpuStatusInfo(spec, status),
		Identity:           workloadIdentitySummary(spec.Identity),
		ResourceRefs:       append([]string(nil), resourceRefs...),
		QuotaTxIDs:         append([]string(nil), quotaTxIDs...),
		Status:             status,
		CreatedAt:          createdAt,
		UpdatedAt:          firstNonZeroTime(status.UpdatedAt, createdAt),
	}
}

func cloneInstanceLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
}

func instanceImageSummary(spec ports.WorkloadSpec) ports.InstanceImageSummary {
	summary := spec.ImageSummary
	summary.ID = firstNonEmpty(summary.ID, spec.ImageID)
	summary.Ref = firstNonEmpty(summary.Ref, spec.ImageRef, spec.Image)
	return summary
}

func instanceComputeSummary(spec ports.WorkloadSpec, status ports.WorkloadStatus) ports.InstanceComputeSummary {
	summary := ports.InstanceComputeSummary{
		CPU:      spec.Resources.CPU,
		Memory:   spec.Resources.Memory,
		NodeName: status.NodeName,
	}
	if spec.GPUSpec != nil {
		summary.SpecID = spec.GPUSpec.SpecID
		summary.GPUType = spec.GPUSpec.GPUType
		summary.GPUShares = spec.GPUSpec.Shares
		summary.GPUMBPerShare = spec.GPUSpec.MBPerShare
	}
	return summary
}

func instanceNetworkSummary(spec ports.WorkloadSpec, status ports.WorkloadStatus) ports.InstanceNetworkSummary {
	summary := ports.InstanceNetworkSummary{
		VPCID:     spec.Network.VPCID,
		SubnetID:  spec.Network.SubnetID,
		PrivateIP: firstNonEmpty(spec.Network.PrivateIP, primaryIPAddress(status.Networks)),
	}
	for _, id := range spec.Network.SecurityGroupIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			summary.SecurityGroups = append(summary.SecurityGroups, ports.InstanceSecurityGroupSummary{ID: trimmed})
		}
	}
	if endpoint := strings.TrimSpace(status.Endpoint); endpoint != "" {
		summary.Endpoints = []ports.InstanceEndpointSummary{{Address: endpoint}}
	}
	return summary
}

func instanceAccessSummary(kind ports.WorkloadKind, state ports.WorkloadState) ports.InstanceAccessSummary {
	available := state == ports.WorkloadStateRunning
	summary := ports.InstanceAccessSummary{
		SSHAvailable:     available && kind == ports.WorkloadKindVM,
		ConsoleAvailable: available && kind == ports.WorkloadKindVM,
		ExecAvailable: available && (kind == ports.WorkloadKindContainer ||
			kind == ports.WorkloadKindGPUContainer ||
			kind == ports.WorkloadKindSandbox),
	}
	if !available {
		summary.Reason = "instance state does not allow interactive access"
	}
	return summary
}

func instanceStorageAttachments(spec ports.WorkloadSpec, status ports.WorkloadStatus) []ports.WorkloadStorageAttachment {
	source := status.Storage
	if len(source) == 0 {
		source = spec.Storage
	}
	if len(source) == 0 {
		return nil
	}
	attachments := make([]ports.WorkloadStorageAttachment, 0, len(source))
	for _, item := range source {
		if item.ResourceType == "" {
			switch item.Kind {
			case ports.StorageAttachmentSharedPVC, ports.StorageAttachmentObjectFuse:
				item.ResourceType = "filesystem"
			default:
				item.ResourceType = "volume"
			}
		}
		item.ResourceID = firstNonEmpty(item.ResourceID, item.SourceRef, item.Name)
		if item.Status == "" {
			if item.ResourceType == "filesystem" || strings.TrimSpace(item.MountPath) != "" {
				item.Status = "mounted"
			} else {
				item.Status = "attached"
			}
		}
		attachments = append(attachments, item)
	}
	return attachments
}

func workloadIdentitySummary(identity *ports.WorkloadIdentityBinding) *ports.WorkloadIdentityBinding {
	if identity == nil {
		return nil
	}
	summary := *identity
	summary.KeyValue = ""
	summary.Scopes = append([]string(nil), identity.Scopes...)
	return &summary
}

func sshConnectionInfo(spec ports.WorkloadSpec, ref ports.WorkloadRef, status ports.WorkloadStatus) *ports.VMSSHConnectionInfo {
	if spec.Kind != ports.WorkloadKindVM {
		return nil
	}
	username := "ubuntu"
	keyRef := ""
	if spec.VM != nil {
		username = firstNonEmpty(spec.VM.SSHUsername, username)
		keyRef = spec.VM.SSHKeySecret
	}
	host := firstNonEmpty(primaryIPAddress(status.Networks), publicEndpointHost(status.Endpoint), ref.InstanceID+".vm.ani.internal")
	return &ports.VMSSHConnectionInfo{
		Username: username,
		Host:     host,
		Port:     22,
		KeyRef:   keyRef,
		Ready:    status.State == ports.WorkloadStateRunning || status.State == ports.WorkloadStateProvisioning,
		Reason:   "ssh connection metadata is generated by the active workload profile; private keys are never returned",
	}
}

func containerStatusInfo(spec ports.WorkloadSpec, status ports.WorkloadStatus, createdAt time.Time) *ports.ContainerInstanceStatus {
	if spec.Kind != ports.WorkloadKindContainer && spec.Kind != ports.WorkloadKindGPUContainer {
		return nil
	}
	replicas := int32(1)
	if spec.Container != nil && spec.Container.Replicas > 0 {
		replicas = spec.Container.Replicas
	}
	readyReplicas := int32(0)
	if status.State == ports.WorkloadStateRunning {
		readyReplicas = replicas
	}
	revision := containerRevision(spec)
	return &ports.ContainerInstanceStatus{
		Replicas:      replicas,
		ReadyReplicas: readyReplicas,
		Revision:      revision,
		RolloutStatus: containerRolloutStatus(status.State),
		History: []ports.ContainerRevisionHistory{
			{
				Revision:  revision,
				Image:     spec.Image,
				CreatedAt: firstNonZeroTime(createdAt, status.UpdatedAt, time.Now().UTC()).UTC(),
			},
		},
	}
}

func containerRolloutStatus(state ports.WorkloadState) string {
	switch state {
	case ports.WorkloadStateRunning:
		return "healthy"
	case ports.WorkloadStateProvisioning, ports.WorkloadStatePending, ports.WorkloadStateStarting:
		return "progressing"
	case ports.WorkloadStateFailed:
		return "degraded"
	default:
		return "pending"
	}
}

func containerRevision(spec ports.WorkloadSpec) string {
	seed := firstNonEmpty(spec.Image, spec.Name, string(spec.Kind))
	replacer := strings.NewReplacer("/", "-", ":", "-", "@", "-", ".", "-", "_", "-")
	seed = strings.Trim(replacer.Replace(strings.ToLower(seed)), "-")
	if seed == "" {
		seed = "local"
	}
	if len(seed) > 48 {
		seed = seed[:48]
	}
	return "rev-" + seed
}

func gpuStatusInfo(spec ports.WorkloadSpec, status ports.WorkloadStatus) *ports.GPUInstanceStatus {
	if spec.Kind != ports.WorkloadKindGPUContainer {
		return nil
	}
	count := spec.Resources.GPU.RequiredCount
	if count <= 0 {
		count = 1
	}
	result := &ports.GPUInstanceStatus{
		Vendor:             firstGPUVendor(spec.Resources.GPU.PreferredVendors),
		Model:              resolvedGPUModel(spec),
		Count:              count,
		SchedulingState:    gpuSchedulingState(status),
		SchedulingReason:   gpuSchedulingReason(spec),
		UtilizationPercent: gpuUtilizationPercent(status.State),
		ResourceName:       annotationValue(spec, gpuResourceNameAnnotation),
		QueueName:          annotationValue(spec, gpuQueueAnnotation),
	}
	if spec.GPUSpec != nil {
		result.SpecID = spec.GPUSpec.SpecID
		result.GPUType = spec.GPUSpec.GPUType
		result.Shares = spec.GPUSpec.Shares
		result.MBPerShare = spec.GPUSpec.MBPerShare
	}
	return result
}

func gpuSchedulingState(status ports.WorkloadStatus) string {
	switch status.State {
	case ports.WorkloadStateRunning:
		return "running"
	case ports.WorkloadStateProvisioning, ports.WorkloadStateStarting:
		if strings.TrimSpace(status.NodeName) != "" {
			return "scheduled"
		}
		return "pending"
	case ports.WorkloadStateFailed:
		return "failed"
	default:
		return "pending"
	}
}

// gpuResourceNameAnnotation and gpuQueueAnnotation carry the scheduling
// decision's chosen resource name (e.g. nvidia.com/gpu / nvidia.com/vgpu) and
// the Volcano/HAMi queue name. They are written by the planning runtime in
// planning.go and read here so the API can surface the real allocation mode.
const (
	gpuResourceNameAnnotation = "ani.kubercloud.io/gpu-resource-name"
	gpuQueueAnnotation        = "ani.kubercloud.io/gpu-queue"
)

// annotationValue returns the trimmed value of the annotation key, or "" when
// the annotation is absent.
func annotationValue(spec ports.WorkloadSpec, key string) string {
	if spec.Annotations == nil {
		return ""
	}
	return strings.TrimSpace(spec.Annotations[key])
}

// gpuSelectedModelAnnotation is the annotation key written by the planning
// runtime when PlanScheduling selects a concrete GPU node. It carries the
// real GPU model of the chosen node (e.g. "RTX4090") as opposed to the
// PreferredModels in the request which is only a scheduling preference.
const gpuSelectedModelAnnotation = "ani.kubercloud.io/gpu-selected-model"

// resolvedGPUModel returns the real GPU model the workload is scheduled onto,
// falling back to the requested PreferredModels and finally "unspecified".
func resolvedGPUModel(spec ports.WorkloadSpec) string {
	if spec.Annotations != nil {
		if selected := strings.TrimSpace(spec.Annotations[gpuSelectedModelAnnotation]); selected != "" {
			return selected
		}
	}
	return firstNonEmpty(firstString(spec.Resources.GPU.PreferredModels), "unspecified")
}

func firstGPUVendor(vendors []ports.GPUVendor) ports.GPUVendor {
	if len(vendors) == 0 || vendors[0] == "" {
		return ports.GPUVendorUnknown
	}
	return vendors[0]
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func gpuSchedulingReason(spec ports.WorkloadSpec) string {
	vendor := string(firstGPUVendor(spec.Resources.GPU.PreferredVendors))
	model := resolvedGPUModel(spec)
	if model == "" {
		model = "any"
	}
	pool := firstNonEmpty(spec.Resources.GPU.Pool, "local-profile")
	count := spec.Resources.GPU.RequiredCount
	if count <= 0 {
		count = 1
	}
	return fmt.Sprintf("scheduled %d %s/%s GPU(s) through %s", count, vendor, model, pool)
}

func gpuUtilizationPercent(state ports.WorkloadState) float64 {
	if state == ports.WorkloadStateRunning {
		return 0
	}
	return 0
}

func primaryIPAddress(networks []ports.WorkloadNetworkAttachment) string {
	for _, network := range networks {
		if network.Primary && strings.TrimSpace(network.IPAddress) != "" {
			return network.IPAddress
		}
	}
	for _, network := range networks {
		if strings.TrimSpace(network.IPAddress) != "" {
			return network.IPAddress
		}
	}
	return ""
}

func publicEndpointHost(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || strings.HasPrefix(endpoint, "/") {
		return ""
	}
	return endpoint
}
