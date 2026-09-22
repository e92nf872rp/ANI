"""Tests for UpdateKBConfig servicer + update_config_in_tx (P1 #23, B7).

Mirrors test_rebuild_kb.py (mock asyncpg conn, no real DB):

Servicer (_update_kb_config):
- Success without rebuild: non-embedding patch (top_k) → explicit-partial
  UPDATE (patch columns only), kb.config.update audit, kb.config.update
  idempotency record (insert + complete); the response carries the new
  config and rebuild_task UNSET — the wire form of the contract's
  nullable rebuild_task.
- Success with paired rebuild: chunk_size change → same-transaction
  rebuild trigger (conditional active→rebuilding transition, kb.rebuild
  audit, nested "rebuild:<uuid>"-keyed task, outbox event) and the
  response carries the rebuild AsyncTaskRef.
- Tri-state: only the carried fields land in the SET clause; absent
  fields keep their current value and trigger no rebuild.
- Change detection: a carried-but-equal field (or an empty patch) is
  INVALID_ARGUMENT "no effective change" — before any write.
- Replay: a completed kb.config.update task under the same key replays
  the recorded config + rebuild ref with zero writes (task_type-filtered
  lookup).
- Guards: KB missing → NOT_FOUND; rebuilding → FAILED_PRECONDITION (B6
  mutex); no pool → FAILED_PRECONDITION; poisoned rebuild key (UNIQUE
  race finding a failed kb.rebuild task on the derived key) →
  FAILED_PRECONDITION with the whole config change rolled back.
- Validation: idempotency_key shape and carried value ranges reject
  INVALID_ARGUMENT BEFORE the pool guard (no DB touched).
- Failure audit: an auditable business rejection (rebuilding gate)
  records a kb.config.update failure audit row in its own transaction.
"""
import json
import os
import re
import sys
import uuid
from contextlib import asynccontextmanager

import asyncpg
import grpc
import pytest
from google.protobuf import wrappers_pb2

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.api.grpc_server import KBServiceServicer
from app.generated.kb.v1 import kb_service_pb2 as kb_pb

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
TASK_ID = "44444444-4444-4444-4444-444444444444"


def _kb_row(**over):
    row = {
        "id": uuid.UUID(KB_ID),
        "tenant_id": uuid.UUID(TENANT_ID),
        "name": "kb",
        "status": "active",
        "chunk_size": 512,
        "embedding_model": "bge-m3",
        "vector_store_id": "vs_kb_2222222222222222222222222",
    }
    row.update(over)
    return row


def _task_row(task_id=TASK_ID, status="pending", task_type="kb.rebuild"):
    return {
        "id": uuid.UUID(task_id),
        "task_type": task_type,
        "status": status,
    }


def _config_task_row(status="completed", result=None):
    """A kb.config.update idempotency row; ``result`` is the recorded
    response payload ({config, rebuild_task}) the replay path returns."""
    row = {
        "id": uuid.UUID(TASK_ID),
        "task_type": "kb.config.update",
        "status": status,
    }
    if result is not None:
        row["result"] = result if isinstance(result, str) else json.dumps(result)
    return row


class _ServicerContext:
    """grpc.ServicerContext stand-in — abort raises with the code embedded."""

    def __init__(self):
        self.aborted = None

    def abort(self, code, message):
        self.aborted = (code, message)
        raise RuntimeError(f"aborted: {code} {message}")

    # _audit_failure_info reads code()/details() off aborted contexts; a
    # raising fake keeps the failure-audit path a no-op (same as
    # test_rebuild_kb.py).
    def code(self):
        raise RuntimeError("not a real context")

    def details(self):
        raise RuntimeError("not a real context")


class _AuditedContext(_ServicerContext):
    """Same as _ServicerContext but code()/details() return the aborted
    state, so _audit_failure_info classifies the failure as auditable and
    _record_failure_audit writes its row through the pool."""

    def code(self):
        return self.aborted[0] if self.aborted else None

    def details(self):
        return self.aborted[1] if self.aborted else ""


