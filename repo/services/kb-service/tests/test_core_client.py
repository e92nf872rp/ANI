"""Tests for the Core OpenAPI client (issue-007 / US-009).

Verifies:
- CoreClient calls the correct Core OpenAPI endpoints with correct method/path/body.
- Error mapping: non-2xx responses raise CoreAPIError with status_code + code.
- vector-stores collection CRUD, documents delete, objects upload/download.

Uses httpx.MockTransport so no real network is required.
"""
import json
import os
import sys

import httpx
import pytest

# Make the kb-service package and generated stubs importable.
_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.core_api.client import CoreAPIError, CoreClient


TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
VECTOR_STORE_ID = "kb_2222222222222222222222222222"


def _ok(body: dict) -> httpx.Response:
    return httpx.Response(200, json=body)


def _created(body: dict) -> httpx.Response:
    return httpx.Response(201, json=body)


def _make_client(handler, base_url="http://gateway.test/api/v1"):
    transport = httpx.MockTransport(handler)
    http_client = httpx.AsyncClient(base_url=base_url, transport=transport)
    return CoreClient(
        base_url=base_url,
        tenant_id=TENANT_ID,
        client=http_client,
    )


# ── vector-stores collection CRUD ─────────────────────────────────────────────


@pytest.mark.asyncio
async def test_create_vector_store_posts_correct_request():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["method"] = req.method
        captured["path"] = req.url.path
        captured["json"] = json.loads(req.content)
        return _created({"id": VECTOR_STORE_ID, "name": "kb_test", "dimension": 1024})

    async with _make_client(handler) as core:
        resp = await core.create_vector_store(
            name=VECTOR_STORE_ID,
            dimension=1024,
            metric="cosine",
            embedding_model="bge-m3",
            idempotency_key="key-1",
        )

    assert captured["method"] == "POST"
    assert captured["path"] == "/api/v1/vector-stores"
    assert captured["json"]["name"] == VECTOR_STORE_ID
    assert captured["json"]["dimension"] == 1024
    assert captured["json"]["idempotency_key"] == "key-1"
    assert resp["id"] == VECTOR_STORE_ID


@pytest.mark.asyncio
async def test_get_vector_store():
    def handler(req: httpx.Request) -> httpx.Response:
        assert req.method == "GET"
        assert req.url.path == f"/api/v1/vector-stores/{VECTOR_STORE_ID}"
        return _ok({"id": VECTOR_STORE_ID, "name": "kb_test"})

    async with _make_client(handler) as core:
        resp = await core.get_vector_store(vector_store_id=VECTOR_STORE_ID)
    assert resp["id"] == VECTOR_STORE_ID


@pytest.mark.asyncio
async def test_list_vector_stores():
    def handler(req: httpx.Request) -> httpx.Response:
        assert req.method == "GET"
        assert req.url.path == "/api/v1/vector-stores"
        assert req.url.params["limit"] == "10"
        return _ok({"items": [], "total": 0})

    async with _make_client(handler) as core:
        resp = await core.list_vector_stores(limit=10)
    assert resp["total"] == 0


@pytest.mark.asyncio
async def test_delete_vector_store():
    def handler(req: httpx.Request) -> httpx.Response:
        assert req.method == "DELETE"
        assert req.url.path == f"/api/v1/vector-stores/{VECTOR_STORE_ID}"
        return _ok({"id": VECTOR_STORE_ID})

    async with _make_client(handler) as core:
        resp = await core.delete_vector_store(vector_store_id=VECTOR_STORE_ID)
    assert resp["id"] == VECTOR_STORE_ID


@pytest.mark.asyncio
async def test_delete_vector_store_documents_sends_filter():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["method"] = req.method
        captured["path"] = req.url.path
        captured["filter"] = req.url.params.get("filter")
        return _ok({"deleted_count": 5})

    async with _make_client(handler) as core:
        resp = await core.delete_vector_store_documents(
            vector_store_id=VECTOR_STORE_ID, filter_expr='doc_id == "abc"'
        )
    assert captured["method"] == "DELETE"
    assert captured["path"] == f"/api/v1/vector-stores/{VECTOR_STORE_ID}/documents"
    assert captured["filter"] == 'doc_id == "abc"'
    assert resp["deleted_count"] == 5


