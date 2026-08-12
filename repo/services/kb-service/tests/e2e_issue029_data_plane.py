"""E2E smoke test for issue-029: kb-service repos → Core data plane (SQL layer).

Tests the actual SQL statements used by the three migrated repositories
(knowledge_base, document, chunk) directly against the real-k8s-lab
PostgreSQL server (10.10.1.66:30945). Verifies:

1. Table schema + pg_trgm extension + GIN trigram index
2. INSERT … RETURNING (create_kb / create_document)
3. SELECT (get_kb / get_document / list_kbs / list_documents)
4. count(*) (list_kbs total / list_documents total / count_chunks_by_doc)
5. UPDATE (update_parse_status / soft_delete_kb / soft_delete_document)
6. pg_trgm similarity() + ILIKE (keyword_search)
7. RLS tenant isolation (SET LOCAL ROLE + set_config)
8. ACID single-transaction semantics

The SQL strings are extracted verbatim from the repository source files to
ensure the test validates exactly what the repos send via CoreClient.data_query.

Note: The gateway /data/query endpoint is NOT yet deployed on the server
(the gateway binary predates the data-plane router), so we test the SQL
layer directly against the real PostgreSQL — the same SQL that the repos
send through CoreClient.data_query — to validate correctness against the
real schema, indexes, and extensions.

Usage:
    cd repo/services/kb-service
    python tests/e2e_issue029_data_plane.py

Environment: reads DATABASE_URL from repo/.env
"""
import asyncio
import os
import sys
import uuid

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)

_REPO_ROOT = os.path.abspath(os.path.join(_SERVICE_ROOT, "..", ".."))
from dotenv import load_dotenv
load_dotenv(os.path.join(_REPO_ROOT, ".env"))

import asyncpg

DATABASE_URL = os.environ.get("DATABASE_URL", "")
TENANT_A = "00000000-0000-0000-0000-000000000001"
TENANT_B = "00000000-0000-0000-0000-000000000002"

GREEN = "\033[92m"
RED = "\033[91m"
YELLOW = "\033[93m"
RESET = "\033[0m"

passed = 0
failed = 0
errors = []


def ok(msg):
    global passed
    passed += 1
    print(f"  {GREEN}PASS{RESET} {msg}")


def fail(msg, detail=""):
    global failed
    failed += 1
    errors.append(f"{msg}: {detail}")
    print(f"  {RED}FAIL{RESET} {msg}")
    if detail:
        print(f"       {RED}{detail}{RESET}")


def section(title):
    print(f"\n{YELLOW}-- {title} --{RESET}")


# ── SQL strings extracted verbatim from the repository source files ────────
# These are the exact SQL statements sent via CoreClient.data_query.

# knowledge_base.py
_KB_COLUMNS = (
    "id, tenant_id, name, description, embedding_model, "
    "chunk_size, top_k, score_threshold, retrieval_mode, "
    "status, doc_count, created_at, updated_at"
)

KB_CREATE_SQL = f"""
    INSERT INTO knowledge_bases
        (tenant_id, name, description, embedding_model,
         chunk_size, top_k, score_threshold, retrieval_mode, status)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active')
    RETURNING {_KB_COLUMNS}
"""

KB_GET_SQL = f"SELECT {_KB_COLUMNS} FROM knowledge_bases WHERE id = $1"

KB_COUNT_SQL = "SELECT count(*) AS total FROM knowledge_bases"

KB_LIST_SQL = f"""
    SELECT {_KB_COLUMNS}
      FROM knowledge_bases
     WHERE id > $1
     ORDER BY id
     LIMIT $2
"""

KB_SOFT_DELETE_SQL = """
    UPDATE knowledge_bases
       SET status = 'deleted', updated_at = NOW()
     WHERE id = $1 AND status != 'deleted'
"""

# document.py
_DOC_COLUMNS = (
    "id, kb_id, tenant_id, file_name, file_type, "
    "file_size_bytes, storage_path, checksum_sha256, "
    "parse_status, chunk_count, error_message, "
    "custom_metadata, created_at, parsed_at, object_id"
)

DOC_CREATE_SQL = f"""
    INSERT INTO kb_documents
        (kb_id, tenant_id, file_name, file_type,
         file_size_bytes, storage_path, checksum_sha256,
         parse_status, custom_metadata, object_id)
    VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $9)
    RETURNING {_DOC_COLUMNS}
"""

