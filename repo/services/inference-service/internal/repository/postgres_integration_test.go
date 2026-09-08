//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

func TestPublicationAndResolvePublishedServiceIntegration(t *testing.T) {
	dsn := os.Getenv("INFERENCE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("INFERENCE_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adminConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	admin, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaName := "inference_c1_" + suffix
	tenantRole := "inference_tenant_" + suffix
	platformRole := "inference_platform_" + suffix
	quotedSchema := pgx.Identifier{schemaName}.Sanitize()
	quotedTenantRole := pgx.Identifier{tenantRole}.Sanitize()
	quotedPlatformRole := pgx.Identifier{platformRole}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		for _, statement := range []string{
			"SET search_path TO public",
			"DROP SCHEMA " + quotedSchema + " CASCADE",
			"DROP ROLE IF EXISTS " + quotedTenantRole,
			"DROP ROLE IF EXISTS " + quotedPlatformRole,
		} {
			if _, cleanupErr := admin.Exec(ctx, statement); cleanupErr != nil {
				t.Errorf("integration cleanup %q: %v", statement, cleanupErr)
			}
		}
	}()
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'tenant-test'", quotedTenantRole)); err != nil {
		t.Fatalf("create tenant test role: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'platform-test' BYPASSRLS", quotedPlatformRole)); err != nil {
		t.Fatalf("create platform test role: %v", err)
	}
	if _, err := admin.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("select isolated schema: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("ALTER ROLE %s SET search_path TO %s", quotedTenantRole, quotedSchema)); err != nil {
		t.Fatalf("configure tenant role search path: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("ALTER ROLE %s SET search_path TO %s", quotedPlatformRole, quotedSchema)); err != nil {
		t.Fatalf("configure platform role search path: %v", err)
	}

	base := `
CREATE TABLE tenants (id UUID PRIMARY KEY);
CREATE TABLE model_versions (id UUID PRIMARY KEY);
CREATE TABLE inference_services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    model_version_id UUID NOT NULL REFERENCES model_versions(id),
    replicas INT NOT NULL DEFAULT 1,
    gpu_type TEXT,
    gpu_count_per_pod INT NOT NULL DEFAULT 1,
    max_concurrency INT NOT NULL DEFAULT 8,
    placement_region TEXT,
    placement_az TEXT,
    status TEXT NOT NULL DEFAULT 'pending'
      CHECK (status IN ('pending','downloading','decrypting','deploying','running','stopping','stopped','failed')),
    endpoint_url TEXT,
    k8s_namespace TEXT,
    k8s_deployment_name TEXT,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);
ALTER TABLE inference_services ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_services FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON inference_services AS RESTRICTIVE
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
`
	if _, err := admin.Exec(ctx, base); err != nil {
		t.Fatalf("create base schema: %v", err)
	}
	tenantID := uuid.New()
	legacyID := uuid.New()
	versionID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO tenants(id) VALUES($1)`, tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO model_versions(id) VALUES($1)`, versionID); err != nil {
		t.Fatalf("seed model version: %v", err)
	}
	if _, err := admin.Exec(ctx, `
INSERT INTO inference_services(id, tenant_id, name, model_version_id, replicas, gpu_type, status)
VALUES($1, $2, 'legacy-gpu', $3, 2, 'A100', 'stopped')`, legacyID, tenantID, versionID); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	migrationPath := filepath.Join("..", "..", "..", "..", "deploy", "migrations", "20260814000100_inference_control_plane.sql")
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	if _, err := admin.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("reapply migration: %v", err)
	}
	publicationMigrationPath := filepath.Join("..", "..", "..", "..", "deploy", "migrations", "20260831_001_inference_gateway_publication.sql")
	publicationMigration, err := os.ReadFile(publicationMigrationPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, string(publicationMigration)); err != nil {
		t.Fatalf("apply publication migration: %v", err)
	}
	if _, err := admin.Exec(ctx, string(publicationMigration)); err != nil {
		t.Fatalf("reapply publication migration: %v", err)
	}
	accessPolicyMigrationPath := filepath.Join("..", "..", "..", "..", "deploy", "migrations", "20260828_001_inference_access_policy.sql")
	accessPolicyMigration, err := os.ReadFile(accessPolicyMigrationPath)
	if err != nil {
		t.Fatal(err)
	}
	legacyAccessPolicyMigration := strings.Replace(string(accessPolicyMigration), "'scope','allow','deny'", "'allow','deny'", 1)
	legacyAccessPolicyMigration = strings.Replace(legacyAccessPolicyMigration, "PRIMARY KEY (policy_id, api_key_id, effect)", "PRIMARY KEY (policy_id, api_key_id)", 1)
	if legacyAccessPolicyMigration == string(accessPolicyMigration) {
		t.Fatal("access policy fixture did not produce legacy key-effect schema")
	}
	if _, err := admin.Exec(ctx, legacyAccessPolicyMigration); err != nil {
		t.Fatalf("apply legacy access policy migration: %v", err)
	}
	if _, err := admin.Exec(ctx, legacyAccessPolicyMigration); err != nil {
		t.Fatalf("reapply legacy access policy migration: %v", err)
	}
	historicalScopedPolicyID, historicalDefaultPolicyID, historicalServicePolicyID := uuid.New(), uuid.New(), uuid.New()
	historicalAllowKey, historicalDenyKey, historicalDefaultKey, historicalServiceKey := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO inference_access_policies(id,tenant_id,name,status,priority,scope_type,allow_all_tenant_keys) VALUES($1,$2,'historical-scoped','enabled',1,'api_key',false),($3,$2,'historical-default','enabled',2,'tenant_default',false),($5,$2,'historical-service','enabled',3,'inference_service',false)`, historicalScopedPolicyID, tenantID, historicalDefaultPolicyID, historicalServicePolicyID); err != nil {
		t.Fatalf("seed historical access policies: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO inference_access_policy_api_keys(policy_id,tenant_id,api_key_id,key_prefix,effect) VALUES($1,$2,$3,'legacy','allow'),($1,$2,$4,'legacy','deny'),($5,$2,$6,'legacy','allow'),($7,$2,$8,'legacy','allow')`, historicalScopedPolicyID, tenantID, historicalAllowKey, historicalDenyKey, historicalDefaultPolicyID, historicalDefaultKey, historicalServicePolicyID, historicalServiceKey); err != nil {
		t.Fatalf("seed historical access policy keys: %v", err)
	}
	accessPolicyForwardMigrationPath := filepath.Join("..", "..", "..", "..", "deploy", "migrations", "20260831_002_inference_access_policy_key_effects.sql")
	accessPolicyForwardMigration, err := os.ReadFile(accessPolicyForwardMigrationPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = admin.Exec(ctx, string(accessPolicyForwardMigration))
	var preflightErr *pgconn.PgError
	if err == nil || !errors.As(err, &preflightErr) || preflightErr.Code != "P0001" || preflightErr.Message != "C41_ACCESS_POLICY_SCOPE_RECONCILIATION_REQUIRED" {
		t.Fatalf("legacy enabled policy migration error = %v", err)
	}
	if _, rollbackErr := admin.Exec(ctx, "ROLLBACK"); rollbackErr != nil {
		t.Fatalf("rollback failed legacy migration: %v", rollbackErr)
	}
	var failedStatus, failedPrimaryKey string
	var failedRLS, failedForceRLS bool
	if err := admin.QueryRow(ctx, `SELECT status FROM inference_access_policies WHERE id=$1`, historicalScopedPolicyID).Scan(&failedStatus); err != nil {
		t.Fatalf("read failed migration policy status: %v", err)
	}
	if err := admin.QueryRow(ctx, `SELECT pg_get_constraintdef(constraint.oid) FROM pg_constraint AS constraint WHERE constraint.conrelid='inference_access_policy_api_keys'::regclass AND constraint.contype='p'`).Scan(&failedPrimaryKey); err != nil {
		t.Fatalf("read failed migration primary key: %v", err)
	}
	if err := admin.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid='inference_access_policy_api_keys'::regclass`).Scan(&failedRLS, &failedForceRLS); err != nil {
		t.Fatalf("read failed migration rls state: %v", err)
	}
	if failedStatus != "enabled" || failedPrimaryKey != "PRIMARY KEY (policy_id, api_key_id)" || !failedRLS || !failedForceRLS {
		t.Fatalf("legacy migration rollback status=%q pkey=%q rls=%v force=%v", failedStatus, failedPrimaryKey, failedRLS, failedForceRLS)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_access_policies SET status='disabled' WHERE id=$1`, historicalScopedPolicyID); err != nil {
		t.Fatalf("disable ambiguous historical policy: %v", err)
	}
	if _, err := admin.Exec(ctx, string(accessPolicyForwardMigration)); err != nil {
		t.Fatalf("upgrade disabled legacy policy key effects: %v", err)
	}
	postMigrationPolicyID, postMigrationKey := uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO inference_access_policies(id,tenant_id,name,status,priority,scope_type,allow_all_tenant_keys) VALUES($1,$2,'post-migration-scoped','enabled',3,'api_key',false)`, postMigrationPolicyID, tenantID); err != nil {
		t.Fatalf("seed post-migration scoped policy: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO inference_access_policy_api_keys(policy_id,tenant_id,api_key_id,key_prefix,effect) VALUES($1,$2,$3,'current','scope')`, postMigrationPolicyID, tenantID, postMigrationKey); err != nil {
		t.Fatalf("seed post-migration scope key: %v", err)
	}
	if _, err := admin.Exec(ctx, string(accessPolicyForwardMigration)); err != nil {
		t.Fatalf("reapply access policy key effects: %v", err)
	}
	accessPolicyIdempotencyMigrationPath := filepath.Join("..", "..", "..", "..", "deploy", "migrations", "20260901_001_inference_access_policy_idempotency.sql")
	accessPolicyIdempotencyMigration, err := os.ReadFile(accessPolicyIdempotencyMigrationPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, string(accessPolicyIdempotencyMigration)); err != nil {
		t.Fatalf("apply access policy idempotency migration: %v", err)
	}
	if _, err := admin.Exec(ctx, string(accessPolicyIdempotencyMigration)); err != nil {
		t.Fatalf("reapply access policy idempotency migration: %v", err)
	}
	var scopedScopeRows, defaultScopeRows, scopedAllowRows, scopedDenyRows int
	var scopedStatus, defaultStatus, serviceStatus, postMigrationStatus string
	if err := admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE policy_id=$1 AND effect='scope'), count(*) FILTER (WHERE policy_id=$2 AND effect='scope'), count(*) FILTER (WHERE policy_id=$1 AND effect='allow'), count(*) FILTER (WHERE policy_id=$1 AND effect='deny') FROM inference_access_policy_api_keys`, historicalScopedPolicyID, historicalDefaultPolicyID).Scan(&scopedScopeRows, &defaultScopeRows, &scopedAllowRows, &scopedDenyRows); err != nil {
		t.Fatalf("read upgraded access policy keys: %v", err)
	}
	if err := admin.QueryRow(ctx, `SELECT (SELECT status FROM inference_access_policies WHERE id=$1), (SELECT status FROM inference_access_policies WHERE id=$2), (SELECT status FROM inference_access_policies WHERE id=$3), (SELECT status FROM inference_access_policies WHERE id=$4)`, historicalScopedPolicyID, historicalDefaultPolicyID, historicalServicePolicyID, postMigrationPolicyID).Scan(&scopedStatus, &defaultStatus, &serviceStatus, &postMigrationStatus); err != nil {
		t.Fatalf("read upgraded access policy statuses: %v", err)
	}
	if scopedScopeRows != 0 || defaultScopeRows != 0 || scopedAllowRows != 1 || scopedDenyRows != 1 || scopedStatus != "disabled" || defaultStatus != "enabled" || serviceStatus != "enabled" || postMigrationStatus != "enabled" {
		t.Fatalf("upgraded policy effects scope=%d default-scope=%d allow=%d deny=%d", scopedScopeRows, defaultScopeRows, scopedAllowRows, scopedDenyRows)
	}
	var quarantined bool
	var desiredState string
	var desiredSpec []byte
	if err := admin.QueryRow(ctx,
		`SELECT legacy_quarantined, desired_state, desired_spec FROM inference_services WHERE id=$1`, legacyID,
	).Scan(&quarantined, &desiredState, &desiredSpec); err != nil {
		t.Fatal(err)
	}
	if !quarantined || desiredState != "stopped" || len(desiredSpec) <= 2 {
		t.Fatalf("legacy row was not safely backfilled: quarantined=%v desired=%s spec=%s", quarantined, desiredState, desiredSpec)
	}
	grants := fmt.Sprintf(`
