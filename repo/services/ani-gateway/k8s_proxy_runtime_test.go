package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestGatewayK8sClusterServiceFromConfigDefaultsToRouterLocalService(t *testing.T) {
	service, err := newGatewayK8sClusterService(gatewayK8sClusterRuntimeConfig{})
	if err != nil {
		t.Fatalf("newGatewayK8sClusterService() error = %v", err)
	}
	if service != nil {
		t.Fatalf("service = %T, want nil so router keeps local default", service)
	}
}

func TestGatewayK8sClusterRuntimeConfigFromEnvIncludesInClusterKubernetesService(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/token")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")

	cfg := gatewayK8sClusterRuntimeConfigFromEnv()

	if cfg.KubernetesServiceHost != "10.96.0.1" || cfg.KubernetesServicePort != "443" {
		t.Fatalf("service host/port = %q/%q, want in-cluster Kubernetes service", cfg.KubernetesServiceHost, cfg.KubernetesServicePort)
	}
	if cfg.KubernetesServiceAccountTokenFile == "" || cfg.KubernetesServiceAccountCAFile == "" {
		t.Fatalf("service account token/CA files = %q/%q, want configured files", cfg.KubernetesServiceAccountTokenFile, cfg.KubernetesServiceAccountCAFile)
	}
}

func TestGatewayK8sClusterServiceFromConfigUsesStaticForwardingTarget(t *testing.T) {
	transport := &gatewayK8sProxyRoundTripper{
		statusCode: http.StatusOK,
		headers:    http.Header{"X-Upstream": []string{"vcluster-a"}},
		body:       `{"kind":"PodList"}`,
	}
	service, err := newGatewayK8sClusterService(gatewayK8sClusterRuntimeConfig{
		ProxyMode:         "forwarding_static",
		TargetServer:      "https://tenant-a-vcluster.example",
		TargetBearerToken: "target-token",
		HTTPClient:        &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("newGatewayK8sClusterService() error = %v", err)
	}
	cluster, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-a",
		Name:           "vc-a",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster error = %v", err)
	}

	got, err := service.Proxy(context.Background(), ports.K8sClusterProxyRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "proxy-vc-a",
		Method:         "GET",
		Path:           "/api/v1/namespaces/default/pods",
		Query:          map[string]string{"limit": "20"},
	})
	if err != nil {
		t.Fatalf("Proxy error = %v", err)
	}

	if transport.path != "/api/v1/namespaces/default/pods" {
		t.Fatalf("upstream path = %s", transport.path)
	}
	if transport.query != "limit=20" {
		t.Fatalf("upstream query = %s", transport.query)
	}
	if transport.authorization != "Bearer target-token" {
		t.Fatalf("authorization = %q", transport.authorization)
	}
	if got.StatusCode != http.StatusOK || got.Headers["x-upstream"] != "vcluster-a" || got.Body["kind"] != "PodList" {
		t.Fatalf("proxy result = %+v", got)
	}
}

func TestGatewayK8sClusterServiceFromConfigUsesMetadataForwardingTarget(t *testing.T) {
	tenantID := "11111111-1111-4111-8111-111111111111"
	transport := &gatewayK8sProxyRoundTripper{
		statusCode: http.StatusCreated,
		headers:    http.Header{"X-Upstream": []string{"metadata-vcluster"}},
		body:       `{"kind":"Namespace"}`,
	}
	store := &gatewayK8sProxyMetadataStore{
		target: ports.K8sClusterProxyTarget{
			TenantID:    tenantID,
			Server:      "https://metadata-vcluster.example",
			BearerToken: "metadata-token",
		},
	}
	service, err := newGatewayK8sClusterService(gatewayK8sClusterRuntimeConfig{
		ProxyMode:     "forwarding_metadata",
		MetadataStore: store,
		HTTPClient:    &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("newGatewayK8sClusterService() error = %v", err)
	}
	cluster, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       tenantID,
		IdempotencyKey: "create-vc-a",
		Name:           "vc-a",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster error = %v", err)
	}
	store.target.ClusterID = cluster.ClusterID

	got, err := service.Proxy(context.Background(), ports.K8sClusterProxyRequest{
		TenantID:       tenantID,
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "proxy-vc-a",
		Method:         "POST",
		Path:           "/api/v1/namespaces",
		Body:           map[string]any{"kind": "Namespace"},
	})
	if err != nil {
		t.Fatalf("Proxy error = %v", err)
	}

	if !store.usedTenantTx {
		t.Fatalf("metadata store was not used for proxy target lookup")
	}
	if transport.path != "/api/v1/namespaces" {
		t.Fatalf("upstream path = %s", transport.path)
	}
	if transport.authorization != "Bearer metadata-token" {
		t.Fatalf("authorization = %q", transport.authorization)
	}
	if got.StatusCode != http.StatusCreated || got.Headers["x-upstream"] != "metadata-vcluster" || got.Body["kind"] != "Namespace" {
		t.Fatalf("proxy result = %+v", got)
	}
}

