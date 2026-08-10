"""MinIO image uploader for document parsing (plan.md §4.4).

Uploads extracted images to the `ani-kb-docs` bucket and returns a permanent
URL that can be embedded as a placeholder node in the parsed text stream.
"""
from __future__ import annotations

import io
import uuid

from app.core.config import settings
from minio import Minio
from minio.error import S3Error


class ImageUploader:
    """Upload images to MinIO and return object URLs."""

    def __init__(self) -> None:
        self._client = Minio(
            settings.minio_endpoint,
            access_key=settings.minio_access_key,
            secret_key=settings.minio_secret_key,
            secure=settings.minio_secure,
        )
        self._bucket = settings.minio_bucket
        self._ensure_bucket()

    def _ensure_bucket(self) -> None:
        try:
            if not self._client.bucket_exists(self._bucket):
                self._client.make_bucket(self._bucket)
        except S3Error:
            # Bucket creation is best-effort; upload will surface real errors.
            pass

    def upload(self, data: bytes, object_prefix: str, ext: str = "png") -> str:
        """Upload image bytes and return a stable object URL.

        Args:
            data: Image bytes.
            object_prefix: Logical key prefix, e.g. "{tenant_id}/{kb_id}/{doc_id}".
            ext: Image file extension.

        Returns:
            Public object path "{bucket}/{key}" within MinIO.
        """
        key = f"{object_prefix}/images/{uuid.uuid4().hex}.{ext}"
        self._client.put_object(
            bucket_name=self._bucket,
            object_name=key,
            data=io.BytesIO(data),
            length=len(data),
            content_type=f"image/{ext}",
        )
        return f"{self._bucket}/{key}"
