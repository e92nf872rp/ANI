"""kb-service NATS parse consumer (Plan step 6, issue-033).

Replaces the rag-engine ``parse_worker`` NATS consumer for the parse
pipeline. Consumes messages from ``ani.tasks.kb.parse.v2`` (a distinct
subject from the legacy ``ani.tasks.kb.parse``, Plan §0.3) and dispatches
each to ``ParseOrchestrator.process_document``.

Transport is a JetStream durable push consumer (ManualAck) on the
ANI_TASKS WorkQueue stream — at-least-once delivery, per the deploy
contract (component-contracts/nats.yaml) and the Go message_bus
Subscribe path. Ack semantics (see app/consumers/jetstream.py):
success → Ack, invalid JSON → Ack (poison pill swallowed; the durable
outbox row keeps the audit trail), unhandled crash → neither Ack nor
Nak (silent for ack_wait redelivery — a redelivery simply re-runs the
pipeline from the top, which self-heals: the orchestrator's re-entrant
chunk cleanup and the ready-skip make the second pass idempotent).
While a parse runs, an InProgress heartbeat (ack_wait/3) renews the
delivery lease so a long parse is never redelivered mid-run.

Default OFF — started only when ``settings.kb_parse_consumer_enabled`` is
True (main.py gates startup on this flag). The Outbox Dispatcher publishes
to the v2 subject only when the flag is on, so the consumer and the
dispatcher stay in sync.

Idempotency (SPEC §5.4 at-least-once):
  - Duplicate messages do not cause duplicate parses. ``ParseOrchestrator``
    checks ``parse_status == 'ready'`` before doing any work (see
    parse_orchestrator.py ``process_document`` idempotency guard), so a
    redelivered message for an already-ingested document is a no-op.
  - The ``pending → parsing`` UPDATE also doubles as a row-existence check;
    a document that was deleted between publish and consume is skipped
    (the orchestrator returns early when the UPDATE matches 0 rows).

Payload shape (from kb-service outbox_events, SPEC §6.1)::

    {"doc_id", "kb_id", "storage_path", "tenant_id",
     "object_id", "file_name", "chunk_size", "task_id"}

``task_id`` (optional) references the async_tasks row created by
NotifyDocumentUploaded / ReparseDocument; when present the consumer
closes it (completed/failed) once the document reaches a terminal
parse_status. Messages without it (pre-change) keep the old behavior.

The consumer additionally resolves ``file_type`` and ``vector_store_id``
from the database (they are not carried in the outbox payload) before
calling the orchestrator:
  - ``file_type``: read from ``kb_documents`` (written by
    GetDocumentUploadURL / NotifyDocumentUploaded).
  - ``vector_store_id``: read from ``knowledge_bases`` (set by CreateKB).
"""
from __future__ import annotations

import asyncio
import json
import logging
from typing import Any, Protocol, runtime_checkable

import asyncpg

from app.consumers import jetstream
from app.repositories import async_task as async_task_repo
from app.repositories import audit as audit_repo
from app.repositories import document as doc_repo
from app.repositories import knowledge_base as kb_repo

logger = logging.getLogger(__name__)

# Concurrency bound: parse is CPU/IO heavy; cap in-flight tasks to protect
# the process under burst load (mirrors rag-engine parse_worker
# DEFAULT_MAX_CONCURRENCY).
DEFAULT_MAX_CONCURRENCY = 4

# JetStream durable identity (Go model-import-worker style constants:
# AckWait 30m — a single document parse can take that long for large
# files, and it matches the rebuild consumer / Go worker precedent so
# the fleet has one convention; MaxDeliver 3 — enough redeliveries for
# one crash-restart cycle, without an endless retry loop; MaxAckPending
# == DEFAULT_MAX_CONCURRENCY — the durable's delivery backpressure
# matches the handler semaphore).
PARSE_DURABLE = "kb-parse-consumer"
PARSE_ACK_WAIT = 30 * 60          # seconds
PARSE_MAX_DELIVER = 3

