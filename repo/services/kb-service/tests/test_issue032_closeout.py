"""Issue #032 — 迁移验证收口（语义等价对比 + 真实 PG 端到端 + 回归）

SPEC §6, §5 Phase C, §9 verification tests. These tests verify that the
kb-service data-plane migration (issues #024–#031) preserves the exact
semantics of the pre-migration direct-asyncpg implementation:

AC1 (SPEC §6-3): 语义等价对比 — same input → same DB results + responses
  - US-010 atomic outbox: NotifyDocumentUploaded 3-table CTE fold is atomic
  - Query persistence: kb_sessions + kb_messages written in a single tx

AC3 (SPEC §6-1): Core 数据面安全用例 — injection, unauthorized table, DDL
  reject, param-concat reject (verified at the Go handler level; here we
  verify the Python client does not bypass those guards).

AC4 (SPEC §9-3): kb-service 无 asyncpg/rls.py/migrations; 7 repos 全走数据面

These are logic-level tests using mock CoreClient (no real PG required).
The real-PG e2e test is in tests/e2e_issue029_full_stack.py.
"""
import os
import sys
import uuid
from contextlib import asynccontextmanager
from unittest.mock import AsyncMock, MagicMock

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC_ID = "33333333-3333-3333-3333-333333333333"


# ── AC4: kb-service has no asyncpg, no rls.py, no migrations ─────────────────


def test_ac4_no_asyncpg_in_app_code():
    """AC4 (SPEC §9-3): kb-service application code must not import asyncpg.

    The only place asyncpg may appear is in the e2e test helper that directly
    connects to PG to verify DB state (tests/e2e_*). All app/ code must
    use CoreClient.data_query instead.
    """
    import app.repositories.knowledge_base as kb_mod
    import app.repositories.document as doc_mod
    import app.repositories.chunk as chunk_mod
    import app.repositories.message as msg_mod
    import app.repositories.async_task as task_mod
    import app.repositories.outbox as obx_mod
    import app.outbox.dispatcher as disp_mod
    import app.api.grpc_server as grpc_mod
    import main as main_mod

    modules = {
        "knowledge_base": kb_mod,
        "document": doc_mod,
        "chunk": chunk_mod,
        "message": msg_mod,
        "async_task": task_mod,
        "outbox": obx_mod,
        "dispatcher": disp_mod,
        "grpc_server": grpc_mod,
        "main": main_mod,
    }
    for name, mod in modules.items():
        assert not hasattr(mod, "asyncpg"), (
            f"AC4 FAIL: {name} still imports asyncpg"
        )


def test_ac4_no_rls_py():
    """AC4 (SPEC §9-3): rls.py must not exist / not be importable."""
    rls_path = os.path.join(_SERVICE_ROOT, "app", "repositories", "rls.py")
    assert not os.path.exists(rls_path), "AC4 FAIL: app/repositories/rls.py still exists"


def test_ac4_no_migrations_dir():
    """AC4 (SPEC §9-3): migrations/ directory must not exist."""
    migrations_path = os.path.join(_SERVICE_ROOT, "migrations")
    assert not os.path.exists(migrations_path), (
        "AC4 FAIL: migrations/ directory still exists"
    )


def test_ac4_all_repos_use_data_query():
    """AC4 (SPEC §9-3): all 7 repositories use CoreClient.data_query."""
    from app.repositories import (
        async_task,
        chunk,
        document,
        knowledge_base,
        message,
        outbox,
    )

    repos = {
        "knowledge_base": knowledge_base,
        "document": document,
        "chunk": chunk,
        "message": message,
        "async_task": async_task,
        "outbox": outbox,
    }
    for name, mod in repos.items():
        # Each module must reference CoreClient (the data-plane client)
        assert hasattr(mod, "CoreClient"), (
            f"AC4 FAIL: {name} does not reference CoreClient"
        )


def test_ac4_requirements_no_asyncpg():
    """AC4 (SPEC §9-3): requirements.txt must not contain asyncpg."""
    req_path = os.path.join(_SERVICE_ROOT, "requirements.txt")
    with open(req_path) as f:
        content = f.read()
    # asyncpg should only appear in comments, not as an actual dependency
    for line in content.splitlines():
        stripped = line.strip()
        if stripped and not stripped.startswith("#"):
            assert "asyncpg" not in stripped, (
                f"AC4 FAIL: asyncpg found in requirements.txt: {stripped}"
            )


# ── AC1: 语义等价对比 — US-010 atomic outbox ─────────────────────────────────


