package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

type fakeTenantNamespaceApplier struct {
	manifests []ports.WorkloadManifest
	err       error
}

func (f *fakeTenantNamespaceApplier) ApplyManifests(_ context.Context, manifests []ports.WorkloadManifest) ([]string, error) {
	f.manifests = append(f.manifests, manifests...)
	if f.err != nil {
		return nil, f.err
	}
	return make([]string, len(manifests)), nil
}

// TestEnsureTenantNamespace_AppliesRenderedNamespace 冻结契约：
// EnsureTenantNamespace 必须复用 renderPlatformWorkloadNamespace 渲染
// （名字、标签与 workload 路径完全一致，避免 SSA 字段管理冲突），
// 且单独 apply 一个 manifest（回滚路径不影响 Namespace）。
func TestEnsureTenantNamespace_AppliesRenderedNamespace(t *testing.T) {
	tenantID := "cfe88569-4960-434c-ad6f-4d78e70fc80d"
	applier := &fakeTenantNamespaceApplier{}
	if err := EnsureTenantNamespace(context.Background(), applier, tenantID); err != nil {
		t.Fatalf("EnsureTenantNamespace() error = %v", err)
	}
	want := renderPlatformWorkloadNamespace(tenantID)
	if len(applier.manifests) != 1 {
		t.Fatalf("applied manifests = %d, want 1", len(applier.manifests))
	}
	got := applier.manifests[0]
	if got.Name != want.Name || got.Kind != want.Kind || got.Provider != want.Provider {
		t.Fatalf("manifest meta = {name:%s kind:%s provider:%s}, want {name:%s kind:%s provider:%s}",
			got.Name, got.Kind, got.Provider, want.Name, want.Kind, want.Provider)
	}
	if got.Content != want.Content {
		t.Fatalf("namespace content = %s, want %s", got.Content, want.Content)
	}
	if want.Name != tenantNamespace(tenantID) {
		t.Fatalf("namespace name %q must match tenantNamespace()", want.Name)
	}
	if !strings.Contains(want.Content, `"app.kubernetes.io/part-of"`) ||
		!strings.Contains(want.Content, "ani-platform") {
		t.Fatalf("namespace content missing platform label: %s", want.Content)
	}
	if !strings.Contains(want.Content, tenantID) {
		t.Fatalf("namespace content missing tenant label: %s", want.Content)
	}
}

func TestEnsureTenantNamespace_RejectsEmptyTenantID(t *testing.T) {
	applier := &fakeTenantNamespaceApplier{}
	err := EnsureTenantNamespace(context.Background(), applier, "   ")
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("EnsureTenantNamespace(empty) error = %v, want ErrInvalid", err)
	}
	if len(applier.manifests) != 0 {
		t.Fatalf("applied manifests = %d, want 0", len(applier.manifests))
	}
}

func TestEnsureTenantNamespace_NilApplier(t *testing.T) {
	err := EnsureTenantNamespace(context.Background(), nil, "cfe88569-4960-434c-ad6f-4d78e70fc80d")
	if !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("EnsureTenantNamespace(nil applier) error = %v, want ErrNotConfigured", err)
	}
}

func TestEnsureTenantNamespace_PropagatesApplyError(t *testing.T) {
	sentinel := errors.New("apply failed")
	applier := &fakeTenantNamespaceApplier{err: sentinel}
	err := EnsureTenantNamespace(context.Background(), applier, "cfe88569-4960-434c-ad6f-4d78e70fc80d")
	if !errors.Is(err, sentinel) {
		t.Fatalf("EnsureTenantNamespace() error = %v, want apply sentinel", err)
	}
}