// 网关滚动重启后内存 map 全新，但集群记录必须仍然可见：此前记录只存进程内存，导致
// 界面看不到集群而底座 release 仍在，用户既删不掉也建不了新的。
func TestGatewayK8sClusterServiceFromConfigPersistsClusterRecords(t *testing.T) {
	tenantID := "11111111-1111-4111-8111-111111111111"
	store := &gatewayK8sProxyMetadataStore{}
	cfg := gatewayK8sClusterRuntimeConfig{
		ProxyMode:                        "forwarding_metadata",
		ProviderMode:                     "vcluster_helm",
		MetadataStore:                    store,
		VClusterHelmRunner:               &gatewayVClusterHelmRunner{},
		VClusterProxyBearerToken:         "tenant-token",
		VClusterKubeconfigServerTemplate: "https://{cluster_id}.{namespace}:443",
	}

	restarted := func() ports.K8sClusterService {
		service, err := newGatewayK8sClusterService(cfg)
		if err != nil {
			t.Fatalf("newGatewayK8sClusterService() error = %v", err)
		}
		return service
	}
	created, err := restarted().CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       tenantID,
		IdempotencyKey: "create-vc-persisted",
		Name:           "vc-persisted",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster error = %v", err)
	}
	if !created.RealProvider || created.Provider != "vcluster" {
		t.Fatalf("created cluster = %+v, want vcluster real provider record", created)
	}

	listed, err := restarted().ListClusters(context.Background(), ports.K8sClusterListRequest{TenantID: tenantID})
	if err != nil {
		t.Fatalf("ListClusters() after simulated restart error = %v", err)
	}
	if len(listed) != 1 || listed[0].ClusterID != created.ClusterID {
		t.Fatalf("listed clusters = %+v, want persisted cluster %s", listed, created.ClusterID)
	}
	if listed[0].Provider != "vcluster" || !listed[0].RealProvider {
		t.Fatalf("listed cluster = %+v, want persisted provider evidence", listed[0])
	}
}

func TestGatewayK8sClusterRuntimeFromConfigConnectsMetadataStore(t *testing.T) {
	tenantID := "11111111-1111-4111-8111-111111111111"
	transport := &gatewayK8sProxyRoundTripper{
		statusCode: http.StatusOK,
		body:       `{"kind":"PodList"}`,
	}
	store := &gatewayK8sProxyMetadataStore{
		target: ports.K8sClusterProxyTarget{
			TenantID:    tenantID,
			Server:      "https://metadata-vcluster.example",
			BearerToken: "metadata-token",
		},
	}
	closed := false
	service, closeRuntime, err := newGatewayK8sClusterRuntime(context.Background(), gatewayK8sClusterRuntimeConfig{
		ProxyMode:   "forwarding_metadata",
		DatabaseURL: "postgres://metadata.example/ani",
		HTTPClient:  &http.Client{Transport: transport},
		MetadataConnector: func(_ context.Context, databaseURL string) (ports.MetadataStore, func(), error) {
			if databaseURL != "postgres://metadata.example/ani" {
				t.Fatalf("databaseURL = %q", databaseURL)
			}
			return store, func() { closed = true }, nil
		},
	})
	if err != nil {
		t.Fatalf("newGatewayK8sClusterRuntime() error = %v", err)
	}
	defer closeRuntime()

	cluster, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       tenantID,
		IdempotencyKey: "create-vc-a",
		Name:           "vc-a",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster error = %v", err)
	}
	store.target.ClusterID = cluster.ClusterID

	if _, err := service.Proxy(context.Background(), ports.K8sClusterProxyRequest{
		TenantID:       tenantID,
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "proxy-vc-a",
		Method:         "GET",
		Path:           "/api/v1/pods",
	}); err != nil {
		t.Fatalf("Proxy error = %v", err)
	}
	closeRuntime()
	if !closed {
		t.Fatalf("metadata connector close function was not called")
	}
}

