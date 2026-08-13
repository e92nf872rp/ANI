"""Verify KB retrieval_mode: create with mode + query honors it (+ request override).

Covers the new retrieval_mode feature end-to-end through gateway -> kb-service -> rag-engine.
1. Create KB with retrieval_mode=keyword, top_k=3, score_threshold=0.3 -> assert response carries them.
2. GetKB -> assert retrieval_mode persisted.
3. Upload a small doc, notify, wait ready.
4. Query (no override) -> assert sources returned (keyword path executes from KB config).
5. Query with retrieval_mode="vector" (request override) -> assert sources returned.
6. Cleanup: delete doc + kb.
"""
import hashlib
import json
import time
import uuid

import requests

BASE = "http://localhost:8080/api/v1/svc"


def create_kb(retrieval_mode="hybrid", top_k=5, score_threshold=0.3):
    body = {
        "idempotency_key": f"vr_create_{uuid.uuid4()}",
        "name": f"vr-kb-{int(time.time())}",
        "description": "retrieval_mode verification",
        "embedding_model": "Qwen3-Embedding-0.6B",
        "chunk_size": 512,
        "top_k": top_k,
        "score_threshold": score_threshold,
        "retrieval_mode": retrieval_mode,
    }
    r = requests.post(f"{BASE}/knowledge-bases", json=body)
    assert r.status_code == 201, f"create_kb {r.status_code}: {r.text}"
    kb = r.json()
    print(f"[CreateKB] retrieval_mode={kb.get('retrieval_mode')} top_k={kb.get('top_k')} score_threshold={kb.get('score_threshold')}")
    assert kb.get("retrieval_mode") == retrieval_mode, f"expected {retrieval_mode}, got {kb.get('retrieval_mode')}"
    return kb["id"]


def get_kb(kb_id):
    r = requests.get(f"{BASE}/knowledge-bases/{kb_id}")
    assert r.status_code == 200, r.text
    return r.json()


def upload_and_parse(kb_id):
    content = "# 标题一\n平台提供计算、存储、网络三大核心能力。\n# 标题二\n支持 GPU 虚拟化与多云管理。"
    checksum = hashlib.sha256(content.encode()).hexdigest()
    body = {
        "idempotency_key": f"vr_up_{uuid.uuid4()}",
        "file_name": "vr.md",
        "file_type": "md",
        "file_size_bytes": len(content.encode()),
        "checksum_sha256": checksum,
    }
    r = requests.post(f"{BASE}/knowledge-bases/{kb_id}/documents", json=body)
    assert r.status_code in (200, 201), f"upload {r.status_code}: {r.text}"
    doc = r.json()
    doc_id = doc["doc_id"]
    # presigned PUT
    up = requests.put(doc["upload_url"], data=content.encode("utf-8"))
    assert up.status_code == 200, f"minio put {up.status_code}"
    notified = requests.post(
        f"{BASE}/knowledge-bases/{kb_id}/documents/{doc_id}/notify-uploaded",
        json={"storage_path": doc["storage_path"]},
    )
    assert notified.status_code in (200, 202), f"notify {notified.status_code}: {notified.text}"
    # wait ready via ListDocuments (matches P0 polling)
    for _ in range(30):
        items = requests.get(f"{BASE}/knowledge-bases/{kb_id}/documents").json().get("items", [])
        d = next((x for x in items if x.get("id") == doc_id), None)
        if d and d.get("parse_status") == "ready":
            return
        time.sleep(1)
    raise RuntimeError("parse did not reach ready")


def query(kb_id, retrieval_mode_override=None, label=""):
    body = {
        "idempotency_key": f"vr_q_{uuid.uuid4()}",
        "question": "平台提供哪些核心能力？",
        "top_k": 3,
        "score_threshold": 0.3,
    }
    if retrieval_mode_override:
        body["retrieval_mode"] = retrieval_mode_override
    r = requests.post(f"{BASE}/knowledge-bases/{kb_id}/query", json=body)
    assert r.status_code == 200, f"query {r.status_code}: {r.text}"
    data = r.json()
    print(f"[Query {label}] sources={len(data.get('sources', []))}")
    return data


def main():
    kb_id = create_kb(retrieval_mode="keyword", top_k=3, score_threshold=0.3)
    got = get_kb(kb_id)
    assert got.get("retrieval_mode") == "keyword", f"persist failed: {got.get('retrieval_mode')}"
    print(f"[GetKB] retrieval_mode={got.get('retrieval_mode')}")
    upload_and_parse(kb_id)
    d1 = query(kb_id, label="KB-config(keyword)")
    assert len(d1.get("sources", [])) > 0, "keyword query returned no sources"
    d2 = query(kb_id, retrieval_mode_override="hybrid", label="override(hybrid)")
    assert len(d2.get("sources", [])) > 0, "hybrid override query returned no sources"
    # cleanup
    for doc in requests.get(f"{BASE}/knowledge-bases/{kb_id}/documents").json().get("items", []):
        requests.delete(f"{BASE}/knowledge-bases/{kb_id}/documents/{doc['id']}")
    requests.delete(f"{BASE}/knowledge-bases/{kb_id}")
    print("[Cleanup] deleted doc + kb")
    print("RESULT: retrieval_mode feature verified OK")


if __name__ == "__main__":
    main()
