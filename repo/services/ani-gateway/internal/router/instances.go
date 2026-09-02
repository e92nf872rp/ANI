package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	registryadapter "github.com/kubercloud/ani/pkg/adapters/registry"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type memoryInstanceStore struct {
	mu      sync.RWMutex
	records map[string]ports.WorkloadInstanceRecord
}

func newMemoryInstanceStore() *memoryInstanceStore {
	return &memoryInstanceStore{records: map[string]ports.WorkloadInstanceRecord{}}
}

func (s *memoryInstanceStore) UpsertStatus(_ context.Context, record ports.WorkloadInstanceRecord) error {
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.InstanceID) == "" {
		return fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.TenantID+"/"+record.InstanceID] = record
	return nil
}

func (s *memoryInstanceStore) Get(_ context.Context, tenantID string, instanceID string) (ports.WorkloadInstanceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[tenantID+"/"+instanceID]
	if !ok {
		return ports.WorkloadInstanceRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *memoryInstanceStore) List(_ context.Context, tenantID string, kind ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := make([]ports.WorkloadInstanceRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.TenantID != tenantID {
			continue
		}
		if kind != "" && record.Kind != kind {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records, nil
}

var _ ports.WorkloadInstanceStore = (*memoryInstanceStore)(nil)

type instanceAPI struct {
	service                       ports.WorkloadInstanceService
	operations                    ports.WorkloadOperationStore
	observability                 ports.InstanceObservability
	observabilityUsesInstanceName bool
	gpuInventory                  ports.GPUInventory
	k8sClient                     *runtimeadapter.KubernetesRESTClient
	store                         ports.WorkloadInstanceStore
	sandboxRuntime                ports.SandboxRuntime
	tasks                         ports.AsyncTaskStore
	realProvider                  bool
	providerName                  string
}

type InstanceRuntime struct {
	Service        ports.WorkloadInstanceService
	Store          ports.WorkloadInstanceStore
	Operations     ports.WorkloadOperationStore
	SandboxRuntime ports.SandboxRuntime
	TaskStore      ports.AsyncTaskStore
	RealProvider   bool
	Provider       string
}

type createInstanceRequest struct {
	Kind                  string                     `json:"kind"`
	InstanceType          string                     `json:"instance_type"`
	Name                  string                     `json:"name"`
	CPU                   string                     `json:"cpu"`
	Memory                string                     `json:"memory"`
	BootImage             string                     `json:"boot_image"`
	SSHUsername           string                     `json:"ssh_username"`
	SSHKeyRef             string                     `json:"ssh_key_ref"`
	Image                 string                     `json:"image"`
	ImageID               string                     `json:"image_id"`
	ImageRef              string                     `json:"image_ref"`
	Labels                map[string]string          `json:"labels"`
	GPUVendor             string                     `json:"gpu_vendor"`
	GPUModel              string                     `json:"gpu_model"`
	GPUCount              int                        `json:"gpu_count"`
	GPU                   createGPURequest           `json:"gpu"`
	Replicas              int                        `json:"replicas"`
	AutoStart             *bool                      `json:"auto_start"`
	TerminationProtection bool                       `json:"termination_protection"`
	VMConfig              *vmConfigRequest           `json:"vm_config"`
	ContainerConfig       *containerConfigRequest    `json:"container_config"`
	GPUContainerConfig    *gpuContainerConfigRequest `json:"gpu_container_config"`
	SandboxConfig         sandboxConfigRequest       `json:"sandbox_config"`
	SecretBindings        []secretBindingRequest     `json:"secret_bindings"`
	Description           string                     `json:"description"`
	// NetworkConfig is the top-level network fallback (v1.yaml
	// InstanceNetworkConfig). Kind-specific *_config.network takes
	// precedence; this is applied first as the base default.
	NetworkConfig  *instanceNetworkRequest `json:"network_config"`
	IdempotencyKey string                  `json:"idempotency_key"`
}

type vmConfigRequest struct {
	BootImage         string                           `json:"boot_image"`
	SSHUsername       string                           `json:"ssh_username"`
	SSHKeyRef         string                           `json:"ssh_key_ref"`
	PasswordSecretRef string                           `json:"password_secret_ref"`
	CloudInitSecret   string                           `json:"cloud_init_secret"`
	UserData          string                           `json:"user_data"`
	OSType            string                           `json:"os_type"`
	Firmware          string                           `json:"firmware"`
	MachineType       string                           `json:"machine_type"`
	Network           *instanceNetworkRequest          `json:"network"`
	SystemDisk        *instanceDiskRequest             `json:"system_disk"`
	DataDisks         []instanceDiskRequest            `json:"data_disks"`
	FilesystemMounts  []instanceFilesystemMountRequest `json:"filesystem_mounts"`
}

type containerConfigRequest struct {
	Network          *instanceNetworkRequest          `json:"network"`
	Replicas         int                              `json:"replicas"`
	Ports            []instancePortRequest            `json:"ports"`
	Env              []instanceEnvRequest             `json:"env"`
	SecretIDs        []string                         `json:"secret_ids"`
	VolumeMounts     []instanceVolumeMountRequest     `json:"volume_mounts"`
	FilesystemMounts []instanceFilesystemMountRequest `json:"filesystem_mounts"`
	WorkloadIdentity *instanceWorkloadIdentityRequest `json:"workload_identity"`
}

type gpuContainerConfigRequest struct {
	Network          *instanceNetworkRequest          `json:"network"`
	Replicas         int                              `json:"replicas"`
	GPU              createGPURequest                 `json:"gpu"`
	Ports            []instancePortRequest            `json:"ports"`
	Env              []instanceEnvRequest             `json:"env"`
	SecretIDs        []string                         `json:"secret_ids"`
	VolumeMounts     []instanceVolumeMountRequest     `json:"volume_mounts"`
	FilesystemMounts []instanceFilesystemMountRequest `json:"filesystem_mounts"`
}

type sandboxConfigRequest struct {
	RuntimeClass        string                `json:"runtime_class"`
	TemplateID          string                `json:"template_id"`
	SessionTimeout      string                `json:"session_timeout"`
	IdleTimeout         string                `json:"idle_timeout"`
	OnTimeout           string                `json:"on_timeout"`
	NetworkEgressPolicy string                `json:"network_egress_policy"`
	EgressAllowlist     []string              `json:"egress_allowlist"`
	Env                 []instanceEnvRequest  `json:"env"`
	InitialPorts        []instancePortRequest `json:"initial_ports"`
}

type instanceNetworkRequest struct {
	VPCID            string   `json:"vpc_id"`
	SubnetID         string   `json:"subnet_id"`
	SecurityGroupIDs []string `json:"security_group_ids"`
	AssignPrivateIP  bool     `json:"assign_private_ip"`
	PrivateIP        string   `json:"private_ip"`
}

type instanceDiskRequest struct {
	VolumeID           string `json:"volume_id"`
	Name               string `json:"name"`
	SizeGiB            int64  `json:"size_gib"`
	VolumeType         string `json:"volume_type"`
	StorageClass       string `json:"storage_class"`
	Encrypted          bool   `json:"encrypted"`
	DeleteOnFailure    bool   `json:"delete_on_failure"`
	DeleteWithInstance bool   `json:"delete_with_instance"`
}

type instanceVolumeMountRequest struct {
	VolumeID  string `json:"volume_id"`
	MountPath string `json:"mount_path"`
	ReadOnly  bool   `json:"read_only"`
}

type instanceFilesystemMountRequest struct {
	FilesystemID string `json:"filesystem_id"`
	MountPath    string `json:"mount_path"`
	ReadOnly     bool   `json:"read_only"`
}

type instancePortRequest struct {
	Name          string `json:"name"`
	ContainerPort int32  `json:"container_port"`
	Protocol      string `json:"protocol"`
}

type instanceEnvRequest struct {
	Name      string  `json:"name"`
	Value     *string `json:"value"`
	SecretRef string  `json:"secret_ref"`
}

type instanceWorkloadIdentityRequest struct {
	Enabled bool     `json:"enabled"`
	Scopes  []string `json:"scopes"`
}

type secretBindingRequest struct {
	SecretID  string `json:"secret_id"`
	MountPath string `json:"mount_path"`
	EnvPrefix string `json:"env_prefix"`
}

type createGPURequest struct {
	SpecID         string `json:"spec_id"`
	Vendor         string `json:"vendor"`
	Model          string `json:"model"`
	Count          int    `json:"count"`
	AllocationMode string `json:"allocation_mode"`
	WorkloadClass  string `json:"workload_class"`
	QueueName      string `json:"queue_name"`
}

type instanceLifecycleRequest struct {
	Action           string   `json:"action"`
	CPU              string   `json:"cpu"`
	Memory           string   `json:"memory"`
	SnapshotName     string   `json:"snapshot_name"`
	SnapshotID       string   `json:"snapshot_id"`
	IncludeDataDisks *bool    `json:"include_data_disks"`
	VolumeID         string   `json:"volume_id"`
	FilesystemID     string   `json:"filesystem_id"`
	MountPath        string   `json:"mount_path"`
	ReadOnly         *bool    `json:"read_only"`
	Revision         string   `json:"revision"`
	Replicas         *int32   `json:"replicas"`
	ImageID          string   `json:"image_id"`
	Strategy         string   `json:"strategy"`
	SecretID         string   `json:"secret_id"`
	BindingType      string   `json:"binding_type"`
	EnvName          string   `json:"env_name"`
	SecurityGroupIDs []string `json:"security_group_ids"`
	Enabled          *bool    `json:"enabled"`
	Duration         string   `json:"duration"`
	IdempotencyKey   string   `json:"idempotency_key"`
}

type instanceConsoleRequest struct {
	Protocol string `json:"protocol"`
}

type shellExecRequest struct {
	Command string `json:"command"`
}

type shellExecResponse struct {
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	CWD      string `json:"cwd"`
}

type createExecSessionRequest struct {
	IdempotencyKey string   `json:"idempotency_key"`
	Container      string   `json:"container"`
	Command        []string `json:"command"`
	TTY            *bool    `json:"tty"`
	Rows           int      `json:"rows"`
	Cols           int      `json:"cols"`
}

type createSandboxTokenRequest struct {
	IdempotencyKey string   `json:"idempotency_key"`
	ExpiresIn      string   `json:"expires_in"`
	Scopes         []string `json:"scopes"`
}

type sandboxTokenResponse struct {
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expires_at"`
	Scopes    []string `json:"scopes"`
}

type createSandboxPortRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Port           int    `json:"port"`
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
}

type sandboxPortResponse struct {
	Port       int    `json:"port"`
	Name       string `json:"name,omitempty"`
	Protocol   string `json:"protocol"`
	Status     string `json:"status"`
	PreviewURL string `json:"preview_url,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

type writeSandboxFileRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Path           string `json:"path"`
	ContentBase64  string `json:"content_base64"`
	UploadID       string `json:"upload_id"`
	Overwrite      bool   `json:"overwrite"`
}

type sandboxFileResponse struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
}

type createSandboxCheckpointRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	KeepMemory     bool   `json:"keep_memory"`
}

type sandboxCheckpointActionRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type cloneSandboxCheckpointRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
}

type createSandboxCodeRunRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Language       string `json:"language"`
	Code           string `json:"code"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Stdin          string `json:"stdin"`
}

type sandboxCheckpointResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	KeepMemory bool   `json:"keep_memory"`
	CreatedAt  string `json:"created_at"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type instanceResponse struct {
	ID                    string                              `json:"id"`
	TenantID              string                              `json:"tenant_id"`
	Name                  string                              `json:"name"`
	Description           string                              `json:"description,omitempty"`
	Labels                map[string]string                   `json:"labels,omitempty"`
	Kind                  string                              `json:"kind"`
	InstanceType          string                              `json:"instance_type"`
	State                 string                              `json:"state"`
	Status                string                              `json:"status"`
	Reason                string                              `json:"reason,omitempty"`
	Provider              string                              `json:"provider"`
	DevProfile            coreDevProfileResponse              `json:"dev_profile"`
	OperationID           string                              `json:"operation_id,omitempty"`
	ResourceRefs          []string                            `json:"resource_refs"`
	Endpoint              string                              `json:"endpoint"`
	Image                 instanceImageSummary                `json:"image"`
	Compute               instanceComputeSummary              `json:"compute"`
	Network               instanceNetworkSummary              `json:"network"`
	Access                instanceAccessSummary               `json:"access"`
	StorageAttachments    []instanceStorageAttachmentResponse `json:"storage_attachments,omitempty"`
	TerminationProtection bool                                `json:"termination_protection"`
	SSH                   *instanceSSHResponse                `json:"ssh,omitempty"`
	Volumes               []instanceVolumeResponse            `json:"volumes,omitempty"`
	Snapshots             []instanceSnapshotResponse          `json:"snapshots,omitempty"`
	Container             *instanceContainerResponse          `json:"container,omitempty"`
	GPU                   *instanceGPUResponse                `json:"gpu,omitempty"`
	Sandbox               *instanceSandboxResponse            `json:"sandbox,omitempty"`
	WorkloadIdentity      *instanceIdentityResponse           `json:"workload_identity,omitempty"`
	CreatedAt             string                              `json:"created_at"`
	UpdatedAt             string                              `json:"updated_at"`
}

type instanceImageSummary struct {
	ID           string `json:"id,omitempty"`
	Ref          string `json:"ref,omitempty"`
	Digest       string `json:"digest,omitempty"`
	Name         string `json:"name,omitempty"`
	Tag          string `json:"tag,omitempty"`
	Purpose      string `json:"purpose,omitempty"`
	Architecture string `json:"architecture,omitempty"`
}

type instanceComputeSummary struct {
	CPU              string `json:"cpu,omitempty"`
	Memory           string `json:"memory,omitempty"`
	SpecID           string `json:"spec_id,omitempty"`
	GPUType          string `json:"gpu_type,omitempty"`
	GPUShares        int    `json:"gpu_shares,omitempty"`
	GPUMBPerShare    int    `json:"gpu_mb_per_share,omitempty"`
	AvailabilityZone string `json:"availability_zone,omitempty"`
	NodeName         string `json:"node_name,omitempty"`
}

type instanceSecurityGroupSummary struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type instanceEndpointSummary struct {
	Name     string `json:"name,omitempty"`
	Address  string `json:"address"`
	Protocol string `json:"protocol,omitempty"`
	Port     int    `json:"port,omitempty"`
}

type instanceNetworkSummary struct {
	VPCID            string                         `json:"vpc_id,omitempty"`
	VPCName          string                         `json:"vpc_name,omitempty"`
	SubnetID         string                         `json:"subnet_id,omitempty"`
	SubnetName       string                         `json:"subnet_name,omitempty"`
	PrivateIP        string                         `json:"private_ip,omitempty"`
	SecurityGroups   []instanceSecurityGroupSummary `json:"security_groups,omitempty"`
	Endpoints        []instanceEndpointSummary      `json:"endpoints,omitempty"`
	LoadBalancerRefs []string                       `json:"load_balancer_refs,omitempty"`
}

type instanceAccessSummary struct {
	SSHAvailable     bool   `json:"ssh_available"`
	ConsoleAvailable bool   `json:"console_available"`
	ExecAvailable    bool   `json:"exec_available"`
	Reason           string `json:"reason,omitempty"`
}

type instanceStorageAttachmentResponse struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Name         string `json:"name,omitempty"`
	MountPath    string `json:"mount_path,omitempty"`
	ReadOnly     bool   `json:"read_only"`
	Status       string `json:"status"`
	TaskID       string `json:"task_id,omitempty"`
}

type instanceSSHResponse struct {
	Username string `json:"username"`
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	KeyRef   string `json:"key_ref,omitempty"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason,omitempty"`
}

type instanceVolumeResponse struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeGiB   int64  `json:"size_gib,omitempty"`
	SourceRef string `json:"source_ref,omitempty"`
	MountPath string `json:"mount_path,omitempty"`
	ReadOnly  bool   `json:"read_only,omitempty"`
}

type instanceSnapshotResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	SourceInstanceID string `json:"source_instance_id"`
	State            string `json:"state"`
	Reason           string `json:"reason,omitempty"`
	CreatedAt        string `json:"created_at"`
	ReadyAt          string `json:"ready_at,omitempty"`
}

type instanceContainerResponse struct {
	Replicas      int32                             `json:"replicas"`
	ReadyReplicas int32                             `json:"ready_replicas"`
	Revision      string                            `json:"revision,omitempty"`
	RolloutStatus string                            `json:"rollout_status,omitempty"`
	History       []instanceContainerChangeResponse `json:"history,omitempty"`
}

