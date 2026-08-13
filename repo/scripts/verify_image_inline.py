#!/usr/bin/env python3
"""验证：新解析的图片内联为 `![图片](完整MinIO URL)`，且该 URL 可无签名 GET 显示。

复用 test_kb_p0_content 的含图 docx 生成，走 上传 → 解析 → 查库：
  1. kb_chunks 中图片内联是否为 `![alt](http://10.10.1.66:30900/ani-kb-docs/{key})`（完整 URL + 图片标记）
  2. 提取该 URL 无签名 GET 返回 200 && content-type=image/*（验证 images/ 前缀 public-read 生效）
"""
import asyncio
import hashlib
import re
import sys
import time
import uuid
from pathlib import Path

import asyncpg
import requests

sys.path.insert(0, str(Path(__file__).resolve().parents[0]))
from test_kb_p0_content import _make_docx_bytes, request  # noqa: E402

GATEWAY = "http://localhost:8080"
TENANT_ID = "00000000-0000-0000-0000-000000000001"
HEADERS = {
    "X-Dev-Tenant-ID": TENANT_ID,
    "X-Dev-User-ID": "00000000-0000-0000-0000-000000000002",
    "Content-Type": "application/json",
}
PG_DSN = "postgresql://ani:ani_dev_password@10.10.1.66:30945/ani"

# 新格式：markdown 图片标记 ![alt](url)，url 应为完整 http(s)://...
IMG_MD_RE = re.compile(r"!\[([^\]]*)\]\(([^)]+)\)")
# 旧格式（不应再出现）：链接标记 [图片: ...](url)。(?<!!) 排除新格式 ![..] 的子串误报。
OLD_LINK_RE = re.compile(r"(?<!!)\[图片[^\]]*\]\(([^)]+)\)")


async def query_chunks(kb_id: str, doc_id: str):
    conn = await asyncpg.connect(PG_DSN)
    try:
        async with conn.transaction():
            await conn.execute("SELECT set_config('app.current_tenant_id', $1, true)", TENANT_ID)
            return await conn.fetch(
                "SELECT id, chunk_type, content_type, content FROM kb_chunks "
                "WHERE kb_id=$1 AND doc_id=$2 ORDER BY id",
                uuid.UUID(kb_id), uuid.UUID(doc_id),
            )
    finally:
        await conn.close()


def main() -> int:
    content_bytes = _make_docx_bytes()
    doc_name = "ani_img_inline_check.docx"

    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases", json={
        "idempotency_key": f"p0i_{uuid.uuid4()}",
        "name": f"p0i-inline-{int(time.time())}",
        "embedding_model": "Qwen3-Embedding-0.6B",
        "chunk_size": 512, "top_k": 5, "score_threshold": 0.3,
        "retrieval_mode": "hybrid",
    })
    kb_id = r.json().get("id")
    print("CreateKB kb_id =", kb_id)

    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents", json={
        "idempotency_key": f"p0i_up_{uuid.uuid4()}",
        "file_name": doc_name, "file_type": "docx",
        "file_size_bytes": len(content_bytes),
        "checksum_sha256": hashlib.sha256(content_bytes).hexdigest(),
    })
    body = r.json()
    doc_id = body.get("doc_id")
    up = requests.put(body.get("upload_url"), data=content_bytes, timeout=60)
    print("MinIO PUT status =", up.status_code)
    request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}/notify-uploaded",
            json={"storage_path": body.get("storage_path")})

    pstatus, cc = "pending", 0
    for _ in range(48):
        time.sleep(5)
        pr = requests.get(
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            headers=HEADERS, params={"limit": "20"}, timeout=30)
        for it in pr.json().get("items", []):
            if it.get("id") == doc_id:
                pstatus, cc = it.get("parse_status"), it.get("chunk_count") or 0
                break
        if pstatus in ("ready", "failed"):
            break
    print("解析 status =", pstatus, "chunk_count =", cc)
    if pstatus != "ready":
        print("[FAIL] 解析未 ready")
        return 1

    rows = asyncio.run(query_chunks(kb_id, doc_id))
    print(f"\n共 {len(rows)} 条 chunk")

    new_imgs, old_imgs = [], []
    for c in rows:
        content = c["content"] or ""
        for m in IMG_MD_RE.finditer(content):
            new_imgs.append((m.group(1), m.group(2)))
        for m in OLD_LINK_RE.finditer(content):
            old_imgs.append(m.group(1))

    print(f"\n图片内联统计: 新格式(![..]) = {len(new_imgs)} 条, 旧格式([图片..]) = {len(old_imgs)} 条")
    for alt, url in new_imgs[:10]:
        print(f"  ![alt={alt}]({url})")

    # 校验每一条新内联 URL 可无签名 GET 读取
    ok = True
    if new_imgs:
        for alt, url in new_imgs[:5]:
            try:
                g = requests.get(url, timeout=20)
                print(f"  GET({g.status_code}) type={g.headers.get('content-type')} len={len(g.content)} url={url[:120]}")
                ok = ok and g.status_code == 200 and g.headers.get("content-type", "").startswith("image/")
            except Exception as e:
                print("  GET error:", e)
                ok = False
    else:
        ok = False
        print("  [FAIL] 未发现新格式图片内联")

    # 校验格式正确性 + 可访问性
    fmt_ok = len(new_imgs) > 0 and len(old_imgs) == 0
    for _, url in new_imgs:
        if not url.startswith("http"):
            fmt_ok = False
            break
    print(f"\n[{'PASS' if fmt_ok else 'FAIL'}] 图片内联为新格式 ![alt](完整URL)，且无旧格式")
    print(f"[{'PASS' if ok else 'FAIL'}] 图片 URL 均可无签名 GET 显示")
    return 0 if (fmt_ok and ok) else 1


if __name__ == "__main__":
    sys.exit(main())
