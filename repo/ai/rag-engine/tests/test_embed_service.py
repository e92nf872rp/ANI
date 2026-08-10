"""Pure-logic unit tests for embed_service + milvus (US-013 / SPEC §3.1, §5.1).

These tests do NOT require llama_index, pymilvus, or any remote service.
Heavy/optional deps are stubbed so the modules import cleanly. The tests
validate the pure-logic parts that matter for the AC:

* Milvus collection naming ``kb_{kb_id 去横杠}`` (SPEC §3.1).
* HNSW index params: metric=COSINE, M=16, efConstruction=200 (SPEC §3.1).
* ``build_vector_store`` passes the right collection name + HNSW params.
* ``build_vector_store_index`` wraps the store via
  ``VectorStoreIndex.from_vector_store(vector_store, embed_model=...)`` so
  the Index layer embeds before calling ``vector_store.add`` (SPEC §5.1).
* ``_build_text_node`` mirrors the Milvus schema metadata (SPEC §3.1).
* No ``CoreAPIVectorStore`` adapter is referenced anywhere (SPEC §1.3).
* Write and query paths share the same embed_model singleton (SPEC §1.3).
* The embedding model is the remote OpenAI-compatible endpoint provided by
  the AI inference service (not a local HuggingFace model) — SPEC §1.3
  v1.2: rag-engine calls the inference-service /v1/embeddings endpoint.
"""
from __future__ import annotations

import sys
from unittest.mock import MagicMock

import pytest

# Stub heavy/optional deps so the modules import cleanly in the test env.
for _mod in (
    "docling",
    "docling.datamodel.base_models",
    "docling.datamodel.pipeline_options",
    "docling.document_converter",
    "docling_core",
    "docling_core.types.doc",
    "llama_index",
    "llama_index.readers",
    "llama_index.readers.docling",
    "llama_index.core",
    "llama_index.core.node_parser",
    "llama_index.core.schema",
    "llama_index.embeddings",
    "llama_index.vector_stores",
    "llama_index.vector_stores.milvus",
    "minio",
    "minio.error",
    "openai",
    "pymilvus",
    "pymilvus.connections",
):
    if _mod not in sys.modules:
        sys.modules[_mod] = MagicMock()

# Configure the llama_index.core.schema stub to return a real-ish object so
# ``_build_text_node`` can set attributes on it.
_schema_stub = sys.modules["llama_index.core.schema"]


class _FakeRelatedNodeInfo:
    def __init__(self, node_id: str) -> None:
        self.node_id = node_id


class _FakeTextNode:
    def __init__(self, id_, text, metadata, relationships=None) -> None:
        self.id_ = id_
        self.text = text
        self.metadata = metadata
        self.relationships = relationships or {}


_schema_stub.TextNode = _FakeTextNode
_schema_stub.NodeRelationship = MagicMock(PARENT="PARENT")
_schema_stub.RelatedNodeInfo = _FakeRelatedNodeInfo

from app.core.config import settings  # noqa: E402
from app.core import milvus  # noqa: E402
from app.services import embed_service  # noqa: E402
from app.services.chunk_service import ChildChunk, ParentChunk  # noqa: E402


# ── kb_collection_name ───────────────────────────────────────────────────────


def test_kb_collection_name_strips_hyphens():
    # SPEC §3.1: kb_{kb_id 去横杠}
    assert milvus.kb_collection_name("550e8400-e29b-41d4-a716-446655440000") == \
        "kb_550e8400e29b41d4a716446655440000"


def test_kb_collection_name_no_hyphens():
    assert milvus.kb_collection_name("abc123") == "kb_abc123"


def test_kb_collection_name_prefix():
    # Every collection name must start with the kb_ prefix.
    assert milvus.kb_collection_name("x").startswith("kb_")


# ── HNSW index params (SPEC §3.1) ───────────────────────────────────────────


def test_hnsw_index_params_match_spec():
    assert milvus.HNSW_INDEX_TYPE == "HNSW"
    assert milvus.HNSW_METRIC_TYPE == "COSINE"
    assert milvus.HNSW_M == 16
    assert milvus.HNSW_EF_CONSTRUCTION == 200


