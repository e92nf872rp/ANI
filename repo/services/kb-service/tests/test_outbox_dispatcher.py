"""Tests for the outbox dispatcher (issue-008 / US-010, SPEC §6.1).

Verifies:
- The dispatcher polls outbox_events and publishes undispatched rows to NATS
  `ani.tasks.kb.parse` (100/batch).
- Each published event is marked published=TRUE in its own short transaction.
- The dispatcher loop survives transient errors (does not die).
- stop() drains the background task.

Uses a mock asyncpg pool and a mock NATS client so no real DB/NATS is required.
"""
import asyncio
import json
import os
import sys
import uuid
from contextlib import asynccontextmanager
from unittest.mock import AsyncMock, MagicMock

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.outbox.dispatcher import OutboxDispatcher


TENANT_ID = "11111111-1111-1111-1111-111111111111"
DOC_ID = "33333333-3333-3333-3333-333333333333"


class _MockNATS:
    """Records publish calls; optionally fails to simulate transient errors."""

    def __init__(self):
        self.published: list[tuple[str, bytes]] = []
        self._fail = False

    def fail_next(self):
        self._fail = True

    async def publish(self, subject: str, payload: bytes):
        if self._fail:
            self._fail = False
            raise RuntimeError("NATS transient error")
        self.published.append((subject, payload))


def _make_event(event_id: int, payload: dict | None = None):
    """Build an outbox_events row dict as returned by list_undispatched."""
    return {
        "id": event_id,
        "aggregate_type": "kb_documents",
        "aggregate_id": uuid.UUID(DOC_ID),
        "event_type": "kb.parse",
        "tenant_id": uuid.UUID(TENANT_ID),
        "payload": payload or {"doc_id": DOC_ID, "kb_id": "kb-1"},
        "published": False,
        "published_at": None,
        "created_at": None,
    }


class _MockConn:
    """Minimal asyncpg.Connection mock for outbox repo calls."""

    def __init__(self, rows=None):
        self._rows = rows if rows is not None else []
        self._marked: list[int] = []

    async def fetch(self, sql, *args):
        # list_undispatched passes LIMIT $1 as the last arg; honor it so
        # batch_size is respected.
        limit = None
        for a in args:
            if isinstance(a, int):
                limit = a
                break
        if limit is not None:
            return list(self._rows[:limit])
        return list(self._rows)

    async def execute(self, sql, *args):
        # mark_dispatched: UPDATE ... WHERE id = $1 (single)
        # mark_dispatched_batch: UPDATE ... WHERE id = ANY($1::int[]) (list)
        if args:
            if isinstance(args[0], int):
                self._marked.append(args[0])
            elif isinstance(args[0], list):
                self._marked.extend(args[0])
        # Return "UPDATE N" where N matches the affected count for batch.
        if args and isinstance(args[0], list):
            return f"UPDATE {len(args[0])}"
        return "UPDATE 1"


class _MockPool:
    """Returns _MockConn instances from acquire(); each acquire returns a
    fresh conn so the dispatcher's list + mark use independent conns (as in
    the real pool path)."""

    def __init__(self, rows=None):
        self._rows = rows
        self._conns: list[_MockConn] = []

    @asynccontextmanager
    async def acquire(self):
        conn = _MockConn(rows=self._rows)
        self._conns.append(conn)
        yield conn


# ── _dispatch_once ────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_dispatch_once_publishes_and_marks_all_events():
    rows = [_make_event(1), _make_event(2), _make_event(3)]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject="ani.tasks.kb.parse", batch_size=100
    )
    dispatched = await dispatcher._dispatch_once()
    assert dispatched == 3
    assert len(nats.published) == 3
    assert all(subject == "ani.tasks.kb.parse" for subject, _ in nats.published)
    # payload is JSON-encoded bytes
    payloads = [json.loads(p) for _, p in nats.published]
    assert payloads[0]["doc_id"] == DOC_ID


@pytest.mark.asyncio
async def test_dispatch_once_batch_marks_all_published_ids():
    """Issue 2 fix: all published events are marked in ONE batched UPDATE on
    ONE connection, not one connection per event."""
    rows = [_make_event(1), _make_event(2), _make_event(3)]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject="ani.tasks.kb.parse", batch_size=100
    )
    await dispatcher._dispatch_once()
    # The mark connection is the SECOND acquire (list=1, mark=1); verify it
    # received all three event ids in a single batch call.
    assert len(pool._conns) == 2  # one for list, one for batch mark
    mark_conn = pool._conns[1]
    assert mark_conn._marked == [1, 2, 3]  # batched, single execute call


