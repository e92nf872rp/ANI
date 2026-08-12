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

issue-031: the dispatcher polls via the Core data plane
(`CoreClient.data_query` with `role="service"` for cross-tenant access,
SPEC §4.2). There is no asyncpg pool in kb-service — all data access goes
through the CoreClient data plane (SPEC §4.3).
"""
from __future__ import annotations

import asyncio
import json
import logging
from typing import Any

from app.core_api.client import CoreClient
from app.repositories import outbox as outbox_repo

logger = logging.getLogger(__name__)

# SPEC §6.1: 100/批, 1s poll interval.
DEFAULT_BATCH_SIZE = 100
DEFAULT_POLL_INTERVAL_SECONDS = 1.0
# Backoff: on consecutive failures, multiply the sleep up to this cap so a
# persistent DB/NATS outage doesn't flood logs with a traceback every second.
MAX_BACKOFF_INTERVAL_SECONDS = 30.0
MAX_BACKOFF_MULTIPLIER = 30  # cap at 30x the base poll interval (30s @ 1s base)


class OutboxDispatcher:
    """Polls outbox_events and publishes undispatched rows to NATS.

    Lifecycle:
        dispatcher = OutboxDispatcher(core_client, nats_client, subject="ani.tasks.kb.parse")
        await dispatcher.start()   # spawns the poll loop as a background task
        ...
        await dispatcher.stop()    # cancels the loop and drains

    Resilience (SPEC §7.3): if ``nats_client`` is None at construction time
    but a ``nats_connect`` callable is supplied, the dispatcher starts anyway
    and (re)connects to NATS lazily inside ``_dispatch_once``. This keeps the
    poll loop running so events accumulated while NATS was down are dispatched
    once NATS recovers — honoring the "delayed dispatch, not lost work" promise
    even when NATS is unavailable at process startup.
    """

    def __init__(
        self,
        *,
        core_client: CoreClient,
        nats_client: Any,
        subject: str = "ani.tasks.kb.parse",
        batch_size: int = DEFAULT_BATCH_SIZE,
        poll_interval: float = DEFAULT_POLL_INTERVAL_SECONDS,
        nats_connect: Any = None,
    ) -> None:
        self._core = core_client
        self._nats = nats_client
        self._subject = subject
        self._batch_size = batch_size
        self._poll_interval = poll_interval
        self._nats_connect = nats_connect
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

    @property
    def nats_client(self) -> Any:
        """The current NATS client (may differ from the startup client after
        a lazy reconnect). Used by main.py shutdown to coordinate NATS
        draining without reaching into private state."""
        return self._nats

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
        # Drain a lazily-(re)connected NATS client if we own one.
        if self._nats is not None and self._nats_connect is not None:
            try:
                await self._nats.drain()
            except Exception:  # noqa: BLE001 — best-effort cleanup
                pass

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
        batched UPDATE using the data plane (role="service", cross-tenant,
        SPEC §4.2). A crash between publish and mark leaves events
        un-dispatched → republished next poll; the rag-engine parse_worker
        MUST be idempotent on doc_id (module docstring, rag-engine SPEC).

        If NATS is not currently connected and a ``nats_connect`` callable was
        supplied, attempt to (re)connect before publishing. A failed publish
        drops the NATS handle so the next iteration reconnects (SPEC §7.3
        self-healing).
        """
        rows = await outbox_repo.list_undispatched(
            self._core, limit=self._batch_size
        )
        if not rows:
            return 0
        # Lazy (re)connect to NATS if we don't currently have a client.
        if self._nats is None and self._nats_connect is not None:
            self._nats = await self._nats_connect()
            if self._nats is not None:
                logger.info("outbox dispatcher (re)connected to NATS")
        if self._nats is None:
            # NATS still unavailable — leave events un-dispatched; they will
            # be retried on the next poll once NATS recovers (SPEC §7.3).
            # Raise so _run_loop treats this as a failure, increments
            # _consecutive_failures, and applies backoff + log suppression
            # (otherwise a nats_connect returning None silently would flood
            # logs every second with no backoff).
            raise RuntimeError("NATS unavailable; nats_connect returned None")
        published_ids: list[int] = []
        try:
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
                await self._nats.publish(self._subject, payload_str.encode("utf-8"))
                published_ids.append(event_id)
        except Exception:
            # NATS publish failed (e.g. connection dropped mid-batch). Drop
            # the handle so the next iteration reconnects; events that were
            # not yet marked stay un-dispatched → republished next poll
            # (at-least-once, SPEC §7.3).
            self._nats = None
            raise
        # Batch-mark all published events in one data-plane UPDATE (role=service).
        await outbox_repo.mark_dispatched_batch(
            self._core, event_ids=published_ids
        )
        return len(published_ids)
