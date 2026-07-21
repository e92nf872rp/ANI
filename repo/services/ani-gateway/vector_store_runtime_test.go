package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestGatewayVectorStoreServiceFromConfigDefaultsToRouterLocalService(t *testing.T) {
	service, err := newGatewayVectorStoreService(gatewayVectorStoreRuntimeConfig{})
	if err != nil {
		t.Fatalf("newGatewayVectorStoreService() error = %v", err)
	}
	if service != nil {
		t.Fatalf("service = %T, want nil so router keeps local default", service)
	}
}

func TestGatewayVectorStoreServiceCanInjectMilvusBackend(t *testing.T) {
	seen := map[string]int{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen[r.URL.Path]++
		if got := r.Header.Get("Authorization"); got != "Bearer milvus-token" {
			t.Fatalf("Authorization = %q, want Milvus bearer token", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode Milvus request body: %v", err)
		}
		if body["dbName"] != "ani" {
			t.Fatalf("Milvus request body = %#v, want dbName", body)
		}
		switch r.URL.Path {
		case "/v2/vectordb/collections/create":
			if body["dimension"] != float64(3) {
				t.Fatalf("create body = %#v, want dimension 3", body)
			}
			return jsonResponse(http.StatusOK, `{"code":0,"data":{}}`), nil
		case "/v2/vectordb/entities/upsert":
			return jsonResponse(http.StatusOK, `{"code":0,"data":{"upsertCount":1}}`), nil
		case "/v2/vectordb/entities/search":
			return jsonResponse(http.StatusOK, `{"code":0,"data":[{"id":"doc-a","score":0.99,"metadata":{"source":"gateway"}}]}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{"code":404,"message":"unexpected path"}`), nil
		}
	})}

	service, err := newGatewayVectorStoreService(gatewayVectorStoreRuntimeConfig{
		VectorStoreProvider:         "milvus",
		VectorStoreEndpoint:         "http://milvus.internal:19530",
		VectorStoreToken:            "milvus-token",
		VectorStoreDatabase:         "ani",
		VectorStoreCollectionPrefix: "ani_s13_",
		VectorStoreHTTPClient:       client,
	})
	if err != nil {
		t.Fatalf("newGatewayVectorStoreService() error = %v", err)
	}
	if service == nil {
		t.Fatal("service = nil, want Milvus-backed vector store service")
	}

	store, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "vector-create-a",
		Name:           "kb-main",
		Dimension:      3,
		Metric:         "cosine",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}
	result, err := service.InsertDocuments(context.Background(), ports.VectorStoreDocumentInsertRequest{
		TenantID:       "tenant-a",
		ResourceID:     store.StoreID,
		IdempotencyKey: "vector-insert-a",
		Documents: []ports.VectorDocumentInput{
			{ID: "doc-a", Content: "hello vector", Metadata: map[string]string{"source": "gateway"}},
		},
	})
	if err != nil {
		t.Fatalf("InsertDocuments() error = %v", err)
	}
	hits, err := service.SearchVectorStore(context.Background(), ports.VectorStoreResourceSearchRequest{
		TenantID:   "tenant-a",
		ResourceID: store.StoreID,
		Vector:     []float32{0.1, 0.2, 0.3},
		TopK:       1,
	})
	if err != nil {
		t.Fatalf("SearchVectorStore() error = %v", err)
	}
	if result.InsertedCount != 1 || len(hits) != 1 || hits[0].ID != "doc-a" {
		t.Fatalf("insert/search result = %+v/%+v, want one Milvus hit", result, hits)
	}
	for _, path := range []string{"/v2/vectordb/collections/create", "/v2/vectordb/entities/upsert", "/v2/vectordb/entities/search"} {
		if seen[path] != 1 {
			t.Fatalf("Milvus path %s calls = %d, want 1", path, seen[path])
		}
	}
}

func TestGatewayVectorStoreConfigFromEnvIncludesMilvusProvider(t *testing.T) {
	t.Setenv("VECTOR_STORE_PROVIDER", "milvus")
	t.Setenv("VECTOR_STORE_ENDPOINT", "http://milvus.example:19530")
	t.Setenv("VECTOR_STORE_ENDPOINTS", "http://milvus-a.example:19530,http://milvus-b.example:19530")
	t.Setenv("VECTOR_STORE_TOKEN", "milvus-token")
	t.Setenv("VECTOR_STORE_DATABASE", "ani")
	t.Setenv("VECTOR_STORE_COLLECTION_PREFIX", "ani_s13_")

	cfg := gatewayVectorStoreRuntimeConfigFromEnv()
	if cfg.VectorStoreProvider != "milvus" || cfg.VectorStoreEndpoint != "http://milvus.example:19530" {
		t.Fatalf("vector store provider config not loaded from env: %#v", cfg)
	}
	if len(cfg.VectorStoreEndpoints) != 2 || cfg.VectorStoreEndpoints[0] != "http://milvus-a.example:19530" || cfg.VectorStoreEndpoints[1] != "http://milvus-b.example:19530" {
		t.Fatalf("vector store endpoints = %#v, want parsed endpoint list", cfg.VectorStoreEndpoints)
	}
	if cfg.VectorStoreToken == "" || cfg.VectorStoreDatabase != "ani" || cfg.VectorStoreCollectionPrefix != "ani_s13_" {
		t.Fatalf("vector store credentials/database/prefix not loaded from env")
	}
}

func TestGatewayVectorStoreServiceRejectsUnsupportedProvider(t *testing.T) {
	if _, err := newGatewayVectorStoreService(gatewayVectorStoreRuntimeConfig{VectorStoreProvider: "memory"}); err == nil {
		t.Fatal("newGatewayVectorStoreService() error = nil, want unsupported provider error")
	}
}
