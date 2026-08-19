package main

import (
	"testing"

	"github.com/kubercloud/ani/services/inference-service/internal/config"
)

func TestNewModelCatalogRequiresModelService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when MODEL_SERVICE_GRPC_ADDR is empty")
		}
	}()
	newModelCatalog(config.Config{})
}

func TestLabCatalogEnvDoesNotBypassModelService(t *testing.T) {
	t.Setenv("INFERENCE_LAB_CATALOG", "1")
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when INFERENCE_LAB_CATALOG is set")
		}
	}()
	newModelCatalog(config.Config{ModelServiceGRPCAddr: "model-service.ani-system.svc.cluster.local:9103"})
}

func TestNewInferenceRuntimeRequiresCoreAPI(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when CORE_API_BASE_URL is empty")
		}
	}()
	newInferenceRuntime(config.Config{})
}
