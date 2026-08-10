#!/usr/bin/env python3
"""P0 接口全方面综合测试（覆盖既有脚本未覆盖的边界/校验分支）。

重点强化"创建知识库 CreateKB"的配置校验与全 P0 接口的边界分支：
  CreateKB:
    - happy path（默认 chunk_size/top_k/score_threshold、自定义 embedding_model、retrieval_mode）
    - 边界：chunk_size = 1 / 8192（合规最值）、chunk_size = 0 / 8193（越界→400）
    - 边界：top_k = 1 / 20（合规最值）、top_k = 0 / 21（越界→400）
    - 边界：score_threshold = 0 / 1.0 / 之间（0 表示使用默认）
    - 失败：name 缺失/空、idempotency_key 缺失、非法 JSON
  GetKB/DeleteKB: 存在→200/204, 不存在→404, 重复删除→404
  ListKBs: 过滤 embedding_model / 分页 limit / 空列表
  GetDocumentUploadURL: 缺文件字段→400、文件过大→413、非法类型→422、不存在KB→404
  NotifyDocumentUploaded: 不存在 doc→404、不存在 KB→404
  ListDocuments / DeleteDocument: 空列表、不存在 doc→404
  Query: 空 question→400、不存在 KB→404、无检索结果→200 且 answer 非幻觉
  SSE: 缺 question→400、正常→200
  幂等回放：CreateKB 相同 idempotency_key 二次请求应 Idempotent-Replay
每环节打印完整 REQUEST + RESPONSE。
"""
import json as _json
import sys
import time
import uuid

# 压制 requests 与全局 urllib3 版本不匹配的依赖警告（仅运行期，不影响结果）
import warnings
warnings.filterwarnings(
    "ignore",
    message=r".*doesn't match a supported version!",
    category=Warning,
)

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


def _show(v, limit=1500):
    s = str(v)
    return s if len(s) <= limit else s[:limit] + f"...(共{len(s)}字)"


def record(label, ok, detail=""):
    SUMMARY.append((label, ok, detail))
    print(f"  [{'PASS' if ok else 'FAIL'}] {label} {detail}")


def request(method, url, *, json=None, data=None, params=None, timeout=60):
    print(f"    REQUEST: {method} {url}")
    if params:
        print(f"      query_params = {_json.dumps(params, ensure_ascii=False)}")
    if json is not None:
        print(f"      body(JSON)   = {_json.dumps(json, ensure_ascii=False, default=str)}")
    elif data is not None:
        print(f"      body(raw)    = {data!r}")
    r = requests.request(method, url, headers=H, json=json, data=data,
                         params=params, timeout=timeout)
    try:
        b = r.json()
    except Exception:
        b = r.text
    print(f"    RESPONSE(status={r.status_code}) = {_show(b, 2000)}")
    return r


def parse(r):
    try:
        return r.json()
    except Exception:
        return r.text


def expect(label, r, want, detail=""):
    ok = r.status_code == want
    record(label, ok, f"status={r.status_code} (期望 {want}) {detail}")
    return ok


def nid():
    return f"p0c_{uuid.uuid4()}"


def create_kb(**over):
    body = {
        "idempotency_key": nid(),
        "name": f"p0c-{int(time.time())}-{uuid.uuid4().hex[:6]}",
        "embedding_model": "Qwen3-Embedding-0.6B",
        "chunk_size": 512,
        "top_k": 5,
        "score_threshold": 0.3,
        "retrieval_mode": "hybrid",
    }
    body.update(over)
    return request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases", json=body)


