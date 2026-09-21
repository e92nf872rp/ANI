package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// recordingMetadataStore 复用 fakeMetadataTx 的参数捕获能力，同时记录 store 收到的租户上下文。
type recordingMetadataStore struct {
	tx   ports.MetadataTx
	seen *types.TenantContext
}

func (s *recordingMetadataStore) Ping(context.Context) error { return nil }

func (s *recordingMetadataStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	if tenant, ok := types.TryFromContext(ctx); ok {
		s.seen = tenant
	}
	return fn(ctx, s.tx)
}

func (s *recordingMetadataStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, s.tx)
}

// k8sClusterErrorTx 用于注入 pgx/pgconn 错误，验证 23505 → ErrConflict 的映射。
type k8sClusterErrorTx struct {
	err error
}

func (tx k8sClusterErrorTx) Exec(context.Context, string, ...any) (ports.CommandTag, error) {
	return ports.CommandTag{}, tx.err
}

func (tx k8sClusterErrorTx) Query(context.Context, string, ...any) (ports.Rows, error) {
	return nil, tx.err
}

func (tx k8sClusterErrorTx) QueryRow(context.Context, string, ...any) ports.Row {
	return fakeMetadataRow{err: tx.err}
}

func TestMetadataK8sClusterStoreUpsertsRecord(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataK8sClusterStore(fakeMetadataStore{tx: tx})

	err := store.UpsertK8sCluster(context.Background(), ports.K8sClusterRecord{
		ClusterID:    "k8sclu-a",
		TenantID:     networkStoreTenantID,
		Name:         "cluster-a",
		Version:      "v1.30.0",
		State:        ports.K8sClusterStateRunning,
		Reason:       "vCluster provider applied",
		Provider:     "vcluster",
		RealProvider: true,
		ProviderRefs: []string{"vcluster/HelmRelease/k8sclu-a"},
		CreatedAt:    100,
		UpdatedAt:    200,
	}, "idem-create-1")
	if err != nil {
		t.Fatalf("UpsertK8sCluster() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO k8s_clusters") {
		t.Fatalf("sql = %q, want k8s_clusters insert", tx.sql)
	}
	if !strings.Contains(tx.sql, "ON CONFLICT (tenant_id, cluster_id) DO UPDATE") {
		t.Fatalf("sql = %q, want upsert conflict target", tx.sql)
	}
	if got, want := tx.args[0], networkStoreTenantID; got != want {
		t.Fatalf("tenant_id arg = %v, want %s", got, want)
	}
	if got, want := tx.args[1], "k8sclu-a"; got != want {
		t.Fatalf("cluster_id arg = %v, want %s", got, want)
	}
	if got, want := tx.args[4], "running"; got != want {
		t.Fatalf("state arg = %v, want %s", got, want)
	}
	if got, want := tx.args[8], `["vcluster/HelmRelease/k8sclu-a"]`; got != want {
		t.Fatalf("provider_refs arg = %v, want %s", got, want)
	}
	if got, want := tx.args[9], "idem-create-1"; got != want {
		t.Fatalf("create_idempotency_key arg = %v, want %s", got, want)
	}
	// created_at/updated_at 由 Unix 秒转成 timestamptz，必须带时区且可往返。
	if got, want := tx.args[10], time.Unix(100, 0).UTC(); got != want {
		t.Fatalf("created_at arg = %v, want %v", got, want)
	}
	if got, want := tx.args[11], time.Unix(200, 0).UTC(); got != want {
		t.Fatalf("updated_at arg = %v, want %v", got, want)
	}
}

func TestMetadataK8sClusterStoreUpsertOmitsBlankIdempotencyKey(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataK8sClusterStore(fakeMetadataStore{tx: tx})

	err := store.UpsertK8sCluster(context.Background(), ports.K8sClusterRecord{
		ClusterID: "k8sclu-a",
		TenantID:  networkStoreTenantID,
		Name:      "cluster-a",
		State:     ports.K8sClusterStateRunning,
	}, "")
	if err != nil {
		t.Fatalf("UpsertK8sCluster() error = %v", err)
	}
	if !strings.Contains(tx.sql, "COALESCE(EXCLUDED.create_idempotency_key") {
		t.Fatalf("sql = %q, want create idempotency key preserved on conflict", tx.sql)
	}
	if got := tx.args[9]; got != "" {
		t.Fatalf("create_idempotency_key arg = %v, want empty", got)
	}
}

func TestMetadataK8sClusterStoreInjectsTenantContext(t *testing.T) {
	recorder := &recordingMetadataStore{tx: &fakeMetadataTx{row: fakeMetadataRow{err: pgx.ErrNoRows}}}
	store := NewMetadataK8sClusterStore(recorder)

	_, err := store.GetK8sCluster(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  networkStoreTenantID,
		ClusterID: "k8sclu-a",
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("GetK8sCluster() error = %v, want ErrNotFound", err)
	}
	if recorder.seen == nil {
		t.Fatalf("WithTenantTx() received no tenant context")
	}
	if got, want := recorder.seen.TenantID, uuid.MustParse(networkStoreTenantID); got != want {
		t.Fatalf("tenant context id = %s, want %s", got, want)
	}
}

func TestMetadataK8sClusterStoreRejectsNonUUIDTenant(t *testing.T) {
	store := NewMetadataK8sClusterStore(fakeMetadataStore{tx: &fakeMetadataTx{}})

	err := store.UpsertK8sCluster(context.Background(), ports.K8sClusterRecord{
		ClusterID: "k8sclu-a",
		TenantID:  "tenant-a",
		Name:      "cluster-a",
		State:     ports.K8sClusterStateRunning,
	}, "")
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("UpsertK8sCluster() error = %v, want ErrInvalid", err)
	}
}

