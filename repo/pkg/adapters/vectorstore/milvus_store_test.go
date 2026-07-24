package vectorstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestMilvusVectorStoreEnsuresCollectionWithQuickSetupSchema(t *testing.T) {
	t.Parallel()

	var requestBody map[string]any
	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/vectordb/collections/create" {
			t.Fatalf("request = %s %s, want POST /v2/vectordb/collections/create", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer milvus-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return milvusTestJSONResponse(http.StatusOK, `{"code":0,"data":{}}`), nil
	})}

	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoint:         "http://milvus.test",
		Token:            "milvus-token",
		Database:         "ani",
		CollectionPrefix: "ani_",
		HTTPClient:       client,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}

	err = store.EnsureCollection(context.Background(), ports.VectorCollectionRef{TenantID: "tenant-a", KBID: "vst-main"}, 768)
	if err != nil {
		t.Fatalf("EnsureCollection() error = %v", err)
	}

	if requestBody["dbName"] != "ani" || requestBody["collectionName"] != "ani_tenant_a_vst_main" {
		t.Fatalf("request body = %#v, want database and sanitized collection name", requestBody)
	}
	if requestBody["dimension"] != float64(768) || requestBody["metricType"] != "COSINE" {
		t.Fatalf("request body = %#v, want dimension and COSINE metric", requestBody)
	}
	params, ok := requestBody["params"].(map[string]any)
	if !ok || params["max_length"] != "256" {
		t.Fatalf("params = %#v, want VarChar max_length", requestBody["params"])
	}
}

func TestMilvusVectorStoreEnforcesRequestTimeout(t *testing.T) {
	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}

	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoint:       "http://milvus.test",
		HTTPClient:     client,
		RequestTimeout: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}

	err = store.EnsureCollection(context.Background(), ports.VectorCollectionRef{TenantID: "tenant-a", KBID: "vst-main"}, 768)
	if err == nil {
		t.Fatal("EnsureCollection() error = nil, want request timeout")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("EnsureCollection() error = %v, want deadline exceeded", err)
	}
}

func TestMilvusVectorStoreHealthListsCollections(t *testing.T) {
	var requestBody map[string]any
	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/vectordb/collections/list" {
			t.Fatalf("request = %s %s, want POST /v2/vectordb/collections/list", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return milvusTestJSONResponse(http.StatusOK, `{"code":0,"data":[]}`), nil
	})}

	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoint:   "http://milvus.test",
		Database:   "ani",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}

	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if requestBody["dbName"] != "ani" {
		t.Fatalf("request body = %#v, want dbName", requestBody)
	}
}

func TestMilvusAcceptsEndpointList(t *testing.T) {
	var gotHost string
	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Host
		return milvusTestJSONResponse(http.StatusOK, `{"code":0,"data":[]}`), nil
	})}
	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoints:  []string{"http://milvus-a.test:19530", "http://milvus-b.test:19530"},
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}

	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if gotHost != "milvus-a.test:19530" {
		t.Fatalf("host = %q, want first endpoint milvus-a.test:19530", gotHost)
	}
}

func TestMilvusHealthFailsOverEndpointList(t *testing.T) {
	var hosts []string
	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		if r.URL.Host == "milvus-a.test:19530" {
			return milvusTestJSONResponse(http.StatusServiceUnavailable, `{"code":1,"message":"down"}`), nil
		}
		return milvusTestJSONResponse(http.StatusOK, `{"code":0,"data":[]}`), nil
	})}
	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoints:  []string{"http://milvus-a.test:19530", "http://milvus-b.test:19530"},
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}

	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	want := []string{"milvus-a.test:19530", "milvus-b.test:19530"}
	if strings.Join(hosts, ",") != strings.Join(want, ",") {
		t.Fatalf("hosts = %v, want %v", hosts, want)
	}
}

