package ports

import (
	"context"
	"time"
)

type DevProfileInfo struct {
	Mode         string
	Provider     string
	RealProvider bool
	Reason       string
}

type SandboxNetworkEgressPolicy string

const (
	SandboxNetworkEgressDenyAll   SandboxNetworkEgressPolicy = "deny_all"
	SandboxNetworkEgressAllowlist SandboxNetworkEgressPolicy = "allowlist"
	SandboxNetworkEgressInternet  SandboxNetworkEgressPolicy = "internet"
)

type SandboxState string

const (
	SandboxStatePending SandboxState = "pending"
	SandboxStateRunning SandboxState = "running"
	SandboxStatePaused  SandboxState = "paused"
	SandboxStateExpired SandboxState = "expired"
	SandboxStateStopped SandboxState = "stopped"
)

type SandboxConfig struct {
	RuntimeClass        string
	TemplateID          string
	SessionTimeout      time.Duration
	IdleTimeout         time.Duration
	OnTimeout           string
	NetworkEgressPolicy SandboxNetworkEgressPolicy
	EgressAllowlist     []string
	Env                 []InstanceEnvVar
	InitialPorts        []InstancePortSpec
	// ExpiresAt is the session deadline (= createdAt + SessionTimeout). The
	// expiration controller compares now against it to decide when to run the
	// OnTimeout action (Bug-7). extend pushes it forward by the requested
	// duration instead of mutating SessionTimeout in place.
	ExpiresAt time.Time
	// LastActivityAt is the last activity timestamp refreshed by touch_idle.
	// When IdleTimeout > 0, idle expiration fires at LastActivityAt+IdleTimeout.
	LastActivityAt time.Time
}

type SandboxCreateRequest struct {
	TenantID            string
	Name                string
	Image               string
	Config              SandboxConfig
	CheckpointSourceRef string
	AutoStart           bool
	CreatedAt           time.Time
}

type SandboxGetRequest struct {
	TenantID   string
	InstanceID string
}

type SandboxListRequest struct {
	TenantID string
}