func TestMetadataK8sClusterStoreMapsUniqueViolationToConflict(t *testing.T) {
	store := NewMetadataK8sClusterStore(&recordingMetadataStore{tx: k8sClusterErrorTx{err: &pgconn.PgError{
		Code:    "23505",
		Message: "duplicate key value violates unique constraint \"idx_k8s_clusters_tenant_unique\"",
	}}})

	err := store.UpsertK8sCluster(context.Background(), ports.K8sClusterRecord{
		ClusterID: "k8sclu-b",
		TenantID:  networkStoreTenantID,
		Name:      "cluster-b",
		State:     ports.K8sClusterStateProvisioning,
	}, "")
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("UpsertK8sCluster() error = %v, want ErrConflict", err)
	}
}

func TestMetadataK8sClusterStoreReadsRecord(t *testing.T) {
	createdAt := time.Unix(300, 0).UTC()
	updatedAt := time.Unix(400, 0).UTC()
	tx := &fakeMetadataTx{
		row: fakeMetadataRow{values: []any{
			networkStoreTenantID,
			"k8sclu-a",
			"cluster-a",
			"v1.30.0",
			"running",
			"vCluster provider applied",
			"vcluster",
			true,
			[]byte(`["vcluster/HelmRelease/k8sclu-a"]`),
			createdAt,
			updatedAt,
		}},
	}
	store := NewMetadataK8sClusterStore(fakeMetadataStore{tx: tx})

	rec, err := store.GetK8sCluster(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  networkStoreTenantID,
		ClusterID: "k8sclu-a",
	})
	if err != nil {
		t.Fatalf("GetK8sCluster() error = %v", err)
	}
	if !strings.Contains(tx.queryRowSQL, "FROM k8s_clusters") {
		t.Fatalf("query sql = %q, want k8s_clusters lookup", tx.queryRowSQL)
	}
	if rec.State != ports.K8sClusterStateRunning || !rec.RealProvider || rec.Name != "cluster-a" || rec.Version != "v1.30.0" {
		t.Fatalf("record = %+v", rec)
	}
	if len(rec.ProviderRefs) != 1 || rec.ProviderRefs[0] != "vcluster/HelmRelease/k8sclu-a" {
		t.Fatalf("provider_refs = %v", rec.ProviderRefs)
	}
	if rec.CreatedAt != 300 || rec.UpdatedAt != 400 {
		t.Fatalf("timestamps = %d/%d, want 300/400", rec.CreatedAt, rec.UpdatedAt)
	}
}