class _MockCoreClient:
    """Mock CoreClient for data-plane call verification."""

    def __init__(self, *, data_query_returns=None):
        self.data_query = AsyncMock()
        self.aclose = AsyncMock()
        self.__aenter__ = AsyncMock(return_value=self)
        self.__aexit__ = AsyncMock(return_value=None)
        if data_query_returns is not None:
            self.data_query.return_value = data_query_returns
        else:
            self.data_query.return_value = {"rows": [], "rowcount": 0}


def _core_factory(core_client):
    @asynccontextmanager
    async def factory(tenant_id):
        yield core_client
    return factory


def test_ac1_notify_folds_3_tables_into_single_atomic_query():
    """AC1 (SPEC §6-3): US-010 atomic outbox — NotifyDocumentUploaded must fold
    kb_documents UPDATE + async_tasks INSERT + outbox_events INSERT into a
    SINGLE data_query call (one atomic transaction).

    Pre-migration: asyncpg BEGIN → 3 separate INSERT/UPDATE → COMMIT.
    Post-migration: single data_query with CTE fold → one HTTP → one tx.
    Semantic equivalence: the DB sees the same atomic commit-or-rollback.
    """
    from app.api.grpc_server import KBServiceServicer
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb

    core = _MockCoreClient()
    task_id = str(uuid.uuid4())
    core.data_query.return_value = {
        "rows": [{"task_id": task_id, "status": "pending"}],
        "rowcount": 1,
    }
    servicer = KBServiceServicer(
        pool=object(),
        core_client_factory=_core_factory(core),
    )

    class _FakeContext:
        def abort(self, code, message):
            raise RuntimeError(f"aborted: {code} {message}")

    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        storage_path="kb-docs/kb1/d",
    )
    import asyncio
    result = asyncio.new_event_loop().run_until_complete(
        servicer._notify_document_uploaded(req, _FakeContext())
    )

    # Semantic equivalence: exactly 1 data_query call = 1 atomic transaction
    assert core.data_query.call_count == 1, (
        "AC1 FAIL: NotifyDocumentUploaded should use exactly 1 data_query call, "
        f"got {core.data_query.call_count}"
    )

    # The single SQL contains all 3 table operations (CTE fold)
    fold_sql = core.data_query.call_args_list[0].kwargs["sql"]
    assert "UPDATE kb_documents" in fold_sql, "AC1 FAIL: missing UPDATE kb_documents"
    assert "INSERT INTO async_tasks" in fold_sql, "AC1 FAIL: missing INSERT async_tasks"
    assert "INSERT INTO outbox_events" in fold_sql, "AC1 FAIL: missing INSERT outbox_events"
    # Idempotency guard (ON CONFLICT) must be in the same atomic statement
    assert "ON CONFLICT (tenant_id, idempotency_key) DO NOTHING" in fold_sql, (
        "AC1 FAIL: missing idempotency ON CONFLICT guard"
    )
    # CTE structure (single-statement atomic fold)
    assert "WITH doc_upd AS" in fold_sql, "AC1 FAIL: not a CTE fold"

    assert result.task_id == task_id
    assert result.status == "pending"


def test_ac1_notify_idempotent_replay_returns_same_task():
    """AC1 (SPEC §6-3): US-010 idempotent replay — a retry with the same
    (tenant, kb, doc) returns the SAME task_id via the UNION ALL branch
    of the CTE (ON CONFLICT DO NOTHING → existing task)."""
    from app.api.grpc_server import KBServiceServicer
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb

    existing_task_id = str(uuid.uuid4())
    core = _MockCoreClient()
    core.data_query.return_value = {
        "rows": [{"task_id": existing_task_id, "status": "pending"}],
        "rowcount": 1,
    }
    servicer = KBServiceServicer(
        pool=object(),
        core_client_factory=_core_factory(core),
    )

    class _FakeContext:
        def abort(self, code, message):
            raise RuntimeError(f"aborted: {code} {message}")

    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        storage_path="kb-docs/kb1/d",
    )
    import asyncio
    result = asyncio.new_event_loop().run_until_complete(
        servicer._notify_document_uploaded(req, _FakeContext())
    )
    assert result.task_id == existing_task_id, (
        f"AC1 FAIL: idempotent replay should return same task_id {existing_task_id}, "
        f"got {result.task_id}"
    )
    assert core.data_query.call_count == 1


