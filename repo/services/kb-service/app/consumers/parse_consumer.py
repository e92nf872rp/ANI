"""kb-service NATS parse consumer (Plan step 6, issue-033).

Replaces the rag-engine ``parse_worker`` NATS consumer for the parse
pipeline. Consumes messages from ``ani.tasks.kb.parse.v2`` (a distinct
subject from the legacy ``ani.tasks.kb.parse``, Plan §0.3) and dispatches
each to ``ParseOrchestrator.process_document``.

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
     "object_id", "file_name", "chunk_size"}

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

from app.repositories import document as doc_repo
from app.repositories import knowledge_base as kb_repo

logger = logging.getLogger(__name__)

# Concurrency bound: parse is CPU/IO heavy; cap in-flight tasks to protect
# the process under burst load (mirrors rag-engine parse_worker
# DEFAULT_MAX_CONCURRENCY).
DEFAULT_MAX_CONCURRENCY = 4


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
        self._subscription = await self._nats.subscribe(
            self._subject, cb=self._on_msg
        )
        logger.info("parse_consumer: subscribed to %s", self._subject)

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
                for task in self._pending:
                    task.cancel()
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
        """Process one NATS message with bounded concurrency."""
        async with self._semaphore:  # type: ignore[union-attr]
            try:
                payload = json.loads(msg.data.decode("utf-8"))
            except Exception as exc:  # noqa: BLE001
                logger.error("parse_consumer: invalid message payload: %s", exc)
                return
            await self.process_message(payload)

    async def process_message(self, payload: dict[str, Any]) -> None:
        """Run the parse pipeline for one task payload.

        Payload shape (from kb-service outbox, SPEC §6.1)::

            {"doc_id", "kb_id", "storage_path", "tenant_id",
             "object_id", "file_name", "chunk_size"}

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
        if kb_row:
            vector_store_id = str(kb_row.get("vector_store_id") or "")
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
            )
        except Exception as exc:  # noqa: BLE001 — orchestrator handles errors
            logger.exception(
                "parse_consumer: orchestrator failed for doc %s: %s",
                doc_id, exc,
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
