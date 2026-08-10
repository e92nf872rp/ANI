"""检索准确性测评脚本 (Issue #13 / SPEC §5.1).

对 retrieve_service 的三路检索（向量 / 全文 / 混合）进行准确性测评：

1. 写入一组已知文档块（4 个 chunk，涵盖不同主题）
2. 构造 8 组查询，每组标注期望命中的 chunk 关键词（ground truth）
3. 对每路检索计算 Precision@K、Recall@K、MRR
4. 打印测评结果对比表

运行：
    $env:PYTHONPATH="ai/rag-engine"; python ai/rag-engine/tests/demo_e2e_retrieval_accuracy.py
"""
from __future__ import annotations

import asyncio
import os
import uuid
from pathlib import Path

os.chdir(Path(__file__).resolve().parents[3])
from dotenv import load_dotenv
load_dotenv()

import nest_asyncio
nest_asyncio.apply()

import asyncpg

from app.core.config import settings
from app.core.milvus import init_milvus, kb_collection_name
from app.core.embeddings import init_embedding_model, get_embed_model
from app.repositories.chunks import write_chunks, delete_chunks_by_doc
from app.services.chunk_service import ChildChunk, ParentChunk
from app.services.embed_service import EmbedService
from app.services.retrieve_service import (
    RetrieveService,
    make_pg_trgm_search_fn,
)
from app.services.qa_service import _default_llm_factory


TENANT_ID = "00000000-0000-0000-0000-0000000000e2"
TOP_K = 5


def hr(title: str):
    print("\n" + "=" * 72)
    print(title)
    print("=" * 72)


# ── 文档块定义 ───────────────────────────────────────────────────────────────
# 4 个父块 + 8 个子块 + 4 个摘要，涵盖 4 个不同主题

CHUNKS_DATA = [
    {
        "topic": "向量检索",
        "parent": "Milvus 向量检索基于 HNSW 图索引与余弦相似度，支持高维向量的近似最近邻搜索。嵌入模型将文本编码为 1024 维向量，写入 Milvus 后构建 HNSW 索引（M=16, efConstruction=200），查询时通过余弦相似度排序返回 top_k 结果。",
        "children": [
            "Milvus 向量检索使用 HNSW 图索引，通过余弦相似度进行近似最近邻搜索，支持大规模向量快速检索。",
            "嵌入模型将文本编码为 1024 维向量向量，写入 Milvus 后构建 HNSW 索引，查询时按相似度排序返回 top_k。",
        ],
        "summary": "本节介绍 Milvus 向量检索的原理，包括 HNSW 索引、余弦相似度与嵌入编码流程。",
    },
    {
        "topic": "关键词检索",
        "parent": "PostgreSQL pg_trgm 全文检索通过三元组（trigram）相似度匹配文本。GIN 索引 idx_kb_chunks_content_trgm 加速 similarity() 函数计算，使用 % 操作符过滤，按 similarity(content, query) 降序排序。",
        "children": [
            "pg_trgm 全文检索通过三元组相似度匹配文本，使用 GIN 索引加速 similarity() 函数计算。",
            "pg_trgm 检索使用 % 操作符过滤候选行，按 similarity(content, query) 降序排序返回结果。",
        ],
        "summary": "本节介绍 PostgreSQL pg_trgm 全文检索的原理，包括三元组、GIN 索引与相似度排序。",
    },
    {
        "topic": "RRF 融合",
        "parent": "RRF 互逆排序融合算法将向量检索与关键词检索的结果合并。每条结果的 RRF 分数为 1/(rank+60)，num_queries=1 关闭查询生成，减少 LLM 调用开销。",
        "children": [
            "RRF 互逆排序融合算法将多路检索结果合并，每条结果分数为 1/(rank+60)。",
            "num_queries=1 关闭查询生成，减少 LLM 调用开销，直接使用原始查询进行融合检索。",
        ],
        "summary": "本节介绍 RRF 融合算法的原理，包括互逆排序、分数计算与查询生成关闭。",
    },
    {
        "topic": "RAG 问答",
        "parent": "RAG 问答基于 ContextChatEngine，使用 ChatMemoryBuffer 持久化多轮对话记忆到 Redis，通过 OpenAILike 调用 vLLM 的 OpenAI 兼容接口生成回答。",
        "children": [
            "RAG 问答基于 ContextChatEngine，使用 ChatMemoryBuffer 将多轮对话记忆持久化到 Redis。",
            "ContextChatEngine 通过 OpenAILike 调用 vLLM 的 OpenAI 兼容接口，生成 RAG 回答。",
        ],
        "summary": "本节介绍 RAG 问答的架构，包括 ContextChatEngine、Redis 对话记忆与 vLLM 接口。",
    },
]