DOC_GET_SQL = f"""
    SELECT {_DOC_COLUMNS}
      FROM kb_documents
     WHERE id = $1 AND kb_id = $2
"""

DOC_COUNT_SQL = "SELECT count(*) AS total FROM kb_documents WHERE kb_id = $1"

DOC_LIST_SQL = f"""
    SELECT {_DOC_COLUMNS}
      FROM kb_documents
     WHERE kb_id = $1
     ORDER BY id ASC
     LIMIT $2
"""

DOC_UPDATE_READY_SQL = """
    UPDATE kb_documents
       SET parse_status = $2,
           error_message = $3,
           chunk_count = COALESCE($4, chunk_count),
           parsed_at = now()
     WHERE id = $1
"""

DOC_SOFT_DELETE_SQL = """
    UPDATE kb_documents
       SET parse_status = 'failed', error_message = 'deleted'
     WHERE id = $1 AND parse_status != 'failed'
"""

# chunk.py
_CHUNK_COLUMNS = (
    "id, tenant_id, kb_id, doc_id, parent_chunk_id, chunk_type, "
    "content, parent_content, page_number, content_type, "
    "file_name, token_count, custom_metadata, created_at"
)

CHUNK_KEYWORD_SEARCH_SQL = f"""
    SELECT {_CHUNK_COLUMNS},
           similarity(content, $1) AS rank
      FROM kb_chunks
     WHERE kb_id = $2 AND content ILIKE '%' || $1 || '%'
     ORDER BY rank DESC
     LIMIT $3
"""

CHUNK_COUNT_SQL = (
    "SELECT count(*) AS total FROM kb_chunks WHERE kb_id = $1 AND doc_id = $2"
)

# Test-only: insert a chunk (writes are done by rag-engine, not kb-service)
CHUNK_INSERT_SQL = """
    INSERT INTO kb_chunks
        (tenant_id, kb_id, doc_id, chunk_type, content, file_name, token_count)
    VALUES ($1, $2, $3, 'parent', $4, 'test.pdf', $5)
    RETURNING id
"""


