"""Tests for the kb_chunks repository (issue-030 / Plan §3.2).

Covers:
- write_chunks: batched INSERT of parents + children + summaries, ordering,
  row count, RLS context set, executemany called once.
- delete_chunks_by_doc: DELETE with kb_id+doc_id, returns count, RLS-scoped.
- keyword_search (refactored): jieba tokenization + multi-token OR + token
  coverage normalization (score = min(1.0, n_hits / n_tokens)), SQL contains
  the `%` operator + `coalesce(similarity(...))`, pg_trgm threshold set.

Uses a recording fake asyncpg Connection so no real Postgres is required.
"""
import json
import os
import sys
import uuid
from contextlib import asynccontextmanager
from unittest.mock import AsyncMock

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.repositories import chunk as chunk_repo

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC_ID = "33333333-3333-3333-3333-333333333333"
PARENT_ID = "44444444-4444-4444-4444-444444444444"
CHILD_ID = "55555555-5555-5555-5555-555555555555"
SUMMARY_ID = "66666666-6666-6666-6666-666666666666"


# ── Fake asyncpg Connection ──────────────────────────────────────────────────


class _FakeConn:
    """Records SQL + args; returns canned rows for fetch / counts for execute."""

    def __init__(self, *, fetch_rows=None, execute_result="DELETE 3"):
        self._fetch_rows = fetch_rows or []
        self._execute_result = execute_result
        self.execute_calls: list[tuple] = []
        self.executemany_calls: list[tuple] = []
        self.fetch_calls: list[tuple] = []

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            yield self
        return _tx()

    async def execute(self, sql, *args):
        self.execute_calls.append((sql, args))
        # For keyword_search's set_config call, return a scalar row marker.
        if "set_config" in sql:
            return "set"
        return self._execute_result

    async def executemany(self, sql, rows):
        self.executemany_calls.append((sql, rows))

    async def fetch(self, sql, *args):
        self.fetch_calls.append((sql, args))
        return self._fetch_rows


def _parent(chunk_id=PARENT_ID, content="parent text", metadata=None):
    return {
        "chunk_id": chunk_id,
        "content": content,
        "content_type": "text",
        "page_number": 1,
        "token_count": 10,
        "metadata": metadata if metadata is not None else {"src": "p1"},
    }


def _child(chunk_id=CHILD_ID, content="child text", parent_chunk_id=PARENT_ID):
    return {
        "chunk_id": chunk_id,
        "content": content,
        "content_type": "text",
        "page_number": 1,
        "parent_chunk_id": parent_chunk_id,
        "parent_content": "parent text",
        "token_count": 5,
        "metadata": {"src": "c1"},
    }


def _summary(chunk_id=SUMMARY_ID, content="doc summary"):
    return {
        "chunk_id": chunk_id,
        "content": content,
        "content_type": "text",
        "page_number": 1,
        "parent_chunk_id": None,
        "parent_content": None,
        "token_count": 8,
        "metadata": {},
    }


# ── write_chunks ────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_write_chunks_inserts_parents_children_summaries_in_order():
    conn = _FakeConn()
    n = await chunk_repo.write_chunks(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        file_name="a.pdf",
        parents=[_parent()],
        children=[_child()],
        summaries=[_summary()],
    )
    assert n == 3
    # executemany called once with all rows in order parent→child→summary.
    assert len(conn.executemany_calls) == 1
    sql, rows = conn.executemany_calls[0]
    assert "INSERT INTO kb_chunks" in sql
    assert len(rows) == 3
    # row[5] is the chunk_type position in _row tuple.
    assert rows[0][5] == "parent"
    assert rows[1][5] == "child"
    assert rows[2][5] == "doc_summary"


@pytest.mark.asyncio
async def test_write_chunks_no_rows_returns_zero():
    conn = _FakeConn()
    n = await chunk_repo.write_chunks(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        file_name="a.pdf", parents=[], children=[], summaries=None,
    )
    assert n == 0
    assert conn.executemany_calls == []


@pytest.mark.asyncio
async def test_write_chunks_sets_rls_tenant_context():
    conn = _FakeConn()
    await chunk_repo.write_chunks(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        file_name="a.pdf", parents=[_parent()], children=[_child()],
    )
    # First execute call sets the tenant context.
    first_sql = conn.execute_calls[0][0]
    assert "app.current_tenant_id" in first_sql
    assert TENANT_ID in first_sql


@pytest.mark.asyncio
async def test_write_chunks_serializes_metadata_as_json():
    conn = _FakeConn()
    await chunk_repo.write_chunks(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        file_name="a.pdf",
        parents=[_parent(metadata={"k": "v"})], children=[],
    )
    _, rows = conn.executemany_calls[0]
    # row[12] is the custom_metadata position (last column).
    assert json.loads(rows[0][12]) == {"k": "v"}


# ── delete_chunks_by_doc ────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_delete_chunks_by_doc_returns_count():
    conn = _FakeConn(execute_result="DELETE 3")
    n = await chunk_repo.delete_chunks_by_doc(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID
    )
    assert n == 3
    # DELETE SQL filters by kb_id + doc_id.
    sql, args = conn.execute_calls[-1]
    assert "DELETE FROM kb_chunks" in sql
    assert "kb_id = $1" in sql
    assert "doc_id = $2" in sql
    assert args[0] == uuid.UUID(KB_ID)
    assert args[1] == uuid.UUID(DOC_ID)