GRANT USAGE ON SCHEMA %s TO %s, %s;
GRANT SELECT, INSERT, UPDATE, DELETE ON inference_services, inference_operations, inference_access_policies, inference_access_policy_services, inference_access_policy_api_keys, inference_access_policy_events, inference_access_policy_mutations TO %s, %s;
GRANT SELECT ON tenants, model_versions TO %s, %s;
`, quotedSchema, quotedTenantRole, quotedPlatformRole, quotedTenantRole, quotedPlatformRole, quotedTenantRole, quotedPlatformRole)
	if _, err := admin.Exec(ctx, grants); err != nil {
		t.Fatal(err)
	}

	tenantPool := openRolePool(t, dsn, tenantRole, "tenant-test")
	defer tenantPool.Close()
	platformPool := openRolePool(t, dsn, platformRole, "platform-test")
	defer platformPool.Close()
	store := NewPostgres(tenantPool, platformPool)

	service, operation := integrationCreateFixture(tenantID, versionID, "native-service", "sha256:same")
	result, err := store.CreateWithOperation(ctx, service, operation)
	if err != nil {
		t.Fatalf("create service+operation: %v", err)
	}
	if result.Replayed {
		t.Fatal("first create unexpectedly replayed")
	}
	scopeKey, allowKey, denyKey := uuid.New(), uuid.New(), uuid.New()
	policy := domain.AccessPolicy{
		ID: uuid.New(), TenantID: tenantID, Name: "round-trip-policy", Status: domain.AccessPolicyEnabled, Priority: 1,
		Scope:       domain.AccessPolicyScope{Type: domain.ScopeInferenceServiceAPIKey, InferenceServiceIDs: []uuid.UUID{service.ID}, APIKeyIDs: []string{scopeKey.String()}},
		Access:      domain.AccessPolicyAccess{AllowAPIKeyIDs: []string{allowKey.String()}, DenyAPIKeyIDs: []string{denyKey.String()}},
		Concurrency: domain.AccessPolicyConcurrency{LeaseTTLSeconds: 60},
	}
	policyKey := uuid.New()
	createdPolicy, err := store.CreateAccessPolicy(ctx, policy, policyKey)
	if err != nil {
		t.Fatalf("create access policy: %v", err)
	}
	if createdPolicy.ID != policy.ID || createdPolicy.CreatedAt.IsZero() || createdPolicy.UpdatedAt.IsZero() {
		t.Fatalf("created policy = %+v", createdPolicy)
	}
	replayedPolicy, err := store.CreateAccessPolicy(ctx, policy, policyKey)
	if err != nil || replayedPolicy.ID != createdPolicy.ID || !replayedPolicy.CreatedAt.Equal(createdPolicy.CreatedAt) {
		t.Fatalf("create policy replay = (%+v,%v)", replayedPolicy, err)
	}
	conflictingPolicy := policy
	conflictingPolicy.Priority++
	if _, err := store.CreateAccessPolicy(ctx, conflictingPolicy, policyKey); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("create policy conflicting replay = %v", err)
	}
	roundTripped, err := store.GetAccessPolicy(ctx, tenantID, policy.ID)
	if err != nil {
		t.Fatalf("get access policy: %v", err)
	}
	if !containsString(roundTripped.Scope.APIKeyIDs, scopeKey.String()) || containsString(roundTripped.Scope.APIKeyIDs, allowKey.String()) ||
		!containsString(roundTripped.Access.AllowAPIKeyIDs, allowKey.String()) || !containsString(roundTripped.Access.DenyAPIKeyIDs, denyKey.String()) {
		t.Fatalf("access policy round-trip = %+v", roundTripped)
	}
	bindingKey := uuid.New()
	bound, err := store.ReplaceServiceAccessPolicies(ctx, tenantID, service.ID, []uuid.UUID{policy.ID}, bindingKey)
	if err != nil || len(bound) != 1 || bound[0].ID != policy.ID {
		t.Fatalf("bind access policy = (%+v,%v)", bound, err)
	}
	replayedBinding, err := store.ReplaceServiceAccessPolicies(ctx, tenantID, service.ID, []uuid.UUID{policy.ID}, bindingKey)
	if err != nil || len(replayedBinding) != 1 || replayedBinding[0].ID != policy.ID {
		t.Fatalf("binding replay = (%+v,%v)", replayedBinding, err)
	}
	if _, err := store.ReplaceServiceAccessPolicies(ctx, tenantID, service.ID, nil, bindingKey); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("binding conflicting replay = %v", err)
	}
	updateKey := uuid.New()
	updateHash := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	updatedIntent := roundTripped
	updatedIntent.Description = "updated"
	updatedPolicy, err := store.UpdateAccessPolicy(ctx, updatedIntent, updateKey, updateHash)
	if err != nil || updatedPolicy.Description != "updated" || updatedPolicy.UpdatedAt.Before(updatedPolicy.CreatedAt) {
		t.Fatalf("update access policy = (%+v,%v)", updatedPolicy, err)
	}
	intervening := updatedPolicy
	intervening.Priority = 2000
	interveningPolicy, err := store.UpdateAccessPolicy(ctx, intervening, uuid.New(), "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	if err != nil || interveningPolicy.Priority != 2000 {
		t.Fatalf("intervening update = (%+v,%v)", interveningPolicy, err)
	}
	retryIntent := interveningPolicy
	retryIntent.Description = "updated"
	replayedUpdate, err := store.UpdateAccessPolicy(ctx, retryIntent, updateKey, updateHash)
	if err != nil || replayedUpdate.Description != "updated" || replayedUpdate.Priority != updatedPolicy.Priority || !replayedUpdate.UpdatedAt.Equal(updatedPolicy.UpdatedAt) {
		t.Fatalf("update policy replay = (%+v,%v)", replayedUpdate, err)
	}
	conflictingUpdate := updatedIntent
	conflictingUpdate.Description = "conflict"
	if _, err := store.UpdateAccessPolicy(ctx, conflictingUpdate, updateKey, "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("update policy conflicting replay = %v", err)
	}
	event := domain.AccessPolicyEvent{ID: uuid.New(), TenantID: tenantID, InferenceServiceID: &service.ID, Decision: "allow", ReasonCode: "ALLOWED", HTTPStatus: 200}
	if err := store.RecordAccessPolicyEvent(ctx, event); err != nil {
		t.Fatalf("record access policy event: %v", err)
	}
	events, _, err := store.ListAccessPolicyEvents(ctx, tenantID, domain.AccessPolicyEventQuery{Limit: 1})
	if err != nil || len(events) != 1 || events[0].CreatedAt.IsZero() {
		t.Fatalf("access policy events = (%+v,%v)", events, err)
	}
	deleteKey := uuid.New()
	if err := store.DeleteAccessPolicy(ctx, tenantID, policy.ID, deleteKey); err != nil {
		t.Fatalf("delete access policy: %v", err)
	}
	if err := store.DeleteAccessPolicy(ctx, tenantID, policy.ID, deleteKey); err != nil {
		t.Fatalf("delete access policy replay: %v", err)
	}
	replay, found, err := store.FindCreateReplay(ctx, tenantID, operation.OperationScope, operation.IdempotencyKey, operation.RequestHash)
	if err != nil || !found || replay.Service.ID != service.ID || replay.Operation.ID != operation.ID {
		t.Fatalf("replay = (%+v,%v,%v)", replay, found, err)
	}
	if _, _, err := store.FindCreateReplay(ctx, tenantID, operation.OperationScope, operation.IdempotencyKey, "sha256:different"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different hash error = %v", err)
	}

	concurrentKey := uuid.New()
	concurrentServiceA, concurrentOpA := integrationCreateFixture(tenantID, versionID, "concurrent-service", "sha256:concurrent")
	concurrentServiceB, concurrentOpB := integrationCreateFixture(tenantID, versionID, "concurrent-service", "sha256:concurrent")
	concurrentOpA.IdempotencyKey, concurrentOpB.IdempotencyKey = concurrentKey, concurrentKey
	var wg sync.WaitGroup
	results := make(chan CreateResult, 2)
	errorsCh := make(chan error, 2)
	for _, pair := range [][2]any{{concurrentServiceA, concurrentOpA}, {concurrentServiceB, concurrentOpB}} {
		wg.Add(1)
		go func(resource domain.Service, op domain.Operation) {
			defer wg.Done()
			created, err := store.CreateWithOperation(ctx, resource, op)
			results <- created
			errorsCh <- err
		}(pair[0].(domain.Service), pair[1].(domain.Operation))
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent create: %v", err)
		}
	}
	var ids []uuid.UUID
	for created := range results {
		ids = append(ids, created.Service.ID)
	}
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("concurrent idempotency created different services: %v", ids)
	}

	tenant2ID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO tenants(id) VALUES($1)`, tenant2ID); err != nil {
		t.Fatalf("seed second tenant: %v", err)
	}
	tenant2Service, tenant2Operation := integrationCreateFixture(tenant2ID, versionID, "tenant-two-service", "sha256:tenant-two")
	if _, err := store.CreateWithOperation(ctx, tenant2Service, tenant2Operation); err != nil {
		t.Fatalf("create second tenant service: %v", err)
	}
	tenant1List, err := store.ListServices(ctx, tenantID)
	if err != nil {
		t.Fatalf("list tenant one services: %v", err)
	}
	if !containsService(tenant1List, service.ID) || containsService(tenant1List, tenant2Service.ID) {
		t.Fatalf("tenant one list crossed tenant boundary: %+v", tenant1List)
	}
	tenant2List, err := store.ListServices(ctx, tenant2ID)
	if err != nil {
		t.Fatalf("list tenant two services: %v", err)
	}
	if !containsService(tenant2List, tenant2Service.ID) || containsService(tenant2List, service.ID) {
		t.Fatalf("tenant two list crossed tenant boundary: %+v", tenant2List)
	}
	if _, err := store.GetService(ctx, tenantID, tenant2Service.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant service lookup error = %v", err)
	}
	if _, err := store.GetOperation(ctx, tenantID, tenant2Operation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant operation lookup error = %v", err)
	}
	var visibleWithoutContext int
	if err := tenantPool.QueryRow(ctx, `SELECT count(*) FROM inference_services`).Scan(&visibleWithoutContext); err != nil {
		t.Fatalf("query without tenant context: %v", err)
	}
	if visibleWithoutContext != 0 {
		t.Fatalf("tenant role without context saw %d service(s)", visibleWithoutContext)
	}
	tenantTx, err := tenantPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tenantTx.Rollback(ctx)
	if err := setTenant(ctx, tenantTx, tenantID); err != nil {
		t.Fatal(err)
	}
	var crossTenantVisible int
	if err := tenantTx.QueryRow(ctx, `SELECT count(*) FROM inference_services WHERE tenant_id=$1`, tenant2ID).Scan(&crossTenantVisible); err != nil {
		t.Fatal(err)
	}
	if crossTenantVisible != 0 {
		t.Fatalf("tenant one saw %d tenant two service(s)", crossTenantVisible)
	}
	if err := tenantTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var platformVisible int
	if err := platformPool.QueryRow(ctx, `SELECT count(*) FROM inference_services`).Scan(&platformVisible); err != nil {
		t.Fatalf("platform role query: %v", err)
	}
	if platformVisible < 3 {
		t.Fatalf("platform role saw %d services, want at least 3", platformVisible)
	}

	rollbackService, rollbackOperation := integrationCreateFixture(tenantID, versionID, "must-rollback", "sha256:rollback")
	rollbackOperation.Type = domain.Action("invalid")
	if _, err := store.CreateWithOperation(ctx, rollbackService, rollbackOperation); err == nil {
		t.Fatal("invalid operation unexpectedly committed")
	}
	var rolledBackServices int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM inference_services WHERE id=$1`, rollbackService.ID).Scan(&rolledBackServices); err != nil {
		t.Fatal(err)
	}
	if rolledBackServices != 0 {
		t.Fatal("service insert was not rolled back after operation insert failed")
	}

	mutationService, mutationCreate := integrationCreateFixture(tenantID, versionID, "concurrent-mutation", "sha256:mutation-create")
	if _, err := store.CreateWithOperation(ctx, mutationService, mutationCreate); err != nil {
		t.Fatalf("create concurrent mutation fixture: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_operations SET state='completed', completed_at=NOW() WHERE id=$1`, mutationCreate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET status='running', applied_spec=desired_spec, observed_generation=generation