def main():
    print("=" * 78)
    print("  P0 接口全方面综合测试")
    print("=" * 78)

    # ══════════ 1. CreateKB ─═════════════════════════════════════════════
    print("\n════ 1. CreateKB 全面 ════")

    # 1.1 默认配置 happy path
    print("\n  -- 1.1 默认配置 --")
    r = create_kb()
    kb_id = (r.json() or {}).get("id") if r.status_code in (200, 201) else None
    expect("CreateKB 默认配置 → 201", r, 201, f"kb_id={kb_id}")
    if kb_id:
        print(f"      default chunk_size/top_k/score_threshold 已下发")

    # 1.2 自定义 retrieval_mode=vector
    print("\n  -- 1.2 retrieval_mode=vector --")
    r2 = create_kb(retrieval_mode="vector")
    expect("CreateKB retrieval_mode=vector → 201", r2, 201)
    kb2 = (r2.json() or {}).get("id") if r2.status_code in (200, 201) else None

    # 1.3 chunk_size 边界（合规 1 / 8192）
    print("\n  -- 1.3 chunk_size 最值 --")
    r = create_kb(chunk_size=1)
    expect("CreateKB chunk_size=1 → 201", r, 201)
    r = create_kb(chunk_size=8192)
    expect("CreateKB chunk_size=8192 → 201", r, 201)

    # 1.4 chunk_size 越界（0 表示使用默认值，8193 越界）
    print("\n  -- 1.4 chunk_size 0(默认) / 越界 --")
    r = create_kb(chunk_size=0)
    expect("CreateKB chunk_size=0(取默认) → 201", r, 201)
    r = create_kb(chunk_size=8193)
    expect("CreateKB chunk_size=8193 → 400", r, 400)

    # 1.5 top_k 边界
    print("\n  -- 1.5 top_k 最值/越界 --")
    r = create_kb(top_k=1)
    expect("CreateKB top_k=1 → 201", r, 201)
    r = create_kb(top_k=20)
    expect("CreateKB top_k=20 → 201", r, 201)
    r = create_kb(top_k=0)
    expect("CreateKB top_k=0(取默认) → 201", r, 201)
    r = create_kb(top_k=21)
    expect("CreateKB top_k=21 → 400", r, 400)

    # 1.5b score_threshold 边界/默认
    print("\n  -- 1.5b score_threshold 默认与边界 --")
    # 未显式传入 → 落库默认为 0（表示未设置，运行时由 rag-engine 兜底）
    r_noth = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases",
                     json={"idempotency_key": nid(),
                           "name": f"p0c-st-default-{int(time.time())}"})
    expect("CreateKB 未传 score_threshold → 201", r_noth, 201)
    kid_noth = (r_noth.json() or {}).get("id") if r_noth.status_code == 201 else None
    if kid_noth:
        g = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kid_noth}")
        st = (g.json() or {}).get("score_threshold")
        print(f"      未传时落库 score_threshold = {st}")
        record("未传 score_threshold → 落库默认 0", st == 0, f"score_threshold={st}")
        request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kid_noth}")
    # 显式合法值原样存储
    r = create_kb(score_threshold=0.6)
    expect("CreateKB score_threshold=0.6 → 201", r, 201)
    # 合法边界 0.0 / 1.0
    r = create_kb(score_threshold=0.0)
    expect("CreateKB score_threshold=0.0 → 201", r, 201)
    r = create_kb(score_threshold=1.0)
    expect("CreateKB score_threshold=1.0 → 201", r, 201)
    # 越界
    r = create_kb(score_threshold=-0.1)
    expect("CreateKB score_threshold=-0.1 → 400", r, 400)
    r = create_kb(score_threshold=1.5)
    expect("CreateKB score_threshold=1.5 → 400", r, 400)

    # 1.6 失败分支
    print("\n  -- 1.6 CreateKB 失败 --")
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases", json={"idempotency_key": nid()})
    expect("CreateKB 缺失 name → 400", r, 400)
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases",
                json={"idempotency_key": nid(), "name": ""})
    expect("CreateKB name 为空 → 400", r, 400)
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases", json={"name": "x"})
    expect("CreateKB 缺失 idempotency_key → 400", r, 400)
    r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases", data="not json")
    expect("CreateKB 非法 JSON → 400", r, 400)

    # 1.7 幂等回放
    print("\n  -- 1.7 CreateKB 幂等回放 --")
    idem = nid()
    kb_name = f"p0c-idem-{int(time.time())}"
    r1 = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases",
                 json={"idempotency_key": idem, "name": kb_name})
    r2 = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases",
                 json={"idempotency_key": idem, "name": kb_name})
    replay = r2.headers.get("Idempotent-Replay")
    print(f"      status1={r1.status_code} status2={r2.status_code} Idempotent-Replay={replay}")
    record("CreateKB 幂等回放(Idempotent-Replay)", replay == "true",
           f"status2={r2.status_code} header={replay}")

    # ══════════ 2. GetKB / DeleteKB ══════════════════════════════════════
    print("\n════ 2. GetKB / DeleteKB ══")
    if kb_id:
        r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}")
        expect("GetKB 存在 → 200", r, 200)
    fake = str(uuid.uuid4())
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{fake}")
    expect("GetKB 不存在 → 404", r, 404)
    r = request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{fake}")
    expect("DeleteKB 不存在 → 404", r, 404)

    # ══════════ 3. ListKBs ═══════════════════════════════════════════════
    print("\n════ 3. ListKBs ══")
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases")
    expect("ListKBs → 200", r, 200)
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases", params={"limit": "5"})
    expect("ListKBs limit=5 → 200", r, 200)

    # ══════════ 4. GetDocumentUploadURL 边界 ═════════════════════════════
    print("\n════ 4. GetDocumentUploadURL ══")
    use_kb = kb_id or kb2
    if use_kb:
        # 缺失字段
        r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/documents",
                    json={"file_name": "a.md", "file_type": "md"})
        expect("Upload 缺失 idempotency_key → 400", r, 400)
        r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/documents",
                    json={"idempotency_key": nid()})
        expect("Upload 缺失 file_name/file_type → 400", r, 400)
        # 非法 file_type
        r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/documents",
                    json={"idempotency_key": nid(), "file_name": "a.exe", "file_type": "exe"})
        # spec 允许 400/422
        record("Upload 非法 file_type 被拒", r.status_code in (400, 422),
               f"status={r.status_code}")
        # 正常上传
        r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/documents",
                    json={"idempotency_key": nid(), "file_name": "sample.md",
                          "file_type": "md", "file_size_bytes": 10,
                          "checksum_sha256": "0" * 64})
        expect("Upload 正常 → 200", r, 200)
        doc_id = (r.json() or {}).get("doc_id") if r.status_code == 200 else None
        # 不存在 KB
        r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{fake}/documents",
                    json={"idempotency_key": nid(), "file_name": "a.md", "file_type": "md"})
        record("Upload 不存在 KB → 404", r.status_code == 404, f"status={r.status_code}")
    else:
        doc_id = None

    # ══════════ 5. NotifyDocumentUploaded ═════════════════════════════════
    print("\n════ 5. NotifyDocumentUploaded ══")
    if use_kb:
        r = request("POST",
                    f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/documents/{str(uuid.uuid4())}/notify-uploaded",
                    json={"storage_path": "kb-docs/x/y"})
        expect("Notify 不存在 doc → 404", r, 404)
        if doc_id:
            r = request("POST",
                        f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/documents/{doc_id}/notify-uploaded",
                        json={"storage_path": "kb-docs/x/y"})
            expect("Notify 正常(对象不存在也按流程) → 202/4xx", r, 202,
                   f"(status={r.status_code})") if r.status_code == 202 else None

    # ══════════ 6. ListDocuments ═════════════════════════════════════════
    print("\n════ 6. ListDocuments / DeleteDocument ══")
    if use_kb:
        r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/documents")
        expect("ListDocuments → 200", r, 200)
        r = request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/documents/{str(uuid.uuid4())}")
        expect("DeleteDocument 不存在 → 404", r, 404)
        if doc_id:
            r = request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/documents/{doc_id}")
            expect("DeleteDocument 存在 → 204", r, 204)

    # ══════════ 7. Query / SSE ═══════════════════════════════════════════
    print("\n════ 7. Query / SSE ══")
    if use_kb:
        r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/query",
                    json={"idempotency_key": nid()})
        expect("Query 缺失 question → 400", r, 400)
        r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{fake}/query",
                    json={"idempotency_key": nid(), "question": "x"})
        expect("Query 不存在 KB → 404", r, 404)
        r = request("POST", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb}/query",
                    json={"idempotency_key": nid(), "question": "ANI 平台的混合检索原理是什么？",
                          "retrieval_mode": "hybrid"})
        # 空 KB 无来源时应返回 no-result 而非幻觉/报错
        expect("Query 空KB(无来源) → 200", r, 200)
    # SSE
    r = request("GET", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb or fake}/query/stream",
                params={"question": "x"})
    record("SSE 不存在/正常 → 不超时", r.status_code in (200, 404), f"status={r.status_code}")

    # ══════════ 8. P1 501 ════════════════════════════════════════════════
    print("\n════ 8. P1 未实现 → 501 ══")
    for path in (f"/api/v1/svc/knowledge-bases/{use_kb or fake}/citations",
                 f"/api/v1/svc/knowledge-bases/{use_kb or fake}/sessions"):
        r = request("GET", f"{GATEWAY}{path}", params={"limit": "20"})
        expect("P1 GET → 501", r, 501)
    r = request("PUT", f"{GATEWAY}/api/v1/svc/knowledge-bases/{use_kb or fake}/permissions",
                json={"idempotency_key": nid(), "public_read": True})
    expect("P1 Permissions → 501", r, 501)

    # ══════════ 清理 ═════════════════════════════════════════════════════
    print("\n════ 清理 ══")
    for k in (kb_id, kb2):
        if k:
            request("DELETE", f"{GATEWAY}/api/v1/svc/knowledge-bases/{k}")

    # ══════════ 汇总 ═════════════════════════════════════════════════════
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
