"""
ANI RAG Engine Service
Provides document parsing, vector indexing, and hybrid retrieval for knowledge bases.

US-015 wires the gRPC server (Query RPC) and the NATS parse_worker into the
FastAPI lifespan so the process serves both the REST health endpoint and the
gRPC Query RPC + the async parse pipeline concurrently.
"""

import logging
from contextlib import asynccontextmanager
from typing import TYPE_CHECKING, Any

import asyncpg
import uvicorn
from app.core.config import settings
from app.core.embeddings import init_embedding_model
from app.core.milvus import init_milvus
from app.routers import documents, query
from fastapi import FastAPI

if TYPE_CHECKING:
    from app.grpc.server import GrpcServer
    from app.workers.parse_worker import ParseWorker
    from nats.aio.client import Client as NATSClient

logger = logging.getLogger(__name__)

# Module-level handles for the gRPC server and parse_worker so the lifespan
# can start/stop them cleanly and the health endpoint can report status.
# #13: typed annotations for type safety.
_grpc_server: "GrpcServer | None" = None
_parse_worker: "ParseWorker | None" = None
_nats_client: "NATSClient | None" = None
_db_pool: Any = None  # asyncpg.Pool | None


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup: initialize connections and models
    global _grpc_server, _parse_worker, _nats_client, _db_pool
    await init_milvus()
    await init_embedding_model(settings.embedding_model)

    # #r1: Create the asyncpg pool for parse_worker (kb_chunks writes +
    # parse_status updates). Without this the production worker crashes on
    # every message at _get_updater().
    try:
        _db_pool = await asyncpg.create_pool(
            dsn=settings.pg_dsn, min_size=1, max_size=4,
        )
        logger.info("asyncpg pool created (dsn=%s)", settings.pg_dsn)
    except Exception as exc:  # noqa: BLE001
        logger.warning("asyncpg pool creation failed: %s", exc)
        _db_pool = None

    # Start the gRPC server (Query RPC, SPEC §4.1) on a background thread.
    try:
        from app.grpc.server import GrpcServer, RagEngineServicer
        from app.services.qa_service import build_production_qa_service

        _grpc_server = GrpcServer(servicer=RagEngineServicer(qa_service=build_production_qa_service()))
        _grpc_server.start()
        logger.info("gRPC server started on %s", _grpc_server.bind_addr)
    except Exception as exc:  # noqa: BLE001
        logger.warning("gRPC server failed to start: %s", exc)
        _grpc_server = None

    # Start the NATS parse_worker (SPEC §5.1, US-015). NATS is optional — a
    # connection failure degrades to "no subscription" rather than crashing
    # the process (SPEC §7.3 — outbox retains work for later dispatch).
    # #8: connect_nats now configures auto-reconnect (max_reconnect_attempts=-1)
    # so the NATS client self-heals when the broker recovers without a process
    # restart. The subscription survives transient outages.
    try:
        from app.workers.parse_worker import ParseWorker, connect_nats

        _nats_client = await connect_nats()
        if _nats_client is not None:
            # Wire the MinIO image uploader so embedded images in
            # docx/xlsx/pptx are extracted & uploaded at parse time (SPEC §4.4).
            # Best-effort: if MinIO is unavailable, degrade to None (image
            # extraction is skipped, text/table parsing still proceeds).
            image_uploader = None
            try:
                from app.clients.minio_client import ImageUploader
                image_uploader = ImageUploader()
            except Exception as exc:  # noqa: BLE001
                logger.warning("image uploader unavailable, image extraction disabled: %s", exc)

            _parse_worker = ParseWorker(
                nats_client=_nats_client, db_pool=_db_pool,
                image_uploader=image_uploader,
            )
            await _parse_worker.start()
            logger.info("parse_worker started (subject=%s)", settings.nats_parse_subject)
        else:
            logger.warning("parse_worker not started (NATS unavailable)")
            _parse_worker = None
    except Exception as exc:  # noqa: BLE001
        logger.warning("parse_worker failed to start: %s", exc)
        _parse_worker = None

    yield
    # Shutdown: cleanup
    if _parse_worker is not None:
        try:
            await _parse_worker.stop()
        except Exception:  # noqa: BLE001, S110 — best-effort shutdown
            pass
        _parse_worker = None
    if _nats_client is not None:
        try:
            await _nats_client.drain()
        except Exception:  # noqa: BLE001, S110 — best-effort shutdown
            pass
        _nats_client = None
    if _grpc_server is not None:
        try:
            _grpc_server.stop()
        except Exception:  # noqa: BLE001, S110 — best-effort shutdown
            pass
        _grpc_server = None
    if _db_pool is not None:
        try:
            await _db_pool.close()
        except Exception:  # noqa: BLE001, S110 — best-effort shutdown
            pass
        _db_pool = None
    # Close the CoreApiClient singleton to release its httpx connection pool.
    try:
        from app.clients.core_api import close_core_client
        await close_core_client()
    except Exception:  # noqa: BLE001, S110 — best-effort shutdown
        pass


app = FastAPI(
    title="ANI RAG Engine",
    version="1.0.0",
    lifespan=lifespan,
)

app.include_router(documents.router, prefix="/api/v1/kb", tags=["documents"])
app.include_router(query.router, prefix="/api/v1/kb", tags=["query"])


@app.get("/health")
async def health():
    return {
        "status": "ok",
        "grpc_server": _grpc_server is not None,
        "parse_worker": _parse_worker is not None,
        "db_pool": _db_pool is not None,
    }


if __name__ == "__main__":
    uvicorn.run("main:app", host="0.0.0.0", port=8001, reload=False)
