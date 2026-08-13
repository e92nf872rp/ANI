"""端到端展示一个文档的真实分块 (chunk) 并验证分块规则。

流程 (复用 P0 链路, 直接调 ani-gateway :8080):
  create KB (chunk_size=<CHUNK_SIZE>) -> 上传一份带多级标题的 markdown 到 MinIO
  -> notify-uploaded -> 轮询 parse_status=ready -> 从 kb_chunks 表读出全部 chunk
  -> 打印每个 chunk 的 section_path / heading_level / token_count / 内容片段

验证点:
  1) 按标题分段: 每个新标题 (sub_type=heading) 应开启新的 section_path,
     child chunk 的 custom_metadata.section_path 应体现文档标题分层。
  2) chunk 上限: 每个 child chunk 的 token_count (估算 = len//2) 应 <= CHUNK_SIZE。

用法:
  python scripts/show_doc_chunks.py [chunk_size] [--show-full]
"""
import asyncio
import json
import sys
import time
import uuid

import asyncpg
import requests

GATEWAY = "http://localhost:8080"
TENANT_ID = "00000000-0000-0000-0000-000000000001"
USER_ID = "00000000-0000-0000-0000-000000000002"
HEADERS = {
    "X-Dev-Tenant-ID": TENANT_ID,
    "X-Dev-User-ID": USER_ID,
    "Content-Type": "application/json",
}
PG_DSN = "postgresql://ani:ani_dev_password@10.10.1.66:30945/ani"

SHOW_FULL = len(sys.argv) > 2 and sys.argv[2] == "--show-full"
CHUNK_SIZE = int(sys.argv[1]) if len(sys.argv) >= 2 and sys.argv[1].isdigit() else 512

# 一份含「超长章节(约5000字连续正文) + 多级短标题章节」的知识库文档 (UTF-8, markdown)。
# 超长章节用于验证 chunk_size 是否真正决定切分粒度：不同 chunk 值应产生不同数量的子块，
# 且每个子块 token_count <= chunk_size。


def _gen_long_body(target_chars: int) -> str:
    """生成目标字符数的连续中文正文段落 (无标题, 用于触发按 chunk_size 滚动切分)。"""
    sentence = (
        "ANI 专有云平台面向大规模 GPU 算力集群提供一体化的研发、纳管、调度与运营能力，"
        "覆盖从集群资源池化、作业队列调度、知识库构建到模型推理服务的完整链路。"
        "平台采用微服务架构，将网关、认证、知识库与 RAG 引擎等核心能力解耦，"
        "以便独立扩展、独立升级并降低单点故障风险。"
    )
    # 一些用于让文本更丰富的独立短句, 循环混入, 形成连续可读的长正文
    extras = [
        "网关负责承接所有外部请求并完成鉴权、限流与路由转发。",
        "知识库服务维护文档与分块的元数据关系。",
        "解析流水线将原始文档结构化并按标题组织成父子分块。",
        "向量库保存嵌入向量用于相似度检索。",
        "作业调度器根据资源空余与优先级对训练任务排队执行。",
        "监控组件持续上报节点负载与健康状态。",
        "权限模型内置了租户、角色与成员三级隔离。",
        "对象存储统一保管原始文档与解析产物。",
    ]
    body: list[str] = []
    total = 0
    idx = 0
    limit = target_chars * 2  # 安全上限, 防止死循环
    while total < target_chars and len(body) < limit:
        piece = sentence if idx % 3 != 0 else extras[idx % len(extras)]
        body.append(piece)
        total += len(piece)
        idx += 1
    return "\n\n".join(body)


def _build_doc() -> str:
    # 超长章节(约 5000 字) —— 无子标题, 用于验证 chunk_size 切实决定切分
    long_sec = "## 五、超长章节（用于验证 chunk 切分）\n\n" + _gen_long_body(5000)
    # 短标题章节 —— 用于验证「按标题分段」
    short_secs = (
        "# ANI 专有云平台使用指南\n\n"
        "本指南介绍 ANI 专有云平台的核心能力与使用方法。\n\n"
        "## 一、快速开始\n\n"
        "系统登录后即可进入控制台概览页，查看集群整体资源状况。\n\n"
        "## 二、算力资源管理\n\n"
        "平台支持统一纳管多机多卡 GPU 集群。\n\n"
        "## 三、知识库与 RAG\n\n"
        "文档上传后自动解析并按标题分块，段落与列表会被结构化为可检索的单元。\n\n"
        "## 四、常见问题\n\n"
        "如遇资源不足，请及时扩容节点或调整作业优先级。\n\n"
    )
    return short_secs + long_sec


