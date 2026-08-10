"""NATS parse_worker (US-015 / SPEC §2.1, §2.2, §5.1, §5.3).

Subscribes to ``ani.tasks.kb.parse`` and runs the parse pipeline per task:

    on_msg(msg):
        payload = json(msg)  # {doc_id, kb_id, storage_path}
        update kb_documents.parse_status = 'parsing'
        doc = core_api.download(payload.storage_path)
        nodes = parse_service(doc)
        parents, children = chunk_service(nodes)
        summary = summary_service(parents)   # best-effort
        embed_and_write(parents, children, summary)  # Milvus + kb_chunks
        update kb_documents.parse_status = 'ready'
    on_err:
        update kb_documents.parse_status = 'failed', error_message=...

State machine (SPEC §5.3)::

    pending → parsing → indexing → ready | failed

Idempotency (SPEC §5.4): at-least-once delivery means a duplicate task may
arrive. The worker checks the current ``parse_status`` before processing;
if already ``ready`` it skips (idempotent no-op) to avoid re-parsing.

Dependencies are injected (core_api, parse_service, chunk_service,
summary_service, embed_service, chunks_repo, db_pool, nats_client) so the
worker is unit-testable without live NATS/Milvus/PG/Core connections.
"""
from __future__ import annotations

import asyncio
import json
import logging
import os
import re
import uuid
from typing import Any, Protocol

from app.core.config import settings
from app.clients.core_api import CoreApiClient
from app.services.parse_service import ParseService
from app.services.chunk_service import ChunkService
from app.services.summary_service import SummaryService
from app.services.embed_service import EmbedService
from app.repositories.chunks import write_chunks

logger = logging.getLogger(__name__)

# parse_status values (SPEC §5.3).
STATUS_PARSING = "parsing"
STATUS_INDEXING = "indexing"
STATUS_READY = "ready"
STATUS_FAILED = "failed"

# Max concurrent parse tasks. Parse is CPU/IO heavy; keep the queue bounded
# so a burst of NATS messages doesn't exhaust resources.
DEFAULT_MAX_CONCURRENCY = 4

# Patterns that may leak sensitive info (file paths, tokens) from exceptions
# into the user-visible ``error_message`` column.
_SENSITIVE_PATTERN = re.compile(r"(/[\w/.\-]+|Bearer\s+[\w.\-]+|token[=:]\s*\S+)", re.I)


def _sanitize_error(msg: str) -> str:
    """Redact file paths and tokens from error messages before persisting."""
    return _SENSITIVE_PATTERN.sub("[redacted]", msg)[:500]


# ── Protocols for dependency injection ───────────────────────────────────────


class _ParseStatusUpdater(Protocol):
    """Updates ``kb_documents.parse_status`` (RLS-scoped).

    Implemented by a thin asyncpg-backed adapter; tests inject a fake to
    avoid a live PG connection.
    """

    async def update(self, *, tenant_id: str, doc_id: str, parse_status: str,
                     error_message: str | None = None,
                     chunk_count: int | None = None) -> bool: ...

    async def current(self, *, tenant_id: str, doc_id: str) -> str | None:
        """Read the current parse_status for idempotency (SPEC §5.4).

        Returns ``None`` when the row is not found or when the backend
        cannot read it (the worker skips the idempotency guard).
        """


# ── Module-level asyncpg-backed updater (#4: avoid per-message class rebuild) ─


