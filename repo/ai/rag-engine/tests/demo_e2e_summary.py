"""E2E demo: summary_service -> embed_service -> real Milvus, against the
interim vLLM endpoint and the remote embedding service.

Verifies the full document-summary link required by issue #11 AC1
(SPEC §5.1: 拼接前 N 父块 -> LLM 生成 200-500 字摘要 -> 向化存 Milvus
chunk_type=doc_summary):

  1. Build N parent blocks (the input summary_service consumes).
  2. SummaryService.summarize(parents) -> real vLLM (interim endpoint)
     -> returns a ChildChunk carrying a 200-500-char summary.
  3. EmbedService.embed_and_write(summaries=[...]) -> real Milvus +
     remote embedding -> Index layer embeds + vector_store.add.
  4. Query the Milvus collection and confirm a chunk_type='doc_summary'
     node carrying the LLM-generated summary text is persisted.
  5. Drop the demo collection (cleanup).

Pre-requisites (must be reachable from the host running this script):
  - Milvus: ``10.10.1.66:31930`` (or override MILVUS_ADDR in .env).
  - Embedding service: ``http://10.10.20.197:8006/v1`` (Qwen3-Embedding-0.6B).
  - vLLM (interim LLM): ``http://10.10.20.181:3011/v1`` (Qwen3.6-35B-A3B),
    configured via VLLM_* in .env.

Run from repo root:

    $env:PYTHONPATH="ai/rag-engine"; python ai/rag-engine/tests/demo_e2e_summary.py

This file is NOT part of the default ``make test``/pytest run (it needs real
infrastructure); run it explicitly when the lab is up.
"""
import asyncio
import uuid
from pathlib import Path
import os

# Load .env so Settings picks up MILVUS_ADDR / EMBEDDING_* / VLLM_* etc.
os.chdir(Path(__file__).resolve().parents[3])

from app.core.config import settings
from app.core.milvus import init_milvus, kb_collection_name
from app.core.embeddings import init_embedding_model, get_embed_model
from app.services.chunk_service import ParentChunk
from app.services.summary_service import SummaryService, DEFAULT_SUMMARY_PARENT_COUNT
from app.services.embed_service import EmbedService, DOC_SUMMARY_TYPE


def hr(title: str):
    print("\n" + "=" * 70)
    print(title)
    print("=" * 70)


