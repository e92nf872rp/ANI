"""Core OpenAPI REST client for kb-service (SPEC §2.2, §2.4, §6.1).

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

All calls use httpx.AsyncClient. Errors are surfaced as CoreAPIError so the
gRPC servicer can map them to gRPC status codes (SPEC §4.3, §7.1).
"""
from __future__ import annotations

from typing import Any

import httpx
import os


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
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._tenant_id = tenant_id
        headers = {"X-Tenant-Id": tenant_id, "Accept": "application/json"}
        if auth_token:
            headers["Authorization"] = f"Bearer {auth_token}"
        # Dev mode: forward X-Dev-Tenant-ID so the gateway's dev auth middleware
        # uses the correct tenant for object storage lookups.
        dev_tenant_id = os.environ.get("ANI_DEV_TENANT_ID", "")
        if dev_tenant_id:
            headers["X-Dev-Tenant-ID"] = dev_tenant_id
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

    # ── vector-stores documents (Plan §3.3 — insert / search / upload) ───────

    async def insert_vector_documents(
        self,
        *,
        vector_store_id: str,
        documents: list[dict[str, Any]],
        idempotency_key: str,
    ) -> dict[str, Any]:
        """POST /vector-stores/{id}/documents — insert pre-computed vectors.

        body: {idempotency_key, documents: [{id, content, vector, metadata}]}
        The `vector` field is the pre-computed embedding (computed by
        rag-engine Embed RPC); Core stores it directly without embedding
        (Plan §2.2 / Core API §1.4).
        """
        body = {
            "idempotency_key": idempotency_key,
            "documents": documents,
        }
        resp = await self._client.post(
            f"/vector-stores/{vector_store_id}/documents", json=body
        )
        # Core API returns 202 Accepted (async insert: returns task_id +
        # Location polling header, VectorStoreDocumentInsertResponse).
        if resp.status_code != 202:
            raise _to_error(resp, "insertVectorStoreDocuments")
        return resp.json()

    async def search_vector_store(
        self,
        *,
        vector_store_id: str,
        vector: list[float],
        top_k: int = 10,
        filter_expr: str | None = None,
    ) -> list[dict[str, Any]]:
        """POST /vector-stores/{id}/search — vector search returning content.

        Sends the pre-computed query vector (from rag-engine Embed RPC).
        Returns the `items` list; each item has {id, score, content, metadata}
        (Core API §1.4 added the `content` field so kb-service avoids a second
        PG round-trip).
        """
        body: dict[str, Any] = {"vector": vector, "top_k": top_k}
        if filter_expr:
            body["filter"] = filter_expr
        resp = await self._client.post(
            f"/vector-stores/{vector_store_id}/search", json=body
        )
        if resp.status_code != 200:
            raise _to_error(resp, "searchVectorStore")
        data = resp.json()
        return data.get("items", [])

    async def upload_object(
        self,
        *,
        bucket_id: str,
        key: str,
        content_bytes: bytes,
        content_type: str | None = None,
        idempotency_key: str,
    ) -> str:
        """Two-step object upload (Plan §3.3).

        1. POST /objects/upload → {upload_url, object_id} (presigned PUT URL).
        2. PUT {upload_url} body=content_bytes → upload to object storage.

        Returns the object_id. The PUT goes to the presigned URL (not the Core
        base URL), so it uses a standalone httpx.AsyncClient without tenant
        headers.
        """
        pre = await self.request_upload_url(
            bucket_id=bucket_id,
            key=key,
            content_type=content_type,
            idempotency_key=idempotency_key,
        )
        upload_url = pre.get("upload_url") or pre.get("uploadUrl")
        object_id = pre.get("object_id") or pre.get("objectId")
        if not upload_url or not object_id:
            raise CoreAPIError(
                "uploadStorageObject: missing upload_url/object_id in response",
                status_code=200,
            )
        headers: dict[str, str] = {}
        if content_type:
            headers["Content-Type"] = content_type
        async with httpx.AsyncClient(timeout=60.0) as put_client:
            put_resp = await put_client.put(upload_url, content=content_bytes, headers=headers)
        if put_resp.status_code not in (200, 204):
            raise CoreAPIError(
                f"uploadObject PUT failed: HTTP {put_resp.status_code}",
                status_code=put_resp.status_code,
            )
        return object_id


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
