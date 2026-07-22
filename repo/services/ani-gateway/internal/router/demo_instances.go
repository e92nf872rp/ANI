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
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type demoInstanceStore struct {
	mu      sync.RWMutex
	records map[string]ports.WorkloadInstanceRecord
}

func newDemoInstanceStore() *demoInstanceStore {
	return &demoInstanceStore{records: map[string]ports.WorkloadInstanceRecord{}}
}

func (s *demoInstanceStore) UpsertStatus(_ context.Context, record ports.WorkloadInstanceRecord) error {
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.InstanceID) == "" {
		return fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.TenantID+"/"+record.InstanceID] = record
	return nil
}

func (s *demoInstanceStore) Get(_ context.Context, tenantID string, instanceID string) (ports.WorkloadInstanceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[tenantID+"/"+instanceID]
	if !ok {
		return ports.WorkloadInstanceRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *demoInstanceStore) List(_ context.Context, tenantID string, kind ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
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

var _ ports.WorkloadInstanceStore = (*demoInstanceStore)(nil)

type demoInstanceAPI struct {
	service                       ports.WorkloadInstanceService
	operations                    ports.WorkloadOperationStore
	observability                 ports.InstanceObservability
	observabilityUsesInstanceName bool
	gpuInventory                  ports.GPUInventory
	k8sClient                     *runtimeadapter.KubernetesRESTClient
	store                         *demoInstanceStore
}

type demoCreateInstanceRequest struct {
	Kind                  string                         `json:"kind"`
	InstanceType          string                         `json:"instance_type"`
	Name                  string                         `json:"name"`
	CPU                   string                         `json:"cpu"`
	Memory                string                         `json:"memory"`
	BootImage             string                         `json:"boot_image"`
	SSHUsername           string                         `json:"ssh_username"`
	SSHKeyRef             string                         `json:"ssh_key_ref"`
	Image                 string                         `json:"image"`
	GPUVendor             string                         `json:"gpu_vendor"`
	GPUModel              string                         `json:"gpu_model"`
	GPUCount              int                            `json:"gpu_count"`
	GPU                   demoCreateGPURequest           `json:"gpu"`
	Replicas              int                            `json:"replicas"`
	AutoStart             *bool                          `json:"auto_start"`
	TerminationProtection bool                           `json:"termination_protection"`
	VMConfig              *demoVMConfigRequest           `json:"vm_config"`
	ContainerConfig       *demoContainerConfigRequest    `json:"container_config"`
	GPUContainerConfig    *demoGPUContainerConfigRequest `json:"gpu_container_config"`
	SandboxConfig         demoSandboxConfigRequest       `json:"sandbox_config"`
	SecretBindings        []demoSecretBindingRequest     `json:"secret_bindings"`
	Description           string                         `json:"description"`
	IdempotencyKey        string                         `json:"idempotency_key"`
}

type demoVMConfigRequest struct {
	BootImage   string `json:"boot_image"`
	SSHUsername string `json:"ssh_username"`
	SSHKeyRef   string `json:"ssh_key_ref"`
}

type demoContainerConfigRequest struct {
	Replicas int `json:"replicas"`
}

type demoGPUContainerConfigRequest struct {
	Replicas int                  `json:"replicas"`
	GPU      demoCreateGPURequest `json:"gpu"`
}

type demoSandboxConfigRequest struct {
	RuntimeClass        string `json:"runtime_class"`
	SessionTimeout      string `json:"session_timeout"`
	NetworkEgressPolicy string `json:"network_egress_policy"`
}

type demoSecretBindingRequest struct {
	SecretID  string `json:"secret_id"`
	MountPath string `json:"mount_path"`
	EnvPrefix string `json:"env_prefix"`
}

type demoCreateGPURequest struct {
	Vendor string `json:"vendor"`
	Model  string `json:"model"`
	Count  int    `json:"count"`
}

type demoLifecycleRequest struct {
	Action         string `json:"action"`
	CPU            string `json:"cpu"`
	Memory         string `json:"memory"`
	SnapshotName   string `json:"snapshot_name"`
	VolumeID       string `json:"volume_id"`
	Revision       string `json:"revision"`
	IdempotencyKey string `json:"idempotency_key"`
}

type demoConsoleRequest struct {
	Protocol string `json:"protocol"`
}

type demoShellExecRequest struct {
	Command string `json:"command"`
}

type demoShellExecResponse struct {
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	CWD      string `json:"cwd"`
}

type demoCreateExecSessionRequest struct {
	IdempotencyKey string   `json:"idempotency_key"`
	Container      string   `json:"container"`
	Command        []string `json:"command"`
	TTY            *bool    `json:"tty"`
	Rows           int      `json:"rows"`
	Cols           int      `json:"cols"`
}

type demoInstanceResponse struct {
	ID                    string                 `json:"id"`
	TenantID              string                 `json:"tenant_id"`
	Name                  string                 `json:"name"`
	Kind                  string                 `json:"kind"`
	InstanceType          string                 `json:"instance_type"`
	State                 string                 `json:"state"`
	Status                string                 `json:"status"`
	Reason                string                 `json:"reason,omitempty"`
	Provider              string                 `json:"provider"`
	DevProfile            coreDevProfileResponse `json:"dev_profile"`
	OperationID           string                 `json:"operation_id,omitempty"`
	ResourceRefs          []string               `json:"resource_refs"`
	Endpoint              string                 `json:"endpoint"`
	TerminationProtection bool                   `json:"termination_protection"`
	SSH                   *demoSSHResponse       `json:"ssh,omitempty"`
	Volumes               []demoVolume           `json:"volumes,omitempty"`
	Snapshots             []demoSnapshot         `json:"snapshots,omitempty"`
	Container             *demoContainer         `json:"container,omitempty"`
	GPU                   *demoGPU               `json:"gpu,omitempty"`
	Sandbox               *demoSandbox           `json:"sandbox,omitempty"`
	WorkloadIdentity      *demoIdentity          `json:"workload_identity,omitempty"`
	CreatedAt             string                 `json:"created_at"`
	UpdatedAt             string                 `json:"updated_at"`
}

type demoSSHResponse struct {
	Username string `json:"username"`
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	KeyRef   string `json:"key_ref,omitempty"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason,omitempty"`
}

type demoVolume struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeGiB   int64  `json:"size_gib,omitempty"`
	SourceRef string `json:"source_ref,omitempty"`
	MountPath string `json:"mount_path,omitempty"`
	ReadOnly  bool   `json:"read_only,omitempty"`
}

type demoSnapshot struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	SourceInstanceID string `json:"source_instance_id"`
	State            string `json:"state"`
	Reason           string `json:"reason,omitempty"`
	CreatedAt        string `json:"created_at"`
	ReadyAt          string `json:"ready_at,omitempty"`
}

