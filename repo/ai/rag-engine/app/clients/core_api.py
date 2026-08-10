"""Core API client for object download (SPEC §2.1, §5.1 parse_worker).

rag-engine's parse_worker downloads source documents via the Core API
``GET /objects/{object_id}/download`` endpoint (Core OpenAPI
``/objects/{object_id}/download``). The endpoint returns a presigned
download URL; the client then streams the bytes to a local temp file so
the parse pipeline (DoclingReader / PyMuPDF) can access it on disk.

The Core API base URL is configured via ``settings.ani_gateway_url``
(Core OpenAPI ``servers[0].url = https://{host}/api/v1``). A service
account token is used for cross-service auth (SPEC §7.1).
"""
from __future__ import annotations

import logging
import os
import re
import tempfile
from typing import Any

import httpx

from app.core.config import settings

logger = logging.getLogger(__name__)

# Default presigned-URL download timeout (seconds). The presigned URL fetch
# is a streaming binary download; allow generous time for large documents.
DEFAULT_DOWNLOAD_TIMEOUT = 120.0

# Only allow file extensions as suffix to prevent path injection (#12).
_SAFE_SUFFIX_RE = re.compile(r"^\.[\w\-]+$")


class CoreApiError(Exception):
    """Error from the Core API download call."""


def _safe_suffix(file_name: str | None) -> str:
    """Extract a safe file extension suffix from ``file_name`` (#12).

    Only returns ``.ext`` (alphanumeric + dash) to prevent path traversal
    via crafted ``file_name`` values from the NATS payload.
    """
    if not file_name:
        return ""
    # Take only the extension (last ``.ext`` component).
    _, ext = os.path.splitext(file_name)
    if ext and _SAFE_SUFFIX_RE.match(ext):
        return ext
    return ""


class CoreApiClient:
    """Async client for the Core API object download endpoint.

    The Core API ``/objects/{object_id}/download`` returns a presigned URL
    (``StorageObjectDownloadInfo``). This client fetches that URL, then
    streams the object bytes to a temp file and returns the local path so
    ``parse_service`` can read it.

    Args:
        base_url: Core API base URL (``settings.ani_gateway_url``). Must
            end with ``/api/v1`` per Core OpenAPI ``servers[0].url``.
        token: Service-account bearer token (SPEC §7.1). When empty no
            Authorization header is sent (dev mode).
        timeout: HTTP timeout for the presigned-URL metadata request.
        download_timeout: Timeout for streaming the object bytes.
    """

    def __init__(
        self,
        *,
        base_url: str | None = None,
        token: str | None = None,
        timeout: float = 30.0,
        download_timeout: float = DEFAULT_DOWNLOAD_TIMEOUT,
        client: httpx.AsyncClient | None = None,
    ) -> None:
        self._base_url = (base_url or settings.ani_gateway_url).rstrip("/")
        if not self._base_url.endswith("/api/v1"):
            self._base_url = self._base_url.rstrip("/") + "/api/v1"
        self._token = token or os.environ.get("ANI_CORE_API_TOKEN", "")
        self._timeout = timeout
        self._download_timeout = download_timeout
        self._client: httpx.AsyncClient | None = client
        self._owns_client = client is None

    async def _get_client(self) -> httpx.AsyncClient:
        """Return the httpx client, lazily creating one if needed (#3).

        Ensures connection reuse across multiple ``download_object`` calls
        instead of creating+closing a client per request.
        """
        if self._client is None:
            self._client = httpx.AsyncClient(timeout=self._timeout)
            self._owns_client = True
        return self._client

    async def aclose(self) -> None:
        if self._owns_client and self._client is not None:
            await self._client.aclose()
            self._client = None

    async def __aenter__(self) -> "CoreApiClient":
        return self

    async def __aexit__(self, exc_type, exc, tb) -> None:
        await self.aclose()

    def _headers(self) -> dict[str, str]:
        h: dict[str, str] = {}
        if self._token:
            h["Authorization"] = f"Bearer {self._token}"
        return h

    async def download_object(
        self,
        object_id: str,
        *,
        dest_dir: str | None = None,
        file_name: str | None = None,
    ) -> str:
        """Download a Core storage object to a local temp file.

        Args:
            object_id: The Core ``object_id`` (from the NATS payload's
                ``storage_path`` derived object id).
            dest_dir: Optional directory for the temp file. Defaults to the
                system temp dir.
            file_name: Optional file name — only the extension is used as
                the temp file suffix (sanitized, #12).

        Returns:
            The local file path of the downloaded document.

        Raises:
            CoreApiError: if the presigned URL request fails or the object
                download fails.
        """
        # 1. Fetch the presigned download URL from Core API (#3: reuse client).
        client = await self._get_client()
        resp = await client.get(
            f"{self._base_url}/objects/{object_id}/download",
            headers=self._headers(),
        )

        if resp.status_code != 200:
            try:
                detail = resp.json().get("detail", resp.text)
            except Exception:
                detail = resp.text
            raise CoreApiError(
                f"Core API download request failed ({resp.status_code}): {detail}"
            )

        body = resp.json()
        download_url = body.get("download_url")
        if not download_url:
            raise CoreApiError("Core API download response missing 'download_url'")

        # 2. Stream the object bytes to a temp file.
        # #12: only use a sanitized extension as suffix.
        suffix = _safe_suffix(file_name)
        tmp = tempfile.NamedTemporaryFile(
            dir=dest_dir, suffix=suffix, delete=False, mode="wb"
        )
        try:
            async with httpx.AsyncClient(timeout=self._download_timeout) as dl_client:
                async with dl_client.stream("GET", download_url) as dl_resp:
                    if dl_resp.status_code != 200:
                        raise CoreApiError(
                            f"Object download failed ({dl_resp.status_code})"
                        )
                    async for chunk in dl_resp.aiter_bytes():
                        tmp.write(chunk)
        except Exception as exc:
            tmp.close()
            try:
                os.unlink(tmp.name)
            except OSError:
                pass
            raise CoreApiError(f"Object download stream failed: {exc}") from exc
        finally:
            tmp.close()

        logger.info("downloaded object %s → %s", object_id, tmp.name)
        return tmp.name


_core_client: CoreApiClient | None = None


def get_core_client() -> CoreApiClient:
    """Return the module-level CoreApiClient singleton."""
    global _core_client
    if _core_client is None:
        _core_client = CoreApiClient()
    return _core_client


async def close_core_client() -> None:
    """Close the singleton CoreApiClient and release its httpx connection pool.

    Called from the FastAPI lifespan shutdown handler.
    """
    global _core_client
    if _core_client is not None:
        await _core_client.aclose()
        _core_client = None