class _ConfigConn:
    """Recording fake conn for the UpdateKBConfig flow.

    Dispatches by SQL keywords: async_tasks idempotency lookups (config
    replay first, rebuild-race retry later), knowledge_bases reads (KB
    gate), the config UPDATE ... RETURNING (patch columns parsed back out
    of the SQL and applied onto the kb row — the servicer builds the
    response from that RETURN), the conditional status UPDATE (rebuild
    trigger), and async_tasks / outbox_events / kb_audit_log inserts.

    UNIQUE-race simulation (mirrors _RebuildConn): ``insert_raise_exc``
    makes the async_tasks INSERT raise; the retry find then returns
    ``race_task`` instead of ``existing_task``.
    """

    def __init__(
        self,
        *,
        existing_task=None,
        kb_row="default",
        transition_result="UPDATE 1",
        insert_raise_exc=None,
        race_task=None,
    ):
        self.existing_task = existing_task
        self._kb_row = _kb_row() if kb_row == "default" else kb_row
        self.transition_result = transition_result
        self.insert_raise_exc = insert_raise_exc
        self.race_task = race_task
        self._find_calls = 0
        self.updated_row = None  # what the config UPDATE returned
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
        if "UPDATE knowledge_bases" in sql and "status = $2" in sql:
            return self.transition_result
        return "UPDATE 1"

    async def fetchrow(self, sql, *args):
        self.fetchrow_calls.append((sql, args))
        if "FROM async_tasks" in sql and "idempotency_key" in sql:
            self._find_calls += 1
            task = self.existing_task
            if (
                task is not None
                and len(args) >= 3
                and args[2] is not None
                and task.get("task_type") != args[2]
            ):
                task = None
            if self._find_calls > 1:
                return self.race_task
            return task
        if "UPDATE knowledge_bases" in sql and "RETURNING" in sql:
            # update_config_in_tx: apply the SET columns (parsed from the
            # SQL, mirroring the whitelist-driven dynamic assignments)
            # onto the kb row — positional args bind $2.. from the patch.
            set_clause = sql.split("SET", 1)[1].split("updated_at", 1)[0]
            cols = re.findall(r"(\w+) = \$\d+", set_clause)
            row = dict(self._kb_row)
            for col, val in zip(cols, args[1:]):
                row[col] = val
            self.updated_row = row
            return row
        if "FROM knowledge_bases" in sql:
            return self._kb_row
        if "INSERT INTO async_tasks" in sql:
            if self.insert_raise_exc is not None:
                raise self.insert_raise_exc
            return _task_row(task_type=args[2] if len(args) > 2 else "kb.rebuild")
        if "INSERT INTO outbox_events" in sql:
            return {"id": 1}
        if "INSERT INTO kb_audit_log" in sql:
            return {"id": uuid.uuid4()}
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
        idempotency_key=str(uuid.uuid4()),
    )
    defaults.update(over)
    return kb_pb.UpdateKBConfigRequest(**defaults)


def _config_update_calls(conn):
    return [
        (s, a) for s, a in conn.fetchrow_calls
        if "UPDATE knowledge_bases" in s and "RETURNING" in s
    ]


# ── servicer: success paths ───────────────────────────────────────────────────


async def test_update_config_success_no_rebuild():
    conn = _ConfigConn()
    servicer = _make_servicer(conn)
    idem = str(uuid.uuid4())

    result = await servicer._update_kb_config(
        _req(idempotency_key=idem, top_k=12), _ServicerContext()
    )

    # 200 carries the new config; rebuild_task stays UNSET — the wire
    # form of the contract's nullable rebuild_task (no embedding change).
    assert result.HasField("config")
    assert not result.HasField("rebuild_task")
    assert result.config.top_k == 12
    assert result.config.embedding_model == "bge-m3"
    assert result.config.chunk_size == 512

    # explicit-partial UPDATE: only the carried column, kb_id bound at $1.
    update_calls = _config_update_calls(conn)
    assert len(update_calls) == 1
    sql, args = update_calls[0]
    assert "SET top_k = $2" in sql
    assert "WHERE id = $1 AND status <> 'deleted'" in sql
    assert args == (uuid.UUID(KB_ID), 12)

    # no rebuild side effects: no transition, no outbox event.
    assert not any(
        "UPDATE knowledge_bases" in s and "status = $2" in s
        for s, _ in conn.execute_calls
    )
    assert not any("INSERT INTO outbox_events" in s for s, _ in conn.fetchrow_calls)

    # audit kb.config.update with before/after config snapshots.
    audit_calls = [
        (s, a) for s, a in conn.fetchrow_calls if "INSERT INTO kb_audit_log" in s
    ]
    assert len(audit_calls) == 1
    a = audit_calls[0][1]
    assert a[0] == uuid.UUID(TENANT_ID)
    assert a[1] == uuid.UUID(KB_ID)
    assert a[3] == "kb.config.update"
    assert json.loads(a[4])["top_k"] is None  # before: current row had no top_k
    assert json.loads(a[5])["top_k"] == 12    # after: the returned row

    # idempotency record: exactly one kb.config.update task under the
    # client key (insert + complete; no nested rebuild key).
    task_inserts = [
        (s, a) for s, a in conn.fetchrow_calls if "INSERT INTO async_tasks" in s
    ]
    assert len(task_inserts) == 1
    assert task_inserts[0][1][1] == idem
    assert task_inserts[0][1][2] == "kb.config.update"
    assert json.loads(task_inserts[0][1][6]) == {
        "kb_id": KB_ID,
        "patch": {"top_k": 12},
        "rebuild_needed": False,
    }


