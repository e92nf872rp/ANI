"""Test all P0 knowledge base APIs — print full request inputs and responses.

P0 APIs (9 total):
  1. CreateKB            POST   /api/v1/svc/knowledge-bases
  2. ListKBs             GET    /api/v1/svc/knowledge-bases
  3. GetKB               GET    /api/v1/svc/knowledge-bases/:kb_id
  4. DeleteKB            DELETE /api/v1/svc/knowledge-bases/:kb_id
  5. ListDocuments       GET    /api/v1/svc/knowledge-bases/:kb_id/documents
  6. GetDocumentUploadURL POST   /api/v1/svc/knowledge-bases/:kb_id/documents
  7. DeleteDocument      DELETE /api/v1/svc/knowledge-bases/:kb_id/documents/:doc_id
  8. Query               POST   /api/v1/svc/knowledge-bases/:kb_id/query
  9. Query Stream (SSE)  GET    /api/v1/svc/knowledge-bases/:kb_id/query/stream

Usage:
  python scripts/test_kb_p0_apis.py
"""
import json
import sys
import time
import uuid

import requests

GATEWAY = "http://localhost:8080"

# Valid tenant UUID from the database (tenant-a)
TENANT_ID = "00000000-0000-0000-0000-000000000001"
USER_ID = "00000000-0000-0000-0000-000000000002"

HEADERS = {
    "X-Dev-Tenant-ID": TENANT_ID,
    "X-Dev-User-ID": USER_ID,
    "Content-Type": "application/json",
}

# ── Helpers ───────────────────────────────────────────────────────────────────

_passed = 0
_failed = 0


def truncate_vectors(obj, max_len=80):
    """Recursively truncate any 'embedding' or 'vector' field values in a
    JSON-like object to max_len characters, replacing with a short repr +
    '[truncated, len=N]' suffix."""
    if isinstance(obj, dict):
        result = {}
        for k, v in obj.items():
            if k in ("embedding", "vector", "embedding_vector") and isinstance(v, (list, str)):
                s = json.dumps(v) if isinstance(v, list) else v
                if len(s) > max_len:
                    result[k] = s[:max_len] + f"...[truncated, len={len(s)}]"
                else:
                    result[k] = v
            else:
                result[k] = truncate_vectors(v, max_len)
        return result
    elif isinstance(obj, list):
        return [truncate_vectors(item, max_len) for item in obj]
    return obj


def pretty_json(obj) -> str:
    """Pretty-print JSON with vector truncation."""
    truncated = truncate_vectors(obj)
    return json.dumps(truncated, ensure_ascii=False, indent=2)


def sep(title):
    print(f"\n{'='*70}")
    print(f"  {title}")
    print(f"{'='*70}")


def test_api(
    label: str,
    method: str,
    url: str,
    *,
    json_body: dict | None = None,
    params: dict | None = None,
    expected: int | None = None,
    stream: bool = False,
) -> tuple[int, str | dict]:
    """Send a request and print full input + response."""
    print(f"\n  ── Request ─────────────────────────────────────────────")
    print(f"  {method} {url}")
    if json_body:
        print(f"  Body:   {pretty_json(json_body)}")
    if params:
        print(f"  Params: {json.dumps(params, ensure_ascii=False)}")

    try:
        if stream:
            resp = requests.request(
                method, url, params=params, headers=HEADERS, stream=True, timeout=30
            )
            body_text = ""
            for line in resp.iter_lines(decode_unicode=True):
                if line:
                    body_text += line + "\n"
                    if len(body_text) > 2000:
                        body_text += "..."
                        break
            status = resp.status_code
            resp_json = None
            print(f"\n  ── Response ────────────────────────────────────────────")
            print(f"  Status: {status}")
            print(f"  Body (SSE stream):")
            print(f"  {body_text.strip()}")
            ok = (expected == status) if expected else (200 <= status < 400)
        else:
            resp = requests.request(
                method, url, json=json_body, params=params, headers=HEADERS, timeout=30
            )
            status = resp.status_code
            try:
                resp_json = resp.json()
                print(f"\n  ── Response ────────────────────────────────────────────")
                print(f"  Status: {status}")
                print(f"  Body:")
                print(pretty_json(resp_json))
            except Exception:
                resp_json = None
                print(f"\n  ── Response ────────────────────────────────────────────")
                print(f"  Status: {status}")
                print(f"  Body: {resp.text}")
            ok = (expected == status) if expected else (200 <= status < 400)
    except Exception as e:
        status = 0
        resp_json = None
        print(f"\n  ── Response ────────────────────────────────────────────")
        print(f"  ERROR: {e}")
        ok = False

    global _passed, _failed
    if ok:
        _passed += 1
        print(f"\n  [PASS]")
    else:
        _failed += 1
        exp_str = f" (expected {expected})" if expected else ""
        print(f"\n  [FAIL] status={status}{exp_str}")

    return status, resp_json