func TestMetadataK8sClusterStoreMapsMissingRecord(t *testing.T) {
	store := NewMetadataK8sClusterStore(fakeMetadataStore{tx: &fakeMetadataTx{
		row: fakeMetadataRow{err: pgx.ErrNoRows},
	}})

	_, err := store.GetK8sCluster(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  networkStoreTenantID,
		ClusterID: "missing",
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("GetK8sCluster() error = %v, want ErrNotFound", err)
	}
}

func TestMetadataK8sClusterStoreListsRecords(t *testing.T) {
	tx := &fakeMetadataTx{rows: &fakeRows{values: [][]any{
		{
			networkStoreTenantID, "k8sclu-a", "cluster-a", "", "running", "", "vcluster", true,
			[]byte(`[]`), time.Unix(100, 0).UTC(), time.Unix(100, 0).UTC(),
		},
		{
			networkStoreTenantID, "k8sclu-b", "cluster-b", "", "deleting", "", "local", false,
			[]byte(nil), time.Unix(150, 0).UTC(), time.Unix(160, 0).UTC(),
		},
	}}}
	store := NewMetadataK8sClusterStore(fakeMetadataStore{tx: tx})

	records, err := store.ListK8sClusters(context.Background(), ports.K8sClusterListRequest{TenantID: networkStoreTenantID})
	if err != nil {
		t.Fatalf("ListK8sClusters() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %+v, want 2 rows", records)
	}
	if records[0].ClusterID != "k8sclu-a" || records[1].State != ports.K8sClusterStateDeleting {
		t.Fatalf("records = %+v", records)
	}
	// provider_refs 为 NULL 时必须退化成空切片，避免下游 nil 解引用。
	if records[1].ProviderRefs == nil || len(records[1].ProviderRefs) != 0 {
		t.Fatalf("provider_refs = %v, want empty slice", records[1].ProviderRefs)
	}
}

func TestMetadataK8sClusterStoreDeletesRecord(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataK8sClusterStore(fakeMetadataStore{tx: tx})

	err := store.DeleteK8sCluster(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  networkStoreTenantID,
		ClusterID: "k8sclu-a",
	})
	if err != nil {
		t.Fatalf("DeleteK8sCluster() error = %v", err)
	}
	if !strings.Contains(tx.sql, "DELETE FROM k8s_clusters") {
		t.Fatalf("sql = %q, want k8s_clusters delete", tx.sql)
	}
}

func TestMetadataK8sClusterStoreSetsUpgradeIdempotency(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataK8sClusterStore(fakeMetadataStore{tx: tx})

	err := store.SetK8sClusterUpgradeIdempotency(context.Background(), networkStoreTenantID, "k8sclu-a", "idem-upgrade-1")
	if err != nil {
		t.Fatalf("SetK8sClusterUpgradeIdempotency() error = %v", err)
	}
	if !strings.Contains(tx.sql, "UPDATE k8s_clusters SET upgrade_idempotency_key") {
		t.Fatalf("sql = %q, want upgrade idempotency update", tx.sql)
	}
	if got, want := tx.args[2], "idem-upgrade-1"; got != want {
		t.Fatalf("idempotency arg = %v, want %s", got, want)
	}
}

func TestMetadataK8sClusterStoreFindsByCreateIdempotencyKey(t *testing.T) {
	tx := &fakeMetadataTx{
		row: fakeMetadataRow{values: []any{
			networkStoreTenantID, "k8sclu-a", "cluster-a", "", "running", "", "vcluster", true,
			[]byte(`[]`), time.Unix(100, 0).UTC(), time.Unix(100, 0).UTC(),
		}},
	}
	store := NewMetadataK8sClusterStore(fakeMetadataStore{tx: tx})

	rec, err := store.FindK8sClusterByCreateIdempotencyKey(context.Background(), networkStoreTenantID, "idem-create-1")
	if err != nil {
		t.Fatalf("FindK8sClusterByCreateIdempotencyKey() error = %v", err)
	}
	if rec.ClusterID != "k8sclu-a" {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(tx.queryRowSQL, "create_idempotency_key = $2") {
		t.Fatalf("query sql = %q, want create idempotency key lookup", tx.queryRowSQL)
	}
}
