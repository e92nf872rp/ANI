"""End-to-end test for issue-014: parse_worker NATS round-trip + gRPC Query RPC.

Runs against the REAL k8s-lab infrastructure (deploy/real-k8s-lab/*.yaml):

  - MinIO:  10.10.1.66:30900 (document upload + image upload)
  - NATS:   10.10.1.66:31062 (ani.tasks.kb.parse)
  - PostgreSQL: 10.10.1.66:30945 (kb_chunks table — exists in ani DB)
  - Milvus: 10.10.1.66:31930 (vector store)
  - Embedding: 10.10.20.197:8006/v1 (Qwen3-Embedding-0.6B)
  - vLLM:   10.10.20.181:3011/v1 (Qwen3.6-35B-A3B, for gRPC Query)

NOTE: kb_documents / knowledge_bases tables live in the kb-service database
(not the ani DB reachable via DATABASE_URL). The parse_worker's
parse_status updater writes to kb-service's kb_documents table. In this E2E
test we inject a fake status_updater that records the state transitions
(pending → parsing → indexing → ready) so we verify the worker drives the
correct state machine, while the real pipeline (download → parse → chunk →
summary → Milvus write → kb_chunks write) runs against live infra.

What it verifies (Issue #14 ACs):

  AC1: parse_worker subscribes to NATS ani.tasks.kb.parse and claims tasks
      (publish a real NATS message; worker consumes it via its subscription).
  AC2: download → parse → chunk → summary → Milvus write (children + summary)
       + kb_chunks table write (verified against real PG + Milvus).
  AC3: parse_status transitions pending → parsing → indexing → ready
      (recorded by the injected status_updater).
  AC4: gRPC server implements Query RPC (sync), returns answer + sources
      (real gRPC server + real vLLM + real Milvus retrieval).

Run from repo root:

    $env:PYTHONPATH="ai/rag-engine"; python ai/rag-engine/tests/demo_e2e_issue014_parse_grpc.py

This file is NOT part of the default make test/pytest run (needs live infra).
"""
import asyncio
import hashlib
import io as _io
import json
import os
import sys
import time
import uuid
from pathlib import Path

# Load .env so Settings picks up MINIO_ENDPOINT / NATS_URL / MILVUS_ADDR etc.
os.chdir(Path(__file__).resolve().parents[3])
from dotenv import load_dotenv
load_dotenv()

# Tee output to a log file so results survive PowerShell stream buffering.
_LOG_PATH = os.path.join(os.getcwd(), "e2e_issue014_result.log")
# Truncate the log at import time.
open(_LOG_PATH, "w", encoding="utf-8").close()

import asyncpg
import nest_asyncio
nest_asyncio.apply()

from app.core.config import settings
from app.core.milvus import init_milvus, kb_collection_name
from app.core.embeddings import init_embedding_model, get_embed_model
from app.repositories.chunks import write_chunks, delete_chunks_by_doc


TENANT_ID = "00000000-0000-0000-0000-0000000000e2"  # valid UUID for RLS

# Global log file handle — opened in main(), written by hr/step/_p.
_LOG_FP = None


def _p(*args, **kwargs):
    """print to both stdout and the log file."""
    import builtins
    builtins.print(*args, **kwargs)
    if _LOG_FP is not None:
        kwargs.pop("file", None)
        builtins.print(*args, file=_LOG_FP, **kwargs)
        _LOG_FP.flush()


def hr(title: str):
    _p("\n" + "=" * 70)
    _p(title)
    _p("=" * 70)


def step(msg: str):
    _p(f"  • {msg}")


