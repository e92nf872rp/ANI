package config

import (
	"os"
	"strings"
	"testing"
)

func TestInferenceDeploymentWiresBoundedMaterializationConfig(t *testing.T) {
	raw, err := os.ReadFile("../../../../deploy/real-k8s-lab/inference-incluster-e2e.yaml")
	if err != nil {
		t.Fatalf("read inference deployment: %v", err)
	}
	manifest := string(raw)
	for _, required := range []string{
		"name: MODEL_SERVICE_GRPC_ADDR",
		"model_service_grpc_addr: model-service.ani-system.svc.cluster.local:9103",
		"name: MODEL_FETCHER_IMAGE_REF",
		"name: ani-inference-materialization",
		"key: model_fetcher_image_ref",
		"model_fetcher_allow_insecure_http: \"false\"",
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("deployment is missing %q", required)
		}
	}
	for _, forbidden := range []string{"MODEL_DOWNLOAD_URL", "MINIO_SECRET", "MINIO_ACCESS_KEY"} {
		if strings.Contains(manifest, forbidden) {
			t.Fatalf("deployment must not contain sensitive materialization setting %q", forbidden)
		}
	}
}

func TestModelServiceDeploymentWiresMinIOObjectStoreFromSecret(t *testing.T) {
	raw, err := os.ReadFile("../../../../deploy/real-k8s-lab/inference-incluster-e2e.yaml")
	if err != nil {
		t.Fatalf("read inference deployment: %v", err)
	}
	manifest := string(raw)
	var modelDeployment string
	for _, document := range strings.Split(manifest, "\n---\n") {
		if strings.Contains(document, "kind: Deployment") && strings.Contains(document, "\n  name: model-service\n") {
			modelDeployment = document
			break
		}
	}
	if modelDeployment == "" {
		t.Fatal("model-service Deployment is missing")
	}
	for _, required := range []string{
		"name: OBJECT_STORE_PROVIDER",
		"value: minio",
		"name: OBJECT_STORE_ENDPOINT",
		"name: OBJECT_STORE_PUBLIC_ENDPOINT",
		"name: OBJECT_STORE_ACCESS_KEY_ID",
		"name: OBJECT_STORE_SECRET_ACCESS_KEY",
		"name: ani-objectstore-production-shaped-runtime",
		"key: endpoint",
		"key: public_endpoint",
		"key: access_key_id",
		"key: secret_access_key",
		"name: OBJECT_STORE_REGION",
		"value: us-east-1",
		"name: OBJECT_STORE_SECURE",
		"value: \"false\"",
		"name: OBJECT_STORE_BUCKET_PREFIX",
		"value: ani-s13-",
	} {
		if !strings.Contains(modelDeployment, required) {
			t.Fatalf("model-service deployment is missing %q", required)
		}
	}
}
