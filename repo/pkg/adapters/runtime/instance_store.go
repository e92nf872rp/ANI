package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type MetadataInstanceStore struct {
	store ports.MetadataStore
	now   func() time.Time
}

type InstanceStoreOption func(*MetadataInstanceStore)

func WithInstanceStoreClock(now func() time.Time) InstanceStoreOption {
	return func(store *MetadataInstanceStore) {
		if now != nil {
			store.now = now
		}
	}
}

func NewMetadataInstanceStore(store ports.MetadataStore, options ...InstanceStoreOption) *MetadataInstanceStore {
	instanceStore := &MetadataInstanceStore{
		store: store,
		now:   time.Now,
	}
	for _, option := range options {
		option(instanceStore)
	}
	return instanceStore
}

func (s *MetadataInstanceStore) UpsertStatus(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if err := validateInstanceRecord(record); err != nil {
		return err
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		return s.upsertStatusInTx(ctx, tx, record)
	})
}

// UpsertStatusTx implements ports.WorkloadInstanceStoreTx (SPEC §3.2). It
// writes the instance status inside an externally-owned MetadataTx so the
// caller can commit it atomically with a quota Confirm/Cancel/Release in the
// same transaction. The caller is responsible for opening the tenant-
// scoped transaction (WithTenantTx) and injecting the TenantContext.
func (s *MetadataInstanceStore) UpsertStatusTx(ctx context.Context, tx ports.MetadataTx, record ports.WorkloadInstanceRecord) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if err := validateInstanceRecord(record); err != nil {
		return err
	}
	if tx == nil {
		return fmt.Errorf("%w: metadata tx is required for UpsertStatusTx", ports.ErrInvalid)
	}
	return s.upsertStatusInTx(ctx, tx, record)
}

