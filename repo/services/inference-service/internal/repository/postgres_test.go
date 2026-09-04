package repository

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

func compactSQL(sql string) string {
	return strings.Join(strings.Fields(strings.ToLower(sql)), " ")
}

type accessPolicyRow struct{ values []any }

func (r accessPolicyRow) Scan(dest ...any) error {
	if len(dest) != len(r.values) {
		return errors.New("access policy scan destination count does not match query")
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(r.values[i]))
	}
	return nil
}

func hasString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestAccessPolicySelectAndScanKeepScopeAllowAndDenyKeysSeparate(t *testing.T) {
	tenantID, serviceID := uuid.New(), uuid.New()
	scopeKey, allowKey, denyKey := uuid.New(), uuid.New(), uuid.New()
	sql := compactSQL(accessPolicySelectSQL)
	for _, required := range []string{
		"k.effect = 'scope'", "k.effect = 'allow'", "k.effect = 'deny'",
		"array_agg(distinct k.api_key_id::text) filter",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("access policy select missing %q: %s", required, sql)
		}
	}
	createdAt := time.Date(2026, 9, 1, 1, 2, 3, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	policy, err := scanAccessPolicy(accessPolicyRow{values: []any{
		uuid.New(), tenantID, "policy", domain.AccessPolicyEnabled, "", 1,
		string(domain.ScopeInferenceServiceAPIKey), true, 1, 2, 3, 60,
		[]uuid.UUID{serviceID}, []string{scopeKey.String()}, []string{allowKey.String()}, []string{denyKey.String()},
		createdAt, updatedAt,
	}})
	if err != nil {
		t.Fatalf("scan access policy: %v", err)
	}
	if !hasString(policy.Scope.APIKeyIDs, scopeKey.String()) || hasString(policy.Scope.APIKeyIDs, allowKey.String()) {
		t.Fatalf("scope keys=%v", policy.Scope.APIKeyIDs)
	}
	if !hasString(policy.Access.AllowAPIKeyIDs, allowKey.String()) || hasString(policy.Access.AllowAPIKeyIDs, scopeKey.String()) {
		t.Fatalf("allow keys=%v", policy.Access.AllowAPIKeyIDs)
	}
	if !hasString(policy.Access.DenyAPIKeyIDs, denyKey.String()) {
		t.Fatalf("deny keys=%v", policy.Access.DenyAPIKeyIDs)
	}
	if !policy.CreatedAt.Equal(createdAt) || !policy.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("policy timestamps = (%v,%v)", policy.CreatedAt, policy.UpdatedAt)
	}
}

func TestAccessPolicyMutationIdempotencyContract(t *testing.T) {
	for _, sql := range []string{findAccessPolicyMutationSQL, insertAccessPolicyMutationSQL} {
		compacted := compactSQL(sql)
		for _, required := range []string{"tenant_id", "operation_scope", "idempotency_key", "request_hash", "result_snapshot"} {
			if !strings.Contains(compacted, required) {
				t.Fatalf("policy mutation SQL missing %q: %s", required, compacted)
			}
		}
	}
	policy := domain.AccessPolicy{
		TenantID: uuid.New(), Name: "stable", Status: domain.AccessPolicyEnabled, Priority: 1000,
		Scope:       domain.AccessPolicyScope{Type: domain.ScopeTenantDefault},
		Access:      domain.AccessPolicyAccess{AllowAllTenantKeys: true},
		Concurrency: domain.AccessPolicyConcurrency{LeaseTTLSeconds: 60},
	}
	first, err := hashAccessPolicyMutation("create", policy)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashAccessPolicyMutation("create", policy)
	if err != nil || first != second {
		t.Fatalf("stable hash = (%q,%q,%v)", first, second, err)
	}
	changed := policy
	changed.Priority++
	different, err := hashAccessPolicyMutation("create", changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := classifyReplay(first, different); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different mutation must conflict, got %v", err)
	}
}

func TestAccessPolicyPatchRequiresCanonicalGatewayRequestHash(t *testing.T) {
	valid := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if !isCanonicalSHA256(valid) {
		t.Fatalf("valid hash rejected: %q", valid)
	}
	for _, value := range []string{"", "sha256:short", "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "md5:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if isCanonicalSHA256(value) {
			t.Fatalf("invalid hash accepted: %q", value)
		}
	}
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
		"where service.tenant_id = $1",
		"service.id = $2",
		"service.generation = $3",
		"service.current_operation_id = $4",
		"applied_spec = case when $11 then $6 else applied_spec end",
		"observed_generation = case when $11 then $3 else observed_generation end",
		"deleted_at = case when $13 then $10 else deleted_at end",
		"operation.type in ('create', 'start', 'restart')",
		"service.desired_state = 'running'",
		"operation.lease_token = $12",
		"operation.lease_until > $10",
		"operation.service_id = $2",
		"operation.target_generation = $3",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("observation SQL missing %q: %s", required, sql)
		}
	}
}