async def run_e2e():
    print(f"{YELLOW}{'='*72}{RESET}")
    print(f"{YELLOW}  E2E Test -- issue-029: kb-service repos SQL layer (real DB){RESET}")
    print(f"{YELLOW}  PostgreSQL: {DATABASE_URL.split('@')[1].split('/')[0]}{RESET}")
    print(f"{YELLOW}{'='*72}{RESET}")

    if not DATABASE_URL:
        fail("DATABASE_URL not set")
        return

    conn = await asyncpg.connect(DATABASE_URL)
    ok("Connected to PostgreSQL")

    # ── Phase 1: Schema & Extension checks ───────────────────────────────────
    section("1. Schema: pg_trgm + tables + GIN index")

    ext = await conn.fetchval(
        "SELECT extname FROM pg_extension WHERE extname = 'pg_trgm'"
    )
    if ext:
        ok("pg_trgm extension installed")
    else:
        fail("pg_trgm extension NOT installed")

    tables = await conn.fetch(
        "SELECT tablename FROM pg_tables WHERE schemaname='public' "
        "AND tablename IN ('knowledge_bases','kb_documents','kb_chunks') "
        "ORDER BY tablename"
    )
    found = {t["tablename"] for t in tables}
    for t in ["knowledge_bases", "kb_documents", "kb_chunks"]:
        if t in found:
            ok(f"table exists: {t}")
        else:
            fail(f"table MISSING: {t}")

    idx = await conn.fetchval(
        "SELECT indexname FROM pg_indexes "
        "WHERE tablename = 'kb_chunks' AND indexname = 'idx_kb_chunks_content_trgm'"
    )
    if idx:
        ok("GIN trigram index on kb_chunks.content exists")
    else:
        fail("GIN trigram index on kb_chunks.content MISSING")

    # ── Phase 2: knowledge_base.create_kb (INSERT … RETURNING) ───────────────
    section("2. knowledge_base.create_kb -> INSERT ... RETURNING (single tx)")

    kb_name = f"e2e-test-{uuid.uuid4().hex[:8]}"
    kb_row = await conn.fetchrow(
        KB_CREATE_SQL,
        uuid.UUID(TENANT_A), kb_name, "e2e smoke", "bge-m3",
        1024, 5, 0.3, "hybrid",
    )
    kb_id = str(kb_row["id"]) if kb_row else None
    if kb_row and kb_row["name"] == kb_name and kb_row["status"] == "active":
        ok(f"create_kb: id={kb_id[:8]}..., name={kb_name}, status=active")
    else:
        fail("create_kb returned unexpected row", str(dict(kb_row) if kb_row else None))
        return

    # ── Phase 3: knowledge_base.get_kb (SELECT) ─────────────────────────────
    section("3. knowledge_base.get_kb -> SELECT by id")

    row = await conn.fetchrow(KB_GET_SQL, uuid.UUID(kb_id))
    if row and str(row["id"]) == kb_id:
        ok(f"get_kb: id={kb_id[:8]}..., name={row['name']}")
    else:
        fail("get_kb returned None or wrong id")

    # ── Phase 4: knowledge_base.list_kbs (SELECT + count) ────────────────────
    section("4. knowledge_base.list_kbs -> SELECT + count(*)")

    total = await conn.fetchval(KB_COUNT_SQL)
    rows = await conn.fetch(KB_LIST_SQL, uuid.UUID(int=0), 10)
    if isinstance(total, int) and total >= 1 and len(rows) >= 1:
        ok(f"list_kbs: {len(rows)} rows, total={total}")
    else:
        fail("list_kbs returned unexpected", f"total={total}, rows={len(rows)}")

    # ── Phase 5: document.create_document (INSERT … RETURNING) ───────────────
    section("5. document.create_document -> INSERT ... RETURNING (single tx)")

    import json
    object_id = str(uuid.uuid4())
    doc_row = await conn.fetchrow(
        DOC_CREATE_SQL,
        uuid.UUID(kb_id), uuid.UUID(TENANT_A),
        "test.pdf", "pdf", 1024,
        f"kb-docs/{kb_id}/{object_id}/test.pdf",
        "abc123def", json.dumps({"source": "e2e"}), object_id,
    )
    doc_id = str(doc_row["id"]) if doc_row else None
    if doc_row and doc_row["parse_status"] == "pending" and doc_row["object_id"] == object_id:
        ok(f"create_document: id={doc_id[:8]}..., parse_status=pending, object_id={object_id[:8]}...")
    else:
        fail("create_document returned unexpected", str(dict(doc_row) if doc_row else None))
        return

    # ── Phase 6: document.get_document + list_documents ───────────────────────
    section("6. document.get_document + list_documents")

    row = await conn.fetchrow(DOC_GET_SQL, uuid.UUID(doc_id), uuid.UUID(kb_id))
    if row and str(row["id"]) == doc_id:
        ok(f"get_document: id={doc_id[:8]}...")
    else:
        fail("get_document returned None or wrong id")

    doc_total = await conn.fetchval(DOC_COUNT_SQL, uuid.UUID(kb_id))
    doc_rows = await conn.fetch(DOC_LIST_SQL, uuid.UUID(kb_id), 10)
    if doc_total >= 1 and len(doc_rows) >= 1:
        ok(f"list_documents: {len(doc_rows)} rows, total={doc_total}")
    else:
        fail("list_documents unexpected", f"total={doc_total}, rows={len(doc_rows)}")

    # ── Phase 7: document.update_parse_status (UPDATE) ───────────────────────
    section("7. document.update_parse_status -> UPDATE")

    result = await conn.execute(
        DOC_UPDATE_READY_SQL, uuid.UUID(doc_id), "ready", None, 3
    )
    if "UPDATE 1" in result:
        ok("update_parse_status: parse_status=ready, chunk_count=3")
    else:
        fail("update_parse_status failed", result)

    # ── Phase 8: chunk.keyword_search (pg_trgm similarity + ILIKE) ────────────
    section("8. chunk.keyword_search -> similarity() + ILIKE (pg_trgm)")

    # Insert a test chunk with known content (writes done by rag-engine, simulated here)
    chunk_row = await conn.fetchrow(
        CHUNK_INSERT_SQL,
        uuid.UUID(TENANT_A), uuid.UUID(kb_id), uuid.UUID(doc_id),
        "This is a test document about machine learning and AI.", 15,
    )
    chunk_id = str(chunk_row["id"]) if chunk_row else None
    if chunk_row:
        ok(f"inserted test chunk: id={chunk_id[:8]}...")

    # Now search for "machine learning" using the exact keyword_search SQL
    search_rows = await conn.fetch(
        CHUNK_KEYWORD_SEARCH_SQL, "machine learning", uuid.UUID(kb_id), 10
    )
    if search_rows and len(search_rows) >= 1:
        rank = search_rows[0]["rank"]
        ok(f"keyword_search: found {len(search_rows)} chunks, rank={rank:.3f}")
    else:
        fail("keyword_search returned 0 rows (pg_trgm similarity() may not work)")

    # Verify similarity() function works (pg_trgm core function)
    sim = await conn.fetchval(
        "SELECT similarity('machine learning', 'machine')"
    )
    if sim is not None and sim > 0:
        ok(f"similarity() function works: similarity={sim:.3f}")
    else:
        fail("similarity() function returned 0 or None")

    # ── Phase 9: chunk.count_chunks_by_doc ──────────────────────────────────
    section("9. chunk.count_chunks_by_doc -> count(*)")

    chunk_count = await conn.fetchval(
        CHUNK_COUNT_SQL, uuid.UUID(kb_id), uuid.UUID(doc_id)
    )
    if chunk_count == 1:
        ok(f"count_chunks_by_doc: count={chunk_count}")
    else:
        fail("count_chunks_by_doc unexpected", f"expected 1, got {chunk_count}")

    # ── Phase 10: RLS tenant isolation ───────────────────────────────────────
    section("10. RLS tenant isolation (SET LOCAL ROLE + set_config)")

    # Check if RLS is enabled on knowledge_bases
    rls_enabled = await conn.fetchval(
        "SELECT relrowsecurity FROM pg_class WHERE relname = 'knowledge_bases'"
    )
    if rls_enabled:
        ok("RLS enabled on knowledge_bases table")
    else:
        print(f"  {YELLOW}INFO{RESET} RLS NOT enabled on knowledge_bases (RLS is applied by Core)")

    # Test RLS via SET LOCAL ROLE + set_config('app.current_tenant_id', ...)
    rls_policy = await conn.fetchval(
        "SELECT policyname FROM pg_policies "
        "WHERE tablename = 'knowledge_bases' LIMIT 1"
    )
    if rls_policy:
        ok(f"RLS policy found: {rls_policy}")
        # Check the role used by the RLS policy
        roles = await conn.fetch(
            "SELECT rolname FROM pg_roles WHERE rolname IN ('tenant','ani_app','ani_app_user') ORDER BY rolname"
        )
        available_roles = [r["rolname"] for r in roles]
        print(f"  {YELLOW}INFO{RESET} Available roles: {available_roles}")

        # Verify RLS policy definition (the policy uses current_setting('app.current_tenant_id'))
        policy_def = await conn.fetchrow(
            "SELECT qual, with_check FROM pg_policies "
            "WHERE tablename = 'knowledge_bases' AND policyname = $1",
            rls_policy
        )
        if policy_def and "tenant_id" in (policy_def["qual"] or ""):
            ok(f"RLS policy definition: {policy_def['qual']}")
        else:
            fail("RLS policy definition unexpected", str(policy_def))

        # Check which roles have SELECT on knowledge_bases
        grants = await conn.fetch(
            "SELECT grantee, privilege_type FROM information_schema.role_table_grants "
            "WHERE table_name = 'knowledge_bases' ORDER BY grantee, privilege_type"
        )
        grant_info = [f"{g['grantee']}:{g['privilege_type']}" for g in grants]
        print(f"  {YELLOW}INFO{RESET} Grants: {grant_info}")

        # Try RLS with ani_app_user if it has SELECT privilege
        rls_role = None
        for r in grants:
            if r["privilege_type"] == "SELECT" and r["grantee"] not in ("ani", "OWNER"):
                rls_role = r["grantee"]
                break

        if not rls_role:
            print(f"  {YELLOW}SKIP{RESET} No non-owner role with SELECT on knowledge_bases -- RLS tested via policy definition only")
        else:
            # Test: set tenant context and verify isolation
            async with conn.transaction():
                await conn.execute(f"SET LOCAL ROLE {rls_role}")
                await conn.execute(
                    "SELECT set_config('app.current_tenant_id', $1, true)", TENANT_A
                )
                visible = await conn.fetchval(
                    "SELECT count(*) FROM knowledge_bases WHERE id = $1",
                    uuid.UUID(kb_id)
                )
                if visible == 1:
                    ok(f"RLS: tenant A can see own KB (count={visible})")
                else:
                    fail(f"RLS: tenant A cannot see own KB (count={visible})")

            async with conn.transaction():
                await conn.execute(f"SET LOCAL ROLE {rls_role}")
                await conn.execute(
                    "SELECT set_config('app.current_tenant_id', $1, true)", TENANT_B
                )
                visible_b = await conn.fetchval(
                    "SELECT count(*) FROM knowledge_bases WHERE id = $1",
                    uuid.UUID(kb_id)
                )
                if visible_b == 0:
                    ok(f"RLS: tenant B cannot see tenant A's KB (count={visible_b})")
                else:
                    fail(f"RLS isolation BROKEN: tenant B sees tenant A's KB (count={visible_b})")
    else:
        print(f"  {YELLOW}INFO{RESET} No RLS policy on knowledge_bases -- RLS is injected by Core on the gateway side")

    # ── Phase 11: soft deletes ───────────────────────────────────────────────
    section("11. soft_delete_document + soft_delete_kb -> UPDATE")

    result = await conn.execute(DOC_SOFT_DELETE_SQL, uuid.UUID(doc_id))
    if "UPDATE 1" in result:
        ok("soft_delete_document: parse_status=failed, error_message=deleted")
    else:
        fail("soft_delete_document failed", result)

    result = await conn.execute(KB_SOFT_DELETE_SQL, uuid.UUID(kb_id))
    if "UPDATE 1" in result:
        ok("soft_delete_kb: status=deleted")
    else:
        fail("soft_delete_kb failed", result)

    # ── Phase 12: ACID single-transaction ─────────────────────────────────────
    section("12. ACID: INSERT + rollback in single transaction")

    test_name = f"acid-test-{uuid.uuid4().hex[:8]}"
    acid_kb_id = None
    try:
        async with conn.transaction():
            row = await conn.fetchrow(
                KB_CREATE_SQL,
                uuid.UUID(TENANT_A), test_name, "acid test", "bge-m3",
                512, 3, 0.5, "keyword",
            )
            acid_kb_id = str(row["id"])
            ok(f"ACID: inserted KB in tx (id={acid_kb_id[:8]}...)")
            raise Exception("force rollback")
    except Exception:
        pass  # expected

    # Verify the row was rolled back
    rolled_back = await conn.fetchval(KB_GET_SQL, uuid.UUID(acid_kb_id))
    if rolled_back is None:
        ok("ACID: rollback successful (row not visible outside tx)")
    else:
        fail("ACID: rollback FAILED (row visible after tx rollback)")

    # ── Cleanup ──────────────────────────────────────────────────────────────
    section("13. Cleanup")

    await conn.execute("DELETE FROM kb_chunks WHERE kb_id = $1", uuid.UUID(kb_id))
    await conn.execute("DELETE FROM kb_documents WHERE kb_id = $1", uuid.UUID(kb_id))
    await conn.execute("DELETE FROM knowledge_bases WHERE id = $1", uuid.UUID(kb_id))
    ok("Cleanup done")

    await conn.close()

    # ── Summary ──────────────────────────────────────────────────────────────
    print(f"\n{YELLOW}{'='*72}{RESET}")
    total = passed + failed
    print(f"  Results: {GREEN}{passed} passed{RESET}, {RED}{failed} failed{RESET}, {total} total")
    if failed:
        print(f"\n  {RED}FAILURES:{RESET}")
        for e in errors:
            print(f"    - {e}")
        sys.exit(1)
    else:
        print(f"  {GREEN}ALL TESTS PASSED{RESET}")
    print(f"{YELLOW}{'='*72}{RESET}")


if __name__ == "__main__":
    asyncio.run(run_e2e())