// upsertStatusInTx runs the upsert SQL within the provided MetadataTx. It is
// shared by UpsertStatus (which opens its own tenant tx) and UpsertStatusTx
// (which runs in the caller's tx). The quota_tx_ids JSONB column is written
// alongside the status so TCC tx_ids persist with the instance row.
func (s *MetadataInstanceStore) upsertStatusInTx(ctx context.Context, tx ports.MetadataTx, record ports.WorkloadInstanceRecord) error {
	resourceRefs, err := json.Marshal(record.ResourceRefs)
	if err != nil {
		return fmt.Errorf("marshal resource refs: %w", err)
	}
	networks, err := json.Marshal(record.Status.Networks)
	if err != nil {
		return fmt.Errorf("marshal networks: %w", err)
	}
	storage, err := json.Marshal(record.Status.Storage)
	if err != nil {
		return fmt.Errorf("marshal storage: %w", err)
	}
	lifecyclePolicy, err := json.Marshal(record.Lifecycle)
	if err != nil {
		return fmt.Errorf("marshal lifecycle policy: %w", err)
	}
	sshConnection, err := json.Marshal(firstNonNilSSH(record.SSH))
	if err != nil {
		return fmt.Errorf("marshal ssh connection: %w", err)
	}
	snapshots, err := json.Marshal(record.Snapshots)
	if err != nil {
		return fmt.Errorf("marshal snapshots: %w", err)
	}
	containerStatus, err := json.Marshal(firstNonNilContainer(record.Container))
	if err != nil {
		return fmt.Errorf("marshal container status: %w", err)
	}
	gpuStatus, err := json.Marshal(firstNonNilGPU(record.GPU))
	if err != nil {
		return fmt.Errorf("marshal gpu status: %w", err)
	}
	labels, err := json.Marshal(firstNonNilStringMap(record.Labels))
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	imageSummary, err := json.Marshal(record.Image)
	if err != nil {
		return fmt.Errorf("marshal image summary: %w", err)
	}
	computeSummary, err := json.Marshal(record.Compute)
	if err != nil {
		return fmt.Errorf("marshal compute summary: %w", err)
	}
	networkSummary, err := json.Marshal(record.Network)
	if err != nil {
		return fmt.Errorf("marshal network summary: %w", err)
	}
	accessSummary, err := json.Marshal(record.Access)
	if err != nil {
		return fmt.Errorf("marshal access summary: %w", err)
	}
	storageAttachments, err := json.Marshal(record.StorageAttachments)
	if err != nil {
		return fmt.Errorf("marshal storage attachments: %w", err)
	}
	sandboxStatus, err := json.Marshal(firstNonNilSandbox(record.Sandbox))
	if err != nil {
		return fmt.Errorf("marshal sandbox status: %w", err)
	}
	quotaTxIDs, err := json.Marshal(firstNonNilStringSlice(record.QuotaTxIDs))
	if err != nil {
		return fmt.Errorf("marshal quota tx ids: %w", err)
	}
	now := s.now().UTC()
	createdAt := firstNonZeroTime(record.CreatedAt, now)
	updatedAt := firstNonZeroTime(record.UpdatedAt, record.Status.UpdatedAt, now)

	_, err = tx.Exec(ctx, `
		INSERT INTO workload_instances (
			tenant_id, instance_id, name, workload_kind, provider, audit_id,
			provider_id, resource_refs, state, endpoint, node_name, reason,
			networks, storage, lifecycle_policy, ssh_connection, snapshots, container_status, gpu_status,
			description, labels, image_summary, compute_summary, network_summary, access_summary,
			storage_attachments, sandbox_status, quota_tx_ids, created_at, updated_at
		)
		VALUES (
			$1::uuid, $2, $3, $4, NULLIF($5, ''), NULLIF($6, '')::uuid,
			NULLIF($7, ''), $8::jsonb, $9, NULLIF($10, ''), NULLIF($11, ''),
			NULLIF($12, ''), $13::jsonb, $14::jsonb, $15::jsonb, $16::jsonb, $17::jsonb, $18::jsonb, $19::jsonb,
			NULLIF($20, ''), $21::jsonb, $22::jsonb, $23::jsonb, $24::jsonb, $25::jsonb,
			$26::jsonb, $27::jsonb, $28::jsonb, $29, $30
		)
		ON CONFLICT (tenant_id, instance_id) DO UPDATE SET
			name = EXCLUDED.name,
			workload_kind = EXCLUDED.workload_kind,
			provider = EXCLUDED.provider,
			audit_id = EXCLUDED.audit_id,
			provider_id = EXCLUDED.provider_id,
			resource_refs = EXCLUDED.resource_refs,
			state = EXCLUDED.state,
			endpoint = EXCLUDED.endpoint,
			node_name = EXCLUDED.node_name,
			reason = EXCLUDED.reason,
			networks = EXCLUDED.networks,
			storage = EXCLUDED.storage,
			lifecycle_policy = EXCLUDED.lifecycle_policy,
			ssh_connection = EXCLUDED.ssh_connection,
			snapshots = EXCLUDED.snapshots,
			container_status = EXCLUDED.container_status,
			gpu_status = EXCLUDED.gpu_status,
			description = EXCLUDED.description,
			labels = EXCLUDED.labels,
			image_summary = EXCLUDED.image_summary,
			compute_summary = EXCLUDED.compute_summary,
			network_summary = EXCLUDED.network_summary,
			access_summary = EXCLUDED.access_summary,
			storage_attachments = EXCLUDED.storage_attachments,
			sandbox_status = EXCLUDED.sandbox_status,
			quota_tx_ids = EXCLUDED.quota_tx_ids,
			updated_at = EXCLUDED.updated_at
	`, record.TenantID, record.InstanceID, record.Name, string(record.Kind), record.Provider,
		record.AuditID, record.Status.Ref.ProviderID, string(resourceRefs), string(record.Status.State),
		record.Status.Endpoint, record.Status.NodeName, record.Status.Reason, string(networks), string(storage),
		string(lifecyclePolicy), string(sshConnection), string(snapshots), string(containerStatus), string(gpuStatus),
		record.Description, string(labels), string(imageSummary), string(computeSummary), string(networkSummary),
		string(accessSummary), string(storageAttachments), string(sandboxStatus), string(quotaTxIDs), createdAt, updatedAt)
	if err != nil {
		return fmt.Errorf("upsert workload instance: %w", err)
	}
	return nil
}

