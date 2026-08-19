package repository

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func compactSQL(sql string) string {
	return strings.Join(strings.Fields(strings.ToLower(sql)), " ")
}

func TestCreateSQLFreezesTenantAndAtomicOperationContract(t *testing.T) {
	tenantSQL := compactSQL(setTenantSQL)
	if !strings.Contains(tenantSQL, "set_config('app.current_tenant_id', $1, true)") {
		t.Fatalf("tenant transaction must use transaction-local RLS context: %s", tenantSQL)
	}

	serviceSQL := compactSQL(insertServiceSQL)
	operationSQL := compactSQL(insertOperationSQL)
	for _, sql := range []string{serviceSQL, operationSQL} {
		if !strings.Contains(sql, "tenant_id") {
			t.Fatalf("create statement lacks tenant_id: %s", sql)
		}
	}
	if !strings.Contains(operationSQL, "operation_scope") || !strings.Contains(operationSQL, "request_hash") {
		t.Fatalf("operation insert does not persist idempotency identity: %s", operationSQL)
	}
}

func TestReplayClassification(t *testing.T) {
	if err := classifyReplay("sha256:same", "sha256:same"); err != nil {
		t.Fatalf("same hash must replay: %v", err)
	}
	if err := classifyReplay("sha256:old", "sha256:new"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different hash must conflict, got %v", err)
	}
}

func TestClaimSQLUsesSkipLockedAndExpiredLeaseTakeover(t *testing.T) {
	sql := compactSQL(claimOperationSQL)
	for _, required := range []string{
		"for update skip locked",
		"next_attempt_at <= $2",
		"lease_until is null or lease_until <= $2",
		"lease_owner = $1",
		"lease_until = $3",
		"lease_token = $4",
		"returning",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("claim SQL missing %q: %s", required, sql)
		}
	}

	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	if !leaseAvailable(nil, now) {
		t.Fatal("an unleased operation must be claimable")
	}
	expired := now.Add(-time.Second)
	if !leaseAvailable(&expired, now) {
		t.Fatal("an expired lease must be claimable")
	}
	active := now.Add(time.Second)
	if leaseAvailable(&active, now) {
		t.Fatal("an active lease must not be claimable")
	}
}

func TestObservationSQLUsesTenantAndGenerationCAS(t *testing.T) {
	sql := compactSQL(applyObservationSQL)
	for _, required := range []string{
		"where tenant_id = $1",
		"id = $2",
		"generation = $3",
		"current_operation_id = $4",
		"applied_spec = case when $11 then $6 else applied_spec end",
		"observed_generation = case when $11 then $3 else observed_generation end",
		"deleted_at = case when $13 then $10 else deleted_at end",
		"operation.lease_token = $12",
		"operation.lease_until > $10",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("observation SQL missing %q: %s", required, sql)
		}
	}
}

func TestWorkerFailureSQLCannotCrossTenantOrGeneration(t *testing.T) {
	sql := compactSQL(failOperationSQL)
	for _, required := range []string{
		"tenant_id = $1",
		"service_id = $2",
		"target_generation = $3",
		"id = $4",
		"lease_token = $10",
		"lease_until > $9",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("failure SQL missing %q: %s", required, sql)
		}
	}
	serviceSQL := compactSQL(failServiceSQL)
	for _, required := range []string{
		"tenant_id = $1",
		"id = $2",
		"generation = $3",
		"current_operation_id = $4",
		"status = 'failed'",
		"runtime_endpoint = null",
		"ready_replicas = 0",
	} {
		if !strings.Contains(serviceSQL, required) {
			t.Fatalf("service failure SQL missing %q: %s", required, serviceSQL)
		}
	}
}