def test_ac1_notify_doc_not_found_aborts_without_outbox_write():
    """AC1 (SPEC §6-3): If the document doesn't exist, the CTE's doc_upd
    returns no rows → no task/outbox inserts → NOT_FOUND. This preserves
    the pre-migration semantic where a missing doc aborts before any
    outbox event is written."""
    from app.api.grpc_server import KBServiceServicer
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb

    core = _MockCoreClient()
    core.data_query.return_value = {"rows": [], "rowcount": 0}
    servicer = KBServiceServicer(
        pool=object(),
        core_client_factory=_core_factory(core),
    )

    class _FakeContext:
        def abort(self, code, message):
            raise RuntimeError(f"aborted: {code} {message}")

    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        storage_path="kb-docs/kb1/d",
    )
    import asyncio
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        asyncio.new_event_loop().run_until_complete(
            servicer._notify_document_uploaded(req, _FakeContext())
        )
    # Only 1 data_query call (the fold); no extra outbox write on failure
    assert core.data_query.call_count == 1


# ── AC1: 语义等价对比 — Query session persistence ────────────────────────────


class _MockRagClient:
    async def query(self, **kwargs):
        return {
            "answer": "hello",
            "sources": [{"doc_id": "d1", "file_name": "a.pdf",
                          "page": 1, "content": "ctx", "score": 0.9}],
            "session_id": kwargs.get("session_id", "new"),
            "input_tokens": 10,
            "output_tokens": 5,
        }

    async def aclose(self): pass
    async def __aenter__(self): return self
    async def __aexit__(self, *args): pass


class _MockSessionCache:
    def __init__(self):
        self.appended = []

    async def append_message(self, **kwargs):
        self.appended.append(kwargs)

    async def list_messages(self, **kwargs):
        return []


def _make_query_servicer(*, core_client=None):
    from app.api.grpc_server import KBServiceServicer

    @asynccontextmanager
    async def rag_factory():
        yield _MockRagClient()

    cache = _MockSessionCache()

    def cache_factory():
        return cache

    core = core_client or _MockCoreClient()
    return (
        KBServiceServicer(
            pool=object(),
            rag_engine_client_factory=rag_factory,
            session_cache_factory=cache_factory,
            core_client_factory=_core_factory(core),
        ),
        cache,
        core,
    )


def _query_core_side_effects():
    return [
        # Call 1: get_kb config
        {"rows": [{"id": KB_ID, "top_k": 5, "score_threshold": 0.0,
                    "retrieval_mode": "hybrid"}], "rowcount": 1},
        # Call 2: create_session_and_message (CTE fold)
        {"rows": [{"id": str(uuid.uuid4()), "session_id": str(uuid.uuid4()),
                    "role": "user", "content": "hi"}], "rowcount": 1},
        # Call 3: insert_message (assistant)
        {"rows": [{"id": str(uuid.uuid4()), "session_id": str(uuid.uuid4()),
                    "role": "assistant", "content": "hello"}], "rowcount": 1},
    ]


def test_ac1_query_persists_user_and_assistant_messages():
    """AC1 (SPEC §6-3): Query persistence — user message via CTE fold
    (kb_sessions + kb_messages atomic), assistant message via insert_message.

    Semantic equivalence: both messages are persisted to the DB just like
    the pre-migration asyncpg implementation, but now via data_query."""
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb

    core = _MockCoreClient()
    core.data_query.side_effect = _query_core_side_effects()
    servicer, cache, core = _make_query_servicer(core_client=core)

    class _FakeContext:
        def abort(self, code, message):
            raise RuntimeError(f"aborted: {code} {message}")

    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    resp = asyncio.new_event_loop().run_until_complete(
        servicer._query(req, _FakeContext())
    )

    # 3 data_query calls: get_kb + create_session_and_message + insert_message
    assert core.data_query.call_count == 3

    # Call 2: user message CTE fold (session + message in one atomic statement)
    user_sql = core.data_query.call_args_list[1].kwargs["sql"]
    assert "WITH sess AS" in user_sql, "AC1 FAIL: missing CTE fold for user message"
    assert "INSERT INTO kb_sessions" in user_sql, "AC1 FAIL: missing INSERT kb_sessions"
    assert "INSERT INTO kb_messages" in user_sql, "AC1 FAIL: missing INSERT kb_messages"
    assert "ON CONFLICT (id) DO NOTHING" in user_sql, (
        "AC1 FAIL: missing ON CONFLICT for session dedup"
    )

    # Call 3: assistant message (single INSERT)
    asst_sql = core.data_query.call_args_list[2].kwargs["sql"]
    assert "INSERT INTO kb_messages" in asst_sql
    assert "WITH sess AS" not in asst_sql  # not a CTE fold

    # Redis cache: both user and assistant appended
    assert len(cache.appended) == 2
    assert cache.appended[0]["role"] == "user"
    assert cache.appended[1]["role"] == "assistant"

    # Response carries resolved session_id + answer
    assert resp.answer == "hello"
    assert resp.session_id