WHERE id=$1`, mutationService.ID); err != nil {
		t.Fatal(err)
	}
	mutationKey := uuid.New()
	mutationTarget := mutationService.DesiredSpec
	mutationTarget.Replicas = 2
	mutationRequest := MutationRequest{
		TenantID: tenantID, ServiceID: mutationService.ID, Action: domain.ActionScale,
		OperationID: uuid.New(), OperationScope: "inference_service.scale",
		IdempotencyKey: mutationKey, RequestHash: "sha256:concurrent-mutation",
		TargetSpec: mutationTarget, Now: time.Now().UTC(),
	}
	mutationRequests := []MutationRequest{mutationRequest, mutationRequest}
	mutationRequests[1].OperationID = uuid.New()
	mutationResults := make(chan MutationResult, 2)
	mutationErrors := make(chan error, 2)
	var mutationWG sync.WaitGroup
	for _, request := range mutationRequests {
		mutationWG.Add(1)
		go func(request MutationRequest) {
			defer mutationWG.Done()
			result, err := store.MutateService(ctx, request)
			mutationResults <- result
			mutationErrors <- err
		}(request)
	}
	mutationWG.Wait()
	close(mutationResults)
	close(mutationErrors)
	for err := range mutationErrors {
		if err != nil {
			t.Fatalf("concurrent mutation: %v", err)
		}
	}
	var mutationOperationIDs []uuid.UUID
	for result := range mutationResults {
		mutationOperationIDs = append(mutationOperationIDs, result.Operation.ID)
	}
	if len(mutationOperationIDs) != 2 || mutationOperationIDs[0] != mutationOperationIDs[1] {
		t.Fatalf("concurrent mutation created different operations: %v", mutationOperationIDs)
	}
	mutatedService, err := store.GetService(ctx, tenantID, mutationService.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mutatedService.Generation != 2 || mutatedService.DesiredSpec.Replicas != 2 {
		t.Fatalf("concurrent mutation applied more than once: %+v", mutatedService)
	}

	lifecycleService, lifecycleCreate := integrationCreateFixture(tenantID, versionID, "lifecycle-service", "sha256:lifecycle-create")
	if _, err := store.CreateWithOperation(ctx, lifecycleService, lifecycleCreate); err != nil {
		t.Fatalf("create lifecycle fixture: %v", err)
	}
	if err := store.BindRuntimeRef(ctx, RuntimeBinding{
		TenantID: tenantID, ServiceID: lifecycleService.ID, OperationID: lifecycleCreate.ID,
		Generation: lifecycleCreate.TargetGeneration, RuntimeRef: uuid.New(),
	}); err != nil {
		t.Fatalf("bind lifecycle create runtime before safe preemption: %v", err)
	}
	stopRequest := MutationRequest{
		TenantID: tenantID, ServiceID: lifecycleService.ID, Action: domain.ActionStop,
		OperationID: uuid.New(), OperationScope: "inference_service.stop",
		IdempotencyKey: uuid.New(), RequestHash: "sha256:lifecycle-stop", Now: time.Now().UTC(),
	}
	stoppedIntent, err := store.MutateService(ctx, stopRequest)
	if err != nil {
		t.Fatalf("stop preempts create: %v", err)
	}
	if stoppedIntent.Disposition != domain.TransitionCreated || stoppedIntent.Service.Generation != 2 ||
		stoppedIntent.Operation.PreemptedOperationID != lifecycleCreate.ID {
		t.Fatalf("stop transition = %+v", stoppedIntent)
	}
	if stoppedIntent.Service.Publication.Desired != domain.PublicationUnpublished ||
		stoppedIntent.Service.Publication.Generation != 2 ||
		stoppedIntent.Service.Publication.Phase != domain.PublicationPending ||
		stoppedIntent.Service.InvocationURL != "" {
		t.Fatalf("stop transition did not atomically withdraw publication: %+v", stoppedIntent.Service.Publication)
	}
	persistedStop, err := store.GetService(ctx, tenantID, lifecycleService.ID)
	if err != nil || persistedStop.Publication.Desired != domain.PublicationUnpublished ||
		persistedStop.Publication.Generation != 2 || persistedStop.Publication.Phase != domain.PublicationPending ||
		persistedStop.InvocationURL != "" {
		t.Fatalf("persisted stop publication = (%+v,%v)", persistedStop, err)
	}
	preemptedCreate, err := store.GetOperation(ctx, tenantID, lifecycleCreate.ID)
	if err != nil || preemptedCreate.State != domain.OperationCancelled {
		t.Fatalf("preempted create = (%+v,%v)", preemptedCreate, err)
	}
	stopReplay, err := store.MutateService(ctx, stopRequest)
	if err != nil || stopReplay.Operation.ID != stoppedIntent.Operation.ID || !stopReplay.Operation.Replayed {
		t.Fatalf("stop replay = (%+v,%v)", stopReplay, err)
	}
	if stopReplay.Service.Generation != 2 || stopReplay.Service.Publication.Generation != 2 {
		t.Fatalf("stop replay advanced generation: %+v", stopReplay.Service)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE inference_operations SET state='completed', completed_at=NOW() WHERE id=$1`, stoppedIntent.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET status='stopped', desired_state='stopped', applied_spec=desired_spec,
    observed_generation=generation, ready_replicas=0, runtime_endpoint=NULL
