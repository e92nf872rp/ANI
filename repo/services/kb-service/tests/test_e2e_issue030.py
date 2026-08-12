"""End-to-end test for issue-030: kb-service data plane migration.

Starts a local ani-gateway (dev mode) connecting to the server's PostgreSQL,
then exercises the issue-030 code paths through the real data plane:

1. Gateway /data/query endpoint reachable (health + data plane configured)
2. CoreClient.data_query → gateway → PostgreSQL (SELECT FROM knowledge_bases)
3. message_repo.create_session_and_message CTE fold (INSERT kb_sessions + kb_messages)
4. message_repo.insert_message (INSERT kb_messages)
5. async_task_repo.create_task + find_by_idempotency_key (INSERT + SELECT async_tasks)
6. outbox_repo.list_undispatched + mark_dispatched (role="service" cross-tenant)
7. NotifyDocumentUploaded 3-table CTE fold (UPDATE kb_documents + INSERT async_tasks + INSERT outbox_events)
8. Query 2-table CTE fold (INSERT kb_sessions + kb_messages)

Usage:
    cd repo/services/kb-service
    python tests/test_e2e_issue030.py

Prerequisites:
    - ani-gateway.exe built at repo/bin/ani-gateway.exe
    - SSH access to 10.10.1.66 (ANI1) for PostgreSQL NodePort 30945
    - Server PostgreSQL has the ANI schema migrated (migrations/20260501_001_init_schema.sql etc.)
"""
import asyncio
import json
import os
import subprocess
import sys
import time
import uuid

import httpx

# ── Configuration ────────────────────────────────────────────────────────────

SERVER_IP = "10.10.1.66"
PG_NODEPORT = 30945
NATS_NODEPORT = 31062
REDIS_NODEPORT = 30453
MINIO_NODEPORT = 30900
MILVUS_NODEPORT = 31930

PG_URL = f"postgres://ani:ani_dev_password@{SERVER_IP}:{PG_NODEPORT}/ani?sslmode=disable"
REDIS_URL = f"redis://:ani_dev_password@{SERVER_IP}:{REDIS_NODEPORT}/0"
NATS_URL = f"nats://{SERVER_IP}:{NATS_NODEPORT}"
MINIO_ENDPOINT = f"{SERVER_IP}:{MINIO_NODEPORT}"
MILVUS_ADDR = f"{SERVER_IP}:{MILVUS_NODEPORT}"

GATEWAY_LISTEN = ":8080"
GATEWAY_URL = "http://127.0.0.1:8080"
CORE_API_URL = f"{GATEWAY_URL}/api/v1"

# Test tenant — a valid UUID that exists in the DB or we create.
TEST_TENANT_ID = os.environ.get("E2E_TENANT_ID", "00000000-0000-0000-0000-000000000001")

# Dev auth headers for the gateway.
DEV_HEADERS = {
    "X-Dev-Scope": "platform",
    "X-Dev-Tenant-ID": TEST_TENANT_ID,
    "Content-Type": "application/json",
}

GATEWAY_BIN = os.path.join(
    os.path.dirname(__file__), "..", "..", "bin", "ani-gateway.exe"
)

# ── Helpers ──────────────────────────────────────────────────────────────────


def _green(s):
    return f"\033[92m{s}\033[0m"


def _red(s):
    return f"\033[91m{s}\033[0m"


def _yellow(s):
    return f"\033[93m{s}\033[0m"


def _bold(s):
    return f"\033[1m{s}\033[0m"


passed = 0
failed = 0
skipped = 0


def report(name, ok, detail=""):
    global passed, failed
    if ok:
        passed += 1
        print(f"  {_green('PASS')} {name}" + (f" — {detail}" if detail else ""))
    else:
        failed += 1
        print(f"  {_red('FAIL')} {name}" + (f" — {detail}" if detail else ""))


def skip(name, reason=""):
    global skipped
    skipped += 1
    print(f"  {_yellow('SKIP')} {name}" + (f" — {reason}" if reason else ""))


# ── Gateway process management ──────────────────────────────────────────────


