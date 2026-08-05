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

type MetadataStorageStore struct {
	store ports.MetadataStore
	now   func() time.Time
}

type StorageStoreOption func(*MetadataStorageStore)

func WithStorageStoreClock(now func() time.Time) StorageStoreOption {
	return func(store *MetadataStorageStore) {
		if now != nil {
			store.now = now
		}
	}
}

func NewMetadataStorageStore(store ports.MetadataStore, options ...StorageStoreOption) *MetadataStorageStore {
	storageStore := &MetadataStorageStore{
		store: store,
		now:   time.Now,
	}
	for _, option := range options {
		option(storageStore)
	}
	return storageStore
}

func (s *MetadataStorageStore) UpsertVolume(ctx context.Context, record ports.StorageVolumeRecord) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if err := requireStorageRecord(record.TenantID, record.VolumeID, record.Name, record.State); err != nil {
		return err
	}
	if record.SizeGiB <= 0 {
		return fmt.Errorf("%w: volume size_gib must be greater than zero", ports.ErrInvalid)
	}
	createdAt, updatedAt := networkRecordTimes(s.now, record.CreatedAt, record.UpdatedAt)
	var deletedAt any
	if !record.DeletedAt.IsZero() {
		deletedAt = record.DeletedAt.UTC()
	} else if record.State == ports.StorageResourceDeleted {
		deletedAt = updatedAt
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO storage_volumes (
				tenant_id, volume_id, name, size_gib, storage_class, state, reason,
				zone, volume_type, iops, encrypted,
				mount_instance_id, mount_route, mount_name,
				os_init_status, os_init_device,
				from_snapshot_id, from_snapshot_name,
				deleted_at, create_idempotency_key, create_request_fingerprint,
				created_at, updated_at
			) VALUES (
				$1::uuid, $2, $3, $4, $5, $6, NULLIF($7, ''),
				NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, 0), $11,
				NULLIF($12, ''), NULLIF($13, ''), NULLIF($14, ''),
				NULLIF($15, ''), NULLIF($16, ''),
				NULLIF($17, ''), NULLIF($18, ''),
				$19, NULLIF($20, ''), NULLIF($21, ''),
				$22, $23
			)
			ON CONFLICT (tenant_id, volume_id) DO UPDATE SET
				name = EXCLUDED.name,
				size_gib = EXCLUDED.size_gib,
				storage_class = EXCLUDED.storage_class,
				state = EXCLUDED.state,
				reason = EXCLUDED.reason,
				zone = EXCLUDED.zone,
				volume_type = EXCLUDED.volume_type,
				iops = EXCLUDED.iops,
				encrypted = EXCLUDED.encrypted,
				mount_instance_id = EXCLUDED.mount_instance_id,
				mount_route = EXCLUDED.mount_route,
				mount_name = EXCLUDED.mount_name,
				os_init_status = EXCLUDED.os_init_status,
				os_init_device = EXCLUDED.os_init_device,
				from_snapshot_id = EXCLUDED.from_snapshot_id,
				from_snapshot_name = EXCLUDED.from_snapshot_name,
				deleted_at = EXCLUDED.deleted_at,
				create_idempotency_key = COALESCE(EXCLUDED.create_idempotency_key, storage_volumes.create_idempotency_key),
				create_request_fingerprint = COALESCE(EXCLUDED.create_request_fingerprint, storage_volumes.create_request_fingerprint),
				updated_at = EXCLUDED.updated_at
		`, record.TenantID, record.VolumeID, record.Name, record.SizeGiB, record.StorageClass, string(record.State), record.Reason,
			record.Zone, record.VolumeType, record.IOPS, record.Encrypted,
			record.MountInstanceID, record.MountRoute, record.MountName,
			record.OSInitStatus, record.OSInitDevice,
			record.FromSnapshotID, record.FromSnapshotName,
			deletedAt, record.CreateIdempotencyKey, record.CreateRequestFingerprint,
			createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("upsert storage volume: %w", err)
		}
		if err := upsertVolumeAutoSnapshotPolicy(ctx, tx, record); err != nil {
			return err
		}
		return replaceVolumeMountEvents(ctx, tx, record)
	})
}

func (s *MetadataStorageStore) GetVolume(ctx context.Context, tenantID string, volumeID string) (ports.StorageVolumeRecord, error) {
	if s.store == nil {
		return ports.StorageVolumeRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(volumeID) == "" {
		return ports.StorageVolumeRecord{}, fmt.Errorf("%w: tenant_id and volume_id are required", ports.ErrInvalid)
	}
	var record ports.StorageVolumeRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, storageVolumeSelectSQL+`
			WHERE tenant_id = $1::uuid AND volume_id = $2 AND deleted_at IS NULL
		`, tenantID, volumeID)
		if err := scanStorageVolume(row, &record); err != nil {
			return err
		}
		return loadVolumeChildren(ctx, tx, &record)
	})
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return record, nil
}

func (s *MetadataStorageStore) ListVolumes(ctx context.Context, tenantID string) ([]ports.StorageVolumeRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	var records []ports.StorageVolumeRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, storageVolumeSelectSQL+`
			WHERE tenant_id = $1::uuid AND deleted_at IS NULL AND state <> 'deleted'
			ORDER BY updated_at DESC
		`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.StorageVolumeRecord
			if err := scanStorageVolume(rows, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Load children after the parent cursor is exhausted; pgx forbids nested
		// queries on the same connection ("conn busy").
		rows.Close()
		for i := range records {
			if err := loadVolumeChildren(ctx, tx, &records[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *MetadataStorageStore) FindVolumeByCreateIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (ports.StorageVolumeRecord, error) {
	if s.store == nil {
		return ports.StorageVolumeRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return ports.StorageVolumeRecord{}, fmt.Errorf("%w: tenant_id and create_idempotency_key are required", ports.ErrInvalid)
	}
	var record ports.StorageVolumeRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, storageVolumeSelectSQL+`
			WHERE tenant_id = $1::uuid AND create_idempotency_key = $2
		`, tenantID, idempotencyKey)
		if err := scanStorageVolume(row, &record); err != nil {
			return err
		}
		return loadVolumeChildren(ctx, tx, &record)
	})
	if err != nil {
		return ports.StorageVolumeRecord{}, err
	}
	return record, nil
}

func (s *MetadataStorageStore) UpsertFilesystem(ctx context.Context, record ports.StorageFilesystemRecord) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if err := requireStorageRecord(record.TenantID, record.FilesystemID, record.Name, record.State); err != nil {
		return err
	}
	if record.SizeGiB <= 0 {
		return fmt.Errorf("%w: filesystem size_gib must be greater than zero", ports.ErrInvalid)
	}
	createdAt, updatedAt := networkRecordTimes(s.now, record.CreatedAt, record.UpdatedAt)
	var deletedAt any
	if !record.DeletedAt.IsZero() {
		deletedAt = record.DeletedAt.UTC()
	} else if record.State == ports.StorageResourceDeleted {
		deletedAt = updatedAt
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO storage_filesystems (
				tenant_id, filesystem_id, name, protocol, size_gib, endpoint, state, reason,
				zone, performance_mode, mount_command,
				deleted_at, create_idempotency_key, create_request_fingerprint,
				created_at, updated_at
			) VALUES (
				$1::uuid, $2, $3, $4, $5, NULLIF($6, ''), $7, NULLIF($8, ''),
				NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''),
				$12, NULLIF($13, ''), NULLIF($14, ''),
				$15, $16
			)
			ON CONFLICT (tenant_id, filesystem_id) DO UPDATE SET
				name = EXCLUDED.name,
				protocol = EXCLUDED.protocol,
				size_gib = EXCLUDED.size_gib,
				endpoint = EXCLUDED.endpoint,
				state = EXCLUDED.state,
				reason = EXCLUDED.reason,
				zone = EXCLUDED.zone,
				performance_mode = EXCLUDED.performance_mode,
				mount_command = EXCLUDED.mount_command,
				deleted_at = EXCLUDED.deleted_at,
				create_idempotency_key = COALESCE(EXCLUDED.create_idempotency_key, storage_filesystems.create_idempotency_key),
				create_request_fingerprint = COALESCE(EXCLUDED.create_request_fingerprint, storage_filesystems.create_request_fingerprint),
				updated_at = EXCLUDED.updated_at
		`, record.TenantID, record.FilesystemID, record.Name, record.Protocol, record.SizeGiB, record.Endpoint, string(record.State), record.Reason,
			record.Zone, record.PerformanceMode, record.MountCommand,
			deletedAt, record.CreateIdempotencyKey, record.CreateRequestFingerprint,
			createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("upsert storage filesystem: %w", err)
		}
		return nil
	})
}

