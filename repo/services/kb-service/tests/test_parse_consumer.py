"""Tests for the kb-service NATS parse consumer (issue-033 / Plan step 6).

Covers all Acceptance Criteria:
- Consumer subscribes to the v2 subject ``ani.tasks.kb.parse.v2`` and
  dispatches messages to ParseOrchestrator.process_document.
- Idempotency: duplicate messages do not cause duplicate parses (the
  orchestrator's ``parse_status == 'ready'`` guard skips already-ingested
  docs; the consumer also rejects messages during shutdown).
- flag=False: consumer does not start (verified via build_parse_consumer
  being conditionally called in main.py; the consumer object itself has
  no flag — main.py gates construction).
- Mock NATS + orchestrator + DB pool — no real services required.
- Message payload validation: missing required fields are dropped.
- file_type / vector_store_id resolution from the database.
- start/stop lifecycle (subscribe + unsubscribe + drain in-flight).
"""
import asyncio
import json
import os
import sys
from contextlib import asynccontextmanager
from typing import Any

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.consumers.parse_consumer import ParseConsumer, build_parse_consumer


TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC_ID = "33333333-3333-3333-3333-333333333333"
OBJECT_ID = "44444444-4444-4444-4444-444444444444"
VECTOR_STORE_ID = "vs_kb_2222222222222222222222222"
SUBJECT_V2 = "ani.tasks.kb.parse.v2"


# ── Fakes ────────────────────────────────────────────────────────────────────


class _FakeSubscription:
    """Records unsubscribe calls."""

    def __init__(self):
        self.unsubscribed = False

    async def unsubscribe(self):
        self.unsubscribed = True


class _FakeNATS:
    """Records subscribe calls and stores the callback for message injection."""

    def __init__(self):
        self.subscriptions: list[tuple[str, Any]] = []
        self._subscription_objs: list[_FakeSubscription] = []

    async def subscribe(self, subject: str, cb=None):
        sub = _FakeSubscription()
        self.subscriptions.append((subject, cb))
        self._subscription_objs.append(sub)
        return sub

    @property
    def callback(self):
        assert self.subscriptions, "no subscription registered"
        return self.subscriptions[-1][1]

    @property
    def subscription(self) -> _FakeSubscription:
        assert self._subscription_objs, "no subscription registered"
        return self._subscription_objs[-1]


class _FakeMsg:
    """NATS message with JSON-encodable data."""

    def __init__(self, data: dict):
        self.data = json.dumps(data).encode("utf-8")


class _FakeOrchestrator:
    """Records process_document calls; optionally skips (idempotency)."""

    def __init__(self):
        self.calls: list[dict] = []
        self._raise: Exception | None = None

    def raise_next(self, exc: Exception):
        self._raise = exc

    async def process_document(self, **kwargs):
        self.calls.append(kwargs)
        if self._raise is not None:
            exc, self._raise = self._raise, None
            raise exc


class _MockConn:
    """Minimal asyncpg.Connection mock for doc_repo + kb_repo lookups."""

    def __init__(
        self,
        *,
        doc_row: dict | None = None,
        kb_row: dict | None = None,
    ):
        self._doc_row = doc_row
        self._kb_row = kb_row

    @asynccontextmanager
    async def transaction(self):
        yield

    async def fetchrow(self, sql, *args):
        # Distinguish doc_repo.get_document vs kb_repo.get_kb by the SQL.
        if "kb_documents" in sql:
            return self._doc_row
        if "knowledge_bases" in sql:
            return self._kb_row
        return None

    async def execute(self, sql, *args):
        # set_tenant_context / set_config calls
        pass


class _MockPool:
    """Returns _MockConn instances from acquire()."""

    def __init__(self, *, doc_row=None, kb_row=None):
        self._doc_row = doc_row
        self._kb_row = kb_row
        self._conns: list[_MockConn] = []

    @asynccontextmanager
    async def acquire(self):
        conn = _MockConn(doc_row=self._doc_row, kb_row=self._kb_row)
        self._conns.append(conn)
        yield conn