class _AsyncpgStatusUpdater:
    """Default asyncpg-backed :class:`_ParseStatusUpdater`.

    Implements the same RLS-scoped ``kb_documents.parse_status`` write as
    kb-service, but owned by rag-engine to avoid cross-service imports.

    #4 Cross-service write convention: rag-engine directly updates the
    ``kb_documents`` table (owned by kb-service) to set ``parse_status``
    during the parse pipeline. This is an explicit architectural decision
    documented here and in ``chunks.py``: rag-engine owns the write paths
    for ``kb_chunks`` (chunk data) and ``kb_documents.parse_status`` (parse
    lifecycle) because it is the sole writer during the parse pipeline. Any
    kb-service migration that changes ``kb_documents`` columns used here
    (``id``, ``tenant_id``, ``parse_status``, ``error_message``,
    ``chunk_count``, ``parsed_at``) MUST coordinate with rag-engine.

    Args:
        pool: asyncpg connection pool.
    """

    def __init__(self, pool: Any) -> None:
        self._pool = pool

    async def update(self, *, tenant_id: str, doc_id: str,
                     parse_status: str, error_message: str | None = None,
                     chunk_count: int | None = None) -> bool:
        # #r3: Guard against empty tenant_id — setting an empty RLS context
        # silently corrupts tenant isolation or no-ops the UPDATE.
        if not tenant_id:
            raise ValueError("tenant_id must not be empty for RLS-scoped update")
        async with self._pool.acquire() as conn:
            async with conn.transaction():
                await conn.execute(
                    "SELECT set_config('app.current_tenant_id', $1, true)",
                    tenant_id,
                )
                if parse_status == STATUS_READY:
                    result = await conn.execute(
                        """
                        UPDATE kb_documents
                           SET parse_status = $3,
                               error_message = $4,
                               chunk_count = COALESCE($5, chunk_count),
                               parsed_at = now()
                         WHERE id = $2 AND tenant_id = $1
                        """,
                        tenant_id,
                        uuid.UUID(doc_id),
                        parse_status,
                        error_message,
                        chunk_count,
                    )
                else:
                    result = await conn.execute(
                        """
                        UPDATE kb_documents
                           SET parse_status = $3,
                               error_message = $4,
                               chunk_count = COALESCE($5, chunk_count)
                         WHERE id = $2 AND tenant_id = $1
                        """,
                        tenant_id,
                        uuid.UUID(doc_id),
                        parse_status,
                        error_message,
                        chunk_count,
                    )
                return result == "UPDATE 1"

    async def current(self, *, tenant_id: str, doc_id: str) -> str | None:
        # #r3: Guard against empty tenant_id (same as update).
        if not tenant_id:
            raise ValueError("tenant_id must not be empty for RLS-scoped read")
        async with self._pool.acquire() as conn:
            async with conn.transaction():
                await conn.execute(
                    "SELECT set_config('app.current_tenant_id', $1, true)",
                    tenant_id,
                )
                row = await conn.fetchrow(
                    "SELECT parse_status FROM kb_documents WHERE id = $2 AND tenant_id = $1",
                    tenant_id,
                    uuid.UUID(doc_id),
                )
                return str(row["parse_status"]) if row else None


# ── ParseWorker ──────────────────────────────────────────────────────────────


