package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// fakeK8sClusterStore 在内存里模拟 k8s_clusters 表的语义：同租户唯一、幂等键可查询、
// 删除即释放租户槽位。用于验证 store 模式下集群记录不再随进程内存消失。
type fakeK8sClusterStore struct {
	mu          sync.Mutex
	records     map[string]ports.K8sClusterRecord
	createIdem  map[string]string
	upgradeIdem map[string]string
}

func newFakeK8sClusterStore() *fakeK8sClusterStore {
	return &fakeK8sClusterStore{
		records:     map[string]ports.K8sClusterRecord{},
		createIdem:  map[string]string{},
		upgradeIdem: map[string]string{},
	}
}

func k8sClusterIdemKey(tenantID string, idempotencyKey string) string {
	return tenantID + "\x00" + idempotencyKey
}

func (s *fakeK8sClusterStore) UpsertK8sCluster(_ context.Context, record ports.K8sClusterRecord, createIdempotencyKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 对应 idx_k8s_clusters_tenant_unique：同租户第二个集群直接冲突。
	for _, existing := range s.records {
		if existing.TenantID == record.TenantID && existing.ClusterID != record.ClusterID {
			return fmt.Errorf("%w: tenant already has a k8s cluster; only one vCluster per tenant is supported", ports.ErrConflict)
		}
	}
	s.records[record.ClusterID] = record
	if createIdempotencyKey != "" {
		s.createIdem[k8sClusterIdemKey(record.TenantID, createIdempotencyKey)] = record.ClusterID
	}
	return nil
}

func (s *fakeK8sClusterStore) GetK8sCluster(_ context.Context, req ports.K8sClusterGetRequest) (ports.K8sClusterRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[req.ClusterID]
	if !ok || rec.TenantID != req.TenantID {
		return ports.K8sClusterRecord{}, ports.ErrNotFound
	}
	return rec, nil
}

func (s *fakeK8sClusterStore) ListK8sClusters(_ context.Context, req ports.K8sClusterListRequest) ([]ports.K8sClusterRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ports.K8sClusterRecord{}
	for _, rec := range s.records {
		if rec.TenantID == req.TenantID {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ClusterID < out[j].ClusterID
	})
	return out, nil
}

func (s *fakeK8sClusterStore) DeleteK8sCluster(_ context.Context, req ports.K8sClusterGetRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[req.ClusterID]
	if !ok || rec.TenantID != req.TenantID {
		return ports.ErrNotFound
	}
	delete(s.records, req.ClusterID)
	for key, clusterID := range s.createIdem {
		if clusterID == req.ClusterID {
			delete(s.createIdem, key)
		}
	}
	for key, clusterID := range s.upgradeIdem {
		if clusterID == req.ClusterID {
			delete(s.upgradeIdem, key)
		}
	}
	return nil
}

func (s *fakeK8sClusterStore) FindK8sClusterByCreateIdempotencyKey(_ context.Context, tenantID string, idempotencyKey string) (ports.K8sClusterRecord, error) {
	return s.findByTenantIdempotencyKey(s.createIdem, tenantID, idempotencyKey)
}

func (s *fakeK8sClusterStore) FindK8sClusterByUpgradeIdempotencyKey(_ context.Context, tenantID string, idempotencyKey string) (ports.K8sClusterRecord, error) {
	return s.findByTenantIdempotencyKey(s.upgradeIdem, tenantID, idempotencyKey)
}

func (s *fakeK8sClusterStore) findByTenantIdempotencyKey(index map[string]string, tenantID string, idempotencyKey string) (ports.K8sClusterRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clusterID, ok := index[k8sClusterIdemKey(tenantID, idempotencyKey)]
	if !ok {
		return ports.K8sClusterRecord{}, ports.ErrNotFound
	}
	rec, ok := s.records[clusterID]
	if !ok {
		return ports.K8sClusterRecord{}, ports.ErrNotFound
	}
	return rec, nil
}

func (s *fakeK8sClusterStore) SetK8sClusterUpgradeIdempotency(_ context.Context, tenantID string, clusterID string, idempotencyKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[clusterID]; !ok {
		return ports.ErrNotFound
	}
	s.upgradeIdem[k8sClusterIdemKey(tenantID, idempotencyKey)] = clusterID
	return nil
}

var _ ports.K8sClusterStore = (*fakeK8sClusterStore)(nil)