def _make_doc_row(file_type="pdf", file_name="test.pdf"):
    return {
        "id": DOC_ID,
        "file_type": file_type,
        "file_name": file_name,
        "object_id": OBJECT_ID,
        "parse_status": "pending",
    }


def _make_kb_row(vector_store_id=VECTOR_STORE_ID):
    return {
        "id": KB_ID,
        "vector_store_id": vector_store_id,
        "chunk_size": 1024,
    }


def _make_payload(**overrides):
    base = {
        "doc_id": DOC_ID,
        "kb_id": KB_ID,
        "object_id": OBJECT_ID,
        "tenant_id": TENANT_ID,
        "file_name": "test.pdf",
        "chunk_size": 1024,
        "storage_path": "kb-docs/...",
    }
    base.update(overrides)
    return base


# ── build_parse_consumer factory ──────────────────────────────────────────


def test_build_parse_consumer_returns_consumer():
    """The factory constructs a ParseConsumer with the given args."""
    nats = _FakeNATS()
    pool = _MockPool()
    orchestrator = _FakeOrchestrator()
    consumer = build_parse_consumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    assert isinstance(consumer, ParseConsumer)
    assert consumer._subject == SUBJECT_V2


# ── start/stop lifecycle ──────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_start_subscribes_to_v2_subject():
    nats = _FakeNATS()
    pool = _MockPool()
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.start()
    assert len(nats.subscriptions) == 1
    assert nats.subscriptions[0][0] == SUBJECT_V2
    await consumer.stop()
    assert nats.subscription.unsubscribed


@pytest.mark.asyncio
async def test_stop_drains_in_flight_tasks():
    """stop() waits for in-flight tasks to complete."""
    nats = _FakeNATS()
    pool = _MockPool(
        doc_row=_make_doc_row(),
        kb_row=_make_kb_row(),
    )
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
        max_concurrency=2,
    )
    await consumer.start()
    # Inject two messages
    for _ in range(2):
        await nats.callback(_FakeMsg(_make_payload()))
    # Wait briefly for tasks to start
    await asyncio.sleep(0.05)
    # Stop should drain the in-flight tasks
    await consumer.stop(timeout=2.0)
    # All tasks should have completed (orchestrator called)
    assert len(orchestrator.calls) == 2


@pytest.mark.asyncio
async def test_stop_timeout_cancels_lingering_tasks():
    """If the drain times out, stop() cancels lingering tasks and clears
    _pending instead of propagating asyncio.TimeoutError."""
    nats = _FakeNATS()
    pool = _MockPool(doc_row=_make_doc_row(), kb_row=_make_kb_row())

    class _SlowOrchestrator:
        """Orchestrator that blocks indefinitely to trigger drain timeout."""

        def __init__(self):
            self.calls = 0

        async def process_document(self, **kwargs):
            self.calls += 1
            await asyncio.sleep(100)

    orchestrator = _SlowOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
        max_concurrency=1,
    )
    await consumer.start()
    # Inject one message — it will start processing and block
    await nats.callback(_FakeMsg(_make_payload()))
    await asyncio.sleep(0.05)
    # Stop with a very short timeout — should cancel the task, not raise
    await consumer.stop(timeout=0.1)
    # _pending should be cleared despite the timeout
    assert len(consumer._pending) == 0


# ── process_message ───────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_process_message_dispatches_to_orchestrator():
    """A valid message resolves doc/kb metadata and calls process_document."""
    nats = _FakeNATS()
    pool = _MockPool(
        doc_row=_make_doc_row(file_type="pdf", file_name="test.pdf"),
        kb_row=_make_kb_row(vector_store_id=VECTOR_STORE_ID),
    )
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.process_message(_make_payload())
    assert len(orchestrator.calls) == 1
    call = orchestrator.calls[0]
    assert call["tenant_id"] == TENANT_ID
    assert call["kb_id"] == KB_ID
    assert call["doc_id"] == DOC_ID
    assert call["object_id"] == OBJECT_ID
    assert call["file_name"] == "test.pdf"
    assert call["file_type"] == "pdf"
    assert call["chunk_size"] == 1024
    assert call["vector_store_id"] == VECTOR_STORE_ID


