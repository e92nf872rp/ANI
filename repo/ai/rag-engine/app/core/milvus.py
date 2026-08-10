"""Milvus connection and collection helpers (US-013 / SPEC §3.1, §5.1).

Provides direct Milvus access via LlamaIndex :class:`MilvusVectorStore`
(package ``llama-index-vector-stores-milvus``) — the v1.2 architecture
(SPEC §1.3) removes the ``CoreAPIVectorStore`` adapter, so rag-engine talks
to Milvus directly and lets :class:`VectorStoreIndex` perform the embedding
before calling ``vector_store.add``.

Collection naming + index params (SPEC §3.1):

* Collection: ``kb_{kb_id 去横杠}`` (hyphens stripped — Milvus collection
  names must be alphanumeric + underscore).
* Vector index: HNSW, metric = COSINE, M = 16, efConstruction = 200.
"""
from __future__ import annotations

import logging
from typing import TYPE_CHECKING

from pymilvus import MilvusClient, connections

from app.core.config import settings

if TYPE_CHECKING:
    from llama_index.core.embeddings import BaseEmbedding

# HNSW index parameters (SPEC §3.1).
HNSW_INDEX_TYPE = "HNSW"
HNSW_METRIC_TYPE = "COSINE"
HNSW_M = 16
HNSW_EF_CONSTRUCTION = 200

_connected = False


async def init_milvus() -> None:
    """Open the default Milvus connection once at startup.

    Runs the blocking ``connections.connect`` in a worker thread with a
    timeout so the FastAPI lifespan is not blocked indefinitely if the Milvus
    broker is unreachable. A connection failure logs a warning and sets
    ``_connected = False``; callers (parse_worker, query) handle the
    ``ConnectionNone`/``NotConnected`` exceptions at call time.
    """
    import asyncio

    global _connected
    if _connected:
        return

    def _connect() -> None:
        connections.connect(
            alias="default",
            host=settings.milvus_host,
            port=settings.milvus_port,
            timeout=10,  # seconds
        )

    try:
        await asyncio.wait_for(asyncio.to_thread(_connect), timeout=15)
        _connected = True
        logging.getLogger(__name__).info(
            "Milvus connected (host=%s, port=%s)", settings.milvus_host, settings.milvus_port,
        )
    except Exception as exc:  # noqa: BLE001 — degrade gracefully on connect failure
        logging.getLogger(__name__).warning(
            "Milvus connection failed (host=%s, port=%s): %s — "
            "parse/query will degrade gracefully",
            settings.milvus_host, settings.milvus_port, exc,
        )
        _connected = False


def kb_collection_name(kb_id: str) -> str:
    """Derive a stable Milvus collection name from a knowledge base UUID.

    Strips hyphens: Milvus collection names must be alphanumeric + underscore
    (SPEC §3.1: ``kb_{kb_id 去横杠}``).
    """
    return "kb_" + kb_id.replace("-", "")


def _milvus_uri() -> str:
    """Build a Milvus URI string from settings (host:port).

    pymilvus 2.5+ requires the URI to carry a scheme prefix; ``tcp://`` is
    used for a plain gRPC connection to a standalone/cluster Milvus.
    """
    return f"tcp://{settings.milvus_host}:{settings.milvus_port}"


def get_milvus_client() -> MilvusClient | None:
    """Return a connected :class:`MilvusClient` or ``None`` if Milvus is down.

    Used by low-level admin/document endpoints (e.g. vector delete) that
    need pymilvus's ``list_collections`` / ``delete`` API directly. Returns
    ``None`` when the connection cannot be established so callers can degrade
    gracefully.
    """
    try:
        return MilvusClient(uri=_milvus_uri())
    except Exception as exc:  # noqa: BLE001 — degrade to None when Milvus is down
        logging.getLogger(__name__).warning(
            "MilvusClient unavailable (host=%s, port=%s): %s",
            settings.milvus_host, settings.milvus_port, exc,
        )
        return None



def build_vector_store(kb_id: str, *, dim: int | None = None):
    """Construct a LlamaIndex :class:`MilvusVectorStore` for a KB collection.

    The collection is named ``kb_{kb_id 去横杠}`` and uses HNSW + COSINE
    with M=16 / efConstruction=200 (SPEC §3.1, §5.1).

    Args:
        kb_id: Knowledge base UUID (hyphens stripped for the collection name).
        dim: Embedding dimension. Passed as ``dim`` so Milvus creates the
            collection with the correct vector size when it does not yet
            exist (SPEC §3.1).

    Returns:
        ``llama_index.vector_stores.milvus.MilvusVectorStore``.
    """
    from llama_index.vector_stores.milvus import MilvusVectorStore

    # MilvusVectorStore 1.1.0 takes the index spec via ``index_config`` and the
    # metric via ``similarity_metric`` (flat kwargs like index_type/M/... are
    # not recognised and silently fall back to FLAT). SPEC §3.1 requires
    # HNSW / COSINE / M=16 / efConstruction=200.
    index_config = {
        "index_type": HNSW_INDEX_TYPE,
        "metric_type": HNSW_METRIC_TYPE,
        "params": {
            "M": HNSW_M,
            "efConstruction": HNSW_EF_CONSTRUCTION,
        },
    }

    return MilvusVectorStore(
        uri=_milvus_uri(),
        collection_name=kb_collection_name(kb_id),
        dim=dim if dim is not None else settings.embedding_dim,
        index_config=index_config,
        similarity_metric=HNSW_METRIC_TYPE,
        overwrite=False,
        # Force sync GrpcHandler — avoids "Event loop is closed" when the
        # sync retrieve path is offloaded via asyncio.to_thread (pymilvus's
        # AsyncGrpcHandler binds asyncio.Lock/create_task to a loop that is
        # closed when the to_thread worker exits).
        use_async_client=False,
    )


def build_vector_store_index(
    kb_id: str,
    *,
    dim: int | None = None,
    embed_model: BaseEmbedding | None = None,
):
    """Wrap a Milvus collection in a :class:`VectorStoreIndex`.

    Per SPEC §5.1 the Index layer performs the embedding and then calls
    ``vector_store.add`` — callers MUST NOT embed manually before calling
    ``index.insert_nodes`` / ``index.as_retriever``.

    Args:
        kb_id: Knowledge base UUID.
        dim: Embedding dimension (used when the collection does not exist yet).
        embed_model: LlamaIndex embedding model. When ``None`` the global
            singleton from :func:`app.core.embeddings.get_embed_model` is used
            so write and query paths share the same model (SPEC §1.3).
    """
    from llama_index.core import VectorStoreIndex

    if embed_model is None:
        from app.core.embeddings import get_embed_model

        embed_model = get_embed_model()

    vector_store = build_vector_store(kb_id, dim=dim)
    return VectorStoreIndex.from_vector_store(
        vector_store,
        embed_model=embed_model,
    )