func (s *MetadataStorageStore) GetFilesystem(ctx context.Context, tenantID string, filesystemID string) (ports.StorageFilesystemRecord, error) {
	if s.store == nil {
		return ports.StorageFilesystemRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(filesystemID) == "" {
		return ports.StorageFilesystemRecord{}, fmt.Errorf("%w: tenant_id and filesystem_id are required", ports.ErrInvalid)
	}
	var record ports.StorageFilesystemRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			SELECT tenant_id::text, filesystem_id, name, protocol, size_gib, COALESCE(endpoint, ''),
				COALESCE(zone, ''), COALESCE(performance_mode, ''), COALESCE(mount_command, ''),
				state, COALESCE(reason, ''), created_at, updated_at
			FROM storage_filesystems
			WHERE tenant_id = $1::uuid AND filesystem_id = $2 AND deleted_at IS NULL
		`, tenantID, filesystemID)
		return scanStorageFilesystem(row, &record)
	})
	if err != nil {
		return ports.StorageFilesystemRecord{}, err
	}
	return record, nil
}

func (s *MetadataStorageStore) ListFilesystems(ctx context.Context, tenantID string) ([]ports.StorageFilesystemRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	var records []ports.StorageFilesystemRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id::text, filesystem_id, name, protocol, size_gib, COALESCE(endpoint, ''),
				COALESCE(zone, ''), COALESCE(performance_mode, ''), COALESCE(mount_command, ''),
				state, COALESCE(reason, ''), created_at, updated_at
			FROM storage_filesystems
			WHERE tenant_id = $1::uuid AND deleted_at IS NULL AND state <> 'deleted'
			ORDER BY updated_at DESC
		`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.StorageFilesystemRecord
			if err := scanStorageFilesystem(rows, &record); err != nil {
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

func (s *MetadataStorageStore) UpsertObject(ctx context.Context, record ports.StorageObjectRecord) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if strings.TrimSpace(record.TenantID) == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.ObjectID) == "" {
		return fmt.Errorf("%w: object id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.Bucket) == "" || strings.TrimSpace(record.Key) == "" {
		return fmt.Errorf("%w: bucket and key are required", ports.ErrInvalid)
	}
	if record.State == "" {
		return fmt.Errorf("%w: state is required", ports.ErrInvalid)
	}
	if record.SizeBytes < 0 {
		return fmt.Errorf("%w: object size_bytes must not be negative", ports.ErrInvalid)
	}
	createdAt, updatedAt := networkRecordTimes(s.now, record.CreatedAt, record.UpdatedAt)
	var deletedAt any
	if !record.DeletedAt.IsZero() {
		deletedAt = record.DeletedAt.UTC()
	} else if record.State == ports.StorageResourceDeleted {
		deletedAt = updatedAt
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO storage_objects (
				tenant_id, object_id, bucket, object_key, size_bytes, content_type, state, reason,
				deleted_at, create_idempotency_key, create_request_fingerprint, created_at, updated_at
			) VALUES (
				$1::uuid, $2, $3, $4, $5, $6, $7, NULLIF($8, ''),
				$9, NULLIF($10, ''), NULLIF($11, ''), $12, $13
			)
			ON CONFLICT (tenant_id, object_id) DO UPDATE SET
				bucket = EXCLUDED.bucket,
				object_key = EXCLUDED.object_key,
				size_bytes = EXCLUDED.size_bytes,
				content_type = EXCLUDED.content_type,
				state = EXCLUDED.state,
				reason = EXCLUDED.reason,
				deleted_at = EXCLUDED.deleted_at,
				create_idempotency_key = COALESCE(EXCLUDED.create_idempotency_key, storage_objects.create_idempotency_key),
				create_request_fingerprint = COALESCE(EXCLUDED.create_request_fingerprint, storage_objects.create_request_fingerprint),
				updated_at = EXCLUDED.updated_at
		`, record.TenantID, record.ObjectID, record.Bucket, record.Key, record.SizeBytes, record.ContentType, string(record.State), record.Reason,
			deletedAt, record.CreateIdempotencyKey, record.CreateRequestFingerprint, createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("upsert storage object: %w", err)
		}
		return nil
	})
}

func (s *MetadataStorageStore) GetObject(ctx context.Context, tenantID string, objectID string) (ports.StorageObjectRecord, error) {
	if s.store == nil {
		return ports.StorageObjectRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(objectID) == "" {
		return ports.StorageObjectRecord{}, fmt.Errorf("%w: tenant_id and object_id are required", ports.ErrInvalid)
	}
	var record ports.StorageObjectRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			SELECT tenant_id::text, object_id, bucket, object_key, size_bytes, content_type,
				state, COALESCE(reason, ''), created_at, updated_at
			FROM storage_objects
			WHERE tenant_id = $1::uuid AND object_id = $2 AND deleted_at IS NULL
		`, tenantID, objectID)
		return scanStorageObject(row, &record)
	})
	if err != nil {
		return ports.StorageObjectRecord{}, err
	}
	return record, nil
}