type instanceContainerChangeResponse struct {
	Revision  string `json:"revision"`
	Image     string `json:"image,omitempty"`
	CreatedAt string `json:"created_at"`
}

type instanceGPUResponse struct {
	Vendor             string  `json:"vendor,omitempty"`
	Model              string  `json:"model,omitempty"`
	Count              int     `json:"count"`
	ResourceName       string  `json:"resource_name,omitempty"`
	QueueName          string  `json:"queue_name,omitempty"`
	SchedulingReason   string  `json:"scheduling_reason,omitempty"`
	UtilizationPercent float64 `json:"utilization_percent"`
}

type instanceSandboxResponse struct {
	RuntimeClass        string                        `json:"runtime_class"`
	SessionTimeout      string                        `json:"session_timeout"`
	NetworkEgressPolicy string                        `json:"network_egress_policy"`
	SessionState        string                        `json:"session_state"`
	Ports               []instanceSandboxPortResponse `json:"ports,omitempty"`
	DevProfile          coreDevProfileResponse        `json:"dev_profile"`
}

type instanceSandboxPortResponse struct {
	Port       int    `json:"port"`
	Name       string `json:"name,omitempty"`
	Protocol   string `json:"protocol"`
	Status     string `json:"status"`
	PreviewURL string `json:"preview_url,omitempty"`
}

type instanceIdentityResponse struct {
	KeyID     string   `json:"key_id,omitempty"`
	KeyPrefix string   `json:"key_prefix,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	Active    bool     `json:"active"`
	CreatedAt string   `json:"created_at,omitempty"`
	RevokedAt string   `json:"revoked_at,omitempty"`
}

type instanceCreateResponse struct {
	Instance      instanceResponse               `json:"instance"`
	OperationID   string                         `json:"operation_id"`
	AuditID       string                         `json:"audit_id"`
	Manifests     []instanceManifestResponse     `json:"manifests"`
	Timeline      []instanceTimelineStepResponse `json:"timeline"`
	RuntimeNotice string                         `json:"demo_notice"`
}

type instanceLifecycleResponse struct {
	Instance    instanceResponse `json:"instance"`
	OperationID string           `json:"operation_id"`
}

type instanceOperationResponse struct {
	ID             string                         `json:"id"`
	TenantID       string                         `json:"tenant_id"`
	InstanceID     string                         `json:"instance_id"`
	Operation      string                         `json:"operation"`
	Status         string                         `json:"status"`
	IdempotencyKey string                         `json:"idempotency_key,omitempty"`
	RequestedBy    string                         `json:"requested_by"`
	FailureReason  string                         `json:"failure_reason,omitempty"`
	FailureMessage string                         `json:"failure_message,omitempty"`
	RetryEligible  bool                           `json:"retry_eligible"`
	Steps          []instanceTimelineStepResponse `json:"steps"`
	CreatedAt      string                         `json:"created_at"`
	UpdatedAt      string                         `json:"updated_at"`
}

type instanceLogEntryResponse struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Container string `json:"container,omitempty"`
	Stream    string `json:"stream,omitempty"`
}

type instanceLogListResponse struct {
	Items      []instanceLogEntryResponse `json:"items"`
	Total      int                        `json:"total"`
	NextCursor *string                    `json:"next_cursor"`
	DevProfile coreDevProfileResponse     `json:"dev_profile"`
}

type instanceEventResponse struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	Type       string `json:"type"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	Count      int    `json:"count,omitempty"`
	OccurredAt string `json:"occurred_at"`
}

type instanceEventListResponse struct {
	Items      []instanceEventResponse `json:"items"`
	Total      int                     `json:"total"`
	NextCursor *string                 `json:"next_cursor"`
	DevProfile coreDevProfileResponse  `json:"dev_profile"`
}

type instanceMetricsResponse struct {
	InstanceID        string                 `json:"instance_id"`
	Timestamp         string                 `json:"timestamp"`
	CPUUtilizationPct *float64               `json:"cpu_utilization_pct"`
	MemoryUsedMB      *float64               `json:"memory_used_mb"`
	MemoryTotalMB     *float64               `json:"memory_total_mb"`
	GPUUtilizationPct *float64               `json:"gpu_utilization_pct"`
	GPUMemoryUsedMB   *float64               `json:"gpu_memory_used_mb"`
	GPUMemoryTotalMB  *float64               `json:"gpu_memory_total_mb"`
	NetworkRXBytes    *int64                 `json:"network_rx_bytes"`
	NetworkTXBytes    *int64                 `json:"network_tx_bytes"`
	DevProfile        coreDevProfileResponse `json:"dev_profile"`
}

type instanceSecurityEventResponse struct {
	ID          string `json:"id"`
	InstanceID  string `json:"instance_id"`
	EventType   string `json:"event_type"`
	Severity    string `json:"severity"`
	Description string `json:"description,omitempty"`
	OccurredAt  string `json:"occurred_at"`
}

type instanceSecurityEventListResponse struct {
	Items      []instanceSecurityEventResponse `json:"items"`
	Total      int                             `json:"total"`
	NextCursor *string                         `json:"next_cursor"`
	DevProfile coreDevProfileResponse          `json:"dev_profile"`
}

type instanceExecSessionResponse struct {
	ID         string                 `json:"id"`
	InstanceID string                 `json:"instance_id"`
	WSURL      string                 `json:"ws_url"`
	Token      string                 `json:"token,omitempty"`
	ExpiresAt  string                 `json:"expires_at"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
}

type instanceConsoleSessionResponse struct {
	SessionID  string                 `json:"session_id"`
	InstanceID string                 `json:"instance_id"`
	Protocol   string                 `json:"protocol"`
	ConnectURL string                 `json:"connect_url"`
	URL        string                 `json:"url"`
	ExpiresAt  string                 `json:"expires_at"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
}

type instanceManifestResponse struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Content  string `json:"content"`
}

type instanceTimelineStepResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func newInstanceAPI() *instanceAPI {
	return newInstanceAPIWithObservability(nil, false, nil, nil, nil, nil)
}

func newInstanceAPIWithObservability(observability ports.InstanceObservability, useInstanceName bool, gpuInventory ports.GPUInventory, k8sClient *runtimeadapter.KubernetesRESTClient, secrets ports.SecretService, specStore ports.GPUSpecStore) *instanceAPI {
	store := newMemoryInstanceStore()
	operations := runtimeadapter.NewLocalOperationStore()
	identity := runtimeadapter.NewLocalWorkloadIdentityService()

	// Use real K8s GPU inventory when available, otherwise use the fallback inventory.
	var inventory ports.GPUInventory = fallbackGPUInventory{}
	if gpuInventory != nil {
		inventory = gpuInventory
	}

	planner := runtimeadapter.NewPlanningRuntime(runtimeadapter.WithGPUInventory(inventory))

	var (
		dryRun    ports.WorkloadProviderDryRun
		apply     ports.WorkloadProviderApply
		reader    ports.WorkloadProviderStatusReader
		lifecycle ports.WorkloadInstanceLifecycleExecutor
	)
	if k8sClient != nil {
		// Real K8s provider: dry-run, apply, observe, and lifecycle all go
		// through the K8s REST client.
		adapter := runtimeadapter.NewKubernetesProviderAdapter(
			k8sClient,
			runtimeadapter.WithKubernetesProviderApplyEnabled(true),
		)
		dryRun = adapter
		apply = adapter
		reader = adapter
		lifecycle = runtimeadapter.NewKubernetesLifecycleExecutor(
			k8sClient,
			runtimeadapter.WithKubernetesLifecycleEnabled(true),
		)
	} else {
		dryRun = runtimeadapter.NewLocalProviderDryRun()
		apply = runtimeadapter.NewLocalProviderApply(runtimeadapter.WithProviderApplyEnabled(true))
		reader = runtimeadapter.NewLocalProviderStatusReader()
	}

	var orchestrator ports.WorkloadInstanceOrchestrator = runtimeadapter.NewLocalInstanceOrchestrator(
		planner,
		runtimeadapter.NewKubernetesDryRunRenderer(planner),
		runtimeadapter.NewLocalAdmissionGuard(),
		&memoryPlanAuditStore{},
		dryRun,
		apply,
		reader,
		runtimeadapter.NewLocalStatusReconciler(),
		runtimeadapter.WithInstanceStore(store),
		runtimeadapter.WithInstanceOrchestratorWorkloadIdentityService(identity),
	)
	// When a CRD-backed GPUSpecStore is available, wrap the orchestrator with
	// the QuotaAwareInstanceOrchestrator so Volcano resource translation runs
	// (vGPU/wholecard spec_id → volcano.sh/vgpu-memory etc.). Quota gates are
	// nil-safe and skipped when quotaService/metadataStore are absent.
	if specStore != nil {
		translator := runtimeadapter.NewVolcanoResourceTranslator(specStore)
		orchestrator = runtimeadapter.NewQuotaAwareInstanceOrchestrator(
			orchestrator,
			runtimeadapter.WithQuotaAwareQuotaEnabled(true),
			runtimeadapter.WithQuotaAwareTranslator(translator),
		)
	}

	sandboxRuntime := runtimeadapter.NewLocalSandboxRuntime()
	serviceOpts := []runtimeadapter.InstanceServiceOption{
		runtimeadapter.WithOperationStore(operations),
		runtimeadapter.WithWorkloadIdentityService(identity),
		runtimeadapter.WithSandboxRuntime(sandboxRuntime),
		runtimeadapter.WithInstanceResourceResolver(runtimeadapter.NewLocalInstanceResourceResolverWithDependencies(
			runtimeadapter.NewLocalNetworkService(),
			runtimeadapter.NewLocalStorageService(),
			runtimeadapter.NewCompositeGPUSpecService(specStore, runtimeadapter.NewLocalGPUSpecService(inventory)),
			registryadapter.NewLocalImageRegistry(),
			secrets,
		)),
	}
	if lifecycle != nil {
		serviceOpts = append(serviceOpts, runtimeadapter.WithInstanceLifecycleExecutor(lifecycle))
	}
	service := runtimeadapter.NewLocalInstanceServiceWithOptions(
		orchestrator,
		store,
		runtimeadapter.NewLocalInstanceOpsGuard(runtimeadapter.WithInstanceOpsEnabled(true)),
		serviceOpts...,
	)
	if observability == nil {
		observability = runtimeadapter.NewLocalInstanceObservabilityService()
	}
	return &instanceAPI{
		service:                       service,
		operations:                    operations,
		observability:                 observability,
		observabilityUsesInstanceName: useInstanceName,
		gpuInventory:                  gpuInventory,
		k8sClient:                     k8sClient,
		store:                         store,
		sandboxRuntime:                sandboxRuntime,
		tasks:                         defaultTaskStore,
	}
}

func registerInstancesWithObservability(v1 *route.RouterGroup, observability ports.InstanceObservability, useInstanceName bool, gpuInventory ports.GPUInventory, k8sClient *runtimeadapter.KubernetesRESTClient) ports.WorkloadInstanceService {
	service, _ := registerInstancesWithRuntime(v1, observability, useInstanceName, gpuInventory, k8sClient, nil, nil, nil)
	return service
}

// registerInstancesWithRuntime registers the instance routes and returns the
// instance service (used as InstanceLookup by the observability proxy) plus
// the shared observeInstance entry used by the task API lazy sync.
func registerInstancesWithRuntime(v1 *route.RouterGroup, observability ports.InstanceObservability, useInstanceName bool, gpuInventory ports.GPUInventory, k8sClient *runtimeadapter.KubernetesRESTClient, secrets ports.SecretService, runtime *InstanceRuntime, specStore ports.GPUSpecStore) (ports.WorkloadInstanceService, instanceObserver) {
	api := newInstanceAPIWithObservability(observability, useInstanceName, gpuInventory, k8sClient, secrets, specStore)
	if runtime != nil {
		if runtime.Service == nil || runtime.Store == nil || runtime.Operations == nil {
			panic("instance runtime requires service, store, and operations")
		}
		api.service = runtime.Service
		api.store = runtime.Store
		api.operations = runtime.Operations
		api.sandboxRuntime = runtime.SandboxRuntime
		if runtime.TaskStore != nil {
			api.tasks = runtime.TaskStore
		}
		api.realProvider = runtime.RealProvider
		api.providerName = strings.TrimSpace(runtime.Provider)
	}
	v1.GET("/instances", api.list)
	v1.POST("/instances", api.create)
	v1.GET("/instances/:instance_id", api.get)
	v1.POST("/instances/:instance_id/lifecycle", api.lifecycle)
	v1.POST("/instances/:instance_id/console", api.createConsoleSession)
	v1.GET("/instances/:instance_id/logs", api.listLogs)
	v1.GET("/instances/:instance_id/events", api.listEvents)
	v1.GET("/instances/:instance_id/metrics", api.getMetrics)
	v1.POST("/instances/:instance_id/exec", api.createExecSession)
	v1.GET("/instances/:instance_id/security-events", api.listSecurityEvents)
	v1.GET("/instances/:instance_id/operations", api.listOperations)
	v1.POST("/instances/:instance_id/sandbox/tokens", api.createSandboxToken)
	v1.POST("/instances/:instance_id/sandbox/ports", api.createSandboxPort)
	v1.DELETE("/instances/:instance_id/sandbox/ports/:port", api.deleteSandboxPort)
	v1.GET("/instances/:instance_id/sandbox/files", api.listSandboxFiles)
	v1.POST("/instances/:instance_id/sandbox/files", api.writeSandboxFile)
	v1.DELETE("/instances/:instance_id/sandbox/files", api.deleteSandboxFile)
	v1.GET("/instances/:instance_id/sandbox/checkpoints", api.listSandboxCheckpoints)
	v1.POST("/instances/:instance_id/sandbox/checkpoints", api.createSandboxCheckpoint)
	v1.POST("/instances/:instance_id/sandbox/checkpoints/:checkpoint_id/restore", api.restoreSandboxCheckpoint)
	v1.POST("/instances/:instance_id/sandbox/checkpoints/:checkpoint_id/clone", api.cloneSandboxCheckpoint)
	v1.POST("/instances/:instance_id/sandbox/code-runs", api.createSandboxCodeRun)
	v1.GET("/demo/instances", api.list)
	v1.POST("/demo/instances", api.create)
	v1.GET("/demo/instances/:instance_id", api.get)
	v1.GET("/demo/instances/:instance_id/operations", api.listOperations)
	v1.POST("/demo/instances/:instance_id/lifecycle", api.lifecycle)
	v1.GET("/demo/instances/:instance_id/ops/:action", api.ops)
	v1.POST("/demo/instances/:instance_id/console", api.console)
	v1.POST("/demo/instances/:instance_id/console/exec", api.consoleExec)
	v1.GET("/instance-operations/:operation_id", api.getOperation)
	return api.service, api.observeInstance
}

func (api *instanceAPI) create(ctx context.Context, c *app.RequestContext) {
	var req createInstanceRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid instance request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	spec, err := instanceSpecFromRequest(req, instanceTenantID(c))
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	result, err := api.service.Create(ctx, ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  req.IdempotencyKey,
		Spec:            spec,
		UserID:          instanceUserID(c),
		PermissionProof: "demo:instance:create",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		writeInstanceCreateError(c, err)
		return
	}
	if strings.HasPrefix(result.Ref.InstanceID, "pending:") {
		// In-progress idempotent replay: the ref is "pending:<operation-id>",
		// so the instance ID is not resolvable here and no task can be bound
		// to a resource yet. The completed-replay retry below repairs the
		// audit record with the real instance ID.
		log.Printf("[TASK-AUDIT] skip instance.create write for in-progress replay (instance id unresolved): idempotency_key=%s", req.IdempotencyKey)
	} else {
		api.writeAuditTask(ctx, instanceTenantID(c), instanceProgressTask("instance.create", result.Ref.InstanceID, string(spec.Kind), spec.Name, string(result.FinalStatus.State), req.IdempotencyKey))
	}
	if result.IdempotentReplay && strings.HasPrefix(result.Ref.InstanceID, "pending:") {
		c.JSON(http.StatusConflict, map[string]any{
			"code":         "IDEMPOTENT_REPLAY_IN_PROGRESS",
			"message":      "request is already accepted and still in progress",
			"operation_id": result.OperationID,
		})
		return
	}
	record, err := api.service.Get(ctx, ports.WorkloadInstanceGetRequest{
		TenantID:   result.Ref.TenantID,
		InstanceID: result.Ref.InstanceID,
	})
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "INSTANCE_LOOKUP_FAILED", err.Error())
		return
	}
	status := http.StatusCreated
	if result.IdempotentReplay {
		status = http.StatusConflict
	}
	c.JSON(status, instanceCreateResponse{
		Instance:      api.instanceResponseFromRecord(record),
		OperationID:   result.OperationID,
		AuditID:       result.AuditID,
		Manifests:     manifestResponses(result.Manifests),
		Timeline:      instanceTimeline(result),
		RuntimeNotice: "instance profile uses the M1 service; configure the Kubernetes provider for live cluster execution.",
	})
}

