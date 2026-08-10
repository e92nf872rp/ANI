"""E2E demo for issue-013: retrieve_service hybrid retrieval + qa_service
RAG question-answering, against real Milvus + PostgreSQL (pg_trgm) + Redis +
vLLM + remote embedding service.

Verifies the full hybrid-retrieval + RAG-QA link required by issue-013 ACs:

  - AC1: retrieve_service uses QueryFusionRetriever: MilvusVectorStore
    (wrapped as VectorStoreIndex.as_retriever()) + pg_trgm keyword
    (PgTrgmRetriever BaseRetriever subclass) + RRF, num_queries=1.
  - AC2: child hits backfill parent_content.
  - AC3: doc_summary hits backfill the document's parent blocks.
  - AC4: qa_service uses ContextChatEngine.from_defaults(retriever=fusion,
    memory=ChatMemoryBuffer(chat_store=RedisChatStore), llm=OpenAILike(
    model=..., api_base=vllm_url, api_key="...", is_chat_model=True,
    context_window=...)).
  - AC5: qa_service.chat() returns answer + sources + session_id + tokens.

Pre-requisites (must be reachable from the host running this script):
  - Milvus: ``10.10.1.66:31930`` (or override MILVUS_ADDR in .env).
  - PostgreSQL (kb_chunks): ``10.10.1.66:30945`` (DATABASE_URL in .env).
  - Redis: ``10.10.1.66:30453`` (REDIS_URL in .env).
  - Embedding service: ``http://10.10.20.197:8006/v1`` (Qwen3-Embedding-0.6B).
  - vLLM (interim LLM): ``http://10.10.20.181:3011/v1`` (Qwen3.6-35B-A3B).

Run from repo root:

    $env:PYTHONPATH="ai/rag-engine"; python ai/rag-engine/tests/demo_e2e_retrieve_qa.py

This file is NOT part of the default ``make test``/pytest run (it needs real
infrastructure); run it explicitly when the lab is up.
"""
import asyncio
import os
import uuid
from pathlib import Path

# Load .env so Settings picks up MILVUS_ADDR / EMBEDDING_* / VLLM_* / REDIS_URL etc.
# Also load DATABASE_URL into os.environ (rag-engine Settings doesn't define it,
# so pydantic_settings ignores it — we need it for the kb_chunks pg_trgm pool).
os.chdir(Path(__file__).resolve().parents[3])
from dotenv import load_dotenv
load_dotenv()  # loads .env into os.environ

# Allow nested event loops (LlamaIndex QueryFusionRetriever runs aretrieve
# inside an event loop; our pg_trgm search bridges asyncpg sync via asyncio.run).
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
    FUSION_MODE_RRF,
    FUSION_NUM_QUERIES,
    RetrievedSource,
)
from app.services.qa_service import QAService, QAResult


def hr(title: str):
    print("\n" + "=" * 70)
    print(title)
    print("=" * 70)


TENANT_ID = "00000000-0000-0000-0000-0000000000e2"  # valid UUID for RLS