func (s *MetadataStorageStore) ListObjects(ctx context.Context, tenantID string) ([]ports.StorageObjectRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	var records []ports.StorageObjectRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id::text, object_id, bucket, object_key, size_bytes, content_type,
				state, COALESCE(reason, ''), created_at, updated_at
			FROM storage_objects
			WHERE tenant_id = $1::uuid AND deleted_at IS NULL AND state <> 'deleted'
			ORDER BY updated_at DESC
		`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.StorageObjectRecord
			if err := scanStorageObject(rows, &record); err != nil {
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

func (s *MetadataStorageStore) UpdateResourceState(ctx context.Context, request ports.StorageResourceStateUpdateRequest) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if err := requireStorageStateUpdate(request); err != nil {
		return err
	}
	table, idColumn, err := storageResourceStateTable(request.ResourceKind)
	if err != nil {
		return err
	}
	updatedAt := firstNonZeroTime(request.UpdatedAt, s.now().UTC())
	deletedAtExpr := "NULL"
	if request.State == ports.StorageResourceDeleted {
		deletedAtExpr = "$5"
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		tag, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %s
			SET state = $3,
				reason = NULLIF($4, ''),
				updated_at = $5,
				deleted_at = %s
			WHERE tenant_id = $1::uuid AND %s = $2
		`, table, deletedAtExpr, idColumn), request.TenantID, request.ResourceID, string(request.State), request.Reason, updatedAt)
		if err != nil {
			return fmt.Errorf("update storage resource state: %w", err)
		}
		if tag.RowsAffected == 0 {
			return ports.ErrNotFound
		}
		return nil
	})
}

