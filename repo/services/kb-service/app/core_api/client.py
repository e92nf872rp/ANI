"""Core OpenAPI REST client for kb-service (SPEC §2.2, §2.4, §6.1, §4.1).

kb-service is a Services-layer process and MUST call Core via the Core
OpenAPI REST API (CLAUDE.md §3 cross-layer boundary). This client wraps the
endpoints needed by the KB business logic:

- POST   /vector-stores                     createVectorStore (CreateKB)
- GET    /vector-stores/{id}                getVectorStore
- GET    /vector-stores                     listVectorStores
- DELETE /vector-stores/{id}                deleteVectorStore (DeleteKB)
- DELETE /vector-stores/{id}/documents      deleteVectorStoreDocuments (DeleteDocument)
- POST   /objects/upload                     uploadStorageObject (GetDocumentUploadURL)
- GET    /objects/{id}/download              downloadStorageObject (NotifyDocumentUploaded checksum verify)
- POST   /data/query                         dataQuery (通用数据面，7 表读写，SPEC §4.1)
- POST   /data/tables                        dataCreateTable (受管建表，SPEC §4.1)

All calls use httpx.AsyncClient. Errors are surfaced as CoreAPIError so the
gRPC servicer can map them to gRPC status codes (SPEC §4.3, §7.1).
"""
from __future__ import annotations

from typing import Any

import httpx


class CoreAPIError(Exception):
    """Error from a Core OpenAPI REST call.

    Attributes:
        status_code: HTTP status code from Core (None for transport errors).
        code:        error code string from the Core response body, if any.
        message:     human-readable detail.
    """

    def __init__(self, message: str, *, status_code: int | None = None, code: str | None = None) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.code = code