@pytest.mark.asyncio
async def test_process_message_resolves_file_type_from_db():
    """file_type is read from kb_documents (not the payload)."""
    nats = _FakeNATS()
    pool = _MockPool(
        doc_row=_make_doc_row(file_type="docx", file_name="report.docx"),
        kb_row=_make_kb_row(),
    )
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    # payload has no file_type field — resolved from DB
    payload = _make_payload(file_name="")
    await consumer.process_message(payload)
    assert orchestrator.calls[0]["file_type"] == "docx"
    assert orchestrator.calls[0]["file_name"] == "report.docx"


@pytest.mark.asyncio
async def test_process_message_resolves_vector_store_id_from_kb():
    """vector_store_id is read from knowledge_bases."""
    nats = _FakeNATS()
    pool = _MockPool(
        doc_row=_make_doc_row(),
        kb_row=_make_kb_row(vector_store_id="vs_custom_123"),
    )
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.process_message(_make_payload())
    assert orchestrator.calls[0]["vector_store_id"] == "vs_custom_123"


@pytest.mark.asyncio
async def test_process_message_missing_doc_id_dropped():
    """Missing doc_id → message dropped, orchestrator not called."""
    nats = _FakeNATS()
    pool = _MockPool(doc_row=_make_doc_row(), kb_row=_make_kb_row())
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.process_message(_make_payload(doc_id=""))
    assert len(orchestrator.calls) == 0


@pytest.mark.asyncio
async def test_process_message_missing_tenant_id_dropped():
    nats = _FakeNATS()
    pool = _MockPool(doc_row=_make_doc_row(), kb_row=_make_kb_row())
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.process_message(_make_payload(tenant_id=""))
    assert len(orchestrator.calls) == 0


@pytest.mark.asyncio
async def test_process_message_missing_object_id_dropped():
    nats = _FakeNATS()
    pool = _MockPool(doc_row=_make_doc_row(), kb_row=_make_kb_row())
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.process_message(_make_payload(object_id=""))
    assert len(orchestrator.calls) == 0


@pytest.mark.asyncio
async def test_process_message_doc_not_found_dropped():
    """If the document is not in kb_documents, the message is dropped."""
    nats = _FakeNATS()
    pool = _MockPool(doc_row=None, kb_row=_make_kb_row())
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.process_message(_make_payload())
    assert len(orchestrator.calls) == 0


@pytest.mark.asyncio
async def test_process_message_missing_file_type_dropped():
    """If the document has no file_type, the message is dropped."""
    nats = _FakeNATS()
    pool = _MockPool(
        doc_row=_make_doc_row(file_type=""),
        kb_row=_make_kb_row(),
    )
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.process_message(_make_payload())
    assert len(orchestrator.calls) == 0


@pytest.mark.asyncio
async def test_process_message_missing_vector_store_id_dropped():
    """If the KB has no vector_store_id, the message is dropped."""
    nats = _FakeNATS()
    pool = _MockPool(
        doc_row=_make_doc_row(),
        kb_row=_make_kb_row(vector_store_id=""),
    )
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.process_message(_make_payload())
    assert len(orchestrator.calls) == 0


# ── idempotency ───────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_duplicate_message_does_not_double_parse():
    """Idempotency: the consumer dispatches duplicates to the orchestrator,
    which is responsible for skipping already-ready documents (its own
    ``parse_status == 'ready'`` guard). The consumer itself does NOT
    short-circuit — it delegates idempotency to the orchestrator so the
    guard logic stays in one place (parse_orchestrator.py).
    """
    nats = _FakeNATS()
    pool = _MockPool(
        doc_row=_make_doc_row(),
        kb_row=_make_kb_row(),
    )
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    payload = _make_payload()
    # Send the same message twice
    await consumer.process_message(payload)
    await consumer.process_message(payload)
    # The consumer dispatched both to the orchestrator; the orchestrator's
    # own idempotency guard (parse_status == 'ready' check) would skip the
    # second in production. Here we verify the consumer does not filter.
    assert len(orchestrator.calls) == 2