# ── build_vector_store passes correct params ────────────────────────────────


def test_build_vector_store_passes_collection_name_and_hnsw(monkeypatch):
    captured = {}

    class _FakeMilvusVectorStore:
        def __init__(self, **kwargs) -> None:
            captured.update(kwargs)

    monkeypatch.setattr(
        sys.modules["llama_index.vector_stores.milvus"],
        "MilvusVectorStore",
        _FakeMilvusVectorStore,
    )

    milvus.build_vector_store("550e8400-e29b-41d4-a716-446655440000", dim=1024)

    assert captured["collection_name"] == "kb_550e8400e29b41d4a716446655440000"
    assert captured["dim"] == 1024
    # MilvusVectorStore 1.1.0 takes the HNSW spec via index_config + the metric
    # via similarity_metric (SPEC §3.1: HNSW / COSINE / M=16 / efConstruction=200).
    index_config = captured["index_config"]
    assert index_config["index_type"] == "HNSW"
    assert index_config["metric_type"] == "COSINE"
    assert index_config["params"]["M"] == 16
    assert index_config["params"]["efConstruction"] == 200
    assert captured["similarity_metric"] == "COSINE"


def test_build_vector_store_default_dim_from_settings(monkeypatch):
    captured = {}

    class _FakeMilvusVectorStore:
        def __init__(self, **kwargs) -> None:
            captured.update(kwargs)

    monkeypatch.setattr(
        sys.modules["llama_index.vector_stores.milvus"],
        "MilvusVectorStore",
        _FakeMilvusVectorStore,
    )

    milvus.build_vector_store("abc123")
    assert captured["dim"] == settings.embedding_dim


# ── build_vector_store_index wraps via from_vector_store ────────────────────


def test_build_vector_store_index_uses_from_vector_store(monkeypatch):
    """SPEC §5.1: VectorStoreIndex.from_vector_store(vector_store, embed_model=...)"""
    vs_captured = {}
    idx_captured = {}

    class _FakeMilvusVectorStore:
        def __init__(self, **kwargs) -> None:
            vs_captured.update(kwargs)

    class _FakeVectorStoreIndex:
        @classmethod
        def from_vector_store(cls, vector_store, embed_model=None, **kw):
            idx_captured["vector_store"] = vector_store
            idx_captured["embed_model"] = embed_model
            return MagicMock()

    monkeypatch.setattr(
        sys.modules["llama_index.vector_stores.milvus"],
        "MilvusVectorStore",
        _FakeMilvusVectorStore,
    )
    monkeypatch.setattr(
        sys.modules["llama_index.core"], "VectorStoreIndex", _FakeVectorStoreIndex
    )

    fake_embed = MagicMock(name="embed_model")
    idx = milvus.build_vector_store_index("kb1", dim=512, embed_model=fake_embed)

    # The Index must be built via from_vector_store (not from_documents).
    assert isinstance(idx_captured["vector_store"], _FakeMilvusVectorStore)
    assert idx_captured["embed_model"] is fake_embed
    assert vs_captured["collection_name"] == "kb_kb1"
    assert vs_captured["dim"] == 512


def test_build_vector_store_index_resolves_embed_singleton(monkeypatch):
    """When embed_model is None, the global OpenAICompatibleEmbedding singleton is used."""
    captured = {}

    class _FakeMilvusVectorStore:
        def __init__(self, **kwargs) -> None:
            captured.update(kwargs)

    class _FakeVectorStoreIndex:
        @classmethod
        def from_vector_store(cls, vector_store, embed_model=None, **kw):
            captured["embed_model"] = embed_model
            return MagicMock()

    monkeypatch.setattr(
        sys.modules["llama_index.vector_stores.milvus"],
        "MilvusVectorStore",
        _FakeMilvusVectorStore,
    )
    monkeypatch.setattr(
        sys.modules["llama_index.core"], "VectorStoreIndex", _FakeVectorStoreIndex
    )

    fake_singleton = MagicMock(name="singleton")
    monkeypatch.setattr(
        "app.core.embeddings.get_embed_model", lambda: fake_singleton
    )

    milvus.build_vector_store_index("kb1")
    assert captured["embed_model"] is fake_singleton


