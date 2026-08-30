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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

func TestPostgresControlPlaneIntegration(t *testing.T) {
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
GRANT SELECT, INSERT, UPDATE, DELETE ON inference_services, inference_operations TO %s, %s;
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
	preemptedCreate, err := store.GetOperation(ctx, tenantID, lifecycleCreate.ID)
	if err != nil || preemptedCreate.State != domain.OperationCancelled {
		t.Fatalf("preempted create = (%+v,%v)", preemptedCreate, err)
	}
	stopReplay, err := store.MutateService(ctx, stopRequest)
	if err != nil || stopReplay.Operation.ID != stoppedIntent.Operation.ID || !stopReplay.Operation.Replayed {
		t.Fatalf("stop replay = (%+v,%v)", stopReplay, err)
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
		stopNoop.Service.Generation != 2 {
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
		TargetGeneration: claimedA.TargetGeneration, Status: domain.StatusDeploying,
		AppliedSpec: claimedA.TargetSpec, RuntimeRef: uuid.New(), ReadyReplicas: 0,
		LeaseToken: claimedA.LeaseToken,
	}
	if err := store.ApplyObservation(ctx, stale); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("expired lease write error = %v", err)
	}
	wrongGeneration := stale
	wrongGeneration.TargetGeneration++
	wrongGeneration.LeaseToken = claimedB.LeaseToken
	if err := store.ApplyObservation(ctx, wrongGeneration); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("wrong generation write error = %v", err)
	}
	wrongOperation := stale
	wrongOperation.OperationID = uuid.New()
	wrongOperation.LeaseToken = claimedB.LeaseToken
	if err := store.ApplyObservation(ctx, wrongOperation); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("wrong current operation write error = %v", err)
	}
	current := stale
	current.LeaseToken = claimedB.LeaseToken
	current.Status = domain.StatusRunning
	current.ReadyReplicas = current.AppliedSpec.Replicas
	current.Complete = true
	if err := store.ApplyObservation(ctx, current); err != nil {
		t.Fatalf("current lease completion: %v", err)
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
}

func containsService(services []domain.Service, serviceID uuid.UUID) bool {
	for _, service := range services {
		if service.ID == serviceID {
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