# ── 测评查询集 ───────────────────────────────────────────────────────────────
# 每组查询标注期望命中的主题关键词（ground truth），用于评估准确性。
# "expected_topics" 是期望检索到的 chunk 所属主题集合。

QUERIES = [
    {
        "question": "Milvus 向量检索的原理是什么？",
        "expected_topics": {"向量检索"},
        "desc": "精确语义匹配（向量检索主题）",
    },
    {
        "question": "pg_trgm 全文检索怎么工作？",
        "expected_topics": {"关键词检索"},
        "desc": "精确语义匹配（关键词检索主题）",
    },
    {
        "question": "RRF 融合算法的分数怎么计算？",
        "expected_topics": {"RRF 融合"},
        "desc": "精确语义匹配（RRF 主题）",
    },
    {
        "question": "RAG 问答的对话记忆怎么实现的？",
        "expected_topics": {"RAG 问答"},
        "desc": "精确语义匹配（RAG 问答主题）",
    },
    {
        "question": "如何进行相似度搜索？",
        "expected_topics": {"向量检索", "关键词检索"},
        "desc": "跨主题模糊匹配（向量+关键词都有相似度概念）",
    },
    {
        "question": "检索结果怎么合并？",
        "expected_topics": {"RRF 融合"},
        "desc": "语义模糊匹配（RRF 融合负责结果合并）",
    },
    {
        "question": "HNSW 索引是什么？",
        "expected_topics": {"向量检索"},
        "desc": "精确关键词匹配（HNSW 仅出现在向量检索块）",
    },
    {
        "question": "对话记忆持久化到哪里？",
        "expected_topics": {"RAG 问答"},
        "desc": "语义匹配（Redis 持久化在 RAG 问答块）",
    },
]


# ── 评估指标 ─────────────────────────────────────────────────────────────────


def compute_precision_at_k(retrieved_topics: list[str], expected_topics: set[str], k: int) -> float:
    """Precision@K: 前 K 个结果中相关结果的比例。"""
    top_k = retrieved_topics[:k]
    if not top_k:
        return 0.0
    relevant = sum(1 for t in top_k if t in expected_topics)
    return relevant / len(top_k)


def compute_recall_at_k(retrieved_topics: list[str], expected_topics: set[str], k: int) -> float:
    """Recall@K: 期望主题中被检索到的比例。"""
    top_k = retrieved_topics[:k]
    found = set()
    for t in top_k:
        if t in expected_topics:
            found.add(t)
    return len(found) / len(expected_topics) if expected_topics else 0.0


def compute_mrr(retrieved_topics: list[str], expected_topics: set[str]) -> float:
    """MRR (Mean Reciprocal Rank): 第一个相关结果的倒数排名。"""
    for i, t in enumerate(retrieved_topics):
        if t in expected_topics:
            return 1.0 / (i + 1)
    return 0.0