def start_gateway():
    """Start the local ani-gateway in dev mode, or use an already-running one."""
    # Check if gateway is already running
    try:
        r = httpx.get(f"{GATEWAY_URL}/healthz", timeout=2)
        if r.status_code == 200:
            print(f"  Gateway already running on {GATEWAY_URL}")
            return None  # sentinel: don't stop it
    except Exception:
        pass

    env = os.environ.copy()
    env.update(
        {
            "ANI_AUTH_MODE": "dev",
            "GATEWAY_LISTEN_ADDR": GATEWAY_LISTEN,
            "DATABASE_URL": PG_URL,
            "REDIS_URL": REDIS_URL,
            # Disable K8s/workload providers for local testing
            "WORKLOAD_PROVIDER": "",
            "WORKLOAD_LIFECYCLE_PROVIDER": "",
            "WORKLOAD_OPS_PROVIDER": "",
            # Object store (server MinIO)
            "OBJECT_STORE_PROVIDER": "minio",
            "OBJECT_STORE_ENDPOINT": f"http://{SERVER_IP}:{MINIO_NODEPORT}",
            "OBJECT_STORE_PUBLIC_ENDPOINT": f"http://{SERVER_IP}:{MINIO_NODEPORT}",
            "OBJECT_STORE_ACCESS_KEY_ID": "ani-s05-minio",
            "OBJECT_STORE_SECRET_ACCESS_KEY": "F36UCbnRR-bY9Upv8uuammuBwkHFlTYABiXCbtMCmlc",
            "OBJECT_STORE_REGION": "us-east-1",
            "OBJECT_STORE_SECURE": "false",
            "OBJECT_STORE_BUCKET_PREFIX": "ani-s13-",
            # Vector store (server Milvus)
            "VECTOR_STORE_PROVIDER": "milvus",
            "VECTOR_STORE_ENDPOINT": f"http://{SERVER_IP}:{MILVUS_NODEPORT}",
            "VECTOR_STORE_COLLECTION_PREFIX": "ani_s13_",
        }
    )
    proc = subprocess.Popen(
        [GATEWAY_BIN],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        cwd=os.path.dirname(GATEWAY_BIN),
    )
    # Wait for gateway to be ready
    for i in range(30):
        try:
            r = httpx.get(f"{GATEWAY_URL}/healthz", timeout=2)
            if r.status_code == 200:
                print(f"  Gateway started on {GATEWAY_URL}")
                return proc
        except Exception:
            pass
        time.sleep(1)
    # Print gateway logs if it failed to start
    proc.terminate()
    out, _ = proc.communicate(timeout=5)
    print(f"  Gateway failed to start. Logs:\n{out.decode()[-3000:]}")
    return False  # sentinel: failed


def stop_gateway(proc):
    if proc:
        proc.terminate()
        try:
            proc.communicate(timeout=5)
        except Exception:
            proc.kill()


# ── Data plane client ───────────────────────────────────────────────────────


async def data_query(client, sql, params=None, role="tenant"):
    """Call the gateway /data/query endpoint."""
    body = {"sql": sql}
    if params:
        body["params"] = params
    if role:
        body["role"] = role
    headers = dict(DEV_HEADERS)
    if role == "service":
        headers["X-Dev-Scope"] = "platform"
    r = await client.post(
        f"{CORE_API_URL}/data/query", json=body, headers=headers, timeout=30
    )
    if r.status_code != 200:
        raise RuntimeError(f"data_query HTTP {r.status_code}: {r.text}")
    return r.json()


# ── Tests ────────────────────────────────────────────────────────────────────


async def test_gateway_health(client):
    """Test 1: Gateway is healthy and data plane is configured."""
    r = await client.get(f"{GATEWAY_URL}/healthz", timeout=5)
    report("Gateway /healthz", r.status_code == 200, f"status={r.status_code}")

    r = await client.get(f"{GATEWAY_URL}/readyz", timeout=5)
    report("Gateway /readyz", r.status_code == 200, f"status={r.status_code}")