func TestGatewayK8sClusterServiceFromConfigUsesVClusterHelmProvider(t *testing.T) {
	runner := &gatewayVClusterHelmRunner{}
	service, err := newGatewayK8sClusterService(gatewayK8sClusterRuntimeConfig{
		ProviderMode:                     "vcluster_helm",
		VClusterHelmRunner:               runner,
		VClusterProxyServerTemplate:      "https://{cluster_id}.{namespace}:443",
		VClusterProxyBearerToken:         "tenant-token",
		VClusterKubeconfigServerTemplate: "https://{cluster_id}.{namespace}:443",
	})
	if err != nil {
		t.Fatalf("newGatewayK8sClusterService() error = %v", err)
	}
	if service == nil {
		t.Fatalf("service = nil, want vCluster provider-backed service")
	}
	record, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-a",
		Name:           "vc-a",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}
	if runner.binary != "helm" || len(runner.args) == 0 {
		t.Fatalf("helm runner was not called: %s %#v", runner.binary, runner.args)
	}
	if !record.RealProvider || record.Provider != "vcluster" {
		t.Fatalf("record provider evidence = %+v, want vcluster real provider", record)
	}
	kubeconfig, err := service.GetKubeconfig(context.Background(), ports.K8sClusterKubeconfigRequest{
		TenantID:  "tenant-a",
		ClusterID: record.ClusterID,
	})
	if err != nil {
		t.Fatalf("GetKubeconfig() error = %v", err)
	}
	if runner.binary != "vcluster" || len(runner.args) == 0 || runner.args[0] != "connect" {
		t.Fatalf("vcluster runner was not called for kubeconfig: %s %#v", runner.binary, runner.args)
	}
	if kubeconfig.Server != "https://"+record.ClusterID+".ani-tenant-tenant-a:443" || kubeconfig.Token != "tenant-token" {
		t.Fatalf("kubeconfig = %+v, want provider-backed vCluster kubeconfig", kubeconfig)
	}
}

