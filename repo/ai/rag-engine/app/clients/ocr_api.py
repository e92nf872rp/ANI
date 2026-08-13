"""AI service OCR API client (US-006 / SPEC §4.2).

Calls the PaddleOCR PP-OCRv4 endpoint exposed by inference-service (issue #5),
so rag-engine does not install PaddleOCR locally. The scanned-page fallback
that invokes this client is deferred to a later phase per current scope.
"""
from __future__ import annotations

from typing import Literal

import httpx
from pydantic import BaseModel, Field

from app.core.config import settings

RegionType = Literal["text", "table", "figure"]


class OcrRegion(BaseModel):
    type: RegionType = Field(description="Layout region type: text | table | figure")
    text: str = ""
    table_html: str | None = None


class OCRResult(BaseModel):
    regions: list[OcrRegion] = Field(default_factory=list)
    ocr_confidence: float = 0.0


class OcrApiClient:
    """Thin async client for the AI service OCR inference endpoint."""

    def __init__(self, base_url: str | None = None, timeout: float | None = None) -> None:
        self.base_url = (base_url or settings.ocr_api_base).rstrip("/")
        self.timeout = timeout if timeout is not None else settings.ocr_timeout_seconds

    async def ocr(
        self,
        image: bytes,
        lang: str = "ch",
        use_angle_cls: bool = True,
    ) -> OCRResult:
        """Request OCR for a single page image.

        Args:
            image: Rendered page image bytes.
            lang: OCR language, default "ch" (PP-OCRv4).
            use_angle_cls: Enable angle classification, default True.

        Returns:
            OCRResult with layout regions (text/table/figure) and confidence.

        Raises:
            httpx.HTTPStatusError: on 4xx/5xx from the AI service.
        """
        files = {"image": ("page.png", image, "image/png")}
        params = {"lang": lang, "use_angle_cls": str(use_angle_cls).lower()}
        async with httpx.AsyncClient(base_url=self.base_url, timeout=self.timeout) as client:
            resp = await client.post("/v1/ocr", files=files, params=params)
            resp.raise_for_status()
            return OCRResult.model_validate(resp.json())


_ocr_client: OcrApiClient | None = None


def get_ocr_client() -> OcrApiClient:
    global _ocr_client
    if _ocr_client is None:
        _ocr_client = OcrApiClient()
    return _ocr_client
