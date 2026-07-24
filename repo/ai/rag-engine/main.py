"""
ANI RAG Engine Service
Provides document parsing, vector indexing, and hybrid retrieval for knowledge bases.
"""

from contextlib import asynccontextmanager

import uvicorn

from app.core.config import settings
from app.core.embeddings import init_embedding_model
from app.core.milvus import init_milvus
from app.routers import documents, query
from fastapi import FastAPI


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup: initialize connections and models
    await init_milvus()
    await init_embedding_model(settings.embedding_model)
    yield
    # Shutdown: cleanup


app = FastAPI(
    title="ANI RAG Engine",
    version="1.0.0",
    lifespan=lifespan,
)

app.include_router(documents.router, prefix="/api/v1/kb", tags=["documents"])
app.include_router(query.router, prefix="/api/v1/kb", tags=["query"])


@app.get("/health")
async def health():
    return {"status": "ok"}


if __name__ == "__main__":
    uvicorn.run("main:app", host="0.0.0.0", port=8001, reload=False)