func TestGatewayK8sClusterServiceFromConfigUsesClusterAPINodePoolProvider(t *testing.T) {
	runner := &gatewayVClusterHelmRunner{}
	var nodePoolPath string
	var nodePoolBody map[string]any
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		nodePoolPath = r.URL.String()
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&nodePoolBody); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		return jsonResponse(http.StatusOK, `{"kind":"MachineDeployment"}`), nil
	})
	service, err := newGatewayK8sClusterService(gatewayK8sClusterRuntimeConfig{
		ProviderMode:                          "vcluster_helm",
		NodePoolProviderMode:                  "clusterapi_kubernetes_rest",
		KubernetesAPIHost:                     "https://kubernetes.example.test",
		KubernetesProviderManager:             "ani-test",
		NodePoolMachineVersion:                "v1.36.1",
		NodePoolBootstrapRefAPIVersion:        "bootstrap.cluster.x-k8s.io/v1beta1",
		NodePoolBootstrapRefKind:              "KubeadmConfigTemplate",
		NodePoolBootstrapRefNameTemplate:      "{cluster_name}-{node_pool_name}",
		NodePoolBootstrapRefNamespace:         "{namespace}",
		NodePoolInfrastructureRefAPIVersion:   "infrastructure.cluster.x-k8s.io/v1alpha1",
		NodePoolInfrastructureRefKind:         "KubevirtMachineTemplate",
		NodePoolInfrastructureRefNameTemplate: "{cluster_name}-{node_pool_name}",
		NodePoolInfrastructureRefNamespace:    "{namespace}",
		VClusterHelmRunner:                    runner,
		VClusterKubeconfigServerTemplate:      "https://{cluster_id}.{namespace}:443",
		HTTPClient:                            &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("newGatewayK8sClusterService() error = %v", err)
	}
	cluster, err := service.CreateCluster(context.Background(), ports.K8sClusterCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "create-vc-nodepool",
		Name:           "vc-nodepool",
		Version:        "v1.30.0",
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}
	nodePool, err := service.CreateNodePool(context.Background(), ports.K8sClusterNodePoolCreateRequest{
		TenantID:       "tenant-a",
		ClusterID:      cluster.ClusterID,
		IdempotencyKey: "create-gpu-pool",
		Name:           "gpu-pool",
		NodeCount:      2,
		InstanceType:   "gpu.l4.xlarge",
		GPU: ports.K8sClusterNodePoolGPU{
			Vendor:       "nvidia",
			Model:        "L4",
			Count:        1,
			ResourceName: "nvidia.com/gpu",
		},
	})
	if err != nil {
		t.Fatalf("CreateNodePool() error = %v", err)
	}
	if !strings.Contains(nodePoolPath, "/apis/cluster.x-k8s.io/v1beta1/namespaces/ani-tenant-tenant-a/machinedeployments/gpu-pool") {
		t.Fatalf("path = %q, want Cluster API MachineDeployment path", nodePoolPath)
	}
	spec := nodePoolBody["spec"].(map[string]any)
	template := spec["template"].(map[string]any)
	machineSpec := template["spec"].(map[string]any)
	if machineSpec["version"] != "v1.36.1" {
		t.Fatalf("machine version = %v, want configured CAPI machine version", machineSpec["version"])
	}
	bootstrap := machineSpec["bootstrap"].(map[string]any)
	configRef := bootstrap["configRef"].(map[string]any)
	if configRef["kind"] != "KubeadmConfigTemplate" || configRef["name"] != "vc-nodepool-gpu-pool" || configRef["namespace"] != "ani-tenant-tenant-a" {
		t.Fatalf("bootstrap configRef = %+v, want configured CAPK bootstrap ref", configRef)
	}
	infraRef := machineSpec["infrastructureRef"].(map[string]any)
	if infraRef["kind"] != "KubevirtMachineTemplate" || infraRef["apiVersion"] != "infrastructure.cluster.x-k8s.io/v1alpha1" || infraRef["name"] != "vc-nodepool-gpu-pool" || infraRef["namespace"] != "ani-tenant-tenant-a" {
		t.Fatalf("infrastructureRef = %+v, want configured CAPK infrastructure ref", infraRef)
	}
	if !nodePool.RealProvider || nodePool.Provider != "clusterapi" {
		t.Fatalf("node pool provider evidence = %+v, want clusterapi real provider", nodePool)
	}
}

func TestGatewayK8sClusterServiceFromConfigRejectsInvalidForwardingConfig(t *testing.T) {
	if _, err := newGatewayK8sClusterService(gatewayK8sClusterRuntimeConfig{ProxyMode: "forwarding_static"}); !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("missing static target error = %v, want ErrNotConfigured", err)
	}
	if _, err := newGatewayK8sClusterService(gatewayK8sClusterRuntimeConfig{ProxyMode: "forwarding_metadata"}); !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("missing metadata store error = %v, want ErrNotConfigured", err)
	}
	if _, _, err := newGatewayK8sClusterRuntime(context.Background(), gatewayK8sClusterRuntimeConfig{ProxyMode: "forwarding_metadata"}); !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("missing metadata database URL error = %v, want ErrNotConfigured", err)
	}
	if _, err := newGatewayK8sClusterService(gatewayK8sClusterRuntimeConfig{ProxyMode: "unknown"}); !errors.Is(err, ports.ErrUnsupported) {
		t.Fatalf("unsupported mode error = %v, want ErrUnsupported", err)
	}
}

type gatewayVClusterHelmRunner struct {
	binary string
	args   []string
}

func (r *gatewayVClusterHelmRunner) Run(_ context.Context, binary string, args ...string) ([]byte, error) {
	r.binary = binary
	r.args = append([]string(nil), args...)
	if binary == "vcluster" {
		return []byte("apiVersion: v1\nusers:\n- name: vc-a\n  user:\n    token: tenant-token\n"), nil
	}
	if len(args) > 0 && args[0] == "list" {
		return []byte("[]"), nil
	}
	return []byte("release applied"), nil
}