WHERE id=$1`, lifecycleService.ID); err != nil {
		t.Fatal(err)
	}
	noOpRequest := stopRequest
	noOpRequest.OperationID = uuid.New()
	noOpRequest.IdempotencyKey = uuid.New()
	noOpRequest.RequestHash = "sha256:lifecycle-stop-noop"
	noOpRequest.Now = noOpRequest.Now.Add(time.Second)
	stopNoop, err := store.MutateService(ctx, noOpRequest)
	if err != nil {
		t.Fatalf("already stopped no-op: %v", err)
	}
	if stopNoop.Disposition != domain.TransitionAlreadyDesired || stopNoop.Operation.State != domain.OperationCompleted ||
		stopNoop.Service.Generation != 2 || stopNoop.Service.Publication.Generation != 2 {
		t.Fatalf("already stopped result = %+v", stopNoop)
	}
	rollbackMutation := MutationRequest{
		TenantID: tenantID, ServiceID: lifecycleService.ID, Action: domain.ActionStart,
		OperationID: lifecycleCreate.ID, OperationScope: "inference_service.start",
		IdempotencyKey: uuid.New(), RequestHash: "sha256:lifecycle-start-rollback", Now: noOpRequest.Now.Add(time.Second),
	}
	if _, err := store.MutateService(ctx, rollbackMutation); err == nil {
		t.Fatal("duplicate operation id unexpectedly committed lifecycle mutation")
	}
	rolledBackMutation, err := store.GetService(ctx, tenantID, lifecycleService.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBackMutation.Generation != 2 || rolledBackMutation.CurrentOperationID != stopNoop.Operation.ID ||
		rolledBackMutation.Status != domain.StatusStopped {
		t.Fatalf("lifecycle mutation did not roll back: %+v", rolledBackMutation)
	}

	for _, action := range []domain.Action{domain.ActionStop, domain.ActionRestart, domain.ActionDelete} {
		fixture, createOperation := integrationCreateFixture(
			tenantID, versionID, "publication-withdraw-"+string(action), "sha256:withdraw-"+string(action),
		)
		if _, err := store.CreateWithOperation(ctx, fixture, createOperation); err != nil {
			t.Fatalf("create %s withdrawal fixture: %v", action, err)
		}
		if _, err := admin.Exec(ctx, `UPDATE inference_operations SET state='completed', completed_at=NOW() WHERE id=$1`, createOperation.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `UPDATE inference_services
SET status='running', desired_state='running', applied_spec=desired_spec, observed_generation=generation,
    runtime_ref=$2, runtime_endpoint='http://runtime.test', invocation_url='https://gateway.test/v1',
    publication_desired='published', publication_generation=1,
    publication_observed_generation=1, publication_phase='published', publication_updated_at=NOW()
