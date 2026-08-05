package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
)

func (s *MetadataStorageStore) UpsertBucket(ctx context.Context, record ports.StorageBucketRecord) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.BucketID) == "" || strings.TrimSpace(record.Name) == "" {
		return fmt.Errorf("%w: tenant_id, bucket_id and name are required", ports.ErrInvalid)
	}
	state := record.State
	if state == "" {
		state = ports.StorageResourceAvailable
	}
	createdAt, updatedAt := networkRecordTimes(s.now, record.CreatedAt, record.UpdatedAt)
	var deletedAt any
	if !record.DeletedAt.IsZero() {
		deletedAt = record.DeletedAt.UTC()
	} else if state == ports.StorageResourceDeleted {
		deletedAt = updatedAt
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO storage_buckets (
				tenant_id, bucket_id, name, region, endpoint, access_mode, acl, acl_label,
				storage_class, versioning, object_count, size_bytes, lifecycle_note,
				state, reason, deleted_at, create_idempotency_key, create_request_fingerprint,
				created_at, updated_at
			) VALUES (
				$1::uuid, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, NULLIF($7, ''), NULLIF($8, ''),
				NULLIF($9, ''), NULLIF($10, ''), $11, $12, NULLIF($13, ''),
				$14, NULLIF($15, ''), $16, NULLIF($17, ''), NULLIF($18, ''),
				$19, $20
			)
			ON CONFLICT (tenant_id, bucket_id) DO UPDATE SET
				name = EXCLUDED.name,
				region = EXCLUDED.region,
				endpoint = EXCLUDED.endpoint,
				access_mode = EXCLUDED.access_mode,
				acl = EXCLUDED.acl,
				acl_label = EXCLUDED.acl_label,
				storage_class = EXCLUDED.storage_class,
				versioning = EXCLUDED.versioning,
				object_count = EXCLUDED.object_count,
				size_bytes = EXCLUDED.size_bytes,
				lifecycle_note = EXCLUDED.lifecycle_note,
				state = EXCLUDED.state,
				reason = EXCLUDED.reason,
				deleted_at = EXCLUDED.deleted_at,
				create_idempotency_key = COALESCE(EXCLUDED.create_idempotency_key, storage_buckets.create_idempotency_key),
				create_request_fingerprint = COALESCE(EXCLUDED.create_request_fingerprint, storage_buckets.create_request_fingerprint),
				updated_at = EXCLUDED.updated_at
		`, record.TenantID, record.BucketID, record.Name, record.Region, record.Endpoint, firstNetworkNonEmpty(record.AccessMode, "private"),
			record.ACL, record.ACLLabel, record.StorageClass, record.Versioning, record.ObjectCount, record.SizeBytes, record.LifecycleNote,
			string(state), record.Reason, deletedAt, record.CreateIdempotencyKey, record.CreateRequestFingerprint, createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("upsert storage bucket: %w", err)
		}
		return replaceBucketLifecycleRules(ctx, tx, record.TenantID, record.BucketID, record.LifecycleRules)
	})
}

func (s *MetadataStorageStore) GetBucket(ctx context.Context, tenantID string, bucketID string) (ports.StorageBucketRecord, error) {
	if s.store == nil {
		return ports.StorageBucketRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(bucketID) == "" {
		return ports.StorageBucketRecord{}, fmt.Errorf("%w: tenant_id and bucket_id are required", ports.ErrInvalid)
	}
	var record ports.StorageBucketRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, storageBucketSelectSQL+`
			WHERE tenant_id = $1::uuid AND bucket_id = $2 AND deleted_at IS NULL AND state <> 'deleted'
		`, tenantID, bucketID)
		if err := scanStorageBucket(row, &record); err != nil {
			return err
		}
		rules, err := listBucketLifecycleRules(ctx, tx, tenantID, bucketID)
		if err != nil {
			return err
		}
		record.LifecycleRules = rules
		return nil
	})
	if err != nil {
		return ports.StorageBucketRecord{}, err
	}
	return record, nil
}

func (s *MetadataStorageStore) ListBuckets(ctx context.Context, tenantID string) ([]ports.StorageBucketRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	var records []ports.StorageBucketRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, storageBucketSelectSQL+`
			WHERE tenant_id = $1::uuid AND deleted_at IS NULL AND state <> 'deleted'
			ORDER BY created_at DESC
		`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.StorageBucketRecord
			if err := scanStorageBucket(rows, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Load lifecycle rules after the parent cursor is exhausted; pgx forbids
		// nested queries on the same connection ("conn busy").
		rows.Close()
		for i := range records {
			rules, err := listBucketLifecycleRules(ctx, tx, tenantID, records[i].BucketID)
			if err != nil {
				return err
			}
			records[i].LifecycleRules = rules
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *MetadataStorageStore) FindBucketByCreateIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (ports.StorageBucketRecord, error) {
	if s.store == nil {
		return ports.StorageBucketRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return ports.StorageBucketRecord{}, fmt.Errorf("%w: tenant_id and create_idempotency_key are required", ports.ErrInvalid)
	}
	var record ports.StorageBucketRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, storageBucketSelectSQL+`
			WHERE tenant_id = $1::uuid AND create_idempotency_key = $2
		`, tenantID, idempotencyKey)
		if err := scanStorageBucket(row, &record); err != nil {
			return err
		}
		rules, err := listBucketLifecycleRules(ctx, tx, tenantID, record.BucketID)
		if err != nil {
			return err
		}
		record.LifecycleRules = rules
		return nil
	})
	if err != nil {
		return ports.StorageBucketRecord{}, err
	}
	return record, nil
}

func (s *MetadataStorageStore) ReplaceBucketLifecycleRules(ctx context.Context, tenantID string, bucketID string, rules []ports.StorageBucketLifecycleRule) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		return replaceBucketLifecycleRules(ctx, tx, tenantID, bucketID, rules)
	})
}

func (s *MetadataStorageStore) UpsertVolumeSnapshot(ctx context.Context, record ports.VolumeSnapshotRecord) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.SnapshotID) == "" || strings.TrimSpace(record.VolumeID) == "" {
		return fmt.Errorf("%w: tenant_id, snapshot_id and volume_id are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.Name) == "" {
		return fmt.Errorf("%w: name is required", ports.ErrInvalid)
	}
	createdAt, updatedAt := networkRecordTimes(s.now, record.CreatedAt, record.UpdatedAt)
	var deletedAt any
	if !record.DeletedAt.IsZero() {
		deletedAt = record.DeletedAt.UTC()
	}
	status := volumeSnapshotStatusToPG(record.Status)
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO storage_volume_snapshots (
				tenant_id, snapshot_id, volume_id, name, description, status, size_bytes,
				deleted_at, create_idempotency_key, create_request_fingerprint, created_at, updated_at
			) VALUES (
				$1::uuid, $2, $3, $4, NULLIF($5, ''), $6, $7,
				$8, NULLIF($9, ''), NULLIF($10, ''), $11, $12
			)
			ON CONFLICT (tenant_id, snapshot_id) DO UPDATE SET
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				status = EXCLUDED.status,
				size_bytes = EXCLUDED.size_bytes,
				deleted_at = EXCLUDED.deleted_at,
				create_idempotency_key = COALESCE(EXCLUDED.create_idempotency_key, storage_volume_snapshots.create_idempotency_key),
				create_request_fingerprint = COALESCE(EXCLUDED.create_request_fingerprint, storage_volume_snapshots.create_request_fingerprint),
				updated_at = EXCLUDED.updated_at
		`, record.TenantID, record.SnapshotID, record.VolumeID, record.Name, record.Description, status, record.SizeBytes,
			deletedAt, record.CreateIdempotencyKey, record.CreateRequestFingerprint, createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("upsert volume snapshot: %w", err)
		}
		return nil
	})
}