type demoContainer struct {
	Replicas      int32                 `json:"replicas"`
	ReadyReplicas int32                 `json:"ready_replicas"`
	Revision      string                `json:"revision,omitempty"`
	RolloutStatus string                `json:"rollout_status,omitempty"`
	History       []demoContainerChange `json:"history,omitempty"`
}

type demoContainerChange struct {
	Revision  string `json:"revision"`
	Image     string `json:"image,omitempty"`
	CreatedAt string `json:"created_at"`
}

type demoGPU struct {
	Vendor             string  `json:"vendor,omitempty"`
	Model              string  `json:"model,omitempty"`
	Count              int     `json:"count"`
	ResourceName       string  `json:"resource_name,omitempty"`
	QueueName          string  `json:"queue_name,omitempty"`
	SchedulingReason   string  `json:"scheduling_reason,omitempty"`
	UtilizationPercent float64 `json:"utilization_percent"`
}

type demoSandbox struct {
	RuntimeClass        string                 `json:"runtime_class"`
	SessionTimeout      string                 `json:"session_timeout"`
	NetworkEgressPolicy string                 `json:"network_egress_policy"`
	SessionState        string                 `json:"session_state"`
	DevProfile          coreDevProfileResponse `json:"dev_profile"`
}

type demoIdentity struct {
	KeyID     string   `json:"key_id,omitempty"`
	KeyPrefix string   `json:"key_prefix,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	Active    bool     `json:"active"`
	CreatedAt string   `json:"created_at,omitempty"`
	RevokedAt string   `json:"revoked_at,omitempty"`
}

type demoInstanceCreateResponse struct {
	Instance    demoInstanceResponse `json:"instance"`
	OperationID string               `json:"operation_id"`
	AuditID     string               `json:"audit_id"`
	Manifests   []demoManifest       `json:"manifests"`
	Timeline    []demoTimelineStep   `json:"timeline"`
	DemoNotice  string               `json:"demo_notice"`
}

type demoInstanceLifecycleResponse struct {
	Instance    demoInstanceResponse `json:"instance"`
	OperationID string               `json:"operation_id"`
}

type demoOperationResponse struct {
	ID             string             `json:"id"`
	TenantID       string             `json:"tenant_id"`
	InstanceID     string             `json:"instance_id"`
	Operation      string             `json:"operation"`
	Status         string             `json:"status"`
	IdempotencyKey string             `json:"idempotency_key,omitempty"`
	RequestedBy    string             `json:"requested_by"`
	FailureReason  string             `json:"failure_reason,omitempty"`
	FailureMessage string             `json:"failure_message,omitempty"`
	RetryEligible  bool               `json:"retry_eligible"`
	Steps          []demoTimelineStep `json:"steps"`
	CreatedAt      string             `json:"created_at"`
	UpdatedAt      string             `json:"updated_at"`
}

type demoInstanceLogEntryResponse struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Container string `json:"container,omitempty"`
	Stream    string `json:"stream,omitempty"`
}

type demoInstanceLogListResponse struct {
	Items      []demoInstanceLogEntryResponse `json:"items"`
	Total      int                            `json:"total"`
	NextCursor *string                        `json:"next_cursor"`
	DevProfile coreDevProfileResponse         `json:"dev_profile"`
}

type demoInstanceEventResponse struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	Type       string `json:"type"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	Count      int    `json:"count,omitempty"`
	OccurredAt string `json:"occurred_at"`
}

type demoInstanceEventListResponse struct {
	Items      []demoInstanceEventResponse `json:"items"`
	Total      int                         `json:"total"`
	NextCursor *string                     `json:"next_cursor"`
	DevProfile coreDevProfileResponse      `json:"dev_profile"`
}

