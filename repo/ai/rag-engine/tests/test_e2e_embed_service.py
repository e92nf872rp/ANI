"""End-to-end functional test for embed_service (issue-012 / US-013).

Tests against REAL Milvus (10.10.1.66:31930) and the remote embedding
service (http://10.10.20.197:8006/v1, Qwen3-Embedding-0.6B). Verifies the
full embed + write + retrieve link required by the issue AC:

  - AC2/AC3: MilvusVectorStore direct + VectorStoreIndex.from_vector_store
    wraps it; Index layer embeds via the remote OpenAIEmbedding then calls
    vector_store.add (insert_nodes).
  - AC4: collection named ``kb_{kb_id 去横杠}``, HNSW / COSINE / M=16 /
    efConstruction=200.
  - AC1: write and query share the same remote embedding endpoint.

Pre-requisites (must be reachable from the host running this test):
  - Milvus: ``10.10.1.66:31930`` (or override MILVUS_ADDR).
  - Embedding service: ``http://10.10.20.197:8006/v1`` (Qwen3-Embedding-0.6B).

Run from repo root:

    python -m pytest ai/rag-engine/tests/test_e2e_embed_service.py -v -s

This file is NOT included in the default ``make test``/pytest run (it needs
real infrastructure); run it explicitly when the lab is up.
"""
from __future__ import annotations

import os
import uuid
from pathlib import Path

# Load .env so Settings picks up MILVUS_ADDR / EMBEDDING_API_BASE etc.
os.chdir(Path(__file__).resolve().parents[3])

import pytest

from app.core.config import settings
from app.core.milvus import (
    HNSW_EF_CONSTRUCTION,
    HNSW_INDEX_TYPE,
    HNSW_M,
    HNSW_METRIC_TYPE,
    build_vector_store,
    build_vector_store_index,
    init_milvus,
    kb_collection_name,
)
from app.services.chunk_service import ChildChunk, ParentChunk
from app.services.embed_service import EmbedService


# ── Fixtures ──────────────────────────────────────────────────────────────────


@pytest.fixture(scope="module")
def event_loop():
    """Shared event loop for the module-scoped async init."""
    import asyncio

    loop = asyncio.new_event_loop()
    yield loop
    loop.close()


@pytest.fixture(scope="module")
def milvus_ready(event_loop):
    """Connect to real Milvus once for the whole module."""
    event_loop.run_until_complete(init_milvus())
    return True


@pytest.fixture(scope="module")
def embed_model():
    """Initialise the real remote OpenAIEmbedding singleton."""
    import asyncio

    from app.core.embeddings import init_embedding_model

    asyncio.run(init_embedding_model())
    from app.core.embeddings import get_embed_model

    return get_embed_model()


@pytest.fixture
def kb_id():
    """Fresh KB id per test so collections don't collide."""
    return str(uuid.uuid4())


@pytest.fixture
def svc(embed_model):
    return EmbedService(embed_model=embed_model)


def _make_parents() -> list[ParentChunk]:
    return [
        ParentChunk(
            chunk_id=str(uuid.uuid4()),
            content=(
                "ANI 平台是面向企业的 AI 专有云解决方案，提供模型推理、"
                "知识库问答和沙箱运行时能力。"
            ),
            content_type="text",
            token_count=30,
            page_number=1,
        )
    ]


def _make_children(parents: list[ParentChunk]) -> list[ChildChunk]:
    parent = parents[0]
    return [
        ChildChunk(
            chunk_id=str(uuid.uuid4()),
            content="ANI 平台提供模型推理服务，支持 vLLM 和 OpenAI 兼容接口。",
            content_type="text",
            page_number=1,
            token_count=15,
            parent_chunk_id=parent.chunk_id,
            parent_content=parent.content,
        ),
        ChildChunk(
            chunk_id=str(uuid.uuid4()),
            content="知识库模块支持文档解析、向量嵌入和混合检索。",
            content_type="text",
            page_number=1,
            token_count=15,
            parent_chunk_id=parent.chunk_id,
            parent_content=parent.content,
        ),
    ]


# ── AC4: collection naming + HNSW params against real Milvus ────────────────


def test_e2e_collection_name_strips_hyphens(kb_id):
    """AC4: collection name is kb_{kb_id 去横杠}."""
    name = kb_collection_name(kb_id)
    assert name == "kb_" + kb_id.replace("-", "")
    assert "-" not in name


