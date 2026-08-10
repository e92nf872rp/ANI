"""rag-engine gRPC server — Query RPC (SPEC §4.1, §5.1, US-015).

Implements the synchronous ``Query`` RPC defined in ``rag.proto``. The RPC
mirrors ``kb_service.proto``'s ``Query`` messages (same field numbers) so
kb-service can forward queries unchanged.

Flow (SPEC §5.1)::

    QueryRequest → QAService.chat() → QueryResponse
                 ↳ RetrieveService (hybrid + RRF + parent backfill)
                 ↳ ContextChatEngine (Redis memory + vLLM)
                 → answer + sources + session_id + tokens

Error mapping (SPEC §4.4)::

    INVALID_ARGUMENT  — question empty / top_k out of range
    NOT_FOUND         — kb_id collection missing (handled by retriever)
    UNAVAILABLE       — vLLM / Milvus unavailable
    DEADLINE_EXCEEDED — LLM timeout

The server runs on a background thread with its own ``grpc.aio`` event loop
so it cooperates with FastAPI's event loop when both are started in the same
process (``main.py`` starts the gRPC server in a separate thread to keep
the FastAPI loop responsive). The ``Query`` RPC is ``async def`` and offloads
the blocking ``QAService.chat()`` call to a thread via ``asyncio.to_thread``
so the aio server's event loop is never blocked (#6).
"""
from __future__ import annotations

import asyncio
import logging
import threading
from typing import Any

import grpc

from app.grpc import rag_pb2 as rag_pb
from app.grpc import rag_pb2_grpc as rag_grpc
from app.core.config import settings
from app.services.qa_service import QAService, NO_RESULT_ANSWER, build_production_qa_service
from app.services.retrieve_service import (
    DEFAULT_TOP_K,
    DEFAULT_SCORE_THRESHOLD,
)

logger = logging.getLogger(__name__)

# gRPC server default bind address. Override via settings.grpc_bind_addr.
DEFAULT_GRPC_BIND = "[::]:50052"

# Validation bounds (SPEC §5.2 / §7.2).
TOP_K_MIN = 1
TOP_K_MAX = 20
QUESTION_MIN_LEN = 1
QUESTION_MAX_LEN = 2000


class RagEngineServicer(rag_grpc.RagEngineServicer):
    """Implements the Query RPC (SPEC §4.1).

    Args:
        qa_service: The :class:`QAService` used to run synchronous RAG QA.
            When ``None`` a default is constructed lazily so the server can
            be instantiated in environments without a live vLLM endpoint
            (tests inject a mock ``qa_service``).
    """

    def __init__(self, qa_service: QAService | None = None) -> None:
        self._qa_service = qa_service
        # #7: lock guards lazy QAService initialization across concurrent
        # threads when the gRPC server uses a thread pool executor.
        self._qa_lock = threading.Lock()

    @property
    def qa_service(self) -> QAService:
        if self._qa_service is not None:
            return self._qa_service
        with self._qa_lock:
            if self._qa_service is None:
                self._qa_service = build_production_qa_service()
        return self._qa_service

    async def Query(
        self,
        request: rag_pb.QueryRequest,
        context: grpc.aio.ServicerContext,
    ) -> rag_pb.QueryResponse:
        """Async RAG query (SPEC §4.1, §5.1).

        Validates the request, offloads the blocking ``QAService.chat`` to a
        thread via ``asyncio.to_thread`` (#6: never blocks the aio event
        loop), and maps the result to ``QueryResponse``. Maps errors to gRPC
        status codes per SPEC §4.4.
        """
        # ── Validation (SPEC §7.2) ──────────────────────────────────────────
        # #8: Validate tenant_id/kb_id non-empty (kb-service validates these too).
        if not request.tenant_id:
            await context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "tenant_id is required"
            )
            return
        if not request.kb_id:
            await context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "kb_id is required"
            )
            return
        if not request.question or len(request.question) < QUESTION_MIN_LEN:
            await context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "question must not be empty"
            )
            return  # unreachable; for type checkers
        if len(request.question) > QUESTION_MAX_LEN:
            await context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"question length must be <= {QUESTION_MAX_LEN}",
            )
            return
        # top_k: proto default 0 means "use service default" (SPEC §4.1).
        top_k = request.top_k if request.top_k else DEFAULT_TOP_K
        if not (TOP_K_MIN <= top_k <= TOP_K_MAX):
            await context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"top_k must be in [{TOP_K_MIN}, {TOP_K_MAX}]; got {top_k}",
            )
            return
        # score_threshold: proto default 0.0 means "use service default".
        # Nonzero values are used as-is (negative = disable thresholding).
        score_threshold = (
            request.score_threshold if request.score_threshold != 0 else DEFAULT_SCORE_THRESHOLD
        )

        # retrieval_mode: vector | hybrid | keyword. Empty → hybrid (KB
        # default is injected upstream by kb-service when not overridden).
        retrieval_mode = request.retrieval_mode or "hybrid"
        if retrieval_mode not in ("vector", "hybrid", "keyword"):
            await context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"retrieval_mode must be one of vector|hybrid|keyword; got {retrieval_mode}",
            )
            return

        # ── Run RAG QA in a thread (#6: avoid blocking the aio event loop) ─
        try:
            result = await asyncio.to_thread(
                self.qa_service.chat,
                kb_id=request.kb_id,
                question=request.question,
                session_id=request.session_id or None,
                top_k=top_k,
                score_threshold=score_threshold,
                retrieval_mode=retrieval_mode,
                tenant_id=request.tenant_id,
                inference_service_name=request.inference_service_name or "default",
            )
        except TimeoutError as exc:
            logger.warning("rag-engine Query LLM timeout: %s", exc)
            await context.abort(grpc.StatusCode.DEADLINE_EXCEEDED, "LLM timed out")
            return
        except RuntimeError as exc:
            # vLLM/Milvus unavailable → UNAVAILABLE (SPEC §4.4).
            logger.warning("rag-engine Query backend unavailable: %s", exc)
            await context.abort(grpc.StatusCode.UNAVAILABLE, str(exc))
            return
        except Exception as exc:  # noqa: BLE001 — surface as INTERNAL
            logger.exception("rag-engine Query failed")
            await context.abort(grpc.StatusCode.INTERNAL, str(exc))
            return

        # ── Build response ──────────────────────────────────────────────────
        response = rag_pb.QueryResponse(
            answer=result.answer,
            session_id=result.session_id,
            input_tokens=result.input_tokens,
            output_tokens=result.output_tokens,
        )
        for src in result.sources:
            response.sources.add(
                doc_id=src.doc_id,
                file_name=src.file_name,
                page=src.page or 0,
                content=src.content,
                score=src.score,
            )
        return response