async def test_update_config_success_paired_rebuild():
    conn = _ConfigConn()
    servicer = _make_servicer(conn)
    idem = str(uuid.uuid4())

    result = await servicer._update_kb_config(
        _req(idempotency_key=idem, chunk_size=1024), _ServicerContext()
    )

    # 200 carries the paired rebuild ref (contract: nullable rebuild_task,
    # present because the change touched the embedding settings).
    assert result.HasField("rebuild_task")
    assert result.rebuild_task.task_id == TASK_ID
    assert result.rebuild_task.task_type == "kb.rebuild"
    assert result.rebuild_task.status == "pending"
    assert result.config.chunk_size == 1024

    # conditional active→rebuilding transition ran (same tx as the UPDATE).
    transition_calls = [
        (s, a) for s, a in conn.execute_calls
        if "UPDATE knowledge_bases" in s and "status = $2" in s
    ]
    assert len(transition_calls) == 1
    assert transition_calls[0][1] == (uuid.UUID(KB_ID), "rebuilding", "active")

    # two task inserts: the nested-key rebuild task, then the config task.
    task_inserts = [
        (s, a) for s, a in conn.fetchrow_calls if "INSERT INTO async_tasks" in s
    ]
    assert len(task_inserts) == 2
    rebuild_args = task_inserts[0][1]
    config_args = task_inserts[1][1]
    assert rebuild_args[1] == f"rebuild:{idem}"
    assert rebuild_args[2] == "kb.rebuild"
    assert json.loads(rebuild_args[6]) == {
        "kb_id": KB_ID,
        "embedding_model": "bge-m3",
        "chunk_size": 1024,
    }
    assert config_args[1] == idem
    assert config_args[2] == "kb.config.update"
    assert json.loads(config_args[6]) == {
        "kb_id": KB_ID,
        "patch": {"chunk_size": 1024},
        "rebuild_needed": True,
    }

    # both audits, in trigger order: kb.rebuild then kb.config.update.
    audits = [a for s, a in conn.fetchrow_calls if "INSERT INTO kb_audit_log" in s]
    assert [a[3] for a in audits] == ["kb.rebuild", "kb.config.update"]
    assert json.loads(audits[0][4]) == {"status": "active"}
    assert json.loads(audits[0][5]) == {"status": "rebuilding"}

    # outbox event carries the rebuild task id.
    outbox = [a for s, a in conn.fetchrow_calls if "INSERT INTO outbox_events" in s]
    assert len(outbox) == 1
    assert outbox[0][0] == "knowledge_bases"
    assert outbox[0][1] == uuid.UUID(KB_ID)
    assert outbox[0][2] == "kb.rebuild"
    assert json.loads(outbox[0][4]) == {
        "kb_id": KB_ID,
        "tenant_id": TENANT_ID,
        "task_id": TASK_ID,
    }


async def test_update_config_tri_state_keeps_absent_fields():
    """Only the explicitly-carried fields are change candidates: the SET
    clause carries exactly those columns; everything else keeps its value
    and no rebuild is triggered."""
    conn = _ConfigConn(kb_row=_kb_row(ocr_enabled=True))
    servicer = _make_servicer(conn)

    result = await servicer._update_kb_config(
        _req(
            ocr_enabled=wrappers_pb2.BoolValue(value=False),
            score_threshold=0.5,
        ),
        _ServicerContext(),
    )

    sql = _config_update_calls(conn)[0][0]
    set_clause = sql.split("SET", 1)[1].split("updated_at", 1)[0]
    assert re.findall(r"(\w+) = \$\d+", set_clause) == [
        "ocr_enabled", "score_threshold",
    ]
    assert not result.HasField("rebuild_task")
    assert result.config.ocr_enabled is False
    assert result.config.score_threshold == pytest.approx(0.5)
    assert result.config.embedding_model == "bge-m3"
    assert result.config.top_k == 0