# ── objects upload/download ───────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_request_upload_url():
    def handler(req: httpx.Request) -> httpx.Response:
        assert req.method == "POST"
        assert req.url.path == "/api/v1/objects/upload"
        body = json.loads(req.content)
        assert body["bucket_id"] == "kb-docs"
        assert body["key"] == "kb-docs/kb-1/doc-1/a.pdf"
        return _ok({"upload_url": "http://minio.test/put", "object_id": "obj-1"})

    async with _make_client(handler) as core:
        resp = await core.request_upload_url(
            bucket_id="kb-docs", key="kb-docs/kb-1/doc-1/a.pdf", idempotency_key="k1"
        )
    assert resp["upload_url"] == "http://minio.test/put"
    assert resp["object_id"] == "obj-1"


@pytest.mark.asyncio
async def test_request_download_url():
    def handler(req: httpx.Request) -> httpx.Response:
        assert req.method == "GET"
        assert req.url.path == "/api/v1/objects/obj-1/download"
        assert req.url.params["expires_seconds"] == "3600"
        return _ok({"download_url": "http://minio.test/get"})

    async with _make_client(handler) as core:
        resp = await core.request_download_url(object_id="obj-1", expires_seconds=3600)
    assert resp["download_url"] == "http://minio.test/get"


@pytest.mark.asyncio
async def test_head_object():
    def handler(req: httpx.Request) -> httpx.Response:
        assert req.method == "GET"
        assert req.url.path == "/api/v1/objects/obj-1"
        return _ok({"id": "obj-1", "size_bytes": 1024})

    async with _make_client(handler) as core:
        resp = await core.head_object(object_id="obj-1")
    assert resp["id"] == "obj-1"


def _accepted(body: dict) -> httpx.Response:
    return httpx.Response(202, json=body)


# ── vector-stores documents insert/search + object upload (issue-030 §3.3) ───


@pytest.mark.asyncio
async def test_insert_vector_documents_posts_with_vector_and_accepts_202():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["method"] = req.method
        captured["path"] = req.url.path
        body = json.loads(req.content)
        captured["idem"] = body["idempotency_key"]
        captured["docs"] = body["documents"]
        # Core API contract: 202 Accepted (async insert).
        return _accepted({"inserted_count": 2, "task_id": "task-1", "status": "pending"})

    async with _make_client(handler) as core:
        resp = await core.insert_vector_documents(
            vector_store_id=VECTOR_STORE_ID,
            documents=[
                {"id": "d1", "content": "hello", "vector": [0.1, 0.2], "metadata": {"chunk_id": "c1"}},
                {"id": "d2", "content": "world", "vector": [0.3, 0.4], "metadata": {}},
            ],
            idempotency_key="ins-1",
        )
    assert captured["method"] == "POST"
    assert captured["path"] == f"/api/v1/vector-stores/{VECTOR_STORE_ID}/documents"
    assert captured["idem"] == "ins-1"
    assert len(captured["docs"]) == 2
    assert captured["docs"][0]["vector"] == [0.1, 0.2]
    assert resp["inserted_count"] == 2
    assert resp["task_id"] == "task-1"


@pytest.mark.asyncio
async def test_insert_vector_documents_rejects_200_as_unexpected():
    """Core contract is 202; a 200 would be a contract drift → error."""
    def handler(req: httpx.Request) -> httpx.Response:
        return _ok({"inserted_count": 1})

    async with _make_client(handler) as core:
        with pytest.raises(CoreAPIError) as exc:
            await core.insert_vector_documents(
                vector_store_id=VECTOR_STORE_ID,
                documents=[{"id": "d1", "content": "x", "vector": [0.1]}],
                idempotency_key="k",
            )
    assert exc.value.status_code == 200


@pytest.mark.asyncio
async def test_search_vector_store_returns_items_with_content():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["method"] = req.method
        captured["path"] = req.url.path
        body = json.loads(req.content)
        captured["vector"] = body["vector"]
        captured["top_k"] = body["top_k"]
        return _ok({
            "items": [
                {"id": "d1", "score": 0.95, "content": "chunk text", "metadata": {"chunk_id": "c1"}},
                {"id": "d2", "score": 0.80, "content": "another", "metadata": {}},
            ],
            "total": 2,
        })

    async with _make_client(handler) as core:
        items = await core.search_vector_store(
            vector_store_id=VECTOR_STORE_ID, vector=[0.1, 0.2, 0.3], top_k=5
        )
    assert captured["method"] == "POST"
    assert captured["path"] == f"/api/v1/vector-stores/{VECTOR_STORE_ID}/search"
    assert captured["vector"] == [0.1, 0.2, 0.3]
    assert captured["top_k"] == 5
    assert len(items) == 2
    assert items[0]["content"] == "chunk text"
    assert items[0]["score"] == 0.95