async def test_data_plane_select(client):
    """Test 2: CoreClient.data_query → gateway → PostgreSQL SELECT."""
    try:
        result = await data_query(
            client,
            "SELECT id, name FROM knowledge_bases LIMIT $1",
            params=[5],
            role="tenant",
        )
        rows = result.get("rows", [])
        report(
            "data_query SELECT knowledge_bases",
            True,
            f"rows={len(rows)}",
        )
    except Exception as e:
        report("data_query SELECT knowledge_bases", False, str(e))


async def test_create_session_and_message_cte(client):
    """Test 3: message_repo.create_session_and_message CTE fold.

    Tests the 2-table atomic fold: INSERT kb_sessions (ON CONFLICT DO NOTHING)
    + INSERT kb_messages in a single SQL statement.
    """
    session_id = str(uuid.uuid4())
    kb_id = str(uuid.uuid4())
    try:
        # Create a KB first (FK constraint)
        await data_query(
            client,
            "INSERT INTO knowledge_bases (id, tenant_id, name) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
            params=[kb_id, TEST_TENANT_ID, f"e2e-cte-{uuid.uuid4().hex[:8]}"],
            role="tenant",
        )
        chunks_json = json.dumps([{"chunk_id": "c1", "score": 0.9}])
        sql = """
            WITH sess AS (
                INSERT INTO kb_sessions (id, kb_id, tenant_id, user_id, title)
                VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5)
                ON CONFLICT (id) DO NOTHING RETURNING id
            ),
            effective_session AS (
                SELECT COALESCE((SELECT id FROM sess), $1::uuid) AS session_id
            )
            INSERT INTO kb_messages (session_id, tenant_id, role, content,
                 source_chunks, input_tokens, output_tokens, duration_ms)
            SELECT es.session_id, $3, $6, $7, $8, $9, $10, $11
            FROM effective_session es
            RETURNING id, session_id, tenant_id, role, content
        """
        result = await data_query(
            client,
            sql,
            params=[
                session_id,
                kb_id,
                TEST_TENANT_ID,
                None,
                "e2e-test-session",
                "user",
                "Hello from E2E test",
                chunks_json,
                10,
                5,
                100,
            ],
            role="tenant",
        )
        rows = result.get("rows", [])
        report(
            "create_session_and_message CTE fold (2-table atomic)",
            len(rows) == 1,
            f"rows={len(rows)}, session_id={rows[0].get('session_id', 'N/A')[:8] if rows else 'N/A'}",
        )

        # Verify: query back the message
        verify = await data_query(
            client,
            "SELECT id, role, content FROM kb_messages WHERE session_id = $1",
            params=[session_id],
            role="tenant",
        )
        verify_rows = verify.get("rows", [])
        report(
            "verify kb_messages written",
            len(verify_rows) == 1 and verify_rows[0]["content"] == "Hello from E2E test",
            f"rows={len(verify_rows)}",
        )
    except Exception as e:
        report("create_session_and_message CTE fold", False, str(e))


async def test_insert_message(client):
    """Test 4: message_repo.insert_message (single INSERT)."""
    session_id = str(uuid.uuid4())
    kb_id = str(uuid.uuid4())
    # First create a KB (FK constraint on kb_sessions.kb_id → knowledge_bases.id)
    try:
        await data_query(
            client,
            "INSERT INTO knowledge_bases (id, tenant_id, name) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
            params=[kb_id, TEST_TENANT_ID, f"e2e-insert-{uuid.uuid4().hex[:8]}"],
            role="tenant",
        )
        # Create session
        await data_query(
            client,
            "INSERT INTO kb_sessions (id, kb_id, tenant_id, title) VALUES ($1, $2, $3, $4) ON CONFLICT (id) DO NOTHING",
            params=[session_id, kb_id, TEST_TENANT_ID, "e2e-insert-test"],
            role="tenant",
        )
        # Now insert a message
        sql = """
            INSERT INTO kb_messages (session_id, tenant_id, role, content,
                 input_tokens, output_tokens, duration_ms)
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            RETURNING id, session_id, role, content
        """
        result = await data_query(
            client,
            sql,
            params=[session_id, TEST_TENANT_ID, "assistant", "Hi from assistant", 5, 10, 50],
            role="tenant",
        )
        rows = result.get("rows", [])
        report(
            "insert_message (single INSERT)",
            len(rows) == 1 and rows[0]["role"] == "assistant",
            f"rows={len(rows)}",
        )
    except Exception as e:
        report("insert_message", False, str(e))