WHERE id=$1`, fixture.ID, uuid.New()); err != nil {
			t.Fatal(err)
		}
		request := MutationRequest{
			TenantID: tenantID, ServiceID: fixture.ID, Action: action,
			OperationID: uuid.New(), OperationScope: "inference_service." + string(action),
			IdempotencyKey: uuid.New(), RequestHash: "sha256:withdraw-request-" + string(action),
			Now: time.Now().UTC(),
		}
		result, err := store.MutateService(ctx, request)
		if err != nil {
			t.Fatalf("%s withdrawal mutation: %v", action, err)
		}
		persisted, err := store.GetService(ctx, tenantID, fixture.ID)
		if err != nil {
			t.Fatal(err)
		}
		for label, candidate := range map[string]domain.Service{"returned": result.Service, "persisted": persisted} {
			if candidate.Generation != 2 || candidate.Publication.Desired != domain.PublicationUnpublished ||
				candidate.Publication.Generation != 2 || candidate.Publication.Phase != domain.PublicationPending ||
				candidate.InvocationURL != "" {
				t.Fatalf("%s %s withdrawal is not atomic: %+v", action, label, candidate)
			}
		}
	}

	unboundService, unboundCreate := integrationCreateFixture(tenantID, versionID, "unbound-create", "sha256:unbound-create")
	if _, err := store.CreateWithOperation(ctx, unboundService, unboundCreate); err != nil {
		t.Fatalf("create unbound request-path fixture: %v", err)
	}
	unboundStop := MutationRequest{
		TenantID: tenantID, ServiceID: unboundService.ID, Action: domain.ActionStop,
		OperationID: uuid.New(), OperationScope: "inference_service.stop", IdempotencyKey: uuid.New(),
		RequestHash: "sha256:unbound-stop", Now: time.Now().UTC(),
	}
	if _, err := store.MutateService(ctx, unboundStop); !errors.Is(err, domain.ErrOperationInProgress) {
		t.Fatalf("unbound create preemption error = %v", err)
	}
	unboundAfter, err := store.GetService(ctx, tenantID, unboundService.ID)
	if err != nil || unboundAfter.Generation != 1 || unboundAfter.CurrentOperationID != unboundCreate.ID {
		t.Fatalf("unbound create changed after rejected preemption: (%+v,%v)", unboundAfter, err)
	}
	unboundOperation, err := store.GetOperation(ctx, tenantID, unboundCreate.ID)
	if err != nil || unboundOperation.State != domain.OperationPending {
		t.Fatalf("unbound create operation changed after rejected preemption: (%+v,%v)", unboundOperation, err)
	}

	runningService, runningCreate := integrationCreateFixture(tenantID, versionID, "running-preemption", "sha256:running-preemption")
	if _, err := store.CreateWithOperation(ctx, runningService, runningCreate); err != nil {
		t.Fatalf("create running preemption fixture: %v", err)
	}
	runningRuntimeRef, runningLease := uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `UPDATE inference_operations
