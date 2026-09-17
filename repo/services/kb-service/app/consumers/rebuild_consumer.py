"""kb-service NATS rebuild consumer (P1 #24, B6).

Consumes messages from ``ani.tasks.kb.rebuild.v1`` (routed there by the
OutboxDispatcher's ``subject_overrides`` for event_type ``kb.rebuild``)
and executes the full-KB re-parse: every ready/failed document in the
KB is reset and re-parsed sequentially.

Transport is a JetStream durable push consumer (ManualAck) on the
ANI_TASKS WorkQueue stream — at-least-once delivery, per the deploy
contract (component-contracts/nats.yaml) and the Go message_bus
Subscribe path. Ack semantics (see app/consumers/jetstream.py):
handler success → Ack, invalid JSON → Ack (poison pill swallowed; the
durable outbox row stays as the operator's source of truth), unhandled
crash → neither Ack nor Nak (silent for ack_wait redelivery — the
redelivery takes over the orphan task via mark_running's expired-lease
path). While a rebuild runs, an InProgress heartbeat (ack_wait/3)
renews the delivery lease so a long rebuild is never redelivered
mid-run.

Default OFF — started only when ``settings.kb_rebuild_consumer_enabled``
is True (main.py gates startup on this flag, mirroring the parse
consumer rollout).

Design decisions (B6 plan):
  - Serial execution (Semaphore(1), D5): a rebuild is a heavy long job;
    documents are processed one at a time so one rebuild never competes
    with itself for pool connections or rag-engine capacity.
  - Per-document reset (D1): each document is reset via
    ``reset_for_reparse_in_tx`` immediately before its
    ``process_document`` call — NOT in one bulk UPDATE. Queries keep
    being served from the existing (old) chunks/vector index while the
    rebuild runs; the orchestrator's built-in re-entrant cleanup removes
    the old chunks per document.
  - The orchestrator's ready-skip (parse_status == 'ready') would defeat
    the rebuild — the reset to 'pending' happens first, so the skip
    never fires; the orchestrator itself is untouched (zero changes
    below this layer).
  - No checkpoint/resume (D5): a crashed rebuild leaves the async_tasks
    row pending (or the KB stuck rebuilding); the client retries with a
    fresh idempotency key (revive semantics: a failed task is not
    replayed on the same key, SPEC §5.4).

Payload shape (from kb-service outbox_events, RebuildKB servicer)::

    {"kb_id", "tenant_id", "task_id"}

Idempotency (at-least-once): a redelivered rebuild message is gated
by ``mark_running`` (pending → running conditional UPDATE, with an
expired-lease takeover path for orphaned running rows). If the
task is still pending (first delivery crashed before marking), or
its running lease has expired (consumer died mid-run), the rebuild
runs; if the task is running under a live lease or already
terminal, the message is a duplicate and skipped entirely. Within
one run,
``list_documents_for_rebuild`` only returns ready/failed docs, so
already-rebuilt (ready) docs are re-reset and re-parsed — this is
the point of a rebuild. Documents deleted between snapshot and
processing are skipped (reset matches 0 rows / process_document
returns early).

KB status lifecycle: the consumer is the only writer of the
rebuilding → active exit transition (``set_status_in_tx``), in a
``finally`` so a crashed handler still restores the KB to active (the
async_tasks row stays failed; the KB must not stay locked forever).
"""
from __future__ import annotations

import asyncio
import json
import logging
from typing import Any, Protocol, runtime_checkable

import asyncpg

from app.consumers import jetstream
from app.repositories import async_task as async_task_repo
from app.repositories import document as doc_repo
from app.repositories import knowledge_base as kb_repo

logger = logging.getLogger(__name__)

# D5: serial — one document at a time, no concurrent rebuild documents.
REBUILD_MAX_CONCURRENCY = 1