type gatewayK8sProxyRoundTripper struct {
	statusCode    int
	headers       http.Header
	body          string
	path          string
	query         string
	authorization string
}

func (t *gatewayK8sProxyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	t.path = req.URL.Path
	t.query = req.URL.RawQuery
	t.authorization = req.Header.Get("Authorization")
	if req.Body != nil {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && err != io.EOF {
			return nil, err
		}
		if err := req.Body.Close(); err != nil {
			return nil, err
		}
	}
	return &http.Response{
		StatusCode: t.statusCode,
		Header:     t.headers,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
}

type gatewayK8sProxyMetadataStore struct {
	target       ports.K8sClusterProxyTarget
	usedTenantTx bool
	// clusters 承载 k8s_clusters 表记录，用于验证集群控制面记录落库（不再随网关重启失忆）。
	clusters   map[string]ports.K8sClusterRecord
	createIdem map[string]string
}

func (s *gatewayK8sProxyMetadataStore) clusterRows() map[string]ports.K8sClusterRecord {
	if s.clusters == nil {
		s.clusters = map[string]ports.K8sClusterRecord{}
	}
	return s.clusters
}

func (s *gatewayK8sProxyMetadataStore) createIdempotency() map[string]string {
	if s.createIdem == nil {
		s.createIdem = map[string]string{}
	}
	return s.createIdem
}

func (s *gatewayK8sProxyMetadataStore) Ping(context.Context) error {
	return nil
}

func (s *gatewayK8sProxyMetadataStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	s.usedTenantTx = true
	return fn(ctx, gatewayK8sProxyMetadataTx{store: s})
}

func (s *gatewayK8sProxyMetadataStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, gatewayK8sProxyMetadataTx{store: s})
}

type gatewayK8sProxyMetadataTx struct {
	store *gatewayK8sProxyMetadataStore
}

func (tx gatewayK8sProxyMetadataTx) Exec(_ context.Context, sql string, args ...any) (ports.CommandTag, error) {
	if !strings.Contains(sql, "k8s_clusters") {
		return ports.CommandTag{}, nil
	}
	if strings.Contains(sql, "DELETE FROM k8s_clusters") {
		delete(tx.store.clusterRows(), args[1].(string))
		return ports.CommandTag{RowsAffected: 1}, nil
	}
	if strings.Contains(sql, "SET upgrade_idempotency_key") {
		return ports.CommandTag{RowsAffected: 1}, nil
	}
	record := ports.K8sClusterRecord{
		TenantID:     args[0].(string),
		ClusterID:    args[1].(string),
		Name:         args[2].(string),
		Version:      args[3].(string),
		State:        ports.K8sClusterState(args[4].(string)),
		Reason:       args[5].(string),
		Provider:     args[6].(string),
		RealProvider: args[7].(bool),
		CreatedAt:    args[10].(time.Time).Unix(),
		UpdatedAt:    args[11].(time.Time).Unix(),
	}
	if encoded, ok := args[8].(string); ok {
		_ = json.Unmarshal([]byte(encoded), &record.ProviderRefs)
	}
	// 对应 idx_k8s_clusters_tenant_unique。
	for _, existing := range tx.store.clusterRows() {
		if existing.TenantID == record.TenantID && existing.ClusterID != record.ClusterID {
			return ports.CommandTag{}, ports.ErrConflict
		}
	}
	tx.store.clusterRows()[record.ClusterID] = record
	if key, ok := args[9].(string); ok && key != "" {
		tx.store.createIdempotency()[record.TenantID+"\x00"+key] = record.ClusterID
	}
	return ports.CommandTag{RowsAffected: 1}, nil
}

func (tx gatewayK8sProxyMetadataTx) Query(_ context.Context, sql string, args ...any) (ports.Rows, error) {
	if !strings.Contains(sql, "FROM k8s_clusters") {
		return nil, nil
	}
	tenantID, _ := args[0].(string)
	values := [][]any{}
	for _, record := range tx.store.clusterRows() {
		if record.TenantID != tenantID {
			continue
		}
		values = append(values, gatewayK8sClusterScanValues(record))
	}
	return &gatewayK8sClusterRows{values: values}, nil
}

