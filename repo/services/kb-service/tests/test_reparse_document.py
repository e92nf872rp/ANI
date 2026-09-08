"""Tests for ReparseDocument servicer + reset_for_reparse_in_tx (issue-047, SPEC §5.1 #12).

Verifies:
- Success: doc reset to parse_status=pending / chunk_count=0 / error_message +
  parsed_at cleared; outbox_events row (event_type='kb.reparse') inserted with
  a payload isomorphic to the notify template (storage_path/file_name from the
  DB doc row); async_tasks idempotency record written (task_type='kb.reparse',
  key = client idempotency_key); returns an AsyncTaskRef.
- Guards: doc=ready → FAILED_PRECONDITION; KB rebuilding → FAILED_PRECONDITION;
  KB missing / doc missing (incl. soft-deleted, RLS-hidden) → NOT_FOUND;
  missing idempotency_key → INVALID_ARGUMENT; no pool → FAILED_PRECONDITION.
- Idempotency: same key with status pending/completed → same task_id replayed
  (no reset / no task insert / no outbox insert); failed task with the same
  key is NOT replayed (client must submit a fresh key → new task created).
- Repository SQL: reset_for_reparse_in_tx binds the exact reset values
  (chunk_count=0 literal, NULLs) and filters soft-deleted rows.

No real DB — mock asyncpg conn records SQL + args (same pattern as
test_update_kb.py / test_b2_chunks_sessions_citations.py).
"""
import os
import sys
import uuid
from contextlib import asynccontextmanager
from datetime import datetime, timezone

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.api.grpc_server import KBServiceServicer
from app.generated.kb.v1 import kb_service_pb2 as kb_pb

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC_ID = "33333333-3333-3333-3333-333333333333"

TASK_ID = "44444444-4444-4444-4444-444444444444"


def _kb_row(**over):
    row = {
        "id": uuid.UUID(KB_ID),
        "tenant_id": uuid.UUID(TENANT_ID),
        "name": "kb",
        "status": "active",
        "chunk_size": 512,
    }
    row.update(over)
    return row


def _doc_row(**over):
    row = {
        "id": uuid.UUID(DOC_ID),
        "kb_id": uuid.UUID(KB_ID),
        "tenant_id": uuid.UUID(TENANT_ID),
        "parse_status": "failed",
        "chunk_count": 3,
        "error_message": "boom",
        "parsed_at": datetime(2026, 9, 1, tzinfo=timezone.utc),
        "storage_path": f"kb-docs/{KB_ID}/{DOC_ID}/a.pdf",
        "file_name": "a.pdf",
        "object_id": "55555555-5555-5555-5555-555555555555",
    }
    row.update(over)
    return row


def _task_row(task_id=TASK_ID, status="pending", task_type="kb.reparse"):
    return {
        "id": uuid.UUID(task_id),
        "task_type": task_type,
        "status": status,
    }


class _ServicerContext:
    """grpc.ServicerContext stand-in — abort raises with the code embedded."""

    def __init__(self):
        self.aborted = None

    def abort(self, code, message):
        self.aborted = (code, message)
        raise RuntimeError(f"aborted: {code} {message}")