func TestMilvusVectorStoreUpsertPostsRecordsToEntitiesEndpoint(t *testing.T) {
	t.Parallel()

	var requestBody map[string]any
	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/vectordb/entities/upsert" {
			t.Fatalf("request = %s %s, want POST /v2/vectordb/entities/upsert", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return milvusTestJSONResponse(http.StatusOK, `{"code":0,"data":{"upsertCount":2}}`), nil
	})}

	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoint:   "http://milvus.test",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}

	err = store.Upsert(context.Background(), ports.VectorCollectionRef{TenantID: "tenant-a", KBID: "vst-main"}, []ports.VectorRecord{
		{ID: "doc-a", Vector: []float32{0.1, 0.2, 0.3}, Metadata: map[string]string{"source": "unit", "content": "hello"}},
		{ID: "doc-b", Vector: []float32{0.4, 0.5, 0.6}},
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	data, ok := requestBody["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("data = %#v, want two records", requestBody["data"])
	}
	first, ok := data[0].(map[string]any)
	if !ok {
		t.Fatalf("first record = %#v, want object", data[0])
	}
	if first["id"] != "doc-a" || first["content"] != "hello" {
		t.Fatalf("first record = %#v, want id and content", first)
	}
	vector, ok := first["vector"].([]any)
	if !ok || len(vector) != 3 {
		t.Fatalf("vector = %#v, want 3 dimensions", first["vector"])
	}
	metadata, ok := first["metadata"].(map[string]any)
	if !ok || metadata["source"] != "unit" {
		t.Fatalf("metadata = %#v, want source", first["metadata"])
	}
}

func TestMilvusVectorStoreSearchMapsHits(t *testing.T) {
	t.Parallel()

	var requestBody map[string]any
	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/vectordb/entities/search" {
			t.Fatalf("request = %s %s, want POST /v2/vectordb/entities/search", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return milvusTestJSONResponse(http.StatusOK, `{"code":0,"data":[{"id":"doc-a","score":0.98,"metadata":{"source":"unit","content":"hello"}}]}`), nil
	})}
	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoint:   "http://milvus.test",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}

	results, err := store.Search(context.Background(), ports.VectorSearchQuery{
		Collection: ports.VectorCollectionRef{TenantID: "tenant-a", KBID: "vst-main"},
		Vector:     []float32{0.1, 0.2, 0.3},
		TopK:       5,
		Filter:     map[string]string{"source": "unit"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if requestBody["limit"] != float64(5) || !strings.Contains(requestBody["filter"].(string), "source") {
		t.Fatalf("request body = %#v, want limit and filter", requestBody)
	}
	if len(results) != 1 || results[0].ID != "doc-a" || results[0].Score != 0.98 {
		t.Fatalf("results = %#v, want mapped Milvus hit", results)
	}
	if results[0].Metadata["source"] != "unit" || results[0].Metadata["content"] != "hello" {
		t.Fatalf("metadata = %#v, want Milvus metadata", results[0].Metadata)
	}
}

func TestMilvusVectorStoreMapsNotFoundHealth(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return milvusTestJSONResponse(http.StatusOK, `{"code":100,"message":"collection not found"}`), nil
	})}
	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoint:   "http://milvus.test",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}

	health, err := store.CollectionHealth(context.Background(), ports.VectorCollectionRef{TenantID: "tenant-a", KBID: "missing"})
	if err != nil {
		t.Fatalf("CollectionHealth() error = %v", err)
	}
	if health.Ready || !strings.Contains(health.Reason, "collection not found") {
		t.Fatalf("health = %#v, want not ready with reason", health)
	}
}

func TestMilvusVectorStoreDeleteByExprPostsFilterToEntitiesDelete(t *testing.T) {
	t.Parallel()

	var requestBody map[string]any
	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/vectordb/entities/delete" {
			t.Fatalf("request = %s %s, want POST /v2/vectordb/entities/delete", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return milvusTestJSONResponse(http.StatusOK, `{"code":0,"data":{"deleteCount":7}}`), nil
	})}
	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoint:   "http://milvus.test",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}

	count, err := store.DeleteByExpr(context.Background(), ports.VectorCollectionRef{TenantID: "tenant-a", KBID: "vst-main"}, `doc_id == "abc"`)
	if err != nil {
		t.Fatalf("DeleteByExpr() error = %v", err)
	}
	if count != 7 {
		t.Fatalf("count = %d, want 7", count)
	}
	if requestBody["filter"] != `doc_id == "abc"` {
		t.Fatalf("request body = %#v, want filter expression", requestBody)
	}
	if requestBody["collectionName"] != "tenant_a_vst_main" {
		t.Fatalf("collectionName = %#v, want sanitized name", requestBody)
	}
}

func TestMilvusVectorStoreDeleteByExprRequiresFilter(t *testing.T) {
	t.Parallel()

	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{Endpoint: "http://milvus.test"})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}
	if _, err := store.DeleteByExpr(context.Background(), ports.VectorCollectionRef{}, "  "); err == nil {
		t.Fatalf("DeleteByExpr() error = nil, want invalid filter")
	}
}

func TestMilvusVectorStoreDeleteByExprMapsServiceUnavailable(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: vectorRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return milvusTestJSONResponse(http.StatusServiceUnavailable, `{"code":1,"message":"milvus down"}`), nil
	})}
	store, err := NewMilvusVectorStore(MilvusVectorStoreConfig{
		Endpoint:   "http://milvus.test",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewMilvusVectorStore() error = %v", err)
	}
	_, err = store.DeleteByExpr(context.Background(), ports.VectorCollectionRef{TenantID: "tenant-a", KBID: "vst-main"}, `doc_id == "abc"`)
	if !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("DeleteByExpr error = %v, want ErrUnavailable", err)
	}
}

type vectorRoundTripFunc func(*http.Request) (*http.Response, error)

func (f vectorRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func milvusTestJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
