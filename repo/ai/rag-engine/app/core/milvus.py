"""Milvus connection and collection helpers."""
from pymilvus import connections

from app.core.config import settings

_connected = False


async def init_milvus() -> None:
    global _connected
    if _connected:
        return
    connections.connect(
        alias="default",
        host=settings.milvus_host,
        port=settings.milvus_port,
    )
    _connected = True


def kb_collection_name(kb_id: str) -> str:
    """Derive a stable Milvus collection name from a knowledge base UUID."""
    # Strip hyphens: Milvus collection names must be alphanumeric + underscore
    return "kb_" + kb_id.replace("-", "")