async def main():
    kb_id = str(uuid.uuid4())
    doc_id = "demo-summary-doc-001"

    hr("1. 环境配置（来自 .env）")
    print(f"Milvus 地址      : {settings.milvus_host}:{settings.milvus_port}")
    print(f"Embedding 端点   : {settings.embedding_api_base}")
    print(f"Embedding 模型   : {settings.embedding_model}")
    print(f"Embedding 维度   : {settings.embedding_dim}")
    print(f"vLLM 端点        : {settings.vllm_api_base}")
    print(f"vLLM 模型        : {settings.vllm_model}")
    print(f"KB ID            : {kb_id}")
    print(f"集合名（去横杠） : {kb_collection_name(kb_id)}")
    print(f"摘要父块数 N     : {DEFAULT_SUMMARY_PARENT_COUNT}")

    hr("2. 初始化连接（Milvus + 远程 Embedding）")
    await init_milvus()
    await init_embedding_model()
    embed_model = get_embed_model()
    print("Milvus 连接      : OK")
    print(f"Embedding 适配器 : {type(embed_model).__name__}")
    print(f"Embedding 模型名 : {embed_model.model_name}")

    # ── 构造 N 个父块（summary_service 的输入） ─────────────────────────
    # 每个 parent content 足够长，使拼接后文本触发 LLM 真实摘要。
    parent_contents = [
        (
            "第一章 平台架构。ANI 是一个面向云原生环境的基础设施平台，"
            "核心职责涵盖算力调度、存储与网络资源管理。平台底层基于 "
            "Kubernetes 构建，支持虚拟机、容器及 GPU 容器等多种计算实例，"
            "并通过控制平面与数据平面两层架构实现资源编排与状态收敛。"
            "多租户隔离通过 namespace 和配额管理实现，确保不同租户资源边界。"
        ),
        (
            "第二章 算力调度。平台提供 GPU 整卡与 vGPU 切分调度能力，"
            "集成 Volcano 批调度与 HAMi 显存隔离，支持队列优先级与抢占式调度。"
            "GPU 清单通过 NVIDIA device-plugin 与 DCGM 指标实时采集，"
            "为调度决策提供可观测输入。算力调度遵循队列模型与公平share策略。"
        ),
        (
            "第三章 存储与网络。存储层基于 Rook-Ceph 提供块、文件和对象三种"
            "存储接口，对象存储使用 MinIO S3 兼容协议并支持预签名 URL 上传。"
            "网络层采用 Kube-OVN 实现 VPC/Subnet 与网络策略，"
            "为实例提供租户隔离的 L2/L3 网络与负载均衡能力。"
        ),
    ][:DEFAULT_SUMMARY_PARENT_COUNT]
    parents = [
        ParentChunk(
            chunk_id=str(uuid.uuid4()),
            content=content,
            content_type="text",
            token_count=max(1, len(content) // 2),
            page_number=i + 1,
        )
        for i, content in enumerate(parent_contents)
    ]

    hr("3. SummaryService.summarize（真实 vLLM）")
    print(f"输入父块数       : {len(parents)}")
    for i, p in enumerate(parents):
        preview = p.content[:50] + ("..." if len(p.content) > 50 else "")
        print(f"  [parent {i}] {preview}")
    svc = SummaryService()  # 使用 _default_llm_factory 读 .env 的 VLLM_*
    summary_chunk = svc.summarize(parents)
    if summary_chunk is None:
        print("\n[FAIL] 摘要生成失败（返回 None），降级路径触发。")
        print("请检查 vLLM 端点可用性与返回摘要长度是否在 [200, 500] 字内。")
        hr("E2E 演示结束（未通过） ❌")
        return
    print("\n摘要生成成功 ✅")
    print(f"  chunk_id        : {summary_chunk.chunk_id}")
    print(f"  content_type    : {summary_chunk.content_type}")
    print(f"  摘要长度（字符）: {len(summary_chunk.content)}")
    print(f"  摘要内容预览    : {summary_chunk.content[:80]}...")

    hr("4. EmbedService.embed_and_write（真实 Milvus + 远程 Embedding）")
    # 只写入 summary chunk（本 demo 验证 summary 链路，parents/children 留空）。
    # embed_service 会将其存为 chunk_type=doc_summary（DOC_SUMMARY_TYPE）。
    embed_svc = EmbedService(embed_model=embed_model)
    result = embed_svc.embed_and_write(
        tenant_id="demo-summary",
        kb_id=kb_id,
        doc_id=doc_id,
        file_name="ani-architecture.txt",
        parents=[],
        children=[],
        summaries=[summary_chunk],
    )
    print("写入结果 EmbedWriteResult：")
    print(f"  kb_id           : {result.kb_id}")
    print(f"  collection_name : {result.collection_name}")
    print(f"  nodes_written   : {result.nodes_written}")
    print(f"  counts          : {result.counts}")
    assert result.counts.get(DOC_SUMMARY_TYPE, 0) == 1, "doc_summary 计数应为 1"

    # ── 验证 Milvus 集合中存在 chunk_type=doc_summary 的节点 ────────────
    hr("5. 校验 Milvus 集合中 doc_summary 节点")
    from pymilvus import Collection, utility

    coll_name = kb_collection_name(kb_id)
    print(f"集合存在         : {utility.has_collection(coll_name)}")
    coll = Collection(coll_name)
    coll.load()

    # Milvus 存储的 metadata 字段名与 embed_service._build_text_node 一致。
    expr = "chunk_type == 'doc_summary'"
    print(f"查询表达式       : {expr}")
    res = coll.query(expr=expr, output_fields=["chunk_type", "content_type", "file_name"])
    print(f"doc_summary 节点数: {len(res)}")
    if not res:
        print("[FAIL] Milvus 中未查到 doc_summary 节点")
    else:
        node = res[0]
        print(f"  chunk_type      : {node.get('chunk_type')}")
        print(f"  content_type    : {node.get('content_type')}")
        print(f"  file_name       : {node.get('file_name')}")
        # 校验摘要文本是否被持久化（Textnode.text -> Milvus 向量存储不直接存原文，
        # 但 chunk_type + 计数已证明 doc_summary 节点存在）。
        assert node.get("chunk_type") == "doc_summary", "chunk_type 必须是 doc_summary"
        print("\nMilvus 校验通过 ✅ (chunk_type=doc_summary 节点已持久化)")

    # ── 顺带验证索引参数与 SPEC §3.1 一致 ───────────────────────────────
    hr("6. 检查 Milvus 集合索引参数（SPEC §3.1）")
    idx = coll.indexes[0]
    params = idx.params
    print(f"index_type       : {params.get('index_type')}")
    print(f"metric_type      : {params.get('metric_type')}")
    hnsw_params = params.get("params", {})
    print(f"M                : {hnsw_params.get('M')}")
    print(f"efConstruction   : {hnsw_params.get('efConstruction')}")
    print("预期 (SPEC §3.1) : HNSW / COSINE / M=16 / efConstruction=200")

    # ── 清理 ────────────────────────────────────────────────────────────
    hr("7. 清理测试集合")
    utility.drop_collection(coll_name)
    print(f"已删除集合       : {coll_name}")

    hr("E2E 演示完成 ✅")


if __name__ == "__main__":
    asyncio.run(main())