DOC_CONTENT = _build_doc()


def _trunc(text: str, n: int = 90) -> str:
    text = text.replace("\n", " ")
    return text if len(text) <= n else text[:n] + "..."


def main() -> int:
    print(f"chunk_size = {CHUNK_SIZE}, show_full = {SHOW_FULL}")
    kb_id = None
    doc_id = None
    try:
        # 1) create KB
        r = requests.post(
            f"{GATEWAY}/api/v1/svc/knowledge-bases",
            headers=HEADERS,
            json={
                "idempotency_key": f"show_chunks_{uuid.uuid4()}",
                "name": f"chunk-demo-{int(time.time())}",
                "description": "展示分块验证",
                "embedding_model": "Qwen3-Embedding-0.6B",
                "chunk_size": CHUNK_SIZE,
                "top_k": 5,
                "score_threshold": 0.3,
            },
            timeout=30,
        )
        kb = r.json()
        kb_id = kb["id"]
        print(f"[CreateKB] status={r.status_code} kb_id={kb_id}")

        # 2) upload url
        r = requests.post(
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            headers=HEADERS,
            json={
                "idempotency_key": f"show_chunks_up_{uuid.uuid4()}",
                "file_name": "ani-guide.md",
                "file_type": "md",
                "file_size_bytes": len(DOC_CONTENT.encode("utf-8")),
                "checksum_sha256": "0" * 64,
            },
            timeout=30,
        )
        body = r.json()
        doc_id = body["doc_id"]
        upload_url = body["upload_url"]
        print(f"[UploadURL] status={r.status_code} doc_id={doc_id}")

        # 3) PUT to MinIO
        r = requests.put(upload_url, data=DOC_CONTENT.encode("utf-8"),
                         headers={"Content-Type": "text/markdown"}, timeout=30)
        print(f"[MinIO PUT] status={r.status_code}")

        # 4) notify
        r = requests.post(
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}/notify-uploaded",
            headers=HEADERS, json={"storage_path": body["storage_path"]}, timeout=30,
        )
        print(f"[Notify] status={r.status_code}")

        # 5) poll until ready
        final_status = None
        for _ in range(30):
            time.sleep(5)
            r = requests.get(
                f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
                headers=HEADERS, params={"limit": "20"}, timeout=30,
            )
            items = r.json().get("items", [])
            if not items:
                continue
            final_status = items[0].get("parse_status")
            print(f"[poll] parse_status={final_status} chunk_count={items[0].get('chunk_count')}")
            if final_status in ("ready", "failed"):
                break
        if final_status != "ready":
            print("!! 解析未达到 ready")
            return 1

        # 6) read kb_chunks
        asyncio.run(_dump_chunks(kb_id, doc_id))
        return 0
    finally:
        # cleanup KB (同时连带删除文档/chunks)
        if kb_id:
            try:
                r = requests.delete(f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}",
                                    headers=HEADERS, timeout=30)
                print(f"[Cleanup DeleteKB] status={r.status_code}")
            except Exception as e:  # noqa: BLE001
                print(f"[Cleanup] {e}")


