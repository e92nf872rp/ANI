# Inference List Filters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Make status and offset in existing Console requests work before pagination.
**Architecture:** Services OpenAPI → Gateway → inference.control.v1 → Controller → PostgreSQL. Keep existing unfiltered internal list for overview; add a paged query path without resurrecting deprecated inference.v1.
**Tech Stack:** Go, Protobuf, PostgreSQL, existing test tooling.

## Global Constraints
- User approved implementation on 2026-09-28; main only, preserve existing changes, no commit/push/deploy.
- Status enum: pending, deploying, running, stopping, stopped, failed; omitted/empty means all.
- limit default 50, range 1..200; offset default 0, nonnegative int32. cursor retains numeric next_cursor behavior. Nonempty cursor and explicit offset together return 400.
- SQL filters tenant and deleted_at as well as status before ORDER BY created_at DESC,id DESC and LIMIT/OFFSET.

### Task 1: Contract and regression
**Files:** `repo/api/openapi/services/v1.yaml`, `repo/services/ani-gateway/internal/router/inference_resources_test.go`.
- [ ] Add query contract and 400 response. Gateway regression request: `GET /api/v1/svc/inference-services?limit=1&offset=1&status=running`; fixture interleaves pending/running records and asserts only second running item.
- [ ] Reject unknown status, negative/noninteger offset, limit outside bounds, and conflicting cursor/offset with 400 before downstream call.
- [ ] Run `cd repo && go test ./services/ani-gateway/internal/router -run 'TestInference.*List' -count=1`; observe failure before implementation.

### Task 2: Full query path
**Files:** `repo/api/proto/inference/control/v1/inference_control.proto`, generated control protobuf, `repo/services/ani-gateway/internal/router/inference_grpc_client.go`, `inference_resources.go`, `repo/services/inference-service/internal/{grpcapi,service,repository}/*.go`.
**Interfaces:** Optional control list query carries status, limit, offset; response supplies next_cursor. Existing tenant-only internal list remains available to overview callers.
- [ ] Add protobuf fields with fresh numbers and regenerate with existing tooling.
- [ ] Carry validated query through controller to parameterized PostgreSQL query:
```sql
WHERE service.tenant_id = $1 AND service.deleted_at IS NULL
  AND ($2 = '' OR service.status = $2)
ORDER BY service.created_at DESC, service.id DESC
LIMIT $3 OFFSET $4
```
- [ ] Read limit+1 to compute next_cursor without fetching every service; preserve empty `items: []`.
- [ ] Test filtered pagination, no matches, unchanged unfiltered overview, tenant isolation, invalid internal query.
- [ ] Run gateway and inference-service module tests.

### Task 3: Generate and verify
**Files:** Services SDK/docs, development record, CURRENT-SPRINT, ANI-06, record index.
- [ ] Regenerate with existing make targets; run `make validate-services`, `make validate-architecture`, `make test`, `git diff --check`.
- [ ] Record local results separately from undeployed cluster behavior.
