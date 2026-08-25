package ports

import (
	"context"
	"time"
)

type WorkloadKind string

const (
	WorkloadKindVM           WorkloadKind = "vm"
	WorkloadKindContainer    WorkloadKind = "container"
	WorkloadKindGPUContainer WorkloadKind = "gpu_container"
	WorkloadKindInference    WorkloadKind = "inference"
	WorkloadKindNotebook     WorkloadKind = "notebook"
	WorkloadKindSandbox      WorkloadKind = "sandbox"
	WorkloadKindAgentSandbox WorkloadKind = WorkloadKindSandbox
	WorkloadKindBatchJob     WorkloadKind = "batch_job"
)

type WorkloadState string

const (
	WorkloadStatePending      WorkloadState = "pending"
	WorkloadStateProvisioning WorkloadState = "provisioning"
	WorkloadStateRunning      WorkloadState = "running"
	WorkloadStateStarting     WorkloadState = "starting"
	WorkloadStateStopping     WorkloadState = "stopping"
	WorkloadStateStopped      WorkloadState = "stopped"
	WorkloadStateFailed       WorkloadState = "failed"
	WorkloadStateDeleting     WorkloadState = "deleting"
	WorkloadStateDeleted      WorkloadState = "deleted"
)

type WorkloadLifecycleAction string

const (
	WorkloadLifecycleCreate                   WorkloadLifecycleAction = "create"
	WorkloadLifecycleStart                    WorkloadLifecycleAction = "start"
	WorkloadLifecycleStop                     WorkloadLifecycleAction = "stop"
	WorkloadLifecycleRestart                  WorkloadLifecycleAction = "restart"
	WorkloadLifecycleResize                   WorkloadLifecycleAction = "resize"
	WorkloadLifecycleRebuild                  WorkloadLifecycleAction = "rebuild"
	WorkloadLifecycleDelete                   WorkloadLifecycleAction = "delete"
	WorkloadLifecycleSnapshot                 WorkloadLifecycleAction = "snapshot"
	WorkloadLifecycleAttachVolume             WorkloadLifecycleAction = "attach_volume"
	WorkloadLifecycleDetachVolume             WorkloadLifecycleAction = "detach_volume"
	WorkloadLifecycleAttachFilesystem         WorkloadLifecycleAction = "attach_filesystem"
	WorkloadLifecycleDetachFilesystem         WorkloadLifecycleAction = "detach_filesystem"
	WorkloadLifecycleRollback                 WorkloadLifecycleAction = "rollback"
	WorkloadLifecycleScale                    WorkloadLifecycleAction = "scale"
	WorkloadLifecycleUpdateImage              WorkloadLifecycleAction = "update_image"
	WorkloadLifecycleBindSecret               WorkloadLifecycleAction = "bind_secret"
	WorkloadLifecycleUnbindSecret             WorkloadLifecycleAction = "unbind_secret"
	WorkloadLifecycleChangeSecurityGroups     WorkloadLifecycleAction = "change_security_groups"
	WorkloadLifecycleSetTerminationProtection WorkloadLifecycleAction = "set_termination_protection"
	WorkloadLifecyclePause                    WorkloadLifecycleAction = "pause"
	WorkloadLifecycleResume                   WorkloadLifecycleAction = "resume"
	WorkloadLifecycleExtend                   WorkloadLifecycleAction = "extend"
	WorkloadLifecycleTouchIdle                WorkloadLifecycleAction = "touch_idle"
	WorkloadLifecycleConsoleSession           WorkloadLifecycleAction = "console_session"
)

type WorkloadOperationStatus string

const (
	WorkloadOperationAccepted   WorkloadOperationStatus = "accepted"
	WorkloadOperationInProgress WorkloadOperationStatus = "in_progress"
	WorkloadOperationSucceeded  WorkloadOperationStatus = "succeeded"
	WorkloadOperationFailed     WorkloadOperationStatus = "failed"
	WorkloadOperationCancelled  WorkloadOperationStatus = "cancelled"
)

type WorkloadOperationStepStatus string