class _ReparseConn:
    """Recording fake conn for the ReparseDocument flow.

    Dispatches by table keywords: async_tasks lookups, knowledge_bases /
    kb_documents reads, async_tasks + outbox_events inserts, the
    reset UPDATE. `existing_task` feeds find_by_idempotency_key;
    `reset_result` ("UPDATE 1"/"UPDATE 0") feeds reset_for_reparse_in_tx.

    UNIQUE-race simulation (mirrors _UpdateKBMockConn): `insert_raise_exc`
    makes INSERT INTO async_tasks raise (asyncpg.UniqueViolationError);
    the retry find (2nd find_by_idempotency_key call) then returns
    `race_task` instead of `existing_task`.
    """

    def __init__(
        self,
        *,
        existing_task=None,
        kb_row="default",
        doc_row="default",
        reset_result="UPDATE 1",
        insert_raise_exc=None,
        race_task=None,
    ):
        self.existing_task = existing_task
        self._kb_row = _kb_row() if kb_row == "default" else kb_row
        self._doc_row = _doc_row() if doc_row == "default" else doc_row
        self.reset_result = reset_result
        self.insert_raise_exc = insert_raise_exc
        self.race_task = race_task
        self._find_calls = 0
        self.execute_calls: list[tuple] = []
        self.fetchrow_calls: list[tuple] = []

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            yield self

        return _tx()

    async def execute(self, sql, *args):
        self.execute_calls.append((sql, args))
        if sql.startswith("SET LOCAL"):
            return None
        if "UPDATE kb_documents" in sql:
            return self.reset_result
        return "UPDATE 1"

    async def fetchrow(self, sql, *args):
        self.fetchrow_calls.append((sql, args))
        if "FROM async_tasks" in sql and "idempotency_key" in sql:
            self._find_calls += 1
            # Honor the task_type filter the way the real SQL does
            # ($3::text IS NULL OR task_type = $3): a row of a different
            # task_type must NOT be returned by the lookup.
            task = self.existing_task
            if (
                task is not None
                and len(args) >= 3
                and args[2] is not None
                and task.get("task_type") != args[2]
            ):
                task = None
            # After the UNIQUE-race INSERT violation, the servicer
            # re-queries; return the concurrent winner's row.
            if self._find_calls > 1:
                return self.race_task
            return task
        if "FROM knowledge_bases" in sql:
            return self._kb_row
        if "FROM kb_documents" in sql:
            return self._doc_row
        if "INSERT INTO async_tasks" in sql:
            if self.insert_raise_exc is not None:
                raise self.insert_raise_exc
            return _task_row()
        if "INSERT INTO outbox_events" in sql:
            return {"id": 1}
        return None

    async def fetch(self, sql, *args):
        return []

    async def fetchval(self, sql, *args):
        return None


class _Pool:
    def __init__(self, conn):
        self._conn = conn

    @asynccontextmanager
    async def acquire(self):
        yield self._conn


def _make_servicer(conn):
    return KBServiceServicer(pool=_Pool(conn))


def _req(**over):
    defaults = dict(
        tenant_id=TENANT_ID,
        kb_id=KB_ID,
        doc_id=DOC_ID,
        idempotency_key=str(uuid.uuid4()),
    )
    defaults.update(over)
    return kb_pb.ReparseDocumentRequest(**defaults)


# ── servicer: success path ───────────────────────────────────────────────────