class ParseWorker:
    """NATS subscriber that runs the parse pipeline (SPEC §5.1, US-015).

    Args:
        nats_client: NATS client (``nats.aio.client.Client``) with an open
            connection. When ``None`` the worker runs in "manual" mode —
            ``process_message(payload)`` can be called directly (tests).
        subject: NATS subject to subscribe to (default
            ``settings.nats_parse_subject``).
        core_api: :class:`CoreApiClient` for object download.
        parse_service / chunk_service / summary_service / embed_service:
            pipeline stages. All injectable for testing.
        chunks_repo_write: Async callable
            ``write_chunks(conn, *, tenant_id, kb_id, doc_id, file_name,
            parents, children, summaries)`` used to write kb_chunks. Defaults
            to :func:`app.repositories.chunks.write_chunks`.
        db_pool: asyncpg pool for kb_chunks writes + parse_status updates.
        status_updater: Optional :class:`_ParseStatusUpdater`. When ``None``
            a default asyncpg-backed updater is built from ``db_pool`` and
            cached for the worker's lifetime.
        max_concurrency: Max concurrent in-flight parse tasks.
        image_uploader: Optional MinIO image uploader for parse_service.
    """

    def __init__(
        self,
        *,
        nats_client: Any | None = None,
        subject: str | None = None,
        core_api: CoreApiClient | None = None,
        parse_service: ParseService | None = None,
        chunk_service: ChunkService | None = None,
        summary_service: SummaryService | None = None,
        embed_service: EmbedService | None = None,
        chunks_repo_write: Any | None = None,
        db_pool: Any | None = None,
        status_updater: _ParseStatusUpdater | None = None,
        max_concurrency: int = DEFAULT_MAX_CONCURRENCY,
        image_uploader: Any | None = None,
    ) -> None:
        self._nats = nats_client
        self._subject = subject or settings.nats_parse_subject
        self._core_api = core_api
        self._parse_service = parse_service
        self._chunk_service = chunk_service or ChunkService()
        self._summary_service = summary_service or SummaryService()
        self._embed_service = embed_service or EmbedService()
        self._chunks_write = chunks_repo_write or write_chunks
        self._db_pool = db_pool
        self._status_updater = status_updater
        self._max_concurrency = max_concurrency
        self._image_uploader = image_uploader
        self._subscription: Any = None
        self._semaphore: asyncio.Semaphore | None = None
        self._pending: set[asyncio.Task] = set()
        self._stopping = False

    @property
    def core_api(self) -> CoreApiClient:
        if self._core_api is None:
            from app.clients.core_api import get_core_client
            self._core_api = get_core_client()
        return self._core_api

    @property
    def parse_service(self) -> ParseService:
        if self._parse_service is None:
            self._parse_service = ParseService(uploader=self._image_uploader)
        return self._parse_service

    @property
    def chunk_service(self) -> ChunkService:
        return self._chunk_service

    @property
    def summary_service(self) -> SummaryService:
        return self._summary_service

    @property
    def embed_service(self) -> EmbedService:
        return self._embed_service

    async def _get_updater(self) -> _ParseStatusUpdater:
        if self._status_updater is not None:
            return self._status_updater
        if self._db_pool is None:
            raise RuntimeError(
                "parse_worker: db_pool or status_updater required to update parse_status"
            )
        # Cache the updater instance for the worker's lifetime (#4).
        self._status_updater = _AsyncpgStatusUpdater(self._db_pool)
        return self._status_updater

    # ── Lifecycle ─────────────────────────────────────────────────────────

    async def start(self) -> None:
        """Subscribe to the NATS subject and begin processing messages."""
        if self._nats is None:
            raise RuntimeError("parse_worker: nats_client is required to start")
        self._stopping = False
        self._semaphore = asyncio.Semaphore(self._max_concurrency)
        self._subscription = await self._nats.subscribe(
            self._subject, cb=self._on_msg
        )
        logger.info("parse_worker subscribed to %s", self._subject)

    async def stop(self, timeout: float = 5.0) -> None:
        """Unsubscribe and drain in-flight tasks.

        Sets ``_stopping`` before unsubscribing so ``_on_msg`` rejects new
        messages during the drain window (#5: stop/start race).
        """
        self._stopping = True  # #5: reject new messages during drain
        if self._subscription is not None:
            try:
                await self._subscription.unsubscribe()
            except Exception:  # noqa: BLE001
                pass
            self._subscription = None
        if self._pending:
            await asyncio.wait_for(
                asyncio.gather(*self._pending, return_exceptions=True),
                timeout=timeout,
            )
        self._pending.clear()

    async def _on_msg(self, msg: Any) -> None:
        """NATS message callback — spawns a bounded task per message."""
        # #5: reject new messages during shutdown to avoid orphaned tasks.
        if self._stopping:
            return
        if self._semaphore is None:
            self._semaphore = asyncio.Semaphore(self._max_concurrency)
        task = asyncio.create_task(self._handle(msg))
        self._pending.add(task)
        task.add_done_callback(self._pending.discard)

    # ── Pipeline ──────────────────────────────────────────────────────────

    async def _handle(self, msg: Any) -> None:
        """Process one NATS message with bounded concurrency."""
        async with self._semaphore:  # type: ignore[union-attr]
            try:
                payload = json.loads(msg.data.decode("utf-8"))
            except Exception as exc:  # noqa: BLE001
                logger.error("parse_worker: invalid message payload: %s", exc)
                return
            await self.process_message(payload)

    async def process_message(self, payload: dict[str, Any]) -> None:
        """Run the full parse pipeline for one task payload.

        Payload shape (from kb-service outbox, SPEC §6.1)::

            {"doc_id", "kb_id", "storage_path", "tenant_id"?}

        The ``tenant_id`` is optional in the payload; when missing it's
        read from the NATS message header or the doc lookup. We require it
        for RLS-scoped writes, so missing tenant_id fails the task.

        State transitions (SPEC §5.3)::

            pending → parsing → indexing → ready | failed
        """
        doc_id = payload.get("doc_id", "")
        kb_id = payload.get("kb_id", "")
        storage_path = payload.get("storage_path", "")
        # #3: Core API uses object_id (UUID), not MinIO storage_path. The NATS
        # payload from kb-service may carry an ``object_id`` field; when absent,
        # look it up from kb_documents (persisted by GetDocumentUploadURL) so
        # downloads still go through the Core API by UUID.
        object_id = payload.get("object_id", "")
        # tenant_id is carried by the outbox payload row but the published
        # payload may not include it; allow callers to pass it explicitly.
        tenant_id = payload.get("tenant_id", "")
        file_name = payload.get("file_name", "") or _file_name_from_path(storage_path)
        # Per-KB chunk size (tokens) from the outbox payload. When absent the
        # worker falls back to the ChunkService default (see chunk_service).
        # chunk_size is passed INTO chunk_service per task, not hard-coded.
        chunk_size = payload.get("chunk_size")

        if not doc_id or not kb_id or not storage_path or not tenant_id:
            logger.error(
                "parse_worker: missing required fields (doc_id=%s kb_id=%s storage_path=%s tenant_id=%s)",
                doc_id, kb_id, storage_path, tenant_id,
            )
            return

        # #3: When the payload carries no object_id (e.g. the synchronous
        # /parse endpoint), resolve it from kb_documents before downloading.
        if not object_id:
            try:
                updater = await self._get_updater()
                async with updater._pool.acquire() as conn:
                    async with conn.transaction():
                        await conn.execute(
                            "SELECT set_config('app.current_tenant_id', $1, true)",
                            tenant_id,
                        )
                        row = await conn.fetchrow(
                            "SELECT object_id FROM kb_documents WHERE id = $1 AND tenant_id = $2",
                            uuid.UUID(doc_id), tenant_id,
                        )
                        object_id = str(row["object_id"]) if row and row["object_id"] else ""
            except Exception:  # noqa: BLE001
                object_id = ""
        if not object_id:
            # Final fallback (dev/e2e): use storage_path as object key.
            object_id = storage_path

        updater = await self._get_updater()

        # Idempotency: skip if already ready (SPEC §5.4 at-least-once).
        current = await self._get_current_status(doc_id, tenant_id)
        if current == STATUS_READY:
            logger.info("parse_worker: doc %s already ready, skipping", doc_id)
            return

        # pending → parsing
        # #r5: Check the return value — if the UPDATE didn't match a row,
        # the document may have been deleted; abort the pipeline.
        updated = await updater.update(
            tenant_id=tenant_id, doc_id=doc_id, parse_status=STATUS_PARSING,
        )
        if not updated:
            logger.warning(
                "parse_worker: doc %s not found in kb_documents (parse_status "
                "update matched 0 rows), skipping", doc_id,
            )
            return

        local_path: str | None = None
        try:
            # 1. Download from Core API (SPEC §5.1). #3: use object_id (UUID)
            #    for Core API, not MinIO storage_path.
            local_path = await self.core_api.download_object(
                object_id, file_name=file_name
            )

            # 2. Parse (SPEC §5.1 parse_service).
            object_prefix = f"{tenant_id}/{kb_id}/{doc_id}" if tenant_id else f"{kb_id}/{doc_id}"
            nodes = await asyncio.to_thread(
                self.parse_service.parse, local_path, object_prefix
            )

            # 3. Chunk (SPEC §5.1 chunk_service).
            # Per-KB chunk_size is injected from the task payload (not a
            # hard-coded constant). Build a service scoped to this task with
            # that value; when absent fall back to the worker's default
            # ChunkService (whose own default is 1024).
            chunk_svc = self.chunk_service
            if chunk_size:
                chunk_svc = ChunkService(child_chunk_size=int(chunk_size))
            parents, children = await asyncio.to_thread(
                chunk_svc.chunk, nodes
            )

            # 4. Summary (best-effort, SPEC §5.1 / §5.4 degradation).
            summaries = []
            try:
                summary = await asyncio.to_thread(self._summary_service.summarize, parents)
                if summary is not None:
                    summaries.append(summary)
            except Exception as exc:  # noqa: BLE001
                logger.warning("parse_worker: summary failed for doc %s: %s (degrading)", doc_id, exc)

            # parsing → indexing
            await updater.update(
                tenant_id=tenant_id, doc_id=doc_id, parse_status=STATUS_INDEXING,
            )

            # 5. Embed + write to Milvus (SPEC §5.1 embed_service).
            await asyncio.to_thread(
                self._embed_service.embed_and_write,
                tenant_id=tenant_id,
                kb_id=kb_id,
                doc_id=doc_id,
                file_name=file_name,
                parents=parents,
                children=children,
                summaries=summaries,
            )

            # 6. Write kb_chunks (SPEC §5.1 kb_chunks 表写入).
            # #9: When db_pool is None the kb_chunks write is skipped —
            # the document MUST be marked failed, not ready, because the
            # chunk table is a required deliverable for retrieval.
            if self._db_pool is None:
                raise RuntimeError(
                    "parse_worker: db_pool is None — kb_chunks write skipped"
                )
            async with self._db_pool.acquire() as conn:
                chunk_count = await self._chunks_write(
                    conn,
                    tenant_id=tenant_id,
                    kb_id=kb_id,
                    doc_id=doc_id,
                    file_name=file_name,
                    parents=parents,
                    children=children,
                    summaries=summaries,
                )

            # indexing → ready
            await updater.update(
                tenant_id=tenant_id,
                doc_id=doc_id,
                parse_status=STATUS_READY,
                chunk_count=chunk_count,
            )
            logger.info(
                "parse_worker: doc %s ready (parents=%d children=%d summaries=%d)",
                doc_id, len(parents), len(children), len(summaries),
            )
        except Exception as exc:  # noqa: BLE001
            logger.exception("parse_worker: doc %s failed", doc_id)
            try:
                await updater.update(
                    tenant_id=tenant_id,
                    doc_id=doc_id,
                    parse_status=STATUS_FAILED,
                    error_message=_sanitize_error(str(exc)),
                )
            except Exception:  # noqa: BLE001
                logger.exception("parse_worker: failed to mark doc %s as failed", doc_id)
        finally:
            if local_path:
                try:
                    os.unlink(local_path)
                except OSError:
                    pass

    async def _get_current_status(self, doc_id: str, tenant_id: str) -> str | None:
        """Read the current ``parse_status`` for idempotency check.

        Returns ``None`` when the DB is unavailable (skip the idempotency
        guard and proceed) or when the row is not found.
        """
        # #r6: Guard against empty tenant_id across ALL paths (including
        # injected status_updater), not just the db_pool fallback branch.
        if not tenant_id:
            return None
        # Prefer the injected status_updater (testable path).
        if self._status_updater is not None:
            try:
                return await self._status_updater.current(
                    tenant_id=tenant_id, doc_id=doc_id,
                )
            except Exception:  # noqa: BLE001
                logger.debug("parse_worker: status_updater.current failed; skipping idempotency")
                return None
        if self._db_pool is None or not tenant_id:
            return None
        try:
            updater = await self._get_updater()
            return await updater.current(tenant_id=tenant_id, doc_id=doc_id)
        except Exception:  # noqa: BLE001
            logger.debug("parse_worker: could not read parse_status, skipping idempotency check")
            return None