const (
	WorkloadOperationStepPending   WorkloadOperationStepStatus = "pending"
	WorkloadOperationStepRunning   WorkloadOperationStepStatus = "running"
	WorkloadOperationStepSucceeded WorkloadOperationStepStatus = "succeeded"
	WorkloadOperationStepFailed    WorkloadOperationStepStatus = "failed"
	WorkloadOperationStepSkipped   WorkloadOperationStepStatus = "skipped"
)

type NetworkPlane string

const (
	NetworkPlaneTenantVPC      NetworkPlane = "tenant_vpc"
	NetworkPlaneFoundationMesh NetworkPlane = "foundation_mesh"
	NetworkPlaneStorage        NetworkPlane = "storage"
	NetworkPlaneManagement     NetworkPlane = "management"
	NetworkPlanePublicIngress  NetworkPlane = "public_ingress"
)

type StorageAttachmentKind string

const (
	StorageAttachmentRootDisk   StorageAttachmentKind = "root_disk"
	StorageAttachmentDataDisk   StorageAttachmentKind = "data_disk"
	StorageAttachmentSharedPVC  StorageAttachmentKind = "shared_pvc"
	StorageAttachmentObjectFuse StorageAttachmentKind = "object_fuse"
	StorageAttachmentEphemeral  StorageAttachmentKind = "ephemeral"
)

type WorkloadResourceRequest struct {
	CPU          string
	Memory       string
	GPU          GPUSchedulingRequest
	StorageGiB   int64
	StorageClass string
}

type WorkloadNetworkAttachment struct {
	Plane       NetworkPlane
	NetworkID   string
	SubnetID    string
	IPAddress   string
	Primary     bool
	Required    bool
	PolicyRefs  []string
	Description string
}

type WorkloadNetworkPolicy struct {
	TenantIsolated          bool
	AllowIngressFromGateway bool
	AllowEgressToInternet   bool
	AllowedEgressCIDRs      []string
	Attachments             []WorkloadNetworkAttachment
	VPCID                   string
	SubnetID                string
	SecurityGroupIDs        []string
	AssignPrivateIP         bool
	PrivateIP               string
}

type WorkloadStorageAttachment struct {
	Name               string
	Kind               StorageAttachmentKind
	ResourceType       string
	ResourceID         string
	MountPath          string
	SizeGiB            int64
	StorageClass       string
	ReadOnly           bool
	Required           bool
	SourceRef          string
	Status             string
	TaskID             string
	Encrypted          bool
	DeleteOnFailure    bool
	DeleteWithInstance bool
}

type InstanceImageSummary struct {
	ID           string
	Ref          string
	Digest       string
	Name         string
	Tag          string
	Purpose      string
	Architecture string
}

type InstanceGPUSpecReference struct {
	SpecID     string
	GPUType    string
	Shares     int
	MBPerShare int
}

type InstanceComputeSummary struct {
	CPU              string
	Memory           string
	SpecID           string
	GPUType          string
	GPUShares        int
	GPUMBPerShare    int
	AvailabilityZone string
	NodeName         string
}

type InstanceSecurityGroupSummary struct {
	ID   string
	Name string
}

type InstanceEndpointSummary struct {
	Name     string
	Address  string
	Protocol string
	Port     int
}

type InstanceNetworkSummary struct {
	VPCID            string
	VPCName          string
	SubnetID         string
	SubnetName       string
	PrivateIP        string
	SecurityGroups   []InstanceSecurityGroupSummary
	Endpoints        []InstanceEndpointSummary
	LoadBalancerRefs []string
}

type InstanceAccessSummary struct {
	SSHAvailable     bool
	ConsoleAvailable bool
	ExecAvailable    bool
	Reason           string
}

type WorkloadSecretBinding struct {
	SecretID  string
	MountPath string
	EnvPrefix string
}

type InstanceDiskSpec struct {
	VolumeID           string
	Name               string
	SizeGiB            int64
	VolumeType         string
	StorageClass       string
	Encrypted          bool
	DeleteOnFailure    bool
	DeleteWithInstance bool
}

type InstanceVolumeMount struct {
	VolumeID  string
	MountPath string
	ReadOnly  bool
}

type InstanceFilesystemMount struct {
	FilesystemID string
	MountPath    string
	ReadOnly     bool
}

