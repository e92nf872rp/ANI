package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/adapters/vectorstore"
	"github.com/kubercloud/ani/pkg/ports"
)

type gatewayVectorStoreRuntimeConfig struct {
	VectorStoreProvider         string
	VectorStoreEndpoint         string
	VectorStoreEndpoints        []string
	VectorStoreToken            string
	VectorStoreDatabase         string
	VectorStoreCollectionPrefix string
	VectorStoreHTTPClient       *http.Client
	VectorStoreRequestTimeout   time.Duration
	DatabaseURL                 string
	MetadataStore               ports.MetadataStore
}

func gatewayVectorStoreRuntimeConfigFromEnv() gatewayVectorStoreRuntimeConfig {
	return gatewayVectorStoreRuntimeConfig{
		VectorStoreProvider:         os.Getenv("VECTOR_STORE_PROVIDER"),
		VectorStoreEndpoint:         os.Getenv("VECTOR_STORE_ENDPOINT"),
		VectorStoreEndpoints:        splitGatewayCSVEnv(os.Getenv("VECTOR_STORE_ENDPOINTS")),
		VectorStoreToken:            os.Getenv("VECTOR_STORE_TOKEN"),
		VectorStoreDatabase:         os.Getenv("VECTOR_STORE_DATABASE"),
		VectorStoreCollectionPrefix: os.Getenv("VECTOR_STORE_COLLECTION_PREFIX"),
		VectorStoreRequestTimeout:   gatewayDurationFromEnv("VECTOR_STORE_REQUEST_TIMEOUT"),
		DatabaseURL:                 os.Getenv("DATABASE_URL"),
	}
}

func newGatewayVectorStoreService(ctx context.Context, cfg gatewayVectorStoreRuntimeConfig) (ports.VectorStoreService, func(), error) {
	closeRuntime := func() {}
	switch provider := strings.TrimSpace(cfg.VectorStoreProvider); provider {
	case "", "local", "not_configured":
		return nil, closeRuntime, nil
	case "milvus":
		store, closeStore, err := connectStorageControlPlaneStore(ctx, cfg.DatabaseURL, cfg.MetadataStore)
		if err != nil {
			return nil, closeRuntime, err
		}
		closeRuntime = closeStore
		backend, err := vectorstore.NewMilvusVectorStore(vectorstore.MilvusVectorStoreConfig{
			Endpoint:         cfg.VectorStoreEndpoint,
			Endpoints:        cfg.VectorStoreEndpoints,
			Token:            cfg.VectorStoreToken,
			Database:         cfg.VectorStoreDatabase,
			CollectionPrefix: cfg.VectorStoreCollectionPrefix,
			HTTPClient:       cfg.VectorStoreHTTPClient,
			RequestTimeout:   cfg.VectorStoreRequestTimeout,
		})
		if err != nil {
			closeRuntime()
			return nil, func() {}, err
		}
		service := runtimeadapter.NewLocalVectorStoreService(
			runtimeadapter.WithVectorStoreBackend(backend),
			runtimeadapter.WithVectorStoreResourceStore(runtimeadapter.NewMetadataVectorStoreStore(store)),
		)
		return service, closeRuntime, nil
	default:
		return nil, closeRuntime, fmt.Errorf("%w: unsupported VECTOR_STORE_PROVIDER %q", ports.ErrUnsupported, provider)
	}
}
