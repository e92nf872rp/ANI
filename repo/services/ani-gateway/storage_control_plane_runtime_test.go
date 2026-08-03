package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type stubControlPlaneMetadataStore struct {
	mu           sync.Mutex
	missingTable string
	pingErr      error
	volumes      map[string]ports.StorageVolumeRecord
	buckets      map[string]ports.StorageBucketRecord
	vectors      map[string]ports.VectorStoreRecord
}

func newStubControlPlaneMetadataStore() *stubControlPlaneMetadataStore {
	return &stubControlPlaneMetadataStore{
		volumes: map[string]ports.StorageVolumeRecord{},
		buckets: map[string]ports.StorageBucketRecord{},
		vectors: map[string]ports.VectorStoreRecord{},
	}
}

func (s *stubControlPlaneMetadataStore) Ping(context.Context) error {
	return s.pingErr
}

func (s *stubControlPlaneMetadataStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, &stubControlPlaneMetadataTx{store: s})
}

func (s *stubControlPlaneMetadataStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, &stubControlPlaneMetadataTx{store: s})
}

type stubControlPlaneMetadataTx struct {
	store *stubControlPlaneMetadataStore
}

func (t *stubControlPlaneMetadataTx) Exec(_ context.Context, sql string, args ...any) (ports.CommandTag, error) {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	lowered := strings.ToLower(sql)
	switch {
	case strings.Contains(lowered, "insert into storage_volumes"):
		record := ports.StorageVolumeRecord{
			TenantID:     args[0].(string),
			VolumeID:     args[1].(string),
			Name:         args[2].(string),
			SizeGiB:      args[3].(int64),
			StorageClass: args[4].(string),
			State:        ports.StorageResourceState(args[5].(string)),
		}
		if len(args) > 19 {
			if key, ok := args[19].(string); ok {
				record.CreateIdempotencyKey = key
			}
		}
		t.store.volumes[record.TenantID+"/"+record.VolumeID] = record
		if record.CreateIdempotencyKey != "" {
			t.store.volumes[record.TenantID+"/idem:"+record.CreateIdempotencyKey] = record
		}
	case strings.Contains(lowered, "insert into storage_buckets"):
		record := ports.StorageBucketRecord{
			TenantID:   args[0].(string),
			BucketID:   args[1].(string),
			Name:       args[2].(string),
			AccessMode: args[5].(string),
			State:      ports.StorageResourceState(args[13].(string)),
		}
		if len(args) > 16 {
			if key, ok := args[16].(string); ok {
				record.CreateIdempotencyKey = key
			}
		}
		t.store.buckets[record.TenantID+"/"+record.BucketID] = record
		if record.CreateIdempotencyKey != "" {
			t.store.buckets[record.TenantID+"/idem:"+record.CreateIdempotencyKey] = record
		}
	case strings.Contains(lowered, "insert into vector_stores"):
		record := ports.VectorStoreRecord{
			TenantID:  args[0].(string),
			StoreID:   args[1].(string),
			Name:      args[2].(string),
			Dimension: args[3].(int),
			Metric:    args[4].(string),
			State:     ports.VectorStoreState(args[9].(string)),
		}
		if len(args) > 12 {
			if key, ok := args[12].(string); ok {
				record.CreateIdempotencyKey = key
			}
		}
		t.store.vectors[record.TenantID+"/"+record.StoreID] = record
		if record.CreateIdempotencyKey != "" {
			t.store.vectors[record.TenantID+"/idem:"+record.CreateIdempotencyKey] = record
		}
	}
	return ports.CommandTag{RowsAffected: 1}, nil
}

func (t *stubControlPlaneMetadataTx) Query(context.Context, string, ...any) (ports.Rows, error) {
	return stubControlPlaneRows{}, nil
}