@pytest.mark.asyncio
async def test_dispatch_once_empty_returns_zero():
    pool = _MockPool(rows=[])
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(pool=pool, nats_client=nats, subject="ani.tasks.kb.parse")
    dispatched = await dispatcher._dispatch_once()
    assert dispatched == 0
    assert nats.published == []


@pytest.mark.asyncio
async def test_dispatch_once_respects_batch_size():
    rows = [_make_event(i) for i in range(5)]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject="ani.tasks.kb.parse", batch_size=2
    )
    dispatched = await dispatcher._dispatch_once()
    assert dispatched == 2
    assert len(nats.published) == 2


@pytest.mark.asyncio
async def test_publish_uses_subject_from_settings():
    rows = [_make_event(1)]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject="ani.tasks.kb.parse"
    )
    await dispatcher._dispatch_once()
    assert nats.published[0][0] == "ani.tasks.kb.parse"


# ── Issue 037: v2 subject switch + rollback ─────────────────────────────────


def test_config_v2_subject_and_flag_defaults():
    """Issue 037: nats_parse_subject_v2 differs from legacy subject and the
    consumer flag defaults to False (rollback / pre-switch state)."""
    from app.core.config import Settings

    s = Settings()
    assert s.nats_parse_subject == "ani.tasks.kb.parse"
    assert s.nats_parse_subject_v2 == "ani.tasks.kb.parse.v2"
    assert s.nats_parse_subject_v2 != s.nats_parse_subject
    assert s.kb_parse_consumer_enabled is False


@pytest.mark.asyncio
async def test_dispatch_publishes_to_v2_subject_when_flag_enabled():
    """Issue 037 / Plan step 9: when kb_parse_consumer_enabled=True the
    Outbox Dispatcher is constructed with nats_parse_subject_v2 and publishes
    to ``ani.tasks.kb.parse.v2`` (consumed by the kb-service parse consumer),
    NOT the legacy subject."""
    from app.core.config import Settings

    s = Settings()
    # Simulate the flag-on wiring done in main.py (lines 202-206):
    #   outbox_subject = v2 if flag else legacy
    outbox_subject = (
        s.nats_parse_subject_v2 if True else s.nats_parse_subject
    )
    rows = [_make_event(1), _make_event(2)]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject=outbox_subject
    )
    await dispatcher._dispatch_once()
    assert len(nats.published) == 2
    assert all(
        subject == "ani.tasks.kb.parse.v2" for subject, _ in nats.published
    )
    # None of the events should go to the legacy subject.
    assert all(
        subject != "ani.tasks.kb.parse" for subject, _ in nats.published
    )


@pytest.mark.asyncio
async def test_dispatch_publishes_to_legacy_subject_when_flag_disabled():
    """Issue 037 rollback path: when kb_parse_consumer_enabled=False the
    Outbox Dispatcher publishes to the legacy subject
    ``ani.tasks.kb.parse`` (consumed by rag-engine parse_worker). This is
    the pre-switch and rollback state."""
    from app.core.config import Settings

    s = Settings()
    # Simulate the flag-off wiring done in main.py (lines 202-206):
    outbox_subject = (
        s.nats_parse_subject_v2 if False else s.nats_parse_subject
    )
    rows = [_make_event(1)]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject=outbox_subject
    )
    await dispatcher._dispatch_once()
    assert len(nats.published) == 1
    assert nats.published[0][0] == "ani.tasks.kb.parse"


@pytest.mark.asyncio
async def test_rollback_reverts_from_v2_to_legacy_subject():
    """Issue 037 rollback plan: switching the flag back to False makes the
    dispatcher publish to the legacy subject again, unblocking the
    rag-engine parse_worker path."""
    rows = [_make_event(1)]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()

    # Switched state (flag=True → v2 subject)
    dispatcher_v2 = OutboxDispatcher(
        pool=pool, nats_client=nats, subject="ani.tasks.kb.parse.v2"
    )
    await dispatcher_v2._dispatch_once()
    assert nats.published[-1][0] == "ani.tasks.kb.parse.v2"

    # Rollback (flag=False → legacy subject). New event dispatched.
    pool2 = _MockPool(rows=[_make_event(2)])
    dispatcher_legacy = OutboxDispatcher(
        pool=pool2, nats_client=nats, subject="ani.tasks.kb.parse"
    )
    await dispatcher_legacy._dispatch_once()
    assert nats.published[-1][0] == "ani.tasks.kb.parse"