func TestObservationSQLRejectsMaliciousPublishForScaleStopAndDelete(t *testing.T) {
	sql := compactSQL(applyObservationSQL)
	predicate := "$11 and $14 and $5 = 'running' and not $13 and service.desired_state = 'running' and operation.type in ('create', 'start', 'restart')"
	if count := strings.Count(sql, predicate); count != 6 {
		t.Fatalf("publication predicate count = %d, want 6: %s", count, sql)
	}
	for _, forbidden := range []string{"operation.type in ('scale'", "operation.type in ('stop'", "operation.type in ('delete'"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("publication predicate permits forbidden operation %q: %s", forbidden, sql)
		}
	}
}

func TestWorkerFailureSQLCannotCrossTenantOrGeneration(t *testing.T) {
	sql := compactSQL(failOperationSQL)
	for _, required := range []string{
		"attempt = attempt + case when $7 in ('gateway_unpublish_pending', 'gateway_unpublish_check_failed') then 0 else 1 end",
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

func TestCompleteOperationSQLClearsRetryResidue(t *testing.T) {
	sql := compactSQL(completeOperationSQL)
	for _, required := range []string{
		"error_code = null",
		"error_message = null",
		"next_attempt_at = $5",
		"completed_at = $5",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("complete operation SQL missing %q: %s", required, sql)
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
		"state = 'pending'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("cancel SQL missing %q: %s", required, sql)
		}
	}
	if strings.Contains(sql, "state in ('pending', 'running')") || strings.Contains(sql, "state = 'running'") {
		t.Fatalf("claimed/running operation must win preemption CAS: %s", sql)
	}
}

func TestPreemptionFenceRejectsUnboundRequestPathCreate(t *testing.T) {
	createID := uuid.New()
	service := domain.Service{ActiveOperationID: createID, ActiveOperation: domain.ActionCreate}
	operation := domain.Operation{PreemptedOperationID: createID}

	if err := validatePreemptionFence(service, operation); !errors.Is(err, domain.ErrOperationInProgress) {
		t.Fatalf("unbound create preemption error = %v, want ErrOperationInProgress", err)
	}
	service.RuntimeRef = uuid.New()
	if err := validatePreemptionFence(service, operation); err != nil {
		t.Fatalf("bound create preemption error = %v", err)
	}
}

func TestMutationSQLPersistsDesiredStateAndGenerationAtomically(t *testing.T) {
	sql := compactSQL(updateServiceTransitionSQL)
	for _, required := range []string{
		"desired_spec = $3",
		"desired_state = $4",
		"generation = $5",
		"current_operation_id = $6",
		"publication_desired = case when $9 then 'unpublished' else publication_desired end",
		"publication_generation = case when $9 then $5 else publication_generation end",
		"publication_phase = case when $9 then 'pending' else publication_phase end",
		"publication_last_error = case when $9 then null else publication_last_error end",
		"publication_updated_at = case when $9 then $8 else publication_updated_at end",
		"invocation_url = case when $9 then null else invocation_url end",
		"where tenant_id = $1 and id = $2",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("transition update SQL missing %q: %s", required, sql)
		}
	}
}

func TestOnlyStopRestartDeleteRequestPublicationWithdrawal(t *testing.T) {
	for _, action := range []domain.Action{domain.ActionStop, domain.ActionRestart, domain.ActionDelete} {
		if !requiresPublicationWithdrawal(action) {
			t.Fatalf("%s did not request withdrawal", action)
		}
	}
	for _, action := range []domain.Action{domain.ActionCreate, domain.ActionStart, domain.ActionScale} {
		if requiresPublicationWithdrawal(action) {
			t.Fatalf("%s unexpectedly requested withdrawal", action)
		}
	}
}

func TestPublicationWithdrawalProjectionMatchesAtomicMutation(t *testing.T) {
	now := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC)
	service := domain.Service{
		Generation: 8, InvocationURL: "https://ai.example.test/v1/chat/completions",
		Publication: domain.Publication{
			Desired: domain.PublicationPublished, Generation: 7, ObservedGeneration: 7,
			Phase: domain.PublicationPublishedOK, LastError: "old", UpdatedAt: now.Add(-time.Minute),
		},
	}
	got := withPublicationWithdrawal(service, now)
	if got.Publication.Desired != domain.PublicationUnpublished || got.Publication.Generation != 8 ||
		got.Publication.ObservedGeneration != 7 || got.Publication.Phase != domain.PublicationPending ||
		got.Publication.LastError != "" || got.Publication.UpdatedAt != now || got.InvocationURL != "" {
		t.Fatalf("withdrawal projection = %+v", got)
	}
}

