"""Tests for the three data-plane-migrated repositories (issue #029).

Verifies the knowledge_base / document / chunk repositories call
`CoreClient.data_query` with the correct SQL/params and interpret the
data-plane response ({rows, rowcount, columns, last_result}) correctly.

Uses a mock CoreClient whose `data_query` records calls and returns canned
responses so no real Core gateway / Postgres is required. The focus is on
the data-plane wiring (correct SQL, params, role; row extraction) rather
than SQL behavior (covered by integration tests against a real PG).
"""
import os
import sys
import uuid
from unittest.mock import AsyncMock, MagicMock

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.repositories import chunk as chunk_repo
from app.repositories import document as document_repo
from app.repositories import knowledge_base as kb_repo

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC_ID = "33333333-3333-3333-3333-333333333333"
CHUNK_ID = "44444444-4444-4444-4444-444444444444"


def _mock_core(*, query_return=None):
    """Build a mock CoreClient whose data_query returns query_return.

    `query_return` can be a dict (always returned) or a list of dicts
    (returned in order, one per call). The mock records every call's
    (sql, params, role) so tests can assert on them.
    """
    core = MagicMock()
    core.data_query = AsyncMock()
    core.aclose = AsyncMock()
    core.__aenter__ = AsyncMock(return_value=core)
    core.__aexit__ = AsyncMock(return_value=None)
    if isinstance(query_return, list):
        core.data_query.side_effect = query_return
    else:
        core.data_query.return_value = query_return or {}
    return core


# ── knowledge_base repository ─────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_create_kb_calls_data_query_with_insert_returning():
    row = {"id": KB_ID, "name": "kb1", "status": "active", "doc_count": 0}
    core = _mock_core(query_return={"rows": [row], "rowcount": 1})

    result = await kb_repo.create_kb(
        core, tenant_id=TENANT_ID, name="kb1", description="d",
        embedding_model="bge-m3", chunk_size=1024, top_k=5,
        score_threshold=0.3, retrieval_mode="hybrid",
    )

    assert result == row
    call = core.data_query.call_args
    sql = call.kwargs["sql"]
    assert "INSERT INTO knowledge_bases" in sql
    assert "RETURNING" in sql
    params = call.kwargs["params"]
    assert params[0] == TENANT_ID
    assert params[1] == "kb1"
    assert params[4] == 1024
    assert call.kwargs.get("role") is None or call.kwargs.get("role") == "tenant"


@pytest.mark.asyncio
async def test_get_kb_returns_row_when_found():
    row = {"id": KB_ID, "name": "kb1"}
    core = _mock_core(query_return={"rows": [row]})

    result = await kb_repo.get_kb(core, tenant_id=TENANT_ID, kb_id=KB_ID)

    assert result == row
    sql = core.data_query.call_args.kwargs["sql"]
    assert "SELECT" in sql and "FROM knowledge_bases" in sql
    assert "WHERE id = $1" in sql
    assert core.data_query.call_args.kwargs["params"] == [KB_ID]


@pytest.mark.asyncio
async def test_get_kb_returns_none_when_not_found():
    core = _mock_core(query_return={"rows": []})

    result = await kb_repo.get_kb(core, tenant_id=TENANT_ID, kb_id=KB_ID)

    assert result is None


@pytest.mark.asyncio
async def test_list_kbs_with_cursor():
    rows = [{"id": KB_ID, "name": "kb1"}]
    # Two calls: count, then select
    core = _mock_core(
        query_return=[
            {"rows": [{"total": 5}]},
            {"rows": rows},
        ]
    )

    result_rows, total = await kb_repo.list_kbs(
        core, tenant_id=TENANT_ID, limit=10, cursor=KB_ID,
    )

    assert total == 5
    assert result_rows == rows
    # Second call uses cursor + limit params
    second_call = core.data_query.call_args_list[1]
    assert second_call.kwargs["params"] == [KB_ID, 10]
    assert "WHERE id > $1" in second_call.kwargs["sql"]


@pytest.mark.asyncio
async def test_list_kbs_without_cursor():
    rows = [{"id": KB_ID, "name": "kb1"}]
    core = _mock_core(
        query_return=[
            {"rows": [{"total": 1}]},
            {"rows": rows},
        ]
    )

    result_rows, total = await kb_repo.list_kbs(
        core, tenant_id=TENANT_ID, limit=20,
    )

    assert total == 1
    assert result_rows == rows
    second_call = core.data_query.call_args_list[1]
    assert second_call.kwargs["params"] == [20]
    assert "WHERE id >" not in second_call.kwargs["sql"]


@pytest.mark.asyncio
async def test_soft_delete_kb_returns_true_when_rowcount_one():
    core = _mock_core(query_return={"rowcount": 1})

    result = await kb_repo.soft_delete_kb(core, tenant_id=TENANT_ID, kb_id=KB_ID)

    assert result is True
    sql = core.data_query.call_args.kwargs["sql"]
    assert "UPDATE knowledge_bases" in sql
    assert "status = 'deleted'" in sql
    assert core.data_query.call_args.kwargs["params"] == [KB_ID]