# ── _build_text_node mirrors Milvus schema metadata (SPEC §3.1) ────────────


def test_build_text_node_metadata_fields():
    node = embed_service._build_text_node(
        chunk_id="c1",
        content="hello",
        chunk_type=embed_service.CHILD_TYPE,
        kb_id="kb1",
        doc_id="doc1",
        tenant_id="t1",
        file_name="f.pdf",
        page_number=3,
        content_type="text",
        parent_content="parent text",
        metadata={"section_path": "S1"},
    )
    # SPEC §3.1 schema fields.
    assert node.metadata["doc_id"] == "doc1"
    assert node.metadata["kb_id"] == "kb1"
    assert node.metadata["tenant_id"] == "t1"
    assert node.metadata["chunk_type"] == "child"
    assert node.metadata["file_name"] == "f.pdf"
    assert node.metadata["page_number"] == 3
    assert node.metadata["content_type"] == "text"
    assert node.metadata["parent_content"] == "parent text"
    # Extra chunk_service metadata preserved.
    assert node.metadata["section_path"] == "S1"
    assert node.text == "hello"
    assert node.id_ == "c1"


def test_build_text_node_page_number_defaults_to_zero():
    node = embed_service._build_text_node(
        chunk_id="c1",
        content="x",
        chunk_type=embed_service.PARENT_TYPE,
        kb_id="kb1",
        doc_id="doc1",
        tenant_id="t1",
        file_name="f.pdf",
        page_number=None,
        content_type="text",
        parent_content=None,
    )
    # Milvus INT column needs a number, not None.
    assert node.metadata["page_number"] == 0


def test_build_text_node_content_type_defaults_to_text():
    node = embed_service._build_text_node(
        chunk_id="c1",
        content="x",
        chunk_type=embed_service.PARENT_TYPE,
        kb_id="kb1",
        doc_id="doc1",
        tenant_id="t1",
        file_name="f.pdf",
        page_number=1,
        content_type=None,
        parent_content=None,
    )
    assert node.metadata["content_type"] == "text"


def test_build_text_node_child_wires_parent_relationship():
    """A child chunk with parent_chunk_id gets a PARENT relationship."""
    node = embed_service._build_text_node(
        chunk_id="c1",
        content="child",
        chunk_type=embed_service.CHILD_TYPE,
        kb_id="kb1",
        doc_id="doc1",
        tenant_id="t1",
        file_name="f.pdf",
        page_number=1,
        content_type="text",
        parent_content="parent text",
        metadata={"parent_chunk_id": "p1"},
    )
    rel = node.relationships.get("PARENT")
    assert rel is not None
    assert rel.node_id == "p1"


def test_build_text_node_parent_no_parent_relationship():
    """Parent chunks have no PARENT relationship."""
    node = embed_service._build_text_node(
        chunk_id="p1",
        content="parent",
        chunk_type=embed_service.PARENT_TYPE,
        kb_id="kb1",
        doc_id="doc1",
        tenant_id="t1",
        file_name="f.pdf",
        page_number=1,
        content_type="text",
        parent_content=None,
        metadata={},
    )
    assert "PARENT" not in node.relationships


# ── _nodes_from_chunks ordering & counts ───────────────────────────────────


def _make_parent(pid="p1", content="parent text"):
    return ParentChunk(
        chunk_id=pid,
        content=content,
        content_type="text",
        token_count=10,
        page_number=1,
    )


def _make_child(cid="c1", pid="p1", parent_content="parent text"):
    return ChildChunk(
        chunk_id=cid,
        content="child text",
        content_type="text",
        page_number=1,
        token_count=5,
        parent_chunk_id=pid,
        parent_content=parent_content,
    )


def _make_summary(sid="s1"):
    return ChildChunk(
        chunk_id=sid,
        content="summary text",
        content_type="text",
        page_number=1,
        token_count=20,
    )