async def _dump_chunks(kb_id: str, doc_id: str) -> None:
    conn = await asyncpg.connect(PG_DSN)
    try:
        # RLS 租户上下文 (与 kb-service / rag-engine repository 一致)
        async with conn.transaction():
            await conn.execute(
                "SELECT set_config('app.current_tenant_id', $1, true)", TENANT_ID
            )
            rows = await conn.fetch(
                """
                SELECT chunk_type, content_type, content, token_count,
                       file_name, page_number, parent_chunk_id, custom_metadata
                FROM kb_chunks
                WHERE kb_id = $1 AND doc_id = $2
                ORDER BY id
                """,
                uuid.UUID(kb_id), uuid.UUID(doc_id),
            )
    finally:
        await conn.close()

    print("\n" + "=" * 80)
    print(f"共读取 {len(rows)} 条 chunk (文档: {doc_id})")
    print("=" * 80)

    children = [r for r in rows if r["chunk_type"] == "child"]
    parents = [r for r in rows if r["chunk_type"] == "parent"]
    print(f"\nchild chunks = {len(children)}, parent chunks = {len(parents)}\n")

    def _md(v):
        # asyncpg 将 jsonb 反序列化为 str; 兼容 dict/str
        if isinstance(v, str):
            try:
                return json.loads(v) if v else {}
            except Exception:  # noqa: BLE001
                return {}
        return v or {}

    # ── child chunks ──
    over = []
    for i, c in enumerate(children, 1):
        md = _md(c["custom_metadata"])
        sp = md.get("section_path", "")
        ht = md.get("heading_level")
        sub = md.get("sub_type", "")
        tc = c["token_count"]
        flag = ""
        if tc > CHUNK_SIZE:
            over.append((i, tc))
            flag = "  <== 超过 chunk 上限!"
        print(f"[{i:02d}] type={c['chunk_type']} ctype={c['content_type']} "
              f"tokens={tc} sec_path='{sp}' h{ht} sub={sub}{flag}")
        body = c["content"] if SHOW_FULL else _trunc(c["content"], 160)
        print(f"     content: {body}")

    # ── parent chunks (概要) ──
    print("\n--- parent chunks 概要 ---")
    for i, p in enumerate(parents, 1):
        md = _md(p["custom_metadata"])
        print(f"[P{i:02d}] tokens={p['token_count']} sec_path='{md.get('section_path')}' "
              f"content({len(p['content'])}chars): {_trunc(p['content'], 120)}")

    # ── 验证结论 ──
    print("\n" + "=" * 80)
    print("验证结果:")
    # 1) 按标题分段: 相邻 child 的 section_path 是否随标题分节
    distinct_sections = []
    for c in children:
        sp = _md(c["custom_metadata"]).get("section_path")
        if sp and sp not in distinct_sections:
            distinct_sections.append(sp)
    print(f"  ① 按标题分段: 文档被划分为 {len(distinct_sections)} 个独立 section_path")
    for sp in distinct_sections:
        print(f"       - {sp}")
    # 2) chunk 上限
    if over:
        print(f"  ② 每个 child chunk 大小 ≤ {CHUNK_SIZE}: FAIL ({len(over)} 条超标)")
        for i, tc in over:
            print(f"       chunk#{i} tokens={tc} > {CHUNK_SIZE}")
    else:
        max_tc = max((c["token_count"] for c in children), default=0)
        print(f"  ② 每个 child chunk 大小 ≤ {CHUNK_SIZE}: PASS (最大 token_count = {max_tc})")

    # 3) chunk_size 是否决定切分粒度: 统计「超长章节」这个 section 派生出的子块数。
    #    同一超长正文, chunk_size 越小 -> 子块越多; 每个子块 token_count 应接近但不超过 chunk_size。
    long_sec_blocks = [
        c for c in children
        if "超长章节" in (_md(c["custom_metadata"]).get("section_path") or "")
    ]
    if long_sec_blocks:
        n = len(long_sec_blocks)
        tcs = [c["token_count"] for c in long_sec_blocks]
        print(f"  ③ [chunk 值决定切分粒度] 超长章节(约5000字) 在 chunk_size={CHUNK_SIZE} 下被切成 {n} 个子块")
        print(f"       各子块 token_count = {tcs}")
        print(f"       子块数随 chunk 值变化: {'是(更小 chunk -> 更多子块)' if n > 1 else '该正文未超当前 chunk 阈值'}")
        print(f"       是否全部 ≤ chunk_size: {all(t <= CHUNK_SIZE for t in tcs)}")
    else:
        print("  ③ [chunk 值决定切分粒度] 未找到超长章节子块")

    # 4) 每个 section 产生的子块数 (用于对照不同 section 的切分)
    print("  ④ 各 section 子块数量分布:")
    from collections import Counter
    sec_counter = Counter(_md(c["custom_metadata"]).get("section_path") for c in children)
    for sp, cnt in sorted(sec_counter.items()):
        print(f"       cnt={cnt}  {sp}")


if __name__ == "__main__":
    sys.exit(main())