@pytest.mark.asyncio
async def test_soft_delete_kb_returns_false_when_rowcount_zero():
    core = _mock_core(query_return={"rowcount": 0})

    result = await kb_repo.soft_delete_kb(core, tenant_id=TENANT_ID, kb_id=KB_ID)

    assert result is False


@pytest.mark.asyncio
async def test_get_kb_status_returns_status():
    core = _mock_core(query_return={"rows": [{"status": "active"}]})

    result = await kb_repo.get_kb_status(core, tenant_id=TENANT_ID, kb_id=KB_ID)

    assert result == "active"


@pytest.mark.asyncio
async def test_get_kb_status_returns_none_when_not_found():
    core = _mock_core(query_return={"rows": []})

    result = await kb_repo.get_kb_status(core, tenant_id=TENANT_ID, kb_id=KB_ID)

    assert result is None


@pytest.mark.asyncio
async def test_increment_doc_count_calls_data_query():
    core = _mock_core(query_return={"rowcount": 1})

    await kb_repo.increment_doc_count(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, delta=1,
    )

    sql = core.data_query.call_args.kwargs["sql"]
    assert "doc_count = doc_count + $2" in sql
    assert core.data_query.call_args.kwargs["params"] == [KB_ID, 1]


# ── document repository ──────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_create_document_with_explicit_doc_id():
    row = {"id": DOC_ID, "file_name": "a.pdf", "parse_status": "pending"}
    core = _mock_core(query_return={"rows": [row]})

    result = await document_repo.create_document(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, file_name="a.pdf",
        file_type="pdf", file_size_bytes=1024, storage_path="kb-docs/kb1/d/a.pdf",
        checksum_sha256="abc", doc_id=DOC_ID, object_id="obj-1",
    )

    assert result == row
    sql = core.data_query.call_args.kwargs["sql"]
    assert "INSERT INTO kb_documents" in sql
    assert "RETURNING" in sql
    params = core.data_query.call_args.kwargs["params"]
    assert params[0] == DOC_ID
    assert params[3] == "a.pdf"


@pytest.mark.asyncio
async def test_create_document_without_doc_id():
    row = {"id": DOC_ID, "file_name": "a.pdf"}
    core = _mock_core(query_return={"rows": [row]})

    result = await document_repo.create_document(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, file_name="a.pdf",
        file_type="pdf", file_size_bytes=1024, storage_path="kb-docs/kb1/d/a.pdf",
        checksum_sha256="abc",
    )

    assert result == row
    params = core.data_query.call_args.kwargs["params"]
    # Without doc_id, params start with kb_id (no explicit id column)
    assert params[0] == KB_ID


@pytest.mark.asyncio
async def test_get_document_returns_row():
    row = {"id": DOC_ID, "file_name": "a.pdf"}
    core = _mock_core(query_return={"rows": [row]})

    result = await document_repo.get_document(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
    )

    assert result == row
    assert core.data_query.call_args.kwargs["params"] == [DOC_ID, KB_ID]


@pytest.mark.asyncio
async def test_get_document_returns_none_when_not_found():
    core = _mock_core(query_return={"rows": []})

    result = await document_repo.get_document(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
    )

    assert result is None


@pytest.mark.asyncio
async def test_list_documents_with_status_filter_and_cursor():
    rows = [{"id": DOC_ID}]
    core = _mock_core(
        query_return=[
            {"rows": [{"total": 2}]},
            {"rows": rows},
        ]
    )

    result_rows, total = await document_repo.list_documents(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, parse_status="ready",
        limit=10, cursor=DOC_ID,
    )

    assert total == 2
    assert result_rows == rows
    count_call = core.data_query.call_args_list[0]
    assert "count(*)" in count_call.kwargs["sql"]
    assert count_call.kwargs["params"] == [KB_ID, "ready"]
    select_call = core.data_query.call_args_list[1]
    assert select_call.kwargs["params"] == [KB_ID, "ready", DOC_ID, 10]


@pytest.mark.asyncio
async def test_list_documents_without_status_or_cursor():
    rows = [{"id": DOC_ID}]
    core = _mock_core(
        query_return=[
            {"rows": [{"total": 1}]},
            {"rows": rows},
        ]
    )

    result_rows, total = await document_repo.list_documents(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, limit=20,
    )

    assert total == 1
    assert result_rows == rows
    select_call = core.data_query.call_args_list[1]
    assert select_call.kwargs["params"] == [KB_ID, 20]


@pytest.mark.asyncio
async def test_update_parse_status_ready_sets_parsed_at():
    core = _mock_core(query_return={"rowcount": 1})

    result = await document_repo.update_parse_status(
        core, tenant_id=TENANT_ID, doc_id=DOC_ID, parse_status="ready",
        chunk_count=5,
    )

    assert result is True
    sql = core.data_query.call_args.kwargs["sql"]
    assert "parsed_at = now()" in sql
    assert "chunk_count = COALESCE" in sql
    params = core.data_query.call_args.kwargs["params"]
    assert params[0] == DOC_ID
    assert params[1] == "ready"
    assert params[3] == 5