async def test_async_task_create_and_find(client):
    """Test 5: async_task_repo.create_task + find_by_idempotency_key."""
    idem_key = f"e2e-test-{uuid.uuid4()}"
    try:
        # Create task
        sql = """
            INSERT INTO async_tasks (tenant_id, idempotency_key, task_type, resource_type,
                resource_id, status, payload)
            VALUES ($1, $2, $3, $4, $5, $6, $7)
            RETURNING id, tenant_id, idempotency_key, task_type, status
        """
        payload_json = json.dumps({"doc_id": "test-doc", "kb_id": "test-kb"})
        result = await data_query(
            client,
            sql,
            params=[TEST_TENANT_ID, idem_key, "kb.parse", "kb_document", str(uuid.uuid4()), "pending", payload_json],
            role="tenant",
        )
        rows = result.get("rows", [])
        report(
            "create_task (INSERT async_tasks)",
            len(rows) == 1 and rows[0]["idempotency_key"] == idem_key,
            f"task_id={rows[0]['id'][:8] if rows else 'N/A'}",
        )

        # Find by idempotency key
        find_sql = """
            SELECT id, task_type, status FROM async_tasks
            WHERE tenant_id = $1 AND idempotency_key = $2
        """
        find_result = await data_query(
            client,
            find_sql,
            params=[TEST_TENANT_ID, idem_key],
            role="tenant",
        )
        find_rows = find_result.get("rows", [])
        report(
            "find_by_idempotency_key (SELECT async_tasks)",
            len(find_rows) == 1 and find_rows[0]["status"] == "pending",
            f"rows={len(find_rows)}",
        )
    except Exception as e:
        report("async_task create+find", False, str(e))


async def test_outbox_service_role(client):
    """Test 6: outbox_repo.list_undispatched + mark_dispatched (role=service).

    Tests cross-tenant access via role="service".
    """
    try:
        # List undispatched (cross-tenant, role=service)
        list_result = await data_query(
            client,
            "SELECT id, aggregate_type, aggregate_id, event_type, tenant_id, published FROM outbox_events WHERE published = FALSE ORDER BY created_at ASC LIMIT $1",
            params=[10],
            role="service",
        )
        list_rows = list_result.get("rows", [])
        report(
            "outbox list_undispatched (role=service, cross-tenant)",
            True,  # Success if no exception — empty list is valid
            f"undispatched_count={len(list_rows)}",
        )

        # If there are undispatched events, mark one as dispatched
        if list_rows:
            event_id = list_rows[0]["id"]
            mark_result = await data_query(
                client,
                "UPDATE outbox_events SET published = TRUE, published_at = now() WHERE id = $1",
                params=[event_id],
                role="service",
            )
            rowcount = mark_result.get("rowcount", 0)
            report(
                "outbox mark_dispatched (role=service)",
                rowcount == 1,
                f"rowcount={rowcount}, event_id={event_id}",
            )
        else:
            skip("outbox mark_dispatched (no undispatched events)")
    except Exception as e:
        report("outbox service_role operations", False, str(e))