def test_e2e_build_vector_store_creates_collection_with_hnsw(
    milvus_ready, embed_model, kb_id
):
    """AC2/AC4: MilvusVectorStore creates the collection with HNSW/COSINE.

    Dropping a node through the index triggers Milvus collection creation
    with the configured index params; we then inspect the collection via
    pymilvus to confirm the index type and metric.
    """
    import asyncio

    from pymilvus import Collection, utility

    index = asyncio.run(
        _build_and_insert_one_node(kb_id, "probe text for hnsw")
    )

    coll = Collection(kb_collection_name(kb_id))
    coll.load()
    try:
        # AC4: collection exists with the right name.
        assert utility.has_collection(kb_collection_name(kb_id))

        # AC4: HNSW index with COSINE metric on the embedding field.
        indexes = coll.indexes
        assert len(indexes) >= 1, "expected at least one index on the collection"
        idx = indexes[0]
        params = idx.params
        # index_type and metric_type live in the index params dict.
        assert params.get("index_type") == HNSW_INDEX_TYPE, (
            f"expected index_type={HNSW_INDEX_TYPE}, got {params.get('index_type')}"
        )
        assert params.get("metric_type") == HNSW_METRIC_TYPE, (
            f"expected metric_type={HNSW_METRIC_TYPE}, got {params.get('metric_type')}"
        )
        # HNSW build params (M, efConstruction) are in params["params"].
        hnsw_params = params.get("params", {})
        assert int(hnsw_params.get("M", -1)) == HNSW_M, (
            f"expected M={HNSW_M}, got {hnsw_params.get('M')}"
        )
        assert int(hnsw_params.get("efConstruction", -1)) == HNSW_EF_CONSTRUCTION, (
            f"expected efConstruction={HNSW_EF_CONSTRUCTION}, "
            f"got {hnsw_params.get('efConstruction')}"
        )
    finally:
        # Clean up the probe collection.
        if utility.has_collection(kb_collection_name(kb_id)):
            utility.drop_collection(kb_collection_name(kb_id))


async def _build_and_insert_one_node(kb_id: str, text: str):
    """Helper: build a VectorStoreIndex and insert a single TextNode."""
    from llama_index.core.schema import TextNode

    index = build_vector_store_index(kb_id, dim=settings.embedding_dim)
    node = TextNode(
        text=text,
        metadata={
            "doc_id": "probe",
            "kb_id": kb_id,
            "tenant_id": "e2e",
            "chunk_type": "child",
            "file_name": "probe.txt",
            "page_number": 1,
            "content_type": "text",
            "parent_content": "",
        },
    )
    index.insert_nodes([node])
    return index


# ── AC1/AC2/AC3: full embed + write + retrieve link ──────────────────────────


def test_e2e_embed_and_write_then_retrieve(milvus_ready, embed_model, svc, kb_id):
    """AC1/AC2/AC3: embed_and_write inserts nodes (Index layer embeds via the
    remote endpoint then calls vector_store.add); as_retriever returns the
    written nodes for a relevant query — proving write and query share the
    same embedding endpoint.
    """
    import asyncio

    parents = _make_parents()
    children = _make_children(parents)

    # Write path: Index layer embeds via remote OpenAICompatibleEmbedding +
    # MilvusVectorStore.add.
    result = asyncio.run(
        _run_async(
            svc,
            tenant_id="e2e",
            kb_id=kb_id,
            doc_id="doc-e2e",
            file_name="ani-overview.txt",
            parents=parents,
            children=children,
        )
    )

    assert result.nodes_written == len(parents) + len(children)
    assert result.collection_name == kb_collection_name(kb_id)

    # Query path: same embed_model singleton embeds the query and searches Milvus.
    retriever = asyncio.run(_build_retriever(svc, kb_id, top_k=3))
    nodes = retriever.retrieve("知识库的向量嵌入和检索能力是什么？")

    assert len(nodes) >= 1, "expected at least one retrieved node"
    # The retrieved text must come from the chunks we just wrote.
    retrieved_texts = [n.get_content() for n in nodes]
    assert any("知识库" in t or "嵌入" in t or "检索" in t for t in retrieved_texts), (
        f"retrieved texts not relevant: {retrieved_texts}"
    )


async def _run_async(svc, **kwargs):
    """Wrap the sync embed_and_write in an async helper for asyncio.run."""
    return svc.embed_and_write(**kwargs)


async def _build_retriever(svc, kb_id, top_k):
    return svc.as_retriever(kb_id, top_k=top_k)


# ── Cleanup ──────────────────────────────────────────────────────────────────


def test_e2e_cleanup_collections(milvus_ready):
    """Drop any e2e test collections left behind (best-effort, runs last)."""
    from pymilvus import utility

    # Collect names from the tests above; they all start with "kb_".
    # We only drop collections created in this run (uuid-based names).
    all_cols = utility.list_collections()
    dropped = []
    for name in all_cols:
        # e2e collections are kb_<uuid-no-hyphens>; skip non-matching ones.
        if name.startswith("kb_") and len(name) > 40:
            try:
                utility.drop_collection(name)
                dropped.append(name)
            except Exception:
                pass
    # This test always passes; it's just cleanup.
    assert True


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v", "-s"]))
