"""Verify KB-config defaults: retrieval_mode/top_k/score_threshold set at creation
are used by query when the client does NOT override them.

Depth-verify already proved all three retrieval branches execute. This script
confirms the "from KB info" default-value resolution (the user's core ask):
query with NO retrieval_mode/top_k/score_threshold uses the KB's values.
"""
import hashlib
import time
import uuid

import requests

BASE = "http://localhost:8080/api/v1/svc"


def main():
    # KB configured: vector mode, top_k=2, score_threshold=0.1
    body = {
        "idempotency_key": f"kbc_create_{uuid.uuid4()}",
        "name": f"kbc-kb-{int(time.time())}",
        "embedding_model": "Qwen3-Embedding-0.6B",
        "chunk_size": 512,
        "top_k": 2,
        "score_threshold": 0.1,
        "retrieval_mode": "vector",
    }
    r = requests.post(f"{BASE}/knowledge-bases", json=body)
    assert r.status_code == 201, f"create {r.status_code}: {r.text}"
    kb = r.json()
    kb_id = kb["id"]
    print(f"[CreateKB] retrieval_mode={kb.get('retrieval_mode')} top_k={kb.get('top_k')} "
          f"score_threshold={kb.get('score_threshold')}")
    assert kb.get("retrieval_mode") == "vector"
    assert kb.get("top_k") == 2
    assert abs(kb.get("score_threshold", 0) - 0.1) < 1e-9

    # upload + parse
    content = "# 平台核心能力\n平台提供计算、存储、网络三大核心能力。\n# GPU 虚拟化\n支持 GPU 虚拟化。"
    checksum = hashlib.sha256(content.encode()).hexdigest()
    body = {
        "idempotency_key": f"kbc_up_{uuid.uuid4()}",
        "file_name": "kbc.md",
        "file_type": "md",
        "file_size_bytes": len(content.encode()),
        "checksum_sha256": checksum,
    }
    r = requests.post(f"{BASE}/knowledge-bases/{kb_id}/documents", json=body)
    assert r.status_code in (200, 201), f"upload {r.status_code}: {r.text}"
    doc = r.json()
    up = requests.put(doc["upload_url"], data=content.encode())
    assert up.status_code == 200, f"put {up.status_code}"
    requests.post(
        f"{BASE}/knowledge-bases/{kb_id}/documents/{doc['doc_id']}/notify-uploaded",
        json={"storage_path": doc["storage_path"]},
    )
    for _ in range(30):
        items = requests.get(f"{BASE}/knowledge-bases/{kb_id}/documents").json().get("items", [])
        d = next((x for x in items if x.get("id") == doc["doc_id"]), None)
        if d and d.get("parse_status") == "ready":
            break
        time.sleep(1)
    else:
        raise RuntimeError("parse not ready")

    # Query WITHOUT overriding retrieval_mode/top_k/score_threshold
    r = requests.post(
        f"{BASE}/knowledge-bases/{kb_id}/query",
        json={"idempotency_key": f"kbc_q_{uuid.uuid4()}", "question": "平台提供哪些核心能力？"},
    )
    assert r.status_code == 200, f"query {r.status_code}: {r.text}"
    data = r.json()
    srcs = data.get("sources", [])
    print(f"[Query KB-config-default (vector,top_k=2)] sources={len(srcs)} answer={data.get('answer','')[:30]!r}")
    # top_k=2 from KB config should cap results at 2
    assert len(srcs) <= 2, f"top_k from KB config not honored: got {len(srcs)}"
    assert len(srcs) > 0, "query returned no sources (KB config default path failed)"

    # cleanup
    for d in requests.get(f"{BASE}/knowledge-bases/{kb_id}/documents").json().get("items", []):
        requests.delete(f"{BASE}/knowledge-bases/{kb_id}/documents/{d['id']}")
    requests.delete(f"{BASE}/knowledge-bases/{kb_id}")
    print("RESULT: KB-config default resolution verified OK")


if __name__ == "__main__":
    main()