type InstancePortSpec struct {
	Name          string
	ContainerPort int32
	Protocol      string
}

type InstanceEnvVar struct {
	Name      string
	Value     *string
	SecretRef string
}

type InstanceWorkloadIdentityConfig struct {
	Enabled bool
	Scopes  []string
}

type VMInstanceSpec struct {
	BootImage        string
	CloudInitSecret  string
	SSHKeySecret     string
	SSHUsername      string
	PasswordSecret   string
	UserData         string
	OSType           string
	Firmware         string
	MachineType      string
	RootDisk         WorkloadStorageAttachment
	DataDisks        []WorkloadStorageAttachment
	SystemDisk       *InstanceDiskSpec
	DataDiskSpecs    []InstanceDiskSpec
	FilesystemMounts []InstanceFilesystemMount
}

type VMSSHConnectionInfo struct {
	Username string
	Host     string
	Port     int32
	KeyRef   string
	Ready    bool
	Reason   string
}

type VMInstanceSnapshot struct {
	ID               string
	Name             string
	SourceInstanceID string
	State            string
	Reason           string
	CreatedAt        time.Time
	ReadyAt          time.Time
}

type ContainerInstanceSpec struct {
	ImagePullSecret  string
	Ports            []int32
	PortSpecs        []InstancePortSpec
	Env              []InstanceEnvVar
	SecretIDs        []string
	VolumeMounts     []InstanceVolumeMount
	FilesystemMounts []InstanceFilesystemMount
	WorkloadIdentity InstanceWorkloadIdentityConfig
	Replicas         int32
	Volumes          []WorkloadStorageAttachment
}

type ContainerRevisionHistory struct {
	Revision  string
	Image     string
	CreatedAt time.Time
}

type ContainerInstanceStatus struct {
	Replicas      int32
	ReadyReplicas int32
	Revision      string
	RolloutStatus string
	History       []ContainerRevisionHistory
}

type GPUInstanceStatus struct {
	SpecID     string
	GPUType    string
	Shares     int
	MBPerShare int
	Vendor     GPUVendor
	Model      string
	Count      int
	// QueueName is the Volcano/HAMi scheduling queue the workload is routed
	// to, sourced from the planning annotation "ani.kubercloud.io/gpu-queue".
	QueueName string
	// ResourceName is the Kubernetes extended resource name the workload is
	// scheduled against (e.g. "nvidia.com/gpu" for whole-card, "nvidia.com/vgpu"
	// for vGPU slices). It is sourced from the planning annotation
	// "ani.kubercloud.io/gpu-resource-name" and lets the API surface the real
	// allocation mode (whole card vs vGPU) chosen at scheduling time.
	ResourceName       string
	SchedulingState    string
	SchedulingReason   string
	UtilizationPercent float64
}

type InstanceLifecyclePolicy struct {
	AutoStart             bool
	RestartOnFailure      bool
	DeleteWithTenant      bool
	RetainStorage         bool
	TerminationProtection bool
	MaxRestarts           int
	TTL                   time.Duration
}

type WorkloadSpec struct {
	TenantID                   string
	Name                       string
	Description                string
	Kind                       WorkloadKind
	Image                      string
	ImageID                    string
	ImageRef                   string
	ImageSummary               InstanceImageSummary
	Command                    []string
	Args                       []string
	Resources                  WorkloadResourceRequest
	GPUSpec                    *InstanceGPUSpecReference
	Network                    WorkloadNetworkPolicy
	Storage                    []WorkloadStorageAttachment
	VM                         *VMInstanceSpec
	Container                  *ContainerInstanceSpec
	Lifecycle                  InstanceLifecyclePolicy
	Labels                     map[string]string
	Annotations                map[string]string
	RuntimeClassName           string
	SchedulerName              string
	ServiceAccountName         string
	Sandbox                    *SandboxConfig
	SandboxCheckpointSourceRef string
	Identity                   *WorkloadIdentityBinding
	SecretBindings             []WorkloadSecretBinding
	TTL                        time.Duration
}

type WorkloadRef struct {
	TenantID   string
	InstanceID string
	Kind       WorkloadKind
	ProviderID string
}