async def test_update_config_explicit_zero_is_a_change_candidate():
    """proto3 optional explicit zero ≠ absent: chunk_size 1 on a 512 KB is
    a real change (and triggers a rebuild), not a 'no effective change'."""
    conn = _ConfigConn()
    servicer = _make_servicer(conn)

    result = await servicer._update_kb_config(
        _req(chunk_size=1), _ServicerContext()
    )
    assert result.config.chunk_size == 1
    assert result.HasField("rebuild_task")  # chunk_size change pairs a rebuild


# ── servicer: change detection ────────────────────────────────────────────────


async def test_update_config_no_effective_change_rejected():
    # carried value equals the current one (chunk_size 512 == current 512).
    conn = _ConfigConn()
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="no effective change"):
        await servicer._update_kb_config(_req(chunk_size=512), ctx)
    assert ctx.aborted[0] == grpc.StatusCode.INVALID_ARGUMENT
    # rejected before any write.
    assert _config_update_calls(conn) == []
    assert not any("INSERT INTO" in s for s, _ in conn.fetchrow_calls)

    # an empty patch (no carried config fields) is the same rejection.
    conn2 = _ConfigConn()
    servicer2 = _make_servicer(conn2)
    ctx2 = _ServicerContext()
    with pytest.raises(RuntimeError, match="no effective change"):
        await servicer2._update_kb_config(_req(), ctx2)
    assert ctx2.aborted[0] == grpc.StatusCode.INVALID_ARGUMENT
    assert not any("INSERT INTO" in s for s, _ in conn2.fetchrow_calls)


# ── servicer: idempotent replay ───────────────────────────────────────────────


async def test_update_config_replay_completed_with_rebuild():
    recorded = {
        "config": {
            "embedding_model": "bge-m3",
            "chunk_size": 1024,
            "top_k": 12,
            "retrieval_mode": "hybrid",
        },
        "rebuild_task": {
            "task_id": TASK_ID,
            "task_type": "kb.rebuild",
            "status": "pending",
            "location_url": "",
        },
    }
    conn = _ConfigConn(existing_task=_config_task_row(result=recorded))
    servicer = _make_servicer(conn)

    result = await servicer._update_kb_config(
        _req(
            idempotency_key="33333333-3333-3333-3333-333333333333",
            chunk_size=1024,
        ),
        _ServicerContext(),
    )

    assert result.config.chunk_size == 1024
    assert result.config.top_k == 12
    assert result.HasField("rebuild_task")
    assert result.rebuild_task.task_id == TASK_ID
    assert result.rebuild_task.task_type == "kb.rebuild"
    assert result.rebuild_task.status == "pending"

    # the replay lookup carried the kb.config.update task-type filter.
    find_calls = [
        (s, a) for s, a in conn.fetchrow_calls
        if "FROM async_tasks" in s and "idempotency_key" in s
    ]
    assert len(find_calls) == 1
    sql, args = find_calls[0]
    assert "task_type = $3" in sql
    assert args[2] == "kb.config.update"

    # zero writes on replay: no UPDATE, no INSERT anywhere.
    assert not any("UPDATE knowledge_bases" in s for s, _ in conn.execute_calls)
    assert not any("UPDATE knowledge_bases" in s for s, _ in conn.fetchrow_calls)
    assert not any("INSERT INTO" in s for s, _ in conn.fetchrow_calls)


async def test_update_config_replay_completed_without_rebuild():
    """A recorded result without a rebuild_task (non-embedding change)
    replays with rebuild_task unset — the nullable's wire form."""
    conn = _ConfigConn(
        existing_task=_config_task_row(
            result={"config": {"embedding_model": "bge-m3", "top_k": 7}}
        )
    )
    servicer = _make_servicer(conn)

    result = await servicer._update_kb_config(_req(top_k=7), _ServicerContext())
    assert not result.HasField("rebuild_task")
    assert result.config.top_k == 7
    assert result.config.retrieval_mode == "hybrid"  # _kb_config_msg default
    assert not any("INSERT INTO" in s for s, _ in conn.fetchrow_calls)