// SandboxExecutionContext is the persisted sandbox state supplied by the
// application layer for one runtime operation. Provider runtimes must not rely
// on process-local instance registries as the source of truth.
type SandboxExecutionContext struct {
	TenantID     string
	InstanceID   string
	Name         string
	Provider     string
	State        SandboxState
	SessionState string
	Config       SandboxConfig
	DevProfile   DevProfileInfo
	Ports        []SandboxPortResult
	ResourceRefs []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SandboxLifecycleRequest struct {
	TenantID    string
	InstanceID  string
	Execution   *SandboxExecutionContext
	Action      WorkloadLifecycleAction
	Duration    time.Duration
	RequestedAt time.Time
}

type SandboxTokenRequest struct {
	TenantID       string
	InstanceID     string
	Execution      *SandboxExecutionContext
	IdempotencyKey string
	ExpiresIn      time.Duration
	Scopes         []string
	RequestedAt    time.Time
}

type SandboxTokenResult struct {
	Token     string
	ExpiresAt time.Time
	Scopes    []string
}

type SandboxPortRequest struct {
	TenantID       string
	InstanceID     string
	Execution      *SandboxExecutionContext
	IdempotencyKey string
	Port           int
	Name           string
	Protocol       string
	RequestedAt    time.Time
}

type SandboxPortDeleteRequest struct {
	TenantID       string
	InstanceID     string
	Execution      *SandboxExecutionContext
	IdempotencyKey string
	Port           int
	RequestedAt    time.Time
}

type SandboxPortResult struct {
	Port       int
	Name       string
	Protocol   string
	Status     string
	PreviewURL string
	ExpiresAt  time.Time
}

type SandboxFileListRequest struct {
	TenantID   string
	InstanceID string
	Execution  *SandboxExecutionContext
	Path       string
	Limit      int
	Cursor     string
}

type SandboxFileWriteRequest struct {
	TenantID       string
	InstanceID     string
	Execution      *SandboxExecutionContext
	IdempotencyKey string
	Path           string
	ContentBase64  string
	UploadID       string
	Overwrite      bool
	RequestedAt    time.Time
}

type SandboxFileDeleteRequest struct {
	TenantID       string
	InstanceID     string
	Execution      *SandboxExecutionContext
	IdempotencyKey string
	Path           string
	RequestedAt    time.Time
}

type SandboxFileResult struct {
	Path      string
	Kind      string
	SizeBytes int64
	UpdatedAt time.Time
}

type SandboxFileListResult struct {
	Items      []SandboxFileResult
	Total      int
	NextCursor string
}

type SandboxCheckpointCreateRequest struct {
	TenantID       string
	InstanceID     string
	Execution      *SandboxExecutionContext
	IdempotencyKey string
	Name           string
	KeepMemory     bool
	RequestedAt    time.Time
}

type SandboxCheckpointListRequest struct {
	TenantID   string
	InstanceID string
	Execution  *SandboxExecutionContext
	Limit      int
	Cursor     string
}

type SandboxCheckpointRestoreRequest struct {
	TenantID       string
	InstanceID     string
	Execution      *SandboxExecutionContext
	CheckpointID   string
	IdempotencyKey string
	RequestedAt    time.Time
}

type SandboxCheckpointCloneRequest struct {
	TenantID       string
	InstanceID     string
	Execution      *SandboxExecutionContext
	CheckpointID   string
	IdempotencyKey string
	Name           string
	RequestedAt    time.Time
}

type SandboxCheckpointResult struct {
	ID         string
	Name       string
	Status     string
	KeepMemory bool
	CreatedAt  time.Time
	SizeBytes  int64
	Reason     string
	// ProviderRef is an internal provider object reference used by clone. It is
	// intentionally omitted from the public checkpoint response.
	ProviderRef string
}

type SandboxCheckpointListResult struct {
	Items      []SandboxCheckpointResult
	Total      int
	NextCursor string
}

type SandboxCodeRunRequest struct {
	TenantID       string
	InstanceID     string
	Execution      *SandboxExecutionContext
	IdempotencyKey string
	Language       string
	Code           string
	TimeoutSeconds int
	Stdin          string
	RequestedAt    time.Time
}

type SandboxCodeRunResult struct {
	ID          string
	Status      string
	Language    string
	Stdout      string
	Stderr      string
	ExitCode    *int
	Truncated   bool
	CreatedAt   time.Time
	CompletedAt *time.Time
}

type SandboxInstanceStatus struct {
	TenantID     string
	InstanceID   string
	Name         string
	Kind         WorkloadKind
	Provider     string
	State        SandboxState
	TemplateID   string
	SessionState string
	Config       SandboxConfig
	DevProfile   DevProfileInfo
	Ports        []SandboxPortResult
	// ResourceRefs are opaque provider refs (e.g. kubernetes/Deployment/name) when a
	// real provider applied sandbox workload objects. Local profile leaves this empty.
	ResourceRefs []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SandboxRuntime owns ANI sandbox session intent and state. It does not expose
// Kubernetes, Kata, RuntimeClass, Pod, or CRI provider SDK objects.
type SandboxRuntime interface {
	Create(ctx context.Context, request SandboxCreateRequest) (SandboxInstanceStatus, error)
	Get(ctx context.Context, request SandboxGetRequest) (SandboxInstanceStatus, error)
	List(ctx context.Context, request SandboxListRequest) ([]SandboxInstanceStatus, error)
	ApplyLifecycle(ctx context.Context, request SandboxLifecycleRequest) (SandboxInstanceStatus, error)
	CreateToken(ctx context.Context, request SandboxTokenRequest) (SandboxTokenResult, error)
	CreatePort(ctx context.Context, request SandboxPortRequest) (SandboxPortResult, error)
	DeletePort(ctx context.Context, request SandboxPortDeleteRequest) (SandboxPortResult, error)
	ListFiles(ctx context.Context, request SandboxFileListRequest) (SandboxFileListResult, error)
	WriteFile(ctx context.Context, request SandboxFileWriteRequest) (SandboxFileResult, error)
	DeleteFile(ctx context.Context, request SandboxFileDeleteRequest) error
	CreateCheckpoint(ctx context.Context, request SandboxCheckpointCreateRequest) (SandboxCheckpointResult, error)
	ListCheckpoints(ctx context.Context, request SandboxCheckpointListRequest) (SandboxCheckpointListResult, error)
	RestoreCheckpoint(ctx context.Context, request SandboxCheckpointRestoreRequest) (SandboxCheckpointResult, error)
	CloneCheckpoint(ctx context.Context, request SandboxCheckpointCloneRequest) (SandboxCheckpointResult, error)
	CreateCodeRun(ctx context.Context, request SandboxCodeRunRequest) (SandboxCodeRunResult, error)
}

// ExpirableSandboxLister cross-tenant enumerates currently non-terminal sandbox
// records for the expiration background controller. Implementations must read
// across all tenants (platform read), not be tenant-scoped.
type ExpirableSandboxLister interface {
	ListRunningSandboxes(ctx context.Context, limit int) ([]WorkloadInstanceRecord, error)
}

// SandboxExpirationController is the background control-plane loop that fires
// the configured OnTimeout action for expired sandboxes and persists the
// terminal "expired" state (Bug-7). It is hosted by the gateway, alongside the
// workload reconcile controller.
type SandboxExpirationController interface {
	Start(ctx context.Context) error
}

// SandboxExpirationStatusWriter persists terminal sandbox states fired by the
// cross-tenant background expiration controller (Bug-7). It must write through
// the platform bypass because the controller holds no per-tenant context: a
// plain tenant-scoped UpsertStatus would require a tenant id in the context
// (Auth middleware), which the background goroutine does not have. Keeping this
// as a separate lane avoids widening the tenant-scoped WorkloadInstanceStore
// contract.
type SandboxExpirationStatusWriter interface {
	UpsertStatusAsPlatform(ctx context.Context, record WorkloadInstanceRecord) error
}
