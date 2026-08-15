package fake

import (
	"testing"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
)

func TestNewLabResolvesAnyVersion(t *testing.T) {
	image := "registry.ani.internal/platform/vllm-openai-cpu@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	profile := catalog.EngineProfile{ID: "vllm-cpu", Runtime: "vllm", ImageRef: image}
	cat := NewLab(catalog.ModelVersion{
		Ready: true, ArtifactRef: "pvc://vllm-model#/models/qwen", CPUProfile: &profile,
	})
	versionID := uuid.MustParse("05f6f46f-3db8-4551-8497-c46debb4be22")
	got, err := cat.Resolve(t.Context(), uuid.MustParse("11111111-1111-1111-1111-111111111111"), versionID)
	if err != nil || !got.Ready || got.ID != versionID || got.CPUProfile == nil || got.CPUProfile.ImageRef != image {
		t.Fatalf("lab resolve = %+v %v", got, err)
	}
}