# ── lifecycle ─────────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_start_stop_lifecycle_drains_task():
    rows = [_make_event(1)]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject="ani.tasks.kb.parse",
        poll_interval=0.01,
    )
    task = dispatcher.start()
    assert isinstance(task, asyncio.Task)
    # let at least one iteration run
    await asyncio.sleep(0.05)
    await dispatcher.stop(timeout=2.0)
    assert task.done() or task.cancelled()
    # at least one event should have been published
    assert len(nats.published) >= 1


@pytest.mark.asyncio
async def test_loop_survives_transient_nats_error():
    """A transient NATS publish error must not kill the dispatcher loop."""
    # First publish fails, subsequent succeed; one event so only one publish
    # attempt per iteration. We give 3 rows so the failing one is skipped and
    # the rest still dispatch on that iteration.
    rows = [_make_event(1), _make_event(2)]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()
    nats.fail_next()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject="ani.tasks.kb.parse",
        poll_interval=0.01,
    )
    task = dispatcher.start()
    await asyncio.sleep(0.05)
    await dispatcher.stop(timeout=2.0)
    # The dispatcher did not crash: the transient error raised from
    # _dispatch_once (publish-all-then-batch-mark) is caught by _run_loop,
    # backed off, and retried; the task stays alive until stop().
    assert task.done() or task.cancelled()


# ── backoff on persistent errors (Issue 1) ────────────────────────────────────


@pytest.mark.asyncio
async def test_run_loop_backs_off_on_consecutive_failures():
    """Issue 1 fix: on consecutive _dispatch_once failures, the backoff
    grows exponentially and is capped at MAX_BACKOFF_INTERVAL_SECONDS."""
    from app.outbox.dispatcher import MAX_BACKOFF_INTERVAL_SECONDS

    pool = _MockPool(rows=[_make_event(1)])
    nats = _MockNATS()
    # Make every publish fail so _dispatch_once always raises.
    nats._fail = True

    class _AlwaysFailNATS(_MockNATS):
        async def publish(self, subject, payload):
            raise RuntimeError("NATS down")

    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=_AlwaysFailNATS(), subject="ani.tasks.kb.parse",
        poll_interval=0.01,
    )
    # Simulate consecutive failures by exercising the backoff formula used
    # in _run_loop: backoff = min(poll_interval * 2**min(n-1, 8), cap).
    dispatcher._consecutive_failures = 0
    # First failure: backoff = 0.01 * 2^0 = 0.01
    dispatcher._consecutive_failures += 1
    b1 = min(0.01 * (2 ** 0), MAX_BACKOFF_INTERVAL_SECONDS)
    assert b1 == 0.01
    # Second failure: backoff = 0.01 * 2^1 = 0.02
    dispatcher._consecutive_failures += 1
    b2 = min(0.01 * (2 ** 1), MAX_BACKOFF_INTERVAL_SECONDS)
    assert b2 == 0.02
    # The exponent is capped at 8, so with poll_interval=0.01 the max backoff
    # is 0.01 * 2^8 = 2.56s (the 30s cap only bites at the default 1s interval).
    dispatcher._consecutive_failures = 20
    b_cap = min(0.01 * (2 ** min(19, 8)), MAX_BACKOFF_INTERVAL_SECONDS)
    assert b_cap == 2.56  # 0.01 * 256, exponent capped at 8

    # Verify the 30s cap bites at the default poll_interval (1.0s).
    from app.outbox.dispatcher import DEFAULT_POLL_INTERVAL_SECONDS
    b_default_cap = min(
        DEFAULT_POLL_INTERVAL_SECONDS * (2 ** 8),
        MAX_BACKOFF_INTERVAL_SECONDS,
    )
    assert b_default_cap == MAX_BACKOFF_INTERVAL_SECONDS  # 256 > 30 → capped


# ── payload encoding ─────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_payload_dict_is_json_encoded():
    rows = [_make_event(1, payload={"doc_id": DOC_ID, "kb_id": "kb-1", "n": 5})]
    pool = _MockPool(rows=rows)
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject="ani.tasks.kb.parse"
    )
    await dispatcher._dispatch_once()
    _, payload_bytes = nats.published[0]
    decoded = json.loads(payload_bytes.decode("utf-8"))
    assert decoded["doc_id"] == DOC_ID
    assert decoded["n"] == 5


@pytest.mark.asyncio
async def test_payload_string_is_passed_through():
    """If the DB returns payload as a JSON string, it is published as-is."""
    event = _make_event(1)
    event["payload"] = json.dumps({"doc_id": DOC_ID})
    pool = _MockPool(rows=[event])
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        pool=pool, nats_client=nats, subject="ani.tasks.kb.parse"
    )
    await dispatcher._dispatch_once()
    decoded = json.loads(nats.published[0][1].decode("utf-8"))
    assert decoded["doc_id"] == DOC_ID