func TestMutationAndObservationSQLArgumentOrder(t *testing.T) {
	tenantID, serviceID, operationID, leaseToken := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	service := domain.Service{
		ID: serviceID, TenantID: tenantID, Generation: 7,
		DesiredState: domain.DesiredStateStopped, Status: domain.StatusStopping,
	}
	mutation, withdraw := mutationTransitionArgs(MutationRequest{
		TenantID: tenantID, ServiceID: serviceID, Action: domain.ActionStop,
	}, service, operationID, []byte(`{"replicas":1}`), now)
	if len(mutation) != 9 || mutation[0] != tenantID || mutation[1] != serviceID || mutation[4] != int64(7) ||
		mutation[5] != operationID || mutation[7] != now || mutation[8] != true || !withdraw {
		t.Fatalf("mutation SQL args = %#v", mutation)
	}

	observation := observationArgs(Observation{
		TenantID: tenantID, ServiceID: serviceID, OperationID: operationID,
		TargetGeneration: 7, Status: domain.StatusRunning, Complete: true,
		Deleted: false, Publish: true, LeaseToken: leaseToken,
	}, []byte(`{"replicas":1}`), now)
	if len(observation) != 14 || observation[0] != tenantID || observation[1] != serviceID ||
		observation[2] != int64(7) || observation[3] != operationID || observation[9] != now ||
		observation[10] != true || observation[11] != leaseToken || observation[12] != false || observation[13] != true {
		t.Fatalf("observation SQL args = %#v", observation)
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

func TestClaimPublicationSQLUsesSkipLockedExpiredLeaseAndDatabaseToken(t *testing.T) {
	sql := compactSQL(claimPublicationSQL)
	for _, required := range []string{
		"for update skip locked",
		"service.publication_lease_until is null or service.publication_lease_until <= $2",
		"publication_lease_token = gen_random_uuid()",
		"publication_lease_until = $3",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("missing %q: %s", required, sql)
		}
	}
}

func TestCompletePublicationSQLUsesGenerationAndLeaseFence(t *testing.T) {
	sql := compactSQL(completePublicationSQL)
	for _, required := range []string{
		"tenant_id = $1", "id = $2", "publication_generation = $3",
		"publication_lease_token = $4", "publication_lease_until > $5",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("missing %q: %s", required, sql)
		}
	}
}

func TestRenewPublicationSQLExtendsOnlyTheCurrentUnexpiredLease(t *testing.T) {
	sql := compactSQL(renewPublicationSQL)
	for _, required := range []string{
		"tenant_id = $1", "id = $2", "publication_generation = $3",
		"publication_lease_token = $4", "publication_lease_until > $6",
		"publication_lease_until = $5",
		"publication_phase in ('publishing', 'unpublishing')",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("missing %q: %s", required, sql)
		}
	}
}

func TestPublicationFailureAndWithdrawalSQLRemainTenantScoped(t *testing.T) {
	for name, sql := range map[string]string{
		"failure":    compactSQL(failPublicationSQL),
		"withdrawal": compactSQL(publicationWithdrawnSQL),
	} {
		for _, required := range []string{"tenant_id = $1", "id = $2", "publication_generation = $3"} {
			if !strings.Contains(sql, required) {
				t.Fatalf("%s missing %q: %s", name, required, sql)
			}
		}
	}
}

func TestResolvePublishedServiceSQLIsTenantScopedAndNonLeaking(t *testing.T) {
	sql := compactSQL(resolvePublishedServiceSQL)
	for _, required := range []string{
		"service.tenant_id = $1", "service.served_model_name = $2",
		"service.publication_desired = 'published'", "service.publication_phase = 'published'",
		"service.publication_generation = service.publication_observed_generation",
		"service.invocation_url is not null", "service.desired_state = 'running'",
		"service.status = 'running' or (service.status = 'deploying'",
		"operation.type = 'scale'", "operation.state in ('pending', 'running')",
		"operation.tenant_id = service.tenant_id", "operation.service_id = service.id",
		"operation.id = service.current_operation_id",
		"service.generation = operation.target_generation",
		"service.generation = operation.rollback_generation", "limit 1",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("missing %q: %s", required, sql)
		}
	}
}

func TestRedactPublicationErrorFailsClosedForSensitiveUpstreamText(t *testing.T) {
	for _, raw := range []string{
		"token=secret-value",
		`{"token":"secret-value"}`,
		"Authorization Bearer secret-value",
		"upstream failed at https://gateway.example/v1?api_key=secret-value",
	} {
		if got := redactPublicationError(raw); got != "GATEWAY_PUBLISH_FAILED" {
			t.Fatalf("redactPublicationError(%q) = %q, want fixed failure code", raw, got)
		}
	}
}
