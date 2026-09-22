"""Tests for the kb-service NATS rebuild consumer (P1 #24, B6).

Mirrors test_parse_consumer.py (mock NATS + pool + orchestrator, no real
services). Covers the B6 design decisions:

- D1 (per-doc reset): every eligible doc is reset via
  reset_for_reparse_in_tx immediately BEFORE process_document — the reset
  clears 'ready' so the orchestrator's ready-skip never fires (verified by
  asserting the reset UPDATE precedes each orchestrator call and that
  ready docs ARE parsed).
- D5 (serial): REBUILD_MAX_CONCURRENCY == 1.
- Task lifecycle: mark_running on pickup → set_progress after each doc
  (monotonic) → complete_task with the outcome.
- Terminal semantics: all-failed → status='failed'; any success →
  'completed' with failed_doc_ids in the result (partial failure visible).
- KB restore: the finally-side set_status_in_tx(rebuilding → active) runs
  even when a doc parse raises, and on the KB-not-found / no-vector-store
  early exits.
- Skips: docs deleted between snapshot and processing (vanished /
  mid-pipeline status / reset matched 0 rows) are skipped without failing
  the task.
- Payload validation: missing kb_id/tenant_id dropped; no task_id → runs
  without task tracking.
- start/stop lifecycle + build_rebuild_consumer factory.
- JetStream transport (B6 p2): start() ensures the ANI_TASKS WorkQueue
  stream (created once when absent, no-op when present) and binds a
  durable push consumer — queue == durable, ManualAck, AckWait 30m ==
  the async-task running lease, MaxDeliver 3, MaxAckPending 1. Ack
  semantics in _handle: invalid payload → Ack (poison pill), any normal
  return of process_message (success, failed close-out, duplicate
  gate-skip) → Ack, unhandled crash → neither Ack nor Nak (silent for
  ack_wait redelivery, aligned with the DB lease expiry so the
  redelivery takes over the orphan).
"""
import asyncio
import json
import os
import sys
import uuid
from contextlib import asynccontextmanager

import pytest
from nats.js.api import RetentionPolicy
from nats.js.errors import NotFoundError

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.consumers.rebuild_consumer import (
    REBUILD_ACK_WAIT,
    REBUILD_DURABLE,
    REBUILD_MAX_CONCURRENCY,
    REBUILD_MAX_DELIVER,
    RebuildConsumer,
    build_rebuild_consumer,
)

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC1 = "33333333-3333-3333-3333-333333333333"
DOC2 = "44444444-4444-4444-4444-444444444444"
DOC3 = "55555555-5555-5555-5555-555555555555"
OBJECT_ID = "66666666-6666-6666-6666-666666666666"
VECTOR_STORE_ID = "vs_kb_2222222222222222222222222"
SUBJECT = "ani.tasks.kb.rebuild.v1"


# ── Fakes ────────────────────────────────────────────────────────────────────


class _FakeSubscription:
    def __init__(self):
        self.unsubscribed = False

    async def unsubscribe(self):
        self.unsubscribed = True


class _FakeJS:
    """JetStream context mock: stream_info (absent until add_stream) +
    subscribe capturing the durable-binding kwargs."""

    def __init__(self):
        self.stream_info_calls: list[str] = []
        self.streams: list[dict] = []   # add_stream payloads
        self.subscriptions: list[dict] = []
        self._subscription_objs: list[_FakeSubscription] = []

    async def stream_info(self, name: str):
        self.stream_info_calls.append(name)
        if not any(s["name"] == name for s in self.streams):
            raise NotFoundError()
        return {"name": name}

    async def add_stream(self, config):
        self.streams.append(
            {
                "name": config.name,
                "subjects": list(config.subjects),
                "retention": config.retention,
                "max_age": config.max_age,
            }
        )

    async def subscribe(self, subject, queue=None, cb=None, durable=None,
                        manual_ack=False, config=None):
        sub = _FakeSubscription()
        self.subscriptions.append(
            {
                "subject": subject,
                "queue": queue,
                "cb": cb,
                "durable": durable,
                "manual_ack": manual_ack,
                "config": config,
            }
        )
        self._subscription_objs.append(sub)
        return sub


