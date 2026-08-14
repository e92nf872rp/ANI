#!/usr/bin/env python3
"""P0 KB 接口失败/边界/负向测试（测试工程师视角）。

在 d 模式（ANI_AUTH_MODE=dev，X-Dev-* 头）下，覆盖 happy-path 之外的：
  - 必填参数缺失（name / idempotency_key / question / file_name / file_type）
  - 不存在的资源（GetKB / DeleteKB / DeleteDocument / Notify / Query 用不存在的 kb_id）
  - 非法 file_type 白名单
  - SSE 边界（无 question / 超长 question）
  - 幂等回放（gateway Idempotent-Replay 头）
  - P1 接口 501 降级
每个用例打印 REQUEST 与 RESPONSE(状态码 + body)。
"""
import json
import sys
import time
import uuid

import requests

GATEWAY = "http://localhost:8080"
TENANT_ID = "00000000-0000-0000-0000-000000000001"
USER_ID = "00000000-0000-0000-0000-000000000002"
H = {
    "X-Dev-Tenant-ID": TENANT_ID,
    "X-Dev-User-ID": USER_ID,
    "Content-Type": "application/json",
}

SUMMARY = []
JSON_SAFE = {"idempotency_key", "storage_path", "upload_url", "checksum_sha256"}  # 保留完整的关键回显字段


def _show(v: object, limit: int = 4000) -> str:
    """完整展示一个值；非安全字段截断到 limit，防止超长内容淹没检查。"""
    s = str(v)
    if len(s) <= limit:
        return s
    return s[:limit] + f"...(截断, 共{len(s)}字符)"


def _req_line(method: str, url: str, *, json_body=None, data=None, params=None):
    print(f"    REQUEST: {method} {url}")
    if params:
        print(f"      query_params = {json.dumps(params, ensure_ascii=False, default=str)}")
    if json_body is not None:
        # 完整显示普通字段；仅对超长字段 batch 截断展示
        safe = {}
        for k, v in json_body.items():
            safe[k] = (
                _show(v) if k in JSON_SAFE or len(str(v)) <= 120
                else _show(v, 120)
            )
        print(f"      body(JSON)   = {json.dumps(safe, ensure_ascii=False, default=str, indent=2)}")
    elif data is not None:
        print(f"      body(raw)    = {_show(data)}")


def resp_line(label, status, body, headers=None, extra_header=""):
    """打印完整响应；body 为 dict/markdown 时完整显示，大字段截断。"""
    print(f"    {label} RESPONSE(status={status}){extra_header}")
    if isinstance(body, dict):
        shown = {}
        for k, v in body.items():
            shown[k] = _show(v) if k in JSON_SAFE or len(str(v)) <= 400 else _show(v, 400)
        print("      body(JSON)  = " + json.dumps(shown, ensure_ascii=False, default=str, indent=2).replace("\n", "\n                   "))
    else:
        print(f"      body(raw)   = {_show(body)}")


def record(label, ok, detail=""):
    SUMMARY.append((label, ok, detail))
    print(f"  [{'PASS' if ok else 'FAIL'}] {label} {detail}")


def parse(r):
    try:
        return r.json()
    except Exception:
        return r.text


def request(method, url, *, json=None, data=None, params=None, headers=H, timeout=30):
    """发请求并打印该环节的完整 输入参数 + 响应参数。"""
    _req_line(method, url, json_body=json, data=data, params=params)
    r = requests.request(method, url, headers=headers, json=json, data=data,
                         params=params, timeout=timeout)
    resp_line("", r.status_code, parse(r))
    return r


def expect(label, r, want, detail="", **reqmeta):
    ok = r.status_code == want
    b = parse(r)
    # 已由 request() 打印，这里仅打印醒目结论行
    print(f"      => {'PASS' if ok else 'FAIL'} 期望 {want}, 实际 {r.status_code}")
    record(label, ok, f"status={r.status_code} (期望 {want}) {detail}")
    return r


def new_idem():
    return f"neg_{uuid.uuid4()}"


def create_ok_kb():
    body = {"idempotency_key": new_idem(), "name": f"neg-kb-{int(time.time())}",
            "embedding_model": "Qwen3-Embedding-0.6B", "chunk_size": 512,
            "top_k": 5, "score_threshold": 0.3, "retrieval_mode": "hybrid"}
    return request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases", json=body)


def cleanup(kb_id):
    return request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}")