const storageVolumeSelectSQL = `
	SELECT tenant_id::text, volume_id, name, size_gib, storage_class,
		COALESCE(zone, ''), COALESCE(volume_type, ''), COALESCE(iops, 0), COALESCE(encrypted, false),
		COALESCE(mount_instance_id, ''), COALESCE(mount_route, ''), COALESCE(mount_name, ''),
		COALESCE(os_init_status, ''), COALESCE(os_init_device, ''),
		COALESCE(from_snapshot_id, ''), COALESCE(from_snapshot_name, ''),
		state, COALESCE(reason, ''), created_at, updated_at,
		COALESCE(create_idempotency_key, ''), COALESCE(create_request_fingerprint, '')
	FROM storage_volumes
`

type storageScanner interface {
	Scan(dest ...any) error
}

func scanStorageVolume(row storageScanner, record *ports.StorageVolumeRecord) error {
	var state string
	err := row.Scan(
		&record.TenantID,
		&record.VolumeID,
		&record.Name,
		&record.SizeGiB,
		&record.StorageClass,
		&record.Zone,
		&record.VolumeType,
		&record.IOPS,
		&record.Encrypted,
		&record.MountInstanceID,
		&record.MountRoute,
		&record.MountName,
		&record.OSInitStatus,
		&record.OSInitDevice,
		&record.FromSnapshotID,
		&record.FromSnapshotName,
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
	record.State = ports.StorageResourceState(state)
	return nil
}

func scanStorageFilesystem(row storageScanner, record *ports.StorageFilesystemRecord) error {
	var state string
	err := row.Scan(
		&record.TenantID,
		&record.FilesystemID,
		&record.Name,
		&record.Protocol,
		&record.SizeGiB,
		&record.Endpoint,
		&record.Zone,
		&record.PerformanceMode,
		&record.MountCommand,
		&state,
		&record.Reason,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ports.ErrNotFound) || isNoRows(err) {
			return ports.ErrNotFound
		}
		return err
	}
	record.State = ports.StorageResourceState(state)
	return nil
}

