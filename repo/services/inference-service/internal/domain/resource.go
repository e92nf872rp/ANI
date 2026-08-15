package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusDeploying Status = "deploying"
	StatusRunning   Status = "running"
	StatusStopping  Status = "stopping"
	StatusStopped   Status = "stopped"
	StatusFailed    Status = "failed"
)

type DesiredState string

const (
	DesiredStateRunning DesiredState = "running"
	DesiredStateStopped DesiredState = "stopped"
	DesiredStateDeleted DesiredState = "deleted"
)

type Action string

const (
	ActionCreate  Action = "create"
	ActionScale   Action = "scale"
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionDelete  Action = "delete"
)

type OperationState string

const (
	OperationPending    OperationState = "pending"
	OperationRunning    OperationState = "running"
	OperationCompleted  OperationState = "completed"
	OperationFailed     OperationState = "failed"
	OperationCancelled  OperationState = "cancelled"
	OperationDeadLetter OperationState = "dead_letter"
)

type Accelerator struct {
	SpecID          string `json:"spec_id"`
	CountPerReplica int    `json:"count_per_replica"`
}

type ExecutionProfile struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Runtime        string `json:"runtime"`
	ImageRef       string `json:"image_ref"`
	ArtifactRef    string `json:"artifact_ref"`
	ArtifactDigest string `json:"artifact_digest"`
	SecretRef      string `json:"secret_ref,omitempty"`
}

type Spec struct {
	Replicas             int              `json:"replicas"`
	CPU                  string           `json:"cpu,omitempty"`
	Memory               string           `json:"memory,omitempty"`
	Accelerator          *Accelerator     `json:"accelerator,omitempty"`
	PlacementMode        string           `json:"placement_mode,omitempty"`
	ExecutionProfile     ExecutionProfile `json:"execution_profile"`
	LegacyGPUType        string           `json:"gpu_type,omitempty"`
	LegacyGPUCountPerPod int              `json:"gpu_count_per_pod,omitempty"`
}

func (s Spec) UsesAccelerator() bool {
	return s.Accelerator != nil
}

type Service struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	Name               string
	ModelVersionID     uuid.UUID
	ServedModelName    string
	ModelSnapshot      json.RawMessage
	Status             Status
	StatusReason       string
	StatusMessage      string
	DesiredState       DesiredState
	Generation         int64
	ObservedGeneration int64
	DesiredSpec        Spec
	AppliedSpec        Spec
	RuntimeRef         uuid.UUID
	RuntimeEndpoint    string
	InvocationURL      string
	ReadyReplicas      int
	CurrentOperationID uuid.UUID
	ActiveOperationID  uuid.UUID
	ActiveOperation    Action
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DeletedAt          *time.Time
	LegacyQuarantined  bool
}

type Operation struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	ServiceID            uuid.UUID
	Type                 Action
	State                OperationState
	TargetGeneration     int64
	BeforeSpec           Spec
	TargetSpec           Spec
	PreemptedOperationID uuid.UUID
	OperationScope       string
	IdempotencyKey       uuid.UUID
	RequestHash          string
	Attempt              int
	NextAttemptAt        time.Time
	LeaseOwner           string
	LeaseUntil           *time.Time
	LeaseToken           uuid.UUID
	RuntimeTaskID        string
	ErrorCode            string
	ErrorMessage         string
	ResultSnapshot       json.RawMessage
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CompletedAt          *time.Time
	Replayed             bool
}

func (o Operation) TaskType() string {
	return "inference_service." + string(o.Type)
}
