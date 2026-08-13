"""Deep verify retrieval_mode via gateway: run each mode with threshold disabled.

Creates a fresh KB, uploads a doc, then queries with retrieval_mode in
{keyword, vector, hybrid} (request override) using score_threshold=-1 to
disable the no-result gate so we can see whether each retrieval branch
actually returns sources from the KB config path.
"""
import hashlib
import json
import time
import uuid

import requests

BASE = "http://localhost:8080/api/v1/svc"


def create_kb(retrieval_mode="hybrid", top_k=5, score_threshold=0.3):
    body = {
        "idempotency_key": f"dv_create_{uuid.uuid4()}",
        "name": f"dv-kb-{int(time.time())}",
        "description": "retrieval_mode deep verify",
        "embedding_model": "Qwen3-Embedding-0.6B",
        "chunk_size": 512,
        "top_k": top_k,
        "score_threshold": score_threshold,
        "retrieval_mode": retrieval_mode,
    }
    r = requests.post(f"{BASE}/knowledge-bases", json=body)
    assert r.status_code == 201, f"create_kb {r.status_code}: {r.text}"
    return r.json()["id"]


def upload_and_parse(kb_id):
    content = (
        "# 平台核心能力\n平台提供计算、存储、网络三大核心能力。\n"
        "# GPU 虚拟化\n支持 GPU 虚拟化与多云管理。\n"
        "# 检索方式\n知识库支持向量检索、全文检索与混合检索三种方式。"
    )
    checksum = hashlib.sha256(content.encode()).hexdigest()
    body = {
        "idempotency_key": f"dv_up_{uuid.uuid4()}",
        "file_name": "dv.md",
        "file_type": "md",
        "file_size_bytes": len(content.encode()),
        "checksum_sha256": checksum,
    }
    r = requests.post(f"{BASE}/knowledge-bases/{kb_id}/documents", json=body)
    assert r.status_code in (200, 201), f"upload {r.status_code}: {r.text}"
    doc = r.json()
    doc_id = doc["doc_id"]
    up = requests.put(doc["upload_url"], data=content.encode("utf-8"))
    assert up.status_code == 200, f"minio put {up.status_code}"
    notified = requests.post(
        f"{BASE}/knowledge-bases/{kb_id}/documents/{doc_id}/notify-uploaded",
        json={"storage_path": doc["storage_path"]},
    )
    assert notified.status_code in (200, 202), f"notify {notified.status_code}: {notified.text}"
    for _ in range(30):
        items = requests.get(f"{BASE}/knowledge-bases/{kb_id}/documents").json().get("items", [])
        d = next((x for x in items if x.get("id") == doc_id), None)
        if d and d.get("parse_status") == "ready":
            return
        time.sleep(1)
    raise RuntimeError("parse did not reach ready")


def query(kb_id, retrieval_mode, label=""):
    body = {
        "idempotency_key": f"dv_q_{uuid.uuid4()}",
        "question": "平台提供哪些核心能力？",
        "top_k": 3,
        "score_threshold": -1,  # disable threshold to observe raw retrieval
        "retrieval_mode": retrieval_mode,
    }
    r = requests.post(f"{BASE}/knowledge-bases/{kb_id}/query", json=body)
    assert r.status_code == 200, f"query {r.status_code}: {r.text}"
    data = r.json()
    srcs = data.get("sources", [])
    print(f"[Query {label}] mode={retrieval_mode} sources={len(srcs)} "
          f"answer={data.get('answer','')[:40]!r}")
    for s in srcs[:3]:
        print(f"    score={s['score']:.4f} content={s['content'][:40]!r}")
    return srcs


def main():
    kb_id = create_kb(retrieval_mode="vector", top_k=3, score_threshold=0.3)
    upload_and_parse(kb_id)
    for mode in ("keyword", "vector", "hybrid"):
        query(kb_id, mode, label=mode.upper())
    # cleanup
    for doc in requests.get(f"{BASE}/knowledge-bases/{kb_id}/documents").json().get("items", []):
        requests.delete(f"{BASE}/knowledge-bases/{kb_id}/documents/{doc['id']}")
    requests.delete(f"{BASE}/knowledge-bases/{kb_id}")
    print("RESULT: depth verify done")


if __name__ == "__main__":
    main()