# Cap for the error message copied into the result audit row's error_msg
# (the orchestrator's sanitized message is already length-bounded; this is
# a defensive trim so the audit row stays small).
_RESULT_AUDIT_MSG_MAX = 512


def _truncate_audit_text(text: str) -> str:
    return text if len(text) <= _RESULT_AUDIT_MSG_MAX else text[:_RESULT_AUDIT_MSG_MAX]


@runtime_checkable
class _ParseOrchestrator(Protocol):
    """Subset of ParseOrchestrator used by the consumer."""

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
    ) -> None: ...


class ParseConsumer:
    """NATS consumer that dispatches parse messages to ParseOrchestrator.

    Lifecycle::

        consumer = ParseConsumer(
            nats_client=nc,
            db_pool=pool,
            orchestrator=parse_orchestrator,
            subject="ani.tasks.kb.parse.v2",
        )
        await consumer.start()    # subscribe
        ...
        await consumer.stop()     # unsubscribe + drain in-flight

    The consumer uses a push subscription with a callback (``cb=_on_msg``)
    that spawns a bounded ``asyncio.Task`` per message, decoupling NATS
    callback latency from the heavy parse work (same pattern as rag-engine
    parse_worker).
    """

    def __init__(
        self,
        *,
        nats_client: Any,
        db_pool: asyncpg.Pool,
        orchestrator: _ParseOrchestrator,
        subject: str,
        max_concurrency: int = DEFAULT_MAX_CONCURRENCY,
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
        """Subscribe to the v2 subject and begin consuming."""
        self._stopped = False
        self._semaphore = asyncio.Semaphore(self._max_concurrency)
        js = self._nats.jetstream()
        await jetstream.ensure_ani_tasks_stream(js)
        self._subscription = await jetstream.subscribe_durable(
            js,
            subject=self._subject,
            durable=PARSE_DURABLE,
            cb=self._on_msg,
            ack_wait=PARSE_ACK_WAIT,
            max_deliver=PARSE_MAX_DELIVER,
            max_ack_pending=self._max_concurrency,
        )
        logger.info(
            "parse_consumer: subscribed to %s (durable=%s)",
            self._subject, PARSE_DURABLE,
        )

    async def stop(self, timeout: float = 5.0) -> None:
        """Unsubscribe and drain in-flight tasks.

        Sets ``_stopped`` first so the NATS callback rejects new messages
        during the drain (mirrors rag-engine parse_worker stop/start race
        guard). Unlike the rag-engine version, this implementation catches
        ``asyncio.TimeoutError`` so a slow drain doesn't propagate the
        exception or skip ``_pending.clear()``; lingering tasks are cancelled.
        """
        self._stopped = True
        if self._subscription is not None:
            try:
                await self._subscription.unsubscribe()
            except Exception:  # noqa: BLE001 — best-effort
                pass
            self._subscription = None
        # Drain in-flight tasks; cancel lingering tasks on timeout
        if self._pending:
            try:
                await asyncio.wait_for(
                    asyncio.gather(*self._pending, return_exceptions=True),
                    timeout=timeout,
                )
            except asyncio.TimeoutError:
                logger.warning(
                    "parse_consumer: drain timed out, cancelling %d "
                    "lingering tasks", len(self._pending),
                )
                # cancel-then-await: cancelling only requests interruption;
                # each task is awaited so cleanup (ack/nak) actually runs
                # before stop() returns. A bare cancel without await
                # leaves the tasks orphaned on a closing event loop.
                for task in self._pending:
                    task.cancel()
                await asyncio.gather(*self._pending, return_exceptions=True)
            self._pending.clear()

    async def _on_msg(self, msg: Any) -> None:
        """NATS callback: spawn a bounded task per message.

        Does NOT process inline — parse work is heavy and would block the
        NATS client dispatch loop. The task is tracked in ``_pending`` and
        removed via a ``done_callback`` (so the set doesn't grow unbounded).
        """
        if self._stopped:
            # Reject messages during shutdown (stop/start race guard).
            return
        if self._semaphore is None:
            self._semaphore = asyncio.Semaphore(self._max_concurrency)
        task = asyncio.create_task(self._handle(msg))
        self._pending.add(task)
        task.add_done_callback(self._pending.discard)

    async def _handle(self, msg: Any) -> None:
        """Process one NATS message with bounded concurrency.

        JetStream ack policy (ManualAck; see jetstream.py):
          - invalid payload → Ack: it can never succeed on redelivery
            (poison pill); the durable outbox row keeps the audit trail;
          - any normal return of ``process_message`` (including the
            orchestrator's own swallowed failures — it writes
            parse_status='failed' and the consumer closes the task row
            accordingly) → Ack: the doc/task rows are the source of
            truth and a redelivery would just re-check and skip;
          - unhandled crash → neither Ack nor Nak. The message goes
            silent and JetStream redelivers after ack_wait (30 min); the
            redelivery re-runs the pipeline from the top, which
            self-heals: the orchestrator's pending→parsing UPDATE matches
            the row (no status gate), its re-entrant chunk cleanup
            removes the old chunks, and the ready-skip covers any doc
            the first pass had already finished. A Nak instead would
            hammer the backend with immediate retries of a parse that
            may be failing for a load-related reason.
        """
        async with self._semaphore:  # type: ignore[union-attr]
            try:
                payload = json.loads(msg.data.decode("utf-8"))
                if not isinstance(payload, dict):
                    raise ValueError(
                        f"payload is {type(payload).__name__}, expected object"
                    )
            except Exception as exc:  # noqa: BLE001
                logger.error("parse_consumer: invalid message payload: %s", exc)
                await jetstream.ack(msg)
                return
            # Renew the JetStream delivery lease while the handler runs
            # (in_progress every ack_wait/3, mirroring the Go heartbeat
            # goroutine) so a long parse is never redelivered mid-run.
            heartbeat = asyncio.create_task(
                jetstream.heartbeat_loop(msg, PARSE_ACK_WAIT / 3)
            )
            try:
                await self.process_message(payload)
                await jetstream.ack(msg)
            except asyncio.CancelledError:
                raise
            except Exception:  # noqa: BLE001
                logger.exception(
                    "parse_consumer: unhandled crash; leaving message "
                    "unacked for ack_wait redelivery (pipeline re-runs "
                    "from the top and self-heals)"
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
        """Run the parse pipeline for one task payload.

        Payload shape (from kb-service outbox, SPEC §6.1)::

            {"doc_id", "kb_id", "storage_path", "tenant_id",
             "object_id", "file_name", "chunk_size", "task_id"}

        Resolves ``file_type`` (from kb_documents) and ``vector_store_id``
        (from knowledge_bases) before calling the orchestrator.
        """
        doc_id = payload.get("doc_id", "")
        kb_id = payload.get("kb_id", "")
        object_id = payload.get("object_id", "")
        tenant_id = payload.get("tenant_id", "")
        file_name = payload.get("file_name", "")
        chunk_size = payload.get("chunk_size") or 1024

        if not doc_id or not kb_id or not tenant_id:
            logger.error(
                "parse_consumer: missing required fields "
                "(doc_id=%s kb_id=%s tenant_id=%s)",
                doc_id, kb_id, tenant_id,
            )
            return

        # Resolve file_type and vector_store_id from the database.
        # These are not carried in the outbox payload.
        try:
            async with self._pool.acquire() as conn:
                doc_row = await doc_repo.get_document(
                    conn, tenant_id=tenant_id, kb_id=kb_id, doc_id=doc_id,
                )
                kb_row = await kb_repo.get_kb(
                    conn, tenant_id=tenant_id, kb_id=kb_id,
                )
        except Exception as exc:  # noqa: BLE001
            logger.exception(
                "parse_consumer: failed to resolve doc/kb metadata for "
                "doc %s: %s", doc_id, exc,
            )
            return

        if not doc_row:
            logger.warning(
                "parse_consumer: doc %s not found in kb_documents, skipping",
                doc_id,
            )
            return

        # Fallback: if object_id was missing or not a UUID, resolve it
        # from kb_documents.object_id (the Core-assigned UUID persisted
        # at upload time). This mirrors the old path's parse_worker logic.
        if not object_id or "/" in object_id:
            db_object_id = doc_row.get("object_id")
            if db_object_id:
                object_id = str(db_object_id)
                logger.info(
                    "parse_consumer: resolved object_id from DB for doc %s: %s",
                    doc_id, object_id,
                )
            else:
                logger.error(
                    "parse_consumer: cannot resolve object_id for doc %s "
                    "(not in payload, not in kb_documents); skipping",
                    doc_id,
                )
                return

        file_type = doc_row.get("file_type", "") or ""
        if not file_type:
            logger.error(
                "parse_consumer: doc %s has no file_type, skipping", doc_id,
            )
            return

        if not file_name:
            file_name = doc_row.get("file_name", "") or ""

        vector_store_id = ""
        embedding_model = ""
        if kb_row:
            vector_store_id = str(kb_row.get("vector_store_id") or "")
            # Per-KB embedding model (M2): write side uses the KB row's
            # embedding_model so write and read always share one model.
            embedding_model = str(kb_row.get("embedding_model") or "")
        if not vector_store_id:
            logger.error(
                "parse_consumer: kb %s has no vector_store_id, skipping "
                "doc %s", kb_id, doc_id,
            )
            return

        # Idempotency: ParseOrchestrator.process_document checks
        # parse_status == 'ready' and skips already-ingested documents.
        # The pending → parsing UPDATE also serves as a row-existence
        # check (0 rows → skip). Both guards live in the orchestrator,
        # so the consumer simply dispatches.
        task_id = payload.get("task_id") or ""
        try:
            await self._orchestrator.process_document(
                tenant_id=tenant_id,
                kb_id=kb_id,
                doc_id=doc_id,
                object_id=object_id,
                file_name=file_name,
                file_type=file_type,
                chunk_size=int(chunk_size),
                vector_store_id=vector_store_id,
                embedding_model=embedding_model,
            )
        except Exception as exc:  # noqa: BLE001 — orchestrator handles errors
            logger.exception(
                "parse_consumer: orchestrator failed for doc %s: %s",
                doc_id, exc,
            )

        # Task lifecycle closure (issue-047 follow-up): the orchestrator
        # records terminal state on kb_documents but never touches
        # async_tasks — without this close-out the row stays pending
        # forever and clients polling the task see no progress. The doc's
        # terminal parse_status decides completed vs failed; the
        # orchestrator swallows its own exceptions (writes failed), so a
        # re-read here is the single source of truth. Messages without a
        # task_id (published before this change) keep the old behavior.
        #
        # Result audit (operation-history display fix): the doc.parse /
        # doc.reparse audit row written at task creation only records the
        # *intent*, so a parse that later failed still showed success in
        # the KB operation history. _close_task_and_audit flips THAT row
        # in place (success: error_code stays NULL; failure: PARSE_FAILED
        # + the doc's sanitized error message) in the same transaction as
        # the task close — one entry per operation, not two.
        if not task_id:
            return
        try:
            async with self._pool.acquire() as conn:
                doc_row = await doc_repo.get_document(
                    conn, tenant_id=tenant_id, kb_id=kb_id, doc_id=doc_id,
                )
                if not doc_row:
                    # Doc deleted mid-parse; nothing meaningful to record.
                    return
                status = doc_row.get("parse_status")
                if status == "ready":
                    await self._close_task_and_audit(
                        conn, tenant_id=tenant_id, kb_id=kb_id,
                        task_id=task_id, doc_row=doc_row, failed=False,
                    )
                elif status == "failed":
                    await self._close_task_and_audit(
                        conn, tenant_id=tenant_id, kb_id=kb_id,
                        task_id=task_id, doc_row=doc_row, failed=True,
                    )
                else:
                    # Non-terminal (pending/parsing/indexing) — e.g. the
                    # orchestrator skipped because the doc was concurrently
                    # reset; leave the row for the next delivery.
                    logger.warning(
                        "parse_consumer: doc %s parse_status=%s after "
                        "processing, leaving task %s open",
                        doc_id, status, task_id,
                    )
        except Exception as exc:  # noqa: BLE001
            logger.exception(
                "parse_consumer: failed to close task %s for doc %s: %s",
                task_id, doc_id, exc,
            )

    async def _close_task_and_audit(
        self,
        conn: asyncpg.Connection,
        *,
        tenant_id: str,
        kb_id: str,
        task_id: str,
        doc_row: dict[str, Any],
        failed: bool,
    ) -> None:
        """Close the async_tasks row and flip the parse-intent audit row.

        The task row's task_type picks the action (kb.reparse → doc.reparse,
        anything else → doc.parse). complete_task_in_tx's terminal-state
        guard makes this idempotent under at-least-once redelivery: a second
        delivery sees the task already terminal (closed=False) and touches
        no audit row. Audit failures roll the close back together with the
        audit write — _handle leaves the message unacked and the redelivery
        retries both atomically.

        The audit write is an in-place UPDATE of the intent row written at
        task creation (located via the task_id inside its after_state):
        error_code flips to PARSE_FAILED on failure (stays NULL on success)
        and after_state is JSONB-merged with the terminal parse_status /
        chunk_count. Only if no intent row carries this task_id (rows
        written by an older build, or the audit was skipped at notify
        time) does it fall back to a separate result INSERT so the trail
        still records the outcome.
        """
        async with conn.transaction():
            task_row = await async_task_repo.get_task(
                conn, tenant_id=tenant_id, task_id=task_id,
            )
            closed = await async_task_repo.complete_task_in_tx(
                conn,
                tenant_id=tenant_id,
                task_id=task_id,
                status="failed" if failed else "completed",
            )
            if not closed:
                # Another writer already closed the task (redelivery race);
                # the audit row was flipped by the first pass.
                return
            task_type = (task_row or {}).get("task_type") or "kb.parse"
            action = "doc.reparse" if task_type == "kb.reparse" else "doc.parse"
            error_code = "PARSE_FAILED" if failed else None
            error_msg = (
                _truncate_audit_text(str(doc_row.get("error_message") or ""))
                or None
            ) if failed else None
            result_overlay = {
                "parse_status": doc_row.get("parse_status"),
                "chunk_count": doc_row.get("chunk_count"),
                "task_id": task_id,
            }
            updated = await audit_repo.update_parse_result_in_tx(
                conn,
                tenant_id=tenant_id,
                kb_id=kb_id,
                action=action,
                task_id=task_id,
                result_overlay=result_overlay,
                error_code=error_code,
                error_msg=error_msg,
            )
            if updated:
                return
            # Fallback: the intent row (if any) predates the task_id link —
            # keep the old separate result row so the outcome is recorded.
            await audit_repo.insert_audit_in_tx(
                conn,
                tenant_id=tenant_id,
                kb_id=kb_id,
                action=action,
                # NULL actor = internal system actor (parse consumer).
                actor_user_id=None,
                before_state=None,
                after_state={
                    "doc_id": str(doc_row.get("id") or ""),
                    "file_name": doc_row.get("file_name"),
                    "file_type": doc_row.get("file_type"),
                    **result_overlay,
                },
                error_code=error_code,
                error_msg=error_msg,
            )


def build_parse_consumer(
    *,
    nats_client: Any,
    db_pool: asyncpg.Pool,
    orchestrator: _ParseOrchestrator,
    subject: str,
    max_concurrency: int = DEFAULT_MAX_CONCURRENCY,
) -> ParseConsumer:
    """Factory for constructing a ParseConsumer (called from main.py).

    Kept as a separate function so main.py can conditionally build the
    consumer only when ``settings.kb_parse_consumer_enabled`` is True.
    """
    return ParseConsumer(
        nats_client=nats_client,
        db_pool=db_pool,
        orchestrator=orchestrator,
        subject=subject,
        max_concurrency=max_concurrency,
    )
