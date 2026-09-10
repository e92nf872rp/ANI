package repo

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestModelMutationHashIsStableAndScoped(t *testing.T) {
	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	one := ModelMutationHash(tenant, "model.create", "key-1", "qwen", "Qwen", "text-generation")
	two := ModelMutationHash(tenant, "model.create", "key-1", "qwen", "Qwen", "text-generation")
	if one == "" || one != two {
		t.Fatalf("hashes = %q/%q, want stable non-empty hash", one, two)
	}
	if one == ModelMutationHash(uuid.MustParse("22222222-2222-2222-2222-222222222222"), "model.create", "key-1", "qwen", "Qwen", "text-generation") {
		t.Fatalf("tenant must be part of idempotency hash")
	}
}

func TestSoftDeleteSQLHasReferenceFence(t *testing.T) {
	sql := strings.Join(strings.Fields(softDeleteModelSQL), " ")
	if !strings.Contains(sql, "inference_services") || !strings.Contains(sql, "model_versions") || !strings.Contains(sql, "model_version_id") {
		t.Fatalf("soft delete lacks inference reference fence: %s", sql)
	}
}

func TestModelMutationSQLPersistsTenantKeyAndHash(t *testing.T) {
	for name, sql := range map[string]string{
		"claim":  claimModelMutationSQL,
		"record": recordModelMutationSQL,
	} {
		flat := strings.Join(strings.Fields(sql), " ")
		for _, fragment := range []string{"tenant_id", "operation_scope", "idempotency_key", "request_hash"} {
			if !strings.Contains(flat, fragment) {
				t.Errorf("%s SQL lacks %q: %s", name, fragment, flat)
			}
		}
	}
}

func TestModelMutationLockKeyIsPostgresTextSafe(t *testing.T) {
	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	key := modelMutationLockKey(tenant, "model.create", "key-1")
	if strings.ContainsRune(key, '\x00') {
		t.Fatalf("advisory lock text key contains NUL: %q", key)
	}
	for _, want := range []string{tenant.String(), "model.create", "key-1"} {
		if !strings.Contains(key, want) {
			t.Fatalf("advisory lock key %q does not contain %q", key, want)
		}
	}
}