func scanStorageObject(row storageScanner, record *ports.StorageObjectRecord) error {
	var state string
	err := row.Scan(
		&record.TenantID,
		&record.ObjectID,
		&record.Bucket,
		&record.Key,
		&record.SizeBytes,
		&record.ContentType,
		&state,
		&record.Reason,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ports.ErrNotFound) || isNoRows(err) {
			return ports.ErrNotFound
		}
		return err
	}
	record.State = ports.StorageResourceState(state)
	return nil
}

func loadVolumeChildren(ctx context.Context, tx ports.MetadataTx, record *ports.StorageVolumeRecord) error {
	row := tx.QueryRow(ctx, `
		SELECT enabled, COALESCE(retain_days, 0), COALESCE(schedule, '')
		FROM storage_volume_auto_snapshot_policies
		WHERE tenant_id = $1::uuid AND volume_id = $2 AND deleted_at IS NULL
	`, record.TenantID, record.VolumeID)
	var enabled bool
	var retainDays int
	var schedule string
	if err := row.Scan(&enabled, &retainDays, &schedule); err == nil {
		record.AutoSnapshot = ports.StorageVolumeAutoSnapshotPolicy{
			Enabled:    enabled,
			RetainDays: retainDays,
			Schedule:   schedule,
		}
	} else if !errors.Is(err, ports.ErrNotFound) && !isNoRows(err) {
		return err
	}

	rows, err := tx.Query(ctx, `
		SELECT action, COALESCE(target, ''), COALESCE(result, ''), occurred_at
		FROM storage_volume_mount_events
		WHERE tenant_id = $1::uuid AND volume_id = $2
		ORDER BY occurred_at ASC
	`, record.TenantID, record.VolumeID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var entry ports.StorageVolumeMountHistoryEntry
		if err := rows.Scan(&entry.Action, &entry.Target, &entry.Result, &entry.At); err != nil {
			return err
		}
		record.MountHistory = append(record.MountHistory, entry)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	countRow := tx.QueryRow(ctx, `
		SELECT COUNT(*)::int
		FROM storage_volume_snapshots
		WHERE tenant_id = $1::uuid AND volume_id = $2 AND deleted_at IS NULL
	`, record.TenantID, record.VolumeID)
	if err := countRow.Scan(&record.SnapshotsCount); err != nil && !errors.Is(err, ports.ErrNotFound) && !isNoRows(err) {
		return err
	}
	return nil
}

func upsertVolumeAutoSnapshotPolicy(ctx context.Context, tx ports.MetadataTx, record ports.StorageVolumeRecord) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO storage_volume_auto_snapshot_policies (
			tenant_id, volume_id, enabled, retain_days, schedule, updated_at, deleted_at
		) VALUES (
			$1::uuid, $2, $3, NULLIF($4, 0), NULLIF($5, ''), $6, NULL
		)
		ON CONFLICT (tenant_id, volume_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			retain_days = EXCLUDED.retain_days,
			schedule = EXCLUDED.schedule,
			updated_at = EXCLUDED.updated_at,
			deleted_at = NULL
	`, record.TenantID, record.VolumeID, record.AutoSnapshot.Enabled, record.AutoSnapshot.RetainDays, record.AutoSnapshot.Schedule, record.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("upsert volume auto snapshot policy: %w", err)
	}
	return nil
}

func replaceVolumeMountEvents(ctx context.Context, tx ports.MetadataTx, record ports.StorageVolumeRecord) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM storage_volume_mount_events
		WHERE tenant_id = $1::uuid AND volume_id = $2
	`, record.TenantID, record.VolumeID); err != nil {
		return fmt.Errorf("clear volume mount events: %w", err)
	}
	for i, entry := range record.MountHistory {
		occurredAt := entry.At
		if occurredAt.IsZero() {
			occurredAt = record.UpdatedAt
		}
		eventID := fmt.Sprintf("mhe_%s_%d", record.VolumeID, i+1)
		if _, err := tx.Exec(ctx, `
			INSERT INTO storage_volume_mount_events (
				tenant_id, event_id, volume_id, action, target, result, occurred_at
			) VALUES ($1::uuid, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7)
		`, record.TenantID, eventID, record.VolumeID, entry.Action, entry.Target, entry.Result, occurredAt.UTC()); err != nil {
			return fmt.Errorf("insert volume mount event: %w", err)
		}
	}
	return nil
}