class _FakeNATS:
    def __init__(self):
        self.js = _FakeJS()

    def jetstream(self):
        return self.js


class _FakeMsg:
    """JetStream Msg mock: data + recorded ack/nak/in_progress calls."""

    def __init__(self, data: dict | bytes):
        if isinstance(data, bytes):
            self.data = data
        else:
            self.data = json.dumps(data).encode("utf-8")
        self.events: list[str] = []  # "ack" / "nak" / "in_progress"

    async def ack(self):
        self.events.append("ack")

    async def nak(self, delay=None):
        self.events.append("nak")

    async def in_progress(self):
        self.events.append("in_progress")


class _FakeOrchestrator:
    """Records process_document calls; raises per doc_id via raise_for."""

    def __init__(self):
        self.calls: list[dict] = []
        self.raise_for: dict[str, Exception] = {}

    async def process_document(self, **kwargs):
        self.calls.append(kwargs)
        exc = self.raise_for.get(kwargs["doc_id"])
        if exc is not None:
            raise exc


class _RebuildMockConn:
    """Minimal asyncpg.Connection mock for the rebuild consumer flow.

    ``doc_rows`` maps doc_id → row returned by get_document (fetchrow on
    kb_documents); ``doc_ids`` feeds list_documents_for_rebuild (fetch).
    execute() records every statement and returns "UPDATE 1" for the
    async_tasks / kb_documents / knowledge_bases UPDATEs.
    """

    def __init__(
        self,
        *,
        kb_row: dict | None,
        doc_rows: dict[str, dict] | None = None,
        doc_ids: list[str] | None = None,
    ):
        self._kb_row = kb_row
        self._doc_rows = doc_rows or {}
        self._doc_ids = doc_ids or []
        self.executes: list[tuple[str, tuple]] = []
        self.fetches: list[tuple[str, tuple]] = []
        self.in_tx = False
        self.set_local_outside_tx: str | None = None

    @asynccontextmanager
    async def transaction(self):
        self.in_tx = True
        try:
            yield
        finally:
            self.in_tx = False

    async def fetchrow(self, sql, *args):
        # Order matters: get_kb embeds a "FROM kb_documents" subquery
        # (live doc_count), so check "FROM knowledge_bases" first.
        if "FROM knowledge_bases" in sql:
            return self._kb_row
        if "FROM kb_documents" in sql:
            # get_document binds (doc_id, kb_id) — resolve via either
            # positional arg that matches a known doc row.
            for doc_id in self._doc_rows:
                if uuid.UUID(doc_id) in args or doc_id in args:
                    return self._doc_rows[doc_id]
            return None
        return None

    async def fetch(self, sql, *args):
        self.fetches.append((sql, args))
        if "FROM kb_documents" in sql and "parse_status IN" in sql:
            return [{"id": uuid.UUID(d)} for d in self._doc_ids]
        return []

    async def execute(self, sql, *args):
        self.executes.append((sql, args))
        if sql.startswith("SET LOCAL"):
            # Real PG: SET LOCAL outside a tx block raises WARNING 25P01
            # and the setting is NOT applied. The mock mirrors that by
            # recording the violation so tests can assert tx-scoped use.
            if not self.in_tx:
                self.set_local_outside_tx = sql
            return None
        if (
            "UPDATE async_tasks" in sql
            or "UPDATE kb_documents" in sql
            or "UPDATE knowledge_bases" in sql
        ):
            return "UPDATE 1"
        return None


class _RebuildMockPool:
    """Returns _RebuildMockConn instances from acquire()."""

    def __init__(self, *, kb_row=None, doc_rows=None, doc_ids=None):
        self._kb_row = kb_row
        self._doc_rows = doc_rows
        self._doc_ids = doc_ids
        self.conns: list[_RebuildMockConn] = []

    @asynccontextmanager
    async def acquire(self):
        conn = _RebuildMockConn(
            kb_row=self._kb_row,
            doc_rows=self._doc_rows,
            doc_ids=self._doc_ids,
        )
        self.conns.append(conn)
        yield conn


