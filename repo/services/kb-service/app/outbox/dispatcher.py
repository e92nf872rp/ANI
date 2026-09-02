"""Outbox dispatcher — poll outbox_events and publish to NATS (SPEC §6.1, US-010).

SPEC §6.1 dispatcher algorithm:

    loop:
      rows = SELECT * FROM outbox_events WHERE published = FALSE
             ORDER BY created_at LIMIT 100
      for r in rows:
        nats.publish('ani.tasks.kb.parse', json(r.payload))
        UPDATE outbox_events SET published = TRUE, published_at = now()
      sleep(1s)

This module implements that algorithm as a long-running coroutine. It is
at-least-once: a process crash between NATS publish and the published=TRUE
marking causes a duplicate publish on the next poll, so the rag-engine
parse_worker MUST be idempotent on doc_id (rag-engine SPEC covers this).

The dispatcher runs as an independent coroutine started by main.py; it is
independent of the gRPC servicer and survives per-request failures. NATS is
not required for NotifyDocumentUploaded to succeed — the event is durably
stored in outbox_events and dispatched on the next poll, so NATS outages
degrade to delayed dispatch rather than lost work (SPEC §7.3).
"""
from __future__ import annotations

import asyncio
import json
import logging
from typing import Any

import asyncpg

from app.repositories import outbox as outbox_repo

logger = logging.getLogger(__name__)

# SPEC §6.1: 100/批, 1s poll interval.
DEFAULT_BATCH_SIZE = 100
DEFAULT_POLL_INTERVAL_SECONDS = 1.0
# Backoff: on consecutive failures, multiply the sleep up to this cap so a
# persistent DB/NATS outage doesn't flood logs with a traceback every second.
MAX_BACKOFF_INTERVAL_SECONDS = 30.0
MAX_BACKOFF_MULTIPLIER = 30  # cap at 30x the base poll interval (30s @ 1s base)
# Per-event NATS publish timeout: nats.publish awaits a server PUB ack window
# on a possibly-dead TCP connection (no keepalive by default on Windows), so
# an unbounded await can hang the whole dispatch loop forever. Bounded waits
# turn that into a normal failure → backoff → retry (at-least-once, safe).
DEFAULT_PUBLISH_TIMEOUT_SECONDS = 10.0