func requireStorageRecord(tenantID string, resourceID string, name string, state ports.StorageResourceState) error {
	if strings.TrimSpace(tenantID) == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(resourceID) == "" {
		return fmt.Errorf("%w: resource id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", ports.ErrInvalid)
	}
	if state == "" {
		return fmt.Errorf("%w: state is required", ports.ErrInvalid)
	}
	return nil
}

func requireStorageStateUpdate(request ports.StorageResourceStateUpdateRequest) error {
	if strings.TrimSpace(request.TenantID) == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.ResourceKind) == "" {
		return fmt.Errorf("%w: resource kind is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.ResourceID) == "" {
		return fmt.Errorf("%w: resource id is required", ports.ErrInvalid)
	}
	if request.State == "" {
		return fmt.Errorf("%w: state is required", ports.ErrInvalid)
	}
	return nil
}

func storageResourceStateTable(resourceKind string) (string, string, error) {
	switch strings.TrimSpace(resourceKind) {
	case "volume":
		return "storage_volumes", "volume_id", nil
	case "filesystem":
		return "storage_filesystems", "filesystem_id", nil
	case "object":
		return "storage_objects", "object_id", nil
	default:
		return "", "", fmt.Errorf("%w: unsupported storage resource kind %q", ports.ErrUnsupported, resourceKind)
	}
}

func isNoRows(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no rows") || strings.Contains(msg, "not found")
}

var _ ports.StorageResourceStore = (*MetadataStorageStore)(nil)