type WorkloadStatus struct {
	Ref       WorkloadRef
	State     WorkloadState
	Endpoint  string
	NodeName  string
	Reason    string
	Networks  []WorkloadNetworkAttachment
	Storage   []WorkloadStorageAttachment
	UpdatedAt time.Time
}

type WorkloadManifest struct {
	Name     string
	Kind     string
	Provider string
	Content  string
}

type WorkloadAdmissionResult struct {
	Allowed  bool
	Reason   string
	Warnings []string
}

type WorkloadPlanAuditRecord struct {
	TenantID        string
	UserID          string
	InstanceID      string
	InstanceName    string
	WorkloadKind    WorkloadKind
	Provider        string
	Manifests       []WorkloadManifest
	AdmissionResult WorkloadAdmissionResult
	CreatedAt       time.Time
}

type WorkloadProviderDryRunResult struct {
	Accepted      bool
	Provider      string
	ManifestCount int
	Reason        string
	Warnings      []string
	CheckedAt     time.Time
}

type WorkloadProviderApplyRequest struct {
	TenantID        string
	UserID          string
	InstanceID      string
	AuditID         string
	PermissionProof string
	Operation       WorkloadLifecycleAction
	Manifests       []WorkloadManifest
	AdmissionResult WorkloadAdmissionResult
	DryRunResult    WorkloadProviderDryRunResult
	RequestedAt     time.Time
	SnapshotName    string
	VolumeID        string
}

type WorkloadProviderApplyResult struct {
	Applied       bool
	Provider      string
	ManifestCount int
	Operation     WorkloadLifecycleAction
	ResourceRefs  []string
	Reason        string
	Warnings      []string
	AppliedAt     time.Time
}

type WorkloadProviderObservation struct {
	TenantID     string
	InstanceID   string
	Kind         WorkloadKind
	Provider     string
	ResourceRefs []string
	Phase        string
	Endpoint     string
	NodeName     string
	Reason       string
	Networks     []WorkloadNetworkAttachment
	Storage      []WorkloadStorageAttachment
	GPUCount     int
	ObservedAt   time.Time
}

type WorkloadReconcileRequest struct {
	AuditID     string
	Current     WorkloadStatus
	ApplyResult WorkloadProviderApplyResult
	Observation WorkloadProviderObservation
}

type WorkloadReconcileResult struct {
	Status       WorkloadStatus
	Changed      bool
	Reason       string
	ReconciledAt time.Time
}

type WorkloadProviderStatusRequest struct {
	TenantID    string
	InstanceID  string
	Kind        WorkloadKind
	ApplyResult WorkloadProviderApplyResult
	RequestedAt time.Time
}

type WorkloadInstanceCreateRequest struct {
	// IdempotencyKey is a client-generated UUID. The server returns the same
	// result for any duplicate submission with the same (tenant_id, IdempotencyKey)
	// within 24 hours, without creating a second instance.
	// Clients MUST supply a new UUID per distinct create intent.
	IdempotencyKey  string
	Spec            WorkloadSpec
	UserID          string
	PermissionProof string
	RequestedAt     time.Time
	// QuotaTxIDs carries the TCC reservation transaction IDs created by
	// QuotaAwareInstanceOrchestrator before delegating to the inner
	// orchestrator. The inner orchestrator persists them on every
	// UpsertStatus call so the reconciler can Confirm/Cancel/Release
	// even if the status transitions synchronously during Create.
	QuotaTxIDs []string
}

type WorkloadInstanceCreateResult struct {
	Ref         WorkloadRef
	OperationID string
	// IdempotentReplay is true when the request reused an existing
	// idempotency_key and no new provider operation was started.
	IdempotentReplay bool
	AuditID          string
	Manifests        []WorkloadManifest
	Admission        WorkloadAdmissionResult
	DryRun           WorkloadProviderDryRunResult
	Apply            WorkloadProviderApplyResult
	Observation      WorkloadProviderObservation
	Reconcile        WorkloadReconcileResult
	FinalStatus      WorkloadStatus
	Orchestrated     bool
	Identity         *WorkloadIdentityBinding
}

type WorkloadInstanceGetRequest struct {
	TenantID   string
	InstanceID string
}

