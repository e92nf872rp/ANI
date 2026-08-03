package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/adapters/objectstore"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type gatewayStorageRuntimeConfig struct {
	ProviderMode                      string
	ProviderApply                     bool
	ProviderUserID                    string
	ProviderProof                     string
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	KubernetesProviderManager         string
	KubernetesHTTPClient              *http.Client
	KubernetesRequestTimeout          time.Duration
	ObjectStoreProvider               string
	ObjectStoreEndpoint               string
	ObjectStoreEndpoints              []string
	ObjectStorePublicEndpoint         string
	ObjectStoreAccessKeyID            string
	ObjectStoreSecretAccessKey        string
	ObjectStoreSessionToken           string
	ObjectStoreRegion                 string
	ObjectStoreSecure                 bool
	ObjectStoreBucketPrefix           string
	ObjectStoreHTTPClient             *http.Client
	ObjectStoreRequestTimeout         time.Duration
	DatabaseURL                       string
	MetadataStore                     ports.MetadataStore
}

func gatewayStorageRuntimeConfigFromEnv() gatewayStorageRuntimeConfig {
	return gatewayStorageRuntimeConfig{
		ProviderMode:                      os.Getenv("STORAGE_PROVIDER"),
		ProviderApply:                     strings.EqualFold(strings.TrimSpace(os.Getenv("STORAGE_PROVIDER_APPLY_ENABLED")), "true"),
		ProviderUserID:                    os.Getenv("STORAGE_PROVIDER_USER_ID"),
		ProviderProof:                     os.Getenv("STORAGE_PROVIDER_PERMISSION_PROOF"),
		KubernetesAPIHost:                 os.Getenv("KUBERNETES_API_HOST"),
		KubernetesServiceHost:             os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubernetesServicePort:             os.Getenv("KUBERNETES_SERVICE_PORT"),
		KubernetesBearerToken:             os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesServiceAccountTokenFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"),
		KubernetesServiceAccountCAFile:    os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"),
		KubernetesProviderManager:         os.Getenv("KUBERNETES_PROVIDER_FIELD_MANAGER"),
		KubernetesRequestTimeout:          gatewayDurationFromEnv("KUBERNETES_REQUEST_TIMEOUT"),
		ObjectStoreProvider:               os.Getenv("OBJECT_STORE_PROVIDER"),
		ObjectStoreEndpoint:               os.Getenv("OBJECT_STORE_ENDPOINT"),
		ObjectStoreEndpoints:              splitGatewayCSVEnv(os.Getenv("OBJECT_STORE_ENDPOINTS")),
		ObjectStorePublicEndpoint:         os.Getenv("OBJECT_STORE_PUBLIC_ENDPOINT"),
		ObjectStoreAccessKeyID:            os.Getenv("OBJECT_STORE_ACCESS_KEY_ID"),
		ObjectStoreSecretAccessKey:        os.Getenv("OBJECT_STORE_SECRET_ACCESS_KEY"),
		ObjectStoreSessionToken:           os.Getenv("OBJECT_STORE_SESSION_TOKEN"),
		ObjectStoreRegion:                 os.Getenv("OBJECT_STORE_REGION"),
		ObjectStoreSecure:                 strings.EqualFold(strings.TrimSpace(os.Getenv("OBJECT_STORE_SECURE")), "true"),
		ObjectStoreBucketPrefix:           os.Getenv("OBJECT_STORE_BUCKET_PREFIX"),
		ObjectStoreRequestTimeout:         gatewayDurationFromEnv("OBJECT_STORE_REQUEST_TIMEOUT"),
		DatabaseURL:                       os.Getenv("DATABASE_URL"),
	}
}

func newGatewayStorageService(ctx context.Context, cfg gatewayStorageRuntimeConfig) (ports.StorageService, func(), error) {
	closeRuntime := func() {}
	options := []runtimeadapter.StorageServiceOption{}

	needsStore := storageNeedsControlPlaneStore(cfg)
	if needsStore {
		store, closeStore, err := connectStorageControlPlaneStore(ctx, cfg.DatabaseURL, cfg.MetadataStore)
		if err != nil {
			return nil, closeRuntime, err
		}
		closeRuntime = closeStore
		options = append(options, runtimeadapter.WithStorageResourceStore(runtimeadapter.NewMetadataStorageStore(store)))
	}

	if strings.TrimSpace(cfg.ObjectStoreProvider) == "minio" {
		store, err := objectstore.NewMinIOObjectStore(objectstore.MinIOObjectStoreConfig{
			Endpoint:        cfg.ObjectStoreEndpoint,
			Endpoints:       cfg.ObjectStoreEndpoints,
			PublicEndpoint:  cfg.ObjectStorePublicEndpoint,
			AccessKeyID:     cfg.ObjectStoreAccessKeyID,
			SecretAccessKey: cfg.ObjectStoreSecretAccessKey,
			SessionToken:    cfg.ObjectStoreSessionToken,
			Region:          cfg.ObjectStoreRegion,
			Secure:          cfg.ObjectStoreSecure,
			BucketPrefix:    cfg.ObjectStoreBucketPrefix,
			HTTPClient:      cfg.ObjectStoreHTTPClient,
			RequestTimeout:  cfg.ObjectStoreRequestTimeout,
		})
		if err != nil {
			closeRuntime()
			return nil, func() {}, err
		}
		options = append(options, runtimeadapter.WithStorageObjectStore(store))
	}

	switch mode := strings.TrimSpace(cfg.ProviderMode); mode {
	case "", "local", "not_configured":
		if len(options) > 0 {
			return runtimeadapter.NewLocalStorageService(options...), closeRuntime, nil
		}
		return nil, closeRuntime, nil
	case "kubernetes_rest":
		if strings.TrimSpace(cfg.ProviderUserID) == "" || strings.TrimSpace(cfg.ProviderProof) == "" {
			closeRuntime()
			return nil, func() {}, fmt.Errorf("%w: storage provider requires STORAGE_PROVIDER_USER_ID and STORAGE_PROVIDER_PERMISSION_PROOF", ports.ErrInvalid)
		}
		client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
			Host:            cfg.KubernetesAPIHost,
			ServiceHost:     cfg.KubernetesServiceHost,
			ServicePort:     cfg.KubernetesServicePort,
			BearerToken:     cfg.KubernetesBearerToken,
			BearerTokenFile: cfg.KubernetesServiceAccountTokenFile,
			CAFile:          cfg.KubernetesServiceAccountCAFile,
			FieldManager:    cfg.KubernetesProviderManager,
			HTTPClient:      cfg.KubernetesHTTPClient,
			RequestTimeout:  cfg.KubernetesRequestTimeout,
		})
		if err != nil {
			closeRuntime()
			return nil, func() {}, err
		}
		provider := runtimeadapter.NewKubernetesStorageProviderAdapter(
			client,
			runtimeadapter.WithKubernetesStorageProviderApplyEnabled(cfg.ProviderApply),
		)
		options = append(options,
			runtimeadapter.WithStorageProvider(
				runtimeadapter.NewKubernetesStorageRenderer(),
				provider,
				provider,
				provider,
				runtimeadapter.StorageProviderExecutionConfig{
					UserID:          cfg.ProviderUserID,
					PermissionProof: cfg.ProviderProof,
				},
			),
		)
		return runtimeadapter.NewLocalStorageService(options...), closeRuntime, nil
	default:
		closeRuntime()
		return nil, func() {}, fmt.Errorf("%w: unsupported STORAGE_PROVIDER %q", ports.ErrUnsupported, mode)
	}
}