@pytest.mark.asyncio
async def test_delete_chunks_by_doc_sets_rls_context():
    conn = _FakeConn(execute_result="DELETE 0")
    await chunk_repo.delete_chunks_by_doc(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID
    )
    assert "app.current_tenant_id" in conn.execute_calls[0][0]


@pytest.mark.asyncio
async def test_delete_chunks_by_doc_handles_non_numeric_result():
    conn = _FakeConn(execute_result="DELETE")
    n = await chunk_repo.delete_chunks_by_doc(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID
    )
    assert n == 0


# ── keyword_search (refactored) ─────────────────────────────────────────────


@pytest.mark.asyncio
async def test_keyword_search_uses_jieba_tokenize_and_coverage_normalization():
    """score = min(1.0, n_hits / n_tokens); SQL uses `content % $i` + coalesce(similarity).

    _tokenize_cn_keywords keeps only CJK tokens (len>=2, non-stopword), so we
    inject a fake jieba.lcut returning mixed tokens and assert the non-CJK /
    short / stopword tokens are dropped before reaching the SQL.
    """
    import types
    import sys as _sys
    # lcut returns: a CJK token, an ASCII token (dropped), a 1-char token
    # (dropped), a stopword (dropped), and a second CJK token.
    fake_jieba = types.SimpleNamespace(lcut=lambda q: ["知识库", "abc", "的", "检", "检索"])
    _sys.modules["jieba"] = fake_jieba

    rows = [
        {"chunk_id": "c1", "content": "x", "parent_content": "p",
         "parent_chunk_id": "p1", "doc_id": "d1", "file_name": "a.pdf",
         "page_number": 1, "content_type": "text", "chunk_type": "child",
         "sum_sim": 1.5, "n_hits": 2},
        {"chunk_id": "c2", "content": "y", "parent_content": "p2",
         "parent_chunk_id": "p2", "doc_id": "d2", "file_name": "b.pdf",
         "page_number": 2, "content_type": "text", "chunk_type": "child",
         "sum_sim": 0.5, "n_hits": 1},
    ]
    conn = _FakeConn(fetch_rows=rows)
    out = await chunk_repo.keyword_search(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, query="知识库检索", limit=5
    )

    # Only "知识库" and "检索" survive the CJK/length/stopword filters → 2 tokens.
    sql, args = conn.fetch_calls[0]
    assert sql.count("content % $") == 2
    assert " OR " in sql
    assert "coalesce(similarity(content, $1), 0)" in sql
    assert "n_hits" in sql
    assert "sum_sim" in sql
    assert "chunk_type = 'child'" in sql
    # args: [tok1, tok2, kb_uuid, tenant_uuid, limit]
    assert args[0] == "知识库"
    assert args[1] == "检索"
    assert args[2] == uuid.UUID(KB_ID)
    assert args[3] == uuid.UUID(TENANT_ID)
    assert args[4] == 5
    # Coverage normalization: n_tokens=2.
    assert out[0]["score"] == 1.0  # 2/2
    assert out[1]["score"] == 0.5  # 1/2
    assert out[0]["chunk_id"] == "c1"


@pytest.mark.asyncio
async def test_keyword_search_empty_query_returns_empty():
    import types
    import sys as _sys
    fake_jieba = types.SimpleNamespace(lcut=lambda q: [])
    _sys.modules["jieba"] = fake_jieba

    conn = _FakeConn()
    out = await chunk_repo.keyword_search(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, query="", limit=5
    )
    assert out == []
    assert conn.fetch_calls == []


@pytest.mark.asyncio
async def test_keyword_search_drops_non_cjk_and_short_and_stopword_tokens():
    """_tokenize_cn_keywords filters non-CJK, len<2, and stopword tokens."""
    import types
    import sys as _sys
    # All tokens are dropped → no SQL issued.
    fake_jieba = types.SimpleNamespace(lcut=lambda q: ["a", "的", "x"])
    _sys.modules["jieba"] = fake_jieba

    conn = _FakeConn(fetch_rows=[])
    out = await chunk_repo.keyword_search(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, query="a 的 x", limit=3
    )
    assert out == []
    assert conn.fetch_calls == []


@pytest.mark.asyncio
async def test_keyword_search_sets_pg_trgm_threshold():
    import types
    import sys as _sys
    # "测试" is CJK len>=2, non-stopword → survives.
    fake_jieba = types.SimpleNamespace(lcut=lambda q: ["测试"])
    _sys.modules["jieba"] = fake_jieba

    conn = _FakeConn(fetch_rows=[])
    await chunk_repo.keyword_search(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, query="测试", limit=3
    )
    # execute_calls[0] is the RLS SET LOCAL; [1] is the pg_trgm threshold.
    threshold_calls = [
        sql for (sql, _args) in conn.execute_calls
        if "pg_trgm.similarity_threshold" in sql
    ]
    assert threshold_calls, "pg_trgm.similarity_threshold set_config was not called"
    assert "'0.0'" in threshold_calls[0]