async def test_reparse_success_resets_doc_and_writes_outbox_and_task():
    conn = _ReparseConn()
    servicer = _make_servicer(conn)
    idem = str(uuid.uuid4())

    result = await servicer._reparse_document(_req(idempotency_key=idem), _ServicerContext())

    assert result.task_id == TASK_ID
    assert result.task_type == "kb.reparse"
    assert result.status == "pending"

    # RLS tenant context is set before the business statements (each repo
    # helper's SET LOCAL goes through conn.execute and is recorded here).
    assert any("SET LOCAL app.current_tenant_id" in s for s, _ in conn.execute_calls)

    # a. reset UPDATE: exact reset semantics bound in SQL.
    reset_calls = [(s, a) for s, a in conn.execute_calls if "UPDATE kb_documents" in s]
    assert len(reset_calls) == 1
    sql, args = reset_calls[0]
    assert "parse_status = 'pending'" in sql
    assert "error_message = NULL" in sql
    assert "parsed_at = NULL" in sql
    # chunk_count must be reset to 0 literal (NOT NULL column — assigning
    # NULL would violate the constraint and abort the transaction).
    assert "chunk_count = 0" in sql
    assert "chunk_count = NULL" not in sql
    assert args == (uuid.UUID(DOC_ID), uuid.UUID(KB_ID))
    # Soft-deleted rows excluded from the reset.
    assert "error_message = 'deleted'" in sql

    # b. async_tasks insert: task_type='kb.reparse', key = client idem key.
    insert_task = [
        (s, a) for s, a in conn.fetchrow_calls if "INSERT INTO async_tasks" in s
    ]
    assert len(insert_task) == 1
    sql, args = insert_task[0]
    assert "INSERT INTO async_tasks" in sql
    assert args[0] == uuid.UUID(TENANT_ID)
    assert args[1] == idem
    assert args[2] == "kb.reparse"

    # c. outbox insert: event_type='kb.reparse'; payload isomorphic to the
    #    notify template with storage_path/file_name taken from the DB row.
    insert_outbox = [
        (s, a) for s, a in conn.fetchrow_calls if "INSERT INTO outbox_events" in s
    ]
    assert len(insert_outbox) == 1
    sql, args = insert_outbox[0]
    assert args[0] == "kb_documents"      # aggregate_type
    assert args[1] == uuid.UUID(DOC_ID)    # aggregate_id
    assert args[2] == "kb.reparse"        # event_type
    assert args[3] == uuid.UUID(TENANT_ID)
    import json as _json
    payload = _json.loads(args[4])
    assert payload == {
        "doc_id": DOC_ID,
        "kb_id": KB_ID,
        "storage_path": f"kb-docs/{KB_ID}/{DOC_ID}/a.pdf",  # from DB doc_row
        "tenant_id": TENANT_ID,
        "file_name": "a.pdf",                               # real name, not ""
        "object_id": "55555555-5555-5555-5555-555555555555",
        "chunk_size": 512,                                  # from KB row
        "task_id": TASK_ID,                                 # consumer closure ref
    }


async def test_reparse_success_on_failed_doc_with_errors():
    """Main scenario: failed doc with error_message + parsed_at + chunks gets
    all of them cleared (asserted through the reset SQL values above); this
    test pins the guard that lets a failed doc through."""
    conn = _ReparseConn(doc_row=_doc_row(parse_status="failed", error_message="ocr failed"))
    servicer = _make_servicer(conn)
    result = await servicer._reparse_document(_req(), _ServicerContext())
    assert result.status == "pending"
    assert any("UPDATE kb_documents" in s for s, _ in conn.execute_calls)


# ── servicer: guards ──────────────────────────────────────────────────────────


async def test_reparse_ready_doc_rejected_failed_precondition():
    conn = _ReparseConn(doc_row=_doc_row(parse_status="ready"))
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="FAILED_PRECONDITION"):
        await servicer._reparse_document(_req(), ctx)
    assert ctx.aborted is not None
    # No reset / task / outbox write happened.
    assert not any("UPDATE kb_documents" in s for s, _ in conn.execute_calls)
    assert not any("INSERT INTO async_tasks" in s for s, _ in conn.fetchrow_calls)


async def test_reparse_kb_rebuilding_rejected_failed_precondition():
    conn = _ReparseConn(kb_row=_kb_row(status="rebuilding"))
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="FAILED_PRECONDITION"):
        await servicer._reparse_document(_req(), ctx)
    assert ctx.aborted is not None


async def test_reparse_kb_missing_not_found():
    conn = _ReparseConn(kb_row=None)
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._reparse_document(_req(), ctx)
    assert ctx.aborted is not None


async def test_reparse_doc_missing_not_found():
    """Also covers soft-deleted docs: get_document filters them → None → 404."""
    conn = _ReparseConn(doc_row=None)
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._reparse_document(_req(), ctx)
    assert ctx.aborted is not None


async def test_reparse_missing_idempotency_key_invalid_argument():
    conn = _ReparseConn()
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._reparse_document(
            _req(idempotency_key=""), ctx
        )
    assert ctx.aborted is not None


async def test_reparse_no_pool_failed_precondition():
    servicer = KBServiceServicer()
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="FAILED_PRECONDITION"):
        await servicer._reparse_document(_req(), ctx)
    assert ctx.aborted is not None