async def test_update_config_replay_skips_other_task_types():
    """A row recorded by a DIFFERENT operation (e.g. kb.reparse) under the
    same tenant + key must NOT replay — the update proceeds down the full
    path (gate read + config UPDATE)."""
    conn = _ConfigConn(
        existing_task=_task_row(status="completed", task_type="kb.reparse")
    )
    servicer = _make_servicer(conn)

    result = await servicer._update_kb_config(_req(top_k=12), _ServicerContext())
    assert result.config.top_k == 12
    assert len(_config_update_calls(conn)) == 1


# ── servicer: guards ──────────────────────────────────────────────────────────


async def test_update_config_kb_missing_not_found():
    conn = _ConfigConn(kb_row=None)
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._update_kb_config(_req(top_k=12), ctx)
    assert ctx.aborted is not None
    assert ctx.aborted[0] == grpc.StatusCode.NOT_FOUND
    assert _config_update_calls(conn) == []
    assert not any("INSERT INTO async_tasks" in s for s, _ in conn.fetchrow_calls)


async def test_update_config_kb_rebuilding_rejected():
    """B6 mutex: a KB already rebuilding rejects the config update at the
    gate — no config write, no rebuild trigger."""
    conn = _ConfigConn(kb_row=_kb_row(status="rebuilding"))
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="FAILED_PRECONDITION"):
        await servicer._update_kb_config(_req(top_k=12), ctx)
    assert ctx.aborted[0] == grpc.StatusCode.FAILED_PRECONDITION
    assert _config_update_calls(conn) == []
    assert not any("INSERT INTO outbox_events" in s for s, _ in conn.fetchrow_calls)


async def test_update_config_no_pool_failed_precondition():
    servicer = KBServiceServicer()
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="FAILED_PRECONDITION"):
        await servicer._update_kb_config(_req(top_k=12), ctx)
    assert ctx.aborted is not None


async def test_update_config_poisoned_rebuild_key_rejected():
    """The nested rebuild insert hits a UNIQUE violation and the retry finds
    a FAILED kb.rebuild task on the derived key — FAILED_PRECONDITION; the
    abort rolls back the whole outer tx (config UPDATE and config audit
    included)."""
    conn = _ConfigConn(
        insert_raise_exc=asyncpg.UniqueViolationError(
            'duplicate key value violates unique constraint '
            '"async_tasks_tenant_id_idempotency_key_key"'
        ),
        race_task=_task_row(status="failed"),
    )
    servicer = _make_servicer(conn)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="FAILED_PRECONDITION"):
        await servicer._update_kb_config(_req(chunk_size=1024), ctx)
    assert ctx.aborted[0] == grpc.StatusCode.FAILED_PRECONDITION

    # only the rebuild task insert was attempted (it raised); no config
    # task record, no outbox event, no config audit.
    task_inserts = [
        (s, a) for s, a in conn.fetchrow_calls if "INSERT INTO async_tasks" in s
    ]
    assert len(task_inserts) == 1
    assert task_inserts[0][1][2] == "kb.rebuild"
    assert not any("INSERT INTO outbox_events" in s for s, _ in conn.fetchrow_calls)
    audits = [a for s, a in conn.fetchrow_calls if "INSERT INTO kb_audit_log" in s]
    assert [a[3] for a in audits] == ["kb.rebuild"]


# ── servicer: request validation (before the pool guard) ──────────────────────


async def test_update_config_value_ranges_invalid_argument():
    """Value validation happens BEFORE the pool check — a bare servicer
    (no pool) must surface INVALID_ARGUMENT, not FAILED_PRECONDITION."""
    servicer = KBServiceServicer()
    cases = [
        ({"chunk_size": 0}, "chunk_size"),
        ({"chunk_size": 8193}, "chunk_size"),
        ({"top_k": 0}, "top_k"),
        ({"top_k": 21}, "top_k"),
        ({"score_threshold": -0.1}, "score_threshold"),
        ({"score_threshold": 1.5}, "score_threshold"),
        ({"retrieval_mode": "fulltext"}, "retrieval_mode"),
        ({"embedding_model": "   "}, "embedding_model"),
    ]
    for over, needle in cases:
        ctx = _ServicerContext()
        with pytest.raises(RuntimeError, match=needle):
            await servicer._update_kb_config(_req(**over), ctx)
        assert ctx.aborted[0] == grpc.StatusCode.INVALID_ARGUMENT