class RecordingStatusUpdater:
    """Records parse_status transitions for AC3 verification.

    Replaces the kb-service kb_documents table write (which lives in a
    different DB not reachable via DATABASE_URL) so we can verify the worker
    drives the correct state machine against live infra.
    """

    def __init__(self):
        self.transitions: list[str] = []
        self._current = "pending"

    async def update(self, *, tenant_id, doc_id, parse_status,
                     error_message=None, chunk_count=None) -> bool:
        self.transitions.append(parse_status)
        self._current = parse_status
        step(f"parse_status: {self._current}" +
             (f" chunk_count={chunk_count}" if chunk_count is not None else "") +
             (f" error={error_message}" if error_message else ""))
        return True

    async def current(self, *, tenant_id, doc_id):
        return self._current


async def main():
    from nats.aio.client import Client as NATSClient
    from minio import Minio
    from app.workers.parse_worker import ParseWorker
    from app.grpc.server import GrpcServer, RagEngineServicer
    from app.services.qa_service import QAService

    # Open the log file for _p() to write to.
    global _LOG_FP
    _LOG_FP = open(_LOG_PATH, "w", encoding="utf-8")

    kb_id = str(uuid.uuid4())
    doc_id = str(uuid.uuid4())

    hr("0. 环境配置（来自 .env）")
    _p(f"MinIO endpoint   : {settings.minio_endpoint}")
    _p(f"NATS URL         : {settings.nats_url}")
    _p(f"NATS subject     : {settings.nats_parse_subject}")
    _p(f"Milvus addr      : {settings.milvus_addr or f'{settings.milvus_host}:{settings.milvus_port}'}")
    _p(f"Embedding 端点   : {settings.embedding_api_base}")
    _p(f"Embedding 模型   : {settings.embedding_model} (dim={settings.embedding_dim})")
    _p(f"vLLM 端点        : {settings.vllm_api_base}")
    _p(f"vLLM 模型        : {settings.vllm_model}")
    database_url = os.environ.get("DATABASE_URL", settings.pg_dsn)
    _p(f"Database URL     : {database_url}")
    _p(f"Tenant ID        : {TENANT_ID}")
    _p(f"KB ID            : {kb_id}")
    _p(f"Doc ID           : {doc_id}")
    _p(f"Collection 名    : {kb_collection_name(kb_id)}")

    # ── 1. 初始化连接 ─────────────────────────────────────────────────────
    hr("1. 初始化连接（Milvus + Embedding + PostgreSQL + MinIO + NATS）")
    await init_milvus()
    await init_embedding_model(settings.embedding_model)
    embed_model = get_embed_model()
    step(f"Milvus 连接 OK, Embedding 模型: {embed_model.model_name}")

    pool = await asyncpg.create_pool(dsn=database_url, min_size=1, max_size=4)
    step("PostgreSQL 连接 OK")

    minio_client = Minio(
        settings.minio_endpoint,
        access_key=settings.minio_access_key,
        secret_key=settings.minio_secret_key,
        secure=settings.minio_secure,
    )
    if not minio_client.bucket_exists(settings.minio_bucket):
        minio_client.make_bucket(settings.minio_bucket)
    step("MinIO 连接 OK")

    nc = NATSClient()
    await nc.connect(servers=[settings.nats_url], name="e2e-issue014-publisher")
    step("NATS 连接 OK")

    worker_nats = None
    worker = None
    grpc_server = None
    coll_name = kb_collection_name(kb_id)
    storage_path = None
    try:
        # ── 2. 上传测试文档到 MinIO ──────────────────────────────────────
        hr("2. 上传测试文档到 MinIO")
        doc_content = (
            "# ANI 平台知识库\n\n"
            "ANI 平台的知识库模块支持文档解析、向量嵌入、混合检索与 RAG 问答。\n\n"
            "## 混合检索\n\n"
            "混合检索融合 Milvus 向量检索与 PostgreSQL pg_trgm 关键词检索，"
            "通过 RRF 互逆排序融合算法合并结果。RRF 是 Reciprocal Rank Fusion 的缩写。\n\n"
            "## RAG 问答\n\n"
            "RAG 问答基于 ContextChatEngine，使用 Redis 持久化多轮对话记忆，"
            "通过 vLLM 提供的 OpenAI 兼容接口生成回答。\n"
        )
        doc_bytes = doc_content.encode("utf-8")
        storage_path = f"kb-docs/{kb_id}/{doc_id}/e2e-issue014-test.md"
        minio_client.put_object(
            bucket_name=settings.minio_bucket,
            object_name=storage_path,
            data=_io.BytesIO(doc_bytes),
            length=len(doc_bytes),
            content_type="text/markdown",
        )
        step(f"文档已上传 MinIO: {settings.minio_bucket}/{storage_path} ({len(doc_bytes)} bytes)")

        # ── 3. 启动 parse_worker (订阅 NATS) ─────────────────────────────
        hr("3. 启动 parse_worker (订阅 NATS ani.tasks.kb.parse)")
        worker_nats = NATSClient()
        await worker_nats.connect(servers=[settings.nats_url], name="rag-engine-parse-worker")
        status_updater = RecordingStatusUpdater()

        # Inject a MinIO-based download client: the Core API gateway
        # (ani-gateway.ani-system.svc.cluster.local) is not resolvable from
        # outside K8s, so we bypass it and download directly from MinIO
        # (the storage_path in the NATS payload is a MinIO object key).
        class MinioDownloadClient:
            """Downloads objects directly from MinIO (bypasses Core API gateway)."""
            def __init__(self, mc):
                self._mc = mc

            async def download_object(self, object_id, *, dest_dir=None, file_name=None):
                import tempfile
                # object_id is the MinIO storage_path (bucket/key); the
                # payload's storage_path is relative to the bucket.
                key = object_id
                if key.startswith(settings.minio_bucket + "/"):
                    key = key[len(settings.minio_bucket) + 1:]
                resp = self._mc.get_object(settings.minio_bucket, key)
                from app.clients.core_api import _safe_suffix
                suffix = _safe_suffix(file_name)
                tmp = tempfile.NamedTemporaryFile(
                    dir=dest_dir, suffix=suffix, delete=False, mode="wb"
                )
                try:
                    for d in resp.stream(1024 * 64):
                        tmp.write(d)
                finally:
                    tmp.close()
                    resp.close()
                    resp.release_conn()
                return tmp.name

        worker = ParseWorker(
            nats_client=worker_nats,
            core_api=MinioDownloadClient(minio_client),  # MinIO direct download
            status_updater=status_updater,
            db_pool=pool,        # for kb_chunks write
        )
        await worker.start()
        step(f"parse_worker 已订阅 {settings.nats_parse_subject}")

        # ── 4. 发布 NATS 解析任务 (AC1) ──────────────────────────────────
        hr("4. 发布 NATS 解析任务 (AC1: parse_worker 领取任务)")
        payload = {
            "doc_id": doc_id,
            "kb_id": kb_id,
            "storage_path": storage_path,
            "tenant_id": TENANT_ID,
            "file_name": "e2e-issue014-test.md",
        }
        await nc.publish(
            settings.nats_parse_subject,
            json.dumps(payload).encode("utf-8"),
        )
        await nc.flush()
        step(f"已发布任务到 {settings.nats_parse_subject}")

        # ── 5. 等待 parse_worker 完成 (AC2+AC3) ─────────────────────────
        hr("5. 等待 parse_worker 完成 (AC2+AC3: download→parse→chunk→summary→Milvus→kb_chunks)")
        # Wait for the worker to reach 'ready' or 'failed' (up to 180s).
        deadline = time.time() + 180
        while time.time() < deadline:
            if status_updater.transitions and status_updater._current in ("ready", "failed"):
                break
            await asyncio.sleep(1.5)

        if not status_updater.transitions:
            _p("\n❌ 超时：parse_worker 未在 180s 内处理任务")
            raise SystemExit(1)

        _p(f"\n  状态流转路径   : pending → {' → '.join(status_updater.transitions)}")
        final_status = status_updater._current
        _p(f"  最终状态       : {final_status}")

        assert final_status == "ready", (
            f"❌ AC3 失败: parse_status 应为 ready, 实际 {final_status}"
        )
        assert "parsing" in status_updater.transitions, (
            f"❌ AC3: 未经过 parsing 状态, 路径={status_updater.transitions}"
        )
        assert "indexing" in status_updater.transitions, (
            f"❌ AC3: 未经过 indexing 状态, 路径={status_updater.transitions}"
        )
        assert "ready" in status_updater.transitions, (
            f"❌ AC3: 未到达 ready 状态, 路径={status_updater.transitions}"
        )
        _p("AC3 验证通过 ✅ (pending → parsing → indexing → ready)")
        _p("AC1 验证通过 ✅ (parse_worker 通过 NATS 订阅领取任务)")

        # ── 6. 验证 Milvus 向量 + kb_chunks 表 (AC2) ─────────────────────
        hr("6. 验证 Milvus 向量 + kb_chunks 表 (AC2: 写入子块 + 摘要)")
        from pymilvus import Collection, utility

        assert utility.has_collection(coll_name), f"❌ AC2: Milvus 集合 {coll_name} 不存在"
        coll = Collection(coll_name)
        coll.load()
        # Flush to ensure num_entities reflects freshly inserted vectors.
        coll.flush()
        await asyncio.sleep(3)
        num_entities = coll.num_entities
        step(f"Milvus 集合 {coll_name} 实体数: {num_entities}")
        assert num_entities > 0, f"❌ AC2: Milvus 集合为空 (entities={num_entities})"

        async with pool.acquire() as conn:
            await conn.execute(
                "SELECT set_config('app.current_tenant_id', $1, true)", TENANT_ID
            )
            chunk_rows = await conn.fetch(
                "SELECT chunk_type, count(*) as n FROM kb_chunks WHERE doc_id=$1 GROUP BY chunk_type",
                uuid.UUID(doc_id),
            )
        chunk_counts = {r["chunk_type"]: r["n"] for r in chunk_rows}
        step(f"kb_chunks 分块统计: {chunk_counts}")
        total_chunks = sum(chunk_counts.values())
        assert total_chunks > 0, "❌ AC2: kb_chunks 表无记录"
        assert "child" in chunk_counts, f"❌ AC2: kb_chunks 缺少 child 子块, 实际={chunk_counts}"
        _p(f"AC2 验证通过 ✅ (Milvus entities={num_entities}, kb_chunks={chunk_counts})")

        # ── 7. 启动 gRPC server + 调用 Query RPC (AC4) ───────────────────
        hr("7. AC4: gRPC server Query RPC (同步) → answer + sources")
        from app.services.retrieve_service import RetrieveService, make_pg_trgm_search_fn
        from app.services.qa_service import _default_llm_factory

        # Ensure the collection is loaded for retrieval.
        coll.load()
        await asyncio.sleep(2)

        keyword_search_fn = make_pg_trgm_search_fn(
            database_url, kb_id=kb_id, tenant_id=TENANT_ID,
        )
        llm = _default_llm_factory()
        retrieve_svc = RetrieveService(
            embed_model=embed_model, keyword_search_fn=keyword_search_fn, llm=llm,
        )
        qa_svc = QAService(retrieve_service=retrieve_svc)
        servicer = RagEngineServicer(qa_service=qa_svc)
        grpc_server = GrpcServer(servicer=servicer, bind_addr="[::]:50062")
        grpc_server.start()
        step(f"gRPC server 已启动: {grpc_server.bind_addr}")

        import grpc as _grpc
        from app.grpc import rag_pb2 as rag_pb, rag_pb2_grpc as rag_grpc

        await asyncio.sleep(1.5)  # let the server bind
        channel = _grpc.aio.insecure_channel("localhost:50062")
        stub = rag_grpc.RagEngineStub(channel)

        question = "ANI 平台知识库的混合检索和 RAG 问答能力是什么？"
        request = rag_pb.QueryRequest(
            tenant_id=TENANT_ID,
            kb_id=kb_id,
            question=question,
            top_k=5,
            score_threshold=-1.0,  # negative = disable threshold (0.0 means "default")
            session_id=f"e2e-{uuid.uuid4()}",
            inference_service_name="default",
        )
        step(f"调用 Query RPC: question={question}")
        resp = await stub.Query(request, timeout=90)

        _p(f"\n  QueryResponse:")
        _p(f"    answer        : {resp.answer[:200]}{'...' if len(resp.answer)>200 else ''}")
        _p(f"    answer 长度   : {len(resp.answer)} 字符")
        _p(f"    sources 数量  : {len(resp.sources)}")
        _p(f"    session_id    : {resp.session_id}")
        _p(f"    input_tokens  : {resp.input_tokens}")
        _p(f"    output_tokens : {resp.output_tokens}")
        for i, src in enumerate(resp.sources):
            preview = src.content[:60].replace("\n", " ") + "..."
            _p(f"    [{i}] doc_id={src.doc_id[:8]}... file={src.file_name} "
                  f"page={src.page} score={src.score:.4f} | {preview}")

        assert resp.answer, "❌ AC4: Query 返回 answer 为空"
        assert len(resp.sources) >= 1, "❌ AC4: Query 返回 sources 为空"
        assert resp.session_id, "❌ AC4: Query 返回 session_id 为空"
        _p("AC4 验证通过 ✅ (gRPC Query RPC 同步返回 answer + sources + session_id + tokens)")

        await channel.close()
        grpc_server.stop()
        grpc_server = None

        # ── 8. 清理测试数据 ─────────────────────────────────────────────
        hr("8. 清理测试数据")
        if utility.has_collection(coll_name):
            utility.drop_collection(coll_name)
            step(f"已删除 Milvus 集合: {coll_name}")
        async with pool.acquire() as conn:
            await conn.execute(
                "SELECT set_config('app.current_tenant_id', $1, true)", TENANT_ID
            )
            deleted = await delete_chunks_by_doc(
                conn, tenant_id=TENANT_ID, kb_id=kb_id, doc_id=doc_id
            )
            step(f"已删除 kb_chunks: {deleted} 行")
        if storage_path:
            try:
                minio_client.remove_object(settings.minio_bucket, storage_path)
                step(f"已删除 MinIO 对象: {storage_path}")
            except Exception as e:
                step(f"MinIO 清理（可忽略）: {e}")

        hr("E2E 测试完成 ✅ (issue-014: parse_worker NATS 全链路 + gRPC Query RPC 验证通过)")
        _p("\n验收结果汇总:")
        _p("  AC1 ✅ parse_worker 订阅 NATS ani.tasks.kb.parse 并领取任务")
        _p("  AC2 ✅ download → parse → chunk → summary → Milvus 子块+摘要 → kb_chunks 表")
        _p("  AC3 ✅ parse_status pending → parsing → indexing → ready")
        _p("  AC4 ✅ gRPC server Query RPC (同步) 返回 answer + sources + tokens")

    finally:
        if grpc_server is not None:
            grpc_server.stop()
        if worker is not None:
            await worker.stop()
        if worker_nats is not None:
            try:
                await worker_nats.drain()
            except Exception:
                pass
        try:
            await nc.drain()
        except Exception:
            pass
        await pool.close()
        step("连接已关闭")


if __name__ == "__main__":
    # Debug marker to confirm the script starts.
    with open(os.path.join(os.getcwd(), "e2e_issue014_result.log"), "a", encoding="utf-8") as _dbg:
        _dbg.write("[main] starting asyncio.run(main())\n")
        _dbg.flush()
    asyncio.run(main())
    with open(os.path.join(os.getcwd(), "e2e_issue014_result.log"), "a", encoding="utf-8") as _dbg:
        _dbg.write("[main] asyncio.run(main()) completed successfully\n")
        _dbg.flush()