async def test_notify_fold_3_tables(client):
    """Test 7: NotifyDocumentUploaded 3-table CTE fold.

    Tests: UPDATE kb_documents + INSERT async_tasks (ON CONFLICT) + INSERT outbox_events
    in a single atomic SQL statement.
    """
    # First, we need a KB and a document. Let's create a minimal KB.
    kb_id = str(uuid.uuid4())
    doc_id = str(uuid.uuid4())
    idem_key = f"kb.parse:{TEST_TENANT_ID}:{kb_id}:{doc_id}"

    try:
        # Create KB
        await data_query(
            client,
            "INSERT INTO knowledge_bases (id, tenant_id, name) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
            params=[kb_id, TEST_TENANT_ID, f"e2e-notify-{uuid.uuid4().hex[:8]}"],
            role="tenant",
        )

        # Create document with correct schema columns
        await data_query(
            client,
            "INSERT INTO kb_documents (id, kb_id, tenant_id, file_name, file_type, file_size_bytes, storage_path, checksum_sha256, parse_status) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (id) DO NOTHING",
            params=[doc_id, kb_id, TEST_TENANT_ID, "test.pdf", "pdf", 1024, "e2e-test/test.pdf", "sha256:dummy", "pending"],
            role="tenant",
        )

        # Now run the 3-table CTE fold (the actual NotifyDocumentUploaded SQL from grpc_server.py)
        payload_json = json.dumps(
            {"doc_id": doc_id, "kb_id": kb_id, "object_id": None}, default=str
        )
        outbox_payload_json = json.dumps(
            {
                "doc_id": doc_id,
                "kb_id": kb_id,
                "storage_path": "e2e-test/test.pdf",
                "tenant_id": TEST_TENANT_ID,
                "file_name": "test.pdf",
                "object_id": None,
                "chunk_size": None,
            },
            default=str,
        )
        fold_sql = """
            WITH doc_upd AS (
                UPDATE kb_documents
                   SET parse_status = 'pending', error_message = NULL
                 WHERE id = $1
             RETURNING id, object_id
            ),
            kb_cfg AS (
                SELECT chunk_size FROM knowledge_bases WHERE id = $2
            ),
            existing AS (
                SELECT id, status FROM async_tasks
                 WHERE tenant_id = $3 AND idempotency_key = $4
            ),
            task AS (
                INSERT INTO async_tasks
                    (tenant_id, idempotency_key, task_type, resource_type,
                     resource_id, status, payload)
                SELECT $3, $4, 'kb.parse', 'kb_document', $1, 'pending',
                       jsonb_set(
                         jsonb_set($5::jsonb, '{object_id}',
                                   COALESCE(to_jsonb(doc_upd.object_id), 'null'::jsonb)),
                         '{doc_id}', to_jsonb($1::text))
                  FROM doc_upd
                ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
                RETURNING id
            ),
            obx AS (
                INSERT INTO outbox_events
                    (aggregate_type, aggregate_id, event_type, tenant_id,
                     payload)
                SELECT 'kb_documents', $1::uuid, 'kb.parse', $3,
                       jsonb_set(
                         jsonb_set(
                           jsonb_set($6::jsonb, '{object_id}',
                                     COALESCE(to_jsonb(doc_upd.object_id), 'null'::jsonb)),
                           '{chunk_size}',
                           COALESCE(to_jsonb(kb_cfg.chunk_size), 'null'::jsonb)),
                         '{storage_path}', to_jsonb($7::text))
                  FROM doc_upd, kb_cfg
                  WHERE NOT EXISTS (SELECT 1 FROM existing)
             RETURNING id
            )
            SELECT t.id AS task_id, 'pending' AS status
              FROM task t
            UNION ALL
            SELECT e.id AS task_id, e.status AS status
              FROM existing e
            WHERE NOT EXISTS (SELECT 1 FROM task)
        """
        result = await data_query(
            client,
            fold_sql,
            params=[doc_id, kb_id, TEST_TENANT_ID, idem_key, payload_json, outbox_payload_json, "e2e-test/test.pdf"],
            role="tenant",
        )
        rows = result.get("rows", [])
        report(
            "NotifyDocumentUploaded 3-table CTE fold (UPDATE + INSERT + INSERT atomic)",
            len(rows) == 1,
            f"task_id={rows[0].get('task_id', 'N/A')[:8] if rows else 'N/A'}, status={rows[0].get('status', 'N/A') if rows else 'N/A'}",
        )

        # Verify: replay returns the same task (idempotent)
        replay_result = await data_query(
            client,
            fold_sql,
            params=[doc_id, kb_id, TEST_TENANT_ID, idem_key, payload_json, outbox_payload_json, "e2e-test/test.pdf"],
            role="tenant",
        )
        replay_rows = replay_result.get("rows", [])
        report(
            "NotifyDocumentUploaded idempotent replay (same task_id)",
            len(replay_rows) == 1 and rows[0]["task_id"] == replay_rows[0]["task_id"],
            f"original={rows[0].get('task_id', 'N/A')[:8] if rows else 'N/A'}, replay={replay_rows[0].get('task_id', 'N/A')[:8] if replay_rows else 'N/A'}",
        )

        # Verify: outbox event was created (not on replay)
        obx_check = await data_query(
            client,
            "SELECT count(*) AS cnt FROM outbox_events WHERE aggregate_id = $1 AND event_type = 'kb.parse'",
            params=[doc_id],
            role="service",
        )
        obx_count = obx_check.get("rows", [{}])[0].get("cnt", 0)
        report(
            "outbox event created (exactly 1, not duplicated on replay)",
            obx_count == 1,
            f"outbox_count={obx_count}",
        )

    except Exception as e:
        report("NotifyDocumentUploaded 3-table fold", False, str(e))


