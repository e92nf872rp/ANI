"""ANI RAG Engine Service.

Provides stateless gRPC RPCs (Parse/Embed/Generate/GenerateStream) for
kb-service orchestration. The FastAPI lifespan starts the gRPC server and
serves a health endpoint.
"""

import logging
from contextlib import asynccontextmanager
from typing import TYPE_CHECKING

import uvicorn
from app.core.config import settings
from app.core.embeddings import init_embedding_model
from fastapi import FastAPI

if TYPE_CHECKING:
    from app.grpc.server import GrpcServer

logger = logging.getLogger(__name__)

# Module-level handle for the gRPC server so the lifespan can start/stop it
# cleanly and the health endpoint can report status.
_grpc_server: "GrpcServer | None" = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup: initialize embedding model + gRPC server
    global _grpc_server
    await init_embedding_model(settings.embedding_model)

    # Start the gRPC server (stateless RPCs) on a background thread.
    try:
        from app.grpc.server import GrpcServer, RagEngineServicer

        _grpc_server = GrpcServer(servicer=RagEngineServicer())
        _grpc_server.start()
        logger.info("gRPC server started on %s", _grpc_server.bind_addr)
    except Exception as exc:  # noqa: BLE001
        logger.warning("gRPC server failed to start: %s", exc)
        _grpc_server = None

    yield
    # Shutdown: cleanup
    if _grpc_server is not None:
        try:
            _grpc_server.stop()
        except Exception:  # noqa: BLE001, S110 — best-effort shutdown
            pass
        _grpc_server = None


app = FastAPI(
    title="ANI RAG Engine",
    version="1.0.0",
    lifespan=lifespan,
)


@app.get("/health")
async def health():
    return {
        "status": "ok",
        "grpc_server": _grpc_server is not None,
    }


if __name__ == "__main__":
    uvicorn.run("main:app", host="0.0.0.0", port=8001, reload=False)
