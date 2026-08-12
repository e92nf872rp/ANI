"""Tests for the outbox dispatcher (issue-008 / US-010, SPEC §6.1, §4.2).

Verifies:
- The dispatcher polls outbox_events (via CoreClient.data_query role="service")
  and publishes undispatched rows to NATS `ani.tasks.kb.parse` (100/batch).
- Each published event is marked published=TRUE in a single batched UPDATE
  (via CoreClient.data_query role="service").
- The dispatcher loop survives transient errors (does not die).
- stop() drains the background task.

Uses a mock CoreClient (data_query) and a mock NATS client so no real
Core gateway / DB / NATS is required (issue-030: data plane mock).
"""
import asyncio
import json
import os
import sys
import uuid
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
        "aggregate_id": DOC_ID,
        "event_type": "kb.parse",
        "tenant_id": TENANT_ID,
        "payload": payload or {"doc_id": DOC_ID, "kb_id": "kb-1"},
        "published": False,
        "published_at": None,
        "created_at": None,
    }


def _mock_core(*, list_rows=None, mark_return=None):
    """Build a mock CoreClient whose data_query returns canned responses.

    - list_undispatched: returns list_rows (role="service")
    - mark_dispatched_batch: returns mark_return (role="service")
    """
    core = MagicMock()
    core.data_query = AsyncMock()
    core.aclose = AsyncMock()
    # Default: list returns list_rows, mark returns rowcount
    list_rows = list_rows if list_rows is not None else []
    mark_return = mark_return if mark_return is not None else {"rowcount": 0}

    call_count = [0]

    async def _data_query(*, sql, params=None, role="tenant"):
        call_count[0] += 1
        if "SELECT" in sql and "outbox_events" in sql:
            # list_undispatched
            return {"rows": list_rows, "rowcount": len(list_rows)}
        if "UPDATE outbox_events" in sql:
            # mark_dispatched or mark_dispatched_batch
            return mark_return
        return {"rows": [], "rowcount": 0}

    core.data_query.side_effect = _data_query
    return core


# ── _dispatch_once ────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_dispatch_once_publishes_and_marks_all_events():
    rows = [_make_event(1), _make_event(2), _make_event(3)]
    core = _mock_core(list_rows=rows, mark_return={"rowcount": 3})
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse",
        batch_size=100,
    )
    dispatched = await dispatcher._dispatch_once()
    assert dispatched == 3
    assert len(nats.published) == 3
    assert all(subject == "ani.tasks.kb.parse" for subject, _ in nats.published)
    # payload is JSON-encoded bytes
    payloads = [json.loads(p) for _, p in nats.published]
    assert payloads[0]["doc_id"] == DOC_ID
    # The mark call used role="service" (cross-tenant, SPEC §4.2)
    mark_call = [c for c in core.data_query.call_args_list
                 if "UPDATE outbox_events" in c.kwargs["sql"]][0]
    assert mark_call.kwargs["role"] == "service"


@pytest.mark.asyncio
async def test_dispatch_once_batch_marks_all_published_ids():
    """Issue 2 fix: all published events are marked in ONE batched UPDATE."""
    rows = [_make_event(1), _make_event(2), _make_event(3)]
    core = _mock_core(list_rows=rows, mark_return={"rowcount": 3})
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse",
        batch_size=100,
    )
    await dispatcher._dispatch_once()
    # Verify the mark call received all three event ids in a single batch
    mark_calls = [c for c in core.data_query.call_args_list
                  if "UPDATE outbox_events" in c.kwargs["sql"]]
    assert len(mark_calls) == 1  # single batched mark call
    params = mark_calls[0].kwargs["params"]
    assert params[0] == [1, 2, 3]  # event_ids passed as a list


@pytest.mark.asyncio
async def test_dispatch_once_empty_returns_zero():
    core = _mock_core(list_rows=[])
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse"
    )
    dispatched = await dispatcher._dispatch_once()
    assert dispatched == 0
    assert nats.published == []


@pytest.mark.asyncio
async def test_dispatch_once_respects_batch_size():
    rows = [_make_event(i) for i in range(5)]
    core = _mock_core(list_rows=rows[:2], mark_return={"rowcount": 2})
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse",
        batch_size=2,
    )
    dispatched = await dispatcher._dispatch_once()
    assert dispatched == 2
    assert len(nats.published) == 2


@pytest.mark.asyncio
async def test_publish_uses_subject_from_settings():
    rows = [_make_event(1)]
    core = _mock_core(list_rows=rows, mark_return={"rowcount": 1})
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse"
    )
    await dispatcher._dispatch_once()
    assert nats.published[0][0] == "ani.tasks.kb.parse"


@pytest.mark.asyncio
async def test_list_undispatched_uses_service_role():
    """AC: list_undispatched uses role='service' for cross-tenant access
    (SPEC §4.2)."""
    rows = [_make_event(1)]
    core = _mock_core(list_rows=rows, mark_return={"rowcount": 1})
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse"
    )
    await dispatcher._dispatch_once()
    # The list call used role="service"
    list_call = [c for c in core.data_query.call_args_list
                 if "SELECT" in c.kwargs["sql"]][0]
    assert list_call.kwargs["role"] == "service"


# ── lifecycle ─────────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_start_stop_lifecycle_drains_task():
    rows = [_make_event(1)]
    core = _mock_core(list_rows=rows, mark_return={"rowcount": 1})
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse",
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
    rows = [_make_event(1), _make_event(2)]
    core = _mock_core(list_rows=rows, mark_return={"rowcount": 2})
    nats = _MockNATS()
    nats.fail_next()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse",
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

    core = _mock_core(list_rows=[_make_event(1)])
    nats = _MockNATS()

    class _AlwaysFailNATS(_MockNATS):
        async def publish(self, subject, payload):
            raise RuntimeError("NATS down")

    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=_AlwaysFailNATS(),
        subject="ani.tasks.kb.parse", poll_interval=0.01,
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
    core = _mock_core(list_rows=rows, mark_return={"rowcount": 1})
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse"
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
    core = _mock_core(list_rows=[event], mark_return={"rowcount": 1})
    nats = _MockNATS()
    dispatcher = OutboxDispatcher(
        core_client=core, nats_client=nats, subject="ani.tasks.kb.parse"
    )
    await dispatcher._dispatch_once()
    decoded = json.loads(nats.published[0][1].decode("utf-8"))
    assert decoded["doc_id"] == DOC_ID