func (t *stubControlPlaneMetadataTx) QueryRow(_ context.Context, sql string, args ...any) ports.Row {
	lowered := strings.ToLower(sql)
	if strings.Contains(lowered, "to_regclass") {
		table := ""
		if len(args) > 0 {
			table, _ = args[0].(string)
		}
		name := strings.TrimPrefix(table, "public.")
		if t.store.missingTable != "" && name == t.store.missingTable {
			return stubControlPlaneRow{values: []any{(*string)(nil)}}
		}
		return stubControlPlaneRow{values: []any{&name}}
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	tenantID, _ := args[0].(string)
	switch {
	case strings.Contains(lowered, "from storage_volumes") && strings.Contains(lowered, "create_idempotency_key ="):
		key, _ := args[1].(string)
		record, ok := t.store.volumes[tenantID+"/idem:"+key]
		if !ok {
			return stubControlPlaneRow{err: ports.ErrNotFound}
		}
		return stubControlPlaneRow{values: storageVolumeScanValues(record)}
	case strings.Contains(lowered, "from storage_volumes"):
		volumeID, _ := args[1].(string)
		record, ok := t.store.volumes[tenantID+"/"+volumeID]
		if !ok {
			return stubControlPlaneRow{err: ports.ErrNotFound}
		}
		return stubControlPlaneRow{values: storageVolumeScanValues(record)}
	case strings.Contains(lowered, "from storage_buckets") && strings.Contains(lowered, "create_idempotency_key ="):
		key, _ := args[1].(string)
		record, ok := t.store.buckets[tenantID+"/idem:"+key]
		if !ok {
			return stubControlPlaneRow{err: ports.ErrNotFound}
		}
		return stubControlPlaneRow{values: storageBucketScanValues(record)}
	case strings.Contains(lowered, "from storage_buckets"):
		bucketID, _ := args[1].(string)
		record, ok := t.store.buckets[tenantID+"/"+bucketID]
		if !ok {
			return stubControlPlaneRow{err: ports.ErrNotFound}
		}
		return stubControlPlaneRow{values: storageBucketScanValues(record)}
	case strings.Contains(lowered, "from vector_stores") && strings.Contains(lowered, "create_idempotency_key ="):
		key, _ := args[1].(string)
		record, ok := t.store.vectors[tenantID+"/idem:"+key]
		if !ok {
			return stubControlPlaneRow{err: ports.ErrNotFound}
		}
		return stubControlPlaneRow{values: vectorStoreScanValues(record)}
	case strings.Contains(lowered, "from vector_stores"):
		storeID, _ := args[1].(string)
		record, ok := t.store.vectors[tenantID+"/"+storeID]
		if !ok {
			return stubControlPlaneRow{err: ports.ErrNotFound}
		}
		return stubControlPlaneRow{values: vectorStoreScanValues(record)}
	case strings.Contains(lowered, "storage_volume_auto_snapshot_policies"),
		strings.Contains(lowered, "vector_store_knowledge_base_links"),
		strings.Contains(lowered, "count(*)"):
		return stubControlPlaneRow{err: ports.ErrNotFound}
	default:
		return stubControlPlaneRow{err: ports.ErrNotFound}
	}
}

func storageVolumeScanValues(record ports.StorageVolumeRecord) []any {
	now := time.Unix(100, 0).UTC()
	return []any{
		record.TenantID, record.VolumeID, record.Name, record.SizeGiB, record.StorageClass,
		"", "", 0, false, "", "", "", "", "", "", "",
		string(record.State), "", now, now, record.CreateIdempotencyKey, "",
	}
}

func storageBucketScanValues(record ports.StorageBucketRecord) []any {
	now := time.Unix(100, 0).UTC()
	return []any{
		record.TenantID, record.BucketID, record.Name, "", "",
		record.AccessMode, "private", "私有", "standard", "disabled",
		int64(0), int64(0), "", string(record.State), "", now, now,
		record.CreateIdempotencyKey, "",
	}
}

func vectorStoreScanValues(record ports.VectorStoreRecord) []any {
	now := time.Unix(100, 0).UTC()
	return []any{
		record.TenantID, record.StoreID, record.Name, record.Dimension, record.Metric, "",
		int64(0), "ready", (*time.Time)(nil), string(record.State), "", now, now,
		record.CreateIdempotencyKey, "",
	}
}

type stubControlPlaneRow struct {
	values []any
	err    error
}

func (r stubControlPlaneRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, target := range dest {
		switch ptr := target.(type) {
		case *string:
			*ptr = r.values[i].(string)
		case *bool:
			*ptr = r.values[i].(bool)
		case *int:
			*ptr = r.values[i].(int)
		case *int64:
			*ptr = r.values[i].(int64)
		case *time.Time:
			*ptr = r.values[i].(time.Time)
		case **time.Time:
			switch value := r.values[i].(type) {
			case nil:
				*ptr = nil
			case *time.Time:
				*ptr = value
			case time.Time:
				copied := value
				*ptr = &copied
			default:
				return ports.ErrUnsupported
			}
		case **string:
			if r.values[i] == nil {
				*ptr = nil
			} else {
				switch value := r.values[i].(type) {
				case *string:
					*ptr = value
				case string:
					copied := value
					*ptr = &copied
				}
			}
		default:
			return ports.ErrUnsupported
		}
	}
	return nil
}

type stubControlPlaneRows struct{}

func (stubControlPlaneRows) Next() bool        { return false }
func (stubControlPlaneRows) Scan(...any) error { return nil }
func (stubControlPlaneRows) Err() error        { return nil }
func (stubControlPlaneRows) Close()            {}

func TestValidateStorageControlPlaneSchemaRejectsMissingTable(t *testing.T) {
	store := newStubControlPlaneMetadataStore()
	store.missingTable = "vector_stores"
	err := validateStorageControlPlaneSchema(context.Background(), store)
	if err == nil || !strings.Contains(err.Error(), "vector_stores") {
		t.Fatalf("validateStorageControlPlaneSchema() error = %v, want missing vector_stores", err)
	}
}

func TestConnectStorageControlPlaneStoreRequiresDatabaseURL(t *testing.T) {
	_, _, err := connectStorageControlPlaneStore(context.Background(), "", nil)
	if err == nil || !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("connectStorageControlPlaneStore() error = %v, want ErrNotConfigured", err)
	}
}

func TestConnectStorageControlPlaneStoreAcceptsInjectedStore(t *testing.T) {
	store, closeStore, err := connectStorageControlPlaneStore(context.Background(), "", newStubControlPlaneMetadataStore())
	if err != nil {
		t.Fatalf("connectStorageControlPlaneStore() error = %v", err)
	}
	defer closeStore()
	if store == nil {
		t.Fatal("store = nil")
	}
}