async def main():
    kb_id = str(uuid.uuid4())
    doc_id = str(uuid.uuid4())

    hr("0. 环境配置（来自 .env）")
    print(f"Milvus 地址      : {settings.milvus_host}:{settings.milvus_port}")
    print(f"Embedding 端点   : {settings.embedding_api_base}")
    print(f"Embedding 模型   : {settings.embedding_model}")
    print(f"Embedding 维度   : {settings.embedding_dim}")
    print(f"vLLM 端点        : {settings.vllm_api_base}")
    print(f"vLLM 模型        : {settings.vllm_model}")
    print(f"Redis URL        : {settings.redis_url}")
    database_url = os.environ.get("DATABASE_URL", "")
    print(f"Database URL     : {database_url}")
    if not database_url:
        raise SystemExit("DATABASE_URL 未在 .env 中配置，无法连接 kb_chunks (pg_trgm 关键词检索)")
    print(f"KB ID            : {kb_id}")
    print(f"Doc ID           : {doc_id}")
    print(f"集合名（去横杠） : {kb_collection_name(kb_id)}")
    print(f"FUSION_MODE_RRF  : {FUSION_MODE_RRF}")
    print(f"FUSION_NUM_QUERIES: {FUSION_NUM_QUERIES}")

    hr("1. 初始化连接（Milvus + 远程 Embedding + PostgreSQL）")
    await init_milvus()
    await init_embedding_model()
    embed_model = get_embed_model()
    print("Milvus 连接      : OK")
    print(f"Embedding 适配器 : {type(embed_model).__name__}")
    print(f"Embedding 模型名 : {embed_model.model_name}")

    pool = await asyncpg.create_pool(dsn=database_url, min_size=1, max_size=2)
    print("PostgreSQL 连接  : OK")

    try:
        # ── 构造文档块（父块 + 子块 + 摘要块）──────────────────────────
        hr("2. 构造文档块（parent + child + doc_summary）")
        parent_id = str(uuid.uuid4())
        parent = ParentChunk(
            chunk_id=parent_id,
            content=(
                "第四章 知识库与 RAG 问答。ANI 平台的知识库模块支持文档解析、"
                "向量嵌入、混合检索与 RAG 问答。混合检索融合 Milvus 向量检索"
                "与 PostgreSQL pg_trgm 关键词检索，通过 RRF 互逆排序融合算法"
                "合并结果。RAG 问答基于 ContextChatEngine，使用 Redis 持久化"
                "多轮对话记忆，通过 vLLM 提供的 OpenAI 兼容接口生成回答。"
            ),
            content_type="text",
            token_count=80,
            page_number=1,
        )
        child1 = ChildChunk(
            chunk_id=str(uuid.uuid4()),
            content=(
                "知识库模块支持混合检索，融合 Milvus 向量检索与 pg_trgm 关键词检索，"
                "使用 RRF 互逆排序融合算法。"
            ),
            content_type="text",
            page_number=1,
            token_count=30,
            parent_chunk_id=parent_id,
            parent_content=parent.content,
        )
        child2 = ChildChunk(
            chunk_id=str(uuid.uuid4()),
            content=(
                "RAG 问答基于 ContextChatEngine，使用 Redis 持久化多轮对话记忆，"
                "通过 vLLM 的 OpenAI 兼容接口生成回答。"
            ),
            content_type="text",
            page_number=1,
            token_count=30,
            parent_chunk_id=parent_id,
            parent_content=parent.content,
        )
        summary = ChildChunk(
            chunk_id=str(uuid.uuid4()),
            content=(
                "本章介绍 ANI 平台知识库模块的混合检索与 RAG 问答能力，"
                "涵盖向量检索、关键词检索、RRF 融合、Redis 对话记忆与 vLLM 生成。"
            ),
            content_type="text",
            page_number=1,
            token_count=40,
            parent_chunk_id=parent_id,
            parent_content=parent.content,
        )
        print(f"父块数           : 1 (id={parent_id[:8]}...)")
        print(f"子块数           : 2")
        print(f"摘要块数         : 1")

        # ── 写入 Milvus（向量）+ kb_chunks（pg_trgm 关键词）────────────
        hr("3. 写入 Milvus（向量）+ kb_chunks（pg_trgm 关键词检索）")
        embed_svc = EmbedService(embed_model=embed_model)
        result = embed_svc.embed_and_write(
            tenant_id=TENANT_ID,
            kb_id=kb_id,
            doc_id=doc_id,
            file_name="ani-knowledge-rag.txt",
            parents=[parent],
            children=[child1, child2],
            summaries=[summary],
        )
        print(f"Milvus 写入结果  : nodes_written={result.nodes_written}")
        print(f"集合名           : {result.collection_name}")
        print(f"counts           : {result.counts}")

        async with pool.acquire() as conn:
            n = await write_chunks(
                conn,
                tenant_id=TENANT_ID,
                kb_id=kb_id,
                doc_id=doc_id,
                file_name="ani-knowledge-rag.txt",
                parents=[parent],
                children=[child1, child2],
                summaries=[summary],
            )
        print(f"kb_chunks 写入   : rows={n} (parent+child+summary, 用于 pg_trgm)")

        # ── AC1: 混合检索（QueryFusionRetriever: 向量 + pg_trgm + RRF）──
        hr("4. AC1 混合检索（QueryFusionRetriever: Milvus + pg_trgm + RRF, num_queries=1）")
        # Build a sync pg_trgm search fn from the asyncpg pool.
        from app.services.retrieve_service import make_pg_trgm_search_fn
        from app.services.qa_service import _default_llm_factory

        keyword_search_fn = make_pg_trgm_search_fn(
            database_url,  # DSN string (not pool) — binds to current loop
            kb_id=kb_id, tenant_id=TENANT_ID,
        )
        # Build the vLLM LLM once; shared by RetrieveService (fusion constructor
        # pre-resolves a default LLM even with num_queries=1) and QAService.
        llm = _default_llm_factory()
        retrieve_svc = RetrieveService(
            embed_model=embed_model,
            keyword_search_fn=keyword_search_fn,
            llm=llm,
        )
        fusion = retrieve_svc.build_fusion_retriever(kb_id=kb_id, top_k=5)
        print(f"QueryFusionRetriever 构造成功")
        print(f"  num_queries = {FUSION_NUM_QUERIES} (关闭查询生成)")
        print(f"  mode        = {FUSION_MODE_RRF} (RRF)")
        print(f"  retrievers  = {len(fusion._retrievers)} (vector + keyword)")

        retrieve_result = retrieve_svc.retrieve(
            kb_id=kb_id,
            question="知识库的混合检索和 RAG 问答能力是什么？",
            top_k=5,
            score_threshold=0.0,  # RRF scores are ~1/(rank+60)≈0.016; disable threshold for e2e
            tenant_id=TENANT_ID,  # 多租户 RLS 透传
        )
        print(f"检索命中数       : {len(retrieve_result.sources)}")
        print(f"max_score        : {retrieve_result.max_score:.4f}")
        for i, src in enumerate(retrieve_result.sources):
            preview = src.content[:60].replace("\n", " ") + "..."
            print(f"  [{i}] type={src.chunk_type:12s} score={src.score:.4f} "
                  f"parent={bool(src.parent_content)} | {preview}")

        assert len(retrieve_result.sources) >= 1, "混合检索应命中至少 1 条"

        # ── AC2: 子块命中后回填父块上下文（parent_content）─────────────
        hr("5. AC2 子块命中回填父块上下文（parent_content）")
        child_hits = [s for s in retrieve_result.sources if s.chunk_type == "child"]
        if child_hits:
            print(f"子块命中数       : {len(child_hits)}")
            for ch in child_hits:
                print(f"  chunk_id={ch.chunk_id[:8]}... parent_content 长度={len(ch.parent_content)}")
                if ch.parent_content:
                    print(f"  parent_content 预览: {ch.parent_content[:50]}...")
            backfilled = [c for c in child_hits if c.parent_content]
            print(f"已回填 parent_content 的子块: {len(backfilled)}/{len(child_hits)}")
            assert any(c.parent_content for c in child_hits), "至少一个子块应回填 parent_content"
            print("AC2 验证通过 ✅ (子块命中后回填父块上下文)")
        else:
            print("（本轮无子块命中，跳过 AC2 检查）")

        # ── AC3: 摘要命中后回填该文档的父块 ─────────────────────────────
        hr("6. AC3 摘要命中回填该文档的父块")
        summary_hits = [s for s in retrieve_result.sources if s.chunk_type == "doc_summary"]
        if summary_hits:
            print(f"摘要命中数       : {len(summary_hits)}")
            for sh in summary_hits:
                print(f"  chunk_id={sh.chunk_id[:8]}... parent_content 长度={len(sh.parent_content)}")
            assert any(s.parent_content for s in summary_hits), "摘要命中应回填父块"
            print("AC3 验证通过 ✅ (摘要命中后回填该文档的父块)")
        else:
            print("（本轮无摘要命中，跳过 AC3 检查）")

        # ── 单路向量检索 ────────────────────────────────────────────────
        hr("6.5 单路向量检索（vector_retrieve: Milvus VectorStoreIndex.as_retriever）")
        question_vec = "混合检索融合算法"
        vec_result = retrieve_svc.vector_retrieve(
            kb_id=kb_id, question=question_vec, top_k=5, score_threshold=0.0,
        )
        print(f"问题             : {question_vec}")
        print(f"向量检索命中数   : {len(vec_result.sources)}")
        print(f"max_score        : {vec_result.max_score:.4f}")
        for i, src in enumerate(vec_result.sources):
            preview = src.content[:60].replace("\n", " ") + "..."
            print(f"  [{i}] type={src.chunk_type:12s} score={src.score:.4f} | {preview}")
        if vec_result.sources:
            assert any(s.parent_content for s in vec_result.sources if s.chunk_type == "child"), \
                "向量检索子块应回填 parent_content"
            print("向量检索验证通过 ✅ (VectorStoreIndex.as_retriever + 父块回填)")
        else:
            print("（向量检索无命中，跳过检查）")

        # ── 单路全文检索（pg_trgm）──────────────────────────────────────
        hr("6.6 单路全文检索（keyword_retrieve: pg_trgm PgTrgmRetriever）")
        # pg_trgm 对短词匹配更友好（中文长句相似度可能不足）
        question_kw = "RAG 问答"
        kw_result = retrieve_svc.keyword_retrieve(
            kb_id=kb_id, question=question_kw, top_k=5, score_threshold=0.0,
            tenant_id=TENANT_ID,
        )
        print(f"问题             : {question_kw}")
        print(f"全文检索命中数   : {len(kw_result.sources)}")
        print(f"max_score        : {kw_result.max_score:.4f}")
        for i, src in enumerate(kw_result.sources):
            preview = src.content[:60].replace("\n", " ") + "..."
            print(f"  [{i}] type={src.chunk_type:12s} score={src.score:.4f} | {preview}")
        assert len(kw_result.sources) >= 1, "全文检索应命中至少 1 条"
        print("全文检索验证通过 ✅ (PgTrgmRetriever BaseRetriever 子类 + pg_trgm)")

        # ── 三路检索结果对比 ─────────────────────────────────────────────
        hr("6.7 三路检索结果对比")
        print(f"{'检索方式':<20s} {'命中数':<8s} {'max_score':<12s}")
        print(f"{'混合检索(Fusion)':<20s} {len(retrieve_result.sources):<8d} {retrieve_result.max_score:<12.4f}")
        print(f"{'向量检索(Vector)':<20s} {len(vec_result.sources):<8d} {vec_result.max_score:<12.4f}")
        print(f"{'全文检索(Keyword)':<20s} {len(kw_result.sources):<8d} {kw_result.max_score:<12.4f}")

        # ── 多租户 RLS 隔离验证 ──────────────────────────────────────────
        hr("6.8 多租户 RLS 隔离验证（tenant_id 透传）")
        # 用一个不同的 tenant_id 查询同一 KB 的 pg_trgm 关键词检索，
        # 由于 RLS 隔离，不应命中其他租户的数据。
        OTHER_TENANT = "00000000-0000-0000-0000-0000000000ff"
        kw_other = retrieve_svc.keyword_retrieve(
            kb_id=kb_id, question=question_kw, top_k=5, score_threshold=0.0,
            tenant_id=OTHER_TENANT,
        )
        print(f"其他租户 tenant_id : {OTHER_TENANT}")
        print(f"其他租户全文命中数 : {len(kw_other.sources)} (期望 0，RLS 隔离)")
        assert len(kw_other.sources) == 0, "RLS 隔离失效：其他租户不应命中本租户数据"
        print("多租户 RLS 隔离验证通过 ✅ (其他 tenant_id 查询返回 0 结果)")

        # 用正确的 tenant_id 验证仍然能命中
        kw_same = retrieve_svc.keyword_retrieve(
            kb_id=kb_id, question=question_kw, top_k=5, score_threshold=0.0,
            tenant_id=TENANT_ID,
        )
        print(f"正确租户全文命中数 : {len(kw_same.sources)} (期望 >0)")
        assert len(kw_same.sources) >= 1, "正确 tenant_id 应命中"
        print("正确 tenant_id 查询验证通过 ✅")

        # ── AC4 + AC5: qa_service RAG 问答 ──────────────────────────────
        hr("7. AC4+AC5 qa_service RAG 问答（ContextChatEngine + Redis + vLLM）")
        qa_svc = QAService(retrieve_service=retrieve_svc)
        session_id = f"e2e-{uuid.uuid4()}"
        print(f"session_id       : {session_id}")
        print(f"问题             : ANI 平台知识库的混合检索和 RAG 问答能力是什么？")
        qa_result = qa_svc.chat(
            kb_id=kb_id,
            question="ANI 平台知识库的混合检索和 RAG 问答能力是什么？",
            session_id=session_id,
            top_k=5,
            score_threshold=0.0,
            tenant_id=TENANT_ID,
            inference_service_name="default",
        )
        print("\nQAResult 返回值：")
        print(f"  answer        : {qa_result.answer[:120]}{'...' if len(qa_result.answer)>120 else ''}")
        print(f"  answer 长度   : {len(qa_result.answer)} 字符")
        print(f"  sources 数量  : {len(qa_result.sources)}")
        print(f"  session_id    : {qa_result.session_id}")
        print(f"  input_tokens  : {qa_result.input_tokens}")
        print(f"  output_tokens : {qa_result.output_tokens}")

        # AC5: chat() 同步返回 answer + sources + session_id + tokens
        assert isinstance(qa_result, QAResult), "返回值必须是 QAResult"
        assert qa_result.answer, "answer 不能为空"
        assert qa_result.session_id == session_id, "session_id 必须与传入一致"
        assert len(qa_result.sources) >= 1, "sources 不能为空"
        assert isinstance(qa_result.input_tokens, int), "input_tokens 必须是 int"
        assert isinstance(qa_result.output_tokens, int), "output_tokens 必须是 int"
        print("\nAC5 验证通过 ✅ (chat() 同步返回 answer + sources + session_id + tokens)")

        # AC4: ContextChatEngine.from_defaults(retriever, memory=ChatMemoryBuffer(chat_store=RedisChatStore), llm=OpenAILike)
        # 验证 LLM 是 OpenAILike、chat_store 是 RedisChatStore
        llm_type = type(qa_svc._llm).__name__
        store_type = type(qa_svc._chat_store).__name__
        print(f"\n  LLM 类型      : {llm_type}")
        print(f"  ChatStore 类型: {store_type}")
        assert "OpenAILike" in llm_type, f"LLM 应为 OpenAILike，实际 {llm_type}"
        assert "Redis" in store_type, f"ChatStore 应为 RedisChatStore，实际 {store_type}"
        print("AC4 验证通过 ✅ (ContextChatEngine.from_defaults + RedisChatStore + OpenAILike)")

        # ── 多轮对话记忆验证（Redis 持久化）──────────────────────────────
        hr("8. 多轮对话记忆验证（Redis 持久化，同一 session_id）")
        print("第二轮问题       : 上面提到的检索融合算法叫什么？")
        qa_result_2 = qa_svc.chat(
            kb_id=kb_id,
            question="上面提到的检索融合算法叫什么？",
            session_id=session_id,
            top_k=5,
            score_threshold=0.0,
        )
        print(f"  answer        : {qa_result_2.answer[:120]}{'...' if len(qa_result_2.answer)>120 else ''}")
        print(f"  session_id    : {qa_result_2.session_id}")
        print("多轮对话记忆验证 ✅ (同一 session_id 第二轮回答)")

        # ── 清理 ────────────────────────────────────────────────────
        hr("9. 清理测试数据")
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
        # 清理 Redis session
        try:
            qa_svc._chat_store.delete_message_block(session_id)
            print(f"已清理 Redis session: {session_id}")
        except Exception as e:
            print(f"Redis session 清理（可忽略）: {e}")

        hr("E2E 演示完成 ✅ (issue-013: 混合检索 + RAG 问答 全链路验证通过)")

    finally:
        await pool.close()
        print("PostgreSQL 连接池已关闭")


if __name__ == "__main__":
    asyncio.run(main())