// 本批次要治的病：集群记录只存进程内存，网关滚动重启后界面「失忆」，而底座 Helm
// release 仍在，用户既删不掉也建不了新的。这里用「新建 service 实例 + 复用同一个
// store」模拟重启，ListClusters 必须仍然看得到该集群。
func TestLocalK8sClusterServiceStoreModeSurvivesServiceRecreation(t *testing.T) {
	store := newFakeK8sClusterStore()
	provider := &fakeK8sClusterProviderApply{result: ports.K8sClusterProviderApplyResult{
		Applied:  true,
		Provider: "vcluster",
		Reason:   "vCluster Helm release applied",
	}}

	before := NewLocalK8sClusterService(WithK8sClusterStore(store), WithK8sClusterProviderApply(provider))
	created, err := before.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-persisted",
		Name:           "vc-persisted",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	// 新实例共享同一个 store，等价于网关换进程：内存 map 全新，记录仍在。
	after := NewLocalK8sClusterService(WithK8sClusterStore(store), WithK8sClusterProviderApply(provider))
	listed, err := after.ListClusters(context.Background(), ports.K8sClusterListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListClusters() after recreation error = %v", err)
	}
	if len(listed) != 1 || listed[0].ClusterID != created.ClusterID {
		t.Fatalf("listed clusters = %+v, want the persisted cluster %s", listed, created.ClusterID)
	}
	if listed[0].State != ports.K8sClusterStateRunning || !listed[0].RealProvider {
		t.Fatalf("listed cluster = %+v, want running real provider record", listed[0])
	}

	fetched, err := after.GetCluster(context.Background(), ports.K8sClusterGetRequest{TenantID: "tenant-a", ClusterID: created.ClusterID})
	if err != nil {
		t.Fatalf("GetCluster() after recreation error = %v", err)
	}
	if fetched.Name != "vc-persisted" || fetched.Provider != "vcluster" {
		t.Fatalf("fetched cluster = %+v, want persisted provider metadata", fetched)
	}
}

func TestLocalK8sClusterServiceStoreModeRejectsSecondCluster(t *testing.T) {
	store := newFakeK8sClusterStore()
	service := NewLocalK8sClusterService(WithK8sClusterStore(store))

	first, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-store-first",
		Name:           "vc-store-first",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	if _, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-store-second",
		Name:           "vc-store-second",
		Version:        "v1.30.0",
	}); !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("CreateCluster(second) error = %v, want ErrConflict", err)
	}

	replay, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-store-first",
		Name:           "vc-store-first",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster(replay) error = %v", err)
	}
	if replay.ClusterID != first.ClusterID {
		t.Fatalf("replay cluster = %+v, want %s", replay, first.ClusterID)
	}
}

func TestLocalK8sClusterServiceStoreModeReleasesSlotAfterDelete(t *testing.T) {
	store := newFakeK8sClusterStore()
	deleteProvider := &fakeK8sClusterProviderDelete{result: ports.K8sClusterProviderDeleteResult{
		Deleted:  true,
		Provider: "vcluster",
		Reason:   "vCluster Helm release uninstalled",
	}}
	service := NewLocalK8sClusterService(
		WithK8sClusterStore(store),
		WithK8sClusterProviderApply(&fakeK8sClusterProviderApply{result: ports.K8sClusterProviderApplyResult{
			Applied:  true,
			Provider: "vcluster",
		}}),
		WithK8sClusterProviderDelete(deleteProvider),
	)

	created, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-store-slot",
		Name:           "vc-store-slot",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	deleted, err := service.DeleteCluster(context.Background(), ports.K8sClusterGetRequest{TenantID: "tenant-a", ClusterID: created.ClusterID})
	if err != nil {
		t.Fatalf("DeleteCluster() error = %v", err)
	}
	if deleted.State != ports.K8sClusterStateDeleting {
		t.Fatalf("deleted cluster = %+v, want deleting state", deleted)
	}
	if _, err := service.GetCluster(context.Background(), ports.K8sClusterGetRequest{TenantID: "tenant-a", ClusterID: created.ClusterID}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("GetCluster() after delete error = %v, want ErrNotFound", err)
	}
	if _, ok := store.records[created.ClusterID]; ok {
		t.Fatalf("store still holds deleted cluster row")
	}

	next, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-store-after-delete",
		Name:           "vc-store-after-delete",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() after delete error = %v, want tenant slot released", err)
	}
	if next.ClusterID == created.ClusterID {
		t.Fatalf("cluster after delete = %+v, want new cluster id", next)
	}
}