class OutboxDispatcher:
    """Polls outbox_events and publishes undispatched rows to NATS.

    Lifecycle:
        dispatcher = OutboxDispatcher(pool, nats_client, subject="ani.tasks.kb.parse")
        await dispatcher.start()   # spawns the poll loop as a background task
        ...
        await dispatcher.stop()    # cancels the loop and drains
    """

    def __init__(
        self,
        *,
        pool: asyncpg.Pool,
        nats_client: Any,
        subject: str = "ani.tasks.kb.parse",
        batch_size: int = DEFAULT_BATCH_SIZE,
        poll_interval: float = DEFAULT_POLL_INTERVAL_SECONDS,
        publish_timeout: float = DEFAULT_PUBLISH_TIMEOUT_SECONDS,
    ) -> None:
        self._pool = pool
        self._nats = nats_client
        self._subject = subject
        self._batch_size = batch_size
        self._poll_interval = poll_interval
        self._publish_timeout = publish_timeout
        self._task: asyncio.Task | None = None
        self._stopped = False
        # Backoff state for persistent-error log dedup.
        self._consecutive_failures = 0
        self._last_error_logged = 0.0  # monotonic time of last traceback log

    def start(self) -> asyncio.Task:
        """Spawn the poll loop as a background task and return it."""
        if self._task is not None and not self._task.done():
            return self._task
        self._stopped = False
        self._task = asyncio.create_task(self._run_loop(), name="outbox-dispatcher")
        return self._task

    async def stop(self, timeout: float | None = 5.0) -> None:
        """Signal the poll loop to stop and wait for it to drain."""
        self._stopped = True
        if self._task is not None and not self._task.done():
            self._task.cancel()
            try:
                await asyncio.wait_for(self._task, timeout=timeout)
            except (asyncio.CancelledError, asyncio.TimeoutError):
                pass
            self._task = None

    async def _run_loop(self) -> None:
        """Main poll loop: list undispatched → publish → mark → sleep.

        On consecutive failures, backs off exponentially (capped) to avoid
        log flooding during a persistent DB/NATS outage, and collapses
        repeated tracebacks so only one full traceback is logged per backoff
        window (at most once per MAX_BACKOFF_INTERVAL_SECONDS).
        """
        import time

        while not self._stopped:
            try:
                await self._dispatch_once()
                self._consecutive_failures = 0
            except asyncio.CancelledError:
                raise
            except Exception:  # noqa: BLE001 — dispatcher must not die on errors
                self._consecutive_failures += 1
                # Log a full traceback at most once per backoff window to avoid
                # flooding logs with identical tracebacks on a persistent outage.
                now = time.monotonic()
                backoff = min(
                    self._poll_interval * (2 ** min(self._consecutive_failures - 1, 8)),
                    MAX_BACKOFF_INTERVAL_SECONDS,
                )
                if now - self._last_error_logged >= backoff:
                    logger.exception(
                        "outbox dispatch iteration failed (attempt %d); backing off %.1fs",
                        self._consecutive_failures, backoff,
                    )
                    self._last_error_logged = now
                await asyncio.sleep(backoff)
                continue
            await asyncio.sleep(self._poll_interval)

    async def _dispatch_once(self) -> int:
        """One poll iteration: list undispatched, publish each, mark in batch.

        Returns the number of events published. Publishes each event to NATS
        first (at-least-once), then marks all published events in a single
        batched UPDATE using one acquired connection (instead of one connection
        per event). A crash between publish and mark leaves events
        un-dispatched → republished next poll; the rag-engine parse_worker
        MUST be idempotent on doc_id (module docstring, rag-engine SPEC).
        """
        async with self._pool.acquire() as conn:
            rows = await outbox_repo.list_undispatched(conn, limit=self._batch_size)
        if not rows:
            return 0
        published_ids: list[int] = []
        for row in rows:
            event_id = int(row["id"])
            payload = row.get("payload")
            if isinstance(payload, str):
                import json as _json
                payload_dict = _json.loads(payload) if payload else {}
            elif isinstance(payload, dict):
                payload_dict = payload
            else:
                payload_dict = {}
            # #2: Merge tenant_id into the published payload so downstream
            # consumers (rag-engine parse_worker) can perform RLS-scoped writes.
            # The outbox_events table has a tenant_id column (selected by
            # list_undispatched) but the original payload from
            # NotifyDocumentUploaded only has {doc_id, kb_id, storage_path}.
            if "tenant_id" not in payload_dict and row.get("tenant_id"):
                payload_dict["tenant_id"] = str(row["tenant_id"])
            payload_str = json.dumps(payload_dict, default=str)
            # Bounded publish: a dead NATS TCP connection must not hang the
            # loop forever. On timeout the event stays un-dispatched and is
            # retried on the next poll (at-least-once semantics preserved).
            await asyncio.wait_for(
                self._nats.publish(
                    self._subject, payload_str.encode("utf-8")
                ),
                timeout=self._publish_timeout,
            )
            published_ids.append(event_id)
        # Batch-mark all published events in one UPDATE on one connection.
        async with self._pool.acquire() as conn:
            await outbox_repo.mark_dispatched_batch(conn, event_ids=published_ids)
        # Heartbeat: one INFO line per productive iteration. If the loop is
        # ever suspected of hanging again, the timestamp gap between two
        # consecutive lines localizes exactly when the last successful
        # iteration ran (an idle-but-healthy loop logs nothing by design).
        logger.info(
            "outbox dispatch: published %d event(s) (ids=%s)",
            len(published_ids), published_ids,
        )
        return len(published_ids)