type demoInstanceMetricsResponse struct {
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

type demoInstanceSecurityEventResponse struct {
	ID          string `json:"id"`
	InstanceID  string `json:"instance_id"`
	EventType   string `json:"event_type"`
	Severity    string `json:"severity"`
	Description string `json:"description,omitempty"`
	OccurredAt  string `json:"occurred_at"`
}

type demoInstanceSecurityEventListResponse struct {
	Items      []demoInstanceSecurityEventResponse `json:"items"`
	Total      int                                 `json:"total"`
	NextCursor *string                             `json:"next_cursor"`
	DevProfile coreDevProfileResponse              `json:"dev_profile"`
}

type demoInstanceExecSessionResponse struct {
	ID         string                 `json:"id"`
	InstanceID string                 `json:"instance_id"`
	WSURL      string                 `json:"ws_url"`
	Token      string                 `json:"token,omitempty"`
	ExpiresAt  string                 `json:"expires_at"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
}

type demoManifest struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Content  string `json:"content"`
}

type demoTimelineStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func newDemoInstanceAPI() *demoInstanceAPI {
	return newDemoInstanceAPIWithObservability(nil, false, nil, nil)
}

func newDemoInstanceAPIWithObservability(observability ports.InstanceObservability, useInstanceName bool, gpuInventory ports.GPUInventory, k8sClient *runtimeadapter.KubernetesRESTClient) *demoInstanceAPI {
	store := newDemoInstanceStore()
	operations := runtimeadapter.NewLocalOperationStore()
	identity := runtimeadapter.NewLocalWorkloadIdentityService()

	// Use real K8s GPU inventory when available, otherwise fall back to demo.
	var inventory ports.GPUInventory = demoGPUInventory{}
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

	orchestrator := runtimeadapter.NewLocalInstanceOrchestrator(
		planner,
		runtimeadapter.NewKubernetesDryRunRenderer(planner),
		runtimeadapter.NewLocalAdmissionGuard(),
		&demoPlanAuditStore{},
		dryRun,
		apply,
		reader,
		runtimeadapter.NewLocalStatusReconciler(),
		runtimeadapter.WithInstanceStore(store),
		runtimeadapter.WithInstanceOrchestratorWorkloadIdentityService(identity),
	)

	serviceOpts := []runtimeadapter.InstanceServiceOption{
		runtimeadapter.WithOperationStore(operations),
		runtimeadapter.WithWorkloadIdentityService(identity),
		runtimeadapter.WithSandboxRuntime(runtimeadapter.NewLocalSandboxRuntime()),
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
	return &demoInstanceAPI{
		service:                       service,
		operations:                    operations,
		observability:                 observability,
		observabilityUsesInstanceName: useInstanceName,
		gpuInventory:                  gpuInventory,
		k8sClient:                     k8sClient,
		store:                         store,
	}
}

func registerDemoInstancesWithObservability(v1 *route.RouterGroup, observability ports.InstanceObservability, useInstanceName bool, gpuInventory ports.GPUInventory, k8sClient *runtimeadapter.KubernetesRESTClient) {
	api := newDemoInstanceAPIWithObservability(observability, useInstanceName, gpuInventory, k8sClient)
	v1.GET("/instances", api.list)
	v1.POST("/instances", api.create)
	v1.GET("/instances/:instance_id", api.get)
	v1.POST("/instances/:instance_id/lifecycle", api.lifecycle)
	v1.POST("/instances/:instance_id/console", api.console)
	v1.GET("/instances/:instance_id/logs", api.listLogs)
	v1.GET("/instances/:instance_id/events", api.listEvents)
	v1.GET("/instances/:instance_id/metrics", api.getMetrics)
	v1.POST("/instances/:instance_id/exec", api.createExecSession)
	v1.GET("/instances/:instance_id/security-events", api.listSecurityEvents)
	v1.GET("/instances/:instance_id/operations", api.listOperations)
	v1.GET("/demo/instances", api.list)
	v1.POST("/demo/instances", api.create)
	v1.GET("/demo/instances/:instance_id", api.get)
	v1.GET("/demo/instances/:instance_id/operations", api.listOperations)
	v1.POST("/demo/instances/:instance_id/lifecycle", api.lifecycle)
	v1.GET("/demo/instances/:instance_id/ops/:action", api.ops)
	v1.POST("/demo/instances/:instance_id/console", api.console)
	v1.POST("/demo/instances/:instance_id/console/exec", api.consoleExec)
	v1.GET("/instance-operations/:operation_id", api.getOperation)
}

func (api *demoInstanceAPI) create(ctx context.Context, c *app.RequestContext) {
	var req demoCreateInstanceRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid instance request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	spec, err := demoSpecFromRequest(req, demoTenantID(c))
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	result, err := api.service.Create(ctx, ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  req.IdempotencyKey,
		Spec:            spec,
		UserID:          demoUserID(c),
		PermissionProof: "demo:instance:create",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "INSTANCE_CREATE_FAILED", err.Error())
		return
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
		writeDemoError(c, http.StatusInternalServerError, "INSTANCE_LOOKUP_FAILED", err.Error())
		return
	}
	status := http.StatusCreated
	if result.IdempotentReplay {
		status = http.StatusConflict
	}
	c.JSON(status, demoInstanceCreateResponse{
		Instance:    demoInstanceFromRecord(record),
		OperationID: result.OperationID,
		AuditID:     result.AuditID,
		Manifests:   demoManifests(result.Manifests),
		Timeline:    demoTimeline(result),
		DemoNotice:  "demo profile uses the M1 instance service with local apply enabled; set kubernetes_rest provider separately for live cluster execution.",
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
// only mechanism that updates the in-memory demo store after create,
// because the background reconcile controller is bound to the
// PostgreSQL-backed MetadataInstanceStore, not the demo in-memory store.
// Records whose Deployment is gone (NotFound) are marked failed so they do
// not linger in "provisioning" forever.
func (api *demoInstanceAPI) refreshStoreStatuses(ctx context.Context, tenantID string, kind ports.WorkloadKind) {
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
func (api *demoInstanceAPI) refreshOneStoreStatus(ctx context.Context, record *ports.WorkloadInstanceRecord) {
	if record == nil || record.Name == "" || record.Provider != "kubernetes" {
		return
	}
	namespace := demoTenantNamespace(record.TenantID)
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
func (api *demoInstanceAPI) discoverOrphanDeployments(ctx context.Context, tenantID string) []ports.WorkloadInstanceRecord {
	if api.k8sClient == nil || strings.TrimSpace(tenantID) == "" {
		return nil
	}
	namespace := demoTenantNamespace(tenantID)
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
				State:    demoOrphanState(obs.Phase),
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

// observeOrphan inspects a single Kubernetes Deployment and its Pods to
// extract the observation fields needed to synthesize a WorkloadInstanceRecord.
func (api *demoInstanceAPI) observeOrphan(ctx context.Context, tenantID string, depName string) orphanObservation {
	namespace := demoTenantNamespace(tenantID)
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
			count := demoOrphanGPUCount(raw)
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
func (api *demoInstanceAPI) orphanGPUStatus(ctx context.Context, nodeName string, count int, phase string) *ports.GPUInstanceStatus {
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

// demoOrphanState maps a Kubernetes Deployment phase string to an ANI
// WorkloadState. Unknown phases default to Pending.
func demoOrphanState(phase string) ports.WorkloadState {
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

// demoOrphanGPUCount parses a Kubernetes resource quantity value for
// nvidia.com/gpu into an integer count.
func demoOrphanGPUCount(raw any) int {
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

// demoTenantNamespace returns the Kubernetes namespace that hosts instances
// for the given tenant. It mirrors the runtime adapter's tenantNamespace
// helper without importing it from the router package.
func demoTenantNamespace(tenantID string) string {
	return "ani-tenant-" + strings.ReplaceAll(tenantID, "_", "-")
}

func (api *demoInstanceAPI) get(ctx context.Context, c *app.RequestContext) {
	tenantID := demoTenantID(c)
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
						c.JSON(http.StatusOK, demoInstanceFromRecord(orphan))
						return
					}
				}
			}
		}
		writeDemoError(c, http.StatusNotFound, "INSTANCE_NOT_FOUND", err.Error())
		return
	}
	c.JSON(http.StatusOK, demoInstanceFromRecord(record))
}

func (api *demoInstanceAPI) list(ctx context.Context, c *app.RequestContext) {
	tenantID := demoTenantID(c)
	kind := ports.WorkloadKind(c.Query("kind"))
	// Refresh store-backed records against the live Kubernetes cluster so the
	// demo API reflects real Deployment/Pod status instead of the stale
	// "provisioning" captured at create time. This is an on-demand refresh
	// triggered by the list request; there is no background reconcile loop
	// for the in-memory demo store.
	api.refreshStoreStatuses(ctx, tenantID, kind)
	records, err := api.service.List(ctx, ports.WorkloadInstanceListRequest{
		TenantID: tenantID,
		Kind:     kind,
	})
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "INSTANCE_LIST_FAILED", err.Error())
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
	items := make([]demoInstanceResponse, 0, len(records))
	for _, record := range records {
		items = append(items, demoInstanceFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (api *demoInstanceAPI) lifecycle(ctx context.Context, c *app.RequestContext) {
	var req demoLifecycleRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid lifecycle request")
		return
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	lifecycle := ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  req.IdempotencyKey,
		TenantID:        demoTenantID(c),
		InstanceID:      c.Param("instance_id"),
		SnapshotName:    req.SnapshotName,
		VolumeID:        req.VolumeID,
		Revision:        req.Revision,
		UserID:          demoUserID(c),
		PermissionProof: "demo:instance:lifecycle",
		RequestedAt:     time.Now().UTC(),
	}
	var (
		record ports.WorkloadInstanceRecord
		err    error
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
			Resources:       ports.WorkloadResourceRequest{CPU: firstNonEmpty(req.CPU, "4"), Memory: firstNonEmpty(req.Memory, "8Gi")},
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
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "action must be start, stop, restart, resize, snapshot, attach_volume, detach_volume, rollback, or delete")
		return
	}
	if err != nil {
		writeDemoError(c, demoLifecycleErrorStatus(err), demoLifecycleErrorCode(err), err.Error())
		return
	}
	c.JSON(http.StatusOK, demoInstanceLifecycleResponse{
		Instance:    demoInstanceFromRecord(record),
		OperationID: record.OperationID,
	})
}

func (api *demoInstanceAPI) listOperations(ctx context.Context, c *app.RequestContext) {
	result, err := api.operations.ListOperations(ctx, ports.WorkloadOperationListRequest{
		TenantID:   demoTenantID(c),
		InstanceID: c.Param("instance_id"),
		Limit:      queryInt(c, "limit", 20),
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "INSTANCE_OPERATIONS_FAILED", err.Error())
		return
	}
	items := make([]demoOperationResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, demoOperationFromRecord(item))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": result.NextCursor})
}

func (api *demoInstanceAPI) listLogs(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	result, err := api.observability.ListLogs(ctx, ports.InstanceObservationListRequest{
		TenantID:   demoTenantID(c),
		InstanceID: api.observabilityTargetID(record),
		Limit:      queryInt(c, "limit", 100),
		Cursor:     c.Query("cursor"),
		Level:      c.Query("level"),
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, demoInstanceLogListFromResult(result))
}

func (api *demoInstanceAPI) listEvents(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	result, err := api.observability.ListEvents(ctx, ports.InstanceObservationListRequest{
		TenantID:   demoTenantID(c),
		InstanceID: api.observabilityTargetID(record),
		Limit:      queryInt(c, "limit", 50),
		Type:       c.Query("type"),
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, demoInstanceEventListFromResult(result))
}

func (api *demoInstanceAPI) getMetrics(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	result, err := api.observability.GetMetrics(ctx, ports.InstanceObservationGetRequest{
		TenantID:   demoTenantID(c),
		InstanceID: api.observabilityTargetID(record),
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, demoInstanceMetricsFromRecord(result))
}

func (api *demoInstanceAPI) createExecSession(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	var req demoCreateExecSessionRequest
	if len(c.Request.Body()) > 0 {
		if err := c.BindJSON(&req); err != nil {
			writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid exec session request")
			return
		}
	}
	if !hasIdempotencyKey(req.IdempotencyKey) {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	tty := true
	if req.TTY != nil {
		tty = *req.TTY
	}
	result, err := api.observability.CreateExecSession(ctx, ports.InstanceExecSessionCreateRequest{
		TenantID:       demoTenantID(c),
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
	c.JSON(http.StatusOK, demoInstanceExecSessionFromRecord(result))
}

func (api *demoInstanceAPI) listSecurityEvents(ctx context.Context, c *app.RequestContext) {
	record, err := api.instanceForObservation(ctx, c)
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	result, err := api.observability.ListSecurityEvents(ctx, ports.InstanceObservationListRequest{
		TenantID:   demoTenantID(c),
		InstanceID: api.observabilityTargetID(record),
		Limit:      queryInt(c, "limit", 50),
		Severity:   c.Query("severity"),
	})
	if err != nil {
		writeInstanceObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, demoInstanceSecurityEventListFromResult(result))
}

func (api *demoInstanceAPI) getOperation(ctx context.Context, c *app.RequestContext) {
	record, err := api.operations.GetOperation(ctx, demoTenantID(c), c.Param("operation_id"))
	if err != nil {
		writeDemoError(c, http.StatusNotFound, "INSTANCE_OPERATION_NOT_FOUND", err.Error())
		return
	}
	c.JSON(http.StatusOK, demoOperationFromRecord(record))
}

func (api *demoInstanceAPI) ops(ctx context.Context, c *app.RequestContext) {
	action := ports.WorkloadInstanceOpsAction(c.Param("action"))
	result, err := api.service.Ops(ctx, ports.WorkloadInstanceOpsRequest{
		TenantID:        demoTenantID(c),
		InstanceID:      c.Param("instance_id"),
		Action:          action,
		ContainerName:   "main",
		Command:         []string{"sh", "-lc", "echo ani-demo"},
		UserID:          demoUserID(c),
		PermissionProof: "demo:instance:ops",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "INSTANCE_OPS_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (api *demoInstanceAPI) console(ctx context.Context, c *app.RequestContext) {
	var req demoConsoleRequest
	if len(c.Request.Body()) > 0 {
		if err := c.BindJSON(&req); err != nil {
			writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid console request")
			return
		}
	}
	action := consoleAction(req.Protocol)
	result, err := api.service.Ops(ctx, ports.WorkloadInstanceOpsRequest{
		TenantID:        demoTenantID(c),
		InstanceID:      c.Param("instance_id"),
		Action:          action,
		Protocol:        firstNonEmpty(req.Protocol, string(action)),
		UserID:          demoUserID(c),
		PermissionProof: "demo:instance:console",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "INSTANCE_CONSOLE_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (api *demoInstanceAPI) consoleExec(ctx context.Context, c *app.RequestContext) {
	var req demoShellExecRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid shell exec request")
		return
	}
	record, err := api.service.Get(ctx, ports.WorkloadInstanceGetRequest{
		TenantID:   demoTenantID(c),
		InstanceID: c.Param("instance_id"),
	})
	if err != nil {
		writeDemoError(c, http.StatusNotFound, "INSTANCE_NOT_FOUND", err.Error())
		return
	}
	if record.Kind != ports.WorkloadKindVM {
		writeDemoError(c, http.StatusBadRequest, "INSTANCE_CONSOLE_UNSUPPORTED", "real shell console is only available for vm demo instances")
		return
	}
	if record.Status.State != ports.WorkloadStateRunning {
		writeDemoError(c, http.StatusConflict, "INSTANCE_NOT_RUNNING", "vm console requires running instance")
		return
	}
	result, err := runDemoShellCommand(ctx, record, req.Command)
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "SHELL_EXEC_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

func (api *demoInstanceAPI) instanceForObservation(ctx context.Context, c *app.RequestContext) (ports.WorkloadInstanceRecord, error) {
	return api.service.Get(ctx, ports.WorkloadInstanceGetRequest{
		TenantID:   demoTenantID(c),
		InstanceID: c.Param("instance_id"),
	})
}

func (api *demoInstanceAPI) observabilityTargetID(record ports.WorkloadInstanceRecord) string {
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

func runDemoShellCommand(ctx context.Context, record ports.WorkloadInstanceRecord, command string) (demoShellExecResponse, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return demoShellExecResponse{}, fmt.Errorf("%w: command is required", ports.ErrInvalid)
	}
	if len(command) > 500 {
		return demoShellExecResponse{}, fmt.Errorf("%w: command is too long for demo shell", ports.ErrInvalid)
	}
	if blockedDemoShellCommand(command) {
		return demoShellExecResponse{}, fmt.Errorf("%w: command is blocked by demo shell guardrail", ports.ErrUnsupported)
	}
	cwd, err := demoShellCWD(record)
	if err != nil {
		return demoShellExecResponse{}, err
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
	return demoShellExecResponse{
		Command:  command,
		Output:   output,
		ExitCode: exitCode,
		CWD:      cwd,
	}, nil
}

func demoShellCWD(record ports.WorkloadInstanceRecord) (string, error) {
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

func blockedDemoShellCommand(command string) bool {
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

func demoSpecFromRequest(req demoCreateInstanceRequest, tenantID string) (ports.WorkloadSpec, error) {
	kind, err := demoInstanceKind(req)
	if err != nil {
		return ports.WorkloadSpec{}, err
	}
	if kind == "" {
		kind = ports.WorkloadKindVM
	}
	resolved, err := demoResolveCreateInstanceFields(req, kind)
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
		TenantID: tenantID,
		Name:     name,
		Kind:     kind,
		Image:    firstNonEmpty(req.Image, "docker.io/nvidia/cuda:12.4.1-base-ubuntu22.04"),
		Resources: ports.WorkloadResourceRequest{
			CPU:    firstNonEmpty(req.CPU, "2"),
			Memory: firstNonEmpty(req.Memory, "4Gi"),
		},
		Network: ports.WorkloadNetworkPolicy{
			TenantIsolated: true,
			Attachments: []ports.WorkloadNetworkAttachment{
				{NetworkID: "tenant-vpc", Plane: ports.NetworkPlaneTenantVPC, Required: true, Primary: true},
				{NetworkID: "foundation-mesh", Plane: ports.NetworkPlaneFoundationMesh, Required: true},
				{NetworkID: "management", Plane: ports.NetworkPlaneManagement, Required: true},
			},
		},
		Storage: []ports.WorkloadStorageAttachment{
			{Name: name + "-root", Kind: ports.StorageAttachmentRootDisk, SizeGiB: 40, SourceRef: firstNonEmpty(resolved.BootImage, "images/ubuntu-22.04.qcow2"), Required: true},
		},
		Lifecycle: ports.InstanceLifecyclePolicy{AutoStart: autoStart, TerminationProtection: req.TerminationProtection},
		Labels: map[string]string{
			"ani.io/demo": "true",
		},
		Annotations: map[string]string{
			"ani.io/demo-description": req.Description,
		},
		SecretBindings: demoSecretBindingsFromRequest(req.SecretBindings),
	}
	switch kind {
	case ports.WorkloadKindVM:
		spec.VM = &ports.VMInstanceSpec{
			BootImage:    firstNonEmpty(resolved.BootImage, "images/ubuntu-22.04.qcow2"),
			SSHUsername:  firstNonEmpty(resolved.SSHUsername, "ubuntu"),
			SSHKeySecret: resolved.SSHKeyRef,
			MachineType:  "q35",
			RootDisk:     spec.Storage[0],
		}
	case ports.WorkloadKindContainer:
		spec.Storage = nil
		spec.Container = &ports.ContainerInstanceSpec{Ports: []int32{8080}, Replicas: int32(maxInt(resolved.Replicas, 1))}
	case ports.WorkloadKindGPUContainer:
		spec.Storage = nil
		spec.Container = &ports.ContainerInstanceSpec{Ports: []int32{8080}, Replicas: int32(maxInt(resolved.Replicas, 1))}
		if len(spec.Command) == 0 {
			spec.Command = []string{"sleep", "infinity"}
		}
		spec.Resources.GPU = ports.GPUSchedulingRequest{
			TenantID:         tenantID,
			WorkloadID:       name,
			PreferredVendors: []ports.GPUVendor{ports.GPUVendor(firstNonEmpty(resolved.GPUVendor, "nvidia"))},
			PreferredModels:  []string{firstNonEmpty(resolved.GPUModel, "A100")},
			RequiredCount:    maxInt(resolved.GPUCount, 1),
		}
	case ports.WorkloadKindSandbox:
		sandboxConfig, err := demoSandboxConfigFromRequest(resolved.SandboxConfig)
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

type demoResolvedCreateFields struct {
	BootImage     string
	SSHUsername   string
	SSHKeyRef     string
	Replicas      int
	GPUVendor     string
	GPUModel      string
	GPUCount      int
	SandboxConfig demoSandboxConfigRequest
}

func demoResolveCreateInstanceFields(req demoCreateInstanceRequest, kind ports.WorkloadKind) (demoResolvedCreateFields, error) {
	if err := demoValidateCreateInstanceConfigs(req, kind); err != nil {
		return demoResolvedCreateFields{}, err
	}
	resolved := demoResolvedCreateFields{
		BootImage:     req.BootImage,
		SSHUsername:   req.SSHUsername,
		SSHKeyRef:     req.SSHKeyRef,
		Replicas:      req.Replicas,
		GPUVendor:     firstNonEmpty(req.GPU.Vendor, req.GPUVendor),
		GPUModel:      firstNonEmpty(req.GPU.Model, req.GPUModel),
		GPUCount:      firstNonZeroInt(req.GPU.Count, req.GPUCount),
		SandboxConfig: req.SandboxConfig,
	}
	switch kind {
	case ports.WorkloadKindVM:
		if req.VMConfig != nil {
			if err := demoConflictString("boot_image", req.VMConfig.BootImage, req.BootImage); err != nil {
				return demoResolvedCreateFields{}, err
			}
			if err := demoConflictString("ssh_username", req.VMConfig.SSHUsername, req.SSHUsername); err != nil {
				return demoResolvedCreateFields{}, err
			}
			if err := demoConflictString("ssh_key_ref", req.VMConfig.SSHKeyRef, req.SSHKeyRef); err != nil {
				return demoResolvedCreateFields{}, err
			}
			resolved.BootImage = firstNonEmpty(req.VMConfig.BootImage, req.BootImage)
			resolved.SSHUsername = firstNonEmpty(req.VMConfig.SSHUsername, req.SSHUsername)
			resolved.SSHKeyRef = firstNonEmpty(req.VMConfig.SSHKeyRef, req.SSHKeyRef)
		}
	case ports.WorkloadKindContainer:
		if req.ContainerConfig != nil {
			if err := demoConflictInt("replicas", req.ContainerConfig.Replicas, req.Replicas); err != nil {
				return demoResolvedCreateFields{}, err
			}
			resolved.Replicas = firstNonZeroInt(req.ContainerConfig.Replicas, req.Replicas)
		}
	case ports.WorkloadKindGPUContainer:
		if req.GPUContainerConfig != nil {
			if err := demoConflictInt("replicas", req.GPUContainerConfig.Replicas, req.Replicas); err != nil {
				return demoResolvedCreateFields{}, err
			}
			flatGPU := demoCreateGPURequest{
				Vendor: firstNonEmpty(req.GPU.Vendor, req.GPUVendor),
				Model:  firstNonEmpty(req.GPU.Model, req.GPUModel),
				Count:  firstNonZeroInt(req.GPU.Count, req.GPUCount),
			}
			if err := demoConflictString("gpu.vendor", req.GPUContainerConfig.GPU.Vendor, flatGPU.Vendor); err != nil {
				return demoResolvedCreateFields{}, err
			}
			if err := demoConflictString("gpu.model", req.GPUContainerConfig.GPU.Model, flatGPU.Model); err != nil {
				return demoResolvedCreateFields{}, err
			}
			if err := demoConflictInt("gpu.count", req.GPUContainerConfig.GPU.Count, flatGPU.Count); err != nil {
				return demoResolvedCreateFields{}, err
			}
			resolved.Replicas = firstNonZeroInt(req.GPUContainerConfig.Replicas, req.Replicas)
			resolved.GPUVendor = firstNonEmpty(req.GPUContainerConfig.GPU.Vendor, flatGPU.Vendor)
			resolved.GPUModel = firstNonEmpty(req.GPUContainerConfig.GPU.Model, flatGPU.Model)
			resolved.GPUCount = firstNonZeroInt(req.GPUContainerConfig.GPU.Count, flatGPU.Count)
		}
	case ports.WorkloadKindSandbox:
		// sandbox_config is already the nested path; no flat aliases.
	}
	return resolved, nil
}

func demoValidateCreateInstanceConfigs(req demoCreateInstanceRequest, kind ports.WorkloadKind) error {
	configs := []struct {
		name       string
		present    bool
		allowedFor ports.WorkloadKind
	}{
		{"vm_config", req.VMConfig != nil, ports.WorkloadKindVM},
		{"container_config", req.ContainerConfig != nil, ports.WorkloadKindContainer},
		{"gpu_container_config", req.GPUContainerConfig != nil, ports.WorkloadKindGPUContainer},
		{"sandbox_config", demoSandboxConfigProvided(req.SandboxConfig), ports.WorkloadKindSandbox},
	}
	for _, cfg := range configs {
		if cfg.present && cfg.allowedFor != kind {
			return fmt.Errorf("%s is only valid when kind=%s", cfg.name, cfg.allowedFor)
		}
	}
	return nil
}

func demoSandboxConfigProvided(cfg demoSandboxConfigRequest) bool {
	return strings.TrimSpace(cfg.RuntimeClass) != "" ||
		strings.TrimSpace(cfg.SessionTimeout) != "" ||
		strings.TrimSpace(cfg.NetworkEgressPolicy) != ""
}

func demoConflictString(field, configValue, flatValue string) error {
	configValue = strings.TrimSpace(configValue)
	flatValue = strings.TrimSpace(flatValue)
	if configValue != "" && flatValue != "" && configValue != flatValue {
		return fmt.Errorf("%s conflicts between *_config and flat alias", field)
	}
	return nil
}

func demoConflictInt(field string, configValue, flatValue int) error {
	if configValue != 0 && flatValue != 0 && configValue != flatValue {
		return fmt.Errorf("%s conflicts between *_config and flat alias", field)
	}
	return nil
}

func demoInstanceKind(req demoCreateInstanceRequest) (ports.WorkloadKind, error) {
	kind := strings.TrimSpace(req.Kind)
	instanceType := strings.TrimSpace(req.InstanceType)
	if kind != "" && instanceType != "" && kind != instanceType {
		return "", fmt.Errorf("kind and instance_type must match when both are provided")
	}
	return ports.WorkloadKind(firstNonEmpty(kind, instanceType)), nil
}

func demoSandboxConfigFromRequest(request demoSandboxConfigRequest) (ports.SandboxConfig, error) {
	timeout := 30 * time.Minute
	if strings.TrimSpace(request.SessionTimeout) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(request.SessionTimeout))
		if err != nil || parsed <= 0 {
			return ports.SandboxConfig{}, fmt.Errorf("sandbox_config.session_timeout must be a positive duration")
		}
		timeout = parsed
	}
	policy := ports.SandboxNetworkEgressPolicy(firstNonEmpty(strings.TrimSpace(request.NetworkEgressPolicy), string(ports.SandboxNetworkEgressDenyAll)))
	switch policy {
	case ports.SandboxNetworkEgressDenyAll, ports.SandboxNetworkEgressAllowlist, ports.SandboxNetworkEgressInternet:
	default:
		return ports.SandboxConfig{}, fmt.Errorf("sandbox_config.network_egress_policy must be deny_all, allowlist, or internet")
	}
	return ports.SandboxConfig{
		RuntimeClass:        firstNonEmpty(strings.TrimSpace(request.RuntimeClass), "sandbox-kata"),
		SessionTimeout:      timeout,
		NetworkEgressPolicy: policy,
	}, nil
}

func demoSecretBindingsFromRequest(request []demoSecretBindingRequest) []ports.WorkloadSecretBinding {
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

func demoInstanceFromRecord(record ports.WorkloadInstanceRecord) demoInstanceResponse {
	return demoInstanceResponse{
		ID:                    record.InstanceID,
		TenantID:              record.TenantID,
		Name:                  record.Name,
		Kind:                  string(record.Kind),
		InstanceType:          string(record.Kind),
		State:                 string(record.Status.State),
		Status:                string(record.Status.State),
		Reason:                record.Status.Reason,
		Provider:              record.Provider,
		DevProfile:            localCoreDevProfile("local-instance-service", "Core dev/local profile; provider execution is gated separately"),
		OperationID:           record.OperationID,
		ResourceRefs:          record.ResourceRefs,
		Endpoint:              record.Status.Endpoint,
		TerminationProtection: record.Lifecycle.TerminationProtection,
		SSH:                   demoSSHFromRecord(record),
		Volumes:               demoVolumesFromRecord(record),
		Snapshots:             demoSnapshotsFromRecord(record),
		Container:             demoContainerFromRecord(record),
		GPU:                   demoGPUFromRecord(record),
		Sandbox:               demoSandboxFromRecord(record),
		WorkloadIdentity:      demoIdentityFromRecord(record),
		CreatedAt:             record.CreatedAt.Format(time.RFC3339),
		UpdatedAt:             record.UpdatedAt.Format(time.RFC3339),
	}
}

func demoSSHFromRecord(record ports.WorkloadInstanceRecord) *demoSSHResponse {
	if record.SSH == nil {
		return nil
	}
	return &demoSSHResponse{
		Username: record.SSH.Username,
		Host:     record.SSH.Host,
		Port:     record.SSH.Port,
		KeyRef:   record.SSH.KeyRef,
		Ready:    record.SSH.Ready,
		Reason:   record.SSH.Reason,
	}
}

func demoVolumesFromRecord(record ports.WorkloadInstanceRecord) []demoVolume {
	if len(record.Status.Storage) == 0 {
		return nil
	}
	items := make([]demoVolume, 0, len(record.Status.Storage))
	for _, volume := range record.Status.Storage {
		items = append(items, demoVolume{
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

func demoContainerFromRecord(record ports.WorkloadInstanceRecord) *demoContainer {
	if record.Container == nil {
		return nil
	}
	history := make([]demoContainerChange, 0, len(record.Container.History))
	for _, item := range record.Container.History {
		history = append(history, demoContainerChange{
			Revision:  item.Revision,
			Image:     item.Image,
			CreatedAt: item.CreatedAt.Format(time.RFC3339),
		})
	}
	return &demoContainer{
		Replicas:      record.Container.Replicas,
		ReadyReplicas: record.Container.ReadyReplicas,
		Revision:      record.Container.Revision,
		RolloutStatus: record.Container.RolloutStatus,
		History:       history,
	}
}

func demoGPUFromRecord(record ports.WorkloadInstanceRecord) *demoGPU {
	if record.GPU == nil {
		return nil
	}
	return &demoGPU{
		Vendor:             string(record.GPU.Vendor),
		Model:              record.GPU.Model,
		Count:              record.GPU.Count,
		ResourceName:       record.GPU.ResourceName,
		QueueName:          record.GPU.QueueName,
		SchedulingReason:   record.GPU.SchedulingReason,
		UtilizationPercent: record.GPU.UtilizationPercent,
	}
}

func demoSandboxFromRecord(record ports.WorkloadInstanceRecord) *demoSandbox {
	if record.Sandbox == nil {
		return nil
	}
	return &demoSandbox{
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
}

func demoIdentityFromRecord(record ports.WorkloadInstanceRecord) *demoIdentity {
	if record.Identity == nil {
		return nil
	}
	identity := &demoIdentity{
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

func demoSnapshotsFromRecord(record ports.WorkloadInstanceRecord) []demoSnapshot {
	if len(record.Snapshots) == 0 {
		return nil
	}
	items := make([]demoSnapshot, 0, len(record.Snapshots))
	for _, snapshot := range record.Snapshots {
		item := demoSnapshot{
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

func demoManifests(manifests []ports.WorkloadManifest) []demoManifest {
	items := make([]demoManifest, 0, len(manifests))
	for _, manifest := range manifests {
		items = append(items, demoManifest{
			Name:     manifest.Name,
			Kind:     manifest.Kind,
			Provider: manifest.Provider,
			Content:  manifest.Content,
		})
	}
	return items
}

func demoTimeline(result ports.WorkloadInstanceCreateResult) []demoTimelineStep {
	return []demoTimelineStep{
		{Name: "规划", Status: "completed", Detail: "network and storage prerequisites resolved before provider rendering"},
		{Name: "渲染", Status: "completed", Detail: fmt.Sprintf("%d provider manifest rendered", len(result.Manifests))},
		{Name: "准入", Status: boolStatus(result.Admission.Allowed), Detail: result.Admission.Reason},
		{Name: "Dry-run", Status: boolStatus(result.DryRun.Accepted), Detail: result.DryRun.Reason},
		{Name: "Apply", Status: boolStatus(result.Apply.Applied), Detail: result.Apply.Reason},
		{Name: "状态回写", Status: string(result.FinalStatus.State), Detail: result.FinalStatus.Reason},
	}
}

func demoOperationFromRecord(record ports.WorkloadOperationRecord) demoOperationResponse {
	steps := make([]demoTimelineStep, 0, len(record.Steps))
	for _, step := range record.Steps {
		steps = append(steps, demoTimelineStep{
			Name:   step.StepName,
			Status: string(step.Status),
			Detail: step.Message,
		})
	}
	return demoOperationResponse{
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

func demoInstanceLogListFromResult(result ports.InstanceLogListResult) demoInstanceLogListResponse {
	items := make([]demoInstanceLogEntryResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, demoInstanceLogEntryResponse{
			Timestamp: item.Timestamp.Format(time.RFC3339),
			Level:     item.Level,
			Message:   item.Message,
			Container: item.Container,
			Stream:    item.Stream,
		})
	}
	return demoInstanceLogListResponse{
		Items:      items,
		Total:      result.Total,
		NextCursor: optionalString(result.NextCursor),
		DevProfile: coreDevProfileFromPort(result.DevProfile),
	}
}

func demoInstanceEventListFromResult(result ports.InstanceEventListResult) demoInstanceEventListResponse {
	items := make([]demoInstanceEventResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, demoInstanceEventResponse{
			ID:         item.ID,
			InstanceID: item.InstanceID,
			Type:       item.Type,
			Reason:     item.Reason,
			Message:    item.Message,
			Count:      item.Count,
			OccurredAt: item.OccurredAt.Format(time.RFC3339),
		})
	}
	return demoInstanceEventListResponse{
		Items:      items,
		Total:      result.Total,
		NextCursor: optionalString(result.NextCursor),
		DevProfile: coreDevProfileFromPort(result.DevProfile),
	}
}

func demoInstanceMetricsFromRecord(record ports.InstanceMetricsRecord) demoInstanceMetricsResponse {
	return demoInstanceMetricsResponse{
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

func demoInstanceSecurityEventListFromResult(result ports.InstanceSecurityEventListResult) demoInstanceSecurityEventListResponse {
	items := make([]demoInstanceSecurityEventResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, demoInstanceSecurityEventResponse{
			ID:          item.ID,
			InstanceID:  item.InstanceID,
			EventType:   item.EventType,
			Severity:    item.Severity,
			Description: item.Description,
			OccurredAt:  item.OccurredAt.Format(time.RFC3339),
		})
	}
	return demoInstanceSecurityEventListResponse{
		Items:      items,
		Total:      result.Total,
		NextCursor: optionalString(result.NextCursor),
		DevProfile: coreDevProfileFromPort(result.DevProfile),
	}
}

func demoInstanceExecSessionFromRecord(record ports.InstanceExecSessionRecord) demoInstanceExecSessionResponse {
	return demoInstanceExecSessionResponse{
		ID:         record.ID,
		InstanceID: record.InstanceID,
		WSURL:      record.WSURL,
		Token:      record.Token,
		ExpiresAt:  record.ExpiresAt.Format(time.RFC3339),
		DevProfile: coreDevProfileFromPort(record.DevProfile),
	}
}

func demoTenantID(c *app.RequestContext) string {
	if tenantID := middleware.GetTenantID(c); tenantID != "" {
		return tenantID
	}
	return "demo-tenant"
}

func demoUserID(c *app.RequestContext) string {
	if value, ok := c.Get("user_id"); ok {
		if userID, ok := value.(string); ok && userID != "" {
			return userID
		}
	}
	return "demo-user"
}

func writeDemoError(c *app.RequestContext, status int, code string, message string) {
	c.JSON(status, map[string]any{
		"code":       code,
		"message":    message,
		"request_id": middleware.GetRequestID(c),
	})
}

func writeInstanceObservabilityError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "INSTANCE_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeDemoError(c, http.StatusBadRequest, "UNSUPPORTED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}

func demoLifecycleErrorStatus(err error) int {
	if errors.Is(err, ports.ErrConflict) {
		return http.StatusConflict
	}
	if errors.Is(err, ports.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func demoLifecycleErrorCode(err error) string {
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

type demoPlanAuditStore struct{}

func (s *demoPlanAuditStore) RecordPlan(_ context.Context, _ ports.WorkloadPlanAuditRecord) (string, error) {
	return "audit_demo_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", ""), nil
}

var _ ports.WorkloadPlanAuditStore = (*demoPlanAuditStore)(nil)

type demoGPUInventory struct{}

func (demoGPUInventory) ListNodeClasses(context.Context, ports.GPUDiscoveryFilter) ([]ports.GPUNodeClass, error) {
	return nil, nil
}

func (demoGPUInventory) GetNodeClass(context.Context, string) (ports.GPUNodeClass, error) {
	return ports.GPUNodeClass{}, ports.ErrNotFound
}

func (demoGPUInventory) PlanScheduling(_ context.Context, request ports.GPUSchedulingRequest) (ports.GPUSchedulingDecision, error) {
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

var _ ports.GPUInventory = demoGPUInventory{}
