package ports

import (
	"context"
	"time"
)

type PlatformWorkloadState string

const (
	PlatformWorkloadPending      PlatformWorkloadState = "pending"
	PlatformWorkloadProvisioning PlatformWorkloadState = "provisioning"
	PlatformWorkloadRunning      PlatformWorkloadState = "running"
	PlatformWorkloadStarting     PlatformWorkloadState = "starting"
	PlatformWorkloadStopping     PlatformWorkloadState = "stopping"
	PlatformWorkloadStopped      PlatformWorkloadState = "stopped"
	PlatformWorkloadFailed       PlatformWorkloadState = "failed"
	PlatformWorkloadDeleting     PlatformWorkloadState = "deleting"
	PlatformWorkloadDeleted      PlatformWorkloadState = "deleted"
)

type PlatformWorkloadResources struct {
	CPU               string
	Memory            string
	AcceleratorSpecID string
	AcceleratorCount  int
}

type PlatformWorkloadTopology struct {
	Mode           string
	ProfileID      string
	ProfileVersion string
	HasLeader      bool
	HasWorkers     bool
}

type PlatformWorkloadScheduling struct {
	QueueClass string
	Gang       bool
}

type PlatformWorkloadNetwork struct {
	Exposure string
	Ports    []PlatformWorkloadPort
}

type PlatformWorkloadPort struct {
	Name string
	Port int
}

type PlatformWorkloadArtifact struct {
	ObjectRef string
	MountPath string
}

type PlatformWorkloadSecretBinding struct {
	SecretRef string
	MountPath string
}

type PlatformWorkloadHealthCheck struct {
	Protocol string
	Path     string
	PortName string
}

type PlatformWorkloadMetadata struct {
	OwnerRef string
	Labels   map[string]string
}

type PlatformWorkloadCreateSpec struct {
	IdempotencyKey string
	Name           string
	WorkloadClass  string
	RuntimeKind    string
	ImageRef       string
	Command        []string
	Args           []string
	Replicas       int
	Resources      PlatformWorkloadResources
	Topology       PlatformWorkloadTopology
	Scheduling     PlatformWorkloadScheduling
	Network        PlatformWorkloadNetwork
	Artifacts      []PlatformWorkloadArtifact
	SecretBindings []PlatformWorkloadSecretBinding
	HealthCheck    PlatformWorkloadHealthCheck
	Metadata       PlatformWorkloadMetadata
}

type PlatformWorkloadRecord struct {
	ID                     string
	TenantID               string
	Name                   string
	State                  PlatformWorkloadState
	Generation             int64
	ObservedGeneration     int64
	DesiredReplicas        int
	ReadyReplicas          int
	RuntimeShape           string
	TopologyProfileID      string
	TopologyProfileVersion string
	InternalEndpoint       string
	Reason                 string
	Message                string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type PlatformWorkloadCapabilities struct {
	SupportedTopologyModes []string
	LeaderWorkerSetReady   bool
	GangSchedulingReady    bool
	SupportedProfiles      []PlatformWorkloadTopologyProfile
	AcceleratorSpecs       []PlatformWorkloadAcceleratorCapability
}

type PlatformWorkloadTopologyProfile struct {
	ID      string
	Version string
	Mode    string
}

type PlatformWorkloadAcceleratorCapability struct {
	SpecID             string
	Available          bool
	MaxSingleNodeCount int
}

type PlatformWorkloadLogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
	Replica   string
	Container string
	Stream    string
}

type PlatformWorkloadLogList struct {
	Items      []PlatformWorkloadLogEntry
	NextCursor string
}

// PlatformWorkloadService is the Core product boundary for service-only
// platform workloads. It must not create tenant /instances records.
type PlatformWorkloadService interface {
	Capabilities(context.Context) (PlatformWorkloadCapabilities, error)
	Create(context.Context, string, PlatformWorkloadCreateSpec) (PlatformWorkloadRecord, error)
	Get(context.Context, string, string) (PlatformWorkloadRecord, error)
	UpdateReplicas(context.Context, string, string, string, int) (PlatformWorkloadRecord, error)
	ApplyLifecycle(context.Context, string, string, string, string) (PlatformWorkloadRecord, error)
	Delete(context.Context, string, string, string) (PlatformWorkloadRecord, error)
	Logs(context.Context, string, string, int, string, string) (PlatformWorkloadLogList, error)
}