SET state='running', lease_owner='request-path', lease_until=NOW()+INTERVAL '5 minutes', lease_token=$2
WHERE id=$1`, runningCreate.ID, runningLease); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET status='deploying', runtime_ref=$2, invocation_url='https://gateway.test/v1',
    publication_desired='published', publication_generation=1,
    publication_observed_generation=1, publication_phase='published', publication_updated_at=NOW()
WHERE id=$1`, runningService.ID, runningRuntimeRef); err != nil {
		t.Fatal(err)
	}
	runningStop := MutationRequest{
		TenantID: tenantID, ServiceID: runningService.ID, Action: domain.ActionStop,
		OperationID: uuid.New(), OperationScope: "inference_service.stop", IdempotencyKey: uuid.New(),
		RequestHash: "sha256:running-stop", Now: time.Now().UTC(),
	}
	if _, err := store.MutateService(ctx, runningStop); !errors.Is(err, domain.ErrOperationInProgress) {
		t.Fatalf("claimed operation preemption error = %v", err)
	}
	runningAfter, err := store.GetService(ctx, tenantID, runningService.ID)
	if err != nil || runningAfter.Generation != 1 || runningAfter.CurrentOperationID != runningCreate.ID ||
		runningAfter.Publication.Desired != domain.PublicationPublished ||
		runningAfter.Publication.Generation != 1 || runningAfter.InvocationURL == "" {
		t.Fatalf("claimed operation preemption was not rolled back: (%+v,%v)", runningAfter, err)
	}
	runningOperation, err := store.GetOperation(ctx, tenantID, runningCreate.ID)
	if err != nil || runningOperation.State != domain.OperationRunning || runningOperation.LeaseToken != runningLease {
		t.Fatalf("claimed operation changed after rejected preemption: (%+v,%v)", runningOperation, err)
	}

	withdrawalRollback, withdrawalRollbackCreate := integrationCreateFixture(
		tenantID, versionID, "withdrawal-rollback", "sha256:withdrawal-rollback",
	)
	if _, err := store.CreateWithOperation(ctx, withdrawalRollback, withdrawalRollbackCreate); err != nil {
		t.Fatalf("create withdrawal rollback fixture: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_operations SET state='completed', completed_at=NOW() WHERE id=$1`, withdrawalRollbackCreate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET status='running', desired_state='running', applied_spec=desired_spec, observed_generation=generation,
    runtime_ref=$2, runtime_endpoint='http://runtime.test', invocation_url='https://gateway.test/v1',
    publication_desired='published', publication_generation=1,
    publication_observed_generation=1, publication_phase='published', publication_updated_at=NOW()
WHERE id=$1`, withdrawalRollback.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateService(ctx, MutationRequest{
		TenantID: tenantID, ServiceID: withdrawalRollback.ID, Action: domain.ActionRestart,
		OperationID: withdrawalRollbackCreate.ID, OperationScope: "inference_service.restart",
		IdempotencyKey: uuid.New(), RequestHash: "sha256:withdrawal-rollback-restart", Now: time.Now().UTC(),
	}); err == nil {
		t.Fatal("duplicate operation id unexpectedly committed publication withdrawal")
	}
	withdrawalRollbackAfter, err := store.GetService(ctx, tenantID, withdrawalRollback.ID)
	if err != nil || withdrawalRollbackAfter.Generation != 1 || withdrawalRollbackAfter.Status != domain.StatusRunning ||
		withdrawalRollbackAfter.Publication.Desired != domain.PublicationPublished ||
		withdrawalRollbackAfter.Publication.Generation != 1 ||
		withdrawalRollbackAfter.Publication.Phase != domain.PublicationPublishedOK ||
		withdrawalRollbackAfter.InvocationURL != "https://gateway.test/v1" {
		t.Fatalf("failed restart partially committed withdrawal: (%+v,%v)", withdrawalRollbackAfter, err)
	}

	claimTime := time.Now().UTC()
	if _, err := admin.Exec(ctx, `UPDATE inference_operations SET state='completed' WHERE id <> $1`, operation.ID); err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		operation domain.Operation
		claimed   bool
		err       error
	}
	claimResults := make(chan claimResult, 2)
	var claimWG sync.WaitGroup
	for _, owner := range []string{"worker-a", "worker-b"} {
		claimWG.Add(1)
		go func(owner string) {
			defer claimWG.Done()
			claimed, ok, err := store.ClaimOperation(ctx, owner, claimTime, time.Second)
			claimResults <- claimResult{operation: claimed, claimed: ok, err: err}
		}(owner)
	}
	claimWG.Wait()
	close(claimResults)
	var claimedA domain.Operation
	claimWinners := 0
	for claim := range claimResults {
		if claim.err != nil {
			t.Fatalf("concurrent claim: %v", claim.err)
		}
		if claim.claimed {
			claimWinners++
			claimedA = claim.operation
		}
	}
	if claimWinners != 1 || claimedA.ID != operation.ID {
		t.Fatalf("claim winners=%d operation=%s, want one winner for %s", claimWinners, claimedA.ID, operation.ID)
	}
	claimedB, ok, err := store.ClaimOperation(ctx, "worker-takeover", claimTime.Add(2*time.Second), time.Minute)
	if err != nil || !ok || claimedA.ID != claimedB.ID || claimedA.LeaseToken == claimedB.LeaseToken {
		t.Fatalf("lease takeover = (%+v,%v,%v)", claimedB, ok, err)
	}
	stale := Observation{
		TenantID: claimedA.TenantID, ServiceID: claimedA.ServiceID, OperationID: claimedA.ID,
		TargetGeneration: claimedA.TargetGeneration, Status: domain.StatusRunning,
		AppliedSpec: claimedA.TargetSpec, RuntimeRef: uuid.New(), ReadyReplicas: 0,
		LeaseToken: claimedA.LeaseToken, Complete: true, Publish: true,
	}
	var beforeStatus, beforePublicationDesired, beforePublicationPhase, beforeInvocationURL string
	var beforeObservedGeneration, beforePublicationGeneration, beforePublicationObservedGeneration int64
	if err := admin.QueryRow(ctx, `SELECT status, observed_generation, publication_desired,
publication_generation, publication_observed_generation, publication_phase, COALESCE(invocation_url,'')
FROM inference_services WHERE id=$1`, service.ID).Scan(
		&beforeStatus, &beforeObservedGeneration, &beforePublicationDesired,
		&beforePublicationGeneration, &beforePublicationObservedGeneration,
		&beforePublicationPhase, &beforeInvocationURL,
	); err != nil {
		t.Fatal(err)
	}
	assertObservationUnchanged := func(label string) {
		t.Helper()
		var statusValue, desiredValue, phaseValue, invocationURL string
		var observedValue, publicationGeneration, publicationObserved int64
		if err := admin.QueryRow(ctx, `SELECT status, observed_generation, publication_desired,
publication_generation, publication_observed_generation, publication_phase, COALESCE(invocation_url,'')
FROM inference_services WHERE id=$1`, service.ID).Scan(
			&statusValue, &observedValue, &desiredValue, &publicationGeneration,
			&publicationObserved, &phaseValue, &invocationURL,
		); err != nil {
			t.Fatal(err)
		}
		if statusValue != beforeStatus || observedValue != beforeObservedGeneration ||
			desiredValue != beforePublicationDesired || publicationGeneration != beforePublicationGeneration ||
			publicationObserved != beforePublicationObservedGeneration || phaseValue != beforePublicationPhase ||
			invocationURL != beforeInvocationURL {
			t.Fatalf("%s stale observation partially committed: status=%s observed=%d publication=%s/%d/%d/%s url=%q",
				label, statusValue, observedValue, desiredValue, publicationGeneration, publicationObserved, phaseValue, invocationURL)
		}
	}
	if err := store.ApplyObservation(ctx, stale); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("expired lease write error = %v", err)
	}
	assertObservationUnchanged("expired lease")
	wrongGeneration := stale
	wrongGeneration.TargetGeneration++
	wrongGeneration.LeaseToken = claimedB.LeaseToken
	if err := store.ApplyObservation(ctx, wrongGeneration); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("wrong generation write error = %v", err)
	}
	assertObservationUnchanged("wrong generation")
	wrongOperation := stale
	wrongOperation.OperationID = uuid.New()
	wrongOperation.LeaseToken = claimedB.LeaseToken
	if err := store.ApplyObservation(ctx, wrongOperation); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("wrong current operation write error = %v", err)
	}
	assertObservationUnchanged("wrong operation")
	current := stale
	current.LeaseToken = claimedB.LeaseToken
	current.Status = domain.StatusRunning
	current.ReadyReplicas = current.AppliedSpec.Replicas
	current.Complete = true
	current.Publish = true
	if _, err := admin.Exec(ctx, `UPDATE inference_operations
SET error_code='GATEWAY_UNPUBLISH_PENDING', error_message='old gateway wait', next_attempt_at=NOW()-INTERVAL '1 minute'
WHERE id=$1`, claimedB.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyObservation(ctx, current); err != nil {
		t.Fatalf("current lease completion: %v", err)
	}
	var completedErrorCode, completedErrorMessage string
	var completedNextAttempt, completedAt time.Time
	if err := admin.QueryRow(ctx, `SELECT COALESCE(error_code,''), COALESCE(error_message,''), next_attempt_at, completed_at
FROM inference_operations WHERE id=$1`, claimedB.ID).Scan(
		&completedErrorCode, &completedErrorMessage, &completedNextAttempt, &completedAt,
	); err != nil {
		t.Fatal(err)
	}
	if completedErrorCode != "" || completedErrorMessage != "" || !completedNextAttempt.Equal(completedAt) {
		t.Fatalf("completed operation retained retry residue: code=%q message=%q next=%v completed=%v",
			completedErrorCode, completedErrorMessage, completedNextAttempt, completedAt)
	}
	completedService, err := store.GetService(ctx, tenantID, service.ID)
	if err != nil || completedService.Publication.Desired != domain.PublicationPublished ||
		completedService.Publication.Generation != current.TargetGeneration ||
		completedService.Publication.Phase != domain.PublicationPending || completedService.InvocationURL != "" {
		t.Fatalf("completed create did not atomically request publication: (%+v,%v)", completedService, err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services SET deleted_at=NOW() WHERE id=$1`, service.ID); err != nil {
		t.Fatal(err)
	}
	visibleAfterTombstone, err := store.ListServices(ctx, tenantID)
	if err != nil {
		t.Fatalf("list after tombstone: %v", err)
	}
	if containsService(visibleAfterTombstone, service.ID) {
		t.Fatalf("tombstoned service remained in list: %+v", visibleAfterTombstone)
	}
	tombstoneReplay, found, err := store.FindCreateReplay(ctx, tenantID, operation.OperationScope, operation.IdempotencyKey, operation.RequestHash)
	if err != nil || !found || tombstoneReplay.Service.ID != service.ID || tombstoneReplay.Operation.ID != operation.ID {
		t.Fatalf("tombstone replay = (%+v,%v,%v)", tombstoneReplay, found, err)
	}

	for _, testCase := range []struct {
		action      domain.Action
		wantPublish bool
	}{
		{action: domain.ActionCreate, wantPublish: true},
		{action: domain.ActionStart, wantPublish: true},
		{action: domain.ActionRestart, wantPublish: true},
		{action: domain.ActionScale, wantPublish: false},
		{action: domain.ActionStop, wantPublish: false},
		{action: domain.ActionDelete, wantPublish: false},
	} {
		fixture, fixtureOperation := integrationCreateFixture(
			tenantID, versionID, "observation-"+string(testCase.action), "sha256:observation-"+string(testCase.action),
		)
		if _, err := store.CreateWithOperation(ctx, fixture, fixtureOperation); err != nil {
			t.Fatalf("create %s observation fixture: %v", testCase.action, err)
		}
		targetGeneration := int64(1)
		if testCase.action != domain.ActionCreate {
			targetGeneration = 2
		}
		leaseToken := uuid.New()
		if _, err := admin.Exec(ctx, `UPDATE inference_operations
SET type=$2, state='running', target_generation=$3, lease_owner='observation-test',
    lease_until=NOW()+INTERVAL '5 minutes', lease_token=$4
WHERE id=$1`, fixtureOperation.ID, testCase.action, targetGeneration, leaseToken); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `UPDATE inference_services
SET status='deploying', desired_state='running', generation=$2, current_operation_id=$3,
    runtime_ref=$4, runtime_endpoint='http://runtime.test', invocation_url=NULL,
    publication_desired='unpublished', publication_generation=$2,
    publication_observed_generation=$2, publication_phase='unpublished', publication_updated_at=NOW()
WHERE id=$1`, fixture.ID, targetGeneration, fixtureOperation.ID, uuid.New()); err != nil {
			t.Fatal(err)
		}
		observation := Observation{
			TenantID: tenantID, ServiceID: fixture.ID, OperationID: fixtureOperation.ID,
			TargetGeneration: targetGeneration, Status: domain.StatusRunning,
			AppliedSpec: fixtureOperation.TargetSpec, RuntimeRef: uuid.New(),
			RuntimeEndpoint: "http://runtime.test", ReadyReplicas: 1,
			Complete: true, Publish: true, LeaseToken: leaseToken,
		}
		partial := observation
		partial.Status = domain.StatusDeploying
		partial.Complete = false
		if err := store.ApplyObservation(ctx, partial); err != nil {
			t.Fatalf("apply partial %s observation: %v", testCase.action, err)
		}
		partialService, err := store.GetService(ctx, tenantID, fixture.ID)
		if err != nil {
			t.Fatal(err)
		}
		if partialService.Publication.Desired != domain.PublicationUnpublished ||
			partialService.Publication.Generation != targetGeneration ||
			partialService.Publication.Phase != domain.PublicationUnpublishedOK || partialService.InvocationURL != "" {
			t.Fatalf("partial %s Publish=true changed publication: %+v", testCase.action, partialService)
		}
		if err := store.ApplyObservation(ctx, observation); err != nil {
			t.Fatalf("apply %s observation: %v", testCase.action, err)
		}
		observed, err := store.GetService(ctx, tenantID, fixture.ID)
		if err != nil {
			t.Fatal(err)
		}
		if testCase.wantPublish {
			if observed.Publication.Desired != domain.PublicationPublished ||
				observed.Publication.Generation != targetGeneration ||
				observed.Publication.Phase != domain.PublicationPending || observed.InvocationURL != "" {
				t.Fatalf("%s completion did not request publication: %+v", testCase.action, observed)
			}
		} else if observed.Publication.Desired != domain.PublicationUnpublished ||
			observed.Publication.Generation != targetGeneration ||
			observed.Publication.Phase != domain.PublicationUnpublishedOK || observed.InvocationURL != "" {
			t.Fatalf("malicious %s Publish=true changed publication: %+v", testCase.action, observed)
		}
	}
	// Earlier lifecycle fixtures deliberately leave publication work pending.
	// Settle them before testing exact ClaimPublication ordering so this fixture
	// is the only eligible publisher candidate.
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET publication_observed_generation=publication_generation,
    publication_phase=CASE WHEN publication_desired='published' THEN 'published' ELSE 'unpublished' END,
    invocation_url=CASE WHEN publication_desired='published' THEN COALESCE(invocation_url,'https://gateway.test/v1') ELSE NULL END,
    publication_updated_at=NOW()
