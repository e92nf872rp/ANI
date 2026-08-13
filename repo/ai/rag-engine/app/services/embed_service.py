"""Embedding + Milvus write service (US-013 / SPEC §2.2, §3.1, §5.1).

``embed_service`` is the write-side orchestrator: it takes the chunk output
of ``chunk_service`` (parents + children + doc-summary) plus optional
document-level summaries, converts each chunk into a LlamaIndex
:class:`TextNode`, and writes them into the KB's Milvus collection via
:class:`VectorStoreIndex.from_vector_store`.

Per SPEC §5.1 the embedding happens at the Index layer — callers MUST NOT
pre-embed text. The embedding model is served by the AI inference service
(OpenAI-compatible ``/v1/embeddings``); ``embeddings.py`` builds an
:class:`OpenAICompatibleEmbedding` pointed at that endpoint and the Index
layer calls it transparently. The flow is::

    embed_model = OpenAICompatibleEmbedding(model=settings.embedding_model,
                                            api_base=settings.embedding_api_base)
    vector_store = MilvusVectorStore(uri=..., collection_name=f"kb_{kb_id_no_dash}",
                                    index_config={'index_type': 'HNSW',
                                                  'metric_type': 'COSINE',
                                                  'params': {'M': 16,
                                                             'efConstruction': 200}},
                                    similarity_metric='COSINE')
    index = VectorStoreIndex.from_vector_store(vector_store, embed_model=embed_model)
    index.insert_nodes(nodes)        # Index embeds then calls vector_store.add
    retriever = index.as_retriever(similarity_top_k=top_k)

This module deliberately does NOT implement a ``CoreAPIVectorStore`` adapter
(SPEC §1.3 v1.2 architecture — direct Milvus, one less HTTP hop).
"""
from __future__ import annotations

import uuid
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

from app.core.config import settings
from app.core.milvus import build_vector_store_index, kb_collection_name
from app.services.chunk_service import ChildChunk, ParentChunk

if TYPE_CHECKING:
    from llama_index.core.embeddings import BaseEmbedding
    from llama_index.core.schema import BaseNode

# ── Chunk-type labels stored in Milvus (SPEC §3.1) ───────────────────────────
CHILD_TYPE = "child"
PARENT_TYPE = "parent"
DOC_SUMMARY_TYPE = "doc_summary"


@dataclass
class EmbedWriteResult:
    """Summary of a single embed + write operation."""

    kb_id: str
    collection_name: str
    nodes_written: int
    # Free-form metadata for the parse_worker (e.g. per-type counts).
    counts: dict[str, int]


def _build_text_node(
    *,
    chunk_id: str,
    content: str,
    chunk_type: str,
    kb_id: str,
    doc_id: str,
    tenant_id: str,
    file_name: str,
    page_number: int | None,
    content_type: str | None,
    parent_content: str | None = None,
    metadata: dict[str, Any] | None = None,
) -> BaseNode:
    """Convert a chunk into a LlamaIndex ``TextNode`` with Milvus metadata.

    The metadata fields mirror the Milvus collection schema (SPEC §3.1) so
    the Index layer can persist them alongside the embedding vector.
    """
    from llama_index.core.schema import NodeRelationship, RelatedNodeInfo, TextNode

    node = TextNode(
        id_=chunk_id,
        text=content,
        # Store schema-aligned metadata for Milvus filtering / parent-backfill.
        metadata={
            "doc_id": doc_id,
            "kb_id": kb_id,
            "tenant_id": tenant_id,
            "chunk_type": chunk_type,
            "file_name": file_name,
            "page_number": page_number if page_number is not None else 0,
            "content_type": content_type or "text",
            "parent_content": parent_content or "",
            # Preserve chunk_service metadata (section_path / sub_type / …).
            **(metadata or {}),
        },
    )
    # Children point at their parent via NodeRelationship.PARENT so the
    # Index layer preserves the relationship for parent-backfill on query.
    if chunk_type == CHILD_TYPE and metadata and metadata.get("parent_chunk_id"):
        node.relationships[NodeRelationship.PARENT] = RelatedNodeInfo(
            node_id=metadata["parent_chunk_id"]
        )
    return node