def _kb_row(**over):
    row = {
        "id": KB_ID,
        "tenant_id": TENANT_ID,
        "vector_store_id": VECTOR_STORE_ID,
        "embedding_model": "bge-m3",
        "chunk_size": 512,
    }
    row.update(over)
    return row


def _doc_row(doc_id=DOC1, parse_status="ready", **over):
    row = {
        "id": doc_id,
        "kb_id": KB_ID,
        "tenant_id": TENANT_ID,
        "parse_status": parse_status,
        "object_id": OBJECT_ID,
        "file_name": "a.pdf",
        "file_type": "pdf",
        "chunk_count": 3,
    }
    row.update(over)
    return row


def _payload(**over):
    base = {
        "kb_id": KB_ID,
        "tenant_id": TENANT_ID,
        "task_id": str(uuid.uuid4()),
    }
    base.update(over)
    return base


def _make_consumer(pool, orchestrator, **over):
    defaults = dict(
        nats_client=_FakeNATS(),
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT,
    )
    defaults.update(over)
    return RebuildConsumer(**defaults)


def _all_executes(pool):
    return [e for conn in pool.conns for e in conn.executes]


# ── constants / factory ──────────────────────────────────────────────────────


def test_rebuild_max_concurrency_is_serial():
    """D5: rebuilds are serial — one document at a time."""
    assert REBUILD_MAX_CONCURRENCY == 1


def test_build_rebuild_consumer_returns_consumer():
    pool = _RebuildMockPool(kb_row=_kb_row())
    consumer = build_rebuild_consumer(
        nats_client=_FakeNATS(),
        db_pool=pool,
        orchestrator=_FakeOrchestrator(),
        subject=SUBJECT,
    )
    assert isinstance(consumer, RebuildConsumer)
    assert consumer._subject == SUBJECT


# ── start/stop lifecycle (JetStream transport) ───────────────────────────────


@pytest.mark.asyncio
async def test_start_ensures_stream_and_binds_durable():
    """start() creates the ANI_TASKS WorkQueue stream when absent and
    binds a durable push consumer: queue == durable (nats-py rule),
    ManualAck, AckWait 30m (== the async-task running lease), MaxDeliver
    3, MaxAckPending == the serial concurrency."""
    nats = _FakeNATS()
    consumer = _make_consumer(
        _RebuildMockPool(kb_row=_kb_row()), _FakeOrchestrator(), nats_client=nats,
    )
    await consumer.start()

    js = nats.js
    # ANI_TASKS created with WorkQueue retention and the ani.tasks.> filter.
    assert js.stream_info_calls == ["ANI_TASKS"]
    assert len(js.streams) == 1
    stream = js.streams[0]
    assert stream["name"] == "ANI_TASKS"
    assert stream["subjects"] == ["ani.tasks.>"]
    assert stream["retention"] == RetentionPolicy.WORK_QUEUE

    # Durable push binding.
    assert len(js.subscriptions) == 1
    sub = js.subscriptions[0]
    assert sub["subject"] == SUBJECT
    assert sub["queue"] == REBUILD_DURABLE
    assert sub["durable"] == REBUILD_DURABLE
    assert sub["cb"] == consumer._on_msg
    assert sub["manual_ack"] is True
    cfg = sub["config"]
    assert cfg.durable_name == REBUILD_DURABLE
    assert cfg.filter_subject == SUBJECT
    assert cfg.ack_wait == REBUILD_ACK_WAIT
    assert cfg.max_deliver == REBUILD_MAX_DELIVER
    assert cfg.max_ack_pending == REBUILD_MAX_CONCURRENCY
    await consumer.stop()


