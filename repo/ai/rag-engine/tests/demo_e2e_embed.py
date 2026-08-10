"""E2E demo: show the actual input and output of the embed + write + retrieve
link against real Milvus + the remote embedding service.

Run from repo root:

    $env:PYTHONPATH="ai/rag-engine"; python ai/rag-engine/tests/demo_e2e_embed.py
"""
import asyncio
import uuid

# Load .env so Settings picks up MILVUS_ADDR / EMBEDDING_API_BASE etc.
from pathlib import Path
import os

os.chdir(Path(__file__).resolve().parents[3])

from app.core.config import settings
from app.core.milvus import (
    HNSW_EF_CONSTRUCTION,
    HNSW_INDEX_TYPE,
    HNSW_M,
    HNSW_METRIC_TYPE,
    init_milvus,
    kb_collection_name,
)
from app.core.embeddings import init_embedding_model, get_embed_model
from app.services.chunk_service import ChildChunk, ParentChunk
from app.services.embed_service import EmbedService


def hr(title: str):
    print("\n" + "=" * 70)
    print(title)
    print("=" * 70)


async def main():
    kb_id = str(uuid.uuid4())
    doc_id = "demo-doc-001"

    hr("1. 环境配置（来自 .env）")
    print(f"Milvus 地址      : {settings.milvus_host}:{settings.milvus_port}")
    print(f"Embedding 端点   : {settings.embedding_api_base}")
    print(f"Embedding 模型   : {settings.embedding_model}")
    print(f"Embedding 维度   : {settings.embedding_dim}")
    print(f"KB ID            : {kb_id}")
    print(f"集合名（去横杠） : {kb_collection_name(kb_id)}")

    hr("2. 初始化连接（Milvus + 远程 Embedding）")
    await init_milvus()
    await init_embedding_model()
    embed_model = get_embed_model()
    print("Milvus 连接      : OK")
    print(f"Embedding 适配器 : {type(embed_model).__name__}")
    print(f"Embedding 模型名 : {embed_model.model_name}")

    # ── 测试远程 embedding 直接调用 ──────────────────────────────────────
    hr("3. 直接调用远程 embedding 服务（/v1/embeddings）")
    sample_text = "这是一段测试文本，用于验证远程嵌入服务。"
    print(f"输入文本         : {sample_text}")
    vec = embed_model.get_text_embedding(sample_text)
    print(f"输出向量维度     : {len(vec)}")
    print(f"输出向量前 5 维  : {vec[:5]}")

    # ── 构造写入的 chunk 数据 ───────────────────────────────────────────
    parent = ParentChunk(
        chunk_id=str(uuid.uuid4()),
        content=(
            "ANI 平台是面向企业的 AI 专有云解决方案，提供模型推理、"
            "知识库问答和沙箱运行时能力。"
        ),
        content_type="text",
        token_count=30,
        page_number=1,
    )
    children = [
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

    hr("4. 写入 Milvus（embed_and_write）")
    print("写入的 chunk 内容：")
    print(f"  [parent] {parent.content}")
    for i, c in enumerate(children):
        print(f"  [child {i}] {c.content}")

    svc = EmbedService(embed_model=embed_model)
    result = svc.embed_and_write(
        tenant_id="demo",
        kb_id=kb_id,
        doc_id=doc_id,
        file_name="ani-overview.txt",
        parents=[parent],
        children=children,
    )
    print("\n写入结果 EmbedWriteResult：")
    print(f"  kb_id           : {result.kb_id}")
    print(f"  collection_name : {result.collection_name}")
    print(f"  nodes_written   : {result.nodes_written}")
    print(f"  counts          : {result.counts}")

    # ── 验证 Milvus 集合索引参数 ───────────────────────────────────────
    hr("5. 检查 Milvus 集合实际索引参数")
    from pymilvus import Collection, utility

    coll = Collection(kb_collection_name(kb_id))
    coll.load()
    print(f"集合存在         : {utility.has_collection(kb_collection_name(kb_id))}")
    idx = coll.indexes[0]
    params = idx.params
    print(f"index_type       : {params.get('index_type')}")
    print(f"metric_type      : {params.get('metric_type')}")
    hnsw_params = params.get("params", {})
    print(f"M                : {hnsw_params.get('M')}")
    print(f"efConstruction   : {hnsw_params.get('efConstruction')}")
    print(
        "预期 (SPEC §3.1) : HNSW / COSINE / M=16 / efConstruction=200"
    )

    # ── 查询测试 ────────────────────────────────────────────────────────
    hr("6. 检索查询（as_retriever）")
    queries = [
        "知识库的向量嵌入和检索能力是什么？",
        "ANI 平台的模型推理用什么框架？",
    ]
    retriever = svc.as_retriever(kb_id, top_k=3)
    for q in queries:
        print(f"\n查询文本         : {q}")
        nodes = retriever.retrieve(q)
        print(f"返回结果数       : {len(nodes)}")
        for i, n in enumerate(nodes):
            score = n.score if hasattr(n, "score") else "N/A"
            text = n.get_content()
            # 截断长文本
            if len(text) > 80:
                text = text[:80] + "..."
            print(f"  [结果 {i}] score={score:.4f} | {text}")

    # ── 清理 ────────────────────────────────────────────────────────────
    hr("7. 清理测试集合")
    utility.drop_collection(kb_collection_name(kb_id))
    print(f"已删除集合       : {kb_collection_name(kb_id)}")

    hr("E2E 演示完成 ✅")


if __name__ == "__main__":
    asyncio.run(main())