func TestLocalK8sClusterServiceStoreModeRestoresRecordWhenProviderDeleteFails(t *testing.T) {
	store := newFakeK8sClusterStore()
	deleteProvider := &fakeK8sClusterProviderDelete{err: errors.New("helm uninstall failed")}
	service := NewLocalK8sClusterService(
		WithK8sClusterStore(store),
		WithK8sClusterProviderApply(&fakeK8sClusterProviderApply{result: ports.K8sClusterProviderApplyResult{
			Applied:  true,
			Provider: "vcluster",
		}}),
		WithK8sClusterProviderDelete(deleteProvider),
	)

	created, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-store-delete-fail",
		Name:           "vc-store-delete-fail",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	if _, err := service.DeleteCluster(context.Background(), ports.K8sClusterGetRequest{TenantID: "tenant-a", ClusterID: created.ClusterID}); err == nil {
		t.Fatalf("DeleteCluster() error = nil, want provider failure")
	}
	restored, err := service.GetCluster(context.Background(), ports.K8sClusterGetRequest{TenantID: "tenant-a", ClusterID: created.ClusterID})
	if err != nil {
		t.Fatalf("GetCluster() after failed delete error = %v", err)
	}
	if restored.State != ports.K8sClusterStateRunning {
		t.Fatalf("restored cluster = %+v, want running state", restored)
	}
}

func TestLocalK8sClusterServiceStoreModeDiscardsRecordWhenProviderApplyFails(t *testing.T) {
	store := newFakeK8sClusterStore()
	provider := &fakeK8sClusterProviderApply{err: errors.New("helm install failed")}
	service := NewLocalK8sClusterService(WithK8sClusterStore(store), WithK8sClusterProviderApply(provider))

	if _, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-store-apply-fail",
		Name:           "vc-store-apply-fail",
		Version:        "v1.30.0",
	}); err == nil || errors.Is(err, ports.ErrConflict) {
		t.Fatalf("CreateCluster() error = %v, want provider apply failure", err)
	}
	if len(store.records) != 0 {
		t.Fatalf("store records = %+v, want placeholder row removed after failed apply", store.records)
	}

	provider.err = nil
	provider.result = ports.K8sClusterProviderApplyResult{Applied: true, Provider: "vcluster", Reason: "applied"}
	retried, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-store-retry",
		Name:           "vc-store-retry",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() retry error = %v, want released tenant slot", err)
	}
	if retried.State != ports.K8sClusterStateRunning {
		t.Fatalf("retried cluster = %+v, want running state", retried)
	}
}

func TestLocalK8sClusterServiceStoreModeUpgradesClusterIdempotently(t *testing.T) {
	store := newFakeK8sClusterStore()
	service := NewLocalK8sClusterService(WithK8sClusterStore(store))

	created, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-store-upgrade",
		Name:           "vc-store-upgrade",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	upgraded, err := service.UpgradeCluster(context.Background(), ports.K8sClusterUpgradeRequest{
		TenantID:       "tenant-a",
		ClusterID:      created.ClusterID,
		IdempotencyKey: "upgrade-vc-store-1",
		Version:        "v1.31.0",
	})
	if err != nil {
		t.Fatalf("UpgradeCluster() error = %v", err)
	}
	if upgraded.Version != "v1.31.0" {
		t.Fatalf("upgraded cluster = %+v, want version v1.31.0", upgraded)
	}
	stored, err := store.GetK8sCluster(context.Background(), ports.K8sClusterGetRequest{TenantID: "tenant-a", ClusterID: created.ClusterID})
	if err != nil {
		t.Fatalf("store GetK8sCluster() error = %v", err)
	}
	if stored.Version != "v1.31.0" {
		t.Fatalf("stored version = %s, want v1.31.0", stored.Version)
	}

	replay, err := service.UpgradeCluster(context.Background(), ports.K8sClusterUpgradeRequest{
		TenantID:       "tenant-a",
		ClusterID:      created.ClusterID,
		IdempotencyKey: "upgrade-vc-store-1",
		Version:        "v1.31.0",
	})
	if err != nil {
		t.Fatalf("UpgradeCluster(replay) error = %v", err)
	}
	if replay.Version != "v1.31.0" {
		t.Fatalf("replay cluster = %+v, want stored v1.31.0", replay)
	}
}