@pytest.mark.asyncio
async def test_start_stream_ensure_is_idempotent():
    """A pre-existing ANI_TASKS stream (production: created by the Go
    bootstrap) makes start() a pure no-op ensure — no add_stream."""
    nats = _FakeNATS()
    # Pre-seed the stream so stream_info succeeds on the first call.
    nats.js.streams.append({"name": "ANI_TASKS", "subjects": ["ani.tasks.>"],
                            "retention": "workqueue", "max_age": 86400})
    consumer = _make_consumer(
        _RebuildMockPool(kb_row=_kb_row()), _FakeOrchestrator(), nats_client=nats,
    )
    await consumer.start()
    assert len(nats.js.streams) == 1  # nothing re-added
    await consumer.stop()


@pytest.mark.asyncio
async def test_stop_unsubscribes():
    nats = _FakeNATS()
    consumer = _make_consumer(
        _RebuildMockPool(kb_row=_kb_row()), _FakeOrchestrator(), nats_client=nats,
    )
    await consumer.start()
    await consumer.stop()
    assert nats.js._subscription_objs[-1].unsubscribed


# ── _handle: JetStream ack semantics ────────────────────────────────────────


@pytest.mark.asyncio
async def test_handle_acks_on_success():
    """A completed rebuild (even one that closes the task 'failed') is
    Acked — the task row is the source of truth; a redelivery would
    just gate-skip."""
    pool = _RebuildMockPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1)},
        doc_ids=[DOC1],
    )
    consumer = _make_consumer(pool, _FakeOrchestrator())
    await consumer.start()
    msg = _FakeMsg(_payload())
    await consumer._handle(msg)
    assert msg.events == ["ack"]
    await consumer.stop()


@pytest.mark.asyncio
async def test_handle_acks_invalid_payload():
    """Invalid JSON is a poison pill: Ack it (it can never succeed on
    redelivery); the durable outbox row keeps the audit trail."""
    pool = _RebuildMockPool(kb_row=_kb_row())
    consumer = _make_consumer(pool, _FakeOrchestrator())
    await consumer.start()
    msg = _FakeMsg(b"not-json{")
    await consumer._handle(msg)
    assert msg.events == ["ack"]
    # No DB round-trip — the payload never reached process_message.
    assert pool.conns == []
    await consumer.stop()


@pytest.mark.asyncio
async def test_handle_acks_non_dict_json_payload():
    """Valid JSON that is not an object (e.g. a bare array) is the same
    poison pill: Ack it — letting it through to ``payload.get`` would
    raise AttributeError and land in the crash-silent branch, wasting
    MaxDeliver redeliveries on a message that can never parse."""
    pool = _RebuildMockPool(kb_row=_kb_row())
    consumer = _make_consumer(pool, _FakeOrchestrator())
    await consumer.start()
    msg = _FakeMsg(b"[1, 2, 3]")
    await consumer._handle(msg)
    assert msg.events == ["ack"]
    assert pool.conns == []
    await consumer.stop()


@pytest.mark.asyncio
async def test_handle_crash_is_silent_no_ack_no_nak():
    """An unhandled crash leaves the message unacked and un-nak'd:
    JetStream redelivers after ack_wait (30 min) — aligned with the
    task row's running-lease expiry, so the redelivery takes over the
    orphan via mark_running's expired-lease path. A Nak would redeliver
    immediately, hit the still-live lease, gate-skip and Ack —
    stranding the message with the task stuck running."""
    class _CrashPool(_RebuildMockPool):
        @asynccontextmanager
        async def acquire(self):
            conn = _RebuildMockConn(
                kb_row=self._kb_row,
                doc_rows=self._doc_rows,
                doc_ids=self._doc_ids,
            )
            orig = conn.execute

            async def execute(sql, *args):
                # Crash on the doc reset (outside the per-doc try, so the
                # exception escapes process_message after the finally-side
                # KB restore). mark_running above returned UPDATE 1 — the
                # task row is now running with a fresh lease.
                if "UPDATE kb_documents" in sql and "parse_status = 'pending'" in sql:
                    raise RuntimeError("db crash mid-rebuild")
                return await orig(sql, *args)

            conn.execute = execute
            self.conns.append(conn)
            yield conn

    pool = _CrashPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1)},
        doc_ids=[DOC1],
    )
    consumer = _make_consumer(pool, _FakeOrchestrator())
    await consumer.start()
    msg = _FakeMsg(_payload())
    # The crash is contained by _handle (logged, not raised) — the
    # consumer stays alive for the next delivery.
    await consumer._handle(msg)
    assert msg.events == []  # neither ack nor nak — silent for ack_wait
    await consumer.stop()