class GrpcServer:
    """Manages the gRPC server lifecycle.

    The server runs on a background thread with its own ``grpc.aio`` event
    loop so it can coexist with FastAPI. ``start()`` is non-blocking;
    ``stop()`` gracefully drains via ``run_coroutine_threadsafe`` (#1).
    """

    def __init__(
        self,
        *,
        bind_addr: str | None = None,
        servicer: RagEngineServicer | None = None,
    ) -> None:
        self._bind_addr = bind_addr or getattr(settings, "grpc_bind_addr", DEFAULT_GRPC_BIND)
        self._servicer = servicer or RagEngineServicer()
        self._server: grpc.aio.Server | None = None
        self._thread: threading.Thread | None = None
        self._started = threading.Event()
        # #1: event loop owned by the background thread; stop() uses
        # run_coroutine_threadsafe to schedule stop() on the correct loop.
        self._loop: asyncio.AbstractEventLoop | None = None

    @property
    def bind_addr(self) -> str:
        return self._bind_addr

    @property
    def servicer(self) -> RagEngineServicer:
        return self._servicer

    def start(self) -> None:
        """Start the gRPC server on a background thread (non-blocking)."""
        if self._server is not None:
            return
        self._thread = threading.Thread(target=self._run, name="rag-grpc-server", daemon=True)
        self._thread.start()
        self._started.wait(timeout=10.0)

    def _run(self) -> None:
        # #1: capture this thread's event loop so stop() can schedule on it.
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        self._loop = loop
        try:
            loop.run_until_complete(self._serve_async())
        finally:
            loop.close()
            self._loop = None

    async def _serve_async(self) -> None:
        self._server = grpc.aio.server()
        rag_grpc.add_RagEngineServicer_to_server(self._servicer, self._server)
        self._server.add_insecure_port(self._bind_addr)
        await self._server.start()
        self._started.set()
        logger.info("rag-engine gRPC server listening on %s", self._bind_addr)
        await self._server.wait_for_termination()

    def stop(self, grace: float = 5.0) -> None:
        """Stop the gRPC server gracefully.

        #1: Uses ``asyncio.run_coroutine_threadsafe`` to schedule
        ``server.stop()`` on the background thread's event loop (where the
        aio server was created), rather than the caller's loop.
        """
        if self._server is None:
            return
        loop = self._loop
        if loop is not None and not loop.is_closed():
            try:
                future = asyncio.run_coroutine_threadsafe(
                    self._server.stop(grace=grace), loop
                )
                # Wait for the stop coroutine to complete (bounded by grace).
                future.result(timeout=grace + 2)
            except Exception as exc:  # noqa: BLE001
                logger.warning("rag-engine gRPC server stop failed: %s", exc)
        if self._thread is not None:
            self._thread.join(timeout=grace + 2)
            self._thread = None
        self._server = None
        self._loop = None


def serve(
    *,
    bind_addr: str | None = None,
    qa_service: QAService | None = None,
    block: bool = True,
) -> GrpcServer:
    """Start a gRPC server (convenience entrypoint).

    Args:
        bind_addr: ``host:port`` to bind. Defaults to settings.grpc_bind_addr
            or ``[::]:50052``.
        qa_service: Optional QAService injection (tests).
        block: When ``True`` blocks the calling thread until the server
            terminates (process entrypoint). When ``False`` starts in the
            background and returns the :class:`GrpcServer`.
    """
    server = GrpcServer(bind_addr=bind_addr, servicer=RagEngineServicer(qa_service=qa_service))
    if block:
        # #14: unified path — reuse GrpcServer._serve_async instead of a
        # duplicate _serve_blocking function.
        asyncio.run(server._serve_async())
    else:
        server.start()
    return server


if __name__ == "__main__":  # pragma: no cover
    logging.basicConfig(level=logging.INFO)
    serve(block=True)
