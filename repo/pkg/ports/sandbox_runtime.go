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
}

type SandboxCreateRequest struct {
	TenantID  string
	Name      string
	Image     string
	Config    SandboxConfig
	AutoStart bool
	CreatedAt time.Time
}

type SandboxGetRequest struct {
	TenantID   string
	InstanceID string
}

type SandboxListRequest struct {
	TenantID string
}

type SandboxLifecycleRequest struct {
	TenantID    string
	InstanceID  string
	Action      WorkloadLifecycleAction
	Duration    time.Duration
	RequestedAt time.Time
}

type SandboxTokenRequest struct {
	TenantID       string
	InstanceID     string
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
	IdempotencyKey string
	Port           int
	Name           string
	Protocol       string
	RequestedAt    time.Time
}

type SandboxPortDeleteRequest struct {
	TenantID       string
	InstanceID     string
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
	Path       string
	Limit      int
	Cursor     string
}

type SandboxFileWriteRequest struct {
	TenantID       string
	InstanceID     string
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
	IdempotencyKey string
	Name           string
	KeepMemory     bool
	RequestedAt    time.Time
}

type SandboxCheckpointListRequest struct {
	TenantID   string
	InstanceID string
	Limit      int
	Cursor     string
}

type SandboxCheckpointRestoreRequest struct {
	TenantID       string
	InstanceID     string
	CheckpointID   string
	IdempotencyKey string
	RequestedAt    time.Time
}

type SandboxCheckpointCloneRequest struct {
	TenantID       string
	InstanceID     string
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
}

type SandboxCheckpointListResult struct {
	Items      []SandboxCheckpointResult
	Total      int
	NextCursor string
}

type SandboxCodeRunRequest struct {
	TenantID       string
	InstanceID     string
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