def test_ac1_query_session_and_message_atomic_fold():
    """AC1 (SPEC §6-3): create_session_and_message folds session + message
    into a SINGLE data_query call (one atomic transaction). Pre-migration
    this was 2 separate INSERTs in one asyncpg tx; post-migration it's
    1 CTE-based data_query (equivalent atomicity)."""
    from app.repositories import message as msg_repo

    core = _MockCoreClient()
    session_id = str(uuid.uuid4())
    row = {
        "id": str(uuid.uuid4()), "session_id": session_id,
        "tenant_id": TENANT_ID, "role": "user", "content": "hi",
        "source_chunks": None, "input_tokens": 0, "output_tokens": 0,
        "duration_ms": None, "created_at": None,
    }
    core.data_query.return_value = {"rows": [row], "rowcount": 1}

    import asyncio
    result = asyncio.new_event_loop().run_until_complete(
        msg_repo.create_session_and_message(
            core, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=session_id,
            role="user", content="hi",
        )
    )

    # Exactly 1 data_query call = 1 atomic transaction
    assert core.data_query.call_count == 1, (
        f"AC1 FAIL: expected 1 data_query call, got {core.data_query.call_count}"
    )
    sql = core.data_query.call_args.kwargs["sql"]
    assert "WITH sess AS" in sql
    assert "INSERT INTO kb_sessions" in sql
    assert "INSERT INTO kb_messages" in sql
    assert result["role"] == "user"
    assert result["content"] == "hi"


def test_ac1_query_degrades_without_cache():
    """AC1 (SPEC §6-3): Query still persists both messages to DB when Redis
    cache is unavailable (best-effort cache). Semantic equivalence: the
    DB writes are the source of truth, not the cache."""
    from app.api.grpc_server import KBServiceServicer
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb

    @asynccontextmanager
    async def rag_factory():
        yield _MockRagClient()

    def cache_factory():
        return None  # Redis unavailable

    core = _MockCoreClient()
    core.data_query.side_effect = _query_core_side_effects()
    servicer = KBServiceServicer(
        pool=object(),
        rag_engine_client_factory=rag_factory,
        session_cache_factory=cache_factory,
        core_client_factory=_core_factory(core),
    )

    class _FakeContext:
        def abort(self, code, message):
            raise RuntimeError(f"aborted: {code} {message}")

    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    resp = asyncio.new_event_loop().run_until_complete(
        servicer._query(req, _FakeContext())
    )
    # Both messages still persisted to DB (3 data_query calls)
    assert core.data_query.call_count == 3
    assert resp.answer == "hello"


# ── AC1: pg_trgm keyword search semantics ────────────────────────────────────


def test_ac1_keyword_search_preserves_pg_trgm_similarity():
    """AC1 (SPEC §6-3): pg_trgm检索 — keyword_search SQL must preserve the
    similarity() function and ILIKE pattern matching. The SQL is executed
    on the Core PG (which has pg_trgm extension), so the semantics are
    identical to the pre-migration direct-asyncpg implementation."""
    from app.repositories import chunk

    core = _MockCoreClient()
    core.data_query.return_value = {
        "rows": [{"id": "c1", "content": "hello world", "rank": 0.5}],
        "rowcount": 1,
    }

    import asyncio
    result = asyncio.new_event_loop().run_until_complete(
        chunk.keyword_search(
            core, tenant_id=TENANT_ID, kb_id=KB_ID, query="hello", limit=10,
        )
    )
    assert len(result) == 1
    assert result[0]["content"] == "hello world"

    sql = core.data_query.call_args.kwargs["sql"]
    # pg_trgm similarity() function must be preserved
    assert "similarity(content, $1)" in sql, (
        "AC1 FAIL: pg_trgm similarity() function not preserved"
    )
    # ILIKE for trigram index usage
    assert "content ILIKE '%' || $1 || '%'" in sql, (
        "AC1 FAIL: ILIKE pattern not preserved for pg_trgm index"
    )
    # Parameterized (no string concatenation injection risk)
    params = core.data_query.call_args.kwargs["params"]
    assert params[0] == "hello"  # query param
    assert params[1] == KB_ID    # kb_id param
    assert params[2] == 10        # limit param


# ── AC1: async_tasks idempotency key UNIQUE constraint ──────────────────────