async def test_query_fold_2_tables(client):
    """Test 8: Query user message 2-table CTE fold.

    Tests: INSERT kb_sessions (ON CONFLICT DO NOTHING) + INSERT kb_messages
    in a single atomic SQL statement.
    """
    session_id = str(uuid.uuid4())
    kb_id = str(uuid.uuid4())
    try:
        # Create a KB first (FK constraint)
        await data_query(
            client,
            "INSERT INTO knowledge_bases (id, tenant_id, name) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
            params=[kb_id, TEST_TENANT_ID, f"e2e-query-{uuid.uuid4().hex[:8]}"],
            role="tenant",
        )

        # Run the 2-table CTE fold (the actual create_session_and_message SQL)
        chunks_json = json.dumps([{"chunk_id": "c1", "score": 0.85}])
        sql = """
            WITH sess AS (
                INSERT INTO kb_sessions (id, kb_id, tenant_id, user_id, title)
                VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5)
                ON CONFLICT (id) DO NOTHING RETURNING id
            ),
            effective_session AS (
                SELECT COALESCE((SELECT id FROM sess), $1::uuid) AS session_id
            )
            INSERT INTO kb_messages (session_id, tenant_id, role, content,
                 source_chunks, input_tokens, output_tokens, duration_ms)
            SELECT es.session_id, $3, $6, $7, $8, $9, $10, $11
            FROM effective_session es
            RETURNING id, session_id, tenant_id, role, content
        """
        result = await data_query(
            client,
            sql,
            params=[session_id, kb_id, TEST_TENANT_ID, None, "e2e-query-session", "user", "What is ANI?", chunks_json, 15, 8, 200],
            role="tenant",
        )
        rows = result.get("rows", [])
        report(
            "Query 2-table CTE fold (INSERT kb_sessions + kb_messages atomic)",
            len(rows) == 1,
            f"msg_id={rows[0].get('id', 'N/A')[:8] if rows else 'N/A'}, session={rows[0].get('session_id', 'N/A')[:8] if rows else 'N/A'}",
        )

        # Verify: session exists
        sess_check = await data_query(
            client,
            "SELECT id FROM kb_sessions WHERE id = $1",
            params=[session_id],
            role="tenant",
        )
        report(
            "verify kb_sessions created",
            len(sess_check.get("rows", [])) == 1,
            f"rows={len(sess_check.get('rows', []))}",
        )

        # Verify: message exists
        msg_check = await data_query(
            client,
            "SELECT id, role, content FROM kb_messages WHERE session_id = $1",
            params=[session_id],
            role="tenant",
        )
        msg_rows = msg_check.get("rows", [])
        report(
            "verify kb_messages created",
            len(msg_rows) == 1 and msg_rows[0]["content"] == "What is ANI?",
            f"rows={len(msg_rows)}",
        )
    except Exception as e:
        report("Query 2-table CTE fold", False, str(e))


