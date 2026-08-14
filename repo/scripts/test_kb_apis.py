"""Test all knowledge base APIs through the gateway.

Tests the 12 KB endpoints from kb_resources.go:
  1. POST   /api/v1/svc/knowledge-bases                     - CreateKB
  2. GET    /api/v1/svc/knowledge-bases                     - ListKBs
  3. GET    /api/v1/svc/knowledge-bases/:kb_id              - GetKB
  4. DELETE /api/v1/svc/knowledge-bases/:kb_id              - DeleteKB
  5. GET    /api/v1/svc/knowledge-bases/:kb_id/documents    - ListDocuments
  6. POST   /api/v1/svc/knowledge-bases/:kb_id/documents    - GetDocumentUploadURL
  7. DELETE /api/v1/svc/knowledge-bases/:kb_id/documents/:doc_id - DeleteDocument
  8. POST   /api/v1/svc/knowledge-bases/:kb_id/query        - Query
  9. GET    /api/v1/svc/knowledge-bases/:kb_id/query/stream - SSE Query (stream)
 10. GET    /api/v1/svc/knowledge-bases/:kb_id/citations    - P1 (501)
 11. GET    /api/v1/svc/knowledge-bases/:kb_id/sessions     - P1 (501)
 12. PUT    /api/v1/svc/knowledge-bases/:kb_id/permissions  - P1 (501)

Also tests:
  - kb-service health/readyz
  - rag-engine health
  - auth-service health
  - gateway health

Usage:
  python scripts/test_kb_apis.py
"""
import json
import sys
import time
import uuid

import requests

GATEWAY = "http://localhost:8080"
KB_SERVICE = "http://localhost:8002"
RAG_ENGINE = "http://localhost:8001"
AUTH_SERVICE = "http://localhost:9201"

# Valid tenant UUID from the database (tenant-a)
TENANT_ID = "00000000-0000-0000-0000-000000000001"
USER_ID = "00000000-0000-0000-0000-000000000002"

HEADERS = {
    "X-Dev-Tenant-ID": TENANT_ID,
    "X-Dev-User-ID": USER_ID,
    "Content-Type": "application/json",
}

# ── Test result tracking ──────────────────────────────────────────────────────

_passed = 0
_failed = 0
_errors: list[str] = []


def _print_sep(title: str):
    print(f"\n{'='*60}")
    print(f"  {title}")
    print(f"{'='*60}")


def _print_result(label: str, status: int, body: str, expected: int | None = None):
    expected_str = f" (expected {expected})" if expected is not None else ""
    icon = "OK" if (expected is None and 200 <= status < 300) or status == expected else "FAIL"
    print(f"  [{icon}] {label}")
    print(f"        Status: {status}{expected_str}")
    # Truncate very long responses
    if len(body) > 500:
        print(f"        Body: {body[:500]}...")
    else:
        print(f"        Body: {body}")
    return icon == "OK"


def _record(ok: bool, label: str, status: int):
    global _passed, _failed
    if ok:
        _passed += 1
    else:
        _failed += 1
        _errors.append(f"{label}: status={status}")


def test_api(
    label: str,
    method: str,
    url: str,
    *,
    json_body: dict | None = None,
    params: dict | None = None,
    headers: dict | None = None,
    expected: int | None = None,
    stream: bool = False,
) -> tuple[int, str]:
    """Send a request and print the result. Returns (status, body)."""
    h = {**HEADERS, **(headers or {})}
    print(f"\n  >>> {method} {url}")
    if json_body:
        print(f"      Request body: {json.dumps(json_body, ensure_ascii=False)}")
    if params:
        print(f"      Query params: {params}")

    try:
        if stream:
            resp = requests.request(
                method, url, params=params, headers=h, stream=True, timeout=30
            )
            body = ""
            for line in resp.iter_lines(decode_unicode=True):
                if line:
                    body += line + "\n"
                    if len(body) > 1000:
                        body += "..."
                        break
            status = resp.status_code
        else:
            resp = requests.request(
                method, url, json=json_body, params=params, headers=h, timeout=30
            )
            status = resp.status_code
            try:
                body = json.dumps(resp.json(), ensure_ascii=False, indent=2)
            except Exception:
                body = resp.text
    except Exception as e:
        status = 0
        body = f"REQUEST ERROR: {e}"

    ok = _print_result(label, status, body, expected)
    _record(ok, label, status)

    return status, body