func (s *MetadataStorageStore) ListVolumeSnapshots(ctx context.Context, tenantID string, volumeID string) ([]ports.VolumeSnapshotRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(volumeID) == "" {
		return nil, fmt.Errorf("%w: tenant_id and volume_id are required", ports.ErrInvalid)
	}
	var records []ports.VolumeSnapshotRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id::text, snapshot_id, volume_id, name, COALESCE(description, ''), status,
				COALESCE(size_bytes, 0), created_at, updated_at,
				COALESCE(create_idempotency_key, ''), COALESCE(create_request_fingerprint, '')
			FROM storage_volume_snapshots
			WHERE tenant_id = $1::uuid AND volume_id = $2 AND deleted_at IS NULL AND status <> 'deleted'
			ORDER BY created_at DESC
		`, tenantID, volumeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.VolumeSnapshotRecord
			var status string
			if err := rows.Scan(
				&record.TenantID, &record.SnapshotID, &record.VolumeID, &record.Name, &record.Description, &status,
				&record.SizeBytes, &record.CreatedAt, &record.UpdatedAt,
				&record.CreateIdempotencyKey, &record.CreateRequestFingerprint,
			); err != nil {
				return err
			}
			record.Status = volumeSnapshotStatusFromPG(status)
			records = append(records, record)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *MetadataStorageStore) FindVolumeSnapshotByCreateIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (ports.VolumeSnapshotRecord, error) {
	if s.store == nil {
		return ports.VolumeSnapshotRecord{}, ports.ErrNotConfigured
	}
	var record ports.VolumeSnapshotRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			SELECT tenant_id::text, snapshot_id, volume_id, name, COALESCE(description, ''), status,
				COALESCE(size_bytes, 0), created_at, updated_at,
				COALESCE(create_idempotency_key, ''), COALESCE(create_request_fingerprint, '')
			FROM storage_volume_snapshots
			WHERE tenant_id = $1::uuid AND create_idempotency_key = $2
		`, tenantID, idempotencyKey)
		var status string
		if err := row.Scan(
			&record.TenantID, &record.SnapshotID, &record.VolumeID, &record.Name, &record.Description, &status,
			&record.SizeBytes, &record.CreatedAt, &record.UpdatedAt,
			&record.CreateIdempotencyKey, &record.CreateRequestFingerprint,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) || isNoRows(err) {
				return ports.ErrNotFound
			}
			return err
		}
		record.Status = volumeSnapshotStatusFromPG(status)
		return nil
	})
	if err != nil {
		return ports.VolumeSnapshotRecord{}, err
	}
	return record, nil
}

