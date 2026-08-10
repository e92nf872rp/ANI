"""Knowledge base query (RAG) endpoint.

Shares the same ``QAService.chat`` handler as the gRPC Query RPC
(``app.grpc.server.RagEngineServicer.Query``) so both transports return
identical results. kb-service's ``RagEngineClient`` calls this REST
endpoint (it has not yet switched to gRPC transport), so this endpoint
MUST remain functional and in sync with the gRPC servicer.
"""
import asyncio
import logging
import threading

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field

from app.core.config import settings
from app.services.qa_service import build_production_qa_service, QAService
from app.services.retrieve_service import (
    DEFAULT_TOP_K,
    DEFAULT_SCORE_THRESHOLD,
)

logger = logging.getLogger(__name__)
router = APIRouter()


class QueryRequest(BaseModel):
    kb_id: str
    tenant_id: str
    question: str = Field(min_length=1, max_length=2000)
    session_id: str | None = None          # for multi-turn context
    top_k: int = Field(default=5, ge=1, le=20)
    # 0.0 means "use service default"; negative values disable thresholding
    # (consistent with gRPC server, #12).
    score_threshold: float = Field(default=0.3, ge=-1.0, le=1.0)
    idempotency_key: str | None = None  # prevents duplicate billing on retry
    inference_service_name: str = "default"   # which vLLM instance to use
    # vector | hybrid | keyword. Empty → KB default (kb-service injects the
    # KB's retrieval_mode into this field before calling rag-engine).
    retrieval_mode: str = Field(default="hybrid")


class SourceChunk(BaseModel):
    doc_id: str
    file_name: str
    page: int | None
    content: str
    score: float


class QueryResponse(BaseModel):
    answer: str
    sources: list[SourceChunk]
    session_id: str
    input_tokens: int
    output_tokens: int


# Shared QAService instance (constructed lazily on first request to avoid
# importing LLM/embedding deps at module load time). Protected by a lock to
# ensure thread-safe initialization under concurrent requests (matching the
# gRPC server's pattern in app/grpc/server.py).
_qa_service: QAService | None = None
_qa_service_lock = threading.Lock()


def _get_qa_service() -> QAService:
    global _qa_service
    if _qa_service is None:
        with _qa_service_lock:
            if _qa_service is None:
                _qa_service = build_production_qa_service()
    return _qa_service


async def _kb_exists(kb_id: str, tenant_id: str) -> bool:
    """Whether the KB exists (not soft-deleted) for the tenant.

    Queries the ``knowledge_bases`` registry (owned by kb-service) so a query
    against an unknown kb_id returns 404 KB_NOT_FOUND instead of silently
    returning an empty-result stream (SPEC §4.3 pre-stream 404).
    """
    import asyncpg

    try:
        conn = await asyncpg.connect(dsn=settings.pg_dsn, timeout=5)
    except Exception:  # noqa: BLE001 — cannot reach PG; degrade to "exists" so
        # the query still runs rather than failing closed on a DB hiccup.
        logger.warning("kb_exists: PG unavailable, assuming KB exists", exc_info=True)
        return True
    try:
        # RLS on knowledge_bases uses app.current_tenant_id; set it scoped to
        # this same transaction (is_local=true) via the parameter-safe
        # set_config(), then SELECT within that transaction so the setting is
        # still active (avoids leaking into other requests).
        async with conn.transaction():
            await conn.execute(
                "SELECT set_config('app.current_tenant_id', $1, true)", tenant_id
            )
            row = await conn.fetchrow(
                "SELECT 1 FROM knowledge_bases "
                "WHERE id = $1 AND tenant_id = $2 AND status = 'active'",
                kb_id, tenant_id,
            )
        return row is not None
    except Exception:  # noqa: BLE001
        logger.warning("kb_exists: query failed, assuming KB exists", exc_info=True)
        return True
    finally:
        await conn.close()


@router.post("/{kb_id}/query", response_model=QueryResponse)
async def query_kb(kb_id: str, req: QueryRequest) -> QueryResponse:
    """
    Hybrid RAG query: vector search (Milvus) + keyword search (PG pg_trgm) → RRF → LLM.

    When score_threshold is not met for any chunk, returns a structured
    "no relevant content found" response rather than hallucinating an answer.
    """
    if kb_id != req.kb_id:
        raise HTTPException(status_code=400, detail="path and body kb_id must match")
    if not req.tenant_id:
        raise HTTPException(status_code=400, detail="tenant_id is required")
    if not await _kb_exists(kb_id, req.tenant_id):
        raise HTTPException(status_code=404, detail="knowledge base not found")

    score_threshold = req.score_threshold if req.score_threshold != 0 else DEFAULT_SCORE_THRESHOLD

    try:
        # #r4: Offload blocking QAService.chat to a thread — same pattern as
        # gRPC server, avoids blocking the FastAPI event loop.
        result = await asyncio.to_thread(
            _get_qa_service().chat,
            kb_id=req.kb_id,
            question=req.question,
            session_id=req.session_id,
            top_k=req.top_k,
            score_threshold=score_threshold,
            retrieval_mode=req.retrieval_mode or "hybrid",
            tenant_id=req.tenant_id,
            inference_service_name=req.inference_service_name,
        )
    except TimeoutError:
        raise HTTPException(status_code=504, detail="LLM timed out")
    except RuntimeError as exc:
        raise HTTPException(status_code=503, detail=str(exc))
    except Exception as exc:  # noqa: BLE001
        logger.exception("query_kb internal error")
        raise HTTPException(status_code=500, detail="internal server error")

    return QueryResponse(
        answer=result.answer,
        sources=[
            SourceChunk(
                doc_id=s.doc_id,
                file_name=s.file_name,
                page=s.page,
                content=s.content,
                score=s.score,
            )
            for s in result.sources
        ],
        session_id=result.session_id,
        input_tokens=result.input_tokens,
        output_tokens=result.output_tokens,
    )