type WorkloadInstanceListRequest struct {
	TenantID        string
	Kind            WorkloadKind
	State           WorkloadState
	Keyword         string
	CreatedAfter    time.Time
	CreatedBefore   time.Time
	SpecID          string
	ImageID         string
	NodeName        string
	RolloutStatus   string
	GPUModel        string
	QueueName       string
	SchedulingState string
	TemplateID      string
	SessionState    string
	Limit           int
	Cursor          string
	Sort            string
}

type WorkloadInstanceLifecycleRequest struct {
	// IdempotencyKey prevents duplicate lifecycle actions on retry.
	IdempotencyKey   string
	TenantID         string
	InstanceID       string
	Action           WorkloadLifecycleAction
	Resources        WorkloadResourceRequest
	SnapshotName     string
	SnapshotID       string
	IncludeDataDisks *bool
	VolumeID         string
	FilesystemID     string
	MountPath        string
	ReadOnly         *bool
	Revision         string
	Replicas         *int32
	ImageID          string
	Strategy         string
	SecretID         string
	BindingType      string
	EnvName          string
	SecurityGroupIDs []string
	Enabled          *bool
	Duration         time.Duration
	UserID           string
	PermissionProof  string
	RequestedAt      time.Time
}

type WorkloadInstanceLifecycleResult struct {
	Action      WorkloadLifecycleAction
	OperationID string
	Accepted    bool
	Reason      string
	Warnings    []string
	CheckedAt   time.Time
}

type WorkloadInstanceResizeRequest struct {
	IdempotencyKey  string
	TenantID        string
	InstanceID      string
	Resources       WorkloadResourceRequest
	UserID          string
	PermissionProof string
	RequestedAt     time.Time
}

type WorkloadInstanceOpsAction string

const (
	WorkloadInstanceOpsLogs      WorkloadInstanceOpsAction = "logs"
	WorkloadInstanceOpsEvents    WorkloadInstanceOpsAction = "events"
	WorkloadInstanceOpsMetrics   WorkloadInstanceOpsAction = "metrics"
	WorkloadInstanceOpsTerminal  WorkloadInstanceOpsAction = "terminal"
	WorkloadInstanceOpsExec      WorkloadInstanceOpsAction = "exec"
	WorkloadInstanceOpsVMConsole WorkloadInstanceOpsAction = "vm_console"
	WorkloadInstanceOpsVMVNC     WorkloadInstanceOpsAction = "vm_vnc"
	WorkloadInstanceOpsVMSerial  WorkloadInstanceOpsAction = "vm_serial_console"
)

type WorkloadInstanceOpsRequest struct {
	TenantID        string
	InstanceID      string
	Action          WorkloadInstanceOpsAction
	Protocol        string
	ContainerName   string
	Command         []string
	SinceSeconds    int64
	Limit           int32
	UserID          string
	PermissionProof string
	RequestedAt     time.Time
}

type WorkloadInstanceOpsResult struct {
	Action      WorkloadInstanceOpsAction `json:"action"`
	Accepted    bool                      `json:"accepted"`
	OperationID string                    `json:"operation_id,omitempty"`
	SessionID   string                    `json:"session_id"`
	Protocol    string                    `json:"protocol"`
	ConnectURL  string                    `json:"connect_url"`
	URL         string                    `json:"url,omitempty"`
	Output      string                    `json:"output"`
	Reason      string                    `json:"reason"`
	Warnings    []string                  `json:"warnings"`
	CheckedAt   time.Time                 `json:"checked_at"`
	ExpiresAt   time.Time                 `json:"expires_at"`
}

