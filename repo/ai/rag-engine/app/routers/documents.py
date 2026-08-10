"""Document ingestion and parsing endpoints."""
import logging
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

logger = logging.getLogger(__name__)

router = APIRouter()


class ParseRequest(BaseModel):
    kb_id: str
    doc_id: str
    tenant_id: str
    storage_path: str   # MinIO object path
    file_type: str      # pdf | docx | xlsx | txt | md
    # Idempotency: if chunk records already exist for this doc_id, skip re-parsing
    idempotency_key: str


class ParseResponse(BaseModel):
    doc_id: str
    chunk_count: int
    status: str


@router.post("/{kb_id}/documents/{doc_id}/parse", response_model=ParseResponse)
async def parse_document(kb_id: str, doc_id: str, req: ParseRequest) -> ParseResponse:
    """
    Parse a document synchronously and index its chunks into Milvus.

    This is a synchronous fallback to the NATS-based async parse_worker.
    It runs the same pipeline (download → parse → chunk → embed → write)
    in-process so callers can test the full flow without NATS.

    Idempotent: calling with the same doc_id twice is safe — the
    parse_worker checks parse_status and skips if already 'ready'.
    """
    if kb_id != req.kb_id or doc_id != req.doc_id:
        raise HTTPException(status_code=400, detail="path and body kb_id/doc_id must match")

    # Import the module-level parse_worker from main.py
    import main as _main

    if _main._parse_worker is None:
        # No NATS worker — build a transient worker for synchronous processing
        from app.workers.parse_worker import ParseWorker

        worker = ParseWorker(nats_client=None, db_pool=_main._db_pool)
    else:
        worker = _main._parse_worker

    # Build the payload in the same shape as the outbox/NATS message
    payload = {
        "doc_id": doc_id,
        "kb_id": kb_id,
        "tenant_id": req.tenant_id,
        "storage_path": req.storage_path,
        "file_name": req.storage_path.rsplit("/", 1)[-1] if "/" in req.storage_path else req.storage_path,
    }

    # Run the pipeline synchronously (await the async process_message)
    await worker.process_message(payload)

    # Read the final parse_status to report the result
    try:
        updater = await worker._get_updater()
        status = await updater.current(tenant_id=req.tenant_id, doc_id=doc_id)
        if status is None:
            status = "unknown"
    except Exception as exc:
        logger.warning("parse endpoint: could not read final status: %s", exc)
        status = "unknown"

    # Read chunk_count from kb_documents
    chunk_count = 0
    try:
        if _main._db_pool is not None:
            async with _main._db_pool.acquire() as conn:
                row = await conn.fetchrow(
                    "SELECT chunk_count FROM kb_documents WHERE id = $1",
                    __import__("uuid").UUID(doc_id),
                )
                if row:
                    chunk_count = row["chunk_count"]
    except Exception as exc:
        logger.warning("parse endpoint: could not read chunk_count: %s", exc)

    return ParseResponse(doc_id=doc_id, chunk_count=chunk_count, status=status)


@router.delete("/{kb_id}/documents/{doc_id}/index")
async def delete_document_index(kb_id: str, doc_id: str) -> dict:
    """Remove all vectors for a document from Milvus. Idempotent."""
    try:
        from app.core.milvus import get_milvus_client
        from app.core.config import settings

        client = get_milvus_client()
        if client is None:
            return {"deleted": False, "doc_id": doc_id, "error": "Milvus not connected"}

        collection_name = f"kb_{kb_id}"
        # Check if collection exists
        collections = client.list_collections()
        if collection_name not in collections:
            return {"deleted": True, "doc_id": doc_id, "note": "collection not found"}

        # Delete by expression
        client.delete(collection_name=collection_name, expr=f'doc_id == "{doc_id}"')
        return {"deleted": True, "doc_id": doc_id}
    except Exception as exc:
        logger.warning("delete index failed: %s", exc)
        return {"deleted": False, "doc_id": doc_id, "error": str(exc)}