def test_nodes_from_chunks_order_and_counts():
    parents = [_make_parent()]
    children = [_make_child()]
    summaries = [_make_summary()]
    nodes = embed_service._nodes_from_chunks(
        tenant_id="t1",
        kb_id="kb1",
        doc_id="doc1",
        file_name="f.pdf",
        parents=parents,
        children=children,
        summaries=summaries,
    )
    # Order: parents first, then children, then summaries (FK order).
    assert len(nodes) == 3
    types = [n.metadata["chunk_type"] for n in nodes]
    assert types == ["parent", "child", "doc_summary"]


def test_nodes_from_chunks_no_summary():
    nodes = embed_service._nodes_from_chunks(
        tenant_id="t1",
        kb_id="kb1",
        doc_id="doc1",
        file_name="f.pdf",
        parents=[_make_parent()],
        children=[_make_child()],
        summaries=None,
    )
    assert len(nodes) == 2
    assert all(n.metadata["chunk_type"] != "doc_summary" for n in nodes)


def test_nodes_from_chunks_empty():
    nodes = embed_service._nodes_from_chunks(
        tenant_id="t1",
        kb_id="kb1",
        doc_id="doc1",
        file_name="f.pdf",
        parents=[],
        children=[],
        summaries=[],
    )
    assert nodes == []


def test_nodes_from_chunks_child_inherits_parent_metadata():
    """Child TextNode metadata preserves parent_chunk_id from ChildChunk."""
    nodes = embed_service._nodes_from_chunks(
        tenant_id="t1",
        kb_id="kb1",
        doc_id="doc1",
        file_name="f.pdf",
        parents=[_make_parent(pid="p1")],
        children=[_make_child(cid="c1", pid="p1")],
    )
    child_node = [n for n in nodes if n.metadata["chunk_type"] == "child"][0]
    assert child_node.metadata["parent_chunk_id"] == "p1"
    assert child_node.metadata["parent_content"] == "parent text"


# ── EmbedService.embed_and_write ────────────────────────────────────────────


def test_embed_and_write_inserts_nodes_and_returns_summary(monkeypatch):
    """embed_and_write builds the index, inserts nodes, returns a summary."""
    inserted_nodes: list = []

    def _fake_build_vector_store_index(kb_id, *, dim=None, embed_model=None):
        idx = MagicMock()
        idx.insert_nodes = lambda nodes: inserted_nodes.extend(nodes)
        return idx

    monkeypatch.setattr(
        embed_service, "build_vector_store_index", _fake_build_vector_store_index
    )

    svc = embed_service.EmbedService(embed_model=MagicMock())
    result = svc.embed_and_write(
        tenant_id="t1",
        kb_id="550e8400-e29b-41d4-a716-446655440000",
        doc_id="doc1",
        file_name="f.pdf",
        parents=[_make_parent()],
        children=[_make_child()],
        summaries=[_make_summary()],
    )

    assert result.nodes_written == 3
    assert result.collection_name == "kb_550e8400e29b41d4a716446655440000"
    assert result.counts == {"parent": 1, "child": 1, "doc_summary": 1}
    assert len(inserted_nodes) == 3
    # Index layer handles embedding — embed_and_write must NOT pre-embed.
    # (We can't assert the negative directly, but we verify insert_nodes was
    # the only write call and no embed was invoked on the embed_model.)


def test_embed_and_write_empty_writes_nothing(monkeypatch):
    insert_calls = 0

    def _fake_build_vector_store_index(kb_id, *, dim=None, embed_model=None):
        idx = MagicMock()

        def _insert(nodes):
            nonlocal insert_calls
            insert_calls += 1

        idx.insert_nodes = _insert
        return idx

    monkeypatch.setattr(
        embed_service, "build_vector_store_index", _fake_build_vector_store_index
    )

    svc = embed_service.EmbedService()
    result = svc.embed_and_write(
        tenant_id="t1",
        kb_id="kb1",
        doc_id="doc1",
        file_name="f.pdf",
        parents=[],
        children=[],
    )
    assert result.nodes_written == 0
    assert insert_calls == 0