def _nodes_from_chunks(
    *,
    tenant_id: str,
    kb_id: str,
    doc_id: str,
    file_name: str,
    parents: list[ParentChunk],
    children: list[ChildChunk],
    summaries: list[ChildChunk] | None = None,
) -> list[BaseNode]:
    """Build the full ordered list of TextNodes for a document.

    Only *child* chunks (and optional doc_summary) are embedded into Milvus.
    Parent blocks are deliberately NOT indexed: they exist in ``kb_chunks``
    as aggregators whose full text is denormalized into each child's
    ``parent_content``. Indexing parent blocks as well lets a query hit the
    whole parent directly *and* its children at once, producing duplicated,
    redundant sources. Retrieval therefore only matches child segments and
    returns the backfilled parent block (deduped) — SPEC §5.1 parent-child.
    """
    nodes: list[BaseNode] = []
    for c in children:
        meta = dict(c.metadata)
        # ``parent_chunk_id`` is on the ChildChunk dataclass; mirror it into
        # metadata so the TextNode relationship can be wired above.
        if c.parent_chunk_id:
            meta["parent_chunk_id"] = c.parent_chunk_id
        nodes.append(
            _build_text_node(
                chunk_id=c.chunk_id,
                content=c.content,
                chunk_type=CHILD_TYPE,
                kb_id=kb_id,
                doc_id=doc_id,
                tenant_id=tenant_id,
                file_name=file_name,
                page_number=c.page_number,
                content_type=c.content_type,
                parent_content=c.parent_content,
                metadata=meta,
            )
        )
    for s in summaries or []:
        nodes.append(
            _build_text_node(
                chunk_id=s.chunk_id,
                content=s.content,
                chunk_type=DOC_SUMMARY_TYPE,
                kb_id=kb_id,
                doc_id=doc_id,
                tenant_id=tenant_id,
                file_name=file_name,
                page_number=s.page_number,
                content_type=s.content_type,
                parent_content=s.parent_content,
                metadata=s.metadata,
            )
        )
    return nodes


class EmbedService:
    """Orchestrates LlamaIndex embedding + Milvus writes for a KB.

    The service holds no per-call state; it builds a fresh
    :class:`VectorStoreIndex` per KB (cheap — the embed_model singleton is
    re-used) so the correct collection name and dim are applied.

    Args:
        embed_model: LlamaIndex embedding model (typically the singleton from
            :func:`app.core.embeddings.get_embed_model`). When ``None`` the
            global singleton is resolved lazily so write and query paths
            share the same model (SPEC §1.3 "嵌入统一").
    """

    def __init__(self, embed_model: BaseEmbedding | None = None) -> None:
        self._embed_model = embed_model

    # ── write path ──────────────────────────────────────────────────────────

    def embed_and_write(
        self,
        *,
        tenant_id: str,
        kb_id: str,
        doc_id: str,
        file_name: str,
        parents: list[ParentChunk],
        children: list[ChildChunk],
        summaries: list[ChildChunk] | None = None,
        dim: int | None = None,
    ) -> EmbedWriteResult:
        """Embed parent + child + summary chunks and write them to Milvus.

        The embedding is performed by the Index layer (SPEC §5.1): we build a
        :class:`VectorStoreIndex` wrapping the KB's Milvus collection and call
        ``index.insert_nodes(nodes)``. The Index layer embeds each node's text
        via the shared :class:`OpenAICompatibleEmbedding` (remote
        inference-service endpoint) and then calls ``vector_store.add`` on Milvus.
        """
        index = build_vector_store_index(
            kb_id,
            dim=dim if dim is not None else settings.embedding_dim,
            embed_model=self._embed_model,
        )
        nodes = _nodes_from_chunks(
            tenant_id=tenant_id,
            kb_id=kb_id,
            doc_id=doc_id,
            file_name=file_name,
            parents=parents,
            children=children,
            summaries=summaries,
        )
        if nodes:
            index.insert_nodes(nodes)
        counts = {
            PARENT_TYPE: len(parents),
            CHILD_TYPE: len(children),
            DOC_SUMMARY_TYPE: len(summaries or []),
        }
        return EmbedWriteResult(
            kb_id=kb_id,
            collection_name=kb_collection_name(kb_id),
            nodes_written=len(nodes),
            counts=counts,
        )

    # ── query path (retriever factory for retrieve_service / US-014) ────────

    def as_retriever(self, kb_id: str, *, top_k: int = 5, dim: int | None = None):
        """Return a LlamaIndex retriever over the KB's Milvus collection.

        Used by ``retrieve_service`` (US-014). Embedding of the query string
        is performed by the Index layer (same shared
        :class:`OpenAICompatibleEmbedding` remote endpoint) so write and
        query embeddings are unified (SPEC §1.3).
        """
        index = build_vector_store_index(
            kb_id,
            dim=dim if dim is not None else settings.embedding_dim,
            embed_model=self._embed_model,
        )
        return index.as_retriever(similarity_top_k=top_k)


def new_chunk_id() -> str:
    """Generate a fresh chunk UUID (used by summary chunks)."""
    return str(uuid.uuid4())