def main():
    sep("P0 Knowledge Base API Tests")
    print(f"  Gateway:   {GATEWAY}")
    print(f"  Tenant ID: {TENANT_ID}")
    print(f"  User ID:   {USER_ID}")

    # ── 1. ListKBs (before create) ────────────────────────────────────────────
    sep("1. ListKBs — 列出知识库")
    test_api(
        "ListKBs",
        "GET",
        f"{GATEWAY}/api/v1/svc/knowledge-bases",
        expected=200,
    )

    # ── 2. CreateKB ───────────────────────────────────────────────────────────
    sep("2. CreateKB — 创建知识库")
    kb_name = f"p0-test-kb-{int(time.time())}"
    create_body = {
        "idempotency_key": f"create_kb:{TENANT_ID}:{kb_name}",
        "name": kb_name,
        "description": "P0 API 测试知识库",
        "embedding_model": "bge-m3",
        "chunk_size": 512,
        "top_k": 5,
        "score_threshold": 0.3,
    }
    status, resp_json = test_api(
        "CreateKB",
        "POST",
        f"{GATEWAY}/api/v1/svc/knowledge-bases",
        json_body=create_body,
        expected=201,
    )

    kb_id = resp_json.get("id") if resp_json else None
    if kb_id:
        print(f"\n  >>> 提取 kb_id = {kb_id}")
    else:
        print(f"\n  >>> 无法提取 kb_id，后续依赖 KB 的测试将跳过")

    # ── 3. GetKB ──────────────────────────────────────────────────────────────
    sep("3. GetKB — 获取知识库详情")
    if kb_id:
        test_api(
            "GetKB",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}",
            expected=200,
        )
    else:
        print("  [SKIP] 无 kb_id")

    # ── 4. ListKBs (after create) ─────────────────────────────────────────────
    sep("4. ListKBs — 创建后再次列出")
    test_api(
        "ListKBs (after create)",
        "GET",
        f"{GATEWAY}/api/v1/svc/knowledge-bases",
        expected=200,
    )

    # ── 5. GetDocumentUploadURL ───────────────────────────────────────────────
    sep("5. GetDocumentUploadURL — 获取文档上传地址")
    doc_id = None
    if kb_id:
        upload_body = {
            "file_name": "p0-test-document.pdf",
            "file_type": "pdf",
            "file_size_bytes": 2048,
            "checksum_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
            "idempotency_key": f"upload-{uuid.uuid4()}",
        }
        status, resp_json = test_api(
            "GetDocumentUploadURL",
            "POST",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            json_body=upload_body,
            expected=200,
        )
        if resp_json:
            doc_id = resp_json.get("doc_id")
            if doc_id:
                print(f"\n  >>> 提取 doc_id = {doc_id}")
    else:
        print("  [SKIP] 无 kb_id")

    # ── 5b. NotifyDocumentUploaded ───────────────────────────────────────────
    sep("5b. NotifyDocumentUploaded — 通知文档已上传")
    storage_path = None
    if kb_id and doc_id:
        # Extract storage_path from the upload response
        if resp_json and "storage_path" in resp_json:
            storage_path = resp_json["storage_path"]
        notify_body = {
            "storage_path": storage_path or "",
        }
        test_api(
            "NotifyDocumentUploaded",
            "POST",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}/notify-uploaded",
            json_body=notify_body,
            expected=202,
        )
    else:
        print("  [SKIP] 无 kb_id 或 doc_id")

    # ── 5c. Trigger rag-engine parse (synchronous fallback) ───────────────────
    sep("5c. Rag-engine parse — 触发文档解析（同步）")
    if kb_id and doc_id and storage_path:
        parse_body = {
            "kb_id": kb_id,
            "doc_id": doc_id,
            "tenant_id": TENANT_ID,
            "storage_path": storage_path,
            "file_type": "pdf",
            "idempotency_key": f"parse-{uuid.uuid4()}",
        }
        test_api(
            "Parse Document (rag-engine sync)",
            "POST",
            f"http://localhost:8001/api/v1/kb/{kb_id}/documents/{doc_id}/parse",
            json_body=parse_body,
            expected=200,
        )
    else:
        print("  [SKIP] 无 kb_id/doc_id/storage_path")

    # ── 6. ListDocuments ─────────────────────────────────────────────────────
    sep("6. ListDocuments — 列出文档（解析后）")
    if kb_id:
        test_api(
            "ListDocuments",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            expected=200,
        )
    else:
        print("  [SKIP] 无 kb_id")

    # ── 7. ListDocuments (filter by parse_status) ────────────────────────────
    sep("7. ListDocuments — 按 parse_status=pending 过滤")
    if kb_id:
        test_api(
            "ListDocuments (filter pending)",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            params={"parse_status": "pending"},
            expected=200,
        )
    else:
        print("  [SKIP] 无 kb_id")

    # ── 8. Query ──────────────────────────────────────────────────────────────
    sep("8. Query — 知识库问答")
    if kb_id:
        query_body = {
            "question": "请介绍一下这个知识库的内容",
            "idempotency_key": f"query-{uuid.uuid4()}",
            "top_k": 5,
            "score_threshold": 0.3,
        }
        test_api(
            "Query KB",
            "POST",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query",
            json_body=query_body,
            expected=200,
        )
    else:
        print("  [SKIP] 无 kb_id")

    # ── 9. Query Stream (SSE) ─────────────────────────────────────────────────
    sep("9. Query Stream (SSE) — 流式问答")
    if kb_id:
        test_api(
            "SSE Query Stream",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query/stream",
            params={"question": "知识库里有什么文档？"},
            stream=True,
            expected=200,
        )
    else:
        print("  [SKIP] 无 kb_id")

    # ── 10. DeleteDocument ───────────────────────────────────────────────────
    sep("10. DeleteDocument — 删除文档")
    if kb_id and doc_id:
        test_api(
            "DeleteDocument",
            "DELETE",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}",
            expected=204,
        )
    else:
        print("  [SKIP] 无 kb_id 或 doc_id")

    # ── 11. ListDocuments (after delete) ─────────────────────────────────────
    sep("11. ListDocuments — 删除文档后再次列出")
    if kb_id:
        test_api(
            "ListDocuments (after delete)",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            expected=200,
        )
    else:
        print("  [SKIP] 无 kb_id")

    # ── 12. DeleteKB ──────────────────────────────────────────────────────────
    sep("12. DeleteKB — 删除知识库")
    if kb_id:
        test_api(
            "DeleteKB",
            "DELETE",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}",
            expected=204,
        )
    else:
        print("  [SKIP] 无 kb_id")

    # ── 13. GetKB (after delete) ──────────────────────────────────────────────
    sep("13. GetKB — 删除后查询（验证软删除）")
    if kb_id:
        test_api(
            "GetKB (after delete)",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}",
            expected=200,
        )
    else:
        print("  [SKIP] 无 kb_id")

    # ── Summary ───────────────────────────────────────────────────────────────
    sep("测试结果汇总")
    print(f"  通过: {_passed}")
    print(f"  失败: {_failed}")
    print()
    return 0 if _failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
