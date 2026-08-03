"""ANI kb-service entrypoint (SPEC §2.4).

Starts the KBService gRPC server on the configured port. FastAPI is exposed
alongside for health/readiness; business RPCs are served over gRPC.
"""
import os
import sys
from concurrent import futures

import grpc
import uvicorn
from fastapi import FastAPI

# Make both the kb-service package root (for `app.*` imports) and the
# generated stubs root (for top-level `common.v1` / `kb.v1` imports used by
# the protoc-generated grpc code) importable regardless of CWD.
_HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, _HERE)                       # so `import app...` works
sys.path.insert(0, os.path.join(_HERE, "app", "generated"))  # so `import common.v1` / `kb.v1` works

from app.api.grpc_server import KBServiceServicer
from app.core.config import settings
from app.generated.kb.v1 import kb_service_pb2_grpc as pb_grpc


def serve_grpc() -> grpc.Server:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    pb_grpc.add_KBServiceServicer_to_server(KBServiceServicer(), server)
    server.add_insecure_port(f"[::]:{settings.grpc_port}")
    server.start()
    return server


app = FastAPI(title="ANI kb-service", version="1.0.0")


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.get("/readyz")
async def readyz():
    # P0 skeleton: always ready. Real readiness depends on DB/NATS/Redis wired in US-009/US-010.
    return {"status": "ok"}


def main():
    server = serve_grpc()
    print(f"kb-service gRPC server listening on :{settings.grpc_port}", flush=True)
    # FastAPI health endpoints on 8002 (distinct from rag-engine 8001).
    # uvicorn.run blocks on its own signal handlers (SIGINT/SIGTERM); on exit
    # we stop the gRPC server so the process can drain and terminate cleanly.
    try:
        uvicorn.run(app, host="0.0.0.0", port=8002, log_level="info")
    finally:
        server.stop(grace=5).wait_for_termination()


if __name__ == "__main__":
    main()