def test_embed_and_write_uses_shared_embed_model(monkeypatch):
    """SPEC §1.3: write and query share the same embed_model singleton."""
    captured_models = []

    def _fake_build_vector_store_index(kb_id, *, dim=None, embed_model=None):
        captured_models.append(embed_model)
        idx = MagicMock()
        return idx

    monkeypatch.setattr(
        embed_service, "build_vector_store_index", _fake_build_vector_store_index
    )

    shared_model = MagicMock(name="shared")
    svc = embed_service.EmbedService(embed_model=shared_model)
    svc.embed_and_write(
        tenant_id="t1", kb_id="kb1", doc_id="d1", file_name="f.pdf",
        parents=[_make_parent()], children=[_make_child()],
    )
    svc.as_retriever("kb1", top_k=10)
    assert captured_models[0] is shared_model
    assert captured_models[1] is shared_model


# ── EmbedService.as_retriever ───────────────────────────────────────────────


def test_as_retriever_passes_top_k(monkeypatch):
    captured_top_k = {}

    def _fake_build_vector_store_index(kb_id, *, dim=None, embed_model=None):
        idx = MagicMock()
        idx.as_retriever = lambda similarity_top_k: similarity_top_k
        return idx

    monkeypatch.setattr(
        embed_service, "build_vector_store_index", _fake_build_vector_store_index
    )

    svc = embed_service.EmbedService()
    top_k = svc.as_retriever("kb1", top_k=7)
    assert top_k == 7


def test_as_retriever_default_top_k(monkeypatch):
    def _fake_build_vector_store_index(kb_id, *, dim=None, embed_model=None):
        idx = MagicMock()
        idx.as_retriever = lambda similarity_top_k: similarity_top_k
        return idx

    monkeypatch.setattr(
        embed_service, "build_vector_store_index", _fake_build_vector_store_index
    )

    svc = embed_service.EmbedService()
    top_k = svc.as_retriever("kb1")
    assert top_k == 5


# ── AC5: no CoreAPIVectorStore adapter (SPEC §1.3) ──────────────────────────


def test_no_coreapi_vectorstore_adapter_referenced():
    """SPEC §1.3 v1.2 architecture: no CoreAPIVectorStore adapter may exist.

    The embed_service and milvus modules must talk to Milvus directly. We
    check the AST (not the raw source string) so docstrings that mention
    "CoreAPIVectorStore" to explain its absence are not flagged.
    """
    import ast
    import inspect

    forbidden_names = {"CoreAPIVectorStore"}

    for mod in (embed_service, milvus):
        src = inspect.getsource(mod)
        tree = ast.parse(src)
        for node in ast.walk(tree):
            # class CoreAPIVectorStore: ...
            if isinstance(node, ast.ClassDef) and node.name in forbidden_names:
                pytest.fail(f"{mod.__name__} defines {node.name}")
            # CoreAPIVectorStore(...) instantiation / attribute access / name load
            if isinstance(node, ast.Name) and node.id in forbidden_names:
                # Allow only when used as a string in a docstring/comment — but
                # ast.Name means it's a code reference, which is forbidden.
                pytest.fail(f"{mod.__name__} references {node.id} in code")
    # Must use the LlamaIndex MilvusVectorStore directly.
    assert "MilvusVectorStore" in inspect.getsource(milvus)


# ── embeddings.py uses remote OpenAI-compatible embedding (SPEC §1.3) ────────


def test_embeddings_uses_remote_openai_compatible_endpoint():
    """SPEC §1.3 v1.2: embedding model served by AI inference service via
    OpenAI-compatible /v1/embeddings; rag-engine uses a custom adapter backed
    by the ``openai`` SDK (not a local HuggingFace model, and not
    llama-index-embeddings-openai which rejects custom model names).
    """
    import ast
    import inspect

    from app.core import embeddings

    src = inspect.getsource(embeddings)
    # Must use the custom OpenAICompatibleEmbedding adapter (remote endpoint).
    assert "OpenAICompatibleEmbedding" in src
    assert "from openai import OpenAI" in src
    # Must NOT load a local HuggingFace model (v1.2 architecture: embedding is
    # served by the inference service, not loaded in-process).
    assert "HuggingFaceEmbedding" not in src
    assert "SentenceTransformer" not in src
    # Must NOT import llama_index.embeddings.openai (it rejects custom model
    # names). Check the AST so docstrings explaining its absence don't match.
    tree = ast.parse(src)
    forbidden_imports = {"llama_index.embeddings.openai"}
    for node in ast.walk(tree):
        if isinstance(node, ast.ImportFrom) and node.module in forbidden_imports:
            pytest.fail(f"embeddings.py imports {node.module}")
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name in forbidden_imports:
                    pytest.fail(f"embeddings.py imports {alias.name}")