# JetStream durable identity (Go model-import-worker style constants:
# AckWait 30m — a full-KB rebuild can legitimately take longer than a
# single document parse; MaxDeliver 3 — enough redeliveries for one
# crash-restart cycle, without an endless retry loop; MaxAckPending 1 —
# the durable's delivery backpressure matches the serial handler).
REBUILD_DURABLE = "kb-rebuild-consumer"
REBUILD_ACK_WAIT = 30 * 60          # seconds
REBUILD_MAX_DELIVER = 3


@runtime_checkable
class _ParseOrchestrator(Protocol):
    """Subset of ParseOrchestrator used by the rebuild consumer."""

    async def process_document(
        self,
        *,
        tenant_id: str,
        kb_id: str,
        doc_id: str,
        object_id: str,
        file_name: str,
        file_type: str,
        chunk_size: int,
        vector_store_id: str,
        embedding_model: str = "",
    ) -> None: ...


class RebuildConsumer:
    """NATS consumer that executes full-KB rebuilds sequentially.

    Lifecycle mirrors ParseConsumer::

        consumer = RebuildConsumer(
            nats_client=nc, db_pool=pool, orchestrator=parse_orchestrator,
            subject="ani.tasks.kb.rebuild.v1",
        )
        await consumer.start()   # subscribe
        ...
        await consumer.stop()   # unsubscribe + drain in-flight
    """

    def __init__(
        self,
        *,
        nats_client: Any,
        db_pool: asyncpg.Pool,
        orchestrator: _ParseOrchestrator,
        subject: str,
        max_concurrency: int = REBUILD_MAX_CONCURRENCY,
    ) -> None:
        self._nats = nats_client
        self._pool = db_pool
        self._orchestrator = orchestrator
        self._subject = subject
        self._max_concurrency = max_concurrency
        self._subscription = None
        self._semaphore: asyncio.Semaphore | None = None
        self._pending: set[asyncio.Task] = set()
        self._stopped = True

    async def start(self) -> None:
        """Subscribe to the rebuild subject and begin consuming."""
        self._stopped = False
        self._semaphore = asyncio.Semaphore(self._max_concurrency)
        js = self._nats.jetstream()
        await jetstream.ensure_ani_tasks_stream(js)
        self._subscription = await jetstream.subscribe_durable(
            js,
            subject=self._subject,
            durable=REBUILD_DURABLE,
            cb=self._on_msg,
            ack_wait=REBUILD_ACK_WAIT,
            max_deliver=REBUILD_MAX_DELIVER,
            max_ack_pending=self._max_concurrency,
        )
        logger.info(
            "rebuild_consumer: subscribed to %s (durable=%s)",
            self._subject, REBUILD_DURABLE,
        )

    async def stop(self, timeout: float = 5.0) -> None:
        """Unsubscribe and drain in-flight rebuild tasks."""
        self._stopped = True
        if self._subscription is not None:
            try:
                await self._subscription.unsubscribe()
            except Exception:  # noqa: BLE001 — best-effort
                pass
            self._subscription = None
        if self._pending:
            try:
                await asyncio.wait_for(
                    asyncio.gather(*self._pending, return_exceptions=True),
                    timeout=timeout,
                )
            except asyncio.TimeoutError:
                logger.warning(
                    "rebuild_consumer: drain timed out, cancelling %d "
                    "lingering tasks", len(self._pending),
                )
                # cancel-then-await: cancelling only requests interruption;
                # each task is awaited so cleanup (KB restore in finally,
                # ack/nak) actually runs before stop() returns. A bare
                # cancel without await leaves the tasks orphaned on a
                # closing event loop.
                for task in self._pending:
                    task.cancel()
                await asyncio.gather(*self._pending, return_exceptions=True)
            self._pending.clear()

    async def _on_msg(self, msg: Any) -> None:
        """NATS callback: spawn a bounded task per rebuild message."""
        if self._stopped:
            return
        if self._semaphore is None:
            self._semaphore = asyncio.Semaphore(self._max_concurrency)
        task = asyncio.create_task(self._handle(msg))
        self._pending.add(task)
        task.add_done_callback(self._pending.discard)

    async def _handle(self, msg: Any) -> None:
        """Process one rebuild message with bounded concurrency.

        JetStream ack policy (ManualAck; see jetstream.py):
          - invalid payload → Ack: it can never succeed on redelivery
            (poison pill); the durable outbox row keeps the audit trail;
          - any normal return of ``process_message`` (including task
            close-outs with status='failed') → Ack: the task row is the
            source of truth and a redelivery would just gate-skip;
          - unhandled crash → neither Ack nor Nak. The message goes
            silent and JetStream redelivers after ack_wait (30 min) —
            which is exactly when the task row's running lease expires,
            so the redelivery takes over the orphan through
            mark_running's expired-lease path. A Nak instead would
            redeliver immediately, hit the still-live lease, gate-skip
            and Ack — stranding the message with the task stuck running.
        """
        async with self._semaphore:  # type: ignore[union-attr]
            try:
                payload = json.loads(msg.data.decode("utf-8"))
                if not isinstance(payload, dict):
                    raise ValueError(
                        f"payload is {type(payload).__name__}, expected object"
                    )
            except Exception as exc:  # noqa: BLE001
                logger.error("rebuild_consumer: invalid message payload: %s", exc)
                await jetstream.ack(msg)
                return
            # Renew the JetStream delivery lease while the handler runs
            # (in_progress every ack_wait/3, mirroring the Go heartbeat
            # goroutine) so a long rebuild is never redelivered mid-run.
            heartbeat = asyncio.create_task(
                jetstream.heartbeat_loop(msg, REBUILD_ACK_WAIT / 3)
            )
            try:
                await self.process_message(payload)
                await jetstream.ack(msg)
            except asyncio.CancelledError:
                raise
            except Exception:  # noqa: BLE001
                logger.exception(
                    "rebuild_consumer: unhandled crash; leaving message "
                    "unacked for ack_wait redelivery (orphan takeover "
                    "via expired lease)"
                )
            finally:
                # Stop renewing the delivery lease before the outcome is
                # final; on crash paths this is what lets JetStream
                # schedule the redelivery at all.
                heartbeat.cancel()
                try:
                    await heartbeat
                except asyncio.CancelledError:
                    pass

    async def process_message(self, payload: dict[str, Any]) -> None:
        """Run the full-KB rebuild for one task payload.

        Payload shape (from the RebuildKB outbox event)::

            {"kb_id", "tenant_id", "task_id"}

        Task lifecycle: mark_running on pickup → set_progress after each
        document (monotonic) → complete_task(completed | failed) with the
        outcome. Terminal semantics (B6 plan):
          - all documents failed → status='failed' (client retries with a
            fresh idempotency key);
          - any success → status='completed' with failed_doc_ids in the
            result (partial failure is visible, not silent).
        """
        kb_id = payload.get("kb_id", "")
        tenant_id = payload.get("tenant_id", "")
        task_id = payload.get("task_id") or ""
        if not kb_id or not tenant_id:
            logger.error(
                "rebuild_consumer: missing required fields (kb_id=%s tenant_id=%s)",
                kb_id, tenant_id,
            )
            return

        if not task_id:
            # Outbox event published without a task ref (shouldn't happen
            # — RebuildKB always creates the row first). Process anyway;
            # only progress/close-out are skipped.
            logger.warning(
                "rebuild_consumer: no task_id in payload for kb %s; "
                "running rebuild without task tracking", kb_id,
            )

        # 1. mark the task running (no-op without a task_id). The
        #    conditional UPDATE (pending → running) is the idempotency
        #    gate for at-least-once redelivery: a duplicate delivery
        #    finds the task already running/completed and returns False —
        #    skip BEFORE entering the try/finally so we neither re-run
        #    the rebuild nor touch the KB status transition.
        if task_id:
            try:
                async with self._pool.acquire() as conn:
                    marked = await async_task_repo.mark_running(
                        conn, tenant_id=tenant_id, task_id=task_id
                    )
            except Exception as exc:  # noqa: BLE001
                logger.exception(
                    "rebuild_consumer: mark_running failed for task %s: %s",
                    task_id, exc,
                )
            else:
                if not marked:
                    logger.warning(
                        "rebuild_consumer: task %s for kb %s is not pending "
                        "(duplicate delivery or already processed); skipping "
                        "rebuild", task_id, kb_id,
                    )
                    return

        kb_restored = False
        failed_doc_ids: list[str] = []
        succeeded = 0
        try:
            # 2. resolve KB metadata (vector store / embedding model /
            #    chunk_size drive every document parse).
            async with self._pool.acquire() as conn:
                kb_row = await kb_repo.get_kb(
                    conn, tenant_id=tenant_id, kb_id=kb_id
                )
            if not kb_row:
                logger.warning(
                    "rebuild_consumer: kb %s not found, skipping rebuild", kb_id,
                )
                if task_id:
                    await self._complete(
                        tenant_id, task_id, status="failed",
                        result={"error": "knowledge base not found"},
                        failed_doc_ids=[],
                    )
                return
            vector_store_id = str(kb_row.get("vector_store_id") or "")
            embedding_model = str(kb_row.get("embedding_model") or "")
            chunk_size = kb_row.get("chunk_size")
            if chunk_size is None:
                chunk_size = 1024  # NOT NULL column; defensive fallback
            if not vector_store_id:
                logger.error(
                    "rebuild_consumer: kb %s has no vector_store_id, "
                    "skipping rebuild", kb_id,
                )
                if task_id:
                    await self._complete(
                        tenant_id, task_id, status="failed",
                        result={"error": "kb has no vector_store_id"},
                        failed_doc_ids=[],
                    )
                return

            # 3. snapshot eligible doc ids (ready/failed, soft-deleted
            #    excluded). Later deletions are skipped per-doc below.
            async with self._pool.acquire() as conn:
                doc_ids = await doc_repo.list_documents_for_rebuild(
                    conn, tenant_id=tenant_id, kb_id=kb_id
                )
            total = len(doc_ids)
            logger.info(
                "rebuild_consumer: rebuilding kb %s (%d documents)", kb_id, total,
            )

            # 4. sequential per-document rebuild (D1: reset then parse —
            #    the reset makes the orchestrator's ready-skip inert).
            for idx, doc_id in enumerate(doc_ids, start=1):
                async with self._pool.acquire() as conn:
                    doc_row = await doc_repo.get_document(
                        conn, tenant_id=tenant_id, kb_id=kb_id, doc_id=doc_id,
                    )
                if not doc_row:
                    # Deleted between snapshot and processing — skip.
                    logger.info(
                        "rebuild_consumer: doc %s vanished, skipping", doc_id,
                    )
                    continue
                # Re-check eligibility: the doc may have been deleted
                # (soft-deleted marker) or gone mid-pipeline after the
                # snapshot; both are out of rebuild scope.
                status = doc_row.get("parse_status")
                if status not in ("ready", "failed"):
                    logger.info(
                        "rebuild_consumer: doc %s parse_status=%s (mid-pipeline?), "
                        "skipping", doc_id, status,
                    )
                    continue

                # Reset (own transaction) then parse. The reset clears the
                # ready state so process_document's skip never fires.
                async with self._pool.acquire() as conn:
                    async with conn.transaction():
                        reset = await doc_repo.reset_for_reparse_in_tx(
                            conn, tenant_id=tenant_id, kb_id=kb_id, doc_id=doc_id,
                        )
                if not reset:
                    logger.info(
                        "rebuild_consumer: doc %s reset matched 0 rows, "
                        "skipping", doc_id,
                    )
                    continue

                try:
                    await self._orchestrator.process_document(
                        tenant_id=tenant_id,
                        kb_id=kb_id,
                        doc_id=doc_id,
                        object_id=str(doc_row.get("object_id") or ""),
                        file_name=str(doc_row.get("file_name") or ""),
                        file_type=str(doc_row.get("file_type") or ""),
                        chunk_size=int(chunk_size),
                        vector_store_id=vector_store_id,
                        embedding_model=embedding_model,
                    )
                    succeeded += 1
                except Exception as exc:  # noqa: BLE001 — per-doc isolation
                    # The orchestrator swallows its own errors (writes
                    # failed); this catches transport-level failures only.
                    logger.exception(
                        "rebuild_consumer: rebuild parse failed for doc %s: %s",
                        doc_id, exc,
                    )
                    failed_doc_ids.append(doc_id)

                # 5. monotonic progress after each document.
                if task_id:
                    try:
                        async with self._pool.acquire() as conn:
                            await async_task_repo.set_progress(
                                conn,
                                tenant_id=tenant_id,
                                task_id=task_id,
                                progress_pct=int(idx * 100 / total),
                            )
                    except Exception as exc:  # noqa: BLE001
                        logger.warning(
                            "rebuild_consumer: set_progress failed for task "
                            "%s: %s", task_id, exc,
                        )
        finally:
            # 6. restore the KB to active — MUST run even on crash paths,
            #    otherwise the KB stays locked (rebuilding) forever.
            #    Wrapped in a transaction: set_status_in_tx is an in_tx
            #    repo function (does NOT open its own transaction) whose
            #    SET LOCAL RLS context is only honored inside a tx block;
            #    bare autocommit would leave PG WARNING 25P01 spam and a
            #    silently unset tenant context.
            try:
                async with self._pool.acquire() as conn:
                    async with conn.transaction():
                        restored = await kb_repo.set_status_in_tx(
                            conn,
                            tenant_id=tenant_id,
                            kb_id=kb_id,
                            from_status="rebuilding",
                            to_status="active",
                        )
                kb_restored = restored
                if not restored:
                    logger.warning(
                        "rebuild_consumer: kb %s was not in 'rebuilding' "
                        "on exit (deleted or already active); no restore "
                        "needed", kb_id,
                    )
            except Exception:  # noqa: BLE001 — best-effort restore
                logger.exception(
                    "rebuild_consumer: failed to restore kb %s to active; "
                    "it stays rebuilding (operator intervention needed)", kb_id,
                )

        # 7. terminal task state (after the finally-side KB restore so the
        #    KB is active by the time the client polls the task).
        if task_id:
            status = "failed" if (failed_doc_ids and succeeded == 0) else "completed"
            await self._complete(
                tenant_id, task_id, status=status,
                result={
                    "total": len(failed_doc_ids) + succeeded,
                    "succeeded": succeeded,
                    "failed_doc_ids": failed_doc_ids,
                },
                failed_doc_ids=failed_doc_ids,
            )
        logger.info(
            "rebuild_consumer: kb %s rebuild done (succeeded=%d failed=%d "
            "kb_restored=%s)", kb_id, succeeded, len(failed_doc_ids), kb_restored,
        )

    async def _complete(
        self,
        tenant_id: str,
        task_id: str,
        *,
        status: str,
        result: dict[str, Any],
        failed_doc_ids: list[str],
    ) -> None:
        """Close the async_tasks row with the rebuild outcome (best-effort)."""
        try:
            async with self._pool.acquire() as conn:
                await async_task_repo.complete_task(
                    conn,
                    tenant_id=tenant_id,
                    task_id=task_id,
                    result=result,
                    status=status,
                )
        except Exception as exc:  # noqa: BLE001
            logger.exception(
                "rebuild_consumer: failed to close task %s (%d failed docs): %s",
                task_id, len(failed_doc_ids), exc,
            )


def build_rebuild_consumer(
    *,
    nats_client: Any,
    db_pool: asyncpg.Pool,
    orchestrator: _ParseOrchestrator,
    subject: str,
    max_concurrency: int = REBUILD_MAX_CONCURRENCY,
) -> RebuildConsumer:
    """Factory for constructing a RebuildConsumer (called from main.py).

    Kept as a separate function so main.py can conditionally build the
    consumer only when ``settings.kb_rebuild_consumer_enabled`` is True.
    """
    return RebuildConsumer(
        nats_client=nats_client,
        db_pool=db_pool,
        orchestrator=orchestrator,
        subject=subject,
        max_concurrency=max_concurrency,
    )