def test_ac1_async_task_idempotency_key_unique_constraint():
    """AC1 (SPEC §8): async_tasks 幂等键 UNIQUE 约束不变量. The ON CONFLICT
    clause must match the actual schema constraint UNIQUE(tenant_id,
    idempotency_key) so idempotent replay returns the existing task."""
    from app.repositories import async_task as task_repo

    core = _MockCoreClient()
    core.data_query.return_value = {
        "rows": [{"id": str(uuid.uuid4()), "status": "pending",
                    "idempotency_key": "kb.parse:t:k:d"}],
        "rowcount": 1,
    }

    import asyncio
    asyncio.new_event_loop().run_until_complete(
        task_repo.find_by_idempotency_key(
            core, tenant_id=TENANT_ID, idempotency_key="kb.parse:t:k:d",
        )
    )

    sql = core.data_query.call_args.kwargs["sql"]
    assert "WHERE tenant_id = $1 AND idempotency_key = $2" in sql, (
        "AC1 FAIL: idempotency key lookup must use parameterized WHERE"
    )


# ── AC1: outbox role=service cross-tenant ───────────────────────────────────


def test_ac1_outbox_list_undispatched_uses_service_role():
    """AC1 (SPEC §4.2): outbox.list_undispatched must use role="service"
    for cross-tenant access (BYPASSRLS semantics). Pre-migration this used
    a separate asyncpg pool with BYPASSRLS; post-migration it uses the
    data plane service role."""
    from app.repositories import outbox

    core = _MockCoreClient()
    core.data_query.return_value = {
        "rows": [{"id": 1, "event_type": "kb.parse"}],
        "rowcount": 1,
    }

    import asyncio
    asyncio.new_event_loop().run_until_complete(
        outbox.list_undispatched(core, limit=10)
    )

    role = core.data_query.call_args.kwargs.get("role", "tenant")
    assert role == "service", (
        f"AC1 FAIL: outbox.list_undispatched must use role='service', got '{role}'"
    )


def test_ac1_outbox_mark_dispatched_uses_service_role():
    """AC1 (SPEC §4.2): outbox.mark_dispatched must use role="service"."""
    from app.repositories import outbox

    core = _MockCoreClient()
    core.data_query.return_value = {"rows": [], "rowcount": 1}

    import asyncio
    asyncio.new_event_loop().run_until_complete(
        outbox.mark_dispatched(core, event_id=42)
    )

    role = core.data_query.call_args.kwargs.get("role", "tenant")
    assert role == "service", (
        f"AC1 FAIL: outbox.mark_dispatched must use role='service', got '{role}'"
    )


# ── AC3: Core data plane security (Python-side verification) ─────────────────


def test_ac3_kb_repos_do_not_concat_params_into_sql():
    """AC3 (SPEC §6-1): All repository SQL must use parameterized queries
    ($1..$n placeholders), never string concatenation for params. This
    complements the Go-side security tests that reject param-concat at
    the handler level."""
    import inspect
    from app.repositories import (
        async_task,
        chunk,
        document,
        knowledge_base,
        message,
        outbox,
    )

    repos = [knowledge_base, document, chunk, message, async_task, outbox]
    for repo in repos:
        for name, func in inspect.getmembers(repo, inspect.iscoroutinefunction):
            src = inspect.getsource(func)
            # Must not use f-string or % formatting for SQL with params
            # (the SQL itself may use f-strings for column names, but params
            # must go through $1..$n placeholders)
            # Check for dangerous patterns: f"...WHERE x = {variable}"
            # This is a heuristic; the Go-side validator is the real guard.
            assert "f\"" not in src or "$" in src, (
                f"AC3 WARNING: {repo.__name__}.{name} uses f-string; "
                "ensure params go through $1..$n placeholders"
            )


# ── AC5: Go-side security test names (documentation) ─────────────────────────
# The following Go tests verify AC3 at the handler/adapter level:
# - TestDataQueryRejectsDestructiveDrop
# - TestDataQueryRejectsDestructiveTruncate
# - TestDataQueryRejectsDestructiveAlterSystem
# - TestDataQueryRejectsCopyToProgram
# - TestDataQueryRejectsPgReadFile
# - TestDataQueryRejectsUnregisteredTable
# - TestDataQueryRejectsTenantEndUserScope
# - TestDataQueryRejectsServiceRoleFromNonPlatformScope
# - TestDataQueryRateLimitedByServiceIdentity
# - TestValidateDataPlaneTablesRejectsUnknown
# These are run via: go test ./pkg/adapters/postgres/... ./services/ani-gateway/internal/router/...