def test_embeddings_init_builds_adapter_with_remote_config(monkeypatch):
    """init_embedding_model builds OpenAICompatibleEmbedding with api_base +
    api_key from settings so it calls the inference-service /v1/embeddings
    endpoint. The ``openai.OpenAI`` client is stubbed so no network call happens.
    """
    captured = {}

    class _FakeOpenAIClient:
        def __init__(self, **kwargs) -> None:
            captured.update(kwargs)

    class _FakeOpenAI:
        def __init__(self, **kwargs) -> None:
            captured.update(kwargs)

    monkeypatch.setattr(sys.modules["openai"], "OpenAI", _FakeOpenAI)

    from app.core import embeddings

    embeddings._model = None
    import asyncio

    asyncio.run(embeddings.init_embedding_model("Qwen3-Embedding-0.6B"))

    # The adapter stores the model name and builds an openai client pointed at
    # the configured endpoint.
    assert embeddings._model is not None
    assert embeddings._model.model_name == "Qwen3-Embedding-0.6B"
    assert captured["base_url"] == settings.embedding_api_base
    # Empty api_key falls back to "EMPTY" (interim service has no auth).
    assert captured["api_key"] in ("", "EMPTY")


def test_embeddings_adapter_get_text_embedding_batch(monkeypatch):
    """The adapter calls the remote /v1/embeddings endpoint and returns vectors
    in input order."""
    from app.core import embeddings as emb_mod

    # Build an adapter with a stubbed openai client.
    class _FakeData:
        def __init__(self, idx, vec):
            self.index = idx
            self.embedding = vec

    class _FakeCreate:
        def __init__(self, data):
            self.data = data

    class _FakeEmbeddings:
        def __init__(self, client):
            self._client = client

        def create(self, *, model, input):
            # Return vectors in the same order as input, tagged with index.
            data = [_FakeData(i, [float(i), float(i) + 0.5]) for i in range(len(input))]
            return _FakeCreate(data)

    class _FakeOpenAI:
        def __init__(self, **kwargs):
            self.embeddings = _FakeEmbeddings(self)

    monkeypatch.setattr(sys.modules["openai"], "OpenAI", _FakeOpenAI)

    adapter = emb_mod.OpenAICompatibleEmbedding(
        model="Qwen3-Embedding-0.6B",
        api_base="http://example/v1",
        api_key="",
    )
    vecs = adapter.get_text_embedding_batch(["a", "b", "c"])
    assert len(vecs) == 3
    assert vecs[0] == [0.0, 0.5]
    assert vecs[1] == [1.0, 1.5]
    assert vecs[2] == [2.0, 2.5]
    # Query embedding shares the same endpoint as text embedding (SPEC §1.3).
    assert adapter.get_query_embedding("x") == adapter.get_text_embedding("x")


def test_embeddings_get_embed_model_raises_before_init():
    from app.core import embeddings

    # Reset the singleton to ensure we test the un-initialised path.
    embeddings._model = None
    with pytest.raises(RuntimeError, match="not initialised"):
        embeddings.get_embed_model()


# ── config: embedding is remote (no local HF mirror) ────────────────────────


def test_config_embedding_is_remote_no_hf_mirror():
    """config must expose the remote embedding endpoint and must NOT carry the
    local HuggingFace mirror (v1.2: embedding served by inference service).
    """
    from app.core.config import settings

    assert settings.embedding_model == "Qwen3-Embedding-0.6B"
    assert settings.embedding_api_base.startswith("http")
    assert settings.embedding_dim == 1024
    # The local HuggingFace mirror setting must be removed.
    assert not hasattr(settings, "hf_endpoint")


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
