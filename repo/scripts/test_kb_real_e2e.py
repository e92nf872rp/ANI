"""Real-data end-to-end KB test (arch-compliant: Core API download).

Full flow, all through the Core API:
  1. CreateKB via gateway
  2. GetDocumentUploadURL via gateway → returns doc_id + storage_path + upload_url
  3. Upload REAL markdown content to MinIO via the returned upload_url
     (arch-compliant presigned PUT from Core object store)
  4. NotifyDocumentUploaded via gateway → outbox → NATS
  5. Wait for rag-engine parse_worker to download via Core API (UUID) and parse
  6. Verify parse_status == ready, chunk_count > 0, query returns content
"""
import io
import json
import sys
import time
import uuid

import requests

GATEWAY = "http://localhost:8080"

TENANT_ID = "00000000-0000-0000-0000-000000000001"
USER_ID = "00000000-0000-0000-0000-000000000002"

HEADERS = {
    "X-Dev-Tenant-ID": TENANT_ID,
    "X-Dev-User-ID": USER_ID,
    "Content-Type": "application/json",
}

_passed = 0
_failed = 0


def sep(t):
    print(f"\n{'='*70}\n  {t}\n{'='*70}")


def check(label, ok):
    global _passed, _failed
    if ok:
        _passed += 1
        print(f"  [PASS] {label}")
    else:
        _failed += 1
        print(f"  [FAIL] {label}")


def show(label, obj):
    print(f"\n  -- {label} --")
    print(json.dumps(obj, ensure_ascii=False, indent=2))


def main():
    sep("1. CreateKB")
    kb_name = f"real-kb-{int(time.time())}"
    r = requests.post(
        f"{GATEWAY}/api/v1/svc/knowledge-bases",
        headers=HEADERS,
        json={
            "idempotency_key": f"create_real_{uuid.uuid4()}",
            "name": kb_name,
            "description": "真实数据端到端测试知识库",
            "embedding_model": "Qwen3-Embedding-0.6B",
            "chunk_size": 512,
            "top_k": 5,
            "score_threshold": 0.3,
        },
        timeout=30,
    )
    show("CreateKB", r.json())
    check("CreateKB 201", r.status_code == 201)
    if r.status_code != 201:
        return 1
    kb_id = r.json()["id"]
    print(f"  >>> kb_id = {kb_id}")

    sep("2. GetDocumentUploadURL")
    file_name = "ani-platform-overview.md"
    r = requests.post(
        f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
        headers=HEADERS,
        json={
            "file_name": file_name,
            "file_type": "md",
            "file_size_bytes": 2048,
            "checksum_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
            "idempotency_key": f"upload_{uuid.uuid4()}",
        },
        timeout=30,
    )
    show("GetDocumentUploadURL", r.json())
    check("GetDocumentUploadURL 200", r.status_code == 200)
    if r.status_code != 200:
        return 1
    doc_id = r.json()["doc_id"]
    storage_path = r.json()["storage_path"]
    upload_url = r.json()["upload_url"]
    print(f"  >>> doc_id = {doc_id}")
    print(f"  >>> storage_path = {storage_path}")
    print(f"  >>> upload_url = {upload_url}")
    check("upload_url is real MinIO (not mock)", upload_url.startswith("http://10.10.1.66:30900"))

    sep("3. Upload real markdown via presigned PUT URL")
    content = """# ANI 平台简介

ANI 是一个面向算力集群的一体化研发与运营平台，专注 GPU 算力调度与高效管理。

## 核心功能

ANI 平台提供以下核心能力：
- 算力资源管理：统一纳管多机多卡的 GPU 集群。
- 作业调度：支持批量提交、排队与优先级调度。
- 数据管理：内置 MinIO 对象存储，支持 KB 知识库文档管理。
- 模型服务：通过 vLLM 提供 OpenAI 兼容的推理接口。

## 知识库模块

知识库模块支持文档上传、解析、分块和向量化检索。
用户可以通过 RAG 问答接口基于知识库内容快速获取答案。

## 技术架构

ANI 采用微服务架构，包含网关、认证服务、知识库服务、RAG 引擎等。
RAG 引擎使用 Milvus 向量数据库与 Qwen 系列嵌入模型进行混合检索。
"""
    data = content.encode("utf-8")
    r = requests.put(upload_url, data=data, headers={"Content-Type": "text/markdown"}, timeout=30)
    show("PUT upload_url", {"status_code": r.status_code, "body": r.text[:200]})
    check(f"presigned PUT upload succeeded ({r.status_code})", r.status_code == 200)

    sep("4. NotifyDocumentUploaded")
    r = requests.post(
        f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}/notify-uploaded",
        headers=HEADERS,
        json={"storage_path": storage_path},
        timeout=30,
    )
    show("NotifyDocumentUploaded", r.json())
    check("NotifyDocumentUploaded 202", r.status_code == 202)

    sep("5. Wait for parse pipeline (NATS async)")
    # Poll ListDocuments until parse_status != pending (timeout 120s)
    final_status = None
    chunk_count = 0
    for i in range(24):
        time.sleep(5)
        r = requests.get(
            f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/documents",
            headers=HEADERS, timeout=30,
        )
        if r.status_code != 200:
            continue
        items = r.json().get("items", [])
        if not items:
            continue
        doc = items[0]
        final_status = doc.get("parse_status")
        chunk_count = doc.get("chunk_count") or 0
        print(f"  [poll {i}] parse_status={final_status} chunk_count={chunk_count}")
        if final_status in ("ready", "failed"):
            break
    check(f"parse_status is terminal (got {final_status})", final_status in ("ready", "failed"))
    check(f"parse_status == ready (got {final_status})", final_status == "ready")
    check(f"chunk_count > 0 (got {chunk_count})", chunk_count > 0)

    sep("6. Query KB with real data")
    r = requests.post(
        f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query",
        headers=HEADERS,
        json={
            "question": "ANI 平台提供哪些核心能力？",
            "idempotency_key": f"query_{uuid.uuid4()}",
            "top_k": 5,
            "score_threshold": 0.3,
        },
        timeout=60,
    )
    show("Query", r.json())
    check("Query 200", r.status_code == 200)
    if r.status_code == 200:
        sources = r.json().get("sources", [])
        check(f"has sources (got {len(sources)})", len(sources) > 0)

    sep("7. Query stream (SSE)")
    r = requests.get(
        f"{GATEWAY}/api/v1/svc/knowledge-bases/{kb_id}/query/stream",
        headers=HEADERS,
        params={"question": "ANI 的架构是什么？"},
        stream=True,
        timeout=30,
    )
    check("SSE 200", r.status_code == 200)
    body = ""
    for line in r.iter_lines(decode_unicode=True):
        if line:
            body += line + "\n"
    print(f"  SSE body:\n  {body.strip()}")
    check("SSE has sources event", "event: sources" in body)

    sep("结果汇总")
    print(f"  通过: {_passed}")
    print(f"  失败: {_failed}")
    return 0 if _failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