// validateInstanceRecord checks the required fields for an upsert.
func validateInstanceRecord(record ports.WorkloadInstanceRecord) error {
	if strings.TrimSpace(record.TenantID) == "" {
		return fmt.Errorf("%w: tenantID is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.InstanceID) == "" {
		return fmt.Errorf("%w: instanceID is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.Name) == "" {
		return fmt.Errorf("%w: name is required", ports.ErrInvalid)
	}
	if record.Kind == "" {
		return fmt.Errorf("%w: workload kind is required", ports.ErrInvalid)
	}
	if record.Status.State == "" {
		return fmt.Errorf("%w: workload state is required", ports.ErrInvalid)
	}
	return nil
}

func firstNonNilStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (s *MetadataInstanceStore) Get(ctx context.Context, tenantID string, instanceID string) (ports.WorkloadInstanceRecord, error) {
	if s.store == nil {
		return ports.WorkloadInstanceRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(instanceID) == "" {
		return ports.WorkloadInstanceRecord{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}

	var record ports.WorkloadInstanceRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			SELECT tenant_id::text, instance_id, name, workload_kind, COALESCE(provider, ''),
				COALESCE(audit_id::text, ''), COALESCE(provider_id, ''), resource_refs,
				state, COALESCE(endpoint, ''), COALESCE(node_name, ''), COALESCE(reason, ''),
				networks, storage, lifecycle_policy, ssh_connection, snapshots, container_status, gpu_status,
				COALESCE(description, ''), labels, image_summary, compute_summary, network_summary,
				access_summary, storage_attachments, sandbox_status, quota_tx_ids, created_at, updated_at
			FROM workload_instances
			WHERE tenant_id = $1::uuid AND instance_id = $2
		`, tenantID, instanceID)
		return scanWorkloadInstance(row, &record)
	})
	if err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	return record, nil
}

func (s *MetadataInstanceStore) List(ctx context.Context, tenantID string, kind ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("%w: tenantID is required", ports.ErrInvalid)
	}

	var records []ports.WorkloadInstanceRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id::text, instance_id, name, workload_kind, COALESCE(provider, ''),
				COALESCE(audit_id::text, ''), COALESCE(provider_id, ''), resource_refs,
				state, COALESCE(endpoint, ''), COALESCE(node_name, ''), COALESCE(reason, ''),
				networks, storage, lifecycle_policy, ssh_connection, snapshots, container_status, gpu_status,
				COALESCE(description, ''), labels, image_summary, compute_summary, network_summary,
				access_summary, storage_attachments, sandbox_status, quota_tx_ids, created_at, updated_at
			FROM workload_instances
			WHERE tenant_id = $1::uuid AND ($2 = '' OR workload_kind = $2)
			ORDER BY updated_at DESC
		`, tenantID, string(kind))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.WorkloadInstanceRecord
			if err := scanWorkloadInstance(rows, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *MetadataInstanceStore) ListReconcileTargets(ctx context.Context, request ports.ReconcileTargetListRequest) ([]ports.ReconcileTarget, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 100
	}
	staleBefore := request.StaleBefore
	if staleBefore.IsZero() {
		staleBefore = s.now().UTC().Add(-2 * time.Minute)
	}

	var targets []ports.ReconcileTarget
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id::text, instance_id, workload_kind, state, COALESCE(provider, ''), updated_at
			FROM workload_instances
			WHERE state NOT IN ('deleting', 'deleted')
				AND (state NOT IN ('stopped', 'failed') OR updated_at < $1)
			ORDER BY updated_at ASC
			LIMIT $2
		`, staleBefore.UTC(), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var target ports.ReconcileTarget
			var kind string
			var state string
			if err := rows.Scan(&target.TenantID, &target.InstanceID, &kind, &state, &target.Provider, &target.LastObservedAt); err != nil {
				return err
			}
			target.Kind = ports.WorkloadKind(kind)
			target.State = ports.WorkloadState(state)
			targets = append(targets, target)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return targets, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanWorkloadInstance(row scanner, record *ports.WorkloadInstanceRecord) error {
	var kind string
	var state string
	var resourceRefsJSON []byte
	var networksJSON []byte
	var storageJSON []byte
	var lifecyclePolicyJSON []byte
	var sshConnectionJSON []byte
	var snapshotsJSON []byte
	var containerStatusJSON []byte
	var gpuStatusJSON []byte
	var labelsJSON []byte
	var imageSummaryJSON []byte
	var computeSummaryJSON []byte
	var networkSummaryJSON []byte
	var accessSummaryJSON []byte
	var storageAttachmentsJSON []byte
	var sandboxStatusJSON []byte
	var quotaTxIDsJSON []byte
	if err := row.Scan(
		&record.TenantID,
		&record.InstanceID,
		&record.Name,
		&kind,
		&record.Provider,
		&record.AuditID,
		&record.Status.Ref.ProviderID,
		&resourceRefsJSON,
		&state,
		&record.Status.Endpoint,
		&record.Status.NodeName,
		&record.Status.Reason,
		&networksJSON,
		&storageJSON,
		&lifecyclePolicyJSON,
		&sshConnectionJSON,
		&snapshotsJSON,
		&containerStatusJSON,
		&gpuStatusJSON,
		&record.Description,
		&labelsJSON,
		&imageSummaryJSON,
		&computeSummaryJSON,
		&networkSummaryJSON,
		&accessSummaryJSON,
		&storageAttachmentsJSON,
		&sandboxStatusJSON,
		&quotaTxIDsJSON,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return err
	}
	record.Kind = ports.WorkloadKind(kind)
	record.Status.Ref = ports.WorkloadRef{
		TenantID:   record.TenantID,
		InstanceID: record.InstanceID,
		Kind:       record.Kind,
		ProviderID: record.Status.Ref.ProviderID,
	}
	record.Status.State = ports.WorkloadState(state)
	record.Status.UpdatedAt = record.UpdatedAt
	if err := json.Unmarshal(resourceRefsJSON, &record.ResourceRefs); err != nil {
		return fmt.Errorf("unmarshal resource refs: %w", err)
	}
	if err := json.Unmarshal(networksJSON, &record.Status.Networks); err != nil {
		return fmt.Errorf("unmarshal networks: %w", err)
	}
	if err := json.Unmarshal(storageJSON, &record.Status.Storage); err != nil {
		return fmt.Errorf("unmarshal storage: %w", err)
	}
	if len(lifecyclePolicyJSON) > 0 {
		if err := json.Unmarshal(lifecyclePolicyJSON, &record.Lifecycle); err != nil {
			return fmt.Errorf("unmarshal lifecycle policy: %w", err)
		}
	}
	if len(sshConnectionJSON) > 0 && string(sshConnectionJSON) != "{}" {
		var ssh ports.VMSSHConnectionInfo
		if err := json.Unmarshal(sshConnectionJSON, &ssh); err != nil {
			return fmt.Errorf("unmarshal ssh connection: %w", err)
		}
		record.SSH = &ssh
	}
	if len(snapshotsJSON) > 0 {
		if err := json.Unmarshal(snapshotsJSON, &record.Snapshots); err != nil {
			return fmt.Errorf("unmarshal snapshots: %w", err)
		}
	}
	if len(containerStatusJSON) > 0 && string(containerStatusJSON) != "{}" {
		var container ports.ContainerInstanceStatus
		if err := json.Unmarshal(containerStatusJSON, &container); err != nil {
			return fmt.Errorf("unmarshal container status: %w", err)
		}
		record.Container = &container
	}
	if len(gpuStatusJSON) > 0 && string(gpuStatusJSON) != "{}" {
		var gpu ports.GPUInstanceStatus
		if err := json.Unmarshal(gpuStatusJSON, &gpu); err != nil {
			return fmt.Errorf("unmarshal gpu status: %w", err)
		}
		record.GPU = &gpu
	}
	if err := json.Unmarshal(labelsJSON, &record.Labels); err != nil {
		return fmt.Errorf("unmarshal labels: %w", err)
	}
	if err := json.Unmarshal(imageSummaryJSON, &record.Image); err != nil {
		return fmt.Errorf("unmarshal image summary: %w", err)
	}
	if err := json.Unmarshal(computeSummaryJSON, &record.Compute); err != nil {
		return fmt.Errorf("unmarshal compute summary: %w", err)
	}
	if err := json.Unmarshal(networkSummaryJSON, &record.Network); err != nil {
		return fmt.Errorf("unmarshal network summary: %w", err)
	}
	if err := json.Unmarshal(accessSummaryJSON, &record.Access); err != nil {
		return fmt.Errorf("unmarshal access summary: %w", err)
	}
	if err := json.Unmarshal(storageAttachmentsJSON, &record.StorageAttachments); err != nil {
		return fmt.Errorf("unmarshal storage attachments: %w", err)
	}
	if len(sandboxStatusJSON) > 0 && string(sandboxStatusJSON) != "{}" {
		var sandbox ports.SandboxInstanceStatus
		if err := json.Unmarshal(sandboxStatusJSON, &sandbox); err != nil {
			return fmt.Errorf("unmarshal sandbox status: %w", err)
		}
		record.Sandbox = &sandbox
	}
	if len(quotaTxIDsJSON) > 0 && string(quotaTxIDsJSON) != "null" {
		if err := json.Unmarshal(quotaTxIDsJSON, &record.QuotaTxIDs); err != nil {
			return fmt.Errorf("unmarshal quota tx ids: %w", err)
		}
	}
	return nil
}

var _ ports.WorkloadInstanceStore = (*MetadataInstanceStore)(nil)
var _ ports.WorkloadInstanceStoreTx = (*MetadataInstanceStore)(nil)
var _ ports.ReconcileTargetLister = (*MetadataInstanceStore)(nil)

func firstNonNilSSH(ssh *ports.VMSSHConnectionInfo) any {
	if ssh == nil {
		return map[string]any{}
	}
	return ssh
}

func firstNonNilContainer(container *ports.ContainerInstanceStatus) any {
	if container == nil {
		return map[string]any{}
	}
	return container
}

func firstNonNilGPU(gpu *ports.GPUInstanceStatus) any {
	if gpu == nil {
		return map[string]any{}
	}
	return gpu
}

func firstNonNilSandbox(sandbox *ports.SandboxInstanceStatus) any {
	if sandbox == nil {
		return map[string]any{}
	}
	return sandbox
}

func firstNonNilStringMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}