async def main():
    kb_id = str(uuid.uuid4())
    doc_id = str(uuid.uuid4())

    hr("0. 环境配置")
    print(f"Milvus 地址      : {settings.milvus_host}:{settings.milvus_port}")
    print(f"Embedding 端点   : {settings.embedding_api_base}")
    print(f"Embedding 模型   : {settings.embedding_model}")
    print(f"Database URL     : {os.environ.get('DATABASE_URL', '')[:40]}...")
    print(f"KB ID            : {kb_id}")
    print(f"Top K            : {TOP_K}")
    print(f"查询数           : {len(QUERIES)}")
    print(f"文档块主题数     : {len(CHUNKS_DATA)}")

    hr("1. 初始化连接")
    await init_milvus()
    await init_embedding_model()
    embed_model = get_embed_model()
    database_url = os.environ.get("DATABASE_URL", "")
    pool = await asyncpg.create_pool(dsn=database_url, min_size=1, max_size=2)
    print("连接 OK")

    try:
        # ── 构造并写入文档块 ────────────────────────────────────────────
        hr("2. 构造并写入文档块")
        parents = []
        children = []
        summaries = []
        topic_to_chunk_ids = {}  # topic → set of chunk_id

        for data in CHUNKS_DATA:
            parent_id = str(uuid.uuid4())
            topic = data["topic"]
            topic_to_chunk_ids[topic] = set()

            p = ParentChunk(
                chunk_id=parent_id,
                content=data["parent"],
                content_type="text",
                token_count=80,
                page_number=1,
            )
            parents.append(p)
            topic_to_chunk_ids[topic].add(parent_id)

            for child_text in data["children"]:
                cid = str(uuid.uuid4())
                c = ChildChunk(
                    chunk_id=cid,
                    content=child_text,
                    content_type="text",
                    page_number=1,
                    token_count=40,
                    parent_chunk_id=parent_id,
                    parent_content=data["parent"],
                )
                children.append(c)
                topic_to_chunk_ids[topic].add(cid)

            sid = str(uuid.uuid4())
            s = ChildChunk(
                chunk_id=sid,
                content=data["summary"],
                content_type="text",
                page_number=1,
                token_count=30,
                parent_chunk_id=parent_id,
                parent_content=data["parent"],
            )
            summaries.append(s)
            topic_to_chunk_ids[topic].add(sid)

        print(f"父块数           : {len(parents)}")
        print(f"子块数           : {len(children)}")
        print(f"摘要块数         : {len(summaries)}")

        embed_svc = EmbedService(embed_model=embed_model)
        result = embed_svc.embed_and_write(
            tenant_id=TENANT_ID, kb_id=kb_id, doc_id=doc_id,
            file_name="accuracy-test.txt",
            parents=parents, children=children, summaries=summaries,
        )
        print(f"Milvus 写入      : {result.nodes_written} nodes")

        async with pool.acquire() as conn:
            n = await write_chunks(
                conn, tenant_id=TENANT_ID, kb_id=kb_id, doc_id=doc_id,
                file_name="accuracy-test.txt",
                parents=parents, children=children, summaries=summaries,
            )
        print(f"kb_chunks 写入   : {n} rows")

        # ── 构建检索服务 ────────────────────────────────────────────────
        keyword_search_fn = make_pg_trgm_search_fn(
            database_url, kb_id=kb_id, tenant_id=TENANT_ID
        )
        llm = _default_llm_factory()
        retrieve_svc = RetrieveService(
            embed_model=embed_model,
            keyword_search_fn=keyword_search_fn,
            llm=llm,
        )

        # ── chunk_id → topic 映射 ───────────────────────────────────────
        chunk_id_to_topic = {}
        for topic, ids in topic_to_chunk_ids.items():
            for cid in ids:
                chunk_id_to_topic[cid] = topic

        # ── 运行三路检索测评 ────────────────────────────────────────────
        hr("3. 三路检索准确性测评")

        methods = {
            "向量检索": retrieve_svc.vector_retrieve,
            "全文检索": retrieve_svc.keyword_retrieve,
            "混合检索": retrieve_svc.retrieve,
        }

        # 收集每路检索的指标
        metrics = {m: {"precision": [], "recall": [], "mrr": []} for m in methods}

        for q in QUERIES:
            question = q["question"]
            expected = q["expected_topics"]
            print(f"\n--- 查询: {question}")
            print(f"    期望主题: {expected} ({q['desc']})")

            for method_name, method_fn in methods.items():
                try:
                    result = method_fn(
                        kb_id=kb_id, question=question,
                        top_k=TOP_K, score_threshold=0.0,
                    )
                    # 提取命中 chunk 的主题序列
                    retrieved_topics = []
                    for src in result.sources:
                        topic = chunk_id_to_topic.get(src.chunk_id, "")
                        retrieved_topics.append(topic)
                    retrieved_topics = [t for t in retrieved_topics if t]

                    p = compute_precision_at_k(retrieved_topics, expected, TOP_K)
                    r = compute_recall_at_k(retrieved_topics, expected, TOP_K)
                    mrr = compute_mrr(retrieved_topics, expected)

                    metrics[method_name]["precision"].append(p)
                    metrics[method_name]["recall"].append(r)
                    metrics[method_name]["mrr"].append(mrr)

                    hit_topics = [t for t in retrieved_topics if t in expected]
                    print(f"    [{method_name}] P@{TOP_K}={p:.2f} R@{TOP_K}={r:.2f} MRR={mrr:.2f} "
                          f"| 命中={len(result.sources)} 相关={len(hit_topics)} "
                          f"主题={retrieved_topics[:3]}")
                except Exception as e:
                    print(f"    [{method_name}] ERROR: {e}")
                    metrics[method_name]["precision"].append(0.0)
                    metrics[method_name]["recall"].append(0.0)
                    metrics[method_name]["mrr"].append(0.0)

        # ── 汇总结果 ────────────────────────────────────────────────────
        hr("4. 测评汇总（平均值）")
        print(f"{'检索方式':<14s} {'Precision@K':<14s} {'Recall@K':<12s} {'MRR':<10s}")
        print("-" * 52)
        for method_name in methods:
            ps = metrics[method_name]["precision"]
            rs = metrics[method_name]["recall"]
            ms = metrics[method_name]["mrr"]
            avg_p = sum(ps) / len(ps) if ps else 0.0
            avg_r = sum(rs) / len(rs) if rs else 0.0
            avg_mrr = sum(ms) / len(ms) if ms else 0.0
            print(f"{method_name:<14s} {avg_p:<14.3f} {avg_r:<12.3f} {avg_mrr:<10.3f}")

        # ── 逐查询详细对比 ───────────────────────────────────────────────
        hr("5. 逐查询详细对比")
        print(f"{'#':<3s} {'查询':<36s} {'向量 P/R/M':<20s} {'全文 P/R/M':<20s} {'混合 P/R/M':<20s}")
        print("-" * 100)
        for i, q in enumerate(QUERIES):
            q_short = q["question"][:34]
            row = f"{i+1:<3d} {q_short:<36s}"
            for method_name in methods:
                p = metrics[method_name]["precision"][i]
                r = metrics[method_name]["recall"][i]
                m = metrics[method_name]["mrr"][i]
                row += f" {p:.2f}/{r:.2f}/{m:.2f}    "
            print(row)

        # ── 清理 ────────────────────────────────────────────────────────
        hr("6. 清理测试数据")
        from pymilvus import utility
        coll_name = kb_collection_name(kb_id)
        if utility.has_collection(coll_name):
            utility.drop_collection(coll_name)
            print(f"已删除 Milvus 集合: {coll_name}")
        async with pool.acquire() as conn:
            deleted = await delete_chunks_by_doc(
                conn, tenant_id=TENANT_ID, kb_id=kb_id, doc_id=doc_id
            )
        print(f"已删除 kb_chunks  : {deleted} 行")

        hr("测评完成")

    finally:
        await pool.close()


if __name__ == "__main__":
    asyncio.run(main())