@pytest.mark.asyncio
async def test_update_parse_status_non_ready_no_parsed_at():
    core = _mock_core(query_return={"rowcount": 1})

    await document_repo.update_parse_status(
        core, tenant_id=TENANT_ID, doc_id=DOC_ID, parse_status="failed",
        error_message="oops",
    )

    sql = core.data_query.call_args.kwargs["sql"]
    assert "parsed_at" not in sql


@pytest.mark.asyncio
async def test_update_parse_status_returns_false_when_zero():
    core = _mock_core(query_return={"rowcount": 0})

    result = await document_repo.update_parse_status(
        core, tenant_id=TENANT_ID, doc_id=DOC_ID, parse_status="ready",
    )

    assert result is False


@pytest.mark.asyncio
async def test_soft_delete_document_sets_failed_with_deleted_message():
    core = _mock_core(query_return={"rowcount": 1})

    result = await document_repo.soft_delete_document(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
    )

    assert result is True
    sql = core.data_query.call_args.kwargs["sql"]
    assert "parse_status = 'failed'" in sql
    assert "error_message = 'deleted'" in sql
    assert core.data_query.call_args.kwargs["params"] == [DOC_ID, KB_ID]


# ── chunk repository ─────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_get_chunk_returns_row():
    row = {"id": CHUNK_ID, "content": "hello"}
    core = _mock_core(query_return={"rows": [row]})

    result = await chunk_repo.get_chunk(
        core, tenant_id=TENANT_ID, chunk_id=CHUNK_ID,
    )

    assert result == row
    assert core.data_query.call_args.kwargs["params"] == [CHUNK_ID]


@pytest.mark.asyncio
async def test_get_chunk_returns_none_when_not_found():
    core = _mock_core(query_return={"rows": []})

    result = await chunk_repo.get_chunk(
        core, tenant_id=TENANT_ID, chunk_id=CHUNK_ID,
    )

    assert result is None


@pytest.mark.asyncio
async def test_list_chunks_by_doc():
    rows = [{"id": CHUNK_ID, "content": "c1"}]
    core = _mock_core(query_return={"rows": rows})

    result = await chunk_repo.list_chunks_by_doc(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, limit=50,
    )

    assert result == rows
    sql = core.data_query.call_args.kwargs["sql"]
    assert "ORDER BY created_at ASC" in sql
    assert core.data_query.call_args.kwargs["params"] == [KB_ID, DOC_ID, 50]


@pytest.mark.asyncio
async def test_keyword_search_preserves_pg_trgm_similarity():
    """AC: keyword_search uses data_query with similarity() + ILIKE (pg_trgm)."""
    rows = [{"id": CHUNK_ID, "content": "hello", "rank": 0.8}]
    core = _mock_core(query_return={"rows": rows})

    result = await chunk_repo.keyword_search(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, query="hel", limit=5,
    )

    assert result == rows
    sql = core.data_query.call_args.kwargs["sql"]
    # pg_trgm semantics preserved (SPEC §4.2)
    assert "similarity(content, $1)" in sql
    assert "content ILIKE '%' || $1 || '%'" in sql
    assert "ORDER BY rank DESC" in sql
    assert core.data_query.call_args.kwargs["params"] == ["hel", KB_ID, 5]


@pytest.mark.asyncio
async def test_count_chunks_by_doc():
    core = _mock_core(query_return={"rows": [{"total": 42}]})

    result = await chunk_repo.count_chunks_by_doc(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
    )

    assert result == 42
    sql = core.data_query.call_args.kwargs["sql"]
    assert "count(*)" in sql
    assert core.data_query.call_args.kwargs["params"] == [KB_ID, DOC_ID]


@pytest.mark.asyncio
async def test_count_chunks_by_doc_returns_zero_when_no_rows():
    core = _mock_core(query_return={"rows": []})

    result = await chunk_repo.count_chunks_by_doc(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
    )

    assert result == 0


# ── cross-cutting: rls.py not imported by these repos ────────────────────────


def test_knowledge_base_repo_does_not_import_rls():
    """AC: rls.py is no longer used by knowledge_base.py (SPEC §4.2)."""
    import app.repositories.knowledge_base as m
    assert not hasattr(m, "set_tenant_context")
    assert "asyncpg" not in dir(m)


def test_document_repo_does_not_import_rls():
    """AC: rls.py is no longer used by document.py (SPEC §4.2)."""
    import app.repositories.document as m
    assert not hasattr(m, "set_tenant_context")
    assert "asyncpg" not in dir(m)


def test_chunk_repo_does_not_import_rls():
    """AC: rls.py is no longer used by chunk.py (SPEC §4.2)."""
    import app.repositories.chunk as m
    assert not hasattr(m, "set_tenant_context")
    assert "asyncpg" not in dir(m)
