package runtime

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
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
	renderSpec := request.Spec
	renderSpec.Name = providerWorkloadResourceName(request.Spec.Kind, request.Spec.Name, ref.InstanceID)
	recordSpec := request.Spec
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
		renderSpec.Identity = identity
		recordSpec.Identity = identity
	}
	manifests, err := o.renderer.Render(ctx, renderSpec)
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
		if err := o.store.UpsertStatus(ctx, instanceRecordFromResult(recordSpec, ref, auditID, provider, nil, current, firstNonZeroTime(request.RequestedAt, o.now().UTC()))); err != nil {
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
		if err := o.store.UpsertStatus(ctx, instanceRecordFromResult(recordSpec, ref, auditID, provider, apply.ResourceRefs, reconcile.Status, firstNonZeroTime(request.RequestedAt, o.now().UTC()))); err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
	}
	return result, nil
}

func providerWorkloadResourceName(kind ports.WorkloadKind, displayName string, instanceID string) string {
	prefix := providerWorkloadKindPrefix(kind)
	base := kubernetesDNSLabelPart(displayName)
	if base == "" {
		base = "instance"
	}
	sum := sha1.Sum([]byte(strings.TrimSpace(instanceID)))
	suffix := hex.EncodeToString(sum[:])[:8]
	name := prefix + "-" + base + "-" + suffix
	if len(name) <= 63 {
		return name
	}
	over := len(name) - 63
	if over >= len(base) {
		base = "instance"
	} else {
		base = strings.TrimRight(base[:len(base)-over], "-")
	}
	if base == "" {
		base = "instance"
	}
	return prefix + "-" + base + "-" + suffix
}

func providerWorkloadKindPrefix(kind ports.WorkloadKind) string {
	switch kind {
	case ports.WorkloadKindVM:
		return "vm"
	case ports.WorkloadKindContainer:
		return "container"
	case ports.WorkloadKindGPUContainer:
		return "gpu-container"
	case ports.WorkloadKindSandbox:
		return "sandbox"
	default:
		return "workload"
	}
}

func kubernetesDNSLabelPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var sanitized strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			sanitized.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			sanitized.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(sanitized.String(), "-")
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

var _ ports.WorkloadInstanceOrchestrator = (*LocalInstanceOrchestrator)(nil)

func instanceRecordFromResult(spec ports.WorkloadSpec, ref ports.WorkloadRef, auditID string, provider string, resourceRefs []string, status ports.WorkloadStatus, createdAt time.Time) ports.WorkloadInstanceRecord {
	status.Ref = ref
	vpcID, subnetID, privateIP := selectedNetworkFields(spec.Network.Attachments)
	return ports.WorkloadInstanceRecord{
		TenantID:     spec.TenantID,
		InstanceID:   ref.InstanceID,
		Name:         spec.Name,
		Kind:         spec.Kind,
		VPCID:        vpcID,
		SubnetID:     subnetID,
		PrivateIP:    privateIP,
		Provider:     provider,
		AuditID:      auditID,
		Lifecycle:    spec.Lifecycle,
		SSH:          sshConnectionInfo(spec, ref, status),
		Container:    containerStatusInfo(spec, status, createdAt),
		GPU:          gpuStatusInfo(spec, status),
		Sandbox:      sandboxStatusInfo(spec, ref, provider, status, createdAt),
		Identity:     workloadIdentitySummary(spec.Identity),
		ResourceRefs: append([]string(nil), resourceRefs...),
		Status:       status,
		CreatedAt:    createdAt,
		UpdatedAt:    firstNonZeroTime(status.UpdatedAt, createdAt),
	}
}

func sandboxStatusInfo(spec ports.WorkloadSpec, ref ports.WorkloadRef, provider string, status ports.WorkloadStatus, createdAt time.Time) *ports.SandboxInstanceStatus {
	if spec.Kind != ports.WorkloadKindSandbox {
		return nil
	}
	config := firstNonNilSandboxConfig(spec.Sandbox)
	if config.RuntimeClass == "" {
		config.RuntimeClass = spec.RuntimeClassName
	}
	config = normalizeSandboxConfig(config)
	return &ports.SandboxInstanceStatus{
		TenantID:   ref.TenantID,
		InstanceID: ref.InstanceID,
		Name:       spec.Name,
		Kind:       ports.WorkloadKindSandbox,
		Provider:   provider,
		State:      sandboxStateFromWorkloadState(status.State),
		Config:     config,
		DevProfile: ports.DevProfileInfo{
			Mode:         "real",
			Provider:     provider,
			RealProvider: provider == "kubernetes" || provider == "kubevirt",
			Reason:       "sandbox materialized through workload provider orchestration",
		},
		CreatedAt: createdAt,
		UpdatedAt: firstNonZeroTime(status.UpdatedAt, createdAt),
	}
}

func sandboxStateFromWorkloadState(state ports.WorkloadState) ports.SandboxState {
	switch state {
	case ports.WorkloadStateRunning:
		return ports.SandboxStateRunning
	case ports.WorkloadStateStopped, ports.WorkloadStateDeleted:
		return ports.SandboxStateStopped
	default:
		return ports.SandboxStatePending
	}
}

func selectedNetworkFields(attachments []ports.WorkloadNetworkAttachment) (string, string, string) {
	for _, attachment := range attachments {
		if attachment.Primary && attachment.Plane == ports.NetworkPlaneTenantVPC && strings.TrimSpace(attachment.SubnetID) != "" {
			return strings.TrimSpace(attachment.NetworkID), strings.TrimSpace(attachment.SubnetID), strings.TrimSpace(attachment.IPAddress)
		}
	}
	return "", "", ""
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
	return &ports.GPUInstanceStatus{
		Vendor:             firstGPUVendor(spec.Resources.GPU.PreferredVendors),
		Model:              firstNonEmpty(firstString(spec.Resources.GPU.PreferredModels), "unspecified"),
		Count:              count,
		SchedulingReason:   gpuSchedulingReason(spec),
		UtilizationPercent: gpuUtilizationPercent(status.State),
	}
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
	model := firstNonEmpty(firstString(spec.Resources.GPU.PreferredModels), "any")
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