WHERE deleted_at IS NULL`); err != nil {
		t.Fatalf("settle earlier publication fixtures: %v", err)
	}

	publicationService, publicationOperation := integrationCreateFixture(tenantID, versionID, "published-model", "sha256:publication")
	if _, err := store.CreateWithOperation(ctx, publicationService, publicationOperation); err != nil {
		t.Fatalf("create publication fixture: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET status='running', desired_state='running', applied_spec=desired_spec, observed_generation=generation,
    runtime_endpoint='http://runtime.test', publication_desired='published', publication_generation=1,
    publication_observed_generation=0, publication_phase='pending', publication_updated_at=NOW()
WHERE id=$1`, publicationService.ID); err != nil {
		t.Fatal(err)
	}
	publicationNow := time.Now().UTC()
	claimedPublication, ok, err := store.ClaimPublication(ctx, "publication-worker", publicationNow, time.Minute)
	if err != nil || !ok || claimedPublication.ServiceID != publicationService.ID || claimedPublication.LeaseToken == uuid.Nil {
		t.Fatalf("claim publication = (%+v,%v,%v)", claimedPublication, ok, err)
	}
	if err := store.CompletePublication(ctx, PublicationResult{
		TenantID: tenantID, ServiceID: publicationService.ID, Generation: 1,
		LeaseToken: claimedPublication.LeaseToken, Phase: domain.PublicationPublishedOK,
		InvocationURL: "https://gateway.test/v1", Now: publicationNow.Add(time.Second),
	}); err != nil {
		t.Fatalf("complete publication: %v", err)
	}
	published, err := store.ResolvePublishedService(ctx, tenantID, publicationService.ServedModelName)
	if err != nil || published.ID != publicationService.ID || published.InvocationURL == "" || published.Publication.Phase != domain.PublicationPublishedOK {
		t.Fatalf("resolve published service = (%+v,%v)", published, err)
	}
	if _, err := store.ResolvePublishedService(ctx, tenant2ID, publicationService.ServedModelName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant published service lookup error = %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET publication_desired='published', publication_generation=2, publication_observed_generation=1,
    publication_phase='pending', invocation_url='https://old.test', publication_updated_at=NOW()
WHERE id=$1`, publicationService.ID); err != nil {
		t.Fatal(err)
	}
	failedPublication, ok, err := store.ClaimPublication(ctx, "publication-worker", publicationNow.Add(2*time.Second), time.Minute)
	if err != nil || !ok || failedPublication.Generation != 2 {
		t.Fatalf("claim failing publication = (%+v,%v,%v)", failedPublication, ok, err)
	}
	if err := store.FailPublication(ctx, failedPublication, "token=must-not-persist", publicationNow.Add(3*time.Second)); err != nil {
		t.Fatalf("fail publication: %v", err)
	}
	var publicationURL, publicationError string
	if err := admin.QueryRow(ctx, `SELECT COALESCE(invocation_url, ''), COALESCE(publication_last_error, '') FROM inference_services WHERE id=$1`, publicationService.ID).Scan(&publicationURL, &publicationError); err != nil {
		t.Fatal(err)
	}
	if publicationURL != "" || publicationError != publicationFailureMessage {
		t.Fatalf("publication failure leaked endpoint or error: url=%q error=%q", publicationURL, publicationError)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET publication_desired='unpublished', publication_generation=3, publication_observed_generation=2,
    publication_phase='pending', publication_updated_at=NOW()
WHERE id=$1`, publicationService.ID); err != nil {
		t.Fatal(err)
	}
	unpublishTarget, ok, err := store.ClaimPublication(ctx, "publication-worker", publicationNow.Add(4*time.Second), time.Minute)
	if err != nil || !ok || unpublishTarget.Generation != 3 {
		t.Fatalf("claim unpublish = (%+v,%v,%v)", unpublishTarget, ok, err)
	}
	if err := store.CompletePublication(ctx, PublicationResult{
		TenantID: tenantID, ServiceID: publicationService.ID, Generation: 3,
		LeaseToken: unpublishTarget.LeaseToken, Phase: domain.PublicationUnpublishedOK,
		Now: publicationNow.Add(5 * time.Second),
	}); err != nil {
		t.Fatalf("complete unpublication: %v", err)
	}
	withdrawn, err := store.PublicationWithdrawn(ctx, tenantID, publicationService.ID, 3)
	if err != nil || !withdrawn {
		t.Fatalf("publication withdrawn = (%v,%v)", withdrawn, err)
	}

	routingScale, routingScaleCreate := integrationCreateFixture(tenantID, versionID, "routing-scale", "sha256:routing-scale")
	if _, err := store.CreateWithOperation(ctx, routingScale, routingScaleCreate); err != nil {
		t.Fatalf("create routing scale fixture: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_operations SET state='completed', completed_at=NOW() WHERE id=$1`, routingScaleCreate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET status='running', desired_state='running', applied_spec=desired_spec, observed_generation=generation,
    runtime_ref=$2, runtime_endpoint='http://runtime.test', invocation_url='https://gateway.test/v1',
    publication_desired='published', publication_generation=1,
    publication_observed_generation=1, publication_phase='published', publication_updated_at=NOW()
WHERE id=$1`, routingScale.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	scaleTarget := routingScale.DesiredSpec
	scaleTarget.Replicas = 2
	scaleResult, err := store.MutateService(ctx, MutationRequest{
		TenantID: tenantID, ServiceID: routingScale.ID, Action: domain.ActionScale,
		OperationID: uuid.New(), OperationScope: "inference_service.scale", IdempotencyKey: uuid.New(),
		RequestHash: "sha256:routing-scale-mutation", TargetSpec: scaleTarget, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("begin routing scale: %v", err)
	}
	resolvedDuringScale, err := store.ResolvePublishedService(ctx, tenantID, routingScale.ServedModelName)
	if err != nil || resolvedDuringScale.ID != routingScale.ID {
		t.Fatalf("published route disappeared during scale: (%+v,%v)", resolvedDuringScale, err)
	}
	rollbackGeneration := scaleResult.Operation.TargetGeneration + 1
	if _, err := admin.Exec(ctx, `UPDATE inference_operations
SET state='running', rollback_generation=$2 WHERE id=$1`, scaleResult.Operation.ID, rollbackGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services SET generation=$2 WHERE id=$1`, routingScale.ID, rollbackGeneration); err != nil {
		t.Fatal(err)
	}
	resolvedDuringRollback, err := store.ResolvePublishedService(ctx, tenantID, routingScale.ServedModelName)
	if err != nil || resolvedDuringRollback.ID != routingScale.ID {
		t.Fatalf("published route disappeared during scale rollback: (%+v,%v)", resolvedDuringRollback, err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_operations SET state='completed' WHERE id=$1`, scaleResult.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolvePublishedService(ctx, tenantID, routingScale.ServedModelName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan deploying scale route resolved without an active scale: %v", err)
	}

	routingRestart, routingRestartCreate := integrationCreateFixture(tenantID, versionID, "routing-restart", "sha256:routing-restart")
	if _, err := store.CreateWithOperation(ctx, routingRestart, routingRestartCreate); err != nil {
		t.Fatalf("create routing restart fixture: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_operations SET state='completed', completed_at=NOW() WHERE id=$1`, routingRestartCreate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET status='running', desired_state='running', applied_spec=desired_spec, observed_generation=generation,
    runtime_ref=$2, runtime_endpoint='http://runtime.test', invocation_url='https://gateway.test/v1',
    publication_desired='published', publication_generation=1,
    publication_observed_generation=1, publication_phase='published', publication_updated_at=NOW()
WHERE id=$1`, routingRestart.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	restartResult, err := store.MutateService(ctx, MutationRequest{
		TenantID: tenantID, ServiceID: routingRestart.ID, Action: domain.ActionRestart,
		OperationID: uuid.New(), OperationScope: "inference_service.restart", IdempotencyKey: uuid.New(),
		RequestHash: "sha256:routing-restart-mutation", Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("begin routing restart: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE inference_services
SET invocation_url='https://gateway.test/v1', publication_desired='published',
    publication_generation=generation, publication_observed_generation=generation,
    publication_phase='published', publication_updated_at=NOW()
WHERE id=$1`, routingRestart.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolvePublishedService(ctx, tenantID, routingRestart.ServedModelName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deploying restart incorrectly resolved as stable route: operation=%s err=%v", restartResult.Operation.ID, err)
	}

	retryService, retryOperation := integrationCreateFixture(tenantID, versionID, "withdrawal-retry", "sha256:withdrawal-retry")
	if _, err := store.CreateWithOperation(ctx, retryService, retryOperation); err != nil {
		t.Fatalf("create withdrawal retry fixture: %v", err)
	}
	retryLease := uuid.New()
	if _, err := admin.Exec(ctx, `UPDATE inference_operations
SET state='running', lease_owner='retry-test', lease_until=NOW()+INTERVAL '5 minutes', lease_token=$2
WHERE id=$1`, retryOperation.ID, retryLease); err != nil {
		t.Fatal(err)
	}
	retryAt := time.Now().UTC().Add(time.Minute)
	if err := store.FailOperation(ctx, Failure{
		TenantID: tenantID, ServiceID: retryService.ID, OperationID: retryOperation.ID,
		TargetGeneration: retryOperation.TargetGeneration, ErrorCode: "GATEWAY_UNPUBLISH_PENDING",
		ErrorMessage: "gateway withdrawal pending", RetryAt: &retryAt, LeaseToken: retryLease,
	}); err != nil {
		t.Fatalf("persist gateway withdrawal retry: %v", err)
	}
	withdrawalRetry, err := store.GetOperation(ctx, tenantID, retryOperation.ID)
	if err != nil || withdrawalRetry.Attempt != 0 || withdrawalRetry.State != domain.OperationPending {
		t.Fatalf("gateway withdrawal consumed runtime attempt: (%+v,%v)", withdrawalRetry, err)
	}
	runtimeRetryLease := uuid.New()
	if _, err := admin.Exec(ctx, `UPDATE inference_operations
SET state='running', lease_owner='retry-test', lease_until=NOW()+INTERVAL '5 minutes', lease_token=$2
WHERE id=$1`, retryOperation.ID, runtimeRetryLease); err != nil {
		t.Fatal(err)
	}
	if err := store.FailOperation(ctx, Failure{
		TenantID: tenantID, ServiceID: retryService.ID, OperationID: retryOperation.ID,
		TargetGeneration: retryOperation.TargetGeneration, ErrorCode: "RUNTIME_STOP_PENDING",
		ErrorMessage: "runtime stop pending", RetryAt: &retryAt, LeaseToken: runtimeRetryLease,
	}); err != nil {
		t.Fatalf("persist runtime retry: %v", err)
	}
	runtimeRetry, err := store.GetOperation(ctx, tenantID, retryOperation.ID)
	if err != nil || runtimeRetry.Attempt != 1 || runtimeRetry.State != domain.OperationPending {
		t.Fatalf("runtime retry did not consume its own attempt: (%+v,%v)", runtimeRetry, err)
	}

	if _, err := admin.Exec(ctx, `UPDATE inference_operations SET state='completed' WHERE state IN ('pending','running')`); err != nil {
		t.Fatal(err)
	}
	raceService, raceCreate := integrationCreateFixture(tenantID, versionID, "claim-preempt-race", "sha256:claim-preempt-race")
	if _, err := store.CreateWithOperation(ctx, raceService, raceCreate); err != nil {
		t.Fatalf("create claim/preempt race fixture: %v", err)
	}
	if err := store.BindRuntimeRef(ctx, RuntimeBinding{
		TenantID: tenantID, ServiceID: raceService.ID, OperationID: raceCreate.ID,
		Generation: raceCreate.TargetGeneration, RuntimeRef: uuid.New(),
	}); err != nil {
		t.Fatalf("bind claim/preempt race runtime: %v", err)
	}
	raceStopRequest := MutationRequest{
		TenantID: tenantID, ServiceID: raceService.ID, Action: domain.ActionStop,
		OperationID: uuid.New(), OperationScope: "inference_service.stop", IdempotencyKey: uuid.New(),
		RequestHash: "sha256:claim-preempt-race-stop", Now: time.Now().UTC(),
	}
	type claimRaceResult struct {
		operation domain.Operation
		claimed   bool
		err       error
	}
	type mutationRaceResult struct {
		result MutationResult
		err    error
	}
	startRace := make(chan struct{})
	claimRaceCh := make(chan claimRaceResult, 1)
	mutationRaceCh := make(chan mutationRaceResult, 1)
	go func() {
		<-startRace
		operation, claimed, err := store.ClaimOperation(ctx, "claim-preempt-race", time.Now().UTC(), time.Minute)
		claimRaceCh <- claimRaceResult{operation: operation, claimed: claimed, err: err}
	}()
	go func() {
		<-startRace
		result, err := store.MutateService(ctx, raceStopRequest)
		mutationRaceCh <- mutationRaceResult{result: result, err: err}
	}()
	close(startRace)
	claimRace := <-claimRaceCh
	mutationRace := <-mutationRaceCh
	if claimRace.err != nil {
		t.Fatalf("claim/preempt race claim = (%+v,%v,%v)", claimRace.operation, claimRace.claimed, claimRace.err)
	}
	raceAfter, err := store.GetService(ctx, tenantID, raceService.ID)
	if err != nil {
		t.Fatal(err)
	}
	raceCreateAfter, err := store.GetOperation(ctx, tenantID, raceCreate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mutationRace.err == nil {
		if raceCreateAfter.State != domain.OperationCancelled || raceAfter.Generation != 2 ||
			raceAfter.CurrentOperationID != mutationRace.result.Operation.ID ||
			(claimRace.claimed && claimRace.operation.ID == raceCreate.ID) {
			t.Fatalf("mutation won race without safely cancelling old pending op: claim=%+v mutation=%+v service=%+v old=%+v",
				claimRace.operation, mutationRace.result, raceAfter, raceCreateAfter)
		}
	} else {
		if !errors.Is(mutationRace.err, domain.ErrOperationInProgress) ||
			raceCreateAfter.State != domain.OperationRunning || raceAfter.Generation != 1 ||
			raceAfter.CurrentOperationID != raceCreate.ID || !claimRace.claimed || claimRace.operation.ID != raceCreate.ID {
			t.Fatalf("claim won race without rolling back new mutation: claim=%+v mutation_err=%v service=%+v old=%+v",
				claimRace.operation, mutationRace.err, raceAfter, raceCreateAfter)
		}
	}
}

func containsService(services []domain.Service, serviceID uuid.UUID) bool {
	for _, service := range services {
		if service.ID == serviceID {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func openRolePool(t *testing.T, rawDSN, username, password string) *pgxpool.Pool {
	t.Helper()
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(username, password)
	pool, err := pgxpool.New(context.Background(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func integrationCreateFixture(tenantID, versionID uuid.UUID, name, hash string) (domain.Service, domain.Operation) {
	now := time.Now().UTC()
	serviceID := uuid.New()
	operationID := uuid.New()
	spec := domain.Spec{Replicas: 1, CPU: "1", Memory: "1Gi", PlacementMode: "auto"}
	service := domain.Service{
		ID: serviceID, TenantID: tenantID, Name: name, ModelVersionID: versionID,
		ServedModelName: name, ModelSnapshot: []byte(`{"display_name":"test"}`),
		Status: domain.StatusPending, DesiredState: domain.DesiredStateRunning,
		Generation: 1, DesiredSpec: spec, CurrentOperationID: operationID,
		ActiveOperationID: operationID, ActiveOperation: domain.ActionCreate,
		CreatedAt: now, UpdatedAt: now,
	}
	operation := domain.Operation{
		ID: operationID, TenantID: tenantID, ServiceID: serviceID,
		Type: domain.ActionCreate, State: domain.OperationPending, TargetGeneration: 1,
		TargetSpec: spec, OperationScope: "inference_service.create",
		IdempotencyKey: uuid.New(), RequestHash: hash, NextAttemptAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	return service, operation
}