@pytest.mark.asyncio
async def test_handle_heartbeat_renewed_then_cancelled():
    """The InProgress heartbeat is cancelled once the outcome is final —
    on crash paths that is what lets JetStream schedule the ack_wait
    redelivery at all (a stray renewal would keep pushing it out)."""
    pool = _RebuildMockPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1)},
        doc_ids=[DOC1],
    )
    consumer = _make_consumer(pool, _FakeOrchestrator())
    await consumer.start()
    msg = _FakeMsg(_payload())
    await consumer._handle(msg)
    # ack_wait/3 == 600s — far beyond the test run, so the loop never
    # fired in_progress; only the final ack happened, and no heartbeat
    # task was left behind.
    assert msg.events == ["ack"]
    assert not any(
        t.get_coro().__name__ == "heartbeat_loop"
        for t in asyncio.all_tasks()
    )
    await consumer.stop()


@pytest.mark.asyncio
async def test_on_msg_spawns_tracked_task():
    """_on_msg spawns the handler as a tracked task so stop() can drain
    it; after drain the pending set is empty."""
    pool = _RebuildMockPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1)},
        doc_ids=[DOC1],
    )
    consumer = _make_consumer(pool, _FakeOrchestrator())
    await consumer.start()
    msg = _FakeMsg(_payload())
    await consumer._on_msg(msg)
    await consumer.stop()  # drains the spawned task
    # The done-callback (pending.discard) is call_soon-scheduled when the
    # task completes — yield once so it fires before asserting.
    await asyncio.sleep(0)
    assert consumer._pending == set()
    assert msg.events == ["ack"]  # handler ran to completion during drain


# ── process_message: task lifecycle ──────────────────────────────────────────


@pytest.mark.asyncio
async def test_process_message_full_lifecycle_two_docs():
    """Two eligible (ready) docs: mark_running → per-doc reset + parse →
    monotonic set_progress → finally KB restore → complete_task."""
    pool = _RebuildMockPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1), DOC2: _doc_row(DOC2)},
        doc_ids=[DOC1, DOC2],
    )
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)
    task_id = str(uuid.uuid4())

    await consumer.process_message(_payload(task_id=task_id))

    # Both docs parsed (ready docs re-parsed — that's the rebuild point).
    assert [c["doc_id"] for c in orchestrator.calls] == [DOC1, DOC2]
    call = orchestrator.calls[0]
    assert call["tenant_id"] == TENANT_ID
    assert call["kb_id"] == KB_ID
    assert call["object_id"] == OBJECT_ID
    assert call["file_name"] == "a.pdf"
    assert call["file_type"] == "pdf"
    assert call["chunk_size"] == 512
    assert call["vector_store_id"] == VECTOR_STORE_ID
    assert call["embedding_model"] == "bge-m3"

    executes = _all_executes(pool)
    sqls = [s for s, _ in executes]

    # mark_running ran on pickup (conditional pending → running). Filter on
    # started_at — set_progress's WHERE also mentions status='running'.
    assert len([s for s in sqls if "started_at = now()" in s]) == 1

    # Each doc was reset before its parse (D1): reset UPDATE present for
    # both docs.
    reset_calls = [
        (s, a) for s, a in executes
        if "UPDATE kb_documents" in s and "parse_status = 'pending'" in s
    ]
    assert len(reset_calls) == 2
    assert {a[0] for _, a in reset_calls} == {uuid.UUID(DOC1), uuid.UUID(DOC2)}

    # Monotonic progress after each doc: 50 then 100 (2 docs).
    progress_calls = [
        (s, a) for s, a in executes if "GREATEST($2, progress_pct)" in s
    ]
    assert [a[1] for _, a in progress_calls] == [50, 100]

    # finally: KB restored rebuilding → active.
    restore_calls = [
        (s, a) for s, a in executes
        if "UPDATE knowledge_bases" in s and "status = $2" in s
    ]
    assert len(restore_calls) == 1
    assert restore_calls[0][1] == (uuid.UUID(KB_ID), "active", "rebuilding")

    # Every SET LOCAL (RLS tenant context) ran inside a transaction
    # block — no WARNING 25P01 / silently-unset RLS in real PG.
    assert all(
        conn.set_local_outside_tx is None for conn in pool.conns
    )

    # Terminal state: completed with counts (best-effort UPDATE async_tasks).
    complete_calls = [
        (s, a) for s, a in executes
        if "UPDATE async_tasks" in s and "completed_at = now()" in s
    ]
    assert len(complete_calls) == 1
    sql, args = complete_calls[0]
    assert args[0] == uuid.UUID(task_id)
    assert args[1] == "completed"
    result = json.loads(args[2])
    assert result == {"total": 2, "succeeded": 2, "failed_doc_ids": []}


