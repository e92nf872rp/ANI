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