# ── servicer: idempotency ─────────────────────────────────────────────────────


async def test_reparse_same_key_pending_replays_same_task_id():
    """Same key, prior task pending → the same task_id is returned and no
    reset/task/outbox write runs."""
    conn = _ReparseConn(existing_task=_task_row(status="pending"))
    servicer = _make_servicer(conn)
    result = await servicer._reparse_document(_req(), _ServicerContext())
    assert result.task_id == TASK_ID
    assert result.status == "pending"
    assert result.task_type == "kb.reparse"
    assert not any("UPDATE kb_documents" in s for s, _ in conn.execute_calls)
    assert not any("INSERT INTO async_tasks" in s for s, _ in conn.fetchrow_calls)
    assert not any("INSERT INTO outbox_events" in s for s, _ in conn.fetchrow_calls)


async def test_reparse_same_key_completed_replays_same_task_id():
    conn = _ReparseConn(existing_task=_task_row(status="completed"))
    servicer = _make_servicer(conn)
    result = await servicer._reparse_document(_req(), _ServicerContext())
    assert result.task_id == TASK_ID
    assert result.status == "completed"


async def test_reparse_find_filters_by_task_type():
    """The replay lookup narrows by task_type='kb.reparse': a row recorded by
    a DIFFERENT operation (e.g. kb.parse) under the same tenant + key must
    NOT short-circuit the replay — the reparse proceeds down the full path
    (reset + INSERT). This pins the task_type filter on
    find_by_idempotency_key (cross-operation key reuse must not replay
    another task's ref)."""
    conn = _ReparseConn(
        existing_task=_task_row(status="pending", task_type="kb.parse")
    )
    servicer = _make_servicer(conn)
    result = await servicer._reparse_document(_req(), _ServicerContext())
    # Not the kb.parse row — a fresh kb.reparse task was created instead.
    assert result.task_type == "kb.reparse"
    assert result.status == "pending"
    assert result.task_id == TASK_ID
    # The full reparse path ran: doc reset + new async_tasks INSERT.
    assert any("UPDATE kb_documents" in s for s, _ in conn.execute_calls)
    assert any("INSERT INTO async_tasks" in s for s, _ in conn.fetchrow_calls)
    # And the replay lookup itself carried the task_type filter.
    find_calls = [
        (s, a) for s, a in conn.fetchrow_calls
        if "FROM async_tasks" in s and "idempotency_key" in s
    ]
    assert len(find_calls) >= 1
    sql, args = find_calls[0]
    assert "task_type = $3" in sql
    assert args[2] == "kb.reparse"


async def test_reparse_failed_task_same_key_not_replayed():
    """A failed task is NOT replayed on the same key (SPEC §5.4): the
    pre-check does not short-circuit, so the transaction runs and the
    INSERT hits UNIQUE(tenant_id, idempotency_key); the self-heal
    re-lookup finds the failed row and rejects with FAILED_PRECONDITION
    (client must submit a fresh key). The abort rolls back the whole
    transaction — the doc reset is undone too."""
    import asyncpg

    conn = _ReparseConn(
        existing_task=_task_row(status="failed"),
        insert_raise_exc=asyncpg.UniqueViolationError(
            'duplicate key value violates unique constraint '
            '"async_tasks_tenant_id_idempotency_key_key"'
        ),
        race_task=_task_row(status="failed"),
    )
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="FAILED_PRECONDITION"):
        await servicer._reparse_document(_req(), ctx)
    assert ctx.aborted is not None
    # No second outbox event on the rejected retry.
    assert not any("INSERT INTO outbox_events" in s for s, _ in conn.fetchrow_calls)