@pytest.mark.asyncio
async def test_process_message_no_task_id_runs_without_tracking():
    """Outbox event without a task ref: the rebuild still runs, only
    progress/close-out are skipped."""
    pool = _RebuildMockPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1)},
        doc_ids=[DOC1],
    )
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)

    await consumer.process_message(_payload(task_id=""))

    assert len(orchestrator.calls) == 1
    executes = _all_executes(pool)
    # No async_tasks statements at all (no mark_running/progress/complete).
    assert not any("UPDATE async_tasks" in s for s, _ in executes)
    # KB still restored to active.
    assert any(
        "UPDATE knowledge_bases" in s for s, _ in executes
    )


# ── process_message: guards ───────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_process_message_non_pending_task_skips_rebuild():
    """mark_running returns False (task already running/completed — a
    redelivered outbox event): the rebuild is skipped entirely. No KB
    status transition, no documents parsed, no KB reads at all — the
    idempotency gate fires before the try/finally so the duplicate
    delivery can neither double-rebuild nor flip the KB status."""
    class _NotPendingPool(_RebuildMockPool):
        @asynccontextmanager
        async def acquire(self):
            conn = _RebuildMockConn(
                kb_row=self._kb_row,
                doc_rows=self._doc_rows,
                doc_ids=self._doc_ids,
            )
            orig = conn.execute

            async def execute(sql, *args):
                if (
                    "UPDATE async_tasks" in sql
                    and "started_at = now()" in sql
                ):
                    conn.executes.append((sql, args))
                    return "UPDATE 0"  # task not pending
                return await orig(sql, *args)

            conn.execute = execute
            self.conns.append(conn)
            yield conn

    pool = _NotPendingPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1)},
        doc_ids=[DOC1],
    )
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)

    await consumer.process_message(_payload())

    # Duplicate delivery: nothing ran past the gate.
    assert orchestrator.calls == []
    # Only the mark_running statement ran — no doc snapshot (fetch),
    # no KB restore, no progress/complete.
    assert not any(
        "UPDATE knowledge_bases" in s for s, _ in _all_executes(pool)
    )
    assert not any(
        "UPDATE async_tasks" in s and "GREATEST($2, progress_pct)" in s
        for s, _ in _all_executes(pool)
    )
    assert not pool.conns[0].fetches  # list_documents never ran


@pytest.mark.asyncio
async def test_process_message_missing_fields_dropped():
    pool = _RebuildMockPool(kb_row=_kb_row())
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)

    await consumer.process_message({"kb_id": KB_ID})           # no tenant
    await consumer.process_message({"tenant_id": TENANT_ID})    # no kb
    await consumer.process_message({})                          # neither

    assert orchestrator.calls == []
    assert pool.conns == []  # no DB round-trip at all


