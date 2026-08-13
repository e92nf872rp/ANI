"""Tests for the rag-engine client (issue-007 / US-009).

Verifies:
- RagEngineClient.query() calls the rag-engine Query endpoint with the
  correct path/body and returns the QueryResponse-shaped dict.
- Error mapping: non-2xx raises RagEngineError with status_code.

Uses httpx.MockTransport so no real rag-engine is required.
"""
import json
import os
import sys

import httpx
import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.rag_engine.client import RagEngineClient, RagEngineError


KB_ID = "22222222-2222-2222-2222-222222222222"
TENANT_ID = "11111111-1111-1111-1111-111111111111"


def _make_client(handler, base_url="http://rag.test"):
    transport = httpx.MockTransport(handler)
    http_client = httpx.AsyncClient(base_url=base_url, transport=transport)
    return RagEngineClient(base_url=base_url, client=http_client)


@pytest.mark.asyncio
async def test_query_sends_correct_request():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["method"] = req.method
        captured["path"] = req.url.path
        captured["json"] = json.loads(req.content)
        return httpx.Response(200, json={
            "answer": "hello",
            "sources": [
                {"doc_id": "d1", "file_name": "a.pdf", "page": 1,
                 "content": "ctx", "score": 0.9}
            ],
            "session_id": "sess-1",
            "input_tokens": 10,
            "output_tokens": 5,
        })

    async with _make_client(handler) as rag:
        resp = await rag.query(
            kb_id=KB_ID,
            tenant_id=TENANT_ID,
            question="hi",
            session_id="sess-1",
            top_k=5,
            score_threshold=0.3,
        )

    assert captured["method"] == "POST"
    assert captured["path"] == f"/api/v1/kb/{KB_ID}/query"
    assert captured["json"]["kb_id"] == KB_ID
    assert captured["json"]["question"] == "hi"
    assert captured["json"]["session_id"] == "sess-1"
    assert resp["answer"] == "hello"
    assert resp["sources"][0]["doc_id"] == "d1"
    assert resp["input_tokens"] == 10


@pytest.mark.asyncio
async def test_query_error_raises():
    def handler(req: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"detail": "rag-engine down"})

    async with _make_client(handler) as rag:
        with pytest.raises(RagEngineError) as exc:
            await rag.query(
                kb_id=KB_ID, tenant_id=TENANT_ID, question="hi"
            )
    assert exc.value.status_code == 503
    assert "rag-engine down" in str(exc.value)


@pytest.mark.asyncio
async def test_query_without_session():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["json"] = json.loads(req.content)
        return httpx.Response(200, json={
            "answer": "ok", "sources": [], "session_id": "new",
            "input_tokens": 0, "output_tokens": 0,
        })

    async with _make_client(handler) as rag:
        resp = await rag.query(
            kb_id=KB_ID, tenant_id=TENANT_ID, question="hi"
        )
    assert "session_id" not in captured["json"]
    assert resp["session_id"] == "new"