async def test_update_config_idempotency_key_validation():
    servicer = KBServiceServicer()

    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._update_kb_config(_req(idempotency_key=""), ctx)
    assert ctx.aborted[0] == grpc.StatusCode.INVALID_ARGUMENT

    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._update_kb_config(_req(idempotency_key="not-a-uuid"), ctx)
    assert ctx.aborted[0] == grpc.StatusCode.INVALID_ARGUMENT


# ── servicer: failure audit ───────────────────────────────────────────────────


async def test_update_config_failure_audit_recorded():
    """An auditable business rejection (rebuilding gate, plan §6.3) records
    the kb.config.update failure audit in its own transaction — the wrapper
    reads the aborted code/details off the context."""
    conn = _ConfigConn(kb_row=_kb_row(status="rebuilding"))
    servicer = _make_servicer(conn)
    ctx = _AuditedContext()
    with pytest.raises(RuntimeError, match="FAILED_PRECONDITION"):
        await servicer._update_kb_config(_req(top_k=12), ctx)
    assert ctx.aborted is not None

    audits = [a for s, a in conn.fetchrow_calls if "INSERT INTO kb_audit_log" in s]
    assert len(audits) == 1  # only the failure row; the business tx aborted
    a = audits[0]
    assert a[3] == "kb.config.update"
    assert json.loads(a[4]) == {"kb_id": KB_ID}  # intent at abort time
    assert a[5] is None                          # no after_state on failure
    assert a[6] == "FAILED_PRECONDITION"
    assert "rebuilding" in a[7]


# ── repository: update_config_in_tx ───────────────────────────────────────────


async def test_update_config_in_tx_sql_and_rls():
    from app.repositories import knowledge_base as kb_repo

    conn = _ConfigConn()
    updated = await kb_repo.update_config_in_tx(
        conn,
        tenant_id=TENANT_ID,
        kb_id=KB_ID,
        patch={"top_k": 12, "retrieval_mode": "vector"},
    )
    assert updated["top_k"] == 12
    assert updated["retrieval_mode"] == "vector"

    sql, args = conn.fetchrow_calls[-1]
    assert "UPDATE knowledge_bases" in sql
    assert "SET top_k = $2" in sql
    assert "retrieval_mode = $3" in sql
    assert "updated_at = now()" in sql
    assert "WHERE id = $1 AND status <> 'deleted'" in sql
    assert "RETURNING" in sql
    assert args == (uuid.UUID(KB_ID), 12, "vector")
    # RLS context set before the UPDATE (no own transaction — the caller's
    # tx is the boundary).
    assert "SET LOCAL app.current_tenant_id" in conn.execute_calls[0][0]


async def test_update_config_in_tx_rejects_non_config_columns():
    from app.repositories import knowledge_base as kb_repo

    conn = _ConfigConn()
    with pytest.raises(ValueError, match="non-config columns"):
        await kb_repo.update_config_in_tx(
            conn, tenant_id=TENANT_ID, kb_id=KB_ID, patch={"name": "x"}
        )
    with pytest.raises(ValueError, match="patch must not be empty"):
        await kb_repo.update_config_in_tx(
            conn, tenant_id=TENANT_ID, kb_id=KB_ID, patch={}
        )
    # nothing was written.
    assert not any(
        "UPDATE knowledge_bases" in s for s, _ in conn.fetchrow_calls
    )


async def test_update_config_in_tx_row_missing_returns_none():
    """A kb_id not visible to this tenant (or soft-deleted) matches 0 rows
    → None; the caller maps that to NOT_FOUND."""
    from app.repositories import knowledge_base as kb_repo

    class _NoRowConn(_ConfigConn):
        async def fetchrow(self, sql, *args):
            self.fetchrow_calls.append((sql, args))
            if "UPDATE knowledge_bases" in sql and "RETURNING" in sql:
                return None
            return None

    conn = _NoRowConn()
    updated = await kb_repo.update_config_in_tx(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, patch={"top_k": 12}
    )
    assert updated is None