@pytest.mark.asyncio
async def test_search_vector_store_sends_filter_when_provided():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        body = json.loads(req.content)
        captured["filter"] = body.get("filter")
        return _ok({"items": [], "total": 0})

    async with _make_client(handler) as core:
        await core.search_vector_store(
            vector_store_id=VECTOR_STORE_ID, vector=[0.1],
            filter_expr='doc_id == "abc"',
        )
    assert captured["filter"] == 'doc_id == "abc"'


@pytest.mark.asyncio
async def test_upload_object_two_step_upload():
    """POST /objects/upload → presigned URL; PUT presigned → object store."""
    put_received: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        # Step 1: POST /objects/upload returns presigned PUT URL.
        assert req.method == "POST"
        assert req.url.path == "/api/v1/objects/upload"
        return _ok({"upload_url": "http://storage.test/put-bucket/obj-99", "object_id": "obj-99"})

    def put_handler(req: httpx.Request) -> httpx.Response:
        put_received["method"] = req.method
        put_received["url"] = str(req.url)
        put_received["body"] = req.content
        put_received["content_type"] = req.headers.get("content-type")
        return httpx.Response(200)

    core = _make_client(handler)
    # Patch the standalone AsyncClient used inside upload_object for the PUT
    # step only. The Core base client was already constructed with its own
    # MockTransport, so this patch only affects the new client created during
    # the PUT call.
    import app.core_api.client as client_mod

    original_async_client = client_mod.httpx.AsyncClient

    class _PutOnlyClient:
        def __init__(self, *args, **kwargs):
            self._inner = original_async_client(transport=httpx.MockTransport(put_handler))

        async def __aenter__(self):
            await self._inner.__aenter__()
            return self._inner

        async def __aexit__(self, *a):
            await self._inner.__aexit__(*a)

    client_mod.httpx.AsyncClient = _PutOnlyClient
    try:
        async with core:
            object_id = await core.upload_object(
                bucket_id="kb-docs",
                key="kb-1/doc-1/img.png",
                content_bytes=b"\x89PNGdata",
                content_type="image/png",
                idempotency_key="up-1",
            )
    finally:
        client_mod.httpx.AsyncClient = original_async_client

    assert object_id == "obj-99"
    assert put_received["method"] == "PUT"
    assert put_received["url"] == "http://storage.test/put-bucket/obj-99"
    assert put_received["body"] == b"\x89PNGdata"
    assert put_received["content_type"] == "image/png"


@pytest.mark.asyncio
async def test_upload_object_raises_on_missing_upload_url():
    def handler(req: httpx.Request) -> httpx.Response:
        return _ok({"object_id": "obj-1"})  # missing upload_url

    async with _make_client(handler) as core:
        with pytest.raises(CoreAPIError, match="missing upload_url"):
            await core.upload_object(
                bucket_id="kb-docs", key="k", content_bytes=b"x", idempotency_key="k"
            )


# ── error mapping ─────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_error_raises_core_api_error_with_status():
    def handler(req: httpx.Request) -> httpx.Response:
        return httpx.Response(404, json={"code": "NOT_FOUND", "message": "not found"})

    async with _make_client(handler) as core:
        with pytest.raises(CoreAPIError) as exc:
            await core.get_vector_store(vector_store_id="missing")
    assert exc.value.status_code == 404
    assert exc.value.code == "NOT_FOUND"


@pytest.mark.asyncio
async def test_create_vector_store_error_raises():
    def handler(req: httpx.Request) -> httpx.Response:
        return httpx.Response(400, json={"code": "INVALID", "message": "bad name"})

    async with _make_client(handler) as core:
        with pytest.raises(CoreAPIError) as exc:
            await core.create_vector_store(
                name="", dimension=1, idempotency_key="k"
            )
    assert exc.value.status_code == 400


@pytest.mark.asyncio
async def test_tenant_header_sent():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["tenant"] = req.headers.get("X-Tenant-Id")
        return _ok({"items": [], "total": 0})

    async with _make_client(handler) as core:
        await core.list_vector_stores()
    assert captured["tenant"] == TENANT_ID