func (tx gatewayK8sProxyMetadataTx) QueryRow(_ context.Context, sql string, args ...any) ports.Row {
	if !strings.Contains(sql, "FROM k8s_clusters") {
		return gatewayK8sProxyMetadataRow{target: tx.store.target}
	}
	tenantID, _ := args[0].(string)
	clusterID, _ := args[1].(string)
	if strings.Contains(sql, "create_idempotency_key = $2") {
		if id, ok := tx.store.createIdempotency()[tenantID+"\x00"+clusterID]; ok {
			if record, ok := tx.store.clusterRows()[id]; ok {
				return gatewayK8sProxyMetadataRow{cluster: &record}
			}
		}
		return gatewayK8sProxyMetadataRow{err: pgx.ErrNoRows}
	}
	if strings.Contains(sql, "upgrade_idempotency_key = $2") {
		return gatewayK8sProxyMetadataRow{err: pgx.ErrNoRows}
	}
	if record, ok := tx.store.clusterRows()[clusterID]; ok && record.TenantID == tenantID {
		return gatewayK8sProxyMetadataRow{cluster: &record}
	}
	return gatewayK8sProxyMetadataRow{err: pgx.ErrNoRows}
}

type gatewayK8sClusterRows struct {
	values [][]any
	index  int
}

func (r *gatewayK8sClusterRows) Close() {}

func (r *gatewayK8sClusterRows) Err() error { return nil }

func (r *gatewayK8sClusterRows) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *gatewayK8sClusterRows) Scan(dest ...any) error {
	return scanGatewayK8sClusterValues(dest, r.values[r.index-1])
}

func gatewayK8sClusterScanValues(record ports.K8sClusterRecord) []any {
	refs, err := json.Marshal(record.ProviderRefs)
	if err != nil || record.ProviderRefs == nil {
		refs = []byte("[]")
	}
	return []any{
		record.TenantID,
		record.ClusterID,
		record.Name,
		record.Version,
		string(record.State),
		record.Reason,
		record.Provider,
		record.RealProvider,
		refs,
		time.Unix(record.CreatedAt, 0).UTC(),
		time.Unix(record.UpdatedAt, 0).UTC(),
	}
}

func scanGatewayK8sClusterValues(dest []any, values []any) error {
	if len(dest) != 11 || len(values) != 11 {
		return errors.New("unexpected k8s cluster scan destination count")
	}
	*(dest[0].(*string)) = values[0].(string)
	*(dest[1].(*string)) = values[1].(string)
	*(dest[2].(*string)) = values[2].(string)
	*(dest[3].(*string)) = values[3].(string)
	*(dest[4].(*string)) = values[4].(string)
	*(dest[5].(*string)) = values[5].(string)
	*(dest[6].(*string)) = values[6].(string)
	*(dest[7].(*bool)) = values[7].(bool)
	*(dest[8].(*[]byte)) = values[8].([]byte)
	*(dest[9].(*time.Time)) = values[9].(time.Time)
	*(dest[10].(*time.Time)) = values[10].(time.Time)
	return nil
}

type gatewayK8sProxyMetadataRow struct {
	target  ports.K8sClusterProxyTarget
	cluster *ports.K8sClusterRecord
	err     error
}

func (r gatewayK8sProxyMetadataRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.cluster != nil {
		return scanGatewayK8sClusterValues(dest, gatewayK8sClusterScanValues(*r.cluster))
	}
	if len(dest) != 7 {
		return errors.New("unexpected metadata scan destination count")
	}
	*(dest[0].(*string)) = r.target.TenantID
	*(dest[1].(*string)) = r.target.ClusterID
	*(dest[2].(*string)) = r.target.Server
	*(dest[3].(*string)) = r.target.BearerToken
	*(dest[4].(*string)) = r.target.CAData
	*(dest[5].(*string)) = r.target.ClientCertificateData
	*(dest[6].(*string)) = r.target.ClientKeyData
	return nil
}