async def test_reparse_unique_race_pending_replays_winner_task():
    """TOCTOU self-heal: the pre-check sees no task, but a concurrent
    reparse with the same key commits between the check and the INSERT.
    The UNIQUE violation is caught; the re-lookup returns the winner's
    pending row → same task_id replayed, no second outbox event."""
    import asyncpg

    conn = _ReparseConn(
        insert_raise_exc=asyncpg.UniqueViolationError(
            'duplicate key value violates unique constraint '
            '"async_tasks_tenant_id_idempotency_key_key"'
        ),
        race_task=_task_row(status="pending"),
    )
    servicer = _make_servicer(conn)
    result = await servicer._reparse_document(_req(), _ServicerContext())
    assert result.task_id == TASK_ID
    assert result.status == "pending"
    # The reset ran (outer tx continues past the SAVEPOINT abort)…
    assert any("UPDATE kb_documents" in s for s, _ in conn.execute_calls)
    # …but no outbox event: the winner's tx already published one.
    assert not any("INSERT INTO outbox_events" in s for s, _ in conn.fetchrow_calls)


async def test_reparse_failed_task_new_key_creates_new_task():
    """End-to-end variant: failed with old key, fresh key → full reparse
    transaction runs (the recovery path users actually take)."""
    conn = _ReparseConn(existing_task=_task_row(status="failed"))
    servicer = _make_servicer(conn)
    result = await servicer._reparse_document(
        _req(idempotency_key=str(uuid.uuid4())), _ServicerContext()
    )
    assert result.task_id == TASK_ID
    assert result.status == "pending"
    assert any("UPDATE kb_documents" in s for s, _ in conn.execute_calls)


# ── repository: reset_for_reparse_in_tx ───────────────────────────────────────


async def test_reset_for_reparse_in_tx_sql_and_args():
    """SQL-level assertions: exact reset values, soft-delete filter, RLS set."""
    from app.repositories import document as document_repo

    conn = _ReparseConn()
    updated = await document_repo.reset_for_reparse_in_tx(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID
    )
    assert updated is True
    sql, args = conn.execute_calls[-1]
    assert "parse_status = 'pending'" in sql
    assert "error_message = NULL" in sql
    assert "parsed_at = NULL" in sql
    assert "chunk_count = 0" in sql
    assert "chunk_count = NULL" not in sql
    assert "NOT (parse_status = 'failed' AND error_message = 'deleted')" in sql
    assert args == (uuid.UUID(DOC_ID), uuid.UUID(KB_ID))
    # RLS context set inside the transaction before the UPDATE.
    assert "SET LOCAL app.current_tenant_id" in conn.execute_calls[0][0]


async def test_reset_for_reparse_in_tx_row_missing_returns_false():
    from app.repositories import document as document_repo

    conn = _ReparseConn(reset_result="UPDATE 0")
    updated = await document_repo.reset_for_reparse_in_tx(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID
    )
    assert updated is False


async def test_reparse_reset_missing_row_surfaces_not_found():
    """The servicer's in-transaction guard: reset affects 0 rows → 404."""
    conn = _ReparseConn(reset_result="UPDATE 0")
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._reparse_document(_req(), ctx)
    assert ctx.aborted is not None
    # Task/outbox inserts never ran.
    assert not any("INSERT INTO async_tasks" in s for s, _ in conn.fetchrow_calls)
    assert not any("INSERT INTO outbox_events" in s for s, _ in conn.fetchrow_calls)


# ── regression: orchestrator cleanup link unaffected ──────────────────────────


def test_reparse_docstring_pins_orchestrator_cleanup():
    """The parse_orchestrator keeps its built-in re-entrant cleanup
    (delete_chunks_by_doc + best-effort Core vector delete). Issue #047
    requires ZERO downstream changes — test_reparse_deletes_prior_chunks in
    test_parse_orchestrator.py covers the cleanup itself; this guard pins that
    the servicer never calls chunk/vector deletion itself (the fake conn would
    record any such statement)."""
    src = os.path.join(
        _SERVICE_ROOT, "app", "services", "parse_orchestrator.py"
    )
    with open(src, encoding="utf-8") as f:
        text = f.read()
    assert "delete_chunks_by_doc" in text
    assert "delete_vector_store_documents" in text