def _file_name_from_path(storage_path: str) -> str:
    """Derive a file name (with extension) from a MinIO storage path."""
    if not storage_path:
        return ""
    return storage_path.rsplit("/", 1)[-1] if "/" in storage_path else storage_path


# ── NATS connection helper ────────────────────────────────────────────────────


async def connect_nats(url: str | None = None) -> Any:
    """Connect to NATS and return the client (returns None on failure).

    Mirrors kb-service's ``connect_nats`` pattern: a failure degrades to
    "no subscription" rather than crashing the process (SPEC §7.3 — NATS
    outages degrade to delayed dispatch via the outbox).
    """
    try:
        from nats.aio.client import Client as NATSClient

        nc = NATSClient()
        await nc.connect(
            servers=[url or settings.nats_url],
            name="rag-engine-parse-worker",
            max_reconnect_attempts=-1,  # #8: auto-reconnect indefinitely
            reconnect_time_wait=2,
        )
        return nc
    except Exception as exc:  # noqa: BLE001
        # #15: distinguish connection vs configuration errors for ops.
        if "no servers" in str(exc).lower() or "connection refused" in str(exc).lower():
            logger.warning("parse_worker: NATS connect failed (network): %s", exc)
        else:
            logger.warning("parse_worker: NATS connect failed (config/error): %s", exc)
        return None


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit("parse_worker is a library module; start via main.py")
