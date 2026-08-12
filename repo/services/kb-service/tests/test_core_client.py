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


# ── data plane (dataQuery / dataCreateTable, SPEC §4.1) ────────────────────────


@pytest.mark.asyncio
async def test_data_query_posts_correct_request():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["method"] = req.method
        captured["path"] = req.url.path
        captured["json"] = json.loads(req.content)
        return _ok({"columns": ["id", "name"], "rows": [{"id": 1, "name": "kb1"}], "rowcount": 1, "last_result": True})

    async with _make_client(handler) as core:
        resp = await core.data_query(
            sql="SELECT id, name FROM knowledge_bases WHERE tenant_id = $1",
            params=[TENANT_ID],
        )

    assert captured["method"] == "POST"
    assert captured["path"] == "/api/v1/data/query"
    assert captured["json"]["sql"].startswith("SELECT id, name")
    assert captured["json"]["params"] == [TENANT_ID]
    assert captured["json"]["role"] == "tenant"
    assert resp["rowcount"] == 1
    assert resp["rows"][0]["name"] == "kb1"


@pytest.mark.asyncio
async def test_data_query_defaults_params_to_empty_list():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["json"] = json.loads(req.content)
        return _ok({"rows": [], "rowcount": 0})

    async with _make_client(handler) as core:
        resp = await core.data_query(sql="SELECT 1")

    assert captured["json"]["params"] == []
    assert resp["rowcount"] == 0


@pytest.mark.asyncio
async def test_data_query_role_service_for_outbox_dispatcher():
    """role=service 跨租户，供 outbox 派发器使用（SPEC §4.1, §4.2）."""
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["json"] = json.loads(req.content)
        return _ok({"rows": [{"id": "ev1"}], "rowcount": 1})

    async with _make_client(handler) as core:
        await core.data_query(
            sql="SELECT id FROM outbox_events WHERE dispatched = $1",
            params=[False],
            role="service",
        )

    assert captured["json"]["role"] == "service"


@pytest.mark.asyncio
async def test_data_query_error_maps_to_core_api_error():
    def handler(req: httpx.Request) -> httpx.Response:
        return httpx.Response(422, json={"code": "UNSUPPORTED_QUERY", "message": "DROP rejected"})

    async with _make_client(handler) as core:
        with pytest.raises(CoreAPIError) as exc:
            await core.data_query(sql="DROP TABLE x")
    assert exc.value.status_code == 422
    assert exc.value.code == "UNSUPPORTED_QUERY"


@pytest.mark.asyncio
async def test_create_table_posts_correct_request():
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["method"] = req.method
        captured["path"] = req.url.path
        captured["json"] = json.loads(req.content)
        return _created({"name": "knowledge_bases", "status": "created"})

    async with _make_client(handler) as core:
        resp = await core.create_table(
            name="knowledge_bases",
            definition="CREATE TABLE knowledge_bases (id uuid PRIMARY KEY)",
        )

    assert captured["method"] == "POST"
    assert captured["path"] == "/api/v1/data/tables"
    assert captured["json"]["name"] == "knowledge_bases"
    assert captured["json"]["definition"].startswith("CREATE TABLE")
    assert resp["status"] == "created"


@pytest.mark.asyncio
async def test_create_table_applied_status():
    def handler(req: httpx.Request) -> httpx.Response:
        return _created({"name": "kb_chunks", "status": "applied"})

    async with _make_client(handler) as core:
        resp = await core.create_table(name="kb_chunks", definition="ALTER TABLE kb_chunks ADD COLUMN x text")
    assert resp["status"] == "applied"


@pytest.mark.asyncio
async def test_create_table_error_maps_to_core_api_error():
    def handler(req: httpx.Request) -> httpx.Response:
        return httpx.Response(422, json={"code": "UNSUPPORTED_QUERY", "message": "DROP not allowed"})

    async with _make_client(handler) as core:
        with pytest.raises(CoreAPIError) as exc:
            await core.create_table(name="x", definition="DROP TABLE x")
    assert exc.value.status_code == 422
    assert exc.value.code == "UNSUPPORTED_QUERY"


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


# ── data-plane reachability probe (issue-031 SPEC §4.3) ──────────────────────


@pytest.mark.asyncio
async def test_ping_returns_true_on_2xx():
    """ping() returns True when the gateway /healthz responds 2xx."""
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["path"] = req.url.path
        captured["method"] = req.method
        return httpx.Response(200, json={"status": "ok"})

    async with _make_client(handler) as core:
        ok = await core.ping()
    assert ok is True
    # /healthz is at the gateway root, not under /api/v1.
    assert captured["method"] == "GET"
    assert captured["path"] == "/healthz"


@pytest.mark.asyncio
async def test_ping_returns_false_on_transport_error():
    """ping() returns False (does not raise) on a transport error."""

    def handler(req: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("gateway unreachable")

    async with _make_client(handler) as core:
        ok = await core.ping()
    assert ok is False


@pytest.mark.asyncio
async def test_ping_returns_false_on_non_2xx():
    """ping() returns False on a non-2xx response (e.g. 503)."""

    def handler(req: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"status": "unavailable"})

    async with _make_client(handler) as core:
        ok = await core.ping()
    assert ok is False


@pytest.mark.asyncio
async def test_ping_handles_ipv6_base_url():
    """ping() targets the gateway root /healthz and preserves IPv6 brackets.

    Regression: using URL.host (unbracketed) would produce an ambiguous
    URL like http://::1:8080/healthz. Using URL.netloc keeps the brackets
    so IPv6 literals resolve correctly.
    """
    captured: dict = {}

    def handler(req: httpx.Request) -> httpx.Response:
        captured["url"] = str(req.url)
        captured["path"] = req.url.path
        return httpx.Response(200, json={"status": "ok"})

    # IPv6 loopback with the /api/v1 prefix.
    async with _make_client(handler, base_url="http://[::1]:8080/api/v1") as core:
        ok = await core.ping()
    assert ok is True
    # The request must target the gateway root /healthz with IPv6 brackets
    # preserved in the host (not http://::1:8080/healthz which is ambiguous).
    assert captured["path"] == "/healthz"
    assert "[::1]:8080" in captured["url"]