async def test_on_conflict_column_match(client):
    """Test 9: Verify ON CONFLICT (tenant_id, idempotency_key) matches the UNIQUE constraint.

    This is the critical fix from the second code review — ensures the ON CONFLICT
    clause matches the actual schema constraint UNIQUE (tenant_id, idempotency_key).
    """
    idem_key = f"e2e-conflict-test-{uuid.uuid4()}"
    try:
        # First INSERT should succeed
        sql = """
            INSERT INTO async_tasks (tenant_id, idempotency_key, task_type, resource_type,
                resource_id, status, payload)
            VALUES ($1, $2, 'kb.parse', 'kb_document', $3, 'pending', $4)
            ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
            RETURNING id
        """
        payload = json.dumps({"test": True})
        result1 = await data_query(
            client,
            sql,
            params=[TEST_TENANT_ID, idem_key, str(uuid.uuid4()), payload],
            role="tenant",
        )
        rows1 = result1.get("rows", [])

        # Second INSERT with same idempotency_key should hit ON CONFLICT DO NOTHING
        result2 = await data_query(
            client,
            sql,
            params=[TEST_TENANT_ID, idem_key, str(uuid.uuid4()), payload],
            role="tenant",
        )
        rows2 = result2.get("rows", [])

        report(
            "ON CONFLICT (tenant_id, idempotency_key) matches UNIQUE constraint",
            len(rows1) == 1 and len(rows2) == 0,
            f"first_insert={len(rows1)}, second_insert={len(rows2)} (conflict → DO NOTHING)",
        )
    except Exception as e:
        report("ON CONFLICT column match", False, str(e))


# ── Main ────────────────────────────────────────────────────────────────────


async def main():
    print()
    print("=" * 72)
    print(_bold("  issue-030 End-to-End Test — kb-service data plane migration"))
    print("=" * 72)
    print()

    # ── Start gateway ──
    print(_bold("  [1/4] Starting local ani-gateway (dev mode)..."))
    proc = start_gateway()
    if proc is False:
        print(_red("  FATAL: Gateway failed to start. Aborting."))
        return 1
    print()

    try:
        async with httpx.AsyncClient() as client:
            # ── Run tests ──
            print(_bold("  [2/4] Gateway health checks"))
            await test_gateway_health(client)
            print()

            print(_bold("  [3/4] Data plane SQL tests (issue-030 code paths)"))
            print()
            print(_bold("  ── Basic data plane ──"))
            await test_data_plane_select(client)
            print()

            print(_bold("  ── message.py (CTE fold + insert) ──"))
            await test_create_session_and_message_cte(client)
            await test_insert_message(client)
            print()

            print(_bold("  ── async_task.py (create + find) ──"))
            await test_async_task_create_and_find(client)
            print()

            print(_bold("  ── outbox.py (role=service cross-tenant) ──"))
            await test_outbox_service_role(client)
            print()

            print(_bold("  ── ON CONFLICT constraint match (critical fix) ──"))
            await test_on_conflict_column_match(client)
            print()

            print(_bold("  ── grpc_server.py NotifyDocumentUploaded (3-table fold) ──"))
            await test_notify_fold_3_tables(client)
            print()

            print(_bold("  ── grpc_server.py Query (2-table fold) ──"))
            await test_query_fold_2_tables(client)
            print()

    finally:
        # ── Stop gateway ──
        print(_bold("  [4/4] Stopping gateway..."))
        if proc:
            stop_gateway(proc)
        else:
            print("  (gateway was already running, leaving it)")

    # ── Summary ──
    print()
    print("=" * 72)
    total = passed + failed + skipped
    print(f"  {_bold('Summary')}: {_green(f'{passed} passed')}, {_red(f'{failed} failed')}, {_yellow(f'{skipped} skipped')} / {total} total")
    print("=" * 72)
    print()

    return 0 if failed == 0 else 1


if __name__ == "__main__":
    rc = asyncio.run(main())
    sys.exit(rc)
