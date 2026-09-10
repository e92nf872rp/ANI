"""Shared keyset-cursor parsing for paginated list endpoints.

Cursor formats used across kb-service list RPCs:

* composite ``{created_at_iso}|{uuid}`` — sessions / messages / citations
  (keyset over (created_at, id))
* plain ``{uuid}`` — chunks (keyset over id ASC)

A malformed cursor is a client input error: the servicer maps
InvalidCursorError to grpc INVALID_ARGUMENT instead of letting the raw
ValueError surface as UNKNOWN.
"""
from __future__ import annotations

import uuid
from datetime import datetime


class InvalidCursorError(ValueError):
    """Raised when a pagination cursor cannot be parsed (client error)."""


def parse_composite_cursor(cursor: str) -> tuple[datetime, uuid.UUID]:
    """Parse a ``{created_at_iso}|{uuid}`` composite keyset cursor.

    Raises InvalidCursorError on malformed timestamps or ids.
    """
    ts_iso, sep, id_part = cursor.partition("|")
    if not sep or not id_part:
        raise InvalidCursorError(f"malformed cursor: {cursor!r}")
    try:
        cursor_ts = datetime.fromisoformat(ts_iso)
        cursor_id = uuid.UUID(id_part)
    except ValueError as e:
        raise InvalidCursorError(f"malformed cursor: {cursor!r}") from e
    return cursor_ts, cursor_id


def parse_chunk_cursor(cursor: str) -> uuid.UUID:
    """Parse a plain-UUID chunk keyset cursor.

    Raises InvalidCursorError on malformed UUIDs.
    """
    try:
        return uuid.UUID(cursor)
    except ValueError as e:
        raise InvalidCursorError(f"malformed cursor: {cursor!r}") from e