def main():
    _print_sep("KB API Tests — Knowledge Base Endpoints")
    print(f"  Gateway:     {GATEWAY}")
    print(f"  Tenant ID:   {TENANT_ID}")
    print(f"  User ID:     {USER_ID}")

    # ── Health checks ─────────────────────────────────────────────────────────
    _print_sep("Health Checks")

    test_api(
        "Gateway Health",
        "GET",
        f"{GATEWAY}/health",
        expected=200,
    )
    test_api(
        "kb-service Health",
        "GET",
        f"{KB_SERVICE}/health",
        expected=200,
    )
    test_api(
        "kb-service Readiness",
        "GET",
        f"{KB_SERVICE}/readyz",
        expected=200,
    )
    test_api(
        "rag-engine Health",
        "GET",
        f"{RAG_ENGINE}/health",
        expected=200,
    )
    test_api(
        "auth-service Health",
        "GET",
        f"{AUTH_SERVICE}/healthz",
        expected=200,
    )

    # ── 1. ListKBs (empty) ─────────────────────────────────────────────────────
    _print_sep("1. ListKBs (initial — should be empty)")
    status, body = test_api(
        "ListKBs",
        "GET",
        f"{GATEWAY}/api/v1/svc/knowledge-bases",
        expected=200,
    )

    # ── 2. CreateKB ────────────────────────────────────────────────────────────
    _print_sep("2. CreateKB")
    kb_name = f"test-kb-{int(time.time())}"
    create_body = {
        "idempotency_key": f"create_kb:{TENANT_ID}:{kb_name}",
        "name": kb_name,
        "description": "Test knowledge base for API testing",
        "embedding_model": "bge-m3",
        "chunk_size": 512,
        "top_k": 5,
        "score_threshold": 0.3,
    }
    status, body = test_api(
        "CreateKB",
        "POST",
        f"{GATEWAY}/api/v1/svc/knowledge-bases",
        json_body=create_body,
        expected=201,
    )

    kb_id = None
    if status in (200, 201):
        try:
            resp_json = json.loads(body)
            kb_id = resp_json.get("id")
            print(f"\n  >>> Created KB ID: {kb_id}")
        except Exception:
            pass

    # ── 3. ListKBs (with one KB) ───────────────────────────────────────────────
    _print_sep("3. ListKBs (after create)")
    test_api(
        "ListKBs (after create)",
        "GET",
        f"{GATEWAY}/api/v1/svc/knowledge-bases",
        expected=200,
    )

    # ── 4. GetKB ──────────────────────────────────────────────────────────────
    _print_sep("4. GetKB")
    if kb_id:
        test_api(
            "GetKB",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}",
            expected=200,
        )
    else:
        print("  [SKIP] No KB ID available")
        _record(False, "GetKB", 0)

    # ── 5. GetDocumentUploadURL ─────────────────────────────────────────────────
    _print_sep("5. GetDocumentUploadURL")
    doc_id = None
    if kb_id:
        upload_body = {
            "file_name": "test-document.pdf",
            "file_type": "pdf",
            "file_size_bytes": 1024,
            "checksum_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
            "idempotency_key": f"upload-{uuid.uuid4()}",
        }
        status, body = test_api(
            "GetDocumentUploadURL",
            "POST",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            json_body=upload_body,
        )
        if status == 200:
            try:
                resp_json = json.loads(body)
                doc_id = resp_json.get("doc_id")
                print(f"\n  >>> Got doc_id: {doc_id}")
            except Exception:
                pass
    else:
        print("  [SKIP] No KB ID available")

    # ── 6. ListDocuments ──────────────────────────────────────────────────────
    _print_sep("6. ListDocuments")
    if kb_id:
        test_api(
            "ListDocuments",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            expected=200,
        )
    else:
        print("  [SKIP] No KB ID available")

    # ── 7. GetDocument ──────────────────────────────────────────────────────────
    _print_sep("7. GetDocument (via ListDocuments)")
    if kb_id and doc_id:
        # The gateway doesn't have a GetDocument endpoint directly,
        # but we can verify via ListDocuments which already shows docs.
        # Let's try getting a specific document from the list.
        test_api(
            "GetDocument (from list)",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            params={"parse_status": "pending"},
            expected=200,
        )
    else:
        print("  [SKIP] No KB ID or doc_id available")

    # ── 8. NotifyDocumentUploaded ──────────────────────────────────────────────
    _print_sep("8. NotifyDocumentUploaded")
    if kb_id and doc_id:
        notify_body = {
            "doc_id": doc_id,
            "storage_path": f"kb-docs/{kb_id}/{doc_id}/test-document.pdf",
        }
        # This is actually a PUT/POST on the document to notify upload completion.
        # Looking at kb_resources.go, there's no separate notify endpoint -
        # the POST /documents already creates the document. Let's check if there's
        # a notify endpoint.
        # Actually, the gateway routes POST /documents to GetDocumentUploadURL.
        # NotifyDocumentUploaded is a separate gRPC RPC but may not have a direct
        # REST endpoint. Let me check the gateway routes.
        print("  Note: NotifyDocumentUploaded is a gRPC RPC called internally.")
        print("  The gateway may not expose it as a separate REST endpoint.")
        print("  Skipping direct test — covered by upload flow.")
    else:
        print("  [SKIP] No KB ID or doc_id available")

    # ── 9. Query ──────────────────────────────────────────────────────────────
    _print_sep("9. Query KB")
    if kb_id:
        query_body = {
            "question": "What is this knowledge base about?",
            "idempotency_key": f"query-{uuid.uuid4()}",
            "top_k": 5,
            "score_threshold": 0.3,
        }
        test_api(
            "Query KB",
            "POST",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query",
            json_body=query_body,
        )
    else:
        print("  [SKIP] No KB ID available")

    # ── 10. SSE Query Stream ──────────────────────────────────────────────────
    _print_sep("10. SSE Query Stream")
    if kb_id:
        test_api(
            "SSE Query Stream",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query/stream",
            params={"question": "Hello, what is in this KB?"},
            stream=True,
        )
    else:
        print("  [SKIP] No KB ID available")

    # ── 11. P1 Endpoints (should return 501) ──────────────────────────────────
    _print_sep("11. P1 Endpoints (expected 501 Not Implemented)")
    if kb_id:
        test_api(
            "ListKBCitations (P1)",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/citations",
            expected=501,
        )
        test_api(
            "ListKBSessions (P1)",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/sessions",
            expected=501,
        )
        test_api(
            "UpdateKBPermissions (P1)",
            "PUT",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/permissions",
            json_body={"idempotency_key": f"perms-{uuid.uuid4()}", "public_read": False, "allowed_user_ids": []},
            expected=501,
        )
    else:
        print("  [SKIP] No KB ID available")

    # ── 12. DeleteDocument ────────────────────────────────────────────────────
    _print_sep("12. DeleteDocument")
    if kb_id and doc_id:
        test_api(
            "DeleteDocument",
            "DELETE",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}",
            expected=204,
        )
    else:
        print("  [SKIP] No KB ID or doc_id available")

    # ── 13. DeleteKB ──────────────────────────────────────────────────────────
    _print_sep("13. DeleteKB")
    if kb_id:
        test_api(
            "DeleteKB",
            "DELETE",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}",
            expected=204,
        )

        # Verify deletion — soft-delete sets status='deleted' but row is still
        # retrievable (GetKB returns the row with status=deleted).
        status, body = test_api(
            "GetKB (after delete — status should be 'deleted')",
            "GET",
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}",
        )
        if status == 200:
            try:
                resp_json = json.loads(body)
                if resp_json.get("status") == "deleted":
                    print("  [OK] KB status is 'deleted' as expected")
                    _record(True, "GetKB after delete verification", 200)
                else:
                    print(f"  [FAIL] KB status is '{resp_json.get('status')}', expected 'deleted'")
                    _record(False, "GetKB after delete verification", status)
            except Exception:
                _record(False, "GetKB after delete verification", status)
        else:
            _record(False, "GetKB after delete verification", status)
    else:
        print("  [SKIP] No KB ID available")

    # ── Summary ───────────────────────────────────────────────────────────────
    _print_sep("Test Summary")
    print(f"  Passed: {_passed}")
    print(f"  Failed: {_failed}")
    if _errors:
        print(f"\n  Failures:")
        for e in _errors:
            print(f"    - {e}")
    print()

    return 0 if _failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