// orphanObservation holds the fields extracted from a live Kubernetes
// Deployment that are needed to synthesize a WorkloadInstanceRecord for
// instances that exist in the cluster but not in the in-memory store.
type orphanObservation struct {
	Phase     string
	NodeName  string
	GPUCount  int
	CreatedAt time.Time
	Reason    string
}

// refreshStoreStatuses queries the live Kubernetes cluster for each
// store-backed record and rewrites its status (state, container replicas,
// node name, reason) to reflect the real Deployment/Pod state. This is the
// only mechanism that updates the in-memory store after create,
// because the background reconcile controller is bound to the
// PostgreSQL-backed MetadataInstanceStore, not this in-memory store.
// Records whose Deployment is gone (NotFound) are marked failed so they do
// not linger in "provisioning" forever.
func (api *instanceAPI) refreshStoreStatuses(ctx context.Context, tenantID string, kind ports.WorkloadKind) {
	if api.k8sClient == nil || api.store == nil || strings.TrimSpace(tenantID) == "" {
		return
	}
	records, err := api.store.List(ctx, tenantID, kind)
	if err != nil || len(records) == 0 {
		return
	}
	for i := range records {
		api.refreshOneStoreStatus(ctx, &records[i])
	}
}

// refreshOneStoreStatus refreshes a single store record from K8s. It reuses
// the same Deployment GET + phase mapping as orphan discovery so the phase
// semantics stay consistent.
func (api *instanceAPI) refreshOneStoreStatus(ctx context.Context, record *ports.WorkloadInstanceRecord) {
	if api.k8sClient == nil || record == nil || record.Name == "" || record.Provider != "kubernetes" {
		return
	}
	if record.Status.State == ports.WorkloadStateDeleting || record.Status.State == ports.WorkloadStateDeleted {
		return
	}
	namespace := instanceTenantNamespace(record.TenantID)
	depEndpoint := api.k8sClient.Host() + "/apis/apps/v1/namespaces/" + url.PathEscape(namespace) + "/deployments/" + url.PathEscape(record.Name)
	body, status, err := api.k8sClient.Do(ctx, http.MethodGet, depEndpoint, "", nil)
	if err != nil {
		if status == http.StatusNotFound {
			// Deployment gone: surface as failed instead of stale provisioning.
			record.Status.State = ports.WorkloadStateFailed
			record.Status.Reason = "deployment not found in cluster"
			record.Status.UpdatedAt = time.Now().UTC()
			record.UpdatedAt = record.Status.UpdatedAt
			_ = api.store.UpsertStatus(ctx, *record)
		}
		return
	}
	var dep struct {
		Status struct {
			Replicas          int32 `json:"replicas"`
			UpdatedReplicas   int32 `json:"updatedReplicas"`
			ReadyReplicas     int32 `json:"readyReplicas"`
			AvailableReplicas int32 `json:"availableReplicas"`
			Conditions        []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		return
	}
	var phase string
	switch {
	case dep.Status.AvailableReplicas > 0 || dep.Status.ReadyReplicas > 0:
		phase = "Running"
	case dep.Status.Replicas > 0 || dep.Status.UpdatedReplicas > 0:
		phase = "Provisioning"
	default:
		phase = "Pending"
	}
	record.Status.State = mapProviderPhaseToState(phase)
	record.Status.Reason = ""
	for _, condition := range dep.Status.Conditions {
		if strings.EqualFold(condition.Status, "False") {
			if condition.Reason != "" {
				record.Status.Reason = condition.Reason
			} else if condition.Message != "" {
				record.Status.Reason = condition.Message
			}
			break
		}
	}
	if record.Container != nil {
		record.Container.Replicas = dep.Status.Replicas
		record.Container.ReadyReplicas = dep.Status.ReadyReplicas
		switch phase {
		case "Running":
			record.Container.RolloutStatus = "running"
		case "Provisioning":
			record.Container.RolloutStatus = "progressing"
		default:
			record.Container.RolloutStatus = "pending"
		}
	}
	// Discover the node name from Pods (HAMi stores it in spec.nodeName).
	podEndpoint := api.k8sClient.Host() + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods?labelSelector=" + url.QueryEscape("ani.kubercloud.io/instance="+record.Name)
	podBody, _, podErr := api.k8sClient.Do(ctx, http.MethodGet, podEndpoint, "", nil)
	if podErr == nil {
		var podList struct {
			Items []struct {
				Spec struct {
					NodeName string `json:"nodeName"`
				} `json:"spec"`
				Status struct {
					NodeName   string `json:"nodeName"`
					Conditions []struct {
						Type    string `json:"type"`
						Status  string `json:"status"`
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal(podBody, &podList) == nil {
			// A Deployment may own multiple Pods across rollouts (e.g. a stale
			// Pending pod alongside a healthy Running pod). Prefer the
			// scheduled pod for node name and only surface the scheduling
			// failure reason when no pod has been scheduled.
			scheduledPod := false
			for _, pod := range podList.Items {
				if pod.Spec.NodeName != "" || pod.Status.NodeName != "" {
					scheduledPod = true
					if pod.Spec.NodeName != "" {
						record.Status.NodeName = pod.Spec.NodeName
					} else if pod.Status.NodeName != "" {
						record.Status.NodeName = pod.Status.NodeName
					}
				}
			}
			if scheduledPod {
				// At least one pod is scheduled: clear any stale failure reason.
				record.Status.Reason = ""
			} else {
				// No pod scheduled yet: surface the real scheduling failure
				// reason from the PodScheduled condition (status=False).
				for _, pod := range podList.Items {
					for _, cond := range pod.Status.Conditions {
						if !strings.EqualFold(cond.Type, "PodScheduled") {
							continue
						}
						if strings.EqualFold(cond.Status, "False") {
							if cond.Message != "" {
								record.Status.Reason = cond.Message
							} else if cond.Reason != "" {
								record.Status.Reason = cond.Reason
							}
							break
						}
					}
				}
			}
		}
	}
	record.Status.UpdatedAt = time.Now().UTC()
	record.UpdatedAt = record.Status.UpdatedAt
	_ = api.store.UpsertStatus(ctx, *record)
}

// observeInstance is the lazy-sync observation entry shared with the task
// API: a store read followed by a single-instance Kubernetes refresh. A pure
// service.Get reads the stored snapshot only, so without the refresh the
// task progress would never advance for instances whose list endpoint was
// never called.
func (api *instanceAPI) observeInstance(ctx context.Context, tenantID, instanceID string) (ports.WorkloadInstanceRecord, error) {
	record, err := api.service.Get(ctx, ports.WorkloadInstanceGetRequest{
		TenantID:   tenantID,
		InstanceID: instanceID,
	})
	if err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	api.refreshOneStoreStatus(ctx, &record)
	return record, nil
}

// writeAuditTask persists an instance task as a side effect: it never
// rewrites the main response. storageWriteAcceptedTask is not reused because
// it also rewrites the response (500 on persist failure, 202 + Location on
// success) for the storage domain; instance responses keep their
// 201/200/409 contract even when the task store hiccups.
func (api *instanceAPI) writeAuditTask(ctx context.Context, tenantID string, task ports.AsyncTaskRecord) {
	if api.tasks == nil {
		return
	}
	task.TenantID = tenantID
	if _, _, err := api.tasks.Create(ctx, task); err != nil {
		log.Printf("[TASK-AUDIT] write failed: task_type=%s err=%v", task.TaskType, err)
	}
}

// instanceProgressTask builds a running instance task record: the operation
// has been accepted by the runtime and is converging, so running/10 is the
// honest snapshot at write time. storageCompletedTask is not reused: it
// hardcodes status=completed/progress=100 acceptance-audit semantics.
func instanceProgressTask(taskType, instanceID, kind, name, state, idempotencyKey string) ports.AsyncTaskRecord {
	return ports.AsyncTaskRecord{
		IdempotencyKey: idempotencyKey,
		TaskType:       taskType,
		ResourceType:   "instance",
		ResourceID:     instanceResourceID(instanceID),
		Status:         "running",
		ProgressPct:    10,
		Result: map[string]any{
			"instance_id": instanceID,
			"kind":        kind,
			"name":        name,
			"state":       state,
		},
		MaxAttempts:  1,
		AttemptCount: 1,
		CreatedAt:    time.Now().UTC(),
	}
}

// instanceResourceID extracts the UUID part of an "inst_<uuid>" instance ID
// for the UUID-typed async_tasks.resource_id column (contract format: uuid).
// The full instance ID is preserved in result.instance_id for lazy-sync
// lookups; the same convention as the reconcile controller's outbox
// aggregate_id handling.
func instanceResourceID(instanceID string) string {
	raw := strings.TrimPrefix(instanceID, "inst_")
	if _, err := uuid.Parse(raw); err != nil {
		return ""
	}
	return raw
}

// mapProviderPhaseToState mirrors the status_reconciler mapping for the
// common provider phases surfaced by the Kubernetes REST client. It keeps
// terminal/lifecycle states (stopping, stopped, deleting, deleted) intact
// because those are driven by lifecycle actions, not by the Deployment
// rollout state.
func mapProviderPhaseToState(phase string) ports.WorkloadState {
	switch strings.ToLower(phase) {
	case "running":
		return ports.WorkloadStateRunning
	case "provisioning":
		return ports.WorkloadStateProvisioning
	case "pending":
		return ports.WorkloadStatePending
	case "failed":
		return ports.WorkloadStateFailed
	default:
		return ports.WorkloadStateProvisioning
	}
}

// discoverOrphanDeployments lists Kubernetes Deployments in the tenant
// namespace and returns synthetic WorkloadInstanceRecord entries for any
// Deployment that is not already tracked by the in-memory instance store.
// This lets the list/get APIs surface instances that survived a gateway
// restart even though the local store was empty.
func (api *instanceAPI) discoverOrphanDeployments(ctx context.Context, tenantID string) []ports.WorkloadInstanceRecord {
	if api.k8sClient == nil || strings.TrimSpace(tenantID) == "" {
		return nil
	}
	namespace := instanceTenantNamespace(tenantID)
	labelSelector := "ani.kubercloud.io/tenant-id=" + tenantID
	endpoint := api.k8sClient.Host() + "/apis/apps/v1/namespaces/" + url.PathEscape(namespace) + "/deployments?labelSelector=" + url.QueryEscape(labelSelector)
	body, status, err := api.k8sClient.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		log.Printf("[LIST] orphan discovery failed to list deployments in namespace %s: status=%d err=%v", namespace, status, err)
		return nil
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		log.Printf("[LIST] orphan discovery failed to decode deployment list: %v", err)
		return nil
	}
	records := make([]ports.WorkloadInstanceRecord, 0, len(list.Items))
	for _, item := range list.Items {
		depName := strings.TrimSpace(item.Metadata.Name)
		if depName == "" {
			continue
		}
		// Skip deployments that already exist in the in-memory store.
		// The store may key records by instance ID (e.g. "inst_1") rather
		// than the deployment name, so check by listing and matching Name.
		skip := false
		if storeRecords, err := api.service.List(ctx, ports.WorkloadInstanceListRequest{
			TenantID: tenantID,
			Kind:     ports.WorkloadKindGPUContainer,
		}); err == nil {
			for _, sr := range storeRecords {
				if sr.Name == depName {
					skip = true
					break
				}
			}
		}
		if skip {
			continue
		}
		obs := api.observeOrphan(ctx, tenantID, depName)
		record := ports.WorkloadInstanceRecord{
			InstanceID:   depName,
			TenantID:     tenantID,
			Name:         depName,
			Kind:         ports.WorkloadKindGPUContainer,
			Provider:     "kubernetes",
			ResourceRefs: []string{"kubernetes/Deployment/" + depName},
			Status: ports.WorkloadStatus{
				Ref: ports.WorkloadRef{
					TenantID:   tenantID,
					InstanceID: depName,
					Kind:       ports.WorkloadKindGPUContainer,
					ProviderID: "kubernetes",
				},
				State:    orphanState(obs.Phase),
				NodeName: obs.NodeName,
				Reason:   obs.Reason,
			},
			CreatedAt: obs.CreatedAt,
			UpdatedAt: time.Now().UTC(),
		}
		if obs.GPUCount > 0 {
			record.GPU = api.orphanGPUStatus(ctx, obs.NodeName, obs.GPUCount, obs.Phase)
		}
		records = append(records, record)
		log.Printf("[LIST] orphan discovery found untracked deployment %s/%s phase=%s node=%s gpu=%d", namespace, depName, obs.Phase, obs.NodeName, obs.GPUCount)
	}
	return records
}

// tryImportOrphanForLifecycle discovers orphan deployments for the tenant,
// finds the one matching instanceID, and persists it into the store so a
// subsequent lifecycle action can succeed. Returns true when the instance
// was imported and the caller should retry the lifecycle action.
func (api *instanceAPI) tryImportOrphanForLifecycle(ctx context.Context, tenantID, instanceID string) bool {
	orphans := api.discoverOrphanDeployments(ctx, tenantID)
	for _, orphan := range orphans {
		if orphan.InstanceID != instanceID && orphan.Name != instanceID {
			continue
		}
		if err := api.store.UpsertStatus(ctx, orphan); err != nil {
			log.Printf("[LIFECYCLE] failed to import orphan instance %s for tenant %s: %v", instanceID, tenantID, err)
			return false
		}
		log.Printf("[LIFECYCLE] imported orphan instance %s for tenant %s to retry lifecycle", instanceID, tenantID)
		return true
	}
	return false
}

// observeOrphan inspects a single Kubernetes Deployment and its Pods to
// extract the observation fields needed to synthesize a WorkloadInstanceRecord.
func (api *instanceAPI) observeOrphan(ctx context.Context, tenantID string, depName string) orphanObservation {
	namespace := instanceTenantNamespace(tenantID)
	obs := orphanObservation{}
	depEndpoint := api.k8sClient.Host() + "/apis/apps/v1/namespaces/" + url.PathEscape(namespace) + "/deployments/" + url.PathEscape(depName)
	body, status, err := api.k8sClient.Do(ctx, http.MethodGet, depEndpoint, "", nil)
	if err != nil {
		log.Printf("[GET] orphan observe failed to get deployment %s/%s: status=%d err=%v", namespace, depName, status, err)
		return obs
	}
	var dep struct {
		Metadata struct {
			CreationTimestamp time.Time `json:"creationTimestamp"`
		} `json:"metadata"`
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Resources struct {
							Limits map[string]any `json:"limits"`
						} `json:"resources"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
		Status struct {
			AvailableReplicas int `json:"availableReplicas"`
			Replicas          int `json:"replicas"`
			Conditions        []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		log.Printf("[GET] orphan observe failed to decode deployment %s/%s: %v", namespace, depName, err)
		return obs
	}
	obs.CreatedAt = dep.Metadata.CreationTimestamp
	if dep.Status.AvailableReplicas > 0 {
		obs.Phase = "Running"
	} else if dep.Status.Replicas > 0 {
		obs.Phase = "Provisioning"
	} else {
		obs.Phase = "Pending"
	}
	for _, condition := range dep.Status.Conditions {
		if strings.EqualFold(condition.Status, "False") {
			if condition.Reason != "" {
				obs.Reason = condition.Reason
			} else if condition.Message != "" {
				obs.Reason = condition.Message
			}
			break
		}
	}
	for _, container := range dep.Spec.Template.Spec.Containers {
		if container.Resources.Limits == nil {
			continue
		}
		for resourceName, raw := range container.Resources.Limits {
			if !strings.HasPrefix(resourceName, "nvidia.com/gpu") && resourceName != "nvidia.com/gpu" {
				continue
			}
			count := orphanGPUCount(raw)
			if count > 0 {
				obs.GPUCount += count
			}
		}
	}
	// Query Pods to discover the node name. HAMi-scheduled pods store the
	// node in spec.nodeName rather than status.nodeName.
	podEndpoint := api.k8sClient.Host() + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods?labelSelector=" + url.QueryEscape("ani.kubercloud.io/instance="+depName)
	podBody, podStatus, podErr := api.k8sClient.Do(ctx, http.MethodGet, podEndpoint, "", nil)
	if podErr != nil {
		log.Printf("[GET] orphan observe failed to list pods for %s/%s: status=%d err=%v", namespace, depName, podStatus, podErr)
		return obs
	}
	var podList struct {
		Items []struct {
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				NodeName   string `json:"nodeName"`
				Conditions []struct {
					Type    string `json:"type"`
					Status  string `json:"status"`
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(podBody, &podList); err != nil {
		log.Printf("[GET] orphan observe failed to decode pod list for %s/%s: %v", namespace, depName, err)
		return obs
	}
	// A Deployment may own multiple Pods across rollouts (e.g. a stale
	// Pending pod from a failed ReplicaSet alongside a healthy Running pod).
	// Prefer the scheduled/running pod for node name and only surface the
	// scheduling failure reason when no pod has been scheduled.
	scheduledPod := false
	for _, pod := range podList.Items {
		if pod.Spec.NodeName != "" || pod.Status.NodeName != "" {
			scheduledPod = true
			if pod.Spec.NodeName != "" {
				obs.NodeName = pod.Spec.NodeName
			} else if pod.Status.NodeName != "" {
				obs.NodeName = pod.Status.NodeName
			}
		}
	}
	if scheduledPod {
		// At least one pod is scheduled: clear any Deployment-level failure
		// reason (e.g. stale ProgressDeadlineExceeded) since the workload
		// is actually running.
		obs.Reason = ""
		return obs
	}
	// No pod scheduled yet: surface the real scheduling failure reason
	// from the PodScheduled condition (status=False). This carries the
	// scheduler's detailed message (e.g. "Unschedulable: ...") which is more
	// actionable than the Deployment's "MinimumReplicasUnavailable".
	for _, pod := range podList.Items {
		for _, cond := range pod.Status.Conditions {
			if !strings.EqualFold(cond.Type, "PodScheduled") {
				continue
			}
			if strings.EqualFold(cond.Status, "False") {
				if cond.Message != "" {
					obs.Reason = cond.Message
				} else if cond.Reason != "" {
					obs.Reason = cond.Reason
				}
				break
			}
		}
	}
	return obs
}

// orphanGPUStatus builds a GPUInstanceStatus from the GPU inventory when the
// node is known, falling back to a count-only status when inventory lookup
// fails or the inventory is not configured. phase is the Deployment phase
// (Running/Provisioning/Pending) used to produce a human-readable
// scheduling_reason for the orphan record.
func (api *instanceAPI) orphanGPUStatus(ctx context.Context, nodeName string, count int, phase string) *ports.GPUInstanceStatus {
	status := &ports.GPUInstanceStatus{Count: count}
	if strings.TrimSpace(nodeName) == "" {
		// Pod not scheduled yet: surface the pending phase as the reason.
		if phase != "" {
			status.SchedulingReason = fmt.Sprintf("%s: awaiting node scheduling", strings.ToLower(phase))
		}
		return status
	}
	if api.gpuInventory == nil {
		status.SchedulingReason = fmt.Sprintf("scheduled on node %s", nodeName)
		return status
	}
	nodeClass, err := api.gpuInventory.GetNodeClass(ctx, nodeName)
	if err != nil {
		log.Printf("[GET] orphan gpu inventory lookup failed for node %s: %v", nodeName, err)
		status.SchedulingReason = fmt.Sprintf("scheduled on node %s", nodeName)
		return status
	}
	status.Vendor = nodeClass.Vendor
	status.Model = nodeClass.Model
	resourceName := ""
	if len(nodeClass.Devices) > 0 {
		resourceName = nodeClass.Devices[0].ResourceName
		status.ResourceName = resourceName
	}
	if resourceName != "" {
		status.SchedulingReason = fmt.Sprintf("scheduled %d %s/%s GPU(s) on node %s", count, nodeClass.Vendor, nodeClass.Model, nodeName)
	} else {
		status.SchedulingReason = fmt.Sprintf("scheduled %d GPU(s) on node %s", count, nodeName)
	}
	return status
}

// orphanState maps a Kubernetes Deployment phase string to an ANI
// WorkloadState. Unknown phases default to Pending.
func orphanState(phase string) ports.WorkloadState {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "running":
		return ports.WorkloadStateRunning
	case "provisioning", "starting":
		return ports.WorkloadStateProvisioning
	case "failed":
		return ports.WorkloadStateFailed
	case "pending":
		return ports.WorkloadStatePending
	default:
		return ports.WorkloadStatePending
	}
}

// orphanGPUCount parses a Kubernetes resource quantity value for
// nvidia.com/gpu into an integer count.
func orphanGPUCount(raw any) int {
	switch value := raw.(type) {
	case float64:
		return int(value)
	case int64:
		return int(value)
	case int:
		return value
	case string:
		count, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0
		}
		return count
	default:
		return 0
	}
}

// instanceTenantNamespace returns the Kubernetes namespace that hosts instances
// for the given tenant. It mirrors the runtime adapter's tenantNamespace
// helper without importing it from the router package.
func instanceTenantNamespace(tenantID string) string {
	return "ani-tenant-" + strings.ReplaceAll(tenantID, "_", "-")
}

func (api *instanceAPI) get(ctx context.Context, c *app.RequestContext) {
	tenantID := instanceTenantID(c)
	instanceID := c.Param("instance_id")
	record, err := api.service.Get(ctx, ports.WorkloadInstanceGetRequest{
		TenantID:   tenantID,
		InstanceID: instanceID,
	})
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			if orphans := api.discoverOrphanDeployments(ctx, tenantID); len(orphans) > 0 {
				for _, orphan := range orphans {
					if orphan.InstanceID == instanceID {
						log.Printf("[GET] instance %s not in store, served from orphan discovery (tenant=%s)", instanceID, tenantID)
						c.JSON(http.StatusOK, api.instanceResponseFromRecord(orphan))
						return
					}
				}
			}
		}
		writeInstanceError(c, http.StatusNotFound, "INSTANCE_NOT_FOUND", err.Error())
		return
	}
	c.JSON(http.StatusOK, api.instanceResponseFromRecord(record))
}

func (api *instanceAPI) list(ctx context.Context, c *app.RequestContext) {
	tenantID := instanceTenantID(c)
	kind := ports.WorkloadKind(c.Query("kind"))
	listReq, err := instanceListRequestFromQuery(c, tenantID, kind)
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	// Refresh store-backed records against the live Kubernetes cluster so the
	// The instance API reflects real Deployment/Pod status instead of stale
	// "provisioning" captured at create time. This is an on-demand refresh
	// triggered by the list request; there is no background reconcile loop
	// data from the in-memory store.
	api.refreshStoreStatuses(ctx, tenantID, kind)
	records, err := api.service.List(ctx, listReq)
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "INSTANCE_LIST_FAILED", err.Error())
		return
	}
	// Merge orphan deployments discovered from the live Kubernetes cluster.
	// Skip orphans whose InstanceID or Name is already present in the
	// store-backed result set to avoid duplicates (store records use
	// instance IDs like "inst_1" while orphans use deployment names).
	existing := make(map[string]struct{}, len(records)*2)
	for _, record := range records {
		existing[record.InstanceID] = struct{}{}
		existing[record.Name] = struct{}{}
	}
	orphans := api.discoverOrphanDeployments(ctx, tenantID)
	for _, orphan := range orphans {
		if _, found := existing[orphan.InstanceID]; found {
			continue
		}
		if kind != "" && orphan.Kind != kind {
			continue
		}
		records = append(records, orphan)
		existing[orphan.InstanceID] = struct{}{}
	}
	total := len(records)
	records, nextCursor, err := paginateInstanceRecords(records, queryInt(c, "limit", 0), c.Query("cursor"))
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	items := make([]instanceResponse, 0, len(records))
	for _, record := range records {
		items = append(items, api.instanceResponseFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": total, "next_cursor": optionalString(nextCursor)})
}

func instanceListRequestFromQuery(c *app.RequestContext, tenantID string, kind ports.WorkloadKind) (ports.WorkloadInstanceListRequest, error) {
	createdAfter, err := optionalRFC3339Query(c, "created_after")
	if err != nil {
		return ports.WorkloadInstanceListRequest{}, err
	}
	createdBefore, err := optionalRFC3339Query(c, "created_before")
	if err != nil {
		return ports.WorkloadInstanceListRequest{}, err
	}
	return ports.WorkloadInstanceListRequest{
		TenantID:        tenantID,
		Kind:            kind,
		State:           ports.WorkloadState(c.Query("state")),
		Keyword:         c.Query("keyword"),
		CreatedAfter:    createdAfter,
		CreatedBefore:   createdBefore,
		SpecID:          c.Query("spec_id"),
		ImageID:         c.Query("image_id"),
		NodeName:        c.Query("node_name"),
		RolloutStatus:   c.Query("rollout_status"),
		GPUModel:        c.Query("gpu_model"),
		QueueName:       c.Query("queue_name"),
		SchedulingState: c.Query("scheduling_state"),
		TemplateID:      c.Query("template_id"),
		SessionState:    c.Query("session_state"),
		Sort:            c.Query("sort"),
	}, nil
}

func optionalRFC3339Query(c *app.RequestContext, name string) (time.Time, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339", name)
	}
	return value, nil
}

func paginateInstanceRecords(records []ports.WorkloadInstanceRecord, limit int, cursor string) ([]ports.WorkloadInstanceRecord, string, error) {
	if limit < 0 || limit > 100 {
		return nil, "", fmt.Errorf("limit must be between 1 and 100")
	}
	if limit == 0 {
		return records, "", nil
	}
	start := 0
	if strings.TrimSpace(cursor) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(cursor))
		if err != nil || parsed < 0 {
			return nil, "", fmt.Errorf("cursor is invalid")
		}
		start = parsed
	}
	if start > len(records) {
		start = len(records)
	}
	end := start + limit
	if end > len(records) {
		end = len(records)
	}
	nextCursor := ""
	if end < len(records) {
		nextCursor = strconv.Itoa(end)
	}
	return records[start:end], nextCursor, nil
}

func (api *instanceAPI) lifecycle(ctx context.Context, c *app.RequestContext) {
	var req instanceLifecycleRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid lifecycle request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	lifecycle, err := workloadLifecycleRequestFromHTTP(req, instanceTenantID(c), c.Param("instance_id"), instanceUserID(c))
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	var (
		record ports.WorkloadInstanceRecord
	)
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "start":
		record, err = api.service.Start(ctx, lifecycle)
	case "stop":
		record, err = api.service.Stop(ctx, lifecycle)
	case "restart":
		record, err = api.service.Restart(ctx, lifecycle)
	case "resize":
		record, err = api.service.Resize(ctx, ports.WorkloadInstanceResizeRequest{
			TenantID:        lifecycle.TenantID,
			InstanceID:      lifecycle.InstanceID,
			IdempotencyKey:  lifecycle.IdempotencyKey,
			Resources:       lifecycle.Resources,
			UserID:          lifecycle.UserID,
			PermissionProof: lifecycle.PermissionProof,
			RequestedAt:     lifecycle.RequestedAt,
		})
	case "delete":
		record, err = api.service.Delete(ctx, lifecycle)
	case "snapshot":
		record, err = api.service.Snapshot(ctx, lifecycle)
	case "attach_volume":
		record, err = api.service.AttachVolume(ctx, lifecycle)
	case "detach_volume":
		record, err = api.service.DetachVolume(ctx, lifecycle)
	case "rollback":
		record, err = api.service.Rollback(ctx, lifecycle)
	default:
		record, err = api.service.ApplyLifecycle(ctx, lifecycle)
	}
	// If the instance is not in the local store but exists as an orphan
	// deployment in the live Kubernetes cluster (e.g. after a gateway
	// restart), import it into the store and retry the lifecycle action.
	if err != nil && errors.Is(err, ports.ErrNotFound) && api.k8sClient != nil {
		if imported := api.tryImportOrphanForLifecycle(ctx, lifecycle.TenantID, lifecycle.InstanceID); imported {
			switch strings.ToLower(strings.TrimSpace(req.Action)) {
			case "start":
				record, err = api.service.Start(ctx, lifecycle)
			case "stop":
				record, err = api.service.Stop(ctx, lifecycle)
			case "restart":
				record, err = api.service.Restart(ctx, lifecycle)
			case "resize":
				record, err = api.service.Resize(ctx, ports.WorkloadInstanceResizeRequest{
					TenantID:        lifecycle.TenantID,
					InstanceID:      lifecycle.InstanceID,
					IdempotencyKey:  lifecycle.IdempotencyKey,
					Resources:       lifecycle.Resources,
					UserID:          lifecycle.UserID,
					PermissionProof: lifecycle.PermissionProof,
					RequestedAt:     lifecycle.RequestedAt,
				})
			case "delete":
				record, err = api.service.Delete(ctx, lifecycle)
			case "snapshot":
				record, err = api.service.Snapshot(ctx, lifecycle)
			case "attach_volume":
				record, err = api.service.AttachVolume(ctx, lifecycle)
			case "detach_volume":
				record, err = api.service.DetachVolume(ctx, lifecycle)
			case "rollback":
				record, err = api.service.Rollback(ctx, lifecycle)
			default:
				record, err = api.service.ApplyLifecycle(ctx, lifecycle)
			}
		}
	}
	if err != nil {
		writeInstanceError(c, instanceLifecycleErrorStatus(err), instanceLifecycleErrorCode(err), err.Error())
		return
	}
	// Task write point for the four instance lifecycle actions with task
	// records: placed after the error check (covering both the first-switch
	// success and the orphan-import retry success) and before the response,
	// so every successful path is audited exactly once.
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "start", "stop", "restart", "delete":
		action := strings.ToLower(strings.TrimSpace(req.Action))
		task := instanceProgressTask("instance."+action, record.InstanceID, string(record.Kind), record.Name, string(record.Status.State), req.IdempotencyKey)
		task.Result["action"] = action
		api.writeAuditTask(ctx, instanceTenantID(c), task)
	}
	c.JSON(http.StatusOK, instanceLifecycleResponse{
		Instance:    api.instanceResponseFromRecord(record),
		OperationID: record.OperationID,
	})
}

func workloadLifecycleRequestFromHTTP(request instanceLifecycleRequest, tenantID, instanceID, userID string) (ports.WorkloadInstanceLifecycleRequest, error) {
	action := ports.WorkloadLifecycleAction(strings.ToLower(strings.TrimSpace(request.Action)))
	if action == "" {
		return ports.WorkloadInstanceLifecycleRequest{}, fmt.Errorf("action is required")
	}
	duration := time.Duration(0)
	if strings.TrimSpace(request.Duration) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(request.Duration))
		if err != nil || parsed <= 0 {
			return ports.WorkloadInstanceLifecycleRequest{}, fmt.Errorf("duration must be a positive duration")
		}
		duration = parsed
	}
	resources := ports.WorkloadResourceRequest{}
	if action == ports.WorkloadLifecycleResize {
		resources = ports.WorkloadResourceRequest{CPU: firstNonEmpty(request.CPU, "4"), Memory: firstNonEmpty(request.Memory, "8Gi")}
	}
	return ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:   request.IdempotencyKey,
		TenantID:         tenantID,
		InstanceID:       instanceID,
		Action:           action,
		Resources:        resources,
		SnapshotName:     request.SnapshotName,
		SnapshotID:       request.SnapshotID,
		IncludeDataDisks: request.IncludeDataDisks,
		VolumeID:         request.VolumeID,
		FilesystemID:     request.FilesystemID,
		MountPath:        request.MountPath,
		ReadOnly:         request.ReadOnly,
		Revision:         request.Revision,
		Replicas:         request.Replicas,
		ImageID:          request.ImageID,
		Strategy:         request.Strategy,
		SecretID:         request.SecretID,
		BindingType:      request.BindingType,
		EnvName:          request.EnvName,
		SecurityGroupIDs: append([]string(nil), request.SecurityGroupIDs...),
		Enabled:          request.Enabled,
		Duration:         duration,
		UserID:           userID,
		PermissionProof:  "instance:lifecycle",
		RequestedAt:      time.Now().UTC(),
	}, nil
}

func (api *instanceAPI) listOperations(ctx context.Context, c *app.RequestContext) {
	result, err := api.operations.ListOperations(ctx, ports.WorkloadOperationListRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: c.Param("instance_id"),
		Limit:      queryInt(c, "limit", 20),
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "INSTANCE_OPERATIONS_FAILED", err.Error())
		return
	}
	items := make([]instanceOperationResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, operationResponseFromRecord(item))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": result.NextCursor})
}

func (api *instanceAPI) listLogs(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	result, err := api.observability.ListLogs(ctx, ports.InstanceObservationListRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: api.observabilityTargetID(record),
		Limit:      queryInt(c, "limit", 100),
		Cursor:     c.Query("cursor"),
		Level:      c.Query("level"),
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, instanceLogListFromResult(result))
}

func (api *instanceAPI) listEvents(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	result, err := api.observability.ListEvents(ctx, ports.InstanceObservationListRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: api.observabilityTargetID(record),
		Limit:      queryInt(c, "limit", 50),
		Cursor:     c.Query("cursor"),
		Type:       c.Query("type"),
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, instanceEventListFromResult(result))
}

func (api *instanceAPI) getMetrics(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	result, err := api.observability.GetMetrics(ctx, ports.InstanceObservationGetRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: api.observabilityTargetID(record),
		// 透传 record.Kind，使 adapter 的 GPU/VM 分支在生产路径下能正确触发：
		// gpu_container 走 DCGM 分支填充 GPU 字段，其他 kind 的 GPU 字段保持 nil。
		// 修复前 handler 未传 Kind，导致 GPU 分支恒不触发，GPU 指标在 Console 中始终为 null。
		Kind: record.Kind,
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, instanceMetricsFromRecord(result))
}

func (api *instanceAPI) createExecSession(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	var req createExecSessionRequest
	if len(c.Request.Body()) > 0 {
		if err := c.BindJSON(&req); err != nil {
			writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid exec session request")
			return
		}
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	tty := true
	if req.TTY != nil {
		tty = *req.TTY
	}
	result, err := api.observability.CreateExecSession(ctx, ports.InstanceExecSessionCreateRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     api.observabilityTargetID(record),
		IdempotencyKey: req.IdempotencyKey,
		Container:      req.Container,
		Command:        req.Command,
		TTY:            tty,
		Rows:           maxInt(req.Rows, 24),
		Cols:           maxInt(req.Cols, 80),
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, instanceExecSessionFromRecord(result))
}

func (api *instanceAPI) createConsoleSession(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindVM {
		writeInstanceError(c, http.StatusBadRequest, "UNSUPPORTED", "console session is only available for vm instances")
		return
	}
	if record.Status.State != ports.WorkloadStateRunning {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "instance must be running to open a console session")
		return
	}
	var req instanceConsoleRequest
	if len(c.Request.Body()) > 0 {
		if err := c.BindJSON(&req); err != nil {
			writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid console session request")
			return
		}
	}
	protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
	if protocol == "" {
		protocol = "vnc"
	}
	if !isValidConsoleProtocol(protocol) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "protocol must be one of console, vnc, novnc, serial")
		return
	}
	result, err := api.observability.CreateConsoleSession(ctx, ports.InstanceConsoleSessionCreateRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: api.observabilityTargetID(record),
		Protocol:   protocol,
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, instanceConsoleSessionFromRecord(result))
}

func (api *instanceAPI) createSandboxToken(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox token is only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	var req createSandboxTokenRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid sandbox token request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	expiresIn := time.Duration(0)
	if strings.TrimSpace(req.ExpiresIn) != "" {
		expiresIn, err = time.ParseDuration(strings.TrimSpace(req.ExpiresIn))
		if err != nil || expiresIn <= 0 {
			writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "expires_in must be a positive duration")
			return
		}
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	result, err := api.sandboxRuntime.CreateToken(ctx, ports.SandboxTokenRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     record.InstanceID,
		Execution:      execution,
		IdempotencyKey: req.IdempotencyKey,
		ExpiresIn:      expiresIn,
		Scopes:         append([]string(nil), req.Scopes...),
		RequestedAt:    time.Now().UTC(),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, sandboxTokenResponse{
		Token:     result.Token,
		ExpiresAt: result.ExpiresAt.Format(time.RFC3339),
		Scopes:    result.Scopes,
	})
}

func (api *instanceAPI) createSandboxPort(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox port is only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	var req createSandboxPortRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid sandbox port request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	result, err := api.sandboxRuntime.CreatePort(ctx, ports.SandboxPortRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     record.InstanceID,
		Execution:      execution,
		IdempotencyKey: req.IdempotencyKey,
		Port:           req.Port,
		Name:           req.Name,
		Protocol:       req.Protocol,
		RequestedAt:    time.Now().UTC(),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	if err := api.persistSandboxPort(ctx, record, result, false); err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "SANDBOX_STATUS_PERSIST_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusCreated, sandboxPortResponseFromResult(result))
}

func (api *instanceAPI) deleteSandboxPort(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox port is only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	idempotencyKey := strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
	if !hasIdempotencyKey(idempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "Idempotency-Key header is required")
		return
	}
	port, err := strconv.Atoi(strings.TrimSpace(c.Param("port")))
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "port must be an integer")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	result, err := api.sandboxRuntime.DeletePort(ctx, ports.SandboxPortDeleteRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     record.InstanceID,
		Execution:      execution,
		IdempotencyKey: idempotencyKey,
		Port:           port,
		RequestedAt:    time.Now().UTC(),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	if err := api.persistSandboxPort(ctx, record, result, true); err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "SANDBOX_STATUS_PERSIST_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, sandboxPortResponseFromResult(result))
}

func sandboxPortResponseFromResult(result ports.SandboxPortResult) sandboxPortResponse {
	response := sandboxPortResponse{
		Port:       result.Port,
		Name:       result.Name,
		Protocol:   result.Protocol,
		Status:     result.Status,
		PreviewURL: result.PreviewURL,
	}
	if !result.ExpiresAt.IsZero() {
		response.ExpiresAt = result.ExpiresAt.Format(time.RFC3339)
	}
	return response
}

func (api *instanceAPI) persistSandboxPort(ctx context.Context, record ports.WorkloadInstanceRecord, result ports.SandboxPortResult, remove bool) error {
	if api.store == nil || record.Sandbox == nil {
		return fmt.Errorf("%w: sandbox persistence is not configured", ports.ErrFailedPrecondition)
	}
	sandbox := *record.Sandbox
	items := make([]ports.SandboxPortResult, 0, len(sandbox.Ports)+1)
	for _, item := range sandbox.Ports {
		if item.Port != result.Port {
			items = append(items, item)
		}
	}
	if !remove {
		items = append(items, result)
	}
	sandbox.Ports = items
	record.Sandbox = &sandbox
	record.UpdatedAt = time.Now().UTC()
	return api.store.UpsertStatus(ctx, record)
}

func (api *instanceAPI) listSandboxFiles(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox files are only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	result, err := api.sandboxRuntime.ListFiles(ctx, ports.SandboxFileListRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: record.InstanceID,
		Execution:  execution,
		Path:       c.Query("path"),
		Limit:      queryInt(c, "limit", 100),
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	items := make([]sandboxFileResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, sandboxFileResponseFromResult(item))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": result.Total, "next_cursor": result.NextCursor})
}

func (api *instanceAPI) writeSandboxFile(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox files are only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	var req writeSandboxFileRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid sandbox file request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	result, err := api.sandboxRuntime.WriteFile(ctx, ports.SandboxFileWriteRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     record.InstanceID,
		Execution:      execution,
		IdempotencyKey: req.IdempotencyKey,
		Path:           req.Path,
		ContentBase64:  req.ContentBase64,
		UploadID:       req.UploadID,
		Overwrite:      req.Overwrite,
		RequestedAt:    time.Now().UTC(),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, sandboxFileResponseFromResult(result))
}

func (api *instanceAPI) deleteSandboxFile(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox files are only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	idempotencyKey := strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
	if !hasIdempotencyKey(idempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "Idempotency-Key header is required")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	if err := api.sandboxRuntime.DeleteFile(ctx, ports.SandboxFileDeleteRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     record.InstanceID,
		Execution:      execution,
		IdempotencyKey: idempotencyKey,
		Path:           c.Query("path"),
		RequestedAt:    time.Now().UTC(),
	}); err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func sandboxFileResponseFromResult(result ports.SandboxFileResult) sandboxFileResponse {
	return sandboxFileResponse{
		Path:      result.Path,
		Kind:      result.Kind,
		SizeBytes: result.SizeBytes,
		UpdatedAt: result.UpdatedAt.Format(time.RFC3339),
	}
}

func (api *instanceAPI) listSandboxCheckpoints(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox checkpoints are only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	result, err := api.sandboxRuntime.ListCheckpoints(ctx, ports.SandboxCheckpointListRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: record.InstanceID,
		Execution:  execution,
		Limit:      queryInt(c, "limit", 50),
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	items := make([]sandboxCheckpointResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, sandboxCheckpointResponseFromResult(item))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": result.Total, "next_cursor": result.NextCursor})
}

func (api *instanceAPI) createSandboxCheckpoint(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox checkpoints are only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	var req createSandboxCheckpointRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid sandbox checkpoint request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	result, err := api.sandboxRuntime.CreateCheckpoint(ctx, ports.SandboxCheckpointCreateRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     record.InstanceID,
		Execution:      execution,
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		KeepMemory:     req.KeepMemory,
		RequestedAt:    time.Now().UTC(),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	task := storageCompletedTask("sandbox.checkpoint.create", "sandbox_checkpoint", req.IdempotencyKey, map[string]any{"checkpoint": sandboxCheckpointResponseFromResult(result)}, result.CreatedAt)
	storageWriteAcceptedTask(ctx, c, api.tasks, task)
}

func (api *instanceAPI) restoreSandboxCheckpoint(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox checkpoints are only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	var req sandboxCheckpointActionRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid sandbox checkpoint restore request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	result, err := api.sandboxRuntime.RestoreCheckpoint(ctx, ports.SandboxCheckpointRestoreRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     record.InstanceID,
		Execution:      execution,
		CheckpointID:   c.Param("checkpoint_id"),
		IdempotencyKey: req.IdempotencyKey,
		RequestedAt:    time.Now().UTC(),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	task := storageCompletedTask("sandbox.checkpoint.restore", "sandbox_checkpoint", req.IdempotencyKey, map[string]any{"checkpoint": sandboxCheckpointResponseFromResult(result)}, time.Now().UTC())
	storageWriteAcceptedTask(ctx, c, api.tasks, task)
}

func (api *instanceAPI) cloneSandboxCheckpoint(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox checkpoints are only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	var req cloneSandboxCheckpointRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid sandbox checkpoint clone request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	checkpoint, err := api.sandboxRuntime.CloneCheckpoint(ctx, ports.SandboxCheckpointCloneRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     record.InstanceID,
		Execution:      execution,
		CheckpointID:   c.Param("checkpoint_id"),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		RequestedAt:    time.Now().UTC(),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	config := ports.SandboxConfig{}
	if record.Sandbox != nil {
		config = record.Sandbox.Config
	}
	result, err := api.service.Create(ctx, ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: req.IdempotencyKey,
		Spec: ports.WorkloadSpec{
			TenantID: instanceTenantID(c), Name: checkpoint.Name, Kind: ports.WorkloadKindSandbox,
			Image: record.Image.Ref, Sandbox: &config,
			SandboxCheckpointSourceRef: checkpoint.ProviderRef,
			Lifecycle:                  ports.InstanceLifecyclePolicy{AutoStart: true},
		},
		UserID:          instanceUserID(c),
		PermissionProof: "instance:sandbox:checkpoint:clone",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "INSTANCE_CREATE_FAILED", err.Error())
		return
	}
	cloned, err := api.service.Get(ctx, ports.WorkloadInstanceGetRequest{TenantID: result.Ref.TenantID, InstanceID: result.Ref.InstanceID})
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "INSTANCE_LOOKUP_FAILED", err.Error())
		return
	}
	status := http.StatusCreated
	if result.IdempotentReplay {
		status = http.StatusConflict
	}
	c.JSON(status, instanceCreateResponse{
		Instance:      api.instanceResponseFromRecord(cloned),
		OperationID:   result.OperationID,
		AuditID:       result.AuditID,
		Manifests:     manifestResponses(result.Manifests),
		Timeline:      instanceTimeline(result),
		RuntimeNotice: "instance profile uses the M1 service; configure the Kubernetes provider for live cluster execution.",
	})
}

func (api *instanceAPI) createSandboxCodeRun(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	if record.Kind != ports.WorkloadKindSandbox {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox code runs are only available for sandbox instances")
		return
	}
	if api.sandboxRuntime == nil {
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "sandbox runtime is not configured")
		return
	}
	var req createSandboxCodeRunRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid sandbox code run request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	execution, err := sandboxExecutionForRecord(record)
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	result, err := api.sandboxRuntime.CreateCodeRun(ctx, ports.SandboxCodeRunRequest{
		TenantID:       instanceTenantID(c),
		InstanceID:     record.InstanceID,
		Execution:      execution,
		IdempotencyKey: req.IdempotencyKey,
		Language:       req.Language,
		Code:           req.Code,
		TimeoutSeconds: req.TimeoutSeconds,
		Stdin:          req.Stdin,
		RequestedAt:    time.Now().UTC(),
	})
	if err != nil {
		writeSandboxRuntimeError(c, err)
		return
	}
	codeRun := map[string]any{
		"id":         result.ID,
		"status":     result.Status,
		"language":   result.Language,
		"created_at": result.CreatedAt.Format(time.RFC3339),
		"truncated":  result.Truncated,
	}
	if result.Stdout != "" {
		codeRun["stdout"] = result.Stdout
	}
	if result.Stderr != "" {
		codeRun["stderr"] = result.Stderr
	}
	if result.ExitCode != nil {
		codeRun["exit_code"] = *result.ExitCode
	}
	if result.CompletedAt != nil {
		codeRun["completed_at"] = result.CompletedAt.Format(time.RFC3339)
	}
	task := storageCompletedTask("sandbox.code_run.create", "sandbox_code_run", req.IdempotencyKey, map[string]any{
		"code_run": codeRun,
	}, result.CreatedAt)
	storageWriteAcceptedTask(ctx, c, api.tasks, task)
}

func sandboxCheckpointResponseFromResult(result ports.SandboxCheckpointResult) sandboxCheckpointResponse {
	return sandboxCheckpointResponse{
		ID:         result.ID,
		Name:       result.Name,
		Status:     result.Status,
		KeepMemory: result.KeepMemory,
		CreatedAt:  result.CreatedAt.Format(time.RFC3339),
		SizeBytes:  result.SizeBytes,
		Reason:     result.Reason,
	}
}

func (api *instanceAPI) listSecurityEvents(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	result, err := api.observability.ListSecurityEvents(ctx, ports.InstanceObservationListRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: api.observabilityTargetID(record),
		Limit:      queryInt(c, "limit", 50),
		Cursor:     c.Query("cursor"),
		Severity:   c.Query("severity"),
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, instanceSecurityEventListFromResult(result))
}

func (api *instanceAPI) getOperation(ctx context.Context, c *app.RequestContext) {
	record, err := api.operations.GetOperation(ctx, instanceTenantID(c), c.Param("operation_id"))
	if err != nil {
		writeInstanceError(c, http.StatusNotFound, "INSTANCE_OPERATION_NOT_FOUND", err.Error())
		return
	}
	c.JSON(http.StatusOK, operationResponseFromRecord(record))
}

func (api *instanceAPI) ops(ctx context.Context, c *app.RequestContext) {
	action := ports.WorkloadInstanceOpsAction(c.Param("action"))
	result, err := api.service.Ops(ctx, ports.WorkloadInstanceOpsRequest{
		TenantID:        instanceTenantID(c),
		InstanceID:      c.Param("instance_id"),
		Action:          action,
		ContainerName:   "main",
		Command:         []string{"sh", "-lc", "echo ani-demo"},
		UserID:          instanceUserID(c),
		PermissionProof: "demo:instance:ops",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "INSTANCE_OPS_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (api *instanceAPI) console(ctx context.Context, c *app.RequestContext) {
	var req instanceConsoleRequest
	if len(c.Request.Body()) > 0 {
		if err := c.BindJSON(&req); err != nil {
			writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid console request")
			return
		}
	}
	action := consoleAction(req.Protocol)
	result, err := api.service.Ops(ctx, ports.WorkloadInstanceOpsRequest{
		TenantID:        instanceTenantID(c),
		InstanceID:      c.Param("instance_id"),
		Action:          action,
		Protocol:        firstNonEmpty(req.Protocol, string(action)),
		UserID:          instanceUserID(c),
		PermissionProof: "demo:instance:console",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "INSTANCE_CONSOLE_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (api *instanceAPI) consoleExec(ctx context.Context, c *app.RequestContext) {
	var req shellExecRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid shell exec request")
		return
	}
	record, err := api.service.Get(ctx, ports.WorkloadInstanceGetRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: c.Param("instance_id"),
	})
	if err != nil {
		writeInstanceError(c, http.StatusNotFound, "INSTANCE_NOT_FOUND", err.Error())
		return
	}
	if record.Kind != ports.WorkloadKindVM {
		writeInstanceError(c, http.StatusBadRequest, "INSTANCE_CONSOLE_UNSUPPORTED", "real shell console is only available for vm demo instances")
		return
	}
	if record.Status.State != ports.WorkloadStateRunning {
		writeInstanceError(c, http.StatusConflict, "INSTANCE_NOT_RUNNING", "vm console requires running instance")
		return
	}
	result, err := runInstanceShellCommand(ctx, record, req.Command)
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "SHELL_EXEC_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (api *instanceAPI) instanceForObservation(ctx context.Context, c *app.RequestContext) (ports.WorkloadInstanceRecord, error) {
	return api.service.Get(ctx, ports.WorkloadInstanceGetRequest{
		TenantID:   instanceTenantID(c),
		InstanceID: c.Param("instance_id"),
	})
}

func (api *instanceAPI) observabilityTargetID(record ports.WorkloadInstanceRecord) string {
	if api.observabilityUsesInstanceName && strings.TrimSpace(record.Name) != "" {
		return record.Name
	}
	return record.InstanceID
}

func consoleAction(protocol string) ports.WorkloadInstanceOpsAction {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "vnc", "novnc":
		return ports.WorkloadInstanceOpsVMVNC
	case "serial", "serial-console":
		return ports.WorkloadInstanceOpsVMSerial
	default:
		return ports.WorkloadInstanceOpsVMConsole
	}
}

func runInstanceShellCommand(ctx context.Context, record ports.WorkloadInstanceRecord, command string) (shellExecResponse, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return shellExecResponse{}, fmt.Errorf("%w: command is required", ports.ErrInvalid)
	}
	if len(command) > 500 {
		return shellExecResponse{}, fmt.Errorf("%w: command is too long for demo shell", ports.ErrInvalid)
	}
	if blockedInstanceShellCommand(command) {
		return shellExecResponse{}, fmt.Errorf("%w: command is blocked by demo shell guardrail", ports.ErrUnsupported)
	}
	cwd, err := instanceShellCWD(record)
	if err != nil {
		return shellExecResponse{}, err
	}
	execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	shell := firstNonEmpty(os.Getenv("ANI_DEMO_SHELL"), "/bin/sh")
	cmd := exec.CommandContext(execCtx, shell, "-lc", command)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"ANI_DEMO_VM_NAME="+record.Name,
		"ANI_DEMO_INSTANCE_ID="+record.InstanceID,
		"ANI_DEMO_TENANT_ID="+record.TenantID,
		"PS1=root@"+record.Name+":~# ",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	output := strings.TrimRight(stdout.String()+stderr.String(), "\n")
	if len(output) > 16000 {
		output = output[:16000] + "\n... output truncated ..."
	}
	return shellExecResponse{
		Command:  command,
		Output:   output,
		ExitCode: exitCode,
		CWD:      cwd,
	}, nil
}

func instanceShellCWD(record ports.WorkloadInstanceRecord) (string, error) {
	root := filepath.Join(os.TempDir(), "ani-demo-vms", sanitizePathPart(record.TenantID), sanitizePathPart(record.InstanceID))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	readme := filepath.Join(root, "README.txt")
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		content := "ANI demo VM shell workspace\ninstance=" + record.Name + "\nprovider=" + record.Provider + "\n"
		if writeErr := os.WriteFile(readme, []byte(content), 0o600); writeErr != nil {
			return "", writeErr
		}
	}
	return root, nil
}

func blockedInstanceShellCommand(command string) bool {
	normalized := strings.ToLower(command)
	blocked := []string{
		"rm -rf /",
		"mkfs",
		"shutdown",
		"reboot",
		"halt",
		":(){",
		"dd if=",
		"chmod -r",
		"chown -r",
	}
	for _, token := range blocked {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", "..", "_", ":", "_")
	return replacer.Replace(value)
}

func instanceSpecFromRequest(req createInstanceRequest, tenantID string) (ports.WorkloadSpec, error) {
	kind, err := instanceKindFromRequest(req)
	if err != nil {
		return ports.WorkloadSpec{}, err
	}
	if kind == "" {
		kind = ports.WorkloadKindVM
	}
	resolved, err := resolveCreateInstanceFields(req, kind)
	if err != nil {
		return ports.WorkloadSpec{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "demo-" + string(kind)
	}
	autoStart := true
	if req.AutoStart != nil {
		autoStart = *req.AutoStart
	}
	spec := ports.WorkloadSpec{
		TenantID:    tenantID,
		Name:        name,
		Kind:        kind,
		Description: req.Description,
		Image:       firstNonEmpty(req.ImageRef, req.Image, "docker.io/nvidia/cuda:12.4.1-base-ubuntu22.04"),
		ImageID:     strings.TrimSpace(req.ImageID),
		ImageRef:    strings.TrimSpace(req.ImageRef),
		Resources: ports.WorkloadResourceRequest{
			CPU:    firstNonEmpty(req.CPU, "2"),
			Memory: firstNonEmpty(req.Memory, "4Gi"),
		},
		Network: defaultInstanceNetworkPolicy(),
		Storage: []ports.WorkloadStorageAttachment{
			{Name: name + "-root", Kind: ports.StorageAttachmentRootDisk, SizeGiB: 40, SourceRef: firstNonEmpty(resolved.BootImage, "images/ubuntu-22.04.qcow2"), Required: true},
		},
		Lifecycle: ports.InstanceLifecyclePolicy{AutoStart: autoStart, TerminationProtection: req.TerminationProtection},
		Labels:    cloneStringMap(req.Labels),
		Annotations: map[string]string{
			"ani.io/demo-description": req.Description,
		},
		SecretBindings: secretBindingsFromRequest(req.SecretBindings),
	}
	if len(spec.Labels) == 0 {
		spec.Labels = map[string]string{}
	}
	// Top-level network_config acts as the fallback default; kind-specific
	// *_config.network overrides it (v1.yaml: network_config priority is
	// *_config.network > top-level network_config).
	if req.NetworkConfig != nil {
		spec.Network = networkPolicyFromRequest(req.NetworkConfig, spec.Network)
	}
	if req.ContainerConfig != nil {
		spec.Network = networkPolicyFromRequest(req.ContainerConfig.Network, spec.Network)
	}
	if req.GPUContainerConfig != nil {
		spec.Network = networkPolicyFromRequest(req.GPUContainerConfig.Network, spec.Network)
	}
	if req.VMConfig != nil {
		spec.Network = networkPolicyFromRequest(req.VMConfig.Network, spec.Network)
	}
	switch kind {
	case ports.WorkloadKindVM:
		vmRequest := req.VMConfig
		spec.VM = &ports.VMInstanceSpec{
			BootImage:    firstNonEmpty(resolved.BootImage, "images/ubuntu-22.04.qcow2"),
			SSHUsername:  firstNonEmpty(resolved.SSHUsername, "ubuntu"),
			SSHKeySecret: resolved.SSHKeyRef,
			MachineType:  "q35",
			RootDisk:     spec.Storage[0],
		}
		if vmRequest != nil {
			spec.VM.PasswordSecret = vmRequest.PasswordSecretRef
			spec.VM.CloudInitSecret = vmRequest.CloudInitSecret
			spec.VM.UserData = vmRequest.UserData
			spec.VM.OSType = vmRequest.OSType
			spec.VM.Firmware = vmRequest.Firmware
			spec.VM.MachineType = firstNonEmpty(vmRequest.MachineType, spec.VM.MachineType)
			spec.VM.SystemDisk = diskSpecFromRequest(vmRequest.SystemDisk)
			spec.VM.DataDiskSpecs = diskSpecsFromRequest(vmRequest.DataDisks)
			spec.VM.FilesystemMounts = filesystemMountsFromRequest(vmRequest.FilesystemMounts)
			if spec.VM.SystemDisk != nil {
				spec.VM.RootDisk = storageAttachmentFromDisk(*spec.VM.SystemDisk, ports.StorageAttachmentRootDisk)
				spec.Storage[0] = spec.VM.RootDisk
			}
		}
	case ports.WorkloadKindContainer:
		spec.Storage = nil
		spec.Container = containerSpecFromRequest(req.ContainerConfig, resolved.Replicas)
		spec.Storage = storageAttachmentsFromContainer(spec.Container)
	case ports.WorkloadKindGPUContainer:
		spec.Storage = nil
		spec.Container = containerSpecFromRequest(nil, resolved.Replicas)
		if req.GPUContainerConfig != nil {
			spec.Container = containerSpecFromGPURequest(req.GPUContainerConfig, resolved.Replicas)
		}
		spec.Storage = storageAttachmentsFromContainer(spec.Container)
		if len(spec.Command) == 0 {
			spec.Command = []string{"sleep", "infinity"}
		}
		spec.Resources.GPU = ports.GPUSchedulingRequest{
			TenantID:         tenantID,
			WorkloadID:       name,
			PreferredVendors: []ports.GPUVendor{ports.GPUVendor(firstNonEmpty(resolved.GPUVendor, "nvidia"))},
			PreferredModels:  []string{firstNonEmpty(resolved.GPUModel, "A100")},
			RequiredCount:    maxInt(resolved.GPUCount, 1),
			QueueName:        resolved.QueueName,
			WorkloadClass:    ports.WorkloadClass(firstNonEmpty(resolved.WorkloadClass, string(ports.WorkloadClassInference))),
		}
		if resolved.GPUSpecID != "" {
			spec.GPUSpec = &ports.InstanceGPUSpecReference{SpecID: resolved.GPUSpecID, GPUType: resolved.GPUModel, Shares: resolved.GPUShares, MBPerShare: resolved.GPUMBPerShare}
			// spec_id carries full GPU type info; clear legacy selectors
			// to avoid conflict validation in instance_service.go.
			spec.Resources.GPU.PreferredVendors = nil
			spec.Resources.GPU.PreferredModels = nil
			spec.Resources.GPU.RequiredCount = 1
		}
	case ports.WorkloadKindSandbox:
		sandboxConfig, err := sandboxConfigFromRequest(resolved.SandboxConfig)
		if err != nil {
			return ports.WorkloadSpec{}, err
		}
		spec.Storage = nil
		spec.RuntimeClassName = sandboxConfig.RuntimeClass
		spec.Sandbox = &sandboxConfig
		spec.Annotations["ani.kubercloud.io/sandbox-runtime-class"] = sandboxConfig.RuntimeClass
		spec.Annotations["ani.kubercloud.io/sandbox-network-egress-policy"] = string(sandboxConfig.NetworkEgressPolicy)
	default:
		return ports.WorkloadSpec{}, fmt.Errorf("unsupported demo instance kind %q", kind)
	}
	return spec, nil
}

func defaultInstanceNetworkPolicy() ports.WorkloadNetworkPolicy {
	return ports.WorkloadNetworkPolicy{
		TenantIsolated: true,
		Attachments: []ports.WorkloadNetworkAttachment{
			{NetworkID: "tenant-vpc", Plane: ports.NetworkPlaneTenantVPC, Required: true, Primary: true},
			{NetworkID: "foundation-mesh", Plane: ports.NetworkPlaneFoundationMesh, Required: true},
			{NetworkID: "management", Plane: ports.NetworkPlaneManagement, Required: true},
		},
	}
}

func networkPolicyFromRequest(request *instanceNetworkRequest, fallback ports.WorkloadNetworkPolicy) ports.WorkloadNetworkPolicy {
	if request == nil {
		return fallback
	}
	fallback.VPCID = strings.TrimSpace(request.VPCID)
	fallback.SubnetID = strings.TrimSpace(request.SubnetID)
	fallback.SecurityGroupIDs = append([]string(nil), request.SecurityGroupIDs...)
	fallback.AssignPrivateIP = request.AssignPrivateIP
	fallback.PrivateIP = strings.TrimSpace(request.PrivateIP)
	if fallback.VPCID != "" {
		fallback.Attachments = []ports.WorkloadNetworkAttachment{{
			NetworkID: fallback.VPCID, SubnetID: fallback.SubnetID, Plane: ports.NetworkPlaneTenantVPC, Required: true, Primary: true,
		}}
	}
	return fallback
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func diskSpecFromRequest(request *instanceDiskRequest) *ports.InstanceDiskSpec {
	if request == nil {
		return nil
	}
	return &ports.InstanceDiskSpec{VolumeID: strings.TrimSpace(request.VolumeID), Name: strings.TrimSpace(request.Name), SizeGiB: request.SizeGiB, VolumeType: strings.TrimSpace(request.VolumeType), StorageClass: strings.TrimSpace(request.StorageClass), Encrypted: request.Encrypted, DeleteOnFailure: request.DeleteOnFailure, DeleteWithInstance: request.DeleteWithInstance}
}

func diskSpecsFromRequest(request []instanceDiskRequest) []ports.InstanceDiskSpec {
	items := make([]ports.InstanceDiskSpec, 0, len(request))
	for _, item := range request {
		if disk := diskSpecFromRequest(&item); disk != nil {
			items = append(items, *disk)
		}
	}
	return items
}

func storageAttachmentFromDisk(disk ports.InstanceDiskSpec, kind ports.StorageAttachmentKind) ports.WorkloadStorageAttachment {
	return ports.WorkloadStorageAttachment{Name: disk.Name, Kind: kind, ResourceID: disk.VolumeID, SizeGiB: disk.SizeGiB, StorageClass: disk.StorageClass, ReadOnly: false, Required: true, Encrypted: disk.Encrypted, DeleteOnFailure: disk.DeleteOnFailure, DeleteWithInstance: disk.DeleteWithInstance}
}

func containerSpecFromRequest(request *containerConfigRequest, replicas int) *ports.ContainerInstanceSpec {
	spec := &ports.ContainerInstanceSpec{Ports: []int32{8080}, Replicas: int32(maxInt(replicas, 1))}
	if request == nil {
		return spec
	}
	spec.Replicas = int32(maxInt(request.Replicas, maxInt(replicas, 1)))
	spec.PortSpecs = portSpecsFromRequest(request.Ports)
	if len(spec.PortSpecs) > 0 {
		spec.Ports = nil
	}
	spec.Env = envVarsFromRequest(request.Env)
	spec.SecretIDs = append([]string(nil), request.SecretIDs...)
	spec.VolumeMounts = volumeMountsFromRequest(request.VolumeMounts)
	spec.FilesystemMounts = filesystemMountsFromRequest(request.FilesystemMounts)
	if request.WorkloadIdentity != nil {
		spec.WorkloadIdentity = ports.InstanceWorkloadIdentityConfig{Enabled: request.WorkloadIdentity.Enabled, Scopes: append([]string(nil), request.WorkloadIdentity.Scopes...)}
	}
	return spec
}

func containerSpecFromGPURequest(request *gpuContainerConfigRequest, replicas int) *ports.ContainerInstanceSpec {
	base := containerSpecFromRequest(nil, replicas)
	base.Replicas = int32(maxInt(request.Replicas, maxInt(replicas, 1)))
	base.PortSpecs = portSpecsFromRequest(request.Ports)
	if len(base.PortSpecs) > 0 {
		base.Ports = nil
	}
	base.Env = envVarsFromRequest(request.Env)
	base.SecretIDs = append([]string(nil), request.SecretIDs...)
	base.VolumeMounts = volumeMountsFromRequest(request.VolumeMounts)
	base.FilesystemMounts = filesystemMountsFromRequest(request.FilesystemMounts)
	return base
}

func portSpecsFromRequest(request []instancePortRequest) []ports.InstancePortSpec {
	items := make([]ports.InstancePortSpec, 0, len(request))
	for _, item := range request {
		protocol := strings.ToLower(strings.TrimSpace(item.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		items = append(items, ports.InstancePortSpec{Name: strings.TrimSpace(item.Name), ContainerPort: item.ContainerPort, Protocol: protocol})
	}
	return items
}

func envVarsFromRequest(request []instanceEnvRequest) []ports.InstanceEnvVar {
	items := make([]ports.InstanceEnvVar, 0, len(request))
	for _, item := range request {
		items = append(items, ports.InstanceEnvVar{Name: strings.TrimSpace(item.Name), Value: item.Value, SecretRef: strings.TrimSpace(item.SecretRef)})
	}
	return items
}

func volumeMountsFromRequest(request []instanceVolumeMountRequest) []ports.InstanceVolumeMount {
	items := make([]ports.InstanceVolumeMount, 0, len(request))
	for _, item := range request {
		items = append(items, ports.InstanceVolumeMount{VolumeID: strings.TrimSpace(item.VolumeID), MountPath: strings.TrimSpace(item.MountPath), ReadOnly: item.ReadOnly})
	}
	return items
}

func filesystemMountsFromRequest(request []instanceFilesystemMountRequest) []ports.InstanceFilesystemMount {
	items := make([]ports.InstanceFilesystemMount, 0, len(request))
	for _, item := range request {
		items = append(items, ports.InstanceFilesystemMount{FilesystemID: strings.TrimSpace(item.FilesystemID), MountPath: strings.TrimSpace(item.MountPath), ReadOnly: item.ReadOnly})
	}
	return items
}

func storageAttachmentsFromContainer(spec *ports.ContainerInstanceSpec) []ports.WorkloadStorageAttachment {
	if spec == nil {
		return nil
	}
	items := make([]ports.WorkloadStorageAttachment, 0, len(spec.VolumeMounts)+len(spec.FilesystemMounts))
	for _, item := range spec.VolumeMounts {
		items = append(items, ports.WorkloadStorageAttachment{ResourceType: "volume", ResourceID: item.VolumeID, MountPath: item.MountPath, ReadOnly: item.ReadOnly, Required: true})
	}
	for _, item := range spec.FilesystemMounts {
		items = append(items, ports.WorkloadStorageAttachment{ResourceType: "filesystem", ResourceID: item.FilesystemID, MountPath: item.MountPath, ReadOnly: item.ReadOnly, Required: true})
	}
	return items
}

type resolvedCreateFields struct {
	BootImage     string
	SSHUsername   string
	SSHKeyRef     string
	Replicas      int
	GPUVendor     string
	GPUModel      string
	GPUCount      int
	GPUSpecID     string
	GPUShares     int
	GPUMBPerShare int
	QueueName     string
	WorkloadClass string
	SandboxConfig sandboxConfigRequest
}

func resolveCreateInstanceFields(req createInstanceRequest, kind ports.WorkloadKind) (resolvedCreateFields, error) {
	if err := validateCreateInstanceConfigs(req, kind); err != nil {
		return resolvedCreateFields{}, err
	}
	resolved := resolvedCreateFields{
		BootImage:     req.BootImage,
		SSHUsername:   req.SSHUsername,
		SSHKeyRef:     req.SSHKeyRef,
		Replicas:      req.Replicas,
		GPUVendor:     firstNonEmpty(req.GPU.Vendor, req.GPUVendor),
		GPUModel:      firstNonEmpty(req.GPU.Model, req.GPUModel),
		GPUCount:      firstNonZeroInt(req.GPU.Count, req.GPUCount),
		GPUSpecID:     strings.TrimSpace(req.GPU.SpecID),
		QueueName:     strings.TrimSpace(req.GPU.QueueName),
		WorkloadClass: strings.TrimSpace(req.GPU.WorkloadClass),
		SandboxConfig: req.SandboxConfig,
	}
	switch kind {
	case ports.WorkloadKindVM:
		if req.VMConfig != nil {
			if err := conflictString("boot_image", req.VMConfig.BootImage, req.BootImage); err != nil {
				return resolvedCreateFields{}, err
			}
			if err := conflictString("ssh_username", req.VMConfig.SSHUsername, req.SSHUsername); err != nil {
				return resolvedCreateFields{}, err
			}
			if err := conflictString("ssh_key_ref", req.VMConfig.SSHKeyRef, req.SSHKeyRef); err != nil {
				return resolvedCreateFields{}, err
			}
			resolved.BootImage = firstNonEmpty(req.VMConfig.BootImage, req.BootImage)
			resolved.SSHUsername = firstNonEmpty(req.VMConfig.SSHUsername, req.SSHUsername)
			resolved.SSHKeyRef = firstNonEmpty(req.VMConfig.SSHKeyRef, req.SSHKeyRef)
		}
	case ports.WorkloadKindContainer:
		if req.ContainerConfig != nil {
			if err := conflictInt("replicas", req.ContainerConfig.Replicas, req.Replicas); err != nil {
				return resolvedCreateFields{}, err
			}
			resolved.Replicas = firstNonZeroInt(req.ContainerConfig.Replicas, req.Replicas)
		}
	case ports.WorkloadKindGPUContainer:
		if req.GPUContainerConfig != nil {
			if err := conflictInt("replicas", req.GPUContainerConfig.Replicas, req.Replicas); err != nil {
				return resolvedCreateFields{}, err
			}
			flatGPU := createGPURequest{
				Vendor: firstNonEmpty(req.GPU.Vendor, req.GPUVendor),
				Model:  firstNonEmpty(req.GPU.Model, req.GPUModel),
				Count:  firstNonZeroInt(req.GPU.Count, req.GPUCount),
			}
			if err := conflictString("gpu.vendor", req.GPUContainerConfig.GPU.Vendor, flatGPU.Vendor); err != nil {
				return resolvedCreateFields{}, err
			}
			if err := conflictString("gpu.model", req.GPUContainerConfig.GPU.Model, flatGPU.Model); err != nil {
				return resolvedCreateFields{}, err
			}
			if err := conflictInt("gpu.count", req.GPUContainerConfig.GPU.Count, flatGPU.Count); err != nil {
				return resolvedCreateFields{}, err
			}
			if err := conflictString("gpu.queue_name", req.GPUContainerConfig.GPU.QueueName, req.GPU.QueueName); err != nil {
				return resolvedCreateFields{}, err
			}
			if err := conflictString("gpu.workload_class", req.GPUContainerConfig.GPU.WorkloadClass, req.GPU.WorkloadClass); err != nil {
				return resolvedCreateFields{}, err
			}
			resolved.Replicas = firstNonZeroInt(req.GPUContainerConfig.Replicas, req.Replicas)
			resolved.GPUVendor = firstNonEmpty(req.GPUContainerConfig.GPU.Vendor, flatGPU.Vendor)
			resolved.GPUModel = firstNonEmpty(req.GPUContainerConfig.GPU.Model, flatGPU.Model)
			resolved.GPUCount = firstNonZeroInt(req.GPUContainerConfig.GPU.Count, flatGPU.Count)
			resolved.GPUSpecID = firstNonEmpty(req.GPUContainerConfig.GPU.SpecID, resolved.GPUSpecID)
			resolved.QueueName = firstNonEmpty(req.GPUContainerConfig.GPU.QueueName, resolved.QueueName)
			resolved.WorkloadClass = firstNonEmpty(req.GPUContainerConfig.GPU.WorkloadClass, resolved.WorkloadClass)
		}
	case ports.WorkloadKindSandbox:
		// sandbox_config is already the nested path; no flat aliases.
	}
	return resolved, nil
}

func validateCreateInstanceConfigs(req createInstanceRequest, kind ports.WorkloadKind) error {
	configs := []struct {
		name       string
		present    bool
		allowedFor ports.WorkloadKind
	}{
		{"vm_config", req.VMConfig != nil, ports.WorkloadKindVM},
		{"container_config", req.ContainerConfig != nil, ports.WorkloadKindContainer},
		{"gpu_container_config", req.GPUContainerConfig != nil, ports.WorkloadKindGPUContainer},
		{"sandbox_config", sandboxConfigProvided(req.SandboxConfig), ports.WorkloadKindSandbox},
	}
	for _, cfg := range configs {
		if cfg.present && cfg.allowedFor != kind {
			return fmt.Errorf("%s is only valid when kind=%s", cfg.name, cfg.allowedFor)
		}
	}
	return nil
}

func sandboxConfigProvided(cfg sandboxConfigRequest) bool {
	return strings.TrimSpace(cfg.RuntimeClass) != "" ||
		strings.TrimSpace(cfg.TemplateID) != "" ||
		strings.TrimSpace(cfg.SessionTimeout) != "" ||
		strings.TrimSpace(cfg.IdleTimeout) != "" ||
		strings.TrimSpace(cfg.OnTimeout) != "" ||
		strings.TrimSpace(cfg.NetworkEgressPolicy) != "" ||
		len(cfg.EgressAllowlist) > 0 || len(cfg.Env) > 0 || len(cfg.InitialPorts) > 0
}

func conflictString(field, configValue, flatValue string) error {
	configValue = strings.TrimSpace(configValue)
	flatValue = strings.TrimSpace(flatValue)
	if configValue != "" && flatValue != "" && configValue != flatValue {
		return fmt.Errorf("%s conflicts between *_config and flat alias", field)
	}
	return nil
}

func conflictInt(field string, configValue, flatValue int) error {
	if configValue != 0 && flatValue != 0 && configValue != flatValue {
		return fmt.Errorf("%s conflicts between *_config and flat alias", field)
	}
	return nil
}

func instanceKindFromRequest(req createInstanceRequest) (ports.WorkloadKind, error) {
	kind := strings.TrimSpace(req.Kind)
	instanceType := strings.TrimSpace(req.InstanceType)
	if kind != "" && instanceType != "" && kind != instanceType {
		return "", fmt.Errorf("kind and instance_type must match when both are provided")
	}
	return ports.WorkloadKind(firstNonEmpty(kind, instanceType)), nil
}

func sandboxConfigFromRequest(request sandboxConfigRequest) (ports.SandboxConfig, error) {
	timeout := 30 * time.Minute
	if strings.TrimSpace(request.SessionTimeout) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(request.SessionTimeout))
		if err != nil || parsed <= 0 {
			return ports.SandboxConfig{}, fmt.Errorf("sandbox_config.session_timeout must be a positive duration")
		}
		timeout = parsed
	}
	idleTimeout := time.Duration(0)
	if strings.TrimSpace(request.IdleTimeout) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(request.IdleTimeout))
		if err != nil || parsed <= 0 {
			return ports.SandboxConfig{}, fmt.Errorf("sandbox_config.idle_timeout must be a positive duration")
		}
		idleTimeout = parsed
	}
	policy := ports.SandboxNetworkEgressPolicy(firstNonEmpty(strings.TrimSpace(request.NetworkEgressPolicy), string(ports.SandboxNetworkEgressDenyAll)))
	switch policy {
	case ports.SandboxNetworkEgressDenyAll, ports.SandboxNetworkEgressAllowlist, ports.SandboxNetworkEgressInternet:
	default:
		return ports.SandboxConfig{}, fmt.Errorf("sandbox_config.network_egress_policy must be deny_all, allowlist, or internet")
	}
	return ports.SandboxConfig{
		RuntimeClass:        firstNonEmpty(strings.TrimSpace(request.RuntimeClass), "sandbox-kata"),
		TemplateID:          strings.TrimSpace(request.TemplateID),
		SessionTimeout:      timeout,
		IdleTimeout:         idleTimeout,
		OnTimeout:           strings.TrimSpace(request.OnTimeout),
		NetworkEgressPolicy: policy,
		EgressAllowlist:     append([]string(nil), request.EgressAllowlist...),
		Env:                 envVarsFromRequest(request.Env),
		InitialPorts:        portSpecsFromRequest(request.InitialPorts),
	}, nil
}

func secretBindingsFromRequest(request []secretBindingRequest) []ports.WorkloadSecretBinding {
	if len(request) == 0 {
		return nil
	}
	bindings := make([]ports.WorkloadSecretBinding, 0, len(request))
	for _, item := range request {
		bindings = append(bindings, ports.WorkloadSecretBinding{
			SecretID:  strings.TrimSpace(item.SecretID),
			MountPath: strings.TrimSpace(item.MountPath),
			EnvPrefix: strings.TrimSpace(item.EnvPrefix),
		})
	}
	return bindings
}

func instanceResponseFromRecord(record ports.WorkloadInstanceRecord) instanceResponse {
	devProfile := localCoreDevProfile("local-instance-service", "Core dev/local profile; provider execution is gated separately")
	return instanceResponse{
		ID:                    record.InstanceID,
		TenantID:              record.TenantID,
		Name:                  record.Name,
		Description:           record.Description,
		Labels:                cloneStringMap(record.Labels),
		Kind:                  string(record.Kind),
		InstanceType:          string(record.Kind),
		State:                 string(record.Status.State),
		Status:                string(record.Status.State),
		Reason:                record.Status.Reason,
		Provider:              record.Provider,
		DevProfile:            devProfile,
		OperationID:           record.OperationID,
		ResourceRefs:          record.ResourceRefs,
		Endpoint:              record.Status.Endpoint,
		Image:                 imageSummaryFromRecord(record),
		Compute:               computeSummaryFromRecord(record),
		Network:               networkSummaryFromRecord(record),
		Access:                accessSummaryFromRecord(record),
		StorageAttachments:    storageAttachmentResponsesFromRecord(record),
		TerminationProtection: record.Lifecycle.TerminationProtection,
		SSH:                   sshResponseFromRecord(record),
		Volumes:               volumeResponsesFromRecord(record),
		Snapshots:             snapshotResponsesFromRecord(record),
		Container:             containerResponseFromRecord(record),
		GPU:                   gpuResponseFromRecord(record),
		Sandbox:               sandboxResponseFromRecord(record),
		WorkloadIdentity:      identityResponseFromRecord(record),
		CreatedAt:             record.CreatedAt.Format(time.RFC3339),
		UpdatedAt:             record.UpdatedAt.Format(time.RFC3339),
	}
}

func imageSummaryFromRecord(record ports.WorkloadInstanceRecord) instanceImageSummary {
	return instanceImageSummary{
		ID:           record.Image.ID,
		Ref:          record.Image.Ref,
		Digest:       record.Image.Digest,
		Name:         record.Image.Name,
		Tag:          record.Image.Tag,
		Purpose:      record.Image.Purpose,
		Architecture: record.Image.Architecture,
	}
}

func computeSummaryFromRecord(record ports.WorkloadInstanceRecord) instanceComputeSummary {
	return instanceComputeSummary{
		CPU:              record.Compute.CPU,
		Memory:           record.Compute.Memory,
		SpecID:           record.Compute.SpecID,
		GPUType:          record.Compute.GPUType,
		GPUShares:        record.Compute.GPUShares,
		GPUMBPerShare:    record.Compute.GPUMBPerShare,
		AvailabilityZone: record.Compute.AvailabilityZone,
		NodeName:         record.Compute.NodeName,
	}
}

func networkSummaryFromRecord(record ports.WorkloadInstanceRecord) instanceNetworkSummary {
	securityGroups := make([]instanceSecurityGroupSummary, 0, len(record.Network.SecurityGroups))
	for _, group := range record.Network.SecurityGroups {
		securityGroups = append(securityGroups, instanceSecurityGroupSummary{ID: group.ID, Name: group.Name})
	}
	endpoints := make([]instanceEndpointSummary, 0, len(record.Network.Endpoints))
	for _, endpoint := range record.Network.Endpoints {
		endpoints = append(endpoints, instanceEndpointSummary{
			Name:     endpoint.Name,
			Address:  endpoint.Address,
			Protocol: endpoint.Protocol,
			Port:     endpoint.Port,
		})
	}
	return instanceNetworkSummary{
		VPCID:            record.Network.VPCID,
		VPCName:          record.Network.VPCName,
		SubnetID:         record.Network.SubnetID,
		SubnetName:       record.Network.SubnetName,
		PrivateIP:        record.Network.PrivateIP,
		SecurityGroups:   securityGroups,
		Endpoints:        endpoints,
		LoadBalancerRefs: append([]string(nil), record.Network.LoadBalancerRefs...),
	}
}

func accessSummaryFromRecord(record ports.WorkloadInstanceRecord) instanceAccessSummary {
	return instanceAccessSummary{
		SSHAvailable:     record.Access.SSHAvailable,
		ConsoleAvailable: record.Access.ConsoleAvailable,
		ExecAvailable:    record.Access.ExecAvailable,
		Reason:           record.Access.Reason,
	}
}

func storageAttachmentResponsesFromRecord(record ports.WorkloadInstanceRecord) []instanceStorageAttachmentResponse {
	if len(record.StorageAttachments) == 0 {
		return nil
	}
	items := make([]instanceStorageAttachmentResponse, 0, len(record.StorageAttachments))
	for _, attachment := range record.StorageAttachments {
		items = append(items, instanceStorageAttachmentResponse{
			ResourceType: attachment.ResourceType,
			ResourceID:   attachment.ResourceID,
			Name:         attachment.Name,
			MountPath:    attachment.MountPath,
			ReadOnly:     attachment.ReadOnly,
			Status:       attachment.Status,
			TaskID:       attachment.TaskID,
		})
	}
	return items
}

func (api *instanceAPI) instanceResponseFromRecord(record ports.WorkloadInstanceRecord) instanceResponse {
	response := instanceResponseFromRecord(record)
	if api == nil || !api.realProvider {
		return response
	}
	provider := firstNonEmpty(api.providerName, record.Provider)
	response.DevProfile = coreDevProfileResponse{
		Mode:         "real",
		Provider:     provider,
		RealProvider: true,
		Reason:       "Instance resources are managed through the configured Kubernetes provider",
	}
	return response
}

func sshResponseFromRecord(record ports.WorkloadInstanceRecord) *instanceSSHResponse {
	if record.SSH == nil {
		return nil
	}
	return &instanceSSHResponse{
		Username: record.SSH.Username,
		Host:     record.SSH.Host,
		Port:     record.SSH.Port,
		KeyRef:   record.SSH.KeyRef,
		Ready:    record.SSH.Ready,
		Reason:   record.SSH.Reason,
	}
}

func volumeResponsesFromRecord(record ports.WorkloadInstanceRecord) []instanceVolumeResponse {
	if len(record.Status.Storage) == 0 {
		return nil
	}
	items := make([]instanceVolumeResponse, 0, len(record.Status.Storage))
	for _, volume := range record.Status.Storage {
		items = append(items, instanceVolumeResponse{
			Name:      volume.Name,
			Kind:      string(volume.Kind),
			SizeGiB:   volume.SizeGiB,
			SourceRef: volume.SourceRef,
			MountPath: volume.MountPath,
			ReadOnly:  volume.ReadOnly,
		})
	}
	return items
}

func containerResponseFromRecord(record ports.WorkloadInstanceRecord) *instanceContainerResponse {
	if record.Container == nil {
		return nil
	}
	history := make([]instanceContainerChangeResponse, 0, len(record.Container.History))
	for _, item := range record.Container.History {
		history = append(history, instanceContainerChangeResponse{
			Revision:  item.Revision,
			Image:     item.Image,
			CreatedAt: item.CreatedAt.Format(time.RFC3339),
		})
	}
	return &instanceContainerResponse{
		Replicas:      record.Container.Replicas,
		ReadyReplicas: record.Container.ReadyReplicas,
		Revision:      record.Container.Revision,
		RolloutStatus: record.Container.RolloutStatus,
		History:       history,
	}
}

func gpuResponseFromRecord(record ports.WorkloadInstanceRecord) *instanceGPUResponse {
	if record.GPU == nil {
		return nil
	}
	return &instanceGPUResponse{
		Vendor:             string(record.GPU.Vendor),
		Model:              record.GPU.Model,
		Count:              record.GPU.Count,
		ResourceName:       record.GPU.ResourceName,
		QueueName:          record.GPU.QueueName,
		SchedulingReason:   record.GPU.SchedulingReason,
		UtilizationPercent: record.GPU.UtilizationPercent,
	}
}

func sandboxResponseFromRecord(record ports.WorkloadInstanceRecord) *instanceSandboxResponse {
	if record.Sandbox == nil {
		return nil
	}
	response := &instanceSandboxResponse{
		RuntimeClass:        record.Sandbox.Config.RuntimeClass,
		SessionTimeout:      record.Sandbox.Config.SessionTimeout.String(),
		NetworkEgressPolicy: string(record.Sandbox.Config.NetworkEgressPolicy),
		SessionState:        string(record.Sandbox.State),
		DevProfile: coreDevProfileResponse{
			Mode:         record.Sandbox.DevProfile.Mode,
			Provider:     record.Sandbox.DevProfile.Provider,
			RealProvider: record.Sandbox.DevProfile.RealProvider,
			Reason:       record.Sandbox.DevProfile.Reason,
		},
	}
	for _, item := range record.Sandbox.Ports {
		response.Ports = append(response.Ports, instanceSandboxPortResponse{
			Port: item.Port, Name: item.Name, Protocol: item.Protocol, Status: item.Status, PreviewURL: item.PreviewURL,
		})
	}
	return response
}

func identityResponseFromRecord(record ports.WorkloadInstanceRecord) *instanceIdentityResponse {
	if record.Identity == nil {
		return nil
	}
	identity := &instanceIdentityResponse{
		KeyID:     record.Identity.KeyID,
		KeyPrefix: record.Identity.KeyPrefix,
		Scopes:    append([]string(nil), record.Identity.Scopes...),
		Active:    record.Identity.Active,
	}
	if !record.Identity.CreatedAt.IsZero() {
		identity.CreatedAt = record.Identity.CreatedAt.Format(time.RFC3339)
	}
	if !record.Identity.RevokedAt.IsZero() {
		identity.RevokedAt = record.Identity.RevokedAt.Format(time.RFC3339)
	}
	return identity
}

func snapshotResponsesFromRecord(record ports.WorkloadInstanceRecord) []instanceSnapshotResponse {
	if len(record.Snapshots) == 0 {
		return nil
	}
	items := make([]instanceSnapshotResponse, 0, len(record.Snapshots))
	for _, snapshot := range record.Snapshots {
		item := instanceSnapshotResponse{
			ID:               snapshot.ID,
			Name:             snapshot.Name,
			SourceInstanceID: snapshot.SourceInstanceID,
			State:            snapshot.State,
			Reason:           snapshot.Reason,
			CreatedAt:        snapshot.CreatedAt.Format(time.RFC3339),
		}
		if !snapshot.ReadyAt.IsZero() {
			item.ReadyAt = snapshot.ReadyAt.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return items
}

func manifestResponses(manifests []ports.WorkloadManifest) []instanceManifestResponse {
	items := make([]instanceManifestResponse, 0, len(manifests))
	for _, manifest := range manifests {
		items = append(items, instanceManifestResponse{
			Name:     manifest.Name,
			Kind:     manifest.Kind,
			Provider: manifest.Provider,
			Content:  manifest.Content,
		})
	}
	return items
}

func instanceTimeline(result ports.WorkloadInstanceCreateResult) []instanceTimelineStepResponse {
	return []instanceTimelineStepResponse{
		{Name: "规划", Status: "completed", Detail: "network and storage prerequisites resolved before provider rendering"},
		{Name: "渲染", Status: "completed", Detail: fmt.Sprintf("%d provider manifest rendered", len(result.Manifests))},
		{Name: "准入", Status: boolStatus(result.Admission.Allowed), Detail: result.Admission.Reason},
		{Name: "Dry-run", Status: boolStatus(result.DryRun.Accepted), Detail: result.DryRun.Reason},
		{Name: "Apply", Status: boolStatus(result.Apply.Applied), Detail: result.Apply.Reason},
		{Name: "状态回写", Status: string(result.FinalStatus.State), Detail: result.FinalStatus.Reason},
	}
}

func operationResponseFromRecord(record ports.WorkloadOperationRecord) instanceOperationResponse {
	steps := make([]instanceTimelineStepResponse, 0, len(record.Steps))
	for _, step := range record.Steps {
		steps = append(steps, instanceTimelineStepResponse{
			Name:   step.StepName,
			Status: string(step.Status),
			Detail: step.Message,
		})
	}
	return instanceOperationResponse{
		ID:             record.ID,
		TenantID:       record.TenantID,
		InstanceID:     record.InstanceID,
		Operation:      string(record.Operation),
		Status:         string(record.Status),
		IdempotencyKey: record.IdempotencyKey,
		RequestedBy:    record.RequestedBy,
		FailureReason:  record.FailureReason,
		FailureMessage: record.FailureMessage,
		RetryEligible:  record.RetryEligible,
		Steps:          steps,
		CreatedAt:      record.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      record.UpdatedAt.Format(time.RFC3339),
	}
}

func instanceLogListFromResult(result ports.InstanceLogListResult) instanceLogListResponse {
	items := make([]instanceLogEntryResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, instanceLogEntryResponse{
			Timestamp: item.Timestamp.Format(time.RFC3339),
			Level:     item.Level,
			Message:   item.Message,
			Container: item.Container,
			Stream:    item.Stream,
		})
	}
	return instanceLogListResponse{
		Items:      items,
		Total:      result.Total,
		NextCursor: optionalString(result.NextCursor),
		DevProfile: coreDevProfileFromPort(result.DevProfile),
	}
}

func instanceEventListFromResult(result ports.InstanceEventListResult) instanceEventListResponse {
	items := make([]instanceEventResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, instanceEventResponse{
			ID:         item.ID,
			InstanceID: item.InstanceID,
			Type:       item.Type,
			Reason:     item.Reason,
			Message:    item.Message,
			Count:      item.Count,
			OccurredAt: item.OccurredAt.Format(time.RFC3339),
		})
	}
	return instanceEventListResponse{
		Items:      items,
		Total:      result.Total,
		NextCursor: optionalString(result.NextCursor),
		DevProfile: coreDevProfileFromPort(result.DevProfile),
	}
}

func instanceMetricsFromRecord(record ports.InstanceMetricsRecord) instanceMetricsResponse {
	return instanceMetricsResponse{
		InstanceID:        record.InstanceID,
		Timestamp:         record.Timestamp.Format(time.RFC3339),
		CPUUtilizationPct: record.CPUUtilizationPct,
		MemoryUsedMB:      record.MemoryUsedMB,
		MemoryTotalMB:     record.MemoryTotalMB,
		GPUUtilizationPct: record.GPUUtilizationPct,
		GPUMemoryUsedMB:   record.GPUMemoryUsedMB,
		GPUMemoryTotalMB:  record.GPUMemoryTotalMB,
		NetworkRXBytes:    record.NetworkRXBytes,
		NetworkTXBytes:    record.NetworkTXBytes,
		DevProfile:        coreDevProfileFromPort(record.DevProfile),
	}
}

func instanceSecurityEventListFromResult(result ports.InstanceSecurityEventListResult) instanceSecurityEventListResponse {
	items := make([]instanceSecurityEventResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, instanceSecurityEventResponse{
			ID:          item.ID,
			InstanceID:  item.InstanceID,
			EventType:   item.EventType,
			Severity:    item.Severity,
			Description: item.Description,
			OccurredAt:  item.OccurredAt.Format(time.RFC3339),
		})
	}
	return instanceSecurityEventListResponse{
		Items:      items,
		Total:      result.Total,
		NextCursor: optionalString(result.NextCursor),
		DevProfile: coreDevProfileFromPort(result.DevProfile),
	}
}

func instanceExecSessionFromRecord(record ports.InstanceExecSessionRecord) instanceExecSessionResponse {
	return instanceExecSessionResponse{
		ID:         record.ID,
		InstanceID: record.InstanceID,
		WSURL:      record.WSURL,
		Token:      record.Token,
		ExpiresAt:  record.ExpiresAt.Format(time.RFC3339),
		DevProfile: coreDevProfileFromPort(record.DevProfile),
	}
}

func instanceConsoleSessionFromRecord(record ports.InstanceConsoleSessionRecord) instanceConsoleSessionResponse {
	return instanceConsoleSessionResponse{
		SessionID:  record.SessionID,
		InstanceID: record.InstanceID,
		Protocol:   record.Protocol,
		ConnectURL: record.ConnectURL,
		URL:        record.URL,
		ExpiresAt:  record.ExpiresAt.Format(time.RFC3339),
		DevProfile: coreDevProfileFromPort(record.DevProfile),
	}
}

func isValidConsoleProtocol(protocol string) bool {
	switch protocol {
	case "console", "vnc", "novnc", "serial":
		return true
	default:
		return false
	}
}

func instanceTenantID(c *app.RequestContext) string {
	if tenantID := middleware.GetTenantID(c); tenantID != "" {
		return tenantID
	}
	return "demo-tenant"
}

func instanceUserID(c *app.RequestContext) string {
	if value, ok := c.Get("user_id"); ok {
		if userID, ok := value.(string); ok && userID != "" {
			return userID
		}
	}
	return "demo-user"
}

func writeInstanceError(c *app.RequestContext, status int, code string, message string) {
	c.JSON(status, map[string]any{
		"code":       code,
		"message":    message,
		"request_id": middleware.GetRequestID(c),
	})
}

func writeInstanceCreateError(c *app.RequestContext, err error) {
	if code, ok := instanceCreatePreconditionCode(err); ok {
		writeInstanceError(c, http.StatusUnprocessableEntity, code, err.Error())
		return
	}
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeInstanceError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeInstanceError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrFailedPrecondition):
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
	case errors.Is(err, ports.ErrQuotaExceeded):
		writeInstanceError(c, http.StatusConflict, "QUOTA_EXCEEDED", err.Error())
	case errors.Is(err, ports.ErrReservedInsufficient):
		writeInstanceError(c, http.StatusConflict, "RESERVED_INSUFFICIENT", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeInstanceError(c, http.StatusBadRequest, "INSTANCE_CREATE_FAILED", err.Error())
	}
}

func instanceCreatePreconditionCode(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	message := err.Error()
	for _, code := range []string{
		"ImageNotFound",
		"ImageScanning",
		"ImageVulnerabilityBlocked",
		"ImagePurposeMismatch",
	} {
		if strings.Contains(message, code+":") {
			return code, true
		}
	}
	return "", false
}

func writeInstanceObservabilityError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeInstanceError(c, http.StatusNotFound, "INSTANCE_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeInstanceError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeInstanceError(c, http.StatusBadRequest, "UNSUPPORTED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}

func writeSandboxRuntimeError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeInstanceError(c, http.StatusNotFound, "INSTANCE_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeInstanceError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrPayloadTooLarge):
		writeInstanceError(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, ports.ErrFailedPrecondition), errors.Is(err, ports.ErrUnsupported), errors.Is(err, ports.ErrNotConfigured):
		writeInstanceError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
	default:
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}

func sandboxExecutionForRecord(record ports.WorkloadInstanceRecord) (*ports.SandboxExecutionContext, error) {
	execution, err := runtimeadapter.SandboxExecutionContextFromRecord(record)
	if err != nil {
		return nil, err
	}
	return &execution, nil
}

func instanceLifecycleErrorStatus(err error) int {
	if errors.Is(err, ports.ErrConflict) {
		return http.StatusConflict
	}
	if errors.Is(err, ports.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func instanceLifecycleErrorCode(err error) string {
	if errors.Is(err, ports.ErrConflict) {
		return "CONFLICT"
	}
	if errors.Is(err, ports.ErrNotFound) {
		return "INSTANCE_NOT_FOUND"
	}
	return "INSTANCE_LIFECYCLE_FAILED"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func hasIdempotencyKey(value string) bool {
	return strings.TrimSpace(value) != ""
}

func boolStatus(ok bool) string {
	if ok {
		return "completed"
	}
	return "blocked"
}

func queryInt(c *app.RequestContext, name string, fallback int) int {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func coreDevProfileFromPort(profile ports.DevProfileInfo) coreDevProfileResponse {
	return coreDevProfileResponse{
		Mode:         profile.Mode,
		Provider:     profile.Provider,
		RealProvider: profile.RealProvider,
		Reason:       profile.Reason,
	}
}

func maxInt(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func firstNonZeroInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

type memoryPlanAuditStore struct{}

func (s *memoryPlanAuditStore) RecordPlan(_ context.Context, _ ports.WorkloadPlanAuditRecord) (string, error) {
	return "audit_demo_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", ""), nil
}

var _ ports.WorkloadPlanAuditStore = (*memoryPlanAuditStore)(nil)

type fallbackGPUInventory struct{}

func (fallbackGPUInventory) ListNodeClasses(context.Context, ports.GPUDiscoveryFilter) ([]ports.GPUNodeClass, error) {
	return nil, nil
}

func (fallbackGPUInventory) GetNodeClass(context.Context, string) (ports.GPUNodeClass, error) {
	return ports.GPUNodeClass{}, ports.ErrNotFound
}

func (fallbackGPUInventory) PlanScheduling(_ context.Context, request ports.GPUSchedulingRequest) (ports.GPUSchedulingDecision, error) {
	quantity := fmt.Sprintf("%d", maxInt(request.RequiredCount, 1))
	return ports.GPUSchedulingDecision{
		NodeSelector:     map[string]string{"ani.io/gpu-demo": "true"},
		ResourceName:     "nvidia.com/gpu",
		ResourceQuantity: quantity,
		RuntimeClassName: "nvidia",
		SchedulerName:    "volcano",
		QueueName:        "demo-gpu",
		Reasons:          []string{"demo GPU scheduling decision"},
	}, nil
}

func (fallbackGPUInventory) ListSpecAvailability(_ context.Context, _ string) ([]ports.GPUSpecAvailability, error) {
	return nil, ports.ErrUnsupported
}

var _ ports.GPUInventory = fallbackGPUInventory{}
