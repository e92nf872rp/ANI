"""Tests for UpdateKB servicer + update_kb repository (issue-043, SPEC §5.1).

Verifies:
- Success path: name+description both updated, async_tasks idempotency record
  written (task_type=kb.update), result row returned via _kb_row_to_pb.
- NOT_FOUND when update_kb returns None (kb missing or RLS-hidden).
- Empty name/description keep current values (COALESCE+NULLIF semantics) —
  verified at the SQL-argument level: empty strings are passed through so the
  DB-side COALESCE keeps the stored value.
- asyncpg.UniqueViolationError (SQLSTATE 23505) → ALREADY_EXISTS.
- Idempotent replay: an existing async_tasks row with a result short-circuits
  the update (no second UPDATE, no second task insert).
- Cross-tenant isolation: RLS makes update_kb return None for a kb belonging
  to another tenant → NOT_FOUND (mocked at the repository boundary, consistent
  with the servicer's contract).

These tests use a mock asyncpg conn/pool (no real DB); SQL semantics are
covered by the assertions on the SQL text and bound parameters.
"""
import asyncio
import os
import sys
import uuid
from contextlib import asynccontextmanager
from datetime import datetime, timezone

import asyncpg
import grpc
import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.api.grpc_server import KBServiceServicer
from app.generated.kb.v1 import kb_service_pb2 as kb_pb
from app.generated.kb.v1 import kb_service_pb2_grpc as kb_grpc

TENANT_ID = "11111111-1111-1111-1111-111111111111"
OTHER_TENANT_ID = "99999999-9999-9999-9999-999999999999"
KB_ID = "22222222-2222-2222-2222-222222222222"


def _kb_row(name="kb-old", description="old desc", **overrides):
    row = {
        "id": uuid.UUID(KB_ID),
        "tenant_id": uuid.UUID(TENANT_ID),
        "name": name,
        "description": description,
        "embedding_model": "bge-m3",
        "chunk_size": 1024,
        "top_k": 5,
        "score_threshold": 0.3,
        "retrieval_mode": "hybrid",
        "status": "active",
        "doc_count": 0,
        "created_at": datetime(2026, 9, 1, tzinfo=timezone.utc),
        "updated_at": datetime(2026, 9, 2, tzinfo=timezone.utc),
        "vector_store_id": None,
    }
    row.update(overrides)
    return row


class _FakeContext:
    """Stand-in for grpc.ServicerContext: abort() records and raises."""

    def __init__(self):
        self.aborted = None

    def abort(self, code, message):
        self.aborted = (code, message)
        raise RuntimeError(f"aborted: {code} {message}")


class _UpdateKBMockConn:
    """Mock asyncpg conn recording SQL calls for the UpdateKB flow.

    `update_result` is the row returned for the UPDATE knowledge_bases
    statement (None → NOT_FOUND path, an exception instance → raised).
    `existing_task` is the row returned by find_by_idempotency_key.
    `insert_raise_exc` makes the INSERT INTO async_tasks raise (poison-key
    path: UNIQUE(tenant_id, idempotency_key) violation); the retry find
    then returns `poison_task` instead of `existing_task`.
    """

    def __init__(
        self,
        *,
        update_result="default",
        existing_task=None,
        raise_exc=None,
        insert_raise_exc=None,
        poison_task=None,
    ):
        self.update_result = update_result
        self.existing_task = existing_task
        self.raise_exc = raise_exc
        self.insert_raise_exc = insert_raise_exc
        self.poison_task = poison_task
        self._find_calls = 0
        self.events: list[tuple] = []

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            self.events.append(("BEGIN",))
            try:
                yield self
            finally:
                self.events.append(("COMMIT",))
        return _tx()

    async def execute(self, sql, *args):
        # complete_task UPDATE
        if "UPDATE async_tasks" in sql:
            self.events.append(("complete_task", args))
            return "UPDATE 1"
        return "UPDATE 1"

    async def fetchrow(self, sql, *args):
        # find_by_idempotency_key lookup
        if "FROM async_tasks" in sql and "idempotency_key" in sql:
            self._find_calls += 1
            self.events.append(("find_idempotency", args))
            # After the poison-key INSERT violation, the servicer re-queries;
            # return the stale pending row (the "poison" row to reuse).
            if self._find_calls > 1:
                return self.poison_task
            return self.existing_task
        # update_kb RETURNING row
        if "UPDATE knowledge_bases" in sql:
            self.events.append(("update_kb", sql, args))
            if self.raise_exc is not None:
                raise self.raise_exc
            if self.update_result == "default":
                return _kb_row()
            return self.update_result
        # create_task_in_tx INSERT
        if "INSERT INTO async_tasks" in sql:
            self.events.append(("insert_async_tasks", args))
            if self.insert_raise_exc is not None:
                raise self.insert_raise_exc
            return {
                "id": uuid.uuid4(),
                "task_type": "kb.update",
                "status": "pending",
            }
        # audit INSERT (kb_audit_log written by the success path)
        if "INSERT INTO kb_audit_log" in sql:
            self.events.append(("insert_audit", args))
            return {"id": uuid.uuid4()}
        return None

    async def fetch(self, sql, *args):
        return []

    async def fetchval(self, sql, *args):
        return 0