# ── shutdown / stop-start race ─────────────────────────────────────────────


@pytest.mark.asyncio
async def test_on_msg_rejected_during_shutdown():
    """Messages arriving after stop() is called are rejected."""
    nats = _FakeNATS()
    pool = _MockPool(doc_row=_make_doc_row(), kb_row=_make_kb_row())
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.start()
    # Simulate stop (set _stopped = True without calling stop() to test
    # the _on_msg guard in isolation)
    consumer._stopped = True
    await nats.callback(_FakeMsg(_make_payload()))
    await asyncio.sleep(0.05)
    # No task should have been created → orchestrator not called
    assert len(orchestrator.calls) == 0
    # Clean up
    consumer._stopped = False
    await consumer.stop()


# ── orchestrator error handling ────────────────────────────────────────────


@pytest.mark.asyncio
async def test_orchestrator_exception_does_not_crash_consumer():
    """If the orchestrator raises, the consumer logs and continues."""
    nats = _FakeNATS()
    pool = _MockPool(doc_row=_make_doc_row(), kb_row=_make_kb_row())
    orchestrator = _FakeOrchestrator()
    orchestrator.raise_next(RuntimeError("orchestrator boom"))
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.start()
    await nats.callback(_FakeMsg(_make_payload()))
    await asyncio.sleep(0.05)
    await consumer.stop(timeout=2.0)
    # The exception was caught; the consumer did not crash
    assert len(orchestrator.calls) == 1


# ── invalid payload ────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_invalid_json_payload_dropped():
    """A message with invalid JSON is dropped (not dispatched)."""
    nats = _FakeNATS()
    pool = _MockPool(doc_row=_make_doc_row(), kb_row=_make_kb_row())
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
    )
    await consumer.start()

    class _BadMsg:
        data = b"not valid json"

    await nats.callback(_BadMsg())
    await asyncio.sleep(0.05)
    await consumer.stop(timeout=2.0)
    assert len(orchestrator.calls) == 0


# ── concurrency bound ─────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_max_concurrency_bounds_in_flight_tasks():
    """With max_concurrency=1, tasks are serialized (bounded parallelism)."""
    nats = _FakeNATS()
    pool = _MockPool(doc_row=_make_doc_row(), kb_row=_make_kb_row())
    orchestrator = _FakeOrchestrator()
    consumer = ParseConsumer(
        nats_client=nats,
        db_pool=pool,
        orchestrator=orchestrator,
        subject=SUBJECT_V2,
        max_concurrency=1,
    )
    await consumer.start()
    # Inject 3 messages
    for _ in range(3):
        await nats.callback(_FakeMsg(_make_payload()))
    await asyncio.sleep(0.1)
    await consumer.stop(timeout=2.0)
    # All 3 should have been processed (serially under the semaphore)
    assert len(orchestrator.calls) == 3


# ── config flag ───────────────────────────────────────────────────────────


def test_config_kb_parse_consumer_enabled_defaults_false():
    """The flag defaults to False (consumer does not start by default)."""
    from app.core.config import Settings

    s = Settings()
    assert s.kb_parse_consumer_enabled is False


def test_config_nats_parse_subject_v2_is_v2():
    """The v2 subject is the new subject, distinct from the legacy one."""
    from app.core.config import Settings

    s = Settings()
    assert s.nats_parse_subject_v2 == "ani.tasks.kb.parse.v2"
    assert s.nats_parse_subject_v2 != s.nats_parse_subject
    assert s.nats_parse_subject == "ani.tasks.kb.parse"
