package repo

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kubercloud/ani/pkg/types"
)

func TestCreateImportContractAndTenantFences(t *testing.T) {
	var _ interface {
		CreateImport(context.Context, pgx.Tx, CreateImportRequest) (*ImportTask, bool, error)
	} = NewPostgresModelRepo()

	for name, sql := range map[string]string{
		"claim":  claimModelImportSQL,
		"model":  createImportedModelSQL,
		"import": createModelImportTaskSQL,
		"task":   createModelImportAsyncTaskSQL,
		"outbox": createModelImportOutboxSQL,
	} {
		flat := strings.Join(strings.Fields(sql), " ")
		if !strings.Contains(flat, "tenant_id") {
			t.Errorf("%s SQL lacks tenant fence: %s", name, flat)
		}
	}
	if flat := strings.Join(strings.Fields(createImportedModelSQL), " "); !strings.Contains(flat, "VALUES ($1, $2, $3, $4, $5, $6, $7)") {
		t.Fatalf("imported model SQL must persist inferred capabilities: %s", flat)
	}
}

func TestModelImportTargetPathIsTenantScoped(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	importID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	path := modelImportTargetPath(tenantID, modelID, importID)
	for _, part := range []string{tenantID.String(), modelID.String(), importID.String(), "model.tar.gz"} {
		if !strings.Contains(path, part) {
			t.Fatalf("target path %q lacks %q", path, part)
		}
	}
	if !strings.HasPrefix(path, "object://models/") {
		t.Fatalf("target path = %q, want object://models prefix", path)
	}
}

func TestImportedModelNameUsesRepoBasenameAndHashFallback(t *testing.T) {
	if got := importedModelName("Qwen/Qwen3-0.6B", "huggingface", ""); got != "qwen3-0.6b" {
		t.Fatalf("basename = %q", got)
	}
	if got := importedModelName("Qwen/Qwen3-0.6B", "huggingface", "0123456789abcdef"); got != "qwen3-0.6b-01234567" {
		t.Fatalf("hash fallback = %q", got)
	}
}

func TestImportedModelCapabilitiesClassifyEmbeddingRepositories(t *testing.T) {
	tests := map[string][]string{
		"Qwen/Qwen3-Embedding-0.6B": {"embedding"},
		"BAAI/bge-m3":               {"embedding"},
		"intfloat/e5-large-v2":      {"embedding"},
		"Qwen/Qwen3-8B":             {"text-generation"},
	}
	for repoID, want := range tests {
		t.Run(repoID, func(t *testing.T) {
			if got := importedModelCapabilities(repoID); !reflect.DeepEqual(got, want) {
				t.Fatalf("importedModelCapabilities(%q) = %#v, want %#v", repoID, got, want)
			}
		})
	}
}

func TestSetResolvedImportRevisionRequiresActiveTaskLease(t *testing.T) {
	flat := strings.Join(strings.Fields(setResolvedImportRevisionSQL), " ")
	for _, fragment := range []string{
		"async_tasks",
		"async_tasks.tenant_id=$2",
		"async_tasks.status='running'",
		"async_tasks.lease_owner=$4",
		"async_tasks.lease_until > NOW()",
	} {
		if !strings.Contains(flat, fragment) {
			t.Errorf("resolved revision SQL lacks active lease fence %q: %s", fragment, flat)
		}
	}
}

func TestSetResolvedImportRevisionRejectsMissingLeaseOwner(t *testing.T) {
	err := (&PostgresModelRepo{}).SetResolvedImportRevision(
		context.Background(), nil,
		uuid.New(), uuid.New(), uuid.New(), "", "main", strings.Repeat("a", 40),
	)
	if !errors.Is(err, types.ErrBadRequest) {
		t.Fatalf("missing worker ID error = %v, want ErrBadRequest", err)
	}
}