class _MockPool:
    def __init__(self, conn):
        self._conn = conn

    @asynccontextmanager
    async def acquire(self):
        yield self._conn


def _make_servicer(conn):
    return KBServiceServicer(pool=_MockPool(conn))


def _req(**overrides):
    defaults = dict(
        tenant_id=TENANT_ID,
        kb_id=KB_ID,
        idempotency_key=str(uuid.uuid4()),
    )
    defaults.update(overrides)
    return kb_pb.UpdateKBRequest(**defaults)


def _run(coro):
    return asyncio.new_event_loop().run_until_complete(coro)


# ── success: name + description both updated ──────────────────────────────────


def test_update_kb_success_updates_name_and_description():
    updated = _kb_row(name="kb-new", description="new desc")
    conn = _UpdateKBMockConn(update_result=updated)
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    result = _run(servicer._update_kb(
        _req(name="kb-new", description="new desc"), ctx
    ))

    assert result.name == "kb-new"
    assert result.description == "new desc"
    assert result.id == KB_ID
    assert result.tenant_id == TENANT_ID

    # The UPDATE statement carries COALESCE(NULLIF(...)) partial-update
    # semantics and binds the new values as parameters.
    events = {(e[0]): e for e in conn.events}
    sql, args = events["update_kb"][1], events["update_kb"][2]
    assert "COALESCE(NULLIF($2, ''), name)" in sql
    assert "COALESCE(NULLIF($3, ''), description)" in sql
    assert "updated_at = now()" in sql
    assert args[0] == uuid.UUID(KB_ID)
    assert args[1] == "kb-new"
    assert args[2] == "new desc"

    # Idempotency record written: task insert + completion with updated row.
    kinds = [e[0] for e in conn.events]
    assert "insert_async_tasks" in kinds
    assert "complete_task" in kinds


# ── NOT_FOUND: kb missing or RLS-hidden ───────────────────────────────────────


def test_update_kb_not_found_when_no_row():
    conn = _UpdateKBMockConn(update_result=None)
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        _run(servicer._update_kb(_req(name="whatever"), ctx))
    assert ctx.aborted is not None
    assert ctx.aborted[0] == grpc.StatusCode.NOT_FOUND
    # No async_tasks write on the failure path.
    kinds = [e[0] for e in conn.events]
    assert "insert_async_tasks" not in kinds


def test_update_kb_cross_tenant_rls_hidden_returns_not_found():
    """Cross-tenant kb_id is invisible under RLS → update_kb returns None →
    NOT_FOUND (indistinguishable from missing; no existence leak)."""
    conn = _UpdateKBMockConn(update_result=None)
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        _run(servicer._update_kb(
            _req(tenant_id=OTHER_TENANT_ID, name="steal"), ctx
        ))
    assert ctx.aborted[0] == grpc.StatusCode.NOT_FOUND


def test_update_kb_soft_deleted_kb_returns_not_found():
    """UpdateKB on a soft-deleted KB (status='deleted'): the repository UPDATE
    filters `status <> 'deleted'` → returns None → NOT_FOUND. A soft-deleted
    KB must not be resurrected by an update (SPEC §5.1, DeleteKB semantics)."""
    conn = _UpdateKBMockConn(update_result=None)  # row filtered out at SQL level
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        _run(servicer._update_kb(_req(name="zombie"), ctx))
    assert ctx.aborted[0] == grpc.StatusCode.NOT_FOUND
    # No async_tasks write on the failure path — replaying the same
    # idempotency_key later (e.g. after a restore) is still possible.
    kinds = [e[0] for e in conn.events]
    assert "insert_async_tasks" not in kinds
    assert "complete_task" not in kinds


