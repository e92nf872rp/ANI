"""Fake rag-engine — minimal health stub for E2E test scaffolding.

Provides a /health endpoint that reports gRPC server status, matching the
real rag-engine health contract. The actual rag-engine now exposes only
gRPC RPCs (Parse/Embed/Generate/GenerateStream); this fake is used only for
E2E test infrastructure that probes rag-engine health.
"""
from fastapi import FastAPI
import uvicorn

app = FastAPI(title="fake-rag-engine")


@app.get("/health")
async def health():
    return {"status": "ok", "grpc_server": True}


if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=8005)