@pytest.mark.asyncio
async def test_process_message_kb_missing_fails_task():
    """KB vanished before the consumer ran: task closed as failed, no
    restore attempt writes anything meaningful (UPDATE 0 in real DB)."""
    pool = _RebuildMockPool(kb_row=None)
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)
    task_id = str(uuid.uuid4())

    await consumer.process_message(_payload(task_id=task_id))

    assert orchestrator.calls == []
    executes = _all_executes(pool)
    complete_calls = [
        (s, a) for s, a in executes
        if "UPDATE async_tasks" in s and "completed_at = now()" in s
    ]
    assert len(complete_calls) == 1
    assert complete_calls[0][1][1] == "failed"
    result = json.loads(complete_calls[0][1][2])
    assert result["error"] == "knowledge base not found"


@pytest.mark.asyncio
async def test_process_message_kb_without_vector_store_fails_task():
    pool = _RebuildMockPool(kb_row=_kb_row(vector_store_id=None))
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)
    task_id = str(uuid.uuid4())

    await consumer.process_message(_payload(task_id=task_id))

    assert orchestrator.calls == []
    executes = _all_executes(pool)
    complete_calls = [
        (s, a) for s, a in executes
        if "UPDATE async_tasks" in s and "completed_at = now()" in s
    ]
    assert complete_calls[0][1][1] == "failed"
    result = json.loads(complete_calls[0][1][2])
    assert result["error"] == "kb has no vector_store_id"


# ── process_message: per-doc skips ────────────────────────────────────────────


@pytest.mark.asyncio
async def test_process_message_skips_vanished_and_mid_pipeline_docs():
    """Snapshot-time races: a doc deleted between snapshot and processing
    (vanished) and a doc mid-pipeline (parsing) are both skipped — neither
    parsed nor failed, and the doc that IS eligible still parses."""
    pool = _RebuildMockPool(
        kb_row=_kb_row(),
        doc_rows={
            DOC1: _doc_row(DOC1),                        # eligible
            DOC2: None,                                  # vanished
            DOC3: _doc_row(DOC3, parse_status="parsing"),  # mid-pipeline
        },
        doc_ids=[DOC1, DOC2, DOC3],
    )
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)

    await consumer.process_message(_payload())

    assert [c["doc_id"] for c in orchestrator.calls] == [DOC1]
    # Only the eligible doc was reset.
    reset_calls = [
        a for s, a in _all_executes(pool)
        if "UPDATE kb_documents" in s and "parse_status = 'pending'" in s
    ]
    assert [a[0] for a in reset_calls] == [uuid.UUID(DOC1)]


@pytest.mark.asyncio
async def test_process_message_reset_zero_rows_skips_doc():
    """Reset matched 0 rows (soft-deleted between the status re-check and
    the reset): doc skipped, no parse."""
    class _ZeroResetPool(_RebuildMockPool):
        def __init__(self, **kw):
            super().__init__(**kw)

        @asynccontextmanager
        async def acquire(self):
            conn = _RebuildMockConn(
                kb_row=self._kb_row,
                doc_rows=self._doc_rows,
                doc_ids=self._doc_ids,
            )
            orig = conn.execute

            async def execute(sql, *args):
                if "UPDATE kb_documents" in sql and "parse_status = 'pending'" in sql:
                    conn.executes.append((sql, args))
                    return "UPDATE 0"
                return await orig(sql, *args)

            conn.execute = execute
            self.conns.append(conn)
            yield conn

    pool = _ZeroResetPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1)},
        doc_ids=[DOC1],
    )
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)

    await consumer.process_message(_payload())

    assert orchestrator.calls == []
    # Task still completed (nothing failed — the doc just was not eligible).
    completes = [
        a for s, a in _all_executes(pool)
        if "UPDATE async_tasks" in s and "completed_at = now()" in s
    ]
    assert completes[0][1] == "completed"


# ── process_message: per-doc failure isolation ────────────────────────────────