func TestScaleRollbackSQLRestoresAppliedSpecAndFencesLease(t *testing.T) {
	beginService := compactSQL(beginScaleRollbackServiceSQL)
	for _, required := range []string{
		"desired_spec = applied_spec",
		"generation = generation + 1",
		"status = 'deploying'",
		"desired_state <> 'deleted'",
		"returning generation",
	} {
		if !strings.Contains(beginService, required) {
			t.Fatalf("begin rollback service SQL missing %q: %s", required, beginService)
		}
	}
	beginOperation := compactSQL(beginScaleRollbackOperationSQL)
	for _, required := range []string{
		"rollback_generation = $4",
		"lease_token = $7",
		"state = 'running'",
	} {
		if !strings.Contains(beginOperation, required) {
			t.Fatalf("begin rollback operation SQL missing %q: %s", required, beginOperation)
		}
	}
	finishService := compactSQL(finishScaleRollbackServiceSQL)
	for _, required := range []string{
		"generation = $3",
		"current_operation_id = $4",
		"applied_spec = case when $6 then $7 else applied_spec end",
	} {
		if !strings.Contains(finishService, required) {
			t.Fatalf("finish rollback service SQL missing %q: %s", required, finishService)
		}
	}
	finishOperation := compactSQL(finishScaleRollbackOperationSQL)
	for _, required := range []string{
		"state = 'failed'",
		"rollback_generation = $4",
		"lease_token = $8",
	} {
		if !strings.Contains(finishOperation, required) {
			t.Fatalf("finish rollback operation SQL missing %q: %s", required, finishOperation)
		}
	}
}

func TestMutationSQLLocksTenantServiceBeforeTransition(t *testing.T) {
	sql := compactSQL(getServiceForMutationSQL)
	for _, required := range []string{
		"where service.tenant_id = $1",
		"service.id = $2",
		"deleted_at is null",
		"for update",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("mutation lock SQL missing %q: %s", required, sql)
		}
	}
}

func TestMutationSQLCancelsPreemptedOperationBeforeInsert(t *testing.T) {
	sql := compactSQL(cancelOperationSQL)
	for _, required := range []string{
		"state = 'cancelled'",
		"tenant_id = $1",
		"service_id = $2",
		"id = $3",
		"state in ('pending', 'running')",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("cancel SQL missing %q: %s", required, sql)
		}
	}
}

func TestMutationSQLPersistsDesiredStateAndGenerationAtomically(t *testing.T) {
	sql := compactSQL(updateServiceTransitionSQL)
	for _, required := range []string{
		"desired_spec = $3",
		"desired_state = $4",
		"generation = $5",
		"current_operation_id = $6",
		"where tenant_id = $1 and id = $2",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("transition update SQL missing %q: %s", required, sql)
		}
	}
}

func TestListSQLExcludesTombstonesAndInternalEndpointProjection(t *testing.T) {
	sql := compactSQL(listServicesSQL)
	if !strings.Contains(sql, "where service.tenant_id = $1 and service.deleted_at is null") {
		t.Fatalf("list SQL must exclude tombstones: %s", sql)
	}
	if strings.Contains(sql, "runtime_endpoint") || strings.Contains(sql, "runtime_ref") {
		t.Fatalf("list projection exposes internal runtime data: %s", sql)
	}
}

func TestBindRuntimeSQLDoesNotRequireWorkerLease(t *testing.T) {
	sql := compactSQL(bindRuntimeRefSQL)
	for _, required := range []string{
		"runtime_ref = $5",
		"status = 'deploying'",
		"tenant_id = $1",
		"id = $2",
		"generation = $3",
		"current_operation_id = $4",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("bind runtime SQL missing %q: %s", required, sql)
		}
	}
	if strings.Contains(sql, "lease_token") {
		t.Fatal("request-path bind must not require a worker lease")
	}
}

func TestAbortCreateSQLRemovesUnsubmittedPendingCreate(t *testing.T) {
	operationSQL := compactSQL(abortCreateOperationSQL)
	if !strings.Contains(operationSQL, "type = 'create'") || !strings.Contains(operationSQL, "state = 'pending'") {
		t.Fatalf("abort create must only delete pending create operations: %s", operationSQL)
	}
	serviceSQL := compactSQL(abortCreateServiceSQL)
	if !strings.Contains(serviceSQL, "runtime_ref is null") {
		t.Fatalf("abort create must not delete a dispatched runtime: %s", serviceSQL)
	}
}

func TestAbortPendingMutationSQLRestoresPreviousGeneration(t *testing.T) {
	sql := compactSQL(abortPendingMutationServiceSQL)
	for _, required := range []string{
		"desired_spec = $5",
		"generation = $7",
		"current_operation_id = null",
		"generation = $3",
		"current_operation_id = $4",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("abort mutation SQL missing %q: %s", required, sql)
		}
	}
}