def main():
    print("=" * 78)
    print("  P0 KB 失败/边界/负向测试")
    print("=" * 78)

    # ── 1. CreateKB 失败分支 ─────────────────────────────────────────────
    print("\n=== 1. CreateKB 失败/边界 ===")
    # 1.1 name 为空
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases",
                json={"idempotency_key": new_idem(), "name": ""})
    expect("CreateKB name 为空 → 400", r, 400)

    # 1.2 idempotency_key 为空
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases",
                json={"name": "neg-nokey"})
    expect("CreateKB idempotency_key 为空 → 400", r, 400)

    # 1.3 非 JSON body
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases", data="not json")
    expect("CreateKB 非法 JSON → 400", r, 400)

    # ── 2. GetKB / DeleteKB 不存在 ───────────────────────────────────────
    print("\n=== 2. 不存在的 KB ===")
    fake_kb = str(uuid.uuid4())
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{fake_kb}")
    expect("GetKB 不存在 → 404", r, 404)
    r = request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{fake_kb}")
    expect("DeleteKB 不存在 → 404", r, 404)

    # ── 3. 上传失败分支 ──────────────────────────────────────────────────
    print("\n=== 3. GetDocumentUploadURL 失败分支 ===")
    r = create_ok_kb()
    kb_id = r.json().get("id")
    print(f"      前置: CreateKB ok kb_id={kb_id}")

    # 3.1 idempotency_key 为空
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
                json={"file_name": "a.md", "file_type": "md"})
    expect("Upload idempotency_key 为空 → 400", r, 400)

    # 3.2 file_name / file_type 为空
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
                json={"idempotency_key": new_idem(), "file_name": "", "file_type": ""})
    expect("Upload file_name/file_type 为空 → 400", r, 400)

    # 3.3 非法 file_type
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
                json={"idempotency_key": new_idem(), "file_name": "a.exe", "file_type": "exe"})
    expect("Upload 非法 file_type(exe) → 400", r, 400)

    # 3.4 正常上传（用于后续幂等/删除用例）
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
                json={"idempotency_key": new_idem(), "file_name": "sample.md", "file_type": "md"})
    expect("Upload 正常 → 200", r, 200)
    doc_id = r.json().get("doc_id")
    print(f"      doc_id={doc_id}")

    # ── 4. Notify / Delete 不存在 ────────────────────────────────────────
    print("\n=== 4. Notify / DeleteDocument 不存在 ===")
    fake_doc = str(uuid.uuid4())
    r = request("POST",
                f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{fake_doc}/notify-uploaded",
                json={"storage_path": "kb-docs/x/y"})
    expect("Notify 不存在 doc → 404", r, 404)
    r = request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{fake_doc}")
    expect("DeleteDocument 不存在 → 404", r, 404)

    # ── 5. Query 失败分支 ────────────────────────────────────────────────
    print("\n=== 5. Query 失败分支 ===")
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query",
                json={"question": "x"})
    expect("Query idempotency_key 为空 → 400", r, 400)
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query",
                json={"idempotency_key": new_idem()})
    expect("Query question 为空 → 400", r, 400)
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{fake_kb}/query",
                json={"idempotency_key": new_idem(), "question": "x"})
    expect("Query 不存在的 kb → 404", r, 404)

    # ── 6. SSE 边界 ──────────────────────────────────────────────────────
    print("\n=== 6. Query/stream 边界 ===")
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query/stream",
                params={})
    expect("SSE 无 question → 400", r, 400)
    long_q = "字" * 2001
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query/stream",
                params={"question": long_q})
    expect("SSE question>2000 → 400", r, 400)
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{fake_kb}/query/stream",
                params={"question": "x"})
    print(f"      SSE 不存在的 kb → status={r.status_code}")
    record("SSE 不存在的 kb → 非200", r.status_code != 200, f"status={r.status_code}")

    # ── 7. 幂等回放 ──────────────────────────────────────────────────────
    print("\n=== 7. 幂等回放 (gateway Idempotent-Replay) ===")
    idem_key = new_idem()
    r1 = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases",
                 json={"idempotency_key": idem_key, "name": f"neg-idem-{int(time.time())}"})
    r2 = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases",
                 json={"idempotency_key": idem_key, "name": f"neg-idem-{int(time.time())}"})
    replay = r2.headers.get("Idempotent-Replay")
    print(f"      request2 headers Idempotent-Replay={replay} status1={r1.status_code} status2={r2.status_code}")
    record("CreateKB 幂等回放带 Idempotent-Replay 头", replay == "true",
           f"status2={r2.status_code} header={replay}")

    # ── 8. P1 接口 501 ───────────────────────────────────────────────────
    print("\n=== 8. P1 未实现 → 501 ===")
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/citations",
                params={"limit": "20"})
    expect("ListKBCitations(P1) → 501", r, 501)
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/sessions",
                params={"limit": "20"})
    expect("ListKBSessions(P1) → 501", r, 501)
    r = request("PUT", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/permissions",
                json={"idempotency_key": new_idem(), "public_read": True})
    expect("UpdateKBPermissions(P1) → 501", r, 501)

    # ── 9. 清理 ──────────────────────────────────────────────────────────
    print("\n=== 9. 清理 ===")
    if doc_id:
        d = request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}")
        record("清理 DeleteDocument", d.status_code == 204, f"status={d.status_code}")
    d = request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}")
    record("清理 DeleteKB", d.status_code == 204, f"status={d.status_code}")

    # ── 汇总 ─────────────────────────────────────────────────────────────
    print("\n" + "=" * 78)
    print("  结果汇总")
    print("=" * 78)
    passed = sum(1 for _, ok, _ in SUMMARY if ok)
    failed = len(SUMMARY) - passed
    print(f"  通过: {passed}")
    print(f"  失败: {failed}")
    for label, ok, detail in SUMMARY:
        print(f"    [{'PASS' if ok else 'FAIL'}] {label} {detail}")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