@pytest.mark.asyncio
async def test_process_message_partial_failure_completes_with_failed_ids():
    """One doc raises: the other still parses; task completes (any success)
    with failed_doc_ids visible in the result."""
    pool = _RebuildMockPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1), DOC2: _doc_row(DOC2)},
        doc_ids=[DOC1, DOC2],
    )
    orchestrator = _FakeOrchestrator()
    orchestrator.raise_for[DOC1] = RuntimeError("rag-engine unreachable")
    consumer = _make_consumer(pool, orchestrator)

    await consumer.process_message(_payload())

    assert [c["doc_id"] for c in orchestrator.calls] == [DOC1, DOC2]
    executes = _all_executes(pool)
    complete_calls = [
        (s, a) for s, a in executes
        if "UPDATE async_tasks" in s and "completed_at = now()" in s
    ]
    assert complete_calls[0][1][1] == "completed"
    result = json.loads(complete_calls[0][1][2])
    assert result == {"total": 2, "succeeded": 1, "failed_doc_ids": [DOC1]}
    # KB restored despite the doc failure.
    assert any(
        "UPDATE knowledge_bases" in s for s, _ in executes
    )


@pytest.mark.asyncio
async def test_process_message_all_failed_fails_task():
    """Every doc fails → status='failed' (client retries with a fresh key)."""
    pool = _RebuildMockPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1), DOC2: _doc_row(DOC2)},
        doc_ids=[DOC1, DOC2],
    )
    orchestrator = _FakeOrchestrator()
    orchestrator.raise_for[DOC1] = RuntimeError("boom1")
    orchestrator.raise_for[DOC2] = RuntimeError("boom2")
    consumer = _make_consumer(pool, orchestrator)

    await consumer.process_message(_payload())

    assert len(orchestrator.calls) == 2
    executes = _all_executes(pool)
    complete_calls = [
        (s, a) for s, a in executes
        if "UPDATE async_tasks" in s and "completed_at = now()" in s
    ]
    assert complete_calls[0][1][1] == "failed"
    result = json.loads(complete_calls[0][1][2])
    assert result["succeeded"] == 0
    assert result["failed_doc_ids"] == [DOC1, DOC2]


@pytest.mark.asyncio
async def test_process_message_restores_kb_when_orchestrator_crashes():
    """An unexpected exception mid-doc OUTSIDE the per-doc try (here: the
    reset UPDATE blows up) still triggers the finally-side KB restore —
    the KB must never stay locked. Orchestrator crashes inside the try
    are isolated per-doc (covered above), so the crash point is a repo
    statement failing."""
    class _CrashPool(_RebuildMockPool):
        @asynccontextmanager
        async def acquire(self):
            conn = _RebuildMockConn(
                kb_row=self._kb_row,
                doc_rows=self._doc_rows,
                doc_ids=self._doc_ids,
            )
            orig = conn.execute

            async def execute(sql, *args):
                if (
                    "UPDATE kb_documents" in sql
                    and "parse_status = 'pending'" in sql
                ):
                    raise RuntimeError("consumer crash")
                return await orig(sql, *args)

            conn.execute = execute
            self.conns.append(conn)
            yield conn

    pool = _CrashPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1)},
        doc_ids=[DOC1],
    )
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)

    # The crash escapes process_message (task-level failure) but the
    # finally already ran the restore.
    with pytest.raises(RuntimeError, match="consumer crash"):
        await consumer.process_message(_payload())

    executes = _all_executes(pool)
    restore_calls = [
        (s, a) for s, a in executes
        if "UPDATE knowledge_bases" in s and "status = $2" in s
    ]
    assert len(restore_calls) == 1
    assert restore_calls[0][1] == (uuid.UUID(KB_ID), "active", "rebuilding")
    # The doc never reached the orchestrator.
    assert orchestrator.calls == []


@pytest.mark.asyncio
async def test_process_message_failed_docs_are_eligible():
    """The rebuild scope includes failed docs (ready AND failed) — a failed
    doc is reset and re-parsed, not skipped (mirrors the repository's
    parse_status IN ('ready','failed') scope)."""
    pool = _RebuildMockPool(
        kb_row=_kb_row(),
        doc_rows={DOC1: _doc_row(DOC1, parse_status="failed", error_message="ocr fail")},
        doc_ids=[DOC1],
    )
    orchestrator = _FakeOrchestrator()
    consumer = _make_consumer(pool, orchestrator)

    await consumer.process_message(_payload())

    assert len(orchestrator.calls) == 1
    assert orchestrator.calls[0]["doc_id"] == DOC1
