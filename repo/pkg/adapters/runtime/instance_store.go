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
	now := s.now().UTC()
	createdAt := firstNonZeroTime(record.CreatedAt, now)
	updatedAt := firstNonZeroTime(record.UpdatedAt, record.Status.UpdatedAt, now)

	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO workload_instances (
				tenant_id, instance_id, name, workload_kind, vpc_id, subnet_id, private_ip, provider, audit_id,
				provider_id, resource_refs, state, endpoint, node_name, reason,
				networks, storage, lifecycle_policy, ssh_connection, snapshots, container_status, gpu_status, created_at, updated_at
			)
			VALUES (
				$1::uuid, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, '')::uuid,
				NULLIF($10, ''), $11::jsonb, $12, NULLIF($13, ''), NULLIF($14, ''),
				NULLIF($15, ''), $16::jsonb, $17::jsonb, $18::jsonb, $19::jsonb, $20::jsonb, $21::jsonb, $22::jsonb, $23, $24
			)
			ON CONFLICT (tenant_id, instance_id) DO UPDATE SET
				name = EXCLUDED.name,
				workload_kind = EXCLUDED.workload_kind,
				vpc_id = EXCLUDED.vpc_id,
				subnet_id = EXCLUDED.subnet_id,
				private_ip = EXCLUDED.private_ip,
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
				updated_at = EXCLUDED.updated_at
		`, record.TenantID, record.InstanceID, record.Name, string(record.Kind), record.VPCID, record.SubnetID, record.PrivateIP, record.Provider,
			record.AuditID, record.Status.Ref.ProviderID, string(resourceRefs), string(record.Status.State),
			record.Status.Endpoint, record.Status.NodeName, record.Status.Reason, string(networks), string(storage),
			string(lifecyclePolicy), string(sshConnection), string(snapshots), string(containerStatus), string(gpuStatus), createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("upsert workload instance: %w", err)
		}
		return nil
	})
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
			SELECT tenant_id::text, instance_id, name, workload_kind, COALESCE(vpc_id, ''), COALESCE(subnet_id, ''), COALESCE(private_ip, ''), COALESCE(provider, ''),
				COALESCE(audit_id::text, ''), COALESCE(provider_id, ''), resource_refs,
				state, COALESCE(endpoint, ''), COALESCE(node_name, ''), COALESCE(reason, ''),
				networks, storage, lifecycle_policy, ssh_connection, snapshots, container_status, gpu_status, created_at, updated_at
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
			SELECT tenant_id::text, instance_id, name, workload_kind, COALESCE(vpc_id, ''), COALESCE(subnet_id, ''), COALESCE(private_ip, ''), COALESCE(provider, ''),
				COALESCE(audit_id::text, ''), COALESCE(provider_id, ''), resource_refs,
				state, COALESCE(endpoint, ''), COALESCE(node_name, ''), COALESCE(reason, ''),
				networks, storage, lifecycle_policy, ssh_connection, snapshots, container_status, gpu_status, created_at, updated_at
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
			WHERE state NOT IN ('stopped', 'failed', 'deleted') OR updated_at < $1
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
	if err := row.Scan(
		&record.TenantID,
		&record.InstanceID,
		&record.Name,
		&kind,
		&record.VPCID,
		&record.SubnetID,
		&record.PrivateIP,
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
	return nil
}

var _ ports.WorkloadInstanceStore = (*MetadataInstanceStore)(nil)
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