class CoreClient:
    """Async httpx client for the Core OpenAPI REST endpoints used by kb-service.

    The base URL is derived from settings.core_api_base_url
    (ANI_GATEWAY_INTERNAL_URL + /api/v1). The tenant context is passed via the
    `X-Tenant-Id` header and the service account token via `Authorization`.
    """

    def __init__(
        self,
        *,
        base_url: str,
        tenant_id: str,
        auth_token: str | None = None,
        timeout: float = 30.0,
        client: httpx.AsyncClient | None = None,
        extra_headers: dict[str, str] | None = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._tenant_id = tenant_id
        headers = {"X-Tenant-Id": tenant_id, "Accept": "application/json"}
        if auth_token:
            headers["Authorization"] = f"Bearer {auth_token}"
        if extra_headers:
            headers.update(extra_headers)
        if client is not None:
            # Merge tenant headers into the injected client (used for testing
            # with MockTransport so the caller doesn't have to set them).
            client.headers.update(headers)
            self._client = client
        else:
            self._client = httpx.AsyncClient(
                base_url=self._base_url, headers=headers, timeout=timeout
            )
        self._owns_client = client is None

    async def aclose(self) -> None:
        if self._owns_client:
            await self._client.aclose()

    async def __aenter__(self) -> "CoreClient":
        return self

    async def __aexit__(self, exc_type, exc, tb) -> None:
        await self.aclose()

    async def ping(self, *, timeout: float = 2.0) -> bool:
        """Lightweight data-plane reachability probe (SPEC §4.3).

        Issues a GET to the gateway ``/healthz`` endpoint with a short
        timeout. The CoreClient's base_url typically includes the
        ``/api/v1`` prefix (e.g. ``http://gw:8080/api/v1``), but the gateway
        health endpoint lives at the host root (``/healthz``), so the probe
        strips the API prefix and targets the gateway root. Returns True on
        any 2xx response, False on transport error or non-2xx. Used by
        ``/readyz`` so the readiness probe reflects real data-plane
        reachability rather than mere client construction.
        """
        # Derive the gateway root from base_url by stripping the API path
        # prefix (e.g. /api/v1). Use netloc (not host) so IPv6 literals keep
        # their brackets (e.g. [::1]:8080); host strips brackets and would
        # produce an ambiguous URL like http://::1:8080. netloc is bytes in
        # httpx, so decode to str.
        from httpx import URL

        base = URL(self._base_url)
        root_url = f"{base.scheme}://{base.netloc.decode('ascii')}"
        try:
            resp = await self._client.get(
                f"{root_url}/healthz", timeout=timeout
            )
            return 200 <= resp.status_code < 300
        except Exception:  # noqa: BLE001 — probe must not raise
            return False

    # ── vector-stores collection CRUD (SPEC §6.1 CreateKB / DeleteKB) ────────

    async def create_vector_store(
        self,
        *,
        name: str,
        dimension: int,
        metric: str = "cosine",
        embedding_model: str | None = None,
        idempotency_key: str,
    ) -> dict[str, Any]:
        """POST /vector-stores — create a vector collection for a KB."""
        body: dict[str, Any] = {
            "name": name,
            "dimension": dimension,
            "metric": metric,
            "idempotency_key": idempotency_key,
        }
        if embedding_model:
            body["embedding_model"] = embedding_model
        resp = await self._client.post("/vector-stores", json=body)
        if resp.status_code != 201:
            raise _to_error(resp, "createVectorStore")
        return resp.json()

    async def get_vector_store(self, *, vector_store_id: str) -> dict[str, Any]:
        """GET /vector-stores/{id}."""
        resp = await self._client.get(f"/vector-stores/{vector_store_id}")
        if resp.status_code != 200:
            raise _to_error(resp, "getVectorStore")
        return resp.json()

    async def list_vector_stores(
        self, *, limit: int = 20, cursor: str | None = None
    ) -> dict[str, Any]:
        """GET /vector-stores."""
        params: dict[str, Any] = {"limit": limit}
        if cursor:
            params["cursor"] = cursor
        resp = await self._client.get("/vector-stores", params=params)
        if resp.status_code != 200:
            raise _to_error(resp, "listVectorStores")
        return resp.json()

    async def get_bucket_id_by_name(self, *, name: str) -> str | None:
        """GET /buckets — find a bucket UUID by name.

        The Core object-store keys buckets by UUID, but kb-service uses the
        bucket name "kb-docs" as a convention. This helper lists buckets for
        the tenant and finds the one matching the name.
        """
        resp = await self._client.get("/buckets", params={"limit": 100})
        if resp.status_code != 200:
            raise _to_error(resp, "listStorageBuckets")
        data = resp.json()
        for item in data.get("items", []):
            if item.get("name") == name:
                return item.get("id")
        return None

    async def delete_vector_store(self, *, vector_store_id: str) -> dict[str, Any]:
        """DELETE /vector-stores/{id} — delete the collection (DeleteKB)."""
        resp = await self._client.delete(f"/vector-stores/{vector_store_id}")
        if resp.status_code != 200:
            raise _to_error(resp, "deleteVectorStore")
        return resp.json()

    async def delete_vector_store_documents(
        self, *, vector_store_id: str, filter_expr: str
    ) -> dict[str, Any]:
        """DELETE /vector-stores/{id}/documents?filter=... — best-effort vector cleanup (DeleteDocument)."""
        resp = await self._client.delete(
            f"/vector-stores/{vector_store_id}/documents",
            params={"filter": filter_expr},
        )
        if resp.status_code != 200:
            raise _to_error(resp, "deleteVectorStoreDocuments")
        return resp.json()

    # ── objects upload/download (SPEC §6.1 GetDocumentUploadURL / checksum verify) ──

    async def request_upload_url(
        self,
        *,
        bucket_id: str,
        key: str,
        content_type: str | None = None,
        idempotency_key: str,
    ) -> dict[str, Any]:
        """POST /objects/upload — request a presigned PUT URL."""
        body: dict[str, Any] = {
            "idempotency_key": idempotency_key,
            "bucket_id": bucket_id,
            "key": key,
        }
        if content_type:
            body["content_type"] = content_type
        resp = await self._client.post("/objects/upload", json=body)
        if resp.status_code != 200:
            raise _to_error(resp, "uploadStorageObject")
        return resp.json()

    async def request_download_url(
        self, *, object_id: str, expires_seconds: int = 3600
    ) -> dict[str, Any]:
        """GET /objects/{id}/download — request a presigned GET URL (checksum verify)."""
        resp = await self._client.get(
            f"/objects/{object_id}/download",
            params={"expires_seconds": expires_seconds},
        )
        if resp.status_code != 200:
            raise _to_error(resp, "downloadStorageObject")
        return resp.json()

    async def head_object(self, *, object_id: str) -> dict[str, Any]:
        """GET /objects/{id} — fetch object metadata (used for checksum verify)."""
        resp = await self._client.get(f"/objects/{object_id}")
        if resp.status_code != 200:
            raise _to_error(resp, "getStorageObject")
        return resp.json()

    # ── data plane (SPEC §4.1 dataQuery / dataCreateTable) ───────────────────

    async def data_query(
        self,
        *,
        sql: str,
        params: list[Any] | None = None,
        role: str = "tenant",
    ) -> dict[str, Any]:
        """POST /data/query — 参数化 SQL 执行（单事务）（SPEC §4.1, §3.1）。

        一次调用即一个事务；所有业务 SQL 必须使用 $1..$n 绑定参数，禁止把 params
        拼接进 SQL（SPEC §3.3 安全加固）。

        Args:
            sql:    参数化 SQL（占位符 $1..$n），可含多语句（同一事务）。
            params: 绑定参数（标量数组），禁止拼接进 SQL。
            role:   ``tenant`` 按 ``X-Tenant-Id`` 设 RLS；``service`` 跨租户
                    （outbox 派发器专用，SPEC §4.2）。

        Returns:
            ``{"columns": [...], "rows": [...], "rowcount": int, "last_result": bool}``
            其中 ``columns``/``last_result`` 可缺省。
        """
        body: dict[str, Any] = {"sql": sql, "params": params or []}
        if role is not None:
            body["role"] = role
        resp = await self._client.post("/data/query", json=body)
        if resp.status_code != 200:
            raise _to_error(resp, "dataQuery")
        return resp.json()

    async def create_table(self, *, name: str, definition: str) -> dict[str, Any]:
        """POST /data/tables — 受管建表（受管迁移）（SPEC §4.1, §3.1）。

        仅接受受管 schema 定义（create/alter），走 Core 迁移编排并记录审计；
        破坏性语句（DROP/TRUNCATE/ALTER SYSTEM）由 Core 拒绝返回 422（SPEC §3.3）。

        Returns:
            ``{"name": str, "status": "created" | "applied"}``
        """
        body = {"name": name, "definition": definition}
        resp = await self._client.post("/data/tables", json=body)
        if resp.status_code != 201:
            raise _to_error(resp, "dataCreateTable")
        return resp.json()


def _to_error(resp: httpx.Response, op: str) -> CoreAPIError:
    """Convert a non-2xx Core response into a CoreAPIError."""
    try:
        body = resp.json()
        code = body.get("code") or body.get("error_code")
        message = body.get("message") or body.get("detail") or body.get("error") or op
    except Exception:
        code = None
        message = f"{op} failed: HTTP {resp.status_code}"
    return CoreAPIError(message, status_code=resp.status_code, code=code)