# ── 23505 name collision → ALREADY_EXISTS ─────────────────────────────────────


def test_update_kb_name_conflict_23505_already_exists():
    exc = asyncpg.UniqueViolationError(
        "duplicate key value violates unique constraint "
        "\"knowledge_bases_tenant_id_name_key\""
    )
    conn = _UpdateKBMockConn(raise_exc=exc)
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    with pytest.raises(RuntimeError, match="ALREADY_EXISTS"):
        _run(servicer._update_kb(_req(name="existing-name"), ctx))
    assert ctx.aborted[0] == grpc.StatusCode.ALREADY_EXISTS
    # The 409 error path must not record an async_tasks result (SPEC §5.4:
    # errors are not persisted; replay won't re-emit the 409).
    kinds = [e[0] for e in conn.events]
    assert "insert_async_tasks" not in kinds
    assert "complete_task" not in kinds


# ── poison key self-heal: INSERT hits UNIQUE(tenant_id, idempotency_key) ──────


def test_update_kb_poison_key_self_heals():
    """A prior attempt crashed between create_task and complete_task, leaving
    a pending row with result=NULL. The replay check skips it (no result),
    the UPDATE re-runs, and the task INSERT now violates
    UNIQUE(tenant_id, idempotency_key). The servicer must reuse the pending
    row, complete it with the fresh result, and return success — the retry
    self-heals instead of dying with UNKNOWN forever."""
    poison_task_id = uuid.uuid4()
    poison_task = {
        "id": poison_task_id,
        "task_type": "kb.update",
        "status": "pending",  # stale: never completed
        "result": None,
    }
    exc = asyncpg.UniqueViolationError(
        'duplicate key value violates unique constraint '
        '"async_tasks_tenant_id_idempotency_key_key"'
    )
    conn = _UpdateKBMockConn(
        update_result=_kb_row(name="kb-new"),
        existing_task=poison_task,  # initial find: pending, no result → no replay
        insert_raise_exc=exc,
        poison_task=poison_task,    # post-violation re-find: same pending row
    )
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    result = _run(servicer._update_kb(_req(name="kb-new"), ctx))

    # Retry succeeds instead of UNKNOWN, and the response is the fresh row.
    assert result.name == "kb-new"
    assert ctx.aborted is None

    # The stale pending row is reused (its id) and completed with the result.
    events = {(e[0]): e for e in conn.events}
    complete_args = events["complete_task"][1]
    assert complete_args[0] == poison_task_id
    # find ran twice: replay check + post-violation re-find.
    kinds = [e[0] for e in conn.events]
    assert kinds.count("find_idempotency") == 2


def test_update_kb_poison_key_row_vanished_reraises():
    """If the UNIQUE-violating row cannot be re-found (e.g. RLS race), the
    original exception propagates instead of being masked."""
    exc = asyncpg.UniqueViolationError(
        'duplicate key value violates unique constraint '
        '"async_tasks_tenant_id_idempotency_key_key"'
    )
    conn = _UpdateKBMockConn(
        insert_raise_exc=exc,
        poison_task=None,  # post-violation re-find finds nothing
    )
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    with pytest.raises(asyncpg.UniqueViolationError):
        _run(servicer._update_kb(_req(name="kb-new"), ctx))


# ── idempotent replay ───────────────────────────────────────────────────────────


def test_update_kb_idempotent_replay_returns_recorded_result():
    recorded_row = _kb_row(name="kb-replayed")
    existing_task = {
        "id": uuid.uuid4(),
        "task_type": "kb.update",
        "status": "completed",
        "result": recorded_row,
    }
    conn = _UpdateKBMockConn(update_result=_kb_row(name="should-not-run"), existing_task=existing_task)
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    result = _run(servicer._update_kb(_req(name="kb-replayed"), ctx))

    # Replays the recorded result row...
    assert result.name == "kb-replayed"
    # ...without re-running the UPDATE or re-inserting the task.
    kinds = [e[0] for e in conn.events]
    assert "update_kb" not in kinds
    assert "insert_async_tasks" not in kinds