type WorkloadInstanceRecord struct {
	TenantID           string
	InstanceID         string
	OperationID        string
	Name               string
	Description        string
	Labels             map[string]string
	Kind               WorkloadKind
	Provider           string
	AuditID            string
	Image              InstanceImageSummary
	Compute            InstanceComputeSummary
	Network            InstanceNetworkSummary
	Access             InstanceAccessSummary
	StorageAttachments []WorkloadStorageAttachment
	Lifecycle          InstanceLifecyclePolicy
	SSH                *VMSSHConnectionInfo
	Snapshots          []VMInstanceSnapshot
	Container          *ContainerInstanceStatus
	GPU                *GPUInstanceStatus
	Sandbox            *SandboxInstanceStatus
	Identity           *WorkloadIdentityBinding
	ResourceRefs       []string
	// QuotaTxIDs stores the TCC reservation transaction IDs created by
	// QuotaService.TryManyTx during instance creation. The reconciler uses
	// these to Confirm (pending->running), Cancel (pending->failed), or
	// Release (running->failed) within the same transaction as the status
	// update (SPEC §5.1). Empty when GPU_QUOTA_ENABLED=false.
	QuotaTxIDs []string
	Status     WorkloadStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type WorkloadIdentityBinding struct {
	TenantID   string
	InstanceID string
	KeyID      string
	KeyPrefix  string
	// KeyValue is returned only at bind time so the runtime adapter can inject it
	// into the workload. Stores and query paths must not return it later.
	KeyValue  string
	Scopes    []string
	Active    bool
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt time.Time
}

type WorkloadIdentityBindRequest struct {
	TenantID     string
	InstanceID   string
	InstanceName string
	Kind         WorkloadKind
	UserID       string
	Scopes       []string
	TTL          time.Duration
	RequestedAt  time.Time
}

type WorkloadIdentityRevokeRequest struct {
	TenantID    string
	InstanceID  string
	RequestedAt time.Time
}

type WorkloadOperationStep struct {
	StepName     string
	Status       WorkloadOperationStepStatus
	Message      string
	TaskID       string
	ResourceType string
	ResourceID   string
	StartedAt    time.Time
	CompletedAt  time.Time
	CreatedAt    time.Time
}

type WorkloadOperationRecord struct {
	ID                string
	TenantID          string
	InstanceID        string
	Operation         WorkloadLifecycleAction
	Status            WorkloadOperationStatus
	IdempotencyKey    string
	RequestedBy       string
	Precheck          map[string]any
	DestructiveImpact map[string]any
	BeforeSpec        map[string]any
	AfterSpec         map[string]any
	ProviderRefs      []string
	FailureReason     string
	FailureMessage    string
	RetryEligible     bool
	Steps             []WorkloadOperationStep
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type WorkloadOperationUpdate struct {
	InstanceID     string
	Status         WorkloadOperationStatus
	ProviderRefs   []string
	FailureReason  string
	FailureMessage string
	RetryEligible  bool
	UpdatedAt      time.Time
}

type WorkloadOperationListRequest struct {
	TenantID   string
	InstanceID string
	Limit      int
	Cursor     string
}

type WorkloadOperationListResult struct {
	Items      []WorkloadOperationRecord
	NextCursor string
}

type WorkloadRuntimeCapabilities struct {
	SupportedKinds         []WorkloadKind
	SupportsGPU            bool
	SupportsVM             bool
	SupportsRuntimeClass   bool
	SupportsTenantNetwork  bool
	SupportsInstanceResize bool
}

// WorkloadRuntime owns instance lifecycle across VM, normal container, GPU
// container, notebook, agent sandbox, batch, and inference workloads. Business
// services depend on this port instead of directly binding to KubeVirt,
// Kubernetes Pod/Deployment APIs, or future runtime providers.
type WorkloadRuntime interface {
	Capabilities(ctx context.Context) (WorkloadRuntimeCapabilities, error)
	Create(ctx context.Context, spec WorkloadSpec) (WorkloadRef, error)
	Get(ctx context.Context, ref WorkloadRef) (WorkloadStatus, error)
	ApplyLifecycle(ctx context.Context, ref WorkloadRef, action WorkloadLifecycleAction) (WorkloadStatus, error)
	Delete(ctx context.Context, ref WorkloadRef) error
	List(ctx context.Context, tenantID string, kind WorkloadKind) ([]WorkloadStatus, error)
}

// WorkloadRenderer converts a planned ANI instance into provider manifests for
// review, dry-run validation, and later provider adapter execution.
type WorkloadRenderer interface {
	Render(ctx context.Context, spec WorkloadSpec) ([]WorkloadManifest, error)
}

// WorkloadAdmission validates rendered provider manifests before any real
// provider adapter can submit server-side dry-run or create requests.
type WorkloadAdmission interface {
	Review(ctx context.Context, manifests []WorkloadManifest) (WorkloadAdmissionResult, error)
}

// WorkloadPlanAuditStore persists the plan/render/admission trail before any
// provider-side dry-run or real create/apply action is allowed.
type WorkloadPlanAuditStore interface {
	RecordPlan(ctx context.Context, record WorkloadPlanAuditRecord) (string, error)
}

// WorkloadProviderDryRun runs provider-side validation without creating
// resources. Implementations may call Kubernetes dryRun=All, KubeVirt through
// Kubernetes dry-run, or an equivalent customer cloud validation API.
type WorkloadProviderDryRun interface {
	DryRun(ctx context.Context, manifests []WorkloadManifest, admission WorkloadAdmissionResult) (WorkloadProviderDryRunResult, error)
}

// WorkloadProviderApply is the controlled boundary for real provider
// create/apply execution. Implementations must fail closed unless execution is
// explicitly enabled and the request carries admission, audit, and provider
// dry-run evidence.
type WorkloadProviderApply interface {
	Apply(ctx context.Context, request WorkloadProviderApplyRequest) (WorkloadProviderApplyResult, error)
}

// WorkloadStatusReconciler converts provider observations into ANI workload
// lifecycle state. Business services must depend on this boundary instead of
// polling Kubernetes, KubeVirt, or customer cloud status APIs directly.
type WorkloadStatusReconciler interface {
	Reconcile(ctx context.Context, request WorkloadReconcileRequest) (WorkloadReconcileResult, error)
}

// WorkloadProviderStatusReader reads provider-specific resource status and
// normalizes it into WorkloadProviderObservation. Provider SDK usage belongs
// inside adapters, not business services.
type WorkloadProviderStatusReader interface {
	Observe(ctx context.Context, request WorkloadProviderStatusRequest) (WorkloadProviderObservation, error)
}

// WorkloadInstanceOrchestrator exposes the business-facing instance creation
// workflow through ANI ports: plan, render, admission, audit, dry-run, gated
// apply, provider status observation, and lifecycle reconcile.
type WorkloadInstanceOrchestrator interface {
	Create(ctx context.Context, request WorkloadInstanceCreateRequest) (WorkloadInstanceCreateResult, error)
}

// WorkloadInstanceResourceResolver validates and enriches references to other
// Core resources before an instance is handed to the provider orchestrator.
// It is intentionally provider-neutral: implementations may use local
// metadata services or real Registry/Network/Storage adapters.
type WorkloadInstanceResourceResolver interface {
	ResolveCreate(ctx context.Context, request WorkloadResourceResolveRequest) (WorkloadResourceResolveResult, error)
}

type WorkloadResourceResolveRequest struct {
	TenantID string
	UserID   string
	Spec     WorkloadSpec
}

type WorkloadResourceResolveResult struct {
	Spec         WorkloadSpec
	ResourceRefs []string
}

// WorkloadInstanceStore persists queryable instance state, provider resource
// references, and audit correlation. Runtime adapters may keep local planning
// state, but business queries should use this store-backed boundary.
type WorkloadInstanceStore interface {
	UpsertStatus(ctx context.Context, record WorkloadInstanceRecord) error
	Get(ctx context.Context, tenantID string, instanceID string) (WorkloadInstanceRecord, error)
	List(ctx context.Context, tenantID string, kind WorkloadKind) ([]WorkloadInstanceRecord, error)
}

// WorkloadInstanceStoreTx is a small transactional companion to
// WorkloadInstanceStore. It lets the reconciler and orchestrator update
// instance status inside an externally-owned MetadataTx -- for example,
// atomically with quota Cancel/Release in the same transaction. Existing
// WorkloadInstanceStore implementations and mocks are unaffected; adapters
// that need transactional status writes implement this additional interface
// (SPEC §3.1/§3.2).
type WorkloadInstanceStoreTx interface {
	UpsertStatusTx(ctx context.Context, tx MetadataTx, record WorkloadInstanceRecord) error
}

// WorkloadInstanceService is the business-facing API layer for VM, container,
// GPU container, and future instance types. It wraps orchestration and
// persistent query ports without exposing provider-specific resources.
type WorkloadInstanceService interface {
	Create(ctx context.Context, request WorkloadInstanceCreateRequest) (WorkloadInstanceCreateResult, error)
	Get(ctx context.Context, request WorkloadInstanceGetRequest) (WorkloadInstanceRecord, error)
	List(ctx context.Context, request WorkloadInstanceListRequest) ([]WorkloadInstanceRecord, error)
	ApplyLifecycle(ctx context.Context, request WorkloadInstanceLifecycleRequest) (WorkloadInstanceRecord, error)
	Start(ctx context.Context, request WorkloadInstanceLifecycleRequest) (WorkloadInstanceRecord, error)
	Stop(ctx context.Context, request WorkloadInstanceLifecycleRequest) (WorkloadInstanceRecord, error)
	Restart(ctx context.Context, request WorkloadInstanceLifecycleRequest) (WorkloadInstanceRecord, error)
	Resize(ctx context.Context, request WorkloadInstanceResizeRequest) (WorkloadInstanceRecord, error)
	Snapshot(ctx context.Context, request WorkloadInstanceLifecycleRequest) (WorkloadInstanceRecord, error)
	AttachVolume(ctx context.Context, request WorkloadInstanceLifecycleRequest) (WorkloadInstanceRecord, error)
	DetachVolume(ctx context.Context, request WorkloadInstanceLifecycleRequest) (WorkloadInstanceRecord, error)
	Rollback(ctx context.Context, request WorkloadInstanceLifecycleRequest) (WorkloadInstanceRecord, error)
	Delete(ctx context.Context, request WorkloadInstanceLifecycleRequest) (WorkloadInstanceRecord, error)
	Ops(ctx context.Context, request WorkloadInstanceOpsRequest) (WorkloadInstanceOpsResult, error)
}

// WorkloadInstanceLifecycleExecutor encapsulates provider-side lifecycle
// actions after an instance already exists. Provider SDK usage must remain
// inside adapters.
type WorkloadInstanceLifecycleExecutor interface {
	Apply(ctx context.Context, request WorkloadInstanceLifecycleRequest, record WorkloadInstanceRecord) (WorkloadInstanceLifecycleResult, error)
}

// WorkloadInstanceOps encapsulates visual operations for container-like
// instances: logs, events, metrics, terminal, and exec. Provider SDK usage must
// remain inside the ops adapter.
type WorkloadInstanceOps interface {
	Run(ctx context.Context, request WorkloadInstanceOpsRequest, record WorkloadInstanceRecord) (WorkloadInstanceOpsResult, error)
}

// WorkloadIdentityService owns lifecycle-bound scoped API keys for workloads.
// Running instances must use these short-scope bindings instead of long-lived
// user API keys. Implementations may persist into api_keys or a provider-native
// identity system, but callers only depend on this ANI capability boundary.
type WorkloadIdentityService interface {
	BindScopedKey(ctx context.Context, request WorkloadIdentityBindRequest) (WorkloadIdentityBinding, error)
	GetForInstance(ctx context.Context, tenantID string, instanceID string) (WorkloadIdentityBinding, error)
	RevokeForInstance(ctx context.Context, request WorkloadIdentityRevokeRequest) (WorkloadIdentityBinding, error)
}

// WorkloadOperationStore persists instance operation records and their
// timeline. It is the query boundary behind GET /instances/{id}/operations and
// the idempotency boundary for create/lifecycle retries.
type WorkloadOperationStore interface {
	RecordOperation(ctx context.Context, record WorkloadOperationRecord) (WorkloadOperationRecord, bool, error)
	GetOperation(ctx context.Context, tenantID string, operationID string) (WorkloadOperationRecord, error)
	GetOperationByIdempotencyKey(ctx context.Context, tenantID string, idempotencyKey string) (WorkloadOperationRecord, error)
	ListOperations(ctx context.Context, request WorkloadOperationListRequest) (WorkloadOperationListResult, error)
	AddOperationStep(ctx context.Context, operationID string, step WorkloadOperationStep) (WorkloadOperationStep, error)
	UpdateOperation(ctx context.Context, operationID string, update WorkloadOperationUpdate) (WorkloadOperationRecord, error)
}