func (s *MetadataStorageStore) UpsertFilesystemMountTarget(ctx context.Context, record ports.FilesystemMountTargetRecord) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.MountTargetID) == "" || strings.TrimSpace(record.FilesystemID) == "" {
		return fmt.Errorf("%w: tenant_id, mount_target_id and filesystem_id are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.SubnetID) == "" {
		return fmt.Errorf("%w: subnet_id is required", ports.ErrInvalid)
	}
	createdAt, updatedAt := networkRecordTimes(s.now, record.CreatedAt, record.UpdatedAt)
	var deletedAt any
	if !record.DeletedAt.IsZero() {
		deletedAt = record.DeletedAt.UTC()
	}
	status := mountTargetStatusToPG(record.Status)
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO storage_filesystem_mount_targets (
				tenant_id, mount_target_id, filesystem_id, subnet_id, vpc_id, ip_address, status,
				deleted_at, create_idempotency_key, create_request_fingerprint, created_at, updated_at
			) VALUES (
				$1::uuid, $2, $3, $4, NULLIF($5, ''), NULLIF($6, '')::inet, $7,
				$8, NULLIF($9, ''), NULLIF($10, ''), $11, $12
			)
			ON CONFLICT (tenant_id, mount_target_id) DO UPDATE SET
				subnet_id = EXCLUDED.subnet_id,
				vpc_id = EXCLUDED.vpc_id,
				ip_address = EXCLUDED.ip_address,
				status = EXCLUDED.status,
				deleted_at = EXCLUDED.deleted_at,
				create_idempotency_key = COALESCE(EXCLUDED.create_idempotency_key, storage_filesystem_mount_targets.create_idempotency_key),
				create_request_fingerprint = COALESCE(EXCLUDED.create_request_fingerprint, storage_filesystem_mount_targets.create_request_fingerprint),
				updated_at = EXCLUDED.updated_at
		`, record.TenantID, record.MountTargetID, record.FilesystemID, record.SubnetID, record.VPCID, record.IPAddress, status,
			deletedAt, record.CreateIdempotencyKey, record.CreateRequestFingerprint, createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("upsert filesystem mount target: %w", err)
		}
		return nil
	})
}

func (s *MetadataStorageStore) ListFilesystemMountTargets(ctx context.Context, tenantID string, filesystemID string) ([]ports.FilesystemMountTargetRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(filesystemID) == "" {
		return nil, fmt.Errorf("%w: tenant_id and filesystem_id are required", ports.ErrInvalid)
	}
	var records []ports.FilesystemMountTargetRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id::text, mount_target_id, filesystem_id, subnet_id, COALESCE(vpc_id, ''),
				COALESCE(host(ip_address), ''), status, created_at, updated_at,
				COALESCE(create_idempotency_key, ''), COALESCE(create_request_fingerprint, '')
			FROM storage_filesystem_mount_targets
			WHERE tenant_id = $1::uuid AND filesystem_id = $2 AND deleted_at IS NULL AND status <> 'deleted'
			ORDER BY created_at DESC
		`, tenantID, filesystemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.FilesystemMountTargetRecord
			var status string
			if err := rows.Scan(
				&record.TenantID, &record.MountTargetID, &record.FilesystemID, &record.SubnetID, &record.VPCID,
				&record.IPAddress, &status, &record.CreatedAt, &record.UpdatedAt,
				&record.CreateIdempotencyKey, &record.CreateRequestFingerprint,
			); err != nil {
				return err
			}
			record.Status = mountTargetStatusFromPG(status)
			records = append(records, record)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *MetadataStorageStore) FindFilesystemMountTargetByCreateIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (ports.FilesystemMountTargetRecord, error) {
	if s.store == nil {
		return ports.FilesystemMountTargetRecord{}, ports.ErrNotConfigured
	}
	var record ports.FilesystemMountTargetRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			SELECT tenant_id::text, mount_target_id, filesystem_id, subnet_id, COALESCE(vpc_id, ''),
				COALESCE(host(ip_address), ''), status, created_at, updated_at,
				COALESCE(create_idempotency_key, ''), COALESCE(create_request_fingerprint, '')
			FROM storage_filesystem_mount_targets
			WHERE tenant_id = $1::uuid AND create_idempotency_key = $2
		`, tenantID, idempotencyKey)
		var status string
		if err := row.Scan(
			&record.TenantID, &record.MountTargetID, &record.FilesystemID, &record.SubnetID, &record.VPCID,
			&record.IPAddress, &status, &record.CreatedAt, &record.UpdatedAt,
			&record.CreateIdempotencyKey, &record.CreateRequestFingerprint,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) || isNoRows(err) {
				return ports.ErrNotFound
			}
			return err
		}
		record.Status = mountTargetStatusFromPG(status)
		return nil
	})
	if err != nil {
		return ports.FilesystemMountTargetRecord{}, err
	}
	return record, nil
}

const storageBucketSelectSQL = `
	SELECT tenant_id::text, bucket_id::text, name, COALESCE(region, ''), COALESCE(endpoint, ''),
		COALESCE(access_mode, 'private'), COALESCE(acl, ''), COALESCE(acl_label, ''), COALESCE(storage_class, ''),
		COALESCE(versioning, ''), COALESCE(object_count, 0), COALESCE(size_bytes, 0), COALESCE(lifecycle_note, ''),
		COALESCE(state, 'available'), COALESCE(reason, ''), created_at, updated_at,
		COALESCE(create_idempotency_key, ''), COALESCE(create_request_fingerprint, '')
	FROM storage_buckets
`

func scanStorageBucket(row storageScanner, record *ports.StorageBucketRecord) error {
	var state string
	var objectCount int64
	err := row.Scan(
		&record.TenantID,
		&record.BucketID,
		&record.Name,
		&record.Region,
		&record.Endpoint,
		&record.AccessMode,
		&record.ACL,
		&record.ACLLabel,
		&record.StorageClass,
		&record.Versioning,
		&objectCount,
		&record.SizeBytes,
		&record.LifecycleNote,
		&state,
		&record.Reason,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.CreateIdempotencyKey,
		&record.CreateRequestFingerprint,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ports.ErrNotFound) || isNoRows(err) {
			return ports.ErrNotFound
		}
		return err
	}
	record.ObjectCount = int(objectCount)
	record.State = ports.StorageResourceState(state)
	return nil
}

func listBucketLifecycleRules(ctx context.Context, tx ports.MetadataTx, tenantID string, bucketID string) ([]ports.StorageBucketLifecycleRule, error) {
	rows, err := tx.Query(ctx, `
		SELECT rule_id, name, COALESCE(prefix, ''), COALESCE(expire_days, 0), COALESCE(to_infrequent_days, 0), enabled
		FROM storage_bucket_lifecycle_rules
		WHERE tenant_id = $1::uuid AND bucket_id = $2 AND deleted_at IS NULL
		ORDER BY created_at ASC
	`, tenantID, bucketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := make([]ports.StorageBucketLifecycleRule, 0)
	for rows.Next() {
		var rule ports.StorageBucketLifecycleRule
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Prefix, &rule.ExpireDays, &rule.ToInfrequentDays, &rule.Enabled); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func replaceBucketLifecycleRules(ctx context.Context, tx ports.MetadataTx, tenantID string, bucketID string, rules []ports.StorageBucketLifecycleRule) error {
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE storage_bucket_lifecycle_rules
		SET deleted_at = $3, updated_at = $3
		WHERE tenant_id = $1::uuid AND bucket_id = $2 AND deleted_at IS NULL
	`, tenantID, bucketID, now); err != nil {
		return fmt.Errorf("soft-delete bucket lifecycle rules: %w", err)
	}
	for _, rule := range rules {
		ruleID := strings.TrimSpace(rule.ID)
		if ruleID == "" {
			ruleID = "blr_" + strings.ReplaceAll(rule.Name, " ", "_")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO storage_bucket_lifecycle_rules (
				tenant_id, rule_id, bucket_id, name, prefix, expire_days, to_infrequent_days, enabled, created_at, updated_at
			) VALUES ($1::uuid, $2, $3, $4, NULLIF($5, ''), NULLIF($6, 0), NULLIF($7, 0), $8, $9, $9)
			ON CONFLICT (tenant_id, rule_id) DO UPDATE SET
				name = EXCLUDED.name,
				prefix = EXCLUDED.prefix,
				expire_days = EXCLUDED.expire_days,
				to_infrequent_days = EXCLUDED.to_infrequent_days,
				enabled = EXCLUDED.enabled,
				deleted_at = NULL,
				updated_at = EXCLUDED.updated_at
		`, tenantID, ruleID, bucketID, rule.Name, rule.Prefix, rule.ExpireDays, rule.ToInfrequentDays, rule.Enabled, now); err != nil {
			return fmt.Errorf("upsert bucket lifecycle rule: %w", err)
		}
	}
	return nil
}

func volumeSnapshotStatusToPG(status ports.VolumeSnapshotStatus) string {
	switch status {
	case ports.VolumeSnapshotCreating:
		return "pending"
	case ports.VolumeSnapshotError:
		return "failed"
	case ports.VolumeSnapshotDeleting:
		return "deleting"
	case ports.VolumeSnapshotAvailable:
		return "available"
	default:
		if strings.TrimSpace(string(status)) == "" {
			return "available"
		}
		return string(status)
	}
}

func volumeSnapshotStatusFromPG(status string) ports.VolumeSnapshotStatus {
	switch status {
	case "pending":
		return ports.VolumeSnapshotCreating
	case "failed":
		return ports.VolumeSnapshotError
	case "deleting":
		return ports.VolumeSnapshotDeleting
	case "available":
		return ports.VolumeSnapshotAvailable
	default:
		return ports.VolumeSnapshotStatus(status)
	}
}

func mountTargetStatusToPG(status ports.MountTargetStatus) string {
	switch status {
	case ports.MountTargetCreating:
		return "pending"
	case ports.MountTargetError:
		return "failed"
	case ports.MountTargetDeleting:
		return "deleting"
	case ports.MountTargetAvailable:
		return "available"
	default:
		if strings.TrimSpace(string(status)) == "" {
			return "available"
		}
		return string(status)
	}
}

func mountTargetStatusFromPG(status string) ports.MountTargetStatus {
	switch status {
	case "pending":
		return ports.MountTargetCreating
	case "failed":
		return ports.MountTargetError
	case "deleting":
		return ports.MountTargetDeleting
	case "available":
		return ports.MountTargetAvailable
	default:
		return ports.MountTargetStatus(status)
	}
}