def test_update_kb_replay_parses_json_string_result():
    """asyncpg may hand back the JSONB result as a raw string; replay must
    normalize it (same handling as _create_kb)."""
    import json

    recorded_row = _kb_row(name="kb-json")
    existing_task = {
        "id": uuid.uuid4(),
        "task_type": "kb.update",
        "status": "completed",
        "result": json.dumps(recorded_row, default=str),
    }
    conn = _UpdateKBMockConn(existing_task=existing_task)
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    result = _run(servicer._update_kb(_req(name="kb-json"), ctx))
    assert result.name == "kb-json"


# ── empty fields keep current values (COALESCE+NULLIF) ──────────────────────────


def test_update_kb_empty_fields_keep_current_values():
    """Empty name/description are passed through to the UPDATE, where
    COALESCE(NULLIF('', ...)) keeps the stored values (SPEC §5.4)."""
    conn = _UpdateKBMockConn()
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    _run(servicer._update_kb(_req(name="", description=""), ctx))

    events = {(e[0]): e for e in conn.events}
    args = events["update_kb"][2]
    assert args[1] == ""  # empty name → COALESCE keeps current
    assert args[2] == ""  # empty description → COALESCE keeps current


def test_update_kb_partial_update_only_description():
    conn = _UpdateKBMockConn()
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    _run(servicer._update_kb(_req(name="", description="only desc"), ctx))

    events = {(e[0]): e for e in conn.events}
    args = events["update_kb"][2]
    assert args[1] == ""
    assert args[2] == "only desc"


# ── validation ─────────────────────────────────────────────────────────────────


def test_update_kb_missing_idempotency_key_invalid_argument():
    conn = _UpdateKBMockConn()
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        _run(servicer._update_kb(_req(idempotency_key=""), ctx))
    assert ctx.aborted[0] == grpc.StatusCode.INVALID_ARGUMENT
    kinds = [e[0] for e in conn.events]
    assert "find_idempotency" not in kinds


def test_update_kb_missing_kb_id_invalid_argument():
    conn = _UpdateKBMockConn()
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        _run(servicer._update_kb(_req(kb_id=""), ctx))
    assert ctx.aborted[0] == grpc.StatusCode.INVALID_ARGUMENT


def test_update_kb_missing_tenant_id_invalid_argument():
    """tenant_id is rejected before touching the DB (validation order
    matches _create_kb; previously UpdateKB relied on RLS to swallow it)."""
    conn = _UpdateKBMockConn()
    servicer = _make_servicer(conn)
    ctx = _FakeContext()

    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        _run(servicer._update_kb(_req(tenant_id=""), ctx))
    assert ctx.aborted[0] == grpc.StatusCode.INVALID_ARGUMENT
    kinds = [e[0] for e in conn.events]
    assert "find_idempotency" not in kinds
    assert "update_kb" not in kinds


# ── skeleton mode: no pool → FAILED_PRECONDITION ───────────────────────────────


@pytest.fixture
def grpc_server():
    from concurrent.futures import ThreadPoolExecutor

    server = grpc.server(ThreadPoolExecutor(max_workers=4))
    kb_grpc.add_KBServiceServicer_to_server(KBServiceServicer(), server)
    port = server.add_insecure_port("[::]:0")
    server.start()
    yield f"localhost:{port}"
    server.stop(grace=None)


@pytest.fixture
def stub(grpc_server):
    return kb_grpc.KBServiceStub(grpc.insecure_channel(grpc_server))


def test_update_kb_no_pool_failed_precondition(stub):
    """Issue-043 AC: `python -m pytest` must pass — UpdateKB is declared and
    responds; without a pool it aborts FAILED_PRECONDITION (skeleton mode)."""
    with pytest.raises(grpc.RpcError) as exc:
        stub.UpdateKB(
            kb_pb.UpdateKBRequest(
                tenant_id=TENANT_ID,
                kb_id=KB_ID,
                idempotency_key=str(uuid.uuid4()),
                name="x",
            )
        )
    assert exc.value.code() == grpc.StatusCode.FAILED_PRECONDITION
